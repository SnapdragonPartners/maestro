+++
title = "Union Dependency-Cache Image"
edit_date = "2026-07-17"
status = "live"
summary = "The golden-cache union image (item 5.1, #268): one image baking every Go fixture's module cache so v1 coder runs skip the cold-download tax, with a GOPROXY=off deterministic acceptance proof and a maintainer-only publish path."
+++

# Union Dependency-Cache Image

Item 5.1 (`cost-latency`, [#268](https://github.com/SnapdragonPartners/maestro/issues/268)); design in [design_cost_latency.md](../../docs/v2/phase_1/design_cost_latency.md).

A single image bakes **every golden Go fixture's module cache** into an immutable layer, so a v1 coder run no longer pays the cold `go mod download` tax (discovery-011 lost ~40% of its wall clock to it). The cache lives in `GOMODCACHE`; the image carries no fixture source (runs mount that separately). The base is pinned by digest to the same `golang:1.26` the configs use, so the toolchain matches a non-cached run.

## Files

- `fixtures.txt` — the source of truth: one `<name> <owner/repo> <commit>` line per Go fixture whose cache is baked in. Keep in sync with the `[fixture]` pins in `../stories/*.toml`.
- `Dockerfile` — `FROM` the pinned base; `COPY`s the staged `context/` and runs `go mod download` per fixture.
- `build.sh` (`make cache-build`) — stages each fixture's `go.mod`/`go.sum` at its pinned commit into `context/`, then builds. Needs network.
- `verify.sh` (`make cache-verify`) — the deterministic acceptance test: runs each fixture's `go mod download` with `GOPROXY=off` so success is only possible if every dep is already cached. A missing dep fails offline, loudly.
- `publish.sh` (`make cache-publish`) — **maintainer/release only**: pushes and prints the digest to pin.

## Lifecycle

- **Maintainer/release publishes.** The [`Publish golden-cache image`](../../.github/workflows/release-cache-image.yml) workflow (manually dispatched, `packages: write` via the built-in `GITHUB_TOKEN`) builds, verifies, and pushes to `ghcr.io/snapdragonpartners/golden-cache`, then prints the immutable digest. Pin that digest into `../configs/*.toml` (`container_image`). This is the **only** path that moves the tracked digest — run it when a fixture re-pins.
- **PR CI is read-only.** CI pulls the committed digest and runs `cache-verify`; it never pushes and never rewrites the digest. A maintainer who re-pinned a fixture but forgot to republish is caught loudly by the offline check.

The image must be **public** on GHCR so PR CI (and every machine) can pull it by digest with no auth — it holds only cached public Go modules, no secrets.
