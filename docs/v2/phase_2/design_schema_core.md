+++
title = "Design: Core Schema And Migrations (Item 3)"
edit_date = "2026-07-26"
status = "live"
summary = "Mini-plan for Phase 2 item 3: golang-migrate conventions and the core DDL applied from empty — ADR 0028's envelope as columns over a jsonb payload, Management and Audit in separate families, scope-conditional lineage enforced in SQL, app-generated UUIDv7 identifiers, text-plus-CHECK over Postgres enums, and the table-by-table trace to an Accepted ADR and a Phase 2 consumer."
type = "design"
+++

# Design: Core Schema And Migrations (Item 3)

Status: **live** — Accepted by Codex and DR, 2026-07-26, after four review rounds. Follows the Phase 1 and item 2 precedent of a design mini-plan for M-sized items.

Implements [Phase 2 plan](plan_scope.md) item 3 under [ADR 0022](../../adr/0022-v2-data-plane.md) (stack, schema families, access discipline) and [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) (the encoding this turns into columns), with shapes from [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) and hierarchy from [ADR 0018](../../adr/0018-v2-work-taxonomy.md).

**This item writes DDL, not design.** ADR 0022 claims Phase 2's schema is mechanical because every family traces to an Accepted ADR; ADR 0028 then fixed the encoding. What remains genuinely open is narrower than it looks — type choices, constraint placement, and which families exist yet — and that is what this document settles. Where it and an ADR disagree, the ADR wins.

Item 3 delivers migrations and the schema. **Typed queries are item 4**; only the sqlc *configuration* lands here, so item 4 has something to generate from.

## Decisions

### D1. Migration conventions

`golang-migrate`, file-per-migration under `internal/dataplane/migrations/`, named `NNNNNN_slug.up.sql` / `.down.sql` with a zero-padded sequential prefix.

- **Applied from empty, always.** There is no v1 migration path (roadmap D7), so migration 000001 assumes nothing.
- **Append-only after merge**, already ratified as Phase 2 plan decision 4: a merged migration is never edited; corrections are new migrations. Cheap now, load-bearing the moment anyone other than us has applied one.
- **Down migrations are written**, and are expected to be exercised only in development. They are cheap while the schema is young, they make local iteration reversible, and writing them at authoring time is far easier than reconstructing them later. They are explicitly *not* a production rollback story — a down migration that drops a table destroys data, so recovery in anger is restore-from-backup, not `migrate down`.
- **One logical change per migration.** Not one table: a table plus the indexes and constraints that make it correct belong together, because a migration that leaves a table briefly unconstrained is a migration that can be interrupted into an invalid state.
- **Every migration wraps itself in `BEGIN`/`COMMIT`, explicitly.** golang-migrate does **not** wrap Postgres migrations in a transaction — its own Postgres tutorial says so, and an earlier draft of this document claimed the opposite. Without the explicit wrapper, a migration that fails halfway leaves the schema half-applied *and* the version marked dirty, which is the worst combination to recover from. Reviewers should treat a migration file missing `BEGIN`/`COMMIT` as a defect.

  Note what the wrapper does and does not buy: the DDL rolls back, but golang-migrate's version row is written outside it, so a failed migration still leaves a **dirty version**. The wrapper turns "half a schema and a dirty version" into "no schema change and a dirty version" — a known state rather than an unknown one. Nothing here needs `CONCURRENTLY`; if that ever changes, it is an explicit, commented exception that must *not* be wrapped.

#### Recovering from a dirty version

`migrate force V` **only rewrites metadata — it runs no SQL**. An earlier draft of this document said to fix the file and "force the version", which is actively dangerous: forcing to the version that just failed records it as applied without ever having run it, so `migrate up` skips it forever and the schema silently lacks whatever it contained.

The runbook below **does not carve an exception into the approved append-only rule**. An earlier draft did — it allowed editing a merged migration that had never applied — which a design document has no standing to do; Phase 2 plan decision 4 is approved, and changing it requires a plan amendment, not a paragraph here. It turns out no exception is needed.

**Force to the last version that actually applied — never to the one that just failed.** With the `BEGIN`/`COMMIT` wrapper the DDL has rolled back, so the database really is at `V-1`, and `force V-1` is the only value that states the truth.

Three cases, exhaustively:

**A. The migration fails from empty, before merge.** CI applies migrations from empty on every PR (`dataplane-up`), so this is caught while the file is still unmerged and editable: the PR is red, fix the file. This is the common case by a wide margin, and — as case C explains — it is the mechanism that keeps the uncomfortable case out of reach.

**B. A dirty version on a developer's local plane.** `force V-1`, then either fix the still-unmerged file and re-run, or — usually faster — `make dataplane-reset && make dataplane-up` to rebuild from empty. The plane is disposable by design, and rebuilding takes the same path CI takes.

**C. A merged migration that turns out to be broken.** The file is frozen — it is never edited, and this document grants no exception to that. What is available depends entirely on one question, which an earlier draft did not ask:

**Does `V` still succeed from empty?**

- **Yes** (it succeeds from empty, but failed on some existing database's data or environment). Forward-fix is safe and is the rule. New installations run `V` cleanly and then `V+1`, so nothing is stranded. The stuck database has two routes: repair its data and re-run `V`, or record `V` as **deliberately skipped** — `migrate force V`, which marks it applied without running it — after which `V+1` applies. The skip is the step most easily missed, because `force V-1` looks like the whole answer and silently leaves the database retrying a migration that cannot succeed there. If the skip route is taken, `V+1` must carry the whole of `V`'s intent and be correct both on databases that ran `V` and on those that skipped it.

- **No** (it fails from empty). **There is no repair inside the append-only rule.** A forward migration cannot help: `migrate up` from empty still stops at `V`, so every new checkout and every CI run is broken, permanently, no matter what `V+1` contains. The only fixes change the content at version `V` — reverting and re-landing included, which is a content change however it is packaged — and that requires an **explicit amendment to Phase 2 plan decision 4**, approved by both reviewers. This document does not pre-authorise one.

**That second case should be unreachable, and keeping it so is a property of CI rather than of discipline.** `dataplane-up` applies migrations from empty on every PR, so a migration that fails from empty cannot go green, and a merged migration has therefore always succeeded from empty at least once. **CI's from-empty run is what makes append-only survivable** — it is not merely a test of the schema, it is the thing that guarantees a forward fix always exists. If that job is ever skipped, disabled, or allowed to fail, the append-only rule loses its escape hatch and the next broken migration becomes an incident requiring a plan amendment rather than a commit.

### D2. `dataplane-up` applies migrations

Confirmed by DR: `make dataplane-up` runs `migrate up` after the stack is healthy. It is the common pattern, and the alternative — a healthy plane with no schema, waiting for a second command — makes the "one command from a clean checkout" criterion false.

The usual objection to migrate-on-start is concurrency: several instances racing to migrate the same database. It does not apply here, twice over. The **lifecycle lock** from item 2 already serializes `up` across processes on this machine, and golang-migrate takes a **Postgres advisory lock** of its own, so even a caller that bypassed the launcher cannot interleave. Belt and braces, and the belt was already there.

A separate `make dataplane-migrate` target runs migrations alone, for the ordinary case of iterating on a migration against a stack that is already up. **It takes the lifecycle lock like every other operation** — it mutates the same data plane, and a migration running against a plane that `reset` is concurrently emptying is precisely the interleaving item 2's lock exists to prevent. Adding a lifecycle operation that skips the lock would reintroduce the race one item after closing it.

`migrate up` is idempotent — at the latest version it is a no-op — so item 2's idempotency guarantee survives, including the CI assertion that a second `dataplane-up` neither replaces nor restarts a container.

### D3. Identifiers: application-generated UUIDv7

Every primary key is a `uuid`, generated by the **application** as **UUIDv7** (`github.com/google/uuid`, already a dependency; verified to produce v7).

- **UUID over bigserial**: ADR 0022 anticipates cloud mode and multi-user operation, and identifiers that require a round trip to allocate make artifact graphs awkward to assemble — an artifact's MPH signature references its inputs' identifiers, and knowing an ID before insert keeps that a single write.
- **v7 over v4**: v7 is time-ordered, so index locality and insertion behaviour resemble a sequence rather than scattering across the B-tree. Audit families will be the largest tables in the system and are written constantly.
- **Application-generated over `uuidv7()` in Postgres**: the pinned Postgres 18.4 has the function, but generating in Go means the caller holds the identifier before the write, which is what the seam needs for cross-store ordering (ADR 0022: object first, pin recorded, row last — the row's identity is known while the object is being written).

Identifiers are never derived from content. Content digests are separate columns (D4), because an artifact's identity must survive an amendment that changes its payload.

### D4. Type choices

- **`text` with a `CHECK` constraint, not Postgres `ENUM`**, for `status`, `artifact_category`, `scope_type`, principal `kind`, and review `decision`. ADR 0021 fixes these vocabularies and permits Phase 2 to *extend* them; extending a `CHECK` is an ordinary migration, while `ALTER TYPE ... ADD VALUE` carries transaction restrictions and has no removal path at all. The vocabularies are small and stable — the flexibility matters more than the two bytes.
- **`timestamptz` everywhere, never `timestamp`.** A naive timestamp in a system that will run in CI, on laptops, and eventually in a cloud region is a latent bug with no upside.
- **`jsonb`, not `json`, for payloads** — indexable and normalised on write. ADR 0028 makes JSON the canonical payload format; `jsonb` does not preserve key order or duplicate keys, which is irrelevant because **digests are computed over the canonical JCS form before storage**, never over what Postgres echoes back.
- **Digests as `text` with a `CHECK (value ~ '^[0-9a-f]{64}$')`.** `bytea` is half the size and worse at every debugging moment that matters; at Maestro's scale the 32 bytes are noise. The CHECK is what stops a mixed-case or prefixed digest from ever landing, which is the failure that would silently break comparison.
- **`NOT NULL` is the default posture.** Nullable columns are for facts genuinely absent — `reviewer_instance_id` before review, `stop_time` on a running instance — not for fields nobody got round to filling.

### D5. The artifact envelope as columns

Directly from ADR 0028 §1, which fixed both the field list and the rule that envelope fields are never duplicated in the payload:

| Column | Type | Notes |
| --- | --- | --- |
| `artifact_id` | `uuid` PK | UUIDv7 (D3) |
| `organization_id` | `uuid` NOT NULL | FK; multi-user lineage carried now (see below) |
| `user_id` | `uuid` NOT NULL | FK → users; the accountable human (see below) |
| `artifact_type` | `text` NOT NULL | governed vocabulary; validated against the code registry on write |
| `artifact_category` | `text` NOT NULL | `CHECK (artifact_category = 'management')` — the table's own category, pinned |
| `status` | `text` NOT NULL | `draft` \| `invalidated` \| `accepted` \| `superseded` \| `archived` |
| `scope_type`, `scope_id` | `text`, `uuid` NOT NULL | artifacts attach to a scope, never assume an Epic; polymorphic, see below |
| `product_id`, `feature_id`, `epic_id`, `story_id` | `uuid` NULL | denormalised lineage, FK'd; scope-conditional constraint below |
| `author_instance_id` | `uuid` NOT NULL | FK → principal instances |
| `reviewer_instance_id` | `uuid` NULL | set on review completion |
| `amends_artifact_id`, `supersedes_artifact_id`, `replaces_artifact_id` | `uuid` NULL | at most one populated (CHECK) |
| `amendment_sequence` | `int` NULL | monotonic per original, assigned on acceptance |
| `accepted_at` | `timestamptz` NULL | set with the accepted status |
| `schema_version` | `int` NOT NULL | payload schema version |
| `summary` | `text` NOT NULL | one line; the artifact-row UI and triage hook |
| `payload` | `jsonb` NOT NULL | canonical JSON |
| `payload_digest`, `review_digest` | `text` NOT NULL | 64-hex (D4) |
| `created_at` | `timestamptz` NOT NULL | |

**`amendment_sequence` and `accepted_at` are required by ADR 0021, not optional bookkeeping.** The effective view is "original plus accepted amendments **in sequence order**, later prevailing on conflict", so without a stored sequence there is no total order and the effective view is undefined — and ADR 0028 binds an amendment's review to the base it applied to, which needs the sequence point to be a fact rather than an inference. Both are null until acceptance, and constrained: `amendment_sequence` is non-null exactly when the artifact is an accepted amendment, with `UNIQUE (amends_artifact_id, amendment_sequence)` making the order total by construction rather than by convention.

**Multi-user lineage is carried now — both halves of it.** ADR 0022 requires organization *and user* lineage from the start, *so team mode never requires a data migration*; an earlier draft of this document carried only the organization, which would have left the backfill it was meant to avoid. Adding it later means touching every table that matters.

`user_id` is the **accountable human**, which is a different question from `author_instance_id` (the principal that produced the artifact) and is not derivable from it: an agent-authored artifact has an agent author and a human on whose behalf the work is being done. That human is what pillar 15's user attribution in artifact and Epic views needs, and reconstructing it later by walking lineage to a Feature's submitter is exactly the migration this column exists to avoid.

- On **Management** artifacts it is `NOT NULL`. ADR 0021's accountability rule already guarantees one exists: every Management artifact has an accountable agent or human principal, and every chain of agent work traces to a human who asked for it.
- On **Audit** artifacts it is nullable. System principals emit exhaust — startup metrics, scheduler ticks — that genuinely precedes or outlives any user's action, and forcing a value there would mean inventing one.

Local mode populates both from the default organization and default user, mirroring the default Product. Nothing enforces authorization on them in Phase 2; they carry lineage, not policy.

**The Audit table is not this table with a different category.** Per ADR 0021, Audit artifacts are *born final and have no lifecycle*, so the Audit table has **no `status`, no `accepted_at`, no amendment or supersession links, no `review_digest`, and no `reviewer_instance_id`** — carrying a Management status vocabulary on rows that can never move through it would invite readers to interpret a value that means nothing. It keeps identity, organization and scope lineage, `author_instance_id` (any principal kind, including system), `payload`, `payload_digest`, `created_at`, and `CHECK (artifact_category = 'audit')`. Each table pins its own category, so a row can never land in the wrong family.

**`scope_id` is polymorphic and therefore has no foreign key** — correcting D6's blanket referential-integrity claim, which was too broad. The referential integrity that carries query weight lives in the **lineage** columns, which are real FKs. `scope_type` determines what `scope_id` names, and the mapping is:

| `scope_type` | `scope_id` references | Table exists |
| --- | --- | --- |
| `organization` | organizations | item 3 |
| `product` | products | item 3 |
| `feature` / `epic` / `story` | features / epics / stories | item 3 |
| `benchmark` | benchmark runs | **item 9**, with the import that first needs it |

The `benchmark` scope is in the vocabulary from item 3 but has no target table until item 9 creates one alongside the vertical slice that imports runner results — the created-when-there-is-a-caller rule working as intended. A CHECK ties `scope_type = 'benchmark'` to null work-hierarchy lineage, since a benchmark artifact belongs to no Epic.

**Real foreign keys, one per scope type — an exclusive arc.** Three drafts got this wrong before landing here, and the errors are worth keeping visible because each looked correct: first claiming referential integrity a polymorphic column cannot have; then a same-transaction seam check, which is not a guarantee at all (a plain `SELECT` takes no lock, so a concurrent transaction can delete the target and commit, and nothing prevents a later delete); then a `scopes` supertable whose foreign keys **point the wrong way**.

That last one is the instructive failure. With entity → `scopes` and artifact → `scopes`, both references point *into* the supertable, so deleting an epic removes only the referencing row and leaves its scope row behind — the artifact still resolves, to a scope whose entity no longer exists. Nothing stops a `scopes` row being inserted with no entity at all. The design claimed "deleting a scoped entity is blocked by the artifact's reference", and it simply was not: the artifact referenced `scopes`, not the epic. **The deletion test this document already specifies would have failed against it**, which is the useful part — the test was right and the schema was wrong.

The resolution is the exclusive-arc pattern, with no supertable:

```sql
-- on each artifact table, one nullable FK per scope type
scope_organization_id uuid REFERENCES organizations (organization_id) ON DELETE RESTRICT,
scope_product_id      uuid REFERENCES products      (product_id)      ON DELETE RESTRICT,
scope_feature_id      uuid REFERENCES features      (feature_id)      ON DELETE RESTRICT,
scope_epic_id         uuid REFERENCES epics         (epic_id)         ON DELETE RESTRICT,
scope_story_id        uuid REFERENCES stories       (story_id)        ON DELETE RESTRICT,

-- exactly one is set, and it agrees with scope_type
CHECK (num_nonnulls(scope_organization_id, scope_product_id,
                    scope_feature_id, scope_epic_id, scope_story_id) = 1),
CHECK ((scope_type = 'epic') = (scope_epic_id IS NOT NULL)),  -- and so on per type

-- one queryable scope identifier, derived rather than stored twice
scope_id uuid GENERATED ALWAYS AS (
  COALESCE(scope_organization_id, scope_product_id,
           scope_feature_id, scope_epic_id, scope_story_id)) STORED
```

Every guarantee is now the database's, and each is a real foreign key rather than a claim about one:

- **The scoped entity must exist** — an ordinary FK, checked on insert and maintained thereafter.
- **Deleting a scoped entity with artifacts is blocked** by `ON DELETE RESTRICT` on that same FK. This is the guarantee the supertable version advertised and did not have.
- **`scope_type` cannot disagree with what the artifact points at**, by the paired CHECKs.
- **`scope_id` cannot drift from the typed column**, because it is generated from it rather than written alongside it.
- A direct `psql` insert is bound by exactly the same rules, which matters because a repair script is how this kind of inconsistency actually arrives.

The **benchmark scope stays self-enforcing, and more simply than before**: there is no `scope_benchmark_run_id` column until item 9 creates the benchmark runs table and adds it, so `scope_type = 'benchmark'` cannot satisfy the exactly-one CHECK and is rejected by the schema. No seam rule, no supertable row, nothing to remember.

The cost is one column per scope type on each artifact table, and a migration to add a column when a scope type is added. That is a real cost and it is the right one: scope types come from a governed ADR vocabulary that changes rarely, and the alternative is trading a compile-time-shaped guarantee for a runtime convention. The seam ends up with **no** scope obligation at all, which is the outcome to prefer — an obligation nobody has to uphold cannot be forgotten.

**Lineage is scope-conditional, and that is enforced in SQL.** ADR 0018 requires non-null lineage at every level the scope covers, which is a real constraint rather than a convention: a story-scoped artifact with a null `epic_id` is unqueryable by the lineage joins the model promises. A `CHECK` encodes it — `scope_type = 'story'` implies all four lineage columns are present, `'epic'` implies three, and so on. Getting this wrong is not caught by any test that only inserts well-formed rows, which is exactly why it belongs in the database.

**Management and Audit are separate tables**, per ADR 0021: opposite retention postures, and Audit volume dwarfs Management volume. The Audit table carries no `reviewer_instance_id` and no review linkage at all — ADR 0021 says Audit data is not review-bearing, and a nullable column invites someone to read meaning into it.

### D6. Which invariants live in the database

The line: **the database enforces what is true of a row in isolation; the seam enforces what requires judgment or a registry.**

In SQL — shape, vocabulary, referential integrity including the scope, via one real foreign key per scope type (D5), scope-conditional lineage, at-most-one relationship link, digest format, amendment sequence uniqueness, each table's fixed category, and the amendment chain's flatness (an artifact with `amends_artifact_id` set may not itself be amended).

At the seam — payload validation against the code-resident type registry (ADR 0028 §2: the registry is code, so the database cannot consult it), and **the acceptance rule**: an artifact reaches `accepted` only with a completed review record whose `review_digest` matches, by a distinct principal of kind agent or human.

That last one is the interesting call, and review settled it: **enforcement stays at the seam, but it is not equivalent to a database constraint and this document will not pretend it is.** A trigger was rejected because the author/reviewer distinctness rule reaches across principal kinds in a way that makes for fragile trigger logic, and because a trigger rewrites an actionable application error into a constraint violation surfacing three layers away.

The price of that choice is paid in two specific obligations, not in a general assurance:

- **No generic status update is exposed.** There is no `UpdateArtifactStatus`-shaped query. Each transition — accept, invalidate, supersede, archive — is its own named operation carrying its own preconditions, so "set status to accepted" is not something a caller can express in passing.
- **Every exposed transition is tested**, individually, for the precondition it owns. Coverage is per-transition rather than one test of the happy path, because the risk here is a *new* transition added later without the check, which a single test would never notice.

A database constraint would make the invariant impossible to violate; this makes it impossible to violate *accidentally through the seam*, and says so plainly.

### D7. Families created now versus reserved

Phase 2 plan decision 1: a family is created in the migration that first has a caller, and every table traces to an Accepted ADR **and** a Phase 2 consumer.

**Created by item 3:** organizations, users, products, repositories (+ the many-to-many with a designated primary Product), features, epics, stories, principal instances, artifacts (Management), artifacts (Audit), review records, retention pins, tool calls, LLM calls, metric events, audit events, binary attachment references.

**Created later in Phase 2, by the item that consumes them** — not here, and an earlier draft of this document wrongly listed them as item 3's:

- **configuration records and the secrets vault → item 7** (`config-secrets`), which the approved Phase 2 plan assigns them to along with the typed queries and the locked-plane failure path. Creating their tables here would have split one coherent slice across two items for no reason, and contradicted an approved plan.
- **benchmark runs → item 9**, with the vertical slice that first imports into them (see D5).

**Reserved by name, not created in Phase 2 at all:** Work Groups and runs (Phase 3), prompt packs (Phase 3, backlog candidate 5), gates (Phase 5), knowledge items (Phase 6), skills/patterns (Phase 5/6).

The Phase 2 schema will therefore be visibly *smaller* than ADR 0022's sixteen-family list. That is the rule working, not an omission — and the item's deliverable includes a table mapping every created table to its ADR and its consumer, so the claim is checkable at review rather than asserted.

### D8. sqlc placement

DR had no strong preference; this follows the Phase 2 plan.

- `internal/dataplane/migrations/` — golang-migrate SQL, the schema's source of truth.
- `internal/dataplane/queries/` — sqlc's input `.sql` files (item 4 fills these).
- `internal/dataplane/gen/` — sqlc output, **committed**, so a clean checkout builds without codegen tooling installed. The directory name says "generated" at every import site, which is the point: nothing here is hand-edited.
- `sqlc.yaml` at the repository root, next to the other tool configs.

Engine `postgresql`, driver `pgx/v5`. Item 4 will likely add a thin hand-written seam above `gen` rather than exposing generated types across the codebase, but that is item 4's call to make with real call sites in front of it.

## Testing

- **Migrations apply from empty in CI**, extending the existing `dataplane-up` job — which now covers migrations for free, since `up` runs them.
- **Up-then-down-then-up** returns to the same schema, as the check that down migrations are real rather than decorative.
- **Constraint tests over deliberately malformed rows**: bad digest casing, a story-scoped artifact missing `epic_id`, two relationship links set at once, a status outside the vocabulary, an artifact inserted into the wrong category table, a duplicate `(amends_artifact_id, amendment_sequence)` pair, and a benchmark-scoped artifact carrying Epic lineage. These are the assertions that matter, because a schema is only as good as what it *refuses* — and every one of them is invisible to a test that inserts only valid rows.
- **Scope integrity is tested per scope type**: an artifact pointing at a non-existent entity is refused; **deleting a scoped entity that has artifacts is blocked rather than orphaning them**; a `scope_type` disagreeing with the populated column is refused; and `scope_type = 'benchmark'` is refused while no such column exists. The deletion case is the one that caught the previous design, so it stays first-class rather than incidental.
- **Every exposed status transition is tested for its own precondition** (D6), individually rather than as one happy path, because the risk is a transition added later without the check.
- **The ADR-and-consumer table is checked at review**, not by a test; it is a claim about intent, which no test can verify.

Integration-tagged and Docker-dependent, consistent with item 2.

## Open questions — resolved

Both answered by Codex, 2026-07-25.

1. **Is the acceptance rule better as a trigger?** **No — keep enforcement at the seam, but do not claim equivalence to a database constraint.** The concrete obligations that replace the constraint are in D6: no generic status update is exposed, and every transition is tested individually. The wording that treated a test as "the same guarantee" has been corrected.
2. **Should the Audit artifact table be partitioned from the start?** **Not yet**, as proposed. Revisit when there is volume data to choose a partition key from; ADR backlog candidate 2 (online backup) is the natural place for that conversation to resurface.
