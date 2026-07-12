# Maestro v2 Build Process (Interim)

Status: agreed working agreement, 2026-07-11

This defines how v2 gets built until Maestro can build Maestro (the Phase 9 ramp). It manually implements the generate/review invariant that Maestro v2 automates: one author, one reviewer, human escalation.

## Roles

- **Author agent: Claude (Claude Code).** Drafts all artifacts — docs, ADRs, phase scopes and plans, specs, code. Roles anchor to the agent, not the underlying model.
- **Reviewer: Codex.** Provides the review function, analogous to what Maestro will automate.
- **Human operator: DR.** Resolves escalation and contention, provides feedback, and accepts. DR is also the effective orchestrator: all communication between Claude and Codex flows through DR.

An artifact is Accepted when both Codex and DR have approved it.

## Phase Workflow

- Each phase begins with a scope and a plan, each reviewed and approved by both Codex and DR.
- The working doc set stays deliberately small — there is no Maestro apparatus yet to manage a large one.
- Each phase gets a branch (or more than one, only if the plan says so).
- Branch naming: `v2/phase_x/XXX` (e.g. `v2/phase_0/adr-taxonomy`); bug-fix branches use `v2/fix/XXX`. Never reuse a prior leaf branch name as a namespace prefix (git ref collision).
- Per-phase working artifacts live in `docs/v2/phase_x/`, mirroring the branch namespace; cross-phase docs stay at the `docs/v2/` root; Accepted decisions land in `docs/adr/`.
- Never more than one feature/dev branch open at a time. Parallel branches are acceptable only for bug fixes. This bounds the human operator's review load, not the author's throughput.
- Smaller PRs get a single end-of-work review. Larger PRs get review checkpoints defined in the plan.

## Testing

- Unit and integration tests gate merges within a phase.
- The golden story suite runs at the end of each phase (once it exists, Phase 1 onward).
- Implementation direction: extend the existing build-tag pattern (`integration`) with `golden-minimal` (smoke subset) and `golden-all` (full suite) tags so golden story runs are automatable from make targets.

## CI Review And Merge

- CI runs automated review agents on every PR. All of their feedback must be resolved before merge: each thread is either fixed or explicitly pushed back on with a reasoned reply, then marked resolved. Resolving CI reviewer feedback is Claude's job.
- Final approval and the merge button are DR's.

## Spikes

- Before a spike begins, all open document work is committed (risk minimization).
- Spike code never merges into app packages (`pkg/`, `internal/`, `cmd/`). Reports land in the phase directory; scripts worth revisiting may be preserved under `spikes/phase_x/`, a standalone module excluded from the main build, test, and lint walkers. Preserved scripts are unmaintained by definition.

## Deferred Work Tracking

All deferred work discovered during v2 development is tracked as GitHub Issues — a durable record that keeps the primary docs and repo clean and does not rely on any one agent's memory. The division of labor: the roadmap holds planned work (phases and spikes), Issues hold deferred work discovered along the way, and the docs/v2 parking lot holds design ideas.

## Escalation

Author/reviewer contention that does not converge goes to DR — the same bounded-contention principle the product applies to agent pairs (roadmap pillar 7).
