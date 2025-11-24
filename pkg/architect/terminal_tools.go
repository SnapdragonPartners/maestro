package architect

import (
	"context"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/tools"
)

// SubmitReplyTool is a terminal tool that wraps submit_reply.
// Used for answering questions and providing iterative feedback.
type SubmitReplyTool struct {
	underlying tools.Tool
}

// NewSubmitReplyTool creates a terminal tool wrapper for submit_reply.
func NewSubmitReplyTool(underlying tools.Tool) *SubmitReplyTool {
	return &SubmitReplyTool{underlying: underlying}
}

// Name returns the tool name.
func (t *SubmitReplyTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *SubmitReplyTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
func (t *SubmitReplyTool) Exec(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *SubmitReplyTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the SubmitReplyResult from tool execution.
func (t *SubmitReplyTool) ExtractResult(calls []agent.ToolCall, results []any) (SubmitReplyResult, error) {
	return ExtractSubmitReply(calls, results)
}

// Verify SubmitReplyTool implements TerminalTool[SubmitReplyResult].
var _ toolloop.TerminalTool[SubmitReplyResult] = (*SubmitReplyTool)(nil)

// ReviewCompleteTool is a terminal tool that wraps review_complete.
// Used for single-turn plan and budget reviews with structured status.
type ReviewCompleteTool struct {
	underlying tools.Tool
}

// NewReviewCompleteTool creates a terminal tool wrapper for review_complete.
func NewReviewCompleteTool(underlying tools.Tool) *ReviewCompleteTool {
	return &ReviewCompleteTool{underlying: underlying}
}

// Name returns the tool name.
func (t *ReviewCompleteTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *ReviewCompleteTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
func (t *ReviewCompleteTool) Exec(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *ReviewCompleteTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the ReviewCompleteResult from tool execution.
func (t *ReviewCompleteTool) ExtractResult(calls []agent.ToolCall, results []any) (ReviewCompleteResult, error) {
	return ExtractReviewComplete(calls, results)
}

// Verify ReviewCompleteTool implements TerminalTool[ReviewCompleteResult].
var _ toolloop.TerminalTool[ReviewCompleteResult] = (*ReviewCompleteTool)(nil)

// SubmitStoriesTool is a terminal tool that wraps submit_stories.
// Used for approving PM specs and generating stories.
type SubmitStoriesTool struct {
	underlying tools.Tool
}

// NewSubmitStoriesTool creates a terminal tool wrapper for submit_stories.
func NewSubmitStoriesTool(underlying tools.Tool) *SubmitStoriesTool {
	return &SubmitStoriesTool{underlying: underlying}
}

// Name returns the tool name.
func (t *SubmitStoriesTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *SubmitStoriesTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
func (t *SubmitStoriesTool) Exec(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *SubmitStoriesTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the SubmitStoriesResult from tool execution.
func (t *SubmitStoriesTool) ExtractResult(calls []agent.ToolCall, results []any) (SubmitStoriesResult, error) {
	return ExtractSubmitStories(calls, results)
}

// Verify SubmitStoriesTool implements TerminalTool[SubmitStoriesResult].
var _ toolloop.TerminalTool[SubmitStoriesResult] = (*SubmitStoriesTool)(nil)
