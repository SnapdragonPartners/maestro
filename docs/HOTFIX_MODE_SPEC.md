# Hotfix Mode Specification

## Overview

Hotfix mode provides a fast path for urgent, small changes that bypass the normal spec-driven development queue. This enables users to make quick tweaks and fixes without waiting for in-progress feature work to complete.

## Key Concepts

### Hotfix vs Express (Orthogonal Dimensions)

These are two independent properties of a story:

| Property | Determined By | Meaning |
|----------|---------------|---------|
| **Hotfix** | PM (user intent) | Bypasses normal queue, goes to dedicated coder |
| **Express** | Architect (complexity) | Skips planning phase, goes directly to coding |

A story can be:
- **Neither**: Regular story, normal queue, full planning
- **Hotfix only**: Urgent but complex, dedicated coder, requires planning
- **Express only**: Simple but not urgent, normal queue, skips planning
- **Both**: Urgent and simple, dedicated coder, skips planning

### Examples

| User Request | Hotfix? | Express? | Flow |
|--------------|---------|----------|------|
| "Add user authentication system" | No | No | Normal queue → Planning → Coding |
| "Update the README with new API docs" | No | Yes | Normal queue → Coding (skip planning) |
| "URGENT: Fix the login button - it's broken in production" | Yes | Yes | Hotfix coder → Coding (skip planning) |
| "URGENT: Refactor the payment flow - causing issues" | Yes | No | Hotfix coder → Planning → Coding |

## Architecture

### System Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                             PM                                  │
│  ┌─────────────┐                                                │
│  │ AWAIT_USER  │◀──────────────────────────────────────────┐    │
│  │             │                                           │    │
│  │ Triages:    │                                           │    │
│  │ - Hotfix?   │                                           │    │
│  │ - Feature?  │                                           │    │
│  └──────┬──────┘                                           │    │
│         │                                                  │    │
│         ├── Feature ──► Normal interview flow              │    │
│         │                                                  │    │
│         └── Hotfix ──► Send HOTFIX request to architect    │    │
│                              │                             │    │
└──────────────────────────────┼─────────────────────────────┼────┘
                               │                             │
                               ▼                             │
┌──────────────────────────────────────────────────────────────────┐
│                          ARCHITECT                               │
│                                                                  │
│  ┌─────────────┐         ┌─────────────┐                        │
│  │   WAITING   │────────▶│   REQUEST   │                        │
│  └─────────────┘         └──────┬──────┘                        │
│                                 │                                │
│                    Examines request type:                        │
│                    ┌────────────┼────────────┐                   │
│                    │            │            │                   │
│                    ▼            ▼            ▼                   │
│              SPEC_REVIEW    HOTFIX     QUESTION/                 │
│              (multi-turn)  (single)    CODE_REVIEW               │
│                    │            │            │                   │
│                    │            │            │                   │
│                    ▼            ▼            │                   │
│              submit_stories  submit_stories  │                   │
│              (to queue)     (validates deps, │                   │
│                    │         direct dispatch)│                   │
│                    │            │            │                   │
│                    ▼            │            │                   │
│              DISPATCHING        │            │                   │
│                    │            │            │                   │
│                    └────────────┼────────────┘                   │
│                                 │                                │
│                                 ▼                                │
│                           MONITORING ─────────────────────────▶  │
│                                        (notify PM on complete)   │
└─────────────────────────────────────────┬───────────────────────┘
                                          │
                    ┌─────────────────────┼─────────────────────┐
                    │                     │                     │
                    ▼                     ▼                     ▼
            ┌─────────────┐       ┌─────────────┐       ┌─────────────┐
            │  coder-001  │       │  coder-002  │       │ hotfix-001  │
            │  (normal)   │       │  (normal)   │       │ (dedicated) │
            │             │       │             │       │             │
            │ From queue  │       │ From queue  │       │ Hotfixes    │
            │ via         │       │ via         │       │ only, own   │
            │ DISPATCHING │       │ DISPATCHING │       │ queue       │
            └─────────────┘       └─────────────┘       └─────────────┘
```

### PM State Machine Changes

#### Current Flow (After Spec Approval)
```
AWAIT_ARCHITECT → WAITING (clears context, user locked out)
```

#### New Flow (After Spec Approval)
```
AWAIT_ARCHITECT → AWAIT_USER (keeps context, stays engaged for tweaks)
```

A "user" message is injected into the PM's context to inform it of the approval:
> "The specification has been approved by the architect and submitted for development. Please inform the user and let them know you'll notify them when there's a demo ready or when development completes. Also let them know they can request tweaks or changes in the meantime."

The PM then generates an appropriate response to the user based on this context.

#### PM Triage in AWAIT_USER

When user sends a message, PM classifies:

1. **New Feature Request** → Continue normal interview flow (eventually new spec)
2. **Hotfix/Tweak Request** → Send HOTFIX request to architect

Classification criteria:
- User explicitly says "quick fix", "hotfix", "tweak", "small change"
- Request is clearly scoped to existing functionality
- No new features or architectural changes implied

### Architect Request Handling

#### HOTFIX Request Type (New)

When architect receives a HOTFIX request in REQUEST state:

1. **Single-turn toolloop** assessing:
   - Express eligibility (complexity analysis)
   - Story generation (title, content, requirements)
   - **Prompt emphasis**: Keep it simple, avoid over-engineering, minimize dependencies

2. **Call `submit_stories`** with the generated story

3. **`submit_stories` validates:**
   - Dependencies are either empty OR all complete
   - If validation fails → `needs_changes` response back to PM with explanation
   - PM can then revise the request or inform user of the issue

4. **Direct dispatch** to hotfix coder (bypasses normal queue)

5. **Return to MONITORING**

**Note**: PM transitions to `AWAIT_ARCHITECT` after submitting a hotfix request, same as spec submission. This allows the architect to send `needs_changes` if the hotfix can't be processed as requested.

#### Express Assessment Criteria

The architect determines `express=true` if ALL of these are true:
- Single file or 2-3 closely related files
- Change is well-defined (not exploratory)
- No architectural decisions needed
- No new dependencies required
- Estimated < 50 lines of changes

This assessment applies to:
- HOTFIX requests (new)
- SPEC_REVIEW story generation (enhancement to existing)

### Hotfix Coder

#### Setup

- Dedicated coder with ID `hotfix-001`
- Always created at startup (hotfix mode is always enabled)
- Receives work directly from architect (not via DISPATCHING)
- Has its own hotfix queue (blocks on this queue like normal coders block on theirs)
- Same state machine as normal coders
- Uses minimal resources when in WAITING state

#### Constraints

- `MaxCoders` must be >= 2 (required, not optional)
- Normal coders: `coder-001` through `coder-N`
- Hotfix coder: `hotfix-001` (dedicated, separate from MaxCoders count)
- Example: `MaxCoders=2` → `coder-001`, `coder-002` (normal), `hotfix-001` (dedicated)

### `submit_stories` Tool Enhancement

```go
type SubmitStoriesInput struct {
    Stories []StoryInput `json:"stories"`
}

type StoryInput struct {
    ID           string   `json:"id"`
    Title        string   `json:"title"`
    Content      string   `json:"content"`
    Express      bool     `json:"express"`       // LLM-determined
    DependsOn    []string `json:"depends_on"`
    StoryType    string   `json:"story_type"`
}

// Tool execution (pseudocode)
func Execute(ctx, input) {
    isHotfix := ctx.RequestType == "HOTFIX"  // Known from request context

    for _, story := range input.Stories {
        if isHotfix {
            // Validate dependencies are complete
            for _, depID := range story.DependsOn {
                if !isStoryComplete(depID) {
                    return error("hotfix depends on incomplete story: " + depID)
                }
            }
            // Dispatch directly to hotfix coder
            dispatchToHotfixCoder(story)
        } else {
            // Add to queue for normal DISPATCHING
            queue.AddStory(story)
        }
    }
}
```

## Implementation Phases

### Phase 1: PM Stays Engaged
- Modify `handleArchitectResult()` in `pkg/pm/driver.go`
- On spec approval: transition to `AWAIT_USER` instead of `WAITING`
- Keep context, inject status message

### Phase 2: PM Hotfix Triage
- Add classification logic to `handleAwaitUser()`
- Detect hotfix vs feature requests
- New helper: `classifyUserRequest(message) → "hotfix" | "feature"`

### Phase 3: HOTFIX Request Type
- Add `PayloadKindHotfix` to `pkg/proto/`
- PM sends HOTFIX requests to architect
- Architect REQUEST state handles HOTFIX type

### Phase 4: Express Assessment
- Add express assessment to HOTFIX toolloop
- Add express assessment to SPEC_REVIEW story generation
- Update story generation prompts

### Phase 5: Hotfix Coder Setup
- Enforce `MaxCoders >= 2` at startup (required)
- Create `hotfix-001` coder at startup (always)
- Modify agent factory to handle hotfix coder
- Create hotfix queue for dedicated coder

### Phase 6: `submit_stories` Enhancement
- Add hotfix validation (dependencies complete)
- Add direct dispatch to hotfix coder path
- Error handling bubbles to PM

### Phase 7: Notifications (Stretch)
- Architect notifies PM on story completion
- PM surfaces completion to user

## Post-MVP Enhancements

### Conflict Detection
File path analysis to detect when hotfix touches files being modified by in-progress stories.

### Conflict Resolution
Semantic conflict handling when hotfix intent conflicts with pending story intent:
- Example: Hotfix says "make button green", pending story says "make all buttons blue"
- Options: Story revision, dependency injection, parallel merge

### User Confirmation
Optional step before hotfix PR where user reviews changes in demo environment.

### Multiple Hotfix Coders
Config option to specify number of dedicated hotfix coders for high-volume scenarios.

### Hotfix-First Flow
Allow users to submit a hotfix request immediately (before any spec) if bootstrap requirements are already met. This would enable quick fixes to existing projects without going through the full interview flow. Requires:
- PM prompt restructuring to recognize when hotfix-first is appropriate
- Bootstrap detection to confirm project is ready for development
- Clear UX for users to indicate "I just need a quick fix"

## Configuration

### MVP
- Hotfix mode is always enabled (not configurable)
- `MaxCoders` must be >= 2 (enforced at startup)
- `hotfix-001` coder is always created in addition to normal coders

### Future Config (Post-MVP)
```json
{
  "hotfix": {
    "dedicated_coders": 1,
    "require_user_confirmation": false,
    "conflict_detection": true
  }
}
```

## Error Handling

### Hotfix Dependency Validation Failed
```
Error: Hotfix depends on incomplete story story-003
```
PM receives error and can:
- Ask user to wait for story-003
- Ask user to modify hotfix to remove dependency

### Hotfix Coder Busy
If `hotfix-001` is already working on a hotfix:
- New hotfix queues behind current one
- PM notified: "Hotfix queued behind current hotfix work"

### Express Assessment Disagreement
If user expects express but architect determines planning needed:
- Architect proceeds with planning
- PM can notify user: "This change is more complex than expected and requires planning"

## Testing Strategy

Production testing of this feature is expensive, so we prioritize comprehensive automated testing.

### Unit Tests
- PM triage classification (hotfix vs feature detection)
- `submit_stories` dependency validation logic
- Express assessment criteria evaluation
- PM state transitions (AWAIT_ARCHITECT → AWAIT_USER on approval)
- Context injection for approval messages
- Hotfix queue routing logic

### Integration Tests
- Full hotfix flow: PM → Architect → Hotfix Coder → Complete
- Error flow: Hotfix with incomplete dependencies → `needs_changes` → PM
- Mixed flow: Hotfix completes while normal stories in progress
- Express assessment: Simple hotfix gets `express=true`
- Complex hotfix: Gets `express=false`, goes through planning
- PM stays engaged: After spec approval, PM accepts hotfix requests
- Hotfix coder queue: Multiple hotfixes queue correctly

### Mock LLM Tests
- PM classification prompt produces correct hotfix/feature decisions
- Architect HOTFIX prompt produces minimal, simple stories
- Architect correctly assesses express eligibility
- `needs_changes` response when dependencies can't be satisfied

### E2E Tests
- User submits hotfix via WebUI chat
- Hotfix completes while feature stories continue in parallel
- Demo shows hotfix changes immediately
- User receives notification when hotfix completes
- Full cycle: Spec → Approval → Hotfix → Both complete
