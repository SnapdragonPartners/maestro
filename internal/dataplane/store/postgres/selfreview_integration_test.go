//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// The self-review invariant, on the identity ADR 0020 names.
//
// The seam compared principal INSTANCE ids, and a principal instance is one
// lifetime: the same operator running a command twice has two of them, so a
// human could author with one and accept with the other. The invariant reads
// as enforced and was not — and nothing in the existing suite could have
// caught it, because every test that exercises the rule uses one instance per
// principal.
//
// ADR 0020: "even the human operator does not self-review — a human may be an
// artifact's author or its approver, never both."

// humanPrincipal creates a human principal instance for the given user.
//
// Two calls with the same user produce two INSTANCES of one principal, which
// is the state this whole file is about and the state the fixture's own
// helpers never produce.
func (f *fixture) humanPrincipal(t *testing.T, userID uuid.UUID) uuid.UUID {
	t.Helper()
	instance, err := f.store.CreatePrincipalInstance(context.Background(), store.CreatePrincipalInstanceInput{
		Kind:           store.PrincipalHuman,
		Model:          "human-" + userID.String(),
		UserID:         &userID,
		OrganizationID: f.organizationID,
	})
	if err != nil {
		t.Fatalf("create human principal for user %s: %v", userID, err)
	}
	return instance.PrincipalInstanceID
}

// secondUser provisions a distinct accountable human.
func (f *fixture) secondUser(t *testing.T) uuid.UUID {
	t.Helper()
	out, err := f.store.BootstrapUser(context.Background(), store.BootstrapUserInput{
		Handle: "second", DisplayName: "Second Operator", OrganizationID: f.organizationID,
	})
	if err != nil {
		t.Fatalf("bootstrap second user: %v", err)
	}
	return out.Record.UserID
}

// draftAuthoredBy writes a draft Management artifact with the given author.
func (f *fixture) draftAuthoredBy(t *testing.T, author uuid.UUID) *store.ManagementArtifact {
	t.Helper()
	artifact, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Type:             testType,
		Summary:          "self-review probe",
		Payload:          []byte(`{"title":"self-review probe"}`),
		Scope:            store.Scope{Type: store.ScopeOrganization, ID: f.organizationID},
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: author,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return artifact
}

// reviewBy records an acceptance of the artifact by the given reviewer.
func (f *fixture) reviewBy(t *testing.T, artifact *store.ManagementArtifact, reviewer uuid.UUID) uuid.UUID {
	t.Helper()
	review, err := f.store.CreateReview(context.Background(), store.CreateReviewInput{
		ReviewDigest:       artifact.ReviewDigest,
		Rationale:          "looks right",
		Decision:           store.DecisionAccepted,
		OrganizationID:     f.organizationID,
		ArtifactID:         artifact.ArtifactID,
		ReviewerInstanceID: reviewer,
	})
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	return review.ReviewID
}

// TestHumanCannotSelfReviewThroughASecondInstance is the regression test.
//
// It fails against the seam as it stood before this change, which is what
// makes it a regression test rather than a restatement of existing behaviour.
func TestHumanCannotSelfReviewThroughASecondInstance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// One human, two instances — a second run of the same command.
	first := f.humanPrincipal(t, f.userID)
	second := f.humanPrincipal(t, f.userID)
	if first == second {
		t.Fatal("the fixture produced one instance twice; it is not exercising the defect")
	}

	artifact := f.draftAuthoredBy(t, first)
	review := f.reviewBy(t, artifact, second)

	err := f.store.AcceptArtifact(ctx, f.organizationID, artifact.ArtifactID, review)
	var rejection *store.TransitionRejected
	if !errors.As(err, &rejection) {
		t.Fatalf("self-review through a second instance must be refused, got: %v", err)
	}
	if rejection.Reason != store.ReasonReviewerIsAuthorUser {
		t.Fatalf("refused with %q, want %q: the instance check cannot see this case",
			rejection.Reason, store.ReasonReviewerIsAuthorUser)
	}

	// And the artifact is untouched — a refused transition writes nothing.
	stored, err := f.store.GetManagementArtifact(ctx, f.organizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Status != store.StatusDraft {
		t.Errorf("the refused acceptance left status %q", stored.Status)
	}
}

// TestDistinctHumansMayReviewEachOther is the control. Without it the rule
// above could be passing by refusing every human review.
func TestDistinctHumansMayReviewEachOther(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	author := f.humanPrincipal(t, f.userID)
	reviewer := f.humanPrincipal(t, f.secondUser(t))

	artifact := f.draftAuthoredBy(t, author)
	review := f.reviewBy(t, artifact, reviewer)

	if err := f.store.AcceptArtifact(ctx, f.organizationID, artifact.ArtifactID, review); err != nil {
		t.Fatalf("two distinct humans must be able to review each other: %v", err)
	}
	stored, err := f.store.GetManagementArtifact(ctx, f.organizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Status != store.StatusAccepted {
		t.Errorf("status is %q, want accepted", stored.Status)
	}
}

// TestAccountableHumanMayReviewAnAgentsArtifact is the boundary that decides
// which column the rule compares.
//
// An agent-authored artifact carries the accountable human in its user_id.
// If the rule compared against THAT column instead of the author principal's
// user, this would be refused — and it is exactly the single-operator
// workflow ADR 0020 endorses, where one human's agent produces work and the
// human reviews it.
func TestAccountableHumanMayReviewAnAgentsArtifact(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// f.author is an agent principal; f.userID is the accountable human the
	// artifact carries.
	artifact := f.draftAuthoredBy(t, f.author)
	reviewer := f.humanPrincipal(t, f.userID)
	review := f.reviewBy(t, artifact, reviewer)

	if err := f.store.AcceptArtifact(ctx, f.organizationID, artifact.ArtifactID, review); err != nil {
		t.Fatalf("the accountable human must be able to review their agent's artifact: %v", err)
	}
}

// TestAgentsSharingAModelMayReviewEachOther is the other boundary.
//
// ADR 0020 makes distinct reviewer model routing a preference — "where
// practical", an M lever and a Phase 5 deliverable — not the invariant.
// Refusing two instances of one model here would enforce a Phase 5 policy
// through a Phase 2 constraint, and would break the ordinary case of two
// agents of the same model reviewing each other.
func TestAgentsSharingAModelMayReviewEachOther(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	sameModel := func() uuid.UUID {
		agentType := "coder"
		instance, err := f.store.CreatePrincipalInstance(ctx, store.CreatePrincipalInstanceInput{
			Kind: store.PrincipalAgent, Model: "claude-opus-5", AgentType: &agentType,
			OrganizationID: f.organizationID,
		})
		if err != nil {
			t.Fatalf("create agent principal: %v", err)
		}
		return instance.PrincipalInstanceID
	}

	artifact := f.draftAuthoredBy(t, sameModel())
	review := f.reviewBy(t, artifact, sameModel())

	if err := f.store.AcceptArtifact(ctx, f.organizationID, artifact.ArtifactID, review); err != nil {
		t.Fatalf("two agent instances of one model must still be able to review: %v", err)
	}
}

// TestSelfReviewIsRefusedForAmendmentsToo: the amendment path has its own
// statement and its own classification call, so it needs its own case. A fix
// applied to one and not the other leaves half the rule missing.
func TestSelfReviewIsRefusedForAmendmentsToo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	author := f.humanPrincipal(t, f.userID)
	other := f.humanPrincipal(t, f.secondUser(t))

	// An accepted original, reviewed by someone else.
	original := f.draftAuthoredBy(t, author)
	if err := f.store.AcceptArtifact(ctx, f.organizationID, original.ArtifactID,
		f.reviewBy(t, original, other)); err != nil {
		t.Fatalf("accept original: %v", err)
	}

	amendment, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Type:             testType,
		Summary:          "amendment",
		Payload:          []byte(`{"title":"amendment"}`),
		Scope:            store.Scope{Type: store.ScopeOrganization, ID: f.organizationID},
		AmendsArtifactID: &original.ArtifactID,
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: author,
	})
	if err != nil {
		t.Fatalf("create amendment: %v", err)
	}

	// The author's OTHER instance tries to accept their own amendment.
	// An amendment's review must record the base it was reviewed against
	// (item 4, design D6), read at one instant under the original's lock.
	base, err := f.store.AmendmentBase(ctx, f.organizationID, original.ArtifactID)
	if err != nil {
		t.Fatalf("amendment base: %v", err)
	}
	second := f.humanPrincipal(t, f.userID)
	review, err := f.store.CreateReview(ctx, store.CreateReviewInput{
		ReviewDigest:       amendment.ReviewDigest,
		BaseDigest:         &base.Digest,
		BaseSequence:       &base.Sequence,
		Rationale:          "looks right",
		Decision:           store.DecisionAccepted,
		OrganizationID:     f.organizationID,
		ArtifactID:         amendment.ArtifactID,
		ReviewerInstanceID: second,
	})
	if err != nil {
		t.Fatalf("create amendment review: %v", err)
	}

	err = f.store.AcceptAmendment(ctx, f.organizationID, amendment.ArtifactID, review.ReviewID)
	var rejection *store.TransitionRejected
	if !errors.As(err, &rejection) || rejection.Reason != store.ReasonReviewerIsAuthorUser {
		t.Fatalf("amendment self-review must be refused with %q, got: %v",
			store.ReasonReviewerIsAuthorUser, err)
	}
}
