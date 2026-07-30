//go:build integration

package stack

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/paths"
)

// TestUpFinishesDeletionClaimsLeftBehind covers the step `up` gained for the
// object sweep: a deletion claim that survived an earlier run is unfinished
// destructive work, and a plane must not be reported ready while it carries
// any.
//
// What this test is for is the WIRING. That reconciliation is idempotent, is
// fenced to the ids a claim records, and never takes over another actor's
// claim is settled where those rules live, in the seam's own suite. What can
// only go wrong here is the composition: the seam has to be opened against
// the database `up` migrated and the bucket `up` provisioned, and a claim
// pointed at the wrong bucket would find nothing to delete and clear itself
// anyway -- reporting reclaimed storage that is still there.
//
// It exercises reconcileClaims rather than up, for the reason
// TestEnsureBucketMakesThePlaneAbleToStoreAnObject does: running up from a
// test would take the lifecycle lock and drive Compose against the
// developer's live plane. Both the database and the bucket here are
// disposable, which is also what keeps the test able to fail -- against the
// canonical pair, a step that did nothing would leave the assertions passing.
func TestUpFinishesDeletionClaimsLeftBehind(t *testing.T) {
	cfg, rootKey := disposablePlane(t)
	ctx := t.Context()

	blob, err := ensureBucket(ctx, cfg, rootKey)
	if err != nil {
		t.Fatalf("ensureBucket: %v", err)
	}

	// An object at a digest key, and a claim condemning exactly the version
	// that was written. This is the state a sweep leaves when it dies between
	// recording its intent and issuing the delete.
	organizationID := uuid.New()
	body := []byte("condemned by a sweep that never came back")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	key := organizationID.String() + "/" + digest[:2] + "/" + digest[2:4] + "/" + digest

	version, err := blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("write the condemned object: %v", err)
	}

	dsn, err := cfg.DSN(rootKey)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", cfg.Database, err)
	}
	defer func() { _ = database.Close() }()

	if _, err = database.ExecContext(ctx,
		`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1, $2, $3)`,
		organizationID, "reconcile-test", "Reconcile Test"); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	// Written directly, because what is under test is the recovery of a row
	// that already exists rather than the protocol that writes one.
	if _, err = database.ExecContext(ctx,
		`INSERT INTO deletion_claims (deletion_claim_id, organization_id, object_digest, version_ids, upload_ids)
		 VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), organizationID, digest, []string{version}, []string{}); err != nil {
		t.Fatalf("seed the deletion claim: %v", err)
	}

	if err = reconcileClaims(ctx, cfg, rootKey, blob); err != nil {
		t.Fatalf("reconcileClaims: %v", err)
	}

	versions, err := blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("%d versions survive at %s; the claim was cleared without its storage being "+
			"reclaimed, which is what a seam opened against the wrong bucket would do", len(versions), key)
	}
	var claims int
	if err = database.QueryRowContext(ctx,
		`SELECT count(*) FROM deletion_claims`).Scan(&claims); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if claims != 0 {
		t.Fatalf("%d deletion claims survive reconciliation, want 0", claims)
	}
}

// TestUpReconcilesAnEmptyClaimsTableQuietly is the everyday case: `up` runs
// this step on every start, and on a plane with nothing to recover it must be
// a no-op rather than a failure.
func TestUpReconcilesAnEmptyClaimsTableQuietly(t *testing.T) {
	cfg, rootKey := disposablePlane(t)
	ctx := t.Context()

	blob, err := ensureBucket(ctx, cfg, rootKey)
	if err != nil {
		t.Fatalf("ensureBucket: %v", err)
	}
	if err = reconcileClaims(ctx, cfg, rootKey, blob); err != nil {
		t.Fatalf("reconcileClaims over an empty table: %v", err)
	}
}

// disposablePlane gives a test a config pointing at a database and a bucket
// it owns outright, both removed afterwards.
//
// Neither may be the canonical one. This test writes rows and deletes
// objects, and doing either in the developer's working plane is the mistake
// the migrations suite already made once.
func disposablePlane(t *testing.T) (*Config, []byte) {
	t.Helper()

	roots, err := paths.Resolve()
	if err != nil {
		t.Skipf("cannot resolve storage roots: %v", err)
	}
	rootKey, err := paths.EnsureKey(roots.Config)
	if err != nil {
		t.Skipf("cannot read the root-of-trust key: %v", err)
	}
	cfg, err := NewConfig(roots)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}

	suffix := make([]byte, 8)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatalf("generate suffix: %v", err)
	}
	label := hex.EncodeToString(suffix)

	adminDSN, err := cfg.DSN(rootKey)
	if err != nil {
		t.Fatalf("admin dsn: %v", err)
	}
	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	// Not deferred: t.Cleanup runs after this returns, and the drop needs the
	// connection still open.
	if err = admin.Ping(); err != nil {
		_ = admin.Close()
		t.Skipf("data plane unavailable (run `make dataplane-up`): %v", err)
	}

	name := "maestro_stack_" + label
	if _, err = admin.Exec(fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		_ = admin.Close()
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() {
		defer func() { _ = admin.Close() }()
		if _, dropErr := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); dropErr != nil {
			t.Errorf("drop %s: %v", name, dropErr)
		}
	})

	cfg.Database = name
	cfg.Bucket = "maestro-stack-it-" + label
	t.Cleanup(func() { removeBucket(t, cfg) })

	dsn, err := cfg.DSN(rootKey)
	if err != nil {
		t.Fatalf("dsn for %s: %v", name, err)
	}
	if err = migrations.Up(context.Background(), dsn); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	return cfg, rootKey
}
