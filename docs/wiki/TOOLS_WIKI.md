# Maestro MCP Tools System

## Overview

Maestro uses a sophisticated **Model Context Protocol (MCP)** tools system that allows AI agents to interact with their execution environment, perform file operations, manage containers, communicate with other agents, and control their workflow state. This document describes the complete lifecycle of MCP tools from creation through runtime execution.

The tools system is built on three core principles:
1. **Type Safety**: Tools are strongly typed with JSON schema validation
2. **Context Awareness**: Tools adapt based on agent state and story type
3. **Execution Flexibility**: Tools can execute in containers or on the host as needed

**Living Documentation Notice**: The Maestro tools system is actively evolving. New tools are regularly added to support emerging workflows and capabilities. While this document covers the core patterns and current tool set, the definitive source of truth for available tools is always the codebase itself:
- `pkg/tools/constants.go` - Complete list of tool constants and state-specific groupings
- `pkg/tools/registry.go` - Tool registration and initialization
- `pkg/tools/*.go` - Individual tool implementations

Tool examples in this document are illustrative and may not represent the complete current tool catalog.

## What are MCP Tools?

MCP (Model Context Protocol) tools are structured function calls that LLM agents can make to interact with their environment. When an LLM needs to perform an action (read a file, run a command, ask a question), it generates a **tool_use** request that Maestro executes and returns results for.

**Example Tool Call Flow**:
```
1. LLM generates: { "tool": "shell", "args": { "cmd": "ls -la" } }
2. Maestro executes: runs 'ls -la' in agent's container
3. LLM receives: { "stdout": "...", "exit_code": 0 }
4. LLM continues: uses results to inform next action
```

## Tool Lifecycle Overview

The complete tool lifecycle consists of five phases:

```
┌─────────────────────────────────────────────────────────────────┐
│                      TOOL LIFECYCLE                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. CREATION & REGISTRATION (init time)                        │
│     • Tool implementations define schemas                       │
│     • Global registry populated via init()                      │
│     • Registry sealed on first provider creation               │
│                                                                 │
│  2. PROVIDER CREATION (agent startup)                          │
│     • Agent creates ToolProvider with context                  │
│     • Provider filters tools by allowed list                   │
│     • Tools instantiated lazily on first use                   │
│                                                                 │
│  3. SCHEMA CONVERSION (before LLM call)                        │
│     • Provider.List() returns ToolMeta for allowed tools       │
│     • Converted to LLM-specific format                         │
│     • Included in LLM request as available tools               │
│                                                                 │
│  4. PROMPT INCLUSION (template rendering)                      │
│     • Provider.GenerateToolDocumentation()                     │
│     • Markdown documentation added to prompt                   │
│     • Guides LLM on when/how to use tools                      │
│                                                                 │
│  5. RUNTIME EXECUTION (during conversation)                    │
│     • LLM returns tool_use in response                         │
│     • Agent calls Provider.Get() to retrieve tool              │
│     • Tool.Exec() executes with parameters                     │
│     • Results added to context for next LLM call               │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Phase 1: Tool Creation & Registration

Tools are created and registered during package initialization using Go's `init()` function pattern.

### Tool Implementation Structure

Every tool implements the `Tool` interface (`pkg/tools/mcp.go:62-71`):

```go
type Tool interface {
    Definition() ToolDefinition       // Returns schema for LLM
    Name() string                     // Tool identifier
    Exec(ctx context.Context, args map[string]any) (any, error)
    PromptDocumentation() string      // Human-readable usage guide
}
```

**Example Tool Implementation** (`pkg/tools/planning_tools.go`):

```go
// SubmitPlanTool signals plan completion
type SubmitPlanTool struct{}

func NewSubmitPlanTool() *SubmitPlanTool {
    return &SubmitPlanTool{}
}

func (t *SubmitPlanTool) Name() string {
    return "submit_plan"
}

func (t *SubmitPlanTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name:        "submit_plan",
        Description: "Submit your final implementation plan",
        InputSchema: InputSchema{
            Type: "object",
            Properties: map[string]Property{
                "plan": {
                    Type:        "string",
                    Description: "Detailed implementation plan",
                },
            },
            Required: []string{"plan"},
        },
    }
}

func (t *SubmitPlanTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    plan, ok := args["plan"].(string)
    if !ok || plan == "" {
        return nil, fmt.Errorf("plan is required")
    }

    // Signal state transition via special return value
    return map[string]any{
        "next_state": "PLAN_REVIEW",
        "plan":       plan,
    }, nil
}

func (t *SubmitPlanTool) PromptDocumentation() string {
    return `- **submit_plan** - Submit completed implementation plan
  - Required: plan (detailed implementation approach)
  - Transitions to PLAN_REVIEW state`
}
```

**Example Tool with Nested Arrays** (`pkg/tools/submit_stories.go`):

For tools that accept complex nested data structures, use pointer types for the `Items` and `Properties` fields:

```go
func (t *SubmitStoriesTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name:        ToolSubmitStories,
        Description: "Submit analyzed requirements as structured stories",
        InputSchema: InputSchema{
            Type: "object",
            Properties: map[string]Property{
                "requirements": {
                    Type:        "array",
                    Description: "Array of requirement objects",
                    Items: &Property{  // Use pointer for nested schemas
                        Type: "object",
                        Properties: map[string]*Property{  // Pointers in map
                            "title": {
                                Type:        "string",
                                Description: "Requirement title",
                            },
                            "acceptance_criteria": {
                                Type:        "array",
                                Description: "Array of testable criteria",
                                Items: &Property{  // Nested array
                                    Type: "string",
                                },
                            },
                            "dependencies": {
                                Type:        "array",
                                Description: "Array of requirement titles",
                                Items: &Property{
                                    Type: "string",
                                },
                            },
                        },
                    },
                },
            },
            Required: []string{"requirements"},
        },
    }
}
```

This pattern ensures proper recursive schema conversion for LLM APIs that require explicit `items` and `properties` fields.

### Global Registry Pattern

All tools register themselves in a global registry during initialization (`pkg/tools/registry.go:60-81`):

```go
// Global registry - thread-safe, immutable after sealing
var globalRegistry = &immutableRegistry{
    tools: make(map[string]toolDescriptor),
}

// Register adds a tool factory to the registry
func Register(name string, factory ToolFactory, meta *ToolMeta) {
    globalRegistry.mu.Lock()
    defer globalRegistry.mu.Unlock()

    if globalRegistry.sealed {
        panic("tool registry sealed - cannot register")
    }

    globalRegistry.tools[name] = toolDescriptor{
        meta:    *meta,
        factory: factory,
    }
}
```

### Tool Factory Pattern

Tools use factories for lazy instantiation with context (`pkg/tools/registry.go:34`):

```go
type ToolFactory func(ctx AgentContext) (Tool, error)

type AgentContext struct {
    Executor        execpkg.Executor   // For executing commands
    ChatService     *chat.Service      // For chat tools
    ReadOnly        bool               // Filesystem restrictions
    NetworkDisabled bool               // Network access control
    WorkDir         string             // Agent workspace path
    Agent           Agent              // For state-aware tools
}
```

**Factory Example** (`pkg/tools/registry.go:214-226`):

```go
func createShellTool(ctx AgentContext) (Tool, error) {
    if ctx.Executor == nil {
        return nil, fmt.Errorf("shell tool requires an executor")
    }

    return NewShellToolWithConfig(
        ctx.Executor,
        ctx.ReadOnly,        // Planning: true, Coding: false
        ctx.NetworkDisabled, // Planning: true, Coding: false
        nil,                 // No resource limits
    ), nil
}
```

### Registration in init()

Tools register themselves when the package loads (`pkg/tools/registry.go:507-660`):

```go
func init() {
    // Planning tools
    Register(ToolSubmitPlan, createSubmitPlanTool, &ToolMeta{
        Name:        ToolSubmitPlan,
        Description: "Submit your final implementation plan",
        InputSchema: getSubmitPlanSchema(),
    })

    // Development tools
    Register(ToolShell, createShellTool, &ToolMeta{
        Name:        ToolShell,
        Description: "Execute shell commands and return output",
        InputSchema: getShellSchema(),
    })

    // Container tools
    Register(ToolContainerBuild, createContainerBuildTool, &ToolMeta{
        Name:        ToolContainerBuild,
        Description: "Build Docker container from Dockerfile",
        InputSchema: getContainerBuildSchema(),
    })

    // Chat tools
    Register(ToolChatPost, createChatPostTool, &ToolMeta{
        Name:        ToolChatPost,
        Description: "Post message to agent chat channel",
        InputSchema: getChatPostSchema(),
    })

    // ... and many more
}
```

### Registry Sealing

The registry becomes immutable on first use to prevent runtime modification (`pkg/tools/registry.go:85-89`):

```go
func Seal() {
    globalRegistry.mu.Lock()
    defer globalRegistry.mu.Unlock()
    globalRegistry.sealed = true
}

// Called automatically in NewProvider
func NewProvider(ctx AgentContext, allowedTools []string) *ToolProvider {
    Seal() // Ensure registry is immutable
    // ...
}
```

## Phase 2: Provider Creation

Each agent creates a `ToolProvider` configured for its current state and story type.

### State-Specific Tool Sets

Tools are organized by workflow state and story type (`pkg/tools/constants.go:43-122`):

```go
// Planning tools - exploration and plan submission
var AppPlanningTools = []string{
    ToolShell,              // Read-only exploration
    ToolSubmitPlan,         // Advance to next state
    ToolAskQuestion,        // Query architect
    ToolMarkStoryComplete,  // Skip if already done
    ToolChatPost,           // Collaborate
    ToolChatRead,           // Read collaboration
}

// DevOps planning includes container verification
var DevOpsPlanningTools = []string{
    ToolShell,
    ToolSubmitPlan,
    ToolAskQuestion,
    ToolMarkStoryComplete,
    ToolContainerTest,      // Verify infrastructure
    ToolContainerList,      // Check available containers
    ToolChatPost,
    ToolChatRead,
}

// Coding tools - full development environment
var AppCodingTools = []string{
    ToolShell,              // Read-write access
    ToolBuild,              // Compile code
    ToolTest,               // Run tests
    ToolLint,               // Check code quality
    ToolAskQuestion,        // Get guidance
    ToolDone,               // Signal completion
    ToolChatPost,
    ToolChatRead,
    ToolTodosAdd,           // Task management
    ToolTodoComplete,
    ToolTodoUpdate,
}

// DevOps coding includes container management
var DevOpsCodingTools = []string{
    ToolShell,
    ToolAskQuestion,
    ToolDone,
    ToolContainerBuild,     // Build images
    ToolContainerUpdate,    // Update configuration
    ToolContainerTest,      // Test containers
    ToolContainerList,      // List containers
    ToolContainerSwitch,    // Change environment
    ToolChatPost,
    ToolChatRead,
    ToolTodosAdd,
    ToolTodoComplete,
    ToolTodoUpdate,
}

// Architect read tools - code inspection and spec analysis
var ArchitectReadTools = []string{
    ToolReadFile,           // Read coder files
    ToolListFiles,          // List workspace files
    ToolGetDiff,            // View git changes
    ToolSubmitReply,        // Exit iteration loop (REQUEST/ANSWERING states)
    ToolSubmitStories,      // Submit spec analysis (SCOPING state)
}
```

**Note**: Tool lists evolve as new capabilities are added. Check `pkg/tools/constants.go` for the current complete list of available tools and their state-specific groupings.

### Provider Instantiation

Agents create providers with appropriate context (`pkg/coder/planning.go:86-89`):

```go
// In coder agent during PLANNING state
if c.planningToolProvider == nil {
    c.planningToolProvider = c.createPlanningToolProvider(storyType)
    c.logger.Debug("Created planning ToolProvider for story type: %s", storyType)
}
```

**Provider Factory Method** (`pkg/coder/driver.go`):

```go
func (c *Coder) createPlanningToolProvider(storyType string) *tools.ToolProvider {
    // Determine tool set based on story type
    var allowedTools []string
    if storyType == string(proto.StoryTypeDevOps) {
        allowedTools = tools.DevOpsPlanningTools
    } else {
        allowedTools = tools.AppPlanningTools
    }

    // Create context with security restrictions for planning
    ctx := tools.AgentContext{
        Executor:        c.longRunningExecutor,
        ChatService:     c.chatService,
        ReadOnly:        true,  // No file modifications
        NetworkDisabled: true,  // No network access
        WorkDir:         "/workspace",
        Agent:           c,
    }

    return tools.NewProvider(ctx, allowedTools)
}

func (c *Coder) createCodingToolProvider(storyType string) *tools.ToolProvider {
    var allowedTools []string
    if storyType == string(proto.StoryTypeDevOps) {
        allowedTools = tools.DevOpsCodingTools
    } else {
        allowedTools = tools.AppCodingTools
    }

    // Coding context has fewer restrictions
    ctx := tools.AgentContext{
        Executor:        c.longRunningExecutor,
        ChatService:     c.chatService,
        ReadOnly:        false, // Allow file modifications
        NetworkDisabled: false, // Allow network access
        WorkDir:         "/workspace",
        Agent:           c,
    }

    return tools.NewProvider(ctx, allowedTools)
}
```

### Lazy Tool Instantiation

Tools are created on-demand when first requested (`pkg/tools/registry.go:131-163`):

```go
func (p *ToolProvider) Get(name string) (Tool, error) {
    p.mu.Lock()
    defer p.mu.Unlock()

    // Check if tool is allowed
    if _, ok := p.allowSet[name]; !ok {
        return nil, fmt.Errorf("tool '%s' not allowed in this context", name)
    }

    // Return cached instance if available
    if tool, ok := p.tools[name]; ok {
        return tool, nil
    }

    // Create new instance using factory
    globalRegistry.mu.RLock()
    desc, exists := globalRegistry.tools[name]
    globalRegistry.mu.RUnlock()

    if !exists {
        return nil, fmt.Errorf("tool '%s' not registered", name)
    }

    tool, err := desc.factory(p.ctx) // Factory creates with context
    if err != nil {
        return nil, fmt.Errorf("failed to create tool '%s': %w", name, err)
    }

    // Cache for reuse
    p.tools[name] = tool
    return tool, nil
}
```

## Phase 3: Schema Conversion for LLM

Before each LLM call, tool schemas are converted to the LLM provider's format.

### Retrieving Tool Definitions

Agents retrieve tool definitions from their provider (`pkg/coder/driver.go:123-159`):

```go
// Get tools for planning state
func (c *Coder) getPlanningToolsForLLM() []tools.ToolDefinition {
    if c.planningToolProvider == nil {
        return nil
    }

    // Get metadata for all allowed tools
    toolMetas := c.planningToolProvider.List()
    definitions := make([]tools.ToolDefinition, 0, len(toolMetas))

    for _, meta := range toolMetas {
        definitions = append(definitions, tools.ToolDefinition(meta))
    }

    c.logger.Debug("Retrieved %d planning tools for LLM", len(definitions))
    return definitions
}
```

### Tool Definition Structure

Each tool provides a complete JSON schema (`pkg/tools/mcp.go:13-40`):

```go
type ToolDefinition struct {
    Name        string      `json:"name"`
    Description string      `json:"description"`
    InputSchema InputSchema `json:"input_schema"`
}

type InputSchema struct {
    Type       string              `json:"type"`
    Properties map[string]Property `json:"properties"`
    Required   []string            `json:"required,omitempty"`
}

type Property struct {
    Type        string               `json:"type"`
    Description string               `json:"description,omitempty"`
    Enum        []string             `json:"enum,omitempty"`
    Items       *Property            `json:"items,omitempty"`
    Properties  map[string]*Property `json:"properties,omitempty"`
    Required    []string             `json:"required,omitempty"`
    MinItems    *int                 `json:"minItems,omitempty"`
    MaxItems    *int                 `json:"maxItems,omitempty"`
}
```

**Example Definition** (shell tool):

```json
{
  "name": "shell",
  "description": "Execute a shell command and return the output",
  "input_schema": {
    "type": "object",
    "properties": {
      "cmd": {
        "type": "string",
        "description": "The shell command to execute"
      },
      "cwd": {
        "type": "string",
        "description": "Optional working directory for the command"
      }
    },
    "required": ["cmd"]
  }
}
```

### Inclusion in LLM Request

Tools are passed to the LLM client in each request (`pkg/coder/planning.go:147-152`):

```go
req := agent.CompletionRequest{
    Messages:  messages,
    MaxTokens: 8192,
    Tools:     c.getPlanningToolsForLLM(), // Tool definitions
    // Temperature defaults to 0.3 for planning
}

resp, llmErr := c.llmClient.Complete(ctx, req)
```

The LLM client converts these to provider-specific formats (Anthropic Messages API, OpenAI Responses API, etc.).

### Recursive Schema Conversion for Complex Tools

Tools with nested structures (arrays of objects, nested properties) require recursive schema conversion for some LLM APIs, particularly OpenAI's Responses API.

**Challenge**: The OpenAI Responses API requires explicit `items` fields for array properties and nested `properties` for object types. Simply copying top-level attributes isn't sufficient for complex schemas.

**Example - submit_stories tool schema** (`pkg/tools/submit_stories.go`):

```go
"requirements": {
    Type:        "array",
    Description: "Array of requirement objects",
    Items: &Property{  // This must be recursively converted
        Type: "object",
        Properties: map[string]*Property{
            "title": {
                Type:        "string",
                Description: "Requirement title",
            },
            "acceptance_criteria": {
                Type:        "array",
                Description: "Array of testable criteria",
                Items: &Property{  // Nested array - requires recursion
                    Type: "string",
                },
            },
            // ... more nested properties
        },
    },
},
```

**Solution - Recursive Conversion** (`pkg/agent/internal/llmimpl/openaiofficial/client.go:39-68`):

```go
// convertPropertyToSchema recursively converts a Property to OpenAI schema format
func convertPropertyToSchema(prop *tools.Property) map[string]interface{} {
    schema := map[string]interface{}{
        "type":        prop.Type,
        "description": prop.Description,
    }

    // Add enum if present
    if len(prop.Enum) > 0 {
        schema["enum"] = prop.Enum
    }

    // Handle array items recursively
    if prop.Type == "array" && prop.Items != nil {
        schema["items"] = convertPropertyToSchema(prop.Items)  // Recursive call
    }

    // Handle object properties recursively
    if prop.Type == "object" && prop.Properties != nil {
        properties := make(map[string]interface{})
        for name, childProp := range prop.Properties {
            if childProp != nil {
                properties[name] = convertPropertyToSchema(childProp)  // Recursive call
            }
        }
        schema["properties"] = properties
    }

    return schema
}
```

This recursive approach ensures that all nested structures are properly converted, including:
- Arrays with complex item schemas
- Objects with nested properties
- Multiple levels of nesting (arrays of objects with array properties, etc.)

Without this recursion, the OpenAI Responses API will reject the schema with errors like:
```
Invalid schema for function 'submit_stories': In context=('properties', 'requirements'),
array schema missing items.
```

## Phase 4: Prompt Documentation

Tools provide human-readable documentation that's included in agent prompts.

### Documentation Generation

Providers generate markdown documentation for all allowed tools (`pkg/tools/registry.go:189-210`):

```go
func (p *ToolProvider) GenerateToolDocumentation() string {
    return GenerateToolDocumentationForTools(p.List())
}

func GenerateToolDocumentationForTools(tools []ToolMeta) string {
    if len(tools) == 0 {
        return "No tools available"
    }

    var doc strings.Builder
    doc.WriteString("## Available Tools\n\n")

    for _, meta := range tools {
        doc.WriteString(fmt.Sprintf("- **%s** - %s\n",
            meta.Name, meta.Description))
    }

    return doc.String()
}
```

### Template Integration

Tool documentation is embedded in state-specific templates (`pkg/coder/planning.go:111-120`):

```go
templateData := &templates.TemplateData{
    TaskContent:       taskContent,
    TreeOutput:        treeOutput,
    ToolDocumentation: c.planningToolProvider.GenerateToolDocumentation(),
    ContainerName:     containerName,
    Extra: map[string]any{
        "story_type": storyType,
    },
}

prompt, err := c.renderer.RenderWithUserInstructions(
    planningTemplate, templateData, c.workDir, "CODER")
```

**Example Template** (`pkg/templates/coder_planning.go`):

```
You are implementing this story:
{{ .TaskContent }}

Current workspace structure:
{{ .TreeOutput }}

## Available Tools

{{ .ToolDocumentation }}

Use the shell tool to explore the codebase and understand the requirements.
When ready, use submit_plan with your implementation approach.
```

### Rich Tool Documentation

Individual tools can provide detailed usage guidance (`pkg/tools/mcp.go:131-137`):

```go
func (s *ShellTool) PromptDocumentation() string {
    return `- **shell** - Execute shell commands for exploration and file operations
  - Parameters: cmd (required), cwd (optional working directory)
  - Read-only filesystem with network disabled for security
  - Returns: stdout, stderr, exit_code, duration, and command details
  - Use for: find, grep, cat, ls, tree, exploration commands`
}
```

## Phase 5: Runtime Execution

When the LLM generates tool calls, Maestro executes them and returns results.

### Tool Call Structure

LLM responses contain tool use requests (`pkg/agent/middleware/resilience/retry/middleware.go`):

```go
type ToolCall struct {
    ID         string         // Unique identifier for correlation
    Name       string         // Tool to execute
    Parameters map[string]any // JSON arguments
}
```

**Example LLM Response**:

```json
{
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "Let me check the current directory structure."
    },
    {
      "type": "tool_use",
      "id": "toolu_123",
      "name": "shell",
      "input": {
        "cmd": "ls -la"
      }
    }
  ]
}
```

### Tool Execution Loop

Agents process tool calls and add results to context (`pkg/coder/planning.go:183-283`):

```go
func (c *Coder) processPlanningToolCalls(ctx context.Context, sm *agent.BaseStateMachine,
                                         toolCalls []agent.ToolCall) (proto.State, bool, error) {
    c.logger.Info("Processing %d planning tool calls", len(toolCalls))

    for i := range toolCalls {
        toolCall := &toolCalls[i]
        c.logger.Info("Executing planning tool: %s", toolCall.Name)

        // Get tool from provider (lazy instantiation)
        tool, err := c.planningToolProvider.Get(toolCall.Name)
        if err != nil {
            c.logger.Error("Tool not found: %s", toolCall.Name)
            continue
        }

        // Add agent context for tools that need it
        toolCtx := context.WithValue(ctx, tools.AgentIDContextKey, c.agentID)

        // Execute tool with timing and logging
        startTime := time.Now()
        result, err := tool.Exec(toolCtx, toolCall.Parameters)
        duration := time.Since(startTime)

        // Log execution to database (fire-and-forget)
        c.logToolExecution(toolCall, result, err, duration)

        if err != nil {
            c.logger.Info("Tool execution failed for %s: %v", toolCall.Name, err)
            continue
        }

        // Check for state transitions
        if resultMap, ok := result.(map[string]any); ok {
            if nextState, hasNextState := resultMap["next_state"]; hasNextState {
                if nextStateStr, ok := nextState.(string); ok {
                    return c.handleToolStateTransition(ctx, sm,
                        toolCall.Name, nextStateStr, resultMap)
                }
            }
        }

        // Add result to context for next LLM call
        c.addToolResultToContext(*toolCall, result)
        c.logger.Info("Planning tool %s executed successfully", toolCall.Name)
    }

    // Continue in current state after processing all tools
    return StatePlanning, false, nil
}
```

### Tool Result Format

Tools return structured data that's serialized for the LLM (`pkg/tools/mcp.go:72-81`):

```go
// Shell tool execution
func (s *ShellTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    cmd := args["cmd"].(string)
    cwd := args["cwd"].(string)

    result, err := s.executor.Run(ctx, []string{"sh", "-c", cmd}, &opts)
    if err != nil {
        return nil, fmt.Errorf("shell command failed: %s", cmd)
    }

    return map[string]any{
        "stdout":    result.Stdout,
        "stderr":    result.Stderr,
        "exit_code": result.ExitCode,
        "cwd":       cwd,
        "command":   cmd,
        "duration":  result.Duration.String(),
    }, nil
}
```

### Context Manager Integration

Tool results are added to the conversation context (`pkg/coder/tool_helpers.go`):

```go
func (c *Coder) addToolResultToContext(toolCall agent.ToolCall, result any) {
    // Serialize result to JSON
    resultJSON, err := json.Marshal(result)
    if err != nil {
        c.logger.Error("Failed to marshal tool result: %v", err)
        return
    }

    // Add to context with proper role and provenance
    c.contextManager.AddToolResult(toolCall.ID, string(resultJSON))
}
```

**Context Manager Method** (`pkg/contextmgr/manager.go`):

```go
func (m *Manager) AddToolResult(toolUseID, content string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.messages = append(m.messages, Message{
        Role:       "tool_result",
        Content:    content,
        ToolUseID:  toolUseID,
        Provenance: "tool-result", // Not cached (dynamic content)
    })
}
```

## Tool Execution Strategies

Different tools execute in different environments based on their requirements.

### Container Execution (Default)

Most tools execute inside the agent's container (`pkg/tools/mcp.go:168-204`):

```go
func (s *ShellTool) executeShellCommand(ctx context.Context, cmdStr, cwd string) (any, error) {
    opts := exec.Opts{
        WorkDir:         cwd,
        Timeout:         30 * time.Second,
        ReadOnly:        s.readOnly,        // State-dependent
        NetworkDisabled: s.networkDisabled, // State-dependent
        ResourceLimits:  s.resourceLimits,
    }

    // Execute via container executor
    result, err := s.executor.Run(ctx, []string{"sh", "-c", cmdStr}, &opts)

    return map[string]any{
        "stdout":    result.Stdout,
        "stderr":    result.Stderr,
        "exit_code": result.ExitCode,
        "command":   cmdStr,
    }, nil
}
```

### Host Execution (Privileged Operations)

Container management tools execute on the host with access to Docker daemon (`pkg/tools/execution.go:43-94`):

```go
func (r *HostRunner) RunContainerTest(ctx context.Context, args map[string]any) (any, error) {
    r.logger.Info("Executing container_test on host (Strategy A)")

    containerName := args["container_name"].(string)
    command := args["command"].(string)
    hostWorkspace := args["host_workspace_path"].(string)
    mountPermissions := args["mount_permissions"].(string)

    // Build docker command to run on host
    dockerArgs := []string{
        "docker", "run", "--rm",
        "-v", fmt.Sprintf("%s:/workspace:%s", hostWorkspace, mountPermissions),
        "-w", "/workspace",
        "--tmpfs", "/tmp:rw,noexec,nosuid,size=2g",
        containerName,
        "sh", "-c", command,
    }

    // Execute directly on host (not in container)
    cmd := exec.CommandContext(ctx, dockerArgs[0], dockerArgs[1:]...)
    // ...
}
```

**Why Host Execution?**
- Full access to Docker daemon (no docker-in-docker)
- Can inspect and manage containers
- Can mount host directories into test containers
- Required for `container_build`, `container_test`, `container_switch`

### Read-Only vs Read-Write

The same tool behaves differently based on agent state:

**Planning Phase** (read-only):
```go
ctx := tools.AgentContext{
    Executor:        executor,
    ReadOnly:        true,  // Filesystem mounted read-only
    NetworkDisabled: true,  // No network access
    // ...
}
```

**Coding Phase** (read-write):
```go
ctx := tools.AgentContext{
    Executor:        executor,
    ReadOnly:        false, // Can modify files
    NetworkDisabled: false, // Can access network
    // ...
}
```

Container mount flags enforce these restrictions at the OS level.

## Special Tool Categories

### Completion Tools and Iteration Loops

Some tools serve as "completion signals" that terminate multi-turn LLM iteration loops. These tools are essential for workflows where the LLM may need to call multiple exploratory tools before submitting final results.

**Pattern**: Agents call LLM repeatedly in a loop, allowing tool calls to be executed and results added to context. The loop continues until a completion tool is called.

**Example - Architect SCOPING State** (`pkg/architect/driver.go:595-647`):

```go
// Tool call iteration loop
maxIterations := 10
for iteration := 0; iteration < maxIterations; iteration++ {
    req := agent.CompletionRequest{
        Messages:  messages,
        MaxTokens: agent.ArchitectMaxTokens,
        Tools:     toolDefs,  // Includes read tools + submit_stories
    }

    resp, err := d.llmClient.Complete(ctx, req)
    if err != nil {
        return "", fmt.Errorf("LLM completion failed: %w", err)
    }

    // If no tool calls, return text content
    if len(resp.ToolCalls) == 0 {
        return resp.Content, nil
    }

    // Process tool calls
    submitResponse, err := d.processArchitectToolCalls(ctx, resp.ToolCalls, toolProvider)
    if err != nil {
        return "", fmt.Errorf("tool processing failed: %w", err)
    }

    // If submit tool was called, return its response and exit loop
    if submitResponse != "" {
        return submitResponse, nil
    }

    // Rebuild messages for next iteration with tool results
    messages = d.buildMessagesWithContext("")
}

return "", fmt.Errorf("maximum tool iterations (%d) exceeded", maxIterations)
```

**Completion Tools**:
- `submit_plan` - Signals planning phase complete
- `submit_stories` - Signals spec analysis complete
- `submit_reply` - Signals architect response complete
- `done` - Signals coding task complete
- `story_complete` - Signals story already implemented

These tools enable flexible workflows where the LLM can explore as needed before signaling completion.

### State Transition Tools

Some tools trigger state machine transitions by returning special values (`pkg/tools/planning_tools.go`):

```go
func (t *SubmitPlanTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    plan := args["plan"].(string)

    // Special return format signals state transition
    return map[string]any{
        "next_state": "PLAN_REVIEW",
        "plan":       plan,
        "message":    "Plan submitted successfully",
    }, nil
}
```

**State Transition Handler** (`pkg/coder/planning.go:297-350`):

```go
func (c *Coder) handleToolStateTransition(ctx context.Context, sm *agent.BaseStateMachine,
                                          toolName, nextState string,
                                          resultMap map[string]any) (proto.State, bool, error) {
    switch toolName {
    case tools.ToolSubmitPlan:
        if plan, ok := resultMap["plan"].(string); ok {
            sm.SetStateData(KeyPlan, plan)
            c.logger.Info("Plan submitted, transitioning to PLAN_REVIEW")
            return StatePlanReview, false, nil
        }

    case tools.ToolMarkStoryComplete:
        reason := resultMap["reason"].(string)
        c.logger.Info("Story marked complete: %s", reason)
        return StateDone, false, nil

    case tools.ToolDone:
        c.logger.Info("Coder signaled completion")
        return StatePrepareMerge, false, nil
    }

    return proto.StateError, false, fmt.Errorf("unknown tool transition")
}
```

### Effect-Based Tools

Some tools create effects that are executed by the dispatcher (`pkg/coder/planning.go:191-236`):

```go
// ask_question tool creates QuestionEffect
if toolCall.Name == tools.ToolAskQuestion {
    question := args["question"].(string)
    context := args["context"].(string)
    urgency := args["urgency"].(string)

    // Store planning context before blocking
    c.storePlanningContext(sm)

    // Create and execute effect (blocks until answer received)
    eff := effect.NewQuestionEffect(question, context, urgency, "PLANNING")
    eff.StoryID = storyID

    result, err := c.ExecuteEffect(ctx, eff)
    if err != nil {
        c.logger.Error("Failed to get answer: %v", err)
        continue
    }

    // Process answer and add to context
    if questionResult, ok := result.(*effect.QuestionResult); ok {
        qaContent := fmt.Sprintf("Question: %s\nAnswer: %s",
            question, questionResult.Answer)
        c.contextManager.AddMessage("architect-answer", qaContent)
    }

    continue // Don't add to normal tool results
}
```

### Architect Read Tools

Architect agents have special read-only tools for inspecting coder workspaces (`pkg/tools/read_file.go`, `pkg/tools/list_files.go`, `pkg/tools/get_diff.go`):

```go
// read_file tool
func (t *ReadFileTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    coderID := args["coder_id"].(string)
    path := args["path"].(string)

    // Validate coder_id to prevent path traversal
    if !isValidCoderID(coderID) {
        return nil, fmt.Errorf("invalid coder_id")
    }

    // Build path: /mnt/coders/<coder_id>/<path>
    fullPath := filepath.Join(t.workspaceRoot, coderID, path)

    // Read file via container executor (read-only mount)
    cmd := []string{"cat", fullPath}
    result, err := t.executor.Run(ctx, cmd, &exec.Opts{
        ReadOnly: true,
        Timeout:  30 * time.Second,
    })

    return map[string]any{
        "coder_id": coderID,
        "path":     path,
        "content":  result.Stdout,
        "size":     len(result.Stdout),
    }, nil
}
```

These tools allow architects to review code without modifying it, supporting the iterative review process.

### Chat Tools

Chat tools enable agent-to-agent communication (`pkg/tools/chat_tools.go`):

```go
func (t *ChatPostTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    message := args["message"].(string)
    agentID := ctx.Value(tools.AgentIDContextKey).(string)

    // Post to shared chat channel
    msg := &chat.Message{
        AgentID:   agentID,
        Message:   message,
        PostType:  chat.PostTypeNormal,
        Timestamp: time.Now(),
    }

    err := t.chatService.PostMessage(msg)
    return map[string]any{
        "success": err == nil,
        "message": message,
    }, err
}
```

Chat messages are automatically injected into LLM contexts, enabling real-time collaboration.

## Tool Documentation Best Practices

### Schema Design

**Good Schema** (clear, typed, with examples):
```go
InputSchema: InputSchema{
    Type: "object",
    Properties: map[string]Property{
        "container_name": {
            Type:        "string",
            Description: "Docker image name or tag (e.g., 'maestro-app', 'ubuntu:22.04')",
        },
        "command": {
            Type:        "string",
            Description: "Shell command to execute (e.g., 'npm test')",
        },
        "timeout_seconds": {
            Type:        "number",
            Description: "Maximum execution time in seconds (default: 60)",
        },
    },
    Required: []string{"container_name"},
}
```

**Bad Schema** (vague, untyped):
```go
InputSchema: InputSchema{
    Type: "object",
    Properties: map[string]Property{
        "container": {
            Type:        "string",
            Description: "Container",
        },
        "cmd": {
            Type:        "string",
            Description: "Command",
        },
    },
}
```

### Error Handling

Tools should return clear, actionable errors:

```go
func (t *MyTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    name, ok := args["name"].(string)
    if !ok || name == "" {
        return nil, fmt.Errorf("name is required and must be a non-empty string")
    }

    result, err := doSomething(name)
    if err != nil {
        // Return structured error with context
        return map[string]any{
            "success": false,
            "error":   fmt.Sprintf("Operation failed: %v", err),
            "name":    name,
        }, nil // Don't propagate error, LLM can handle it
    }

    return map[string]any{
        "success": true,
        "result":  result,
        "name":    name,
    }, nil
}
```

### Documentation Strings

Provide clear, concise usage guidance:

```go
func (t *MyTool) PromptDocumentation() string {
    return `- **my_tool** - Brief one-line description
  - Required: param1 (what it does), param2 (what it does)
  - Optional: param3 (default behavior)
  - Returns: what the tool returns
  - Use for: when to use this tool
  - Example: concrete usage example`
}
```

## Tool Persistence and Logging

All tool executions are logged to the database for audit and debugging.

### Tool Execution Logging

Every tool call is recorded (`pkg/coder/planning.go:255`):

```go
c.logToolExecution(toolCall, result, err, duration)
```

**Implementation** (`pkg/coder/tool_helpers.go`):

```go
func (c *Coder) logToolExecution(toolCall *agent.ToolCall, result any, err error, duration time.Duration) {
    if c.persistenceChannel == nil {
        return
    }

    // Serialize result
    resultJSON, _ := json.Marshal(result)

    // Create log entry
    entry := &persistence.ToolExecution{
        AgentID:    c.agentID,
        ToolName:   toolCall.Name,
        Parameters: toolCall.Parameters,
        Result:     string(resultJSON),
        Error:      errString(err),
        Duration:   duration,
        Timestamp:  time.Now(),
    }

    // Send to persistence queue (fire-and-forget)
    c.persistenceChannel <- &persistence.Request{
        Operation: persistence.OpLogToolExecution,
        Data:      entry,
    }
}
```

### Database Schema

Tool executions are stored with full context:

```sql
CREATE TABLE tool_executions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    parameters JSON,
    result JSON,
    error TEXT,
    duration_ms INTEGER,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

This enables:
- Debugging tool failures
- Performance analysis
- Usage pattern analysis
- Audit trail for compliance

## Tool Testing

Tools include comprehensive test coverage.

### Unit Tests

Test tool schemas and basic execution (`pkg/tools/mcp_test.go`):

```go
func TestShellToolDefinition(t *testing.T) {
    tool := tools.NewShellTool(nil)
    def := tool.Definition()

    assert.Equal(t, "shell", def.Name)
    assert.Contains(t, def.Description, "Execute")
    assert.Equal(t, "object", def.InputSchema.Type)
    assert.Contains(t, def.InputSchema.Required, "cmd")
}

func TestShellToolExecution(t *testing.T) {
    mockExecutor := &MockExecutor{
        result: &exec.Result{
            Stdout:   "hello\n",
            Stderr:   "",
            ExitCode: 0,
        },
    }

    tool := tools.NewShellTool(mockExecutor)
    result, err := tool.Exec(context.Background(), map[string]any{
        "cmd": "echo hello",
    })

    require.NoError(t, err)
    resultMap := result.(map[string]any)
    assert.Equal(t, 0, resultMap["exit_code"])
    assert.Contains(t, resultMap["stdout"], "hello")
}
```

### Integration Tests

Test tools in real container environments (`pkg/tools/shell_integration_test.go`):

```go
func TestShellToolInContainer(t *testing.T) {
    // Create real Docker executor
    executor := exec.NewDockerLongRunningExec(
        "maestro-bootstrap",
        "/workspace",
        agentID,
    )

    tool := tools.NewShellTool(executor)

    result, err := tool.Exec(context.Background(), map[string]any{
        "cmd": "ls -la /workspace",
    })

    require.NoError(t, err)
    resultMap := result.(map[string]any)
    assert.Equal(t, 0, resultMap["exit_code"])
}
```

### Capability Tests

Validate tool requirements before execution (`pkg/tools/capability_test.go`):

```go
func TestValidateContainerCapabilities(t *testing.T) {
    executor := exec.NewLocalExec()

    result := tools.ValidateContainerCapabilities(
        context.Background(),
        executor,
        "maestro-bootstrap",
    )

    assert.True(t, result.Success)
    assert.Contains(t, result.Message, "validation passed")
    assert.True(t, result.HasGit)
    assert.True(t, result.HasGitHubCLI)
}
```

## Debugging Tools

### Tool Execution Logs

View tool execution history in logs:

```bash
# Real-time tool execution
tail -f logs/run.log | grep "Executing.*tool"

# Tool results
grep "tool.*executed successfully" logs/run.log
```

### Database Queries

Query tool execution history:

```sql
-- Recent tool executions
SELECT agent_id, tool_name, duration_ms, timestamp
FROM tool_executions
WHERE session_id = 'current-session'
ORDER BY timestamp DESC
LIMIT 50;

-- Tool failure analysis
SELECT tool_name, error, COUNT(*) as failures
FROM tool_executions
WHERE session_id = 'current-session'
  AND error IS NOT NULL
GROUP BY tool_name, error
ORDER BY failures DESC;

-- Performance analysis
SELECT tool_name,
       AVG(duration_ms) as avg_ms,
       MAX(duration_ms) as max_ms,
       COUNT(*) as executions
FROM tool_executions
WHERE session_id = 'current-session'
GROUP BY tool_name
ORDER BY avg_ms DESC;
```

### Web UI Tool Viewer

The Maestro Web UI provides real-time tool execution monitoring:

- **Tool Timeline**: Visual timeline of all tool calls
- **Parameters**: Inspect input arguments for each call
- **Results**: View structured output and errors
- **Duration**: Performance metrics for each execution
- **Filtering**: Filter by agent, tool name, or time range

## Advanced Topics

### Custom Tool Development

To add a new tool:

1. **Create Tool Implementation** (`pkg/tools/my_tool.go`):
```go
type MyTool struct {
    executor exec.Executor
}

func NewMyTool(executor exec.Executor) *MyTool {
    return &MyTool{executor: executor}
}

func (t *MyTool) Name() string {
    return "my_tool"
}

func (t *MyTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name:        "my_tool",
        Description: "Does something useful",
        InputSchema: InputSchema{
            Type: "object",
            Properties: map[string]Property{
                "param": {
                    Type:        "string",
                    Description: "Input parameter",
                },
            },
            Required: []string{"param"},
        },
    }
}

func (t *MyTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    param := args["param"].(string)
    // Implementation...
    return map[string]any{"result": "success"}, nil
}

func (t *MyTool) PromptDocumentation() string {
    return "- **my_tool** - Documentation"
}
```

2. **Create Factory** (`pkg/tools/registry.go`):
```go
func createMyTool(ctx AgentContext) (Tool, error) {
    if ctx.Executor == nil {
        return nil, fmt.Errorf("my_tool requires an executor")
    }
    return NewMyTool(ctx.Executor), nil
}

func getMyToolSchema() InputSchema {
    return NewMyTool(nil).Definition().InputSchema
}
```

3. **Register in init()** (`pkg/tools/registry.go`):
```go
func init() {
    // ... existing registrations

    Register(ToolMyTool, createMyTool, &ToolMeta{
        Name:        "my_tool",
        Description: "Does something useful",
        InputSchema: getMyToolSchema(),
    })
}
```

4. **Add to Tool Sets** (`pkg/tools/constants.go`):
```go
const ToolMyTool = "my_tool"

var AppCodingTools = []string{
    // ... existing tools
    ToolMyTool,
}
```

5. **Write Tests** (`pkg/tools/my_tool_test.go`):
```go
func TestMyTool(t *testing.T) {
    tool := NewMyTool(mockExecutor)
    result, err := tool.Exec(context.Background(), map[string]any{
        "param": "value",
    })
    require.NoError(t, err)
    // Assertions...
}
```

### Tool Composition

Complex operations can compose multiple tools:

```go
func (t *BuildAndTestTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    // Get sub-tools
    buildTool, _ := t.provider.Get("build")
    testTool, _ := t.provider.Get("test")

    // Execute in sequence
    buildResult, err := buildTool.Exec(ctx, map[string]any{})
    if err != nil {
        return nil, err
    }

    testResult, err := testTool.Exec(ctx, map[string]any{})
    if err != nil {
        return nil, err
    }

    return map[string]any{
        "build": buildResult,
        "test":  testResult,
    }, nil
}
```

### Dynamic Tool Generation

Tools can be generated programmatically based on runtime configuration:

```go
func createDynamicTools(config *Config) []ToolMeta {
    var tools []ToolMeta

    for _, endpoint := range config.APIEndpoints {
        tools = append(tools, ToolMeta{
            Name:        fmt.Sprintf("call_%s", endpoint.Name),
            Description: fmt.Sprintf("Call %s API", endpoint.Name),
            InputSchema: generateSchema(endpoint),
        })
    }

    return tools
}
```

## Performance Considerations

### Tool Caching

Tools are instantiated once and cached (`pkg/tools/registry.go:131-163`):

```go
// First call: creates and caches
tool1, _ := provider.Get("shell")

// Subsequent calls: returns cached instance
tool2, _ := provider.Get("shell") // Same instance as tool1
```

### Lazy Loading

Tools are only created when actually used:

```go
// Creating provider doesn't instantiate tools
provider := tools.NewProvider(ctx, []string{"shell", "build", "test"})

// Tools created on first Get() call
shellTool, _ := provider.Get("shell")    // Creates ShellTool
buildTool, _ := provider.Get("build")    // Creates BuildTool
// test tool never created if not used
```

### Execution Timeouts

All tool executions have configurable timeouts to prevent hangs:

```go
opts := exec.Opts{
    Timeout: 30 * time.Second, // Default for most tools
}

// Container tests may have longer timeouts
testOpts := exec.Opts{
    Timeout: 300 * time.Second, // 5 minutes for complex tests
}
```

## Security Considerations

### Input Validation

Tools must validate all inputs to prevent injection attacks:

```go
func (t *ShellTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    cmd, ok := args["cmd"].(string)
    if !ok || cmd == "" {
        return nil, fmt.Errorf("cmd must be a non-empty string")
    }

    // Additional validation for dangerous patterns
    if strings.Contains(cmd, "rm -rf /") {
        return nil, fmt.Errorf("dangerous command rejected")
    }

    // Execute...
}
```

### Path Traversal Prevention

File operations must prevent directory traversal:

```go
func (t *ReadFileTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    path := args["path"].(string)

    // Validate path doesn't escape workspace
    cleanPath := filepath.Clean(path)
    if strings.HasPrefix(cleanPath, "..") {
        return nil, fmt.Errorf("path traversal not allowed")
    }

    fullPath := filepath.Join(t.workspaceRoot, cleanPath)
    // Read file...
}
```

### Container Isolation

Tools execute with appropriate security restrictions:

- **Read-only filesystem**: During planning phase
- **No network**: During planning phase
- **Resource limits**: CPU, memory, PIDs
- **Security options**: `no-new-privileges`, `read-only`

### Secret Scanning

Tool results are scanned for secrets before logging:

```go
func (c *Coder) logToolExecution(toolCall *agent.ToolCall, result any, err error, duration time.Duration) {
    resultJSON, _ := json.Marshal(result)

    // Scan for secrets before logging
    if c.secretScanner != nil {
        if secrets := c.secretScanner.Scan(string(resultJSON)); len(secrets) > 0 {
            c.logger.Warn("Tool result contains potential secrets, redacting before log")
            resultJSON = redactSecrets(resultJSON, secrets)
        }
    }

    // Log to database...
}
```

## Summary

The Maestro MCP tools system provides a robust, extensible framework for AI agent capabilities:

**Key Features**:
- **Type-safe** tool definitions with JSON schemas
- **State-aware** tool availability based on agent phase
- **Lazy instantiation** for performance
- **Comprehensive logging** for debugging and audit
- **Security-first** design with validation and isolation
- **Flexible execution** strategies (container vs host)
- **Rich documentation** for LLM guidance

**Tool Lifecycle**:
1. **Creation**: Tools implement interface and register via init()
2. **Provider**: Agents create providers with state-specific context
3. **Schema**: Definitions converted to LLM-specific formats
4. **Documentation**: Markdown guides included in prompts
5. **Execution**: LLM calls tools, results added to context

**Best Practices**:
- Use factories for context-dependent instantiation
- Validate all inputs thoroughly
- Return structured results with clear error messages
- Log all executions for audit trail
- Test tools in isolation and integration
- Document usage clearly for LLM understanding

The tools system is the primary interface between AI agents and their execution environment, enabling them to explore codebases, modify files, run tests, manage containers, and collaborate with other agents—all while maintaining security, performance, and auditability.

---

**Related Documentation**:
- [Git Workflow](GIT_FLOW_WIKI.md) - How agents use git-related tools
- [Knowledge Graph](DOCS_WIKI.md) - How architect uses read tools
- [Project Architecture](../CLAUDE.md) - Overall system design
- [Agent Implementation](../pkg/tools/) - Tool source code
