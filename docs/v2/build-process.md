# Maestro v2 Build Process (Interim)

Status: agreed working agreement, 2026-07-11

This defines how v2 gets built until Maestro can build Maestro (the Phase 9 ramp). It manually implements the generate/review invariant that Maestro v2 automates: one author, one reviewer, human escalation.

## Roles

- **Author agent: Claude (Claude Code).** Drafts all artifacts — docs, ADRs, phase scopes and plans, specs, code. Roles anchor to the agent, not the underlying model.
- **Reviewer: Codex.** Provides the review function, analogous to what Maestro will automate.
- **Human operator: DR.** Resolves escalation and contention, provides feedback, and accepts.

An artifact is Accepted when both Codex and DR have approved it.

## Phase Workflow

- Each phase begins with a scope and a plan, each reviewed and approved by both Codex and DR.
- The working doc set stays deliberately small — there is no Maestro apparatus yet to manage a large one.
- Each phase gets a branch (or more than one, only if the plan says so).
- Never more than one feature/dev branch open at a time. Parallel branches are acceptable only for bug fixes. This bounds the human operator's review load, not the author's throughput.
- Smaller PRs get a single end-of-work review. Larger PRs get review checkpoints defined in the plan.

## Testing

- Unit and integration tests gate merges within a phase.
- The golden story suite runs at the end of each phase (once it exists, Phase 1 onward).
- Implementation direction: extend the existing build-tag pattern (`integration`) with `golden-minimal` (smoke subset) and `golden-all` (full suite) tags so golden story runs are automatable from make targets.

## Escalation

Author/reviewer contention that does not converge goes to DR — the same bounded-contention principle the product applies to agent pairs (roadmap pillar 7).
