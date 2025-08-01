.PHONY: build test lint run clean agentctl replayer maestro ui-dev build-css fix fix-imports fix-godot install-lint install-goimports

# Build all binaries
build: lint
	go generate ./...
	go build -o bin/maestro ./cmd/orchestrator
	go build -o bin/agentctl ./cmd/agentctl
	go build -o bin/replayer ./cmd/replayer

# Build the agentctl CLI tool
agentctl: lint
	go build -o bin/agentctl ./cmd/agentctl

# Build the maestro CLI tool
maestro: lint
	go build -o bin/maestro ./cmd/orchestrator

# Build the replayer tool
replayer: lint
	go build -o bin/replayer ./cmd/replayer

# Run all tests
test:
	go test ./...

# Install golangci-lint if not present
install-lint:
	@which golangci-lint > /dev/null || { \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	}

# Install goimports if not present
install-goimports:
	@which goimports > /dev/null || { \
		echo "Installing goimports..."; \
		go install golang.org/x/tools/cmd/goimports@latest; \
	}

# Fix import formatting automatically
fix-imports: install-goimports
	@echo "Fixing import formatting with goimports..."
	goimports -w .
	@echo "Import formatting fixed"

# Fix godot comment period issues automatically
fix-godot:
	@echo "Fixing godot comment period issues..."
	@find . -name "*.go" -not -path "./vendor/*" -not -path "./.git/*" | \
		xargs sed -i '' -E 's|^(\s*//\s*[A-Z][^.]*[a-zA-Z0-9)])\s*$$|\1.|g'
	@echo "Godot comment issues fixed"

# Run all automatic fixes
fix: fix-imports fix-godot
	@echo "All automatic fixes applied"

# Run linting tools
lint: install-lint
	golangci-lint run

# Lint documentation (markdown files)
lint-docs:
	@echo "Linting documentation files..."
	@find . -name "*.md" -not -path "./work/*" -not -path "./status/*" -not -path "./logs/*" | while read file; do \
		echo "Checking $$file"; \
		if ! grep -q "^# " "$$file"; then \
			echo "Warning: $$file may be missing a top-level heading"; \
		fi; \
	done
	@echo "Documentation lint completed"

# Run the orchestrator with banner
run: build-css build
	clear && rm -rf ~/Code/maestro-work/test && ./bin/maestro -workdir ~/Code/maestro-work/test -ui 2>&1 | tee logs/run.log

# Build Tailwind CSS
build-css:
	@echo "🎨 Building Tailwind CSS..."
	@tailwindcss -i ./web/static/css/input.css -o ./web/static/css/tailwind.css --minify
	@echo "✅ Tailwind CSS built successfully"

# Start web UI in development mode
ui-dev: build build-css
	@echo "🚀 Starting Maestro Web UI in development mode..."
	@TEMP_DIR=$$(mktemp -d) && echo "📁 Using temporary workdir: $$TEMP_DIR" && \
	./bin/maestro -ui -workdir=$$TEMP_DIR

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f web/static/css/tailwind.css
