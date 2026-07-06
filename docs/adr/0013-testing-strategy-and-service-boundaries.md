# ADR 0013: Testing Strategy and Service Boundaries

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro has complex state machines, LLM interactions, git/forge operations,
containers, Docker Compose, WebUI endpoints, persistence, and local/offline modes.
Full end-to-end tests are valuable but slow, expensive, and environment-dependent.
Unit tests need enough fidelity to catch state-machine and protocol regressions.

## Decision

Use a hybrid testing strategy:

- Unit tests use mocks for expensive or nondeterministic boundaries.
- Integration tests use real services where mock fidelity would be misleading.
- The real dispatcher should be preferred in tests because it is in-memory, fast,
  and central to behavior.
- LLM clients should be mocked for normal unit tests.
- GitHub, Gitea, Docker, and live provider behavior should be tested through
  integration or e2e targets with explicit build tags.

Shared mocks should live under `internal/mocks/`. State-machine and protocol changes
should include focused unit tests. Cross-component workflow changes should add or
update integration tests when unit tests cannot provide confidence.

## Current Implementation

- `docs/TESTING_STRATEGY.md` documents the hybrid approach and shared mocks.
- `internal/mocks/` contains mocks for LLM, GitHub, chat, git runner, and containers.
- `Makefile` provides `make test`, `make test-integration`, `make test-e2e`,
  `make test-all`, and coverage helpers.
- Many packages have focused unit tests for FSMs, toolloop behavior, persistence,
  config, forge adapters, WebUI handlers, and container helpers.
- Integration and e2e tests live under `tests/integration` and `tests/e2e`.

## Consequences

- New agent behavior should usually have deterministic LLM mocks.
- External service tests should be opt-in through tags and should fail with clear
  setup guidance when prerequisites are absent.
- Shared mocks should stay small and behavior-focused. Avoid building a second
  implementation of git, Docker, or a provider API inside mocks.
- Missing tests are most important when new logic has branches, state transitions,
  persistence changes, or recoverable/unrecoverable error paths.

## Related Documents

- `docs/TESTING_STRATEGY.md`
- `CLAUDE.md`
- `Makefile`
- `internal/mocks/doc.go`

