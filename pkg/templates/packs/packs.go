// Package packs provides language/platform pack loading, validation, and token replacement
// for bootstrap templates and future system prompts.
package packs

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

//go:embed *.json
var packsFS embed.FS

// AllowedTokens defines the tokens that can be used in pack strings.
// These are replaced at render time with values from TemplateData.
//
//nolint:gochecknoglobals // Package-level constant list.
var AllowedTokens = []string{
	"${PROJECT_NAME}",
	"${LANGUAGE_VERSION}",
}

// tokenPattern matches token placeholders like ${TOKEN_NAME}.
var tokenPattern = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

// Pack represents a language/platform pack with configuration for bootstrap templates.
type Pack struct {
	Name            string `json:"name"`
	DisplayName     string `json:"display_name"`
	Version         string `json:"version"`
	LanguageVersion string `json:"language_version,omitempty"`

	RecommendedBaseImage string `json:"recommended_base_image,omitempty"`

	Tooling          Tooling          `json:"tooling"`
	MakefileTargets  MakefileTargets  `json:"makefile_targets"`
	TemplateSections TemplateSections `json:"template_sections"`
}

// Tooling contains informational tool names for a platform.
type Tooling struct {
	PackageManager string `json:"package_manager,omitempty"`
	Linter         string `json:"linter,omitempty"`
	TestFramework  string `json:"test_framework,omitempty"`
	Formatter      string `json:"formatter,omitempty"`
}

// MakefileTargets contains the commands that go INTO Makefile targets.
// Build, Test, Lint, and Run are required for proper Maestro functionality.
type MakefileTargets struct {
	Build   string `json:"build"`
	Test    string `json:"test"`
	Lint    string `json:"lint"`
	Run     string `json:"run"`
	Clean   string `json:"clean,omitempty"`
	Install string `json:"install,omitempty"`
}

// TemplateSections contains markdown fragments for insertion into the bootstrap template.
type TemplateSections struct {
	ModuleSetup  string `json:"module_setup,omitempty"`
	LintConfig   string `json:"lint_config,omitempty"`
	QualitySetup string `json:"quality_setup,omitempty"`
}

// TokenValues holds the values for token replacement.
type TokenValues struct {
	ProjectName     string
	LanguageVersion string
}

// ValidationResult contains the result of pack validation.
type ValidationResult struct {
	Warnings []string
	Errors   []string
	Valid    bool
}

// registry holds loaded packs by name.
//
//nolint:gochecknoglobals // Package-level cache for loaded packs.
var registry = make(map[string]*Pack)

// genericPack is cached for fallback scenarios.
//
//nolint:gochecknoglobals // Package-level cache for generic fallback.
var genericPack *Pack

// Load reads and parses a pack from the embedded filesystem.
func Load(name string) (*Pack, error) {
	filename := name + ".json"
	data, err := packsFS.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("pack %q not found: %w", name, err)
	}

	var pack Pack
	if err := json.Unmarshal(data, &pack); err != nil {
		return nil, fmt.Errorf("failed to parse pack %q: %w", name, err)
	}

	return &pack, nil
}

// Get retrieves a pack by name, loading it if necessary.
// Returns the generic pack with warnings if the requested pack is invalid or not found.
func Get(name string) (*Pack, []string, error) {
	// Check cache first
	if pack, exists := registry[name]; exists {
		return pack, nil, nil
	}

	// Try to load the pack
	pack, err := Load(name)
	if err != nil {
		// Fallback to generic
		generic, fallbackErr := GetGeneric()
		if fallbackErr != nil {
			return nil, nil, fmt.Errorf("pack %q not found and generic fallback failed: %w", name, fallbackErr)
		}
		return generic, []string{fmt.Sprintf("Pack %q not found, using generic pack", name)}, nil
	}

	// Validate the pack
	result := Validate(pack)
	if !result.Valid {
		// Fallback to generic
		generic, fallbackErr := GetGeneric()
		if fallbackErr != nil {
			return nil, nil, fmt.Errorf("pack %q invalid and generic fallback failed: %w", name, fallbackErr)
		}
		warnings := append([]string{fmt.Sprintf("Pack %q invalid, using generic pack", name)}, result.Errors...)
		return generic, warnings, nil
	}

	// Cache and return
	registry[name] = pack
	return pack, result.Warnings, nil
}

// GetGeneric returns the generic fallback pack.
func GetGeneric() (*Pack, error) {
	if genericPack != nil {
		return genericPack, nil
	}

	pack, err := Load("generic")
	if err != nil {
		return nil, fmt.Errorf("generic pack not found: %w", err)
	}

	// Validate generic pack - it must be valid
	result := Validate(pack)
	if !result.Valid {
		return nil, fmt.Errorf("generic pack is invalid: %v", result.Errors)
	}

	genericPack = pack
	return genericPack, nil
}

// ListAvailable returns the names of all available packs.
func ListAvailable() ([]string, error) {
	entries, err := packsFS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("failed to read packs directory: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".json") {
			names = append(names, strings.TrimSuffix(name, ".json"))
		}
	}
	return names, nil
}

// PlatformGeneric is the canonical name for the generic/fallback platform.
const PlatformGeneric = "generic"

// platformAliases maps common platform name variations to canonical pack names.
// The canonical names correspond to available pack JSON files.
//
//nolint:gochecknoglobals // Package-level constant map.
var platformAliases = map[string]string{
	// Go aliases
	"golang": "go",
	"go":     "go",

	// Python aliases
	"python":  "python",
	"python3": "python",
	"py":      "python",
	"py3":     "python",

	// Node.js aliases
	"node":       "node",
	"nodejs":     "node",
	"javascript": "node",
	"js":         "node",
	"typescript": "node",
	"ts":         "node",

	// Rust aliases
	"rust": "rust",
	"rs":   "rust",

	// Generic fallback
	"generic": PlatformGeneric,
}

// NormalizePlatform converts a user-provided platform string to a canonical pack name.
// Always returns a valid pack name: either a canonical name with an available pack,
// or "generic" as the fallback. This ensures config only stores valid pack IDs.
func NormalizePlatform(input string) string {
	if input == "" {
		return PlatformGeneric
	}

	// Normalize to lowercase
	normalized := strings.ToLower(strings.TrimSpace(input))

	// Check alias map first to get canonical name
	canonical := normalized
	if mapped, ok := platformAliases[normalized]; ok {
		canonical = mapped
	}

	// Always verify the canonical name has an available pack
	available, err := ListAvailable()
	if err != nil {
		// If we can't list packs, fall back to generic for safety
		return PlatformGeneric
	}

	if slices.Contains(available, canonical) {
		return canonical
	}

	// No pack available for this platform - fall back to generic
	return PlatformGeneric
}

// IsValidPlatform checks if a platform name has a dedicated pack (not just fallback to generic).
// Returns true only if a specific pack exists for the platform or its alias target.
func IsValidPlatform(input string) bool {
	if input == "" {
		return false
	}

	// Normalize to lowercase and check alias
	normalized := strings.ToLower(strings.TrimSpace(input))
	canonical := normalized
	if mapped, ok := platformAliases[normalized]; ok {
		canonical = mapped
	}

	// Check if this canonical name has an available pack
	available, err := ListAvailable()
	if err != nil {
		return false
	}

	return slices.Contains(available, canonical)
}

// GetPlatformList returns a formatted string of available platforms for user prompts.
// Only includes platforms that have actual packs (excludes generic as it's the fallback).
func GetPlatformList() string {
	available, err := ListAvailable()
	if err != nil {
		return "go" // Fallback to known pack
	}

	// Build list with aliases shown (pre-allocate for efficiency)
	parts := make([]string, 0, len(available))
	for _, name := range available {
		if name == PlatformGeneric {
			continue // Don't advertise generic as a choice
		}
		parts = append(parts, name)
	}

	if len(parts) == 0 {
		return PlatformGeneric
	}

	return strings.Join(parts, ", ")
}

// Validate checks a pack for required fields and valid token usage.
func Validate(pack *Pack) ValidationResult {
	result := ValidationResult{Valid: true}

	// Check required fields
	if pack.Name == "" {
		result.Errors = append(result.Errors, "missing required field: name")
		result.Valid = false
	}
	if pack.Version == "" {
		result.Errors = append(result.Errors, "missing required field: version")
		result.Valid = false
	}
	if pack.DisplayName == "" {
		result.Errors = append(result.Errors, "missing required field: display_name")
		result.Valid = false
	}
	if pack.MakefileTargets.Build == "" {
		result.Errors = append(result.Errors, "missing required field: makefile_targets.build")
		result.Valid = false
	}
	if pack.MakefileTargets.Test == "" {
		result.Errors = append(result.Errors, "missing required field: makefile_targets.test")
		result.Valid = false
	}
	if pack.MakefileTargets.Lint == "" {
		result.Errors = append(result.Errors, "missing required field: makefile_targets.lint")
		result.Valid = false
	}
	if pack.MakefileTargets.Run == "" {
		result.Errors = append(result.Errors, "missing required field: makefile_targets.run")
		result.Valid = false
	}

	// Validate token usage in all string fields that support tokens
	tokenFields := map[string]string{
		"recommended_base_image":          pack.RecommendedBaseImage,
		"makefile_targets.build":          pack.MakefileTargets.Build,
		"makefile_targets.test":           pack.MakefileTargets.Test,
		"makefile_targets.lint":           pack.MakefileTargets.Lint,
		"makefile_targets.run":            pack.MakefileTargets.Run,
		"makefile_targets.clean":          pack.MakefileTargets.Clean,
		"makefile_targets.install":        pack.MakefileTargets.Install,
		"template_sections.module_setup":  pack.TemplateSections.ModuleSetup,
		"template_sections.lint_config":   pack.TemplateSections.LintConfig,
		"template_sections.quality_setup": pack.TemplateSections.QualitySetup,
	}

	for field, value := range tokenFields {
		if errs := validateTokens(field, value); len(errs) > 0 {
			result.Errors = append(result.Errors, errs...)
			result.Valid = false
		}
	}

	// Check for ${LANGUAGE_VERSION} usage without language_version defined
	if pack.LanguageVersion == "" {
		for field, value := range tokenFields {
			if strings.Contains(value, "${LANGUAGE_VERSION}") {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("field %q uses ${LANGUAGE_VERSION} but pack has no language_version defined", field))
			}
		}
	}

	return result
}

// validateTokens checks that all tokens in a string are in the allowed list.
func validateTokens(field, value string) []string {
	if value == "" {
		return nil
	}

	var errors []string
	matches := tokenPattern.FindAllStringSubmatch(value, -1)
	for _, match := range matches {
		token := "${" + match[1] + "}"
		if !isAllowedToken(token) {
			errors = append(errors, fmt.Sprintf("field %q contains unknown token %q", field, token))
		}
	}
	return errors
}

// isAllowedToken checks if a token is in the allowed list.
func isAllowedToken(token string) bool {
	return slices.Contains(AllowedTokens, token)
}

// Rendered returns a copy of the pack with all tokens replaced using the provided values.
// Returns an error if a required token value is missing.
func (p *Pack) Rendered(values TokenValues) (*Pack, error) {
	// Build replacement map
	replacements := map[string]string{
		"${PROJECT_NAME}":     values.ProjectName,
		"${LANGUAGE_VERSION}": values.LanguageVersion,
	}

	// Determine effective language version: prefer provided value, fall back to pack default
	effectiveLanguageVersion := values.LanguageVersion
	if effectiveLanguageVersion == "" && p.LanguageVersion != "" {
		effectiveLanguageVersion = p.LanguageVersion
	}
	replacements["${LANGUAGE_VERSION}"] = effectiveLanguageVersion

	// Create a copy with tokens replaced
	rendered := *p
	rendered.Tooling = p.Tooling
	// Update LanguageVersion to the effective value used for replacements
	rendered.LanguageVersion = effectiveLanguageVersion

	var err error
	if rendered.RecommendedBaseImage, err = replaceTokens(p.RecommendedBaseImage, replacements); err != nil {
		return nil, fmt.Errorf("recommended_base_image: %w", err)
	}

	if rendered.MakefileTargets, err = p.MakefileTargets.withReplacements(replacements); err != nil {
		return nil, err
	}

	if rendered.TemplateSections, err = p.TemplateSections.withReplacements(replacements); err != nil {
		return nil, err
	}

	// Verify no unrendered tokens remain
	if err := rendered.verifyNoUnrenderedTokens(); err != nil {
		return nil, err
	}

	return &rendered, nil
}

// replaceTokens performs token replacement on a string.
func replaceTokens(s string, replacements map[string]string) (string, error) {
	result := s
	for token, value := range replacements {
		if strings.Contains(result, token) && value == "" {
			return "", fmt.Errorf("token %s used but value is empty", token)
		}
		result = strings.ReplaceAll(result, token, value)
	}
	return result, nil
}

// withReplacements returns a copy of MakefileTargets with tokens replaced.
func (m *MakefileTargets) withReplacements(replacements map[string]string) (MakefileTargets, error) {
	var err error
	result := *m

	if result.Build, err = replaceTokens(m.Build, replacements); err != nil {
		return result, fmt.Errorf("makefile_targets.build: %w", err)
	}
	if result.Test, err = replaceTokens(m.Test, replacements); err != nil {
		return result, fmt.Errorf("makefile_targets.test: %w", err)
	}
	if result.Lint, err = replaceTokens(m.Lint, replacements); err != nil {
		return result, fmt.Errorf("makefile_targets.lint: %w", err)
	}
	if result.Run, err = replaceTokens(m.Run, replacements); err != nil {
		return result, fmt.Errorf("makefile_targets.run: %w", err)
	}
	if result.Clean, err = replaceTokens(m.Clean, replacements); err != nil {
		return result, fmt.Errorf("makefile_targets.clean: %w", err)
	}
	if result.Install, err = replaceTokens(m.Install, replacements); err != nil {
		return result, fmt.Errorf("makefile_targets.install: %w", err)
	}

	return result, nil
}

// withReplacements returns a copy of TemplateSections with tokens replaced.
func (t TemplateSections) withReplacements(replacements map[string]string) (TemplateSections, error) {
	var err error
	result := t

	if result.ModuleSetup, err = replaceTokens(t.ModuleSetup, replacements); err != nil {
		return result, fmt.Errorf("template_sections.module_setup: %w", err)
	}
	if result.LintConfig, err = replaceTokens(t.LintConfig, replacements); err != nil {
		return result, fmt.Errorf("template_sections.lint_config: %w", err)
	}
	if result.QualitySetup, err = replaceTokens(t.QualitySetup, replacements); err != nil {
		return result, fmt.Errorf("template_sections.quality_setup: %w", err)
	}

	return result, nil
}

// verifyNoUnrenderedTokens checks that no ${...} tokens remain in the rendered pack.
func (p *Pack) verifyNoUnrenderedTokens() error {
	fields := map[string]string{
		"recommended_base_image":          p.RecommendedBaseImage,
		"makefile_targets.build":          p.MakefileTargets.Build,
		"makefile_targets.test":           p.MakefileTargets.Test,
		"makefile_targets.lint":           p.MakefileTargets.Lint,
		"makefile_targets.run":            p.MakefileTargets.Run,
		"makefile_targets.clean":          p.MakefileTargets.Clean,
		"makefile_targets.install":        p.MakefileTargets.Install,
		"template_sections.module_setup":  p.TemplateSections.ModuleSetup,
		"template_sections.lint_config":   p.TemplateSections.LintConfig,
		"template_sections.quality_setup": p.TemplateSections.QualitySetup,
	}

	for name, value := range fields {
		if strings.Contains(value, "${") {
			matches := tokenPattern.FindAllString(value, -1)
			return fmt.Errorf("field %q contains unrendered tokens: %v", name, matches)
		}
	}
	return nil
}

// ClearRegistry clears the pack cache (useful for testing).
func ClearRegistry() {
	registry = make(map[string]*Pack)
	genericPack = nil
}
