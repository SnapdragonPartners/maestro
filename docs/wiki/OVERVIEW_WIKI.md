# Maestro Architecture Overview

**Last Updated**: 2025-11-07

This document provides a high-level introduction to the Maestro multi-agent AI coding system. It's designed for developers exploring the codebase for the first time.

## What is Maestro?

Maestro is a Multi-Agent AI Coding System orchestrator built in Go. The system coordinates between different AI agents to process development specifications and implement code changes autonomously.

**Key Capabilities:**
- Parse development specifications into structured stories
- Coordinate multiple coding agents working in parallel
- Manage containerized development environments
- Track progress with audit trails and event logging
- Support human-in-the-loop intervention when needed

## Core Concept: Multi-Agent Collaboration

Maestro uses a **coordinator-worker pattern** with two types of agents:

### Architect Agent (Coordinator)
- Powered by OpenAI's O3 model
- Processes development specifications
- Generates structured stories with dependencies
- Assigns work to coding agents
- Reviews code submissions
- Answers technical questions

### Coder Agents (Workers)
- Powered by Anthropic's Claude model
- Implement assigned stories
- Execute in isolated containerized workspaces
- Use MCP (Model Context Protocol) tools for file operations
- Request reviews from the architect
- Can ask questions during implementation

### Communication Flow

```
Developer
    │
    ├─ Writes Spec File
    │
    ▼
Architect Agent
    │
    ├─ Parses Specification
    ├─ Generates Stories
    ├─ Manages Dependencies
    │
    ├─────────────────┬────────────────┐
    ▼                 ▼                ▼
Coder-001       Coder-002        Coder-003
    │                 │                │
    ├─ Plans          ├─ Plans         ├─ Plans
    ├─ Codes          ├─ Codes         ├─ Codes
    ├─ Tests          ├─ Tests         ├─ Tests
    │                 │                │
    └─► Requests Review
         │
         ▼
    Architect Reviews
         │
         ├─ Approve → Story Complete
         └─ Request Changes → Coder Iterates
```

## State Machines

Both agent types operate as state machines with well-defined transitions:

### Architect States

1. **SPEC_PARSING** - Parse specification into requirements
2. **STORY_GENERATION** - Generate structured stories from requirements
3. **QUEUE_MANAGEMENT** - Load stories and manage dependencies
4. **DISPATCHING** - Assign ready stories to available coders
5. **ANSWERING** - Respond to technical questions from coders
6. **REVIEWING** - Evaluate code submissions and provide feedback
7. **ESCALATED** - Wait for human intervention when needed

### Coder States

1. **PLANNING** - Analyze task and create implementation plan
2. **CODING** - Generate code using MCP tools
3. **TESTING** - Run formatting, building, and tests
4. **AWAIT_APPROVAL** - Request review from architect
5. **DONE** - Mark task as completed

## Message Protocol

Agents communicate via a structured message protocol (`pkg/proto/`):

### Message Types

- **TASK** - Work assignments from architect to coders
- **QUESTION** / **ANSWER** - Information requests between agents
- **REQUEST** / **RESULT** - Code review requests and responses
- **ERROR** - Error notifications
- **SHUTDOWN** - System control messages

### Message Flow Examples

**Story Assignment:**
```
Architect → TASK → Coder-001
```

**Technical Question:**
```
Coder-001 → QUESTION → Architect → ANSWER → Coder-001
```

**Code Review:**
```
Coder-001 → REQUEST → Architect → RESULT → Coder-001
```

## Container Architecture

Maestro uses a **three-container model** for safe, isolated development:

### Container Types

1. **Safe Container** (`maestro-bootstrap`)
   - Bootstrap and fallback environment
   - Never modified - always clean and reliable
   - Contains essential build tools

2. **Target Container** (project-specific)
   - Primary development environment
   - Built from project's Dockerfile
   - Where coder agents normally execute

3. **Test Container** (temporary)
   - Throwaway containers for validation
   - Test changes without affecting active environment

### Self-Managing Agents

Coder agents manage their own containers:
- Start with verified target container or fallback to safe
- Can build, test, and switch containers during execution
- Use MCP tools for container lifecycle operations

## Project Structure

### Core Packages

```
pkg/
├── agent/          # LLM client interface and foundational abstractions
├── architect/      # Architect agent state machine
├── coder/          # Coder agent state machine
├── proto/          # Message protocol definitions
├── dispatch/       # Message routing with rate limiting
├── config/         # Configuration management
├── tools/          # MCP tool implementations
├── limiter/        # Token bucket rate limiting
├── eventlog/       # Structured event logging
├── chat/           # Agent chat system
└── templates/      # Prompt templates
```

### Working Directories

```
projectDir/
├── .maestro/               # Master configuration
│   ├── config.json        # Project settings
│   └── database/          # Agent state and history
├── .mirrors/              # Repository mirrors
├── coder-001/             # Agent workspace (isolated)
├── coder-002/             # Agent workspace (isolated)
└── logs/                  # Event logs
```

## Key Subsystems

### 1. LLM Abstraction Layer
- Unified `LLMClient` interface for all AI providers
- Middleware chain for resilience (retry, circuit breaker, timeout)
- Token bucket rate limiting with daily budgets
- Comprehensive metrics and observability
- See `docs/wiki/LLM_WIKI.md` for details

### 2. MCP Tools System
- Model Context Protocol tools for agent capabilities
- File operations (read, write, edit, list)
- Container management (build, test, switch)
- Chat system (post, read messages)
- Completion tools (submit_plan, done, etc.)
- See `docs/wiki/TOOLS_WIKI.md` for details

### 3. Task Dispatcher
- Routes messages between agents
- Manages queues with rate limiting
- Handles retry logic for failed deliveries
- Coordinates concurrent agent execution

### 4. Event Logging
- Structured JSON logs (`logs/events.jsonl`)
- Daily rotation with timestamps
- Audit trail for all agent actions
- Human-readable debugging output

### 5. Chat System
- Real-time collaboration channel
- Automatic message injection into LLM calls
- Human-in-the-loop participation via Web UI
- Secret scanning for sensitive data
- Escalation support for human intervention

### 6. State Persistence
- SQLite database for messages and audit data
- Session-based isolation
- Agent state storage and recovery
- Architect in-memory state for active stories

## Data Flow

### 1. Specification Processing
```
Developer writes spec → Architect parses → Stories generated → Queue managed
```

### 2. Story Execution
```
Architect dispatches → Coder plans → Coder codes → Coder tests → Review requested
```

### 3. Code Review
```
Architect reads code → Evaluates quality → Provides feedback → Iterates or approves
```

### 4. Iteration Control
- Soft limit: 8 iterations (warning)
- Hard limit: 16 iterations (escalation to human)
- 2-hour timeout for human response

## Configuration

Configuration is managed via JSON with environment variable overrides:

```json
{
  "session_id": "uuid",
  "models": {
    "architect_model": "o3-mini",
    "coder_model": "claude-sonnet-4.5"
  },
  "rate_limits": {
    "o3-mini": { "tpm": 2000000, "rpm": 10000 },
    "claude-sonnet-4.5": { "tpm": 400000, "rpm": 50 }
  },
  "chat": {
    "enabled": true,
    "max_new_messages": 100
  }
}
```

**Key Features:**
- Environment variable substitution: `${API_KEY}`
- Direct env overrides for any config key
- Session ID for tracking and restart
- Model-specific rate limits and budgets

## Development Commands

```bash
make build    # Build orchestrator binary (includes linting)
make test     # Run all tests
make lint     # Run golangci-lint
make run      # Run orchestrator with banner
```

**Pre-commit Hooks:**
- Build must pass
- Linting issues must be resolved
- Core tests should complete

## Observability

### Event Logs
- `logs/events.jsonl` - Structured event stream
- `logs/run.log` - Human-readable output
- Daily rotation with timestamps

### Database
- `maestro.db` - Structured, queryable data
- Agent messages and responses
- State history and audit trails
- Session-based isolation

### Metrics
- LLM request/response tracking
- Token usage and cost calculation
- Rate limit enforcement
- Error classification and retry counts

### Web UI
- Real-time progress monitoring
- Story status visualization
- Message viewer for agent communication
- Interactive chat for human intervention
- Escalation notifications with reply functionality

## Error Handling

### Resilience Patterns
- **Retry with exponential backoff** - Transient failures
- **Circuit breaker** - Protect against cascading failures
- **Timeouts** - Prevent hung requests
- **Rate limiting** - Respect API quotas

### Error Classification
- **Retryable errors** - Network issues, rate limits
- **Non-retryable errors** - Invalid input, auth failures
- **Empty responses** - Two-tier retry with LLM guidance

### Human Intervention
- **Escalation** - When iteration limits exceeded
- **Question handling** - Architect can't answer technical questions
- **Manual approval** - Override automated decisions

## Getting Started

### For Developers
1. Read `CLAUDE.md` for project context
2. Review this overview for architecture
3. Explore `docs/wiki/LLM_WIKI.md` for LLM details
4. Explore `docs/wiki/TOOLS_WIKI.md` for MCP tools
5. Check `pkg/agent/`, `pkg/architect/`, `pkg/coder/` for implementations

### For AI Agents
1. Understand state machines and transitions
2. Use MCP tools for capabilities
3. Follow message protocol for communication
4. Respect container isolation
5. Request human help when needed

## Design Philosophy

### Clean Architecture
- Clear separation of concerns
- Dependency injection throughout
- Testable components
- Middleware composition

### Agent Autonomy
- Self-managing containers
- Independent decision-making
- Explicit coordination points
- Human-in-the-loop when needed

### Observability First
- Comprehensive logging
- Structured audit trails
- Real-time progress tracking
- Historical analysis support

### Resilience by Design
- Graceful degradation
- Automatic retry logic
- Circuit breaker protection
- Session restart capability

## What's Next?

This overview covers the high-level architecture. For detailed information on specific subsystems:

- **LLM Layer**: `docs/wiki/LLM_WIKI.md`
- **MCP Tools**: `docs/wiki/TOOLS_WIKI.md`
- **Chat System**: `docs/MAESTRO_CHAT_SPEC.md`
- **API Reference**: See package-level godoc comments
- **Project Instructions**: `CLAUDE.md`

The codebase is actively evolving. Check git history and recent commits for the latest changes.
