# Tool Loop Abstraction

## Overview

The `pkg/agent/toolloop` package provides a reusable abstraction for LLM tool calling loops, eliminating code duplication across PM, Architect, and Coder agents. This pattern is used whenever an agent needs to:

1. Call an LLM with available tools
2. Execute the tools the LLM requests
3. Feed results back to the LLM
4. Repeat until a terminal condition is met

## Why Toolloop Exists

Before toolloop, each agent (PM, Architect, Coder) implemented its own tool calling loop with subtle differences:
- PM maintained context across iterations
- Architect created fresh context for each request
- All had similar iteration limits, error handling, and terminal detection logic

This led to:
- **Code duplication** across 3+ implementations
- **Bug propagation** (e.g., Anthropic message formatting issues)
- **Inconsistent behavior** between agents

Toolloop consolidates this into a single, well-tested implementation.

## How It Works

### Execution Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Initialize: Add initial prompt to context (if provided) │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 2. Build messages from ContextManager                       │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 3. Call LLM with tools and max tokens                       │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 4. Add assistant response to context                        │
│    - If tool calls: AddAssistantMessageWithTools()          │
│    - If no tools: AddAssistantMessage()                     │
└─────────────────────────────────────────────────────────────┘
                           ↓
               ┌─────────────────────┐
               │ No tool calls?      │
               └─────────────────────┘
                    ↓           ↓
                  YES          NO
                    ↓           ↓
         ┌──────────────┐      │
         │ Return       │      │
         │ content as   │      │
         │ signal       │      │
         └──────────────┘      │
                               ↓
               ┌─────────────────────────────────────┐
               │ 5. Execute ALL tools (in order)     │
               │    - API requirement: every         │
               │      tool_use needs tool_result     │
               └─────────────────────────────────────┘
                               ↓
               ┌─────────────────────────────────────┐
               │ 6. Add all tool results to context  │
               └─────────────────────────────────────┘
                               ↓
               ┌─────────────────────────────────────┐
               │ 7. Call CheckTerminal callback      │
               │    - Examines tool calls + results  │
               │    - Returns signal if terminal     │
               └─────────────────────────────────────┘
                               ↓
                  ┌─────────────────────┐
                  │ Terminal signal?    │
                  └─────────────────────┘
                       ↓           ↓
                     YES          NO
                       ↓           ↓
            ┌──────────────┐      │
            │ Return       │      │
            │ signal       │      │
            └──────────────┘      │
                                  ↓
                       ┌─────────────────────┐
                       │ Max iterations?     │
                       └─────────────────────┘
                            ↓           ↓
                          YES          NO
                            ↓           ↓
                 ┌──────────────────┐  │
                 │ Call             │  │
                 │ OnIterationLimit │  │
                 │ callback         │  │
                 └──────────────────┘  │
                            ↓           ↓
                            └───────────┘
                                  ↓
                          (loop back to step 2)
```

## Configuration

### Config Structure

```go
type Config struct {
    // Context management (passed in, not owned by ToolLoop)
    ContextManager *contextmgr.ContextManager

    // Tool configuration
    ToolProvider ToolProvider

    // Callbacks
    CheckTerminal    func(calls []agent.ToolCall, results []any) string
    OnIterationLimit func(ctx context.Context) (string, error)

    // Initial prompt (optional)
    InitialPrompt string

    // Limits
    MaxIterations int
    MaxTokens     int

    // Debug
    DebugLogging bool
}
```

### ToolProvider Interface

Agents must implement the `ToolProvider` interface:

```go
type ToolProvider interface {
    Get(name string) (tools.Tool, error)
    List() []tools.ToolMeta
}
```

This interface allows agents to provide their specific tool implementations while toolloop handles the execution logic.

### CheckTerminal Callback

Called after **ALL** tools in a turn have executed. Examines tool calls and results to determine if a state transition should occur.

**Signature:**
```go
func(calls []agent.ToolCall, results []any) string
```

**Returns:**
- Empty string `""` to continue the loop
- Non-empty signal to exit and trigger state transition

**Example:**
```go
CheckTerminal: func(calls []agent.ToolCall, results []any) string {
    for i := range calls {
        if calls[i].Name == "spec_submit" {
            // Terminal tool detected - transition to CONFIRM state
            if resultMap, ok := results[i].(map[string]any); ok {
                if specID, ok := resultMap["spec_id"].(string); ok {
                    return specID  // Return signal for state machine
                }
            }
        }
    }
    return ""  // Continue loop
}
```

### OnIterationLimit Callback

Called when `MaxIterations` is reached without a terminal signal. Typically used to request more budget or escalate.

**Signature:**
```go
func(ctx context.Context) (string, error)
```

**Example:**
```go
OnIterationLimit: func(ctx context.Context) (string, error) {
    return "REQUEST_BUDGET", nil  // Signal state machine to request more iterations
}
```

## Terminal Signals and State Transitions

Toolloop returns a **signal string** that the calling agent uses to determine state transitions:

- **Empty signal (`""`)**: Normal completion without state change
- **Non-empty signal**: State transition requested

### How Terminal Tools Work

1. **LLM calls a terminal tool** (e.g., `spec_submit`, `submit_story`)
2. **Tool executes** and returns structured result:
   ```go
   return map[string]any{
       "success": true,
       "spec_id": "spec-123",
   }, nil
   ```
3. **CheckTerminal examines the result** and extracts the signal:
   ```go
   if calls[i].Name == "spec_submit" {
       return resultMap["spec_id"].(string)  // "spec-123"
   }
   ```
4. **Toolloop returns signal to caller**
5. **Agent's state machine handles the transition**:
   ```go
   signal, err := loop.Run(ctx, &cfg)
   if signal == "spec-123" {
       // Transition to CONFIRM state with spec ID
   }
   ```

### Important: Tools Execute Even If Terminal

Toolloop follows the **batch callback pattern**: ALL tools in a turn execute before checking for terminal conditions. This is an API requirement for Anthropic and OpenAI - every `tool_use` block must have a corresponding `tool_result` block.

## Debug Logging

### Toolloop Debug Logging

Enable detailed message logging in toolloop:

```go
cfg := toolloop.Config{
    DebugLogging: true,  // Logs messages sent to LLM
    // ... other config
}
```

Output:
```
[integration-test] INFO: 📝 DEBUG - Messages sent to LLM:
[integration-test] INFO:   [0] Role: user, Content: "Calculate 5 + 3 using the calculator tool"
[integration-test] INFO:   [1] Role: assistant, Content: "", ToolCalls: 1
[integration-test] INFO:     ToolCall[0] ID=call1 Name=calculator Params=map[a:5 b:3 operation:add]
[integration-test] INFO:   [2] Role: user, Content: "Tool results:", ToolResults: 1
[integration-test] INFO:     ToolResult[0] ID=call1 IsError=false Content="map[result:8 success:true]"
```

### Anthropic API Debug Logging

Enable diagnostic logging for Anthropic message formatting:

```bash
DEBUG_LLM=1 ./bin/maestro
```

Output:
```
[DEBUG ensureAlternation] Merged 3 messages:
  [0] Role=user Content="Calculate 5 + 3" ToolCalls=0 ToolResults=0
  [1] Role=assistant Content="" ToolCalls=1 ToolResults=0
  [2] Role=user Content="Tool results:" ToolCalls=0 ToolResults=1
    ToolResult[0] ID=toolu_01ABC IsError=false

[DEBUG API Request] Sending 3 messages to Anthropic:
  [0] Role=user ContentBlocks=1
    ContentBlock[0] Type=text Text="Calculate 5 + 3"
  [1] Role=assistant ContentBlocks=1
    ContentBlock[0] Type=tool_use ID=toolu_01ABC Name=calculator
  [2] Role=user ContentBlocks=2
    ContentBlock[0] Type=tool_result ToolUseID=toolu_01ABC
    ContentBlock[1] Type=text Text="Tool results:"
```

This is invaluable when debugging message format issues with the Anthropic API.

## Usage Example

### Basic Usage

```go
// Create toolloop instance
loop := toolloop.New(llmClient, logger)

// Configure
cfg := toolloop.Config{
    ContextManager: contextManager,
    InitialPrompt:  "Process this user request",
    ToolProvider:   myToolProvider,
    MaxIterations:  10,
    MaxTokens:      4000,
    CheckTerminal: func(calls []agent.ToolCall, results []any) string {
        // Check for terminal tools
        for i := range calls {
            if calls[i].Name == "complete" {
                return "DONE"
            }
        }
        return ""
    },
}

// Run the loop
signal, err := loop.Run(ctx, &cfg)
if err != nil {
    return err
}

// Handle state transition based on signal
switch signal {
case "DONE":
    d.transitionTo(ctx, StateDone)
case "":
    // No transition, normal completion
default:
    // Handle other signals
}
```

### PM Agent Example

The PM agent maintains context across states and uses terminal tools to signal transitions:

```go
func (d *Driver) executeWorkingLoop(ctx context.Context) (string, error) {
    loop := toolloop.New(d.llmClient, d.logger)

    cfg := toolloop.Config{
        ContextManager: d.contextManager,  // Maintained across calls
        ToolProvider:   d.toolProvider,
        MaxIterations:  20,
        MaxTokens:      4000,
        CheckTerminal: func(calls []agent.ToolCall, results []any) string {
            for i := range calls {
                if calls[i].Name == "spec_submit" {
                    // Extract spec ID from result
                    if resultMap, ok := results[i].(map[string]any); ok {
                        if specID, ok := resultMap["spec_id"].(string); ok {
                            return specID  // Signal: transition to CONFIRM
                        }
                    }
                }
            }
            return ""
        },
        OnIterationLimit: func(ctx context.Context) (string, error) {
            // Request more budget from user
            return "REQUEST_BUDGET", nil
        },
    }

    return loop.Run(ctx, &cfg)
}
```

### Architect Agent Example

The architect creates fresh context for each request (doesn't maintain history):

```go
func (d *Driver) processCoderRequest(ctx context.Context, request *proto.AgentMsg) error {
    // Create fresh context for this request
    cm := contextmgr.NewContextManager()
    cm.AddSystemMessage(architectSystemPrompt)
    cm.AddUserMessage(request.Content)

    loop := toolloop.New(d.llmClient, d.logger)

    cfg := toolloop.Config{
        ContextManager: cm,  // Fresh context per request
        ToolProvider:   d.toolProvider,
        MaxIterations:  5,
        MaxTokens:      2000,
        CheckTerminal: func(calls []agent.ToolCall, results []any) string {
            for i := range calls {
                if calls[i].Name == "submit_reply" {
                    return "REPLIED"
                }
            }
            return ""
        },
    }

    signal, err := loop.Run(ctx, &cfg)
    // Handle signal...
}
```

## Migration Guide

### Before Toolloop (Manual Loop)

```go
for iteration := 0; iteration < maxIterations; iteration++ {
    // Flush user buffer
    if err := d.contextManager.FlushUserBuffer(ctx); err != nil {
        return err
    }

    // Build messages
    messages := buildMessages(d.contextManager)

    // Call LLM
    req := agent.CompletionRequest{
        Messages:  messages,
        MaxTokens: 4000,
        Tools:     toolDefs,
    }
    resp, err := d.llmClient.Complete(ctx, req)

    // Add assistant response
    if len(resp.ToolCalls) > 0 {
        toolCalls := make([]contextmgr.ToolCall, len(resp.ToolCalls))
        for i := range resp.ToolCalls {
            toolCalls[i] = contextmgr.ToolCall{
                ID: resp.ToolCalls[i].ID,
                Name: resp.ToolCalls[i].Name,
                Parameters: resp.ToolCalls[i].Parameters,
            }
        }
        d.contextManager.AddAssistantMessageWithTools(resp.Content, toolCalls)
    } else {
        d.contextManager.AddAssistantMessage(resp.Content)
        return nil
    }

    // Execute tools
    for _, toolCall := range resp.ToolCalls {
        tool, err := d.toolProvider.Get(toolCall.Name)
        result, err := tool.Exec(ctx, toolCall.Parameters)
        resultStr := formatToolResult(result, err)
        d.contextManager.AddToolResult(toolCall.ID, resultStr, err != nil)

        // Check if terminal
        if toolCall.Name == "spec_submit" {
            return result["spec_id"].(string), nil
        }
    }
}
```

### After Toolloop

```go
loop := toolloop.New(d.llmClient, d.logger)

cfg := toolloop.Config{
    ContextManager: d.contextManager,
    ToolProvider:   d.toolProvider,
    MaxIterations:  20,
    MaxTokens:      4000,
    CheckTerminal: func(calls []agent.ToolCall, results []any) string {
        for i := range calls {
            if calls[i].Name == "spec_submit" {
                if resultMap, ok := results[i].(map[string]any); ok {
                    if specID, ok := resultMap["spec_id"].(string); ok {
                        return specID
                    }
                }
            }
        }
        return ""
    },
}

return loop.Run(ctx, &cfg)
```

### Key Migration Steps

1. **Identify tool loop code** - Look for iteration loops with LLM calls and tool execution
2. **Extract tool provider** - Ensure your agent implements `ToolProvider` interface
3. **Identify terminal conditions** - Look for places where tools trigger state transitions
4. **Implement CheckTerminal** - Convert terminal logic to callback
5. **Replace loop with toolloop.Run()** - Remove manual loop code
6. **Test thoroughly** - Verify state transitions and terminal signals work correctly

## API Requirements and Best Practices

### ALL Tools Must Execute

Both Anthropic and OpenAI require that every `tool_use` block has a corresponding `tool_result` block. Toolloop enforces this by executing all requested tools before checking for terminal conditions.

**Don't:**
```go
// BAD: Early exit on terminal tool
for _, call := range toolCalls {
    result := executeTool(call)
    if call.Name == "terminal_tool" {
        return result  // WRONG: Other tools not executed
    }
}
```

**Do:**
```go
// GOOD: Execute all tools, then check terminal
results := make([]any, len(toolCalls))
for i := range toolCalls {
    results[i] = executeTool(toolCalls[i])
}

// Now check for terminal
for i := range toolCalls {
    if toolCalls[i].Name == "terminal_tool" {
        return results[i]
    }
}
```

Toolloop handles this automatically.

### Context Management Patterns

**Pattern 1: Maintained Context (PM Agent)**
- Context persists across multiple toolloop calls
- Used for conversational agents with memory
- Pass same ContextManager to multiple `Run()` calls

**Pattern 2: Fresh Context (Architect Agent)**
- Create new ContextManager for each request
- Used for stateless request/response agents
- Pass new ContextManager to each `Run()` call

Toolloop supports both patterns - the caller maintains ownership of the ContextManager.

### Error Handling

Toolloop returns errors for:
- LLM API failures
- Missing required configuration
- Tool execution failures (logged but not fatal)

Individual tool errors are logged and added to context as error results, allowing the LLM to handle them gracefully.

## Testing

See `pkg/agent/toolloop/toolloop_test.go` for unit tests with mocks.

See `pkg/agent/toolloop/toolloop_integration_test.go` for integration tests with real LLMs (gpt-4o-mini, claude-sonnet-4-5).

Run integration tests:
```bash
go test -tags=integration ./pkg/agent/toolloop -v
```

Enable debug logging during tests:
```bash
DEBUG_LLM=1 go test -tags=integration ./pkg/agent/toolloop -v
```
