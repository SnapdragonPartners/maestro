//go:build cloud

// These tests run against REAL managed services — a Cloud SQL instance and a
// Cloud Storage bucket — and they are the portability claim of #286 rather than
// a rehearsal of it. What is being asserted is that the same migration set and
// the same `store.Store` composition work against managed infrastructure, which
// only managed infrastructure can demonstrate.
//
// They are behind their own build tag, separate from both `integration` (the
// local Docker stack) and `gcs` (the object adapter alone), because they need
// credentials AND a running Auth Proxy. The pre-push gate must never require
// either. Run them deliberately:
//
//	MAESTRO_CLOUD_DSN='postgres://postgres:PW@127.0.0.1:5433/maestro_286?sslmode=disable' \
//	MAESTRO_GCS_TEST_BUCKET=maestro-objects-286 \
//	MAESTRO_CLOUD_ROOT_KEY=<EXACTLY 32 bytes> \
//	GOOGLE_CLOUD_QUOTA_PROJECT=<project> \
//	go test -tags=cloud -count=1 ./internal/dataplane/cloud/
//
// The root key must be EXACTLY 32 bytes — the same length a key file must be,
// because it protects the same vault. Not "at least": 33 bytes is refused, since
// truncating or hashing over-long material to fit would accept two different
// inputs as the same key. Prefer plain ASCII so the environment string's byte
// length is its character count; multi-byte characters make a 32-character value
// longer than 32 bytes and the refusal then looks arbitrary. For example:
//
//	MAESTRO_CLOUD_ROOT_KEY=$(openssl rand -hex 16)
//
// Sixteen random bytes hex-encode to the thirty-two this requires. The obvious
// `tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32` is avoided deliberately: it
// works interactively but returns 141 under `set -o pipefail`, because head
// exits at its byte count and tr dies of SIGPIPE. A script that adopts it fails
// before reaching these tests.
//
// `sslmode=disable` in that DSN is correct rather than careless: the Auth Proxy
// listens on loopback and supplies the TLS, and the instance itself refuses
// unencrypted connections and has no authorized networks, so there is no path
// to it that bypasses the proxy.

package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	// Registers the pgx stdlib driver, for the administrative CREATE/DROP
	// DATABASE statements that cannot run through the seam.
	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/benchmarkimport"
	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/importslice"
	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
)

const (
	dsnEnv     = "MAESTRO_CLOUD_DSN"
	bucketEnv  = "MAESTRO_GCS_TEST_BUCKET"
	rootKeyEnv = "MAESTRO_CLOUD_ROOT_KEY"
)

// cloudConfig assembles a configuration from the environment, skipping when the
// suite has not been deliberately pointed at a plane.
//
// It SKIPS on absent configuration and FAILS on partial configuration. A
// half-configured run that skipped would look like a pass, and this is the
// suite whose passing is the acceptance claim.
func cloudConfig(t *testing.T) Config {
	t.Helper()
	dsn, bucket, key := os.Getenv(dsnEnv), os.Getenv(bucketEnv), os.Getenv(rootKeyEnv)
	if dsn == "" && bucket == "" && key == "" {
		t.Skipf("%s, %s and %s are unset; these tests require a real cloud plane",
			dsnEnv, bucketEnv, rootKeyEnv)
	}
	for name, value := range map[string]string{dsnEnv: dsn, bucketEnv: bucket, rootKeyEnv: key} {
		if value == "" {
			t.Fatalf("%s is empty while the others are set: a partially configured run would skip "+
				"and look like a pass", name)
		}
	}
	return Config{DSN: dsn, Bucket: bucket, RootKey: []byte(key)}
}

// freshDatabase creates a uniquely named, EMPTY database on the configured
// instance and returns a Config pointing at it.
//
// A unique database per run is what makes "migrate from empty" mean anything.
// An earlier version of this test reused one database, so after its first run it
// started at the current version and still reported migrating from empty — which
// left the suite unable to catch the failure it exists for: a migration set that
// no longer applies cleanly from zero. A rerun of a passing test proved only
// that migrating an already-migrated plane is a no-op.
//
// It also gives the re-runnable requirement a real meaning: each run provisions
// its own plane rather than depending on residue from the last one.
//
// The purge step is the OBJECT store's half of teardown, and may be nil for a
// test that writes none. See purgeStep for why the drop runs it rather than
// standing beside it as a second cleanup.
func freshDatabase(t *testing.T, cfg Config, purge *purgeStep) Config {
	t.Helper()

	parsed, err := url.Parse(cfg.DSN)
	if err != nil {
		t.Fatalf("parse %s: %v", dsnEnv, err)
	}
	// Administer through the default database: CREATE DATABASE cannot run from
	// inside the database being created.
	admin := *parsed
	admin.Path = "/postgres"

	name := freshDatabaseName(t)

	adminDB, err := sql.Open("pgx", admin.String())
	if err != nil {
		t.Fatalf("open the administrative connection: %v", err)
	}
	defer func() { _ = adminDB.Close() }()

	// Identifiers cannot be parameterised, and this one is machine-generated
	// from random bytes and a test name reduced to [a-z0-9_], so there is no
	// operator-supplied text in it.
	if _, createErr := adminDB.ExecContext(t.Context(), "CREATE DATABASE "+name); createErr != nil {
		t.Fatalf("create database %s: %v", name, createErr)
	}
	t.Cleanup(func() {
		// The OBJECT store first, and the database only if that succeeded.
		//
		// A database left behind holds storage that bills, which is why this
		// drops it. Stranded objects are worse, and the rows dropped here are
		// the only record of which digests this run wrote — so dropping after a
		// failed purge converts a reported problem into permanently
		// unattributable storage. Retaining the database is the cheaper failure,
		// and the one an operator can still act on.
		if purge != nil && purge.Run != nil {
			if purgeErr := purge.Run(); purgeErr != nil {
				t.Errorf("cleanup: the object purge failed: %v\n"+
					"RETAINING database %s so the stranded objects stay identifiable: its "+
					"binary_attachments rows name every digest this run wrote, beneath %s. "+
					"Purge those prefixes, then drop the database by hand.",
					purgeErr, name, purge.What)
				return
			}
		}

		cleanup, openErr := sql.Open("pgx", admin.String())
		if openErr != nil {
			t.Errorf("cleanup: open administrative connection: %v", openErr)
			return
		}
		defer func() { _ = cleanup.Close() }()
		if _, dropErr := cleanup.ExecContext(context.Background(),
			"DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); dropErr != nil {
			t.Errorf("cleanup: drop database %s: %v", name, dropErr)
		}
	})

	fresh := cfg
	target := *parsed
	target.Path = "/" + name
	fresh.DSN = target.String()

	// The empty precondition is ASSERTED rather than assumed. If a future
	// change made this reuse a database, the test would go back to reporting a
	// migration from empty that was nothing of the kind.
	planeDB, err := sql.Open("pgx", fresh.DSN)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = planeDB.Close() }()
	var tables int
	if err := planeDB.QueryRowContext(t.Context(),
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'`,
	).Scan(&tables); err != nil {
		t.Fatalf("count tables in %s: %v", name, err)
	}
	if tables != 0 {
		t.Fatalf("database %s holds %d tables before migrating, so this run cannot establish that "+
			"the schema applies from empty", name, tables)
	}
	t.Logf("provisioned empty database %s", name)
	return fresh
}

// TestCloudProvisionMigrateFromEmptyThenOpen is the sequence that matters, in
// the order it has to happen.
//
// Provisioning comes FIRST and is a real stage rather than a comment. The bucket
// obligations — versioning, and soft delete disabled — cannot be applied
// retroactively: disabling soft delete does not release residue already
// accumulated and makes it unobservable, so a plane that wrote objects before
// being configured cannot be audited afterwards. Running EnsureBucket here, ahead
// of anything that could write, is what makes the workflow's ordering claim true
// instead of documented.
//
// Migrating from EMPTY is the second claim. A schema that only ever advances on
// planes it grew up with proves nothing about portability; the local migration
// set has to apply to a database a provider just handed us.
func TestCloudProvisionMigrateFromEmptyThenOpen(t *testing.T) {
	base := cloudConfig(t)
	ctx := context.Background()

	// Stage 1: provisioning. Refusing here is correct and must not be skipped
	// past — an unsafe bucket makes the object sweep report storage it did not
	// reclaim, which no later test can detect.
	report, err := EnsureBucket(ctx, base)
	if err != nil {
		t.Fatalf("provision the object bucket before any write (report: %+v): %v", report, err)
	}
	if !report.VersioningEnabled {
		t.Fatalf("EnsureBucket returned no error for an unversioned bucket: %+v", report)
	}
	if report.SoftDeleteRetention != 0 {
		t.Fatalf("EnsureBucket returned no error while soft delete retains for %s: %+v",
			report.SoftDeleteRetention, report)
	}
	t.Logf("bucket provisioned: %+v", report)

	// Stage 2: a database that has never held the schema.
	// No purge step: this test writes no objects, so there is nothing in the
	// bucket for the drop to have to sequence itself behind.
	cfg := freshDatabase(t, base, nil)

	if migrateErr := Migrate(ctx, cfg); migrateErr != nil {
		t.Fatalf("migrate a cloud plane from empty: %v", migrateErr)
	}

	// The recorded version must be clean. golang-migrate marks dirty BEFORE
	// executing, so a dirty version after a successful return would mean the
	// success was not one — and every later migration would refuse.
	version, dirty, err := migrations.Version(cfg.DSN)
	if err != nil {
		t.Fatalf("read the migration version: %v", err)
	}
	if dirty {
		t.Fatalf("the schema version %d is dirty after a migration that reported success; every "+
			"later migration will refuse until an operator clears it", version)
	}
	if version == 0 {
		t.Fatal("the migration version is 0 after migrating, so nothing was applied")
	}
	t.Logf("cloud plane migrated from empty to version %d", version)

	// Stage 3: re-running must be a no-op rather than an error, against THIS
	// database — the one that started empty.
	if rerunErr := Migrate(ctx, cfg); rerunErr != nil {
		t.Fatalf("re-running migrate against an already-migrated cloud plane must be a no-op: %v",
			rerunErr)
	}
	after, dirtyAfter, err := migrations.Version(cfg.DSN)
	if err != nil {
		t.Fatalf("read the migration version after the rerun: %v", err)
	}
	if after != version || dirtyAfter {
		t.Fatalf("a rerun moved the plane from version %d (dirty=%v) to %d (dirty=%v)",
			version, dirty, after, dirtyAfter)
	}

	// Stage 4: the composition itself, against the migrated database and a real
	// bucket. Opening is a genuine assertion: the store validates the object
	// adapter's capability at construction, so a seam that opens has agreed
	// with the provider about how interrupted writes are handled.
	types, err := registry.New(nil)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	seam, err := OpenSeam(ctx, cfg, types, configkeys.MustNew(nil))
	if err != nil {
		t.Fatalf("open the seam against a cloud plane: %v", err)
	}
	seam.Close()

	// Closing twice must be harmless. The composition owns the object client,
	// and a second Close releasing it again would act on a client already shut
	// down — the contract plane enforces, worth confirming through a real
	// client rather than only a double.
	seam.Close()
}

// roundTripMediaType is what the round-trip's attachment declares. Named
// because the row read back out is compared against it.
const roundTripMediaType = "application/octet-stream"

// noVocabulary is the registry a plane is opened with when it writes no
// artifacts. Named so a call site reads as a statement about what the plane
// accepts rather than as a bare nil.
//
// Registering types that nothing writes would imply a coverage the test does not
// have, which is why this is empty rather than the importer's entries.
var noVocabulary map[registry.Type]registry.Entry

// migratedCloudPlane provisions the bucket, creates an EMPTY database, migrates
// it, and opens the seam against both.
//
// It performs the same stages as TestCloudProvisionMigrateFromEmptyThenOpen
// without duplicating that test's assertions, and the separation is deliberate
// rather than incidental. There, each stage IS the subject, and folding them
// into a helper would hide what is being asserted. Here they are a
// precondition, and the only thing worth knowing about a precondition is
// whether it held.
//
// The returned purge step is the object store's half of teardown, unclaimed
// until a caller that writes objects hands it their organization.
func migratedCloudPlane(
	t *testing.T, entries map[registry.Type]registry.Entry,
) (store.Store, Config, *purgeStep) {
	t.Helper()
	base := cloudConfig(t)
	ctx := t.Context()

	// Ahead of anything that could write, for the reason EnsureBucket
	// documents: the soft-delete obligation cannot be applied retroactively.
	if _, err := EnsureBucket(ctx, base); err != nil {
		t.Fatalf("provision the object bucket before any write: %v", err)
	}
	purge := &purgeStep{}
	cfg := freshDatabase(t, base, purge)
	if err := Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate a cloud plane from empty: %v", err)
	}

	types, err := registry.New(entries)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	seam, err := OpenSeam(ctx, cfg, types, configkeys.MustNew(nil))
	if err != nil {
		t.Fatalf("open the seam against a cloud plane: %v", err)
	}
	// The unconditional safety net. A test that writes objects also closes the
	// seam inside its purge step, where the ordering is visible; Close is
	// at-most-once, so both running is harmless and neither being forgotten
	// matters more.
	t.Cleanup(seam.Close)
	return seam, cfg, purge
}

// ownObjectsOf points a plane's teardown at the prefixes one organization owns.
//
// Called by every test that writes objects, and the reason it takes the seam is
// ordering: the seam is closed here, immediately before the purge, rather than
// left to a separate cleanup whose position relative to this one is a property
// of registration order that no reader can see.
func ownObjectsOf(t *testing.T, purge *purgeStep, seam store.Store, cfg Config, organizationID uuid.UUID) {
	t.Helper()
	purge.What = objectPrefixesOf(organizationID)
	purge.Run = func() error {
		seam.Close()
		// Not t.Context(): cleanups run after it is cancelled, and a purge that
		// cannot issue calls would delete nothing while reporting success.
		return purgeAndConfirm(context.Background(), cfg, organizationID)
	}
}

// bootstrapOrganization creates the tenant an attachment's foreign key requires.
//
// Through the seam's own bootstrap verb rather than by inserting a row. An
// earlier version of this helper inserted directly and said the seam had no verb
// for it; that was simply false — BootstrapOrganization is on the seam, reachable
// from the bootstrap command — and a raw insert would have been exercising a
// plane no supported path could produce.
func bootstrapOrganization(t *testing.T, seam store.Store) uuid.UUID {
	t.Helper()
	organization, err := seam.BootstrapOrganization(t.Context(), store.BootstrapOrganizationInput{
		Slug: "cloud-round-trip", DisplayName: "Cloud Round Trip",
	})
	if err != nil {
		t.Fatalf("bootstrap the owning organization: %v", err)
	}
	return organization.Record.OrganizationID
}

// cloudObjectKey reproduces the layout the seam stores objects at.
//
// Reproduced rather than exported, exactly as the local cross-store fixture
// does it: a test that asks the PROVIDER what it holds needs the key the seam
// would have used. It is not trusted — the round trip requires at least one
// generation to be present here after a successful write, so a layout that
// drifted fails loudly instead of quietly listing an empty prefix and
// concluding nothing happened.
func cloudObjectKey(organizationID uuid.UUID, digest string) string {
	return organizationID.String() + "/" + digest[:2] + "/" + digest[2:4] + "/" + digest
}

// withObjectClient runs fn against a second client on the configured bucket.
//
// A second client is the only option: the seam does not expose the object store
// it composed, and deliberately so. These helpers reach past the seam to ask the
// provider what it actually holds, which is a question the seam cannot be asked.
//
// The context is a parameter rather than `t.Context()` because one caller is a
// CLEANUP. `t.Context()` is cancelled just before cleanups run, so a cleanup
// using it could not issue a single call — and a cleanup that silently deletes
// nothing is worse than none, since it leaves residue while reporting success.
func withObjectClient(ctx context.Context, t *testing.T, cfg Config, fn func(blob *objects.GCS)) {
	t.Helper()
	blob, err := objects.NewGCS(ctx, objects.GCSConfig{Bucket: cfg.Bucket})
	if err != nil {
		t.Errorf("reach the object bucket %s: %v", cfg.Bucket, err)
		return
	}
	defer func() {
		if closeErr := blob.Close(); closeErr != nil {
			t.Errorf("close the object client: %v", closeErr)
		}
	}()
	fn(blob)
}

// objectGenerations counts the versions stored at one exact key.
func objectGenerations(t *testing.T, cfg Config, key string) int {
	t.Helper()
	count := -1
	withObjectClient(t.Context(), t, cfg, func(blob *objects.GCS) {
		versions, err := blob.ListVersions(t.Context(), key)
		if err != nil {
			t.Fatalf("list versions at %s: %v", key, err)
		}
		count = len(versions)
	})
	return count
}

// purgeOrganizationObjects deletes every generation a run wrote for one
// organization.
//
// The bucket is REAL and PERSISTENT while each run's database is disposable, and
// that asymmetry is the whole reason this exists. Dropping the database takes
// every row that referenced these objects with it, so what is left in the bucket
// becomes unreachable by the object sweep — the sweep walks the database to
// decide what is unreferenced, and there is no longer a database to walk. Each
// run would otherwise add permanently orphaned storage that bills and that no
// later run can even attribute to itself. `freshDatabase` drops its database for
// this reason; this is the other store's half of the same obligation.
//
// Both prefixes, because one write leaves objects under two: the digest key
// beneath the organization, and the staging key the upload passed through. The
// seam releases staging on the paths that succeed — a failed run is exactly when
// that cannot be relied on.
//
// Deletion genuinely reclaims here only because the bucket's soft-delete policy
// is off, which EnsureBucket refused to proceed without. Under the provider
// default these calls would return success and reclaim nothing for a week.
func purgeAndConfirm(ctx context.Context, cfg Config, organizationID uuid.UUID) error {
	blob, err := objects.NewGCS(ctx, objects.GCSConfig{Bucket: cfg.Bucket})
	if err != nil {
		return fmt.Errorf("reach the object bucket %s: %w", cfg.Bucket, err)
	}
	defer func() { _ = blob.Close() }()

	prefixes := objectPrefixesOf(organizationID)
	var problems []error
	for _, prefix := range prefixes {
		versions, listErr := blob.ListVersions(ctx, prefix)
		if listErr != nil {
			problems = append(problems, fmt.Errorf("list versions under %s: %w", prefix, listErr))
			continue
		}
		for i := range versions {
			if delErr := blob.DeleteVersion(ctx, versions[i].Key, versions[i].VersionID); delErr != nil {
				problems = append(problems, fmt.Errorf("delete %s version %s: %w",
					versions[i].Key, versions[i].VersionID, delErr))
			}
		}
	}

	// RE-LISTED rather than inferred from the deletes returning nil, and this is
	// the check that makes the caller's decision meaningful. A generation-scoped
	// delete succeeds and reclaims NOTHING under a soft-delete policy, so a
	// bucket whose policy came back on would report a clean purge while retaining
	// everything. Confirming emptiness is the only way to tell those apart, and
	// the database must not be dropped on the strength of the weaker signal.
	for _, prefix := range prefixes {
		remaining, listErr := blob.ListVersions(ctx, prefix)
		if listErr != nil {
			problems = append(problems, fmt.Errorf("re-list versions under %s: %w", prefix, listErr))
			continue
		}
		if len(remaining) != 0 {
			problems = append(problems, fmt.Errorf(
				"%d generation(s) remain under %s after purging it", len(remaining), prefix))
		}
	}
	return errors.Join(problems...)
}

// objectPrefixesOf names every prefix one organization's writes can reach.
//
// Two, because a single write leaves objects under both: the digest key beneath
// the organization, and the staging key the upload passed through before being
// promoted. The seam releases staging on the paths that succeed, so the second
// matters for runs that die in between — which is exactly when cleanup is doing
// something rather than confirming nothing.
//
// A finalized staged object is what this reaches. An upload interrupted before
// finalizing is a third state that no listing reports and no prefix purge can
// touch; the object seam declares that with a capability rather than pretending
// an empty listing means an empty bucket.
func objectPrefixesOf(organizationID uuid.UUID) []string {
	return []string{
		organizationID.String() + "/",
		"staging/" + organizationID.String() + "/",
	}
}

// purgeStep is a teardown action the database drop must run, and succeed at,
// before it may proceed.
//
// Consulted AT DROP TIME rather than registered as its own t.Cleanup, and that
// is the entire point. Cleanups run last-in-first-out, so ordering two of them
// correctly is a property of where each was registered — invisible to a reader,
// and silently invertible by one edit that moves a line. Worse, t.Errorf does not
// stop later cleanups, so an independent purge could fail and the drop would
// still take away the rows that identify what it failed to delete. Here the drop
// calls the purge, so the sequence is the code and the failure is a decision.
type purgeStep struct {
	// Run is nil until a test that writes objects claims them. Nil means there
	// is nothing to purge, not that purging is optional.
	Run func() error
	// What names the prefixes the step owns, for the report a failure leaves an
	// operator.
	What []string
}

// corruptObjectAt writes bytes at a digest key that do not hash to it.
//
// Through the object API rather than by editing the row: nothing structural is
// then wrong — the attachment exists, the object exists, and only the content
// disagrees with the digest addressing it, which recomputing the hash over the
// whole stream is the only way to observe. On a versioned bucket this is a newer
// generation, which is what a read returns.
func corruptObjectAt(t *testing.T, cfg Config, key string) {
	t.Helper()
	corrupt := []byte("these bytes do not hash to the digest that addresses them")
	withObjectClient(t.Context(), t, cfg, func(blob *objects.GCS) {
		if _, err := blob.PutStaged(t.Context(), key, int64(len(corrupt)),
			bytes.NewReader(corrupt)); err != nil {
			t.Fatalf("write a corrupt generation at %s: %v", key, err)
		}
	})
}

// TestCloudAttachmentRoundTrip drives the real store.ObjectStore contract
// against Cloud SQL and Cloud Storage together.
//
// It is where a divergence in the OBJECT CONTRACT would hide, and the reason is
// the composition rather than the adapter. The GCS adapter's own suite measured
// each provider operation in isolation; what it could not exercise is the write
// path the seam actually uses, which spans BOTH stores at once: a staging lease
// taken and renewed in Postgres, an upload to a staging key, a generation-pinned
// server-side copy onto the digest key, and a read-back hashed before any row is
// allowed to reference it — all inside one database transaction that stays open
// across those remote calls.
//
// It is NOT the last behavioural claim #286 requires. TestCloudBenchmarkImportSlice
// below carries the acceptance criterion this cannot: an importer can hold a
// correct object adapter and still depend on the composition through its ledger
// identity, its transaction boundaries, or the queries that read its work back.
//
// The local suite proves that sequence against MinIO. Nothing but a managed
// plane proves it against a provider whose copy semantics, generation
// numbering and consistency are its own.
func TestCloudAttachmentRoundTrip(t *testing.T) {
	seam, cfg, purge := migratedCloudPlane(t, noVocabulary)
	ctx := t.Context()
	organizationID := bootstrapOrganization(t, seam)

	// Claimed BEFORE the first write, not after the last one. Every object below
	// is scoped to this organization's fresh id, and a run that fails halfway is
	// precisely the run whose residue nothing else will ever find.
	ownObjectsOf(t, purge, seam, cfg, organizationID)

	body := []byte("evidence bytes that have to survive a managed object store and come back identical")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	// Preallocated, the same way an evidence-bearing caller must: a payload
	// naming an attachment has to be built before the write that creates it.
	attachmentID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate an attachment id: %v", err)
	}

	stored, err := seam.PutAttachment(ctx, store.PutAttachmentInput{
		Body:           bytes.NewReader(body),
		Digest:         digest,
		MediaType:      roundTripMediaType,
		SizeBytes:      int64(len(body)),
		OrganizationID: organizationID,
		AttachmentID:   attachmentID,
	})
	if err != nil {
		t.Fatalf("store an attachment on a cloud plane: %v", err)
	}
	if stored.AttachmentID != attachmentID {
		t.Errorf("stored attachment id = %s, want the preallocated %s", stored.AttachmentID, attachmentID)
	}
	if stored.Digest != digest {
		t.Errorf("stored digest = %s, want %s", stored.Digest, digest)
	}
	if stored.SizeBytes != int64(len(body)) {
		t.Errorf("stored size = %d, want %d", stored.SizeBytes, len(body))
	}

	// What the promote actually created, read from the provider. Both claims
	// below are measured against this number.
	key := cloudObjectKey(organizationID, digest)
	afterFirst := objectGenerations(t, cfg, key)
	if afterFirst == 0 {
		t.Fatalf("nothing is stored at %s after a write that reported success: either the promote did "+
			"not land or this test's idea of the object layout no longer matches the seam's", key)
	}

	// The round trip proper: the BYTES, drained to EOF.
	//
	// Draining is the assertion and not merely how the bytes are obtained.
	// GetAttachment hashes as it streams and can only compare once the stream
	// ends, so a body that does not match the digest addressing it fails at EOF —
	// a sampled read would pass straight over the corruption this exists to
	// catch.
	reader, attachment, err := seam.GetAttachment(ctx, organizationID, attachmentID)
	if err != nil {
		t.Fatalf("open the stored attachment: %v", err)
	}
	defer func() { _ = reader.Close() }()
	read, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the attachment's bytes back through the verifying reader: %v", err)
	}
	if !bytes.Equal(read, body) {
		t.Errorf("attachment body = %q, want %q", read, body)
	}
	if attachment.Digest != digest {
		t.Errorf("attachment digest = %s, want %s", attachment.Digest, digest)
	}
	if attachment.MediaType != roundTripMediaType {
		t.Errorf("attachment media type = %q, want %q", attachment.MediaType, roundTripMediaType)
	}
	if attachment.SizeBytes != int64(len(body)) {
		t.Errorf("attachment size = %d, want %d", attachment.SizeBytes, len(body))
	}

	// Existence asks the object store, not only the row.
	exists, err := seam.AttachmentExists(ctx, organizationID, attachmentID)
	if err != nil {
		t.Fatalf("check that the attachment exists: %v", err)
	}
	if !exists {
		t.Error("AttachmentExists reported false for an attachment whose bytes just read back")
	}
	// An id nothing wrote is an ordinary false with no error. Asserted so the
	// check above cannot be satisfied by a method that answers true regardless.
	unknown, err := seam.AttachmentExists(ctx, organizationID, uuid.New())
	if err != nil {
		t.Fatalf("check an attachment id nothing ever stored: %v", err)
	}
	if unknown {
		t.Error("AttachmentExists reported true for an id nothing ever stored")
	}

	// The already-stored branch, which on a content-addressed store is the one
	// taken most often: a second attachment naming a digest the bucket holds.
	//
	// That it SUCCEEDS is only half the claim; that it skipped the transfer is
	// the other half, and it needs its own evidence. The discriminator is the
	// generation count — a promote CREATES a generation at the digest key, which
	// is how the one counted above got there, so a second write that added none
	// cannot have promoted anything.
	secondID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate a second attachment id: %v", err)
	}
	second, err := seam.PutAttachment(ctx, store.PutAttachmentInput{
		Body:           bytes.NewReader(body),
		Digest:         digest,
		MediaType:      roundTripMediaType,
		SizeBytes:      int64(len(body)),
		OrganizationID: organizationID,
		AttachmentID:   secondID,
	})
	if err != nil {
		t.Fatalf("store a second attachment over an object the bucket already holds: %v", err)
	}
	if second.AttachmentID != secondID {
		t.Errorf("the second write recorded id %s, want %s", second.AttachmentID, secondID)
	}
	if afterSecond := objectGenerations(t, cfg, key); afterSecond != afterFirst {
		t.Errorf("a second write of a digest the bucket already held took %s from %d generation(s) to "+
			"%d, so it re-uploaded and promoted instead of verifying what was there",
			key, afterFirst, afterSecond)
	}

	// And it is a real attachment rather than a call that returned nil error:
	// its bytes come back through the same verifying path.
	secondReader, _, err := seam.GetAttachment(ctx, organizationID, secondID)
	if err != nil {
		t.Fatalf("open the second attachment: %v", err)
	}
	defer func() { _ = secondReader.Close() }()
	secondRead, err := io.ReadAll(secondReader)
	if err != nil {
		t.Fatalf("read the second attachment's bytes: %v", err)
	}
	if !bytes.Equal(secondRead, body) {
		t.Errorf("second attachment body = %q, want %q", secondRead, body)
	}

	// Last: proof that the cloud read path VERIFIES rather than merely streams.
	//
	// Everything above is equally consistent with a seam that hands back
	// whatever the bucket returns and checks nothing — the bytes were correct,
	// so a verifying reader and an absent one behave identically. The only way
	// to tell those apart is to make the object disagree with the digest
	// addressing it and require the read to fail.
	//
	// It runs at the end because it leaves the object corrupt, and it fails the
	// test if the read SUCCEEDS, which is what makes every assertion above
	// non-vacuous.
	corruptObjectAt(t, cfg, key)

	// The generation counter's own sensitivity, established rather than assumed.
	//
	// "The second write added no generation" is worth exactly as much as the
	// measurement's ability to NOTICE one, and nothing above establishes that.
	// The corruption just written IS a new generation at that same key, so the
	// count must have moved by one. Without this, an unchanged count would be
	// equally consistent with a listing that cannot see generations at all —
	// which would make the skipped-upload claim above vacuous rather than wrong.
	if afterCorruption := objectGenerations(t, cfg, key); afterCorruption != afterFirst+1 {
		t.Errorf("a new generation at %s moved the count from %d to %d, want %d: this listing cannot "+
			"see a version being added, so the unchanged count asserted earlier establishes nothing "+
			"about the second write skipping its upload", key, afterFirst, afterCorruption, afterFirst+1)
	}

	corruptReader, _, err := seam.GetAttachment(ctx, organizationID, attachmentID)
	if err != nil {
		t.Fatalf("open the attachment after corrupting its object: %v", err)
	}
	defer func() { _ = corruptReader.Close() }()
	switch _, readErr := io.ReadAll(corruptReader); {
	case readErr == nil:
		t.Error("reading an object that does not hash to the digest addressing it succeeded, so the " +
			"cloud read path returns provider bytes without verifying them")
	case !errors.Is(readErr, store.ErrInvariant):
		t.Errorf("reading a corrupt object failed with %v, want a %v — a caller has to distinguish the "+
			"store contradicting itself from a transport failure", readErr, store.ErrInvariant)
	}
}

// TestCloudBenchmarkImportSlice runs the shared portability slice against the
// managed plane.
//
// This is #286's FIRST acceptance criterion, and the one the attachment round
// trip above does not satisfy: the identical benchmark-import vertical slice must
// pass against local Docker and one managed cloud configuration. Identical means
// the same function — importslice.Run — called from here and from the importer's
// own integration suite, so that a divergence cannot hide in the difference
// between two hand-written copies.
//
// The round trip proves the object contract; this proves the SLICE. They are
// different claims: an importer can hold a correct object adapter and still
// depend on the composition through its ledger identity, its transaction
// boundaries or the queries that read its work back.
//
// It is opened with the importer's OWN registry entries, not this package's
// usual empty vocabulary. The run-record and suite-report payloads are refused
// by a registry that does not know their types, so the import would fail at the
// seam rather than reaching the plane.
func TestCloudBenchmarkImportSlice(t *testing.T) {
	seam, cfg, purge := migratedCloudPlane(t, benchmarkimport.RegistryEntries())

	// The slice writes the suite's evidence as attachments, so this plane's
	// objects need claiming exactly as the round trip's do. The claim happens
	// through a callback DURING the slice rather than after it: the organization
	// is the slice's to create, and a run that dies partway through has already
	// written objects while never reaching a line that follows it.
	importslice.Run(t, seam, func(organizationID uuid.UUID) {
		ownObjectsOf(t, purge, seam, cfg, organizationID)
	})
}

// TestCloudOpenRefusesAMissingBucket confirms that a failure to reach the
// object store surfaces as a failure to open, rather than as a seam that opens
// and breaks on first use.
//
// It also exercises the ownership path that unit tests can only simulate: a
// real client is built and then released when the composition fails.
func TestCloudOpenRefusesAMissingBucket(t *testing.T) {
	cfg := cloudConfig(t)
	cfg.Bucket = cfg.Bucket + "-does-not-exist-" + t.Name()

	types, err := registry.New(nil)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	seam, err := OpenSeam(context.Background(), cfg, types, configkeys.MustNew(nil))
	if err == nil {
		seam.Close()
		t.Fatal("opening against a bucket that does not exist succeeded, so the failure would " +
			"arrive at the first object read instead")
	}
}
