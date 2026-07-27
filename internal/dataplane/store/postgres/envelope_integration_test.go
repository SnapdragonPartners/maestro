//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// --- the reviewable envelope (ADR 0028 §5) ---------------------------------

// The review digest's field-by-field binding is tested in
// projection_test.go, against the projection itself with the artifact id
// held FIXED. It cannot be tested through creation: every created artifact
// gets a fresh id, the id is part of the projection, so every digest
// differs whatever else the projection contains -- a comparison that passes
// even with the payload, scope, lineage, author and links all removed. The
// test that used to live here was exactly that false positive.

// TestReviewDigestBindsTheArtifactIdentity is the case that forces the id to
// be allocated before the digest rather than by the INSERT. Two artifacts
// identical in every other respect must not share a review digest, or a
// review of one would match the other.
func TestReviewDigestBindsTheArtifactIdentity(t *testing.T) {
	f := newFixture(t)

	first := f.createDraft(t, `{"title":"same"}`)
	second := f.createDraft(t, `{"title":"same"}`)

	if first.PayloadDigest != second.PayloadDigest {
		t.Fatal("identical payloads produced different payload digests")
	}
	if first.ReviewDigest == second.ReviewDigest {
		t.Fatal("two distinct artifacts with identical content share a review digest, so a review of one " +
			"would license acceptance of the other")
	}
}

// --- identifiers -----------------------------------------------------------

// TestCreatedIdentifiersAreUUIDv7 pins the version. uuid.New() returns v4,
// which is what this used to call: v7 is time-ordered, so keys cluster by
// creation time instead of scattering across the index.
func TestCreatedIdentifiersAreUUIDv7(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	artifact := f.createDraft(t, `{"title":"x"}`)
	if got := artifact.ArtifactID.Version(); got != 7 {
		t.Errorf("management artifact id is UUID version %d, want 7", got)
	}

	review := f.review(t, artifact.ArtifactID, artifact.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)
	if got := review.ReviewID.Version(); got != 7 {
		t.Errorf("review id is UUID version %d, want 7", got)
	}

	audit, err := f.store.CreateAuditArtifact(ctx, store.CreateAuditArtifactInput{
		Payload:          json.RawMessage(`{"title":"e"}`),
		Type:             "test_event",
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		AuthorInstanceID: f.systemAgent,
	})
	if err != nil {
		t.Fatalf("create audit artifact: %v", err)
	}
	if got := audit.ArtifactID.Version(); got != 7 {
		t.Errorf("audit artifact id is UUID version %d, want 7", got)
	}
}

// TestPreallocatedIdentityIsHonoured covers item 6's object-first commit
// order, which needs the id before the row exists.
func TestPreallocatedIdentityIsHonoured(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	preallocated, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	artifact, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		ArtifactID:       preallocated,
		Payload:          json.RawMessage(`{"title":"x"}`),
		Type:             testType,
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if artifact.ArtifactID != preallocated {
		t.Fatalf("id = %s, want the preallocated %s", artifact.ArtifactID, preallocated)
	}

	// A preallocated v4 must be refused rather than silently stored: the
	// ordering property is the reason for the requirement.
	_, err = f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		ArtifactID:       uuid.New(), // v4
		Payload:          json.RawMessage(`{"title":"x"}`),
		Type:             testType,
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if err == nil || !strings.Contains(err.Error(), "version 4") {
		t.Fatalf("error = %v, want a refusal naming the wrong UUID version", err)
	}
}

// --- authorship ------------------------------------------------------------

// TestSystemPrincipalCannotAuthorAManagementArtifact closes the gap the
// foreign key leaves: it proves the author EXISTS, not that it may author.
// Without this a system principal authors reviewable work product and an
// agent accepts it, because acceptance validated only the reviewer's kind.
func TestSystemPrincipalCannotAuthorAManagementArtifact(t *testing.T) {
	f := newFixture(t)

	_, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(`{"title":"x"}`),
		Type:             testType,
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.systemAgent,
	})
	if err == nil {
		t.Fatal("a system principal authored a Management artifact; ADR 0021 requires an agent or human")
	}

	// The Audit family genuinely admits system authors, so the rule must
	// not have been applied there.
	if _, err := f.store.CreateAuditArtifact(context.Background(), store.CreateAuditArtifactInput{
		Payload:          json.RawMessage(`{"title":"e"}`),
		Type:             "test_event",
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		AuthorInstanceID: f.systemAgent,
	}); err != nil {
		t.Fatalf("a system principal must be able to author Audit exhaust: %v", err)
	}
}

// --- amendment targets -----------------------------------------------------

// TestAmendmentTargetMustBeAccepted covers ADR 0021's definition: an
// amendment is an after-acceptance change. A draft is edited, not amended.
func TestAmendmentTargetMustBeAccepted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	t.Run("draft target", func(t *testing.T) {
		draft := f.createDraft(t, `{"title":"one"}`)
		_, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
			Payload:          json.RawMessage(`{"title":"two"}`),
			AmendsArtifactID: &draft.ArtifactID,
			Type:             testType,
			Summary:          "s",
			Scope:            f.scope(),
			OrganizationID:   f.organizationID,
			UserID:           f.userID,
			AuthorInstanceID: f.author,
		})
		if err == nil || !strings.Contains(err.Error(), "draft") {
			t.Fatalf("error = %v, want a refusal naming the target's status", err)
		}
	})

	t.Run("invalidated target", func(t *testing.T) {
		draft := f.createDraft(t, `{"title":"one"}`)
		if err := f.store.InvalidateArtifact(ctx, f.organizationID, draft.ArtifactID); err != nil {
			t.Fatalf("invalidate: %v", err)
		}
		_, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
			Payload:          json.RawMessage(`{"title":"two"}`),
			AmendsArtifactID: &draft.ArtifactID,
			Type:             testType,
			Summary:          "s",
			Scope:            f.scope(),
			OrganizationID:   f.organizationID,
			UserID:           f.userID,
			AuthorInstanceID: f.author,
		})
		if err == nil {
			t.Fatal("an invalidated artifact was amended")
		}
	})

	t.Run("archived target", func(t *testing.T) {
		original := acceptedOriginal(t, f, `{"title":"one"}`)
		if err := f.store.ArchiveArtifact(ctx, f.organizationID, original.ArtifactID); err != nil {
			t.Fatalf("archive: %v", err)
		}
		_, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
			Payload:          json.RawMessage(`{"title":"two"}`),
			AmendsArtifactID: &original.ArtifactID,
			Type:             testType,
			Summary:          "s",
			Scope:            f.scope(),
			OrganizationID:   f.organizationID,
			UserID:           f.userID,
			AuthorInstanceID: f.author,
		})
		if err == nil {
			t.Fatal("an archived artifact was amended")
		}
	})
}

// --- review inputs ---------------------------------------------------------

func TestReviewDecisionVocabulary(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	artifact := f.createDraft(t, `{"title":"x"}`)

	// changes_requested is part of the schema's vocabulary and must be
	// nameable, or it is a decision the database accepts and no caller can
	// record.
	if _, err := f.store.CreateReview(ctx, store.CreateReviewInput{
		ReviewDigest:       artifact.ReviewDigest,
		Rationale:          "needs work",
		Decision:           store.DecisionChangesRequested,
		OrganizationID:     f.organizationID,
		ArtifactID:         artifact.ArtifactID,
		ReviewerInstanceID: f.reviewer,
	}); err != nil {
		t.Fatalf("changes_requested was refused: %v", err)
	}

	_, err := f.store.CreateReview(ctx, store.CreateReviewInput{
		ReviewDigest:       artifact.ReviewDigest,
		Rationale:          "?",
		Decision:           "looks-fine-to-me",
		OrganizationID:     f.organizationID,
		ArtifactID:         artifact.ArtifactID,
		ReviewerInstanceID: f.reviewer,
	})
	if err == nil || strings.Contains(err.Error(), "SQLSTATE") {
		t.Fatalf("error = %v, want the seam to refuse an unknown decision before Postgres does", err)
	}
}

// TestBaseSequenceOutsideInt32IsRejected covers the silent-wrap hazard. An
// unchecked int32() conversion turns 4294967297 into 1 -- and 1 is a REAL
// sequence, so the review would bind to a base the reviewer never named.
func TestBaseSequenceOutsideInt32IsRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)
	base := f.base(t, original.ArtifactID)
	amendment := f.createAmendment(t, original.ArtifactID, `{"title":"two"}`)

	for _, sequence := range []int{math.MaxInt32 + 1, -1, math.MaxInt32 + 2} {
		digest := base.Digest
		bad := sequence
		_, err := f.store.CreateReview(ctx, store.CreateReviewInput{
			ReviewDigest:       amendment.ReviewDigest,
			BaseDigest:         &digest,
			BaseSequence:       &bad,
			Rationale:          "r",
			Decision:           store.DecisionAccepted,
			OrganizationID:     f.organizationID,
			ArtifactID:         amendment.ArtifactID,
			ReviewerInstanceID: f.reviewer,
		})
		if err == nil {
			t.Fatalf("base sequence %d was accepted; it does not fit the int4 column and would wrap", sequence)
		}
		if strings.Contains(err.Error(), "SQLSTATE") {
			t.Fatalf("base sequence %d reached Postgres: %v", sequence, err)
		}
	}
}

// TestBaseApplicabilityIsChecked keeps the mismatch at the moment of
// recording rather than at acceptance, when the reviewer has gone.
func TestBaseApplicabilityIsChecked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)
	base := f.base(t, original.ArtifactID)
	amendment := f.createAmendment(t, original.ArtifactID, `{"title":"two"}`)

	// An amendment review with no base can never be accepted.
	if _, err := f.store.CreateReview(ctx, store.CreateReviewInput{
		ReviewDigest:       amendment.ReviewDigest,
		Rationale:          "r",
		Decision:           store.DecisionAccepted,
		OrganizationID:     f.organizationID,
		ArtifactID:         amendment.ArtifactID,
		ReviewerInstanceID: f.reviewer,
	}); err == nil {
		t.Fatal("a review of an amendment was recorded with no base, so the amendment could never be accepted")
	}

	// An original review with a base names something originals do not have.
	digest := base.Digest
	sequence := base.Sequence
	if _, err := f.store.CreateReview(ctx, store.CreateReviewInput{
		ReviewDigest:       original.ReviewDigest,
		BaseDigest:         &digest,
		BaseSequence:       &sequence,
		Rationale:          "r",
		Decision:           store.DecisionAccepted,
		OrganizationID:     f.organizationID,
		ArtifactID:         original.ArtifactID,
		ReviewerInstanceID: f.reviewer,
	}); err == nil {
		t.Fatal("a review of an original was recorded with a base")
	}
}

// --- version-bounded reads (design D3) -------------------------------------

// TestReadsAreVersionBoundedOnEveryPath is D3's read rule. An earlier
// version checked only the two single-artifact Gets, so EffectiveView and
// every list handed back payloads this build cannot validate -- content the
// seam refuses to return one at a time.
func TestReadsAreVersionBoundedOnEveryPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)

	// A build whose registry knows the type but not the stored version.
	narrowed, err := registry.New(map[registry.Type]registry.Entry{
		testType: {
			Category:       registry.CategoryManagement,
			CurrentVersion: 2,
			Validators:     map[int]registry.Validator{2: requireTitle()},
		},
	})
	if err != nil {
		t.Fatalf("narrowed registry: %v", err)
	}
	narrowStore, err := postgres.New(f.pool, narrowed)
	if err != nil {
		t.Fatalf("narrowed store: %v", err)
	}

	t.Run("get", func(t *testing.T) {
		if _, err := narrowStore.GetManagementArtifact(ctx, f.organizationID, original.ArtifactID); !errors.Is(err, registry.ErrVersionOutOfRange) {
			t.Fatalf("error = %v, want ErrVersionOutOfRange", err)
		}
	})
	t.Run("effective view", func(t *testing.T) {
		if _, err := narrowStore.EffectiveView(ctx, f.organizationID, original.ArtifactID); !errors.Is(err, registry.ErrVersionOutOfRange) {
			t.Fatalf("error = %v, want ErrVersionOutOfRange", err)
		}
	})
	t.Run("list by scope", func(t *testing.T) {
		if _, err := narrowStore.ListManagementArtifactsByScope(ctx, f.organizationID, f.scope()); !errors.Is(err, registry.ErrVersionOutOfRange) {
			t.Fatalf("error = %v, want ErrVersionOutOfRange", err)
		}
	})
	t.Run("amendment base", func(t *testing.T) {
		if _, err := narrowStore.AmendmentBase(ctx, f.organizationID, original.ArtifactID); !errors.Is(err, registry.ErrVersionOutOfRange) {
			t.Fatalf("error = %v, want ErrVersionOutOfRange", err)
		}
	})
}

// --- AmendmentBase is a single snapshot (design D6) ------------------------

// TestAmendmentBaseHoldsTheOriginalsLock is the regression test for the
// READ COMMITTED hazard. inTx runs at Postgres's default isolation, where
// every statement takes a fresh snapshot -- so a version that merely ran
// the two reads in one transaction could return an old digest paired with a
// new sequence if an amendment were accepted between them. A reviewer would
// record a base that never existed, and acceptance would reject it.
//
// The check is deterministic rather than timing-based: while AmendmentBase
// is in flight, a SELECT ... FOR UPDATE NOWAIT on the original from another
// connection must fail immediately, which it can only do if the lock is
// held.
func TestAmendmentBaseHoldsTheOriginalsLock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)

	var probeErr error
	err := f.store.WithTx(ctx, func(tx store.Tx) error {
		if _, err := tx.AmendmentBase(ctx, f.organizationID, original.ArtifactID); err != nil {
			return err
		}
		// Still inside the transaction that took the lock.
		_, probeErr = f.pool.Exec(ctx,
			`SELECT 1 FROM management_artifacts WHERE artifact_id = $1 FOR UPDATE NOWAIT`,
			original.ArtifactID)
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	if probeErr == nil {
		t.Fatal("a concurrent FOR UPDATE NOWAIT succeeded while AmendmentBase was in flight, so the " +
			"original was not locked and the digest and sequence can come from different instants")
	}

	// And the lock must be released when the transaction ends, or every
	// later reader blocks forever.
	if _, err := f.pool.Exec(ctx,
		`SELECT 1 FROM management_artifacts WHERE artifact_id = $1 FOR UPDATE NOWAIT`,
		original.ArtifactID); err != nil {
		t.Fatalf("the lock outlived its transaction: %v", err)
	}
}

// TestAuthorKindBackstopFiresInSQL proves the acceptance statement's own
// author-kind condition, independently of the seam's creation check.
//
// It writes a system-authored Management artifact DIRECTLY, bypassing the
// seam, to reach the state the creation check would have prevented. That is
// the only way to exercise a backstop: if the guard in front of it always
// holds, the backstop is never reached and an assertion about it would be
// vacuous. Reaching it must fail, and as an invariant failure rather than a
// rejection -- the row should not exist.
func TestAuthorKindBackstopFiresInSQL(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A well-formed draft, then re-point its author at a system principal
	// behind the seam's back.
	artifact := f.createDraft(t, `{"title":"x"}`)
	if _, err := f.pool.Exec(ctx,
		`UPDATE management_artifacts SET author_instance_id = $1 WHERE artifact_id = $2`,
		f.systemAgent, artifact.ArtifactID); err != nil {
		t.Fatalf("re-point author: %v", err)
	}

	rev := f.review(t, artifact.ArtifactID, artifact.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)
	err := f.store.AcceptArtifact(ctx, f.organizationID, artifact.ArtifactID, rev.ReviewID)
	if !errors.Is(err, store.ErrInvariant) {
		t.Fatalf("error = %v, want ErrInvariant; the acceptance statement must refuse a system-authored "+
			"artifact even when the seam's creation check has been bypassed", err)
	}

	reloaded, err := f.store.GetManagementArtifact(ctx, f.organizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != store.StatusDraft {
		t.Fatalf("status = %q, want draft; the refused acceptance still wrote", reloaded.Status)
	}
}

// TestPrincipalIdentifiersAreUUIDv7 completes the identifier rule. The
// earlier test covered artifacts and reviews and left principals on
// uuid.New(), which is v4 -- so the one table whose rows are written most
// often kept the scattering keys the rule exists to avoid.
func TestPrincipalIdentifiersAreUUIDv7(t *testing.T) {
	f := newFixture(t)

	instance, err := f.store.CreatePrincipalInstance(context.Background(), f.agentInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := instance.PrincipalInstanceID.Version(); got != 7 {
		t.Fatalf("principal instance id is UUID version %d, want 7", got)
	}
	// The fixture's own principals go through the same path.
	for name, id := range map[string]uuid.UUID{"author": f.author, "reviewer": f.reviewer, "system": f.systemAgent} {
		if got := id.Version(); got != 7 {
			t.Errorf("%s principal id is UUID version %d, want 7", name, got)
		}
	}
}

// TestAmendmentTargetStatusIsRecheckedAtAcceptance covers the window
// between writing an amendment and accepting it.
//
// Creation requires an accepted original, but review takes time and status
// moves. Without a recheck, archiving the original in between still lets
// the amendment be accepted -- attaching new accepted content to something
// retired, and folding it into an effective view nobody expects to change.
func TestAmendmentTargetStatusIsRecheckedAtAcceptance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)
	base := f.base(t, original.ArtifactID)
	amendment := f.createAmendment(t, original.ArtifactID, `{"title":"two"}`)
	rev := f.review(t, amendment.ArtifactID, amendment.ReviewDigest, store.DecisionAccepted, f.reviewer, &base)

	// The original is retired after the amendment was written and reviewed.
	if err := f.store.ArchiveArtifact(ctx, f.organizationID, original.ArtifactID); err != nil {
		t.Fatalf("archive original: %v", err)
	}

	err := f.store.AcceptAmendment(ctx, f.organizationID, amendment.ArtifactID, rev.ReviewID)
	var rejection *store.TransitionRejected
	if !errors.As(err, &rejection) || rejection.Reason != store.ReasonWrongStatus {
		t.Fatalf("error = %v, want ReasonWrongStatus naming the retired original", err)
	}

	reloaded, err := f.store.GetManagementArtifact(ctx, f.organizationID, amendment.ArtifactID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != store.StatusDraft {
		t.Fatalf("amendment status = %q, want draft", reloaded.Status)
	}
}

// TestRegistryVersionsAreBoundedToInt32 closes the other silent-narrowing
// path. A configured version of 4294967297 stored as 1 would validate under
// one schema version and record another.
func TestRegistryVersionsAreBoundedToInt32(t *testing.T) {
	_, err := registry.New(map[registry.Type]registry.Entry{
		"oversized": {
			Category:       registry.CategoryManagement,
			CurrentVersion: math.MaxInt32 + 1,
			Validators:     map[int]registry.Validator{math.MaxInt32 + 1: requireTitle()},
		},
	})
	if err == nil {
		t.Fatal("a schema version beyond int32 was registered; it would narrow silently at the write")
	}
}
