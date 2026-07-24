+++
title = "Maestro v2 Parking Lot"
edit_date = "2026-07-24"
status = "live"
type = "notes"
summary = "Design ideas parked for later consideration — not planned work; an idea graduates to the roadmap or an ADR when picked up."
+++

# Maestro v2 Parking Lot

Status: rough companion note

These ideas are worth preserving but are either too granular, too speculative, or post-MVP for the main roadmap.

## Implementation-Focused Ideas

### Preserve Commentary And Reasoning Summaries

Maestro v1 aggressively required tool calls and discarded non-tool output. That loses useful rationale and commentary.

Direction:

- Continue requiring at least one tool call in tool-driven loops.
- Preserve model-visible commentary and provider-supported reasoning summaries as Audit data.
- Compact/summarize before reuse.
- Do not automatically inject raw reasoning into future context.

### Review Toolloop Ownership

Review whether Maestro should maintain its own toolloop independent of `maestro-llms` or build on the simplified `maestro-llms` toolloop.

Update (2026-07-11): promoted to a Phase 0 spike; see roadmap D8. The refactor itself still waits until v2 contracts are clearer, but the ownership question gets answered up front.

### Extract More Packages

Identify other reusable packages worth extracting beyond `maestro-llms`, `maestro-cms`, and possible `maestro-agent`.

Criteria:

- Stable boundary.
- Useful outside Maestro.
- Low coupling to v2-specific workflows.

### Maintenance Mode Revisit

Current maintenance mode runs automatically after a number of Stories.

Possible v2 direction:

- Recast maintenance as a distinct Epic when relevant.
- Decide which maintenance duties stay internal to Maestro.
- Move deterministic docs/knowledge updates to post-merge hooks where possible.

### Tool-Level Policy Gates

Workflow gates (roadmap pillar 8) decide when work advances. This is different: per-action enforcement on tool calls and high-risk operations.

From the research synthesis:

- Structural gates: role/env/tool allowlists, branch protections, filesystem scopes.
- Semantic gates: high-risk action summaries checked against policy.
- Human gates: risk-tiered approval with clear summaries.

Post-MVP as implementation, but the toolloop/dispatcher contracts should leave a seam for it (see ADR backlog).

### Context Ledger

Record per LLM call: what context was injected, why it was selected, which scope it came from, whether it was fresh or stale, and what was deliberately excluded. Supports debugging, cost control, and trust.

Natural extension of Audit artifacts once the artifact schema exists; also feeds the knowledge-pack "as little as possible" principle with real data.

### Config And Credentials In Data Plane

Move user credentials and configs from JSON files to the database where appropriate.

Potential principle:

- Project folder should be disposable.
- Only data-plane connection bootstrap remains local.

Update (2026-07-11): a Phase 0 spike will scope this; see roadmap D8.

### Live Benchmark Result Writes

Have the golden runner write each completed attempt into the data plane as it finishes, rather than relying on a post-run import.

Mechanism that preserves every existing constraint: the runner invokes Maestro's importer as a **subprocess** — an external surface, exactly how it already invokes targets — so `benchmark/` gains no Postgres driver, no duplicated schema, and no cross-module import of `internal/dataplane`, and plane access still routes through the Orchestrator's persistence seam (ADR 0022).

Parked 2026-07-24 (Phase 2 plan, reviewer question 5): Codex resolved Phase 2 to import-destination and declined this variant for now. Picking it up requires an **ADR 0025 amendment** — it promotes the plane from import destination to results sink, against that ADR's "zero dependency on the Phase 2 data plane… Phase 2's vertical slice does that import". Wanted only if post-run import proves to lose records or lag badly enough to matter.

## UI Ideas

### Intake/Master Dashboard

Shows:

- Intake form and chat.
- Items in flight.
- Workflow state for each item.
- Status: READY, PROCESSING, AWAITING USER.

Clicking an Epic opens Epic Dashboard.

### Epic Dashboard

Similar to v1, but artifact-first.

Possible states:

- INTAKE.
- REQUIREMENTS DEVELOPMENT.
- REVIEW GATE.
- STORY DEVELOPMENT.
- REVIEW GATE.
- BUILDING.
- VERIFYING.
- UAT GATE.
- COMPLETE.
- AWAITING USER.

### Artifact Rows

Each artifact starts as a one-line summary with expand, copy, and download actions.

### Chat Windows

All chat windows should support:

- Interactive chat.
- Upload at any time.
- Inline artifact links.
- Blocking approval/escalation states.

## Knowledge And Documentation Ideas

### Self Knowledge

Agents need knowledge about Maestro itself when building or debugging Maestro.

Direction:

- Seed a Knowledge Tool with Maestro self-knowledge.
- Add a `knowledge_search` MCP with special-case self-knowledge support.
- The initial self-knowledge seed may be docs-only to avoid token clutter; broader knowledge ingestion (ADRs, interfaces, AST) is governed by the Phase 6 knowledge ADR, not by this note.

### Design For AI Library

Start a library of best practices analogous to "design for manufacturing," but for AI/agentic development.

Storage:

- General knowledge.
- Lower precedence than product/repo-specific knowledge.
- Citation/provenance included.

### Standards And LLM-Optimized Formats

Investigate best practices for:

- Interoperability.
- Token economy.
- Model comprehension.

Candidate formats:

- JSON for canonical API/storage.
- TOML/YAML for prompt-facing config/fragments.
- ADR standards.
- Requirements/story standards.
- A2A/A2UI where appropriate.

## Runtime And Infrastructure Ideas

### Container Runtime Abstraction

Docker remains initial implementation, but define an interface that could later support:

- Raw filesystem execution.
- macOS/Apple app development.
- iPhone development.
- Other sandbox providers.

### Headless Development Agent Interface

Generalize Claude Code-mode agents.

Goals:

- Capture token usage where possible.
- Run other headless development agents, such as OpenHands, inside Maestro containers.
- Define interface/contract now; implement later.

### Cloud Job Agent Execution

Consider whether agent communication should eventually support agents running as cloud jobs.

This is probably v3. At most, define dispatcher/message seams so a queue-backed system can be added later.

### Read-Only Access To Other Repos

Support Epics that need read-only access to shared tools, design systems, reference repos, or interface contracts in other repositories.

Needs:

- Repo access model.
- Knowledge scope model.
- Citation model.
- Container mount/access policy.

## Product And Multi-Repo Ideas

### Product-Level Knowledge

Product can include one or more repos.

Uses:

- Explain how repos fit together.
- Frontend/backend/microservice context.
- Multi-repo Demo/UAT.
- Product-level knowledge claims.

Product-level claims may not be directly verifiable against one repo and should be labeled accordingly.

### Companion GitHub App For Ingestion

Open question:

- Should content ingestion be managed by Maestro like database migrations, using commit hashes and locks?
- Or should a companion GitHub app update knowledge on repo events?

Likely not MVP. Maestro likely needs leases/locks anyway for multi-user local/cloud operation.

## Farther-Out Ideas

### Full SaaS Roles And RBAC

Post-MVP.

Potential roles:

- Human PM interacts with intake and PM agents.
- Engineer works on Epics entered by PM.
- Admin manages org settings and credentials.

### Hosted Work Groups

Post-MVP or v3.

Would require:

- Cloud execution.
- Queue-backed dispatcher.
- Stronger authz.
- Cloud logs/artifacts.
- Cost controls.

### Cryptographic Artifact Signing

Not needed initially. Lightweight signatures and hashes are enough unless compliance or external audit demands stronger guarantees.

