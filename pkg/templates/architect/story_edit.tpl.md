# Story Annotation Before Requeue

The coder working on **{{.Extra.StoryTitle}}** ({{.Extra.StoryID}}) has been auto-rejected after {{.Extra.StreakCount}} consecutive NEEDS_CHANGES budget reviews. The story will be requeued for another coder to attempt.

## Your Task

Review the conversation history above — it contains all the budget review feedback you provided to the failing coder. Based on this history:

1. **Identify the root cause**: What fundamental issue caused the coder to get stuck?
2. **Provide concrete guidance**: What specific approach should the next coder take to avoid the same problem?
3. **Keep it actionable**: Write notes that a coder can immediately act on, not abstract strategy.

## Instructions

Call the `story_edit` tool with your implementation notes. These notes will be appended to the story content as an "Implementation Notes" section that the next coder will see.

If you genuinely have no useful guidance to add (e.g., the failure was due to transient issues rather than a conceptual problem), pass an empty string.

**Good notes example:**
> "The Go html/template package shares template names globally across Parse calls. Use template.New() to create isolated template sets per page to avoid 'already defined' collisions. The previous coder tried to reuse a single template set which caused name conflicts."

**Bad notes example:**
> "The coder needs to try harder and follow best practices."
