+++
title = "Phase 2 Item 8 Design: Cold Backup, Restore And New-Key Recovery"
edit_date = "2026-08-03"
status = "live"
summary = "Mini-plan for Phase 2 item 8: cold backup as a whole-root tree copy with no exclusion list, a keyless stop/start quiesce protocol measured against the pinned images, a whole-root freshness rule counting any non-directory entry, leaving the service registry only its directory-creation job, restore that preserves every bind-mount inode and the held lock file under one lock spanning stop through verification with a durable incomplete marker and a phase boundary deciding whether failure restarts or stays stopped, archives validated by a completion manifest written last rather than by directory shape, a backup that returns the project to the state it found and waits for the originally-running services to be usable again, a hand-rolled copier because os.CopyFS widens modes rather than preserving them, digest revalidation across both artifact families under the seam's own snapshot and advisory locks, a verification debt carried in its own marker across the two-part restore and settled by the next up on pain of the plane being stopped, and resumable new-key recovery that installs its staged key last."
type = "design"
+++

# Phase 2 Item 8 Design: Cold Backup, Restore And New-Key Recovery

Status: **live** — Accepted by Codex and DR, 2026-08-01, after four review rounds (six, four, five and three P1s). One M-sized item, with a review checkpoint after backup, restore and verify and before new-key recovery. **D4a is approved by Codex and awaiting DR** — the verification debt carried across the two-part restore and its own marker policy, written during implementation because the accepted D4 cleared the incomplete marker on a branch that had verified nothing, which was a hole in D7 rather than the clean exception it read as.

Implements [Phase 2 plan](plan_scope.md) item 8 under [ADR 0022](../../adr/0022-v2-data-plane.md) (cold backup as the MVP baseline; restore from "the backup plus the key file, **or** re-entry of secrets") and the [project-folder spike](../phase_0/spike_project-folder.md) item 4 (backup copies only `data/` and excludes the root-of-trust key, under `MAESTRO_HOME` too). Builds on item 2's lifecycle machinery, item 6's object module and sweep, and item 7's key-access rule and measured recovery procedure.

## Scope correction: item 8 is larger than S

The plan sizes item 8 **S**. Item 7's design — accepted *after* that plan — measured the new-key recovery procedure and assigned it here by name, including native-Linux CI verification (see [item 7 design](design_config_secrets.md), "The other restore path ADR 0022 promises"). Round 1 of this document proposed rewording that obligation away; Codex refused it correctly, and the refusal stands as the record.

With recovery included, item 8 is **M**: four lifecycle verbs, a guarded and resumable destructive recovery operation, and a native-Linux CI job. Codex agreed in round 2 and the [plan](plan_scope.md) row is amended to match; splitting recovery into its own item remains available and is DR's call, not this document's.

## What item 8 owes

1. **Backup**: quiesce every writer into the data root, copy it, restart.
2. **Completeness that does not depend on a hardcoded writer list**, so Phase 3's airplane-mode local forge joins without reopening this work — delivered structurally rather than by enumeration, per the plan amendment in D2.
3. **Restore**, validated by the seam's digest checks.
4. **The unlock key excluded by design**, with the two-part restore requirement documented *and tested as a failure path*.
5. **New-key recovery** — ADR 0022's second restore branch, per item 7's measured procedure.

## D1. The copy set needs no enumeration; the whole data root goes

Backup copies the data root as a tree and excludes nothing inside it. The config root (key file and bootstrap pointer), cache, and state are outside the boundary and are never read.

The reasoning is about which mistake each design makes. An exclusion list, or a copy driven by a registry of known services, fails *silently and in the losing direction*: a Phase 3 service writing under the data root without registering itself produces a backup that appears to succeed and is missing data nobody discovers until a restore. Copying the root wholesale fails in the safe direction — an unregistered writer's data is included whether or not anyone remembered it.

The lifecycle lock file (`.maestro-dataplane.lock`) is copied like everything else rather than excluded: its contents are empty by design and `flock` state lives in the open file description, never on disk. Opening an exclusion list is the mechanism D1 exists to avoid. Restore treats it specially, which is a different matter — see D5.

## D2. Freshness comes from the root; the registry only creates directories

The plan originally required the writer set to be enumerated from the composed stack. That requirement bought three properties: a future forge cannot be omitted from the backup, it cannot be left running during the copy, and `dataRootIsEmpty` cannot ignore its data and let `up` mint a new key over a live plane.

**D1 and D3 deliver the first two structurally** — the backup copies the whole root, and `compose stop` stops the whole project — so neither depends on any list being right. Only the third still needs a mechanism, and it does not need a service list either: **any non-directory entry under the data root makes the plane non-fresh.** That is a stronger rule than enumeration, for D1's reason — it cannot be wrong about a writer nobody registered.

The [plan amendment](plan_scope.md) carrying this is **Accepted — Codex and DR, 2026-08-01**. Round 1 changed the mechanism silently, which a design document cannot do; the amendment is the correct instrument, and the record of the objection stands.

**The rule is any non-directory entry except the lifecycle lock.** Restricting the evidence to regular files and symlinks — round 2's wording — would still call a root holding a FIFO, socket, device node, or unknown special entry fresh, and freshness is the judgement that authorizes minting a key over whatever is there. Anything that is not a directory counts. A traversal that cannot be read is an **error**, never a "fresh" answer: an unreadable root is precisely the case where nothing is known, and the safe reading of nothing-known is not "empty".

**Empty directory trees stay ignorable, and the ordering is why.** `up` calls `EnsureServiceDataDirs` *before* `rootKeyFor` (`stack.go:86` and `:90`), so on a first run the data root already contains empty `postgres/` and `minio/` directories, plus the lock file that `lockLifecycle` just created, when freshness is judged. A rule counting any *content* would call a clean checkout non-fresh, refuse to mint a key, and fail `dataplane-up` from empty — the phase's headline exit criterion. No provisioned plane can hide behind this, because `initdb` writes files, MinIO writes `.minio.sys`, and a forge writes refs.

**The refusal names what it found.** A stray file — a macOS `.DS_Store` from browsing the data directory is the realistic case — makes a genuinely fresh plane look provisioned. Refusing is still the right direction, since minting over a real plane costs every secret in it, but the error must list the offending paths so that case is self-diagnosing rather than a mysterious `ErrPlaneLocked` on a clean machine. An exclusion list for known junk is deliberately not the answer; naming the evidence is.

**What the registry is left doing.** `paths.Service`, `ServiceDataDir`, and `EnsureServiceDataDirs` stay. Their remaining job is *creating* the host-owned bind-mount sources before Compose mounts them (item 2, D2a) — a creation need, not a safety need. Stating that boundary is the point of this section: **the registry creates directories; every safety property derives from the root itself.** The literal `[]paths.Service{paths.ServicePostgres, paths.ServiceMinIO}`, written three times in `stack.go` (lines 86, 242, 486), collapses to one centralized `paths.Services()`, conformance-tested bidirectionally against the shipped Compose file so registry and Compose cannot drift.

Deriving the set from resolved Compose configuration was considered and rejected as disproportionate. It would require re-parameterizing `compose.yaml` to a single `${MAESTRO_DATA_ROOT}` to break the circularity — `composeEnv` currently computes the very mount sources that would be parsed back — and would retire accepted item 2 surface. What it buys over the above is support for arbitrary user-supplied Compose files carrying unknown stateful services, which the closed `paths.Service` model does not offer today and no phase requires.

**`Reset` must follow the same rule, and this is a consequence rather than a nicety.** Round 2 offered it as optional; under a freshness rule that reads the whole root, it is required. `Reset` clearing only the registry's service directories would leave any other top-level entry in place — a forgotten service's directory, a stray file, the restore-incomplete marker — and the next `up` would then judge the root non-fresh and refuse to provision a plane the operator just asked to be wiped. `Reset` therefore clears every top-level directory's contents in place (preserving inodes, per D5) and removes every non-directory entry **except the lifecycle lock**, which is never unlinked. Reset and freshness are two halves of one definition: reset returns the root to exactly the state freshness calls fresh, and a test asserts that composition directly rather than each half separately.

## D3. Quiescing is `compose stop`, not `down`

Round 1 said backup quiesces with `compose down`. Codex found that this contradicts D5: `down` removes the containers, so restarting through `up` must re-render their real credentials, which reads the root key. Both claims cannot hold.

**Backup uses `compose stop` and `compose start`.** Stopped containers retain the environment they were created with, so `start` restores the plane without re-rendering credentials and without reading the key. Compose still substitutes variables to parse the file; `composeEnv(placeholderKey())` covers that, exactly as `down` already does today.

**Backup restores the project to the state it found, which is not the same as "running".** `compose start` only works on containers that still exist, and `dataplane-down` — a supported, ordinary command — removes them. So after a `down`, round 3's design would copy successfully and then fail the restart it promised, turning a clean backup of a stopped plane into an error. Requiring a running project up front would be the other way to resolve it, and it is worse: backing up a stopped plane is the *easiest* case to get right, and refusing it would push operators toward starting a plane they did not want running just to back it up.

Backup therefore **snapshots the project state before touching it** (`compose ps` over the project, recording which containers exist and which are running), stops what is running, copies, and restarts **only the containers that existed and were running**. An already-down plane stays down and the copy is trivially cold; a partially running project is returned to exactly its mixed state. This keeps the operation genuinely keyless, because nothing is ever *created* — only stopped and started.

### D3a. "The state it found" means usable, not merely started (clarification)

*Status: **approved by Codex 2026-08-03, awaiting DR**, alongside D4a. Not a new decision — a statement of what the existing promise entails, and a defect fixed against it.*

`compose start` returns as soon as the containers are started, which is **not** the same as Postgres accepting connections or MinIO answering requests. Backup restarted the project and returned inside that window, so a successful maintenance operation was reporting completion while the outage it had itself caused was still in progress; the first caller to reach for the plane got a connection error out of an operation that said it had finished. A plane that was usable before a backup must be usable after it.

The restart therefore waits for the restarted services to become usable, and the subset rule from the paragraph above **applies to the wait as well as to the start**: it waits for the *originally running* services and no others. Waiting for both unconditionally is not a harmless simplification — against a project with one service deliberately stopped it blocks for the full readiness timeout (measured: ~198s) and fails a backup that was entirely correct, and starting that service to satisfy the wait would break the very promise this section exists to keep.

One consequence worth stating: the restart's own time budget must cover the readiness wait as well as the start, so it is the restart timeout **plus** the readiness timeout. A bound smaller than the wait it contains would cut off a slow-but-healthy restart before the wait's own deadline could be reached — a timeout that fires only on the machines least able to afford it.

**Measured, not assumed:** the pinned image `postgres@sha256:3a82e1f5…` carries `STOPSIGNAL SIGINT`. That matters more than it looks. Docker's default `SIGTERM` is Postgres's *smart* shutdown, which waits for clients to disconnect and would therefore run out the stop timeout and be `SIGKILL`ed — capturing a crashed cluster in every backup taken while anything held a connection. `SIGINT` is *fast* shutdown: active transactions abort, the cluster checkpoints and exits cleanly. Backup passes an explicit generous `--timeout` rather than relying on the ten-second default, and the round-trip test asserts the restored cluster required no crash recovery.

Two writer classes sit outside Compose and are named rather than left to be discovered:

- **In-process Maestro writers** (a running Orchestrator with a connection pool) are not stopped and need not be. Fast shutdown aborts their transactions and the on-disk cluster is consistent; they see connection errors, which is a liveness event, not a corruption one.
- **Concurrent lifecycle operations** are excluded by the existing lock (D4).

The cross-store window is already closed by item 6: its commit order (object, then attachment row, then artifact and pins in one transaction) means a write caught mid-sequence leaves an *unreferenced* blob, never a row pointing at a missing one. Reclaiming that residue takes two steps in order — attachment truncation removes the rows, and only then can the object sweep collect the objects. (`up`'s claim reconciliation handles surviving *deletion claims*, a different mechanism; round 1 misattributed this and Codex corrected it.) A cold backup is therefore no more hazardous than a power cut, which is the bar ADR 0022 set for the MVP baseline.

## D4. One lock spans stop, copy, restart and verification

Round 1 released the lock before restarting. That leaves a window in which `reset`, `migrate`, or a second `restore` can act on a half-restored plane — the ADR 0027 hazard the lock exists for.

Both verbs hold `lockLifecycle` from before the stop until after verification, and both call the **internal lock-assuming forms** (`up`, `down`, and new `stop`/`start` helpers) rather than the exported ones, because `flock` is not re-entrant and the exported forms would deadlock against the caller. `Reset` already establishes this pattern and its comment says why.

**Failure recovery differs between the two verbs, because their destructive phases do.** Round 2 said "restart on every copy outcome", which is right for backup and unsafe for restore.

| Verb | Phase | On failure |
| --- | --- | --- |
| Backup | Any — the authoritative plane is only ever *read* | Restart, and leave no archive behind (below). The error is joined to the copy's rather than replacing it; the operator needs both facts. |
| Restore | Pre-destructive: containment checks, source validation, stop, the populated-root refusal | Restart the original plane. Nothing has been touched. |
| Restore | Destructive: from the first `CleanDirectoryContents` onward | Leave **stopped**. A partial Postgres/MinIO tree must not be started; restarting it would present a torn plane as a live one. |

The boundary is a single point in the code, not a judgement call at each step: restore records that it has entered the destructive phase before clearing anything, and every failure after that point routes to stopped-and-reported.

**Backup publishes atomically, because a failed backup is more dangerous than no backup.** Round 2 left a failed copy's partial tree at the destination. Restore validates an archive by its directory *shape*, so a partial tree containing the service directories passes validation — and the operator reaching for it is by definition someone whose live plane is already in trouble. A truncated archive that presents itself as a completed backup and then replaces a good plane is the worst outcome this item can produce.

Round 3's fix — copy to a temporary sibling and rename — is necessary and not sufficient, because cleanup cannot run if the process is killed. The temporary tree survives, and restore accepts any source path by shape, so a partial tree at a temporary path is still a loaded gun. **Safety cannot depend on cleanup running or on a path being named "temporary".**

An archive therefore carries an explicit completion protocol, and validity is a property of its contents rather than its location:

- An archive is a **wrapper directory** containing `data/` — the copied root — and `manifest.json`.
- The manifest is written, `fsync`ed, **last**. Its presence is the only thing that makes a directory an archive.
- Backup requires the destination **not to exist**, builds the wrapper at a temporary sibling in the destination's parent (same filesystem, so publication is a rename rather than a second copy), `fsync`s the copied files and their directories, writes the manifest, renames the wrapper into place, and then `fsync`s the destination's **parent** so the rename itself is durable.
- **Restore refuses any source without a valid manifest**, before any destructive action. A killed backup's residue has no manifest and is rejected; a residue that *does* have one is, by construction, a complete copy, and accepting it is correct.

The manifest records the archive format version, when it was taken, the source data root, and the top-level inventory (entry names with file counts and total bytes) that restore checks the tree against. It is a **completion protocol, not an integrity protocol** — it proves the copy finished, not that the bytes are good. Integrity is D7's job, after the plane is up, and conflating the two would let a cheap structural check pass for the expensive semantic one.

**The partial restore state is made durable, and it gates every unsafe operation.** Restore writes a `.maestro-restore-incomplete` marker into the data root and `fsync`s it — the marker and its parent directory both — **before the first deletion**, removing it only on success. Otherwise a crashed restore leaves a torn tree that looks exactly like a plane, with nothing between it and a normal startup but an operator's memory of an error message from a previous session.

Guarding only `up` is not enough, since every other verb can act on a torn tree just as harmfully. The matrix is defined in one place and enforced structurally:

| Operation | With the marker present |
| --- | --- |
| `up`, `migrate`, `force-version`, `backup`, `verify`, `recover-key` | **Refuse**, naming the marker and the two ways out. Backing up a torn plane is how a torn plane becomes an archive. |
| `down` | Allowed — stopping something already stopped is harmless. |
| `restore` | Allowed. Resuming is the intended repair. |
| `reset` | Allowed, and removes the marker as part of returning the root to freshness (D2). |

"Enforced structurally" means a test enumerating every lifecycle operation and asserting each one either refuses or appears in the allowlist above, so a verb added later cannot default into permitted by omission — the same shape as item 7's `rootKeyFor` structure test, which caught its own author.

**Backup's restart runs on a fresh bounded context.** The operation context is cancelled by Ctrl-C, and a deferred restart inheriting it would be cancelled before it ran — turning an interrupted backup into a stopped plane, which is the exact outcome the deferred restart exists to prevent. The restart derives its own context with `context.WithoutCancel` plus a timeout.

**One deliberate exception, defined rather than implied.** A restore that completes its copy but cannot open the plane because the root key is absent terminates **stopped**, with `ErrPlaneLocked` and a nonzero exit, and *with the incomplete marker removed* — it is a complete restore awaiting its second part, not a torn one. The files are in place and the next `dataplane-up` with the key present completes the sequence. The lock is released by process exit, as `flock` always is; the durable state is "restored and stopped", which the operator can act on.

### D4a. The verification debt, and its own marker (amendment)

*Status: **approved by Codex 2026-08-02, awaiting DR** — proposed during implementation and reviewed over three rounds (the `verify` permission, which was wrong; the debt-bearing shutdown's coverage, which reached only the settlement step; and the disarming point's structural check). Accepted once DR approves; until then this section and the code implementing it carry Codex's approval alone.*

*The original text above stopped at "restored and stopped", and that was not enough: it clears the incomplete marker on a branch that has verified nothing, so the exception it defines is also a hole in D7.*

The two-part restore is the one branch that completes a copy and **cannot verify it**. Verification needs an open plane, and the plane cannot be opened without its key. Clearing the incomplete marker and stopping there lets a torn pair go live by the intended route: the operator supplies the key, `dataplane-up` starts the plane, and nothing ever recomputes a digest. The state that made verification skippable — a locked plane — is gone by then, and nothing records that the skip happened.

The debt is therefore **handed forward rather than dropped**, in a second marker at the data root, `.maestro-restore-unverified`.

**Two markers and not one, because the states need opposite treatment.** A torn tree must not be started at all; an unverified one **must** be started, because starting it is the only way it gets verified. Folding them into one flag would make the two-part restore uncompletable — the plane would refuse the single operation that can settle its debt.

**Write order.** The unverified marker is written and `fsync`ed **before** the incomplete marker is cleared, so no instant exists in which the plane looks whole and owing nothing. The reverse order has a crash window that produces exactly the state this exists to prevent.

**Settlement is a pass PLUS its consequences,** and that distinction decides the policy table below. A verification pass answers a question; settling the debt means acting on the answer — clearing the marker when the plane is healthy, and stopping the plane when it is not. A verb that runs the pass and does neither has not settled anything.

**Settlement belongs to `up`,** after readiness, migrations and claim reconciliation — the first moment the plane is open. It is the **only** settlement path. A healthy pass clears the marker; anything else does not:

| Settlement outcome | What `up` does |
| --- | --- |
| Verification healthy | Clear the marker. The plane is open and checked. |
| Verification finds problems | **Stop the plane** on a fresh bounded context, return `ErrRestoreUnverifiedPending`, and **retain the marker**. |
| Verification could not run | Same: stop, report the underlying error, retain the marker. |

**The stop covers the whole of a debt-bearing `up`, not its last step.** This is the correction round six forced, and it is the same defect D4's own boundary rule already names: recovery armed beside the failure its author pictured covers that failure and no other. Settlement is the *last* thing `up` does, and everything between `compose up` and it — readiness, bucket provisioning, migration, claim reconciliation — can fail with the containers already started. Each of those returned directly, leaving a live plane that never reached the step which would have condemned it.

The debt is therefore read **once, before Compose is invoked**, and a single deferred stop is armed there and disarmed only after a healthy settlement. One mechanism, spanning the whole region the invariant covers, rather than one mechanism per failure somebody thought of. A plane owing nothing is untouched by it: an ordinary `up` that fails readiness still leaves its containers for the operator to inspect.

Stopping is the load-bearing half. Reporting a torn pair while leaving the plane serving is the worst available outcome — the operator sees an error while clients keep using the plane it condemns — and the marker gates lifecycle verbs, not a client holding a connection string. Retaining the marker matters for the same reason the incomplete one is retained: a debt cleared by a *failed* settlement would let the next `up` start a plane nothing has ever checked, the tear laundered by one failed attempt. The stop runs on `context.WithoutCancel` plus a timeout, for D4's reason — a Ctrl-C'd `up` must still stop the plane it condemned.

**The marker gates verbs, on its own policy table.** `markerPermits` answers the torn question; `unverifiedPermits` answers this one, and they differ on exactly one operation:

| Operation | With a verification debt outstanding |
| --- | --- |
| `up` | **Allowed** — it *is* the settlement. Refusing it strands the plane the debt exists to rescue. |
| `verify` | **Refuse.** This is the answer round five corrected: the obvious reading is that verification should be allowed against a plane owing verification, and it is wrong. The exported `Verify` runs a pass and settles nothing — it neither clears a healthy plane's marker nor stops an unhealthy one, and it cannot sensibly do the second, since it takes no Compose file and a read-shaped verb that stops a running plane as a side effect is a trap. Permitting it would leave a verb that reports "healthy" against an owing plane while the debt survives, which is the most convincing possible way to tell an operator the problem is gone. Nothing is lost: an owing plane is a *stopped* plane, so `verify` against one could only have failed to connect. |
| `down`, `reset`, `restore` | Allowed. Stop, discard, replace. |
| `backup`, `migrate`, `force-version`, `recover-key` | **Refuse.** Backing up an unchecked plane is how an unchecked plane becomes an archive somebody later restores from — and the two-part restore leaves the plane *stopped and owing*, which is a state `backup` is otherwise perfectly happy to copy. `migrate` and `force-version` would apply schema changes to contents nothing has vouched for. |

So the tables agree on every refusal and differ only on `up` — which is the honest shape of the thing. The two states share one hazard, *acting on contents nobody has vouched for*, and diverge on one point only: the operation that makes the vouching possible.

Both tables are enforced by the same structural completeness test over `lifecycles`, and every **guarded** verb consults them through **one** combined guard rather than two calls, so a verb cannot be guarded against the failure its author remembered and open to the other. (`down` and `reset` are guarded by neither, by decision recorded in the call-site table rather than by omission.)

**Neither `reset` nor `restore` clears this marker specially.** Both sweep the data root — D2's whole-root freshness rule and D5's marker-preserving clear respectively — and a plane that has been discarded or replaced owes nothing about contents that are gone. Only a healthy settlement clears it in place.

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

Restore's full sequence, all under one lock: stop the stack → validate containment (D6) → **require a valid `manifest.json`** and check the source's `data/` tree against its inventory, plus a directory for every service in `paths.Services()` → refuse a populated root without `-force`, mirroring `reset`'s existing `FORCE=1` convention → apply the table above → `up` → verify (D7). Every check precedes the first deletion, so neither a mistyped path nor a killed backup's residue can destroy a plane.

**The copier is hand-rolled; `os.CopyFS` is disqualified.** Round 2 left this open "to verify at implementation". The pinned toolchain answers it, and the answer is not a near miss — `os.CopyFS` does not merely fail to preserve modes, it **widens** them. In Go 1.26.3 (`src/os/dir.go`) directories are created with `MkdirAll(newPath, 0777)` and files with `OpenFile(..., 0666|info.Mode()&0777)`, both before umask. Under a typical `umask 022` the `0700` storage roots come back `0755` and the `0600` cluster files come back `0644` — a backup/restore cycle that silently relaxes the permissions on the directory holding the Postgres cluster and the object store, which is precisely what item 2's `ErrRootPermissions` refuses to tolerate and what `Roots.Ensure` would then reject on the next `up`.

The copier therefore walks the tree itself: `MkdirAll` followed by an explicit `os.Chmod` to the source mode (`Chmod` is not umask-filtered, so it is the step that actually sets the bits), regular files copied and then `Chmod`ed the same way, symlinks recreated with `os.Symlink`, and **any other file type refused loudly** rather than skipped — a silently dropped entry is D1's failure mode wearing a different hat. Ownership needs no special handling: Compose runs both services as `${MAESTRO_UID}:${MAESTRO_GID}`, the invoking user, so a same-user restore preserves it naturally. A cross-user restore is out of scope and refused rather than half-supported.

The mode assertions in the test plan are written to fail against `os.CopyFS`, so the disqualification is enforced rather than remembered.

## D6. Overlapping source and destination are refused before anything runs

Neither verb currently guards path containment, and both fail badly without it: a backup destination inside the data root produces a recursive self-including copy, and a restore source inside the data root can be deleted by restore before it is read.

Both verbs resolve source and destination to absolute canonical paths with symlinks evaluated along the whole ancestry, then refuse **equality or ancestry in either direction** — before stopping the stack and before deleting anything. Symlink evaluation is the part worth stating: a destination that is a symlink into the data root, or one whose *parent* is, passes a naive string comparison and is exactly the case the regression tests cover, direct and symlinked both.

## D7. Validation recomputes digests rather than trusting the copy

"Validated by the seam's digest checks" means a `verify` pass against the restored, running plane:

- **Management artifacts**: recompute both `payload_digest` and the full `review_digest`. Two columns, and checking only the payload would leave the review binding — the thing an accepted artifact's authority rests on — unverified.
- **Audit artifacts**: recompute `payload_digest`. That family has no `review_digest` by design.
- **Attachments**: every row in `binary_attachments`, not a "referenced" subset, which is ambiguous and would silently skip rows. Each blob is read through the object module, which verifies the content digest on read, and **each reader is drained to EOF** — a digest verified on a stream nobody finished reading is not verified.

This is the only step proving a copied Postgres cluster and a copied object store are still consistent *with each other*. A torn pair is the failure a whole-root copy can plausibly produce, and neither store can detect it alone.

**Verify runs concurrently with live writers, so it needs the seam's own concurrency vocabulary.** The lifecycle lock excludes `reset` and `down`; it does not exclude the in-process writers D3 deliberately allows to keep running. Between listing a `binary_attachments` row and draining its blob, attachment truncation can delete the row and the object sweep can then reclaim the object — and a verifier that read the row first would report corruption in a plane that is behaving exactly as designed. A verification tool whose failure mode is crying wolf about a healthy plane is worse than none, because the response to it is to distrust the tool.

Verify therefore reuses the mechanisms item 5 and item 6 already established rather than inventing a protocol — but the transaction boundary is load-bearing and round 2 got it wrong. Holding the listing's `REPEATABLE READ` transaction open across the reads makes the recheck useless: the recheck would run against the *listing's* snapshot, in which the row still exists, so it could never produce the skip it exists to produce. A recheck that cannot observe the deletion it is checking for is decoration.

The listing is therefore **materialized and committed** before any blob is read, and each attachment is then processed in its own fresh transaction:

- The listing runs in one **`REPEATABLE READ`** transaction — the same isolation truncation uses — and **commits**, yielding a stable list of candidates rather than a live view.
- For each candidate, a new transaction acquires the **per-`(organization, digest)` advisory lock** that writers and the sweep already serialize on. The sweep establishes "unreferenced" under that lock, so it cannot conclude and delete while verify holds it.
- Under that lock, in the **current** snapshot, verify rechecks that the row still exists, and drains the blob while still holding it. A row a concurrent truncation legitimately removed since the listing is reported as **skipped**, not as damage — the sweep's own reference-recheck-under-lock pattern, inverted.

The pass is a full scan, right at Phase 2 scale and flagged in the exit record as needing bounds before the plane holds real volume. `verify` is its own verb, not only a step inside restore, so it can run against a plane whose provenance is in doubt.

## D8. New-key recovery, per item 7's measured procedure

ADR 0022 promises restore from the backup **plus the key**, *or* re-entry of secrets. Item 7 delivered the first branch and measured the second against the pinned images, assigning the guarded operation here. Item 8 implements it as `dataplanectl recover-key`, refusing without `-force`.

**Recovery must be resumable, because it is the operation most likely to be interrupted.** It runs on a plane somebody is already anxious about, and round 2's ordering — mint the real key first, `ALTER USER`, restart normally, then delete secrets — has two crash windows that leave the plane worse than it started: dying after minting leaves a key that opens nothing, and restarting on the network before the secrets are dropped exposes undecryptable rows to reconnecting callers.

The ordering is therefore staged, and the plane's real key is installed **last**:

1. **Establish exclusive control of the data directory first.** Acquire the lifecycle lock, then `compose stop` the project. The isolated server below opens the same `PGDATA` as the normal container, so starting it against a running plane is not merely untidy; every step after this assumes one postmaster.
2. **Entry versus resume, which are different conditions.** *Initial* entry requires `rootKeyFor` to report `ErrPlaneLocked` — a populated root whose key is absent — which is the situation ADR 0022 describes and keeps recovery from becoming a general-purpose key rotation nobody designed. A *resume* is authorized by the recovery marker instead, and must proceed even when the final key is already installed and the plane therefore no longer reports locked. Requiring `ErrPlaneLocked` on resume would strand exactly the window that most needs finishing.
3. **Stage, don't install — and order the two artifacts.** Mint the new key to a staged path and `fsync` it **first**, then write and `fsync` the recovery marker naming it. The order matters on resume: a staged key with no marker means nothing downstream can have run — the marker is written before the isolated server ever starts — so it is safe to delete, while a marker without its staged key is an incoherent state that refuses rather than guesses. The live key path stays absent throughout, so an interrupted recovery leaves a plane that is still honestly locked rather than one holding a key that opens nothing. This is a new `lifecycle` value, so `rootKeyFor` remains the only decider of create-versus-load.

4. **The marker names the recovery container, because the container can outlive the process.** Killing `dataplanectl` does not stop the Docker container it started: the isolated postmaster keeps running, keeps owning `PGDATA`, and the dead process's `flock` is released — so the next operation can acquire the lifecycle lock and start working against a data directory another postmaster still holds. The recovery container therefore has a **deterministic identity recorded in the marker**, and every resume stops and removes any survivor *before* the SCRAM probe. Ordinary Compose lifecycle does not cover this container; it is outside the project by design, so nothing else would ever clean it up.
5. **Determine whether the credential has already moved, with a probe that can actually fail.** The recovery server's `local all all trust` HBA accepts *any* password over the socket, so a probe through it authenticates whether or not `ALTER USER` ever ran — item 7's in-container authentication trap, in a new place. The probe therefore runs against a socket-only server started with `local all all scram-sha-256`: still no listener, still no published port, but real authentication. Success means the transaction below already committed.
6. **If it has not: one transaction, before any network exposure.** Restart the isolated server with the trust HBA — `-c listen_addresses=''`, no published port, no network attachment, as `${MAESTRO_UID}:${MAESTRO_GID}`, the identity the normal container runs as — and issue through the container's Unix socket via `docker exec`: `ALTER USER` to the password derived from the staged key, **and** delete every row in the secrets family. Postgres makes both transactional, so the plane never exists in a state where the credential has moved but undecryptable ciphertext is still readable. Other families are untouched; item 7 built the vault so it drops wholesale. Trust authentication means anyone who can open a connection owns the database, so the absence of a listener is the security boundary, not a convenience.
7. **Install the staged key atomically** (rename), then recreate the normal containers with the new environment.
8. **Verify over the network by service name**, not from inside the container, whose `pg_hba` trusts local connections and would make the check vacuous — the same trap as step 5, at the other end of the sequence. Then remove the marker.

MinIO needs no step: its credentials are environment, not baked into the data directory, so the store follows the new key. Item 7 measured this.

**Retry semantics, one per window.** A rerun is authorized by the marker and branches on step 5's SCRAM probe, which is durable evidence living in the cluster rather than a flag we would have to keep honest.

| Crash point | State | Rerun does |
| --- | --- | --- |
| Before the step 6 transaction commits | Cluster unchanged, plane still locked | Probe fails → repeat from step 6. The same staged key derives the same password, so this is idempotent rather than a second rotation. |
| After commit, before key install | Cluster on the new password, live key still absent | Probe succeeds → skip to step 7. |
| After key install, before marker removal | Plane fully recovered, no longer reporting locked | Marker authorizes the resume, probe succeeds, key already present → verify and clear the marker. |

**D8a. A missing staged key is two states, not one (amendment).** *Approved by Codex — pending; found in implementation.* Step 7 installs the key by **renaming** the staged file into place, so the third window above legitimately has a marker whose staged key is absent. Treating that as the incoherent state of step 3 refuses exactly the window the marker exists to authorize, and strands an operator with a fully recovered plane carrying a marker nothing will ever clear. **The live key decides:** present means this is the post-install window and the live key *is* the staged one under its final name; absent means nothing accounts for the missing file, the credential may already have moved to a key nobody has, and refusing remains correct.

**D8b. `pg_isready` and `psql` must be told the user and the socket (amendment).** *Approved by Codex — pending; found in implementation.* Both derive a default username through `getpwuid`, which fails for the arbitrary uid Compose runs as — **the same trap that makes `postgres --single` unusable here**, in a third place. `pg_isready` exits 3 ("no attempt was made") against a server whose own log shows it accepting connections, so the readiness wait times out with a diagnosis that points nowhere. Every in-container invocation therefore passes `-U`, `-d`, and `-h /var/run/postgresql` explicitly; the socket path additionally prevents any fallback to a TCP attempt against a server that deliberately has no listener. The readiness failure now carries the container's log, because "never accepted socket connections" is not a diagnosis: a refused bind mount, an unopenable PGDATA, and an unparseable HBA all look identical from outside.

**The native-Linux CI job is a requirement, not a nicety.** Item 7's measurements were taken on macOS, where Docker Desktop virtualises bind-mount ownership, and item 2's history is that uid handling over a `0700` host-owned mount is precisely where the two platforms diverge. Item 7 says item 8 must exercise the sequence in native-Linux CI rather than inherit a developer-machine result.

## D9. Command surface

| Verb | Make target | Notes |
| --- | --- | --- |
| `backup -to <dir>` | `dataplane-backup DEST=<dir>` | Destination must not exist, and must not overlap the data root. Keyless; returns the project to the state it found. |
| `restore -from <dir>` | `dataplane-restore SRC=<dir> [FORCE=1]` | `FORCE=1` over a populated root, as `reset`. |
| `verify` | `dataplane-verify` | Runs against the live plane. |
| `recover-key` | `dataplane-recover-key FORCE=1` | Destructive: drops every secret and rewrites a database credential. |

## Test plan

Behind the `integration` build tag where a real stack is needed, per the phase's testing rule.

**Placement follows where the behaviour lives.** Cases about a lifecycle verb's composition — that restore verifies, that `up` settles a debt, that a refusal leaves the plane stopped — belong to the launcher's suite and need a real isolated Compose project. Cases about what verification *itself* concludes belong to the seam's suite, where a disposable database and bucket make the state cheap and deterministic to produce. Case 16 is split accordingly: the detection is the seam's, and restore's refusal to complete on it is the launcher's.

**Every plane populated for a case below carries the cross-store fixture** — an artifact, an attachment whose bytes live in the object store, and the pin that holds it. A plane holding only relational rows makes each of these pass vacuously: verification recomputes nothing across the object store and reports the same empty problem list as a healthy plane, so a backup that copied the cluster and forgot the bucket would satisfy every assertion. Coverage counts are asserted alongside outcomes for the same reason.

1. **Round trip**: populate a plane (artifact with attachment and pin), back up, `reset`, restore, verify, read the artifact back.
2. **Clean shutdown**: the restored cluster's log shows no crash recovery, proving the `SIGINT` fast-shutdown path (D3) rather than assuming it — asserted with a client connection held open across the backup, which is the case `SIGTERM` would have broken.
3. **Two-part restore**: restore without the key, assert `up` fails with `ErrPlaneLocked` and the plane is left stopped (D4's defined terminal state), place the key, assert `up` succeeds.
4. **Key exclusion**: the key file's bytes appear nowhere under the archive.
5. **Registry conformance and freshness**: `paths.Services()` matches the shipped Compose file's bind-mounted services in **both** directions, proven to fail with either side changed alone. Freshness counts a regular file, a symlink, and a FIFO alike; empty directory trees stay fresh; an unreadable root errors rather than answering "fresh"; and `reset` followed by `up` provisions cleanly, which is the composition of the two halves rather than each separately.
6. **Inode preservation**: the data root, every service directory, and the lock file retain their inodes across a restore. This is the round-1 defect, so it is asserted directly.
7. **Lock coverage**: a concurrent `reset` blocks for the whole of a restore, including the restart, rather than only its copy.
8. **Containment**: backup into the data root, restore from inside it, and both again through a symlinked parent — all refused before the stack stops.
9. **Phased failure recovery**: an injected copy failure during *backup* leaves the plane running with both errors reported **and no path at the destination**, asserted by pointing `restore` at it and requiring refusal; an injected failure during *restore's destructive phase* leaves the plane stopped with the marker present. The two assertions are opposite on purpose.
10. **The marker gates everything**: every lifecycle verb is exercised against a marked root and asserted to refuse except `down`, `restore`, and `reset`; the enumeration is structural, so a verb added later without a decision fails the test rather than defaulting to permitted. `reset` clears the marker.
11. **A killed backup leaves nothing restorable**: kill the process mid-copy — not an injected error return, which cannot exercise the residue — and assert the surviving temporary tree has no manifest and that `restore` refuses it before taking any destructive action.
12. **Project state round-trips**: backup with the plane fully running, fully stopped after `dataplane-down`, and with one service stopped; assert each ends in the state it began. The stopped case is the one round 3 would have failed. Per D3a, "the state it began" is asserted as USABILITY — the plane is used immediately after `Backup` returns, with no sleep and no retry, since a retry loop would restate the defect as the test's own workaround and pass against the broken version. The one-service-stopped case additionally pings the service that stayed up, which is what distinguishes a wait that happened from one that was skipped.
13. **Interrupted backup restarts anyway**: cancel the operation context mid-copy and assert the plane is running afterwards — the case a restart inheriting that context would fail.
14. **Modes survive the round trip**: exact permissions asserted on the restored tree — `0700` roots, `0600` cluster files. Written to fail against `os.CopyFS`, and proven to do so.
15. **Refusals**: restore onto a populated root without `-force`; restore from a source missing a service directory, asserted to leave the existing plane intact.
16. **Torn-pair detection**: corrupt one object **through the S3 API**, not by editing MinIO's on-disk files — whose representation is not the object body — and assert `verify` fails and names it. Then the same tear carried through a *restore*: back up the torn plane, `reset`, restore, and assert the restore refuses with the plane left **stopped** and the incomplete marker left in place. Existence is not the check — the row, the object and the size all agree, and only the content disagrees with the digest addressing it.
17. **Verify under concurrent truncation**: run `verify` while attachment truncation and the object sweep delete rows it has already listed, and assert it reports skips rather than corruption. Without this, the false-positive path is untested and the tool's credibility rests on argument. **Made deterministic by holding the per-`(organization, digest)` advisory lock verify itself takes**, which stops the pass exactly between its committed listing and its recheck — the interval the deletions must land in. Two goroutines and a hope would leave the interesting case untested on most runs, and passing.
17a. **The verification debt, both halves** (D4a). A two-part restore of a *torn* archive: part one completes and records the debt, the key is supplied, and `up` fails the settlement — asserting `ErrRestoreUnverifiedPending`, the plane **stopped**, the marker **retained**, and `backup` refusing the plane afterwards. **And the pre-settlement region**, which the case above does not reach: a dirty schema version — the real state a killed migration leaves — makes `up` fail *after* Compose has started the containers and *before* verification is attempted, asserting the plane stopped and the marker retained, with the error deliberately required NOT to be `ErrRestoreUnverifiedPending` so an injection landing in the wrong place fails rather than passes. Both the arming point and the disarm are additionally checked structurally, like D4's shutdown ordering. Plus the policy table itself, enforced structurally like D4's: every lifecycle operation has an entry, the permitted set is exercised against a marked root, and the call sites are parsed to prove each verb reaches the *combined* guard rather than the torn half alone.
18. **New-key recovery** (native-Linux CI): recovery over a plane with data and secrets; the data survives, secrets are gone, the new credential authenticates over the network by service name, the old one is rejected, and no listener exists during the recovery step.
19. **Recovery resumability**: reach each of the three windows in D8's table and assert convergence to the same recovered state. For the pre-commit window, that the plane is still honestly locked rather than holding a key that opens nothing; for the post-install window, that marker-authorized resume works after the plane stops reporting locked. **On method, amended in implementation:** the plan said "kill the CLI process", and a timing-based kill cannot select *which* window it lands in — the three are seconds apart and the boundaries move with disk speed, so three killed runs sample one window three times and report three passes. What matters about a kill is the **durable state** it leaves, which is exactly enumerable, so each window is constructed as the kill leaves it *including the surviving recovery container* — the residue an injected error return would not produce, and the thing the original clause was really about. One test additionally kills a real child process, because an orphan no in-process handle refers to is the one thing no constructed state reproduces faithfully. Not covered, stated rather than implied: a kill landing between two statements inside one window.
20. **Recovery owns its residues**: after a killed run, assert the surviving recovery container is stopped and removed before the probe, and that a staged key written without its marker is cleaned rather than adopted.
21. **The recovery probe can fail**: assert the SCRAM probe rejects a wrong password over the socket-only server. Without it the probe is the trust-HBA tautology item 7 recorded, and every branch in D8's table would take the same path regardless of what actually happened.

## Related documents

- [Phase 2 plan](plan_scope.md) item 8 and the exit checklist entry it satisfies.
- [ADR 0022](../../adr/0022-v2-data-plane.md) (cold backup as MVP baseline; both restore branches; online snapshot deferred as [backlog candidate 2](../notes_adr-backlog.md)), [ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md) (destructive recovery under the resource's lock).
- [Project-folder spike](../phase_0/spike_project-folder.md) items 2 and 4: the backup boundary and the key exclusion.
- [Item 2 design](design_local_stack.md) (roots, lock, lifecycle), [item 6 design](design_object_module.md) (commit order, digest-on-read, sweep), [item 7 design](design_config_secrets.md) (`rootKeyFor`, `ErrPlaneLocked`, the measured recovery procedure).
