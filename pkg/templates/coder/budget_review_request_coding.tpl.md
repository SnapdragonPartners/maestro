# Budget Review Request - CODING State

Loop budget exceeded in CODING state ({{.Extra.Loops}}/{{.Extra.MaxLoops}} iterations). Requesting guidance on implementation progress.

## Story Context

**Story ID:** {{.Extra.StoryID}}
**Story Type:** {{.Extra.StoryType}}

### Story Requirements

{{.Extra.TaskContent}}

## Implementation Status

I am in **CODING** state with **full write access**, implementing the approved plan.

### Approved Plan (Reference)

{{.Extra.ApprovedPlan}}

### Automated Pattern Analysis

{{.Extra.IssuePattern}}

## Recent Context ({{.Extra.ContextMessageCount}} messages)

```
{{.Extra.RecentActivity}}
```

## Review Request

**Please carefully review the "Recent Context" section above for duplicate or near-duplicate commands.** If the same command (or very similar commands) appear multiple times consecutively, this indicates a stuck loop that requires intervention.

**Please assess my implementation progress and determine if:**

1. ✅ **Making progress** - Continue implementing the approved plan
   - Check: Recent Context shows varied tool use with forward progress
   - Guidance: "You are on track. Continue with [specific next steps from plan]."

2. ⚠️ **Stuck in loop** - Repeating the same or similar commands without progress
   - **Check: Look for duplicate commands in Recent Context** (e.g., same `cat`, `sed`, `ls` commands repeated)
   - Guidance: "You are stuck repeating [specific command]. This is not making progress. Instead, [specific corrective action or use ask_question tool]."

3. ⚠️ **Wrong approach** - Implementation doesn't match approved plan
   - Check: Tool use and file changes diverge from approved plan
   - Guidance: "Your current approach differs from the approved plan. [Specific realignment needed]."

4. ⚠️ **Need clarification** - Approved plan is unclear or ambiguous
   - Check: Agent appears uncertain about next steps
   - Guidance: "The plan needs clarification on [specific area]. [Specific guidance]."

5. ❌ **Fundamental blocker** - Cannot proceed with current approach
   - Check: Persistent errors or architectural issues preventing progress
   - Guidance: Return to PLANNING or escalate issue

**Important Context:**
- I am in CODING state (full write access)
- I am implementing the APPROVED plan shown above
- I cannot modify the approved plan without re-approval
