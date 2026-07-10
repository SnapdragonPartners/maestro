# Maestro v2 Research Synthesis

Date: 2026-07-10

This note summarizes the research documents under `/Users/dratner/Code/temp/Research` and captures preliminary high-level implications for Maestro v2.

## Documents Reviewed

Google papers:

- `Google Papers/Day_1_v3.pdf` - The new SDLC with vibe coding / agentic engineering.
- `Google Papers/Agent Tools & Interoperability_Day_2.pdf` - MCP, A2A, A2UI, AP2, UCP, and agent interoperability.
- `Google Papers/Agent Skills_Day_3.pdf` - Agent skills as procedural memory, progressive disclosure, evals, and skill governance.
- `Google Papers/Vibe Coding Agent Security and Evaluation_Day_4.pdf` - Security, trust, sandboxing, policy, observability, and evaluation dimensions.
- `Google Papers/Day_5_v3.pdf` - Spec-driven production-grade development, review culture, guardrails, policy servers, and context hygiene.

Community notes:

- `Google Papers/Community Notes/01_work_decomposition_agentic_workflows.md`
- `Google Papers/Community Notes/02_intent_interfaces_decision_artifacts.md`
- `Google Papers/Community Notes/03_finishing_validation_production_feedback.md`
- `Google Papers/Community Notes/04_knowledge_persistence_context_memory_patterns.md`
- `Google Papers/Community Notes/05_human_leverage_attention_org_design.md`
- `Google Papers/Community Notes/agentic_organization_white_papers_combined.md` - Combined duplicate of the five notes above.

External enterprise lens:

- `McKinsey/the-ai-revolution-in-software-development_final.pdf`

## Core Synthesis

The corpus is highly coherent. Across the Google papers, community notes, and McKinsey piece, the central claim is:

> The future of software development is not a better chat loop or a single smarter coding agent. It is a governed agent factory: graph-shaped work, scoped context, durable artifacts, proof-oriented validation, policy-bound execution, reusable procedural knowledge, and carefully protected human attention.

Maestro is already pointed at this target. Its PM, Architect, and Coder roles; explicit FSMs; typed dispatcher protocol; SQLite persistence; containerized workspaces; architect per-agent context; durable asks/incidents; and knowledge graph direction all match the research's "graph, not chat" thesis.

The v2 opportunity is less about inventing orchestration from scratch and more about making Maestro's harness explicit, inspectable, tunable, and productized.

## What The Research Emphasizes

### 1. Graphs Beat Chats

The research repeatedly argues that chat is an interface, not an operating model. Long-running agentic work needs named phases, explicit edges, recovery loops, state, artifacts, and telemetry.

Implication for Maestro:

- Maestro's FSM architecture is a strength.
- v2 should expose the workflow graph more directly in the product surface.
- Users should be able to see where work is, what edge is blocking it, what evidence exists, and what decision is needed.

### 2. Intent Is The First Bottleneck

The papers distinguish a prompt from intent. Humans often cannot author perfect requirements upfront, but they can react to concrete artifacts: prototypes, examples, simulations, decision logs, good/bad piles, and review surfaces.

Implication for Maestro:

- The PM should become an intent compiler, not just an interviewer.
- Specs should preserve goals, non-goals, constraints, examples, risks, decisions, and acceptance evidence.
- Ambiguous work may need generated "decision artifacts" before implementation.

### 3. Artifacts Are The Unit Of Handoff

Durable artifacts are the practical memory of an agentic organization: specs, plans, story packets, evidence, screenshots, logs, journals, risk registers, acceptance criteria, and postmortems.

Implication for Maestro:

- Chat should feed artifacts, not be the artifact.
- Every major node should emit an inspectable artifact.
- v2 should make artifacts easy to navigate across a story lifecycle.

### 4. "Done" Means Evidence, Not Confidence

The papers are explicit: a model saying "done" is not evidence. Finished work requires tests, review, visual proof where relevant, logs, deployment controls, and feedback loops.

Implication for Maestro:

- Story completion should produce an evidence package.
- Evidence should map back to acceptance criteria.
- For UI work, the package should include screenshots, browser traces, or short before/after videos.
- The architect's review should inspect evidence, not just code.

### 5. Skills Are Procedural Memory

The Agent Skills paper frames skills as small, owned, versioned, tested units of reusable know-how. They use progressive disclosure: metadata is always visible, full instructions load only when triggered, and scripts/assets/references load on demand.

Implication for Maestro:

- Not every reusable behavior should become a sub-agent.
- Maestro v2 could support a project/org skill registry for PM, Architect, and Coder workflows.
- Successful repeated workflows could be harvested from traces into draft skills.
- Skills should have evals, owners, versions, and promotion tiers.

### 6. Context Governance Beats Universal Memory

The community notes strongly warn against "one giant memory." Useful memory is scoped, retrievable, decayed, auditable, and sometimes forgotten. Context selection is an architectural discipline.

Implication for Maestro:

- Maestro's per-agent architect context is already aligned.
- v2 could add explicit context scopes: working context, story state, project state, session history, persona continuity, organization knowledge, external pattern library.
- The system should record why context was injected and when it becomes stale.
- Transcript mining and contradiction detection are promising v2 features.

### 7. Security And Evaluation Are Different

Day 4 separates safety from quality. Security answers "did the agent stay inside the boundary?" Evaluation answers "was the result worth shipping?"

Implication for Maestro:

- Container isolation is necessary but not sufficient.
- v2 should add policy gating before tool calls and high-risk actions.
- Evaluation should measure intent satisfaction, functional correctness, UI behavior, cost, code quality, trajectory quality, and self-repair.
- Traces should support both debugging and eval.

### 8. Humans Remain The Scarce Resource

The research frames human attention as the limiting factor. Agents can produce more work than humans can inspect. Human value shifts to intent, judgment, taste, risk acceptance, prioritization, and governance.

Implication for Maestro:

- The WebUI should evolve from "watch agents work" to "manage a production line."
- The system should prioritize review queues by risk, uncertainty, cost, and blocked state.
- Interruptions should be earned, not constant.
- Users need evidence summaries and decision surfaces, not raw exhaust.

### 9. Protocol Boundaries Matter

Day 2 distinguishes MCP, A2A, and A2UI:

- MCP is for tools with structured request/response semantics.
- A2A is for collaborators that may need multi-turn state and responsibility transfer.
- A2UI is for safe interactive human-facing artifacts.

Implication for Maestro:

- Maestro should keep "tool," "agent," and "artifact/UI surface" as separate contracts.
- A future plugin/protocol layer should not flatten all external capability into one tool abstraction.

### 10. Enterprise Adoption Requires Operating Model Change

McKinsey's piece emphasizes that real gains come from rearchitecting the software lifecycle, not merely handing developers AI tools. The recurring enterprise pattern is a day/night factory: humans set direction and review evidence, agents execute and iterate.

Implication for Maestro:

- Maestro's product story should be about the agent factory, not just code generation.
- The metrics should track outcomes: accepted changes, defect rate, cycle time, cost-to-converge, review cycles, and human attention per shipped artifact.

## Where Maestro Is Already Aligned

- Explicit PM, Architect, and Coder roles.
- FSM-based agent lifecycle rather than a loose chat loop.
- Typed dispatcher protocol for tasks, questions, requests, responses, errors, and shutdown.
- Per-agent workspaces and container isolation.
- Plan, code, test, review, merge loop.
- Architect per-agent conversation contexts scoped by story.
- SQLite persistence and resume semantics.
- Durable user asks and architect incidents.
- Toolloop pattern with terminal tools and state-machine ProcessEffect signals.
- Knowledge graph as a repository artifact.
- Model heterogeneity by role.

## Preliminary High-Level Ideas For Maestro v2

### A. Make The Harness The Product

Maestro v2 should expose and manage the harness explicitly:

- Context sources and scopes.
- Tool access.
- Agent roles and capability profiles.
- Workflow graph state.
- Policy gates.
- Evaluation results.
- Evidence packages.
- Cost and token budgets.
- Recovery loops.

This would make Maestro less "a thing that runs agents" and more "the local operating system for agentic engineering."

### B. Introduce Evidence Packages

Each story should produce a durable evidence package, possibly under `.maestro/evidence/` or persisted/indexed in SQLite:

- Acceptance criteria.
- Tests run and results.
- Build/lint output.
- Diff summary.
- Files changed.
- Screenshots/browser traces/videos where relevant.
- Risk flags.
- Known gaps.
- Architect decision.
- Human approvals, if any.

This becomes the artifact that justifies merge and later explains why the work was accepted.

### C. Add Intent Artifacts To The PM Flow

The PM output could grow beyond a spec markdown:

- Intent brief.
- Domain glossary.
- Good/bad examples.
- Decision log.
- Risk register.
- Open questions.
- Acceptance map.

The PM's job becomes "compile ambiguous intent into executable artifacts."

### D. Build A Maestro Skill/Pattern Registry

Maestro could maintain reusable skills or patterns at project and global scope:

- Spec-writing patterns.
- Review patterns.
- Testing patterns.
- UI verification patterns.
- Hotfix workflows.
- Domain-specific implementation conventions.
- Deployment/runbook patterns.

Important guardrails:

- Skills are owned and versioned.
- Skills have positive and negative trigger tests.
- Skills have promotion tiers: read-only, draft-only, action-allowed.
- Agent-generated skills enter as draft and require review.

### E. Treat Context As A Governed Resource

Add a context ledger or context plan to each LLM call:

- What was injected.
- Why it was selected.
- Which scope it came from.
- Whether it is fresh, stale, or speculative.
- What was deliberately excluded.

This supports debugging, cost control, and trust.

### F. Add Contradiction Detection

Run a background or on-demand contradiction detector across:

- Specs.
- ADRs.
- Knowledge graph.
- Story plans.
- Architect feedback.
- User decisions.
- Chat transcripts.

Contradictions should not auto-fix anything. They should become discussable artifacts.

### G. Add Policy Gates

Move toward an explicit policy layer:

- Structural gates: role/env/tool allowlists, branch protections, filesystem scopes.
- Semantic gates: high-risk action summaries checked against policy.
- Human gates: risk-tiered approval with clear "vibe diff" summaries.

This is especially relevant for future actions beyond local code changes.

### H. Upgrade The WebUI Into An Attention Surface

Possible v2 UI concepts:

- Production-line dashboard: running, blocked, reviewing, merged, failed.
- Review queue sorted by risk and uncertainty.
- Evidence package viewer.
- Cost-to-converge and token burn.
- Agent trajectory replay.
- Human ask/incident inbox.
- Pattern/skill registry browser.
- Context explorer: why the agent believed what it believed.

### I. Add Evaluation As A First-Class Subsystem

A v2 eval subsystem could score:

- Intent satisfaction.
- Functional correctness.
- Visual and behavioral correctness.
- Cost and efficiency.
- Code quality and convention matching.
- Trajectory quality.
- Self-repair quality.

The goal is not one perfect score. The goal is enough signals to see where the factory is improving or degrading.

### J. Keep Multi-Agent Where It Matters

The research pushes against reflexively making every concern a sub-agent. Maestro should keep agents where they carry distinct responsibility, access, state, model choice, or parallelism. Use skills/patterns for procedural specialization.

## Possible v2 Thesis

Maestro v2 is a local agent factory that converts ambiguous human intent into reviewed, evidenced, production-ready changes while continuously improving the harness, skills, and project knowledge that make future work cheaper and safer.

## Discussion Questions

1. Should v2 be positioned primarily as an "agent factory" rather than an "agent orchestrator"?
2. What is the minimum viable evidence package for a story?
3. Which artifacts should live in the repo, which in SQLite, and which in both?
4. Should the PM generate structured intent artifacts in addition to markdown specs?
5. Should Maestro implement skills as a user-facing primitive, or should "patterns" be the first abstraction?
6. How much of context governance should be visible in the UI?
7. Where should policy gates live: toolloop, dispatcher, tool execution layer, or a separate policy service?
8. What should be the first eval metric Maestro tracks beyond tests passing?
9. What kinds of agent workflows should remain explicit agents versus becoming skills?
10. What would make v2 feel calmer and more trustworthy to a human supervising multiple agents?

