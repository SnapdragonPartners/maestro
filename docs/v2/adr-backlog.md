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

### v2 Taxonomy: Product, Feature, Task, Story, Team

Define the v2 work hierarchy and ownership boundaries.

Key questions:

- Is Product a first-class data model?
- Does Feature span repos?
- Does Task always scope to one repo?
- Does Story map roughly to one PR?
- What owns a Task Team lifecycle?

### CPA/CTA Scope

Define CPA and CTA as Feature-level roles.

Recommended scope:

- Produce and review Feature artifacts.
- Resolve escalations relating to Feature artifacts.
- Prompt users to inspect Tasks when Task-local escalation is needed.
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

Define golden story schema, runner semantics, cleanup, fixture repos, and comparison reports.

Key questions:

- How deterministic does a golden story need to be?
- How are scored rubrics represented?
- How are branches cleaned?
- Which repos become fixtures?

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

Define Task and Story branch behavior.

Recommended:

- Story branches merge to Task branch.
- Task branch merges to default after acceptance.
- Rebase/conflict resolution is a harness function.

### UAT And Demo Mode

Define how Demo Mode becomes or supports UAT.

Key question:

- Is UAT optional in MVP or required for Task merge?

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

### External Agent Runtime Contract

Define whether Maestro can run Claude Code, OpenHands, or other headless agents inside containers.

Probably post-MVP.

### Dispatcher/Message Abstraction For Cloud Jobs

Define whether agent communication should anticipate cloud job execution.

Likely v3. Avoid overbuilding early.

