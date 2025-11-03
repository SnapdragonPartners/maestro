# Plan Review Decision: {{.Extra.Status}}

{{- if eq .Extra.Status "APPROVED"}}

Your implementation plan has been **approved**. You may now proceed to CODING state with full write access.

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

Your implementation plan requires **changes** before proceeding to implementation.

{{- else}}

Your implementation plan has been **rejected**. Please return to planning with the feedback below.

{{- end}}

## Architect Feedback

{{.Extra.Feedback}}

## Next Steps

{{- if eq .Extra.Status "APPROVED"}}

- Transition to CODING state
- Begin implementing the approved plan
- Use the todos you collected to track progress

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

- Review the feedback above carefully
- Refine your plan to address the concerns
- Resubmit your plan for approval when ready

{{- else}}

- Return to PLANNING state
- Consider a different approach based on the feedback
- Create a new plan when ready

{{- end}}
