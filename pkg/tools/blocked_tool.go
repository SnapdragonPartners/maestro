package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// createReportBlockedTool is the factory function for the tool registry.
func createReportBlockedTool(ctx *AgentContext) (Tool, error) {
	return NewReportBlockedTool(ctx.Agent), nil
}

// getReportBlockedSchema returns the input schema for registry metadata.
func getReportBlockedSchema() InputSchema {
	t := NewReportBlockedTool(nil)
	return t.Definition().InputSchema
}

// ReportBlockedTool allows a coder to signal that it cannot proceed due to
// issues outside its control. Available in PLANNING and CODING states.
//
// Two failure kinds are exposed:
//   - story_invalid: story requirements are unclear, contradictory, or impossible
//   - external: infrastructure/environment issue (git corruption, container, dependencies)
//
// Returns a ProcessEffect with SignalBlocked, carrying structured FailureInfo.
type ReportBlockedTool struct {
	agent Agent // Optional agent reference to get current state dynamically
}

// NewReportBlockedTool creates a new report_blocked tool instance.
func NewReportBlockedTool(agent Agent) *ReportBlockedTool {
	return &ReportBlockedTool{
		agent: agent,
	}
}

// Definition returns the tool's definition in Claude API format.
func (r *ReportBlockedTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: ToolReportBlocked,
		Description: "Report that you are blocked and cannot proceed. " +
			"Use story_invalid when the story requirements are unclear, contradictory, or impossible to implement. " +
			"Use external when infrastructure or environment issues prevent progress " +
			"(git corruption, container problems, missing build dependencies). " +
			"This will stop your current work and escalate to the architect for resolution.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"failure_kind": {
					Type:        "string",
					Description: "Classification of the blocking issue",
					Enum:        []string{string(proto.FailureKindStoryInvalid), string(proto.FailureKindExternal)},
				},
				"explanation": {
					Type:        "string",
					Description: "Detailed explanation of why you are blocked and what you tried",
				},
			},
			Required: []string{"failure_kind", "explanation"},
		},
	}
}

// Name returns the tool identifier.
func (r *ReportBlockedTool) Name() string {
	return ToolReportBlocked
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (r *ReportBlockedTool) PromptDocumentation() string {
	return `- **report_blocked** - Report that you cannot proceed due to issues outside your control
  - Parameters: failure_kind (required), explanation (required)
  - failure_kind: "story_invalid" (requirements are unclear/contradictory/impossible) or "external" (infrastructure/environment issue)
  - explanation: Detailed description of the blocking issue and what you attempted
  - This stops your current work and escalates to the architect for resolution
  - Use only when you genuinely cannot make progress, not for normal difficulties`
}

// Exec reports the coder as blocked, returning a ProcessEffect to exit the toolloop.
func (r *ReportBlockedTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Extract failure_kind
	kindStr, ok := utils.SafeAssert[string](args["failure_kind"])
	if !ok || kindStr == "" {
		return nil, fmt.Errorf("failure_kind is required and must be a non-empty string")
	}

	// Validate failure_kind
	kind := proto.FailureKind(kindStr)
	switch kind {
	case proto.FailureKindStoryInvalid, proto.FailureKindExternal:
		// Valid
	default:
		return nil, fmt.Errorf("failure_kind must be 'story_invalid' or 'external', got %q", kindStr)
	}

	// Extract explanation
	explanation, ok := utils.SafeAssert[string](args["explanation"])
	if !ok || explanation == "" {
		return nil, fmt.Errorf("explanation is required and must be a non-empty string")
	}

	// Get the current state from the agent if available
	failedState := "UNKNOWN"
	if r.agent != nil {
		failedState = string(r.agent.GetCurrentState())
	}

	failureInfo := proto.NewFailureInfo(kind, explanation, failedState, "")

	return &ExecResult{
		Content: fmt.Sprintf("Reported as blocked (%s): %s. Escalating to architect.", kind, explanation),
		ProcessEffect: &ProcessEffect{
			Signal: SignalBlocked,
			Data: map[string]any{
				proto.KeyFailureInfo: failureInfo,
			},
		},
	}, nil
}
