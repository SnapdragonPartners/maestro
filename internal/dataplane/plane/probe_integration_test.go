//go:build integration

package plane_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/readiness"
	"orchestrator/internal/dataplane/registry"
)

// composition builds an otherwise-valid composition over dsn, so the only
// thing under test is what the probe observes about the database.
func composition(t *testing.T, dsn string) plane.Composition {
	t.Helper()
	blob, _ := planetest.Blob(t, "probe")
	types, err := registry.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return plane.Composition{DSN: dsn, Objects: blob, RootKey: planetest.RootKey(t), Types: types,
		Keys: configkeys.MustNew(nil)}
}

func expectCause(t *testing.T, err error, want readiness.Cause, remedyFragment string) {
	t.Helper()
	cause, ok := readiness.CauseOf(err)
	if !ok {
		t.Fatalf("no readiness cause in %v", err)
	}
	if cause != want {
		t.Fatalf("cause %q, want %q: %v", cause, want, err)
	}
	remedy, _ := readiness.RemedyOf(err)
	if !strings.Contains(remedy, remedyFragment) {
		t.Fatalf("remedy %q lacks %q", remedy, remedyFragment)
	}
}

// TestProbeRefusesAnUnreachableDatabaseAsUnreachable is the row that
// distinguishes a stopped service from a schema problem.
//
// THE MUTANT this must kill: delete the explicit Acquire in probe and read
// the version through the pool, which connects lazily inside the query. The
// connection failure then surfaces from the version read and is classified
// SchemaUnreadable, and this test fails on the cause. With the Acquire in
// place the mutant "read through the pool" still fails at Acquire and does
// not test anything; the mutant must remove the step the claim is about.
func TestProbeRefusesAnUnreachableDatabaseAsUnreachable(t *testing.T) {
	// A port nothing listens on: listen, learn the port, close.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	_, err = plane.Open(context.Background(), composition(t,
		fmt.Sprintf("postgres://maestro:x@%s/maestro?sslmode=disable&connect_timeout=2", addr)))
	expectCause(t, err, readiness.Unreachable, "start the database")
}

// TestProbeClassifiesEverySchemaState puts one migrated database through
// behind, ahead, dirty, never-migrated and unreadable, and asserts each
// cause and remedy.
func TestProbeClassifiesEverySchemaState(t *testing.T) {
	ctx := context.Background()
	dsn := planetest.DSN(t, "probe")
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	embedded, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("migrated plane opens", func(t *testing.T) {
		seam, openErr := plane.Open(ctx, composition(t, dsn))
		if openErr != nil {
			t.Fatalf("a migrated plane was refused: %v", openErr)
		}
		seam.Close()
	})

	t.Run("behind", func(t *testing.T) {
		if toErr := migrations.To(ctx, dsn, embedded-1); toErr != nil {
			t.Fatal(toErr)
		}
		t.Cleanup(func() {
			if upErr := migrations.Up(ctx, dsn); upErr != nil {
				t.Fatal(upErr)
			}
		})
		_, openErr := plane.Open(ctx, composition(t, dsn))
		expectCause(t, openErr, readiness.SchemaBehind, "pending migrations")
		if !strings.Contains(openErr.Error(), fmt.Sprintf("version %d", embedded-1)) {
			t.Fatalf("the refusal does not name the plane's version: %v", openErr)
		}
	})

	t.Run("never migrated is behind, not an error", func(t *testing.T) {
		// THE MUTANT: map an undefined table to SchemaUnreadable. The remedy
		// assertion below fails, because "inspect the plane" is wrong advice
		// for a cluster that simply needs migrating.
		if _, execErr := conn.Exec(ctx, "DROP TABLE schema_migrations"); execErr != nil {
			t.Fatal(execErr)
		}
		// Restored by shape, not by re-migrating: the schema itself is still
		// there, and Up against version 0 would re-run 000001 into it.
		t.Cleanup(func() {
			for _, stmt := range []string{
				"CREATE TABLE schema_migrations (version bigint not null primary key, dirty boolean not null)",
				fmt.Sprintf("INSERT INTO schema_migrations VALUES (%d, false)", embedded),
			} {
				if _, execErr := conn.Exec(ctx, stmt); execErr != nil {
					t.Fatal(execErr)
				}
			}
		})
		_, openErr := plane.Open(ctx, composition(t, dsn))
		expectCause(t, openErr, readiness.SchemaBehind, "pending migrations")
	})

	t.Run("ahead", func(t *testing.T) {
		setVersion(t, ctx, conn, int64(embedded)+1000, false)
		t.Cleanup(func() { setVersion(t, ctx, conn, int64(embedded), false) })
		_, openErr := plane.Open(ctx, composition(t, dsn))
		expectCause(t, openErr, readiness.SchemaAhead, "at or after")
	})

	t.Run("dirty", func(t *testing.T) {
		setVersion(t, ctx, conn, int64(embedded), true)
		t.Cleanup(func() { setVersion(t, ctx, conn, int64(embedded), false) })
		_, openErr := plane.Open(ctx, composition(t, dsn))
		expectCause(t, openErr, readiness.SchemaDirty, "repair")
	})

	t.Run("dirty outranks behind", func(t *testing.T) {
		setVersion(t, ctx, conn, int64(embedded)-1, true)
		t.Cleanup(func() { setVersion(t, ctx, conn, int64(embedded), false) })
		_, openErr := plane.Open(ctx, composition(t, dsn))
		expectCause(t, openErr, readiness.SchemaDirty, "repair")
	})

	t.Run("unreadable for any other reason", func(t *testing.T) {
		// A table of the right name and the wrong shape: the read fails on
		// a reachable plane for a reason that is not "never migrated".
		if _, execErr := conn.Exec(ctx, "ALTER TABLE schema_migrations RENAME COLUMN dirty TO soiled"); execErr != nil {
			t.Fatal(execErr)
		}
		t.Cleanup(func() {
			if _, execErr := conn.Exec(ctx, "ALTER TABLE schema_migrations RENAME COLUMN soiled TO dirty"); execErr != nil {
				t.Fatal(execErr)
			}
		})
		_, openErr := plane.Open(ctx, composition(t, dsn))
		expectCause(t, openErr, readiness.SchemaUnreadable, "inspect")
	})
}

// TestProbeFailureClosesThePool: a refused open holds no connection behind
// it. Observed through pg_stat_activity rather than inferred.
func TestProbeFailureClosesThePool(t *testing.T) {
	ctx := context.Background()
	dsn := planetest.DSN(t, "probeleak")
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	embedded, _ := migrations.Embedded()

	before := sessions(t, ctx, conn)
	setVersion(t, ctx, conn, int64(embedded)+1, false)
	t.Cleanup(func() { setVersion(t, ctx, conn, int64(embedded), false) })
	if _, openErr := plane.Open(ctx, composition(t, dsn)); openErr == nil {
		t.Fatal("an ahead plane opened")
	}
	if after := sessions(t, ctx, conn); after != before {
		t.Fatalf("%d sessions before the refused open, %d after: the probe leaked its pool", before, after)
	}
}

func setVersion(t *testing.T, ctx context.Context, conn *pgx.Conn, version int64, dirty bool) {
	t.Helper()
	if _, err := conn.Exec(ctx, "UPDATE schema_migrations SET version = $1, dirty = $2", version, dirty); err != nil {
		t.Fatal(err)
	}
}

// sessions counts connections to this test's database other than the
// counting one, polling briefly because a closed pool's sessions end
// asynchronously on the server side.
func sessions(t *testing.T, ctx context.Context, conn *pgx.Conn) int {
	t.Helper()
	var n int
	for attempt := 0; attempt < 50; attempt++ {
		if err := conn.QueryRow(ctx,
			"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()").
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return n
		}
		if _, err := conn.Exec(ctx, "SELECT pg_sleep(0.05)"); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	return n
}
