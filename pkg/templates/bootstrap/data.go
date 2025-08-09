// Package bootstrap provides template data structures and rendering for bootstrap specifications.
package bootstrap

import (
	"fmt"
	"strings"

	"orchestrator/pkg/workspace"
)

const (
	// defaultPrivateModulePattern is the default GOPRIVATE pattern.
	defaultPrivateModulePattern = "github.com/myorg/*"
)

// TemplateData holds all data needed for bootstrap template rendering.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type TemplateData struct {
	ProjectName            string                                  `json:"project_name"`             // Name of the project
	Platform               string                                  `json:"platform"`                 // Detected platform (go, node, python, etc.)
	PlatformDisplayName    string                                  `json:"platform_display_name"`    // Human-readable platform name
	ContainerImage         string                                  `json:"container_image"`          // Docker image to use
	RecommendedContainer   string                                  `json:"recommended_container"`    // Platform-specific recommended container
	GoVersion              string                                  `json:"go_version,omitempty"`     // Detected or recommended Go version
	NeedsContainerUpgrade  bool                                    `json:"needs_container_upgrade"`  // True if container should be upgraded
	GitRepoURL             string                                  `json:"git_repo_url,omitempty"`   // Git repository URL for go mod init
	HasGoWorkspace         bool                                    `json:"has_go_workspace"`         // True if go.work file exists
	RequiresPrivateModules bool                                    `json:"requires_private_modules"` // True if private modules detected
	PrivateModulePattern   string                                  `json:"private_module_pattern"`   // GOPRIVATE pattern for private modules
	HasCGOCode             bool                                    `json:"has_cgo_code"`             // True if CGO imports detected
	NeedsGitHooks          bool                                    `json:"needs_git_hooks"`          // True if git hooks should be installed
	ShouldGenerateCI       bool                                    `json:"should_generate_ci"`       // True if CI pipeline stub needed
	FailuresByType         map[string][]workspace.BootstrapFailure `json:"failures_by_type"`         // Failures grouped by type
	FailuresByPriority     []workspace.BootstrapFailure            `json:"failures_by_priority"`     // Failures sorted by priority
	BuildSystemFailures    []workspace.BootstrapFailure            `json:"build_system_failures"`    // Makefile and build issues
	ContainerFailures      []workspace.BootstrapFailure            `json:"container_failures"`       // Container setup issues
	BinarySizeFailures     []workspace.BootstrapFailure            `json:"binary_size_failures"`     // Large file issues
	GitAccessFailures      []workspace.BootstrapFailure            `json:"git_access_failures"`      // Git repository issues
	InfrastructureFailures []workspace.BootstrapFailure            `json:"infrastructure_failures"`  // Maestro infrastructure issues
	ExternalToolFailures   []workspace.BootstrapFailure            `json:"external_tool_failures"`   // Missing external tools
	TotalFailures          int                                     `json:"total_failures"`           // Total number of failures
	HasCriticalFailures    bool                                    `json:"has_critical_failures"`    // True if any priority 1 failures exist
	RequiresNetworkAccess  bool                                    `json:"requires_network_access"`  // True if container needs network for setup
	TemplateName           string                                  `json:"template_name"`            // Name of template being used
	DockerfilePath         string                                  `json:"dockerfile_path"`          // Path to dockerfile if using dockerfile mode
}

// NewTemplateData creates a new TemplateData from verification results.
func NewTemplateData(projectName, platform, platformDisplayName, containerImage, gitRepoURL string, failures []workspace.BootstrapFailure) *TemplateData {
	data := &TemplateData{
		ProjectName:            projectName,
		Platform:               platform,
		PlatformDisplayName:    platformDisplayName,
		ContainerImage:         containerImage,
		GitRepoURL:             gitRepoURL,
		FailuresByType:         make(map[string][]workspace.BootstrapFailure),
		FailuresByPriority:     failures,
		BuildSystemFailures:    []workspace.BootstrapFailure{},
		ContainerFailures:      []workspace.BootstrapFailure{},
		BinarySizeFailures:     []workspace.BootstrapFailure{},
		GitAccessFailures:      []workspace.BootstrapFailure{},
		InfrastructureFailures: []workspace.BootstrapFailure{},
		ExternalToolFailures:   []workspace.BootstrapFailure{},
		TotalFailures:          len(failures),
		RequiresNetworkAccess:  false,
		NeedsContainerUpgrade:  false,
	}

	// Platform-specific initialization
	if platform == "go" {
		data.initializeGoSpecific()
	}

	// Group failures by type and category
	for i := range failures {
		failure := &failures[i]

		// Check for critical failures
		if failure.Priority == 1 {
			data.HasCriticalFailures = true
		}

		// Group by type string for template access
		typeStr := string(failure.Type)
		data.FailuresByType[typeStr] = append(data.FailuresByType[typeStr], *failure)

		// Categorize for easy template access
		switch failure.Type {
		case workspace.BootstrapFailureBuildSystem:
			data.BuildSystemFailures = append(data.BuildSystemFailures, *failure)
		case workspace.BootstrapFailureContainer:
			data.ContainerFailures = append(data.ContainerFailures, *failure)
			// Container issues might require network access to pull images
			data.RequiresNetworkAccess = true
		case workspace.BootstrapFailureBinarySize:
			data.BinarySizeFailures = append(data.BinarySizeFailures, *failure)
		case workspace.BootstrapFailureGitAccess:
			data.GitAccessFailures = append(data.GitAccessFailures, *failure)
		case workspace.BootstrapFailureInfrastructure:
			data.InfrastructureFailures = append(data.InfrastructureFailures, *failure)
		case workspace.BootstrapFailureExternalTools:
			data.ExternalToolFailures = append(data.ExternalToolFailures, *failure)
		}
	}

	// Sort failures by priority (1=highest)
	for i := 0; i < len(data.FailuresByPriority); i++ {
		for j := i + 1; j < len(data.FailuresByPriority); j++ {
			if data.FailuresByPriority[i].Priority > data.FailuresByPriority[j].Priority {
				data.FailuresByPriority[i], data.FailuresByPriority[j] = data.FailuresByPriority[j], data.FailuresByPriority[i]
			}
		}
	}

	return data
}

// GetPlatformDefaults returns platform-specific default values for template rendering.
func (d *TemplateData) GetPlatformDefaults() map[string]string {
	defaults := map[string]string{
		"build_command": "make build",
		"test_command":  "make test",
		"lint_command":  "make lint",
		"run_command":   "make run",
	}

	// Customize based on platform
	switch d.Platform {
	case "go":
		defaults["package_manager"] = "go mod"
		defaults["test_framework"] = "go test"
		defaults["lint_tool"] = "golangci-lint"
		defaults["binary_name"] = d.ProjectName
	case "node":
		defaults["package_manager"] = "npm"
		defaults["test_framework"] = "jest"
		defaults["lint_tool"] = "eslint"
		defaults["binary_name"] = "node"
	case "python":
		defaults["package_manager"] = "pip"
		defaults["test_framework"] = "pytest"
		defaults["lint_tool"] = "ruff"
		defaults["binary_name"] = "python"
	default:
		defaults["package_manager"] = "make"
		defaults["test_framework"] = "make test"
		defaults["lint_tool"] = "make lint"
		defaults["binary_name"] = d.ProjectName
	}

	return defaults
}

// HasFailuresOfType returns true if there are failures of the specified type.
func (d *TemplateData) HasFailuresOfType(failureType workspace.BootstrapFailureType) bool {
	typeStr := string(failureType)
	failures, exists := d.FailuresByType[typeStr]
	return exists && len(failures) > 0
}

// GetFailureDetails returns structured details for a specific failure type.
func (d *TemplateData) GetFailureDetails(failureType workspace.BootstrapFailureType) map[string]interface{} {
	details := make(map[string]interface{})
	typeStr := string(failureType)

	if failures, exists := d.FailuresByType[typeStr]; exists && len(failures) > 0 {
		details["count"] = len(failures)
		details["failures"] = failures

		// Extract common patterns from failure details
		actions := make(map[string]int)
		components := make(map[string]int)

		for i := range failures {
			failure := &failures[i]
			if action, ok := failure.Details["action"]; ok {
				actions[action]++
			}
			components[failure.Component]++
		}

		details["common_actions"] = actions
		details["affected_components"] = components
	}

	return details
}

// initializeGoSpecific sets up Go-specific template data including version detection and container recommendations.
func (d *TemplateData) initializeGoSpecific() {
	// Detect Go version using priority order
	d.GoVersion = detectGoVersion()

	// Set recommended container based on Go version and CGO requirements
	d.RecommendedContainer = d.selectOptimalContainer()

	// Check if container upgrade is needed
	d.NeedsContainerUpgrade = shouldUpgradeContainer(d.ContainerImage, d.RecommendedContainer)

	// Detect Go workspace and module features
	d.HasGoWorkspace = detectGoWorkspace()
	d.RequiresPrivateModules = detectPrivateModules()
	if d.RequiresPrivateModules {
		d.PrivateModulePattern = generatePrivateModulePattern(d.GitRepoURL)
	}

	// Detect CGO usage
	d.HasCGOCode = detectCGOUsage()

	// Check if git hooks and CI are needed
	d.NeedsGitHooks = true // Always recommend git hooks for Go projects
	d.ShouldGenerateCI = shouldGenerateCI()

	// If we need container upgrade, we'll need network access to pull new image
	if d.NeedsContainerUpgrade {
		d.RequiresNetworkAccess = true
	}
}

// detectGoVersion attempts to detect the appropriate Go version to use.
// Priority order: go.mod > dockerfile > config > internet > fallback.
func detectGoVersion() string {
	// 1. Check go.mod file for version directive (highest priority)
	if version := getGoVersionFromGoMod(); version != "" {
		return version
	}

	// 2. Check dockerfile for Go version (if using dockerfile)
	if version := getGoVersionFromDockerfile(); version != "" {
		return version
	}

	// 3. Check project config for go_version field
	if version := getGoVersionFromConfig(); version != "" {
		return version
	}

	// 4. Internet detection from go.dev API
	if version := getLatestGoVersionFromInternet(); version != "" {
		return version
	}

	// 5. Fallback: return empty string to indicate runtime detection needed
	return ""
}

// getGoVersionFromGoMod extracts Go version from go.mod file.
func getGoVersionFromGoMod() string {
	// TODO: Implement go.mod parsing in template generation context
	// For now, this would be handled by the coder agent at runtime
	// The template provides instructions for: `grep "^go " go.mod | awk '{print $2}'`
	return ""
}

// getGoVersionFromDockerfile extracts Go version from Dockerfile.
func getGoVersionFromDockerfile() string {
	// TODO: Implement dockerfile parsing in template generation context
	// For now, this would be handled by the coder agent at runtime
	// The template provides instructions for: `grep "FROM golang:" Dockerfile | sed 's/.*golang:\([0-9.]*\).*/\1/'`
	return ""
}

// getGoVersionFromConfig extracts Go version from project config.
func getGoVersionFromConfig() string {
	// TODO: Implement config parsing in template generation context
	// For now, this would be handled by the coder agent at runtime
	// The template provides instructions for: `jq -r '.go_version // empty' .maestro/config.json`
	return ""
}

// getLatestGoVersionFromInternet fetches latest Go version from go.dev API.
func getLatestGoVersionFromInternet() string {
	// TODO: Implement internet detection in template generation context
	// For now, this would be handled by the coder agent at runtime
	// The template provides instructions for: `curl -s 'https://go.dev/dl/?mode=json' | jq -r '[.[] | select(.stable)][0].version'`
	return ""
}

// selectOptimalContainer chooses the best container based on Go version and CGO requirements.
func (d *TemplateData) selectOptimalContainer() string {
	// If no Go version detected, use runtime detection placeholder
	if d.GoVersion == "" {
		if d.HasCGOCode {
			return "golang:latest" // Debian-based for CGO compatibility
		}
		return "golang:alpine" // Alpine for smaller size
	}

	baseImage := fmt.Sprintf("golang:%s", d.GoVersion)

	// If CGO is detected, use non-alpine image for glibc compatibility
	if d.HasCGOCode {
		return baseImage // golang:X.Y (debian-based)
	}

	// Default to alpine for smaller size
	return fmt.Sprintf("%s-alpine", baseImage)
}

// detectGoWorkspace checks if go.work file exists indicating multi-module workspace.
func detectGoWorkspace() bool {
	// This would check for go.work file in the project root
	// For template generation, return false - actual detection happens at runtime
	return false
}

// detectPrivateModules analyzes import paths to detect private module usage.
func detectPrivateModules() bool {
	// This would scan Go files for private module imports
	// For template generation, return false - actual detection happens at runtime
	return false
}

// generatePrivateModulePattern creates GOPRIVATE pattern from git repository URL.
func generatePrivateModulePattern(gitRepoURL string) string {
	// Extract domain from git URL for GOPRIVATE pattern
	// Example: git@github.com:myorg/repo.git -> github.com/myorg/*
	if gitRepoURL == "" {
		return defaultPrivateModulePattern // Default pattern
	}

	// Extract domain and organization from git URL
	// This is a simplified implementation - actual parsing would be more robust
	if strings.Contains(gitRepoURL, "github.com") {
		return defaultPrivateModulePattern // Would extract actual org from URL
	}
	if strings.Contains(gitRepoURL, "gitlab.com") {
		return "gitlab.com/myorg/*" // Would extract actual org from URL
	}

	// Default pattern for other domains
	return defaultPrivateModulePattern
}

// detectCGOUsage scans for CGO imports in Go source files.
func detectCGOUsage() bool {
	// This would scan for `import "C"` in .go files
	// For template generation, return false - actual detection happens at runtime
	return false
}

// shouldGenerateCI determines if CI pipeline stub should be generated.
func shouldGenerateCI() bool {
	// This would check if .github/workflows/ directory exists
	// For template generation, return true to always recommend CI
	return true
}

// shouldUpgradeContainer determines if the current container should be upgraded.
func shouldUpgradeContainer(currentImage, recommendedImage string) bool {
	// Always upgrade if current image is empty (dockerfile mode - container needs to be built)
	if currentImage == "" {
		return true
	}

	// Don't upgrade if already using the recommended image
	if currentImage == recommendedImage {
		return false
	}

	// Upgrade if using generic containers (ubuntu, alpine, etc.)
	genericContainers := []string{
		"ubuntu:22.04",
		"ubuntu:24.04",
		"alpine:latest",
		"alpine:3.18",
		"alpine:3.19",
	}

	for _, generic := range genericContainers {
		if currentImage == generic {
			return true
		}
	}

	// Upgrade if using older Go version
	if strings.HasPrefix(currentImage, "golang:") && currentImage != recommendedImage {
		return true
	}

	return false
}

// GetGoSpecificCommands returns Go-specific commands for the template.
func (d *TemplateData) GetGoSpecificCommands() map[string]string {
	if d.Platform != "go" {
		return map[string]string{}
	}

	return map[string]string{
		"mod_init":           "go mod init",
		"mod_tidy":           "go mod tidy",
		"work_sync":          "go work sync",
		"version_check":      "go version",
		"latest_version_cmd": `curl -s 'https://go.dev/dl/?mode=json' | jq -r '[.[] | select(.stable)][0].version'`,
		"install_golangci":   "go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest",
		"golangci_version":   "golangci-lint --version",
		"container_validate": `curl -fs "https://registry.hub.docker.com/v2/repositories/library/golang/tags/{{.GoVersion}}-alpine"`,
		"install_hooks":      "make install-githooks",
	}
}

// GetDockerfilePath returns the dockerfile path from the container configuration.
func (d *TemplateData) GetDockerfilePath() string {
	if d.DockerfilePath != "" {
		return d.DockerfilePath
	}
	// Fallback to default
	return "Dockerfile"
}

// GetGoLintConfig returns the golangci-lint configuration to include in the template.
func (d *TemplateData) GetGoLintConfig() string {
	if d.Platform != "go" {
		return ""
	}

	// Based on maestro's aggressive linting configuration
	return `# .golangci.yml - Aggressive linting configuration
run:
  timeout: 5m
  
linters-settings:
  cyclop:
    max-complexity: 15
  gocognit:
    min-complexity: 15
  gocritic:
    enabled-tags:
      - diagnostic
      - experimental
      - opinionated
      - performance
      - style
  govet:
    check-shadowing: true
    fieldalignment: true
  misspell:
    locale: US
  revive:
    min-confidence: 0

linters:
  enable:
    - cyclop
    - errcheck
    - gocognit
    - gocritic
    - gofmt
    - goimports
    - golint
    - gosec
    - gosimple
    - govet
    - ineffassign
    - misspell
    - revive
    - staticcheck
    - stylecheck
    - typecheck
    - unconvert
    - unused
    - vet
    - vetshadow
  disable:
    - deadcode
    - structcheck
    - varcheck

issues:
  exclude-use-default: false
  max-issues-per-linter: 0
  max-same-issues: 0`
}
