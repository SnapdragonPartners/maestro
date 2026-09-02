//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/work"
)

// openOne runs OpenWork and returns the single pending row.
func openOne(t *testing.T, f *fixture) store.OpenDispatch {
	t.Helper()
	open, err := f.store.OpenWork(context.Background(), f.organizationID)
	if err != nil {
		t.Fatalf("OpenWork: %v", err)
	}
	if len(open.Pending)+len(open.Accepted) != 1 {
		t.Fatalf("open work has %d pending and %d accepted rows, want one", len(open.Pending), len(open.Accepted))
	}
	if len(open.Pending) == 1 {
		return open.Pending[0]
	}
	return open.Accepted[0]
}

// amendNoOp accepts an amendment that leaves the view byte-identical: the
// digest stays, the sequence moves.
func (f *fixture) amendNoOp(t *testing.T, original *store.ManagementArtifact, scope store.Scope, lineage store.Lineage) {
	t.Helper()
	ctx := context.Background()
	base, err := f.store.AmendmentBase(ctx, f.organizationID, original.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	amendment, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload: json.RawMessage(`{}`), Type: original.Type, Summary: "no-op", Scope: scope, Lineage: lineage,
		AmendsArtifactID: &original.ArtifactID, OrganizationID: f.organizationID, UserID: f.userID, AuthorInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create amendment: %v", err)
	}
	review := f.review(t, amendment.ArtifactID, amendment.ReviewDigest, store.DecisionAccepted, f.reviewer, &base)
	if err := f.store.AcceptAmendment(ctx, f.organizationID, amendment.ArtifactID, review.ReviewID); err != nil {
		t.Fatalf("accept amendment: %v", err)
	}
}

// TestOpenWorkMapsEveryFieldOfBothSides is design D9's row-mapping proof:
// one change at a time, and the CURRENT side must move exactly where the
// change was while the SNAPSHOT stays where it was issued.
func TestOpenWorkMapsEveryFieldOfBothSides(t *testing.T) {
	ctx := context.Background()

	t.Run("snapshot and current agree at issue", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		p, c := f.addPredecessor(t, g, "p", true)
		d, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
		if err != nil {
			t.Fatal(err)
		}
		row := openOne(t, f)
		if row.Dispatch.StoryDispatchID != d.StoryDispatchID || row.Execution != nil {
			t.Fatalf("row %+v", row)
		}
		if *row.Current.StoryVersion != row.Dispatch.StoryVersion || *row.Current.EpicVersion != row.Dispatch.EpicVersion {
			t.Fatalf("current %+v / %+v differs from snapshot %+v / %+v at issue",
				row.Current.StoryVersion, row.Current.EpicVersion, row.Dispatch.StoryVersion, row.Dispatch.EpicVersion)
		}
		if len(row.Current.Edges) != 1 || row.Current.Edges[0].PredecessorStoryID != p.StoryID ||
			*row.Current.Edges[0].Completion != row.Dispatch.Basis[0].Completion || row.Current.Edges[0].Completion.ArtifactID != c.ArtifactID {
			t.Fatalf("current edges %+v, snapshot basis %+v", row.Current.Edges, row.Dispatch.Basis)
		}
	})

	t.Run("story sequence moves on a no-op amendment", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		if _, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID); err != nil {
			t.Fatal(err)
		}
		f.amendNoOp(t, g.storyRecord, storyScope(g.story.StoryID), storyLineage(g.hierarchy, g.story.StoryID))
		row := openOne(t, f)
		cur, snap := row.Current.StoryVersion, row.Dispatch.StoryVersion
		if cur.ArtifactID != snap.ArtifactID || cur.Digest != snap.Digest || cur.Sequence != snap.Sequence+1 {
			t.Fatalf("current %+v vs snapshot %+v: only the sequence should move", cur, snap)
		}
		if *row.Current.EpicVersion != row.Dispatch.EpicVersion {
			t.Fatal("the epic side moved too")
		}
	})

	t.Run("epic sequence moves on a no-op amendment", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		if _, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID); err != nil {
			t.Fatal(err)
		}
		f.amendNoOp(t, g.epicRecord, epicScope(g.epic.EpicID), epicLineage(g.hierarchy))
		row := openOne(t, f)
		cur, snap := row.Current.EpicVersion, row.Dispatch.EpicVersion
		if cur.ArtifactID != snap.ArtifactID || cur.Digest != snap.Digest || cur.Sequence != snap.Sequence+1 {
			t.Fatalf("current %+v vs snapshot %+v", cur, snap)
		}
	})

	t.Run("story id and digest move on a repoint", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		if _, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID); err != nil {
			t.Fatal(err)
		}
		twin := f.acceptedRecord(t, work.TypeStoryRecord, storyScope(g.story.StoryID), storyLineage(g.hierarchy, g.story.StoryID), `{"intent":"add the flag"}`)
		if err := f.store.SetStoryGoverningArtifact(ctx, f.organizationID, g.story.StoryID, twin.ArtifactID); err != nil {
			t.Fatal(err)
		}
		row := openOne(t, f)
		cur, snap := row.Current.StoryVersion, row.Dispatch.StoryVersion
		// Identical content: the digest and sequence agree, only the id moves.
		if cur.ArtifactID == snap.ArtifactID || cur.ArtifactID != twin.ArtifactID || cur.Digest != snap.Digest || cur.Sequence != snap.Sequence {
			t.Fatalf("current %+v vs snapshot %+v: only the id should move", cur, snap)
		}
	})

	t.Run("edge set and completion move", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		p1, c1 := f.addPredecessor(t, g, "one", true)
		if _, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID); err != nil {
			t.Fatal(err)
		}
		// An added, already-satisfied predecessor.
		p2, _ := f.addPredecessor(t, g, "two", true)
		row := openOne(t, f)
		if len(row.Dispatch.Basis) != 1 || len(row.Current.Edges) != 2 {
			t.Fatalf("snapshot has %d edges, current %d; want 1 and 2", len(row.Dispatch.Basis), len(row.Current.Edges))
		}
		seen := map[uuid.UUID]bool{}
		for _, e := range row.Current.Edges {
			seen[e.PredecessorStoryID] = true
		}
		if !seen[p1.StoryID] || !seen[p2.StoryID] {
			t.Fatalf("current edges %+v", row.Current.Edges)
		}
		// The first completion's sequence moves on a no-op amendment, and
		// stays paired with ITS predecessor.
		f.amendNoOp(t, c1, storyScope(p1.StoryID), storyLineage(g.hierarchy, p1.StoryID))
		row = openOne(t, f)
		for _, e := range row.Current.Edges {
			if e.PredecessorStoryID == p1.StoryID {
				if e.Completion.ArtifactID != c1.ArtifactID || e.Completion.Sequence != row.Dispatch.Basis[0].Completion.Sequence+1 {
					t.Fatalf("edge %+v vs snapshot %+v", e, row.Dispatch.Basis[0])
				}
			}
		}
		// An unsatisfied edge reads as a nil completion.
		if _, err := f.pool.Exec(ctx, `UPDATE story_dependencies SET satisfying_completion_artifact_id = NULL WHERE predecessor_story_id = $1`, p2.StoryID); err != nil {
			t.Fatal(err)
		}
		row = openOne(t, f)
		for _, e := range row.Current.Edges {
			if e.PredecessorStoryID == p2.StoryID && e.Completion != nil {
				t.Fatalf("an unsatisfied edge carries a completion: %+v", e)
			}
		}
	})

	t.Run("accepted rows carry their execution; a missing one is an invariant error", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		d, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
		if err != nil {
			t.Fatal(err)
		}
		execution, err := f.store.AcceptDispatch(ctx, f.organizationID, d.StoryDispatchID)
		if err != nil {
			t.Fatal(err)
		}
		row := openOne(t, f)
		if row.Execution == nil || row.Execution.ExecutionID != execution.ExecutionID || row.Execution.AuthorityState != store.AuthorityCurrent {
			t.Fatalf("accepted row %+v", row.Execution)
		}
		// THE MUTANT: skip the invariant error and return the row with a
		// nil execution; the projection would then misfile it.
		if _, err := f.pool.Exec(ctx, "DELETE FROM executions WHERE execution_id = $1", execution.ExecutionID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.OpenWork(ctx, f.organizationID); !errors.Is(err, store.ErrInvariant) {
			t.Fatalf("an accepted dispatch without an execution: %v, want ErrInvariant", err)
		}
	})
}
