package runtime

import (
	"context"
	"strings"
	"testing"
)

// mockDocker implements the Docker interface for testing.
type mockDocker struct {
	installedFiles map[string][]byte
	execCommands   []string
}

func newMockDocker() *mockDocker {
	return &mockDocker{
		installedFiles: make(map[string][]byte),
		execCommands:   make([]string, 0),
	}
}

func (m *mockDocker) Exec(_ context.Context, _ string, args ...string) ([]byte, error) {
	m.execCommands = append(m.execCommands, strings.Join(args, " "))
	return []byte("mock exec output"), nil
}

func (m *mockDocker) CpToContainer(_ context.Context, _, dstPath string, data []byte, _ int) error {
	m.installedFiles[dstPath] = data
	return nil
}

func TestInstallAndRunGHInit(t *testing.T) {
	ctx := context.Background()
	mockDoc := newMockDocker()

	testScript := []byte("#!/bin/sh\necho 'test script'")
	testRepoURL := "https://github.com/test/repo.git"
	testCID := "test-container-123"

	// Test the installation and execution
	err := InstallAndRunGHInit(ctx, mockDoc, testCID, testRepoURL, testScript)
	if err != nil {
		t.Fatalf("InstallAndRunGHInit failed: %v", err)
	}

	// Verify script was installed
	installedScript, exists := mockDoc.installedFiles["/tmp/gh-init"]
	if !exists {
		t.Fatalf("Script was not installed at /tmp/gh-init")
	}

	if string(installedScript) != string(testScript) {
		t.Errorf("Installed script content mismatch.\nExpected: %s\nGot: %s",
			string(testScript), string(installedScript))
	}

	// Verify script was executed with correct repo URL
	if len(mockDoc.execCommands) != 1 {
		t.Fatalf("Expected 1 exec command, got %d: %v", len(mockDoc.execCommands), mockDoc.execCommands)
	}

	expectedCmd := `sh -lc REPO_URL="https://github.com/test/repo.git" /tmp/gh-init`
	if mockDoc.execCommands[0] != expectedCmd {
		t.Errorf("Exec command mismatch.\nExpected: %s\nGot: %s",
			expectedCmd, mockDoc.execCommands[0])
	}
}
