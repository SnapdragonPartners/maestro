package dispatch

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/agent"
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
	architectRequestQueue []*proto.AgentMsg // Questions/requests for architect
	coderQueue            []*proto.AgentMsg // Answers/feedback for coders
	sharedWorkQueue       []*proto.AgentMsg // Ready stories for any coder
	queueMutex      sync.RWMutex      // Protects all queues

	// Channel-based notifications for architect
	idleAgentCh    chan string          // Notifies architect when agents become idle
	specCh         chan *proto.AgentMsg // Delivers spec messages to architect
	architectID    string               // ID of the architect to notify
	notificationMu sync.RWMutex         // Protects notification channels
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
		idleAgentCh: make(chan string, 10),          // Buffered channel for idle agent notifications
		specCh:      make(chan *proto.AgentMsg, 10), // Buffered channel for spec messages
		busyAgents:  make(map[string]bool),          // Initialize busy agents map
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
	d.logger.Info("Processing message %s: %s → %s (%s)", msg.ID, msg.FromAgent, msg.ToAgent, msg.Type)

	// Log message
	if err := d.eventLog.WriteMessage(msg); err != nil {
		d.logger.Error("Failed to log incoming message: %v", err)
		// Continue processing even if logging fails
	}

	// Resolve logical agent name to actual agent ID for all messages
	resolvedToAgent := d.resolveAgentName(msg.ToAgent)
	if resolvedToAgent != msg.ToAgent {
		d.logger.Debug("Resolved logical name %s to %s", msg.ToAgent, resolvedToAgent)
		msg.ToAgent = resolvedToAgent
	}

	// Route messages to appropriate queues based on type
	d.queueMutex.Lock()
	defer d.queueMutex.Unlock()

	switch msg.Type {
	case proto.MsgTypeTASK:
		// TASK messages go to shared work queue for coders to pull
		d.sharedWorkQueue = append(d.sharedWorkQueue, msg)
		d.logger.Info("🔄 Queued TASK %s to shared work queue (queue size: %d)", msg.ID, len(d.sharedWorkQueue))

	case proto.MsgTypeSPEC:
		// SPEC messages go to architect via spec channel
		d.logger.Info("🔄 Dispatcher sending SPEC %s to architect via spec channel %p", msg.ID, d.specCh)
		select {
		case d.specCh <- msg:
			d.logger.Info("✅ SPEC %s delivered to architect spec channel", msg.ID)
		default:
			d.logger.Warn("❌ Architect spec channel full, dropping SPEC %s", msg.ID)
		}

	case proto.MsgTypeQUESTION:
		// QUESTION messages go to architect request queue for architect to pull
		d.architectRequestQueue = append(d.architectRequestQueue, msg)
		d.logger.Debug("Queued QUESTION %s to architect request queue (queue size: %d)", msg.ID, len(d.architectRequestQueue))

	case proto.MsgTypeREQUEST:
		// REQUEST messages (approval requests) go to architect request queue for architect to pull
		d.architectRequestQueue = append(d.architectRequestQueue, msg)
		d.logger.Debug("Queued REQUEST %s to architect request queue (queue size: %d)", msg.ID, len(d.architectRequestQueue))

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
		d.logger.Info("Processing message type %s immediately (not queued)", msg.Type)
		d.queueMutex.Unlock() // Unlock early for immediate processing

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
		d.architectRequestQueue = append(d.architectRequestQueue, response)
		d.logger.Debug("Queued QUESTION %s for architect (queue size: %d)", response.ID, len(d.architectRequestQueue))

	case proto.MsgTypeREQUEST:
		// Approval requests go to architect queue
		d.architectRequestQueue = append(d.architectRequestQueue, response)
		d.logger.Debug("Queued REQUEST %s for architect (queue size: %d)", response.ID, len(d.architectRequestQueue))

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

// isArchitectAgent checks if an agent ID represents an architect agent
func (d *Dispatcher) isArchitectAgent(agentID string) bool {
	// Check if this agent is an architect type
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	// Look up agent info to check type
	for _, agentWithModel := range d.config.GetAllAgents() {
		if agentWithModel.Agent.GetLogID(agentWithModel.ModelName) == agentID {
			return agentWithModel.Agent.Type == "architect"
		}
	}
	
	// Fallback: check if ID contains architect indicators
	return strings.Contains(strings.ToLower(agentID), "architect") || strings.Contains(strings.ToLower(agentID), "o3")
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
		"architect_request_queue_size": len(d.architectRequestQueue),
		"coder_queue_size":       len(d.coderQueue),
		"shared_work_queue_size": len(d.sharedWorkQueue),
	}
}

// PullArchitectWork retrieves the next question for the architect to process
func (d *Dispatcher) PullArchitectWork() *proto.AgentMsg {
	d.queueMutex.Lock()
	defer d.queueMutex.Unlock()

	if len(d.architectRequestQueue) == 0 {
		return nil
	}

	// Get first message from architect request queue (FIFO)
	msg := d.architectRequestQueue[0]
	d.architectRequestQueue = d.architectRequestQueue[1:]

	d.logger.Debug("Pulled %s %s from architect request queue (remaining: %d)", msg.Type, msg.ID, len(d.architectRequestQueue))
	return msg
}

// PullCoderFeedback retrieves the next answer/feedback for a specific coder
func (d *Dispatcher) PullCoderFeedback(agentID string) *proto.AgentMsg {
	d.queueMutex.Lock()
	defer d.queueMutex.Unlock()

	// Debug: Log queue contents
	logx.DebugToFile(context.Background(), "dispatch", "queue_debug.log", "PullCoderFeedback for %s, queue size: %d", agentID, len(d.coderQueue))
	for _, msg := range d.coderQueue {
		d.logger.Debug("Queue msg: %s -> %s (type: %s)", msg.FromAgent, msg.ToAgent, msg.Type)
	}

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

	// Debug: Log queue state
	logx.DebugToFile(context.Background(), "dispatch", "pull_shared_debug.log", "PullSharedWork called, queue size: %d", len(d.sharedWorkQueue))
	if len(d.sharedWorkQueue) > 0 {
		msg := d.sharedWorkQueue[0]
		d.logger.Debug("Available task: %s -> %s (type: %s)", msg.FromAgent, msg.ToAgent, msg.Type)
	}

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


// ArchitectChannels contains all channels for architect notifications
type ArchitectChannels struct {
	IdleAgents <-chan string          // Notifies when agents become idle
	Specs      <-chan *proto.AgentMsg // Delivers spec messages
}

// SubscribeArchitect allows the architect to subscribe to all architect notifications
func (d *Dispatcher) SubscribeArchitect(architectID string) ArchitectChannels {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()

	d.architectID = architectID
	d.logger.Info("🔔 Architect %s subscribed to all notifications", architectID)
	d.logger.Info("🔔 Spec channel %p provided to architect %s", d.specCh, architectID)
	
	return ArchitectChannels{
		IdleAgents: d.idleAgentCh,
		Specs:      d.specCh,
	}
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

	if d.specCh != nil {
		close(d.specCh)
		d.logger.Info("Closed spec channel")
	}
}

// QueueHead represents a message head in a queue
type QueueHead struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
	TS   string `json:"ts"`
}

// QueueInfo represents queue information
type QueueInfo struct {
	Name   string      `json:"name"`
	Length int         `json:"length"`
	Heads  []QueueHead `json:"heads"`
}

// DumpHeads returns queue information with up to n message heads from each queue
func (d *Dispatcher) DumpHeads(n int) map[string]any {
	d.queueMutex.RLock()
	defer d.queueMutex.RUnlock()

	// Helper function to format message heads
	formatHeads := func(messages []*proto.AgentMsg, limit int) []QueueHead {
		var heads []QueueHead
		count := len(messages)
		if limit < count {
			count = limit
		}

		for i := 0; i < count; i++ {
			msg := messages[i]
			heads = append(heads, QueueHead{
				ID:   msg.ID,
				Type: string(msg.Type),
				From: msg.FromAgent,
				To:   msg.ToAgent,
				TS:   msg.Timestamp.Format("2006-01-02T15:04:05Z"),
			})
		}
		return heads
	}

	return map[string]any{
		"architect": QueueInfo{
			Name:   "architect",
			Length: len(d.architectRequestQueue),
			Heads:  formatHeads(d.architectRequestQueue, n),
		},
		"coder": QueueInfo{
			Name:   "coder",
			Length: len(d.coderQueue),
			Heads:  formatHeads(d.coderQueue, n),
		},
		"shared": QueueInfo{
			Name:   "shared",
			Length: len(d.sharedWorkQueue),
			Heads:  formatHeads(d.sharedWorkQueue, n),
		},
		"input_channel": map[string]any{
			"length":   len(d.inputChan),
			"capacity": cap(d.inputChan),
			"blocked":  len(d.inputChan) >= cap(d.inputChan),
		},
	}
}

// AgentInfo represents information about a registered agent
type AgentInfo struct {
	ID     string
	Type   agent.AgentType
	State  string
	Driver agent.Driver
}

// GetRegisteredAgents returns information about all registered agents
func (d *Dispatcher) GetRegisteredAgents() []AgentInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	var agentInfos []AgentInfo
	for id, agentInterface := range d.agents {
		// Try to cast to Driver interface to get more information
		if driver, ok := agentInterface.(agent.Driver); ok {
			agentInfos = append(agentInfos, AgentInfo{
				ID:     id,
				Type:   driver.GetAgentType(),
				State:  fmt.Sprintf("%v", driver.GetCurrentState()),
				Driver: driver,
			})
		} else {
			// Fallback for agents that don't implement Driver interface
			// Extract type from ID (format: "model:id")
			agentType := agent.AgentTypeCoder // Default fallback
			if strings.Contains(strings.ToLower(id), "architect") || strings.Contains(strings.ToLower(id), "o3") {
				agentType = agent.AgentTypeArchitect
			}
			
			agentInfos = append(agentInfos, AgentInfo{
				ID:     id,
				Type:   agentType,
				State:  "UNKNOWN",
				Driver: nil,
			})
		}
	}
	
	return agentInfos
}
