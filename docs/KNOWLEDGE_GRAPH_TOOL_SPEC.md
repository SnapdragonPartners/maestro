# Knowledge Graph: Tool-Based Editing + Validation at Test Time

## Problem

The coder agents directly edit `.maestro/knowledge.dot` but have no schema guidance during normal coding (only during bootstrap). This results in invalid entries — wrong enum values (`application` instead of `architecture`, `planned` instead of `future`), missing required fields, etc. The validation only runs post-merge, so invalid graphs get committed and merged before the error is caught.

Two fixes needed:
1. **Prevent invalid edits**: Replace direct file editing with a `knowledge_update` tool that validates before writing
2. **Catch invalid edits**: Add knowledge graph validation to the TESTING phase so invalid graphs fail the test cycle

## Design

### Tool: `knowledge_update`

A structured tool that the LLM calls instead of editing the DOT file directly. The tool's parameter schema (with enums) IS the documentation — no prompt bloat.

Operations:
- `add_node` — Add a new node (fails if already exists)
- `update_node` — Update an existing node's attributes (fails if not found)
- `remove_node` — Remove a node and its edges (fails if not found)
- `add_edge` — Add a relationship between two nodes
- `remove_edge` — Remove a relationship

The tool:
1. Reads + parses existing `.maestro/knowledge.dot` (uses `knowledge.ParseDOT()`)
2. Applies the operation
3. Validates the full graph (uses `knowledge.ValidateAndReport()`)
4. Writes back only if valid (uses `graph.ToDOT()`)
5. Returns error with guidance if invalid

### Testing Phase Validation

Add knowledge graph validation alongside the existing loopback lint check in `proceedToCodeReviewWithLintCheck()`. If `.maestro/knowledge.dot` was modified on the branch (check via `git diff`), parse and validate it. If invalid, return to CODING with the validation errors as feedback.

## Changes

### 1. `pkg/tools/knowledge_update.go` — New tool implementation

```go
type KnowledgeUpdateTool struct {
    executor      execpkg.Executor
    workspaceRoot string
}
```

**Tool Schema:**
```
knowledge_update:
  operation (required, enum): "add_node" | "update_node" | "remove_node" | "add_edge" | "remove_edge"

  # For node operations:
  name (required for nodes): Node identifier (kebab-case, e.g. "api-server")
  type (required for add_node, enum): "component" | "interface" | "abstraction" | "datastore" | "external" | "pattern" | "rule"
  level (required for add_node, enum): "architecture" | "implementation"
  status (required for add_node, enum): "current" | "deprecated" | "future" | "legacy"
  description (required for add_node): Human-readable explanation
  priority (required for rules, enum): "critical" | "high" | "medium" | "low"
  tag (optional): Categorization tag
  component (optional): Parent component name
  path (optional): File path reference
  example (optional): Code example

  # For edge operations:
  from (required for edges): Source node ID
  to (required for edges): Target node ID
  relation (required for edges, enum): "calls" | "uses" | "implements" | "configured_with" | "must_follow" | "must_not_use" | "superseded_by" | "supersedes" | "coexists_with"
```

**Exec logic:**
1. Read `.maestro/knowledge.dot` from workspace (create empty graph if file doesn't exist)
2. Parse with `knowledge.ParseDOT()`
3. Apply operation:
   - `add_node`: Check node doesn't exist, create `knowledge.Node`, add to graph
   - `update_node`: Find existing node, merge provided fields (only update non-empty fields)
   - `remove_node`: Remove node and all edges referencing it
   - `add_edge`: Verify both nodes exist, add `knowledge.Edge`
   - `remove_edge`: Find and remove matching edge
4. Validate with `knowledge.ValidateAndReport()`
5. If invalid: return error with validation messages (don't write)
6. If valid: write `graph.ToDOT()` to file, return success summary

**Key design choices:**
- `update_node` merges fields — only provided (non-empty) fields are updated, others preserved
- `remove_node` cascades to edges — removes all edges where the node is from or to
- File path is always `.maestro/knowledge.dot` relative to workspace root
- Uses executor to read/write (works in both container and local modes)

### 2. `pkg/tools/constants.go` — Add constant

```go
// Knowledge tools.
ToolKnowledgeUpdate = "knowledge_update"
```

### 3. `pkg/tools/registry.go` — Register the tool

Register factory in `init()`. Factory creates tool with executor and workspace root from `AgentContext`.

### 4. `pkg/tools/constants.go` — Add to coder tool sets

Add `ToolKnowledgeUpdate` to:
- `AppCodingTools` (app stories can update knowledge graph)
- `DevOpsCodingTools` (devops stories can update knowledge graph)
- `AppPlanningTools` (planning can add nodes for architectural decisions)
- `DevOpsPlanningTools` (same)

### 5. `pkg/coder/testing.go` — Knowledge graph validation in testing

Add `runKnowledgeGraphValidation()` method, called from `proceedToCodeReviewWithLintCheck()`:

```go
func (c *Coder) proceedToCodeReviewWithLintCheck(ctx context.Context, sm *agent.BaseStateMachine, workspacePath string) (proto.State, bool, error) {
    // Existing loopback lint check
    if lintResult := c.runLoopbackLintCheck(workspacePath); lintResult != nil {
        c.logger.Warn("🔍 Loopback lint check found issues, returning to CODING")
        return c.executeTestFailureAndTransition(ctx, sm, lintResult)
    }

    // NEW: Knowledge graph validation
    if lintResult := c.runKnowledgeGraphValidation(workspacePath); lintResult != nil {
        c.logger.Warn("🔍 Knowledge graph validation failed, returning to CODING")
        return c.executeTestFailureAndTransition(ctx, sm, lintResult)
    }

    return c.proceedToCodeReview()
}
```

**`runKnowledgeGraphValidation()`**:
1. Check if `.maestro/knowledge.dot` exists in workspace — if not, skip (return nil)
2. Check if file was modified on the branch: `git diff --name-only origin/main...HEAD -- .maestro/knowledge.dot`
3. If not modified, skip (return nil) — only validate changes the coder made
4. Read and parse the file with `knowledge.ParseDOT()`
5. Validate with `knowledge.ValidateAndReport()`
6. If invalid: return `effect.NewGenericTestFailureEffect()` with the validation error message
7. If valid: return nil (continue to code review)

### 6. Prompt updates — Discourage direct editing

Add brief guidance to coding templates:
- `pkg/templates/coder/app_coding.tpl.md`
- `pkg/templates/coder/devops_coding.tpl.md`
- `pkg/templates/claude/prompts.go` (Claude Code system prompt)

Example addition:
> **Knowledge Graph**: To update `.maestro/knowledge.dot`, use the `knowledge_update` tool instead of editing the file directly. The tool validates your changes against the schema before writing. Available operations: add_node, update_node, remove_node, add_edge, remove_edge.

This can be brief since the tool schema itself documents the valid enum values.

### 7. `pkg/templates/coder/app_planning.tpl.md` / `devops_planning.tpl.md` — Planning guidance

Add brief note that `knowledge_update` is available in planning to document architectural decisions:
> You can use `knowledge_update` to document new architectural patterns or components discovered during planning.

## Files Modified

| File | Change |
|------|--------|
| `pkg/tools/knowledge_update.go` | **New**: `KnowledgeUpdateTool` with 5 operations |
| `pkg/tools/constants.go` | `ToolKnowledgeUpdate` constant + add to coding/planning tool sets |
| `pkg/tools/registry.go` | Factory registration |
| `pkg/coder/testing.go` | `runKnowledgeGraphValidation()` + wire into `proceedToCodeReviewWithLintCheck()` |
| `pkg/templates/coder/app_coding.tpl.md` | Brief tool guidance |
| `pkg/templates/coder/devops_coding.tpl.md` | Brief tool guidance |
| `pkg/templates/claude/prompts.go` | Claude Code system prompt guidance |
| `pkg/templates/coder/app_planning.tpl.md` | Brief tool mention |
| `pkg/templates/coder/devops_planning.tpl.md` | Brief tool mention |

## Existing Code Reused

| File | What |
|------|------|
| `pkg/knowledge/parser.go` | `ParseDOT()`, `Node`, `Edge`, `Graph` structs, `graph.ToDOT()` |
| `pkg/knowledge/validator.go` | `ValidateGraph()`, `ValidateAndReport()`, all enum definitions |
| `pkg/coder/testing.go:556` | `proceedToCodeReviewWithLintCheck()` — insertion point for validation |
| `pkg/effect/` | `NewGenericTestFailureEffect()` for returning validation errors |

## Verification

1. `make build` — compilation + lint
2. `make test` — existing tests pass
3. Unit test: `KnowledgeUpdateTool` add_node with valid enums succeeds
4. Unit test: `KnowledgeUpdateTool` add_node with invalid enum returns error
5. Unit test: `KnowledgeUpdateTool` update_node merges fields correctly
6. Unit test: `KnowledgeUpdateTool` remove_node cascades to edges
7. Unit test: `runKnowledgeGraphValidation` detects invalid graph, returns test failure effect
8. Unit test: `runKnowledgeGraphValidation` skips when file not modified on branch
9. Integration: coder creates node via tool, testing phase validates, code review proceeds

## Migration

Existing direct edits to `knowledge.dot` will still work — the testing phase validation catches errors regardless of whether the tool or direct editing was used. The tool is the recommended path (via prompt guidance) but not the only path. The validation at test time is the enforcement mechanism.

## Not in Scope

- Blocking direct file_edit on knowledge.dot (too heavy-handed; testing validation catches it)
- Read operations (the knowledge pack system already handles reads via `pkg/knowledge/indexer.go`)
- Schema migration of existing knowledge.dot files (validator errors are self-documenting)
- Architect-side knowledge_update tool (architect is read-only; maintenance stories handle graph cleanup)
