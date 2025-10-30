# RepoState Architecture Design

**Status**: Design Phase
**Date**: 2025-10-22
**Purpose**: Eliminate redundant work by maintaining structured repository state instead of raw tool history

## Problem Statement

Current architecture sends full conversation history (4-20k tokens of raw tool output) to LLM on each turn. This causes:
1. **Silent token truncation** - oldest messages (file creation confirmations) are lost
2. **Prompt structure issues** - fresh "TOOL CALLS ONLY" instruction at end overrides history
3. **Unstructured context** - LLM can't easily infer "these files already exist" from Go code snippets

Result: LLM repeatedly recreates files (main.go written 41 times in single story).

## Solution: Snapshot + Delta Pattern

Replace historical transcript with **structured snapshot** of current repo state plus **explicit delta** (what remains to be done).

## Core Data Structures

### RepoState

```go
// RepoState represents the current state of the agent's workspace repository.
// This is the single source of truth for what work has been completed.
type RepoState struct {
    // WorkDir is the absolute path to the agent's workspace
    WorkDir string

    // Files contains metadata for all tracked files in the workspace
    Files []FileMeta

    // OpenTasks contains remaining work items
    OpenTasks []Task

    // LastUpdated is the timestamp of the most recent state update
    LastUpdated time.Time

    // StoryID is the current story being worked on
    StoryID string
}

// FileMeta contains metadata about a single file in the workspace.
type FileMeta struct {
    // Path relative to WorkDir
    Path string

    // ContentHash (SHA256) of file contents
    Hash string

    // Size in bytes
    Size int64

    // LastModified timestamp
    ModTime time.Time

    // FileType (e.g., "go", "md", "yaml")
    Type string

    // Status: "created", "modified", "unchanged"
    Status string
}

// Task represents a single work item to be completed.
type Task struct {
    // Description of the task
    Description string

    // Status: "pending", "in_progress", "completed"
    Status string

    // Priority for ordering
    Priority int
}
```

## Component Design

### 1. RepoState Tracker

**Location**: `pkg/repostate/tracker.go`

**Responsibilities**:
- Monitor tool execution results
- Update file metadata on writes
- Add/remove tasks based on work progress
- Persist state to disk

```go
type Tracker struct {
    state      *RepoState
    workDir    string
    logger     *logx.Logger
    persistence chan<- RepoStateUpdate
}

// UpdateFromToolResult processes a tool execution result and updates state.
func (t *Tracker) UpdateFromToolResult(toolName string, result map[string]any) error

// GetSnapshot returns the current RepoState for prompt injection.
func (t *Tracker) GetSnapshot() *RepoState

// Persist saves the current state to disk.
func (t *Tracker) Persist() error

// Load restores state from disk (for restart scenarios).
func (t *Tracker) Load() error
```

### 2. Snapshot Formatter

**Location**: `pkg/repostate/formatter.go`

**Responsibilities**:
- Format RepoState as human-readable text for LLM prompts
- Keep output concise (target: <500 tokens)

```go
// FormatForPrompt renders RepoState as structured text for LLM consumption.
func FormatForPrompt(state *RepoState) string

// Example output:
// --- Repository State ---
// Workspace: /work/coder-001
// Story: hello-001
//
// Existing Files (3):
//   • cmd/main.go (a4f3e1, 245 bytes) - go source
//   • go.mod (8d2c47, 89 bytes) - go module
//   • README.md (1b4e9a, 156 bytes) - markdown
//
// Open Tasks (2):
//   1. [pending] Add unit tests for main package
//   2. [pending] Run go build and verify compilation
//
// Last updated: 2025-10-22 03:15:42 UTC
```

### 3. Context Manager Integration

**Location**: `pkg/contextmgr/snapshot.go`

**Responsibilities**:
- Build prompts using snapshot instead of full history
- Garbage collect tool results after each turn

```go
// BuildSnapshotPrompt creates a minimal prompt from template + repo snapshot.
func (cm *ContextManager) BuildSnapshotPrompt(
    template string,
    repoState *RepoState,
    additionalContext string,
) []Message
```

**Prompt Structure**:
```
[System Message]
You are a coder agent. Use TOOL CALLS ONLY. Never output explanatory text.

[Repo Snapshot]
<formatted RepoState>

[Delta Request]
Your plan has been approved. Complete the remaining open tasks and invoke 'done' when finished.

DO NOT recreate files that already exist. Use read_file to verify state before writing.
```

### 4. Tool Result Processing

**Location**: `pkg/coder/driver.go` (existing file, add integration)

**Integration Points**:
```go
// After tool execution in processToolResult:
func (c *Coder) processToolResult(toolName string, result map[string]any) error {
    // ... existing error handling ...

    // Update RepoState tracker
    if c.repoTracker != nil {
        if err := c.repoTracker.UpdateFromToolResult(toolName, result); err != nil {
            c.logger.Warn("Failed to update repo state: %v", err)
        }
    }

    // ... existing persistence logic ...
}
```

## File Operations Integration

### Idempotent Write Detection

**Location**: `pkg/tools/mcp/file_write.go` (example)

```go
// Before writing file, check if content is identical
func (t *FileWriteTool) Execute(params map[string]any) (map[string]any, error) {
    path := params["path"].(string)
    newContent := params["content"].(string)

    // Check if file exists and hash matches
    if existingHash, err := getFileHash(path); err == nil {
        newHash := computeHash(newContent)
        if existingHash == newHash {
            return map[string]any{
                "success": true,
                "noop": true,
                "message": "File already exists with identical content",
            }, nil
        }
    }

    // Proceed with write...
}
```

## State Persistence

### Storage Format

**Location**: `{workDir}/.maestro/repostate.json`

```json
{
  "work_dir": "/work/coder-001",
  "story_id": "hello-001",
  "files": [
    {
      "path": "cmd/main.go",
      "hash": "a4f3e1b2...",
      "size": 245,
      "mod_time": "2025-10-22T03:15:42Z",
      "type": "go",
      "status": "created"
    }
  ],
  "open_tasks": [
    {
      "description": "Add unit tests for main package",
      "status": "pending",
      "priority": 1
    }
  ],
  "last_updated": "2025-10-22T03:15:42Z"
}
```

### Persistence Pattern

- **Write**: After every tool execution that modifies files
- **Read**: On agent initialization/restart
- **Atomic writes**: Use temp file + rename pattern

## Context Window Management

### Before (Current)
```
Token count by turn:
Turn 1: 4,032 tokens  (template + spec)
Turn 2: 5,558 tokens  (+ tool results from turn 1)
Turn 3: 8,758 tokens  (+ tool results from turns 1-2)
...
Turn N: 19,606 tokens (+ tool results from all previous turns)
```

### After (Snapshot Pattern)
```
Token count by turn:
Turn 1: 4,032 tokens  (template + spec + empty snapshot)
Turn 2: 4,100 tokens  (template + snapshot with 2 files + 3 tasks)
Turn 3: 4,150 tokens  (template + snapshot with 3 files + 2 tasks)
...
Turn N: 4,200 tokens  (template + snapshot with 5 files + 0 tasks)
```

**Target**: Keep total prompt under 5k tokens regardless of turn count.

## Integration Phases

### Phase 1: Infrastructure (No Behavior Change)
1. Create `pkg/repostate/` package with core types
2. Implement Tracker with file monitoring
3. Implement Formatter for snapshot rendering
4. Add persistence (save/load from disk)
5. **Validation**: RepoState accurately reflects file system

### Phase 2: Parallel Tracking (Validation)
1. Integrate Tracker into Coder agent
2. Update RepoState on every tool result
3. Log snapshots but DON'T use in prompts yet
4. **Validation**: Compare snapshot accuracy with manual inspection

### Phase 3: Snapshot-Based Prompts (Behavior Change)
1. Add `BuildSnapshotPrompt()` to ContextManager
2. Switch CODING state to use snapshot prompts
3. Keep fallback to old behavior (feature flag)
4. **Validation**: Verify no regressions in simple stories

### Phase 4: Context Garbage Collection
1. After each turn, squash tool results into snapshot
2. Clear message history except last user/assistant exchange
3. Remove feature flag, make snapshot default
4. **Validation**: Confirm context stays <5k tokens

### Phase 5: Idempotent Tools
1. Add hash checking to file write tools
2. Return `noop: true` for duplicate writes
3. Skip storing no-op results in any context
4. **Validation**: Repeated writes become instant no-ops

## Success Metrics

### Primary Goals
- ✅ File write count: 1 per unique file (not 41x)
- ✅ Context size: stays <5k tokens across all turns
- ✅ No silent truncation: oldest data never lost

### Secondary Goals
- ✅ Budget efficiency: fewer tokens = lower cost
- ✅ Restart resilience: RepoState persisted, work resumes cleanly
- ✅ Observable: snapshot visible in logs for debugging

## Migration Strategy

### Compatibility
- Keep old context manager behavior intact
- Add new snapshot code path alongside
- Use feature flag: `config.Context.UseSnapshot` (default: false)
- Gradually enable for new stories

### Rollback Plan
If snapshot approach causes issues:
1. Set `UseSnapshot: false` in config
2. Agent falls back to full history approach
3. No data loss (history still accumulated)

## Testing Plan

### Unit Tests
- `pkg/repostate/tracker_test.go` - file tracking accuracy
- `pkg/repostate/formatter_test.go` - snapshot rendering
- `pkg/repostate/persistence_test.go` - save/load

### Integration Tests
- `pkg/coder/snapshot_integration_test.go` - end-to-end with real tools
- Verify: file writes detected, tasks updated, snapshot accurate

### Validation Test
- Run original failing scenario (hello world story)
- Measure: file write count, context size per turn, completion time
- Compare: before vs after metrics

## Open Questions

1. **Task inference**: How do we automatically generate OpenTasks from story spec?
   - Option A: Parse story requirements deterministically
   - Option B: Ask LLM to generate task list in PLANNING state
   - **Decision**: Start with Option B, can optimize later

2. **File discovery**: Should we scan entire workspace or only track tool-created files?
   - Option A: Only track files created via tools (simpler, faster)
   - Option B: Full scan periodically (catches external changes)
   - **Decision**: Start with Option A, add Option B if needed

3. **State conflicts**: What if file system state diverges from RepoState?
   - Mitigation: Hash-based validation on each turn
   - Recovery: Re-scan workspace if hashes mismatch

4. **Cross-agent state**: Should RepoState be shared between coder agents?
   - Current: Each agent has isolated workspace
   - Future: If agents collaborate, need shared state mechanism
   - **Decision**: Out of scope for now

## References

- Expert analysis (get_help tool): `/Users/dratner/Code/maestro/CONTEXT_ISSUE_NOTES.md`
- Current context manager: `pkg/contextmgr/contextmgr.go`
- Tool integration: `pkg/coder/driver.go:processToolResult()`
- MCP tools: `pkg/tools/mcp/`
