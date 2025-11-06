package knowledge

import (
	"fmt"
)

// ValidationError represents a validation error in the knowledge graph.
type ValidationError struct {
	NodeID  string
	Field   string
	Message string
	Line    int // Line number in DOT file for better error reporting (not implemented yet)
}

// Error implements the error interface.
func (v ValidationError) Error() string {
	if v.NodeID != "" && v.Field != "" {
		return fmt.Sprintf("node '%s' field '%s': %s", v.NodeID, v.Field, v.Message)
	}
	if v.NodeID != "" {
		return fmt.Sprintf("node '%s': %s", v.NodeID, v.Message)
	}
	return v.Message
}

// ValidateGraph validates the entire graph structure and returns all errors found.
func ValidateGraph(graph *Graph) []ValidationError {
	var errors []ValidationError

	if graph == nil {
		errors = append(errors, ValidationError{
			Message: "graph is nil",
		})
		return errors
	}

	// Validate nodes
	for id, node := range graph.Nodes {
		errors = append(errors, validateNode(id, node)...)
	}

	// Validate edges
	for _, edge := range graph.Edges {
		errors = append(errors, validateEdge(edge, graph)...)
	}

	return errors
}

// validateNode validates a single node's attributes.
func validateNode(id string, node *Node) []ValidationError {
	var errors []ValidationError

	// Check required fields
	if node.Type == "" {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "type",
			Message: "type is required",
		})
	}

	if node.Level == "" {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "level",
			Message: "level is required",
		})
	}

	if node.Status == "" {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "status",
			Message: "status is required",
		})
	}

	if node.Description == "" {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "description",
			Message: "description is required",
		})
	}

	// Validate enum values
	validTypes := map[string]bool{
		"component":   true,
		"interface":   true,
		"abstraction": true,
		"datastore":   true,
		"external":    true,
		"pattern":     true,
		"rule":        true,
	}

	if node.Type != "" && !validTypes[node.Type] {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "type",
			Message: fmt.Sprintf("invalid type '%s', must be one of: component, interface, abstraction, datastore, external, pattern, rule", node.Type),
		})
	}

	validLevels := map[string]bool{
		"architecture":   true,
		"implementation": true,
	}

	if node.Level != "" && !validLevels[node.Level] {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "level",
			Message: fmt.Sprintf("invalid level '%s', must be one of: architecture, implementation", node.Level),
		})
	}

	validStatuses := map[string]bool{
		"current":    true,
		"deprecated": true,
		"future":     true,
		"legacy":     true,
	}

	if node.Status != "" && !validStatuses[node.Status] {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "status",
			Message: fmt.Sprintf("invalid status '%s', must be one of: current, deprecated, future, legacy", node.Status),
		})
	}

	validPriorities := map[string]bool{
		"critical": true,
		"high":     true,
		"medium":   true,
		"low":      true,
	}

	if node.Priority != "" && !validPriorities[node.Priority] {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "priority",
			Message: fmt.Sprintf("invalid priority '%s', must be one of: critical, high, medium, low", node.Priority),
		})
	}

	// Type-specific validation
	if node.Type == "rule" && node.Priority == "" {
		errors = append(errors, ValidationError{
			NodeID:  id,
			Field:   "priority",
			Message: "priority is required for nodes of type 'rule'",
		})
	}

	return errors
}

// validateEdge validates a single edge and its endpoints.
func validateEdge(edge *Edge, graph *Graph) []ValidationError {
	var errors []ValidationError

	// Check that referenced nodes exist
	if _, ok := graph.Nodes[edge.FromID]; !ok {
		errors = append(errors, ValidationError{
			Message: fmt.Sprintf("edge references unknown source node '%s'", edge.FromID),
		})
	}

	if _, ok := graph.Nodes[edge.ToID]; !ok {
		errors = append(errors, ValidationError{
			Message: fmt.Sprintf("edge references unknown target node '%s'", edge.ToID),
		})
	}

	// Validate relation enum
	validRelations := map[string]bool{
		"calls":           true,
		"uses":            true,
		"implements":      true,
		"configured_with": true,
		"must_follow":     true,
		"must_not_use":    true,
		"superseded_by":   true,
		"supersedes":      true,
		"coexists_with":   true,
	}

	if edge.Relation != "" && !validRelations[edge.Relation] {
		errors = append(errors, ValidationError{
			Message: fmt.Sprintf("invalid edge relation '%s', must be one of: calls, uses, implements, configured_with, must_follow, must_not_use, superseded_by, supersedes, coexists_with", edge.Relation),
		})
	}

	return errors
}

// ValidateAndReport validates the graph and returns a formatted error message if validation fails.
// Returns nil if validation succeeds.
func ValidateAndReport(graph *Graph) error {
	errors := ValidateGraph(graph)
	if len(errors) == 0 {
		return nil
	}

	// Format error message
	msg := fmt.Sprintf("knowledge graph validation failed with %d error(s):\n", len(errors))
	for i := range errors {
		msg += fmt.Sprintf("  %d. %s\n", i+1, errors[i].Error())
	}

	return fmt.Errorf("%s", msg)
}
