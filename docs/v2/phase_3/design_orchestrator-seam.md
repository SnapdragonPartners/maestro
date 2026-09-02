+++
title = "Design: The Orchestrator Acquires The Data Plane (Item 3)"
edit_date = "2026-09-02"
status = "draft"
type = "design"
summary = "Mini-plan for Phase 3 item 3: the Orchestrator becomes the data plane's caller — a provider-neutral internal/orchestrator handed an opener rather than a composer, whose transitive closure is guarded after the seam's own closure is cut free of paths and v1 config; a startup contract over the enumerated not-ready states, with causes mapped explicitly by each producer and proved behaviourally per state; one probe connection supplying both reachability and schema version because pgxpool.New is lazy and opening a seam proves nothing today; the configuration-key registry threaded through plane.Composition so governed configuration has a writer at all; the first production Management artifact types — Epic record, Story record and Story completion, each carrying only what no row owns — without which no Story can be dispatched; typed durable checkpoints as committed artifacts plus control rows plus a recovery projection that compares effective views by digest and sequence; dispatch creation that derives its basis from authoritative rows under the Epic lock and validates every reference under the artifact lock the transitions themselves take; the named conditional transitions item 2 deferred here; provisioning shaped so item 4's pack selector completes it; and StateStore retired whole. Carries three plan amendments: pkg/persistence's deletion moves to item 14, 'all four not-ready states' becomes the enumerated set, and the live-consumer criterion reassigns to item 4 (configuration, ADR 0031 §4) and item 5 (secrets, ADR 0030 §3)."
+++

# Design: The Orchestrator Acquires The Data Plane (Item 3)

Status: **draft** — revised after review rounds 1 and 2 (Codex, 2026-09-02:
nine P1s each, all confirmed against the tree or the ADR they turned on; see
*Points Resolved In Review*). Phase 3 item 3.

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
| [ADR 0024](../../adr/0024-intake-and-triage-artifact-contract.md) | The record shapes intake produces — Feature record, Epic record — and dispatch of dependency-ready work "from the durable backlog as the authoritative scheduler state" |
| [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) | Payload encoding and the registry that makes a payload readable |
| [ADR 0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) §4 | Pack selection reads "scoped configuration … through the Phase 2 configuration records and their key registry" — the first live configuration reader, in item 4 |
| [Item 1 inventory](inventory_agent-surfaces.md) finding 1 | `StateStore` is vestigial: zero production implementations, all four construction sites pass `nil`. Disposition **retire, for the interface and its implementation together** |
| [Item 2 design](design_work-hierarchy.md) | Three obligations assigned to item 3: dispatch creation writes both version references and the complete basis in one transaction; terminal dispositions are immutable via named conditional transitions; **a referenced governing artifact is of the expected type and is accepted** (D7's two seam-validated rows) |

Where this document and an accepted ADR disagree, the ADR wins.

## Scope

Built here:

| Deliverable | Why it is item 3's |
| --- | --- |
| `internal/orchestrator`, provider-neutral, handed an opener | The plan's "the Orchestrator routes through the seam" needs an Orchestrator |
| The seam's own closure cut free of `paths` and `pkg/config` | The dependency guard cannot pass otherwise (D2) |
| The startup contract over the enumerated not-ready states | Plan item 3(d), and an exit criterion |
| One probe connection supplying reachability and schema version | Without it "the seam opened" asserts nothing (D4) |
| The configuration-key registry threaded through the composition | Nothing can write governed configuration today (D7) |
| The first production Management artifact types: Epic record, Story record, Story completion | A dispatch requires an accepted governing artifact and validates each completion; none can exist (D14) |
| Provisioning: organization, user, product, repository | Plan item 3(e) |
| Work-hierarchy writers and readers on the seam | Nothing can create a Feature, Epic, Story or Work Group today |
| Dispatch creation deriving its basis, and the named conditional transitions | Plan item 3(a); item 2's three obligations |
| The durable-checkpoint rule and the recovery projection | Plan item 3(b) and decision 2 |
| `StateStore` retired whole: interface, field, parameter, runtime slot, `Persist`, and `pkg/state` | Item 1 finding 1; exit criterion (D12) |
| The dependency-closure guard | Exit criterion, "checkable by import graph, so it is checked rather than trusted" |

Deferred, each to the item that first has a consumer:

| Deferred | To | Why |
| --- | --- | --- |
| Prompt-pack content, installation records and the scoped selector | Item 4 | ADR 0031. Provisioning is shaped so item 4 **completes** it (D11) |
| **Configuration's live reader** | Item 4 | ADR 0031 §4 names it (D7, amendment 3) |
| **A secret's live reader** | Item 5 | ADR 0030 §3's substitution and injection (D7, amendment 3) |
| `pkg/persistence`'s deletion | Item 14 | D1 — it has 42 live v1 importers |
| Execution configuration, per-incarnation bindings, agent restart | Items 5/6 | ADR 0032 §2, under that ADR's own demotion |
| Writing `epic_dependencies` / `story_dependencies` edges | Items 10/11 | Item 2's consumer table. Item 3 **reads** them under the same lock (D10) |
| `work.feature_record` | Item 11 | No item-3 consumer; no governing pointer wants it (D14) |
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

**This amends the plan** (amendment 1). It is a sequencing correction of the
same kind as item 2's `runs` move: evidence from the tree, no ADR need, no
substantive change to what the phase delivers. The exit checklist is unaffected
— it names `StateStore.Save` and `paths.Bootstrap` against item 3, and
`pkg/persistence` only under item 14's "every `drop` disposition is complete".

### D2. The Orchestrator's closure — after the seam's own is cut free

The Orchestrator sees **`store.Store`, the provider-neutral vocabulary packages,
and nothing else below the seam.** The first draft of this rule listed the
allowed set and could not have passed its own guard: review re-derived the
closure, and the seam drags in what the rule forbids.

**The seam's closure today**, from `go list -deps ./internal/dataplane/store`:

```text
store → registry, configkeys, canonical, nilcheck, secret
secret → canonical, paths            (secret.KeyFile: paths.EnsureKey/LoadKey/RootKeyLen)
paths  → pkg/utils                   (one SafeAssert, paths.go:238)
pkg/utils → pkg/config, pkg/logx     (v1's file-based configuration)
```

So `store.Store` transitively reaches **`paths`** — the package whose
`Bootstrap` the exit checklist names as the thing that must not be imported from
above the seam — and, through one type assertion helper, **v1's `pkg/config`**.
A guard that forbids `paths` fails on `store` itself. That is not a reason to
weaken the guard to direct imports; it is the guard reporting a real leak.

**Item 3 cuts two edges below the seam** before the Orchestrator exists:

1. **`secret` no longer imports `paths`.** The key-file provider
   (`secret.KeyFile`) is the *local* root-of-trust backend and belongs with the
   local machinery: it moves to `paths` (or a `paths/keyfile` subpackage —
   implementation's call, reviewed with the commit), implementing
   `secret.RootKeyProvider` from below. `RootKeyLen` becomes `secret`'s
   constant, which `paths` imports, reversing the edge. `secret` keeps
   `RootKeyProvider`, `ResolvedKey`, `Value` and the envelope — nothing local.
2. **`paths` no longer imports `pkg/utils`.** One `SafeAssert` on
   `info.Sys()` becomes a checked assertion in place. CLAUDE.md permits a
   constrained, proven assertion; a v1 dependency for one is not the trade.

After the cut, the seam's closure is `store, registry, configkeys, canonical,
nilcheck, secret` — six packages, none local, none v1. The rule then reads:

| May reach | Must not reach |
| --- | --- |
| `internal/dataplane/store` — the seam | `internal/dataplane/stack`, `cloud` — the composers |
| `internal/dataplane/registry`, `configkeys` — vocabularies it declares | `internal/dataplane/paths` — data roots, key files, flocks, the bootstrap pointer |
| `internal/dataplane/secret` — `Value`, `RootKeyProvider` | `internal/dataplane/plane`, `store/postgres`, `objects`, `migrations` |
| `internal/dataplane/canonical`, `nilcheck` — neutral helpers | `pkg/persistence`, `pkg/state`, **`pkg/config`** |
| `internal/dataplane/readiness` — typed startup causes (D6) | |

`pkg/config` joins the forbidden set deliberately: item 3(c) says the
Orchestrator's configuration is read through the registry, and an Orchestrator
that can reach v1's file-based config has a second source to drift toward.

**The guard is an import-closure test**, computed with `go list -deps` over the
applicable build configurations per item 1's *Reachability Claims* rule, and
**defect-shaped**: the branch proves it fails by adding a real `stack` import to
the Orchestrator and reading the named failure, then removes it. The forbidden
set is derived — everything under `internal/dataplane/` except the seven allowed
— so a package added later is forbidden by default rather than permitted by
omission.

### D3. The composition root is `cmd/dataplanectl`; the Orchestrator is handed an opener

Something must import `stack`, resolve a data root and hand the Orchestrator a
way to reach the plane. That is a **composition root**, and `cmd/dataplanectl`
already is one: it holds `bootstrap`, it builds the registry it hands to
`OpenSeam`, and #287 folds it into the main binary at item 14 anyway. **No v2
command is added to `cmd/maestro`.**

Review found the first draft contradictory: it had `dataplanectl` open the seam
*before* constructing the Orchestrator, and D6 had the Orchestrator classify open
failures. A failed open cannot be handled by an object that does not exist.

**Resolved: the composition root hands the Orchestrator an opener, not a
store.**

```go
// readiness-neutral; the composition root builds it from stack or cloud.
type Opener func(ctx context.Context) (store.Store, error)

func Start(ctx context.Context, open Opener, cfg Config) (*Orchestrator, error)
```

`Start` calls the opener, classifies any failure through `readiness` (D6),
renders the cause and remedy, and refuses. The Orchestrator therefore owns the
startup contract end to end — which is what the exit criterion asks of *it*
("exercised by the Orchestrator rather than only by its own tests") — while the
only thing that ever names `stack.Config` is the composition root's closure
around `stack.OpenSeam`.

**Epic and Story creation and dispatch stay out of the CLI.** The test harness
calls the package API. Item 11 owns the first manual intake surface, and a CLI
shaped here would be the thing item 11 has to unbuild. Provisioning verbs
extend `dataplanectl` as a **`provision` command group**; the existing
`bootstrap` stays as a thin compatibility shortcut over the same seam methods.

### D4. Opening a seam must prove the plane is usable, from one connection

Today it proves almost nothing. `postgres.Open` calls `pgxpool.New`, which is
**lazy** — it validates the DSN and returns without contacting anything — and
neither `plane.Open` nor either composer pings the database or reads the schema
version. There is no `Ping` call anywhere in `internal/dataplane/plane` or
`internal/dataplane/store/postgres`. A seam therefore "opens" against a stopped
Postgres, and against a database three migrations behind, and reports the same
success either way.

**One probe, one connection.** `plane.Open` acquires a single connection from
the pool — the act that establishes reachability, since the pool is lazy — and
reads the schema version **on that connection** through a new
`migrations.VersionOn(ctx, conn)`, which owns the `schema_migrations` table
knowledge. The embedded maximum comes from `migrations.FS`. Reachability and
version are not two operations that might disagree; they are one connection's
two facts.

**An absent `schema_migrations` table is version 0, not an error.** Round 2
caught the first revision misclassifying it as "schema unreadable". The pinned
driver (`golang-migrate/migrate/v4 v4.19.1`, `database/postgres/postgres.go:393-399`)
deliberately maps both `sql.ErrNoRows` and `undefined_table` to `NilVersion`,
and `migrations.Version` already maps that to `0, clean`. `VersionOn` preserves
exactly that contract, so a fresh cluster nobody has migrated is **schema
behind** — the remedy is `make dataplane-migrate`, which is true — and "schema
unreadable" is reserved for a read that fails for any *other* reason on a
connection that was just acquired: permission, a mid-query drop, a table with
an unexpected shape.

Classification follows from which step failed on that connection: acquire →
**unreachable**; version read (other than the two nil cases) → **schema
unreadable**; version, dirty flag and comparison → the three schema states of
D5.

**The defect-shaped mutation is executable.** The first revision's — "skip the
acquire and call `VersionOn`" — was not, since there is no connection to call
it on. The real one: read the version **through the pool** (`pool` satisfies the
querier interface `VersionOn` takes) instead of through the acquired
connection. A connection failure then surfaces from the version query and is
classified *schema unreadable*; the test asserting *unreachable* fails on the
cause. That is the mutation, and it is one line.

**Cleanup on every failure path.** `postgres.Open` already closes the pool when
`New` fails; the probe's failure must do the same, and `plane.Open` already
releases every `Owned` resource on failure. The test injects a failing version
read *after* acquire and observes the pool's close, because a probe that leaks a
connection on exactly the path it was added to diagnose is worse than no probe.

`plane` is the right layer for the same reason the cloud composer already probes
its bucket rather than trusting a constructed handle: *"it is what makes 'the
seam opened' mean the same thing in both modes."*

**This changes behaviour for existing callers**, deliberately: a seam against an
unreachable or wrongly-versioned plane now fails at open. The benchmark importer
and `planetest` run against migrated planes and stay green; if either does not,
that is the probe reporting something true.

### D5. The not-ready states are enumerated, not counted

The plan says "all four not-ready states — absent, unmigrated, locked, and
carrying an interrupted-recovery marker". Four is wrong in both directions:
`unmigrated` conflates three conditions with different remedies, and Phase 2's
D4a/D4b added two further marked states after the plan was written.

The enumerated set, **each row named by what actually produces it**. Review
corrected the first draft's "no plane" row, which named the bootstrap pointer:
nothing in production reads that pointer (no non-test caller of
`paths.BootstrapFileName` exists outside `paths`), and `stack.rootKeyFor`
decides "no plane" from **data-root evidence** — `planeEvidence(c)` empty — not
from the pointer.

| State | Producer and proof | Orchestrator behaviour | Remedy |
| --- | --- | --- | --- |
| **No plane provisioned** | `stack.ErrNoPlane` — every service data directory empty, under a non-provisioning operation | Refuse, name the data root | `make dataplane-up` |
| **Root key missing** | `stack.ErrPlaneLocked` — populated data root, no key file; the evidence is named | Refuse, name the expected key path | Restore the key file, or `recover-key` |
| **Restore incomplete** | `stack.ErrRestoreIncomplete` — `.maestro-restore-incomplete` | Refuse. **Never start** — a restore began deleting into this root | Finish the restore, or `reset` |
| **Restore unverified** | `stack.ErrRestoreUnverifiedPending` — `.maestro-restore-unverified` | Refuse *ordinary use* | `make dataplane-up`, which starts it **specifically to verify** and settles the debt |
| **Interrupted recovery** | `stack.ErrRecoveryInterrupted` — `.maestro-recovery-in-progress` | Refuse — an orphaned postmaster may still own the data root | Finish `recover-key`, or `reset` |
| **Object store unusable** | `stack.ensureBucket` / cloud's bucket probe fails | Refuse, name the endpoint | Start the plane; check credentials |
| **Unreachable** | D4's acquire fails | Refuse, report the endpoint and driver error | Start the plane |
| **Schema unreadable** | D4's version read fails on a reachable plane for a reason other than the two nil cases | Refuse, report the read error | Inspect the plane; not a migration problem |
| **Schema behind** | version < embedded max — **including version 0**, the absent-table case | Refuse. **Never self-migrate** | `make dataplane-migrate` |
| **Schema dirty** | `dirty` set | Refuse, name the failed version | The manual repair Phase 2 deliberately left unautomated |
| **Schema ahead** | version > embedded max | Refuse, name both versions | Run a newer binary; never downgrade the plane |

Two rows deserve emphasis because folding them in would be wrong:

- **Restore-unverified is not "refuse everything".** Phase 2 amendment D4a makes
  `up` the operation that settles the verification debt. An Orchestrator that
  refused the marker categorically would be correct about ordinary use and say
  nothing about the one verb that clears it.
- **Interrupted recovery is refused, not repaired.** Phase 2 amendment D4b makes
  it a third gated state precisely because mishandling it destroys a staged key.
  Normal startup must neither bypass nor advance the recovery protocol.

**The Orchestrator never migrates**, structurally: `stack.OpenSeam` holds the
lifecycle lock **shared** for the seam's lifetime, `Migrate` takes it
**exclusive**, and `flock` is not re-entrant. A process holding an open seam
that tried to migrate would block against its own lock forever.

**This amends the plan** (amendment 2): "all four not-ready states" becomes
"all enumerated not-ready states", in the item-3 line and the exit checklist,
with the enumeration living in this document.

### D6. Typed causes are provider-neutral, mapped explicitly by each producer

D2 forbids the Orchestrator from importing `stack`, and every sentinel in D5's
table lives there. So the Orchestrator cannot classify a startup failure by
`errors.Is` against its producer — the correct constraint, expressed
inconveniently.

**A small package `internal/dataplane/readiness`** holds the neutral vocabulary:
a cause code per row of D5, an error type carrying cause, detail and operator
remedy, and nothing else. It is its own package rather than part of `store`
because — review's phrasing, and the decisive one — *failures exist before a
Store does*. It has two producers and one consumer, so it is a seam with present
consumers rather than a speculative one.

**The mapping is explicit, per producer, and proved behaviourally.** The first
draft proposed deriving it by AST over `stack`'s exported sentinels. Review
rejected that, correctly: an AST enumerates *names*, not which of them reach
`OpenSeam` under `lifecycleUse`, and it cannot derive a remedy. Re-deriving from
the code, the sentinels that reach ordinary use are exactly five —
`ErrNoPlane`, `ErrPlaneLocked` (from `rootKeyFor`), `ErrRestoreIncomplete`,
`ErrRestoreUnverifiedPending`, `ErrRecoveryInterrupted` (from
`guardRestoreState`) — while `ErrRecoveryIncoherent` and
`ErrRecoveryForeignMarker`, which the first draft named, are produced by
`readRecoveryMarker` and reach only the recovery verb: `guardRecoveryMarker`
stats the file and never reads it.

So each producer carries a **mapping table** from its own sentinels to
`readiness` causes — `stack` for the five above plus the bucket, `plane` for the
probe's five, `cloud` for its own — and **one behavioural test per D5 row**
puts a real plane into that state and asserts the cause *and the remedy* the
Orchestrator renders. The AST survives only as secondary evidence, in the shape
`stack/callsite_test.go` already uses: every exported `Err*` in `stack` appears
either in the mapping or in an explicit not-on-the-use-path table with its
reason, and absence from both fails. A sentinel added later cannot become
unclassified by nobody noticing it.

### D7. The registry is threaded; the live reader is item 4's

Plan item 3(c) — "configuration and secrets acquire their first consumer" — is
**currently unreachable**. `postgres.New` defaults its key registry to
`configkeys.MustNew(nil)` (`postgres.go:151`), empty and fail-closed, since
every config write consults the registry first. `postgres.WithConfigKeys`
(`postgres.go:101`) exists to supply a real one, and `plane.Open` never passes
it, because `plane.Composition` has no field for it. No caller reaching the
plane through either composer can write a governed configuration record.

Item 3 adds `Keys *configkeys.Registry` to `Composition`, threaded by both
composers, with `Types`'s semantics: **the caller's registry, because what keys
are writable is a property of the caller's job.** `dataplanectl`'s `openSeam`
(`cmd/dataplanectl/benchmark.go:44`) builds only the benchmark artifact registry
and must not quietly become the Orchestrator's; the two declare different jobs.

**What item 3 does not have is a live reader, and the first draft pretended
otherwise.** Review is right that a fixture write proves the mechanism and
nothing else, and that loading the root key is not vault consumption — every
seam caller already does it. Item 3 registers no production key, because no
Orchestrator path in item 3 reads one: the record shapes it writes carry their
content as artifacts, provisioning takes identities from the operator, and a key
registered without a reader is the `runs` mistake in a different family.

**The first live reader is item 4, and an Accepted ADR says so.** ADR 0031 §4:
resolution "falls back through scoped configuration … through the Phase 2
configuration records and their key registry (`internal/dataplane/configkeys`).
… Pack selection registers a key there." It is in block A, and checkpoint 1
checks it — "a fresh organization is provisioned with a resolvable prompt-pack
selector". For a **secret**, the first reader is **item 5**, and an Accepted ADR fixes it:
ADR 0030 §3 has the boundary replace every secret in a mediated action's input
with a **version-pinned reference** — "Phase 2's `secrets.version` is already
part of the key-derivation context" — and inject the value at effect. That is
the "forge binding" Phase 2's D8 named as the first caller, seen from the
consumer's side: a forge token stops being a v1 file and becomes a vault row
the boundary reveals. Item 7 is not the reader: it provisions resources and performs no mediated
action, and a credential issued *into* a resource is the unmediated external
path ADR 0030's effect-site table bounds "only by what was granted" — the case
the boundary exists to avoid.

**This amends the plan** (amendment 3): the exit criterion "Configuration and
secrets have a live consumer" is reassigned — configuration to item 4, a secret
to item 5 — while "the locked-plane path is exercised by the
Orchestrator rather than only by its own tests" stays item 3's and is met
through D5/D6. Item 3's own claim is narrow and true: the registry has a writer,
and the vault's failure path is the Orchestrator's startup failure.

### D8. What a typed durable checkpoint is

There is **no generic checkpoint table**, and no `Save(id string, value any)` in
any form. A checkpoint is three things together:

1. **A committed artifact conforming to a registered payload schema** (ADR 0021,
   ADR 0028) — the governing records of D14, reviewed under ADR 0020, readable
   only because their types are in the registry. This is plan decision 2's
   "last committed workflow artifact".
2. **Durable control rows** identifying the authoritative dispatch and work
   state — `story_dispatches.disposition` and its basis, `executions.authority_state`
   and `admission_closed_at`, and the governing pointers on `stories` and `epics`.
3. **A recovery projection** (D9) stating which rows are read, in what order,
   what is compared, and what wins when they disagree.

Inventing a checkpoint family would repeat exactly what the `runs` rule forbids.
Every piece of state the Orchestrator needs to resume is already representable,
because item 2 built the spine for this consumer.

**What this does not claim.** It proves *Orchestrator workflow* recovery, before
any agent runs. ADR 0032's persisted execution configuration, per-incarnation
bindings, epochs, re-attach and agent restart are demoted design inputs owned by
items 5 and 6.

### D9. The recovery projection compares the whole basis, and its classes are disjoint

Round 1 named the first gap — pointer equality misses an accepted amendment,
including a **no-op** one where only the sequence moves. Round 2 named two more:
the first revision loaded governing artifact ids and then compared only digest
and sequence, so a repoint to a *different original with identical content* was
missed; and it compared completions by id alone, so an accepted completion
amendment — no-op included — was missed. And its classes were neither disjoint
nor total: it selected only current executions and then had a "superseded"
class, and a matching accepted dispatch satisfied two classes at once.

**What "the basis matches" means**, for one dispatch, is a conjunction over
every reference item 2's snapshot carries:

| Reference | Snapshot columns | Current side | Equal iff |
| --- | --- | --- | --- |
| Story version | `story_version_artifact_id`, `_effective_digest`, `_effective_sequence` | `stories.governing_artifact_id`; `AmendmentBase` on it | **id** and digest and sequence all equal |
| Epic version | the `epic_version_*` triple | `epics.governing_artifact_id`; `AmendmentBase` on it | id and digest and sequence |
| Incoming edges | the set of `predecessor_story_id` in `dispatch_basis_dependencies` | the set of `predecessor_story_id` in `story_dependencies` where this Story is successor | the two sets are identical |
| Each completion | `completion_artifact_id`, `_effective_digest`, `_effective_sequence` | the edge's `satisfying_completion_artifact_id`; `AmendmentBase` on it | id and digest and sequence; a null current pointer is *diverged* |

Id **and** digest **and** sequence, every time. Id alone misses amendments;
digest alone misses no-op amendments; digest and sequence without id miss a
repoint to an identical twin. Item 2 kept all three halves for exactly this
comparison.

**The row kinds selected, and the predicates.** Terminal dispatches are not
open work and are not read. The projection reads:

- **K1** — `story_dispatches` with `disposition = 'pending'`;
- **K2** — `story_dispatches` with `disposition = 'accepted'`, joined to their
  execution **regardless of `authority_state`**. D10's seam invariant says the
  join finds exactly one row; a K2 dispatch with none is a projection **error**,
  not a class.

Each row lands in exactly one class, by (kind × authority × match):

| Class | Predicate | Meaning |
| --- | --- | --- |
| `pending_resumable` | K1 ∧ match | Awaiting acceptance under a basis that is still current |
| `pending_diverged` | K1 ∧ ¬match | ADR 0019's pending-dispatch case: to be invalidated and reissued — item 9's; reported, untouched |
| `execution_awaiting_boundary` | K2 ∧ current ∧ match | Item 3 can create this (D10) and nothing in item 3 can drive it; awaits item 5 |
| `execution_diverged` | K2 ∧ current ∧ ¬match | Item 9's cancellation input; reported, untouched — a startup that reconciled would destroy the evidence its cancellation is triggered by |
| `execution_superseded` | K2 ∧ superseded | Item 9's drain state, whatever the basis says. Item 3 cannot produce it; its run asserts the class is empty |

Disjoint because the three factors partition the rows; total because every
selected row has a kind, every K2 row has an authority, and match is a
predicate over columns that exist. An unclassifiable row — a K2 without an
execution, an `authority_state` outside the vocabulary — is an error, never a
skipped line.

**What the projection does after classifying: nothing.** The plane wins. The
Orchestrator reports the classes and holds; item 9 owns every transition out of
a diverged class.

**Startup order.** The projection runs in `Start` after the readiness contract
(D5) passes, in one `REPEATABLE READ` transaction, because it is a read of a
*consistent* picture and item 9's writers may be moving rows underneath a
restart.

**The subprocess proof (D13) traverses real reviewed artifacts**: a Story
record authored by one principal, reviewed and accepted by another, pointed at
by `stories.governing_artifact_id`, dispatched; the fresh process must reload
its effective view and land it `pending_resumable`. Then three further runs,
each a single change between the two processes, each landing
`pending_diverged` for the named reason: an accepted **no-op amendment** of the
Story record (sequence only); a **repoint** of the governing pointer to an
accepted twin with identical content (id only); an accepted no-op amendment of
a **predecessor's completion** (completion sequence only).

### D10. Dispatch derives its basis under two locks; named transitions guard the dispositions

Item 2 assigned three obligations here. Round 1 found the first draft met one
and a half; round 2 found the revision validated status **before** taking the
artifact lock, and never validated completions at all.

**Dispatch creation derives the basis from authoritative rows; the caller
supplies a Story and nothing else.** A caller-supplied basis can omit a
predecessor and still commit atomically, and several `READ COMMITTED`
statements can observe different states of the graph. So `CreateDispatch`,
inside one transaction:

1. **Lock the Epic row** (`SELECT … FOR UPDATE`). This is the stable parent
   item 2 chose for serializing Story-graph mutations under ADR 0027, so
   item 9's and item 10's graph writes, the governing-pointer repoints, and
   dispatch creation all queue on one row.
2. **Read the Story's incoming edges** under that lock. Any edge with a null
   `satisfying_completion_artifact_id` is **not dependency-ready** — a typed
   rejection, never a dispatch with a hole in its basis.
3. **Lock every referenced artifact, then validate it under its lock.** The
   Epic lock serializes the *work graph*; it does not block `AcceptArtifact`,
   `SupersedeArtifact` or `ArchiveArtifact`, which take only the artifact's own
   row (`LockManagementArtifact`, `FOR UPDATE`). So an artifact validated as
   accepted at one statement can be superseded before the next and still enter
   the dispatch. The order is therefore: for the Story's governing artifact,
   the Epic's, and each edge's completion — **in ascending artifact id** —
   take the same row lock the transitions take, and *under it* read type and
   status, validate, and compute the effective view's digest and sequence. One
   locked read per reference; nothing is validated on an unlocked row.
4. **Validation, per reference**, all through the registry (D14):
   the Story's governing artifact is a `work.story_record`; the Epic's is a
   `work.epic_record`; **each completion is a `work.story_completion`** — the
   check item 2 named "this is a Story completion" and assigned to the seam;
   every one of them has `status = accepted`. Scope-to-the-right-work-item is
   already the composite foreign key's. A draft, a wrong type, or a superseded
   row is a typed rejection naming which reference failed.
5. **Write** the dispatch row, both version references and every
   `dispatch_basis_dependencies` row, in that transaction.

**Lock order is a rule for every writer, stated so item 9 can keep it:** the
Epic row before any artifact row, and artifact rows in ascending id. The
artifact transitions take an artifact lock and never an Epic lock afterwards,
so no cycle exists today; a repoint in item 9 that took the artifact first would
create one, and that is the sentence it must read before it does.

**Disposition changes are named conditional transitions** — `AcceptDispatch`,
`FailDispatch`, `InvalidateDispatch` — each an
`UPDATE … WHERE disposition = 'pending'` in which **zero rows affected is a
rejected transition**, reported as a typed reason on the artifact seam's
`RejectionReason` pattern. No generic setter exists; a generic setter is what
makes the immutability unenforceable.

**Accepting a dispatch and creating its execution commit together.** The schema
gives *at most one*; the *at least one* half is cross-table and item 2 states
it is the seam's. D9's projection treats its absence as an error for the same
reason.

### D11. Provisioning, shaped so item 4 completes it

`BootstrapOrganization`/`BootstrapUser` exist — idempotent by natural key — but
sit on **`BenchmarkWriter`** (`store/benchmark.go:205-206`), because item 9 was
the only consumer that ever needed a tenant. Item 3 moves them onto a
`Provisioning` family beside the others in `Reader`/`Writer` and adds product
and repository on the same `Bootstrapped[T]` pattern: created versus existing,
and a conflict on differing data rather than a silent overwrite.

**Repository provisioning is not independent of its product, and the schema
says so.** Review pointed at `repositories_primary_is_member_fkey` (migration
000002 line 83): the primary Product must also be a member, enforced
`DEFERRABLE INITIALLY DEFERRED` — mandatory at commit. So `ProvisionRepository`
inserts the repository and its primary `product_repositories` row **in one
transaction**; **secondary memberships** are a separate idempotent operation;
and re-provisioning an existing repository with a different primary Product is
a **conflict**, because changing the designation is a decision someone makes
rather than a side effect of a retried command.

**The pack selector is item 4's and is not stubbed here.** ADR 0031 makes
organization provisioning seed the scoped selector, which is why packs sit in
block A. Item 3 leaves the seat empty: a default written here is one item 4
must migrate, and the plan wants item 4 to *complete* item 3's provisioning.

Feature, Epic, Story and Work Group creation are **seam and package API only**
(D3).

### D12. `StateStore` retires whole, here

The first draft proposed deleting `pkg/state` in item 3 and leaving the
interface and its constructor parameter to item 6, on the cost of 81 call sites
through the FSM engine item 6 refactors. Review declined, and the reasons are
the right ones: item 1's inventory — **live** — retires "the interface and its
implementation together"; the plan's exit criterion says `StateStore.Save` is
gone *in item 3*; and eighty-one mechanical edits are a cost, not evidence for
moving an accepted item boundary.

So item 3 deletes all of it:

| Surface | Location |
| --- | --- |
| `StateStore` interface | `pkg/agent/internal/core/machine.go:70-76`; alias at `pkg/agent/core.go:55` |
| `BaseStateMachine.store` field and the constructor's third parameter | `machine.go:86`, `:111`; **81 occurrences across 27 files**, essentially all v1 tests |
| `Persist()` and the `Load` branch of state restoration | `machine.go:308-320`, `:427-429` |
| The never-assigned runtime slot | `pkg/agent/internal/runtime/driver.go:49` (`Context.Store`), forwarded at `base_driver.go:25` |
| The implementation and its two test consumers | `pkg/state`; `pkg/agent/race_test.go`, `proper_race_test.go` |

The race tests exercised concurrent `Persist` against the file store; with
nothing to persist, the concurrency they covered is gone with the feature, and
they are deleted rather than retargeted at a mock of something that no longer
exists.

`pkg/metrics` and `pkg/agent/middleware/chat` — #298's other two unblocked
deletions — are **not** taken here; they have no relationship to the seam.

### D13. Restart is proved in a new process

Constructing a second Orchestrator in the same process does not prove restart
recovery. Package-level state, caches, connection pools and anything a
`sync.Once` already ran survive, and every one of them is a way for the second
instance to "recover" something it never read from the plane.

The proof is a **test-only subprocess**: start it, provision, create an Epic and
Stories with reviewed governing records, dispatch, commit; exit or kill it; start
a **fresh process** against the same plane; reconstruct using only persisted
identities and configuration, and assert the projection's classification of
every row equals what was committed. Phase 2's `stack/subprocess_integration_test.go`
and `killed_integration_test.go` are the harness precedent, the kill path
included: a clean exit can flush something a kill would not.

### D14. The first production Management artifact types

No production Management artifact type exists — the registry holds only the
benchmark importer's two. And item 2's schema makes `story_version_artifact_id`
**NOT NULL** on a dispatch: **no Story can be dispatched until a Story has an
accepted governing artifact of a known type.** That is a consumer, and it is
item 3's.

Round 2 corrected the first revision on three counts, and each changed what
lands. A `work.feature_record` was registered with no item-3 consumer — its own
table said it governed nothing — and is **deferred to item 11**, where intake
authors it; a Feature *row* exists without a governing artifact, and migration
000021 added governing pointers to `stories` and `epics` only, so nothing in the
schema wants one. The **completion** type, which item 3 *does* consume (D10
step 4), was missing. And the payloads duplicated `title` and `repository`,
which migration 000003 already holds as authoritative columns, so nothing
prevented a reviewed payload and its row from disagreeing.

**Three types land, and each payload carries only what no row owns:**

| Type | Governs / satisfies | Version 1 payload | Source of every field |
| --- | --- | --- | --- |
| `work.epic_record` | `epics.governing_artifact_id` | `intent` (text, required); `mode` (`workbench` \| `factory`, required) | ADR 0024's Epic record: intent content, and the triage output the row lacks. `repository` and `dependencies` — the other two triage outputs — **are rows** (`epics.repository_id`, `epic_dependencies`) and are not repeated |
| `work.story_record` | `stories.governing_artifact_id` | `intent` (text, required) | ADR 0024: the Architect-owned decomposition unit; ADR 0018: a PR-sized chunk. `title` is the row's |
| `work.story_completion` | `story_dependencies.satisfying_completion_*`, and the basis | `head_commit` (40-hex SHA, required) | ADR 0023 *Merge policy*: a Story completes when "the Architect's review record completes after final code review" and the Story branch merges. The branch name is derived from the Story id (ADR 0023 *Branch naming*), so it is not repeated; the merge commit follows acceptance and is Audit data, so it is not here |

**One authority per fact.** Title, repository, lineage and dependencies are
rows; intent, mode and the reviewed head are payload. No field appears in both,
so there is no equality to enforce and no way for the two to disagree.

**How version 1 stays usable, forever.** Round 2 was right that planning
version bumps before the first workflow exists creates the compatibility debt
ADR 0028 makes permanent — every historical version supported or explicitly
refused. So version 1 is **not** "what item 3 needs, to be completed later".
It is the ADR 0024 / ADR 0023 contract minus the fields rows own, which is the
whole contract those ADRs state. Items 10 and 11 consume version 1 as it stands.
If either ever needs a field no Accepted ADR names today, that is a new version
whose validator is **additive** and whose predecessor stays readable — the rule
ADR 0028 already imposes, not a plan this design is making.

The `work.` prefix follows the importer's `benchmark.` convention; round 2
accepted the naming.

## Implementation And Review Sequence

One branch, `v2/phase_3/orchestrator-seam`; commits reviewable in sequence, and
the order is forced: nothing above the seam can be built before the seam admits
it, and the guard cannot be written before the closure it guards exists.

| # | Commit | Contents |
| --- | --- | --- |
| 1 | `closure` | `secret`'s key-file provider moves below; `paths` drops `pkg/utils`; the seam's closure asserted |
| 2 | `readiness` | The neutral vocabulary; `migrations.VersionOn`; the single-connection probe in `plane.Open` with failure-path cleanup; explicit producer mappings; the two-table sentinel guard |
| 3 | `config-registry` | `plane.Composition.Keys`, both composers, `dataplanectl`'s registry kept distinct |
| 4 | `artifact-types` | `work.epic_record`, `work.story_record`, `work.story_completion`: version 1 validators and extractors |
| 5 | `provisioning` | The `Provisioning` family; org/user moved off `BenchmarkWriter`; product; repository with primary membership in one transaction; secondary membership; `dataplanectl provision` |
| 6 | `work-queries` | Queries and seam methods for features, epics, stories, work groups, governing pointers, and the edge and pointer reads D10 needs |
| 7 | `dispatch` | `CreateDispatch` deriving its basis under the Epic lock and per-artifact locks; type and status validation of every reference, completions included; the three named transitions; accept-creates-execution |
| 8 | `orchestrator` | `Start(opener)`, the startup contract, the recovery projection, the seam-routed writes |
| 9 | `state-retirement` | `StateStore` gone: interface, field, parameter, runtime slot, `Persist`, `pkg/state`, the two race tests |
| 10 | `proofs` | The subprocess restart test with the no-op amendment run; the import-closure guard with its planted violation; the per-state readiness suite |

## Testing And Verification

Per the phase's testing rule, the plane is **real and ephemeral** (`planetest`);
a mock of the thing under test proves nothing about it. Per *Defect-Shaped
Verification*, every guard below is mutation-verified: the protected behaviour
is broken, the named check fails **for its named reason**, and the break is
reverted. Round 1 found one mutation in this table that would have failed for
the wrong reason; each row now states the reason the failure must carry.

| Claim | How it is proved | The mutation, and the reason the failure must name |
| --- | --- | --- |
| The seam's closure is six packages, none local, none v1 | `go list -deps` assertion | Reintroduce `secret → paths`; the guard names `paths` |
| The Orchestrator cannot reach a composer | Import-closure test over the applicable configurations | Add a real `stack` import; the guard names `stack` |
| An unreachable plane is reported as **unreachable** | Stop the service; `Start` | Read the version through the pool instead of the acquired connection; the connection failure arrives as a query error and is classified *schema unreadable* — the test asserts the cause |
| A never-migrated cluster is **schema behind**, not an error | Open against a cluster with no `schema_migrations` | Map `undefined_table` to *schema unreadable*; the remedy assertion (`dataplane-migrate`) fails |
| A behind / dirty / ahead schema is refused with its own cause | Migrate down one; force dirty; force a version above the embedded max | Remove the comparison; the open succeeds |
| The probe leaks nothing on failure | Inject a failing version read after acquire | Drop the pool close on that path; the leak is observed |
| Each not-ready state produces its own cause **and remedy** | One test per D5 row, plane put into that state | Merge two mappings; the remedy assertion fails |
| Interrupted recovery is refused without advancing the protocol | Plant the marker and its staged key; `Start` | Make startup clear the marker; the staged-key assertion fails |
| Governed configuration is writable only for registered keys | Write a registered key through the Orchestrator's registry; write an unregistered one | Drop `Keys` from the composition; the registered write is refused |
| A dispatch cannot be created dependency-unready | Leave one edge unsatisfied | Skip the null check; a dispatch commits with a missing predecessor |
| A dispatch cannot reference a draft or wrong-type governing artifact | Point the Story at a draft; at a `work.epic_record` | Skip the registry check; the dispatch commits |
| A completion must be an accepted `work.story_completion` | Satisfy an edge with a draft; with an accepted `work.story_record` | Skip the completion check; the dispatch commits with a non-completion in its basis |
| Status is validated under the artifact lock | Force a supersession to interleave between the Epic lock and the artifact read | Validate before locking; the superseded artifact enters the dispatch |
| Dispatch inputs cannot move between the reads | Force a pointer repoint to interleave after step 2 | Drop the Epic lock; the interleaved write commits and the basis is stale at creation |
| A terminal disposition cannot be reopened | Fail a dispatch, then attempt every transition | Widen a `WHERE`; the rejection becomes a success |
| An accepted dispatch always has an execution | Force a failure between the flip and the insert | Split the transaction |
| Recovery compares the whole basis | Three runs: a no-op Story amendment; a repoint to an identical twin; a no-op completion amendment | Drop the sequence compare; drop the id compare; compare completions by id only — each run's `pending_diverged` becomes `pending_resumable` for its own reason |
| The classes are disjoint and total | Every item-3-producible state, plus a K2 row with its execution deleted by fixture | Remove the K2-without-execution error; the row is silently skipped |
| Recovery reads only the plane | The subprocess test (D13), kill path included | Cache the projection in a package variable; the fresh process passes only if it read the plane |
| Nothing persists through `StateStore` | The symbols are gone; `go build ./...` | Reintroduce `Persist`; a structural check names it |
| Repository provisioning commits with its primary membership | Provision; read `product_repositories` | Split the transaction; the deferred constraint fires at commit and the test asserts **that** constraint's name |

Not claimed: the projection's totality beyond the states item 3 can produce.
Item 9 adds states item 3 cannot reach, and the projection is re-checked there.

## Amendments Carried In This Branch

All three are sequencing corrections with evidence from the tree or an Accepted
ADR, in the shape item 2 established. None changes what Phase 3 delivers.
`plan_scope.md` is edited in the acceptance commit, not before, so no live
document asserts a decision nobody has accepted.

1. **`pkg/persistence`'s deletion moves from item 3 to item 14** (D1). The
   item-3 line loses its deletion sentence and gains the closure rule.
2. **"All four not-ready states" becomes "all enumerated not-ready states"**
   (D5), in the item-3 line and the exit checklist.
3. **Configuration's live consumer is item 4 (ADR 0031 §4); a secret's is
   item 5 (ADR 0030 §3)** (D7). The exit criterion splits: the live-consumer
   clause moves, the locked-plane clause stays with item 3.

## Points Resolved In Review

Round 1 (Codex, 2026-09-02). Nine P1s, every one confirmed against the tree
before the design moved; the confirmations are recorded in the decisions above.

| # | Finding | Resolution |
| --- | --- | --- |
| 1 | The guard could not pass: `store` reaches `paths`, `canonical`, `nilcheck` | D2 — closure re-derived; two edges cut below the seam; `canonical`/`nilcheck` allowed. Found further: `paths → pkg/utils → pkg/config` |
| 2 | The probe's mutation was false: `migrations.Version` also connects | D4 — one connection supplies both facts via `migrations.VersionOn`; failure-path cleanup tested |
| 3 | Wrong producers; AST cannot derive reachability or remedies | D5/D6 — rows named by their real producer; explicit mapping tables; behavioural test per row; AST as secondary two-table guard |
| 4 | Startup ownership contradictory | D3 — the Orchestrator is handed an `Opener`; `Start` owns classification |
| 5 | No live config or secret consumer | D7 — no production key registered here; amendment 3 per ADR 0031 §4 |
| 6 | Basis neither coherent nor complete; type/status check omitted | D10 — derived under the Epic lock; unready and draft/wrong-type rejected |
| 7 | Projection did not recover the checkpoint it defined | D9 — `AmendmentBase` digest and sequence; executions classified; D14 names the types; the no-op amendment run |
| 8 | Repository cannot bootstrap independently | D11 — primary membership in one transaction; secondary separate; re-primary is a conflict |
| 9 | `StateStore` narrowing declined | D12 — retired whole |

Open-question calls taken as review gave them: `readiness` is its own package;
`provision` is a command group with `bootstrap` as a shortcut; D1, no second
entrypoint, and fresh-process restart stand.

Round 2 (Codex, 2026-09-02). Nine P1s, each confirmed against the tree or the
Accepted ADR it turned on.

| # | Finding | Resolution |
| --- | --- | --- |
| 1 | Absent `schema_migrations` misclassified; the mutation was not executable | D4 — nil version is 0/clean, hence *schema behind*; the mutation is "read through the pool" |
| 2 | Governing ids not compared; completions compared by id only | D9 — id ∧ digest ∧ sequence for every reference, completions included |
| 3 | Classes neither disjoint nor total | D9 — K1/K2 × authority × match; K2 without execution is an error |
| 4 | Status validated before the artifact lock | D10 — lock each artifact with the transitions' own `FOR UPDATE`, validate under it; lock order stated |
| 5 | Completions never validated; no completion type | D10 step 4; D14's `work.story_completion` |
| 6 | `work.feature_record` had no consumer | D14 — deferred to item 11 |
| 7 | Planned version bumps create permanent debt | D14 — v1 is the ADR contract minus row-owned fields; consumed as-is; any later version is additive under ADR 0028's existing rule |
| 8 | Payloads duplicated relational fields | D14 — one authority per fact; nothing appears in both |
| 9 | Amendment 3's secret owner was provisional | D7 — item 5, by ADR 0030 §3's substitution rule; item 7 performs no mediated action |

Calls taken: the key-file provider lands in `paths` itself; the `work.*_record`
naming stands.

## Open Questions

None remain from rounds 1 and 2. What the implementation will surface is
recorded as it appears.

## Related Documents

- [Phase 3 scope and plan](plan_scope.md) — item 3; the three amendments above.
- [Item 1 inventory](inventory_agent-surfaces.md) — finding 1 (`StateStore`),
  and #298's deletion groups.
- [Item 2 design](design_work-hierarchy.md) — the schema this item writes
  through; D7's seam-validated rows; the obligations table.
- Phase 2: [config and secrets](../phase_2/design_config_secrets.md) (D4, the
  locked plane), [backup](../phase_2/design_backup.md) (D4a, D4b, the markers),
  [the slice import](../phase_2/design_slice_import.md) (seam use as a caller),
  [exit record](../phase_2/notes_exit-record.md) (the `paths.Bootstrap` rule).
- ADRs [0019](../../adr/0019-orchestrator-boundary.md),
  [0021](../../adr/0021-artifacts-and-principal-instances.md),
  [0022](../../adr/0022-v2-data-plane.md),
  [0024](../../adr/0024-intake-and-triage-artifact-contract.md),
  [0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md),
  [0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md),
  [0032](../../adr/0032-agent-execution-contract.md).
