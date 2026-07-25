+++
title = "Design: The Local Data-Plane Stack (Item 2)"
edit_date = "2026-07-24"
status = "draft"
summary = "Mini-plan for Phase 2 item 2: the four-root path resolver with MAESTRO_HOME collapse, the 0600 root-of-trust key file, and a Compose stack for Postgres and MinIO bind-mounted under the data root — isolated from v1's container labelling so a benchmark sweep cannot tear it down, digest-pinned, health-gated, and idempotent from a clean checkout."
type = "design"
+++

# Design: The Local Data-Plane Stack (Item 2)

Status: **draft** — for Codex review before the Compose and CI work lands. Follows the Phase 1 precedent of a design mini-plan for M-sized items ([design_runner.md](../phase_1/design_runner.md), [design_engine.md](../phase_1/design_engine.md), [design_adapter_v1.md](../phase_1/design_adapter_v1.md)).

Implements [Phase 2 plan](plan_scope.md) item 2 under [ADR 0022](../../adr/0022-v2-data-plane.md) (local durability invariant, Docker-local default, backup boundary) and the [project-folder spike](../phase_0/spike_project-folder.md) (the four-root split and the key-file root of trust). No schema — that is item 3.

## What item 2 owes

1. The **path resolver**: the four OS-standard roots, the `MAESTRO_HOME` collapse, and directory creation with correct permissions.
2. The **root-of-trust key file**: generated silently at setup, `0600`, under Maestro config, excluded from backup by design.
2a. The **bootstrap pointer**, under the config root (resolved in review — see Q2): the Postgres endpoint and port, the object-store endpoint, and a *reference* to the root of trust. Never secrets. It records deployment facts established by this item; item 3 consumes it when applying migrations rather than introducing it.
3. The **Compose stack**: Postgres and MinIO, both bind-mounted to durable host paths under the data root.
4. **`make dataplane-up`**: one command from a clean checkout — compose, wait for health, idempotent. (Item 3 adds migrations to this target.)
5. A **CI job** proving it comes up from a clean checkout.

## Decisions

### D1. Four roots, and the macOS collision

The spike fixes the semantics; Go gives us two of the four and the platforms disagree about the rest.

| Root | Linux | macOS |
| --- | --- | --- |
| config | `os.UserConfigDir()` = `$XDG_CONFIG_HOME` or `~/.config` | `os.UserConfigDir()` = `~/Library/Application Support` |
| cache | `os.UserCacheDir()` = `$XDG_CACHE_HOME` or `~/.cache` | `os.UserCacheDir()` = `~/Library/Caches` |
| state | `$XDG_STATE_HOME` or `~/.local/state` | `~/Library/Application Support` |
| data | `$XDG_DATA_HOME` or `~/.local/share` | `~/Library/Application Support` |

**On macOS, config, state, and data all resolve to `~/Library/Application Support`** — the OS simply does not draw the distinctions XDG does. Three roots would land on the same directory, and `data/` is the one the cold backup copies while `config/` holds the key that backup must exclude. Collapsing them silently would put the unlock key inside the backup boundary, defeating the design.

**Decision:** every root is always a *named subdirectory*, on every platform:

```
<config base>/maestro/config/
<cache  base>/maestro/cache/
<state  base>/maestro/state/
<data   base>/maestro/data/
```

On macOS these become `~/Library/Application Support/maestro/{config,state,data}` — distinct paths that share a base, which is exactly what the spike's `MAESTRO_HOME` rule already demands ("as subdirectories, never flattened: the semantics travel with the split"). Applying the same shape unconditionally means the backup boundary is one rule on every platform, not a per-OS special case. Go has no `UserDataDir`/`UserStateDir`, so those two are resolved by our own XDG-then-fallback logic.

The decisive argument is not tidiness, it is **nesting**. The alternative — add the subdirectory only on macOS, keeping `~/.config/maestro` on Linux — would make the macOS config root `~/Library/Application Support/maestro` and the data root `~/Library/Application Support/maestro/data`, i.e. *the data root nested inside the config root*. The cold backup copies the data root and must exclude the key that lives in the config root; a containment relationship between those two is the last shape that arrangement should have. Uniform named subdirectories make them siblings on every platform. The cost is a slightly redundant-looking `~/.config/maestro/config` on Linux, which is worth it.

`MAESTRO_HOME=<dir>` overrides all four bases at once, yielding `<dir>/{config,cache,state,data}`. It must be absolute; a relative value is an error rather than a surprise relative to the process's working directory.

Windows is **not supported** in Phase 2 and returns a clear error. Docker is already load-bearing (ADR 0022) and WSL is the documented path; a half-working `%AppData%` guess is worse than a refusal.

### D2. Permissions

Directories are `0700`, not `0755`. The data root will hold the Postgres cluster and the object store; the config root holds the unlock key. Neither is other-users' business on a shared machine, and defaulting tight costs nothing.

The key file is `0600`, created by **writing a temporary file and atomically linking it into place**, so a concurrent setup can neither race two keys into existence nor observe a partial one. This is [ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md)'s rule at file granularity: the shared resource is the path.

*Corrected during implementation.* The first version used plain `O_CREATE|O_EXCL`, which is the obvious answer and is wrong: `O_EXCL` makes **creation** atomic, not creation-plus-write. A losing caller finds the file already present and reads it before the winner has written a byte, getting an empty key. The concurrency test caught it on the first run (`caller 3: key is 0 bytes, want 32`). `os.Link` closes the window because it is atomic *and* fails rather than overwrites: a reader sees either no file or the complete file, and the winner's key is never replaced. The temporary file is always removed — a stray one is a second copy of a secret in the config root, so a test asserts the config root ends up holding exactly one file.

Content is 32 random bytes from `crypto/rand`, hex-encoded with a trailing newline so the file is greppable and copy-pasteable for the documented "restore on a new machine" flow. If the file exists with wrong permissions, Maestro **fails loudly rather than silently fixing it** — a key that was briefly world-readable is a key that may have leaked, and quietly `chmod`-ing it hides that.

### D3. Compose stack isolation from v1 — the load-bearing constraint

The Phase 2 plan's hard constraint is that nothing may disturb v1's path, because v1 is the benchmark target. Two concrete hazards, both easy to trip:

- **Label collision.** v1 labels its containers `com.maestro.session=<id>` (`internal/kernel/kernel.go`, `pkg/exec/*`, `pkg/demo`), and the benchmark adapter *sweeps by that label* on teardown (`benchmark/target/v1target/adapter.go`). A data-plane container carrying that label would be destroyed mid-run by a benchmark sweep. **The stack therefore carries `com.maestro.component=dataplane` and never a session label**, and this is called out in the compose file itself so nobody adds one for symmetry.
- **Compose project collision.** The stack uses an explicit project name `maestro-dataplane`, so `docker compose down` in one context can never reach the other's containers.

### D4. Ports

Host ports are **not** the service defaults: 5432 collides with any developer's local Postgres, and a silent connection to the wrong database is a bad failure. Defaults are `MAESTRO_PG_PORT=55432` and `MAESTRO_MINIO_PORT=59000` (console `59001`), each overridable. High ports in the ephemeral-adjacent range are unlikely to collide, and the bootstrap pointer records what was actually used rather than assuming.

### D5. Image pinning

Both images are pinned by **arch-independent manifest digest**, not tag — the discipline [ADR 0026](../../adr/0026-multi-architecture-artifacts.md) established for the benchmark cache image, and for the same reason: development is arm64, CI is amd64, and a per-arch digest would break one of them. The digests live in one file with the tag recorded alongside as a comment, so a bump is a reviewable one-line change.

### D6. Health gating and idempotency

`make dataplane-up` composes, then waits: Postgres via `pg_isready`, MinIO via its `/minio/health/live` endpoint, both with a bounded timeout that fails with the container's logs rather than a bare timeout message. Re-running is a no-op when the stack is already healthy — the "one command from a clean checkout" criterion and the everyday inner-loop command are the same command, so it must be safe to run repeatedly.

`dataplane-down` stops containers and leaves the data root untouched. `dataplane-reset` deletes the data root's contents and is the only destructive target; it prompts unless `--force`/`FORCE=1`, because the data root is the thing ADR 0022 spent an amendment making durable.

### D7. Where the code lives

`internal/dataplane/paths` (resolver + key file). Compose and its digest pin in `deploy/dataplane/`. Nothing under `pkg/`: this is Orchestrator machinery ([ADR 0019](../../adr/0019-orchestrator-boundary.md)), and Phase 2 plan decision 2 puts the data plane in `internal/`. **`pkg/persistence` and `pkg/config` are not touched** — v1 keeps its files and its SQLite for as long as it is the benchmark target.

## Testing

- Path resolution is table-driven over `(GOOS, env)` with the platform base injected rather than read from the real OS, so Linux CI can assert the macOS layout and the collision-avoidance property directly.
- Key file: creation, permissions, idempotent re-read, `O_EXCL` race, and the wrong-permissions refusal.
- Compose bring-up is an `integration`-tagged test plus the CI job — it needs Docker, so it stays out of `make test`.
- **Explicit non-regression check**: a test asserts no compose service carries a `com.maestro.session` label, so D3's isolation is enforced mechanically rather than by memory.

## Review questions — resolved

Both answered by Codex, 2026-07-24.

1. **Is `0700` on the data root a problem for the container bind mounts?** **No — keep the top-level data root at `0700`.** Bind-mount *per-service child directories* (`data/postgres`, `data/minio`), each pre-created with the ownership and mode its container requires. A container sees its mounted child as its own mount root and never traverses the host parent, so the tight root costs nothing. The children must be **pre-created rather than left to Compose**, which would otherwise create them root-owned, and the arrangement is verified on macOS and on Linux CI — the two platforms differ exactly here, since Docker Desktop's VM translates ownership while native Linux does not.
2. **Does the bootstrap pointer land in item 2 or item 3?** **Item 2.** It points at deployment facts this item establishes — the Postgres endpoint and port, and the root-of-trust reference — not at the schema. Item 3 consumes it while applying migrations rather than inventing the bootstrap mechanism. Recorded above as deliverable 2a.

## Implementation corrections

Findings from the first implementation round, kept here because each is a mistake worth not repeating.

- **`MAESTRO_HOME` produced `<dir>/maestro/{config,…}`**, contradicting the documented `<dir>/{config,…}` contract — the override is already a user-named directory and must not gain a second `maestro` component. Worse than a plain bug: *the unit test asserted the wrong paths*, so it encoded the defect rather than catching it. The override path no longer shares the base-assembly helper with the platform path, since the two have genuinely different rules.
- **`os.Link` is atomic but not durable.** The key file's contents were flushed, but the new directory entry was not, so a crash could lose a key that had already been returned to a caller — and possibly already used to encrypt the vault, which the backup deliberately does not hold a copy of. The containing directory is now synced before the new key is returned.

  **The first fix for this was itself incomplete** (second review round). Syncing only on the *creating* path left two holes: a caller that lost the link race, or simply found an existing file, could observe the winner's freshly linked entry and return that key *before* the winner synced — so a crash in that window still loses a key already in use. And the temporary link was dropped by an unchecked `defer` that ran *after* the sync, making its removal neither verified nor crash-durable, so a crash could resurrect a second copy of the key.

  Every successful return, on every path, now satisfies one ordered protocol: **(1)** the final link exists, **(2)** this caller's temporary link has been removed, successfully, **(3)** the containing directory is synced, after both. The ordering is the substance, not ceremony. The general lesson: **the durability obligation belongs to whoever returns the key, because that is who causes it to be used** — not to whoever created it.
- **`Roots.Ensure` neither repaired nor rejected pre-existing roots with wrong permissions**, while its own comment claimed such changes should be surfaced. It now refuses, matching the key-file policy and for the same reason — and refusing rather than ignoring is the other half of that policy, since a rule nothing enforces is not a rule.
