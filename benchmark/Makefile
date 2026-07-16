.PHONY: build test test-race lint install-lint run clean

# Pinned for reproducible lint results; keep in sync with the root Makefile.
GOLANGCI_LINT_VERSION := v1.64.8

# Compile every package (no main packages yet; the runner CLI lands with
# Phase 1 item 3, which will add a bin/ output here).
build:
	go build ./...

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

# The runner CLI arrives with Phase 1 item 3 (runner-core); nothing to run yet.
run:
	@echo "No runner CLI yet — it lands with Phase 1 item 3 (runner-core)."
	@echo "Available now: make build, make test, make test-race, make lint."
	@exit 1

clean:
	rm -rf bin/
