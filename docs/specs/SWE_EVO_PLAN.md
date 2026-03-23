# SWE-EVO Benchmark Integration Plan

## Goal

Run Maestro against all 48 SWE-EVO instances, capture results as unified diff patches, and evaluate using SWE-EVO's provided harness. Measure whether multi-agent orchestration (architect decomposing release notes into stories, coders implementing in parallel) improves on single-agent baselines.

## Background

SWE-EVO presents release notes describing changes between two versions of a Python project. The agent must evolve the codebase from `start_version` to `end_version`. Output is a unified diff patch. Evaluation runs in Docker containers with pre-built images per instance.

**Current best**: 21% resolved rate (GPT-5 with OpenHands).

---

## Key Design Decision: One Maestro Run Per Instance

Maestro does not support changing repos after launch. Each SWE-EVO instance is a different repo at a different commit. **Each instance runs as a completely independent Maestro session** — fresh config, fresh project directory, fresh Maestro process. This requires **zero changes to Maestro's repo management**.

The benchmark runner is an outer harness that:
1. Configures a Maestro project for the instance (repo URL, base commit, spec file)
2. Launches Maestro as a subprocess
3. Waits for completion or timeout
4. Collects the output diff
5. Shuts down Maestro
6. Repeats for the next instance

This means the only Maestro-internal change is **zero-shot mode** (`--zeroshot <specfile>`):
- PM runs autonomously (never blocks on human input)
- Architect and coder auto-resolve escalations
- Spec injected via file instead of human interview
- Everything else — git workflow, forge integration, architect review, coder execution, container management — works as-is.

## Terminology

To avoid ambiguity, this document uses these terms consistently:

- **Zero-shot mode**: A Maestro product feature (`--zeroshot <specfile>`). Autonomous execution with no human in the loop. Useful for benchmarks and any scenario where you have a spec and want hands-off execution.
- **Benchmark runner**: The outer harness (`cmd/benchmark/`). Manages SWE-EVO instances, launches Maestro subprocesses, collects patches, invokes evaluation. Not part of Maestro core.
- **Benchmark config**: Runner-provided settings (instance data, GitHub repo URL, output paths). Separate from Maestro's `.maestro/config.json`.

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────┐
│                    Benchmark Runner                           │
│  (cmd/benchmark/)  — outer harness, NOT inside Maestro       │
│                                                              │
│  for each instance:                                          │
│    1. Create/reset GitHub repo with base_commit              │
│    2. Generate Maestro project config                        │
│    3. Write spec file (release notes) to project dir         │
│    4. Launch: maestro --zeroshot spec.md --project-dir ...   │
│    5. Wait for completion / timeout                          │
│    6. Validate and collect patch from remote main            │
│    7. Tag, archive logs, shut down                           │
│    8. Append validated result to preds.json                  │
│                                                              │
│  after all instances:                                        │
│    9. Invoke SWE-EVO evaluation harness                      │
└──────────┬───────────────────────────────────┬───────────────┘
           │  (per instance)                   │  (once, at end)
           ▼                                   ▼
┌─────────────────────┐             ┌─────────────────────┐
│  Maestro Process    │             │  SWE-EVO Evaluator  │
│  (--zeroshot mode)  │             │  (evaluate_instance) │
│                     │             │                      │
│  PM (zero-shot):    │             │  - Pulls Docker image│
│   validate spec →   │             │  - Applies patch     │
│   bootstrap detect →│             │  - Runs tests        │
│   submit to arch    │             │  - Scores results    │
│                     │             │                      │
│  Architect:         │             └─────────────────────┘
│   spec → stories    │
│   review → merge    │
│   (full iterative)  │
│                     │
│  Coders (3+hotfix): │
│   branch → code →   │
│   commit → push →   │
│   PR → review →     │
│   squash-merge      │
│                     │
│  Forge: GitHub      │
│  (regular mode)     │
└─────────────────────┘
```

---

## Phase 1: Zero-Shot Execution Mode

Maestro currently requires human input at two points: the PM interviews the user to refine specs, and both PM and architect can escalate to humans when stuck. Zero-shot mode removes the human dependency while preserving the full multi-agent workflow.

### 1.1 CLI Interface

```bash
maestro --zeroshot <specfile>
```

This is the primary entry point. The `--zeroshot` flag:
- Enables zero-shot mode across all agents
- Loads the spec file and injects it into the PM (as if uploaded via WebUI)
- The full PM → Architect → Coder pipeline runs, just with no human in the loop

This is useful beyond benchmarks — any scenario where you have a spec and want Maestro to execute it autonomously.

### 1.2 Design: Keep All Agents, Remove Human Dependency

**Key insight**: The maintenance story flow already shows the pattern we want — architect operates autonomously with full iterative review, no PM involvement needed for dispatch or approval. But rather than skip the PM entirely, we keep it for its heterogeneous model advantage (PM runs a different model than the architect, so PM↔architect Q&A produces better reasoning than either alone).

**Behavior changes in zero-shot mode:**

| Component | Normal Mode | Zero-Shot Mode |
|-----------|-------------|----------------|
| PM agent | Interviews human, refines spec | **Receives spec file directly**, validates, submits to architect. No human interview. |
| PM AWAIT_USER | Blocks on human chat input | **Fatal transition** — PM must never block on human. See §1.3. |
| Architect escalation | Posts to chat, waits 2hr for human | **Auto-resolves** — makes judgment call, continues |
| Coder escalation | Posts to chat, waits for human | **Auto-resolves** — same pattern |
| PM↔Architect Q&A | Normal (architect asks PM questions) | **Normal** — preserved. PM answers using its own judgment. |
| Architect review | Full iterative code review | **Normal** — preserved. This is the value proposition. |
| Coders | 3 coders + hotfix agent (default) | **Normal** — default parallelism preserved. |

### 1.3 PM Zero-Shot Mode

The PM receives the spec file and processes it through its normal flow, but with one critical constraint: **it must never enter AWAIT_USER**.

**Current PM states and zero-shot behavior:**

| State | Normal | Zero-Shot |
|-------|--------|-----------|
| WAITING | Waits for user to start interview | Receives injected spec, transitions to WORKING |
| SETUP | Bootstrap detection | **Normal** — still runs, still useful |
| WORKING | LLM tool loop (interview, spec drafting) | **Normal** — PM validates spec, handles architect Q&A |
| AWAIT_USER | Blocks on human chat | **AUTO_RESOLVE** — see below |
| PREVIEW | User reviews spec in WebUI | **Auto-submit** — skip preview, submit directly to architect |
| AWAIT_ARCHITECT | Blocks on architect response | **Normal** — preserved |

**AUTO_RESOLVE behavior** (replaces AWAIT_USER in zero-shot mode):
When the PM would transition to AWAIT_USER:
1. Log what the PM wanted to ask the human (for debugging/analysis)
2. Inject a system prompt: "You are in autonomous mode. There is no human available. You must use your best judgment based on the spec and available context. Make a reasonable decision and proceed."
3. Transition back to WORKING (not AWAIT_USER)

This is the same pattern as architect self-resolution but applied to the PM. The PM synthesizes its best answer from available context rather than blocking.

**Graceful failure**: If the PM enters a loop (AUTO_RESOLVE → WORKING → would-AWAIT_USER repeatedly), cap at N iterations and transition to ERROR with a descriptive message about what the PM couldn't resolve. This is better than a hard fatal because it preserves logs of what happened.

### 1.4 Architect Zero-Shot Mode

Follows the maintenance story pattern — architect operates autonomously with full review authority.

**Key change**: When the architect would enter ESCALATED state:
```go
if zeroShotMode {
    // Inject: "You must use your best judgment to proceed.
    // There is no human available. Make a reasonable decision and continue."
    // Return to prior state (DISPATCHING, ANSWERING, REVIEWING, etc.)
}
```

States affected:
- **ESCALATED** → auto-resolve with judgment prompt, return to prior state
- **ANSWERING** → architect answers its own questions using workspace context
- All review states (iterative approval, spec review) → **normal operation**, no changes needed

### 1.5 Spec Injection Flow

The existing `--spec-file` CLI flag (`cmd/maestro/main.go:29`) feeds through `flows.InjectSpec()` which sends a REQUEST directly to the architect. For zero-shot mode, we modify this slightly:

```
--zeroshot <specfile>
    ↓
PM receives spec (as if uploaded via WebUI)
    ↓
PM validates, runs bootstrap detection
    ↓
PM submits to architect (auto-skips PREVIEW, never enters AWAIT_USER)
    ↓
Architect processes spec → generates stories → dispatches
    ↓
Normal coder flow (3 coders + hotfix, default config)
```

This preserves the PM's bootstrap detection (ensuring the project has a Dockerfile, Makefile, etc.) which is valuable for benchmark instances where we're working with unfamiliar repos.

---

## Phase 2: Benchmark Runner

### 2.1 New Binary: `cmd/benchmark/main.go`

An **external harness** that launches independent Maestro processes. Not a Maestro plugin — a separate binary that treats Maestro as a subprocess.

```bash
# Run single pilot instance
maestro-bench run \
  --instance "psf__requests_2.31.0_2.32.0" \
  --dataset ./swe-evo/instances.json \
  --output ./results/ \
  --maestro-bin ./bin/maestro

# Run first 3 instances (stage 2)
maestro-bench run \
  --instances "psf__requests_2.31.0_2.32.0,psf__requests_2.32.0_2.32.1,psf__requests_2.32.1_2.32.2" \
  --dataset ./swe-evo/instances.json \
  --output ./results/

# Run all 48 instances (stage 3)
maestro-bench run \
  --dataset ./swe-evo/instances.json \
  --output ./results/ \
  --parallel 4

# Evaluate results
maestro-bench evaluate \
  --results ./results/preds.json \
  --swe-evo-path ./SWE-EVO/
```

### 2.2 Instance Loading

Load from pre-converted JSON (convert from HuggingFace Arrow once, commit to benchmark config). Each instance has:

```go
type SWEEvoInstance struct {
    InstanceID       string   `json:"instance_id"`       // e.g. "conan-io__conan_2.0.14_2.0.15"
    Repo             string   `json:"repo"`              // e.g. "conan-io/conan"
    StartVersion     string   `json:"start_version"`     // e.g. "2.0.14"
    EndVersion       string   `json:"end_version"`       // e.g. "2.0.15"
    BaseCommit       string   `json:"base_commit"`       // SHA to check out
    ProblemStatement string   `json:"problem_statement"` // Release note text
    FailToPass       []string `json:"FAIL_TO_PASS"`      // Tests that must start passing (NOT given to agents)
    PassToPass       []string `json:"PASS_TO_PASS"`      // Tests that must remain passing (NOT given to agents)
    Image            string   `json:"image"`             // Docker eval image
}
```

Note: `FAIL_TO_PASS` and `PASS_TO_PASS` are loaded by the runner for evaluation only. They are **never** passed to Maestro or any agent.

### 2.3 Per-Instance Execution Flow

Each instance is a **fully independent Maestro run**. No state carries between instances.

```
for each instance:

  1. FORGE SETUP (runner)
     - Create GitHub repo for this instance via `gh` CLI
       (e.g., maestro-bench/swe-evo-psf-requests-2.31.0)
     - Clone upstream repo locally, checkout base_commit
     - Push to GitHub as initial `main`
     - Store repo URL for config generation

  2. PROJECT SETUP (runner)
     - Create fresh Maestro project directory:
       benchmark-runs/{instance_id}/
     - Generate .maestro/config.json with:
       - git.repo_url → GitHub repo from step 1
       - git.target_branch → "main"
       - pack → "python" (all SWE-EVO repos are Python)
       - Standard agent config (3 coders + hotfix, default)
     - Write spec file (problem_statement) as release-note.md
     - Tag base_commit as `benchmark-base` in the repo

  3. LAUNCH MAESTRO (runner)
     - exec: `maestro --zeroshot release-note.md --project-dir benchmark-runs/{instance_id}/`
     - Maestro starts in zero-shot mode:
       - PM receives spec file, validates, runs bootstrap detection
       - PM submits spec to architect (auto-skips preview, never blocks on human)
       - Architect decomposes into stories, dispatches to coders
       - 3 coders + hotfix agent (default config)
       - Full iterative architect review on each story
       - Each story: branch → code → commit → push → PR → review → squash-merge
     - Runner monitors Maestro process (stdout/stderr, exit code)
     - Timeout: kill after task_timeout_minutes

  4. WAIT FOR COMPLETION (runner)
     - Maestro exits when:
       a. All stories reach DONE (clean exit)
       b. Timeout reached (runner sends SIGTERM → Maestro graceful shutdown)
       c. Unrecoverable error (non-zero exit)
     - In all cases, the validated diff from `benchmark-base..github/main` is the result

  5. OUTPUT COLLECTION (runner)
     - cd into project dir, fetch latest main from GitHub
     - git diff benchmark-base..main → patch.diff
     - Append to preds.json in SWE-agent format
     - Archive: logs, stories.json, event log, git log

  6. TAG & CLEANUP (runner)
     - Tag final state: `git tag benchmark/swe-evo/{instance_id}/run-{NNN}`
     - Push tag to GitHub
     - Maestro process already exited (or was killed)
     - Docker containers cleaned up by Maestro's shutdown
     - Keep GitHub repo for inspection (delete optionally in full suite)
```

### 2.4 Forge Management Strategy

The forge (GitHub in regular mode, Gitea in airplane mode) is **core Maestro infrastructure**, not a benchmark convenience. The PR workflow — branch creation, push, PR review, squash-merge — is part of what we're testing. Removing it would make the benchmark results non-representative of how Maestro actually works.

Each instance needs a GitHub repo. The runner manages this via `gh` CLI.

**MVP (pilot + stage 2)**: One persistent repo per instance under a GitHub org.
- e.g., `maestro-bench/swe-evo-psf-requests-2.31.0`
- Keep repos after run for PR inspection and debugging
- Create manually or via runner script

**Full suite (stage 3)**: Automated repo creation/teardown.
- Runner creates repo, runs instance, archives results
- Option to keep or delete repos after evaluation
- For parallel execution: create N repos up front, one per parallel slot

**Airplane mode (future)**: Runner spins up local Gitea, creates repos there instead of GitHub. Same Maestro flow, different forge. Tests both modes on same benchmark.

### 2.5 Parallelism Strategy

**Within-instance**: Use Maestro's default configuration — 3 coders + hotfix agent. No artificial limits. This is how Maestro works in production and is what we're benchmarking.

**Cross-instance**: Sequential for stages 1-2. For stage 3 (full suite), multiple Maestro processes with separate project dirs and GitHub repos. Each instance uses ~5 containers (1 architect + 3 coders + 1 hotfix), so Docker network pool (~24 containers) limits to ~4-5 parallel instances.

**No benchmark requirement restricts parallelism.** SWE-EVO evaluates the output patch, not how it was produced. We run Maestro as Maestro works.

### 2.6 GitHub Tagging After Completion

After each benchmark instance completes, the runner tags the final state on `main`:

```
benchmark/swe-evo/{instance_id}/run-{NNN}
```

e.g., `benchmark/swe-evo/psf__requests_2.31.0_2.32.0/run-001`

This is useful beyond benchmarks — tagging spec completions in Maestro generally would let users track what state the repo was in after each piece of work. Tags should be lightweight (no release workflows). The runner pushes tags to GitHub after collecting the diff.

**For Maestro core** (future feature): Tag each spec completion on main with `maestro/spec-{id}/complete`. Needs a config flag to avoid triggering CI/CD workflows (e.g., `git.tag_completions: true`, with a configurable prefix).

---

## Phase 3: Input Adapter (Release Notes → Spec File)

### 3.1 Primary Path: Spec Through PM (Zero-Shot)

The benchmark runner writes the SWE-EVO `problem_statement` as a markdown spec file. Maestro's `--zeroshot` flag injects it into the PM, which validates it, runs bootstrap detection, and submits to the architect for decomposition. This is the standard PM → Architect → Coder pipeline with no human in the loop.

```markdown
# Release Note: {repo} {start_version} → {end_version}

{problem_statement text from SWE-EVO instance}
```

The PM receives this as if the user uploaded a spec file via WebUI. Normal flow from there.

### 3.2 Ablation Paths (Experiment Variants Only)

For the experiment matrix, some variants bypass the PM:

- **No-decompose**: Spec goes to PM → architect, but architect is configured to create a single story instead of decomposing.
- **Single-agent**: Spec content is injected directly as a single story to one coder, bypassing both PM and architect. This requires a separate code path.

These are **not the primary architecture.** They exist only for controlled comparison experiments in Stage 4. Engineers should implement the primary PM path first; ablation paths are deferred.

---

## Phase 4: Output Collector

### 4.1 Git History After Completion

Maestro's git workflow produces this history for a single benchmark task:

```
base_commit (tagged: benchmark-base)
    │
    ├── [squash merge] Story 1: "Add conan lock remove command"
    ├── [squash merge] Story 2: "Fix auth token refresh"
    ├── [squash merge] Story 3: "Update dependency resolver caching"
    │
    └── main HEAD (after all stories merged)
```

Each story: branches from `main` → coder commits → PR → architect review → squash-merge to `main` → branch deleted. After all stories complete, `main` has N squash commits on top of `base_commit`.

### 4.2 Run Terminal States

Every instance run ends in one of these states. The runner **must** classify before collecting output:

| Terminal State | Condition | Enters preds.json? |
|---------------|-----------|-------------------|
| `completed_clean` | Maestro exited 0, all stories DONE | Yes |
| `completed_partial` | Maestro exited 0, some stories DONE, others ERROR/timeout | Yes (partial credit via Fix Rate) |
| `timeout` | Runner killed Maestro after deadline | Yes (whatever merged to main) |
| `error` | Maestro exited non-zero | Yes (whatever merged to main, may be empty) |
| `invalid_workspace` | Local repo state is corrupt, dirty, or base tag missing | **No** — log error, skip instance |
| `invalid_patch` | Patch fails validation (see §4.4) | **No** — log error, skip instance |

All states are recorded in the instance metadata. `preds.json` only contains validated patches.

### 4.3 Canonical Patch Source: Remote Main

**Remote `main` on GitHub is the single source of truth.** The runner fetches from the forge, not from a local workspace, to ensure it captures exactly what was squash-merged through PRs.

```go
// cmd/benchmark/collector.go

func CollectPatch(repoDir string, githubRepo string, baseTag string) (*PatchResult, error) {
    // 1. Fetch latest main from GitHub (canonical source)
    git.Run(repoDir, "fetch", "github", "main")

    // 2. Generate cumulative diff: base_commit → remote main
    result, err := git.Run(repoDir, "diff", baseTag+"..github/main")
    if err != nil {
        return nil, fmt.Errorf("failed to generate diff: %w", err)
    }
    patch := result.Stdout

    // 3. Validate (see §4.4)
    if err := validatePatch(repoDir, baseTag, patch); err != nil {
        return &PatchResult{State: "invalid_patch", Error: err.Error()}, nil
    }

    return &PatchResult{
        State:      determineTerminalState(maestroExitCode, storyStatuses),
        Patch:      patch,
        FinalSHA:   getRemoteMainSHA(repoDir),
    }, nil
}
```

Local workspace state is captured for diagnostics only (git log, uncommitted changes check), not used for the canonical patch.

### 4.4 Patch Validation

Before a patch enters `preds.json`, it must pass two checks:

1. **Applies cleanly to base_commit**: Check out `base_commit` in a temp worktree, apply the patch with `git apply --check`. If it fails, the patch is invalid (workspace corruption, encoding issues, etc.).

2. **Encoding and content safety**: Verify the patch is valid UTF-8 text, contains no binary hunks, and is non-empty (empty patches are valid but scored as 0%).

Patches that fail validation are recorded as `invalid_patch` in metadata but excluded from `preds.json`.

### 4.5 Output Format

SWE-agent format:

```json
{
    "conan-io__conan_2.0.14_2.0.15": {
        "model_patch": "diff --git a/conan/cli/commands/lock.py ...\n..."
    },
    "pandas-dev__pandas_2.1.0_2.1.1": {
        "model_patch": "diff --git a/pandas/core/frame.py ...\n..."
    }
}
```

Written incrementally as instances complete (crash-safe — don't lose results if a later instance fails).

### 4.6 Per-Instance Metadata (Required Schema)

Every instance produces a structured metadata record, regardless of terminal state:

```json
{
    "instance_id": "conan-io__conan_2.0.14_2.0.15",
    "terminal_state": "completed_clean",
    "base_commit": "4614b3abbff15627b3fabdd98bee419721f423ce",
    "final_main_sha": "a1b2c3d4...",
    "maestro_commit_sha": "deadbeef...",
    "benchmark_runner_commit_sha": "cafebabe...",
    "dataset_sha256": "abc123...",
    "zeroshot_config": { /* snapshot of Maestro config used */ },
    "eval_image_digest": "sha256:abc123...",
    "coder_image_digest": "sha256:def456...",
    "timeout_seconds": 7200,
    "timeout_reason": null,
    "wall_clock_seconds": 3842,
    "total_tokens": 847293,
    "estimated_cost_usd": 24.50,
    "stories_generated": 5,
    "stories_completed": 4,
    "stories_failed": 1,
    "stories_merged": 4,
    "review_iterations_total": 12,
    "rebase_conflicts": 0,
    "pm_auto_resolves": 1,
    "architect_auto_resolves": 0,
    "patch_lines_added": 342,
    "patch_lines_removed": 87,
    "patch_files_changed": 18
}
```

### 4.7 Results Directory Structure

```
results/
├── preds.json                              # SWE-agent format (validated patches only)
├── metadata.json                           # Array of per-instance metadata records
├── instances/
│   ├── conan-io__conan_2.0.14_2.0.15/
│   │   ├── patch.diff                      # Raw unified diff
│   │   ├── metadata.json                   # Instance metadata (same as in top-level array)
│   │   ├── stories.json                    # Stories architect generated
│   │   ├── maestro_config.json             # Maestro config snapshot
│   │   ├── maestro_events.jsonl            # Full event log
│   │   └── git_log.txt                     # git log benchmark-base..github/main
│   └── ...
├── summary.json                            # Aggregate: pass/fail counts, total cost, timing
└── runner_config.json                      # Benchmark runner config for reproducibility
```

---

## Phase 5: Evaluation Integration

### 5.1 Running the SWE-EVO Evaluator

After all instances produce patches:

```bash
# Install SWE-EVO
pip install -e ./SWE-EVO/

# Run evaluation
python SWE-EVO/SWE-bench/evaluate_instance.py \
    --trajectories_path ./results/ \
    --max_workers 8 \
    --scaffold maestro  # Need to add this scaffold parser
```

### 5.2 Custom Scaffold Parser

Add a Maestro scaffold to `evaluate_instance.py` (minimal change, ~15 lines):

```python
# In evaluate_instance.py, add to scaffold parsing:
elif scaffold == "maestro":
    with open(os.path.join(trajectories_path, "preds.json")) as f:
        preds = json.load(f)
    for instance_id, data in preds.items():
        patches[instance_id] = data["model_patch"]
```

Or, simpler: output in the existing SWE-agent format and use `--scaffold SWE-agent` directly.

### 5.3 Docker Image Handling

Evaluation images (`xingyaoww/sweb.eval.x86_64.*`) need to be available:
- **x86_64 Linux**: Pull directly from Docker Hub
- **macOS ARM**: Build locally with `--namespace ''` flag (slower, but works)
- Pre-pull all 48 images before starting evaluation to avoid timeout issues

---

## Phase 6: Container Strategy for Coder Execution

### 6.1 Coder Container Requirements

Each of the 7 Python repos needs a working development environment:
- Correct Python version with project dependencies installed
- Build tools (setuptools, pip, etc.)
- Test framework (pytest)
- Project-specific system dependencies
- Maestro development tooling (git, make, etc.)

### 6.2 Approach: Derive from Evaluation Images

**The environment is part of the test.** If the coder container diverges from the evaluation container, we risk false negatives (code works in dev, fails in eval) and wasted time debugging environment issues instead of agent behavior.

**Strategy**: Start from SWE-EVO's per-instance evaluation images (`xingyaoww/sweb.eval.x86_64.*`) and add Maestro's required tooling:

```dockerfile
# Per-instance Dockerfile, generated by benchmark runner
FROM xingyaoww/sweb.eval.x86_64.{instance_image}:latest

# Add Maestro development tooling
RUN apt-get update && apt-get install -y \
    git make curl jq \
    && rm -rf /var/lib/apt/lists/*

# Maestro workspace conventions
WORKDIR /workspace
```

**Trade-offs accepted:**
- 48 derived images (one per instance, not one per repo) — build once, cache
- Evaluation images may lack some dev tools — the thin Dockerfile layer adds them
- x86_64 images on ARM Macs need Rosetta or remote build — acceptable for benchmark use

**Why not Maestro's Python language pack:** The Python pack uses `python:3.12-slim` which may differ from the evaluation image's Python version, system libraries, and pre-installed dependencies. Any environment mismatch creates ambiguity about whether a failure is an agent issue or an environment issue. Starting from the eval image eliminates this class of problems.

**Image digest tracking:** The runner records both the eval image digest and the derived coder image digest in per-instance metadata for reproducibility.

---

## Phase 7: Experiment Design

### 7.1 Configurations to Test

The primary run uses Maestro's default configuration. Variant runs isolate which capabilities contribute to the score.

| Run | PM | Architect | Coders | What it tests |
|-----|-----|-----------|--------|---------------|
| **Default** | Yes (zero-shot) | Full decomposition + review | 3 + hotfix | **Maestro as Maestro works.** Primary result. |
| **No-decompose** | Yes | Single story, full review | 1 | Does decomposition help? Architect still reviews but doesn't break work into stories. |
| **No-review** | Yes | Decomposition, auto-merge | 3 + hotfix | Does iterative review help? Architect decomposes but auto-approves on first attempt. |
| **Single-agent** | No | No | 1 | True single-agent baseline. Coder gets release note directly. Comparison point for published results. |
| **Sequential** | Yes | Full decomposition + review | 1 | Does parallelism help? Same decomposition/review but stories execute one at a time. |

**Rationale**: Each variant removes one Maestro capability to measure its contribution:
- Default vs No-decompose → value of story decomposition
- Default vs No-review → value of architect review
- Default vs Sequential → value of parallel execution
- Default vs Single-agent → total value of orchestration

**Equal-substrate guarantee**: All variants share the same execution substrate unless explicitly varied. Specifically:
- Same container image (derived from SWE-EVO evaluation image)
- Same tool access (file read/write, git, shell, test execution)
- Same iterative edit-and-test loop within each coder
- Same timeout per instance
- Same model family (Claude for coders across all variants)
- The single-agent baseline gets the same coder with the same tools — it just doesn't have PM or architect orchestration on top.

### 7.2 Metrics to Report

**Primary:**
- **Resolved Rate**: Binary pass/fail per instance. The headline number.
- **Fix Rate**: Partial credit — fraction of FAIL_TO_PASS tests passing (with no PASS_TO_PASS regressions).

**Decomposition quality** (not compared to gold PRs — there is no single canonical decomposition):
- Story count per instance
- Dependency depth (max chain length in story dependency graph)
- File overlap between stories (how many files touched by >1 story)
- Rebase/conflict rate (how often parallel stories conflict)
- First-pass merge rate (% of stories approved by architect on first review)

**Efficiency:**
- Wall clock time per instance (total, and broken down by phase)
- Total LLM tokens consumed per instance
- Estimated cost (USD) per instance
- Per-story success rate (fraction of generated stories that reach DONE)

### 7.3 Expected Challenges

1. **Large release notes** (up to 22K words) may exceed context windows or confuse decomposition
2. **Implicit dependencies** between changes described in release notes — architect may not detect them
3. **Test infrastructure** — some repos have complex test setups (custom pytest plugins, fixtures, etc.)
4. **Scale** — avg 21 files / 610 lines per task is a lot of coordinated changes
5. **PM auto-resolve loops** — PM may repeatedly try to ask the human the same question; cap and error handling needed

---

## Implementation Roadmap

### Stage 0: Prerequisites & Setup
- [ ] Clone SWE-EVO repo, install Python dependencies
- [ ] Convert HuggingFace Arrow dataset → JSON for the pilot instance
- [ ] Select pilot instance (recommend: smallest psf/requests version bump)
- [ ] Create GitHub org/repo for pilot (e.g., `maestro-bench/swe-evo-pilot`)
- [ ] Push pilot instance's upstream repo at `base_commit` to GitHub repo
- [ ] Pre-pull the SWE-EVO evaluation Docker image for the pilot instance
- [ ] Build derived coder image from evaluation image (add git, make, Maestro tooling)
- [ ] Verify evaluation harness works standalone (apply gold patch, confirm scoring)

### Stage 1: Single Instance Pilot

**Goal**: One SWE-EVO instance runs end-to-end through Maestro and produces a scoreable patch.

#### Milestone 1A: Zero-Shot Mode in Maestro
- [ ] Add `--zeroshot <specfile>` CLI flag to `cmd/maestro/main.go`
- [ ] PM zero-shot mode:
  - [ ] Receive spec file as if uploaded (reuse existing `UploadSpec` path)
  - [ ] AUTO_RESOLVE: when PM would enter AWAIT_USER, log the question, inject "use your judgment" prompt, return to WORKING
  - [ ] Auto-skip PREVIEW state (submit directly to architect)
  - [ ] Iteration cap on AUTO_RESOLVE (3 attempts → ERROR with descriptive log)
- [ ] Architect zero-shot mode:
  - [ ] When would enter ESCALATED, inject "use your judgment" prompt, return to prior state
  - [ ] Follows maintenance story pattern for autonomous operation
- [ ] Coder zero-shot mode:
  - [ ] Auto-resolve any coder escalations (same pattern)
- [ ] Tests: unit tests with mock LLM verifying zero-shot flow through PM → Architect → Coder

#### Milestone 1B: Benchmark Runner (single instance)
- [ ] Create `cmd/benchmark/main.go` — external harness binary
- [ ] Instance loader (read single instance from JSON)
- [ ] GitHub repo setup (create repo, push base_commit to main via `gh` CLI)
- [ ] Config generator — produce `.maestro/config.json` for the instance
  - repo URL, target branch, default agent config (3 coders + hotfix)
- [ ] Spec file writer (problem_statement → release-note.md)
- [ ] Coder image builder (derive from evaluation image, add Maestro tooling)
- [ ] Maestro subprocess management (launch `--zeroshot`, monitor, timeout, SIGTERM)
- [ ] Output collector with validation:
  - [ ] Fetch remote main (canonical source)
  - [ ] `git diff benchmark-base..github/main` → patch
  - [ ] Validate: patch applies cleanly to base_commit, valid UTF-8, no binary hunks
  - [ ] Classify terminal state (completed_clean / completed_partial / timeout / error / invalid_*)
  - [ ] Write validated patch to `preds.json`, structured metadata to `metadata.json`
- [ ] GitHub tagging (`benchmark/swe-evo/{instance_id}/run-{NNN}`)
- [ ] Per-instance archival (logs, stories, config snapshot, git log)

#### Milestone 1C: Evaluation
- [ ] Run `evaluate_instance.py` against pilot patch (use `--scaffold SWE-agent` format)
- [ ] Parse and display results (resolved rate, fix rate, test details)
- [ ] Debug cycle: inspect what the architect decomposed, what coders produced, what tests failed

#### Milestone 1D: Iterate on Pilot
- [ ] Fix any issues found (container env, test failures, timeout tuning, etc.)
- [ ] Re-run until the harness works cleanly end-to-end (not chasing score, just reliability)
- [ ] Document lessons learned, update this plan

### Stage 2: Three Instances

**Goal**: Verify the harness generalizes beyond one repo/instance. Catch issues with different project structures.

- [ ] Select 3 instances across different repos (e.g., psf/requests, pydantic, conan-io/conan)
- [ ] Create GitHub repos for each
- [ ] Run sequentially, collect all 3 patches
- [ ] Evaluate all 3 together
- [ ] Fix any per-repo issues (dependency installation, test commands, container setup)

### Stage 3: Full Suite (48 instances)

**Goal**: Run all 48 instances and produce a complete benchmark result.

- [ ] Automate GitHub repo creation/teardown for all instances
- [ ] Sequential run of all 48 (no parallelism yet — reliability first)
- [ ] Full evaluation, compare against published baselines
- [ ] Cost accounting (total tokens, time, dollars)

### Stage 4: Experiment Configurations

**Goal**: Demonstrate that orchestration adds value by comparing configurations (see §7.1 for full matrix).

- [ ] Default run (Maestro as Maestro works — primary result)
- [ ] No-decompose variant (single story, full review)
- [ ] No-review variant (decomposition, auto-merge)
- [ ] Sequential variant (decomposition + review, 1 coder)
- [ ] Single-agent baseline (coder only, no PM/architect)
- [ ] Analysis: resolved rate, fix rate, per-variant attribution, cost, timing
- Note: Start with a representative subset (e.g., 10-12 instances across all 7 repos) before running all variants on all 48. Saves cost while debugging attribution effects.

### Stage 5: Optimization & Polish

- [ ] Parallel cross-instance execution
- [ ] Container image caching/reuse across instances sharing a repo
- [ ] Result caching (skip completed instances on re-run)
- [ ] Airplane mode (Gitea) runs for comparison
- [ ] Cloud runner for x86_64 Linux (CI/CD integration)
- [ ] Publishable results and methodology writeup

---

## Resolved Decisions

1. **Test oracle**: **No.** Coders do NOT get FAIL_TO_PASS test lists. They rely on the project's existing test suite via Maestro's normal TESTING state. This matches the standard protocol used by all agents on the SWE-bench leaderboard. Coders use the repo's normal test commands and Maestro's standard testing state; the benchmark runner does not inject benchmark-specific test targets or test lists.

2. **Forge/PRs**: **Required.** Full PR workflow through a forge is non-negotiable — it's core Maestro functionality. MVP uses **regular mode (GitHub)**. Future runs will also test **airplane mode (Gitea)** for comparison.

3. **Architect review**: **Full normal operating mode.** Iterative code review exactly as in production. This is the Maestro value proposition — disabling it would undermine the entire point.

4. **Cost budget**: **No hard cap.** Measured on pilot instance first. Estimated $700-1,500 for all 48 instances at Opus rates. Acceptable.

5. **Rollout strategy**: **Incremental.** Single instance → 3 instances → full 48. Do not expand until the harness is verified working on the prior stage.

6. **Story decomposition**: Use Maestro's default decomposition mechanism. We're testing Maestro as Maestro works.

7. **Parallelism**: Use default config (3 coders + hotfix agent). No artificial limits. No benchmark requirement restricts this.

8. **PM**: Keep the PM in the loop (zero-shot mode, not skipped). Heterogeneous model advantage is worth preserving. PM must never enter AWAIT_USER — auto-resolves or fails gracefully.

9. **GitHub tagging**: Tag final state on main after each instance completes. Lightweight tags, no release workflows.

10. **One Maestro run per instance**: Each SWE-EVO instance is a separate Maestro process with its own project dir and config. No repo-switching needed — zero changes to Maestro's repo management.

## Open Questions

1. **Pilot instance selection**: Leaning toward a psf/requests version bump (smallest repo, simplest changes). Need to inspect the actual instances to pick the best candidate.

2. **PM AUTO_RESOLVE iteration cap**: How many times should the PM auto-resolve before giving up? Need to balance "try harder" vs. "stuck in a loop." Suggest 3 attempts, then ERROR with descriptive log.
