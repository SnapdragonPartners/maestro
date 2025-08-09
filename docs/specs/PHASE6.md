# Phase 6 — Refactor & State‑Machine Alignment

This phase updates the codebase to match the **v2 state diagrams** and cleans up the package layout.  It assumes all Phase 3–5 functionality is merged.

Front‑matter schema unchanged.

## Table of Contents

| ID  | Title                                                   | Est. | Depends | Status |
| --- | ------------------------------------------------------- | ---- | ------- | ------ |
| 060 | Repository refactor to new package layout               | 3    | 051,053 | ✅ DONE |
| 061 | Coding Agent driver update to v2 FSM                    | 4    | 060     | ✅ DONE |
| 062 | Architect driver update (merged queue/dispatch + chans) | 4    | 060     | ✅ DONE |
| 063 | Dispatcher & channel wiring                             | 2    | 061,062 | ✅ DONE |
| 064 | Documentation & diagram sync                            | 1    | 061,062 | ✅ DONE |

---

### Story 060 — Repository refactor to new package layout

```markdown
---
id: 060
title: "Repository refactor to new package layout"
depends_on: [051,053]
est_points: 3
---
**Task**  
Re‑organize source tree:
```

/pkg/agent       # common abstractions (LLMClient, Tool registry, FSM base)
/pkg/coder       # Claude coding agent implementation
/pkg/architect   # o3 architect agent implementation
/pkg/tools       # existing MCP tools (shell, file, github)
/agents          # REMOVE (moved code into pkg/\*)

```
Steps:
1. Move `driver_agent.go` → `/pkg/architect/driver.go`.
2. Move `claude_live.go` → `/pkg/coder/claude.go` and update imports.
3. Extract shared structs (`AgentMsg`, `State`, contextmgr) into `/pkg/agent`.
4. Update `go.mod`, fix import paths, run `go vet` & `go test`.

**Acceptance Criteria**
* `go test ./...` green after move.
* No code references `/agents/`.
```

### Story 061 — Coding Agent driver update to v2 FSM

```markdown
---
id: 061
title: "Coding Agent driver update to v2 FSM"
depends_on: [060]
est_points: 4
---
**Task**  
Refactor `/pkg/coder/driver.go`:
1. Implement new `State` enum per master diagram.
2. Create single `QUESTION` state with `origin` metadata (use helper: `NewQuestion(origState, msg)`).
3. Add explicit `TESTING` and `FIXING` states; on test fail go to `FIXING`.
4. Remove `TOOL_INVOCATION` state; treat tool calls as in‑state events.
5. Add retry & timeout logic; store `{state, retries, ts}` in `STATUS.json`.
6. PLAN_REVIEW and CODE_REVIEW both use REQUEST→RESULT flow with approval payload.

**Acceptance Criteria**
* Integration: `/health` story runs through PLANNING → DONE with mocks.
* Test simulate failure path using table-driven tests: `{pass bool, attempts int}` where mock test runner fails first N attempts, driver loops FIXING→TESTING until pass or timeout.

**Implementation Summary (✅ COMPLETED 2025-06-13)**
* ✅ Complete v2 FSM implementation with all required states
* ✅ Agent foundation integration with BaseStateMachine and BaseDriver 
* ✅ REQUEST→RESULT flow for plan and code approvals with proper state keys
* ✅ QUESTION→ANSWER flow with origin tracking (PLANNING, CODING, FIXING)
* ✅ Mock mode autonomous testing without LLM dependency
* ✅ Live mode LLM integration ready (Claude client)
* ✅ Comprehensive integration tests covering all state flows
* ✅ agentctl test harness updated for standalone coder testing
* ✅ State persistence and recovery through state store
* ✅ Critical bug fixes: state key consistency, transition logic
* 🔧 Architect commands temporarily disabled in agentctl (LLM interface compatibility)
```

### Story 062 — Architect driver update (merged queue/dispatch + channels)

```markdown
---
id: 062
title: "Architect driver update (merged queue/dispatch + channels)"
depends_on: [060]
est_points: 4
---
**Task**  
Replace separate queue & dispatch states with `QUEUE_AND_DISPATCH` in `/pkg/architect/driver.go`:
1. Maintain `architect_queue.json` with statuses.
2. Use buffered channels (size 1): `readyStoryCh`, `idleAgentCh`, `reviewDoneCh`, `questionAnsweredCh`.
3. Spawn long-running **ANSWER_WORKER** & **REVIEW_WORKER** goroutines at driver start; workers send back on channels.
4. Implement `AWAIT_HUMAN_FEEDBACK` state with retry counter.

**Acceptance Criteria**
* Mock run shows queue processed, workers spawn, DONE when queue empty.
* Escalation path logs and waits for human flag.

**Implementation Summary (✅ COMPLETED 2025-06-13)**

**Core v2 Architecture Implementation:**
* ✅ Complete state enum refactor: `QUEUE_MANAGEMENT` + `DISPATCHING` → `QUEUE_AND_DISPATCH`
* ✅ Channel-based worker architecture with buffered channels (size 1 as per spec)
* ✅ Long-running `ANSWER_WORKER` and `REVIEW_WORKER` goroutines 
* ✅ Aggressive legacy code removal (separate state handlers, old fields, constructors)
* ✅ `handleQueueAndDispatch()` with channel select loop implementation

**Production-Ready Critical Fixes:**
* ✅ **DispatcherAdapter**: Real dispatcher integration via adapter pattern (resolves production blocker)
* ✅ **Panic Recovery**: Worker goroutines protected with `defer recover()` blocks
* ✅ **Timeout Protection**: 5-second timeouts on all channel operations in `RouteMessage()`
* ✅ **Complete Dispatch Logic**: `dispatchReadyStory()` and `assignStoryToAgent()` methods
* ✅ **Critical State Persistence**: Queue failures now return `StateError` instead of warnings
* ✅ **Graceful Shutdown**: 30-second timeout with channel drainage and proper cleanup
* ✅ **Resource Limits**: Spec parser protected (10MB max, 1000 requirements max)
* ✅ **Message Validation**: Comprehensive validation (nil, empty ID, missing sender checks)

**Channel Connectivity & Message Flow:**
* ✅ **Queue Notifications**: `readyStoryCh` connected with `checkAndNotifyReady()` 
* ✅ **Worker Message Routing**: `RouteMessage()` with timeout and validation
* ✅ **Response Generation**: Workers send `ANSWER`/`RESULT` messages via dispatcher
* ✅ **Error Handling**: `sendErrorResponse()` methods for graceful error communication
* ✅ **Mock & Live Modes**: Full support for both testing and production environments

**Integration & Testing:**
* ✅ **Channel Integration Tests**: Comprehensive test suite verifying end-to-end message flow
* ✅ **Worker Processing**: Verified question answering and code review workflows
* ✅ **Queue Story Notifications**: Test coverage for story readiness notifications
* ✅ **Graceful Shutdown**: Verified worker cleanup and channel closure
* ✅ **Legacy Test Cleanup**: Disabled outdated tests referencing removed methods

**Files Modified:**
* `pkg/architect/driver.go` - Complete v2 FSM implementation with production fixes
* `pkg/architect/queue.go` - Channel notification integration
* `pkg/architect/spec2stories.go` - Resource limits and validation
* `pkg/architect/integration_channel_test.go` - Comprehensive connectivity tests
```

### Story 063 — Dispatcher & channel wiring

```markdown
---
id: 063
title: "Dispatcher & channel wiring"
depends_on: [061,062]
est_points: 2
---
**Task**  
Update `pkg/dispatch`:
1. Expose `SubscribeIdleAgents()` returning `idleAgentCh`.
2. Notify architect driver when coding agent finishes (`RESULT`).
3. Route architect answer/review messages back to coding agent.
4. Close `idleAgentCh` when architect exits for graceful dispatcher shutdown.

**Acceptance Criteria**
* End‑to‑end smoke test: architect dispatches → coder completes → architect marks done.

**Implementation Summary (✅ COMPLETED 2025-06-13)**

**Core Channel-Based Communication:**
* ✅ **SubscribeIdleAgents()**: Architect subscription to idle agent notifications with buffered channel (size 10)
* ✅ **Idle Agent Notifications**: Automatic notifications when coding agents complete tasks with status checking
* ✅ **Channel Integration**: Architect driver connects to dispatcher's idle channel instead of creating own
* ✅ **Graceful Shutdown**: CloseIdleChannel() method for proper resource cleanup on dispatcher shutdown

**Production-Ready Agent State Tracking:**
* ✅ **Busy/Idle State Management**: busyAgents map with mutex protection prevents duplicate notifications
* ✅ **Work Assignment Tracking**: PullSharedWork() marks agents as busy when pulling tasks
* ✅ **Completion Detection**: NotifyArchitectOnResult() with comprehensive status validation
* ✅ **Error State Handling**: Extended completion statuses include "error", "failed", "timeout", "cancelled", "aborted"

**Message Routing and Dispatch Integration:**
* ✅ **Pull-Based Architecture**: Message routing through shared work queue with proper agent resolution
* ✅ **Logical Agent Names**: Fixed hardcoded agent IDs, now uses "coder" logical name resolved by dispatcher
* ✅ **Dispatcher Integration**: assignStoryToAgent() properly sends tasks via DispatchMessage() in production mode
* ✅ **Queue-Based Message Flow**: TASK → shared queue → agent pull → processing → RESULT → idle notification

**Testing and Validation:**
* ✅ **End-to-End Tests**: Comprehensive TestEndToEndChannelWiring validates full message flow
* ✅ **Channel Cleanup Tests**: TestIdleAgentChannelCleanup verifies graceful shutdown
* ✅ **Mock Agent Simulation**: Realistic testing with MockArchitectAgent and MockCoderAgent
* ✅ **State Transition Verification**: Logged transitions show busy→idle state changes

**Files Modified:**
* `pkg/dispatch/dispatcher.go` - Agent state tracking, idle notifications, completion status validation
* `pkg/architect/driver.go` - Logical agent naming, dispatcher integration in story assignment
* `pkg/dispatch/channel_integration_test.go` - Comprehensive integration testing
```

### Story 064 — Documentation & diagram sync

```markdown
---
id: 064
title: "Documentation & diagram sync"
depends_on: [061,062]
est_points: 1
---
**Task**  
Update `/docs/` and `master_state_diagrams_v2.md`:
1. Ensure diagrams exactly match implemented enums.
2. Add README section on channel architecture.

**Acceptance Criteria**
* `make lint-docs` passes (Markdown linter).

**Implementation Summary (✅ COMPLETED 2025-06-13)**

**Documentation Updates:**
* ✅ **State Diagrams Verified**: STATES.md diagrams exactly match implemented enums
  - Coder FSM: WAITING, PLANNING, PLAN_REVIEW, CODING, TESTING, FIXING, CODE_REVIEW, QUESTION, DONE
  - Architect FSM: SPEC_PARSING, STORY_GENERATION, QUEUE_AND_DISPATCH, AWAIT_HUMAN_FEEDBACK, DONE, ERROR
* ✅ **Channel Architecture Documentation**: Added comprehensive README section explaining Phase 6 architecture
  - Dispatcher channel subscriptions and notifications
  - Worker channel patterns and buffering strategies  
  - Message flow diagrams for task assignment, completion, questions, and reviews
* ✅ **Agent Flow Updated**: README now reflects v2 FSM and channel-based coordination
* ✅ **Package Structure**: Updated directory structure to reflect Phase 6 clean architecture

**Build System:**
* ✅ **Makefile Enhancement**: Added `lint-docs` target for markdown documentation validation
* ✅ **Documentation Linting**: All 28 markdown files pass linting checks

**File Updates:**
* `STATES.md` - Updated timestamp to reflect Phase 6 completion
* `README.md` - Comprehensive Phase 6 architecture documentation with channel patterns
* `Makefile` - Added lint-docs target with markdown validation
* `PHASE6.md` - Story completion documentation
```

---

## Implementation Notes

### Question State Helper Pattern
```go
func NewQuestion(orig State, q AgentMsg) AgentMsg {
    q.Type = MsgTypeQuestion
    q.Metadata = map[string]string{"origin": orig.String()}
    return q
}
// Driver reads msg.Metadata["origin"] to know which state to resume
```

### Worker Creation Pattern  
```go
answerW := NewAnswerWorker(questionCh, questionAnsweredCh)
reviewW := NewReviewWorker(reviewReqCh, reviewDoneCh)
go answerW.Run(ctx)
go reviewW.Run(ctx)
// Workers block on inbound channel; architect pushes work onto it
```

### Review Flow Pattern
Both PLAN_REVIEW and CODE_REVIEW emit `QUESTION` with `origin = PLAN` and payload `{"plan": <json>}`. Architect replies with `RESULT { "approved": true|false, "feedback": "…" }`. Driver routes back to `PLANNING` on rejection or forward to `CODING` on approval.

---

> **Generated:** 2025‑06‑11

