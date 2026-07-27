+++
title = "Phase 2 Item 5 Design: Calls, Metrics And Audit Events"
edit_date = "2026-07-27"
status = "draft"
summary = "Mini-plan for the call family's typed queries: the invariants each write must hold, the cases the seam must reject, retention-safe Audit truncation, and where transaction boundaries fall."
type = "design"
+++

# Phase 2 Item 5 Design: Calls, Metrics And Audit Events

Status: **draft** — for Codex review before any query is written.

Covers `llm_calls`, `tool_calls`, `metric_events` and `audit_events` (item 3's [table inventory](inventory_schema-tables.md)), plus the **truncation** operation that makes the Audit family's retention posture real. The seam and its conventions are item 4's ([`design_queries_artifacts.md`](design_queries_artifacts.md), live); this records only what differs.

## What is different from item 4

Item 4's family is **reviewable and mutable through a lifecycle**. This one is neither: calls, metrics and events are **born final**, like Audit artifacts. So there are no transitions, no reviews, no digests to bind, and no amendment protocol.

What replaces them is the opposite problem. This is the **high-volume** family — ADR 0022 calls `llm_calls` and `tool_calls` metrics and traces, and `audit_events` the Audit enumeration — and the largest tables in the system. The risks move from *did the lifecycle rules hold* to *can this be written cheaply, and deleted safely*.

Three things therefore get the attention item 4 gave to transitions: **write-path invariants**, **truncation that cannot delete pinned evidence**, and **transaction boundaries that do not serialise a hot path**.

## D1. Invariants each write must hold

The schema already enforces a good deal. The seam adds what SQL cannot say, and refuses early what SQL would refuse late with a constraint name.

**Lineage shape.** Every table carries a `lineage_key` generated column and a `lineage_shape_check`: lineage is a **prefix chain** — a Story implies its Epic, Feature and Product. The seam validates the chain before the write so the caller learns *which* level is missing rather than reading `lineage_shape_check`.

**Provenance.** `tool_calls.llm_call_id` points at `llm_calls_provenance_key` — `(llm_call_id, principal_instance_id, lineage_key, organization_id)` — so a tool call may only claim an LLM call **made by the same principal for the same work**. The seam therefore cannot let a caller pass a bare `llm_call_id`; it must carry the principal and lineage that the composite key requires, and those must be the tool call's own. Checked before the write, because the FK failure names four columns and no rule.

**Non-negative counters and cost.** `input/output/reasoning/cached_tokens >= 0`, `cost_usd IS NULL OR >= 0`. `cost_usd` is `numeric(18,8)` — **never a float**. The seam takes a decimal string or a `*big.Rat`-shaped value, never `float64`: binary64 cannot represent 8 decimal places exactly, and a cost that rounds is a cost that does not reconcile.

`cost_usd` is **nullable and that is load-bearing**: Phase 1's `paired-local` config reports `cost_usd: unavailable` for local models. Null means *not knowable*, not zero — the four-state metric discipline from ADR 0025, carried into the plane. A seam that defaulted it to 0 would make free and unmeasured indistinguishable in exactly the aggregate the benchmark exists to compute.

**Token counts are `bigint`** and the seam narrows nothing. Item 4's lesson stands: any int conversion at the seam is checked or absent.

**Open-ended calls.** `finished_at` is nullable — a call in flight has no end. The seam exposes a distinct completion operation rather than an update-any-column path, and it is **once-only** on the same shape as `StopPrincipalInstance`: two paths can observe one call ending.

## D2. Rejected cases, enumerated before the queries

Per `CLAUDE.md`'s rule that invariants and rejected cases are listed before schemas or validators are written:

| Rejected | Why |
| --- | --- |
| Lineage with a gap (Story without Epic) | Violates the prefix chain; caller told which level is missing |
| Tool call claiming another principal's LLM call | Provenance key exists precisely to forbid it |
| Tool call whose lineage differs from its LLM call's | Same key; "same work" is part of the claim |
| Negative tokens or cost | Schema check, refused early with the field named |
| `cost_usd` supplied as a float | Precision loss on a reconciled number |
| Completing an already-completed call | Once-only; returns the recorded completion, idempotent |
| `finished_at` before `started_at` | Nonsense interval; no schema check exists, so the seam owns it |
| Any write naming another organization's principal or lineage | Multi-tenant boundary, as item 4 |
| Metric or event with an empty name | An unnamed metric is unqueryable and silently useless |
| Truncation without a horizon | An unbounded delete is not an operation anyone should be able to invoke by accident |

## D3. Retention-safe truncation

The Audit family is **truncatable by design**, and this is where the phase can lose data it promised to keep. ADR 0021 makes retention **pinning** the mechanism: `retention_pins` names either an Audit artifact or a binary attachment, plus the `pinned_digest`.

Rules:

1. **Truncation is horizon-based and explicit.** `TruncateAuditBefore(ctx, organizationID, before time.Time)` — never "delete all", never a default horizon. Organization-scoped like everything else.
2. **A pinned row is never deleted**, whatever the horizon. The delete is `WHERE ... AND NOT EXISTS (SELECT 1 FROM retention_pins ...)`. This is the SQL backstop; the seam does not pre-filter and hope.
3. **Truncation reports what it kept, not only what it removed.** Returning only a delete count makes "nothing was pinned" and "everything was pinned" look identical. The result carries removed and retained-because-pinned counts.
4. **Pins are checked by the same composite identity the schema uses** — `(pinned_audit_artifact_id, organization_id)` — so a pin in one organization cannot protect or fail to protect a row in another.
5. **`metric_events` and `llm_calls`/`tool_calls` are truncatable on the same horizon but carry no pins today.** Pins target Audit artifacts and attachments only. This is stated rather than assumed, because the obvious next step — "pin an expensive call record" — is a schema change, not a seam change.

**Testing this needs a mutation that item 4 taught.** A truncation test that seeds only unpinned rows passes against a delete with no pin clause at all. Every truncation test seeds **both** pinned and unpinned rows past the horizon, and asserts the pinned one survives *and* the unpinned one does not — otherwise the assertion cannot discriminate. And the pin clause gets a direct-query test, because the seam is not the only thing that could go wrong.

## D4. Transaction boundaries

Item 4 wrapped nearly everything, because nearly everything was multi-statement. Here the default inverts.

- **Single-row writes take no transaction.** A call record is one INSERT. Wrapping it buys nothing and costs a round trip on the hottest path in the system.
- **Batch writes take one.** Importing a run's call records (item 9) is many rows that are meaningless individually; one transaction, and the batch is the unit of failure.
- **Truncation takes one**, and reports its counts from inside it, so the numbers describe one instant rather than two.
- **Completion takes one**, because it is lock-classify-write like every other once-only operation.

The seam exposes batch insert as a distinct method rather than a loop over the single-row one. A loop inside a transaction is a correct-but-slow shape that no caller can improve on; `CopyFrom` is available through pgx and this is the family that will want it. **Whether item 5 uses `CopyFrom` or multi-row INSERT is deferred to implementation** — the interface is the same either way, and choosing on measurement beats choosing now.

## D5. Reads

Deliberately few. ADR 0022 says these are metrics and traces, and item 9's import is the first real consumer; item 7's reporting is where query shapes get chosen against a real question.

Item 5 ships: by principal instance, by Story, by time range, and cost/token aggregates by model — the shapes Phase 1's D9 calibration already needed. Anything else waits for a caller, on the same rule item 3 applied to tables and the registry applies to types.

## Open questions for review

1. **Should `metric_events` be truncatable in item 5, or wait for item 8's backup?** Truncation without a validated restore is a one-way door. My inclination is to ship truncation now with the pin guard, since Audit growth is the pressing problem and restore is item 8's own criterion — but the sequencing is a real choice.
2. **Does `cost_usd` want a shared money type at the seam?** Item 5 needs it, item 7's reporting will, and item 9's import must not round on the way in. A `Decimal` alias over a string is unglamorous and hard to misuse; a `float64` anywhere in the chain silently loses the eighth decimal place.
