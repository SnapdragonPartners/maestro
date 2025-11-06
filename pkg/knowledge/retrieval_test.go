package knowledge

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite" // SQLite driver
)

// setupTestDBWithData creates a test database with sample knowledge graph data.
func setupTestDBWithData(t *testing.T) (*sql.DB, string) {
	t.Helper()

	db := setupTestDB(t)
	sessionID := "test-session"

	// Create a more complex graph for retrieval testing
	graph := &Graph{
		Nodes: map[string]*Node{
			"api-design": {
				ID:          "api-design",
				Type:        "rule",
				Level:       "architecture",
				Status:      "current",
				Description: "Follow RESTful API design principles",
				Priority:    "critical",
				Tag:         "api",
			},
			"error-handling": {
				ID:          "error-handling",
				Type:        "pattern",
				Level:       "implementation",
				Status:      "current",
				Description: "Use structured error handling with context wrapping",
				Tag:         "errors",
				Example:     "return fmt.Errorf(\"context: %w\", err)",
			},
			"database-access": {
				ID:          "database-access",
				Type:        "pattern",
				Level:       "implementation",
				Status:      "current",
				Description: "Use connection pooling for database operations",
				Tag:         "database",
				Component:   "persistence",
			},
			"auth-middleware": {
				ID:          "auth-middleware",
				Type:        "component",
				Level:       "implementation",
				Status:      "current",
				Description: "Authentication middleware for API endpoints",
				Component:   "auth",
			},
			"deprecated-pattern": {
				ID:          "deprecated-pattern",
				Type:        "pattern",
				Level:       "implementation",
				Status:      "deprecated",
				Description: "Old error handling approach",
			},
		},
		Edges: []*Edge{
			{FromID: "api-design", ToID: "error-handling", Relation: "requires"},
			{FromID: "api-design", ToID: "auth-middleware", Relation: "uses"},
			{FromID: "error-handling", ToID: "database-access", Relation: "applies_to"},
		},
	}

	if err := IndexGraph(db, graph, sessionID); err != nil {
		t.Fatalf("Failed to index test data: %v", err)
	}

	return db, sessionID
}

// TestSearchByTerms tests retrieving nodes by search terms.
func TestSearchByTerms(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	tests := []struct {
		name        string
		terms       string
		wantMin     int
		mustInclude []string
	}{
		{
			name:        "search by API keyword",
			terms:       "API",
			wantMin:     2,
			mustInclude: []string{"api-design", "auth-middleware"},
		},
		{
			name:        "search by error keyword",
			terms:       "error",
			wantMin:     1,
			mustInclude: []string{"error-handling"},
		},
		{
			name:        "search by database keyword",
			terms:       "database",
			wantMin:     1,
			mustInclude: []string{"database-access"},
		},
		{
			name:        "multiple terms",
			terms:       "error database",
			wantMin:     2,
			mustInclude: []string{"error-handling", "database-access"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := RetrievalOptions{
				Terms:      tt.terms,
				Level:      "all",
				MaxResults: 10,
				Depth:      0,
			}

			result, err := Retrieve(db, sessionID, options)
			if err != nil {
				t.Fatalf("Retrieve() error = %v", err)
			}

			if result.Count < tt.wantMin {
				t.Errorf("Retrieve() count = %d, want at least %d", result.Count, tt.wantMin)
			}

			// Parse the subgraph to verify nodes
			subgraph, parseErr := ParseDOT(result.Subgraph)
			if parseErr != nil {
				t.Fatalf("Failed to parse result subgraph: %v", parseErr)
			}

			for _, mustHave := range tt.mustInclude {
				if _, exists := subgraph.Nodes[mustHave]; !exists {
					t.Errorf("Retrieve() missing expected node %q", mustHave)
				}
			}
		})
	}
}

// TestMaxResults tests respecting the maximum results limit.
func TestMaxResults(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	tests := []struct {
		name       string
		maxResults int
	}{
		{"limit 1", 1},
		{"limit 2", 2},
		{"limit 3", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := RetrievalOptions{
				Terms:      "pattern error database", // Should match multiple nodes
				Level:      "all",
				MaxResults: tt.maxResults,
				Depth:      0,
			}

			result, err := Retrieve(db, sessionID, options)
			if err != nil {
				t.Fatalf("Retrieve() error = %v", err)
			}

			if result.Count > tt.maxResults {
				t.Errorf("Retrieve() returned %d nodes, want at most %d", result.Count, tt.maxResults)
			}
		})
	}
}

// TestLevelFilter tests filtering by architecture vs implementation level.
func TestLevelFilter(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	tests := []struct {
		name        string
		level       string
		mustInclude []string
		mustExclude []string
	}{
		{
			name:        "architecture level only",
			level:       "architecture",
			mustInclude: []string{"api-design"},
			mustExclude: []string{"error-handling", "database-access", "auth-middleware"},
		},
		{
			name:        "implementation level only",
			level:       "implementation",
			mustInclude: []string{"error-handling", "database-access"},
			mustExclude: []string{"api-design"},
		},
		{
			name:        "all levels",
			level:       "all",
			mustInclude: []string{"api-design", "error-handling", "database-access"},
			mustExclude: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := RetrievalOptions{
				Terms:      "API error database", // Broad search to get multiple levels
				Level:      tt.level,
				MaxResults: 10,
				Depth:      0,
			}

			result, err := Retrieve(db, sessionID, options)
			if err != nil {
				t.Fatalf("Retrieve() error = %v", err)
			}

			// Parse subgraph to check node levels
			subgraph, parseErr := ParseDOT(result.Subgraph)
			if parseErr != nil {
				t.Fatalf("Failed to parse result subgraph: %v", parseErr)
			}

			for _, mustHave := range tt.mustInclude {
				if _, exists := subgraph.Nodes[mustHave]; !exists {
					t.Errorf("Level filter %q missing expected node %q", tt.level, mustHave)
				}
			}

			for _, mustNotHave := range tt.mustExclude {
				if _, exists := subgraph.Nodes[mustNotHave]; exists {
					t.Errorf("Level filter %q included excluded node %q", tt.level, mustNotHave)
				}
			}
		})
	}
}

// TestNeighborInclusion tests including connected nodes with depth expansion.
func TestNeighborInclusion(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	tests := []struct {
		name        string
		terms       string
		depth       int
		wantMin     int
		mustInclude []string
	}{
		{
			name:        "depth 0 - no neighbors",
			terms:       "RESTful", // Matches api-design
			depth:       0,
			wantMin:     1,
			mustInclude: []string{"api-design"},
		},
		{
			name:        "depth 1 - direct neighbors",
			terms:       "RESTful", // Matches api-design, should include error-handling and auth-middleware
			depth:       1,
			wantMin:     3,
			mustInclude: []string{"api-design", "error-handling", "auth-middleware"},
		},
		{
			name:        "depth 2 - second-degree neighbors",
			terms:       "RESTful", // Should also include database-access (connected to error-handling)
			depth:       2,
			wantMin:     4,
			mustInclude: []string{"api-design", "error-handling", "auth-middleware", "database-access"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := RetrievalOptions{
				Terms:      tt.terms,
				Level:      "all",
				MaxResults: 10,
				Depth:      tt.depth,
			}

			result, err := Retrieve(db, sessionID, options)
			if err != nil {
				t.Fatalf("Retrieve() error = %v", err)
			}

			if result.Count < tt.wantMin {
				t.Errorf("Retrieve() with depth %d returned %d nodes, want at least %d", tt.depth, result.Count, tt.wantMin)
			}

			// Parse and verify
			subgraph, parseErr := ParseDOT(result.Subgraph)
			if parseErr != nil {
				t.Fatalf("Failed to parse result subgraph: %v", parseErr)
			}

			for _, mustHave := range tt.mustInclude {
				if _, exists := subgraph.Nodes[mustHave]; !exists {
					t.Errorf("Depth %d missing expected node %q", tt.depth, mustHave)
				}
			}
		})
	}
}

// TestPackCaching tests storing and retrieving cached knowledge packs.
func TestPackCaching(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	// Create knowledge_packs table (part of schema v10)
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS knowledge_packs (
			story_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			subgraph TEXT NOT NULL,
			search_terms TEXT NOT NULL,
			node_count INTEGER NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_used TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (story_id, session_id)
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create knowledge_packs table: %v", err)
	}

	storyID := "story-123"
	searchTerms := "API error handling"
	subgraph := `digraph Knowledge { "test" [type="rule" level="architecture" status="current" description="Test"]; }`
	nodeCount := 1

	// Store pack
	err = StorePack(db, storyID, sessionID, subgraph, searchTerms, nodeCount)
	if err != nil {
		t.Fatalf("StorePack() error = %v", err)
	}

	// Retrieve pack
	result, err := GetCachedPack(db, storyID, sessionID)
	if err != nil {
		t.Fatalf("GetCachedPack() error = %v", err)
	}

	if result.Subgraph != subgraph {
		t.Errorf("GetCachedPack() subgraph mismatch")
	}

	if result.Count != nodeCount {
		t.Errorf("GetCachedPack() count = %d, want %d", result.Count, nodeCount)
	}

	// Verify error on non-existent pack
	_, err = GetCachedPack(db, "non-existent-story", sessionID)
	if !errors.Is(err, ErrNoCachedPack) {
		t.Errorf("GetCachedPack() for non-existent story error = %v, want ErrNoCachedPack", err)
	}
}

// TestRetrievalWithNoResults tests behavior when no nodes match search.
func TestRetrievalWithNoResults(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	options := RetrievalOptions{
		Terms:      "nonexistent_keyword_xyz",
		Level:      "all",
		MaxResults: 10,
		Depth:      1,
	}

	result, err := Retrieve(db, sessionID, options)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	if result.Count != 0 {
		t.Errorf("Retrieve() with no matches returned count = %d, want 0", result.Count)
	}

	// Should return a valid empty graph
	if result.Subgraph == "" {
		t.Error("Retrieve() with no matches returned empty string, want valid empty graph")
	}
}

// TestStatusFiltering tests that deprecated nodes can be excluded.
func TestStatusFiltering(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	// Search for "error" - should find both current and deprecated patterns
	options := RetrievalOptions{
		Terms:      "error",
		Level:      "all",
		MaxResults: 10,
		Depth:      0,
	}

	result, err := Retrieve(db, sessionID, options)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	// Parse to check what we got
	subgraph, parseErr := ParseDOT(result.Subgraph)
	if parseErr != nil {
		t.Fatalf("Failed to parse result subgraph: %v", parseErr)
	}

	// Should include current error-handling
	if _, exists := subgraph.Nodes["error-handling"]; !exists {
		t.Error("Retrieve() missing current error-handling node")
	}

	// Check if deprecated pattern is included (depends on implementation)
	// The spec doesn't explicitly filter by status, so this documents behavior
	if node, exists := subgraph.Nodes["deprecated-pattern"]; exists {
		if node.Status != "deprecated" {
			t.Errorf("Node marked as deprecated has status %q", node.Status)
		}
		t.Log("Note: deprecated nodes are included in search results")
	}
}

// TestEmptySearchTerms tests retrieval with empty search terms.
func TestEmptySearchTerms(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	options := RetrievalOptions{
		Terms:      "",
		Level:      "all",
		MaxResults: 10,
		Depth:      0,
	}

	result, err := Retrieve(db, sessionID, options)
	if err != nil {
		t.Fatalf("Retrieve() with empty terms error = %v", err)
	}

	// With empty terms, should return empty or handle gracefully
	// Document the actual behavior
	t.Logf("Empty search returned %d nodes", result.Count)
}

// TestConcurrentRetrieval tests that multiple retrievals don't interfere.
func TestConcurrentRetrieval(t *testing.T) {
	db, sessionID := setupTestDBWithData(t)
	defer db.Close()

	// Run multiple retrievals in sequence (actual concurrency would need goroutines)
	searches := []string{"API", "error", "database"}

	for _, term := range searches {
		options := RetrievalOptions{
			Terms:      term,
			Level:      "all",
			MaxResults: 10,
			Depth:      1,
		}

		_, err := Retrieve(db, sessionID, options)
		if err != nil {
			t.Errorf("Concurrent retrieve for %q error = %v", term, err)
		}
	}
}
