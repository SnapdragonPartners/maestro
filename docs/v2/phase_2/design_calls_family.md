+++
title = "Phase 2 Item 5 Design: Calls, Metrics And Audit Events"
edit_date = "2026-07-27"
status = "draft"
summary = "Mini-plan for the call family's typed queries: per-table write invariants, the open-to-completed call lifecycle, the cases the seam must reject, dependency-ordered truncation that refuses to prune pinned, open or referenced rows, and an exact decimal type for cost."
type = "design"
+++

# Phase 2 Item 5 Design: Calls, Metrics And Audit Events

Status: **draft** — revised after Codex round 1 (six P1s). For review before any query is written.

Covers `llm_calls`, `tool_calls`, `metric_events` and `audit_events`, plus the **truncation** operation that makes the Audit family's retention posture real. The seam and its conventions are item 4's ([`design_queries_artifacts.md`](design_queries_artifacts.md), live); this records only what differs.

## What is different from item 4

Item 4's family is reviewable and mutable through a lifecycle. This one has **no review, no digest binding and no amendment protocol** — but it is not uniformly immutable, and the first draft's claim that it is "born final" was wrong.

**Two shapes live here:**

| Shape | Tables | Mutability |
| --- | --- | --- |
| Born final | `metric_events`, `audit_events` | Written once, never updated |
| Open → completed | `llm_calls`, `tool_calls` | Created unfinished, completed exactly once |

`finished_at` is nullable on both call tables precisely because a call in flight has not ended. Treating the whole family as immutable would have produced a seam with no way to record an outcome.

The other difference is volume. ADR 0022 makes these the largest tables in the system, so the risks are **write-path invariants**, **deletion that cannot destroy what must be kept**, and **transaction boundaries that do not serialise a hot path**.

## D1. The call lifecycle

`CreateLLMCall` / `CreateToolCall` write the open row: identity, principal, lineage, and the request-side facts (provider, model, tool name, arguments). `finished_at` is null and the outcome columns are unset.

`CompleteLLMCall` records `finished_at`, the four token counters and `cost_usd`. `CompleteToolCall` records `finished_at`, `succeeded`, `result` and `error_message`.

**Completion is once-only and idempotent**, on the same shape as `StopPrincipalInstance` (item 4 D7): lock the row, classify in Go, write conditionally with `WHERE finished_at IS NULL`. Two paths can observe one call ending — a normal return and a supervisor's error handler — and the first outcome is the true one. A repeat call returns **the recorded outcome plus `Recorded: false`**, never an error, because a caller that genuinely cares can read the flag and a caller that does not should not fail.

No other column is updatable. There is no generic update, for the same reason item 4 has no generic status update.

## D2. Write invariants, per table

The first draft described one lineage rule for four tables. The schema disagrees, and the design must match it rather than the other way round:

| Table | Work lineage | `lineage_key` | Shape check |
| --- | --- | --- | --- |
| `llm_calls` | product/feature/epic/story | yes | yes |
| `tool_calls` | product/feature/epic/story | yes | yes |
| `metric_events` | product/feature/epic/story | **no** | yes |
| `audit_events` | **none at all** | no | no |

So:

- **`llm_calls`, `tool_calls`** — lineage is a prefix chain (a Story implies its Epic, Feature and Product). The seam validates the chain before the write so a caller learns *which* level is missing rather than reading `lineage_shape_check`. `lineage_key` is generated; the seam never supplies it.
- **`metric_events`** — same prefix-chain validation, but there is no `lineage_key` column to reference. Any design that joins or filters on one here is written against a column that does not exist.
- **`audit_events`** — carries only organization, user and principal. It has **no work lineage and no shape check**, so there is nothing to validate beyond the principal and organization. An audit event is not scoped to a Story today; wanting it to be is a schema change, not a seam change.

**Counters and cost.** `input/output/reasoning/cached_tokens` are `bigint`, non-negative by schema check; the seam narrows nothing. `cost_usd` is `numeric(18,8)` — see D5.

`cost_usd` **null is load-bearing**: Phase 1's `paired-local` config reports `cost_usd: unavailable` for local models. Null means *not knowable*, not zero — the four-state metric discipline of ADR 0025 carried into the plane. A seam defaulting it to 0 would make free and unmeasured indistinguishable in exactly the aggregate the benchmark exists to compute.

## D3. Provenance stays one atomic write

`tool_calls.llm_call_id` references `llm_calls_provenance_key` — `(llm_call_id, principal_instance_id, lineage_key, organization_id)` — so a tool call may only claim an LLM call made by the **same principal for the same work**.

The first draft proposed validating this before the insert. That is wrong twice: it adds a round trip to the hottest write path in the system, and it is a TOCTOU — the row it validated can change between the read and the write, so the check proves nothing the foreign key was not already proving.

**The composite foreign key stays authoritative.** The seam issues one INSERT and maps the named constraint violation back to a domain error, so the caller still gets "this tool call claims an LLM call belonging to a different principal or a different Story" rather than a four-column constraint name. Mapping by **constraint name** (not by message text) keeps that stable across Postgres versions.

This is the general rule for this family: where a constraint already expresses the invariant exactly, the seam translates its failure rather than duplicating its check.

## D4. Rejected cases

Enumerated before the queries are written, per `CLAUDE.md`:

| Rejected | Why |
| --- | --- |
| Lineage with a gap (Story without Epic) — `llm_calls`, `tool_calls`, `metric_events` | Violates the prefix chain; caller told which level is missing |
| Work lineage supplied for `audit_events` | The columns do not exist; silently dropping it would be worse |
| Tool call claiming another principal's or another Story's LLM call | Composite FK; translated to a domain error |
| Negative tokens or cost | Schema check, refused early with the field named |
| `cost_usd` supplied as a float | Precision loss on a reconciled number (D5) |
| Non-finite cost or metric value (NaN, ±Inf) | `numeric` has no NaN-safe ordering for our purposes and a non-finite cost is not a cost; refused before it can poison an aggregate |
| Empty `provider`, `model` or `tool_name` | Unattributable call record; every MPH and cost aggregate groups by these |
| Empty `metric_name` or `event_type` | An unnamed metric or event is unqueryable and silently useless |
| Completing an already-completed call | Once-only; returns the recorded outcome with `Recorded: false` |
| `finished_at` before `started_at` | Nonsense interval; no schema check exists, so the seam owns it |
| Any write naming another organization's principal or lineage | Multi-tenant boundary, as item 4 |
| Truncation without an explicit horizon | An unbounded delete should not be reachable by accident |

## D5. Cost is an exact decimal type

Created now, in `store`, rather than deferred: item 5 writes it, item 9's import must not round on the way in, and Phase 1B's economic comparison is the reason the column exists.

`store.USD` wraps an exact decimal and is **validated at construction** — not a freely castable string alias, which is a type that documents an intention rather than enforcing one. It parses from a decimal string, rejects non-finite and negative values, and carries at most 8 fractional digits to match `numeric(18,8)`. `float64` appears nowhere in the chain: binary64 cannot represent 8 decimal places exactly, and a cost that rounds is a cost that does not reconcile.

**The honest limit.** Item 9 imports from the Phase 1 runner, whose records carry cost as `float64` already. A new type cannot recover precision lost upstream. So the import path must either parse the **persisted numeric lexeme** where the runner preserved one, or apply a **single documented quantization** at the boundary — deterministic, applied once, and recorded as a lossy conversion rather than presented as an exact figure. Which of the two is available is a question for item 9's own design; item 5's obligation is to make sure the plane does not add loss of its own.

**Aggregates must report completeness.** A `SUM(cost_usd)` silently skips nulls, so a partial total looks identical to a complete one. Every cost aggregate returns the sum **plus the measured and unmeasured call counts**. This is the same four-state discipline as the runner's metrics: unmeasured is a reportable state, not an absence.

## D6. Truncation

The Audit family is truncatable by design, and this is where the phase can destroy what it promised to keep. Ship it in item 5: backup (item 8) is disaster recovery, not an undo mechanism, and Audit growth is the pressing problem.

**Per-table horizons**, because the cutoff column differs and a single "before" against the wrong column silently deletes the wrong rows:

| Table | Cutoff column | Dependents |
| --- | --- | --- |
| `audit_events` | `occurred_at` | none |
| `metric_events` | `recorded_at` | none |
| `tool_calls` | `started_at` | `management_artifacts`, `audit_artifacts` (`produced_by_tool_call_id`, `ON DELETE RESTRICT`) |
| `llm_calls` | `started_at` | `tool_calls` (`ON DELETE RESTRICT`) |

**Deletion runs in dependency order** — events, then metrics, then tool calls, then LLM calls — so a tool call that becomes deletable in this pass is gone before its LLM call is considered.

**`ON DELETE RESTRICT` raises an error rather than skipping.** A referenced row does not quietly survive a `DELETE`; it aborts the statement and takes the whole batch with it. Referenced rows must therefore be **excluded in the `WHERE`**, not discovered at commit. That is the single most important implementation consequence in this document.

**Three reasons a row past the horizon is retained**, and all three are excluded in SQL and counted:

1. **Pinned** — `retention_pins` names it (Audit artifacts and attachments today; calls carry no pins, which is stated because "pin an expensive call record" is a schema change, not a seam change).
2. **Open** — `finished_at IS NULL`. An unfinished call is in-flight work, not history, and deleting it destroys a record something still holds. This is ADR 0027's rule that destructive recovery must never remove another actor's in-progress work.
3. **Referenced** — an artifact points at this tool call, or a tool call points at this LLM call.

**The result reports each reason separately.** Returning only a delete count makes "nothing was retained" and "everything was retained" identical, and returning a single retained count makes "still running" and "pinned forever" identical — which are different operational situations with different responses.

**Testing.** Every truncation test seeds rows past the horizon in **all four states** — deletable, pinned, open, referenced — and asserts the first is gone and the other three survive. A test seeding only deletable rows passes against a delete with no guard clauses at all. Each guard also gets a **direct generated-query test**, since the seam is not the only thing that could regress, and item 4 proved that a backstop behind a working guard is unreachable through the normal path.

## D7. Transaction boundaries and isolation

Item 4 wrapped nearly everything. Here the default inverts — but the isolation question is separate from the transaction question, and conflating them is the mistake item 4 already made once.

- **Single-row writes take no transaction.** A call record is one INSERT on the hottest path in the system.
- **Batch writes take one.** A run's imported call records are meaningless individually; the batch is the unit of failure. Exposed as a distinct method, not a loop over the single-row one.
- **Completion takes one**, being lock-classify-write.
- **Truncation takes one, at explicit `REPEATABLE READ`.**

That last point is the correction. A transaction at Postgres's default `READ COMMITTED` gives **every statement a fresh snapshot**, so a multi-table delete would evaluate its guards against four different instants: a call could be completed, a pin created, or an artifact written between the statements, and the pass would delete a row that was protected when the operation began. Truncation therefore sets `REPEATABLE READ` explicitly and performs its deletes in dependency order within that one snapshot.

This is the same error item 4 shipped and had to fix, restated here because writing it down evidently did not prevent repeating it.

## D8. Reads

Deliberately few. ADR 0022 says these are metrics and traces; item 9's import is the first real consumer, and Phase 1B's economic comparison is where aggregate shapes get chosen against a real question.

Item 5 ships: by principal instance, by Story, by time range, and cost/token aggregates by model — the shapes Phase 1's D9 calibration already needed, each returning measured and unmeasured counts per D5. Anything else waits for a caller, on the rule item 3 applied to tables and the registry applies to types.

## Open question for review

**Should completion be permitted to arrive with no matching open row?** An importer replaying a finished run has both halves at once, and a two-step create-then-complete is a round trip it does not need. A single `CreateCompletedLLMCall` would serve it — but it is also a path that writes a terminal row directly, bypassing the once-only guard, so it wants its own justification rather than being added quietly. My inclination is to defer it to item 9 with a real caller in hand.
