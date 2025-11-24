package pm

import (
	"context"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/tools"
)

// SpecSubmitTool is a terminal tool that wraps spec_submit.
// Signals: BOOTSTRAP_COMPLETE, SPEC_PREVIEW, or AWAIT_USER depending on result.
type SpecSubmitTool struct {
	underlying tools.Tool
}

// NewSpecSubmitTool creates a terminal tool wrapper for spec_submit.
func NewSpecSubmitTool(underlying tools.Tool) *SpecSubmitTool {
	return &SpecSubmitTool{underlying: underlying}
}

// Name returns the tool name.
func (t *SpecSubmitTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *SpecSubmitTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
//
//nolint:wrapcheck // Direct forwarding to underlying tool
func (t *SpecSubmitTool) Exec(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *SpecSubmitTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the WorkingResult from tool execution.
func (t *SpecSubmitTool) ExtractResult(calls []agent.ToolCall, results []any) (WorkingResult, error) {
	return ExtractPMWorkingResult(calls, results)
}

// Verify SpecSubmitTool implements TerminalTool[WorkingResult].
var _ toolloop.TerminalTool[WorkingResult] = (*SpecSubmitTool)(nil)
