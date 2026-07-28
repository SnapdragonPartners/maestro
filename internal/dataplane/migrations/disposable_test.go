//go:build integration

package migrations_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/stack"
)

// disposableDatabase creates a uniquely-named database, migrates it, and
// drops it when the test finishes.
//
// This exists because the first version of the down-migration test ran
// against the CANONICAL `maestro` database and dropped every table in it.
// Any test that migrates down, or that would leave rows behind, must use a
// database it owns outright. Reversibility is worth testing; it is not
// worth the developer's working data.
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
	// Deliberately NOT deferred: t.Cleanup runs after this function
	// returns, so a deferred close would leave the drop with a closed
	// connection — which is exactly how the first version leaked a
	// database. The cleanup owns the handle's lifetime.
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Skipf("data plane unavailable (run `make dataplane-up`): %v", err)
	}

	// Random suffix, so concurrent runs cannot collide on the name and a
	// leaked database from an earlier crash is never reused.
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	name := "maestro_test_" + hex.EncodeToString(suffix)

	// CREATE DATABASE cannot run inside a transaction, hence Exec directly.
	if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		_ = admin.Close()
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() {
		defer func() { _ = admin.Close() }()
		// FORCE terminates any lingering connection; without it a leaked
		// handle turns cleanup into a failure that leaves the database
		// behind for the next run to trip over.
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

// disposableDatabaseAt is disposableDatabase stopped at a specific version,
// for tests that must write rows under an older schema and then migrate
// over them.
func disposableDatabaseAt(t *testing.T, version uint) string {
	t.Helper()

	dsn := disposableDatabase(t)
	if err := migrations.To(context.Background(), dsn, version); err != nil {
		t.Fatalf("migrate down to v%d: %v", version, err)
	}
	return dsn
}

// Down migrations are written for development reversibility, and this is
// what stops them being decorative: an up-down-up round trip must return to
// the same schema.
func TestDownThenUpRestoresSchema(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)

	before := tableNames(t, dsn)
	if len(before) == 0 {
		t.Fatal("no tables after migrating up")
	}

	if err := migrations.Down(ctx, dsn); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if remaining := tableNames(t, dsn); len(remaining) != 0 {
		t.Errorf("down left tables behind: %v", remaining)
	}

	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("Up after Down: %v", err)
	}
	after := tableNames(t, dsn)
	if len(after) != len(before) {
		t.Errorf("schema differs after the round trip: %d tables before, %d after", len(before), len(after))
	}

	version, dirty, err := migrations.Version(dsn)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if dirty {
		t.Error("schema is dirty after a clean round trip")
	}
	if version == 0 {
		t.Error("schema version is 0 after migrating")
	}
}

// tableNames lists user tables, excluding migrate's own bookkeeping.
func tableNames(t *testing.T, dsn string) []string {
	t.Helper()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`SELECT tablename FROM pg_tables
	                       WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
	                       ORDER BY tablename`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return names
}
