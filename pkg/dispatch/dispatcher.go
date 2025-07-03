package dispatch

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

type Agent interface {
	ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error)
	GetID() string
	Shutdown(ctx context.Context) error
}

type Dispatcher struct {
	agents      map[string]Agent
	rateLimiter *limiter.Limiter
	eventLog    *eventlog.Writer
	logger      *logx.Logger
	config      *config.Config
	inputChan   chan *proto.AgentMsg
	shutdown    chan struct{}
	wg          sync.WaitGroup
	mu          sync.RWMutex
	running     bool

	// Pull-based queues
	architectQueue  []*proto.AgentMsg // Questions for architect
	coderQueue      []*proto.AgentMsg // Answers/feedback for coders
	sharedWorkQueue []*proto.AgentMsg // Ready stories for any coder
	queueMutex      sync.RWMutex      // Protects all queues

	// Channel-based notifications for architect
	idleAgentCh    chan string     // Notifies architect when agents become idle
	architectID    string          // ID of the architect to notify
	notificationMu sync.RWMutex    // Protects notification channels
	busyAgents     map[string]bool // Track which agents are processing work
	busyMu         sync.RWMutex    // Protects busy agents map
}

type DispatchResult struct {
	Message *proto.AgentMsg
	Error   error
}

func NewDispatcher(cfg *config.Config, rateLimiter *limiter.Limiter, eventLog *eventlog.Writer) (*Dispatcher, error) {
	return &Dispatcher{
		agents:      make(map[string]Agent),
		rateLimiter: rateLimiter,
		eventLog:    eventLog,
		logger:      logx.NewLogger("dispatcher"),
		config:      cfg,
		inputChan:   make(chan *proto.AgentMsg, 100), // Buffered channel for message queue
		shutdown:    make(chan struct{}),
		running:     false,
		idleAgentCh: make(chan string, 10), // Buffered channel for idle agent notifications
		busyAgents:  make(map[string]bool), // Initialize busy agents map
	}, nil
}

func (d *Dispatcher) RegisterAgent(agent Agent) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	agentID := agent.GetID()
	if _, exists := d.agents[agentID]; exists {
		return fmt.Errorf("agent %s already registered", agentID)
	}

	d.agents[agentID] = agent
	d.logger.Info("Registered agent: %s", agentID)

	return nil
}

func (d *Dispatcher) UnregisterAgent(agentID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.agents[agentID]; !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	delete(d.agents, agentID)
	d.logger.Info("Unregistered agent: %s", agentID)

	return nil
}

func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("dispatcher is already running")
	}
	d.running = true
	d.mu.Unlock()

	d.logger.Info("Starting dispatcher")

	// Start message processing worker
	d.wg.Add(1)
	go d.messageProcessor(ctx)

	return nil
}

func (d *Dispatcher) Stop(ctx context.Context) error {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return nil
	}
	d.running = false
	d.mu.Unlock()

	d.logger.Info("Stopping dispatcher")

	// Signal shutdown
	close(d.shutdown)

	// Close idle agent channel for graceful shutdown
	d.CloseIdleChannel()
	// Wait for workers to finish
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.logger.Info("Dispatcher stopped successfully")
		return nil
	case <-ctx.Done():
		d.logger.Warn("Dispatcher stop timed out")
		return ctx.Err()
	}
}

func (d *Dispatcher) DispatchMessage(msg *proto.AgentMsg) error {
	d.mu.RLock()
	running := d.running
	d.mu.RUnlock()

	if !running {
		return fmt.Errorf("dispatcher is not running")
	}

	// Note: Event logging will happen after name resolution in processMessage
	// to ensure the logged message reflects the actual target agent

	select {
	case d.inputChan <- msg:
		d.logger.Debug("Queued message %s: %s → %s", msg.ID, msg.FromAgent, msg.ToAgent)
		return nil
	default:
		return fmt.Errorf("message queue is full")
	}
}

func (d *Dispatcher) messageProcessor(ctx context.Context) {
	defer d.wg.Done()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Message processor stopped by context")
			return
		case <-d.shutdown:
			d.logger.Info("Message processor stopped by shutdown signal")
			return
		case msg := <-d.inputChan:
			d.processMessage(ctx, msg)
		}
	}
}

func (d *Dispatcher) processMessage(ctx context.Context, msg *proto.AgentMsg) {
	d.logger.Debug("Processing message %s: %s → %s (%s)", msg.ID, msg.FromAgent, msg.ToAgent, msg.Type)

	// Log message
	if err := d.eventLog.WriteMessage(msg); err != nil {
		d.logger.Error("Failed to log incoming message: %v", err)
		// Continue processing even if logging fails
	}

	// Route messages to appropriate queues based on type
	d.queueMutex.Lock()
	defer d.queueMutex.Unlock()

	switch msg.Type {
	case proto.MsgTypeTASK:
		// TASK messages go to shared work queue for coders to pull
		d.sharedWorkQueue = append(d.sharedWorkQueue, msg)
		d.logger.Debug("Queued TASK %s to shared work queue (queue size: %d)", msg.ID, len(d.sharedWorkQueue))

	case proto.MsgTypeQUESTION:
		// QUESTION messages go to architect queue for architect to pull
		d.architectQueue = append(d.architectQueue, msg)
		d.logger.Debug("Queued QUESTION %s to architect queue (queue size: %d)", msg.ID, len(d.architectQueue))

	case proto.MsgTypeREQUEST:
		// REQUEST messages (approval requests) go to architect queue for architect to pull
		d.architectQueue = append(d.architectQueue, msg)
		d.logger.Debug("Queued REQUEST %s to architect queue (queue size: %d)", msg.ID, len(d.architectQueue))

	case proto.MsgTypeRESULT:
		// Check for idle agent notifications before queuing
		d.NotifyArchitectOnResult(msg)
		// RESULT messages go to coder queue for specific coder to pull
		d.coderQueue = append(d.coderQueue, msg)
		d.logger.Debug("Queued RESULT %s to coder queue (queue size: %d)", msg.ID, len(d.coderQueue))

	case proto.MsgTypeANSWER:
		// ANSWER messages (information responses) go to coder queue for specific coder to pull
		d.coderQueue = append(d.coderQueue, msg)
		d.logger.Debug("Queued ANSWER %s to coder queue (queue size: %d)", msg.ID, len(d.coderQueue))

	default:
		// Other message types (ERROR, SHUTDOWN, etc.) still processed immediately
		d.queueMutex.Unlock() // Unlock early for immediate processing

		// Resolve logical agent name to actual agent ID
		resolvedToAgent := d.resolveAgentName(msg.ToAgent)
		if resolvedToAgent != msg.ToAgent {
			d.logger.Debug("Resolved logical name %s to %s", msg.ToAgent, resolvedToAgent)
			msg.ToAgent = resolvedToAgent
		}

		// Find target agent
		d.mu.RLock()
		targetAgent, exists := d.agents[msg.ToAgent]
		d.mu.RUnlock()

		if !exists {
			d.sendErrorResponse(msg, fmt.Errorf("target agent %s not found", msg.ToAgent))
			return
		}

		// Process immediately for non-TASK messages
		response := d.processWithRetry(ctx, msg, targetAgent)

		// If there's a response, route it to appropriate queue
		if response.Message != nil {
			d.sendResponse(response.Message)
		} else if response.Error != nil {
			d.sendErrorResponse(msg, response.Error)
		}

		d.queueMutex.Lock() // Re-lock for defer unlock
	}
}

func (d *Dispatcher) processWithRetry(ctx context.Context, msg *proto.AgentMsg, agent Agent) *DispatchResult {
	maxRetries := d.config.MaxRetryAttempts
	backoffMultiplier := d.config.RetryBackoffMultiplier

	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Extract model name from agent logID (format: "model:id")
		modelName := msg.ToAgent
		if parts := strings.Split(msg.ToAgent, ":"); len(parts) >= 2 {
			modelName = parts[0]
		}

		// Check rate limiting before each attempt
		if err := d.checkRateLimit(modelName); err != nil {
			d.logger.Warn("Rate limit exceeded for %s (model %s): %v", msg.ToAgent, modelName, err)
			return &DispatchResult{Error: err}
		}

		// Create retry message
		retryMsg := msg.Clone()
		retryMsg.RetryCount = attempt

		// Process message
		d.logger.Debug("Attempt %d/%d for message %s", attempt+1, maxRetries+1, msg.ID)

		response, err := agent.ProcessMessage(ctx, retryMsg)

		// Release agent slot after processing attempt
		d.rateLimiter.ReleaseAgent(modelName)

		if err == nil {
			// Success
			d.logger.Debug("Message %s processed successfully on attempt %d", msg.ID, attempt+1)

			return &DispatchResult{Message: response}
		}

		lastErr = err
		d.logger.Warn("Attempt %d failed for message %s: %v", attempt+1, msg.ID, err)

		// Don't wait after the last attempt
		if attempt < maxRetries {
			backoffDuration := time.Duration(math.Pow(backoffMultiplier, float64(attempt))) * time.Second
			d.logger.Debug("Waiting %v before retry", backoffDuration)

			select {
			case <-ctx.Done():
				return &DispatchResult{Error: ctx.Err()}
			case <-time.After(backoffDuration):
				// Continue to next attempt
			}
		}
	}

	d.logger.Error("All retry attempts failed for message %s: %v", msg.ID, lastErr)
	return &DispatchResult{Error: fmt.Errorf("failed after %d attempts: %w", maxRetries+1, lastErr)}
}

func (d *Dispatcher) checkRateLimit(agentModel string) error {
	// Reserve agent slot
	if err := d.rateLimiter.ReserveAgent(agentModel); err != nil {
		return err
	}

	// For now, we don't know token count in advance, so we'll reserve a default amount
	// In a real implementation, this might be estimated or configured
	defaultTokenReservation := 100

	if err := d.rateLimiter.Reserve(agentModel, defaultTokenReservation); err != nil {
		// Release the agent slot if token reservation fails
		d.rateLimiter.ReleaseAgent(agentModel)
		return err
	}

	return nil
}

func (d *Dispatcher) sendResponse(response *proto.AgentMsg) {
	// Route response to appropriate queue based on message type
	d.logger.Info("Routing response %s: %s → %s (%s)", response.ID, response.FromAgent, response.ToAgent, response.Type)

	if err := d.eventLog.WriteMessage(response); err != nil {
		d.logger.Error("Failed to log response message: %v", err)
	}

	// Check for architect notifications before routing
	d.NotifyArchitectOnResult(response)
	// Resolve logical agent name to actual agent ID
	resolvedToAgent := d.resolveAgentName(response.ToAgent)
	if resolvedToAgent != response.ToAgent {
		d.logger.Debug("Resolved logical name %s to %s", response.ToAgent, resolvedToAgent)
		response.ToAgent = resolvedToAgent
	}

	d.queueMutex.Lock()
	defer d.queueMutex.Unlock()

	switch response.Type {
	case proto.MsgTypeQUESTION:
		// Questions go to architect queue
		d.architectQueue = append(d.architectQueue, response)
		d.logger.Debug("Queued QUESTION %s for architect (queue size: %d)", response.ID, len(d.architectQueue))

	case proto.MsgTypeREQUEST:
		// Approval requests go to architect queue
		d.architectQueue = append(d.architectQueue, response)
		d.logger.Debug("Queued REQUEST %s for architect (queue size: %d)", response.ID, len(d.architectQueue))

	case proto.MsgTypeRESULT:
		// Approval responses go to coder queue
		d.coderQueue = append(d.coderQueue, response)
		d.logger.Debug("Queued RESULT %s for coders (queue size: %d)", response.ID, len(d.coderQueue))

	case proto.MsgTypeANSWER:
		// Information responses go to coder queue
		d.coderQueue = append(d.coderQueue, response)
		d.logger.Debug("Queued ANSWER %s for coders (queue size: %d)", response.ID, len(d.coderQueue))

	default:
		// Other types logged only
		d.logger.Debug("Response message %s of type %s logged only", response.ID, response.Type)
	}
}

func (d *Dispatcher) sendErrorResponse(originalMsg *proto.AgentMsg, err error) {
	errorMsg := proto.NewAgentMsg(proto.MsgTypeERROR, "dispatcher", originalMsg.FromAgent)
	errorMsg.ParentMsgID = originalMsg.ID
	errorMsg.SetPayload("error", err.Error())
	errorMsg.SetPayload("original_message_id", originalMsg.ID)
	errorMsg.SetMetadata("error_type", "processing_error")

	d.logger.Error("Sending error response for message %s: %v", originalMsg.ID, err)

	if logErr := d.eventLog.WriteMessage(errorMsg); logErr != nil {
		d.logger.Error("Failed to log error message: %v", logErr)
	}
}

// resolveAgentName resolves logical agent names to actual agent IDs
func (d *Dispatcher) resolveAgentName(logicalName string) string {
	// If already an exact agent ID, return as-is
	d.mu.RLock()
	if _, exists := d.agents[logicalName]; exists {
		d.mu.RUnlock()
		return logicalName
	}
	d.mu.RUnlock()

	// Map logical names to agent types
	targetType := ""
	switch logicalName {
	case "architect":
		targetType = "architect"
	case "coder":
		targetType = "coder"
	default:
		// Not a logical name, return as-is
		return logicalName
	}

	// Find first agent of the target type
	d.mu.RLock()
	defer d.mu.RUnlock()

	allAgents := d.config.GetAllAgents()
	for _, agentWithModel := range allAgents {
		if agentWithModel.Agent.Type == targetType {
			agentID := agentWithModel.Agent.GetLogID(agentWithModel.ModelName)
			if _, exists := d.agents[agentID]; exists {
				return agentID
			}
		}
	}

	// No agent found for this type, return original name (will cause "not found" error)
	return logicalName
}

func (d *Dispatcher) GetStats() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()

	agentList := make([]string, 0, len(d.agents))
	for agentID := range d.agents {
		agentList = append(agentList, agentID)
	}

	d.queueMutex.RLock()
	defer d.queueMutex.RUnlock()

	return map[string]any{
		"running":                d.running,
		"agents":                 agentList,
		"queue_length":           len(d.inputChan),
		"queue_capacity":         cap(d.inputChan),
		"architect_queue_size":   len(d.architectQueue),
		"coder_queue_size":       len(d.coderQueue),
		"shared_work_queue_size": len(d.sharedWorkQueue),
	}
}

// PullArchitectWork retrieves the next question for the architect to process
func (d *Dispatcher) PullArchitectWork() *proto.AgentMsg {
	d.queueMutex.Lock()
	defer d.queueMutex.Unlock()

	if len(d.architectQueue) == 0 {
		return nil
	}

	// Get first message from architect queue (FIFO)
	msg := d.architectQueue[0]
	d.architectQueue = d.architectQueue[1:]

	d.logger.Debug("Pulled QUESTION %s from architect queue (remaining: %d)", msg.ID, len(d.architectQueue))
	return msg
}

// PullCoderFeedback retrieves the next answer/feedback for a specific coder
func (d *Dispatcher) PullCoderFeedback(agentID string) *proto.AgentMsg {
	d.queueMutex.Lock()
	defer d.queueMutex.Unlock()

	// Look for messages targeted to this specific coder
	for i, msg := range d.coderQueue {
		if msg.ToAgent == agentID {
			// Remove message from queue
			d.coderQueue = append(d.coderQueue[:i], d.coderQueue[i+1:]...)
			d.logger.Debug("Pulled RESULT %s for coder %s (remaining: %d)", msg.ID, agentID, len(d.coderQueue))
			return msg
		}
	}

	return nil
}

// PullSharedWork retrieves the next available task from the shared work queue
func (d *Dispatcher) PullSharedWork() *proto.AgentMsg {
	d.queueMutex.Lock()
	defer d.queueMutex.Unlock()

	if len(d.sharedWorkQueue) == 0 {
		return nil
	}

	// Get first message from shared work queue (FIFO)
	msg := d.sharedWorkQueue[0]
	d.sharedWorkQueue = d.sharedWorkQueue[1:]

	// Mark agent as busy when pulling work
	d.busyMu.Lock()
	d.busyAgents[msg.ToAgent] = true
	d.busyMu.Unlock()

	d.logger.Debug("Pulled TASK %s from shared work queue (remaining: %d), marked agent %s as busy", msg.ID, len(d.sharedWorkQueue), msg.ToAgent)
	return msg
}

// SubscribeIdleAgents allows the architect to subscribe to idle agent notifications
func (d *Dispatcher) SubscribeIdleAgents(architectID string) <-chan string {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()

	d.architectID = architectID
	d.logger.Info("Architect %s subscribed to idle agent notifications", architectID)
	return d.idleAgentCh
}

// NotifyIdleAgent sends a notification when an agent becomes idle
func (d *Dispatcher) NotifyIdleAgent(agentID string) {
	d.notificationMu.RLock()
	defer d.notificationMu.RUnlock()

	if d.architectID == "" {
		// No architect subscribed
		return
	}

	select {
	case d.idleAgentCh <- agentID:
		d.logger.Debug("Notified architect %s that agent %s is idle", d.architectID, agentID)
	default:
		// Channel full, drop notification
		d.logger.Warn("Idle agent notification dropped - channel full")
	}
}

// NotifyArchitectOnResult notifies the architect when a coding agent finishes work
// isCompletionStatus checks if a status indicates task completion
func (d *Dispatcher) isCompletionStatus(status any) bool {
	statusStr, ok := status.(string)
	if !ok {
		return false
	}
	switch statusStr {
	case "completed", "done", "error", "failed", "timeout", "cancelled", "aborted":
		return true
	default:
		return false
	}
}

func (d *Dispatcher) NotifyArchitectOnResult(msg *proto.AgentMsg) {
	// Check if this is a RESULT from a coding agent completion
	if msg.Type == proto.MsgTypeRESULT {
		if status, exists := msg.GetPayload("status"); exists {
			d.logger.Debug("Checking RESULT message status: %v from agent %s", status, msg.FromAgent)
			if d.isCompletionStatus(status) {
				// Check if agent was actually busy before sending idle notification
				d.busyMu.Lock()
				wasBusy := d.busyAgents[msg.FromAgent]
				if wasBusy {
					// Remove from busy agents map
					delete(d.busyAgents, msg.FromAgent)
					d.busyMu.Unlock()

					// Coding agent finished work - notify they are idle
					d.NotifyIdleAgent(msg.FromAgent)
					d.logger.Debug("Agent %s marked as idle after completion", msg.FromAgent)
				} else {
					d.busyMu.Unlock()
					d.logger.Debug("Agent %s was not marked as busy, skipping idle notification", msg.FromAgent)
				}
			}
		}
	}
}

// CloseIdleChannel closes the idle agent notification channel for graceful shutdown
func (d *Dispatcher) CloseIdleChannel() {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()

	if d.idleAgentCh != nil {
		close(d.idleAgentCh)
		d.logger.Info("Closed idle agent notification channel")
	}
}
