package bootstrap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/pkg/build"
)

func TestGetEcosystemForPlatform(t *testing.T) {
	tests := []struct {
		platform string
		want     string
	}{
		{"go", "gomod"},
		{"Go", "gomod"},
		{"GO", "gomod"},
		{"node", "npm"},
		{"Node", "npm"},
		{"python", "pip"},
		{"Python", "pip"},
		{"rust", "cargo"},
		{"java", "maven"},
		{"unknown", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			got := GetEcosystemForPlatform(tt.platform)
			if got != tt.want {
				t.Errorf("GetEcosystemForPlatform(%q) = %q, want %q", tt.platform, got, tt.want)
			}
		})
	}
}

func TestWorkflowGenerator_GenerateAll(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "workflow-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a mock backend
	backend := &mockBackend{name: "go"}

	// Generate workflows
	generator := NewWorkflowGenerator(tempDir)
	files, err := generator.GenerateAll(backend, "make test")
	if err != nil {
		t.Fatalf("GenerateAll() error = %v", err)
	}

	// Check expected files were returned
	expectedFiles := []string{
		".github/dependabot.yml",
		".github/workflows/ci.yml",
		".github/workflows/dependabot-auto-merge.yml",
	}

	if len(files) != len(expectedFiles) {
		t.Errorf("GenerateAll() returned %d files, want %d", len(files), len(expectedFiles))
	}

	// Check files exist on disk
	for _, f := range expectedFiles {
		fullPath := filepath.Join(tempDir, f)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			t.Errorf("Expected file %s was not created", f)
		}
	}
}

func TestWorkflowGenerator_DependabotConfig(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "workflow-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tests := []struct {
		backendName   string
		wantEcosystem string
	}{
		{"go", "gomod"},
		{"node", "npm"},
		{"python", "pip"},
	}

	for _, tt := range tests {
		t.Run(tt.backendName, func(t *testing.T) {
			// Create subdirectory for this test
			testDir := filepath.Join(tempDir, tt.backendName)
			if err := os.MkdirAll(testDir, 0755); err != nil {
				t.Fatalf("Failed to create test dir: %v", err)
			}

			backend := &mockBackend{name: tt.backendName}
			generator := NewWorkflowGenerator(testDir)

			if err := generator.generateDependabotConfig(backend); err != nil {
				t.Fatalf("generateDependabotConfig() error = %v", err)
			}

			// Read the generated file
			content, err := os.ReadFile(filepath.Join(testDir, ".github", "dependabot.yml"))
			if err != nil {
				t.Fatalf("Failed to read dependabot.yml: %v", err)
			}

			// Check ecosystem is correct
			if !strings.Contains(string(content), tt.wantEcosystem) {
				t.Errorf("dependabot.yml does not contain ecosystem %q", tt.wantEcosystem)
			}
		})
	}
}

func TestWorkflowGenerator_CIWorkflow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "workflow-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	backend := &mockBackend{name: "go"}
	generator := NewWorkflowGenerator(tempDir)

	testTarget := "go test ./..."
	if genErr := generator.generateCIWorkflow(backend, testTarget); genErr != nil {
		t.Fatalf("generateCIWorkflow() error = %v", genErr)
	}

	// Read the generated file
	content, err := os.ReadFile(filepath.Join(tempDir, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("Failed to read ci.yml: %v", err)
	}

	// Check test target is present
	if !strings.Contains(string(content), testTarget) {
		t.Errorf("ci.yml does not contain test target %q", testTarget)
	}

	// Check Go setup step is present
	if !strings.Contains(string(content), "actions/setup-go") {
		t.Error("ci.yml does not contain Go setup step")
	}
}

func TestWorkflowGenerator_DependabotAutoMerge(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "workflow-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	generator := NewWorkflowGenerator(tempDir)
	if genErr := generator.generateDependabotAutoMergeWorkflow(); genErr != nil {
		t.Fatalf("generateDependabotAutoMergeWorkflow() error = %v", genErr)
	}

	// Read the generated file
	content, err := os.ReadFile(filepath.Join(tempDir, ".github", "workflows", "dependabot-auto-merge.yml"))
	if err != nil {
		t.Fatalf("Failed to read dependabot-auto-merge.yml: %v", err)
	}

	// Check key elements are present
	contentStr := string(content)
	checks := []string{
		"dependabot[bot]",
		"dependabot/fetch-metadata",
		"version-update:semver-patch",
		"gh pr merge --auto --merge",
	}

	for _, check := range checks {
		if !strings.Contains(contentStr, check) {
			t.Errorf("dependabot-auto-merge.yml does not contain %q", check)
		}
	}
}

// mockBackend implements build.Backend for testing.
type mockBackend struct {
	name string
}

func (m *mockBackend) Name() string {
	return m.name
}

func (m *mockBackend) Detect(_ string) bool {
	return true
}

func (m *mockBackend) Build(_ context.Context, _ build.Executor, _ string, _ io.Writer) error {
	return nil
}

func (m *mockBackend) Test(_ context.Context, _ build.Executor, _ string, _ io.Writer) error {
	return nil
}

func (m *mockBackend) Lint(_ context.Context, _ build.Executor, _ string, _ io.Writer) error {
	return nil
}

func (m *mockBackend) Run(_ context.Context, _ build.Executor, _ string, _ []string, _ io.Writer) error {
	return nil
}

func (m *mockBackend) GetDockerImage(_ string) string {
	return "golang:1.21"
}
