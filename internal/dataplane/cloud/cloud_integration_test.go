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
//	MAESTRO_CLOUD_ROOT_KEY=<32+ bytes> \
//	GOOGLE_CLOUD_QUOTA_PROJECT=<project> \
//	go test -tags=cloud -count=1 ./internal/dataplane/cloud/
//
// `sslmode=disable` in that DSN is correct rather than careless: the Auth Proxy
// listens on loopback and supplies the TLS, and the instance itself refuses
// unencrypted connections and has no authorized networks, so there is no path
// to it that bypasses the proxy.

package cloud

import (
	"context"
	"os"
	"testing"

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

// TestCloudMigrateFromEmptyThenOpen is the sequence that matters: the schema
// applies to a managed database that has never held it, and the seam then opens
// against that database and a real bucket together.
//
// Migrating from EMPTY is the specific claim. A schema that only ever advances
// on planes it grew up with proves nothing about portability; the local
// migration set has to be applicable to an instance provisioned by a provider,
// with whatever defaults and extensions that provider ships.
func TestCloudMigrateFromEmptyThenOpen(t *testing.T) {
	cfg := cloudConfig(t)
	ctx := context.Background()

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

	// Re-running must be a no-op rather than an error: the acceptance criteria
	// require a re-runnable workflow, and a migrate that refuses on an
	// already-migrated plane would make every rerun a manual step.
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

	// Now the composition itself, against the migrated database and a real
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
	// down — which is the contract plane enforces and worth confirming through
	// a real client rather than only a double.
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
