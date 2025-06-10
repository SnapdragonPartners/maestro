.PHONY: build test lint run clean agentctl replayer

# Build the orchestrator binary
build:
	go build -o bin/orchestrator .

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
	go fmt ./...
	staticcheck ./...

# Run the orchestrator with banner
run: build
	./bin/orchestrator

# Clean build artifacts
clean:
	rm -rf bin/