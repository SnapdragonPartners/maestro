+++
title = "Design: Work-Hierarchy Schema And The Dispatch Basis (Item 2)"
edit_date = "2026-09-01"
status = "draft"
summary = "Mini-plan for Phase 3 item 2: work groups, Story-scoped executions carrying authority rather than configuration, Story dispatch records carrying ADR 0019's two-part dispatch basis, typed Epic and Story dependency graphs, and the tool_calls migration that replaces tool_calls_finished_check with an explicit state and a six-value outcome — plus the deferral of runs to item 10, which amends the phase plan."
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
shape completion identity borrows).

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
| `story_dispatches` | [0019](../../adr/0019-orchestrator-boundary.md) as amended — the dispatch and its basis | Item 9; item 3 writes them |
| `dispatch_basis_dependencies` | [0019](../../adr/0019-orchestrator-boundary.md) test 2 — the basis is a set, not a scalar | Item 9's comparison |
| `epic_dependencies` | [0024](../../adr/0024-intake-and-triage-artifact-contract.md) — "intake persists the full Epic dependency graph" | Item 11 (intake writes it); item 9 |
| `story_dependencies` | [0024](../../adr/0024-intake-and-triage-artifact-contract.md) — the Architect owns "its dependency graph within an Epic"; [0019](../../adr/0019-orchestrator-boundary.md) test 2 | Item 9's comparison; item 10 dispatches dependency-ready Stories |

Deferred, each with the item that first has a caller:

| Family | Required by | Created by |
| --- | --- | --- |
| `runs` | [0022](../../adr/0022-v2-data-plane.md) family list only — **no Accepted definition exists** | Item 10 (see D2) |
| Execution configuration and per-incarnation bindings | [0032](../../adr/0032-agent-execution-contract.md) §2, **demoted** | Item 5/6, against a consumer |
| `epic_dispatches` | [0024](../../adr/0024-intake-and-triage-artifact-contract.md) — a dispatch per dependency-ready Epic | Item 10/11 (see D5) |
| Prompt packs | [0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) | Item 4 |

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

This cuts narrowly. **`work_groups` stays**: it is defined concretely at
[ADR 0018](../../adr/0018-v2-work-taxonomy.md) line 31 — agents, workspace,
branch, prompt pack, harness configuration, review and evidence policy, and
gates, one per Epic — and item 3 consumes it. Definition plus consumer is the
rule working, not a preference.

**ADR 0029's runs are a different noun.** Its iteration and evidence-bearing
runs (§6) are resource-verification operations *inside* an execution. They are
item 7's and must take a qualified name rather than share a generic `runs`
concept.

### D3. `executions` carries identity and authority, not configuration

One row per **logical, Story-scoped execution**, which may span several runtime
incarnations. [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md)
§2 is explicit: the Incubator is scoped to the Story execution rather than the
Agent principal, so "agent restart or replacement therefore preserves the work,
and a replacement agent resumes the same execution."

```text
executions
    execution_id        uuid PRIMARY KEY
    organization_id     uuid NOT NULL
    story lineage tuple                     -- all NOT NULL; Story-grained
    story_dispatch_id   uuid NOT NULL
    authority_state     text NOT NULL       -- 'current' | 'superseded'
    admission_closed_at timestamptz
    created_at          timestamptz NOT NULL
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
admission under current authority is therefore a real state. The implication
that *does* hold is a constraint:

```text
CHECK (authority_state <> 'superseded' OR admission_closed_at IS NOT NULL)
```

**Cardinality: one dispatch yields zero or one execution.** A refused invocation
"produces **no execution and no terminal result** … It is a dispatch failure"
([ADR 0032](../../adr/0032-agent-execution-contract.md) lines 805-810), and
restart or agent replacement preserves the execution without a new dispatch. So
`UNIQUE (story_dispatch_id, organization_id)`. This is deliberately falsifiable
and cheap to relax if a consumer needs one-to-many.

### D4. The dispatch basis is two halves in two shapes

[ADR 0019](../../adr/0019-orchestrator-boundary.md) as amended binds a dispatch
to a basis with a test on each half. The shapes differ because the halves do.

**Test 1 — the governing version set — is a fixed pair of columns.** For a
Phase 3 Story execution the set is "exactly two members: the effective version
of the Story, and the effective version of the Epic that governs it," named
exactly rather than as a floor, because "one implementation drops a governing
input and the Epic case returns, another adds the whole graph and every edit
becomes a mass cancellation." Two members is two column groups on
`story_dispatches`, not a child table — a child table would represent a
three-member set the ADR forbids.

**Test 2 — the incoming dependency basis — is a child table.** It is "the work
item's own incoming edges: the identities of its predecessors, together with the
effective completions that satisfied them," which is a set of unbounded size.

```text
story_dispatches
    story_dispatch_id  uuid PRIMARY KEY
    organization_id    uuid NOT NULL
    story lineage tuple                     -- all NOT NULL
    work_group_id      uuid NOT NULL
    dispatched_at      timestamptz NOT NULL
    story_version_ref                       -- D6's three columns
    epic_version_ref                        -- D6's three columns

dispatch_basis_dependencies
    story_dispatch_id      uuid NOT NULL
    predecessor_story_id   uuid NOT NULL
    satisfying_completion  -- D6's three columns, NOT NULL
    PRIMARY KEY (story_dispatch_id, predecessor_story_id)
```

`satisfying_completion` is non-null because a Story is dispatched only when
dependency-ready, so every predecessor in the basis was satisfied at that moment.

### D5. Dispatch grains are distinguished by table, not by a discriminator

[ADR 0024](../../adr/0024-intake-and-triage-artifact-contract.md) defines **two**
dispatch grains: one per dependency-ready Epic, into a Work Group, and — under
the amended division of labor — dependency-ready Stories within an Epic. An Epic
dispatch produces no execution, so a single `dispatches` table with
`UNIQUE (dispatch_id)` on `executions` would be wrong.

The table is therefore named for its grain: **`story_dispatches`**, and the
uniqueness above is unambiguous by construction rather than by a type column.
This follows D7's reasoning — typed tables keep real foreign keys and lineage
tuples, which a `(kind, id)` shape discards.

**`epic_dispatches` is not created here.** Its first consumer is item 10/11.
It also could not carry a basis if it were: ADR 0019 scopes the two-member
governing set to the Story execution grain and says a future grain binds a
different set "only by stating it in that grain's own dispatch contract; nothing
is added to this one by implication."

### D6. Completion and version identity follow ADR 0021's reference pattern

A basis row must detect the change its test names. **Test 1 and test 2 detect
different things**, and conflating them was an error in this document's first
draft: an amendment to the governing Story or Epic moves test 1's effective
version set and does *not* touch a predecessor's satisfying completion. Test 2
moves when the selected completion changes, or when that completion's own
effective view does.

[ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) already fixes the
shape for citing an artifact whose effective view can move: an evidence
reference "binds `artifact_id` plus the referenced payload's digest (and
version, for amended artifacts: the effective-view sequence point it cites)."
Both the version refs and the completion ref use that composite:

```text
    *_artifact_id        uuid NOT NULL
    *_payload_digest     text NOT NULL
    *_effective_sequence int  NOT NULL
```

A constrained composite, not an opaque scalar. `tool_calls.lineage_key` is
**not** the precedent: it exists to force enforcement of foreign keys that
`MATCH SIMPLE` would skip over nullable columns, and generalizing it into an
identity pattern would borrow a workaround for a problem this does not have.

The reason a status column cannot serve:
[ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) is explicit that
an accepted amendment does not supersede the original, so a test written on
`status` misses every amendment — "the common case and the one this decision is
named for."

### D7. Two typed dependency tables, and what SQL cannot enforce

[ADR 0024](../../adr/0024-intake-and-triage-artifact-contract.md) requires both
graph grains with different owners: intake persists the Epic graph, the Architect
owns the Story graph within an Epic. Two tables, matching migration 000003's
discipline of referencing a parent by the whole lineage tuple so a contradiction
is unrepresentable rather than discouraged. A polymorphic `(kind, id)` table
would discard exactly that.

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
one level up, sharing `feature_id`.

**Acyclicity is not expressible as a CHECK**, and a self-edge is only the
one-hop case. Proposal: item 2 enforces the self-edge constraint in SQL, states
the acyclicity invariant beside the table, and assigns enforcement to the
Orchestrator at edge-insert time — with the enforcement point named rather than
implied. Flagged in D10; a trigger is the alternative and I do not think the
cost is warranted before a consumer exists.

### D8. The `tool_calls` migration: an explicit state and six outcomes

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

**State** follows ADR 0032's table (lines 836-841):

| State | Terminal |
| --- | --- |
| `open` | No |
| `operator_waiting` | No |
| `resource_waiting` | No |
| `settled` | Yes |

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
`blocked`, "three descriptions of one event, no two of them agreeing." The
requirement is **preserved on the settled row** so the execution's result can
reference it, which means the recorded requirement cannot live only in a
wait-scoped place.

Replacing the constraint with the equivalence, in both directions:

```text
CHECK ((state = 'settled') = (outcome     IS NOT NULL))
CHECK ((state = 'settled') = (finished_at IS NOT NULL))
```

`succeeded boolean` is backfilled into `outcome` and dropped; the generated
queries and sqlc output are updated in the same item.

### D9. Backfill, and a down migration that refuses rather than lies

Existing rows: `succeeded = true → 'succeeded'`, `false → 'failed'`. Rows with
`succeeded IS NULL` and no `finished_at` are historical in-flight attempts whose
process is gone. **They are left in `open`, not settled as `unknown`** —
settling them would assert a reconciliation nobody performed, and `open` in no
declared wait is precisely what ADR 0030 §8 says a reconciler reads as
*attempted, outcome unknown*.

**The down migration is lossy and must fail loudly.** `blocked`, `stale` and
`unknown` have no boolean image, so a down migration that maps them to `NULL`
silently converts settled history into in-flight rows. It instead aborts when any
such row exists. A down migration that corrupts is worse than one that refuses,
and this is checkable.

### D10. What item 2 must not foreclose

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
| Story edge cannot cross Epics | Insert a `story_dependencies` row whose predecessor is in another Epic |
| No self-edge | Insert an edge with equal endpoints |
| Superseded implies closed admission | Set `authority_state = 'superseded'` leaving `admission_closed_at` null |
| One execution per dispatch | Insert a second execution for one `story_dispatch_id` |
| Settled iff outcome | Insert `state = 'settled'` with null `outcome`; and a non-settled row with an outcome |
| Settled iff finished | Same pair against `finished_at` |
| Outcome vocabulary | Insert an outcome outside the six |
| Basis detects a version move | Accept an amendment to the governing Epic; the stored `epic_version_ref` no longer matches the effective view |
| Basis detects an added predecessor | Insert an already-satisfied predecessor edge; the basis set and the current edge set differ |
| Basis detects a re-satisfied edge | Amend a predecessor's completion; the stored `satisfying_completion` no longer matches |
| Down migration refuses | Run `down` with a `blocked` row present; it must abort, not null the column |

The last three are the ones that matter most: they are the tests that cannot be
written at all if the dependency tables are absent, which is the argument for
building them here.

## Open questions

1. **Acyclicity enforcement** (D7) — stated invariant plus Orchestrator
   enforcement, or a trigger. Proposal: the former, until a consumer exists.
2. **Whether `epic_dependencies` may span Features.** ADR 0024 describes a
   per-Feature DAG; sharing `feature_id` between endpoints assumes it may not.
   If cross-Feature Epic dependencies are intended, the shared column drops and
   the constraint weakens.
3. **Where the preserved requirement lives on a settled `blocked` row** (D8) —
   a `jsonb` column on `tool_calls`, or a reference. ADR 0030 §3 says the record
   carries only the declared projection and a digest, which argues against
   inlining an arbitrary payload.
