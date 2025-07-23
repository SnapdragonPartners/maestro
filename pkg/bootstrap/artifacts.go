// Package bootstrap provides project scaffolding and artifact generation for different platform types.
// It includes Dockerfile generation, dependency management, and platform-specific configuration.
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

const (
	languagePython = "python"
	languageNode   = "node"
	languageGo     = "go"
	versionPython  = "3.11"
	versionGo      = "1.21"
)

// ArtifactGenerator generates bootstrap artifacts based on the detected backend.
//
//nolint:govet // Simple generator struct, logical grouping preferred
type ArtifactGenerator struct {
	projectRoot string
	config      *Config
	logger      *logx.Logger
}

// NewArtifactGenerator creates a new artifact generator.
func NewArtifactGenerator(projectRoot string, config *Config) *ArtifactGenerator {
	return &ArtifactGenerator{
		projectRoot: projectRoot,
		config:      config,
		logger:      logx.NewLogger("bootstrap-artifacts"),
	}
}

// Generate creates bootstrap artifacts for the given backend.
func (g *ArtifactGenerator) Generate(ctx context.Context, backend build.Backend) ([]string, error) {
	g.logger.Info("Generating bootstrap artifacts for %s backend", backend.Name())

	var generatedFiles []string

	// Core artifacts (always generated).
	coreArtifacts, err := g.generateCoreArtifacts(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to generate core artifacts: %w", err)
	}
	generatedFiles = append(generatedFiles, coreArtifacts...)

	// Makefile artifacts (unless disabled).
	if !g.config.SkipMakefile {
		makefileArtifacts, makefileErr := g.generateMakefileArtifacts(ctx, backend)
		if makefileErr != nil {
			return nil, fmt.Errorf("failed to generate Makefile artifacts: %w", makefileErr)
		}
		generatedFiles = append(generatedFiles, makefileArtifacts...)
	}

	// Backend-specific artifacts.
	backendArtifacts, err := g.generateBackendArtifacts(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to generate backend artifacts: %w", err)
	}
	generatedFiles = append(generatedFiles, backendArtifacts...)

	// Additional artifacts (from configuration).
	additionalArtifacts, err := g.generateAdditionalArtifacts(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to generate additional artifacts: %w", err)
	}
	generatedFiles = append(generatedFiles, additionalArtifacts...)

	g.logger.Info("Generated %d bootstrap artifacts", len(generatedFiles))
	return generatedFiles, nil
}

// generateCoreArtifacts generates core bootstrap artifacts (always created).
func (g *ArtifactGenerator) generateCoreArtifacts(_ /* ctx */ context.Context, backend build.Backend) ([]string, error) {
	var files []string

	// Create .maestro directory first.
	maestroDir := filepath.Join(g.projectRoot, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .maestro directory: %w", err)
	}

	// Create .maestro/makefiles subdirectory.
	makefilesDir := filepath.Join(maestroDir, "makefiles")
	if err := os.MkdirAll(makefilesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .maestro/makefiles directory: %w", err)
	}

	// .gitignore.
	if err := g.generateGitignore(backend); err != nil {
		return nil, fmt.Errorf("failed to generate .gitignore: %w", err)
	}
	files = append(files, ".gitignore")

	// .gitattributes (for union merge strategy).
	if err := g.generateGitattributes(); err != nil {
		return nil, fmt.Errorf("failed to generate .gitattributes: %w", err)
	}
	files = append(files, ".gitattributes")

	// .editorconfig.
	if err := g.generateEditorconfig(); err != nil {
		return nil, fmt.Errorf("failed to generate .editorconfig: %w", err)
	}
	files = append(files, ".editorconfig")

	// Dockerfile (for Docker sandboxing support).
	if err := g.GenerateDockerfile(backend); err != nil {
		return nil, fmt.Errorf("failed to generate Dockerfile: %w", err)
	}
	files = append(files, "Dockerfile")

	// .dockerignore.
	if err := g.GenerateDockerignore(backend); err != nil {
		return nil, fmt.Errorf("failed to generate .dockerignore: %w", err)
	}
	files = append(files, ".dockerignore")

	return files, nil
}

// generateMakefileArtifacts generates Makefile-related artifacts.
func (g *ArtifactGenerator) generateMakefileArtifacts(_ /* ctx */ context.Context, backend build.Backend) ([]string, error) {
	var files []string

	// Root Makefile with include pattern.
	if err := g.generateRootMakefile(); err != nil {
		return nil, fmt.Errorf("failed to generate root Makefile: %w", err)
	}
	files = append(files, "Makefile")

	// Generate core makefile in .maestro/makefiles/.
	if err := g.generateCoreMakefile(); err != nil {
		return nil, fmt.Errorf("failed to generate core makefile: %w", err)
	}
	files = append(files, ".maestro/makefiles/core.mk")

	// Generate platform-specific makefiles based on architect recommendation.
	platformFiles, err := g.generatePlatformMakefiles(backend)
	if err != nil {
		return nil, fmt.Errorf("failed to generate platform makefiles: %w", err)
	}
	files = append(files, platformFiles...)

	return files, nil
}

// generateBackendArtifacts generates backend-specific artifacts.
func (g *ArtifactGenerator) generateBackendArtifacts(_ /* ctx */ context.Context, backend build.Backend) ([]string, error) {
	var files []string

	switch backend.Name() {
	case languageGo:
		goFiles, err := g.generateGoArtifacts()
		if err != nil {
			return nil, fmt.Errorf("failed to generate Go artifacts: %w", err)
		}
		files = append(files, goFiles...)

	case languagePython:
		pythonFiles, err := g.generatePythonArtifacts()
		if err != nil {
			return nil, fmt.Errorf("failed to generate Python artifacts: %w", err)
		}
		files = append(files, pythonFiles...)

	case languageNode:
		nodeFiles, err := g.generateNodeArtifacts()
		if err != nil {
			return nil, fmt.Errorf("failed to generate Node.js artifacts: %w", err)
		}
		files = append(files, nodeFiles...)

	case "null":
		// No backend-specific artifacts for null backend.
		g.logger.Info("No backend-specific artifacts for null backend")

	default:
		g.logger.Warn("Unknown backend type: %s, skipping backend-specific artifacts", backend.Name())
	}

	return files, nil
}

// generateAdditionalArtifacts generates additional artifacts from configuration.
func (g *ArtifactGenerator) generateAdditionalArtifacts(_ /* ctx */ context.Context, backend build.Backend) ([]string, error) {
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
			if err := g.GenerateDockerfile(backend); err != nil {
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

// generateGitignore generates a .gitignore file appropriate for the backend.
func (g *ArtifactGenerator) generateGitignore(backend build.Backend) error {
	var content strings.Builder

	// Common ignores.
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

	// Backend-specific ignores.
	switch backend.Name() {
	case languageGo:
		content.WriteString("# Go\n")
		content.WriteString("*.exe\n")
		content.WriteString("*.exe~\n")
		content.WriteString("*.dll\n")
		content.WriteString("*.so\n")
		content.WriteString("*.dylib\n")
		content.WriteString("*.test\n")
		content.WriteString("*.out\n")
		content.WriteString("go.work\n")

	case languagePython:
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

	case languageNode:
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

// generateGitattributes generates .gitattributes for union merge strategy.
func (g *ArtifactGenerator) generateGitattributes() error {
	content := `# Generated by Claude Code Bootstrap
# Union merge strategy for build files to prevent conflicts
Makefile merge=union
agent.mk merge=union
*.mk merge=union
`
	return g.writeFile(".gitattributes", content)
}

// generateEditorconfig generates .editorconfig for consistent formatting.
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

// generateRootMakefile generates the root Makefile with include pattern.
func (g *ArtifactGenerator) generateRootMakefile() error {
	content := `# Generated by Claude Code Bootstrap
# This Makefile uses the include pattern to prevent merge conflicts
# Human-maintained targets should be added here
# Agent-generated targets are in .maestro/makefiles/

# Include core makefile
-include .maestro/makefiles/core.mk

# Include platform-specific makefiles
-include .maestro/makefiles/go.mk
-include .maestro/makefiles/node.mk
-include .maestro/makefiles/python.mk
-include .maestro/makefiles/react.mk
-include .maestro/makefiles/docker.mk

# Default targets if no makefiles exist
.PHONY: help

# Only define fallback targets if core.mk doesn't exist
ifeq ($(wildcard .maestro/makefiles/core.mk),)
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
	@echo "  build      - Build the project"
	@echo "  test       - Run tests"
	@echo "  lint       - Run linting"
	@echo "  run        - Run the application"
	@echo "  clean      - Clean build artifacts"
	@echo "  help       - Show this help message"
	@echo ""
	@echo "Platform-specific help:"
	@echo "  go-help    - Show Go targets (if available)"
	@echo "  node-help  - Show Node.js targets (if available)"
	@echo "  python-help - Show Python targets (if available)"
	@echo "  react-help - Show React targets (if available)"
	@echo "  docker-help - Show Docker targets (if available)"
	@echo ""
	@echo "This project uses Claude Code Bootstrap for build management."
	@echo "Agent-generated targets are in .maestro/makefiles/"
`
	return g.writeFile("Makefile", content)
}

// generateCoreMakefile generates the core makefile with common targets.
func (g *ArtifactGenerator) generateCoreMakefile() error {
	content := `# Generated by Claude Code Bootstrap
# Core makefile with common targets and utilities

### ⇡ GENERATED BLOCK ⇡ DO NOT EDIT
.PHONY: help clean info bootstrap-info

# Default target
.DEFAULT_GOAL := help

# Clean up build artifacts
clean:
	@echo "🧹 Cleaning up build artifacts..."
	rm -rf build/
	rm -rf dist/
	rm -rf node_modules/
	rm -rf .pytest_cache/
	rm -rf __pycache__/
	find . -name "*.pyc" -delete
	find . -name "*.pyo" -delete
	find . -name "*~" -delete
	go clean -cache -testcache -modcache 2>/dev/null || true
	@echo "✅ Clean completed"

# Show build information
info:
	@echo "📋 Build Information:"
	@echo "  Project: $(shell basename $(PWD))"
	@echo "  Generated: $(shell date)"
	@echo "  Bootstrap: Claude Code Bootstrap"
	@echo "  Platform: $(shell uname -s)"

# Show bootstrap information
bootstrap-info:
	@echo "🚀 Bootstrap Information:"
	@echo "  Status: Active"
	@echo "  Configuration: .maestro/makefiles/"
	@echo "  Available platforms: go, node, python, react, docker"
	@echo "  Help: make help"

# Help target (will be overridden by platform-specific makefiles)
help:
	@echo "📖 Available targets:"
	@echo "  help         - Show this help message"
	@echo "  clean        - Clean build artifacts"
	@echo "  info         - Show build information"
	@echo "  bootstrap-info - Show bootstrap information"
	@echo ""
	@echo "Platform-specific targets will be shown when available."
### ⇣ GENERATED BLOCK ⇣ DO NOT EDIT
`
	return g.writeFile(".maestro/makefiles/core.mk", content)
}

// generatePlatformMakefiles generates platform-specific makefiles based on architect recommendation.
func (g *ArtifactGenerator) generatePlatformMakefiles(backend build.Backend) ([]string, error) {
	var files []string

	// Get platforms from architect recommendation.
	platforms := g.getPlatformsFromRecommendation(backend)

	for _, platform := range platforms {
		switch platform {
		case languageGo:
			if err := g.generateGoMakefile(); err != nil {
				return nil, fmt.Errorf("failed to generate Go makefile: %w", err)
			}
			files = append(files, ".maestro/makefiles/go.mk")

		case languageNode:
			if err := g.generateNodeMakefile(); err != nil {
				return nil, fmt.Errorf("failed to generate Node.js makefile: %w", err)
			}
			files = append(files, ".maestro/makefiles/node.mk")

		case languagePython:
			if err := g.generatePythonMakefile(); err != nil {
				return nil, fmt.Errorf("failed to generate Python makefile: %w", err)
			}
			files = append(files, ".maestro/makefiles/python.mk")

		case "react":
			if err := g.generateReactMakefile(); err != nil {
				return nil, fmt.Errorf("failed to generate React makefile: %w", err)
			}
			files = append(files, ".maestro/makefiles/react.mk")

		case "docker":
			if err := g.generateDockerMakefile(); err != nil {
				return nil, fmt.Errorf("failed to generate Docker makefile: %w", err)
			}
			files = append(files, ".maestro/makefiles/docker.mk")

		default:
			g.logger.Warn("Unknown platform: %s, skipping makefile generation", platform)
		}
	}

	return files, nil
}

// getPlatformsFromRecommendation extracts platforms from architect recommendation.
func (g *ArtifactGenerator) getPlatformsFromRecommendation(backend build.Backend) []string {
	// If we have architect recommendation, use it.
	if g.config.ArchitectRecommendation != nil {
		if g.config.ArchitectRecommendation.MultiStack {
			return g.config.ArchitectRecommendation.Platforms
		}
		return []string{g.config.ArchitectRecommendation.Platform}
	}

	// Fallback to backend name.
	return []string{backend.Name()}
}

// generateGoMakefile generates Go-specific makefile.
func (g *ArtifactGenerator) generateGoMakefile() error {
	content := `# Generated by Claude Code Bootstrap
# Go-specific build targets

### ⇡ GENERATED BLOCK ⇡ DO NOT EDIT
.PHONY: go-build go-test go-lint go-run go-mod go-format go-vet

# Override common targets for Go
build: go-build
test: go-test
lint: go-lint
run: go-run

# Go-specific targets
go-build:
	@echo "🔨 Building Go project..."
	go mod download
	go build ./...
	@echo "✅ Go build completed"

go-test:
	@echo "🧪 Running Go tests..."
	go test ./...
	@echo "✅ Go tests completed"

go-lint:
	@echo "🔍 Running Go linting..."
	@which golangci-lint > /dev/null || (echo "golangci-lint not found, using go vet" && go vet ./...)
	@which golangci-lint > /dev/null && golangci-lint run ./...
	@echo "✅ Go linting completed"

go-run:
	@echo "🚀 Running Go application..."
	go run .

go-mod:
	@echo "📦 Updating Go modules..."
	go mod tidy
	go mod download
	@echo "✅ Go modules updated"

go-format:
	@echo "🎨 Formatting Go code..."
	go fmt ./...
	@echo "✅ Go formatting completed"

go-vet:
	@echo "🔍 Running go vet..."
	go vet ./...
	@echo "✅ Go vet completed"

go-help:
	@echo "🐹 Go targets:"
	@echo "  go-build     - Build Go project"
	@echo "  go-test      - Run Go tests"
	@echo "  go-lint      - Run Go linting"
	@echo "  go-run       - Run Go application"
	@echo "  go-mod       - Update Go modules"
	@echo "  go-format    - Format Go code"
	@echo "  go-vet       - Run go vet"
	@echo ""
### ⇣ GENERATED BLOCK ⇣ DO NOT EDIT
`
	return g.writeFile(".maestro/makefiles/go.mk", content)
}

// generateNodeMakefile generates Node.js-specific makefile.
func (g *ArtifactGenerator) generateNodeMakefile() error {
	content := `# Generated by Claude Code Bootstrap
# Node.js-specific build targets

### ⇡ GENERATED BLOCK ⇡ DO NOT EDIT
.PHONY: node-build node-test node-lint node-run node-install node-dev

# Override common targets for Node.js
build: node-build
test: node-test
lint: node-lint
run: node-run

# Node.js-specific targets
node-build:
	@echo "🔨 Building Node.js project..."
	npm run build
	@echo "✅ Node.js build completed"

node-test:
	@echo "🧪 Running Node.js tests..."
	npm test
	@echo "✅ Node.js tests completed"

node-lint:
	@echo "🔍 Running Node.js linting..."
	npm run lint
	@echo "✅ Node.js linting completed"

node-run:
	@echo "🚀 Running Node.js application..."
	npm start

node-install:
	@echo "📦 Installing Node.js dependencies..."
	npm install
	@echo "✅ Node.js dependencies installed"

node-dev:
	@echo "🚀 Starting Node.js development server..."
	npm run dev

node-help:
	@echo "🟢 Node.js targets:"
	@echo "  node-build   - Build Node.js project"
	@echo "  node-test    - Run Node.js tests"
	@echo "  node-lint    - Run Node.js linting"
	@echo "  node-run     - Run Node.js application"
	@echo "  node-install - Install Node.js dependencies"
	@echo "  node-dev     - Start development server"
	@echo ""
### ⇣ GENERATED BLOCK ⇣ DO NOT EDIT
`
	return g.writeFile(".maestro/makefiles/node.mk", content)
}

// generatePythonMakefile generates Python-specific makefile.
func (g *ArtifactGenerator) generatePythonMakefile() error {
	content := `# Generated by Claude Code Bootstrap
# Python-specific build targets

### ⇡ GENERATED BLOCK ⇡ DO NOT EDIT
.PHONY: python-build python-test python-lint python-run python-install python-format

# Override common targets for Python
build: python-build
test: python-test
lint: python-lint
run: python-run

# Python-specific targets
python-build:
	@echo "🔨 Building Python project..."
	@which uv > /dev/null && uv sync || pip install -r requirements.txt
	@echo "✅ Python build completed"

python-test:
	@echo "🧪 Running Python tests..."
	@which uv > /dev/null && uv run pytest || python -m pytest
	@echo "✅ Python tests completed"

python-lint:
	@echo "🔍 Running Python linting..."
	@which uv > /dev/null && uv run ruff check . || python -m ruff check .
	@echo "✅ Python linting completed"

python-run:
	@echo "🚀 Running Python application..."
	@which uv > /dev/null && uv run python main.py || python main.py

python-install:
	@echo "📦 Installing Python dependencies..."
	@which uv > /dev/null && uv sync || pip install -r requirements.txt
	@echo "✅ Python dependencies installed"

python-format:
	@echo "🎨 Formatting Python code..."
	@which uv > /dev/null && uv run black . || python -m black .
	@echo "✅ Python formatting completed"

python-help:
	@echo "🐍 Python targets:"
	@echo "  python-build   - Build Python project"
	@echo "  python-test    - Run Python tests"
	@echo "  python-lint    - Run Python linting"
	@echo "  python-run     - Run Python application"
	@echo "  python-install - Install Python dependencies"
	@echo "  python-format  - Format Python code"
	@echo ""
### ⇣ GENERATED BLOCK ⇣ DO NOT EDIT
`
	return g.writeFile(".maestro/makefiles/python.mk", content)
}

// generateReactMakefile generates React-specific makefile.
func (g *ArtifactGenerator) generateReactMakefile() error {
	content := `# Generated by Claude Code Bootstrap
# React-specific build targets

### ⇡ GENERATED BLOCK ⇡ DO NOT EDIT
.PHONY: react-build react-test react-lint react-run react-install react-dev

# React-specific targets (doesn't override common targets)
react-build:
	@echo "🔨 Building React project..."
	npm run build
	@echo "✅ React build completed"

react-test:
	@echo "🧪 Running React tests..."
	npm test
	@echo "✅ React tests completed"

react-lint:
	@echo "🔍 Running React linting..."
	npm run lint
	@echo "✅ React linting completed"

react-run:
	@echo "🚀 Running React application..."
	npm start

react-install:
	@echo "📦 Installing React dependencies..."
	npm install
	@echo "✅ React dependencies installed"

react-dev:
	@echo "🚀 Starting React development server..."
	npm run dev

react-help:
	@echo "⚛️ React targets:"
	@echo "  react-build   - Build React project"
	@echo "  react-test    - Run React tests"
	@echo "  react-lint    - Run React linting"
	@echo "  react-run     - Run React application"
	@echo "  react-install - Install React dependencies"
	@echo "  react-dev     - Start development server"
	@echo ""
### ⇣ GENERATED BLOCK ⇣ DO NOT EDIT
`
	return g.writeFile(".maestro/makefiles/react.mk", content)
}

// generateDockerMakefile generates Docker-specific makefile.
func (g *ArtifactGenerator) generateDockerMakefile() error {
	content := `# Generated by Claude Code Bootstrap
# Docker-specific build targets

### ⇡ GENERATED BLOCK ⇡ DO NOT EDIT
.PHONY: docker-build docker-run docker-push docker-clean docker-logs docker-shell

# Docker-specific targets
docker-build:
	@echo "🐳 Building Docker image..."
	docker build -t $(shell basename $(PWD)) .
	@echo "✅ Docker build completed"

docker-run:
	@echo "🚀 Running Docker container..."
	docker run -it --rm $(shell basename $(PWD))

docker-push:
	@echo "📤 Pushing Docker image..."
	docker push $(shell basename $(PWD))
	@echo "✅ Docker push completed"

docker-clean:
	@echo "🧹 Cleaning Docker artifacts..."
	docker system prune -f
	@echo "✅ Docker cleanup completed"

docker-logs:
	@echo "📋 Showing Docker logs..."
	docker logs $(shell basename $(PWD))

docker-shell:
	@echo "🐚 Opening Docker shell..."
	docker run -it --rm $(shell basename $(PWD)) /bin/bash

docker-help:
	@echo "🐳 Docker targets:"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run Docker container"
	@echo "  docker-push  - Push Docker image"
	@echo "  docker-clean - Clean Docker artifacts"
	@echo "  docker-logs  - Show Docker logs"
	@echo "  docker-shell - Open Docker shell"
	@echo ""
### ⇣ GENERATED BLOCK ⇣ DO NOT EDIT
`
	return g.writeFile(".maestro/makefiles/docker.mk", content)
}

// Backend-specific artifact generators.
func (g *ArtifactGenerator) generateGoArtifacts() ([]string, error) {
	var files []string

	// golangci-lint.yaml.
	if err := g.generateGolangciLintConfig(); err != nil {
		return nil, err
	}
	files = append(files, ".golangci.yaml")

	return files, nil
}

func (g *ArtifactGenerator) generatePythonArtifacts() ([]string, error) {
	var files []string

	// pyproject.toml (if it doesn't exist).
	if _, err := os.Stat(filepath.Join(g.projectRoot, "pyproject.toml")); os.IsNotExist(err) {
		if err := g.generatePyprojectToml(); err != nil {
			return nil, err
		}
		files = append(files, "pyproject.toml")
	}

	// ruff.toml.
	if err := g.generateRuffConfig(); err != nil {
		return nil, err
	}
	files = append(files, "ruff.toml")

	return files, nil
}

func (g *ArtifactGenerator) generateNodeArtifacts() ([]string, error) {
	var files []string

	// .eslintrc.js.
	if err := g.generateEslintConfig(); err != nil {
		return nil, err
	}
	files = append(files, ".eslintrc.js")

	// .nvmrc.
	if err := g.generateNvmrc(); err != nil {
		return nil, err
	}
	files = append(files, ".nvmrc")

	return files, nil
}

// Helper method to write files.
func (g *ArtifactGenerator) writeFile(filename, content string) error {
	fullPath := filepath.Join(g.projectRoot, filename)

	// Create directory if it doesn't exist.
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", filename, err)
	}

	// Write file.
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filename, err)
	}

	g.logger.Debug("Generated file: %s", filename)
	return nil
}

// Additional artifact generators (stubs for now).
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

func (g *ArtifactGenerator) generateReadme(backend build.Backend) error {
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

func (g *ArtifactGenerator) generateContributing(_ /* backend */ build.Backend) error {
	content := `# Contributing

## Development Setup

1. Clone the repository
2. Install dependencies: ` + "`make build`" + `
3. Run tests: ` + "`make test`" + `
4. Run linting: ` + "`make lint`" + `

## Code Style

This project uses automated linting and formatting tools.
Run ` + "`make lint`" + ` to check your code before submitting.

## Testing

All code changes should include appropriate tests.
Run ` + "`make test`" + ` to run the test suite.

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

// GenerateDockerfile creates a Dockerfile based on the build backend.
func (g *ArtifactGenerator) GenerateDockerfile(backend build.Backend) error {
	var content strings.Builder

	switch backend.Name() {
	case languageGo:
		// Detect Go version.
		goVersion, err := g.detectGoVersion()
		if err != nil {
			g.logger.Warn("Failed to detect Go version, using default: %v", err)
			goVersion = versionGo
		}

		content.WriteString(fmt.Sprintf(`# Generated by Claude Code Bootstrap
# Multi-stage build for Go application
FROM golang:%s-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy dependency files first (for better caching)
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1001 -S appgroup && \
    adduser -S -D -H -u 1001 -h /app -s /sbin/nologin -G appgroup appuser

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/main .

# Change ownership to non-root user
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose port (adjust as needed)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ./main -health || exit 1

# Run the application
CMD ["./main"]
`, goVersion))

	case languagePython:
		// Detect Python version.
		pythonVersion, err := g.detectPythonVersion()
		if err != nil {
			g.logger.Warn("Failed to detect Python version, using default: %v", err)
			pythonVersion = versionPython
		}

		content.WriteString(fmt.Sprintf(`# Generated by Claude Code Bootstrap
FROM python:%s-slim

# Set environment variables
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

# Install system dependencies
RUN apt-get update && apt-get install -y \
    gcc \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN groupadd -r appgroup && useradd -r -g appgroup appuser

# Set working directory
WORKDIR /app

# Copy requirements first (for better caching)
COPY requirements.txt .

# Install Python dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Copy source code
COPY . .

# Change ownership to non-root user
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8000

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD python -c "import requests; requests.get('http://localhost:8000/health')" || exit 1

# Run the application
CMD ["python", "main.py"]
`, pythonVersion))

	case languageNode:
		// Detect Node version.
		nodeVersion, err := g.detectNodeVersion()
		if err != nil {
			g.logger.Warn("Failed to detect Node version, using default: %v", err)
			nodeVersion = "18"
		}

		content.WriteString(fmt.Sprintf(`# Generated by Claude Code Bootstrap
FROM node:%s-alpine

# Set environment variables
ENV NODE_ENV=production \
    NPM_CONFIG_LOGLEVEL=warn

# Install security updates
RUN apk --no-cache upgrade

# Create non-root user
RUN addgroup -g 1001 -S appgroup && \
    adduser -S -D -H -u 1001 -h /app -s /sbin/nologin -G appgroup appuser

# Set working directory
WORKDIR /app

# Copy package files first (for better caching)
COPY package*.json ./

# Install dependencies
RUN npm ci --only=production && npm cache clean --force

# Copy source code
COPY . .

# Change ownership to non-root user
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 3000

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD node -e "require('http').get('http://localhost:3000/health', (res) => process.exit(res.statusCode === 200 ? 0 : 1))" || exit 1

# Run the application
CMD ["npm", "start"]
`, nodeVersion))

	default:
		content.WriteString(fmt.Sprintf(`# Generated by Claude Code Bootstrap
FROM alpine:latest

# Install basic tools
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1001 -S appgroup && \
    adduser -S -D -H -u 1001 -h /app -s /sbin/nologin -G appgroup appuser

# Set working directory
WORKDIR /app

# Copy application files
COPY . .

# Change ownership to non-root user
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Default command
CMD ["echo", "No Dockerfile template for backend: %s"]
`, backend.Name()))
	}

	return g.writeFile("Dockerfile", content.String())
}

func (g *ArtifactGenerator) generateCIWorkflow(backend build.Backend) error {
	var content string

	switch backend.Name() {
	case languageGo:
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
	case languagePython:
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
	case languageNode:
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

	// Create .github/workflows directory.
	workflowDir := filepath.Join(g.projectRoot, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return fmt.Errorf("failed to create .github/workflows directory: %w", err)
	}

	return g.writeFile(".github/workflows/ci.yaml", content)
}
