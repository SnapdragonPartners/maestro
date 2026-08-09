+++
title = "ADR 0020: The Review Invariant — Reviewer vs Partner/Supervisor"
edit_date = "2026-08-09"
status = "live"
summary = "Canonical statement of the symmetric review invariant (every Management artifact reviewed by a non-author) and the two review scopes: narrow Reviewers that block, and Partner/Supervisors that judge; reviewer heterogeneity is measured in model lineage — the set of originating labs — on a three-rung ladder that warns but never refuses, with unknown lineage held outside it as unclassified."
+++

# 0020. The Review Invariant — Reviewer vs Partner/Supervisor

Status: Accepted (Codex + DR, 2026-07-13); amended 2026-07-13 (code review is review, not a human reservation); amended 2026-07-13 (human Accept is unconditional — auto-merge waiver withdrawn); amended 2026-08-08, accepted 2026-08-09 (Codex + DR): heterogeneity is measured in model lineage rather than model identity — set-valued, three-rung ladder plus an explicit unclassified state

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

Where practical, the reviewer runs a model whose **lineage is disjoint** from the author's. Heterogeneous lineages catch errors that a correlated model, differently prompted, systematically misses; this is observed repeatedly in practice (distinct review agents over the same diffs routinely surface disjoint findings). Reviewer model routing is an M lever in MPH and a Phase 5 deliverable.

**Model lineage is the set of originating labs.** It is set-valued, not a single label, because derivation is not always a single inheritance. Serving provider and weight availability never change it. The rules are:

- A model trained from scratch by one lab carries that one lab.
- **Derivation adds; it never removes.** A fine-tune or derived model carries its base's lineage *plus* the lineage of whoever derived it — a third-party fine-tune of another lab's base is a two-lab set, not a relabelling. Fine-tuning does not undo the training-data ordering and alignment choices being proxied for, which is why the inherited entry survives.
- A merge or distillation of several bases carries the union of their sets.
- Each human is a distinct model *and* a distinct singleton lineage (`human-<user_id>`), which preserves both the non-agentic norm that the reviewer is a different person than the author and the human-reviews-agent case.

This is a **conservative proxy** for correlated training and alignment choices, not a claim that every model from one lab is technically identical. Set-valued lineage keeps the proxy conservative in the right direction: an added entry can only ever make two models look *more* correlated, never less.

**Lineage may be unknown, and unknown is its own state.** A model whose lineage is not recorded is **unclassified** — never silently rung 1, and never inferred from a routing or provider field. Unclassified **must be surfaced** exactly as a degradation is: an operator has to be able to see that the check did not run, because a check that quietly returns "fine" when it has no data is worse than no check.

> **Terminology.** "Lineage" elsewhere in the v2 corpus — ADRs 0018, 0021, 0022 and the data-plane schema — means the *work* hierarchy chain (Product → Feature → Epic → Story). **Model lineage is an unrelated concept and shares no columns, keys, or constraints with it.** Any implementation must name the two distinctly; they will otherwise be conflated at exactly the seam where a heterogeneity check reads a principal instance that also carries work lineage.

Two consequences of taking the lab as the unit:

- **Serving is not origin.** A model's routing destination describes how a request is dispatched, not who trained the weights. An open-weight model shares its lab's lineage no matter who serves it, so a locally-served model and that lab's hosted models are one lineage — and, conversely, two models served through one endpoint may be two lineages. A heterogeneity check built on a routing or provider field is therefore wrong in both directions, and lineage needs its own declared attribute.
- **Distinctness is a property of the pair, not of a model.** What is classified is an author/reviewer **edge**, and the two kinds of edge are not equally available. A **realized** edge — who actually reviewed what — is already recorded: a Management artifact carries `author_instance_id`, its review record carries `reviewer_instance_id`, both resolve to principal instances carrying `model`. A **prospective** edge — which role will review which, before anything runs — is not declared anywhere; role-to-model routing names the cast, and the reviews-whom relation belongs to the harness that executes it. Classification of completed work therefore needs only lineage metadata; classification of a configuration in advance needs the routing contract as well.

**The degradation ladder.** Three rungs, best to worst, comparing the author's lineage set with the reviewer's:

1. **Disjoint lineage sets** — no lab in common. The preferred state, and the one the invariant is written for.
2. **Overlapping lineage sets, distinct model** — degraded. The failure modes the invariant exists to break are correlated at the lab, so two models sharing any originating lab are weaker fresh eyes than the model count suggests. Any overlap is enough; partial overlap is not a partial rung.
3. **Same model** — degraded further; the degenerate case.

Rung 1 requires both sets to be **known and disjoint**. If either side's lineage is unrecorded the pairing is **unclassified**, which sits outside the ladder rather than at the top of it.

**Same-lineage review warns; it never refuses.** Rungs 2 and 3 are permitted when heterogeneity is unavailable — economic constraints, airplane mode, single-lineage model availability, and high-security or sovereign-AI installations where only one lab is admissible. A hard failure would make those deployments unusable rather than honestly labelled. There is deliberately no "must use distinct lineages" rule here, and unclassified pairings are likewise never refused.

What the warning must clear is **visibility**. A review record **must** carry author and reviewer model *and lineage set*, so that evidence and metrics can distinguish the three rungs from the unclassified state; and per the code-review section below, a degraded state **must** be actively surfaced to the operator rather than merely stored. An unlabelled degradation is the failure mode this ADR forbids — silence reads as rung 1.

This is a requirement, not a description of today. The models are already recorded — author and reviewer resolve to principal instances, and `model` is populated for every principal kind — but **lineage is not recorded and no classification is computed anywhere**, so the visibility requirement is currently unmet. See Consequences for what closes it.

**Where it binds.** Both the product's own reviewer routing (the Phase 5 deliverable above) and benchmark configurations under ADR 0025. The rule is uniform; what differs is the surfacing point and what each side can classify — an MPH bundle contributes role-to-model routing but does not by itself carry the reviews-whom relation, so ADR 0025 defers classification rather than claiming the bundle settles it.

### Human-reserved approvals

The invariant has a ceiling: some approvals are reserved to the human operator and can never be satisfied by agent review alone — canonically, final acceptance that an Epic is complete (the Epic-to-default merge, roadmap D4). Agents review; humans accept. The reservation is independent of tempo and is **unconditional** (amended 2026-07-13: the earlier low-risk auto-merge waiver is withdrawn). Acceptance is not about risk — it is outcome validation, whether the work solves the need, and no risk assessment can stand in for the one answer only the human holds. Accepting a trivial Epic costs one glance at its evidence, because acceptance is not code review; the click is cheap and the invariant is load-bearing.

### Code review is review, not a human reservation

v2 explicitly rejects the current community article of faith that humans should review all code before final acceptance. The invariant is "all code is reviewed" — not "all code is reviewed by a human." Fully agentic code review is acceptable, and for high-volume agent-written code often preferable. Reviewer heterogeneity (above) is what replaces the human's fresh eyes, so agentic review earns its keep with lineage diversity; same-lineage agentic code review remains permitted under the same recorded degradation — degraded is not broken, and a degraded case is simply expected to perform worse. The system should actively surface the degraded state to the operator, not merely record it. Human review of high-volume generated code is at best performative; models review at least as well as they write.

What the human is indispensable for is outcome validation: *does the Feature solve the problem, and does it work as intended?* That is the only question whose answer only the human holds, because only the human holds the intent. This is precisely the human-reserved approval above — the reservation spends the operator's scarce attention on the judgment only they can make, instead of on inspections agents perform better. Humans retain the right to inspect any code at any time (drilldown is a stated purpose of the UI); what is rejected is mandatory human code review as an acceptance gate.

This deliberately diverges from the research-corpus orthodoxy — while following the corpus's own premise, that human attention is the scarce resource, to its actual conclusion.

### Bounded contention

Author/reviewer disagreement escalates to a human after a bounded number of iterations — initially three, configurable per role pair. The Orchestrator enforces the bound (rules); a human resolves the contention (judgment). This is the same principle the interim build process applies to its own author/reviewer pair.

## Consequences

- Phase 5's gates and artifact-review machinery implement this invariant; its data-plane expression is a pair of principal-generic author/reviewer instance references on every Management artifact, able to point at agent or human principal instances alike — exact field shapes belong to the artifacts ADR.
- The agent-instance model (artifacts ADR) generalizes to principal instances: user accounts get instance records too, which is also what makes the heterogeneity record (author vs reviewer, compared on lineage first and model second, where `human-<user_id>` is both a model and a lineage and distinct humans are distinct on both) uniformly checkable.
- Reviewer narrowness is a hard property, not a suggestion — a Reviewer that starts contributing ideas is misconfigured, and the roadmap's "internal reviewers become scope expanders" risk has a concrete test.
- Recipient pushback gives every dispatched Epic fresh-eyes review by the party with the most skin in the game, at no extra standing cost.
- Liveness: bounded contention plus mandatory escalation means review can never deadlock silently — every disagreement terminates in acceptance, revision, or a human decision ("the system does not get stuck").
- The lineage amendment reclassifies existing measurements without invalidating them. The benchmark's `paired-default` configuration has paired two models from one lab since its first commit; under the prior "distinct model" text those pairings were **conforming**, and nothing in the [conformance log](../v2/notes_conformance-log.md) violated this ADR. From this amendment they are **rung 2 — degraded, not invalid**. They remain valid measurements of what they measured, now correctly labelled. Single-agent rows are unaffected: they have no reviewer pairing to classify.
- **The machinery this section requires does not exist at any granularity, and that gap predates the amendment.** Nothing computes heterogeneity today, so even a same-*model* pairing would go unflagged — a live violation of the surfacing requirement under the text already in force, not one this amendment introduces. What is missing is narrower than it first appears, and the pieces do not all wait on the same phase:
  - **Model-lineage metadata** — the only genuinely absent input. A property of a model, so it belongs with model metadata and is never restated per configuration, or two configurations can disagree about one model.
  - **Classification and surfacing** — the computation over an edge and the operator-visible result, including the unclassified state.
  - **The prospective routing contract** — needed only to classify a configuration *before* it runs, and owned by the Phase 5 reviewer-routing deliverable.

  The **realized** edge is already persisted by the Phase 2 schema, so once lineage metadata exists, completed reviews can be classified retrospectively without waiting for Phase 5. Only advance classification of a configuration depends on that phase.

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (Work Group roles, Workbench invariants, recipient review), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md), [ADR 0025](0025-golden-stories-and-benchmark-runner.md) (MPH configurations, where the heterogeneity rule is benchmarkable).
- [Roadmap](../v2/plan_roadmap.md) pillar 7 (agent pairs), north star, risks; [ADR backlog](../v2/notes_adr-backlog.md) Reviewer vs Partner entry; [build-process](../v2/process_build.md) (the manual rehearsal of this invariant).
- Historical note [0003](0003-agent-roles-and-finite-state-machines.md) (v1 role model; role taxonomy superseded via ADR 0018).
