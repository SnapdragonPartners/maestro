+++
title = "Design: The Orchestrator Acquires The Data Plane (Item 3)"
edit_date = "2026-09-02"
status = "draft"
type = "design"
summary = "Mini-plan for Phase 3 item 3: the Orchestrator becomes the data plane's caller — a provider-neutral internal/orchestrator whose dependency closure is guarded away from every local composer, a startup contract over the enumerated not-ready states with typed causes and operator remedies, an explicit readiness probe because pgxpool.New is lazy and opening a seam proves nothing today, the configuration-key registry threaded through plane.Composition so governed configuration has a writer at all, typed durable checkpoints defined as committed artifacts plus control rows plus a deterministic recovery projection, the named conditional dispatch transitions item 2 deferred here, and organization/product/repository provisioning shaped so item 4's pack selector completes it rather than amends it. Carries two plan amendments: pkg/persistence's deletion moves to item 14 on import-graph evidence, and \"all four not-ready states\" becomes the enumerated set."
+++

# Design: The Orchestrator Acquires The Data Plane (Item 3)

Status: **draft** — authored for review (Codex + DR). Phase 3 item 3.

Phase 2 built the persistence seam standing alone: `store.Store` has an
implementation, a local composer, a cloud composer, lifecycle verbs, a vault, a
config registry, cold backup and an importer. What it has never had is the
caller the whole thing exists for. This item supplies it.

The plan's own sequencing note states the consequence: **item 3 is where the
data plane stops being a library.** Everything from checkpoint 1 onward assumes
a running plane and an Orchestrator that can be restarted without losing what it
was doing.

Reference tree: `main` at `6c32158` (item 2 merged), plus this branch.

## What Binds This Item

| Source | What it binds here |
| --- | --- |
| [Plan](plan_scope.md) item 3 | The five parts: seam-routed writes; the durable-checkpoint rule; config and secrets acquiring a consumer; the startup contract for a plane that is not ready; organization, product and repository provisioning |
| [Plan](plan_scope.md) decision 2 | Restart is **artifact-level**, and it is a rule before it is a mechanism: an agent restarts from the last committed workflow artifact, never from an arbitrary instruction |
| [ADR 0019](../../adr/0019-orchestrator-boundary.md) | Persistence is Orchestrator machinery; the no-inference rule; the dispatch basis in two halves |
| [ADR 0022](../../adr/0022-v2-data-plane.md) | All data-plane access flows through the seam; agents hold no connection; the cross-store commit-order invariant |
| [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) | Artifacts, principal instances, the review invariant, the effective view |
| [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) | Payload encoding and the registry that makes a payload readable |
| [Item 1 inventory](inventory_agent-surfaces.md) finding 1 | `StateStore` is vestigial: zero production implementations, all four construction sites pass `nil`. Typed checkpoints are a **new build**, and no on-disk migration is owed |
| [Item 2 design](design_work-hierarchy.md) | Terminal dispatch dispositions are immutable, enforced by **this item's** named conditional transitions; an accepted dispatch having at least one execution is a **seam invariant**, committed with the disposition flip |

Where this document and an accepted ADR disagree, the ADR wins.

## Scope

Built here:

| Deliverable | Why it is item 3's |
| --- | --- |
| `internal/orchestrator`, provider-neutral | The plan's "the Orchestrator routes through the seam" needs an Orchestrator |
| The startup contract over the enumerated not-ready states | Plan item 3(d), and an exit criterion |
| An explicit readiness probe below the seam | Without it "the seam opened" asserts nothing (D4) |
| The configuration-key registry threaded through the composition | Plan item 3(c) is unreachable without it (D7) |
| Provisioning: organization, user, product, repository | Plan item 3(e) |
| Work-hierarchy writers and readers on the seam | Nothing can create a Feature, Epic, Story or Work Group today |
| Dispatch creation, and the named conditional transitions | Plan item 3(a); item 2's recorded obligation |
| The durable-checkpoint rule and the recovery projection | Plan item 3(b) and decision 2 |
| `pkg/state` deleted | Item 1 finding 1; exit criterion |
| The dependency-closure guard | Exit criterion, "checkable by import graph, so it is checked rather than trusted" |

Deferred, each to the item that first has a consumer:

| Deferred | To | Why |
| --- | --- | --- |
| Prompt-pack content, installation records and the scoped selector | Item 4 | ADR 0031. Provisioning is shaped so item 4 **completes** it (D11) |
| `pkg/persistence`'s deletion | Item 14 | D1 — it has 42 live v1 importers |
| The `StateStore` *interface* and its constructor parameter | Item 6 | D12 — 81 call sites through the FSM engine item 6 refactors |
| Execution configuration, per-incarnation bindings, agent restart | Items 5/6 | ADR 0032 §2, under that ADR's own demotion |
| Writing `epic_dependencies` / `story_dependencies` edges | Items 10/11 | Item 2's consumer table. Item 3 **reads** them to assemble a basis |
| Cancellation, supersession, drain and fence | Item 9 | The whole of ADR 0019's second amendment's enforcement |
| `tool_calls.arguments` as ADR 0030 §3's projection | Item 5 | Item 2's deferred table |

**Not in scope, stated because a reader could reasonably expect it:** no agent
runs in this item. Checkpoint 1 says so explicitly — "No agent has run." What is
proved here is that *the Orchestrator's own* workflow state survives a restart.
Nothing here implies ADR 0032's execution configuration or agent restart is
settled; items 5 and 6 own those, against a real consumer.

## Decisions

### D1. `pkg/persistence` is not deletable in item 3, and its deletion moves to item 14

The plan's item 3 line ends "Deletes `pkg/persistence`, which Phase 2 deferred
here by design." Phase 2 deferred it **at phase grain** — its plan says "deleted
in Phase 3" and "deleted rather than edited in Phase 3", with no item named. The
item-3 assignment was made without re-deriving the import graph, and the graph
refuses it.

At `6c32158`, `orchestrator/pkg/persistence` is imported by **42 files** outside
the package itself:

| Area | Files |
| --- | --- |
| v1 role drivers | `pkg/architect` (18), `pkg/coder` (4), `pkg/pm` (2) |
| v1 process machinery | `internal/kernel` (2), `internal/supervisor` (2), `internal/factory` (1) |
| v1 entrypoint and suites | `cmd/maestro` (2), `tests/integration` (1) |
| Agent surfaces | `pkg/agent/toolloop` (**refactor**), `pkg/agent/tool_logging.go`, `pkg/agent/middleware/chat` (retire) |
| Other live v1 packages | `pkg/chat` (2), `pkg/contextmgr`, `pkg/webui`, `pkg/telemetry` (2), `pkg/workspace` |

Deleting the package in item 3 means deleting the v1 factory path in item 3,
which is **item 14's** deliverable, before item 5's boundary or item 6's agent
core exist to replace any of it. `pkg/agent/toolloop` makes the point sharpest:
item 1 classifies it **refactor**, not retire, so one importer survives even
after v1's drivers are gone.

**Item 3 leaves `pkg/persistence` untouched and enforces a rule instead**
(D2): the Orchestrator's dependency closure cannot reach it, directly or
transitively. Final deletion is item 14's, with the rest of the `drop`
dispositions.

Rejected: a partial deletion, or an allow-list of permitted importers. Both
create split ownership of a package that is going away whole, and neither
advances the v2 boundary by one line.

**This amends the plan**, and the amendment is carried in this branch. It is a
sequencing correction of the same kind as item 2's `runs` move: evidence from
the tree, no ADR need, no substantive change to what the phase delivers. The
exit checklist is unaffected — it names `StateStore.Save` and `paths.Bootstrap`
against item 3, and `pkg/persistence` only under item 14's "every `drop`
disposition is complete".

### D2. What the Orchestrator may depend on, and the guard that keeps it there

The Orchestrator sees **`store.Store` and the provider-neutral vocabulary
packages, and nothing else below the seam.**

| May import | Must not import |
| --- | --- |
| `internal/dataplane/store` — the seam | `internal/dataplane/stack` — the **local** composer |
| `internal/dataplane/registry` — artifact types it declares | `internal/dataplane/paths` — data roots, key files, flocks |
| `internal/dataplane/configkeys` — config keys it declares | `internal/dataplane/plane`, `.../store/postgres`, `.../objects` |
| `internal/dataplane/readiness` — typed startup causes (D6) | `internal/dataplane/migrations`, `internal/dataplane/cloud` |
| | `pkg/persistence`, `pkg/state` |

`stack` and `paths` are on that list for the reason Phase 2's exit record
predicted by name: "the pressure to import the concrete struct from above the
seam will be real in Phase 3, and that is precisely how a local-only assumption
hardens into architecture." `stack.Config` **is** a Docker Compose topology,
ports and container labels included. An Orchestrator that can name it is an
Orchestrator that runs in exactly one deployment.

**The guard is an import-closure test**, not a documented intention: it walks
the transitive closure of `internal/orchestrator/...` and fails on any forbidden
package. Two properties it must have, both learned in this repository:

- **Computed over the applicable build configurations**, per the *Reachability
  Claims* rule item 1 established. A single default-configuration listing
  reports four packages dead that are not.
- **Defect-shaped**: the branch proves the guard fails by adding a real import
  of `stack` to the Orchestrator and observing the named failure, then removing
  it. A guard that cannot fail for the defect it names is not a regression test.

The forbidden set is derived, not hand-listed where derivation is possible:
everything under `internal/dataplane/` **except** an allowed set of four is
forbidden, so a package added to the data plane later is forbidden by default
rather than permitted by omission. Hand-maintained enumerations have failed
three times in this repository's history.

### D3. The composition root is `cmd/dataplanectl`; no second product entrypoint

Something must import `stack`, resolve a data root, take the lifecycle lock and
hand `store.Store` to the Orchestrator. That is a **composition root**, and
`cmd/dataplanectl` already is one: it holds `bootstrap`, it builds the registry
it hands to `OpenSeam`, and #287 folds it into the main binary at item 14 anyway.

So: provisioning verbs extend `dataplanectl`. **No v2 command is added to
`cmd/maestro`** — a second live product entrypoint beside v1's, four items before
v1 dies, buys nothing that a test-only binary does not buy (D13).

**Epic and Story creation and dispatch stay out of the CLI.** The test harness
calls the package API. Item 11 owns the first manual intake surface, and a CLI
shaped here would be the thing item 11 has to unbuild.

### D4. Opening a seam must prove the plane is usable

Today it proves almost nothing. `postgres.Open` calls `pgxpool.New`, which is
**lazy** — it validates the DSN and returns without contacting anything — and
neither `plane.Open` nor either composer pings the database or reads the schema
version. There is no `Ping` call anywhere in `internal/dataplane/plane` or
`internal/dataplane/store/postgres` outside tests. A seam therefore "opens"
against a stopped Postgres, and against a database whose schema is three
migrations behind, and reports the same success either way. The first symptom is
a failing query somewhere else entirely.

**The probe goes in `plane.Open`**, below the seam and above both composers, and
it does two things: `Ping`, then compare `migrations.Version` against the maximum
version embedded in this binary (`migrations.FS`).

`plane` is the right layer for the same reason the cloud composer already probes
its bucket rather than trusting a constructed handle: *"the bucket is PROBED
before a seam is returned … it is what makes 'the seam opened' mean the same
thing in both modes."* A probe in each composer separately is how one of them
ends up without it.

**This changes behaviour for existing callers**, deliberately: opening a seam
against an unreachable or wrongly-versioned plane now fails at open. The
benchmark importer and `planetest` both run against migrated planes and stay
green; if either does not, that is the probe reporting something true.

### D5. The not-ready states are enumerated, not counted

The plan says "all four not-ready states — absent, unmigrated, locked, and
carrying an interrupted-recovery marker". Four is wrong in both directions:
`unmigrated` conflates three conditions with different remedies, and Phase 2's
D4a/D4b added two further marked states after the plan was written.

The enumerated set, each with what proves it and what the operator does:

| State | Proof | Orchestrator behaviour | Remedy |
| --- | --- | --- | --- |
| **No plane provisioned** | No bootstrap pointer at the config root | Refuse, name the pointer path | `make dataplane-up` |
| **Unreachable or unhealthy** | `Ping` fails (D4) | Refuse, report the endpoint and the driver error | Start the plane; check the service |
| **Schema behind** | `Version` < embedded max | Refuse. **Never self-migrate** | `make dataplane-migrate` |
| **Schema dirty** | `Version` reports `dirty` | Refuse, name the failed version | The manual repair Phase 2 deliberately left unautomated |
| **Schema ahead** | `Version` > embedded max | Refuse, name both versions | Run a newer binary; do not downgrade the plane |
| **Root key missing** | `stack.ErrPlaneLocked` — populated data root, no key file | Refuse, name the expected key path | Restore the key file, or `recover-key` |
| **Restore incomplete** | `.maestro-restore-incomplete` | Refuse. **Never start** — a restore began deleting into this root | Complete the restore |
| **Restore unverified** | `.maestro-restore-unverified` | Refuse *ordinary use* | `make dataplane-up`, which starts it **specifically to verify** and settles the debt |
| **Interrupted recovery** | `.maestro-recovery-in-progress` | Refuse — an orphaned postmaster may still own the data root | The recovery protocol; `down` keeps it resumable |

Two of these deserve emphasis because folding them in would be wrong:

- **Restore-unverified is not "refuse everything".** Phase 2 amendment D4a makes
  `up` the operation that settles the verification debt, through its own
  verification. An Orchestrator that refused the marker categorically would be
  correct about ordinary use and would say nothing about the one verb that
  clears it.
- **Interrupted recovery is refused, not repaired.** Phase 2 amendment D4b makes
  it a third gated state precisely because mishandling it destroys a staged key.
  Normal startup must neither bypass nor advance the recovery protocol.

**The Orchestrator never migrates**, and that is structural rather than a policy
choice: `stack.OpenSeam` takes the lifecycle lock **shared** and holds it for the
seam's lifetime, `Migrate` takes it **exclusive**, and `flock` is not re-entrant.
A process holding an open seam that tried to migrate would block against its own
lock forever. Refusing with a remedy is the only correct behaviour available.

**This amends the plan**: "all four not-ready states" becomes "all enumerated
not-ready states", in the item-3 line and in the exit checklist, and the fixed
count goes. Carried in this branch. The exit criterion keeps its teeth — the
enumeration is in this document, and each row is a test.

### D6. The typed causes are provider-neutral; composers map their own sentinels

D2 forbids the Orchestrator from importing `stack`. Every sentinel in the table
above lives there: `ErrPlaneLocked` (`stack/stack.go:200`),
`ErrRestoreUnverifiedPending` (`:305`), `ErrRecoveryIncoherent` and
`ErrRecoveryForeignMarker` (`stack/recovery.go`), `ErrRestoreUnverified`
(`stack/restore.go:21`). So the Orchestrator cannot classify a startup failure by
`errors.Is` against the package that produces it — which is the correct
constraint, expressed inconveniently.

**A small package `internal/dataplane/readiness`** holds the neutral vocabulary:
a cause code per row of D5, an error type carrying cause, detail and operator
remedy, and nothing else. Producers map onto it — `plane` for the probe results,
`stack` for the local marker and key states, `cloud` for its own — and the
Orchestrator consumes it.

Marker knowledge stays below the seam. What crosses is a cause and a remedy,
which is what a startup boundary needs and all it needs.

Placement is a reviewable point. The alternative is putting the vocabulary in
`store`, which the Orchestrator already imports and which already carries
sentinel errors. It is rejected because `store` is the *persistence interface*,
whose own documentation makes narrowness a rule — "deliberately narrower than
the generated query set" — and plane lifecycle state is not persistence.
A separate package also has two producers and one consumer today, so it is a
seam with present consumers rather than a speculative one.

**The mapping is discovered, not listed.** A structural test enumerates `stack`'s
exported error sentinels by AST and fails on any that reaches the startup path
with no `readiness` mapping. A hand-written table would be the fourth
hand-maintained enumeration in this repository, and the first three all failed.

### D7. `plane.Composition` carries the configuration-key registry

Plan item 3(c) — "configuration and secrets acquire their first consumer" — is
**currently unreachable**, and this is a real gap rather than a preference.

`postgres.New` defaults its key registry to `configkeys.MustNew(nil)`
(`postgres.go:151`) — empty, and fail-closed by design, since every config write
consults the registry first and an unregistered key is refused.
`postgres.WithConfigKeys` (`postgres.go:101`) exists to supply a real one, and
**`plane.Open` never passes it**, because `plane.Composition` has no field for it.
`Composition` carries `Types` (the artifact registry) and stops there. So no
caller reaching the plane through either composer can write a governed
configuration record at all.

Item 3 adds `Keys *configkeys.Registry` to `Composition`, threaded by both
composers, with the same semantics `Types` already has: **the caller's registry,
because what keys are writable is a property of the caller's job.** A caller that
writes no configuration supplies an empty one and is refused if it tries —
which is the existing fail-closed behaviour, now reached deliberately instead of
by omission.

`dataplanectl`'s `openSeam` (`cmd/dataplanectl/benchmark.go:44`) builds only the
benchmark artifact registry today. It must **not** quietly become the
Orchestrator's registry: the two commands declare different jobs. Item 3 gives
the Orchestrator its own registry composition, and the benchmark verbs keep
theirs.

### D8. What a typed durable checkpoint is

There is **no generic checkpoint table**, and no `Save(id string, value any)` in
any form. A checkpoint is three things together:

1. **A committed artifact conforming to a registered payload schema** (ADR 0021,
   ADR 0028). This is what plan decision 2 means by "the last committed workflow
   artifact": typed, reviewed where the review invariant applies, and readable
   only because its type is in the registry.
2. **Durable control rows** identifying the authoritative dispatch and work
   state — `story_dispatches.disposition` and its basis, `executions.authority_state`
   and `admission_closed_at`, and the governing pointers on `stories` and `epics`.
   Item 2 built these; item 3 is what writes and reads them.
3. **A deterministic recovery projection** (D9) stating exactly which rows are
   read, in what order, and what wins when they disagree.

Inventing a checkpoint family would repeat exactly what the `runs` rule forbids:
a table with an ADR mention, no definition and no consumer. Every piece of state
the Orchestrator needs to resume is already representable, because item 2 built
the spine for this consumer.

**What this does not claim.** It proves *Orchestrator workflow* recovery, before
any agent runs. ADR 0032's persisted execution configuration, per-incarnation
bindings, epochs, re-attach and agent restart are demoted design inputs owned by
items 5 and 6, and nothing here should be read as settling them.

### D9. The recovery projection is explicit, ordered, and total

"Reconstruct from the plane" is not a design until it says which rows and what
wins. The projection, run at startup after the readiness contract passes:

1. **Identity.** Resolve the organization and the acting principal from
   configuration and the provisioning records. Nothing is created here; a
   startup that provisions a tenant is the defect item 9's design already names.
2. **Open work.** List `story_dispatches` with `disposition = 'pending'`, and
   `executions` with `authority_state = 'current'`.
3. **The basis.** For each pending dispatch, read its snapshot — the two version
   references and its `dispatch_basis_dependencies` rows — as a set.
4. **The current side.** Read the governing pointers and the dependency edges
   the snapshot must be compared against.
5. **Disagreement.** Where snapshot and current side differ, the **plane wins and
   nothing is repaired**: the dispatch is reported as basis-diverged and left
   exactly as it is. Item 9 owns what happens next. A startup that silently
   reconciled would destroy the evidence item 9's cancellation is triggered by.

The projection is **total**: every pending dispatch lands in exactly one of
"resumable" or "basis-diverged, referred to item 9", and an unclassifiable row is
an error rather than a skipped line. A recovery that quietly ignores a row it
does not understand is how a plane and an Orchestrator start disagreeing.

Restart holds no process-local state to reconcile, which is the property D13
proves rather than assumes.

### D10. Named conditional transitions, and the two one-transaction rules

Item 2 recorded this obligation against item 3 explicitly, and it is not
optional: `story_dispatches`' terminal dispositions are immutable, and **nothing
in the schema enforces it.** The shape constraints permit setting a `failed` row
back to `pending`.

**Every disposition change is a named conditional transition** —
`AcceptDispatch`, `FailDispatch`, `InvalidateDispatch` — each an
`UPDATE … WHERE disposition = 'pending'` in which **zero rows affected is a
rejected transition**, reported as a typed reason rather than a row count. The
artifact seam's `RejectionReason` is the established pattern and this follows it:
classification happens in Go against the locked row, so a caller receives a
reason it can act on.

No generic `UpdateDispatchDisposition` exists. A generic setter is the thing that
makes the immutability unenforceable, and adding one later re-opens this.

Two invariants are cross-table and therefore the seam's, each committed in **one
transaction**:

- **Dispatch creation writes the version references and the complete basis
  together.** Item 2's design assigns this here. A dispatch row committed without
  its `dispatch_basis_dependencies` rows means the plane holds a basis that never
  existed as a set — and item 9's comparison would then be against a snapshot
  that is simply wrong, silently.
- **Accepting a dispatch and creating its execution commit together.** The schema
  gives *at most one* execution per accepted dispatch; the *at least one* half is
  cross-table, and item 2 states plainly that it is a seam invariant rather than
  a schema guarantee.

### D11. Provisioning, shaped so item 4 completes it

Organization and user provisioning exist —
`BootstrapOrganization`/`BootstrapUser`, idempotent by natural key — but they sit
on **`BenchmarkWriter`** (`store/benchmark.go:205-206`), because item 9 was the
only consumer that had ever needed a tenant. Provisioning a tenant is not
benchmark work, and item 3 is its general consumer.

Item 3 moves them onto a `Provisioning` family beside the others in
`Reader`/`Writer`, and adds **product**, **repository** and the
`product_repositories` link, all idempotent by natural key on the same pattern:
a `Bootstrapped[T]` result distinguishing created from existing, and a conflict
on differing display data rather than a silent overwrite.

**The pack selector is item 4's and is not stubbed here.** ADR 0031 makes
organization provisioning seed the scoped selector, which is exactly why packs
sit in block A. Item 3 leaves the seat empty rather than filling it with a
placeholder: a default selector written here is one item 4 must migrate, and the
plan explicitly wants item 4 to *complete* item 3's provisioning rather than
amend it.

Feature, Epic, Story and Work Group creation land as **seam and package API
only** (D3).

### D12. `pkg/state` dies here; the vestigial interface retires with item 6

Item 1 finding 1 established that `StateStore` persists nothing at runtime: its
only implementation is `pkg/state`, which has zero production importers, and all
four production construction sites pass `nil`. The exit criterion is
"`StateStore.Save(id, any)` is gone, and no workflow state persists through a
non-atomic write."

The second clause is satisfied by deleting `pkg/state` — the non-atomic write is
`store.go:133,140`, `json.MarshalIndent` plus `os.WriteFile`, and it is the only
one. Item 3 deletes the package and retargets the two `pkg/agent` race tests that
are its sole consumers.

The **interface** is a different cost. `NewBaseStateMachine(agentID, initialState,
store StateStore, table)` is the v1 FSM engine's constructor:
**81 textual occurrences across 27 files** (including the declaration and
`pkg/agent`'s re-export), essentially all v1 tests. Removing the parameter is a
mechanical 81-site edit through the engine **item 6 refactors** and the test
migration item 6 already owns — and item 6 would then change the same
constructor again.

Item 1 resolved the identical trade-off the same way for the `core.go` stubs:
they stay in item 6, with the test migration, "rather than being pulled forward".
This proposes consistency with that: **item 3 deletes the implementation and the
write; item 6 retires the interface and the parameter with the refactor that is
already touching every one of those call sites.**

This narrows an item-3 exit criterion, so it needs ratification rather than
assertion — it is listed as an open question below. If review prefers the strict
reading, the cost is the 81-site edit in block A and item 6 editing them again;
nothing about the outcome differs by item 14.

`pkg/metrics` and `pkg/agent/middleware/chat` — #298's other two unblocked
deletions — are **not** taken here. They have no relationship to the seam, and
item 3 is already an L.

### D13. Restart is proved in a new process

Constructing a second Orchestrator in the same process does not prove restart
recovery. Package-level state, caches, connection pools and anything a
`sync.Once` already ran survive, and every one of them is a way for the second
instance to "recover" something it never read from the plane.

The proof is a **test-only subprocess**: start it, provision, create an Epic and
Stories, dispatch, commit; exit or kill it; start a **fresh process** against the
same plane; reconstruct using only persisted identities and configuration, and
assert the reconstructed state equals what was committed. Phase 2 has the
precedent and the harness shape — `stack/subprocess_integration_test.go` and
`killed_integration_test.go` — including the kill path, which matters here: a
clean exit can flush something a kill would not.

Test-only, so no second product entrypoint exists (D3), and the evidence is the
one option 2 would have bought.

## Implementation And Review Sequence

One branch, `v2/phase_3/orchestrator-seam`; commits reviewable in sequence. The
order is forced where it is stated: nothing above the seam can be built before
the seam admits it.

| # | Commit | Contents |
| --- | --- | --- |
| 1 | `readiness` | The neutral cause vocabulary, the probe in `plane.Open`, composer mappings, the AST guard over `stack`'s sentinels |
| 2 | `config-registry` | `plane.Composition.Keys`, both composers, `dataplanectl`'s registry kept distinct |
| 3 | `provisioning` | The `Provisioning` family; org/user moved off `BenchmarkWriter`; product, repository, link; `dataplanectl` verbs |
| 4 | `work-queries` | Queries and seam methods for features, epics, stories, work groups, governing pointers |
| 5 | `dispatch` | Dispatch creation in one transaction; the named conditional transitions; the accepted-implies-execution invariant |
| 6 | `orchestrator` | The package: startup contract, recovery projection, checkpoint rule, the seam-routed writes |
| 7 | `state-retirement` | `pkg/state` deleted; race tests retargeted |
| 8 | `proofs` | The subprocess restart test; the import-closure guard; the not-ready state suite |

## Testing And Verification

Per the phase's testing rule, the plane is **real and ephemeral** (`planetest`);
a mock of the thing under test proves nothing about it. Per
*Defect-Shaped Verification*, every guard below is mutation-verified: the
protected behaviour is broken, the named check fails **for its named reason**,
and the break is reverted.

| Claim | How it is proved | The mutation that must kill it |
| --- | --- | --- |
| The Orchestrator cannot reach a local composer | Import-closure test over the applicable configurations | Add a real `stack` import; the guard names it |
| A seam does not open against an unreachable plane | Stop the service, open | Remove the `Ping`; the open succeeds |
| A seam does not open against a wrong schema version | Migrate down one; open. Force a version above the embedded max; open | Remove the version comparison |
| Each not-ready state produces its own cause and remedy | One test per row of D5, against a plane put into that state | Collapse two causes into one; the test naming the remedy fails |
| Interrupted recovery is refused without advancing the protocol | Plant the marker and its staged key; start | Make startup clear the marker; the staged-key assertion fails |
| Governed configuration is writable, and only for registered keys | Write through the Orchestrator's registry; write an unregistered key | Drop `Keys` from the composition; the write is refused |
| A terminal dispatch disposition cannot be reopened | Fail a dispatch, then attempt every transition | Widen a transition's `WHERE`; the rejection becomes a success |
| A dispatch never commits without its complete basis | Force a mid-transaction failure after the dispatch insert | Split the transaction; the partial basis is observable |
| An accepted dispatch always has an execution | Force a failure between the flip and the insert | Split the transaction |
| Recovery reads only the plane | The subprocess test (D13) | Cache the projection in a package variable; the fresh process still passes only if it read the plane |
| Nothing persists through a non-atomic write | `pkg/state` is gone; no `os.WriteFile` of workflow state remains | Reintroduce the file write; the structural check names it |

One thing is deliberately **not** claimed: the projection's totality is asserted
by construction and by a test over the states item 3 can produce. Item 9 adds
states item 3 cannot reach, and the projection will need re-checking there.

## Amendments Carried In This Branch

Both are sequencing corrections with evidence from the tree, in the shape item 2
established. Neither changes what Phase 3 delivers.

1. **`pkg/persistence`'s deletion moves from item 3 to item 14** (D1). The
   item-3 line loses its deletion sentence and gains the closure rule; item 14
   already carries the deletion under #298's dispositions.
2. **"All four not-ready states" becomes "all enumerated not-ready states"**
   (D5), in the item-3 line and the exit checklist, with the enumeration living
   in this document.

## Open Questions

1. **Does item 3's exit criterion accept D12's narrowing?** "`StateStore.Save(id,
   any)` is gone" is satisfied in substance by deleting the only implementation
   and the only write; the interface and its constructor parameter would retire
   in item 6, with the FSM refactor already touching all 81 call sites. The
   alternative is the mechanical edit in block A and item 6 editing them again.
2. **Does `readiness` belong in its own package or in `store`?** D6 argues for
   its own; the counter-argument is one fewer package for a consumer that
   already imports `store`.
3. **Should `dataplanectl` grow a `provision` verb group, or extend
   `bootstrap`?** Cosmetic today, but item 14 folds these into the main binary
   and the shape chosen here is the one that gets folded.

## Related Documents

- [Phase 3 scope and plan](plan_scope.md) — item 3; the two amendments above.
- [Item 1 inventory](inventory_agent-surfaces.md) — finding 1 (`StateStore`),
  and #298's deletion groups.
- [Item 2 design](design_work-hierarchy.md) — the schema this item writes
  through, and the obligations it records against item 3.
- Phase 2: [config and secrets](../phase_2/design_config_secrets.md) (D4, the
  locked plane), [backup](../phase_2/design_backup.md) (D4a, D4b, the markers),
  [the slice import](../phase_2/design_slice_import.md) (seam use as a caller),
  [exit record](../phase_2/notes_exit-record.md) (the `paths.Bootstrap` rule).
- ADRs [0019](../../adr/0019-orchestrator-boundary.md),
  [0021](../../adr/0021-artifacts-and-principal-instances.md),
  [0022](../../adr/0022-v2-data-plane.md),
  [0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md),
  [0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md),
  [0032](../../adr/0032-agent-execution-contract.md).
