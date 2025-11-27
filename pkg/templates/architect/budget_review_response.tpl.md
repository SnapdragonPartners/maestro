# Budget Review Decision: {{.Extra.Status}}

You have exceeded the iteration budget for {{.Extra.OriginState}} state. The architect has reviewed your progress.

{{- if eq .Extra.Status "APPROVED"}}

## Decision: Continue Working

You are making good progress. Continue with your current approach.

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

## Decision: Adjust Approach

Your current approach needs adjustment to make progress.

{{- else}}

## Decision: Blocked

There is a fundamental blocker that needs to be addressed.

{{- end}}

## Architect Guidance

{{.Extra.Feedback}}

## Next Steps

{{- if eq .Extra.Status "APPROVED"}}

Continue with your current task. The architect believes you are on the right track.

{{- else if eq .Extra.Status "NEEDS_CHANGES"}}

Adjust your approach based on the guidance above and continue working.

{{- else}}

Address the blocking issue identified above. You may need to ask clarifying questions or escalate.

{{- end}}
