package orchestrator

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// The recovery projection (design D9): every open row lands in exactly one
// class, by (kind × authority × match), and the plane is never repaired.

// Class is where one open row lands.
type Class string

// The classes. Disjoint because the three factors partition the rows;
// total because every selected row has a kind, every accepted row has an
// authority, and match is a predicate over columns that exist.
const (
	// PendingResumable: awaiting acceptance under a basis still current.
	PendingResumable Class = "pending_resumable"
	// PendingDiverged: ADR 0019's pending-dispatch case, to be invalidated
	// and reissued — item 9's; reported, untouched.
	PendingDiverged Class = "pending_diverged"
	// ExecutionAwaitingBoundary: an accepted dispatch's current execution
	// under a matching basis. Item 3 can create this and nothing in item 3
	// can drive it; it awaits item 5. Item 5 owes OpenWork an extension the
	// moment executions gain terminal or outstanding-action states, or a
	// finished execution would land here.
	ExecutionAwaitingBoundary Class = "execution_awaiting_boundary"
	// ExecutionDiverged: item 9's cancellation input; reported, untouched.
	ExecutionDiverged Class = "execution_diverged"
	// ExecutionSuperseded: item 9's drain state, whatever the basis says.
	// Item 3 cannot produce it.
	ExecutionSuperseded Class = "execution_superseded"
)

// Classes is every class, for tests that must be exhaustive.
//
//nolint:gochecknoglobals // Immutable enumeration.
var Classes = []Class{PendingResumable, PendingDiverged, ExecutionAwaitingBoundary, ExecutionDiverged, ExecutionSuperseded}

// Component names which comparison category diverged (design D9's ten).
type Component string

// The comparison categories, in the order basisMatch checks them.
const (
	StoryID            Component = "story_version.artifact_id"
	StoryDigest        Component = "story_version.digest"
	StorySequence      Component = "story_version.sequence"
	EpicID             Component = "epic_version.artifact_id"
	EpicDigest         Component = "epic_version.digest"
	EpicSequence       Component = "epic_version.sequence"
	EdgeSet            Component = "edges.predecessor_set"
	CompletionID       Component = "completion.artifact_id"
	CompletionDigest   Component = "completion.digest"
	CompletionSequence Component = "completion.sequence"
)

// Divergence is the first mismatch basisMatch found: the component, and for
// a completion the predecessor it belongs to.
type Divergence struct {
	Component   Component
	Detail      string
	Predecessor uuid.UUID
}

func (d Divergence) String() string {
	if d.Predecessor != uuid.Nil {
		return fmt.Sprintf("%s (predecessor %s): %s", d.Component, d.Predecessor, d.Detail)
	}
	return fmt.Sprintf("%s: %s", d.Component, d.Detail)
}

// ProjectedRow is one classified row.
type ProjectedRow struct {
	Divergence *Divergence
	Class      Class
	DispatchID uuid.UUID
	StoryID    uuid.UUID
}

// Projection is the classified open work.
type Projection struct {
	Counts map[Class]int
	Rows   []ProjectedRow
}

// Project classifies every row of OpenWork. An unclassifiable row is an
// error, never a skipped line: a recovery that quietly ignored a row it did
// not understand is how a plane and an Orchestrator start disagreeing.
func Project(open store.OpenWork) (Projection, error) {
	projection := Projection{Counts: map[Class]int{}}
	for _, c := range Classes {
		projection.Counts[c] = 0
	}
	for i := range open.Pending {
		row, err := Classify(&open.Pending[i])
		if err != nil {
			return Projection{}, err
		}
		projection.add(row)
	}
	for i := range open.Accepted {
		row, err := Classify(&open.Accepted[i])
		if err != nil {
			return Projection{}, err
		}
		projection.add(row)
	}
	return projection, nil
}

func (p *Projection) add(row ProjectedRow) {
	p.Rows = append(p.Rows, row)
	p.Counts[row.Class]++
}

// Classify lands one row in exactly one class.
func Classify(row *store.OpenDispatch) (ProjectedRow, error) {
	out := ProjectedRow{DispatchID: row.Dispatch.StoryDispatchID, StoryID: row.Dispatch.StoryID}
	divergence, matches := basisMatch(&row.Dispatch, &row.Current)
	if !matches {
		out.Divergence = &divergence
	}
	switch row.Dispatch.Disposition {
	case store.DispositionPending:
		if row.Execution != nil {
			return out, fmt.Errorf("dispatch %s is pending and carries an execution", row.Dispatch.StoryDispatchID)
		}
		if matches {
			out.Class = PendingResumable
		} else {
			out.Class = PendingDiverged
		}
	case store.DispositionAccepted:
		if row.Execution == nil {
			return out, fmt.Errorf("dispatch %s is accepted and carries no execution", row.Dispatch.StoryDispatchID)
		}
		switch row.Execution.AuthorityState {
		case store.AuthoritySuperseded:
			out.Class = ExecutionSuperseded
		case store.AuthorityCurrent:
			if matches {
				out.Class = ExecutionAwaitingBoundary
			} else {
				out.Class = ExecutionDiverged
			}
		default:
			return out, fmt.Errorf("execution %s has authority %q, which is not in the vocabulary",
				row.Execution.ExecutionID, row.Execution.AuthorityState)
		}
	default:
		return out, fmt.Errorf("dispatch %s has disposition %q, which is not open work",
			row.Dispatch.StoryDispatchID, row.Dispatch.Disposition)
	}
	return out, nil
}

// basisMatch compares a dispatch's snapshot against the current side and
// reports the first component that differs, in a fixed order.
//
// Id AND digest AND sequence, for every reference: id alone misses
// amendments, digest alone misses no-op amendments, digest and sequence
// without id miss a repoint to an identical twin. Edges are compared as a
// set of predecessors, then each completion is compared against the
// completion of the SAME predecessor — pairing is by predecessor, never by
// position or by multiset.
func basisMatch(snapshot *store.StoryDispatch, current *store.CurrentBasis) (Divergence, bool) {
	if d, ok := compareRef(StoryID, StoryDigest, StorySequence, &snapshot.StoryVersion, current.StoryVersion); !ok {
		return d, false
	}
	if d, ok := compareRef(EpicID, EpicDigest, EpicSequence, &snapshot.EpicVersion, current.EpicVersion); !ok {
		return d, false
	}
	// The edge set: every predecessor in the snapshot must be current, and
	// every current predecessor must be in the snapshot.
	snapshotSet := make(map[uuid.UUID]*store.VersionRef, len(snapshot.Basis))
	for i := range snapshot.Basis {
		snapshotSet[snapshot.Basis[i].PredecessorStoryID] = &snapshot.Basis[i].Completion
	}
	currentSet := make(map[uuid.UUID]*store.VersionRef, len(current.Edges))
	for i := range current.Edges {
		currentSet[current.Edges[i].PredecessorStoryID] = current.Edges[i].Completion
	}
	if d, ok := compareSets(snapshotSet, currentSet); !ok {
		return d, false
	}
	// Each completion, paired by predecessor, in a deterministic order.
	predecessors := make([]uuid.UUID, 0, len(snapshotSet))
	for p := range snapshotSet {
		predecessors = append(predecessors, p)
	}
	slices.SortFunc(predecessors, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, p := range predecessors {
		if d, ok := compareRef(CompletionID, CompletionDigest, CompletionSequence, snapshotSet[p], currentSet[p]); !ok {
			d.Predecessor = p
			return d, false
		}
	}
	return Divergence{}, true
}

// compareRef compares one reference's three halves. A nil current side is
// an unset pointer or an unsatisfied edge, and diverges on the id.
func compareRef(idC, digestC, sequenceC Component, snapshot, current *store.VersionRef) (Divergence, bool) {
	if current == nil {
		return Divergence{Component: idC, Detail: fmt.Sprintf("snapshot names %s; current side has no reference", snapshot.ArtifactID)}, false
	}
	switch {
	case snapshot.ArtifactID != current.ArtifactID:
		return Divergence{Component: idC, Detail: fmt.Sprintf("snapshot %s, current %s", snapshot.ArtifactID, current.ArtifactID)}, false
	case snapshot.Digest != current.Digest:
		return Divergence{Component: digestC, Detail: fmt.Sprintf("snapshot %s, current %s", snapshot.Digest, current.Digest)}, false
	case snapshot.Sequence != current.Sequence:
		return Divergence{Component: sequenceC, Detail: fmt.Sprintf("snapshot %d, current %d", snapshot.Sequence, current.Sequence)}, false
	}
	return Divergence{}, true
}

func compareSets(snapshot, current map[uuid.UUID]*store.VersionRef) (Divergence, bool) {
	for p := range snapshot {
		if _, present := current[p]; !present {
			return Divergence{Component: EdgeSet, Predecessor: p, Detail: "predecessor in the snapshot is no longer an edge"}, false
		}
	}
	for p := range current {
		if _, present := snapshot[p]; !present {
			return Divergence{Component: EdgeSet, Predecessor: p, Detail: "predecessor is an edge the snapshot did not have"}, false
		}
	}
	return Divergence{}, true
}
