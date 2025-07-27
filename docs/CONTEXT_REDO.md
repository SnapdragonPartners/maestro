# Context Management Redesign Plan

## Overview
Redesign the coder agent's context management to eliminate duplication between templates and conversation messages, implement strategic context clearing at state transitions, and preserve critical information like PIVOT messages.

## Current Issues
1. **Context Duplication**: Templates embed context via `formatContextAsString()`, then `buildMessagesWithContext()` adds the same context as separate messages
2. **Missing PIVOT Preservation**: Budget review doesn't preserve feedback like plan review does (line 842 vs missing in budget review)  
3. **Inconsistent Message Adding**: Some message types added inconsistently across states
4. **No Context Boundaries**: Context persists indefinitely across state transitions

## Research Findings

### 1. PIVOT Message Structure ✅
- **Structure**: `ApprovalResult.Feedback` contains PIVOT instructions
- **Current Issue**: Budget review handler doesn't preserve feedback (unlike plan review at pkg/coder/driver.go:842)
- **Solution**: Add missing feedback preservation to budget review handler

### 2. Message Type Audit ✅
**Current message types and locations:**
- **Task content**: Line 541 (user message)
- **Phase indicators**: Lines 575, 651, 871, etc. (assistant messages)
- **LLM responses**: Lines 1056, 2918 (assistant messages)
- **Tool results**: Lines 1143, 1177, 1205+ (tool messages)
- **System messages**: Line 1072 (system messages)
- **Architect feedback**: Line 842 (architect messages, but **missing in budget review**)
- **Q&A pairs**: Lines 2155, 2157 (user question, assistant answer)

### 3. Compaction Logic ✅
**Existing foundation is strong:**
- Already preserves index 0 as "system prompt" (pkg/contextmgr/contextmgr.go:111-114)
- Has sliding window compaction and summarization
- Preserves last 2 messages as "recent" during summarization

### 4. Template Review ✅
**Templates are appropriate for system prompts:**
- Planning and coding templates don't embed conversational context
- No `{{.Context}}` usage in main templates (planning.tpl.md, coding.tpl.md)
- Ready to serve as pseudo-system prompts

## Architect-Validated Solution

### Core Architecture ✔️
```
Template (Static + State Data) → System Prompt (Index 0)
+ 
Conversation Messages → Rolling Window (Index 1+)
```

### Context Manager Interface ✔️
```go
type ContextManager interface {
    SystemPrompt() Msg               // Always index 0
    Conversation() []Msg             // Rolling window  
    ResetSystemPrompt(Msg)           // Used on state entry
    Append(role, content string)     // Regular dialogue with role (user/assistant/tool/system)
    Compact(maxTokens int) error
}
```

### Context Clearing Triggers ✔️
- **PLAN_REVIEW → CODING**: Clear (plan frozen, new semantic contract)
- **BUDGET_REVIEW → PLANNING (PIVOT)**: Clear + preserve story + plan + PIVOT feedback
- **All other transitions**: Keep conversation (micro-cycles within same contract)

### PIVOT Preservation Strategy ✔️
```
System Prompt = Story + Original Plan + **[BUDGET_PIVOT_FEEDBACK]:** "<text>"
```

### Todo List Approach ✔️
- **File-based**: `TODO.md` in worktree
- **Tool**: `update_todo_list` (replace entire content)
- **Benefits**: Compaction-resistant, version-controlled, load-on-demand

## Implementation Plan

### Phase 1: Fix PIVOT Preservation
- [x] Add missing feedback preservation in budget review handler (similar to line 842)
- [x] Add context clearing in BUDGET_REVIEW → PLANNING transition
- [x] Add context clearing in PLAN_REVIEW → CODING transition
- [x] Implement clearContextForTransition helper method
- [ ] Test PIVOT message flows

### Phase 2: Context Manager Interface Redesign
- [x] Create new ContextManager interface with system/conversation separation
- [x] Implement ResetSystemPrompt() method
- [x] Update Append() method to include role parameter
- [x] Ensure index 0 preservation through compaction

### Phase 3: Eliminate Duplication
- [x] Modify buildMessagesWithContext to avoid template/context duplication
- [x] Remove formatContextAsString() from template data
- [x] Update template rendering to only provide static data
- [x] Test that templates serve as system prompts

### Phase 4: Context Clearing Logic
- [x] Add context clearing in PLAN_REVIEW → CODING transition
- [x] Implement strategic clearing with state data preservation
- [x] Test context boundaries at state transitions
- [x] Verify conversation history preservation within states

### Phase 5: Todo List Refactor
- [x] Create TODO.md file-based approach
- [x] Implement update_todo_list tool for CODING state
- [x] Remove todo list from state data/prompts
- [x] Test todo list persistence and editing

### Phase 6: Testing & Validation
- [x] Write unit tests for context boundaries and deduplication
- [x] Add integration tests for state transitions
- [x] Measure token usage reduction
- [x] Verify no information loss during transitions

## Expected Benefits
- **Eliminate duplication**: No more double-inclusion of context
- **Reduce token usage**: More efficient prompts
- **Clear state boundaries**: Explicit context management  
- **Better testability**: Predictable context lifecycle
- **Preserved information**: PIVOT messages and critical data maintained

## Trade-offs Accepted
- **History loss between CODING↔PLANNING cycles**: Acceptable since plan preserved in state data
- **LLM notification needed**: Should inform about potential reversion based on PIVOT nature

## Current Todo Status
1. [completed] Investigate git worktree issues causing agent failures (high)
2. [completed] Fix GitRunner logging level for expected worktree remove failures (medium)
3. [pending] Test git operations in coder agents (high)
4. [completed] Fix linting issues in LLM error handling code (medium)
5. [completed] Investigate coder not picking up answers to questions (high)
6. [pending] Test the QUESTION/ANSWER fix with a running instance (high)
7. [completed] Fix QUESTION state to use blocking select like other request states (high)
8. [completed] Include question and answer in LLM prompt when returning to origin state (high)
9. [completed] Research PIVOT message structure and storage (high)
10. [completed] Audit where each message type gets added to context (high)
11. [completed] Check existing compaction logic for message preservation (high)
12. [completed] Review templates for inappropriate conversational content (high)

## Implementation Notes
- **Architect validated**: Technical approach confirmed sound
- **Role parameter**: Append() method needs role (user/assistant/tool/system) for Q&A pairs
- **System prompt tagging**: Use `**[BUDGET_PIVOT_FEEDBACK]:**` for deterministic identification
- **Size limits**: Trim PIVOT feedback to prevent prompt bloat
- **Edge cases**: Handle ESCALATION scenarios carefully to avoid context loss