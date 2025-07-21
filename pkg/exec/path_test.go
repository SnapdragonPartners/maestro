package exec

import (
	"runtime"
	"testing"

	"orchestrator/pkg/logx"
)

func TestNormalizePathMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test is macOS-specific")
	}

	logger := logx.NewLogger("test")
	exec := &LongRunningDockerExec{logger: logger}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Users path",
			input:    "/Users/dratner/Code/maestro-work/test/claude_sonnet4-001",
			expected: "/Users/dratner/Code/maestro-work/test/claude_sonnet4-001",
		},
		{
			name:     "path with relative components",
			input:    "/Users/dratner/../dratner/Code/maestro-work/test",
			expected: "/Users/dratner/Code/maestro-work/test",
		},
		{
			name:     "tmp path",
			input:    "/tmp/test-workspace",
			expected: "/tmp/test-workspace",
		},
		{
			name:     "var folders path",
			input:    "/var/folders/xx/test",
			expected: "/var/folders/xx/test",
		},
		{
			name:     "unsupported path",
			input:    "/opt/unsupported",
			expected: "/opt/unsupported", // Still returns the path but logs warning
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exec.normalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNormalizePathWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("This test is Windows-specific")
	}

	logger := logx.NewLogger("test")
	exec := &LongRunningDockerExec{logger: logger}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "C drive path",
			input:    "C:\\Users\\test\\workspace",
			expected: "/c/Users/test/workspace",
		},
		{
			name:     "D drive path",
			input:    "D:\\projects\\test",
			expected: "/d/projects/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exec.normalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNormalizePathLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("This test is Linux-specific")
	}

	logger := logx.NewLogger("test")
	exec := &LongRunningDockerExec{logger: logger}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard Linux path",
			input:    "/home/user/workspace",
			expected: "/home/user/workspace",
		},
		{
			name:     "path with relative components",
			input:    "/home/user/../user/workspace",
			expected: "/home/user/workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exec.normalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
