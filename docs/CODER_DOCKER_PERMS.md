# Coder Docker Permissions Analysis

## Problem Statement

We need Claude Code to run as a non-root user (UID 1000) so that `--dangerously-skip-permissions` will work. Additionally, running all coder operations as non-privileged users is a security best practice.

**Current Issue**: If Claude Code runs as UID 1000 and tries to use `container_build` or other docker-related tools, those commands will fail because:
1. Docker commands inside the container require root or docker group membership
2. The coder container doesn't have docker.sock mounted (for security)
3. Even with docker.sock, the non-root user wouldn't have permission to use it

**Key Insight**: Since the MCP server runs on the **host**, not inside the container, container_* tools could use a local executor to run docker commands directly on the host. This eliminates the need for:
- docker.sock mounting in coder containers
- Root privileges for DevOps stories
- Docker group membership inside containers

## Current Architecture

```
Claude Code (in container, UID 1000)
  → MCP proxy (in container)
  → TCP to host MCP server (on host)
  → tool.Execute() with CONTAINER executor  <-- PROBLEM
  → docker exec <container> docker buildx...  <-- Fails without root/docker.sock
```

## Proposed Architecture

```
Claude Code (in container, UID 1000)
  → MCP proxy (in container)
  → TCP to host MCP server (on host)
  → tool.Execute() with LOCAL executor  <-- SOLUTION
  → docker buildx... (runs directly on host)  <-- Works!
```

## Container Tools Analysis

### Analysis Results

| Tool | Uses Docker CLI | Current Executor | Can Use Local | Status | Notes |
|------|-----------------|------------------|---------------|--------|-------|
| container_build | Yes (`docker buildx build`, `docker build`) | Container (`c.executor`) | **Yes** | NEEDS UPDATE | All commands are `docker ...` |
| container_test | Yes (`docker run`, `docker inspect`) | **Already Local** (`HostRunner`, `exec.NewLocalExec()`) | Already done | ✅ DONE | Uses `HostRunner.RunContainerTest()` |
| container_switch | Yes (via `ValidateContainerCapabilities`) | Container (`c.executor`) | **Yes** | NEEDS UPDATE | Only uses executor for validation |
| container_update | Yes (`docker inspect`) | Mixed - Local for validation, Container for inspect | **Yes** | NEEDS UPDATE | Line 84 uses `exec.NewLocalExec()`, line 149 uses `c.executor` |
| container_list | Yes (`docker ps`) | Container (`c.executor`) | **Yes** | NEEDS UPDATE | Simple `docker ps` command |
| container_common | Yes (`docker run --rm ... git/gh`) | Passed in | **Yes** | N/A | `ValidateContainerCapabilities` uses whatever executor is passed |

### Detailed Analysis

#### container_build.go
- **Lines 196, 220**: `c.executor.Run(ctx, []string{"docker", "buildx/build", ...})`
- **Uses WorkDir**: Yes, passes `cwd` for docker build context
- **Fix**: Replace `c.executor` with local executor
- **Consideration**: WorkDir is the build context path on host

#### container_test_tool.go
- **Already fixed!** Uses `HostRunner` (line 28, 139)
- `HostRunner.RunContainerTest()` uses `exec.CommandContext()` directly
- `ValidateContainerCapabilities` called with `exec.NewLocalExec()` (execution.go:248)

#### container_switch.go
- **Line 108**: `ValidateContainerCapabilities(ctx, c.executor, containerName)`
- **Fix**: Pass `exec.NewLocalExec()` instead of `c.executor`
- **No other docker commands** - just config updates

#### container_update.go
- **Line 84**: Already uses `exec.NewLocalExec()` for validation ✅
- **Line 149**: Uses `c.executor.Run()` for `docker inspect` ❌
- **Fix**: Use local executor for docker inspect too

#### container_list.go
- **Line 79**: `c.executor.Run(ctx, dockerArgs, ...)` for `docker ps`
- **Fix**: Use local executor

#### container_common.go (ValidateContainerCapabilities)
- **Lines 82, 93, 169, 176**: Uses passed executor for `docker run --rm` commands
- **Already compatible**: Caller determines executor
- **Already using local in container_test**: execution.go:248 passes `exec.NewLocalExec()`

## Implementation Plan

### Phase 1: Tool Transition (Simple - just change executor usage)

**container_build.go**:
```go
// Change from:
func NewContainerBuildTool(executor exec.Executor) *ContainerBuildTool {
    return &ContainerBuildTool{executor: executor}
}

// To:
func NewContainerBuildTool() *ContainerBuildTool {
    return &ContainerBuildTool{executor: exec.NewLocalExec()}
}
```

**container_switch.go**:
```go
// Change from:
validationResult := ValidateContainerCapabilities(ctx, c.executor, containerName)

// To:
validationResult := ValidateContainerCapabilities(ctx, exec.NewLocalExec(), containerName)
```

**container_update.go**:
```go
// Change line 149 from:
result, err := c.executor.Run(ctx, []string{"docker", "inspect", ...}

// To use local executor (already have hostExecutor from line 84)
```

**container_list.go**:
```go
// Change to use local executor
```

### Phase 2: Permission Changes

- [x] Claude Code runner sets `User: "1000:1000"` (already done in runner.go)
- [x] `--dangerously-skip-permissions` enabled for both modes (already done)
- [ ] Remove root user exception for DevOps stories in setup.go
- [ ] Update bootstrap Dockerfile (already has UID 1000 user)
- [ ] Update bootstrap story template to remove docker.sock requirements
- [ ] Update README.md (references running containers as root)

### Phase 3: Registry Updates

**registry.go** - Simplify tool creation (no executor needed for docker tools):
```go
func createContainerBuildTool(ctx *AgentContext) (Tool, error) {
    return NewContainerBuildTool(), nil  // No executor needed
}
```

### Phase 4: Testing

- [ ] Test container_build with local executor
- [ ] Test container_test (already works)
- [ ] Test container_switch with local executor
- [ ] Test container_update with local executor
- [ ] Test container_list with local executor
- [ ] Test full DevOps story workflow as non-root
- [ ] Verify Claude Code mode works end-to-end

## Files to Modify

### Core Changes
- `pkg/tools/container_build.go` - Use local executor
- `pkg/tools/container_switch.go` - Use local executor for validation
- `pkg/tools/container_update.go` - Use local executor for docker inspect
- `pkg/tools/container_list.go` - Use local executor
- `pkg/tools/registry.go` - Simplify tool creation

### Permission Changes
- `pkg/coder/setup.go` - Remove root user exception for DevOps stories
- `pkg/templates/bootstrap/bootstrap.tpl.md` - Remove docker.sock requirements (partially done)
- `README.md` - Update container user documentation

### Already Done
- `pkg/dockerfiles/bootstrap.dockerfile` - Already has UID 1000 user
- `pkg/coder/claude/runner.go` - Already sets `User: "1000:1000"`
- `pkg/coder/claude/installer.go` - Already has `EnsureCoderUser()`
- `pkg/tools/container_test_tool.go` - Already uses HostRunner
- `pkg/tools/execution.go` - Already has HostRunner implementation

## Risk Assessment

- **Low Risk**: All changes are straightforward executor swaps
- **No Breaking Changes**: Tools keep same interface, just different internal executor
- **Already Proven**: container_test already uses this pattern successfully
- **Backward Compatible**: Standard coder mode unaffected (uses different code path)

## Summary

The fix is straightforward because:
1. `container_test` already demonstrates the pattern (uses `HostRunner` + `exec.NewLocalExec()`)
2. All container tools just run docker CLI commands
3. Docker CLI works the same whether called from container or host
4. The MCP server already runs on host, so this is the natural execution location

**Estimated effort**: 1-2 hours for tool changes + testing
