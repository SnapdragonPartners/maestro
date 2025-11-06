package knowledge

import (
	"strings"
	"testing"
)

// TestRequiredFields tests validation of required node fields.
func TestRequiredFields(t *testing.T) {
	tests := []struct {
		name      string
		graph     *Graph
		wantError bool
		errorMsg  string
	}{
		{
			name: "all required fields present",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "pattern",
						Level:       "architecture",
						Status:      "current",
						Description: "Test description",
					},
				},
				Edges: []*Edge{},
			},
			wantError: false,
		},
		{
			name: "missing type",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Level:       "architecture",
						Status:      "current",
						Description: "Test description",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
			errorMsg:  "type is required",
		},
		{
			name: "missing level",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "rule",
						Status:      "current",
						Description: "Test description",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
			errorMsg:  "level is required",
		},
		{
			name: "missing status",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "rule",
						Level:       "architecture",
						Description: "Test description",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
			errorMsg:  "status is required",
		},
		{
			name: "missing description",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:     "test",
						Type:   "rule",
						Level:  "architecture",
						Status: "current",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
			errorMsg:  "description is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidateGraph(tt.graph)
			hasError := len(errors) > 0

			if hasError != tt.wantError {
				t.Errorf("ValidateGraph() hasError = %v, want %v", hasError, tt.wantError)
			}

			if tt.wantError && len(errors) > 0 {
				found := false
				for _, err := range errors {
					if strings.Contains(err.Message, tt.errorMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ValidateGraph() expected error message containing %q not found in errors", tt.errorMsg)
				}
			}
		})
	}
}

// TestEnumValidation tests validation of enum field values.
func TestEnumValidation(t *testing.T) {
	tests := []struct {
		name      string
		graph     *Graph
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid type enum",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "pattern",
						Level:       "architecture",
						Status:      "current",
						Description: "Test",
					},
				},
				Edges: []*Edge{},
			},
			wantError: false,
		},
		{
			name: "invalid type enum",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "invalid-type",
						Level:       "architecture",
						Status:      "current",
						Description: "Test",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
			errorMsg:  "invalid type",
		},
		{
			name: "invalid level enum",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "rule",
						Level:       "invalid-level",
						Status:      "current",
						Description: "Test",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
			errorMsg:  "invalid level",
		},
		{
			name: "invalid status enum",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "rule",
						Level:       "architecture",
						Status:      "invalid-status",
						Description: "Test",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
			errorMsg:  "invalid status",
		},
		{
			name: "invalid priority enum",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "rule",
						Level:       "architecture",
						Status:      "current",
						Description: "Test",
						Priority:    "super-urgent",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
			errorMsg:  "invalid priority",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidateGraph(tt.graph)
			hasError := len(errors) > 0

			if hasError != tt.wantError {
				t.Errorf("ValidateGraph() hasError = %v, want %v", hasError, tt.wantError)
			}

			if tt.wantError && len(errors) > 0 {
				found := false
				for _, err := range errors {
					if strings.Contains(err.Message, tt.errorMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ValidateGraph() expected error message containing %q not found, got %+v", tt.errorMsg, errors)
				}
			}
		})
	}
}

// TestEdgeValidation tests validation of edge references.
func TestEdgeValidation(t *testing.T) {
	tests := []struct {
		name      string
		graph     *Graph
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid edge references",
			graph: &Graph{
				Nodes: map[string]*Node{
					"node-a": {
						ID:          "node-a",
						Type:        "component",
						Level:       "implementation",
						Status:      "current",
						Description: "Node A",
					},
					"node-b": {
						ID:          "node-b",
						Type:        "component",
						Level:       "implementation",
						Status:      "current",
						Description: "Node B",
					},
				},
				Edges: []*Edge{
					{FromID: "node-a", ToID: "node-b", Relation: "uses"},
				},
			},
			wantError: false,
		},
		{
			name: "edge from non-existent node",
			graph: &Graph{
				Nodes: map[string]*Node{
					"node-b": {
						ID:          "node-b",
						Type:        "component",
						Level:       "implementation",
						Status:      "current",
						Description: "Node B",
					},
				},
				Edges: []*Edge{
					{FromID: "non-existent", ToID: "node-b", Relation: "uses"},
				},
			},
			wantError: true,
			errorMsg:  "references unknown",
		},
		{
			name: "edge to non-existent node",
			graph: &Graph{
				Nodes: map[string]*Node{
					"node-a": {
						ID:          "node-a",
						Type:        "component",
						Level:       "implementation",
						Status:      "current",
						Description: "Node A",
					},
				},
				Edges: []*Edge{
					{FromID: "node-a", ToID: "non-existent", Relation: "uses"},
				},
			},
			wantError: true,
			errorMsg:  "references unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidateGraph(tt.graph)
			hasError := len(errors) > 0

			if hasError != tt.wantError {
				t.Errorf("ValidateGraph() hasError = %v, want %v (errors: %+v)", hasError, tt.wantError, errors)
			}

			if tt.wantError && len(errors) > 0 {
				found := false
				for _, err := range errors {
					if strings.Contains(err.Message, tt.errorMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ValidateGraph() expected error message containing %q not found, got %+v", tt.errorMsg, errors)
				}
			}
		})
	}
}

// TestRulePriorityValidation tests that rules must have priority set.
func TestRulePriorityValidation(t *testing.T) {
	tests := []struct {
		name      string
		graph     *Graph
		wantError bool
	}{
		{
			name: "rule with priority",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "rule",
						Level:       "architecture",
						Status:      "current",
						Description: "Test rule",
						Priority:    "high",
					},
				},
				Edges: []*Edge{},
			},
			wantError: false,
		},
		{
			name: "rule without priority",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "rule",
						Level:       "architecture",
						Status:      "current",
						Description: "Test rule",
					},
				},
				Edges: []*Edge{},
			},
			wantError: true,
		},
		{
			name: "non-rule without priority is ok",
			graph: &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "pattern",
						Level:       "implementation",
						Status:      "current",
						Description: "Test pattern",
					},
				},
				Edges: []*Edge{},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidateGraph(tt.graph)
			hasError := len(errors) > 0

			if hasError != tt.wantError {
				t.Errorf("ValidateGraph() hasError = %v, want %v", hasError, tt.wantError)
			}
		})
	}
}

// TestMultipleValidationErrors tests that all validation errors are reported.
func TestMultipleValidationErrors(t *testing.T) {
	graph := &Graph{
		Nodes: map[string]*Node{
			"bad-node": {
				ID:     "bad-node",
				Type:   "invalid-type",
				Level:  "invalid-level",
				Status: "invalid-status",
				// Missing description
			},
		},
		Edges: []*Edge{
			{FromID: "bad-node", ToID: "non-existent", Relation: "uses"},
		},
	}

	errors := ValidateGraph(graph)

	// Should have at least 5 errors:
	// 1. invalid type
	// 2. invalid level
	// 3. invalid status
	// 4. missing description
	// 5. edge to non-existent node
	if len(errors) < 5 {
		t.Errorf("ValidateGraph() expected at least 5 errors, got %d: %+v", len(errors), errors)
	}
}

// TestValidAllNodeTypes tests validation with all valid node types.
func TestValidAllNodeTypes(t *testing.T) {
	types := []string{"component", "interface", "abstraction", "datastore", "external", "pattern", "rule"}

	for _, nodeType := range types {
		t.Run("type_"+nodeType, func(t *testing.T) {
			graph := &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        nodeType,
						Level:       "architecture",
						Status:      "current",
						Description: "Test node",
					},
				},
				Edges: []*Edge{},
			}

			// Add priority for rule type
			if nodeType == "rule" {
				graph.Nodes["test"].Priority = "medium"
			}

			errors := ValidateGraph(graph)
			if len(errors) > 0 {
				t.Errorf("ValidateGraph() for type %q got unexpected errors: %+v", nodeType, errors)
			}
		})
	}
}

// TestValidAllStatuses tests validation with all valid status values.
func TestValidAllStatuses(t *testing.T) {
	statuses := []string{"current", "deprecated", "future", "legacy"}

	for _, status := range statuses {
		t.Run("status_"+status, func(t *testing.T) {
			graph := &Graph{
				Nodes: map[string]*Node{
					"test": {
						ID:          "test",
						Type:        "pattern",
						Level:       "implementation",
						Status:      status,
						Description: "Test node",
					},
				},
				Edges: []*Edge{},
			}

			errors := ValidateGraph(graph)
			if len(errors) > 0 {
				t.Errorf("ValidateGraph() for status %q got unexpected errors: %+v", status, errors)
			}
		})
	}
}
