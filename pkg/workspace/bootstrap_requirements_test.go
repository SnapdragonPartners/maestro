package workspace

import (
	"testing"
)

func TestIsValidRequirementID(t *testing.T) {
	tests := []struct {
		name string
		id   BootstrapRequirementID
		want bool
	}{
		{"container is valid", BootstrapReqContainer, true},
		{"dockerfile is valid", BootstrapReqDockerfile, true},
		{"build_system is valid", BootstrapReqBuildSystem, true},
		{"knowledge_graph is valid", BootstrapReqKnowledgeGraph, true},
		{"git_access is valid", BootstrapReqGitAccess, true},
		{"binary_size is valid", BootstrapReqBinarySize, true},
		{"claude_code is valid", BootstrapReqClaudeCode, true},
		{"invalid ID", BootstrapRequirementID("invalid"), false},
		{"empty ID", BootstrapRequirementID(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidRequirementID(tt.id); got != tt.want {
				t.Errorf("IsValidRequirementID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

func TestRequirementIDToFailureType(t *testing.T) {
	tests := []struct {
		name string
		id   BootstrapRequirementID
		want BootstrapFailureType
	}{
		{"container maps to container", BootstrapReqContainer, BootstrapFailureContainer},
		{"dockerfile maps to container", BootstrapReqDockerfile, BootstrapFailureContainer},
		{"build_system maps to build_system", BootstrapReqBuildSystem, BootstrapFailureBuildSystem},
		{"knowledge_graph maps to infrastructure", BootstrapReqKnowledgeGraph, BootstrapFailureInfrastructure},
		{"git_access maps to git_access", BootstrapReqGitAccess, BootstrapFailureGitAccess},
		{"binary_size maps to binary_size", BootstrapReqBinarySize, BootstrapFailureBinarySize},
		{"claude_code maps to claude_code", BootstrapReqClaudeCode, BootstrapFailureClaudeCode},
		{"unknown maps to infrastructure", BootstrapRequirementID("unknown"), BootstrapFailureInfrastructure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RequirementIDToFailureType(tt.id); got != tt.want {
				t.Errorf("RequirementIDToFailureType(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

func TestRequirementIDsToFailures(t *testing.T) {
	tests := []struct {
		name    string
		ids     []BootstrapRequirementID
		wantLen int
	}{
		{
			name:    "empty list",
			ids:     []BootstrapRequirementID{},
			wantLen: 0,
		},
		{
			name:    "single requirement",
			ids:     []BootstrapRequirementID{BootstrapReqContainer},
			wantLen: 1,
		},
		{
			name: "multiple requirements",
			ids: []BootstrapRequirementID{
				BootstrapReqContainer,
				BootstrapReqBuildSystem,
				BootstrapReqKnowledgeGraph,
			},
			wantLen: 3,
		},
		{
			name: "filters invalid IDs",
			ids: []BootstrapRequirementID{
				BootstrapReqContainer,
				BootstrapRequirementID("invalid"),
				BootstrapReqBuildSystem,
			},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := RequirementIDsToFailures(tt.ids)
			if len(failures) != tt.wantLen {
				t.Errorf("RequirementIDsToFailures() returned %d failures, want %d", len(failures), tt.wantLen)
			}

			// Verify each failure has required fields
			for i, f := range failures {
				if f.Type == "" {
					t.Errorf("failure[%d].Type is empty", i)
				}
				if f.Component == "" {
					t.Errorf("failure[%d].Component is empty", i)
				}
				if f.Description == "" {
					t.Errorf("failure[%d].Description is empty", i)
				}
				if f.Priority == 0 {
					t.Errorf("failure[%d].Priority is 0", i)
				}
			}
		})
	}
}

func TestRequirementIDsToFailures_FieldValues(t *testing.T) {
	failures := RequirementIDsToFailures([]BootstrapRequirementID{BootstrapReqBuildSystem})

	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(failures))
	}

	f := failures[0]

	if f.Type != BootstrapFailureBuildSystem {
		t.Errorf("Type = %q, want %q", f.Type, BootstrapFailureBuildSystem)
	}

	if f.Component != string(BootstrapReqBuildSystem) {
		t.Errorf("Component = %q, want %q", f.Component, BootstrapReqBuildSystem)
	}

	if f.Description == "" {
		t.Error("Description should not be empty")
	}

	if f.Priority != 1 {
		t.Errorf("Priority = %d, want 1 (build_system is critical)", f.Priority)
	}

	if f.Details == nil {
		t.Error("Details should be initialized (not nil)")
	}
}
