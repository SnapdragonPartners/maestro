# Unified Coder Tools Spec

**Author:** Claude (with Dan)
**Date:** 2026-01-30
**Status:** Implemented
**Branch:** `devops_tool_refactor`

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

Introduce state variables to track whether the story modified container configuration.

**State keys** in `pkg/coder/coder_fsm.go`:

```go
const (
    KeyContainerModified = "container_modified"      // bool: was container_update called?
    KeyNewContainerImage = "new_container_image"     // string: image ID from container_update
    KeyDockerfileHash    = "dockerfile_hash"         // string: SHA256 hash of Dockerfile when built
)
```

**State mutation** happens in `SetPendingContainerConfig()` which is called by the `container_update` tool:

```go
func (c *Coder) SetPendingContainerConfig(name, dockerfile, imageID, dockerfileHash string) {
    // ... store pending config fields ...
    c.BaseStateMachine.SetStateData(KeyContainerModified, true)
    c.BaseStateMachine.SetStateData(KeyNewContainerImage, imageID)
    c.BaseStateMachine.SetStateData(KeyDockerfileHash, dockerfileHash)
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

### 4.4 Dockerfile Hash Verification

**Problem**: Concurrent agents may modify the same Dockerfile. If Agent A and B both modify, build, and call `container_update`, then one merges first and the other resolves merge conflicts, the second agent's pending config could pin an image built from stale Dockerfile content.

**Solution**: Hash verification with auto-rebuild in TESTING state.

1. **On `container_update`**: Compute SHA256 hash of Dockerfile content, store with pending config
2. **In TESTING state**: Compare current Dockerfile hash with stored hash
3. **If mismatch**: Auto-rebuild container from current Dockerfile, update pending config
4. **If rebuild fails**: Return to CODING with error message

```go
func (c *Coder) verifyAndRebuildIfNeeded(ctx context.Context, workspacePath, containerName,
    dockerfilePath, storedHash string) (needsRebuild bool, err error) {

    if storedHash == "" {
        return false, nil // No hash stored, skip verification
    }

    // Compute current Dockerfile hash
    currentHash := computeDockerfileHash(workspacePath, dockerfilePath)

    if currentHash == storedHash {
        return false, nil // Hash matches, no rebuild needed
    }

    // Dockerfile changed - rebuild
    logger.Warn("Dockerfile modified since container was built, rebuilding...")

    if err := rebuildContainer(ctx, containerName, dockerfilePath); err != nil {
        return false, err // Rebuild failed
    }

    // Update pending config with new image ID and hash
    newImageID := getImageID(containerName)
    c.SetPendingContainerConfig(containerName, dockerfilePath, newImageID, currentHash)

    return true, nil // Rebuild succeeded
}
```

### 4.5 Container Switch with Recovery

The `container_switch` tool includes built-in fallback to safe container on failure. Uses `config.BootstrapContainerTag` as the fallback.

### 4.6 Claude Code Session Persistence

#### 4.6.1 Problem

Currently, containers set `HOME=/tmp` with `/tmp` as a tmpfs mount. Claude Code sessions go to `$HOME/.claude/` which doesn't survive container recreation (e.g., after `container_switch`).

#### 4.6.2 Solution: Docker Named Volume

Use a Docker named volume to persist **only** the Claude state directory:

```go
// In StartContainer, for Claude Code mode:
if opts.ClaudeCodeMode && d.agentID != "" {
    volumeName := fmt.Sprintf("maestro-claude-%s", d.agentID)
    args = append(args, "--volume", fmt.Sprintf("%s:/tmp/.claude:rw", volumeName))
}
```

**Volume lifecycle:**
- Created automatically by Docker when container starts
- Persists across container stop/start/remove
- Cleaned up in `Shutdown()` method

#### 4.6.3 Container Switch Signal Detection

Added `SignalContainerSwitch` to the Claude runner's signal detection:

```go
var signalToolNames = map[string]Signal{
    tools.ToolSubmitPlan:      SignalPlanComplete,
    tools.ToolDone:            SignalDone,
    tools.ToolAskQuestion:     SignalQuestion,
    tools.ToolStoryComplete:   SignalStoryComplete,
    tools.ToolContainerSwitch: SignalContainerSwitch,  // NEW
}
```

Note: Full container restart flow (handleContainerRestart, waitForSessionFlush) deferred - requires more testing with live containers.

### 4.7 Compose: Single Tool + Guardrails

**Coder tool**: Only `compose_up` is exposed to coders.

**Project name isolation**: Derive compose project name from agent ID:

```go
projectName := "maestro-" + c.agentID
```

**Compose cleanup**: Runs automatically on DONE and ERROR state entry via `cleanupComposeStack()`.

### 4.8 Prompt Guidance

Updated coder prompts with environment tools section:

```markdown
### Environment Tools (Container & Compose)

Container and compose tools are available when you encounter genuine environment prerequisites:

- **container_build**: Build a new container image from Dockerfile changes
- **container_test**: Verify a container image works correctly
- **container_switch**: Switch to a different container (has automatic fallback on failure)
- **container_update**: Update the pinned target container for future runs
- **container_list**: List available containers and their status
- **compose_up**: Bring up Docker Compose services defined in .maestro/compose.yml

**Use these when** you discover missing dependencies (linters, packages, database services) that block your work.
**For typical application development**, these tools are unnecessary.
```

---

## 5. Implementation Summary

### Commits

1. `4823af0` - Add unified coder tools spec
2. `1990b29` - Phase 1: Unify tool sets for app and devops stories
3. `594de1e` - Phase 2: Container modification tracking via state keys
4. `6df57d9` - Phase 3: Add bootstrap container constant for container_switch fallback
5. `eb05780` - Phase 4: Claude Code session persistence and container_switch detection
6. `d9716a4` - Phase 5: Compose project name isolation and cleanup naming fix
7. `da63f99` - Phase 6: Update coder prompts with environment tools guidance
8. `fcc261e` - Add Dockerfile hash verification for concurrent edit protection
9. `977cf11` - Update unified coder tools spec to reflect completed implementation
10. `06ff237` - Add path validation to compose_up tool for security
11. `7a3a850` - Implement container restart flow for Claude Code mode
12. `238496d` - Add e2e test for unified coder tools container workflow

### Files Modified

| Component | Path | Changes |
|-----------|------|---------|
| Tool constants | `pkg/tools/constants.go` | Added container/compose tools to app tool sets |
| Tool capability tests | `pkg/tools/capability_test.go` | Updated for new tool sets |
| Tool registry | `pkg/tools/registry.go` | Updated Agent interface for hash parameter |
| Coder state keys | `pkg/coder/coder_fsm.go` | Added KeyContainerModified, KeyNewContainerImage, KeyDockerfileHash |
| Coder driver | `pkg/coder/driver.go` | Updated SetPendingContainerConfig/GetPendingContainerConfig |
| Coder testing | `pkg/coder/testing.go` | Hash verification and auto-rebuild logic |
| Coder terminal states | `pkg/coder/terminal_states.go` | Fixed compose cleanup naming |
| Coder setup | `pkg/coder/setup.go` | Added ClaudeCodeMode option |
| Container switch | `pkg/tools/container_switch.go` | Use config.BootstrapContainerTag for fallback |
| Container update | `pkg/tools/container_update.go` | Compute and store Dockerfile hash |
| Compose up | `pkg/tools/compose_up.go` | Added agentID for project isolation |
| Docker executor | `pkg/exec/docker_long_running.go` | Claude state volume mount |
| Executor options | `pkg/exec/executor.go` | Added ClaudeCodeMode field |
| Claude types | `pkg/coder/claude/types.go` | Added SignalContainerSwitch, ContainerSwitchTarget |
| Claude signals | `pkg/coder/claude/signals.go` | Added container_switch to signal detection |
| Coder prompts | `pkg/templates/coder/*.tpl.md` | Environment tools section |

---

## 6. Acceptance Criteria

### A. Tool Access
- [x] App stories can call `container_build`, `container_update`, `container_test`, `container_list`, `container_switch`
- [x] App stories can call `compose_up`
- [x] Tool access is not gated by story type

### B. Container Modification Tracking
- [x] `container_update` success triggers state key updates
- [x] TESTING state checks `KeyContainerModified` to determine test strategy
- [x] Stories that modify containers get container validation tests

### C. Container Switch Recovery
- [x] `container_switch` attempts switch to target container
- [x] On failure, automatically falls back to safe container (config.BootstrapContainerTag)
- [x] Returns structured result indicating what happened

### D. Claude Code Session Persistence
- [x] Docker volume `maestro-claude-<agentID>` created for Claude Code mode
- [x] Volume mounted at `/tmp/.claude` in container
- [x] Volume persists across container stop/start/remove
- [x] Volume deleted on agent shutdown
- [x] `container_switch` signal detection added
- [x] Full container restart flow with `--resume` implemented

### E. Compose
- [x] Project name derived from agent ID (`maestro-<agentID>`)
- [x] Compose stack torn down on DONE/ERROR
- [x] Cleanup naming matches compose_up naming

### F. Dockerfile Hash Verification
- [x] Hash computed on `container_update` call
- [x] Hash verified in TESTING state before container tests
- [x] Auto-rebuild on hash mismatch
- [x] Pending config updated with new image ID and hash after rebuild

### G. Backwards Compatibility
- [x] Existing DevOps stories continue to work
- [x] Story type field preserved
- [x] Native mode coders unaffected by Claude Code changes

---

## 7. Testing

### Automated Tests

1. **E2E test** (`tests/integration/container_workflow_e2e_test.go`): Tests app story container workflow
   - Container build for app stories
   - Container update with Dockerfile hash computation
   - Container modification tracking via state keys
   - Container switch signal detection
   - Tool access verification for app stories

2. **Path validation test** (`pkg/tools/compose_tools_test.go`): Tests compose_up path validation

### Manual Testing Required

1. **Manual test**: Concurrent Dockerfile edit scenario → verify hash mismatch triggers rebuild
2. **Manual test**: Live Claude Code session with container switch and resume

### Completed Work

1. **Full container restart flow for Claude Code**: Implemented `performContainerSwitch()` method and SignalContainerSwitch handling in both planning and coding states. Container switch triggers re-entry to current state with session resume.

2. **Path validation for compose_up**: Added explicit path validation ensuring workspace is absolute and compose file stays within `.maestro/` directory.

---

## 8. File References

| Component | Path |
|-----------|------|
| Tool constants | `pkg/tools/constants.go` |
| Tool capability tests | `pkg/tools/capability_test.go` |
| Tool registry | `pkg/tools/registry.go` |
| Coder state keys | `pkg/coder/coder_fsm.go` |
| Coder testing state | `pkg/coder/testing.go` |
| Coder terminal states | `pkg/coder/terminal_states.go` |
| Container switch tool | `pkg/tools/container_switch.go` |
| Container update tool | `pkg/tools/container_update.go` |
| Compose up tool | `pkg/tools/compose_up.go` |
| Claude Code coding | `pkg/coder/claudecode_coding.go` |
| Claude Code planning | `pkg/coder/claudecode_planning.go` |
| E2E test | `tests/integration/container_workflow_e2e_test.go` |
| Docker executor | `pkg/exec/docker_long_running.go` |
| Executor options | `pkg/exec/executor.go` |
| Claude Runner | `pkg/coder/claude/runner.go` |
| Claude types | `pkg/coder/claude/types.go` |
| Claude signals | `pkg/coder/claude/signals.go` |
| Coder prompts | `pkg/templates/coder/` |
