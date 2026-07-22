+++
title = "ADR 0025: Golden Stories And The Benchmark Runner"
edit_date = "2026-07-22"
status = "live"
summary = "Specifies the golden story instrument: story schema, the black-box runner contract and its self-contained results store, D9 sampling and budget mechanics, MPH configurations including the single-agent baseline, and the golden-minimal/golden-all suite tiers. A conformance-first sequencing amendment is PROPOSED 2026-07-22 (pending acceptance): proving the pipeline completes progressively harder stories comes first; economic baselining defers to Phase 1B after Phase 7."
+++

# 0025. Golden Stories And The Benchmark Runner

Status: Accepted (Codex + DR, 2026-07-13); amended 2026-07-16 (Phase 1 item 1 review, Codex + DR): metric semantics extended from tri-state to **four-state** — `unavailable` (the target supports the metric but it could not be collected on this attempt) added alongside value/unsupported/not_applicable, so a crashed target still produces a valid *failed* record instead of lying `unsupported` or failing validation. Amended again 2026-07-16 (item 3 review, Codex + DR): **budget enforcement modes** — the overrun-aborts rule is satisfied natively by targets that stream usage (engine-cancelled at the cap) or self-enforce declared caps; targets that can only report usage post-hoc are permitted as *degraded enforcement*, declared as `budget_enforcement` in every run record. Wall-clock caps are engine-enforced for every mode. Cost metrics remain comparable across modes (costs are real costs), but budget-overrun *rates* are comparable only within one enforcement mode. **Amendment PROPOSED 2026-07-22** (Phase 1 item 6 retrospective; DR-directed, pending Codex + DR acceptance — this line flips to `Amended … (Codex + DR)` in the approval commit): **conformance-first sequencing** — the instrument's near-term primary function is proving the pipeline completes progressively harder stories, repeatably; economic baselining defers to **Phase 1B** after Phase 7. No deliverable is cancelled and sequence/emphasis move; the one mechanic that does change is the tier/N binding (a tier names a story set; N follows purpose), amended explicitly. See *Conformance-first sequencing* below.

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
- Optional scored rubrics, recorded separately and never gating pass/fail in Phase 1. Model-scored rubrics carry evaluator provenance — evaluator model, prompt, and rubric version — so evaluator drift is separable from target performance in later comparisons.
- Expected evidence-package shape.
- Budget expectations (tokens, wall-clock).

The suite ladders in complexity (dependency bump → cleanup → focused bug fix → API change with tests → app change with behavioral evidence → feature with migration → multi-Story Epic with merge conflicts → external service setup). The roadmap's "UI-bearing" requirement is refined here: **the point is a full, human-exercisable application rather than a bare library** — not necessarily a web UI. The first 5–10 stories are single-repo and Story-scoped, drawn from the low rungs; fixture candidates are forked, pinned variants of `maestro-llms` and `maestro-cms`, with the standalone LLM-tester CLI app from the toolkit repos as the starting app-bearing fixture (no browser tooling required), and `maestro-issues` — the automated bug fixer, a simple true-webui app in the Maestro universe — as the candidate when browser evidence arrives. Stories requiring visual/browser evidence remain deferred until that tooling exists. Fixture repos use pinned base branches, and the runner cleans golden branches after every run (ADR 0023's cleanup rule).

### Runner contract

- **Black-box.** The runner drives its target only through external surfaces — configuration, CLI/API invocation, and the resulting branches, PRs, artifacts, and metrics. It never imports Maestro internals. This is what lets one runner benchmark the v1-as-patched path today, v2 as it comes up, and harnesses that do not exist yet.
- **Target descriptor.** Every run records what it measured: target commit hash, binary/image identity, and the MPH identity of the configuration under test (model, prompt pack and hash, harness config hash, Maestro version) — aligning run records with ADR 0021's MPH signature. The initial target is the minimally patched v1 factory path (the decided Phase 1 target strategy); "v1-as-patched" is an honest, labeled baseline, never a repaired product.
- **Self-contained results store.** The runner owns its persistence: append-only, schema-versioned flat records (JSONL or equivalent), zero dependency on the Phase 2 data plane. The record shapes are designed for later import as `benchmark`-scoped artifacts — Phase 2's vertical slice does that import.
- **Normalized run-record contract.** Black-box is not enough: v1-as-patched exposes logs, SQLite, and PRs; v2 exposes artifacts and data-plane records. Per-target **adapters** normalize observations into one stable, versioned run-record contract carrying: the adapter identity and version, the target's declared capabilities, raw evidence pointers into whatever the target exposes, and normalized metrics with **four-state semantics** (as amended 2026-07-16) — a value, `unsupported` (this target cannot report it), `not_applicable` (this story does not exercise it), or `unavailable` (the target supports it but it could not be collected on this attempt — e.g. the target crashed). Missing is never zero; comparisons across targets are honest by construction.
- **Benchmark acceptance is the runner's terminal verdict**, defined identically for every target: deterministic checks pass, required validators, artifacts, and evidence shapes are present, and the expected branch/PR terminal state is reached. "Cost to accepted change" in benchmark context means cost to that verdict. It deliberately does not simulate human acceptance (ADR 0020's outcome validation is not benchmarkable) — which is exactly what keeps the headline metric's meaning stable across v1-as-patched, v2, and future targets.
- **Repeat isolation.** Every repeat starts from a fresh, run-scoped checkout and branch namespace derived from the pinned commit, keyed by a unique run ID; no repeat may inherit state from another, or the spread is meaningless. Cleanup failures are recorded loudly — a run whose isolation cannot be verified is flagged invalid, never silently included in comparisons.

### Sampling and budgets (the D9 mechanism)

- Standard comparisons run **N = 3** per story per configuration; smoke runs are **N = 1**. Both numbers are provisional until the first instrumented runs establish real per-story costs (D9's agreed first action); the mechanism is not provisional.
- Aggregation semantics are defined per metric class, over the cohort of **valid attempts** (invalid runs — failed isolation or unverifiable cleanup — are excluded from every aggregation and counted separately; budget-overrun aborts are valid *failed* attempts whose costs count):
  - **Numeric per-run metrics** — tokens, cost, wall-clock, LLM calls, tool calls, iterations, review cycles, self-repair cycles, human interventions and attention time — report as min/median/max across valid attempts, never bare points.
  - **Pass rate** — accepted verdicts over valid attempts.
  - **Failure kinds** — counts per kind across valid attempts.
  - **Cost to accepted change** (the headline, D6) — total cost of all valid attempts divided by the number of accepted verdicts, so failed-attempt costs are included: that is what the factory actually spends per accepted change. Undefined when no attempt passes — reported as undefined, never as zero or infinity.
- Budgets are declared, not discovered: every configuration declares expected per-run cost, and the suite run carries a per-run and per-suite cap. **Overrun aborts the run and records it with failure kind `budget-overrun`** — partial results are reported as partial; nothing silently truncates into a fake pass. (Amended 2026-07-16: abort-at-the-cap is native for `streamed` and `self-enforced` targets; `post-hoc` targets are degraded enforcement, declared per record — wall-clock stays hard for all modes, and overrun rates are comparable only within one mode.)
- Full-matrix runs (stories × models × packs × harness configs) require explicit justification — release comparisons and D6-grade questions; spot checks are the default posture.

### Configurations

A benchmark configuration is an MPH bundle: model routing, prompt pack reference, and harness settings. Two configurations are required: the **paired-agent default**, and the **single-agent happy-path baseline** — the vibe-coding comparator that quantifies the paired-agent premium and its payoff (the roadmap's economic argument, made measurable). *(Retimed by the 2026-07-22 proposed amendment: the paired-agent default is required from the start; the single-agent baseline as an economic comparator is required at Phase 1B. Phase 1 uses only its cheap half — the achievability check below. "Mandatory from the start" as originally written is superseded for the baseline.)* Reviewer-model heterogeneity is part of the bundle, so homogeneous-review degradation (ADR 0020) is itself benchmarkable.

Configuration storage follows the same trajectory as results: **file-based in Phase 1** (no data plane exists yet), **data-plane-resident once Phase 2 lands** (durability; prompt packs are DB-canonical per roadmap pillar 10), with the runner accepting either through a source switch. Either way a configuration is identified by content hash — the MPH identity in run records derives from content, never location, so results remain comparable across the storage transition.

### Suite tiers and integration

Two tiers, extending the repo's existing build-tag pattern: **`golden-minimal`** — a small, cheapest-rung smoke subset at N = 1, runnable from a make target, executed at minimum at the end of every phase (build process) — and **`golden-all`** — the full suite, for release comparisons and D6 questions. *(Amended by the 2026-07-22 proposal: a tier names a **story set**, not a repeat count. N is set by the run's purpose — see the cadence table below — so `golden-all` is N = 1 for conformance and N = 3 for comparison. The original "at standard N" wording conflated the two.)* Third-party benchmarks (the v1 SWE-EVO harness work is the seed) remain complementary for cross-system comparison and model-science questions; golden stories measure Maestro against itself. Beyond MVP, the same runner — its adapter and normalized-record contract in particular — is designed to eventually drive industry benchmarks with less deterministic outcomes, continuing the v1 benchmark support work on this harness rather than a separate one.

### Conformance-first sequencing (PROPOSED 2026-07-22, pending acceptance)

This ADR was written on a buried assumption: **that measurement presupposes function.** "Build the measuring instrument first" quietly assumes the machine runs and what you want is its *economics*. Phase 1's instrumented runs falsified that assumption for the current target — **7 of the 11 enumerated v1 patches were run-blocking** (v1 could not complete a run without them), surfaced by exercising only four stories, and the one story requiring a genuine multi-site refactor never completed at all. An instrument pointed at a machine that does not reliably run produces diagnosis, not economics.

The near-term primary function is therefore **conformance**: proving the pipeline completes progressively harder stories, and re-proving it as the system changes. Economics remains the eventual purpose and is deferred, not abandoned.

**What does not change — with one stated exception.** The story schema, the black-box runner contract, verdicts and failure kinds, repeat isolation, declared budgets with overrun-as-failure, MPH identity, and the results store all stand unaltered. **The exception is the tier/N binding**, which this amendment does change and says so explicitly below: a tier names a story set, and N follows the run's purpose rather than the tier. That is a policy amendment to *Suite tiers* and to the accepted D9 record, not a clarification, and it requires acceptance on those terms. Conformance was always latent here — the story *ladder*, the terminal-verdict definition, and the phase-end `golden-minimal` cadence are already in this ADR. This amendment re-weights what the instrument is *for* first; it adds no capability and cancels no deliverable.

**Phase 1B: Benchmark Economics**, anchored **after Phase 7**. Before then the v2 work is largely infrastructure — data plane, hierarchy, branches, runtime — so a baseline taken earlier prices scaffolding rather than the system. Phase 1B carries: per-metric-class comparison reporting with spread, the **single-agent happy-path baseline** as an economic comparator, and the cost-to-accepted-change baseline. The "two configurations mandatory from the start" requirement in *Configurations* is retimed to Phase 1B accordingly; the paired-agent default remains mandatory now.

**Cost data keeps accruing regardless.** Conformance runs already record `cost_usd`, `tokens_total`, and call counts on every attempt. Those records are retained from now on, so a cost trend accumulates across phase-end runs at zero additional effort — only the *analysis* defers. A structurally expensive v2 shows up in the raw numbers well before Phase 1B rather than being discovered at the end.

**Cadence, repeats, and budget** — stated exactly, because "phase end" and N were previously ambiguous:

| When | Tier | N | Configuration | Purpose |
|---|---|---|---|---|
| End of **every** phase (unchanged, *Suite tiers* above) | `golden-minimal` | 1 | `paired-default` | the harness-is-alive smoke check |
| End of every phase, **from Phase 2 onward** | `golden-all` | 1 | `paired-default` | the conformance proof — every rung exercised once |
| Phase 1B only | `golden-all` | 3 | both configurations | D9 comparison sampling |

**This is a policy amendment, not a clarification, and is proposed as such.** As accepted, the *Suite tiers* text binds `golden-all` to "standard N" and the [D9 policy](../v2/phase_1/d9_budget_policy.md) sets standard N = 3 for `paired-default`; running `golden-all` at N = 1 therefore changes both, and acceptance of this amendment is required to do so.

The substance: **a tier names a story set; N is a property of the run's purpose.** N = 3 exists to characterize a *distribution* for comparison. Conformance asks a different question — did each rung still behave — for which one pass per story is the honest unit, and three passes buy precision nobody reads. Under this amendment D9's N = 3 governs comparison runs (Phase 1B) and N = 1 governs phase-end conformance; the D9 record is amended in step. The ~**$50 per phase** budget covers the `golden-all` N = 1 conformance run — `golden-minimal` is a few dollars and is not what the $50 is for.

Each conformance run is retained as a **committed distilled record** (see below): a proof that leaves no trace cannot distinguish a regression from a memory.

**Durable retention.** The raw results store (`benchmark/runs/`) is git-ignored and reproducible-by-rerun, so it cannot by itself carry a longitudinal claim — exactly how earlier run evidence was lost to a power failure. Each phase-end conformance run therefore appends a **distilled, committed record**: date, target descriptor, per-story verdict, and cost/token/call totals. This is explicitly interim: once the Phase 2 data plane lands, performance records become first-class artifacts there (ADR 0022), and the committed file retires rather than becoming permanent scaffolding.

**Red rungs are legitimate.** A story that a competent single agent can complete but the pipeline cannot is a **progress marker**, not a suite defect — it measures distance to capability. What a red rung must never be is ambiguous between "the pipeline cannot do this yet" and "this story is unreasonable." Hence the achievability check below.

**Single-agent achievability check** — a cheap, scripted headless-agent pass over a candidate story, answering only *is this story completable at all*. It is a **low-rung triage tool with a designed expiry**: stories from the decomposition rungs upward exist precisely to test multi-story coordination a single autonomous agent cannot do, so the check is invalid there by construction and is retired rather than left to emit false "unachievable" verdicts.

**`paired-local` is experimental.** It is a valuable experiment — cheap tight loops and small-model capability learning — but never a pass requirement at any tier, including v2 MVP. A story must clear `paired-default` before `paired-local` is attempted on it, and no story is required to go green locally.

**Target-patch appetite is context-dependent.** The bar for patching the benchmarked target was necessarily *higher* when no story completed at all — a large patch bought the first signal in existence. Now that runs mostly complete, it tightens: patch when the fix is small (order of ten lines, within a couple of functions), otherwise record the diagnosis and park the rung as blocked. Crucially, **scope the decision by the shape that makes the patch sound, not by its first diff** — P-11 began as ~15 lines in one function and was only correct once every writer to the resource took the lock (three packages, seven sites). A fix that requires a resource-wide audit to be correct is a deliberate defer-or-commit call at the moment of discovery, not a drift.

## Consequences

- Phase 1 implements exactly this ADR. Its exit criteria split with the 2026-07-22 amendment: **Phase 1 proper** keeps five stories black-box and the D9 policy enforced by the runner; **Phase 1B** (after Phase 7) carries spread-reporting comparison and the two-configuration comparison. The split is scheduling — both halves are still built.
- The instrument survives the rewrite by construction: nothing in the runner changes when v1 code is deleted.
- The benchmark-cost risk is managed structurally — declared budgets, hard caps, overrun-as-failure — rather than by restraint.
- The single-agent baseline turns the economic argument into a number, and reviewer-heterogeneity bundling turns ADR 0020's degradation claim into a measurable one — both realized in Phase 1B. In Phase 1 the single agent serves a different, cheaper role: an achievability control that keeps a red rung from being ambiguous between an incapable pipeline and an unreasonable story.
- Golden stories gate releases from Phase 9 onward; this ADR is where that gate's semantics are anchored.

## Related Documents

- [Roadmap](../v2/plan_roadmap.md) pillar 1 (golden stories, runner constraints), D6, D9, the Phase 1 target strategy, and the economic argument; [Phase 0 plan](../v2/phase_0/plan_scope.md) item 7 and Phase 1 exit criteria; [build-process](../v2/process_build.md) (suite at phase end, build tags).
- [ADR 0021](0021-artifacts-and-principal-instances.md) (MPH signature, benchmark scope), [ADR 0022](0022-v2-data-plane.md) (later import), [ADR 0023](0023-v2-branch-strategy.md) (fixture branch cleanup), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (heterogeneity as a measurable).
- [ADR backlog](../v2/notes_adr-backlog.md) Golden Stories And Benchmark Runner entry (this ADR answers its key questions).
