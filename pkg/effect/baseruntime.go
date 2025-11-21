// Package effect provides BaseRuntime implementation that provides common
// runtime capabilities for effect execution across all agent types.
package effect

import (
	"context"
	"fmt"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// MessageDispatcher is an interface for dispatching messages to break import cycles.
type MessageDispatcher interface {
	DispatchMessage(msg *proto.AgentMsg) error
}

// BaseRuntime provides the standard implementation of Runtime interface.
// It can be embedded or used directly by agents to provide effect execution capabilities.
//
//nolint:govet // fieldalignment: keeping fields in logical order (dispatcher/logger/channels then IDs/roles)
type BaseRuntime struct {
	dispatcher MessageDispatcher
	logger     *logx.Logger
	replyCh    <-chan *proto.AgentMsg // Reply channel for receiving RESPONSE messages
	agentID    string
	agentRole  string
}

// NewBaseRuntime creates a new BaseRuntime with the specified dependencies.
// The replyCh parameter is the reply channel from the dispatcher for receiving RESPONSE messages.
func NewBaseRuntime(dispatcher MessageDispatcher, logger *logx.Logger, agentID, agentRole string, replyCh <-chan *proto.AgentMsg) *BaseRuntime {
	return &BaseRuntime{
		dispatcher: dispatcher,
		logger:     logger,
		agentID:    agentID,
		agentRole:  agentRole,
		replyCh:    replyCh,
	}
}

// SendMessage implements the Messaging interface.
func (r *BaseRuntime) SendMessage(msg *proto.AgentMsg) error {
	r.logger.Debug("🔄 BaseRuntime sending message %s from %s to %s", msg.ID, msg.FromAgent, msg.ToAgent)

	if err := r.dispatcher.DispatchMessage(msg); err != nil {
		r.logger.Error("❌ Failed to dispatch message %s: %v", msg.ID, err)
		return fmt.Errorf("failed to dispatch message %s: %w", msg.ID, err)
	}

	r.logger.Debug("✅ Message %s dispatched successfully", msg.ID)
	return nil
}

// ReceiveMessage implements the Messaging interface.
// Blocks waiting for a message of the expected type from the reply channel.
func (r *BaseRuntime) ReceiveMessage(ctx context.Context, expectedType proto.MsgType) (*proto.AgentMsg, error) {
	if r.replyCh == nil {
		return nil, fmt.Errorf("reply channel not configured for agent %s", r.agentID)
	}

	r.logger.Debug("🔄 BaseRuntime waiting for %s message for agent %s", expectedType, r.agentID)

	select {
	case msg, ok := <-r.replyCh:
		if !ok {
			return nil, fmt.Errorf("reply channel closed unexpectedly for agent %s", r.agentID)
		}
		if msg == nil {
			return nil, fmt.Errorf("received nil message for agent %s", r.agentID)
		}
		if msg.Type != expectedType {
			return nil, fmt.Errorf("expected message type %s but received %s for agent %s", expectedType, msg.Type, r.agentID)
		}
		r.logger.Debug("✅ Received expected %s message %s", expectedType, msg.ID)
		return msg, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for %s message: %w", expectedType, ctx.Err())
	}
}

// Info implements the Logging interface.
func (r *BaseRuntime) Info(msg string, args ...any) {
	r.logger.Info(msg, args...)
}

// Error implements the Logging interface.
func (r *BaseRuntime) Error(msg string, args ...any) {
	r.logger.Error(msg, args...)
}

// Debug implements the Logging interface.
func (r *BaseRuntime) Debug(msg string, args ...any) {
	r.logger.Debug(msg, args...)
}

// GetAgentID implements the AgentInfo interface.
func (r *BaseRuntime) GetAgentID() string {
	return r.agentID
}

// GetAgentRole implements the AgentInfo interface.
func (r *BaseRuntime) GetAgentRole() string {
	return r.agentRole
}

// Verify that BaseRuntime implements Runtime interface.
var _ Runtime = (*BaseRuntime)(nil)
