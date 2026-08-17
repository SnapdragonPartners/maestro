.PHONY: build test test-integration test-gcs test-e2e test-all test-coverage check-coverage lint lint-state run clean maestro benchmark ui-dev build-css fix fix-imports fix-godot install-lint install-goimports build-mcp-proxy install-hooks benchmark-build benchmark-test benchmark-lint

# Directory for embedded proxy binaries (must be in package dir for go:embed)
EMBEDDED_DIR := pkg/coder/claude/embedded

# Build-time ldflags: inject issue reporting key if set in environment
LDFLAGS :=
ifdef ISSUE_REPORTING_KEY
LDFLAGS += -X orchestrator/pkg/version.IssueReportingKey=$(ISSUE_REPORTING_KEY)
endif

# Install git hooks from hooks/ directory (non-fatal for read-only checkouts / CI)
install-hooks:
	@if [ -d .git ] && [ -w .git/hooks ]; then \
		cp hooks/pre-commit .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit; \
		cp hooks/pre-push .git/hooks/pre-push && chmod +x .git/hooks/pre-push; \
		echo "✅ Git hooks installed"; \
	fi

# Build all binaries (includes MCP proxy for embedding)
# Note: build-mcp-proxy must run before lint because go:embed requires files to exist
build: install-hooks build-css build-mcp-proxy lint benchmark-build
	go generate ./...
	go build -ldflags "$(LDFLAGS)" -o bin/maestro ./cmd/maestro

# --- benchmark runner module (benchmark/) ---
# A standalone Go module (ADR 0025: black-box, never imports orchestrator).
# Root Go walkers do not descend into nested modules, so these delegate to
# benchmark/Makefile and are wired as prerequisites of build/test/lint.
benchmark-build:
	$(MAKE) -C benchmark build

benchmark-test:
	$(MAKE) -C benchmark test

benchmark-lint:
	$(MAKE) -C benchmark lint

# Cross-compile MCP proxy for Linux containers (ARM64 and AMD64)
build-mcp-proxy:
	@echo "🔨 Cross-compiling MCP proxy for Linux..."
	@mkdir -p $(EMBEDDED_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o $(EMBEDDED_DIR)/proxy-linux-arm64 ./cmd/maestro-mcp-proxy
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(EMBEDDED_DIR)/proxy-linux-amd64 ./cmd/maestro-mcp-proxy
	@echo "✅ MCP proxy binaries built: $(EMBEDDED_DIR)/proxy-linux-{arm64,amd64}"

# Build the maestro CLI tool
maestro: build-mcp-proxy lint
	go build -ldflags "$(LDFLAGS)" -o bin/maestro ./cmd/maestro

# Build the benchmark runner
benchmark: lint
	go build -o bin/benchmark ./cmd/benchmark

# Run all tests with coverage
test: benchmark-test
	go test -cover ./...

# Run integration tests only (requires API keys and external services)
test-integration:
	@echo "🧪 Running integration tests..."
	go test -tags=integration -cover -timeout=20m ./...

# Run the GCS adapter tests against a REAL Google Cloud Storage bucket.
#
# Deliberately NOT under the `integration` tag: pre-push runs test-integration,
# and requiring cloud credentials to push would either block anyone without
# them or skip silently and look green. The bucket must be versioned and have
# soft delete DISABLED — with soft delete on, these pass while reclaiming
# nothing, which is the failure recorded on #286.
test-gcs:
	@echo "☁️  Running GCS adapter tests against a real bucket..."
	@echo "   Requires: MAESTRO_GCS_TEST_BUCKET and application default credentials"
	@test -n "$(MAESTRO_GCS_TEST_BUCKET)" || \
		(echo "❌ MAESTRO_GCS_TEST_BUCKET is not set; refusing to run and report a green skip" && exit 1)
	go test -tags=gcs -count=1 -timeout=10m ./internal/dataplane/objects/

# Run E2E tests (full workflow tests requiring Docker, Gitea, real Git operations)
test-e2e:
	@echo "🚀 Running E2E tests..."
	@echo "   Requires: Docker, network access to GitHub test repo"
	go test -tags=e2e -cover -timeout=30m ./tests/...

# Run all tests including integration tests (combines unit and integration)
test-all:
	@echo "🔬 Running all tests (unit + integration)..."
	go test -tags=integration -cover -timeout=20m ./...

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

# Pinned so lint results are reproducible across CI runs and dev machines;
# @latest silently changes lint behavior (and busts CI caches) on new releases.
GOLANGCI_LINT_VERSION := v1.64.8
# Pinned so CI and local runs generate identical output; a version drift
# would show up as spurious diffs in the sqlc-check.
SQLC_VERSION := v1.31.1

# Install golangci-lint if not present; warn (don't force-reinstall) on a
# version mismatch — a PATH-shadowing install (e.g. homebrew) would win over
# a reinstall anyway, so a loud warning beats a silent no-op loop.
install-lint:
	@which golangci-lint > /dev/null || { \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	}
	@golangci-lint version 2>/dev/null | grep -q "$(GOLANGCI_LINT_VERSION)" || \
		echo "⚠️  golangci-lint on PATH is not $(GOLANGCI_LINT_VERSION); lint results may differ from CI"

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
lint: install-lint benchmark-lint
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

# --- v2 data plane (Phase 2) -------------------------------------------
#
# The one command from a clean checkout. Idempotent: safe to re-run, and
# the same command serves first-time setup and the everyday inner loop.
# Deliberately separate from the agent-container and benchmark-Gitea
# machinery, so a data-plane restart cannot disturb a benchmark run.
.PHONY: dataplane-up dataplane-down dataplane-reset dataplane-migrate dataplane-force-version dataplane-backup dataplane-restore dataplane-verify dataplane-recover-key

dataplane-up:
	go run ./cmd/dataplanectl up

dataplane-down:
	go run ./cmd/dataplanectl down

# --- sqlc ---------------------------------------------------------------
#
# Generated output is COMMITTED, so a clean checkout builds without sqlc
# installed. That only stays true if regeneration is checked: sqlc-check
# regenerates and fails if the tree moved, which is what catches a migration
# edited without regenerating.
.PHONY: install-sqlc sqlc-generate sqlc-check

install-sqlc:
	@which sqlc > /dev/null || { \
		echo "Installing sqlc $(SQLC_VERSION)..."; \
		go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION); \
	}
	@sqlc version 2>/dev/null | grep -q "$(SQLC_VERSION)" || \
		echo "⚠️  sqlc on PATH is not $(SQLC_VERSION); generated output may differ from CI and show up as sqlc-check failures"

sqlc-generate: install-sqlc
	sqlc generate

# git status, not git diff: diff only examines TRACKED files, so a new
# query generating a new .sql.go file would leave the check passing while
# the generated set is incomplete. --untracked-files=all catches the new
# file, and porcelain also reports modifications and deletions.
sqlc-check: sqlc-generate
	@status=$$(git status --porcelain --untracked-files=all -- internal/dataplane/gen); \
	if [ -n "$$status" ]; then \
		echo "$$status"; \
		echo "❌ generated code is stale or incomplete: run 'make sqlc-generate' and commit the result"; \
		exit 1; \
	fi
	@echo "✅ generated code matches the schema"

# Apply pending migrations to an already-running stack. `dataplane-up` also
# migrates, so this is for iterating on a migration without a full cycle.
dataplane-migrate:
	go run ./cmd/dataplanectl migrate

# Destructive: deletes the Postgres cluster and object store. Prompts
# unless FORCE=1.
#
# `filter 1`, not a bare `if $(FORCE)`: Make's if is true for ANY non-empty
# value, so FORCE=0 would skip the confirmation on a destructive command --
# the opposite of what someone typing 0 intends. Only the exact value 1
# suppresses the prompt; anything else prompts, which is the safe default.
dataplane-reset:
	go run ./cmd/dataplanectl $(if $(filter 1,$(FORCE)),-force,) reset

# Repair a DIRTY schema version after a failed migration, by recording
# VERSION without running anything. Metadata only: if the schema is not
# really at VERSION, nothing will detect the disagreement.
#
# Same `filter 1` rule as reset -- only the exact value 1 suppresses the
# confirmation, so FORCE=0 still prompts.
dataplane-force-version:
	@test -n "$(VERSION)" || { echo "usage: make dataplane-force-version VERSION=<n> [FORCE=1]"; exit 1; }
	go run ./cmd/dataplanectl $(if $(filter 1,$(FORCE)),-force,) -version $(VERSION) force-version

# Cold backup: stop the plane, copy the data root to DEST, restart whatever
# was running. DEST must NOT already exist -- an archive is identified by the
# manifest written last inside it, so a pre-existing directory could not be
# told apart from a completed one.
#
# The archive deliberately EXCLUDES the root-of-trust key, so restoring it on
# another machine needs the key file as well (or new-key recovery). Backup
# never reads the key, which is what makes that exclusion structural.
dataplane-backup:
	@test -n "$(DEST)" || { echo "usage: make dataplane-backup DEST=<directory>"; exit 1; }
	go run ./cmd/dataplanectl -to $(DEST) backup

# Destructive: replaces the data root with SRC's contents. Same `filter 1`
# rule as reset -- only the exact value 1 suppresses the refusal, so FORCE=0
# still refuses a populated root.
dataplane-restore:
	@test -n "$(SRC)" || { echo "usage: make dataplane-restore SRC=<directory> [FORCE=1]"; exit 1; }
	go run ./cmd/dataplanectl $(if $(filter 1,$(FORCE)),-force,) -from $(SRC) restore

# Recompute every stored digest and read every attachment. This is what
# validates a restore: a torn Postgres/object-store pair is invisible to
# either store alone.
dataplane-verify:
	go run ./cmd/dataplanectl verify

# DESTRUCTIVE, and unlike reset there is no archive that undoes it: every
# stored secret is deleted, because the ciphertext was written under a key
# nobody has any more. Same `filter 1` rule -- only the exact value 1
# suppresses the prompt.
dataplane-recover-key:
	go run ./cmd/dataplanectl $(if $(filter 1,$(FORCE)),-force,) recover-key

# --- benchmark import ---------------------------------------------------
#
# Provisioning and import, mirroring the dataplane-* family. ORG and USER
# are required by bootstrap because nothing else creates either; the
# importer resolves them and never creates them, since an import that
# silently provisions a tenant is a defect waiting for team mode.
.PHONY: dataplane-bootstrap benchmark-import benchmark-show

dataplane-bootstrap:
	@test -n "$(ORG)" || { echo "usage: make dataplane-bootstrap ORG=<slug> USER=<handle>"; exit 1; }
	@test -n "$(USER)" || { echo "usage: make dataplane-bootstrap ORG=<slug> USER=<handle>"; exit 1; }
	go run ./cmd/dataplanectl -org $(ORG) -user $(USER) bootstrap

# SUITE is optional: omitted, it imports every suite the results store
# holds, which is the store's own layout answering rather than a list
# somebody has to keep current.
benchmark-import:
	@test -n "$(ORG)" || { echo "usage: make benchmark-import ORG=<slug> OPERATOR=<handle> [SUITE=<id>]"; exit 1; }
	@test -n "$(OPERATOR)" || { echo "usage: make benchmark-import ORG=<slug> OPERATOR=<handle> [SUITE=<id>]"; exit 1; }
	go run ./cmd/dataplanectl -org $(ORG) -operator $(OPERATOR) $(if $(SUITE),-suite $(SUITE),) benchmark import

benchmark-show:
	@test -n "$(ORG)" || { echo "usage: make benchmark-show ORG=<slug> SUITE=<id>"; exit 1; }
	@test -n "$(SUITE)" || { echo "usage: make benchmark-show ORG=<slug> SUITE=<id>"; exit 1; }
	go run ./cmd/dataplanectl -org $(ORG) -suite $(SUITE) benchmark show

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f pkg/webui/web/static/css/tailwind.css
	rm -f $(EMBEDDED_DIR)/proxy-linux-*
