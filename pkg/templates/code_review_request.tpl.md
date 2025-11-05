# Code Review Request

I have completed the implementation and all tests are passing. Ready for code review.

## Implementation Summary

{{.Extra.Summary}}

## Evidence

{{.Extra.Evidence}}

## Confidence Level

{{.Extra.Confidence}}

## Git Diff

{{.Extra.GitDiff}}

## Original Story Requirements

{{.Extra.OriginalStory}}

## Reference: Approved Plan (DO NOT EVALUATE - ALREADY APPROVED)

The following plan was already approved in PLAN_REVIEW and is immutable. Use it only as context to verify the implementation matches what was approved:

{{.Extra.ApprovedPlan}}
{{if .Extra.KnowledgePack}}

## Relevant Architectural Knowledge

The following architectural patterns and rules are relevant to this story:

```dot
{{.Extra.KnowledgePack}}
```

**Review Note**: Please verify the implementation aligns with these established patterns, especially any rules marked as high or critical priority.
{{end}}

## Review Request

**Please review this implementation and determine if:**

1. ✅ **Code is acceptable** - Implementation matches approved plan, tests pass, changes are appropriate
   - If yes: **APPROVE** so I can proceed to merge/completion

2. ⚠️ **Code needs changes** - Issues found that require fixes
   - If yes: Use **NEEDS_CHANGES** with specific feedback on what to fix

3. ❌ **Code is rejected** - Fundamental issues, wrong approach
   - If yes: Use **REJECTED** and I will return to planning
