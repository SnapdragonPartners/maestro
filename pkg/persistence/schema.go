// Package persistence provides SQLite-based storage for specs and stories.
package persistence

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // SQLite driver

	"orchestrator/pkg/proto"
)

// CurrentSchemaVersion defines the current schema version for migration support.
const CurrentSchemaVersion = 12

// InitializeDatabase creates and initializes the SQLite database with the required schema.
// This function is idempotent and safe to call multiple times.
func InitializeDatabase(dbPath string) (*sql.DB, error) {
	// Open database connection
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_foreign_keys=ON&_journal_mode=WAL", dbPath))
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

	// Initialize schema with migrations
	if err := initializeSchemaWithMigrations(db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			// Log close error but return the original error
			_ = closeErr
		}
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// initializeSchemaWithMigrations ensures the database schema is at the current version.
func initializeSchemaWithMigrations(db *sql.DB) error {
	// Get current schema version
	currentVersion, err := GetSchemaVersion(db)
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}

	// If database is empty (version 0), create fresh schema
	if currentVersion == 0 {
		return createSchema(db)
	}

	// If database is up-to-date, no migration needed
	if currentVersion == CurrentSchemaVersion {
		return nil
	}

	// Run migrations from current version to target version
	return runMigrations(db, currentVersion, CurrentSchemaVersion)
}

// runMigrations applies database migrations from current version to target version.
func runMigrations(db *sql.DB, fromVersion, toVersion int) error {
	for version := fromVersion + 1; version <= toVersion; version++ {
		if err := runMigration(db, version); err != nil {
			return fmt.Errorf("migration to version %d failed: %w", version, err)
		}

		// Update schema version after successful migration
		if err := setSchemaVersion(db, version); err != nil {
			return fmt.Errorf("failed to update schema version to %d: %w", version, err)
		}
	}
	return nil
}

// runMigration applies a specific version migration.
func runMigration(db *sql.DB, version int) error {
	switch version {
	case 1:
		return migrateToVersion1(db)
	case 2:
		return migrateToVersion2(db)
	case 3:
		return migrateToVersion3(db)
	case 4:
		return migrateToVersion4(db)
	case 5:
		return migrateToVersion5(db)
	case 6:
		return migrateToVersion6(db)
	case 7:
		return migrateToVersion7(db)
	case 8:
		return migrateToVersion8(db)
	case 9:
		return migrateToVersion9(db)
	case 10:
		return migrateToVersion10(db)
	case 11:
		return migrateToVersion11(db)
	case 12:
		return migrateToVersion12(db)
	default:
		return fmt.Errorf("unknown migration version: %d", version)
	}
}

// migrateToVersion6 adds the new completion fields to the stories table.
func migrateToVersion6(db *sql.DB) error {
	migrations := []string{
		"ALTER TABLE stories ADD COLUMN pr_id TEXT",
		"ALTER TABLE stories ADD COLUMN commit_hash TEXT",
		"ALTER TABLE stories ADD COLUMN completion_summary TEXT",
	}

	for _, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			return fmt.Errorf("failed to execute migration: %s: %w", migration, err)
		}
	}

	return nil
}

// Placeholder migrations for future versions 1-5 (these would be implemented when needed).
func migrateToVersion1(_ *sql.DB) error { return nil }
func migrateToVersion2(_ *sql.DB) error { return nil }
func migrateToVersion3(_ *sql.DB) error { return nil }
func migrateToVersion4(_ *sql.DB) error { return nil }
func migrateToVersion5(_ *sql.DB) error { return nil }

// migrateToVersion7 adds session_id to all tables for session isolation.
// This enables filtering all reads and writes by orchestrator session.
func migrateToVersion7(db *sql.DB) error {
	migrations := []string{
		// Add session_id to all main tables
		"ALTER TABLE specs ADD COLUMN session_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE stories ADD COLUMN session_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_states ADD COLUMN session_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_requests ADD COLUMN session_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_responses ADD COLUMN session_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_plans ADD COLUMN session_id TEXT NOT NULL DEFAULT ''",

		// Add indices for efficient session filtering
		"CREATE INDEX IF NOT EXISTS idx_specs_session ON specs(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_stories_session ON stories(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_states_session ON agent_states(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_requests_session ON agent_requests(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_responses_session ON agent_responses(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_plans_session ON agent_plans(session_id)",
	}

	for _, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			return fmt.Errorf("failed to execute migration: %s: %w", migration, err)
		}
	}

	return nil
}

// migrateToVersion8 adds tool_executions table for debugging tool call history.
func migrateToVersion8(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tool_executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			story_id TEXT,
			tool_name TEXT NOT NULL,
			tool_id TEXT,
			params TEXT,
			exit_code INTEGER,
			success INTEGER CHECK (success IN (0, 1)),
			stdout TEXT,
			stderr TEXT,
			error TEXT,
			duration_ms INTEGER,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create tool_executions table: %w", err)
	}

	// Add indices for common queries
	indices := []string{
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_session ON tool_executions(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_agent ON tool_executions(agent_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_story ON tool_executions(story_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_tool ON tool_executions(tool_name)",
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_created ON tool_executions(created_at)",
	}

	for _, idx := range indices {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("failed to create index: %s: %w", idx, err)
		}
	}

	return nil
}

// migrateToVersion9 adds reply_to and post_type fields to chat table for escalation support.
func migrateToVersion9(db *sql.DB) error {
	migrations := []string{
		// Add reply_to field for message threading
		"ALTER TABLE chat ADD COLUMN reply_to INTEGER REFERENCES chat(id)",
		// Add post_type field for escalation tracking
		"ALTER TABLE chat ADD COLUMN post_type TEXT NOT NULL DEFAULT 'chat' CHECK (post_type IN ('chat', 'reply', 'escalate'))",
		// Add indices for efficient escalation queries
		"CREATE INDEX IF NOT EXISTS idx_chat_reply_to ON chat(reply_to)",
		"CREATE INDEX IF NOT EXISTS idx_chat_post_type ON chat(post_type)",
	}

	for _, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			return fmt.Errorf("failed to execute migration: %s: %w", migration, err)
		}
	}

	return nil
}

// migrateToVersion10 adds knowledge graph tables for storing architectural patterns and design decisions.
func migrateToVersion10(db *sql.DB) error {
	tables := []string{
		// Node index
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			type TEXT NOT NULL CHECK (type IN ('component','interface','abstraction','datastore','external','pattern','rule')),
			level TEXT NOT NULL CHECK (level IN ('architecture','implementation')),
			status TEXT NOT NULL CHECK (status IN ('current','deprecated','future','legacy')),
			description TEXT NOT NULL,
			tag TEXT,
			component TEXT,
			path TEXT,
			example TEXT,
			priority TEXT CHECK (priority IN ('critical','high','medium','low')),
			raw_dot TEXT NOT NULL
		)`,

		// Edge index
		`CREATE TABLE IF NOT EXISTS edges (
			from_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			relation TEXT NOT NULL CHECK (relation IN ('calls','uses','implements','configured_with','must_follow','must_not_use','superseded_by','supersedes','coexists_with')),
			note TEXT,
			PRIMARY KEY (from_id, to_id, relation)
		)`,

		// Cached knowledge packs (story-specific subgraphs)
		`CREATE TABLE IF NOT EXISTS knowledge_packs (
			story_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			subgraph TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_used TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			node_count INTEGER,
			search_terms TEXT
		)`,

		// Knowledge graph metadata (file modification tracking)
		`CREATE TABLE IF NOT EXISTS knowledge_metadata (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			session_id TEXT NOT NULL,
			dot_file_mtime INTEGER NOT NULL,
			last_indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Full-text search index
		`CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
			id, tag, description, path, example,
			content=nodes
		)`,
	}

	// Create tables
	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("failed to create knowledge table: %w", err)
		}
	}

	// FTS sync triggers (keep FTS table in sync with nodes table)
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS nodes_fts_insert AFTER INSERT ON nodes BEGIN
			INSERT INTO nodes_fts(rowid, id, tag, description, path, example)
			VALUES (new.rowid, new.id, new.tag, new.description, new.path, new.example);
		END`,

		`CREATE TRIGGER IF NOT EXISTS nodes_fts_update AFTER UPDATE ON nodes BEGIN
			UPDATE nodes_fts SET
				id = new.id,
				tag = new.tag,
				description = new.description,
				path = new.path,
				example = new.example
			WHERE rowid = new.rowid;
		END`,

		`CREATE TRIGGER IF NOT EXISTS nodes_fts_delete AFTER DELETE ON nodes BEGIN
			DELETE FROM nodes_fts WHERE rowid = old.rowid;
		END`,
	}

	// Create triggers
	for _, trigger := range triggers {
		if _, err := db.Exec(trigger); err != nil {
			return fmt.Errorf("failed to create FTS trigger: %w", err)
		}
	}

	// Performance indices
	indices := []string{
		"CREATE INDEX IF NOT EXISTS idx_nodes_session ON nodes(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_nodes_type ON nodes(type)",
		"CREATE INDEX IF NOT EXISTS idx_nodes_level ON nodes(level)",
		"CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status)",
		"CREATE INDEX IF NOT EXISTS idx_nodes_component ON nodes(component)",
		"CREATE INDEX IF NOT EXISTS idx_edges_session ON edges(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_id)",
		"CREATE INDEX IF NOT EXISTS idx_edges_to ON edges(to_id)",
		"CREATE INDEX IF NOT EXISTS idx_packs_session ON knowledge_packs(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_packs_last_used ON knowledge_packs(last_used)",
	}

	// Create indices
	for _, idx := range indices {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("failed to create knowledge index: %w", err)
		}
	}

	return nil
}

// migrateToVersion11 adds PM conversation and message tables for interview tracking.
func migrateToVersion11(db *sql.DB) error {
	tables := []string{
		// PM conversations table for interview state tracking
		`CREATE TABLE IF NOT EXISTS pm_conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL UNIQUE,
			user_expertise TEXT CHECK(user_expertise IN ('NON_TECHNICAL', 'BASIC', 'EXPERT')),
			repo_url TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			completed INTEGER DEFAULT 0,
			spec_id TEXT REFERENCES specs(id)
		)`,

		// PM messages table for conversation history
		`CREATE TABLE IF NOT EXISTS pm_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_session_id TEXT NOT NULL,
			role TEXT CHECK(role IN ('user', 'pm')) NOT NULL,
			content TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			FOREIGN KEY(conversation_session_id) REFERENCES pm_conversations(session_id) ON DELETE CASCADE
		)`,
	}

	// Create tables
	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("failed to create PM table: %w", err)
		}
	}

	// Create indices
	indices := []string{
		"CREATE INDEX IF NOT EXISTS idx_pm_conversations_session ON pm_conversations(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_pm_messages_conversation ON pm_messages(conversation_session_id, timestamp)",
	}

	for _, idx := range indices {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("failed to create PM index: %w", err)
		}
	}

	return nil
}

// migrateToVersion12 adds multi-channel support to chat system.
// This migration:
// 1. Adds channel column to chat table (defaults to 'development').
// 2. Recreates chat_cursor with composite primary key (agent_id, channel, session_id).
// 3. Migrates existing cursors to 'development' channel with current session_id.
func migrateToVersion12(db *sql.DB) error {
	// Get current session_id from config (we'll need to read it)
	// For migration purposes, we'll query the most recent message's session_id
	var sessionID string
	err := db.QueryRow("SELECT session_id FROM chat ORDER BY id DESC LIMIT 1").Scan(&sessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to get session_id: %w", err)
	}
	// If no messages exist, use empty string (will be populated on first use)
	if errors.Is(err, sql.ErrNoRows) {
		sessionID = ""
	}

	migrations := []string{
		// Step 1: Add channel column to chat table
		"ALTER TABLE chat ADD COLUMN channel TEXT NOT NULL DEFAULT 'development'",

		// Step 2: Create new index for channel-based queries
		"CREATE INDEX IF NOT EXISTS idx_chat_channel_session ON chat(channel, session_id, id)",

		// Step 3: Create new chat_cursor table with composite key
		`CREATE TABLE chat_cursor_new (
			agent_id TEXT NOT NULL,
			channel TEXT NOT NULL,
			session_id TEXT NOT NULL,
			last_id INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (agent_id, channel, session_id)
		)`,
	}

	for _, migration := range migrations {
		if _, execErr := db.Exec(migration); execErr != nil {
			return fmt.Errorf("migration failed (%s): %w", migration, execErr)
		}
	}

	// Step 4: Migrate existing cursors to new table with 'development' channel
	if sessionID != "" {
		_, err = db.Exec(`
			INSERT INTO chat_cursor_new (agent_id, channel, session_id, last_id)
			SELECT agent_id, 'development', ?, last_id
			FROM chat_cursor
		`, sessionID)
		if err != nil {
			return fmt.Errorf("failed to migrate cursors: %w", err)
		}
	}

	// Step 5: Drop old cursor table and rename new one
	if _, err := db.Exec("DROP TABLE chat_cursor"); err != nil {
		return fmt.Errorf("failed to drop old cursor table: %w", err)
	}
	if _, err := db.Exec("ALTER TABLE chat_cursor_new RENAME TO chat_cursor"); err != nil {
		return fmt.Errorf("failed to rename cursor table: %w", err)
	}

	return nil
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
			session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			processed_at DATETIME
		)`,

		// Stories table
		`CREATE TABLE IF NOT EXISTS stories (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			spec_id TEXT REFERENCES specs(id),
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			status TEXT DEFAULT 'new' CHECK (status IN ('new','pending','dispatched','planning','coding','done')),
			priority INTEGER DEFAULT 0,
			approved_plan TEXT,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			started_at DATETIME,
			completed_at DATETIME,
			assigned_agent TEXT,
			tokens_used BIGINT DEFAULT 0,
			cost_usd DECIMAL(10,4) DEFAULT 0.0,
			metadata TEXT,
			story_type TEXT DEFAULT 'app' CHECK (story_type IN ('devops', 'app')),
			pr_id TEXT,
			commit_hash TEXT,
			completion_summary TEXT
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
			session_id TEXT NOT NULL,
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
			session_id TEXT NOT NULL,
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
			session_id TEXT NOT NULL,
			request_id TEXT REFERENCES agent_requests(id),
			story_id TEXT REFERENCES stories(id),
			response_type TEXT NOT NULL CHECK (response_type IN ('answer', 'result')),
			from_agent TEXT NOT NULL,
			to_agent TEXT NOT NULL,
			content TEXT NOT NULL,
			status TEXT CHECK (status IN ('APPROVED', 'REJECTED', 'NEEDS_CHANGES', 'PENDING')),
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			correlation_id TEXT
		)`,

		// Agent plans table (extracted from stories.approved_plan)
		`CREATE TABLE IF NOT EXISTS agent_plans (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			story_id TEXT NOT NULL REFERENCES stories(id),
			from_agent TEXT NOT NULL,
			content TEXT NOT NULL,
			confidence TEXT CHECK (confidence IN ('` + string(proto.ConfidenceHigh) + `', '` + string(proto.ConfidenceMedium) + `', '` + string(proto.ConfidenceLow) + `')),
			status TEXT DEFAULT 'submitted' CHECK (status IN ('submitted', 'approved', 'rejected', 'needs_changes')),
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			reviewed_at DATETIME,
			reviewed_by TEXT,
			feedback TEXT
		)`,

		// Chat messages table for agent collaboration (multi-channel support)
		`CREATE TABLE IF NOT EXISTS chat (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			channel TEXT NOT NULL DEFAULT 'development',
			ts TEXT NOT NULL,
			author TEXT NOT NULL,
			text TEXT NOT NULL,
			reply_to INTEGER REFERENCES chat(id),
			post_type TEXT NOT NULL DEFAULT 'chat' CHECK (post_type IN ('chat', 'reply', 'escalate'))
		)`,

		// Chat cursor table for tracking agent read positions (per-channel, per-session)
		`CREATE TABLE IF NOT EXISTS chat_cursor (
			agent_id TEXT NOT NULL,
			channel TEXT NOT NULL,
			session_id TEXT NOT NULL,
			last_id INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (agent_id, channel, session_id)
		)`,

		// Tool executions table for debugging and analysis
		`CREATE TABLE IF NOT EXISTS tool_executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			story_id TEXT,
			tool_name TEXT NOT NULL,
			tool_id TEXT,
			params TEXT,
			exit_code INTEGER,
			success INTEGER CHECK (success IN (0, 1)),
			stdout TEXT,
			stderr TEXT,
			error TEXT,
			duration_ms INTEGER,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,

		// PM conversations table for interview state tracking
		`CREATE TABLE IF NOT EXISTS pm_conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL UNIQUE,
			user_expertise TEXT CHECK(user_expertise IN ('NON_TECHNICAL', 'BASIC', 'EXPERT')),
			repo_url TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			completed INTEGER DEFAULT 0,
			spec_id TEXT REFERENCES specs(id)
		)`,

		// PM messages table for conversation history
		`CREATE TABLE IF NOT EXISTS pm_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_session_id TEXT NOT NULL,
			role TEXT CHECK(role IN ('user', 'pm')) NOT NULL,
			content TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			FOREIGN KEY(conversation_session_id) REFERENCES pm_conversations(session_id) ON DELETE CASCADE
		)`,

		// Knowledge graph nodes
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			type TEXT NOT NULL CHECK (type IN ('component','interface','abstraction','datastore','external','pattern','rule')),
			level TEXT NOT NULL CHECK (level IN ('architecture','implementation')),
			status TEXT NOT NULL CHECK (status IN ('current','deprecated','future','legacy')),
			description TEXT NOT NULL,
			tag TEXT,
			component TEXT,
			path TEXT,
			example TEXT,
			priority TEXT CHECK (priority IN ('critical','high','medium','low')),
			raw_dot TEXT NOT NULL
		)`,

		// Knowledge graph edges
		`CREATE TABLE IF NOT EXISTS edges (
			from_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			relation TEXT NOT NULL CHECK (relation IN ('calls','uses','implements','configured_with','must_follow','must_not_use','superseded_by','supersedes','coexists_with')),
			note TEXT,
			PRIMARY KEY (from_id, to_id, relation)
		)`,

		// Cached knowledge packs (story-specific subgraphs)
		`CREATE TABLE IF NOT EXISTS knowledge_packs (
			story_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			subgraph TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_used TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			node_count INTEGER,
			search_terms TEXT
		)`,

		// Knowledge graph metadata (file modification tracking)
		`CREATE TABLE IF NOT EXISTS knowledge_metadata (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			session_id TEXT NOT NULL,
			dot_file_mtime INTEGER NOT NULL,
			last_indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Full-text search index for nodes
		`CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
			id, tag, description, path, example,
			content=nodes
		)`,
	}

	// Create indices
	indices := []string{
		// Session isolation indices (critical for performance)
		"CREATE INDEX IF NOT EXISTS idx_specs_session ON specs(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_stories_session ON stories(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_states_session ON agent_states(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_requests_session ON agent_requests(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_responses_session ON agent_responses(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_plans_session ON agent_plans(session_id)",

		// Existing functional indices
		"CREATE INDEX IF NOT EXISTS idx_stories_status ON stories(status)",
		"CREATE INDEX IF NOT EXISTS idx_stories_agent ON stories(assigned_agent)",
		"CREATE INDEX IF NOT EXISTS idx_stories_type ON stories(story_type)",
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

		// Chat indices
		"CREATE INDEX IF NOT EXISTS idx_chat_session ON chat(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_chat_session_id ON chat(session_id, id)",
		"CREATE INDEX IF NOT EXISTS idx_chat_channel_session ON chat(channel, session_id, id)",
		"CREATE INDEX IF NOT EXISTS idx_chat_reply_to ON chat(reply_to)",
		"CREATE INDEX IF NOT EXISTS idx_chat_post_type ON chat(post_type)",

		// Tool execution indices
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_session ON tool_executions(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_agent ON tool_executions(agent_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_story ON tool_executions(story_id)",
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_tool ON tool_executions(tool_name)",
		"CREATE INDEX IF NOT EXISTS idx_tool_exec_created ON tool_executions(created_at)",

		// PM conversation indices
		"CREATE INDEX IF NOT EXISTS idx_pm_conversations_session ON pm_conversations(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_pm_messages_conversation ON pm_messages(conversation_session_id, timestamp)",

		// Knowledge graph indices
		"CREATE INDEX IF NOT EXISTS idx_nodes_session ON nodes(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_nodes_type ON nodes(type)",
		"CREATE INDEX IF NOT EXISTS idx_nodes_level ON nodes(level)",
		"CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status)",
		"CREATE INDEX IF NOT EXISTS idx_nodes_component ON nodes(component)",
		"CREATE INDEX IF NOT EXISTS idx_edges_session ON edges(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_id)",
		"CREATE INDEX IF NOT EXISTS idx_edges_to ON edges(to_id)",
		"CREATE INDEX IF NOT EXISTS idx_packs_session ON knowledge_packs(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_packs_last_used ON knowledge_packs(last_used)",
	}

	// Execute table creation
	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}

	// Create FTS triggers for knowledge graph (sync nodes table with FTS index)
	ftsTriggers := []string{
		`CREATE TRIGGER IF NOT EXISTS nodes_fts_insert AFTER INSERT ON nodes BEGIN
			INSERT INTO nodes_fts(rowid, id, tag, description, path, example)
			VALUES (new.rowid, new.id, new.tag, new.description, new.path, new.example);
		END`,

		`CREATE TRIGGER IF NOT EXISTS nodes_fts_update AFTER UPDATE ON nodes BEGIN
			UPDATE nodes_fts SET
				id = new.id,
				tag = new.tag,
				description = new.description,
				path = new.path,
				example = new.example
			WHERE rowid = new.rowid;
		END`,

		`CREATE TRIGGER IF NOT EXISTS nodes_fts_delete AFTER DELETE ON nodes BEGIN
			DELETE FROM nodes_fts WHERE rowid = old.rowid;
		END`,
	}

	for _, trigger := range ftsTriggers {
		if _, err := db.Exec(trigger); err != nil {
			return fmt.Errorf("failed to create FTS trigger: %w", err)
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
	// First ensure the schema_version table exists
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`)
	if err != nil {
		return 0, fmt.Errorf("failed to create schema_version table: %w", err)
	}

	var version int
	err = db.QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil // No version set yet
	}
	if err != nil {
		return 0, fmt.Errorf("schema version scan error: %w", err)
	}
	return version, nil
}
