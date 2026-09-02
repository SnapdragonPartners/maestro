package orchestrator

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

func ref(id uuid.UUID, digest string, seq int) store.VersionRef {
	return store.VersionRef{ArtifactID: id, Digest: digest, Sequence: seq}
}

// matching builds a snapshot and a current side that agree on every
// comparison category, with two predecessors.
func matching() (store.StoryDispatch, store.CurrentBasis) {
	story, epic := uuid.New(), uuid.New()
	p1, p2 := uuid.New(), uuid.New()
	c1, c2 := uuid.New(), uuid.New()
	snapshot := store.StoryDispatch{
		Disposition:  store.DispositionPending,
		StoryVersion: ref(story, strings.Repeat("a", 64), 0),
		EpicVersion:  ref(epic, strings.Repeat("b", 64), 1),
		Basis: []store.BasisDependency{
			{PredecessorStoryID: p1, Completion: ref(c1, strings.Repeat("c", 64), 0)},
			{PredecessorStoryID: p2, Completion: ref(c2, strings.Repeat("d", 64), 2)},
		},
	}
	sv, ev := snapshot.StoryVersion, snapshot.EpicVersion
	cc1, cc2 := snapshot.Basis[0].Completion, snapshot.Basis[1].Completion
	current := store.CurrentBasis{
		StoryVersion: &sv, EpicVersion: &ev,
		Edges: []store.CurrentEdge{{PredecessorStoryID: p1, Completion: &cc1}, {PredecessorStoryID: p2, Completion: &cc2}},
	}
	return snapshot, current
}

// TestBasisMatchSeesEveryComparisonCategory: ten fixtures, each differing
// in exactly one category, each returning exactly that category.
// Deleting that category's comparison in basisMatch fails exactly that
// fixture — which is the mutant per row.
func TestBasisMatchSeesEveryComparisonCategory(t *testing.T) {
	cases := []struct {
		name string
		want Component
		mut  func(*store.CurrentBasis)
	}{
		{"story id", StoryID, func(c *store.CurrentBasis) {
			c.StoryVersion.ArtifactID = uuid.New()
		}},
		{"story digest", StoryDigest, func(c *store.CurrentBasis) {
			c.StoryVersion.Digest = strings.Repeat("9", 64)
		}},
		{"story sequence", StorySequence, func(c *store.CurrentBasis) { c.StoryVersion.Sequence++ }},
		{"epic id", EpicID, func(c *store.CurrentBasis) {
			c.EpicVersion.ArtifactID = uuid.New()
		}},
		{"epic digest", EpicDigest, func(c *store.CurrentBasis) {
			c.EpicVersion.Digest = strings.Repeat("8", 64)
		}},
		{"epic sequence", EpicSequence, func(c *store.CurrentBasis) { c.EpicVersion.Sequence++ }},
		{"edge added", EdgeSet, func(c *store.CurrentBasis) {
			extra := ref(uuid.New(), strings.Repeat("e", 64), 0)
			c.Edges = append(c.Edges, store.CurrentEdge{PredecessorStoryID: uuid.New(), Completion: &extra})
		}},
		{"edge removed", EdgeSet, func(c *store.CurrentBasis) { c.Edges = c.Edges[:1] }},
		{"completion id", CompletionID, func(c *store.CurrentBasis) {
			c.Edges[1].Completion.ArtifactID = uuid.New()
		}},
		{"completion digest", CompletionDigest, func(c *store.CurrentBasis) {
			c.Edges[1].Completion.Digest = strings.Repeat("7", 64)
		}},
		{"completion sequence", CompletionSequence, func(c *store.CurrentBasis) { c.Edges[1].Completion.Sequence++ }},
		{"unset story pointer", StoryID, func(c *store.CurrentBasis) { c.StoryVersion = nil }},
		{"unsatisfied edge", CompletionID, func(c *store.CurrentBasis) { c.Edges[0].Completion = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, current := matching()
			if d, ok := basisMatch(&snapshot, &current); !ok {
				t.Fatalf("the control fixture diverges: %s", d)
			}
			tc.mut(&current)
			d, ok := basisMatch(&snapshot, &current)
			if ok {
				t.Fatalf("a change in %s was not seen", tc.want)
			}
			if d.Component != tc.want {
				t.Fatalf("diverged on %s, want %s: %s", d.Component, tc.want, d)
			}
			if strings.HasPrefix(string(tc.want), "completion") && d.Predecessor == uuid.Nil {
				t.Fatal("a completion divergence does not name its predecessor")
			}
		})
	}
}

// TestBasisMatchPairsCompletionsByPredecessor is design D9's pairing
// property, tested where the values are unconstrained: equal completion
// MULTISETS assigned to opposite predecessors, no amendment. A
// pairing-blind comparator returns match; the correct one returns
// diverged. The assertion is on the verdict, not on which edge is named.
func TestBasisMatchPairsCompletionsByPredecessor(t *testing.T) {
	snapshot, current := matching()
	// Swap the associations on the current side only.
	current.Edges[0].Completion, current.Edges[1].Completion = current.Edges[1].Completion, current.Edges[0].Completion
	d, ok := basisMatch(&snapshot, &current)
	if ok {
		t.Fatal("cross-assigned completions with equal multisets matched: the comparator is pairing-blind")
	}
	if d.Component != CompletionID {
		t.Fatalf("diverged on %s, want a completion id", d.Component)
	}
}

// TestClassifyIsDisjointAndTotal: every producible combination lands in
// exactly one class, and the two malformed rows are errors, not classes.
func TestClassifyIsDisjointAndTotal(t *testing.T) {
	snapshot, current := matching()
	execution := func(authority store.AuthorityState) *store.Execution {
		return &store.Execution{ExecutionID: uuid.New(), AuthorityState: authority}
	}
	// diverged returns a COPY whose story sequence moved; the pointer must
	// not be shared with the control fixture, or the table would mutate it.
	diverged := func(c store.CurrentBasis) store.CurrentBasis {
		moved := *c.StoryVersion
		moved.Sequence++
		c.StoryVersion = &moved
		return c
	}
	accepted := snapshot
	accepted.Disposition = store.DispositionAccepted

	cases := []struct {
		name     string
		row      store.OpenDispatch
		want     Class
		diverged bool
	}{
		{"pending match", store.OpenDispatch{Dispatch: snapshot, Current: current}, PendingResumable, false},
		{"pending diverged", store.OpenDispatch{Dispatch: snapshot, Current: diverged(current)}, PendingDiverged, true},
		{"accepted current match", store.OpenDispatch{Dispatch: accepted, Current: current, Execution: execution(store.AuthorityCurrent)}, ExecutionAwaitingBoundary, false},
		{"accepted current diverged", store.OpenDispatch{Dispatch: accepted, Current: diverged(current), Execution: execution(store.AuthorityCurrent)}, ExecutionDiverged, true},
		{"accepted superseded match", store.OpenDispatch{Dispatch: accepted, Current: current, Execution: execution(store.AuthoritySuperseded)}, ExecutionSuperseded, false},
		{"accepted superseded diverged", store.OpenDispatch{Dispatch: accepted, Current: diverged(current), Execution: execution(store.AuthoritySuperseded)}, ExecutionSuperseded, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := tc.row
			got, err := Classify(&row)
			if err != nil {
				t.Fatal(err)
			}
			if got.Class != tc.want {
				t.Fatalf("class %s, want %s", got.Class, tc.want)
			}
			if (got.Divergence != nil) != tc.diverged {
				t.Fatalf("divergence %v, want diverged=%v", got.Divergence, tc.diverged)
			}
		})
	}

	// THE MUTANT: return a class instead of an error for these two.
	for name, row := range map[string]store.OpenDispatch{
		"accepted without execution": {Dispatch: accepted, Current: current},
		"pending with execution":     {Dispatch: snapshot, Current: current, Execution: execution(store.AuthorityCurrent)},
		"unknown authority":          {Dispatch: accepted, Current: current, Execution: execution("limbo")},
		"terminal disposition":       {Dispatch: func() store.StoryDispatch { d := snapshot; d.Disposition = store.DispositionFailed; return d }(), Current: current},
	} {
		t.Run(name, func(t *testing.T) {
			r := row
			if _, err := Classify(&r); err == nil {
				t.Fatal("an unclassifiable row was given a class instead of an error")
			}
		})
	}
}

func TestProjectCountsEveryClassAndStopsOnAnError(t *testing.T) {
	snapshot, current := matching()
	accepted := snapshot
	accepted.Disposition = store.DispositionAccepted
	open := store.OpenWork{
		Pending:  []store.OpenDispatch{{Dispatch: snapshot, Current: current}},
		Accepted: []store.OpenDispatch{{Dispatch: accepted, Current: current, Execution: &store.Execution{AuthorityState: store.AuthorityCurrent}}},
	}
	p, err := Project(open)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 2 || p.Counts[PendingResumable] != 1 || p.Counts[ExecutionAwaitingBoundary] != 1 {
		t.Fatalf("projection %+v", p)
	}
	for _, c := range Classes {
		if _, present := p.Counts[c]; !present {
			t.Fatalf("class %s has no count; an absent class must read as zero, not missing", c)
		}
	}
	open.Accepted[0].Execution = nil
	if _, err := Project(open); err == nil {
		t.Fatal("a projection over an unclassifiable row succeeded")
	}
}

func TestRefuseClassifiesOnlyReadinessFailures(t *testing.T) {
	plain := errors.New("disk on fire")
	var refused *StartupRefused
	if errors.As(refuse(plain), &refused) {
		t.Fatal("an error with no readiness cause was dressed as a startup refusal")
	}
}
