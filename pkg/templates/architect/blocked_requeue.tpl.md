# Blocked Story Requeue Review

The coder working on **{{.Extra.StoryTitle}}** ({{.Extra.StoryID}}) reported a **{{.Extra.FailureKind}}** failure and has been removed from the story. This is attempt {{.Extra.AttemptCount}}.

## Failure Details

- **Failure kind:** `{{.Extra.FailureKind}}`
- **Explanation:** {{.Extra.Explanation}}
- **Failed state:** {{.Extra.FailedState}}
{{- if .Extra.ToolName}}
- **Failed tool:** `{{.Extra.ToolName}}`
{{- end}}

## Your Task

The story will be requeued for another coder. **The next coder has NO memory of this attempt.** Before requeue, you should review the failure and decide how to help the next attempt succeed.

{{if eq .Extra.FailureKind "story_invalid" -}}
### Story Invalid

The coder reported that the story requirements are unclear, contradictory, or impossible to implement. You **must** rewrite the story to fix the issues the coder identified.

Use `story_edit` with `revised_content` to replace the story with corrected requirements. Preserve the original intent and acceptance criteria, but fix whatever made the story unimplementable.

If you can also add `implementation_notes` with guidance for the next coder, do so.

{{else if eq .Extra.FailureKind "external" -}}
### External / Infrastructure Failure

The coder reported an infrastructure or environment issue (e.g., git corruption, container problems, missing build dependencies). This may or may not be related to the story itself.

**Decide which case applies:**

1. **Story-related** (e.g., story assumes a dependency that doesn't exist, requires a build step that isn't configured): Use `story_edit` to fix the story requirements.

2. **System-level** (e.g., git corruption, Docker daemon issues, filesystem problems): The story itself is fine. You may optionally add `implementation_notes` via `story_edit` to document what happened, or pass empty strings if no edits are needed.

{{end -}}
## Instructions

Call the `story_edit` tool with either:
- `revised_content` to replace the entire story (for fundamental issues)
- `implementation_notes` to append guidance (for minor context)
- Empty strings for both if no edits are needed

If this looks like a systemic infrastructure issue that will recur, note this in the implementation notes so the next coder is aware.
