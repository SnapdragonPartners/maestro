+++
title = "Maestro v2 Roadmap"
edit_date = "2026-07-22"
status = "live"
summary = "The v2 roadmap: thesis, economic argument, vocabulary, 17 design pillars, phases 0-9 with exit criteria, and decisions D1-D10. Decisions are progressively ratified into ADRs (0017+), which outrank this document."
type = "plan"
+++

# Maestro v2 Roadmap

Date: 2026-07-10; revised through 2026-07-13.
Status: live planning document — decisions are progressively ratified into ADRs (0017+), which outrank this roadmap for v2 design intent.

This document blends:

- Maestro v1 lived experience.
- Recent client-project lessons.
- The Google/McKinsey research corpus summarized in [research_synthesis.md](research_synthesis.md).
- Codex and Claude feedback.

It is intentionally opinionated but provisional. The goal is to create a draft that can be marked up, argued with, and converted into ADRs and implementation specs.

## Executive Summary

Maestro v2 should be a clean, breaking redesign around one core idea:

> Maestro is an agentic factory for individuals and teams doing large-scale, production-grade software work. It converts ambiguous human intent into reviewed, evidenced, auditable changes by optimizing the Model/Prompt/Harness (MPH) system around software development best practices, reusable artifacts, epic-scoped work groups, governed context, and measurable outcomes.

Maestro v1 already embodied many ideas that later appeared in the external research: role separation, state-machine workflows, review loops, isolated workspaces, PR-based delivery, human escalation, and agent orchestration. The research is useful less as an origin story and more as validation, vocabulary, and pressure to make the harness explicit.

The v2 sequencing principle:

> Build the measuring instrument first, then persistence so we can measure, iterate and improve.

Golden stories, metrics, artifact signatures, and run comparison need to appear early enough that later model, prompt, and harness changes can be evaluated against evidence rather than anecdote.

## The Economic Argument

Paired agents look expensive next to vibe coding, and Maestro v1 demos have repeatedly required explaining why spending 100k tokens building a spec is a good thing. On the happy path, a single agent driving straight at a diff is always cheaper per token.

The factory's bet is that cost per token is the wrong denominator; cost per accepted change is the right one:

- Errors caught at spec and plan time are far cheaper than errors caught in review, and cheaper still than errors caught in production. Measure twice, cut once.
- Paired review suppresses hallucination, scope drift, and rule/compliance drift — the failure modes that quietly compound when many agents ship into one codebase.
- The premium buys evidence and auditability, which is what makes human acceptance fast and defensible.

This is a testable claim, not a slogan. The golden story suite should include a single-agent happy-path baseline configuration so the paired-agent premium and its payoff — rework, review cycles, failure kinds, cost to accepted change (D6) — are quantified rather than asserted. That number is also the answer to the demo question.

## Product North Star

Maestro v2 helps a person or team supervise many software-producing agents without losing control, taste, evidence, or accountability.

Success looks like:

- A human can express a feature-level goal or an entire greenfield MVP app.
- Intake can push back on ridiculous scope and preserve oversized requests as higher-level Feature memory — artifacts in the data plane, not any agent's conversation — rather than forcing them into immediate execution.
- Maestro, meaning the whole product, turns Features into repo-scoped Epics and PR-sized Stories through its Orchestrator, its intake/triage function, and Work Groups.
- Work Groups execute concurrently in isolated workspaces.
- Every persistent Management artifact is reviewed by a party other than its author: agent-authored artifacts by another agent (or a human gate), human-authored artifacts (e.g. intake form output) by the receiving agent.
- Contention between paired agents escalates to a human after a bounded number of turns.
- Every Story and Epic carries evidence explaining why it was accepted.
- Every model call, tool call, prompt pack, harness decision, and artifact is auditable.
- The system can be benchmarked against golden stories and third-party industry benchmarks to improve token cost, wall-clock time, quality, and reliability.
- Eventually, Maestro can build most of Maestro.
- The system does not get "stuck." All issues are resolved or escalated.

Non-goals:

- The 80/20 principle applies: approximately 80% of features can be built by this kind of automated development, but approximately 20% require a tighter interaction with an operator ("conductor" instead of "orchestrator"). The non-goal is fully automating that 20% — not abandoning it. The Workbench (pillar 17) is how conductor-style work stays inside Maestro's provenance and evidence rather than leaking to outside tools.

### Measurable Success Criteria

The north star needs numbers behind it. Candidate v2 acceptance measures:

- The golden story suite runs end-to-end on demand and produces comparable reports across MPH configurations.
- Cost to accepted change on the golden suite trends down release over release.
- Human attention per accepted change (interventions plus wall-clock blocked on a person) trends down.
- A defined share of Maestro's own development flows through Maestro (Phase 9 target: 80%).
- Every Epic merged in daily use carries a complete evidence package.

These should be baselined as soon as the Phase 1 runner exists and reported alongside every subsequent phase.

## Human Operators

For v2 all human operators are treated equivalently and with interchangeable levels of access. Any process overlay (e.g. product people enter feature requests and engineers review outcomes) is outside of product scope and up to the operators themselves to manage in process. One human can operate all of Maestro.

## Core Vocabulary

Naming note (2026-07-11): the repo-scoped work unit was originally called Task and the executing unit Task Team. Renamed to Epic and Work Group: Feature > Epic > Story preserves the universal industry prior that an Epic contains Stories, and removes collisions with the v1 TASK message type and the generic "task" language pervasive in agent tooling. The v1 hotfix path generalizes to the Workbench (see pillar 17); "Hotfix" implied bug fixes only, and the interim name "Live Mode" implied a live product.

### Product

A Product groups one or more repositories that together deliver a user-facing or operational system. It may include a frontend repo, backend repo, shared libraries, deployment configs, and demo/UAT instructions.

Product likely needs to be a real data model, not only a knowledge-base concept, because it affects dashboards, multi-repo knowledge, multi-repo UAT, and golden stories.

### Feature

The highest-level ask from the human. A Feature may span multiple repositories and contain many Epics. It may also be a complete app MVP for a greenfield project.

Example: "Add team-based project management and billing to Maestro Cloud."
Example: "Build an app to parse these sample JSON files and put them in a database."

### Orchestrator

The software layer that manages agents and the factory's foundational machinery: agent launch and destruction, tool implementation, message routing, forge interaction, persistence, and scheduling. It is entirely programmatic — maximally fault tolerant, and deterministic to the extent software can be — and it is not an agent: it never interacts with an LLM directly, only through the agents it spawns.

The boundary rule: decisions from rules and config belong to the Orchestrator; decisions requiring inference belong to an agent. The moment an LLM gets involved in a workflow step, that step is an agent — however small or short-lived. (The intake form's "I don't know" button is this rule in miniature: the form is Orchestrator, pressing the button spawns an agent.)

In v1 terms the Orchestrator is the evolution of the runtime kernel, supervisor, and dispatcher (all "port largely as-is" in D8). Pillar 15 sketches its v3 trajectory as an orchestration plane.

### Intake And Triage

The organization-scoped function that turns a Feature ask into dispatched Epics. What actually has to happen is small: decide whether the Feature is one Epic or several, and per Epic decide mode (Workbench or factory), repo, and dependencies.

Direction (revised 2026-07-12, after external design review): intake is a triage function owned by the Orchestrator, not a standing agent pair. The primary surface is a form — mode, repo(s), size — with an "I don't know" button that spins up a short-lived triage agent for exactly the question the operator cannot answer. Conversational intake (greenfield asks) runs through a provisional Work Group whose PM is scoped to the Feature. The earlier CPA/CTA (Chief Product Agent / Chief Technical Agent) concept is retired as a standing pair; see D2 and the pre-Phase-5 intake spike.

Invariant: the trivial path may bypass the agent, never the artifact — even the ten-second expert path emits Feature/Epic records with provenance.

### Epic

A repo-scoped component of a Feature assigned to a Work Group. An Epic owns an Epic branch and may not be fully designed when first created.

Example: "Add Postgres-backed artifact persistence to the Maestro repository."

### Work Group

The group of agents assigned to execute an Epic. A Work Group includes agents, workspace, branch, prompt pack, harness config, review/evidence policy, and gates.

MVP constraint: support one Work Group execution path first. Multiple concurrent teams can follow after the runtime object and persistence model are stable.

### PM

The user/human-supervisor-scoped requirements agent inside a Work Group. Intake/triage handles Feature decomposition; PM refines a specific Epic with the relevant human supervisor. In conversational intake, a provisional Work Group's PM — scoped to the Feature — runs the Feature-level conversation, and in the single-repo case that group continues as the executing group.

This split may be simplified during MVP, but the conceptual distinction is useful.

### Architect

The Epic/Story technical partner and supervisor inside a Work Group. It reviews Epic plans, Story plans, code, completion evidence, and merge decisions.

### Story

A PR-sized chunk of an Epic assigned to a single Coder. It should be large enough to be useful and small enough to be testable, reviewable, and mergeable.

Story decomposition should optimize for parallel development by multiple Coders.

### Workbench

A Work Group execution tempo optimized for rapid, interactive iteration with a human — polish, debugging, tweaks, follow-ups — rather than backlog work. It generalizes and replaces the v1 hotfix path, whose name wrongly implied bug fixes only. The Workbench is a harness preset, not a separate system: same Epic/Story data model, same branches and evidence, different review timing. Entered from a Workbench button on the master dashboard (a special-case blank Feature request scoped to a repo) or from an existing Epic. See pillar 17 and D10.

### MPH

Model / Prompt / Harness. The three major levers in the software factory.

- Model: provider, model family, model parameters, model routing.
- Prompt: prompt packs, role instructions, skills, review prompts.
- Harness: workflow graph, tools, policy gates, context loading, containers, branch strategy, evals, artifact handling, recovery loops.

## Design Pillars

### 1. Golden Stories And MPH Benchmarking

Golden stories are deterministic or semi-deterministic work items used to evaluate Maestro itself.

They should ladder in complexity:

- Dependency bump.
- Small code cleanup.
- Focused bug fix.
- API change with tests.
- UI change with screenshot/browser evidence.
- Feature with database migration.
- Multi-story Epic with merge conflicts.
- Epic requiring external service/container setup.

The full suite should eventually span at least two repos and at least one UI-bearing repo. The first stories, however, should be single-repo and Story-scoped: multi-repo and UI-evidence stories depend on Product/Feature machinery and browser tooling that land in later phases, and the runner needs simple stories to prove itself. Good fixture candidates include forked/pinned variants of `maestro-cms`, `maestro-llms`, and a UI repo. Fixture repos should use pinned base branches and clean up golden branches after each run.

Each golden story should define:

- Starting repo and commit.
- Input Feature, Epic, or Story prompt.
- Allowed files or expected affected areas.
- Expected validators.
- Required artifacts.
- Pass/fail checks.
- Optional scored rubrics.
- Expected evidence package shape.
- Budget expectations.

The benchmark harness should compare:

- Prompt packs.
- Model choices.
- Reviewer models.
- Harness changes.
- Tool availability.
- Context strategies.
- Branch strategies.
- Paired-agent versus single-agent happy-path baselines (see The Economic Argument).

Golden stories measure Maestro against itself. Third-party industry benchmarks (the v1 SWE-EVO harness work is a seed) complement them for cross-system comparison and for model-science questions such as SLM viability and new-model TCO; see D6.

Metrics should include:

- Token use.
- Wall-clock time.
- LLM call count.
- Tool call count.
- Iteration count.
- Review cycles.
- Self-repair cycles.
- Human interventions.
- Human attention time (wall-clock blocked on a person).
- Test/eval pass rate.
- Cost to accepted change.
- Failure kind.

#### Runner Design Constraints

- **Black-box.** The runner drives its target through external surfaces only: config, CLI/API invocation, and the resulting branches, PRs, artifacts, and metrics. It never imports Maestro internals. This is what lets one runner survive the v1-to-v2 break, benchmark the frozen v1 binary as just another target, and later benchmark harnesses that do not exist yet.
- **Self-contained persistence.** The runner owns its result store (flat JSON files or an embedded database). It must not depend on the Phase 2 data plane; otherwise Phase 1 silently acquires a Phase 2 dependency. Importing runner results into the data plane is a Phase 2+ integration.
- **Nondeterminism is the default.** Golden story runs are stochastic. Standard comparisons use N repeat runs per story per configuration and report spread, not point values. Single runs are for smoke checks only. See D9.
- **Budgeted.** Every benchmark configuration declares an expected token/dollar cost before it runs. The matrix (stories x models x prompt packs x harness configs x repeats) multiplies fast; see the benchmark cost risk.

This is the first major v2 enabling project. It is also the most direct path to "Maestro builds Maestro."

### 2. Management And Audit Artifacts

Everything important should be persisted, but not everything should be presented equally.

Use two artifact categories.

#### Management Artifacts

Management artifacts are intended for human review and comprehension.

Examples:

- Feature brief.
- Requirements draft.
- Requirements review.
- Epic plan.
- Story list.
- Story plan.
- Evidence package.
- Acceptance decision.
- Human approval.
- Incident summary.
- Postmortem.

#### Audit Artifacts

Audit artifacts are durable, queryable logs for debugging, reconstruction, and bulk analysis.

Examples:

- Tool call.
- LLM call summary.
- Raw trace.
- Metric event.
- Agent checkpoint.
- Message event.
- Compaction input/output.

Humans may occasionally inspect Audit artifacts, but the UI should summarize and route attention through Management artifacts.

#### Minimal Artifact Signature

Artifacts attach to a scope, not to an Epic. Feature briefs exist before any Epic does; Product-level knowledge, org-level prompt packs and skills, benchmark artifacts, and some Audit events never belong to one. Start with a minimal skeleton:

- `artifact_id`
- `artifact_type`
- `artifact_category` (`management` or `audit`)
- `status`
- `scope_type` (`organization`, `product`, `feature`, `epic`, `story`, `benchmark`, ...)
- `scope_id`
- `product_id`, `feature_id`, `epic_id`, `story_id` — nullable, denormalized lineage for querying, populated as far up the hierarchy as the scope implies
- `author_agent_instance_id`
- `reviewer_agent_instance_id`, nullable
- `created_at`
- `payload`
- `schema_version`

An Epic-scoped artifact still infers Feature, Product, organization, and user through its lineage columns; the scope model just stops pretending everything has an Epic. The Phase 0 artifact ADR fixes the exact shape.

Artifact payloads should be JSON with schema/version fields. Markdown can be a human rendering format. TOML/YAML may be useful for prompt-facing fragments, but JSON is the storage/API substrate.

### 3. Agent Instances And Lightweight Signatures

Introduce explicit agent instances for provenance.

Minimal `agent_instance` fields:

- `agent_instance_id`
- `agent_type`
- `organization_id`
- `feature_id`, nullable (Feature-scoped instances: a provisional Work Group's PM, a triage agent)
- `epic_id`, nullable
- `story_id`, nullable
- `model`
- `prompt_pack_id`
- `prompt_hash`
- `harness_config_hash`
- `start_time`
- `stop_time`
- `stop_reason`
- `payload`

Lightweight agent signatures should bind:

- Agent instance.
- Model.
- Prompt hash.
- Harness config hash.
- Input artifact digests.
- Output artifact digest.
- Reviewer artifact digest, if reviewed.

Do not start with cryptographic signing unless a concrete requirement appears.

### 4. Postgres Data Plane

The v1 SQLite database started as a searchable log file. In v2 it becomes the data plane.

Move to:

- Postgres by default.
- Docker-hosted local Postgres for local mode.
- Cloud-hosted Postgres for team/cloud mode.
- `sqlc` for typed queries.
- `golang-migrate` for schema migrations.

Docker-local Postgres is acceptable as the community default because Maestro already requires Docker. True local Postgres can be supported later but should not be the default path.

Core schema families:

- Organizations and users.
- Products and repos.
- Features.
- Epics.
- Stories.
- Work Groups.
- Agent instances and runs.
- Prompt packs.
- LLM calls.
- Tool calls.
- Artifacts.
- Artifact reviews.
- Metrics.
- Gates.
- Knowledge items.
- Skills/patterns.
- Binary attachments or object-store references.
- Audit events.

Single-user local mode can use a default organization and user.

### 5. Multi-User And Multi-Epic

Maestro v2 should support teams, but the MVP should avoid full cloud/SaaS complexity.

Initial scope:

- Users belong to organizations.
- Major DB records carry organization and user lineage.
- Fine-grained roles/security groups are a later non-goal.
- Individual user credentials, such as forge tokens and LLM tokens, can do much of the early enforcement.
- Multiple Epics can eventually run concurrently.
- Agent groups are scoped to Epics, not the Maestro instance.
- Working directories are scoped to Epics.
- A dashboard shows active Features, Epics, Stories, Teams, gates, and blockers.

MVP correction:

- A standing intake agent is not required.
- Multiple concurrent Work Groups are not required.
- Cloud auth is not required.
- The local/team-capable architecture should be right before SaaS polish.

### 6. Branch Hierarchy

Current v1 behavior merges every Story to default. v2 should align git structure with the Feature/Epic/Story taxonomy:

- Feature may span repos and may not own a branch directly.
- Each Epic gets an Epic branch.
- Each Story gets a Story branch from the Epic branch head.
- Story branches merge into the Epic branch.
- The Epic branch merges into default when the Epic is accepted.
- Architect/Work Group can dispatch conflict resolution to a Workbench session.

This makes:

- Story = local implementation/review unit.
- Epic = integration/UAT/final acceptance unit.
- Feature = product intent and cross-repo coordination unit.

Rebasing becomes a harness function, not an incidental git operation.

### 7. Agent Pairs, Reviewers, And Partners

Rule:

> Any persistent Management artifact, document or code, must be reviewed by at least one party other than its author, with human escalation for irreconcilable contention.

The invariant is symmetric across author kinds: agent-authored artifacts are reviewed by another agent (or a human gate); human-authored artifacts — such as intake form output — are reviewed by the receiving agent (recipient review: the Work Group PM reviews a human-authored Feature). The Workbench satisfies it too: agent-authored work, reviewed by the present human plus the trailing drift check.

Distinguish two scopes.

#### Reviewer

A Reviewer checks correctness and completeness. It does not expand scope.

Examples:

- Coder/Internal Coder Reviewer.
- Budget reviewer.
- Artifact citation verifier.

Reviewer powers:

- Block excessive usage.
- Block non-adherence to the relevant artifact.
- Block incomplete or incorrect work.
- Trigger escalation after bounded back-and-forth, initially perhaps three iterations.

#### Partner/Supervisor

A Partner/Supervisor may add value and resolve higher-level questions.

Examples:

- PM/Architect.
- Architect/Coder.
- A Work Group challenging the Epic it was handed (recipient pushback).

Partner/Supervisor powers:

- Judge optimality.
- Enforce project guidelines, ADRs, docs, and best practices.
- Apply pluggable skills, such as compliance or security checks.
- Resolve ambiguity or escalate when necessary.

This distinction should become an ADR.

### 8. Optional Gates And UAT

V2 should support optional gates controlled by config:

1. Requirements complete: product review before Epic decomposition or execution.
2. Stories complete: technical review before Story execution.
3. UAT: pre-final Epic-level merge gate.

Recommended default:

- Story-to-Epic branch merge can be automated when Story evidence passes.
- Epic-to-default merge should default to human Accept.
- (Withdrawn 2026-07-13: the earlier idea of config-based Epic auto-merge for low-risk Epics. Acceptance is outcome validation, not risk management — see ADR 0020.)

Demo Mode likely becomes the foundation for UAT. It is not conceptually hard, but it becomes much cleaner after artifacts, gates, and Epic branches exist.

The green Accept UX should be based on the current Demo Mode pattern and backed by:

- Evidence package.
- Diff summary.
- UAT result.
- Risk summary.
- Remaining open questions.
- Gate status.

### 9. Evidence Packages

Every Story and Epic should produce an evidence package.

Story evidence:

- Story prompt/spec.
- Plan.
- Diff summary.
- Tests run.
- Test outputs.
- Build/lint outputs.
- Tool trajectory summary.
- Reviewer decision.
- Known risks.
- Commit/branch/PR metadata.

Epic evidence:

- Epic brief.
- Story list and dependency graph.
- Epic branch diff summary.
- Story evidence rollup.
- Integration test results.
- UAT artifacts, if gate enabled.
- Human acceptance, if required.
- Final merge decision.

UI work should include:

- Screenshots.
- Browser traces.
- Optional before/after videos.
- Accessibility or visual checks where available.

### 10. Prompt Packs

Prompt packs let users select a coherent set of prompts/instructions for the whole system.

Uses:

- A/B test prompt versions against golden stories.
- Support conservative vs aggressive modes.
- Support provider-specific prompt tuning.
- Allow internal/customer-specific operating styles.
- Version prompts independently of code.

Storage:

- Database should be canonical for installed org-level prompt packs.
- Prompt packs should be immutable, hash-addressed, exportable packages.
- Repo-versioned or file-imported packs should remain possible for open-source/community workflows.

Prompt pack metadata:

- Name.
- Version.
- Supported Maestro version.
- Intended mode.
- Agent roles covered.
- Model assumptions.
- Evaluation history.
- Changelog.

MVP note: prompt pack selection can be deferred if it blocks the golden story runner. A minimal prompt-pack ID/hash can be enough for early metrics.

### 11. Knowledge Revisit

The current DOT-based knowledge artifact was a useful step, but v2 should move knowledge into the data plane and make it multi-source.

Use `maestro-cms` where possible, especially for reusable document and binary ingestion.

Knowledge sources:

- Approved ADRs.
- Interfaces/contracts.
- README and docs.
- Skills library / prompt fragments.
- Artifact history.
- Review history.
- Incidents and postmortems.
- Domain/user-provided files.
- AST/code information, later where valuable.

#### Knowledge Hierarchy

Avoid one universal static order. Use claim type:

- ADRs: architectural intent.
- Interfaces/contracts: integration behavior.
- Docs: human/product semantics.
- AST/code facts: implementation reality.

Interfaces/contracts should be first-class because they can often let Coders work correctly without exploring implementation internals.

All code-level claims in a knowledge pack should have verifiable citations. A reviewer agent should verify citations against the repo. Flagged inconsistencies should not automatically mutate the knowledge base; they can create invalidated-pack notes or follow-up checks.

#### Knowledge Pack Flow

1. Query the knowledge base based on the Epic.
2. Hand raw knowledge to the PM in the Work Group.
3. PM uses it during Epic refinement with the user.
4. PM supplements or prunes based on final requirements and code-level discovery.
5. PM internal reviewer verifies findings.
6. Validated Epic knowledge pack goes to Architect.
7. Architect selects minimal Story-specific subsets for Coders.

Principle:

> As little as possible to preserve tokens; as much as necessary to prevent duplicative repo exploration.

Artifacts, including knowledge packs, should be the only context passed between distinct agents to kick off work. After that, agents communicate through structured messages such as QUESTION/ANSWER and REQUEST/RESPONSE. Those messages are Audit artifacts.

### 12. Skills And Pattern Registry

Skills are procedural memory. V2 should have a first-class place for them.

Possible skill/pattern types:

- Spec-writing patterns.
- Story decomposition patterns.
- Code review patterns.
- Bug forensic patterns.
- UI validation patterns.
- Database migration patterns.
- Project-specific architecture rules.
- Domain-specific workflows.

Governance:

- Owner.
- Version.
- Trigger examples.
- Anti-trigger examples.
- Eval cases.
- Promotion tier: read-only, draft-only, action-allowed.
- Provenance: human-authored, agent-harvested, imported.

Storage:

- Database should be canonical for installed org-level skills.
- Skills should be immutable/versioned and exportable.
- Repo-local skills should remain possible when they are tightly bound to a specific project.

Agent-harvested skills always enter as draft.

### 13. Binary And Rich Spec Uploads

PM and intake uploads should support binaries:

- Images.
- Spreadsheets.
- PDFs.
- Word docs.
- Diagrams.
- Other domain artifacts.

Default storage should be data plane/object storage, content-addressed by digest. Put binaries in the repo only when they are actual project artifacts.

Binary attachments may be:

- Sent to multimodal models during analysis.
- Linked to Feature/Epic artifacts.
- Summarized/indexed into the knowledge system.
- Retained if required for future understanding.

### 14. Artifact-Based UI

The raw log view should become an artifact stream.

UI concepts:

- Intake/Master Dashboard.
- Epic Dashboard.
- Story board.
- Artifact timeline.
- Evidence package viewer.
- Gate review page.
- Metrics/benchmark page.
- Prompt pack comparison page.
- Knowledge browser.
- Skill/pattern registry.
- Agent trajectory replay.

Draft dashboard shape:

- Intake/Master Dashboard: intake form and chat, list of items in flight, state for each item, and status values such as READY, PROCESSING, and AWAITING USER.
- Epic Dashboard: scoped workflow view similar to v1, with Demo Mode becoming UAT and logs/messages/stories replaced by a single filterable artifact view.
- Epic states: INTAKE, REQUIREMENTS DEVELOPMENT, REVIEW GATE, STORY DEVELOPMENT, REVIEW GATE, BUILDING, VERIFYING, UAT GATE, COMPLETE, AWAITING USER.
- Artifact rows: initially one-line summaries that can expand, copy, or download.
- Chat windows: interactive chat, document upload at any time, inline artifact links, and blocking approvals/escalations.

Primary UI purpose:

- Necessary user interaction.
- State and progress visibility.
- Attention routing.
- Drilldown when needed.

### 15. Cloud Data Plane And Auth

Multi-user requires a cloud-capable data plane, but cloud polish should not precede a measurable local factory.

Minimum later scope:

- Auth mini-app.
- Local Docker mode with local accounts.
- Cloud mode with direct accounts and federated login.
- GitHub login.
- Google login.
- User-to-organization mapping.
- Database connection management.

Post-MVP:

- Roles.
- Team permissions.
- SaaS billing.
- Cloud worker pools.
- Hosted Work Groups.

v3 and Beyond:

- Orchestrator becomes a supervisor and dispatcher (Orchestration Plane).
- Agents can run in external environments like cloud-based agent runners.

### 16. Extract `maestro-agent`

The agent packages may eventually become a separate open-source package, similar to `maestro-llms` and `maestro-cms`.

Strategic reason:

- Agent development becomes a first-class Maestro task.
- New agents built by Maestro default to the Maestro agent ecosystem unless otherwise specified.
- The extracted package becomes reusable infrastructure and attracts development.

Requirements:

- Stable contracts.
- Standalone agent runtime.
- Public APIs.
- Example agents.
- Documentation.

Suggested timing:

- Do not extract too early.
- Stabilize artifacts, Work Groups, prompt packs, toolloop/policy interfaces, metrics, and agent pair contracts first.

### 17. The Workbench: The Interactive Second Tempo

Status: direction agreed at this level; deeper design deliberately deferred to a spike and an ADR.

The factory loop is asynchronous and gate-driven. Real development also includes rapid, interactive iteration: polish, debugging, small follow-ups, and finishing work on things the factory just built. v1 called this hotfix, which wrongly implied bug fixes only. If this work is declared out of scope, Maestro consistently delivers 90% and the last mile leaks to outside tools — losing evidence, provenance, and knowledge capture exactly where human taste is applied most.

Resolution: the Workbench is a tempo, not a separate system.

- **Same nouns.** Everything that changes a repo is still a Story inside an Epic, on the same branch and merge machinery, producing the same artifacts. A Workbench session may run inside a degenerate single-Story Epic created on the spot, or attach to an existing Epic branch — for example, polishing what the factory just produced.
- **Different review timing.** In the factory, review gates lead the merge and another agent is the reviewer. At the Workbench the human is present and is the accepting gate — but agents stay in the loop: a lightweight trailing reviewer still checks for syntactic, rule, and architectural drift, the classes of error agents catch better than a human moving fast. The generate/review invariant is satisfied by human accept plus the trailing agent check.
- **Trailing evidence.** The evidence package is assembled from the session automatically — diff, tests run, session summary — rather than staged through leading gates. The Workbench skips intake ceremony and leading gates; it never skips provenance.
- **MPH framing.** The Workbench is a harness preset: a different workflow graph, gate policy, and context strategy for the same Work Group machinery. This keeps one data model, one audit story, and lets golden stories benchmark the Workbench path too.
- **Entry point (decided).** The master dashboard — the intake surface — has a Workbench button that spins up a Work Group with only a target repo as its specification, implemented as the orchestrator dispatching a special-case blank Feature request. Sessions can also be opened from an existing Epic.
- **Two-way handoff.** A Workbench session that outgrows its scope gets promoted: the PM drafts a Feature/Epic and hands it to the factory. Factory output can spawn a Workbench follow-up session bound to the same Epic.

Open design questions (parked until the ADR):

- Composition: at the Workbench the human largely plays PM. Is the Work Group a full PM/Architect/Coder trio, or a Coder with an Architect reviewer on demand?
- What exactly does the trailing agent reviewer check, and when does it run?
- How much of the session transcript becomes evidence versus Audit-only data?
- Budgets and limits for open-ended interactive sessions.
- Within a Workbench session, can Story-to-Epic merges execute on the present human's approval plus a clean trailing drift check, without a separate Architect review record? (Epic-to-default always requires the human Accept — ADR 0020.)

## Proposed Sequencing

This is a discussion order, not a locked project plan.

### Execution Model

v2 will be built by multiple AI agents overseen by one human operator. That constraint shapes the plan more than any technology choice:

- Each phase must decompose into agent-executable specs with crisp, checkable exit criteria. Vague outputs stall an agent fleet faster than they stall a human team.
- The human operator's attention is the scarce resource of the build itself, not just of the product. Phases are sequenced so review load stays bounded: one architectural decision stream at a time, with mechanical work fanned out to agents.
- Dogfooding is a leverage strategy, not just a milestone. Early phases are built with off-the-shelf agent tooling; as soon as the Phase 1-4 core can run a Work Group against the Maestro repo itself, v2 development should progressively shift onto it. Phase 9 is the end state of a ramp, not a switch.

The interim process — roles (Claude authors, Codex reviews, DR accepts and resolves contention), branching, review cadence, and testing — is defined in [process_build.md](process_build.md).

Each phase below lists exit criteria. A phase is done when its exit criteria are demonstrably met, not when its code is merged.

### Phase 0: v2 Design Groundwork

Goal: decide the conceptual shape before code churn.

Outputs:

- v2 product thesis.
- ADRs for taxonomy and core architecture.
- Feature/Epic/Story/Work Group taxonomy.
- Intake/triage artifact contract — the Feature/Epic records, provenance, and orchestrator seam, with the executor (form, short-lived agent, provisional Work Group) deliberately unbound until the pre-Phase-5 spike.
- Reviewer vs Partner/Supervisor distinction.
- MPH definition.
- Minimal artifact model.
- Agent instance/signature model.
- Branch strategy decision.
- Postgres/sqlc/migrate decision.
- Multi-user scope boundaries.
- Breaking-change principles.
- Documentation reset plan.
- Reconciled, dependency-ordered ADR backlog: which ADRs block Phase 0 exit versus trail into later phases (supersedes the interim priority list in [notes_v1-adr-alignment.md](notes_v1-adr-alignment.md)).
- Spike: toolloop ownership — Maestro-owned vs `maestro-llms` (see D8).
- Spike: disposable project folder — how much state can leave the filesystem for the data plane (see D8).

Phase 0 should also archive older docs as needed and create a documentation set designed for ingestion into the v2 knowledge base. Repo docs should generally be LLM-facing; wiki/docs-site output can be human-facing.

Note: repo documents should carry Hugo-like TOML front-matter:

- Title.
- Edit date.
- Status (draft, live, archive).

Exit criteria:

- The taxonomy, artifact model (including artifact scope), branch strategy, data plane, and reviewer/partner ADRs exist and are Accepted.
- The Phase 1-blocking ADRs are Accepted before any Phase 1 implementation starts: the golden story schema / benchmark runner ADR, including the D9 sampling and budget mechanism (numeric values may be provisional pending the first instrumented runs) and the Phase 1 target strategy.
- The v2 MVP boundary (D1) and the port-vs-rewrite inventory (D8) are written down and agreed.
- Documentation reset is done: stale docs archived, remaining repo docs safe for agent ingestion.

### Phase 1: Golden Stories And Measurement Harness

Goal: build the measuring instrument before rewriting the machine.

**Resequencing PROPOSED 2026-07-22** (ADR 0025 conformance-first amendment; DR-directed, pending Codex + DR acceptance — this note and the Phase 1B section below flip on approval). Phase 1's instrumented runs falsified the assumption underneath "measure first": measurement presupposes function, and the target does not reliably function — 7 of 11 enumerated v1 patches were run-blocking, surfaced by only four stories. The near-term deliverable is therefore **e2e conformance**: a set of tools proving Maestro completes progressively harder stories, re-proven at every phase end. **Economic baselining moves to Phase 1B, after Phase 7** — before then v2 is largely infrastructure, so a baseline would price scaffolding. Nothing is cancelled; sequence and emphasis move. Cost and token data still accrue on every conformance run, so a trend exists well before Phase 1B.

Outputs:

- Golden story schema.
- Benchmark/golden story runner (black-box, self-contained persistence; see Runner Design Constraints).
- First 5-10 golden stories, single-repo and Story-scoped.
- Minimal prompt hash/pack identification.
- Metrics capture for LLM calls and tool calls, pushing reusable pieces to `maestro-llms` and `maestro-cms` where possible.
- ~~Run comparison reports with repeat-run spread.~~ → Phase 1B.
- A v1-derived baseline on the `golden-minimal` story subset, with its full target descriptor. **Stays in Phase 1 and cannot move**: v1's factory path is deleted during the rewrite, so this obligation expires rather than defers. (Phase 1B's v2-derived baseline is additional to it, not a substitute.)
- A single-agent achievability check: a scripted headless pass answering whether a candidate story is completable at all, so a red rung is never ambiguous between an incapable pipeline and an unreasonable story. A low-rung tool, retired at the decomposition rungs by construction.

Target strategy (decided 2026-07-11): the runner needs a real factory to drive, and the first true v2 Work Group path does not exist until Phase 3. The Phase 1 target is therefore the current codebase — v1's factory path, minimally patched so that a basic golden story can pass. This does not reopen v1 maintenance: after `v1-freeze`, the code on `main` is v2's raw material, not a supported v1 release. Patches are the bare minimum needed to make the measuring instrument usable, are never backported to the tag, and every run record captures the target commit hash, so "v1-as-patched" is an honest, labeled baseline. Full v1 defects that do not block golden-minimal stay unfixed.

Why first:

- Prevents unmeasured rewrite enthusiasm.
- Gives Maestro a way to test Maestro.
- Creates a shared language for model/prompt/harness changes.

Exit criteria:

- The runner executes at least 5 single-repo golden stories against a target Maestro build, black-box (the minimally patched v1 path per the target strategy).
- Every one of those stories clears the single-agent achievability check, and each has been run — red or green — against the current target on `paired-default`, with the run retained as an artifact.
- The D9 sampling and budget policy is written down and enforced by the runner. ✅ (item 6)
- The v1-derived baseline on `golden-minimal` is recorded with its full target descriptor (commit hash, binary identity, MPH identity). Binding in Phase 1 — it expires with v1 rather than deferring.
- ~~Repeat runs produce a comparison report showing cost, time, and pass/fail spread.~~ → Phase 1B.
- ~~Two different MPH configurations can be compared on the same story set.~~ → Phase 1B.


### Phase 2: Data Plane And Artifact Core

Goal: establish the v2 persistence model.

Outputs:

- Postgres local Docker setup.
- `sqlc` integration.
- `golang-migrate` integration.
- Core schema: orgs, users, products, repos, features, epics, stories, agent instances.
- Artifact tables.
- LLM/tool call tables.
- Metrics tables.
- Binary attachment strategy.
- Migration story from nothing, not necessarily from v1.

Exit criteria:

- Postgres, migrations, and typed queries build and run locally via Docker with one command from a clean checkout.
- Core schema migrations apply from empty, and artifact, agent-instance, and LLM/tool-call writes have typed queries with tests.
- One vertical slice writes real data: golden story runner results can be imported into the data plane and queried.

### Phase 3: Minimal Work Hierarchy And Work Group Runtime

Goal: create the smallest real v2 factory path.

Outputs:

- Feature intake model — contract-only: a minimal manual path honoring the intake artifact contract. It must not preempt the pre-Phase-5 spike; the final intake design (form, triage agent, provisional Work Groups) is decided there.
- Constraint: the Work Group runtime is tempo-neutral. Nothing in the lifecycle, gate wiring, or workspace model may assume leading gates only — the Workbench tempo (pillar 17, shipped in Phase 5) must remain expressible as a harness preset, not a parallel system.
- Epic model.
- Story model.
- Single Work Group lifecycle.
- Epic-scoped workspace.
- Epic dashboard skeleton.
- Epic-level plan workflow.

MVP constraint:

- Multiple Work Groups and a standing intake agent are not required.

Exit criteria:

- One Epic can go from intake through Story execution to merged Story branches, driven by a single Work Group, on a fixture repo.
- Every step emits artifacts to the data plane with correct provenance (agent instance, epic, story).
- The Epic dashboard shows live state for that Epic.

### Phase 4: Branch Hierarchy And Evidence Packages

Goal: make source control and proof match the work hierarchy.

Outputs:

- Epic branch creation.
- Story branch creation.
- Story-to-Epic merge.
- Epic-to-default merge.
- Rebase harness functions.
- Evidence package generation.
- Evidence viewer.
- Human Accept for Epic merge.
- Constraint: the branch and evidence contracts are tempo-neutral. Evidence packages must support trailing assembly from a session (the Workbench path, shipped in Phase 5) as well as staged assembly through leading gates — neither may be foreclosed.

Exit criteria:

- Story branches merge into an Epic branch; the Epic branch merges into default only after human Accept.
- Every merged Story and Epic has an evidence package viewable in the UI.
- At least one golden story exercises a rebase/conflict case handled by the harness.

### Pre-Phase-5 Spike: Intake And Triage Design

This is planned work, so it lives here rather than in the issue tracker (Issues hold deferred work discovered along the way; the roadmap holds planned work).

Goal: settle the intake/triage design (D2) with two phases of lived intake friction and real golden-story metrics in hand, before the Phase 5 agent-pair and gate ADRs harden anything around it.

Questions:

- Form-first intake: exact fields (mode, repo, size), the "I don't know" escalation, and the brief for the short-lived triage agent behind it.
- Provisional Work Groups: lifecycle, and continuity into execution in the single-repo case (intake to execution with zero handoff).
- Recipient pushback as review: protocol and bounds for a Work Group challenging the Epic it was handed.
- Cross-Epic coherence: per-Epic recipients cannot see cross-Epic decomposition errors (bad splits, missed inter-Epic dependencies, duplicated work); decide where those get caught — deterministic data-plane checks, the human, or an agent.
- Conflict with in-flight work: deterministic pre-checks over in-flight Epics by repo and scope.
- Graduation criteria: what measured triage failure rate justifies a standing intake agent.

Exit criteria:

- The Intake/Triage ADR is Accepted.
- External design review requested and incorporated, or DR accepts a timeboxed fallback — the spike must not block on availability outside the build process. (DR's designer contact has offered input at spike time; routed through DR per the build process.)

### Pre-Phase-5 Spike: Workbench Design

Companion to the intake spike above, deliberately separate from it (added 2026-07-15, Phase 0 item 12 review): the Workbench is a critical v2 commitment (pillar 17, D10) and gets its own spike and Accepted ADR before Phase 5 hardens gate and review machinery around it.

Goal: settle the Workbench design with the Phase 3 runtime and Phase 4 branch/evidence contracts as lived constraints, so Phase 5 ships it rather than discovering it.

Questions (carried from pillar 17):

- Work Group composition for sessions: full PM/Architect/Coder trio, or Coder plus on-demand Architect with the human playing PM.
- The trailing drift reviewer: what it checks (syntax, rules, architectural drift) and when it runs.
- Transcript-to-evidence boundary: what of the session becomes evidence versus Audit-only data.
- Budgets and limits for open-ended sessions.
- Whether Story-to-Epic merges can execute on the present human's approval plus a clean trailing drift check, without a separate Architect review record (Epic-to-default always requires the human Accept — ADR 0020).
- Promotion path when a session outgrows its scope.

Exit criteria:

- The Workbench ADR is Accepted, satisfying ADR 0020's invariant through human accept plus the trailing agent check — no review exemptions.

### Phase 5: Agent Pairs, Internal Reviewers, And Gates

Goal: make review an invariant of Management artifact creation — and ship the Workbench as the first full consumer of the alternate review/evidence timing.

Outputs:

- Artifact generation/review contract.
- Internal adversarial reviewer interface.
- Distinct reviewer model routing.
- Budget review moved to internal reviewer where appropriate.
- Optional gates: requirements, stories, UAT.
- Gate UI.
- Human escalation path.
- **The Workbench tempo, end-to-end** (pillar 17, D10, per the pre-Phase-5 Workbench ADR): dashboard-button entry dispatching the blank Feature request, sessions on degenerate or existing Epics, human accept plus trailing drift review, trailing evidence assembly.

Exit criteria:

- No Management artifact can reach a persisted, accepted state without a reviewer record. (The Workbench satisfies this through human accept plus the trailing agent check — ADR 0020 admits no configured exemptions.)
- A reviewer/author disagreement escalates to a human after the configured bound, demonstrated end-to-end.
- The three optional gates can be toggled per Epic by config and are visible in the UI.
- A Workbench session runs end-to-end: entered from the dashboard button, producing Story work on a real Epic branch with trailing evidence and drift review, closed by human Accept.

### Phase 6: Knowledge, Skills, And Post-Merge Hooks

Goal: move from chat/history memory to governed knowledge.

Outputs:

- Knowledge item schema.
- ADR/doc ingestion.
- Interface/contract ingestion.
- README/doc indexing.
- Skills/pattern registry.
- Skill eval/promotion metadata.
- Post-merge documentation/knowledge update hook.
- Contradiction detector prototype.

AST ingestion can be deferred if needed; it may require sidecar/runtime choices that are larger than the first knowledge MVP.

Exit criteria:

- Knowledge items with citations can be ingested from ADRs, docs, and interfaces, and queried by a Work Group.
- A knowledge pack is assembled for a real Epic and its code-level citations are verified by a reviewer agent.
- A post-merge hook updates docs/knowledge, demonstrated on at least the golden story fixtures.

### Phase 7: Multi-User Dashboard And Cloud Data Plane

Goal: make Maestro usable by teams.

Outputs:

- Auth mini-app.
- Local account mode.
- GitHub/Google login.
- Organization membership.
- Cloud Postgres support.
- Multi-project dashboard.
- User attribution in artifact/epic views.

Exit criteria:

- Two users in one organization can operate distinct Epics against a shared data plane.
- Auth works in local account mode and at least one federated mode.

### Phase 1B: Benchmark Economics

Goal: price the machine, once there is a machine worth pricing. Anchored **after Phase 7** — earlier baselines measure infrastructure, not the system.

Outputs:

- Per-metric-class comparison reporting with repeat-run spread.
- The single-agent happy-path baseline as the economic comparator (the vibe-coding premium made measurable).
- The cost-to-accepted-change baseline across configurations.

Inputs are already accruing: conformance runs retain cost, token, and call records from Phase 1 onward, so Phase 1B analyzes a trend rather than starting cold.

Exit criteria:

- Repeat runs produce a comparison report showing cost, time, and pass/fail spread (never bare points), aggregated only within one complete identity group and enforcement mode.
- Two different MPH configurations are compared on the same story set — the paired-agent default and the single-agent baseline.
- A target-derived baseline on `golden-minimal` is recorded with its target descriptor (commit hash, MPH identity), taken against v2.
- Cost to accepted change is reported per configuration, including its undefined case when no attempt passes.

**These roadmap criteria are the authority for Phase 1B until its own phase plan exists.** The Phase 1B section of the [Phase 1 plan](phase_1/plan_scope.md) mirrors them for continuity, but that document flips to `archive` when Phase 1 closes and an archived document carries no authority ([ADR 0017](../adr/0017-v2-documentation-authority-and-lifecycle.md)) — so it cannot own this checklist. When Phase 1B opens it gets a live `plan_scope` of its own, as every phase does, and that becomes controlling in the usual way.

### Phase 8: Extract `maestro-agent`

Goal: turn stable v2 agent runtime pieces into reusable infrastructure.

Outputs:

- Package boundary.
- Standalone runtime.
- Public APIs.
- Internal migration.
- Example agents.
- Documentation.

Exit criteria:

- `maestro-agent` builds standalone with public API docs and at least one example agent living outside the Maestro repo.
- Maestro consumes the extracted package with no imports of its internals.

### Phase 9: Maestro Builds Maestro

Goal: use Maestro v2 as its own primary development factory.

Targets:

- 80% of normal Maestro development can go through Maestro.
- 20% remains hands-on human agentic development.
- Golden stories include Maestro repo tasks.
- Prompt/harness improvements are evaluated against Maestro tasks.

Exit criteria:

- Measured over a trailing month, at least 80% of merged Maestro changes went through Maestro Work Groups.
- Golden stories include Maestro-repo tasks and gate Maestro releases.

## Candidate First 90 Days

If the goal is momentum without locking the whole design too early:

1. Write the v2 architecture memo.
2. Lock in initial ADRs and taxonomy.
3. Define golden story schema.
4. Build minimal golden story runner.
5. Capture reliable token/model/tool metrics.
6. Define minimal Management/Audit artifact schema.
7. Define `agent_instance` schema.
8. Prototype Postgres/sqlc/migrate in a small vertical slice.
9. Create rough artifact timeline UI.
10. Model Feature/Epic/Story in the database.
11. Run first comparison on 3 golden stories.

Prompt pack selection can be deferred if needed; a prompt hash and manual pack label are enough for the first benchmark runner.

This creates the loop:

> Change model/prompt/harness -> run golden stories -> inspect artifacts/metrics -> decide.

## Decisions To Make Soon

### D1. What Is The v2 MVP?

Agreed answer:

The v2 MVP is local/team-capable architecture, not full cloud multi-user.

MVP includes:

- Golden story / benchmark runner.
- Minimal Management/Audit artifact skeleton.
- Agent instance table.
- LLM/tool metrics.
- Postgres/sqlc/migrate vertical slice.
- Feature/Epic/Story taxonomy.
- Single Work Group execution path.
- Evidence package basics.
- Epic branch/story branch strategy.

MVP does not require:

- A standing intake agent (the retired CPA/CTA concept).
- Multiple concurrent Work Groups.
- Cloud auth.
- AST ingestion.
- Full skill registry.
- `maestro-agent` extraction.

### D2. Is There A Standing Intake Agent? (Originally: Is CPA An Agent?)

Revised answer (2026-07-12, after external design review; converges on the original Codex position — contract and shape, no standing agent — with stronger reasons):

No. Intake is a triage role, not a senior role, and there is no standing agent pair. What must happen at intake is small — one Epic or several; per Epic, mode, repo, and dependencies — and in the common single-repo case the human already knows all three answers.

The shape:

- The Orchestrator owns intake. The primary surface is a form (mode, repo, size) with an "I don't know" button that spins up a short-lived triage agent for exactly the question the operator cannot answer.
- Bypass the agent, never the artifact: even the ten-second expert path emits Feature/Epic records with provenance (author = human, via intake form). The receiving Work Group's PM reviews human-authored intake artifacts, keeping the review invariant intact (pillar 7).
- Conversational intake (greenfield asks) runs through a provisional Work Group: its PM, scoped to the Feature, holds the conversation. If the Feature dies at intake the group evaporates; in the single-repo case the provisional group becomes the executing group — intake to execution with zero handoff.
- Scope pushback is proxied: the Work Group receiving an Epic can push back on it (recipient review). CTA dissolves entirely — per-Epic review is recipient pushback; cross-Epic coherence is a spike question, likely deterministic data-plane checks plus the human.
- Feature memory lives in artifacts in the data plane, not in any agent's conversation context. The always-on-agent framing was a v1-shaped idea.
- The doc-only privilege boundary survives every variant: whatever performs intake reads documentation and the knowledge system, never code — the forcing function on knowledge quality transfers intact.
- Graduation path: contract and shape now; a standing intake agent later only if measured triage failure rates justify it. Golden stories arbitrate.

Phase 0 ADRs must specify the intake artifact contracts and the orchestrator seam while leaving the executor unbound. Final design is settled by the pre-Phase-5 intake spike.

### D3. Is There An Epic-Level Plan?

Agreed answer, with the requirements/plan boundary made explicit:

Yes. It should be in the workflow.

The executable plan is the Story list plus dependency graph. The Epic plan is the contract:

- Repo scope.
- Acceptance evidence.
- Integration strategy.
- Risk.
- Gates.
- Branch policy.
- Story rationale.

To keep the Epic plan from duplicating the Requirements artifact, the split is:

- Requirements (PM-owned, shaped with the user): the what and the why — intent, scope, non-goals, constraints, acceptance criteria, examples, open questions.
- Epic plan (Architect-owned): the how and the proof — repo scope, integration strategy, branch policy, story decomposition and rationale, evidence plan, risks, gates.

The Epic plan cites requirements; it never restates them. If intent changes, the Requirements artifact changes first and the plan is re-derived. The artifact schemas should enforce this split, not convention.

### D4. Should Epic-Level Merge Be Automatic?

Agreed answer:

No — unconditionally (revised 2026-07-13). Human Accept is required for every Epic-to-default merge. The earlier low-risk auto-merge idea is withdrawn: acceptance is outcome validation — does the work solve the need — which has nothing to do with risk, and no risk assessment can stand in for it (ADR 0020). Accepting a trivial Epic costs one glance at evidence, because acceptance is not code review.

This is the right human-level gate. Note that it creates deliberate back-pressure in large automated Features: the dependency graph cannot unblock downstream Epics until upstream Epics merge. The dashboard should make that queue visible so the operator can see when they are the bottleneck.

### D5. What Lives In The Repo vs Database?

Agreed split:

Repo:

- Source code.
- Project docs and ADRs that are part of the project.
- Generated docs that are project artifacts.
- Repo-local skills/prompt packs only when tightly bound to the project.

Database/object storage:

- Users/orgs.
- Products/repos.
- Features/epics/stories.
- Runs.
- Artifacts and relationships.
- LLM/tool calls.
- Metrics.
- Binary attachments or object-store references.
- Indexed knowledge and retrieval metadata.
- Installed org-level skills and prompt packs.
- Audit events.

Both:

- Knowledge derived from repo docs/code.
- Skills/prompt packs may have repo canonical source and DB index/metadata in some workflows.

### D6. What Is The First Eval Metric Beyond Tests?

Agreed answer:

Cost to accepted change, paired with review cycles and failure kind. This is concrete, hard to game, and directly tied to factory performance.

Beyond tuning the factory, the same metric set makes Maestro a scientific instrument. Golden stories plus third-party benchmarks should be able to answer questions like whether SLMs are feasible for certain kinds of development, or whether a new LLM version is genuinely superior once latency and total cost of ownership are counted.

### D7. How Aggressive Should The v1 Break Be?

Agreed answer:

Aggressive. Treat v1 as a conceptual demo. v1 is frozen with no users to support, which simplifies everything.

Decided (2026-07-11): **v1 is deprecated as of now.** There is no coming back from v2 — it is not only meant to be more efficient, it solves problems v1 simply does not (team work, artifact provenance, measurement).

Path:

- Tag the current `main` head as `v1-freeze`. Any hypothetical future v1 work forks from that tag.
- No pre-freeze bug fixes. Known v1 defects (the watchdog requeue race, the benchmark spec-review bug) die with v1 — spending time or tokens fixing code v2 discards is waste.
- Post-freeze, the code on `main` is v2 raw material, not a supported v1 release. Phase 1 may patch it minimally to serve as the benchmark target (see the Phase 1 target strategy); that is v2 instrument work, not v1 maintenance, and is never backported to the tag.
- Keep a prebuilt v1 binary or image at the tag for reference.
- Develop v2 directly on `main` through normal PRs, not on a long-lived side branch. A long-lived branch only pays for itself when the old version needs parallel maintenance; with v1 frozen, it buys nothing and costs divergence pain, and incremental PRs to `main` are also what the agent-fleet build model needs.
- Aggressively prune.
- Port selectively per the D8 inventory.
- Keep useful tests/history/context.
- No backports, no dual maintenance.

Fresh repo creates too much porting tax.

### D8. Which v1 Subsystems Port And Which Are Rewritten?

This decision dominates the Phase 3 schedule and should be made explicitly in Phase 0, not discovered mid-rewrite.

Candidate first pass, aligned with [notes_v1-adr-alignment.md](notes_v1-adr-alignment.md):

- Port largely as-is: `maestro-llms` boundary, toolloop/ProcessEffect discipline, container/workspace isolation, clone/mirror/forge workflow, FSM engine, typed dispatcher protocol.
- Port with rework: PM/Architect/Coder state machines (re-scoped to Work Groups), failure taxonomy and durable asks, chat.
- Rewrite: persistence (SQLite session log becomes the Postgres artifact schema), knowledge (DOT artifact becomes data-plane knowledge), WebUI (log view becomes artifact view), config/secrets handling (to database where possible).
- Drop: maintenance mode as-is, spec/story intake flows superseded by Feature/Epic/Story intake.
- Revisit: bootstrap mode, in light of Maestro-proprietary knowledge and prompt fragments moving out of the repo — including whether `.maestro/` needs to exist at all.

The Phase 0 output should be this table at package grain over the actual v1 package list.

Two Phase 0 spikes fall out of this inventory:

- Toolloop ownership: is maintaining a Maestro-owned toolloop distinct from the `maestro-llms` toolloop still justified?
- Disposable project folder: how much non-disposable state can move off the user's filesystem into the data plane, or into the repo where it is a true project artifact?

### D9. How Are Benchmark Nondeterminism And Cost Handled?

Golden story runs are stochastic and cost real money. Before Phase 1 results are used for decisions, define:

- Runs per story per configuration (candidate: 3 for standard comparisons, 1 for smoke checks).
- Which metrics are reported with spread rather than point values.
- A per-run and per-suite budget cap with an explicit overrun policy.
- Which comparisons justify full-matrix runs versus spot checks.

Agreed in principle. First action: instrument a handful of representative runs to establish real per-story costs before fixing the sampling policy.

### D10. Is The Fast Loop A Mode Or A Parallel System?

Candidate answer:

A mode. The Workbench (pillar 17) reuses the Epic/Story data model, branches, artifacts, and evidence machinery under a different harness preset: human accept plus trailing agent drift checks instead of leading gates, trailing evidence instead of staged evidence. The two failure directions it avoids: a parallel PM/Architect/Coder system beside the factory would drift into a second product with weaker guarantees, and a degenerate Epic forced through full factory ceremony would be too slow to use. One data model, two tempos.

The entry point is decided: a Workbench button on the master dashboard, implemented as the orchestrator dispatching a special-case blank Feature request scoped to a target repo.

## Risks

### Risk: Building The Cloud Before The Factory Works

Multi-user matters, but cloud auth/dashboard work can consume enormous attention. The local factory should be measurable and artifact-driven before SaaS polish.

### Risk: Intake Becomes A Mega-Agent

Largely defused by the triage model (D2): intake is a form plus an optional short-lived agent, and substantial conversation runs through provisional Work Groups. The risk returns if a standing intake agent is ever added — its scope must stay bounded to producing Feature artifacts, resolving escalations about those artifacts, and pointing users at Epic-local escalations. It must never become an all-knowing prompt that hides workflow logic.

### Risk: Database Becomes The New Junk Drawer

Moving knowledge to Postgres improves queryability, but it can recreate "one giant memory." Use scopes, provenance, staleness, and retrieval rules.

### Risk: Internal Reviewers Become Scope Expanders

Internal adversarial reviewers must stay narrow. Their job is correctness, completeness, scope, and budget. They do not add clever ideas.

### Risk: Golden Stories Are Too Synthetic

Golden stories need realistic brownfield friction: existing conventions, flaky tests, migrations, merge conflicts, ambiguous requirements, and UI validation.

### Risk: Benchmark Cost And Noise

The benchmark matrix multiplies fast: stories x models x prompt packs x harness configs x repeat runs. Without sampling discipline and budget caps, Phase 1 either burns the token budget or results get quietly downgraded to single noisy runs that support any conclusion. D9 must be settled before the runner is used for real decisions.

### Risk: One Human Operator Is The Build Bottleneck

v2 is built by an agent fleet under a single human. If phases produce large, entangled review surfaces, the operator becomes the queue and the fleet idles. The mitigation is structural: small independently reviewable specs, exit criteria agents can check themselves, and early dogfooding so Maestro's own review machinery absorbs load as soon as it exists.

### Risk: Artifact Volume Overwhelms Humans

Artifact-first does not mean humans read everything. The UI must summarize, prioritize, and route attention.

### Risk: Reasoning Capture Becomes Context Poison

Preserve model commentary and provider-supported reasoning summaries as Audit data, but do not automatically reinject raw reasoning into future context. Compact, summarize, cite, and select deliberately.

## Working Thesis

Maestro v2 should not be "v1 but with more agents."

It should be:

> A measurable, artifact-first agentic factory where epic-scoped work groups create and review production-grade changes under explicit Model/Prompt/Harness control.

The research says the winners will not have the cleverest prompt. They will have the best operating system for agentic work. Maestro v2 can be that operating system by first making the factory observable, then making it multi-user and extensible.

