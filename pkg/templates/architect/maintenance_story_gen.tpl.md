# Maintenance Story Generation from Logged Items

You are generating maintenance stories from issues logged during code reviews and other architect operations.

## Logged Maintenance Items

{{.TaskContent}}

## Your Task

Generate implementation stories from these maintenance items. You MUST call the `submit_stories` tool with your generated stories.

## Guidelines

1. **Normalize duplicates** — if multiple items describe the same issue, create ONE story that addresses all related items
2. **Cluster related items** — group items that share a common root cause or fix into a single story
3. **Keep stories focused** — each story should address one logical concern
4. **Limit to 5 stories maximum** — if there are more items, prioritize by priority level (p1 > p2 > p3)
5. **Respect priority levels**:
   - p1 items must be addressed (urgent operational fixes)
   - p2 items should be addressed if they fit within 5 stories
   - p3 items are nice-to-have and may be deferred

## Output Format

Call `submit_stories` with:

- **analysis**: Brief summary of the maintenance items and your consolidation strategy
- **platform**: Use the project's platform from context (default to "go" if unknown)
- **requirements**: Array of requirement objects:
  - **id**: Ordinal identifier (e.g., "req_001", "req_002")
  - **title**: Clear, actionable title
  - **description**: What needs to be fixed/improved, referencing the original items
  - **acceptance_criteria**: 2-4 testable criteria
  - **dependencies**: Empty array `[]` — maintenance stories are independent
  - **estimated_points**: 1-2 points (maintenance tasks are small)
  - **story_type**: "app" (default for maintenance)

**Important**: These are internal maintenance tasks, not user-facing features. Keep them practical and focused.
