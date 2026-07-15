+++
title = "ADR 0024: Intake And Triage Artifact Contract"
edit_date = "2026-07-15"
status = "live"
summary = "Fixes what intake produces — Feature and Epic records, triage outputs, provenance, review, and the dispatch seam — while deliberately leaving the intake executor unbound until the pre-Phase-5 spike."
+++

# 0024. Intake And Triage Artifact Contract

Status: Accepted (Codex + DR, 2026-07-13); amended 2026-07-14 (Story dispatch moved from Architect to Orchestrator, aligning with ADR 0019 as amended)

## Context

Intake is a triage function owned by the Orchestrator, not a standing agent (roadmap D2, revised 2026-07-12). What must happen is small: is this Feature one Epic or several, and per Epic — mode (Workbench or factory), repository, dependencies. The executor — form logic, short-lived triage agent, provisional Work Group — is deliberately unbound until the pre-Phase-5 spike, after two phases of lived intake friction. But Phase 3 must build a contract-only intake path *now* without preempting that spike, which is only safe if the contract is fixed: whatever performs intake, it produces identical artifacts. This ADR is that contract. It also discharges assignments from ADRs 0018 (the wrapper Feature record), 0020 (cross-Epic coherence review assignment), and 0021 (accountable authorship at intake).

## Decision

### What intake produces

Every intake path terminates in the same record shapes, all Management artifacts under ADR 0021's signature (scope, lineage, accountable author, review):

- A **Feature record** — the highest-level ask, carrying its intent content and provenance.
- One or more **Epic records**, each carrying the three triage outputs: **mode** (`workbench` | `factory`), **repository** (with Product lineage via the repo's primary Product where inferred), and **dependencies** (on other Epics, if any).
- A **dispatch** per dependency-ready Epic through the Orchestrator's seam (ADR 0019): the Epic record plus its seed artifacts — the Feature's effective view and, when available, a knowledge pack — per ADR 0021's handoff rule. Seeds must suffice to commence.

Dependency-bearing Features dispatch as a DAG, and the DAG is Orchestrator-owned: intake persists the full Epic dependency graph; the Orchestrator dispatches only dependency-ready Epics, holds blocked Epics until their upstream Epics are accepted (Epic-to-default merged, ADR 0023), and reruns the deterministic pre-checks below before releasing a held Epic. The division of labor is fixed (amended 2026-07-14, aligned with ADR 0019 as amended): **authoring is inference, dispatch is rules. The Architect owns the Story decomposition and its dependency graph within an Epic; the Orchestrator owns dispatch at both grains** — releasing dependency-ready Epics across a Feature and dependency-ready Stories within an Epic to available executors under configured policy, from the durable backlog as the authoritative scheduler state.

### Entry paths

Four entry paths, one contract:

1. **Structured entry** — the operator answers the triage questions directly. Collecting structured answers is Orchestrator work (ADR 0019); the author of the resulting records is the human principal.
2. **Degenerate entry** — ceremony-free single-Story Epic creation (ADR 0018): anchors to an existing Feature when the work belongs to one; otherwise the Orchestrator auto-creates the wrapper Feature record — provenance `auto-created`, accountable author the submitting human (ADR 0021), Product parent inferred from the repository's primary Product.
3. **Workbench entry** — the special-case blank Feature request scoped to a repository (ADRs 0018/0023), dispatched through the same seam.
4. **Conversational entry** — greenfield asks that need a dialogue. The contract reserves this path and its record shapes; its executor is *the* open spike question and is not decided here.

### The escalation slot

Any triage question the operator cannot answer is an inference problem, so it spawns a short-lived triage agent (ADR 0019's boundary rule). The agent's proposed decomposition is a Management artifact like any other: accountable author the agent, reviewed per ADR 0020 before it becomes authoritative input.

### Review of intake artifacts

- Human- or agent-authored Epic framing is reviewed by the receiving Work Group — recipient pushback, scoped to the received Epic (ADR 0020).
- The **Feature record** is seed input and needs completed review before it is authoritative (ADR 0021): the receiving Work Group's recipient review includes the Feature effective view alongside the Epic framing. The Feature reaches `accepted` on the first completed recipient review; later recipients may still push back, flowing as amendments. This is the minimal assignment — no unreviewed Feature ever seeds work — and the spike may strengthen it for multi-Epic Features with a dedicated Feature-intent review.
- **Cross-Epic coherence** splits by the ADR 0019 rule. The deterministic half is assigned here: the Orchestrator runs data-plane pre-checks at dispatch time — in-flight Epics touching the same repository, dependency cycles among the new Epics. **Findings gate dispatch**: a detected conflict or cycle blocks the affected Epic's dispatch and becomes a Management blocker artifact; the Orchestrator releases dispatch only when an authored, reviewed resolution clears it — a human decision, or an agent proposal via the escalation slot. The Orchestrator gates deterministically; resolution is judgment. The remaining judgment half — is this multi-Epic decomposition *good* — is explicitly deferred to the pre-Phase-5 spike, which decides who reviews it.

### Explicitly unbound

Reserved for the pre-Phase-5 spike, and nothing in Phase 3 may foreclose them: the final form-field shapes; the triage agent's brief; the provisional Work Group lifecycle and its single-repo continuity into execution; the conversational-entry executor; the cross-Epic coherence reviewer; graduation criteria for a standing intake agent.

Phase 3's constraint is restated: its intake path is contract-only — a minimal manual path implementing exactly these record shapes, nothing more.

## Consequences

- Intake executors are swappable by construction: form, agent, and any future design produce identical artifacts, so the spike's decision changes no schema and breaks no consumer.
- The spike is now bounded — its open questions are enumerated here rather than discovered later.
- The wrapper-Feature, accountable-authorship, and coherence-assignment obligations from 0018/0020/0021 are discharged; the deterministic coherence pre-checks give Phase 3 real conflict detection without any inference.
- Dispatch-time pre-checks make the "conflict with work in flight" roadmap concern a query with teeth: findings block dispatch until resolved, rather than annotating Epics that proceed anyway.
- The Epic DAG gives D4's back-pressure its mechanism: downstream Epics wait on upstream acceptance by construction, and the dashboard's queue view reads straight off the held-Epic set.

## Related Documents

- [Roadmap](../v2/plan_roadmap.md) D2 and the pre-Phase-5 spike; [ADR backlog](../v2/notes_adr-backlog.md) Intake And Triage entry (this is the first of its two staged ADRs).
- [ADR 0018](0018-v2-work-taxonomy.md) (degenerate path, wrapper Feature), [ADR 0019](0019-orchestrator-boundary.md) (seams, boundary rule), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (recipient review, coherence assignment), [ADR 0021](0021-artifacts-and-principal-instances.md) (signatures, handoff, accountable authorship), [ADR 0023](0023-v2-branch-strategy.md) (Workbench entry dispatch).
