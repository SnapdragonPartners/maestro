//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/work"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// acceptedRecord creates and accepts a Management artifact of the given
// type at the given scope, authored by f.author and reviewed by f.reviewer.
func (f *fixture) acceptedRecord(t *testing.T, artifactType registry.Type, scope store.Scope, lineage store.Lineage, payload string) *store.ManagementArtifact {
	t.Helper()
	draft := f.draftRecord(t, artifactType, scope, lineage, payload)
	if err := f.store.AcceptArtifact(context.Background(), f.organizationID, draft.ArtifactID, f.acceptableReview(t, draft)); err != nil {
		t.Fatalf("accept %s: %v", artifactType, err)
	}
	return draft
}

func (f *fixture) draftRecord(t *testing.T, artifactType registry.Type, scope store.Scope, lineage store.Lineage, payload string) *store.ManagementArtifact {
	t.Helper()
	draft, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Payload: json.RawMessage(payload), Type: artifactType, Summary: string(artifactType), Scope: scope, Lineage: lineage,
		OrganizationID: f.organizationID, UserID: f.userID, AuthorInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create %s: %v", artifactType, err)
	}
	return draft
}

func storyScope(id uuid.UUID) store.Scope { return store.Scope{Type: store.ScopeStory, ID: id} }
func epicScope(id uuid.UUID) store.Scope  { return store.Scope{Type: store.ScopeEpic, ID: id} }

// epicLineage and storyLineage are the denormalised lineage the schema's
// lineage check requires beside a scope.
func epicLineage(h hierarchy) store.Lineage {
	return store.Lineage{ProductID: &h.product, FeatureID: &h.feature.FeatureID, EpicID: &h.epic.EpicID}
}

func storyLineage(h hierarchy, storyID uuid.UUID) store.Lineage {
	l := epicLineage(h)
	l.StoryID = &storyID
	return l
}

// governed provisions the hierarchy, points Story and Epic at accepted
// records, and ensures the Work Group: everything a dispatch needs except
// predecessors.
type governed struct {
	hierarchy
	storyRecord *store.ManagementArtifact
	epicRecord  *store.ManagementArtifact
	workGroup   uuid.UUID
}

func provisionGoverned(t *testing.T, f *fixture) governed {
	t.Helper()
	ctx := context.Background()
	h := provisionHierarchy(t, f)
	epicRecord := f.acceptedRecord(t, work.TypeEpicRecord, epicScope(h.epic.EpicID), epicLineage(h), `{"intent":"flags","mode":"factory"}`)
	storyRecord := f.acceptedRecord(t, work.TypeStoryRecord, storyScope(h.story.StoryID), storyLineage(h, h.story.StoryID), `{"intent":"add the flag"}`)
	if err := f.store.SetEpicGoverningArtifact(ctx, f.organizationID, h.epic.EpicID, epicRecord.ArtifactID); err != nil {
		t.Fatalf("point epic: %v", err)
	}
	if err := f.store.SetStoryGoverningArtifact(ctx, f.organizationID, h.story.StoryID, storyRecord.ArtifactID); err != nil {
		t.Fatalf("point story: %v", err)
	}
	group, err := f.store.EnsureWorkGroup(ctx, f.organizationID, h.epic.EpicID)
	if err != nil {
		t.Fatal(err)
	}
	return governed{hierarchy: h, storyRecord: storyRecord, epicRecord: epicRecord, workGroup: group.Record.WorkGroupID}
}

// addPredecessor creates a predecessor Story with an edge into g.story, and
// optionally satisfies it with an accepted completion. The edge and the
// satisfying pointer are planted by fixture: item 10 owns their writer.
func (f *fixture) addPredecessor(t *testing.T, g governed, title string, complete bool) (*store.Story, *store.ManagementArtifact) {
	t.Helper()
	ctx := context.Background()
	predecessor, err := f.store.CreateStory(ctx, store.CreateStoryInput{
		Title: title, OrganizationID: f.organizationID, UserID: f.userID, EpicID: g.epic.EpicID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO story_dependencies
		(organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		f.organizationID, g.product, g.feature.FeatureID, g.epic.EpicID, g.story.StoryID, predecessor.StoryID); err != nil {
		t.Fatal(err)
	}
	if !complete {
		return predecessor, nil
	}
	completion := f.acceptedRecord(t, work.TypeStoryCompletion, storyScope(predecessor.StoryID), storyLineage(g.hierarchy, predecessor.StoryID), `{"head_commit":"`+testSHA+`"}`)
	f.satisfyEdge(t, g, predecessor.StoryID, completion.ArtifactID)
	return predecessor, completion
}

func (f *fixture) satisfyEdge(t *testing.T, g governed, predecessor, completion uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `UPDATE story_dependencies
		SET satisfying_completion_artifact_id = $1
		WHERE organization_id = $2 AND epic_id = $3 AND successor_story_id = $4 AND predecessor_story_id = $5`,
		completion, f.organizationID, g.epic.EpicID, g.story.StoryID, predecessor); err != nil {
		t.Fatal(err)
	}
}

func assertDispatchRejected(t *testing.T, err error, want store.DispatchReason) {
	t.Helper()
	var rejection *store.DispatchRejected
	if !errors.As(err, &rejection) {
		t.Fatalf("want a DispatchRejected with reason %q, got: %v", want, err)
	}
	if rejection.Reason != want {
		t.Fatalf("reason %q, want %q: %v", rejection.Reason, want, err)
	}
	if !errors.Is(err, store.ErrDispatchRejected) {
		t.Fatal("the rejection does not match the sentinel")
	}
}

// TestCreateDispatchDerivesTheWholeBasis: two predecessors, both completed;
// the dispatch carries both version references and both completions, each
// with the id, digest and sequence of the accepted view.
func TestCreateDispatchDerivesTheWholeBasis(t *testing.T) {
	f := newFixture(t)
	g := provisionGoverned(t, f)
	ctx := context.Background()
	p1, c1 := f.addPredecessor(t, g, "one", true)
	p2, c2 := f.addPredecessor(t, g, "two", true)

	dispatch, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
	if err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}
	if dispatch.Disposition != store.DispositionPending || dispatch.WorkGroupID != g.workGroup {
		t.Fatalf("dispatch %+v", dispatch)
	}
	if dispatch.StoryVersion.ArtifactID != g.storyRecord.ArtifactID || dispatch.EpicVersion.ArtifactID != g.epicRecord.ArtifactID {
		t.Fatal("the version references do not name the governing records")
	}
	for _, ref := range []store.VersionRef{dispatch.StoryVersion, dispatch.EpicVersion} {
		base, baseErr := f.store.AmendmentBase(ctx, f.organizationID, ref.ArtifactID)
		if baseErr != nil {
			t.Fatal(baseErr)
		}
		if ref.Digest != base.Digest || ref.Sequence != base.Sequence {
			t.Fatalf("reference %+v does not match the effective base %+v", ref, base)
		}
	}
	if len(dispatch.Basis) != 2 {
		t.Fatalf("basis has %d rows, want 2", len(dispatch.Basis))
	}
	wantCompletion := map[uuid.UUID]uuid.UUID{p1.StoryID: c1.ArtifactID, p2.StoryID: c2.ArtifactID}
	for _, row := range dispatch.Basis {
		if wantCompletion[row.PredecessorStoryID] != row.Completion.ArtifactID {
			t.Fatalf("basis row %+v pairs the wrong completion", row)
		}
		if row.Completion.Digest == "" || len(row.Completion.Digest) != 64 {
			t.Fatalf("completion digest %q", row.Completion.Digest)
		}
	}
	read, err := f.store.GetDispatch(ctx, f.organizationID, dispatch.StoryDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Basis) != 2 {
		t.Fatal("the basis did not round-trip")
	}
}

// TestCreateDispatchRefusesEveryBadReference: one subtest per typed refusal.
func TestCreateDispatchRefusesEveryBadReference(t *testing.T) {
	ctx := context.Background()

	t.Run("not dependency-ready", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		f.addPredecessor(t, g, "pending", false)
		_, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
		assertDispatchRejected(t, err, store.ReasonNotDependencyReady)
		if rows, _ := f.store.ListDispatchesByDisposition(ctx, f.organizationID, store.DispositionPending); len(rows) != 0 {
			t.Fatal("a refused dispatch left a row behind")
		}
	})

	t.Run("no governing pointer", func(t *testing.T) {
		f := newFixture(t)
		h := provisionHierarchy(t, f)
		if _, err := f.store.EnsureWorkGroup(ctx, f.organizationID, h.epic.EpicID); err != nil {
			t.Fatal(err)
		}
		_, err := f.store.CreateDispatch(ctx, f.organizationID, h.story.StoryID)
		assertDispatchRejected(t, err, store.ReasonNoGoverningArtifact)
	})

	t.Run("no work group", func(t *testing.T) {
		f := newFixture(t)
		h := provisionHierarchy(t, f)
		_, err := f.store.CreateDispatch(ctx, f.organizationID, h.story.StoryID)
		assertDispatchRejected(t, err, store.ReasonNoWorkGroup)
	})

	t.Run("draft completion", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		predecessor, _ := f.addPredecessor(t, g, "p", false)
		draft := f.draftRecord(t, work.TypeStoryCompletion, storyScope(predecessor.StoryID), storyLineage(g.hierarchy, predecessor.StoryID), `{"head_commit":"`+testSHA+`"}`)
		f.satisfyEdge(t, g, predecessor.StoryID, draft.ArtifactID)
		_, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
		assertDispatchRejected(t, err, store.ReasonGoverningNotAccepted)
	})

	t.Run("wrong-type completion", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		predecessor, _ := f.addPredecessor(t, g, "p", false)
		// An accepted STORY RECORD scoped to the predecessor: right scope,
		// right status, wrong type. THE MUTANT: skip the type check.
		record := f.acceptedRecord(t, work.TypeStoryRecord, storyScope(predecessor.StoryID), storyLineage(g.hierarchy, predecessor.StoryID), `{"intent":"x"}`)
		f.satisfyEdge(t, g, predecessor.StoryID, record.ArtifactID)
		_, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
		assertDispatchRejected(t, err, store.ReasonGoverningWrongType)
	})

	t.Run("superseded story record", func(t *testing.T) {
		f := newFixture(t)
		g := provisionGoverned(t, f)
		replacement, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
			Payload: json.RawMessage(`{"intent":"v2"}`), Type: work.TypeStoryRecord, Summary: "v2",
			Scope: storyScope(g.story.StoryID), Lineage: storyLineage(g.hierarchy, g.story.StoryID), SupersedesArtifactID: &g.storyRecord.ArtifactID,
			OrganizationID: f.organizationID, UserID: f.userID, AuthorInstanceID: f.author,
		})
		if err != nil {
			t.Fatal(err)
		}
		// Supersession accepts the draft replacement and retires the target
		// in one transition; the replacement must still be a draft.
		review := f.acceptableReview(t, replacement)
		if err := f.store.SupersedeArtifact(ctx, f.organizationID, g.storyRecord.ArtifactID, replacement.ArtifactID, review); err != nil {
			t.Fatalf("supersede: %v", err)
		}
		// The pointer still names the superseded original: not accepted.
		_, err = f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
		assertDispatchRejected(t, err, store.ReasonGoverningNotAccepted)
	})
}

// TestGoverningPointerIsValidatedUnderTheArtifactLock covers the pointer
// writes: wrong type, wrong scope, a draft, an amendment, and another
// tenant's artifact are all refused, and a good one lands.
func TestGoverningPointerIsValidatedUnderTheArtifactLock(t *testing.T) {
	f := newFixture(t)
	h := provisionHierarchy(t, f)
	ctx := context.Background()

	draft := f.draftRecord(t, work.TypeStoryRecord, storyScope(h.story.StoryID), storyLineage(h, h.story.StoryID), `{"intent":"x"}`)
	assertDispatchRejected(t, f.store.SetStoryGoverningArtifact(ctx, f.organizationID, h.story.StoryID, draft.ArtifactID), store.ReasonGoverningNotAccepted)

	epicTyped := f.acceptedRecord(t, work.TypeEpicRecord, storyScope(h.story.StoryID), storyLineage(h, h.story.StoryID), `{"intent":"x","mode":"factory"}`)
	assertDispatchRejected(t, f.store.SetStoryGoverningArtifact(ctx, f.organizationID, h.story.StoryID, epicTyped.ArtifactID), store.ReasonGoverningWrongType)

	other, err := f.store.CreateStory(ctx, store.CreateStoryInput{Title: "other", OrganizationID: f.organizationID, UserID: f.userID, EpicID: h.epic.EpicID})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := f.acceptedRecord(t, work.TypeStoryRecord, storyScope(other.StoryID), storyLineage(h, other.StoryID), `{"intent":"x"}`)
	assertDispatchRejected(t, f.store.SetStoryGoverningArtifact(ctx, f.organizationID, h.story.StoryID, elsewhere.ArtifactID), store.ReasonGoverningWrongScope)

	assertDispatchRejected(t, f.store.SetStoryGoverningArtifact(ctx, f.organizationID, h.story.StoryID, uuid.New()), store.ReasonNoGoverningArtifact)

	good := f.acceptedRecord(t, work.TypeStoryRecord, storyScope(h.story.StoryID), storyLineage(h, h.story.StoryID), `{"intent":"good"}`)
	if err := f.store.SetStoryGoverningArtifact(ctx, f.organizationID, h.story.StoryID, good.ArtifactID); err != nil {
		t.Fatalf("a valid pointer was refused: %v", err)
	}
	story, err := f.store.GetStory(ctx, f.organizationID, h.story.StoryID)
	if err != nil {
		t.Fatal(err)
	}
	if story.GoverningArtifactID == nil || *story.GoverningArtifactID != good.ArtifactID {
		t.Fatalf("pointer = %v, want %s", story.GoverningArtifactID, good.ArtifactID)
	}
}

// TestDispositionTransitionsAreNamedAndTerminal: accept creates the
// execution in the same commit; every transition off a settled dispatch is
// rejected NotPending; a failure needs a code.
func TestDispositionTransitionsAreNamedAndTerminal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	dispatchOf := func(t *testing.T, g governed) *store.StoryDispatch {
		t.Helper()
		d, err := f.store.CreateDispatch(ctx, f.organizationID, g.story.StoryID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	t.Run("accept creates the execution", func(t *testing.T) {
		g := provisionGoverned(t, f)
		d := dispatchOf(t, g)
		execution, err := f.store.AcceptDispatch(ctx, f.organizationID, d.StoryDispatchID)
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		if execution.AuthorityState != store.AuthorityCurrent || execution.StoryID != g.story.StoryID {
			t.Fatalf("execution %+v", execution)
		}
		read, err := f.store.GetExecutionByDispatch(ctx, f.organizationID, d.StoryDispatchID)
		if err != nil || read.ExecutionID != execution.ExecutionID {
			t.Fatalf("execution did not persist with the acceptance: %v", err)
		}
		accepted, _ := f.store.GetDispatch(ctx, f.organizationID, d.StoryDispatchID)
		if accepted.Disposition != store.DispositionAccepted || accepted.SettledAt == nil {
			t.Fatalf("dispatch after accept: %+v", accepted)
		}
		// Terminal: every further transition is refused, and nothing moves.
		if _, err := f.store.AcceptDispatch(ctx, f.organizationID, d.StoryDispatchID); err == nil {
			t.Fatal("a second accept succeeded")
		} else {
			assertDispatchRejected(t, err, store.ReasonNotPending)
		}
		assertDispatchRejected(t, f.store.FailDispatch(ctx, f.organizationID, d.StoryDispatchID, "x", ""), store.ReasonNotPending)
		assertDispatchRejected(t, f.store.InvalidateDispatch(ctx, f.organizationID, d.StoryDispatchID), store.ReasonNotPending)
	})

	t.Run("fail needs a code and is terminal", func(t *testing.T) {
		// A second Epic's worth of hierarchy would collide on slugs; reuse
		// the fixture's second tenant instead of re-provisioning.
		f2 := newFixture(t)
		g := provisionGoverned(t, f2)
		d, err := f2.store.CreateDispatch(ctx, f2.organizationID, g.story.StoryID)
		if err != nil {
			t.Fatal(err)
		}
		assertDispatchRejected(t, f2.store.FailDispatch(ctx, f2.organizationID, d.StoryDispatchID, "  ", "no code"), store.ReasonFailureCodeRequired)
		if err := f2.store.FailDispatch(ctx, f2.organizationID, d.StoryDispatchID, "handshake_refused", "the agent refused"); err != nil {
			t.Fatal(err)
		}
		failed, _ := f2.store.GetDispatch(ctx, f2.organizationID, d.StoryDispatchID)
		if failed.Disposition != store.DispositionFailed || failed.FailureCode == nil || *failed.FailureCode != "handshake_refused" {
			t.Fatalf("after fail: %+v", failed)
		}
		if _, err := f2.store.AcceptDispatch(ctx, f2.organizationID, d.StoryDispatchID); err == nil {
			t.Fatal("a failed dispatch was accepted")
		}
		if _, err := f2.store.GetExecutionByDispatch(ctx, f2.organizationID, d.StoryDispatchID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a failed dispatch has an execution: %v", err)
		}
	})

	t.Run("invalidate is terminal", func(t *testing.T) {
		f3 := newFixture(t)
		g := provisionGoverned(t, f3)
		d, err := f3.store.CreateDispatch(ctx, f3.organizationID, g.story.StoryID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f3.store.InvalidateDispatch(ctx, f3.organizationID, d.StoryDispatchID); err != nil {
			t.Fatal(err)
		}
		assertDispatchRejected(t, f3.store.InvalidateDispatch(ctx, f3.organizationID, d.StoryDispatchID), store.ReasonNotPending)
	})
}
