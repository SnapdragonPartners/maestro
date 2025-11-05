package knowledge

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite" // SQLite driver
)

// setupTestDB creates a temporary in-memory database for testing.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Create schema (version 10 tables) - matches production schema
	schema := `
	CREATE TABLE IF NOT EXISTS nodes (
		id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		type TEXT NOT NULL,
		level TEXT NOT NULL,
		status TEXT NOT NULL,
		description TEXT NOT NULL,
		tag TEXT,
		component TEXT,
		path TEXT,
		example TEXT,
		priority TEXT,
		raw_dot TEXT,
		indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (session_id, id)
	);

	CREATE TABLE IF NOT EXISTS edges (
		session_id TEXT NOT NULL,
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		relation TEXT,
		note TEXT,
		indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (session_id, from_id, to_id)
	);

	CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
		id UNINDEXED,
		session_id UNINDEXED,
		type,
		description,
		tag,
		component,
		example,
		content=nodes,
		content_rowid=rowid
	);

	CREATE TABLE IF NOT EXISTS knowledge_metadata (
		session_id TEXT NOT NULL,
		graph_path TEXT NOT NULL,
		last_mtime INTEGER NOT NULL,
		last_indexed TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (session_id, graph_path)
	);

	CREATE TRIGGER IF NOT EXISTS nodes_fts_insert AFTER INSERT ON nodes BEGIN
		INSERT INTO nodes_fts(rowid, id, session_id, type, description, tag, component, example)
		VALUES (new.rowid, new.id, new.session_id, new.type, new.description, new.tag, new.component, new.example);
	END;

	CREATE TRIGGER IF NOT EXISTS nodes_fts_update AFTER UPDATE ON nodes BEGIN
		UPDATE nodes_fts SET
			type = new.type,
			description = new.description,
			tag = new.tag,
			component = new.component,
			example = new.example
		WHERE rowid = new.rowid;
	END;

	CREATE TRIGGER IF NOT EXISTS nodes_fts_delete AFTER DELETE ON nodes BEGIN
		DELETE FROM nodes_fts WHERE rowid = old.rowid;
	END;
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("Failed to create test schema: %v", err)
	}

	return db
}

// TestBuildIndex tests creating index and inserting nodes/edges.
func TestBuildIndex(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	graph := &Graph{
		Nodes: map[string]*Node{
			"test-node": {
				ID:          "test-node",
				Type:        "rule",
				Level:       "architecture",
				Status:      "current",
				Description: "Test rule for indexing",
				Priority:    "high",
			},
			"test-pattern": {
				ID:          "test-pattern",
				Type:        "pattern",
				Level:       "implementation",
				Status:      "current",
				Description: "Test pattern",
				Tag:         "testing",
			},
		},
		Edges: []*Edge{
			{FromID: "test-node", ToID: "test-pattern", Relation: "influences"},
		},
	}

	sessionID := "test-session"
	err := IndexGraph(db, graph, sessionID)
	if err != nil {
		t.Fatalf("IndexGraph() error = %v", err)
	}

	// Verify nodes were inserted
	var nodeCount int
	err = db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", sessionID).Scan(&nodeCount)
	if err != nil {
		t.Fatalf("Failed to query node count: %v", err)
	}
	if nodeCount != 2 {
		t.Errorf("Node count = %d, want 2", nodeCount)
	}

	// Verify edges were inserted
	var edgeCount int
	err = db.QueryRow("SELECT COUNT(*) FROM edges WHERE session_id = ?", sessionID).Scan(&edgeCount)
	if err != nil {
		t.Fatalf("Failed to query edge count: %v", err)
	}
	if edgeCount != 1 {
		t.Errorf("Edge count = %d, want 1", edgeCount)
	}

	// Verify specific node data
	var description string
	err = db.QueryRow("SELECT description FROM nodes WHERE session_id = ? AND id = ?", sessionID, "test-node").Scan(&description)
	if err != nil {
		t.Fatalf("Failed to query node description: %v", err)
	}
	if description != "Test rule for indexing" {
		t.Errorf("Node description = %q, want %q", description, "Test rule for indexing")
	}
}

// TestDetectModification tests file modification detection.
func TestDetectModification(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a temporary file
	tmpDir := t.TempDir()
	dotPath := filepath.Join(tmpDir, "knowledge.dot")

	// Write initial content
	initialContent := `digraph Knowledge {
		"test" [type="rule" level="architecture" status="current" description="Test"];
	}`
	if err := os.WriteFile(dotPath, []byte(initialContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	sessionID := "test-session"

	// First check - should be modified (no metadata yet)
	modified, err := IsGraphModified(db, dotPath)
	if err != nil {
		t.Fatalf("IsGraphModified() error = %v", err)
	}
	if !modified {
		t.Error("IsGraphModified() = false on first check, want true")
	}

	// Update metadata to mark as indexed
	if updateErr := UpdateMetadata(db, dotPath, sessionID); updateErr != nil {
		t.Fatalf("UpdateMetadata() error = %v", updateErr)
	}

	// Second check - should not be modified
	modified, err = IsGraphModified(db, dotPath)
	if err != nil {
		t.Fatalf("IsGraphModified() error = %v", err)
	}
	if modified {
		t.Error("IsGraphModified() = true after update, want false")
	}

	// Modify the file
	time.Sleep(1100 * time.Millisecond) // Ensure mtime changes (must be > 1 second for Unix timestamps)
	modifiedContent := `digraph Knowledge {
		"test" [type="rule" level="architecture" status="current" description="Updated"];
	}`
	if writeErr := os.WriteFile(dotPath, []byte(modifiedContent), 0644); writeErr != nil {
		t.Fatalf("Failed to modify test file: %v", writeErr)
	}

	// Third check - should be modified again
	modified, err = IsGraphModified(db, dotPath)
	if err != nil {
		t.Fatalf("IsGraphModified() error = %v", err)
	}
	if !modified {
		t.Error("IsGraphModified() = false after file modification, want true")
	}
}

// TestFTSSearch tests full-text search functionality.
func TestFTSSearch(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Index test data
	graph := &Graph{
		Nodes: map[string]*Node{
			"api-standards": {
				ID:          "api-standards",
				Type:        "rule",
				Level:       "architecture",
				Status:      "current",
				Description: "Follow RESTful API standards for all endpoints",
				Priority:    "high",
			},
			"error-handling": {
				ID:          "error-handling",
				Type:        "pattern",
				Level:       "implementation",
				Status:      "current",
				Description: "Use structured error handling with context",
				Tag:         "errors",
			},
			"database-pattern": {
				ID:          "database-pattern",
				Type:        "pattern",
				Level:       "implementation",
				Status:      "current",
				Description: "Use connection pooling for database access",
				Tag:         "database",
			},
		},
		Edges: []*Edge{},
	}

	sessionID := "test-session"
	if err := IndexGraph(db, graph, sessionID); err != nil {
		t.Fatalf("IndexGraph() error = %v", err)
	}

	tests := []struct {
		name       string
		searchTerm string
		wantCount  int
		wantNodes  []string
	}{
		{
			name:       "search by description keyword",
			searchTerm: "API",
			wantCount:  1,
			wantNodes:  []string{"api-standards"},
		},
		{
			name:       "search by tag",
			searchTerm: "errors",
			wantCount:  1,
			wantNodes:  []string{"error-handling"},
		},
		{
			name:       "search multiple matches",
			searchTerm: "pattern",
			wantCount:  2,
			wantNodes:  []string{"error-handling", "database-pattern"},
		},
		{
			name:       "search no matches",
			searchTerm: "nonexistent",
			wantCount:  0,
			wantNodes:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use FTS query
			query := `
				SELECT id FROM nodes_fts
				WHERE nodes_fts MATCH ? AND session_id = ?
			`
			rows, err := db.Query(query, tt.searchTerm, sessionID)
			if err != nil {
				t.Fatalf("FTS query error: %v", err)
			}
			defer rows.Close()

			var foundNodes []string
			for rows.Next() {
				var nodeID string
				if err := rows.Scan(&nodeID); err != nil {
					t.Fatalf("Scan error: %v", err)
				}
				foundNodes = append(foundNodes, nodeID)
			}

			if len(foundNodes) != tt.wantCount {
				t.Errorf("FTS search %q found %d nodes, want %d", tt.searchTerm, len(foundNodes), tt.wantCount)
			}

			for _, wantNode := range tt.wantNodes {
				found := false
				for _, foundNode := range foundNodes {
					if foundNode == wantNode {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("FTS search %q missing expected node %q", tt.searchTerm, wantNode)
				}
			}
		})
	}
}

// TestRebuildIndex tests clearing and rebuilding the index.
func TestRebuildIndex(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	sessionID := "test-session"

	// Index initial data
	graph1 := &Graph{
		Nodes: map[string]*Node{
			"node-1": {
				ID:          "node-1",
				Type:        "rule",
				Level:       "architecture",
				Status:      "current",
				Description: "First node",
			},
		},
		Edges: []*Edge{},
	}

	if err := IndexGraph(db, graph1, sessionID); err != nil {
		t.Fatalf("Initial IndexGraph() error = %v", err)
	}

	// Verify initial count
	var count1 int
	db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", sessionID).Scan(&count1)
	if count1 != 1 {
		t.Errorf("Initial node count = %d, want 1", count1)
	}

	// Create a temporary file with new content
	tmpDir := t.TempDir()
	dotPath := filepath.Join(tmpDir, "knowledge.dot")
	newContent := `digraph Knowledge {
		"node-2" [type="pattern" level="implementation" status="current" description="Second node"];
		"node-3" [type="pattern" level="implementation" status="current" description="Third node"];
	}`
	if err := os.WriteFile(dotPath, []byte(newContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Rebuild index from file
	if err := RebuildIndex(db, dotPath, sessionID); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}

	// Verify new count (should have replaced old data)
	var count2 int
	db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", sessionID).Scan(&count2)
	if count2 != 2 {
		t.Errorf("After rebuild node count = %d, want 2", count2)
	}

	// Verify old node is gone
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes WHERE session_id = ? AND id = ?)", sessionID, "node-1").Scan(&exists)
	if err != nil {
		t.Fatalf("Failed to check node existence: %v", err)
	}
	if exists {
		t.Error("Old node still exists after rebuild")
	}

	// Verify new nodes exist
	for _, nodeID := range []string{"node-2", "node-3"} {
		err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes WHERE session_id = ? AND id = ?)", sessionID, nodeID).Scan(&exists)
		if err != nil {
			t.Fatalf("Failed to check node existence: %v", err)
		}
		if !exists {
			t.Errorf("New node %q does not exist after rebuild", nodeID)
		}
	}
}

// TestSessionIsolation tests that sessions are properly isolated.
func TestSessionIsolation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Index same graph in two different sessions
	graph := &Graph{
		Nodes: map[string]*Node{
			"shared-node": {
				ID:          "shared-node",
				Type:        "rule",
				Level:       "architecture",
				Status:      "current",
				Description: "Shared node",
			},
		},
		Edges: []*Edge{},
	}

	session1 := "session-1"
	session2 := "session-2"

	if err := IndexGraph(db, graph, session1); err != nil {
		t.Fatalf("IndexGraph() session1 error = %v", err)
	}

	if err := IndexGraph(db, graph, session2); err != nil {
		t.Fatalf("IndexGraph() session2 error = %v", err)
	}

	// Verify both sessions have the node
	for _, sid := range []string{session1, session2} {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ? AND id = ?", sid, "shared-node").Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query session %s: %v", sid, err)
		}
		if count != 1 {
			t.Errorf("Session %s node count = %d, want 1", sid, count)
		}
	}

	// Delete from session1
	_, err := db.Exec("DELETE FROM nodes WHERE session_id = ? AND id = ?", session1, "shared-node")
	if err != nil {
		t.Fatalf("Failed to delete from session1: %v", err)
	}

	// Verify session1 has no nodes, session2 still has the node
	var count1, count2 int
	db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", session1).Scan(&count1)
	db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", session2).Scan(&count2)

	if count1 != 0 {
		t.Errorf("Session1 count after delete = %d, want 0", count1)
	}
	if count2 != 1 {
		t.Errorf("Session2 count after session1 delete = %d, want 1", count2)
	}
}
