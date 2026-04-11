package coder

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils" // SafeAssert, GetMapFieldOr for type-safe map field access
)

// ProbingStatus represents the outcome of adversarial robustness probing.
type ProbingStatus string

const (
	// ProbingPass means no critical robustness issues were found.
	// Advisory findings may still be present.
	ProbingPass ProbingStatus = "pass"
	// ProbingFail means one or more critical robustness issues were found.
	ProbingFail ProbingStatus = "fail"
	// ProbingUnavailable means probing could not complete (LLM error, timeout, etc.).
	ProbingUnavailable ProbingStatus = "unavailable"
	// ProbingSkipped means probing was intentionally not run (wrong story type, disabled, etc.).
	ProbingSkipped ProbingStatus = "skipped"
)

// ProbingOutcome is the result of an adversarial probing run.
// Stored in state machine as KeyProbingEvidence.
type ProbingOutcome struct {
	// Status is the overall probing result.
	Status ProbingStatus
	// Evidence contains structured probing data from submit_probing tool.
	// nil when Status is ProbingUnavailable or ProbingSkipped.
	Evidence map[string]any
	// Reason explains why probing is unavailable or skipped (empty for pass/fail).
	Reason string
}

const (
	maxProbingIterations        = 3
	probingTemperature          = 0.3
	maxProbingFailureMessageLen = 1500
	maxChangedFilesInPrompt     = 50

	// Finding result values used in evidence processing.
	findingResultIssueFound = "issue_found"
	findingSeverityCritical = "critical"
	findingSeverityAdvisory = "advisory"
)

// shouldRunAdversarialProbing checks whether probing should run for the current story.
// Returns true only if: config enabled, app story, not express, not hotfix, verification passed.
func shouldRunAdversarialProbing(sm *agent.BaseStateMachine) bool {
	// Check config kill switch
	if !config.IsAdversarialProbingEnabled() {
		return false
	}

	// Only run for app stories (not devops, maintenance, or unknown)
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, "")
	if storyType != string(proto.StoryTypeApp) {
		return false
	}

	// Skip express stories
	if express := utils.GetStateValueOr[bool](sm, KeyExpress, false); express {
		return false
	}

	// Skip hotfix stories
	if isHotfix := utils.GetStateValueOr[bool](sm, KeyIsHotfix, false); isHotfix {
		return false
	}

	// Only run if verification passed
	veRaw, exists := sm.GetStateValue(KeyVerificationEvidence)
	if !exists || veRaw == nil {
		return false
	}
	outcome, ok := rehydrateVerificationOutcome(veRaw)
	if !ok {
		return false
	}
	return outcome.Status == VerificationPass
}

// runAdversarialProbing runs a bounded, fresh-context LLM loop to probe the
// implementation for edge-case robustness issues.
//
// The loop is read-only (shell only, network disabled) and produces structured findings.
// Returns ProbingOutcome with explicit pass/fail/unavailable status.
//
//nolint:dupl // Outcome processing mirrors verification.go but with different types and signals
func (c *Coder) runAdversarialProbing(
	ctx context.Context,
	sm *agent.BaseStateMachine,
	_ string, // workspacePath — unused, shell tool uses executor's workdir
	changedFiles []string,
) ProbingOutcome {
	c.logger.Info("🔬 Starting adversarial robustness probing")

	// Gather context from state machine
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	testOutput := utils.GetStateValueOr[string](sm, KeyTestOutput, "")
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")

	if taskContent == "" {
		c.logger.Warn("🔬 No task content available for probing, skipping")
		return ProbingOutcome{
			Status: ProbingUnavailable,
			Reason: "no task content available",
		}
	}

	// Create fresh context manager (isolated from coder's main context)
	probingCM := contextmgr.NewContextManager()

	// Build verification summary from stored evidence
	verificationSummary := ""
	if veRaw, exists := sm.GetStateValue(KeyVerificationEvidence); exists && veRaw != nil {
		if outcome, ok := rehydrateVerificationOutcome(veRaw); ok {
			verificationSummary = formatVerificationSummaryForProbing(outcome)
		}
	}

	// Build template data
	templateData := &templates.TemplateData{
		TaskContent: taskContent,
		Plan:        plan,
		TestResults: truncateOutput(testOutput),
		Extra: map[string]any{
			"ChangedFiles":        formatChangedFilesForPrompt(changedFiles),
			"VerificationSummary": verificationSummary,
		},
	}

	// Render probing template with user instructions from .maestro/*instructions*.md
	if c.renderer == nil {
		c.logger.Warn("🔬 Template renderer not available, skipping probing")
		return ProbingOutcome{
			Status: ProbingUnavailable,
			Reason: "template renderer not available",
		}
	}

	prompt, err := c.renderer.RenderWithUserInstructions(
		templates.TestingAdversarialProbingTemplate, templateData, c.workDir, "CODER",
	)
	if err != nil {
		c.logger.Error("🔬 Failed to render probing template: %v", err)
		return ProbingOutcome{
			Status: ProbingUnavailable,
			Reason: fmt.Sprintf("template render error: %v", err),
		}
	}

	// Set up fresh context with probing prompt
	probingCM.ResetForNewTemplate("testing-adversarial-probing", prompt)

	// Create probing tool provider (read-only, network disabled, shell only).
	// Reuses createVerificationToolProvider — same LIMITATION applies:
	// ReadOnly and NetworkDisabled are set on AgentContext but the long-running Docker
	// executor's `docker exec` path does not enforce these flags. The probing loop's
	// containment guarantees therefore rely on the system prompt constraints until
	// executor-level enforcement is added. See docker_long_running.go Run().
	probingProvider := c.createVerificationToolProvider()

	// Get shell as the single general tool
	shellTool, err := probingProvider.Get(tools.ToolShell)
	if err != nil {
		c.logger.Error("🔬 Failed to get shell tool for probing: %v", err)
		return ProbingOutcome{
			Status: ProbingUnavailable,
			Reason: fmt.Sprintf("tool setup error: %v", err),
		}
	}
	generalTools := []tools.Tool{shellTool}

	// Create terminal tool directly (not from provider — follows coding.go pattern)
	terminalTool := tools.NewSubmitProbingTool()

	// Configure toolloop
	loop := toolloop.New(c.LLMClient, c.logger)
	cfg := &toolloop.Config[struct{}]{
		ContextManager:     probingCM,
		GeneralTools:       generalTools,
		TerminalTool:       terminalTool,
		MaxIterations:      maxProbingIterations,
		MaxTokens:          4096,
		Temperature:        probingTemperature,
		AgentID:            c.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		ActivityTracker:    c.activityTracker,
		PersistenceChannel: c.persistenceChannel,
		StoryID:            storyID,
		ToolCircuitBreaker: &toolloop.ToolCircuitBreakerConfig{
			MaxConsecutiveFailures: 3,
			OnTrip: func(_ string, label string, count int) {
				c.logger.Warn("🔌 Circuit breaker tripped in probing: %s (%d failures)", label, count)
			},
		},
	}

	// Run probing loop
	out := toolloop.Run[struct{}](loop, ctx, cfg)

	// Process outcome
	switch out.Kind {
	case toolloop.OutcomeProcessEffect:
		evidence, _ := out.EffectData.(map[string]any)
		switch out.Signal {
		case tools.SignalProbingPass:
			c.logger.Info("🔬 Probing passed (iteration %d)", out.Iteration)
			return ProbingOutcome{Status: ProbingPass, Evidence: evidence}
		case tools.SignalProbingFail:
			c.logger.Info("🔬 Probing found critical issues (iteration %d)", out.Iteration)
			return ProbingOutcome{Status: ProbingFail, Evidence: evidence}
		default:
			c.logger.Warn("🔬 Unexpected probing signal: %s", out.Signal)
			return ProbingOutcome{
				Status: ProbingUnavailable,
				Reason: fmt.Sprintf("unexpected signal: %s", out.Signal),
			}
		}

	case toolloop.OutcomeMaxIterations, toolloop.OutcomeNoToolTwice:
		c.logger.Warn("🔬 Probing loop exhausted without submitting findings (kind=%s, iteration=%d)", out.Kind, out.Iteration)
		return ProbingOutcome{
			Status: ProbingUnavailable,
			Reason: fmt.Sprintf("probing loop exhausted (%s at iteration %d)", out.Kind, out.Iteration),
		}

	case toolloop.OutcomeLLMError:
		c.logger.Error("🔬 LLM error during probing: %v", out.Err)
		return ProbingOutcome{
			Status: ProbingUnavailable,
			Reason: fmt.Sprintf("LLM error: %v", out.Err),
		}

	case toolloop.OutcomeGracefulShutdown:
		c.logger.Info("🔬 Graceful shutdown during probing")
		return ProbingOutcome{
			Status: ProbingUnavailable,
			Reason: "graceful shutdown",
		}

	default:
		c.logger.Warn("🔬 Unexpected probing outcome: %s", out.Kind)
		return ProbingOutcome{
			Status: ProbingUnavailable,
			Reason: fmt.Sprintf("unexpected outcome: %s", out.Kind),
		}
	}
}

// buildProbingFailureMessage extracts critical findings from probing evidence
// and formats an actionable message for the CODING state. Hard-capped at maxProbingFailureMessageLen.
func buildProbingFailureMessage(evidence map[string]any) string {
	if evidence == nil {
		return "Adversarial probing found critical robustness issues but no detailed evidence was provided."
	}

	var b strings.Builder
	b.WriteString("Adversarial probing found the following critical robustness issues:\n\n")

	// Extract critical findings only.
	// The tool emits []any of map[string]any; JSON roundtrip (resume) preserves this shape.
	if findings, ok := utils.SafeAssert[[]any](evidence["findings"]); ok {
		for _, item := range findings {
			fm, fmOK := utils.SafeAssert[map[string]any](item)
			if !fmOK {
				continue
			}

			result := utils.GetMapFieldOr(fm, "result", "")
			severity := utils.GetMapFieldOr(fm, "severity", "")
			if result != findingResultIssueFound || severity != findingSeverityCritical {
				continue
			}

			category := utils.GetMapFieldOr(fm, "category", "")
			description := utils.GetMapFieldOr(fm, "description", "")
			evidenceText := utils.GetMapFieldOr(fm, "evidence", "")

			line := fmt.Sprintf("- [CRITICAL/%s] %s: %s\n", category, description, evidenceText)
			b.WriteString(line)

			if b.Len() > maxProbingFailureMessageLen {
				break
			}
		}
	}

	b.WriteString("\nPlease address these critical robustness issues and call done when ready for re-testing.")

	result := b.String()
	if len(result) > maxProbingFailureMessageLen {
		const truncationSuffix = "\n\n[... truncated for context management ...]"
		result = result[:maxProbingFailureMessageLen-len(truncationSuffix)] + truncationSuffix
	}
	return result
}

// formatProbingEvidence formats a ProbingOutcome for the CODE_REVIEW evidence section.
func formatProbingEvidence(outcome ProbingOutcome) string {
	var b strings.Builder
	b.WriteString("\n## Adversarial Robustness Probing\n")

	switch outcome.Status {
	case ProbingSkipped:
		b.WriteString(fmt.Sprintf("⏭️ Probing skipped: %s\n", outcome.Reason))
		return b.String()

	case ProbingUnavailable:
		b.WriteString(fmt.Sprintf("⚠️ Probing attempted but unavailable: %s\n", outcome.Reason))
		return b.String()

	case ProbingPass:
		b.WriteString("✅ No critical robustness issues found\n")

	case ProbingFail:
		b.WriteString("❌ Critical robustness issues found\n")
	}

	if outcome.Evidence == nil {
		return b.String()
	}

	// Format per-finding results.
	// The tool emits []any of map[string]any; JSON roundtrip (resume) preserves this shape.
	if findings, ok := utils.SafeAssert[[]any](outcome.Evidence["findings"]); ok {
		for _, item := range findings {
			fm, fmOK := utils.SafeAssert[map[string]any](item)
			if !fmOK {
				continue
			}

			result := utils.GetMapFieldOr(fm, "result", "")
			severity := utils.GetMapFieldOr(fm, "severity", "")
			category := utils.GetMapFieldOr(fm, "category", "")
			description := utils.GetMapFieldOr(fm, "description", "")

			icon := "❓" //nolint:goconst // emoji icons in presentation code, not worth extracting
			switch {
			case result == findingResultIssueFound && severity == findingSeverityCritical:
				icon = "🔴"
			case result == findingResultIssueFound && severity == findingSeverityAdvisory:
				icon = "🟡"
			case result == "no_issue":
				icon = "✅"
			case result == "inconclusive":
				icon = "❓"
			}

			b.WriteString(fmt.Sprintf("  %s [%s] %s: %s\n", icon, category, description, severity))
		}
	}

	// Add summary
	if summary := utils.GetMapFieldOr(outcome.Evidence, "summary", ""); summary != "" {
		b.WriteString(fmt.Sprintf("Probing summary: %s\n", summary))
	}

	return b.String()
}

// rehydrateProbingOutcome converts raw state data back into a ProbingOutcome.
// Handles both the direct in-memory path (typed struct) and the post-resume path
// where state persistence round-trips through map[string]any.
//
//nolint:dupl // Structurally similar to rehydrateVerificationOutcome but operates on different types
func rehydrateProbingOutcome(raw any) (ProbingOutcome, bool) {
	// Direct in-memory path: typed struct survives within a single process lifetime.
	if outcome, ok := utils.SafeAssert[ProbingOutcome](raw); ok {
		return outcome, true
	}

	// Post-resume path: state persistence serializes to JSON and restores as map[string]any.
	m, ok := utils.SafeAssert[map[string]any](raw)
	if !ok {
		return ProbingOutcome{}, false
	}

	outcome := ProbingOutcome{}
	if status, statusOK := utils.SafeAssert[string](m["Status"]); statusOK {
		outcome.Status = ProbingStatus(status)
	}
	if evidence, evidenceOK := utils.SafeAssert[map[string]any](m["Evidence"]); evidenceOK {
		outcome.Evidence = evidence
	}
	if reason, reasonOK := utils.SafeAssert[string](m["Reason"]); reasonOK {
		outcome.Reason = reason
	}

	return outcome, outcome.Status != ""
}

// formatChangedFilesForPrompt formats a list of changed files for the probing prompt.
// Caps at maxChangedFilesInPrompt and adds a truncation note if exceeded.
func formatChangedFilesForPrompt(files []string) string {
	if len(files) == 0 {
		return "(no changed files detected)"
	}

	var b strings.Builder
	limit := len(files)
	truncated := false
	if limit > maxChangedFilesInPrompt {
		limit = maxChangedFilesInPrompt
		truncated = true
	}

	for _, f := range files[:limit] {
		b.WriteString(fmt.Sprintf("- %s\n", f))
	}

	if truncated {
		b.WriteString(fmt.Sprintf("\n(%d more files not shown — focus on the files above)\n", len(files)-maxChangedFilesInPrompt))
	}

	return b.String()
}

// formatVerificationSummaryForProbing creates a brief summary of verification results
// for the probing prompt, so the probing agent doesn't re-check verified criteria.
func formatVerificationSummaryForProbing(outcome VerificationOutcome) string {
	if outcome.Evidence == nil {
		return ""
	}

	var b strings.Builder
	if criteria, ok := utils.SafeAssert[[]any](outcome.Evidence["acceptance_criteria_checked"]); ok {
		for _, item := range criteria {
			cm, cmOK := utils.SafeAssert[map[string]any](item)
			if !cmOK {
				continue
			}
			criterion := utils.GetMapFieldOr(cm, "criterion", "")
			result := utils.GetMapFieldOr(cm, "result", "")
			b.WriteString(fmt.Sprintf("- %s: %s\n", criterion, result))
		}
	}
	return b.String()
}
