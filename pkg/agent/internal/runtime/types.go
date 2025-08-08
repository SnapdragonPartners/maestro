package runtime

// AgentType represents the type of an agent.
type AgentType string

const (
	// AgentTypeArchitect represents an architect agent.
	AgentTypeArchitect AgentType = "architect"

	// AgentTypeCoder represents a coder agent.
	AgentTypeCoder AgentType = "coder"
)

// String returns the string representation.
func (t AgentType) String() string {
	return string(t)
}

// ParseAgentType converts string to AgentType.
func ParseAgentType(s string) AgentType {
	switch s {
	case "architect":
		return AgentTypeArchitect
	case "coder":
		return AgentTypeCoder
	default:
		return AgentTypeCoder // Default fallback
	}
}
