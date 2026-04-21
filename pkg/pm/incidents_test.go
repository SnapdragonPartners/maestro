package pm

import (
	"strings"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// createIncidentTestDriver creates a minimal PM driver with openIncidents initialized.
func createIncidentTestDriver() *Driver {
	sm := agent.NewBaseStateMachine("pm-test", StateWorking, nil, validTransitions)
	return &Driver{
		BaseStateMachine: sm,
		contextManager:   contextmgr.NewContextManager(),
		logger:           logx.NewLogger("pm-test"),
		workDir:          "/tmp/test-pm",
		openIncidents:    make(map[string]*proto.Incident),
	}
}

func TestBuildPendingSummary_NothingPending(t *testing.T) {
	d := createIncidentTestDriver()

	summary := d.buildPendingSummary()
	if summary != "" {
		t.Errorf("expected empty string for no pending items, got %q", summary)
	}
}

func TestBuildPendingSummary_IncidentOnly(t *testing.T) {
	d := createIncidentTestDriver()
	d.openIncidents["inc-001"] = &proto.Incident{
		ID:             "inc-001",
		Title:          "Build environment corrupted",
		Summary:        "Docker image missing gcc",
		AllowedActions: []proto.IncidentAction{proto.IncidentActionTryAgain},
	}

	summary := d.buildPendingSummary()
	if summary == "" {
		t.Fatal("expected non-empty summary when incident is present")
	}
	if !strings.Contains(summary, "inc-001") {
		t.Errorf("summary should contain incident ID, got: %s", summary)
	}
	if !strings.Contains(summary, "Build environment corrupted") {
		t.Errorf("summary should contain incident title, got: %s", summary)
	}
	if !strings.Contains(summary, "Docker image missing gcc") {
		t.Errorf("summary should contain incident summary, got: %s", summary)
	}
	if !strings.Contains(summary, "INCIDENT") {
		t.Errorf("summary should contain INCIDENT label, got: %s", summary)
	}
	// Should not contain UNANSWERED QUESTION since no ask is set
	if strings.Contains(summary, "UNANSWERED QUESTION") {
		t.Errorf("summary should not contain UNANSWERED QUESTION when no ask is set, got: %s", summary)
	}
}

func TestBuildPendingSummary_AskOnly(t *testing.T) {
	d := createIncidentTestDriver()
	d.currentAsk = &proto.UserAsk{
		ID:       "ask-001",
		Prompt:   "What database engine should we use?",
		Kind:     "decision_required",
		OpenedAt: "2026-04-19T10:00:00Z",
	}

	summary := d.buildPendingSummary()
	if summary == "" {
		t.Fatal("expected non-empty summary when ask is present")
	}
	if !strings.Contains(summary, "ask-001") {
		t.Errorf("summary should contain ask ID, got: %s", summary)
	}
	if !strings.Contains(summary, "What database engine should we use?") {
		t.Errorf("summary should contain ask prompt, got: %s", summary)
	}
	if !strings.Contains(summary, "UNANSWERED QUESTION") {
		t.Errorf("summary should contain UNANSWERED QUESTION label, got: %s", summary)
	}
	// Should not contain INCIDENT since no incidents are open
	if strings.Contains(summary, "INCIDENT") {
		t.Errorf("summary should not contain INCIDENT when no incidents are open, got: %s", summary)
	}
}

func TestBuildPendingSummary_BothAskAndIncident(t *testing.T) {
	d := createIncidentTestDriver()
	d.openIncidents["inc-001"] = &proto.Incident{
		ID:             "inc-001",
		Title:          "Story blocked",
		Summary:        "Dependency unavailable",
		AllowedActions: []proto.IncidentAction{proto.IncidentActionSkip, proto.IncidentActionResume},
	}
	d.currentAsk = &proto.UserAsk{
		ID:       "ask-002",
		Prompt:   "Should we proceed without the dependency?",
		Kind:     "clarification",
		OpenedAt: "2026-04-19T10:05:00Z",
	}

	summary := d.buildPendingSummary()
	if summary == "" {
		t.Fatal("expected non-empty summary when both ask and incident are present")
	}
	if !strings.Contains(summary, "INCIDENT") {
		t.Errorf("summary should contain INCIDENT, got: %s", summary)
	}
	if !strings.Contains(summary, "UNANSWERED QUESTION") {
		t.Errorf("summary should contain UNANSWERED QUESTION, got: %s", summary)
	}
	if !strings.Contains(summary, "inc-001") {
		t.Errorf("summary should contain incident ID, got: %s", summary)
	}
	if !strings.Contains(summary, "ask-002") {
		t.Errorf("summary should contain ask ID, got: %s", summary)
	}
	if !strings.Contains(summary, "Pending items requiring attention") {
		t.Errorf("summary should contain header text, got: %s", summary)
	}
}

func TestBuildPendingSummary_DeterministicOrder(t *testing.T) {
	d := createIncidentTestDriver()
	d.openIncidents["inc-zzz"] = &proto.Incident{
		ID: "inc-zzz", Title: "Zulu incident", Summary: "last alphabetically",
		AllowedActions: []proto.IncidentAction{proto.IncidentActionResume},
	}
	d.openIncidents["inc-aaa"] = &proto.Incident{
		ID: "inc-aaa", Title: "Alpha incident", Summary: "first alphabetically",
		AllowedActions: []proto.IncidentAction{proto.IncidentActionTryAgain},
	}
	d.openIncidents["inc-mmm"] = &proto.Incident{
		ID: "inc-mmm", Title: "Middle incident", Summary: "middle alphabetically",
		AllowedActions: []proto.IncidentAction{proto.IncidentActionSkip},
	}

	first := d.buildPendingSummary()
	for i := 0; i < 20; i++ {
		if got := d.buildPendingSummary(); got != first {
			t.Fatalf("buildPendingSummary is nondeterministic: iteration %d differs", i)
		}
	}

	aaaIdx := strings.Index(first, "inc-aaa")
	mmmIdx := strings.Index(first, "inc-mmm")
	zzzIdx := strings.Index(first, "inc-zzz")
	if aaaIdx > mmmIdx || mmmIdx > zzzIdx {
		t.Errorf("incidents should appear in sorted order: aaa@%d, mmm@%d, zzz@%d", aaaIdx, mmmIdx, zzzIdx)
	}
}

func TestSha256Hex_Determinism(t *testing.T) {
	input := "test input for hashing"

	hash1 := sha256Hex(input)
	hash2 := sha256Hex(input)

	if hash1 != hash2 {
		t.Errorf("sha256Hex is not deterministic: %q != %q", hash1, hash2)
	}

	// Verify it's a valid hex string of expected length (SHA-256 = 64 hex chars)
	if len(hash1) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(hash1))
	}

	// Different input should produce different output
	hash3 := sha256Hex("different input")
	if hash1 == hash3 {
		t.Error("different inputs should produce different hashes")
	}
}
