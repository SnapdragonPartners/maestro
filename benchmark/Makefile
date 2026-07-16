.PHONY: build test test-race lint install-lint run clean

# Pinned for reproducible lint results; keep in sync with the root Makefile.
GOLANGCI_LINT_VERSION := v1.64.8

# Compile every package and the runner CLI.
build:
	go build ./...
	@mkdir -p bin
	go build -o bin/runner ./cmd/runner

test:
	go test -cover ./...

test-race:
	go test -race ./...

install-lint:
	@which golangci-lint > /dev/null || { \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	}
	@golangci-lint version 2>/dev/null | grep -q "$(GOLANGCI_LINT_VERSION)" || \
		echo "⚠️  golangci-lint on PATH is not $(GOLANGCI_LINT_VERSION); lint results may differ from CI"

# Uses the repo-root .golangci.yaml (config discovery walks up).
lint: install-lint
	go fmt ./...
	golangci-lint run

# Validate the authored stories and configs (no execution, no cost).
# Executing a suite spends real tokens against real targets, so it stays an
# explicit command: bin/runner run --stories stories --configs <bundles> ...
run: build
	./bin/runner validate --stories stories --configs configs

clean:
	rm -rf bin/
