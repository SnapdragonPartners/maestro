package knowledge

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNoCachedPack is returned when no cached pack exists for a story.
var ErrNoCachedPack = errors.New("no cached knowledge pack found")

// RetrievalOptions configures knowledge graph retrieval.
type RetrievalOptions struct {
	Terms      string // Search terms (space-separated)
	Level      string // Filter by level: "architecture", "implementation", or "all"
	MaxResults int    // Maximum nodes to return (default: 20)
	Depth      int    // Neighbor depth (default: 1 for immediate neighbors)
}

// RetrievalResult contains the retrieved knowledge subgraph.
type RetrievalResult struct {
	Subgraph string // DOT format subgraph
	Count    int    // Number of nodes in result
}

// Retrieve searches the knowledge graph and returns a relevant subgraph.
// It uses FTS5 full-text search and includes neighboring nodes for context.
func Retrieve(db *sql.DB, sessionID string, options RetrievalOptions) (*RetrievalResult, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	// Set defaults
	if options.MaxResults <= 0 {
		options.MaxResults = 20
	}
	if options.Depth < 0 {
		options.Depth = 1
	}
	if options.Level == "" {
		options.Level = "all"
	}

	// Search for matching nodes
	nodeIDs, err := searchNodes(db, sessionID, options)
	if err != nil {
		return nil, fmt.Errorf("failed to search nodes: %w", err)
	}

	if len(nodeIDs) == 0 {
		// No matches - return empty graph
		return &RetrievalResult{
			Subgraph: "digraph ProjectKnowledge {\n}\n",
			Count:    0,
		}, nil
	}

	// Load full graph for this session
	graph, err := loadGraph(db, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load graph: %w", err)
	}

	// Create subgraph with neighbors
	subgraph := graph.Subgraph(nodeIDs, options.Depth)

	// Convert to DOT
	dot := subgraph.ToDOT()

	return &RetrievalResult{
		Subgraph: dot,
		Count:    len(subgraph.Nodes),
	}, nil
}

// searchNodes performs FTS5 search and returns matching node IDs.
func searchNodes(db *sql.DB, sessionID string, options RetrievalOptions) ([]string, error) {
	if strings.TrimSpace(options.Terms) == "" {
		return []string{}, nil
	}

	// Build FTS query
	// FTS5 expects terms separated by OR for multi-term search
	terms := strings.Fields(options.Terms)
	ftsQuery := strings.Join(terms, " OR ")

	// Build SQL query with level filter
	query := `
		SELECT DISTINCT n.id
		FROM nodes_fts f
		JOIN nodes n ON n.rowid = f.rowid
		WHERE f.nodes_fts MATCH ?
		  AND n.session_id = ?
	`

	args := []interface{}{ftsQuery, sessionID}

	// Add level filter if not "all"
	if options.Level != "all" {
		query += " AND n.level = ?"
		args = append(args, options.Level)
	}

	// Limit results
	query += fmt.Sprintf(" LIMIT %d", options.MaxResults)

	// Execute query
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS query failed: %w", err)
	}
	defer rows.Close() //nolint:errcheck // Close in defer is safe

	// Collect node IDs
	var nodeIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		nodeIDs = append(nodeIDs, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return nodeIDs, nil
}

// loadGraph loads the entire knowledge graph for a session.
//
//nolint:cyclop // Complexity from NULL handling is acceptable here
func loadGraph(db *sql.DB, sessionID string) (*Graph, error) {
	graph := NewGraph()

	// Load nodes
	nodeRows, err := db.Query(`
		SELECT id, type, level, status, description, tag, component, path, example, priority, raw_dot
		FROM nodes
		WHERE session_id = ?
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer nodeRows.Close() //nolint:errcheck // Close in defer is safe

	for nodeRows.Next() {
		node := &Node{}
		var tag, component, path, example, priority, rawDOT sql.NullString

		scanErr := nodeRows.Scan(
			&node.ID,
			&node.Type,
			&node.Level,
			&node.Status,
			&node.Description,
			&tag,
			&component,
			&path,
			&example,
			&priority,
			&rawDOT,
		)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan node: %w", scanErr)
		}

		// Convert nullable fields
		if tag.Valid {
			node.Tag = tag.String
		}
		if component.Valid {
			node.Component = component.String
		}
		if path.Valid {
			node.Path = path.String
		}
		if example.Valid {
			node.Example = example.String
		}
		if priority.Valid {
			node.Priority = priority.String
		}
		if rawDOT.Valid {
			node.RawDOT = rawDOT.String
		}

		graph.Nodes[node.ID] = node
	}

	if rowErr := nodeRows.Err(); rowErr != nil {
		return nil, fmt.Errorf("node rows error: %w", rowErr)
	}

	// Load edges
	edgeRows, err := db.Query(`
		SELECT from_id, to_id, relation, note
		FROM edges
		WHERE session_id = ?
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query edges: %w", err)
	}
	defer edgeRows.Close() //nolint:errcheck // Close in defer is safe

	for edgeRows.Next() {
		edge := &Edge{}
		var relation, note sql.NullString

		err := edgeRows.Scan(
			&edge.FromID,
			&edge.ToID,
			&relation,
			&note,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan edge: %w", err)
		}

		// Convert nullable fields
		if relation.Valid {
			edge.Relation = relation.String
		}
		if note.Valid {
			edge.Note = note.String
		}

		graph.Edges = append(graph.Edges, edge)
	}

	if err := edgeRows.Err(); err != nil {
		return nil, fmt.Errorf("edge rows error: %w", err)
	}

	return graph, nil
}

// StorePack saves a knowledge pack for a story.
func StorePack(db *sql.DB, storyID, sessionID, subgraph, searchTerms string, nodeCount int) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	_, err := db.Exec(`
		INSERT OR REPLACE INTO knowledge_packs (
			story_id, session_id, subgraph, search_terms, node_count,
			created_at, last_used
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, storyID, sessionID, subgraph, searchTerms, nodeCount, time.Now(), time.Now())

	if err != nil {
		return fmt.Errorf("failed to store pack: %w", err)
	}

	return nil
}

// GetCachedPack retrieves a cached knowledge pack for a story.
func GetCachedPack(db *sql.DB, storyID, sessionID string) (*RetrievalResult, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	var subgraph string
	var nodeCount int

	err := db.QueryRow(`
		SELECT subgraph, node_count
		FROM knowledge_packs
		WHERE story_id = ? AND session_id = ?
	`, storyID, sessionID).Scan(&subgraph, &nodeCount)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoCachedPack
		}
		return nil, fmt.Errorf("failed to query pack: %w", err)
	}

	// Update last_used timestamp
	_, _ = db.Exec(`
		UPDATE knowledge_packs
		SET last_used = ?
		WHERE story_id = ? AND session_id = ?
	`, time.Now(), storyID, sessionID)

	return &RetrievalResult{
		Subgraph: subgraph,
		Count:    nodeCount,
	}, nil
}

// CleanOldPacks removes knowledge packs older than the specified duration.
func CleanOldPacks(db *sql.DB, olderThan time.Duration) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	cutoff := time.Now().Add(-olderThan)

	_, err := db.Exec(`
		DELETE FROM knowledge_packs
		WHERE last_used < ?
	`, cutoff)

	if err != nil {
		return fmt.Errorf("failed to clean old packs: %w", err)
	}

	return nil
}
