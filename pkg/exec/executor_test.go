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
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != len(envVars) {
		t.Fatalf("expected %d lines, got %d: %v", len(envVars), len(lines), lines)
	}
	for i, want := range envVars {
		if lines[i] != want {
			t.Errorf("line %d: got %q, want %q", i, lines[i], want)
		}
	}
}

func TestWriteEnvFile_RejectsNewlines(t *testing.T) {
	tests := []struct {
		name string
		env  string
	}{
		{"newline in value", "FOO=bar\nbaz"},
		{"carriage return", "FOO=bar\rbaz"},
		{"null byte", "FOO=bar\x00baz"},
		{"newline in key", "FO\nO=bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := WriteEnvFile([]string{tt.env})
			if err == nil {
				_ = os.Remove(path)
				t.Fatal("expected error for env var with invalid characters")
			}
			if !strings.Contains(err.Error(), "invalid characters") {
				t.Errorf("expected 'invalid characters' error, got: %v", err)
			}
		})
	}
}
