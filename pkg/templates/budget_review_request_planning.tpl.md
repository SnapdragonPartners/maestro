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

**Please assess my planning progress and determine if:**

1. ✅ **Ready to submit plan** - I have done sufficient investigation and should use the `submit_plan` tool
   - Guidance: "You have gathered enough information. Please submit your plan using the `submit_plan` tool."

2. ✅ **Work already complete** - No code changes needed (static parity only), should use `mark_story_complete` tool
   - Guidance: "The requirements are already satisfied. Use the `mark_story_complete` tool to finish the story."

3. ⚠️ **Need more exploration** - Plan needs additional investigation before submission
   - Guidance: "Please explore [specific areas] before submitting your plan. Focus on [specific details]."

4. ⚠️ **Wrong approach** - I'm using wrong tools or stuck in a loop
   - Guidance: "Stop attempting [specific commands]. Instead, [specific corrective action]."

5. ❌ **Story blocked** - Requirements unclear or technical blockers prevent progress
   - Guidance: Escalate or reject story with explanation

**Important Context:**
- I am in PLANNING state (read-only access)
- I have NOT started coding yet
- I need to either submit a plan OR mark the story complete
- I cannot execute implementation commands like `go mod init`, `npm install`, `make build` in planning state
