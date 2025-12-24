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
│                           │ git push, tea pr create/merge        │
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
| PR creation | `gh pr create` | Gitea API / `tea pr create` |
| PR merge | `gh pr merge` | Gitea API / `tea pr merge` |
| PR listing | `gh pr list` | Gitea API / `tea pr list` |

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
   - tea pr create → GITEA (changed from gh)

4. AWAIT_MERGE
   - Architect reviews via read tools (unchanged)
   - tea pr merge → GITEA (changed from gh)
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

Configuration follows the principle of minimal duplication. Airplane mode settings are overrides, not a parallel config tree.

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

### Mode-Aware Validation

| Check | Standard Mode | Airplane Mode |
|-------|---------------|---------------|
| `GITHUB_TOKEN` | Required | Skipped |
| `OPENAI_API_KEY` | Required (if using o3) | Skipped |
| `ANTHROPIC_API_KEY` | Required (if using Claude) | Skipped |
| `gh` CLI | Required | Skipped |
| Docker | Required | Required |
| Ollama reachable | Skipped | Required |
| Gitea healthy | Skipped | Required |
| Local models available | Skipped | Required |

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
services:
  gitea:
    image: gitea/gitea:latest
    container_name: maestro-gitea
    environment:
      - USER_UID=1000
      - USER_GID=1000
      - GITEA__server__ROOT_URL=http://localhost:3000
      - GITEA__server__HTTP_PORT=3000
    volumes:
      - gitea-data:/data
    ports:
      - "3000:3000"
      - "2222:22"
    restart: unless-stopped

volumes:
  gitea-data:
```

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
// pkg/git/factory.go

func NewGitClient(cfg *config.Config) (GitClient, error) {
    if cfg.Git.OfflineMode {
        return NewGiteaClient(
            cfg.Git.GiteaURL,
            cfg.Git.GiteaToken,
            cfg.Git.GiteaOwner,
            cfg.Git.GiteaRepo,
        ), nil
    }

    return NewGitHubClient(), nil
}
```

### Phase 3: Mirror Management (MVP)

#### 3.1 Dynamic Remote Selection

```go
// pkg/mirror/manager.go

func (m *Manager) GetFetchURL() string {
    if m.config.Git.OfflineMode && m.config.Git.Gitea != nil {
        return fmt.Sprintf("%s/%s/%s.git",
            m.config.Git.Gitea.URL,
            m.config.Git.Gitea.Owner,
            m.config.Git.Gitea.Repo,
        )
    }
    return m.config.Git.RepoURL
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

#### 4.1 agentctl sync

```go
// cmd/agentctl/sync.go

func runSync(cmd *cobra.Command, args []string) error {
    cfg, err := config.Load()
    if err != nil {
        return err
    }

    if !cfg.Git.OfflineMode {
        return fmt.Errorf("sync only needed in offline mode")
    }

    syncer := git.NewSyncer(cfg)

    // Step 1: Push all refs from Gitea to GitHub
    fmt.Println("Pushing branches to GitHub...")
    if err := syncer.PushAllBranches(ctx); err != nil {
        return fmt.Errorf("push branches: %w", err)
    }

    // Step 2: Push main branch
    fmt.Println("Pushing main branch...")
    if err := syncer.PushMain(ctx); err != nil {
        return fmt.Errorf("push main: %w", err)
    }

    // Step 3: Update mirror from GitHub
    fmt.Println("Updating mirror from GitHub...")
    if err := syncer.UpdateMirrorFromGitHub(ctx); err != nil {
        return fmt.Errorf("update mirror: %w", err)
    }

    // Step 4: Create retrospective PRs (optional)
    if createPRs {
        fmt.Println("Creating retrospective PRs...")
        if err := syncer.CreateRetrospectivePRs(ctx); err != nil {
            return fmt.Errorf("create PRs: %w", err)
        }
    }

    fmt.Println("Sync complete!")
    return nil
}
```

#### 4.2 Syncer Implementation

```go
// pkg/git/syncer.go

type Syncer struct {
    cfg         *config.Config
    gitea       *GiteaClient
    github      *GitHubClient
    mirrorPath  string
}

func (s *Syncer) PushAllBranches(ctx context.Context) error {
    // Clone Gitea repo to temp dir
    tmpDir, err := os.MkdirTemp("", "maestro-sync-*")
    if err != nil {
        return err
    }
    defer os.RemoveAll(tmpDir)

    // Clone from Gitea
    giteaURL := s.gitea.GetRepoURL()
    if err := git.Clone(ctx, giteaURL, tmpDir); err != nil {
        return err
    }

    // Add GitHub as remote
    githubURL := s.cfg.Git.RepoURL
    if err := git.Run(ctx, tmpDir, "remote", "add", "github", githubURL); err != nil {
        return err
    }

    // Push all branches
    return git.Run(ctx, tmpDir, "push", "github", "--all")
}

func (s *Syncer) CreateRetrospectivePRs(ctx context.Context) error {
    // List merged branches in Gitea that don't have corresponding PRs on GitHub
    // Create PRs with body explaining they were merged offline
    // This is optional - mainly for audit trail
}
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
$ agentctl sync --to-github

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
3. **GiteaClient**: Implements GitClient interface for PR operations
4. **Mode-aware validation**: Skip irrelevant checks based on mode
5. **Mirror upstream switching**: `GetFetchURL()` returns Gitea in airplane mode
6. **Model resolution**: Config overrides + preferred list fallback
7. **Graceful failure**: Clear guidance when requirements aren't met
8. **`default_mode` config**: Persistent mode preference
9. **SQLite PR persistence**: Workflow identity survives restarts
10. **Basic sync**: Simple `agentctl sync` that pushes to GitHub

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

1. **Port collisions**: Multiple concurrent Maestro instances may collide on:
   - WebUI port (default: 8080)
   - Gitea port (default: 3000)
   - Workaround: Run one project at a time, or manually configure different ports

2. **Model download requires connectivity**: Ollama models must be pre-downloaded
   - Cannot pull models while truly offline
   - Workaround: Run `ollama pull <model>` before going offline

3. **No CI parity**: Local testing doesn't replicate GitHub Actions
   - Tests run in container, but no workflow simulation
   - Workaround: Test thoroughly before syncing to GitHub

4. **Single project per Gitea**: MVP uses a single Gitea instance per project
   - No multi-project support in shared Gitea
   - Workaround: Each project gets its own Gitea container

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

## Appendix: Gitea CLI (tea)

```bash
# Installation
go install code.gitea.io/tea@latest

# Configuration
tea login add --name local --url http://localhost:3000 --token $GITEA_TOKEN

# Common commands (mirror gh CLI)
tea pr create --title "Story 001" --head feature --base main
tea pr list
tea pr view 1
tea pr merge 1 --style squash --delete
tea pr close 1

# API fallback (when tea isn't available)
curl -X POST "http://localhost:3000/api/v1/repos/org/repo/pulls" \
  -H "Authorization: token $GITEA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Story 001","head":"feature","base":"main"}'
```

## References

- [Gitea Documentation](https://docs.gitea.io/)
- [Gitea API Reference](https://docs.gitea.io/en-us/api-usage/)
- [Tea CLI Documentation](https://gitea.com/gitea/tea)
- [Ollama Integration](./OLLAMA.md) - Local LLM support
- [Git Workflow](./GIT.md) - Current git architecture
