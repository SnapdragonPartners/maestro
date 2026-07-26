+++
title = "Design: Core Schema And Migrations (Item 3)"
edit_date = "2026-07-26"
status = "draft"
summary = "Mini-plan for Phase 2 item 3: golang-migrate conventions and the core DDL applied from empty — ADR 0028's envelope as columns over a jsonb payload, Management and Audit in separate families, scope-conditional lineage enforced in SQL, app-generated UUIDv7 identifiers, text-plus-CHECK over Postgres enums, and the table-by-table trace to an Accepted ADR and a Phase 2 consumer."
type = "design"
+++

# Design: Core Schema And Migrations (Item 3)

Status: **draft** — for Codex review before any SQL merges. Follows the Phase 1 and item 2 precedent of a design mini-plan for M-sized items.

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

**Always force to the last version that actually applied — `V-1`, not `V`.** That is the true state of the database once the wrapper has rolled the DDL back. What follows depends on whether the migration has ever succeeded anywhere:

- **Never successfully applied anywhere** (the normal case: it fails from empty, so it fails for everyone). Force to `V-1`, correct the file **in place**, run `migrate up`. This holds even if the migration is already merged — the append-only rule protects *applied history*, and a migration that has never applied has none. There is no database anywhere whose state would diverge from the corrected file, which is the only thing append-only exists to prevent. Say so in the PR that corrects it.
- **Applied successfully somewhere, failing elsewhere** (data- or environment-dependent). Now the file is genuinely frozen: editing it would leave two databases claiming the same version with different schemas. Force to `V-1`, leave the file alone, and land a **new** migration that reaches the intended state from either starting point.
- **Locally, in development**, the fastest route is usually neither: `make dataplane-reset && make dataplane-up` rebuilds from empty. The plane is disposable by design, and re-deriving from migration 000001 is the same path CI takes.

The distinction that matters is *whether any database has applied it*, not whether the commit merged. Append-only is a rule about divergent state, and this is the one place its purpose has to be reasoned about rather than followed mechanically.

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

**Losing the foreign key does not mean losing the guarantee — the seam takes it over.** Dropping the FK claim without replacing it, as an earlier draft did, would have left `scope_id` as a `uuid` pointing at nothing in particular: a dangling scope is undetectable, and every lineage query silently returns less than it should. So the persistence seam **validates the scope on write**:

- `scope_type` selects the target table from the mapping above, and the write is rejected unless a row with that `scope_id` exists in it.
- Validation happens **in the same transaction as the insert**, so a target deleted concurrently cannot slip between the check and the write.
- `scope_type = 'benchmark'` is **rejected outright until item 9 creates the table**. Not silently tolerated as "unvalidatable" — a scope whose target does not exist is exactly the dangling reference this rule prevents, and the temporary refusal is what stops item 9's own work from landing rows the model cannot resolve.

This is the same division as ADR 0028's payload validation (§2): the database enforces what is true of a row in isolation, and the seam enforces what needs a lookup the database cannot express. It is weaker than an FK — nothing stops a direct `psql` insert — and that is an accepted consequence of a polymorphic scope, not an oversight. Tested per scope type, including the benchmark refusal.

**Lineage is scope-conditional, and that is enforced in SQL.** ADR 0018 requires non-null lineage at every level the scope covers, which is a real constraint rather than a convention: a story-scoped artifact with a null `epic_id` is unqueryable by the lineage joins the model promises. A `CHECK` encodes it — `scope_type = 'story'` implies all four lineage columns are present, `'epic'` implies three, and so on. Getting this wrong is not caught by any test that only inserts well-formed rows, which is exactly why it belongs in the database.

**Management and Audit are separate tables**, per ADR 0021: opposite retention postures, and Audit volume dwarfs Management volume. The Audit table carries no `reviewer_instance_id` and no review linkage at all — ADR 0021 says Audit data is not review-bearing, and a nullable column invites someone to read meaning into it.

### D6. Which invariants live in the database

The line: **the database enforces what is true of a row in isolation; the seam enforces what requires judgment or a registry.**

In SQL — shape, vocabulary, referential integrity **where a column names one table** (lineage, authorship, organization; *not* the polymorphic `scope_id`, see D5), scope-conditional lineage, at-most-one relationship link, digest format, amendment sequence uniqueness, each table's fixed category, and the amendment chain's flatness (an artifact with `amends_artifact_id` set may not itself be amended).

At the seam — payload validation against the code-resident type registry (ADR 0028 §2: the registry is code, so the database cannot consult it), **scope-target existence** (D5: `scope_id` is polymorphic, so no FK can express it), and **the acceptance rule**: an artifact reaches `accepted` only with a completed review record whose `review_digest` matches, by a distinct principal of kind agent or human.

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
- **Scope validation is tested per scope type**, including that `benchmark` is refused while its table does not exist.
- **Every exposed status transition is tested for its own precondition** (D6), individually rather than as one happy path, because the risk is a transition added later without the check.
- **The ADR-and-consumer table is checked at review**, not by a test; it is a claim about intent, which no test can verify.

Integration-tagged and Docker-dependent, consistent with item 2.

## Open questions — resolved

Both answered by Codex, 2026-07-25.

1. **Is the acceptance rule better as a trigger?** **No — keep enforcement at the seam, but do not claim equivalence to a database constraint.** The concrete obligations that replace the constraint are in D6: no generic status update is exposed, and every transition is tested individually. The wording that treated a test as "the same guarantee" has been corrected.
2. **Should the Audit artifact table be partitioned from the start?** **Not yet**, as proposed. Revisit when there is volume data to choose a partition key from; ADR backlog candidate 2 (online backup) is the natural place for that conversation to resurface.
