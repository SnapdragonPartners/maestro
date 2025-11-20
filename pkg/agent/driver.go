package agent

import (
	"context"

	"orchestrator/pkg/agent/internal/core"
	"orchestrator/pkg/proto"
)

// StateData is a type alias for state data maps.
// This allows external packages (like tests) to use the correct type.
type StateData = core.StateData

// Driver represents a generic agent driver interface.
type Driver interface {
	// Initialize sets up the driver and loads any existing state.
	Initialize(ctx context.Context) error

	// Run executes the driver's main loop until completion or error.
	Run(ctx context.Context) error

	// Step executes a single step of the driver's state machine.
	// Returns whether processing is complete.
	Step(ctx context.Context) (bool, error)

	// GetCurrentState returns the current state of the driver.
	GetCurrentState() proto.State

	// GetStateData returns a copy of the current state data.
	GetStateData() core.StateData

	// GetAgentType returns the type of the agent.
	GetAgentType() Type

	// ValidateState checks if a state is valid for this agent type.
	ValidateState(state proto.State) error

	// GetValidStates returns all valid states for this agent type.
	GetValidStates() []proto.State

	// Shutdown performs cleanup when the driver is stopping.
	Shutdown(ctx context.Context) error
}
