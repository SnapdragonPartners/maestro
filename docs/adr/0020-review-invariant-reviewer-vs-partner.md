+++
title = "ADR 0020: The Review Invariant — Reviewer vs Partner/Supervisor"
edit_date = "2026-07-13"
status = "live"
summary = "Canonical statement of the symmetric review invariant (every Management artifact reviewed by a non-author) and the two review scopes: narrow Reviewers that block, and Partner/Supervisors that judge."
+++

# 0020. The Review Invariant — Reviewer vs Partner/Supervisor

Status: Accepted (Codex + DR, 2026-07-13); amended 2026-07-13 (code review is review, not a human reservation); amended 2026-07-13 (human Accept is unconditional — auto-merge waiver withdrawn)

## Context

The economic argument for the factory rests on paired review: errors caught at authoring time are cheaper than errors caught in production, and hallucination, scope drift, and rule/compliance drift compound quietly when many agents ship into one codebase. But review roles have two known failure modes the roadmap flags as risks: reviewers that expand scope ("clever ideas" injected during review), and review requirements so rigid they can't accommodate human-authored artifacts or interactive tempos. v2 needs one canonical invariant and a clean split of review scopes.

## Decision

### The symmetric review invariant

**Every persistent Management artifact must be reviewed by at least one party other than its author, with human escalation for irreconcilable contention.** The invariant is symmetric across author kinds:

- Agent-authored artifacts are reviewed by another agent or a human gate.
- Human-authored artifacts (e.g. intake form output) are reviewed by the receiving agent — recipient review: the Work Group receiving an Epic reviews the framing it was handed, and may push back on it. Recipient pushback satisfies the invariant for the received Epic and its framing only; review of Feature-level decomposition and cross-Epic coherence is assigned by the intake contract ADR and the pre-Phase-5 spike, not implied here.
- At the Workbench, the present human accepts and a trailing agent reviewer checks syntactic, rule, and architectural drift — author and reviewer still differ.

The invariant is **principal-based**, not agent-based. Humans are principals like agents: every user account gets a principal instance record whose `model` is `human-<user_id>` — two distinct humans are two distinct models, so the pre-agentic norm of one human reviewing another's work falls out of the same heterogeneity check. Authorship, review, and heterogeneity are thus expressed uniformly with no nulls or side channels. Diversity outranks authority: even the human operator does not self-review — a human may be an artifact's author or its approver, never both. While a single human operates the system, this automatically guarantees at least one agent pass on every Management artifact; multi-operator organizations may later satisfy the invariant with two distinct humans.

No exemptions are needed; symmetry covers every case. The invariant applies to amendments exactly as to originals: an addendum to an accepted artifact carries its own author and reviewer (artifact amendment records are defined in the artifacts ADR).

### Two review scopes

**Reviewer** — narrow, blocking, never additive. Checks correctness, completeness, adherence to the governing artifact, and budget/nonconvergence. A Reviewer may block excessive usage, non-adherence, or incomplete work; it never expands scope or contributes design ideas. Examples: the internal coder reviewer, the budget reviewer, a citation verifier.

**Partner/Supervisor** — judgment-bearing. May add value: judge optimality, enforce project guidelines, ADRs, and best practices, apply pluggable skills (compliance, security), and resolve ambiguity or escalate it. Examples: PM/Architect, Architect/Coder, recipient pushback by a Work Group on its Epic.

The distinction is enforced in prompts and harness policy: a role configured as a Reviewer is not given the tools or instructions to propose alternatives, only to verify and block.

### Reviewer heterogeneity

Where practical, the reviewer runs a distinct model from the author — and for this purpose each human is a distinct model (`human-<user_id>`), echoing the non-agentic norm that the reviewer is a different person than the author. Heterogeneous model lineages catch errors that the same model, differently prompted, systematically misses; this is observed repeatedly in practice (distinct review agents over the same diffs routinely surface disjoint findings). Reviewer model routing is an M lever in MPH and a Phase 5 deliverable.

Homogeneous review — author and reviewer on the same model — is a permitted degenerate case when heterogeneity is unavailable (economic constraints, airplane mode, single-provider environments), but it is a flagged degradation, not a silent default: the review record captures the author and reviewer models, so evidence and metrics can distinguish heterogeneous from homogeneous review.

### Human-reserved approvals

The invariant has a ceiling: some approvals are reserved to the human operator and can never be satisfied by agent review alone — canonically, final acceptance that an Epic is complete (the Epic-to-default merge, roadmap D4). Agents review; humans accept. The reservation is independent of tempo and is **unconditional** (amended 2026-07-13: the earlier low-risk auto-merge waiver is withdrawn). Acceptance is not about risk — it is outcome validation, whether the work solves the need, and no risk assessment can stand in for the one answer only the human holds. Accepting a trivial Epic costs one glance at its evidence, because acceptance is not code review; the click is cheap and the invariant is load-bearing.

### Code review is review, not a human reservation

v2 explicitly rejects the current community article of faith that humans should review all code before final acceptance. The invariant is "all code is reviewed" — not "all code is reviewed by a human." Fully agentic code review is acceptable, and for high-volume agent-written code often preferable. Reviewer heterogeneity (above) is what replaces the human's fresh eyes, so agentic review earns its keep with model diversity; homogeneous agentic code review remains permitted under the same recorded degradation — degraded is not broken, and a degraded case is simply expected to perform worse. The system should actively surface the degraded state to the operator, not merely record it. Human review of high-volume generated code is at best performative; models review at least as well as they write.

What the human is indispensable for is outcome validation: *does the Feature solve the problem, and does it work as intended?* That is the only question whose answer only the human holds, because only the human holds the intent. This is precisely the human-reserved approval above — the reservation spends the operator's scarce attention on the judgment only they can make, instead of on inspections agents perform better. Humans retain the right to inspect any code at any time (drilldown is a stated purpose of the UI); what is rejected is mandatory human code review as an acceptance gate.

This deliberately diverges from the research-corpus orthodoxy — while following the corpus's own premise, that human attention is the scarce resource, to its actual conclusion.

### Bounded contention

Author/reviewer disagreement escalates to a human after a bounded number of iterations — initially three, configurable per role pair. The Orchestrator enforces the bound (rules); a human resolves the contention (judgment). This is the same principle the interim build process applies to its own author/reviewer pair.

## Consequences

- Phase 5's gates and artifact-review machinery implement this invariant; its data-plane expression is a pair of principal-generic author/reviewer instance references on every Management artifact, able to point at agent or human principal instances alike — exact field shapes belong to the artifacts ADR.
- The agent-instance model (artifacts ADR) generalizes to principal instances: user accounts get instance records too, which is also what makes the heterogeneity record (author model vs reviewer model, where `human-<user_id>` is a model and distinct humans are distinct models) uniformly checkable.
- Reviewer narrowness is a hard property, not a suggestion — a Reviewer that starts contributing ideas is misconfigured, and the roadmap's "internal reviewers become scope expanders" risk has a concrete test.
- Recipient pushback gives every dispatched Epic fresh-eyes review by the party with the most skin in the game, at no extra standing cost.
- Liveness: bounded contention plus mandatory escalation means review can never deadlock silently — every disagreement terminates in acceptance, revision, or a human decision ("the system does not get stuck").

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (Work Group roles, Workbench invariants, recipient review), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md).
- [Roadmap](../v2/roadmap.md) pillar 7 (agent pairs), north star, risks; [ADR backlog](../v2/adr-backlog.md) Reviewer vs Partner entry; [build-process](../v2/build-process.md) (the manual rehearsal of this invariant).
- Historical note [0003](0003-agent-roles-and-finite-state-machines.md) (v1 role model; role taxonomy superseded via ADR 0018).
