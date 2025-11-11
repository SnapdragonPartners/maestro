# Tool Call/Result Architecture Refactor

## Problem Statement

Our current `CompletionMessage` abstraction only supports plain text content, forcing us to convert structured tool calls into text placeholders like `[list_files(pattern="*")]`. This breaks the expected API structure for both Anthropic and OpenAI, and causes issues with:

- Proper message alternation (user/assistant/user/assistant)
- Token efficiency (structured data is more compact)
- API validation and reliability
- Multi-tool scenarios where context is lost

While LLMs have been compensating for our malformed structure, we're hitting limits with complex flows like PM's `chat_ask_user` which requires maintaining conversation context.

## Current Architecture

### Message Types (`pkg/agent/llm/api.go`)

```go
type CompletionMessage struct {
    Content      string
    CacheControl *CacheControl
    Role         CompletionRole  // "system", "user", "assistant"
}

type ToolCall struct {
    Parameters map[string]any
    ID         string
    Name       string
}

type CompletionResponse struct {
    ToolCalls  []ToolCall
    Content    string
    StopReason string
}
```

### Current Flow

1. **LLM Response**: Returns `CompletionResponse` with `ToolCalls` array
2. **Tool Call Conversion**: We convert to text placeholder (e.g., `"[list_files(pattern=\"*\")]"`)
3. **Tool Execution**: Tool executes, result added to userBuffer
4. **Next Turn**: userBuffer flushed to "user" role message with text content

**Example from PM (`pkg/pm/working.go:199-236`):**
```go
// Converting structured tool calls to text
if len(resp.ToolCalls) > 0 {
    var parts []string
    for i := range resp.ToolCalls {
        toolCall := &resp.ToolCalls[i]
        // ... extract parameters ...
        parts = append(parts, fmt.Sprintf("[%s(%s)]", toolCall.Name, strings.Join(paramStrs, ", ")))
    }
    assistantMessage := strings.Join(parts, " ")
    d.contextManager.AddAssistantMessage(assistantMessage)  // Just a string!
}
```

### Problems with Current Approach

1. **Lost Structure**: Tool calls are flattened to strings, losing type information
2. **Broken Alternation**: Tool results become separate user messages, creating `user → user` sequences
3. **Context Pollution**: Tool results like `"map[await_user:true message:Question posted...]"` appear in conversation
4. **Token Waste**: Text representations are verbose compared to structured data

## Proposed Solution

### 1. Extend Core Message Types

Add first-class support for tool calls and results in `CompletionMessage`:

```go
// pkg/agent/llm/api.go

type CompletionMessage struct {
    Content      string
    Role         CompletionRole
    CacheControl *CacheControl

    // NEW: Structured tool calls (for assistant messages)
    ToolCalls    []ToolCall

    // NEW: Structured tool results (for user messages)
    ToolResults  []ToolResult
}

type ToolResult struct {
    ToolCallID string  // Links back to the tool call
    Content    string  // Result data
    IsError    bool
}
```

### 2. Message Alternation Rules

**Critical**: When both tool results AND human input exist, they MUST be combined in a SINGLE user message:

```go
// CORRECT - One user message with both:
{
    Role: "user",
    Content: "general knowledge probably...",  // Human response
    ToolResults: [
        {ToolCallID: "call_1", Content: "file1.go\nfile2.go"},
    ]
}

// WRONG - Two user messages breaks alternation:
{Role: "user", ToolResults: [...]}
{Role: "user", Content: "general knowledge..."}  // Violates alternation!
```

### 3. Context Manager Changes

Track pending items and batch them into single user messages:

```go
// pkg/contextmgr/contextmgr.go

type ContextManager struct {
    messages           []Message
    userBuffer         []Fragment  // Existing
    chatService        ChatService // Existing
    chatCursor         int64       // Existing

    // NEW: Track pending items for batching
    pendingToolCalls   []ToolCall   // From last assistant message
    pendingToolResults []ToolResult // Accumulating results
}

// NEW: Add tool result to pending batch
func (cm *ContextManager) AddToolResult(toolCallID, content string, isError bool) {
    cm.pendingToolResults = append(cm.pendingToolResults, ToolResult{
        ToolCallID: toolCallID,
        Content:    content,
        IsError:    isError,
    })
}

// NEW: Add assistant message with tool calls
func (cm *ContextManager) AddAssistantMessageWithTools(content string, toolCalls []ToolCall) {
    cm.messages = append(cm.messages, Message{
        Role:      "assistant",
        Content:   content,
        ToolCalls: toolCalls,
    })
    cm.pendingToolCalls = toolCalls  // Track for result linking
}

// UPDATED: Flush combines everything into one user message
func (cm *ContextManager) FlushUserBuffer(ctx context.Context) error {
    // Step 1: Inject chat messages (human responses)
    if cm.chatService != nil && cm.agentID != "" {
        if err := cm.injectChatMessages(ctx); err != nil {
            // log warning but continue
        }
    }

    // Step 2: Combine pending tool results + userBuffer into ONE user message
    if len(cm.pendingToolResults) > 0 || len(cm.userBuffer) > 0 {
        // Build combined content from userBuffer fragments
        var combinedContent string
        if len(cm.userBuffer) > 0 {
            parts := make([]string, len(cm.userBuffer))
            for i := range cm.userBuffer {
                parts[i] = cm.userBuffer[i].Content
            }
            combinedContent = strings.Join(parts, "\n\n")
        }

        // Create single user message with both tool results and content
        cm.messages = append(cm.messages, Message{
            Role:        "user",
            Content:     combinedContent,  // Can be empty if only tool results
            ToolResults: cm.pendingToolResults,
        })

        // Clear pending state
        cm.pendingToolResults = nil
        cm.userBuffer = cm.userBuffer[:0]
    }

    // Step 3: Compaction
    return cm.CompactIfNeeded()
}
```

### 4. API Client Updates

Each client converts structured messages to native API format.

#### Anthropic Client (`pkg/agent/internal/llmimpl/anthropic/client.go`)

Anthropic uses content blocks within messages:

```go
// Convert CompletionMessage to Anthropic format
func convertMessage(msg llm.CompletionMessage) anthropic.MessageParam {
    var content []anthropic.ContentBlock

    // Add text content if present
    if msg.Content != "" {
        content = append(content, anthropic.TextBlock{
            Type: "text",
            Text: msg.Content,
        })
    }

    // Add tool calls (for assistant messages)
    for _, tc := range msg.ToolCalls {
        content = append(content, anthropic.ToolUseBlock{
            Type:  "tool_use",
            ID:    tc.ID,
            Name:  tc.Name,
            Input: tc.Parameters,
        })
    }

    // Add tool results (for user messages)
    for _, tr := range msg.ToolResults {
        content = append(content, anthropic.ToolResultBlock{
            Type:       "tool_result",
            ToolUseID:  tr.ToolCallID,
            Content:    tr.Content,
            IsError:    tr.IsError,
        })
    }

    return anthropic.MessageParam{
        Role:    string(msg.Role),
        Content: content,
    }
}
```

**Example Anthropic sequence:**
```go
// Assistant with tool call
{Role: "assistant", Content: [
    {Type: "text", Text: "Let me check the files..."},
    {Type: "tool_use", ID: "tool-1", Name: "list_files", Input: {"pattern": "*"}}
]}

// User with tool result + human response (SINGLE MESSAGE)
{Role: "user", Content: [
    {Type: "tool_result", ToolUseID: "tool-1", Content: "file1.go\nfile2.go"},
    {Type: "text", Text: "general knowledge probably..."}
]}
```

#### OpenAI Client (`pkg/agent/internal/llmimpl/openaiofficial/client.go`)

OpenAI uses separate messages with special "tool" role:

```go
func convertMessages(messages []llm.CompletionMessage) []openai.Message {
    var result []openai.Message

    for _, msg := range messages {
        switch msg.Role {
        case llm.RoleAssistant:
            // Assistant message with tool calls
            result = append(result, openai.Message{
                Role:      "assistant",
                Content:   msg.Content,
                ToolCalls: convertToolCalls(msg.ToolCalls),
            })

        case llm.RoleUser:
            // First add any tool results as "tool" role messages
            for _, tr := range msg.ToolResults {
                result = append(result, openai.Message{
                    Role:       "tool",
                    Content:    tr.Content,
                    ToolCallID: tr.ToolCallID,
                })
            }

            // Then add user content if present
            if msg.Content != "" {
                result = append(result, openai.Message{
                    Role:    "user",
                    Content: msg.Content,
                })
            }

        case llm.RoleSystem:
            result = append(result, openai.Message{
                Role:    "system",
                Content: msg.Content,
            })
        }
    }

    return result
}
```

**Example OpenAI sequence:**
```go
// Assistant with tool calls
{Role: "assistant", Content: "Let me check...", ToolCalls: [...]}

// Tool results (special "tool" role - doesn't break alternation in OpenAI)
{Role: "tool", ToolCallID: "call_1", Content: "file1.go\nfile2.go"}

// User response
{Role: "user", Content: "general knowledge probably..."}
```

**Note**: OpenAI's "tool" role is special and doesn't count as "user" for alternation purposes.

### 5. Agent Updates

#### PM Agent (`pkg/pm/working.go`)

**Current problematic code:**
```go
// Lines 199-236: Converting to text placeholders
d.contextManager.AddAssistantMessage("[list_files(pattern=\"*\")]")
d.contextManager.AddMessage("tool-result", fmt.Sprintf("%v", result))
```

**New approach:**
```go
// After LLM response
if len(resp.ToolCalls) > 0 {
    // Preserve structured tool calls in assistant message
    d.contextManager.AddAssistantMessageWithTools(resp.Content, resp.ToolCalls)
} else {
    d.contextManager.AddAssistantMessage(resp.Content)
}

// After tool execution
for i := range resp.ToolCalls {
    toolCall := &resp.ToolCalls[i]
    result, err := tool.Exec(ctx, toolCall.Parameters)

    if err != nil {
        d.contextManager.AddToolResult(toolCall.ID, err.Error(), true)
    } else {
        resultStr := formatToolResult(result)
        d.contextManager.AddToolResult(toolCall.ID, resultStr, false)
    }
}
```

#### Special Case: chat_ask_user

The `chat_ask_user` tool is unique because:
1. It posts a message to chat (side effect)
2. Returns a result we don't care about: `"Question posted, waiting..."`
3. Blocks immediately (exits to AWAIT_USER)

**Question**: Should `chat_ask_user` return a result at all? Options:

**Option A**: Return empty/minimal result since human response comes via chat injection
```go
func (a *ChatAskUserTool) Exec(ctx context.Context, params map[string]any) (any, error) {
    // Post message to chat
    // ...

    // Return minimal result - the actual response comes via chat injection
    return map[string]any{
        "success":    true,
        "await_user": true,
    }, nil
}
```

**Option B**: Return the question as the result for context
```go
return map[string]any{
    "success":    true,
    "question":   message,  // Include for context
    "await_user": true,
}, nil
```

**Recommendation**: Option A. The question text is already in the assistant message's tool call parameters, so including it in the result is redundant.

#### Architect Agent (`pkg/architect/driver.go`)

Similar changes to PM - currently uses text placeholders at line 509:
```go
placeholder := fmt.Sprintf("Tool %s invoked", strings.Join(toolNames, ", "))
d.contextManager.AddAssistantMessage(placeholder)
```

Should change to:
```go
d.contextManager.AddAssistantMessageWithTools(resp.Content, resp.ToolCalls)
```

#### Coder Agent (`pkg/coder/driver.go`)

Similar pattern - currently at line 595:
```go
placeholder := fmt.Sprintf("Tool %s invoked", strings.Join(toolNames, ", "))
c.contextManager.AddAssistantMessage(placeholder)
```

### 6. Multi-Party Chat Handling

For coding agents that communicate with each other via chat, messages from other agents should still be formatted as user messages with attribution:

```go
// In injectChatMessages
for _, msg := range newMessages {
    if msg.Author == "@human" {
        // Human message - add to pending content
        pendingHumanInput = msg.Text
    } else if msg.Author == expectedAgentAuthor {
        // Skip own messages - already in context as assistant messages
        continue
    } else {
        // Other agent message - format with attribution
        pendingHumanInput += fmt.Sprintf("\n[Chat from %s]: %s", msg.Author, msg.Text)
    }
}
```

This maintains the existing multi-party chat behavior while using the proper structured format.

## Implementation Plan

### Phase 1: Core Type Extensions
- [ ] Extend `CompletionMessage` with `ToolCalls` and `ToolResults` fields
- [ ] Add `ToolResult` type definition
- [ ] Verify no breaking changes to existing code (fields are optional)

### Phase 2: Context Manager Updates
- [ ] Add `pendingToolCalls` and `pendingToolResults` fields
- [ ] Implement `AddToolResult()` method
- [ ] Implement `AddAssistantMessageWithTools()` method
- [ ] Update `FlushUserBuffer()` to combine tool results + human input
- [ ] Add unit tests for batching behavior

### Phase 3: API Client Updates
- [ ] Update Anthropic client to convert structured messages to content blocks
- [ ] Update OpenAI client to convert structured messages to proper format
- [ ] Add debug logging for API payloads
- [ ] Test with both APIs

### Phase 4: Agent Updates (PM First)
- [ ] Update PM agent to use new methods
- [ ] Remove text placeholder generation
- [ ] Test PM end-to-end with chat_ask_user flow
- [ ] Verify conversation context is maintained

### Phase 5: Other Agents
- [ ] Update Architect agent
- [ ] Update Coder agent
- [ ] Test multi-tool scenarios
- [ ] Test multi-party chat (coder-to-coder)

### Phase 6: Cleanup and Documentation
- [ ] Remove old placeholder code
- [ ] Update architecture docs
- [ ] Add examples to developer guide
- [ ] Performance testing and token count comparison

## Testing Strategy

### Unit Tests
- ContextManager batching logic (tool results + human input)
- API client message conversion (Anthropic and OpenAI formats)
- Edge cases: empty content, no tool results, multiple tools

### Integration Tests
- PM agent: chat_ask_user flow with multiple questions
- Architect agent: read_file + list_files in single turn
- Coder agent: multi-tool usage in coding phase
- Multi-party chat: messages from other agents

### Validation
- Log and inspect actual API payloads sent to Anthropic/OpenAI
- Verify no alternation errors from APIs
- Compare token usage before/after refactor
- Ensure all existing functionality still works

## Rollback Plan

If issues arise:
1. The new fields are optional - old code continues to work
2. Can incrementally roll back agents one at a time
3. API clients can detect absence of new fields and fall back to old behavior

## Success Criteria

- [ ] No user/user or assistant/assistant alternation violations
- [ ] Tool call parameters visible in conversation context
- [ ] Tool results properly linked to their calls
- [ ] Token usage decreased (structured data is more efficient)
- [ ] All agents (PM, Architect, Coder) working correctly
- [ ] Multi-party chat still functional
- [ ] API clients handle both APIs correctly

## Open Questions

1. Should `chat_ask_user` return an empty result or include the question text?
2. How do we handle tool results that are too large? Truncation strategy?
3. Should we support parallel tool calls (multiple tools in one response)?
4. Do we need to preserve tool call order in the ToolResults array?
5. How do we handle tool execution failures? (Addressed via IsError flag)

## References

- Anthropic Tool Use API: https://docs.anthropic.com/claude/docs/tool-use
- OpenAI Function Calling: https://platform.openai.com/docs/guides/function-calling
- Current codebase: `pkg/agent/llm/api.go`, `pkg/contextmgr/contextmgr.go`
- Related issues: PM context loss, tool result pollution in logs
