//go:build integration

package stack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// The cross-store fixture: contents that span BOTH stores, so the operations
// this package owns can be judged on what they actually promise.
//
// Every destructive verb here is about a Postgres cluster and an object
// store held together. A test whose plane contains only a Postgres table
// cannot tell a working backup from one that copied the database and
// forgot the bucket, and cannot exercise verification at all — a pass over
// a plane with no attachments recomputes nothing and reports healthy. The
// characteristic failure of a whole-root copy is a TORN PAIR, and a torn
// pair needs two stores to be torn between.
//
// So the fixture writes one organization, one Management artifact carrying
// an attachment and the pin that holds it, and one Audit artifact — the
// three digest families verification walks — through the real seam, into
// the real isolated plane, at its real bind-mounted paths.

// crossStoreType is a Management type that carries evidence. It is
// registered per-test: the registry ships no vocabulary, and a package-level
// registration would be the shared mutable state its freeze exists to
// prevent.
const crossStoreType registry.Type = "backup_fixture_spec"

// crossStoreEventType is the Audit half. Verification recomputes
// payload_digest for that family too, and a fixture with no Audit row leaves
// that branch of the walk unexercised.
const crossStoreEventType registry.Type = "backup_fixture_event"

// crossStoreMediaType is what the attachment declares. Named rather than
// inline because the restored row is compared against it.
const crossStoreMediaType = "application/octet-stream"

// crossStorePayload is the shape the extractor below reads.
type crossStorePayload struct {
	Title       string      `json:"title"`
	Attachments []uuid.UUID `json:"attachments"`
}

// crossStoreRegistry knows the two fixture types, with a reference extractor
// on the evidence-bearing one.
//
// The extractor is required rather than decorative: acceptance compares the
// pins against what the PAYLOAD names, so a type without one carries no
// evidence and must be written with zero pins. A fixture that pinned an
// attachment under a type with no extractor would be describing a state the
// seam refuses.
func crossStoreRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	validator := registry.ValidatorFunc(func(payload []byte) error {
		var decoded struct {
			Title *string `json:"title"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return err
		}
		if decoded.Title == nil {
			// A validator that cannot reject anything would let every
			// validation path pass against a seam that never called it.
			return errors.New(`field "title" is required`)
		}
		return nil
	})
	extractor := registry.ExtractorFunc(func(payload []byte) ([]registry.Reference, error) {
		var decoded crossStorePayload
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return nil, err
		}
		references := make([]registry.Reference, 0, len(decoded.Attachments))
		for i := range decoded.Attachments {
			references = append(references, registry.Reference{AttachmentID: &decoded.Attachments[i]})
		}
		return references, nil
	})

	built, err := registry.New(map[registry.Type]registry.Entry{
		crossStoreType: {
			Category:       registry.CategoryManagement,
			CurrentVersion: 1,
			Validators:     map[int]registry.Validator{1: validator},
			Extractors:     map[int]registry.Extractor{1: extractor},
		},
		crossStoreEventType: {
			Category:       registry.CategoryAudit,
			CurrentVersion: 1,
			Validators:     map[int]registry.Validator{1: validator},
		},
	})
	if err != nil {
		t.Fatalf("build the fixture registry: %v", err)
	}
	return built
}

// planeRootKey reads the running plane's root-of-trust key.
func planeRootKey(t *testing.T, cfg *Config) []byte {
	t.Helper()
	rootKey, err := paths.EnsureKey(cfg.Roots.Config)
	if err != nil {
		t.Fatalf("read the root-of-trust key: %v", err)
	}
	return rootKey
}

// openSeam opens the persistence seam against a RUNNING isolated plane: the
// database `up` migrated and the bucket `up` provisioned.
//
// Not a disposable database and not a disposable bucket, unlike the seam's
// own suite. Those exist so seam tests never touch the canonical pair; here
// the canonical pair of an isolated plane is precisely the subject, because
// what backup copies is the data root those two are bind-mounted into.
func openSeam(t *testing.T, cfg *Config) *postgres.Store {
	t.Helper()
	rootKey := planeRootKey(t, cfg)
	dsn, err := cfg.DSN(rootKey)
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	blob, err := ensureBucket(t.Context(), cfg, rootKey)
	if err != nil {
		t.Fatalf("reach the object store: %v", err)
	}
	keyProvider, err := resolvedRootKey(rootKey)
	if err != nil {
		t.Fatalf("wrap the root key: %v", err)
	}
	seam, err := postgres.Open(t.Context(), dsn, crossStoreRegistry(t), blob, keyProvider)
	if err != nil {
		t.Fatalf("open the persistence seam: %v", err)
	}
	t.Cleanup(seam.Close)
	return seam
}

// crossStoreSeed is what a fixture wrote, in the terms a later assertion
// looks for it in.
type crossStoreSeed struct {
	Body           []byte
	Digest         string
	OrganizationID uuid.UUID
	ArtifactID     uuid.UUID
	AttachmentID   uuid.UUID
	AuditID        uuid.UUID
}

// ObjectKey is where the seeded object lives in the bucket.
//
// Reproduced here rather than exported from the seam, because a test that
// reaches the object store directly — to corrupt one restored object through
// the S3 API, which is the only way to produce a torn pair — needs the key
// the seam would use. The layout is asserted against the seam in
// TestCrossStoreSeedIsWhereTheSeamPutIt, so the two cannot drift silently.
func (s crossStoreSeed) ObjectKey() string {
	return s.OrganizationID.String() + "/" + s.Digest[:2] + "/" + s.Digest[2:4] + "/" + s.Digest
}

// seedCrossStore populates a running plane with contents spanning both
// stores, and returns what it wrote.
//
// The artifact is left a DRAFT. Acceptance would add a review and a
// reviewer, and would verify the evidence set — all of which happens before
// any backup runs and none of which changes what the copy has to carry.
// Verification walks every artifact regardless of status, so a draft
// exercises both digest columns exactly as an accepted one would.
func seedCrossStore(t *testing.T, cfg *Config) crossStoreSeed {
	t.Helper()
	seam := openSeam(t, cfg)
	ctx := t.Context()

	// OrganizationID is left zero here and filled in by the bootstrap below,
	// which ALLOCATES it. A placeholder assigned here would be overwritten a
	// few lines later, and the object key derived from it would then depend on
	// which of the two a reader believed.
	seed := crossStoreSeed{
		Body: []byte("evidence bytes that must survive the whole round trip"),
	}
	sum := sha256.Sum256(seed.Body)
	seed.Digest = hex.EncodeToString(sum[:])

	// The organization and its user come from the seam's own bootstrap verbs.
	//
	// An earlier version inserted both rows directly and said the seam had no
	// verb for either. That stopped being true when the benchmark family
	// landed: BootstrapOrganization and BootstrapUser are on the seam,
	// reachable from the bootstrap command. Raw inserts would now be building
	// a fixture through a path no supported caller uses, which is the wrong
	// starting point for a test about what survives a backup.
	organization, err := seam.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{
		Slug: "backup-fixture", DisplayName: "Backup Fixture",
	})
	if err != nil {
		t.Fatalf("bootstrap organization: %v", err)
	}
	seed.OrganizationID = organization.Record.OrganizationID
	operator, err := seam.BootstrapUser(ctx, store.BootstrapUserInput{
		Handle: "fixture", DisplayName: "Fixture",
		OrganizationID: seed.OrganizationID,
	})
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	userID := operator.Record.UserID

	agentType := "coder"
	author, err := seam.CreatePrincipalInstance(ctx, store.CreatePrincipalInstanceInput{
		Kind:           store.PrincipalAgent,
		Model:          "fixture-model",
		AgentType:      &agentType,
		OrganizationID: seed.OrganizationID,
	})
	if err != nil {
		t.Fatalf("create the authoring principal: %v", err)
	}

	// Preallocated, because the payload has to NAME the attachment before
	// the transaction that writes either exists -- which is the whole reason
	// the seam accepts a caller-supplied id.
	seed.AttachmentID, err = uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate an attachment id: %v", err)
	}
	payload, err := json.Marshal(crossStorePayload{
		Title:       "an artifact whose evidence lives in the object store",
		Attachments: []uuid.UUID{seed.AttachmentID},
	})
	if err != nil {
		t.Fatalf("marshal the payload: %v", err)
	}

	scope := store.Scope{Type: store.ScopeOrganization, ID: seed.OrganizationID}
	result, err := seam.AttachEvidence(ctx, store.AttachEvidenceInput{
		Attachments: []store.PutAttachmentInput{{
			Body:           bytes.NewReader(seed.Body),
			Digest:         seed.Digest,
			MediaType:      crossStoreMediaType,
			SizeBytes:      int64(len(seed.Body)),
			OrganizationID: seed.OrganizationID,
			AttachmentID:   seed.AttachmentID,
		}},
		Artifact: store.CreateManagementArtifactInput{
			Payload:          payload,
			Type:             crossStoreType,
			Summary:          "an artifact whose evidence lives in the object store",
			Scope:            scope,
			OrganizationID:   seed.OrganizationID,
			UserID:           userID,
			AuthorInstanceID: author.PrincipalInstanceID,
		},
		Pins: []store.EvidenceRef{{AttachmentID: &seed.AttachmentID}},
	})
	if err != nil {
		t.Fatalf("AttachEvidence: %v", err)
	}
	seed.ArtifactID = result.Artifact.ArtifactID

	auditPayload, err := json.Marshal(map[string]string{"title": "exhaust from the same run"})
	if err != nil {
		t.Fatalf("marshal the audit payload: %v", err)
	}
	audit, err := seam.CreateAuditArtifact(ctx, store.CreateAuditArtifactInput{
		Payload:          auditPayload,
		Type:             crossStoreEventType,
		Summary:          "exhaust from the same run",
		Scope:            scope,
		OrganizationID:   seed.OrganizationID,
		UserID:           &userID,
		AuthorInstanceID: author.PrincipalInstanceID,
	})
	if err != nil {
		t.Fatalf("CreateAuditArtifact: %v", err)
	}
	seed.AuditID = audit.ArtifactID

	return seed
}

// assertCrossStoreIntact reads every seeded row and the attachment's BYTES
// back out of a plane.
//
// The bytes are what makes this cross-store. Reading the attachment ROW
// proves only that Postgres survived; the row is a reference, and the object
// it addresses lives in the other store entirely. GetAttachment streams
// through the verifying reader, so a body that does not hash to the digest
// addressing it fails at EOF -- which is why the copy is drained rather than
// sampled.
func assertCrossStoreIntact(t *testing.T, cfg *Config, seed crossStoreSeed) {
	t.Helper()
	seam := openSeam(t, cfg)
	ctx := t.Context()

	artifact, err := seam.GetManagementArtifact(ctx, seed.OrganizationID, seed.ArtifactID)
	if err != nil {
		t.Fatalf("read the seeded artifact back: %v", err)
	}
	if artifact.Type != crossStoreType {
		t.Errorf("artifact type = %q, want %q", artifact.Type, crossStoreType)
	}
	if _, err := seam.GetAuditArtifact(ctx, seed.OrganizationID, seed.AuditID); err != nil {
		t.Fatalf("read the seeded audit artifact back: %v", err)
	}

	body, attachment, err := seam.GetAttachment(ctx, seed.OrganizationID, seed.AttachmentID)
	if err != nil {
		t.Fatalf("open the seeded attachment: %v", err)
	}
	defer func() { _ = body.Close() }()
	read, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read the attachment's bytes: %v", err)
	}
	if !bytes.Equal(read, seed.Body) {
		t.Errorf("attachment body = %q, want %q", read, seed.Body)
	}
	if attachment.Digest != seed.Digest {
		t.Errorf("attachment digest = %s, want %s", attachment.Digest, seed.Digest)
	}
	if attachment.MediaType != crossStoreMediaType {
		t.Errorf("attachment media type = %q, want %q", attachment.MediaType, crossStoreMediaType)
	}

	// The pin as well as the attachment. A pin is the retention claim that
	// keeps the object from being swept; an artifact whose evidence survived
	// the copy without its pin is evidence the next sweep would collect.
	pins, err := seam.ListPins(ctx, seed.OrganizationID, seed.ArtifactID)
	if err != nil {
		t.Fatalf("list the artifact's pins: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("artifact holds %d pins, want exactly the one it was seeded with", len(pins))
	}
	if pins[0].AttachmentID == nil || *pins[0].AttachmentID != seed.AttachmentID {
		t.Errorf("pin names %v, want attachment %s", pins[0].AttachmentID, seed.AttachmentID)
	}
	if pins[0].Digest != seed.Digest {
		t.Errorf("pin is bound to %s, want %s", pins[0].Digest, seed.Digest)
	}
}

// assertVerifyWalkedTheSeed requires a verification pass to have COVERED the
// fixture, not merely to have reported no problems.
//
// "No problems" is what an empty plane reports. Every one of this package's
// verification tests would pass vacuously against a plane with nothing in
// it, so the counts are asserted as exactly the fixture's -- and the whole
// point of the fixture is that they are not zero.
func assertVerifyWalkedTheSeed(t *testing.T, report store.VerifyReport) {
	t.Helper()
	if !report.Healthy() {
		t.Errorf("verify reported %d problem(s) on an undamaged plane; first: %+v",
			len(report.Problems), report.Problems[0])
	}
	if report.Organizations != 1 {
		t.Errorf("verify walked %d organizations, want 1", report.Organizations)
	}
	if report.ManagementArtifacts != 1 {
		t.Errorf("verify checked %d Management artifacts, want 1", report.ManagementArtifacts)
	}
	if report.AuditArtifacts != 1 {
		t.Errorf("verify checked %d Audit artifacts, want 1", report.AuditArtifacts)
	}
	if report.Attachments != 1 {
		t.Errorf("verify read %d attachments, want 1: a pass that read none proves nothing about "+
			"the object store, and reports the same empty problem list as a healthy plane", report.Attachments)
	}
	if report.Skipped != 0 {
		t.Errorf("verify skipped %d attachments with no concurrent truncation running, want 0",
			report.Skipped)
	}
}

// tearTheSeededPair makes one half of the plane disagree with the other.
//
// A torn pair is the characteristic failure of a whole-root copy: a Postgres
// cluster and an object store captured at moments that disagree. It is
// produced here by writing different bytes at the object's digest key
// THROUGH THE S3 API — a second version, which is what a read returns —
// rather than by editing MinIO's on-disk files, whose representation is
// erasure-coded metadata and not the object body.
//
// The row is untouched, so nothing structural is wrong: the attachment
// exists, the object exists, and only the content disagrees with the digest
// addressing it. Recomputing the hash over the whole stream is the only
// thing that observes it.
func tearTheSeededPair(t *testing.T, cfg *Config, seed crossStoreSeed) {
	t.Helper()
	blob, err := ensureBucket(t.Context(), cfg, planeRootKey(t, cfg))
	if err != nil {
		t.Fatalf("reach the object store: %v", err)
	}

	// The key is pinned before it is used. If the fixture's idea of the
	// layout no longer matched the seam's, the corruption would land
	// somewhere nothing reads, verification would pass, and every test below
	// would report a detection that never happened.
	versions, err := blob.ListVersions(t.Context(), seed.ObjectKey())
	if err != nil {
		t.Fatalf("list versions at %s: %v", seed.ObjectKey(), err)
	}
	if len(versions) == 0 {
		t.Fatalf("nothing is stored at %s: the fixture's idea of the object key no longer matches "+
			"the seam's, so this would corrupt nothing", seed.ObjectKey())
	}

	corrupt := []byte("these bytes do not hash to the digest that addresses them")
	if _, err := blob.PutStaged(t.Context(), seed.ObjectKey(), int64(len(corrupt)),
		bytes.NewReader(corrupt)); err != nil {
		t.Fatalf("write the corrupt version at %s: %v", seed.ObjectKey(), err)
	}
}
