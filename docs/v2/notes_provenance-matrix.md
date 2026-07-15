+++
title = "Maestro v2 Provenance Matrix"
edit_date = "2026-07-15"
status = "live"
summary = "Tracks where each major v2 idea came from — Maestro v1, DR notes, the research corpus, Codex synthesis, or Claude review — including decisions that deliberately diverge from research orthodoxy."
type = "notes"
+++

# Maestro v2 Provenance Matrix

Status: live — actively maintained cross-phase record.

This matrix tracks where major roadmap ideas came from. It is intentionally coarse. Many ideas have multiple sources.

Source categories:

- **Maestro v1:** already present or implied in current Maestro architecture.
- **DR notes:** explicit user/client-experience feedback in the marked-up roadmap.
- **Research corpus:** the Google/Kaggle whitepaper series, community notes, and McKinsey piece cited in the [research synthesis bibliography](research_synthesis.md#bibliography).
- **Codex synthesis:** recommendations or pushback introduced during roadmap synthesis.
- **Claude review:** feedback and revisions from the 2026-07-11 Claude review pass.

| Idea | Primary Sources | Notes |
|---|---|---|
| Agent factory framing | Maestro v1, research corpus, DR notes | Maestro already used PM/Architect/Coder and PR workflows; research adds shared "factory" vocabulary. |
| Graph-shaped workflows over chat | Maestro v1, research corpus | Current FSMs strongly align with research emphasis on workflow graphs. |
| Feature/Epic/Story taxonomy | DR notes, Codex synthesis | Refines v1 Spec/Story model into multi-repo and epic-scoped hierarchy. |
| CPA role | DR notes | Codex pushback: CPA should be role/interface first, not a mega-agent. Superseded 2026-07-12: intake-as-triage. |
| CTA role | DR notes | Added as technical pair for CPA, analogous to Architect. Dissolved 2026-07-12 into recipient pushback. |
| Work Groups | DR notes, Maestro v1 | Generalizes current agent group/coder model to Epic-scoped teams. |
| Workbench (originally hotfix/Live Team) | DR notes, Maestro v1 | Generalizes current hotfix path; recast as a harness tempo, not a separate team type. |
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
| Workbench as tempo, not parallel system (D10) | DR notes, Claude review | Hotfix name rejected as bug-associated, "Live Mode" as product-implying; interactive loop reuses Epic/Story model with trailing evidence. |
| Workbench entry via blank Feature request | DR notes | Master dashboard button; the orchestrator dispatches a special-case blank Feature scoped to a repo (originally CPA-dispatched). |
| Human accept + trailing agent drift review | DR notes | Human gates acceptance at the Workbench; agents still catch syntactic, rule, and architectural drift. |
| Economic argument / single-agent baseline | DR notes, Claude review | Cost per accepted change, not per token; golden suite includes a vibe-coding baseline to quantify the paired-agent premium and payoff. |
| Interim build process (Claude authors, Codex reviews, DR accepts) | DR notes | Manual implementation of the generate/review invariant; one dev branch at a time; golden suite at phase end. |
| v1 deprecated; `v1-freeze` tag | DR notes | Hard break declared 2026-07-11; no pre-freeze fixes; known v1 defects die with v1. |
| CPA/CTA names retained | DR notes, Claude review | Chief X Agent pattern mirrors CPO/CTO titles. Moot as of 2026-07-12: no standing pair to name. |
| Intake as triage, orchestrator-owned (D2 v3) | DR notes (external designer feedback), Claude review | Form + "I don't know" button + short-lived triage agent; artifact contract fixed, executor unbound; converges on original Codex D2 shape. |
| Provisional Work Groups | DR notes, Claude review | Feature-scoped PM runs conversational intake; becomes the executing group in the single-repo case — zero handoff. |
| Recipient pushback as review | DR notes | The Work Group receiving an Epic challenges its framing; replaces CTA. Cross-Epic coherence left to the spike. |
| Pre-Phase-5 intake spike | DR notes, Claude review | Planned-work bracket in the roadmap, not an issue; external design review included (timeboxed per Codex). |
| Symmetric review invariant | Codex synthesis, DR notes, Claude review | Codex flagged the human-authored-intake conflict; DR resolved it: reviewed by a party other than its author, recipient PM reviews human-authored Features. |
| Orchestrator definition and no-inference rule | DR notes, Claude review | Programmatic layer, never an agent, never calls an LLM; rules/config decisions vs inference decisions as the boundary test. |
| Phase 3 contract-only intake constraint | Codex synthesis | Phase 3 intake path must not preempt the pre-Phase-5 spike. |
| Fully agentic code review; humans validate outcomes | DR notes | Deliberate divergence from research-corpus orthodoxy: "all code is reviewed" ≠ "reviewed by a human"; conditioned on reviewer heterogeneity; human reservation is outcome validation (ADR 0020, amended). |
| Repo–Product many-to-many with primary Product | DR notes, Claude review | Shared repos (e.g. an API serving two Products) forced the revisit 0018 anticipated; primary Product keeps wrapper-Feature inference deterministic. |
| Tool call as the Audit action unit | DR notes | An LLM call without a tool call does nothing; LLM call records are token/cost metrics and optional traces (ADR 0022). |
| Object storage first-class with pluggable interface | DR notes | Local and cloud use different products; the contract is fixed, implementations plug in; retention pins apply to objects. |
| Generalized persistence interface + cloud auth mini-app | DR notes | Auth, data, and object persistence as pluggable modules per mode; cloud is not "point at another database" (ADR 0022). |
| Unconditional human Accept; auto-merge withdrawn | DR notes | Acceptance is outcome validation (need solved), not risk management; the D4 low-risk auto-merge idea is withdrawn from 0020/D4/pillar 8 (2026-07-13). |
| Reviewed history is immutable; Epic branches never rebase | Codex synthesis | Evidence binds commit provenance; Epics sync default via history-preserving merges; only Story branches rebase (ADR 0023). |
| Epic conflict flow via supplementary Story | DR notes | Orchestrator detects, Architect mints a conflict-resolution Story, merge retries — the Story-level flow one level up. |
| Golden build tags (`golden-minimal`/`golden-all`) | DR notes | Extends the existing `integration` build-tag pattern to automate golden story runs. |
| Artifact scope model (`scope_type`/`scope_id` + lineage) | Codex synthesis | Pre-Epic Feature artifacts, Product/org artifacts, and benchmark artifacts do not hang off an Epic. |
| Phase 1 target strategy (minimally patched v1 path) | Codex synthesis, DR notes | Codex surfaced the missing-target problem; DR resolved it: post-freeze main is v2 raw material, patched just enough for golden-minimal. |
| Phase 0 exit blocks on Phase 1-blocking ADRs | Codex synthesis | Runner ADR and D9 mechanism accepted before Phase 1 implementation starts. |

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

