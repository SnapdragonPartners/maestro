package bootstrap

import (
	"testing"
)

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

	if mgr.client == nil {
		t.Error("client is nil")
	}

	// Verify underlying client has correct owner/repo
	if mgr.Client().Owner() != "test-owner" {
		t.Errorf("Client().Owner() = %v, want %v", mgr.Client().Owner(), "test-owner")
	}

	if mgr.Client().Repo() != "test-repo" {
		t.Errorf("Client().Repo() = %v, want %v", mgr.Client().Repo(), "test-repo")
	}
}

func TestGitHubManagerAccessors(t *testing.T) {
	mgr := NewGitHubManager("test-owner", "test-repo")

	if mgr.Owner() != "test-owner" {
		t.Errorf("Owner() = %v, want %v", mgr.Owner(), "test-owner")
	}

	if mgr.Repo() != "test-repo" {
		t.Errorf("Repo() = %v, want %v", mgr.Repo(), "test-repo")
	}
}
