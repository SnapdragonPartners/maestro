+++
title = "Design: The v1-As-Patched Adapter (Item 4)"
edit_date = "2026-07-16"
status = "draft"
summary = "Design sketch for the adapter-v1 work item: per-run Gitea forge isolation with a complete Docker lifecycle, subprocess invocation and DB-poll lifecycle, the usage-surface patch seam that earns streamed enforcement, durable evidence export with consistent WAL snapshots, the audited prompt manifest, canonical model-routing identity, and immutable binary identity."
+++

# Design: The v1-As-Patched Adapter (Item 4)

Status: draft — mini-plan for Phase 1 item 4 (`adapter-v1`), the checkpoint before implementation; revised for Codex rounds 1 (nine P1s) and 2 (six tightenings: the post-hoc-then-flip P-1 sequence with a versioned-surface handshake, the item 5 scope amendment made explicit in [plan_scope.md](plan_scope.md), engine-contributed test-output evidence, the two-sided manifest guard, canonical-JSON model routing, and all-stories terminal semantics). Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) (target strategy, adapter contract), the [Phase 1 plan](plan_scope.md), [design_engine.md](design_engine.md), [process_fixtures.md](process_fixtures.md). Prior art: v1's own SWE-EVO harness (`pkg/benchmark`), used strictly as a **pattern and salvage source** — the benchmark module never imports `orchestrator` code.

## The Hard Problem: v1 Merges To Main

v1's factory path merges story PRs into its target repo's default branch. Pointed at a GitHub fixture, a single run would advance fixture `main` — violating the fixture conventions (fixtures are never written by runs; merged history cannot be cleaned without prohibited force-pushes) and permanently accreting closed PRs on the fixture repos.

**Resolution: per-run forge isolation via local Gitea** — the SWE-EVO mechanics, salvaged. The adapter maintains one runner-managed Gitea container; each attempt gets a fresh Gitea repo seeded from the fixture at the story's pinned commit; v1 is configured with `git.repo_url` = that Gitea repo and `forge.provider = gitea` (which v1 supports with cloud LLMs — airplane mode is not involved). v1 does its full PR/merge dance against the throwaway repo; the fixture is never touched by the target at all. Gitea enters as the *target's sandbox*, not a second fixture-hosting path (reviewer question 4's deferral stands).

## Docker Lifecycle (complete, per Codex round 1)

- **Shared Gitea container + volume**: created on demand by the adapter; **torn down by the runner** — adapters may implement `io.Closer`, and `cmd/runner` closes every closer after the suite, surfacing teardown failures as runner errors (a `--keep-infra` flag preserves the infrastructure for iterative dev runs). The Gitea image is **pinned by digest** (a mutable tag would let two nominally identical runs execute different forge code), and the digest is bound into the harness hash.
- **Per-attempt**: the Gitea repo is deleted; the v1 project dir is removed; and as defense-in-depth after forced termination, the adapter enumerates and force-removes v1's session-scoped coder containers (matched by v1's container naming for the run's project) — **any leftover container, repo, or volume the adapter cannot remove is a cleanup failure, and the engine records the attempt `invalid`.**

## Invocation And Lifecycle

Directly from the SWE-EVO recipe, reimplemented in-module:

1. **Prepare**: per-run v1 project dir (inside the run's workdir); Gitea repo seeded from the fixture pin; generated `config.json` (git repo/target branch, forge gitea, `agents.max_coders: 1`, webui/maintenance off, build/test commands from adapter settings); the story prompt as the spec file.
2. **Launch**: `maestro --config ... --spec-file ... --projectdir ... --nowebui` as a subprocess under the attempt context, process-group isolated, output teed to a log file.
3. **Poll**: watch `.maestro/maestro.db` — discover spec/session IDs, then poll `stories` to terminal status (SWE-EVO poller queries, reimplemented over `modernc.org/sqlite`).
4. **Stop**: graceful signal, bounded wait, then process-group kill.
5. **Export evidence, then import the solution** (below); fetch the Gitea repo's merged main into the engine workspace as `golden/<run-id>/solution` — a **local** branch the engine binds, ancestry-checks, and validates as usual.
6. **Clean up** per the Docker lifecycle above.

## Budget Enforcement: The Usage-Surface Patch Seam

Codex round 1 killed the original claim: `stories.tokens_used`/`cost_usd` are written **only at acceptance** (`handleWorkAccepted`) — polling them is post-hoc, and an aborted attempt persists nothing, losing exactly the failed-attempt costs ADR 0025 requires.

v1's metrics middleware already observes every LLM call through its `Recorder` interface, aggregating **in memory only**. That interface is the patch seam, with a clean two-step sequence (Codex round 2):

- **Item 4 ships post-hoc, unconditionally.** The adapter declares `post-hoc` (the degraded mode ADR 0025 as amended permits), reporting `tokens_total`/`cost_usd` from story aggregates when acceptance was reached and `unavailable` otherwise. No runtime sniffing for a surface that does not exist yet.
- **Item 5 lands patch P-1 and flips the descriptor.** P-1 is a **fan-out recorder**: the durable implementation *wraps* the existing `InternalRecorder` singleton (which `handleWorkAccepted` still queries for story aggregates — replacing it would break them), forwarding every observation to both, and appending one line per LLM call to a **versioned usage log** (a header line carries the surface version; the current `Recorder` signature provides story ID, tokens, cost, success — extending it with agent/model is an intentional interface change made in the patch, not an assumption). The capability handshake has a **pre-run half and a run half** (Codex round 3): P-1 also advertises its usage-surface version through `maestro -version` output, which `Describe` — running before any launch — validates against the version it expects; `Run` then separately validates the emitted log header against the same expectation. A mismatch on either side is a target-identity error, never a silent downgrade. Once P-1 is mandatory in the v1-as-patched build, item 5 flips the adapter's descriptor to `streamed`.
- **With P-1**: the adapter tails the usage log, streams deltas through `ReportUsage` (per-call accrual, satisfying the streamed bar); `llm_calls` becomes a real counter; aborted attempts carry their true accrued cost.

## Observation And Normalization

Declared capabilities only — everything else honestly `unsupported`:

- `tokens_total`, `cost_usd` — usage log (P-1) or story aggregates (pre-patch, acceptance-reached only).
- `llm_calls` — from the usage log only (P-1). **Not** `agent_requests`, which records inter-agent messages, not model invocations (Codex round 1); pre-patch this is `unsupported`.
- `tool_calls` — count of `tool_executions` rows.
- `review_cycles`, `iterations` — declared only if honestly derivable (investigated during implementation).
- `human_interventions`, `human_attention_seconds` — `not_applicable` in unattended benchmark runs.

**Terminal state**: one golden input may fan out into multiple internal v1 stories and PRs. `TerminalStateReached` requires **every** story of the run's spec to be terminal-done and **every** required PR merged; a failed, cancelled, or on-hold terminal row means the state was not reached. Evidence exports all PRs, not just the first.

## Durable Evidence Export (before any teardown)

The stories require `pr`, `diff`, and `test-output` evidence; a Gitea URL dies with the repo, so evidence is **exported to `AttemptSpec.EvidenceDir`** (engine-provided, under the results store, survives cleanup):

- `pr.json` — metadata for **every** PR of the run from the Gitea API: number, title, body, merge commit, timestamps.
- `diff.patch` — `git diff <pin>..<final-merge-commit>` from the seeded repo before deletion.
- `maestro.db` — a **consistent snapshot**: the DB runs in WAL mode, so a raw file copy after a kill can miss the newest frames; the adapter snapshots via SQLite online backup (`VACUUM INTO`) after process termination — the same path on forced stops (opening the DB replays the WAL).
- `usage.jsonl` (with P-1) and the v1 log tree + launch log.

**`test-output` evidence comes from the engine, not the adapter** (Codex round 2): v1's `tool_executions` rows usually carry result strings, not captured stdout/stderr, so extraction from them is unreliable. Instead the engine — whose validator execution is the authoritative test run anyway — writes each validator's captured output to the evidence directory and appends the corresponding `test-output` evidence pointers itself (engine contract addition 3 below). The adapter's DB snapshot still preserves whatever v1 recorded.

## MPH Identity

- **Prompt**: pack label `v1-embedded`; hash = `sha256:` over a deterministic manifest (sorted relative paths + contents) of an **explicit, audited list of prompt-bearing inputs** — `pkg/templates` **plus** the hard-coded prompt surfaces in Go files (architect request prompts, coder todo instructions, and whatever else the implementation audit finds). Two guards, because existence checks alone cannot catch a *newly introduced* prompt source (Codex round 2): every manifest entry must match a file in the target checkout (catches removals/moves), **and** an independent scanner test generates a prompt-source inventory from the tree — the templates directory plus a documented heuristic sweep for prompt-bearing Go literals — whose every candidate must be classified into **exactly one of two reviewed lists**: the prompt manifest (hashed into P) or an explicit **non-prompt allowlist** (not hashed) for scanner false positives (Codex round 3 — dumping false positives into the manifest would make P move when non-prompt code changes). An unclassified candidate fails the test. The heuristics are tuned by the implementation audit and deliberately over-inclusive: a false positive costs an allowlist entry, a false negative costs prompt-identity honesty.
- **Model**: `MPHIdentity.Model` carries the **canonical JSON serialization of the complete routing** (sorted keys: the default plus every role override) — length-delimited and collision-safe, since model and role strings are only validated as nonempty and may contain any delimiter; reviewer heterogeneity is never reduced to the default model. (Representation convention, no schema change; documented on the field.)
- **Binary identity**: `sha256:` digest of the executable bytes plus the `maestro -version` output — immutable, unlike a path. When the version output exposes a commit, the adapter verifies it against the declared target commit and fails Describe on mismatch.

## Engine Contract Additions (small, all engine-side)

1. **Local solution-branch resolution**: `resolveSolutionCommit` tries the local workspace ref first, then origin. Same namespace and ancestry rules.
2. **`AttemptSpec.EvidenceDir`**: engine-provided durable directory under the results store, keyed by run ID; the stub target gains a scripted use.
3. **Engine-contributed validator evidence**: after verification, the engine writes each validator's captured output to the evidence directory and appends `test-output` evidence pointers to the record — the authoritative test run producing the authoritative test evidence, for every target uniformly.

## Dependency Note: `modernc.org/sqlite`

An **adapter-scoped v1-compatibility dependency**: its import is confined to the v1 adapter package, labeled as such in the code and module README. Removal trigger: retiring the v1 adapter or replacing its observation surface. The intent lives in the doc trail, not just grep.

## The First Config Bundle

`configs/paired-default.toml` lands with this item: the paired-agent default driving `v1-as-patched`, models per current v1 defaults, budget expectations provisional until item 6.

## Testing

- **Unit**: config generation, prompt-manifest hashing (including the manifest-completeness test), DB normalization against a canned WAL-mode `maestro.db` in testdata, usage-log tailing, evidence export.
- **Hermetic integration**: a **fake-maestro** script standing in for the binary — writes a plausible `maestro.db` (+ usage log), pushes a merged result to the Gitea repo, exits — driven through the real engine; plus the Gitea lifecycle (container, seed, delete, teardown).
- **CI is required, not skipped**: Docker-dependent tests skip locally when Docker is absent, but under `BENCHMARK_REQUIRE_DOCKER=1` — set in the Linux CI job, which has Docker — they **fail** instead of skipping. A green PR has executed the Gitea lifecycle.
- **Real end-to-end runs are item 5**: they spend tokens, and the patch discovery loop (P-1 first on the list) belongs there. This item ends with hermetic tests green and `runner validate` accepting the real bundle.

## Explicitly Deferred

The v1 patch set (item 5 — P-1 usage surface enumerated above; the rest discovered by running); instrumented budgets (item 6); the single-agent baseline (item 8); Gitea for fixture *hosting* (deferred per reviewer question 4).

## Review Questions — Resolutions

Codex round 1 (2026-07-16); nine P1s incorporated above.

1. Per-run Gitea isolation: **concur**, subject to durable evidence export and the complete Docker lifecycle — both now specified.
2. Poll-based streaming: **only from a per-call accrual source** — hence the P-1 usage-surface patch; story aggregates are post-hoc and declared so until P-1 lands.
3. Local-ref resolution and `EvidenceDir`: **concur**.
4. Docker: **acceptable**; local skips fine, **CI must execute** the Gitea lifecycle (fail, not skip).
