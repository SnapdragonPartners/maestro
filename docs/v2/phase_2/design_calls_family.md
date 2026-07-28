+++
title = "Phase 2 Item 5 Design: Calls, Metrics And Audit Events"
edit_date = "2026-07-27"
status = "draft"
summary = "Mini-plan for the call family's typed queries: per-table write invariants, the open-to-completed call lifecycle, the cases the seam must reject, dependency-ordered truncation that refuses to prune pinned, open or referenced rows, and an exact decimal type for cost."
type = "design"
+++

# Phase 2 Item 5 Design: Calls, Metrics And Audit Events

Status: **draft** — revised after three Codex rounds (six P1s, then five, then three). Carries one schema correction, migration 000011.

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

**Completion is once-only and idempotent**, on the shape of `StopPrincipalInstance` (item 4 D7): lock the row, classify in Go, write conditionally with `WHERE finished_at IS NULL`. Two paths can observe one call ending — a normal return and a supervisor's error handler — and the first outcome is the true one. A repeat returns **the recorded outcome with `Recorded: false`**, never an error, and a *conflicting* repeat returns the first outcome too. It is not a rejected case; a caller that cares reads the flag.

No other column is updatable. There is no generic update, for the reason item 4 has no generic status update.

### Tool call outcomes must cohere

`CompleteToolCall` records `finished_at`, `succeeded`, `result` and `error_message`. The schema does not tie them together, so the seam does:

- `succeeded = true` **must not** carry an `error_message`. A successful call with an error recorded is a row no reader can interpret.
- `succeeded = false` **must** carry a diagnostic. A failure with no reason is an audit record that answers nothing, and the failure path is exactly when someone reads it.

### LLM call outcome — a schema correction, not a note

`CompleteLLMCall` records `finished_at`, the four token counters, `cost_usd`, and now `succeeded` and `error_message`.

The first draft left `llm_calls` with **no success, error or status column** and proposed recording provider failures as an `audit_event` naming the call. That does not work, and checking rather than assuming is what showed it: `audit_events` has **no `llm_call_id` column and no foreign key**, so the "link" would be an unenforced identifier buried in `detail` JSON. An unenforced pointer is not a closed gap; it is the same gap with a convention on top, and nothing would notice when a caller stopped honouring it.

Deferring it was also the wrong instinct for a second reason. Without an outcome column a completed zero-token call and a failed call are indistinguishable **on the row**, which corrupts precisely the cost and reliability aggregates this family exists to serve. That is not documentation debt; it is a family that cannot answer its own question.

**Migration `000011_llm_call_outcome`** adds both columns and mirrors `tool_calls`' existing pairing, `CHECK ((finished_at IS NULL) = (succeeded IS NULL))` — append-only, per the phase's delegated decision 4. It backfills before adding the constraint so it is correct on a non-empty table, and records the assumption it must make: rows completed before the migration are presumed successful because no outcome is recoverable, so `succeeded = true` on them is not evidence of anything.

Coherence **between** `succeeded` and `error_message` stays the seam's rule, exactly as for tool calls — a success must not carry an error, a failure must carry a diagnostic. The schema can express "finished implies an outcome"; it cannot express which pairings are meaningful.

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

`cost_usd` **null is load-bearing, and means two different things depending on the row's state**:

| Row state | `cost_usd` null means |
| --- | --- |
| Open (`finished_at IS NULL`) | **Pending** — the call has not ended, so no cost exists yet |
| Completed | **Unavailable** — the call ended and its cost is not knowable, e.g. `paired-local`'s local models |

Only the second is ADR 0025's *unmeasured*. Conflating them classifies in-flight usage as permanently unmeasured, which is how a running campaign would under-report its own cost and never correct itself. A seam defaulting either to 0 would additionally make free and unmeasured indistinguishable — in exactly the aggregate the benchmark exists to compute.

## D3. Provenance stays one atomic write

`tool_calls.llm_call_id` references `llm_calls_provenance_key` — `(llm_call_id, principal_instance_id, lineage_key, organization_id)` — so a tool call may only claim an LLM call made by the **same principal for the same work**.

The first draft proposed validating this before the insert. That is wrong twice: it adds a round trip to the hottest write path in the system, and it is a TOCTOU — the row it validated can change between the read and the write, so the check proves nothing the foreign key was not already proving.

**The composite foreign key stays authoritative.** The seam issues one INSERT and maps the named constraint violation to a domain error, matching on **constraint name** rather than message text so it stays stable across Postgres versions.

The error is deliberately **generic — `ErrInvalidProvenance`** — because the constraint cannot honestly tell the causes apart. A violation means the claimed parent does not exist, *or* the principal differs, *or* the lineage differs (and `lineage_key` includes `user_id`, so that covers an accountable-user mismatch), *or* it belongs to another organization. Naming one of those would be a guess presented as a diagnosis. The error carries the four values the caller supplied so they can see what they claimed; it does not claim to know which part was wrong.

This is the general rule for this family: where a constraint already expresses the invariant exactly, the seam translates its failure rather than duplicating its check.

## D4. Rejected cases

Enumerated before the queries are written, per `CLAUDE.md`:

| Rejected | Why |
| --- | --- |
| Lineage with a gap (Story without Epic) — `llm_calls`, `tool_calls`, `metric_events` | Violates the prefix chain; caller told which level is missing |
| Work lineage supplied for `audit_events` | The columns do not exist; silently dropping it would be worse |
| Tool call whose claimed LLM call fails the provenance key | Composite FK; translated to a generic `ErrInvalidProvenance`, which does not claim to know which of the four causes applied (D3) |
| Negative tokens or cost | Schema check, refused early with the field named |
| `cost_usd` supplied as a float | Precision loss on a reconciled number (D5) |
| `cost_usd` whose integer part exceeds 10 digits | `numeric(18,8)` bounds TOTAL precision, not just the fraction (D5) |
| Non-finite cost or metric value (NaN, ±Inf) | `numeric` has no NaN-safe ordering for our purposes and a non-finite cost is not a cost; refused before it can poison an aggregate |
| Empty `provider`, `model` or `tool_name` | Unattributable call record; every MPH and cost aggregate groups by these |
| Empty `metric_name` or `event_type` | An unnamed metric or event is unqueryable and silently useless |
| Tool call completed as succeeded but carrying an error message | Incoherent outcome; no reader can interpret it |
| Tool call completed as failed with no diagnostic | The failure path is precisely when someone reads the record |
| `finished_at` before `started_at` | Nonsense interval; no schema check exists, so the seam owns it |
| Any write naming another organization's principal or lineage | Multi-tenant boundary, as item 4 |
| Truncation without an explicit horizon | An unbounded delete should not be reachable by accident |

## D5. Cost is an exact decimal type

Created now, in `store`: item 5 writes it, item 9's import must not round on the way in, and Phase 1B's economic comparison is the reason the column exists.

`store.USD` wraps an exact decimal and is **validated at construction** — not a freely castable string alias, which documents an intention rather than enforcing one. It parses from a decimal string and rejects non-finite and negative values. `float64` appears nowhere in the chain: binary64 cannot represent 8 decimal places exactly, and a cost that rounds is a cost that does not reconcile.

**Write range and aggregate range are different, and conflating them breaks one of them.**

`numeric(18,8)` bounds **total** precision at 18 digits, of which 8 are fractional — so a stored value has at most **10 integer digits**. Validation on the write path enforces both bounds; the first draft mentioned only the fractional one, which would have let a value pass the seam and fail the column.

An **aggregate is not bound by a row's typmod**. `SUM(cost_usd)` over a large campaign can exceed any single row's 10 integer digits, and Postgres returns unconstrained `numeric` for it. So the aggregate result type carries no typmod validation: applying the write-range rule to a sum would reject a correct total for being large, which is precisely what a total is for.

**Aggregates group by `(provider, model)`, never model alone.** The same model name is served by more than one provider at different prices — that is the whole premise of Phase 1's `paired-local` config, where a local runner and a hosted API can answer to the same name. Grouping on model alone silently sums two price regimes into one figure that describes neither.

**Aggregates report completeness.** `SUM` skips nulls, so a partial total is indistinguishable from a complete one. Every cost aggregate returns the sum **plus measured and unmeasured call counts** — the four-state discipline again: unmeasured is a reportable state, not an absence.

**The honest limit.** Item 9 imports from the Phase 1 runner, whose records carry cost as `float64` already. No new type recovers precision lost upstream. The import must either parse the **persisted numeric lexeme** where the runner preserved one, or apply a **single documented quantization** at the boundary — deterministic, applied once, recorded as a lossy conversion rather than presented as exact. Which is available is item 9's question; item 5's obligation is that the plane adds no loss of its own.

## D6. Truncation

The Audit family is truncatable by design, and this is where the phase can destroy what it promised to keep. Ship it in item 5: backup (item 8) is disaster recovery, not an undo mechanism, and Audit growth is the pressing problem.

**The first draft's pin guard could never fire.** Pins target `audit_artifacts` and `binary_attachments` — and neither table was in the truncation set, so "retained because pinned" was unreachable and its test would have been vacuous. `audit_artifacts` belongs here; attachments belong to item 6 with the object module, since deleting a row whose bytes live in object storage is that item's commit-order problem.

**Per-table horizons and cutoff columns**, in deletion order:

| Order | Table | Cutoff | Retained when |
| --- | --- | --- | --- |
| 1 | `audit_events` | `occurred_at` | — |
| 2 | `metric_events` | `recorded_at` | — |
| 3 | `audit_artifacts` | `created_at` | **pinned** (`retention_pins`, itself `ON DELETE RESTRICT`) |
| 4 | `tool_calls` | `finished_at` | **open**; **referenced** by a Management or Audit artifact |
| 5 | `llm_calls` | `finished_at` | **open**; **referenced** by a surviving tool call |

`binary_attachments` — item 6.

**Completed calls age from completion, not from start.** The first draft used `started_at`, which deletes a long-running call the instant it finishes if it *started* before the horizon — the calls most worth keeping are exactly the slow ones. Deletion therefore tests `finished_at < before`.

**Old open calls are counted, never deleted.** A call still open with `started_at < before` is reported separately, because a call open long past the horizon is an operational signal — a leaked or stuck call — and silently ignoring it wastes the one place it would have been visible. Deleting it would destroy an in-progress record, which ADR 0027 forbids.

**Order is forced by the foreign keys.** `audit_artifacts` references `tool_calls`, and `tool_calls` references `llm_calls`, so artifacts must go before the calls they point at. `management_artifacts` also references `tool_calls` and is **never truncated** — durable by definition — so a tool call cited by a Management artifact is retained as referenced, permanently. That is correct: it is provenance for reviewable work product.

**`ON DELETE RESTRICT` raises an error rather than skipping.** A referenced row does not quietly survive a `DELETE`; it aborts the statement and takes the batch with it. Referenced rows must therefore be **excluded in the `WHERE`**, not discovered at commit. This is the single most important implementation consequence in this document.

**The result reports each retention reason separately, and the reasons do not overlap.** One delete count makes "nothing retained" and "everything retained" identical; one *retained* count makes "still running" and "pinned forever" identical. They are different situations with different responses.

But the reasons are **not naturally disjoint** — a call can be open *and* referenced at once — so independent counts would not sum to anything, and a reader adding them up would over-count. Each candidate is therefore assigned to **exactly one bucket by precedence**:

**pinned → open → referenced**

Open outranks referenced deliberately: a call open long past the horizon is an operational problem, and being referenced as well does not make it less so. Pinned outranks both, though it cannot currently collide with them, since only `audit_artifacts` is pinnable and it has no open state.

The result therefore reconciles exactly:

`candidates = deleted + retained_pinned + retained_open + retained_referenced`

and the test asserts that identity rather than only the individual counts, because four numbers that each look plausible can still describe no consistent set of rows.

**Testing.** Each table is seeded past the horizon in **the states that apply to it** — every table gets a deletable row; `audit_artifacts` also gets a pinned one; the call tables also get an open one and a referenced one. A test seeding only deletable rows passes against a delete with no guard clauses at all. Each guard also gets a **direct generated-query test**, since item 4 proved a backstop behind a working guard is unreachable through the normal path.

## D7. Transaction boundaries and isolation

Item 4 wrapped nearly everything. Here the default inverts — but the isolation question is separate from the transaction question, and conflating them is the mistake item 4 already made once.

- **Single-row writes take no transaction.** A call record is one INSERT on the hottest path in the system.
- **No batch-import API in item 5.** It existed in the first draft to serve item 9's importer — whose records are *already completed*, which is the very API the open question proposed deferring. The two cannot be decided apart, so both are deferred to item 9 together, with a real caller in hand. Item 5 ships the create-then-complete pair only.
- **Completion takes one**, being lock-classify-write.
- **Truncation takes one, at explicit `REPEATABLE READ`.**

That last point is the correction. A transaction at Postgres's default `READ COMMITTED` gives **every statement a fresh snapshot**, so a multi-table delete would evaluate its guards against four different instants: a call could be completed, a pin created, or an artifact written between the statements, and the pass would delete a row that was protected when the operation began. Truncation therefore sets `REPEATABLE READ` explicitly and performs its deletes in dependency order within that one snapshot.

This is the same error item 4 shipped and had to fix, restated here because writing it down evidently did not prevent repeating it.

## D8. Reads

Deliberately few. ADR 0022 says these are metrics and traces; item 9's import is the first real consumer, and Phase 1B's economic comparison is where aggregate shapes get chosen against a real question. Anything not listed waits for a caller, on the rule item 3 applied to tables and the registry applies to types.

**The read matrix is per table, because the tables do not carry the same columns.** The first draft offered "by Story" across the family, which `audit_events` cannot serve:

| Read | `llm_calls` | `tool_calls` | `metric_events` | `audit_events` |
| --- | --- | --- | --- | --- |
| By principal instance | yes | yes | yes | yes |
| By Story | yes | yes | yes | **no — no work lineage exists** |
| By time range | `started_at` | `started_at` | `recorded_at` | `occurred_at` |
| By type | by `(provider, model)` | by `tool_name` | by `metric_name` | by `event_type` |

`audit_events` is reachable by organization, user, principal and time only. Offering a Story filter would mean inventing one, and the honest alternative — joining through the principal instance — answers a different question ("events by the agent that also worked on this Story") and must not be presented as the same one.

**Aggregates.** Cost and token totals group by **`(provider, model)`** — never model alone, consistently with D5. The same model name is served by different providers at different prices, which is the premise of Phase 1's `paired-local` config; grouping on the name alone sums two price regimes into a figure describing neither.

**Aggregates run over completed calls only, and report the open ones.** A `SUM` over all rows would fold pending calls into the unmeasured bucket, so every aggregate returns:

- the total over completed calls,
- the count of completed calls **with** a cost (measured),
- the count of completed calls **without** one (unmeasured — genuinely not knowable),
- the count of **open** calls excluded from the total (pending — not yet knowable).

Three states, reported separately, because a campaign that under-reports its own cost while still running and never corrects itself is the failure mode this exists to prevent.

## Resolved: completed-call insertion and batch import both go to item 9

The first draft asked whether a `CreateCompletedLLMCall` should exist for importers, while separately promising a batch API for the same importer. Those are one decision, not two: item 9's records arrive already completed, so a batch API for them *is* the completed-call API.

**Both deferred to item 9** (DR's call). Item 5 ships create-then-complete, and item 9 designs the import path against its actual source — including whether writing a terminal row directly, bypassing the once-only guard, is justified there.
