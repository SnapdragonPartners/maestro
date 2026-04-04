package proto

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewFailureInfo(t *testing.T) {
	fi := NewFailureInfo(FailureKindExternal, "git corrupt", "CODING", "done")

	if fi.Kind != FailureKindExternal {
		t.Errorf("expected kind %q, got %q", FailureKindExternal, fi.Kind)
	}
	if fi.Explanation != "git corrupt" {
		t.Errorf("unexpected explanation: %s", fi.Explanation)
	}
	if fi.FailedState != "CODING" {
		t.Errorf("expected failed state CODING, got %q", fi.FailedState)
	}
	if fi.ToolName != "done" {
		t.Errorf("expected tool name done, got %q", fi.ToolName)
	}
}

func TestFailureKindConstants(t *testing.T) {
	// Verify the string values are stable (used in JSON, templates, etc.)
	if string(FailureKindTransient) != "transient" {
		t.Errorf("FailureKindTransient = %q", FailureKindTransient)
	}
	if string(FailureKindStoryInvalid) != "story_invalid" {
		t.Errorf("FailureKindStoryInvalid = %q", FailureKindStoryInvalid)
	}
	if string(FailureKindExternal) != "external" {
		t.Errorf("FailureKindExternal = %q", FailureKindExternal)
	}
}

func TestKeyFailureInfo(t *testing.T) {
	if KeyFailureInfo != "failure_info" {
		t.Errorf("KeyFailureInfo = %q", KeyFailureInfo)
	}
}

func TestNewFailureInfoV2(t *testing.T) {
	before := time.Now().UTC()
	fi := NewFailureInfoV2(
		FailureKindExternal,
		"git corruption",
		"CODING",
		"done",
		FailureSourceAutoClassifier,
		FailureScopeAttempt,
		"story-123",
		"spec-456",
		"session-789",
		2,
	)
	after := time.Now().UTC()

	if fi.ID == "" {
		t.Error("expected non-empty ID")
	}
	if fi.Kind != FailureKindExternal {
		t.Errorf("expected kind %q, got %q", FailureKindExternal, fi.Kind)
	}
	if fi.Source != FailureSourceAutoClassifier {
		t.Errorf("expected source %q, got %q", FailureSourceAutoClassifier, fi.Source)
	}
	if fi.ScopeGuess != FailureScopeAttempt {
		t.Errorf("expected scope_guess %q, got %q", FailureScopeAttempt, fi.ScopeGuess)
	}
	if fi.StoryID != "story-123" {
		t.Errorf("expected story_id %q, got %q", "story-123", fi.StoryID)
	}
	if fi.AttemptNumber != 2 {
		t.Errorf("expected attempt_number 2, got %d", fi.AttemptNumber)
	}
	if fi.ResolutionStatus != FailureResolutionPending {
		t.Errorf("expected resolution_status %q, got %q", FailureResolutionPending, fi.ResolutionStatus)
	}
	if fi.CreatedAt.Before(before) || fi.CreatedAt.After(after) {
		t.Errorf("created_at %v not between %v and %v", fi.CreatedAt, before, after)
	}
}

func TestFailureInfo_JSONRoundTrip(t *testing.T) {
	fi := NewFailureInfoV2(
		FailureKindStoryInvalid, "contradictory requirements", "PLANNING", "",
		FailureSourceLLMReport, FailureScopeStory,
		"story-abc", "spec-def", "session-ghi", 1,
	)
	fi.Evidence = []FailureEvidence{
		{Kind: "tool_output", Summary: "build failed", Snippet: "error: undefined reference"},
	}
	fi.Tags = []string{"build", "linking"}
	fi.SetTriage(FailureKindStoryInvalid, FailureScopeStory, false, []string{"story-abc"}, "story-level issue")
	fi.SetResolution(FailureOwnerArchitect, FailureActionRewriteStory, FailureResolutionRunning)

	data, err := json.Marshal(fi)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded FailureInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.ID != fi.ID {
		t.Errorf("ID mismatch: %q != %q", decoded.ID, fi.ID)
	}
	if decoded.Kind != fi.Kind {
		t.Errorf("Kind mismatch")
	}
	if decoded.Source != fi.Source {
		t.Errorf("Source mismatch")
	}
	if decoded.ResolvedScope != fi.ResolvedScope {
		t.Errorf("ResolvedScope mismatch")
	}
	if decoded.Owner != fi.Owner {
		t.Errorf("Owner mismatch")
	}
	if decoded.Action != fi.Action {
		t.Errorf("Action mismatch")
	}
	if decoded.ResolutionStatus != fi.ResolutionStatus {
		t.Errorf("ResolutionStatus mismatch")
	}
	if len(decoded.Evidence) != 1 || decoded.Evidence[0].Kind != "tool_output" {
		t.Errorf("Evidence mismatch: %v", decoded.Evidence)
	}
	if len(decoded.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(decoded.Tags))
	}
	if len(decoded.AffectedStoryIDs) != 1 || decoded.AffectedStoryIDs[0] != "story-abc" {
		t.Errorf("AffectedStoryIDs mismatch: %v", decoded.AffectedStoryIDs)
	}
}

func TestGenerateFailureID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for range 100 {
		id := GenerateFailureID()
		if ids[id] {
			t.Errorf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestFailureInfo_SetResolution(t *testing.T) {
	fi := NewFailureInfo(FailureKindExternal, "broken", "CODING", "")
	fi.SetResolution(FailureOwnerOrchestrator, FailureActionRepairEnvironment, FailureResolutionRunning)

	if fi.Owner != FailureOwnerOrchestrator {
		t.Errorf("expected owner %q, got %q", FailureOwnerOrchestrator, fi.Owner)
	}
	if fi.Action != FailureActionRepairEnvironment {
		t.Errorf("expected action %q, got %q", FailureActionRepairEnvironment, fi.Action)
	}
	if fi.ResolutionStatus != FailureResolutionRunning {
		t.Errorf("expected status %q, got %q", FailureResolutionRunning, fi.ResolutionStatus)
	}
}

func TestFailureInfo_SetTriage(t *testing.T) {
	fi := NewFailureInfo(FailureKindStoryInvalid, "ambiguous", "PLANNING", "")
	fi.SetTriage(FailureKindStoryInvalid, FailureScopeEpoch, true, []string{"s1", "s2"}, "affects multiple stories")

	if fi.ResolvedScope != FailureScopeEpoch {
		t.Errorf("expected resolved_scope %q, got %q", FailureScopeEpoch, fi.ResolvedScope)
	}
	if !fi.HumanNeeded {
		t.Error("expected human_needed true")
	}
	if len(fi.AffectedStoryIDs) != 2 {
		t.Errorf("expected 2 affected stories, got %d", len(fi.AffectedStoryIDs))
	}
}

func TestFailureKindV2Constants(t *testing.T) {
	if string(FailureKindEnvironment) != "environment" {
		t.Errorf("FailureKindEnvironment = %q", FailureKindEnvironment)
	}
	if string(FailureKindPrerequisite) != "prerequisite" {
		t.Errorf("FailureKindPrerequisite = %q", FailureKindPrerequisite)
	}
}

func TestComputeSignature_Stability(t *testing.T) {
	fi1 := NewFailureInfo(FailureKindEnvironment, "no space left on device at /workspace/coder-001/src", "TESTING", "shell")
	fi2 := NewFailureInfo(FailureKindEnvironment, "no space left on device at /workspace/coder-002/build", "TESTING", "shell")

	sig1 := fi1.ComputeSignature()
	sig2 := fi2.ComputeSignature()

	if sig1 != sig2 {
		t.Errorf("same failure family should produce same signature: %s vs %s", sig1, sig2)
	}
	if len(sig1) != 64 {
		t.Errorf("signature should be 64 hex chars, got %d", len(sig1))
	}
}

func TestComputeSignature_DifferentKinds(t *testing.T) {
	fi1 := NewFailureInfo(FailureKindEnvironment, "connection refused", "CODING", "shell")
	fi2 := NewFailureInfo(FailureKindPrerequisite, "connection refused", "CODING", "shell")

	if fi1.ComputeSignature() == fi2.ComputeSignature() {
		t.Error("different kinds should produce different signatures")
	}
}

func TestComputeSignature_NormalizesVariableDetails(t *testing.T) {
	fi1 := NewFailureInfo(FailureKindEnvironment,
		"build failed at 2026-04-03T10:30:00Z with hash abc1234def in /tmp/workspace/foo.go:42",
		"TESTING", "shell")
	fi2 := NewFailureInfo(FailureKindEnvironment,
		"build failed at 2026-05-15T22:00:00Z with hash 9876543abc in /opt/build/bar.go:99",
		"TESTING", "shell")

	if fi1.ComputeSignature() != fi2.ComputeSignature() {
		t.Error("same failure pattern with different variable details should produce same signature")
	}
}

func TestSanitize_CapsEvidence(t *testing.T) {
	fi := NewFailureInfo(FailureKindEnvironment, "test failure", "TESTING", "")

	// Add more than MaxEvidenceEntries
	for i := 0; i < 15; i++ {
		fi.Evidence = append(fi.Evidence, FailureEvidence{
			Kind:    "test",
			Summary: "summary",
			Snippet: "snippet",
		})
	}

	fi.Sanitize(func(s string, maxLen int) string {
		if maxLen > 0 && len(s) > maxLen {
			return s[:maxLen]
		}
		return s
	})

	if len(fi.Evidence) != MaxEvidenceEntries {
		t.Errorf("expected %d evidence entries, got %d", MaxEvidenceEntries, len(fi.Evidence))
	}
	if fi.Signature == "" {
		t.Error("Sanitize should compute signature")
	}
}
