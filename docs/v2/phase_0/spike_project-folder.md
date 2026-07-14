+++
title = "Spike Report: The Disposable Project Folder"
edit_date = "2026-07-14"
status = "draft"
type = "spike"
summary = "How much non-disposable state can leave the user's filesystem? Answer: nearly all of it. v2 retires the project directory entirely: a data-plane bootstrap pointer in the OS config dir, disposable mirrors/workspaces in the OS cache dir, and the committed repo .maestro/ as the only surviving .maestro."
+++

# Spike Report: The Disposable Project Folder

Status: draft. Phase 0 item 9. Question (from roadmap D8 and the parking lot): how much non-disposable state can move off the user's filesystem into the data plane (or the repo, where it is a true project artifact) — and does `.maestro/` need to exist at all?

## Method

Full survey of every path v1 reads or writes: the `.maestro/` state hub, mirrors, agent workspaces, bootstrap outputs, temp files, and secrets, each classified by durability, secrecy, and recreatability. No code written; no refactor performed (spike rules).

## Findings

- **There is no global state.** Zero home-directory, XDG, or `~/` usage anywhere in the runtime — every path derives from the project directory or `os.TempDir()`. Disposability is therefore a per-project question with no hidden global tail.
- **The non-recreatable core is six files**: `maestro.db` (SQLite — already a database), `config.json`, `secrets.json.enc` (encrypted), `forge_state.json`, `.password-verifier.json`, and the user-authored `.env`. Plus `knowledge.dot`, which is repo-committed and whose design dies with v1 anyway (the cms spike moves v2 knowledge to the data plane).
- **Everything else is recreatable**: `.mirrors/` re-clones from the forge (corruption already triggers auto-reclone), agent workspaces rebuild from mirrors (their outputs live on the forge as branches/PRs, never on disk), `.tmp/` and `os.TempDir()` staging is transient, logs are scratch (and were never the record — that is what Audit artifacts are for, ADR 0021), and the legacy `.maestro/{states,stories,work,mirrors}` dirs are superseded scaffolding already displaced by the database.
- **v1 conflates two different `.maestro/` directories.** One is a *committed repo directory* — the Dockerfile, makefiles, compose file, and instruction documents (`COMMON.md`, `MAESTRO.md`, etc.) are true project artifacts that belong in the repo per D5. The other is a *local state hub* — db, config, secrets, logs — that happens to share the path. These have opposite fates and should stop sharing a name's worth of confusion.
- **Security finding**: `forge_state.json` stores the forge API token in plaintext (0600). Frozen v1 behavior — WONTFIX there — but the v2 secrets design must not reproduce it; forge tokens belong in secrets storage, and the forge *binding* (provider/url/owner/repo) belongs in the repo records ADR 0022 already defines.

## Recommendation

**Yes: the v2 project folder is disposable.** The parking-lot hypothesis is confirmed by inventory — only the data-plane connection bootstrap remains local.

1. **To the data plane** (resolving ADR 0022's deliberate deferral: the schema *should* hold config and credentials, not merely be capable of it):
   - `maestro.db` → Postgres (already decided, ADR 0022).
   - `config.json` → configuration records with org/product/repo lineage, behind the persistence seam.
   - Secrets (`secrets.json.enc`, the forge token from `forge_state.json`, password verifier) → the persistence interface's auth/secrets module, encrypted at rest locally, real secret-manager adapters in cloud mode. Environment variables remain a supported injection path, never persisted.
   - Forge bindings (minus token) → ADR 0022's repo records (forge-independent repos with multiple bindings).
   - Knowledge → the data plane per the cms spike; `knowledge.dot` retires with v1's design.
2. **What remains local**: the **bootstrap pointer** (data-plane endpoint plus a *reference* to the root of trust — never secrets), the **root-of-trust key file** (below), active workspaces until their work is durable, and recreatable caches. The root of trust is the external anchor Codex's review demanded: secrets are encrypted inside the plane, and the unlock key can therefore never live in the plane. Default: a Maestro-generated random key in a 0600 file under Maestro config, created silently at setup — no env vars to configure, nothing to remember, unattended-operation-safe (the constraint that disqualifies passphrase-by-default). Opt-in upgrades behind the same auth-module interface: OS keychain where available; passphrase mode for those accepting the unattended cost. The data-root backup deliberately excludes the key.
3. **The `.maestro/` split**: as a *committed repo directory* it survives (Dockerfile, makefiles, instruction docs — project artifacts under D5); as a *local state hub* it retires down to the bootstrap pointer. This answers D8's queued question directly.
4. **Retire the "project directory" concept entirely** (per DR, on review). It existed for a v1 assumption that no longer holds — operating on an already-materialized user checkout. v2 never does that: Maestro works only from its own mirrors and clones (ADR 0023), and a user's existing checkout is irrelevant to it. The taxonomy broke the model independently: a Product spans repositories, so no single folder can host "the project." What remains locally is split by function into OS-standard locations:
   - **Maestro config** — `os.UserConfigDir()/maestro` (macOS `~/Library/Application Support`, XDG config on Linux): the data-plane bootstrap pointer, nothing else.
   - **Maestro cache** — `os.UserCacheDir()/maestro`: mirrors and *reconstructible* workspaces only. Apple reserves cache locations for regenerable data and may purge them under storage pressure, so nothing whose only copy is local may live here.
   - **Maestro state** — a non-purgeable application-state location (XDG state on Linux; Application Support on macOS): **active workspaces**, which until pushed contain the only copy of real work — v1 pushes only at PREPARE_MERGE, and ADR 0023 mandates no mid-Story cadence, so "replay from the last push" can mean losing a whole Story's work-in-progress. Workspaces are keyed by **repo + Story/run**, not repo alone, so concurrent Stories in one repository cannot collide. A workspace may migrate to cache (or deletion) once fully pushed; push-checkpoint cadence is Phase 3 workspace design.
   - **Maestro data** — an OS user-data location: the single durable storage root holding the bind-mounted Postgres and object-store volumes **and the airplane-mode local forge's data** (its repositories, refs, and PR state are authoritative until synchronization — they can neither stay in a Docker-internal named volume nor be called "reconstructible from the forge," because until sync they *are* the forge). Backup of this root is a defined operation, not a live directory copy: the MVP baseline is cold backup (stop services, copy, restart), per ADR 0022 as amended; the backup boundary excludes the root-of-trust key by design.
   - A `MAESTRO_HOME` override collapses all three into one directory (the classic `~/.maestro` layout) for those who want it.
5. **The naming tangle resolves as a side effect**: with the local state hub retired and local directories named by function (config, cache), the committed repo `.maestro/` directory becomes the *only* thing named `.maestro` — repo-scoped project artifacts, exactly like `.github/`. No rename needed; the collision dies because one of the colliding parties does. Retired from the vocabulary: "project directory."
6. **User-authored `.env`**: user-managed application configuration, located through the product/repo configuration records (not by convention in a Maestro directory), referenced by Maestro for demo/UAT runs and **explicitly outside Maestro's durability guarantee** — it is the product's file, not the harness's state.
7. **D8 inventory inputs**: `pkg/config` → rework (data-plane config records + bootstrap-pointer loader); `pkg/state` file store and the legacy `.maestro` dirs → drop; `pkg/forge` state → rework into repo records + secrets module; `pkg/utils/maestro_files.go` → rework to repo-artifact management only; secrets/password machinery → rework behind the persistence interface.
8. **No Phase 1 impact** (v1-as-patched keeps its files). Phase 2 implements the configuration and secrets families, the key-file root of trust, and the cold-backup operation — ADR 0022 is amended in lockstep so the Accepted contract and this spike agree on Phase 2's scope.

No third-party package is involved, so no wishlist accompanies this spike. No spike scripts were produced (reading spike); `spikes/phase_0/` is not needed.

## Related Documents

- [Phase 0 plan](scope-and-plan.md) item 9; [roadmap](../roadmap.md) D8; [parking lot](../parking-lot.md) (Config And Credentials In Data Plane — hypothesis confirmed).
- ADRs [0022](../../adr/0022-v2-data-plane.md) (persistence interface, repo records, the config/credentials deferral this resolves), [0021](../../adr/0021-artifacts-and-principal-instances.md) (Audit artifacts as the record; logs as scratch).
- [cms spike](spike_cms.md) (knowledge to the data plane); historical note [0011](../../adr/0011-configuration-operating-modes-and-secrets.md) (v1 config/secrets design this supersedes for v2 intent).
