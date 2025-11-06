# Plan Approval Request

I have completed my analysis and created an implementation plan for this story.

## Story Requirements

{{.Extra.TaskContent}}

## Implementation Plan

{{.Extra.PlanContent}}
{{if .Extra.KnowledgePack}}

## Relevant Architectural Knowledge

The following architectural patterns and rules are relevant to this story:

```dot
{{.Extra.KnowledgePack}}
```

**Review Note**: Please verify the implementation plan aligns with these established patterns, especially any rules marked as high or critical priority.
{{end}}

## Review Request

**Please review this implementation plan and determine if:**

1. ✅ **Plan is sufficient** - The plan adequately addresses all requirements and acceptance criteria
   - If yes: **APPROVE** the plan so I can proceed to the CODING state with full write access

2. ⚠️ **Plan needs refinement** - The plan has gaps or unclear approaches
   - If yes: Use **NEEDS_CHANGES** and specify what needs clarification or additional detail

3. ❌ **Work is already complete** - No code changes are needed (static parity only)
   - If yes: Use **REJECTED** and suggest I use the `mark_story_complete` tool instead

**Note**: I am currently in PLANNING state with read-only access. I cannot start implementation until the plan is approved.
