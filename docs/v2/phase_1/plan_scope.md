+++
title = "Maestro v2 Phase 1: Scope And Plan"
edit_date = "2026-07-16"
status = "live"
summary = "Approved Phase 1 scope and execution plan: build the golden story runner per ADR 0025 — 11 serial work items covering the runner module, fixtures, the v1-as-patched target, the single-agent baseline, D9 cost instrumentation, and the first 5-10 stories."
type = "plan"
+++

# Phase 1: Golden Stories And Measurement Harness — Scope And Plan

Status: live — approved by Codex and DR, 2026-07-15 (PR #259; the status flip landed one commit late, in a follow-up). Flips to `archive` when Phase 1 closes (lifecycle per ADR 0017 and the Phase 0 precedent).

Goal (from the [roadmap](../plan_roadmap.md)): build the measuring instrument before rewriting the machine.

Phase 1 implements exactly [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) — the golden story schema, the black-box runner with self-contained persistence, per-target adapters and the normalized run-record contract, the D9 sampling/budget mechanism, and the two mandatory MPH configurations. Where this plan and that ADR diverge, the ADR wins; this plan only sequences the work and fixes the decisions the ADR explicitly delegated to Phase 1 (definition directory, module location, format choices).

## Scope

In scope:

- The runner as a new, standalone deliverable (the port inventory classifies v1 `pkg/benchmark`/`cmd/benchmark` as **rewrite, Phase 1**; the SWE-EVO Gitea-fixture mechanics are salvage seeds only, per breaking-change principle 4).
- Golden story definition format, loader, and validation.
- Fixture repositories: forked, pinned variants of `maestro-llms` and `maestro-cms`, plus the standalone LLM-tester CLI app as the first app-bearing fixture (ADR 0025).
- The v1-as-patched target: the **minimal** patch set to the current `main` factory path so golden stories can pass, plus the v1 target adapter normalizing its observations (SQLite, logs, branches, PRs) into run records.
- The single-agent happy-path baseline as a second target adapter (headless Claude Code) — Phase 1 exit-blocking per reviewer question 3's resolution.
- MPH configuration bundles, file-based, content-hash identified; minimal prompt identification for targets without prompt packs.
- D9 instrumented cost runs; fixing N and budget-cap numbers; runner-enforced budgets with overrun-as-failure.
- Comparison reports with per-metric-class aggregation and repeat-run spread (ADR 0025 semantics).
- First 5–10 golden stories, single-repo, Story-scoped, from the ladder's low rungs.
- `golden-minimal` / `golden-all` suite tiers and make targets (build process).
- The v1-derived baseline on `golden-minimal`, reported at phase exit.
- Upstream wishlist entries for reusable metrics pieces discovered along the way (`maestro-llms`, `maestro-cms`), per breaking-change principle 2.

Out of scope:

- Importing runner results into the data plane (Phase 2's vertical slice; the record shapes are designed for it, per ADR 0025).
- Any Postgres/data-plane work (Phase 2).
- The prompt pack system (pillar 10, backlog candidate 5). Phase 1 carries only pack label + content hash in MPH identity.
- Industry benchmark adapters (SWE-EVO successor work) — the adapter contract anticipates them; none are built.
- Multi-repo stories, browser/UI-evidence stories, and rubric-scored gating (rubrics may be recorded, never gate — ADR 0025).
- Any v1 fix beyond the golden-blocking minimum. The target strategy is explicit: patches are instrument work, never v1 maintenance, and are never backported to `v1-freeze`.
- Runner-driven simulation of human acceptance (ADR 0020/0025: benchmark acceptance is the runner's terminal verdict).

## Decisions Delegated To Phase 1 By ADR 0025

Proposed here, ratified by this plan's approval:

1. **Runner location**: a new top-level directory `benchmark/` that is its own Go module. Black-box is then structurally enforced — the module cannot import Maestro internals without declaring a dependency on the main module, which review would catch immediately. Unlike `spikes/`, it is a maintained surface: wired into `make build`/`make test`/lint via explicit targets. v1's `pkg/benchmark` and `cmd/benchmark` are left untouched until their consumers die (inventory).
2. **Story definition directory and format**: `benchmark/stories/`, one TOML file per story (authoring-friendly, consistent with the repo's front-matter convention), validated against a versioned schema on load. JSON remains the substrate for everything the runner *emits* (run records, results store) per ADR 0021.
3. **Configuration directory**: `benchmark/configs/`, one MPH bundle per file, identified by content hash (ADR 0025's storage-transition rule).

## Deliverables And PR Sequence

One short-lived branch per item (`v2/phase_1/XXX`), one open at a time, per the build process. Sizes: S under a day of review-ready work, M a few days. ADRs are not expected this phase; new ADR needs discovered mid-phase go to the [backlog](../notes_adr-backlog.md).

| # | Branch suffix | Deliverable | Size |
|---|---|---|---|
| 0 | `scope-and-plan` | This document, Accepted. | S |
| 1 | `runner-skeleton` | The `benchmark/` module: story schema (format, loader, validation), MPH configuration bundles with content-hash identity, the append-only schema-versioned results store (JSONL), the normalized run-record contract with four-state metric semantics (ADR 0025 as amended), and the target-adapter interface. Unit-tested against a fake adapter; no real execution yet. | M |
| 2 | `fixtures` | Fixture repos stood up and pinned: forks of `maestro-llms`, `maestro-cms`, and the LLM-tester CLI app; pinned base branches; fixture conventions documented (golden-branch cleanup per ADR 0023). First 3 story definitions drafted against them (dependency bump, cleanup, focused bug fix) — not yet runnable. | S |
| 3 | `runner-core` | The execution engine: N-repeat orchestration, repeat isolation (fresh run-scoped checkout and branch namespace per unique run ID), declared budgets with overrun-abort recorded as failure kind `budget-overrun`, cleanup with loud invalid-run flagging. Integration-tested with a stub target. | M |
| 4 | `adapter-v1` | The v1-as-patched adapter: target invocation (config generation, CLI launch, shutdown), observation of what v1 exposes (SQLite, logs, branches, PRs), normalization into run records with declared capabilities and honest `unsupported` markings. Minimal prompt identity for v1: pack label `v1-embedded` + a content hash over a deterministic manifest of the embedded prompt/template inputs (`pkg/templates` and kin), so prompt identity moves only when prompt content moves; the target commit is recorded separately in the target descriptor, never conflated with the P dimension (ADRs 0021/0025: MPH identity derives from content). | M |
| 5 | `target-v1-patch` | The minimal patch set to the `main` factory path so story 1 passes end-to-end through the runner — discovered by running it, not guessed. Known candidates from v1's dying-defect list: the architect spec-review strictness defect (terse story prompts rejected) and the watchdog requeue race (#221) if it triggers. Every patch enumerated in a patch record in this directory, each justified against the Phase 1 target strategy. | M |
| 6 | `instrument-costs` | D9's agreed first action: instrumented runs on the first stories to establish real per-story costs; fix N (provisional 3/1) and per-run/per-suite budget caps as enforced runner values; the D9 policy record lands in this directory. | S |
| 7 | `reporting` | Comparison reports: per-metric-class aggregation over valid attempts (min/median/max numerics, pass rate, failure-kind counts, cost to accepted change with its undefined case), spread rendering, two-configuration side-by-side. | M |
| 8 | `baseline-single-agent` | The single-agent happy-path baseline: a second target adapter driving a headless coding agent (proposed: Claude Code non-interactive) through the same stories, checks, isolation, and budgets; its MPH configuration bundle. The vibe-coding comparator that prices the paired-agent premium (the economic argument). | M |
| 9 | `stories-suite` | Grow the suite to 5–10 stories across the ladder's low rungs (through "API change with tests" and "app change with behavioral evidence" on the LLM-tester fixture); define the `golden-minimal` (N=1, cheapest rungs) and `golden-all` tiers; make targets for both. | M |
| 10 | `baseline-report` | The v1-derived baseline on `golden-minimal`; the two-configuration comparison on the same story set; metrics upstream wishlist entries filed; the Phase 1 exit review against the checklist below. This document flips to `archive` on its merge. | S |

Sequencing notes:

- Items 1→3→4→5 are a strict dependency chain: the patch set (5) is discovered by executing a real story, which needs the engine (3) and the adapter (4). Items 2 and the story-authoring half of 9 are the designated slack — declarative, doc-like work that can proceed if a code review stalls, without violating the one-branch rule.
- Item 5 is the highest-variance item in the phase: nobody knows how many patches "minimal" is until story 1 runs. It is timeboxed by the enumerate-and-justify rule — any patch that cannot be justified in a sentence against the target strategy goes back to DR as a scope question rather than getting written.
- Item 6 deliberately precedes the report work and the suite build-out: budget caps must be real before the matrix grows (the benchmark-cost risk).
- Item 8 is exit-blocking (reviewer question 3, resolved). ADR 0025 makes the single-agent baseline mandatory from the start, and without it Phase 1 cannot measure the economic argument. If the phase runs long, schedule pressure reduces story count toward the 5-story exit floor or trims adapter capability (more honestly-marked `unsupported` metrics) — never the baseline.
- Testing follows the strategy doc's pattern: runner unit tests use fake adapters; real-execution tests cost tokens and sit behind the integration/golden build tags, never in `make test`.

## Exit Checklist

The roadmap's Phase 1 exit criteria, plus this plan's scope items that are not themselves roadmap criteria:

- [ ] The runner executes at least 5 single-repo golden stories against the v1-as-patched target, black-box, from a make target.
- [ ] Repeat runs produce a comparison report showing cost, time, and pass/fail spread (never bare points).
- [ ] Two different MPH configurations are compared on the same story set — the paired-agent default and the single-agent baseline.
- [ ] The D9 sampling and budget policy is written down with numbers fixed from instrumented runs, and enforced by the runner (declared budgets, overrun-as-failure).
- [ ] `golden-minimal` and `golden-all` tiers exist with make targets; `golden-minimal` has run at phase end (build process rule — this phase is where the rule activates).
- [ ] The v1-derived baseline on `golden-minimal` is recorded with its target descriptor (commit hash, MPH identity).
- [ ] The v1 patch record exists: every patch enumerated and justified against the target strategy; nothing backported to `v1-freeze`.
- [ ] Reusable metrics pieces are filed as upstream wishlist entries or explicitly found not to exist.

## Risks

- **The patch set becomes v1 maintenance.** The known v1 defects were declared dead with v1; item 5 resurrects only what blocks the instrument. Mitigation: the enumerate-and-justify rule, and the patch record as a reviewed artifact — growth in that file is visible, not silent.
- **Benchmark cost.** This is the first phase that spends real tokens on runs, and the matrix multiplies. Mitigation is structural per ADR 0025 — declared budgets, hard caps, overrun-as-failure — plus sequencing: caps are fixed (item 6) before the suite grows (item 9).
- **Nondeterminism makes pass/fail flaky.** Low-rung stories with deterministic checks are chosen first deliberately; spread reporting is the honest rendering of what remains. A story whose verdict flaps across repeats under one configuration is a defective story definition, and gets fixed or dropped rather than averaged.
- **Fixture drift.** External fixture repos are pinned by commit, and runs start from run-scoped checkouts; the fixture conventions doc (item 2) owns the re-pinning procedure. Fixture forks are never tracked against their upstreams automatically.
- **The single-agent adapter grows into a second product.** It exists to price the premium, not to be a good agent harness. Its scope is one honest happy path: same stories, same checks, same budgets, `unsupported` for everything it cannot report.
- **Review bottleneck (standing risk from Phase 0).** Serial PRs bound operator load; story authoring is the pressure-relief valve when a code review stalls.

## Reviewer Questions — Resolutions

Codex has answered (2026-07-15); DR confirmation rides on this document's approval.

1. **Runner module location**: top-level `benchmark/` as its own Go module (structurally enforced black-box), maintained surface wired into make. Codex concurs.
2. **Story definition format**: TOML for authored story definitions, JSON for everything the runner emits. Codex concurs.
3. **Single-agent baseline**: **in Phase 1 and exit-blocking.** The draft's descope-to-defer valve is withdrawn (Codex P1): ADR 0025 makes both configurations mandatory from the start, and substituting two v1-path configurations would leave Phase 1 unable to measure the economic argument. Schedule pressure reduces story count or adapter capability instead (see sequencing notes).
4. **Fixture hosting**: pinned forks under the SnapdragonPartners GitHub org; the v1 SWE-EVO local-Gitea mechanics stay salvage seeds for the later industry-benchmark adapter. Codex concurs.

## Related Documents

- [Roadmap](../plan_roadmap.md): Phase 1, the target strategy, D6, D9, the economic argument, benchmark-cost risk.
- [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) — the binding specification for this phase; [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) (MPH identity), [ADR 0023](../../adr/0023-v2-branch-strategy.md) (branch cleanup).
- [Port inventory](../phase_0/inventory_v1-port.md): `pkg/benchmark` rewrite disposition, breaking-change principles.
- [Build process](../process_build.md): roles, branching, suite-at-phase-end rule.
- [ADR backlog](../notes_adr-backlog.md): candidate 5 (prompt pack identity — Phase 3-blocking; Phase 1 uses the minimal label+hash form).
