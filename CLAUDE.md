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
- **Coder State Machine** (`pkg/coder/`) - Coder-specific state machine for structured coding workflows
- **Architect State Machine** (`pkg/architect/`) - Architect-specific state machine for spec processing and coordination
- **Template System** (`pkg/templates/`) - Prompt templates for different workflow states
- **MCP Tool Integration** (`pkg/tools/`) - Model Context Protocol tools for file operations in workspaces

### Agent Flow
1. **Architect Workflow**: Processes development specifications through state machine:
   - **SPEC_PARSING**: Parse specification files into requirements using LLM or deterministic parser
   - **STORY_GENERATION**: Generate story files from requirements
   - **QUEUE_MANAGEMENT**: Load stories and manage dependencies
   - **DISPATCHING**: Assign ready stories to coding agents
   - **ANSWERING**: Handle technical questions from coding agents (QUESTION→ANSWER)
   - **REVIEWING**: Evaluate code submissions and provide approval/feedback (REQUEST→RESULT)

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

4. System maintains event logs and handles graceful shutdown with STATUS.md dumps

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

### Runtime Directories
- `config/` - Configuration files (config.json)
- `logs/` - Runtime event logs and debugging output
- `stories/` - Generated story files from specifications
- `work/` - Agent workspace directories with isolated state
- `tests/fixtures/` - Test input files and examples
- `docs/` - Documentation and style guides

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

## Configuration

The system uses JSON configuration with environment variable overrides:
- Config path via `CONFIG_PATH` env var or command flag
- Placeholder substitution: `${ENV_VAR}` in JSON
- Direct env override: any JSON key can be overridden by matching env var name
- Model-specific settings for rate limits and budgets

## Getting Help

If you get stuck, have questions, or need clarification on anything, use your get_help tool.