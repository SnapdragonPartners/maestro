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
// Phase 1.5 supports only the "resume" action.
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
		Description: "Request an action on an open incident. Currently only 'resume' is supported, which tells the development system to retry or resume work related to the incident.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"incident_id": {
					Type:        "string",
					Description: "The ID of the incident to act on (from the incident summary injected into context)",
				},
				"action": {
					Type:        "string",
					Description: "The action to take. Currently only 'resume' is supported.",
					Enum:        []string{"resume"},
				},
				"reason": {
					Type:        "string",
					Description: "Brief explanation of why this action is being taken (e.g., 'user requested retry', 'underlying issue resolved')",
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

	if action != "resume" {
		return nil, fmt.Errorf("unsupported action %q: only 'resume' is supported", action)
	}

	reason, ok := utils.SafeAssert[string](args["reason"])
	if !ok || reason == "" {
		return nil, fmt.Errorf("reason is required")
	}

	return &ExecResult{
		Content: fmt.Sprintf("Incident action '%s' sent for %s: %s", action, incidentID, reason),
		ProcessEffect: &ProcessEffect{
			Signal: SignalIncidentAction,
			Data: map[string]any{
				"incident_id": incidentID,
				"action":      action,
				"reason":      reason,
			},
		},
	}, nil
}

// PromptDocumentation returns prompt-friendly docs for the tool.
func (t *IncidentActionTool) PromptDocumentation() string {
	return `## incident_action

Request an action on an open incident reported by the development system.

Currently only the 'resume' action is supported. Use this when the user wants to retry
work that has stalled or failed. The system will attempt to recover and resume work
associated with the incident.

Parameters:
- incident_id (required): The incident ID from the incident summary (e.g., "incident-system_idle-system-20260419T154212Z")
- action (required): The action to take (currently only "resume")
- reason (required): Why this action is being taken (e.g., "user requested retry")`
}
