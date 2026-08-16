+++
title = "Architecture Decision Records"
edit_date = "2026-08-15"
status = "live"
summary = "Index of Maestro ADRs: the v2 decision sequence (0017+) and the deprecated historical v1 notes (0001-0016), with the documentation authority order in brief."
+++

# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for Maestro, in a single
numbered sequence with two tiers:

- **0001–0016: historical v1 notes.** Proposed current-state summaries of the v1
  implementation (as of 2026-07-06). They were never accepted as binding and never
  will be; they remain useful context about how v1 works and carry `deprecated`
  status (stamped in the Phase 0 doc reset). v1 is deprecated as of 2026-07-11
  (tag `v1-freeze`).
- **0017+: v2 decisions.** These follow the lifecycle in
  [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md): Proposed →
  Accepted (Codex + DR approval) → Superseded/Rejected. A v2 ADR that replaces a
  historical note marks it Superseded explicitly.

## Documentation Authority

Defined by [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md). In short:
for current runtime behavior — code and tests, then the canonical FSM docs
(`pkg/*/STATES.md`), then `CLAUDE.md`/`README.md`, then `deprecated` v1 docs as
unverified hints. For v2 design intent — Accepted ADRs (0017+), then live phase
artifacts in `docs/v2/phase_x/`, then the roadmap and cross-phase docs in
`docs/v2/`, then the historical notes below. Archived documents carry no authority.

## v2 ADRs

| ADR | Title | Status | Summary |
| --- | --- | --- | --- |
| [0017](0017-v2-documentation-authority-and-lifecycle.md) | v2 Documentation Authority And Lifecycle | Accepted | Defines v2 doc conventions: ADR numbering and acceptance lifecycle, front-matter schema, draft/live/deprecated/archive authority, type_slug.md naming, directory indexes, and the v1 doc archive plan. |
| [0018](0018-v2-work-taxonomy.md) | v2 Work Taxonomy | Accepted | Defines the v2 work hierarchy — Product, Feature, Epic, Story — and its executors (Work Group, Workbench tempo), the collapsible degenerate path for small work, and the v2 MVP boundary (D1). |
| [0019](0019-orchestrator-boundary.md) | Orchestrator Boundary | Accepted | Defines the v2 Orchestrator as the programmatic, non-agentic layer owning agent lifecycle, tools, routing, forge, persistence, and scheduling — with the no-inference rule as the boundary test. A proposed second amendment settles what happens to work already executing when its record is amended: cancel rather than suspend or complete-then-reconcile, triggered when the execution's bound effective work version is no longer current or DAG re-evaluation leaves its work not dependency-ready, enforced in both cases by superseding the execution's own authority, and sequenced so admission closes, every admitted attempt reaches one of ADR 0030's three permitted dispositions and is settled, the resource is fenced, and only then is capacity released and the result recorded as cancelled/superseded — with what already happened left intact — Audit final, drafts still draft, and accepted artifacts still accepted under ADR 0021's own lifecycle — because an amendment changes what the work must satisfy rather than rewriting what happened. |
| [0020](0020-review-invariant-reviewer-vs-partner.md) | The Review Invariant — Reviewer vs Partner/Supervisor | Accepted | Canonical statement of the symmetric review invariant (every Management artifact reviewed by a non-author) and the two review scopes: narrow Reviewers that block, and Partner/Supervisors that judge; reviewer heterogeneity is measured in model lineage — the set of originating labs — on a three-rung ladder that warns but never refuses, with unknown lineage held outside it as unclassified. |
| [0021](0021-artifacts-and-principal-instances.md) | Artifacts And Principal Instances | Accepted | Defines the v2 artifact model: artifacts as the sole agent handoff, Management (inputs) vs Audit (exhaust) categories, the scope/lineage signature, principal instances (agent/human/system), the invalidate/amend/supersede lifecycle, evidence retention-pinning, and the MPH signature. |
| [0022](0022-v2-data-plane.md) | v2 Data Plane | Accepted | Postgres/sqlc/golang-migrate as the v2 data plane, Docker-local by default; schema families derived from the taxonomy and artifact model; multi-user boundaries; all access through the Orchestrator's persistence seam. |
| [0023](0023-v2-branch-strategy.md) | v2 Branch Strategy | Accepted | Maps git structure to the work hierarchy: Epic branches off default, Story branches off Epic; automated Story→Epic merges, human Accept for Epic→default; reviewed history immutability; naming for Orchestrator-managed branches. |
| [0024](0024-intake-and-triage-artifact-contract.md) | Intake And Triage Artifact Contract | Accepted | Fixes what intake produces — Feature and Epic records, triage outputs, provenance, review, and the dispatch seam — while deliberately leaving the intake executor unbound until the pre-Phase-5 spike. |
| [0025](0025-golden-stories-and-benchmark-runner.md) | Golden Stories And The Benchmark Runner | Accepted | Specifies the golden story instrument: story schema, the black-box runner contract and its self-contained results store, D9 sampling and budget mechanics, MPH configurations including the single-agent baseline, and the golden-minimal/golden-all suite tiers. Amended 2026-07-22 (Codex + DR) to conformance-first sequencing: proving the pipeline completes progressively harder stories comes first; economic baselining defers to Phase 1B after Phase 7. |
| [0026](0026-multi-architecture-artifacts.md) | Multi-Architecture Distributable Artifacts | Accepted | Every artifact built on one architecture and executed on another — embedded binaries and published container images — must be built for all target architectures (at least linux/amd64 + linux/arm64) and verified on each: images as a multi-arch manifest pinned by its arch-independent digest, binaries cross-compiled and runtime-selected. Single-arch builds of cross-arch artifacts are a defect. |
| [0027](0027-concurrency-safety-for-shared-local-infrastructure.md) | Concurrency Safety For Shared Local Infrastructure | Accepted | Any operation mutating state reachable from more than one agent lifecycle — git mirrors, workspace directories, dispatcher leases, supervisor restarts — must be serialized by a key matching the shared resource (path, agent ID, story ID) or made idempotent, and destructive recovery must never delete another actor's in-progress work. Bare last-writer-wins on shared infrastructure is a defect. |
| [0028](0028-artifact-envelopes-and-payload-schemas.md) | Artifact Envelopes And Payload Schemas | Accepted | The encoding layer under ADR 0021: a relational envelope plus a typed JSON payload digested with RFC 8785 JCS; a code-resident payload type registry validated at the persistence seam; additive-only schema evolution; amendments as RFC 7396 merge patches materialized on read; reviews bound to the whole reviewable projection, not the payload alone. |
| [0029](0029-incubator-and-habitat-execution-boundaries.md) | Incubator And Habitat Execution Boundaries | Accepted | Splits the single conflated execution resource into two Orchestrator-managed types: the Incubator, a unitary Story-scoped development environment carrying a toolchain and no ecosystem because it must also be implementable on platforms that reject containers, and the Habitat, a deployed application environment holding every Maestro-managed dependent service. Source and definitions cross only as one immutable forge commit -- exactly one candidate repository commit is promoted, with any other user-authored service participating as an immutable artifact digest that commit declares; spec identity closes over the inputs describing the environment while the inputs producing the candidate stay in a separate deployment closure, with runtime configuration as a third axis carrying its own lifecycle; specification, instance, and deployment are distinct identities, as are the lease that authorizes and the retention claim that keeps an environment warm; contracts route by what they require rather than what they are named; reset is demanded before evidence-bearing verification and on transfer of ownership, and must prove namespacing or removal rather than assume teardown suffices; and both types are fenced as provider-created domains that must be closed under the authority they expose, returning a three-valued receipt in which isolated means every path into state current or future work will touch is either closed by the authority enforcing it or leads to a permanently abandoned target -- never merely that time has passed, and never requiring the fenced generation to stop running -- and in which fence state and cleanup state are independent, so every resource not yet confirmed deallocated stays visibly recorded, retried, and flagged as potentially billable regardless of fence state, under a cleanup alarm distinct from the fencing one. |
| [0030](0030-tool-execution-policy-hook.md) | The Tool Execution Boundary And Its Policy Hook | Accepted | All agent requests for Maestro-managed effects pass through one Orchestrator boundary that records intent, validates machine policy, obtains human approval when required, and records the result. Human approval blocks the Story and caller without consuming LLM turns; headless runs instead mark the Story blocked. Expensive resources are acquired only after approval, and all current authorization, work-version, and resource conditions are revalidated immediately before execution. The design centralizes policy and auditing without introducing approval retries, resolution chains, or arbitrary agent checkpointing. |
| [0031](0031-prompt-pack-identity-resolution-and-storage.md) | Prompt Pack Identity, Resolution, And Storage | Accepted | Gives the MPH signature's P component the minimal contract it has carried informally since Phase 1. A pack version is whole and immutable, identified by a content digest under a named scheme, and comparison never crosses schemes, so an imported v1 identity stays opaque rather than joining a group with a v2 pack. The name is a label, so a selector names exactly one plane-owned version; mutable metadata -- display name, declared version range, declared role coverage -- lives on a separate installation record, because it sits outside the digest that makes content immutable. Resolution happens once at dispatch and is reused on restart. Organization provisioning imports the built-in pack and seeds the selector that makes it resolvable, and there is no run-time fallback to the binary. Compatibility is validated against the running harness rather than asserted by a declared range. P identifies the pack, not the prompt: accounting for a model input is invocation provenance over four sources, whose bindings, closure status, and retention rule belong to the agent execution contract. |
| [0032](0032-agent-execution-contract.md) | The Agent Execution Contract | Accepted | Defines the versioned wire contract every agent reaches Maestro through, so a native Go agent and an adapted external runtime meet one boundary and neither receives a local path or a database connection. What was resolved for an execution must not silently change while its resource grants may be replaced mid-execution, so the two carry different lifetimes; how that is represented is Phase 3's. The terminal result is four independent axes rather than one status list, so an already-satisfied Story, a superseded cancellation, a gate no operator can answer, and an infrastructure failure stop colliding. Every Orchestrator-forced ending closes admission, drains the actions it admitted, fences the resource, and only then records a result, and recovery is artifact-level: an agent restarts from the last committed workflow artifact rather than from where it stopped. A post-acceptance scope correction (Codex + DR, 2026-08-15) closes the list of binding decisions at thirteen -- the boundary, the identities, the four axes with their applicability rule, mediated actions, the fencing preconditions, the two lifetimes, the concurrency accounting and the capability model -- and returns everything else, including the execution FSM, re-attach, the delivery machinery, the question-wait lifecycle and reusable approvals, to Phase 3 as design inputs. |

## Historical v1 Notes

| ADR | Title | Summary |
| --- | --- | --- |
| [0001](0001-documentation-authority-and-adr-lifecycle.md) | Documentation Authority and ADR Lifecycle | v1 documentation authority order and ADR lifecycle as practiced; superseded for v2 questions by ADR 0017. |
| [0002](0002-local-single-user-runtime-kernel.md) | Local Single-User Runtime Kernel | v1 single-user runtime kernel, supervisor, and dispatcher architecture; superseded for v2 by ADR 0019. |
| [0003](0003-agent-roles-and-finite-state-machines.md) | Agent Roles and Finite-State Machines | v1 agent roles (PM, Architect, Coder) and their finite state machines. |
| [0004](0004-channel-dispatch-and-typed-agent-protocol.md) | Channel Dispatch and Typed Agent Protocol | v1 typed channel dispatch and agent message protocol; the discipline carries into v2 via ADR 0019. |
| [0005](0005-sqlite-session-persistence-and-resume.md) | SQLite Session Persistence and Resume | v1 SQLite session persistence and resume; superseded for v2 intent by ADR 0022. |
| [0006](0006-toolloop-process-effect-and-terminal-tools.md) | Toolloop ProcessEffect and Terminal Tools | v1 toolloop ProcessEffect and terminal-tool discipline; promoted to a v2 data-plane rule by ADR 0022. |
| [0007](0007-llm-provider-boundary-through-maestro-llms.md) | LLM Provider Boundary Through maestro-llms | v1 LLM provider boundary through the maestro-llms toolkit. |
| [0008](0008-container-workspace-and-compose-isolation.md) | Container, Workspace, and Compose Isolation | v1 container, workspace, and compose isolation model. |
| [0009](0009-clone-mirror-and-forge-pr-workflow.md) | Clone, Mirror, and Forge PR Workflow | v1 clone, mirror, and forge PR workflow. |
| [0010](0010-pm-led-spec-bootstrap-hotfix-and-demo-lifecycle.md) | PM-Led Spec, Bootstrap, Hotfix, and Demo Lifecycle | v1 PM-led spec, bootstrap, hotfix, and demo lifecycle; superseded conceptually by v2 intake (ADR 0024) and the Workbench. |
| [0011](0011-configuration-operating-modes-and-secrets.md) | Configuration, Operating Modes, and Secrets | v1 configuration, operating modes, and secrets handling; superseded for v2 intent by the project-folder spike and ADR 0022 as amended. |
| [0012](0012-knowledge-graph-as-repository-artifact.md) | Knowledge Graph as Repository Artifact | v1 knowledge graph as a repository artifact; superseded for v2 by the maestro-cms spike direction. |
| [0013](0013-testing-strategy-and-service-boundaries.md) | Testing Strategy and Service Boundaries | v1 testing strategy and service boundaries. |
| [0014](0014-failure-taxonomy-durable-asks-and-incidents.md) | Failure Taxonomy, Durable Asks, and Incidents | v1 failure taxonomy, durable asks, and incident handling. |
| [0015](0015-agent-chat-and-human-in-the-loop-escalation.md) | Agent Chat and Human-in-the-Loop Escalation | v1 agent chat and human-in-the-loop escalation. |
| [0016](0016-architect-per-agent-conversation-context.md) | Architect Per-Agent Conversation Context | v1 architect per-agent conversation context design. |

## ADR Format

v2 ADRs (0017+) carry TOML front-matter (`title`, `edit_date`, `status`, `summary`) and include:

- `Status`: Proposed, Accepted, Superseded, or Rejected.
- `Context`: Why this decision matters.
- `Decision`: The design contract.
- `Consequences`: Trade-offs and follow-up obligations.
- `Related Documents`: Sources, superseded notes, and history.
- `Implementation Notes` (optional): code paths, when implementation exists.

The historical notes (0001–0016) used a mandatory `Current Implementation` section
instead, appropriate to their current-state purpose.

