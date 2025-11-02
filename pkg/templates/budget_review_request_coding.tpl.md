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

**Please assess my implementation progress and determine if:**

1. ✅ **Making progress** - Continue implementing the approved plan
   - Guidance: "You are on track. Continue with [specific next steps from plan]."

2. ⚠️ **Stuck in loop** - Repeating failed commands or not making forward progress
   - Guidance: "Stop attempting [specific failing commands]. Instead, [specific corrective action]."

3. ⚠️ **Wrong approach** - Implementation doesn't match approved plan
   - Guidance: "Your current approach differs from the approved plan. [Specific realignment needed]."

4. ⚠️ **Need clarification** - Approved plan is unclear or ambiguous
   - Guidance: "The plan needs clarification on [specific area]. [Specific guidance]."

5. ❌ **Fundamental blocker** - Cannot proceed with current approach
   - Guidance: Return to PLANNING or escalate issue

**Important Context:**
- I am in CODING state (full write access)
- I am implementing the APPROVED plan shown above
- I cannot modify the approved plan without re-approval
