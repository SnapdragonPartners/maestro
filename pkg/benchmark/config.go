package benchmark

import (
	"encoding/json"
	"fmt"
)

// GenerateConfig produces a Maestro JSON config for a single benchmark instance.
// containerImage must be non-empty — no silent fallback.
func GenerateConfig(inst *Instance, giteaRepoURL, containerImage string) ([]byte, error) {
	if containerImage == "" {
		return nil, fmt.Errorf("container image required for instance %s", inst.InstanceID)
	}

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
			"provider": "gitea",
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
		"container": map[string]any{
			"name": containerImage,
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return data, nil
}
