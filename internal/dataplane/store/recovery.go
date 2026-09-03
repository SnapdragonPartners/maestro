package store

import (
	"context"

	"github.com/google/uuid"
)

// The recovery family (Phase 3 item 3, design D9): the one consistent,
// non-locking read the Orchestrator's recovery projection is built from.

// CurrentEdge is one incoming edge as it stands now: the predecessor, and
// the effective base of the completion that currently satisfies it — nil
// while unsatisfied.
type CurrentEdge struct {
	Completion         *VersionRef
	PredecessorStoryID uuid.UUID
}

// CurrentBasis is the current side of one dispatch's basis, read in the
// same snapshot as the dispatch: the governing pointers' effective bases
// and the incoming edges. A nil base means the pointer is unset.
type CurrentBasis struct {
	StoryVersion *VersionRef
	EpicVersion  *VersionRef
	Edges        []CurrentEdge
}

// OpenDispatch is one open dispatch with everything the projection compares:
// the snapshot it was issued under, its execution if accepted, and the
// current side.
type OpenDispatch struct {
	Execution *Execution
	Current   CurrentBasis
	Dispatch  StoryDispatch
}

// OpenWork is every open dispatch of one organization, read under ONE
// REPEATABLE READ snapshot with no locks: pending dispatches, and accepted
// dispatches joined to their execution regardless of authority state.
// Terminal dispatches are not open work and are not read.
type OpenWork struct {
	Pending  []OpenDispatch
	Accepted []OpenDispatch
}

// Recovery is the projection's read surface. It sits beside Maintenance —
// on Store only, never on Tx — because it opens its own REPEATABLE READ
// transaction: a read of a consistent picture, taken while item 9's
// writers may be moving rows underneath a restart, and one that must not
// lock anything, since a FOR UPDATE under REPEATABLE READ aborts with
// 40001 on any row updated after the snapshot.
type Recovery interface {
	// OpenWork reads every open dispatch and its current side. An accepted
	// dispatch with no execution is an ErrInvariant, not a row: the seam
	// commits the two together, so their disagreement is a defect.
	OpenWork(ctx context.Context, organizationID uuid.UUID) (OpenWork, error)
}
