package agent

import (
	"context"
	"log"
)

// Driver represents a generic agent driver interface
type Driver interface {
	// Initialize sets up the driver and loads any existing state
	Initialize(ctx context.Context) error

	// Run executes the driver's main loop until completion or error
	Run(ctx context.Context) error

	// Step executes a single step of the driver's state machine
	// Returns whether processing is complete
	Step(ctx context.Context) (bool, error)

	// GetCurrentState returns the current state of the driver
	GetCurrentState() any

	// GetStateData returns a copy of the current state data
	GetStateData() map[string]any

	// GetAgentType returns the type of the agent (architect, coder, etc.)
	GetAgentType() AgentType

	// Shutdown performs cleanup when the driver is stopping
	Shutdown(ctx context.Context) error
}

// AgentContext contains shared context for all agents
type AgentContext struct {
	Context   context.Context
	Logger    *log.Logger
	WorkDir   string
	LLMClient LLMClient
	Store     StateStore
}

// AgentConfig represents configuration for an agent
type AgentConfig struct {
	ID        string
	Type      string
	Context   AgentContext
	LLMConfig *LLMConfig // Optional LLM configuration
}

// NewAgentConfig creates a new agent configuration
func NewAgentConfig(id, agentType string, ctx AgentContext) *AgentConfig {
	return &AgentConfig{
		ID:      id,
		Type:    agentType,
		Context: ctx,
	}
}

// WithLLM sets the LLM configuration for the agent
func (ac *AgentConfig) WithLLM(config *LLMConfig) *AgentConfig {
	ac.LLMConfig = config
	return ac
}
