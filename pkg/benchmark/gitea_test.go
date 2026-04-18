package benchmark

import (
	"testing"
)

func TestSanitizeRepoName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"psf__requests-1234", "psf__requests-1234"},
		{"pandas-dev__pandas-5678", "pandas-dev__pandas-5678"},
		{"simple", "simple"},
		{"UPPER_case", "upper_case"},
		{"has spaces and (parens)", "has-spaces-and--parens-"},
		{"a/b/c", "a-b-c"},
		{"", "instance"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeRepoName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeRepoName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMinInt(t *testing.T) {
	if got := minInt(5, 10); got != 5 {
		t.Errorf("minInt(5, 10) = %d, want 5", got)
	}
	if got := minInt(10, 5); got != 5 {
		t.Errorf("minInt(10, 5) = %d, want 5", got)
	}
	if got := minInt(7, 7); got != 7 {
		t.Errorf("minInt(7, 7) = %d, want 7", got)
	}
}

func TestNewBenchGitea(t *testing.T) {
	g := NewBenchGitea("/tmp/repos")
	if g == nil {
		t.Fatal("NewBenchGitea returned nil")
	}
	if g.ReposDir != "/tmp/repos" {
		t.Errorf("ReposDir = %q, want /tmp/repos", g.ReposDir)
	}
	if g.ContainerName() != "" {
		t.Errorf("ContainerName before EnsureRunning should be empty, got %q", g.ContainerName())
	}
}
