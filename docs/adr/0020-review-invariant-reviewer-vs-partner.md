+++
title = "ADR 0020: The Review Invariant — Reviewer vs Partner/Supervisor"
edit_date = "2026-07-13"
status = "draft"
summary = "Canonical statement of the symmetric review invariant (every Management artifact reviewed by a non-author) and the two review scopes: narrow Reviewers that block, and Partner/Supervisors that judge."
+++

# 0020. The Review Invariant — Reviewer vs Partner/Supervisor

Status: Proposed

## Context

The economic argument for the factory rests on paired review: errors caught at authoring time are cheaper than errors caught in production, and hallucination, scope drift, and rule/compliance drift compound quietly when many agents ship into one codebase. But review roles have two known failure modes the roadmap flags as risks: reviewers that expand scope ("clever ideas" injected during review), and review requirements so rigid they can't accommodate human-authored artifacts or interactive tempos. v2 needs one canonical invariant and a clean split of review scopes.

## Decision

### The symmetric review invariant

**Every persistent Management artifact must be reviewed by at least one party other than its author, with human escalation for irreconcilable contention.** The invariant is symmetric across author kinds:

- Agent-authored artifacts are reviewed by another agent or a human gate.
- Human-authored artifacts (e.g. intake form output) are reviewed by the receiving agent — recipient review: the Work Group receiving an Epic reviews the framing it was handed, and may push back on it.
- At the Workbench, the present human accepts and a trailing agent reviewer checks syntactic, rule, and architectural drift — author and reviewer still differ.

No exemptions are needed; symmetry covers every case. The invariant applies to amendments exactly as to originals: an addendum to an accepted artifact carries its own author and reviewer (artifact amendment records are defined in the artifacts ADR).

### Two review scopes

**Reviewer** — narrow, blocking, never additive. Checks correctness, completeness, adherence to the governing artifact, and budget/nonconvergence. A Reviewer may block excessive usage, non-adherence, or incomplete work; it never expands scope or contributes design ideas. Examples: the internal coder reviewer, the budget reviewer, a citation verifier.

**Partner/Supervisor** — judgment-bearing. May add value: judge optimality, enforce project guidelines, ADRs, and best practices, apply pluggable skills (compliance, security), and resolve ambiguity or escalate it. Examples: PM/Architect, Architect/Coder, recipient pushback by a Work Group on its Epic.

The distinction is enforced in prompts and harness policy: a role configured as a Reviewer is not given the tools or instructions to propose alternatives, only to verify and block.

### Bounded contention

Author/reviewer disagreement escalates to a human after a bounded number of iterations — initially three, configurable per role pair. The Orchestrator enforces the bound (rules); a human resolves the contention (judgment). This is the same principle the interim build process applies to its own author/reviewer pair.

## Consequences

- Phase 5's gates and artifact-review machinery implement this invariant; the artifact schema's `author_agent_instance_id`/`reviewer_agent_instance_id` fields (artifacts ADR) are its data-plane expression.
- Reviewer narrowness is a hard property, not a suggestion — a Reviewer that starts contributing ideas is misconfigured, and the roadmap's "internal reviewers become scope expanders" risk has a concrete test.
- Recipient pushback gives every dispatched Epic fresh-eyes review by the party with the most skin in the game, at no extra standing cost.
- Liveness: bounded contention plus mandatory escalation means review can never deadlock silently — every disagreement terminates in acceptance, revision, or a human decision ("the system does not get stuck").

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (Work Group roles, Workbench invariants, recipient review), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md).
- [Roadmap](../v2/roadmap.md) pillar 7 (agent pairs), north star, risks; [ADR backlog](../v2/adr-backlog.md) Reviewer vs Partner entry; [build-process](../v2/build-process.md) (the manual rehearsal of this invariant).
- Historical note [0003](0003-agent-roles-and-finite-state-machines.md) (v1 role model; role taxonomy superseded via ADR 0018).
