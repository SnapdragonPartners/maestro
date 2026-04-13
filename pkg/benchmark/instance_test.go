package benchmark

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadInstances_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances.json")

	data := `[
		{
			"instance_id": "psf__requests-1234",
			"repo": "psf/requests",
			"base_commit": "abc123",
			"problem_statement": "Fix the bug where...",
			"test_cmd": "pytest tests/test_api.py",
			"eval_image": "swe-eval:requests"
		},
		{
			"instance_id": "pandas-dev__pandas-5678",
			"repo": "pandas-dev/pandas",
			"base_commit": "def456",
			"problem_statement": "Implement feature...",
			"eval_image": "swe-eval:pandas"
		}
	]`

	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	instances, err := LoadInstances(path)
	if err != nil {
		t.Fatalf("LoadInstances() error: %v", err)
	}

	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	if instances[0].InstanceID != "psf__requests-1234" {
		t.Errorf("expected instance_id 'psf__requests-1234', got %q", instances[0].InstanceID)
	}
	if instances[0].TestCmd != "pytest tests/test_api.py" {
		t.Errorf("expected test_cmd, got %q", instances[0].TestCmd)
	}
	if instances[1].TestCmd != "" {
		t.Errorf("expected empty test_cmd for instance 2, got %q", instances[1].TestCmd)
	}
}

func TestLoadInstances_MissingRequiredField(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "missing instance_id",
			json: `[{"repo": "a/b", "base_commit": "abc", "problem_statement": "fix"}]`,
			want: "missing instance_id",
		},
		{
			name: "missing repo",
			json: `[{"instance_id": "x", "base_commit": "abc", "problem_statement": "fix"}]`,
			want: "missing repo",
		},
		{
			name: "missing base_commit",
			json: `[{"instance_id": "x", "repo": "a/b", "problem_statement": "fix"}]`,
			want: "missing base_commit",
		},
		{
			name: "missing problem_statement",
			json: `[{"instance_id": "x", "repo": "a/b", "base_commit": "abc"}]`,
			want: "missing problem_statement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "instances.json")
			if err := os.WriteFile(path, []byte(tt.json), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := LoadInstances(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); !contains(got, tt.want) {
				t.Errorf("error %q should contain %q", got, tt.want)
			}
		})
	}
}

func TestLoadInstances_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadInstances(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadInstances_FileNotFound(t *testing.T) {
	_, err := LoadInstances("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFilterInstances(t *testing.T) {
	instances := []Instance{
		{InstanceID: "a"},
		{InstanceID: "b"},
		{InstanceID: "c"},
	}

	// Empty filter returns all
	all := FilterInstances(instances, nil)
	if len(all) != 3 {
		t.Errorf("empty filter: expected 3, got %d", len(all))
	}

	// Filter specific IDs
	filtered := FilterInstances(instances, []string{"a", "c"})
	if len(filtered) != 2 {
		t.Fatalf("filtered: expected 2, got %d", len(filtered))
	}
	if filtered[0].InstanceID != "a" || filtered[1].InstanceID != "c" {
		t.Errorf("unexpected filter result: %v", filtered)
	}

	// Filter with non-matching ID
	none := FilterInstances(instances, []string{"z"})
	if len(none) != 0 {
		t.Errorf("non-matching filter: expected 0, got %d", len(none))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
