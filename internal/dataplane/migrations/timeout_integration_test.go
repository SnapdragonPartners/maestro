//go:build integration

package migrations

import (
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// In-package because it needs withStatementTimeout, and because the claim
// being tested is about the connection this package builds.
//
// The unit test proves the option is in the connection string; this proves
// the SERVER honours it. Both matter — a correctly-formed option the server
// ignored would leave migrations exactly as unbounded as before, while
// looking fixed.
func TestStatementTimeoutReachesTheServer(t *testing.T) {
	dsn := testDSN(t)

	bounded, err := withStatementTimeout(dsn)
	if err != nil {
		t.Fatalf("withStatementTimeout: %v", err)
	}

	db, err := sql.Open("pgx", bounded)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var timeout string
	if err := db.QueryRow("SHOW statement_timeout").Scan(&timeout); err != nil {
		t.Skipf("data plane unavailable (run `make dataplane-up`): %v", err)
	}
	if timeout != "5min" {
		t.Errorf("statement_timeout is %q on the migration connection, want 5min", timeout)
	}

	// And the unbounded connection is NOT already limited, so the value
	// above demonstrably comes from our option rather than from the
	// server's own configuration.
	plain, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open plain: %v", err)
	}
	defer func() { _ = plain.Close() }()

	var plainTimeout string
	if err := plain.QueryRow("SHOW statement_timeout").Scan(&plainTimeout); err != nil {
		t.Fatalf("SHOW on plain connection: %v", err)
	}
	if plainTimeout == timeout {
		t.Errorf("the server already defaults to %q, so this test proves nothing about our option", plainTimeout)
	}
}
