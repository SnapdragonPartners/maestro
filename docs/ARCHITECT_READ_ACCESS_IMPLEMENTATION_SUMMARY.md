# Architect Read Access - Revised Implementation Summary

**Date**: 2025-11-04
**Status**: In Progress - Phase 4b Complete

---

## Implementation Progress

### ✅ Completed Phases

**Phase 1: Workspace Pre-Creation** (Completed 2025-11-03)
- Orchestrator pre-creates coder workspace directories at startup
- Configuration schema updated with `max_coders` setting
- Workspace lifecycle verified and tested

**Phase 2: Architect Containerization** (Completed 2025-11-03)
- `pkg/exec/architect_executor.go` - Docker executor with read-only mounts
- Architect container starts at orchestrator initialization
- Mounts all coder workspaces: `/mnt/coders/coder-001` through `/mnt/coders/coder-NNN`
- Integration with `pkg/architect/driver.go` via executor field

**Phase 3: MCP Read Tools** (Completed 2025-11-03)
- `pkg/tools/read_file.go` - Read file contents from coder workspace
- `pkg/tools/list_files.go` - List files with pattern matching
- `pkg/tools/get_diff.go` - Get git diff for coder changes
- `pkg/tools/submit_reply.go` - Explicit iteration termination signal
- All tools registered in `pkg/tools/registry.go`
- Tool documentation generation implemented

**Phase 4a: Iteration Pattern Helper Methods** (Completed 2025-11-04)
- `checkIterationLimit()` - Soft/hard limit enforcement in `pkg/architect/driver.go`
- `createReadToolProvider()` - Lazy tool provider creation
- `getArchitectToolsForLLM()` - Tool definition conversion
- `processArchitectToolCalls()` - Tool execution with logging
- Iteration state tracking via state data

**Phase 4b: Iterative Approval Implementation** (Completed 2025-11-04)
- `handleIterativeApproval()` - Code and completion review with tools
- `handleIterativeQuestion()` - Technical question answering with workspace exploration
- `generateIterativeCodeReviewPrompt()` - Code review prompt with tool docs
- `generateIterativeCompletionPrompt()` - Completion review prompt with tool docs
- `generateIterativeQuestionPrompt()` - Question answering prompt with tool docs
- `buildApprovalResponseFromSubmit()` - Response packaging
- `buildQuestionResponseFromSubmit()` - Answer packaging
- Request routing logic updated in `handleRequest()`
- Budget reviews kept lightweight (no tools) per design

**Phase 4c: Shared Tool Logging Infrastructure** (Completed 2025-11-04)
- `pkg/agent/tool_logging.go` - Shared `LogToolExecution()` function
- Tool execution persistence to database with session tracking
- Both coder and architect now use shared logging
- Audit trail for all tool usage across agents

### 🚧 In Progress

None - ready for Phase 5

### 📋 Pending Phases

**Phase 5: Chat Escalation Support**
- Chat-based escalation when hard iteration limits exceeded
- ESCALATE state handler with human-in-the-loop
- WebUI enhancements for escalation display

**Phase 6: Testing & Validation**
- Integration test for OpenAI tool calling
- End-to-end testing of iterative approval flow
- Performance validation

**Phase 7: Documentation Updates**
- Update CLAUDE.md with architect read access notes
- Update ARCHITECTURE.md with container topology
- Final implementation notes

---

## Key Insights from Coder Study

After studying the existing coder implementation, I've identified that:

1. **Workspace management already exists** - `CloneManager.SetupWorkspace()` handles atomic cloning
2. **No iteration loop manager needed** - the FSM IS the loop (state returns itself to continue)
3. **The pattern is simple** - just replicate what PLANNING and CODING states already do

---

## Simplified Phase Structure

### Phase 1: Workspace Infrastructure (MOSTLY DONE)
**Duration**: 1 day

**What Already Exists**:
- `CloneManager.SetupWorkspace()` - atomic workspace creation with fresh clone
- `CloneManager.ensureMirrorClone()` - mirror management with file locking
- `CloneManager.createFreshClone()` - removes old, creates new clone

**What Needs to be Done**:
- Orchestrator creates empty `coder-NNN/` directories at startup based on `max_coders` config
- Orchestrator ensures coders call `SetupWorkspace()` during SETUP phase (already happens)
- Verify mirror updates happen after story completion (already implemented)

**Implementation**:
```go
// Read max_coders from config
cfg := config.GetConfig()
maxCoders := cfg.Orchestrator.MaxCoders // e.g., 3

// Pre-create workspace directories for mounting
for i := 1; i <= maxCoders; i++ {
    coderDir := filepath.Join(projectDir, fmt.Sprintf("coder-%03d", i))
    if err := os.MkdirAll(coderDir, 0755); err != nil {
        return fmt.Errorf("failed to create workspace directory: %w", err)
    }
}
```

**Deliverables**:
- [x] Orchestrator pre-creates empty workspace directories based on `max_coders` config
- [x] Add `max_coders` to config schema
- [x] Verify workspace lifecycle works correctly
- [x] Test concurrent workspace operations

---

### Phase 2: Architect Containerization
**Duration**: 3-4 days

**Container Startup**:
- Architect starts once at orchestrator startup
- Mounts ALL workspace directories: `/mnt/coders/coder-001` through `/mnt/coders/coder-010`
- Mounts mirror: `/mnt/mirror`
- All mounts are read-only
- Uses same bootstrap image as coders

**No Dynamic Updates**:
- Workspaces persist on host regardless of coder lifecycle
- Empty workspaces just have no content (but mount points exist)
- No container restart needed (coders are ephemeral, workspaces persist)

**Architecture Changes**:
```go
// pkg/exec/architect_executor.go
type ArchitectExecutor struct {
    containerID string
    image       string
    mounts      []Mount  // Fixed at startup
    executor    Executor
}

// Mounts configured once:
// - /mnt/coders/coder-001 → projectDir/coder-001 (ro)
// - /mnt/coders/coder-002 → projectDir/coder-002 (ro)
// - ... up to max coders (10)
// - /mnt/mirror → projectDir/.mirrors/repo.git (ro)
```

**Integration**:
- `pkg/architect/driver.go` gets `executor` field
- Used by new MCP tools for file access within container
- Architect failure remains fatal error (one retry acceptable)

**Deliverables**:
- [x] `pkg/exec/architect_executor.go` - container lifecycle management
- [x] Update `pkg/architect/driver.go` - add executor field
- [x] Orchestrator starts architect container at startup
- [x] Integration tests for mount configuration

---

### Phase 3: MCP Read Tools
**Duration**: 2-3 days

**Four New Tools** (following exact pattern in `pkg/tools/registry.go`):

1. **read_file(coder_id, path)** - read file contents (max 1MB)
2. **list_files(coder_id, pattern)** - list files matching pattern (max 1000 results)
3. **get_diff(coder_id, path?)** - git diff vs origin/main
4. **submit_reply(response)** - explicit iteration termination (like `done` tool)

**Implementation Pattern** (identical to existing tools):
```go
// Each tool implements the Tool interface
type ReadFileTool struct {
    executor execpkg.Executor
    config   ReadFileConfig
}

func (t *ReadFileTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    // Extract args
    // Execute command in architect container via executor
    // Return structured result
}
```

**Tool Registry** (`pkg/tools/constants.go`):
```go
// Architect read tools
const (
    ToolReadFile    = "read_file"
    ToolListFiles   = "list_files"
    ToolGetDiff     = "get_diff"
    ToolSubmitReply = "submit_reply"
)

var ArchitectReadTools = []string{
    ToolReadFile,
    ToolListFiles,
    ToolGetDiff,
    ToolSubmitReply,
}
```

**Deliverables**:
- [x] `pkg/tools/read_file.go` + tests
- [x] `pkg/tools/list_files.go` + tests
- [x] `pkg/tools/get_diff.go` + tests
- [x] `pkg/tools/submit_reply.go` + tests
- [x] Registration in `pkg/tools/registry.go`
- [x] Tool documentation generation

---

### Phase 4a: Architect State Iteration Pattern
**Duration**: 2 days

**Follow Exact Coder Pattern** with soft/hard limit escalation!

**Iteration Limit Strategy**:
- **Soft Limit (8 iterations)**: Add warning message to context, give architect one more chance
- **Hard Limit (16 iterations = 2x soft)**: Transition to ESCALATE state for human review
- This IS the architect's budget review mechanism!

**Pattern Structure** (from `pkg/coder/planning.go` and `coding.go`):
```go
func (a *Architect) handleSCOPING(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    const SoftLimit = 8
    const HardLimit = 16  // 2x soft limit

    // 1. Increment and check iteration budget
    iterationCount := incrementAndGetCounter(sm, "scoping_iterations")

    // Hard limit - escalate to human
    if iterationCount >= HardLimit {
        a.logger.Warn("SCOPING hard limit (%d) exceeded, escalating to human", HardLimit)
        // ESCALATE state will use chat system to notify human
        return StateESCALATED, false, nil
    }

    // Soft limit - warn and give one more chance
    if iterationCount >= SoftLimit {
        a.contextManager.AddMessage("system",
            "⚠️ ITERATION LIMIT REACHED: You have reached your iteration limit. "+
            "You must complete your analysis with this response and call submit_reply to move on. "+
            "If you cannot complete the analysis, the request will be escalated to a human for review.")
    }

    // 2. Create tool provider for this state
    if a.scopingToolProvider == nil {
        a.scopingToolProvider = a.createScopingToolProvider()
    }

    // 3. **CRITICAL**: Flush context for new state (different from coder!)
    a.contextManager.ResetForNewState() // Force flush, not template-based

    // 4. Render template with tool documentation
    prompt, err := a.renderer.RenderWithUserInstructions(
        templates.ArchitectScopingTemplate,
        templateData,
        a.workDir,
        "ARCHITECT",
    )

    // 5. Build messages with context
    messages := a.buildMessagesWithContext(prompt)

    // 6. Call LLM with tools
    req := agent.CompletionRequest{
        Messages:  messages,
        MaxTokens: 8192,
        Tools:     a.getScopingToolsForLLM(),
    }
    resp, err := a.llmClient.Complete(ctx, req)

    // 7. Handle response
    if err := a.handleLLMResponse(resp); err != nil {
        return proto.StateError, false, err
    }

    // 8. Process tool calls
    if len(resp.ToolCalls) > 0 {
        return a.processScopingToolCalls(ctx, sm, resp.ToolCalls)
    }

    // 9. Continue in same state (FSM will call this again)
    return StateSCOPING, false, nil
}

func (a *Architect) processScopingToolCalls(ctx context.Context, sm *agent.BaseStateMachine, toolCalls []agent.ToolCall) (proto.State, bool, error) {
    for i := range toolCalls {
        toolCall := &toolCalls[i]

        // Check for submit_reply (termination signal)
        if toolCall.Name == tools.ToolSubmitReply {
            response := toolCall.Parameters["response"].(string)
            // Store response and transition to next state
            sm.SetStateData("scoping_result", response)
            return StateSTORY_GENERATION, false, nil
        }

        // Execute other tools
        tool, err := a.scopingToolProvider.Get(toolCall.Name)
        if err != nil {
            a.logger.Error("Tool not found: %s", toolCall.Name)
            continue
        }

        result, err := tool.Exec(ctx, toolCall.Parameters)

        // Add result to context
        a.addToolResultToContext(*toolCall, result)
    }

    // Continue in same state
    return StateSCOPING, false, nil
}
```

**Key Differences from Coder**:
1. **Soft/hard limit escalation** - soft limit warns, hard limit → ESCALATE state (not fatal!)
2. **Context flush between states** - must not leak context between coders
3. **No completion heuristics** - architect must explicitly call `submit_reply`

**States to Update**:
- `pkg/architect/scoping.go` - SCOPING state (uses LLM)
- `pkg/architect/request.go` - REQUEST state (code review, uses LLM)

**Note**: MONITORING state does NOT use LLM - just routes messages from coders to REQUEST

**Context Management** (CRITICAL):
```go
// Add to pkg/contextmgr/context_manager.go
func (cm *ContextManager) ResetForNewState() {
    // Force complete flush - don't check template name
    cm.messages = []Message{}
    cm.currentTemplate = ""
}

// Architect uses this instead of ResetForNewTemplate()
// Ensures no context leakage between coders
```

**Configuration** (`config.json`):
```json
{
  "orchestrator": {
    "max_coders": 3
  },
  "architect": {
    "scoping_soft_limit": 8,
    "scoping_hard_limit": 16,
    "request_soft_limit": 8,
    "request_hard_limit": 16,
    "tool_timeout_sec": 30
  }
}
```

**Deliverables**:
- [x] Helper methods in `pkg/architect/driver.go` - iteration limit checking, tool provider creation
- [x] Update `pkg/architect/request.go` - add iterative approval and question handling
- [x] Tool call processing with logging integration
- [x] Prompt generation for code review, completion, and questions
- [x] Response building from submit_reply signal
- [x] Configuration loading for iteration limits

---

### Phase 5: Chat Escalation Support
**Duration**: 2 days

**Goal**: Enable architect to escalate to humans via chat when hard iteration limit reached.

**Chat Schema Enhancements**:
```go
// pkg/chat/message.go
type PostType string

const (
    PostTypeChat     PostType = "chat"     // Regular chat message
    PostTypeReply    PostType = "reply"    // Reply to another message
    PostTypeEscalate PostType = "escalate" // Escalation requiring human response
)

type Message struct {
    ID        string    `json:"id"`
    AgentID   string    `json:"agent_id"`
    Content   string    `json:"content"`
    Timestamp time.Time `json:"timestamp"`
    ReplyTo   *string   `json:"reply_to"`   // NEW: Message ID being replied to (for threading)
    PostType  PostType  `json:"post_type"`  // NEW: Type of post
}
```

**Escalation Flow**:
1. Architect reaches hard limit → transitions to ESCALATE state
2. ESCALATE state handler:
   - Posts escalation message to chat with `post_type="escalate"`
   - Blocks waiting for human reply (message with `reply_to` pointing to escalation)
   - Receives human guidance
   - Returns to origin state with guidance added to context

**ESCALATE State Handler**:
```go
func (a *Architect) handleESCALATE(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // Get origin state and iteration count
    originState := utils.GetStateValueOr[string](sm, "origin_state", "")
    iterationCount := utils.GetStateValueOr[int](sm, originState+"_iterations", 0)

    // Post escalation message to chat
    escalationMsg := fmt.Sprintf(
        "🚨 ESCALATION: I have reached my iteration limit (%d) in %s state and need human guidance.\n\n"+
        "Context: [summary of what architect was trying to do]\n\n"+
        "Please provide guidance on how to proceed.",
        iterationCount, originState)

    msgID, err := a.chatService.PostMessage(ctx, a.agentID, escalationMsg, chat.PostTypeEscalate)
    if err != nil {
        return proto.StateError, false, fmt.Errorf("failed to post escalation: %w", err)
    }

    // Wait for human reply
    a.logger.Info("Architect escalated to human, waiting for reply to message %s", msgID)
    reply, err := a.waitForChatReply(ctx, msgID)
    if err != nil {
        return proto.StateError, false, fmt.Errorf("failed to receive escalation reply: %w", err)
    }

    // Add human guidance to context
    a.contextManager.AddMessage("human-guidance", reply.Content)

    // Reset iteration counter for origin state
    sm.SetStateData(originState+"_iterations", 0)

    // Return to origin state
    return proto.State(originState), false, nil
}
```

**WebUI Changes**:
- Display escalations prominently (red badge, top of message list)
- Allow replying to specific messages (thread view)
- Visual distinction for escalations vs regular chat
- Notification when new escalation arrives

**Database Schema**:
```sql
-- Add columns to chat_messages table
ALTER TABLE chat_messages ADD COLUMN reply_to TEXT;
ALTER TABLE chat_messages ADD COLUMN post_type TEXT DEFAULT 'chat';
CREATE INDEX idx_chat_messages_reply_to ON chat_messages(reply_to);
CREATE INDEX idx_chat_messages_post_type ON chat_messages(post_type);
```

**Deliverables**:
- [ ] Update `pkg/chat/message.go` - add ReplyTo and PostType fields
- [ ] Update chat service API - support posting escalations and replies
- [ ] Implement `pkg/architect/escalate.go` - ESCALATE state handler
- [ ] Add `waitForChatReply()` helper to architect
- [ ] Database migration for chat schema changes
- [ ] WebUI: Display escalations prominently
- [ ] WebUI: Reply threading UI
- [ ] WebUI: Visual treatment for escalations
- [ ] Integration tests for escalation flow

---

### Phase 4c: Shared Tool Logging Infrastructure
**Duration**: 0.5 days (emergent implementation)

**Goal**: Centralize tool execution logging for all agents

**Implementation**:
- Created `pkg/agent/tool_logging.go` with shared `LogToolExecution()` function
- Extracts tool execution metadata (params, result, duration, success/failure)
- Persists to database via fire-and-forget persistence queue
- Supports both shell tools and generic tools
- Session-aware logging with automatic session_id injection

**Refactoring**:
- Moved coder's `logToolExecution()` to use shared implementation
- Added architect tool logging in `processArchitectToolCalls()`
- Both agents now share same audit trail infrastructure

**Deliverables**:
- [x] `pkg/agent/tool_logging.go` - shared logging function
- [x] Refactor `pkg/coder/driver.go` to use shared logging
- [x] Add logging to `pkg/architect/driver.go` tool processing
- [x] Database integration via persistence channel

---

### Phase 6: Testing & Validation
**Duration**: 2 days

**Unit Tests**:
- Tool implementations (>85% coverage)
- Architect state handlers with iteration
- Context management (verify flush between states)

**Integration Tests**:
```go
func TestArchitectReadAccess(t *testing.T)
func TestArchitectIterationPattern(t *testing.T)
func TestContextFlushBetweenStates(t *testing.T)
func TestToolExecution(t *testing.T)
func TestIterationLimitEnforcement(t *testing.T)
```

**E2E Test**:
- Start orchestrator with architect + 2 coders
- Coders work on stories (create files)
- Architect uses read tools during code review
- Verify no context leakage between reviews

**Deliverables**:
- [ ] Unit tests for all new code
- [ ] Integration tests
- [ ] E2E test with realistic scenario
- [ ] Performance validation (tool latency <500ms)

---

### Phase 7: Documentation
**Duration**: 1 day

**Updates Required**:
- `CLAUDE.md` - architect containerization notes
- `docs/ARCHITECTURE.md` - container topology
- `docs/ARCHITECT_READ_ACCESS_SPEC.md` - implementation notes

**Deliverables**:
- [ ] Updated documentation
- [ ] Code comments and godoc
- [ ] Rollout checklist verification

---

## Total Estimated Effort

**14-18 days** (3-4 weeks)

Breakdown:
- Phase 1: 1 day (workspace pre-creation) ✅
- Phase 2: 3-4 days (architect containerization) ✅
- Phase 3: 2-3 days (MCP read tools) ✅
- Phase 4a: 2 days (iteration pattern helpers) ✅
- Phase 4b: 2 days (iterative approval implementation) ✅
- Phase 4c: 0.5 days (shared tool logging - emergent) ✅
- Phase 5: 2 days (chat escalation) 🚧
- Phase 6: 2-3 days (testing & validation) 📋
- Phase 7: 1 day (documentation) 📋

**Current Progress**: ~60% complete (Phases 1-4c done)

---

## Critical Design Decisions

### 1. No Workspace Swap During Architect Operations
**Decision**: Workspace swap happens in coder SETUP phase (before work begins)

**Rationale**:
- Architect reads stable workspaces between stories
- Coders do fresh clone at start of each story
- No race conditions (architect reads, never writes)

### 2. No Dynamic Mount Management
**Decision**: Mount all workspace directories at architect startup (empty or not)

**Rationale**:
- Workspaces persist regardless of coder lifecycle
- Empty directories are fine (no content to read)
- Simpler than restarting containers

### 3. Context Flush Between States
**Decision**: Architect flushes context when transitioning between states

**Rationale**:
- Each state serves a different coder's request
- Must not leak context from one coder to another
- Different from coder (serves one story throughout lifecycle)

### 4. Soft/Hard Limit Escalation Strategy
**Decision**: Soft limit (8) warns, hard limit (16) escalates to human via chat

**Rationale**:
- Architect can't review itself - humans are the budget reviewer
- Soft limit gives architect a chance to wrap up gracefully
- Hard limit prevents infinite loops while keeping system running
- Chat-based escalation is elegant and uses existing infrastructure
- Non-fatal: system continues running, human provides guidance

**Flow**:
1. Iteration 1-7: Normal operation
2. Iteration 8+: Warning message added to context
3. Iteration 16+: Transition to ESCALATE state → post to chat → wait for human reply → return to origin state with guidance

### 5. Workspace Pre-Creation Based on Config
**Decision**: Pre-create exactly `max_coders` workspace directories at startup

**Rationale**:
- We know we'll need them - not wasteful
- Simplifies mount configuration (no dynamic updates)
- Empty directories are fine until coders populate them
- Scales appropriately based on deployment (3 vs 10 coders)

### 6. Explicit Termination via submit_reply
**Decision**: Architect must call `submit_reply()` to exit iteration loop

**Rationale**:
- Clear control over when to stop iterating
- Consistent with coder's `done` tool pattern
- Avoids ambiguity about completion

### 7. Chat-Based Escalation
**Decision**: Use existing chat system for architect escalations (not new infrastructure)

**Rationale**:
- Reuses existing agent-human communication channel
- Simple additions: `reply_to` field + `post_type` enum
- WebUI already displays chat - just needs escalation highlighting
- Elegant: architect posts, blocks, waits for reply, continues

---

## Design Questions (Answered)

1. **Context Flush Strategy**: Always flush context between state transitions (prevents leakage between coders)
2. **Iteration Limits**: Per-state constants with soft/hard limits (scoping: 8/16, request: 8/16)
3. **Workspace Pre-Creation**: Based on `max_coders` config (not hardcoded 10)
4. **Escalation on Iteration Limit**: Soft limit warns, hard limit escalates to human via chat (not fatal!)
5. **Mirror Update Timing**: After story completion (already implemented)
6. **Knowledge Pack**: Message templates and data structures in `pkg/templates` and `pkg/proto`
7. **Tool Availability**: Only in LLM-using states (SCOPING, REQUEST) - NOT in MONITORING
8. **Workspace Swap Trigger**: Coder SETUP phase, just-in-time before work begins
9. **MONITORING State**: Does NOT use LLM - just message routing from coders to REQUEST

---

## Summary

This implementation adds architect read access to coder workspaces through:

1. **Container-based isolation** - Architect runs in container with read-only mounts
2. **Four new MCP tools** - read_file, list_files, get_diff, submit_reply
3. **Iteration pattern** - Following exact coder PLANNING/CODING pattern
4. **Graceful escalation** - Soft/hard limits with chat-based human escalation
5. **Context management** - Flush between states to prevent leakage

**Key Innovations**:
- Uses existing CloneManager for workspace atomicity
- Leverages existing chat system for escalations
- Follows proven coder state machine pattern
- Non-fatal escalation keeps system running

**Ready to begin implementation at Phase 1.**
