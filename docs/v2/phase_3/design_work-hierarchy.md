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

Deferred, each with the item that first has a caller:

| Family | Required by | Created by |
| --- | --- | --- |
| `runs` | [0022](../../adr/0022-v2-data-plane.md) family list only — **no Accepted definition exists** | Item 10 (see D2) |
| Execution configuration and per-incarnation bindings | [0032](../../adr/0032-agent-execution-contract.md) §2, **demoted** | Item 5/6, against a consumer |
| `epic_dispatches` | [0024](../../adr/0024-intake-and-triage-artifact-contract.md) — a dispatch per dependency-ready Epic | Item 10/11 (see D6) |
| Prompt packs | [0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) | Item 4 |
| Resource reference on a `resource_waiting` attempt | [0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) §5, §8 | Item 7 (see D10) |

### D2. `runs` is deferred to item 10, and this amends the plan

`plan_scope.md` item 2 names runs. This document proposes moving them to item 10
(`work-group-lifecycle`), and that amendment is carried in this branch.

**The reason is the phase's own admission rule**: every table traces to an
Accepted ADR *and* a Phase 3 consumer. A run has neither.

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

    UNIQUE (story_dispatch_id, organization_id)
    CHECK (dispatch_is_accepted)
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
unrepresentable.** It is a constant column carrying a `CHECK`, paired with the
generated column on `story_dispatches` in D5, so the composite foreign key can
only resolve against a dispatch whose disposition is `accepted`. This is the
idiom `management_artifacts_amends_original_fkey` already uses (migration
000006) to make an amendment-of-an-amendment unrepresentable rather than merely
checked.

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
    failure_reason     text

    story_version_ref  -- D7's triple
    epic_version_ref   -- D7's triple

    FOREIGN KEY (story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES stories (...) ON DELETE RESTRICT
    FOREIGN KEY (work_group_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES work_groups (work_group_id, epic_id, feature_id, product_id, organization_id)
        ON DELETE RESTRICT

    UNIQUE (story_dispatch_id, is_accepted, organization_id)
    CHECK ((disposition = 'pending') = (settled_at IS NULL))
    CHECK ((disposition = 'failed')  = (failure_reason IS NOT NULL))
```

The Work Group foreign key travels through the whole Epic lineage tuple, so a
dispatch into another Epic's Work Group is unrepresentable rather than merely
wrong.

**What SQL enforces and what the seam does.** `UNIQUE (story_dispatch_id,
organization_id)` on `executions` gives *at most one* execution per dispatch, and
D4's constant column gives *only for an accepted dispatch*. The remaining half —
that an `accepted` dispatch has *at least* one execution — is cross-table and is
a seam invariant, committed in the same transaction that flips the disposition.
Stated rather than implied, because a reader could otherwise assume the schema
carries it.

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
that triple:

```text
    *_artifact_id        uuid NOT NULL   -- the ORIGINAL, never an amendment
    *_is_amendment       boolean NOT NULL DEFAULT false   -- constant, CHECK (NOT ...)
    *_effective_digest   text NOT NULL   -- CHECK (~ '^[0-9a-f]{64}$')
    *_effective_sequence int  NOT NULL
```

**Both halves are load-bearing and the sequence is not redundant.** `verifyReviewedBase`
says why in the code: "a no-op amendment still advances the chain, and a later
reviewer must be looking at the same point in it"
(`artifacts.go:1062-1065`). An amendment whose patch leaves the view
byte-identical moves the effective version without moving its digest, so a
digest-only reference would miss exactly the amendment ADR 0019 is named for.

**Database-constrained versus seam-validated**, stated explicitly:

| Property | Enforced by |
| --- | --- |
| The reference names a real artifact in this organization, and an **original** rather than an amendment | Composite FK against `management_artifacts (artifact_id, is_amendment, organization_id)` |
| The satisfying completion is **scoped to the predecessor Story** | Composite FK — requires adding `UNIQUE (artifact_id, is_amendment, scope_story_id, organization_id)` to `management_artifacts` in this migration |
| The digest matches the artifact's current effective view | **Seam** — it is a computed value; a stale reference is the signal, not a violation |
| The artifact is of the completion / Story-spec / Epic-spec **type** | **Seam** — `artifact_type` (000006 line 13) carries no CHECK vocabulary, and inventing one here would preempt ADR 0028 |

The last row is a real gap and is named as one rather than papered over. A status
column could not substitute:
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
    completion_*                                     -- D7's triple, NOT NULL

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
```

Sharing the Epic columns between both endpoints makes a cross-Epic Story edge
unrepresentable, which is ADR 0024's "within an Epic" carried by the schema
rather than by a rule someone remembers. `epic_dependencies` has the same shape
one level up, sharing `feature_id` — **Epic edges stay within one Feature**, and
a cross-Feature dependency would need its own contract rather than a weakened
column.

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

```text
    requirement_set        jsonb   -- canonical, ordering-independent
    requirement_set_digest text    -- CHECK (~ '^[0-9a-f]{64}$'); gate 3's set equality

    CHECK ((requirement_set IS NULL) = (requirement_set_digest IS NULL))
    CHECK (state <> 'operator_waiting' OR requirement_set IS NOT NULL)
    CHECK (outcome IS DISTINCT FROM 'blocked' OR requirement_set IS NOT NULL)
```

The digest is stored beside the canonical form because gate 3's test is set
equality against what gate 1 recorded, and a digest makes that comparison cheap
and total; the canonical JSON is what makes the digest well-defined.

**`resource_waiting` carries no reference in item 2.** What such a row needs to
be actionable is the identity of the provisioning or capacity operation, which is
ADR 0029's resource reference plus instance generation — item 7's shape, and
building it here would be the demoted-mechanism error one more time. Until then a
`resource_waiting` row is distinguishable *as a wait kind*, which is exactly ADR
0030 §8's binding requirement and no more. ADR 0030 lines 341-344 separately
assign the release rule for this wait to the Phase 3 plan; item 9 owns it with
the watchdog policy.

Replacing the constraint with the equivalence, in both directions:

```text
CHECK ((state = 'settled') = (outcome     IS NOT NULL))
CHECK ((state = 'settled') = (finished_at IS NOT NULL))
```

`succeeded boolean` is backfilled into `outcome` and dropped; the generated
queries and sqlc output are updated in the same item.

### D11. Backfill, and a down migration that refuses rather than lies

Existing rows: `succeeded = true → 'succeeded'`, `false → 'failed'`. Rows with
`succeeded IS NULL` and no `finished_at` are historical in-flight attempts whose
process is gone. **They are left in `open`, not settled as `unknown`** —
settling them would assert a reconciliation nobody performed, and `open` in no
declared wait is precisely what ADR 0030 §8 says a reconciler reads as
*attempted, outcome unknown*.

**The down migration preserves outcome identity, so it aborts on all four new
values** — `denied`, `blocked`, `stale` and `unknown` — not on three of them. An
earlier draft singled out three and was inconsistent: `denied` survives a
round-trip only as `failed`, which asserts that an action the boundary refused
was attempted and failed.

The alternative policy is coherent and is rejected deliberately: `denied`,
`blocked` and `stale` all *could* map to `succeeded = false` as a truthful coarse
projection, leaving only `unknown` unrepresentable. That loses the distinction
silently on the way back up, and it fabricates history in the Audit family, which
[ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) treats as
evidence. A down migration that corrupts is worse than one that refuses, and
refusal puts the decision in front of an operator instead of behind a backfill.

### D12. What item 2 must not foreclose

ADR 0019 says whether the basis closure is one transaction or an authoritative
recheck at admission "is Phase 3's to choose." That choice belongs to **item 9**,
which implements the enforcement. Item 2's obligation is to make the comparison
expressible and to leave both open. Stating the limit here so the boundary is
not crossed quietly.

## Testing

Against a real ephemeral plane, per the phase's testing rule. Every guard below
gets a **defect-shaped mutation** (`process_build.md`): the mutation must
falsify the named claim for the named reason, and a mutant that dies for another
reason proves nothing.

| Guard | Mutation that must fail |
| --- | --- |
| One Work Group per Epic | Insert a second `work_groups` row for one Epic |
| Dispatch cannot borrow another Epic's Work Group | Insert a dispatch whose `work_group_id` belongs to a different Epic |
| Story edge cannot cross Epics | Insert a `story_dependencies` row whose predecessor is in another Epic |
| Epic edge cannot cross Features | The same, one level up |
| No self-edge | Insert an edge with equal endpoints |
| Superseded implies closed admission | Set `authority_state = 'superseded'` leaving `admission_closed_at` null |
| One execution per dispatch | Insert a second execution for one `story_dispatch_id` |
| No execution without an accepted dispatch | Insert an execution against a `pending`, `failed` and `invalidated` dispatch |
| Dispatch lifecycle | `pending` with `settled_at`; `failed` without `failure_reason` |
| Basis row cannot cross tenants or lineage | Insert a basis row whose predecessor is in another organization, and one whose completion is scoped to a different Story |
| Settled iff outcome / iff finished | `settled` with null `outcome`; a non-settled row carrying one; the same pair against `finished_at` |
| Outcome vocabulary | Insert an outcome outside the six |
| Requirement set present where required | `operator_waiting` with a null requirement set; `blocked` with one |
| Basis detects a version move | Accept an amendment to the governing Epic; the stored `epic_effective_digest`/`_sequence` no longer match the effective view |
| Basis detects a **no-op** amendment | Accept an amendment whose patch leaves the view byte-identical; the digest still matches and only the sequence moves — the test the digest alone would fail |
| Basis detects an added predecessor | Insert an already-satisfied predecessor edge; the basis set and the current edge set differ |
| Basis detects a re-satisfied edge | Amend a predecessor's completion; the stored completion reference no longer matches |
| Acyclicity survives concurrency | Two concurrent transactions inserting A→B and B→A; without the parent lock both commit and a cycle exists. Run under the race detector, with the interleaving forced rather than hoped for |
| Down migration refuses | Run `down` with each of `denied`, `blocked`, `stale`, `unknown` present; each must abort, not null the column |

The concurrency mutation and the three basis mutations are the ones that matter
most: the basis tests cannot be written at all if the dependency tables are
absent, which is the argument for building them here, and the acyclicity test is
the one a single-threaded suite passes while the defect is present.

## Open questions

1. **Adding `UNIQUE (artifact_id, is_amendment, scope_story_id, organization_id)`
   to `management_artifacts`** (D7) touches a table item 2 otherwise only reads.
   It buys a database-enforced scope check on the completion reference. If a
   reviewer prefers item 2 not to alter migration 000006's table, the property
   drops to seam-validated and should be recorded as such rather than assumed.
2. **`artifact_type` has no CHECK vocabulary** (D7), so "this artifact is a Story
   completion" is seam-validated. Introducing the vocabulary here would preempt
   ADR 0028; leaving it is a stated gap. Confirming which is preferred is worth a
   reviewer's explicit call rather than my assumption.
3. **Whether `invalidated` and `failed` both need `settled_at`** (D5), or whether
   `failed` should carry a structured reason code rather than free text. ADR 0032
   requires the failure recorded durably but does not fix its shape.
