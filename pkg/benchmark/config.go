package benchmark

import (
	"encoding/json"
	"fmt"

	"orchestrator/pkg/config"
)

// GenerateConfig produces a Maestro JSON config for a single benchmark instance.
// containerImage is optional — when empty, Maestro bootstraps its own container
// from the language pack (recommended for benchmark runs).
func GenerateConfig(inst *Instance, giteaRepoURL, containerImage string) ([]byte, error) {
	testCmd := inst.TestCmd
	if testCmd == "" {
		testCmd = "pytest"
	}

	cfg := map[string]any{
		"project": map[string]any{
			"primary_platform": "python",
			"pack_name":        "python",
		},
		"git": map[string]any{
			"repo_url":      giteaRepoURL,
			"target_branch": "main",
		},
		"forge": map[string]any{
			"provider": config.ForgeProviderGitea,
		},
		"maintenance": map[string]any{
			"enabled": false,
		},
		"webui": map[string]any{
			"enabled": false,
		},
		"agents": map[string]any{
			"max_coders": 1,
		},
		"build": map[string]any{
			"build": "true",
			"lint":  "true",
			"run":   "true",
			"test":  testCmd,
		},
	}

	// Only set container.name if explicitly provided; otherwise let Maestro
	// bootstrap from the language pack.
	if containerImage != "" {
		cfg["container"] = map[string]any{
			"name": containerImage,
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return data, nil
}
