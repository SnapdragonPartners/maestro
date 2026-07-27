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
// reviewable envelope, not the payload alone.
//
// Everything a reviewer's judgement depends on belongs here. An earlier
// version carried only type, version, summary and payload, which left the
// scope, the lineage, the author and every relationship link outside the
// binding — so an artifact could be re-pointed at a different Story, or
// have its supersession target changed, and its review would still match.
// The identity is included too, which is why the id is allocated before
// the digest rather than by the INSERT.
//
// Field ORDER is chosen for alignment and does not affect the digest: JCS
// sorts object keys, so the canonical form is identical whatever order
// encoding/json emits them in.
type reviewableProjection struct {
	ProductID            *string `json:"product_id"`
	FeatureID            *string `json:"feature_id"`
	EpicID               *string `json:"epic_id"`
	StoryID              *string `json:"story_id"`
	AmendsArtifactID     *string `json:"amends_artifact_id"`
	SupersedesArtifactID *string `json:"supersedes_artifact_id"`
	ReplacesArtifactID   *string `json:"replaces_artifact_id"`

	ArtifactID       string `json:"artifact_id"`
	ArtifactType     string `json:"artifact_type"`
	ArtifactCategory string `json:"artifact_category"`
	Summary          string `json:"summary"`
	ScopeType        string `json:"scope_type"`
	ScopeID          string `json:"scope_id"`
	AuthorInstanceID string `json:"author_instance_id"`

	Payload json.RawMessage `json:"payload"`

	SchemaVersion int `json:"schema_version"`
}

// buildReviewableProjection assembles the envelope a review binds to.
//
// Extracted so the projection can be tested on its own, with the artifact
// id HELD FIXED. Testing it through creation cannot work: every created
// artifact gets a fresh id, the id is part of the projection, so every
// digest differs whatever else the projection contains — a comparison that
// passes even with the payload, scope, lineage, author and links removed.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func buildReviewableProjection(artifactID uuid.UUID, artifactType registry.Type, category registry.Category,
	version int, input store.CreateManagementArtifactInput,
) reviewableProjection {
	return reviewableProjection{
		ProductID:            optionalID(input.Lineage.ProductID),
		FeatureID:            optionalID(input.Lineage.FeatureID),
		EpicID:               optionalID(input.Lineage.EpicID),
		StoryID:              optionalID(input.Lineage.StoryID),
		AmendsArtifactID:     optionalID(input.AmendsArtifactID),
		SupersedesArtifactID: optionalID(input.SupersedesArtifactID),
		ReplacesArtifactID:   optionalID(input.ReplacesArtifactID),

		ArtifactID:       artifactID.String(),
		ArtifactType:     string(artifactType),
		ArtifactCategory: string(category),
		Summary:          input.Summary,
		ScopeType:        string(input.Scope.Type),
		ScopeID:          input.Scope.ID.String(),
		AuthorInstanceID: input.AuthorInstanceID.String(),

		Payload:       input.Payload,
		SchemaVersion: version,
	}
}

// optionalID renders an optional identifier for the projection.
func optionalID(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	rendered := id.String()
	return &rendered
}

// --- creation --------------------------------------------------------------

// resolveManagementIdentity settles what an artifact IS: its type, category
// and schema version.
//
// An original takes all three from the registry. An amendment takes them
// from the TARGET ORIGINAL (design D3): ADR 0028 forbids an amendment
// changing type or version, and stored payloads are never rewritten, so an
// artifact created at v1 stays v1 for life. Taking the registry's current
// version would stamp the amendment v2 once the registry advanced and
// validate the merged result against a schema its payload was never written
// for.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) resolveManagementIdentity(ctx context.Context, input store.CreateManagementArtifactInput) (
	registry.Type, registry.Category, int, error,
) {
	if input.AmendsArtifactID == nil {
		registration, err := t.registry.Lookup(input.Type)
		if err != nil {
			return "", "", 0, fmt.Errorf("management artifact write: %w", err)
		}
		if registration.Category != registry.CategoryManagement {
			return "", "", 0, fmt.Errorf("type %q is registered as %q, so it cannot be written as a Management artifact",
				input.Type, registration.Category)
		}
		return input.Type, registry.CategoryManagement, registration.CurrentVersion, nil
	}

	original, err := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(*input.AmendsArtifactID),
		OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return "", "", 0, notFound(err, "amendment target", *input.AmendsArtifactID)
	}
	if original.IsAmendment {
		return "", "", 0, fmt.Errorf("artifact %s is itself an amendment; the amendment chain is flat (ADR 0021)",
			*input.AmendsArtifactID)
	}
	// ADR 0021 defines an amendment as an after-acceptance change. A draft
	// is edited, not amended; an invalidated, superseded or archived
	// artifact is not current content to amend at all.
	if store.Status(original.Status) != store.StatusAccepted {
		return "", "", 0, fmt.Errorf("artifact %s is %q, and only an accepted artifact can be amended (ADR 0021): "+
			"a draft is edited rather than amended", *input.AmendsArtifactID, original.Status)
	}
	return registry.Type(original.ArtifactType), registry.Category(original.ArtifactCategory),
		int(original.SchemaVersion), nil
}

// CreateManagementArtifact writes a draft Management artifact.
//
// Category, schema version and both digests are settled here rather than by
// the caller (design D3): a caller-supplied digest is a caller-asserted one,
// and the point is that it is derived. The identity is allocated before the
// digest because the digest binds it.
//
// The input is taken by value so a caller cannot mutate it after the call
// begins. One struct copy per write is not worth trading that guarantee for.
//
//nolint:gocritic // hugeParam: by value, deliberately — see above
func (t *tx) CreateManagementArtifact(ctx context.Context, input store.CreateManagementArtifactInput) (*store.ManagementArtifact, error) {
	artifactID, idErr := newIdentifier(input.ArtifactID)
	if idErr != nil {
		return nil, idErr
	}
	// ADR 0021: a Management artifact's author is an agent or a human.
	// System principals emit exhaust, not reviewable work product.
	if authorErr := t.requirePrincipalKind(ctx, input.OrganizationID, input.AuthorInstanceID, "author",
		store.PrincipalAgent, store.PrincipalHuman); authorErr != nil {
		return nil, authorErr
	}

	artifactType, category, version, err := t.resolveManagementIdentity(ctx, input)
	if err != nil {
		return nil, err
	}

	// Validate the instance that will be STORED. For an original that is
	// the payload; for an amendment the patch is stored but the MERGED
	// result is what must satisfy the schema, so it is validated below and
	// again at acceptance.
	if input.AmendsArtifactID == nil {
		if validationErr := t.validatePayload(artifactType, version, input.Payload); validationErr != nil {
			return nil, validationErr
		}
	} else {
		merged, mergeErr := t.effectiveViewWithPatch(ctx, input.OrganizationID, *input.AmendsArtifactID, input.Payload)
		if mergeErr != nil {
			return nil, mergeErr
		}
		if validationErr := t.validatePayload(artifactType, version, merged); validationErr != nil {
			return nil, fmt.Errorf("merged effective payload is invalid: %w", validationErr)
		}
	}

	arc, scopeErr := scopeColumns(input.Scope)
	if scopeErr != nil {
		return nil, scopeErr
	}

	storedVersion, err := toInt32(version, "schema version")
	if err != nil {
		return nil, err
	}
	payloadDigest, err := canonical.DigestJSON(input.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload digest: %w", err)
	}
	reviewDigest, err := canonical.Digest(
		buildReviewableProjection(artifactID, artifactType, category, version, input))
	if err != nil {
		return nil, fmt.Errorf("review digest: %w", err)
	}

	row, err := t.queries.CreateManagementArtifact(ctx, gen.CreateManagementArtifactParams{
		ArtifactID:       toUUID(artifactID),
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

		SchemaVersion: storedVersion,
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

// CreateAuditArtifact writes an Audit artifact, which is born final: no
// status, no review, no amendment, no supersession.
//
// Unlike the Management family it admits a system principal as author and
// no user at all, because exhaust genuinely precedes any user's action.
//
// The input is taken by value so a caller cannot mutate it after the call
// begins. One struct copy per write is not worth trading that guarantee for.
//
//nolint:gocritic // hugeParam: by value, deliberately — see above
func (t *tx) CreateAuditArtifact(ctx context.Context, input store.CreateAuditArtifactInput) (*store.AuditArtifact, error) {
	artifactID, err := newIdentifier(input.ArtifactID)
	if err != nil {
		return nil, err
	}
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

	arc, scopeErr := scopeColumns(input.Scope)
	if scopeErr != nil {
		return nil, scopeErr
	}
	storedVersion, err := toInt32(registration.CurrentVersion, "schema version")
	if err != nil {
		return nil, err
	}
	payloadDigest, err := canonical.DigestJSON(input.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload digest: %w", err)
	}

	row, err := t.queries.CreateAuditArtifact(ctx, gen.CreateAuditArtifactParams{
		ArtifactID:       toUUID(artifactID),
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

		SchemaVersion: storedVersion,
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

// CreateReview records a review decision.
//
// The digests are stored EXACTLY as observed (design D3a). The seam
// validates the shape it is given but never recomputes "current" values,
// because a non-current digest is a legitimate thing to record — and
// recomputing would bind the review to content the reviewer never saw,
// manufacturing the false attestation the digest binding exists to prevent.
//
// The input is taken by value so a caller cannot mutate it after the call
// begins. One struct copy per write is not worth trading that guarantee for.
//
//nolint:gocritic // hugeParam: by value, deliberately — see above
func (t *tx) CreateReview(ctx context.Context, input store.CreateReviewInput) (*store.Review, error) {
	// The digests are stored EXACTLY as observed (design D3a). The seam
	// checks the shape it was given -- well-formed digest, artifact exists,
	// decision in the vocabulary -- but never recomputes "current" values,
	// because a non-current digest is a legitimate thing to record and
	// recomputing would bind the review to content the reviewer never saw.
	switch input.Decision {
	case store.DecisionAccepted, store.DecisionRejected, store.DecisionChangesRequested:
	default:
		return nil, fmt.Errorf("decision %q is not one of %q, %q or %q", input.Decision,
			store.DecisionAccepted, store.DecisionRejected, store.DecisionChangesRequested)
	}
	if !digestPattern.MatchString(input.ReviewDigest) {
		return nil, fmt.Errorf("review digest %q is not 64 lowercase hex characters; a review must record "+
			"what the reviewer saw", input.ReviewDigest)
	}
	// The base is recorded as a PAIR or not at all, matching the schema and
	// what design D6 compares at acceptance.
	if (input.BaseDigest == nil) != (input.BaseSequence == nil) {
		return nil, errors.New("base digest and base sequence must be given together or not at all; " +
			"a base is a digest AT a sequence, and either alone identifies nothing")
	}
	if input.BaseDigest != nil && !digestPattern.MatchString(*input.BaseDigest) {
		return nil, fmt.Errorf("base digest %q is not 64 lowercase hex characters", *input.BaseDigest)
	}
	baseSequence, err := toNullInt32(input.BaseSequence)
	if err != nil {
		return nil, fmt.Errorf("base sequence: %w", err)
	}

	artifact, err := t.queries.GetManagementArtifact(ctx, gen.GetManagementArtifactParams{
		ArtifactID:     toUUID(input.ArtifactID),
		OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return nil, notFound(err, "artifact under review", input.ArtifactID)
	}
	// A base is meaningful only for an amendment, and an amendment review
	// without one cannot be accepted at all (design D6 step 3). Checking
	// applicability here means the mismatch surfaces when the review is
	// recorded rather than at acceptance, when the reviewer has gone.
	if artifact.IsAmendment && input.BaseDigest == nil {
		return nil, errors.New("a review of an amendment must record the base it was reviewed against, " +
			"or the amendment can never be accepted")
	}
	if !artifact.IsAmendment && input.BaseDigest != nil {
		return nil, errors.New("a review of an original must not record a base; only an amendment has one")
	}

	reviewID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}
	row, err := t.queries.CreateArtifactReview(ctx, gen.CreateArtifactReviewParams{
		ReviewID:           toUUID(reviewID),
		OrganizationID:     toUUID(input.OrganizationID),
		ArtifactID:         toUUID(input.ArtifactID),
		ReviewDigest:       input.ReviewDigest,
		BaseDigest:         input.BaseDigest,
		BaseSequence:       baseSequence,
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
	// Design D3's read rule applies here too. Assembling a view from a
	// payload this build cannot validate would produce content the seam
	// claims it cannot read, by a path that never consulted the registry.
	if readErr := t.checkReadable(registry.Type(original.ArtifactType), int(original.SchemaVersion)); readErr != nil {
		return nil, readErr
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
		if readErr := t.checkReadable(registry.Type(amendments[i].ArtifactType), int(amendments[i].SchemaVersion)); readErr != nil {
			return nil, fmt.Errorf("amendment %s: %w", fromUUID(amendments[i].ArtifactID), readErr)
		}
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

// AmendmentBase reads the view, its digest and the current sequence under
// the ORIGINAL's lock.
//
// The transaction alone is not enough. inTx runs at Postgres's default READ
// COMMITTED, where every statement takes a fresh snapshot -- so an
// amendment accepted between the view query and the sequence query would
// return an old digest paired with a new sequence. A reviewer would then
// record a base that never existed, and acceptance would reject it as
// moved. Taking the same lock acceptance takes serialises the two against
// each other, which is what makes this a snapshot rather than two reads.
func (t *tx) AmendmentBase(ctx context.Context, organizationID, originalID uuid.UUID) (store.AmendmentBase, error) {
	if _, lockErr := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(originalID),
		OrganizationID: toUUID(organizationID),
	}); lockErr != nil {
		return store.AmendmentBase{}, notFound(lockErr, "amendment target", originalID)
	}

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
	return t.managementList(rows)
}

func (t *tx) ListManagementArtifactsByStory(ctx context.Context, organizationID, storyID uuid.UUID) ([]store.ManagementArtifact, error) {
	rows, err := t.queries.ListManagementArtifactsByStory(ctx, gen.ListManagementArtifactsByStoryParams{
		OrganizationID: toUUID(organizationID),
		StoryID:        toUUID(storyID),
	})
	if err != nil {
		return nil, fmt.Errorf("list management artifacts by story: %w", err)
	}
	return t.managementList(rows)
}

// managementList maps a row slice, checking each row's readability.
//
// Shared by every Management list query so both the mapping and design D3's
// read rule exist once. A list that skipped the check would hand back
// payloads the seam refuses to return one at a time.
func (t *tx) managementList(rows []gen.ManagementArtifact) ([]store.ManagementArtifact, error) {
	artifacts := make([]store.ManagementArtifact, 0, len(rows))
	for i := range rows {
		if err := t.checkReadable(registry.Type(rows[i].ArtifactType), int(rows[i].SchemaVersion)); err != nil {
			return nil, fmt.Errorf("artifact %s: %w", fromUUID(rows[i].ArtifactID), err)
		}
		artifacts = append(artifacts, managementFromRow(&rows[i]))
	}
	return artifacts, nil
}

// auditList is the Audit equivalent, with the same read rule.
func (t *tx) auditList(rows []gen.AuditArtifact) ([]store.AuditArtifact, error) {
	artifacts := make([]store.AuditArtifact, 0, len(rows))
	for i := range rows {
		if err := t.checkReadable(registry.Type(rows[i].ArtifactType), int(rows[i].SchemaVersion)); err != nil {
			return nil, fmt.Errorf("artifact %s: %w", fromUUID(rows[i].ArtifactID), err)
		}
		artifacts = append(artifacts, auditFromRow(&rows[i]))
	}
	return artifacts, nil
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
	return t.auditList(rows)
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
	original, lockErr := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(originalID),
		OrganizationID: toUUID(organizationID),
	})
	if lockErr != nil {
		return notFound(lockErr, "amendment target", originalID)
	}
	// The target was accepted when the amendment was WRITTEN, but review
	// takes time and status moves. Archiving or superseding an original
	// between the two would otherwise let it still receive an accepted
	// amendment -- new accepted content attached to something retired, and
	// folded into an effective view nobody expects to change. Rechecked
	// here under the lock, and repeated in the SQL backstop.
	if store.Status(original.Status) != store.StatusAccepted {
		return rejected(transitionAcceptAmendment, amendmentID, store.ReasonWrongStatus,
			fmt.Sprintf("the amended original %s is %q, not %q", originalID, original.Status, store.StatusAccepted))
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
