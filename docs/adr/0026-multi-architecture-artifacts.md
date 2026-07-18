+++
title = "ADR 0026: Multi-Architecture Distributable Artifacts"
edit_date = "2026-07-18"
status = "proposed"
summary = "Every artifact Maestro builds on one architecture and executes on another — embedded/packaged binaries and published container images — must be built for all target architectures (at least linux/amd64 + linux/arm64), pinned by an arch-independent digest, and verified on each arch. Single-arch builds of cross-arch artifacts are a defect."
+++

# 0026. Multi-Architecture Distributable Artifacts

Status: Proposed (2026-07-18) — pending Codex + DR review.

## Context

Maestro is developed on **arm64** (Apple Silicon Macs) and runs its CI and (for v2) its production/benchmark workloads on **amd64** Linux. Any artifact produced on one architecture and executed on another must carry both architectures, or it fails at execution time — not at build time — with the confusing, arch-silent error `exec format error`, and only on the architecture that was not built.

This failure mode is insidious because it is **invisible on the build machine**: it passes locally (the dev arch), then fails remotely (CI or prod arch), and the error message never says "architecture." It has now recurred twice:

- **The MCP proxy binaries** embedded into the app (`pkg/coder/claude/embedded/proxy-linux-{amd64,arm64}`, selected at runtime; `make build-mcp-proxy` must run before `go:embed`). These are cross-compiled per-arch precisely because a single-arch embed would break the arch it was not built on.
- **The benchmark union dependency-cache image** (item 5.1, `benchmark/cache/`). Its first publish was built on an arm64 Mac and pushed arm64-only; the amd64 CI runner then failed the offline proof with `exec format error`. The fix was a multi-arch manifest.

Two recurrences of the same defect in different subsystems is the signal that this belongs in a durable, referenceable standard rather than being rediscovered each time.

## Decision

Every **distributable artifact consumed across architectures** — packaged or embedded binaries, and published container images — MUST be built for all target architectures (at minimum `linux/amd64` and `linux/arm64`) and **verified on each**, not just built.

- **Container images:** build a multi-arch manifest — `docker buildx build --platform linux/amd64,linux/arm64 --push` — and pin consumers by the **arch-independent manifest (index) digest**, never a per-arch image digest and never a mutable tag. A host resolves the manifest digest to its native arch at pull time. (See `benchmark/cache/publish.sh` for the canonical shape; the golang base it builds `FROM` is itself pinned by a multi-arch manifest digest.)
- **Embedded/packaged binaries:** cross-compile for each target (Go: the `GOOS`/`GOARCH` matrix) and select the right one at runtime by the running architecture — the MCP proxy pattern.
- **Verify per-arch.** A successful build proves nothing about the arch you did not run. CI must exercise the artifact on **its own** arch (e.g. the benchmark cache image's `GOPROXY=off` offline proof runs on amd64 CI), so a regression to single-arch fails a check rather than a production run. When the build machine's arch differs from a target arch, close the gap with emulation (QEMU + buildx) in the publish step or a native multi-runner.
- **Publishing owns the cross-build cost, not PR CI.** Multi-arch builds under emulation are slow; that cost sits in the manually dispatched maintainer/release step (which sets up QEMU + Buildx), while PR CI only pulls and verifies.

## Consequences

- New distributable artifacts inherit this checklist. **Reviewers flag a single-arch build of a cross-arch artifact as a defect** (P1), the same way a mutable-tag pin is flagged.
- The publish/release path requires buildx + QEMU (cross-arch from an arm64 dev machine) or native multi-arch runners; this is a one-time setup per artifact, documented alongside it.
- Pinning by manifest digest keeps identity reproducible (identical inputs → identical bytes per arch) while remaining arch-portable — the two properties are not in tension.
- Cost: emulated cross-builds add minutes to a release, never to a PR.

## Related Documents

- `benchmark/cache/README.md` and [design_cost_latency.md](../v2/phase_1/design_cost_latency.md) — the union cache image, the motivating recurrence and the canonical multi-arch image shape.
- `pkg/coder/claude/embedded/` — the MCP proxy per-arch embedding, the prior recurrence.
- [ADR 0021](0021-artifacts-and-principal-instances.md) — artifact identity (digests, content-addressing).
