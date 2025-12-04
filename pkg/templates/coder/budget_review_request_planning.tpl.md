# Budget Review Request - PLANNING State

Loop budget exceeded in PLANNING state ({{.Extra.Loops}}/{{.Extra.MaxLoops}} iterations). Requesting guidance on next steps.

## Story Context

**Story ID:** {{.Extra.StoryID}}
**Story Type:** {{.Extra.StoryType}}

### Story Requirements

{{.Extra.TaskContent}}

## Current Progress

I am in **PLANNING** state with **read-only** container access. I have been exploring the codebase and requirements to create an implementation plan.

### Implementation Plan (Work in Progress)

{{.Extra.Plan}}

### Automated Pattern Analysis

{{.Extra.IssuePattern}}

## Recent Context ({{.Extra.ContextMessageCount}} messages)

```
{{.Extra.RecentActivity}}
```

## Review Request

**Please carefully review the "Recent Context" section above for duplicate or near-duplicate commands.** If the same command (or very similar commands) appear multiple times consecutively, this indicates a stuck loop that requires intervention.

**Please assess my planning progress and determine if:**

1. ✅ **Ready to submit plan** - I have done sufficient investigation and should use the `submit_plan` tool
   - Check: Recent Context shows varied exploration leading to understanding
   - Guidance: "You have gathered enough information. Please submit your plan using the `submit_plan` tool."

2. ✅ **Work already complete** - No code changes needed (static parity only), should use `story_complete` tool
   - Check: Exploration confirms feature already implemented
   - Guidance: "The requirements are already satisfied. Use the `story_complete` tool to finish the story."

3. ⚠️ **Need more exploration** - Plan needs additional investigation before submission
   - Check: Agent has clear direction for further exploration
   - Guidance: "Please explore [specific areas] before submitting your plan. Focus on [specific details]."

4. ⚠️ **Stuck in loop** - Repeating the same commands without learning anything new
   - **Check: Look for duplicate commands in Recent Context** (e.g., same `grep`, `find`, `cat` commands repeated)
   - Guidance: "You are stuck repeating [specific command]. Use ask_question tool to clarify requirements instead of repeating exploration."

5. ❌ **Story blocked** - Requirements unclear or technical blockers prevent progress
   - Check: Persistent blockers preventing plan creation
   - Guidance: Escalate or reject story with explanation

**Important Context:**
- I am in PLANNING state (read-only access)
- I have NOT started coding yet
- I need to either submit a plan OR mark the story complete
- I cannot execute implementation commands like `go mod init`, `npm install`, `make build` in planning state
