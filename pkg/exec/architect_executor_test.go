package exec

import (
	"testing"
)

func TestNewArchitectExecutor(t *testing.T) {
	executor := NewArchitectExecutor("test-image:latest", "/tmp/project", 3)

	if executor == nil {
		t.Fatal("NewArchitectExecutor returned nil")
	}

	if executor.maxCoders != 3 {
		t.Errorf("expected maxCoders=3, got %d", executor.maxCoders)
	}

	if executor.maxHotfixers != 1 {
		t.Errorf("expected maxHotfixers=1 (default), got %d", executor.maxHotfixers)
	}

	if executor.image != "test-image:latest" {
		t.Errorf("expected image='test-image:latest', got %s", executor.image)
	}

	if executor.projectDir != "/tmp/project" {
		t.Errorf("expected projectDir='/tmp/project', got %s", executor.projectDir)
	}
}

func TestArchitectExecutorMountsHotfixWorkspaces(t *testing.T) {
	// This test verifies that the executor is configured to mount hotfix workspaces
	// The actual mount happens in Start(), but we verify the configuration is correct
	executor := NewArchitectExecutor("test-image:latest", "/tmp/project", 3)

	// Verify hotfix configuration exists
	if executor.maxHotfixers < 1 {
		t.Errorf("architect executor should have at least 1 hotfix workspace configured, got %d", executor.maxHotfixers)
	}

	// Verify both coder and hotfix counts are set
	if executor.maxCoders == 0 {
		t.Error("architect executor should have coder workspaces configured")
	}

	t.Logf("Architect executor configured with %d coders and %d hotfixers", executor.maxCoders, executor.maxHotfixers)
}
