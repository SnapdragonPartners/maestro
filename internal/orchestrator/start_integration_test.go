//go:build integration

package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/readiness"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/orchestrator"
)

// opener builds a provider-neutral Opener over a disposable plane. It is
// the shape the composition root supplies, minus the local composer: the
// Orchestrator under test must not know how this was built.
func opener(t *testing.T, dsn string) orchestrator.Opener {
	t.Helper()
	blob, _ := planetest.Blob(t, "orch")
	types, err := orchestrator.Registry()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context) (store.Store, error) {
		return plane.Open(ctx, plane.Composition{
			DSN: dsn, Objects: blob, RootKey: planetest.RootKey(t),
			Caller: plane.Caller{Types: types, Keys: orchestrator.Keys(), Prompts: orchestrator.Prompts(), Actions: orchestrator.Actions(), Harness: planetest.Harness(t)},
		})
	}
}

// TestStartRefusesANotReadyPlaneWithCauseAndRemedy: the startup contract
// end to end through Start, for the unreachable state. The four schema
// states follow below; the local marker, key and object-store states are
// driven through the real composition root in cmd/dataplanectl's tests.
func TestStartRefusesANotReadyPlaneWithCauseAndRemedy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	dsn := fmt.Sprintf("postgres://maestro:x@%s/maestro?sslmode=disable&connect_timeout=2", addr)

	_, err = orchestrator.Start(context.Background(), opener(t, dsn), orchestrator.Config{OrganizationSlug: "acme", OperatorHandle: "dan"})
	var refused *orchestrator.StartupRefused
	if !errors.As(err, &refused) {
		t.Fatalf("want a StartupRefused, got %v", err)
	}
	if refused.Cause != readiness.Unreachable {
		t.Fatalf("cause %s, want unreachable", refused.Cause)
	}
	if refused.Remedy == "" || !strings.Contains(err.Error(), "remedy:") {
		t.Fatalf("the refusal does not render a remedy: %v", err)
	}
	// The producer's diagnostic -- the endpoint and the driver's refusal --
	// is rendered, not only unwrap-able. THE MUTANT: drop Err from Error().
	for _, want := range []string{"detail:", addr, "connection refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rendering lacks %q:\n%s", want, err)
		}
	}
}

// TestStartResolvesIdentityBeforeRecovering: an unprovisioned organization
// is a typed refusal, and startup provisions nothing.
func TestStartRefusesAnUnprovisionedIdentity(t *testing.T) {
	dsn := planetest.DSN(t, "orchid")
	_, err := orchestrator.Start(context.Background(), opener(t, dsn), orchestrator.Config{OrganizationSlug: "ghost", OperatorHandle: "nobody"})
	if !errors.Is(err, orchestrator.ErrNotProvisioned) {
		t.Fatalf("want ErrNotProvisioned, got %v", err)
	}
	seam, err := opener(t, dsn)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer seam.Close()
	if _, err := seam.GetOrganizationBySlug(context.Background(), "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("startup provisioned the organization: %v", err)
	}
}

// TestStartRecoversAnEmptyPlane: a provisioned tenant with no open work
// starts, and the projection is empty with every class present at zero.
func TestStartRecoversAnEmptyPlane(t *testing.T) {
	dsn := planetest.DSN(t, "orchempty")
	ctx := context.Background()
	seam, err := opener(t, dsn)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	org, err := seam.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{Slug: "acme", DisplayName: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seam.BootstrapUser(ctx, store.BootstrapUserInput{Handle: "dan", DisplayName: "Dan", OrganizationID: org.Record.OrganizationID}); err != nil {
		t.Fatal(err)
	}
	seam.Close()

	o, err := orchestrator.Start(ctx, opener(t, dsn), orchestrator.Config{OrganizationSlug: "acme", OperatorHandle: "dan"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer o.Close()
	if o.Organization().Slug != "acme" || o.Operator().Handle != "dan" {
		t.Fatalf("identity %+v / %+v", o.Organization(), o.Operator())
	}
	p := o.Projection()
	if len(p.Rows) != 0 {
		t.Fatalf("an empty plane projected %d rows", len(p.Rows))
	}
	for _, c := range orchestrator.Classes {
		if n := p.Counts[c]; n != 0 {
			t.Fatalf("class %s counts %d on an empty plane", c, n)
		}
	}
}

// TestStartRendersEverySchemaState is design D5's four schema rows driven
// THROUGH orchestrator.Start over a reachable plane: behind (including the
// never-migrated version 0), ahead, dirty and unreadable. The probe's own
// tests classify these one layer down; Checkpoint 1 owes the demonstration
// at the Orchestrator, where the cause, the version the plane is actually at
// and the remedy are what the operator reads. Never self-migrate is asserted
// by shape: after each refusal the plane's version is exactly what the case
// planted.
func TestStartRendersEverySchemaState(t *testing.T) {
	ctx := context.Background()
	dsn := planetest.DSN(t, "orchschema")
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	embedded, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	setVersion := func(t *testing.T, version int64, dirty bool) {
		t.Helper()
		if _, err := conn.Exec(ctx, "UPDATE schema_migrations SET version = $1, dirty = $2", version, dirty); err != nil {
			t.Fatal(err)
		}
	}
	readVersion := func(t *testing.T) (int64, bool) {
		t.Helper()
		var version int64
		var dirty bool
		if err := conn.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
			t.Fatal(err)
		}
		return version, dirty
	}
	start := func(t *testing.T) *orchestrator.StartupRefused {
		t.Helper()
		_, err := orchestrator.Start(ctx, opener(t, dsn), orchestrator.Config{OrganizationSlug: "acme", OperatorHandle: "dan"})
		var refused *orchestrator.StartupRefused
		if !errors.As(err, &refused) {
			t.Fatalf("want a StartupRefused, got %v", err)
		}
		// The rendering an operator reads carries all three parts.
		for _, part := range []string{string(refused.Cause), "observed:", "remedy:"} {
			if !strings.Contains(err.Error(), part) {
				t.Fatalf("rendering lacks %q: %s", part, err)
			}
		}
		return refused
	}
	expect := func(t *testing.T, refused *orchestrator.StartupRefused, cause readiness.Cause, fragments ...string) {
		t.Helper()
		if refused.Cause != cause {
			t.Fatalf("cause %s, want %s: %v", refused.Cause, cause, refused)
		}
		for _, want := range fragments {
			if !strings.Contains(refused.Error(), want) {
				t.Fatalf("rendering lacks %q:\n%s", want, refused)
			}
		}
	}

	t.Run("behind", func(t *testing.T) {
		if err := migrations.To(ctx, dsn, embedded-1); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := migrations.Up(ctx, dsn); err != nil {
				t.Fatal(err)
			}
		})
		refused := start(t)
		// The plane's version is named, and so is the binary's.
		expect(t, refused, readiness.SchemaBehind, fmt.Sprintf("version %d", embedded-1), fmt.Sprintf("needs %d", embedded), "pending migrations")
		// THE MUTANT: have Start migrate on its way in. The version must be
		// exactly where the case left it.
		if version, dirty := readVersion(t); version != int64(embedded-1) || dirty {
			t.Fatalf("startup moved the plane to %d (dirty=%v): the Orchestrator never migrates", version, dirty)
		}
	})

	t.Run("never migrated is behind at version 0", func(t *testing.T) {
		if _, err := conn.Exec(ctx, "DROP TABLE schema_migrations"); err != nil {
			t.Fatal(err)
		}
		// Restored by shape, as the probe's test does: the schema is intact
		// and Up against version 0 would re-run 000001 into it.
		t.Cleanup(func() {
			for _, stmt := range []string{
				"CREATE TABLE schema_migrations (version bigint not null primary key, dirty boolean not null)",
				fmt.Sprintf("INSERT INTO schema_migrations VALUES (%d, false)", embedded),
			} {
				if _, err := conn.Exec(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
		})
		refused := start(t)
		expect(t, refused, readiness.SchemaBehind, "version 0", "pending migrations")
		// Nothing was created on the way out.
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatal("startup created schema_migrations on a never-migrated plane")
		}
	})

	t.Run("ahead", func(t *testing.T) {
		setVersion(t, int64(embedded)+1000, false)
		t.Cleanup(func() { setVersion(t, int64(embedded), false) })
		refused := start(t)
		expect(t, refused, readiness.SchemaAhead, fmt.Sprintf("version %d", embedded+1000), fmt.Sprintf("knows only %d", embedded), "never downgrade")
		if version, _ := readVersion(t); version != int64(embedded)+1000 {
			t.Fatalf("startup moved an ahead plane to %d: never downgrade", version)
		}
	})

	t.Run("dirty", func(t *testing.T) {
		setVersion(t, int64(embedded), true)
		t.Cleanup(func() { setVersion(t, int64(embedded), false) })
		refused := start(t)
		expect(t, refused, readiness.SchemaDirty, fmt.Sprintf("version %d", embedded), "dirty", "repair")
		if _, dirty := readVersion(t); !dirty {
			t.Fatal("startup cleared the dirty flag: repair is the operator's, not the Orchestrator's")
		}
	})

	t.Run("dirty outranks behind", func(t *testing.T) {
		setVersion(t, int64(embedded)-1, true)
		t.Cleanup(func() { setVersion(t, int64(embedded), false) })
		expect(t, start(t), readiness.SchemaDirty, fmt.Sprintf("version %d", embedded-1), "repair")
	})

	t.Run("unreadable", func(t *testing.T) {
		// A table of the right name and the wrong shape: the read fails on
		// a reachable plane for a reason that is not "never migrated".
		if _, err := conn.Exec(ctx, "ALTER TABLE schema_migrations RENAME COLUMN dirty TO soiled"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := conn.Exec(ctx, "ALTER TABLE schema_migrations RENAME COLUMN soiled TO dirty"); err != nil {
				t.Fatal(err)
			}
		})
		refused := start(t)
		// The read error itself is rendered, not only the classification:
		// "inspect the plane" without the driver's complaint is not a remedy.
		expect(t, refused, readiness.SchemaUnreadable, "inspect the plane", "detail:", "dirty")
	})

	t.Run("migrated plane starts", func(t *testing.T) {
		// The control: with every case restored, the same opener is accepted
		// as far as identity, which this plane does not hold.
		_, err := orchestrator.Start(ctx, opener(t, dsn), orchestrator.Config{OrganizationSlug: "acme", OperatorHandle: "dan"})
		if !errors.Is(err, orchestrator.ErrNotProvisioned) {
			t.Fatalf("a restored plane was still refused: %v", err)
		}
	})
}
