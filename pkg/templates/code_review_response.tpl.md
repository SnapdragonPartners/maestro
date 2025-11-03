# Code Review Decision: {{.Extra.Status}}

{{- if eq .Extra.Status "APPROVED"}}

Your implementation has been **approved**. Ready to proceed with merge.

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

Your implementation requires **changes** before it can be merged.

{{- else}}

Your implementation has been **rejected**. Significant rework is needed.

{{- end}}

## Architect Feedback

{{.Extra.Feedback}}

## Next Steps

{{- if eq .Extra.Status "APPROVED"}}

- Proceed to merge preparation
- Create pull request if not already created
- PR will be reviewed and merged

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

- Return to CODING state
- Address the feedback above
- Fix identified issues
- Call `done` tool when ready for re-review

{{- else}}

- Return to CODING state
- Major changes needed based on feedback
- Consider revisiting your approach
- Call `done` tool when ready for re-review

{{- end}}
