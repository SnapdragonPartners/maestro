// Package persistence provides SQLite-based storage for specs and stories.
package persistence

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

// CurrentSchemaVersion defines the current schema version for migration support.
const CurrentSchemaVersion = 3

// InitializeDatabase creates and initializes the SQLite database with the required schema.
// This function is idempotent and safe to call multiple times.
func InitializeDatabase(dbPath string) (*sql.DB, error) {
	// Open database connection
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=ON&_journal_mode=WAL", dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection with a simple ping
	if err := db.Ping(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			// Log close error but return the original error
			_ = closeErr
		}
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Initialize schema
	if err := createSchema(db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			// Log close error but return the original error
			_ = closeErr
		}
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	return db, nil
}

// createSchema creates all required tables and indices.
func createSchema(db *sql.DB) error {
	// Enable WAL mode and foreign keys
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("failed to execute pragma %s: %w", pragma, err)
		}
	}

	// Create tables
	tables := []string{
		// Schema version tracking
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,

		// Specifications table
		`CREATE TABLE IF NOT EXISTS specs (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			processed_at DATETIME
		)`,

		// Stories table
		`CREATE TABLE IF NOT EXISTS stories (
			id TEXT PRIMARY KEY,
			spec_id TEXT REFERENCES specs(id),
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			status TEXT DEFAULT 'new' CHECK (status IN ('new','planning','coding','committed','merged','error','duplicate')),
			priority INTEGER DEFAULT 0,
			approved_plan TEXT,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			started_at DATETIME,
			completed_at DATETIME,
			assigned_agent TEXT,
			tokens_used BIGINT DEFAULT 0,
			cost_usd DECIMAL(10,4) DEFAULT 0.0,
			metadata TEXT
		)`,

		// Story dependencies junction table
		`CREATE TABLE IF NOT EXISTS story_dependencies (
			story_id TEXT REFERENCES stories(id) ON DELETE CASCADE,
			depends_on TEXT REFERENCES stories(id) ON DELETE CASCADE,
			PRIMARY KEY (story_id, depends_on),
			CHECK (story_id <> depends_on)
		)`,

		// Agent state persistence for system-level resume functionality
		`CREATE TABLE IF NOT EXISTS agent_states (
			agent_id TEXT PRIMARY KEY,
			agent_type TEXT NOT NULL CHECK (agent_type IN ('architect','coder')),
			current_state TEXT NOT NULL,
			state_data TEXT,
			story_id TEXT REFERENCES stories(id),
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,

		// Agent requests table (unified questions and approval requests)
		`CREATE TABLE IF NOT EXISTS agent_requests (
			id TEXT PRIMARY KEY,
			story_id TEXT REFERENCES stories(id),
			request_type TEXT NOT NULL CHECK (request_type IN ('question', 'approval')),
			approval_type TEXT CHECK (approval_type IN ('plan', 'code', 'budget_review', 'completion')),
			from_agent TEXT NOT NULL,
			to_agent TEXT NOT NULL,
			content TEXT NOT NULL,
			context TEXT,
			reason TEXT,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			correlation_id TEXT,
			parent_msg_id TEXT
		)`,

		// Agent responses table (unified answers and approval results)
		`CREATE TABLE IF NOT EXISTS agent_responses (
			id TEXT PRIMARY KEY,
			request_id TEXT REFERENCES agent_requests(id),
			story_id TEXT REFERENCES stories(id),
			response_type TEXT NOT NULL CHECK (response_type IN ('answer', 'result')),
			from_agent TEXT NOT NULL,
			to_agent TEXT NOT NULL,
			content TEXT NOT NULL,
			status TEXT CHECK (status IN ('APPROVED', 'REJECTED', 'NEEDS_CHANGES', 'PENDING')),
			feedback TEXT,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			correlation_id TEXT
		)`,

		// Agent plans table (extracted from stories.approved_plan)
		`CREATE TABLE IF NOT EXISTS agent_plans (
			id TEXT PRIMARY KEY,
			story_id TEXT NOT NULL REFERENCES stories(id),
			from_agent TEXT NOT NULL,
			content TEXT NOT NULL,
			confidence TEXT CHECK (confidence IN ('high', 'medium', 'low')),
			status TEXT DEFAULT 'submitted' CHECK (status IN ('submitted', 'approved', 'rejected', 'needs_changes')),
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			reviewed_at DATETIME,
			reviewed_by TEXT,
			feedback TEXT
		)`,
	}

	// Create indices
	indices := []string{
		"CREATE INDEX IF NOT EXISTS idx_stories_status ON stories(status)",
		"CREATE INDEX IF NOT EXISTS idx_stories_agent ON stories(assigned_agent)",
		"CREATE INDEX IF NOT EXISTS idx_depends_on ON story_dependencies(depends_on)",
		"CREATE INDEX IF NOT EXISTS idx_stories_spec ON stories(spec_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_states_type ON agent_states(agent_type)",
		"CREATE INDEX IF NOT EXISTS idx_agent_states_story ON agent_states(story_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_requests_story ON agent_requests(story_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_requests_type ON agent_requests(request_type)",
		"CREATE INDEX IF NOT EXISTS idx_agent_requests_correlation ON agent_requests(correlation_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_responses_request ON agent_responses(request_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_responses_story ON agent_responses(story_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_responses_correlation ON agent_responses(correlation_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_plans_story ON agent_plans(story_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_plans_status ON agent_plans(status)",
	}

	// Execute table creation
	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}

	// Execute index creation
	for _, ddl := range indices {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// Set schema version
	if err := setSchemaVersion(db, CurrentSchemaVersion); err != nil {
		return fmt.Errorf("failed to set schema version: %w", err)
	}

	return nil
}

// setSchemaVersion records the current schema version.
func setSchemaVersion(db *sql.DB, version int) error {
	_, err := db.Exec(`
		INSERT OR REPLACE INTO schema_version (version) VALUES (?)
	`, version)
	if err != nil {
		return fmt.Errorf("database exec error: %w", err)
	}
	return nil
}

// GetSchemaVersion returns the current schema version from the database.
func GetSchemaVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&version)
	if err == sql.ErrNoRows {
		return 0, nil // No version set yet
	}
	if err != nil {
		return 0, fmt.Errorf("schema version scan error: %w", err)
	}
	return version, nil
}
