// Package supervisor manages agent lifecycle and restart policies.
// It consolidates the state change processing logic that was previously embedded
// in the main orchestrator and missing from bootstrap.
package supervisor

import (
	"context"
	"fmt"
	"os"

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
			string(agent.TypeArchitect): RestartAgent, // Architects restart for next spec (NEW)
		},
		OnError: map[string]RestartAction{
			string(agent.TypeCoder):     RestartAgent,  // Coders restart after errors
			string(agent.TypeArchitect): FatalShutdown, // Architect errors are fatal (NEW)
		},
	}
}

// Supervisor manages agent lifecycle, restart policies, and state change processing.
// It consolidates the logic that was previously scattered across the orchestrator.
type Supervisor struct {
	Kernel  *kernel.Kernel
	Factory *factory.AgentFactory
	Logger  *logx.Logger
	Policy  RestartPolicy

	// Agent tracking (preserves existing patterns)
	Agents     map[string]dispatch.Agent
	AgentTypes map[string]string

	// Runtime state
	running bool
}

// NewSupervisor creates a new supervisor with the given kernel.
func NewSupervisor(k *kernel.Kernel) *Supervisor {
	// Create the agent factory with kernel dependencies
	agentFactory := factory.NewAgentFactory(k.Dispatcher, k.PersistenceChannel, k.BuildService)

	return &Supervisor{
		Kernel:     k,
		Factory:    agentFactory,
		Logger:     logx.NewLogger("supervisor"),
		Policy:     DefaultRestartPolicy(),
		Agents:     make(map[string]dispatch.Agent),
		AgentTypes: make(map[string]string),
		running:    false,
	}
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
		// This preserves the exact flow from the old orchestrator
		if agentType == string(agent.TypeCoder) {
			if err := s.Kernel.Dispatcher.SendRequeue(notification.AgentID, "agent error"); err != nil {
				s.Logger.Error("Failed to requeue story for agent %s: %v", notification.AgentID, err)
			}
		}

		s.handleStateAction(ctx, notification, action, "ERROR")
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

		// For architect errors, we should trigger system shutdown
		// This will be handled by the flow that's monitoring the supervisor
		s.Logger.Error("Architect error requires system shutdown")
		os.Exit(1)

	default:
		s.Logger.Info("No action configured for agent %s (%s) in %s state",
			notification.AgentID, agentType, stateType)
	}
}

// restartAgent handles restarting an individual agent.
// This preserves the restartAgent logic from the old orchestrator.
func (s *Supervisor) restartAgent(ctx context.Context, agentID string) error {
	s.Logger.Info("Restarting agent: %s", agentID)

	// Get the agent type
	agentType := s.getAgentType(agentID)
	if agentType == "" {
		return fmt.Errorf("unknown agent type for %s", agentID)
	}

	// Clean up existing agent resources
	s.cleanupAgentResources(agentID)

	// Recreate the agent using the factory
	newAgent, err := s.Factory.RecreateAgent(ctx, agentID, agentType)
	if err != nil {
		return fmt.Errorf("failed to recreate agent %s: %w", agentID, err)
	}

	// Register the newly created agent
	s.RegisterAgent(ctx, agentID, agentType, newAgent)

	s.Logger.Info("Agent %s successfully recreated and registered", agentID)
	return nil
}

// cleanupAgentResources performs cleanup for a terminated agent.
// This preserves the cleanupAgentResources logic from the old orchestrator.
func (s *Supervisor) cleanupAgentResources(agentID string) {
	s.Logger.Info("Cleaning up resources for agent: %s", agentID)

	// Remove from tracking maps
	delete(s.Agents, agentID)
	delete(s.AgentTypes, agentID)

	// TODO: Add any additional cleanup needed (containers, file handles, etc.)
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
func (s *Supervisor) RegisterAgent(ctx context.Context, agentID, agentType string, agent dispatch.Agent) {
	s.AgentTypes[agentID] = agentType
	s.Agents[agentID] = agent
	s.Logger.Info("Registered agent %s (type: %s)", agentID, agentType)

	// Start the agent's state machine
	if runnable, ok := agent.(interface{ Run(context.Context) error }); ok {
		go func() {
			s.Logger.Info("Starting agent %s state machine", agentID)
			if err := runnable.Run(ctx); err != nil {
				s.Logger.Error("Agent %s state machine failed: %v", agentID, err)
			}
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
