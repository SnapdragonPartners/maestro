+++
title = "Schema Table Inventory: ADR And Consumer"
edit_date = "2026-07-27"
status = "live"
summary = "Every table created by Phase 2 item 3, traced to the Accepted ADR that requires it and the Phase 2 item that consumes it — the checkable form of the reserved-by-name rule, plus the families deliberately not created and where they land instead."
type = "inventory"
+++

# Schema Table Inventory: ADR And Consumer

The [Phase 2 plan](plan_scope.md) decision 1 says a family is created in the migration that first has a caller, and that **every table traces to an Accepted ADR and a Phase 2 consumer**. That is only a rule if someone can check it, so this is the check: one row per table, with the ADR that requires it and the item that uses it.

It exists because [ADR 0022](../../adr/0022-v2-data-plane.md) claims Phase 2's DDL is *mechanical* — "schema review is conformance checking, not design". A reviewer can only perform conformance checking against something enumerable.

**The Phase 2 schema is deliberately smaller than ADR 0022's sixteen-family list.** That is the rule working, not an omission; the families that are absent are listed at the bottom with the item or phase that creates them.

## Created by item 3

| Table | Required by | Consumed by |
| --- | --- | --- |
| `organizations` | [0022](../../adr/0022-v2-data-plane.md) multi-user boundaries | Item 4 typed queries; every table's lineage |
| `users` | [0022](../../adr/0022-v2-data-plane.md) ("users belong to organizations") | Item 4; the `user_id` lineage on every major record |
| `products` | [0018](../../adr/0018-v2-work-taxonomy.md) taxonomy; [0022](../../adr/0022-v2-data-plane.md) products/repos family | Item 4; artifact scope and lineage |
| `repositories` | [0022](../../adr/0022-v2-data-plane.md) (forge-independent repo records) | Item 4; Epic branch ownership ([0023](../../adr/0023-v2-branch-strategy.md)) |
| `product_repositories` | [0022](../../adr/0022-v2-data-plane.md) as amended (many-to-many, one primary Product) | Item 4; the primary-Product designation |
| `features` | [0018](../../adr/0018-v2-work-taxonomy.md) (incl. wrapper Features) | Item 4; artifact lineage |
| `epics` | [0018](../../adr/0018-v2-work-taxonomy.md) | Item 4; artifact lineage |
| `stories` | [0018](../../adr/0018-v2-work-taxonomy.md) | Item 4; artifact lineage; call attribution |
| `principal_instances` | [0021](../../adr/0021-artifacts-and-principal-instances.md) (agent/human/system principals, MPH) | Item 4; artifact authorship and review |
| `principal_instance_inputs` | [0021](../../adr/0021-artifacts-and-principal-instances.md) (the MPH seeding set) | Item 4 — without it "what was this agent given to start?" is not a query |
| `llm_calls` | [0022](../../adr/0022-v2-data-plane.md) (LLM calls as metrics/trace) | Item 5 call queries; item 9 imports runner cost records. **Amended by migration 000011** (item 5): gained `succeeded` / `error_message`, since a completed zero-token call and a failed one were otherwise indistinguishable on the row. |
| `tool_calls` | [0022](../../adr/0022-v2-data-plane.md) (the atomic Audit **action** unit) | Item 5; artifact provenance |
| `metric_events` | [0022](../../adr/0022-v2-data-plane.md) metrics family | Item 5; item 9 imports runner metrics |
| `audit_events` | [0021](../../adr/0021-artifacts-and-principal-instances.md) Audit enumeration | Item 5 |
| `management_artifacts` | [0021](../../adr/0021-artifacts-and-principal-instances.md) + [0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) encoding | Item 4 — the phase's central deliverable |
| `audit_artifacts` | [0021](../../adr/0021-artifacts-and-principal-instances.md) (separate storage family, opposite retention posture) | Item 5; item 9 imports runner records |
| `artifact_reviews` | [0021](../../adr/0021-artifacts-and-principal-instances.md) review records; [0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) digest binding | Item 4 — acceptance is impossible without it |
| `binary_attachments` | [0022](../../adr/0022-v2-data-plane.md) (digest references; binaries never in rows) | Item 6 object module; item 9's object write |
| `retention_pins` | [0021](../../adr/0021-artifacts-and-principal-instances.md) retention pinning | Item 5 Audit truncation; item 9's pin |

Nineteen tables, plus golang-migrate's own `schema_migrations`.

## Not created by item 3

Each has an Accepted ADR behind it and no Phase 2 consumer *yet*, so each waits for the item that first needs it. Listing them is the other half of the rule: a reviewer expecting ADR 0022's full family list should see these as scheduled rather than forgotten.

| Family | Required by | Created by |
| --- | --- | --- |
| Configuration records | [0022](../../adr/0022-v2-data-plane.md) (config resolved into the plane) | **Item 7** (`config-secrets`), per the approved plan |
| Secrets vault | [0022](../../adr/0022-v2-data-plane.md) + the [project-folder spike](../phase_0/spike_project-folder.md) | **Item 7** |
| Benchmark runs | [0025](../../adr/0025-golden-stories-and-benchmark-runner.md); the `benchmark` artifact scope | **Item 9**, with the import that first needs it |
| Work Groups and runs | [0018](../../adr/0018-v2-work-taxonomy.md), [0022](../../adr/0022-v2-data-plane.md) | Phase 3 (Work Group runtime) |
| Prompt packs | [0022](../../adr/0022-v2-data-plane.md); [backlog candidate 5](../notes_adr-backlog.md) | Phase 3 |
| Gates | [0022](../../adr/0022-v2-data-plane.md); roadmap pillar 8 | Phase 5 |
| Knowledge items | [0022](../../adr/0022-v2-data-plane.md) ("Phase 6 fills this out") | Phase 6 |
| Skills / patterns | [0022](../../adr/0022-v2-data-plane.md); roadmap pillar 10 | Phase 5/6 |

**The `benchmark` scope is self-enforcing in the meantime.** There is no `scope_benchmark_run_id` column until item 9 adds it alongside the table, so an artifact claiming that scope cannot satisfy the exactly-one-scope constraint and is refused by the schema — no seam rule to write, remember, or test.

## How to keep this honest

- A migration that adds a table adds a row here, in the same PR. A table with no row is the reviewable defect the plan's rule describes.
- The "consumed by" column is a claim about a *Phase 2* item. If the honest answer is a later phase, the table belongs in the second list, not the first.
- This is checked at review rather than by a test: it is a claim about intent, and no test can verify that a table is needed.
