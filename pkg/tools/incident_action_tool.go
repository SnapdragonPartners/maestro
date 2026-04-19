package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/utils"
)

// createIncidentActionTool is the factory function for the tool registry.
func createIncidentActionTool(ctx *AgentContext) (Tool, error) {
	return NewIncidentActionTool(ctx), nil
}

// getIncidentActionSchema returns the input schema for registry metadata.
func getIncidentActionSchema() InputSchema {
	t := NewIncidentActionTool(nil)
	return t.Definition().InputSchema
}

// IncidentActionTool allows PM to request an action on an open incident.
type IncidentActionTool struct {
	agentCtx *AgentContext
}

// NewIncidentActionTool creates a new incident_action tool.
func NewIncidentActionTool(agentCtx *AgentContext) *IncidentActionTool {
	return &IncidentActionTool{agentCtx: agentCtx}
}

// Name returns the tool name.
func (t *IncidentActionTool) Name() string {
	return ToolIncidentAction
}

// Definition returns the MCP tool definition.
func (t *IncidentActionTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolIncidentAction,
		Description: "Request an action on an open incident. Actions: resume (retry/resume work), try_again (same as resume), skip (abandon the story), change_request (modify the story and retry).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"incident_id": {
					Type:        "string",
					Description: "The ID of the incident to act on (from the incident summary injected into context)",
				},
				"action": {
					Type:        "string",
					Description: "The action to take: 'resume' to retry/resume, 'try_again' to retry, 'skip' to permanently abandon the story, 'change_request' to modify the story requirements and retry.",
					Enum:        []string{"resume", "try_again", "skip", "change_request"},
				},
				"reason": {
					Type:        "string",
					Description: "Brief explanation of why this action is being taken",
				},
				"content": {
					Type:        "string",
					Description: "Required when action is 'change_request': describes what changes the user wants made to the story requirements before retrying.",
				},
			},
			Required: []string{"incident_id", "action", "reason"},
		},
	}
}

// Exec executes the incident_action tool.
func (t *IncidentActionTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	incidentID, ok := utils.SafeAssert[string](args["incident_id"])
	if !ok || incidentID == "" {
		return nil, fmt.Errorf("incident_id is required")
	}

	action, ok := utils.SafeAssert[string](args["action"])
	if !ok || action == "" {
		return nil, fmt.Errorf("action is required")
	}

	validActions := map[string]bool{"resume": true, "try_again": true, "skip": true, "change_request": true}
	if !validActions[action] {
		return nil, fmt.Errorf("unsupported action %q: valid actions are resume, try_again, skip, change_request", action)
	}

	reason, ok := utils.SafeAssert[string](args["reason"])
	if !ok || reason == "" {
		return nil, fmt.Errorf("reason is required")
	}

	content, _ := utils.SafeAssert[string](args["content"])
	if action == "change_request" && content == "" {
		return nil, fmt.Errorf("content is required when action is 'change_request'")
	}

	return &ExecResult{
		Content: fmt.Sprintf("Incident action '%s' sent for %s: %s", action, incidentID, reason),
		ProcessEffect: &ProcessEffect{
			Signal: SignalIncidentAction,
			Data: map[string]any{
				"incident_id": incidentID,
				"action":      action,
				"reason":      reason,
				"content":     content,
			},
		},
	}, nil
}

// PromptDocumentation returns prompt-friendly docs for the tool.
func (t *IncidentActionTool) PromptDocumentation() string {
	return `## incident_action

Request an action on an open incident reported by the development system.

Actions:
- resume: Signal that the blocking condition has been resolved and work should resume.
- try_again: Retry the failed or stalled work (same recovery as resume).
- skip: Permanently abandon the story. Cannot be used if other stories depend on it.
- change_request: Modify the story requirements before retrying. Requires the 'content' parameter describing the changes. Resets the retry budget for a fresh start.

Parameters:
- incident_id (required): The incident ID from the incident summary
- action (required): One of "resume", "try_again", "skip", "change_request"
- reason (required): Why this action is being taken
- content (required for change_request): Description of what changes the user wants made`
}
