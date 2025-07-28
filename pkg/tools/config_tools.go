package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"orchestrator/pkg/config"
)

// ModifyConfigTool provides MCP interface for updating project configuration.
type ModifyConfigTool struct{}

// NewModifyConfigTool creates a new modify config tool instance.
func NewModifyConfigTool() *ModifyConfigTool {
	return &ModifyConfigTool{}
}

// Definition returns the tool's definition in Claude API format.
func (m *ModifyConfigTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "modify_config",
		Description: "Update the project configuration file (.maestro/config.json) with validation results",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory containing .maestro/config.json (defaults to current directory)",
				},
				"bootstrap_completed": {
					Type:        "boolean",
					Description: "Mark bootstrap as completed",
				},
				"requirements_met": {
					Type:        "object",
					Description: "Map of requirement names to completion status (e.g., {'makefile_validated': true})",
				},
				"validation_errors": {
					Type:        "array",
					Description: "List of validation error messages",
					Items: &Property{
						Type: "string",
					},
				},
				"container_dockerfile_hash": {
					Type:        "string",
					Description: "MD5 hash of generated/validated Dockerfile",
				},
				"container_image_tag": {
					Type:        "string",
					Description: "Docker image tag for built container",
				},
				"container_last_built": {
					Type:        "string",
					Description: "ISO timestamp when container was last built",
				},
				"container_needs_rebuild": {
					Type:        "boolean",
					Description: "Whether container needs rebuilding",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (m *ModifyConfigTool) Name() string {
	return "modify_config"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (m *ModifyConfigTool) PromptDocumentation() string {
	return `- **modify_config** - Update project configuration with validation results
  - Parameters: 
    - cwd (optional): working directory
    - bootstrap_completed (optional): mark bootstrap as done
    - requirements_met (optional): map of requirement statuses
    - validation_errors (optional): list of error messages  
    - container_* (optional): container build information
  - Updates .maestro/config.json with bootstrap progress and validation results
  - Use during bootstrap to track completion status`
}

// Exec executes the config modification operation.
func (m *ModifyConfigTool) Exec(_ context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd, err := extractWorkingDirectory(args)
	if err != nil {
		return nil, err
	}

	// Load existing project configuration
	projectConfig, err := config.LoadProjectConfig(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to load project config: %w", err)
	}

	// Apply modifications
	modified := m.applyConfigModifications(projectConfig, args)

	// Save configuration if modified
	if modified {
		if err := projectConfig.Save(cwd); err != nil {
			return nil, fmt.Errorf("failed to save project config: %w", err)
		}
	}

	return map[string]any{
		"success":     true,
		"modified":    modified,
		"message":     "Project configuration updated successfully",
		"config_path": filepath.Join(cwd, config.ProjectConfigDir, config.ProjectConfigFilename),
	}, nil
}

// extractWorkingDirectory extracts and validates the working directory from args.
func extractWorkingDirectory(args map[string]any) (string, error) {
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	if cwd == "" {
		var err error
		cwd, err = filepath.Abs(".")
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	return cwd, nil
}

// applyConfigModifications applies all config modifications and returns whether any changes were made.
func (m *ModifyConfigTool) applyConfigModifications(projectConfig *config.ProjectConfig, args map[string]any) bool {
	modified := false

	// Bootstrap completion
	if m.updateBootstrapCompletion(projectConfig, args) {
		modified = true
	}

	// Requirements met
	if m.updateRequirementsMet(projectConfig, args) {
		modified = true
	}

	// Validation errors
	if m.updateValidationErrors(projectConfig, args) {
		modified = true
	}

	// Container information
	if m.updateContainerInfo(projectConfig, args) {
		modified = true
	}

	return modified
}

// updateBootstrapCompletion updates bootstrap completion status.
func (m *ModifyConfigTool) updateBootstrapCompletion(projectConfig *config.ProjectConfig, args map[string]any) bool {
	if completed, hasCompleted := args["bootstrap_completed"]; hasCompleted {
		if completedBool, ok := completed.(bool); ok {
			projectConfig.Bootstrap.Completed = completedBool
			if completedBool {
				projectConfig.Bootstrap.LastRun = time.Now()
			}
			return true
		}
	}
	return false
}

// updateRequirementsMet updates requirements met status.
func (m *ModifyConfigTool) updateRequirementsMet(projectConfig *config.ProjectConfig, args map[string]any) bool {
	if requirements, hasRequirements := args["requirements_met"]; hasRequirements {
		if reqMap, ok := requirements.(map[string]any); ok {
			if projectConfig.Bootstrap.RequirementsMet == nil {
				projectConfig.Bootstrap.RequirementsMet = make(map[string]bool)
			}
			modified := false
			for key, value := range reqMap {
				if boolVal, ok := value.(bool); ok {
					projectConfig.Bootstrap.RequirementsMet[key] = boolVal
					modified = true
				}
			}
			return modified
		}
	}
	return false
}

// updateValidationErrors updates validation errors list.
func (m *ModifyConfigTool) updateValidationErrors(projectConfig *config.ProjectConfig, args map[string]any) bool {
	if errors, hasErrors := args["validation_errors"]; hasErrors {
		if errorList, ok := errors.([]any); ok {
			projectConfig.Bootstrap.ValidationErrors = []string{}
			for _, err := range errorList {
				if errStr, ok := err.(string); ok {
					projectConfig.Bootstrap.ValidationErrors = append(projectConfig.Bootstrap.ValidationErrors, errStr)
				}
			}
			return true
		}
	}
	return false
}

// updateContainerInfo updates container-related information.
func (m *ModifyConfigTool) updateContainerInfo(projectConfig *config.ProjectConfig, args map[string]any) bool {
	modified := false

	if hash, hasHash := args["container_dockerfile_hash"]; hasHash {
		if hashStr, ok := hash.(string); ok {
			projectConfig.Container.DockerfileHash = hashStr
			modified = true
		}
	}

	if tag, hasTag := args["container_image_tag"]; hasTag {
		if tagStr, ok := tag.(string); ok {
			projectConfig.Container.ImageTag = tagStr
			modified = true
		}
	}

	if lastBuilt, hasLastBuilt := args["container_last_built"]; hasLastBuilt {
		if lastBuiltStr, ok := lastBuilt.(string); ok {
			if timestamp, err := time.Parse(time.RFC3339, lastBuiltStr); err == nil {
				projectConfig.Container.LastBuilt = timestamp
				modified = true
			}
		}
	}

	if needsRebuild, hasNeedsRebuild := args["container_needs_rebuild"]; hasNeedsRebuild {
		if needsRebuildBool, ok := needsRebuild.(bool); ok {
			projectConfig.Container.NeedsRebuild = needsRebuildBool
			modified = true
		}
	}

	return modified
}

// CreateMakefileTool provides MCP interface for creating default Makefiles.
type CreateMakefileTool struct{}

// NewCreateMakefileTool creates a new create makefile tool instance.
func NewCreateMakefileTool() *CreateMakefileTool {
	return &CreateMakefileTool{}
}

// Definition returns the tool's definition in Claude API format.
func (c *CreateMakefileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "create_makefile",
		Description: "Create a default Makefile with required targets (build, test, lint, run) for the detected platform",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory where Makefile should be created (defaults to current directory)",
				},
				"platform": {
					Type:        "string",
					Description: "Platform type (go, node, python, generic) - auto-detected if not specified",
				},
				"force": {
					Type:        "boolean",
					Description: "Overwrite existing Makefile if it exists (default: false)",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (c *CreateMakefileTool) Name() string {
	return "create_makefile"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *CreateMakefileTool) PromptDocumentation() string {
	return `- **create_makefile** - Create default Makefile with required targets
  - Parameters: 
    - cwd (optional): working directory
    - platform (optional): go/node/python/generic (auto-detected)
    - force (optional): overwrite existing Makefile
  - Creates Makefile with build, test, lint, run targets
  - Platform-specific commands for common project types
  - Use during bootstrap when Makefile is missing`
}

// Exec executes the create makefile operation.
func (c *CreateMakefileTool) Exec(_ context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	if cwd == "" {
		var err error
		cwd, err = filepath.Abs(".")
		if err != nil {
			return nil, fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	// Extract platform
	platform := ""
	if platformVal, hasPlatform := args["platform"]; hasPlatform {
		if platformStr, ok := platformVal.(string); ok {
			platform = platformStr
		}
	}

	// Auto-detect platform if not specified
	if platform == "" {
		platform = detectPlatformFromDirectory(cwd)
	}

	// Extract force flag
	force := false
	if forceVal, hasForce := args["force"]; hasForce {
		if forceBool, ok := forceVal.(bool); ok {
			force = forceBool
		}
	}

	// Check if Makefile already exists
	makefilePath := filepath.Join(cwd, "Makefile")
	if !force && fileExists(makefilePath) {
		return map[string]any{
			"success": false,
			"error":   "Makefile already exists. Use force=true to overwrite.",
		}, nil
	}

	// Generate Makefile content based on platform
	content := generateMakefileContent(platform)

	// Write Makefile
	if err := writeFile(makefilePath, content); err != nil {
		return nil, fmt.Errorf("failed to write Makefile: %w", err)
	}

	return map[string]any{
		"success":  true,
		"message":  fmt.Sprintf("Created Makefile for %s platform", platform),
		"path":     makefilePath,
		"platform": platform,
	}, nil
}

// detectPlatformFromDirectory detects platform based on files in directory.
func detectPlatformFromDirectory(dir string) string {
	// Check for go.mod
	if fileExists(filepath.Join(dir, "go.mod")) {
		return "go"
	}

	// Check for package.json
	if fileExists(filepath.Join(dir, "package.json")) {
		return "node"
	}

	// Check for Python files
	if fileExists(filepath.Join(dir, "requirements.txt")) ||
		fileExists(filepath.Join(dir, "pyproject.toml")) ||
		fileExists(filepath.Join(dir, "setup.py")) {
		return "python"
	}

	return "generic"
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeFile writes content to a file.
func writeFile(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return nil
}

// generateMakefileContent generates Makefile content for the specified platform.
func generateMakefileContent(platform string) string {
	switch platform {
	case "go":
		return `.PHONY: build test lint run clean

# Build the Go project
build:
	go build -o bin/app ./cmd/...

# Run tests
test:
	go test ./...

# Run linting
lint:
	golangci-lint run

# Run the application
run: build
	./bin/app

# Clean build artifacts
clean:
	rm -rf bin/
`
	case "node":
		return `.PHONY: build test lint run clean install

# Install dependencies
install:
	npm install

# Build the project
build: install
	npm run build

# Run tests
test:
	npm test

# Run linting
lint:
	npm run lint

# Run the application
run:
	npm start

# Clean build artifacts
clean:
	rm -rf node_modules/ dist/ build/
`
	case "python":
		return `.PHONY: build test lint run clean install

# Install dependencies
install:
	pip install -r requirements.txt

# Build the project (placeholder)
build: install
	python -m py_compile $(shell find . -name "*.py")

# Run tests
test:
	python -m pytest

# Run linting
lint:
	flake8 .
	black --check .

# Run the application
run:
	python main.py

# Clean build artifacts
clean:
	find . -type d -name "__pycache__" -delete
	find . -name "*.pyc" -delete
`
	default: // generic
		return `.PHONY: build test lint run clean

# Build the project
build:
	@echo "No build steps defined for generic project"
	@echo "Please customize this Makefile for your project"

# Run tests
test:
	@echo "No test steps defined for generic project"
	@echo "Please customize this Makefile for your project"

# Run linting
lint:
	@echo "No lint steps defined for generic project"
	@echo "Please customize this Makefile for your project"

# Run the application
run:
	@echo "No run steps defined for generic project"
	@echo "Please customize this Makefile for your project"

# Clean build artifacts
clean:
	@echo "No clean steps defined for generic project"
	@echo "Please customize this Makefile for your project"
`
	}
}
