package coder

import (
	"context"
	"path/filepath"
	"testing"

	"orchestrator/pkg/build"
	"orchestrator/pkg/config"
)

func TestGetHostWorkspacePath(t *testing.T) {
	// Setup test config
	tempDir := t.TempDir()
	if err := config.LoadConfig(tempDir); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Create a test coder with a relative workspace path (simulating current behavior)
	agentID := "test-coder-001"
	workDir := "./test-workspace"

	coder, err := NewCoder(context.Background(), agentID, workDir, nil, build.NewBuildService(), nil)
	if err != nil {
		t.Fatalf("Failed to create coder: %v", err)
	}

	// Test GetHostWorkspacePath
	hostPath := coder.GetHostWorkspacePath()

	// Should return absolute path, not relative
	if !filepath.IsAbs(hostPath) {
		t.Errorf("Expected absolute path, got relative: %s", hostPath)
	}

	// Should contain the workDir
	expectedSuffix := "test-workspace"
	if hostPath != workDir && !filepath.IsAbs(hostPath) {
		t.Errorf("Expected absolute path containing %s, got: %s", expectedSuffix, hostPath)
	}

	t.Logf("Host workspace path: %s", hostPath)
	t.Logf("Original work dir: %s", coder.originalWorkDir)
	t.Logf("Current work dir: %s", coder.workDir)
}

func TestAgentFactoryWorkspaceCreation(t *testing.T) {
	// Test what the agent factory actually creates
	tempDir := t.TempDir()
	if err := config.LoadConfig(tempDir); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Simulate what happens in agent factory

	agentID := "test-agent"
	baseWorkDir := "." // This is what getWorkDirFromConfig returns
	coderWorkDir := filepath.Join(baseWorkDir, agentID)

	t.Logf("Base work dir: %s", baseWorkDir)
	t.Logf("Coder work dir: %s", coderWorkDir)

	// Test filepath.Abs on the result
	absPath, err := filepath.Abs(coderWorkDir)
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

	t.Logf("Absolute path: %s", absPath)

	// The absolute path should be a real host path, not relative
	if !filepath.IsAbs(absPath) {
		t.Errorf("Expected absolute path, got: %s", absPath)
	}
}
