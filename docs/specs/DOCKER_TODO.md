# Docker Sandboxing Implementation TODO

## Overview
Implement Docker-based sandboxing for AI coder agents to prevent directory confusion and improve security. Docker will be the default execution environment with LocalExec as fallback.

## Stories (Architect Reviewed & Reordered)

### Story 070: Executor Interface + LocalExec (0.5d) ✅ COMPLETED
**Goal**: Create pluggable executor architecture with current behavior preserved
- [x] Create `pkg/exec/` package
- [x] Define `Executor` interface: `Run(ctx, cmd []string, opts ExecOpts) (ExecResult, error)`
- [x] Implement `LocalExec` struct (current shell tool behavior)
- [x] Define `ExecOpts` and `ExecResult` structs
- [x] Add unit tests for interface and LocalExec
- [x] Create `Registry` for managing multiple executors
- [x] Add `ShellCommandAdapter` for backward compatibility
- [x] **Acceptance**: Default implementation `LocalExec` passes all existing unit tests

### Story 071: Bootstrap Dockerfile Generation (1.0d)
**Goal**: Generate appropriate Dockerfile during bootstrap phase
- [ ] Add Dockerfile generation to bootstrap artifacts
- [ ] Create language-specific Dockerfile templates (Go, Node.js, Python)
- [ ] Compute Go version, cache layers, pick Alpine vs Debian
- [ ] Include necessary build tools and dependencies
- [ ] Add .dockerignore generation
- [ ] Update bootstrap phase to detect project type and generate appropriate Dockerfile
- [ ] Add tests for Dockerfile generation
- [ ] **Acceptance**: `go run ./cmd/agentctl bootstrap-docker` emits minimal Dockerfile pinned to go.major.minor and produces reproducible image

### Story 072: DockerExec Implementation (2.0d)
**Goal**: Implement Docker-based command execution with worktree bind mounts
- [ ] Implement `DockerExec` struct
- [ ] Configure rootless Docker execution with proper UID/GID handling
- [ ] Implement worktree bind mounting (`--volume <worktree>:/workspace`)
- [ ] Add security hardening (read-only, no network, tmpfs)
- [ ] Add proper error handling and logging
- [ ] Implement signal propagation and container cleanup
- [ ] Add timeout handling and process-kill strategy
- [ ] Handle concurrent agent support and container lifecycle
- [ ] Address path-mapping differences (macOS/Windows/Linux)
- [ ] Create unit tests with Docker available/unavailable scenarios
- [ ] **Acceptance**: Runs `go test ./...` in container using worktree bind mount read-only, separate writable `/tmp` tmpfs. Honors `ExecOpts.Timeout` and returns ExitCode/logs. Falls back to LocalExec with actionable error when daemon unavailable.

### Story 073: Configuration & Startup Detection (0.5d)
**Goal**: Add Docker detection and configuration management
- [ ] Add Docker detection to startup dependency checks
- [ ] Update configuration schema for executor settings
- [ ] Add `--sandbox` CLI flag (auto|docker|local)
- [ ] Add Docker image configuration options
- [ ] Add CPU/memory limits configuration
- [ ] Update startup banner to show executor status
- [ ] Add graceful degradation messaging when Docker unavailable
- [ ] **Acceptance**: `executor: "auto" | "docker" | "local"` in config with env override. Auto = Docker if socket reachable AND image exists/auto-build succeeds. CPU/MEM default limits in config; overridable per command.

### Story 074: Shell Tool Integration (0.5d)
**Goal**: Replace shell tool execution with dockerized execution
- [ ] Update shell tool to use Executor interface
- [ ] Set Docker as default executor when available
- [ ] Add fallback to LocalExec when Docker unavailable
- [ ] Update MCP tool registration to use configured executor
- [ ] Add integration tests with real Docker containers
- [ ] **Acceptance**: All existing agent shell steps (format, vet, test) route through executor abstraction. No regression in Phase-6 E2E smoke test (run both with Docker on/off).

### Story 075: Testing, Documentation & CI (1.0d)
**Goal**: Comprehensive testing and documentation
- [ ] Add unit tests for DockerExec using Dockertest or DIND
- [ ] Add end-to-end tests with Docker sandboxing
- [ ] Test worktree compatibility with Docker bind mounts
- [ ] Verify architect can review code via git diff
- [ ] Add performance benchmarks (Local vs Docker)
- [ ] Add multi-agent stress testing
- [ ] Update README with Docker requirements
- [ ] Add troubleshooting guide for Docker issues
- [ ] Document security model and benefits
- [ ] Update GitHub Actions with Docker daemon requirements
- [ ] Add CI job that runs full orchestrator in Docker mode
- [ ] **Acceptance**: Unit test for DockerExec using Dockertest or DIND. Docs section in `README_SANDBOX.md` + parameter list in config reference. GitHub Actions updated with job that runs full orchestrator in Docker mode.

## Configuration Schema

```json
{
  "executor": {
    "type": "docker",           // docker|local
    "docker": {
      "image": "golang:1.21-alpine",
      "network": "none",        // none|bridge|host
      "readonly": true,
      "tmpfs": ["/tmp"],
      "volumes": [],            // additional volumes if needed
      "env": {}                 // environment variables
    }
  }
}
```

## Technical Requirements

### Docker Detection
- Check for `docker` or `podman` in PATH
- Verify Docker daemon is running
- Test container creation permissions
- Graceful fallback to LocalExec if Docker unavailable

### Security Hardening
- Run containers as non-root user
- Read-only filesystem (except /workspace)
- Network isolation by default
- Minimal attack surface (alpine-based images)
- Proper resource limits

### Performance Goals
- Container startup < 1 second
- Build/test operations should not be significantly slower
- Image caching for repeated operations
- Cleanup containers after execution

### Compatibility
- macOS (Docker Desktop)
- Linux (Docker CE/Podman)
- Windows (Docker Desktop) - nice to have
- Git worktrees must work seamlessly
- Architect code review workflow unchanged

## Dependencies
- Docker or Podman installed
- Appropriate base images available
- Proper user permissions for container execution

## Success Criteria
- [ ] AI agents cannot create files outside workspace
- [ ] No performance degradation for build/test operations
- [ ] Architect can review code via git diff
- [ ] Seamless worktree integration
- [ ] Cross-platform compatibility (macOS/Linux)
- [ ] Graceful fallback when Docker unavailable
- [ ] Clear error messages and troubleshooting

## Rollout Plan (Architect Reviewed)
1. Implement interface (Story 070) - 0.5d
2. Bootstrap Dockerfile generation (Story 071) - 1.0d  
3. Docker executor implementation (Story 072) - 2.0d
4. Configuration & startup detection (Story 073) - 0.5d
5. Shell tool integration (Story 074) - 0.5d
6. Testing, documentation & CI (Story 075) - 1.0d

**Total estimated time: 5.5 days** (revised from 3.5d based on architect feedback)

## Future Stories (Backlog)
- **Story 076**: Image cache & pruning policy
- **Story 077**: Advanced resource & concurrency limits  
- **Story 078**: Security scan / Snyk integration

## Architect Review Summary
✅ **Scope**: Mostly correct, main gaps are image caching, resource limits, CI adaptations
✅ **Order**: Swapped to generate Dockerfile first, then implement executor
✅ **Time**: Expanded estimates for DockerExec (2.0d) and validation work (1.0d)
✅ **Acceptance**: Added explicit criteria to keep stories tight and testable