+++
title = "Checkpoint 1: The Plane Holds The Work"
edit_date = "2026-09-26"
status = "live"
type = "notes"
summary = "Record of Phase 3's first checkpoint, closing block A: each of the checkpoint's five clauses mapped to the test or operator command that demonstrates it, the three gaps the review of that mapping found and closed — the four schema states and the object-store state were proved one layer below the Orchestrator, and the dispatch's persisted prompt resolution was never read back across the restart — the operator run against the real local plane, the mutants that prove the new assertions discriminate, and what the checkpoint deliberately does not claim: no agent has run, the built-in pack is resolvable and not executable, and work creation is seam API only."
+++

# Checkpoint 1: The Plane Holds The Work

Status: **live** — Checkpoint 1 passed. Codex approved the demonstration
commit `39b097b9` with no blocking findings (round 1, build-capable,
2026-09-25); DR accepted 2026-09-26. Block A is closed and block B opens
with item 5. The rendering observation below is deferred to a GitHub issue by
DR's decision.

The [phase plan](plan_scope.md#block-a--foundations) closes block A with:

> **Checkpoint 1 — the plane holds the work.** An Epic and its Stories are
> created, dispatched, and durably checkpointed through the seam; the
> Orchestrator recovers its own state across a restart; a fresh organization is
> provisioned with a resolvable prompt-pack selector; and startup is correct
> against **every enumerated** not-ready state — including locked, each schema
> state, and **interrupted recovery**, where normal startup must neither
> bypass nor corrupt the recovery protocol. No agent has run.
> Reviewed before block B opens, because everything below persists through it.

A checkpoint is "a review against a demonstrated capability, not a document:
it either runs or the block is not done." This record is the map from each
clause to what runs, so the review can check the demonstration rather than the
description. It was written by walking the clauses against `main` at
`72c34080` (items 3 and 4 merged), finding where the evidence stopped one
layer short of the clause, and closing those gaps. No mechanism was added.

## The Clauses And Their Evidence

| Clause | Demonstrated by | Where |
| --- | --- | --- |
| An Epic and its Stories are created, dispatched and durably checkpointed through the seam | The restart harness's **commit child**: provisions the tenant, records two foreign principals, creates product, repository, feature, Epic and Story, authors and accepts the `work.epic_record` and `work.story_record` governing artifacts, points the rows at them, ensures the Work Group, plants two completed predecessors, creates the dispatch (which resolves the prompt pack), optionally accepts it — then is **killed** (`SIGKILL`) so nothing a clean exit would flush can help the next process | `internal/orchestrator/restart_integration_test.go`, `commitWork` |
| The Orchestrator recovers its own state across a restart | A **fresh process** starts against the same plane using only persisted identities and configuration, and its projection classifies every open dispatch as committed: `pending_resumable`, `execution_awaiting_boundary`, and five `pending_diverged` shapes each produced by one other-writer change between the two processes (item 3 design D13, D9) | `TestRestartRecoversCommittedWork`, `TestRestartRecoversAnAcceptedDispatch`, `TestRestartClassifiesEachTransitionShape` |
| — and the decision it recovers is the one that was made | **Closed here.** The fresh process reads the dispatch's persisted prompt resolution through the seam it started with and the parent asserts it equals, field for field, what the commit child persisted; in the baseline test the parent **moves the installation record** between the two processes (new display name, revision 2), so a fresh process that re-resolved instead of reading the row would be seen. ADR 0031 §5, plan exit criterion "resolved once at dispatch and reused verbatim across restarts" | `restart_integration_test.go`, `resolutionJSON`, `harness.moveInstallation`, `harness.expect` |
| A fresh organization is provisioned with a resolvable prompt-pack selector | The production built-in pack, loaded the way the composition root loads it, provisions an organization whose selector resolves — three writes or none, idempotent, concurrent provisioners seed one selector, a dangling selector is reported and repaired; and the **operator run below**, against the real local plane | `internal/dataplane/store/postgres/promptprovision_integration_test.go`, `TestTheProductionBuiltinProvisionsAResolvableSelector` and siblings; `dataplanectl bootstrap`, `prompt-pack show` |
| Startup is correct against every enumerated not-ready state | Every one of item 3 design D5's eleven rows driven **through `orchestrator.Start`** — see the table below. Six of the eleven were already there; **five are closed here** | `cmd/dataplanectl/startup_test.go`, `internal/orchestrator/start_integration_test.go` |
| — interrupted recovery neither bypassed nor corrupted | Startup against the recovery marker is refused, and afterwards **both the marker and the staged key are still there** | `TestStartLeavesAnInterruptedRecoveryUntouched` |
| No agent has run | True by construction: nothing in block A starts a principal. The harness's two principals are recorded as foreign imports with closed lifetimes (item 4 design D5), and the projection's `execution_awaiting_boundary` class is exactly "accepted, and nothing has run" | — |

### Every enumerated not-ready state, through `Start`

Item 3's design (D5) enumerates eleven states. Before this branch the local
five and *unreachable* were driven through `Start`; the four schema states were
proved at `plane.Open` (`TestProbeClassifiesEverySchemaState`) and the
object-store state at `stack.OpenSeam` (`TestOpenSeamRefusesAnUnusableObjectStore`),
one layer below the clause. `Start`'s mapping from a readiness cause to a
`StartupRefused` was unit-tested (`TestRefuseClassifiesOnlyReadinessFailures`),
so the chain was sound; but the checkpoint's standard is demonstrated, and a
mapping test plus a producer test is an inference, not a run.

| D5 row | Cause | Driven through `Start` by | Opener | Remedy asserted |
| --- | --- | --- | --- | --- |
| No plane provisioned | `no_plane` | `TestStartRendersEveryLocalNotReadyState/no plane` | real composition root | `dataplane-up` |
| Root key missing | `root_key_missing` | `…/root key missing` | real composition root | `recover-key` |
| Restore incomplete | `restore_incomplete` | `…/restore incomplete` | real composition root | `dataplane-restore` |
| Restore unverified | `restore_unverified` | `…/restore unverified` | real composition root | `dataplane-up` |
| Interrupted recovery | `recovery_interrupted` | `…/recovery interrupted`, and `TestStartLeavesAnInterruptedRecoveryUntouched` | real composition root | `recover-key`; marker and staged key survive |
| **Object store unusable** | `object_store_unusable` | **new:** `…/object store unusable` — populated data root, key present, object endpoint a closed port; the bucket probe runs before the database is touched | real composition root | `dataplane-up` |
| Unreachable | `unreachable` | `TestStartRefusesANotReadyPlaneWithCauseAndRemedy` — endpoint and driver error rendered | neutral | non-empty, rendered |
| **Schema unreadable** | `schema_unreadable` | **new:** `TestStartRendersEverySchemaState/unreadable` — right table, wrong shape; the driver's read error is rendered | neutral | `inspect the plane`; the driver's error in `detail:` |
| **Schema behind** | `schema_behind` | **new:** `…/behind` (embedded − 1) and `…/never migrated is behind at version 0` — plane's version and binary's version both named; **the plane's version is unchanged afterwards** and no `schema_migrations` table is created on the never-migrated plane | neutral | `pending migrations` (neutral wording) |
| **Schema dirty** | `schema_dirty` | **new:** `…/dirty` and `…/dirty outranks behind` — the dirty flag is still set afterwards | neutral | `repair`, the dirty version named |
| **Schema ahead** | `schema_ahead` | **new:** `…/ahead` — both versions named; the plane's version is unchanged afterwards | neutral | `never downgrade`, both versions named |

"Neutral" means the provider-neutral `Opener` the orchestrator tests build
over `plane.Open`; its remedies are the probe's neutral wording, and the local
composer's localization (`dataplane-migrate`) is asserted where the composer is
(`TestLocalizeProbeReplacesNeutralRemediesOnly`). The composition root is
`cmd/dataplanectl`'s `orchestratorOpener`, which is what `recover` runs.

**Never self-migrate is asserted by shape, not by absence of a call.** After
each schema refusal the test reads `schema_migrations` back and requires
exactly what the case planted. That is what the mutants below attack.

## The Operator Run

Against the developer's real local plane (Postgres + SeaweedFS under Compose),
`main` at `72c34080` plus this branch's tests, 2026-09-25. Output verbatim
except for whitespace; the ids are the plane's.

```text
$ make dataplane-up
data plane ready
  postgres  127.0.0.1:55432/maestro
  objects   http://127.0.0.1:59000

$ go run ./cmd/dataplanectl -org checkpoint-1 prompt-pack show   # before provisioning
dataplanectl: resolve organization "checkpoint-1": not found: organization "checkpoint-1"
exit=1

$ go run ./cmd/dataplanectl -org checkpoint-1 -user dr recover   # before provisioning
dataplanectl: recover: not provisioned: organization "checkpoint-1"; run `dataplanectl provision organization` first
exit=1

$ go run ./cmd/dataplanectl -org checkpoint-1 -user dr bootstrap
created organization checkpoint-1 (checkpoint-1)
seeded prompt pack selector: pack-jcs-sha256-v1:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a ("built-in", 0 entries, no roles, maestro [v2.0.0-phase.3.0.0, v2.0.0-phase.4.0.0), built-in of maestro dev; content 01a0db81-4eed-7330-8661-e7d02fee6ab4)
created user dr (dr)
exit=0

$ go run ./cmd/dataplanectl -org checkpoint-1 prompt-pack show
organization checkpoint-1 selects pack-jcs-sha256-v1:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a ("built-in", 0 entries, no roles, maestro [v2.0.0-phase.3.0.0, v2.0.0-phase.4.0.0), built-in of maestro dev; content 01a0db81-4eed-7330-8661-e7d02fee6ab4)
  selector record 01a0db81-4ef1-7284-8e2f-5d1c1f243eb2 at version 1
exit=0

$ go run ./cmd/dataplanectl -org checkpoint-1 -user dr recover
organization checkpoint-1, operator dr: 0 open dispatch(es)
  execution_awaiting_boundary  0
  execution_diverged           0
  execution_superseded         0
  pending_diverged             0
  pending_resumable            0
exit=0

$ go run ./cmd/dataplanectl -org checkpoint-1 -user dr bootstrap   # again: idempotent
existing organization checkpoint-1 (checkpoint-1)
existing prompt pack selector kept: pack-jcs-sha256-v1:44136fa3… (same content 01a0db81-4eed-…)
existing user dr (dr)
exit=0

$ make dataplane-down

$ go run ./cmd/dataplanectl -org checkpoint-1 -user dr recover   # plane stopped
dataplanectl: recover: orchestrator cannot start: the data plane is not ready (object_store_unusable).
  observed: the object store could not be provisioned or reached: provision object storage: check bucket maestro: Get "http://127.0.0.1:59000/maestro/?location=": dial tcp 127.0.0.1:59000: connect: connection refused
  remedy:   start the plane with `make dataplane-up` and check the service
  detail:   data plane not ready (object_store_unusable): the object store could not be provisioned or reached: … Remedy: start the plane with `make dataplane-up` and check the service: … connection refused
exit=1

$ make dataplane-up
data plane ready

$ go run ./cmd/dataplanectl -org checkpoint-1 -user dr recover   # plane back
organization checkpoint-1, operator dr: 0 open dispatch(es)
  (five classes, all 0)
exit=0
```

Three things the run shows that the tests do not, on their own:

- **Order of refusals is the contract's.** `recover` against an unprovisioned
  organization on a ready plane is `not provisioned` with the verb to run;
  against a stopped plane it is a readiness refusal — readiness before
  identity, as item 3 design D3 requires.
- **A stopped local plane presents as `object_store_unusable`, not
  `unreachable`.** Every local guard passes (populated data root, key present),
  and the bucket probe runs before the database probe, so that is the first
  refusal an operator meets. The tests reach `unreachable` with a neutral
  opener over a closed Postgres port; an operator on the local composer will
  see it only when the object store is up and Postgres is not.
- **The organization is real and stays.** `checkpoint-1` is now a tenant on
  the developer's plane with a selector at version 1. That is the point — the
  plane holds it — and the second `bootstrap` shows nothing is rewritten.

## Verification

Commands and outcomes, on this branch at the commit under review.

```text
go vet -tags integration ./internal/orchestrator/ ./cmd/dataplanectl/ ./internal/dataplane/plane/ ./internal/dataplane/stack/    OK
make lint                                                                  clean (gofmt made no change)
go test -count=1 ./cmd/dataplanectl/ ./internal/orchestrator/               ok / ok
go test -tags integration -count=1 ./internal/orchestrator/                ok  (11.6s; restart harness 3 tests + 5 subtests, schema states 7 subtests)
go test -tags integration -count=1 -run TestStartRendersEverySchemaState -v ./internal/orchestrator/   7/7 PASS
go test -count=1 -run TestStart -v ./cmd/dataplanectl/                     6/6 rows PASS + untouched-marker PASS
make test                                                                  ok, 86 packages, exit 0
```

The `integration`-tagged suites need the Docker data plane and are run here,
not in the reviewer's sandbox.

### Mutants

Each new assertion was attacked with a production-shaped mutant, run, read
for the line it died on, and the source restored (`git diff --stat` empty
afterwards). A mutant that dies for a reason other than the one the
assertion was written for proves nothing, so the dying line is recorded.

| # | Mutant (production code) | Test | Died on |
| --- | --- | --- | --- |
| M1 | `postgres.(*tx).resolutionOf` overlays the **live** installation's revision and display name onto the persisted resolution — a reader that follows the installation record | `TestRestartRecoversCommittedWork` | `the fresh process read resolution {… InstallationRevision:2 … DisplayName:moved after dispatch} but the dispatch persisted {… InstallationRevision:1 … DisplayName:built-in}` |
| M2 | `plane.Open` migrates a `schema_behind` plane on the way in and re-probes | `TestStartRendersEverySchemaState/behind`, `…/never migrated…` | `want a StartupRefused, got not provisioned` — the plane was silently brought current and `Start` proceeded to identity |
| M2b | `plane.Open` refuses as before but runs `migrations.Up` on the way out | same two | `startup moved the plane to 23 (dirty=false): the Orchestrator never migrates` and `startup created schema_migrations on a never-migrated plane` |
| M3 | `stack.objectStoreUnusable` returns an untyped error — no cause, no remedy | `TestStartRendersEveryLocalNotReadyState/object store unusable` | `want a StartupRefused, got orchestrator: open the data plane: … connection refused` |

M1's first application did not apply — the anchor string matched two sites
and the script refused — and the test passed against unmutated code. That
green run is recorded here as the non-result it was; the mutant was
re-anchored and run again.

## What This Checkpoint Does Not Claim

- **No agent has run**, and nothing here could have made one run. The
  checkpoint says so; the projection class `execution_awaiting_boundary` is
  the furthest a dispatch gets in block A.
- **Resolvable, not executable.** The built-in pack is empty and declares no
  roles by item 4's design (D1, plan amendment 1). "A resolvable prompt-pack
  selector" is demonstrated in full; that a dispatch could start a principal
  under it is item 6's, with the coverage and restart-time compatibility
  re-checks (item 4 design D8, D11).
- **Epic and Story creation is seam and package API only** (item 3 design D3).
  The first two clauses are therefore demonstrated by the integration harness
  and not by an operator verb; `recover` is the only Orchestrator verb the CLI
  exposes, and it changes nothing.
- **"Durably checkpointed" means item 3's D8**: committed artifacts, control
  rows and a recovery projection — there is no checkpoint table, by decision.
- **The restart harness's other writer plants dependency edges directly**
  through the seam's transaction, because the edge writer is item 10's.
- **The stopped-plane demonstration exercised one row live**
  (`object_store_unusable`). The other ten are demonstrated by the tests over
  scratch planes and a disposable database; none was reproduced by hand on the
  developer's plane, and the record does not say otherwise.

## Observations For Later

Not defects in the checkpoint; recorded so they are not rediscovered.

- **`StartupRefused.Error()` renders the diagnostic three times** when the
  wrapped error is itself a `readiness.Error`: the `observed:` line, the
  `remedy:` line, and then `detail:` re-renders the readiness error's own
  "data plane not ready (cause): observed. Remedy: remedy: cause chain". Item
  3's rendering; the `detail:` line was added for the unreachable and
  unreadable cases, where the wrapped error carries the driver's complaint and
  nothing else does. A rendering that appends only the chain *below* the
  readiness error would say everything once. Left for a follow-up.
- **The local composer's first refusal is the object store's**, so an
  operator whose Postgres is down but whose SeaweedFS is up meets
  `unreachable`, and one whose whole plane is down meets
  `object_store_unusable`. Both are correct and both name `dataplane-up`; the
  asymmetry is worth a line in the operator docs when there are operator docs.

## Exit Checklist Items This Checkpoint Settles

Marked in [`plan_scope.md`](plan_scope.md#exit-checklist) with this record as
the evidence:

- *Prompt pack identity is resolved once at dispatch and reused verbatim
  across restarts.* — the restart harness's resolution assertion with the
  installation moved between processes.
- *`StateStore.Save(id, any)` is gone, and no workflow state persists through
  a non-atomic write.* — `pkg/state` does not exist; the one remaining mention
  of `StateStore` in the tree is a comment recording its retirement
  (`pkg/agent/internal/core/machine.go:387`).
- *`paths.Bootstrap` is not imported from above the seam.* — the closure
  guards `TestSeamClosureReachesNothingLocalOrV1` and
  `TestOrchestratorClosureReachesNothingLocalOrV1`; the only non-`paths`
  reference is the local composer's own `stack.(*Config).Bootstrap`, which is
  below the seam.
- *The locked-plane path is exercised by the Orchestrator's own startup.* —
  `TestStartRendersEveryLocalNotReadyState/root key missing`, through the real
  composition root.
- *Startup is defined and demonstrated for every enumerated not-ready plane
  state, including interrupted recovery.* — the eleven-row table above.

Not settled here, and not marked: *Configuration and secrets have a live
reader* is half done — the selector is the key registry's live reader (item
4); the secret's is item 5's forge token. Everything under checkpoints 2–4
stands.

## References

- [Phase 3 scope and plan](plan_scope.md) — block A, Checkpoint 1, exit checklist.
- [Item 3 design](design_orchestrator-seam.md) — D3 (opener and startup ownership), D5 (the enumerated states), D8 (what a durable checkpoint is), D13 (restart proved in a new process).
- [Item 4 design](design_prompt-packs.md) — D1 (empty built-in), D8/D11 (dispatch-and-start checks deferred to item 6), plan amendment 1.
- [ADR 0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) §5 — resolve once at dispatch.
- [Build process](../process_build.md) — Defect-Shaped Verification, which the mutant table follows.
