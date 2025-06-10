# Agent Testing Guide

This guide shows how to test the orchestrator agents in both mock and live modes using the `agentctl` command-line tool.

## Prerequisites

### API Keys (for live mode)
Set up your API keys as environment variables:

```bash
export ANTHROPIC_API_KEY="your-anthropic-api-key-here"
export OPENAI_API_KEY="your-openai-api-key-here"
```

### Build the Tools
```bash
make agentctl  # Build the agent control CLI
```

## Understanding the Flags

- `--input`: Input file (JSON for Claude, Markdown for Architect)
- `--mode`: Execution mode (`live` for real APIs, `mock` for fake responses)
- `--workdir`: Agent workspace where code files are generated and tested
- `--output`: Where to save the final JSON result (default: stdout)

## Testing Claude Agent (Coding Agent)

### 1. Create a Task File

The task file tells Claude what to implement:

```json
{
  "id": "msg_test_001",
  "type": "TASK",
  "from_agent": "test",
  "to_agent": "claude_sonnet4:001",
  "timestamp": "2025-06-10T20:00:00.000000Z",
  "payload": {
    "content": "Create a simple HTTP server health endpoint in Go",
    "requirements": [
      "GET /health endpoint",
      "Return JSON response with status and timestamp",
      "Use port 8080",
      "Include proper error handling"
    ],
    "story_id": "test_001"
  },
  "metadata": {
    "test_type": "live_api_test"
  }
}
```

### 2. Test Commands

```bash
# Mock mode (fast, predictable)
./bin/agentctl run claude --input test_task.json --mode mock

# Live mode with temporary workspace (files cleaned up)
./bin/agentctl run claude --input test_task.json --mode live

# Live mode with persistent workspace (files kept for inspection)
mkdir -p ./claude-workspace
./bin/agentctl run claude \
  --input test_task.json \
  --mode live \
  --workdir ./claude-workspace

# Live mode with workspace and result file
./bin/agentctl run claude \
  --input test_task.json \
  --mode live \
  --workdir ./claude-workspace \
  --output claude_result.json
```

### 3. Inspect Generated Code

When using `--workdir`, you can examine what Claude generated:

```bash
# List generated files
ls -la ./claude-workspace/

# View the generated Go code
cat ./claude-workspace/*.go

# Check if there were compilation errors
cd ./claude-workspace
go fmt .
go build .
```

## Testing Architect Agent (Design Agent)

### 1. Create a Story File

The story file describes what you want to build:

```markdown
# Test Story - User Authentication System

## Description
Implement a basic user authentication system with login and registration functionality.

## Requirements
- User registration with email and password
- User login with session management
- Password hashing for security
- Basic input validation
- JSON API endpoints

## Acceptance Criteria
- POST /register endpoint accepts email and password
- POST /login endpoint returns session token
- Passwords are hashed before storage
- Invalid inputs return proper error messages
- Session tokens expire after 24 hours

## Estimation
This should take approximately 2-3 hours to implement and test.
```

### 2. Test Commands

```bash
# Mock mode
./bin/agentctl run architect --input test_story.md --mode mock

# With persistent workspace
mkdir -p ./architect-workspace
./bin/agentctl run architect \
  --input test_story.md \
  --mode mock \
  --workdir ./architect-workspace

# With result file
./bin/agentctl run architect \
  --input test_story.md \
  --mode mock \
  --workdir ./architect-workspace \
  --output architect_result.json
```

Note: Live mode for architect requires OpenAI integration (Story 013) which is not yet implemented.

## Common Test Scenarios

### 1. Compare Mock vs Live Outputs

```bash
# Run mock version
./bin/agentctl run claude \
  --input test_task.json \
  --mode mock \
  --workdir ./mock-workspace \
  --output mock_result.json

# Run live version
./bin/agentctl run claude \
  --input test_task.json \
  --mode live \
  --workdir ./live-workspace \
  --output live_result.json

# Compare results
diff mock_result.json live_result.json
diff ./mock-workspace/ ./live-workspace/
```

### 2. Debug Failed Tests

When you see errors like "Tests failed: go fmt failed", the workspace helps debug:

```bash
# Run with persistent workspace
./bin/agentctl run claude \
  --input test_task.json \
  --mode live \
  --workdir ./debug-workspace

# Manually run the same commands that failed
cd ./debug-workspace
go fmt .          # See formatting issues
go build .        # See compilation errors
make test         # If there's a Makefile
```

### 3. Iterative Development

```bash
# Create a dedicated workspace for experimentation
mkdir -p ./experiment-workspace

# Run multiple tasks, keeping all outputs
./bin/agentctl run claude \
  --input task1.json \
  --mode live \
  --workdir ./experiment-workspace/task1

./bin/agentctl run claude \
  --input task2.json \
  --mode live \
  --workdir ./experiment-workspace/task2

# Compare approaches
diff -r ./experiment-workspace/task1 ./experiment-workspace/task2
```

## Sample Test Files

### Basic Health Endpoint Task
```json
{
  "id": "msg_health_test",
  "type": "TASK",
  "from_agent": "test",
  "to_agent": "claude_sonnet4:001",
  "timestamp": "2025-06-10T20:00:00.000000Z",
  "payload": {
    "content": "Create a health endpoint that returns server status",
    "requirements": [
      "GET /health endpoint",
      "Return 200 OK with JSON response",
      "Include timestamp and status fields"
    ],
    "story_id": "health_001"
  }
}
```

### REST API Task
```json
{
  "id": "msg_rest_test",
  "type": "TASK",
  "from_agent": "test",
  "to_agent": "claude_sonnet4:001",
  "timestamp": "2025-06-10T20:00:00.000000Z",
  "payload": {
    "content": "Create a REST API for managing books",
    "requirements": [
      "CRUD operations for books (Create, Read, Update, Delete)",
      "JSON request/response format",
      "Proper HTTP status codes",
      "Input validation",
      "In-memory storage"
    ],
    "story_id": "books_api_001"
  }
}
```

### Database Integration Task
```json
{
  "id": "msg_db_test",
  "type": "TASK",
  "from_agent": "test",
  "to_agent": "claude_sonnet4:001",
  "timestamp": "2025-06-10T20:00:00.000000Z",
  "payload": {
    "content": "Create a user service with database persistence",
    "requirements": [
      "PostgreSQL database connection",
      "User model with CRUD operations",
      "Connection pooling",
      "Migration scripts",
      "Environment-based configuration"
    ],
    "story_id": "user_service_001"
  }
}
```

## Expected Outputs

### Claude Agent Result
```json
{
  "id": "msg_result_001",
  "type": "RESULT",
  "from_agent": "claude_sonnet4:001",
  "to_agent": "test",
  "timestamp": "2025-06-10T20:01:30.000000Z",
  "payload": {
    "status": "completed",
    "implementation": "package main\n\nimport (\n\t\"encoding/json\"\n\t\"net/http\"\n\t\"time\"\n)\n\n...",
    "test_results": {
      "success": true,
      "output": "All checks passed: go fmt, go build completed successfully",
      "elapsed": "150ms"
    }
  },
  "parent_msg_id": "msg_test_001"
}
```

### Architect Agent Result
```json
{
  "id": "msg_arch_result_001",
  "type": "TASK",
  "from_agent": "architect",
  "to_agent": "claude_sonnet4:001",
  "timestamp": "2025-06-10T20:01:15.000000Z",
  "payload": {
    "content": "Implement user authentication system with the following components...",
    "requirements": [
      "Create User model with email and password fields",
      "Implement password hashing using bcrypt",
      "Create POST /register endpoint",
      "Create POST /login endpoint with JWT tokens",
      "Add input validation middleware"
    ],
    "story_id": "auth_system_001"
  }
}
```

## Troubleshooting

### Common Issues

1. **API Key Missing**
   ```
   Error: ANTHROPIC_API_KEY environment variable required for live mode
   ```
   Solution: Set your API key: `export ANTHROPIC_API_KEY="your-key"`

2. **Go Format Failures**
   ```
   Tests failed: go fmt failed: exit status 1
   ```
   Solution: Use `--workdir` to inspect generated code and fix formatting issues

3. **Build Failures**
   ```
   Tests failed: go build failed: exit status 2
   ```
   Solution: Check generated code in workspace for syntax errors or missing imports

4. **Permission Errors**
   ```
   failed to create work directory: permission denied
   ```
   Solution: Ensure the parent directory exists and is writable

### Getting Help

```bash
# Show usage information
./bin/agentctl run claude --help

# Show all available commands
./bin/agentctl --help
```

## Integration with Main Orchestrator

The standalone `agentctl` tool is perfect for:
- Testing individual agents in isolation
- Debugging agent behavior
- Developing new prompts and requirements
- Comparing mock vs live API responses

For full multi-agent orchestration, use the main orchestrator binary which coordinates between architect and coding agents automatically.