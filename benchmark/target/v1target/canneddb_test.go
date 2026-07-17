package v1target

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// writeCannedDB builds a minimal WAL-mode maestro.db matching v1's schema
// shape for the tables this adapter reads.
func writeCannedDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "maestro.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open canned db: %v", err)
	}
	defer db.Close() //nolint:errcheck // test fixture
	statements := []string{
		`CREATE TABLE specs (id TEXT PRIMARY KEY, session_id TEXT, created_at TEXT)`,
		`CREATE TABLE stories (
			id TEXT PRIMARY KEY, session_id TEXT, spec_id TEXT, status TEXT,
			tokens_used BIGINT DEFAULT 0, cost_usd DECIMAL(10,4) DEFAULT 0,
			pr_id TEXT, commit_hash TEXT)`,
		`CREATE TABLE tool_executions (id INTEGER PRIMARY KEY, session_id TEXT)`,
		`INSERT INTO specs VALUES ('spec-1', 'sess-1', '2026-07-16T00:00:00Z')`,
		`INSERT INTO stories VALUES ('st-1', 'sess-1', 'spec-1', 'done', 8000, 0.5, '1', 'abc'),
			('st-2', 'sess-1', 'spec-1', 'done', 4000, 0.25, '2', 'def')`,
		`INSERT INTO tool_executions (session_id) VALUES ('sess-1'), ('sess-1'), ('sess-1'), ('other')`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("canned db %q: %v", stmt[:30], err)
		}
	}
	return path
}
