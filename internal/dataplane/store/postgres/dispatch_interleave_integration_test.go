//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/work"
)

// TestCreateDispatchReadsTheStoryUnderTheEpicLock is design D10's forced
// interleaving. Another transaction holds the Epic lock, CreateDispatch
// blocks behind it, the holder repoints the Story's governing pointer and
// commits; the dispatch must record the NEW pointer.
//
// THE MUTANT: build the dispatch from the Story row read before the lock.
// The dispatch then records the old pointer, and this test fails on the
// artifact id -- the exact stale basis the design names.
func TestCreateDispatchReadsTheStoryUnderTheEpicLock(t *testing.T) {
	f := newFixture(t)
	g := provisionGoverned(t, f)
	ctx := context.Background()
	twin := f.acceptedRecord(t, work.TypeStoryRecord, storyScope(g.story.StoryID), storyLineage(g.hierarchy, g.story.StoryID), `{"intent":"add the flag"}`)

	// The other writer takes the Epic lock first, as D10's order requires.
	holder, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT 1 FROM epics WHERE epic_id = $1 FOR UPDATE", g.epic.EpicID); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		dispatch *store.StoryDispatch
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		d, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
		done <- outcome{d, err}
	}()

	// Wait until the dispatcher is blocked on the Epic lock -- observed,
	// not assumed: a backend other than the holder waiting on a lock.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := f.pool.QueryRow(ctx,
			"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").
			Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("CreateDispatch never blocked on the Epic lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The repoint, under the lock the dispatcher is waiting for.
	if _, err := holder.Exec(ctx, "UPDATE stories SET governing_artifact_id = $1 WHERE story_id = $2", twin.ArtifactID, g.story.StoryID); err != nil {
		t.Fatal(err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("CreateDispatch: %v", out.err)
		}
		if out.dispatch.StoryVersion.ArtifactID != twin.ArtifactID {
			t.Fatalf("the dispatch recorded %s, want the repointed %s: it was built from the Story row "+
				"read before the Epic lock", out.dispatch.StoryVersion.ArtifactID, twin.ArtifactID)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("CreateDispatch did not return after the lock was released")
	}
}
