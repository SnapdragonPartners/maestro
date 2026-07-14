+++
title = "ADR 0025: Golden Stories And The Benchmark Runner"
edit_date = "2026-07-13"
status = "draft"
summary = "Specifies the measuring instrument: golden story schema, the black-box runner contract and its self-contained results store, D9 sampling and budget mechanics, MPH configurations including the single-agent baseline, and the golden-minimal/golden-all suite tiers."
+++

# 0025. Golden Stories And The Benchmark Runner

Status: Proposed

## Context

The v2 sequencing principle is "build the measuring instrument first": golden stories, metrics, and run comparison must exist before the machine is rewritten, or every later model, prompt, and harness change is evaluated by anecdote. This ADR is Phase-1-blocking — Phase 1 implements exactly what it specifies. The instrument's defining hazard is that it measures a system scheduled for replacement, so its contract must survive the v1-to-v2 break intact.

## Decision

### Golden story schema

A golden story is a declarative, versioned fixture — a definition file in the Maestro repo (exact directory chosen in Phase 1) referencing a pinned external fixture repository. Each definition carries:

- Fixture repository and pinned starting commit.
- Input prompt, typed by level (Feature, Epic, or Story).
- Allowed files or expected affected areas.
- Expected validators (build, tests, lint) and required artifacts.
- Deterministic pass/fail checks — the primary verdict.
- Optional scored rubrics, recorded separately and never gating pass/fail in Phase 1.
- Expected evidence-package shape.
- Budget expectations (tokens, wall-clock).

The suite ladders in complexity (dependency bump → cleanup → focused bug fix → API change with tests → UI change with visual evidence → feature with migration → multi-Story Epic with merge conflicts → external service setup). The first 5–10 stories are single-repo and Story-scoped, drawn from the low rungs; fixture candidates are forked, pinned variants of `maestro-llms` and `maestro-cms`, with the UI-bearing fixture deferred until Product/Feature machinery and browser evidence exist. Fixture repos use pinned base branches, and the runner cleans golden branches after every run (ADR 0023's cleanup rule).

### Runner contract

- **Black-box.** The runner drives its target only through external surfaces — configuration, CLI/API invocation, and the resulting branches, PRs, artifacts, and metrics. It never imports Maestro internals. This is what lets one runner benchmark the v1-as-patched path today, v2 as it comes up, and harnesses that do not exist yet.
- **Target descriptor.** Every run records what it measured: target commit hash, binary/image identity, and the MPH identity of the configuration under test (model, prompt pack and hash, harness config hash, Maestro version) — aligning run records with ADR 0021's MPH signature. The initial target is the minimally patched v1 factory path (the decided Phase 1 target strategy); "v1-as-patched" is an honest, labeled baseline, never a repaired product.
- **Self-contained results store.** The runner owns its persistence: append-only, schema-versioned flat records (JSONL or equivalent), zero dependency on the Phase 2 data plane. The record shapes are designed for later import as `benchmark`-scoped artifacts — Phase 2's vertical slice does that import.

### Sampling and budgets (the D9 mechanism)

- Standard comparisons run **N = 3** per story per configuration; smoke runs are **N = 1**. Both numbers are provisional until the first instrumented runs establish real per-story costs (D9's agreed first action); the mechanism is not provisional.
- Spread-reported metrics are reported as min/median/max across repeats, never as bare points: tokens, cost, wall-clock, LLM calls, tool calls, iterations, review cycles, self-repair cycles, human interventions and attention time, pass rate, and failure kind. Cost to accepted change is the headline (D6).
- Budgets are declared, not discovered: every configuration declares expected per-run cost, and the suite run carries a per-run and per-suite cap. **Overrun aborts the run and records it with failure kind `budget-overrun`** — partial results are reported as partial; nothing silently truncates into a fake pass.
- Full-matrix runs (stories × models × packs × harness configs) require explicit justification — release comparisons and D6-grade questions; spot checks are the default posture.

### Configurations

A benchmark configuration is an MPH bundle: model routing, prompt pack reference, and harness settings. Two configurations are mandatory from the start: the **paired-agent default**, and the **single-agent happy-path baseline** — the vibe-coding comparator that quantifies the paired-agent premium and its payoff (the roadmap's economic argument, made measurable). Reviewer-model heterogeneity is part of the bundle, so homogeneous-review degradation (ADR 0020) is itself benchmarkable.

### Suite tiers and integration

Two tiers, extending the repo's existing build-tag pattern: **`golden-minimal`** — a small, cheapest-rung smoke subset at N = 1, runnable from a make target, executed at minimum at the end of every phase (build process) — and **`golden-all`** — the full suite at standard N, for release comparisons and D6 questions. Third-party benchmarks (the v1 SWE-EVO harness work is the seed) remain complementary for cross-system comparison and model-science questions; golden stories measure Maestro against itself.

## Consequences

- Phase 1 implements exactly this ADR; its exit criteria (five stories black-box, spread-reporting comparison, two configurations compared, D9 policy enforced by the runner) already match.
- The instrument survives the rewrite by construction: nothing in the runner changes when v1 code is deleted.
- The benchmark-cost risk is managed structurally — declared budgets, hard caps, overrun-as-failure — rather than by restraint.
- The single-agent baseline turns the economic argument into a number, and reviewer-heterogeneity bundling turns ADR 0020's degradation claim into a measurable one.
- Golden stories gate releases from Phase 9 onward; this ADR is where that gate's semantics are anchored.

## Related Documents

- [Roadmap](../v2/roadmap.md) pillar 1 (golden stories, runner constraints), D6, D9, the Phase 1 target strategy, and the economic argument; [Phase 0 plan](../v2/phase_0/scope-and-plan.md) item 7 and Phase 1 exit criteria; [build-process](../v2/build-process.md) (suite at phase end, build tags).
- [ADR 0021](0021-artifacts-and-principal-instances.md) (MPH signature, benchmark scope), [ADR 0022](0022-v2-data-plane.md) (later import), [ADR 0023](0023-v2-branch-strategy.md) (fixture branch cleanup), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (heterogeneity as a measurable).
- [ADR backlog](../v2/adr-backlog.md) Golden Stories And Benchmark Runner entry (this ADR answers its key questions).
