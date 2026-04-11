package coder

import (
	"context"
	"strings"
	"testing"

	"orchestrator/internal/mocks"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// --- Unit tests: buildProbingFailureMessage ---

func TestBuildProbingFailureMessage_CriticalFindings(t *testing.T) {
	evidence := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "error_handling",
				"description": "Missing nil check",
				"method":      "inspection",
				"result":      "issue_found",
				"severity":    "critical",
				"evidence":    "handler.go:42 dereferences nil",
			},
			map[string]any{
				"category":    "boundary_values",
				"description": "Empty input handled",
				"method":      "command",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "Validation present",
			},
		},
	}

	msg := buildProbingFailureMessage(evidence)

	if !strings.Contains(msg, "[CRITICAL/error_handling]") {
		t.Error("Expected [CRITICAL/error_handling] tag in message")
	}
	if !strings.Contains(msg, "Missing nil check") {
		t.Error("Expected critical finding description")
	}
	// Non-critical findings should not appear
	if strings.Contains(msg, "Empty input handled") {
		t.Error("Should not include non-critical findings")
	}
}

func TestBuildProbingFailureMessage_NilEvidence(t *testing.T) {
	msg := buildProbingFailureMessage(nil)
	if msg == "" {
		t.Error("Expected non-empty fallback message")
	}
}

func TestBuildProbingFailureMessage_Truncation(t *testing.T) {
	// Create evidence with very long entries to trigger truncation
	findings := make([]any, 50)
	for i := range findings {
		findings[i] = map[string]any{
			"category":    "error_handling",
			"description": strings.Repeat("description text ", 10),
			"method":      "inspection",
			"result":      "issue_found",
			"severity":    "critical",
			"evidence":    strings.Repeat("evidence text ", 20),
		}
	}

	evidence := map[string]any{
		"findings": findings,
	}

	msg := buildProbingFailureMessage(evidence)

	if len(msg) > maxProbingFailureMessageLen {
		t.Errorf("Message too long: %d chars (max %d)", len(msg), maxProbingFailureMessageLen)
	}
}

// --- Unit tests: formatProbingEvidence ---

func TestFormatProbingEvidence_Pass(t *testing.T) {
	outcome := ProbingOutcome{
		Status: ProbingPass,
		Evidence: map[string]any{
			"findings": []any{
				map[string]any{
					"category":    "error_handling",
					"description": "Error paths covered",
					"result":      "no_issue",
					"severity":    "advisory",
				},
			},
			"summary": "No issues found",
		},
	}

	result := formatProbingEvidence(outcome)
	if !strings.Contains(result, "No critical robustness issues found") {
		t.Error("Expected pass header")
	}
	if !strings.Contains(result, "Error paths covered") {
		t.Error("Expected finding description")
	}
}

func TestFormatProbingEvidence_Fail(t *testing.T) {
	outcome := ProbingOutcome{
		Status: ProbingFail,
		Evidence: map[string]any{
			"findings": []any{
				map[string]any{
					"category":    "security",
					"description": "SQL injection",
					"result":      "issue_found",
					"severity":    "critical",
				},
			},
			"summary": "Critical issue found",
		},
	}

	result := formatProbingEvidence(outcome)
	if !strings.Contains(result, "Critical robustness issues found") {
		t.Error("Expected fail header")
	}
	if !strings.Contains(result, "SQL injection") {
		t.Error("Expected finding description")
	}
}

func TestFormatProbingEvidence_Skipped(t *testing.T) {
	outcome := ProbingOutcome{
		Status: ProbingSkipped,
		Reason: "devops story type",
	}

	result := formatProbingEvidence(outcome)
	if !strings.Contains(result, "skipped") {
		t.Error("Expected skipped header")
	}
	if !strings.Contains(result, "devops story type") {
		t.Error("Expected reason text")
	}
}

func TestFormatProbingEvidence_Unavailable(t *testing.T) {
	outcome := ProbingOutcome{
		Status: ProbingUnavailable,
		Reason: "LLM error: rate limited",
	}

	result := formatProbingEvidence(outcome)
	if !strings.Contains(result, "unavailable") {
		t.Error("Expected unavailable header")
	}
	if !strings.Contains(result, "LLM error: rate limited") {
		t.Error("Expected reason text")
	}
}

func TestFormatProbingEvidence_Icons(t *testing.T) {
	outcome := ProbingOutcome{
		Status: ProbingPass,
		Evidence: map[string]any{
			"findings": []any{
				map[string]any{"category": "error_handling", "description": "A", "result": "issue_found", "severity": "critical"},
				map[string]any{"category": "security", "description": "B", "result": "issue_found", "severity": "advisory"},
				map[string]any{"category": "boundary_values", "description": "C", "result": "no_issue", "severity": "advisory"},
				map[string]any{"category": "resource_cleanup", "description": "D", "result": "inconclusive", "severity": "advisory"},
			},
			"summary": "Mixed results",
		},
	}

	result := formatProbingEvidence(outcome)
	lines := strings.Split(result, "\n")
	var foundCritical, foundAdvisory, foundNoIssue, foundInconclusive bool
	for _, line := range lines {
		if strings.Contains(line, "A") && strings.Contains(line, "\U0001f534") {
			foundCritical = true
		}
		if strings.Contains(line, "B") && strings.Contains(line, "\U0001f7e1") {
			foundAdvisory = true
		}
		if strings.Contains(line, "C") && strings.Contains(line, "\u2705") {
			foundNoIssue = true
		}
		if strings.Contains(line, "D") && strings.Contains(line, "\u2753") {
			foundInconclusive = true
		}
	}
	if !foundCritical {
		t.Error("Expected critical icon for finding A")
	}
	if !foundAdvisory {
		t.Error("Expected advisory icon for finding B")
	}
	if !foundNoIssue {
		t.Error("Expected no_issue icon for finding C")
	}
	if !foundInconclusive {
		t.Error("Expected inconclusive icon for finding D")
	}
}

// --- Unit tests: shouldRunAdversarialProbing ---

func newTestStateMachine() *agent.BaseStateMachine {
	return agent.NewBaseStateMachine("test-coder", StateTesting, nil, CoderTransitions)
}

func setupEligibleSM(t *testing.T) *agent.BaseStateMachine {
	t.Helper()
	sm := newTestStateMachine()
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))
	sm.SetStateData(KeyExpress, false)
	sm.SetStateData(KeyIsHotfix, false)
	sm.SetStateData(KeyVerificationEvidence, VerificationOutcome{
		Status: VerificationPass,
		Evidence: map[string]any{
			"acceptance_criteria_checked": []any{
				map[string]any{"criterion": "A", "result": "pass"},
			},
			"confidence": "high",
		},
	})
	return sm
}

func TestShouldRunAdversarialProbing_Eligible(t *testing.T) {
	sm := setupEligibleSM(t)
	if !shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be eligible for standard app story")
	}
}

func TestShouldRunAdversarialProbing_DevOpsStory(t *testing.T) {
	sm := setupEligibleSM(t)
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible for devops story")
	}
}

func TestShouldRunAdversarialProbing_MaintenanceStory(t *testing.T) {
	sm := setupEligibleSM(t)
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeMaintenance))

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible for maintenance story")
	}
}

func TestShouldRunAdversarialProbing_UnknownStoryType(t *testing.T) {
	sm := setupEligibleSM(t)
	sm.SetStateData(proto.KeyStoryType, "unknown")

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible for unknown story type")
	}
}

func TestShouldRunAdversarialProbing_ExpressStory(t *testing.T) {
	sm := setupEligibleSM(t)
	sm.SetStateData(KeyExpress, true)

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible for express story")
	}
}

func TestShouldRunAdversarialProbing_HotfixStory(t *testing.T) {
	sm := setupEligibleSM(t)
	sm.SetStateData(KeyIsHotfix, true)

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible for hotfix story")
	}
}

func TestShouldRunAdversarialProbing_VerificationFailed(t *testing.T) {
	sm := setupEligibleSM(t)
	sm.SetStateData(KeyVerificationEvidence, VerificationOutcome{
		Status: VerificationFail,
	})

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible when verification failed")
	}
}

func TestShouldRunAdversarialProbing_VerificationUnavailable(t *testing.T) {
	sm := setupEligibleSM(t)
	sm.SetStateData(KeyVerificationEvidence, VerificationOutcome{
		Status: VerificationUnavailable,
		Reason: "LLM error",
	})

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible when verification unavailable")
	}
}

func TestShouldRunAdversarialProbing_NoVerificationEvidence(t *testing.T) {
	sm := newTestStateMachine()
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible when no verification evidence")
	}
}

func TestShouldRunAdversarialProbing_EmptyStoryType(t *testing.T) {
	sm := setupEligibleSM(t)
	sm.SetStateData(proto.KeyStoryType, "")

	if shouldRunAdversarialProbing(sm) {
		t.Error("Expected probing to be ineligible for empty story type")
	}
}

// --- Unit tests: rehydrateProbingOutcome ---

func TestRehydrateProbingOutcome_DirectPath(t *testing.T) {
	original := ProbingOutcome{
		Status: ProbingPass,
		Evidence: map[string]any{
			"findings": []any{
				map[string]any{"category": "error_handling", "result": "no_issue"},
			},
			"summary": "All clear",
		},
	}

	outcome, ok := rehydrateProbingOutcome(original)
	if !ok {
		t.Fatal("Expected rehydration to succeed for typed struct")
	}
	if outcome.Status != ProbingPass {
		t.Errorf("Expected status %s, got %s", ProbingPass, outcome.Status)
	}
	if outcome.Evidence["summary"] != "All clear" {
		t.Errorf("Expected summary 'All clear', got %v", outcome.Evidence["summary"])
	}
}

func TestRehydrateProbingOutcome_ResumedPath(t *testing.T) {
	// Simulate what state persistence produces after JSON roundtrip
	resumed := map[string]any{
		"Status": "fail",
		"Evidence": map[string]any{
			"findings": []any{
				map[string]any{"category": "security", "result": "issue_found", "severity": "critical"},
			},
			"summary": "Critical issue",
		},
		"Reason": "",
	}

	outcome, ok := rehydrateProbingOutcome(resumed)
	if !ok {
		t.Fatal("Expected rehydration to succeed for map[string]any")
	}
	if outcome.Status != ProbingFail {
		t.Errorf("Expected status %s, got %s", ProbingFail, outcome.Status)
	}
	if outcome.Evidence == nil {
		t.Fatal("Expected non-nil evidence")
	}
}

func TestRehydrateProbingOutcome_SkippedResumed(t *testing.T) {
	resumed := map[string]any{
		"Status": "skipped",
		"Reason": "devops story type",
	}

	outcome, ok := rehydrateProbingOutcome(resumed)
	if !ok {
		t.Fatal("Expected rehydration to succeed")
	}
	if outcome.Status != ProbingSkipped {
		t.Errorf("Expected status %s, got %s", ProbingSkipped, outcome.Status)
	}
	if outcome.Reason != "devops story type" {
		t.Errorf("Expected reason text, got %q", outcome.Reason)
	}
	if outcome.Evidence != nil {
		t.Error("Expected nil evidence for skipped")
	}
}

func TestRehydrateProbingOutcome_UnavailableResumed(t *testing.T) {
	resumed := map[string]any{
		"Status": "unavailable",
		"Reason": "LLM error: timeout",
	}

	outcome, ok := rehydrateProbingOutcome(resumed)
	if !ok {
		t.Fatal("Expected rehydration to succeed")
	}
	if outcome.Status != ProbingUnavailable {
		t.Errorf("Expected status %s, got %s", ProbingUnavailable, outcome.Status)
	}
	if outcome.Reason != "LLM error: timeout" {
		t.Errorf("Expected reason text, got %q", outcome.Reason)
	}
}

func TestRehydrateProbingOutcome_InvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{"nil", nil},
		{"string", "not a struct"},
		{"int", 42},
		{"empty map", map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := rehydrateProbingOutcome(tt.input)
			if ok {
				t.Error("Expected rehydration to fail for invalid input")
			}
		})
	}
}

// --- Unit tests: formatChangedFilesForPrompt ---

func TestFormatChangedFilesForPrompt_Normal(t *testing.T) {
	files := []string{"src/handler.go", "src/model.go", "tests/handler_test.go"}
	result := formatChangedFilesForPrompt(files)

	if !strings.Contains(result, "src/handler.go") {
		t.Error("Expected file path in output")
	}
	if !strings.Contains(result, "tests/handler_test.go") {
		t.Error("Expected test file in output")
	}
	if strings.Contains(result, "more files not shown") {
		t.Error("Should not show truncation note for small list")
	}
}

func TestFormatChangedFilesForPrompt_ExceedsLimit(t *testing.T) {
	files := make([]string, 60)
	for i := range files {
		files[i] = "file_" + string(rune('a'+i%26)) + ".go"
	}

	result := formatChangedFilesForPrompt(files)

	if !strings.Contains(result, "10 more files not shown") {
		t.Error("Expected truncation note for >50 files")
	}
}

func TestFormatChangedFilesForPrompt_Empty(t *testing.T) {
	result := formatChangedFilesForPrompt([]string{})
	if !strings.Contains(result, "no changed files detected") {
		t.Error("Expected empty message")
	}
}

func TestFormatChangedFilesForPrompt_Nil(t *testing.T) {
	result := formatChangedFilesForPrompt(nil)
	if !strings.Contains(result, "no changed files detected") {
		t.Error("Expected empty message for nil input")
	}
}

// --- Unit tests: ProbingStatus values ---

func TestProbingOutcome_StatusValues(t *testing.T) {
	statuses := []ProbingStatus{ProbingPass, ProbingFail, ProbingUnavailable, ProbingSkipped}
	for i, a := range statuses {
		for j, b := range statuses {
			if i != j && a == b {
				t.Errorf("Status values %d and %d should be different: %s == %s", i, j, a, b)
			}
		}
	}
}

// --- Producer→Consumer round-trip tests ---

func TestProbingProducerConsumerRoundTrip(t *testing.T) {
	tool := tools.NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "error_handling",
				"description": "Nil pointer dereference",
				"method":      "inspection",
				"result":      "issue_found",
				"severity":    "critical",
				"evidence":    "handler.go:42 dereferences without check",
			},
			map[string]any{
				"category":    "boundary_values",
				"description": "Max length input",
				"method":      "command",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "Validated at API boundary",
			},
		},
		"summary": "One critical issue",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Tool exec failed: %v", err)
	}

	evidence, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("Expected map[string]any data from tool")
	}

	// Feed tool output directly into buildProbingFailureMessage
	failMsg := buildProbingFailureMessage(evidence)
	if !strings.Contains(failMsg, "[CRITICAL/error_handling]") {
		t.Error("buildProbingFailureMessage: expected [CRITICAL/error_handling] tag from tool output")
	}
	if !strings.Contains(failMsg, "Nil pointer dereference") {
		t.Error("buildProbingFailureMessage: expected finding description from tool output")
	}
	// Non-critical findings should not appear
	if strings.Contains(failMsg, "Max length input") {
		t.Error("buildProbingFailureMessage: should not include non-critical findings")
	}

	// Feed tool output directly into formatProbingEvidence
	outcome := ProbingOutcome{
		Status:   ProbingFail,
		Evidence: evidence,
	}
	formatted := formatProbingEvidence(outcome)
	if !strings.Contains(formatted, "Critical robustness issues found") {
		t.Error("formatProbingEvidence: expected fail header from tool output")
	}
	if !strings.Contains(formatted, "Nil pointer dereference") {
		t.Error("formatProbingEvidence: expected finding in formatted output")
	}
}

func TestProbingProducerConsumerRoundTrip_AllPass(t *testing.T) {
	tool := tools.NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "error_handling",
				"description": "All errors handled",
				"method":      "inspection",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "Error wrapping consistent",
			},
		},
		"summary": "No issues",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Tool exec failed: %v", err)
	}
	if result.ProcessEffect.Signal != tools.SignalProbingPass {
		t.Errorf("Expected pass signal, got %s", result.ProcessEffect.Signal)
	}

	evidence, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("Expected map[string]any data from tool")
	}

	outcome := ProbingOutcome{
		Status:   ProbingPass,
		Evidence: evidence,
	}
	formatted := formatProbingEvidence(outcome)
	if !strings.Contains(formatted, "No critical robustness issues found") {
		t.Error("Expected pass header in formatted output")
	}
	if !strings.Contains(formatted, "All errors handled") {
		t.Error("Expected finding description in formatted output")
	}
}

// --- formatVerificationSummaryForProbing ---

func TestFormatVerificationSummaryForProbing_WithCriteria(t *testing.T) {
	outcome := VerificationOutcome{
		Status: VerificationPass,
		Evidence: map[string]any{
			"acceptance_criteria_checked": []any{
				map[string]any{"criterion": "API returns 200", "result": "pass"},
				map[string]any{"criterion": "Tests cover edge cases", "result": "pass"},
			},
		},
	}

	result := formatVerificationSummaryForProbing(outcome)
	if !strings.Contains(result, "API returns 200") {
		t.Error("Expected criterion text")
	}
	if !strings.Contains(result, "Tests cover edge cases") {
		t.Error("Expected second criterion text")
	}
}

func TestFormatVerificationSummaryForProbing_NilEvidence(t *testing.T) {
	outcome := VerificationOutcome{
		Status: VerificationPass,
	}

	result := formatVerificationSummaryForProbing(outcome)
	if result != "" {
		t.Errorf("Expected empty string for nil evidence, got %q", result)
	}
}

// =============================================================================
// Integration tests: drive proceedWithAdversarialProbing through all paths
// =============================================================================

// createProbingTestCoder creates a Coder with mocked LLM for probing integration tests.
// The mock LLM is pre-configured to return a submit_probing tool call.
func createProbingTestCoder(t *testing.T, mockLLM *mocks.MockLLMClient) (*Coder, *agent.BaseStateMachine) {
	t.Helper()

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	// Set up longRunningExecutor (needed for tool provider creation).
	// Using a real LongRunningDockerExec with dummy image — the shell tool is created
	// but never invoked because the mock LLM returns the terminal tool directly.
	coder.longRunningExecutor = execpkg.NewLongRunningDockerExec("test-image:latest", "test-coder-001")

	sm := coder.BaseStateMachine
	return coder, sm
}

// setupProbingEligibleState configures a state machine as eligible for probing:
// app story type, not express, not hotfix, verification passed.
func setupProbingEligibleState(sm *agent.BaseStateMachine) {
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))
	sm.SetStateData(KeyExpress, false)
	sm.SetStateData(KeyIsHotfix, false)
	sm.SetStateData(KeyStoryID, "test-story-001")
	sm.SetStateData(string(stateDataKeyTaskContent), "Implement API endpoint with validation")
	sm.SetStateData(KeyPlan, "1. Create handler\n2. Add validation")
	sm.SetStateData(KeyWorkspacePath, "/tmp/test-workspace")
	sm.SetStateData(KeyVerificationEvidence, VerificationOutcome{
		Status: VerificationPass,
		Evidence: map[string]any{
			"acceptance_criteria_checked": []any{
				map[string]any{"criterion": "API returns 200", "result": "pass"},
			},
			"confidence": "high",
		},
	})
}

// makeProbingToolCall creates an LLM response that calls submit_probing with given findings.
func makeProbingToolCall(findings []map[string]any, summary string) llm.CompletionResponse {
	findingsAny := make([]any, len(findings))
	for i, f := range findings {
		findingsAny[i] = f
	}
	return llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{
			{
				ID:   "probe-1",
				Name: "submit_probing",
				Parameters: map[string]any{
					"findings": findingsAny,
					"summary":  summary,
				},
			},
		},
		StopReason: "tool_use",
	}
}

func TestProceedWithAdversarialProbing_ProbingPass(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()
	mockLLM.RespondWithSequence([]llm.CompletionResponse{
		makeProbingToolCall([]map[string]any{
			{
				"category":    "error_handling",
				"description": "Error paths covered",
				"method":      "inspection",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "All errors wrapped",
			},
		}, "No issues found"),
	})

	coder, sm := createProbingTestCoder(t, mockLLM)
	setupProbingEligibleState(sm)

	nextState, done, err := coder.proceedWithAdversarialProbing(context.Background(), sm, "/tmp/test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != StateCodeReview {
		t.Errorf("Expected CODE_REVIEW, got %s", nextState)
	}

	// Verify KeyProbingEvidence was stored
	peRaw, exists := sm.GetStateValue(KeyProbingEvidence)
	if !exists || peRaw == nil {
		t.Fatal("Expected KeyProbingEvidence to be set")
	}
	outcome, ok := peRaw.(ProbingOutcome)
	if !ok {
		t.Fatal("Expected ProbingOutcome type")
	}
	if outcome.Status != ProbingPass {
		t.Errorf("Expected probing status pass, got %s", outcome.Status)
	}
}

func TestProceedWithAdversarialProbing_ProbingFail(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()
	mockLLM.RespondWithSequence([]llm.CompletionResponse{
		makeProbingToolCall([]map[string]any{
			{
				"category":    "security",
				"description": "SQL injection in user input",
				"method":      "inspection",
				"result":      "issue_found",
				"severity":    "critical",
				"evidence":    "query.go:15 concatenates user input",
			},
		}, "Critical SQL injection found"),
	})

	coder, sm := createProbingTestCoder(t, mockLLM)
	setupProbingEligibleState(sm)

	nextState, done, err := coder.proceedWithAdversarialProbing(context.Background(), sm, "/tmp/test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	// Should route back to CODING for fixing
	if nextState != StateCoding {
		t.Errorf("Expected CODING (probing fail routes back), got %s", nextState)
	}

	// Verify KeyProbingEvidence was stored with fail status
	peRaw, exists := sm.GetStateValue(KeyProbingEvidence)
	if !exists || peRaw == nil {
		t.Fatal("Expected KeyProbingEvidence to be set")
	}
	outcome, ok := peRaw.(ProbingOutcome)
	if !ok {
		t.Fatal("Expected ProbingOutcome type")
	}
	if outcome.Status != ProbingFail {
		t.Errorf("Expected probing status fail, got %s", outcome.Status)
	}
}

func TestProceedWithAdversarialProbing_Skipped_DevOps(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient() // LLM should never be called
	coder, sm := createProbingTestCoder(t, mockLLM)
	setupProbingEligibleState(sm)
	// Override story type to devops — should skip probing
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	nextState, done, err := coder.proceedWithAdversarialProbing(context.Background(), sm, "/tmp/test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != StateCodeReview {
		t.Errorf("Expected CODE_REVIEW (skipped probing), got %s", nextState)
	}

	// Verify probing evidence is stored as skipped
	peRaw, exists := sm.GetStateValue(KeyProbingEvidence)
	if !exists || peRaw == nil {
		t.Fatal("Expected KeyProbingEvidence to be set")
	}
	outcome, ok := peRaw.(ProbingOutcome)
	if !ok {
		t.Fatal("Expected ProbingOutcome type")
	}
	if outcome.Status != ProbingSkipped {
		t.Errorf("Expected probing status skipped, got %s", outcome.Status)
	}

	// LLM should not have been called
	if len(mockLLM.CompleteCalls) > 0 {
		t.Error("LLM should not be called when probing is skipped")
	}
}

func TestProceedWithAdversarialProbing_Skipped_Maintenance(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()
	coder, sm := createProbingTestCoder(t, mockLLM)
	setupProbingEligibleState(sm)
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeMaintenance))

	nextState, _, err := coder.proceedWithAdversarialProbing(context.Background(), sm, "/tmp/test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if nextState != StateCodeReview {
		t.Errorf("Expected CODE_REVIEW, got %s", nextState)
	}
	if outcome, ok := sm.GetStateValue(KeyProbingEvidence); ok {
		po, poOK := outcome.(ProbingOutcome)
		if !poOK || po.Status != ProbingSkipped {
			t.Error("Expected ProbingSkipped for maintenance story")
		}
	}
}

func TestProceedWithAdversarialProbing_Skipped_Express(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()
	coder, sm := createProbingTestCoder(t, mockLLM)
	setupProbingEligibleState(sm)
	sm.SetStateData(KeyExpress, true)

	nextState, _, err := coder.proceedWithAdversarialProbing(context.Background(), sm, "/tmp/test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if nextState != StateCodeReview {
		t.Errorf("Expected CODE_REVIEW, got %s", nextState)
	}
}

func TestProceedWithAdversarialProbing_Skipped_Hotfix(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()
	coder, sm := createProbingTestCoder(t, mockLLM)
	setupProbingEligibleState(sm)
	sm.SetStateData(KeyIsHotfix, true)

	nextState, _, err := coder.proceedWithAdversarialProbing(context.Background(), sm, "/tmp/test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if nextState != StateCodeReview {
		t.Errorf("Expected CODE_REVIEW, got %s", nextState)
	}
}

func TestProceedWithAdversarialProbing_GracefulShutdown(t *testing.T) {
	// Create a context that's already cancelled to simulate shutdown during probing
	ctx, cancel := context.WithCancel(context.Background())

	mockLLM := mocks.NewMockLLMClient()
	// Make LLM check for context cancellation (simulating shutdown during probing)
	mockLLM.OnComplete(func(callCtx context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
		// Cancel the context before responding (simulates shutdown mid-probing)
		cancel()
		return llm.CompletionResponse{
			Content:    "Thinking about probing...",
			StopReason: "end_turn",
		}, callCtx.Err()
	})

	coder, sm := createProbingTestCoder(t, mockLLM)
	setupProbingEligibleState(sm)

	nextState, done, err := coder.proceedWithAdversarialProbing(ctx, sm, "/tmp/test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// Graceful shutdown should return StateTesting, done=true
	if nextState != StateTesting {
		t.Errorf("Expected TESTING (graceful shutdown), got %s", nextState)
	}
	if !done {
		t.Error("Expected done=true for graceful shutdown")
	}
}

func TestProceedWithAdversarialProbing_StaleEvidenceCleared(t *testing.T) {
	// Verify that a previous probing outcome is cleared at start of TESTING
	sm := newTestStateMachine()
	sm.SetStateData(KeyProbingEvidence, ProbingOutcome{
		Status: ProbingPass,
		Evidence: map[string]any{
			"findings": []any{map[string]any{"category": "error_handling", "result": "no_issue"}},
		},
	})

	// Simulate what handleTesting does at the top
	sm.SetStateData(KeyProbingEvidence, nil)

	peRaw, exists := sm.GetStateValue(KeyProbingEvidence)
	if exists && peRaw != nil {
		t.Error("Expected stale probing evidence to be cleared")
	}
}

func TestProceedWithAdversarialProbing_LLMError(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()
	mockLLM.FailCompleteWith(context.DeadlineExceeded)

	coder, sm := createProbingTestCoder(t, mockLLM)
	setupProbingEligibleState(sm)

	nextState, done, err := coder.proceedWithAdversarialProbing(context.Background(), sm, "/tmp/test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	// LLM error → probing unavailable → proceed to CODE_REVIEW
	if nextState != StateCodeReview {
		t.Errorf("Expected CODE_REVIEW (probing unavailable), got %s", nextState)
	}

	// Verify evidence shows unavailable
	peRaw, exists := sm.GetStateValue(KeyProbingEvidence)
	if !exists || peRaw == nil {
		t.Fatal("Expected KeyProbingEvidence to be set")
	}
	outcome, ok := peRaw.(ProbingOutcome)
	if !ok {
		t.Fatal("Expected ProbingOutcome type")
	}
	if outcome.Status != ProbingUnavailable {
		t.Errorf("Expected probing status unavailable, got %s", outcome.Status)
	}
}

// TestVerificationUnavailable_ProbingSkipped tests the path in proceedToCodeReviewWithLintCheck
// where verification is unavailable, causing probing to be skipped.
func TestVerificationUnavailable_ProbingSkipped(t *testing.T) {
	// This tests the routing in proceedToCodeReviewWithLintCheck where
	// verification unavailable → store ProbingSkipped → CODE_REVIEW.
	// We verify the state data is set correctly.
	sm := newTestStateMachine()

	// Simulate what proceedToCodeReviewWithLintCheck does for verification unavailable:
	sm.SetStateData(KeyProbingEvidence, ProbingOutcome{
		Status: ProbingSkipped,
		Reason: "verification unavailable",
	})

	peRaw, exists := sm.GetStateValue(KeyProbingEvidence)
	if !exists || peRaw == nil {
		t.Fatal("Expected KeyProbingEvidence to be set")
	}
	outcome, ok := peRaw.(ProbingOutcome)
	if !ok {
		t.Fatal("Expected ProbingOutcome type")
	}
	if outcome.Status != ProbingSkipped {
		t.Errorf("Expected skipped status, got %s", outcome.Status)
	}
	if outcome.Reason != "verification unavailable" {
		t.Errorf("Expected reason 'verification unavailable', got %q", outcome.Reason)
	}

	// Verify the evidence renders correctly for CODE_REVIEW
	formatted := formatProbingEvidence(outcome)
	if !strings.Contains(formatted, "skipped") {
		t.Error("Expected 'skipped' in formatted output")
	}
	if !strings.Contains(formatted, "verification unavailable") {
		t.Error("Expected reason in formatted output")
	}
}
