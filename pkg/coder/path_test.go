package coder

import (
	"testing"
)

func TestPathConstruction(t *testing.T) {
	gitRunner := NewDefaultGitRunner()

	// Test path construction
	wm := NewWorkspaceManager(
		gitRunner,
		"/Users/dratner/Code/maestro/work/test",   // projectWorkDir
		"git@github.com:dratner/maestro-demo.git", // repoURL
		"main",             // baseBranch
		".mirrors",         // mirrorDir
		"story-{STORY_ID}", // branchPattern
		"{STORY_ID}",       // worktreePattern
	)

	agentWorkDir := "/Users/dratner/Code/maestro/work/test/claude_sonnet4-001"
	actualAgentWorkDir := wm.BuildAgentWorkDir("claude_sonnet4-001", agentWorkDir)
	mirrorPath := wm.BuildMirrorPath()

	expectedAgentWorkDir := "/Users/dratner/Code/maestro/work/test/claude_sonnet4-001"
	expectedMirrorPath := "/Users/dratner/Code/maestro/work/test/.mirrors/maestro-demo.git"

	if actualAgentWorkDir != expectedAgentWorkDir {
		t.Errorf("Expected agent work dir %s, got %s", expectedAgentWorkDir, actualAgentWorkDir)
	}

	if mirrorPath != expectedMirrorPath {
		t.Errorf("Expected mirror path %s, got %s", expectedMirrorPath, mirrorPath)
	}

	t.Logf("ProjectWorkDir: /Users/dratner/Code/maestro/work/test")
	t.Logf("AgentWorkDir: %s", agentWorkDir)
	t.Logf("ActualAgentWorkDir: %s", actualAgentWorkDir)
	t.Logf("MirrorPath: %s", mirrorPath)
}
