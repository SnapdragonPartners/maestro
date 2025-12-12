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
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
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
	restoreCh       chan struct{} // Broadcast channel for service recovery
	suspendedAgents map[string]bool
	suspendMu       sync.Mutex
	pollCancel      context.CancelFunc // Cancel function for API polling goroutine
}

// NewSupervisor creates a new supervisor with the given kernel.
func NewSupervisor(k *kernel.Kernel) *Supervisor {
	logger := logx.NewLogger("supervisor")

	// Use the kernel's chat service (already initialized with scanner and config)
	// Don't create a duplicate chat service
	chatService := k.ChatService

	// Create the agent factory with kernel dependencies (lightweight)
	// Pass the shared LLM factory to ensure proper rate limiting across all agents
	agentFactory := factory.NewAgentFactory(k.Dispatcher, k.PersistenceChannel, chatService, k.LLMFactory)

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
		suspendedAgents: make(map[string]bool),
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

	s.running = true

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
			}
		}
	}()
}

// handleStateChange processes individual state change notifications.
// This consolidates and extends the handleStateChange logic from the old orchestrator.
func (s *Supervisor) handleStateChange(ctx context.Context, notification *proto.StateChangeNotification) {
	s.Logger.Info("Agent %s state changed: %s -> %s",
		notification.AgentID, notification.FromState, notification.ToState)

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
				// Use the new clean channel-based requeue pattern
				if err := s.Kernel.Dispatcher.UpdateStoryRequeue(storyID, notification.AgentID, "Agent failed with error state"); err != nil {
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
		s.handleAgentSuspend(ctx, notification.AgentID)
	}

	// Handle agent leaving SUSPEND state (recovery)
	if notification.FromState == proto.StateSuspend && notification.ToState != proto.StateSuspend {
		s.handleAgentResume(notification.AgentID)
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
	if cancelFunc, exists := s.AgentContexts[agentID]; exists {
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

	// Remove from tracking maps
	delete(s.Agents, agentID)
	delete(s.AgentTypes, agentID)
	delete(s.AgentContexts, agentID)

	// Work directory cleanup is handled by agent SETUP state for fresh workspace
}

// getAgentType returns the type of an agent by ID.
// This preserves the getAgentType logic from the old orchestrator.
func (s *Supervisor) getAgentType(agentID string) string {
	if agentType, exists := s.AgentTypes[agentID]; exists {
		return agentType
	}
	return ""
}

// RegisterAgent adds an agent to the supervisor's tracking and starts its state machine.
// Creates individual context for the agent to enable graceful shutdown.
func (s *Supervisor) RegisterAgent(ctx context.Context, agentID, agentType string, agent dispatch.Agent) {
	s.AgentTypes[agentID] = agentType
	s.Agents[agentID] = agent
	s.Logger.Info("Registered agent %s (type: %s)", agentID, agentType)

	// Start the agent's state machine with individual context
	if runnable, ok := agent.(interface{ Run(context.Context) error }); ok {
		// Create individual context for this agent (child of main context)
		agentCtx, cancel := context.WithCancel(ctx)
		s.AgentContexts[agentID] = cancel

		// Track this agent goroutine for graceful shutdown
		s.agentWg.Add(1)

		go func() {
			defer s.agentWg.Done()
			s.Logger.Info("Starting agent %s state machine", agentID)
			if err := runnable.Run(agentCtx); err != nil {
				s.Logger.Error("Agent %s state machine failed: %v", agentID, err)
			}
			s.Logger.Info("Agent %s state machine exited", agentID)
		}()
	} else {
		s.Logger.Debug("Agent %s does not implement Run method", agentID)
	}
}

// GetAgents returns a copy of the current agent tracking maps.
func (s *Supervisor) GetAgents() (map[string]dispatch.Agent, map[string]string) {
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
// Tracks suspended agents and starts API health polling if not already running.
func (s *Supervisor) handleAgentSuspend(ctx context.Context, agentID string) {
	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()

	s.Logger.Info("⏸️  Agent %s entered SUSPEND state", agentID)
	s.suspendedAgents[agentID] = true

	// Start API polling if this is the first suspended agent
	if len(s.suspendedAgents) == 1 && s.pollCancel == nil {
		s.Logger.Info("🔍 Starting API health polling (first agent suspended)")
		pollCtx, cancel := context.WithCancel(ctx)
		s.pollCancel = cancel
		go s.pollAPIHealth(pollCtx)
	}
}

// handleAgentResume is called when an agent leaves SUSPEND state.
// Removes from tracking and stops polling if no more suspended agents.
func (s *Supervisor) handleAgentResume(agentID string) {
	s.suspendMu.Lock()
	defer s.suspendMu.Unlock()

	s.Logger.Info("▶️  Agent %s left SUSPEND state", agentID)
	delete(s.suspendedAgents, agentID)

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

	// Check LLM providers configured in the kernel
	// For now, do a simple check - in production this would ping each configured provider
	// The kernel's LLMFactory has the client configurations

	// TODO: Implement actual health checks for:
	// - Anthropic API (if configured)
	// - OpenAI API (if configured)
	// - Google API (if configured)
	// - GitHub API

	// For now, return true to allow testing the flow
	// In production, this would make lightweight API calls to each configured provider
	s.Logger.Debug("API health check: all APIs healthy (placeholder)")
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
