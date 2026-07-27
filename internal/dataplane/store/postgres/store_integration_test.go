//go:build integration

package postgres_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/canonical"
	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/stack"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// testType is registered per-test rather than globally: the registry ships
// no vocabulary, and a package-level registration would be exactly the
// shared mutable state the freeze exists to prevent.
const testType registry.Type = "test_spec"

// requireTitle is a stand-in schema validator. It is deliberately weak --
// item 4 owns the seam that CALLS validators, not the schemas themselves --
// but it must actually reject something, or every validation test would
// pass against a seam that never called it.
func requireTitle() registry.Validator {
	return registry.ValidatorFunc(func(payload []byte) error {
		var decoded struct {
			Title *string `json:"title"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return fmt.Errorf("payload is not an object: %w", err)
		}
		if decoded.Title == nil {
			return errors.New(`field "title" is required`)
		}
		return nil
	})
}

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	built, err := registry.New(map[registry.Type]registry.Entry{
		testType: {
			Category:       registry.CategoryManagement,
			CurrentVersion: 1,
			Validators:     map[int]registry.Validator{1: requireTitle()},
		},
		"test_event": {
			Category:       registry.CategoryAudit,
			CurrentVersion: 1,
			Validators:     map[int]registry.Validator{1: requireTitle()},
		},
	})
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	return built
}

// disposableDatabase mirrors the migrations suite's helper: a uniquely
// named database, migrated, dropped at the end. Tests here write rows and
// must never do so in the developer's working database.
func disposableDatabase(t *testing.T) string {
	t.Helper()

	roots, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve roots: %v", err)
	}
	cfg, err := stack.NewConfig(roots)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	rootKey, err := paths.EnsureKey(roots.Config)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	adminDSN, err := cfg.DSN(rootKey)
	if err != nil {
		t.Fatalf("admin dsn: %v", err)
	}
	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	// Not deferred: t.Cleanup runs after this returns, so a deferred close
	// would leave the drop holding a closed connection.
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Skipf("data plane unavailable (run `make dataplane-up`): %v", err)
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	name := "maestro_store_" + hex.EncodeToString(suffix)

	if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		_ = admin.Close()
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() {
		defer func() { _ = admin.Close() }()
		if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})

	dsn, err := cfg.DSNFor(rootKey, name)
	if err != nil {
		t.Fatalf("dsn for %s: %v", name, err)
	}
	if err := migrations.Up(context.Background(), dsn); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	return dsn
}

// fixture is one organization with the principals the acceptance rules need
// to be distinguishable: an author, a separate reviewer, and a system
// principal that must never be able to accept.
type fixture struct {
	store *postgres.Store
	pool  *pgxpool.Pool

	organizationID uuid.UUID
	otherOrgID     uuid.UUID
	userID         uuid.UUID
	author         uuid.UUID
	reviewer       uuid.UUID
	systemAgent    uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	dsn := disposableDatabase(t)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	built, err := postgres.New(pool, testRegistry(t))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	f := &fixture{
		store:          built,
		pool:           pool,
		organizationID: uuid.New(),
		otherOrgID:     uuid.New(),
		userID:         uuid.New(),
	}

	for _, org := range []struct {
		id   uuid.UUID
		slug string
	}{{f.organizationID, "primary"}, {f.otherOrgID, "other"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1, $2, $3)`,
			org.id, org.slug, org.slug); err != nil {
			t.Fatalf("insert organization: %v", err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1, $2, $3, $4)`,
		f.userID, f.organizationID, "tester", "Tester"); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	f.author = f.newPrincipal(t, store.PrincipalAgent, "author-model")
	f.reviewer = f.newPrincipal(t, store.PrincipalAgent, "reviewer-model")
	f.systemAgent = f.newPrincipal(t, store.PrincipalSystem, "system")
	return f
}

func (f *fixture) newPrincipal(t *testing.T, kind store.PrincipalKind, model string) uuid.UUID {
	t.Helper()
	input := store.CreatePrincipalInstanceInput{
		Kind:           kind,
		Model:          model,
		OrganizationID: f.organizationID,
	}
	// The schema requires agent_type exactly when kind is 'agent'.
	if kind == store.PrincipalAgent {
		agentType := "coder"
		input.AgentType = &agentType
	}
	instance, err := f.store.CreatePrincipalInstance(context.Background(), input)
	if err != nil {
		t.Fatalf("create %s principal: %v", kind, err)
	}
	return instance.PrincipalInstanceID
}

// agentInput builds a valid agent instance input, so tests that are not
// about the kind/field rules do not have to restate them.
func (f *fixture) agentInput() store.CreatePrincipalInstanceInput {
	agentType := "coder"
	return store.CreatePrincipalInstanceInput{
		Kind:           store.PrincipalAgent,
		Model:          "m",
		AgentType:      &agentType,
		OrganizationID: f.organizationID,
	}
}

// TestPrincipalKindFieldRulesAreEnforcedAtTheSeam covers the schema's two
// biconditional constraints from both directions. The seam checks them so a
// caller reads which field it omitted rather than a constraint name.
func TestPrincipalKindFieldRulesAreEnforcedAtTheSeam(t *testing.T) {
	f := newFixture(t)
	agentType := "coder"

	cases := []struct {
		name  string
		input store.CreatePrincipalInstanceInput
	}{
		{"agent without an agent type", store.CreatePrincipalInstanceInput{
			Kind: store.PrincipalAgent, Model: "m", OrganizationID: f.organizationID}},
		{"agent carrying a user id", store.CreatePrincipalInstanceInput{
			Kind: store.PrincipalAgent, Model: "m", AgentType: &agentType, UserID: &f.userID, OrganizationID: f.organizationID}},
		{"human without a user id", store.CreatePrincipalInstanceInput{
			Kind: store.PrincipalHuman, Model: "m", OrganizationID: f.organizationID}},
		{"human carrying an agent type", store.CreatePrincipalInstanceInput{
			Kind: store.PrincipalHuman, Model: "m", AgentType: &agentType, UserID: &f.userID, OrganizationID: f.organizationID}},
		{"system carrying an agent type", store.CreatePrincipalInstanceInput{
			Kind: store.PrincipalSystem, Model: "m", AgentType: &agentType, OrganizationID: f.organizationID}},
		{"system carrying a user id", store.CreatePrincipalInstanceInput{
			Kind: store.PrincipalSystem, Model: "m", UserID: &f.userID, OrganizationID: f.organizationID}},
		{"unknown kind", store.CreatePrincipalInstanceInput{
			Kind: "supervisor", Model: "m", OrganizationID: f.organizationID}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := f.store.CreatePrincipalInstance(context.Background(), testCase.input)
			if err == nil {
				t.Fatal("expected the seam to refuse this combination")
			}
			// It must be refused BEFORE reaching Postgres, or the caller
			// gets a constraint name instead of a diagnosis.
			if strings.Contains(err.Error(), "SQLSTATE") {
				t.Fatalf("refused by the database rather than the seam: %v", err)
			}
		})
	}

	// The valid combinations must still be accepted, or the rules above
	// would be satisfied by refusing everything.
	human, err := f.store.CreatePrincipalInstance(context.Background(), store.CreatePrincipalInstanceInput{
		Kind: store.PrincipalHuman, Model: "human", UserID: &f.userID, OrganizationID: f.organizationID})
	if err != nil {
		t.Fatalf("valid human principal was refused: %v", err)
	}
	if human.UserID == nil || *human.UserID != f.userID {
		t.Fatalf("human user id did not round-trip: %v", human.UserID)
	}
}

func (f *fixture) scope() store.Scope {
	return store.Scope{Type: store.ScopeOrganization, ID: f.organizationID}
}

// createDraft writes a draft Management artifact authored by f.author.
func (f *fixture) createDraft(t *testing.T, payload string) *store.ManagementArtifact {
	t.Helper()
	artifact, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(payload),
		Type:             testType,
		Summary:          "a draft",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return artifact
}

// review records an acceptance by f.reviewer against the digest given.
func (f *fixture) review(t *testing.T, artifactID uuid.UUID, digest string, decision store.Decision, reviewer uuid.UUID, base *store.AmendmentBase) *store.Review {
	t.Helper()
	input := store.CreateReviewInput{
		ReviewDigest:       digest,
		Rationale:          "because",
		Decision:           decision,
		OrganizationID:     f.organizationID,
		ArtifactID:         artifactID,
		ReviewerInstanceID: reviewer,
	}
	// The base is recorded as a pair or not at all.
	if base != nil {
		digestCopy := base.Digest
		sequenceCopy := base.Sequence
		input.BaseDigest = &digestCopy
		input.BaseSequence = &sequenceCopy
	}
	created, err := f.store.CreateReview(context.Background(), input)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	return created
}

// --- creation and digests --------------------------------------------------

func TestCreateDerivesCategoryVersionAndDigests(t *testing.T) {
	f := newFixture(t)
	artifact := f.createDraft(t, `{"title":"one"}`)

	if artifact.Category != registry.CategoryManagement {
		t.Fatalf("category = %q, want management (it must come from the registry, not the caller)", artifact.Category)
	}
	if artifact.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", artifact.SchemaVersion)
	}
	if artifact.Status != store.StatusDraft {
		t.Fatalf("status = %q, want draft", artifact.Status)
	}

	// The digest must be derived, not asserted: recomputing it here from
	// the payload is the check that the seam did not simply store
	// something a caller handed it.
	want, err := canonical.DigestJSON(artifact.Payload)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if artifact.PayloadDigest != want {
		t.Fatalf("payload digest = %s, want %s", artifact.PayloadDigest, want)
	}
	if artifact.ReviewDigest == artifact.PayloadDigest {
		t.Fatal("review digest equals payload digest; the review digest must cover the whole reviewable " +
			"projection (ADR 0028 §5), so a summary change must move it")
	}
}

func TestCreateRejectsUnregisteredType(t *testing.T) {
	f := newFixture(t)
	_, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(`{"title":"x"}`),
		Type:             "never_registered",
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if !errors.Is(err, registry.ErrUnknownType) {
		t.Fatalf("error = %v, want ErrUnknownType", err)
	}
}

func TestCreateRejectsPayloadFailingItsSchema(t *testing.T) {
	f := newFixture(t)
	_, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(`{"no_title":true}`),
		Type:             testType,
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if err == nil {
		t.Fatal("expected the registered validator to reject a payload with no title")
	}
}

func TestCreateRejectsUnsafeNumbers(t *testing.T) {
	f := newFixture(t)
	_, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(`{"title":"x","count":9007199254740993}`),
		Type:             testType,
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if !errors.Is(err, canonical.ErrUnsafeNumber) {
		t.Fatalf("error = %v, want ErrUnsafeNumber", err)
	}
}

func TestCreateRejectsAuditTypeInManagementFamily(t *testing.T) {
	f := newFixture(t)
	_, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(`{"title":"x"}`),
		Type:             "test_event", // registered as Audit
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if err == nil {
		t.Fatal("expected an Audit-registered type to be refused in the Management family")
	}
}

// --- acceptance ------------------------------------------------------------

func TestAcceptHappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	artifact := f.createDraft(t, `{"title":"one"}`)
	rev := f.review(t, artifact.ArtifactID, artifact.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)

	if err := f.store.AcceptArtifact(ctx, f.organizationID, artifact.ArtifactID, rev.ReviewID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	reloaded, err := f.store.GetManagementArtifact(ctx, f.organizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != store.StatusAccepted {
		t.Fatalf("status = %q, want accepted", reloaded.Status)
	}
	if reloaded.AcceptedAt == nil {
		t.Fatal("accepted_at was not set")
	}
	// The reviewer is taken from the review, never from a caller argument.
	if reloaded.ReviewerInstanceID == nil || *reloaded.ReviewerInstanceID != f.reviewer {
		t.Fatalf("reviewer = %v, want %s", reloaded.ReviewerInstanceID, f.reviewer)
	}
}

// TestAcceptRejections walks the design's matrix. Each case must fail for
// its OWN reason: a test that only asserted "an error happened" would pass
// against a seam that refused everything.
func TestAcceptRejections(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture) (artifactID, reviewID uuid.UUID)
		want  store.RejectionReason
	}{
		{
			name: "reviewer is the author",
			setup: func(t *testing.T, f *fixture) (uuid.UUID, uuid.UUID) {
				a := f.createDraft(t, `{"title":"x"}`)
				r := f.review(t, a.ArtifactID, a.ReviewDigest, store.DecisionAccepted, f.author, nil)
				return a.ArtifactID, r.ReviewID
			},
			want: store.ReasonReviewerIsAuthor,
		},
		{
			name: "reviewer is a system principal",
			setup: func(t *testing.T, f *fixture) (uuid.UUID, uuid.UUID) {
				a := f.createDraft(t, `{"title":"x"}`)
				r := f.review(t, a.ArtifactID, a.ReviewDigest, store.DecisionAccepted, f.systemAgent, nil)
				return a.ArtifactID, r.ReviewID
			},
			want: store.ReasonReviewerKind,
		},
		{
			name: "review is a rejection",
			setup: func(t *testing.T, f *fixture) (uuid.UUID, uuid.UUID) {
				a := f.createDraft(t, `{"title":"x"}`)
				r := f.review(t, a.ArtifactID, a.ReviewDigest, store.DecisionRejected, f.reviewer, nil)
				return a.ArtifactID, r.ReviewID
			},
			want: store.ReasonReviewNotAccept,
		},
		{
			name: "review digest does not match current content",
			setup: func(t *testing.T, f *fixture) (uuid.UUID, uuid.UUID) {
				a := f.createDraft(t, `{"title":"x"}`)
				stale := "0000000000000000000000000000000000000000000000000000000000000000"
				r := f.review(t, a.ArtifactID, stale, store.DecisionAccepted, f.reviewer, nil)
				return a.ArtifactID, r.ReviewID
			},
			want: store.ReasonDigestMismatch,
		},
		{
			name: "review belongs to a different artifact",
			setup: func(t *testing.T, f *fixture) (uuid.UUID, uuid.UUID) {
				a := f.createDraft(t, `{"title":"x"}`)
				other := f.createDraft(t, `{"title":"y"}`)
				r := f.review(t, other.ArtifactID, other.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)
				return a.ArtifactID, r.ReviewID
			},
			want: store.ReasonReviewNotFound,
		},
		{
			name: "artifact is already accepted",
			setup: func(t *testing.T, f *fixture) (uuid.UUID, uuid.UUID) {
				a := f.createDraft(t, `{"title":"x"}`)
				r := f.review(t, a.ArtifactID, a.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)
				if err := f.store.AcceptArtifact(context.Background(), f.organizationID, a.ArtifactID, r.ReviewID); err != nil {
					t.Fatalf("first accept: %v", err)
				}
				return a.ArtifactID, r.ReviewID
			},
			want: store.ReasonWrongStatus,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := newFixture(t)
			artifactID, reviewID := testCase.setup(t, f)

			err := f.store.AcceptArtifact(context.Background(), f.organizationID, artifactID, reviewID)

			var rejection *store.TransitionRejected
			if !errors.As(err, &rejection) {
				t.Fatalf("error = %v, want a TransitionRejected", err)
			}
			if rejection.Reason != testCase.want {
				t.Fatalf("reason = %q, want %q", rejection.Reason, testCase.want)
			}
		})
	}
}

// --- amendments ------------------------------------------------------------

// acceptedOriginal returns an accepted original artifact.
func acceptedOriginal(t *testing.T, f *fixture, payload string) *store.ManagementArtifact {
	t.Helper()
	artifact := f.createDraft(t, payload)
	rev := f.review(t, artifact.ArtifactID, artifact.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)
	if err := f.store.AcceptArtifact(context.Background(), f.organizationID, artifact.ArtifactID, rev.ReviewID); err != nil {
		t.Fatalf("accept original: %v", err)
	}
	return artifact
}

func (f *fixture) createAmendment(t *testing.T, originalID uuid.UUID, patch string) *store.ManagementArtifact {
	t.Helper()
	amendment, err := f.store.CreateManagementArtifact(context.Background(), store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(patch),
		AmendsArtifactID: &originalID,
		Type:             testType,
		Summary:          "an amendment",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create amendment: %v", err)
	}
	return amendment
}

func (f *fixture) base(t *testing.T, originalID uuid.UUID) store.AmendmentBase {
	t.Helper()
	got, err := f.store.AmendmentBase(context.Background(), f.organizationID, originalID)
	if err != nil {
		t.Fatalf("amendment base: %v", err)
	}
	return got
}

func TestEffectiveViewAppliesAcceptedAmendmentsInOrder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one","keep":"yes","drop":"me"}`)

	// First amendment: change the title and delete a key.
	base := f.base(t, original.ArtifactID)
	first := f.createAmendment(t, original.ArtifactID, `{"title":"two","drop":null}`)
	firstReview := f.review(t, first.ArtifactID, first.ReviewDigest, store.DecisionAccepted, f.reviewer, &base)
	if err := f.store.AcceptAmendment(ctx, f.organizationID, first.ArtifactID, firstReview.ReviewID); err != nil {
		t.Fatalf("accept first amendment: %v", err)
	}

	// Second amendment, reviewed against the NEW base.
	base2 := f.base(t, original.ArtifactID)
	second := f.createAmendment(t, original.ArtifactID, `{"title":"three"}`)
	secondReview := f.review(t, second.ArtifactID, second.ReviewDigest, store.DecisionAccepted, f.reviewer, &base2)
	if err := f.store.AcceptAmendment(ctx, f.organizationID, second.ArtifactID, secondReview.ReviewID); err != nil {
		t.Fatalf("accept second amendment: %v", err)
	}

	view, err := f.store.EffectiveView(ctx, f.organizationID, original.ArtifactID)
	if err != nil {
		t.Fatalf("effective view: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(view, &decoded); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if decoded["title"] != "three" {
		t.Fatalf("title = %v, want three (amendments must apply in sequence order)", decoded["title"])
	}
	if decoded["keep"] != "yes" {
		t.Fatalf("keep = %v, want yes (an untouched key must survive)", decoded["keep"])
	}
	if _, present := decoded["drop"]; present {
		t.Fatal("a key deleted by an accepted amendment is still present in the effective view")
	}
}

// TestDraftAmendmentDoesNotAffectTheEffectiveView is the other half: only
// ACCEPTED amendments contribute, so an unreviewed patch cannot change what
// readers see.
func TestDraftAmendmentDoesNotAffectTheEffectiveView(t *testing.T) {
	f := newFixture(t)
	original := acceptedOriginal(t, f, `{"title":"one"}`)
	f.createAmendment(t, original.ArtifactID, `{"title":"sneaky"}`)

	view, err := f.store.EffectiveView(context.Background(), f.organizationID, original.ArtifactID)
	if err != nil {
		t.Fatalf("effective view: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(view, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["title"] != "one" {
		t.Fatalf("title = %v, want one; a DRAFT amendment changed the effective view", decoded["title"])
	}
}

// TestTwoAmendmentsOnTheSameBaseYieldOneAcceptance is design D6's contract,
// and the reason the max(sequence)+1-with-retry approach was rejected: that
// version would have accepted both, which is exactly what ADR 0028 forbids.
func TestTwoAmendmentsOnTheSameBaseYieldOneAcceptance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)
	base := f.base(t, original.ArtifactID)

	first := f.createAmendment(t, original.ArtifactID, `{"title":"from-first"}`)
	second := f.createAmendment(t, original.ArtifactID, `{"title":"from-second"}`)

	firstReview := f.review(t, first.ArtifactID, first.ReviewDigest, store.DecisionAccepted, f.reviewer, &base)
	secondReview := f.review(t, second.ArtifactID, second.ReviewDigest, store.DecisionAccepted, f.reviewer, &base)

	if err := f.store.AcceptAmendment(ctx, f.organizationID, first.ArtifactID, firstReview.ReviewID); err != nil {
		t.Fatalf("accept first: %v", err)
	}

	err := f.store.AcceptAmendment(ctx, f.organizationID, second.ArtifactID, secondReview.ReviewID)
	if !errors.Is(err, store.ErrBaseMoved) {
		t.Fatalf("second acceptance error = %v, want ErrBaseMoved; both amendments were reviewed against "+
			"the same base, so the second must require re-review", err)
	}

	reloaded, err := f.store.GetManagementArtifact(ctx, f.organizationID, second.ArtifactID)
	if err != nil {
		t.Fatalf("reload second: %v", err)
	}
	if reloaded.Status != store.StatusDraft {
		t.Fatalf("second amendment status = %q, want draft", reloaded.Status)
	}
}

// TestAmendmentInheritsVersionFromTheOriginal covers design D3's registry
// exception. The registry advances to v2 while a v1 artifact exists; the
// amendment must stay v1, because stored payloads are never rewritten.
func TestAmendmentInheritsVersionFromTheOriginal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)
	if original.SchemaVersion != 1 {
		t.Fatalf("original version = %d, want 1", original.SchemaVersion)
	}

	// Advance the registry to v2, as a later build would.
	advanced, err := registry.New(map[registry.Type]registry.Entry{
		testType: {
			Category:       registry.CategoryManagement,
			CurrentVersion: 2,
			Validators:     map[int]registry.Validator{1: requireTitle(), 2: requireTitle()},
		},
	})
	if err != nil {
		t.Fatalf("advanced registry: %v", err)
	}
	advancedStore, err := postgres.New(f.pool, advanced)
	if err != nil {
		t.Fatalf("advanced store: %v", err)
	}

	amendment, err := advancedStore.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(`{"title":"two"}`),
		AmendsArtifactID: &original.ArtifactID,
		Type:             testType,
		Summary:          "amendment under a v2 registry",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create amendment: %v", err)
	}
	if amendment.SchemaVersion != 1 {
		t.Fatalf("amendment version = %d, want 1; an amendment must inherit the original's version, not the "+
			"registry's current one, or an immutable artifact is silently migrated", amendment.SchemaVersion)
	}
}

func TestAmendmentOfAnAmendmentIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)
	base := f.base(t, original.ArtifactID)
	amendment := f.createAmendment(t, original.ArtifactID, `{"title":"two"}`)
	rev := f.review(t, amendment.ArtifactID, amendment.ReviewDigest, store.DecisionAccepted, f.reviewer, &base)
	if err := f.store.AcceptAmendment(ctx, f.organizationID, amendment.ArtifactID, rev.ReviewID); err != nil {
		t.Fatalf("accept amendment: %v", err)
	}

	_, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(`{"title":"three"}`),
		AmendsArtifactID: &amendment.ArtifactID,
		Type:             testType,
		Summary:          "amendment of an amendment",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		UserID:           f.userID,
		AuthorInstanceID: f.author,
	})
	if err == nil {
		t.Fatal("expected an amendment of an amendment to be refused; the chain is flat (ADR 0021)")
	}
}

func TestArchiveAndSupersedeRefuseAmendments(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	original := acceptedOriginal(t, f, `{"title":"one"}`)
	base := f.base(t, original.ArtifactID)
	amendment := f.createAmendment(t, original.ArtifactID, `{"title":"two"}`)
	rev := f.review(t, amendment.ArtifactID, amendment.ReviewDigest, store.DecisionAccepted, f.reviewer, &base)
	if err := f.store.AcceptAmendment(ctx, f.organizationID, amendment.ArtifactID, rev.ReviewID); err != nil {
		t.Fatalf("accept amendment: %v", err)
	}

	var rejection *store.TransitionRejected
	if err := f.store.ArchiveArtifact(ctx, f.organizationID, amendment.ArtifactID); !errors.As(err, &rejection) ||
		rejection.Reason != store.ReasonIsAmendment {
		t.Fatalf("archive error = %v, want ReasonIsAmendment; archiving an amendment would drop its "+
			"contribution from an effective view nobody re-reviewed", err)
	}
}

// --- supersession ----------------------------------------------------------

func TestSupersedeRequiresTheSupersedingArtifactToNameItsTarget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	targetA := acceptedOriginal(t, f, `{"title":"A"}`)
	targetB := acceptedOriginal(t, f, `{"title":"B"}`)

	// A replacement reviewed and authored as superseding A.
	replacement, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload:              json.RawMessage(`{"title":"replacement"}`),
		SupersedesArtifactID: &targetA.ArtifactID,
		Type:                 testType,
		Summary:              "replaces A",
		Scope:                f.scope(),
		OrganizationID:       f.organizationID,
		UserID:               f.userID,
		AuthorInstanceID:     f.author,
	})
	if err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	rev := f.review(t, replacement.ArtifactID, replacement.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)

	// Pointing it at B must be refused: the reviewer approved a
	// replacement for A, and this would retire B.
	err = f.store.SupersedeArtifact(ctx, f.organizationID, targetB.ArtifactID, replacement.ArtifactID, rev.ReviewID)
	var rejection *store.TransitionRejected
	if !errors.As(err, &rejection) || rejection.Reason != store.ReasonSupersedeTarget {
		t.Fatalf("error = %v, want ReasonSupersedeTarget", err)
	}

	// And B must be untouched.
	reloaded, err := f.store.GetManagementArtifact(ctx, f.organizationID, targetB.ArtifactID)
	if err != nil {
		t.Fatalf("reload B: %v", err)
	}
	if reloaded.Status != store.StatusAccepted {
		t.Fatalf("B status = %q, want accepted", reloaded.Status)
	}
}

// TestSupersessionIsAtomic checks that acceptance and retirement land
// together. A reader between the two statements would otherwise observe two
// authoritative artifacts for one subject.
func TestSupersessionIsAtomic(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	target := acceptedOriginal(t, f, `{"title":"old"}`)
	replacement, err := f.store.CreateManagementArtifact(ctx, store.CreateManagementArtifactInput{
		Payload:              json.RawMessage(`{"title":"new"}`),
		SupersedesArtifactID: &target.ArtifactID,
		Type:                 testType,
		Summary:              "replaces it",
		Scope:                f.scope(),
		OrganizationID:       f.organizationID,
		UserID:               f.userID,
		AuthorInstanceID:     f.author,
	})
	if err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	rev := f.review(t, replacement.ArtifactID, replacement.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)

	if err := f.store.SupersedeArtifact(ctx, f.organizationID, target.ArtifactID, replacement.ArtifactID, rev.ReviewID); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	reloadedTarget, err := f.store.GetManagementArtifact(ctx, f.organizationID, target.ArtifactID)
	if err != nil {
		t.Fatalf("reload target: %v", err)
	}
	reloadedReplacement, err := f.store.GetManagementArtifact(ctx, f.organizationID, replacement.ArtifactID)
	if err != nil {
		t.Fatalf("reload replacement: %v", err)
	}
	if reloadedTarget.Status != store.StatusSuperseded {
		t.Fatalf("target status = %q, want superseded", reloadedTarget.Status)
	}
	if reloadedReplacement.Status != store.StatusAccepted {
		t.Fatalf("replacement status = %q, want accepted", reloadedReplacement.Status)
	}
}

// --- principal instances ---------------------------------------------------

// TestInstanceAndSeedsAreAtomic is ADR 0021's promise that "what was this
// agent given to start?" is always a query. A failing seed must leave no
// instance behind, or the promise is false for exactly as long as the gap.
func TestInstanceAndSeedsAreAtomic(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	artifact := acceptedOriginal(t, f, `{"title":"seed"}`)

	before := f.countInstances(t)
	seeded := f.agentInput()
	seeded.Seeds = []store.SeedInput{
		{ArtifactID: artifact.ArtifactID, SeededDigest: artifact.PayloadDigest},
		// A seed naming an artifact that does not exist, so the write
		// fails partway through.
		{ArtifactID: uuid.New(), SeededDigest: "deadbeef"},
	}
	_, err := f.store.CreatePrincipalInstance(ctx, seeded)
	if err == nil {
		t.Fatal("expected the second seed to fail")
	}
	if after := f.countInstances(t); after != before {
		t.Fatalf("instance count went %d -> %d; a failed seeding left an instance without its inputs", before, after)
	}
}

func (f *fixture) countInstances(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM principal_instances WHERE organization_id = $1`, f.organizationID).Scan(&count); err != nil {
		t.Fatalf("count instances: %v", err)
	}
	return count
}

func TestSeedsRecordTheDigestAsSeeded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	artifact := acceptedOriginal(t, f, `{"title":"seed"}`)
	withSeed := f.agentInput()
	withSeed.Seeds = []store.SeedInput{{ArtifactID: artifact.ArtifactID, SeededDigest: artifact.PayloadDigest}}
	instance, err := f.store.CreatePrincipalInstance(ctx, withSeed)
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}

	seeds, err := f.store.ListSeededInputs(ctx, f.organizationID, instance.PrincipalInstanceID)
	if err != nil {
		t.Fatalf("list seeds: %v", err)
	}
	if len(seeds) != 1 {
		t.Fatalf("got %d seeds, want 1", len(seeds))
	}
	if seeds[0].SeededDigest != artifact.PayloadDigest {
		t.Fatalf("seeded digest = %s, want %s", seeds[0].SeededDigest, artifact.PayloadDigest)
	}
}

// TestStopIsOnceOnlyAndIdempotent is ADR 0027's P-6 shape: two paths
// finalise one lifecycle about a millisecond apart, and the first reason --
// the diagnostic saying why the agent died -- must survive the second.
func TestStopIsOnceOnlyAndIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	instance, err := f.store.CreatePrincipalInstance(ctx, f.agentInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := f.store.StopPrincipalInstance(ctx, f.organizationID, instance.PrincipalInstanceID, "panic: nil map write")
	if err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if !first.Recorded {
		t.Fatal("first stop did not report itself as the recorder")
	}

	second, err := f.store.StopPrincipalInstance(ctx, f.organizationID, instance.PrincipalInstanceID, "clean shutdown")
	if err != nil {
		t.Fatalf("second stop returned an error; repeat stops are idempotent, not failures: %v", err)
	}
	if second.Recorded {
		t.Fatal("second stop claimed to have recorded the stop")
	}
	if second.Reason != "panic: nil map write" {
		t.Fatalf("reason = %q, want the FIRST reason; the later, blander shutdown path overwrote the "+
			"diagnostic that says why the agent died", second.Reason)
	}
	if !second.StopTime.Equal(first.StopTime) {
		t.Fatalf("stop time moved from %v to %v", first.StopTime, second.StopTime)
	}
}

func TestFindPrincipalInstancesByModel(t *testing.T) {
	f := newFixture(t)
	model := "reviewer-model"
	found, err := f.store.FindPrincipalInstances(context.Background(), store.MPHQuery{
		OrganizationID: f.organizationID,
		Model:          &model,
	})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 1 || found[0].PrincipalInstanceID != f.reviewer {
		t.Fatalf("got %d instances, want exactly the reviewer", len(found))
	}
}

func TestMPHQueryRequiresExactlyOneAxis(t *testing.T) {
	f := newFixture(t)
	model := "m"
	hash := "h"
	for _, query := range []store.MPHQuery{
		{OrganizationID: f.organizationID},
		{OrganizationID: f.organizationID, Model: &model, PromptHash: &hash},
	} {
		if _, err := f.store.FindPrincipalInstances(context.Background(), query); err == nil {
			t.Fatalf("query %+v was accepted; it must name exactly one axis", query)
		}
	}
}

// --- organization scoping --------------------------------------------------

// TestReadsAreOrganizationScoped is the multi-tenant boundary. An artifact
// id is unguessable but not a permission, and this interface is the one a
// cloud module implements.
func TestReadsAreOrganizationScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	artifact := f.createDraft(t, `{"title":"private"}`)

	if _, err := f.store.GetManagementArtifact(ctx, f.otherOrgID, artifact.ArtifactID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-organization read error = %v, want ErrNotFound", err)
	}
	if err := f.store.InvalidateArtifact(ctx, f.otherOrgID, artifact.ArtifactID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-organization write error = %v, want ErrNotFound", err)
	}

	// And the artifact is untouched.
	reloaded, err := f.store.GetManagementArtifact(ctx, f.organizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != store.StatusDraft {
		t.Fatalf("status = %q, want draft", reloaded.Status)
	}
}

// --- nullability (design D9) -----------------------------------------------

// TestNullableFieldsRoundTripAsAbsent is D9's concrete hazard: a Valid flag
// dropped on the floor turns an absent reviewer into the zero UUID -- a
// value that looks like data, reads like data, and joins like data.
func TestNullableFieldsRoundTripAsAbsent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	artifact := f.createDraft(t, `{"title":"x"}`)
	if artifact.ReviewerInstanceID != nil {
		t.Fatalf("a draft has a reviewer: %v", artifact.ReviewerInstanceID)
	}
	if artifact.AcceptedAt != nil {
		t.Fatalf("a draft has an accepted_at: %v", artifact.AcceptedAt)
	}
	if artifact.AmendmentSequence != nil {
		t.Fatalf("a non-amendment has a sequence: %v", artifact.AmendmentSequence)
	}
	if artifact.AmendsArtifactID != nil || artifact.SupersedesArtifactID != nil || artifact.ReplacesArtifactID != nil {
		t.Fatal("an unlinked artifact carries a lifecycle link")
	}

	// The populated direction, so the test cannot pass by always returning nil.
	rev := f.review(t, artifact.ArtifactID, artifact.ReviewDigest, store.DecisionAccepted, f.reviewer, nil)
	if err := f.store.AcceptArtifact(ctx, f.organizationID, artifact.ArtifactID, rev.ReviewID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	accepted, err := f.store.GetManagementArtifact(ctx, f.organizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if accepted.ReviewerInstanceID == nil || *accepted.ReviewerInstanceID != f.reviewer {
		t.Fatalf("reviewer = %v, want %s", accepted.ReviewerInstanceID, f.reviewer)
	}
	if accepted.AcceptedAt == nil {
		t.Fatal("accepted_at is absent on an accepted artifact")
	}

	// A stopped instance's optional fields, both directions.
	instance, err := f.store.CreatePrincipalInstance(ctx, f.agentInput())
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if instance.StopTime != nil || instance.StopReason != nil || instance.UserID != nil {
		t.Fatal("a fresh instance carries stop or user values")
	}
	if _, err := f.store.StopPrincipalInstance(ctx, f.organizationID, instance.PrincipalInstanceID, "done"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	stopped, err := f.store.GetPrincipalInstance(ctx, f.organizationID, instance.PrincipalInstanceID)
	if err != nil {
		t.Fatalf("reload instance: %v", err)
	}
	if stopped.StopTime == nil || stopped.StopReason == nil || *stopped.StopReason != "done" {
		t.Fatalf("stop fields did not round-trip: time=%v reason=%v", stopped.StopTime, stopped.StopReason)
	}
}

// --- audit artifacts -------------------------------------------------------

func TestAuditArtifactsAreBornFinal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	artifact, err := f.store.CreateAuditArtifact(ctx, store.CreateAuditArtifactInput{
		Payload:          json.RawMessage(`{"title":"an event"}`),
		Type:             "test_event",
		Summary:          "exhaust",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		AuthorInstanceID: f.systemAgent,
	})
	if err != nil {
		t.Fatalf("create audit artifact: %v", err)
	}
	if artifact.Category != registry.CategoryAudit {
		t.Fatalf("category = %q, want audit", artifact.Category)
	}
	// A system principal with no user is legitimate here, unlike the
	// Management family where accountability requires one.
	if artifact.UserID != nil {
		t.Fatalf("user = %v, want absent", artifact.UserID)
	}

	reloaded, err := f.store.GetAuditArtifact(ctx, f.organizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.PayloadDigest != artifact.PayloadDigest {
		t.Fatal("payload digest changed across a round trip")
	}
}

func TestManagementTypeRefusedInAuditFamily(t *testing.T) {
	f := newFixture(t)
	_, err := f.store.CreateAuditArtifact(context.Background(), store.CreateAuditArtifactInput{
		Payload:          json.RawMessage(`{"title":"x"}`),
		Type:             testType, // registered as Management
		Summary:          "s",
		Scope:            f.scope(),
		OrganizationID:   f.organizationID,
		AuthorInstanceID: f.systemAgent,
	})
	if err == nil {
		t.Fatal("expected a Management-registered type to be refused in the Audit family")
	}
}
