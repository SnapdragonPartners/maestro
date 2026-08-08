+++
title = "Conformance Run Log"
edit_date = "2026-08-08"
status = "live"
type = "notes"
summary = "The committed, distilled record of every phase-end golden-story conformance run: date, target identity, per-story verdict, and cost/token totals. The durable counterpart to the git-ignored raw results store — interim until the Phase 2 data plane makes performance records first-class artifacts."
+++

# Conformance Run Log

Status: live — introduced by the conformance-first amendment to [ADR 0025](../adr/0025-golden-stories-and-benchmark-runner.md), Accepted (Codex + DR, 2026-07-22).

## Why this file exists

The runner's raw results store (`benchmark/runs/`) is **git-ignored and reproducible-by-rerun**: it holds agent transcripts and per-attempt SQLite, which do not belong in version control. That is the right call for the raw evidence and the wrong basis for a longitudinal claim — a trend that lives only in an ignored directory can vanish, and did: a power failure destroyed the scratchpad evidence for two accepted runs during Phase 1 item 6, leaving only figures that happened to be quoted in review.

So each phase-end conformance run appends a **distilled, committed row** here: enough to establish a trend and to detect a regression, not enough to leak transcripts.

**This file is interim by design.** Once the Phase 2 data plane lands, performance records become first-class artifacts there ([ADR 0022](../adr/0022-v2-data-plane.md)) — the proper long-term home for all artifacts, this one included. When that import exists, this file retires rather than becoming permanent scaffolding.

## What to record

Per run: date, phase checkpoint, tier and N, configuration, **target descriptor** (commit hash + binary identity), and per story the verdict plus tokens/cost/wall-clock. Identity matters as much as the numbers — two rows are only comparable when their target descriptor and MPH identity match ([Run Protocol](../../benchmark/README.md), *Comparability*).

Procedure for producing a run is the Run Protocol; this file records only its distilled outcome.

## Runs

### 2026-07-21 — Phase 1 item 6 measurement campaign (pre-cadence; **NOT a baseline**)

Not a phase-end conformance run and **not the v1 baseline** Phase 1 owes. Recorded because it is the only measured v1-as-patched cost data and would otherwise live solely in the item-6 record.

**These rows do not meet this log's own identity bar.** They carry *descriptive* identity ("post-P-9/P-10") rather than the required commit hash, binary identity, and MPH identity, and those descriptors cannot now be reconstructed — the raw records for two attempts were destroyed by the power failure described above. The attempts also span three target identities, none of them the settled one, and several lack token/wall/call values. They are evidence of magnitude only. Full derivation and caveats: [d9_budget_policy.md](phase_1/d9_budget_policy.md).

**Every row from here on must carry the full target descriptor**; a row that cannot is not admissible as a trend point.

| Story | Verdict | Tokens | Cost | Wall | Target identity |
|---|---|---|---|---|---|
| `smoke-comment` | accepted | 426k | $1.97 | 750s | pre-re-pin, pre-cache-image, pre-P-9/P-10/P-11 |
| `dep-bump-xnet` | accepted | 320k | $1.81 | 277s | as above |
| `smoke-comment` | accepted | — | $0.89 | — | post-re-pin + cache-image, pre-P-9/P-10/P-11 |
| `dep-bump-xnet` | accepted | — | $2.09 | — | post-P-9/P-10, pre-P-11 |
| `bugfix-openai-stopreason` | accepted | 727k | $7.06 | 418s | post-P-9/P-10, pre-P-11 |
| `cleanup-provider-options` | never completed | 2.01M / 2.26M | $9.37 / $13.11 | — | two attempts; parked for over-decomposition |

Configuration `paired-default` (frontier) throughout. **These attempts span three target identities and none used the final one** — they are the basis for the D9 caps, not a comparable series. Campaign cost ~$41.

### 2026-07-22 — v1-as-patched baseline on `golden-minimal` (N=3)

**The v1 baseline owed by Phase 1.** The last one that can be taken: v1's factory path is deleted during the rewrite, so this obligation expired rather than deferred. Tier `golden-minimal` (the two cheapest rungs), N=3, configuration `paired-default` (frontier). **6/6 accepted**, total $9.66.

| Story | Rep | Verdict | Tokens | Cost | Wall | Calls | Commit | Binary |
|---|---|---|---|---|---|---|---|---|
| `smoke-comment` | r1 | accepted | 141,376 | $0.95 | 178s | 26 | `387b8bd64ee8` | `cd3b413034f6` |
| `smoke-comment` | r2 | accepted | 167,605 | $0.95 | 216s | 30 | `387b8bd64ee8` | `cd3b413034f6` |
| `smoke-comment` | r3 | accepted | 201,869 | $1.30 | 341s | 34 | `e0323edecc89` | `816477ad9ab4` |
| `dep-bump-xnet` | r1 | accepted | 312,246 | $2.00 | 307s | 41 | `e0323edecc89` | `816477ad9ab4` |
| `dep-bump-xnet` | r2 | accepted | 370,910 | $2.39 | 320s | 46 | `4990a1e8f92b` | `8c8ce7642fa0` |
| `dep-bump-xnet` | r3 | accepted | 342,568 | $2.08 | 275s | 42 | `4990a1e8f92b` | `8c8ce7642fa0` |

Aggregates (min / median / max over valid attempts, per [ADR 0025](../adr/0025-golden-stories-and-benchmark-runner.md) — never bare points):

| Story | Tokens | Cost | Wall clock |
|---|---|---|---|
| `smoke-comment` | 141,376 / 167,605 / 201,869 | $0.95 / $0.95 / $1.30 | 179 / 217 / 341s |
| `dep-bump-xnet` | 312,246 / 342,568 / 370,910 | $2.00 / $2.08 / $2.39 | 275 / 308 / 320s |

Uniform across all six attempts: story hashes `smoke-comment` `sha256:75495b46c1a2` and `dep-bump-xnet` `sha256:6b5141b820bb`; config `paired-default` `sha256:3d999b22fbbb`; adapter `v1-as-patched` 0.1.0; enforcement `streamed`; MPH prompt pack `v1-embedded`, prompt hash `sha256:410ab96e5627…`, harness hash `sha256:6cfd2372be07…`; model routing architect `claude-opus-4-1`, coder/PM `claude-sonnet-4-6`.

#### Caveat — the target descriptor is not uniform (recorded, not hidden)

The six attempts span **three `binary_identity` values and three commit hashes**, as the table shows. Cause: the pre-commit hook runs `make build`, so documentation commits landed *while the run was in flight* rebuilt `bin/maestro`, and Go builds are not byte-reproducible.

**The code was provably identical throughout** — `git diff --name-only 387b8bd 4990a1e` returns documentation only, no Go files — so all six attempts exercised the same target behaviour and the numbers are substantively sound. But by the comparability rule in the [Run Protocol](../../benchmark/README.md), a comparable series shares one descriptor, and this one does not. It is therefore recorded as **the v1 baseline with a stated identity caveat**, not as a clean single-identity series. DR accepted this trade rather than spend a second ~$10 re-running; the alternative was a clean re-run, and the reason it was not worth it is that v1 is being deleted regardless.

The Run Protocol now carries a preflight warning so this cannot recur: do not commit while a run is in flight.

### 2026-07-22 — item 9 story batch (pre-fix pin; **superseded**)

Not a phase-end conformance run: the first execution of item 9's three new stories, kept because the reds are informative and the run is what surfaced a fixture defect. **All three ran against fixture pin `6ed67444e955`, which has since been superseded by `60e79fd075c8`** — so these rows are not comparable to anything taken after, and are recorded as history rather than as trend points.

Target identity is uniform across all three (commit `5ef8443232de`, binary `e7427da43e0c`, config `paired-default`), so they are comparable *to each other*.

| Story | Verdict | Tokens | Cost | Wall | Failed checks |
|---|---|---|---|---|---|
| `flag-chat-timeout` (story since **replaced**) | failed / checks-failed | 622,891 | $3.90 | 353s | diff-confined-to-source |
| `api-option-lookup` | failed / branch-state | 563,892 | $4.13 | 529s | diff-confined-to-source, tests-cover-four-cases |
| `app-healthz-endpoint` | failed / checks-failed | 691,376 | $3.64 | 611s | diff-confined-to-source |

Total $11.67. **Zero validator failures across all three** — every `build`, `vet` and `test` passed.

**Read the reds carefully: they are authoring defects, not capability limits.**

- `diff-confined-to-source` failed on all three for one shared cause — `golden-fixture-chat`, a compiled binary committed into the fixture by mistake in the item-2 extraction. `go build` regenerated it, so the diff always carried a path no agent touched, making the check **unsatisfiable** for any chat-fixture story that builds. Fixed additively in the fixture (`60e79fd`) and all four chat stories re-pinned.
- `tests-cover-four-cases` failed because the check counted `func Test` and `t.Run(` occurrences, which scores *any* table-driven test at 2. The pipeline had written a table with **nine** named cases covering all four required behaviours plus edge cases the story never asked for. The check now counts results reported by `go test -v` instead.

**What this does and does not show.** All three official verdicts are **failures**, the fixture pin is superseded, and the engine-owned behavioural oracles added later were **never run against these solutions** — the acceptance contracts in force at the time were structural greps since shown to accept implementations that ignore the requirement entirely. So these are **promising validator-passing candidates, not proof that the pipeline succeeded** on the three new paths. What can be said: every `build`, `vet` and `test` passed, and the recorded failures trace to author defects rather than to anything the target did.

`flag-chat-timeout` has since been **replaced** by `flag-instance-name`: its behaviour could not be verified hermetically, so every check it had was structural and an implementation that ignored the flag passed all of them. Establishing the stronger claim needs a re-run on current pins against the current contracts, which is item 10's phase-end `golden-all`. Not re-run at the time: re-pinning had already churned the hashes, and paying frontier prices to re-confirm known authoring bugs was poor use of budget.

### 2026-07-23 — Item 9-oracle achievability control (**NOT a performance run**)

This records the achievability control that closed item 9-oracle (engine-owned
behavioural oracles), per the durability rule above — accepted evidence must be
committed, not left in transient output. It is deliberately **non-performance**:
a single headless agent per story with **no cost accounting** (a control, not a
measurement, so no tokens/cost/wall-clock row). It establishes that the three
rung-3/4/5 contracts are achievable **and that the engine-owned oracles function
against real agent solutions** — not a baseline. The performance baseline remains
item 10's phase-end `golden-all`.

- **Executor:** `claude` CLI 2.1.218 headless (`-p`, `--permission-mode acceptEdits`), fresh clone detached at the pin, solution committed, then evaluated by `bin/runner verify` — the engine's single production `Verify` seam (`scripts/achievability-check.sh`). No facsimile of check execution.
- **Fixture pin:** `golden-fixture-chat` @ `60e79fd075c8`.
- **Prompt fidelity:** the agent receives `prompt.text` **verbatim** — the same bytes `target/v1target/run.go` writes to `story-spec.md`. The scope constraint each story needs lives inside `prompt.text` (so the control and the measured pipeline read identical bytes); the control does not augment the prompt. The hashes below are the committed prompts, byte-identical to what these runs received.

| Story | Story hash | Verdict | Behavioural oracle(s) — all PASS |
|---|---|---|---|
| `flag-instance-name` | `sha256:2730eace3d79` | proven-achievable | `oracle-instance-name-observed` (real binary + fake Ollama, header on 3 handlers) |
| `api-option-lookup` | `sha256:b33ec4422e48` | proven-achievable | `oracle-lookup-semantics` (in-solution) + `authored-tests-cover-each-behaviour` (scratch mutation, agent's own tests kill all 4 reference mutants) |
| `app-healthz-endpoint` | `sha256:6b23d0a8105b` | proven-achievable | `oracle-healthz-responds` (httptest through `newServer`) + `authored-tests-detect-broken-behaviour` (scratch mutation, agent's own test detects both broken clauses) |

Every validator and check passed for all three (`build`, `vet`, `test`, diff-confinement, the structural greps, gofmt, and the oracles above). The scratch-mode mutation oracles passing against the agents' **own authored tests** is the load-bearing result: the full engine oracle path — in-solution materialisation, scratch worktree checkout of the immutable solution commit, mutation, compile-gate, cleanup — works end-to-end on real agent output.

**What this does and does not show.** It shows the contracts are achievable and the oracles are sound and functional against genuine solutions. It is **not** a pipeline verdict or a performance baseline: the executor is a single headless agent, not the PM→architect→coder pipeline, and nothing here is cost-accounted. `not-proven-achievable` from this control would bound our knowledge, never the story (a documented limitation). The official verdicts come from item 10's `golden-all`.

### 2026-07-23 — Phase 1 exit: `golden-all` conformance run (N=1)

**The phase-end `golden-all` run item 10 owes** — the first one, and the first time the engine-owned oracles run inside the real paired pipeline (not the single-agent control). Full active suite (6 stories), N=1, `paired-default`. **Clean single-identity series:** one target descriptor across all six attempts (the don't-commit-mid-run discipline held, unlike the 2026-07-22 baseline's three identities). Purpose is conformance — *did each rung still behave against current definitions + oracles* — not a performance baseline. **5/6 accepted, $26.40.**

Target descriptor (uniform, all six):

- adapter `v1-as-patched` 0.1.0; commit `75aec6e58c53`; binary `sha256:8a477bb494f3…` (`maestro dev`); enforcement **streamed** (P-1 usage-surface v1).
- config `paired-default` `sha256:3d999b22fbbb`; MPH model routing architect `claude-opus-4-1`, coder/PM `claude-sonnet-4-6`; prompt pack `v1-embedded`, prompt hash `sha256:410ab96e5627…`, harness hash `sha256:6cfd2372be07…`, maestro `dev`.

| Story | Story hash | Verdict | Tokens | Cost | Wall | Calls |
|---|---|---|---|---|---|---|
| `smoke-comment` | `sha256:75495b46c1a2` | accepted | 162,621 | $1.00 | 181s | 30 |
| `dep-bump-xnet` | `sha256:6b5141b820bb` | accepted | 331,018 | $2.07 | 270s | 42 |
| `bugfix-openai-stopreason` | `sha256:909bf81ad2ac` | accepted | 1,545,106 | $13.41 | 811s | 127 |
| `flag-instance-name` | `sha256:2730eace3d79` | accepted | 846,126 | $4.22 | 546s | 89 |
| `app-healthz-endpoint` | `sha256:6b23d0a8105b` | accepted | 446,478 | $2.41 | 441s | 62 |
| `api-option-lookup` | `sha256:b33ec4422e48` | **failed / branch-state** | 489,511 | $3.28 | 458s | 60 |

Total **$26.40**. Every attempt was healthy — no fatal shutdown or abandonment in any of the six; the watchdog post-`done` requeue (#221) fired benignly throughout (harmlessly failing on already-terminal stories).

**The engine-owned oracles ran and PASSED against real target-produced solutions** — the first end-to-end exercise of the oracle machinery in the full pipeline (previously only via the single-agent `runner verify` control). `flag-instance-name`'s `oracle-instance-name-observed`, `app-healthz-endpoint`'s `oracle-healthz-responds` **and** its scratch-mode `authored-tests-detect-broken-behaviour`, and even `api-option-lookup`'s `oracle-lookup-semantics` **and** scratch-mode `authored-tests-cover-each-behaviour` all passed. So all three new stories are now proven achievable by **accepted pipeline runs** (the stronger evidence), retiring the single-agent caveat — with one asterisk on `api-option-lookup`, whose solution here is also in fact correct (see below).

**The one red is a harness false-negative, not a target/story/oracle defect.** `api-option-lookup`'s solution is correct and merged — every validator, every check, and *both* oracles passed. The verdict is `branch-state` because the architect split the story into two internal stories; the second ("add tests") completed `done` with an **empty PR/commit** (its work already done by the first story's PR), and the v1 adapter's per-Story PR accounting (`prsSatisfied`, empty pr_id rejected) fails on it → engine `!solutionOK` → `branch-state`. Over-decomposition itself is a notice, not an error (we grade the final merged result); the empty-PR/commit completion is the real defect, filed as **[maestro#280](https://github.com/SnapdragonPartners/maestro/issues/280)** for a future fix. It recurred from the 2026-07-22 run and is rooted in **ADR backlog #15** (unreviewed Architect decomposition, ADR 0020 non-author-review gap). Per the exit criteria, a red that clears achievability is a progress marker, not a suite defect — and this one clears it twice over (proven-achievable by the control **and** its pipeline solution is actually correct).

**Cost note (recorded, not smoothed):** `bugfix-openai-stopreason` ran ~2× its earlier single point ($13.41 / 1.55M vs $7.06 / 727k) — well within its 2.5M / $24 cap; an N=1 conformance point, so nondeterminism shows as a bare value rather than a distribution.

*Phase 1 exit review pending on this run; the v2-derived baseline and two-configuration comparison remain Phase 1B.*

### 2026-08-08 — Phase 2 exit: `golden-all` **not run** (DR override); partial run recorded

**No phase-end `golden-all` run exists for Phase 2.** DR explicitly overrode it after the instrument was found broken by causes outside this phase's work. This row exists because the log's purpose is to make an absent proof visible rather than silent — the override is recorded here as the reason, not as a gap to be discovered later.

**What broke it.** `claude-opus-4-1`, the architect model in `paired-default` for every prior run in this log, was **deprecated on 2026-06-05 and retired on 2026-08-05**, and now returns `404 not_found_error`. The first attempt at the phase-exit run died in ~8s per story at the architect's first LLM call, both stories `target-error`, **$0.00 spent** — no model, no run.

The deprecation was published with ≥60 days' notice, **seven weeks before this phase's plan was approved**. It was catchable and nobody caught it: nothing in the Run Protocol or the phase plan checks pinned model IDs against their published lifecycle. It is not a Phase 2 regression — but note that Phase 2 *did* change the agent path (item 9 moved the usage surface v1 → v2 and the adapter 0.1.0 → 0.2.0), so "Phase 2 didn't touch it" would be false. The correct statement is narrower: **the blocker was unrelated to Phase 2's work, and Phase 2's agent-path changes remain unverified.**

**What was attempted.** The architect seat was moved to `gpt-4.1`. That also makes the pairing **cross-lineage**. This sits under [ADR 0020](../adr/0020-review-invariant-reviewer-vs-partner.md)'s reviewer-heterogeneity norm rather than being new policy, but note the status carefully: **0020 as written says "distinct model"**, and by that operative text `claude-opus-4-1` reviewing `claude-sonnet-4-6` is heterogeneous and conforming. DR proposed on 2026-08-08 that lineage — the originating lab — is the right unit, with same-lineage review remaining valid but visibly marked **degraded, not invalid**. That is [backlog candidate 16](notes_adr-backlog.md), **pending amendment**; it is not yet the operative rule.

**Conditional consequence, recorded rather than left to be rediscovered:** `paired-default` has paired Opus with Sonnet since its first commit (`0649f51`, 2026-07-17). **Under the proposed lineage clarification, every prior `paired-default` paired-agent run in this log would be classified as degraded** — not the single-agent rows, which are a different configuration entirely (the 2026-07-23 achievability control is one headless agent, with no reviewer pairing to classify). Nothing here violated ADR 0020 as it stands. Those rows remain valid measurements of what they measured; if the amendment is accepted they become labelled degraded-review runs rather than unlabelled ones. The run was stopped during the second story. Two v1 defects, both **Phase 3 work** and both filed:

- **[#316](https://github.com/SnapdragonPartners/maestro/issues/316)** — v1 sends a non-nil `temperature` on every call, so every model that now rejects sampling parameters is undrivable. Verified: `claude-opus-5`, `gpt-5` and `o4-mini` all return 400 at the architect's 0.65.
- **[#317](https://github.com/SnapdragonPartners/maestro/issues/317)** — the architect's code-approval loop cannot force its terminal tool. `gpt-4.1` called `review_complete` zero times there, hit the hard limit at 16 iterations, and deadlocked into `ESCALATED`, which no headless run can answer; the coder then requeued on its 5-minute timeout and billed tokens in a loop.

**Only #317 blocks the committed configuration.** `gpt-4.1` accepts sampling parameters, so #316 does not apply to it; #316 constrains which *other* models could fill the seat. The viable set was not shown to be empty — `gpt-4o`, `claude-opus-4-5` and `claude-opus-4-6` all accept `temperature` and were never tested against #317. **DR declined further paid exploration after the one tested replacement failed**, and overrode the suite; that is the reason there is no run, not a demonstrated absence of alternatives.

**The one clean attempt.** Recorded because it is real data on a real identity, and it is the only evidence available that the agent path still functions.

Target descriptor: adapter `v1-as-patched` **0.2.0**; commit `6dfeabe6d167`; binary `sha256:96b34e9965a8…` (`maestro dev`); enforcement **streamed**; usage-surface **v2**; config `paired-default` **`sha256:cdc1490b5cc3`**; MPH model routing architect **`gpt-4.1`**, coder/PM `claude-sonnet-4-6`; prompt pack `v1-embedded`, prompt hash `sha256:410ab96e5627…`, harness hash `sha256:6cfd2372be07…`.

| Story | Story hash | Verdict | Tokens | Cost | Wall | Calls |
|---|---|---|---|---|---|---|
| `dep-bump-xnet` | `sha256:6b5141b820bb` | accepted | 670,129 | $1.771702 | 281s | 63 |
| `smoke-comment` | `sha256:75495b46c1a2` | **failed / target-error** | 332,670 | $0.948506 | 1,117s | `unavailable` (55 in the usage log) |

**Both attempts are measured.** The failed one is not a blank row: `smoke-comment` ran for nearly 19 minutes and spent real money before deadlocking, and its cost counts. `llm_calls` reports `unavailable` on that record because the attempt never reached a clean terminal state, but its usage log carries 55 calls, which the importer read — hence 118 in the plane against 63 for the accepted attempt alone.

Suite total **$2.720208** (plus $0.00 for the retired-model attempt). Per-model split across **both** attempts, read back out of the data plane after the full import:

| Model | Role | Calls | Tokens | Cost |
|---|---|---|---|---|
| `claude-sonnet-4-6` | coder + PM | 72 | 575,632 | $1.8489 |
| `gpt-4.1` | architect | 46 | 427,167 | $0.8713 |

**This is not a comparable series point.** `config_hash` moved `3d999b22fbbb` → `cdc1490b5cc3` and the adapter moved 0.1.0 → 0.2.0, so this is a new identity group by the [Run Protocol](../../benchmark/README.md)'s own rule. Two attempts on two rungs at N=1, one of them a failure.

#### Caveat — the recorded commit does not reproduce the binary (recorded, not hidden)

**The target was built from a dirty tree, contrary to the Run Protocol.** The recorded `commit_hash` is `6dfeabe6d167`, which does **not** contain the `gpt-4.1` entry in `KnownModels` — yet the usage log contains priced `gpt-4.1` calls, so the binary was built from an uncommitted working tree. The `binary_identity` digest still pins *what ran*, so the attempt's identity is intact and the numbers are sound; what is broken is reproducibility from the recorded commit alone.

This is the same class of failure as the 2026-07-22 baseline's three-identity split, and the Run Protocol's existing warning does not cover it: that warning says *do not commit while a run is in flight*, and says nothing about running with **uncommitted changes already present**. Filed as **[maestro#318](https://github.com/SnapdragonPartners/maestro/issues/318)** — a preflight clean-tree check, so a dirty descriptor is refused or recorded rather than discovered in review. No re-run: the binary digest preserves what matters.

**What it does and does not show.** It shows the agent path still completes a rung-2 story end to end — every validator and check passed on `dep-bump-xnet`, and the pipeline produced an accepted result. It does **not** discharge Phase 2's regression obligation: **four of six stories never ran**, and the agent-path changes item 9 shipped (usage-surface v1 → v2, adapter 0.1.0 → 0.2.0) are therefore not regression-cleared. That obligation is carried forward, not met.

**A cost observation worth keeping.** `dep-bump-xnet` cost **$1.77 against Phase 1a's $2.07** while using **roughly double the tokens** (670k vs 331k) and 50% more LLM calls (63 vs 42). The GPT architect runs materially more review rounds; it is cheaper only because its per-token rate is far below the retired Opus 4.1's $15/$75. Token-based budget headroom therefore matters more than dollar headroom under this pairing — a distinction that did not apply to any earlier row.
