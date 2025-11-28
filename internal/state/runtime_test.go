package state

import (
	"testing"
	"time"
)

// TestNewRuntimeState tests state creation.
func TestNewRuntimeState(t *testing.T) {
	state := NewRuntimeState()

	if state == nil {
		t.Fatal("expected state, got nil")
	}

	if state.Active != nil {
		t.Error("expected active to be nil initially")
	}

	if len(state.History) != 0 {
		t.Errorf("expected empty history, got %d entries", len(state.History))
	}
}

// TestSetGetActive tests active container management.
func TestSetGetActive(t *testing.T) {
	state := NewRuntimeState()

	// Initially nil
	active := state.GetActive()
	if active != nil {
		t.Error("expected nil active container initially")
	}

	// Set active container
	testContainer := &ActiveContainer{
		Role:    RoleTarget,
		CID:     "cid-123",
		ImageID: "sha256:abc",
		Name:    "test-container",
		Started: time.Now(),
	}

	state.SetActive(testContainer)

	// Get active container
	retrieved := state.GetActive()
	if retrieved == nil {
		t.Fatal("expected active container, got nil")
	}

	if retrieved.Role != RoleTarget {
		t.Errorf("expected role %s, got %s", RoleTarget, retrieved.Role)
	}

	if retrieved.CID != "cid-123" {
		t.Errorf("expected CID %q, got %q", "cid-123", retrieved.CID)
	}

	if retrieved.ImageID != "sha256:abc" {
		t.Errorf("expected ImageID %q, got %q", "sha256:abc", retrieved.ImageID)
	}

	// Verify copy semantics (mutation doesn't affect internal state)
	retrieved.CID = "modified"
	retrieved2 := state.GetActive()
	if retrieved2.CID != "cid-123" {
		t.Error("expected copy semantics, but internal state was mutated")
	}

	// Clear active
	state.SetActive(nil)
	cleared := state.GetActive()
	if cleared != nil {
		t.Error("expected nil after clearing active")
	}
}

// TestHistoryPush tests history management.
func TestHistoryPush(t *testing.T) {
	state := NewRuntimeState()

	// Add first entry
	entry1 := &HistoryEntry{
		Role:    RoleTarget,
		ImageID: "sha256:aaa",
		Name:    "container-1",
		Started: time.Now().Add(-10 * time.Minute),
		Stopped: time.Now().Add(-5 * time.Minute),
	}
	state.HistoryPush(entry1)

	if len(state.History) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(state.History))
	}

	// Add second entry (should be at front)
	entry2 := &HistoryEntry{
		Role:    RoleSafe,
		ImageID: "sha256:bbb",
		Name:    "container-2",
		Started: time.Now().Add(-3 * time.Minute),
		Stopped: time.Now(),
	}
	state.HistoryPush(entry2)

	if len(state.History) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(state.History))
	}

	// Verify newest first
	history := state.GetHistory()
	if history[0].ImageID != "sha256:bbb" {
		t.Error("expected newest entry first")
	}
	if history[1].ImageID != "sha256:aaa" {
		t.Error("expected oldest entry last")
	}
}

// TestHistoryRingBuffer tests history size limits.
func TestHistoryRingBuffer(t *testing.T) {
	state := NewRuntimeState()

	// Add 15 entries (exceeds max of 10)
	for i := 0; i < 15; i++ {
		entry := &HistoryEntry{
			Role:    RoleTarget,
			ImageID: "sha256:test",
			Name:    "container",
			Started: time.Now(),
			Stopped: time.Now(),
		}
		state.HistoryPush(entry)
	}

	// Should only keep last 10
	if len(state.History) != 10 {
		t.Errorf("expected 10 history entries (ring buffer), got %d", len(state.History))
	}
}

// TestHistoryTop tests most recent entry retrieval.
func TestHistoryTop(t *testing.T) {
	state := NewRuntimeState()

	// Empty history
	top := state.HistoryTop()
	if top != nil {
		t.Error("expected nil for empty history")
	}

	// Add entry
	entry := &HistoryEntry{
		Role:    RoleTarget,
		ImageID: "sha256:latest",
		Name:    "test-container",
		Started: time.Now(),
		Stopped: time.Now(),
	}
	state.HistoryPush(entry)

	// Get top
	top = state.HistoryTop()
	if top == nil {
		t.Fatal("expected history entry, got nil")
	}

	if top.ImageID != "sha256:latest" {
		t.Errorf("expected ImageID %q, got %q", "sha256:latest", top.ImageID)
	}

	// Verify copy semantics
	top.ImageID = "modified"
	top2 := state.HistoryTop()
	if top2.ImageID != "sha256:latest" {
		t.Error("expected copy semantics, but internal state was mutated")
	}
}

// TestClearHistory tests history clearing.
func TestClearHistory(t *testing.T) {
	state := NewRuntimeState()

	// Add entries
	for i := 0; i < 3; i++ {
		entry := &HistoryEntry{
			Role:    RoleTarget,
			ImageID: "sha256:test",
			Name:    "container",
			Started: time.Now(),
			Stopped: time.Now(),
		}
		state.HistoryPush(entry)
	}

	if len(state.History) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(state.History))
	}

	// Clear
	state.ClearHistory()

	if len(state.History) != 0 {
		t.Errorf("expected empty history after clear, got %d entries", len(state.History))
	}
}

// TestIsActiveImageID tests active image ID checking.
func TestIsActiveImageID(t *testing.T) {
	state := NewRuntimeState()

	// No active container
	if state.IsActiveImageID("sha256:test") {
		t.Error("expected false when no active container")
	}

	// Set active container
	state.SetActive(&ActiveContainer{
		Role:    RoleTarget,
		CID:     "cid-123",
		ImageID: "sha256:abc123",
		Name:    "test",
		Started: time.Now(),
	})

	// Matching ID
	if !state.IsActiveImageID("sha256:abc123") {
		t.Error("expected true for matching image ID")
	}

	// Non-matching ID
	if state.IsActiveImageID("sha256:different") {
		t.Error("expected false for non-matching image ID")
	}
}

// TestRoleConstants tests role constant values.
func TestRoleConstants(t *testing.T) {
	if RoleSafe != "safe" {
		t.Errorf("expected RoleSafe to be %q, got %q", "safe", RoleSafe)
	}

	if RoleTarget != "target" {
		t.Errorf("expected RoleTarget to be %q, got %q", "target", RoleTarget)
	}
}

// TestConcurrentAccess tests thread safety (basic smoke test).
func TestConcurrentAccess(_ *testing.T) {
	state := NewRuntimeState()

	// Launch concurrent readers and writers
	done := make(chan bool, 3)

	// Writer
	go func() {
		for i := 0; i < 100; i++ {
			state.SetActive(&ActiveContainer{
				Role:    RoleTarget,
				CID:     "cid",
				ImageID: "sha256:test",
				Name:    "test",
				Started: time.Now(),
			})
		}
		done <- true
	}()

	// Reader 1
	go func() {
		for i := 0; i < 100; i++ {
			_ = state.GetActive()
		}
		done <- true
	}()

	// Reader 2
	go func() {
		for i := 0; i < 100; i++ {
			_ = state.IsActiveImageID("sha256:test")
		}
		done <- true
	}()

	// Wait for completion (should not panic)
	<-done
	<-done
	<-done
}
