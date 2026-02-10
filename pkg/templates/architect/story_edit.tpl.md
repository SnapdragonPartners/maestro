# Story Annotation Before Requeue

{{if ge .Extra.StreakCount 6 -}}
The coder working on **{{.Extra.StoryTitle}}** ({{.Extra.StoryID}}) has been auto-rejected after {{.Extra.StreakCount}} consecutive NEEDS_CHANGES budget reviews. Multiple coders have struggled with this story.
{{- else -}}
The coder working on **{{.Extra.StoryTitle}}** ({{.Extra.StoryID}}) has been rejected during budget review (after {{.Extra.StreakCount}} NEEDS_CHANGES round(s)). The story will be requeued for another coder.
{{- end}}

**Critical: The next coder has NO memory of this attempt.** They will see only the original story content plus any notes you add here.

## Your Task

Review the conversation history above — it contains all the budget review feedback you provided to the failing coder. Based on this history:

1. **Identify the root cause**: What fundamental issue caused the coder to get stuck or fail?
2. **Warn about pitfalls**: What specific mistakes should the next coder avoid?
3. **Provide concrete guidance**: What approach should the next coder take instead?
4. **Keep it actionable**: Write notes the next coder can immediately act on — they have zero context from the failed attempt.

## Instructions

Call the `story_edit` tool with your implementation notes. These notes will be appended to the story content as an "Implementation Notes" section that the next coder will see.

If you genuinely have no useful guidance to add (e.g., the failure was due to transient issues rather than a conceptual problem), pass an empty string.

**Good notes example:**
> "The Go html/template package shares template names globally across Parse calls. Use template.New() to create isolated template sets per page to avoid 'already defined' collisions. The previous coder tried to reuse a single template set which caused name conflicts."

**Bad notes example:**
> "The coder needs to try harder and follow best practices."
