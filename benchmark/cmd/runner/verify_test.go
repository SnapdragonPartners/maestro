package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRepoAtHead lays down a one-commit git repo and returns its dir. cmdVerify
// resolves the solution as the workspace HEAD, so a real commit is required.
func gitRepoAtHead(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@t"},
		{"add", "-A"}, {"commit", "-q", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// A minimal v1 story whose single check passes iff x.txt exists. The fixture
// commit is a placeholder; files_changed_within is not exercised here.
func writeVerifyStory(t *testing.T, check string) string {
	t.Helper()
	body := `schema_version = 1
id = "verify-fixture"
title = "t"
level = "story"
[fixture]
repo = "https://example.invalid/r"
commit = "0123456789012345678901234567890123456789"
base_branch = "main"
[prompt]
text = "t"
[expectations]
allowed_paths = ["x.txt"]
required_artifacts = ["pr"]
evidence_shape = ["diff"]
[[validators]]
name = "true"
command = "true"
` + check + `
[budget]
max_tokens = 1000
max_wall_clock_seconds = 60
max_cost_usd = 1.0
`
	p := filepath.Join(t.TempDir(), "story.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const passCheck = `[[checks]]
name = "present"
type = "command"
command = "test -f x.txt"`

const failCheck = `[[checks]]
name = "missing"
type = "command"
command = "test -f nope.txt"`

func TestCmdVerifyExitCodes(t *testing.T) {
	tests := []struct {
		name    string
		check   string
		wantErr error
	}{
		{"all pass exits nil", passCheck, nil},
		{"failing check returns the verification sentinel", failCheck, errVerificationFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := gitRepoAtHead(t, map[string]string{"x.txt": "hi"})
			storyPath := writeVerifyStory(t, tt.check)
			err := cmdVerify(context.Background(), []string{"--story", storyPath, "--workspace", ws})
			if tt.wantErr == nil && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCmdVerifyRequiresFlags(t *testing.T) {
	if err := cmdVerify(context.Background(), []string{"--story", "x.toml"}); err == nil {
		t.Fatal("missing --workspace must error")
	}
	if err := cmdVerify(context.Background(), []string{"--workspace", "/tmp"}); err == nil {
		t.Fatal("missing --story must error")
	}
}
