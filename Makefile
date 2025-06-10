.PHONY: build test lint run clean

# Build the orchestrator binary
build:
	go build -o bin/orchestrator .

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