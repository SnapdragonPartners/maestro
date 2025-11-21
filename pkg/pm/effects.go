package pm

import (
	"context"
	"fmt"

	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// Runtime extends BaseRuntime with PM-specific capabilities.
type Runtime struct {
	*effect.BaseRuntime
}

// NewRuntime creates a new runtime for PM effects.
func NewRuntime(dispatcher *dispatch.Dispatcher, logger *logx.Logger, agentID string, replyCh <-chan *proto.AgentMsg) *Runtime {
	baseRuntime := effect.NewBaseRuntime(dispatcher, logger, agentID, "pm", replyCh)
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

// ExecuteEffect executes a single Effect using the PM's runtime.
func (d *Driver) ExecuteEffect(ctx context.Context, eff effect.Effect) error {
	runtime := NewRuntime(d.dispatcher, d.logger, d.GetAgentID(), d.replyCh)
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
