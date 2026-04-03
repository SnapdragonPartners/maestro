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
			"Use environment when the local or shared execution environment is broken " +
			"(git corruption, container problems, broken toolchain, disk space, permissions). " +
			"Use prerequisite when an external dependency is missing or invalid " +
			"(API credentials, access tokens, third-party service unavailable, missing configuration). " +
			"This will stop your current work and escalate to the architect for resolution.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"failure_kind": {
					Type:        "string",
					Description: "Classification of the blocking issue",
					Enum:        []string{string(proto.FailureKindStoryInvalid), string(proto.FailureKindEnvironment), string(proto.FailureKindPrerequisite)},
				},
				"explanation": {
					Type:        "string",
					Description: "Detailed explanation of why you are blocked and what you tried",
				},
				"scope_guess": {
					Type:        "string",
					Description: "Your best guess at the blast radius. attempt=only my workspace, story=this story can't be implemented as written, epoch=multiple stories in this spec may be affected, system=shared infrastructure is broken for everyone",
					Enum:        []string{string(proto.FailureScopeAttempt), string(proto.FailureScopeStory), string(proto.FailureScopeEpoch), string(proto.FailureScopeSystem)},
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
  - Parameters: failure_kind (required), explanation (required), scope_guess (optional)
  - failure_kind: "story_invalid" (requirements are unclear/contradictory/impossible), "environment" (execution environment broken: git corruption, container, toolchain, disk, permissions), or "prerequisite" (external dependency missing/invalid: API credentials, access tokens, third-party service)
  - explanation: Detailed description of the blocking issue and what you attempted
  - scope_guess: "attempt" (only my workspace), "story" (this story), "epoch" (multiple stories), "system" (shared infrastructure)
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
	case proto.FailureKindStoryInvalid, proto.FailureKindEnvironment, proto.FailureKindPrerequisite:
		// Valid
	case proto.FailureKindExternal:
		// Backward compat: map deprecated "external" to "environment"
		kind = proto.NormalizeFailureKind(kind)
	default:
		return nil, fmt.Errorf("failure_kind must be 'story_invalid', 'environment', or 'prerequisite', got %q", kindStr)
	}

	// Extract explanation
	explanation, ok := utils.SafeAssert[string](args["explanation"])
	if !ok || explanation == "" {
		return nil, fmt.Errorf("explanation is required and must be a non-empty string")
	}

	// Extract optional scope_guess with validation
	var scopeGuess proto.FailureScope
	if scopeStr, scopeOK := utils.SafeAssert[string](args["scope_guess"]); scopeOK && scopeStr != "" {
		scopeGuess = proto.FailureScope(scopeStr)
		switch scopeGuess {
		case proto.FailureScopeAttempt, proto.FailureScopeStory, proto.FailureScopeEpoch, proto.FailureScopeSystem:
			// Valid scope_guess
		default:
			return nil, fmt.Errorf("scope_guess must be one of 'attempt', 'story', 'epoch', or 'system', got %q", scopeStr)
		}
	}

	// Get the current state from the agent if available
	failedState := "UNKNOWN"
	if r.agent != nil {
		failedState = string(r.agent.GetCurrentState())
	}

	failureInfo := proto.NewFailureInfo(kind, explanation, failedState, "")
	failureInfo.ScopeGuess = scopeGuess
	failureInfo.Source = proto.FailureSourceLLMReport
	failureInfo.Evidence = []proto.FailureEvidence{
		{
			Kind:    "llm_report",
			Summary: fmt.Sprintf("Coder reported blocked: %s", kind),
			Snippet: utils.SanitizeString(explanation, 1000),
		},
	}

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
