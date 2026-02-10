# Budget Review Limits: Analysis and Implementation Plan

## Problem Statement

Production testing on 2026-02-09 revealed that coders can enter unbounded budget review loops. The architect repeatedly identifies that a coder is "stuck in a loop / wrong approach" via NEEDS_CHANGES responses to budget reviews, but never returns REJECTED to actually stop the coder. This burns significant time and money with no convergence.

## Production Data

### Session Summary (2026-02-09, ~55 minutes observed)

| Coder | Stories Completed | Budget Reviews | NEEDS_CHANGES | Max Consecutive NC | Outcome |
|-------|-------------------|----------------|---------------|--------------------|---------|
| coder-001 | 1 (took ~55 min) | 16 | 10 | 6 consecutive | Eventually completed |
| coder-002 | 2 + still stuck | 20 | 10 | 8+ consecutive (ongoing) | Still looping at session end |
| coder-003 | 4 | 23 | 5 | 2 consecutive | Healthy — completed normally |

### Key Observations

1. **The architect never voluntarily returns REJECTED.** Despite saying "stuck in a loop / wrong approach" 10 times in budget review responses, the architect always returns NEEDS_CHANGES (which resets the iteration counter and continues) or APPROVED. The REJECTED path exists in code (`budget_review.go:137-140` → `StateError`) but the LLM never picks it.

2. **NEEDS_CHANGES and APPROVED have nearly identical effects for budget reviews from CODING.** Both reset the iteration counter to 0, giving 12 fresh iterations. NEEDS_CHANGES additionally injects feedback into the context, but in a 200+ message context window, one more message doesn't change the coder's behavior.

3. **Context grows monotonically.** The coder's ContextManager preserves all messages across budget review cycles. coder-001 went from 2 messages at first CODING entry to 394+ messages by the end — all failed attempts still in context.

4. **Healthy baseline exists.** coder-003 completed 4 stories with 23 budget reviews but never more than 2 consecutive NEEDS_CHANGES. This confirms that budget reviews work well for coders making progress — the problem is specifically the lack of a circuit breaker for persistent failure.

### Root Cause

The architect LLM (gpt-5.2) can diagnose that a coder is stuck but lacks the assertiveness to return REJECTED. This is a known LLM behavior pattern — models tend to be optimistic and give "one more chance" indefinitely. The system needs a mechanistic circuit breaker rather than relying on the LLM's judgment to stop a coder.

## Agreed Changes

### Phase 1: Budget Review Limits (this PR)

#### 1A. Consecutive NEEDS_CHANGES tracking with soft/hard limits

Track consecutive NEEDS_CHANGES responses on budget reviews. The streak counter lives on the **architect** side, stored in architect state data as a per-coder, per-review-type map.

**Data structure:**
```go
// reviewStreaks tracks consecutive NEEDS_CHANGES per coder per review type.
// map[coderID]map[reviewType]int
reviewStreaks map[string]map[string]int
```

Review type constants: `"budget"`, `"code"`, `"plan"` — only `"budget"` is enforced in Phase 1, but the plumbing supports all types for future use.

**Soft limit (3 consecutive NEEDS_CHANGES):**
- Inject additional context into the budget review prompt sent to the architect LLM: "This is the Nth consecutive NEEDS_CHANGES budget review for this coder. If the coder is stuck on the same underlying issue, you should REJECT the request rather than continuing to provide feedback that isn't being actioned."
- The architect LLM can still return APPROVED or NEEDS_CHANGES if it judges the situation differently.

**Hard limit (6 consecutive NEEDS_CHANGES):**
- Auto-reject without calling the architect LLM.
- The architect builds a REJECTED response directly and sends it back through the dispatcher.
- The coder receives REJECTED, transitions to ERROR state, and the story is requeued.
- Log: "Auto-rejected after 6 consecutive NEEDS_CHANGES budget reviews."

**Counter behavior:**
- **Increment** on NEEDS_CHANGES from budget review.
- **Reset to 0** on any non-NEEDS_CHANGES outcome (APPROVED, REJECTED, etc.).
- **Clear all counters for a coder** (`delete(reviewStreaks, coderID)`) on terminal exit — when the coder completes a story (DONE) or hits a terminal error. Centralize this in a single cleanup path (e.g., alongside `ResetAgentContext`).
- Limits apply regardless of todo completion state. The architect has no visibility into the todo list; the consecutive NC count is the signal.

**Implementation location:** `pkg/architect/` — streak tracking in architect state, enforcement in the budget review request handler.

#### 1B. Stop adding full feedback text to todo list

Currently, `code_review.go:138` adds the full architect feedback as a todo item:
```go
feedbackTodo := fmt.Sprintf("Address architect feedback: %s", result.Feedback)
c.todoList.AddTodo(feedbackTodo, -1)
```

This is wrong for three reasons:
- Todo items should be atomic work items ("Fix template collision"), not multi-paragraph strategy documents
- The full feedback is already injected into the conversation context via `c.contextManager.AddMessage`
- The todo list is rendered in full on every LLM call (via `getTodoListStatus()`), so a large feedback todo pollutes every subsequent prompt

**Change:** Remove the feedback-to-todo addition entirely in `code_review.go`, `plan_review.go`, and `await_merge.go`. The feedback is already in the context where it belongs.

#### 1C. Add "all complete" nudge to todo status

When all todos are complete, `getTodoListStatus()` currently just lists everything as completed with no guidance. The coder needs to know it should address any outstanding architect feedback or call `done`.

**Change:** When all todos are complete, append a nudge to `getTodoListStatus()`:

```
**All todos complete.** Wrap up any remaining architect feedback, then call the `done` tool.
```

This is particularly important after code review NEEDS_CHANGES, where the feedback is in the conversation context but the todo list shows "all complete" — the coder needs direction.

### Phase 2: Story Edit on Hard Failure (follow-up PR)

When the hard limit triggers and a story is requeued, it currently goes back to the queue with **identical content**. The next coder hits the same conceptual wall because the story doesn't contain the lessons learned.

**Planned change:** Before requeueing, give the architect a chance to edit the story:

1. Hard limit fires → instead of immediate auto-reject, give the architect a single-turn call with a `story_edit` tool
2. `story_edit` appends an "Implementation Notes" section to the story content with the architect's guidance (e.g., "Use per-page template sets for Go template isolation")
3. The enriched story is requeued
4. The next coder starts with the fix baked into the requirements

This is a larger change requiring:
- New `story_edit` tool (only available in this specific context)
- Modification to the hard-limit handler to orchestrate the architect call
- Story content mutation before requeue

Deferred to a separate PR to keep Phase 1 focused on the immediate circuit breaker.

## Design Notes

### Why architect-side, not coder-side

The streak counter lives on the architect because:
- The architect is the component that enforces the limit (modifies its own prompt at soft limit, short-circuits at hard limit).
- Architect state persists across restarts; coder instances may be re-created.
- The architect already maintains per-agent state (context managers, agent contexts).

### Why not restrict to todos-complete

The production data shows stuck loops happening during todo completion, not just after. coder-001 was getting NEEDS_CHANGES budget reviews while still working on todos 7-9/9. The architect has no visibility into the todo list — the consecutive NC count is the only signal it has. Restricting enforcement to todos-complete would miss real stuck scenarios.

The todos-complete condition is relevant only for the **coder-side nudge** (1C), where the coder needs to know "your todos are done, address feedback or call done."

### Claude Code mode

Budget reviews are not invoked in Claude Code mode. No changes needed there.

### Reusable mechanism, narrow policy

The `map[coder_id]map[review_type]int` structure and the soft/hard limit logic are built to support any review type. Only `"budget"` enforcement is enabled in Phase 1. Code/plan review limits can be added later as a policy change without plumbing changes.

## Files to Modify (Phase 1)

| File | Change |
|------|--------|
| `pkg/architect/driver.go` | Add `reviewStreaks map[string]map[string]int` field; add helper methods for increment/reset/clear |
| `pkg/architect/request.go` (or `request_budget.go`) | Check streak before calling LLM; inject soft limit warning at 3; auto-reject at 6 |
| `pkg/architect/request.go` | In response processing, increment/reset streak based on outcome |
| `pkg/architect/driver.go` | Clear streaks on coder exit (alongside `ResetAgentContext`) |
| `pkg/coder/code_review.go` | Remove full feedback text from todo list (lines 135-142) |
| `pkg/coder/plan_review.go` | Remove full feedback text from todo list |
| `pkg/coder/await_merge.go` | Remove full feedback text from todo list |
| `pkg/coder/todo_handlers.go` | Add "all complete" nudge when all todos are done |

## Testing Plan

- Unit test: streak increments on NEEDS_CHANGES, resets on APPROVED
- Unit test: soft limit at 3 injects warning text into budget review prompt
- Unit test: hard limit at 6 returns auto-REJECTED without calling architect LLM
- Unit test: `delete(streaks, coderID)` on coder exit clears all review types
- Unit test: feedback no longer added to todo list in code_review, plan_review, await_merge
- Unit test: todo status shows "all complete" nudge
- Verify message-role alternation is respected when injecting soft limit warning
