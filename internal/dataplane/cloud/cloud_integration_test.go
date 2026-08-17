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
//	MAESTRO_CLOUD_ROOT_KEY=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32)
//
// `sslmode=disable` in that DSN is correct rather than careless: the Auth Proxy
// listens on loopback and supplies the TLS, and the instance itself refuses
// unencrypted connections and has no authorized networks, so there is no path
// to it that bypasses the proxy.

package cloud

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"

	// Registers the pgx stdlib driver, for the administrative CREATE/DROP
	// DATABASE statements that cannot run through the seam.
	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/registry"
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
func freshDatabase(t *testing.T, cfg Config) Config {
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
	// from a timestamp and a test name reduced to [a-z0-9_] above, so there is
	// no operator-supplied text in it.
	if _, err := adminDB.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		// A database left behind holds storage that bills, and the next run
		// would have to guess whether it mattered.
		cleanup, err := sql.Open("pgx", admin.String())
		if err != nil {
			t.Errorf("cleanup: open administrative connection: %v", err)
			return
		}
		defer func() { _ = cleanup.Close() }()
		if _, err := cleanup.ExecContext(context.Background(),
			"DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("cleanup: drop database %s: %v", name, err)
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
	cfg := freshDatabase(t, base)

	if err := Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate a cloud plane from empty: %v", err)
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
	if err := Migrate(ctx, cfg); err != nil {
		t.Fatalf("re-running migrate against an already-migrated cloud plane must be a no-op: %v", err)
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
	seam, err := OpenSeam(ctx, cfg, types)
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
	seam, err := OpenSeam(context.Background(), cfg, types)
	if err == nil {
		seam.Close()
		t.Fatal("opening against a bucket that does not exist succeeded, so the failure would " +
			"arrive at the first object read instead")
	}
}
