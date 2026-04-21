package benchmark

import (
	"encoding/json"
	"testing"

	"orchestrator/pkg/config"
)

func TestGenerateConfig_Valid(t *testing.T) {
	inst := &Instance{
		InstanceID:       "psf__requests-1234",
		Repo:             "psf/requests",
		BaseCommit:       "abc123",
		ProblemStatement: "Fix the bug",
		TestCmd:          "pytest tests/",
	}

	data, err := GenerateConfig(inst, "http://localhost:3000/maestro/psf__requests-1234.git", "swe-eval:requests")
	if err != nil {
		t.Fatalf("GenerateConfig() error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	assertMapField(t, cfg, "project", "primary_platform", "python")
	assertMapField(t, cfg, "project", "pack_name", "python")
	assertMapField(t, cfg, "git", "repo_url", "http://localhost:3000/maestro/psf__requests-1234.git")
	assertMapField(t, cfg, "git", "target_branch", "main")
	assertMapField(t, cfg, "forge", "provider", config.ForgeProviderGitea)
	assertMapField(t, cfg, "build", "test", "pytest tests/")
	assertMapField(t, cfg, "container", "name", "swe-eval:requests")

	// Check disabled features
	assertMapBoolField(t, cfg, "webui", "enabled", false)
	assertMapBoolField(t, cfg, "maintenance", "enabled", false)

	// Check max coders
	agents, ok := cfg["agents"].(map[string]any)
	if !ok {
		t.Fatal("agents section missing or wrong type")
	}
	if agents["max_coders"] != float64(1) {
		t.Errorf("expected max_coders 1, got %v", agents["max_coders"])
	}
}

func TestGenerateConfig_DefaultTestCmd(t *testing.T) {
	inst := &Instance{
		InstanceID:       "test-1",
		Repo:             "org/repo",
		BaseCommit:       "abc",
		ProblemStatement: "fix",
	}

	data, err := GenerateConfig(inst, "http://localhost:3000/maestro/test-1.git", "python:3.11")
	if err != nil {
		t.Fatalf("GenerateConfig() error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	assertMapField(t, cfg, "build", "test", "pytest")
}

func TestGenerateConfig_NoImage_Bootstrap(t *testing.T) {
	inst := &Instance{
		InstanceID:       "test-1",
		Repo:             "org/repo",
		BaseCommit:       "abc",
		ProblemStatement: "fix",
	}

	data, err := GenerateConfig(inst, "http://localhost:3000/repo.git", "")
	if err != nil {
		t.Fatalf("GenerateConfig() error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// container section should be absent — Maestro bootstraps from language pack.
	if _, ok := cfg["container"]; ok {
		t.Error("expected no container section when image is empty")
	}
}

// assertMapField checks cfg[section][field] == want.
func assertMapField(t *testing.T, cfg map[string]any, section, field, want string) {
	t.Helper()
	sec, ok := cfg[section].(map[string]any)
	if !ok {
		t.Fatalf("%s section missing or wrong type", section)
	}
	got, ok := sec[field].(string)
	if !ok {
		t.Fatalf("%s.%s missing or wrong type", section, field)
	}
	if got != want {
		t.Errorf("%s.%s = %q, want %q", section, field, got, want)
	}
}

// assertMapBoolField checks cfg[section][field] == want (bool).
func assertMapBoolField(t *testing.T, cfg map[string]any, section, field string, want bool) {
	t.Helper()
	sec, ok := cfg[section].(map[string]any)
	if !ok {
		t.Fatalf("%s section missing or wrong type", section)
	}
	got, ok := sec[field].(bool)
	if !ok {
		t.Fatalf("%s.%s missing or wrong type", section, field)
	}
	if got != want {
		t.Errorf("%s.%s = %v, want %v", section, field, got, want)
	}
}
