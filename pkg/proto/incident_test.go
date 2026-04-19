package proto

import (
	"testing"
)

func TestIncidentOpenedPayload_RoundTrip(t *testing.T) {
	original := &Incident{
		ID:               "inc-001",
		Kind:             IncidentKindStoryBlocked,
		Scope:            "story",
		StoryID:          "story-42",
		SpecID:           "spec-7",
		FailureID:        "fail-abc",
		Title:            "Build environment corrupted",
		Summary:          "Docker image missing required build tools after update",
		Details:          "gcc not found in PATH; apt cache stale",
		AffectedStoryIDs: []string{"story-42", "story-43"},
		AllowedActions:   []IncidentAction{IncidentActionTryAgain, IncidentActionChangeRequest},
		Blocking:         true,
		OpenedAt:         "2026-04-19T10:00:00Z",
	}

	payload := NewIncidentOpenedPayload(original)
	if payload.Kind != PayloadKindIncidentOpened {
		t.Errorf("expected kind %q, got %q", PayloadKindIncidentOpened, payload.Kind)
	}

	extracted, err := payload.ExtractIncidentOpened()
	if err != nil {
		t.Fatalf("ExtractIncidentOpened failed: %v", err)
	}

	if extracted.ID != original.ID {
		t.Errorf("ID: got %q, want %q", extracted.ID, original.ID)
	}
	if extracted.Kind != original.Kind {
		t.Errorf("Kind: got %q, want %q", extracted.Kind, original.Kind)
	}
	if extracted.Scope != original.Scope {
		t.Errorf("Scope: got %q, want %q", extracted.Scope, original.Scope)
	}
	if extracted.StoryID != original.StoryID {
		t.Errorf("StoryID: got %q, want %q", extracted.StoryID, original.StoryID)
	}
	if extracted.SpecID != original.SpecID {
		t.Errorf("SpecID: got %q, want %q", extracted.SpecID, original.SpecID)
	}
	if extracted.FailureID != original.FailureID {
		t.Errorf("FailureID: got %q, want %q", extracted.FailureID, original.FailureID)
	}
	if extracted.Title != original.Title {
		t.Errorf("Title: got %q, want %q", extracted.Title, original.Title)
	}
	if extracted.Summary != original.Summary {
		t.Errorf("Summary: got %q, want %q", extracted.Summary, original.Summary)
	}
	if extracted.Details != original.Details {
		t.Errorf("Details: got %q, want %q", extracted.Details, original.Details)
	}
	if len(extracted.AffectedStoryIDs) != len(original.AffectedStoryIDs) {
		t.Fatalf("AffectedStoryIDs length: got %d, want %d", len(extracted.AffectedStoryIDs), len(original.AffectedStoryIDs))
	}
	for i, id := range extracted.AffectedStoryIDs {
		if id != original.AffectedStoryIDs[i] {
			t.Errorf("AffectedStoryIDs[%d]: got %q, want %q", i, id, original.AffectedStoryIDs[i])
		}
	}
	if len(extracted.AllowedActions) != len(original.AllowedActions) {
		t.Fatalf("AllowedActions length: got %d, want %d", len(extracted.AllowedActions), len(original.AllowedActions))
	}
	for i, action := range extracted.AllowedActions {
		if action != original.AllowedActions[i] {
			t.Errorf("AllowedActions[%d]: got %q, want %q", i, action, original.AllowedActions[i])
		}
	}
	if extracted.Blocking != original.Blocking {
		t.Errorf("Blocking: got %v, want %v", extracted.Blocking, original.Blocking)
	}
	if extracted.OpenedAt != original.OpenedAt {
		t.Errorf("OpenedAt: got %q, want %q", extracted.OpenedAt, original.OpenedAt)
	}
	if extracted.ResolvedAt != original.ResolvedAt {
		t.Errorf("ResolvedAt: got %q, want %q", extracted.ResolvedAt, original.ResolvedAt)
	}
	if extracted.Resolution != original.Resolution {
		t.Errorf("Resolution: got %q, want %q", extracted.Resolution, original.Resolution)
	}
}

func TestIncidentResolvedPayload_RoundTrip(t *testing.T) {
	original := &IncidentResolvedPayload{
		IncidentID: "inc-001",
		Resolution: "work_resumed",
		Message:    "Docker image rebuilt successfully; stories unblocked",
		Timestamp:  "2026-04-19T10:15:00Z",
	}

	payload := NewIncidentResolvedPayload(original)
	if payload.Kind != PayloadKindIncidentResolved {
		t.Errorf("expected kind %q, got %q", PayloadKindIncidentResolved, payload.Kind)
	}

	extracted, err := payload.ExtractIncidentResolved()
	if err != nil {
		t.Fatalf("ExtractIncidentResolved failed: %v", err)
	}

	if extracted.IncidentID != original.IncidentID {
		t.Errorf("IncidentID: got %q, want %q", extracted.IncidentID, original.IncidentID)
	}
	if extracted.Resolution != original.Resolution {
		t.Errorf("Resolution: got %q, want %q", extracted.Resolution, original.Resolution)
	}
	if extracted.Message != original.Message {
		t.Errorf("Message: got %q, want %q", extracted.Message, original.Message)
	}
	if extracted.Timestamp != original.Timestamp {
		t.Errorf("Timestamp: got %q, want %q", extracted.Timestamp, original.Timestamp)
	}
}

func TestIncidentOpenedPayload_WrongKind(t *testing.T) {
	// Create a story_complete payload, then try to extract as incident_opened
	payload := NewStoryCompletePayload(&StoryCompletePayload{
		StoryID:   "story-1",
		Title:     "test",
		Timestamp: "2026-04-19T10:00:00Z",
	})

	_, err := payload.ExtractIncidentOpened()
	if err == nil {
		t.Error("expected error when extracting incident_opened from story_complete payload")
	}
}

func TestIncidentResolvedPayload_WrongKind(t *testing.T) {
	// Create an incident_opened payload, then try to extract as incident_resolved
	payload := NewIncidentOpenedPayload(&Incident{
		ID:    "inc-001",
		Title: "test incident",
	})

	_, err := payload.ExtractIncidentResolved()
	if err == nil {
		t.Error("expected error when extracting incident_resolved from incident_opened payload")
	}
}

func TestIncidentKindConstants(t *testing.T) {
	if string(IncidentKindStoryBlocked) != "story_blocked" {
		t.Errorf("IncidentKindStoryBlocked = %q", IncidentKindStoryBlocked)
	}
	if string(IncidentKindClarification) != "clarification_needed" {
		t.Errorf("IncidentKindClarification = %q", IncidentKindClarification)
	}
	if string(IncidentKindSystemIdle) != "system_idle" {
		t.Errorf("IncidentKindSystemIdle = %q", IncidentKindSystemIdle)
	}
}

func TestIncidentActionConstants(t *testing.T) {
	if string(IncidentActionTryAgain) != "try_again" {
		t.Errorf("IncidentActionTryAgain = %q", IncidentActionTryAgain)
	}
	if string(IncidentActionChangeRequest) != "change_request" {
		t.Errorf("IncidentActionChangeRequest = %q", IncidentActionChangeRequest)
	}
	if string(IncidentActionSkip) != "skip" {
		t.Errorf("IncidentActionSkip = %q", IncidentActionSkip)
	}
	if string(IncidentActionResume) != "resume" {
		t.Errorf("IncidentActionResume = %q", IncidentActionResume)
	}
}

func TestIncidentActionPayload_RoundTrip(t *testing.T) {
	original := &IncidentActionPayload{
		IncidentID: "inc-042",
		Action:     "resume",
		Reason:     "Docker daemon restarted, environment should be healthy now",
	}

	payload := NewIncidentActionPayload(original)
	if payload.Kind != PayloadKindIncidentAction {
		t.Errorf("expected kind %q, got %q", PayloadKindIncidentAction, payload.Kind)
	}

	extracted, err := payload.ExtractIncidentAction()
	if err != nil {
		t.Fatalf("ExtractIncidentAction failed: %v", err)
	}

	if extracted.IncidentID != original.IncidentID {
		t.Errorf("IncidentID: got %q, want %q", extracted.IncidentID, original.IncidentID)
	}
	if extracted.Action != original.Action {
		t.Errorf("Action: got %q, want %q", extracted.Action, original.Action)
	}
	if extracted.Reason != original.Reason {
		t.Errorf("Reason: got %q, want %q", extracted.Reason, original.Reason)
	}
}

func TestIncidentActionResultPayload_RoundTrip(t *testing.T) {
	original := &IncidentActionResultPayload{
		IncidentID: "inc-042",
		Action:     "resume",
		Success:    true,
		Message:    "Incident resolved, 2 stories released from hold",
	}

	payload := NewIncidentActionResultPayload(original)
	if payload.Kind != PayloadKindIncidentActionResult {
		t.Errorf("expected kind %q, got %q", PayloadKindIncidentActionResult, payload.Kind)
	}

	extracted, err := payload.ExtractIncidentActionResult()
	if err != nil {
		t.Fatalf("ExtractIncidentActionResult failed: %v", err)
	}

	if extracted.IncidentID != original.IncidentID {
		t.Errorf("IncidentID: got %q, want %q", extracted.IncidentID, original.IncidentID)
	}
	if extracted.Action != original.Action {
		t.Errorf("Action: got %q, want %q", extracted.Action, original.Action)
	}
	if extracted.Success != original.Success {
		t.Errorf("Success: got %v, want %v", extracted.Success, original.Success)
	}
	if extracted.Message != original.Message {
		t.Errorf("Message: got %q, want %q", extracted.Message, original.Message)
	}
}

func TestIncidentActionResultPayload_Failure(t *testing.T) {
	original := &IncidentActionResultPayload{
		IncidentID: "inc-099",
		Action:     "resume",
		Success:    false,
		Message:    "Incident inc-099 not found or already resolved",
	}

	payload := NewIncidentActionResultPayload(original)
	extracted, err := payload.ExtractIncidentActionResult()
	if err != nil {
		t.Fatalf("ExtractIncidentActionResult failed: %v", err)
	}

	if extracted.Success {
		t.Error("expected Success=false for failed action result")
	}
	if extracted.Message != original.Message {
		t.Errorf("Message: got %q, want %q", extracted.Message, original.Message)
	}
}

func TestIncidentActionPayload_WrongKind(t *testing.T) {
	// Create an incident_opened payload, then try to extract as incident_action
	payload := NewIncidentOpenedPayload(&Incident{
		ID:    "inc-001",
		Title: "test incident",
	})

	_, err := payload.ExtractIncidentAction()
	if err == nil {
		t.Error("expected error when extracting incident_action from incident_opened payload")
	}
}

func TestIncidentActionResultPayload_WrongKind(t *testing.T) {
	// Create an incident_action payload, then try to extract as incident_action_result
	payload := NewIncidentActionPayload(&IncidentActionPayload{
		IncidentID: "inc-001",
		Action:     "resume",
		Reason:     "test",
	})

	_, err := payload.ExtractIncidentActionResult()
	if err == nil {
		t.Error("expected error when extracting incident_action_result from incident_action payload")
	}
}

func TestIncidentActionPayload_WithContent_RoundTrip(t *testing.T) {
	original := &IncidentActionPayload{
		IncidentID: "inc-100",
		Action:     "change_request",
		Reason:     "User wants different approach",
		Content:    "Please use PostgreSQL instead of SQLite for the database layer.",
	}

	payload := NewIncidentActionPayload(original)
	extracted, err := payload.ExtractIncidentAction()
	if err != nil {
		t.Fatalf("ExtractIncidentAction failed: %v", err)
	}

	if extracted.Content != original.Content {
		t.Errorf("Content: got %q, want %q", extracted.Content, original.Content)
	}
	if extracted.Action != original.Action {
		t.Errorf("Action: got %q, want %q", extracted.Action, original.Action)
	}
}

func TestIncidentActionPayload_ContentOmittedWhenEmpty(t *testing.T) {
	original := &IncidentActionPayload{
		IncidentID: "inc-101",
		Action:     "resume",
		Reason:     "Fixed the issue",
	}

	payload := NewIncidentActionPayload(original)
	extracted, err := payload.ExtractIncidentAction()
	if err != nil {
		t.Fatalf("ExtractIncidentAction failed: %v", err)
	}

	if extracted.Content != "" {
		t.Errorf("Content should be empty for resume, got %q", extracted.Content)
	}
}
