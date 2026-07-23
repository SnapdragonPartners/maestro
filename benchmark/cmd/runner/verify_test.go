package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// boundWorkspace builds a repo with a base commit (the pin) and a solution
// commit on top, and returns the dir and the base commit SHA. The solution
// adds x.txt so a `test -f x.txt` check passes. The pin is a real ancestor of
// HEAD and the worktree is clean — a valid bound workspace.
func boundWorkspace(t *testing.T) (dir, pin string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.name", "t")
	git(t, dir, "config", "user.email", "t@t")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	pin = git(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("solution"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "solution")
	return dir, pin
}

// writeVerifyStory writes a v1 story pinned at `pin` with the given check.
func writeVerifyStory(t *testing.T, pin, check string) string {
	t.Helper()
	body := `schema_version = 1
id = "verify-fixture"
title = "t"
level = "story"
[fixture]
repo = "https://example.invalid/r"
commit = "` + pin + `"
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
			ws, pin := boundWorkspace(t)
			storyPath := writeVerifyStory(t, pin, tt.check)
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

// TestCmdVerifyRejectsUnboundWorkspace covers the binding contract: a pin that
// is not an ancestor of HEAD, and a dirty worktree, are rejected BEFORE any
// check runs (so a red would be a binding failure, not a story failure).
func TestCmdVerifyRejectsUnboundWorkspace(t *testing.T) {
	t.Run("pin not in workspace history", func(t *testing.T) {
		ws, _ := boundWorkspace(t)
		// A syntactically valid but absent commit.
		bogus := "0123456789012345678901234567890123456789"
		storyPath := writeVerifyStory(t, bogus, passCheck)
		err := cmdVerify(context.Background(), []string{"--story", storyPath, "--workspace", ws})
		if err == nil || errors.Is(err, errVerificationFailed) {
			t.Fatalf("expected a binding error for an absent pin, got %v", err)
		}
	})

	t.Run("pin present but not an ancestor of HEAD", func(t *testing.T) {
		ws, pin := boundWorkspace(t)
		// Create a divergent commit on a new branch; its HEAD does not descend
		// from the story's pin's *sibling*. Simulate by re-pinning the story to
		// a commit made on an unrelated orphan branch.
		git(t, ws, "checkout", "-q", "--orphan", "orphan")
		if err := os.WriteFile(filepath.Join(ws, "y.txt"), []byte("y"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, ws, "add", "-A")
		git(t, ws, "commit", "-q", "-m", "orphan")
		// HEAD is now the orphan commit; pin (from the original branch) is not
		// its ancestor.
		storyPath := writeVerifyStory(t, pin, passCheck)
		err := cmdVerify(context.Background(), []string{"--story", storyPath, "--workspace", ws})
		if err == nil || errors.Is(err, errVerificationFailed) {
			t.Fatalf("expected a non-ancestor binding error, got %v", err)
		}
		if !strings.Contains(err.Error(), "ancestor") {
			t.Errorf("error should mention ancestry: %v", err)
		}
	})

	t.Run("dirty worktree", func(t *testing.T) {
		ws, pin := boundWorkspace(t)
		// Uncommitted modification.
		if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("dirtied"), 0o644); err != nil {
			t.Fatal(err)
		}
		storyPath := writeVerifyStory(t, pin, passCheck)
		err := cmdVerify(context.Background(), []string{"--story", storyPath, "--workspace", ws})
		if err == nil || errors.Is(err, errVerificationFailed) {
			t.Fatalf("expected a dirty-worktree error, got %v", err)
		}
		if !strings.Contains(err.Error(), "clean") {
			t.Errorf("error should mention cleanliness: %v", err)
		}
	})

	t.Run("untracked file present", func(t *testing.T) {
		ws, pin := boundWorkspace(t)
		if err := os.WriteFile(filepath.Join(ws, "stray.txt"), []byte("stray"), 0o644); err != nil {
			t.Fatal(err)
		}
		storyPath := writeVerifyStory(t, pin, passCheck)
		err := cmdVerify(context.Background(), []string{"--story", storyPath, "--workspace", ws})
		if err == nil || errors.Is(err, errVerificationFailed) {
			t.Fatalf("expected an untracked-file error, got %v", err)
		}
	})

	// Ignored state is what the engine's `git clean -fdx` specifically removes;
	// an ignored file left in the workspace can help a validator pass outside
	// the bound commit, so it must be rejected too. Plain `git status
	// --porcelain` would NOT show it (it is ignored) — only `--ignored` does.
	t.Run("ignored file present", func(t *testing.T) {
		ws, pin := boundWorkspace(t)
		if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, ws, "add", "-A")
		git(t, ws, "commit", "-q", "-m", "gitignore") // committed, so the worktree is otherwise clean
		if err := os.WriteFile(filepath.Join(ws, "stray.log"), []byte("ignored"), 0o644); err != nil {
			t.Fatal(err)
		}
		storyPath := writeVerifyStory(t, pin, passCheck)
		err := cmdVerify(context.Background(), []string{"--story", storyPath, "--workspace", ws})
		if err == nil || errors.Is(err, errVerificationFailed) {
			t.Fatalf("expected an ignored-file error, got %v", err)
		}
		if !strings.Contains(err.Error(), "clean") {
			t.Errorf("error should mention cleanliness: %v", err)
		}
	})
}

func TestCmdVerifyRequiresFlags(t *testing.T) {
	if err := cmdVerify(context.Background(), []string{"--story", "x.toml"}); err == nil {
		t.Fatal("missing --workspace must error")
	}
	if err := cmdVerify(context.Background(), []string{"--workspace", "/tmp"}); err == nil {
		t.Fatal("missing --story must error")
	}
}
