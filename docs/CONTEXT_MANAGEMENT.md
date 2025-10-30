# Context Management Architecture

## Overview

This document describes the context management architecture for the Maestro multi-agent orchestration system. Context management is responsible for maintaining conversation history between agents and LLMs, handling message accumulation, role-based caching, and ensuring proper message alternation for different LLM APIs.

## Current Status

**Phase 1: COMPLETE - Ready for Production Testing**

All Phase 1 improvements have been implemented and compile successfully:

✅ **Anthropic Alternation Handling**:
- Alternation preflight validator (`pkg/agent/internal/llmimpl/anthropic/client.go:91-186`)
- Pre-send request linter (`pkg/agent/internal/llmimpl/anthropic/client.go:46-89`)
- System message extraction to top-level parameter
- Consecutive non-assistant message consolidation
- Strict user↔assistant alternation validation

✅ **Temperature Settings**:
- Defined temperature constants in `pkg/agent/llm/api.go`:
  - `TemperatureDefault = 0.3` (for planning, reviews, architect)
  - `TemperatureDeterministic = 0.0` (for code generation)
- Default changed from 0.7 → 0.3 for better focus
- Coding phase uses 0.0 for deterministic output
- Planning phase uses 0.3 (default) for exploration
- Plan review uses 0.0 for deterministic todo collection
- Architect functions use 0.3 (default) for judgment/reasoning
- Temperature parameter support added to Anthropic client

✅ **Prompt Improvements**:
- "If stuck" rule added to both coding templates
- Instructs agents to use `ask_question` or `chat_post` when blocked

✅ **Compaction**:
- Already enabled with system prompt preservation at index 0
- Verified to never remove system prompt during compaction

✅ **Build Verification**:
- All changes compile successfully
- Linting checks pass

**Status**: ⚠️ Code changes are complete but have not been runtime-tested. Ready for production testing to verify LLM integration.

**What's Deferred (Phase 2)**:
- OpenAI prompt caching support
- Multi-breakpoint caching strategy
- Rolling summaries with structured templates
- Type-aware tool result caps (8-32KB by type)

## Design Principles

1. **LLM-Agnostic Core**: Generic context management code maintains semantic roles without awareness of specific LLM API requirements
2. **LLM-Specific Adaptation**: Each LLM implementation handles role mapping, alternation rules, and caching strategies appropriate for its API
3. **Separation of Concerns**: Context accumulation, role semantics, and API formatting are handled at different layers
4. **Preserve Fidelity**: Semantic information (roles, message boundaries) is preserved as long as possible before API-specific transformations

## Architecture Layers

### Layer 1: Context Manager (LLM-Agnostic)

**Location**: `pkg/contextmgr/`

**Responsibilities**:
- Maintain conversation history as a sequence of messages
- Support message accumulation during agent turns (user buffer)
- Preserve semantic roles: `SYSTEM`, `USER`, `ASSISTANT`, `TOOL`
- Provide context compaction when approaching token limits
- No knowledge of LLM-specific APIs or requirements

**Key Structures**:

```go
type Message struct {
    Role    string  // Semantic role: "system", "user", "assistant", "tool"
    Content string  // Message content
}

type Fragment struct {
    Role      string    // Semantic role for this fragment
    Content   string    // Fragment content
    Timestamp time.Time // When fragment was created
}

type ContextManager struct {
    messages   []Message  // Conversation history
    userBuffer []Fragment // Accumulator for current turn
}
```

**Message Accumulation Flow**:

1. **During Agent Turn**: Multiple events occur (tool executions, updates, etc.)
   ```
   AddMessage("tool", "shell command result 1")
   AddMessage("tool", "shell command result 2")
   AddMessage("user", "todo status update")
   ```

2. **Before LLM Request**: `FlushUserBuffer()` is called
   - Consolidates accumulated fragments into individual messages
   - Each fragment becomes a separate `Message` with its semantic role preserved
   - Clears the buffer for next turn

3. **After LLM Response**: `AddAssistantMessage()` adds LLM response to history
   ```
   messages = [
       Message{Role: "system", Content: "..."},
       Message{Role: "tool", Content: "shell result 1"},
       Message{Role: "tool", Content: "shell result 2"},
       Message{Role: "user", Content: "todo update"},
       Message{Role: "assistant", Content: "LLM response"}
   ]
   ```

**Key Point**: Context manager maintains separate messages with semantic roles. It does NOT enforce alternation or collapse messages.

### Layer 2: Agent Message Building (LLM-Agnostic)

**Location**: `pkg/coder/driver.go`, `pkg/architect/driver.go`

**Responsibilities**:
- Retrieve messages from context manager
- Build `CompletionRequest` structure for LLM client
- Pass messages through with semantic roles intact
- No role mapping or alternation handling

**Example**:
```go
func (c *Coder) buildMessagesWithContext(initialPrompt string) []agent.CompletionMessage {
    messages := []agent.CompletionMessage{
        {Role: "system", Content: initialPrompt},
    }

    // Get conversation history from context manager
    contextMessages := c.contextManager.GetMessages()

    for _, msg := range contextMessages {
        messages = append(messages, agent.CompletionMessage{
            Role:    msg.Role,  // Preserve semantic role
            Content: msg.Content,
        })
    }

    return messages
}
```

**Key Point**: No transformation of roles at this layer. Messages flow through with their semantic roles.

### Layer 3: LLM Client Implementation (LLM-Specific)

**Location**: `pkg/agent/internal/llmimpl/anthropic/`, `pkg/agent/internal/llmimpl/openai/`

**Responsibilities**:
- Map semantic roles to LLM-specific API roles
- Enforce LLM-specific alternation rules
- Apply LLM-specific caching strategies
- Transform message structure to API format

#### Anthropic Implementation

**API Characteristics**:
- Supports: `user`, `assistant` roles
- System messages: Special parameter (not in messages array) OR first user message
- Alternation: Strict user ↔ assistant alternation required
- Caching: Prompt caching with `cache_control` markers
- Tool results: Embedded as content blocks within messages

**Role Mapping**:
```
SYSTEM    → system parameter (or prepend to first user message)
USER      → user
ASSISTANT → assistant
TOOL      → user (embedded as tool_result content block)
```

**Alternation Handling**:

When receiving messages from generic layer:
```
[system, tool, tool, user, assistant, tool, tool, user]
```

Anthropic client must ensure alternation:
```
Step 1: Extract system message
  system = "system prompt"
  remaining = [tool, tool, user, assistant, tool, tool, user]

Step 2: Consolidate consecutive non-assistant messages
  [tool, tool, user] → single user message
  remaining = [user, assistant, tool, tool, user]

Step 3: Consolidate again if needed
  [tool, tool, user] → single user message
  final = [user, assistant, user]

Step 4: Build API request
  {
    system: "system prompt",
    messages: [
      {role: "user", content: "tool1\n\ntool2\n\nuser"},
      {role: "assistant", content: "..."},
      {role: "user", content: "tool3\n\ntool4\n\nuser"}
    ]
  }
```

**Caching Strategy**:

Anthropic supports prompt caching with breakpoints. The client applies caching based on semantic roles:

```go
func (c *ClaudeClient) Complete(ctx context.Context, in llm.CompletionRequest) {
    // Apply cache control to system message
    if msg.Role == "system" {
        msg.CacheControl = &CacheControl{Type: "ephemeral"}
    }

    // Optionally: Mark last message in cacheable region
    // (messages that don't change frequently)
    if isLastCacheableMessage {
        msg.CacheControl = &CacheControl{Type: "ephemeral"}
    }
}
```

**Key Strategy**: System messages are marked for caching since they contain persistent instructions. Dynamic content (tool results, transient conversation) is not cached.

#### OpenAI Implementation (Phase 2)

**API Characteristics**:
- Supports: `system`, `user`, `assistant`, `tool` roles
- System messages: Can appear anywhere in conversation
- Alternation: More flexible - allows `assistant` → `tool` → `tool` sequences
- Caching: **Supported** - Automatic prefix caching for prompts ≥1,024 tokens with optional `prompt_cache_key`
- Tool results: Separate `tool` role messages with `tool_call_id`

**Role Mapping**:
```
SYSTEM    → system
USER      → user
ASSISTANT → assistant
TOOL      → tool
```

**Alternation Handling**:

OpenAI's API allows more flexible sequencing:
```
Input: [system, tool, tool, user, assistant, tool, tool, user]

OpenAI can handle:
  [system, user, assistant, tool, tool, user, assistant]

Because tool messages can follow assistant messages directly.
```

**Strategy**: Minimal consolidation needed. Tool messages can remain separate if they follow an assistant message that made tool calls.

**Caching Strategy**: Use `prompt_cache_key` derived from story/session to stabilize routing. OpenAI automatically caches common prefixes ≥1,024 tokens. Track `cached_tokens` in usage metrics.

**Note**: OpenAI support is deferred to Phase 2. Current focus is on Anthropic implementation.

## Role Semantics

### SYSTEM
**Meaning**: Persistent, dominant instructions that guide agent behavior

**Examples**:
- System prompts ("You are a coding agent...")
- Story requirements ("Implement user authentication...")
- Implementation plans ("Step 1: Create database schema...")

**Characteristics**:
- Should persist across conversation
- Rarely changes during a story
- Prime candidate for caching (if LLM supports it)
- May be repeated/refreshed but content is relatively stable

**LLM-Specific Handling**:
- Anthropic: System parameter or prepend to first user message, cache it
- OpenAI: System message in conversation, no caching

### USER
**Meaning**: Input from user or user-side events

**Examples**:
- Human input
- Task assignments from architect to coder
- Status updates
- Non-tool-related feedback

**Characteristics**:
- Part of conversational flow
- Can accumulate during a turn
- Should maintain alternation with assistant

**LLM-Specific Handling**:
- Anthropic: User messages, consolidate if needed for alternation
- OpenAI: User messages, less consolidation needed

### ASSISTANT
**Meaning**: Responses from the LLM

**Examples**:
- Generated text
- Tool call requests
- Reasoning and explanations

**Characteristics**:
- Always from LLM
- May contain tool calls
- Must alternate with user messages (in most APIs)

**LLM-Specific Handling**:
- Anthropic: Assistant messages with content/tool_use blocks
- OpenAI: Assistant messages with tool_calls array

### TOOL
**Meaning**: Results from tool executions

**Examples**:
- Shell command output
- File read/write results
- Database query results
- Container build output

**Characteristics**:
- Generated by tool execution, not by user or LLM
- Often multiple tool results per turn
- Contains structured data (exit codes, stdout, stderr, etc.)
- Frequently changes (dynamic content, not cacheable)

**LLM-Specific Handling**:
- Anthropic: Converted to user messages (tool_result content blocks), consolidated for alternation
- OpenAI: Separate tool messages with tool_call_id, can remain separate

## Message Flow Example

### Scenario: Coder Agent Executes 3 Shell Commands

**Step 1: Context Manager Accumulation**
```
Agent calls:
  contextManager.AddMessage("tool", "Command: ls\nOutput: file1.go file2.go")
  contextManager.AddMessage("tool", "Command: cat file1.go\nOutput: package main...")
  contextManager.AddMessage("tool", "Command: go build\nOutput: Success")

UserBuffer state:
  [
    Fragment{Role: "tool", Content: "Command: ls..."},
    Fragment{Role: "tool", Content: "Command: cat..."},
    Fragment{Role: "tool", Content: "Command: go build..."}
  ]
```

**Step 2: Flush Before LLM Request**
```
contextManager.FlushUserBuffer()

Messages state:
  [
    Message{Role: "system", Content: "You are a coding agent..."},
    Message{Role: "assistant", Content: "I'll execute these commands..."},
    Message{Role: "tool", Content: "Command: ls..."},
    Message{Role: "tool", Content: "Command: cat..."},
    Message{Role: "tool", Content: "Command: go build..."}
  ]
```

**Step 3: Agent Builds Request**
```
messages := buildMessagesWithContext(systemPrompt)
// Returns CompletionMessage array with semantic roles preserved
```

**Step 4A: Anthropic Client Processing**
```
Input: [system, assistant, tool, tool, tool]

Transform:
1. Extract system → cache it
2. Consolidate [tool, tool, tool] → single user message
   Content: "Command: ls...\n\nCommand: cat...\n\nCommand: go build..."

Output to API:
{
  system: "You are a coding agent..." (with cache_control),
  messages: [
    {role: "assistant", content: "I'll execute these commands..."},
    {role: "user", content: "Command: ls...\n\nCommand: cat...\n\nCommand: go build..."}
  ]
}
```

**Step 4B: OpenAI Client Processing**
```
Input: [system, assistant, tool, tool, tool]

Transform:
1. Keep system as system message
2. Map assistant → assistant
3. Map tool → tool (can keep separate because OpenAI supports it)
4. Link tools to assistant's tool_calls via tool_call_id

Output to API:
{
  messages: [
    {role: "system", content: "You are a coding agent..."},
    {role: "assistant", content: "I'll execute these commands...", tool_calls: [...]},
    {role: "tool", tool_call_id: "1", content: "Command: ls..."},
    {role: "tool", tool_call_id: "2", content: "Command: cat..."},
    {role: "tool", tool_call_id: "3", content: "Command: go build..."}
  ]
}
```

## Caching Strategy (Anthropic-Specific)

### Why Cache?

LLM APIs charge per token for input. For iterative workflows (like multi-turn coding), we repeatedly send similar content:
- System prompt (rarely changes)
- Story requirements (unchanged during story)
- Implementation plan (unchanged during story)
- Tool results (change every turn)

**Without caching**: Pay full price for all tokens every request
**With caching**: Pay reduced price for cached tokens after first request

### Anthropic Prompt Caching

Anthropic supports prompt caching with explicit cache breakpoints:

```json
{
  "system": "Long system prompt...",
  "messages": [
    {
      "role": "user",
      "content": "Story requirements...",
      "cache_control": {"type": "ephemeral"}  // ← Cache breakpoint
    },
    {
      "role": "assistant",
      "content": "..."
    },
    {
      "role": "user",
      "content": "Tool results (dynamic, don't cache)"
    }
  ]
}
```

**Effect**: Everything up to (and including) the marked message is cached. Subsequent requests with identical prefix hit the cache.

### Cache Breakpoint Strategy

**Rule**: Place cache breakpoint at the last SYSTEM-role message before dynamic content begins.

**Reasoning**:
- System messages contain stable instructions
- Tool results, todo updates, and conversational turns are dynamic
- Place breakpoint after stable content, before dynamic content

**Implementation** (in Anthropic client):
```go
// Find last system-role message
lastSystemIndex := -1
for i, msg := range messages {
    if msg.Role == "system" {
        lastSystemIndex = i
    }
}

// Mark it for caching
if lastSystemIndex >= 0 {
    messages[lastSystemIndex].CacheControl = &CacheControl{Type: "ephemeral"}
}
```

### Expected Savings

**Scenario**: 8-iteration coding session
- System prompt: 3000 tokens (cached)
- Story/plan: 2000 tokens (cached)
- Tool results: 500 tokens per iteration (not cached)

**Without caching**:
- 8 requests × 5500 tokens = 44,000 tokens

**With caching**:
- Request 1: 5500 tokens (cache miss, full price)
- Requests 2-8: 500 tokens each (cache hit, only dynamic content)
- Total: 5500 + (7 × 500) = 9000 tokens

**Savings**: 79% reduction in input tokens

## Implementation Roadmap

### Phase 1: Anthropic Focus (✅ COMPLETE)

**Context Manager**
- [x] Add Provenance field to Message struct
- [x] FlushUserBuffer preserves provenance through consolidation
- [x] Re-enable compaction with system prompt preservation
- [x] Ensure compaction only removes messages after system prompt (index 0)

**Agent Layer**
- [x] buildMessagesWithContext preserves provenance for cache decisions
- [x] No role mapping at this layer (passes through semantic roles)

**Anthropic Client**
- [x] Implement alternation preflight validator (`client.go:91-186`)
  - Extract system to top-level parameter
  - Merge consecutive non-assistant messages into single user message
  - Ensure strict user ↔ assistant alternation
  - Validate sequence ends with user message
- [x] Implement pre-send request linter (`client.go:46-89`)
  - Check: no system role in messages array
  - Check: alternation is correct
  - Check: only valid roles (user/assistant)
  - Returns errors for violations before API call
- [x] Temperature constants defined (`pkg/agent/llm/api.go:29-35`)
  - `TemperatureDefault = 0.3` (planning, reviews, architect)
  - `TemperatureDeterministic = 0.0` (code generation)
- [x] Temperature parameter support added to client (`client.go:253`)
- [x] Temperature settings applied:
  - Coding: 0.0 (`coding.go:135`)
  - Planning: 0.3 (default, `planning.go:136`)
  - Plan review: 0.0 (`plan_review.go:251,346`)
  - Architect: 0.3 (default, uses NewCompletionRequest)

**Prompt Templates**
- [x] Add "If stuck" rule to both coding templates
  - `app_coding.tpl.md:73`
  - `devops_coding.tpl.md:83`
- [x] Tool-only output constraint already present
- [x] Todo status at end (Tier-3, dynamic)

**Build Status**
- [x] All changes compile successfully
- [x] Linting checks pass
- ⚠️ Runtime testing pending (ready for production)

### Phase 2: OpenAI Support (Deferred)

**OpenAI Client**
- [ ] Add prompt caching support (`prompt_cache_key`)
- [ ] Track cached token metrics
- [ ] Implement tool_call_id validation and ordering
- [ ] Support parallel tool calls with deterministic ordering
- [ ] Pre-send linter for OpenAI-specific requirements

**Advanced Features (Deferred)**
- [ ] Multi-breakpoint caching (Anthropic)
- [ ] Rolling summaries with structured templates
- [ ] Type-aware tool result caps (8-32KB by type)
- [ ] SHA-256 hashing and artifact references
- [ ] Deterministic normalization for cache stability

## Testing Strategy

### Unit Tests
- Context manager: Verify separate messages created with correct roles
- Anthropic client: Verify consolidation and alternation
- OpenAI client: Verify role mapping and tool linking

### Integration Tests
- End-to-end coder workflow with multiple tool executions
- Verify Anthropic requests have proper alternation
- Verify OpenAI requests use tool role correctly
- Verify cache breakpoints are placed correctly

### Observability
- Log message sequences at each layer
- Track cache hit rates (Anthropic)
- Monitor token usage reduction
- Alert on alternation violations

## Design Decisions

### Resolved

1. **Cache Breakpoint Optimization**: **Single breakpoint** for Phase 1 (on system message). Multi-breakpoint deferred to Phase 2.

2. **Tool Result Size**: Keep current **2KB truncation** at context manager layer. Type-aware caps (8-32KB) deferred to Phase 2.

3. **OpenAI Support**: Deferred to Phase 2. Focus Phase 1 on Anthropic implementation and validation.

4. **Compaction Strategy**: Re-enable existing simple removal strategy. Rolling summaries deferred to Phase 2.

5. **Temperature**: Verify coding agents use **0-0.3** temperature for deterministic output.

### Open Questions

1. **System Message Refresh**: Should we periodically refresh the system prompt even if cached? Or trust the cache for the duration of a story?

2. **Mixed Conversations**: What happens if we have SYSTEM messages interspersed in conversation (not just at the start)? Current design assumes system at index 0.

3. **Cache TTL**: Should we explore 1-hour cache TTL (beta) for longer stories, or stick with default 5-minute?

## References

- Anthropic API Documentation: https://docs.anthropic.com/claude/docs/prompt-caching
- OpenAI API Documentation: https://platform.openai.com/docs/guides/function-calling
- Maestro Context Manager: `pkg/contextmgr/contextmgr.go`
- LLM Client Interface: `pkg/agent/llm/api.go`
