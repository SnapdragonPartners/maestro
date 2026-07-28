// Package migrations carries the data plane's schema as embedded SQL and
// applies it.
//
// The migration files are embedded rather than read from disk. That is not
// just tidiness: the compose assets in deploy/ cannot be embedded, because
// embedding refuses to reach parent directories (maestro#287), and the
// schema must not inherit that problem — a binary that cannot find its own
// migrations is a binary that cannot start.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // Registers the pgx driver the migrate postgres driver opens with.
)

// FS holds every migration file. The `.sql` files beside this one are the
// schema's source of truth.
//
//go:embed *.sql
var FS embed.FS

// Up applies every pending migration, and is a no-op when the schema is
// already current.
//
// Concurrency is handled twice over and neither guard is redundant:
// callers reach this through the data-plane lifecycle lock (serialising
// operations on this machine), and golang-migrate additionally takes a
// Postgres advisory lock, which covers a caller that bypassed the launcher
// entirely.
func Up(ctx context.Context, dsn string) (err error) {
	m, closeFn, err := open(dsn)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeFn(); closeErr != nil && err == nil {
			err = fmt.Errorf("close migrator: %w", closeErr)
		}
	}()

	return run(ctx, m, m.Up, "apply migrations")
}

// To migrates to a specific version, up or down.
//
// It exists for staged upgrades and for tests that must observe a database
// at an intermediate version -- verifying what a migration does to data
// written under the schema BEFORE it needs exactly this, and applying every
// migration to an empty database can never show it.
func To(ctx context.Context, dsn string, version uint) (err error) {
	m, closeFn, err := open(dsn)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeFn(); closeErr != nil && err == nil {
			err = fmt.Errorf("close migrator: %w", closeErr)
		}
	}()

	return run(ctx, m, func() error { return m.Migrate(version) }, fmt.Sprintf("migrate to version %d", version))
}

// Force sets the recorded schema version and clears the dirty flag WITHOUT
// running any migration.
//
// This is a repair tool, and the only way out of a dirty version. When a
// migration fails, golang-migrate has already recorded the target version
// with dirty = true -- it marks BEFORE executing -- so every later
// migration refuses to run until the flag is cleared. Deleting the rows
// that caused the failure is not enough on its own; the metadata still says
// a migration is half-applied.
//
// It changes only the metadata. The caller is asserting that the database
// really is at the version being forced, and a wrong assertion leaves the
// schema and its recorded version disagreeing, which no later migration can
// detect. Use it after establishing what actually applied.
func Force(dsn string, version int) (err error) {
	m, closeFn, err := open(dsn)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeFn(); closeErr != nil && err == nil {
			err = fmt.Errorf("close migrator: %w", closeErr)
		}
	}()

	if forceErr := m.Force(version); forceErr != nil {
		return fmt.Errorf("force schema version to %d: %w", version, forceErr)
	}
	return nil
}

// run executes a migration operation under the caller's context.
//
// Two mechanisms, because neither alone is sufficient. GracefulStop makes
// migrate finish the current migration and stop before the next, which
// bounds a long SEQUENCE but not a single blocked statement. The statement
// timeout on the connection (see open) bounds the statement itself. Without
// both, a DDL blocked behind another session's lock hangs indefinitely --
// while holding the data-plane lifecycle lock, so nothing else can proceed
// either.
func run(ctx context.Context, m *migrate.Migrate, op func() error, what string) error {
	return runOp(ctx, gracefulStopper(m), op, what)
}

// gracefulStopper asks migrate to stop at the next migration boundary.
//
// The send is bounded as insurance, NOT as a fix for an observed deadlock.
// An earlier version of this comment claimed GracefulStop was unbuffered
// and that a plain send could hang forever; that was wrong. In the pinned
// golang-migrate v4.19.1 the channel is `make(chan bool, 1)` (migrate.go),
// so one send always succeeds immediately even when the operation has
// already finished and nobody will ever receive it.
//
// The bound is kept because it costs nothing and removes the dependency on
// that capacity: a buffer that is already full -- or an upstream change to
// an unbuffered channel -- would turn a plain send into a hang while this
// caller holds the data-plane lifecycle lock. It guards an assumption
// rather than a bug.
func gracefulStopper(m *migrate.Migrate) func() {
	return func() {
		select {
		case m.GracefulStop <- true:
		case <-time.After(stopSignalTimeout):
			// Nobody is listening, which means the operation is already
			// finishing on its own. The wait below collects it.
		}
	}
}

// runOp executes op under the caller's context, asking it to stop early if
// the context is cancelled.
//
// Split from run so the cancellation path is testable without a database:
// its correctness is about channel choreography, not SQL, and the failure
// it guards against (a hung migration holding the lifecycle lock) is one no
// integration test would reliably reproduce.
func runOp(ctx context.Context, stop func(), op func() error, what string) error {
	done := make(chan error, 1)
	go func() { done <- op() }()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	case <-ctx.Done():
		stop()
		// Wait for the operation to actually finish. Returning early would
		// report the database as unmigrated while a migration is still
		// running against it.
		<-done
		return fmt.Errorf("%s: %w", what, ctx.Err())
	}
}

// Down reverses every migration, leaving an empty schema.
//
// Exported for tests and local iteration, NOT wired to a make target. Down
// migrations are development reversibility, never a production rollback
// story: one that drops a table destroys its data, so recovery in anger is
// restore-from-backup. Keeping this off the command surface is deliberate —
// a `dataplane-down` that sounded like it stopped containers but instead
// dropped the schema would be a trap.
func Down(ctx context.Context, dsn string) (err error) {
	m, closeFn, err := open(dsn)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeFn(); closeErr != nil && err == nil {
			err = fmt.Errorf("close migrator: %w", closeErr)
		}
	}()

	return run(ctx, m, m.Down, "reverse migrations")
}

// statementTimeout bounds a single DDL statement. Generous, because
// creating indexes on a populated table is legitimately slow; the point is
// that a statement blocked behind another session's lock fails instead of
// hanging forever.
const statementTimeout = 5 * time.Minute

// stopSignalTimeout bounds the attempt to signal migrate. See
// gracefulStopper for why an unbounded send is a deadlock.
const stopSignalTimeout = 5 * time.Second

// withStatementTimeout returns dsn with a server-side statement timeout.
//
// Set through the connection options rather than a SET statement: database/
// sql pools connections, so a SET on one connection would not apply to the
// others migrate uses.
func withStatementTimeout(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse data-plane dsn: %w", err)
	}
	q := u.Query()
	// APPEND, never replace: `options` is a space-separated list of -c
	// settings, and a caller supplying search_path or application_name
	// would otherwise have them silently dropped by a timeout we added.
	timeout := fmt.Sprintf("-c statement_timeout=%d", statementTimeout.Milliseconds())
	if existing := strings.TrimSpace(q.Get("options")); existing != "" {
		timeout = existing + " " + timeout
	}
	q.Set("options", timeout)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Version reports the current schema version and whether it is dirty.
//
// A dirty version means a migration failed partway. Recovery is documented
// in docs/v2/phase_2/design_schema_core.md and is deliberately NOT
// automated: the correct repair depends on whether the failed migration
// still succeeds from empty, which is a judgement about the repository
// rather than about this database.
func Version(dsn string) (version uint, dirty bool, err error) {
	m, closeFn, err := open(dsn)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = closeFn() }()

	version, dirty, err = m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		// An empty database is version 0, not an error.
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read schema version: %w", err)
	}
	return version, dirty, nil
}

// open builds a migrate instance over the embedded files. The returned
// function releases both the source and the database handle.
func open(dsn string) (*migrate.Migrate, func() error, error) {
	source, err := iofs.New(FS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	bounded, err := withStatementTimeout(dsn)
	if err != nil {
		return nil, nil, err
	}

	db, err := sql.Open("pgx", bounded)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("prepare migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("prepare migrator: %w", err)
	}

	// Close the MIGRATOR, not just the database handle: it owns the
	// embedded source driver too, and closing only the *sql.DB leaks that
	// source on every call -- which is every `dataplane-up`. The postgres
	// driver's Close also closes the *sql.DB, because WithInstance records
	// it (postgres.go: `px.db = instance`), so this is the whole cleanup
	// rather than half of it.
	closeFn := func() error {
		sourceErr, dbErr := m.Close()
		//nolint:wrapcheck // Both errors originate here; Join only combines them.
		return errors.Join(sourceErr, dbErr)
	}
	return m, closeFn, nil
}
