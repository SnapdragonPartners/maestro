package coder

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestPathConstruction(t *testing.T) {
	// Set up config with test values (CloneManager reads from config)
	config.SetConfigForTesting(&config.Config{
		Git: &config.GitConfig{
			RepoURL:       "git@github.com:dratner/maestro-demo.git",
			TargetBranch:  "main",
			BranchPattern: "story-{STORY_ID}",
		},
	})
	t.Cleanup(func() { config.SetConfigForTesting(nil) })

	gitRunner := NewDefaultGitRunner()

	// Test path construction.
	cm := NewCloneManager(
		gitRunner,
		"/Users/dratner/Code/maestro/work/test", // projectWorkDir
		"", "", "", "",                          // These are now ignored - values come from config
	)

	agentWorkDir := "/Users/dratner/Code/maestro/work/test/claude_sonnet4-001"
	actualAgentWorkDir := cm.BuildAgentWorkDir("claude_sonnet4-001", agentWorkDir)
	mirrorPath := cm.BuildMirrorPath()

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
