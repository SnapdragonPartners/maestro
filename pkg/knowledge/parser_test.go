package knowledge

import (
	"strings"
	"testing"
)

// TestParseDOT tests parsing valid DOT graphs with various node configurations.
func TestParseDOT(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantNodeCount int
		wantEdgeCount int
		checkNode     string
		checkType     string
	}{
		{
			name: "basic graph with all attributes",
			input: `digraph Knowledge {
				"api-standards" [
					type="rule"
					level="architecture"
					status="current"
					description="Follow RESTful API standards"
					priority="high"
				];
			}`,
			wantNodeCount: 1,
			wantEdgeCount: 0,
			checkNode:     "api-standards",
			checkType:     "rule",
		},
		{
			name: "graph with edges",
			input: `digraph Knowledge {
				"component-a" [type="component" level="implementation" status="current" description="Component A"];
				"component-b" [type="component" level="implementation" status="current" description="Component B"];
				"component-a" -> "component-b" [relation="depends_on"];
			}`,
			wantNodeCount: 2,
			wantEdgeCount: 1,
			checkNode:     "component-a",
			checkType:     "component",
		},
		{
			name: "graph with optional attributes",
			input: `digraph Knowledge {
				"error-pattern" [
					type="pattern"
					level="implementation"
					status="current"
					description="Error handling pattern"
					tag="error-handling"
					component="core"
					path="pkg/errors"
					example="return fmt.Errorf(...)"
				];
			}`,
			wantNodeCount: 1,
			wantEdgeCount: 0,
			checkNode:     "error-pattern",
			checkType:     "pattern",
		},
		{
			name: "empty graph",
			input: `digraph Knowledge {
			}`,
			wantNodeCount: 0,
			wantEdgeCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph, err := ParseDOT(tt.input)
			if err != nil {
				t.Fatalf("ParseDOT() error = %v", err)
			}

			if len(graph.Nodes) != tt.wantNodeCount {
				t.Errorf("ParseDOT() node count = %d, want %d", len(graph.Nodes), tt.wantNodeCount)
			}

			if len(graph.Edges) != tt.wantEdgeCount {
				t.Errorf("ParseDOT() edge count = %d, want %d", len(graph.Edges), tt.wantEdgeCount)
			}

			if tt.checkNode != "" {
				node, exists := graph.Nodes[tt.checkNode]
				if !exists {
					t.Errorf("ParseDOT() missing expected node %q", tt.checkNode)
				}
				if node.Type != tt.checkType {
					t.Errorf("ParseDOT() node %q type = %q, want %q", tt.checkNode, node.Type, tt.checkType)
				}
			}
		})
	}
}

// TestParseMalformed tests parsing invalid DOT syntax.
// Our regex-based parser is intentionally lenient - it extracts what it can
// and only errors on truly invalid input (missing graph declaration).
func TestParseMalformed(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "missing closing brace",
			input:       `digraph Knowledge { "node1" [type="rule"`,
			expectError: false, // Lenient parser extracts what it can
		},
		{
			name:        "invalid attribute syntax",
			input:       `digraph Knowledge { "node1" [type:rule]; }`,
			expectError: false, // Regex won't match, returns graph with no nodes
		},
		{
			name:        "completely invalid",
			input:       `not valid syntax whatsoever`,
			expectError: true, // Missing digraph/graph keyword
		},
		{
			name:        "empty string",
			input:       ``,
			expectError: false, // Empty is valid, returns empty graph
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph, err := ParseDOT(tt.input)
			if tt.expectError {
				if err == nil {
					t.Errorf("ParseDOT() expected error for input %q, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseDOT() unexpected error for input %q: %v", tt.input, err)
				}
				if graph == nil {
					t.Errorf("ParseDOT() returned nil graph for input %q", tt.input)
				}
			}
		})
	}
}

// TestRoundTrip tests that parsing and generating produces equivalent graphs.
func TestRoundTrip(t *testing.T) {
	input := `digraph Knowledge {
	"api-standards" [type="rule" level="architecture" status="current" description="Follow RESTful API standards" priority="high"];
	"error-handling" [type="pattern" level="implementation" status="current" description="Use structured errors"];
	"api-standards" -> "error-handling" [relation="influences"];
}`

	// Parse original
	graph1, err := ParseDOT(input)
	if err != nil {
		t.Fatalf("ParseDOT() first parse error = %v", err)
	}

	// Generate DOT from parsed graph
	generated := graph1.ToDOT()

	// Parse generated DOT
	graph2, err := ParseDOT(generated)
	if err != nil {
		t.Fatalf("ParseDOT() second parse error = %v", err)
	}

	// Compare graphs
	if len(graph1.Nodes) != len(graph2.Nodes) {
		t.Errorf("Round trip node count mismatch: %d vs %d", len(graph1.Nodes), len(graph2.Nodes))
	}

	if len(graph1.Edges) != len(graph2.Edges) {
		t.Errorf("Round trip edge count mismatch: %d vs %d", len(graph1.Edges), len(graph2.Edges))
	}

	// Check specific nodes
	for id, node1 := range graph1.Nodes {
		node2, exists := graph2.Nodes[id]
		if !exists {
			t.Errorf("Round trip missing node %q", id)
			continue
		}
		if node1.Type != node2.Type {
			t.Errorf("Round trip node %q type mismatch: %q vs %q", id, node1.Type, node2.Type)
		}
		if node1.Description != node2.Description {
			t.Errorf("Round trip node %q description mismatch", id)
		}
	}
}

// TestNodeExtraction tests that all node properties are correctly extracted.
func TestNodeExtraction(t *testing.T) {
	input := `digraph Knowledge {
		"test-node" [
			type="rule"
			level="architecture"
			status="current"
			description="Test description"
			tag="testing"
			component="test-component"
			path="test/path"
			example="test example"
			priority="critical"
		];
	}`

	graph, err := ParseDOT(input)
	if err != nil {
		t.Fatalf("ParseDOT() error = %v", err)
	}

	node, exists := graph.Nodes["test-node"]
	if !exists {
		t.Fatal("ParseDOT() missing test-node")
	}

	tests := []struct {
		name  string
		got   string
		want  string
		field string
	}{
		{"Type", node.Type, "rule", "type"},
		{"Level", node.Level, "architecture", "level"},
		{"Status", node.Status, "current", "status"},
		{"Description", node.Description, "Test description", "description"},
		{"Tag", node.Tag, "testing", "tag"},
		{"Component", node.Component, "test-component", "component"},
		{"Path", node.Path, "test/path", "path"},
		{"Example", node.Example, "test example", "example"},
		{"Priority", node.Priority, "critical", "priority"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("Node %s = %q, want %q", tt.field, tt.got, tt.want)
			}
		})
	}
}

// TestEdgeExtraction tests that edges and their properties are correctly extracted.
func TestEdgeExtraction(t *testing.T) {
	input := `digraph Knowledge {
		"node-a" [type="component" level="implementation" status="current" description="Node A"];
		"node-b" [type="component" level="implementation" status="current" description="Node B"];
		"node-c" [type="component" level="implementation" status="current" description="Node C"];
		"node-a" -> "node-b" [relation="depends_on" note="critical dependency"];
		"node-b" -> "node-c" [relation="uses"];
	}`

	graph, err := ParseDOT(input)
	if err != nil {
		t.Fatalf("ParseDOT() error = %v", err)
	}

	if len(graph.Edges) != 2 {
		t.Fatalf("ParseDOT() edge count = %d, want 2", len(graph.Edges))
	}

	// Check first edge
	edge1 := graph.Edges[0]
	if edge1.FromID != "node-a" || edge1.ToID != "node-b" {
		t.Errorf("Edge 1 endpoints = %q -> %q, want node-a -> node-b", edge1.FromID, edge1.ToID)
	}
	if edge1.Relation != "depends_on" {
		t.Errorf("Edge 1 relation = %q, want depends_on", edge1.Relation)
	}
	if edge1.Note != "critical dependency" {
		t.Errorf("Edge 1 note = %q, want 'critical dependency'", edge1.Note)
	}

	// Check second edge
	edge2 := graph.Edges[1]
	if edge2.FromID != "node-b" || edge2.ToID != "node-c" {
		t.Errorf("Edge 2 endpoints = %q -> %q, want node-b -> node-c", edge2.FromID, edge2.ToID)
	}
	if edge2.Relation != "uses" {
		t.Errorf("Edge 2 relation = %q, want uses", edge2.Relation)
	}
}

// TestFilter tests filtering nodes by predicate.
func TestFilter(t *testing.T) {
	input := `digraph Knowledge {
		"arch-rule" [type="rule" level="architecture" status="current" description="Architecture rule"];
		"impl-pattern" [type="pattern" level="implementation" status="current" description="Implementation pattern"];
		"deprecated-rule" [type="rule" level="architecture" status="deprecated" description="Old rule"];
	}`

	graph, err := ParseDOT(input)
	if err != nil {
		t.Fatalf("ParseDOT() error = %v", err)
	}

	tests := []struct {
		name      string
		predicate func(*Node) bool
		wantCount int
		wantNodes []string
	}{
		{
			name: "filter architecture level",
			predicate: func(n *Node) bool {
				return n.Level == "architecture"
			},
			wantCount: 2,
			wantNodes: []string{"arch-rule", "deprecated-rule"},
		},
		{
			name: "filter current status",
			predicate: func(n *Node) bool {
				return n.Status == "current"
			},
			wantCount: 2,
			wantNodes: []string{"arch-rule", "impl-pattern"},
		},
		{
			name: "filter rules only",
			predicate: func(n *Node) bool {
				return n.Type == "rule"
			},
			wantCount: 2,
			wantNodes: []string{"arch-rule", "deprecated-rule"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := graph.Filter(tt.predicate)
			if len(filtered.Nodes) != tt.wantCount {
				t.Errorf("Filter() node count = %d, want %d", len(filtered.Nodes), tt.wantCount)
			}
			for _, wantNode := range tt.wantNodes {
				if _, exists := filtered.Nodes[wantNode]; !exists {
					t.Errorf("Filter() missing expected node %q", wantNode)
				}
			}
		})
	}
}

// TestSubgraph tests subgraph extraction with neighbor expansion.
func TestSubgraph(t *testing.T) {
	input := `digraph Knowledge {
		"a" [type="component" level="implementation" status="current" description="A"];
		"b" [type="component" level="implementation" status="current" description="B"];
		"c" [type="component" level="implementation" status="current" description="C"];
		"d" [type="component" level="implementation" status="current" description="D"];
		"a" -> "b" [relation="depends_on"];
		"b" -> "c" [relation="depends_on"];
		"c" -> "d" [relation="depends_on"];
	}`

	graph, err := ParseDOT(input)
	if err != nil {
		t.Fatalf("ParseDOT() error = %v", err)
	}

	tests := []struct {
		name      string
		nodeIDs   []string
		depth     int
		wantCount int
		wantNodes []string
	}{
		{
			name:      "single node, depth 0",
			nodeIDs:   []string{"b"},
			depth:     0,
			wantCount: 1,
			wantNodes: []string{"b"},
		},
		{
			name:      "single node, depth 1",
			nodeIDs:   []string{"b"},
			depth:     1,
			wantCount: 3,
			wantNodes: []string{"a", "b", "c"},
		},
		{
			name:      "single node, depth 2",
			nodeIDs:   []string{"b"},
			depth:     2,
			wantCount: 4,
			wantNodes: []string{"a", "b", "c", "d"},
		},
		{
			name:      "multiple nodes, depth 0",
			nodeIDs:   []string{"a", "c"},
			depth:     0,
			wantCount: 2,
			wantNodes: []string{"a", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subgraph := graph.Subgraph(tt.nodeIDs, tt.depth)
			if len(subgraph.Nodes) != tt.wantCount {
				t.Errorf("Subgraph() node count = %d, want %d", len(subgraph.Nodes), tt.wantCount)
			}
			for _, wantNode := range tt.wantNodes {
				if _, exists := subgraph.Nodes[wantNode]; !exists {
					t.Errorf("Subgraph() missing expected node %q", wantNode)
				}
			}
		})
	}
}

// TestToDOT tests DOT generation from parsed graph.
func TestToDOT(t *testing.T) {
	input := `digraph Knowledge {
		"test-node" [type="rule" level="architecture" status="current" description="Test rule"];
	}`

	graph, err := ParseDOT(input)
	if err != nil {
		t.Fatalf("ParseDOT() error = %v", err)
	}

	output := graph.ToDOT()

	// Check for expected components
	requiredSubstrings := []string{
		"digraph",
		"test-node",
		"type=",
		"level=",
		"status=",
		"description=",
	}

	for _, substr := range requiredSubstrings {
		if !strings.Contains(output, substr) {
			t.Errorf("ToDOT() output missing %q", substr)
		}
	}

	// Verify output is valid DOT by parsing it again
	_, err = ParseDOT(output)
	if err != nil {
		t.Errorf("ToDOT() produced invalid DOT: %v", err)
	}
}
