+++
title = "Phase 2 Exit Record (In Progress)"
edit_date = "2026-07-26"
status = "draft"
summary = "Running record of Phase 2: what each item delivered, exit-criteria status, decisions and what they cost, and the verification post-mortem behind CLAUDE.md's Verification Discipline. Accumulates as the phase runs; flips live at phase close."
type = "notes"
+++

# Phase 2 Exit Record (In Progress)

Status: **draft — work in progress.** This accumulates as Phase 2 runs and flips to `live` at phase close, when the exit checklist is walked and the remaining criteria are marked. It is written now rather than reconstructed later, because the parts worth keeping — what a decision cost, why a rule exists — are the parts that get lost when a record is assembled from memory at the end.

The binding exit criteria live in the [Phase 2 plan](plan_scope.md). This records **status and evidence** against them; where the two disagree, the plan wins.

## Items delivered

| Item | Branch | State | What it delivered |
| --- | --- | --- | --- |
| 0 | `scope-and-plan` | Merged `83a8522` (#283) | Phase scope, 11-item sequence, four delegated decisions. Plus the build-process policy/mechanics split between `process_build.md` and `CLAUDE.md`. |
| 1 | `adr-artifact-envelopes` | Merged `01e7b82` (#284) | [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) Accepted: envelope/payload split, JCS digests, code-resident type registry, additive-only evolution, RFC 7386 amendments, review bound to the reviewable projection. |
| 2 | `local-stack` | Merged `dcf4dd0` (#288) | Four-root path resolver, root-of-trust key, bootstrap pointer, per-service data directories, Compose stack (Postgres + MinIO), HKDF credentials, `dataplane-up/down/reset`, Linux CI job. |
| 3 | `schema-core` | **PR #289 open** | 10 migrations / 19 tables applied from empty, embedded migration runner, sqlc config with drift check, [table inventory](inventory_schema-tables.md). |
| 4–10 | — | Not started | Typed queries, calls family, object module, config/secrets, backup, vertical slice, phase exit. |

## Exit criteria status

Nothing here is claimed complete that has not been demonstrated. Criteria not yet met are listed as such rather than partially credited.

**Met**

- **Artifact envelopes ADR Accepted before any DDL merged** — ADR 0028 merged in item 1; the first migration merges in item 3. Backlog candidate 1 moved to Resolved.
- **Every table traces to an Accepted ADR and a Phase 2 consumer** — the [table inventory](inventory_schema-tables.md) is the checkable form: 19 tables with their ADR and consuming item, plus the eight families deliberately deferred and where they land.

**Partially met**

- **One command from a clean checkout** — `make dataplane-up` composes, health-gates, and now migrates, proven on native Linux CI from cold. The criterion also names *typed queries*, which are item 4, so this closes then.
- **Migrations apply from empty** — done and CI-proven. The paired clause, *typed queries with tests* for the artifact, principal-instance and call families, is item 4.
- **MinIO composed and bind-mounted; local durability invariant** — composed and bind-mounted under the data root since item 2. The invariant is *asserted* by design but has not been demonstrated by recreating containers and restarting the Docker daemon with data intact. **Outstanding, and worth doing before phase close rather than at it.**

**Not yet met (scheduled)**

- Configuration and secrets families with typed queries → item 7.
- Cold-backup operation and validated restore → item 8.
- Object module with its S3-compatible adapter → item 6.
- Vertical slice, including an object write with digest reference and retention pin exercising the commit-order invariant, and idempotent re-import → item 9.
- Phase-end `golden-all` regression run, imported and distilled into the conformance log → item 10.
- Backlog reconciliation, and confirming the Phase 3-blocking entries → item 10.

## Decisions and what they cost

Recorded because the cost is the part that does not survive in the code.

- **The scope model took three wrong shapes** before the exclusive arc: a claimed foreign key a polymorphic column cannot have; a same-transaction seam check that is not a guarantee (a plain `SELECT` takes no lock); and a `scopes` supertable whose foreign keys pointed *into* it, so deleting an entity left its scope row behind with artifacts still resolving to it. The deletion test already specified in the design would have failed against that third version — the test was right and the schema was wrong.
- **Item 3 took four design rounds and five implementation rounds.** The design rounds were cheap (a document); the implementation rounds were expensive (rewriting migrations twice). Most implementation findings were items that belonged on a rejected-cases list written *before* the DDL. That observation is now a rule in `CLAUDE.md`.
- **Health checking is deliberately asymmetric** (item 2): Postgres via its own container healthcheck running an authenticated query, MinIO via a host-side probe. `pg_isready` succeeds with wrong credentials, and a loopback check inside the Postgres container accepts *any* password because the image's `pg_hba` trusts loopback. Both traps were hit while testing manually before being designed out.
- **`paths.Bootstrap` is the local module's bootstrap, not the persistence contract.** Recorded as a rule because the pressure to import the concrete struct from above the seam will be real in Phase 3, and that is precisely how a local-only assumption hardens into architecture.

## Verification post-mortem

The evidence behind [`CLAUDE.md`'s Verification Discipline](../../../CLAUDE.md). The rules there are deliberately short; these are the cases that produced them, kept so a future reader can see what each rule cost before deciding to relax it.

**Claims asserted from memory and wrong.** Two shipped into comments and commit messages: *"golang-migrate wraps Postgres migrations in a transaction"* (it does not — every migration needs explicit `BEGIN`/`COMMIT`, and without it a half-applied migration is the recoverable case lost), and *"`GracefulStop` is unbuffered, so a plain send deadlocks"* (it is `make(chan bool, 1)` in the pinned v4.19.1). The second is the worse failure: the commit message presented a hazard invented from an unverified assumption as a bug I had *found and shipped*. Both took under a minute to check once challenged.

**Assertions that could not fail.** A credential-leak test whose two comparisons were both vacuous — the base64url alphabet can never contain raw key bytes, and a 64-character hex needle cannot occur in a 43-character output — which passed against a `Derive` that returned the key verbatim, confirmed by writing that mutant. A lock test that passed with the lock deleted. A CI label check that proved only *at least one* container carried the label. A drift check blind to untracked files, so a new generated file left it green. Each looked like coverage and was decoration.

**Fixes that introduced new defects.** Fixing IPv6 support introduced bracketed forms, which `net.JoinHostPort` would double-bracket. Fixing an ownership error message introduced a copy-pasteable `sudo chown` command for a path that contains a space on the default macOS layout. The first key-durability fix synced only on the creating path, leaving readers able to return a key whose directory entry was not yet durable.

**The same rule applied in one place and not the adjacent one.** `Roots.Ensure` gained a permission check while `EnsureServiceDataDirs` did not. Organization lineage was added without user lineage. A foreign key was removed with nothing put in its place. Understanding that `git diff` ignores untracked files — and using that knowledge to fix the *test harness* — did not carry to the *assertion the harness was testing*.

**Guarantees that cannot be tested.** Three key-file durability defects were green the entire time, because `fsync` ordering and crash windows are not reproducible in a unit test. Review caught all three. The response is not more tests but stating the boundary beside the code, so adjacent passing tests stop implying coverage.

**One P0.** A down-migration test ran against the canonical `maestro` database and dropped every table in it — written by copying a file through `/tmp` without asking which database it pointed at. Now behind a disposable-database harness, which itself leaked a database on first run because a deferred close ran before `t.Cleanup`.

## Follow-ups

- [maestro#287](https://github.com/SnapdragonPartners/maestro/issues/287) — fold `dataplanectl` into the main binary; blocked on moving the compose assets under a package, since embedding cannot reach parent directories.
- **Demonstrate the local durability invariant** (containers recreated, Docker restarted, data intact) before phase close.
- ADR needs discovered in-phase: none so far. Confirmed at item 10.
