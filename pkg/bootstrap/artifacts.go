package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/pkg/build"
	"orchestrator/pkg/logx"
)

// ArtifactGenerator generates bootstrap artifacts based on the detected backend
type ArtifactGenerator struct {
	projectRoot string
	config      *Config
	logger      *logx.Logger
}

// NewArtifactGenerator creates a new artifact generator
func NewArtifactGenerator(projectRoot string, config *Config) *ArtifactGenerator {
	return &ArtifactGenerator{
		projectRoot: projectRoot,
		config:      config,
		logger:      logx.NewLogger("bootstrap-artifacts"),
	}
}

// Generate creates bootstrap artifacts for the given backend
func (g *ArtifactGenerator) Generate(ctx context.Context, backend build.BuildBackend) ([]string, error) {
	g.logger.Info("Generating bootstrap artifacts for %s backend", backend.Name())
	
	var generatedFiles []string
	
	// Core artifacts (always generated)
	coreArtifacts, err := g.generateCoreArtifacts(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to generate core artifacts: %w", err)
	}
	generatedFiles = append(generatedFiles, coreArtifacts...)
	
	// Makefile artifacts (unless disabled)
	if !g.config.SkipMakefile {
		makefileArtifacts, err := g.generateMakefileArtifacts(ctx, backend)
		if err != nil {
			return nil, fmt.Errorf("failed to generate Makefile artifacts: %w", err)
		}
		generatedFiles = append(generatedFiles, makefileArtifacts...)
	}
	
	// Backend-specific artifacts
	backendArtifacts, err := g.generateBackendArtifacts(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to generate backend artifacts: %w", err)
	}
	generatedFiles = append(generatedFiles, backendArtifacts...)
	
	// Additional artifacts (from configuration)
	additionalArtifacts, err := g.generateAdditionalArtifacts(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to generate additional artifacts: %w", err)
	}
	generatedFiles = append(generatedFiles, additionalArtifacts...)
	
	g.logger.Info("Generated %d bootstrap artifacts", len(generatedFiles))
	return generatedFiles, nil
}

// generateCoreArtifacts generates core bootstrap artifacts (always created)
func (g *ArtifactGenerator) generateCoreArtifacts(ctx context.Context, backend build.BuildBackend) ([]string, error) {
	var files []string
	
	// .gitignore
	if err := g.generateGitignore(backend); err != nil {
		return nil, fmt.Errorf("failed to generate .gitignore: %w", err)
	}
	files = append(files, ".gitignore")
	
	// .gitattributes (for union merge strategy)
	if err := g.generateGitattributes(); err != nil {
		return nil, fmt.Errorf("failed to generate .gitattributes: %w", err)
	}
	files = append(files, ".gitattributes")
	
	// .editorconfig
	if err := g.generateEditorconfig(); err != nil {
		return nil, fmt.Errorf("failed to generate .editorconfig: %w", err)
	}
	files = append(files, ".editorconfig")
	
	return files, nil
}

// generateMakefileArtifacts generates Makefile-related artifacts
func (g *ArtifactGenerator) generateMakefileArtifacts(ctx context.Context, backend build.BuildBackend) ([]string, error) {
	var files []string
	
	// Root Makefile with include pattern
	if err := g.generateRootMakefile(); err != nil {
		return nil, fmt.Errorf("failed to generate root Makefile: %w", err)
	}
	files = append(files, "Makefile")
	
	// agent.mk with backend-specific targets
	if err := g.generateAgentMakefile(backend); err != nil {
		return nil, fmt.Errorf("failed to generate agent.mk: %w", err)
	}
	files = append(files, "agent.mk")
	
	return files, nil
}

// generateBackendArtifacts generates backend-specific artifacts
func (g *ArtifactGenerator) generateBackendArtifacts(ctx context.Context, backend build.BuildBackend) ([]string, error) {
	var files []string
	
	switch backend.Name() {
	case "go":
		goFiles, err := g.generateGoArtifacts()
		if err != nil {
			return nil, fmt.Errorf("failed to generate Go artifacts: %w", err)
		}
		files = append(files, goFiles...)
		
	case "python":
		pythonFiles, err := g.generatePythonArtifacts()
		if err != nil {
			return nil, fmt.Errorf("failed to generate Python artifacts: %w", err)
		}
		files = append(files, pythonFiles...)
		
	case "node":
		nodeFiles, err := g.generateNodeArtifacts()
		if err != nil {
			return nil, fmt.Errorf("failed to generate Node.js artifacts: %w", err)
		}
		files = append(files, nodeFiles...)
		
	case "null":
		// No backend-specific artifacts for null backend
		g.logger.Info("No backend-specific artifacts for null backend")
		
	default:
		g.logger.Warn("Unknown backend type: %s, skipping backend-specific artifacts", backend.Name())
	}
	
	return files, nil
}

// generateAdditionalArtifacts generates additional artifacts from configuration
func (g *ArtifactGenerator) generateAdditionalArtifacts(ctx context.Context, backend build.BuildBackend) ([]string, error) {
	var files []string
	
	for _, artifact := range g.config.AdditionalArtifacts {
		switch artifact {
		case "README.md":
			if err := g.generateReadme(backend); err != nil {
				return nil, fmt.Errorf("failed to generate README.md: %w", err)
			}
			files = append(files, "README.md")
			
		case "CONTRIBUTING.md":
			if err := g.generateContributing(backend); err != nil {
				return nil, fmt.Errorf("failed to generate CONTRIBUTING.md: %w", err)
			}
			files = append(files, "CONTRIBUTING.md")
			
		case "LICENSE":
			if err := g.generateLicense(); err != nil {
				return nil, fmt.Errorf("failed to generate LICENSE: %w", err)
			}
			files = append(files, "LICENSE")
			
		case "Dockerfile":
			if err := g.generateDockerfile(backend); err != nil {
				return nil, fmt.Errorf("failed to generate Dockerfile: %w", err)
			}
			files = append(files, "Dockerfile")
			
		case ".github/workflows/ci.yaml":
			if err := g.generateCIWorkflow(backend); err != nil {
				return nil, fmt.Errorf("failed to generate CI workflow: %w", err)
			}
			files = append(files, ".github/workflows/ci.yaml")
			
		default:
			g.logger.Warn("Unknown additional artifact: %s", artifact)
		}
	}
	
	return files, nil
}

// generateGitignore generates a .gitignore file appropriate for the backend
func (g *ArtifactGenerator) generateGitignore(backend build.BuildBackend) error {
	var content strings.Builder
	
	// Common ignores
	content.WriteString("# Generated by Claude Code Bootstrap\n")
	content.WriteString("# OS-specific files\n")
	content.WriteString(".DS_Store\n")
	content.WriteString("Thumbs.db\n")
	content.WriteString("\n")
	
	content.WriteString("# IDE/Editor files\n")
	content.WriteString(".vscode/\n")
	content.WriteString(".idea/\n")
	content.WriteString("*.swp\n")
	content.WriteString("*.swo\n")
	content.WriteString("\n")
	
	content.WriteString("# Temporary files\n")
	content.WriteString("*.tmp\n")
	content.WriteString("*.temp\n")
	content.WriteString("*.log\n")
	content.WriteString("\n")
	
	// Backend-specific ignores
	switch backend.Name() {
	case "go":
		content.WriteString("# Go\n")
		content.WriteString("*.exe\n")
		content.WriteString("*.exe~\n")
		content.WriteString("*.dll\n")
		content.WriteString("*.so\n")
		content.WriteString("*.dylib\n")
		content.WriteString("*.test\n")
		content.WriteString("*.out\n")
		content.WriteString("go.work\n")
		
	case "python":
		content.WriteString("# Python\n")
		content.WriteString("__pycache__/\n")
		content.WriteString("*.py[cod]\n")
		content.WriteString("*$py.class\n")
		content.WriteString("*.so\n")
		content.WriteString(".Python\n")
		content.WriteString("build/\n")
		content.WriteString("develop-eggs/\n")
		content.WriteString("dist/\n")
		content.WriteString("downloads/\n")
		content.WriteString("eggs/\n")
		content.WriteString(".eggs/\n")
		content.WriteString("lib/\n")
		content.WriteString("lib64/\n")
		content.WriteString("parts/\n")
		content.WriteString("sdist/\n")
		content.WriteString("var/\n")
		content.WriteString("wheels/\n")
		content.WriteString("*.egg-info/\n")
		content.WriteString(".installed.cfg\n")
		content.WriteString("*.egg\n")
		content.WriteString("MANIFEST\n")
		content.WriteString(".env\n")
		content.WriteString(".venv\n")
		content.WriteString("env/\n")
		content.WriteString("venv/\n")
		content.WriteString("ENV/\n")
		content.WriteString("env.bak/\n")
		content.WriteString("venv.bak/\n")
		
	case "node":
		content.WriteString("# Node.js\n")
		content.WriteString("node_modules/\n")
		content.WriteString("npm-debug.log*\n")
		content.WriteString("yarn-debug.log*\n")
		content.WriteString("yarn-error.log*\n")
		content.WriteString("lerna-debug.log*\n")
		content.WriteString(".pnpm-debug.log*\n")
		content.WriteString("report.[0-9]*.[0-9]*.[0-9]*.[0-9]*.json\n")
		content.WriteString("pids\n")
		content.WriteString("*.pid\n")
		content.WriteString("*.seed\n")
		content.WriteString("*.pid.lock\n")
		content.WriteString("lib-cov\n")
		content.WriteString("coverage\n")
		content.WriteString("*.lcov\n")
		content.WriteString(".nyc_output\n")
		content.WriteString(".grunt\n")
		content.WriteString("bower_components\n")
		content.WriteString(".lock-wscript\n")
		content.WriteString("build/Release\n")
		content.WriteString("node_modules/\n")
		content.WriteString("jspm_packages/\n")
		content.WriteString("*.tsbuildinfo\n")
		content.WriteString(".npm\n")
		content.WriteString(".eslintcache\n")
		content.WriteString(".stylelintcache\n")
		content.WriteString(".node_repl_history\n")
		content.WriteString("*.tgz\n")
		content.WriteString(".yarn-integrity\n")
		content.WriteString(".env\n")
		content.WriteString(".env.local\n")
		content.WriteString(".env.development.local\n")
		content.WriteString(".env.test.local\n")
		content.WriteString(".env.production.local\n")
		content.WriteString(".cache\n")
		content.WriteString(".parcel-cache\n")
		content.WriteString(".next\n")
		content.WriteString("out/\n")
		content.WriteString("dist\n")
		content.WriteString(".nuxt\n")
		content.WriteString(".vuepress/dist\n")
		content.WriteString(".serverless/\n")
		content.WriteString(".fusebox/\n")
		content.WriteString(".dynamodb/\n")
		content.WriteString(".tern-port\n")
		content.WriteString(".stores\n")
	}
	
	return g.writeFile(".gitignore", content.String())
}

// generateGitattributes generates .gitattributes for union merge strategy
func (g *ArtifactGenerator) generateGitattributes() error {
	content := `# Generated by Claude Code Bootstrap
# Union merge strategy for build files to prevent conflicts
Makefile merge=union
agent.mk merge=union
*.mk merge=union
`
	return g.writeFile(".gitattributes", content)
}

// generateEditorconfig generates .editorconfig for consistent formatting
func (g *ArtifactGenerator) generateEditorconfig() error {
	content := `# Generated by Claude Code Bootstrap
root = true

[*]
charset = utf-8
end_of_line = lf
indent_style = space
indent_size = 2
insert_final_newline = true
trim_trailing_whitespace = true

[*.go]
indent_style = tab
indent_size = 4

[*.py]
indent_size = 4

[*.{js,jsx,ts,tsx}]
indent_size = 2

[*.md]
trim_trailing_whitespace = false

[Makefile]
indent_style = tab
`
	return g.writeFile(".editorconfig", content)
}

// generateRootMakefile generates the root Makefile with include pattern
func (g *ArtifactGenerator) generateRootMakefile() error {
	content := `# Generated by Claude Code Bootstrap
# This Makefile uses the include pattern to prevent merge conflicts
# Human-maintained targets should be added here
# Agent-generated targets are in agent.mk

# Include agent-generated targets
-include agent.mk

# Default targets if agent.mk doesn't exist
# Note: .PHONY declarations are in agent.mk to avoid conflicts
.PHONY: help

# Only define fallback targets if agent.mk doesn't exist
ifeq ($(wildcard agent.mk),)
build:
	@echo "No build configured. Run bootstrap to set up build system."

test:
	@echo "No tests configured. Run bootstrap to set up test system."

lint:
	@echo "No linting configured. Run bootstrap to set up linting system."

run:
	@echo "No run target configured. Run bootstrap to set up run system."
endif

help:
	@echo "Available targets:"
	@echo "  build  - Build the project"
	@echo "  test   - Run tests"
	@echo "  lint   - Run linting"
	@echo "  run    - Run the application"
	@echo "  help   - Show this help message"
	@echo ""
	@echo "This project uses Claude Code Bootstrap for build management."
	@echo "Agent-generated targets are in agent.mk."
`
	return g.writeFile("Makefile", content)
}

// generateAgentMakefile generates agent.mk with backend-specific targets
func (g *ArtifactGenerator) generateAgentMakefile(backend build.BuildBackend) error {
	var content strings.Builder
	
	content.WriteString("# Generated by Claude Code Bootstrap\n")
	content.WriteString("# This file contains agent-generated build targets\n")
	content.WriteString("# Backend: " + backend.Name() + "\n")
	content.WriteString("\n")
	content.WriteString(".PHONY: build test lint run\n")
	content.WriteString("\n")
	
	switch backend.Name() {
	case "go":
		content.WriteString("build:\n")
		content.WriteString("\tgo mod download\n")
		content.WriteString("\tgo build ./...\n")
		content.WriteString("\n")
		content.WriteString("test:\n")
		content.WriteString("\tgo test ./...\n")
		content.WriteString("\n")
		content.WriteString("lint:\n")
		content.WriteString("\t@which golangci-lint > /dev/null || (echo \"golangci-lint not found, using go vet\" && go vet ./...)\n")
		content.WriteString("\t@which golangci-lint > /dev/null && golangci-lint run ./...\n")
		content.WriteString("\n")
		content.WriteString("run:\n")
		content.WriteString("\tgo run .\n")
		
	case "python":
		content.WriteString("build:\n")
		content.WriteString("\t@which uv > /dev/null && uv sync || pip install -r requirements.txt\n")
		content.WriteString("\n")
		content.WriteString("test:\n")
		content.WriteString("\t@which pytest > /dev/null && pytest || python -m unittest discover\n")
		content.WriteString("\n")
		content.WriteString("lint:\n")
		content.WriteString("\t@which ruff > /dev/null && ruff check . || (which flake8 > /dev/null && flake8 .)\n")
		content.WriteString("\n")
		content.WriteString("run:\n")
		content.WriteString("\t@test -f main.py && python main.py || (test -f app.py && python app.py || echo \"No main.py or app.py found\")\n")
		
	case "node":
		content.WriteString("build:\n")
		content.WriteString("\t@which npm > /dev/null && npm install || echo \"npm not found\"\n")
		content.WriteString("\t@npm run build 2>/dev/null || echo \"No build script found\"\n")
		content.WriteString("\n")
		content.WriteString("test:\n")
		content.WriteString("\t@npm test 2>/dev/null || echo \"No test script found\"\n")
		content.WriteString("\n")
		content.WriteString("lint:\n")
		content.WriteString("\t@npm run lint 2>/dev/null || (which eslint > /dev/null && npx eslint .)\n")
		content.WriteString("\n")
		content.WriteString("run:\n")
		content.WriteString("\t@npm start 2>/dev/null || (test -f index.js && node index.js || echo \"No start script or index.js found\")\n")
		
	case "make":
		content.WriteString("# Makefile backend detected - delegating to existing Makefile\n")
		content.WriteString("build:\n")
		content.WriteString("\t@make build 2>/dev/null || echo \"No build target in Makefile\"\n")
		content.WriteString("\n")
		content.WriteString("test:\n")
		content.WriteString("\t@make test 2>/dev/null || echo \"No test target in Makefile\"\n")
		content.WriteString("\n")
		content.WriteString("lint:\n")
		content.WriteString("\t@make lint 2>/dev/null || echo \"No lint target in Makefile\"\n")
		content.WriteString("\n")
		content.WriteString("run:\n")
		content.WriteString("\t@make run 2>/dev/null || echo \"No run target in Makefile\"\n")
		
	default:
		content.WriteString("# Unknown backend - providing basic targets\n")
		content.WriteString("build:\n")
		content.WriteString("\t@echo \"Build not configured for backend: " + backend.Name() + "\"\n")
		content.WriteString("\n")
		content.WriteString("test:\n")
		content.WriteString("\t@echo \"Tests not configured for backend: " + backend.Name() + "\"\n")
		content.WriteString("\n")
		content.WriteString("lint:\n")
		content.WriteString("\t@echo \"Linting not configured for backend: " + backend.Name() + "\"\n")
		content.WriteString("\n")
		content.WriteString("run:\n")
		content.WriteString("\t@echo \"Run not configured for backend: " + backend.Name() + "\"\n")
	}
	
	return g.writeFile("agent.mk", content.String())
}

// Backend-specific artifact generators
func (g *ArtifactGenerator) generateGoArtifacts() ([]string, error) {
	var files []string
	
	// golangci-lint.yaml
	if err := g.generateGolangciLintConfig(); err != nil {
		return nil, err
	}
	files = append(files, ".golangci.yaml")
	
	return files, nil
}

func (g *ArtifactGenerator) generatePythonArtifacts() ([]string, error) {
	var files []string
	
	// pyproject.toml (if it doesn't exist)
	if _, err := os.Stat(filepath.Join(g.projectRoot, "pyproject.toml")); os.IsNotExist(err) {
		if err := g.generatePyprojectToml(); err != nil {
			return nil, err
		}
		files = append(files, "pyproject.toml")
	}
	
	// ruff.toml
	if err := g.generateRuffConfig(); err != nil {
		return nil, err
	}
	files = append(files, "ruff.toml")
	
	return files, nil
}

func (g *ArtifactGenerator) generateNodeArtifacts() ([]string, error) {
	var files []string
	
	// .eslintrc.js
	if err := g.generateEslintConfig(); err != nil {
		return nil, err
	}
	files = append(files, ".eslintrc.js")
	
	// .nvmrc
	if err := g.generateNvmrc(); err != nil {
		return nil, err
	}
	files = append(files, ".nvmrc")
	
	return files, nil
}

// Helper method to write files
func (g *ArtifactGenerator) writeFile(filename, content string) error {
	fullPath := filepath.Join(g.projectRoot, filename)
	
	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", filename, err)
	}
	
	// Write file
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filename, err)
	}
	
	g.logger.Debug("Generated file: %s", filename)
	return nil
}

// Additional artifact generators (stubs for now)
func (g *ArtifactGenerator) generateGolangciLintConfig() error {
	content := `# Generated by Claude Code Bootstrap
run:
  timeout: 5m
  modules-download-mode: readonly

linters:
  enable:
    - gofmt
    - goimports
    - govet
    - ineffassign
    - misspell
    - staticcheck
    - unused
    - errcheck
    - gosimple
    - deadcode
    - structcheck
    - varcheck
    - typecheck

issues:
  exclude-use-default: false
`
	return g.writeFile(".golangci.yaml", content)
}

func (g *ArtifactGenerator) generatePyprojectToml() error {
	content := `# Generated by Claude Code Bootstrap
[build-system]
requires = ["setuptools>=45", "wheel"]
build-backend = "setuptools.build_meta"

[project]
name = "my-project"
version = "0.1.0"
description = "A project bootstrapped with Claude Code"
readme = "README.md"
requires-python = ">=3.8"
dependencies = []

[project.optional-dependencies]
dev = [
    "pytest>=7.0",
    "ruff>=0.1.0",
    "black>=23.0",
]

[tool.ruff]
line-length = 88
target-version = "py38"

[tool.ruff.lint]
select = ["E", "F", "W", "I", "N", "UP", "S", "B", "A", "C4", "DTZ", "T10", "EM", "ISC", "ICN", "G", "PIE", "T20", "PT", "Q", "RSE", "RET", "SLF", "SIM", "TID", "TCH", "ARG", "PTH", "PD", "PGH", "PL", "TRY", "NPY", "RUF"]
ignore = ["E501", "S101", "PLR0913", "PLR0915"]

[tool.pytest.ini_options]
minversion = "7.0"
testpaths = ["tests"]
python_files = "test_*.py"
python_classes = "Test*"
python_functions = "test_*"
`
	return g.writeFile("pyproject.toml", content)
}

func (g *ArtifactGenerator) generateRuffConfig() error {
	content := `# Generated by Claude Code Bootstrap
line-length = 88
target-version = "py38"

[lint]
select = ["E", "F", "W", "I", "N", "UP", "S", "B", "A", "C4", "DTZ", "T10", "EM", "ISC", "ICN", "G", "PIE", "T20", "PT", "Q", "RSE", "RET", "SLF", "SIM", "TID", "TCH", "ARG", "PTH", "PD", "PGH", "PL", "TRY", "NPY", "RUF"]
ignore = ["E501", "S101", "PLR0913", "PLR0915"]

[lint.per-file-ignores]
"tests/*" = ["S101", "PLR2004"]
`
	return g.writeFile("ruff.toml", content)
}

func (g *ArtifactGenerator) generateEslintConfig() error {
	content := `// Generated by Claude Code Bootstrap
module.exports = {
  env: {
    browser: true,
    es2021: true,
    node: true,
  },
  extends: [
    'eslint:recommended',
  ],
  parserOptions: {
    ecmaVersion: 12,
    sourceType: 'module',
  },
  rules: {
    'no-unused-vars': 'warn',
    'no-console': 'warn',
    'quotes': ['error', 'single'],
    'semi': ['error', 'always'],
  },
};
`
	return g.writeFile(".eslintrc.js", content)
}

func (g *ArtifactGenerator) generateNvmrc() error {
	content := `18
`
	return g.writeFile(".nvmrc", content)
}

func (g *ArtifactGenerator) generateReadme(backend build.BuildBackend) error {
	content := fmt.Sprintf(`# My Project

A project bootstrapped with Claude Code Bootstrap.

## Backend

This project uses the **%s** backend for build operations.

## Available Commands

- `+"`make build`"+` - Build the project
- `+"`make test`"+` - Run tests
- `+"`make lint`"+` - Run linting
- `+"`make run`"+` - Run the application

## Development

This project uses Claude Code Bootstrap for consistent build management.
Agent-generated targets are in `+"`agent.mk`"+`.

## Getting Started

1. Install dependencies
2. Run `+"`make build`"+` to build the project
3. Run `+"`make test`"+` to run tests
4. Run `+"`make run`"+` to start the application

Generated by Claude Code Bootstrap on %s
`, backend.Name(), "2025-07-15")
	return g.writeFile("README.md", content)
}

func (g *ArtifactGenerator) generateContributing(backend build.BuildBackend) error {
	content := `# Contributing

## Development Setup

1. Clone the repository
2. Install dependencies: `+"`make build`"+`
3. Run tests: `+"`make test`"+`
4. Run linting: `+"`make lint`"+`

## Code Style

This project uses automated linting and formatting tools.
Run `+"`make lint`"+` to check your code before submitting.

## Testing

All code changes should include appropriate tests.
Run `+"`make test`"+` to run the test suite.

## Pull Requests

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests and linting
5. Submit a pull request

Generated by Claude Code Bootstrap
`
	return g.writeFile("CONTRIBUTING.md", content)
}

func (g *ArtifactGenerator) generateLicense() error {
	content := `MIT License

Copyright (c) 2025 Project Name

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`
	return g.writeFile("LICENSE", content)
}

func (g *ArtifactGenerator) generateDockerfile(backend build.BuildBackend) error {
	var content string
	
	switch backend.Name() {
	case "go":
		content = `# Generated by Claude Code Bootstrap
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o main .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/main .
CMD ["./main"]
`
	case "python":
		content = `# Generated by Claude Code Bootstrap
FROM python:3.11-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .
EXPOSE 8000
CMD ["python", "main.py"]
`
	case "node":
		content = `# Generated by Claude Code Bootstrap
FROM node:18-alpine

WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production

COPY . .
EXPOSE 3000
CMD ["npm", "start"]
`
	default:
		content = `# Generated by Claude Code Bootstrap
FROM alpine:latest
WORKDIR /app
COPY . .
CMD ["echo", "No Dockerfile template for backend: ` + backend.Name() + `"]
`
	}
	
	return g.writeFile("Dockerfile", content)
}

func (g *ArtifactGenerator) generateCIWorkflow(backend build.BuildBackend) error {
	var content string
	
	switch backend.Name() {
	case "go":
		content = `# Generated by Claude Code Bootstrap
name: CI

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest
    
    steps:
    - uses: actions/checkout@v3
    
    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: '1.21'
    
    - name: Build
      run: make build
    
    - name: Test
      run: make test
    
    - name: Lint
      run: make lint
`
	case "python":
		content = `# Generated by Claude Code Bootstrap
name: CI

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest
    
    steps:
    - uses: actions/checkout@v3
    
    - name: Set up Python
      uses: actions/setup-python@v4
      with:
        python-version: '3.11'
    
    - name: Install uv
      run: pip install uv
    
    - name: Build
      run: make build
    
    - name: Test
      run: make test
    
    - name: Lint
      run: make lint
`
	case "node":
		content = `# Generated by Claude Code Bootstrap
name: CI

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest
    
    steps:
    - uses: actions/checkout@v3
    
    - name: Set up Node.js
      uses: actions/setup-node@v3
      with:
        node-version: '18'
        cache: 'npm'
    
    - name: Build
      run: make build
    
    - name: Test
      run: make test
    
    - name: Lint
      run: make lint
`
	default:
		content = `# Generated by Claude Code Bootstrap
name: CI

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest
    
    steps:
    - uses: actions/checkout@v3
    
    - name: Build
      run: make build
    
    - name: Test
      run: make test
    
    - name: Lint
      run: make lint
`
	}
	
	// Create .github/workflows directory
	workflowDir := filepath.Join(g.projectRoot, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return fmt.Errorf("failed to create .github/workflows directory: %w", err)
	}
	
	return g.writeFile(".github/workflows/ci.yaml", content)
}