# Bootstrap as Spec 0

## Overview

Bootstrap infrastructure requirements (Dockerfile, Makefile, .gitignore, knowledge graph, etc.) should flow through the same spec pipeline as user feature specs rather than being special-cased and concatenated. Bootstrap becomes "Spec 0" — a separate spec submitted to the architect before the user feature spec, processed through the same review and story generation flow, and dispatched to coders while the PM interviews the user for Spec 1.

This design eliminates the current special-casing that bundles bootstrap requirements with user requirements in a single LLM call, and naturally extends to a FIFO spec queue where each spec's stories gate on all prior spec's stories.

## Goals

1. **Bootstrap requirements are implemented first** — as a separate spec (Spec 0) from templates, processed by the architect through its standard review + story generation pipeline
2. **User spec is broken into stories by the LLM** — with its own internal dependency graph; the only validation error is circular dependencies
3. **Dispatcher is spec-unaware** — it only looks at per-story `DependsOn` edges; ordering comes from dependency injection at story creation time
4. **Specs form a FIFO queue** — bootstrap Spec 0 must complete before user Spec 1 starts; this is enforced by injecting dependencies from all Spec N stories onto all Spec N-1 stories
5. **App vs devops story types are cosmetic** — ordering comes from spec FIFO, not from story type classification

## Current Flow (What Happens Today)

```
PM SETUP → detect bootstrap → WAITING
User starts interview → PM WORKING
PM gathers config (name, platform, git) → calls bootstrap tool → BOOTSTRAP_COMPLETE
PM context reset → interview continues → PM calls spec_submit
  └─ spec_submit bundles: user spec + bootstrap requirement IDs
PM PREVIEW → user approves → sendSpecApprovalRequest()
  └─ REQUEST bundles: user spec (Content) + bootstrap req IDs (BootstrapRequirements)
PM → AWAIT_ARCHITECT (blocks)
Architect receives REQUEST:
  1. RenderBootstrapSpec(reqIDs) → infrastructure markdown
  2. Concatenate: infrastructure + user spec → completeSpec
  3. Toolloop 1: LLM reviews concatenated spec
  4. Toolloop 2: LLM generates stories from concatenated spec
  5. loadStoriesFromSubmitResultData() → creates all stories at once
Architect → DISPATCHING → MONITORING → coders work
```

**Problems:**
- One LLM call must understand both infrastructure templates and user features
- The LLM can create cross-category dependency cycles
- Bootstrap work can't start until user interview completes
- `in_flight` flag is all-or-nothing — blocks second spec after first is accepted

## Proposed Flow

```
PM SETUP → detect bootstrap → WAITING
User starts interview → PM WORKING
PM gathers config from user → calls bootstrap tool → BOOTSTRAP_COMPLETE
PM handles BOOTSTRAP_COMPLETE:
  1. Re-detect bootstrap requirements (already done)
  2. If infrastructure items remain (Dockerfile, Makefile, etc.):
     a. Render bootstrap spec via RenderBootstrapSpec()
     b. Set StateKeyAwaitingSpecType = "bootstrap"
     c. Send as spec REQUEST to architect (Spec 0) — NO PREVIEW (skip UI)
     d. Transition to AWAIT_ARCHITECT (block)
  3. Architect processes bootstrap spec:
     - Review (with bootstrap guidance — see §D4 Template Guidance)
     - Generate stories
     - Load into queue (empty queue → no pre-existing gates)
     - Respond with APPROVED
  4. PM receives approval:
     - Checks StateKeyAwaitingSpecType == "bootstrap" → do NOT set in_flight
     - Return to WORKING
  5. PM continues user interview (bootstrap stories executing in parallel)

PM completes interview → spec_submit (NO bootstrap req IDs)
PM PREVIEW → user approves (normal user-facing preview)
  └─ Set StateKeyAwaitingSpecType = "user"
  └─ sendSpecApprovalRequest() with user spec only (Content)
PM → AWAIT_ARCHITECT (block)

Architect receives user spec (in MONITORING or WAITING state):
  - Review user spec
  - Generate user stories
  - loadStoriesFromSubmitResultData():
    └─ Pre-existing stories gate: ALL new stories depend on ALL bootstrap stories
  - Respond with APPROVED
PM receives approval:
  - Checks StateKeyAwaitingSpecType == "user" → set in_flight = true
  - DISPATCHING → bootstrap stories complete → user stories unblock → MONITORING
```

### "Config Already Complete" Case

If config is complete at interview start (project name, platform, git URL already in config.json) but infrastructure items are missing (Dockerfile, Makefile, etc.), the PM should send bootstrap Spec 0 immediately during `StartInterview()` rather than waiting for the bootstrap tool to be called.

```
PM StartInterview():
  - Detect bootstrap requirements
  - Config is complete, infrastructure items missing
  - Render bootstrap spec, send to architect
  - Transition to AWAIT_ARCHITECT
  - Architect processes → PM receives approval → WORKING
  - PM interviews user for feature spec (bootstrap stories executing in parallel)
```

### "No Infrastructure Bootstrap Needed" Case

If config is complete and no infrastructure items are missing, no Spec 0 is sent. The user spec is the only spec and the flow is identical to today. This path requires zero changes.

## Design Decisions

### D1: PM blocks in AWAIT_ARCHITECT for bootstrap spec (no PREVIEW)

The PM fully emulates the spec submission flow for bootstrap Spec 0, including blocking in `AWAIT_ARCHITECT`. However, bootstrap specs **skip the PREVIEW state entirely** — there is no user-facing preview UI for template-generated infrastructure. The PM submits directly to the architect and blocks.

Reasons for blocking:

- **No channel confusion**: If architect sends feedback/questions about bootstrap spec while PM is mid-interview, the interleaved messages would confuse both the LLM and the user
- **Simpler state management**: PM is either doing bootstrap or doing interview, never both simultaneously
- **Fast in practice**: Bootstrap specs are template-generated and straightforward; architect review should be quick
- **Consistent error handling**: NEEDS_CHANGES and error paths work identically to user specs

Reasons for skipping PREVIEW:

- Bootstrap is template-generated infrastructure, not user-authored content
- The "approval" is internal correctness checking, not product intent review
- Showing infrastructure scaffolding in the preview UI invites bikeshedding on mandatory requirements
- If NEEDS_CHANGES comes back, PM treats it as a system "fix and resubmit" loop, only asking the user if genuinely new information is needed

### D2: Architect recycles DONE → WAITING (never exits)

The architect's `Run()` loop currently breaks on DONE, causing the entire goroutine to exit. This must change:

- DONE → WAITING is already a valid FSM transition
- The architect should only truly exit on context cancellation (SIGTERM/SIGINT)
- This supports the FIFO spec queue model: after Spec 0 stories complete, architect must stay alive for Spec 1
- This is the correct behavior regardless of bootstrap

### D3: REJECT is not a valid spec response

The architect's spec review can return APPROVED, NEEDS_CHANGES, or REJECTED. For specs:

- **APPROVED**: Proceed to story generation
- **NEEDS_CHANGES**: Return feedback to PM, PM revises and resubmits
- **REJECTED**: Currently treated as "stop trying" — this is wrong for specs

REJECT should be removed from the spec review flow. At worst, a spec needs changes. REJECT exists to prevent automated work cycles from continuing indefinitely, not to permanently block human-authored work. For bootstrap specs specifically, the requirements are the minimum needed for the system to function — rejecting them is effectively fatal.

**Implementation**: The spec review template should strongly discourage REJECT as an option. The `review_complete` tool can still return REJECTED for internal safety, but the architect prompt should frame the choices as APPROVED (proceed) or NEEDS_CHANGES (provide feedback for revision).

### D4: Bootstrap template guidance text

When the architect reviews a bootstrap spec, it receives guidance that frames expectations:

> "This specification contains the minimum infrastructure requirements for the project to function. These requirements are generated from project templates based on the detected platform and missing components. You should approve this spec unless the requirements conflict with or duplicate existing code/infrastructure. If changes are needed, provide specific, actionable feedback. Do not reject this spec — these are mandatory requirements."

This guidance is injected as part of the spec review prompt when the request is tagged as a bootstrap spec.

### D5: All-to-all gating (not barrier stories)

An alternative was proposed: instead of making all Spec N stories depend on all Spec N-1 stories, create a synthetic "barrier story" per spec that depends on all stories within that spec, then make the next spec depend only on the barrier.

We chose all-to-all gating because:

- **No new concept**: Barrier stories would require a new story type/flag, auto-completion logic in the dispatcher (barriers have no coder work), WebUI filtering (don't show as real stories), persistence/metrics exclusion, and PM notification filtering. That's extensive special-casing for a "simplification."
- **Scale is small**: Bootstrap specs have 3-7 stories, user specs have 5-20. The all-to-all gating adds 15-140 dependency edges — just entries in a `[]string` slice per story.
- **Already implemented**: The pre-existing stories gate in `scoping.go` works correctly today. Barrier stories would require new code everywhere that touches story lifecycle.
- **No correctness risk**: Pre-existing stories are read from the queue at insertion time — they definitionally exist. The "missing dependency reference" concern doesn't apply.

If Maestro scales to specs with hundreds of stories, barrier stories can be reconsidered. At current scale, the simpler approach wins.

### D6: Intra-spec dependencies are honored exactly

Within a single spec, the LLM's explicit dependency graph is preserved faithfully. The `loadStoriesFromSubmitResultData()` code does two things per story:

1. Resolves ordinal dependencies from the LLM (`req_001` → `req_002`) to real story IDs
2. Adds cross-spec gates (pre-existing stories from prior specs)

Stories without dependencies within their spec remain independent and can execute in parallel. The cross-spec gates are additive — they don't make everything serial within a spec.

### D7: Future evolution — PM breaks large specs into smaller ones

This architecture naturally extends to the PM splitting large user specs into multiple smaller specs:

- PM submits Spec 1a, Spec 1b, Spec 1c in sequence
- Each spec's stories gate on all prior specs' stories (via pre-existing stories gate)
- Architect processes each spec independently
- `in_flight` becomes per-spec rather than global (or is only set after the final spec)

The FIFO spec queue model and architect DONE → WAITING recycling are the foundations that make this possible.

## Required Changes

### Change 1: Move `RenderBootstrapSpec` to shared package

**Why**: PM needs to call `RenderBootstrapSpec` to produce bootstrap spec markdown before sending it to the architect. Currently it's in `pkg/architect/bootstrap_spec.go`. Importing `architect` from `pm` would create a dependency cycle.

**What**: Move `RenderBootstrapSpec()` from `pkg/architect/bootstrap_spec.go` to `pkg/workspace/bootstrap_render.go`. The function only uses `config`, `packs`, `bootstraptpl`, and `workspace` — all already importable by both PM and architect.

**Files**:
- `pkg/architect/bootstrap_spec.go` → thin wrapper calling `workspace.RenderBootstrapSpec`
- `pkg/workspace/bootstrap_render.go` → new file, contains the moved function

### Change 2: PM sends bootstrap Spec 0 after config completes

**Why**: Once config is confirmed (via bootstrap tool or already in config.json), PM should render and submit the infrastructure bootstrap spec to the architect as Spec 0 before continuing with the user interview.

**What**: Two trigger points:

1. **After bootstrap tool completes** (`working.go`, `SignalBootstrapComplete` handler):
   - Re-detect bootstrap requirements (already done)
   - If `HasAnyMissingComponents()` is true (infrastructure items remain):
     - Render bootstrap spec
     - Call `sendBootstrapSpecRequest()` (new helper, similar to `sendSpecApprovalRequest`)
     - Return signal to transition to `AWAIT_ARCHITECT`
   - If no infrastructure items remain: continue as today

2. **At interview start when config is already complete** (`StartInterview()`):
   - After detecting bootstrap requirements
   - If `!NeedsBootstrapGate() && HasAnyMissingComponents()`:
     - Config is complete, infrastructure items missing
     - Render bootstrap spec, send to architect
     - Transition to `AWAIT_ARCHITECT` (PM blocks until acknowledged)
     - On approval: transition to `WORKING` or `AWAIT_USER` for interview

**New state key**: `StateKeyBootstrapSpecSent` — tracks whether bootstrap Spec 0 has been sent. Prevents double-submission.

**Files**:
- `pkg/pm/working.go` — handle `SignalBootstrapComplete` with bootstrap spec submission
- `pkg/pm/driver.go` — `StartInterview()` checks for immediate bootstrap spec submission
- `pkg/pm/driver.go` — add `sendBootstrapSpecRequest()` helper
- `pkg/pm/driver.go` — add `StateKeyBootstrapSpecSent` constant

### Change 3: PM tracks what it's waiting for in state (not response metadata)

**Why**: PM must not set `in_flight=true` when receiving bootstrap spec approval. `in_flight` should only be set for user spec approval.

**What**: Use PM-side state tracking instead of relying on response metadata round-tripping. The PM knows what it sent — it doesn't need the response to tell it.

**New state key**: `StateKeyAwaitingSpecType` with values `"bootstrap"`, `"user"`, or `"hotfix"`.

- Set `StateKeyAwaitingSpecType = "bootstrap"` right before dispatching bootstrap spec REQUEST
- Set `StateKeyAwaitingSpecType = "user"` right before dispatching user spec REQUEST
- Set `StateKeyAwaitingSpecType = "hotfix"` right before dispatching hotfix REQUEST
- In `handleAwaitArchitect()`, read `StateKeyAwaitingSpecType` to decide behavior:
  - `"bootstrap"`: log, inject context message, return to `WORKING` (do NOT set `in_flight`)
  - `"user"`: set `in_flight=true` as today
  - `"hotfix"`: existing hotfix handling
- Clear `StateKeyAwaitingSpecType` after processing the response

This is more robust than metadata tagging because:
- No dependency on response echoing request metadata
- PM explicitly declares its intent before blocking
- Eliminates a class of "oops wrong mode" bugs if metadata gets lost or mangled

**Files**:
- `pkg/pm/await_architect.go` — check `StateKeyAwaitingSpecType` on approval
- `pkg/pm/driver.go` — add `StateKeyAwaitingSpecType` constant, set it in `sendBootstrapSpecRequest`, `sendSpecApprovalRequest`, `sendHotfixRequest`

### Change 4: spec_submit stops bundling bootstrap requirements

**Why**: Once bootstrap Spec 0 has been sent separately, `spec_submit` should not include bootstrap requirement IDs in subsequent user spec submissions.

**What**: In `callLLMWithTools()`:
- Check `StateKeyBootstrapSpecSent` before injecting bootstrap reqs into spec_submit
- If already sent: skip injection (user spec only)
- If not sent: inject as today (fallback for non-bootstrap projects)

In `sendSpecApprovalRequest()`:
- Check `StateKeyBootstrapSpecSent` before including `BootstrapRequirements` in payload
- If already sent: empty `BootstrapRequirements` field

**Files**:
- `pkg/pm/working.go` — `callLLMWithTools()` guard
- `pkg/pm/working.go` — `sendSpecApprovalRequest()` guard

### Change 5: Architect recycles DONE → WAITING

**Why**: If bootstrap stories complete before the user spec arrives, the architect currently exits entirely. The architect must stay alive to receive Spec 1.

**What**: In `Run()`, replace the DONE break with DONE → WAITING transition:

```go
// Before:
if currentState == StateDone || currentState == StateError {
    break
}

// After:
if currentState == StateDone {
    d.logger.Info("All stories completed — recycling to WAITING for new specs")
    if err := d.TransitionTo(ctx, StateWaiting, nil); err != nil {
        d.logger.Error("Failed to recycle to WAITING: %v", err)
        break
    }
    continue
}
if currentState == StateError {
    break
}
```

Also update `processCurrentState`:
```go
// Before:
case StateDone:
    return StateDone, nil

// After:
case StateDone:
    return StateWaiting, nil
```

**Files**:
- `pkg/architect/driver.go` — `Run()` loop and `processCurrentState()`

### Change 6: Architect handles bootstrap spec type with lighter review

**Why**: Bootstrap specs are template-generated and should get a streamlined review with appropriate guidance text, not the full creative spec review treatment.

**What**: In `handleSpecReview()`:
- Check request metadata for `"spec_type": "bootstrap"`
- If bootstrap: prepend bootstrap guidance text to the spec review prompt (see §D4)
- Story generation uses the same `SpecAnalysisTemplate` (no change needed — the architect LLM should handle infrastructure stories the same way)

**Files**:
- `pkg/architect/request_spec.go` — `handleSpecReview()` checks metadata and adjusts prompt

### Change 7: Remove bootstrap req bundling from architect spec handler

**Why**: With bootstrap as Spec 0, the architect's `handleSpecReview` no longer needs to handle `BootstrapRequirements` in the approval payload. Bootstrap specs arrive as standalone requests with the rendered markdown in the Content field.

**What**: In `handleSpecReview()`:
- Remove `RenderBootstrapSpec(reqIDs)` call
- Remove infrastructure spec concatenation logic
- `userSpec` (Content field) IS the complete spec — no more combining
- Keep infrastructure spec handling as a deprecated fallback with a warning log

**Files**:
- `pkg/architect/request_spec.go` — simplify `handleSpecReview` to use Content directly

## File Change Summary

| # | File | Change | Complexity |
|---|------|--------|------------|
| 1 | `pkg/workspace/bootstrap_render.go` | New file: moved `RenderBootstrapSpec` | Low |
| 2 | `pkg/architect/bootstrap_spec.go` | Thin wrapper calling `workspace.RenderBootstrapSpec` | Low |
| 3 | `pkg/pm/driver.go` | `sendBootstrapSpecRequest()`, `StateKeyBootstrapSpecSent`, `StateKeyAwaitingSpecType`, trigger in `StartInterview()` | Medium |
| 4 | `pkg/pm/working.go` | Bootstrap spec submission on `BOOTSTRAP_COMPLETE` (skip PREVIEW), skip req injection if already sent | Medium |
| 5 | `pkg/pm/await_architect.go` | Check `StateKeyAwaitingSpecType` to decide `in_flight` behavior | Medium |
| 6 | `pkg/architect/driver.go` | DONE → WAITING recycling in `Run()` and `processCurrentState()` | Medium |
| 7 | `pkg/architect/request_spec.go` | Bootstrap guidance text, remove bootstrap req bundling | Medium |
| 8 | `pkg/architect/scoping.go` | No changes needed — pre-existing stories gate already handles FIFO | None |

## Test Plan

### Unit Tests

- **PM bootstrap spec submission**: Mock architect response, verify PM sends bootstrap REQUEST directly (no PREVIEW), blocks in AWAIT_ARCHITECT
- **PM `in_flight` handling**: Verify `in_flight` is NOT set when `StateKeyAwaitingSpecType == "bootstrap"`, IS set when `"user"`
- **PM `StateKeyAwaitingSpecType` lifecycle**: Verify it's set before dispatch, read in AWAIT_ARCHITECT, cleared after response
- **PM `spec_submit` guard**: Verify bootstrap req IDs are NOT bundled when `StateKeyBootstrapSpecSent` is true
- **Architect DONE recycling**: Verify architect transitions DONE → WAITING instead of exiting
- **Architect bootstrap review**: Verify bootstrap guidance text is included in spec review prompt when request metadata indicates bootstrap
- **Pre-existing stories gate**: Already tested — verify bootstrap stories gate user stories (existing tests in `scoping_test.go`)

### Integration Tests

- **Full bootstrap flow**: PM detects infrastructure needs → sends bootstrap Spec 0 → architect generates stories → PM interviews user → sends user Spec 1 → user stories gated on bootstrap stories
- **No bootstrap needed**: Config complete, no infrastructure gaps → user spec only, no Spec 0
- **Config already complete**: Infrastructure gaps detected at interview start → immediate bootstrap spec submission

### Manual Verification

- Run with a new greenfield project → observe bootstrap stories dispatched before user interview completes
- Run with an already-configured project → observe no bootstrap spec sent
- Verify architect stays alive after bootstrap stories complete and processes user spec

## Phases

### Phase 1: Foundation (this PR)
- Architect DONE → WAITING recycling
- Move `RenderBootstrapSpec` to shared package
- PM `sendBootstrapSpecRequest()` and `StateKeyBootstrapSpecSent`
- PM sends bootstrap spec after bootstrap tool completes
- PM differentiates bootstrap vs user spec in AWAIT_ARCHITECT
- spec_submit stops bundling bootstrap reqs when already sent
- Architect bootstrap guidance text
- Architect removes bootstrap req concatenation (deprecated fallback kept)

### Phase 2: Future enhancements
- PM breaks large user specs into multiple smaller specs (FIFO ordering via pre-existing stories gate)
- `in_flight` becomes per-spec or is only set after final spec in a batch
- Spec queue visibility in WebUI
- REJECT removal from spec review (prompt-level, not code-level — keep as safety valve)
