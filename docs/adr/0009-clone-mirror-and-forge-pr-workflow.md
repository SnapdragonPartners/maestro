# ADR 0009: Clone, Mirror, and Forge PR Workflow

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro coordinates multiple coders working concurrently. Those coders need isolated
repositories, fast local fetch/rebase operations, host-side authenticated pushes,
and a PR/merge workflow that works in standard online mode and airplane mode.

Earlier docs focus on GitHub. Current code has a mode-aware forge abstraction with
GitHub and Gitea adapters, while retaining older GitHub package types for parts of
the migration and tests.

## Decision

Use clone-based git isolation with local bare mirrors:

- Each coder gets a complete workspace clone.
- Local mirrors provide fast unauthenticated fetch/rebase operations inside
  containers.
- Host-side operations handle authenticated push, PR creation, and merges.
- Architect owns final merge decisions.
- Standard mode uses GitHub. Airplane mode uses local Gitea and can later sync back
  to GitHub.

Use `pkg/forge.Client` as the intended provider-neutral PR/merge boundary. Keep the
older `pkg/github` client as the GitHub implementation detail behind the forge adapter
or as legacy test plumbing until migration cleanup is complete.

## Current Implementation

- `pkg/coder/clone.go` manages mirrors, workspace clones, branch naming, and remote
  setup.
- `pkg/coder/prepare_merge.go` pushes branches, creates or finds PRs through
  `forge.NewClient`, and sends merge requests to the architect.
- `pkg/architect/request_merge.go` uses `forge.NewClient` to create or merge PRs
  and handles conflicts as recoverable feedback.
- `pkg/forge` defines provider-neutral PR and merge interfaces.
- `pkg/forge/github` adapts `pkg/github.Client`.
- `pkg/forge/gitea` implements the local Gitea API client.
- `internal/orch/airplane.go` starts/configures Gitea and prepares local mirrors.
- `pkg/sync` supports syncing airplane-mode changes back to GitHub.

## Consequences

- Coder containers should not need GitHub tokens or forge API credentials.
- Merge conflict handling should send coders back to CODING with concrete guidance.
- New forge behavior should use `pkg/forge` unless intentionally changing legacy
  GitHub-only code.
- Documentation that says PR creation is only `gh pr create` is incomplete; standard
  mode still uses `gh` under the GitHub adapter, but callers should depend on the
  forge boundary.

## Related Documents

- `docs/GIT.md`
- `docs/AIRPLANE_MODE.md`
- `docs/MERGE_CONFLICT_RESOLUTION_SPEC.md`
- `docs/REPOSTATE_DESIGN.md`
- `docs/specs/SWE_EVO_PLAN.md`
- `docs/specs/SWE_EVO_IMPL.md`

