# Benchmark Evaluation Tracker

This document tracks external coding benchmarks we're evaluating for measuring Maestro's effectiveness as a multi-agent coding orchestrator.

**Key context**: There is currently no benchmark specifically designed for multi-agent coding orchestrators. All benchmarks below were designed for single-agent evaluation. Part of our value proposition is demonstrating that orchestration improves results on tasks where single agents struggle — particularly at larger scale.

**Cross-cutting concerns**:
- Maestro requires Docker for agent execution. Benchmarks that also use Docker for sandboxing may require docker-in-docker or architectural changes.
- Maestro's current workflow assumes a PM/human provides specs and answers clarifying questions. Benchmarks require autonomous "zero-shot" execution with no human in the loop.
- Benchmarks expect a specific input/output contract (typically: receive task description → produce patch/commits). Maestro needs an adapter layer to translate between benchmark formats and its internal story-driven protocol.

---

## 1. SWE-EVO (Primary Target)

| Field | Detail |
|-------|--------|
| **What it tests** | Long-horizon software evolution — implement changes from release notes spanning multiple PRs to evolve a codebase to a new version |
| **Scale** | Avg 21 files modified per task, ~15 PRs per unresolved task, 874 tests per instance |
| **Task count** | 48 tasks from 7 mature Python projects |
| **Languages** | Python only |
| **Best score** | 21% (GPT-5) |
| **Published** | December 2025 |
| **Repo** | https://github.com/SWE-EVO/SWE-EVO |
| **Why it fits** | Closest to Maestro's workflow: interpret high-level requirements → plan coordinated multi-file changes → iterate. The multi-PR nature maps directly to story decomposition. |

### Status: DEEP EVALUATION COMPLETE

### Technical Details

**Repository**: `github.com/FSoft-AI4Code/SWE-EVO` (fork of SWE-bench). Installs as the `swebench` Python package.

**Repo structure**:
```
SWE-EVO/
  OpenHands/           # Vendored fork of OpenHands agent framework
  SWE-agent/           # Vendored fork of SWE-agent framework
  SWE-bench/           # Modified SWE-bench harness (evaluate_instance.py, etc.)
  _release_note/       # Release notes by project
  hf_out/hf_dataset/   # Pre-built HuggingFace dataset (Arrow format)
  run.sh               # Example evaluation runner (has hardcoded research paths)
```

**Task input format**: Each instance provides:
1. `problem_statement` — release note text describing changes between two versions (mean 2,390 words, max 22,344)
2. A repo checked out at `base_commit` (the `start_version` tag)
3. Two settings: release-note-only, or release-note + referenced PR/issue content

**Task output format**: Agent produces a **unified diff patch** (`git diff` format). That's it — no commits, no full repo state.
- OpenHands format: `output.jsonl` with `test_result.git_patch` per instance
- SWE-agent format: `preds.json` mapping `instance_id` to `{model_patch: "..."}`

**Evaluation flow**:
1. Pulls pre-built Docker image per instance (hosted on Docker Hub as `xingyaoww/sweb.eval.x86_64.*`)
2. Applies agent's patch + test patch inside container
3. Runs test commands (e.g., `pytest --continue-on-collection-errors -n0 -rA`)
4. Scores: **Resolved Rate** (binary: all FAIL_TO_PASS pass AND no PASS_TO_PASS regressions) and **Fix Rate** (partial credit)

**Docker story**: Docker is **required for evaluation only**. Each of the 48 instances has a pre-built Docker image. The agent inference itself does NOT need to run in Docker — only the test evaluation does. **No docker-in-docker needed.** This is good news for Maestro.

**Possible integration approach**: Maestro's coders work in Docker containers. SWE-EVO evaluation runs in separate Docker containers. These don't overlap — Maestro produces patches, evaluation consumes them independently.

**The 7 repos** (all Python): conan-io/conan, dask/dask, iterative/dvc, pandas-dev/pandas, psf/requests, pydantic/pydantic, scikit-learn/scikit-learn

**Per-task statistics**:
- Mean files edited: 20.9 (max 105)
- Mean lines edited: 610.5 (max 4,113)
- Mean functions edited: 51.0 (max 379)
- Mean FAIL_TO_PASS tests: 81.4 per instance
- Mean PASS_TO_PASS tests: 793 per instance

**Infrastructure requirements**:
- x86_64 architecture recommended (ARM/M-series works with `--namespace ''` flag, builds locally)
- 16GB+ RAM, 120GB+ disk
- No GPU required
- Evaluation timeout: 1800s (30 min) per instance
- Max ~24 concurrent Docker containers recommended (network pool limits)
- Full run: several hours with parallelization

**Known issues**:
- Research-grade code (hardcoded paths in `run.sh`, fragile version matching)
- Only 2 scaffolds supported out of the box (OpenHands, SWE-agent) — custom agents need adapter code
- Docker images hosted by third party (`xingyaoww/*`) — may become unavailable
- macOS ARM needs local image builds (slower)
- Only 48 instances — small dataset, limited statistical significance
- Some test suites have flaky tests

### Maestro Integration Plan

**What we need to build**:

#### 1. Benchmark Runner (`cmd/benchmark/` or `pkg/benchmark/`)
A harness that:
- Loads SWE-EVO instances from HuggingFace dataset or local JSON
- For each instance: checks out the repo at `base_commit`, feeds `problem_statement` to Maestro
- Captures all code changes as a unified diff patch
- Writes output in SWE-agent JSON format (simpler than OpenHands JSONL)
- Orchestrates sequential or parallel runs across instances

#### 2. Zero-Shot Execution Mode
- [ ] New config flag (e.g., `autonomous_mode: true`) that modifies the workflow:
  - **Skip PM bootstrap entirely** — task comes pre-formed from the benchmark adapter
  - **Architect auto-resolves ambiguities** — instead of ESCALATED state, architect makes a judgment call and continues
  - **Disable escalation timeouts** — or set to 0 to auto-proceed
  - **Single-story mode** — one release note = one story (or let architect decompose into sub-stories)
- [ ] Consider whether architect should decompose release notes into multiple stories or treat each as one unit

#### 3. Input Adapter (Release Notes → Stories)
- [ ] Parse SWE-EVO instance JSON into Maestro story format
- [ ] Include `problem_statement` as story content
- [ ] Set repo URL and `base_commit` for workspace setup
- [ ] Optionally: let the architect decompose large release notes into sub-stories (this is where multi-agent orchestration adds value over single-agent)

#### 4. Output Collector (Workspace → Patch)
- [ ] After Maestro completes, run `git diff base_commit..HEAD` in the workspace to produce a unified patch
- [ ] Write to SWE-agent-compatible JSON format
- [ ] Handle edge cases: no changes, partial changes, multiple coder workspaces

#### 5. Container Strategy (RESOLVED — No Conflict)
Docker-in-Docker is **NOT needed**. The separation is clean:
- Maestro coders work in their own Docker containers during implementation
- SWE-EVO evaluation runs in separate Docker containers after Maestro finishes
- These are independent — Maestro produces a patch file, evaluation consumes it

The one consideration: Maestro's coder containers need the target Python project's dependencies installed. We'll need Dockerfiles (or a base image) for each of the 7 Python repos. The SWE-EVO evaluation images (`xingyaoww/sweb.eval.*`) could potentially be reused as Maestro's target containers, which would ensure environment parity.

#### 6. macOS vs Linux
- Development/testing on macOS (ARM) is feasible but slower (local image builds)
- Production benchmark runs should target x86_64 Linux for image compatibility and performance
- Consider a CI/cloud setup for official benchmark runs

---

## 2. FeatureBench

| Field | Detail |
|-------|--------|
| **What it tests** | End-to-end feature development (not bug fixing) |
| **Scale** | Multi-file, multi-commit feature implementations |
| **Task count** | 200 tasks from 24 open-source repos |
| **Languages** | Multiple |
| **Best score** | ~11% (Claude Opus 4.5, down from 74% on SWE-bench) |
| **Published** | ICLR 2026 |
| **Repo** | https://github.com/LiberCoders/FeatureBench |
| **Why it fits** | Feature implementation decomposes naturally into planning/coding/testing — Maestro's exact workflow. The massive single-agent score drop suggests orchestration could add value. |

### Status: NOT STARTED

### Maestro gaps identified (preliminary)
- [ ] **Zero-shot execution mode** (same as SWE-EVO)
- [ ] **Docker compatibility**: Supports Claude Code, Codex, Gemini CLI — need to check execution model
- [ ] **Benchmark adapter**: Feature request → Maestro story translation
- [ ] **Multi-language container support**: 24 repos across multiple languages

---

## 3. SWE-bench Pro

| Field | Detail |
|-------|--------|
| **What it tests** | Issue resolution across multi-file, multi-language codebases |
| **Scale** | Avg 4.1 files, 107 lines changed per task |
| **Task count** | 1,865 instances from 41 repos (+ 276 private) |
| **Languages** | Python, Go, TypeScript, JavaScript |
| **Best score** | ~46% standardized, ~57% custom agents |
| **Published** | 2025 (Scale AI) |
| **Repo/Leaderboard** | https://labs.scale.com/leaderboard/swe_bench_pro_public |
| **Why it fits** | Recognized baseline benchmark. Multi-language, contamination-resistant. Good for credibility even though task granularity is smaller than ideal. |

### Status: NOT STARTED

### Maestro gaps identified (preliminary)
- [ ] **Zero-shot execution mode** (same as above)
- [ ] **Issue → story translation**: GitHub issues are different from specs/release notes
- [ ] **Standardized scaffolding compliance**: May need to match their harness requirements for leaderboard submission
- [ ] **Go/TS/JS container toolchain support**

---

## 4. Commit0

| Field | Detail |
|-------|--------|
| **What it tests** | Library generation from scratch — given a spec and test suite, implement an entire library |
| **Scale** | Full library implementation |
| **Task count** | 54 core Python libraries |
| **Languages** | Python only |
| **Best score** | No model can fully reproduce a library yet |
| **Published** | December 2024 |
| **Repo** | https://commit-0.github.io/ |
| **Why it fits** | Greenfield spec → code pipeline maps directly to Maestro's spec parsing → story generation → implementation workflow. |

### Status: NOT STARTED

### Maestro gaps identified (preliminary)
- [ ] **Greenfield project initialization**: Maestro currently assumes an existing repo to clone
- [ ] **Spec format adapter**: Library specs → Maestro spec format
- [ ] **Zero-shot mode** (same as above)
- [ ] **Test suite integration**: Need to run Commit0's test suites within Maestro's container model

---

## 5. ProjDevBench

| Field | Detail |
|-------|--------|
| **What it tests** | End-to-end project construction from high-level requirements (no starter codebase) |
| **Scale** | Full project creation including architecture design |
| **Task count** | 20 tasks across 8 categories |
| **Languages** | Multiple |
| **Best score** | 27.38% overall acceptance |
| **Published** | February 2026 |
| **Repo** | (arXiv: 2602.01655) |
| **Why it fits** | Tests the full "from requirements to working project" pipeline. Small task set but directly relevant. |

### Status: NOT STARTED

### Maestro gaps identified (preliminary)
- [ ] **Greenfield project support** (same as Commit0)
- [ ] **Requirements → spec translation**: High-level requirements format differs from Maestro specs
- [ ] **Zero-shot mode** (same as above)
- [ ] **LLM-assisted evaluation integration**: Uses LLM-based code review as part of scoring

---

## Cross-Cutting Feature Requirements

These Maestro changes would benefit multiple benchmarks:

### 1. Zero-Shot Execution Mode (`maestro --zeroshot <specfile>`)
**Needed by**: All 5 benchmarks
**Description**: A Maestro product feature (not benchmark-specific) for fully autonomous execution. All agents stay in the loop but never block on human input:
- **PM**: Receives spec file directly, validates, submits to architect. Never enters AWAIT_USER — auto-resolves questions using own judgment.
- **Architect**: Auto-resolves escalations instead of waiting for human. Full iterative review preserved.
- **Coders**: Auto-resolve any escalations. Normal coding/testing flow.
- See `docs/specs/SWE_EVO_PLAN.md` Phase 1 for detailed design.

### 2. Benchmark Adapter Framework
**Needed by**: All 5 benchmarks
**Description**: Translation layer between benchmark-specific formats and Maestro's internal protocol.
- **Input adapters**: Convert benchmark task descriptions (release notes, issues, feature requests, specs) into Maestro story format
- **Output collectors**: Capture Maestro's changes (commits, patches, modified files) in the format each benchmark expects
- **Evaluation hooks**: Trigger benchmark-specific test suites against Maestro's output

### 3. Docker-in-Docker or Alternative Containerization Strategy
**Needed by**: Potentially SWE-bench Pro, FeatureBench (TBD per benchmark)
**Description**: If benchmarks require their own Docker containers for sandboxing, and Maestro also uses Docker, we need a strategy:
- Option A: Docker-in-Docker (complex, performance overhead)
- Option B: Run Maestro outside Docker, only use Docker for benchmark sandboxing
- Option C: Shared Docker daemon with namespace isolation
- **SWE-EVO**: RESOLVED — no conflict. Evaluation Docker is independent from Maestro's agent Docker.
- **Others**: Needs investigation per benchmark

### 4. Greenfield Project Support
**Needed by**: Commit0, ProjDevBench
**Description**: Maestro currently assumes cloning an existing repository. For from-scratch benchmarks, need to support initializing an empty project and building it up.

---

## Evaluation Priority & Roadmap

1. **SWE-EVO** — Deep evaluation complete. Best fit for Maestro's architecture. No Docker-in-Docker needed.
2. **FeatureBench** — Second priority. ICLR 2026 gives it credibility and the score gap is compelling.
3. **SWE-bench Pro** — Third. Important for baseline credibility on recognized leaderboard.
4. **Commit0** — Fourth. Interesting for greenfield showcase but needs more Maestro changes.
5. **ProjDevBench** — Fifth. Small task set, very new, but worth monitoring.

---

## Language Pack Coverage

Maestro has first-class language packs for **Go**, **Python**, **Node.js** (covers JS/TS), and a **Generic** fallback. Packs are JSON files in `pkg/templates/packs/` — adding a new one requires only a JSON file (no code changes). Each pack provides: build/test/lint/run commands, recommended Docker base image, tooling metadata, and template sections.

### Per-Benchmark Language Requirements

| Benchmark | Languages Needed | Pack Status |
|-----------|-----------------|-------------|
| **SWE-EVO** | Python | **Ready** — `python.json` exists |
| **FeatureBench** | Multiple (TBD — 24 repos) | **Partial** — need to audit which languages the 24 repos use. Python/JS/TS covered, others may need new packs |
| **SWE-bench Pro** | Python, Go, TypeScript, JavaScript | **Ready** — all 4 languages covered by existing packs (python, go, node) |
| **Commit0** | Python | **Ready** — `python.json` exists |
| **ProjDevBench** | Multiple (TBD — 8 categories) | **Unknown** — need to audit language distribution |

### Potential New Packs Needed
- **Rust** — if FeatureBench or ProjDevBench include Rust repos
- **Java/Kotlin** — if any benchmark includes JVM projects
- **Ruby** — possible in FeatureBench's 24 repos
- **C/C++** — possible but unlikely for these benchmarks

Adding a pack is ~30 lines of JSON + optionally adding platform aliases in `packs.go`.

---

## Notes

- No benchmark exists for multi-agent orchestration specifically. Publishing our methodology and results could be a novel contribution.
- All top benchmarks are Python-heavy. Multi-language support (Go, TS, JS) is only tested by SWE-bench Pro.
- Contamination is a real concern — SWE-bench Verified is effectively deprecated due to training data leakage. Prefer benchmarks with contamination resistance (Pro, Live, SWE-EVO).
- Language packs are a non-blocker for SWE-EVO (Python only) and SWE-bench Pro (all languages already covered).
