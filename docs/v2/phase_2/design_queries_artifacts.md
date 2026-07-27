+++
title = "Design: Artifact And Principal Queries (Item 4)"
edit_date = "2026-07-26"
status = "draft"
summary = "Mini-plan for Phase 2 item 4: typed queries over the artifact and principal-instance families — named transitions with their preconditions in the UPDATE's WHERE clause rather than a preceding read, no generic status write, effective views assembled in Go against RFC 7386's own test vectors, and the MPH seeding set captured at instance creation."
type = "design"
+++

# Design: Artifact And Principal Queries (Item 4)

Status: **draft** — for Codex review before the queries land.

Implements [Phase 2 plan](plan_scope.md) item 4 over the schema from [item 3](design_schema_core.md), under [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) (encoding, acceptance rule, amendment semantics), [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) (lifecycle, MPH) and [ADR 0022](../../adr/0022-v2-data-plane.md) (access discipline).

**Item 4 inherits two obligations that ADR 0028 paid to keep the acceptance rule at the seam rather than in a database trigger.** They are the reason that choice was defensible, so this item either honours them or the choice was wrong in retrospect:

1. **No generic status update is exposed.** Each transition is its own named operation carrying its own preconditions.
2. **Every transition is tested individually** for the precondition it owns — because the risk is a *new* transition added later without the check, which one happy-path test would never catch.

## Decisions

### D1. A concrete `store` package over `gen`, no interface yet

Queries live in `internal/dataplane/queries/*.sql`, one file per family; sqlc generates into `internal/dataplane/gen`. Callers use a hand-written `internal/dataplane/store` package.

The seam exists for a present reason rather than a speculative one: generated code speaks `pgtype.UUID` and `pgtype.Timestamptz`, and letting those spread into item 9's importer and Phase 3's Orchestrator would make every consumer handle driver types to ask a domain question. `store` converts at the boundary (D7) and is where the transition preconditions live.

**No interface in item 4.** ADR 0022 does require a pluggable persistence interface, but it is the seam where *deployment modules* swap, and Phase 2 has exactly one module. An interface with a single implementation and no second caller is the abstraction `CLAUDE.md` says to reject; it costs nothing to extract when the cloud module or a Phase 3 fake gives it a second shape. Flagged as open question 1 in case that reads as under-delivering ADR 0022.

### D2. No generic status update, enforced rather than intended

There is no `UpdateArtifactStatus`. The exposed operations are `AcceptArtifact`, `InvalidateArtifact`, `SupersedeArtifact`, `ArchiveArtifact`, and each carries the preconditions for that transition alone.

Enforced by a test, not a convention: it parses `queries/*.sql`, finds every statement writing `status`, and fails unless the query's sqlc name is in a known set of transitions. A future `-- name: SetArtifactStatus :exec` fails the build rather than passing review on a quiet day. That guard is itself mutation-verified by adding such a query and confirming the failure.

### D3. Preconditions live in the `UPDATE`'s `WHERE`, never in a preceding read

Every transition is a single conditional statement. `AcceptArtifact` is the shape:

```sql
UPDATE management_artifacts a
SET status = 'accepted', accepted_at = now(), reviewer_instance_id = r.reviewer_instance_id
FROM artifact_reviews r, principal_instances p
WHERE a.artifact_id = @artifact_id
  AND a.status = 'draft'
  AND r.artifact_id = a.artifact_id
  AND r.review_digest = a.review_digest      -- the digest actually reviewed
  AND r.decision = 'accepted'
  AND p.principal_instance_id = r.reviewer_instance_id
  AND p.principal_instance_id <> a.author_instance_id   -- non-author
  AND p.kind IN ('agent', 'human')                      -- never a system principal
```

Zero rows affected means a precondition failed, and the store turns that into a typed error naming which.

**Reading first and then writing would be a TOCTOU**, and not a theoretical one: two concurrent accepts, or an accept racing an amendment that moves `review_digest`, would both pass a prior `SELECT` and then write. Putting the conditions in the `WHERE` makes the check and the write the same statement. This is the same reasoning as ADR 0027's serialize-by-the-resource rule, applied where the resource is a row rather than a file.

The digest match is what makes ADR 0028's binding real: a review of superseded content cannot license acceptance, because the row's current `review_digest` no longer equals the one reviewed.

### D4. Effective views are assembled in Go, against RFC 7386's own test vectors

Postgres 18.4 has no RFC 7386 merge-patch function — checked, not assumed — so the choice is a PL/pgSQL implementation or Go. Go, because it is the same language as its tests and because ADR 0028 requires the view be materialised on read rather than stored, so there is no database-side consumer that would benefit.

The algorithm is ~25 lines and the RFC publishes a **test-case table in Appendix A**. Those vectors are the test suite: they are authored by the specification rather than derived from our implementation, so they cannot drift toward whatever we happened to build.

Assembly is: load the original, load its accepted amendments ordered by `amendment_sequence`, apply each patch in order. Draft and rejected amendments are never applied. The schema already guarantees the sequence is total and the chain is flat, so the query relies on those rather than re-checking them.

### D5. Amendment acceptance allocates the sequence, and re-checks the base

Accepting an amendment does two things acceptance of an original does not:

- **Allocates `amendment_sequence`** as `coalesce(max(...), 0) + 1` over that original's accepted amendments, computed inside the same statement. The partial unique index is the backstop rather than the mechanism: under concurrency one transaction loses and retries.
- **Re-checks the base.** ADR 0028 binds an amendment's review to the effective view it was reviewed against, so if that base has moved the amendment requires re-review. The review record carries `base_digest` and `base_sequence`; acceptance compares the stored base against the current effective view and refuses on mismatch with a distinct error, since "re-review needed" is operationally different from "precondition failed".

### D6. The MPH seeding set is written with the instance, not after it

`principal_instance_inputs` rows are written in the same transaction that creates the principal instance. ADR 0021's promise is that "what was this agent given to start?" is always a query; an instance that exists briefly without its inputs makes that promise false for exactly as long as the gap. Each row records the artifact **and the digest as seeded**, so a later comparison against the artifact's current digest shows the seed has moved.

### D7. Driver types stop at the seam

`store` accepts and returns `uuid.UUID`, `time.Time`, `[]byte` and domain structs; `pgtype.UUID`/`pgtype.Timestamptz` do not escape it. Nullable scalars remain pointers, matching what sqlc generates and what the schema means by null (D4 of the schema design). The conversion is mechanical and lives in one file, so a caller never asks whether `.Valid` is set to answer a domain question.

## Testing

- **RFC 7386 Appendix A vectors** for merge patch, verbatim.
- **Effective view** over conflicting amendments (later sequence prevails per field), out-of-order insertion, an amendment that deletes a field with `null`, and a chain containing draft and rejected amendments that must not be applied.
- **Every transition tested individually** for its own precondition, not one happy path: accept without a review record, with a review of a *different* digest, by the author, by a system principal, and from a non-`draft` status. Same shape for invalidate, supersede, archive.
- **The no-generic-status guard**, mutation-verified by adding a generic status query and confirming the failure.
- **Concurrency**: two simultaneous accepts leave exactly one winner; two simultaneous amendment acceptances produce distinct sequences.
- **Seeding set** written atomically with the instance — an instance never observable without its inputs.

Integration-tagged, against the disposable-database harness from item 3 rather than the canonical database.

## Open questions for review

1. **Is deferring ADR 0022's persistence interface to a second implementation right?** (D1.) The ADR mandates the interface; I read it as the module-swap seam, which Phase 2 does not yet exercise, and `CLAUDE.md` rejects single-implementation interfaces without a present reason. The counter is that Phase 3 will build against `store` directly and inherit a refactor.
2. **Should `store` own transaction composition now?** Item 9's slice needs object-write-then-pin-then-row across two stores (ADR 0022's commit order). That could be item 4's `WithTx`-style helper or item 6's problem when the object module exists. I lean to item 6, where the second store is real, but it shapes `store`'s signatures either way and is cheaper to decide now than to retrofit.
