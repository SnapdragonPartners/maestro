# Claude Code Integration Specification

## Document Status
- **Status**: Draft (Revised)
- **Created**: 2025-01-07
- **Last Updated**: 2025-12-04
- **Version**: 2.1

## Overview

This specification describes the integration of Claude Code as an alternative coder implementation within the Maestro orchestration system. When enabled, Claude Code runs as a subprocess during PLANNING and CODING states, leveraging Anthropic's highly optimized toolsets and prompts while Maestro handles orchestration, architect coordination, testing, and merging.

### Goals

1. **Leverage Claude Code Optimizations**: Use Claude Code's optimized tools (Bash, Read, Write, Edit, Glob, Grep) and prompts for planning and coding phases
2. **Preserve Orchestration Value**: Maintain Maestro's multi-agent architecture, parallel coders, architect review cycles, and workflow management
3. **Configuration-Based Selection**: Allow users to choose between standard LLM mode and Claude Code mode via configuration
4. **Keep Existing Coder as Default**: Standard coder implementation remains unchanged and is the default
5. **DRY Implementation**: Reuse existing coder infrastructure (communication channels, testing, merging, state persistence)

### Non-Goals

1. Replacing the entire coder agent with Claude Code (only PLANNING and CODING states)
2. Replacing architect agent with Claude Code
3. Removing or deprecating the standard coder implementation
4. Human-in-the-loop interactive mode with Claude Code

## Architecture

### High-Level Design

```
Story Assignment
       ↓
┌──────────────────────────────────────────────────────────┐
│                    Existing Coder Agent                   │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ SETUP → PLANNING → PLAN_REVIEW → CODING → TESTING   │ │
│  │         ↑                        ↑                   │ │
│  │    [Claude Code]            [Claude Code]            │ │
│  │    subprocess               subprocess               │ │
│  └─────────────────────────────────────────────────────┘ │
│  CODE_REVIEW → PREPARE_MERGE → AWAIT_MERGE → DONE        │
│  (existing infrastructure, unchanged)                     │
└──────────────────────────────────────────────────────────┘
```

### Mode Selection

The coder operates in one of two modes based on configuration:

| Mode | PLANNING | CODING | Other States |
|------|----------|--------|--------------|
| `standard` (default) | LLM + MCP tools | LLM + MCP tools | Existing impl |
| `claude-code` | Claude Code subprocess | Claude Code subprocess | Existing impl |

### Component Architecture

```
maestro/
├── cmd/
│   └── maestro-mcp-proxy/         # MCP stdio-to-TCP proxy (runs in container)
│       └── main.go                # ~50 lines, forwards stdio ↔ TCP
│
├── pkg/
│   ├── coder/
│   │   ├── driver.go              # Existing coder - add mode branching
│   │   ├── planning.go            # Standard planning (unchanged)
│   │   ├── coding.go              # Standard coding (unchanged)
│   │   ├── claudecode_planning.go # Claude Code planning handler
│   │   ├── claudecode_coding.go   # Claude Code coding handler
│   │   ├── claude/                # Claude Code integration sub-package
│   │   │   ├── runner.go          # Execute Claude Code, start/stop MCP server
│   │   │   ├── installer.go       # Auto-install Node.js/npm/Claude Code
│   │   │   ├── parser.go          # Stream-JSON output parsing
│   │   │   ├── signals.go         # Signal detection from tool calls
│   │   │   ├── timeout.go         # Inactivity and total timeout management
│   │   │   └── mcpserver/         # MCP server sub-package
│   │   │       ├── server.go      # TCP JSON-RPC 2.0 server
│   │   │       └── server_test.go # Unit tests for MCP server
│   │   └── [other files]          # Unchanged (testing, merge, etc.)
│   │
│   ├── exec/
│   │   └── docker_long_running.go # Container execution (no MCP mount needed)
│   │
│   ├── templates/
│   │   └── claude/                # Claude Code prompt templates
│   │       ├── planning.go        # Planning phase template
│   │       └── coding.go          # Coding phase template
│   │
│   └── config/
│       └── config.go              # Add CoderMode to AgentConfig
│
├── pkg/dockerfiles/
│   └── bootstrap.dockerfile       # Includes maestro-mcp-proxy binary
```

### Process Architecture

Each coder agent manages its own independent Claude Code process:

```
Orchestrator Process
    │
    ├── Coder-001 (agent in orchestrator)
    │       │
    │       └── executor.Run("claude --print ...")
    │               ↓
    │           Container-001 (ANTHROPIC_API_KEY injected)
    │           └── Claude Code subprocess
    │               └── Reads/writes /workspace
    │
    ├── Coder-002 (agent in orchestrator)
    │       │
    │       └── executor.Run("claude --print ...")
    │               ↓
    │           Container-002 (ANTHROPIC_API_KEY injected)
    │           └── Claude Code subprocess
    │               └── Reads/writes /workspace
    │
    └── ... (up to max_coders concurrent agents)
```

**Key characteristics:**
- Claude Code runs **inside the container** via the existing executor infrastructure
- Each coder has an independent Claude Code process (supports parallelism)
- Coder agent parses stdout (stream-json format) for completion signals
- ANTHROPIC_API_KEY is injected as environment variable into the container
- Maestro tools exposed via MCP (Model Context Protocol) over TCP

### MCP Tool Integration Architecture

Claude Code needs to call Maestro tools (like `maestro_submit_plan`, `shell`, `container_build`) that execute on the host. This is implemented using TCP and a stdio proxy:

**Why TCP instead of Unix sockets?**
Unix sockets don't work through Docker Desktop's file sharing on macOS (gRPC-FUSE limitation returns "Not supported"). TCP via `host.docker.internal` works on both macOS and Linux, making it the cross-platform solution.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ HOST                                                                         │
│                                                                              │
│  ┌──────────────────┐         ┌──────────────────────────────────────────┐  │
│  │ Maestro Coder    │         │ MCP Server (per agent)                   │  │
│  │                  │         │                                          │  │
│  │  - Starts MCP    │────────▶│  Listens: 127.0.0.1:<dynamic-port>       │  │
│  │    server        │         │                                          │  │
│  │  - Runs Claude   │         │  Tools: shell, container_build,          │  │
│  │    Code via exec │         │         maestro_submit_plan, etc.        │  │
│  └──────────────────┘         └──────────────────────────────────────────┘  │
│                                              ▲                               │
│                                              │ TCP                           │
│                                              │                               │
│              host.docker.internal ───────────┘                               │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
                                               │
┌──────────────────────────────────────────────│───────────────────────────────┐
│ CONTAINER                                    ▼                               │
│                                                                              │
│  ┌──────────────────┐         ┌──────────────────────────────────────────┐  │
│  │ Claude Code      │◀───────▶│ maestro-mcp-proxy (stdio binary)         │  │
│  │                  │  stdio  │                                          │  │
│  │  --mcp-config    │         │  Connects: host.docker.internal:<port>   │  │
│  │  {maestro:...}   │         │  Forwards stdio ↔ TCP                    │  │
│  └──────────────────┘         └──────────────────────────────────────────┘  │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Components:**

1. **MCP Server** (`pkg/coder/claude/mcpserver/server.go`)
   - Runs on the HOST, listens on TCP at `127.0.0.1:<dynamic-port>`
   - Port is dynamically assigned by the OS (`:0`)
   - Implements JSON-RPC 2.0 protocol (MCP standard)
   - Delegates tool calls to Maestro's existing `ToolProvider`
   - One server per agent (supports concurrent agents on different ports)

2. **MCP Proxy** (`cmd/maestro-mcp-proxy/main.go`)
   - Tiny binary (~50 lines) that runs INSIDE the container
   - Forwards stdio from Claude Code to the TCP connection
   - Pre-installed in bootstrap container at `/usr/local/bin/maestro-mcp-proxy`
   - Uses `host.docker.internal` to reach host from container

3. **Docker Host DNS**
   - `host.docker.internal` resolves to host from within containers
   - Available by default on Docker Desktop (macOS/Windows)
   - On Linux, add `--add-host=host.docker.internal:host-gateway` to container

**Flow:**
1. Coder starts MCP server on host before running Claude Code (binds to dynamic port)
2. Claude Code launched with `--mcp-config` pointing to proxy with host:port
3. Claude Code makes tool call → proxy forwards via TCP → server executes tool → result returns
4. MCP server stopped when Claude Code exits

### Container Prerequisites

Claude Code requires Node.js/npm. The runner auto-installs both if missing:

```
Container startup flow:
1. Check if `claude` binary exists (which claude)
2. If missing:
   a. Check if npm is available (which npm)
   b. If npm missing, install Node.js/npm:
      - Try: apt-get update && apt-get install -y nodejs npm
      - If apt fails, try: apk add --no-cache nodejs npm
      - If apk fails, try: yum install -y nodejs npm || dnf install -y nodejs npm
   c. Install Claude Code: npm install -g @anthropic-ai/claude-code
   d. Verify: claude --version
3. If all installation attempts fail → Fatal error
4. Proceed with Claude Code execution
```

**Container requirements:**
- Root or sudo access for package installation (typical in containers)
- Network access for initial installation
- Write access to npm global directory

**Recommended**: Pre-install in the base container image to avoid installation delay:
```dockerfile
# In project Dockerfile
RUN apt-get update && apt-get install -y nodejs npm \
    && npm install -g @anthropic-ai/claude-code
```

**Note**: First-run installation adds ~30-60 seconds. Subsequent runs use cached installation.

### MCP Proxy Distribution

The `maestro-mcp-proxy` binary must be present in every container that runs Claude Code. This section describes how the proxy is distributed.

#### Problem

- The proxy is a Go binary that runs inside containers
- Custom user containers won't have it pre-installed
- We want a single `maestro` binary distribution (no separate files to manage)
- Need to support both linux/arm64 (Apple Silicon Docker) and linux/amd64

#### Solution: Embedded Binary Distribution

The proxy binaries are cross-compiled at build time and embedded in the maestro binary using Go's `//go:embed` directive:

```
maestro (main binary)
├── embedded/proxy-linux-arm64  (~2.5MB)
└── embedded/proxy-linux-amd64  (~2.5MB)
```

**Build Process:**
```makefile
# Cross-compile MCP proxy for both architectures
build-mcp-proxy:
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o pkg/coder/claude/embedded/proxy-linux-arm64 ./cmd/maestro-mcp-proxy
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o pkg/coder/claude/embedded/proxy-linux-amd64 ./cmd/maestro-mcp-proxy
```

**Embed Package** (`pkg/coder/claude/embedded/proxy.go`):
```go
package embedded

import (
    _ "embed"
    "fmt"
)

//go:embed proxy-linux-arm64
var proxyLinuxArm64 []byte

//go:embed proxy-linux-amd64
var proxyLinuxAmd64 []byte

// GetProxyBinary returns the proxy binary for the given architecture.
// arch should be the output of `uname -m`: "aarch64" or "x86_64"
func GetProxyBinary(arch string) ([]byte, error) {
    switch arch {
    case "aarch64", "arm64":
        return proxyLinuxArm64, nil
    case "x86_64", "amd64":
        return proxyLinuxAmd64, nil
    default:
        return nil, fmt.Errorf("unsupported architecture: %s", arch)
    }
}
```

#### Runtime Flow

The `Installer.EnsureMCPProxy()` method handles proxy installation:

```
1. Check if /usr/local/bin/maestro-mcp-proxy exists in container
   → If yes: Done (already installed)

2. Detect container architecture:
   → Run: uname -m
   → Returns: "aarch64" (ARM) or "x86_64" (AMD)

3. Get embedded binary for architecture:
   → embedded.GetProxyBinary(arch)

4. Write binary to temp file on host:
   → /tmp/maestro-mcp-proxy-<random>

5. Copy into container:
   → docker cp /tmp/maestro-mcp-proxy-<random> <container>:/usr/local/bin/maestro-mcp-proxy

6. Make executable:
   → docker exec <container> chmod +x /usr/local/bin/maestro-mcp-proxy

7. Clean up temp file on host
```

**Benefits:**
- Single `maestro` binary - no separate files to distribute
- Works with any Linux container (ARM64 or AMD64)
- Automatic architecture detection
- Binary only copied once per container (cached check)

**Trade-offs:**
- Adds ~5MB to maestro binary size (2.5MB × 2 architectures)
- Requires rebuild of maestro when proxy changes

#### Future: OCI Side-car Distribution

For users who want to pre-install the proxy in their Dockerfiles (avoiding runtime copy), we can publish an OCI image:

```dockerfile
# Future: Users can add this to their Dockerfile
COPY --from=ghcr.io/anthropics/maestro-mcp-proxy:v1 /proxy /usr/local/bin/maestro-mcp-proxy
```

**Implementation** (via goreleaser):
```yaml
# .goreleaser.yaml (future addition)
dockers:
  - image_templates:
      - "ghcr.io/anthropics/maestro-mcp-proxy:{{ .Version }}"
    dockerfile: Dockerfile.proxy
    build_flag_templates:
      - "--platform=linux/arm64,linux/amd64"
```

This approach:
- Versioned independently from maestro
- Multi-arch manifest for automatic platform selection
- Users can pin to specific versions
- Eliminates runtime copy overhead for prepared containers

**Note**: The OCI distribution is planned for future releases. Currently, the embedded approach is the primary distribution method.

## Design Principles

### 1. Minimal Coder Modification

The existing `Coder` struct in `driver.go` gains mode awareness with minimal changes:

```go
// In driver.go - add to Coder struct
type Coder struct {
    // ... existing fields ...

    // Claude Code mode (nil when mode is "standard")
    claudeCodeManager *claudecode.Manager
}

// Mode branching in state handlers
func (c *Coder) handlePlanning(ctx context.Context) (proto.State, bool, error) {
    if c.claudeCodeManager != nil {
        return c.handleClaudeCodePlanning(ctx)  // New file: claudecode_planning.go
    }
    return c.handleStandardPlanning(ctx)  // Existing: planning.go
}

func (c *Coder) handleCoding(ctx context.Context) (proto.State, bool, error) {
    if c.claudeCodeManager != nil {
        return c.handleClaudeCodeCoding(ctx)  // New file: claudecode_coding.go
    }
    return c.handleStandardCoding(ctx)  // Existing: coding.go
}
```

### 2. Claude Code Execution via Executor

Claude Code runs inside the coder's container using the existing executor infrastructure:

**Launch Command:**
```bash
claude \
  --print \
  --output-format stream-json \
  --input-format stream-json \
  --dangerously-skip-permissions \
  --append-system-prompt "$(cat /tmp/maestro-prompt.md)" \
  --model "$CODER_MODEL"
```

**Key flags:**
- `--print` - Non-interactive mode, output to stdout
- `--output-format stream-json` - Streaming JSONL for real-time parsing
- `--input-format stream-json` - JSONL input for multi-turn conversations
- `--dangerously-skip-permissions` - Bypass permission prompts (container is sandboxed)
- `--append-system-prompt` - **Preserves Claude Code's optimized defaults** while adding Maestro context
- `--model` - Uses `coder_model` from config (must be Anthropic model)

**Container Lifecycle Alignment:**
- PLANNING: Container with read-only workspace mount → Claude Code explores codebase
- CODING: Container with read-write workspace mount → Claude Code implements changes

### 3. Signal Detection via Tool Call Parsing

Claude Code signals completion by calling "virtual" maestro tools defined in the appended system prompt. The coder agent parses stream-json output to detect these tool calls:

| Tool Call | Purpose | Detected Signal |
|-----------|---------|-----------------|
| `maestro_submit_plan` | Submit plan for architect review | `PLAN_COMPLETE` |
| `maestro_done` | Signal implementation complete | `DONE` |
| `maestro_ask_question` | Ask architect for guidance | `QUESTION` |
| `maestro_mark_complete` | Story already implemented | `STORY_COMPLETE` |

**How it works:**
1. System prompt defines maestro_* tools with specific schemas
2. Claude Code calls these tools like any other tool
3. Coder agent parses stdout for tool_use blocks matching `maestro_*`
4. Signal is extracted and state transition occurs
5. Tool result is injected back via stdin if conversation continues

## Component Specifications

### 1. Claude Code Runner

**File**: `pkg/coder/claude/runner.go`

Executes Claude Code via the existing container executor infrastructure:

```go
package claude

type Runner struct {
    executor exec.Executor
    logger   *logx.Logger
}

type RunOptions struct {
    Mode             Mode              // PLANNING or CODING
    WorkDir          string            // Container workspace path
    Model            string            // Anthropic model (from coder_model config)
    SystemPrompt     string            // Appended system prompt with maestro tools
    InitialInput     string            // Story content or approved plan
    EnvVars          map[string]string // ANTHROPIC_API_KEY, etc.
    TotalTimeout     time.Duration     // Max time for entire run (default: 5m)
    InactivityTimeout time.Duration    // Max time without output (default: 1m)
}

type Mode string

const (
    ModePlanning Mode = "PLANNING"
    ModeCoding   Mode = "CODING"
)

// EnsureInstalled checks if Claude Code is installed and installs it if needed
func (r *Runner) EnsureInstalled(ctx context.Context) error {
    // 1. Check if claude binary exists: which claude
    // 2. If not found, install via npm: npm install -g @anthropic-ai/claude-code
    // 3. Verify installation: claude --version
    // 4. Return error if installation fails
}

// Run executes Claude Code and returns when a terminal signal is detected
func (r *Runner) Run(ctx context.Context, opts RunOptions) (*Result, error) {
    // 0. Ensure Claude Code is installed
    // 1. Build command with flags
    // 2. Execute via container executor
    // 3. Parse stream-json output for signals
    // 4. Handle maestro_ask_question by pausing and resuming
    // 5. Return result with signal and extracted data
}

type Result struct {
    Signal Signal
    Plan   string // For PLAN_COMPLETE
    Summary string // For DONE
    Reason string // For STORY_COMPLETE
    Question *Question // For QUESTION (needs answer injection)
}

type Signal string

const (
    SignalPlanComplete  Signal = "PLAN_COMPLETE"
    SignalDone          Signal = "DONE"
    SignalQuestion      Signal = "QUESTION"
    SignalStoryComplete Signal = "STORY_COMPLETE"
    SignalError         Signal = "ERROR"
)
```

### 2. Stream-JSON Parser

**File**: `pkg/coder/claude/parser.go`

Parses Claude Code's stream-json output to detect maestro tool calls:

```go
package claude

type Parser struct {
    logger *logx.Logger
}

// StreamEvent represents a single line of stream-json output
type StreamEvent struct {
    Type      string          `json:"type"`      // "assistant", "tool_use", "tool_result", etc.
    Content   json.RawMessage `json:"content"`
    ToolName  string          `json:"name"`      // For tool_use events
    ToolInput json.RawMessage `json:"input"`     // For tool_use events
}

// ParseLine parses a single JSONL line
func (p *Parser) ParseLine(line []byte) (*StreamEvent, error)

// IsMaestroToolCall checks if event is a maestro_* tool call
func (p *Parser) IsMaestroToolCall(event *StreamEvent) bool

// ExtractSignal extracts signal and data from maestro tool call
func (p *Parser) ExtractSignal(event *StreamEvent) (Signal, map[string]any, error)
```

### 3. Maestro MCP Tools

The maestro tools are exposed as real MCP tools via the socket-based MCP server. Claude Code discovers and calls them like any other MCP tool.

**Signal Tools** (detected by signal detector to trigger state transitions):

| Tool | Purpose | Signal |
|------|---------|--------|
| `maestro_submit_plan` | Submit plan for architect review | `PLAN_COMPLETE` |
| `maestro_done` | Signal implementation complete | `DONE` |
| `maestro_ask_question` | Ask architect for guidance | `QUESTION` |
| `maestro_mark_complete` | Story already implemented | `STORY_COMPLETE` |

**Execution Tools** (delegated to existing ToolProvider):

| Tool | Purpose |
|------|---------|
| `shell` | Execute shell commands |
| `container_build` | Build Docker images |
| `container_test` | Test in temporary container |
| `read_file` | Read file contents |
| `write_file` | Write file contents |
| ... | All tools from `pkg/tools/` |

**Implementation:**
- Tools are registered in `pkg/tools/` using the standard `ToolProvider` pattern
- MCP server (`pkg/coder/claude/mcpserver/server.go`) exposes them via JSON-RPC 2.0
- Signal detector (`pkg/coder/claude/signals.go`) parses stdout for `maestro_*` tool calls
- Tool results are synchronous - Claude Code waits for execution to complete

### 4. Prompt Templates

**File**: `pkg/templates/claude/planning.go`

```go
package claude

const PlanningTemplate = `# PLANNING MODE

You are implementing a development story within the Maestro multi-agent system. Your task is to analyze the story and create a detailed implementation plan.

## Story

{{.TaskContent}}

## Context

- **Workspace**: {{.WorkDir}}
- **Mode**: Read-only filesystem (exploration only)
- **Branch**: {{.BranchName}}

## Maestro Integration Tools

You have access to the following tools to communicate with the Maestro orchestration system:

### maestro_submit_plan
Submit your implementation plan for architect review.
- plan (required): Your detailed implementation plan
- confidence: low | medium | high

### maestro_ask_question
Ask the architect for clarification. Your work will pause until answered.
- question (required): Your question
- context (required): Context about why you're asking

### maestro_mark_complete
Signal that story requirements are already implemented (no work needed).
- reason (required): Why no work is needed, with code references

## Git Guidelines

- You are on a feature branch
- DO NOT: switch branches, merge, rebase, reset
- Commits are allowed for exploration notes

## Instructions

1. Explore the codebase thoroughly using your standard tools
2. Ask questions if requirements are unclear
3. When ready, call maestro_submit_plan with your detailed plan
4. If story is already complete, call maestro_mark_complete

**Remember**: Read-only mode - focus on analysis and planning.
`
```

**File**: `pkg/templates/claude/coding.go`

```go
package claude

const CodingTemplate = `# CODING MODE

You are implementing an approved plan within the Maestro multi-agent system.

## Approved Plan

{{.Plan}}

## Context

- **Workspace**: {{.WorkDir}}
- **Mode**: Read-write filesystem (full implementation access)
- **Branch**: {{.BranchName}}

## Maestro Integration Tools

### maestro_done
Signal that implementation is complete and ready for testing.
- summary (required): Brief summary of what was implemented

### maestro_ask_question
Ask the architect for clarification. Your work will pause until answered.
- question (required): Your question
- context (required): Context about why you're asking

## Git Guidelines

- Commit frequently with clear messages
- DO NOT: switch branches, merge, rebase, reset

## Testing

- Run tests as you implement
- Ensure tests pass before calling maestro_done
- Fix linting and build errors

## Instructions

1. Implement your plan step by step
2. Test your changes as you go
3. Commit work incrementally
4. When complete and tested, call maestro_done

**Remember**: Full read-write access - implement thoroughly and test carefully.
`
```

### 5. Configuration

**Update to `pkg/config/config.go`:**

```go
// AgentConfig defines which models to use and concurrency limits.
type AgentConfig struct {
    MaxCoders      int              `json:"max_coders"`
    CoderModel     string           `json:"coder_model"`     // Model for coder (used by both modes)
    CoderMode      string           `json:"coder_mode"`      // NEW: "standard" (default) or "claude-code"
    ArchitectModel string           `json:"architect_model"`
    PMModel        string           `json:"pm_model"`
    Metrics        MetricsConfig    `json:"metrics"`
    Resilience     ResilienceConfig `json:"resilience"`
    StateTimeout   time.Duration    `json:"state_timeout"`
}

// GetCoderMode returns the coder mode with default
func (c *AgentConfig) GetCoderMode() string {
    if c.CoderMode == "" {
        return "standard"
    }
    return c.CoderMode
}

// ValidateCoderMode validates that coder_mode and coder_model are compatible
func (c *AgentConfig) ValidateCoderMode() error {
    if c.GetCoderMode() == "claude-code" {
        model := c.CoderModel
        if !isAnthropicModel(model) {
            return fmt.Errorf("coder_mode 'claude-code' requires an Anthropic model, got: %s", model)
        }
    }
    return nil
}

func isAnthropicModel(model string) bool {
    // Check if model is an Anthropic model (claude-*, sonnet, opus, haiku)
    return strings.HasPrefix(model, "claude-") ||
           model == "sonnet" || model == "opus" || model == "haiku"
}
```

**Example Configuration:**

```json
{
  "agents": {
    "max_coders": 3,
    "coder_model": "claude-sonnet-4-20250514",
    "coder_mode": "claude-code",
    "architect_model": "o3"
  }
}
```

**Note:** When `coder_mode` is `"claude-code"`, the `coder_model` is passed to Claude Code's `--model` flag. The model must be an Anthropic model (claude-*, sonnet, opus, haiku). If using standard mode, any supported model works.

## Workflow

### PLANNING State with Claude Code

```
1. Coder receives story, transitions to PLANNING
2. Container started with read-only workspace mount + ANTHROPIC_API_KEY

3. Coder executes Claude Code via executor:
   - Renders planning template with story content
   - Runs: claude --print --output-format stream-json --append-system-prompt "..."
   - Parses stream-json stdout for tool calls

4. Claude Code explores codebase using its optimized tools
   - Uses Glob, Grep, Read to understand code
   - Uses Bash for build commands, tree, etc.

5. If clarification needed:
   - Claude Code calls maestro_ask_question (parsed from stdout)
   - Coder transitions to QUESTION state
   - Architect provides answer
   - Coder injects answer via stdin and resumes Claude Code
   - Claude Code continues planning

6. When ready:
   - Claude Code calls maestro_submit_plan with detailed plan
   - Coder parses PLAN_COMPLETE signal from stdout
   - Coder captures plan, transitions to PLAN_REVIEW

7. Claude Code process exits, container stopped
```

### CODING State with Claude Code

```
1. Plan approved by architect, coder transitions to CODING
2. Container started with read-write workspace mount + ANTHROPIC_API_KEY

3. Coder executes Claude Code via executor:
   - Renders coding template with approved plan
   - Runs: claude --print --output-format stream-json --append-system-prompt "..."
   - Parses stream-json stdout for tool calls

4. Claude Code implements plan using its optimized tools
   - Uses Write, Edit for file changes
   - Uses Bash for tests, builds, git commits

5. If issues encountered:
   - Claude Code calls maestro_ask_question
   - Same flow as planning (QUESTION state)

6. When complete:
   - Claude Code calls maestro_done with summary
   - Coder parses DONE signal from stdout
   - Coder captures summary, transitions to TESTING

7. Claude Code process exits, container stopped
8. Existing TESTING state runs (unchanged)
```

### Known Limitations

When using Claude Code mode:
- **BUDGET_REVIEW state is not used** - Claude Code manages its own token usage internally
- **Less granular iteration control** - Uses timeout-based limits instead of iteration counts
- Standard mode retains full budget control if needed

### Stall Detection

If Claude Code runs without calling maestro tools (stall), we detect and handle it:

```
Stall detection mechanisms:
1. Total timeout (default: 5 minutes per state)
   - Configurable via config
   - Kills process and transitions to ERROR if exceeded

2. Inactivity timeout (default: 1 minute)
   - If no stdout output for 1 minute, assume stalled
   - Kills process and attempts restart

3. Response counting (soft limit)
   - Track number of assistant responses
   - Log warning at 20 responses without maestro tool call
   - This is informational only (no hard limit)
```

**Stall handling:**
- First stall → Kill process, attempt restart with reminder in prompt
- Second stall → Transition to ERROR state, story requeued

**Restart prompt injection:**
```
IMPORTANT: You must call one of the maestro tools to complete this phase:
- maestro_submit_plan (for planning)
- maestro_done (for coding)
- maestro_ask_question (if you need clarification)
Your previous session was terminated due to inactivity. Please proceed to completion.
```

## Error Handling

### Error Categories

**Fatal Errors** (transition to ERROR state):
- All installation attempts fail (no apt/apk/yum available or all fail)
- Claude Code installation fails after Node.js/npm installed
- Container start failure
- ANTHROPIC_API_KEY not available
- Second failure after restart attempt

**Recoverable Errors** (attempt one restart):
- Claude Code process crash (non-zero exit)
- Stream parsing errors
- Inactivity timeout (no output for 5 minutes)
- Stall detection (no maestro tool call after extended activity)

**Normal Conditions** (handle gracefully):
- Tool execution failures (Claude Code handles internally)
- Architect timeout on question (return timeout message, continue)
- Plan/code rejection (handled by existing state machine)

### Restart Logic

```go
func (c *Coder) handleClaudeCodeError(err error) (proto.State, bool, error) {
    restartAttempted := c.GetStateValue(KeyClaudeCodeRestartAttempted, false)

    if restartAttempted {
        c.logger.Error("Claude Code failed after restart: %v", err)
        return proto.StateError, false, err
    }

    c.logger.Warn("Claude Code error, attempting restart: %v", err)
    c.SetStateData(KeyClaudeCodeRestartAttempted, true)

    // Retry current state - runner will re-execute Claude Code
    return c.GetCurrentState(), false, nil
}
```

## Implementation Plan

### Phase 1: Foundation (2-3 days)

**Goal**: Claude Code execution via executor and stream-json parsing

**Tasks**:
1. Create `pkg/coder/claude/` sub-package
   - `runner.go` - Execute Claude Code via container executor
   - `installer.go` - Auto-install Node.js/npm/Claude Code if missing
   - `parser.go` - Stream-JSON output parsing
   - `signals.go` - Signal detection from tool calls
   - `timeout.go` - Stall detection (inactivity + total timeout)

2. Basic integration test
   - Execute Claude Code in test container
   - Test auto-installation flow
   - Parse stream-json output
   - Verify signal detection works
   - Test timeout/stall handling

**Deliverables**:
- Runner can execute Claude Code via executor
- Auto-installation works when Node.js/npm/Claude Code is missing
- Parser extracts tool calls from stream-json
- Signal detection identifies maestro_* calls
- Stall detection triggers restart on inactivity

### Phase 2: Coder Integration (3-4 days)

**Goal**: Wire Claude Code into coder state machine

**Tasks**:
1. Add mode support to config
   - `coder_mode` field in AgentConfig
   - `ValidateCoderMode()` for Anthropic model check
   - Defaults to "standard"

2. Create coder handlers
   - `claudecode_planning.go` - PLANNING state with Claude Code
   - `claudecode_coding.go` - CODING state with Claude Code
   - Mode branching in `driver.go`

3. Create prompt templates
   - `pkg/templates/claude/planning.go`
   - `pkg/templates/claude/coding.go`

4. Handle QUESTION flow
   - Detect maestro_ask_question calls
   - Transition to QUESTION state
   - Inject answer and resume Claude Code

**Deliverables**:
- Can complete PLANNING with Claude Code
- Can complete CODING with Claude Code
- QUESTION flow works correctly
- Existing states (TESTING, CODE_REVIEW, etc.) work unchanged

### Phase 3: Testing & Polish (2-3 days)

**Goal**: Comprehensive testing and documentation

**Tasks**:
1. End-to-end tests
   - Full story lifecycle in Claude Code mode
   - Question/answer flow
   - Error recovery and restart

2. Documentation
   - Configuration guide in CLAUDE.md
   - Troubleshooting section

**Deliverables**:
- All tests passing
- Documentation complete
- Ready for use

## Timeline Summary

| Phase | Duration | Deliverables |
|-------|----------|--------------|
| Phase 1: Foundation | 2-3 days | Runner, parser, signal detection |
| Phase 2: Integration | 3-4 days | Coder handlers, config, prompts, QUESTION flow |
| Phase 3: Polish | 2-3 days | Tests, documentation |
| **Total** | **7-10 days** | **Production-ready Claude Code mode** |

## Success Criteria

**Functional**:
- Can complete stories end-to-end in Claude Code mode
- Architect review cycles work correctly
- Questions to architect work during planning/coding
- Standard coder mode unchanged and remains default

**Quality**:
- No regressions in standard mode
- Error handling covers all failure scenarios
- Clean process cleanup (no orphans)

**Operational**:
- Simple configuration to enable
- Clear error messages for debugging
- Documentation covers common scenarios

## Future Enhancements

1. **Resume Support**: Save Claude Code session for resume mode
2. **Parallel Phases**: Use Claude Code for planning while another story is coding
3. **Tool Filtering**: Granular control over which Claude Code tools are allowed
4. **Metrics Comparison**: Track and compare token usage/cost between modes
5. **Model Selection**: Support different Claude models for Claude Code mode
