+++
title = "Union Dependency-Cache Image"
edit_date = "2026-07-22"
status = "live"
summary = "The golden-cache union image (item 5.1, #268): one image baking every Go fixture's module cache so v1 coder runs skip the cold-download tax, with a GOPROXY=off deterministic acceptance proof and a maintainer-only publish path."
+++

# Union Dependency-Cache Image

Item 5.1 (`cost-latency`, [#268](https://github.com/SnapdragonPartners/maestro/issues/268)); design in [design_cost_latency.md](../../docs/v2/phase_1/design_cost_latency.md).

A single image bakes **every golden Go fixture's module cache** into an immutable layer, so a v1 coder run no longer pays the cold `go mod download` tax (discovery-011 lost ~40% of its wall clock to it). The cache lives in `GOMODCACHE`; the image carries no fixture source (runs mount that separately). The base is pinned by digest to the same `golang:1.26` the configs use, so the toolchain matches a non-cached run. The published image is a **multi-arch manifest** (`linux/amd64` + `linux/arm64`) pinned by its arch-independent manifest digest, so arm64 dev machines and amd64 CI each pull their matching arch.

## Files

- `fixtures.txt` — the source of truth: one `<name> <owner/repo> <commit>` line per Go fixture whose cache is baked in. Keep in sync with the `[fixture]` pins in `../stories/*.toml`.
- `Dockerfile` — `FROM` the pinned (multi-arch) base; `COPY`s the staged `context/` and runs `go mod download` per fixture.
- `stage.sh` — fetches each fixture's `go.mod`/`go.sum` at its pinned commit into `context/` (the build context). Shared by build.sh and publish.sh; needs network.
- `build.sh` (`make cache-build`) — local, **host-arch only**: stage + `docker build --load`, for iterating and running `cache-verify` during development. Never pin a locally built image.
- `verify.sh` (`make cache-verify`) — the deterministic acceptance test: runs each fixture's `go mod download` with `GOPROXY=off` so success is only possible if every dep is already cached. A missing dep fails offline, loudly.
- `publish.sh` (`make cache-publish`) — **maintainer/release only**: multi-arch `buildx --push` then prints the manifest digest to pin.

## Lifecycle

- **Maintainer/release publishes.** The [`Publish golden-cache image`](../../.github/workflows/release-cache-image.yml) workflow (manually dispatched, `packages: write` via the built-in `GITHUB_TOKEN`) builds, verifies, and pushes to `ghcr.io/snapdragonpartners/golden-cache`, then prints the immutable digest. Pin that digest into `../configs/*.toml` (`container_image`). This is the **only** path that moves the tracked digest — run it when a re-pin leaves the image without coverage of the current pins (`cache-verify` decides; a re-pin the image still covers needs no republish).
- **PR CI is read-only.** CI pulls the committed digest and runs `cache-verify`; it never pushes and never rewrites the digest. A re-pin that leaves the image without coverage of the current pins is caught loudly by the offline check; a re-pin it still covers needs no republish (amended 2026-07-22, PROPOSED — see design_cost_latency.md). Coverage is the trigger, not the pin string, and the check proves coverage, not input equality.

The image must be **public** on GHCR so PR CI (and every machine) can pull it by digest with no auth — it holds only cached public Go modules, no secrets.
