package coder

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// VerificationStatus represents the outcome of acceptance-criteria verification.
type VerificationStatus string

const (
	// VerificationPass means no acceptance criteria were found to fail.
	// Note: partial and unverified criteria also produce this status —
	// only an explicit "fail" result triggers VerificationFail.
	VerificationPass VerificationStatus = "pass"
	// VerificationFail means one or more acceptance criteria failed verification.
	VerificationFail VerificationStatus = "fail"
	// VerificationUnavailable means verification could not complete (LLM error, timeout, etc.).
	VerificationUnavailable VerificationStatus = "unavailable"
)

// VerificationOutcome is the result of an acceptance-criteria verification run.
// Stored in state machine as KeyVerificationEvidence.
type VerificationOutcome struct {
	// Status is the overall verification result.
	Status VerificationStatus
	// Evidence contains structured verification data from submit_verification tool.
	// nil when Status is VerificationUnavailable.
	Evidence map[string]any
	// Reason explains why verification is unavailable (empty for pass/fail).
	Reason string
}

const (
	maxVerificationIterations = 5
	verificationTemperature   = 0.2
	maxFailureMessageLen      = 1500

	// Criterion result values used in evidence processing.
	criterionResultFail    = "fail"
	criterionResultPartial = "partial"
)

// runAcceptanceCriteriaVerification runs a bounded, fresh-context LLM loop to verify
// that the implementation satisfies the story's acceptance criteria.
//
// The loop is read-only (shell only, network disabled) and produces structured evidence.
// Returns VerificationOutcome with explicit pass/fail/unavailable status.
//
//nolint:dupl // Outcome processing mirrors probing.go but with different types and signals
func (c *Coder) runAcceptanceCriteriaVerification(
	ctx context.Context,
	sm *agent.BaseStateMachine,
	_ string, // workspacePath — unused, shell tool uses executor's workdir
	changedFiles []string,
) VerificationOutcome {
	c.logger.Info("🔍 Starting acceptance-criteria verification")

	// Gather context from state machine
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	testOutput := utils.GetStateValueOr[string](sm, KeyTestOutput, "")
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")

	if taskContent == "" {
		c.logger.Warn("🔍 No task content available for verification, skipping")
		return VerificationOutcome{
			Status: VerificationUnavailable,
			Reason: "no task content available",
		}
	}

	// Create fresh context manager (isolated from coder's main context)
	verificationCM := contextmgr.NewContextManager()

	// Build template data
	templateData := &templates.TemplateData{
		TaskContent: taskContent,
		Plan:        plan,
		TestResults: truncateOutput(testOutput),
		Extra: map[string]any{
			"ChangedFiles": formatChangedFilesForPrompt(changedFiles),
		},
	}

	// Render verification template with user instructions from .maestro/*instructions*.md
	if c.renderer == nil {
		c.logger.Warn("🔍 Template renderer not available, skipping verification")
		return VerificationOutcome{
			Status: VerificationUnavailable,
			Reason: "template renderer not available",
		}
	}

	prompt, err := c.renderer.RenderWithUserInstructions(
		templates.TestingVerificationTemplate, templateData, c.workDir, "CODER",
	)
	if err != nil {
		c.logger.Error("🔍 Failed to render verification template: %v", err)
		return VerificationOutcome{
			Status: VerificationUnavailable,
			Reason: fmt.Sprintf("template render error: %v", err),
		}
	}

	// Set up fresh context with verification prompt
	verificationCM.ResetForNewTemplate("testing-verification", prompt)

	// Create verification tool provider (read-only, network disabled, shell only)
	verificationProvider := c.createVerificationToolProvider()

	// Get shell as the single general tool
	shellTool, err := verificationProvider.Get(tools.ToolShell)
	if err != nil {
		c.logger.Error("🔍 Failed to get shell tool for verification: %v", err)
		return VerificationOutcome{
			Status: VerificationUnavailable,
			Reason: fmt.Sprintf("tool setup error: %v", err),
		}
	}
	generalTools := []tools.Tool{shellTool}

	// Create terminal tool directly (not from provider — follows coding.go pattern)
	terminalTool := tools.NewSubmitVerificationTool()

	// Configure toolloop
	loop := toolloop.New(c.LLMClient, c.logger)
	cfg := &toolloop.Config[struct{}]{
		ContextManager:     verificationCM,
		GeneralTools:       generalTools,
		TerminalTool:       terminalTool,
		MaxIterations:      maxVerificationIterations,
		MaxTokens:          4096,
		Temperature:        verificationTemperature,
		AgentID:            c.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		ActivityTracker:    c.activityTracker,
		PersistenceChannel: c.persistenceChannel,
		StoryID:            storyID,
		ToolCircuitBreaker: &toolloop.ToolCircuitBreakerConfig{
			MaxConsecutiveFailures: 3,
			OnTrip: func(_ string, label string, count int) {
				c.logger.Warn("🔌 Circuit breaker tripped in verification: %s (%d failures)", label, count)
			},
		},
		BeforeIteration: func(iteration int, cm *contextmgr.ContextManager) {
			if iteration == maxVerificationIterations-1 {
				c.logger.Info("🔍 Injecting submit reminder at iteration %d", iteration)
				cm.AddMessage("user", "You have 1 tool call remaining. Do not use shell again unless absolutely necessary. Call submit_verification now. If any criterion is not fully proven, mark it partial or unverified and submit.")
			}
		},
	}

	// Run verification loop
	out := toolloop.Run[struct{}](loop, ctx, cfg)

	// Process outcome
	switch out.Kind {
	case toolloop.OutcomeProcessEffect:
		evidence, _ := out.EffectData.(map[string]any)
		switch out.Signal {
		case tools.SignalVerificationPass:
			c.logger.Info("🔍 Verification passed (iteration %d)", out.Iteration)
			return VerificationOutcome{Status: VerificationPass, Evidence: evidence}
		case tools.SignalVerificationFail:
			c.logger.Info("🔍 Verification found gaps (iteration %d)", out.Iteration)
			return VerificationOutcome{Status: VerificationFail, Evidence: evidence}
		default:
			c.logger.Warn("🔍 Unexpected verification signal: %s", out.Signal)
			return VerificationOutcome{
				Status: VerificationUnavailable,
				Reason: fmt.Sprintf("unexpected signal: %s", out.Signal),
			}
		}

	case toolloop.OutcomeMaxIterations, toolloop.OutcomeNoToolTwice:
		c.logger.Warn("🔍 Verification loop exhausted without submitting evidence (kind=%s, iteration=%d)", out.Kind, out.Iteration)
		return VerificationOutcome{
			Status: VerificationUnavailable,
			Reason: fmt.Sprintf("verification loop exhausted (%s at iteration %d)", out.Kind, out.Iteration),
		}

	case toolloop.OutcomeLLMError:
		c.logger.Error("🔍 LLM error during verification: %v", out.Err)
		return VerificationOutcome{
			Status: VerificationUnavailable,
			Reason: fmt.Sprintf("LLM error: %v", out.Err),
		}

	case toolloop.OutcomeGracefulShutdown:
		c.logger.Info("🔍 Graceful shutdown during verification")
		return VerificationOutcome{
			Status: VerificationUnavailable,
			Reason: "graceful shutdown",
		}

	default:
		c.logger.Warn("🔍 Unexpected verification outcome: %s", out.Kind)
		return VerificationOutcome{
			Status: VerificationUnavailable,
			Reason: fmt.Sprintf("unexpected outcome: %s", out.Kind),
		}
	}
}

// createVerificationToolProvider creates a read-only, network-disabled ToolProvider
// with only shell available for acceptance-criteria verification.
//
// LIMITATION: ReadOnly and NetworkDisabled are set on AgentContext and forwarded to
// exec.Opts by the shell tool, but the long-running Docker executor's `docker exec`
// path does not currently enforce these flags (it only applies user, workdir, and env).
// The verification loop's read-only and network-disabled guarantees therefore rely on
// the LLM following the system prompt constraints until executor-level enforcement
// is added. See docker_long_running.go Run() for the exec path.
func (c *Coder) createVerificationToolProvider() *tools.ToolProvider {
	agentCtx := tools.AgentContext{
		Executor:        c.longRunningExecutor,
		Agent:           c,
		ChatService:     c.chatService,
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         c.workDir,
		AgentID:         c.GetAgentID(),
	}
	return tools.NewProvider(&agentCtx, tools.VerificationTools)
}

// buildVerificationFailureMessage extracts failed/partial criteria from verification evidence
// and formats an actionable message for the CODING state. Hard-capped at maxFailureMessageLen.
func buildVerificationFailureMessage(evidence map[string]any) string {
	if evidence == nil {
		return "Acceptance criteria verification failed but no detailed evidence was provided."
	}

	var b strings.Builder
	b.WriteString("Acceptance criteria verification found the following gaps:\n\n")

	// Extract failed/partial criteria only.
	// The tool emits []any of map[string]any; JSON roundtrip (resume) preserves this shape.
	if criteria, ok := evidence["acceptance_criteria_checked"].([]any); ok {
		for _, item := range criteria {
			cm, ok := item.(map[string]any)
			if !ok {
				continue
			}

			result, _ := cm["result"].(string)
			if result != criterionResultFail && result != criterionResultPartial {
				continue
			}

			criterion, _ := cm["criterion"].(string)
			evidenceText, _ := cm["evidence"].(string)

			tag := "FAIL"
			if result == criterionResultPartial {
				tag = "PARTIAL"
			}

			line := fmt.Sprintf("- [%s] %s: %s\n", tag, criterion, evidenceText)
			b.WriteString(line)

			if b.Len() > maxFailureMessageLen {
				break
			}
		}
	}

	// Add gaps (tool emits []any of string; JSON roundtrip preserves this)
	if gaps, ok := evidence["gaps"].([]any); ok && len(gaps) > 0 {
		b.WriteString("\nAdditional gaps:\n")
		for _, g := range gaps {
			if gs, ok := g.(string); ok {
				b.WriteString(fmt.Sprintf("- %s\n", gs))
				if b.Len() > maxFailureMessageLen {
					break
				}
			}
		}
	}

	b.WriteString("\nPlease address these gaps and call done when ready for re-testing.")

	result := b.String()
	if len(result) > maxFailureMessageLen {
		const truncationSuffix = "\n\n[... truncated for context management ...]"
		result = result[:maxFailureMessageLen-len(truncationSuffix)] + truncationSuffix
	}
	return result
}

// formatVerificationEvidence formats a VerificationOutcome for the CODE_REVIEW evidence section.
func formatVerificationEvidence(outcome VerificationOutcome) string {
	var b strings.Builder
	b.WriteString("\n## Acceptance Criteria Verification\n")

	switch outcome.Status {
	case VerificationUnavailable:
		b.WriteString(fmt.Sprintf("⚠️ Verification attempted but unavailable: %s\n", outcome.Reason))
		return b.String()

	case VerificationPass:
		b.WriteString("✅ No acceptance criteria failures found\n")

	case VerificationFail:
		b.WriteString("❌ Acceptance criteria gaps found\n")
	}

	if outcome.Evidence == nil {
		return b.String()
	}

	// Format per-criterion results.
	// The tool emits []any of map[string]any; JSON roundtrip (resume) preserves this shape.
	if criteria, ok := outcome.Evidence["acceptance_criteria_checked"].([]any); ok {
		for _, item := range criteria {
			cm, ok := item.(map[string]any)
			if !ok {
				continue
			}

			result, _ := cm["result"].(string)
			criterion, _ := cm["criterion"].(string)

			icon := "❓"
			switch result {
			case "pass":
				icon = "✅"
			case "fail":
				icon = "❌"
			case "partial":
				icon = "⚠️"
			case "unverified":
				icon = "❓"
			}
			b.WriteString(fmt.Sprintf("  %s %s\n", icon, criterion))
		}
	}

	// Add confidence
	if confidence, ok := outcome.Evidence["confidence"].(string); ok {
		b.WriteString(fmt.Sprintf("Verification confidence: %s\n", confidence))
	}

	// Add gaps if any
	if gaps, ok := outcome.Evidence["gaps"].([]any); ok && len(gaps) > 0 {
		b.WriteString("Gaps:\n")
		for _, g := range gaps {
			if gs, ok := g.(string); ok {
				b.WriteString(fmt.Sprintf("  - %s\n", gs))
			}
		}
	}

	return b.String()
}

// rehydrateVerificationOutcome converts raw state data back into a VerificationOutcome.
// Handles both the direct in-memory path (typed struct) and the post-resume path
// where state persistence round-trips through map[string]any.
//
//nolint:dupl // Structurally similar to rehydrateProbingOutcome but operates on different types
func rehydrateVerificationOutcome(raw any) (VerificationOutcome, bool) {
	// Direct in-memory path: typed struct survives within a single process lifetime.
	if outcome, ok := raw.(VerificationOutcome); ok {
		return outcome, true
	}

	// Post-resume path: state persistence serializes to JSON and restores as map[string]any.
	m, ok := raw.(map[string]any)
	if !ok {
		return VerificationOutcome{}, false
	}

	outcome := VerificationOutcome{}
	if status, ok := m["Status"].(string); ok {
		outcome.Status = VerificationStatus(status)
	}
	if evidence, ok := m["Evidence"].(map[string]any); ok {
		outcome.Evidence = evidence
	}
	if reason, ok := m["Reason"].(string); ok {
		outcome.Reason = reason
	}

	return outcome, outcome.Status != ""
}
