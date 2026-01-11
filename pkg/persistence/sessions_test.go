package persistence

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB creates an in-memory SQLite database for testing.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Create required tables
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		ended_at TIMESTAMP,
		status TEXT NOT NULL DEFAULT 'active',
		config_json TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS stories (
		id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		spec_id TEXT NOT NULL,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'new',
		priority INTEGER DEFAULT 0,
		approved_plan TEXT DEFAULT '',
		story_type TEXT DEFAULT 'app',
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		started_at TIMESTAMP,
		completed_at TIMESTAMP,
		assigned_agent TEXT DEFAULT '',
		tokens_used INTEGER DEFAULT 0,
		cost_usd REAL DEFAULT 0.0,
		metadata TEXT DEFAULT '',
		PRIMARY KEY (id, session_id),
		FOREIGN KEY (session_id) REFERENCES sessions(session_id)
	);

	CREATE TABLE IF NOT EXISTS architect_state (
		session_id TEXT PRIMARY KEY,
		state TEXT NOT NULL,
		current_spec_id TEXT,
		escalation_counts_json TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS pm_state (
		session_id TEXT PRIMARY KEY,
		state TEXT NOT NULL,
		spec_content TEXT,
		bootstrap_params_json TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS story_dependencies (
		story_id TEXT NOT NULL,
		depends_on TEXT NOT NULL,
		PRIMARY KEY (story_id, depends_on)
	);
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	return db
}

func TestGetMostRecentResumableSession_NoSessions(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	result, err := GetMostRecentResumableSession(db)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result != nil {
		t.Errorf("Expected nil result when no sessions exist, got: %+v", result)
	}
}

func TestGetMostRecentResumableSession_ShutdownSession(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sessionID := "session-shutdown"

	// Create a shutdown session
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, started_at, ended_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, SessionStatusShutdown, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	// Add an incomplete story (required for session to be resumable)
	_, err = db.Exec(`
		INSERT INTO stories (id, session_id, spec_id, title, content, status, story_type)
		VALUES (?, ?, 'spec-1', 'Title', 'Content', 'new', 'app')
	`, "story-1", sessionID)
	if err != nil {
		t.Fatalf("Failed to insert story: %v", err)
	}

	result, err := GetMostRecentResumableSession(db)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.Session.SessionID != sessionID {
		t.Errorf("Expected session ID '%s', got '%s'", sessionID, result.Session.SessionID)
	}
	if result.Session.Status != SessionStatusShutdown {
		t.Errorf("Expected status '%s', got '%s'", SessionStatusShutdown, result.Session.Status)
	}
}

func TestGetMostRecentResumableSession_CrashedSession(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sessionID := "session-crashed"

	// Create an active session (will be treated as crashed since process died)
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, started_at)
		VALUES (?, ?, ?)
	`, sessionID, SessionStatusActive, time.Now())
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	// Add an incomplete story (required for session to be resumable)
	_, err = db.Exec(`
		INSERT INTO stories (id, session_id, spec_id, title, content, status, story_type)
		VALUES (?, ?, 'spec-1', 'Title', 'Content', 'planning', 'app')
	`, "story-1", sessionID)
	if err != nil {
		t.Fatalf("Failed to insert story: %v", err)
	}

	result, err := GetMostRecentResumableSession(db)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.Session.SessionID != sessionID {
		t.Errorf("Expected session ID '%s', got '%s'", sessionID, result.Session.SessionID)
	}
	// Status should be updated to "crashed" (was active, meaning process died unexpectedly)
	if result.Session.Status != SessionStatusCrashed {
		t.Errorf("Expected status '%s', got '%s'", SessionStatusCrashed, result.Session.Status)
	}
}

func TestGetMostRecentResumableSession_MostRecent(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Create older shutdown session
	older := time.Now().Add(-time.Hour)
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, started_at, ended_at)
		VALUES (?, ?, ?, ?)
	`, "session-older", SessionStatusShutdown, older, older)
	if err != nil {
		t.Fatalf("Failed to insert older session: %v", err)
	}

	// Create newer shutdown session
	newer := time.Now()
	_, err = db.Exec(`
		INSERT INTO sessions (session_id, status, started_at, ended_at)
		VALUES (?, ?, ?, ?)
	`, "session-newer", SessionStatusShutdown, newer, newer)
	if err != nil {
		t.Fatalf("Failed to insert newer session: %v", err)
	}

	// Add incomplete stories to both sessions
	for _, sessionID := range []string{"session-older", "session-newer"} {
		_, err = db.Exec(`
			INSERT INTO stories (id, session_id, spec_id, title, content, status, story_type)
			VALUES (?, ?, 'spec-1', 'Title', 'Content', 'new', 'app')
		`, "story-"+sessionID, sessionID)
		if err != nil {
			t.Fatalf("Failed to insert story for %s: %v", sessionID, err)
		}
	}

	result, err := GetMostRecentResumableSession(db)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.Session.SessionID != "session-newer" {
		t.Errorf("Expected most recent session 'session-newer', got '%s'", result.Session.SessionID)
	}
}

func TestGetMostRecentResumableSession_WithStoryCounts(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sessionID := "session-with-stories"

	// Create session
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, started_at, ended_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, SessionStatusShutdown, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	// Add stories with various statuses
	stories := []struct {
		id     string
		status string
	}{
		{"story-1", "new"},
		{"story-2", "planning"},
		{"story-3", "coding"},
		{"story-4", "done"},
		{"story-5", "done"},
	}

	for _, s := range stories {
		_, insertErr := db.Exec(`
			INSERT INTO stories (id, session_id, spec_id, title, content, status)
			VALUES (?, ?, 'spec-1', 'Title', 'Content', ?)
		`, s.id, sessionID, s.status)
		if insertErr != nil {
			t.Fatalf("Failed to insert story: %v", insertErr)
		}
	}

	result, err := GetMostRecentResumableSession(db)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.IncompleteStories != 3 {
		t.Errorf("Expected 3 incomplete stories, got %d", result.IncompleteStories)
	}
	if result.DoneStories != 2 {
		t.Errorf("Expected 2 done stories, got %d", result.DoneStories)
	}
}

func TestGetMostRecentResumableSession_IgnoresCompletedSessions(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Create completed session (should be ignored)
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, started_at, ended_at)
		VALUES (?, ?, ?, ?)
	`, "session-completed", SessionStatusCompleted, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to insert completed session: %v", err)
	}

	result, err := GetMostRecentResumableSession(db)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result != nil {
		t.Errorf("Expected nil result for completed session, got: %+v", result)
	}
}

func TestResetInFlightStories(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sessionID := "session-reset"

	// Create session
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, started_at, ended_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, SessionStatusCrashed, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	// Add stories with various statuses including 'dispatched'
	stories := []struct {
		id     string
		status string
		agent  string
	}{
		{"story-new", "new", ""},
		{"story-dispatched", "dispatched", ""},        // dispatched but not yet picked up
		{"story-planning", "planning", "coder-001"},   // picked up, planning
		{"story-coding", "coding", "coder-002"},       // implementing
		{"story-review", "review", "coder-001"},       // submitted for review
		{"story-done", "done", "coder-001"},           // completed
		{"story-in-progress", "in_progress", "coder"}, // legacy status
	}

	for _, s := range stories {
		_, insertErr := db.Exec(`
			INSERT INTO stories (id, session_id, spec_id, title, content, status, assigned_agent, started_at)
			VALUES (?, ?, 'spec-1', 'Title', 'Content', ?, ?, ?)
		`, s.id, sessionID, s.status, s.agent, time.Now())
		if insertErr != nil {
			t.Fatalf("Failed to insert story %s: %v", s.id, insertErr)
		}
	}

	// Reset in-flight stories
	count, resetErr := ResetInFlightStories(db, sessionID)
	if resetErr != nil {
		t.Fatalf("ResetInFlightStories failed: %v", resetErr)
	}

	// Should reset dispatched, planning, coding, review, in_progress (5 stories)
	if count != 5 {
		t.Errorf("Expected 5 stories reset, got %d", count)
	}

	// Verify statuses
	verifyStatus := func(storyID, expectedStatus string) {
		var status string
		queryErr := db.QueryRow(`SELECT status FROM stories WHERE id = ? AND session_id = ?`, storyID, sessionID).Scan(&status)
		if queryErr != nil {
			t.Fatalf("Failed to query story %s: %v", storyID, queryErr)
		}
		if status != expectedStatus {
			t.Errorf("Story %s: expected status '%s', got '%s'", storyID, expectedStatus, status)
		}
	}

	verifyStatus("story-new", "new")         // unchanged
	verifyStatus("story-dispatched", "new")  // reset
	verifyStatus("story-planning", "new")    // reset
	verifyStatus("story-coding", "new")      // reset
	verifyStatus("story-review", "new")      // reset
	verifyStatus("story-done", "done")       // unchanged (completed)
	verifyStatus("story-in-progress", "new") // reset (legacy status)

	// Verify assigned agents cleared
	var assignedAgent sql.NullString
	agentQueryErr := db.QueryRow(`SELECT assigned_agent FROM stories WHERE id = ? AND session_id = ?`, "story-planning", sessionID).Scan(&assignedAgent)
	if agentQueryErr != nil {
		t.Fatalf("Failed to query assigned_agent: %v", agentQueryErr)
	}
	if assignedAgent.Valid && assignedAgent.String != "" {
		t.Errorf("Expected assigned_agent to be cleared, got '%s'", assignedAgent.String)
	}
}

func TestGetAllStoriesForSession(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sessionID := "session-all"

	// Create session
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, started_at, ended_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, SessionStatusShutdown, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	// Add stories with various statuses
	stories := []struct {
		id       string
		status   string
		priority int
	}{
		{"story-new", "new", 1},
		{"story-planning", "planning", 2},
		{"story-done", "done", 3},
		{"story-failed", "failed", 4},
		{"story-coding", "coding", 5},
	}

	for _, s := range stories {
		_, insertErr := db.Exec(`
			INSERT INTO stories (id, session_id, spec_id, title, content, status, priority, story_type)
			VALUES (?, ?, 'spec-1', 'Title', 'Content', ?, ?, 'app')
		`, s.id, sessionID, s.status, s.priority)
		if insertErr != nil {
			t.Fatalf("Failed to insert story %s: %v", s.id, insertErr)
		}
	}

	// Get all stories
	result, err := GetAllStoriesForSession(db, sessionID)
	if err != nil {
		t.Fatalf("GetAllStoriesForSession failed: %v", err)
	}

	// Should return ALL 5 stories including done and failed
	if len(result) != 5 {
		t.Errorf("Expected 5 stories, got %d", len(result))
	}

	// Verify all statuses are present
	statusCount := make(map[string]int)
	for _, story := range result {
		statusCount[story.Status]++
	}
	if statusCount["new"] != 1 {
		t.Errorf("Expected 1 'new' story, got %d", statusCount["new"])
	}
	if statusCount["done"] != 1 {
		t.Errorf("Expected 1 'done' story, got %d", statusCount["done"])
	}
	if statusCount["failed"] != 1 {
		t.Errorf("Expected 1 'failed' story, got %d", statusCount["failed"])
	}
}

func TestGetAllStoriesForSession_WithDependencies(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sessionID := "session-deps"

	// Create session
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, started_at)
		VALUES (?, ?, ?)
	`, sessionID, SessionStatusShutdown, time.Now())
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	// Create stories with dependencies
	stories := []struct {
		id     string
		status string
	}{
		{"story-done-1", "done"},
		{"story-done-2", "done"},
		{"story-incomplete-1", "new"},
	}

	for _, s := range stories {
		_, insertErr := db.Exec(`
			INSERT INTO stories (id, session_id, spec_id, title, content, status, story_type)
			VALUES (?, ?, 'spec-1', 'Title', 'Content', ?, 'app')
		`, s.id, sessionID, s.status)
		if insertErr != nil {
			t.Fatalf("Failed to insert story %s: %v", s.id, insertErr)
		}
	}

	// Create dependencies: incomplete-1 depends on done-1 and done-2
	deps := []struct {
		storyID   string
		dependsOn string
	}{
		{"story-incomplete-1", "story-done-1"},
		{"story-incomplete-1", "story-done-2"},
	}

	for _, d := range deps {
		_, depErr := db.Exec(`
			INSERT INTO story_dependencies (story_id, depends_on)
			VALUES (?, ?)
		`, d.storyID, d.dependsOn)
		if depErr != nil {
			t.Fatalf("Failed to insert dependency: %v", depErr)
		}
	}

	// Get all stories
	result, err := GetAllStoriesForSession(db, sessionID)
	if err != nil {
		t.Fatalf("GetAllStoriesForSession failed: %v", err)
	}

	// Should return all 3 stories
	if len(result) != 3 {
		t.Errorf("Expected 3 stories, got %d", len(result))
	}

	// Find incomplete story and verify dependencies are populated
	for _, story := range result {
		if story.ID == "story-incomplete-1" {
			if len(story.DependsOn) != 2 {
				t.Errorf("Expected 2 dependencies for story-incomplete-1, got %d", len(story.DependsOn))
			}
		}
	}
}

func TestGetAllStoriesForSession_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Get stories for non-existent session
	result, err := GetAllStoriesForSession(db, "non-existent")
	if err != nil {
		t.Fatalf("GetAllStoriesForSession failed: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("Expected 0 stories for non-existent session, got %d", len(result))
	}
}
