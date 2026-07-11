# Maestro v2 Provenance Matrix

Status: rough companion note

This matrix tracks where major roadmap ideas came from. It is intentionally coarse. Many ideas have multiple sources.

Source categories:

- **Maestro v1:** already present or implied in current Maestro architecture.
- **DR notes:** explicit user/client-experience feedback in the marked-up roadmap.
- **Research corpus:** Google/McKinsey papers and community notes reviewed under `/Users/dratner/Code/temp/Research`.
- **Codex synthesis:** recommendations or pushback introduced during roadmap synthesis.
- **Claude review:** feedback and revisions from the 2026-07-11 Claude review pass.

| Idea | Primary Sources | Notes |
|---|---|---|
| Agent factory framing | Maestro v1, research corpus, DR notes | Maestro already used PM/Architect/Coder and PR workflows; research adds shared "factory" vocabulary. |
| Graph-shaped workflows over chat | Maestro v1, research corpus | Current FSMs strongly align with research emphasis on workflow graphs. |
| Feature/Epic/Story taxonomy | DR notes, Codex synthesis | Refines v1 Spec/Story model into multi-repo and epic-scoped hierarchy. |
| CPA role | DR notes | Codex pushback: CPA should be role/interface first, not a mega-agent. |
| CTA role | DR notes | Added as technical pair for CPA, analogous to Architect. |
| Work Groups | DR notes, Maestro v1 | Generalizes current agent group/coder model to Epic-scoped teams. |
| Live Mode (originally Live Team) | DR notes, Maestro v1 | Generalizes current hotfix path; recast as a harness tempo, not a separate team type. |
| Golden stories | DR notes, Codex synthesis | Strongly reinforced by research on evaluation and benchmarks. |
| MPH: Model/Prompt/Harness | DR notes, research corpus | Research emphasizes harness engineering; DR names the triad. |
| Prompt packs | DR notes, research corpus | Research supports prompt/skill/harness versioning and eval. |
| First-class metrics | DR notes, research corpus | Tied to loop analysis, token/cost tracking, and eval. |
| Management vs Audit artifacts | DR notes, Codex synthesis | Codex initially proposed artifact-first; DR sharpened with two categories. |
| Evidence packages | Research corpus, Codex synthesis, DR notes | Research emphasizes proof artifacts; DR ties to artifact model. |
| Agent pairs | DR notes, research corpus | Research supports fresh eyes/adversarial review; DR makes artifact review invariant. |
| Reviewer vs Partner/Supervisor | DR notes, Codex synthesis | ADR-worthy refinement to prevent scope creep. |
| Internal adversarial reviewer | DR notes, research corpus | Research supports adversarial review; DR constrains mandate. |
| Postgres/sqlc/migrate data plane | DR notes | Codex agrees because artifacts/multi-user/metrics need stronger DB substrate. |
| Organizations/users | DR notes | MVP can defer RBAC and project memberships. |
| Docker-local Postgres default | DR notes, Codex synthesis | Fits current Docker requirement. |
| Branch hierarchy | DR notes, Codex synthesis | Aligns Epic/Story model with git. |
| UAT from Demo Mode | DR notes, Maestro v1 | Codex caution: easier after artifacts/gates exist. |
| Knowledge hierarchy | DR notes, research corpus | Research supports context governance; DR adds ADR/interface/doc/AST hierarchy. |
| Interfaces/contracts as knowledge | DR notes, Codex synthesis | High leverage for Coder handoffs. |
| Knowledge pack flow | DR notes, research corpus | Strong context-governance pattern. |
| Skills/pattern registry | Research corpus, DR notes | Research frames skills as procedural memory; DR prefers DB-relevant reuse. |
| Binary/rich uploads | DR notes, research corpus | Research supports multimodal intent artifacts. |
| Artifact templates | DR notes, Codex synthesis | Codex recommends canonical JSON with human Markdown rendering. |
| Cloud data plane/auth | DR notes | Codex recommends post-local-factory sequencing. |
| Extract `maestro-agent` | DR notes | Codex recommends after v2 contracts stabilize. |
| Preserve commentary/reasoning tokens | DR notes, Codex synthesis | Codex pushback: preserve as Audit data, avoid hidden chain-of-thought assumptions and automatic reinjection. |
| Repo docs LLM-facing, wiki human-facing | DR notes, Codex synthesis | Important documentation architecture principle. |
| Product model | DR notes, Codex synthesis | Codex recommends real lightweight model, not knowledge-only. |
| Container abstraction | DR notes | Parking lot/post-MVP; define interface before supporting non-Docker runtimes. |
| Remote/cloud agent jobs | DR notes | Likely v3; avoid early dispatcher over-abstraction. |
| Black-box benchmark runner | Claude review | Runner drives Maestro through external surfaces only, so it survives the v1-to-v2 break and can baseline the frozen v1 binary. |
| Benchmark noise/cost policy (D9) | Claude review | Repeat-run sampling, spread reporting, and budget caps before benchmark results drive decisions. |
| Per-phase exit criteria | Claude review, DR notes | Required by the agent-fleet build model: agents need checkable done-ness. |
| Single-operator build model | DR notes, Claude review | v2 is built by agents under one human; sequencing optimizes for bounded review load and early dogfooding. |
| v1 freeze-and-tag, develop on main (D7 revision) | DR notes, Claude review | v1 has no users to support; long-lived v2 branch dropped in favor of PRs to main after a freeze tag. |
| Port-vs-rewrite inventory (D8) | Claude review, Codex synthesis | Makes the v1-adr-alignment table an explicit Phase 0 decision at package grain. |
| Measurable success criteria | Claude review | Numbers behind the north star, baselined by the Phase 1 runner. |
| Single-repo first golden stories | Claude review | Multi-repo/UI golden stories deferred until Product/Feature machinery exists. |
| Task renamed to Epic | Claude review, DR notes | Preserves the universal Epic-contains-Stories prior; removes TASK message type and agent-tooling collisions. |
| Work Group naming | DR notes | Replaces Task Team; "Epic Team/Group" rejected as awkward. |
| Live Mode as tempo, not parallel system (D10) | DR notes, Claude review | Hotfix name rejected as bug-associated; interactive loop reuses Epic/Story model with human-in-loop review and trailing evidence. |

## Research Anchors

The external research corpus is most useful as validation and vocabulary, not as the origin of Maestro's core ideas.

Key anchors:

- **The New SDLC with Vibe Coding:** harness engineering, context engineering, factory model, human as orchestrator.
- **Agent Tools & Interoperability:** MCP for tools, A2A for collaborators, A2UI for safe UI artifacts.
- **Agent Skills:** progressive disclosure, skills as procedural memory, evals for trigger/execution/regression/token budget.
- **Vibe Coding Agent Security and Evaluation:** security vs evaluation, trajectory quality, self-repair, policy, sandboxing, observability.
- **Spec-Driven Production-Grade Development:** specs, gates, policy server, context hygiene, code review changes.
- **McKinsey AI Revolution:** agent factory operating model, daily human review, productivity measurement, knowledge graphs, spec-driven work.
- **Community Notes:** artifact-first handoffs, intent interfaces, finishing discipline, context governance, human attention.

