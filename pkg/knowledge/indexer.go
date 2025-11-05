package knowledge

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// IndexGraph inserts or updates graph nodes and edges in the database.
// Uses the provided session_id for all inserts to support session isolation.
func IndexGraph(db *sql.DB, graph *Graph, sessionID string) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	if graph == nil {
		return fmt.Errorf("graph is nil")
	}
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is safe to call after commit

	// Insert nodes
	nodeStmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO nodes (
			id, session_id, type, level, status, description,
			tag, component, path, example, priority, raw_dot
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare node statement: %w", err)
	}
	defer nodeStmt.Close() //nolint:errcheck // Close in defer is safe

	for _, node := range graph.Nodes {
		_, execErr := nodeStmt.Exec(
			node.ID,
			sessionID,
			node.Type,
			node.Level,
			node.Status,
			node.Description,
			node.Tag,
			node.Component,
			node.Path,
			node.Example,
			node.Priority,
			node.RawDOT,
		)
		if execErr != nil {
			return fmt.Errorf("failed to insert node %s: %w", node.ID, execErr)
		}
	}

	// Insert edges
	edgeStmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO edges (
			from_id, to_id, session_id, relation, note
		) VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare edge statement: %w", err)
	}
	defer edgeStmt.Close() //nolint:errcheck // Close in defer is safe

	for _, edge := range graph.Edges {
		_, execErr := edgeStmt.Exec(
			edge.FromID,
			edge.ToID,
			sessionID,
			edge.Relation,
			edge.Note,
		)
		if execErr != nil {
			return fmt.Errorf("failed to insert edge %s->%s: %w", edge.FromID, edge.ToID, execErr)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// RebuildIndex clears existing knowledge tables and rebuilds from DOT file.
// This is a full rebuild - use carefully as it clears all existing data for the session.
func RebuildIndex(db *sql.DB, dotPath, sessionID string) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	if dotPath == "" {
		return fmt.Errorf("dotPath is required")
	}
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}

	// Read DOT file
	content, err := os.ReadFile(dotPath)
	if err != nil {
		return fmt.Errorf("failed to read DOT file: %w", err)
	}

	// Parse graph
	graph, err := ParseDOT(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse DOT: %w", err)
	}

	// Validate graph
	if validationErr := ValidateAndReport(graph); validationErr != nil {
		return fmt.Errorf("graph validation failed: %w", validationErr)
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is safe after commit

	// Clear existing data for this session
	if _, err := tx.Exec("DELETE FROM nodes WHERE session_id = ?", sessionID); err != nil {
		return fmt.Errorf("failed to clear nodes: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM edges WHERE session_id = ?", sessionID); err != nil {
		return fmt.Errorf("failed to clear edges: %w", err)
	}

	// Commit transaction (clears are done)
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit clear: %w", err)
	}

	// Index the new graph
	if err := IndexGraph(db, graph, sessionID); err != nil {
		return fmt.Errorf("failed to index graph: %w", err)
	}

	// Update metadata
	if err := UpdateMetadata(db, dotPath, sessionID); err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}

	return nil
}

// IsGraphModified checks if the DOT file has been modified since the last index.
// Returns true if modified or if no metadata exists.
func IsGraphModified(db *sql.DB, dotPath string) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("database connection is nil")
	}
	if dotPath == "" {
		return false, fmt.Errorf("dotPath is required")
	}

	// Get current file mtime
	stat, err := os.Stat(dotPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - not modified (will be created)
			return false, nil
		}
		return false, fmt.Errorf("failed to stat file: %w", err)
	}

	currentMtime := stat.ModTime().Unix()

	// Get stored mtime from database
	var storedMtime int64
	err = db.QueryRow("SELECT dot_file_mtime FROM knowledge_metadata WHERE id = 1").Scan(&storedMtime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No metadata exists - treat as modified
			return true, nil
		}
		return false, fmt.Errorf("failed to query metadata: %w", err)
	}

	// Compare mtimes
	return currentMtime != storedMtime, nil
}

// UpdateMetadata stores the current file mtime and index timestamp.
func UpdateMetadata(db *sql.DB, dotPath, sessionID string) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	if dotPath == "" {
		return fmt.Errorf("dotPath is required")
	}
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}

	// Get current file mtime
	stat, err := os.Stat(dotPath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	mtime := stat.ModTime().Unix()

	// Insert or update metadata (singleton table with id=1)
	_, err = db.Exec(`
		INSERT OR REPLACE INTO knowledge_metadata (
			id, session_id, dot_file_mtime, last_indexed_at
		) VALUES (1, ?, ?, ?)
	`, sessionID, mtime, time.Now())

	if err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}

	return nil
}
