.PHONY: build test test-integration test-e2e test-all test-coverage check-coverage lint lint-state run clean maestro ui-dev build-css fix fix-imports fix-godot install-lint install-goimports build-mcp-proxy install-hooks

# Directory for embedded proxy binaries (must be in package dir for go:embed)
EMBEDDED_DIR := pkg/coder/claude/embedded

# Install git hooks from hooks/ directory (non-fatal for read-only checkouts / CI)
install-hooks:
	@if [ -d .git ] && [ -w .git/hooks ]; then \
		cp hooks/pre-commit .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit; \
		cp hooks/pre-push .git/hooks/pre-push && chmod +x .git/hooks/pre-push; \
		echo "✅ Git hooks installed"; \
	fi

# Build all binaries (includes MCP proxy for embedding)
# Note: build-mcp-proxy must run before lint because go:embed requires files to exist
build: install-hooks build-css build-mcp-proxy lint
	go generate ./...
	go build -o bin/maestro ./cmd/maestro

# Cross-compile MCP proxy for Linux containers (ARM64 and AMD64)
build-mcp-proxy:
	@echo "🔨 Cross-compiling MCP proxy for Linux..."
	@mkdir -p $(EMBEDDED_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o $(EMBEDDED_DIR)/proxy-linux-arm64 ./cmd/maestro-mcp-proxy
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(EMBEDDED_DIR)/proxy-linux-amd64 ./cmd/maestro-mcp-proxy
	@echo "✅ MCP proxy binaries built: $(EMBEDDED_DIR)/proxy-linux-{arm64,amd64}"

# Build the maestro CLI tool
maestro: build-mcp-proxy lint
	go build -o bin/maestro ./cmd/maestro

# Run all tests with coverage
test:
	go test -cover ./...

# Run integration tests only (requires API keys and external services)
test-integration:
	@echo "🧪 Running integration tests..."
	go test -tags=integration -cover -timeout=10m ./...

# Run E2E tests (full workflow tests requiring Docker, Gitea, real Git operations)
test-e2e:
	@echo "🚀 Running E2E tests..."
	@echo "   Requires: Docker, network access to GitHub test repo"
	go test -tags=e2e -cover -timeout=30m ./tests/...

# Run all tests including integration tests (combines unit and integration)
test-all:
	@echo "🔬 Running all tests (unit + integration)..."
	go test -tags=integration -cover -timeout=10m ./...

# Run tests and generate detailed coverage report
test-coverage:
	@echo "📊 Running tests with coverage reporting..."
	@mkdir -p coverage
	go test -coverprofile=coverage/coverage.out ./...
	go tool cover -html=coverage/coverage.out -o coverage/coverage.html
	@echo "✅ Coverage report generated: coverage/coverage.html"

# Check coverage for key packages and fail if below 80%
check-coverage:
	@echo "🎯 Checking coverage for key packages..."
	@COVERAGE_FAIL=0; \
	for pkg in pkg/agent pkg/dispatch pkg/coder pkg/architect; do \
		OUTPUT=$$(go test -cover ./$$pkg 2>&1); \
		COVERAGE=$$(echo "$$OUTPUT" | grep -o '[0-9]\+\.[0-9]\+%' | tr -d '%' | head -1); \
		if [ -z "$$COVERAGE" ]; then COVERAGE="0.0"; fi; \
		echo "📈 $$pkg: $${COVERAGE}%"; \
		if [ "$$(echo "$$COVERAGE < 80.0" | bc -l 2>/dev/null || python3 -c "print(1 if $$COVERAGE < 80.0 else 0)" 2>/dev/null || echo "1")" = "1" ]; then \
			echo "❌ Coverage for $$pkg ($${COVERAGE}%) is below 80% threshold"; \
			COVERAGE_FAIL=1; \
		else \
			echo "✅ Coverage for $$pkg ($${COVERAGE}%) meets 80% threshold"; \
		fi; \
	done; \
	if [ $$COVERAGE_FAIL -eq 1 ]; then \
		echo "💥 Coverage check failed - some packages below 80% threshold"; \
		exit 1; \
	else \
		echo "🎉 All key packages meet 80% coverage threshold"; \
	fi

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
	go fmt ./...
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

# Lint state access patterns (type assertions and magic strings)
lint-state:
	@echo "🔍 Linting state access patterns..."
	@./scripts/lint-state-access.sh ./pkg

# Run the orchestrator with banner
run: build-css build
	clear && rm -rf ~/Code/maestro-work/test && ./bin/maestro -workdir ~/Code/maestro-work/test -ui 2>&1 | tee logs/run.log

# Build Tailwind CSS (optional - skipped if tailwindcss not installed)
build-css:
	@if command -v tailwindcss >/dev/null 2>&1; then \
		echo "🎨 Building Tailwind CSS..."; \
		tailwindcss -i ./pkg/webui/web/static/css/input.css -o ./pkg/webui/web/static/css/tailwind.css --minify; \
		echo "✅ Tailwind CSS built successfully"; \
	else \
		echo "⏭️  Skipping Tailwind CSS build (tailwindcss not installed, using committed CSS)"; \
	fi

# Start web UI in development mode
ui-dev: build build-css
	@echo "🚀 Starting Maestro Web UI in development mode..."
	@TEMP_DIR=$$(mktemp -d) && echo "📁 Using temporary workdir: $$TEMP_DIR" && \
	./bin/maestro -ui -workdir=$$TEMP_DIR

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f pkg/webui/web/static/css/tailwind.css
	rm -f $(EMBEDDED_DIR)/proxy-linux-*
