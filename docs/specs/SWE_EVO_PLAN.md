# SWE-EVO Benchmark Integration Plan

## Goal

Run Maestro against SWE-EVO with the smallest product surface change that still gives us a fair, repeatable benchmark path.

For v1, the goal is:

1. Run benchmark instances end to end with a real Maestro subprocess.
2. Produce canonical unified diff patches in a deterministic way.
3. Avoid changing normal Maestro behavior or defaults for non-benchmark usage.

This plan is intentionally optimized for **benchmark readiness**, not for introducing a general-purpose autonomous mode on day one.

## Current Reality

The current codebase matters more than the old plan:

- Maestro already has `--spec-file`, which injects a spec directly to the architect.
- Maestro already has `--config`, which deep-merges a JSON file into the project config and persists the result.
- The main Maestro process does **not** exit when work completes; it stays alive until `SIGINT` or `SIGTERM`.
- The architect already emits an internal `all_stories_complete` notification to PM, but that is an internal agent signal, not a clean per-spec external contract in its current implementation.
- Maintenance mode is enabled by default and can enqueue unrelated follow-up work after the benchmark task finishes.
- WebUI is enabled by default and should be disabled for headless benchmark runs.
- Current config defaults are not benchmark-safe for SWE-EVO Python repos:
  - container defaults point at the bootstrap container
  - build defaults assume `make build`, `make test`, `make lint`, `make run`
- Mirror creation currently happens inside PM setup and coder clone logic, not at startup. If `git.repo_url` is configured and `--spec-file` is used, the first spec injection can race against infrastructure setup.
- Architect story-generation prompts infer platform from spec text rather than reading `project.primary_platform` from config, which is weak for raw benchmark inputs.

The old plan assumed process exit, incomplete zero-shot coverage, and stale config fields. This replacement plan does not.

## Design Decisions

### 1. Use the existing `--spec-file` path for benchmark v1

Benchmark v1 should launch Maestro with the existing spec injection path instead of starting with PM zero-shot work.

Why:

- It is already implemented.
- It avoids broad changes to PM behavior.
- It keeps benchmark work mostly outside the core product path.

This means benchmark v1 is:

- **low-risk**
- **good enough to run real benchmark instances**
- **not yet a guarantee that every human-dependent edge case is autonomously resolved**

That tradeoff is acceptable for the first benchmark-ready version.

### 2. Pass benchmark input through verbatim

The runner should pass the SWE-EVO `problem_statement` through to Maestro without normalization or reformatting.

On the current `--spec-file` path, Maestro reads the file and injects its raw contents as the architect approval payload, and the architect templates already say to extract value from incomplete or poorly formatted specs. If benchmark inputs need extra structure, platform hints, or normalization, that should happen inside Maestro's prompts, not in the harness.

This keeps the harness simple and ensures any input-handling improvements benefit all Maestro users, not just benchmark runs.

### 3. Keep the benchmark runner external

The runner should live outside the normal Maestro runtime as a separate harness.

Responsibilities:

- load SWE-EVO instances
- prepare per-instance repos on the benchmark forge
- generate benchmark-specific Maestro config
- launch Maestro as a subprocess
- detect completion
- collect the output patch
- write benchmark outputs in SWE-bench-compatible format
- clean up ephemeral forge repos after each instance

This keeps benchmark orchestration isolated from normal app usage.

### 4. Use Gitea as the default benchmark forge

The current successful Maestro path merges work through a forge PR+merge API. The architect handles merge requests by calling the forge merge API, marks the story accepted on success, and the coder only reaches `DONE` after receiving that approved merge result. This means every benchmark instance needs a forge-backed repo.

For benchmark runs, the default forge should be a **local Gitea instance** running in Docker.

Why Gitea:

- Lightweight, single-binary, runs in Docker
- Scriptable repo creation/deletion via REST API
- No external rate limits or API dependencies
- Keeps the entire benchmark self-contained
- Free and open source

**Ephemeral repo model**: The runner should create repos inside one persistent Gitea instance and delete them after each instance completes. This bounds live repo count by active worker concurrency, not by the full benchmark dataset size. Even for the full SWE-bench-lite set (~300 instances), only one repo exists at a time under serial execution.

Runner responsibilities for forge management:

1. Start Gitea (or verify it's running) before the benchmark begins.
2. Create an ephemeral repo per instance, seed it at the SWE-EVO `base_commit`.
3. Configure `git.repo_url` to point at the Gitea repo.
4. After patch collection, delete the ephemeral repo.
5. Optionally tear down Gitea after the full benchmark run.

### 5. Do not use process exit as the completion contract

The runner must not assume Maestro exits when a spec completes.

For v1, completion should be based on **database-visible story state**:

- success: all stories for the benchmark `spec_id` have `status = 'done'`
- terminal failure: all stories for the benchmark `spec_id` are terminal and at least one has `status = 'failed'`
- stalled: all non-`done` stories for the benchmark `spec_id` have been `on_hold` for longer than 5 minutes
- timeout: no terminal condition reached before the per-instance timeout (default: 60 minutes)

Once the runner detects success, terminal failure, or stall, it should send `SIGTERM` to Maestro and let Maestro shut down cleanly.

**`on_hold` handling**: Internally, `on_hold` is non-terminal and can be released back to `pending`. However, in benchmark context there is no human to release holds. Rather than inspecting `hold_reason` or tracking progress windows (v2 complexity), v1 uses a simple grace period: if all remaining stories have been `on_hold` for 5 minutes with no state changes, treat it as terminal failure.

**Maintenance story filtering**: With `maintenance.enabled = false`, maintenance stories are not created. Stories are also scoped by `spec_id`, and maintenance stories belong to a separate maintenance spec. So the completion rule does not need a `story_type` filter — just check all stories for the target `spec_id`.

### 6. Treat the PM completion signal as internal, not canonical

The existing architect -> PM `all_stories_complete` notification is useful confirmation that the system already has a completion concept.

We should use its **semantics**, but not couple the runner to that internal message for v1, because:

- it is an internal agent-level signal
- it is not a clean per-spec external contract in its current implementation
- it is not the cleanest observable surface for an external harness

DB polling is the right v1 contract because it is observable, current with the codebase, independent of internal PM messaging, and independent of process lifetime.

If we later want a productized benchmark/autonomous mode, we can expose an explicit completion signal or mark the session `completed`.

### 7. Use `--config` for benchmark profiles

Benchmark runs should not rely on ordinary defaults.

The benchmark control surface should be a single config injection flag:

```bash
maestro --config benchmark.json --spec-file problem_statement.md --projectdir <instance-project-dir>
```

`--config` is already implemented. Its behavior:

- load or create the project config as usual
- deep-merge the provided JSON file into the current config
- validate the merged result
- persist the merged result back to `.maestro/config.json`

This is intentionally a **persistent** apply model, not a one-shot in-memory override. It works naturally with resume/restart, and benchmark runs already use fresh per-instance project directories, so persistence is a feature, not a liability.

For SWE-EVO, the injected benchmark config must override at least:

- `project.primary_platform = "python"`
- `project.pack_name = "python"`
- `git.repo_url = <Gitea repo URL for this instance>`
- `git.target_branch = "main"`
- `maintenance.enabled = false`
- `webui.enabled = false`
- `agents.max_coders = 1`

**Build command overrides**:

- `build.build = "true"` (no-op — Python repos have no build step)
- `build.lint = "true"` (no-op)
- `build.run = "true"` (no-op)
- `build.test = <instance test command>` when available, otherwise `"pytest"`

Empty strings are not a skip signal — config defaulting fills them back to `make *` defaults. Explicit `"true"` is the correct no-op.

**Container override**:

- `container.name = <SWE-EVO evaluation image>` when available, otherwise an explicit Python base image

`container.name` is the only essential field. The coder runtime prefers it over Dockerfile mode, so Maestro will use the image directly. The runner should **pre-pull the image** before launching Maestro, since startup inspects the image locally rather than pulling it.

### 8. Disable maintenance for benchmark runs

Benchmark runs should set `maintenance.enabled = false`.

Reason:

- maintenance is expensive
- maintenance is not part of the benchmark task
- maintenance mutates `main` after spec completion
- maintenance would pollute the submitted diff with unrelated work

This is the one benchmark-specific runtime behavior change that is clearly worth making.

### 9. Canonical output should come from benchmark-specific base -> forge `main`

The benchmark output should be defined against the merged forge state, not local worktrees.

Canonical patch:

`git diff benchmark-base..origin/main`

where `benchmark-base` is a benchmark-specific tag created by the runner immediately after seeding the instance repo at the SWE-EVO base commit.

Why this is valid:

- the current successful path really does merge into the target branch — the architect calls the forge merge API, and the coder only reaches `DONE` after receiving the approved merge result
- it reflects what Maestro actually merged
- it excludes abandoned local work
- it is reproducible and easy to archive
- it matches the benchmark's patch-based output model

Local mirrors and agent workspaces are useful debug artifacts, but they should not be the source of truth for the submitted patch.

### 10. Human-dependent paths are v1 failure modes, not v1 product work

Even on the `--spec-file` path, current Maestro can still hit human-dependent flows:

- architect escalation
- PM clarification requests for prerequisite failures

Benchmark v1 should **not** broaden product behavior to auto-resolve all of these up front.

Instead:

- the runner records them as ordinary timeouts, stalls, or failures if they block progress
- we measure how often they occur in a pilot
- only then decide whether benchmark-specific autonomy work is worth adding

That keeps v1 small and honest.

## Benchmark Runner Flow

Each SWE-EVO instance is a completely separate Maestro project directory and process. V1 executes instances **serially** to avoid API rate limit contention across multiple Maestro processes.

### Per-instance flow

1. Ensure the Gitea forge is running.
2. Create an ephemeral repo on Gitea for the instance.
3. Seed it at the SWE-EVO `base_commit`.
4. Tag that starting point as `benchmark-base`.
5. Create a fresh Maestro project directory for the instance.
6. Generate a benchmark config file for the instance.
7. Write the SWE-EVO `problem_statement` verbatim to a spec file in the project dir.
8. Pre-pull the container image if not already local.
9. Launch Maestro:

```bash
maestro --config benchmark.json --spec-file problem_statement.md --projectdir <instance-project-dir>
```

10. Poll the instance database for completion (see Completion Detection).
11. On success, terminal failure, stall, or timeout, send `SIGTERM` and wait for graceful shutdown.
12. Fetch `origin/main` from the Gitea repo and collect `git diff benchmark-base..origin/main`.
13. Write the result to benchmark output JSON.
14. Archive logs and DB artifacts for debugging.
15. Delete the ephemeral Gitea repo.

## Completion Detection

Because each benchmark instance gets a fresh project directory, the runner can treat the instance database as single-session state.

### Runner behavior

1. Wait for the first stories for the session to appear in `.maestro/maestro.db`.
2. Capture the benchmark `spec_id` from those rows.
3. Poll story state for that `spec_id`.

**Success**: all stories for the target `spec_id` have `status = 'done'`.

**Terminal failure**: all stories for the target `spec_id` are in a terminal state, and at least one has `status = 'failed'`.

**Stalled**: all non-`done` stories for the target `spec_id` have been `on_hold` for longer than 5 minutes with no state changes. This covers architect escalation and other human-blocked paths that will never resolve in benchmark context.

**Timeout**: no terminal condition reached before the per-instance timeout (default: 60 minutes).

**Everything else**: remains in progress; continue polling.

SQLite contention is not a concern. Maestro opens the DB in WAL mode with a 5-second busy timeout, so concurrent reads during writes are expected. The runner should use its own read-only connection and retry `SQLITE_BUSY` / `database is locked` defensively.

## Output Contract

For every instance, the runner should record:

- benchmark instance id
- Maestro run outcome: `success`, `terminal_failure`, `stalled`, `timeout`, or `process_error`
- canonical patch from `benchmark-base..origin/main`
- elapsed wall-clock time
- archived artifacts path

The patch should always be collected, even for failure, stall, or timeout, because partial merged work is still useful for evaluation and debugging.

### SWE-bench-compatible output

The runner should produce a `preds.json` file mapping `instance_id` to `{"model_patch": "..."}` for compatibility with the SWE-bench evaluation harness.

### Retry policy

V1 should not auto-retry crashed instances. Record `process_error` and move on. Retry logic is deferred to v2 if pilot data shows crashes are common enough to warrant it.

## Benchmark Config Profile

The runner-generated config should be benchmark-specific and current with the schema.

### Required fields

- `project.name`
- `project.primary_platform = "python"`
- `project.pack_name = "python"`
- `git.repo_url = <Gitea repo URL>`
- `git.target_branch = "main"`
- `maintenance.enabled = false`
- `webui.enabled = false`
- `agents.max_coders = 1`

### Build overrides

- `build.build = "true"` (no-op)
- `build.lint = "true"` (no-op)
- `build.run = "true"` (no-op)
- `build.test = <instance test command or "pytest">`

### Container overrides

- `container.name = <SWE-EVO evaluation image or Python base image>`
- Runner must pre-pull the image before launching Maestro

### Required policy

- do not leave the run on bootstrap container defaults
- do not leave Python benchmark repos on default `make *` build commands
- do not enable maintenance
- do not rely on WebUI

The injected config is expected to become the persisted project config for that benchmark project directory.

## Required Maestro Changes (Stage 1)

Two small Maestro-side changes are required for benchmark correctness:

### 1. Ensure mirror and workspaces before first spec injection

Mirror creation currently happens inside PM setup and coder clone logic, not at startup. When `git.repo_url` is configured and `--spec-file` is used (bypassing PM), the first spec injection can race against infrastructure setup.

**Change**: if `git.repo_url` is configured, ensure the mirror and coder workspaces exist before injecting `--spec-file`. This should happen in the startup sequence, after config is loaded but before the spec is dispatched.

### 2. Pass `project.primary_platform` into architect prompts

The architect's story-generation prompt currently infers platform from spec text, which is unreliable for raw benchmark inputs (GitHub issue bodies, release notes). The architect should read `project.primary_platform` from config and include it as explicit context in story-generation and spec-analysis prompts.

**Change**: thread `project.primary_platform` into the architect's spec analysis and story generation template context so the architect does not have to guess.

### Contingent: improve raw benchmark-input handling

If Stage 0 pilot runs show the architect misreads release-note-style or GitHub-issue-style inputs (generating irrelevant stories, missing the actual bug, etc.), add targeted prompt improvements to the architect's spec analysis and story generation templates. This is scoped as contingent work — only pursued if pilot data shows it's needed.

## Phased Rollout

### Stage 0: Pilot validation

Run 1-2 SWE-EVO instances and validate:

- the benchmark config overrides are sufficient
- completion polling works
- canonical patch collection works
- the Gitea forge lifecycle works end to end
- human-dependent stalls are rare enough to tolerate initially
- the architect correctly interprets raw benchmark inputs

### Stage 1: Benchmark-ready v1

**Maestro-side** (required):

- ensure mirror/workspaces before first spec injection when `git.repo_url` is configured
- pass `project.primary_platform` into architect prompts

**Maestro-side** (contingent):

- improve raw benchmark-input handling if pilot data shows architect misreads

**Harness-side**:

- external benchmark runner with serial execution
- Gitea-backed ephemeral repos (live repo count bounded by concurrency, not dataset size)
- benchmark config generation with `max_coders: 1`
- DB-based completion detection with `on_hold` grace period
- canonical patch collection from `benchmark-base..origin/main`
- `preds.json` output in SWE-bench-compatible format
- archive/output writing

### Stage 2: Optional autonomy improvements

Only pursue this if pilot data justifies it.

Candidate follow-ups:

- explicit benchmark completion signal
- session status `completed`
- benchmark-specific auto-exit after terminal completion
- benchmark-specific handling for architect escalation
- benchmark-specific handling for PM clarification paths
- parallel instance execution with rate limit coordination
- auto-retry for `process_error` outcomes

These are intentionally deferred.

## Effect On Normal Usage

This design should not impair normal Maestro usage.

Why:

- benchmark logic lives in the external runner
- the benchmark profile is generated only for benchmark runs
- `--config` is an existing persistent apply, not hidden behavior
- maintenance remains enabled by default for normal usage
- WebUI remains enabled by default for normal usage
- PM and architect behavior do not need to change for v1
- the two required Maestro changes (mirror readiness, platform prompting) improve general robustness, not just benchmark behavior

The only benchmark-specific runtime differences are in the applied benchmark config and in how the external runner interprets completion.

## Recommendation Summary

For the first benchmark-capable version, we should:

1. Use `--spec-file`, not PM zero-shot. Pass benchmark input verbatim.
2. Use DB story state, not process exit, as the completion contract.
3. Disable maintenance.
4. Run against a local Gitea forge with ephemeral repos.
5. Collect the canonical patch from `benchmark-base..origin/main`.
6. Use `--config` to persist a benchmark-specific config that overrides container, build, maintenance, WebUI, and agent count defaults.
7. Make two small Maestro-side changes: mirror readiness at startup, and platform context in architect prompts.
8. Run instances serially with `max_coders: 1`.

That is enough to produce a current, runnable, low-risk SWE-EVO benchmark path without changing the normal app experience.
