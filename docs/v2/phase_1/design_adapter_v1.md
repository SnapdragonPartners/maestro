+++
title = "Design: The v1-As-Patched Adapter (Item 4)"
edit_date = "2026-07-16"
status = "draft"
summary = "Design sketch for the adapter-v1 work item: per-run Gitea forge isolation (SWE-EVO salvage), subprocess invocation and DB-poll lifecycle, poll-streamed budget enforcement, honest metric normalization from maestro.db, the templates-manifest prompt hash, and two small engine contract additions."
+++

# Design: The v1-As-Patched Adapter (Item 4)

Status: draft — mini-plan for Phase 1 item 4 (`adapter-v1`), the checkpoint before implementation. Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) (target strategy, adapter contract), the [Phase 1 plan](plan_scope.md), [design_engine.md](design_engine.md), [process_fixtures.md](process_fixtures.md). Prior art: v1's own SWE-EVO harness (`pkg/benchmark`), used strictly as a **pattern and salvage source** — the benchmark module never imports `orchestrator` code.

## The Hard Problem: v1 Merges To Main

v1's factory path merges story PRs into its target repo's default branch. Pointed at a GitHub fixture, a single run would advance fixture `main` — violating the fixture conventions (fixtures are never written by runs; merged history cannot be cleaned without prohibited force-pushes) and permanently accreting closed PRs on the fixture repos.

**Resolution: per-run forge isolation via local Gitea** — the SWE-EVO mechanics, salvaged. The adapter maintains one runner-managed Gitea container; each attempt gets a fresh Gitea repo seeded from the fixture at the story's pinned commit; v1 is configured with `git.repo_url` = that Gitea repo and `forge.provider = gitea` (which v1 supports with cloud LLMs — airplane mode is not involved). v1 does its full PR/merge dance against the throwaway repo; the fixture is never touched by the target at all (the engine's own read-only clone remains the validation workspace). Attempt cleanup deletes the Gitea repo — nothing persists anywhere.

This also resolves reviewer question 4's deferral honestly: Gitea enters not as a second fixture-hosting path but as the *target's sandbox*; fixtures stay pinned GitHub repos.

## Invocation And Lifecycle

Directly from the SWE-EVO recipe, reimplemented in-module:

1. **Prepare**: create a per-run v1 project dir (sibling of the engine workspace, inside the run's workdir); seed the Gitea repo from the fixture pin; generate `config.json` (git repo/target branch, forge gitea, `agents.max_coders: 1`, webui/maintenance off, build/test commands from adapter settings); write the story prompt as the spec file.
2. **Launch**: `maestro --config ... --spec-file ... --projectdir ... --nowebui` as a subprocess under the attempt context (the engine's wall-clock deadline kills it), process-group isolated, stdout/stderr teed to a log file.
3. **Poll**: watch `.maestro/maestro.db` — discover spec/session IDs, then poll `stories` status to terminal (the SWE-EVO poller queries, reimplemented over `modernc.org/sqlite`, the module's one new dependency).
4. **Stop**: graceful signal, bounded wait, then kill.
5. **Import the solution**: fetch the Gitea repo's merged main into the engine workspace as `golden/<run-id>/solution` — a **local** branch the engine then binds, ancestry-checks, and validates as usual.
6. **Cleanup**: delete the Gitea repo, remove the project dir. The engine's fixture-side namespace check passes trivially (the target never touched the fixture).

## Observation And Normalization

From `maestro.db`, declared capabilities only — everything else honestly `unsupported`:

- `tokens_total`, `cost_usd` — summed from story records (v1 persists both).
- `llm_calls` — count of `agent_requests`; `tool_calls` — count of `tool_executions`.
- Candidates to confirm during implementation (declared only if derivable honestly): `review_cycles`, `iterations`. `human_interventions`/`human_attention_seconds`: `not_applicable` in unattended benchmark runs.

**Budget enforcement: `streamed` via DB polling.** The poll loop reports usage deltas through `ReportUsage` as story cost/token records advance, so the engine cancels at the cap with poll-interval latency; wall-clock stays engine-hard. This keeps v1 out of the degraded post-hoc mode.

**Terminal state**: story status terminal + PR merged in the Gitea repo → `TerminalStateReached`.

**Evidence**: `maestro.db`, the v1 log tree, and the launch log — copied to a durable per-run evidence directory *before* cleanup (workspace and project dir are deleted; evidence pointers must outlive them).

## MPH Identity

- **Prompt**: pack label `v1-embedded`; hash = `sha256:` over a deterministic manifest (sorted relative paths + contents) of the target checkout's prompt/template inputs (`pkg/templates` tree), per the plan's Codex resolution — prompt identity moves only when prompt content moves.
- **Model**: from the bundle's model routing, mapped into v1's model config.
- **Target**: adapter settings supply the maestro binary path, the target commit hash, and the templates dir of the matching checkout; binary identity = path + `maestro -version` output.

## Engine Contract Additions (small, both engine-side)

1. **Local solution-branch resolution**: `resolveSolutionCommit` currently only fetches from `origin`; the adapter imports the solution as a local workspace branch, so resolution tries the local ref first, then origin. Same namespace and ancestry rules.
2. **`AttemptSpec.EvidenceDir`**: an engine-provided durable directory (under the results store, keyed by run ID) where adapters deposit evidence files; pointers into it survive cleanup. The stub target gains a scripted use of it.

## The First Config Bundle

`configs/paired-default.toml` lands with this item (the plan assigned it here): the paired-agent default driving `v1-as-patched`, models per current v1 defaults, budget expectations set provisionally and revisited by item 6's instrumented runs.

## Testing

- **Unit**: config generation, prompt-manifest hashing (fixture dir), DB normalization against a canned `maestro.db` (SQLite file in testdata), poll parsing, evidence copying.
- **Hermetic integration**: a **fake-maestro** shell script standing in for the binary — writes a plausible `maestro.db`, pushes a merged result to the Gitea repo, exits — driven through the real engine against a local fixture; plus a Gitea-lifecycle test behind a `docker`-guarded skip (skips cleanly where Docker is absent, runs in CI if available).
- **Real end-to-end runs are item 5**, deliberately: they spend tokens, and the patch discovery loop belongs there. This item ends with the adapter passing hermetic tests and a `runner validate` of the real bundle.

## Explicitly Deferred

The minimal v1 patch set (item 5 — discovered by running, not guessed); instrumented budgets (item 6); the single-agent baseline adapter (item 8); any Gitea use for fixture *hosting* (stays deferred per reviewer question 4).

## Review Questions

1. **Per-run Gitea forge isolation** as the resolution to v1's merge-to-main behavior (fixtures never written by targets; SWE-EVO mechanics salvaged in-module). Concur?
2. **`streamed` enforcement via DB polling** — cancellation latency equals the poll interval (seconds). Acceptable as `streamed`, or should poll-based streaming be a declared sub-mode?
3. The two engine contract additions (local branch resolution, `EvidenceDir`). Concur?
4. **Docker becomes a runner dependency** for v1 targets (Gitea container; v1 itself also needs Docker for its coder containers — so this adds no new requirement in practice). Any objection to the hermetic tests skipping when Docker is absent?
