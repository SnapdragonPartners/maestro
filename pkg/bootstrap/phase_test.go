package bootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// initTestGitRepo initializes a git repository in the given directory.
func initTestGitRepo(dir string) error {
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return err
	}

	// Configure git for the test
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return err
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return err
	}

	// Create initial commit
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return err
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = dir
	return cmd.Run()
}

func TestPhaseExecution(t *testing.T) {
	// Create temporary directory for test.
	tempDir, err := os.MkdirTemp("", "bootstrap-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal Go project.
	goMod := `module test-project
go 1.21
`
	if writeErr := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644); writeErr != nil {
		t.Fatalf("Failed to create go.mod: %v", writeErr)
	}

	// Initialize a git repository in the temp directory
	if gitErr := initTestGitRepo(tempDir); gitErr != nil {
		t.Fatalf("Failed to initialize git repo: %v", gitErr)
	}

	// Create bootstrap phase.
	config := DefaultConfig()
	config.AutoMerge = false // Disable auto-merge for testing
	config.RepoURL = tempDir // Use local directory as repo URL
	phase := NewPhase(tempDir, config)

	// Execute bootstrap phase.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := phase.Execute(ctx)
	if err != nil {
		t.Fatalf("Bootstrap phase execution failed: %v", err)
	}

	// Verify results.
	if !result.Success {
		t.Errorf("Expected success=true, got %v", result.Success)
	}

	if result.Backend != "go" {
		t.Errorf("Expected backend=go, got %s", result.Backend)
	}

	if len(result.GeneratedFiles) == 0 {
		t.Error("Expected generated files, got none")
	}

	// Log what files were actually generated for debugging
	t.Logf("Generated files: %v", result.GeneratedFiles)

	// Update expected files based on actual implementation (without agent.mk)
	expectedFiles := []string{".gitignore", ".gitattributes", ".editorconfig", "Makefile"}
	for _, expectedFile := range expectedFiles {
		found := false
		for _, generatedFile := range result.GeneratedFiles {
			if generatedFile == expectedFile {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected file %s not found in generated files", expectedFile)
		}

		// Verify file actually exists.
		if _, err := os.Stat(filepath.Join(tempDir, expectedFile)); os.IsNotExist(err) {
			t.Errorf("Generated file %s does not exist on disk", expectedFile)
		}
	}
}

func TestPhaseWithDisabledConfig(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bootstrap-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	config := DefaultConfig()
	config.Enabled = false
	phase := NewPhase(tempDir, config)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := phase.Execute(ctx)
	if err != nil {
		t.Fatalf("Bootstrap phase execution failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success=true for disabled bootstrap, got %v", result.Success)
	}

	if result.Backend != "disabled" {
		t.Errorf("Expected backend=disabled, got %s", result.Backend)
	}

	if len(result.GeneratedFiles) != 0 {
		t.Errorf("Expected no generated files for disabled bootstrap, got %d", len(result.GeneratedFiles))
	}
}

func TestPhaseWithForcedBackend(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bootstrap-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create Node.js project.
	packageJSON := `{
  "name": "test-project",
  "version": "1.0.0"
}`
	if writeErr := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(packageJSON), 0644); writeErr != nil {
		t.Fatalf("Failed to create package.json: %v", writeErr)
	}

	// Initialize git repo
	if gitErr := initTestGitRepo(tempDir); gitErr != nil {
		t.Fatalf("Failed to initialize git repo: %v", gitErr)
	}

	// Force Python backend.
	config := DefaultConfig()
	config.ForceBackend = "python"
	config.AutoMerge = false
	config.RepoURL = tempDir
	phase := NewPhase(tempDir, config)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := phase.Execute(ctx)
	if err != nil {
		t.Fatalf("Bootstrap phase execution failed: %v", err)
	}

	if result.Backend != "python" {
		t.Errorf("Expected forced backend=python, got %s", result.Backend)
	}
}

func TestPhaseWithInvalidForcedBackend(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bootstrap-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal file
	os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("test"), 0644)

	// Initialize git repo
	if gitErr := initTestGitRepo(tempDir); gitErr != nil {
		t.Fatalf("Failed to initialize git repo: %v", gitErr)
	}

	config := DefaultConfig()
	config.ForceBackend = "nonexistent"
	config.RepoURL = tempDir
	phase := NewPhase(tempDir, config)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := phase.Execute(ctx)
	if err == nil {
		t.Error("Expected error for invalid forced backend")
	}

	if result.Success {
		t.Error("Expected success=false for invalid forced backend")
	}
}

func TestPhaseWithSkipMakefile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bootstrap-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal project.
	os.WriteFile(filepath.Join(tempDir, "main.py"), []byte("print('hello')"), 0644)

	// Initialize git repo
	if gitErr := initTestGitRepo(tempDir); gitErr != nil {
		t.Fatalf("Failed to initialize git repo: %v", gitErr)
	}

	config := DefaultConfig()
	config.SkipMakefile = true
	config.AutoMerge = false
	config.RepoURL = tempDir
	phase := NewPhase(tempDir, config)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := phase.Execute(ctx)
	if err != nil {
		t.Fatalf("Bootstrap phase execution failed: %v", err)
	}

	// Log generated files for debugging
	t.Logf("Generated files with skip_makefile=true: %v", result.GeneratedFiles)

	// Verify Makefile was not created.
	makefileFound := false
	for _, file := range result.GeneratedFiles {
		if file == "Makefile" {
			makefileFound = true
		}
	}

	if makefileFound {
		t.Error("Makefile should not be generated when skip_makefile=true")
	}
}

func TestPhaseWithAdditionalArtifacts(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bootstrap-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal Go project.
	goMod := `module test-project
go 1.21
`
	if writeErr := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644); writeErr != nil {
		t.Fatalf("Failed to create go.mod: %v", writeErr)
	}

	// Initialize git repo
	if gitErr := initTestGitRepo(tempDir); gitErr != nil {
		t.Fatalf("Failed to initialize git repo: %v", gitErr)
	}

	config := DefaultConfig()
	config.AdditionalArtifacts = []string{"README.md", "LICENSE", "Dockerfile"}
	config.AutoMerge = false
	config.RepoURL = tempDir
	phase := NewPhase(tempDir, config)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := phase.Execute(ctx)
	if err != nil {
		t.Fatalf("Bootstrap phase execution failed: %v", err)
	}

	// Verify additional artifacts were created.
	expectedAdditional := []string{"README.md", "LICENSE", "Dockerfile"}
	for _, expected := range expectedAdditional {
		found := false
		for _, generated := range result.GeneratedFiles {
			if generated == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected additional artifact %s not found", expected)
		}

		// Verify file exists.
		if _, err := os.Stat(filepath.Join(tempDir, expected)); os.IsNotExist(err) {
			t.Errorf("Additional artifact %s does not exist on disk", expected)
		}
	}
}

func TestPhaseGetStatus(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bootstrap-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	config := DefaultConfig()
	phase := NewPhase(tempDir, config)

	status := phase.GetStatus()

	// Verify status contains expected keys.
	expectedKeys := []string{"enabled", "project_root", "config", "backends"}
	for _, key := range expectedKeys {
		if _, exists := status[key]; !exists {
			t.Errorf("Expected status key %s not found", key)
		}
	}

	// Verify values.
	if status["enabled"] != true {
		t.Errorf("Expected enabled=true, got %v", status["enabled"])
	}

	if status["project_root"] != tempDir {
		t.Errorf("Expected project_root=%s, got %v", tempDir, status["project_root"])
	}
}
