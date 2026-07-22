package mirror

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// These are the regression tests for the mirror clone race (P-11 / ADR 0027):
// two writers to the same mirror directory must never hold it at once.
//
// They deliberately assert on lock *state and identity* rather than on
// observed interleaving. A timing-based test ("did two goroutines overlap?")
// can pass even with locking removed, because a short critical section may
// simply never overlap on a given run; these fail deterministically instead.

// TestLockPathExcludesSecondHolder proves the lock is actually held: while one
// caller holds a path, TryLock on the same path's mutex must fail, and must
// succeed once released. Removing the locking makes this fail every run.
func TestLockPathExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo.git")

	release := LockPath(path)

	if mutexForPath(path).TryLock() {
		t.Fatal("acquired the same mirror path twice; LockPath does not exclude a second writer")
	}

	release()

	mu := mutexForPath(path)
	if !mu.TryLock() {
		t.Fatal("path still locked after release; LockPath leaks the lock")
	}
	mu.Unlock()
}

// TestLockPathCanonicalizesKey proves callers that spell the same directory
// differently share one lock. mirror.Manager builds its path with
// filepath.Join while pkg/coder's CloneManager and pkg/forge/gitea pass their
// own; if keys were not canonicalized they would take different locks and the
// race would survive the fix.
//
// Asserted by pointer identity plus TryLock, so it cannot pass by scheduling
// accident the way a select/default probe can.
func TestLockPathCanonicalizesKey(t *testing.T) {
	dir := t.TempDir()
	canonical := filepath.Join(dir, "repo.git")
	uncleaned := filepath.Join(dir, "sub", "..", "repo.git")
	relativeish := canonical + string(filepath.Separator) + "." // trailing "/."

	if a, b := mutexForPath(canonical), mutexForPath(uncleaned); a != b {
		t.Errorf("uncleaned spelling took a different lock (%p vs %p); keys are not canonicalized", a, b)
	}
	if a, b := mutexForPath(canonical), mutexForPath(relativeish); a != b {
		t.Errorf("trailing-dot spelling took a different lock (%p vs %p); keys are not canonicalized", a, b)
	}

	release := LockPath(canonical)
	defer release()
	if mutexForPath(uncleaned).TryLock() {
		t.Fatal("locked via an alternate spelling while the canonical path was held")
	}
}

// TestLockPathDistinctPathsDoNotContend guards the other direction: locking is
// per-mirror, so unrelated mirrors must proceed concurrently. A single global
// mirror lock would satisfy every exclusion test above while needlessly
// serializing every repository.
func TestLockPathDistinctPathsDoNotContend(t *testing.T) {
	dir := t.TempDir()

	release := LockPath(filepath.Join(dir, "a.git"))
	defer release()

	other := mutexForPath(filepath.Join(dir, "b.git"))
	if !other.TryLock() {
		t.Fatal("a distinct mirror path was blocked; locks are not per-path")
	}
	other.Unlock()
}

// TestLockPathSerializesConcurrentWriters is the end-to-end behavioural check:
// many concurrent writers each observe an exclusive critical section. Bounded
// by a real handoff (each writer waits for the previous release) so a hang
// surfaces as this test's own failure rather than the package timeout.
func TestLockPathSerializesConcurrentWriters(t *testing.T) {
	const writers = 16

	path := filepath.Join(t.TempDir(), "repo.git")

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex // guards `shared` — the resource the path lock protects
		shared   int
		occupied bool
		overlaps int
	)

	for range writers {
		wg.Go(func() {
			release := LockPath(path)
			defer release()

			mu.Lock()
			if occupied {
				overlaps++
			}
			occupied = true
			mu.Unlock()

			shared++ // safe only under the path lock; -race flags this if it is not

			mu.Lock()
			occupied = false
			mu.Unlock()
		})
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("writers did not all complete; LockPath deadlocked")
	}

	if overlaps != 0 {
		t.Errorf("observed %d overlapping critical sections", overlaps)
	}
	if shared != writers {
		t.Errorf("shared = %d, want %d — lost updates mean writers were not serialized", shared, writers)
	}
}
