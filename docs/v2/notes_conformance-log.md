+++
title = "Conformance Run Log"
edit_date = "2026-07-22"
status = "draft"
type = "notes"
summary = "The committed, distilled record of every phase-end golden-story conformance run: date, target identity, per-story verdict, and cost/token totals. The durable counterpart to the git-ignored raw results store — interim until the Phase 2 data plane makes performance records first-class artifacts."
+++

# Conformance Run Log

Status: **draft** — introduced by the conformance-first amendment to [ADR 0025](../adr/0025-golden-stories-and-benchmark-runner.md), which is itself PROPOSED and pending Codex + DR acceptance. Flips to `live` in that approval commit.

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

*The first phase-end `golden-all` conformance run appends below.*
