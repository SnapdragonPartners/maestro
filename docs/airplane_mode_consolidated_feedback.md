# Consolidated feedback on Airplane Mode spec (Maestro)

Audience: the coder who authored the initial airplane-mode design.

Date: 2025-12-24

---

## Executive summary

The core idea—supporting an “offline/airplane mode” by swapping GitHub with a **local forge** while preserving Maestro’s **architect‑gated merge** workflow—is sound. The most important improvements are:

1. Make `maestro --airplane` a **single, idempotent** entrypoint that (a) prepares/repairs what it can, (b) validates readiness, and (c) boots in airplane mode (fail fast if not ready).
2. Keep config minimal: avoid duplicating the entire config tree for airplane mode. Add only **optional local-model overrides** for agent roles, with deterministic automatic selection if unspecified.
3. Make validation **mode-aware**: in airplane mode, skip checks for GitHub and hosted-provider API keys; instead validate offline-critical dependencies (docker, forge, local LLM).
4. Treat “local forge” as a **first-class backend** that can be used indefinitely (not only in airplane mode). Sync to GitHub becomes an explicit operation.
5. Store workflow/PR identity in **SQLite** for durability and idempotency across retries.

This file captures concrete spec deltas, recommended APIs, and behavior details.

---

## Goals and invariants

### Primary goal
Run Maestro productively on a single laptop **without Internet**, where:
- containers can talk to each other via local/virtual networking
- local models are available (e.g., Ollama)
- the workflow still enforces **architect-gated merges**

### Non-goals / de-scoped (for initial version)
- Perfect support for multiple concurrent Maestro runs on one laptop (port collisions for WebUI and forge). Note as a limitation; don’t solve yet.
- Full fidelity CI parity with GitHub Actions (nice-to-have; can be incremental).

---

## Key design decisions

### 1) One entrypoint: `maestro --airplane`
Avoid a separate “prepare” command. Instead:

- `maestro --airplane`:
  - runs **all** checks (idempotent)
  - attempts safe repairs (start containers, create directories, etc.)
  - attempts downloads **only when needed** (model pull / image pull)
  - produces a clear status report
  - **fails fast** if requirements cannot be satisfied (e.g., truly offline and missing image/model)
  - then boots airplane mode, which is a second validation pass by virtue of successful runtime operations

This aligns with “no surprises” and keeps CLI surface area minimal.

### 2) Minimal airplane config (no duplication)
Do **not** replicate all config for “normal vs airplane” modes. Instead:
- Keep airplane mode opinionated:
  - single local forge choice (no user-configured forge settings required initially)
  - no “network policy” config knob
- Add only optional airplane overrides for agent model selection.

Example (shape, not exact naming):
```json
{
  "agents": {
    "coder_model": "claude-sonnet-…",
    "architect_model": "gpt-…",
    "pm_model": "…",
    "airplane": {
      "coder_model": "ollama:qwen2.5-coder:7b",
      "architect_model": "ollama:llama3.1:70b",
      "pm_model": "ollama:llama3.1:8b"
    }
  }
}
```

If airplane overrides are absent, Maestro should choose a local model deterministically from a preferred list by probing `ollama list` / Ollama tags.

### 3) Mode-aware validation
In airplane mode:
- **Do not** require `GITHUB_TOKEN`
- **Do not** require hosted-provider API keys
- **Do not** require `gh` tooling (if GitHub is not used)

Instead require:
- Docker available
- Local forge running and reachable
- Local LLM endpoint reachable and has a suitable model available

### 4) Local forge storage: project-local
To avoid confusion between:
- `<projectDir>/.maestro` (uncommitted runtime state, DB, etc.)
- `<repoDir>/.maestro` (committed assets like Dockerfiles)

Prefer keeping forge data under the project runtime directory:
- `<projectDir>/.maestro/forge/…`

Run the forge as a Docker container and mount `<projectDir>/.maestro/forge` into the container volume (e.g., `/data` for Gitea).

This also makes removal straightforward: delete the project directory.

---

## Core implementation primitives (recommended functions)

### A) Local LLM selection + ensuring availability

**Why**: airplane mode must guarantee at least one local model is usable. Prefer deterministic selection and idempotent ensures.

Recommend splitting “choose” vs “ensure”:

```go
// Determine which model to use (from config override or preferred list).
func resolveLocalLLM(cfg *Config, ollamaHost string) (model string, err error)

// Ensure the chosen model is available locally (pull if missing).
func ensureLocalLLM(ollamaHost, model string) (wasPulled bool, err error)
```

If you prefer a single function:

```go
func getLocalLLM(cfg *Config, ollamaHost string) (model string, ensured bool, err error)
```

**Behavior**
1. If config specifies an `ollama:` model for an agent role, verify it exists locally; if not, attempt pull.
2. If config doesn’t specify, query Ollama for local models and choose the first match from a preferred const list.
3. If no preferred models exist locally, attempt to pull the first preferred model.
4. If pull fails (no Internet), fail fast with clear guidance: “Model X not present locally; cannot pull while offline.”

**Preferred model list (example)**
- Coder: `qwen2.5-coder:7b`, `deepseek-coder-v2`, …
- Architect/PM: `llama3.1:8b`, `llama3.1:70b`, …

(Choose models based on what you’ve tested and what fits typical laptop constraints.)

**Notes**
- Ollama typically loads models on demand when invoked; explicit “preload” is optional optimization.

### B) Local forge bootstrap

```go
func ensureLocalForge(projectDir string) (status ForgeStatus, err error)
```

**Behavior**
- Ensure required directories exist: `<projectDir>/.maestro/forge/...`
- Ensure Docker image is present (pull if missing)
- Ensure container exists; start if stopped; create if missing
- Health-check the forge HTTP endpoint before proceeding

**Opinionated defaults**
- Pin a known-good image version (avoid `latest`).
- Bind forge UI to localhost only.
- Use a stable port (can be a constant for MVP). Document that multiple concurrent runs may collide.

### C) Ensure Ollama (optional)

```go
func ensureOllama(ollamaHost string) (status OllamaStatus, err error)
```

**Behavior**
- Check reachability of `ollamaHost` (e.g., via tags endpoint)
- If unreachable:
  - either fail with guidance (“install/start Ollama”)
  - or best-effort start an Ollama Docker container (be cautious about GPU/perf portability)

Given current paradigm (“agents call LLMs directly from host”), simplest path is:
- require host Ollama to be reachable
- optionally add Docker fallback later

---

## Airplane boot flow (what `--airplane` does)

1. **Compute airplane context**
   - Set “mode=airplane” in runtime (CLI flag overrides config)
   - Force local forge backend
   - Force local model provider selection (Ollama)

2. **Run idempotent ensures**
   - ensure docker running
   - ensure local forge container up + healthy
   - ensure Ollama reachable (or start it, if you choose)
   - resolve + ensure local models for required agent roles
   - ensure project mirror/repo state is consistent for local workflow (see below)

3. **Validate offline readiness**
   - confirm each required component is “ready”
   - if any are missing and cannot be installed/pulled: exit nonzero with explicit missing items

4. **Boot Maestro**
   - successful boot doubles as validation that no checks were missed

---

## Local forge vs mirrors (simplification suggestion)

The original spec maintained `.mirrors/` as the clone source even in airplane mode. That preserves parity but adds complexity: syncing mirrors, remote switching, etc.

Recommended simplification for MVP:
- In airplane mode, treat the local forge as the primary remote and clone from it directly.
- Keep `.mirrors/` as an optimization for online mode only, or make it optional.

This reduces moving parts and avoids needing to repoint mirror remotes dynamically.

---

## “Start from scratch” offline (desirable enhancement)

Support `maestro --airplane` on a brand-new idea with no GitHub repo yet:

- If no repo URL/mirror exists:
  - initialize a new local repo
  - create a new repo in local forge
  - set up default branch and initial commit
  - record project metadata in SQLite
  - proceed as usual

This makes the “transatlantic flight, new app idea” workflow possible.

---

## SQLite as the source of truth for workflow identity

Even if you use a forge, persist PR/workflow identity in SQLite for robustness:

Store:
- forge type (github / local)
- forge PR ID (if applicable)
- base/head refs
- created-by story ID
- merge commit SHA
- status transitions

Benefits:
- idempotent merges/retries
- stable reference across agent restarts
- easier to support both “real PRs” and “logical PRs” later

---

## Sync to GitHub (post-offline)

Provide an explicit command/operation for publishing local work upstream when back online.

Key behaviors:
- Detect upstream advancement since airplane mode entry (record base commit when switching modes).
- Default conservative behavior:
  - if upstream moved, refuse automatic sync and provide a resolution path (merge/rebase or explicit `--force`).
- Decide how to handle:
  - merged PRs (push merged main)
  - open PR branches (push branches, optionally recreate PRs upstream later)

Also: allow “local forge indefinitely” even when online. “Airplane mode” is about offline constraints; forge selection should be orthogonal.

---

## Compose vs docker run

You are docker-centric. Using **Docker Compose internally** can simplify orchestration without adding CLI complexity:

- Ship a compose template (embedded or generated)
- Write it to `<projectDir>/.maestro/airplane/docker-compose.yml`
- Run `docker compose up -d`
- Health-check services

Compose helps keep flags consistent and makes “idempotent ensure” behavior easier.

---

## Known limitations to document (for MVP)

- Multiple concurrent Maestro instances may collide on:
  - WebUI port
  - local forge port
- Airplane mode can only auto-download images/models when Internet is available at that moment.
- CI parity is partial initially (can add local runner support later).

---

## Checklist of spec edits (action items)

1. Replace separate prepare/validate with a single `maestro --airplane` flow.
2. Add mode-aware validation: skip GitHub/hosted keys and `gh` in airplane mode.
3. Add local model resolution/ensure logic using Ollama inventory + preferred list.
4. Make local forge bootstrap fully idempotent and pinned to a known-good image version.
5. Prefer cloning directly from local forge in airplane mode (reduce mirror complexity).
6. Add “init from scratch” path for offline new projects.
7. Persist PR/workflow identifiers in SQLite for idempotency.
8. Define explicit “sync/publish to GitHub” operation for when connectivity returns.
9. Store forge data per-project under `<projectDir>/.maestro/forge` and mount into container.

---

## Open questions (optional for MVP)

- Should we support local CI execution in airplane mode?
  - Near-term: no
  - Later: consider local runners (forge-based) or `act` if leveraging existing workflows
- Should we support dockerized Ollama as fallback?
  - Likely later; start with host Ollama requirement if that’s the current paradigm

