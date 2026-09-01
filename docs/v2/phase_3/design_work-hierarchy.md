+++
title = "Design: Work-Hierarchy Schema And The Dispatch Basis (Item 2)"
edit_date = "2026-09-01"
status = "draft"
summary = "Mini-plan for Phase 3 item 2: work groups keyed one-per-Epic, Story-scoped executions carrying authority rather than configuration, Story dispatch records with an explicit disposition and ADR 0019's two-part dispatch basis, typed Epic and Story dependency graphs serialized under a stable parent lock, and the tool_calls migration that replaces tool_calls_finished_check with an explicit state, a six-value outcome and the persisted canonical requirement set — plus the deferral of runs to item 10, which amends the phase plan."
type = "design"
+++

# Design: Work-Hierarchy Schema And The Dispatch Basis (Item 2)

Status: **draft** — awaiting Codex and DR approval. Follows the Phase 2 precedent
of a design mini-plan for M-sized items
([item 3](../phase_2/design_schema_core.md), [item 4](../phase_2/design_queries_artifacts.md)).

Implements [Phase 3 plan](plan_scope.md) item 2 under
[ADR 0019](../../adr/0019-orchestrator-boundary.md) as amended (the dispatch
basis and its two tests), [ADR 0024](../../adr/0024-intake-and-triage-artifact-contract.md)
(the persisted dependency graphs), [ADR 0030](../../adr/0030-tool-execution-policy-hook.md)
§8 (the `tool_calls` record contract), [ADR 0032](../../adr/0032-agent-execution-contract.md)
(execution identity and authority, and the action state vocabulary),
[ADR 0018](../../adr/0018-v2-work-taxonomy.md) (Work Group), and
[ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) (the reference
shape version identity borrows).

**The governing tension is what this item may not build.** ADR 0032's
[Status Of Decisions](../../adr/0032-agent-execution-contract.md) closes the
binding list at thirteen items and returns the rest — including *"how item 10's
two lifetimes are represented … and the persistence shape of either"* and *"any
persistence family implied solely by one of the above"* — to Phase 3 as design
inputs, to be settled against a real consumer. Item 2 has no consumer. It sits
before items 5, 6 and 8, which are where those consumers arrive.

So this item builds the **spine** the binding rules need in order to be
enforceable, and nothing that a demoted section merely describes. Where this
document and an ADR disagree, the ADR wins.

Item 2 delivers migrations and schema. Typed queries and the seam wiring are
item 3's; only the table shapes land here.

**Item 2 is an L, not the M the plan sized it.** Six new tables, additive changes
to four existing ones, a backfill, a refusing down migration, and cross-table
lineage constraints throughout. It stays **one plan item** — splitting the item
would put the `tool_calls` record contract in a different review from the
executions table it correlates to — but implementation lands as **two migrations
reviewed in sequence**:

| Migration | Contents | Reviewed |
| --- | --- | --- |
| `000021_work_hierarchy` | `work_groups`, `executions`, `story_dispatches`, `dispatch_basis_dependencies`, the two dependency graphs, `management_artifacts`'s two scope keys, and D13's current-basis pointers | Before 000022 begins |
| `000022_tool_call_state` | `tool_calls`: state, outcome, correlation and requirement columns; the constraint replacement; the backfill; the refusing down migration | Before the item closes |

The second depends on the first — `tool_calls.execution_id` references a table
000021 creates — so the order is forced rather than chosen. The plan's size is
amended alongside the `runs` move.

## Decisions

### D1. What item 2 creates, and what it defers

Created here:

| Table | Required by | Consumed by |
| --- | --- | --- |
| `work_groups` | [0018](../../adr/0018-v2-work-taxonomy.md) — the per-Epic unit of execution; [0022](../../adr/0022-v2-data-plane.md) family list | Item 3: dispatch through the seam names the Work Group it dispatches into |
| `executions` | [0019](../../adr/0019-orchestrator-boundary.md) as amended — superseding "the execution's own authority" needs a durable referent; [0032](../../adr/0032-agent-execution-contract.md) binding items 8, 9, 11 | Item 5 (the boundary checks authority), item 9 (cancellation writes it) |
| `story_dispatches` | [0019](../../adr/0019-orchestrator-boundary.md) as amended — the dispatch, its disposition and its basis | Item 9; item 3 writes them |
| `dispatch_basis_dependencies` | [0019](../../adr/0019-orchestrator-boundary.md) test 2 — the basis is a set, not a scalar | Item 9's comparison |
| `epic_dependencies` | [0024](../../adr/0024-intake-and-triage-artifact-contract.md) — "intake persists the full Epic dependency graph" | Item 11 (intake writes it); item 9 |
| `story_dependencies` | [0024](../../adr/0024-intake-and-triage-artifact-contract.md) — the Architect owns "its dependency graph within an Epic"; [0019](../../adr/0019-orchestrator-boundary.md) test 2 | Item 9's comparison; item 10 dispatches dependency-ready Stories |

Altered here, all additive: `management_artifacts` gains two scope keys (D7); `stories`, `epics` and `story_dependencies` gain the current-basis pointers (D13); `tool_calls` gains its state, outcome, correlation and requirement columns and loses `succeeded` (D10, D11).

Deferred, each with the item that first has a caller:

| Family | Required by | Created by |
| --- | --- | --- |
| `runs` | [0022](../../adr/0022-v2-data-plane.md) family list only — **no Accepted definition exists** | Item 10 (see D2) |
| Execution configuration and per-incarnation bindings | [0032](../../adr/0032-agent-execution-contract.md) §2, **demoted** | Item 5/6, against a consumer |
| `epic_dispatches` | [0024](../../adr/0024-intake-and-triage-artifact-contract.md) — a dispatch per dependency-ready Epic | Item 10/11 (see D6) |
| Prompt packs | [0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) | Item 4 |
| Resource reference on a `resource_waiting` attempt | [0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) §5, §8 | Item 7 (see D10) |
| **The requirement *identity* vocabulary** — what keys the requirement set | No ADR defines it (see D10) | Item 5, which owns the gates that emit requirements |
| **`tool_calls.arguments` → ADR 0030 §3's persisted projection** | [0030](../../adr/0030-tool-execution-policy-hook.md) §3 — declared-safe fields, the substituted-input digest, and references for anything large | Item 5, which owns the action schema registry (see D10) |

### D2. `runs` is deferred to item 10, and this amends the plan

`plan_scope.md` item 2 names runs. This document proposes moving them to item 10
(`work-group-lifecycle`), and that amendment is carried in this branch.

**The reason is the phase's own admission rule**: every table traces to an
Accepted ADR *and* a Phase 3 consumer. A run has an ADR *mention* but no
definition, and no consumer.

- **No definition.** `run` appears in the Accepted set once, at
  [ADR 0022](../../adr/0022-v2-data-plane.md) line 35, as the bare phrase "Work
  Groups and runs" in a family list. [ADR 0018](../../adr/0018-v2-work-taxonomy.md)
  defines Work Group in full and never uses `run` as a noun. No ADR states a
  run's lifetime, cardinality, or boundary.
- **No consumer before item 10.** Nothing in items 3 through 9 reads or writes a
  run.

Defining it here would mean a schema mini-plan originating a work-taxonomy
concept that [ADR 0018](../../adr/0018-v2-work-taxonomy.md) owns, then writing
DDL to its own invention in the same item — mechanism reasoned forward from a
model with no consumer to answer to, which is the failure ADR 0032's scope
correction names. `process_build.md` also routes new ADR needs discovered
mid-phase to the backlog rather than into the phase.

This cuts narrowly. **`work_groups` stays** (D3): ADR 0018 defines it and item 3
consumes it. Definition plus consumer is the rule working, not a preference.

**ADR 0029's runs are a different noun.** Its iteration and evidence-bearing
runs (§6) are resource-verification operations *inside* an execution. They are
item 7's and must take a qualified name rather than share a generic `runs`
concept.

### D3. `work_groups`, keyed one-per-Epic

[ADR 0018](../../adr/0018-v2-work-taxonomy.md) line 31 fixes both the contents
and the cardinality: the Work Group is the unit of execution assigned to one
Epic, and "one Work Group per Epic; multiple concurrent Work Groups are
post-MVP."

```text
work_groups
    work_group_id   uuid PRIMARY KEY
    organization_id uuid NOT NULL
    product_id      uuid NOT NULL
    feature_id      uuid NOT NULL
    epic_id         uuid NOT NULL
    created_at      timestamptz NOT NULL DEFAULT now()

    FOREIGN KEY (epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT

    UNIQUE (epic_id, organization_id)                 -- ADR 0018's MVP cardinality
    UNIQUE (work_group_id, epic_id, feature_id, product_id, organization_id)
```

**The columns ADR 0018 lists are not all here**, and that is the same rule as
everywhere else in this item: the prompt pack arrives with item 4, gates with
Phase 5, and the workspace and harness configuration with items 5–7. What lands
now is identity, lineage, and the cardinality constraint.

**The second `UNIQUE` is the composite `story_dispatches` consumes.** Without it
a dispatch could name a Work Group belonging to another Epic — the same defect
migration 000003 designed out by referencing a parent through the whole lineage
tuple, and it must not reappear one level up.

The one-per-Epic key is an **MVP constraint that post-MVP will have to drop**.
Recorded here so its removal is a decision someone makes rather than a
constraint someone is surprised by.

### D4. `executions` carries identity and authority, not configuration

One row per **logical, Story-scoped execution**, which may span several runtime
incarnations. [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md)
§2 is explicit: the Incubator is scoped to the Story execution rather than the
Agent principal, so "agent restart or replacement therefore preserves the work,
and a replacement agent resumes the same execution."

```text
executions
    execution_id         uuid PRIMARY KEY
    organization_id      uuid NOT NULL
    product_id, feature_id, epic_id, story_id  uuid NOT NULL
    story_dispatch_id    uuid NOT NULL
    dispatch_is_accepted boolean NOT NULL DEFAULT true
    authority_state      text NOT NULL       -- 'current' | 'superseded'
    admission_closed_at  timestamptz
    created_at           timestamptz NOT NULL DEFAULT now()

    FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (...) ON DELETE RESTRICT

    -- The constraint that actually excludes an unaccepted dispatch. Without
    -- this FK the constant column below is decoration.
    -- Through the WHOLE Story lineage, not just id + organization. Narrower,
    -- an execution could carry Story B's lineage while naming an accepted
    -- dispatch for Story A in the same organization.
    FOREIGN KEY (story_dispatch_id, dispatch_is_accepted,
                 story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES story_dispatches (story_dispatch_id, is_accepted,
                 story_id, epic_id, feature_id, product_id, organization_id)
        ON DELETE RESTRICT

    UNIQUE (story_dispatch_id, organization_id)
    UNIQUE (execution_id, story_id, epic_id,
            feature_id, product_id, organization_id)   -- tool_calls (D10)
    CHECK (dispatch_is_accepted)
    CHECK (authority_state IN ('current', 'superseded'))
    CHECK (authority_state <> 'superseded' OR admission_closed_at IS NOT NULL)
```

**No `principal_instance_id`.** Principal instances and incarnations are
many-to-one over an execution, and the direction of the association is exactly
what the restart lifecycle settles. Item 2 asserts neither direction.

**No resolved configuration, resource bindings, epochs, or resume tokens.**
Those are [ADR 0032](../../adr/0032-agent-execution-contract.md) §2, under its
own demotion notice.

**`authority_state` and `admission_closed_at` are not redundant.** ADR 0019's
sequence marks authority superseded *and* closes admission together, but the
converse does not hold: a headless block is a forced stop that closes admission
with no dispatch-basis supersession at all
([ADR 0032](../../adr/0032-agent-execution-contract.md) lines 790-793). Closed
admission under current authority is therefore a real state; the implication that
does hold is the CHECK above.

**`dispatch_is_accepted` makes an execution against an unaccepted dispatch
unrepresentable — but only together with its foreign key.** The column is a
constant carrying a `CHECK`; the exclusion comes from the composite FK above
resolving against `story_dispatches`'s generated `is_accepted` column, so the
only dispatch row it can match is one whose disposition is `accepted`. A draft
of this section described the column as load-bearing while omitting the FK,
which left the mutation it claims to fail perfectly insertable. This is the
idiom `management_artifacts_amends_original_fkey` already uses (migration
000006) to make an amendment-of-an-amendment unrepresentable rather than merely
checked, and it is only ever as good as the key it references.

**Cardinality: one dispatch yields zero or one execution.** A refused invocation
"produces **no execution and no terminal result** … It is a dispatch failure"
([ADR 0032](../../adr/0032-agent-execution-contract.md) lines 805-810), while
restart and principal replacement stay within the same logical execution and a
configuration change requires a new dispatch.

### D5. The dispatch carries a disposition, not only a timestamp

A `dispatched_at` alone cannot distinguish the states the ADRs require, and every
zero-execution dispatch would be ambiguous between "not yet answered", "refused"
and "invalidated before it was answered". Two separate requirements land on this
column:

- **ADR 0019** requires pending version-bound dispatches to be invalidated when
  the basis moves, and reissued.
- **ADR 0032** (lines 805-810) requires a handshake failure to be **recorded
  durably** while creating no execution.

```text
story_dispatches
    story_dispatch_id  uuid PRIMARY KEY
    organization_id    uuid NOT NULL
    product_id, feature_id, epic_id, story_id  uuid NOT NULL
    work_group_id      uuid NOT NULL
    dispatched_at      timestamptz NOT NULL DEFAULT now()

    disposition        text NOT NULL   -- 'pending'|'accepted'|'failed'|'invalidated'
    is_accepted        boolean GENERATED ALWAYS AS (disposition = 'accepted') STORED NOT NULL
    settled_at         timestamptz
    failure_code       text            -- stable, machine-readable
    failure_detail     text            -- optional prose, never the discriminator

    story_version_ref  -- D7's quadruple
    epic_version_ref   -- D7's quadruple

    FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (...) ON DELETE RESTRICT
    FOREIGN KEY (work_group_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES work_groups (work_group_id, epic_id, feature_id, product_id, organization_id)
        ON DELETE RESTRICT

    -- Two composite keys, each with a consumer. Omitting either makes the
    -- referring foreign key invalid DDL, not merely unenforced.
    UNIQUE (story_dispatch_id, is_accepted, story_id, epic_id,
            feature_id, product_id, organization_id)                 -- executions (D4)
    UNIQUE (story_dispatch_id, epic_id, feature_id,
            product_id, organization_id)                             -- basis child (D8)

    CHECK (disposition IN ('pending', 'accepted', 'failed', 'invalidated'))
    CHECK ((disposition = 'pending') = (settled_at IS NULL))
    CHECK ((disposition = 'failed')  = (failure_code IS NOT NULL))
    CHECK (failure_detail IS NULL OR failure_code IS NOT NULL)
```

**All three terminal dispositions carry `settled_at`**, which the pending
equivalence gives directly: `accepted`, `failed` and `invalidated` each record
when they settled. A failure carries a **stable `failure_code`** plus optional
detail, so a consumer branches on the code and never parses prose.

**The dispositions are a lifecycle, and the shape constraints do not express
it.** `pending → accepted | failed | invalidated`, and **every terminal
disposition is immutable**. Nothing above stops a `failed` row being set back to
`pending`, which would erase precisely the durable history ADR 0032 requires the
`failed` disposition to preserve.

Enforcement is **item 3's named conditional transitions** — each an
`UPDATE … WHERE disposition = 'pending'` that reports zero rows as a rejected
transition — rather than a generic disposition update. Two reasons for putting it
there rather than in a trigger: this schema uses no triggers anywhere (twenty
migrations, zero `CREATE TRIGGER`), so one here would be a new pattern needing
its own justification; and ADR 0022's access discipline makes the seam the only
writer, so there is no production path that bypasses the named transitions. The
obligation is recorded against item 3 in the testing split below rather than
claimed as an item 2 guarantee.

The Work Group foreign key travels through the whole Epic lineage tuple, so a
dispatch into another Epic's Work Group is unrepresentable rather than merely
wrong.

**What SQL enforces and what the seam does.** `UNIQUE (story_dispatch_id,
organization_id)` on `executions` gives *at most one* execution per dispatch, and
D4's constant column **together with its foreign key** gives *only for an
accepted dispatch*. The remaining half — that an `accepted` dispatch has *at
least* one execution — is cross-table and is a seam invariant, committed in the
same transaction that flips the disposition. Stated rather than implied, because
a reader could otherwise assume the schema carries it.

### D6. Dispatch grains are distinguished by table, not by a discriminator

[ADR 0024](../../adr/0024-intake-and-triage-artifact-contract.md) defines **two**
dispatch grains: one per dependency-ready Epic, into a Work Group, and — under
the amended division of labor — dependency-ready Stories within an Epic. An Epic
dispatch produces no execution, so a single `dispatches` table with a uniqueness
rule on `executions` would be wrong.

The table is therefore named for its grain: **`story_dispatches`**, and D4's
uniqueness is unambiguous by construction rather than by a type column. This
follows D9's reasoning — typed tables keep real foreign keys and lineage tuples,
which a `(kind, id)` shape discards.

**`epic_dispatches` is not created here.** Its first consumer is item 10/11.
It also could not carry a basis if it were: ADR 0019 scopes the two-member
governing set to the Story execution grain and says a future grain binds a
different set "only by stating it in that grain's own dispatch contract; nothing
is added to this one by implication."

### D7. Version and completion identity: the effective view, not the stored payload

A basis row must detect the change its test names. **Test 1 and test 2 detect
different things**, and conflating them was an error in this document's first
draft: an amendment to the governing Story or Epic moves test 1's effective
version set and does *not* touch a predecessor's satisfying completion. Test 2
moves when the selected completion changes, or when that completion's own
effective view does.

**The reference is to an assembled effective view, and `payload_digest` is the
wrong column.** A second-draft error, corrected here: `management_artifacts`
stores each row's *own* `payload_digest` (migration 000006 line 75) — for an
original that is the original bytes, and for an amendment its patch. The
effective view is assembled at read time by `EffectiveView` and digested by
`canonical.DigestJSON` (`internal/dataplane/store/postgres/artifacts.go:1044`).
So `(artifact_id, payload_digest, sequence)` would name bytes that are not the
view the basis was dispatched under.

The correct precedent is already in the schema. `artifact_reviews` cites a moving
base with `base_digest` + `base_sequence` (migration 000008 lines 23-24), under a
`CHECK ((base_digest IS NULL) = (base_sequence IS NULL))`. Each reference here is
that shape, plus the constant `is_amendment` component:

```text
    *_artifact_id        uuid NOT NULL   -- the ORIGINAL, never an amendment
    *_is_amendment       boolean NOT NULL DEFAULT false   -- constant, CHECK (NOT ...)
    *_effective_digest   text NOT NULL   -- CHECK (~ '^[0-9a-f]{64}$')
    *_effective_sequence int  NOT NULL   -- CHECK (>= 0)
```

**Each reference is bound to its own work item, not merely to some artifact.**
An earlier draft constrained only the completion, leaving a Story version
reference free to name another Story's artifact and an Epic reference another
Epic's. All three use the scope column as an FK component:

```text
-- on story_dispatches
FOREIGN KEY (story_version_artifact_id, story_version_is_amendment,
             story_id, organization_id)
    REFERENCES management_artifacts (artifact_id, is_amendment,
                                     scope_story_id, organization_id)
FOREIGN KEY (epic_version_artifact_id, epic_version_is_amendment,
             epic_id, organization_id)
    REFERENCES management_artifacts (artifact_id, is_amendment,
                                     scope_epic_id, organization_id)
```

**Migration 000021 therefore adds two composite keys to `management_artifacts`**,
which is otherwise a table this item only reads:

```text
UNIQUE (artifact_id, is_amendment, scope_story_id, organization_id)
UNIQUE (artifact_id, is_amendment, scope_epic_id,  organization_id)
```

Both are additive and neither weakens an existing constraint: `artifact_id` is
already the primary key, so each is a superset key that adds a referencable
target without admitting a row the table previously rejected.

**Both halves are load-bearing and the sequence is not redundant.** `verifyReviewedBase`
says why in the code: "a no-op amendment still advances the chain, and a later
reviewer must be looking at the same point in it"
(`artifacts.go:1062-1065`). An amendment whose patch leaves the view
byte-identical moves the effective version without moving its digest, so a
digest-only reference would miss exactly the amendment ADR 0019 is named for.

**Database-constrained versus seam-validated**, stated explicitly:

| Property | Enforced by |
| --- | --- |
| The reference names a real artifact in this organization, and an **original** rather than an amendment | Composite FK, via the constant `is_amendment` column |
| The governing Story version is **scoped to this dispatch's Story**; the Epic version to its Epic; the satisfying completion to its predecessor Story | Composite FK, via the two new scope keys above |
| The digest matches the artifact's current effective view | **Seam** — it is a computed value, and a stale reference is the *signal* test 1 and test 2 look for, not a violation to reject at write time |
| The artifact is of the expected **type** | **Seam** — `artifact_type` (000006 line 13) carries no CHECK vocabulary. Per DR's call, validation stays in ADR 0028's code registry and no database vocabulary is introduced |
| The referenced artifact is **accepted** rather than draft | **Seam** — `status` moves over the row's life (`accepted → superseded`), so a foreign key onto it would either forbid a legal transition or enforce nothing |

The last two rows are real gaps, named as gaps. A status column could not
substitute for the digest-and-sequence pair either:
[ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) is explicit that
an accepted amendment does not supersede the original, so a test on `status`
misses every amendment — "the common case and the one this decision is named
for."

### D8. The dispatch basis is two halves in two shapes

**Test 1 — the governing version set — is a fixed pair of column groups** on
`story_dispatches`. For a Phase 3 Story execution the set is "exactly two
members: the effective version of the Story, and the effective version of the
Epic that governs it," named exactly rather than as a floor. Two members is two
column groups, not a child table — a child table would represent the
three-member set the ADR forbids.

**Test 2 — the incoming dependency basis — is a child table**, being "the work
item's own incoming edges: the identities of its predecessors, together with the
effective completions that satisfied them."

```text
dispatch_basis_dependencies
    story_dispatch_id    uuid NOT NULL
    organization_id      uuid NOT NULL
    product_id, feature_id, epic_id  uuid NOT NULL   -- shared with the dispatch
    predecessor_story_id uuid NOT NULL
    completion_*                                     -- D7's four columns, NOT NULL

    PRIMARY KEY (story_dispatch_id, predecessor_story_id)

    FOREIGN KEY (story_dispatch_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES story_dispatches (...) ON DELETE RESTRICT
    FOREIGN KEY (predecessor_story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT
    FOREIGN KEY (completion_artifact_id, completion_is_amendment,
                 predecessor_story_id, organization_id)
        REFERENCES management_artifacts (artifact_id, is_amendment, scope_story_id, organization_id)
        ON DELETE RESTRICT
```

**Carrying the shared lineage is what makes the row provable.** A child holding
only two UUIDs cannot show that the predecessor belongs to the dispatch's own
organization and Epic, nor that the completion is scoped to that predecessor —
all three become true by construction once the lineage columns are shared across
the three foreign keys. This requires `story_dispatches` to expose the matching
composite key.

`completion_*` is non-null because a Story is dispatched only when
dependency-ready, so every predecessor in the basis was satisfied at that moment.

### D9. Two typed dependency tables, and acyclicity under a stable parent lock

[ADR 0024](../../adr/0024-intake-and-triage-artifact-contract.md) requires both
graph grains with different owners: intake persists the Epic graph, the Architect
owns the Story graph within an Epic. Two tables, matching migration 000003's
discipline of referencing a parent by the whole lineage tuple. A polymorphic
`(kind, id)` table would discard exactly that.

```text
story_dependencies
    organization_id, product_id, feature_id, epic_id    -- shared by both endpoints
    successor_story_id, predecessor_story_id

    PRIMARY KEY (organization_id, epic_id, successor_story_id, predecessor_story_id)
    CHECK (successor_story_id <> predecessor_story_id)

    -- Both endpoints, or the shared lineage constrains nothing.
    FOREIGN KEY (successor_story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id)
        ON DELETE RESTRICT
    FOREIGN KEY (predecessor_story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (story_id, epic_id, feature_id, product_id, organization_id)
        ON DELETE RESTRICT

epic_dependencies
    organization_id, product_id, feature_id             -- shared by both endpoints
    successor_epic_id, predecessor_epic_id

    PRIMARY KEY (organization_id, feature_id, successor_epic_id, predecessor_epic_id)
    CHECK (successor_epic_id <> predecessor_epic_id)

    FOREIGN KEY (successor_epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT
    FOREIGN KEY (predecessor_epic_id, feature_id, product_id, organization_id)
        REFERENCES epics (epic_id, feature_id, product_id, organization_id) ON DELETE RESTRICT
```

**Sharing the lineage columns only constrains anything once both endpoints are
foreign keys through them.** A draft showed the columns, the primary key and the
self-edge check, and left both foreign keys as prose — under which a cross-Epic
Story edge stays insertable and the mutation claiming to catch it does not fail.
With both keys present, ADR 0024's "within an Epic" is carried by the schema
rather than by a rule someone remembers. **Epic edges likewise stay within one
Feature**; a cross-Feature dependency would need its own contract rather than a
weakened column.

**Acyclicity is enforced in the Orchestrator, and a bare check-then-insert is a
defect.** Two concurrent transactions adding A→B and B→A each observe an acyclic
graph and both commit, producing a cycle neither could see — the check is not
serializable against its own concurrent writers. The rule, which is
[ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md)'s
applied here: **every mutation of a graph is serialized under the same stable
parent lock** — the Feature row for the Epic graph, the Epic row for the Story
graph — with the recursive check and the mutation inside that one transaction.
The parent is the right key because it is the object the whole graph hangs from
and it does not move; locking the edge rows would not exclude an insert of a row
that does not yet exist.

The self-edge case stays a `CHECK`, since one hop needs no traversal.

### D10. The `tool_calls` migration: an explicit state, six outcomes, and the requirement set

The binding requirement is [ADR 0030](../../adr/0030-tool-execution-policy-hook.md)
§8: the record must distinguish a healthy operator wait, a healthy resource wait,
and an interrupted attempt, "because the watchdog cannot act correctly without
that distinction." Only the naming was returned to Phase 3.

Today's `tool_calls` has two positions — in flight and finished — carried by
`CONSTRAINT tool_calls_finished_check CHECK ((finished_at IS NULL) = (succeeded IS NULL))`
(`000005_calls_and_metrics.up.sql:218`). Settling an attempt therefore requires a
boolean, and `unknown` is neither, so the constraint is **replaced** rather than
added to — recorded identically at ADR 0030 line 648, ADR 0032 line 1578, and
ADR 0019 line 174.

**State** follows ADR 0032's table (lines 836-841): `open`, `operator_waiting`,
`resource_waiting` — none terminal — and `settled`, terminal.

**Outcome is six values, and no single ADR carries all six.** ADR 0032 line 841
gives `succeeded`, `failed`, `denied`, `blocked`, `unknown`; `stale` comes from
ADR 0030 line 443, "the action terminates as stale, and a fresh action is
required." The set is the union, stated here because a reviewer checking either
ADR alone will find five.

The outcome is decided by the **cause of the ending, not by the state it was in**:

| Cause | Outcome |
| --- | --- |
| The effect completed | `succeeded` / `failed` |
| Gate 1 or gate 3 refused it | `denied` |
| Headless: a valid requirement with no declared responder | `blocked` |
| Superseded authority, cancelled wait, or an interrupted resumable wait | `stale` |
| Abandoned mid-execution, effect unknowable | `unknown` |

**A headless action never enters `operator_waiting`.** It is opened and settled
terminally in one transaction, as a denial is. ADR 0032 lines 771-782 is direct:
"nothing will ever answer it, and a state named for a responder that does not
exist is a false record" — and it records an earlier draft that returned `denied`
on the wire, left the action in `operator_waiting`, and recorded the execution
`blocked`, "three descriptions of one event, no two of them agreeing."

**The canonical requirement set is persisted on the attempt**, and its absence on
a row that needs it is a defect the schema should refuse.
[ADR 0030](../../adr/0030-tool-execution-policy-hook.md) line 211: "The
requirement set is persisted in canonical form, ordering-independent, and it is
what gate 3 compares against." [ADR 0032](../../adr/0032-agent-execution-contract.md)
line 1250 persists "attempt, its correlation binding, requirement set,
disposition and wait."

This is **not** what ADR 0030 §3's projection restriction excludes. That
restriction is about the substituted request and its action arguments — ADR 0032
line 1250 says so in the same row: "**Not** the substituted request: ADR 0030 §3
keeps it out of Audit." An earlier draft of this document cited the projection
rule against the requirement set and had it backwards.

The whole column and constraint set migration 000022 adds to `tool_calls`,
written out rather than described — an earlier draft left the state and outcome
columns implicit and the object rule as a comment, which is not a constraint:

```text
ALTER TABLE tool_calls
    ADD COLUMN state        text  NOT NULL DEFAULT 'open',
    ADD COLUMN outcome      text,
    ADD COLUMN execution_id uuid,                  -- D10's correlation
    ADD COLUMN requirement_set        jsonb,
    ADD COLUMN requirement_set_digest text;

-- Vocabulary
CHECK (state IN ('open', 'operator_waiting', 'resource_waiting', 'settled'))
CHECK (outcome IS NULL OR outcome IN
       ('succeeded', 'failed', 'denied', 'blocked', 'stale', 'unknown'))

-- Replaces tool_calls_finished_check, in both directions
CHECK ((state = 'settled') = (outcome     IS NOT NULL))
CHECK ((state = 'settled') = (finished_at IS NOT NULL))

-- The requirement set is a NON-EMPTY OBJECT or absent. Structure enforced,
-- not merely intended.
CHECK ((requirement_set IS NULL) = (requirement_set_digest IS NULL))
CHECK (requirement_set IS NULL OR jsonb_typeof(requirement_set) = 'object')
CHECK (requirement_set IS NULL OR requirement_set <> '{}'::jsonb)
CHECK (requirement_set_digest IS NULL OR requirement_set_digest ~ '^[0-9a-f]{64}$')

-- Applicability
CHECK (state   <>          'operator_waiting' OR requirement_set IS NOT NULL)
CHECK (outcome IS DISTINCT FROM 'blocked'     OR requirement_set IS NOT NULL)

-- Correlation through the whole lineage. On this table the lineage columns
-- are NULLABLE (migration 000005), so MATCH SIMPLE would skip the entire
-- foreign key whenever any of them is null -- and a partially-filled row is
-- exactly how a tool call would come to name another Story's execution.
CHECK (execution_id IS NULL OR
       (story_id IS NOT NULL AND epic_id    IS NOT NULL AND
        feature_id IS NOT NULL AND product_id IS NOT NULL))
FOREIGN KEY (execution_id, story_id, epic_id,
             feature_id, product_id, organization_id)
    REFERENCES executions (execution_id, story_id, epic_id,
             feature_id, product_id, organization_id) ON DELETE RESTRICT
```

The `CHECK` is what makes the foreign key inescapable rather than advisory. This
is the same trap migration 000005 documents against its own lineage columns —
its comment on `lineage_key` says a composite key over nullable lineage "would
be SKIPPED whenever any of them is null (MATCH SIMPLE) — which is the common
case — so the provenance check below would silently not apply." A correlation
that can name the wrong execution is the failure that table already calls worse
than none, "because it reads as evidence."

The empty-object case matters: `{}` is an object and passes `jsonb_typeof`, so
without the third check an `operator_waiting` row could satisfy "has a
requirement set" while recording no requirement at all — the applicability rule
present in form and absent in substance.

**"Canonical" here means an encoding decision, not just a serializer.** The
plane's canonicalizer is RFC 8785 JCS plus SHA-256
(`internal/dataplane/canonical/canonical.go:72-82`), and **JCS does not make an
array into a set** — it sorts object *keys* and leaves array order exactly as
given, so two evaluations collecting the same requirements in different orders
would digest differently and gate 3's set equality would fail spuriously. ADR
0030 line 211 requires the stored form to be ordering-independent, so:

- The requirement set is stored as a **JSON object keyed by requirement
  identity**. Ordering becomes structurally irrelevant because JCS sorts keys,
  and **duplicates become unrepresentable** because an object cannot carry one
  key twice. Neither property needs a sort convention anyone must remember.
- **No ADR defines that identity, and this document previously claimed one did.**
  ADR 0030 §3 requires the set to be persisted in canonical, ordering-independent
  form (line 211) and never names a per-requirement identity field. Defining the
  vocabulary belongs to **item 5**, which owns the gates that emit requirements;
  it is recorded in D1's deferred table rather than invented here. Item 2
  therefore constrains the *structure* — object, non-empty, digest well-formed —
  and treats the keys as opaque, which is all the schema can honestly enforce
  before the vocabulary exists.
- The **seam computes the digest** with `canonical.DigestJSON` over that object
  and never accepts one from a caller. A caller-supplied digest is an assertion
  about bytes the caller also supplied, which is not evidence of anything.

An array plus a documented sort order would also work and is rejected as the
weaker option: it puts the set semantics in a convention every writer must
reproduce, where the object encoding puts it in the data type.

**`execution_id` correlates the action with the execution that admitted it**, and
without it two of ADR 0032's binding items are not computable. Today's row names
a Story and a principal instance, which is neither: draining "the actions this
execution admitted" (binding item 8) cannot be expressed by Story, because a
Story has successive executions, and cannot be expressed by principal instance,
because a replacement principal resumes the same execution (ADR 0029 §2). The
column is nullable — Orchestrator-initiated work is recorded and is not an
agent action under an execution (ADR 0030 §10), and every pre-migration row
predates executions entirely.

**What is *not* fixed here: `arguments` is still a verbatim payload.** ADR 0030
§3 permits only the persisted projection — the fields the action schema declares
safe, the digest of the *substituted* input, and references for anything large —
and `tool_calls.arguments jsonb NOT NULL` (migration 000005) stores whatever it
is handed. Correcting it needs the code-resident action schema registry that
declares which fields are safe, which is **item 5's**, so it is deferred there
explicitly and listed in D1 rather than left as an unscheduled half-migration.
Item 2 moving the column without that registry would be a redaction rule with
nothing to consult.

**`resource_waiting` carries no reference in item 2, and a resource reference is
the wrong field for it anyway.** A provisioning or capacity wait exists *before*
any resource does — that is what it is waiting for — so ADR 0029's resource
reference and instance generation are null for exactly the interval the wait
covers and cannot identify the operation. What item 7 owes is a durable
**provisioning-or-capacity operation identity**, present from the moment the wait
opens, with the resulting resource reference and generation populated only once
one exists. Building either here would be the demoted-mechanism error again.

Until item 7, a `resource_waiting` row is distinguishable *as a wait kind*, which
is exactly ADR 0030 §8's binding requirement and no more. ADR 0030 lines 341-344
separately assign the release rule for this wait to the Phase 3 plan; item 9 owns
it with the watchdog policy.

Replacing the constraint with the equivalence, in both directions:

```text
CHECK ((state = 'settled') = (outcome     IS NOT NULL))
CHECK ((state = 'settled') = (finished_at IS NOT NULL))
```

`succeeded boolean` is backfilled into `outcome` and dropped; the generated
queries and sqlc output are updated in the same item.

### D11. Backfill, and a down migration that refuses rather than lies

**The backfill sets `state`, not only `outcome`.** The `state` column defaults to
`'open'`, so without an explicit update every historical finished row would land
`settled`-in-fact but `open`-in-column and violate the new equivalence on its
first read:

```text
UPDATE tool_calls
   SET state   = 'settled',
       outcome = CASE succeeded WHEN true THEN 'succeeded' ELSE 'failed' END
 WHERE finished_at IS NOT NULL;
```

Rows with `finished_at IS NULL` keep the default `open`. They are historical
in-flight attempts whose process is gone, and **they are left `open` rather than
settled as `unknown`** — settling them would assert a reconciliation nobody
performed, and `open` in no declared wait is precisely what ADR 0030 §8 says a
reconciler reads as *attempted, outcome unknown*.

**The down migration preserves identity, so it refuses on every state the old
shape cannot express — not only on the new outcomes.** Three classes, and an
earlier draft caught only the first:

| Refuses on | Because the old shape would record |
| --- | --- |
| `outcome` in `denied`, `blocked`, `stale`, `unknown` | `denied` round-trips as `failed`, asserting an action the boundary refused was attempted; `unknown` has no boolean image at all |
| `state` in `operator_waiting`, `resource_waiting` | An indistinguishable legacy in-flight row — the healthy wait and the dead process collapse into the same two nulls, which is the exact ambiguity ADR 0030 §8 created this migration to remove |
| `requirement_set IS NOT NULL` | Dropping the column erases it. This bites hardest on a **`succeeded`** row, which the outcome guard waves through while its requirement set — the record of what an operator was asked and answered — disappears silently |
| `execution_id IS NOT NULL` | The same shape once more: a `succeeded` or `failed` row passes every guard above and still loses the correlation binding ADR 0032 line 1250 requires persisted. Identity-preservation has to mean *all* the identity the new columns carry, not only the outcome vocabulary |

The coarse-projection alternative is coherent and is rejected deliberately:
`denied`, `blocked` and `stale` could all map to `succeeded = false`, leaving only
`unknown` unrepresentable. That loses the distinction silently on the way back
up, and it fabricates history in the Audit family, which
[ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) treats as
evidence. A down migration that corrupts is worse than one that refuses, and
refusal puts the decision in front of an operator instead of behind a backfill.

### D12. What item 2 must not foreclose

ADR 0019 says whether the basis closure is one transaction or an authoritative
recheck at admission "is Phase 3's to choose." That choice belongs to **item 9**,
which implements the enforcement. Item 2's obligation is to make the comparison
expressible and to leave both open. Stating the limit here so the boundary is
not crossed quietly.

### D13. The current basis needs a home, or the snapshot compares against nothing

D7 and D8 store what the dispatch was issued under. Item 9 compares that against
**the current basis**, and until now nothing in this design said where the
current basis is read from. The references prove organization, scope, type and
acceptance — none of which identify *which* artifact is governing right now.

Two associations are missing, and neither is derivable:

1. **Which accepted original is a Story's or an Epic's governing artifact.**
   Scope plus type plus `accepted` does not name one. Several accepted originals
   of a type can be scoped to one Story over its life, and under ADR 0021 an
   accepted **amendment** leaves the original `accepted` — it changes the
   effective view without changing the original's status. (Supersession is the
   other path and does mark the old artifact `superseded`; a draft here
   overstated ADR 0021 by describing both as leaving the original accepted.)
2. **Which completion currently satisfies a dependency edge.** Same reason, and
   sharper: "the accepted completion of the predecessor" is not a function, so
   ADR 0019's trigger — "a satisfying completion that is no longer the effective
   one" — has nothing to evaluate.

**Both are explicit pointers.** Item 2 adds them beside the entities they
describe, scope-bound by the same composite keys D7 introduces:

```text
ALTER TABLE stories
    ADD COLUMN governing_artifact_id  uuid,                       -- NULLABLE
    ADD COLUMN governing_is_amendment boolean NOT NULL DEFAULT false;
ALTER TABLE epics
    ADD COLUMN governing_artifact_id  uuid,
    ADD COLUMN governing_is_amendment boolean NOT NULL DEFAULT false;
ALTER TABLE story_dependencies
    ADD COLUMN satisfying_completion_artifact_id  uuid,
    ADD COLUMN satisfying_completion_is_amendment boolean NOT NULL DEFAULT false;

CHECK (NOT governing_is_amendment)              -- and the edge's equivalent
FOREIGN KEY (governing_artifact_id, governing_is_amendment,
             story_id, organization_id)
    REFERENCES management_artifacts (artifact_id, is_amendment,
             scope_story_id, organization_id) ON DELETE RESTRICT
```

**The pointer is nullable; its discriminator must not be.** A Story exists before
its spec is accepted and an edge exists before its predecessor completes — that
is what "not yet dependency-ready" *is* — so `*_artifact_id` is nullable, and a
null there correctly skips the composite foreign key under `MATCH SIMPLE`.

But the discriminator is `NOT NULL DEFAULT false` with its `CHECK`, because a
*nullable* one reopens the same skip from the other side: a non-null
`governing_artifact_id` paired with a null `governing_is_amendment` also skips
the whole key, and with it both the original-only and the scope claims. The
constant column only enforces anything while it is guaranteed present.

**This applies to every reference in this design**, not only D13's — D7's two
version references and the basis child's completion carry the same
`NOT NULL DEFAULT false` discriminator for the same reason. Stated once here
rather than four times, and tested once per site.

**A derivation rule is the alternative and is rejected.** "The latest accepted
original of type X" makes accepting a second artifact silently redefine the
basis, with no record that anything moved and nothing for a reviewer to inspect.
ADR 0019's entire mechanism is that basis changes are *detectable*; deriving the
current basis from a rule that can change under it defeats the detection it
feeds.

**The snapshot must NOT be constrained to equal the pointer.** No foreign key
ties `story_dispatches.story_version_artifact_id` to
`stories.governing_artifact_id`, and none may be added. Divergence between the
two is the signal — it is precisely what test 1 detects — so a constraint
forbidding it would make the observable condition unrepresentable and the
comparison vacuous. Recorded because it is the natural next constraint to reach
for and it would quietly destroy the mechanism.

**Changing a pointer is a dispatch-basis transition**, not an ordinary update.
ADR 0019: "the moment the changed dispatch basis becomes authoritative, the old
authority of every execution it affects is already unusable." So repointing a
governing artifact or a satisfying completion must linearize with superseding the
affected executions' authority. Item 2's obligation is to make that reachable:
each pointer lives on **one row**, so a single-row lock is available to whichever
mechanism item 9 chooses, and D12's choice between one transaction and an
authoritative recheck at admission stays open. The linearization itself is item
9's, and is listed among its obligations rather than claimed here.

## Testing

Against a real ephemeral plane, per the phase's testing rule. Every guard below
gets a **defect-shaped mutation** (`process_build.md`): the mutation must
falsify the named claim for the named reason, and a mutant that dies for another
reason proves nothing.

**Item 2 lands schema, so item 2's tests are constraint tests.** A draft listed
guards this item cannot honestly test — atomic dispatch creation, effective-view
comparison, canonical requirement handling, serialized graph mutation — all of
which are application behaviour that does not exist until items 3, 5 and 9. The
only way to "test" them here would be a raw-SQL reenactment of logic no
production path executes, which demonstrates that the reenactment agrees with
itself. They are obligations, and they are recorded against their implementing
items rather than claimed here.

### Item 2 — constraint tests

Each is an insert or update that the database must reject, run against a real
ephemeral plane. The defect-shaped requirement is that removing the named
constraint makes the statement **succeed**.

| Guard | Statement that must be rejected |
| --- | --- |
| One Work Group per Epic | A second `work_groups` row for one Epic |
| Dispatch cannot borrow another Epic's Work Group | A dispatch whose `work_group_id` belongs to a different Epic |
| Story edge cannot cross Epics | A `story_dependencies` row whose predecessor is in another Epic |
| Epic edge cannot cross Features | The same, one level up |
| No self-edge | An edge with equal endpoints, in both tables |
| Superseded implies closed admission | `authority_state = 'superseded'` with `admission_closed_at` null |
| One execution per dispatch | A second execution for one `story_dispatch_id` |
| No execution without an accepted dispatch | An execution against a `pending`, a `failed`, and an `invalidated` dispatch — three statements, since one passing proves nothing about the others |
| Dispatch shape | `pending` carrying `settled_at`; a terminal disposition without it; `failed` without `failure_code`; `failure_detail` without `failure_code` |
| Governing reference bound to its own work | A dispatch whose `story_version_artifact_id` is scoped to another Story; the same for the Epic reference |
| Basis row cannot cross tenants or lineage | A basis row whose predecessor is in another organization; one whose predecessor is in another Epic; one whose completion is scoped to a different Story |
| Reference names an original, not an amendment | Any of the three references pointing at an amendment row |
| Settled iff outcome / iff finished | `settled` with null `outcome`; a non-settled row carrying one; the same pair against `finished_at` |
| Outcome vocabulary | An outcome outside the six; a state outside the four |
| Requirement set present where required | `operator_waiting` with a **null** requirement set; `blocked` with a **null** requirement set. (A draft wrote the second as "`blocked` with one", which is the valid case — the inversion would have made the test assert the opposite of the rule) |
| Requirement set is a non-empty object | An **array**-valued requirement set; an **empty object** `{}`; a `requirement_set` with a null digest and the converse |
| Digest well-formed | A `requirement_set_digest` that is not 64 lowercase hex — short, uppercase, and non-hex |
| Execution bound to its dispatch's Story | An execution carrying Story B's lineage that references an accepted dispatch for Story A in the same organization |
| Action correlates to its own Story's execution | A `tool_calls` row whose `execution_id` names an execution in another organization; and one carrying Story B's lineage while naming Story A's execution |
| Correlation cannot escape through null lineage | A `tool_calls` row with a non-null `execution_id` and a partially-null lineage tuple — the `MATCH SIMPLE` skip |
| Discriminator cannot escape through null | Set a pointer's `*_is_amendment` to null with a non-null artifact id — rejected by `NOT NULL`, which is what keeps the composite key from being skipped |
| Pointer names an original | `stories.governing_artifact_id` set to an amendment row; the same for the Epic and edge pointers |
| Pointer is scope-bound | `stories.governing_artifact_id` set to an artifact scoped to another Story; the edge's completion pointer to an artifact scoped to a non-predecessor |
| Snapshot may diverge from the pointer | **Inverse test**: repoint `stories.governing_artifact_id` while a dispatch holds the old snapshot. This must **succeed** — a failure means someone added the constraint D13 forbids, and the detection mechanism is gone |
| State backfill | After migrating a fixture with finished and unfinished rows, no row violates the settled equivalences: every `finished_at IS NOT NULL` row is `settled` with an outcome, every other row is `open` |
| Down migration refuses | Eight runs: one per new outcome (`denied`, `blocked`, `stale`, `unknown`), one per declared wait (`operator_waiting`, `resource_waiting`), one on a **`succeeded`** row carrying a requirement set, and one on a **`succeeded`** row carrying an `execution_id` — the last two both pass the outcome guard and still lose identity |

### Obligations assigned to later items

Recorded here because this design creates them, tested where the code lands.

| Obligation | Item | Note |
| --- | --- | --- |
| Dispatch creation writes both version references and the **complete** basis in one transaction | 3 | Otherwise the plane can hold a basis that never existed as a set — a partial write is indistinguishable from a real dispatch under a different contract |
| Terminal dispositions are immutable; only `pending →` transitions succeed | 3 | Named conditional updates; a zero-row result is a rejected transition, not a no-op |
| Referenced artifact is of the expected type and is accepted | 3 | The two seam-validated rows of D7's table |
| Seam computes the requirement digest; callers never supply one | 5 | With the keyed-object encoding, so reordering inputs yields an identical digest |
| Basis detects a version move | 9 | Amend the governing Epic; the stored digest and sequence no longer match the effective view |
| Basis detects a **no-op** amendment | 9 | An amendment leaving the view byte-identical: the digest still matches and only the sequence moves. This is the case a digest-only reference fails, and the reason D7 keeps both halves |
| Basis detects an added predecessor and a re-satisfied edge | 9 | An already-satisfied predecessor inserted; a predecessor's completion amended |
| Acyclicity holds under concurrent opposing writers | 9 | Two transactions inserting A→B and B→A with the interleaving **forced**, not raced. Note this is a database serialization anomaly, not a Go data race: `-race` cannot observe it, and a passing `-race` run is not evidence about it |
| Repointing a governing artifact or satisfying completion linearizes with superseding affected executions' authority | 9 | D13. The window to prove absent is the one ADR 0019 names: a basis component already authoritative while an affected execution still holds usable authority |
| The requirement identity vocabulary, and the digest's invariance under reordering | 5 | Item 5 defines the keys; the test is that two evaluations collecting the same requirements in different orders produce the same digest |
| `arguments` becomes ADR 0030 §3's persisted projection | 5 | Declared-safe fields plus the substituted-input digest. Until then the column is verbatim and non-conforming, which is recorded rather than silently carried |

The last row is the correction that matters most in this section. A draft
proposed proving the acyclicity guard "under the race detector", which would have
produced a green run that says nothing about write skew between two Postgres
transactions.

## Open questions — resolved

All three were carried to review rather than assumed, and DR settled them.
Recorded with their answers so a later reader sees a decision rather than a
silence.

1. **May item 2 add composite keys to `management_artifacts`**, a table it
   otherwise only reads? **Yes** — migration 000021 adds both the Story-scope and
   Epic-scope keys (D7). This is what makes all three artifact references'
   **identity, original-not-amendment, and scope** properties
   database-constrained, and it is why those mutations appear in item 2's
   constraint tests rather than in item 9's obligations. It does **not** upgrade
   the whole reference: expected **type**, **acceptance**, and effective-view
   **currency** remain seam-validated, as D7's table records.
2. **Should `artifact_type` gain a database CHECK vocabulary** so "this is a
   Story completion" is enforceable in SQL? **No** — validation stays in ADR
   0028's code registry, and no vocabulary is introduced into the schema. The
   type check is therefore seam-validated by decision rather than by omission,
   and D7's table says so.
3. **Do `invalidated` and `failed` both need `settled_at`, and should `failed`
   carry a structured code?** **Both yes** — every non-pending disposition
   records `settled_at`, and a failure carries a stable `failure_code` with
   optional `failure_detail` (D5). Consumers branch on the code; the detail is
   never the discriminator.
