package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/canonical"
	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/mergepatch"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
)

// digestPattern matches the schema's own digest format check, so the seam
// refuses a malformed digest before Postgres has to.
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Transition names, used in refusals and invariant failures so an operator
// reading a log sees the same word the design's matrix uses.
const (
	transitionAccept          = "accept"
	transitionAcceptAmendment = "accept amendment"
	transitionInvalidate      = "invalidate"
	transitionSupersede       = "supersede"
	transitionArchive         = "archive"
)

// --- row mapping -----------------------------------------------------------

func managementFromRow(row *gen.ManagementArtifact) store.ManagementArtifact {
	return store.ManagementArtifact{
		AcceptedAt:           fromNullTimestamptz(row.AcceptedAt),
		ReviewerInstanceID:   fromNullUUID(row.ReviewerInstanceID),
		ProducedByToolCallID: fromNullUUID(row.ProducedByToolCallID),
		AmendsArtifactID:     fromNullUUID(row.AmendsArtifactID),
		SupersedesArtifactID: fromNullUUID(row.SupersedesArtifactID),
		ReplacesArtifactID:   fromNullUUID(row.ReplacesArtifactID),
		AmendmentSequence:    fromNullInt32(row.AmendmentSequence),

		Payload: json.RawMessage(row.Payload),

		Type:          registry.Type(row.ArtifactType),
		Category:      registry.Category(row.ArtifactCategory),
		Status:        store.Status(row.Status),
		Summary:       row.Summary,
		PayloadDigest: row.PayloadDigest,
		ReviewDigest:  row.ReviewDigest,

		Scope: store.Scope{
			Type: store.ScopeType(row.ScopeType),
			ID:   fromUUID(row.ScopeID),
		},
		Lineage: store.Lineage{
			ProductID: fromNullUUID(row.ProductID),
			FeatureID: fromNullUUID(row.FeatureID),
			EpicID:    fromNullUUID(row.EpicID),
			StoryID:   fromNullUUID(row.StoryID),
		},

		ArtifactID:       fromUUID(row.ArtifactID),
		OrganizationID:   fromUUID(row.OrganizationID),
		UserID:           fromUUID(row.UserID),
		AuthorInstanceID: fromUUID(row.AuthorInstanceID),

		CreatedAt:     fromTimestamptz(row.CreatedAt),
		SchemaVersion: int(row.SchemaVersion),
		IsAmendment:   row.IsAmendment,
	}
}

func auditFromRow(row *gen.AuditArtifact) store.AuditArtifact {
	return store.AuditArtifact{
		UserID:               fromNullUUID(row.UserID),
		ProducedByToolCallID: fromNullUUID(row.ProducedByToolCallID),

		Payload: json.RawMessage(row.Payload),

		Type:          registry.Type(row.ArtifactType),
		Category:      registry.Category(row.ArtifactCategory),
		Summary:       row.Summary,
		PayloadDigest: row.PayloadDigest,

		Scope: store.Scope{
			Type: store.ScopeType(row.ScopeType),
			ID:   fromUUID(row.ScopeID),
		},
		Lineage: store.Lineage{
			ProductID: fromNullUUID(row.ProductID),
			FeatureID: fromNullUUID(row.FeatureID),
			EpicID:    fromNullUUID(row.EpicID),
			StoryID:   fromNullUUID(row.StoryID),
		},

		ArtifactID:       fromUUID(row.ArtifactID),
		OrganizationID:   fromUUID(row.OrganizationID),
		AuthorInstanceID: fromUUID(row.AuthorInstanceID),

		CreatedAt:     fromTimestamptz(row.CreatedAt),
		SchemaVersion: int(row.SchemaVersion),
	}
}

func reviewFromRow(row *gen.ArtifactReview) store.Review {
	return store.Review{
		BaseDigest:   fromNullString(row.BaseDigest),
		BaseSequence: fromNullInt32(row.BaseSequence),

		ReviewDigest: row.ReviewDigest,
		Decision:     store.Decision(row.Decision),
		Rationale:    row.Rationale,

		ReviewID:           fromUUID(row.ReviewID),
		OrganizationID:     fromUUID(row.OrganizationID),
		ArtifactID:         fromUUID(row.ArtifactID),
		ReviewerInstanceID: fromUUID(row.ReviewerInstanceID),

		DecidedAt: fromTimestamptz(row.DecidedAt),
	}
}

// --- digests ---------------------------------------------------------------

// reviewableProjection is what ADR 0028 §5 binds a review to: the whole
// reviewable content, not the payload alone. Summary and type are part of
// what a reviewer reads, so a change to either must invalidate the review.
// Field ORDER here is chosen for alignment and does not affect the digest:
// JCS sorts object keys, so the canonical form is identical whatever order
// encoding/json emits them in. That independence is the reason ADR 0028
// specifies a canonicalization at all.
type reviewableProjection struct {
	Type          registry.Type   `json:"artifact_type"`
	Summary       string          `json:"summary"`
	Payload       json.RawMessage `json:"payload"`
	SchemaVersion int             `json:"schema_version"`
}

func computeDigests(artifactType registry.Type, version int, summary string, payload json.RawMessage) (payloadDigest, reviewDigest string, err error) {
	payloadDigest, err = canonical.DigestJSON(payload)
	if err != nil {
		return "", "", fmt.Errorf("payload digest: %w", err)
	}
	reviewDigest, err = canonical.Digest(reviewableProjection{
		Type:          artifactType,
		SchemaVersion: version,
		Summary:       summary,
		Payload:       payload,
	})
	if err != nil {
		return "", "", fmt.Errorf("review digest: %w", err)
	}
	return payloadDigest, reviewDigest, nil
}

// --- creation --------------------------------------------------------------

// after the call begins. One struct copy per artifact write is not worth trading that for.
//
//nolint:gocritic // hugeParam: the seam takes inputs by value so a caller cannot mutate one
func (t *tx) CreateManagementArtifact(ctx context.Context, input store.CreateManagementArtifactInput) (*store.ManagementArtifact, error) {
	artifactType := input.Type
	category := registry.CategoryManagement
	version := 0

	if input.AmendsArtifactID == nil {
		// An original takes type, category and version from the registry.
		registration, err := t.registry.Lookup(artifactType)
		if err != nil {
			return nil, fmt.Errorf("management artifact write: %w", err)
		}
		if registration.Category != registry.CategoryManagement {
			return nil, fmt.Errorf("type %q is registered as %q, so it cannot be written as a Management artifact",
				artifactType, registration.Category)
		}
		version = registration.CurrentVersion
	} else {
		// An amendment takes all three from the TARGET ORIGINAL (design
		// D3). ADR 0028 forbids an amendment changing type or version, and
		// stored payloads are never rewritten, so an artifact created at v1
		// stays v1 for life. Taking the registry's current version would
		// stamp this amendment v2 once the registry advanced and validate
		// the merged result against a schema its payload was never written
		// for.
		original, err := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
			ArtifactID:     toUUID(*input.AmendsArtifactID),
			OrganizationID: toUUID(input.OrganizationID),
		})
		if err != nil {
			return nil, notFound(err, "amendment target", *input.AmendsArtifactID)
		}
		if original.IsAmendment {
			return nil, fmt.Errorf("artifact %s is itself an amendment; the amendment chain is flat (ADR 0021)",
				*input.AmendsArtifactID)
		}
		artifactType = registry.Type(original.ArtifactType)
		category = registry.Category(original.ArtifactCategory)
		version = int(original.SchemaVersion)
	}

	// Validate the instance that will be STORED. For an original that is
	// the payload; for an amendment the patch is stored but the MERGED
	// result is what must satisfy the schema, so it is validated below and
	// again at acceptance.
	if input.AmendsArtifactID == nil {
		if err := t.validatePayload(artifactType, version, input.Payload); err != nil {
			return nil, err
		}
	} else {
		merged, err := t.effectiveViewWithPatch(ctx, input.OrganizationID, *input.AmendsArtifactID, input.Payload)
		if err != nil {
			return nil, err
		}
		if err := t.validatePayload(artifactType, version, merged); err != nil {
			return nil, fmt.Errorf("merged effective payload is invalid: %w", err)
		}
	}

	arc, err := scopeColumns(input.Scope)
	if err != nil {
		return nil, err
	}

	payloadDigest, reviewDigest, err := computeDigests(artifactType, version, input.Summary, input.Payload)
	if err != nil {
		return nil, err
	}

	row, err := t.queries.CreateManagementArtifact(ctx, gen.CreateManagementArtifactParams{
		ArtifactID:       toUUID(uuid.New()),
		OrganizationID:   toUUID(input.OrganizationID),
		UserID:           toUUID(input.UserID),
		ArtifactType:     string(artifactType),
		ArtifactCategory: string(category),
		ScopeType:        string(input.Scope.Type),

		ScopeOrganizationID: toNullUUID(arc.organizationID),
		ScopeProductID:      toNullUUID(arc.productID),
		ScopeFeatureID:      toNullUUID(arc.featureID),
		ScopeEpicID:         toNullUUID(arc.epicID),
		ScopeStoryID:        toNullUUID(arc.storyID),

		ProductID: toNullUUID(input.Lineage.ProductID),
		FeatureID: toNullUUID(input.Lineage.FeatureID),
		EpicID:    toNullUUID(input.Lineage.EpicID),
		StoryID:   toNullUUID(input.Lineage.StoryID),

		AuthorInstanceID:     toUUID(input.AuthorInstanceID),
		ProducedByToolCallID: toNullUUID(input.ProducedByToolCallID),

		AmendsArtifactID:     toNullUUID(input.AmendsArtifactID),
		SupersedesArtifactID: toNullUUID(input.SupersedesArtifactID),
		ReplacesArtifactID:   toNullUUID(input.ReplacesArtifactID),

		SchemaVersion: int32(version), //nolint:gosec // schema versions are small
		Summary:       input.Summary,
		Payload:       input.Payload,
		PayloadDigest: payloadDigest,
		ReviewDigest:  reviewDigest,
	})
	if err != nil {
		return nil, fmt.Errorf("create management artifact: %w", err)
	}
	created := managementFromRow(&row)
	return &created, nil
}

// after the call begins. One struct copy per artifact write is not worth trading that for.
//
//nolint:gocritic // hugeParam: the seam takes inputs by value so a caller cannot mutate one
func (t *tx) CreateAuditArtifact(ctx context.Context, input store.CreateAuditArtifactInput) (*store.AuditArtifact, error) {
	registration, err := t.registry.Lookup(input.Type)
	if err != nil {
		return nil, fmt.Errorf("audit artifact write: %w", err)
	}
	if registration.Category != registry.CategoryAudit {
		return nil, fmt.Errorf("type %q is registered as %q, so it cannot be written as an Audit artifact",
			input.Type, registration.Category)
	}
	if validationErr := t.validatePayload(input.Type, registration.CurrentVersion, input.Payload); validationErr != nil {
		return nil, validationErr
	}

	arc, err := scopeColumns(input.Scope)
	if err != nil {
		return nil, err
	}
	payloadDigest, err := canonical.DigestJSON(input.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload digest: %w", err)
	}

	row, err := t.queries.CreateAuditArtifact(ctx, gen.CreateAuditArtifactParams{
		ArtifactID:       toUUID(uuid.New()),
		OrganizationID:   toUUID(input.OrganizationID),
		UserID:           toNullUUID(input.UserID),
		ArtifactType:     string(input.Type),
		ArtifactCategory: string(registry.CategoryAudit),
		ScopeType:        string(input.Scope.Type),

		ScopeOrganizationID: toNullUUID(arc.organizationID),
		ScopeProductID:      toNullUUID(arc.productID),
		ScopeFeatureID:      toNullUUID(arc.featureID),
		ScopeEpicID:         toNullUUID(arc.epicID),
		ScopeStoryID:        toNullUUID(arc.storyID),

		ProductID: toNullUUID(input.Lineage.ProductID),
		FeatureID: toNullUUID(input.Lineage.FeatureID),
		EpicID:    toNullUUID(input.Lineage.EpicID),
		StoryID:   toNullUUID(input.Lineage.StoryID),

		AuthorInstanceID:     toUUID(input.AuthorInstanceID),
		ProducedByToolCallID: toNullUUID(input.ProducedByToolCallID),

		SchemaVersion: int32(registration.CurrentVersion), //nolint:gosec // schema versions are small
		Summary:       input.Summary,
		Payload:       input.Payload,
		PayloadDigest: payloadDigest,
	})
	if err != nil {
		return nil, fmt.Errorf("create audit artifact: %w", err)
	}
	created := auditFromRow(&row)
	return &created, nil
}

// after the call begins. One struct copy per artifact write is not worth trading that for.
//
//nolint:gocritic // hugeParam: the seam takes inputs by value so a caller cannot mutate one
func (t *tx) CreateReview(ctx context.Context, input store.CreateReviewInput) (*store.Review, error) {
	// The digests are stored EXACTLY as observed (design D3a). The seam
	// checks the shape it was given -- well-formed digest, artifact exists
	// -- but never recomputes "current" values, because a non-current
	// digest is a legitimate thing to record and recomputing would bind the
	// review to content the reviewer never saw.
	if !digestPattern.MatchString(input.ReviewDigest) {
		return nil, fmt.Errorf("review digest %q is not 64 lowercase hex characters; a review must record "+
			"what the reviewer saw", input.ReviewDigest)
	}
	// The base is recorded as a PAIR or not at all, matching the schema and
	// what design D6 compares at acceptance. Checked here so a caller reads
	// which half it omitted rather than a constraint name.
	if (input.BaseDigest == nil) != (input.BaseSequence == nil) {
		return nil, errors.New("base digest and base sequence must be given together or not at all; " +
			"a base is a digest AT a sequence, and either alone identifies nothing")
	}
	if input.BaseDigest != nil && !digestPattern.MatchString(*input.BaseDigest) {
		return nil, fmt.Errorf("base digest %q is not 64 lowercase hex characters", *input.BaseDigest)
	}
	if _, err := t.queries.GetManagementArtifact(ctx, gen.GetManagementArtifactParams{
		ArtifactID:     toUUID(input.ArtifactID),
		OrganizationID: toUUID(input.OrganizationID),
	}); err != nil {
		return nil, notFound(err, "artifact under review", input.ArtifactID)
	}

	row, err := t.queries.CreateArtifactReview(ctx, gen.CreateArtifactReviewParams{
		ReviewID:           toUUID(uuid.New()),
		OrganizationID:     toUUID(input.OrganizationID),
		ArtifactID:         toUUID(input.ArtifactID),
		ReviewDigest:       input.ReviewDigest,
		BaseDigest:         input.BaseDigest,
		BaseSequence:       toNullInt32(input.BaseSequence),
		ReviewerInstanceID: toUUID(input.ReviewerInstanceID),
		Decision:           string(input.Decision),
		Rationale:          input.Rationale,
	})
	if err != nil {
		return nil, fmt.Errorf("create review: %w", err)
	}
	created := reviewFromRow(&row)
	return &created, nil
}

// --- reads -----------------------------------------------------------------

func (t *tx) GetManagementArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) (*store.ManagementArtifact, error) {
	row, err := t.queries.GetManagementArtifact(ctx, gen.GetManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return nil, notFound(err, "management artifact", artifactID)
	}
	artifact := managementFromRow(&row)
	if err := t.checkReadable(artifact.Type, artifact.SchemaVersion); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (t *tx) GetAuditArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) (*store.AuditArtifact, error) {
	row, err := t.queries.GetAuditArtifact(ctx, gen.GetAuditArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return nil, notFound(err, "audit artifact", artifactID)
	}
	artifact := auditFromRow(&row)
	if err := t.checkReadable(artifact.Type, artifact.SchemaVersion); err != nil {
		return nil, err
	}
	return &artifact, nil
}

// checkReadable enforces the read direction of design D3: a stored payload
// whose schema_version is outside the registry's readable range is an error
// naming the version, never a best-effort parse.
func (t *tx) checkReadable(artifactType registry.Type, version int) error {
	if _, err := t.registry.ValidatorFor(artifactType, version); err != nil {
		return fmt.Errorf("stored artifact is not readable by this build: %w", err)
	}
	return nil
}

// EffectiveView assembles the original plus its accepted amendments.
func (t *tx) EffectiveView(ctx context.Context, organizationID, artifactID uuid.UUID) (json.RawMessage, error) {
	return t.effectiveViewWithPatch(ctx, organizationID, artifactID, nil)
}

// effectiveViewWithPatch assembles the effective view, optionally applying
// one further patch on top. The extra patch is how a not-yet-accepted
// amendment is validated against what it would produce (design D3).
func (t *tx) effectiveViewWithPatch(ctx context.Context, organizationID, artifactID uuid.UUID, extra json.RawMessage) (json.RawMessage, error) {
	original, err := t.queries.GetManagementArtifact(ctx, gen.GetManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return nil, notFound(err, "management artifact", artifactID)
	}
	if original.IsAmendment {
		return nil, fmt.Errorf("artifact %s is an amendment; effective views are assembled for originals", artifactID)
	}

	amendments, err := t.queries.ListAcceptedAmendments(ctx, gen.ListAcceptedAmendmentsParams{
		AmendsArtifactID: toUUID(artifactID),
		OrganizationID:   toUUID(organizationID),
	})
	if err != nil {
		return nil, fmt.Errorf("list accepted amendments of %s: %w", artifactID, err)
	}

	patches := make([][]byte, 0, len(amendments)+1)
	for i := range amendments {
		patches = append(patches, amendments[i].Payload)
	}
	if extra != nil {
		patches = append(patches, extra)
	}

	view, err := mergepatch.ApplyChain(original.Payload, patches)
	if err != nil {
		return nil, fmt.Errorf("assemble effective view of %s: %w", artifactID, err)
	}
	return view, nil
}

// AmendmentBase reads the view, its digest and the current sequence
// together, inside whatever transaction this tx belongs to, so the three
// cannot disagree.
func (t *tx) AmendmentBase(ctx context.Context, organizationID, originalID uuid.UUID) (store.AmendmentBase, error) {
	view, err := t.EffectiveView(ctx, organizationID, originalID)
	if err != nil {
		return store.AmendmentBase{}, err
	}
	digest, err := canonical.DigestJSON(view)
	if err != nil {
		return store.AmendmentBase{}, fmt.Errorf("digest base of %s: %w", originalID, err)
	}
	sequence, err := t.queries.MaxAmendmentSequence(ctx, gen.MaxAmendmentSequenceParams{
		AmendsArtifactID: toUUID(originalID),
		OrganizationID:   toUUID(organizationID),
	})
	if err != nil {
		return store.AmendmentBase{}, fmt.Errorf("read amendment sequence for %s: %w", originalID, err)
	}
	return store.AmendmentBase{View: view, Digest: digest, Sequence: int(sequence)}, nil
}

func (t *tx) ListManagementArtifactsByScope(ctx context.Context, organizationID uuid.UUID, scope store.Scope) ([]store.ManagementArtifact, error) {
	rows, err := t.queries.ListManagementArtifactsByScope(ctx, gen.ListManagementArtifactsByScopeParams{
		OrganizationID: toUUID(organizationID),
		ScopeType:      string(scope.Type),
		ScopeID:        toUUID(scope.ID),
	})
	if err != nil {
		return nil, fmt.Errorf("list management artifacts by scope: %w", err)
	}
	return managementList(rows), nil
}

func (t *tx) ListManagementArtifactsByStory(ctx context.Context, organizationID, storyID uuid.UUID) ([]store.ManagementArtifact, error) {
	rows, err := t.queries.ListManagementArtifactsByStory(ctx, gen.ListManagementArtifactsByStoryParams{
		OrganizationID: toUUID(organizationID),
		StoryID:        toUUID(storyID),
	})
	if err != nil {
		return nil, fmt.Errorf("list management artifacts by story: %w", err)
	}
	return managementList(rows), nil
}

// managementList maps a row slice, shared by every Management list query so
// the mapping exists once rather than per query.
func managementList(rows []gen.ManagementArtifact) []store.ManagementArtifact {
	artifacts := make([]store.ManagementArtifact, 0, len(rows))
	for i := range rows {
		artifacts = append(artifacts, managementFromRow(&rows[i]))
	}
	return artifacts
}

func (t *tx) ListAuditArtifactsByScope(ctx context.Context, organizationID uuid.UUID, scope store.Scope) ([]store.AuditArtifact, error) {
	rows, err := t.queries.ListAuditArtifactsByScope(ctx, gen.ListAuditArtifactsByScopeParams{
		OrganizationID: toUUID(organizationID),
		ScopeType:      string(scope.Type),
		ScopeID:        toUUID(scope.ID),
	})
	if err != nil {
		return nil, fmt.Errorf("list audit artifacts by scope: %w", err)
	}
	artifacts := make([]store.AuditArtifact, 0, len(rows))
	for i := range rows {
		artifacts = append(artifacts, auditFromRow(&rows[i]))
	}
	return artifacts, nil
}

func (t *tx) ListReviews(ctx context.Context, organizationID, artifactID uuid.UUID) ([]store.Review, error) {
	rows, err := t.queries.ListArtifactReviews(ctx, gen.ListArtifactReviewsParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return nil, fmt.Errorf("list reviews of %s: %w", artifactID, err)
	}
	reviews := make([]store.Review, 0, len(rows))
	for i := range rows {
		reviews = append(reviews, reviewFromRow(&rows[i]))
	}
	return reviews, nil
}

// --- transitions -----------------------------------------------------------

// classifyAcceptance applies every acceptance rule against the locked row
// and the named review, returning the specific rule that refuses.
//
// This runs in Go because a rowcount carries no reason (design D5). The
// same conditions are repeated in the UPDATE as a backstop against a bug
// here.
func classifyAcceptance(transition string, artifact *gen.ManagementArtifact, review *gen.GetArtifactReviewWithReviewerRow) error {
	artifactID := fromUUID(artifact.ArtifactID)

	if store.Status(artifact.Status) != store.StatusDraft {
		return rejected(transition, artifactID, store.ReasonWrongStatus,
			fmt.Sprintf("status is %q, want %q", artifact.Status, store.StatusDraft))
	}
	if fromUUID(review.ArtifactReview.ArtifactID) != artifactID {
		return rejected(transition, artifactID, store.ReasonReviewNotFound,
			fmt.Sprintf("review %s reviews artifact %s", fromUUID(review.ArtifactReview.ReviewID), fromUUID(review.ArtifactReview.ArtifactID)))
	}
	if store.Decision(review.ArtifactReview.Decision) != store.DecisionAccepted {
		return rejected(transition, artifactID, store.ReasonReviewNotAccept,
			fmt.Sprintf("decision is %q", review.ArtifactReview.Decision))
	}
	if review.ArtifactReview.ReviewDigest != artifact.ReviewDigest {
		return rejected(transition, artifactID, store.ReasonDigestMismatch,
			"the artifact's reviewable content changed after this review was recorded")
	}
	if fromUUID(review.ArtifactReview.ReviewerInstanceID) == fromUUID(artifact.AuthorInstanceID) {
		return rejected(transition, artifactID, store.ReasonReviewerIsAuthor, "")
	}
	switch store.PrincipalKind(review.ReviewerKind) {
	case store.PrincipalAgent, store.PrincipalHuman:
	default:
		return rejected(transition, artifactID, store.ReasonReviewerKind,
			fmt.Sprintf("reviewer kind is %q", review.ReviewerKind))
	}
	return nil
}

// lockAndLoadReview locks the artifact and loads the named review together,
// in that order.
func (t *tx) lockAndLoadReview(ctx context.Context, organizationID, artifactID, reviewID uuid.UUID) (gen.ManagementArtifact, gen.GetArtifactReviewWithReviewerRow, error) {
	artifact, err := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return artifact, gen.GetArtifactReviewWithReviewerRow{}, notFound(err, "management artifact", artifactID)
	}
	review, err := t.queries.GetArtifactReviewWithReviewer(ctx, gen.GetArtifactReviewWithReviewerParams{
		ReviewID:       toUUID(reviewID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return artifact, review, notFound(err, "review", reviewID)
	}
	return artifact, review, nil
}

func (t *tx) AcceptArtifact(ctx context.Context, organizationID, artifactID, reviewID uuid.UUID) error {
	artifact, review, err := t.lockAndLoadReview(ctx, organizationID, artifactID, reviewID)
	if err != nil {
		return err
	}
	if artifact.IsAmendment {
		return rejected(transitionAccept, artifactID, store.ReasonIsAmendment,
			"use AcceptAmendment, which checks the reviewed base")
	}
	if classifyErr := classifyAcceptance(transitionAccept, &artifact, &review); classifyErr != nil {
		return classifyErr
	}

	affected, err := t.queries.AcceptManagementArtifact(ctx, gen.AcceptManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
		ReviewID:       toUUID(reviewID),
	})
	if err != nil {
		return fmt.Errorf("accept artifact %s: %w", artifactID, err)
	}
	if affected != 1 {
		return invariant(transitionAccept, artifactID)
	}
	return nil
}

func (t *tx) AcceptAmendment(ctx context.Context, organizationID, amendmentID, reviewID uuid.UUID) error {
	// Load the amendment first only to learn its target, then take the
	// locks in the FIXED order the design requires: original before
	// amendment, everywhere, so concurrent acceptances cannot deadlock.
	preview, err := t.queries.GetManagementArtifact(ctx, gen.GetManagementArtifactParams{
		ArtifactID:     toUUID(amendmentID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return notFound(err, "amendment", amendmentID)
	}
	if !preview.IsAmendment {
		return rejected(transitionAcceptAmendment, amendmentID, store.ReasonNotAmendment, "")
	}
	originalID := fromUUID(preview.AmendsArtifactID)

	// 1. Lock the ORIGINAL. ADR 0028 serializes amendment acceptance per
	// original, so bases move one at a time.
	if _, lockErr := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(originalID),
		OrganizationID: toUUID(organizationID),
	}); lockErr != nil {
		return notFound(lockErr, "amendment target", originalID)
	}

	amendment, review, err := t.lockAndLoadReview(ctx, organizationID, amendmentID, reviewID)
	if err != nil {
		return err
	}
	if classifyErr := classifyAcceptance(transitionAcceptAmendment, &amendment, &review); classifyErr != nil {
		return classifyErr
	}

	// 2-3. Assemble the current effective view and compare it against the
	// base the reviewer actually reviewed against.
	base, currentSequence, err := t.verifyReviewedBase(ctx, organizationID, originalID, amendmentID, &review)
	if err != nil {
		return err
	}

	// 4. Validate the RESULT, not the patch. ADR 0028 requires validation
	// at acceptance and not only at write, since the base may have moved
	// since the patch was authored.
	merged, err := mergepatch.ApplyChain(base, [][]byte{amendment.Payload})
	if err != nil {
		return fmt.Errorf("apply amendment %s: %w", amendmentID, err)
	}
	if validationErr := t.validatePayload(registry.Type(amendment.ArtifactType), int(amendment.SchemaVersion), merged); validationErr != nil {
		return fmt.Errorf("amendment %s produces an invalid effective payload: %w", amendmentID, validationErr)
	}

	// 5. Allocate the sequence from the historical maximum.
	next := currentSequence + 1

	affected, err := t.queries.AcceptManagementAmendment(ctx, gen.AcceptManagementAmendmentParams{
		AmendmentSequence: &next,
		ArtifactID:        toUUID(amendmentID),
		OrganizationID:    toUUID(organizationID),
		AmendsArtifactID:  toUUID(originalID),
		ReviewID:          toUUID(reviewID),
	})
	if err != nil {
		return fmt.Errorf("accept amendment %s: %w", amendmentID, err)
	}
	if affected != 1 {
		return invariant(transitionAcceptAmendment, amendmentID)
	}
	return nil
}

// verifyReviewedBase assembles the original's current effective view and
// checks it against the base recorded on the review.
//
// Two amendments reviewed against the same base must yield exactly one
// acceptance and one ErrBaseMoved -- the max(sequence)+1-with-retry
// approach this replaced would have accepted both, which is the outcome
// ADR 0028 forbids.
func (t *tx) verifyReviewedBase(ctx context.Context, organizationID, originalID, amendmentID uuid.UUID,
	review *gen.GetArtifactReviewWithReviewerRow,
) (base []byte, sequence int32, err error) {
	base, err = t.EffectiveView(ctx, organizationID, originalID)
	if err != nil {
		return nil, 0, err
	}
	baseDigest, err := canonical.DigestJSON(base)
	if err != nil {
		return nil, 0, fmt.Errorf("digest current base of %s: %w", originalID, err)
	}
	if review.ArtifactReview.BaseDigest == nil || review.ArtifactReview.BaseSequence == nil {
		return nil, 0, rejected(transitionAcceptAmendment, amendmentID, store.ReasonReviewNotFound,
			"review records no base, so it cannot have reviewed an amendment")
	}
	sequence, err = t.queries.MaxAmendmentSequence(ctx, gen.MaxAmendmentSequenceParams{
		AmendsArtifactID: toUUID(originalID),
		OrganizationID:   toUUID(organizationID),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("read amendment sequence for %s: %w", originalID, err)
	}
	// Both halves are compared. The digest alone would miss an amendment
	// whose patch left the view byte-identical -- a no-op amendment still
	// advances the chain, and a later reviewer must be looking at the same
	// point in it.
	if *review.ArtifactReview.BaseDigest != baseDigest || *review.ArtifactReview.BaseSequence != sequence {
		return nil, 0, fmt.Errorf("%w: amendment %s was reviewed against base %s at sequence %d, but the "+
			"original's current base is %s at sequence %d",
			store.ErrBaseMoved, amendmentID, *review.ArtifactReview.BaseDigest, *review.ArtifactReview.BaseSequence,
			baseDigest, sequence)
	}
	return base, sequence, nil
}

func (t *tx) InvalidateArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) error {
	artifact, err := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return notFound(err, "management artifact", artifactID)
	}
	if store.Status(artifact.Status) != store.StatusDraft {
		return rejected(transitionInvalidate, artifactID, store.ReasonWrongStatus,
			fmt.Sprintf("status is %q; invalidation is pre-acceptance by definition", artifact.Status))
	}

	affected, err := t.queries.InvalidateManagementArtifact(ctx, gen.InvalidateManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return fmt.Errorf("invalidate artifact %s: %w", artifactID, err)
	}
	if affected != 1 {
		return invariant(transitionInvalidate, artifactID)
	}
	return nil
}

func (t *tx) SupersedeArtifact(ctx context.Context, organizationID, targetID, supersedingID, reviewID uuid.UUID) error {
	// Lock the TARGET first. The order is fixed so two concurrent
	// supersessions of the same target cannot deadlock against each other.
	target, err := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(targetID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return notFound(err, "supersession target", targetID)
	}
	if target.IsAmendment {
		return rejected(transitionSupersede, targetID, store.ReasonIsAmendment, "")
	}
	if store.Status(target.Status) != store.StatusAccepted {
		return rejected(transitionSupersede, targetID, store.ReasonWrongStatus,
			fmt.Sprintf("status is %q, want %q", target.Status, store.StatusAccepted))
	}

	superseding, review, err := t.lockAndLoadReview(ctx, organizationID, supersedingID, reviewID)
	if err != nil {
		return err
	}
	// The superseding artifact must name THIS target. Without the check, an
	// artifact reviewed and accepted as superseding A could retire B.
	if fromUUID(superseding.SupersedesArtifactID) != targetID {
		return rejected(transitionSupersede, targetID, store.ReasonSupersedeTarget,
			fmt.Sprintf("artifact %s supersedes %s", supersedingID, fromUUID(superseding.SupersedesArtifactID)))
	}
	if classifyErr := classifyAcceptance(transitionSupersede, &superseding, &review); classifyErr != nil {
		return classifyErr
	}

	// Accept the superseding artifact and retire the target in ONE
	// transaction: a reader between the two would otherwise observe two
	// authoritative artifacts for the same subject.
	acceptedRows, err := t.queries.AcceptManagementArtifact(ctx, gen.AcceptManagementArtifactParams{
		ArtifactID:     toUUID(supersedingID),
		OrganizationID: toUUID(organizationID),
		ReviewID:       toUUID(reviewID),
	})
	if err != nil {
		return fmt.Errorf("accept superseding artifact %s: %w", supersedingID, err)
	}
	if acceptedRows != 1 {
		return invariant(transitionSupersede, supersedingID)
	}

	supersededRows, err := t.queries.SupersedeManagementArtifact(ctx, gen.SupersedeManagementArtifactParams{
		ArtifactID:            toUUID(targetID),
		OrganizationID:        toUUID(organizationID),
		SupersedingArtifactID: toUUID(supersedingID),
	})
	if err != nil {
		return fmt.Errorf("supersede artifact %s: %w", targetID, err)
	}
	if supersededRows != 1 {
		return invariant(transitionSupersede, targetID)
	}
	return nil
}

func (t *tx) ArchiveArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) error {
	artifact, err := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return notFound(err, "management artifact", artifactID)
	}
	if artifact.IsAmendment {
		return rejected(transitionArchive, artifactID, store.ReasonIsAmendment,
			"archiving an amendment would drop its contribution from an effective view nobody re-reviewed")
	}
	switch store.Status(artifact.Status) {
	case store.StatusAccepted, store.StatusSuperseded:
	default:
		return rejected(transitionArchive, artifactID, store.ReasonWrongStatus,
			fmt.Sprintf("status is %q, want %q or %q", artifact.Status, store.StatusAccepted, store.StatusSuperseded))
	}

	affected, err := t.queries.ArchiveManagementArtifact(ctx, gen.ArchiveManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return fmt.Errorf("archive artifact %s: %w", artifactID, err)
	}
	if affected != 1 {
		return invariant(transitionArchive, artifactID)
	}
	return nil
}
