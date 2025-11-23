package coder

import (
	"context"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/tools"
)

// PlanSubmitTool is a terminal tool that wraps submit_plan for the planning phase.
// Signals: PLAN_REVIEW
type PlanSubmitTool struct {
	underlying tools.Tool
}

// NewPlanSubmitTool creates a terminal tool wrapper for submit_plan.
func NewPlanSubmitTool(underlying tools.Tool) *PlanSubmitTool {
	return &PlanSubmitTool{underlying: underlying}
}

// Name returns the tool name.
func (t *PlanSubmitTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *PlanSubmitTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
func (t *PlanSubmitTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *PlanSubmitTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the PlanningResult from tool execution.
func (t *PlanSubmitTool) ExtractResult(calls []agent.ToolCall, results []any) (PlanningResult, error) {
	return ExtractPlanningResult(calls, results)
}

// Verify PlanSubmitTool implements TerminalTool[PlanningResult].
var _ toolloop.TerminalTool[PlanningResult] = (*PlanSubmitTool)(nil)

// DoneTool is a terminal tool that wraps done for the coding phase.
// Signals: TESTING
type DoneTool struct {
	underlying tools.Tool
}

// NewDoneTool creates a terminal tool wrapper for done.
func NewDoneTool(underlying tools.Tool) *DoneTool {
	return &DoneTool{underlying: underlying}
}

// Name returns the tool name.
func (t *DoneTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *DoneTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
func (t *DoneTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *DoneTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the CodingResult from tool execution.
func (t *DoneTool) ExtractResult(calls []agent.ToolCall, results []any) (CodingResult, error) {
	return ExtractCodingResult(calls, results)
}

// Verify DoneTool implements TerminalTool[CodingResult].
var _ toolloop.TerminalTool[CodingResult] = (*DoneTool)(nil)

// AskQuestionTool is a terminal tool that wraps ask_question for the coding phase.
// Signals: QUESTION
type AskQuestionTool struct {
	underlying tools.Tool
}

// NewAskQuestionTool creates a terminal tool wrapper for ask_question.
func NewAskQuestionTool(underlying tools.Tool) *AskQuestionTool {
	return &AskQuestionTool{underlying: underlying}
}

// Name returns the tool name.
func (t *AskQuestionTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *AskQuestionTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
func (t *AskQuestionTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *AskQuestionTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the CodingResult from tool execution.
func (t *AskQuestionTool) ExtractResult(calls []agent.ToolCall, results []any) (CodingResult, error) {
	return ExtractCodingResult(calls, results)
}

// Verify AskQuestionTool implements TerminalTool[CodingResult].
var _ toolloop.TerminalTool[CodingResult] = (*AskQuestionTool)(nil)

// TodosAddTool is a terminal tool that wraps todos_add for the plan review phase.
// Signals: CODING
type TodosAddTool struct {
	underlying tools.Tool
}

// NewTodosAddTool creates a terminal tool wrapper for todos_add.
func NewTodosAddTool(underlying tools.Tool) *TodosAddTool {
	return &TodosAddTool{underlying: underlying}
}

// Name returns the tool name.
func (t *TodosAddTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *TodosAddTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
func (t *TodosAddTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *TodosAddTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the TodoCollectionResult from tool execution.
func (t *TodosAddTool) ExtractResult(calls []agent.ToolCall, results []any) (TodoCollectionResult, error) {
	return ExtractTodoCollectionResult(calls, results)
}

// Verify TodosAddTool implements TerminalTool[TodoCollectionResult].
var _ toolloop.TerminalTool[TodoCollectionResult] = (*TodosAddTool)(nil)

// MarkStoryCompleteTool is a terminal tool that wraps mark_story_complete for the planning phase.
// Signals: STORY_COMPLETE
// NOTE: Planning phase has TWO terminal tools (submit_plan and mark_story_complete).
// This violates "one goal, one exit" - we need to combine these in Phase 3.
type MarkStoryCompleteTool struct {
	underlying tools.Tool
}

// NewMarkStoryCompleteTool creates a terminal tool wrapper for mark_story_complete.
func NewMarkStoryCompleteTool(underlying tools.Tool) *MarkStoryCompleteTool {
	return &MarkStoryCompleteTool{underlying: underlying}
}

// Name returns the tool name.
func (t *MarkStoryCompleteTool) Name() string {
	return t.underlying.Name()
}

// Definition returns the tool definition.
func (t *MarkStoryCompleteTool) Definition() tools.ToolDefinition {
	return t.underlying.Definition()
}

// Exec executes the underlying tool.
func (t *MarkStoryCompleteTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	return t.underlying.Exec(ctx, args)
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *MarkStoryCompleteTool) PromptDocumentation() string {
	return t.underlying.PromptDocumentation()
}

// ExtractResult extracts the PlanningResult from tool execution.
func (t *MarkStoryCompleteTool) ExtractResult(calls []agent.ToolCall, results []any) (PlanningResult, error) {
	return ExtractPlanningResult(calls, results)
}

// Verify MarkStoryCompleteTool implements TerminalTool[PlanningResult].
var _ toolloop.TerminalTool[PlanningResult] = (*MarkStoryCompleteTool)(nil)
