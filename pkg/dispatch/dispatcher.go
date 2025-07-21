package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// Severity represents the severity level of agent errors
type Severity int

const (
	Warn Severity = iota
	Fatal
)

// AgentError represents an error reported by an agent
type AgentError struct {
	ID  string
	Err error
	Sev Severity
}

// AttachChannels removed - channels are now set directly via ChannelReceiver interface

type Agent interface {
	GetID() string
	Shutdown(ctx context.Context) error
}

// ChannelReceiver is an optional interface for agents that need direct channel access
type ChannelReceiver interface {
	SetChannels(specCh <-chan *proto.AgentMsg, questionsCh chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg)
	SetDispatcher(dispatcher *Dispatcher)
	SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification)
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

	// Phase 1: Channel-based queues
	storyCh     chan *proto.AgentMsg // Ready stories for any coder (replaces sharedWorkQueue)
	questionsCh chan *proto.AgentMsg // Questions/requests for architect (replaces architectRequestQueue)

	// Phase 2: Per-agent reply channels
	replyChannels map[string]chan *proto.AgentMsg // Per-agent reply channels for ANSWER/RESULT messages

	// Channel-based notifications for architect
	specCh         chan *proto.AgentMsg // Delivers spec messages to architect
	architectID    string               // ID of the architect to notify
	notificationMu sync.RWMutex         // Protects notification channels

	// S-5: Metrics monitoring
	highUtilizationStart time.Time    // Track when high utilization started
	highUtilizationMu    sync.RWMutex // Protects high utilization tracking

	// Supervisor pattern for error handling
	errCh chan AgentError // Channel for agent error reporting

	// Story lease tracking
	leases      map[string]string // agent_id -> story_id
	leasesMutex sync.Mutex        // Protects lease map

	// Container registry for centralized container tracking
	containerRegistry *exec.ContainerRegistry // Tracks all active containers

	// State change notifications
	stateChangeCh chan *proto.StateChangeNotification // Channel for agent state change notifications
}

type DispatchResult struct {
	Message *proto.AgentMsg
	Error   error
}

func NewDispatcher(cfg *config.Config, rateLimiter *limiter.Limiter, eventLog *eventlog.Writer) (*Dispatcher, error) {
	return &Dispatcher{
		agents:        make(map[string]Agent),
		rateLimiter:   rateLimiter,
		eventLog:      eventLog,
		logger:        logx.NewLogger("dispatcher"),
		config:        cfg,
		inputChan:     make(chan *proto.AgentMsg, 100), // Buffered channel for message queue
		shutdown:      make(chan struct{}),
		running:       false,
		specCh:        make(chan *proto.AgentMsg, 10),                                       // Buffered channel for spec messages
		storyCh:       make(chan *proto.AgentMsg, cfg.StoryChannelFactor*cfg.CountCoders()), // S-5: Buffer size = factor × numCoders
		questionsCh:   make(chan *proto.AgentMsg, cfg.QuestionsChannelSize),                 // Buffer size from config
		replyChannels: make(map[string]chan *proto.AgentMsg),                                // Per-agent reply channels
		errCh:         make(chan AgentError, 10),                                            // Buffered channel for error reporting
		stateChangeCh:     make(chan *proto.StateChangeNotification, 100), // Buffered channel for state change notifications
		leases:            make(map[string]string),                                      // Story lease tracking
		containerRegistry: exec.NewContainerRegistry(logx.NewLogger("container-registry")), // Container tracking registry
	}, nil
}

// RegisterAgent is deprecated - use Attach() instead
func (d *Dispatcher) RegisterAgent(agent Agent) error {
	d.logger.Warn("RegisterAgent is deprecated, use Attach() instead for agent %s", agent.GetID())
	d.Attach(agent)
	return nil
}

// Attach provides channels for agent communication based on agent type
func (d *Dispatcher) Attach(ag Agent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	agentID := ag.GetID()

	// Store agent in map
	d.agents[agentID] = ag

	// Create reply channel for this agent (buffer size = 1)
	replyCh := make(chan *proto.AgentMsg, 1)
	d.replyChannels[agentID] = replyCh

	// Set up channels for agents that implement ChannelReceiver interface
	if channelReceiver, ok := ag.(ChannelReceiver); ok {
		// Set up state notification channel for all ChannelReceiver agents
		channelReceiver.SetStateNotificationChannel(d.stateChangeCh)

		// Determine agent type to provide appropriate channels
		if agentDriver, ok := ag.(agent.Driver); ok {
			switch agentDriver.GetAgentType() {
			case agent.AgentTypeArchitect:
				d.logger.Info("Attached architect agent: %s with direct channel setup", agentID)
				channelReceiver.SetChannels(d.specCh, d.questionsCh, replyCh)
				channelReceiver.SetDispatcher(d)
				return
			case agent.AgentTypeCoder:
				d.logger.Info("Attached coder agent: %s with direct channel setup", agentID)
				// Coders receive story messages via storyCh
				channelReceiver.SetChannels(d.storyCh, nil, replyCh)
				channelReceiver.SetDispatcher(d)
				return
			}
		}
	}

	// For other agents, log the attachment
	if agentDriver, ok := ag.(agent.Driver); ok {
		switch agentDriver.GetAgentType() {
		case agent.AgentTypeArchitect:
			d.logger.Info("Attached architect agent: %s", agentID)
		case agent.AgentTypeCoder:
			d.logger.Info("Attached coder agent: %s", agentID)
		}
	} else {
		d.logger.Warn("Agent %s does not implement Driver interface", agentID)
	}
}

// Detach removes an agent and cleans up its channels (public method for orchestrator)
func (d *Dispatcher) Detach(agentID string) {
	d.detach(agentID)
}

// detach removes an agent and cleans up its channels
func (d *Dispatcher) detach(agentID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Remove from agents map
	delete(d.agents, agentID)

	// Close and remove reply channel
	if replyCh, exists := d.replyChannels[agentID]; exists {
		close(replyCh)
		delete(d.replyChannels, agentID)
		d.logger.Info("Detached agent: %s and closed reply channel", agentID)
	}
}

// UnregisterAgent is deprecated - agents should use defer d.detach() after Attach()
func (d *Dispatcher) UnregisterAgent(agentID string) error {
	d.logger.Warn("UnregisterAgent is deprecated, use defer detach() instead for agent %s", agentID)
	d.detach(agentID)
	return nil
}

// routeToReplyCh routes ANSWER/RESULT messages to the appropriate coder's reply channel
func (d *Dispatcher) routeToReplyCh(msg *proto.AgentMsg, msgTypeStr string) {
	targetAgent := msg.ToAgent

	d.logger.Info("🔄 Routing %s %s to agent %s reply channel", msgTypeStr, msg.ID, targetAgent)

	// Find the reply channel for this agent
	d.mu.RLock()
	replyCh, exists := d.replyChannels[targetAgent]
	d.mu.RUnlock()

	if !exists {
		d.logger.Warn("❌ No reply channel found for agent %s (message %s dropped)", targetAgent, msg.ID)
		return
	}

	// Send to reply channel (non-blocking with buffer size 1)
	select {
	case replyCh <- msg:
		d.logger.Info("✅ %s %s delivered to agent %s reply channel", msgTypeStr, msg.ID, targetAgent)
	default:
		d.logger.Warn("❌ Reply channel full for agent %s, dropping %s %s", targetAgent, msgTypeStr, msg.ID)
	}
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

	// Channel readers removed - stories are delivered directly via Attach() channels

	// S-5: Start metrics monitoring worker
	d.wg.Add(1)
	go d.metricsMonitor(ctx)

	// Start supervisor goroutine for error handling
	d.wg.Add(1)
	go d.supervisor(ctx)

	// Start container registry cleanup routine
	// Check every 5 minutes for containers idle > 30 minutes
	executor := exec.NewLongRunningDockerExec("alpine:latest", "") // Empty agentID - for cleanup only
	d.containerRegistry.StartCleanupRoutine(ctx, executor, 5*time.Minute, 30*time.Minute)

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

	// Close all channels for graceful shutdown
	d.closeAllChannels()
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

// metricsMonitor checks storyCh utilization and logs warnings for sustained high utilization
func (d *Dispatcher) metricsMonitor(ctx context.Context) {
	defer d.wg.Done()

	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Metrics monitor stopped by context")
			return
		case <-d.shutdown:
			d.logger.Info("Metrics monitor stopped by shutdown signal")
			return
		case <-ticker.C:
			// Check storyCh utilization
			if cap(d.storyCh) == 0 {
				continue // Avoid division by zero
			}

			utilization := float64(len(d.storyCh)) / float64(cap(d.storyCh))

			d.highUtilizationMu.Lock()
			if utilization > 0.8 {
				// High utilization detected
				if d.highUtilizationStart.IsZero() {
					// First time detecting high utilization
					d.highUtilizationStart = time.Now()
					d.logger.Debug("High storyCh utilization detected: %.2f%% - monitoring started", utilization*100)
				} else {
					// Check if sustained for more than 30 seconds
					duration := time.Since(d.highUtilizationStart)
					if duration > 30*time.Second {
						d.logger.Warn("⚠️  SUSTAINED HIGH storyCh utilization: %.2f%% for %v (capacity: %d)", utilization*100, duration, cap(d.storyCh))
					}
				}
			} else {
				// Normal utilization, reset tracking
				if !d.highUtilizationStart.IsZero() {
					d.logger.Debug("storyCh utilization back to normal: %.2f%%", utilization*100)
					d.highUtilizationStart = time.Time{}
				}
			}
			d.highUtilizationMu.Unlock()
		}
	}
}

// supervisor handles agent error reporting and fatal error cleanup
func (d *Dispatcher) supervisor(ctx context.Context) {
	defer d.wg.Done()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Supervisor stopped by context")
			return
		case <-d.shutdown:
			d.logger.Info("Supervisor stopped by shutdown signal")
			return
		case agentErr := <-d.errCh:
			// Log every error
			d.logger.Warn("Agent error reported - ID: %s, Error: %s, Severity: %v",
				agentErr.ID, agentErr.Err.Error(), agentErr.Sev)

			// Handle fatal errors by detaching the agent
			if agentErr.Sev == Fatal {
				d.logger.Error("Fatal error from agent %s, detaching", agentErr.ID)
				d.detach(agentErr.ID)

				// Check for zero-agent conditions
				d.checkZeroAgentCondition()
			}
		}
	}
}

// checkZeroAgentCondition warns if there are no agents of a certain type
func (d *Dispatcher) checkZeroAgentCondition() {
	d.mu.RLock()
	architectCount := 0
	coderCount := 0

	for _, ag := range d.agents {
		if agentDriver, ok := ag.(agent.Driver); ok {
			switch agentDriver.GetAgentType() {
			case agent.AgentTypeArchitect:
				architectCount++
			case agent.AgentTypeCoder:
				coderCount++
			}
		}
	}
	d.mu.RUnlock()

	if architectCount == 0 {
		d.logger.Warn("Zero-agent condition: no architect agents remaining")
	}
	if coderCount == 0 {
		d.logger.Warn("Zero-agent condition: no coder agents remaining")
	}
}

// ReportError allows agents to report errors to the supervisor
func (d *Dispatcher) ReportError(agentID string, err error, severity Severity) {
	select {
	case d.errCh <- AgentError{ID: agentID, Err: err, Sev: severity}:
		// Error reported successfully
	default:
		// Error channel full, log directly
		d.logger.Error("Error channel full, dropping error report from agent %s: %v", agentID, err)
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
	switch msg.Type {
	case proto.MsgTypeSTORY:
		// STORY messages go to storyCh for coders to receive
		d.logger.Info("🔄 Sending STORY %s to storyCh", msg.ID)

		// Send to storyCh (blocking send - buffer should prevent blocking)
		d.storyCh <- msg
		d.logger.Info("✅ STORY %s delivered to storyCh", msg.ID)

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
		// QUESTION messages go to questionsCh for architect to receive
		d.logger.Info("🔄 Sending QUESTION %s to questionsCh", msg.ID)

		// Send to questionsCh (blocking)
		d.questionsCh <- msg
		d.logger.Info("✅ QUESTION %s delivered to questionsCh", msg.ID)

	case proto.MsgTypeREQUEST:
		// REQUEST messages go to questionsCh for architect to receive
		d.logger.Info("🔄 Sending REQUEST %s to questionsCh", msg.ID)

		// Send to questionsCh (blocking)
		d.questionsCh <- msg
		d.logger.Info("✅ REQUEST %s delivered to questionsCh", msg.ID)

	case proto.MsgTypeRESULT:
		// RESULT messages go to specific coder's reply channel
		d.routeToReplyCh(msg, "RESULT")

	case proto.MsgTypeANSWER:
		// ANSWER messages go to specific coder's reply channel
		d.routeToReplyCh(msg, "ANSWER")

	case proto.MsgTypeREQUEUE:
		// REQUEUE messages go to questionsCh for architect to handle
		d.logger.Info("🔄 Sending REQUEUE %s to questionsCh", msg.ID)

		// Send to questionsCh (blocking)
		d.questionsCh <- msg
		d.logger.Info("✅ REQUEUE %s delivered to questionsCh", msg.ID)

	default:
		// Other message types (ERROR, SHUTDOWN, etc.) still processed immediately
		d.logger.Info("Processing message type %s immediately (not queued)", msg.Type)

		// Find target agent
		d.mu.RLock()
		targetAgent, exists := d.agents[msg.ToAgent]
		d.mu.RUnlock()

		if !exists {
			d.sendErrorResponse(msg, fmt.Errorf("target agent %s not found", msg.ToAgent))
			return
		}

		// Process immediately for non-STORY messages
		response := d.processWithRetry(ctx, msg, targetAgent)

		// If there's a response, route it to appropriate queue
		if response.Message != nil {
			d.sendResponse(response.Message)
		} else if response.Error != nil {
			d.sendErrorResponse(msg, response.Error)
		}
	}
}

func (d *Dispatcher) processWithRetry(ctx context.Context, msg *proto.AgentMsg, agent Agent) *DispatchResult {
	// Extract model name from agent logID (format: "model:id")
	modelName := msg.ToAgent
	if parts := strings.Split(msg.ToAgent, ":"); len(parts) >= 2 {
		modelName = parts[0]
	}

	// Check rate limiting before processing
	if err := d.checkRateLimit(modelName); err != nil {
		d.logger.Warn("Rate limit exceeded for %s (model %s): %v", msg.ToAgent, modelName, err)
		return &DispatchResult{Error: err}
	}

	// Process message - NOTE: ProcessMessage removed from Agent interface
	// Agents now receive messages via channels exclusively
	d.logger.Debug("Processing message %s for agent %s", msg.ID, msg.ToAgent)

	// Release agent slot after processing
	d.rateLimiter.ReleaseAgent(modelName)

	// Messages flow through channels - no direct processing or retry needed here
	// The retry logic would be handled at a higher level if needed
	return &DispatchResult{Message: nil}
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

	// Resolve logical agent name to actual agent ID
	resolvedToAgent := d.resolveAgentName(response.ToAgent)
	if resolvedToAgent != response.ToAgent {
		d.logger.Debug("Resolved logical name %s to %s", response.ToAgent, resolvedToAgent)
		response.ToAgent = resolvedToAgent
	}

	switch response.Type {
	case proto.MsgTypeQUESTION:
		// Questions go to questionsCh
		select {
		case d.questionsCh <- response:
			d.logger.Debug("Queued QUESTION %s for architect", response.ID)
		default:
			d.logger.Warn("Questions channel full, dropping QUESTION %s", response.ID)
		}

	case proto.MsgTypeREQUEST:
		// Approval requests go to questionsCh
		select {
		case d.questionsCh <- response:
			d.logger.Debug("Queued REQUEST %s for architect", response.ID)
		default:
			d.logger.Warn("Questions channel full, dropping REQUEST %s", response.ID)
		}

	case proto.MsgTypeRESULT:
		// Approval responses go to coder reply channel
		d.routeToReplyCh(response, "RESULT")

	case proto.MsgTypeANSWER:
		// Information responses go to coder reply channel
		d.routeToReplyCh(response, "ANSWER")

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

	storyChUtilization := float64(len(d.storyCh)) / float64(cap(d.storyCh))

	// S-5: WARN if storyCh utilization > 0.8 (this should be logged separately for monitoring)
	if storyChUtilization > 0.8 {
		d.logger.Warn("⚠️  storyCh utilization high: %.2f%% (%d/%d)", storyChUtilization*100, len(d.storyCh), cap(d.storyCh))
	}

	return map[string]any{
		"running":                      d.running,
		"agents":                       agentList,
		"queue_length":                 len(d.inputChan),
		"queue_capacity":               cap(d.inputChan),
		"architect_request_queue_size": 0, // Legacy field, now always 0
		"story_ch_length":              len(d.storyCh),
		"story_ch_capacity":            cap(d.storyCh),
		"story_ch_utilization":         storyChUtilization,
		"questions_ch_length":          len(d.questionsCh),
		"questions_ch_capacity":        cap(d.questionsCh),
	}
}

// GetQuestionsCh returns the questions channel for architect to receive from
func (d *Dispatcher) GetQuestionsCh() <-chan *proto.AgentMsg {
	return d.questionsCh
}

// GetReplyCh returns the reply channel for a specific coder agent
func (d *Dispatcher) GetReplyCh(agentID string) <-chan *proto.AgentMsg {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if replyCh, exists := d.replyChannels[agentID]; exists {
		return replyCh
	}
	return nil
}

// GetStoryCh returns the story channel for coders to receive from
func (d *Dispatcher) GetStoryCh() <-chan *proto.AgentMsg {
	return d.storyCh
}

// GetStateChangeChannel returns the state change notification channel
func (d *Dispatcher) GetStateChangeChannel() <-chan *proto.StateChangeNotification {
	return d.stateChangeCh
}

// ArchitectChannels contains the channels returned to architects
type ArchitectChannels struct {
	Specs <-chan *proto.AgentMsg // Delivers spec messages
}

// SubscribeArchitect allows the architect to get the spec channel
func (d *Dispatcher) SubscribeArchitect(architectID string) ArchitectChannels {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()

	d.architectID = architectID
	d.logger.Info("🔔 Architect %s subscribed to spec notifications", architectID)
	d.logger.Info("🔔 Spec channel %p provided to architect %s", d.specCh, architectID)

	return ArchitectChannels{
		Specs: d.specCh,
	}
}

// closeAllChannels closes all dispatcher-owned channels for graceful shutdown
func (d *Dispatcher) closeAllChannels() {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()

	// Close specCh
	if d.specCh != nil {
		close(d.specCh)
		d.logger.Info("Closed spec channel")
	}

	// Close storyCh
	if d.storyCh != nil {
		close(d.storyCh)
		d.logger.Info("Closed story channel")
	}

	// Close questionsCh
	if d.questionsCh != nil {
		close(d.questionsCh)
		d.logger.Info("Closed questions channel")
	}

	// Close all reply channels
	d.mu.Lock()
	for agentID, replyCh := range d.replyChannels {
		close(replyCh)
		d.logger.Info("Closed reply channel for agent: %s", agentID)
	}
	// Clear the map
	d.replyChannels = make(map[string]chan *proto.AgentMsg)
	d.mu.Unlock()

	// Close error reporting channel
	if d.errCh != nil {
		close(d.errCh)
		d.logger.Info("Closed error reporting channel")
	}

	// Close state change notification channel
	if d.stateChangeCh != nil {
		close(d.stateChangeCh)
		d.logger.Info("Closed state change notification channel")
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
	return map[string]any{
		"architect_legacy": QueueInfo{
			Name:   "architect_legacy",
			Length: 0,             // Legacy queue removed
			Heads:  []QueueHead{}, // Empty
		},
		"questions_ch": map[string]any{
			"length":   len(d.questionsCh),
			"capacity": cap(d.questionsCh),
			"blocked":  len(d.questionsCh) >= cap(d.questionsCh),
		},
		"reply_channels": map[string]any{
			"count": len(d.replyChannels),
		},
		"story_ch": map[string]any{
			"length":   len(d.storyCh),
			"capacity": cap(d.storyCh),
			"blocked":  len(d.storyCh) >= cap(d.storyCh),
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
			// Default to coder type (all new agents should implement Driver interface)
			agentInfos = append(agentInfos, AgentInfo{
				ID:     id,
				Type:   agent.AgentTypeCoder, // Default fallback
				State:  "UNKNOWN",
				Driver: nil,
			})
		}
	}

	return agentInfos
}

// SetLease records that an agent is working on a specific story
func (d *Dispatcher) SetLease(agentID, storyID string) {
	d.leasesMutex.Lock()
	defer d.leasesMutex.Unlock()
	d.leases[agentID] = storyID
	d.logger.Debug("Set lease: agent %s -> story %s", agentID, storyID)
}

// GetLease returns the story ID that an agent is working on, or empty string if none
func (d *Dispatcher) GetLease(agentID string) string {
	d.leasesMutex.Lock()
	defer d.leasesMutex.Unlock()
	storyID := d.leases[agentID]
	return storyID
}

// ClearLease removes an agent's story assignment
func (d *Dispatcher) ClearLease(agentID string) {
	d.leasesMutex.Lock()
	defer d.leasesMutex.Unlock()
	if storyID, exists := d.leases[agentID]; exists {
		delete(d.leases, agentID)
		d.logger.Debug("Cleared lease: agent %s was working on story %s", agentID, storyID)
	}
}

// SendRequeue sends a requeue message for the story currently assigned to the given agent
func (d *Dispatcher) SendRequeue(agentID, reason string) error {
	storyID := d.GetLease(agentID)
	if storyID == "" {
		return fmt.Errorf("no lease found for agent %s", agentID)
	}

	// Create requeue message (matches existing logic from coder ERROR handler)
	requeueMsg := proto.NewAgentMsg(proto.MsgTypeREQUEUE, "orchestrator", "architect")
	requeueMsg.SetPayload("story_id", storyID)
	requeueMsg.SetPayload("reason", reason)

	// Send to questions channel (same as existing requeue logic)
	select {
	case d.questionsCh <- requeueMsg:
		d.logger.Info("Requeued story %s for agent %s (reason: %s)", storyID, agentID, reason)
		return nil
	default:
		return fmt.Errorf("questions channel full, could not requeue story %s", storyID)
	}
}

// GetContainerRegistry returns the container registry for orchestrator access
func (d *Dispatcher) GetContainerRegistry() *exec.ContainerRegistry {
	return d.containerRegistry
}
