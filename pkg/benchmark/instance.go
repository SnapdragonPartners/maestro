// Package benchmark implements the SWE-EVO benchmark runner for Maestro.
package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
)

// Instance represents a single SWE-EVO benchmark task.
type Instance struct {
	InstanceID       string `json:"instance_id"`
	Repo             string `json:"repo"`              // e.g. "pandas-dev/pandas"
	BaseCommit       string `json:"base_commit"`       // Git SHA to reset to
	ProblemStatement string `json:"problem_statement"` // Raw problem text for the spec
	TestCmd          string `json:"test_cmd"`          // Optional test command override
	EvalImage        string `json:"eval_image"`        // Optional Docker image for evaluation
}

// LoadInstances reads and parses a JSON file containing SWE-EVO benchmark instances.
func LoadInstances(path string) ([]Instance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read instances file: %w", err)
	}

	var instances []Instance
	if err := json.Unmarshal(data, &instances); err != nil {
		return nil, fmt.Errorf("parse instances JSON: %w", err)
	}

	for i := range instances {
		if instances[i].InstanceID == "" {
			return nil, fmt.Errorf("instance[%d]: missing instance_id", i)
		}
		if instances[i].Repo == "" {
			return nil, fmt.Errorf("instance[%d] (%s): missing repo", i, instances[i].InstanceID)
		}
		if instances[i].BaseCommit == "" {
			return nil, fmt.Errorf("instance[%d] (%s): missing base_commit", i, instances[i].InstanceID)
		}
		if instances[i].ProblemStatement == "" {
			return nil, fmt.Errorf("instance[%d] (%s): missing problem_statement", i, instances[i].InstanceID)
		}
	}

	return instances, nil
}

// FilterInstances returns only instances whose IDs are in the provided list.
// If ids is empty, all instances are returned.
func FilterInstances(instances []Instance, ids []string) []Instance {
	if len(ids) == 0 {
		return instances
	}

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	var filtered []Instance
	for i := range instances {
		if _, ok := idSet[instances[i].InstanceID]; ok {
			filtered = append(filtered, instances[i])
		}
	}
	return filtered
}
