# Multi-Agent AI Coding System Orchestrator

A Go-based orchestrator that coordinates between Architect Agents (o3) and Coding Agents (Claude) to process development stories and implement code changes.

## Overview

This system implements a message-passing architecture where:
- **Architect agents** read development stories and create TASK messages
- **Coding agents** process tasks and return RESULT/ERROR/QUESTION messages
- The **orchestrator** manages agent communication with rate limiting and event logging

## Quick Start

### Building the System

```bash
# Build the orchestrator
make build

# Run tests
make test

# Lint code
make lint

# Start the orchestrator
make run
```

### Running Individual Agents with AgentCtl

The `agentctl` tool allows you to run individual agents in isolation for testing and development:

#### Basic Usage

```bash
# Run architect agent with a story file
./bin/agentctl run architect --input stories/001.md --mock

# Run claude agent with a task JSON
./bin/agentctl run claude --input task.json --mock

# Use live API calls (requires API keys)
./bin/agentctl run claude --input task.json --live

# Save output to file
./bin/agentctl run claude --input task.json --mock --output result.json
```

#### Agent Types

- **architect** - Process development stories and generate task messages
  - Input: Markdown story files (`.md`)
  - Output: TASK JSON messages with requirements and implementation details

- **claude** - Process coding tasks and generate implementations
  - Input: TASK JSON messages
  - Output: RESULT JSON with generated code and test results

#### Modes

- **--mock** - Use mock implementations (default, fast, no API calls)
- **--live** - Use real API calls (requires environment variables)

#### Environment Variables for Live Mode

```bash
# For Claude agent live mode
export ANTHROPIC_API_KEY="your-anthropic-api-key"

# For architect agent live mode (when implemented)
export OPENAI_API_KEY="your-openai-api-key"
```

#### Examples

```bash
# Generate a task from a health endpoint story
./bin/agentctl run architect --input stories/001.md --mock

# Process a coding task and generate implementation
./bin/agentctl run claude --input task.json --mock

# Use live API with output file
./bin/agentctl run claude --input task.json --live --output implementation.json
```

#### Sample Task JSON Format

For testing the Claude agent, create a task JSON file in the `tests/fixtures/` directory:

```json
{
  "id": "test_msg_001",
  "type": "TASK", 
  "from_agent": "architect",
  "to_agent": "claude",
  "timestamp": "2025-06-10T19:00:00Z",
  "payload": {
    "content": "Create a simple health endpoint that returns JSON with status and timestamp",
    "requirements": [
      "GET /health endpoint",
      "Return JSON response",
      "Include timestamp"
    ]
  }
}
```

## System Architecture

### Core Components

- **Task Dispatcher** (`pkg/dispatch/`) - Routes messages between agents with rate limiting
- **Agent Message Protocol** (`pkg/proto/`) - Structured communication via `AgentMsg`
- **Rate Limiting** (`pkg/limiter/`) - Token bucket per-model rate limiting with budget enforcement
- **Event Logging** (`pkg/eventlog/`) - Structured logging to `logs/events.jsonl`
- **Configuration** (`pkg/config/`) - JSON config with environment variable overrides
- **State Machine Driver** (`pkg/agent/`) - Phase 3 state machine for coding workflows
- **Template System** (`pkg/templates/`) - Prompt templates for different workflow states
- **MCP Tool Integration** (`pkg/tools/`) - Model Context Protocol tools for file operations

### Agent Flow

1. Architect agent reads development stories and creates TASK messages
2. Dispatcher routes tasks to appropriate coding agents with rate limiting
3. **Phase 3 State Machine**: Coding agents follow structured workflow:
   - **PLANNING**: Analyze requirements and create implementation plan
   - **CODING**: Generate code using MCP tools to create files in workspace
   - **TESTING**: Validate code with formatting, building, and tests
   - **AWAIT_APPROVAL**: Request review and approval
   - **DONE**: Complete the task
4. System maintains event logs and handles graceful shutdown

## Configuration

The system uses JSON configuration with environment variable overrides:

```json
{
  "models": {
    "claude_sonnet4": {
      "max_tokens_per_minute": 5000,
      "max_budget_per_day_usd": 50.0,
      "api_key": "${ANTHROPIC_API_KEY}",
      "agents": [
        {"name": "claude-coder", "id": "001", "type": "coder", "work_dir": "./work/claude"}
      ]
    },
    "openai_o3": {
      "max_tokens_per_minute": 2000,
      "max_budget_per_day_usd": 20.0, 
      "api_key": "${OPENAI_API_KEY}",
      "agents": [
        {"name": "architect", "id": "001", "type": "architect", "work_dir": "./work/architect"}
      ]
    }
  }
}
```

## Development Stories

The system follows story-driven development with ordered implementation stories:

- **Stories 001-012**: MVP implementation (completed)
- **Stories 013-019**: Phase 2 - Real LLM integrations and standalone testing tools

See `PROJECT.md` and `PHASE2.md` for detailed story specifications.

## Testing

```bash
# Run all tests
go test ./...

# Run end-to-end smoke test
go test -v . -run TestE2ESmokeTest

# Test individual agents (using files in tests/fixtures/)
./bin/agentctl run architect --input stories/001.md --mock
./bin/agentctl run claude --input tests/fixtures/test_task.json --mock

# Test live mode with workspace
./bin/agentctl run claude --input tests/fixtures/test_task.json --mode live --workdir ./work/tmp
```

## Directory Structure

```
orchestrator/
├── agents/          # Agent implementations
├── cmd/agentctl/    # Standalone agent runner CLI
├── config/          # Configuration files
├── docs/            # Documentation and style guide
├── logs/            # Runtime event logs (generated)
├── pkg/             # Core packages
│   ├── agent/       # Phase 3 state machine driver
│   ├── config/      # Configuration loader
│   ├── contextmgr/  # Context management for LLM conversations
│   ├── dispatch/    # Message routing and retry logic
│   ├── eventlog/    # Event logging system
│   ├── limiter/     # Rate limiting and budget tracking
│   ├── logx/        # Structured logging
│   ├── proto/       # Message protocol definitions
│   ├── state/       # State persistence for agents
│   ├── templates/   # Prompt templates for workflow states
│   ├── testkit/     # Testing utilities
│   └── tools/       # MCP tool implementations
├── status/          # Agent status reports (generated)
├── stories/         # Development story definitions
├── tests/           # Test files and fixtures
│   └── fixtures/    # Test input files (JSON, MD)
└── work/            # Agent workspaces (generated)
```

## License

See project documentation for license information.