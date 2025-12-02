package github

import (
	"testing"
	"time"
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
			name:      "Real repo SSH",
			url:       "git@github.com:anthropics/claude-code.git",
			wantOwner: "anthropics",
			wantRepo:  "claude-code",
			wantErr:   false,
		},
		{
			name:      "Real repo HTTPS",
			url:       "https://github.com/anthropics/claude-code.git",
			wantOwner: "anthropics",
			wantRepo:  "claude-code",
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
			name:    "Unsupported format - Bitbucket",
			url:     "git@bitbucket.org:owner/repo.git",
			wantErr: true,
		},
		{
			name:    "Empty string",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := ParseGitHubURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseGitHubURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if owner != tt.wantOwner {
					t.Errorf("ParseGitHubURL() owner = %v, want %v", owner, tt.wantOwner)
				}
				if repo != tt.wantRepo {
					t.Errorf("ParseGitHubURL() repo = %v, want %v", repo, tt.wantRepo)
				}
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	client := NewClient("test-owner", "test-repo")

	if client == nil {
		t.Fatal("NewClient returned nil")
	}

	if client.Owner() != "test-owner" {
		t.Errorf("Owner() = %v, want %v", client.Owner(), "test-owner")
	}

	if client.Repo() != "test-repo" {
		t.Errorf("Repo() = %v, want %v", client.Repo(), "test-repo")
	}

	if client.RepoPath() != "test-owner/test-repo" {
		t.Errorf("RepoPath() = %v, want %v", client.RepoPath(), "test-owner/test-repo")
	}

	if client.logger == nil {
		t.Error("logger is nil")
	}

	if client.timeout != 30*time.Second {
		t.Errorf("timeout = %v, want %v", client.timeout, 30*time.Second)
	}
}

func TestNewClientFromRemote(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "Valid SSH URL",
			url:       "git@github.com:owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "Valid HTTPS URL",
			url:       "https://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:    "Invalid URL",
			url:     "not-a-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClientFromRemote(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClientFromRemote() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if client.Owner() != tt.wantOwner {
					t.Errorf("Owner() = %v, want %v", client.Owner(), tt.wantOwner)
				}
				if client.Repo() != tt.wantRepo {
					t.Errorf("Repo() = %v, want %v", client.Repo(), tt.wantRepo)
				}
			}
		})
	}
}

func TestWithTimeout(t *testing.T) {
	client := NewClient("owner", "repo")
	newClient := client.WithTimeout(5 * time.Minute)

	// Original should be unchanged
	if client.timeout != 30*time.Second {
		t.Errorf("original timeout changed: %v", client.timeout)
	}

	// New client should have new timeout
	if newClient.timeout != 5*time.Minute {
		t.Errorf("new timeout = %v, want %v", newClient.timeout, 5*time.Minute)
	}

	// Other fields should be copied
	if newClient.owner != client.owner || newClient.repo != client.repo {
		t.Error("owner/repo not copied correctly")
	}
}
