// Package planetest builds disposable data planes for integration suites.
//
// It exists because "a real Postgres database and a real object store, both
// thrown away afterwards" is needed by more than one package, and the risky
// half of it — creating and dropping databases and buckets against the
// developer's own running stack — is exactly the code that must not exist in
// two hand-maintained copies. A second copy that drifts does not fail
// visibly; it deletes or reuses something it should not.
//
// Every helper SKIPS rather than fails when the stack is not running, so a
// checkout without `make dataplane-up` reports honestly instead of reporting
// a defect that is not there.
package planetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for the admin connection
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/stack"
)

// defaultMinIOPort mirrors stack.DefaultMinIOPort. Mirrored rather than
// imported: a test helper is not a good enough reason to pull in the
// launcher, and MAESTRO_MINIO_PORT overrides it where the two could differ.
const defaultMinIOPort = 59000

// minioPortEnv names the override a non-default stack is reachable through.
const minioPortEnv = "MAESTRO_MINIO_PORT"

// DSN creates a uniquely named database, migrates it, and drops it when the
// test ends. The label distinguishes one suite's leftovers from another's if
// a process dies before its cleanup runs.
//
// One database per test, not one per suite: these tests write rows, and they
// must never write them into the developer's working database.
func DSN(t *testing.T, label string) string {
	t.Helper()

	cfg, rootKey := stackConfig(t)
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
	if pingErr := admin.Ping(); pingErr != nil {
		_ = admin.Close()
		t.Skipf("data plane unavailable (run `make dataplane-up`): %v", pingErr)
	}

	name := "maestro_" + label + "_" + randomSuffix(t)
	if _, createErr := admin.Exec(fmt.Sprintf("CREATE DATABASE %q", name)); createErr != nil {
		_ = admin.Close()
		t.Fatalf("create %s: %v", name, createErr)
	}
	t.Cleanup(func() {
		defer func() { _ = admin.Close() }()
		if _, dropErr := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); dropErr != nil {
			t.Errorf("drop %s: %v", name, dropErr)
		}
	})

	dsn, err := cfg.DSNFor(rootKey, name)
	if err != nil {
		t.Fatalf("dsn for %s: %v", name, err)
	}
	if migrateErr := migrations.Up(context.Background(), dsn); migrateErr != nil {
		t.Fatalf("migrate %s: %v", name, migrateErr)
	}
	return dsn
}

// Pool opens a connection pool over a DSN, closed when the test ends.
func Pool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Blob gives a test its own bucket, created and versioned the way `up`
// creates the real one, and removed afterwards.
//
// One bucket per test rather than a shared one, for the same reason as one
// database per test, and one that bites harder: these tests write objects at
// CONTENT-ADDRESSED keys, so two tests storing the same bytes share a key.
// Cross-test interference on a content-addressed store is the normal case,
// not a hypothetical.
//
// The config comes back with the adapter because some states a test needs
// cannot be produced through it — an incomplete multipart upload is
// precisely what the adapter refuses to leave behind, and a sweep test has
// to be able to leave one.
func Blob(t *testing.T, label string) (*objects.Blob, objects.Config) {
	t.Helper()

	roots, err := paths.Resolve()
	if err != nil {
		t.Skipf("cannot resolve storage roots: %v", err)
	}
	rootKey, err := paths.EnsureKey(roots.Config)
	if err != nil {
		t.Skipf("cannot read the root-of-trust key: %v", err)
	}
	accessKey, err := secret.Derive(rootKey, secret.ContextObjectAccessKey)
	if err != nil {
		t.Fatalf("derive object access key: %v", err)
	}
	secretKey, err := secret.Derive(rootKey, secret.ContextObjectSecretKey)
	if err != nil {
		t.Fatalf("derive object secret key: %v", err)
	}

	cfg := objects.Config{
		Endpoint:  net.JoinHostPort("127.0.0.1", strconv.Itoa(minioPort(t))),
		Bucket:    "maestro-" + label + "-it-" + randomSuffix(t),
		AccessKey: accessKey,
		SecretKey: secretKey,
	}
	blob, err := objects.New(cfg)
	if err != nil {
		t.Fatalf("build object adapter: %v", err)
	}
	if err := blob.EnsureBucket(context.Background()); err != nil {
		t.Skipf("object store unavailable (run `make dataplane-up`): %v", err)
	}
	t.Cleanup(func() { removeBucket(t, &cfg) })
	return blob, cfg
}

// RootKey builds a file-backed key provider over a throwaway config root.
//
// A REAL provider, not a stub, so a store built for a test behaves like a
// store built in production. MayCreate because the root is empty and this IS
// first-time setup for it; the LoadOnly half of that rule belongs to the
// paths suite, which owns the decision.
func RootKey(t *testing.T) secret.RootKeyProvider {
	t.Helper()
	return paths.KeyFile(t.TempDir(), paths.MayCreate)
}

// stackConfig resolves the roots and key the running stack was launched with.
func stackConfig(t *testing.T) (*stack.Config, []byte) {
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
	return cfg, rootKey
}

// minioPort is the default unless the environment names another stack.
func minioPort(t *testing.T) int {
	t.Helper()
	raw := os.Getenv(minioPortEnv)
	if raw == "" {
		return defaultMinIOPort
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s=%q: %v", minioPortEnv, raw, err)
	}
	return port
}

// removeBucket empties and drops a disposable bucket. A versioned bucket
// refuses removal while anything remains, and "anything" includes delete
// markers and incomplete uploads.
func removeBucket(t *testing.T, cfg *objects.Config) {
	t.Helper()
	ctx := context.Background()

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
	})
	if err != nil {
		t.Errorf("cleanup: build client: %v", err)
		return
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		t.Errorf("cleanup: check bucket %s: %v", cfg.Bucket, err)
		return
	}
	if !exists {
		// A test may have removed it deliberately, to make every remote call
		// fail.
		return
	}
	if err := client.RemoveBucketWithOptions(ctx, cfg.Bucket,
		minio.RemoveBucketOptions{ForceDelete: true}); err != nil {
		t.Errorf("cleanup: remove bucket %s: %v", cfg.Bucket, err)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	return hex.EncodeToString(suffix)
}
