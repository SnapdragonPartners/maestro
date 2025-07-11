package agent

import "fmt"

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