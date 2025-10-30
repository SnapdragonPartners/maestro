# Context Issue Investigation Notes

**Date**: 2025-10-22
**Issue**: Coder agents are repeating work and rewriting files multiple times in CODING state

## Symptoms

1. **Excessive file rewrites**: `main.go` written 41 times, `main()` function appears 14 times
2. **Empty response warnings**: 14-16 instances of LLM returning conversational text without tool calls
3. **Context growth**: Coder-002 context grew from 4,032 tokens → 19,606 tokens
4. **Budget review loop**: CODING → BUDGET_REVIEW → CODING repeatedly

## Root Causes Identified (Revised After Log Analysis)

### PRIMARY ROOT CAUSE: No Clear Completion Detection

**Analysis of Actual Behavior** (from logs):
- Coder-002 wrote `main.go` **11 times** (not 41 - other lines were context dumps)
- Each version was nearly **identical** with only minor variations (comments, imports)
- Also created variations: `main_quiz.go`, `main_new.go`, `/tmp/main_part1.go`, `main_quiz_functions.txt`
- Pattern: Write → Success → "Let me refine it" → Write again → Success → "Let me try another approach" → Repeat

**The model IS seeing its previous work in context:**
```
Assistant: Tool shell invoked
User: Command: cat > main.go << 'EOF'
package main
[full code with all imports, types, functions]
...
Exit Code: 0
```

**The problem is NOT "forgetting what was done"** - it's **"not knowing when to stop"**.

The model sees:
1. ✅ Successfully wrote main.go with complete implementation
2. ❓ "Is this good enough? Should I refine it?"
3. ✅ Successfully wrote main.go again with tiny changes
4. ❓ "Maybe try a different approach?"
5. Repeat until budget exceeded

### Root Causes

#### 1. Vague Completion Criteria
Template says: "When you have finished creating all necessary files and the implementation is complete, call the done tool"

But doesn't specify:
- **HOW** to determine if implementation is complete
- **WHEN** to stop refining/improving
- **WHAT** counts as "finished"

#### 2. No Explicit "Don't Repeat Work" Instruction
Template lacks guidance like:
- "Don't rewrite files that already exist and work"
- "If you find yourself writing the same file multiple times, STOP"
- "Only modify files if there are errors or missing requirements"

#### 3. Budget Approval Added Noise (Now Fixed)
- After budget exceeded, approval message said: "Continue with the implementation..."
- This reinforced "keep working" rather than "finish what remains"
- **Fixed**: Budget approval is now transparent - no message added

### What Expert Analysis Got Wrong

The get_help tool suggested:
- **Token truncation** - Wrong: 20k tokens is nowhere near 200k+ context limits
- **Snapshot + delta architecture** - Potentially helpful long-term, but not the immediate issue
- **Tool results are useless without calls** - Wrong: We DO echo full commands with all code

The expert was reasoning about a different architecture (MCP tools as separate executables) rather than Claude's native tool use API.

## Log Evidence

```
[2025-10-23T02:52:16.816Z] [coder-002] INFO: Coding budget exceeded, triggering BUDGET_REVIEW
[2025-10-23T02:52:21.232Z] [coder-002] INFO: 🧑‍💻 Budget review approved, returning to origin state: CODING
[2025-10-23T02:52:21.232Z] [coder-002] INFO: 🧑‍💻 Starting coding phase for story_type 'app'
```

File rewrite count:
```bash
$ grep "cat > main.go <<" logs/run.log | wc -l
41
```

Context growth:
```
request tokens: 4,032  (initial)
request tokens: 5,558
request tokens: 8,758
...
request tokens: 19,606 (final)
```

## Solution Implemented

### Immediate Fixes (Completed)

#### 1. ✅ Improve Budget Approval Message
**File**: `pkg/coder/budget_review.go:62-87`

**Before**:
```go
approvalMessage = "The architect has approved your current approach. Continue with the implementation and invoke the 'done' tool when you are complete."
```

**After**:
```go
approvalMessage = "Budget approved. If your implementation substantially fulfills the story requirements, call the 'done' tool to submit your work for review. Otherwise, continue with any remaining implementation work."
```

**Rationale**: Budget approval is the right place to redirect if the coder is spinning wheels. The message now explicitly prompts completion check rather than just saying "continue".

#### 2. ✅ Add Clear Completion Criteria to Coding Templates
**Files**: `pkg/templates/app_coding.tpl.md` and `pkg/templates/devops_coding.tpl.md`

**Added**:
```markdown
**COMPLETION CRITERIA - Call the `done` tool when ALL of these are true**:
1. All required files from your plan have been created (check with `ls`)
2. The code compiles/runs without errors (verify with build or run command)
3. All requirements from the task are satisfied
4. Only modify files if there are errors or missing requirements to address
```

**Rationale**: Give the model concrete, actionable criteria for when to call `done`.

#### 3. ✅ Add Completion Detection Guidance to Architect Budget Review
**File**: `pkg/templates/budget_review_coding.tpl.md`

**Added**:
```markdown
**Issue**: Work appears complete but agent hasn't called 'done' tool
- **Pattern**: Agent has created all required files, code compiles/runs successfully, but continues refining or rewriting working code
- **Correct Response**: Use APPROVED status with empty feedback field. The budget approval message will automatically remind the agent to call 'done' if work is substantially complete.
- **Why**: This lets the architect validate completion without micromanaging.
```

**Rationale**: Teach the architect (o3) to recognize the "spinning wheels" pattern and use simple APPROVED status to trigger the completion reminder. This is better than having the coder self-detect because the architect has the full context.

### Expected Impact

**Before** (observed behavior):
- Writes main.go 11 times with minor variations
- Keeps refining/experimenting without clear stopping point
- Hits budget limit repeatedly

**After** (expected behavior):
- Write main.go once
- Verify it compiles/works
- Call `done` tool
- Move to testing phase

### Testing Plan

1. Run the same hello-world quiz story that triggered the issue
2. Monitor:
   - Number of times main.go is written (should be 1-2 max)
   - Whether coder calls `done` tool after successful implementation
   - Whether budget review is triggered (should be less frequent)
3. Check logs for "Shell command succeeded: cat > main.go" count

## Long-Term Architectural Improvements (Deferred)

### Pattern: Deterministic-State Prompting (Snapshot + Delta)

**Core Principle**: Treat the repository as the single source of truth. Never feed the model the entire historical transcript. Instead, provide a concise structured snapshot of current state plus an explicit delta request.

### Implementation Components

#### 1. RepoState Projection
```go
type RepoState struct {
    Files     []FileMeta          // name, hash, size, test-coverage, etc.
    TODO      []string            // open tasks that remain
}
```

- Update after every successful tool execution
- Persist so it survives restarts
- This becomes the **canonical representation** of what's been done

#### 2. Prompt Construction Pattern
```
--- system ---
You are a coder. Use TOOL CALLS ONLY …

--- repo state ---
Existing files (hash in parentheses):
  • cmd/main.go  (8e54c6)
  • internal/db/db.go (4d32a1)
  …

Open tasks:
  • implement REST handler
  • add unit tests for service layer

--- architect ---
Your plan has been approved. Continue and call `done` when finished.
```

**No 20k lines of Go code, no earlier tool-call chatter.**

#### 3. Read-Before-Write Pattern
Ask the model to call cheap, read-only tools if unsure:
- `read_file`, `list_files`, `git_diff`
- Reinforces that repo already exists
- Avoids "re-create everything" reflex

#### 4. Idempotent Write Tools
- If file exists AND payload hash is identical → respond with `"noop": true`
- Don't store the message if it's a no-op
- If content differs → commit change and update RepoState

#### 5. Context Garbage Collection
- After a turn finishes, squash all tool-result messages into updated RepoState summary
- Throw away raw tool results
- Keeps context window permanently small (<2k tokens)

### Benefits

✅ Model always sees **current** world, never out-of-date/truncated scrollback
✅ "These files already exist" is explicit and near bottom of prompt
✅ Idempotent writes mean repeated operations become cheap NO-OPs
✅ Context stays small, no token truncation issues
✅ Works with both streaming and non-streaming APIs

## Quick Wins (Can Implement Immediately)

These don't require full architectural change:

### 1. Move "TOOL CALLS ONLY" to System Message
Current: User message at end of prompt (overrides history)
Better: System message (persistent instruction without repetition)

### 2. Fix Role Mapping in buildMessagesWithContext
Current: Custom "architect" role becomes "assistant"
Better: Map "architect" → RoleUser explicitly
Location: `pkg/coder/driver.go:157-183`

### 3. Add Explicit State Reminder in Budget Approval
```go
approvalMessage = `Budget approved.

REPO STATE: Files already exist in workspace. Use read_file to verify before writing.

Continue your previous work and invoke 'done' when complete. DO NOT recreate files that already exist.`
```

## Implementation Roadmap

### Phase 1: Quick Wins (Immediate)
1. ✅ Move "TOOL CALLS ONLY" to system message in templates
2. ✅ Fix architect role mapping in buildMessagesWithContext
3. ✅ Add explicit repo state reminder in budget approval message

### Phase 2: RepoState Projection (Core Architecture)
1. Design RepoState struct and FileMeta schema
2. Implement RepoState tracker that updates on tool results
3. Add RepoState persistence (survives restarts)
4. Create RepoState formatter for prompt injection

### Phase 3: Context Manager Refactor
1. Implement snapshot-based prompt builder
2. Add context garbage collection after each turn
3. Remove raw tool result accumulation
4. Keep context window < 2k tokens permanently

### Phase 4: Idempotent Tools
1. Add content hash checking to file write tools
2. Return `"noop": true` for duplicate writes
3. Skip storing no-op messages in context
4. Update RepoState only on actual changes

### Phase 5: Testing & Validation
1. Test with original failing scenario (hello world story)
2. Monitor: file write count, context size, budget cycles
3. Verify: no repeated work, context stays small
4. Load test: multi-story scenarios

## Related Code Locations

- Budget review handler: `pkg/coder/budget_review.go:64`
- Coding phase start: `pkg/coder/coding.go` (search "Starting coding phase")
- Context manager: `pkg/contextmgr/`
- Templates: `pkg/templates/app_coding.tpl.md`, `pkg/templates/devops_coding.tpl.md`
