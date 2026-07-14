+++
title = "Spike Report: The Disposable Project Folder"
edit_date = "2026-07-13"
status = "draft"
type = "spike"
summary = "How much non-disposable state can leave the user's filesystem for the data plane? Answer: nearly all of it — the durable non-repo core is six files; v2 keeps only a minimal data-plane bootstrap pointer locally, and `.maestro/` splits into a committed repo directory and a retired local state hub."
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
2. **What remains local**: one minimal **bootstrap pointer** — where the data plane is and how to authenticate to it — plus recreatable caches (mirrors, workspaces, temp, logs-as-scratch). Lose the folder, re-point, and everything durable is still there; everything else rebuilds from the forge and the repo.
3. **The `.maestro/` split**: as a *committed repo directory* it survives (Dockerfile, makefiles, instruction docs — project artifacts under D5); as a *local state hub* it retires down to the bootstrap pointer. This answers D8's queued question directly.
4. **User-authored `.env`**: becomes user-managed app configuration referenced by, not owned by, Maestro — it is the project's file, not the harness's state.
5. **D8 inventory inputs**: `pkg/config` → rework (data-plane config records + bootstrap-pointer loader); `pkg/state` file store and the legacy `.maestro` dirs → drop; `pkg/forge` state → rework into repo records + secrets module; `pkg/utils/maestro_files.go` → rework to repo-artifact management only; secrets/password machinery → rework behind the persistence interface.
6. **No Phase 1 impact** (v1-as-patched keeps its files); Phase 2 implements the config and secrets families this confirms.

No third-party package is involved, so no wishlist accompanies this spike. No spike scripts were produced (reading spike); `spikes/phase_0/` is not needed.

## Related Documents

- [Phase 0 plan](scope-and-plan.md) item 9; [roadmap](../roadmap.md) D8; [parking lot](../parking-lot.md) (Config And Credentials In Data Plane — hypothesis confirmed).
- ADRs [0022](../../adr/0022-v2-data-plane.md) (persistence interface, repo records, the config/credentials deferral this resolves), [0021](../../adr/0021-artifacts-and-principal-instances.md) (Audit artifacts as the record; logs as scratch).
- [cms spike](spike_cms.md) (knowledge to the data plane); historical note [0011](../../adr/0011-configuration-operating-modes-and-secrets.md) (v1 config/secrets design this supersedes for v2 intent).
