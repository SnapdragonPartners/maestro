package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/pkg/templates/packs"
	"orchestrator/pkg/workspace"
)

func TestNewRenderer(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error: %v", err)
	}
	if renderer == nil {
		t.Fatal("NewRenderer() returned nil")
	}
	if renderer.template == nil {
		t.Fatal("NewRenderer() template is nil")
	}
}

func TestRenderBootstrapSpec_Generic(t *testing.T) {
	packs.ClearRegistry()

	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error: %v", err)
	}

	result, err := renderer.RenderBootstrapSpec("testproject", "generic", "ubuntu:24.04", nil)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error: %v", err)
	}

	// Check required content
	assertContains(t, result, "# Bootstrap Project Setup for testproject")
	assertContains(t, result, "**Platform**: generic")
	assertContains(t, result, "**Container**: ubuntu:24.04")
	assertContains(t, result, "Generic Pack v1.0.0")

	// Verify no unrendered placeholders
	assertNoUnrenderedPlaceholders(t, result)
}

func TestRenderBootstrapSpec_Go(t *testing.T) {
	packs.ClearRegistry()

	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error: %v", err)
	}

	result, err := renderer.RenderBootstrapSpec("mygoproject", "go", "golang:1.23-alpine", nil)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error: %v", err)
	}

	// Check required content
	assertContains(t, result, "# Bootstrap Project Setup for mygoproject")
	assertContains(t, result, "**Platform**:")
	assertContains(t, result, "(go)")
	assertContains(t, result, "Go Pack v1.0.0")

	// Check Go-specific content from pack
	assertContains(t, result, "golangci-lint")
	assertContains(t, result, "go mod")
	assertContains(t, result, "go test")

	// Verify tokens are replaced
	assertContains(t, result, "mygoproject") // ${PROJECT_NAME} replaced
	assertNotContains(t, result, "${PROJECT_NAME}")
	assertNotContains(t, result, "${LANGUAGE_VERSION}")

	// Verify no unrendered placeholders
	assertNoUnrenderedPlaceholders(t, result)
}

func TestRenderBootstrapSpec_WithFailures(t *testing.T) {
	packs.ClearRegistry()

	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error: %v", err)
	}

	failures := []workspace.BootstrapFailure{
		{
			Type:        workspace.BootstrapFailureBuildSystem,
			Component:   "makefile",
			Description: "Makefile missing",
			Priority:    1,
			Details:     map[string]string{"action": "create_makefile"},
		},
		{
			Type:        workspace.BootstrapFailureContainer,
			Component:   "dockerfile",
			Description: "Dockerfile required",
			Priority:    1,
			Details:     map[string]string{"action": "create_dockerfile"},
		},
	}

	result, err := renderer.RenderBootstrapSpec("testproject", "go", "", failures)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error: %v", err)
	}

	// Check failure content
	assertContains(t, result, "CRITICAL")
	assertContains(t, result, "Makefile missing")
	assertContains(t, result, "Dockerfile required")
	assertContains(t, result, "Build System Setup")
	assertContains(t, result, "Container Infrastructure")

	// Verify no unrendered placeholders
	assertNoUnrenderedPlaceholders(t, result)
}

func TestRenderBootstrapSpec_NoEmptyBackticks(t *testing.T) {
	packs.ClearRegistry()

	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error: %v", err)
	}

	// Test both platforms
	platforms := []string{"generic", "go"}
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			result, err := renderer.RenderBootstrapSpec("testproject", platform, "test-image", nil)
			if err != nil {
				t.Fatalf("RenderBootstrapSpec() error: %v", err)
			}

			// Check for empty backticks pattern (excluding code fences)
			lines := strings.Split(result, "\n")
			for i, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "```") {
					continue
				}
				if strings.Contains(line, "``") {
					t.Errorf("Found empty backticks in output for platform %s on line %d: %s", platform, i+1, line)
				}
			}
		})
	}
}

func TestAllPacksBindToTemplate(t *testing.T) {
	packs.ClearRegistry()

	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error: %v", err)
	}

	packNames, err := packs.ListAvailable()
	if err != nil {
		t.Fatalf("ListAvailable() error: %v", err)
	}

	for _, packName := range packNames {
		t.Run(packName, func(t *testing.T) {
			result, err := renderer.RenderBootstrapSpec("testproject", packName, "test-image", nil)
			if err != nil {
				t.Fatalf("RenderBootstrapSpec() error: %v", err)
			}

			// Verify no unrendered tokens
			assertNoUnrenderedPlaceholders(t, result)

			// Verify pack info is present
			pack, _, _ := packs.Get(packName)
			if pack != nil {
				assertContains(t, result, pack.DisplayName+" Pack")
			}
		})
	}
}

func TestTemplateDataSetPack(t *testing.T) {
	packs.ClearRegistry()

	tests := []struct {
		name        string
		platform    string
		projectName string
		wantPack    bool
	}{
		{"go platform", "go", "myproject", true},
		{"generic platform", "generic", "myproject", true},
		{"unknown platform falls back", "unknown", "myproject", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &TemplateData{
				Platform:    tt.platform,
				ProjectName: tt.projectName,
			}

			warnings, err := data.SetPack()
			if err != nil {
				t.Fatalf("SetPack() error: %v", err)
			}

			if tt.wantPack && data.Pack == nil {
				t.Error("Expected pack to be set, got nil")
			}

			// Unknown platforms should have fallback warning
			if tt.platform == "unknown" && len(warnings) == 0 {
				t.Error("Expected warnings for unknown platform")
			}
		})
	}
}

// Golden file tests - compare rendered output against checked-in golden files.

func TestGoldenOutput_Generic(t *testing.T) {
	testGoldenFile(t, "generic", "testproject", "ubuntu:24.04", nil, "golden_generic.md")
}

func TestGoldenOutput_Go(t *testing.T) {
	testGoldenFile(t, "go", "mygoproject", "golang:1.23-alpine", nil, "golden_go.md")
}

func testGoldenFile(t *testing.T, platform, projectName, containerImage string, failures []workspace.BootstrapFailure, goldenFileName string) {
	t.Helper()
	packs.ClearRegistry()

	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error: %v", err)
	}

	result, err := renderer.RenderBootstrapSpec(projectName, platform, containerImage, failures)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error: %v", err)
	}

	goldenPath := filepath.Join("testdata", goldenFileName)

	// If UPDATE_GOLDEN env var is set, update the golden file
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if writeErr := os.WriteFile(goldenPath, []byte(result), 0644); writeErr != nil {
			t.Fatalf("Failed to update golden file: %v", writeErr)
		}
		t.Logf("Updated golden file: %s", goldenPath)
		return
	}

	// Read expected golden file
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("Golden file %s does not exist. Run with UPDATE_GOLDEN=1 to create it.", goldenPath)
		}
		t.Fatalf("Failed to read golden file: %v", err)
	}

	// Compare
	if result != string(expected) {
		t.Errorf("Output does not match golden file %s.\nRun with UPDATE_GOLDEN=1 to update.\n\nGot:\n%s\n\nExpected:\n%s", goldenPath, result, string(expected))
	}
}

// Helper functions

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("Expected output to contain %q", substr)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("Expected output NOT to contain %q", substr)
	}
}

func assertNoUnrenderedPlaceholders(t *testing.T, s string) {
	t.Helper()

	// Check for unrendered Go template placeholders
	if strings.Contains(s, "{{") {
		t.Error("Found unrendered Go template placeholder '{{' in output")
	}

	// Check for unrendered token placeholders
	if strings.Contains(s, "${") {
		t.Error("Found unrendered token placeholder '${' in output")
	}

	// Check for empty inline backticks (sign of missing values)
	// Pattern: `` followed by comma/space, or preceded by space/start of line
	// But NOT code fences like ```makefile
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		// Skip code fence lines
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			continue
		}
		// Check for empty inline code patterns like ``, `` or just ``
		if strings.Contains(line, "``") {
			t.Errorf("Found empty backticks '``' on line %d: %s", i+1, line)
		}
	}
}
