package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

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
func (c *CreateMakefileTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
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
		response := map[string]any{
			"success": false,
			"error":   "Makefile already exists. Use force=true to overwrite.",
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	// Generate Makefile content based on platform
	content := generateMakefileContent(platform)

	// Write Makefile
	if err := writeFile(makefilePath, content); err != nil {
		return nil, fmt.Errorf("failed to write Makefile: %w", err)
	}

	result := map[string]any{
		"success":  true,
		"message":  fmt.Sprintf("Created Makefile for %s platform", platform),
		"path":     makefilePath,
		"platform": platform,
	}

	resultContent, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(resultContent)}, nil
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
