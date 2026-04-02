package architect

import (
	"testing"
	"time"

	"orchestrator/pkg/proto"
)

func TestScopeWidener_NoWideningBelowThreshold(t *testing.T) {
	sw := NewScopeWidener()

	// 2 stories with same environment failure (below threshold of 3)
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "no space left on device")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2", "no space left on device")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, "no space left on device", proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (attempt), got %q", scope)
	}
}

func TestScopeWidener_WidensAtThreshold(t *testing.T) {
	sw := NewScopeWidener()

	// 3 distinct stories with same cause → should widen
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "no space left on device")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2", "no space left on device")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3", "no space left on device")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, "no space left on device", proto.FailureScopeAttempt)
	if scope != proto.FailureScopeStory {
		t.Errorf("expected attempt → story, got %q", scope)
	}
}

func TestScopeWidener_WidensStoryToEpoch(t *testing.T) {
	sw := NewScopeWidener()

	sw.RecordFailure(proto.FailureKindStoryInvalid, "story-1", "missing acceptance criteria")
	sw.RecordFailure(proto.FailureKindStoryInvalid, "story-2", "missing acceptance criteria")
	sw.RecordFailure(proto.FailureKindStoryInvalid, "story-3", "missing acceptance criteria")

	scope := sw.CheckForWidening(proto.FailureKindStoryInvalid, "missing acceptance criteria", proto.FailureScopeStory)
	if scope != proto.FailureScopeEpoch {
		t.Errorf("expected story → epoch, got %q", scope)
	}
}

func TestScopeWidener_WidensEpochToSystem(t *testing.T) {
	sw := NewScopeWidener()

	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "docker daemon not running")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2", "docker daemon not running")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3", "docker daemon not running")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, "docker daemon not running", proto.FailureScopeEpoch)
	if scope != proto.FailureScopeSystem {
		t.Errorf("expected epoch → system, got %q", scope)
	}
}

func TestScopeWidener_SystemDoesNotWiden(t *testing.T) {
	sw := NewScopeWidener()

	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "disk full")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2", "disk full")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3", "disk full")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, "disk full", proto.FailureScopeSystem)
	if scope != proto.FailureScopeSystem {
		t.Errorf("expected system unchanged, got %q", scope)
	}
}

func TestScopeWidener_DuplicateStoriesNotCounted(t *testing.T) {
	sw := NewScopeWidener()

	// Same story 3 times — only 1 distinct story
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "disk full")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "disk full")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "disk full")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, "disk full", proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (duplicate stories), got %q", scope)
	}
}

func TestScopeWidener_DifferentKindsIndependent(t *testing.T) {
	sw := NewScopeWidener()

	// 2 environment + 1 prerequisite = not enough of either kind
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "disk full")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2", "disk full")
	sw.RecordFailure(proto.FailureKindPrerequisite, "story-3", "auth token expired")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, "disk full", proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (different kinds), got %q", scope)
	}
}

func TestScopeWidener_DifferentExplanationsDoNotGroup(t *testing.T) {
	sw := NewScopeWidener()

	// 3 environment failures but with different causes — should NOT widen
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "no space left on device")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2", "permission denied on /workspace")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3", "docker daemon not running")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, "no space left on device", proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (different explanations), got %q", scope)
	}
}

func TestScopeWidener_ExpiredEntriesPruned(t *testing.T) {
	sw := &ScopeWidener{
		recurrenceThreshold: 3,
		timeWindow:          1 * time.Millisecond, // Very short window
		maxEntries:          100,
	}

	sw.RecordFailure(proto.FailureKindEnvironment, "story-1", "disk full")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2", "disk full")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3", "disk full")

	// Wait for entries to expire
	time.Sleep(5 * time.Millisecond)

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, "disk full", proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (entries expired), got %q", scope)
	}
}
