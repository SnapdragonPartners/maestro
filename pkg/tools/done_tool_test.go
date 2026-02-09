package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	execpkg "orchestrator/pkg/exec"
)

// TestDoneTool_NoExecutor verifies the done tool works when no executor is provided (test/schema mode).
func TestDoneTool_NoExecutor(t *testing.T) {
	tool := NewDoneTool(nil, nil, "", "")

	result, err := tool.Exec(context.Background(), map[string]any{
		"summary": "Implemented feature X",
	})
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.ProcessEffect == nil {
		t.Fatal("Expected ProcessEffect to be set")
	}
	if result.ProcessEffect.Signal != SignalTesting {
		t.Errorf("Expected signal %q, got %q", SignalTesting, result.ProcessEffect.Signal)
	}
	if !strings.Contains(result.Content, "skipped git operations") {
		t.Errorf("Expected content to mention skipped git operations, got: %s", result.Content)
	}
}

// TestDoneTool_MissingSummary verifies the done tool rejects missing summary.
func TestDoneTool_MissingSummary(t *testing.T) {
	tool := NewDoneTool(nil, nil, "", "")

	_, err := tool.Exec(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("Expected error for missing summary")
	}
	if !strings.Contains(err.Error(), "summary is required") {
		t.Errorf("Expected 'summary is required' error, got: %v", err)
	}
}

// TestDoneTool_CommitsChanges verifies the done tool commits changes when an executor is available.
// This is an integration test that uses a real git repo in a temp directory.
func TestDoneTool_CommitsChanges(t *testing.T) {
	// Create a temp dir with a real git repo
	repoDir := initTestGitRepo(t)

	// Write a new file (uncommitted)
	testFile := filepath.Join(repoDir, "feature.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Verify file is untracked
	out := gitCmd(t, repoDir, "status", "--porcelain")
	if !strings.Contains(out, "feature.go") {
		t.Fatalf("Expected feature.go in git status, got: %s", out)
	}

	// Create done tool with local executor
	executor := execpkg.NewLocalExec()
	tool := NewDoneTool(nil, executor, repoDir, "STORY-42")

	// Execute done tool
	result, err := tool.Exec(context.Background(), map[string]any{
		"summary": "Added feature.go with main package",
	})
	if err != nil {
		t.Fatalf("Done tool failed: %v", err)
	}

	// Verify ProcessEffect
	if result.ProcessEffect == nil || result.ProcessEffect.Signal != SignalTesting {
		t.Fatal("Expected TESTING signal in ProcessEffect")
	}

	// Verify the file was committed
	out = gitCmd(t, repoDir, "status", "--porcelain")
	if strings.TrimSpace(out) != "" {
		t.Errorf("Expected clean working directory after commit, got: %s", out)
	}

	// Verify commit message includes story ID prefix
	logOut := gitCmd(t, repoDir, "log", "--oneline", "-1")
	if !strings.Contains(logOut, "STORY-42") {
		t.Errorf("Expected commit message to contain 'STORY-42', got: %s", logOut)
	}
	if !strings.Contains(logOut, "Added feature.go") {
		t.Errorf("Expected commit message to contain summary, got: %s", logOut)
	}
}

// TestDoneTool_NothingToCommit verifies the done tool handles no changes gracefully.
func TestDoneTool_NothingToCommit(t *testing.T) {
	repoDir := initTestGitRepo(t)

	executor := execpkg.NewLocalExec()
	tool := NewDoneTool(nil, executor, repoDir, "STORY-1")

	result, err := tool.Exec(context.Background(), map[string]any{
		"summary": "No changes needed",
	})
	if err != nil {
		t.Fatalf("Done tool failed: %v", err)
	}

	if result.ProcessEffect == nil || result.ProcessEffect.Signal != SignalTesting {
		t.Fatal("Expected TESTING signal even with no changes")
	}
	if !strings.Contains(result.Content, "No changes to commit") {
		t.Errorf("Expected 'No changes to commit' message, got: %s", result.Content)
	}
}

// TestDoneTool_CommitMessageWithoutStoryID verifies commit message when no story ID provided.
func TestDoneTool_CommitMessageWithoutStoryID(t *testing.T) {
	repoDir := initTestGitRepo(t)

	// Write a file
	if err := os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	executor := execpkg.NewLocalExec()
	tool := NewDoneTool(nil, executor, repoDir, "") // No story ID

	_, err := tool.Exec(context.Background(), map[string]any{
		"summary": "Added file.txt",
	})
	if err != nil {
		t.Fatalf("Done tool failed: %v", err)
	}

	// Verify commit message is just the summary (no story prefix)
	logOut := gitCmd(t, repoDir, "log", "--oneline", "-1")
	if strings.Contains(logOut, "Story") {
		t.Errorf("Expected no story prefix in commit message, got: %s", logOut)
	}
	if !strings.Contains(logOut, "Added file.txt") {
		t.Errorf("Expected summary in commit message, got: %s", logOut)
	}
}

// TestDoneTool_Definition verifies the tool definition reflects commit behavior.
func TestDoneTool_Definition(t *testing.T) {
	tool := NewDoneTool(nil, nil, "", "")
	def := tool.Definition()

	if def.Name != "done" {
		t.Errorf("Expected name 'done', got %q", def.Name)
	}
	if !strings.Contains(def.Description, "Commit") {
		t.Errorf("Expected description to mention committing, got: %s", def.Description)
	}
	if !strings.Contains(def.Description, "git") {
		t.Errorf("Expected description to mention git, got: %s", def.Description)
	}
}

// TestDoneTool_PromptDocumentation verifies documentation mentions commit behavior.
func TestDoneTool_PromptDocumentation(t *testing.T) {
	tool := NewDoneTool(nil, nil, "", "")
	doc := tool.PromptDocumentation()

	if !strings.Contains(doc, "git add") {
		t.Errorf("Expected documentation to mention 'git add', got: %s", doc)
	}
	if !strings.Contains(doc, "commit message") {
		t.Errorf("Expected documentation to mention 'commit message', got: %s", doc)
	}
}

// TestIsExecutorUsable verifies the typed-nil safety check.
func TestIsExecutorUsable(t *testing.T) {
	// Pure nil
	if isExecutorUsable(nil) {
		t.Error("Expected nil executor to be unusable")
	}

	// Typed nil (the Go gotcha)
	var typedNil *execpkg.LongRunningDockerExec
	if isExecutorUsable(typedNil) {
		t.Error("Expected typed nil executor to be unusable")
	}

	// Real executor
	localExec := execpkg.NewLocalExec()
	if !isExecutorUsable(localExec) {
		t.Error("Expected real executor to be usable")
	}
}

// --- helpers ---

// initTestGitRepo creates a temp directory with an initialized git repo and one initial commit.
func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitCmd(t, dir, "init")
	gitCmd(t, dir, "config", "user.email", "test@test.com")
	gitCmd(t, dir, "config", "user.name", "Test")

	// Create initial commit so HEAD exists
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("Failed to write README: %v", err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "Initial commit")

	return dir
}

// gitCmd runs a git command in the given directory and returns stdout.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\nOutput: %s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
