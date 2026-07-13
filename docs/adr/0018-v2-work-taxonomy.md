+++
title = "ADR 0018: v2 Work Taxonomy"
edit_date = "2026-07-13"
status = "live"
summary = "Defines the v2 work hierarchy — Product, Feature, Epic, Story — and its executors (Work Group, Workbench tempo), the collapsible degenerate path for small work, and the v2 MVP boundary (D1)."
+++

# 0018. v2 Work Taxonomy

Status: Accepted (Codex + DR, 2026-07-13)

## Context

Maestro v2 needs a shared work hierarchy that humans, agents, the data plane, the git model, and the UI all use with the same meanings. v1's Spec/Story model was single-repo and instance-scoped; v2 must express multi-repo intent, repo-scoped execution by concurrent teams, and PR-sized units of review.

Names were chosen to preserve industry and LLM priors rather than fight them (roadmap naming note, 2026-07-11): the repo-scoped unit was originally "Task," which inverted the universal prior that Tasks are smaller than Stories and collided with the v1 TASK message type and generic agent-tooling language. Epic-contains-Stories is the strongest shared prior in the industry, so the hierarchy adopts it.

## Decision

### The hierarchy

**Product ⊇ Feature ⊇ Epic ⊇ Story.** Each level is a real data-plane model with provenance, not only a concept.

- **Product** groups one or more repositories that together deliver a user-facing or operational system. First-class but lightweight: it exists because dashboards, multi-repo knowledge, multi-repo UAT, and golden stories need it, not because it carries workflow.
- **Feature** is the highest-level human ask. It may span multiple repositories, contain many Epics, and may be an entire greenfield MVP. Features are decomposed into Epics by intake/triage (contract in the intake ADR; executor deliberately unbound until the pre-Phase-5 spike). Decomposition is not frozen at intake: a Feature may gain Epics after development begins — the canonical case is UAT revealing a critical missing capability that is not a bug.
- **Epic** is a repo-scoped component of a Feature: exactly one repository, one Epic branch, one Work Group. An Epic may not be fully designed when first created. Epic-to-default merge is the human acceptance gate (roadmap D4).
- **Story** is a PR-sized chunk of an Epic assigned to a single Coder: large enough to be useful, small enough to be testable, reviewable, and mergeable. Story branches cut from and merge into the Epic branch. Story decomposition optimizes for parallel development.

### Executors

- **Work Group** is the unit of execution: the agents (PM, Architect, Coders), workspace, branch, prompt pack, harness configuration, review/evidence policy, and gates assigned to one Epic. Work Group lifecycle is owned by the Orchestrator (ADR 0019). One Work Group per Epic; multiple concurrent Work Groups are post-MVP.
- **Workbench** is a Work Group execution *tempo*, not a separate system — where "tempo" constrains the nouns and the proof, not the loop. Invariant in both tempos: every repo change is a Story in an Epic, lands through the branch and evidence machinery with provenance, and every Management artifact is reviewed by a party other than its author (at the Workbench: the present human accepts, a trailing agent reviewer checks syntactic, rule, and architectural drift). Free to vary as harness-preset parameters (the H in MPH): the workflow graph itself, gate timing (leading versus trailing), agent lifetimes (e.g. a Coder kept live across review iterations with the human in the loop), and Story dispatch cadence (add-on Stories minted mid-Epic as the human iterates). Same nouns, same proof, different loop. Mode is chosen per Epic — at intake or by the user. Entry is a dispatch contract: a special-case blank Feature request scoped to a repository, or attachment to an existing Epic; UI surface details (e.g. the master-dashboard button) live outside this ADR.
- Roles inside a Work Group: **PM** (requirements; user-facing refinement of the Epic), **Architect** (technical partner and supervisor: reviews plans, code, evidence, merges), **Coder** (implements one Story at a time). Whether Feature-level conversational intake is run by a PM-like role is an intake-executor question, deliberately unbound here (intake contract ADR; pre-Phase-5 spike).

### The collapsible degenerate path

Small work must not require Feature-level ceremony. The degenerate path is ceremony-free Epic creation with two anchors: when the work belongs to an existing Feature (e.g. an Epic added mid-Feature from UAT findings), the new Epic anchors there; when it does not (a standalone bug fix or tweak entering as a single-Story Epic), the path auto-creates a minimal wrapper Feature record (provenance marked `auto-created`, no intake ceremony) rather than allowing Epics without a Feature parent.

Lineage is non-null at every level, not just below Feature: a wrapper Feature gets a Product parent inferred from the target repository. To make that inference deterministic, a repository belongs to exactly one Product (MVP simplification; revisit if repo sharing between Products emerges), and repositories not yet assigned belong to a default Product — mirroring the default organization in single-user mode. The workflow collapses instead of the schema. Bypass the ceremony, never the artifact.

The hierarchy is late-bound at every level: Features gain Epics and Epics gain Stories while in flight. A late-added Epic is reviewed like any other — recipient pushback by its Work Group applies regardless of when the Epic was minted.

### Model/Prompt/Harness

MPH — the three levers of the factory (model routing; prompt packs, role instructions, skills; workflow graph, tools, gates, context loading, containers, branch strategy, evals) — is ratified as defined in the [roadmap](../v2/roadmap.md) Core Vocabulary.

### MVP boundary (D1, ratified)

The v2 MVP is local/team-capable architecture, not full cloud multi-user. It includes: the golden story/benchmark runner, the minimal Management/Audit artifact skeleton, agent instances, LLM/tool metrics, the Postgres/sqlc/migrate vertical slice, this taxonomy, a single Work Group execution path, evidence package basics, and the Epic/Story branch strategy. It does not require: a standing intake agent, multiple concurrent Work Groups, cloud auth, AST ingestion, a full skill registry, or `maestro-agent` extraction.

## Consequences

- The data-plane schema families (Phase 0 artifacts ADR, Phase 2 implementation) derive directly from this hierarchy; lineage is non-null at every level (`product_id` on Features, `feature_id` on Epics, `epic_id` on Stories) because of the wrapper-Feature and repo-to-Product decisions.
- The git model maps one-to-one: Epic branch per Epic, Story branches into it, default branch guarded by human Accept (branching ADR).
- Dashboards and golden stories can assume the hierarchy uniformly — no special-casing "epics without features."
- The degenerate path obligates the intake contract ADR to define the auto-created Feature record.
- Mid-flight changes to accepted artifacts are tempo-independent (a requirements tweak during a Workbench loop; a Coder/Architect-agreed requirement fix in factory mode) and obligate the artifacts ADR (item 3) to define amendment records — linked addenda carrying author, reviewer, and reason, never mutation — with the review invariant applying to amendments.
- Renames are done: no document or schema may reintroduce "Task" as a work unit.

## Related Documents

- [Roadmap](../v2/roadmap.md) Core Vocabulary, naming note, D1/D4; [ADR backlog](../v2/adr-backlog.md) taxonomy questions.
- [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md) (conventions), [ADR 0019](0019-orchestrator-boundary.md) (Orchestrator boundary), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (review invariant), intake contract ADR (item 6, forthcoming).
- Supersedes the role taxonomy in historical note [0003](0003-agent-roles-and-finite-state-machines.md) (FSM discipline itself is unaffected and remains v2-aligned).
