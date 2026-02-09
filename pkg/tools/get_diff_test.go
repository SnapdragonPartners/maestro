package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	execpkg "orchestrator/pkg/exec"
)

func TestGetDiffToolUsesConfiguredWorkspace(t *testing.T) {
	// Verify the tool stores and uses the workspace root
	workspace := "/mnt/coders/hotfix-001"
	tool := NewGetDiffTool(nil, workspace, 1000)

	// Check that the workspace root is stored correctly
	if tool.workspaceRoot != workspace {
		t.Errorf("expected workspaceRoot %q, got %q", workspace, tool.workspaceRoot)
	}
}

func TestGetDiffToolDefaultWorkspace(t *testing.T) {
	// Verify default workspace is set when empty string is passed
	tool := NewGetDiffTool(nil, "", 1000)

	if tool.workspaceRoot != "/workspace" {
		t.Errorf("expected default workspaceRoot '/workspace', got %q", tool.workspaceRoot)
	}
}

func TestGetDiffToolBuildDiffCommand(t *testing.T) {
	tool := NewGetDiffTool(nil, "/mnt/coders/coder-001", 1000)

	testCases := []struct {
		name    string
		path    string
		baseSHA string
		wantCmd string
		wantErr bool
	}{
		{
			name:    "no path, no base - uses merge-base SHA",
			path:    "",
			baseSHA: "abc123",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff abc123..HEAD 2>&1 | head -n 1000",
		},
		{
			name:    "specific file with base SHA",
			path:    "db/questions.go",
			baseSHA: "abc123",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff abc123..HEAD -- db/questions.go 2>&1 | head -n 1000",
		},
		{
			name:    "empty base SHA falls back to origin/main",
			path:    "",
			baseSHA: "",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff origin/main 2>&1 | head -n 1000",
		},
		{
			name:    "specific file with empty base SHA",
			path:    "main.go",
			baseSHA: "",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff origin/main -- main.go 2>&1 | head -n 1000",
		},
		{
			name:    "path traversal blocked",
			path:    "../../../etc/passwd",
			baseSHA: "abc123",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := tool.buildDiffCommand(tool.workspaceRoot, tc.path, tc.baseSHA)

			if tc.wantErr {
				if err == nil {
					t.Error("expected error for path traversal, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cmd != tc.wantCmd {
				t.Errorf("expected command:\n%s\ngot:\n%s", tc.wantCmd, cmd)
			}
		})
	}
}

func TestGetDiffToolDefinitionHasNoRequiredParams(t *testing.T) {
	tool := NewGetDiffTool(nil, "/workspace", 1000)
	def := tool.Definition()

	// No required parameters
	if len(def.InputSchema.Required) != 0 {
		t.Errorf("expected no required parameters, got %v", def.InputSchema.Required)
	}

	// path property should exist
	if _, exists := def.InputSchema.Properties["path"]; !exists {
		t.Error("expected 'path' property in schema")
	}

	// base property should exist
	if _, exists := def.InputSchema.Properties["base"]; !exists {
		t.Error("expected 'base' property in schema")
	}

	// coder_id should NOT exist
	if _, exists := def.InputSchema.Properties["coder_id"]; exists {
		t.Error("coder_id property should have been removed from schema")
	}
}

func TestGetDiffToolDefinitionDescribesMergeBase(t *testing.T) {
	tool := NewGetDiffTool(nil, "/workspace", 1000)
	def := tool.Definition()

	// Description should mention merge-base behavior
	if !strings.Contains(def.Description, "merge-base") {
		t.Error("tool description should mention merge-base default behavior")
	}

	// Base property description should explain merge-base default
	baseProp, exists := def.InputSchema.Properties["base"]
	if !exists {
		t.Fatal("expected 'base' property in schema")
	}
	if !strings.Contains(baseProp.Description, "merge-base") {
		t.Error("base property description should mention merge-base")
	}
	if !strings.Contains(baseProp.Description, "origin/main") {
		t.Error("base property description should mention origin/main")
	}
}

func TestGetDiffToolDocumentation(t *testing.T) {
	tool := NewGetDiffTool(nil, "/workspace", 1000)
	doc := tool.PromptDocumentation()

	// Should mention path parameter
	if !strings.Contains(doc, "path") {
		t.Error("documentation should mention path parameter")
	}

	// Should mention base parameter
	if !strings.Contains(doc, "base") {
		t.Error("documentation should mention base parameter")
	}

	// Should mention merge-base behavior
	if !strings.Contains(doc, "merge-base") {
		t.Error("documentation should mention merge-base default")
	}

	// Should NOT mention coder_id
	if strings.Contains(doc, "coder_id") {
		t.Error("documentation should not mention coder_id")
	}

	// Should mention traceability fields
	if !strings.Contains(doc, "head_sha") {
		t.Error("documentation should mention head_sha")
	}
	if !strings.Contains(doc, "base_sha") {
		t.Error("documentation should mention base_sha")
	}
}

// =============================================================================
// Integration test: Reproduces the architect diff visibility bug
// =============================================================================

// TestGetDiff_UncommittedChangesInvisible reproduces the original bug:
// get_diff uses `baseSHA..HEAD` which only shows committed changes.
// Uncommitted files are invisible to the architect's get_diff tool.
// After committing (as the done tool now does), they become visible.
func TestGetDiff_UncommittedChangesInvisible(t *testing.T) {
	// Create a real git repo with main branch
	repoDir := setupGetDiffTestRepo(t)

	// Create a feature branch (simulating coder workspace)
	gitExec(t, repoDir, "checkout", "-b", "coder-001-story-1")

	// Write a new file but do NOT commit it
	featureFile := filepath.Join(repoDir, "feature.go")
	if err := os.WriteFile(featureFile, []byte("package main\nfunc Feature() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write feature file: %v", err)
	}

	// Run get_diff - this is what the architect does
	executor := execpkg.NewLocalExec()
	diffTool := NewGetDiffTool(executor, repoDir, 10000)

	result, err := diffTool.Exec(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("get_diff failed: %v", err)
	}

	// Parse result
	var diffResult map[string]any
	if parseErr := json.Unmarshal([]byte(result.Content), &diffResult); parseErr != nil {
		t.Fatalf("Failed to parse get_diff result: %v", parseErr)
	}

	// BUG REPRODUCTION: diff should be empty because file is not committed
	diff, _ := diffResult["diff"].(string)
	if strings.Contains(diff, "feature.go") {
		t.Fatal("BUG: get_diff should NOT show uncommitted files (baseSHA..HEAD only shows commits)")
	}
	t.Log("Confirmed: uncommitted changes are invisible to get_diff (the original bug)")

	// Now commit the changes (as the done tool does)
	gitExec(t, repoDir, "add", "-A")
	gitExec(t, repoDir, "commit", "-m", "Story 1: Added feature")

	// Run get_diff again - now the architect should see the changes
	result, err = diffTool.Exec(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("get_diff after commit failed: %v", err)
	}

	if parseErr := json.Unmarshal([]byte(result.Content), &diffResult); parseErr != nil {
		t.Fatalf("Failed to parse get_diff result: %v", parseErr)
	}

	diff, _ = diffResult["diff"].(string)
	if !strings.Contains(diff, "feature.go") {
		t.Errorf("After commit, get_diff should show feature.go in diff, got: %s", diff)
	}
	if !strings.Contains(diff, "+func Feature()") {
		t.Errorf("After commit, get_diff should show the added function, got: %s", diff)
	}
	t.Log("Confirmed: committed changes are visible to get_diff (the fix works)")
}

// TestGetDiff_DoneToolThenGetDiff is an end-to-end test verifying that calling
// the done tool makes changes visible to get_diff (the full fix).
func TestGetDiff_DoneToolThenGetDiff(t *testing.T) {
	repoDir := setupGetDiffTestRepo(t)

	// Create feature branch
	gitExec(t, repoDir, "checkout", "-b", "coder-001-story-2")

	// Write new code
	if err := os.WriteFile(filepath.Join(repoDir, "api.go"), []byte("package main\nfunc API() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Call the done tool (this should commit the changes)
	executor := execpkg.NewLocalExec()
	doneTool := NewDoneTool(nil, executor, repoDir, "STORY-2")

	doneResult, err := doneTool.Exec(context.Background(), map[string]any{
		"summary": "Added API endpoint",
	})
	if err != nil {
		t.Fatalf("done tool failed: %v", err)
	}
	if doneResult.ProcessEffect == nil || doneResult.ProcessEffect.Signal != SignalTesting {
		t.Fatal("Expected TESTING signal from done tool")
	}

	// Now run get_diff as the architect would
	diffTool := NewGetDiffTool(executor, repoDir, 10000)
	diffResult, err := diffTool.Exec(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("get_diff failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(diffResult.Content), &parsed); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	diff, _ := parsed["diff"].(string)
	if !strings.Contains(diff, "api.go") {
		t.Errorf("After done tool, get_diff should show api.go, got: %s", diff)
	}
	if !strings.Contains(diff, "+func API()") {
		t.Errorf("After done tool, get_diff should show added function, got: %s", diff)
	}
	t.Log("End-to-end: done tool commit makes changes visible to architect's get_diff")
}

// --- helpers for get_diff tests ---

// setupGetDiffTestRepo creates a git repo with an initial commit on main
// and a remote origin set up (needed for merge-base calculations).
func setupGetDiffTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitExec(t, dir, "init")
	gitExec(t, dir, "config", "user.email", "test@test.com")
	gitExec(t, dir, "config", "user.name", "Test")

	// Create initial commit on main
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("Failed to write README: %v", err)
	}
	gitExec(t, dir, "add", "-A")
	gitExec(t, dir, "commit", "-m", "Initial commit")

	// Rename branch to main (in case git defaults to master)
	gitExec(t, dir, "branch", "-M", "main")

	// Create a bare clone to act as "origin" (needed for origin/main ref)
	originDir := t.TempDir()
	originExec(t, "git", "clone", "--bare", dir, originDir)

	// Add origin to the repo
	gitExec(t, dir, "remote", "add", "origin", originDir)
	gitExec(t, dir, "fetch", "origin")

	return dir
}

// gitExec runs a git command in the given directory.
func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\nOutput: %s", strings.Join(args, " "), err, string(out))
	}
}

// originExec runs a command (not necessarily in a specific dir).
func originExec(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\nOutput: %s", name, strings.Join(args, " "), err, string(out))
	}
}
