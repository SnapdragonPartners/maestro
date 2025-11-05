// Package knowledge implements the knowledge graph system for storing architectural patterns and decisions.
package knowledge

import (
	"fmt"
	"strings"

	"github.com/awalterschulze/gographviz"
)

// Node represents a knowledge graph node with all its attributes.
type Node struct {
	ID          string
	Type        string // component|interface|abstraction|datastore|external|pattern|rule
	Level       string // architecture|implementation
	Status      string // current|deprecated|future|legacy
	Description string
	Tag         string
	Component   string
	Path        string
	Example     string
	Priority    string // critical|high|medium|low (for rules only)
	RawDOT      string // Full DOT node definition for reconstruction
}

// Edge represents a relationship between two nodes.
type Edge struct {
	FromID   string
	ToID     string
	Relation string // calls|uses|implements|configured_with|must_follow|must_not_use|superseded_by|supersedes|coexists_with
	Note     string
}

// Graph represents the complete knowledge graph.
type Graph struct {
	Nodes map[string]*Node
	Edges []*Edge
}

// NewGraph creates a new empty graph.
func NewGraph() *Graph {
	return &Graph{
		Nodes: make(map[string]*Node),
		Edges: make([]*Edge, 0),
	}
}

// ParseDOT parses a DOT format string into a Graph.
func ParseDOT(content string) (*Graph, error) {
	if strings.TrimSpace(content) == "" {
		return NewGraph(), nil
	}

	// Parse using gographviz
	graphAst, err := gographviz.ParseString(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DOT: %w", err)
	}

	graph := gographviz.NewGraph()
	if err := gographviz.Analyse(graphAst, graph); err != nil {
		return nil, fmt.Errorf("failed to analyze DOT: %w", err)
	}

	// Convert to our Graph structure
	result := NewGraph()

	// Extract nodes using Lookup map
	for nodeName, node := range graph.Nodes.Lookup {
		// Remove quotes from node name
		nodeID := unquote(nodeName)

		// Extract attributes
		attrs := node.Attrs

		n := &Node{
			ID:          nodeID,
			Type:        getAttrFromGV(attrs, "type"),
			Level:       getAttrFromGV(attrs, "level"),
			Status:      getAttrFromGV(attrs, "status"),
			Description: getAttrFromGV(attrs, "description"),
			Tag:         getAttrFromGV(attrs, "tag"),
			Component:   getAttrFromGV(attrs, "component"),
			Path:        getAttrFromGV(attrs, "path"),
			Example:     getAttrFromGV(attrs, "example"),
			Priority:    getAttrFromGV(attrs, "priority"),
		}

		// Store raw DOT for reconstruction
		n.RawDOT = buildRawDOTFromGV(nodeID, attrs)

		result.Nodes[nodeID] = n
	}

	// Extract edges
	for _, edge := range graph.Edges.Edges {
		fromID := unquote(edge.Src)
		toID := unquote(edge.Dst)

		e := &Edge{
			FromID:   fromID,
			ToID:     toID,
			Relation: getAttrFromGV(edge.Attrs, "relation"),
			Note:     getAttrFromGV(edge.Attrs, "note"),
		}

		result.Edges = append(result.Edges, e)
	}

	return result, nil
}

// ToDOT converts the graph back to DOT format.
//
//nolint:gocritic // We need literal quotes in DOT format, not Go-escaped quotes
func (g *Graph) ToDOT() string {
	var sb strings.Builder

	sb.WriteString("digraph ProjectKnowledge {\n")

	// Write nodes
	for _, node := range g.Nodes {
		sb.WriteString(fmt.Sprintf("    \"%s\" [\n", node.ID))

		// Required attributes
		sb.WriteString(fmt.Sprintf("        type=\"%s\"\n", node.Type))
		sb.WriteString(fmt.Sprintf("        level=\"%s\"\n", node.Level))
		sb.WriteString(fmt.Sprintf("        status=\"%s\"\n", node.Status))
		sb.WriteString(fmt.Sprintf("        description=\"%s\"\n", escapeQuotes(node.Description)))

		// Optional attributes
		if node.Tag != "" {
			sb.WriteString(fmt.Sprintf("        tag=\"%s\"\n", escapeQuotes(node.Tag)))
		}
		if node.Component != "" {
			sb.WriteString(fmt.Sprintf("        component=\"%s\"\n", escapeQuotes(node.Component)))
		}
		if node.Path != "" {
			sb.WriteString(fmt.Sprintf("        path=\"%s\"\n", escapeQuotes(node.Path)))
		}
		if node.Example != "" {
			sb.WriteString(fmt.Sprintf("        example=\"%s\"\n", escapeQuotes(node.Example)))
		}
		if node.Priority != "" {
			sb.WriteString(fmt.Sprintf("        priority=\"%s\"\n", node.Priority))
		}

		sb.WriteString("    ];\n\n")
	}

	// Write edges
	for _, edge := range g.Edges {
		sb.WriteString(fmt.Sprintf("    \"%s\" -> \"%s\"", edge.FromID, edge.ToID))
		if edge.Relation != "" || edge.Note != "" {
			sb.WriteString(" [")
			if edge.Relation != "" {
				sb.WriteString(fmt.Sprintf("relation=\"%s\"", edge.Relation))
			}
			if edge.Note != "" {
				if edge.Relation != "" {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("note=\"%s\"", escapeQuotes(edge.Note)))
			}
			sb.WriteString("]")
		}
		sb.WriteString(";\n")
	}

	sb.WriteString("}\n")

	return sb.String()
}

// Filter creates a new graph containing only nodes matching the predicate.
// Edges are preserved only if both nodes are included.
func (g *Graph) Filter(predicate func(*Node) bool) *Graph {
	result := NewGraph()

	// Add matching nodes
	for id, node := range g.Nodes {
		if predicate(node) {
			result.Nodes[id] = node
		}
	}

	// Add edges where both endpoints are included
	for _, edge := range g.Edges {
		if _, fromOK := result.Nodes[edge.FromID]; fromOK {
			if _, toOK := result.Nodes[edge.ToID]; toOK {
				result.Edges = append(result.Edges, edge)
			}
		}
	}

	return result
}

// Subgraph creates a new graph containing specified nodes and their neighbors up to depth.
// depth=0 means only specified nodes, depth=1 includes immediate neighbors, etc.
func (g *Graph) Subgraph(nodeIDs []string, depth int) *Graph {
	if depth < 0 {
		depth = 0
	}

	// Start with specified nodes
	included := make(map[string]bool)
	for _, id := range nodeIDs {
		if _, exists := g.Nodes[id]; exists {
			included[id] = true
		}
	}

	// Expand by depth
	for d := 0; d < depth; d++ {
		// Find neighbors of currently included nodes
		neighbors := make(map[string]bool)
		for _, edge := range g.Edges {
			if included[edge.FromID] {
				if _, exists := g.Nodes[edge.ToID]; exists {
					neighbors[edge.ToID] = true
				}
			}
			if included[edge.ToID] {
				if _, exists := g.Nodes[edge.FromID]; exists {
					neighbors[edge.FromID] = true
				}
			}
		}

		// Add neighbors to included set
		for id := range neighbors {
			included[id] = true
		}
	}

	// Build result graph
	result := NewGraph()
	for id := range included {
		result.Nodes[id] = g.Nodes[id]
	}

	// Add edges where both endpoints are included
	for _, edge := range g.Edges {
		if included[edge.FromID] && included[edge.ToID] {
			result.Edges = append(result.Edges, edge)
		}
	}

	return result
}

// Helper functions

// unquote removes surrounding quotes from a string if present.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// getAttrFromGV extracts an attribute value from gographviz Attrs, unquoting if necessary.
func getAttrFromGV(attrs gographviz.Attrs, key string) string {
	// Try to find the attribute by creating an Attr key
	for k, v := range attrs {
		if string(k) == key {
			return unquote(v)
		}
	}
	return ""
}

// escapeQuotes escapes double quotes in strings for DOT format.
func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, "\"", "\\\"")
}

// buildRawDOTFromGV constructs the raw DOT representation of a node from gographviz Attrs.
//
//nolint:gocritic // We need literal quotes in DOT format
func buildRawDOTFromGV(nodeID string, attrs gographviz.Attrs) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\"%s\" [", nodeID))

	first := true
	for key, val := range attrs {
		if !first {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%s=%s", string(key), val))
		first = false
	}

	sb.WriteString("]")
	return sb.String()
}
