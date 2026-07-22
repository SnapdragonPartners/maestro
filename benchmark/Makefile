.PHONY: build test test-race lint install-lint run clean golden-minimal golden-all golden-tiers-check cache-build cache-verify cache-publish

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
	rm -rf bin/ cache/context/

# --- Suite tiers (ADR 0025) -------------------------------------------------
#
# golden-minimal: the cheapest rungs, N=1 — the "is the harness alive" check,
#   required at the end of every phase.
# golden-all:     every active story, N=1 — the conformance proof, run at each
#   phase end from Phase 2 onward (~$50 budget).
#
# N=1 for both: a tier names a STORY SET, while N follows the run's PURPOSE.
# Conformance asks whether each rung still behaves, for which one pass per
# story is the honest unit; N=3 is comparison sampling and belongs to Phase 1B.
#
# WHY MEMBERSHIP LIVES HERE AND NOT ON THE STORY. A `tier` field would be
# self-documenting and validated, which is the better design in the abstract —
# but the story content hash covers the whole definition, so adding a field to
# smoke-comment and dep-bump-xnet would move their hashes and break
# comparability with the v1 baseline just recorded against them
# (notes_conformance-log.md). Tier membership is a suite-composition concern,
# not a property of the story, so it is expressed here where changing it costs
# nothing. golden-tiers-check guards the drift this trades for.
GOLDEN_MINIMAL := smoke-comment dep-bump-xnet
GOLDEN_CONFIG  ?= paired-default
GOLDEN_SUITE   ?= $(shell date +%Y%m%d-%H%M%S)

# Fail loudly if a tier names a story that no longer exists — the one real
# risk of keeping membership out of the story files.
golden-tiers-check: build
	@for s in $(GOLDEN_MINIMAL); do \
		test -f stories/$$s.toml || { echo "❌ golden-minimal names a missing story: $$s"; exit 1; }; \
	done
	@echo "✅ tier membership resolves: $(GOLDEN_MINIMAL)"

# Spends real tokens. Kept explicit for that reason — no target runs a suite
# as a side effect of building or testing.
# ONE invocation, so the tier is one suite: separate runs sharing a suite ID
# would each rewrite the manifest and restart budget accounting from zero.
golden-minimal: golden-tiers-check
	./bin/runner run --config $(GOLDEN_CONFIG) \
		--story "$(shell echo $(GOLDEN_MINIMAL) | tr ' ' ',')" \
		--suite-id golden-minimal-$(GOLDEN_SUITE)

golden-all: build
	./bin/runner run --config $(GOLDEN_CONFIG) --suite-id golden-all-$(GOLDEN_SUITE)

# --- Union dependency-cache image (item 5.1, #268) ---
# Bakes every golden Go fixture's module cache into one image so v1 coder
# runs skip the cold-download tax. See docs/v2/phase_1/design_cost_latency.md.

# Build the union image locally (needs network to warm the cache).
cache-build:
	./cache/build.sh

# Deterministic acceptance test: GOPROXY=off offline `go mod download` proves
# every fixture's deps are baked in. The CI gate; run after cache-build, or
# pass a published digest: make cache-verify ARGS=ghcr.io/...@sha256:...
cache-verify:
	./cache/verify.sh $(ARGS)

# Maintainer/release ONLY: push the image and print the digest to pin into
# configs/*.toml. Needs GHCR push credentials; PR CI never runs this.
cache-publish:
	./cache/publish.sh
