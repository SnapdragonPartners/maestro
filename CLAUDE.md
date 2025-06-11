# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is an MVP Multi-Agent AI Coding System orchestrator built in Go. The system coordinates between an Architect Agent (o3) and Coding Agents (Claude) to process development stories and implement code changes.

### Key Architecture Components

- **Task Dispatcher** (`pkg/dispatch/`) - Routes messages between agents with rate limiting and retry logic
- **Agent Message Protocol** (`pkg/proto/`) - Defines structured communication via `AgentMsg` with types: TASK, RESULT, ERROR, QUESTION, SHUTDOWN
- **Rate Limiting** (`pkg/limiter/`) - Token bucket per-model rate limiting with daily budget enforcement
- **Event Logging** (`pkg/eventlog/`) - Structured logging to `logs/events.jsonl` with daily rotation
- **Configuration** (`pkg/config/`) - JSON config loader with environment variable overrides
- **State Machine Driver** (`pkg/agent/`) - Phase 3 state machine for structured coding workflows
- **Template System** (`pkg/templates/`) - Prompt templates for different workflow states
- **MCP Tool Integration** (`pkg/tools/`) - Model Context Protocol tools for file operations in workspaces

### Agent Flow
1. Architect agent reads development stories and creates TASK messages
2. Dispatcher routes tasks to appropriate coding agents with rate limiting
3. **Phase 3 State Machine**: Coding agents execute structured workflow:
   - **PLANNING**: Analyze task and create implementation plan
   - **CODING**: Generate code using MCP tools to create files in workspace
   - **TESTING**: Run formatting, building, and tests on generated code
   - **AWAIT_APPROVAL**: Request review and approval from architect
   - **DONE**: Mark task as completed
4. System maintains event logs and handles graceful shutdown with STATUS.md dumps

## Development Commands

Based on the project specification, the following commands should be available via Makefile:

```bash
make build    # Build the orchestrator binary
make test     # Run all tests (go test ./...)
make lint     # Run golangci-lint
make run      # Run the orchestrator with banner output
```

## Project Structure

The codebase follows this structure:
- `agents/` - Agent implementations (architect, coding)
- `config/` - Configuration files (config.json)
- `pkg/` - Core packages:
  - `agent/` - Phase 3 state machine driver
  - `dispatch/` - Message routing and retry logic
  - `proto/` - Message protocol definitions
  - `limiter/` - Rate limiting and budget tracking
  - `eventlog/` - Event logging system
  - `config/` - Configuration loader
  - `templates/` - Prompt templates for workflow states
  - `tools/` - MCP tool implementations
  - `logx/` - Structured logging
- `logs/` - Runtime event logs
- `stories/` - Development story definitions
- `tests/fixtures/` - Test input files and examples
- `docs/` - Documentation and STYLE.md

## Story-Driven Development

Development follows ordered stories defined in PROJECT.md. Each story has:
- Numeric ID and dependencies
- Acceptance criteria
- Estimation points (1-3)
- Self-contained implementation scope

Stories 001-012 define the complete MVP implementation path from scaffolding to end-to-end testing.

## Configuration

The system uses JSON configuration with environment variable overrides:
- Config path via `CONFIG_PATH` env var or command flag
- Placeholder substitution: `${ENV_VAR}` in JSON
- Direct env override: any JSON key can be overridden by matching env var name
- Model-specific settings for rate limits and budgets

## Getting Help

If you get stuck, have questions, or need clarification on anything, use your get_help tool.