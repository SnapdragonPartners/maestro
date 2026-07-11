# ADR 0012: Knowledge Graph as Repository Artifact

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro needs a way to preserve architectural knowledge across agent runs and story
boundaries. The knowledge graph feature captures project patterns, components, rules,
and design decisions so coders and the architect can share context beyond the current
prompt.

The implementation spec records many completed stories and notes that the parser
changed from a third-party DOT parser to a custom lenient parser.

## Decision

Represent project architectural knowledge as a repository artifact at
`.maestro/knowledge.dot`, and index it into the existing project SQLite database for
retrieval.

Principles:

- The DOT file is version-controlled with the application repo.
- Coders may update the knowledge graph as part of normal code changes.
- Architect reads the graph from its own up-to-date workspace and treats it as review
  context.
- SQLite indexes accelerate retrieval and cache story-specific knowledge packs.
- The feature is additive. Missing or invalid knowledge should degrade gracefully
  unless deterministic validation is explicitly part of the current workflow.

## Current Implementation

- `pkg/knowledge/` contains parser, validator, indexer, retrieval, search, and default
  graph logic with tests.
- `pkg/persistence/schema.go` added knowledge tables in migration version 10; current
  schema has advanced to version 23.
- `docs/DOC_GRAPH.md` records implementation status, schema decisions, parser
  decisions, workspace architecture, and future metrics work.
- `pkg/architect/request_merge.go` gives special merge-conflict guidance for
  `.maestro/knowledge.dot`.
- `pkg/tools/bootstrap_detector_test.go` exercises bootstrap detection around
  missing/present knowledge graph files.

## Consequences

- Do not create a separate `knowledge.db`; knowledge tables belong in `maestro.db`.
- Knowledge graph changes should go through the same branch, PR, review, and merge
  flow as code.
- Conflict guidance for knowledge graph merges should preserve unique nodes and
  relationships.
- If future work makes knowledge validation merge-blocking, that should be captured
  in a follow-up ADR or an accepted revision of this one.

## Related Documents

- `docs/DOC_GRAPH.md`
- `docs/KNOWLEDGE_GRAPH_TOOL_SPEC.md`
- `docs/ARCHITECT_CONTEXT.md`
- `docs/MERGE_CONFLICT_RESOLUTION_SPEC.md`

