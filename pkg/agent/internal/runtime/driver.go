// Package runtime provides driver implementations and runtime management for agent execution.
package runtime

import (
	"context"
	"log"

	"orchestrator/pkg/agent/internal/core"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/proto"
)

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
	GetStateData() map[string]any

	// GetAgentType returns the type of the agent (architect, coder, etc.)
	GetAgentType() AgentType

	// ValidateState checks if a state is valid for this agent type.
	ValidateState(state proto.State) error

	// GetValidStates returns all valid states for this agent type.
	GetValidStates() []proto.State

	// Shutdown performs cleanup when the driver is stopping.
	Shutdown(ctx context.Context) error
}

// Context contains shared context for all agents.
type Context struct {
	Context   context.Context //nolint:containedctx // Shared context container by design
	Logger    *log.Logger
	LLMClient llm.LLMClient
	Store     core.StateStore
	WorkDir   string
}

// Config represents configuration for an agent.
type Config struct {
	Context   Context
	LLMConfig *llm.LLMConfig // Optional LLM configuration
	ID        string
	Type      string
}

// NewConfig creates a new agent configuration.
func NewConfig(id, agentType string, ctx Context) *Config {
	return &Config{
		ID:      id,
		Type:    agentType,
		Context: ctx,
	}
}

// WithLLM sets the LLM configuration for the agent.
func (ac *Config) WithLLM(config *llm.LLMConfig) *Config {
	ac.LLMConfig = config
	return ac
}
