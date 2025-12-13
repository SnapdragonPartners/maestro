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

### `submit_stories` Tool (Consolidated)

The `submit_stories` tool is used by both architect (for specs) and PM (for hotfixes). A single `hotfix` parameter controls routing:

```go
// Tool input schema
{
    "analysis": "string",       // Brief summary
    "platform": "string",       // e.g., "go", "python", "nodejs"
    "requirements": [...],      // Array of requirement objects
    "hotfix": false             // Optional: if true, routes to hotfix queue
}

// Requirement object
{
    "title": "string",
    "description": "string",
    "acceptance_criteria": ["..."],
    "dependencies": ["..."],    // Titles of dependent requirements
    "story_type": "app" | "devops"
}

// Tool execution (pseudocode)
func Execute(ctx, input) {
    isHotfix := input["hotfix"].(bool)
    signal := SignalStoriesSubmitted

    if isHotfix {
        signal = SignalHotfixSubmit
        // Architect will validate dependencies when processing HOTFIX request
    }

    return &ExecResult{
        Signal: signal,
        Data: input,  // Pass through for state machine processing
    }
}
```

**Note**: The tool itself just signals intent. Dependency validation and routing to hotfix coder happens in the architect's HOTFIX request handler.

## Implementation Phases

### Phase 1: PM Stays Engaged
- Modify `handleArchitectResult()` in `pkg/pm/driver.go`
- On spec approval: transition to `AWAIT_USER` instead of `WAITING`
- Keep context, inject status message

### Phase 2: PM Hotfix Triage
- PM triages user requests and decides hotfix vs feature
- For hotfixes: PM calls `submit_stories` with `hotfix=true` parameter
- This sends a HOTFIX REQUEST to architect with requirements
- `submit_stories` tool is consolidated - same tool for both specs and hotfixes

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

### Phase 7: PM Story Completion Notifications
- Architect notifies PM on story completion via RESPONSE message
- PM surfaces completion to user (hotfix or regular story)

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

---

## Detailed Implementation Plan

This section provides file-by-file implementation guidance.

### Phase 1: PM Stays Engaged After Approval

**Goal**: After spec approval, PM transitions to AWAIT_USER instead of WAITING, keeping context.

#### File: `pkg/pm/await_architect.go`

**Current behavior** (lines 48-59):
```go
if approvalResult.Status == proto.ApprovalStatusApproved {
    d.logger.Info("✅ Spec APPROVED by architect")
    // Clear draft spec and bootstrap requirements from state data
    d.SetStateData("draft_spec_markdown", nil)
    // ... more clearing ...
    return StateWaiting, nil  // <-- Returns to WAITING
}
```

**Change to**:
```go
if approvalResult.Status == proto.ApprovalStatusApproved {
    d.logger.Info("✅ Spec APPROVED by architect")

    // DON'T clear context - we're staying engaged for tweaks
    // Only clear spec-specific data, keep conversation context
    d.SetStateData("draft_spec_markdown", nil)
    d.SetStateData("spec_metadata", nil)
    // Keep StateKeyBootstrapRequirements - project is bootstrapped

    // Inject user message to inform PM of approval
    d.contextManager.AddMessage("user",
        "The specification has been approved by the architect and submitted for development. "+
        "Please inform the user and let them know you'll notify them when there's a demo ready "+
        "or when development completes. Also let them know they can request tweaks or changes in the meantime.")

    // Stay engaged - transition to WORKING so PM generates response
    return StateWorking, nil
}
```

#### File: `pkg/pm/states.go`

**Add transition** (around line 57):
```go
StateAwaitArchitect: {
    StateAwaitArchitect,
    StateWorking,    // Architect provides feedback OR approval (NEW: approval goes to WORKING)
    // Remove: StateWaiting - no longer transition directly to waiting on approval
    proto.StateError,
    proto.StateDone,
},
```

#### Tests: `pkg/pm/await_architect_test.go` (new file)

```go
func TestHandleAwaitArchitect_Approval_TransitionsToWorking(t *testing.T)
func TestHandleAwaitArchitect_Approval_InjectsUserMessage(t *testing.T)
func TestHandleAwaitArchitect_NeedsChanges_TransitionsToWorking(t *testing.T)
```

---

### Phase 2: PM Hotfix Triage

**Goal**: PM triages user requests and submits hotfixes via `submit_stories(hotfix=true)`.

**IMPLEMENTED** - Changes made:

#### File: `pkg/tools/submit_stories.go`

Added `hotfix` parameter to consolidated tool:
```go
// Added to InputSchema.Properties
"hotfix": {
    Type:        "boolean",
    Description: "If true, routes stories to the dedicated hotfix queue (generally 1 story for hotfixes)",
}

// In Exec()
isHotfix := false
if hotfix, ok := args["hotfix"].(bool); ok {
    isHotfix = hotfix
}

signal := SignalStoriesSubmitted
if isHotfix {
    signal = SignalHotfixSubmit
}
```

Also removed `estimated_points` from schema to reduce model overload.

#### File: `pkg/tools/constants.go`

- Removed `ToolHotfixSubmit` constant (consolidated into `submit_stories`)
- Updated `PMTools` to use `ToolSubmitStories` instead

#### File: `pkg/pm/working.go`

Added `SignalHotfixSubmit` handling:
```go
case tools.SignalHotfixSubmit:
    // Extract hotfix stories data
    analysis, _ := effectData["analysis"].(string)
    platform, _ := effectData["platform"].(string)
    requirements, _ := effectData["requirements"].([]any)

    // Store in state and send HOTFIX request to architect
    d.SetStateData("hotfix_analysis", analysis)
    d.SetStateData("hotfix_platform", platform)
    d.SetStateData("hotfix_requirements", requirements)
    return SignalHotfixSubmit, nil
```

#### File: `pkg/proto/unified_protocol.go`

Updated `HotfixRequestPayload` to use full requirements format:
```go
type HotfixRequestPayload struct {
    Analysis     string            `json:"analysis"`
    Platform     string            `json:"platform"`
    Requirements []any             `json:"requirements"`  // Same format as submit_stories
    Urgency      string            `json:"urgency,omitempty"`
    Metadata     map[string]string `json:"metadata,omitempty"`
}
```

#### File: `pkg/templates/architect/spec_analysis.tpl.md`

Removed `estimated_points` from requirements documentation.

---

### Phase 3: HOTFIX Request Type

**Goal**: Add protocol support for HOTFIX requests and architect handling.

**PARTIALLY IMPLEMENTED** - Protocol changes done, architect handling still needed.

#### Completed: `pkg/proto/payload.go`

Added payload kind:
```go
PayloadKindHotfixRequest PayloadKind = "hotfix_request"
```

Added `NewHotfixRequestPayload()` and `ExtractHotfixRequest()` functions.

#### Completed: `pkg/proto/unified_protocol.go`

Updated `HotfixRequestPayload` structure (see Phase 2 changes).

#### TODO: `pkg/architect/request.go`

Add hotfix handling in the switch statement:
```go
switch requestKind {
case proto.RequestKindQuestion:
    response, err = d.handleIterativeQuestion(ctx, requestMsg)
case proto.RequestKindApproval:
    response, err = d.handleApprovalRequest(ctx, requestMsg)
case proto.RequestKindHotfix:  // NEW
    response, err = d.handleHotfixRequest(ctx, requestMsg)
// ... existing cases
}
```

#### TODO: `pkg/architect/request_hotfix.go` (new file)

```go
package architect

// handleHotfixRequest processes a HOTFIX request from PM.
// The requirements are already structured (from PM's submit_stories call).
// Architect assesses express eligibility and dispatches to hotfix coder.
func (d *Driver) handleHotfixRequest(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
    // Extract hotfix payload (has requirements array)
    typedPayload := requestMsg.GetTypedPayload()
    hotfixPayload, err := typedPayload.ExtractHotfixRequest()
    if err != nil {
        return nil, err
    }

    d.logger.Info("🔧 Processing hotfix request: %d requirements for %s",
        len(hotfixPayload.Requirements), hotfixPayload.Platform)

    // For each requirement:
    // 1. Validate dependencies are complete
    // 2. Assess express eligibility
    // 3. Dispatch to hotfix coder

    // If any validation fails, return needs_changes to PM
    // Otherwise, return approval to PM
}
```

#### Tests: `pkg/architect/request_hotfix_test.go`

```go
func TestHandleHotfixRequest_SimpleHotfix_Express(t *testing.T)
func TestHandleHotfixRequest_ComplexHotfix_NotExpress(t *testing.T)
func TestHandleHotfixRequest_InvalidDependency_NeedsChanges(t *testing.T)
```

---

### Phase 4: Express Assessment for All Stories

**Goal**: Add express flag to story generation for both specs and hotfixes.

#### File: `pkg/tools/submit_stories.go`

**Already exists** - need to add `express` field to story input schema:

```go
// Update the JSON schema for the tool
"express": {
    "type": "boolean",
    "description": "If true, story skips planning and goes directly to coding. Use for simple, well-defined changes."
}
```

**Update Execute** to extract and pass express flag:
```go
express := safeAssert[bool](storyData["express"], false)
// Pass to queue.AddStory or dispatcher
```

#### File: `pkg/architect/queue.go`

**Update AddStory** to accept express parameter:
```go
func (q *StoryQueue) AddStory(story *QueuedStory) error {
    // story.Express is already set from input
    // ... existing logic
}
```

**Update QueuedStory struct**:
```go
type QueuedStory struct {
    // ... existing fields
    Express bool `json:"express"` // If true, skip planning
}
```

#### File: `pkg/architect/dispatching.go`

**Update sendStoryToDispatcher** (around line 115-131):
```go
// Add express flag to payload
payloadData[proto.KeyExpress] = story.Express
```

**Note**: `proto.KeyExpress` may need to be added to `pkg/proto/message.go`.

#### File: `pkg/proto/message.go`

**Add key constant**:
```go
KeyExpress = "express"
```

#### File: `pkg/templates/architect/` (prompt templates)

**Update story generation prompts** to include express assessment criteria.

---

### Phase 5: Hotfix Coder Setup

**Goal**: Create dedicated hotfix-001 coder at startup.

#### File: `pkg/config/config.go`

**Update validation** (already changed MaxCoders default to 3):
```go
if agents.MaxCoders < 2 {
    return fmt.Errorf("max_coders must be >= 2 (required for hotfix mode)")
}
```

#### File: `internal/factory/agent_factory.go`

**Add hotfix coder creation**:
```go
func (f *AgentFactory) CreateCoders(ctx context.Context, count int) ([]*coder.Driver, error) {
    coders := make([]*coder.Driver, 0, count+1) // +1 for hotfix coder

    // Create normal coders
    for i := 1; i <= count; i++ {
        coderID := fmt.Sprintf("coder-%03d", i)
        c, err := f.createCoder(ctx, coderID, false /* isHotfix */)
        if err != nil {
            return nil, err
        }
        coders = append(coders, c)
    }

    // Create hotfix coder
    hotfixCoder, err := f.createCoder(ctx, "hotfix-001", true /* isHotfix */)
    if err != nil {
        return nil, err
    }
    coders = append(coders, hotfixCoder)

    return coders, nil
}
```

#### File: `pkg/dispatch/dispatcher.go`

**Add hotfix routing**:
```go
// Add hotfix channel
hotfixStoryCh chan *proto.AgentMsg

// In Route():
if msg.IsHotfix() {
    return d.hotfixStoryCh
}
```

#### File: `pkg/coder/driver.go`

**Add hotfix flag** (optional, for logging):
```go
type Driver struct {
    // ... existing fields
    isHotfix bool // True if this is the dedicated hotfix coder
}
```

---

### Phase 6: submit_stories Enhancement

**Goal**: Validate hotfix dependencies and dispatch directly.

#### File: `pkg/tools/submit_stories.go`

**Add hotfix mode**:
```go
func (t *SubmitStoriesTool) Execute(ctx context.Context, input map[string]any) (any, error) {
    isHotfix := t.isHotfixContext(ctx) // Check if this is a hotfix request

    stories := extractStories(input)

    for _, story := range stories {
        if isHotfix {
            // Validate dependencies are complete
            if err := t.validateHotfixDependencies(story); err != nil {
                return nil, err // This bubbles back as needs_changes
            }

            // Dispatch directly to hotfix coder (bypass queue)
            if err := t.dispatchToHotfixCoder(ctx, story); err != nil {
                return nil, err
            }
        } else {
            // Normal: add to queue
            if err := t.queue.AddStory(story); err != nil {
                return nil, err
            }
        }
    }

    return map[string]any{"success": true}, nil
}

func (t *SubmitStoriesTool) validateHotfixDependencies(story *StoryInput) error {
    for _, depID := range story.DependsOn {
        depStory, exists := t.queue.GetStory(depID)
        if !exists {
            return fmt.Errorf("hotfix depends on unknown story: %s", depID)
        }
        if depStory.GetStatus() != StatusDone {
            return fmt.Errorf("hotfix depends on incomplete story %s (status: %s) - "+
                "please wait for it to complete or revise the hotfix to remove this dependency",
                depID, depStory.GetStatus())
        }
    }
    return nil
}
```

---

### Phase 7: Notifications (Stretch)

**Goal**: Notify PM when stories complete.

#### File: `pkg/architect/request.go`

**In handleWorkAccepted** (around line 517):
```go
func (d *Driver) handleWorkAccepted(ctx context.Context, storyID, acceptanceType string, ...) {
    // ... existing logic ...

    // NEW: Notify PM of completion
    if d.pmNotificationCh != nil {
        notification := &PMNotification{
            Type:    "story_complete",
            StoryID: storyID,
            Message: fmt.Sprintf("Story %s has been completed", storyID),
        }
        select {
        case d.pmNotificationCh <- notification:
        default:
            d.logger.Warn("PM notification channel full, dropping notification")
        }
    }
}
```

---

### Implementation Status Summary

| Phase | Status | Key Changes |
|-------|--------|-------------|
| **1. PM Stays Engaged** | ✅ DONE | `await_architect.go` → WORKING on approval |
| **2. PM Hotfix Triage** | ✅ DONE | `submit_stories(hotfix=true)` consolidated tool |
| **3. HOTFIX Request Type** | ✅ DONE | Protocol + architect handler complete |
| **4. Express Assessment** | ✅ DONE | Keys in proto, wired through dispatcher to coder |
| **5. Hotfix Coder Setup** | ✅ DONE | Dedicated hotfix-001 coder with separate channel |
| **6. submit_stories Enhancement** | ✅ DONE | Dependency validation in request_hotfix.go (Phase 3) |
| **7. Notifications** | ✅ DONE | PM notified on story completion |

### Phase 4 Completion Notes

Express flag is now fully wired:
- `pkg/proto/message.go:104-105` - Added `KeyExpress` and `KeyIsHotfix` constants
- `pkg/architect/dispatching.go:119-120` - Passes `express` and `is_hotfix` in story payload
- `pkg/coder/coder_fsm.go:46-47` - Constants for extracting express and hotfix flags
- `pkg/coder/waiting.go:101-129` - Extracts both flags from payload, stores in state
- `pkg/coder/setup.go:86-120` - If express, transitions SETUP→CODING (skips PLANNING)

### Phase 5 Completion Notes

Dedicated hotfix coder with separate channel routing:
- `pkg/dispatch/dispatcher.go` - Added `hotfixStoryCh` for dedicated hotfix routing
- Dispatcher `Attach()` routes `hotfix-*` coders to `hotfixStoryCh`
- Dispatcher `processMessage()` routes stories with `is_hotfix=true` to `hotfixStoryCh`
- `cmd/maestro/flows.go` - Both `BootstrapFlow` and `OrchestratorFlow` create `hotfix-001`
- Hotfix coder receives stories on dedicated channel, runs same state machine as normal coders

### Phase 6 Note

Dependency validation was implemented as part of Phase 3 in `request_hotfix.go:60-82`:
- Validates each dependency exists using `FindStoryByTitle()`
- Returns `needs_changes` if dependency unknown or incomplete
- Error messages guide PM to either wait or remove dependency

### Phase 7 Completion Notes

PM story completion notifications:
- `pkg/proto/payload.go:44,289-318` - Added `PayloadKindStoryComplete` and `StoryCompletePayload` struct
- `pkg/architect/request.go` - Added `notifyPMOfCompletion()` method
- Called from `handleWorkAccepted()` when coder work is approved
- Notification includes: story_id, title, is_hotfix flag, summary, pr_id, timestamp
- Uses existing effect pattern (`SendMessageEffect`) to send RESPONSE to PM
- PM can surface completion info to user via chat

---

## Phase 8: Unified Hotfix Entry Point (Consolidation)

**Date**: December 2024

### Problem Statement

The original implementation created two separate paths for hotfixes:

1. **Path A**: `submit_stories(hotfix=true)` → `SignalHotfixSubmit` → PM stores in dedicated state vars → `sendHotfixRequest()` → architect
2. **Path B**: `spec_submit(hotfix=true)` → `SignalSpecPreview` → PM stores in `user_spec_md` → PREVIEW → normal approval flow

Path A bypassed user preview entirely and used dedicated state variables (`hotfix_analysis`, `hotfix_platform`, `hotfix_requirements`). Path B went through preview but then used the normal `ApprovalRequestPayload`, meaning hotfixes would wait in queue like regular specs instead of "jumping the line."

### Solution: Unified Flow

Consolidate to a single entry point (`spec_submit(hotfix=true)`) that:
1. Goes through user preview (user can see what they're submitting)
2. Routes to `sendHotfixRequest()` on submission (jumps the line to hotfix coder)
3. Uses canonical state variable (`user_spec_md`) instead of dedicated hotfix vars

### New Hotfix Flow

```
User requests hotfix
        │
        ▼
PM calls spec_submit(hotfix=true)
        │
        ▼
SignalSpecPreview with is_hotfix=true
        │
        ▼
PM stores:
  - user_spec_md = hotfix content
  - is_hotfix = true (NEW state var)
        │
        ▼
PM transitions to PREVIEW
        │
        ▼
User reviews hotfix in WebUI
        │
        ▼
User clicks "Submit for Development"
        │
        ▼
PreviewAction checks is_hotfix flag
        │
        ├── is_hotfix=true ──► sendHotfixRequest() ──► HotfixRequestPayload to architect
        │                                                      │
        │                                                      ▼
        │                                              Architect validates deps
        │                                                      │
        │                                                      ▼
        │                                              Direct dispatch to hotfix-001
        │
        └── is_hotfix=false ─► sendSpecApprovalRequest() ──► Normal approval flow
```

### Implementation Changes

#### 1. Add `StateKeyIsHotfix` State Variable

**File**: `pkg/pm/driver.go`

```go
// StateKeyIsHotfix indicates the current spec submission is a hotfix.
// Set when spec_submit(hotfix=true) is called, cleared on approval.
StateKeyIsHotfix = "is_hotfix"
```

#### 2. Store `is_hotfix` Flag in WORKING State

**File**: `pkg/pm/working.go` (SignalSpecPreview case)

```go
case tools.SignalSpecPreview:
    // ... existing extraction ...
    isHotfix := utils.GetMapFieldOr[bool](effectData, "is_hotfix", false)

    // Store specs using canonical state keys
    d.SetStateData(StateKeyUserSpecMd, userSpec)
    d.SetStateData(StateKeySpecMetadata, metadata)
    d.SetStateData(StateKeyIsHotfix, isHotfix)  // NEW: Store hotfix flag
    // ...
```

#### 3. Route Based on `is_hotfix` in PreviewAction

**File**: `pkg/pm/driver.go` (PreviewAction method)

```go
case PreviewActionSubmit:
    userSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
    if userSpec == "" {
        return fmt.Errorf("no spec to submit - user_spec_md is empty")
    }

    // Check if this is a hotfix submission
    isHotfix := utils.GetStateValueOr[bool](d.BaseStateMachine, StateKeyIsHotfix, false)

    var err error
    if isHotfix {
        // Hotfixes jump the line - send directly to architect's hotfix handler
        err = d.sendHotfixRequest(ctx)
    } else {
        // Normal specs go through approval flow
        err = d.sendSpecApprovalRequest(ctx)
    }
    // ...
```

#### 4. Modify `sendHotfixRequest()` to Use `user_spec_md`

**File**: `pkg/pm/working.go`

The function should read from `user_spec_md` instead of dedicated hotfix state vars:

```go
func (d *Driver) sendHotfixRequest(_ context.Context) error {
    // Get hotfix content from canonical state var
    userSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
    if userSpec == "" {
        return fmt.Errorf("no user_spec_md found in state for hotfix")
    }

    // Get platform from bootstrap params or detected platform
    platform := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyDetectedPlatform, "unknown")

    // Create hotfix request payload
    // Note: Architect will parse the markdown into requirements
    hotfixPayload := &proto.HotfixRequestPayload{
        Analysis:     "Hotfix request from user",
        Platform:     platform,
        Requirements: []any{
            map[string]any{
                "title":       "Hotfix",
                "description": userSpec,
                "story_type":  "app",
            },
        },
        Urgency: "normal",
    }
    // ... rest of function unchanged ...
}
```

#### 5. Remove Dead Code

**Remove from `pkg/pm/driver.go`**:
```go
// DELETE these state keys:
StateKeyHotfixAnalysis     = "hotfix_analysis"
StateKeyHotfixPlatform     = "hotfix_platform"
StateKeyHotfixRequirements = "hotfix_requirements"
StateKeyPendingRequestID   = "pending_request_id"  // Dead code - never used for correlation
```

**Remove from `pkg/pm/working.go`**:
```go
// DELETE the SignalHotfixSubmit case (lines ~375-394)
case tools.SignalHotfixSubmit:
    // ... all of this ...
```

**Remove from `pkg/tools/constants.go` and `pkg/tools/mcp.go`**:
```go
// DELETE:
SignalHotfixSubmit = "HOTFIX_SUBMIT"
```

**Remove from `pkg/tools/submit_stories.go`**:
- Remove `hotfix` parameter from tool schema
- Remove hotfix signal logic

**Remove `submit_stories` from PM tools**:
- PM no longer uses this tool (only architect does for spec analysis)
- Update PM prompts to not reference `submit_stories`

### State Variable Cleanup on Approval

When architect approves a spec or hotfix, the following state variables must be cleared in `handleAwaitArchitect()`:

```go
// Clear all submission-related state data
d.SetStateData(StateKeyUserSpecMd, nil)
d.SetStateData(StateKeyBootstrapSpecMd, nil)
d.SetStateData(StateKeySpecMetadata, nil)
d.SetStateData(StateKeySpecUploaded, nil)
d.SetStateData(StateKeyBootstrapRequirements, nil)
d.SetStateData(StateKeyDetectedPlatform, nil)
d.SetStateData(StateKeyBootstrapParams, nil)
d.SetStateData(StateKeyIsHotfix, nil)  // NEW: Clear hotfix flag
d.SetStateData(StateKeyTurnCount, nil) // Reset turn count (no churning occurred)

// Mark development as in flight
d.SetStateData(StateKeyInFlight, true)
```

### PM State Variables Reference

| State Key | Purpose | Lifecycle |
|-----------|---------|-----------|
| `StateKeyHasRepository` | Has git repo access | Session-persistent |
| `StateKeyUserExpertise` | User expertise level | Session-persistent |
| `StateKeyInFlight` | Dev in progress | Set true on approval, false on all-complete |
| `StateKeyUserSpecMd` | User's spec/hotfix markdown | Cleared on approval |
| `StateKeyBootstrapSpecMd` | Infrastructure spec | Cleared on approval |
| `StateKeySpecMetadata` | Spec metadata | Cleared on approval |
| `StateKeySpecUploaded` | Spec was uploaded | Cleared on approval |
| `StateKeyBootstrapRequirements` | Bootstrap requirements | Cleared on approval |
| `StateKeyDetectedPlatform` | Detected platform | Cleared on approval |
| `StateKeyBootstrapParams` | Bootstrap params | Cleared on approval |
| `StateKeyIsHotfix` | Current submission is hotfix | Cleared on approval |
| `StateKeyTurnCount` | Conversation turns | Cleared on approval |

### Files to Modify

| File | Changes |
|------|---------|
| `pkg/pm/driver.go` | Add `StateKeyIsHotfix`, remove hotfix state keys, modify `PreviewAction` |
| `pkg/pm/working.go` | Store `is_hotfix` flag, remove `SignalHotfixSubmit` case, modify `sendHotfixRequest()` |
| `pkg/pm/await_architect.go` | Clear additional state vars on approval |
| `pkg/tools/constants.go` | Remove `SignalHotfixSubmit` |
| `pkg/tools/mcp.go` | Remove `SignalHotfixSubmit` |
| `pkg/tools/submit_stories.go` | Remove `hotfix` parameter |
| PM prompt templates | Remove `submit_stories` references |

### What Stays the Same

- `pkg/architect/request_hotfix.go` - Architect's hotfix handler (validates deps, dispatches to hotfix coder)
- `HotfixRequestPayload` protocol type
- `spec_submit(hotfix=true)` tool behavior
- Hotfix coder (`hotfix-001`) and dedicated channel routing
- Express flag handling for hotfixes

### Acceptance Criteria

1. [ ] `spec_submit(hotfix=true)` stores `is_hotfix=true` in PM state
2. [ ] User sees hotfix preview before submission
3. [ ] "Submit for Development" routes hotfixes to `sendHotfixRequest()`
4. [ ] Hotfixes jump the queue and go to `hotfix-001` coder
5. [ ] All spec-related state variables cleared on approval (including `is_hotfix`, `turn_count`)
6. [ ] `submit_stories` tool removed from PM (architect-only)
7. [ ] Dead code removed (`SignalHotfixSubmit`, dedicated hotfix state vars)
