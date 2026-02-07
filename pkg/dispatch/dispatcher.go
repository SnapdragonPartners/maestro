// Package dispatch provides message routing and agent coordination for the orchestrator.
// It manages agent communication channels, rate limiting, and message processing workflows.
//
// ## Channel Communication Patterns
//
// The dispatcher implements several channel-based communication patterns for clean, non-blocking
// coordination between agents. These patterns replace legacy API-based approaches with async channels.
//
// ### 1. Status Updates Pattern
// Used for updating story status from coders to architect:
//   - Coders call: dispatcher.UpdateStoryStatus(storyID, status)
//   - Dispatcher sends via: statusUpdatesCh chan *proto.StoryStatusUpdate
//   - Architect processes via: processStatusUpdates() goroutine worker
//   - Benefits: Non-blocking, fire-and-forget status updates
//
// ### 2. Requeue Requests Pattern
// Used for requeuing failed stories from supervisor to architect:
//   - Supervisor calls: dispatcher.UpdateStoryRequeue(storyID, agentID, reason)
//   - Dispatcher sends via: requeueRequestsCh chan *proto.StoryRequeueRequest
//   - Architect processes via: processRequeueRequests() goroutine worker
//   - Benefits: Replaces legacy ExternalAPIProvider with clean channel communication
//
// ### 3. State Change Notifications Pattern
// Used for notifying supervisor of agent state transitions:
//   - Agents send: stateChangeCh <- &proto.StateChangeNotification{}
//   - Supervisor processes via: handleStateChange() in main loop
//   - Benefits: Centralized lifecycle management and restart policies
//
// ### 4. Question/Answer Pattern
// Used for synchronous communication between coders and architect:
//   - Coders send QUESTION messages via: questionsCh
//   - Architect processes and sends ANSWER via: reply channels
//   - Benefits: Bidirectional communication for technical questions
//
// ### 5. Work Distribution Pattern
// Used for distributing ready stories to available coders:
//   - Architect sends TASK messages via: storyCh
//   - Coders pick up work from shared channel
//   - Benefits: Load balancing across multiple coder agents
//
// All channels use buffered queues with appropriate sizing for production load.
// Workers use context cancellation for graceful shutdown coordination.
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
// Different agent types use different channels:
// - Architect: questionsCh (incoming requests), unused (was specCh), replyCh (outgoing responses).
// - Coder: storyCh (incoming stories), unused, replyCh (outgoing responses).
// - PM: pmRequestsCh (incoming interview requests), unused, replyCh (outgoing responses).
type ChannelReceiver interface {
	SetChannels(incomingCh chan *proto.AgentMsg, unusedCh chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg)
	SetDispatcher(dispatcher *Dispatcher)
	SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification)
}

// Dispatcher coordinates message passing and work distribution between agents.
//
//nolint:govet // Large complex struct, logical grouping preferred over memory optimization
type Dispatcher struct {
	agents    map[string]Agent
	logger    *logx.Logger
	config    *config.Config
	inputChan chan *proto.AgentMsg
	shutdown  chan struct{}
	mu        sync.RWMutex
	wg        sync.WaitGroup
	running   bool

	// Phase 1: Channel-based queues.
	storyCh           chan *proto.AgentMsg            // Ready stories for any coder (replaces sharedWorkQueue)
	hotfixStoryCh     chan *proto.AgentMsg            // Ready hotfix stories for dedicated hotfix coder
	questionsCh       chan *proto.AgentMsg            // Questions/requests for architect (replaces architectRequestQueue)
	pmRequestsCh      chan *proto.AgentMsg            // Interview requests for PM agent
	statusUpdatesCh   chan *proto.StoryStatusUpdate   // Story status updates for architect (non-blocking)
	requeueRequestsCh chan *proto.StoryRequeueRequest // Story requeue requests for architect (non-blocking)

	// Phase 2: Per-agent reply channels.
	replyChannels map[string]chan *proto.AgentMsg // Per-agent reply channels for ANSWER/RESULT messages

	// Channel-based notifications for architect.
	notificationMu sync.RWMutex // Protects notification channels

	// S-5: Metrics monitoring.
	highUtilizationStart time.Time    // Track when high utilization started
	highUtilizationMu    sync.RWMutex // Protects high utilization tracking

	// Supervisor pattern for error handling.
	errCh chan AgentError // Channel for agent error reporting

	// Story lease tracking.
	leases      map[string]string // agent_id -> story_id
	leasesMutex sync.Mutex        // Protects lease map

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
func NewDispatcher(cfg *config.Config) (*Dispatcher, error) {
	return &Dispatcher{
		agents:            make(map[string]Agent),
		logger:            logx.NewLogger("dispatcher"),
		config:            cfg,
		inputChan:         make(chan *proto.AgentMsg, 100), // Buffered channel for message queue
		shutdown:          make(chan struct{}),
		running:           false,
		storyCh:           make(chan *proto.AgentMsg, config.StoryChannelFactor*cfg.Agents.MaxCoders), // S-5: Buffer size = factor × numCoders
		hotfixStoryCh:     make(chan *proto.AgentMsg, 10),                                             // Hotfix stories channel (dedicated coder)
		questionsCh:       make(chan *proto.AgentMsg, config.QuestionsChannelSize),                    // Buffer size from config
		pmRequestsCh:      make(chan *proto.AgentMsg, 10),                                             // Buffered channel for PM interview requests
		statusUpdatesCh:   make(chan *proto.StoryStatusUpdate, 100),                                   // Buffered channel for status updates
		requeueRequestsCh: make(chan *proto.StoryRequeueRequest, 100),                                 // Buffered channel for requeue requests
		replyChannels:     make(map[string]chan *proto.AgentMsg),                                      // Per-agent reply channels
		errCh:             make(chan AgentError, 10),                                                  // Buffered channel for error reporting
		stateChangeCh:     make(chan *proto.StateChangeNotification, 100),                             // Buffered channel for state change notifications
		leases:            make(map[string]string),                                                    // Story lease tracking
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
	if channelReceiver, ok := utils.SafeAssert[ChannelReceiver](ag); ok {
		// Set up state notification channel for all ChannelReceiver agents.
		channelReceiver.SetStateNotificationChannel(d.stateChangeCh)

		// Determine agent type to provide appropriate channels.
		if agentDriver, ok := utils.SafeAssert[agent.Driver](ag); ok {
			switch agentDriver.GetAgentType() {
			case agent.TypeArchitect:
				d.logger.Info("Attached architect agent: %s with direct channel setup", agentID)
				// Architect receives requests via questionsCh, unused (was specCh), replyCh for responses
				channelReceiver.SetChannels(d.questionsCh, nil, replyCh)
				channelReceiver.SetDispatcher(d)
				// TODO: Add status updates channel to architect interface
				return
			case agent.TypeCoder:
				// Hotfix coders get their own dedicated channel, normal coders share storyCh
				if strings.HasPrefix(agentID, "hotfix-") {
					d.logger.Info("Attached hotfix coder agent: %s with dedicated hotfix channel", agentID)
					channelReceiver.SetChannels(d.hotfixStoryCh, nil, replyCh)
				} else {
					d.logger.Info("Attached coder agent: %s with shared story channel", agentID)
					channelReceiver.SetChannels(d.storyCh, nil, replyCh)
				}
				channelReceiver.SetDispatcher(d)
				return
			case agent.TypePM:
				d.logger.Info("Attached PM agent: %s with direct channel setup", agentID)
				// PM receives interview requests via pmRequestsCh and gets reply channel for RESULT messages.
				// First channel is for PM interview requests, second is unused (nil), third is reply channel.
				channelReceiver.SetChannels(d.pmRequestsCh, nil, replyCh)
				channelReceiver.SetDispatcher(d)
				return
			}
		}
	}

	// For other agents, log the attachment.
	if agentDriver, ok := utils.SafeAssert[agent.Driver](ag); ok {
		switch agentDriver.GetAgentType() {
		case agent.TypeArchitect:
			d.logger.Info("Attached architect agent: %s", agentID)
		case agent.TypeCoder:
			d.logger.Info("Attached coder agent: %s", agentID)
		case agent.TypePM:
			d.logger.Info("Attached PM agent: %s", agentID)
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
	if registry := exec.GetGlobalRegistry(); registry != nil {
		executor := exec.NewLongRunningDockerExec("alpine:latest", "") // Empty agentID - for cleanup only
		registry.StartCleanupRoutine(ctx, executor, 5*time.Minute, 30*time.Minute)
	}

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

	// Stop all registered containers before shutting down.
	if registry := exec.GetGlobalRegistry(); registry != nil {
		d.logger.Info("📦 Stopping all registered containers...")
		if err := registry.StopAllContainersDirect(ctx); err != nil {
			d.logger.Warn("📦 Some containers failed to stop during shutdown: %v", err)
		}

		// Shutdown container registry cleanup routine.
		registry.Shutdown()
	}

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

	// For STORY messages, check if appropriate channel has capacity first
	if msg.Type == proto.MsgTypeSTORY {
		// Check if this is a hotfix story by examining the payload
		isHotfix := false
		if payload := msg.GetTypedPayload(); payload != nil {
			if payloadData, err := payload.ExtractGeneric(); err == nil {
				if hotfixVal, exists := payloadData[proto.KeyIsHotfix]; exists {
					if hotfix, ok := hotfixVal.(bool); ok {
						isHotfix = hotfix
					}
				}
			}
		}

		if isHotfix {
			// Check hotfix channel capacity
			if len(d.hotfixStoryCh) >= cap(d.hotfixStoryCh) {
				d.logger.Warn("⚠️  Hotfix story channel full (%d/%d), could not deliver story %s", len(d.hotfixStoryCh), cap(d.hotfixStoryCh), msg.ID)
				return fmt.Errorf("hotfix story channel full, could not deliver story %s", msg.ID)
			}
		} else {
			// Check normal story channel capacity
			if len(d.storyCh) >= cap(d.storyCh) {
				d.logger.Warn("⚠️  Story channel full (%d/%d), could not deliver story %s", len(d.storyCh), cap(d.storyCh), msg.ID)
				return fmt.Errorf("story channel full, could not deliver story %s", msg.ID)
			}
		}
	}

	select {
	case d.inputChan <- msg:
		// Enhanced logging for unified protocol messages
		kindInfo := ""
		if msg.Type == proto.MsgTypeREQUEST || msg.Type == proto.MsgTypeRESPONSE {
			if typedPayload := msg.GetTypedPayload(); typedPayload != nil {
				kindInfo = fmt.Sprintf(" (payload_kind: %s)", typedPayload.Kind)
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
		if agentDriver, ok := utils.SafeAssert[agent.Driver](ag); ok {
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

	// Validate story_id presence - only SPEC messages and story-independent messages are allowed without story_id
	// Story-independent messages include:
	// 1. REQUEST with ApprovalTypeSpec - spec approval requests from PM to architect
	// 2. REQUEST with HotfixRequestPayload - hotfix approval requests from PM to architect
	// 3. RESPONSE to PM - spec approval responses from architect to PM
	// 4. REQUEST to PM - interview requests (for future escalations)
	isStoryIndependentMessage := false
	if msg.Type == proto.MsgTypeREQUEST {
		// Check if this is a spec approval request by examining the approval type in the payload
		if payload := msg.GetTypedPayload(); payload != nil {
			if approvalPayload, err := payload.ExtractApprovalRequest(); err == nil {
				isStoryIndependentMessage = (approvalPayload.ApprovalType == proto.ApprovalTypeSpec)
			}
			// Check if this is a hotfix request (also story-independent - story created after approval)
			if !isStoryIndependentMessage {
				if _, err := payload.ExtractHotfixRequest(); err == nil {
					isStoryIndependentMessage = true
				}
			}
		}
		// Also allow PM-destined requests (interview requests from WebUI)
		if !isStoryIndependentMessage && strings.HasPrefix(msg.ToAgent, "pm-") {
			isStoryIndependentMessage = true
		}
	} else if msg.Type == proto.MsgTypeRESPONSE {
		// Check if this is a response to PM (spec approval responses)
		if strings.HasPrefix(msg.ToAgent, "pm-") {
			isStoryIndependentMessage = true
		}
	}

	if msg.Type != proto.MsgTypeSPEC && !isStoryIndependentMessage {
		// Story ID is now in metadata, not payload
		storyIDStr, hasStoryID := msg.Metadata[proto.KeyStoryID]
		if !hasStoryID {
			d.logger.Error("❌ Message %s (%s) missing required story_id in metadata - rejecting at dispatcher level", msg.ID, msg.Type)
			d.sendErrorResponse(msg, fmt.Errorf("message %s (%s) missing required story_id", msg.ID, msg.Type))
			return
		}

		// Validate story_id is not empty
		if strings.TrimSpace(storyIDStr) == "" {
			d.logger.Error("❌ Message %s (%s) has empty story_id - rejecting at dispatcher level", msg.ID, msg.Type)
			d.sendErrorResponse(msg, fmt.Errorf("message %s (%s) has empty story_id", msg.ID, msg.Type))
			return
		}

		d.logger.Debug("✅ Message %s (%s) has valid story_id: %s", msg.ID, msg.Type, storyIDStr)
	} else if isStoryIndependentMessage {
		d.logger.Debug("✅ Message %s (%s) is story-independent message - story_id not required", msg.ID, msg.Type)
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
		// STORY messages go to storyCh (normal) or hotfixStoryCh (hotfix) based on payload.
		// Check if this is a hotfix story by examining the payload
		isHotfix := false
		if payload := msg.GetTypedPayload(); payload != nil {
			if payloadData, err := payload.ExtractGeneric(); err == nil {
				if hotfixVal, exists := payloadData[proto.KeyIsHotfix]; exists {
					if hotfix, ok := hotfixVal.(bool); ok {
						isHotfix = hotfix
					}
				}
			}
		}

		if isHotfix {
			d.logger.Info("🔧 Sending HOTFIX STORY %s to hotfixStoryCh", msg.ID)
			select {
			case d.hotfixStoryCh <- msg:
				d.logger.Info("✅ HOTFIX STORY %s delivered to hotfixStoryCh", msg.ID)
			case <-d.shutdown:
				d.logger.Warn("❌ Shutdown in progress, dropping HOTFIX STORY %s", msg.ID)
				return
			default:
				d.logger.Warn("❌ Hotfix story channel full, dropping HOTFIX STORY %s", msg.ID)
			}
		} else {
			d.logger.Info("🔄 Sending STORY %s to storyCh", msg.ID)
			select {
			case d.storyCh <- msg:
				d.logger.Info("✅ STORY %s delivered to storyCh", msg.ID)
			case <-d.shutdown:
				d.logger.Warn("❌ Shutdown in progress, dropping STORY %s", msg.ID)
				return
			default:
				d.logger.Warn("❌ Story channel full, dropping STORY %s", msg.ID)
			}
		}

	case proto.MsgTypeREQUEST:
		// Route REQUEST messages based on target agent:
		// - PM-destined requests go to pmRequestsCh (interview/spec submissions)
		// - All other requests go to questionsCh for architect to process
		if strings.HasPrefix(msg.ToAgent, "pm-") {
			select {
			case d.pmRequestsCh <- msg:
				d.logger.Info("✅ REQUEST %s routed to PM", msg.ID)
			case <-d.shutdown:
				d.logger.Warn("❌ Shutdown in progress, dropping PM REQUEST %s", msg.ID)
				return
			default:
				d.logger.Warn("❌ PM requests channel full, dropping REQUEST %s", msg.ID)
			}
		} else {
			// Architect REQUEST processing
			select {
			case d.questionsCh <- msg:
				// Note: REQUEST processing and persistence handled by architect
			case <-d.shutdown:
				d.logger.Warn("❌ Shutdown in progress, dropping REQUEST %s", msg.ID)
				return
			default:
				d.logger.Warn("❌ Questions channel full, dropping REQUEST %s", msg.ID)
			}
		}

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
	// Process message - NOTE: ProcessMessage removed from Agent interface.
	// Agents now receive messages via channels exclusively.
	d.logger.Debug("Processing message %s for agent %s", msg.ID, msg.ToAgent)

	// Messages flow through channels - no direct processing or retry needed here.
	// The retry logic would be handled at a higher level if needed.
	return &Result{Message: nil}
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
		kindStr := ""
		if typedPayload := response.GetTypedPayload(); typedPayload != nil {
			kindStr = string(typedPayload.Kind)
		}

		d.logger.Debug("Routing RESPONSE %s (payload_kind: %s) to reply channel", response.ID, kindStr)
		d.routeToReplyCh(response)

	default:
		// Other types logged only.
		d.logger.Debug("Response message %s of type %s logged only", response.ID, response.Type)
	}
}

func (d *Dispatcher) sendErrorResponse(originalMsg *proto.AgentMsg, err error) {
	errorMsg := proto.NewAgentMsg(proto.MsgTypeERROR, "dispatcher", originalMsg.FromAgent)
	errorMsg.ParentMsgID = originalMsg.ID

	// Set typed error payload
	errorPayload := map[string]any{
		"error":               err.Error(),
		"original_message_id": originalMsg.ID,
	}
	errorMsg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindError, errorPayload))
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

// GetAgent returns an agent by ID, or nil if not found.
// The caller must type assert to the specific agent type if needed.
func (d *Dispatcher) GetAgent(agentID string) Agent {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.agents[agentID]
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

// GetStatusUpdatesChannel returns the story status updates channel.
func (d *Dispatcher) GetStatusUpdatesChannel() <-chan *proto.StoryStatusUpdate {
	return d.statusUpdatesCh
}

// GetRequeueRequestsChannel returns the story requeue requests channel.
func (d *Dispatcher) GetRequeueRequestsChannel() <-chan *proto.StoryRequeueRequest {
	return d.requeueRequestsCh
}

// Note: SubscribeArchitect and ArchitectChannels have been removed.
// Specs now come through REQUEST messages like all other architect work.

// closeAllChannels closes all dispatcher-owned channels for graceful shutdown.
func (d *Dispatcher) closeAllChannels() {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()

	// Close storyCh.
	if d.storyCh != nil {
		close(d.storyCh)
		d.logger.Info("Closed story channel")
	}

	// Close hotfixStoryCh.
	if d.hotfixStoryCh != nil {
		close(d.hotfixStoryCh)
		d.logger.Info("Closed hotfix story channel")
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

	if d.statusUpdatesCh != nil {
		close(d.statusUpdatesCh)
		d.logger.Info("Closed status updates channel")
	}

	if d.requeueRequestsCh != nil {
		close(d.requeueRequestsCh)
		d.logger.Info("Closed requeue requests channel")
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
	Driver    agent.Driver
	ID        string
	Type      agent.Type
	State     string
	ModelName string
	StoryID   string
}

// GetRegisteredAgents returns information about all registered agents.
//
//nolint:cyclop // Complexity from multiple agent type cases, acceptable for this function
func (d *Dispatcher) GetRegisteredAgents() []AgentInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var agentInfos []AgentInfo
	for id, agentInterface := range d.agents {
		// Try to cast to Driver interface to get more information.
		if driver, ok := utils.SafeAssert[agent.Driver](agentInterface); ok {
			// Get story ID from lease tracking
			storyID := d.GetLease(id)

			// Get model name - try to get it from agent config in state data
			modelName := ""
			stateData := driver.GetStateData()

			// Try to get model name from different state keys
			if modelNameVal, ok := stateData["model_name"]; ok {
				if name, ok := modelNameVal.(string); ok {
					modelName = name
				}
			}

			// If not found, try to get it from config path
			if modelName == "" {
				if cfg, err := config.GetConfig(); err == nil {
					// Use the appropriate model based on agent type
					switch driver.GetAgentType() {
					case agent.TypeArchitect:
						if cfg.Agents != nil && cfg.Agents.ArchitectModel != "" {
							modelName = cfg.Agents.ArchitectModel
						}
					case agent.TypeCoder:
						if cfg.Agents != nil && cfg.Agents.CoderModel != "" {
							modelName = cfg.Agents.CoderModel
						}
					case agent.TypePM:
						if cfg.Agents != nil && cfg.Agents.PMModel != "" {
							modelName = cfg.Agents.PMModel
						}
					}
				}
			}

			agentInfos = append(agentInfos, AgentInfo{
				ID:        id,
				Type:      driver.GetAgentType(),
				State:     driver.GetCurrentState().String(),
				ModelName: modelName,
				StoryID:   storyID,
				Driver:    driver,
			})
		} else {
			// Fallback for agents that don't implement Driver interface.
			// Default to coder type (all new agents should implement Driver interface).
			agentInfos = append(agentInfos, AgentInfo{
				ID:        id,
				Type:      agent.TypeCoder, // Default fallback
				State:     "UNKNOWN",
				ModelName: "",
				StoryID:   "",
				Driver:    nil,
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

// GetStoryForAgent returns the story ID that an agent is currently working on.
// This is a semantic alias for GetLease to improve code readability in multi-context scenarios.
func (d *Dispatcher) GetStoryForAgent(agentID string) string {
	return d.GetLease(agentID)
}

// SendRequeue sends a requeue message for the story currently assigned to the given agent.
func (d *Dispatcher) SendRequeue(agentID, reason string) error {
	storyID := d.GetLease(agentID)
	if storyID == "" {
		return fmt.Errorf("no lease found for agent %s", agentID)
	}

	// Create requeue message using unified REQUEST protocol.
	requeueMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "orchestrator", "architect")

	// Set typed requeue request payload
	requeuePayload := &proto.RequeueRequestPayload{
		StoryID: storyID,
		AgentID: agentID,
		Reason:  reason,
	}
	requeueMsg.SetTypedPayload(proto.NewRequeueRequestPayload(requeuePayload))

	// Store correlation_id in metadata
	requeueMsg.SetMetadata("correlation_id", proto.GenerateCorrelationID())

	// Send to questions channel (same as existing requeue logic).
	select {
	case d.questionsCh <- requeueMsg:
		d.logger.Info("Requeued story %s for agent %s (reason: %s)", storyID, agentID, reason)
		return nil
	default:
		return fmt.Errorf("questions channel full, could not requeue story %s", storyID)
	}
}

// UpdateStoryStatus provides a non-blocking way for coders to update story status.
// This sends a simple status update notification to the architect via a dedicated channel.
func (d *Dispatcher) UpdateStoryStatus(storyID, status string) error {
	d.logger.Info("🔄 Sending story %s status update to %s via status channel", storyID, status)

	// Create a simple status update notification
	statusUpdate := &proto.StoryStatusUpdate{
		StoryID:   storyID,
		Status:    status,
		Timestamp: time.Now().UTC(),
		AgentID:   "dispatcher", // Could be enhanced to track the actual requesting agent
	}

	// Send to status updates channel (non-blocking)
	select {
	case d.statusUpdatesCh <- statusUpdate:
		d.logger.Info("✅ Story %s status update sent to architect", storyID)
		return nil
	default:
		d.logger.Warn("❌ Status updates channel full, dropping status update for story %s", storyID)
		return fmt.Errorf("status updates channel full")
	}
}

// UpdateStoryRequeue sends a requeue request to the architect via the requeue channel.
// This replaces the legacy ExternalAPIProvider pattern with clean channel communication.
func (d *Dispatcher) UpdateStoryRequeue(storyID, agentID, reason string) error {
	d.logger.Info("🔄 Sending story %s requeue request from agent %s via requeue channel: %s", storyID, agentID, reason)

	// Create a requeue request notification
	requeueRequest := &proto.StoryRequeueRequest{
		StoryID:   storyID,
		AgentID:   agentID,
		Reason:    reason,
		Timestamp: time.Now().UTC(),
	}

	// Send to requeue requests channel (non-blocking)
	select {
	case d.requeueRequestsCh <- requeueRequest:
		d.logger.Info("✅ Story %s requeue request sent to architect", storyID)
		return nil
	default:
		d.logger.Warn("❌ Requeue requests channel full, dropping requeue request for story %s", storyID)
		return fmt.Errorf("requeue requests channel full")
	}
}
