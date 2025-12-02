package bootstrap

import (
	"testing"
)

func TestParseGitHubURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "SSH format",
			url:       "git@github.com:owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "SSH format without .git",
			url:       "git@github.com:owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "HTTPS format",
			url:       "https://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "HTTPS format without .git",
			url:       "https://github.com/owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:    "Invalid SSH format - missing parts",
			url:     "git@github.com:owner",
			wantErr: true,
		},
		{
			name:    "Invalid HTTPS format - missing parts",
			url:     "https://github.com/owner",
			wantErr: true,
		},
		{
			name:    "Unsupported format - GitLab",
			url:     "https://gitlab.com/owner/repo.git",
			wantErr: true,
		},
		{
			name:    "Unsupported format - random URL",
			url:     "https://example.com/owner/repo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := parseGitHubURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGitHubURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if owner != tt.wantOwner {
					t.Errorf("parseGitHubURL() owner = %v, want %v", owner, tt.wantOwner)
				}
				if repo != tt.wantRepo {
					t.Errorf("parseGitHubURL() repo = %v, want %v", repo, tt.wantRepo)
				}
			}
		})
	}
}

func TestNewGitHubManager(t *testing.T) {
	mgr := NewGitHubManager("test-owner", "test-repo")

	if mgr == nil {
		t.Fatal("NewGitHubManager returned nil")
	}

	if mgr.config.Owner != "test-owner" {
		t.Errorf("Owner = %v, want %v", mgr.config.Owner, "test-owner")
	}

	if mgr.config.Repo != "test-repo" {
		t.Errorf("Repo = %v, want %v", mgr.config.Repo, "test-repo")
	}

	if mgr.logger == nil {
		t.Error("logger is nil")
	}
}
