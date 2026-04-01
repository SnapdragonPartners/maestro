package architect

import (
	"sync"
	"time"

	"orchestrator/pkg/proto"
)

// ScopeWidener tracks recent failures and auto-escalates scope when the same
// failure kind recurs across multiple stories within a time window.
type ScopeWidener struct {
	entries             []recentFailureEntry
	mu                  sync.Mutex
	timeWindow          time.Duration
	recurrenceThreshold int
	maxEntries          int
}

type recentFailureEntry struct {
	Timestamp time.Time
	Kind      proto.FailureKind
	StoryID   string
}

// DefaultRecurrenceThreshold is the number of distinct stories with the same
// failure kind required to trigger scope widening.
const DefaultRecurrenceThreshold = 3

// DefaultScopeWideningWindow is the time window for recurrence detection.
const DefaultScopeWideningWindow = 30 * time.Minute

// NewScopeWidener creates a ScopeWidener with default settings.
func NewScopeWidener() *ScopeWidener {
	return &ScopeWidener{
		recurrenceThreshold: DefaultRecurrenceThreshold,
		timeWindow:          DefaultScopeWideningWindow,
		maxEntries:          100,
	}
}

// RecordFailure adds a failure to the ring buffer for recurrence tracking.
func (sw *ScopeWidener) RecordFailure(kind proto.FailureKind, storyID string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	now := time.Now()
	sw.entries = append(sw.entries, recentFailureEntry{
		Kind:      kind,
		StoryID:   storyID,
		Timestamp: now,
	})

	sw.pruneLocked(now)
}

// CheckForWidening evaluates whether the given failure kind has recurred across
// enough distinct stories to warrant scope escalation. Returns the widened scope
// or the original scope if no widening is warranted.
func (sw *ScopeWidener) CheckForWidening(kind proto.FailureKind, currentScope proto.FailureScope) proto.FailureScope {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	now := time.Now()
	sw.pruneLocked(now)

	// Count distinct story IDs with the same kind in the window
	storySet := make(map[string]struct{})
	for i := range sw.entries {
		if sw.entries[i].Kind == kind {
			storySet[sw.entries[i].StoryID] = struct{}{}
		}
	}

	if len(storySet) < sw.recurrenceThreshold {
		return currentScope
	}

	// Widen one level
	switch currentScope {
	case proto.FailureScopeAttempt:
		return proto.FailureScopeStory
	case proto.FailureScopeStory:
		return proto.FailureScopeEpoch
	case proto.FailureScopeEpoch:
		return proto.FailureScopeSystem
	default:
		return currentScope
	}
}

// pruneLocked removes entries older than the time window and trims to maxEntries.
// Must be called with sw.mu held.
func (sw *ScopeWidener) pruneLocked(now time.Time) {
	cutoff := now.Add(-sw.timeWindow)

	// Remove expired entries
	n := 0
	for i := range sw.entries {
		if sw.entries[i].Timestamp.After(cutoff) {
			sw.entries[n] = sw.entries[i]
			n++
		}
	}
	sw.entries = sw.entries[:n]

	// Trim to max size (keep most recent)
	if len(sw.entries) > sw.maxEntries {
		sw.entries = sw.entries[len(sw.entries)-sw.maxEntries:]
	}
}
