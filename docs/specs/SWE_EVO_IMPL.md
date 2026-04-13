# SWE-EVO Benchmark Integration — Implementation Plan

## Context

Maestro needs a repeatable benchmark path against SWE-EVO (48 Python tasks from 7 repos). The design spec is at `docs/specs/SWE_EVO_PLAN.md`. This plan implements Stage 1: an external runner harness with a local Gitea forge, plus three Maestro-side prerequisites.

The work splits into **Track A** (Maestro changes — 3 items) and **Track B** (benchmark runner — 8 items). A3 is the largest item because forge selection is currently hardcoded to operating mode across 6+ call sites.

After approval, this plan should be saved as `docs/specs/SWE_EVO_IMPL.md` for tracking.

---

## Track A: Maestro-Side Changes

### A1. Ensure mirror exists before spec injection

**Problem**: `--spec-file` injection at `flows.go:146` races against PM mirror creation (async in PM SETUP state). Coders can't clone, architect falls back to placeholder workspace (`workspace/architect.go:70`).

**Fix**: Synchronous `mirror.EnsureMirror()` in `OrchestratorFlow.Run()` between lines 143-146. **Fail fast** on any error — config error, mirror creation failure, or missing `git.repo_url`.

**File**: `cmd/maestro/flows.go`

```go
// After runStartupOrchestration (line 143), before spec injection (line 146):
if f.specFile != "" {
    cfg, cfgErr := config.GetConfig()
    if cfgErr != nil {
        return fmt.Errorf("config required before spec injection: %w", cfgErr)
    }
    if cfg.Git != nil && cfg.Git.RepoURL != "" {
        mirrorMgr := mirror.NewManager(k.ProjectDir())
        if _, mirrorErr := mirrorMgr.EnsureMirror(ctx); mirrorErr != nil {
            return fmt.Errorf("mirror creation required before spec injection: %w", mirrorErr)
        }
        k.Logger.Info("Mirror ready before spec injection")
    }
}
```

**Existing function**: `mirror.NewManager(projectDir).EnsureMirror(ctx)` at `pkg/mirror/manager.go:243`.

---

### A2. Thread `project.primary_platform` into architect prompts

**Problem**: Architect spec analysis template (`spec_analysis.tpl.md:34`) infers platform from spec text. Unreliable for raw benchmark inputs. `project.primary_platform` is in config but never passed to these templates.

**Files**:
- `pkg/architect/request_spec.go` — lines 70-87 (spec review Extra map) and 228-232 (story gen Extra map)
- `pkg/templates/architect/spec_analysis.tpl.md` — line 34
- `pkg/templates/architect/spec_review.tpl.md` — line 66

**Changes**:
1. In `request_spec.go`, read platform from config and add `"primary_platform"` to the Extra map at both template data construction sites (lines 71 and 231):
```go
cfg, _ := config.GetConfig()
platform := ""
if cfg.Project != nil && cfg.Project.PrimaryPlatform != "" {
    platform = cfg.Project.PrimaryPlatform
}
// Add to Extra map: "primary_platform": platform
```

2. In `spec_analysis.tpl.md`, before the CRITICAL block (line 34):
```
{{if .Extra.primary_platform}}
**PROJECT PLATFORM**: This project's configured platform is **{{.Extra.primary_platform}}**. Use this as the baseline unless the specification explicitly declares a different one.
{{end}}
```

3. Update the CRITICAL instruction at line 34 to reference configured platform as fallback.

4. In `spec_review.tpl.md`, add platform context near the "Platform Consistency" check (line 66).

---

### A3. Config-driven forge provider

**Problem**: Forge selection is mode-gated (`IsAirplaneMode()`), not config-driven. Six call sites hardcode GitHub for standard mode. A `forge.provider = "gitea"` config value alone won't work because startup, preflight, clone, auth, bootstrap, and mirror code all independently check mode instead of config.

Additionally, `forge_state.json` lookup is inconsistent: some callers pass the project root, others pass agent workspace dirs (`prepare_merge.go:371` passes `c.workDir`, `request_merge.go:236` passes `d.workDir`). The runner-written state file lives at the project root, so these lookups fail.

#### A3a. Central helper + config field

**File**: `pkg/config/config.go`

Add config struct:
```go
type ForgeConfig struct {
    Provider string `json:"provider,omitempty"` // "github", "gitea", or "" (auto)
}
```

Add `Forge *ForgeConfig` field to `Config` struct (~line 730). Default: nil (preserves current behavior).

Add accessor (near line 2371):
```go
// GetForgeProvider returns the configured forge provider.
// Priority: explicit config > airplane mode implies gitea > default github.
func GetForgeProvider() string {
    cfg, err := GetConfig()
    if err == nil && cfg.Forge != nil && cfg.Forge.Provider != "" {
        return cfg.Forge.Provider
    }
    if IsAirplaneMode() {
        return "gitea"
    }
    return "github"
}
```

All forge-related decisions use this single function instead of `IsAirplaneMode()`.

#### A3b. Fix forge_state.json path resolution

**File**: `pkg/forge/state.go` (~line 77)

`LoadState()` should always resolve from the project root, not the caller's workspace dir:
```go
func LoadState(projectDir string) (*State, error) {
    // Always resolve from project root, not agent workspace
    root := config.GetProjectDir()
    if root != "" {
        projectDir = root
    }
    statePath := filepath.Join(projectDir, config.ProjectConfigDir, ForgeStateFile)
    // ... rest unchanged
}
```

This fixes the mismatch where `prepare_merge.go:371` and `request_merge.go:236` pass agent workspace paths but `forge_state.json` lives at the project root.

#### A3c. Update all mode-gated forge decisions

Six sites need `IsAirplaneMode()` replaced with `config.GetForgeProvider()`:

| # | File | Line | Current | Change |
|---|------|------|---------|--------|
| 1 | `pkg/forge/factory.go` | 13 | `config.IsAirplaneMode()` | `config.GetForgeProvider() == "gitea"` |
| 2 | `pkg/preflight/preflight.go` | 54 | `config.IsAirplaneMode()` | `config.GetForgeProvider() == "gitea"` |
| 3 | `pkg/coder/clone.go` | 341 | `config.IsAirplaneMode()` | `config.GetForgeProvider() == "gitea"` |
| 4 | `pkg/coder/setup.go` | ~298 | `setupGitHubAuthentication` requires `GITHUB_TOKEN` | Skip `GITHUB_TOKEN` check when `GetForgeProvider() == "gitea"` |
| 5 | `pkg/pm/bootstrap.go` | 340 | `strings.Contains(repoURL, "github.com/")` | Accept any HTTPS URL when provider is "gitea"; keep GitHub validation when provider is "github" |
| 6 | `pkg/mirror/manager.go` | 41 | `config.IsAirplaneMode()` | `config.GetForgeProvider() == "gitea"` |

**Sites that auto-fix** once the core sites are updated (no direct changes needed):
- `pkg/coder/prepare_merge.go:371` — calls `forge.NewClient()` → fixed by #1
- `pkg/architect/request_merge.go:236` — calls `forge.NewClient()` → fixed by #1
- `pkg/webui/setup.go` — calls preflight → fixed by #2
- `pkg/preflight/apikeys.go` — called by preflight → fixed by #2
- `pkg/coder/prepare_merge.go:304-312` (`getPushRemote`) — already prefers "forge" remote when it exists, no change needed
- `pkg/coder/prepare_merge.go:317-327` (`validatePushCredentials`) — already returns nil for "forge" remote, no change needed

**Backward compatibility**: When `forge.provider` is unset (nil/empty), `GetForgeProvider()` falls back to mode-based logic. Airplane mode still implies Gitea. Standard mode without explicit config still implies GitHub. Zero behavior change for existing users.

---

## Track B: Benchmark Runner

New binary at `cmd/benchmark/` with supporting packages under `pkg/benchmark/`.

### Package structure
```
cmd/benchmark/
    main.go                 # CLI entry point, flags, serial instance loop
pkg/benchmark/
    instance.go             # SWE-EVO dataset loading from JSON
    gitea.go                # Thin wrapper around pkg/forge/gitea for benchmark lifecycle
    config.go               # Benchmark Maestro config generation
    poller.go               # SQLite DB completion polling
    patch.go                # Patch collection from forge repo
    output.go               # preds.json writer + artifact archiving
    runner.go               # Per-instance orchestration
```

---

### B1. Instance loader (`pkg/benchmark/instance.go`)

```go
type Instance struct {
    InstanceID       string `json:"instance_id"`
    Repo             string `json:"repo"`             // e.g. "pandas-dev/pandas"
    BaseCommit       string `json:"base_commit"`
    ProblemStatement string `json:"problem_statement"`
    TestCmd          string `json:"test_cmd"`          // Optional
    EvalImage        string `json:"eval_image"`        // Optional
}
```

- `LoadInstances(path string) ([]Instance, error)` — Parse JSON array, validate required fields
- `FilterInstances(instances []Instance, ids []string) []Instance` — Optional ID filter for pilot

---

### B2. Gitea forge manager (`pkg/benchmark/gitea.go`)

**Reuses existing code** — NOT a reimplementation:
- `pkg/forge/gitea/container.go` — `EnsureContainer()`, `WaitForReady()`
- `pkg/forge/gitea/setup.go` — `Setup()` for admin/token/org
- `pkg/forge/state.go` — `SaveState()` for writing `forge_state.json`

**Thin wrapper**:
```go
type BenchGitea struct {
    ContainerInfo *gitea.ContainerInfo
    Token         string
    BaseURL       string
    ReposDir      string // Local bare clones of upstream repos
}
```

- `EnsureRunning(ctx) error` — `EnsureContainer()` + `WaitForReady()` + `Setup()` (idempotent admin/token/org)
- `CreateAndSeedRepo(ctx, instanceID, repo, baseCommit string) (cloneURL string, err error)`:
  1. Sanitize `instanceID` to valid repo name
  2. Create repo via Gitea API
  3. Clone from local bare cache (`ReposDir`), checkout `baseCommit`
  4. Push to Gitea, tag `benchmark-base`
- `DeleteRepo(ctx, repoName string) error` — API delete
- `WriteForgeState(projectDir, repoName string) error` — `forge.SaveState()` with Gitea credentials

---

### B3. Config generator (`pkg/benchmark/config.go`)

```go
func GenerateConfig(inst Instance, giteaRepoURL, containerImage string) ([]byte, error)
```

**No silent image fallback** — error if no image provided.

Generates JSON with:
```json
{
    "project": { "primary_platform": "python", "pack_name": "python" },
    "git": { "repo_url": "<giteaRepoURL>", "target_branch": "main" },
    "forge": { "provider": "gitea" },
    "maintenance": { "enabled": false },
    "webui": { "enabled": false },
    "agents": { "max_coders": 1 },
    "build": { "build": "true", "lint": "true", "run": "true", "test": "<testCmd or pytest>" },
    "container": { "name": "<containerImage>" }
}
```

---

### B4. DB completion poller (`pkg/benchmark/poller.go`)

```go
type Outcome string
const (
    OutcomeSuccess        Outcome = "success"
    OutcomeTerminalFailure Outcome = "terminal_failure"
    OutcomeStalled        Outcome = "stalled"
    OutcomeTimeout        Outcome = "timeout"
    OutcomeProcessError   Outcome = "process_error"
)
```

**Classification** (evaluated in order):
1. **Success**: all stories for `spec_id` have `status = 'done'`
2. **Terminal failure**: no stories in active states (`new`, `pending`, `dispatched`, `planning`, `coding`) AND at least one `failed`
3. **Stalled**: all non-`done` stories are `on_hold` with `hold_since` older than grace period (5m)
4. **Timeout**: wall clock exceeds limit (60m)
5. In progress: continue polling

Captures `session_id` from first story row. Filters on both `session_id` and `spec_id`.
DB connection: read-only, WAL mode, 5s busy timeout, retry on `SQLITE_BUSY`.

---

### B5. Patch collector (`pkg/benchmark/patch.go`)

```go
func CollectPatch(ctx context.Context, giteaCloneURL, workDir string) (string, error)
```

Clone/fetch from Gitea, `git diff benchmark-base..origin/main`. Always attempt even on failure.

---

### B6. Output writer (`pkg/benchmark/output.go`)

```go
type Result struct {
    InstanceID   string  `json:"instance_id"`
    Outcome      Outcome `json:"outcome"`
    Patch        string  `json:"model_patch"`
    ElapsedSecs  float64 `json:"elapsed_seconds"`
    ArtifactsDir string  `json:"artifacts_dir,omitempty"`
}
```

- `WritePreds(results []Result, path string) error` — `{"instance_id": {"model_patch": "..."}}`
- `WriteFullResults(results []Result, path string) error` — Detailed JSON
- `ArchiveArtifacts(projectDir, archiveDir, instanceID string) error` — DB, logs, config, forge_state

---

### B7. Per-instance runner (`pkg/benchmark/runner.go`)

```go
func RunInstance(ctx context.Context, inst Instance, opts RunOptions) Result
```

**Flow**:
1. Clean any existing `<BaseDir>/<instanceID>/`
2. Create fresh project dir
3. `CreateAndSeedRepo` — ephemeral Gitea repo
4. `WriteForgeState` — write `forge_state.json` for Maestro
5. Resolve container image (inst.EvalImage > opts.ContainerImage > error)
6. `GenerateConfig` — write benchmark.json
7. Write `problem_statement.md` verbatim
8. `docker pull <image>` — pre-pull the resolved image
9. Launch Maestro: `maestro --config benchmark.json --spec-file problem_statement.md --projectdir <dir>`
10. Poll + monitor Maestro process in parallel
11. On any terminal condition: SIGTERM → 30s grace → SIGKILL
12. `CollectPatch` — always attempt
13. `ArchiveArtifacts`
14. `DeleteRepo` — clean up
15. Return Result

---

### B8. CLI entry point (`cmd/benchmark/main.go`)

**Flags**:
```
-dataset      string   Path to SWE-EVO instances JSON (required)
-repos-dir    string   Bare clones of upstream repos (required)
-output       string   preds.json path (default: "preds.json")
-results      string   Full results JSON path (default: "results.json")
-archive-dir  string   Artifact archives (default: "archives/")
-base-dir     string   Per-instance project dirs (default: "runs/")
-maestro-bin  string   Maestro binary path (default: "maestro")
-timeout      duration Per-instance timeout (default: 60m)
-instances    string   Comma-separated IDs to run (empty = all)
-container    string   Default image (required if instances lack eval_image)
```

Gitea auto-managed by runner. Serial execution.

**Main loop**: parse flags → validate → load instances → filter → validate images → ensure Gitea → run each instance → write preds.json + results.json → print summary.

---

## Implementation Order

1. **A3** — Config-driven forge provider (load-bearing for benchmark and non-trivial)
   - A3a: config field + `GetForgeProvider()` helper
   - A3b: forge_state.json path fix
   - A3c: update 6 call sites
2. **A1** — Mirror readiness (small, depends on A3 being sound)
3. **A2** — Platform prompts (small, independent)
4. **B1, B3, B4, B5, B6** — Runner foundation (parallel, no interdependencies)
5. **B2** — Gitea manager wrapper (depends on A3)
6. **B7** — Per-instance runner (integrates B1-B6)
7. **B8** — CLI entry point

---

## Verification

### Track A
- `make build && make test` — no regressions
- A3 tests: factory creates correct client based on `forge.provider` config; `LoadState` resolves from project root regardless of caller path; preflight selects correct provider checks
- A1 test: `--spec-file` with `git.repo_url` — mirror created before dispatch or hard error
- A2 test: rendered template includes platform when set in Extra

### Track B
- Unit tests: instance loader validation, config generator output, poller state classification (all 5 outcomes), output writer format
- Integration: Stage 0 pilot with 1 SWE-EVO instance end-to-end

### Stage 0 pilot prerequisites
- Gitea image: `gitea/gitea:1.25`
- Bare clone of 1 upstream repo (e.g., `psf/requests`)
- 1 SWE-EVO instance exported to JSON with eval image
- Maestro binary: `make build`
