package benchmark

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB creates a temporary SQLite DB with the stories table for testing.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE stories (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			spec_id TEXT,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			status TEXT DEFAULT 'new',
			hold_since DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create stories table: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertStory(t *testing.T, db *sql.DB, id, sessionID, status string, holdSince *time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO stories (id, session_id, spec_id, title, content, status, hold_since) VALUES (?, ?, 'spec1', 'title', 'content', ?, ?)`,
		id, sessionID, status, holdSince,
	)
	if err != nil {
		t.Fatalf("insert story: %v", err)
	}
}

func TestClassifyOnce_AllDone(t *testing.T) {
	db := setupTestDB(t)
	cfg := PollConfig{SpecID: "spec1", SessionID: "sess1"}

	insertStory(t, db, "s1", "sess1", "done", nil)
	insertStory(t, db, "s2", "sess1", "done", nil)

	outcome, err := classifyOnce(db, cfg)
	if err != nil {
		t.Fatalf("classifyOnce error: %v", err)
	}
	if outcome != OutcomeSuccess {
		t.Errorf("expected success, got %s", outcome)
	}
}

func TestClassifyOnce_TerminalFailure(t *testing.T) {
	db := setupTestDB(t)
	cfg := PollConfig{SpecID: "spec1", SessionID: "sess1"}

	insertStory(t, db, "s1", "sess1", "done", nil)
	insertStory(t, db, "s2", "sess1", "failed", nil)

	outcome, err := classifyOnce(db, cfg)
	if err != nil {
		t.Fatalf("classifyOnce error: %v", err)
	}
	if outcome != OutcomeTerminalFailure {
		t.Errorf("expected terminal_failure, got %s", outcome)
	}
}

func TestClassifyOnce_StillActive(t *testing.T) {
	db := setupTestDB(t)
	cfg := PollConfig{SpecID: "spec1", SessionID: "sess1"}

	insertStory(t, db, "s1", "sess1", "done", nil)
	insertStory(t, db, "s2", "sess1", "coding", nil)

	outcome, err := classifyOnce(db, cfg)
	if err != nil {
		t.Fatalf("classifyOnce error: %v", err)
	}
	if outcome != "" {
		t.Errorf("expected empty (in progress), got %s", outcome)
	}
}

func TestClassifyOnce_Stalled(t *testing.T) {
	db := setupTestDB(t)
	cfg := PollConfig{SpecID: "spec1", SessionID: "sess1", StallGrace: 1 * time.Minute}
	cfg.defaults()

	staleTime := time.Now().Add(-10 * time.Minute)
	insertStory(t, db, "s1", "sess1", "done", nil)
	insertStory(t, db, "s2", "sess1", "on_hold", &staleTime)

	outcome, err := classifyOnce(db, cfg)
	if err != nil {
		t.Fatalf("classifyOnce error: %v", err)
	}
	if outcome != OutcomeStalled {
		t.Errorf("expected stalled, got %s", outcome)
	}
}

func TestClassifyOnce_OnHoldWithinGrace(t *testing.T) {
	db := setupTestDB(t)
	cfg := PollConfig{SpecID: "spec1", SessionID: "sess1", StallGrace: 10 * time.Minute}
	cfg.defaults()

	recentTime := time.Now().Add(-1 * time.Minute)
	insertStory(t, db, "s1", "sess1", "done", nil)
	insertStory(t, db, "s2", "sess1", "on_hold", &recentTime)

	outcome, err := classifyOnce(db, cfg)
	if err != nil {
		t.Fatalf("classifyOnce error: %v", err)
	}
	if outcome != "" {
		t.Errorf("expected in-progress (on_hold within grace), got %s", outcome)
	}
}

func TestClassifyOnce_NoStories(t *testing.T) {
	db := setupTestDB(t)
	cfg := PollConfig{SpecID: "spec1", SessionID: "sess1"}

	outcome, err := classifyOnce(db, cfg)
	if err != nil {
		t.Fatalf("classifyOnce error: %v", err)
	}
	if outcome != "" {
		t.Errorf("expected empty (no stories yet), got %s", outcome)
	}
}

func TestClassifyOnce_SessionIsolation(t *testing.T) {
	db := setupTestDB(t)
	cfg := PollConfig{SpecID: "spec1", SessionID: "sess1"}

	// Stories in a different session should not be counted.
	insertStory(t, db, "s1", "sess1", "done", nil)
	insertStory(t, db, "s2", "other-session", "failed", nil)

	outcome, err := classifyOnce(db, cfg)
	if err != nil {
		t.Fatalf("classifyOnce error: %v", err)
	}
	if outcome != OutcomeSuccess {
		t.Errorf("expected success (other session story ignored), got %s", outcome)
	}
}
