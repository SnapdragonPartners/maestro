# ADR 0002: Local Single-User Runtime Kernel

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro is a locally run app factory, not a hosted multi-tenant service. The runtime
coordinates agents, containers, git operations, a WebUI, local state, and optional
offline services from a single user workstation.

Earlier docs describe several modes and bootstrap flows. The current code has
converged on a shared kernel that owns infrastructure setup for the main and resume
flows.

## Decision

Keep Maestro as a single local Go runtime with a shared `Kernel` that owns process
infrastructure:

- dispatcher
- SQLite database connection and persistence queue
- chat service
- shared LLM client factory
- build service
- demo service
- WebUI server
- Docker Compose registry
- global container registry

The kernel should be concrete and boring. Avoid introducing broad service abstractions
unless they support an actual second implementation or materially improve tests.

Security posture should match a single-user local application: prevent obvious
dangerous behavior, protect local secrets, avoid leaking forge/API tokens into
containers, and keep operational complexity lower than a hosted system would need.

## Current Implementation

- `internal/kernel/kernel.go` initializes and owns dispatcher, database,
  persistence channel, build service, chat service, demo service, WebUI, shared
  `agent.LLMClientFactory`, and compose registry.
- `cmd/maestro/main.go` handles mode selection, config loading, secrets/password
  setup, resume mode, sync mode, and run mode.
- `cmd/maestro/flows.go` uses the kernel for main and resume flows, creates a
  supervisor, registers agents, starts the WebUI, waits for setup, and performs
  graceful shutdown.
- `internal/supervisor/supervisor.go` manages agent lifecycle, restart policy,
  SUSPEND recovery, and watchdog behavior.

## Consequences

- Runtime state belongs under the project `.maestro/` directory and per-agent
  workspaces, not in global services.
- The WebUI can be treated as a local control surface over the same process rather
  than a separate backend.
- Tests can instantiate a kernel with mocks around expensive external boundaries
  while preserving real dispatcher/database behavior.
- A future hosted mode would be a separate architecture decision, not an incremental
  extension of this local contract.

## Related Documents

- `README.md`
- `CLAUDE.md`
- `docs/AGENT_LIFECYCLE.md`
- `docs/MAESTRO_MACOS_CHANGES.md`
- `docs/RESUME_MODE_SPEC.md`

