+++
title = "Phase 2 Item 8 Design: Cold Backup, Restore And New-Key Recovery"
edit_date = "2026-07-31"
status = "draft"
summary = "Mini-plan for Phase 2 item 8: cold backup as a whole-root tree copy with no exclusion list, a keyless stop/start quiesce protocol measured against the pinned images, the service set derived from resolved Compose configuration rather than a Go registry, restore that preserves every bind-mount inode and the held lock file under one lock spanning stop through verification, containment guards against overlapping source and destination, digest revalidation across both artifact families and every attachment, and the new-key recovery path item 7 measured and assigned here."
type = "design"
+++

# Phase 2 Item 8 Design: Cold Backup, Restore And New-Key Recovery

Status: **draft** — revised after Codex review round 1, which found six P1s. Awaiting round 2.

Implements [Phase 2 plan](plan_scope.md) item 8 under [ADR 0022](../../adr/0022-v2-data-plane.md) (cold backup as the MVP baseline; restore from "the backup plus the key file, **or** re-entry of secrets") and the [project-folder spike](../phase_0/spike_project-folder.md) item 4 (backup copies only `data/` and excludes the root-of-trust key, under `MAESTRO_HOME` too). Builds on item 2's lifecycle machinery, item 6's object module and sweep, and item 7's key-access rule and measured recovery procedure.

## Scope correction: item 8 is larger than S

The plan sizes item 8 **S**. Item 7's design — accepted *after* that plan — measured the new-key recovery procedure and assigned it here by name, including native-Linux CI verification (see [item 7 design](design_config_secrets.md), "The other restore path ADR 0022 promises"). Round 1 of this document proposed rewording that obligation away; Codex refused it correctly, and the refusal stands as the record.

With recovery included, item 8 is **M**: three lifecycle verbs, a guarded destructive recovery operation, a Compose-derivation refactor (D2), and a native-Linux CI job. Flagged for DR as a plan fact rather than absorbed silently — the alternative is splitting recovery into its own item, which is DR's call, not this document's.

## What item 8 owes

1. **Backup**: quiesce every writer into the data root, copy it, restart.
2. **The writer set derived from the composed stack**, not hardcoded, so Phase 3's airplane-mode local forge joins without reopening this work.
3. **Restore**, validated by the seam's digest checks.
4. **The unlock key excluded by design**, with the two-part restore requirement documented *and tested as a failure path*.
5. **New-key recovery** — ADR 0022's second restore branch, per item 7's measured procedure.

## D1. The copy set needs no enumeration; the whole data root goes

Backup copies the data root as a tree and excludes nothing inside it. The config root (key file and bootstrap pointer), cache, and state are outside the boundary and are never read.

The reasoning is about which mistake each design makes. An exclusion list, or a copy driven by a registry of known services, fails *silently and in the losing direction*: a Phase 3 service writing under the data root without registering itself produces a backup that appears to succeed and is missing data nobody discovers until a restore. Copying the root wholesale fails in the safe direction — an unregistered writer's data is included whether or not anyone remembered it.

The lifecycle lock file (`.maestro-dataplane.lock`) is copied like everything else rather than excluded: its contents are empty by design and `flock` state lives in the open file description, never on disk. Opening an exclusion list is the mechanism D1 exists to avoid. Restore treats it specially, which is a different matter — see D5.

## D2. The service set is derived from resolved Compose configuration

Round 1 proposed a Go-side `paths.Services()` registry with a Compose conformance test. Codex ruled that this remains a hardcoded registry and therefore changes the mechanism the binding plan approved, which a design document cannot do on its own. That is correct, and the derivation is achievable — the reason round 1 avoided it is removable.

**The circularity, and how it breaks.** Today `compose.yaml` parameterizes each mount source separately (`${MAESTRO_PG_DATA_DIR}`, `${MAESTRO_MINIO_DATA_DIR}`), and `Config.composeEnv` computes those values from the `paths.Service` constants. So resolving the Compose file requires already knowing the set — deriving the set from the resolved file is circular. Breaking it takes one change: **`compose.yaml` mounts `${MAESTRO_DATA_ROOT}/postgres` and `${MAESTRO_DATA_ROOT}/minio`**, so a single variable resolves every source and the service-to-directory mapping lives entirely in the Compose file, which is where the plan wants it.

**The derivation.** `stack.dataDirs()` runs `docker compose config --format json`, takes every service's `bind`-type volumes, and keeps those whose resolved source lies under the data root. It returns the service-to-directory mapping in sorted order. Its consumers are `up`'s pre-creation, `dataRootIsEmpty`, `Reset`, backup's post-copy verification, and restore's source validation — the literal `[]paths.Service{paths.ServicePostgres, paths.ServiceMinIO}` currently written three times in `stack.go` (lines 86, 242, 486) collapses to one call.

The dangerous of those three is `dataRootIsEmpty`: a copy that does not know about the Phase 3 forge would call a plane holding forge data "fresh" and let `up` mint a new root key over it.

**What this retires, and what replaces its guarantee.** `paths.Service`, `knownServices`, `ServiceDataDir`, and `EnsureServiceDataDirs` largely retire. Their load-bearing property is a traversal guard — a service name of `../config` would point a container's bind mount at the directory holding the unlock key. The replacement is stronger because it validates the resolved path rather than the name: every derived source must be a **direct child of the data root**, refusing the root itself, any ancestor, and anything outside. A `compose.yaml` mounting outside the data root is rejected at every lifecycle operation rather than at review.

**Costs, stated:** one `docker compose config` subprocess per lifecycle operation (computed once and passed down), a malformed Compose file becoming a lifecycle failure rather than a review failure, and a refactor reaching into an accepted item's surface. If DR prefers to keep item 2's structure intact, the alternative is an explicit `plan_scope.md` amendment recording that the set is a reviewed Go registry — that is a plan change, and it needs DR, not this document.

## D3. Quiescing is `compose stop`, not `down`

Round 1 said backup quiesces with `compose down`. Codex found that this contradicts D5: `down` removes the containers, so restarting through `up` must re-render their real credentials, which reads the root key. Both claims cannot hold.

**Backup uses `compose stop` and `compose start`.** Stopped containers retain the environment they were created with, so `start` restores the plane without re-rendering credentials and without reading the key. Compose still substitutes variables to parse the file; `composeEnv(placeholderKey())` covers that, exactly as `down` already does today.

**Measured, not assumed:** the pinned image `postgres@sha256:3a82e1f5…` carries `STOPSIGNAL SIGINT`. That matters more than it looks. Docker's default `SIGTERM` is Postgres's *smart* shutdown, which waits for clients to disconnect and would therefore run out the stop timeout and be `SIGKILL`ed — capturing a crashed cluster in every backup taken while anything held a connection. `SIGINT` is *fast* shutdown: active transactions abort, the cluster checkpoints and exits cleanly. Backup passes an explicit generous `--timeout` rather than relying on the ten-second default, and the round-trip test asserts the restored cluster required no crash recovery.

Two writer classes sit outside Compose and are named rather than left to be discovered:

- **In-process Maestro writers** (a running Orchestrator with a connection pool) are not stopped and need not be. Fast shutdown aborts their transactions and the on-disk cluster is consistent; they see connection errors, which is a liveness event, not a corruption one.
- **Concurrent lifecycle operations** are excluded by the existing lock (D4).

The cross-store window is already closed by item 6: its commit order (object, then attachment row, then artifact and pins in one transaction) means a write caught mid-sequence leaves an *unreferenced* blob, never a row pointing at a missing one. Reclaiming that residue takes two steps in order — attachment truncation removes the rows, and only then can the object sweep collect the objects. (`up`'s claim reconciliation handles surviving *deletion claims*, a different mechanism; round 1 misattributed this and Codex corrected it.) A cold backup is therefore no more hazardous than a power cut, which is the bar ADR 0022 set for the MVP baseline.

## D4. One lock spans stop, copy, restart and verification

Round 1 released the lock before restarting. That leaves a window in which `reset`, `migrate`, or a second `restore` can act on a half-restored plane — the ADR 0027 hazard the lock exists for.

Both verbs hold `lockLifecycle` from before the stop until after verification, and both call the **internal lock-assuming forms** (`up`, `down`, and new `stop`/`start` helpers) rather than the exported ones, because `flock` is not re-entrant and the exported forms would deadlock against the caller. `Reset` already establishes this pattern and its comment says why.

**Restart is attempted on every copy outcome.** A copy failure that leaves the plane stopped is a worse outcome than the copy failure alone, so the restart runs in a deferred step and its error is joined to the copy's rather than replacing it — the operator needs both facts.

**One deliberate exception, defined rather than implied.** A restore whose plane cannot be opened because the root key is absent terminates **stopped**, with `ErrPlaneLocked` and a nonzero exit. That is a successful restore awaiting its second part, not a half-finished one: the files are in place and the next `dataplane-up` with the key present completes the sequence. The lock is released by process exit, as `flock` always is; the durable state is "restored and stopped", which the operator can act on.

## D5. Restore preserves every inode, including the lock's

Round 1 said restore clears the data root with `utils.CleanDirectoryContents`. Codex found the defect: that helper `RemoveAll`s every child, which unlinks the **held** lock file — letting another process create and lock a fresh inode at the same path, producing two simultaneous "exclusive" holders, the precise failure item 2's never-unlink rule exists to prevent — and deletes the service directories, which are the bind-mount sources this design promises to preserve. On macOS a recreated directory has a new inode and any existing mount keeps pointing at the old one.

Restore therefore never removes anything at the top level of the data root. It works one directory down:

| Top-level entry | Action |
| --- | --- |
| `.maestro-dataplane.lock` | Retained untouched. Never copied over, never removed. |
| Directory present in both live root and archive | Contents cleared in place with `CleanDirectoryContents`, archive contents copied in. Inode preserved. |
| Directory in the archive only | Created, then populated. This is how a plane restored onto a fresh machine, or a Phase 3 service added since, arrives — the archive is authoritative, so nothing is silently omitted. |
| Directory in the live root only | Contents cleared in place, directory left empty. It is stale plane data the archive says should not exist; removing the directory itself would break a mount that may already reference it. |
| Non-directory entry in the archive | Copied. Refused only if it would overwrite the lock file. |

Restore's full sequence, all under one lock: stop the stack → validate containment (D6) → validate the source contains a directory for every service the resolved Compose configuration expects, rejecting *before* anything is deleted so a mistyped path cannot destroy a plane → refuse a populated root without `-force`, mirroring `reset`'s existing `FORCE=1` convention → apply the table above → `up` → verify (D7).

*To verify at implementation:* whether `os.CopyFS` in Go 1.26.3 preserves mode bits closely enough for the `0700` roots and the cluster's file modes, or whether the copy needs hand-rolling. Checked against the pinned stdlib source, not assumed.

## D6. Overlapping source and destination are refused before anything runs

Neither verb currently guards path containment, and both fail badly without it: a backup destination inside the data root produces a recursive self-including copy, and a restore source inside the data root can be deleted by restore before it is read.

Both verbs resolve source and destination to absolute canonical paths with symlinks evaluated along the whole ancestry, then refuse **equality or ancestry in either direction** — before stopping the stack and before deleting anything. Symlink evaluation is the part worth stating: a destination that is a symlink into the data root, or one whose *parent* is, passes a naive string comparison and is exactly the case the regression tests cover, direct and symlinked both.

## D7. Validation recomputes digests rather than trusting the copy

"Validated by the seam's digest checks" means a `verify` pass against the restored, running plane:

- **Management artifacts**: recompute both `payload_digest` and the full `review_digest`. Two columns, and checking only the payload would leave the review binding — the thing an accepted artifact's authority rests on — unverified.
- **Audit artifacts**: recompute `payload_digest`. That family has no `review_digest` by design.
- **Attachments**: every row in `binary_attachments`, not a "referenced" subset, which is ambiguous and would silently skip rows. Each blob is read through the object module, which verifies the content digest on read, and **each reader is drained to EOF** — a digest verified on a stream nobody finished reading is not verified.

This is the only step proving a copied Postgres cluster and a copied object store are still consistent *with each other*. A torn pair is the failure a whole-root copy can plausibly produce, and neither store can detect it alone.

The pass is a full scan, right at Phase 2 scale and flagged in the exit record as needing bounds before the plane holds real volume. `verify` is its own verb, not only a step inside restore, so it can run against a plane whose provenance is in doubt.

## D8. New-key recovery, per item 7's measured procedure

ADR 0022 promises restore from the backup **plus the key**, *or* re-entry of secrets. Item 7 delivered the first branch and measured the second against the pinned images, assigning the guarded operation here. Item 8 implements it as `dataplanectl recover-key`, refusing without `-force`:

1. Mint a new root key (the one place besides first-`up` provisioning that may, and it is a new `lifecycle` value so `rootKeyFor` remains the only decider).
2. Start Postgres **socket-only** — `-c listen_addresses=''`, no published port, no network attachment, an `hba_file` carrying `local all all trust` and nothing else — as `${MAESTRO_UID}:${MAESTRO_GID}`, the same identity the normal container runs as. Trust authentication means anyone who can open a connection owns the database, so the absence of a listener is the security boundary, not a convenience.
3. `ALTER USER` through the container's Unix socket via `docker exec`, setting the password derived from the new key.
4. Restart with no overrides; verify the new credential authenticates **over the network by service name**, not from inside the container, whose `pg_hba` trusts local connections and would make the check vacuous. Item 7 walked into that trap and recorded it.
5. Delete every row in the secrets family — every ciphertext is undecryptable under the new key — leaving other families untouched. Item 7 built the vault so this drops wholesale.
6. MinIO needs no step: its credentials are environment, not baked into the data directory, so the store follows the new key. Item 7 measured this.

**The native-Linux CI job is a requirement, not a nicety.** Item 7's measurements were taken on macOS, where Docker Desktop virtualises bind-mount ownership, and item 2's history is that uid handling over a `0700` host-owned mount is precisely where the two platforms diverge. Item 7 says item 8 must exercise the sequence in native-Linux CI rather than inherit a developer-machine result.

## D9. Command surface

| Verb | Make target | Notes |
| --- | --- | --- |
| `backup -to <dir>` | `dataplane-backup DEST=<dir>` | Refuses a non-empty or overlapping destination. Keyless. |
| `restore -from <dir>` | `dataplane-restore SRC=<dir> [FORCE=1]` | `FORCE=1` over a populated root, as `reset`. |
| `verify` | `dataplane-verify` | Runs against the live plane. |
| `recover-key` | `dataplane-recover-key FORCE=1` | Destructive: drops every secret and rewrites a database credential. |

## Test plan

Behind the `integration` build tag where a real stack is needed, per the phase's testing rule.

1. **Round trip**: populate a plane (artifact with attachment and pin), back up, `reset`, restore, verify, read the artifact back.
2. **Clean shutdown**: the restored cluster's log shows no crash recovery, proving the `SIGINT` fast-shutdown path (D3) rather than assuming it — asserted with a client connection held open across the backup, which is the case `SIGTERM` would have broken.
3. **Two-part restore**: restore without the key, assert `up` fails with `ErrPlaneLocked` and the plane is left stopped (D4's defined terminal state), place the key, assert `up` succeeds.
4. **Key exclusion**: the key file's bytes appear nowhere under the archive.
5. **Compose derivation**: adding a bind-mounted service to `compose.yaml` changes the derived set with no Go change; a service mounting outside the data root is refused. Both proven to fail when the guard is removed.
6. **Inode preservation**: the data root, every service directory, and the lock file retain their inodes across a restore. This is the round-1 defect, so it is asserted directly.
7. **Lock coverage**: a concurrent `reset` blocks for the whole of a restore, including the restart, rather than only its copy.
8. **Containment**: backup into the data root, restore from inside it, and both again through a symlinked parent — all refused before the stack stops.
9. **Restart on failure**: an injected copy failure still leaves the plane running, with both errors reported.
10. **Refusals**: restore onto a populated root without `-force`; restore from a source missing a service directory, asserted to leave the existing plane intact.
11. **Torn-pair detection**: corrupt one restored object **through the S3 API**, not by editing MinIO's on-disk files — whose representation is not the object body — and assert `verify` fails and names it.
12. **New-key recovery** (native-Linux CI): recovery over a plane with data and secrets; the data survives, secrets are gone, the new credential authenticates over the network by service name, the old one is rejected, and no listener exists during the recovery step.

## Related documents

- [Phase 2 plan](plan_scope.md) item 8 and the exit checklist entry it satisfies.
- [ADR 0022](../../adr/0022-v2-data-plane.md) (cold backup as MVP baseline; both restore branches; online snapshot deferred as [backlog candidate 2](../notes_adr-backlog.md)), [ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md) (destructive recovery under the resource's lock).
- [Project-folder spike](../phase_0/spike_project-folder.md) items 2 and 4: the backup boundary and the key exclusion.
- [Item 2 design](design_local_stack.md) (roots, lock, lifecycle), [item 6 design](design_object_module.md) (commit order, digest-on-read, sweep), [item 7 design](design_config_secrets.md) (`rootKeyFor`, `ErrPlaneLocked`, the measured recovery procedure).
