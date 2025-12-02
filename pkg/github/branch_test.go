package github

import (
	"testing"
)

func TestIsProtected(t *testing.T) {
	client := NewClient("owner", "repo")

	tests := []struct {
		name     string
		branch   string
		patterns []string
		want     bool
	}{
		{
			name:     "main is protected",
			branch:   "main",
			patterns: []string{"main", "master", "develop"},
			want:     true,
		},
		{
			name:     "master is protected",
			branch:   "master",
			patterns: []string{"main", "master", "develop"},
			want:     true,
		},
		{
			name:     "develop is protected",
			branch:   "develop",
			patterns: []string{"main", "master", "develop"},
			want:     true,
		},
		{
			name:     "feature branch not protected",
			branch:   "feature/my-feature",
			patterns: []string{"main", "master", "develop"},
			want:     false,
		},
		{
			name:     "release branch with wildcard",
			branch:   "release/v1.0",
			patterns: []string{"main", "release/*"},
			want:     true,
		},
		{
			name:     "release branch with wildcard - nested",
			branch:   "release/v1.0.1",
			patterns: []string{"main", "release/*"},
			want:     true,
		},
		{
			name:     "hotfix branch with wildcard",
			branch:   "hotfix/urgent-fix",
			patterns: []string{"main", "hotfix/*"},
			want:     true,
		},
		{
			name:     "not matching wildcard prefix",
			branch:   "releases/v1.0",
			patterns: []string{"main", "release/*"},
			want:     false,
		},
		{
			name:     "empty patterns",
			branch:   "main",
			patterns: []string{},
			want:     false,
		},
		{
			name:     "empty branch",
			branch:   "",
			patterns: []string{"main"},
			want:     false,
		},
		{
			name:     "glob pattern star",
			branch:   "feature-123",
			patterns: []string{"feature-*"},
			want:     true,
		},
		{
			name:     "glob pattern question mark",
			branch:   "v1",
			patterns: []string{"v?"},
			want:     true,
		},
		{
			name:     "default protected patterns",
			branch:   "main",
			patterns: []string{"main", "master", "develop", "release/*", "hotfix/*"},
			want:     true,
		},
		{
			name:     "default protected - release",
			branch:   "release/2024-01",
			patterns: []string{"main", "master", "develop", "release/*", "hotfix/*"},
			want:     true,
		},
		{
			name:     "default protected - hotfix",
			branch:   "hotfix/security-patch",
			patterns: []string{"main", "master", "develop", "release/*", "hotfix/*"},
			want:     true,
		},
		{
			name:     "story branch not protected",
			branch:   "story-001-add-feature",
			patterns: []string{"main", "master", "develop", "release/*", "hotfix/*"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.isProtected(tt.branch, tt.patterns)
			if got != tt.want {
				t.Errorf("isProtected(%q, %v) = %v, want %v", tt.branch, tt.patterns, got, tt.want)
			}
		})
	}
}
