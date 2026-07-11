# Maestro v2 ADR Backlog

Status: rough companion note

These are concepts that likely need ADRs before implementation. They are listed here to keep the main roadmap readable.

## ADR Candidates

### v2 Documentation Authority And Planning Reset

Decide how v2 docs are organized, what is archived, what is LLM-facing, and what is human-facing.

Likely decisions:

- `docs/v2` starts as planning material.
- Accepted v2 architecture decisions move into ADRs.
- Repo docs are optimized for agent ingestion.
- Wiki/docs-site output is optimized for humans.

### v2 Taxonomy: Product, Feature, Epic, Story, Work Group

Define the v2 work hierarchy and ownership boundaries.

Naming decided (2026-07-11): the repo-scoped unit is `Epic` (originally `Task`, which collided with the v1 TASK message type, generic agent-tooling "task" language, and the industry prior that Tasks are smaller than Stories). The executing unit is a `Work Group` (originally `Task Team`). The v1 hotfix path becomes the `Workbench` (interim name "Live Mode" rejected: implies a live product). CPA/CTA retained (decided 2026-07-11): the Chief X Agent pattern deliberately mirrors corporate CPO/CTO titles; expand the acronym on first use per document to defuse the "certified public accountant" reading.

Key questions:

- Is Product a first-class data model?
- Does Feature span repos?
- Does an Epic always scope to one repo?
- Does Story map roughly to one PR?
- What owns a Work Group lifecycle?
- Can the hierarchy collapse for small work? A bug fix or tweak should be enterable as a single-Story Epic without Feature-level ceremony, the way industry tools allow a Story without an Epic. The Workbench overlaps here but is distinct: that is about interactivity and review timing, this is about skipping intake layers.

### CPA/CTA Scope

Define CPA and CTA as Feature-level roles.

Recommended scope:

- Produce and review Feature artifacts.
- Resolve escalations relating to Feature artifacts.
- Prompt users to inspect Epics when Epic-local escalation is needed.
- Avoid becoming an all-knowing orchestrator prompt.

### Reviewer vs Partner/Supervisor

Define the two review scopes.

Reviewer:

- Correctness.
- Completeness.
- Scope adherence.
- Budget/nonconvergence.

Partner/Supervisor:

- Adds judgment.
- Applies project standards.
- Resolves ambiguity.
- Applies domain/compliance/security skills.

### Management And Audit Artifacts

Define artifact categories, lifecycle, review requirements, and UI treatment.

Key decision:

- Management artifacts are human-facing.
- Audit artifacts are durable/queryable backing records.

### Artifact Schema And Templates

Define canonical artifact encoding.

Recommended:

- JSON as storage/API canonical format.
- Schema/version in every artifact.
- Markdown as rendering format.
- TOML/YAML allowed for prompt-facing fragments where useful.

### Agent Instance And Lightweight Signatures

Define agent instance records and artifact provenance signatures.

Avoid cryptographic signing initially unless required.

### Golden Stories And Benchmark Runner

Define golden story schema, runner semantics, cleanup, fixture repos, and comparison reports. Phase 0 exit-blocking: Phase 1 builds directly on this ADR.

Target strategy decided (2026-07-11): the Phase 1 target is the current codebase's v1 factory path, minimally patched so a basic golden story passes; run records capture the target commit hash (see roadmap Phase 1).

Key questions:

- How deterministic does a golden story need to be?
- How many runs per story per configuration, and how is spread reported? (Roadmap D9.)
- What budget caps apply to benchmark runs, and what is the overrun policy?
- What is the runner's black-box contract: which external surfaces does it drive, and where does it store its own results before the v2 data plane exists?
- How are scored rubrics represented?
- How are branches cleaned?
- Which repos become fixtures?
- Should golden story runs be exposed through build tags analogous to `integration` — e.g. `golden-minimal` for a smoke subset and `golden-all` for the full suite (see build-process.md)?

### v1 Freeze And Port-Vs-Rewrite Inventory

Record the v1 freeze and the package-level port/rework/rewrite/drop inventory (roadmap D8).

Freeze decided (2026-07-11): v1 is deprecated; tag `v1-freeze` at the pre-v2 `main` head; no pre-freeze fixes or backports; hypothetical future v1 work forks from the tag; v2 develops on `main`.

Key questions:

- Which v1 packages port as-is, which need rework, which are rewritten, and which are dropped?

### Postgres Data Plane

Define Postgres as v2 data plane, Docker-local default, cloud mode, `sqlc`, and migrations.

Key question:

- What minimal schema lands first?

### Prompt Packs And Skills Storage

Define whether prompt packs and skills are database-canonical, repo-canonical, or hybrid.

Recommended:

- Installed org-level packs/skills are DB-canonical.
- They are immutable, hash-addressed, versioned, and exportable.
- Repo-local packs/skills remain possible.

### Knowledge Hierarchy And Knowledge Packs

Define knowledge source precedence, citation rules, staleness, and pack generation.

Key sources:

- ADRs.
- Interfaces/contracts.
- Docs.
- Skills.
- AST/code facts.

### Branch Strategy

Define Epic and Story branch behavior.

Recommended:

- Story branches merge to Epic branch.
- Epic branch merges to default after acceptance.
- Rebase/conflict resolution is a harness function.

### Workbench And The Interactive Loop

Define the fast/interactive second tempo (roadmap pillar 17 and D10): same Epic/Story data model, human as accepting gate plus trailing agent drift review, trailing evidence.

Entry point already decided: a Workbench button on the master dashboard, implemented as the CPA dispatching a special-case blank Feature request scoped to a target repo; sessions can also open from an existing Epic.

Key questions:

- Work Group composition for Workbench sessions (full PM/Architect/Coder trio, or Coder plus on-demand Architect, with the human playing PM)?
- What exactly does the trailing agent reviewer check (syntax, rules, architectural drift), and when does it run?
- What of the session transcript becomes evidence versus Audit-only data?
- Budgets/limits for open-ended sessions.
- Can Workbench merges to the Epic branch auto-accept given human presence and a clean drift check?
- Promotion path when a session outgrows its scope.

### UAT And Demo Mode

Define how Demo Mode becomes or supports UAT.

Key question:

- Is UAT optional in MVP or required for Epic merge?

### Binary Attachment Storage

Define storage for uploaded images, spreadsheets, PDFs, docs, and diagrams.

Recommended:

- Data plane/object storage by default.
- Content-addressed digest.
- Repo only for true project artifacts.

### User Credentials And Configs

Decide which configs/secrets move from JSON files to database.

Potential principle:

- Project folders should become disposable.
- All durable control-plane state lives in local/cloud data plane.

### Container Runtime Abstraction

Define a future container/execution interface while keeping Docker as the only initial implementation.

Useful for future Apple/iPhone/raw-filesystem use cases.

### Tool And Action Policy Gating

Define where per-action policy checks on tool calls and high-risk actions live: toolloop, dispatcher, tool execution layer, or a separate policy service.

The research corpus (Day 4/Day 5) pushes structural gates (role/env/tool allowlists, filesystem scopes), semantic gates (high-risk action summaries checked against policy), and human gates. The v2 MVP has workflow gates only; per-action policy is probably post-MVP, but the seam should be chosen early so it is not retrofitted into every tool.

### External Agent Runtime Contract

Define whether Maestro can run Claude Code, OpenHands, or other headless agents inside containers.

Probably post-MVP.

### Dispatcher/Message Abstraction For Cloud Jobs

Define whether agent communication should anticipate cloud job execution.

Likely v3. Avoid overbuilding early.

