+++
title = "ADR 0008: Container, Workspace, and Compose Isolation"
edit_date = "2026-07-15"
status = "deprecated"
summary = "v1 container, workspace, and compose isolation model."
+++

# ADR 0008: Container, Workspace, and Compose Isolation

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro asks LLM-driven agents to inspect, edit, build, test, and run user projects.
The system must isolate work between agents, avoid leaking host credentials into
containers, and keep development environments recoverable on a local workstation.

The docs describe several container eras. The current implementation uses startup
validation, per-agent workspaces, safe fallback containers, target containers,
temporary test containers, and Docker Compose tracking.

## Decision

Use isolated per-agent workspaces and container execution as the default agent
runtime model.

Container roles:

- Safe/bootstrap container: reliable fallback and bootstrap environment.
- Target container: project-specific primary development environment.
- Test containers: temporary validation environments.
- Compose stacks: project dependencies, tracked per agent or demo instance.

Coder agents self-manage their active container through tools. The orchestrator
validates startup prerequisites, owns cleanup, and tracks active compose stacks,
but it should not hide container switches from the agent workflow.

Workspace roots that are bind-mounted into running containers must preserve their
inodes during cleanup. Clean contents in place instead of deleting and recreating
mounted roots.

## Current Implementation

- `internal/orch/startup.go` validates the safe container, validates or rebuilds
  the target container, and falls back to bootstrap when needed.
- `pkg/coder/setup.go`, `pkg/coder/driver.go`, and `pkg/tools/container_*.go`
  provide coder setup and container lifecycle tooling.
- `internal/state/compose.go` defines a thread-safe `ComposeRegistry` and shutdown
  cleanup.
- `internal/kernel/kernel.go` initializes the compose registry and global container
  registry.
- `pkg/utils/fs.go` provides `CleanDirectoryContents()` for bind-mount-safe cleanup.
- `CLAUDE.md` documents the bind mount inode rule and the three-container model.

## Consequences

- Workspaces are part of the runtime contract, not incidental temp directories.
- Container tools must be state-aware and should preserve the planning/coding/testing
  mount policy.
- User app secrets may be injected into containers, but system/forge secrets should
  stay on the host except for explicitly documented exceptions.
- Docker Desktop/macOS bind-mount behavior is a design constraint, not an edge case.

## Related Documents

- `CLAUDE.md`
- `docs/CONTAINER_SECURITY_SPEC.md`
- `docs/BUILD_SERVICE_CONTAINERIZATION_SPEC.md`
- `docs/CODER_DOCKER_PERMS.md`
- `docs/DOCKERFILE_PATH_SPEC.md`
- `docs/specs/ENHANCED_PLANNING_ARCHITECTURE.md`

