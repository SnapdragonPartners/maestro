package scripts

import (
	"strings"
	"testing"
)

func TestGHInitScriptEmbedded(t *testing.T) {
	// Verify the script is properly embedded
	if len(GHInitSh) == 0 {
		t.Fatalf("GHInitSh is empty - script was not embedded")
	}

	scriptContent := string(GHInitSh)

	// Verify script has the shebang
	if !strings.HasPrefix(scriptContent, "#!/usr/bin/env sh") {
		t.Errorf("Script should start with shebang, got: %s",
			strings.Split(scriptContent, "\n")[0])
	}

	// Verify script contains key components
	expectedComponents := []string{
		"set -euo pipefail",
		"GITHUB_TOKEN",
		"gh auth login",
		"gh auth setup-git",
		"git config --global user.name",
		"git config --global user.email",
		"[gh-init] GitHub auth configured",
	}

	for _, component := range expectedComponents {
		if !strings.Contains(scriptContent, component) {
			t.Errorf("Script missing expected component: %s", component)
		}
	}

	// Verify script handles ephemeral config directory
	if !strings.Contains(scriptContent, "GH_CONFIG_DIR") {
		t.Errorf("Script should handle ephemeral GH_CONFIG_DIR")
	}

	// Verify script validates GITHUB_TOKEN
	if !strings.Contains(scriptContent, "${GITHUB_TOKEN:?GITHUB_TOKEN is required}") {
		t.Errorf("Script should validate GITHUB_TOKEN is set")
	}
}
