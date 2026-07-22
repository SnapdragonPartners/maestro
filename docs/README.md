+++
title = "Maestro Documentation"
edit_date = "2026-07-15"
status = "live"
summary = "Top-level index of docs/: the subdirectory map (ADRs, v2 planning, wiki, archive) and the retained deprecated v1 references at this root."
+++

# Maestro Documentation

Documentation authority is defined by [ADR 0017](adr/0017-v2-documentation-authority-and-lifecycle.md): for current runtime behavior — code and tests, then the FSM docs (`pkg/*/STATES.md`), then `CLAUDE.md`/`README.md`, then the `deprecated` v1 references below as unverified hints. For v2 design intent — Accepted ADRs, then `live` documents under `docs/v2/`. Archived documents carry no authority.

## Directories

- [adr/](adr/README.md) — Index of Maestro ADRs: the v2 decision sequence (0017+) and the deprecated historical v1 notes (0001-0016), with the documentation authority order in brief.
- [v2/](v2/README.md) — Index of the v2 planning doc set: roadmap, build process, phase artifacts, wishlists, and companion notes, with the per-phase directory convention.
- [wiki/](wiki/README.md) — Index of the human-facing v1 wiki pages — all deprecated, retained pending the wiki/docs-site decision (ADR 0017).
- [archive/](archive/README.md) — docs/archive/ holds v1-era documents with no authority for any question; bodies preserved verbatim as history per ADR 0017's archive plan.

## Retained v1 References (`deprecated`)

Unverified against current code; never authoritative for v2 design. Each flips to `archive` when its subject is ported, rewritten, or dropped.

- [Welcome to Maestro](WELCOME_TO_MAESTRO.md) — User-facing orientation to running v1 Maestro.
- [Maestro Operating Modes](MODES.md) — v1 operating modes reference (bootstrap, factory, hotfix, maintenance, demo).
- [Git Workflow Implementation](GIT.md) — v1 git workflow, branch protection, and hook reference.
- [Maestro Testing Strategy](TESTING_STRATEGY.md) — v1 testing strategy: shared mocks, unit vs integration boundaries, when to use real services.
- [Maestro → `maestro-llms` Migration Spec](MAESTRO_LLMS_MIGRATION.md) — Design and divergence checklist for the maestro-llms migration; still the boundary reference for pkg/agent/internal/llmadapter.
- [Architect Multi-Context Support](ARCHITECT_CONTEXT.md) — Implementation history and design decisions for the v1 architect's per-agent conversation contexts.
- [Maestro Agent Chat — Draft Spec (v0.3)](MAESTRO_CHAT_SPEC.md) — v1 agent chat system architecture: injection, persistence, secret scanning, escalation.
- [Hotfix Mode Specification](HOTFIX_MODE_SPEC.md) — v1 hotfix mode design — the ancestor of the v2 Workbench tempo.
- [Airplane Mode Specification](AIRPLANE_MODE.md) — v1 airplane mode: local Gitea forge lifecycle and GitHub sync.
- [Maintenance Mode Specification](MAINTENANCE_MODE_SPEC.md) — v1 maintenance mode spec; the mode is dropped in v2 (port inventory).
- [Ollama LLM Provider](OLLAMA.md) — Local Ollama provider usage notes for v1.
- [Maestro Knowledge Graph Implementation Spec](DOC_GRAPH.md) — v1 knowledge-graph documentation tooling (knowledge.dot); design retires with v1.
- [Running SWE-EVO Benchmarks](BENCHMARK_HOWTO.md) — How to run the v1 SWE-EVO benchmark — a Phase 1 seed for the ADR 0025 runner.
- [Benchmark Evaluation Tracker](BENCHMARKS.md) — v1 benchmark results and notes — a Phase 1 seed.
