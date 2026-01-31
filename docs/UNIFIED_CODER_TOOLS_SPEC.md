# Unified Coder Tools Spec

**Author:** Claude (with Dan)
**Date:** 2026-01-30
**Status:** Ready for Implementation

---

## 1. Problem Statement

Maestro currently enforces a binary split between **app stories** and **devops stories**, where story type determines tool access:

- App stories get build/test/lint tools but no container or compose tools
- DevOps stories get container tools but no build/test/lint tools

This creates friction when app stories discover infrastructure prerequisites mid-execution (e.g., missing linter, need for a database service). The coder cannot remediate the issue without a separate DevOps story, adding coordination overhead.

**Frequency estimate**: 5-10% of app stories encounter this situation.

---

## 2. Goals

1. **G1**: Allow app stories to modify containers and compose services when genuinely needed
2. **G2**: Maintain safety through recovery mechanisms rather than permission gating
3. **G3**: Keep the implementation simple - no capability flags, no approval workflows
4. **G4**: Preserve the ability to identify "container-modifying" stories for appropriate testing
5. **G5**: Support Claude Code mode with seamless container restarts

---

## 3. Non-Goals

1. **N1**: Complex approval workflows for infrastructure changes
2. **N2**: Dynamic tool surface expansion mid-story
3. **N3**: Direct JSONL file manipulation for Claude Code sessions

---

## 4. Design

### 4.1 Unified Tool Sets

Merge container and compose tools into app coding tools. The story type distinction becomes a hint for prompts and defaults, not a hard gate on tool access.

**New AppCodingTools** (in `pkg/tools/constants.go`):

```go
AppCodingTools = []string{
    // Existing app tools
    ToolShell,
    ToolBuild,
    ToolTest,
    ToolLint,
    ToolAskQuestion,
    ToolDone,
    ToolChatPost,
    ToolChatRead,
    ToolTodosAdd,
    ToolTodoComplete,
    ToolTodoUpdate,
    ToolWebSearch,
    ToolWebFetch,
    // New: container tools
    ToolContainerBuild,
    ToolContainerUpdate,
    ToolContainerTest,
    ToolContainerList,
    ToolContainerSwitch,
    // New: compose tool
    ToolComposeUp,
}
```

**DevOpsCodingTools**: Keep identical to AppCodingTools. The distinction becomes purely semantic/hint-based.

**Planning tools**: Add `ToolContainerTest` and `ToolContainerList` to `AppPlanningTools` for environment verification during planning.

### 4.2 Story Type as Hint

The `StoryType` field (app vs devops) becomes:

- **Prompt guidance**: DevOps stories get prompts emphasizing infrastructure focus
- **Default expectations**: DevOps stories are expected to modify containers
- **NOT tool gating**: Both types have access to all tools

This preserves backwards compatibility while removing the friction.

### 4.3 Container Modification Tracking

Introduce a state variable to track whether the story modified container configuration.

**New state keys** in `pkg/coder/coder_fsm.go`:

```go
const (
    // ... existing keys ...
    KeyContainerModified = "container_modified"      // bool: was container_update called?
    KeyNewContainerImage = "new_container_image"     // string: image ID from container_update
)
```

**Important**: State mutation happens in the **coder's tool execution handler**, not in the tool itself. This keeps state mutation centralized:

```go
// In coder tool execution path (not in the tool)
result, err := tool.Exec(ctx, params)
if err == nil && toolName == ToolContainerUpdate {
    sm.SetStateData(KeyContainerModified, true)
    if imageID, ok := result["image"].(string); ok {
        sm.SetStateData(KeyNewContainerImage, imageID)
    }
}
```

**Read by TESTING state**: Determines which test strategy to use:

```go
func (c *Coder) handleTesting(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    containerModified := utils.GetStateValueOr[bool](sm, KeyContainerModified, false)

    if containerModified {
        // Run container validation tests (build verification, boot test)
        return c.handleContainerTesting(ctx, sm)
    }

    // Run normal app tests
    return c.handleAppStoryTesting(ctx, sm)
}
```

This replaces the current `storyType == DevOps` check with actual behavior detection.

### 4.4 Container Switch with Recovery

The `container_switch` tool includes built-in fallback to safe container on failure:

```go
func (t *ContainerSwitchTool) Exec(ctx context.Context, params map[string]any) (any, error) {
    targetID, _ := params["container_id"].(string)

    // Attempt switch
    if err := t.containerManager.Switch(ctx, targetID); err != nil {
        t.logger.Warn("container switch to %s failed: %v, falling back to safe container", targetID, err)

        // Fallback to safe container
        if fallbackErr := t.containerManager.Switch(ctx, t.safeContainerID); fallbackErr != nil {
            return nil, fmt.Errorf("switch failed and fallback failed: original=%v, fallback=%v", err, fallbackErr)
        }

        return map[string]any{
            "success":   false,
            "fell_back": true,
            "target":    targetID,
            "actual":    t.safeContainerID,
            "error":     err.Error(),
            "message":   fmt.Sprintf("Switch to %s failed, now using safe container %s", targetID, t.safeContainerID),
        }, nil
    }

    return map[string]any{
        "success":   true,
        "container": targetID,
        "message":   fmt.Sprintf("Successfully switched to container %s", targetID),
    }, nil
}
```

### 4.5 Container Restart Semantics

When a container image changes (via `container_update`), "restart" means:

1. `StopContainer(oldContainerName)` - stop and remove old container
2. `SetImage(newImageID)` - update the executor's image reference
3. `StartContainer(storyID, opts)` - create new container with correct options:
   - Planning phase: workspace mounted read-only
   - Coding phase: workspace mounted read-write
   - Claude Code mode: include Claude state volume mount

This prevents half-updated states where the image reference changes but the container doesn't.

### 4.6 Claude Code Session Persistence

#### 4.6.1 Problem

Currently, containers set `HOME=/tmp` with `/tmp` as a tmpfs mount. Claude Code sessions go to `$HOME/.claude/` which doesn't survive container recreation (e.g., after `container_switch`).

The issue is not "Claude can't write" - it's "Claude can't survive container recreation."

#### 4.6.2 Solution: Docker Named Volume

Use a Docker named volume to persist **only** the Claude state directory, keeping everything else ephemeral:

```go
// In StartContainer, for Claude Code mode:
volumeName := fmt.Sprintf("maestro-claude-%s", agentID)

args = append(args,
    "--tmpfs", "/tmp:exec,nodev,nosuid,size=512m",      // ephemeral (existing)
    "--volume", fmt.Sprintf("%s:/tmp/.claude:rw", volumeName), // persistent (NEW)
    "--env", "HOME=/tmp",  // unchanged
)
```

**How this works:**
- `/tmp` is tmpfs (in-memory, ephemeral)
- `/tmp/.claude` is a Docker named volume that "punches through" the tmpfs
- Volume data stored at `/var/lib/docker/volumes/maestro-claude-<agentID>/_data/` (managed by Docker)
- Volume persists across container stop/start/remove
- Volume explicitly deleted on agent shutdown

**Volume lifecycle:**
```
Container A starts with --volume maestro-claude-001:/tmp/.claude
  ↓
Claude writes to /tmp/.claude/projects/-workspace/session-123.jsonl
  ↓
Data stored in Docker volume (on host, outside container)
  ↓
Container A stopped and REMOVED (container_switch)
  ↓
Container B starts with same volume mount
  ↓
Session file still exists - Claude --resume works
```

**Volume cleanup** (on agent shutdown/restart):

```go
func (d *LongRunningDockerExec) Shutdown(ctx context.Context) error {
    // ... existing container cleanup ...

    // Remove Claude state volumes
    if d.agentID != "" {
        volumeName := fmt.Sprintf("maestro-claude-%s", d.agentID)
        exec.Command("docker", "volume", "rm", volumeName).Run()
    }
}
```

#### 4.6.3 Container Switch in Claude Code Mode

When `container_switch` is called in Claude Code mode, the Runner must:

1. Detect the `container_switch` tool call (via existing signal detection pattern)
2. Return success message: "Restart requested. Save your progress so we can resume later with full context."
3. Wait for JSONL flush (watch file for idle - no writes for 2 seconds)
4. Subprocess exits after tool call completes
5. Perform container switch operation (stop old → start new with same volume)
6. Restart Claude Code with `--resume <session-id>` and status message as `ResumeInput`

**Runner enhancement** (`pkg/coder/claude/runner.go`):

```go
func (r *Runner) Run(ctx context.Context, opts *RunOptions) (Result, error) {
    // ... existing setup ...

    result := r.executeAndParse(ctx, opts)

    // Check if container_switch was called
    if result.ContainerSwitchRequested {
        return r.handleContainerRestart(ctx, opts, result)
    }

    return result, nil
}

func (r *Runner) handleContainerRestart(ctx context.Context, opts *RunOptions, result Result) (Result, error) {
    sessionID := opts.SessionID

    // 1. Wait for JSONL flush (volume-mounted path)
    r.waitForSessionFlush(sessionID, 2*time.Second)

    // 2. Subprocess already exited (tool call completed)

    // 3. Perform container switch (via callback)
    switchResult := r.containerSwitchCallback(result.ContainerSwitchTarget)

    // 4. Build status message for resume
    var statusMsg string
    if switchResult.Success {
        statusMsg = fmt.Sprintf("Container switched to %s. Continue your work.", switchResult.Container)
    } else {
        statusMsg = fmt.Sprintf("Container switch failed: %s. Using safe container. Continue your work.", switchResult.Error)
    }

    // 5. Restart with --resume and status as ResumeInput
    opts.Resume = true
    opts.ResumeInput = statusMsg

    return r.Run(ctx, opts) // Recursive call with resume
}

func (r *Runner) waitForSessionFlush(sessionID string, idleTimeout time.Duration) {
    // Session file is in the Docker volume, accessible from host
    // Path: /var/lib/docker/volumes/maestro-claude-<agent>/projects/-workspace/<sessionID>.jsonl
    // Or access via: docker volume inspect + path
    jsonlPath := r.getSessionPath(sessionID)
    var lastMod time.Time

    for {
        info, err := os.Stat(jsonlPath)
        if err != nil {
            return // File doesn't exist or error, proceed anyway
        }
        if info.ModTime() == lastMod {
            return // No writes for idleTimeout, safe to proceed
        }
        lastMod = info.ModTime()
        time.Sleep(idleTimeout)
    }
}
```

### 4.7 Compose: Single Tool + Guardrails

**Coder tool**: Only `compose_up` is exposed to coders.

The compose file (`<repo>/.maestro/compose.yml`) is a normal file that coders edit with shell/file tools. The only action tool needed is `compose_up` to bring services online during CODING.

**Guardrails:**

1. **Path restriction**: Only allow compose files under `/workspace` (the story workspace). Prevents "compose up arbitrary host paths" scenarios.

2. **Project name isolation**: Derive compose project name from agent ID (e.g., `maestro-coder-001`). This is already done in `ensureComposeStackRunning` - ensure `compose_up` tool follows the same pattern.

```go
func (t *ComposeUpTool) Exec(ctx context.Context, params map[string]any) (any, error) {
    composePath := params["path"].(string)

    // Guardrail: path must be under /workspace
    if !strings.HasPrefix(composePath, "/workspace") {
        return nil, fmt.Errorf("compose file must be under /workspace, got: %s", composePath)
    }

    // Project name derived from agent ID for isolation
    projectName := fmt.Sprintf("maestro-%s", t.agentCtx.AgentID)

    // ... run compose up with projectName ...
}
```

**Programmatic cleanup**: The orchestrator ensures `compose down` runs:
- On story completion (DONE state entry)
- On story error (ERROR state entry)
- On graceful shutdown
- On agent termination

This is NOT a coder tool - it's automatic cleanup to prevent orphaned containers.

### 4.8 Prompt Guidance

Add guidance to coder prompts (CODING state) indicating container/compose tools are available but should be used judiciously:

```markdown
## Environment Tools

Container and compose tools are available when you encounter genuine environment prerequisites:

- **container_build**: Build a new container image from Dockerfile changes
- **container_test**: Verify a container image works correctly
- **container_switch**: Switch to a different container (has automatic fallback on failure)
- **container_update**: Update the pinned target container for future runs
- **compose_up**: Bring up Docker Compose services defined in .maestro/compose.yml

Use these when you discover missing dependencies (linters, packages, services) that block your work.
For typical application development, these tools are unnecessary.

All changes will be reviewed by the architect before merge.
```

---

## 5. Implementation Plan

### Phase 1: Tool Set Unification

1. Update `pkg/tools/constants.go`:
   - Add container tools to `AppCodingTools`
   - Add `ToolComposeUp` to `AppCodingTools`
   - Add `ToolContainerTest`, `ToolContainerList` to `AppPlanningTools`

2. Update capability tests in `pkg/tools/capability_test.go`

### Phase 2: Container Modification Tracking

1. Add state keys to `pkg/coder/coder_fsm.go`
2. Add state mutation logic to coder's tool execution handler
3. Update TESTING state to check `KeyContainerModified`

### Phase 3: Container Switch Recovery

1. Update `container_switch` tool with try/fallback logic
2. Ensure safe container ID available via config or agent context

### Phase 4: Claude Code Session Persistence

1. Update `docker_long_running.go` to create/mount Claude state volume
2. Update shutdown to clean up volumes
3. Add signal detection for `container_switch` in Claude runner
4. Add `handleContainerRestart()` and `waitForSessionFlush()` to Runner
5. Integration test for container restart with session resume

### Phase 5: Compose

1. Add path restriction guardrail to `compose_up` tool
2. Ensure project name isolation
3. Add compose cleanup to DONE and ERROR state handlers

### Phase 6: Prompt Updates

1. Update coder CODING state prompt with environment tools section

---

## 6. Acceptance Criteria

### A. Tool Access
- [ ] App stories can call `container_build`, `container_update`, `container_test`, `container_list`, `container_switch`
- [ ] App stories can call `compose_up`
- [ ] Tool access is not gated by story type

### B. Container Modification Tracking
- [ ] `container_update` success triggers state key updates (in coder handler, not tool)
- [ ] TESTING state checks `KeyContainerModified` to determine test strategy
- [ ] Stories that modify containers get container validation tests

### C. Container Switch Recovery
- [ ] `container_switch` attempts switch to target container
- [ ] On failure, automatically falls back to safe container
- [ ] Returns structured result indicating what happened

### D. Claude Code Session Persistence
- [ ] Docker volume `maestro-claude-<agentID>` created for Claude Code mode
- [ ] Volume mounted at `/tmp/.claude` in container
- [ ] Volume persists across container stop/start/remove
- [ ] Volume deleted on agent shutdown
- [ ] `container_switch` triggers graceful restart with `--resume`
- [ ] Session continues seamlessly after container switch

### E. Compose
- [ ] `compose_up` restricted to `/workspace` paths
- [ ] Project name derived from agent ID
- [ ] Compose stack torn down on DONE/ERROR

### F. Backwards Compatibility
- [ ] Existing DevOps stories continue to work
- [ ] Story type field preserved
- [ ] Native mode coders unaffected by Claude Code changes

---

## 7. File References

| Component | Path |
|-----------|------|
| Tool constants | `pkg/tools/constants.go` |
| Tool capability tests | `pkg/tools/capability_test.go` |
| Coder state keys | `pkg/coder/coder_fsm.go` |
| Coder testing state | `pkg/coder/testing.go` |
| Coder done state | `pkg/coder/done.go` |
| Container switch tool | `pkg/tools/container_switch.go` |
| Container update tool | `pkg/tools/container_update.go` |
| Compose up tool | `pkg/tools/compose_up.go` |
| Docker executor | `pkg/exec/docker_long_running.go` |
| Claude Runner | `pkg/coder/claude/runner.go` |
| Claude signal detection | `pkg/coder/claude/signal.go` |
| Coder prompts | `pkg/templates/coder/` |

---

## 8. Testing Strategy

### Integration Test: Claude Code Container Restart

```go
func TestClaudeCodeContainerRestart(t *testing.T) {
    // 1. Start Claude Code session in container A
    // 2. Have Claude Code call container_switch to container B
    // 3. Verify:
    //    - Docker volume persists
    //    - Claude Code restarts with --resume
    //    - ResumeInput contains status message
    //    - Claude Code can continue work
}
```

### Unit Tests
- Tool set membership
- Container modification tracking (state set in handler)
- Signal detection for container_switch
- Volume naming/lifecycle

---

## 9. TODO List

### Phase 1: Tool Set Unification
- [ ] `pkg/tools/constants.go`: Add container tools to `AppCodingTools`
- [ ] `pkg/tools/constants.go`: Add `ToolComposeUp` to `AppCodingTools`
- [ ] `pkg/tools/constants.go`: Add `ToolContainerTest`, `ToolContainerList` to `AppPlanningTools`
- [ ] `pkg/tools/capability_test.go`: Update tests for new tool sets

### Phase 2: Container Modification Tracking
- [ ] `pkg/coder/coder_fsm.go`: Add `KeyContainerModified` and `KeyNewContainerImage` constants
- [ ] `pkg/coder/coding.go` (or tool handler): Add state mutation on `container_update` success
- [ ] `pkg/coder/testing.go`: Replace `storyType == DevOps` with `KeyContainerModified` check

### Phase 3: Container Switch Recovery
- [ ] `pkg/tools/container_switch.go`: Add try/fallback logic
- [ ] `pkg/tools/container_switch.go`: Ensure safe container ID available
- [ ] `pkg/tools/container_switch.go`: Return structured success/fallback result

### Phase 4: Claude Code Session Persistence
- [ ] `pkg/exec/docker_long_running.go`: Add Claude state volume mount (`maestro-claude-<agentID>:/tmp/.claude`)
- [ ] `pkg/exec/docker_long_running.go`: Add volume cleanup in `Shutdown()`
- [ ] `pkg/coder/claude/signal.go`: Add `container_switch` detection
- [ ] `pkg/coder/claude/runner.go`: Add `handleContainerRestart()` method
- [ ] `pkg/coder/claude/runner.go`: Add `waitForSessionFlush()` method
- [ ] `pkg/coder/claude/runner.go`: Add container switch callback mechanism
- [ ] `tests/integration/`: Add Claude Code container restart test

### Phase 5: Compose
- [ ] `pkg/tools/compose_up.go`: Add `/workspace` path restriction
- [ ] `pkg/tools/compose_up.go`: Ensure project name from agent ID
- [ ] `pkg/coder/done.go`: Add compose cleanup call
- [ ] `pkg/coder/error.go`: Add compose cleanup call (or shared cleanup function)

### Phase 6: Prompt Updates
- [ ] `pkg/templates/coder/app_coding.tpl.md`: Add environment tools section
- [ ] `pkg/templates/coder/devops_coding.tpl.md`: Review/align with app coding

### Phase 7: Testing & Validation
- [ ] Run existing tests to verify no regressions
- [ ] Manual test: app story using container_build → container_switch
- [ ] Manual test: app story using compose_up
- [ ] Manual test: Claude Code mode container restart with session resume
