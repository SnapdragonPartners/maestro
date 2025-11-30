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

**Goal**: PM classifies user messages as hotfix vs feature requests.

#### File: `pkg/pm/working.go`

**Add hotfix detection** in the main toolloop prompt or as a tool:

Option A: **Add `classify_request` tool** that PM calls to classify user input
- Returns: `{"type": "hotfix" | "feature", "confidence": 0.0-1.0}`
- PM then routes accordingly

Option B: **Modify `spec_submit` tool** to accept a `type` parameter
- `type: "spec"` → Normal spec submission
- `type: "hotfix"` → Hotfix submission

**Recommended**: Option B is simpler - extend existing `spec_submit` tool.

#### File: `pkg/tools/spec_submit.go`

**Add hotfix mode**:
```go
type SpecSubmitInput struct {
    Markdown    string `json:"markdown"`
    Type        string `json:"type"`        // NEW: "spec" | "hotfix"
    Description string `json:"description"` // NEW: For hotfix, brief description
}

func (t *SpecSubmitTool) Execute(ctx context.Context, input map[string]any) (any, error) {
    submitType := safeAssert[string](input["type"], "spec")

    if submitType == "hotfix" {
        // Send HOTFIX request to architect
        return t.submitHotfix(ctx, input)
    }

    // Existing spec submission logic
    return t.submitSpec(ctx, input)
}
```

#### File: `pkg/templates/pm/` (prompt templates)

**Update PM system prompt** to explain hotfix option:
```markdown
## After Spec Approval

Once a spec is approved and development begins, you can help the user with:
1. **Tweaks/Hotfixes**: Small, urgent changes (use spec_submit with type="hotfix")
2. **New Features**: Start a new interview for substantial new functionality
```

#### Tests: `pkg/pm/working_test.go`

```go
func TestPM_ClassifiesHotfixRequest(t *testing.T)
func TestPM_ClassifiesFeatureRequest(t *testing.T)
func TestPM_SubmitsHotfixToArchitect(t *testing.T)
```

---

### Phase 3: HOTFIX Request Type

**Goal**: Add protocol support for HOTFIX requests.

#### File: `pkg/proto/payload.go`

**Add payload kind** (around line 38):
```go
// Request types
PayloadKindHotfixRequest PayloadKind = "hotfix_request"
```

**Add request kind** (around line 46 in request_kinds.go or similar):
```go
RequestKindHotfix RequestKind = "hotfix"
```

#### File: `pkg/proto/hotfix.go` (new file)

```go
package proto

// HotfixRequestPayload contains data for a hotfix request from PM to architect.
type HotfixRequestPayload struct {
    Description string            `json:"description"` // Brief description of the hotfix
    Context     string            `json:"context"`     // Any relevant context
    Metadata    map[string]string `json:"metadata"`    // Additional metadata
}

// NewHotfixRequestPayload creates a new hotfix request payload.
func NewHotfixRequestPayload(description, context string) *TypedPayload {
    return &TypedPayload{
        Kind: PayloadKindHotfixRequest,
        Data: map[string]any{
            "description": description,
            "context":     context,
        },
    }
}

// ExtractHotfixRequest extracts hotfix request data from a typed payload.
func (p *TypedPayload) ExtractHotfixRequest() (*HotfixRequestPayload, error) {
    if p.Kind != PayloadKindHotfixRequest {
        return nil, fmt.Errorf("expected hotfix_request payload, got %s", p.Kind)
    }

    return &HotfixRequestPayload{
        Description: safeAssert[string](p.Data["description"], ""),
        Context:     safeAssert[string](p.Data["context"], ""),
    }, nil
}
```

#### File: `pkg/architect/request.go`

**Add hotfix handling** (around line 61 in the switch statement):
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

#### File: `pkg/architect/request_hotfix.go` (new file)

```go
package architect

// handleHotfixRequest processes a HOTFIX request from PM.
// Uses single-turn toolloop to assess complexity and generate story.
func (d *Driver) handleHotfixRequest(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
    // Extract hotfix description
    typedPayload := requestMsg.GetTypedPayload()
    hotfixPayload, err := typedPayload.ExtractHotfixRequest()
    if err != nil {
        return nil, err
    }

    d.logger.Info("🔧 Processing hotfix request: %s", hotfixPayload.Description)

    // Create context manager for hotfix (fresh context)
    cm := contextmgr.NewContextManager()

    // Build hotfix assessment prompt
    prompt := d.generateHotfixPrompt(hotfixPayload)
    cm.AddMessage("user", prompt)

    // Create tool provider with submit_stories
    toolProvider := d.createHotfixToolProvider()

    // Run single-turn toolloop
    out := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[HotfixResult]{
        ContextManager: cm,
        GeneralTools:   []tools.Tool{},  // No exploration tools for hotfix
        TerminalTool:   submitStoriesTool,
        MaxIterations:  3,
        SingleTurn:     true,
        AgentID:        d.GetAgentID(),
    })

    // Handle outcome - either success or needs_changes
    if out.Kind != toolloop.OutcomeProcessEffect {
        // Return needs_changes to PM
        return d.buildHotfixNeedsChangesResponse(requestMsg, out.Err)
    }

    // Hotfix story was submitted successfully
    d.logger.Info("✅ Hotfix story created and dispatched")

    // Return approval to PM
    return d.buildHotfixApprovalResponse(requestMsg)
}

func (d *Driver) generateHotfixPrompt(payload *proto.HotfixRequestPayload) string {
    return fmt.Sprintf(`# Hotfix Request

## Description
%s

## Context
%s

## Instructions

Create a SINGLE story for this hotfix. Keep it as simple as possible:
- Minimize dependencies (prefer none)
- Minimize scope (single file if possible)
- Avoid over-engineering

Assess if this is EXPRESS (skip planning) based on:
- Single file or 2-3 closely related files → express=true
- Well-defined change, not exploratory → express=true
- No architectural decisions needed → express=true
- Estimated < 50 lines → express=true

Call submit_stories with your assessment.`, payload.Description, payload.Context)
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

### Implementation Order Summary

1. **Phase 1** (PM stays engaged) - ~2 hours
   - `pkg/pm/await_architect.go` - Change approval transition
   - `pkg/pm/states.go` - Update valid transitions
   - Tests

2. **Phase 2** (PM hotfix triage) - ~3 hours
   - `pkg/tools/spec_submit.go` - Add hotfix mode
   - PM prompt templates
   - Tests

3. **Phase 3** (HOTFIX request type) - ~4 hours
   - `pkg/proto/payload.go` - Add payload kind
   - `pkg/proto/hotfix.go` - New file
   - `pkg/architect/request.go` - Add routing
   - `pkg/architect/request_hotfix.go` - New file
   - Tests

4. **Phase 4** (Express assessment) - ~2 hours
   - `pkg/tools/submit_stories.go` - Add express field
   - `pkg/architect/queue.go` - Store express flag
   - `pkg/architect/dispatching.go` - Pass express to coder
   - `pkg/proto/message.go` - Add KeyExpress
   - Tests

5. **Phase 5** (Hotfix coder) - ~3 hours
   - `pkg/config/config.go` - Validate MaxCoders >= 2
   - `internal/factory/agent_factory.go` - Create hotfix coder
   - `pkg/dispatch/dispatcher.go` - Add hotfix routing
   - Tests

6. **Phase 6** (submit_stories enhancement) - ~2 hours
   - `pkg/tools/submit_stories.go` - Hotfix validation and dispatch
   - Tests

7. **Phase 7** (Notifications) - ~2 hours (stretch)
   - `pkg/architect/request.go` - Add PM notifications
   - `pkg/pm/driver.go` - Handle notifications
   - Tests

**Total estimated effort**: ~18 hours

### Key Discovery: Express Already Implemented in Coder

The coder already fully supports express stories:
- `pkg/coder/coder_fsm.go:46` - `KeyExpress` constant defined
- `pkg/coder/waiting.go:101-117` - Extracts `express` flag from story payload
- `pkg/coder/setup.go:86-120` - If express, transitions SETUP→CODING (skips PLANNING)

We just need to ensure the flag flows through from architect to coder via the dispatcher.
