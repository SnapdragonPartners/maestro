# Build Service Containerization Specification

## Problem Statement

The build service (`pkg/build/`) currently executes build, test, and lint operations **directly on the host machine** rather than inside the coder's container. This creates two critical issues:

### 1. Security Vulnerability

Coder-generated code executes directly on the host with full host privileges:
- Malicious or buggy code can access host filesystem, network, and processes
- No isolation between coders - one coder's tests could affect another's workspace
- Bypasses all container security constraints (read-only filesystem, network isolation, resource limits)

### 2. Environment Mismatch (Correctness Bug)

Tests validate against the wrong environment:
- Tests pass/fail based on host-installed tools, not container tools
- Container configuration issues are masked (as discovered: `make` missing from container but tests passed)
- Host may have different tool versions than container
- Results are non-reproducible across different host machines

### Evidence from Production

From the rc2 run logs:
```
# TESTING state runs on HOST - passes because host has make
[build-service] INFO: Build request test operation for /Users/dratner/Code/maestro-work/rc2/coder-002
[coder-002] INFO: Tests completed successfully via build service

# Coder shell commands run in CONTAINER - fails because container lacks make
[docker] WARN: Docker exec command failed: docker exec -i maestro-story-coder-002 sh -c make lint
[coder-002] ERROR: shell command failed: make lint (exit code: 127)
```

The automated tests passed while the actual working environment was broken.

## Core Invariant

**No code under `pkg/build` may call `os/exec` in production paths.**

This invariant must be enforced via:
1. A lint rule or unit test that fails if `pkg/build` imports `os/exec`
2. Code review policy requiring justification for any `exec.Command` usage

This prevents regressions where someone adds a "quick command" and reopens the security hole.

## Current Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Orchestrator (Host)                       │
│  ┌─────────────────────────────────────────────────────────┐│
│  │              Build Service                               ││
│  │  - Detects backend (Go, Node, Python, Make)             ││
│  │  - Executes: make test, go test, npm test, etc.         ││
│  │  - Uses os/exec.Command ← SECURITY VULNERABILITY        ││
│  │  - Runs DIRECTLY ON HOST ← CORRECTNESS BUG              ││
│  └─────────────────────────────────────────────────────────┘│
│                           │                                  │
│                           ▼                                  │
│              Host Filesystem (workspace dirs)                │
│              /Users/.../maestro-work/rc2/coder-001/         │
└─────────────────────────────────────────────────────────────┘
                            │
            ┌───────────────┼───────────────┐
            ▼               ▼               ▼
    ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
    │  Container   │ │  Container   │ │  Container   │
    │  coder-001   │ │  coder-002   │ │  coder-003   │
    │  (idle)      │ │  (idle)      │ │  (idle)      │
    └──────────────┘ └──────────────┘ └──────────────┘
```

## Proposed Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Orchestrator (Host)                       │
│  ┌─────────────────────────────────────────────────────────┐│
│  │              Build Service                               ││
│  │  - Detects backend (Go, Node, Python, Make)             ││
│  │  - Delegates execution to ContainerExecutor             ││
│  │  - NO os/exec imports (enforced by lint/test)           ││
│  │  - Receives results via stdout/stderr                   ││
│  └─────────────────────────────────────────────────────────┘│
│                           │                                  │
│                    docker exec                               │
│                           │                                  │
└───────────────────────────┼─────────────────────────────────┘
            ┌───────────────┼───────────────┐
            ▼               ▼               ▼
    ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
    │  Container   │ │  Container   │ │  Container   │
    │  coder-001   │ │  coder-002   │ │  coder-003   │
    │  RUNS TESTS  │ │  RUNS TESTS  │ │  RUNS TESTS  │
    └──────────────┘ └──────────────┘ └──────────────┘
```

## Path and Identity Semantics

The build service operates with two distinct path concepts:

- **`ProjectRoot` (host path)**: Identity only - used for cache keys, logging, and selecting the coder container. Example: `/Users/.../maestro-work/rc2/coder-001/`

- **`ExecRoot` (container path)**: Always `/workspace` - where commands execute inside the container. This is constant across all coders.

**Rules:**
1. Backend detection may read host filesystem under `ProjectRoot` (files are bind-mounted)
2. Executor always runs commands under `ExecRoot` (`/workspace`)
3. Any path arguments passed to the executor must be **container paths**, not host paths
4. `ProjectRoot` must be normalized (`filepath.EvalSymlinks`, `filepath.Clean`) before use as cache key or container selector
5. The service must verify exactly one running coder container corresponds to `ProjectRoot` before executing; 0 or >1 matches is a hard error

**Assumption:** Host path under `ProjectRoot` is bind-mounted into the container's `/workspace` with identical contents at detection and execution time.

## Design

### Option A: Pass Container Reference to Build Service (Recommended)

The build service receives a container executor/reference and uses it for all operations.

**Pros:**
- Clean separation of concerns
- Build service remains testable with mock executors
- Explicit about execution context
- Enforces "no os/exec" invariant naturally

**Cons:**
- Requires plumbing container reference through call chain

### Option B: Build Service Manages Its Own Container Execution

Build service directly calls `docker exec` using container name from config.

**Cons:**
- Build service gains Docker dependency
- Harder to test
- Duplicates container execution logic
- Easier to accidentally add host exec paths

### Recommendation: Option A

## Executor Interface

```go
// pkg/build/executor.go

// ExecOpts configures command execution.
type ExecOpts struct {
    // Dir is the working directory inside the container (e.g., "/workspace").
    // Required. Must be a container path, not a host path.
    Dir string

    // Env contains environment variable overrides.
    // These are merged with the container's existing environment.
    // Optional.
    Env map[string]string

    // Stdout receives standard output. Required.
    Stdout io.Writer

    // Stderr receives standard error. Can be same as Stdout for combined output.
    // Required.
    Stderr io.Writer
}

// Executor runs commands and returns results.
type Executor interface {
    // Run executes a command with the given arguments.
    //
    // argv is the command and arguments as a string slice (NOT a shell string).
    // This prevents shell injection vulnerabilities.
    //
    // Returns the exit code and any execution error.
    // Exit code is valid even when err != nil (e.g., command ran but returned non-zero).
    //
    // Context cancellation must terminate the running command and return
    // context.Canceled or context.DeadlineExceeded as appropriate.
    Run(ctx context.Context, argv []string, opts ExecOpts) (exitCode int, err error)
}

// ContainerExecutor runs commands inside a Docker container via docker exec.
type ContainerExecutor struct {
    ContainerName string
    DockerClient  DockerClient // existing pkg/docker abstraction
}

// HostExecutor runs commands on the host.
// ONLY for use in tests and migration. Must not be used in production.
type HostExecutor struct{}
```

### Execution Requirements

1. **No shell wrapping**: Commands must be executed as argv arrays, not via `sh -c "string"`. This prevents shell injection. Docker exec supports argv-style execution directly.

2. **Shell only when justified**: If a specific backend requires shell features (pipes, redirects), it must be explicitly documented and isolated to that backend with strict input validation.

3. **Output prefix**: The executor (or wrapper) must emit a command prefix for debugging:
   ```
   $ make test
   ```
   Include container name/ID when relevant. Do not leak environment variables.

4. **Cancellation contract**: When `ctx.Done()` fires:
   - The executor must terminate the running command
   - It must kill the entire process group (not just the parent)
   - It must return `context.Canceled` or `context.DeadlineExceeded` as the error
   - Docker exec hang must be handled (timeout on the exec call itself)

5. **Exit code semantics**:
   - Exit code 0 = success
   - Exit code non-zero = command failed (but executed)
   - `err != nil` with no exit code = execution failed (couldn't run command)

## Implementation Plan

### Phase 1: Define Executor Interface and Invariant Enforcement

1. Create `pkg/build/executor.go` with the interface above
2. Add `pkg/build/no_exec_test.go` that fails if `pkg/build` imports `os/exec`:
   ```go
   func TestNoOsExecImport(t *testing.T) {
       // Use go/parser to scan pkg/build/*.go for "os/exec" imports
       // Fail if found in any non-test file
   }
   ```
3. Implement `HostExecutor` (wraps current behavior, for migration only)
4. Implement `ContainerExecutor` using existing `pkg/docker` abstractions

### Phase 2: Refactor Build Service to Use Executor

Modify `Service` to accept an `Executor`:

```go
// pkg/build/build_api.go

type Service struct {
    buildRegistry *Registry
    logger        *logx.Logger
    projectCache  map[string]*ProjectInfo
    executor      Executor  // NEW: injected executor
}

func NewBuildService(executor Executor) *Service {
    return &Service{
        // ...
        executor: executor,
    }
}
```

### Phase 3: Refactor Backends to Use Executor

Each backend (Go, Node, Python, Make) currently uses `runMakeCommand` which calls `exec.Command`. Refactor to use the injected executor:

```go
// pkg/build/go_backend.go

func (g *GoBackend) Test(ctx context.Context, root string, opts ExecOpts, exec Executor) error {
    _, _ = fmt.Fprintf(opts.Stdout, "$ make test\n")

    exitCode, err := exec.Run(ctx, []string{"make", "test"}, opts)
    if err != nil {
        return fmt.Errorf("execution failed: %w", err)
    }
    if exitCode != 0 {
        return fmt.Errorf("make test failed with exit code %d", exitCode)
    }
    return nil
}
```

Remove `runMakeCommand` helper and all `os/exec` imports from `pkg/build`.

### Phase 4: Wire Container Executor in Coder

In the coder's TESTING state handler, create a `ContainerExecutor` with the coder's container:

```go
// pkg/coder/testing.go

func (c *Coder) handleAppStoryTesting(ctx context.Context, sm *agent.BaseStateMachine, workspacePathStr string) (proto.State, bool, error) {
    // Create executor that runs in coder's container
    containerExec := build.NewContainerExecutor(c.containerName, c.dockerClient)

    // Build service uses container executor
    // ExecRoot is always /workspace inside container
    c.buildService.SetExecutor(containerExec)

    // ... rest of testing logic (ProjectRoot used for cache/identity only)
}
```

### Phase 5: Update Container Requirements

Already completed - bootstrap template now requires `make`:
- `pkg/templates/bootstrap/bootstrap.tpl.md` updated
- `pkg/templates/bootstrap/testdata/golden_go.md` updated
- `pkg/templates/bootstrap/testdata/golden_generic.md` updated

## Files to Modify

1. **pkg/build/executor.go** (NEW) - Executor interface and implementations
2. **pkg/build/no_exec_test.go** (NEW) - Lint test for os/exec imports
3. **pkg/build/build_api.go** - Accept and use Executor, remove runMakeCommand
4. **pkg/build/go_backend.go** - Use Executor instead of direct exec
5. **pkg/build/node_backend.go** - Use Executor instead of direct exec
6. **pkg/build/python_backend.go** - Use Executor instead of direct exec
7. **pkg/build/make_backend.go** - Use Executor instead of direct exec
8. **pkg/coder/testing.go** - Wire ContainerExecutor

## Testing Strategy

1. **Invariant test**: `no_exec_test.go` fails if `os/exec` imported in `pkg/build`
2. **Unit tests**: Mock Executor to verify backends pass correct argv, dir, env
3. **Integration tests**: Verify ContainerExecutor properly runs in container
4. **Regression test**: Verify that missing `make` in container causes test failure (not silent pass)
5. **Cancellation test**: Verify context cancellation terminates container exec

## Rollout Plan

1. Implement Executor interface with HostExecutor (no behavior change)
2. Add `no_exec_test.go` invariant enforcement
3. Add ContainerExecutor implementation
4. Feature flag to switch between host/container execution
5. Validate in staging environment
6. Enable container execution by default
7. Remove HostExecutor after validation period

## Success Criteria

- [ ] All build/test/lint operations execute inside coder containers
- [ ] Missing tools in container cause clear test failures
- [ ] No code from coders executes on host machine
- [ ] `no_exec_test.go` passes (no os/exec in pkg/build)
- [ ] Existing test suite passes
- [ ] Performance impact < 10% (container exec overhead)
- [ ] Context cancellation properly terminates container exec

## Security Considerations

- Container execution inherits existing security constraints (read-only, no network, resource limits)
- Build service no longer has direct code execution capability on host
- No shell injection surface (argv-style execution)
- Reduces attack surface significantly

## Resolved Questions

1. **Timeout handling**: Container execution uses same timeouts as host; context cancellation terminates exec
2. **Output streaming**: Stream output in real-time via io.Writer (same as current behavior)
3. **Error messages**: Exit code 127 = "command not found" (tool missing); other non-zero = "command failed"

---

## TODO List

### Phase 1: Foundation
- [ ] Create `pkg/build/executor.go` with `Executor` interface and `ExecOpts` struct
- [ ] Create `pkg/build/no_exec_test.go` to enforce no `os/exec` imports
- [ ] Implement `ContainerExecutor` using `pkg/docker` abstractions
- [ ] Implement `HostExecutor` for testing/migration (marked as deprecated)
- [ ] Add unit tests for both executor implementations

### Phase 2: Build Service Refactor
- [ ] Modify `Service` struct to accept `Executor`
- [ ] Update `NewBuildService()` constructor
- [ ] Add `SetExecutor()` method for runtime configuration
- [ ] Remove `runMakeCommand()` helper function
- [ ] Remove `os/exec` import from `build_api.go`

### Phase 3: Backend Refactor
- [ ] Refactor `GoBackend` to use Executor
- [ ] Refactor `NodeBackend` to use Executor
- [ ] Refactor `PythonBackend` to use Executor
- [ ] Refactor `MakeBackend` to use Executor
- [ ] Remove all `os/exec` imports from backend files
- [ ] Update backend tests to use mock Executor

### Phase 4: Coder Integration
- [ ] Update `handleAppStoryTesting()` to create and inject `ContainerExecutor`
- [ ] Ensure `ProjectRoot` (host) vs `ExecRoot` (container) distinction is clear
- [ ] Add path normalization before cache key usage
- [ ] Add container existence validation before execution

### Phase 5: Testing & Validation
- [ ] Add integration test: missing tool causes test failure
- [ ] Add integration test: context cancellation terminates exec
- [ ] Add integration test: output streaming works correctly
- [ ] Verify `no_exec_test.go` catches violations
- [ ] Performance benchmark: measure container exec overhead

### Phase 6: Rollout
- [ ] Add feature flag for host vs container execution
- [ ] Deploy with flag disabled (host execution)
- [ ] Enable flag in staging, validate
- [ ] Enable flag in production
- [ ] Remove `HostExecutor` and feature flag after validation period

### Already Completed
- [x] Update bootstrap template to require `make` (`bootstrap.tpl.md`)
- [x] Update golden test files (`golden_go.md`, `golden_generic.md`)
