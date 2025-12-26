# Airplane Mode Specification

**Status**: Draft - Revised after feedback
**Author**: Claude Code
**Date**: 2025-12-24

## Overview

This document specifies how Maestro can operate in "airplane mode" - fully offline development without access to GitHub or external LLM APIs. Combined with local LLM support via Ollama (see `docs/OLLAMA.md`), this enables complete offline multi-agent development.

### Phased Approach

The implementation is split into phases:

| Phase | Scope | Description |
|-------|-------|-------------|
| **MVP** | Core offline operation | Single CLI flag, bundled defaults, local forge + Ollama |
| **v2** | Configuration & UX | WebUI mode selection, system profiling, model recommendations |
| **v3** | Advanced workflows | Start-from-scratch offline, GitHub sync with conflict detection |

### The Problem

Maestro currently requires GitHub connectivity for two critical operations:

1. **PR Creation** (`gh pr create`) - Coders create pull requests after completing work
2. **PR Merge** (`gh pr merge`) - Architect merges approved PRs to main

Without GitHub, the development workflow cannot complete - stories cannot be merged, and the main branch cannot evolve.

### Why Not Simpler Solutions?

We evaluated several approaches before concluding that a local git server is necessary:

#### Option 1: Local Branch Merge (Rejected)

**Idea**: Skip PR creation, merge feature branches directly to a local main branch.

**Why it doesn't work**:
- **Ephemeral workspaces**: Coder workspaces are deleted between stories. There's no persistent local repository to accumulate merges.
- **Multiple agents**: With 3 coders + 1 hotfix agent, there's no shared local main they could all merge to.
- **Mirror is read-only**: The `.mirrors/` directory is fetch-only from GitHub - it has no mechanism for accepting merges.

#### Option 2: Deferred Operation Queue (Rejected)

**Idea**: Queue PR/merge operations and replay them when connectivity returns.

**Why it doesn't work**:
- **Branch persistence**: Feature branches would need to persist somewhere until sync. With ephemeral workspaces, where?
- **Dependent merges**: Story B might depend on Story A's merge. Can't defer A's merge without blocking B.
- **Mirror staleness**: The mirror wouldn't reflect merged changes, so new clones would be based on stale main.

#### Option 3: Make Mirror Read-Write (Rejected)

**Idea**: Push to the mirror directly, treating it as the authoritative repo offline.

**Why it doesn't work**:
- **Bare repository**: The mirror is a bare git repo (no working tree). It can receive pushes, but...
- **No PR workflow**: Git itself has no concept of pull requests. We'd lose the review/approval workflow entirely.
- **Merge conflicts**: Without PR machinery, handling merge conflicts between concurrent agents becomes manual and error-prone.

### The Solution: Local Git Server (Gitea)

A local git server provides the missing infrastructure:

- **Persistent repository**: Survives across ephemeral agent workspaces
- **PR workflow**: Full pull request creation, review, and merge
- **Multi-agent support**: Multiple agents can push branches and create PRs concurrently
- **API compatibility**: Gitea's API mirrors GitHub's, minimizing code changes
- **Lightweight**: ~100MB Docker image, runs anywhere

**Key insight**: Gitea becomes the "local GitHub" during offline operation. The existing mirror becomes a cache of Gitea (not GitHub), and all PR operations target Gitea instead of GitHub.

### Design Principles

Based on consolidated feedback, the implementation follows these principles:

1. **Single idempotent entrypoint**: `maestro --airplane` prepares, validates, and boots in one command
2. **Minimal config**: No duplication; airplane overrides only where needed
3. **Mode-aware validation**: Skip GitHub checks in airplane mode; skip Ollama checks in standard mode
4. **Bundled defaults**: Airplane mode = local forge + local LLM + offline validation (can decouple later)
5. **Graceful failure**: Clear guidance when requirements aren't met
6. **Mirror preserved**: Keep mirror architecture; only change its upstream source

## Architecture

### Key Architectural Decision: Mirror Preserved

The mirror layer (`.mirrors/`) is preserved in airplane mode. Only its upstream changes:

```
Standard mode:  GitHub → .mirrors/ → workspaces
Airplane mode:  Gitea  → .mirrors/ → workspaces
```

**Why keep the mirror:**

1. **Single clone path (reliability win)**: CloneManager always does `git clone <filesystem path>`. No "HTTP clone" as a separate operational surface with different auth, URL reachability, and failure modes.

2. **Locking/synchronization stays consistent**: The proven model for locking a shared mirror, fetching, and serving ephemeral workspaces safely remains unchanged. Direct HTTP clones from forge would require re-validating concurrency behavior.

3. **Upstream switching is clean**: `mirror.GetFetchURL()` returns either GitHub or Gitea URL depending on mode. Everything else stays the same - mirror is the clone source, forge is the PR system of record.

4. **Extra hop is negligible**: Forge → mirror is a local fetch (fast), mirror → workspace is a local filesystem clone (also fast). Overhead is minimal compared to agent runtime.

**When we would NOT keep the mirror:**
- Planning to drop it entirely long-term
- It creates correctness issues (e.g., complex ref rewriting)
- It materially increases disk usage hurting laptop workflows

None of these apply, so mirror stays.

### Mirror Coherence with PR Merges

**Critical invariant**: If merges happen in the forge (Gitea), the mirror must be updated from the forge at the right times, or cloned workspaces will lag.

**Mirror refresh points:**
- Before creating a new workspace
- Before opening/refreshing a PR diff (if using mirror for diffs)
- **After merging PRs in the forge** (most important)

**Rule**: "Mirror is a cache of the forge's canonical branches."
- Airplane mode: canonical = local Gitea
- Standard mode: canonical = GitHub

### Mirror Upstream State

To keep upstream switching clean and debuggable, persist mirror state:

```go
type MirrorState struct {
    Upstream    string // "github" | "gitea"
    UpstreamURL string // The actual fetch URL
    BaseCommit  string // SHA at mode switch (useful for sync)
}
```

**Mode switching sequence:**
1. Acquire mirror lock
2. Update `mirror.upstream` and `mirror.upstream_url`
3. Record `base_commit` (current HEAD of main)
4. Run `git remote set-url origin <new_url>`
5. Run `git fetch --prune`
6. Release lock

This makes mode switching atomic and debuggable.

### Standard Mode (Current)

```
┌─────────────────────────────────────────────────────────────────┐
│                                                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │ coder-001│  │ coder-002│  │ coder-003│  │ hotfix   │        │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘        │
│       │             │             │             │                │
│       └─────────────┴──────┬──────┴─────────────┘                │
│                            │                                     │
│                            ▼                                     │
│                    ┌──────────────┐                              │
│                    │   .mirrors/  │◄──── git fetch from GitHub   │
│                    └──────────────┘                              │
│                            │                                     │
│                            │ git clone (local, fast)             │
│                            ▼                                     │
│                    ┌──────────────┐                              │
│                    │  Workspaces  │                              │
│                    └──────┬───────┘                              │
│                           │                                      │
│                           │ git push, gh pr create/merge         │
│                           ▼                                      │
│                    ┌──────────────┐                              │
│                    │    GITHUB    │  (remote, requires network)  │
│                    └──────────────┘                              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Airplane Mode (Proposed)

```
┌─────────────────────────────────────────────────────────────────┐
│                        AIRPLANE MODE                             │
│                                                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │ coder-001│  │ coder-002│  │ coder-003│  │ hotfix   │        │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘        │
│       │             │             │             │                │
│       └─────────────┴──────┬──────┴─────────────┘                │
│                            │                                     │
│                            ▼                                     │
│                    ┌──────────────┐                              │
│                    │   .mirrors/  │◄──── git fetch from Gitea    │
│                    └──────────────┘      (same mirror layer!)    │
│                            │                                     │
│                            │ git clone (unchanged code path)     │
│                            ▼                                     │
│                    ┌──────────────┐                              │
│                    │  Workspaces  │                              │
│                    └──────┬───────┘                              │
│                           │                                      │
│                           │ git push, Gitea API (PR ops)         │
│                           ▼                                      │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │                    LOCAL GITEA                             │  │
│  │  localhost:3000                                            │  │
│  │                                                            │  │
│  │  - Receives pushes from all agents                        │  │
│  │  - PRs created via Gitea API                              │  │
│  │  - Architect reviews/merges via Gitea API                 │  │
│  │  - Main branch evolves locally                            │  │
│  │  - Full git history preserved                             │  │
│  └───────────────────────────────────────────────────────────┘  │
│                           │                                      │
│                           │ (no network - fully offline)         │
│                           ▼                                      │
│                    ┌──────────────┐                              │
│                    │    GITHUB    │  (unreachable)               │
│                    └──────────────┘                              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                     SYNC (When Online) - v3 Enhancement          │
│                                                                  │
│  ┌─────────────────────┐         ┌─────────────────────┐        │
│  │    LOCAL GITEA      │ ──────► │      GITHUB         │        │
│  │                     │  push   │                     │        │
│  │  - All branches     │ ──────► │  - All branches     │        │
│  │  - Updated main     │ ──────► │  - Updated main     │        │
│  │  - Merge history    │         │  - Full history     │        │
│  └─────────────────────┘         └─────────────────────┘        │
│                                                                  │
│  MVP: Simple push (assumes no upstream changes)                  │
│  v3:  Conflict detection, divergence warnings, resolution paths  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Data Flow Comparison

| Operation | Online Mode | Offline Mode |
|-----------|-------------|--------------|
| Mirror source | GitHub | Gitea |
| Clone source | `.mirrors/` | `.mirrors/` (unchanged) |
| Push target | GitHub | Gitea |
| PR creation | `gh pr create` | Gitea API (`POST /repos/{owner}/{repo}/pulls`) |
| PR merge | `gh pr merge` | Gitea API (`POST /repos/{owner}/{repo}/pulls/{index}/merge`) |
| PR listing | `gh pr list` | Gitea API (`GET /repos/{owner}/{repo}/pulls`) |

## Workflow

### Single Entrypoint: `maestro --airplane`

The `--airplane` flag is a single idempotent entrypoint that:

1. **Computes airplane context**
   - Sets mode=airplane (CLI flag overrides config default)
   - Forces local forge backend (Gitea)
   - Forces local LLM provider (Ollama)

2. **Runs idempotent ensures**
   - Ensures Docker is running
   - Ensures Gitea container is up and healthy
   - Ensures Ollama is reachable
   - Resolves and validates local models for agent roles
   - Ensures mirror is synced from Gitea

3. **Validates offline readiness**
   - Confirms each required component is ready
   - If any are missing and cannot be auto-fixed: exit with clear guidance
   - Skips GitHub/hosted-API checks (not needed in this mode)

4. **Boots Maestro**
   - Successful boot doubles as final validation
   - Operations proceed using local forge and local LLM

**Graceful failure example:**
```
$ maestro --airplane

Checking airplane mode requirements...
  ✓ Docker running
  ✓ Gitea container healthy (localhost:3000)
  ✓ Ollama reachable (localhost:11434)
  ✗ Model 'qwen2.5-coder:32b' not found locally

Cannot start in airplane mode:
  - Model 'qwen2.5-coder:32b' is not available locally
  - Run 'ollama pull qwen2.5-coder:32b' while online, or
  - Configure a different model in airplane.agents.coder_model

Available local models:
  - qwen2.5-coder:7b (4.7GB)
  - llama3.1:8b (4.9GB)
```

### Story Lifecycle (Offline)

The workflow is identical to online mode, with different targets:

```
1. SETUP
   - Clone from .mirrors/ (unchanged)
   - Create feature branch (unchanged)
   - Configure git identity (unchanged)

2. PLANNING → CODING → TESTING
   - All local operations (unchanged)

3. PREPARE_MERGE
   - git add -A (unchanged)
   - git commit (unchanged)
   - git push origin → GITEA (changed target)
   - GiteaClient.CreatePR() → GITEA API (changed from gh CLI)

4. AWAIT_MERGE
   - Architect reviews via read tools (unchanged)
   - GiteaClient.MergePR() → GITEA API (changed from gh CLI)
   - Coder workspace deleted (unchanged)
   - Mirror updated from GITEA (changed source)

5. Next story clones updated main from mirror
```

### Architect Merge Flow

```go
// Current (online)
func (a *Architect) handleMergeRequest(ctx context.Context, req *MergeRequest) {
    // Review code using read tools...

    result, err := a.github.MergePRWithResult(ctx, req.PRRef, opts)
    // ...
}

// Proposed (mode-aware)
func (a *Architect) handleMergeRequest(ctx context.Context, req *MergeRequest) {
    // Review code using read tools... (unchanged)

    result, err := a.gitClient.MergePRWithResult(ctx, req.PRRef, opts)
    // gitClient is GitHubClient or GiteaClient based on config
}
```

### Returning Online (Sync)

When connectivity returns:

```bash
# Option A: Manual sync via agentctl
agentctl sync --to-github

# Option B: Automatic sync on mode change
# (config.offline_mode = false triggers sync)
```

Sync process:
1. Push all branches from Gitea to GitHub
2. Push updated main branch
3. Push tags (if any)
4. Update mirror from GitHub (pick up any CI-generated changes)
5. Optionally create retrospective PRs for audit trail

## Configuration

### Minimal Config Approach

Configuration follows the principle of minimal duplication. **Config only controls agent model overrides** — forge connection details (Gitea URL, token, etc.) are stored in **project runtime state**, not config.

**Why this split:**
- Config is checked into version control, shouldn't contain generated tokens
- Gitea settings are auto-generated during `--airplane` startup
- Keeps config minimal and declarative (what models to use)
- Runtime state handles operational details (how to connect to forge)

**Config file** (`config.json` or user config):
```json
{
  "default_mode": "standard",

  "git": {
    "repo_url": "https://github.com/org/repo.git",
    "target_branch": "main"
  },

  "agents": {
    "coder_model": "claude-sonnet-4-20250514",
    "architect_model": "o3",
    "pm_model": "claude-sonnet-4-20250514",

    "airplane": {
      "coder_model": "ollama:qwen2.5-coder:32b",
      "architect_model": "ollama:llama3.1:70b",
      "pm_model": "ollama:llama3.1:8b"
    }
  }
}
```

**Runtime state** (`<projectDir>/.maestro/forge_state.json`, permissions `0600`):
```json
{
  "url": "http://localhost:3000",
  "token": "abc123...",
  "owner": "maestro",
  "repo_name": "myproject",
  "container_name": "maestro-gitea-myproject",
  "port": 3000
}
```
Token is local-only (localhost Gitea), auto-generated, and ephemeral — file storage with restrictive permissions is acceptable.

### Config Fields

| Field | Description | Default |
|-------|-------------|---------|
| `default_mode` | Default operating mode (`standard` or `airplane`) | `standard` |
| `agents.airplane.*` | Model overrides for airplane mode | (none - uses preferred list) |

### Mode Resolution

```
CLI flag (--airplane) > config.default_mode > "standard"
```

- `maestro` → uses `config.default_mode` (defaults to standard)
- `maestro --airplane` → forces airplane mode regardless of config
- `maestro --standard` → forces standard mode regardless of config (v2)

### Model Resolution (Airplane Mode)

When in airplane mode, models are resolved in this order:

1. **Explicit config**: If `agents.airplane.coder_model` is set, use it
2. **Preferred list**: Query Ollama for available models, pick first match from preferred list
3. **Fail with guidance**: If no suitable model found, exit with helpful message

**Preferred model list (MVP):**
```go
var PreferredCoderModels = []string{
    "qwen2.5-coder:32b",
    "qwen2.5-coder:14b",
    "qwen2.5-coder:7b",
    "deepseek-coder-v2:16b",
    "codellama:34b",
    "codellama:13b",
}

var PreferredArchitectModels = []string{
    "llama3.1:70b",
    "qwen2.5:72b",
    "llama3.1:8b",
    "qwen2.5:14b",
}
```

### Provider-Aware Validation

Validation is based on **which providers the resolved models actually require**, not purely on mode. This allows future flexibility (e.g., using Ollama models in standard mode, or using a local forge while online).

| Check | When Required |
|-------|---------------|
| `GITHUB_TOKEN` | Any agent uses GitHub as git forge |
| `OPENAI_API_KEY` | Any agent model starts with `o3`, `gpt-`, etc. |
| `ANTHROPIC_API_KEY` | Any agent model starts with `claude-` |
| `GOOGLE_GENAI_API_KEY` | Any agent model starts with `gemini-` |
| `gh` CLI | Git forge is GitHub |
| Docker | Always (containers required) |
| Ollama reachable | Any agent model starts with `ollama:` |
| Gitea healthy | Git forge is Gitea (airplane mode) |
| Local model available | Each `ollama:*` model resolved for an agent |

**Practical effect by mode:**

| Check | Standard Mode (typical) | Airplane Mode (typical) |
|-------|-------------------------|-------------------------|
| `GITHUB_TOKEN` | Required | Skipped |
| `OPENAI_API_KEY` | Required (o3 architect) | Skipped |
| `ANTHROPIC_API_KEY` | Required (Claude coders) | Skipped |
| `gh` CLI | Required | Skipped |
| Ollama reachable | Skipped | Required |
| Gitea healthy | Skipped | Required |
| Local models | Skipped | Required |

The validation engine resolves all agent models first, then validates only the providers those models require.

## Implementation

### Scope by Phase

| Component | MVP | v2 | v3 |
|-----------|-----|----|----|
| `--airplane` CLI flag | ✓ | | |
| Gitea container management | ✓ | | |
| GiteaClient (PR operations) | ✓ | | |
| Mode-aware validation | ✓ | | |
| Mirror upstream switching | ✓ | | |
| Model resolution from config | ✓ | | |
| Graceful failure with guidance | ✓ | | |
| `default_mode` config | ✓ | | |
| SQLite PR/workflow persistence | ✓ | | |
| WebUI mode selection | | ✓ | |
| System profiling (Metal/CUDA/RAM) | | ✓ | |
| Model recommendations in UI | | ✓ | |
| `--standard` CLI flag | | ✓ | |
| Start-from-scratch offline | | | ✓ |
| GitHub sync with conflict detection | | | ✓ |
| Retrospective PR creation | | | ✓ |

### Phase 1: Gitea Infrastructure (MVP)

#### 1.1 Gitea Container Setup

Add Gitea to the maestro infrastructure:

```yaml
# docker-compose.yml (or equivalent)
# NOTE: Container name and ports are per-project to avoid collisions
services:
  gitea:
    # Pin to specific version for offline reproducibility
    # Using 1.21.11 (LTS) - update periodically while online
    image: gitea/gitea:1.21.11
    # Per-project naming: maestro-gitea-{project-name}
    # Derived from project directory name at runtime
    container_name: maestro-gitea-${PROJECT_NAME}
    environment:
      - USER_UID=1000
      - USER_GID=1000
      # Port is allocated dynamically per-project (see below)
      - GITEA__server__ROOT_URL=http://localhost:${GITEA_PORT}
      - GITEA__server__HTTP_PORT=${GITEA_PORT}
    volumes:
      # Per-project volume naming
      - maestro-gitea-${PROJECT_NAME}-data:/data
    ports:
      # MVP: Static port allocation (collision possible)
      # v2: Dynamic port allocation with discovery
      - "${GITEA_PORT}:${GITEA_PORT}"
      - "${GITEA_SSH_PORT}:22"
    restart: unless-stopped

# Per-project volumes prevent data mixing
volumes:
  maestro-gitea-${PROJECT_NAME}-data:
```

**Per-project container management:**

MVP uses explicit per-project naming to avoid data mixing:
- Container: `maestro-gitea-{project-name}` (e.g., `maestro-gitea-myapp`)
- Volume: `maestro-gitea-{project-name}-data`
- Default ports: 3000 (HTTP), 2222 (SSH)

Port collision is a **documented MVP limitation** — running multiple projects concurrently requires manual port configuration or waiting for v2 dynamic allocation.

#### 1.2 Repository Migration

Script to mirror GitHub repo to Gitea:

```bash
#!/bin/bash
# scripts/setup-gitea-mirror.sh

GITHUB_REPO="https://github.com/org/repo.git"
GITEA_URL="http://localhost:3000"
GITEA_ORG="maestro"
GITEA_REPO="repo"

# Create org and repo in Gitea via API
curl -X POST "$GITEA_URL/api/v1/orgs" \
  -H "Authorization: token $GITEA_TOKEN" \
  -d '{"username": "'$GITEA_ORG'"}'

curl -X POST "$GITEA_URL/api/v1/org/$GITEA_ORG/repos" \
  -H "Authorization: token $GITEA_TOKEN" \
  -d '{"name": "'$GITEA_REPO'", "private": false}'

# Push existing mirror to Gitea
cd .mirrors/repo.git
git remote add gitea "$GITEA_URL/$GITEA_ORG/$GITEA_REPO.git"
git push gitea --mirror
```

### Phase 2: GitClient Abstraction

#### 2.1 Interface Definition

```go
// pkg/git/client.go

// GitClient abstracts git hosting operations (GitHub, Gitea, etc.)
type GitClient interface {
    // PR operations
    CreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error)
    GetPR(ctx context.Context, number int) (*PullRequest, error)
    ListPRsForBranch(ctx context.Context, branch string) ([]PullRequest, error)
    GetOrCreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error)
    MergePR(ctx context.Context, ref string, opts PRMergeOptions) error
    MergePRWithResult(ctx context.Context, ref string, opts PRMergeOptions) (*MergeResult, error)
    ClosePR(ctx context.Context, number int) error
    CommentOnPR(ctx context.Context, number int, body string) error

    // Branch operations
    CleanupMergedBranches(ctx context.Context, target string, protected []string) ([]string, error)

    // Info
    GetRepoURL() string
    GetType() string // "github" or "gitea"
}
```

#### 2.2 Gitea Client Implementation

```go
// pkg/git/gitea_client.go

type GiteaClient struct {
    baseURL   string
    token     string
    owner     string
    repo      string
    client    *http.Client
}

func NewGiteaClient(baseURL, token, owner, repo string) *GiteaClient {
    return &GiteaClient{
        baseURL: baseURL,
        token:   token,
        owner:   owner,
        repo:    repo,
        client:  &http.Client{Timeout: 30 * time.Second},
    }
}

func (g *GiteaClient) CreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error) {
    url := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls", g.baseURL, g.owner, g.repo)

    payload := map[string]interface{}{
        "title": opts.Title,
        "body":  opts.Body,
        "head":  opts.Head,
        "base":  opts.Base,
    }

    // POST to Gitea API...
}

func (g *GiteaClient) MergePRWithResult(ctx context.Context, ref string, opts PRMergeOptions) (*MergeResult, error) {
    // Find PR number from ref (branch name or PR number)
    // POST to /api/v1/repos/{owner}/{repo}/pulls/{index}/merge
    // Parse response for success/conflict/error
}

// ... implement remaining interface methods
```

#### 2.3 Client Factory

```go
// pkg/forge/factory.go

// NewForgeClient creates the appropriate forge client based on operating mode.
// Gitea connection details are read from project runtime state (forge_state.json),
// NOT from config. Config only controls agent model overrides.
func NewForgeClient(projectDir string, mode string) (ForgeClient, error) {
    if mode == "airplane" {
        // Load Gitea connection from runtime state
        state, err := LoadForgeState(projectDir)
        if err != nil {
            return nil, fmt.Errorf("load forge state: %w", err)
        }
        return gitea.NewClient(
            state.URL,      // e.g., "http://localhost:3000"
            state.Token,    // Auto-generated on first setup
            state.Owner,    // e.g., "maestro"
            state.RepoName, // e.g., "myproject"
        ), nil
    }

    return github.NewClient(), nil
}
```

```go
// pkg/forge/state.go

// ForgeState is persisted in <projectDir>/.maestro/forge_state.json
// Auto-populated by ensureLocalForge() during --airplane startup
// File permissions: 0600 (owner read/write only)
//
// Token storage rationale: Gitea token is local-only (localhost access),
// auto-generated, and ephemeral. File storage with restrictive permissions
// is acceptable for this use case.
type ForgeState struct {
    URL           string // Gitea base URL
    Token         string // Auto-generated API token
    Owner         string // Organization/user in Gitea
    RepoName      string // Repository name
    Port          int    // HTTP port (for container management)
    ContainerName string // Per-project container name
}
```

### Phase 3: Mirror Management (MVP)

#### 3.1 Dynamic Remote Selection

```go
// pkg/mirror/manager.go

// GetFetchURL returns the upstream URL based on operating mode.
// In airplane mode, reads from runtime state (not config).
func (m *Manager) GetFetchURL() string {
    if m.mode == "airplane" {
        // Forge state is loaded from <projectDir>/.maestro/forge_state.json
        state, err := forge.LoadForgeState(m.projectDir)
        if err != nil {
            // Fallback to GitHub URL if state unavailable
            return m.githubURL
        }
        return fmt.Sprintf("%s/%s/%s.git",
            state.URL,
            state.Owner,
            state.RepoName,
        )
    }
    return m.githubURL // From config.git.repo_url
}

func (m *Manager) UpdateMirror(ctx context.Context) error {
    fetchURL := m.GetFetchURL()

    // Update remote URL if changed
    currentURL := m.getCurrentRemoteURL()
    if currentURL != fetchURL {
        m.git.Run(ctx, m.mirrorPath, "remote", "set-url", "origin", fetchURL)
    }

    // Fetch updates
    return m.git.Run(ctx, m.mirrorPath, "remote", "update", "--prune")
}
```

### Phase 4: Sync Command (MVP - Basic)

> **Architecture Decision**: Sync is implemented as `maestro --sync` flag rather than a separate
> `agentctl` binary. This keeps Maestro as a single binary for simpler distribution via Homebrew
> and other package managers. The sync logic lives in `pkg/sync/` and is designed to be invokable
> from multiple contexts (CLI, WebUI, PM agent) for future flexibility.

#### 4.1 CLI Integration (maestro --sync)

```bash
# Sync offline changes to GitHub
maestro --sync

# Preview what would be synced without making changes
maestro --sync --sync-dry-run
```

The `--sync` flag is handled in `cmd/maestro/main.go` before the full orchestrator starts:

```go
// cmd/maestro/main.go

// Handle sync mode (runs and exits before full orchestrator startup)
if *syncMode {
    exitCode := runSyncMode(*projectDir, *syncDryRun)
    os.Exit(exitCode)
}
```

#### 4.2 Reusable Sync Package

The sync logic is in `pkg/sync/syncer.go`, designed for invocation from multiple contexts:

```go
// pkg/sync/syncer.go
//
// This package is designed to be invoked from multiple contexts:
// - CLI via `maestro --sync`
// - WebUI via API endpoint (future)
// - PM agent via tool call (future)

type Syncer struct {
    logger     *logx.Logger
    gitHub     *gitHubTarget
    gitea      *giteaSource
    projectDir string
    dryRun     bool
}

type Result struct {
    BranchesPushed []string
    Warnings       []string
    Success        bool
    MainPushed     bool
    MainUpToDate   bool
    MirrorUpdated  bool
}

func NewSyncer(projectDir string, dryRun bool) (*Syncer, error) {
    // Load config for GitHub URL
    // Load forge state for Gitea details
    // Return configured syncer
}

func (s *Syncer) SyncToGitHub(ctx context.Context) (*Result, error) {
    // 1. Create temp directory
    // 2. Clone from Gitea
    // 3. Add GitHub as remote
    // 4. Fetch from GitHub (detect divergence)
    // 5. Push all branches to GitHub
    // 6. Push main branch
    // 7. Update mirror from GitHub
}
```

#### 4.3 Future WebUI/PM Integration

The sync package can be invoked programmatically:

```go
// From WebUI handler or PM agent tool
syncer, err := sync.NewSyncer(projectDir, false)
if err != nil {
    return err
}
result, err := syncer.SyncToGitHub(ctx)
```

## Future Enhancements (v2/v3)

### v2: WebUI Configuration

The WebUI will provide airplane mode configuration:

```
┌─────────────────────────────────────────────────────────────┐
│  Maestro Settings                                            │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Operating Mode                                              │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ ○ Standard (GitHub + Cloud LLMs)                    │    │
│  │ ● Airplane (Local Forge + Ollama)                   │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
│  System Profile                                              │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ Apple M2 Max • 64GB RAM • Metal GPU                 │    │
│  │ Recommended models: qwen2.5-coder:32b, llama3.1:70b │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
│  Local Models                                ○ Auto-select   │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ Coder:     [qwen2.5-coder:32b     ▼] ✓ Available    │    │
│  │ Architect: [llama3.1:70b          ▼] ✓ Available    │    │
│  │ PM:        [llama3.1:8b           ▼] ✓ Available    │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
│  Available Models (Ollama)                                   │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ ✓ qwen2.5-coder:32b (18GB)                          │    │
│  │ ✓ llama3.1:70b (40GB)                               │    │
│  │ ✓ llama3.1:8b (4.9GB)                               │    │
│  │ ○ deepseek-coder-v2:16b (not installed) [Pull]      │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
│                              [Save]  [Switch to Airplane]    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**System profiling detects:**
- CPU type (Apple Silicon / Intel / AMD)
- GPU availability (Metal / CUDA / ROCm / None)
- Available RAM
- Disk space for models

**Model recommendations based on:**
- Available VRAM/RAM for model loading
- Model quality vs resource tradeoff
- Tested compatibility with Maestro workflows

### v3: Start From Scratch Offline

Support initializing a new project without any GitHub repository:

```bash
$ maestro --airplane init myproject

Creating new offline project 'myproject'...
  ✓ Created project directory
  ✓ Initialized git repository
  ✓ Created Gitea repository (localhost:3000/maestro/myproject)
  ✓ Configured maestro settings
  ✓ Ready for development

Start working:
  cd myproject
  maestro --airplane
```

This enables the "transatlantic flight, new idea" workflow - start a project
from scratch while completely offline, then sync to GitHub later.

### v3: Sync with Conflict Detection

Enhanced sync that handles upstream changes:

```bash
$ maestro --sync

Checking upstream state...
  ⚠ GitHub main has advanced since airplane mode entry

  Local:  abc1234 "Story 003: Add user auth"
  Remote: def5678 "Hotfix: Security patch" (2 commits ahead)

Options:
  1. Merge: Fetch remote, merge locally, then push (safe)
  2. Rebase: Rebase local work onto remote (cleaner history)
  3. Force: Push local state, overwriting remote (destructive)
  4. Abort: Cancel sync, investigate manually

Choice [1/2/3/4]:
```

## Testing

### Unit Tests

```go
// pkg/git/gitea_client_test.go

func TestGiteaClient_CreatePR(t *testing.T) {
    // Mock Gitea API server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        assert.Equal(t, "/api/v1/repos/maestro/repo/pulls", r.URL.Path)
        assert.Equal(t, "POST", r.Method)

        w.WriteHeader(http.StatusCreated)
        json.NewEncoder(w).Encode(map[string]interface{}{
            "number": 1,
            "html_url": "http://localhost:3000/maestro/repo/pulls/1",
        })
    }))
    defer server.Close()

    client := NewGiteaClient(server.URL, "token", "maestro", "repo")
    pr, err := client.CreatePR(context.Background(), PRCreateOptions{
        Title: "Test PR",
        Head:  "feature",
        Base:  "main",
    })

    assert.NoError(t, err)
    assert.Equal(t, 1, pr.Number)
}
```

### Integration Tests

```go
// tests/integration/offline_mode_test.go

func TestOfflineWorkflow(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Requires: Gitea running at localhost:3000
    // Requires: GITEA_TOKEN environment variable

    ctx := context.Background()

    // 1. Setup: Mirror GitHub repo to Gitea
    // 2. Enable offline mode
    // 3. Run coder workflow (clone, code, PR, merge)
    // 4. Verify: PR exists in Gitea
    // 5. Verify: Main branch updated
    // 6. Sync to GitHub
    // 7. Verify: GitHub has changes
}
```

## Rollout Plan

### MVP Deliverables

1. **`--airplane` CLI flag**: Single idempotent entrypoint
2. **Gitea container management**: `ensureLocalForge()` function
3. **GiteaClient**: Implements ForgeClient interface for PR operations
4. **Mode-aware validation**: Skip irrelevant checks based on mode
5. **Mirror upstream switching**: `GetFetchURL()` returns Gitea in airplane mode
6. **Model resolution**: Config overrides + preferred list fallback
7. **Graceful failure**: Clear guidance when requirements aren't met
8. **`default_mode` config**: Persistent mode preference
9. **SQLite PR persistence**: Workflow identity survives restarts
10. **`--sync` flag**: Single binary sync via `maestro --sync` (reusable `pkg/sync/` for WebUI/PM)

### v2 Enhancements

1. **WebUI mode selection**: Switch between standard/airplane in UI
2. **System profiling**: Detect Metal/CUDA, available RAM
3. **Model recommendations**: UI suggests models based on hardware
4. **`--standard` CLI flag**: Explicit override to standard mode
5. **Model download from UI**: Pull Ollama models via WebUI

### v3 Enhancements

1. **Start-from-scratch offline**: Initialize new project without GitHub
2. **GitHub sync with conflict detection**: Warn on upstream divergence
3. **Retrospective PR creation**: Optional audit trail on GitHub
4. **Sync resolution paths**: Merge/rebase/force options

## Decisions Made

Based on feedback, these questions have been resolved:

| Question | Decision |
|----------|----------|
| Gitea token management | Auto-generated on first setup, stored in `.maestro/forge/` |
| Sync conflict handling | MVP: Trust "no one else working" constraint, simple push |
| PR audit trail | v3: Optional retrospective PRs, default off |
| Partial offline (mixed mode) | No: All-or-nothing mode for simplicity |
| Gitea data location | `<projectDir>/.maestro/forge/` for portability |
| Mirror architecture | Keep mirror layer, change upstream source only |
| Model auto-selection | MVP: Preferred list with graceful failure; v2: UI recommendations |

## Open Questions (Remaining)

1. **Port collisions**: Multiple Maestro instances may collide on WebUI/Gitea ports
   - Document as limitation for MVP
   - v2: Dynamic port allocation or per-project port config

2. **Ollama Docker fallback**: Should we auto-start Ollama in Docker if not running?
   - MVP: No, require host Ollama
   - v2: Consider Docker fallback with GPU passthrough caveats

3. **CI parity**: Local test execution for CI-like validation
   - MVP: No CI parity
   - Future: Consider `act` or local runners

## Known Limitations (MVP)

These limitations are documented for MVP and may be addressed in future versions:

1. **Port collisions**: Multiple concurrent Maestro projects may collide on default ports:
   - WebUI port (default: 8080)
   - Gitea HTTP port (default: 3000)
   - Gitea SSH port (default: 2222)
   - **Container/volume names are per-project** (`maestro-gitea-{project}`) so data won't mix
   - **Ports are the collision risk** — two projects using port 3000 will conflict
   - Workaround: Run one project at a time, or manually configure different ports per project
   - v2: Dynamic port allocation with automatic discovery

2. **Model download requires connectivity**: Ollama models must be pre-downloaded
   - Cannot pull models while truly offline
   - Workaround: Run `ollama pull <model>` before going offline

3. **No CI parity**: Local testing doesn't replicate GitHub Actions
   - Tests run in container, but no workflow simulation
   - Workaround: Test thoroughly before syncing to GitHub

4. **One Gitea container per project**: Each project gets its own Gitea instance
   - Container: `maestro-gitea-{project-name}`
   - Volume: `maestro-gitea-{project-name}-data`
   - No shared multi-project Gitea (keeps isolation simple)
   - Each project's Gitea is independent

5. **No partial offline**: Cannot mix online/offline stories in same session
   - Mode is all-or-nothing per session
   - Workaround: Switch modes between sessions

6. **Sync assumes clean upstream**: MVP sync doesn't handle upstream divergence
   - Assumes "no one else working" constraint holds
   - v3 will add conflict detection

## Alternatives Considered

### Alternative A: Use Mirror as Writable (Rejected)

Push directly to bare mirror, treating it as authoritative.

**Why rejected**:
- No PR workflow (git has no PR concept)
- No merge conflict handling machinery
- Would need custom merge tooling
- Loses review/approval workflow entirely

### Alternative B: Serialized Local Merges (Rejected)

Queue stories to execute serially, merging each to a persistent local branch.

**Why rejected**:
- Loses parallelism (major performance regression)
- Still needs persistent storage (where?)
- Doesn't match online workflow (testing burden)

### Alternative C: GitLab Instead of Gitea (Considered)

GitLab has similar capabilities but heavier footprint.

**Decision**: Gitea preferred for:
- Smaller image (~100MB vs ~1GB)
- Faster startup
- Simpler configuration
- API compatibility with GitHub is excellent

### Alternative D: Forgejo Instead of Gitea (Viable)

Forgejo is a Gitea fork with community governance.

**Decision**: Either works. Gitea chosen for:
- Larger ecosystem
- More documentation
- Same API compatibility
- Could switch later with minimal changes

## Security Considerations

1. **Gitea runs locally**: No external exposure needed for offline use
2. **Token storage**: GITEA_TOKEN should be treated like GITHUB_TOKEN
3. **Network isolation**: Gitea container can be fully isolated from external network
4. **Data at rest**: Gitea volume contains repository data - same security as .mirrors/

## Success Criteria

1. **Full offline operation**: Complete story lifecycle without any network calls
2. **Multi-agent support**: 3+ coders working concurrently offline
3. **Clean sync**: All offline work syncs to GitHub without conflicts (given constraint)
4. **Workflow parity**: Developer experience matches online mode
5. **Minimal overhead**: Gitea adds <200MB disk, <100MB RAM

## Appendix: Gitea CLI (tea) - Optional Tooling

**Note**: The `tea` CLI is **NOT a runtime dependency**. Maestro uses the Gitea HTTP API directly via `GiteaClient`. The `tea` CLI is provided here as optional tooling for manual debugging and troubleshooting.

```bash
# Installation (optional - for manual debugging only)
go install code.gitea.io/tea@latest

# Configuration
tea login add --name local --url http://localhost:3000 --token $GITEA_TOKEN

# Manual debugging commands (not used by Maestro at runtime)
tea pr create --title "Story 001" --head feature --base main
tea pr list
tea pr view 1
tea pr merge 1 --style squash --delete
tea pr close 1

# Direct API examples (what GiteaClient uses internally)
# Create PR
curl -X POST "http://localhost:3000/api/v1/repos/maestro/myproject/pulls" \
  -H "Authorization: token $GITEA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Story 001","head":"feature","base":"main"}'

# List PRs
curl "http://localhost:3000/api/v1/repos/maestro/myproject/pulls" \
  -H "Authorization: token $GITEA_TOKEN"

# Merge PR
curl -X POST "http://localhost:3000/api/v1/repos/maestro/myproject/pulls/1/merge" \
  -H "Authorization: token $GITEA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"do":"squash","delete_branch_after_merge":true}'
```

## References

- [Gitea Documentation](https://docs.gitea.io/)
- [Gitea API Reference](https://docs.gitea.io/en-us/api-usage/)
- [Tea CLI Documentation](https://gitea.com/gitea/tea)
- [Ollama Integration](./OLLAMA.md) - Local LLM support
- [Git Workflow](./GIT.md) - Current git architecture

---

## Phase 1 Implementation Plan

**Status**: Ready for implementation
**Estimated Tasks**: 45 items across 10 work packages

### Work Package 1: Configuration Foundation

#### 1.1 Add Operating Mode to Config
- [ ] Add `DefaultMode` field to `Config` struct in `pkg/config/config.go`
- [ ] Add `AirplaneAgents` struct for model overrides (`CoderModel`, `ArchitectModel`, `PMModel`)
- [ ] Add `AirplaneAgents` field to `AgentConfig` struct
- [ ] Update `applyDefaults()` to set `DefaultMode = "standard"`
- [ ] Update `createDefaultConfig()` with airplane agent defaults

**Tests:**
- [ ] `TestConfig_DefaultMode_Standard` - Verify default mode is "standard"
- [ ] `TestConfig_AirplaneAgents_Override` - Verify airplane overrides are applied in airplane mode
- [ ] `TestConfig_LoadWithAirplaneSection` - Verify config with airplane section loads correctly

#### 1.2 ForgeState Persistence
- [ ] Create `pkg/forge/state.go` with `ForgeState` struct
- [ ] Implement `SaveForgeState(projectDir, state)` with 0600 permissions
- [ ] Implement `LoadForgeState(projectDir)`
- [ ] Implement `ForgeStateExists(projectDir)` check

**Tests:**
- [ ] `TestForgeState_SaveLoad_RoundTrip` - Save and load state correctly
- [ ] `TestForgeState_Permissions` - Verify file has 0600 permissions
- [ ] `TestForgeState_MissingFile` - Returns appropriate error when file doesn't exist

### Work Package 2: CLI Flag and Mode Resolution

#### 2.1 Add --airplane Flag
- [ ] Add `airplane` flag in `cmd/maestro/main.go`
- [ ] Pass mode to `run()` function
- [ ] Pass mode through to kernel initialization

#### 2.2 Mode Resolution Logic
- [ ] Add `ResolveOperatingMode(cliFlag string, configDefault string)` to `pkg/config/config.go`
- [ ] Implement precedence: CLI flag > config default_mode > "standard"
- [ ] Store resolved mode in runtime config (not persisted)

**Tests:**
- [ ] `TestConfig_ResolveOperatingMode_CLIOverrides` - --airplane overrides config
- [ ] `TestConfig_ResolveOperatingMode_ConfigDefault` - Uses config when no CLI flag
- [ ] `TestConfig_ResolveOperatingMode_FallbackStandard` - Falls back to standard when nothing set

### Work Package 3: Preflight Checks

#### 3.1 Preflight Check Engine
- [ ] Create `pkg/preflight/checks.go` with provider requirement detection
- [ ] Implement `RequiredProviders(cfg *config.Config, mode string)` - returns set of providers needed
- [ ] Implement `RunPreflightChecks(ctx, cfg, mode)` - validates all required providers

#### 3.2 Provider-Specific Validators
- [ ] `pkg/preflight/validators.go` with individual check functions:
- [ ] `CheckGitHub()` - GITHUB_TOKEN, gh CLI
- [ ] `CheckAnthropic()` - ANTHROPIC_API_KEY
- [ ] `CheckOpenAI()` - OPENAI_API_KEY
- [ ] `CheckGoogle()` - GOOGLE_GENAI_API_KEY
- [ ] `CheckOllama()` - Ollama reachability, model availability
- [ ] `CheckGitea()` - Gitea container health, API reachability

#### 3.3 Graceful Failure with Guidance
- [ ] Create `pkg/preflight/guidance.go` with user-friendly error messages
- [ ] Implement `FormatPreflightError(err)` with actionable guidance
- [ ] Add model availability listing when Ollama model missing

**Tests:**
- [ ] `TestPreflight_StandardMode_RequiresGitHub` - Standard mode needs GitHub
- [ ] `TestPreflight_AirplaneMode_RequiresOllama` - Airplane mode needs Ollama
- [ ] `TestPreflight_MixedProviders` - Ollama model in standard mode validates Ollama
- [ ] `TestPreflight_GracefulFailure_ModelMissing` - Shows helpful message when model missing
- [ ] `TestPreflight_SkipsIrrelevantProviders` - Doesn't validate unused providers

### Work Package 4: Gitea Container Management

#### 4.1 Container Lifecycle
- [ ] Create `pkg/forge/gitea/container.go` with Gitea container management
- [ ] Implement `EnsureContainer(ctx, projectDir, projectName)` - idempotent start
- [ ] Implement `StopContainer(ctx, containerName)` - graceful shutdown
- [ ] Implement `IsHealthy(ctx, url)` - health check via API
- [ ] Implement `WaitForReady(ctx, url, timeout)` - wait with backoff

#### 4.2 Container Configuration
- [ ] Per-project container naming: `maestro-gitea-{project-name}`
- [ ] Per-project volume naming: `maestro-gitea-{project-name}-data`
- [ ] Default port allocation (3000 HTTP, 2222 SSH)
- [ ] Pin Gitea image to `gitea/gitea:1.21.11`

#### 4.3 Initial Setup
- [ ] Implement `SetupRepository(ctx, state, mirrorPath)` - create org/repo from mirror
- [ ] Implement `GenerateToken(ctx, url)` - auto-generate API token
- [ ] Push mirror to Gitea on first setup

**Tests:**
- [ ] `TestGiteaContainer_Ensure_StartsWhenMissing` - Starts container if not running
- [ ] `TestGiteaContainer_Ensure_IdempotentWhenRunning` - No-op when already running
- [ ] `TestGiteaContainer_HealthCheck_Healthy` - Returns true for healthy Gitea
- [ ] `TestGiteaContainer_HealthCheck_Unhealthy` - Returns false/error for unreachable
- [ ] `TestGiteaContainer_Naming_PerProject` - Uses project-specific names
- [ ] `TestGiteaContainer_SetupRepository_PushesFromMirror` - Initial push works

### Work Package 5: ForgeClient Implementation

#### 5.1 ForgeClient Interface
- [ ] Create `pkg/forge/client.go` with `ForgeClient` interface
- [ ] Define PR operations: `CreatePR`, `GetPR`, `MergePR`, `ListPRs`, `ClosePR`
- [ ] Define branch operations: `CleanupMergedBranches`
- [ ] Add `Type()` method for runtime type checking ("github" | "gitea")

#### 5.2 GiteaClient Implementation
- [ ] Create `pkg/forge/gitea/client.go`
- [ ] Implement `NewClient(url, token, owner, repo)`
- [ ] Implement `CreatePR()` via `POST /repos/{owner}/{repo}/pulls`
- [ ] Implement `GetPR()` via `GET /repos/{owner}/{repo}/pulls/{index}`
- [ ] Implement `MergePR()` via `POST /repos/{owner}/{repo}/pulls/{index}/merge`
- [ ] Implement `ListPRsForBranch()` via `GET /repos/{owner}/{repo}/pulls?head={branch}`
- [ ] Implement `ClosePR()` via `PATCH /repos/{owner}/{repo}/pulls/{index}`
- [ ] Implement `GetOrCreatePR()` - idempotent PR creation

#### 5.3 GitHubClient Extraction
- [ ] Create `pkg/forge/github/client.go` - extract from existing GitHub code
- [ ] Ensure it implements `ForgeClient` interface
- [ ] Wrap existing `gh` CLI calls or migrate to API

#### 5.4 Client Factory
- [ ] Create `pkg/forge/factory.go` with `NewForgeClient(projectDir, mode)`
- [ ] Load ForgeState for airplane mode
- [ ] Return GiteaClient or GitHubClient based on mode

**Tests:**
- [ ] `TestGiteaClient_CreatePR_Success` - Creates PR via API
- [ ] `TestGiteaClient_CreatePR_Conflict` - Handles existing PR gracefully
- [ ] `TestGiteaClient_MergePR_Success` - Merges PR via API
- [ ] `TestGiteaClient_MergePR_Conflict` - Returns conflict info
- [ ] `TestGiteaClient_GetOrCreatePR_Idempotent` - Returns existing PR if present
- [ ] `TestForgeFactory_StandardMode` - Returns GitHubClient
- [ ] `TestForgeFactory_AirplaneMode` - Returns GiteaClient

### Work Package 6: Mirror Upstream Switching

#### 6.1 Mirror Manager Updates
- [ ] Add `mode` field to mirror Manager struct
- [ ] Update `GetFetchURL()` to check mode and load ForgeState for Gitea URL
- [ ] Implement `SwitchUpstream(ctx, newMode)` for mode transitions
- [ ] Persist mirror state (`upstream`, `upstream_url`, `base_commit`)

#### 6.2 Mirror Coherence
- [ ] Add `RefreshFromForge(ctx)` - fetch after PR merges
- [ ] Call refresh after merge in architect flow (uses ForgeClient)
- [ ] Verify clone source is always mirror (not direct from forge)

**Tests:**
- [ ] `TestMirror_GetFetchURL_StandardMode` - Returns GitHub URL
- [ ] `TestMirror_GetFetchURL_AirplaneMode` - Returns Gitea URL from ForgeState
- [ ] `TestMirror_SwitchUpstream_UpdatesRemote` - git remote set-url called
- [ ] `TestMirror_RefreshAfterMerge` - Mirror updated after PR merge

### Work Package 7: Model Resolution

#### 7.1 Airplane Model Resolution
- [ ] Create `pkg/models/resolver.go`
- [ ] Implement `ResolveCoderModel(cfg, mode)` - returns effective model
- [ ] Implement `ResolveArchitectModel(cfg, mode)` - returns effective model
- [ ] Implement `ResolvePMModel(cfg, mode)` - returns effective model

#### 7.2 Preferred Model Fallback
- [ ] Define `PreferredCoderModels` list (qwen2.5-coder variants, deepseek, codellama)
- [ ] Define `PreferredArchitectModels` list (llama3.1, qwen2.5 variants)
- [ ] Implement `FindAvailableModel(ctx, preferredList)` - queries Ollama
- [ ] Graceful failure when no suitable model found

**Tests:**
- [ ] `TestModelResolver_ExplicitConfig` - Uses config when set
- [ ] `TestModelResolver_PreferredFallback` - Falls back to preferred list
- [ ] `TestModelResolver_NoModelAvailable` - Returns helpful error

### Work Package 8: Airplane Startup Flow

#### 8.1 Startup Orchestration
- [ ] Create `internal/orch/airplane.go` with `AirplaneOrchestrator`
- [ ] Implement `PrepareAirplaneMode(ctx, projectDir)` - orchestrates all checks
- [ ] Sequence: Docker → Gitea → Ollama → Models → Mirror → Boot
- [ ] Integrate with `cmd/maestro/flows.go` (call from OrchestratorFlow or new AirplaneFlow)

#### 8.2 Idempotent Ensures
- [ ] `ensureDocker()` - verify Docker daemon running
- [ ] `ensureGitea()` - use `pkg/forge/gitea` to start container, wait healthy, setup repo
- [ ] `ensureOllama()` - verify Ollama reachable
- [ ] `ensureModels()` - use `pkg/models` to verify all required models available
- [ ] `ensureMirror()` - switch upstream if needed, fetch from Gitea

**Tests:**
- [ ] `TestAirplaneOrch_AllComponentsReady` - Boots successfully
- [ ] `TestAirplaneOrch_GiteaMissing` - Starts Gitea automatically
- [ ] `TestAirplaneOrch_OllamaUnreachable` - Fails with guidance
- [ ] `TestAirplaneOrch_ModelMissing` - Fails with available models list
- [ ] `TestAirplaneOrch_Idempotent` - Second run is fast no-op

### Work Package 9: Agent Integration

#### 9.1 Coder Integration
- [ ] Update coder PREPARE_MERGE to use `ForgeClient` interface
- [ ] Update push target based on mode (Gitea vs GitHub)
- [ ] PR creation via `ForgeClient.CreatePR()` in airplane mode

#### 9.2 Architect Integration
- [ ] Update architect merge flow to use `ForgeClient` interface
- [ ] PR merge via `ForgeClient.MergePR()` in airplane mode
- [ ] Mirror refresh after merge

**Tests:**
- [ ] `TestCoder_PrepareMerge_AirplaneMode` - Pushes to Gitea, creates PR via ForgeClient
- [ ] `TestArchitect_Merge_AirplaneMode` - Merges via ForgeClient, refreshes mirror

### Work Package 10: Sync Command (MVP)

> **Architecture Decision**: Sync is `maestro --sync` (not a separate `agentctl` binary).
> Single binary distribution via Homebrew. Sync logic in `pkg/sync/` for reusability.

#### 10.1 Basic Sync Implementation
- [x] Add `--sync` and `--sync-dry-run` flags to `cmd/maestro/main.go`
- [x] Create `pkg/sync/syncer.go` with reusable `Syncer` type
- [x] Implement `SyncToGitHub(ctx)` that:
  - Clones from Gitea to temp directory
  - Adds GitHub as remote
  - Pushes all branches to GitHub
  - Pushes main branch
  - Updates mirror from GitHub after sync
- [x] Design for future invocation from WebUI/PM agent

**Tests:**
- [ ] `TestSync_PushesToGitHub` - Pushes Gitea changes to GitHub
- [ ] `TestSync_UpdatesMirror` - Mirror refreshed from GitHub after sync
- [ ] `TestSync_NotInAirplaneMode` - Fails gracefully in standard mode

### Work Package 11: Documentation Updates

#### 11.1 README.md Updates
- [ ] Update "Operating Modes" section to include Airplane Mode
- [ ] Update requirements section (GitHub no longer hard requirement in airplane mode)
- [ ] Add airplane mode to the modes table
- [ ] Document `--airplane` CLI flag
- [ ] Add link to `docs/AIRPLANE_MODE.md` for details

#### 11.2 Mode Documentation
- [ ] Update `docs/MODES.md` if it exists, or create reference in README
- [ ] Clarify terminology: Operating modes vs Coder mode vs Airplane mode

---

### Testing Notes

**Local Ollama Model Available:**
- `mistral-nemo:latest` (7.1 GB) - Use for integration tests
- Tests should use this model or mock Ollama responses for unit tests

---

### Integration Tests

#### End-to-End Airplane Mode Test
- [ ] `TestE2E_AirplaneMode_FullStoryCycle`
  1. Start with `--airplane` flag
  2. Verify Gitea container started
  3. Coder completes story (push to Gitea, create PR)
  4. Architect reviews and merges (via Gitea)
  5. Mirror updated from Gitea
  6. Second story clones from updated mirror
  7. Verify no network calls to GitHub

#### Mode Switching Test
- [ ] `TestE2E_ModeSwitching_StandardToAirplane`
  1. Start in standard mode, process story
  2. Stop orchestrator
  3. Restart with `--airplane`
  4. Verify mirror switches upstream to Gitea
  5. Continue processing

---

### Task Tracking

| Package | Tasks | Tests | Status |
|---------|-------|-------|--------|
| WP1: Config Foundation | 5 | 3 | ✅ Complete |
| WP2: CLI Flag & Mode | 3 | 3 | ✅ Complete |
| WP3: Preflight Checks | 8 | 5 | ✅ Complete |
| WP4: Gitea Container (`pkg/forge/gitea`) | 8 | 6 | ✅ Complete |
| WP5: ForgeClient (`pkg/forge`) | 12 | 7 | ✅ Complete |
| WP6: Mirror Switching | 4 | 4 | ✅ Complete |
| WP7: Model Resolution | 5 | 3 | ✅ Complete |
| WP8: Airplane Orchestrator (`internal/orch`) | 6 | 5 | ✅ Complete |
| WP9: Agent Integration | 4 | 2 | ✅ Complete |
| WP10: Sync Command | 4 | 3 | ✅ Complete |
| WP11: Documentation Updates | 7 | - | ✅ Complete |
| **Integration Tests** | - | 2 | Not Started |
| **Total** | **66** | **43** | **MVP Complete** |

### Implementation Order

Recommended implementation sequence:

1. **WP1 + WP2** - Config and CLI (foundation)
2. **WP3** - Preflight checks (`pkg/preflight`)
3. **WP4** - Gitea container (`pkg/forge/gitea`)
4. **WP5** - ForgeClient (`pkg/forge` + subpackages)
5. **WP6** - Mirror switching (integrate with ForgeClient)
6. **WP7** - Model resolution (`pkg/models`)
7. **WP8** - Airplane orchestrator (`internal/orch/airplane.go`)
8. **WP9** - Agent integration (coder/architect use ForgeClient)
9. **WP10** - Sync command (day-2 operation)
10. **Integration tests** (validation)
11. **WP11** - Documentation updates (README.md, modes clarification)

### Files to Create

```
pkg/forge/
├── state.go              # ForgeState persistence (0600 permissions)
├── state_test.go
├── client.go             # ForgeClient interface (PR operations)
├── factory.go            # Client factory (returns GitHub or Gitea client)
├── factory_test.go
├── gitea/
│   ├── client.go         # GiteaClient implementation
│   ├── client_test.go
│   ├── container.go      # Gitea container lifecycle management
│   ├── container_test.go
│   ├── setup.go          # Repository setup and initialization
│   ├── setup_test.go
│   └── init.go           # Auto-registration via init()
└── github/
    ├── client.go         # GitHubClient adapter (wraps pkg/github)
    └── init.go           # Auto-registration via init()

pkg/preflight/
├── checks.go             # Provider requirement detection
├── validators.go         # Per-provider validators (GitHub, Anthropic, Ollama, Gitea, etc.)
├── guidance.go           # User-friendly error messages with actionable guidance
└── preflight_test.go

pkg/sync/
└── syncer.go             # Reusable sync logic (CLI, WebUI, PM can all invoke)

internal/orch/
└── airplane.go           # AirplaneOrchestrator (parallel to StartupOrchestrator)

cmd/maestro/
└── main.go               # --sync and --sync-dry-run flags (no separate agentctl binary)

tests/integration/
└── airplane_mode_test.go # E2E integration tests
```

**Package naming rationale:**
- `pkg/forge/` - "Forge" is the generic term for git hosting (GitHub, Gitea, GitLab, etc.)
- `pkg/forge/gitea/` and `pkg/forge/github/` - Subpackages for each forge implementation
- `pkg/preflight/` - "Preflight checks" is clearer than generic "validation"
- `pkg/sync/` - Reusable sync logic, invokable from CLI (`--sync`), WebUI, or PM agent
- **No `cmd/agentctl/`** - Single binary distribution; sync is `maestro --sync`
- **No `pkg/mode/`** - Mode resolution is simple (CLI > config > default), lives in config
- **No `pkg/airplane/`** - Orchestration logic goes in `internal/orch/airplane.go`

### Files to Modify

```
pkg/config/config.go           # Add DefaultMode, AirplaneAgents, mode resolution
cmd/maestro/main.go            # Add --airplane flag, pass to flows
cmd/maestro/flows.go           # Call AirplaneOrchestrator, possibly add AirplaneFlow
pkg/mirror/manager.go          # Add mode-aware GetFetchURL, use ForgeClient
pkg/coder/prepare_merge.go     # Use ForgeClient interface
pkg/architect/merge.go         # Use ForgeClient interface
README.md                      # Update modes table, requirements, add airplane mode
docs/MODES.md                  # Update if exists (clarify mode terminology)
```
