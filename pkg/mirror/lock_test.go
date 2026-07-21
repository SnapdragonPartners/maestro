package mirror

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestLockPathSerializesSamePath is the regression test for the mirror clone
// race (P-11 / ADR 0027): two writers to the same mirror directory must never
// be inside their critical sections at the same time. Before the per-path lock,
// one caller's in-progress `git clone --mirror` was deleted mid-clone by
// another caller's corruption check.
//
// Deterministic by construction: any overlap flips inCritical and is recorded,
// rather than depending on timing to expose the bug.
func TestLockPathSerializesSamePath(t *testing.T) {
	const writers = 32

	var (
		inCritical atomic.Bool
		overlaps   atomic.Int64
		entries    atomic.Int64
		wg         sync.WaitGroup
	)

	path := filepath.Join(t.TempDir(), "repo.git")

	for range writers {
		wg.Go(func() {
			release := LockPath(path)
			defer release()

			// If the lock works, no other goroutine can be here.
			if !inCritical.CompareAndSwap(false, true) {
				overlaps.Add(1)
			}
			entries.Add(1)
			inCritical.Store(false)
		})
	}
	wg.Wait()

	if got := overlaps.Load(); got != 0 {
		t.Errorf("detected %d overlapping critical sections; LockPath is not mutually exclusive", got)
	}
	if got := entries.Load(); got != writers {
		t.Errorf("entries = %d, want %d — every writer must run exactly once", got, writers)
	}
}

// TestLockPathCanonicalizesKey proves callers that spell the same directory
// differently still share one lock. mirror.Manager builds its path with
// filepath.Join while pkg/coder's CloneManager builds its own; if canonical
// spelling were not enforced they would take different locks and the race
// would survive the fix.
func TestLockPathCanonicalizesKey(t *testing.T) {
	dir := t.TempDir()
	canonical := filepath.Join(dir, "repo.git")
	uncleaned := filepath.Join(dir, "sub", "..", "repo.git")

	release := LockPath(canonical)

	acquired := make(chan struct{})
	go func() {
		defer close(acquired)
		LockPath(uncleaned)()
	}()

	select {
	case <-acquired:
		t.Fatal("uncleaned spelling of the same path took a different lock; keys are not canonicalized")
	default:
	}

	release()
	<-acquired // must now succeed
}

// TestLockPathDistinctPathsDoNotContend guards the other direction: locking is
// per-mirror, so unrelated mirrors must proceed concurrently. A global mirror
// lock would pass the exclusion tests above while needlessly serializing every
// repository.
func TestLockPathDistinctPathsDoNotContend(t *testing.T) {
	dir := t.TempDir()

	releaseA := LockPath(filepath.Join(dir, "a.git"))
	defer releaseA()

	acquired := make(chan struct{})
	go func() {
		defer close(acquired)
		LockPath(filepath.Join(dir, "b.git"))()
	}()

	<-acquired // blocks forever (test timeout) if distinct paths share a lock
}
