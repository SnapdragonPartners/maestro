package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/store"
)

// OpenWork reads every open dispatch and its current side under one
// REPEATABLE READ snapshot, locking nothing (design D9).
//
// It does not go through WithTx, which begins at READ COMMITTED: there a
// dispatch read at one statement and a pointer read at the next could come
// from different instants, and the comparison would report a divergence
// that never existed as a state. And it takes no locks, because under
// REPEATABLE READ a FOR UPDATE on a row a concurrent transaction updated
// after the snapshot aborts with 40001 — a read has no reason to pay that.
func (s *Store) OpenWork(ctx context.Context, organizationID uuid.UUID) (store.OpenWork, error) {
	pgxTx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return store.OpenWork{}, fmt.Errorf("begin recovery snapshot: %w", err)
	}
	defer func() { _ = pgxTx.Rollback(ctx) }()

	handle := &tx{queries: s.queries.WithTx(pgxTx), registry: s.registry, keys: s.keys, rootKey: s.rootKey, blob: s.blob}
	work, err := handle.openWork(ctx, organizationID)
	if err != nil {
		return store.OpenWork{}, err
	}
	// Read-only, so the commit changes nothing; it ends the snapshot cleanly.
	if commitErr := pgxTx.Commit(ctx); commitErr != nil {
		return store.OpenWork{}, fmt.Errorf("end recovery snapshot: %w", commitErr)
	}
	return work, nil
}

func (t *tx) openWork(ctx context.Context, organizationID uuid.UUID) (store.OpenWork, error) {
	var out store.OpenWork
	pending, err := t.ListDispatchesByDisposition(ctx, organizationID, store.DispositionPending)
	if err != nil {
		return out, err
	}
	for i := range pending {
		row, rowErr := t.openDispatch(ctx, &pending[i], false)
		if rowErr != nil {
			return out, rowErr
		}
		out.Pending = append(out.Pending, row)
	}
	accepted, err := t.ListDispatchesByDisposition(ctx, organizationID, store.DispositionAccepted)
	if err != nil {
		return out, err
	}
	for i := range accepted {
		row, rowErr := t.openDispatch(ctx, &accepted[i], true)
		if rowErr != nil {
			return out, rowErr
		}
		out.Accepted = append(out.Accepted, row)
	}
	return out, nil
}

// openDispatch assembles one row: the dispatch, its execution when the
// disposition promises one, and the current side of its basis.
func (t *tx) openDispatch(ctx context.Context, dispatch *store.StoryDispatch, withExecution bool) (store.OpenDispatch, error) {
	row := store.OpenDispatch{Dispatch: *dispatch}
	if withExecution {
		execution, err := t.GetExecutionByDispatch(ctx, dispatch.OrganizationID, dispatch.StoryDispatchID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return row, fmt.Errorf("%w: accepted dispatch %s has no execution; the seam commits the two together",
					store.ErrInvariant, dispatch.StoryDispatchID)
			}
			return row, err
		}
		row.Execution = execution
	}
	current, err := t.currentBasis(ctx, dispatch)
	if err != nil {
		return row, err
	}
	row.Current = current
	return row, nil
}

// currentBasis reads the governing pointers and incoming edges as they
// stand, measuring each referenced artifact without locking.
func (t *tx) currentBasis(ctx context.Context, dispatch *store.StoryDispatch) (store.CurrentBasis, error) {
	var current store.CurrentBasis
	story, err := t.GetStory(ctx, dispatch.OrganizationID, dispatch.StoryID)
	if err != nil {
		return current, err
	}
	epic, err := t.GetEpic(ctx, dispatch.OrganizationID, dispatch.EpicID)
	if err != nil {
		return current, err
	}
	if current.StoryVersion, err = t.currentRef(ctx, dispatch.OrganizationID, story.GoverningArtifactID); err != nil {
		return current, err
	}
	if current.EpicVersion, err = t.currentRef(ctx, dispatch.OrganizationID, epic.GoverningArtifactID); err != nil {
		return current, err
	}
	edges, err := t.incomingEdges(ctx, story)
	if err != nil {
		return current, err
	}
	current.Edges = make([]store.CurrentEdge, 0, len(edges))
	for i := range edges {
		completion, refErr := t.currentRef(ctx, dispatch.OrganizationID, edges[i].SatisfyingCompletionArtifactID)
		if refErr != nil {
			return current, refErr
		}
		current.Edges = append(current.Edges, store.CurrentEdge{
			PredecessorStoryID: edges[i].PredecessorStoryID, Completion: completion,
		})
	}
	return current, nil
}

// currentRef measures a pointed-at artifact, or reports an unset pointer as
// nil.
func (t *tx) currentRef(ctx context.Context, organizationID uuid.UUID, artifactID *uuid.UUID) (*store.VersionRef, error) {
	if artifactID == nil {
		return nil, nil //nolint:nilnil // nil IS the answer: the pointer is unset
	}
	base, err := t.EffectiveBase(ctx, organizationID, *artifactID)
	if err != nil {
		return nil, err
	}
	return &store.VersionRef{ArtifactID: *artifactID, Digest: base.Digest, Sequence: base.Sequence}, nil
}
