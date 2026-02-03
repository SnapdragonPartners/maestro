// Package loopback provides a linter that detects localhost/loopback references
// in .env and compose files. In containerized environments, loopback addresses
// typically point to the wrong network namespace and should reference service names.
package loopback

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Finding represents a single loopback reference found during linting.
type Finding struct {
	Content string // The line content
	File    string // Relative path to the file
	Pattern string // The matched loopback pattern (e.g., "localhost", "127.0.0.1")
	Line    int    // 1-indexed line number
}

// Result contains the linting results.
type Result struct {
	Findings        []Finding // All loopback references found
	ComposeServices []string  // Service names extracted from compose files (for suggestions)
	ScannedFiles    []string  // Files that were scanned
}

// HasFindings returns true if any loopback references were found.
func (r *Result) HasFindings() bool {
	return len(r.Findings) > 0
}

// FormatError formats the findings as an error message suitable for LLM feedback.
func (r *Result) FormatError() string {
	if !r.HasFindings() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Loopback/localhost references found in configuration files:\n\n")

	for i := range r.Findings {
		f := &r.Findings[i]
		sb.WriteString(fmt.Sprintf("  %s:%d - found '%s'\n", f.File, f.Line, f.Pattern))
		sb.WriteString(fmt.Sprintf("    > %s\n\n", strings.TrimSpace(f.Content)))
	}

	sb.WriteString("In Docker Compose environments, loopback addresses (localhost, 127.0.0.1, ::1) ")
	sb.WriteString("point to the container itself, not other services.\n\n")

	if len(r.ComposeServices) > 0 {
		sb.WriteString(fmt.Sprintf("Available compose service names: %s\n\n", strings.Join(r.ComposeServices, ", ")))
	}

	sb.WriteString("Fix: Replace loopback addresses with the appropriate compose service name, ")
	sb.WriteString("or add `# nolint:localhost (reason)` to suppress if intentional.")

	return sb.String()
}

// Linter scans files for loopback references.
type Linter struct {
	workspacePath string
}

// NewLinter creates a new loopback linter for the given workspace.
func NewLinter(workspacePath string) *Linter {
	return &Linter{workspacePath: workspacePath}
}

// Regex patterns for loopback detection.
//
//nolint:gochecknoglobals // Package-level compiled regexes for performance.
var (
	// loopbackRegex matches loopback patterns we flag as errors.
	// Does NOT include 0.0.0.0 (common bind address).
	// The pattern uses word boundaries for most cases, and a separate check for ::1.
	loopbackRegex = regexp.MustCompile(`\b(localhost|127\.0\.0\.1|127\.0\.1\.1)\b`)

	// ipv6LoopbackRegex matches the IPv6 loopback address ::1.
	// Uses lookaround-like patterns since \b doesn't work with colons.
	ipv6LoopbackRegex = regexp.MustCompile(`(^|[^:])::1(\D|$)`)

	// nolintRegex matches the nolint:localhost directive with required whitespace before #.
	nolintRegex = regexp.MustCompile(`\s#\s*nolint:localhost`)

	// envFilePatterns are glob patterns for .env files.
	envFilePatterns = []string{
		".env",
		".env.*",
		"*.env",
	}

	// composeFilePatterns are glob patterns for compose files.
	composeFilePatterns = []string{
		"compose.yml",
		"compose.yaml",
		"docker-compose.yml",
		"docker-compose.yaml",
		"docker-compose.*.yml",
		"docker-compose.*.yaml",
		".maestro/compose.yml",
		".maestro/compose.yaml",
	}
)

// findLoopbackPattern checks a string for loopback patterns and returns the matched pattern.
// Returns empty string if no loopback pattern is found.
func findLoopbackPattern(s string) string {
	// Check standard loopback patterns (localhost, 127.x.x.x)
	if match := loopbackRegex.FindString(s); match != "" {
		return match
	}
	// Check IPv6 loopback (::1)
	if ipv6LoopbackRegex.MatchString(s) {
		return "::1"
	}
	return ""
}

// ScanChangedFiles scans only files that changed in the branch (vs origin/main).
// changedFiles should be relative paths from the workspace root.
func (l *Linter) ScanChangedFiles(changedFiles []string) (*Result, error) {
	result := &Result{
		Findings:        []Finding{},
		ComposeServices: []string{},
		ScannedFiles:    []string{},
	}

	// Separate files into env files and compose files
	var envFiles, composeFiles []string
	for _, f := range changedFiles {
		if l.isEnvFile(f) {
			envFiles = append(envFiles, f)
		}
		if l.isComposeFile(f) {
			composeFiles = append(composeFiles, f)
		}
	}

	// If neither env nor compose files changed, nothing to scan
	if len(envFiles) == 0 && len(composeFiles) == 0 {
		return result, nil
	}

	// Extract compose service names for helpful suggestions
	// Scan ALL compose files in workspace (not just changed ones) to get full service list
	allComposeFiles := l.findAllComposeFiles()
	for _, cf := range allComposeFiles {
		services, err := l.extractComposeServices(cf)
		if err == nil {
			result.ComposeServices = append(result.ComposeServices, services...)
		}
	}
	// Deduplicate services
	result.ComposeServices = uniqueStrings(result.ComposeServices)

	// Scan changed .env files (full scan)
	for _, ef := range envFiles {
		fullPath := filepath.Join(l.workspacePath, ef)
		findings, err := l.scanEnvFile(fullPath, ef)
		if err != nil {
			continue // Skip files that can't be read
		}
		result.Findings = append(result.Findings, findings...)
		result.ScannedFiles = append(result.ScannedFiles, ef)
	}

	// Scan changed compose files (only environment sections)
	for _, cf := range composeFiles {
		fullPath := filepath.Join(l.workspacePath, cf)
		findings, err := l.scanComposeFile(fullPath, cf)
		if err != nil {
			continue // Skip files that can't be read
		}
		result.Findings = append(result.Findings, findings...)
		result.ScannedFiles = append(result.ScannedFiles, cf)
	}

	return result, nil
}

// isEnvFile checks if a path matches env file patterns.
func (l *Linter) isEnvFile(path string) bool {
	base := filepath.Base(path)
	for _, pattern := range envFilePatterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
	}
	return false
}

// isComposeFile checks if a path matches compose file patterns.
func (l *Linter) isComposeFile(path string) bool {
	// Check both base name and full relative path for .maestro/ patterns
	base := filepath.Base(path)
	for _, pattern := range composeFilePatterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
	}
	return false
}

// findAllComposeFiles finds all compose files in the workspace.
func (l *Linter) findAllComposeFiles() []string {
	var files []string
	for _, pattern := range composeFilePatterns {
		matches, err := filepath.Glob(filepath.Join(l.workspacePath, pattern))
		if err != nil {
			continue
		}
		files = append(files, matches...)
	}
	return files
}

// scanEnvFile scans a .env file for loopback references.
func (l *Linter) scanEnvFile(fullPath, relativePath string) ([]Finding, error) {
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var findings []Finding
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Skip blank lines
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Skip full-line comments
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Only scan lines with assignments
		if !strings.Contains(line, "=") {
			continue
		}

		// Check for nolint directive (requires whitespace before #)
		if nolintRegex.MatchString(line) {
			continue
		}

		// Check for loopback patterns
		if match := findLoopbackPattern(line); match != "" {
			findings = append(findings, Finding{
				File:    relativePath,
				Line:    lineNum,
				Content: line,
				Pattern: match,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return findings, fmt.Errorf("scan env file: %w", err)
	}
	return findings, nil
}

// scanComposeFile scans a compose file for loopback references in environment sections only.
func (l *Linter) scanComposeFile(fullPath, relativePath string) ([]Finding, error) {
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}

	// Parse YAML to extract environment values
	var compose composeFile
	if err := yaml.Unmarshal(content, &compose); err != nil {
		return nil, fmt.Errorf("parse compose yaml: %w", err)
	}

	var findings []Finding

	for serviceName := range compose.Services {
		service := compose.Services[serviceName]
		// Scan environment map
		for key, value := range service.Environment {
			if match := findLoopbackPattern(value); match != "" {
				// Check for nolint in the value (less common but supported)
				if nolintRegex.MatchString(value) {
					continue
				}
				findings = append(findings, Finding{
					File:    relativePath,
					Line:    0, // Line numbers not tracked for YAML parsing
					Content: fmt.Sprintf("services.%s.environment.%s: %s", serviceName, key, value),
					Pattern: match,
				})
			}
		}

		// Scan environment list (alternative format)
		for _, envLine := range service.EnvironmentList {
			if match := findLoopbackPattern(envLine); match != "" {
				if nolintRegex.MatchString(envLine) {
					continue
				}
				findings = append(findings, Finding{
					File:    relativePath,
					Line:    0,
					Content: fmt.Sprintf("services.%s.environment: %s", serviceName, envLine),
					Pattern: match,
				})
			}
		}
	}

	return findings, nil
}

// extractComposeServices extracts service names from a compose file.
func (l *Linter) extractComposeServices(fullPath string) ([]string, error) {
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}

	var compose composeFile
	if err := yaml.Unmarshal(content, &compose); err != nil {
		return nil, fmt.Errorf("parse compose yaml: %w", err)
	}

	services := make([]string, 0, len(compose.Services))
	for name := range compose.Services {
		services = append(services, name)
	}
	return services, nil
}

// composeFile represents the structure we care about in compose files.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

// composeService represents a service definition (only fields we scan).
type composeService struct {
	Environment     map[string]string `yaml:"environment,omitempty"`
	EnvironmentList []string          `yaml:"-"` // Alternative list format, handled by custom unmarshal
}

// UnmarshalYAML handles both map and list formats for environment.
func (s *composeService) UnmarshalYAML(node *yaml.Node) error {
	// Create a temporary struct for normal unmarshaling
	type rawService struct {
		Environment yaml.Node `yaml:"environment"`
	}

	var raw rawService
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("decode service: %w", err)
	}

	// Handle environment based on its type
	switch raw.Environment.Kind {
	case yaml.MappingNode:
		s.Environment = make(map[string]string)
		if err := raw.Environment.Decode(&s.Environment); err != nil {
			return fmt.Errorf("decode environment map: %w", err)
		}
	case yaml.SequenceNode:
		if err := raw.Environment.Decode(&s.EnvironmentList); err != nil {
			return fmt.Errorf("decode environment list: %w", err)
		}
	}

	return nil
}

// uniqueStrings removes duplicates from a string slice.
func uniqueStrings(slice []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
