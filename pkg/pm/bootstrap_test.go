package pm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/workspace"
)

func TestBootstrapDetector_DetectMissingComponents(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	detector := NewBootstrapDetector(tmpDir)

	// Test detection in empty directory
	reqs, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	// Should detect all components as missing
	if !reqs.NeedsDockerfile {
		t.Error("Expected NeedsDockerfile = true for empty directory")
	}

	if !reqs.NeedsMakefile {
		t.Error("Expected NeedsMakefile = true for empty directory")
	}

	if !reqs.NeedsKnowledgeGraph {
		t.Error("Expected NeedsKnowledgeGraph = true for empty directory")
	}

	if len(reqs.MissingComponents) == 0 {
		t.Error("Expected missing components to be detected")
	}

	// Verify HasAnyMissingComponents works
	if !reqs.HasAnyMissingComponents() {
		t.Error("Expected HasAnyMissingComponents() = true for empty directory")
	}
}

// Note: Platform detection tests removed - platform confirmation is now handled by PM LLM,
// not programmatic detection. Platform is set in config when user confirms during bootstrap.

func TestBootstrapDetector_DetectMakefile_Missing(t *testing.T) {
	tmpDir := t.TempDir()

	detector := NewBootstrapDetector(tmpDir)
	needsMakefile, missingTargets := detector.detectMissingMakefile()

	if !needsMakefile {
		t.Error("Expected NeedsMakefile = true when Makefile doesn't exist")
	}

	expectedTargets := []string{"build", "test", "lint", "run"}
	if len(missingTargets) != len(expectedTargets) {
		t.Errorf("Expected %d missing targets, got %d", len(expectedTargets), len(missingTargets))
	}
}

func TestBootstrapDetector_DetectMakefile_Partial(t *testing.T) {
	tmpDir := t.TempDir()
	makefilePath := filepath.Join(tmpDir, "Makefile")

	// Create Makefile with only build and test targets
	content := `build:
	go build

test:
	go test
`
	if err := os.WriteFile(makefilePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create Makefile: %v", err)
	}

	detector := NewBootstrapDetector(tmpDir)
	needsMakefile, missingTargets := detector.detectMissingMakefile()

	if !needsMakefile {
		t.Error("Expected NeedsMakefile = true when required targets are missing")
	}

	// Should be missing lint and run
	if len(missingTargets) != 2 {
		t.Errorf("Expected 2 missing targets, got %d: %v", len(missingTargets), missingTargets)
	}
}

func TestBootstrapDetector_DetectMakefile_Complete(t *testing.T) {
	tmpDir := t.TempDir()
	makefilePath := filepath.Join(tmpDir, "Makefile")

	// Create complete Makefile
	content := `build:
	go build

test:
	go test

lint:
	golangci-lint run

run:
	go run .
`
	if err := os.WriteFile(makefilePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create Makefile: %v", err)
	}

	detector := NewBootstrapDetector(tmpDir)
	needsMakefile, missingTargets := detector.detectMissingMakefile()

	if needsMakefile {
		t.Error("Expected NeedsMakefile = false when all targets present")
	}

	if len(missingTargets) != 0 {
		t.Errorf("Expected no missing targets, got: %v", missingTargets)
	}
}

func TestBootstrapDetector_DetectKnowledgeGraph(t *testing.T) {
	tmpDir := t.TempDir()

	// Initially missing
	detector := NewBootstrapDetector(tmpDir)
	if !detector.detectMissingKnowledgeGraph() {
		t.Error("Expected knowledge graph to be missing initially")
	}

	// Create .maestro directory and knowledge.dot
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro directory: %v", err)
	}

	knowledgePath := filepath.Join(maestroDir, "knowledge.dot")
	if err := os.WriteFile(knowledgePath, []byte("digraph {}"), 0644); err != nil {
		t.Fatalf("Failed to create knowledge.dot: %v", err)
	}

	// Should now be found
	if detector.detectMissingKnowledgeGraph() {
		t.Error("Expected knowledge graph to be found after creation")
	}
}

func TestBootstrapDetector_DetectGitignore_Missing(t *testing.T) {
	tmpDir := t.TempDir()

	detector := NewBootstrapDetector(tmpDir)
	needsGitignore := detector.detectMissingGitignore()

	if !needsGitignore {
		t.Error("Expected NeedsGitignore = true when .gitignore doesn't exist")
	}
}

func TestBootstrapDetector_DetectGitignore_Present(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .gitignore file
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("*.log\n"), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	detector := NewBootstrapDetector(tmpDir)
	needsGitignore := detector.detectMissingGitignore()

	if needsGitignore {
		t.Error("Expected NeedsGitignore = false when .gitignore exists")
	}
}

func TestBootstrapRequirements_NeedsBootstrapGate(t *testing.T) {
	tests := []struct {
		name     string
		reqs     BootstrapRequirements
		expected bool
	}{
		{
			name:     "needs project config",
			reqs:     BootstrapRequirements{NeedsProjectConfig: true, NeedsGitRepo: false},
			expected: true,
		},
		{
			name:     "needs git repo",
			reqs:     BootstrapRequirements{NeedsProjectConfig: false, NeedsGitRepo: true},
			expected: true,
		},
		{
			name:     "needs both",
			reqs:     BootstrapRequirements{NeedsProjectConfig: true, NeedsGitRepo: true},
			expected: true,
		},
		{
			name:     "has both configured",
			reqs:     BootstrapRequirements{NeedsProjectConfig: false, NeedsGitRepo: false},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reqs.NeedsBootstrapGate(); got != tt.expected {
				t.Errorf("NeedsBootstrapGate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBootstrapRequirements_HasAnyMissingComponents(t *testing.T) {
	tests := []struct {
		name     string
		reqs     BootstrapRequirements
		expected bool
	}{
		{
			name:     "no missing components",
			reqs:     BootstrapRequirements{MissingComponents: []string{}},
			expected: false,
		},
		{
			name:     "has missing components",
			reqs:     BootstrapRequirements{MissingComponents: []string{"Dockerfile", "Makefile"}},
			expected: true,
		},
		{
			name:     "nil missing components",
			reqs:     BootstrapRequirements{MissingComponents: nil},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reqs.HasAnyMissingComponents(); got != tt.expected {
				t.Errorf("HasAnyMissingComponents() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDriver_IsDemoAvailable(t *testing.T) {
	// Demo requires: working container (valid or fallback) + Makefile with run target
	tests := []struct {
		name         string
		reqs         *BootstrapRequirements
		expectedDemo bool
	}{
		{
			name: "demo available with valid container and makefile",
			reqs: &BootstrapRequirements{
				ContainerStatus: ContainerStatus{HasValidContainer: true},
				NeedsMakefile:   false,
			},
			expectedDemo: true,
		},
		{
			name: "demo available with fallback container and makefile",
			reqs: &BootstrapRequirements{
				ContainerStatus: ContainerStatus{IsBootstrapFallback: true},
				NeedsMakefile:   false,
			},
			expectedDemo: true,
		},
		{
			name: "demo unavailable when makefile missing",
			reqs: &BootstrapRequirements{
				ContainerStatus: ContainerStatus{HasValidContainer: true},
				NeedsMakefile:   true,
			},
			expectedDemo: false,
		},
		{
			name: "demo unavailable when no container",
			reqs: &BootstrapRequirements{
				ContainerStatus: ContainerStatus{HasValidContainer: false, IsBootstrapFallback: false},
				NeedsMakefile:   false,
			},
			expectedDemo: false,
		},
		{
			name: "demo available even with gitignore missing",
			reqs: &BootstrapRequirements{
				ContainerStatus:   ContainerStatus{HasValidContainer: true},
				NeedsMakefile:     false,
				NeedsGitignore:    true,
				MissingComponents: []string{".gitignore"},
			},
			expectedDemo: true, // gitignore doesn't block demo
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal driver for testing
			d := &Driver{}

			// Update demo availability
			d.updateDemoAvailable(tt.reqs)

			// Check demo availability
			if got := d.IsDemoAvailable(); got != tt.expectedDemo {
				t.Errorf("IsDemoAvailable() = %v, want %v", got, tt.expectedDemo)
			}
		})
	}
}

func TestDriver_UpdateDemoAvailable_NilRequirements(t *testing.T) {
	d := &Driver{demoAvailable: true}

	// Should not panic and should not change state when reqs is nil
	d.updateDemoAvailable(nil)

	if !d.IsDemoAvailable() {
		t.Error("Expected demoAvailable to remain unchanged when reqs is nil")
	}
}

func TestBootstrapRequirements_ToRequirementIDs(t *testing.T) {
	tests := []struct {
		name        string
		reqs        *BootstrapRequirements
		expectIDs   []workspace.BootstrapRequirementID
		expectCount int
	}{
		{
			name: "empty requirements returns empty slice",
			reqs: &BootstrapRequirements{
				ContainerStatus: ContainerStatus{
					HasValidContainer:   true,
					IsBootstrapFallback: false,
				},
			},
			expectIDs:   []workspace.BootstrapRequirementID{},
			expectCount: 0,
		},
		{
			name: "container fallback without valid container",
			reqs: &BootstrapRequirements{
				ContainerStatus: ContainerStatus{
					HasValidContainer:   false,
					IsBootstrapFallback: true,
				},
			},
			expectIDs:   []workspace.BootstrapRequirementID{workspace.BootstrapReqContainer},
			expectCount: 1,
		},
		{
			name: "container fallback with valid container - no requirement",
			reqs: &BootstrapRequirements{
				ContainerStatus: ContainerStatus{
					HasValidContainer:   true,
					IsBootstrapFallback: true,
				},
			},
			expectIDs:   []workspace.BootstrapRequirementID{},
			expectCount: 0,
		},
		{
			name: "needs dockerfile only",
			reqs: &BootstrapRequirements{
				NeedsDockerfile: true,
				ContainerStatus: ContainerStatus{
					HasValidContainer: true,
				},
			},
			expectIDs:   []workspace.BootstrapRequirementID{workspace.BootstrapReqDockerfile},
			expectCount: 1,
		},
		{
			name: "needs makefile only",
			reqs: &BootstrapRequirements{
				NeedsMakefile: true,
				ContainerStatus: ContainerStatus{
					HasValidContainer: true,
				},
			},
			expectIDs:   []workspace.BootstrapRequirementID{workspace.BootstrapReqBuildSystem},
			expectCount: 1,
		},
		{
			name: "needs knowledge graph only",
			reqs: &BootstrapRequirements{
				NeedsKnowledgeGraph: true,
				ContainerStatus: ContainerStatus{
					HasValidContainer: true,
				},
			},
			expectIDs:   []workspace.BootstrapRequirementID{workspace.BootstrapReqKnowledgeGraph},
			expectCount: 1,
		},
		{
			name: "needs git repo only",
			reqs: &BootstrapRequirements{
				NeedsGitRepo: true,
				ContainerStatus: ContainerStatus{
					HasValidContainer: true,
				},
			},
			expectIDs:   []workspace.BootstrapRequirementID{workspace.BootstrapReqGitAccess},
			expectCount: 1,
		},
		{
			name: "multiple requirements",
			reqs: &BootstrapRequirements{
				NeedsDockerfile:     true,
				NeedsMakefile:       true,
				NeedsKnowledgeGraph: true,
				ContainerStatus: ContainerStatus{
					HasValidContainer:   false,
					IsBootstrapFallback: true,
				},
			},
			expectIDs: []workspace.BootstrapRequirementID{
				workspace.BootstrapReqContainer,
				workspace.BootstrapReqDockerfile,
				workspace.BootstrapReqBuildSystem,
				workspace.BootstrapReqKnowledgeGraph,
			},
			expectCount: 4,
		},
		{
			name: "all requirements",
			reqs: &BootstrapRequirements{
				NeedsDockerfile:     true,
				NeedsMakefile:       true,
				NeedsKnowledgeGraph: true,
				NeedsGitRepo:        true,
				ContainerStatus: ContainerStatus{
					HasValidContainer:   false,
					IsBootstrapFallback: true,
				},
			},
			expectIDs: []workspace.BootstrapRequirementID{
				workspace.BootstrapReqContainer,
				workspace.BootstrapReqDockerfile,
				workspace.BootstrapReqBuildSystem,
				workspace.BootstrapReqKnowledgeGraph,
				workspace.BootstrapReqGitAccess,
			},
			expectCount: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := tt.reqs.ToRequirementIDs()

			if len(ids) != tt.expectCount {
				t.Errorf("ToRequirementIDs() returned %d IDs, want %d", len(ids), tt.expectCount)
			}

			// Verify all expected IDs are present (order may vary)
			for _, expectedID := range tt.expectIDs {
				found := false
				for _, id := range ids {
					if id == expectedID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ToRequirementIDs() missing expected ID: %s", expectedID)
				}
			}

			// Verify no unexpected IDs
			for _, id := range ids {
				found := false
				for _, expectedID := range tt.expectIDs {
					if id == expectedID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ToRequirementIDs() returned unexpected ID: %s", id)
				}
			}
		})
	}
}

func TestBootstrapRequirements_ToRequirementIDs_Idempotent(t *testing.T) {
	// Verify that calling ToRequirementIDs multiple times returns same results
	reqs := &BootstrapRequirements{
		NeedsDockerfile:     true,
		NeedsMakefile:       true,
		NeedsKnowledgeGraph: true,
		ContainerStatus: ContainerStatus{
			HasValidContainer:   false,
			IsBootstrapFallback: true,
		},
	}

	ids1 := reqs.ToRequirementIDs()
	ids2 := reqs.ToRequirementIDs()

	if len(ids1) != len(ids2) {
		t.Errorf("ToRequirementIDs() not idempotent: got %d and %d IDs", len(ids1), len(ids2))
	}

	for i := range ids1 {
		if ids1[i] != ids2[i] {
			t.Errorf("ToRequirementIDs() not idempotent at index %d: %s vs %s", i, ids1[i], ids2[i])
		}
	}
}

func TestBootstrapRequirements_ToRequirementIDs_AllIDsAreValid(t *testing.T) {
	// Verify all returned IDs pass validation
	reqs := &BootstrapRequirements{
		NeedsDockerfile:     true,
		NeedsMakefile:       true,
		NeedsKnowledgeGraph: true,
		NeedsGitRepo:        true,
		ContainerStatus: ContainerStatus{
			HasValidContainer:   false,
			IsBootstrapFallback: true,
		},
	}

	ids := reqs.ToRequirementIDs()

	for _, id := range ids {
		if !workspace.IsValidRequirementID(id) {
			t.Errorf("ToRequirementIDs() returned invalid ID: %s", id)
		}
	}
}

func TestBootstrapRequirements_ToBootstrapFailures(t *testing.T) {
	tests := []struct {
		name        string
		reqs        *BootstrapRequirements
		expectCount int
		expectTypes []string
		expectPrio1 bool // expect priority 1 failure
	}{
		{
			name:        "empty requirements",
			reqs:        &BootstrapRequirements{},
			expectCount: 0,
			expectTypes: []string{},
			expectPrio1: false,
		},
		{
			name: "dockerfile only",
			reqs: &BootstrapRequirements{
				NeedsDockerfile: true,
			},
			expectCount: 1,
			expectTypes: []string{"container"},
			expectPrio1: true,
		},
		{
			name: "makefile only",
			reqs: &BootstrapRequirements{
				NeedsMakefile:     true,
				NeedsBuildTargets: []string{"build", "test"},
			},
			expectCount: 1,
			expectTypes: []string{"build_system"},
			expectPrio1: false,
		},
		{
			name: "all requirements",
			reqs: &BootstrapRequirements{
				NeedsDockerfile:     true,
				NeedsMakefile:       true,
				NeedsKnowledgeGraph: true,
				NeedsGitignore:      true,
				NeedsClaudeCode:     true,
			},
			expectCount: 5,
			expectTypes: []string{"container", "build_system", "infrastructure", "build_system", "claude_code"},
			expectPrio1: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := tt.reqs.ToBootstrapFailures()

			if len(failures) != tt.expectCount {
				t.Errorf("ToBootstrapFailures() returned %d failures, want %d", len(failures), tt.expectCount)
			}

			// Check types
			for i, f := range failures {
				if i < len(tt.expectTypes) {
					if string(f.Type) != tt.expectTypes[i] {
						t.Errorf("Failure %d type = %s, want %s", i, f.Type, tt.expectTypes[i])
					}
				}
			}

			// Check priority 1
			hasPrio1 := false
			for _, f := range failures {
				if f.Priority == 1 {
					hasPrio1 = true
					break
				}
			}
			if hasPrio1 != tt.expectPrio1 {
				t.Errorf("Has priority 1 = %v, want %v", hasPrio1, tt.expectPrio1)
			}
		})
	}
}
