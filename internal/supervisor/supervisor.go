// Package supervisor manages agent lifecycle and restart policies.
// It consolidates the state change processing logic that was previously embedded
// in the main orchestrator and missing from bootstrap.
package supervisor

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"orchestrator/internal/factory"
	"orchestrator/internal/kernel"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/github"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// RestartAction defines what to do when an agent reaches a terminal state.
type RestartAction int

const (
	// RestartAgent indicates the agent should be restarted for new work.
	RestartAgent RestartAction = iota
	// FatalShutdown indicates the system should shut down (unrecoverable error).
	FatalShutdown
)

// ShutdownHandler provides an abstraction for system shutdown operations.
// This allows for graceful shutdown and alternative behaviors (e.g., testing, recovery).
type ShutdownHandler interface {
	// Shutdown initiates system shutdown with the given exit code and reason.
	Shutdown(exitCode int, reason string)
}

// DefaultShutdownHandler implements immediate process termination.
type DefaultShutdownHandler struct {
	logger *logx.Logger
}

// NewDefaultShutdownHandler creates a shutdown handler that calls os.Exit.
func NewDefaultShutdownHandler(logger *logx.Logger) *DefaultShutdownHandler {
	return &DefaultShutdownHandler{logger: logger}
}

// Shutdown performs immediate process termination.
func (h *DefaultShutdownHandler) Shutdown(exitCode int, reason string) {
	h.logger.Error("FATAL SHUTDOWN: %s (exit code: %d)", reason, exitCode)
	os.Exit(exitCode)
}

// GracefulShutdownHandler implements graceful shutdown with cleanup.
// This handler performs cleanup operations before terminating the process.
type GracefulShutdownHandler struct {
	logger          *logx.Logger
	cleanupFunc     func() error // Optional cleanup function to run before exit
	shutdownChannel chan int     // Optional channel to signal shutdown instead of os.Exit
}

// NewGracefulShutdownHandler creates a shutdown handler with optional cleanup.
// If shutdownChannel is provided, it signals shutdown via channel instead of os.Exit (useful for testing).
func NewGracefulShutdownHandler(logger *logx.Logger, cleanupFunc func() error, shutdownChannel chan int) *GracefulShutdownHandler {
	return &GracefulShutdownHandler{
		logger:          logger,
		cleanupFunc:     cleanupFunc,
		shutdownChannel: shutdownChannel,
	}
}

// Shutdown performs graceful shutdown with cleanup operations.
func (h *GracefulShutdownHandler) Shutdown(exitCode int, reason string) {
	h.logger.Error("GRACEFUL SHUTDOWN: %s (exit code: %d)", reason, exitCode)

	// Run cleanup if provided
	if h.cleanupFunc != nil {
		h.logger.Info("Running cleanup operations before shutdown...")
		if err := h.cleanupFunc(); err != nil {
			h.logger.Error("Cleanup failed: %v", err)
		} else {
			h.logger.Info("Cleanup completed successfully")
		}
	}

	// Signal via channel if provided (for testing/controlled shutdown)
	if h.shutdownChannel != nil {
		select {
		case h.shutdownChannel <- exitCode:
			h.logger.Info("Shutdown signal sent via channel")
		default:
			h.logger.Warn("Shutdown channel full, falling back to os.Exit")
			os.Exit(exitCode)
		}
	} else {
		// Default to immediate termination
		os.Exit(exitCode)
	}
}

// RestartPolicy defines how to handle agent state transitions.
type RestartPolicy struct {
	OnDone  map[string]RestartAction // Actions when agents reach DONE state
	OnError map[string]RestartAction // Actions when agents reach ERROR state
}

// DefaultRestartPolicy returns the standard restart policy for the orchestrator.
func DefaultRestartPolicy() RestartPolicy {
	return RestartPolicy{
		OnDone: map[string]RestartAction{
			string(agent.TypeCoder):     RestartAgent, // Coders restart for next story
			string(agent.TypeArchitect): RestartAgent, // Architects restart for next spec
			string(agent.TypePM):        RestartAgent, // PM restarts for next interview
		},
		OnError: map[string]RestartAction{
			string(agent.TypeCoder):     RestartAgent,  // Coders restart after errors
			string(agent.TypeArchitect): FatalShutdown, // Architect errors are fatal
			string(agent.TypePM):        RestartAgent,  // PM restarts after errors (interview failures non-fatal)
		},
	}
}

// Supervisor manages agent lifecycle, restart policies, and state change processing.
// It consolidates the logic that was previously scattered across the orchestrator.
//
//nolint:govet // Logical grouping preferred over memory optimization for this complex struct
type Supervisor struct {
	Kernel          *kernel.Kernel
	Factory         *factory.AgentFactory
	Logger          *logx.Logger
	Policy          RestartPolicy
	ShutdownHandler ShutdownHandler // Encapsulated shutdown for graceful termination and testing

	// Agent tracking (preserves existing patterns)
	Agents        map[string]dispatch.Agent
	AgentTypes    map[string]string
	AgentContexts map[string]context.CancelFunc // Context management for graceful shutdown

	// Graceful shutdown tracking
	agentWg sync.WaitGroup // Tracks running agent goroutines for graceful shutdown

	// Runtime state
	running bool

	// SUSPEND state support
	restoreCh        chan struct{} // Broadcast channel for service recovery
	suspendedAgents  map[string]bool
	suspendFailureID map[string]string // agentID → failure record ID for resolution tracking
	suspendMu        sync.Mutex
	pollCancel       context.CancelFunc // Cancel function for API polling goroutine

	// Coding watchdog: between-turns activity tracking
	lastActivity    map[string]time.Time   // agentID → last toolloop iteration start
	agentStates     map[string]proto.State // agentID → current FSM state (for watchdog filtering)
	agentGeneration map[string]uint64      // agentID → monotonic generation counter (prevents stale goroutine restarts)
	activityMu      sync.Mutex
}

// NewSupervisor creates a new supervisor with the given kernel.
func NewSupervisor(k *kernel.Kernel) *Supervisor {
	logger := logx.NewLogger("supervisor")

	// Use the kernel's chat service (already initialized with scanner and config)
	// Don't create a duplicate chat service
	chatService := k.ChatService

	// Create the agent factory with kernel dependencies (lightweight)
	// Pass the shared LLM factory to ensure proper rate limiting across all agents
	agentFactory := factory.NewAgentFactory(k.Dispatcher, k.PersistenceChannel, chatService, k.LLMFactory, k.ComposeRegistry)

	supervisor := &Supervisor{
		Kernel:          k,
		Factory:         agentFactory,
		Logger:          logger,
		Policy:          DefaultRestartPolicy(),
		ShutdownHandler: NewDefaultShutdownHandler(logger), // Default to immediate shutdown
		Agents:          make(map[string]dispatch.Agent),
		AgentTypes:      make(map[string]string),
		AgentContexts:   make(map[string]context.CancelFunc),
		running:         false,
		// SUSPEND support - channel is created lazily when first agent suspends
		suspendedAgents:  make(map[string]bool),
		suspendFailureID: make(map[string]string),
		// Coding watchdog
		lastActivity:    make(map[string]time.Time),
		agentStates:     make(map[string]proto.State),
		agentGeneration: make(map[string]uint64),
	}

	// Wire up the restore channel to the factory for SUSPEND state support
	agentFactory.SetRestoreChannel(supervisor.GetRestoreChannel())

	return supervisor
}

// Start begins the supervisor's state change processing loop.
// This consolidates the startStateChangeProcessor logic from the old orchestrator.
func (s *Supervisor) Start(ctx context.Context) {
	if s.running {
		s.Logger.Warn("Supervisor already running")
		return
	}

	s.Logger.Info("Starting supervisor state change processor")

	// Get state change notifications from dispatcher
	stateChangeCh := s.Kernel.Dispatcher.GetStateChangeChannel()
	s.Logger.Info("State change processor got channel from dispatcher: %p", stateChangeCh)

	// Get agent cancellation requests from dispatcher
	cancelRequestsCh := s.Kernel.Dispatcher.GetCancelRequestsChannel()

	s.running = true

	// Start coding watchdog for between-turns timeout detection
	go s.startCodingWatchdog(ctx)

	// Start state change processing goroutine
	go func() {
		defer func() {
			s.running = false
			s.Logger.Info("Supervisor state change processor stopped")
		}()

		for {
			select {
			case <-ctx.Done():
				s.Logger.Info("State change processor stopping due to context cancellation")
				return

			case notification, ok := <-stateChangeCh:
				if !ok {
					s.Logger.Info("State change channel closed, stopping processor")
					return
				}

				if notification != nil {
					s.Logger.Info("🔔 Received state change notification: %s %s -> %s",
						notification.AgentID, notification.FromState, notification.ToState)
					s.handleStateChange(ctx, notification)
				} else {
					s.Logger.Warn("Received nil state change notification")
				}

			case cancelReq, ok := <-cancelRequestsCh:
				if !ok {
					s.Logger.Info("Cancel requests channel closed")
					continue
				}
				if cancelReq != nil {
					s.handleAgentCancel(ctx, cancelReq)
				}
			}
		}
	}()
}

// handleStateChange processes individual state change notifications.
// This consolidates and extends the handleStateChange logic from the old orchestrator.
func (s *Supervisor) handleStateChange(ctx context.Context, notification *proto.StateChangeNotification) {
	s.Logger.Info("Agent %s state changed: %s -> %s",
		notification.AgentID, notification.FromState, notification.ToState)

	s.activityMu.Lock()
	s.agentStates[notification.AgentID] = notification.ToState
	s.activityMu.Unlock()

	// Determine agent type from stored configuration
	agentType := s.getAgentType(notification.AgentID)
	if agentType == "" {
		s.Logger.Error("Unknown agent type for agent %s", notification.AgentID)
		return
	}

	// Handle DONE state transitions
	if notification.ToState == proto.StateDone {
		action := s.Policy.OnDone[agentType]
		s.handleStateAction(ctx, notification, action, "DONE")
	}

	// Handle ERROR state transitions
	if notification.ToState == proto.StateError {
		action := s.Policy.OnError[agentType]

		// CRITICAL: For coder errors, requeue the story before restart
		if agentType == string(agent.TypeCoder) {
			s.Logger.Info("Requeuing story for failed agent %s", notification.AgentID)
			// Get the story ID from the agent's lease
			storyID := s.Kernel.Dispatcher.GetLease(notification.AgentID)
			if storyID == "" {
				s.Logger.Error("No story lease found for failed agent %s", notification.AgentID)
			} else {
				// Clear the lease first
				s.Kernel.Dispatcher.ClearLease(notification.AgentID)

				// Extract structured failure info from notification metadata (if present)
				reason := "Agent failed with error state"
				var failureInfo *proto.FailureInfo
				if notification.Metadata != nil {
					// Use value type assertion — FailureInfo is stored as value in metadata
					if fi, ok := notification.Metadata[proto.KeyFailureInfo].(proto.FailureInfo); ok {
						failureInfo = &fi
						reason = fmt.Sprintf("%s: %s", fi.Kind, fi.Explanation)
						s.Logger.Info("Coder %s failed with classified failure: %s (%s)", notification.AgentID, fi.Kind, fi.Explanation)
					}
				}

				// Use the clean channel-based requeue pattern
				if err := s.Kernel.Dispatcher.UpdateStoryRequeue(storyID, notification.AgentID, reason, failureInfo); err != nil {
					s.Logger.Error("Failed to requeue story %s from agent %s: %v", storyID, notification.AgentID, err)
				} else {
					s.Logger.Info("Successfully requeued story %s from failed agent %s", storyID, notification.AgentID)
				}
			}
		}

		s.handleStateAction(ctx, notification, action, "ERROR")
	}

	// Handle SUSPEND state transitions
	if notification.ToState == proto.StateSuspend {
		s.handleAgentSuspend(ctx, notification)
	}

	// Handle agent leaving SUSPEND state (recovery)
	if notification.FromState == proto.StateSuspend && notification.ToState != proto.StateSuspend {
		s.handleAgentResume(notification.AgentID, notification.ToState)
	}
}

// handleStateAction executes the appropriate action based on the restart policy.
func (s *Supervisor) handleStateAction(ctx context.Context, notification *proto.StateChangeNotification, action RestartAction, stateType string) {
	agentType := s.getAgentType(notification.AgentID)

	switch action {
	case RestartAgent:
		s.Logger.Info("Agent %s (%s) reached %s state, restarting...",
			notification.AgentID, agentType, stateType)

		if err := s.restartAgent(ctx, notification.AgentID); err != nil {
			s.Logger.Error("Failed to restart agent %s: %v", notification.AgentID, err)
		}

	case FatalShutdown:
		s.Logger.Error("Agent %s (%s) reached %s state, triggering fatal shutdown",
			notification.AgentID, agentType, stateType)

		// Use encapsulated shutdown handler for graceful termination
		reason := fmt.Sprintf("Agent %s (%s) reached %s state", notification.AgentID, agentType, stateType)
		s.ShutdownHandler.Shutdown(1, reason)

	default:
		s.Logger.Info("No action configured for agent %s (%s) in %s state",
			notification.AgentID, agentType, stateType)
	}
}

// handleAgentCancel processes an agent cancellation request from the architect.
// Verifies the agent is still working on the expected story before cancelling,
// to avoid cancelling unrelated work if the request is stale.
func (s *Supervisor) handleAgentCancel(ctx context.Context, req *proto.AgentCancelRequest) {
	s.Logger.Info("🛑 Agent cancel request: agent=%s story=%s reason=%s",
		req.AgentID, req.StoryID, req.Reason)

	// Verify the agent is still leased to the expected story — if it has
	// moved on to a different story, this cancel request is stale.
	currentStory := s.Kernel.Dispatcher.GetLease(req.AgentID)
	if currentStory != "" && currentStory != req.StoryID {
		s.Logger.Warn("⏭️ Ignoring stale cancel for agent %s: was for story %s but agent now on %s",
			req.AgentID, req.StoryID, currentStory)
		return
	}

	// Clear the story lease so the story isn't double-assigned on restart
	s.Kernel.Dispatcher.ClearLease(req.AgentID)

	// Restart the agent (cancels context, creates fresh instance)
	if err := s.restartAgent(ctx, req.AgentID); err != nil {
		s.Logger.Error("❌ Failed to cancel agent %s: %v", req.AgentID, err)
	} else {
		s.Logger.Info("✅ Agent %s cancelled and restarted (was working on story %s)", req.AgentID, req.StoryID)
	}
}

// restartAgent handles restarting an individual agent.
// This creates a completely fresh agent instance with no state preservation.
func (s *Supervisor) restartAgent(ctx context.Context, agentID string) error {
	s.Logger.Info("Restarting agent: %s", agentID)

	// Get the agent type
	agentType := s.getAgentType(agentID)
	if agentType == "" {
		return fmt.Errorf("unknown agent type for %s", agentID)
	}

	// Terminate existing agent by cancelling its context
	s.activityMu.Lock()
	cancelFunc, exists := s.AgentContexts[agentID]
	s.activityMu.Unlock()
	if exists {
		s.Logger.Info("Cancelling context for agent: %s", agentID)
		cancelFunc()
	}

	// Clean up tracking maps
	s.cleanupAgentResources(agentID)

	// Create new agent using the lightweight factory
	newAgent, err := s.Factory.NewAgent(ctx, agentID, agentType)
	if err != nil {
		return fmt.Errorf("failed to create agent %s: %w", agentID, err)
	}

	// Register the newly created agent with fresh context
	s.RegisterAgent(ctx, agentID, agentType, newAgent)

	s.Logger.Info("Agent %s successfully recreated and registered", agentID)
	return nil
}

// cleanupAgentResources performs cleanup for a terminated agent.
// This only cleans up tracking maps - work directory cleanup happens in agent SETUP state.
func (s *Supervisor) cleanupAgentResources(agentID string) {
	s.Logger.Info("Cleaning up resources for agent: %s", agentID)

	// Remove from tracking maps under activityMu to synchronize with the watchdog's
	// concurrent reads of AgentTypes and AgentContexts in checkCodingActivity.
	s.activityMu.Lock()
	delete(s.Agents, agentID)
	delete(s.AgentTypes, agentID)
	delete(s.AgentContexts, agentID)
	delete(s.agentStates, agentID)
	// Note: agentGeneration is NOT deleted here — stale goroutines need to see the
	// incremented generation to avoid double-restarts. It's cleaned up lazily.
	s.activityMu.Unlock()

	// Work directory cleanup is handled by agent SETUP state for fresh workspace
}

// handleUnexpectedExit detects when an agent's Run() goroutine exits without
// a corresponding DONE/ERROR state notification (e.g., watchdog context cancellation)
// and restarts the agent to prevent permanent loss.
//
// The generation parameter prevents races with the normal restart path: if a DONE/ERROR
// notification already triggered restartAgent (which increments the generation via
// RegisterAgent), the old goroutine sees a stale generation and skips the restart.
func (s *Supervisor) handleUnexpectedExit(ctx context.Context, agentID string, generation uint64) {
	// System shutdown — don't restart anything
	if ctx.Err() != nil {
		s.Logger.Info("Agent %s exited during system shutdown, not restarting", agentID)
		return
	}

	// Check if this goroutine's generation is still current. If a DONE/ERROR state
	// notification already triggered restartAgent → cleanupAgentResources → RegisterAgent,
	// the generation will have been incremented and this goroutine is stale.
	s.activityMu.Lock()
	currentGen := s.agentGeneration[agentID]
	s.activityMu.Unlock()

	if generation != currentGen {
		s.Logger.Debug("Agent %s exit handler skipped: generation %d is stale (current: %d)", agentID, generation, currentGen)
		return
	}

	// Only restart coder agents — architect/PM unexpected exits are fatal.
	// If agent type metadata is missing (e.g., cleanupAgentResources ran during a failed
	// prior restart attempt), treat as restartable since coders are the common case.
	if agentType := s.getAgentType(agentID); agentType == "" {
		s.Logger.Warn("Agent %s (gen %d) exited unexpectedly but agent type metadata is missing; attempting restart as coder", agentID, generation)
	} else if agentType != string(agent.TypeCoder) {
		s.Logger.Error("Non-coder agent %s (%s) exited unexpectedly without state notification", agentID, agentType)
		return
	}

	s.Logger.Warn("🔄 Agent %s (gen %d) exited without state notification (likely watchdog kill), restarting", agentID, generation)

	// Requeue the story if the agent had one
	storyID := s.Kernel.Dispatcher.GetLease(agentID)
	if storyID != "" {
		s.Kernel.Dispatcher.ClearLease(agentID)
		if err := s.Kernel.Dispatcher.UpdateStoryRequeue(storyID, agentID, "agent killed by watchdog without state notification", nil); err != nil {
			s.Logger.Error("Failed to requeue story %s from unexpectedly exited agent %s: %v", storyID, agentID, err)
		} else {
			s.Logger.Info("Requeued story %s from unexpectedly exited agent %s", storyID, agentID)
		}
	}

	if err := s.restartAgent(ctx, agentID); err != nil {
		s.Logger.Error("Failed to restart unexpectedly exited agent %s: %v", agentID, err)
	}
}

// getAgentType returns the type of an agent by ID.
// This preserves the getAgentType logic from the old orchestrator.
func (s *Supervisor) getAgentType(agentID string) string {
	s.activityMu.Lock()
	agentType, exists := s.AgentTypes[agentID]
	s.activityMu.Unlock()
	if exists {
		return agentType
	}
	return ""
}

// RegisterAgent adds an agent to the supervisor's tracking and starts its state machine.
// Creates individual context for the agent to enable graceful shutdown.
func (s *Supervisor) RegisterAgent(ctx context.Context, agentID, agentType string, agent dispatch.Agent) {
	// All map writes under activityMu to synchronize with the watchdog's
	// concurrent reads of AgentTypes/AgentContexts in checkCodingActivity.
	s.activityMu.Lock()
	s.AgentTypes[agentID] = agentType
	s.Agents[agentID] = agent
	s.agentGeneration[agentID]++
	gen := s.agentGeneration[agentID]
	if stateGetter, ok := agent.(interface{ GetCurrentState() proto.State }); ok {
		if state := stateGetter.GetCurrentState(); state != "" {
			s.agentStates[agentID] = state
		} else {
			s.agentStates[agentID] = proto.StateWaiting
		}
	} else {
		s.agentStates[agentID] = proto.StateWaiting
	}
	s.activityMu.Unlock()
	s.Logger.Info("Registered agent %s (type: %s, gen: %d)", agentID, agentType, gen)

	// Wire up activity tracker for coder agents (watchdog monitoring)
	if agentType == "coder" {
		type activityTrackerSetter interface {
			SetActivityTracker(tracker toolloop.ActivityTracker)
		}
		if ats, ok := agent.(activityTrackerSetter); ok {
			ats.SetActivityTracker(s)
		}
	}

	// Start the agent's state machine with individual context
	if runnable, ok := agent.(interface{ Run(context.Context) error }); ok {
		// Create individual context for this agent (child of main context)
		agentCtx, cancel := context.WithCancel(ctx)
		s.activityMu.Lock()
		s.AgentContexts[agentID] = cancel
		s.activityMu.Unlock()

		// Track this agent goroutine for graceful shutdown
		s.agentWg.Add(1)

		go func() {
			defer s.agentWg.Done()
			s.Logger.Info("Starting agent %s state machine", agentID)
			if err := runnable.Run(agentCtx); err != nil {
				s.Logger.Error("Agent %s state machine failed: %v", agentID, err)
			}
			s.Logger.Info("Agent %s state machine exited", agentID)
			s.handleUnexpectedExit(ctx, agentID, gen)
		}()
	} else {
		s.Logger.Debug("Agent %s does not implement Run method", agentID)
	}
}

// GetAgents returns a copy of the current agent tracking maps.
func (s *Supervisor) GetAgents() (map[string]dispatch.Agent, map[string]string) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()

	agents := make(map[string]dispatch.Agent)
	agentTypes := make(map[string]string)

	for k, v := range s.Agents {
		agents[k] = v
	}

	for k, v := range s.AgentTypes {
		agentTypes[k] = v
	}

	return agents, agentTypes
}

// GetFactory returns the agent factory for external use.
func (s *Supervisor) GetFactory() *factory.AgentFactory {
	return s.Factory
}

// SetShutdownHandler allows injecting a custom shutdown handler.
// This is useful for testing (mock handler) or implementing graceful shutdown.
func (s *Supervisor) SetShutdownHandler(handler ShutdownHandler) {
	s.ShutdownHandler = handler
	s.Logger.Info("Custom shutdown handler installed")
}

// WaitForAgentsShutdown waits for all agent goroutines to exit after context cancellation.
// This should be called after cancelling the context to ensure agents have finished
// their current work and serialized their state before proceeding with shutdown.
// Returns nil on success, or an error if the timeout is exceeded.
func (s *Supervisor) WaitForAgentsShutdown(timeout time.Duration) error {
	s.Logger.Info("⏳ Waiting for agents to complete shutdown (timeout: %v)...", timeout)

	// Create a channel to signal completion
	done := make(chan struct{})
	go func() {
		s.agentWg.Wait()
		close(done)
	}()

	// Wait with timeout
	select {
	case <-done:
		s.Logger.Info("✅ All agents have completed shutdown")
		return nil
	case <-time.After(timeout):
		s.Logger.Warn("⚠️ Timeout waiting for agents to shutdown")
		return fmt.Errorf("timeout waiting for agents to shutdown after %v", timeout)
	}
}

// =============================================================================
// SUSPEND State Support
// =============================================================================

const (
	// apiPollInterval is how often to check API health when agents are suspended.
	apiPollInterval = 30 * time.Second
)

// GetRestoreChannel returns the restore channel for agents to listen on.
// Creates the channel lazily on first call.
func (s *Supervisor) GetRestoreChannel() <-chan struct{} {
	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()

	if s.restoreCh == nil {
		// Create a buffered channel to avoid blocking broadcast
		s.restoreCh = make(chan struct{}, 100)
	}
	return s.restoreCh
}

// handleAgentSuspend is called when an agent enters SUSPEND state.
// Tracks suspended agents, persists a transient failure record, and starts API health polling.
func (s *Supervisor) handleAgentSuspend(ctx context.Context, notification *proto.StateChangeNotification) {
	agentID := notification.AgentID

	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()

	s.Logger.Info("⏸️  Agent %s entered SUSPEND state", agentID)
	s.suspendedAgents[agentID] = true

	// Persist a transient failure record for analytics tracking
	if s.Kernel.PersistenceChannel != nil && notification.Metadata != nil {
		if fi, ok := notification.Metadata[proto.KeyFailureInfo].(proto.FailureInfo); ok {
			const sqliteTimestampFormat = "2006-01-02T15:04:05.000000Z"
			now := time.Now().UTC()
			failureID := proto.GenerateFailureID()

			// Get story ID from the agent's lease
			storyID := s.Kernel.Dispatcher.GetLease(agentID)

			// Sanitize and compute signature before persistence.
			fi.Sanitize(utils.SanitizeString)

			record := &persistence.FailureRecord{
				ID:               failureID,
				CreatedAt:        now.Format(sqliteTimestampFormat),
				UpdatedAt:        now.Format(sqliteTimestampFormat),
				StoryID:          storyID,
				Source:           string(fi.Source),
				ReporterAgentID:  agentID,
				FailedState:      fi.FailedState,
				Kind:             string(fi.Kind),
				ScopeGuess:       string(fi.ScopeGuess),
				Explanation:      fi.Explanation,
				Signature:        fi.Signature,
				Action:           string(proto.FailureActionRetryAttempt),
				ResolutionStatus: string(proto.FailureResolutionRunning),
			}

			persistence.PersistFailureRecord(record, s.Kernel.PersistenceChannel)
			s.suspendFailureID[agentID] = failureID
			s.Logger.Info("📝 Persisted transient failure record %s for agent %s", failureID, agentID)
		}
	}

	// Start API polling if this is the first suspended agent
	if len(s.suspendedAgents) == 1 && s.pollCancel == nil {
		s.Logger.Info("🔍 Starting API health polling (first agent suspended)")
		pollCtx, cancel := context.WithCancel(ctx)
		s.pollCancel = cancel
		go s.pollAPIHealth(pollCtx)
	}
}

// handleAgentResume is called when an agent leaves SUSPEND state.
// Removes from tracking, updates the failure record, and stops polling if no more suspended agents.
func (s *Supervisor) handleAgentResume(agentID string, toState proto.State) {
	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()

	s.Logger.Info("▶️  Agent %s left SUSPEND state → %s", agentID, toState)
	delete(s.suspendedAgents, agentID)

	// Update the transient failure record based on outcome
	if failureID, ok := s.suspendFailureID[agentID]; ok && s.Kernel.PersistenceChannel != nil {
		status := proto.FailureResolutionSucceeded
		if toState == proto.StateError {
			status = proto.FailureResolutionFailed
		}
		persistence.UpdateFailureResolutionAsync(&persistence.UpdateFailureResolutionRequest{
			ID:               failureID,
			ResolutionStatus: string(status),
		}, s.Kernel.PersistenceChannel)
		delete(s.suspendFailureID, agentID)
		s.Logger.Info("📝 Updated transient failure %s → %s for agent %s", failureID, status, agentID)
	}

	// Stop polling if no more suspended agents
	if len(s.suspendedAgents) == 0 && s.pollCancel != nil {
		s.Logger.Info("🛑 Stopping API health polling (no suspended agents)")
		s.pollCancel()
		s.pollCancel = nil
	}
}

// pollAPIHealth periodically checks API health and broadcasts restore when all healthy.
func (s *Supervisor) pollAPIHealth(ctx context.Context) {
	ticker := time.NewTicker(apiPollInterval)
	defer ticker.Stop()

	s.Logger.Info("🔍 API health polling started")

	for {
		select {
		case <-ctx.Done():
			s.Logger.Info("🛑 API health polling stopped")
			return

		case <-ticker.C:
			if s.checkAllAPIsHealthy(ctx) {
				s.broadcastRestore()
			}
		}
	}
}

// checkAllAPIsHealthy verifies all configured APIs are responding.
// Returns true only if ALL APIs pass health checks.
func (s *Supervisor) checkAllAPIsHealthy(ctx context.Context) bool {
	// Respect context cancellation
	select {
	case <-ctx.Done():
		s.Logger.Debug("API health check cancelled")
		return false
	default:
	}

	s.Logger.Debug("Checking API health...")

	// Check GitHub API - uses gh auth status which verifies both CLI and API connectivity
	ghCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := github.CheckAuth(ghCtx); err != nil {
		s.Logger.Debug("GitHub API health check failed: %v", err)
		return false
	}
	s.Logger.Debug("GitHub API health check passed")

	// TODO: Implement actual health checks for LLM providers:
	// - Anthropic API (if configured)
	// - OpenAI API (if configured)
	// - Google API (if configured)
	// For now, we only check GitHub since it's critical for merge operations.
	// LLM providers will naturally fail fast on first request if unavailable.

	s.Logger.Debug("API health check: all APIs healthy")
	return true
}

// broadcastRestore sends restore signals to all suspended agents.
func (s *Supervisor) broadcastRestore() {
	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()

	if len(s.suspendedAgents) == 0 {
		return
	}

	s.Logger.Info("📢 Broadcasting restore signal to %d suspended agents", len(s.suspendedAgents))

	// Ensure channel exists
	if s.restoreCh == nil {
		s.restoreCh = make(chan struct{}, 100)
	}

	// Send restore signal for each suspended agent
	// Use non-blocking send to avoid deadlock if channel is full
	for agentID := range s.suspendedAgents {
		select {
		case s.restoreCh <- struct{}{}:
			s.Logger.Debug("Sent restore signal for agent %s", agentID)
		default:
			s.Logger.Warn("Restore channel full, agent %s may not receive signal", agentID)
		}
	}
}

// GetSuspendedAgentCount returns the number of currently suspended agents.
func (s *Supervisor) GetSuspendedAgentCount() int {
	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()
	return len(s.suspendedAgents)
}

// RecordActivity records a toolloop heartbeat for the given agent.
// Called by the toolloop at the start of each iteration via the ActivityTracker interface.
func (s *Supervisor) RecordActivity(agentID string) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	s.lastActivity[agentID] = time.Now()
}

// startCodingWatchdog monitors coder agents for between-turns activity gaps.
// If a coder agent has no toolloop activity for longer than the configured timeout,
// the watchdog cancels the agent context to force a clean exit.
// Claude Code mode coders are excluded (they have their own timeout management).
func (s *Supervisor) startCodingWatchdog(ctx context.Context) {
	const watchdogPollInterval = 30 * time.Second

	ticker := time.NewTicker(watchdogPollInterval)
	defer ticker.Stop()

	s.Logger.Info("🐕 Coding watchdog started (timeout: %d minutes)", config.GetCodingWatchdogMinutes())

	for {
		select {
		case <-ctx.Done():
			s.Logger.Info("🐕 Coding watchdog stopped")
			return
		case <-ticker.C:
			s.checkCodingActivity()
		}
	}
}

// checkCodingActivity checks all coder agents for activity timeouts.
func (s *Supervisor) checkCodingActivity() {
	timeout := time.Duration(config.GetCodingWatchdogMinutes()) * time.Minute

	// Skip if Claude Code mode (they manage their own timeouts)
	cfg, err := config.GetConfig()
	if err == nil && cfg.Agents.CoderMode == config.CoderModeClaudeCode {
		return
	}

	type staleAgent struct {
		lastTime time.Time
		cancel   context.CancelFunc
	}

	s.activityMu.Lock()
	// Snapshot stale agents under lock — AgentTypes, AgentContexts, and agentStates
	// are all protected by activityMu to prevent concurrent map read/write panics.
	staleAgents := make(map[string]*staleAgent)
	for agentID, lastTime := range s.lastActivity {
		if time.Since(lastTime) > timeout {
			if agentType, exists := s.AgentTypes[agentID]; exists && agentType == "coder" {
				// Only kill agents in states where toolloop activity is expected.
				// States like PLAN_REVIEW, CODE_REVIEW, QUESTION, and BUDGET_REVIEW
				// block on ExecuteEffect() waiting for architect responses — no
				// toolloop iterations means no heartbeat, but the agent is healthy.
				state := s.agentStates[agentID]
				if state != proto.State("PLANNING") && state != proto.State("CODING") && state != proto.State("TESTING") {
					continue
				}
				staleAgents[agentID] = &staleAgent{
					lastTime: lastTime,
					cancel:   s.AgentContexts[agentID],
				}
			}
		}
	}
	s.activityMu.Unlock()

	// Cancel stale agents outside the lock
	for agentID, info := range staleAgents {
		s.Logger.Error("🐕 Coding watchdog timeout: agent %s has had no toolloop activity for %v (last: %s). Cancelling agent.",
			agentID, time.Since(info.lastTime).Round(time.Second), info.lastTime.Format(time.RFC3339))

		if info.cancel != nil {
			info.cancel()
		}

		// Remove from activity tracking to avoid re-firing
		s.activityMu.Lock()
		delete(s.lastActivity, agentID)
		s.activityMu.Unlock()
	}
}
