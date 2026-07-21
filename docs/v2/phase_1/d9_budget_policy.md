+++
title = "D9 Sampling And Budget Policy"
edit_date = "2026-07-21"
status = "live"
summary = "The D9 policy record required by Phase 1 item 6: real per-story costs measured on instrumented runs, N fixed at 3 for the primary configuration, and per-story and per-suite budget caps fixed as runner-enforced values with overrun-as-failure. Caps are a runaway safeguard sized from observed accepted runs, not a performance target."
+++

# D9 Sampling And Budget Policy

Status: live — Phase 1 item 6 (`instrument-costs`), the policy record the [Phase 1 plan](plan_scope.md) requires ("the D9 policy record lands in this directory"), satisfying the plan's exit criterion that *the D9 sampling and budget policy is written down with numbers fixed from instrumented runs, and enforced by the runner*. Measurement campaign: 2026-07-21, `paired-default` (frontier) against v1-as-patched.

## Context

Before this record, every budget in the suite was a **guess**. The drafts were inconsistent by an order of magnitude — `dep-bump` declared 3M tokens, `bugfix` 400k, `cleanup` 250k — set before any story had ever run to completion. Guessed caps are not free: a cap set below a story's real cost converts a *healthy* run into a false `budget-overrun`, which is exactly what happened twice during this campaign (`bugfix` at 400k and `cleanup` at 250k were both killed mid-progress while behaving correctly). The purpose of this record is to replace guesses with measurements.

The campaign also had to fix the target itself before it could measure anything: three v1 defects (P-9, P-10, P-11 in [patches_v1.md](patches_v1.md)) each killed runs before any cost signal existed. Those are enumerated there, not here.

## Measurements

Accepted (terminal-verdict) runs on `paired-default`. These are the only admissible cost observations — a failed run measures the failure, not the story.

| Story | Tokens | Cost | Wall clock | LLM calls | n |
|---|---|---|---|---|---|
| `smoke-comment` (rung 0) | 426k | $1.97 / $0.89 | 750s | 47 | 2 |
| `dep-bump-xnet` (rung 1) | 320k | $1.81 / $2.09 | 277s | 41 | 2 |
| `bugfix-openai-stopreason` | 727k | $7.06 | 418s | 70 | 1 |

Mean across the three working stories: **~500k tokens, ~$3.7, ~480s per accepted run.**

The spread is the headline finding: **`bugfix` costs ~3.5× `dep-bump`** in dollars despite being only ~2.3× the tokens (the architect's opus seat dominates cost, the coder's sonnet seat dominates volume). Per-story caps are therefore mandatory — a single uniform cap would either strand `bugfix` or leave `dep-bump` effectively uncapped.

**Sample thinness is a real limitation.** These are n=1–2, not distributions. The caps below are consequently sized for *safety margin*, not fitted to a percentile; they are enforced values, but the underlying samples remain thin and should be widened opportunistically as the suite runs.

## Decisions

### N (sampling)

**N = 3 for the primary configuration (`paired-default`); N = 1 for secondary configurations.** This confirms the provisional 3/1 the plan carried. A full N=3 sweep of the three working stories costs ~9 × $3.7 ≈ **$33**, which is affordable per-sweep; N=1 on secondary configs keeps the matrix from multiplying that. N=3 is the minimum that shows variance at all and remains the honest floor — it is a variance *smoke test*, not a statistically strong sample.

### Per-story caps (runner-enforced, overrun-as-failure)

Sized at **~3× the observed accepted maximum**, rounded. Rationale: a healthy run varies well within 3×, so the cap never fires on correct behavior; a genuine runaway (nonconvergence, decomposition multiplication) exceeds it by far more and is caught early. Caps are a **safeguard, not a target** — being under cap is not a result.

| Story | max_tokens | max_cost_usd | max_wall_clock_seconds |
|---|---|---|---|
| `smoke-comment` | 1,500,000 | 8.0 | 2400 |
| `dep-bump-xnet` | 1,500,000 | 8.0 | 2400 |
| `bugfix-openai-stopreason` | 2,500,000 | 20.0 | 2700 |

### Per-run and per-suite caps (`paired-default`)

| Field | Value | Derivation |
|---|---|---|
| `expected_tokens_per_run` | 500,000 | measured mean |
| `expected_cost_usd_per_run` | 4.0 | measured mean (~$3.7), rounded up |
| `max_cost_usd_per_run` | 20.0 | aligned to the highest per-story cap |
| `max_cost_usd_per_suite` | 60.0 | bounds a full N=3 sweep (~$33 expected) with headroom for one in-flight conservative reservation |

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
