package coder

import (
	"testing"
)

func TestPathConstruction(t *testing.T) {
	gitRunner := NewDefaultGitRunner()
	
	// Test path construction
	wm := NewWorkspaceManager(
		gitRunner,
		"/Users/dratner/Code/maestro/work/test",        // projectWorkDir
		"git@github.com:dratner/maestro-demo.git",      // repoURL
		"main",                                         // baseBranch
		".mirrors",                                     // mirrorDir
		"story-{STORY_ID}",                            // branchPattern
		"{STORY_ID}",                                  // worktreePattern
	)
	
	agentWorkDir := "/Users/dratner/Code/maestro/work/test/claude_sonnet4-001"
	storyWorkDir := wm.BuildStoryWorkDir("claude_sonnet4-001", "050", agentWorkDir)
	mirrorPath := wm.BuildMirrorPath()
	
	expectedStoryWorkDir := "/Users/dratner/Code/maestro/work/test/claude_sonnet4-001/050"
	expectedMirrorPath := "/Users/dratner/Code/maestro/work/test/.mirrors/maestro-demo.git"
	
	if storyWorkDir != expectedStoryWorkDir {
		t.Errorf("Expected story work dir %s, got %s", expectedStoryWorkDir, storyWorkDir)
	}
	
	if mirrorPath != expectedMirrorPath {
		t.Errorf("Expected mirror path %s, got %s", expectedMirrorPath, mirrorPath)
	}
	
	t.Logf("ProjectWorkDir: /Users/dratner/Code/maestro/work/test")
	t.Logf("AgentWorkDir: %s", agentWorkDir)
	t.Logf("StoryWorkDir: %s", storyWorkDir)
	t.Logf("MirrorPath: %s", mirrorPath)
}