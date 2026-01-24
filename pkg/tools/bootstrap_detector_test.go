package tools

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

func TestBootstrapDetector_GetRequiredQuestions_NonTechnical(t *testing.T) {
	detector := NewBootstrapDetector("/tmp")

	ctx := &BootstrapContext{
		Expertise:  "NON_TECHNICAL",
		HasRepo:    false,
		Platform:   "go",
		ProjectDir: "/tmp",
	}

	questions := detector.GetRequiredQuestions(ctx)

	// Non-technical should get minimal questions
	// Should ask about repo (required)
	// Should NOT ask about platform confirmation
	foundRepo := false
	foundPlatform := false

	for _, q := range questions {
		if q.ID == "git_repo" {
			foundRepo = true
		}
		if q.ID == "confirm_platform" {
			foundPlatform = true
		}
	}

	if !foundRepo {
		t.Error("Expected git_repo question for NON_TECHNICAL without repo")
	}

	if foundPlatform {
		t.Error("Did not expect platform confirmation for NON_TECHNICAL")
	}
}

func TestBootstrapDetector_GetRequiredQuestions_Basic(t *testing.T) {
	detector := NewBootstrapDetector("/tmp")

	ctx := &BootstrapContext{
		Expertise:  "BASIC",
		HasRepo:    false,
		Platform:   "python",
		ProjectDir: "/tmp",
	}

	questions := detector.GetRequiredQuestions(ctx)

	// Basic should get git_repo question (platform confirmation handled by PM LLM)
	foundRepo := false

	for _, q := range questions {
		if q.ID == "git_repo" {
			foundRepo = true
		}
	}

	if !foundRepo {
		t.Error("Expected git_repo question for BASIC without repo")
	}
}

func TestBootstrapDetector_GetRequiredQuestions_Expert(t *testing.T) {
	detector := NewBootstrapDetector("/tmp")

	ctx := &BootstrapContext{
		Expertise:         "EXPERT",
		HasRepo:           false,
		HasDockerfile:     false,
		HasKnowledgeGraph: false,
		Platform:          "node",
		ProjectDir:        "/tmp",
	}

	questions := detector.GetRequiredQuestions(ctx)

	// Expert should get questions (platform confirmation handled by PM LLM)
	foundRepo := false
	foundDockerfile := false
	foundPatterns := false

	for _, q := range questions {
		switch q.ID {
		case "git_repo":
			foundRepo = true
		case "custom_dockerfile":
			foundDockerfile = true
		case "initial_patterns":
			foundPatterns = true
		}
	}

	if !foundRepo {
		t.Error("Expected git_repo question for EXPERT without repo")
	}

	if !foundDockerfile {
		t.Error("Expected custom Dockerfile question for EXPERT")
	}

	if !foundPatterns {
		t.Error("Expected initial patterns question for EXPERT")
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

func TestBootstrapDetector_DetectGitignore_InMissingComponents(t *testing.T) {
	tmpDir := t.TempDir()

	detector := NewBootstrapDetector(tmpDir)
	reqs, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if !reqs.NeedsGitignore {
		t.Error("Expected NeedsGitignore = true for empty directory")
	}

	// Verify .gitignore appears in MissingComponents
	found := false
	for _, component := range reqs.MissingComponents {
		if component == ".gitignore file" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected '.gitignore file' in MissingComponents, got: %v", reqs.MissingComponents)
	}
}
