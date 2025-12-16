# Demo Without Compose - Specification

## Overview

Enable demo mode to work without requiring a docker-compose file. Simple apps should be able to demo by running directly in the PM's development container, with compose reserved for when additional services (databases, caches, etc.) are needed.

## Current State

- `startContainerOnly()` in `pkg/demo/service.go` is a stub that throws an error requiring compose
- Demo mode only available after spec/interview completion
- Bootstrap prompt doesn't require `EXPOSE` directive in Dockerfile

## Requirements

### 1. Implement `startContainerOnly()`

**Location**: `pkg/demo/service.go`

**Behavior**:
1. Execute `Build.Build` command inside PM's dev container (build the app)
2. Execute `Build.Run` command inside PM's dev container (start the app)
3. Use ports already exposed by the Dockerfile (`EXPOSE` directives)

**Error handling**:
- If build fails, return error with build output
- If run fails to start, return error with details
- If no `EXPOSE` ports in Dockerfile, warn but don't fail (app may still work)

### 2. Enable Demo After Bootstrap

**Current gate**: Demo requires spec + interview completion

**New gate**: Demo available once bootstrap succeeds (working Dockerfile exists)

**Location**: Likely in `pkg/pm/` or `pkg/demo/` where demo availability is checked

### 3. Update Bootstrap Prompt - EXPOSE Requirement

**Location**: Bootstrap prompt templates in `pkg/templates/bootstrap/` or `pkg/pm/`

**Change**: Add `EXPOSE` directive to Dockerfile acceptance criteria

**Requirement text** (or similar):
> The Dockerfile MUST include an `EXPOSE` directive for the port the application listens on. This enables demo mode to work correctly.

## Implementation Notes

### Container Execution

The PM has its own dev container. `startContainerOnly()` should:
- Use existing container infrastructure to exec commands
- Run build synchronously, wait for completion
- Run the app (may need to run in background or handle long-running process)

### Port Handling

- Rely on Dockerfile `EXPOSE` directives
- Docker automatically makes exposed ports available when container runs
- No additional port configuration needed in maestro config

### Compose Remains Available

This change doesn't remove compose support. The logic remains:
```go
if ComposeFileExists(workspacePath) {
    return s.startWithCompose(ctx, composePath)
} else {
    return s.startContainerOnly(ctx)  // Now actually works
}
```

## Acceptance Criteria

1. [x] Demo starts successfully without compose file when Dockerfile has `EXPOSE`
2. [x] Build command runs before run command
3. [x] Demo available immediately after bootstrap (no spec required)
4. [x] Bootstrap prompt requires `EXPOSE` in Dockerfile
5. [x] Existing compose-based demos continue to work unchanged
6. [x] WebUI shows demo controls after bootstrap completes
7. [x] WebUI handles no-compose demo gracefully (no missing service info errors)

## Out of Scope (Phase 2)

- Port mismatch detection in TESTING state
- Automatic Dockerfile fixes for missing `EXPOSE`
- Detection of app listening on unexposed ports

### 4. WebUI Updates

**Location**: `pkg/webui/`

**Changes needed**:
- Ensure demo controls appear after bootstrap (not just after spec/interview)
- Handle the case where demo runs without compose (no services to show)
- Any status/state display changes needed for the new demo availability gate

## Files to Modify

1. `pkg/demo/service.go` - Implement `startContainerOnly()`
2. `pkg/pm/*.go` or `pkg/demo/*.go` - Change demo availability gate
3. `pkg/templates/bootstrap/*.go` or `pkg/pm/bootstrap*.go` - Add EXPOSE requirement to prompt
4. `pkg/webui/*.go` - Update demo availability in UI, handle no-compose case
