.PHONY: build test lint run clean agentctl replayer ui-dev build-css

# Build all binaries
build:
	go generate ./...
	go build -o bin/maestro ./cmd/orchestrator
	go build -o bin/agentctl ./cmd/agentctl
	go build -o bin/replayer ./cmd/replayer

# Build the agentctl CLI tool
agentctl:
	go build -o bin/agentctl ./cmd/agentctl

# Build the replayer tool
replayer:
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
	clear && rm -rf ~/Code/maestro-work/test && ./bin/maestro -workdir ~/Code/maestro-work/test -ui

# Build Tailwind CSS
build-css:
	@echo "🎨 Building Tailwind CSS..."
	@tailwindcss -i ./web/static/css/input.css -o ./web/static/css/tailwind.css --minify
	@echo "✅ Tailwind CSS built successfully"

# Start web UI in development mode
ui-dev: build build-css
	@echo "🚀 Starting Maestro Web UI in development mode..."
	@TEMP_DIR=$$(mktemp -d) && echo "📁 Using temporary workdir: $$TEMP_DIR" && \
	./bin/orchestrator -ui -workdir=$$TEMP_DIR

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f web/static/css/tailwind.css
