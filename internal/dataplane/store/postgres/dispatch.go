package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/work"
)

// The dispatch family (design D10).
//
// Lock order, kept by everything in this file: the Epic row first (LockEpic),
// then artifact rows in ascending id (LockManagementArtifact — the SAME lock
// the artifact transitions take). The Epic lock serializes the work graph;
// the artifact lock is what stops an artifact validated as accepted at one
// statement being superseded before the next.

// lockedReference is what one artifact reference looks like once it has been
// locked, validated and measured.
type lockedReference struct {
	ref store.VersionRef
}

// governingRequirement says what a reference must be to enter a basis.
type governingRequirement struct {
	wantType  registry.Type
	what      string
	scopeType store.ScopeType
	scopeID   uuid.UUID
	subject   uuid.UUID
}

func rejectDispatch(operation string, subject uuid.UUID, reason store.DispatchReason, detail string) error {
	return &store.DispatchRejected{Operation: operation, Subject: subject, Reason: reason, Detail: detail}
}

// lockAndValidate takes the artifact's row lock, validates type, status,
// originality and scope UNDER it, and measures the effective view.
//
// AmendmentBase re-takes the same lock; a row lock is re-entrant within the
// transaction that holds it, and reusing the one measurement means there is
// exactly one place that defines "digest and sequence of a reference".
func (t *tx) lockAndValidate(ctx context.Context, operation string, organizationID, artifactID uuid.UUID, req *governingRequirement) (lockedReference, error) {
	var none lockedReference
	row, err := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID: toUUID(artifactID), OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return none, rejectDispatch(operation, req.subject, store.ReasonNoGoverningArtifact,
				fmt.Sprintf("%s names artifact %s, which does not exist in this organization", req.what, artifactID))
		}
		return none, fmt.Errorf("lock %s %s: %w", req.what, artifactID, err)
	}
	switch {
	case row.IsAmendment:
		return none, rejectDispatch(operation, req.subject, store.ReasonGoverningIsAmendment, req.what)
	case registry.Type(row.ArtifactType) != req.wantType:
		return none, rejectDispatch(operation, req.subject, store.ReasonGoverningWrongType,
			fmt.Sprintf("%s is %s, want %s", req.what, row.ArtifactType, req.wantType))
	case store.Status(row.Status) != store.StatusAccepted:
		return none, rejectDispatch(operation, req.subject, store.ReasonGoverningNotAccepted,
			fmt.Sprintf("%s is %s", req.what, row.Status))
	case store.ScopeType(row.ScopeType) != req.scopeType || fromUUID(row.ScopeID) != req.scopeID:
		return none, rejectDispatch(operation, req.subject, store.ReasonGoverningWrongScope,
			fmt.Sprintf("%s is scoped to %s %s, want %s %s", req.what, row.ScopeType, fromUUID(row.ScopeID), req.scopeType, req.scopeID))
	}
	base, err := t.AmendmentBase(ctx, organizationID, artifactID)
	if err != nil {
		return none, fmt.Errorf("measure %s %s: %w", req.what, artifactID, err)
	}
	return lockedReference{ref: store.VersionRef{ArtifactID: artifactID, Digest: base.Digest, Sequence: base.Sequence}}, nil
}

// SetStoryGoverningArtifact points a Story at an accepted work.story_record.
func (t *tx) SetStoryGoverningArtifact(ctx context.Context, organizationID, storyID, artifactID uuid.UUID) error {
	const operation = "SetStoryGoverningArtifact"
	story, err := t.GetStory(ctx, organizationID, storyID)
	if err != nil {
		return err
	}
	if _, lockErr := t.queries.LockEpic(ctx, gen.LockEpicParams{OrganizationID: toUUID(organizationID), EpicID: toUUID(story.EpicID)}); lockErr != nil {
		return fmt.Errorf("lock epic %s: %w", story.EpicID, lockErr)
	}
	if _, validateErr := t.lockAndValidate(ctx, operation, organizationID, artifactID, &governingRequirement{
		wantType: work.TypeStoryRecord, scopeType: store.ScopeStory, scopeID: storyID, subject: storyID, what: "story record",
	}); validateErr != nil {
		return validateErr
	}
	rows, err := t.queries.SetStoryGoverningArtifact(ctx, gen.SetStoryGoverningArtifactParams{
		ArtifactID: toNullUUID(&artifactID), OrganizationID: toUUID(organizationID), StoryID: toUUID(storyID),
	})
	if err != nil {
		return fmt.Errorf("point story %s at %s: %w", storyID, artifactID, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: pointing story %s affected %d rows", store.ErrInvariant, storyID, rows)
	}
	return nil
}

// SetEpicGoverningArtifact points an Epic at an accepted work.epic_record.
func (t *tx) SetEpicGoverningArtifact(ctx context.Context, organizationID, epicID, artifactID uuid.UUID) error {
	const operation = "SetEpicGoverningArtifact"
	if _, err := t.queries.LockEpic(ctx, gen.LockEpicParams{OrganizationID: toUUID(organizationID), EpicID: toUUID(epicID)}); err != nil {
		return notFound(err, "epic", epicID)
	}
	if _, validateErr := t.lockAndValidate(ctx, operation, organizationID, artifactID, &governingRequirement{
		wantType: work.TypeEpicRecord, scopeType: store.ScopeEpic, scopeID: epicID, subject: epicID, what: "epic record",
	}); validateErr != nil {
		return validateErr
	}
	rows, err := t.queries.SetEpicGoverningArtifact(ctx, gen.SetEpicGoverningArtifactParams{
		ArtifactID: toNullUUID(&artifactID), OrganizationID: toUUID(organizationID), EpicID: toUUID(epicID),
	})
	if err != nil {
		return fmt.Errorf("point epic %s at %s: %w", epicID, artifactID, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: pointing epic %s affected %d rows", store.ErrInvariant, epicID, rows)
	}
	return nil
}

// CreateDispatch derives the basis and writes it whole (design D10).
func (t *tx) CreateDispatch(ctx context.Context, organizationID, storyID uuid.UUID) (*store.StoryDispatch, error) {
	const operation = "CreateDispatch"
	story, err := t.GetStory(ctx, organizationID, storyID)
	if err != nil {
		return nil, err
	}
	// 1. The Epic lock: every input below is read under it, and every writer
	//    of those inputs takes it first, so READ COMMITTED suffices.
	epicRow, err := t.queries.LockEpic(ctx, gen.LockEpicParams{OrganizationID: toUUID(organizationID), EpicID: toUUID(story.EpicID)})
	if err != nil {
		return nil, notFound(err, "epic", story.EpicID)
	}
	epic := epicFromRow(&epicRow)
	group, err := t.GetWorkGroupByEpic(ctx, organizationID, story.EpicID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, rejectDispatch(operation, storyID, store.ReasonNoWorkGroup, epic.EpicID.String())
		}
		return nil, err
	}
	// 2. The references the basis is made of, each with what it must be.
	edges, requirements, err := t.basisRequirements(ctx, operation, story, &epic)
	if err != nil {
		return nil, err
	}
	// 3. Every referenced artifact, locked and validated in ascending id
	//    order, each against its own requirement.
	measured, err := t.lockAndMeasureAll(ctx, operation, organizationID, requirements)
	if err != nil {
		return nil, err
	}
	// 4. Write the dispatch and its complete basis, in this transaction.
	return t.writeDispatch(ctx, story, &epic, group.WorkGroupID, edges, measured)
}

// basisRequirements reads the inputs under the Epic lock and says what each
// referenced artifact must be. Any unsatisfied edge or missing pointer is a
// typed refusal before anything is locked.
func (t *tx) basisRequirements(ctx context.Context, operation string, story *store.Story, epic *store.Epic) ([]store.StoryDependency, map[uuid.UUID]*governingRequirement, error) {
	edges, err := t.incomingEdges(ctx, story)
	if err != nil {
		return nil, nil, err
	}
	for i := range edges {
		if edges[i].SatisfyingCompletionArtifactID == nil {
			return nil, nil, rejectDispatch(operation, story.StoryID, store.ReasonNotDependencyReady, edges[i].PredecessorStoryID.String())
		}
	}
	if story.GoverningArtifactID == nil {
		return nil, nil, rejectDispatch(operation, story.StoryID, store.ReasonNoGoverningArtifact, "story")
	}
	if epic.GoverningArtifactID == nil {
		return nil, nil, rejectDispatch(operation, story.StoryID, store.ReasonNoGoverningArtifact, "epic")
	}
	requirements := map[uuid.UUID]*governingRequirement{
		*story.GoverningArtifactID: {wantType: work.TypeStoryRecord, scopeType: store.ScopeStory, scopeID: story.StoryID, subject: story.StoryID, what: "story record"},
		*epic.GoverningArtifactID:  {wantType: work.TypeEpicRecord, scopeType: store.ScopeEpic, scopeID: epic.EpicID, subject: story.StoryID, what: "epic record"},
	}
	for i := range edges {
		requirements[*edges[i].SatisfyingCompletionArtifactID] = &governingRequirement{
			wantType: work.TypeStoryCompletion, scopeType: store.ScopeStory, scopeID: edges[i].PredecessorStoryID,
			subject: story.StoryID, what: "completion of predecessor " + edges[i].PredecessorStoryID.String(),
		}
	}
	return edges, requirements, nil
}

// lockAndMeasureAll locks every artifact in ascending id order — the lock
// order design D10 fixes — validating and measuring each under its lock.
func (t *tx) lockAndMeasureAll(ctx context.Context, operation string, organizationID uuid.UUID, requirements map[uuid.UUID]*governingRequirement) (map[uuid.UUID]store.VersionRef, error) {
	ids := make([]uuid.UUID, 0, len(requirements))
	for id := range requirements {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	measured := make(map[uuid.UUID]store.VersionRef, len(ids))
	for _, id := range ids {
		locked, err := t.lockAndValidate(ctx, operation, organizationID, id, requirements[id])
		if err != nil {
			return nil, err
		}
		measured[id] = locked.ref
	}
	return measured, nil
}

// writeDispatch inserts the dispatch row, its two version references and
// every basis row, in the caller's transaction.
func (t *tx) writeDispatch(ctx context.Context, story *store.Story, epic *store.Epic, workGroupID uuid.UUID, edges []store.StoryDependency, measured map[uuid.UUID]store.VersionRef) (*store.StoryDispatch, error) {
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	storyRef := measured[*story.GoverningArtifactID]
	epicRef := measured[*epic.GoverningArtifactID]
	row, err := t.queries.InsertStoryDispatch(ctx, gen.InsertStoryDispatchParams{
		StoryDispatchID: toUUID(identifier), OrganizationID: toUUID(story.OrganizationID),
		ProductID: toUUID(story.ProductID), FeatureID: toUUID(story.FeatureID), EpicID: toUUID(story.EpicID),
		StoryID: toUUID(story.StoryID), WorkGroupID: toUUID(workGroupID),
		StoryVersionArtifactID: toUUID(storyRef.ArtifactID), StoryVersionEffectiveDigest: storyRef.Digest,
		StoryVersionEffectiveSequence: int32(storyRef.Sequence), //nolint:gosec // sequences are small
		EpicVersionArtifactID:         toUUID(epicRef.ArtifactID), EpicVersionEffectiveDigest: epicRef.Digest,
		EpicVersionEffectiveSequence: int32(epicRef.Sequence), //nolint:gosec // sequences are small
	})
	if err != nil {
		return nil, fmt.Errorf("insert dispatch of story %s: %w", story.StoryID, err)
	}
	for i := range edges {
		ref := measured[*edges[i].SatisfyingCompletionArtifactID]
		if insertErr := t.queries.InsertDispatchBasisDependency(ctx, gen.InsertDispatchBasisDependencyParams{
			StoryDispatchID: row.StoryDispatchID, OrganizationID: row.OrganizationID,
			ProductID: row.ProductID, FeatureID: row.FeatureID, EpicID: row.EpicID,
			PredecessorStoryID:   toUUID(edges[i].PredecessorStoryID),
			CompletionArtifactID: toUUID(ref.ArtifactID), CompletionEffectiveDigest: ref.Digest,
			CompletionEffectiveSequence: int32(ref.Sequence), //nolint:gosec // sequences are small
		}); insertErr != nil {
			return nil, fmt.Errorf("insert basis row for predecessor %s: %w", edges[i].PredecessorStoryID, insertErr)
		}
	}
	return t.dispatchWithBasis(ctx, &row)
}

func (t *tx) dispatchWithBasis(ctx context.Context, row *gen.StoryDispatch) (*store.StoryDispatch, error) {
	basisRows, err := t.queries.ListDispatchBasisDependencies(ctx, gen.ListDispatchBasisDependenciesParams{
		StoryDispatchID: row.StoryDispatchID, OrganizationID: row.OrganizationID,
	})
	if err != nil {
		return nil, fmt.Errorf("list basis of dispatch %s: %w", fromUUID(row.StoryDispatchID), err)
	}
	basis := make([]store.BasisDependency, 0, len(basisRows))
	for i := range basisRows {
		basis = append(basis, store.BasisDependency{
			PredecessorStoryID: fromUUID(basisRows[i].PredecessorStoryID),
			Completion: store.VersionRef{
				ArtifactID: fromUUID(basisRows[i].CompletionArtifactID),
				Digest:     basisRows[i].CompletionEffectiveDigest,
				Sequence:   int(basisRows[i].CompletionEffectiveSequence),
			},
		})
	}
	dispatch := dispatchFromRow(row, basis)
	return &dispatch, nil
}

func dispatchFromRow(row *gen.StoryDispatch, basis []store.BasisDependency) store.StoryDispatch {
	return store.StoryDispatch{
		SettledAt: fromNullTimestamptz(row.SettledAt), FailureCode: row.FailureCode, FailureDetail: row.FailureDetail,
		Basis: basis, DispatchedAt: fromTimestamptz(row.DispatchedAt), Disposition: store.Disposition(row.Disposition),
		StoryVersion:    store.VersionRef{ArtifactID: fromUUID(row.StoryVersionArtifactID), Digest: row.StoryVersionEffectiveDigest, Sequence: int(row.StoryVersionEffectiveSequence)},
		EpicVersion:     store.VersionRef{ArtifactID: fromUUID(row.EpicVersionArtifactID), Digest: row.EpicVersionEffectiveDigest, Sequence: int(row.EpicVersionEffectiveSequence)},
		StoryDispatchID: fromUUID(row.StoryDispatchID), OrganizationID: fromUUID(row.OrganizationID),
		ProductID: fromUUID(row.ProductID), FeatureID: fromUUID(row.FeatureID), EpicID: fromUUID(row.EpicID),
		StoryID: fromUUID(row.StoryID), WorkGroupID: fromUUID(row.WorkGroupID),
	}
}

func executionFromRow(row *gen.Execution) store.Execution {
	return store.Execution{
		AdmissionClosedAt: fromNullTimestamptz(row.AdmissionClosedAt), CreatedAt: fromTimestamptz(row.CreatedAt),
		AuthorityState: store.AuthorityState(row.AuthorityState),
		ExecutionID:    fromUUID(row.ExecutionID), OrganizationID: fromUUID(row.OrganizationID),
		ProductID: fromUUID(row.ProductID), FeatureID: fromUUID(row.FeatureID), EpicID: fromUUID(row.EpicID),
		StoryID: fromUUID(row.StoryID), StoryDispatchID: fromUUID(row.StoryDispatchID),
	}
}

func (t *tx) GetDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) (*store.StoryDispatch, error) {
	row, err := t.queries.GetStoryDispatch(ctx, gen.GetStoryDispatchParams{OrganizationID: toUUID(organizationID), StoryDispatchID: toUUID(dispatchID)})
	if err != nil {
		return nil, notFound(err, "dispatch", dispatchID)
	}
	return t.dispatchWithBasis(ctx, &row)
}

func (t *tx) ListDispatchesByDisposition(ctx context.Context, organizationID uuid.UUID, disposition store.Disposition) ([]store.StoryDispatch, error) {
	rows, err := t.queries.ListStoryDispatchesByDisposition(ctx, gen.ListStoryDispatchesByDispositionParams{
		OrganizationID: toUUID(organizationID), Disposition: string(disposition),
	})
	if err != nil {
		return nil, fmt.Errorf("list %s dispatches: %w", disposition, err)
	}
	dispatches := make([]store.StoryDispatch, 0, len(rows))
	for i := range rows {
		dispatch, err := t.dispatchWithBasis(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		dispatches = append(dispatches, *dispatch)
	}
	return dispatches, nil
}

func (t *tx) GetExecutionByDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) (*store.Execution, error) {
	row, err := t.queries.GetExecutionByDispatch(ctx, gen.GetExecutionByDispatchParams{OrganizationID: toUUID(organizationID), StoryDispatchID: toUUID(dispatchID)})
	if err != nil {
		return nil, notFound(err, "execution of dispatch", dispatchID)
	}
	execution := executionFromRow(&row)
	return &execution, nil
}

// transition runs one named conditional update and classifies a zero row
// count as a rejected transition, distinguishing "not pending" from "not
// there" by reading the row.
func (t *tx) transition(ctx context.Context, operation string, organizationID, dispatchID uuid.UUID, rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("%s %s: %w", operation, dispatchID, err)
	}
	if rows == 1 {
		return nil
	}
	current, readErr := t.GetDispatch(ctx, organizationID, dispatchID)
	if readErr != nil {
		return readErr
	}
	return rejectDispatch(operation, dispatchID, store.ReasonNotPending, "disposition is "+string(current.Disposition))
}

// AcceptDispatch flips pending → accepted and creates the execution.
func (t *tx) AcceptDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) (*store.Execution, error) {
	const operation = "AcceptDispatch"
	rows, execErr := t.queries.AcceptStoryDispatch(ctx, gen.AcceptStoryDispatchParams{OrganizationID: toUUID(organizationID), StoryDispatchID: toUUID(dispatchID)})
	if err := t.transition(ctx, operation, organizationID, dispatchID, rows, execErr); err != nil {
		return nil, err
	}
	dispatch, err := t.GetDispatch(ctx, organizationID, dispatchID)
	if err != nil {
		return nil, err
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	row, err := t.queries.InsertExecution(ctx, gen.InsertExecutionParams{
		ExecutionID: toUUID(identifier), OrganizationID: toUUID(organizationID),
		ProductID: toUUID(dispatch.ProductID), FeatureID: toUUID(dispatch.FeatureID), EpicID: toUUID(dispatch.EpicID),
		StoryID: toUUID(dispatch.StoryID), StoryDispatchID: toUUID(dispatchID),
	})
	if err != nil {
		return nil, fmt.Errorf("create execution for dispatch %s: %w", dispatchID, err)
	}
	execution := executionFromRow(&row)
	return &execution, nil
}

// FailDispatch flips pending → failed with a stable code.
func (t *tx) FailDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID, failureCode, failureDetail string) error {
	const operation = "FailDispatch"
	if strings.TrimSpace(failureCode) == "" {
		return rejectDispatch(operation, dispatchID, store.ReasonFailureCodeRequired, "")
	}
	var detail *string
	if failureDetail != "" {
		detail = &failureDetail
	}
	rows, err := t.queries.FailStoryDispatch(ctx, gen.FailStoryDispatchParams{
		OrganizationID: toUUID(organizationID), StoryDispatchID: toUUID(dispatchID), FailureCode: &failureCode, FailureDetail: detail,
	})
	return t.transition(ctx, operation, organizationID, dispatchID, rows, err)
}

// InvalidateDispatch flips pending → invalidated.
func (t *tx) InvalidateDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) error {
	rows, err := t.queries.InvalidateStoryDispatch(ctx, gen.InvalidateStoryDispatchParams{OrganizationID: toUUID(organizationID), StoryDispatchID: toUUID(dispatchID)})
	return t.transition(ctx, "InvalidateDispatch", organizationID, dispatchID, rows, err)
}
