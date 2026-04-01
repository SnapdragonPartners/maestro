package architect

import (
	"testing"
	"time"

	"orchestrator/pkg/proto"
)

func TestScopeWidener_NoWideningBelowThreshold(t *testing.T) {
	sw := NewScopeWidener()

	// 2 stories with environment failures (below threshold of 3)
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (attempt), got %q", scope)
	}
}

func TestScopeWidener_WidensAtThreshold(t *testing.T) {
	sw := NewScopeWidener()

	// 3 distinct stories → should widen
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, proto.FailureScopeAttempt)
	if scope != proto.FailureScopeStory {
		t.Errorf("expected attempt → story, got %q", scope)
	}
}

func TestScopeWidener_WidensStoryToEpoch(t *testing.T) {
	sw := NewScopeWidener()

	sw.RecordFailure(proto.FailureKindStoryInvalid, "story-1")
	sw.RecordFailure(proto.FailureKindStoryInvalid, "story-2")
	sw.RecordFailure(proto.FailureKindStoryInvalid, "story-3")

	scope := sw.CheckForWidening(proto.FailureKindStoryInvalid, proto.FailureScopeStory)
	if scope != proto.FailureScopeEpoch {
		t.Errorf("expected story → epoch, got %q", scope)
	}
}

func TestScopeWidener_WidensEpochToSystem(t *testing.T) {
	sw := NewScopeWidener()

	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, proto.FailureScopeEpoch)
	if scope != proto.FailureScopeSystem {
		t.Errorf("expected epoch → system, got %q", scope)
	}
}

func TestScopeWidener_SystemDoesNotWiden(t *testing.T) {
	sw := NewScopeWidener()

	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, proto.FailureScopeSystem)
	if scope != proto.FailureScopeSystem {
		t.Errorf("expected system unchanged, got %q", scope)
	}
}

func TestScopeWidener_DuplicateStoriesNotCounted(t *testing.T) {
	sw := NewScopeWidener()

	// Same story 3 times — only 1 distinct story
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (duplicate stories), got %q", scope)
	}
}

func TestScopeWidener_DifferentKindsIndependent(t *testing.T) {
	sw := NewScopeWidener()

	// 2 environment + 1 prerequisite = not enough of either kind
	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2")
	sw.RecordFailure(proto.FailureKindPrerequisite, "story-3")

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (different kinds), got %q", scope)
	}
}

func TestScopeWidener_ExpiredEntriesPruned(t *testing.T) {
	sw := &ScopeWidener{
		recurrenceThreshold: 3,
		timeWindow:          1 * time.Millisecond, // Very short window
		maxEntries:          100,
	}

	sw.RecordFailure(proto.FailureKindEnvironment, "story-1")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-2")
	sw.RecordFailure(proto.FailureKindEnvironment, "story-3")

	// Wait for entries to expire
	time.Sleep(5 * time.Millisecond)

	scope := sw.CheckForWidening(proto.FailureKindEnvironment, proto.FailureScopeAttempt)
	if scope != proto.FailureScopeAttempt {
		t.Errorf("expected no widening (entries expired), got %q", scope)
	}
}
