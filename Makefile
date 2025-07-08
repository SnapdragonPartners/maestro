.PHONY: build test lint run clean agentctl replayer

# Build all binaries
build:
	go build -o bin/orchestrator .
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

# Run linting tools
lint:
	go fix ./...
	go fmt -s -w ./...
	staticcheck ./...

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
run: build
	./bin/orchestrator

# Clean build artifacts
clean:
	rm -rf bin/
