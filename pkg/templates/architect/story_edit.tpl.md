# Story Edit Before Requeue

{{if ge .Extra.StreakCount 6 -}}
The coder working on **{{.Extra.StoryTitle}}** ({{.Extra.StoryID}}) has been auto-rejected after {{.Extra.StreakCount}} consecutive NEEDS_CHANGES budget reviews. Multiple coders have struggled with this story.
{{- else -}}
The coder working on **{{.Extra.StoryTitle}}** ({{.Extra.StoryID}}) has been rejected during budget review (after {{.Extra.StreakCount}} NEEDS_CHANGES round(s)). The story will be requeued for another coder.
{{- end}}

**Critical: The next coder has NO memory of this attempt.** They will see only the story content you provide here.

## Your Task

Review the conversation history above — it contains all the budget review feedback you provided to the failing coder. Decide which approach to take:

### Option 1: Append Notes (minor issues)
If the story's approach is correct but the coder made avoidable mistakes, use `implementation_notes` to add guidance. The original story content is preserved and your notes are appended.

### Option 2: Rewrite the Story (fundamental issues)
If the story's prescribed approach is **fundamentally infeasible** — for example, it assumes an architecture that doesn't exist, prescribes a fix that can't work with the actual codebase, or the technical approach needs to be completely different — use `revised_content` to **replace the entire story**. Preserve the original title, acceptance criteria, and intent, but fix the technical approach.

**You should rewrite when:**
- The coder correctly diagnosed why the plan can't work but had no approved alternative
- The story assumes something about the codebase that isn't true
- The same fundamental failure will recur regardless of implementation skill

**You should append notes when:**
- The approach is sound but the coder made implementation mistakes
- The coder missed a specific technical detail
- Additional context would help the next attempt succeed

## Instructions

Call the `story_edit` tool. Either:
- Set `implementation_notes` with guidance (keeps original story, appends notes)
- Set `revised_content` with a complete rewritten story (replaces everything)

If you genuinely have no useful edits, pass empty strings for both.

**Good rewrite example:**
> Rewrites the story to say "Change main.go template parsing to create per-page template sets instead of one global set" when the original story said "just remove the fallback blocks" but the global template set makes that approach impossible.

**Good notes example:**
> "The Go html/template package shares template names globally across Parse calls. Use template.New() to create isolated template sets per page to avoid 'already defined' collisions."

**Bad example:**
> "The coder needs to try harder and follow best practices."
