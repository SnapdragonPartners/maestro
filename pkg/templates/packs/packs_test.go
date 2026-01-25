package packs

import (
	"slices"
	"strings"
	"testing"
)

func TestAllPacksValid(t *testing.T) {
	// Clear any cached state
	ClearRegistry()

	names, err := ListAvailable()
	if err != nil {
		t.Fatalf("ListAvailable() error: %v", err)
	}

	if len(names) == 0 {
		t.Fatal("No packs found")
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			pack, err := Load(name)
			if err != nil {
				t.Fatalf("Load(%q) error: %v", name, err)
			}

			result := Validate(pack)
			if !result.Valid {
				t.Errorf("Pack %q is invalid: %v", name, result.Errors)
			}
		})
	}
}

func TestPackRequiredFields(t *testing.T) {
	tests := []struct {
		name     string
		pack     Pack
		wantErrs []string
	}{
		{
			name: "empty pack",
			pack: Pack{},
			wantErrs: []string{
				"missing required field: name",
				"missing required field: version",
				"missing required field: display_name",
				"missing required field: makefile_targets.build",
				"missing required field: makefile_targets.test",
				"missing required field: makefile_targets.lint",
				"missing required field: makefile_targets.run",
			},
		},
		{
			name: "missing build target",
			pack: Pack{
				Name:        "test",
				Version:     "1.0.0",
				DisplayName: "Test",
				MakefileTargets: MakefileTargets{
					Test: "echo test",
					Lint: "echo lint",
					Run:  "echo run",
				},
			},
			wantErrs: []string{
				"missing required field: makefile_targets.build",
			},
		},
		{
			name: "missing test target",
			pack: Pack{
				Name:        "test",
				Version:     "1.0.0",
				DisplayName: "Test",
				MakefileTargets: MakefileTargets{
					Build: "echo build",
					Lint:  "echo lint",
					Run:   "echo run",
				},
			},
			wantErrs: []string{
				"missing required field: makefile_targets.test",
			},
		},
		{
			name: "valid minimal pack",
			pack: Pack{
				Name:        "test",
				Version:     "1.0.0",
				DisplayName: "Test",
				MakefileTargets: MakefileTargets{
					Build: "echo build",
					Test:  "echo test",
					Lint:  "echo lint",
					Run:   "echo run",
				},
			},
			wantErrs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Validate(&tt.pack)

			if tt.wantErrs == nil {
				if !result.Valid {
					t.Errorf("Expected valid, got errors: %v", result.Errors)
				}
				return
			}

			if result.Valid {
				t.Errorf("Expected invalid, but got valid")
				return
			}

			for _, wantErr := range tt.wantErrs {
				if !slices.Contains(result.Errors, wantErr) {
					t.Errorf("Expected error %q not found in %v", wantErr, result.Errors)
				}
			}
		})
	}
}

func TestGenericPackExists(t *testing.T) {
	ClearRegistry()

	pack, err := GetGeneric()
	if err != nil {
		t.Fatalf("GetGeneric() error: %v", err)
	}

	if pack.Name != "generic" {
		t.Errorf("Expected name 'generic', got %q", pack.Name)
	}

	result := Validate(pack)
	if !result.Valid {
		t.Errorf("Generic pack is invalid: %v", result.Errors)
	}
}

func TestInvalidPackFallsBackToGeneric(t *testing.T) {
	ClearRegistry()

	// Request a non-existent pack
	pack, warnings, err := Get("nonexistent")
	if err != nil {
		t.Fatalf("Get('nonexistent') error: %v", err)
	}

	if pack.Name != "generic" {
		t.Errorf("Expected fallback to generic, got %q", pack.Name)
	}

	if len(warnings) == 0 {
		t.Error("Expected warnings about fallback, got none")
	}

	foundFallbackWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "not found") && strings.Contains(w, "generic") {
			foundFallbackWarning = true
			break
		}
	}
	if !foundFallbackWarning {
		t.Errorf("Expected fallback warning, got: %v", warnings)
	}
}

func TestUnknownTokenDetection(t *testing.T) {
	pack := Pack{
		Name:        "test",
		Version:     "1.0.0",
		DisplayName: "Test",
		MakefileTargets: MakefileTargets{
			Build: "echo ${UNKNOWN_TOKEN}",
			Test:  "echo test",
		},
	}

	result := Validate(&pack)
	if result.Valid {
		t.Error("Expected invalid due to unknown token")
	}

	foundTokenError := false
	for _, err := range result.Errors {
		if strings.Contains(err, "unknown token") && strings.Contains(err, "${UNKNOWN_TOKEN}") {
			foundTokenError = true
			break
		}
	}
	if !foundTokenError {
		t.Errorf("Expected unknown token error, got: %v", result.Errors)
	}
}

func TestAllowedTokensPass(t *testing.T) {
	pack := Pack{
		Name:            "test",
		Version:         "1.0.0",
		DisplayName:     "Test",
		LanguageVersion: "1.23",
		MakefileTargets: MakefileTargets{
			Build: "build -o ${PROJECT_NAME}",
			Test:  "test ${PROJECT_NAME}",
			Lint:  "lint ${PROJECT_NAME}",
			Run:   "run ${PROJECT_NAME}",
		},
		RecommendedBaseImage: "golang:${LANGUAGE_VERSION}-alpine",
	}

	result := Validate(&pack)
	if !result.Valid {
		t.Errorf("Expected valid, got errors: %v", result.Errors)
	}
}

func TestRenderedTokenReplacement(t *testing.T) {
	ClearRegistry()

	pack, err := Load("go")
	if err != nil {
		t.Fatalf("Load('go') error: %v", err)
	}

	rendered, err := pack.Rendered(TokenValues{
		ProjectName:     "myproject",
		LanguageVersion: "1.22",
	})
	if err != nil {
		t.Fatalf("Rendered() error: %v", err)
	}

	// Check that tokens were replaced
	if strings.Contains(rendered.MakefileTargets.Build, "${PROJECT_NAME}") {
		t.Error("${PROJECT_NAME} not replaced in build target")
	}
	if !strings.Contains(rendered.MakefileTargets.Build, "myproject") {
		t.Error("Expected 'myproject' in build target")
	}

	if strings.Contains(rendered.RecommendedBaseImage, "${LANGUAGE_VERSION}") {
		t.Error("${LANGUAGE_VERSION} not replaced in recommended_base_image")
	}
	if !strings.Contains(rendered.RecommendedBaseImage, "1.22") {
		t.Error("Expected '1.22' in recommended_base_image")
	}

	// Verify no unrendered tokens remain
	if strings.Contains(rendered.MakefileTargets.Build, "${") {
		t.Error("Unrendered tokens remain in build target")
	}
}

func TestRenderedUsesPackLanguageVersion(t *testing.T) {
	pack := Pack{
		Name:            "test",
		Version:         "1.0.0",
		DisplayName:     "Test",
		LanguageVersion: "1.23",
		MakefileTargets: MakefileTargets{
			Build: "build",
			Test:  "test",
		},
		RecommendedBaseImage: "golang:${LANGUAGE_VERSION}-alpine",
	}

	// Don't provide LanguageVersion in TokenValues - should use pack's value
	rendered, err := pack.Rendered(TokenValues{
		ProjectName: "myproject",
	})
	if err != nil {
		t.Fatalf("Rendered() error: %v", err)
	}

	if !strings.Contains(rendered.RecommendedBaseImage, "1.23") {
		t.Errorf("Expected pack's language_version '1.23', got: %s", rendered.RecommendedBaseImage)
	}
}

func TestRenderedMissingLanguageVersionError(t *testing.T) {
	pack := Pack{
		Name:        "test",
		Version:     "1.0.0",
		DisplayName: "Test",
		// No LanguageVersion set
		MakefileTargets: MakefileTargets{
			Build: "build",
			Test:  "test",
		},
		RecommendedBaseImage: "golang:${LANGUAGE_VERSION}-alpine", // Uses token
	}

	// Don't provide LanguageVersion in TokenValues either
	_, err := pack.Rendered(TokenValues{
		ProjectName: "myproject",
	})

	if err == nil {
		t.Error("Expected error for missing LANGUAGE_VERSION, got nil")
	}

	if !strings.Contains(err.Error(), "${LANGUAGE_VERSION}") {
		t.Errorf("Expected error about LANGUAGE_VERSION, got: %v", err)
	}
}

func TestRenderedNoUnrenderedTokens(t *testing.T) {
	ClearRegistry()

	names, err := ListAvailable()
	if err != nil {
		t.Fatalf("ListAvailable() error: %v", err)
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			pack, err := Load(name)
			if err != nil {
				t.Fatalf("Load(%q) error: %v", name, err)
			}

			// Provide all possible token values
			rendered, err := pack.Rendered(TokenValues{
				ProjectName:     "testproject",
				LanguageVersion: "1.23",
			})
			if err != nil {
				t.Fatalf("Rendered() error: %v", err)
			}

			// Check all fields for unrendered tokens
			fieldsToCheck := []struct {
				name  string
				value string
			}{
				{"recommended_base_image", rendered.RecommendedBaseImage},
				{"makefile_targets.build", rendered.MakefileTargets.Build},
				{"makefile_targets.test", rendered.MakefileTargets.Test},
				{"makefile_targets.lint", rendered.MakefileTargets.Lint},
				{"makefile_targets.run", rendered.MakefileTargets.Run},
				{"makefile_targets.clean", rendered.MakefileTargets.Clean},
				{"template_sections.module_setup", rendered.TemplateSections.ModuleSetup},
				{"template_sections.lint_config", rendered.TemplateSections.LintConfig},
				{"template_sections.quality_setup", rendered.TemplateSections.QualitySetup},
			}

			for _, field := range fieldsToCheck {
				if strings.Contains(field.value, "${") {
					t.Errorf("Field %q contains unrendered token: %s", field.name, field.value)
				}
			}
		})
	}
}

func TestLanguageVersionWarningWithoutDefinition(t *testing.T) {
	pack := Pack{
		Name:        "test",
		Version:     "1.0.0",
		DisplayName: "Test",
		// No LanguageVersion defined
		MakefileTargets: MakefileTargets{
			Build: "build",
			Test:  "test",
			Lint:  "lint",
			Run:   "run",
		},
		RecommendedBaseImage: "golang:${LANGUAGE_VERSION}-alpine", // Uses token without definition
	}

	result := Validate(&pack)
	// Should be valid but with warnings
	if !result.Valid {
		t.Errorf("Expected valid (with warnings), got errors: %v", result.Errors)
	}

	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "${LANGUAGE_VERSION}") && strings.Contains(w, "no language_version") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("Expected warning about LANGUAGE_VERSION usage without definition, got: %v", result.Warnings)
	}
}

func TestLoadGoPackSpecifics(t *testing.T) {
	ClearRegistry()

	pack, err := Load("go")
	if err != nil {
		t.Fatalf("Load('go') error: %v", err)
	}

	// Verify Go-specific fields
	if pack.LanguageVersion == "" {
		t.Error("Go pack should have language_version set")
	}

	if pack.Tooling.Linter != "golangci-lint" {
		t.Errorf("Expected linter 'golangci-lint', got %q", pack.Tooling.Linter)
	}

	if pack.Tooling.PackageManager != "go mod" {
		t.Errorf("Expected package_manager 'go mod', got %q", pack.Tooling.PackageManager)
	}

	// Verify template sections are present
	if pack.TemplateSections.ModuleSetup == "" {
		t.Error("Go pack should have module_setup section")
	}
	if pack.TemplateSections.LintConfig == "" {
		t.Error("Go pack should have lint_config section")
	}
}

func TestNormalizePlatform(t *testing.T) {
	ClearRegistry()

	tests := []struct {
		input    string
		expected string
	}{
		// Go aliases - pack exists
		{"go", "go"},
		{"golang", "go"},
		{"Go", "go"},
		{"GOLANG", "go"},

		// Python aliases - pack exists
		{"python", "python"},
		{"py", "python"},
		{"python3", "python"},
		{"py3", "python"},

		// Node aliases - pack exists
		{"node", "node"},
		{"nodejs", "node"},
		{"javascript", "node"},
		{"js", "node"},
		{"typescript", "node"},
		{"ts", "node"},

		// Rust aliases - no pack, falls back to generic
		{"rust", "generic"},
		{"rs", "generic"},

		// Generic - pack exists
		{"generic", "generic"},

		// Empty returns generic
		{"", "generic"},

		// Unknown returns generic (not the unknown string)
		{"unknown-platform", "generic"},

		// Whitespace handling
		{"  go  ", "go"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := NormalizePlatform(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizePlatform(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsValidPlatform(t *testing.T) {
	ClearRegistry()

	// Available packs should be valid
	if !IsValidPlatform("go") {
		t.Error("'go' should be a valid platform")
	}
	if !IsValidPlatform("generic") {
		t.Error("'generic' should be a valid platform")
	}
	if !IsValidPlatform("python") {
		t.Error("'python' should be a valid platform")
	}
	if !IsValidPlatform("node") {
		t.Error("'node' should be a valid platform")
	}

	// Aliases for existing packs should resolve to valid platforms
	if !IsValidPlatform("golang") {
		t.Error("'golang' should resolve to valid 'go' platform")
	}
	if !IsValidPlatform("py") {
		t.Error("'py' should resolve to valid 'python' platform")
	}
	if !IsValidPlatform("typescript") {
		t.Error("'typescript' should resolve to valid 'node' platform")
	}

	// Aliases for non-existent packs are not valid
	if IsValidPlatform("rust") {
		t.Error("'rust' should not be valid (no rust pack exists)")
	}

	// Unknown platforms are not valid
	if IsValidPlatform("unknown-platform-xyz") {
		t.Error("'unknown-platform-xyz' should not be a valid platform")
	}
}

func TestGetPlatformList(t *testing.T) {
	ClearRegistry()
	list := GetPlatformList()

	// Should contain all packs with actual pack files
	if !strings.Contains(list, "go") {
		t.Errorf("Platform list should contain 'go', got: %s", list)
	}
	if !strings.Contains(list, "python") {
		t.Errorf("Platform list should contain 'python', got: %s", list)
	}
	if !strings.Contains(list, "node") {
		t.Errorf("Platform list should contain 'node', got: %s", list)
	}

	// Should NOT contain 'generic' (not advertised as a choice)
	if strings.Contains(list, "generic") {
		t.Errorf("Platform list should not advertise 'generic', got: %s", list)
	}
}
