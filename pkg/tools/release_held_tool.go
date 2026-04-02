package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/utils"
)

// createReleaseHeldStoriesTool is the factory function for the tool registry.
func createReleaseHeldStoriesTool(ctx *AgentContext) (Tool, error) {
	return NewReleaseHeldStoriesTool(ctx), nil
}

// getReleaseHeldStoriesSchema returns the input schema for registry metadata.
func getReleaseHeldStoriesSchema() InputSchema {
	t := NewReleaseHeldStoriesTool(nil)
	return t.Definition().InputSchema
}

// ReleaseHeldStoriesTool allows PM to signal the architect to release held stories
// after a human confirms that a system repair or prerequisite issue is resolved.
type ReleaseHeldStoriesTool struct {
	agentCtx *AgentContext
}

// NewReleaseHeldStoriesTool creates a new release_held_stories tool.
func NewReleaseHeldStoriesTool(agentCtx *AgentContext) *ReleaseHeldStoriesTool {
	return &ReleaseHeldStoriesTool{agentCtx: agentCtx}
}

// Name returns the tool name.
func (t *ReleaseHeldStoriesTool) Name() string {
	return ToolReleaseHeldStories
}

// Definition returns the MCP tool definition.
func (t *ReleaseHeldStoriesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolReleaseHeldStories,
		Description: "Signal the development system to release stories that are on hold due to a failure. Call this after the user confirms that the underlying issue (e.g., expired credentials, infrastructure problem) has been resolved.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"reason": {
					Type:        "string",
					Description: "Brief description of what was resolved (e.g., 'API key rotated', 'disk space freed')",
				},
				"failure_id": {
					Type:        "string",
					Description: "Optional: specific failure ID to release. If omitted, releases all held stories.",
				},
			},
			Required: []string{"reason"},
		},
	}
}

// Exec executes the release_held_stories tool.
func (t *ReleaseHeldStoriesTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	reason, ok := utils.SafeAssert[string](args["reason"])
	if !ok || reason == "" {
		return nil, fmt.Errorf("reason is required")
	}

	var failureID string
	if fid, fidOK := utils.SafeAssert[string](args["failure_id"]); fidOK {
		failureID = fid
	}

	return &ExecResult{
		Content: fmt.Sprintf("Release signal sent: %s", reason),
		ProcessEffect: &ProcessEffect{
			Signal: SignalReleaseHeld,
			Data: map[string]any{
				"reason":     reason,
				"failure_id": failureID,
			},
		},
	}, nil
}

// PromptDocumentation returns prompt-friendly docs for the tool.
func (t *ReleaseHeldStoriesTool) PromptDocumentation() string {
	return `## release_held_stories

Signal the development system to release stories that are on hold due to a failure.

Call this tool ONLY after the user has confirmed that the underlying issue has been resolved.
For example: API keys have been rotated, disk space has been freed, credentials have been updated.

Parameters:
- reason (required): Brief description of what was resolved
- failure_id (optional): Specific failure ID to release (omit to release all held stories)`
}
