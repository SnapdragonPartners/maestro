// Package dispatch provides message routing and agent coordination for the orchestrator.
// It manages agent communication channels, rate limiting, and message processing workflows.
package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// Severity represents the severity level of agent errors.
type Severity int

const (
	// Warn indicates a warning-level severity.
	Warn Severity = iota
	// Fatal indicates a fatal-level severity.
	Fatal
)

// AgentError represents an error reported by an agent.
type AgentError struct {
	Err error
	ID  string
	Sev Severity
}

// AttachChannels removed - channels are now set directly via ChannelReceiver interface.

// runStrategy defines how the dispatcher executes its core loops.
type runStrategy interface {
	Run(d *Dispatcher, ctx context.Context) error
	Stop() error
}

// Agent represents an agent that can be managed by the dispatcher.
type Agent interface {
	GetID() string
	Shutdown(ctx context.Context) error
}

// ChannelReceiver is an optional interface for agents that need direct channel access.
type ChannelReceiver interface {
	SetChannels(specCh <-chan *proto.AgentMsg, questionsCh chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg)
	SetDispatcher(dispatcher *Dispatcher)
	SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification)
}

// Dispatcher coordinates message passing and work distribution between agents.
//
//nolint:govet // Large complex struct, logical grouping preferred over memory optimization
type Dispatcher struct {
	agents      map[string]Agent
	rateLimiter *limiter.Limiter
	logger      *logx.Logger
	config      *config.Config
	inputChan   chan *proto.AgentMsg
	shutdown    chan struct{}
	mu          sync.RWMutex
	wg          sync.WaitGroup
	running     bool

	// Phase 1: Channel-based queues.
	storyCh     chan *proto.AgentMsg // Ready stories for any coder (replaces sharedWorkQueue)
	questionsCh chan *proto.AgentMsg // Questions/requests for architect (replaces architectRequestQueue)

	// Phase 2: Per-agent reply channels.
	replyChannels map[string]chan *proto.AgentMsg // Per-agent reply channels for ANSWER/RESULT messages

	// Channel-based notifications for architect.
	specCh         chan *proto.AgentMsg // Delivers spec messages to architect
	architectID    string               // ID of the architect to notify
	notificationMu sync.RWMutex         // Protects notification channels

	// S-5: Metrics monitoring.
	highUtilizationStart time.Time    // Track when high utilization started
	highUtilizationMu    sync.RWMutex // Protects high utilization tracking

	// Supervisor pattern for error handling.
	errCh chan AgentError // Channel for agent error reporting

	// Story lease tracking.
	leases      map[string]string // agent_id -> story_id
	leasesMutex sync.Mutex        // Protects lease map

	// Container registry for centralized container tracking.
	containerRegistry *exec.ContainerRegistry // Tracks all active containers

	// State change notifications.
	stateChangeCh chan *proto.StateChangeNotification // Channel for agent state change notifications

	// Execution strategy for testing
	runStrat runStrategy // Controls how dispatcher executes (goroutines vs step-by-step)
}

// Result represents the result of a message dispatch operation.
type Result struct {
	Message *proto.AgentMsg
	Error   error
}

// NewDispatcher creates a new message dispatcher with the given configuration.
func NewDispatcher(cfg *config.Config, rateLimiter *limiter.Limiter) (*Dispatcher, error) {
	return &Dispatcher{
		agents:            make(map[string]Agent),
		rateLimiter:       rateLimiter,
		logger:            logx.NewLogger("dispatcher"),
		config:            cfg,
		inputChan:         make(chan *proto.AgentMsg, 100), // Buffered channel for message queue
		shutdown:          make(chan struct{}),
		running:           false,
		specCh:            make(chan *proto.AgentMsg, 10),                                             // Buffered channel for spec messages
		storyCh:           make(chan *proto.AgentMsg, config.StoryChannelFactor*cfg.Agents.MaxCoders), // S-5: Buffer size = factor × numCoders
		questionsCh:       make(chan *proto.AgentMsg, config.QuestionsChannelSize),                    // Buffer size from config
		replyChannels:     make(map[string]chan *proto.AgentMsg),                                      // Per-agent reply channels
		errCh:             make(chan AgentError, 10),                                                  // Buffered channel for error reporting
		stateChangeCh:     make(chan *proto.StateChangeNotification, 100),                             // Buffered channel for state change notifications
		leases:            make(map[string]string),                                                    // Story lease tracking
		containerRegistry: exec.NewContainerRegistry(logx.NewLogger("container-registry")),            // Container tracking registry
		runStrat:          &goroutineStrategy{},                                                       // Default to production goroutine strategy
	}, nil
}

// RegisterAgent is deprecated - use Attach() instead.
func (d *Dispatcher) RegisterAgent(agent Agent) error {
	d.logger.Warn("RegisterAgent is deprecated, use Attach() instead for agent %s", agent.GetID())
	d.Attach(agent)
	return nil
}

// Attach provides channels for agent communication based on agent type.
func (d *Dispatcher) Attach(ag Agent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	agentID := ag.GetID()

	// Store agent in map.
	d.agents[agentID] = ag

	// Create reply channel for this agent (buffer size = 1).
	replyCh := make(chan *proto.AgentMsg, 1)
	d.replyChannels[agentID] = replyCh

	// Set up channels for agents that implement ChannelReceiver interface.
	if channelReceiver, ok := ag.(ChannelReceiver); ok {
		// Set up state notification channel for all ChannelReceiver agents.
		channelReceiver.SetStateNotificationChannel(d.stateChangeCh)

		// Determine agent type to provide appropriate channels.
		if agentDriver, ok := ag.(agent.Driver); ok {
			switch agentDriver.GetAgentType() {
			case agent.TypeArchitect:
				d.logger.Info("Attached architect agent: %s with direct channel setup", agentID)
				channelReceiver.SetChannels(d.specCh, d.questionsCh, replyCh)
				channelReceiver.SetDispatcher(d)
				return
			case agent.TypeCoder:
				d.logger.Info("Attached coder agent: %s with direct channel setup", agentID)
				// Coders receive story messages via storyCh.
				channelReceiver.SetChannels(d.storyCh, nil, replyCh)
				channelReceiver.SetDispatcher(d)
				return
			}
		}
	}

	// For other agents, log the attachment.
	if agentDriver, ok := ag.(agent.Driver); ok {
		switch agentDriver.GetAgentType() {
		case agent.TypeArchitect:
			d.logger.Info("Attached architect agent: %s", agentID)
		case agent.TypeCoder:
			d.logger.Info("Attached coder agent: %s", agentID)
		}
	} else {
		d.logger.Warn("Agent %s does not implement Driver interface", agentID)
	}
}

// Detach removes an agent and cleans up its channels (public method for orchestrator).
func (d *Dispatcher) Detach(agentID string) {
	d.detach(agentID)
}

// detach removes an agent and cleans up its channels.
func (d *Dispatcher) detach(agentID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Remove from agents map.
	delete(d.agents, agentID)

	// Close and remove reply channel.
	if replyCh, exists := d.replyChannels[agentID]; exists {
		close(replyCh)
		delete(d.replyChannels, agentID)
		d.logger.Info("Detached agent: %s and closed reply channel", agentID)
	}
}

// UnregisterAgent is deprecated - agents should use defer d.detach() after Attach().
func (d *Dispatcher) UnregisterAgent(agentID string) error {
	d.logger.Warn("UnregisterAgent is deprecated, use defer detach() instead for agent %s", agentID)
	d.detach(agentID)
	return nil
}

// routeToReplyCh routes ANSWER/RESULT messages to the appropriate coder's reply channel.
func (d *Dispatcher) routeToReplyCh(msg *proto.AgentMsg) {
	targetAgent := msg.ToAgent

	d.logger.Info("🔄 Routing %s %s to agent %s reply channel", msg.Type, msg.ID, targetAgent)

	// Find the reply channel for this agent.
	d.mu.RLock()
	replyCh, exists := d.replyChannels[targetAgent]
	d.mu.RUnlock()

	if !exists {
		d.logger.Warn("❌ No reply channel found for agent %s (message %s dropped)", targetAgent, msg.ID)
		return
	}

	// Send to reply channel (non-blocking with buffer size 1).
	select {
	case replyCh <- msg:
		d.logger.Info("✅ %s %s delivered to agent %s reply channel", msg.Type, msg.ID, targetAgent)
	default:
		d.logger.Warn("❌ Reply channel full for agent %s, dropping %s %s", targetAgent, msg.Type, msg.ID)
	}
}

// Start begins the dispatcher's message processing loop.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("dispatcher is already running")
	}
	d.running = true
	d.mu.Unlock()

	d.logger.Info("Starting dispatcher")

	// Use the run strategy to start execution
	if d.runStrat != nil {
		if err := d.runStrat.Run(d, ctx); err != nil {
			d.mu.Lock()
			d.running = false
			d.mu.Unlock()
			return fmt.Errorf("run strategy failed: %w", err)
		}
	}

	// Start container registry cleanup routine.
	// Check every 5 minutes for containers idle > 30 minutes.
	executor := exec.NewLongRunningDockerExec("alpine:latest", "") // Empty agentID - for cleanup only
	d.containerRegistry.StartCleanupRoutine(ctx, executor, 5*time.Minute, 30*time.Minute)

	return nil
}

// Stop gracefully shuts down the dispatcher.
func (d *Dispatcher) Stop(ctx context.Context) error {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return nil
	}
	d.running = false
	d.mu.Unlock()

	d.logger.Info("Stopping dispatcher")

	// Signal shutdown to all goroutines.
	close(d.shutdown)

	// Shutdown container registry cleanup routine.
	d.containerRegistry.Shutdown()

	// Use the run strategy for stopping
	if d.runStrat != nil {
		if err := d.runStrat.Stop(); err != nil {
			d.logger.Warn("Run strategy stop returned error: %v", err)
		}
	}

	// Wait for workers to finish BEFORE closing channels to prevent race conditions.
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.logger.Info("All workers finished, now closing channels")
		// Close all channels AFTER workers have finished.
		d.closeAllChannels()
		d.logger.Info("Dispatcher stopped successfully")
		return nil
	case <-ctx.Done():
		d.logger.Warn("Dispatcher stop timed out, closing channels anyway")
		// Close channels even on timeout to prevent resource leaks
		d.closeAllChannels()
		return logx.Wrap(ctx.Err(), "dispatcher stop timed out")
	}
}

// DispatchMessage routes a message to the appropriate agent or queue.
func (d *Dispatcher) DispatchMessage(msg *proto.AgentMsg) error {
	d.mu.RLock()
	running := d.running
	d.mu.RUnlock()

	if !running {
		return fmt.Errorf("dispatcher is not running")
	}

	// Note: Event logging will happen after name resolution in processMessage.
	// to ensure the logged message reflects the actual target agent.

	// For STORY messages, check if story channel has capacity first
	if msg.Type == proto.MsgTypeSTORY {
		// Check if story channel is full (non-blocking check)
		if len(d.storyCh) >= cap(d.storyCh) {
			d.logger.Warn("⚠️  Story channel full (%d/%d), could not deliver story %s", len(d.storyCh), cap(d.storyCh), msg.ID)
			return fmt.Errorf("story channel full, could not deliver story %s", msg.ID)
		}
	}

	select {
	case d.inputChan <- msg:
		// Enhanced logging for unified protocol messages
		kindInfo := ""
		if msg.Type == proto.MsgTypeREQUEST || msg.Type == proto.MsgTypeRESPONSE {
			if kindRaw, hasKind := msg.GetPayload(proto.KeyKind); hasKind {
				if kindStr, ok := kindRaw.(string); ok {
					kindInfo = fmt.Sprintf(" (kind: %s)", kindStr)
				}
			}
		}
		d.logger.Debug("Queued message %s: %s → %s%s", msg.ID, msg.FromAgent, msg.ToAgent, kindInfo)
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
		case msg, ok := <-d.inputChan:
			if !ok {
				d.logger.Info("Input channel closed, stopping message processor")
				return
			}
			d.processMessage(ctx, msg)
		}
	}
}

// metricsMonitor checks storyCh utilization and logs warnings for sustained high utilization.
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
			// Check storyCh utilization.
			if cap(d.storyCh) == 0 {
				continue // Avoid division by zero
			}

			utilization := float64(len(d.storyCh)) / float64(cap(d.storyCh))

			d.highUtilizationMu.Lock()
			if utilization > 0.8 {
				// High utilization detected.
				if d.highUtilizationStart.IsZero() {
					// First time detecting high utilization.
					d.highUtilizationStart = time.Now()
					d.logger.Debug("High storyCh utilization detected: %.2f%% - monitoring started", utilization*100)
				} else {
					// Check if sustained for more than 30 seconds.
					duration := time.Since(d.highUtilizationStart)
					if duration > 30*time.Second {
						d.logger.Warn("⚠️  SUSTAINED HIGH storyCh utilization: %.2f%% for %v (capacity: %d)", utilization*100, duration, cap(d.storyCh))
					}
				}
			} else {
				// Normal utilization, reset tracking.
				if !d.highUtilizationStart.IsZero() {
					d.logger.Debug("storyCh utilization back to normal: %.2f%%", utilization*100)
					d.highUtilizationStart = time.Time{}
				}
			}
			d.highUtilizationMu.Unlock()
		}
	}
}

// supervisor handles agent error reporting and fatal error cleanup.
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
		case agentErr, ok := <-d.errCh:
			if !ok {
				d.logger.Info("Error channel closed, stopping supervisor")
				return
			}
			// Log every error.
			d.logger.Warn("Agent error reported - ID: %s, Error: %s, Severity: %v",
				agentErr.ID, agentErr.Err.Error(), agentErr.Sev)

			// Handle fatal errors by detaching the agent.
			if agentErr.Sev == Fatal {
				d.logger.Error("Fatal error from agent %s, detaching", agentErr.ID)
				d.detach(agentErr.ID)

				// Check for zero-agent conditions.
				d.checkZeroAgentCondition()
			}
		}
	}
}

// checkZeroAgentCondition warns if there are no agents of a certain type.
func (d *Dispatcher) checkZeroAgentCondition() {
	d.mu.RLock()
	architectCount := 0
	coderCount := 0

	for _, ag := range d.agents {
		if agentDriver, ok := ag.(agent.Driver); ok {
			switch agentDriver.GetAgentType() {
			case agent.TypeArchitect:
				architectCount++
			case agent.TypeCoder:
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

// ReportError allows agents to report errors to the supervisor.
func (d *Dispatcher) ReportError(agentID string, err error, severity Severity) {
	select {
	case d.errCh <- AgentError{ID: agentID, Err: err, Sev: severity}:
		// Error reported successfully.
	default:
		// Error channel full, log directly.
		d.logger.Error("Error channel full, dropping error report from agent %s: %v", agentID, err)
	}
}

//nolint:cyclop // Complex message routing logic, acceptable for dispatcher
func (d *Dispatcher) processMessage(ctx context.Context, msg *proto.AgentMsg) {
	d.logger.Info("Processing message %s: %s → %s (%s)", msg.ID, msg.FromAgent, msg.ToAgent, msg.Type)

	// Validate story_id presence - only SPEC messages are allowed without story_id
	if msg.Type != proto.MsgTypeSPEC {
		storyIDRaw, hasStoryID := msg.GetPayload(proto.KeyStoryID)
		if !hasStoryID {
			d.logger.Error("❌ Message %s (%s) missing required story_id - rejecting at dispatcher level", msg.ID, msg.Type)
			d.sendErrorResponse(msg, fmt.Errorf("message %s (%s) missing required story_id", msg.ID, msg.Type))
			return
		}

		// Validate story_id is not empty using generics pattern
		storyIDStr, ok := utils.SafeAssert[string](storyIDRaw)
		if !ok || strings.TrimSpace(storyIDStr) == "" {
			d.logger.Error("❌ Message %s (%s) has empty story_id - rejecting at dispatcher level", msg.ID, msg.Type)
			d.sendErrorResponse(msg, fmt.Errorf("message %s (%s) has empty story_id", msg.ID, msg.Type))
			return
		}

		d.logger.Debug("✅ Message %s (%s) has valid story_id: %s", msg.ID, msg.Type, storyIDStr)
	}

	// Resolve logical agent name to actual agent ID for all messages.
	resolvedToAgent := d.resolveAgentName(msg.ToAgent)
	if resolvedToAgent != msg.ToAgent {
		d.logger.Debug("Resolved logical name %s to %s", msg.ToAgent, resolvedToAgent)
		msg.ToAgent = resolvedToAgent
	}

	// Route messages to appropriate queues based on type.
	switch msg.Type {
	case proto.MsgTypeSTORY:
		// STORY messages go to storyCh for coders to receive.
		d.logger.Info("🔄 Sending STORY %s to storyCh", msg.ID)

		// Send to storyCh (blocking send - buffer should prevent blocking).
		d.storyCh <- msg
		d.logger.Info("✅ STORY %s delivered to storyCh", msg.ID)

	case proto.MsgTypeSPEC:
		// SPEC messages go to architect via spec channel.
		d.logger.Info("🔄 Dispatcher sending SPEC %s to architect via spec channel %p", msg.ID, d.specCh)
		select {
		case d.specCh <- msg:
			d.logger.Info("✅ SPEC %s delivered to architect spec channel", msg.ID)
		default:
			d.logger.Warn("❌ Architect spec channel full, dropping SPEC %s", msg.ID)
		}

	case proto.MsgTypeREQUEST:
		// All REQUEST kinds go to questionsCh for architect to process
		// Architect will handle kind-based routing internally
		d.questionsCh <- msg
		// Note: REQUEST processing and persistence handled by architect

	case proto.MsgTypeRESPONSE:
		// RESPONSE messages go to specific coder's reply channel.
		d.routeToReplyCh(msg)

	default:
		// Other message types (ERROR, SHUTDOWN, etc.) still processed immediately.
		d.logger.Info("Processing message type %s immediately (not queued)", msg.Type)

		// Find target agent.
		d.mu.RLock()
		targetAgent, exists := d.agents[msg.ToAgent]
		d.mu.RUnlock()

		if !exists {
			d.sendErrorResponse(msg, fmt.Errorf("target agent %s not found", msg.ToAgent))
			return
		}

		// Process immediately for non-STORY messages.
		response := d.processWithRetry(ctx, msg, targetAgent)

		// If there's a response, route it to appropriate queue.
		if response.Message != nil {
			d.sendResponse(response.Message)
		} else if response.Error != nil {
			d.sendErrorResponse(msg, response.Error)
		}
	}
}

func (d *Dispatcher) processWithRetry(_ context.Context, msg *proto.AgentMsg, _ Agent) *Result {
	// SHUTDOWN messages bypass rate limiting
	if msg.Type != proto.MsgTypeSHUTDOWN {
		// Extract model name from agent logID (format: "model:id").
		modelName := msg.ToAgent
		if parts := strings.Split(msg.ToAgent, ":"); len(parts) >= 2 {
			modelName = parts[0]
		}

		// Check rate limiting before processing.
		if err := d.checkRateLimit(modelName); err != nil {
			d.logger.Warn("Rate limit exceeded for %s (model %s): %v", msg.ToAgent, modelName, err)
			return &Result{Error: err}
		}
	}

	// Process message - NOTE: ProcessMessage removed from Agent interface.
	// Agents now receive messages via channels exclusively.
	d.logger.Debug("Processing message %s for agent %s", msg.ID, msg.ToAgent)

	// Release agent slot after processing (only for non-SHUTDOWN messages).
	if msg.Type != proto.MsgTypeSHUTDOWN {
		// Extract model name for agent slot release
		modelName := msg.ToAgent
		if parts := strings.Split(msg.ToAgent, ":"); len(parts) >= 2 {
			modelName = parts[0]
		}
		if err := d.rateLimiter.ReleaseAgent(modelName); err != nil {
			d.logger.Warn("Failed to release agent slot for model %s: %v", modelName, err)
		}
	}

	// Messages flow through channels - no direct processing or retry needed here.
	// The retry logic would be handled at a higher level if needed.
	return &Result{Message: nil}
}

func (d *Dispatcher) checkRateLimit(agentModel string) error {
	// Reserve agent slot.
	if err := d.rateLimiter.ReserveAgent(agentModel); err != nil {
		return logx.Wrap(err, "failed to reserve agent slot")
	}

	// For now, we don't know token count in advance, so we'll reserve a default amount.
	// In a real implementation, this might be estimated or configured.
	defaultTokenReservation := 100

	if err := d.rateLimiter.Reserve(agentModel, defaultTokenReservation); err != nil {
		// Release the agent slot if token reservation fails.
		if releaseErr := d.rateLimiter.ReleaseAgent(agentModel); releaseErr != nil {
			d.logger.Warn("Failed to release agent slot for model %s: %v", agentModel, releaseErr)
		}
		return logx.Wrap(err, "failed to reserve tokens")
	}

	return nil
}

func (d *Dispatcher) sendResponse(response *proto.AgentMsg) {
	// Route response to appropriate queue based on message type.
	d.logger.Info("Routing response %s: %s → %s (%s)", response.ID, response.FromAgent, response.ToAgent, response.Type)

	// Resolve logical agent name to actual agent ID.
	resolvedToAgent := d.resolveAgentName(response.ToAgent)
	if resolvedToAgent != response.ToAgent {
		d.logger.Debug("Resolved logical name %s to %s", response.ToAgent, resolvedToAgent)
		response.ToAgent = resolvedToAgent
	}

	switch response.Type {
	case proto.MsgTypeREQUEST:
		// Approval requests go to questionsCh.
		select {
		case d.questionsCh <- response:
			d.logger.Debug("Queued REQUEST %s for architect", response.ID)
		default:
			d.logger.Warn("Questions channel full, dropping REQUEST %s", response.ID)
		}

	case proto.MsgTypeRESPONSE:
		// Route unified RESPONSE messages with kind logging
		kindRaw, hasKind := response.GetPayload(proto.KeyKind)
		kindStr := ""
		if hasKind {
			kindStr, _ = kindRaw.(string)
		}

		d.logger.Debug("Routing RESPONSE %s (kind: %s) to reply channel", response.ID, kindStr)
		d.routeToReplyCh(response)

	default:
		// Other types logged only.
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
}

// resolveAgentName resolves logical agent names to actual agent IDs.
func (d *Dispatcher) resolveAgentName(logicalName string) string {
	// If already an exact agent ID, return as-is.
	d.mu.RLock()
	if _, exists := d.agents[logicalName]; exists {
		d.mu.RUnlock()
		return logicalName
	}
	d.mu.RUnlock()

	// Map logical names to agent types.
	targetType := ""
	switch logicalName {
	case "architect":
		targetType = "architect"
	case "coder":
		targetType = "coder"
	default:
		// Not a logical name, return as-is.
		return logicalName
	}

	// Find first agent of the target type (deterministically sorted).
	d.mu.RLock()
	defer d.mu.RUnlock()

	// Collect matching agents and sort for consistent resolution
	var matchingAgents []string
	for agentID := range d.agents {
		// Agent IDs follow the pattern "type-id" (e.g., "architect-001", "coder-001")
		if strings.HasPrefix(agentID, targetType+"-") {
			matchingAgents = append(matchingAgents, agentID)
		}
	}

	if len(matchingAgents) > 0 {
		// Sort for deterministic resolution
		for i := 0; i < len(matchingAgents)-1; i++ {
			for j := i + 1; j < len(matchingAgents); j++ {
				if matchingAgents[i] > matchingAgents[j] {
					matchingAgents[i], matchingAgents[j] = matchingAgents[j], matchingAgents[i]
				}
			}
		}
		return matchingAgents[0]
	}

	// No agent found for this type, return original name (will cause "not found" error).
	return logicalName
}

// GetStats returns dispatcher statistics and status information.
func (d *Dispatcher) GetStats() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()

	agentList := make([]string, 0, len(d.agents))
	for agentID := range d.agents {
		agentList = append(agentList, agentID)
	}

	storyChUtilization := float64(len(d.storyCh)) / float64(cap(d.storyCh))

	// S-5: WARN if storyCh utilization > 0.8 (this should be logged separately for monitoring).
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

// GetQuestionsCh returns the questions channel for architect to receive from.
func (d *Dispatcher) GetQuestionsCh() <-chan *proto.AgentMsg {
	return d.questionsCh
}

// GetReplyCh returns the reply channel for a specific coder agent.
func (d *Dispatcher) GetReplyCh(agentID string) <-chan *proto.AgentMsg {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if replyCh, exists := d.replyChannels[agentID]; exists {
		return replyCh
	}
	return nil
}

// GetStoryCh returns the story channel for coders to receive from.
func (d *Dispatcher) GetStoryCh() <-chan *proto.AgentMsg {
	return d.storyCh
}

// GetStateChangeChannel returns the state change notification channel.
func (d *Dispatcher) GetStateChangeChannel() <-chan *proto.StateChangeNotification {
	return d.stateChangeCh
}

// ArchitectChannels contains the channels returned to architects.
type ArchitectChannels struct {
	Specs <-chan *proto.AgentMsg // Delivers spec messages
}

// SubscribeArchitect allows the architect to get the spec channel.
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

// closeAllChannels closes all dispatcher-owned channels for graceful shutdown.
func (d *Dispatcher) closeAllChannels() {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()

	// Close specCh.
	if d.specCh != nil {
		close(d.specCh)
		d.logger.Info("Closed spec channel")
	}

	// Close storyCh.
	if d.storyCh != nil {
		close(d.storyCh)
		d.logger.Info("Closed story channel")
	}

	// Close questionsCh.
	if d.questionsCh != nil {
		close(d.questionsCh)
		d.logger.Info("Closed questions channel")
	}

	// Close all reply channels.
	d.mu.Lock()
	for agentID, replyCh := range d.replyChannels {
		close(replyCh)
		d.logger.Info("Closed reply channel for agent: %s", agentID)
	}
	// Clear the map.
	d.replyChannels = make(map[string]chan *proto.AgentMsg)
	d.mu.Unlock()

	// Close error reporting channel.
	if d.errCh != nil {
		close(d.errCh)
		d.logger.Info("Closed error reporting channel")
	}

	// Close state change notification channel.
	if d.stateChangeCh != nil {
		close(d.stateChangeCh)
		d.logger.Info("Closed state change notification channel")
	}
}

// QueueHead represents a message head in a queue.
type QueueHead struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
	TS   string `json:"ts"`
}

// QueueInfo represents queue information.
//
//nolint:govet // JSON serialization struct, logical order preferred
type QueueInfo struct {
	Name   string      `json:"name"`
	Length int         `json:"length"`
	Heads  []QueueHead `json:"heads"`
}

// DumpHeads returns queue information with up to n message heads from each queue.
func (d *Dispatcher) DumpHeads(_ int) map[string]any {
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

// AgentInfo represents information about a registered agent.
type AgentInfo struct {
	Driver agent.Driver
	ID     string
	Type   agent.Type
	State  string
}

// GetRegisteredAgents returns information about all registered agents.
func (d *Dispatcher) GetRegisteredAgents() []AgentInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var agentInfos []AgentInfo
	for id, agentInterface := range d.agents {
		// Try to cast to Driver interface to get more information.
		if driver, ok := agentInterface.(agent.Driver); ok {
			agentInfos = append(agentInfos, AgentInfo{
				ID:     id,
				Type:   driver.GetAgentType(),
				State:  driver.GetCurrentState().String(),
				Driver: driver,
			})
		} else {
			// Fallback for agents that don't implement Driver interface.
			// Default to coder type (all new agents should implement Driver interface).
			agentInfos = append(agentInfos, AgentInfo{
				ID:     id,
				Type:   agent.TypeCoder, // Default fallback
				State:  "UNKNOWN",
				Driver: nil,
			})
		}
	}

	return agentInfos
}

// SetLease records that an agent is working on a specific story.
func (d *Dispatcher) SetLease(agentID, storyID string) {
	d.leasesMutex.Lock()
	defer d.leasesMutex.Unlock()
	d.leases[agentID] = storyID
	d.logger.Debug("Set lease: agent %s -> story %s", agentID, storyID)
}

// GetLease returns the story ID that an agent is working on, or empty string if none.
func (d *Dispatcher) GetLease(agentID string) string {
	d.leasesMutex.Lock()
	defer d.leasesMutex.Unlock()
	storyID := d.leases[agentID]
	return storyID
}

// ClearLease removes an agent's story assignment.
func (d *Dispatcher) ClearLease(agentID string) {
	d.leasesMutex.Lock()
	defer d.leasesMutex.Unlock()
	if storyID, exists := d.leases[agentID]; exists {
		delete(d.leases, agentID)
		d.logger.Debug("Cleared lease: agent %s was working on story %s", agentID, storyID)
	}
}

// SendRequeue sends a requeue message for the story currently assigned to the given agent.
func (d *Dispatcher) SendRequeue(agentID, reason string) error {
	storyID := d.GetLease(agentID)
	if storyID == "" {
		return fmt.Errorf("no lease found for agent %s", agentID)
	}

	// Create requeue message using unified REQUEST protocol.
	requeueMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "orchestrator", "architect")
	requeueMsg.SetPayload(proto.KeyKind, string(proto.RequestKindRequeue))
	requeueMsg.SetPayload(proto.KeyRequeue, proto.RequeueRequestPayload{
		StoryID: storyID,
		AgentID: agentID,
		Reason:  reason,
	})
	requeueMsg.SetPayload(proto.KeyCorrelationID, proto.GenerateCorrelationID())

	// Send to questions channel (same as existing requeue logic).
	select {
	case d.questionsCh <- requeueMsg:
		d.logger.Info("Requeued story %s for agent %s (reason: %s)", storyID, agentID, reason)
		return nil
	default:
		return fmt.Errorf("questions channel full, could not requeue story %s", storyID)
	}
}

// RequeueStory directly requeues a story by clearing the agent's lease.
// This makes the story available for reassignment to another agent.
func (d *Dispatcher) RequeueStory(agentID string) error {
	storyID := d.GetLease(agentID)
	if storyID == "" {
		return fmt.Errorf("no lease found for agent %s", agentID)
	}

	d.ClearLease(agentID)
	d.logger.Info("Requeued story %s from failed agent %s", storyID, agentID)
	return nil
}

// GetContainerRegistry returns the container registry for orchestrator access.
func (d *Dispatcher) GetContainerRegistry() *exec.ContainerRegistry {
	return d.containerRegistry
}
