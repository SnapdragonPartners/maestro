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

*No admissible run yet. The v1 `golden-minimal` baseline owed by Phase 1 — a real run on the settled identity, with full descriptor — appends here when taken; the first phase-end `golden-all` conformance run follows it.*
