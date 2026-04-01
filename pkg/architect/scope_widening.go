package architect

import (
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/proto"
)

// ScopeWidener tracks recent failures and auto-escalates scope when the same
// failure kind+cause recurs across multiple stories within a time window.
// Failures are grouped by a fingerprint (kind + normalized explanation prefix)
// so that unrelated failures of the same kind do not trigger false widening.
type ScopeWidener struct {
	entries             []recentFailureEntry
	mu                  sync.Mutex
	timeWindow          time.Duration
	recurrenceThreshold int
	maxEntries          int
}

type recentFailureEntry struct {
	Timestamp   time.Time
	Fingerprint string
	StoryID     string
}

// DefaultRecurrenceThreshold is the number of distinct stories with the same
// failure fingerprint required to trigger scope widening.
const DefaultRecurrenceThreshold = 3

// DefaultScopeWideningWindow is the time window for recurrence detection.
const DefaultScopeWideningWindow = 30 * time.Minute

// fingerprintMaxLen is the max length of explanation prefix used in fingerprints.
const fingerprintMaxLen = 60

// NewScopeWidener creates a ScopeWidener with default settings.
func NewScopeWidener() *ScopeWidener {
	return &ScopeWidener{
		recurrenceThreshold: DefaultRecurrenceThreshold,
		timeWindow:          DefaultScopeWideningWindow,
		maxEntries:          100,
	}
}

// failureFingerprint produces a grouping key from kind + normalized explanation prefix.
// Two failures with the same kind but different explanations will not group together.
func failureFingerprint(kind proto.FailureKind, explanation string) string {
	// Normalize: lowercase, trim, take first N chars
	normalized := strings.ToLower(strings.TrimSpace(explanation))
	if len(normalized) > fingerprintMaxLen {
		normalized = normalized[:fingerprintMaxLen]
	}
	return string(kind) + ":" + normalized
}

// RecordFailure adds a failure to the ring buffer for recurrence tracking.
func (sw *ScopeWidener) RecordFailure(kind proto.FailureKind, storyID, explanation string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	now := time.Now()
	sw.entries = append(sw.entries, recentFailureEntry{
		Fingerprint: failureFingerprint(kind, explanation),
		StoryID:     storyID,
		Timestamp:   now,
	})

	sw.pruneLocked(now)
}

// CheckForWidening evaluates whether the given failure fingerprint has recurred across
// enough distinct stories to warrant scope escalation. Returns the widened scope
// or the original scope if no widening is warranted.
func (sw *ScopeWidener) CheckForWidening(kind proto.FailureKind, explanation string, currentScope proto.FailureScope) proto.FailureScope {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	now := time.Now()
	sw.pruneLocked(now)

	fp := failureFingerprint(kind, explanation)

	// Count distinct story IDs with the same fingerprint in the window
	storySet := make(map[string]struct{})
	for i := range sw.entries {
		if sw.entries[i].Fingerprint == fp {
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
