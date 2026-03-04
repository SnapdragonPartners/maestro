package exec

import (
	"os"
	"strings"
	"testing"
)

func TestWriteEnvFile_Empty(t *testing.T) {
	path, err := WriteEnvFile(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Fatalf("expected empty path for nil input, got %q", path)
	}

	path, err = WriteEnvFile([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Fatalf("expected empty path for empty input, got %q", path)
	}
}

func TestWriteEnvFile_WritesAndCleanup(t *testing.T) {
	envVars := []string{"FOO=bar", "SECRET_KEY=hunter2", "EMPTY="}

	path, err := WriteEnvFile(envVars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	defer func() { _ = os.Remove(path) }()

	// Verify file permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat env file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected permissions 0600, got %o", info.Mode().Perm())
	}

	// Verify contents.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(envVars) {
		t.Fatalf("expected %d lines, got %d: %v", len(envVars), len(lines), lines)
	}
	for i, want := range envVars {
		if lines[i] != want {
			t.Errorf("line %d: got %q, want %q", i, lines[i], want)
		}
	}
}
