package migrations

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the one query method the version read needs. *pgx.Conn,
// *pgxpool.Pool and pgx.Tx all satisfy it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// versionTable is golang-migrate's default table, whose shape this package
// verified against the pinned driver (v4.19.1, database/postgres/postgres.go):
// `version bigint not null primary key, dirty boolean not null`.
const versionTable = "schema_migrations"

// sqlstateUndefinedTable is PostgreSQL's code for a table that does not exist.
const sqlstateUndefinedTable = "42P01"

// VersionOn reads the schema version over a connection the caller already
// holds, so reachability and version are one connection's two facts rather
// than two operations that might disagree (Phase 3 item 3, design D4).
//
// Version(dsn) opens its own handle and is what the lifecycle verbs use. This
// is for a caller — the seam's probe — that has just acquired a connection
// and must learn the schema version ON it.
//
// It preserves the pinned driver's contract exactly: no rows and no table
// are both version 0, clean. A cluster nobody has migrated is "behind", with
// migrating as the remedy; only a read that fails for any OTHER reason is an
// error.
func VersionOn(ctx context.Context, q Querier) (version uint, dirty bool, err error) {
	var stored int64
	scanErr := q.QueryRow(ctx, "SELECT version, dirty FROM "+versionTable+" LIMIT 1").Scan(&stored, &dirty)
	switch {
	case errors.Is(scanErr, pgx.ErrNoRows), isUndefinedTable(scanErr):
		return 0, false, nil
	case scanErr != nil:
		return 0, false, fmt.Errorf("read schema version: %w", scanErr)
	case stored < 0:
		return 0, false, fmt.Errorf("read schema version: stored version %d is negative", stored)
	}
	return uint(stored), dirty, nil
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlstateUndefinedTable
}

// migrationName matches an embedded up migration and captures its version.
var migrationName = regexp.MustCompile(`^(\d+)_.+\.up\.sql$`)

// Embedded returns the highest migration version this binary carries — the
// version a plane must be at for this binary to use it.
//
// Derived from the embedded files every call rather than declared, so it
// cannot go stale when a migration is added.
func Embedded() (uint, error) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return 0, fmt.Errorf("list embedded migrations: %w", err)
	}
	var highest uint
	found := false
	for _, entry := range entries {
		m := migrationName.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		version, parseErr := strconv.ParseUint(m[1], 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("embedded migration %s: %w", entry.Name(), parseErr)
		}
		found = true
		if uint(version) > highest {
			highest = uint(version)
		}
	}
	if !found {
		return 0, errors.New("no embedded up migrations; a binary that cannot find its own schema cannot start")
	}
	return highest, nil
}
