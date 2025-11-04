# Architect Read Access Specification  
*Last updated: 2025-10-31 (rev A — Initial implementation)*

---

## Overview

This document defines the requirements and design for giving the **Architect Agent** controlled read-only access to the project codebase and coder workspaces.  

The purpose of this capability is to allow the architect to:
- Inspect current and in-progress code.  
- Validate story plans, documentation updates, and merges.  
- Perform limited line-level diff checks for review and validation.  

This is a forward-only feature. There is **no backward compatibility** and **no feature flag** — all future architect instances operate with direct read access.

---

## Goals

1. Provide the architect with reliable, deterministic visibility into coder workspaces.  
2. Preserve existing isolation and stability guarantees.  
3. Avoid unnecessary complexity — minimal tools, minimal privileges.  
4. Establish a pattern reusable by future read-access agents (e.g., ideation, documentation).

---

## Architecture

### Container & Mounts

The architect runs in its own Docker container:

```
/mnt/coders/coder-001 -> projectDir/coder-001 (ro)
/mnt/coders/coder-002 -> projectDir/coder-002 (ro)
/mnt/coders/coder-003 -> projectDir/coder-003 (ro)
/mnt/mirror            -> projectDir/.mirror    (ro)
```

- `/mnt/coders/*` are the active coder workspaces (always stable due to atomic clone-and-swap).  
- `/mnt/mirror` provides the mainline HEAD for diff operations.  
- All mounts are read-only.  
- The architect runs as root (same as coders). No user isolation required.  

---

## Workspace Stability Requirements

Each coder workspace must always exist and be safe to mount.  
The orchestrator guarantees this via **atomic reclone-and-swap**:

1. Clone mirror → `<coderDir>.new`  
2. Rename existing `<coderDir>` → `<coderDir>.old` (if exists)  
3. Rename `<coderDir>.new` → `<coderDir>` (atomic swap)  
4. Delete `.old` asynchronously  

This ensures that at any moment, the architect sees a consistent directory tree for each coder.

---

## MCP Toolset (Architect-Visible Tools)

| Tool | Description | Behavior |
|------|--------------|-----------|
| `read_file(coder_id, path)` | Read contents of a file from the specified coder workspace. | Returns UTF-8 text (truncated if large). |
| `list_files(coder_id, pattern)` | Enumerate files matching pattern under coder workspace. | Supports wildcards and directory traversal. |
| `get_diff(coder_id, path?)` | Return unified diff between the coder’s branch and mainline HEAD. | Uses mirror for comparison; path optional. |
| `submit_reply(payload)` | Submit architect’s reply or decision to orchestrator. | Terminates current iteration cycle. |

### `get_diff` Implementation

- Executed as:
  ```bash
  git -C /mnt/coders/<coder_id> diff --no-color --no-ext-diff origin/main -- <path?>
  ```
- If `path` omitted, diff entire repo.  
- Returns raw unified diff text.  
- Read-only operation.  
- Errors returned as structured messages.

---

## Iteration Cycle

Tool use is permitted in the following architect FSM states:

| State | Tool Use | Iteration Limit |
|--------|-----------|----------------|
| `SCOPING` | ✅ | configurable (default 8) |
| `REQUEST` | ✅ | configurable (default 8) |
| `MONITORING` | ✅ | configurable (default 8) |

### Iteration Flow
1. Architect decides to invoke a tool.  
2. Tool runs synchronously.  
3. Response appended to conversation context.  
4. Architect may reason further or invoke another tool.  
5. After reaching iteration cap or choosing to reply, architect sends `submit_reply`.  

Timeout per tool call: 30 s (default).

---

## Prompt Guidance

Architect prompts must include:

> You can inspect coder workspaces via provided tools (`read_file`, `list_files`, `get_diff`).  
> Use these tools only when the knowledge pack does not provide sufficient context.  
> Do **not** write, merge, or modify files — your role is analysis and validation.

Example usage patterns:
- During code review: use `get_diff` to verify specific changes.  
- During doc validation: `read_file` to confirm documentation updates.  
- During spec→story planning: `list_files` to check existing modules.  

---

## Security Posture

- Containers run as root within Docker isolation.  
- All mounts are read-only.  
- No network egress beyond orchestrator and LLM APIs.  
- No additional privilege separation required.  

This aligns with Maestro’s **moderate security** goal — safe for local single-user execution and competitive with current AI-coding tools.

---

## Telemetry

Every tool invocation must log:
- `coder_id`  
- `tool`  
- `path` or pattern  
- elapsed time and result size  

This enables performance profiling and ensures architects are not overusing file reads.

---

## Future Reuse

The same read-access pattern (container mounts + minimal tools + iteration loop) will be used for other agent types such as:
- **Ideation Agent** – reads prior specs and code for concept generation.  
- **Documentation Agent** – reads code and docs for synchronization checks.  

Only prompts and output schemas differ.

---

## Acceptance Criteria

1. Architect can successfully mount all coder directories read-only.  
2. Architect can read, list, and diff files without error.  
3. Atomic clone-and-swap prevents `ENOENT` during orchestrator churn.  
4. Architect remains performant under parallel coder updates.  
5. Telemetry confirms correct tool usage and iteration behavior.  

---

## Implementation Plan

### Overview

This is a major architectural change that introduces:
1. Architect containerization (architects currently run at orchestrator level)
2. Coder workspace management with atomic operations
3. New iteration-based LLM interaction model
4. Four new MCP tools for file system access

**Scale Targets**: 2-3 coders typical, ~10 coders maximum

**Compatibility**: Pre-release, no backward compatibility required

---

### Phase 1: Workspace Management Infrastructure

**Goal**: Establish reliable coder workspace directories with atomic swap capability.

#### 1.1 Workspace Manager Component (`pkg/workspace/`)

Create new package with:

```go
type Manager struct {
    projectDir    string          // Base project directory
    mirrorPath    string          // Path to .mirror bare repo
    logger        *logx.Logger
    mu            sync.RWMutex
}

// Core operations:
func (m *Manager) EnsureCoderWorkspace(coderID string) error
func (m *Manager) AtomicSwap(coderID string) error
func (m *Manager) GetWorkspacePath(coderID string) (string, error)
func (m *Manager) CleanupOldWorkspaces() error
```

**Implementation Details**:

- `EnsureCoderWorkspace(coderID)`:
  1. Check if `projectDir/coder-NNN` exists
  2. If not, perform initial clone from mirror
  3. If exists, verify it's a valid git repo
  4. Return error only if unrecoverable

- `AtomicSwap(coderID)`:
  1. Clone mirror → `projectDir/coder-NNN.new`
  2. `os.Rename(coder-NNN, coder-NNN.old)` (atomic on same filesystem)
  3. `os.Rename(coder-NNN.new, coder-NNN)` (atomic)
  4. Queue `coder-NNN.old` for async deletion
  5. On any failure, attempt rollback

- `CleanupOldWorkspaces()`:
  1. Find all `*.old` directories in projectDir
  2. Remove them in background goroutine
  3. Log any errors but don't fail

**Integration Points**:
- Orchestrator calls `EnsureCoderWorkspace()` before launching each coder
- Called during orchestrator startup for all known coders
- No changes to coder agents (they're unaware of swaps)

**Testing**:
- Unit tests for atomic operations
- Concurrent access tests (architect reading during swap)
- Failure recovery tests (interrupted swap, corrupt clone)

#### 1.2 Mirror Repository Management

Extend workspace manager with:

```go
func (m *Manager) EnsureMirror(repoURL string) error
func (m *Manager) UpdateMirror() error
```

**Implementation**:
- `EnsureMirror()`: Create `projectDir/.mirror` as bare git repo if missing
- `UpdateMirror()`: `git fetch --all` with one retry on failure
- Fatal error if mirror corrupt and refresh fails

**Configuration** (`config.json`):
```json
{
  "workspace": {
    "project_dir": "/path/to/maestro-work",
    "mirror_update_interval": "5m",
    "cleanup_interval": "30s"
  }
}
```

#### 1.3 Orchestrator Integration

**Changes to main orchestrator**:

1. Initialize workspace manager at startup:
```go
workspaceManager := workspace.NewManager(cfg.Workspace.ProjectDir)
err := workspaceManager.EnsureMirror(cfg.Repository.URL)
// Fatal if error
```

2. Before launching coder:
```go
err := workspaceManager.EnsureCoderWorkspace(coderID)
// Fatal if error - cannot proceed without workspace
```

3. Background goroutines:
```go
// Cleanup old workspaces every 30s
go workspaceManager.StartCleanupWorker(ctx)

// Update mirror every 5m (optional for this phase)
go workspaceManager.StartMirrorUpdateWorker(ctx)
```

**Deliverables**:
- [ ] `pkg/workspace/manager.go` - core workspace operations
- [ ] `pkg/workspace/manager_test.go` - comprehensive tests
- [ ] `pkg/workspace/cleanup.go` - background cleanup worker
- [ ] Orchestrator initialization code
- [ ] Configuration schema updates

---

### Phase 2: Architect Containerization

**Goal**: Run architect in Docker container with read-only mounts to coder workspaces.

#### 2.1 Architect Container Lifecycle

**New Component**: `pkg/exec/architect_executor.go`

```go
type ArchitectExecutor struct {
    containerID   string
    image         string
    mounts        []Mount
    executor      Executor  // Reuse existing Docker executor
    workspaceManager *workspace.Manager
}

type Mount struct {
    Source   string  // Host path
    Target   string  // Container path
    ReadOnly bool
}

func NewArchitectExecutor(workspaceManager *workspace.Manager) *ArchitectExecutor
func (e *ArchitectExecutor) Start(ctx context.Context) error
func (e *ArchitectExecutor) Stop(ctx context.Context) error
func (e *ArchitectExecutor) Exec(ctx context.Context, cmd []string) (*ExecResult, error)
```

**Mount Configuration**:

For each active coder, mount:
- Host: `projectDir/coder-NNN`
- Container: `/mnt/coders/coder-NNN`
- Mode: `ro` (read-only)

Plus mirror:
- Host: `projectDir/.mirror`
- Container: `/mnt/mirror`
- Mode: `ro`

**Container Image**:
- Use same bootstrap image as coders (`maestro-bootstrap`)
- Requires: git, shell, basic Unix tools
- No build tools needed (read-only operations)

#### 2.2 Dynamic Mount Management

**Challenge**: Coders can start/stop dynamically. Architect needs updated mounts.

**Solution**: Restart architect container when coder topology changes.

```go
func (e *ArchitectExecutor) UpdateMounts(coderIDs []string) error {
    // 1. Build new mount list
    newMounts := e.buildMountList(coderIDs)

    // 2. Stop existing container
    e.Stop(ctx)

    // 3. Update mount config
    e.mounts = newMounts

    // 4. Start new container with updated mounts
    return e.Start(ctx)
}
```

**Orchestrator Integration**:
- Track active coder IDs
- Call `architectExecutor.UpdateMounts()` when:
  - New coder launched
  - Coder terminated
  - Architect restarts from ERROR state

**Failure Handling**:
- Architect entering ERROR state is fatal (per requirements)
- Log detailed error and shutdown gracefully
- STATUS.md should capture final state for post-mortem

#### 2.3 Architect Driver Changes

**Current**: `pkg/architect/driver.go` has no executor

**New**: Add executor field and use for all file operations

```go
type Driver struct {
    // ... existing fields ...
    executor execpkg.Executor  // For tool execution within container
}

func NewArchitect(..., executor execpkg.Executor) (*Driver, error) {
    architect := NewDriver(..., executor)
    // ... existing initialization ...
}
```

**Impact on existing code**:
- `getDockerfileContent()` in `request.go:1084-1114` - needs refactor to use executor
- Any other direct file system access in architect - audit and migrate to tool-based access

**Deliverables**:
- [ ] `pkg/exec/architect_executor.go` - architect container management
- [ ] `pkg/exec/architect_executor_test.go` - lifecycle tests
- [ ] Update `pkg/architect/driver.go` - add executor field
- [ ] Orchestrator changes for architect lifecycle
- [ ] Integration tests for mount management

---

### Phase 3: MCP Read Tools

**Goal**: Implement four new tools for architect file system access.

#### 3.1 Tool Implementations

All tools follow existing factory pattern in `pkg/tools/registry.go`.

##### `read_file` Tool

```go
// pkg/tools/read_file.go

type ReadFileTool struct {
    executor execpkg.Executor
    maxSizeBytes int64  // Default 1MB, configurable
}

func (t *ReadFileTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name: "read_file",
        Description: "Read contents of a file from a coder workspace",
        InputSchema: InputSchema{
            Type: "object",
            Properties: map[string]Property{
                "coder_id": {
                    Type: "string",
                    Description: "Coder ID (e.g., 'coder-001')",
                },
                "path": {
                    Type: "string",
                    Description: "Relative path within coder workspace",
                },
            },
            Required: []string{"coder_id", "path"},
        },
    }
}

func (t *ReadFileTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    coderID := args["coder_id"].(string)
    path := args["path"].(string)

    // Validate coder_id format
    if !isValidCoderID(coderID) {
        return nil, fmt.Errorf("invalid coder_id format")
    }

    // Construct container path
    fullPath := filepath.Join("/mnt/coders", coderID, path)

    // Execute cat with size limit
    cmd := fmt.Sprintf("head -c %d %s", t.maxSizeBytes, fullPath)
    result, err := t.executor.Run(ctx, []string{"sh", "-c", cmd}, nil)

    if err != nil || result.ExitCode != 0 {
        return map[string]any{
            "success": false,
            "error": fmt.Sprintf("file not found or not readable: %s", path),
        }, nil
    }

    return map[string]any{
        "success": true,
        "content": result.Stdout,
        "truncated": len(result.Stdout) >= int(t.maxSizeBytes),
        "path": path,
    }, nil
}
```

**Configuration**:
```json
{
  "architect": {
    "tools": {
      "read_file_max_bytes": 1048576
    }
  }
}
```

##### `list_files` Tool

```go
// pkg/tools/list_files.go

type ListFilesTool struct {
    executor execpkg.Executor
    maxResults int  // Default 1000
}

func (t *ListFilesTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    coderID := args["coder_id"].(string)
    pattern := args["pattern"].(string)  // Optional, defaults to "*"

    if pattern == "" {
        pattern = "*"
    }

    // Use find with pattern matching
    basePath := filepath.Join("/mnt/coders", coderID)
    cmd := fmt.Sprintf("find %s -type f -name '%s' | head -n %d",
                       basePath, pattern, t.maxResults)

    result, err := t.executor.Run(ctx, []string{"sh", "-c", cmd}, nil)

    if err != nil {
        return map[string]any{
            "success": false,
            "error": err.Error(),
        }, nil
    }

    // Parse output into file list
    files := strings.Split(strings.TrimSpace(result.Stdout), "\n")

    // Strip /mnt/coders/{coder_id} prefix for cleaner output
    relativeFiles := make([]string, 0, len(files))
    for _, f := range files {
        if f != "" {
            rel := strings.TrimPrefix(f, basePath+"/")
            relativeFiles = append(relativeFiles, rel)
        }
    }

    return map[string]any{
        "success": true,
        "files": relativeFiles,
        "truncated": len(relativeFiles) >= t.maxResults,
        "count": len(relativeFiles),
    }, nil
}
```

##### `get_diff` Tool

```go
// pkg/tools/get_diff.go

type GetDiffTool struct {
    executor execpkg.Executor
    maxDiffLines int  // Default 10000
}

func (t *GetDiffTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    coderID := args["coder_id"].(string)
    path := ""
    if p, ok := args["path"].(string); ok {
        path = p
    }

    workspacePath := filepath.Join("/mnt/coders", coderID)

    // Build git diff command
    var cmd string
    if path != "" {
        cmd = fmt.Sprintf("cd %s && git diff --no-color --no-ext-diff origin/main -- %s | head -n %d",
                         workspacePath, path, t.maxDiffLines)
    } else {
        cmd = fmt.Sprintf("cd %s && git diff --no-color --no-ext-diff origin/main | head -n %d",
                         workspacePath, t.maxDiffLines)
    }

    result, err := t.executor.Run(ctx, []string{"sh", "-c", cmd}, nil)

    // Note: git diff returns 0 even if there are differences
    if err != nil || result.ExitCode != 0 {
        return map[string]any{
            "success": false,
            "error": fmt.Sprintf("git diff failed: %s", result.Stderr),
        }, nil
    }

    diffLines := strings.Split(result.Stdout, "\n")

    return map[string]any{
        "success": true,
        "diff": result.Stdout,
        "truncated": len(diffLines) >= t.maxDiffLines,
        "coder_id": coderID,
        "path": path,
    }, nil
}
```

##### `submit_reply` Tool

```go
// pkg/tools/submit_reply.go

type SubmitReplyTool struct {
    // This tool signals iteration loop termination
    // Similar to existing submit_plan, done, etc.
}

func (t *SubmitReplyTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name: "submit_reply",
        Description: "Submit your final response and exit iteration loop",
        InputSchema: InputSchema{
            Type: "object",
            Properties: map[string]Property{
                "response": {
                    Type: "string",
                    Description: "Your final response to the request",
                },
            },
            Required: []string{"response"},
        },
    }
}

func (t *SubmitReplyTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    response := args["response"].(string)

    // Return special signal that iteration loop should terminate
    return map[string]any{
        "success": true,
        "action": "submit",
        "response": response,
    }, nil
}
```

#### 3.2 Tool Registration

Add to `pkg/tools/registry.go` init():

```go
Register("read_file", createReadFileTool, &ToolMeta{
    Name: "read_file",
    Description: "Read contents of a file from a coder workspace",
    InputSchema: getReadFileSchema(),
})

Register("list_files", createListFilesTool, &ToolMeta{
    Name: "list_files",
    Description: "List files in a coder workspace matching a pattern",
    InputSchema: getListFilesSchema(),
})

Register("get_diff", createGetDiffTool, &ToolMeta{
    Name: "get_diff",
    Description: "Get git diff between coder workspace and main branch",
    InputSchema: getGetDiffSchema(),
})

Register("submit_reply", createSubmitReplyTool, &ToolMeta{
    Name: "submit_reply",
    Description: "Submit final response and exit iteration loop",
    InputSchema: getSubmitReplySchema(),
})
```

#### 3.3 Tool Availability by State

Update `pkg/tools/constants.go`:

```go
// Architect read tools - available in iteration-enabled states
var ArchitectReadTools = []string{
    ToolReadFile,
    ToolListFiles,
    ToolGetDiff,
    ToolSubmitReply,
}

// State-specific tool sets (to be used in Phase 4)
var ArchitectScopingTools = ArchitectReadTools
var ArchitectRequestTools = ArchitectReadTools
var ArchitectMonitoringTools = ArchitectReadTools
```

#### 3.4 Telemetry

Add to each tool's `Exec()`:

```go
startTime := time.Now()
defer func() {
    elapsed := time.Since(startTime)
    resultSize := len(resultContent)  // Varies by tool

    logger.Info("Tool invocation: tool=%s coder_id=%s path=%s elapsed=%v size=%d",
                toolName, coderID, path, elapsed, resultSize)

    // Also emit to metrics if enabled
    metrics.RecordToolInvocation(toolName, coderID, elapsed, resultSize)
}()
```

**Deliverables**:
- [ ] `pkg/tools/read_file.go` + tests
- [ ] `pkg/tools/list_files.go` + tests
- [ ] `pkg/tools/get_diff.go` + tests
- [ ] `pkg/tools/submit_reply.go` + tests
- [ ] Tool registration in `registry.go`
- [ ] Constants in `constants.go`
- [ ] Configuration schema updates
- [ ] Telemetry implementation

---

### Phase 4: Iteration Loop Infrastructure

**Goal**: Replace single-call LLM pattern with iteration loop for tool-enabled states.

#### 4.1 Iteration Loop Manager

**New Component**: `pkg/agent/iteration/loop.go`

```go
type Config struct {
    MaxIterations   int
    ToolTimeout     time.Duration
    TotalTimeout    time.Duration
}

type Loop struct {
    llmClient    agent.LLMClient
    toolProvider *tools.ToolProvider
    config       Config
    logger       *logx.Logger
}

type Result struct {
    FinalResponse string
    Iterations    int
    ToolCalls     []ToolCall
    Error         error
}

type ToolCall struct {
    Name     string
    Args     map[string]any
    Result   any
    Duration time.Duration
}

func NewLoop(llmClient agent.LLMClient, toolProvider *tools.ToolProvider, config Config) *Loop

func (l *Loop) Run(ctx context.Context, initialPrompt string) (*Result, error) {
    // Main iteration loop
    conversation := []Message{
        {Role: "user", Content: initialPrompt},
    }

    for i := 0; i < l.config.MaxIterations; i++ {
        // Call LLM with current conversation
        response, toolUses, err := l.callLLMWithTools(ctx, conversation)
        if err != nil {
            return nil, err
        }

        // If no tool uses, we're done
        if len(toolUses) == 0 {
            return &Result{
                FinalResponse: response,
                Iterations: i + 1,
            }, nil
        }

        // Execute tools and append results to conversation
        for _, toolUse := range toolUses {
            result, err := l.executeTool(ctx, toolUse)

            // Check for submit_reply termination signal
            if toolUse.Name == "submit_reply" {
                if resultMap, ok := result.(map[string]any); ok {
                    if action, ok := resultMap["action"].(string); ok && action == "submit" {
                        return &Result{
                            FinalResponse: resultMap["response"].(string),
                            Iterations: i + 1,
                        }, nil
                    }
                }
            }

            // Append tool result to conversation
            conversation = append(conversation, Message{
                Role: "tool",
                Content: formatToolResult(toolUse, result, err),
            })
        }
    }

    // Exceeded max iterations
    return nil, fmt.Errorf("exceeded maximum iterations (%d)", l.config.MaxIterations)
}

func (l *Loop) executeTool(ctx context.Context, toolUse *ToolUse) (any, error) {
    // Get tool from provider
    tool, err := l.toolProvider.Get(toolUse.Name)
    if err != nil {
        return nil, err
    }

    // Execute with timeout
    ctx, cancel := context.WithTimeout(ctx, l.config.ToolTimeout)
    defer cancel()

    return tool.Exec(ctx, toolUse.Args)
}
```

#### 4.2 Integration with Architect States

**Update**: `pkg/architect/scoping.go`, `request.go`, `monitoring.go`

##### Example: SCOPING State

Current flow (lines 203-234 in scoping.go):
```go
// Get LLM response using centralized helper
llmAnalysis, err := d.callLLMWithTemplate(ctx, prompt)
```

New flow:
```go
// Use iteration loop for SCOPING state
iterConfig := iteration.Config{
    MaxIterations: d.getIterationLimit("SCOPING"),  // From config, default 8
    ToolTimeout: 30 * time.Second,
    TotalTimeout: 5 * time.Minute,
}

loop := iteration.NewLoop(d.llmClient, d.toolProvider, iterConfig)
result, err := loop.Run(ctx, prompt)
if err != nil {
    return StateError, fmt.Errorf("iteration loop failed: %w", err)
}

llmAnalysis := result.FinalResponse
```

##### Example: REQUEST State

Current flow (lines 382-442 in request.go):
```go
llmFeedback, err := d.callLLMWithTemplate(ctx, prompt)
```

New flow:
```go
// Use iteration loop for REQUEST state
iterConfig := iteration.Config{
    MaxIterations: d.getIterationLimit("REQUEST"),
    ToolTimeout: 30 * time.Second,
    TotalTimeout: 10 * time.Minute,  // Longer for code review
}

loop := iteration.NewLoop(d.llmClient, d.toolProvider, iterConfig)
result, err := loop.Run(ctx, prompt)
if err != nil {
    return nil, fmt.Errorf("iteration loop failed: %w", err)
}

llmFeedback := result.FinalResponse
```

#### 4.3 Tool Provider Setup

**Update**: `pkg/architect/driver.go`

Add tool provider field:

```go
type Driver struct {
    // ... existing fields ...
    executor     execpkg.Executor
    toolProvider *tools.ToolProvider
}
```

Initialize in constructor:

```go
func NewArchitect(..., executor execpkg.Executor) (*Driver, error) {
    // ... existing code ...

    // Create tool provider with architect-specific tools
    agentCtx := tools.AgentContext{
        Executor: executor,
        ReadOnly: true,
        NetworkDisabled: true,
        Agent: architect,  // For state-aware behavior
    }

    // Tools vary by state - use ArchitectReadTools for iteration states
    toolProvider := tools.NewProvider(agentCtx, tools.ArchitectReadTools)

    architect.toolProvider = toolProvider

    return architect, nil
}
```

#### 4.4 Configuration

Add to `config.json`:

```json
{
  "architect": {
    "iteration": {
      "max_iterations": 8,
      "tool_timeout_sec": 30,
      "total_timeout_sec": 600
    }
  }
}
```

Add config struct in `pkg/config/config.go`:

```go
type ArchitectConfig struct {
    Model     string           `json:"model"`
    Iteration IterationConfig  `json:"iteration"`
}

type IterationConfig struct {
    MaxIterations    int `json:"max_iterations"`
    ToolTimeoutSec   int `json:"tool_timeout_sec"`
    TotalTimeoutSec  int `json:"total_timeout_sec"`
}
```

#### 4.5 Prompt Updates

**Update**: Architect prompts in `pkg/templates/`

Add to all iteration-enabled state prompts:

```markdown
## Available Tools

You have access to the following tools for inspecting coder workspaces:

- **read_file(coder_id, path)** - Read contents of a file
- **list_files(coder_id, pattern)** - List files matching a pattern
- **get_diff(coder_id, path?)** - Get git diff vs main branch
- **submit_reply(response)** - Submit your final response

## Tool Usage Guidelines

- Use tools **only when necessary** - they consume time and tokens
- Prefer to reason from existing context when possible
- Always call `submit_reply` with your final response
- Tools have timeouts - keep operations focused

## Example Usage

If you need to review a coder's implementation:
1. Use `get_diff(coder_id="coder-001")` to see all changes
2. If you need specific file details, use `read_file(coder_id="coder-001", path="src/main.go")`
3. Once satisfied, call `submit_reply(response="APPROVED: Changes look good...")`
```

**Deliverables**:
- [ ] `pkg/agent/iteration/loop.go` - iteration loop manager
- [ ] `pkg/agent/iteration/loop_test.go` - comprehensive tests
- [ ] Update `pkg/architect/scoping.go` - use iteration loop
- [ ] Update `pkg/architect/request.go` - use iteration loop
- [ ] Update `pkg/architect/monitoring.go` - use iteration loop
- [ ] Update `pkg/architect/driver.go` - add tool provider
- [ ] Update architect prompt templates
- [ ] Configuration schema and loading
- [ ] Integration tests for iteration flow

---

### Phase 5: Testing & Validation

**Goal**: Comprehensive testing of the complete system.

#### 5.1 Unit Tests

Each component must have:
- Happy path tests
- Error handling tests
- Edge case tests
- Concurrent access tests (where applicable)

**Coverage targets**:
- Workspace manager: >90%
- Tool implementations: >85%
- Iteration loop: >90%

#### 5.2 Integration Tests

Create `tests/architect_read_access_test.go`:

```go
func TestArchitectCanReadCoderWorkspace(t *testing.T)
func TestArchitectIterationLoop(t *testing.T)
func TestAtomicWorkspaceSwap(t *testing.T)
func TestArchitectContainerMounts(t *testing.T)
func TestToolTimeouts(t *testing.T)
func TestMaxIterationsExceeded(t *testing.T)
func TestMultipleCodersReadAccess(t *testing.T)
```

#### 5.3 End-to-End Test

Create realistic scenario:
1. Start orchestrator with architect + 2 coders
2. Submit spec that triggers SCOPING state
3. Architect uses `list_files` to explore codebase
4. Coders implement stories
5. Architect uses `get_diff` during code review
6. Verify all operations logged correctly

**Test fixture**: `tests/fixtures/read_access_e2e_spec.md`

#### 5.4 Performance Testing

Measure:
- Workspace swap time (should be <1s for typical repos)
- Tool execution overhead (read_file, list_files, get_diff)
- Iteration loop latency
- Architect container restart time

**Performance targets**:
- Tool execution: <500ms for typical operations
- Workspace swap: <2s for repos up to 1GB
- Iteration loop: Complete in <5min for complex reviews

#### 5.5 Failure Mode Testing

Test scenarios:
- Coder workspace directory missing during architect startup
- Mirror repository corrupt
- Git diff fails (detached HEAD, merge conflicts)
- Tool timeout exceeded
- Max iterations exceeded
- Architect container crash

Verify:
- Errors logged clearly
- System fails gracefully
- No data corruption
- STATUS.md captures failure state

**Deliverables**:
- [ ] Unit tests for all new components (>85% coverage)
- [ ] Integration tests for tool + iteration flow
- [ ] E2E test with realistic scenario
- [ ] Performance benchmarks
- [ ] Failure mode tests
- [ ] Test documentation

---

### Phase 6: Documentation & Rollout

#### 6.1 Code Documentation

- Godoc comments for all public APIs
- Architecture diagrams (container topology, data flow)
- Sequence diagrams for:
  - Workspace atomic swap
  - Iteration loop flow
  - Tool execution

#### 6.2 User Documentation

Create/update:
- `docs/ARCHITECTURE.md` - add architect containerization section
- `docs/WORKSPACE_MANAGEMENT.md` - new file for workspace operations
- `docs/ARCHITECT_TOOLS.md` - new file documenting read tools
- `CLAUDE.md` - update with architect containerization notes

#### 6.3 Configuration Migration

Since pre-release, no migration needed, but document:
- New config fields and defaults
- How to tune iteration limits
- Tool timeout tuning guidelines

#### 6.4 Rollout Checklist

- [ ] All tests passing
- [ ] Performance benchmarks meet targets
- [ ] Documentation complete
- [ ] Code reviewed
- [ ] Integration tested with real projects
- [ ] Failure modes verified
- [ ] Telemetry validated

---

## Technical Decisions & Rationale

### Why Atomic Swap vs Lock-Based Access?

**Decision**: Use atomic directory rename instead of file locking.

**Rationale**:
- Simpler implementation (2 rename calls)
- No risk of stale locks
- Works across processes
- Read-only architect can never corrupt workspace
- Matches Unix best practices

### Why Restart Architect on Coder Topology Change?

**Decision**: Restart architect container when coders start/stop.

**Rationale**:
- Docker doesn't support dynamic mount updates
- Architect restarts are rare (coders are long-lived)
- Cleaner than maintaining two mount paths (new + old)
- Consistent with "architect error = fatal" philosophy

### Why submit_reply Tool Instead of Automatic Detection?

**Decision**: Require explicit `submit_reply()` call to exit iteration loop.

**Rationale**:
- LLM has clear control over when to stop iterating
- Avoids ambiguity (is LLM done or just thinking?)
- Consistent with existing tools (`done`, `submit_plan`)
- Enables future features (e.g., partial responses, streaming)

### Why Separate Iteration Loop Manager?

**Decision**: Create `pkg/agent/iteration/` package instead of embedding in architect.

**Rationale**:
- Reusable for future agents (ideation, documentation)
- Easier to test in isolation
- Clear separation of concerns
- Can be used by other agent types without code duplication

---

## Dependencies & Sequencing

```
Phase 1 (Workspace)
    ↓
Phase 2 (Architect Container) ← depends on Phase 1
    ↓
Phase 3 (Tools) ← depends on Phase 2
    ↓
Phase 4 (Iteration Loop) ← depends on Phase 3
    ↓
Phase 5 (Testing) ← depends on Phases 1-4
    ↓
Phase 6 (Documentation) ← depends on Phase 5
```

**Estimated effort**:
- Phase 1: 3-4 days
- Phase 2: 4-5 days
- Phase 3: 3-4 days
- Phase 4: 4-5 days
- Phase 5: 2-3 days
- Phase 6: 1-2 days

**Total**: ~18-23 days (3-4 weeks)

---

## Open Questions

1. **Mirror Update Strategy**: Should mirror be updated:
   - On every workspace swap?
   - On fixed interval (5min)?
   - Only on demand?
   - Never (assume user keeps it updated)?

2. **Knowledge Pack**: Spec mentions "knowledge pack" (line 110). What is this?
   - Story content + metadata?
   - Cached context?
   - Database query results?
   - Needs clarification before Phase 4 prompt updates.

3. **Tool Availability in Non-Iteration States**: Should tools be available in WAITING, DISPATCHING, etc.?
   - Spec only lists SCOPING, REQUEST, MONITORING
   - Probably not needed elsewhere, but verify

4. **Error Recovery**: If architect container fails to start, should system:
   - Retry N times?
   - Fall back to non-containerized mode?
   - Fatal error immediately?

5. **Workspace Swap Trigger**: When should workspaces be swapped?
   - Before every architect iteration?
   - On fixed interval?
   - On explicit request from coder?
   - Manual only?

---

*Any deviation from this document is a bug.*
