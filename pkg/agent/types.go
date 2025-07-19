package agent

import (
	"fmt"

	"orchestrator/pkg/proto"
)

// AgentType represents the type of an agent
type AgentType string

const (
	// AgentTypeArchitect represents an architect agent that processes specifications and manages workflow
	AgentTypeArchitect AgentType = "architect"

	// AgentTypeCoder represents a coder agent that implements code based on tasks
	AgentTypeCoder AgentType = "coder"
)

// IsValid checks if the agent type is valid
func (t AgentType) IsValid() bool {
	return t == AgentTypeArchitect || t == AgentTypeCoder
}

// String returns the string representation of the agent type
func (t AgentType) String() string {
	return string(t)
}

// ParseAgentType parses a string into an AgentType
func ParseAgentType(s string) (AgentType, error) {
	t := AgentType(s)
	if !t.IsValid() {
		return "", fmt.Errorf("invalid agent type: %s (must be 'architect' or 'coder')", s)
	}
	return t, nil
}

// ParseState safely parses a string into a State with validation
func ParseState(s string, driver Driver) (proto.State, error) {
	state := proto.State(s)
	if err := driver.ValidateState(state); err != nil {
		return "", fmt.Errorf("invalid state '%s' for agent type '%s': %w", s, driver.GetAgentType(), err)
	}
	return state, nil
}

// IsValidStateTransition checks if a transition from one state to another is valid for the given driver
func IsValidStateTransition(driver Driver, from, to proto.State) error {
	// First validate both states are valid for this agent type
	if err := driver.ValidateState(from); err != nil {
		return fmt.Errorf("invalid from state: %w", err)
	}
	if err := driver.ValidateState(to); err != nil {
		return fmt.Errorf("invalid to state: %w", err)
	}

	// Additional transition logic would go here if needed
	// For now, if both states are valid, the transition is allowed
	return nil
}
