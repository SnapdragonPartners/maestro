# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a local multi-agent AI app factory built in Go. The system coordinates a PM agent, an Architect agent, and Coder agents to gather requirements, generate specs and stories, implement changes, review code, and merge pull requests.

## Architecture Decision Records

Proposed and accepted Architecture Decision Records live in `docs/adr/`. Check those ADRs before relying on older files in `docs/`, because many historical specs remain useful context but no longer describe the current implementation exactly.

Current documentation precedence:
1. Actual code and tests.
2. Canonical FSM docs in `pkg/pm/STATES.md`, `pkg/architect/STATES.md`, and `pkg/coder/STATES.md`.
3. Accepted ADRs in `docs/adr/`.
4. Current implementation summaries such as this file, `README.md`, and focused docs.
5. Retained v1 references at `docs/` root with `deprecated` front-matter status (unverified against current code; never authoritative for v2 design). Archived v1-era documents in `docs/archive/` carry no authority for any question (ADR 0017).

## Current Work: Maestro v2

Active development is the v2 redesign. v1 is frozen — v1 bugs are WONTFIX unless they block v2 work.

- `docs/v2/plan_roadmap.md` — phases, pillars, decisions D1-D10
- `docs/v2/process_build.md` — **the binding working agreement for how v2 gets built**
- `docs/v2/phase_x/plan_scope.md` — scope and plan for a phase; binding on that phase's work

Roles: **Claude authors** (docs, ADRs, scopes, specs, code), **Codex reviews**, **DR orchestrates and accepts**. An artifact is Accepted only when both Codex and DR approve. All Claude/Codex communication routes through DR. Contention that does not converge escalates to DR.

**Precedence.** `process_build.md` is the binding agreement and binds all three parties. This file is Claude's operating manual: it carries the command-level mechanics that execute that agreement — branch-name patterns, the git workflow, the version-tag ladder, documentation front matter — and restates just enough policy to be usable standalone. Where the two disagree, **`process_build.md` wins and this file is the bug.**

### Key Architecture Components

- **Runtime Kernel** (`internal/kernel/`) - Owns shared local infrastructure: dispatcher, SQLite, persistence queue, chat, WebUI, demo service, shared LLM factory, and Docker Compose registry
- **Supervisor** (`internal/supervisor/`) - Manages agent lifecycle, restart policy, SUSPEND recovery, and watchdog behavior
- **Task Dispatcher** (`pkg/dispatch/`) - Routes messages and state notifications between agents using typed channels and leases
- **Agent Message Protocol** (`pkg/proto/`) - Defines structured communication via `AgentMsg` with TASK, QUESTION/ANSWER, REQUEST/RESPONSE, ERROR, and SHUTDOWN flows
- **LLM Toolkit** (`maestro-llms`, external) - All provider I/O, retry/circuit/timeout/rate-limit middleware, and error classification live in `github.com/SnapdragonPartners/maestro-llms`, bridged via the `pkg/agent/internal/llmadapter` seam (see `docs/MAESTRO_LLMS_MIGRATION.md`)
- **Logging** (`pkg/logx/`) - Structured logging to project-local `.maestro/logs`
- **Configuration** (`pkg/config/`) - JSON config loader with environment variable overrides
- **Agent Foundation** (`pkg/agent/`) - Core LLM abstractions, state machine interfaces, and foundational components
  - **Toolloop System** (`pkg/agent/toolloop/`) - Generic LLM tool-calling loop with one terminal tool and `ProcessEffect`-based state signals
    - Uses `tools.ProcessEffect` for terminal signal and structured data
    - Keeps the generic `Config[T any]` shape for compatibility while the ProcessEffect migration is completed
    - Escalation support with soft/hard limits for iteration management
    - Tool execution persistence, activity tracking, and per-tool circuit breaking
- **PM State Machine** (`pkg/pm/`) - PM-specific state machine for user interviews, spec preview, spec submission, and user asks
- **Coder State Machine** (`pkg/coder/`) - Coder-specific state machine for structured coding workflows
- **Architect State Machine** (`pkg/architect/`) - Architect-specific state machine for spec processing and coordination
- **Template System** (`pkg/templates/`) - Prompt templates for different workflow states
- **MCP Tool Integration** (`pkg/tools/`) - Model Context Protocol tools including container management and file operations
- **Container Runtime** (`internal/state/`) - Container orchestration state management and history tracking
- **Container Tools** (`pkg/tools/`) - Container lifecycle management: build, test, switch, update operations

### Agent Flow
1. **PM Workflow**: Interviews the user, reads uploaded specs, drafts spec previews, submits specs to Architect, receives feedback, and remains available for tweaks, hotfixes, and user asks.

2. **Architect Workflow**: Reviews PM/CLI specs, generates stories, dispatches ready work, answers coder questions, reviews plans/code/completion/budget requests, merges PRs, and owns incidents.

3. **Coder Workflow**: Sets up a workspace, plans, codes, tests, requests review, prepares a PR, waits for merge, then terminates and restarts for new work.

4. **Message Types**:
   - **QUESTION/ANSWER**: Information requests ("How should I approach this?")
   - **REQUEST/RESPONSE**: Approval, spec review, merge, and budget requests
   - **TASK**: Work assignments from architect to coders
   - **ERROR/SHUTDOWN**: System control messages

5. **Agent Chat System**: Real-time collaboration channel
   - **chat_post**: Agents and humans can post messages to shared chat
   - **chat_read**: Agents can explicitly read messages (optional)
   - **Automatic Injection**: New messages are automatically injected into each LLM call
   - Messages are stored in database with session isolation and secret scanning
   - Web UI provides interactive chat interface for human participation
   - **Escalation Support**: When architect exceeds iteration limits, escalation messages are posted with `post_type: 'escalate'`, displayed prominently in WebUI with reply functionality for human guidance

6. System maintains structured SQLite state, project-local logs, and graceful shutdown/resume checkpoints.

## Architect Context Management

The architect maintains **per-agent conversation contexts** to eliminate contradictory feedback and preserve conversation continuity throughout story lifecycles.

### Key Design
- **One context per agent**: Each agent the architect communicates with (coders, PM) gets a dedicated `ContextManager`
- **Thread-safe access**: `sync.RWMutex` with double-check locking prevents race conditions during concurrent context creation
- **Persistent system prompts**: Each context starts with a comprehensive system prompt containing:
  - Agent ID and story ID
  - Full story details (title, content, spec ID)
  - Role descriptions and available tools
- **Knowledge context**: Story-specific knowledge packs are delivered through request content/templates rather than stored on story records
- **90% smaller request prompts**: Request-specific prompts now contain just the request content + brief instruction (story context in system prompt)
- **Context lifecycle**: Contexts reset automatically on story transitions — detected by comparing template names (which encode story IDs) at the top of `handleRequest()`

### Implementation
- **Location**: `pkg/architect/driver.go` - `agentContexts map[string]*contextmgr.ContextManager`
- **Context creation**: `getContextForAgent(agentID)` - Creates or retrieves agent-specific context
- **Context scoping**: `ensureContextForStory(agentID, storyID)` - Idempotent method that checks template name (`"agent-{agentID}-story-{storyID}"`) against current template. On mismatch (story change or first use), resets context with fresh system prompt and clears review streaks. No-op if already scoped to correct story.
- **Trigger**: Called at the top of `handleRequest()` in `pkg/architect/request.go`, using the **dispatcher lease** (`d.dispatcher.GetStoryForAgent(coderID)`) as the authoritative story source — not the request payload — to avoid desync in resume/reassignment scenarios.
- **Legacy wrapper**: `ResetAgentContext(agentID)` delegates to `ensureContextForStory` for backward compatibility
- **System prompt**: `buildSystemPrompt(ctx, agentID, storyID)` - Generates persistent context from story data
- **All request handlers** use agent-specific contexts:
  - `handleSingleTurnReview()` - Single-turn spec/plan reviews
  - `handleIterativeQuestion()` - Multi-turn Q&A with workspace inspection
  - `handleIterativeApproval()` - Multi-turn code reviews
  - `handleSpecReview()` - PM spec approval

### Benefits
- **No contradictory feedback**: Architect remembers previous interactions within story
- **Efficient prompts**: 90% reduction in prompt size by eliminating repeated story context
- **Clean boundaries**: Each story starts with fresh context to avoid cross-contamination
- **Automatic detection**: Story transitions are detected idempotently via template name comparison — no external trigger needed
- **Scalable**: Supports multiple concurrent conversations with different agents

See `docs/ARCHITECT_CONTEXT.md` for detailed implementation history and design decisions.

## Toolloop Pattern

The toolloop system (`pkg/agent/toolloop/`) provides a generic, type-safe abstraction for LLM tool-calling loops used by all agents.

### Design Principles

**One Goal, One Terminal Tool:**
- Each toolloop has one configured `TerminalTool`.
- General tools can be called repeatedly for exploration or work.
- Terminal tools should return `tools.ProcessEffect` with:
  - `Signal`: state transition signal such as `PLAN_REVIEW`, `TESTING`, or `REVIEW_COMPLETE`
  - `Data`: structured payload for the state machine
- Callers switch on `toolloop.Outcome.Kind`, then handle `OutcomeProcessEffect` by reading `Signal` and `EffectData`.
- The remaining `Config[T any]` generic exists for compatibility while the ProcessEffect migration is completed.

### Usage Pattern

```go
// 1. Configure the loop with general tools and exactly one terminal tool.
cfg := &toolloop.Config[CodingResult]{
    ContextManager: contextManager,
    GeneralTools:   []tools.Tool{readFileTool, listFilesTool},
    TerminalTool:   doneTool,
    MaxIterations:  10,
    Escalation: &toolloop.EscalationConfig{
        Key:       "coding_story123",
        SoftLimit: 8,   // Warning at 8 iterations
        HardLimit: 16,  // Stop at 16 iterations
        OnSoftLimit: func(count int) { /* log warning */ },
        OnHardLimit: func(ctx, key string, count int) error { /* escalate */ },
    },
}

// 2. Run toolloop and handle the outcome.
out := toolloop.Run[CodingResult](loop, ctx, cfg)
switch out.Kind {
case toolloop.OutcomeProcessEffect:
    switch out.Signal {
    case tools.SignalTesting:
        return StateTesting
    case tools.SignalStoryComplete:
        return StateCodeReview
    }
case toolloop.OutcomeIterationLimit:
    return StateBudgetReview
case toolloop.OutcomeLLMError:
    return proto.StateSuspend
}
```

### ProcessEffect Signals

ProcessEffect signal constants live in `pkg/tools/mcp.go`. Use those constants
instead of magic strings when writing tools or state-machine handlers.

### Escalation Management

The escalation system manages iteration limits and human intervention:

- **Soft Limit**: Warning threshold (e.g., 8 iterations) - callback invoked, execution continues
- **Hard Limit**: Stop threshold (e.g., 16 iterations) - execution halted, escalation triggered
- **Escalation Handler**: Posts to chat, waits for human guidance, returns error to stop loop
- **Per-Story Keys**: Each story has unique escalation key for independent tracking

All iteration counts are 1-indexed for user-facing logs.

## Container Architecture

The system uses a **three-container model** for safe development and deployment:

### Container Types

1. **Safe Container** (`maestro-bootstrap` or similar)
   - Bootstrap and fallback environment only
   - Contains build tools, Docker, analysis utilities
   - Never modified - always clean and reliable
   - Used when target container is unavailable

2. **Target Container** (project-specific, e.g., `maestro-projectname`)
   - Primary development environment for application code
   - Built from project's Dockerfile
   - Where coder agents normally execute
   - Updated through container_update tool

3. **Test Container** (temporary instances)
   - Throwaway containers for validation
   - Run on host (not docker-in-docker)
   - Test changes without affecting active environment

### Container Management

**Coder agents manage their own containers:**
- Start with verified target container or fallback to safe container
- Can build, test, and switch containers mid-execution
- Use `container_switch` for immediate environment changes
- Use `container_update` to set persistent target image for future runs
- Use `container_test` for temporary validation without disrupting active container

### Key Container Tools

- `container_build` - Build Docker images from Dockerfile
- `container_test` - Run validation in temporary containers (mount policy: CODING=RW, others=RO)
- `container_switch` - Switch active execution environment
- `container_update` - Set persistent target image configuration
- `container_list` - View available containers and registry status

**Mount Policy**: Test containers mount `/workspace` as read-only except in CODING state (read-write). `/tmp` is always writable.

**Orchestrator Role**: The main orchestrator does NOT manage container switching - agents are self-managing. The orchestrator only handles container cleanup via the container registry for containers that don't exit on their own.

**Architect Execution**: The architect agent runs in a containerized environment (safe container) to execute read tools safely. Coder workspaces are mounted as read-only volumes when the architect uses read tools to inspect code.

### Bind Mount Inode Preservation (CRITICAL)

**Problem**: On macOS with Docker Desktop, bind mounts track directory **inodes**, not paths. If you delete and recreate a directory (`os.RemoveAll` + `os.MkdirAll`), the new directory has a different inode, and existing bind mounts become stale (showing empty contents).

**Impact**: The architect container mounts coder workspaces at startup. If a coder's workspace directory is deleted and recreated (e.g., during story cleanup in SETUP state), the architect's mount becomes stale and `list_files`/`read_file` operations fail.

**Solution**: When cleaning workspace directories that may be bind-mounted, use `cleanDirectoryContents()` instead of delete+recreate:
```go
// WRONG - breaks bind mounts:
os.RemoveAll(dir)
os.MkdirAll(dir, 0755)

// CORRECT - preserves inode:
cleanDirectoryContents(dir)  // Removes contents, keeps directory
```

**Implementation**: See `pkg/utils/fs.go:CleanDirectoryContents()` for the canonical implementation.

**When to use delete+recreate**: Only for directories that are NOT bind-mounted to other containers:
- Temporary directories created and cleaned within a single operation
- State directories that aren't shared across containers
- Mirror/clone directories (subdirectories of the project, not the mount point itself)

**Code review rule**: Any code that calls `os.RemoveAll()` on a workspace root directory is a bug. Use `utils.CleanDirectoryContents()` instead.

### Phantom Diff Prevention

The architect's `get_diff` tool uses **merge-base semantics** by default (`git merge-base origin/main HEAD`), showing only changes made on the current branch. This prevents "phantom diffs" where changes from other merged PRs appear in the review.

The coder's review request does **not** include a raw git diff — the architect must call `get_diff` directly to inspect changes. This ensures a single canonical source of truth for diffs. Architect review prompts enforce a structured protocol requiring fresh `get_diff` calls on re-review, preventing the LLM from relying on stale tool results from prior review iterations.

### Multi-Architecture Artifacts (CRITICAL)

Development happens on arm64 (Apple Silicon), but CI and v2 production/benchmark run on amd64. Any artifact built on one arch and executed on another — **embedded/packaged binaries and published container images** — must be built for **both** `linux/amd64` and `linux/arm64`, or it fails at run time with `exec format error` on the arch that wasn't built (invisible locally, breaks only remotely). Container images: build multi-arch manifests (`docker buildx build --platform linux/amd64,linux/arm64 --push`) and pin by the arch-independent **manifest digest**. Binaries: cross-compile per-arch and select at runtime (the MCP proxy `proxy-linux-{amd64,arm64}` pattern). **Verify on each arch, not just build.** A single-arch build of a cross-arch artifact is a defect — see [ADR 0026](docs/adr/0026-multi-architecture-artifacts.md).

## Development Commands

Based on the project specification, the following commands should be available via Makefile:

```bash
make build    # Build the orchestrator binary (includes linting)
make test     # Run all tests (go test ./...)
make lint     # Run golangci-lint
make run      # Run the orchestrator with banner output
```

**Important**: All build commands (`make build`, `make agentctl`, `make replayer`) now include linting as a prerequisite. This ensures code quality is maintained at all times.

### Git Hooks

**Pre-commit Hooks:**
The repository includes pre-commit hooks that enforce:
- Build must pass
- All linting issues must be resolved
- Core tests should complete (with timeout)

The pre-commit hooks are automatically installed and will prevent commits with linting issues.

**Pre-push Hooks:**
The repository includes pre-push hooks that run integration tests:
- Checks for API keys (ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_GENAI_API_KEY)
- Skips if no API keys are set (with warning)
- Runs `make test-integration` if API keys are available
- Prevents push if integration tests fail
- **NEVER use `--no-verify` to bypass hooks** - fix the failing tests instead

### Testing Strategy

See `docs/TESTING_STRATEGY.md` for the comprehensive testing approach. Key points:

**Shared Mocks** (`internal/mocks/`):
- `MockLLMClient` - For testing agent flows with deterministic LLM responses
- `MockGitHubClient` - For PR/merge tests without hitting GitHub API
- `MockChatService` - For escalation and chat testing
- `MockGitRunner` - For git operations (clone, branch, merge)
- `MockContainerManager` - For Docker container lifecycle tests

**When to use real services vs mocks:**
- **LLMClient**: Always mock (non-deterministic, costly)
- **Dispatcher**: Always use real (in-memory, fast, deterministic)
- **GitRunner/ContainerManager**: Mock for unit tests, real for integration tests

**Running tests:**
```bash
make test              # Unit tests with mocks
make test-integration  # Integration tests with real services (requires API keys)
```

**Golden story suite**: runs at the end of each phase, not per-PR. Build tags `golden-minimal` (smoke subset) and `golden-all` (full suite), extending the existing `integration` tag pattern.

**Golden runs spend real money.** A `golden-*` run bills live model APIs — the Phase 1a `golden-all` run cost $26.40. **Never start one without DR's explicit approval or override for that specific run.** Prior approval of a phase, a plan, or an earlier run is not approval for this one. Every phase-end run gets a digest entry in `docs/v2/notes_conformance-log.md`: date, target identity, per-story verdict, cost/token totals.

## Git Workflow and Branch Protection

### Branch Protection Rules

The `main` branch is protected with the following rules:
- **No direct pushes to main** - All changes must go through pull requests
- **Required status checks** - CI tests must pass before merging
- Pre-commit hooks enforce local quality checks before commits

### Development Workflow

When making changes:
1. Create a branch off `main`: `v2/phase_x/XXX` for phase work, `v2/fix/XXX` for bug fixes (e.g. `v2/phase_2/artifact-envelopes`). Never reuse a prior leaf branch name as a namespace prefix — git ref collision.
2. Make changes and commit **locally** (pre-commit hooks will run)
3. **Codex review, in place.** Produce branch notes for Codex and iterate until every point is resolved or explicitly overridden by DR. Claude and Codex both review the local commits directly — no push is needed to review.
4. Push only once Codex and DR have approved: `git push -u origin <branch>` (pre-push hooks run integration tests if API keys are set)
5. Open a pull request to `main`, referencing the phase plan or ADR it executes
6. Resolve all CI reviewer feedback (see below), then wait for CI checks to pass
7. **DR merges.** Final approval and the merge button are DR's, never Claude's.

**Important**: Always work on feature branches. Never attempt to push directly to `main` as it will be rejected by branch protection rules.

**One dev branch at a time.** Never more than one feature/dev branch open concurrently; parallel branches are acceptable only for bug fixes. This bounds DR's review load, not authoring throughput.

**Push is a gate, not a step.** A branch that has not cleared Codex and DR does not get pushed. Review happens on local commits.

**Resolving PR review comments**: After addressing a PR review comment (including automated reviewers like Copilot) — by fixing it, or by determining it's a non-issue — push the change and **mark that review thread resolved** (e.g. `gh api graphql` `resolveReviewThread`), with a brief reply noting how it was addressed. Don't leave addressed threads open; do not resolve a thread without actually addressing it. Re-check for new threads after each push, since reviewers re-run on new commits.

### Pull Request Guidelines

When creating PRs for features with specification documents:
- **Reference the spec file** in the PR description (e.g., "See `docs/HOTFIX_MODE_SPEC.md` for detailed design")
- This helps code reviewers understand the design intent and implementation plan
- Spec files in `docs/` document architecture decisions, implementation phases, and acceptance criteria

### Spikes And Deferred Work

- Commit all open document work before a spike begins (risk minimization).
- Spike code **never** merges into `pkg/`, `internal/`, or `cmd/`. Reports land in the phase directory; scripts worth revisiting go under `spikes/phase_x/`, a standalone module excluded from the main build, test, and lint walkers. Preserved spike scripts are unmaintained by definition.
- Deferred work discovered along the way becomes a **GitHub Issue** on `SnapdragonPartners/maestro`. Division of labor: the roadmap holds *planned* work (phases and spikes), Issues hold *deferred* work found along the way, and `docs/v2/notes_parking-lot.md` holds *design ideas*.

### Version Tagging

Phases are tagged as SemVer prereleases of `v2.0.0`, so the whole v2 build is one ordered ladder:

```
v2.0.0-phase.1.0.0      phase 1 complete
v2.0.0-phase.1.1.0      phase 1a
v2.0.0-phase.1.1.1      phase 1a, second cut (bugfix during review)
v2.0.0-phase.2.0.0      phase 2
v2.0.0-phase.9.0.0      phase 9 — feature complete
v2.0.0-rc.1
v2.0.0
```

The ladder starts at `phase.2.0.0`. Phases 1 and 1a are deliberately **not** tagged: they predate this convention, broke several of the rules above, and were substantially corrective work on v1. Do not backfill tags for them.

Format is `<phase>.<subphase>.<iteration>` — **all numeric, always three slots**. Subphase `0` is the phase proper, `1` = a, `2` = b. So "phase 1a" is written `1.1.0`; that is the tax for total ordering. `phase` sorts after `alpha`/`beta` and before `rc` on plain ASCII, so either can be dropped in later without breaking order.

**Never write a mixed identifier like `v2.0.0-phase.1a`.** SemVer §11.4.3 ranks numeric identifiers *below* alphanumeric ones, so `phase.1a` sorts after `phase.2` and even `phase.10` — inverting the sequence — while git sorts it between `phase.1.1.0` and `phase.2.0.0`. The two disagree, which is a nasty class of bug. Rule: within any identifier position, keep every value the same type.

Git's default `version:refname` sorts `v2.0.0` *above* its own prereleases, which is backwards. Fix per-repo (verified on git 2.50.1):

```bash
git config --add versionsort.suffix "-alpha."
git config --add versionsort.suffix "-phase."
git config --add versionsort.suffix "-rc."
git config --add versionsort.suffix ""      # bare release sorts last
```

Spikes stay out of the version line entirely — a spike is not a candidate for `v2.0.0`. Git tags need not be versions:

```
spike/storage-layer-2026-07-24
spike/phase-2-auth
```

## Project Structure

The codebase follows this clean architecture:

### Core Foundation
- `pkg/agent/` - **Foundational abstractions**: LLM client interfaces, state machine building blocks, agent configuration
- `pkg/proto/` - **Message protocol**: AgentMsg definitions and validation
- `pkg/dispatch/` - **Message routing**: Queue management, rate limiting, retry logic
- `pkg/config/` - **Configuration**: JSON loader with environment variable overrides
- `pkg/state/` - **State persistence**: Agent state storage and recovery
- `pkg/templates/` - **Prompt templates**: Reusable LLM prompt templates
- `pkg/tools/` - **MCP integration**: Model Context Protocol tool implementations

### Agent Implementations
- `pkg/architect/` - **Architect agent**: Spec processing, story generation, coordination state machine
- `pkg/coder/` - **Coder agent**: Implementation workflows, coding state machine

### Supporting Infrastructure
- Rate limiting / retry / circuit breaking - provided by `maestro-llms` middleware (configured in `pkg/agent/factory_llms.go`)
- `pkg/contextmgr/` - Context management for LLM conversations
- `pkg/logx/` - Structured logging utilities
- `pkg/chat/` - Agent chat system for real-time collaboration and human-agent communication

### Runtime Directories
- `.maestro/config.json` - Project configuration
- `.maestro/maestro.db` - SQLite database for sessions, messages, audit data, chat, tool executions, and knowledge indexes
- `.maestro/logs/` - Runtime logs and debugging output
- `.mirrors/` - Local bare git mirrors
- `pm-001/`, `architect-001/`, `coder-001/`, etc. - Agent workspace directories
- `tests/fixtures/` - Test input files and examples
- `docs/` - Documentation and style guides

### Project Directory Structure

The system uses a specific directory layout for configuration and workspace management:

```
projectDir/                    # Binary location or CLI specified
├── .maestro/                 # Master configuration directory
│   ├── config.json          # Project configuration with pinned image IDs
│   ├── maestro.db           # Agent state, messages, sessions, and audit data
│   └── logs/                # Runtime logs
├── .mirrors/                # Repository mirrors
│   └── project-mirror.git/  # Bare git mirror of main repository
├── pm-001/                  # PM workspace
├── architect-001/           # Architect workspace
├── coder-001/               # Agent working directory (directory-name-safe agent ID)
├── coder-002/               # Agent working directory (directory-name-safe agent ID)
└── ...                      # Additional agent directories as needed
```

**Configuration Management:**
- `projectDir/.maestro/config.json` - Master config with pinned target image ID
- `projectDir/.maestro/maestro.db` - Agent state, session history, messages, chat, and audit data
- `coder-001/`, `coder-002/`, etc. - Individual agent working directories (repo clones with `.maestro/` for committed artifacts)

**Workspace Pre-creation:**
- Coder working directories are created before agent execution starts
- Ensures workspaces exist when architect uses read tools to inspect code
- Implemented in `pkg/workspace/verify.go` and called during startup

### LLM Abstraction
All AI model interactions go through the unified `LLMClient` interface in `pkg/agent/`, which is adapted onto the **maestro-llms** toolkit by `pkg/agent/internal/llmadapter`:
- Providers (Anthropic, OpenAI, Google/Gemini, Ollama), retry/circuit/timeout/rate-limit middleware, and typed error classification are all owned by `maestro-llms`
- Maestro keeps the app-side glue: transcript normalization, explicit tool-choice policy, agent-aware empty-response/pause-turn handling, the SUSPEND boundary (`llmerrors.IsServiceUnavailable`), cost/story metrics, and the Gemini `ProviderSignature` (thought-signature) round-trip
- Adding a provider is a maestro-llms change, not a Maestro one
- See `docs/MAESTRO_LLMS_MIGRATION.md` for the design and the divergence checklist

## Documentation Conventions

Docs under `docs/` carry TOML front matter:

```
+++
title = "..."
edit_date = "YYYY-MM-DD"
status = "live" | "draft" | "deprecated" | "archive"
summary = "One sentence, used verbatim in the parent README index."
+++
```

Update `edit_date` on every edit. When an artifact lands in a phase directory, add its one-line entry to that directory's `README.md`. Per-phase working artifacts live in `docs/v2/phase_x/` mirroring the branch namespace; cross-phase docs stay at `docs/v2/`; Accepted decisions land in `docs/adr/`.

## Data Architecture & Persistence

### Canonical Data Sources

The system maintains clear separation between ephemeral and persistent data:

**Architect In-Memory State (Canonical for Stories)**
- Stories are the architect's in-memory state - this is the **single source of truth for active stories**
- Represents "what's happening right now" in the current session
- Automatically filters out stale stories from previous runs
- Web UI pulls story data from architect's `GetStoryList()` method
- Database stores stories for audit/history, but architect state is authoritative for active work

**Database (Canonical for Messages & Audit Data)**
- Messages (QUESTION/ANSWER, REQUEST/RESPONSE) are **canonical in database**
- Agents process messages and discard them (fire-and-forget to persistence queue)
- Database is the only source of truth for message history
- All audit data (agent_requests, agent_responses, agent_plans) persists to database
- Web UI queries database directly for message viewer

### Session Management

**Session ID in Config:**
- `config.json` contains `session_id` field (generated UUID at startup)
- Persistence layer reads `session_id` from config for all writes
- Agents remain unaware of sessions - persistence layer adds `session_id` automatically
- Web UI defaults to current session, can query historical sessions explicitly

**Session Lifecycle:**
1. **New Session**: Generate new UUID, save to config.json, all writes use new session_id
2. **Restart Session**: Keep existing session_id in config.json to continue interrupted work
3. **Historical Analysis**: Web UI can request data from old session_id via query parameters

**Agent Isolation:**
- Agents only write via fire-and-forget queue (never read database)
- Agents never know about session_id
- Persistence worker adds session_id before INSERT
- Clean separation: agents produce data, persistence layer manages storage

### Logs vs Database

**Logs** (`.maestro/logs/`):
- Human-readable event stream for debugging
- Never parsed for structured data
- Used only by log viewer in web UI

**Database** (`maestro.db`):
- Structured, queryable data for messages and audit trail
- Source of truth for all inter-agent communication
- Enables historical analysis and session restart

## Configuration

The system uses JSON configuration with environment variable overrides:
- Config path via `CONFIG_PATH` env var or command flag
- Placeholder substitution: `${ENV_VAR}` in JSON
- Direct env override: any JSON key can be overridden by matching env var name
- Model-specific settings for rate limits and budgets
- `session_id` field tracks current orchestrator session (generated at startup or reused for restarts)

### Chat Configuration

Chat system is configured in the `chat` section of `config.json`:

```json
{
  "chat": {
    "enabled": true,
    "max_new_messages": 100,
    "limits": {
      "max_message_chars": 4096
    },
    "scanner": {
      "enabled": true,
      "timeout_ms": 800
    }
  }
}
```

- **enabled**: Enable/disable the chat system
- **max_new_messages**: Maximum messages to inject into each LLM call (default: 100)
- **limits.max_message_chars**: Maximum characters per message (default: 4096)
- **scanner.enabled**: Enable secret scanning for chat messages
- **scanner.timeout_ms**: Timeout for secret scanning operations

See `docs/MAESTRO_CHAT_SPEC.md` for detailed chat system architecture and implementation notes.

## Getting Help

If you get stuck, have questions, or need clarification on anything, use your get_help tool.

## Codex Review Guidelines

When reviewing this repository:

- Prioritize correctness, robustness, and maintainability over cleverness or micro-optimizations.
- Assume this is a single-user, locally run app with a moderate security posture: we care about glaring issues and obviously unsafe practices, but we do not need enterprise-grade hardening.
- When in doubt, favor simple, idiomatic Go.
- Only block PRs on P0 and P1 issues:
  - P0: Likely bugs, data corruption, crashes, or severe security issues.
  - P1: Clear violations of these guidelines that will noticeably harm maintainability, clarity, or robustness.
- Treat everything else as suggestions or questions.

### Go Style and Modern Features

- Use modern Go constructs.
- Flag use of `interface{}`; prefer `any` in new code.
- Prefer generics where they simplify code or eliminate duplication, not where they add unnecessary abstraction.
- Prefer clear, idiomatic Go over clever one-liners.
- Encourage explicit error handling, especially where errors are silently dropped or logged without context.
- Enforce standard Go formatting and naming where it materially improves clarity.
- Treat non-idiomatic constructs that reduce readability as P1 if they are easy to fix.

### SafeAssert vs Bare Type Assertions

We use a generics-based helper called `SafeAssert` to replace unsafe, brittle, or repeatedly duplicated type assertions.

Flag any bare type assertion of the form:

```go
v := x.(T)
v, ok := x.(T)
```

Recommend replacing it with the project's `SafeAssert` pattern unless the assertion is performance-critical and there is clear evidence it cannot fail, such as a well-constrained generic type parameter or validated upstream input.

When `SafeAssert` improves clarity, error messaging, or robustness, prefer it even if the bare assertion includes `, ok`. Treat unsafe or unjustified bare type assertions as P1.

### Constants vs Literals

- Prefer named constants for magic numbers, repeated string literals, keys, paths, environment variables, API endpoints, timeouts, limits, and well-known sizes.
- Flag repeated literals that should be constants.
- Obvious, single-use literals may stay inline, such as `len(x) == 0`.
- Treat repeated or unclear literals as P1.

### DRY, Reuse, and Robustness

- Watch for duplicated logic or near-duplicate code blocks.
- Prefer well-tested shared helpers over repeated custom implementations.
- Suggest extracting helpers or consolidating logic when reuse is clear and beneficial.
- If duplication is intentional to avoid coupling, ask the author to confirm this in the PR.
- Treat obvious duplication that would materially benefit from reuse as P1.

### Abstraction Level and Architecture

- Push back on unnecessary or overly layered abstractions.
- Flag interfaces with one implementation and thin wrapper layers that add no testability, clarity, or reuse.
- Prefer simple, direct designs over abstractions added just in case.
- Accept purposeful abstractions that meaningfully improve modularity or support multiple backends, such as the LLM and forge abstraction layers.
- Treat clearly gratuitous abstraction as P1; treat borderline cases as questions.

### Comments, TODOs, and Deprecation

- Treat comments as part of the contract.
- Flag outdated or misleading comments.
- For `TODO`, `FIXME`, or deprecation notes, ask whether there is a corresponding spec, plan, or ticket.
- Ask for a reference to that item inside the comment when appropriate.
- Push back on TODOs that mask meaningful risks without tracking.
- Treat critical-path TODOs without tracking as P1.

### Dead Code and Cleanup

- Flag orphaned or dead code, including unused functions, unreachable blocks, and fields with no references.
- Ask for removal or clarification, such as feature-gate or build-tag usage.
- Allow code behind legitimate build tags or experiments when documented.
- Treat clear dead code with no justification as P1.

### Security Posture

Given this app is single-user and locally run, flag glaring issues:

- Arbitrary command execution from untrusted input.
- Unsanitized user-controlled paths passed to shell commands or filesystem operations.
- Secrets committed to source.

Be lenient on running as root inside local Docker and debug logging in development configurations. Prefer pragmatic mitigations over heavy security refactors. Treat only truly dangerous patterns as P0.

### Testing Expectations

We do not enforce a strict test coverage threshold, but flag obvious missing tests where they would materially improve confidence or prevent regressions.

Recommend unit tests for:

- New logic with multiple branches or edge cases.
- Reusable helpers or parsing/validation logic.
- Behavior that previously caused bugs.

Recommend integration tests with the `integration` build tag for:

- Cross-component workflows.
- Realistic interactions with files, APIs, or external systems.
- Cases where unit tests alone cannot provide coverage or fidelity.

Avoid nitpicks when tests would provide little real value. Treat missing tests for clearly complex or risk-prone logic as P1; otherwise treat them as suggestions.

### Clean Code vs Expediency

- Encourage clean, readable, maintainable code.
- Push back when short-term hacks significantly increase long-term maintenance cost.
- Accept pragmatic shortcuts when clearly documented and tracked.

### Tone and Collaboration

- Use constructive, specific feedback.
- When guidelines conflict with established Go idioms or library conventions, prefer those idioms and call out the trade-offs rather than insisting.
