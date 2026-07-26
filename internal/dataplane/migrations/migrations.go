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
	"database/sql"
	"embed"
	"errors"
	"fmt"

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
func Up(dsn string) error {
	m, closeFn, err := open(dsn)
	if err != nil {
		return err
	}
	defer closeFn()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// Down reverses every migration, leaving an empty schema.
//
// Exported for tests and local iteration, NOT wired to a make target. Down
// migrations are development reversibility, never a production rollback
// story: one that drops a table destroys its data, so recovery in anger is
// restore-from-backup. Keeping this off the command surface is deliberate —
// a `dataplane-down` that sounded like it stopped containers but instead
// dropped the schema would be a trap.
func Down(dsn string) error {
	m, closeFn, err := open(dsn)
	if err != nil {
		return err
	}
	defer closeFn()

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("reverse migrations: %w", err)
	}
	return nil
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
	defer closeFn()

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
func open(dsn string) (*migrate.Migrate, func(), error) {
	source, err := iofs.New(FS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	db, err := sql.Open("pgx", dsn)
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

	return m, func() { _ = db.Close() }, nil
}
