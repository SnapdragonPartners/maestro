+++
title = "D9 Sampling And Budget Policy"
edit_date = "2026-07-21"
status = "draft"
summary = "The D9 policy record required by Phase 1 item 6: per-story costs measured on instrumented runs, N fixed at 3 for the primary configuration, and per-story and per-suite budget caps proposed as runner-enforced values with overrun-as-failure. Caps are a provisional runaway safeguard sized from observed accepted runs, not a performance target."
+++

# D9 Sampling And Budget Policy

Status: **draft** — Phase 1 item 6 (`instrument-costs`), under review. Per [ADR 0017](../../adr/0017-v2-documentation-authority-and-lifecycle.md) a phase artifact stays draft until its approval commit; this record flips to `live` (with [ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md) and the [Phase 1 plan](plan_scope.md) exit checkbox) only on approval. Until then the numbers below are **proposed**, though they are already enforced in the story and config TOMLs as provisional runaway safeguards.

When accepted this satisfies the plan's exit criterion that *the D9 sampling and budget policy is written down with numbers fixed from instrumented runs, and enforced by the runner*. Measurement campaign: 2026-07-21, `paired-default` (frontier) against v1-as-patched.

## Context

Before this record, every budget in the suite was a **guess**. The drafts were inconsistent by an order of magnitude — `dep-bump` declared 3M tokens, `bugfix` 400k, `cleanup` 250k — set before any story had ever run to completion. Guessed caps are not free: a cap set below a story's real cost converts a *healthy* run into a false `budget-overrun`, which is exactly what happened twice during this campaign (`bugfix` at 400k and `cleanup` at 250k were both killed mid-progress while behaving correctly). The purpose of this record is to replace guesses with measurements.

The campaign also had to fix the target itself before it could measure anything: three v1 defects (P-9, P-10, P-11 in [patches_v1.md](patches_v1.md)) each killed runs before any cost signal existed. Those are enumerated there, not here.

## Measurements

Every accepted (terminal-verdict) run on `paired-default`, listed individually with the target identity it ran against. Failed runs are excluded — a failure measures the failure, not the story. **These attempts are not mutually comparable**: the target identity moved three times across the campaign (fixture re-pin, the union cache image, and the P-9/P-10 patches), so they are pooled here only as evidence of magnitude, never as a distribution.

| # | Suite | Story | Tokens | Cost | Wall | Calls | Target identity |
|---|---|---|---|---|---|---|---|
| 1 | `discovery-011` | `smoke-comment` | 426k | $1.97 | 750s | 47 | **I-A**: pre-re-pin, pre-cache-image, pre-P-9/P-10/P-11 |
| 2 | `discovery-012` | `dep-bump-xnet` | 320k | $1.81 | 277s | 41 | **I-A** |
| 3 | `cal-frontier-validate` | `smoke-comment` | — | $0.89 | — | — | **I-B**: post-re-pin + cache-image, pre-P-9/P-10/P-11 |
| 4 | `cal-frontier-v2` | `dep-bump-xnet` | — | $2.09 | — | — | **I-C**: post-P-9/P-10, pre-P-11 |
| 5 | `cal-d9-stage1` | `bugfix-openai-stopreason` | 726,873 | $7.06 | 418s | 70 | **I-C** |

Attempts 3 and 4 report cost only: **a power failure destroyed the scratchpad results store before those records were moved to `benchmark/runs/`**, and only the headline cost survived in the branch discussion. Attempt 5 is the only run whose full record survives on disk. This gap is the direct cause of the durable-store change in this branch, and it is why the token/wall/call columns are sparser than the cost column.

### Derivation

Stated explicitly rather than asserted, since the pool is heterogeneous:

- **Cost, all accepted attempts (n=5, spans I-A/I-B/I-C):** ($1.97 + $1.81 + $0.89 + $2.09 + $7.06) / 5 = **$2.76**.
- **Cost, current-identity attempts only (I-C, n=2):** ($2.09 + $7.06) / 2 = **$4.58**. This is the figure `expected_cost_usd_per_run` is derived from — it is the only subset that ran against the patched target, and it is the conservative of the two.
- **Tokens, attempts with surviving token records (n=3, spans I-A/I-C):** (426k + 320k + 727k) / 3 = **491k** → `expected_tokens_per_run` 500,000. Necessarily cross-identity: only one current-identity attempt has a token record.

No attempt ran against the **final** identity in this branch: P-11 landed after attempt 5, and fixing the caps below moves every `story_hash` again. The caps are therefore *provisional safeguards derived from a prior identity*, not a calibrated baseline. The first post-approval sweep establishes the comparable baseline.

The spread is the headline finding and survives the identity caveat: **`bugfix` costs ~3.4× `dep-bump`** in dollars despite being only ~2.3× the tokens (the architect's opus seat dominates cost, the coder's sonnet seat dominates volume). Per-story caps are therefore mandatory — a single uniform cap would either strand `bugfix` or leave `dep-bump` effectively uncapped.

## Decisions

### N (sampling)

**N = 3 for the primary configuration (`paired-default`); N = 1 for secondary configurations.** This confirms the provisional 3/1 the plan carried. A full N=3 sweep of the three working stories costs ~9 × $3.7 ≈ **$33**, which is affordable per-sweep; N=1 on secondary configs keeps the matrix from multiplying that. N=3 is the minimum that shows variance at all and remains the honest floor — it is a variance *smoke test*, not a statistically strong sample.

### Per-story caps (runner-enforced, overrun-as-failure)

Rule: **at least 3× the observed accepted maximum for that story, rounded up to a round figure.** Rationale: a healthy run varies well within 3×, so the cap never fires on correct behavior; a genuine runaway (nonconvergence, decomposition multiplication) exceeds it by far more and is caught early. Caps are a **safeguard, not a target** — being under cap is not a result.

Applied per story, showing the arithmetic:

| Story | Observed max | 3× | max_tokens | max_cost_usd | max_wall_clock_seconds |
|---|---|---|---|---|---|
| `smoke-comment` | 426k / $1.97 | 1.28M / $5.91 | 1,500,000 | 8.0 | 2400 |
| `dep-bump-xnet` | 320k / $2.09 | 0.96M / $6.27 | 1,500,000 | 8.0 | 2400 |
| `bugfix-openai-stopreason` | 727k / $7.06 | 2.18M / $21.18 | 2,500,000 | 24.0 | 2700 |

`dep-bump` takes `smoke`'s token cap rather than its own smaller 3× figure: the two are the same order of magnitude and a shared floor avoids a cap so tight that ordinary variance trips it.

### Per-run and per-suite caps (`paired-default`)

| Field | Value | Derivation |
|---|---|---|
| `expected_tokens_per_run` | 500,000 | 491k mean of the three surviving token records, rounded |
| `expected_cost_usd_per_run` | 5.0 | $4.58 current-identity (I-C) mean, rounded up |
| `max_cost_usd_per_run` | 24.0 | must be ≥ the highest per-story cap, which is `bugfix` at $24 |
| `max_cost_usd_per_suite` | 70.0 | a full N=3 sweep of three stories expects 9 × $4.58 ≈ $41; $70 leaves headroom for variance and one in-flight reservation while still stopping a systematic runaway well below the $120 sum-of-caps |

Suite accounting settles down on completion (`charged` converges to `observed`), so the suite cap needs to cover realized cost plus one outstanding reservation, not the sum of every attempt's cap.

## Methodology

How to execute a run correctly — preflight, credentials, reading four-state metrics, and policing a run's health — is the **Run Protocol** in [`benchmark/README.md`](../../../benchmark/README.md). It is the operational companion to this record and must be followed unchanged between runs; methodology drift silently invalidates comparison.

Admissibility rules for this record:

- **Only `accepted` runs are cost observations.** A `budget-overrun` measures the cap; a `target-error` measures a defect.
- **A verdict is not sufficient.** A run is admissible only after the health check (orderly state progression, no requeue/abandonment, no architect fatal shutdown) — an `accepted` run that thrashed is not a clean sample.
- **Comparability requires matching `story_hash` + `config_hash` + harness identity.** The story hash covers the `[budget]` block, so fixing the caps in this record moves every story's hash. **Observations above predate that change and are not hash-comparable to future runs** — they are the basis for the caps, not a baseline to compare against. The first post-policy sweep establishes the comparable baseline.

## Known Gaps

- **Thin samples (n=1–2).** Caps carry margin rather than fitted confidence. Widen as sweeps accumulate.
- **`cleanup-provider-options` is blocked, not calibrated.** It over-decomposes — its prompt enumerates five provider functions and the architect splits that into 5 Stories, 0 of which ever completed across two attempts (2.01M tok/$9.37, then 2.26M tok/$13.11). Cost is ceremony multiplication, not difficulty. Parked in `benchmark/stories/blocked/` with the unblock condition; it contributes no numbers here.
- **The structural cause of that over-decomposition is unfixed.** Nothing reviews an architect's Story decomposition, violating [ADR 0020](../../adr/0020-review-invariant-reviewer-vs-partner.md)'s invariant that every persistent Management artifact is reviewed by a non-author. Carried as [ADR backlog #15](../notes_adr-backlog.md) (Phase 5). Until that seat exists, over-decomposition is a live cost risk on any enumerating prompt, and the per-story caps are the only thing bounding it.
- **Single configuration.** All numbers are `paired-default`. `paired-local` is token-budgeted with `cost_usd` `unavailable` by design (item 5.1) and its token counts come from different models, so it cannot proxy frontier budgets.

## Campaign Cost

The measurement campaign itself cost **~$41** (frontier, hosted): ~$11.75 of validation runs establishing that the stories and harness worked, $16.43 of stage-1 completion runs, and $13.11 on the terminated `cleanup` achievability run. Recorded here because the benchmark's own operating cost is a Phase 1 concern — item 5.1 exists to reduce it, and a policy record that hides its own price would be dishonest about the instrument's economics.
