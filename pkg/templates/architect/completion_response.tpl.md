# Completion Review Decision: {{.Extra.Status}}

{{- if eq .Extra.Status "APPROVED"}}

Your story completion has been **approved**. The work is complete.

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

Additional work is required before this story can be marked complete.

{{- else}}

Your completion request has been **rejected**. The story clearly requires implementation work.

{{- end}}

## Architect Feedback

{{.Extra.Feedback}}

## Next Steps

{{- if eq .Extra.Status "APPROVED"}}

Story is complete. The work will be merged and the story closed.

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

- Return to CODING state
- Complete the additional work identified in the feedback
- Call `done` tool when ready for re-review

{{- else}}

- Return to CODING state
- Implement the required changes
- This story needs active development work
- Call `done` tool when implementation is complete

{{- end}}
