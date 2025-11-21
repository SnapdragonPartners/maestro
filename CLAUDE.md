# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is an MVP Multi-Agent AI Coding System orchestrator built in Go. The system coordinates between an Architect Agent (o3) and Coding Agents (Claude) to process development stories and implement code changes.

### Key Architecture Components

- **Task Dispatcher** (`pkg/dispatch/`) - Routes messages between agents with rate limiting and retry logic
- **Agent Message Protocol** (`pkg/proto/`) - Defines structured communication via `AgentMsg` with types: TASK, RESULT, ERROR, QUESTION, SHUTDOWN, ANSWER, REQUEST
- **Rate Limiting** (`pkg/limiter/`) - Token bucket per-model rate limiting with daily budget enforcement
- **Event Logging** (`pkg/eventlog/`) - Structured logging to `logs/events.jsonl` with daily rotation
- **Configuration** (`pkg/config/`) - JSON config loader with environment variable overrides
- **Agent Foundation** (`pkg/agent/`) - Core LLM abstractions, state machine interfaces, and foundational components
  - **Toolloop System** (`pkg/agent/toolloop/`) - Generic LLM tool-calling loop with type-safe result extraction
    - Uses Go generics (`Config[T any]`) for typed result extraction
    - Separation of concerns: `CheckTerminal` (signals) vs `ExtractResult` (data)
    - Escalation support with soft/hard limits for iteration management
    - All agents use proper result types (no no-op extractors)
- **Coder State Machine** (`pkg/coder/`) - Coder-specific state machine for structured coding workflows
- **Architect State Machine** (`pkg/architect/`) - Architect-specific state machine for spec processing and coordination
- **Template System** (`pkg/templates/`) - Prompt templates for different workflow states
- **MCP Tool Integration** (`pkg/tools/`) - Model Context Protocol tools including container management and file operations
- **Container Runtime** (`internal/state/`) - Container orchestration state management and history tracking
- **Container Tools** (`pkg/tools/`) - Container lifecycle management: build, test, switch, update operations

### Agent Flow
1. **Architect Workflow**: Processes development specifications through state machine:
   - **SPEC_PARSING**: Parse specification files into requirements using LLM or deterministic parser
   - **STORY_GENERATION**: Generate story files from requirements
   - **QUEUE_MANAGEMENT**: Load stories and manage dependencies
   - **DISPATCHING**: Assign ready stories to coding agents
   - **ANSWERING**: Handle technical questions from coding agents (QUESTION→ANSWER)
   - **REVIEWING**: Evaluate code submissions and provide approval/feedback (REQUEST→RESULT)
     - **Iterative Approval with Read Tools**: Architect can use MCP read tools to inspect coder workspaces (read_file, list_files, get_diff, submit_reply) before approving
     - **Iteration Limits**: Soft limit at 8 iterations (warning), hard limit at 16 iterations (escalation)
   - **ESCALATED**: Wait for human intervention when iteration limits exceeded or cannot answer (2-hour timeout)

2. **Coder Workflow**: Implements stories through state machine:
   - **PLANNING**: Analyze task and create implementation plan
   - **CODING**: Generate code using MCP tools to create files in workspace
   - **TESTING**: Run formatting, building, and tests on generated code
   - **AWAIT_APPROVAL**: Request review and approval from architect
   - **DONE**: Mark task as completed

3. **Message Types**:
   - **QUESTION/ANSWER**: Information requests ("How should I approach this?")
   - **REQUEST/RESULT**: Approval requests ("Please review this code")
   - **TASK**: Work assignments from architect to coders
   - **ERROR/SHUTDOWN**: System control messages

4. **Agent Chat System**: Real-time collaboration channel
   - **chat_post**: Agents and humans can post messages to shared chat
   - **chat_read**: Agents can explicitly read messages (optional)
   - **Automatic Injection**: New messages are automatically injected into each LLM call
   - Messages are stored in database with session isolation and secret scanning
   - Web UI provides interactive chat interface for human participation
   - **Escalation Support**: When architect exceeds iteration limits, escalation messages are posted with `post_type: 'escalate'`, displayed prominently in WebUI with reply functionality for human guidance

5. System maintains event logs and handles graceful shutdown with STATUS.md dumps

## Toolloop Pattern

The toolloop system (`pkg/agent/toolloop/`) provides a generic, type-safe abstraction for LLM tool-calling loops used by all agents.

### Design Principles

**Clean Separation of Concerns:**
- `CheckTerminal(calls, results) string` - Determines if workflow is complete (returns signal)
  - "Are we done?" - Only checks for terminal conditions
  - Returns signals like "PLAN_REVIEW", "CODING", "TESTING", "DONE", etc.
  - Does NOT extract data from tool calls
- `ExtractResult(calls, results) (T, error)` - Extracts typed data from tool execution
  - "What happened?" - Type-safe data extraction
  - Returns strongly-typed result structs (e.g., `PlanningResult`, `SubmitReplyResult`)
  - Called automatically when terminal signal is detected
- Process function - Stores extracted data in state machine
  - Handles side effects (logging, persistence, state updates)

### Usage Pattern

```go
// 1. Define result type
type PlanningResult struct {
    Signal string
    Plan   string
    // ... other fields
}

// 2. Create extraction function
func ExtractPlanningResult(calls []agent.ToolCall, results []any) (PlanningResult, error) {
    // Extract data from tool calls/results
}

// 3. Configure toolloop with type parameter
cfg := &toolloop.Config[PlanningResult]{
    ContextManager: contextManager,
    ToolProvider:   toolProvider,
    MaxIterations:  10,
    CheckTerminal:  checkTerminalFunc,  // Only checks signals
    ExtractResult:  ExtractPlanningResult,  // Type-safe extraction
    Escalation: &toolloop.EscalationConfig{
        Key:       "planning_story123",
        SoftLimit: 8,   // Warning at 8 iterations
        HardLimit: 16,  // Stop at 16 iterations
        OnSoftLimit: func(count int) { /* log warning */ },
        OnHardLimit: func(ctx, key string, count int) error { /* escalate */ },
    },
}

// 4. Run toolloop and get typed result
signal, result, err := toolloop.Run(loop, ctx, cfg)

// 5. Process extracted result
processPlanningResult(&result)  // Store in state, log, etc.

// 6. Handle terminal signal
switch signal {
case "PLAN_REVIEW": return StatePlanReview
// ...
}
```

### Result Types by Agent

**Architect** (`pkg/architect/toolloop_results.go`):
- `SubmitReplyResult` - Question/request responses
- `SpecReviewResult` - Spec approval decisions
- `ReviewCompleteResult` - Code review outcomes

**PM** (`pkg/pm/toolloop_results.go`):
- `WorkingResult` - Bootstrap params, spec content, await_user flag

**Coder** (`pkg/coder/toolloop_results.go`):
- `PlanningResult` - Plan, confidence, exploration, risks, todos, knowledge pack
- `CodingResult` - Todo completions, testing requests
- `TodoCollectionResult` - Extracted todo list

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

## Development Commands

Based on the project specification, the following commands should be available via Makefile:

```bash
make build    # Build the orchestrator binary (includes linting)
make test     # Run all tests (go test ./...)
make lint     # Run golangci-lint
make run      # Run the orchestrator with banner output
```

**Important**: All build commands (`make build`, `make agentctl`, `make replayer`) now include linting as a prerequisite. This ensures code quality is maintained at all times.

### Pre-commit Hooks

The repository includes pre-commit hooks that enforce:
- Build must pass
- All linting issues must be resolved
- Core tests should complete (with timeout)

The pre-commit hooks are automatically installed and will prevent commits with linting issues.

## Git Workflow and Branch Protection

### Branch Protection Rules

The `main` branch is protected with the following rules:
- **No direct pushes to main** - All changes must go through pull requests
- **Required status checks** - CI tests must pass before merging
- Pre-commit hooks enforce local quality checks before commits

### Development Workflow

When making changes:
1. Create a feature branch: `git checkout -b feature-name`
2. Make your changes and commit (pre-commit hooks will run)
3. Push the branch: `git push -u origin feature-name`
4. Create a pull request to `main`
5. Wait for CI checks to pass
6. Merge via GitHub UI after approval

**Important**: Always work on feature branches. Never attempt to push directly to `main` as it will be rejected by branch protection rules.

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
- `pkg/limiter/` - Token bucket rate limiting with daily budget enforcement
- `pkg/eventlog/` - Structured logging to `logs/events.jsonl` with daily rotation
- `pkg/contextmgr/` - Context management for LLM conversations
- `pkg/logx/` - Structured logging utilities
- `pkg/chat/` - Agent chat system for real-time collaboration and human-agent communication

### Runtime Directories
- `config/` - Configuration files (config.json)
- `logs/` - Runtime event logs and debugging output
- `stories/` - Generated story files from specifications
- `work/` - Agent workspace directories with isolated state
- `tests/fixtures/` - Test input files and examples
- `docs/` - Documentation and style guides

### Project Directory Structure

The system uses a specific directory layout for configuration and workspace management:

```
projectDir/                    # Binary location or CLI specified
├── .maestro/                 # Master configuration directory
│   ├── config.json          # Project configuration with pinned image IDs
│   └── database/            # Agent state and history database
├── .mirrors/                # Repository mirrors
│   └── project-mirror.git/  # Bare git mirror of main repository
├── coder-001/               # Agent working directory (directory-name-safe agent ID)
├── coder-002/               # Agent working directory (directory-name-safe agent ID)
└── ...                      # Additional agent directories as needed
```

**Configuration Management:**
- `projectDir/.maestro/config.json` - Master config with pinned target image ID
- `projectDir/.maestro/database/` - Agent state, container history, runtime data
- `coder-001/`, `coder-002/`, etc. - Individual agent working directories (repo clones with `.maestro/` for committed artifacts)

**Workspace Pre-creation:**
- Coder working directories are created before agent execution starts
- Ensures workspaces exist when architect uses read tools to inspect code
- Implemented in `pkg/workspace/verify.go` and called during startup

### LLM Abstraction
All AI model interactions go through the unified `LLMClient` interface in `pkg/agent/`:
- `ClaudeClient` - Anthropic Claude integration
- `O3Client` - OpenAI O3 integration  
- Easily extensible for new LLM providers

## Story-Driven Development

Development follows ordered stories defined in PROJECT.md. Each story has:
- Numeric ID and dependencies
- Acceptance criteria
- Estimation points (1-3)
- Self-contained implementation scope

Stories 001-012 define the complete MVP implementation path from scaffolding to end-to-end testing.

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
- Messages (QUESTION/ANSWER, REQUEST/RESULT) are **canonical in database**
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

**Logs** (`logs/events.jsonl`, `logs/run.log`):
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