package pm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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

func TestBootstrapDetector_DetectPlatform_Go(t *testing.T) {
	// Create a temporary directory with go.mod
	tmpDir := t.TempDir()
	goModPath := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module test\n"), 0644); err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	detector := NewBootstrapDetector(tmpDir)
	reqs, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if reqs.DetectedPlatform != "go" {
		t.Errorf("Expected platform = go, got %s", reqs.DetectedPlatform)
	}

	if reqs.PlatformConfidence < 0.8 {
		t.Errorf("Expected high confidence for go.mod, got %.2f", reqs.PlatformConfidence)
	}
}

func TestBootstrapDetector_DetectPlatform_Python(t *testing.T) {
	// Create a temporary directory with pyproject.toml
	tmpDir := t.TempDir()
	pyprojectPath := filepath.Join(tmpDir, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte("[build-system]\n"), 0644); err != nil {
		t.Fatalf("Failed to create pyproject.toml: %v", err)
	}

	detector := NewBootstrapDetector(tmpDir)
	reqs, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if reqs.DetectedPlatform != "python" {
		t.Errorf("Expected platform = python, got %s", reqs.DetectedPlatform)
	}

	if reqs.PlatformConfidence < 0.8 {
		t.Errorf("Expected high confidence for pyproject.toml, got %.2f", reqs.PlatformConfidence)
	}
}

func TestBootstrapDetector_DetectPlatform_Node(t *testing.T) {
	// Create a temporary directory with package.json
	tmpDir := t.TempDir()
	packagePath := filepath.Join(tmpDir, "package.json")
	if err := os.WriteFile(packagePath, []byte("{\"name\": \"test\"}\n"), 0644); err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	detector := NewBootstrapDetector(tmpDir)
	reqs, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if reqs.DetectedPlatform != "node" {
		t.Errorf("Expected platform = node, got %s", reqs.DetectedPlatform)
	}

	if reqs.PlatformConfidence < 0.8 {
		t.Errorf("Expected high confidence for package.json, got %.2f", reqs.PlatformConfidence)
	}
}

func TestBootstrapDetector_DetectPlatform_Generic(t *testing.T) {
	// Create a temporary directory with no platform indicators
	tmpDir := t.TempDir()

	detector := NewBootstrapDetector(tmpDir)
	reqs, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if reqs.DetectedPlatform != "generic" {
		t.Errorf("Expected platform = generic, got %s", reqs.DetectedPlatform)
	}

	if reqs.PlatformConfidence > 0.5 {
		t.Errorf("Expected low confidence for unknown platform, got %.2f", reqs.PlatformConfidence)
	}
}

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
	tests := []struct {
		name            string
		reqs            *BootstrapRequirements
		expectedDemo    bool
		expectedMissing bool
	}{
		{
			name: "demo available when no missing components",
			reqs: &BootstrapRequirements{
				MissingComponents: []string{},
			},
			expectedDemo:    true,
			expectedMissing: false,
		},
		{
			name: "demo unavailable when components missing",
			reqs: &BootstrapRequirements{
				MissingComponents: []string{"Dockerfile", "Makefile"},
			},
			expectedDemo:    false,
			expectedMissing: true,
		},
		{
			name: "demo unavailable with nil components",
			reqs: &BootstrapRequirements{
				MissingComponents: nil,
			},
			expectedDemo:    true, // nil slice means no missing components
			expectedMissing: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal driver for testing
			d := &Driver{}

			// Verify HasAnyMissingComponents matches expected
			if got := tt.reqs.HasAnyMissingComponents(); got != tt.expectedMissing {
				t.Errorf("HasAnyMissingComponents() = %v, want %v", got, tt.expectedMissing)
			}

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
