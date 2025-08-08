// Package effect provides the core abstractions for executable effects in the orchestrator.
// This package defines the single source of truth for Effect and Runtime interfaces.
package effect

import (
	"context"

	"orchestrator/pkg/proto"
)

// Effect represents an executable unit that can perform actions using a Runtime.
// Examples: sending messages, requesting approvals, asking questions.
type Effect interface {
	// Execute performs the effect using the provided runtime capabilities.
	Execute(ctx context.Context, runtime Runtime) (any, error)

	// Type returns a string identifier for this effect type (useful for logging/debugging).
	Type() string
}

// Runtime provides the capability surface that effects can use to interact
// with the agent system. It's composed of smaller capability interfaces.
type Runtime interface {
	Messaging
	Logging
	AgentInfo
}

// Messaging provides inter-agent communication capabilities.
type Messaging interface {
	// SendMessage dispatches a message to another agent.
	SendMessage(msg *proto.AgentMsg) error

	// ReceiveMessage blocks waiting for a specific message type.
	ReceiveMessage(ctx context.Context, expectedType proto.MsgType) (*proto.AgentMsg, error)
}

// Logging provides structured logging capabilities.
type Logging interface {
	// Info logs an informational message.
	Info(msg string, args ...any)

	// Error logs an error message.
	Error(msg string, args ...any)

	// Debug logs a debug message.
	Debug(msg string, args ...any)
}

// AgentInfo provides information about the current agent.
type AgentInfo interface {
	// GetAgentID returns the current agent's unique identifier.
	GetAgentID() string

	// GetAgentRole returns the agent's role ("coder", "architect", etc.).
	GetAgentRole() string
}
