package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// Runtime extends BaseRuntime with architect-specific capabilities.
type Runtime struct {
	*effect.BaseRuntime
}

// NewRuntime creates a new runtime for architect effects.
func NewRuntime(dispatcher *dispatch.Dispatcher, logger *logx.Logger, agentID string, replyCh <-chan *proto.AgentMsg) *Runtime {
	baseRuntime := effect.NewBaseRuntime(dispatcher, logger, agentID, "architect", replyCh)
	return &Runtime{
		BaseRuntime: baseRuntime,
	}
}

// SendMessageEffect represents an effect to send a message via dispatcher.
type SendMessageEffect struct {
	Message *proto.AgentMsg
}

// Type returns the effect type identifier.
func (e *SendMessageEffect) Type() string {
	return "send_message"
}

// Execute sends the message via the dispatcher.
func (e *SendMessageEffect) Execute(_ context.Context, runtime effect.Runtime) (any, error) {
	runtime.Debug("🔄 Executing SendMessage effect for message %s", e.Message.ID)

	if err := runtime.SendMessage(e.Message); err != nil {
		runtime.Error("❌ Failed to dispatch message %s: %v", e.Message.ID, err)
		return nil, fmt.Errorf("failed to dispatch message %s: %w", e.Message.ID, err)
	}

	runtime.Debug("✅ Message %s dispatched successfully", e.Message.ID)
	return struct{}{}, nil
}

// SendResponseEffect represents an effect to send a response message.
type SendResponseEffect struct {
	Response *proto.AgentMsg
}

// Type returns the effect type identifier.
func (e *SendResponseEffect) Type() string {
	return "send_response"
}

// Execute sends the response message via the dispatcher.
func (e *SendResponseEffect) Execute(_ context.Context, runtime effect.Runtime) (any, error) {
	runtime.Debug("🔄 Executing SendResponse effect for response %s", e.Response.ID)

	if err := runtime.SendMessage(e.Response); err != nil {
		runtime.Error("❌ Failed to dispatch response %s: %v", e.Response.ID, err)
		return nil, fmt.Errorf("failed to dispatch response %s: %w", e.Response.ID, err)
	}

	runtime.Debug("✅ Response %s dispatched successfully", e.Response.ID)
	return struct{}{}, nil
}

// DispatchStoryEffect represents an effect to dispatch a story to coders.
type DispatchStoryEffect struct {
	Story *proto.AgentMsg
}

// Type returns the effect type identifier.
func (e *DispatchStoryEffect) Type() string {
	return "dispatch_story"
}

// Execute dispatches the story message via the dispatcher.
func (e *DispatchStoryEffect) Execute(_ context.Context, runtime effect.Runtime) (any, error) {
	runtime.Info("🔄 Executing DispatchStory effect for story %s", e.Story.ID)

	if err := runtime.SendMessage(e.Story); err != nil {
		runtime.Error("❌ Failed to dispatch story %s: %v", e.Story.ID, err)
		return nil, fmt.Errorf("failed to dispatch story %s: %w", e.Story.ID, err)
	}

	runtime.Info("✅ Story %s dispatched successfully", e.Story.ID)
	return struct{}{}, nil
}

// ExecuteEffect executes a single Effect using the architect's runtime.
func (d *Driver) ExecuteEffect(ctx context.Context, eff effect.Effect) error {
	runtime := NewRuntime(d.dispatcher, d.logger, d.architectID, d.replyCh)
	_, err := eff.Execute(ctx, runtime)
	if err != nil {
		return fmt.Errorf("effect execution failed: %w", err)
	}
	return nil
}

// ExecuteEffects executes multiple Effects in sequence.
func (d *Driver) ExecuteEffects(ctx context.Context, effects ...effect.Effect) error {
	for i, eff := range effects {
		d.logger.Debug("Executing effect %d/%d", i+1, len(effects))

		if err := d.ExecuteEffect(ctx, eff); err != nil {
			return fmt.Errorf("effect %d failed: %w", i+1, err)
		}
	}

	return nil
}
