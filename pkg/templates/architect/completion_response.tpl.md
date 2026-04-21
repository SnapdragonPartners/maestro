# Completion Review Decision: {{.Extra.Status}}

{{- if eq .Extra.Status "APPROVED"}}

Your story completion has been **approved**. The story is complete.

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

Additional work is required before this story can be marked complete.

{{- else}}

Your completion request has been **rejected**. The story will be abandoned due to fundamental misassessment.

{{- end}}

## Architect Feedback

{{.Extra.Feedback}}

## Next Steps

{{- if eq .Extra.Status "APPROVED"}}

Story is complete. No further action needed.

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

- Complete the additional work identified in the feedback
- Call `done` tool when ready for re-review

{{- else}}

- Story will be abandoned and may be rewritten by the architect
- No further action required from the coder

{{- end}}
