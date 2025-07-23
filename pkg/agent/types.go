package agent

import (
	"fmt"

	"orchestrator/pkg/proto"
)

// Type represents the type of an agent.
type Type string

const (
	// TypeArchitect represents an architect agent that processes specifications and manages workflow.
	TypeArchitect Type = "architect"

	// TypeCoder represents a coder agent that implements code based on tasks.
	TypeCoder Type = "coder"
)

// IsValid checks if the agent type is valid.
func (t Type) IsValid() bool {
	return t == TypeArchitect || t == TypeCoder
}

// String returns the string representation of the agent type.
func (t Type) String() string {
	return string(t)
}

// Parse parses a string into a Type.
func Parse(s string) (Type, error) {
	t := Type(s)
	if !t.IsValid() {
		return "", fmt.Errorf("invalid agent type: %s (must be 'architect' or 'coder')", s)
	}
	return t, nil
}

// ParseState safely parses a string into a State with validation.
func ParseState(s string, driver Driver) (proto.State, error) {
	state := proto.State(s)
	if err := driver.ValidateState(state); err != nil {
		return "", fmt.Errorf("invalid state '%s' for agent type '%s': %w", s, driver.GetAgentType(), err)
	}
	return state, nil
}

// IsValidStateTransition checks if a transition from one state to another is valid for the given driver.
func IsValidStateTransition(driver Driver, from, to proto.State) error {
	// First validate both states are valid for this agent type.
	if err := driver.ValidateState(from); err != nil {
		return fmt.Errorf("invalid from state: %w", err)
	}
	if err := driver.ValidateState(to); err != nil {
		return fmt.Errorf("invalid to state: %w", err)
	}

	// Additional transition logic would go here if needed.
	// For now, if both states are valid, the transition is allowed.
	return nil
}
