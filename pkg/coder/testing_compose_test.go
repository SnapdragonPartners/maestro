package coder

import (
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/demo"
)

func TestComposeFileExists(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Test when compose file does not exist
	if demo.ComposeFileExists(tmpDir) {
		t.Error("expected ComposeFileExists to return false for empty directory")
	}

	// Create .maestro directory and compose.yml
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("failed to create .maestro directory: %v", err)
	}

	composePath := filepath.Join(maestroDir, "compose.yml")
	composeContent := `services:
  db:
    image: postgres:15
    ports:
      - "5432:5432"
`
	if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
		t.Fatalf("failed to write compose.yml: %v", err)
	}

	// Test when compose file exists
	if !demo.ComposeFileExists(tmpDir) {
		t.Error("expected ComposeFileExists to return true when compose.yml exists")
	}
}

func TestComposeFilePath(t *testing.T) {
	workspacePath := "/some/workspace"
	expected := "/some/workspace/.maestro/compose.yml"
	actual := demo.ComposeFilePath(workspacePath)

	if actual != expected {
		t.Errorf("ComposeFilePath(%q) = %q, want %q", workspacePath, actual, expected)
	}
}
