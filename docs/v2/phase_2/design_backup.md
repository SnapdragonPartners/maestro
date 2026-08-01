+++
title = "Phase 2 Item 8 Design: Cold Backup And Restore"
edit_date = "2026-07-31"
status = "draft"
summary = "Mini-plan for Phase 2 item 8: cold backup as a whole-root tree copy with no exclusion list, quiescing by stopping the composed stack so completeness does not depend on a registry, one enumeration of the service set replacing three copies and conformance-tested against the Compose file, restore that preserves bind-mount inodes and refuses a populated root, a key the backup never reads at all, and post-restore validation that recomputes artifact and blob digests."
type = "design"
+++

# Phase 2 Item 8 Design: Cold Backup And Restore

Status: **draft** — awaiting Codex and DR review.

Implements [Phase 2 plan](plan_scope.md) item 8 under [ADR 0022](../../adr/0022-v2-data-plane.md) (cold backup as the MVP baseline, the backup boundary) and the [project-folder spike](../phase_0/spike_project-folder.md) item 4 (backup copies only `data/` and excludes the root-of-trust key, under `MAESTRO_HOME` too). Builds directly on item 2's lifecycle machinery, item 6's object module, and item 7's key-access rule.

Item 8 is sized S and most of its substrate already exists: `stack.Up`/`Down` are the quiesce and restart primitives, `paths.AcquireLock` serializes lifecycle operations across processes, `stack.Reset` is the structural precedent for a destructive operation over bind-mount sources, and item 7's `ErrPlaneLocked` was written for this item by name. This document exists for the five decisions that are not mechanical, plus one defect item 7 left behind.

## What item 8 owes

1. **Backup**: quiesce every writer into the data root, copy it, restart.
2. **The writer set enumerated from the composed stack**, not hardcoded, so Phase 3's airplane-mode local forge joins without reopening this work.
3. **Restore**, validated by the seam's digest checks.
4. **The unlock key excluded by design**, with the two-part restore requirement (backup + key, or secret re-entry) documented *and tested as a failure path*.

## D1. The copy set needs no enumeration; the whole data root goes

Backup copies the data root as a tree and excludes nothing inside it. The config root (key file and bootstrap pointer), cache, and state are outside the boundary and are not consulted.

The reasoning is about which mistake each design makes. An exclusion list inside the data root, or a copy driven by a registry of known services, fails *silently and in the losing direction*: a Phase 3 service that writes under the data root without registering itself produces a backup that appears to succeed and is missing data nobody discovers until a restore. Copying the root wholesale fails in the safe direction — an unregistered writer's data is included whether or not anyone remembered it.

This also settles what the plan's "enumerated from the composed stack" constraint is actually for. It is not for selecting what to copy. It is for **stopping** and for **verifying**, which D2 covers.

The lifecycle lock file (`.maestro-dataplane.lock`, at the data root) is copied like everything else rather than excluded. Its contents are empty by design and `flock` state lives in the open file description, never on disk, so a copied lock file carries nothing. Excluding it would mean opening an exclusion list, which is the mechanism D1 exists to avoid.

## D2. One service enumeration, conformance-tested against Compose

Today the service set is written out three times as a literal `[]paths.Service{paths.ServicePostgres, paths.ServiceMinIO}` — in `up`, in `dataRootIsEmpty`, and in `Reset` (`internal/dataplane/stack/stack.go`). Three copies is exactly the shape that makes Phase 3's forge a multi-site edit with one site forgotten, and the forgotten site is silent: a `dataRootIsEmpty` that does not know about the forge would call a plane holding forge data "fresh" and let `up` mint a new root key over it.

Item 8 replaces all three with one exported enumeration, `paths.Services()`, returning the closed set in a stable order.

The enumeration lives in Go, not in parsed YAML, and a **test** — not the runtime — asserts it against the Compose file: the set of Compose services carrying a bind mount under the data root must equal `paths.Services()`. This satisfies the plan's constraint where it bites (adding a service to `compose.yaml` without registering it fails a test, and the reverse fails too) without making the backup path depend on YAML parsing at runtime, where a parse error becomes a backup failure. `stack.loadImagePins` is the precedent for reading beside the Compose file; the compose-conformance test extends the `compose_test.go` already in the package.

Phase 3's forge joins by adding one constant, one Compose service, and nothing else.

## D3. Quiescing is `down`, so completeness is structural

Backup quiesces by taking the whole Compose project down, which stops every service in it whether or not that service is in `paths.Services()`. Completeness of the quiesce therefore does not depend on the enumeration being right — a service missing from `paths.Services()` is still stopped, because `compose down` is project-wide. The enumeration's job is verification, not coverage.

Two writer classes are outside Compose and worth naming rather than leaving to be discovered:

- **In-process Maestro writers** (a running Orchestrator with a connection pool) are not stopped by backup and are not required to be down. Postgres shuts down cleanly under `compose down`, so the on-disk cluster is consistent regardless of what was connected; the Orchestrator sees connection errors, which is a liveness event, not a corruption one.
- **A concurrent lifecycle operation** is excluded by the existing lock. Backup and restore both run under `lockLifecycle` for their entire duration, the same as `up`, `down`, and `reset`, for ADR 0027's reason: recovery must not run concurrently with a live writer.

The cross-store window deserves one sentence because item 6 already closed it. Stopping Postgres and MinIO at slightly different moments can catch a write mid-sequence, but item 6's commit order (object, then attachment row, then artifact and pins in one transaction) means the surviving state is an unreferenced blob, never a row pointing at a missing one. That is the same state a crash leaves, and `up`'s claim reconciliation already handles it. A cold backup is therefore no more hazardous than a power cut, which is the bar ADR 0022 set for the MVP baseline.

## D4. Restore preserves inodes and refuses a populated root

Restore's sequence, all under the lifecycle lock:

1. Take the stack down (idempotent if it is already down).
2. Validate the source: it must contain a directory for every member of `paths.Services()`. A source missing one is rejected before anything is deleted, so a mistyped path cannot destroy a plane.
3. Refuse if the data root is non-empty, unless `-force`. This mirrors `reset`'s `FORCE=1` convention rather than inventing a second one.
4. Clear the data root's *contents* with `utils.CleanDirectoryContents`, never `os.RemoveAll` on the root or on a service directory. These are bind-mount sources: a recreated directory has a new inode and leaves any existing mount pointing at the old one. `Reset` already documents and follows this rule; restore inherits it.
5. Copy the archive in.
6. Release the lock, then `up`, then verify (D6).

Restore adds no new key-access lifecycle value, which is the point of D5.

*To verify at implementation:* whether `os.CopyFS` in Go 1.26.3 preserves mode bits closely enough for the `0700` roots and the Postgres cluster's file modes, or whether the copy needs to be hand-rolled. Treated as an implementation detail to check against the pinned stdlib source, not a design question — but it is checked, not assumed.

## D5. Backup never reads the key; restore never writes one

**Backup does not read the root key at all.** It needs none: `down` already builds its Compose environment from `placeholderKey()`, and copying files needs no credentials. So the exclusion in the plan's "the unlock key is excluded by design" is not an exclusion rule that could be got wrong — the code path never holds the key. The test asserts the stronger property directly: the key file's bytes appear nowhere in the archive.

That property is worth asserting rather than reasoning about, per the unobservable-property lesson in the [exit record](notes_exit-record.md)'s item 7 post-mortem — a guarantee no test can fail for is a guarantee nobody is holding.

**Restore's failure path is already implemented and needs only a test.** After restore the data root is populated, so `up`'s `rootKeyFor(lifecycleUp)` takes its `LoadOnly` branch; with no key file present it returns `ErrPlaneLocked`, whose doc comment in item 7 says in as many words that it is "the observable restore state item 8 builds on: refuse, supply the original key, open." Item 8 therefore tests the two-part restore requirement as a sequence — restore, `up` refuses with `ErrPlaneLocked`, place the key, `up` succeeds — rather than adding code for it.

Nothing in item 8 constructs a `secret.KeyFile` with `MayCreate`. Only `rootKeyFor` decides create-versus-load, and a structure test from item 7 enforces it.

## D6. Validation recomputes digests rather than trusting the copy

"Validated by the seam's digest checks" means a `verify` pass that, against the restored and running plane:

- recomputes each artifact's JCS digest from its stored envelope and compares it to the recorded digest (item 4's construction), and
- reads every referenced attachment blob through the object module, which verifies the content digest on read (item 6), so a blob that did not survive the copy fails loudly.

This is the only step that proves a copied Postgres cluster and a copied object store are still consistent *with each other* — the failure a whole-root copy could plausibly produce is a torn pair, and neither store can detect it alone. The pass is a full scan, which is right at Phase 2 scale and is flagged in the exit record as something that needs bounding before the plane holds real volume.

`verify` is its own verb, not only a step inside restore, so it can be run against a plane whose provenance is in doubt.

## D7. Command surface

Three verbs on `cmd/dataplanectl`, with make targets following the existing conventions:

| Verb | Make target | Notes |
| --- | --- | --- |
| `backup -to <dir>` | `dataplane-backup DEST=<dir>` | Refuses a non-empty destination. |
| `restore -from <dir>` | `dataplane-restore SRC=<dir> [FORCE=1]` | `FORCE=1` required over a populated root, as `reset`. |
| `verify` | `dataplane-verify` | Runs against the live plane. |

## Defect found while planning: a promised path that does not exist

Item 7's `rootKeyFor` returns, in a shipped error message, "Restore the key file beside the backup, **or run the new-key recovery path**". No new-key recovery path exists anywhere in `internal/dataplane/`, and on inspection none can: the Postgres password and the object-store credentials are derived from the root key, so a plane whose key is lost cannot be opened by any new key. What the message calls recovery is re-provisioning after data loss.

Recommendation: **reword the message** rather than implement it, since the honest instruction is "the data cannot be opened without the original key; `dataplane-reset` discards the plane and starts over". Item 8 is the right place — it is the item that makes the surrounding sequence real and tests it.

Flagged for DR and Codex rather than decided unilaterally, since it edits an accepted item's shipped text.

## Test plan

Behind the `integration` build tag where a real stack is needed, per the phase's testing rule.

1. **Round trip**: populate a plane (artifact with an attachment and a pin), back up, `reset`, restore, verify — and read the artifact back.
2. **Two-part restore**: restore without the key file, assert `up` fails with `ErrPlaneLocked`, place the key, assert `up` succeeds. The failure path is the requirement, so it is asserted, not narrated.
3. **Key exclusion**: the key file's bytes appear nowhere under the archive.
4. **Compose conformance**: `paths.Services()` equals the Compose services bind-mounting under the data root — asserted in both directions, and proven to fail by temporarily adding a service to one side only.
5. **Inode preservation**: the data root's and service directories' inodes are unchanged across a restore.
6. **Refusals**: restore onto a populated root without `-force`; restore from a source missing a service directory, asserted to leave the existing plane intact.
7. **Torn-pair detection**: corrupt one blob in a backup, assert `verify` fails and names it. Without this, `verify` is untested for the one failure it exists to catch.

## Related documents

- [Phase 2 plan](plan_scope.md) item 8 and the exit checklist entry it satisfies.
- [ADR 0022](../../adr/0022-v2-data-plane.md) (cold backup as MVP baseline; online snapshot deferred as [backlog candidate 2](../notes_adr-backlog.md)), [ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md) (destructive recovery under the resource's lock).
- [Project-folder spike](../phase_0/spike_project-folder.md) items 2 and 4: the backup boundary and the key exclusion.
- [Item 2 design](design_local_stack.md) (roots, lock, lifecycle), [item 6 design](design_object_module.md) (commit order, digest-on-read, claims), [item 7 design](design_config_secrets.md) (`rootKeyFor`, `ErrPlaneLocked`).
