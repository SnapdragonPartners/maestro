package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // SQLite driver
)

// TestEndToEndPipeline tests the complete flow: parse → validate → index → retrieve.
func TestEndToEndPipeline(t *testing.T) {
	// Create in-memory database
	db := setupTestDB(t)
	defer db.Close()

	// Create a temporary DOT file
	tmpDir := t.TempDir()
	dotPath := filepath.Join(tmpDir, "knowledge.dot")

	dotContent := `digraph ProjectKnowledge {
		"api-standards" [type="rule" level="architecture" status="current" description="Follow RESTful API design principles" priority="critical" tag="api"];
		"error-handling" [type="pattern" level="implementation" status="current" description="Use structured error handling with context wrapping" tag="errors" example="return fmt.Errorf(\"context: %w\", err)"];
		"database-pool" [type="pattern" level="implementation" status="current" description="Use connection pooling for database operations" tag="database" component="persistence"];
		"api-standards" -> "error-handling";
		"error-handling" -> "database-pool";
	}`

	if err := os.WriteFile(dotPath, []byte(dotContent), 0644); err != nil {
		t.Fatalf("Failed to write DOT file: %v", err)
	}

	sessionID := "integration-test-session"

	// Step 1: Parse DOT file
	graph, err := ParseDOT(dotContent)
	if err != nil {
		t.Fatalf("ParseDOT() error = %v", err)
	}

	if len(graph.Nodes) != 3 {
		t.Errorf("ParseDOT() parsed %d nodes, want 3, got nodes: %v", len(graph.Nodes), func() []string {
			ids := make([]string, 0, len(graph.Nodes))
			for id := range graph.Nodes {
				ids = append(ids, id)
			}
			return ids
		}())
	}

	if len(graph.Edges) != 2 {
		t.Errorf("ParseDOT() parsed %d edges, want 2", len(graph.Edges))
	}

	// Step 2: Validate graph
	errors := ValidateGraph(graph)
	if len(errors) > 0 {
		t.Fatalf("ValidateGraph() found errors: %+v", errors)
	}

	// Step 3: Index graph
	if indexErr := IndexGraph(db, graph, sessionID); indexErr != nil {
		t.Fatalf("IndexGraph() error = %v", indexErr)
	}

	// Verify indexing
	var nodeCount int
	err = db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", sessionID).Scan(&nodeCount)
	if err != nil {
		t.Fatalf("Failed to query node count: %v", err)
	}
	if nodeCount != 3 {
		t.Errorf("Indexed %d nodes, want 3", nodeCount)
	}

	// Step 4: Retrieve with search
	options := RetrievalOptions{
		Terms:      "API error handling",
		Level:      "all",
		MaxResults: 10,
		Depth:      1,
	}

	result, err := Retrieve(db, sessionID, options)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	if result.Count < 2 {
		t.Errorf("Retrieve() returned %d nodes, want at least 2", result.Count)
	}

	// Parse result subgraph
	resultGraph, err := ParseDOT(result.Subgraph)
	if err != nil {
		t.Fatalf("Failed to parse result subgraph: %v", err)
	}

	// Should find api-standards and error-handling (both match search terms)
	if _, exists := resultGraph.Nodes["api-standards"]; !exists {
		t.Error("Result missing 'api-standards' node")
	}

	if _, exists := resultGraph.Nodes["error-handling"]; !exists {
		t.Error("Result missing 'error-handling' node")
	}

	// Step 5: Test RebuildIndex (full cycle)
	if rebuildErr := RebuildIndex(db, dotPath, sessionID); rebuildErr != nil {
		t.Fatalf("RebuildIndex() error = %v", rebuildErr)
	}

	// Verify rebuild
	var countAfterRebuild int
	db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", sessionID).Scan(&countAfterRebuild)
	if countAfterRebuild != 3 {
		t.Errorf("After rebuild, have %d nodes, want 3", countAfterRebuild)
	}

	// Step 6: Verify metadata was updated
	modified, err := IsGraphModified(db, dotPath)
	if err != nil {
		t.Fatalf("IsGraphModified() error = %v", err)
	}
	if modified {
		t.Error("IsGraphModified() = true after rebuild, want false")
	}
}

// TestErrorHandlingNilDatabase tests that functions handle nil database gracefully.
func TestErrorHandlingNilDatabase(t *testing.T) {
	tests := []struct {
		name string
		fn   func() error
	}{
		{
			name: "IndexGraph with nil db",
			fn: func() error {
				graph := &Graph{Nodes: map[string]*Node{}, Edges: []*Edge{}}
				return IndexGraph(nil, graph, "test-session")
			},
		},
		{
			name: "RebuildIndex with nil db",
			fn: func() error {
				return RebuildIndex(nil, "/tmp/test.dot", "test-session")
			},
		},
		{
			name: "IsGraphModified with nil db",
			fn: func() error {
				_, err := IsGraphModified(nil, "/tmp/test.dot")
				return err
			},
		},
		{
			name: "UpdateMetadata with nil db",
			fn: func() error {
				return UpdateMetadata(nil, "/tmp/test.dot", "test-session")
			},
		},
		{
			name: "Retrieve with nil db",
			fn: func() error {
				_, err := Retrieve(nil, "test-session", RetrievalOptions{})
				return err
			},
		},
		{
			name: "StorePack with nil db",
			fn: func() error {
				return StorePack(nil, "story-1", "session-1", "graph", "terms", 1)
			},
		},
		{
			name: "GetCachedPack with nil db",
			fn: func() error {
				_, err := GetCachedPack(nil, "story-1", "session-1")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Errorf("%s: expected error for nil database, got nil", tt.name)
			}
		})
	}
}

// TestErrorHandlingInvalidInputs tests that functions validate their inputs.
func TestErrorHandlingInvalidInputs(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	t.Run("IndexGraph with nil graph", func(t *testing.T) {
		err := IndexGraph(db, nil, "test-session")
		if err == nil {
			t.Error("IndexGraph() with nil graph should return error")
		}
	})

	t.Run("IndexGraph with empty session_id", func(t *testing.T) {
		graph := &Graph{Nodes: map[string]*Node{}, Edges: []*Edge{}}
		err := IndexGraph(db, graph, "")
		if err == nil {
			t.Error("IndexGraph() with empty session_id should return error")
		}
	})

	t.Run("RebuildIndex with empty path", func(t *testing.T) {
		err := RebuildIndex(db, "", "test-session")
		if err == nil {
			t.Error("RebuildIndex() with empty path should return error")
		}
	})

	t.Run("RebuildIndex with empty session_id", func(t *testing.T) {
		err := RebuildIndex(db, "/tmp/test.dot", "")
		if err == nil {
			t.Error("RebuildIndex() with empty session_id should return error")
		}
	})

	t.Run("IsGraphModified with empty path", func(t *testing.T) {
		_, err := IsGraphModified(db, "")
		if err == nil {
			t.Error("IsGraphModified() with empty path should return error")
		}
	})

	t.Run("UpdateMetadata with empty path", func(t *testing.T) {
		err := UpdateMetadata(db, "", "test-session")
		if err == nil {
			t.Error("UpdateMetadata() with empty path should return error")
		}
	})

	t.Run("UpdateMetadata with empty session_id", func(t *testing.T) {
		err := UpdateMetadata(db, "/tmp/test.dot", "")
		if err == nil {
			t.Error("UpdateMetadata() with empty session_id should return error")
		}
	})

	t.Run("Retrieve with empty session_id", func(t *testing.T) {
		_, err := Retrieve(db, "", RetrievalOptions{Terms: "test"})
		if err == nil {
			t.Error("Retrieve() with empty session_id should return error")
		}
	})
}

// TestErrorHandlingFileOperations tests error handling for file I/O operations.
func TestErrorHandlingFileOperations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	t.Run("RebuildIndex with non-existent file", func(t *testing.T) {
		err := RebuildIndex(db, "/nonexistent/path/file.dot", "test-session")
		if err == nil {
			t.Error("RebuildIndex() with non-existent file should return error")
		}
	})

	t.Run("IsGraphModified with non-existent file", func(t *testing.T) {
		modified, err := IsGraphModified(db, "/nonexistent/path/file.dot")
		if err != nil {
			// This is actually expected - file doesn't exist
			// Function returns (false, nil) for non-existent files
			return
		}
		if modified {
			t.Error("IsGraphModified() with non-existent file returned modified=true")
		}
	})

	t.Run("UpdateMetadata with non-existent file", func(t *testing.T) {
		err := UpdateMetadata(db, "/nonexistent/path/file.dot", "test-session")
		if err == nil {
			t.Error("UpdateMetadata() with non-existent file should return error")
		}
	})

	t.Run("RebuildIndex with invalid DOT content", func(t *testing.T) {
		tmpDir := t.TempDir()
		dotPath := filepath.Join(tmpDir, "invalid.dot")

		if err := os.WriteFile(dotPath, []byte("not valid dot syntax"), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		err := RebuildIndex(db, dotPath, "test-session")
		if err == nil {
			t.Error("RebuildIndex() with invalid DOT should return error")
		}
	})

	t.Run("RebuildIndex with validation failure", func(t *testing.T) {
		tmpDir := t.TempDir()
		dotPath := filepath.Join(tmpDir, "invalid-graph.dot")

		// Valid DOT syntax but invalid graph (missing required fields)
		invalidGraph := `digraph Knowledge {
			"test-node" [type="rule"];
		}`

		if err := os.WriteFile(dotPath, []byte(invalidGraph), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		err := RebuildIndex(db, dotPath, "test-session")
		if err == nil {
			t.Error("RebuildIndex() with invalid graph should return validation error")
		}
	})
}

// TestConcurrentIndexing tests behavior with concurrent indexing operations.
func TestConcurrentIndexing(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	graph1 := &Graph{
		Nodes: map[string]*Node{
			"node-1": {
				ID:          "node-1",
				Type:        "pattern",
				Level:       "implementation",
				Status:      "current",
				Description: "First node",
			},
		},
		Edges: []*Edge{},
	}

	graph2 := &Graph{
		Nodes: map[string]*Node{
			"node-2": {
				ID:          "node-2",
				Type:        "pattern",
				Level:       "implementation",
				Status:      "current",
				Description: "Second node",
			},
		},
		Edges: []*Edge{},
	}

	// Index to different sessions sequentially (simulating concurrent sessions)
	err1 := IndexGraph(db, graph1, "session-1")
	err2 := IndexGraph(db, graph2, "session-2")

	if err1 != nil {
		t.Errorf("IndexGraph() session-1 error = %v", err1)
	}
	if err2 != nil {
		t.Errorf("IndexGraph() session-2 error = %v", err2)
	}

	// Verify isolation
	var count1, count2 int
	db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", "session-1").Scan(&count1)
	db.QueryRow("SELECT COUNT(*) FROM nodes WHERE session_id = ?", "session-2").Scan(&count2)

	if count1 != 1 || count2 != 1 {
		t.Errorf("Session isolation failed: session-1=%d, session-2=%d, want 1 each", count1, count2)
	}
}

// TestRetrieveWithNullFields tests retrieval handles NULL optional fields correctly.
func TestRetrieveWithNullFields(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	sessionID := "test-session"

	// Insert a node with NULL optional fields directly
	_, err := db.Exec(`
		INSERT INTO nodes (id, session_id, type, level, status, description, tag, component, path, example, priority, raw_dot)
		VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, NULL, NULL, NULL, NULL)
	`, "minimal-node", sessionID, "pattern", "implementation", "current", "Minimal node")

	if err != nil {
		t.Fatalf("Failed to insert minimal node: %v", err)
	}

	// Manually insert into FTS since we bypassed triggers
	_, err = db.Exec(`
		INSERT INTO nodes_fts (rowid, id, session_id, type, description, tag, component, example)
		SELECT rowid, id, session_id, type, description, tag, component, example
		FROM nodes WHERE id = ?
	`, "minimal-node")

	if err != nil {
		t.Fatalf("Failed to insert into FTS: %v", err)
	}

	// Try to retrieve - should handle NULL fields gracefully
	options := RetrievalOptions{
		Terms:      "minimal",
		Level:      "all",
		MaxResults: 10,
		Depth:      0,
	}

	result, err := Retrieve(db, sessionID, options)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	// Should return 1 result with NULL fields handled
	if result.Count != 1 {
		t.Errorf("Retrieve() with NULL fields returned %d nodes, want 1", result.Count)
	}

	// Parse result and verify node has empty strings for NULL fields
	graph, parseErr := ParseDOT(result.Subgraph)
	if parseErr != nil {
		t.Fatalf("Failed to parse result: %v", parseErr)
	}

	node, exists := graph.Nodes["minimal-node"]
	if !exists {
		t.Fatal("Result missing minimal-node")
	}

	// Verify NULL fields are empty strings
	if node.Tag != "" {
		t.Errorf("Expected empty tag, got %q", node.Tag)
	}
	if node.Component != "" {
		t.Errorf("Expected empty component, got %q", node.Component)
	}
}

// TestValidateGraphEdgeCases tests validation with edge cases.
func TestValidateGraphEdgeCases(t *testing.T) {
	t.Run("nil graph", func(t *testing.T) {
		errors := ValidateGraph(nil)
		if len(errors) == 0 {
			t.Error("ValidateGraph(nil) should return error")
		}
	})

	t.Run("graph with circular edge references", func(t *testing.T) {
		graph := &Graph{
			Nodes: map[string]*Node{
				"node-a": {
					ID:          "node-a",
					Type:        "pattern",
					Level:       "implementation",
					Status:      "current",
					Description: "Node A",
				},
				"node-b": {
					ID:          "node-b",
					Type:        "pattern",
					Level:       "implementation",
					Status:      "current",
					Description: "Node B",
				},
			},
			Edges: []*Edge{
				{FromID: "node-a", ToID: "node-b", Relation: "uses"},
				{FromID: "node-b", ToID: "node-a", Relation: "uses"},
			},
		}

		errors := ValidateGraph(graph)
		// Circular references are allowed in the graph
		if len(errors) > 0 {
			t.Errorf("ValidateGraph() with circular edges returned errors: %+v", errors)
		}
	})

	t.Run("graph with self-referencing edge", func(t *testing.T) {
		graph := &Graph{
			Nodes: map[string]*Node{
				"node-a": {
					ID:          "node-a",
					Type:        "pattern",
					Level:       "implementation",
					Status:      "current",
					Description: "Node A",
				},
			},
			Edges: []*Edge{
				{FromID: "node-a", ToID: "node-a", Relation: "uses"},
			},
		}

		errors := ValidateGraph(graph)
		// Self-references are allowed
		if len(errors) > 0 {
			t.Errorf("ValidateGraph() with self-reference returned errors: %+v", errors)
		}
	})
}
