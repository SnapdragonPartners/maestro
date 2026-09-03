//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/migrations"
)

// TestVersionOnPreservesTheDriverContract: a migrated plane reports the
// embedded version; an absent table and an empty one are both version 0,
// clean; and a dirty flag comes through as written.
//
// The absent-table case is the one that matters. The pinned driver maps
// undefined_table to NilVersion deliberately, and a probe that reported it
// as "unreadable" would tell the operator to inspect a cluster that simply
// needs migrating.
func TestVersionOnPreservesTheDriverContract(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	embedded, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}

	assertVersion(t, ctx, conn, embedded, false, "migrated")

	if _, execErr := conn.Exec(ctx, "UPDATE schema_migrations SET dirty = true"); execErr != nil {
		t.Fatal(execErr)
	}
	assertVersion(t, ctx, conn, embedded, true, "dirty")

	if _, execErr := conn.Exec(ctx, "DELETE FROM schema_migrations"); execErr != nil {
		t.Fatal(execErr)
	}
	assertVersion(t, ctx, conn, 0, false, "table with no rows")

	// Last, because nothing re-migrates afterwards: the schema is still
	// present and Up against version 0 would re-run 000001 into it. The
	// database is disposable and is dropped at cleanup.
	if _, execErr := conn.Exec(ctx, "DROP TABLE schema_migrations"); execErr != nil {
		t.Fatal(execErr)
	}
	assertVersion(t, ctx, conn, 0, false, "no table")
}

func assertVersion(t *testing.T, ctx context.Context, q migrations.Querier, wantVersion uint, wantDirty bool, state string) {
	t.Helper()
	version, dirty, err := migrations.VersionOn(ctx, q)
	if err != nil {
		t.Fatalf("%s: VersionOn: %v", state, err)
	}
	if version != wantVersion || dirty != wantDirty {
		t.Fatalf("%s: VersionOn = (%d, %v), want (%d, %v)", state, version, dirty, wantVersion, wantDirty)
	}
}
