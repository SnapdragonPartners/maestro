# Claude Code Integration Specification

## Document Status
- **Status**: Draft
- **Created**: 2025-01-07
- **Last Updated**: 2025-01-07
- **Version**: 1.0

## Overview

This specification describes the integration of Claude Code as an alternative coder agent implementation within the Maestro orchestration system. The integration will allow users to select between the traditional LLM+MCP tools approach ("classic mode") and a Claude Code-based approach ("Claude Code mode") via configuration.

### Goals

1. **Leverage Claude Code Optimizations**: Utilize Claude Code's highly optimized planning and coding workflows while maintaining Maestro's orchestration capabilities
2. **Minimal Divergence**: Share ~80% of coder agent code between classic and Claude Code modes
3. **Configuration-Based Selection**: Allow users to choose implementation mode per project
4. **Maintain Orchestration**: Preserve Maestro's multi-agent architecture, review cycles, and architect-coder interactions
5. **Production Ready**: Robust error handling, comprehensive testing, and observability

### Non-Goals

1. Replacing Maestro's orchestrator with Claude Code as primary entry point
2. Replacing architect agent with Claude Code
3. Supporting mixed mode (classic + Claude Code in same story)
4. Human-in-the-loop interactive mode with Claude Code

## Architecture

### High-Level Design

```
Orchestrator
    ↓
CoderBase (shared state machine)
    ↓
    ├─→ ClassicCoder (current implementation)
    │       ↓
    │   LLM Client + MCP Tools
    │
    └─→ ClaudeCodeCoder (new implementation)
            ↓
        Claude Code Process
            ↓
        Built-in Tools + MCP Bridge Server
```

### Component Architecture

```
maestro/
├── pkg/
│   ├── coder/
│   │   ├── base.go              # CoderBase - shared state machine (80% reuse)
│   │   ├── classic.go           # ClassicCoder - LLM+tools implementation
│   │   ├── claudecode.go        # ClaudeCodeCoder - Claude Code implementation
│   │   ├── factory.go           # Factory for creating appropriate coder
│   │   ├── planning.go          # Shared planning helpers
│   │   ├── coding.go            # Shared coding helpers
│   │   └── [other shared files] # Testing, effects, context management
│   │
│   ├── claudecode/
│   │   ├── manager.go           # Claude Code process lifecycle management
│   │   ├── handler.go           # Response parsing and signal detection
│   │   ├── protocol.go          # Stream-JSON protocol implementation
│   │   └── bridge/
│   │       ├── server.go        # MCP bridge server
│   │       ├── tools.go         # Bridge tool implementations
│   │       ├── client.go        # Bridge client (for Claude Code)
│   │       └── protocol.go      # MCP protocol handling
│   │
│   ├── templates/
│   │   ├── claudecode_planning.go   # Planning phase prompt template
│   │   └── claudecode_coding.go     # Coding phase prompt template
│   │
│   └── config/
│       └── config.go            # Extended with Claude Code configuration
│
└── cmd/
    └── maestro-bridge-client/
        └── main.go              # Bridge client binary (for Claude Code MCP)
```

## Design Principles

### 1. Shared State Machine

Both classic and Claude Code coders share the same state machine structure:

```
PLANNING → PLAN_REVIEW → CODING → TESTING → CODE_REVIEW →
AWAIT_APPROVAL → PREPARE_MERGE → DONE
```

**Shared States** (in `CoderBase`):
- PLAN_REVIEW: Architect reviews plan (identical for both modes)
- CODE_REVIEW: Architect reviews code (identical for both modes)
- TESTING: Run automated tests (identical for both modes)
- AWAIT_APPROVAL: Wait for architect approval (identical for both modes)
- PREPARE_MERGE: Prepare merge request (identical for both modes)
- QUESTION: Handle architect questions (identical for both modes)
- BUDGET_REVIEW: Budget exceeded handling (identical for both modes)
- ERROR: Error state (identical for both modes)
- DONE: Completion (identical for both modes)

**Divergent States** (implemented differently):
- PLANNING: How plan is generated (LLM+tools vs Claude Code)
- CODING: How code is generated (LLM+tools vs Claude Code)

### 2. Strategy Pattern for Divergence

```go
// pkg/coder/base.go
type CoderBase struct {
    // Shared fields
    agentID           string
    workDir           string
    dispatcher        *dispatch.Dispatcher
    llmClient         agent.LLMClient  // Only used by classic
    contextManager    *contextmgr.Manager
    renderer          *templates.Renderer
    persistenceChannel chan<- *persistence.Request
    chatService       *chat.Service

    // Strategy interfaces for divergent behavior
    planningHandler   PlanningHandler
    codingHandler     CodingHandler

    // Shared state
    sm                *agent.BaseStateMachine
    todoList          *TodoList
    logger            *logx.Logger
}

// Strategy interfaces
type PlanningHandler interface {
    ExecutePlanning(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error)
}

type CodingHandler interface {
    ExecuteCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error)
}

// State machine dispatcher delegates to appropriate handler
func (c *CoderBase) Run(ctx context.Context) {
    switch currentState {
    case StatePlanning:
        return c.planningHandler.ExecutePlanning(ctx, c.sm)
    case StateCoding:
        return c.codingHandler.ExecuteCoding(ctx, c.sm)
    case StatePlanReview:
        return c.handlePlanReview(ctx, c.sm)  // Shared implementation
    case StateCodeReview:
        return c.handleCodeReview(ctx, c.sm)  // Shared implementation
    // ... other shared state handlers
    }
}
```

### 3. Container Lifecycle Alignment

Claude Code process lifecycle aligns with container lifecycle:

**PLANNING Phase Container** (read-only mount):
```
1. Start container with ro mount
2. Launch Claude Code process
3. Execute PLANNING state (Claude Code running)
4. Handle PLAN_REVIEW, QUESTION states (Claude Code still running)
5. Terminate Claude Code process
6. Stop container
```

**CODING Phase Container** (read-write mount):
```
1. Start container with rw mount
2. Launch Claude Code process with approved plan context
3. Execute CODING state (Claude Code running)
4. Handle TESTING, CODE_REVIEW, QUESTION, BUDGET_REVIEW states (Claude Code still running)
5. Terminate Claude Code process on DONE or ERROR
6. Stop container
```

**Key Points**:
- Claude Code process runs for entire container lifecycle (PLANNING or CODING phase)
- State transitions within a phase are transparent to Claude Code (just user messages)
- Only PLANNING→CODING transition requires container/process restart

## Component Specifications

### 1. Claude Code Process Manager

**File**: `pkg/claudecode/manager.go`

**Responsibilities**:
- Start Claude Code process within coder container via `docker exec`
- Manage stdin/stdout/stderr pipes with stream-json protocol
- Parse responses and detect completion signals
- Handle graceful restart (one attempt on failure)
- Clean shutdown and resource cleanup

**Key Types**:

```go
package claudecode

type Manager struct {
    containerName string
    executor      exec.Executor
    cmd           *exec.Cmd
    stdin         io.WriteCloser
    stdout        *bufio.Reader
    stderr        *bufio.Reader
    sessionID     string
    workDir       string
    mode          Mode  // PLANNING or CODING
    logger        *logx.Logger

    // State
    running       bool
    mu            sync.Mutex
}

type Mode string

const (
    ModePlanning Mode = "PLANNING"
    ModeCoding   Mode = "CODING"
)

type StartOptions struct {
    Mode              Mode
    SessionID         string
    WorkDir           string
    StoryContent      string     // For PLANNING
    ApprovedPlan      string     // For CODING
    SystemPrompt      string
    BridgeConfigPath  string
    AgentID           string
}

type Message struct {
    Role    string `json:"role"`    // "user" or "assistant"
    Content string `json:"content"`
}

type Response struct {
    Type         string      `json:"type"`         // "assistant", "tool_result", "error"
    Content      string      `json:"content"`
    ToolCalls    []ToolCall  `json:"tool_calls,omitempty"`
    ToolResults  []ToolResult `json:"tool_results,omitempty"`
    Completion   bool        `json:"completion,omitempty"`
    Error        string      `json:"error,omitempty"`
}

type Signal string

const (
    SignalPlanComplete  Signal = "PLAN_COMPLETE"
    SignalDone          Signal = "DONE"
    SignalStoryComplete Signal = "STORY_COMPLETE"
    SignalError         Signal = "ERROR"
)
```

**Key Methods**:

```go
// Start launches Claude Code process in container
func (m *Manager) Start(ctx context.Context, opts StartOptions) error

// SendMessage sends user message to Claude Code via stdin
func (m *Manager) SendMessage(ctx context.Context, msg Message) error

// ReadResponse reads next response from Claude Code (blocking)
func (m *Manager) ReadResponse(ctx context.Context) (*Response, error)

// Stop gracefully terminates Claude Code process
func (m *Manager) Stop(ctx context.Context) error

// Restart attempts one graceful restart on failure
func (m *Manager) Restart(ctx context.Context) error
```

**Claude Code Launch Command**:

```bash
claude --print \
  --output-format stream-json \
  --input-format stream-json \
  --session-id <uuid> \
  --tools default \
  --mcp-config /workspace/.maestro/bridge-config.json \
  --append-system-prompt "<maestro-system-prompt>"
```

### 2. MCP Bridge Server

**File**: `pkg/claudecode/bridge/server.go`

**Purpose**: Enable Claude Code to invoke Maestro-specific operations (ask architect, signal completion, etc.)

**Architecture**:

```
Claude Code Process
    ↓ (MCP protocol via bridge client)
Bridge Client Binary (maestro-bridge-client)
    ↓ (Unix socket)
Bridge Server (in orchestrator process)
    ↓ (Go function calls)
Dispatcher / Effects System
```

**Bridge Server Implementation**:

```go
package bridge

type Server struct {
    socketPath  string
    listener    net.Listener
    dispatcher  *dispatch.Dispatcher
    logger      *logx.Logger

    // Active agent tracking
    agents      map[string]*AgentContext  // agentID -> context
    mu          sync.RWMutex
}

type AgentContext struct {
    AgentID    string
    StoryID    string
    StateMachine *agent.BaseStateMachine
}

// Start begins listening for bridge client connections
func (s *Server) Start(ctx context.Context) error

// Stop gracefully shuts down bridge server
func (s *Server) Stop(ctx context.Context) error

// RegisterAgent registers an active agent for tool calls
func (s *Server) RegisterAgent(agentID string, ctx *AgentContext)

// UnregisterAgent removes agent on completion/error
func (s *Server) UnregisterAgent(agentID string)

// HandleConnection processes MCP requests from a bridge client
func (s *Server) HandleConnection(conn net.Conn)
```

**Bridge Client Binary**:

**File**: `cmd/maestro-bridge-client/main.go`

Thin wrapper that:
1. Reads MCP requests from stdin (from Claude Code)
2. Connects to bridge server via Unix socket
3. Adds agent_id metadata from environment
4. Forwards requests to bridge server
5. Returns responses to stdout (back to Claude Code)

**Launch Configuration** (generated per agent):

```json
// /workspace/.maestro/bridge-config.json
{
  "mcpServers": {
    "maestro": {
      "command": "/usr/local/bin/maestro-bridge-client",
      "args": ["--socket", "/tmp/maestro-bridge.sock"],
      "env": {
        "CODER_AGENT_ID": "coder-001",
        "CODER_STORY_ID": "story-042"
      }
    }
  }
}
```

### 3. Bridge Tools

**File**: `pkg/claudecode/bridge/tools.go`

**Tool Definitions**:

#### maestro_submit_plan

Signals that planning is complete and submits plan for review.

```go
type SubmitPlanTool struct {
    server *Server
}

// Input schema
{
  "name": "maestro_submit_plan",
  "description": "Submit your implementation plan to the architect for review. Call this when you have completed your planning and are ready to proceed to implementation.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "plan": {
        "type": "string",
        "description": "Your detailed implementation plan, including approach, files to modify, testing strategy, and any risks or dependencies"
      }
    },
    "required": ["plan"]
  }
}

func (t *SubmitPlanTool) Execute(ctx context.Context, args map[string]any) (any, error) {
    agentID := getAgentIDFromContext(ctx)
    plan := args["plan"].(string)

    // Store plan in agent's state data
    agentCtx := t.server.GetAgent(agentID)
    agentCtx.StateMachine.SetStateData("plan", plan)

    return map[string]any{
        "status": "success",
        "message": "Plan submitted for architect review",
        "signal": "PLAN_COMPLETE",
    }, nil
}
```

#### maestro_ask_question

Sends question to architect and blocks until answer is received.

```go
type AskQuestionTool struct {
    server *Server
}

// Input schema
{
  "name": "maestro_ask_question",
  "description": "Ask the architect for clarification or guidance. This will pause your work until the architect responds. Use when you encounter ambiguity or need technical direction.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "question": {
        "type": "string",
        "description": "Your question for the architect"
      },
      "context": {
        "type": "string",
        "description": "Context about why you're asking (what you're working on, what you've tried)"
      },
      "urgency": {
        "type": "string",
        "enum": ["low", "medium", "high"],
        "description": "How critical is this question to proceeding?"
      }
    },
    "required": ["question", "context"]
  }
}

const QuestionTimeout = 10 * time.Minute

func (t *AskQuestionTool) Execute(ctx context.Context, args map[string]any) (any, error) {
    agentID := getAgentIDFromContext(ctx)
    question := args["question"].(string)
    context := args["context"].(string)
    urgency := args["urgency"].(string)
    if urgency == "" {
        urgency = "medium"
    }

    // Create question effect
    agentCtx := t.server.GetAgent(agentID)
    effect := effect.NewQuestionEffect(question, context, urgency, "PLANNING_OR_CODING")
    effect.StoryID = agentCtx.StoryID

    // Execute with timeout (blocks until answer received)
    execCtx, cancel := context.WithTimeout(ctx, QuestionTimeout)
    defer cancel()

    result, err := t.server.dispatcher.ExecuteEffect(execCtx, effect)
    if err == context.DeadlineExceeded {
        return map[string]any{
            "status": "timeout",
            "message": "Architect did not respond within timeout. Continue with your best judgment and document your decision.",
        }, nil
    }
    if err != nil {
        return nil, fmt.Errorf("failed to get answer: %w", err)
    }

    questionResult := result.(*effect.QuestionResult)
    return map[string]any{
        "status": "answered",
        "answer": questionResult.Answer,
    }, nil
}
```

#### maestro_done

Signals that implementation is complete and ready for testing/review.

```go
type DoneTool struct {
    server *Server
}

// Input schema
{
  "name": "maestro_done",
  "description": "Signal that you have completed the implementation and it is ready for automated testing and architect review. Include a summary of what was implemented.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "summary": {
        "type": "string",
        "description": "Brief summary of what was implemented, key decisions made, and any notes for the reviewer"
      }
    },
    "required": ["summary"]
  }
}

func (t *DoneTool) Execute(ctx context.Context, args map[string]any) (any, error) {
    agentID := getAgentIDFromContext(ctx)
    summary := args["summary"].(string)

    // Store completion details for code review
    agentCtx := t.server.GetAgent(agentID)
    agentCtx.StateMachine.SetStateData("completion_summary", summary)

    return map[string]any{
        "status": "success",
        "message": "Implementation marked complete. Proceeding to testing and review.",
        "signal": "DONE",
    }, nil
}
```

#### maestro_mark_story_complete

Signals that story requirements are already implemented (no work needed).

```go
type MarkStoryCompleteTool struct {
    server *Server
}

// Input schema
{
  "name": "maestro_mark_story_complete",
  "description": "Signal that the story requirements are already fully implemented in the codebase. Use this when analysis shows no work is needed.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "reason": {
        "type": "string",
        "description": "Explanation of why the story is already complete, with references to existing code"
      }
    },
    "required": ["reason"]
  }
}

func (t *MarkStoryCompleteTool) Execute(ctx context.Context, args map[string]any) (any, error) {
    agentID := getAgentIDFromContext(ctx)
    reason := args["reason"].(string)

    agentCtx := t.server.GetAgent(agentID)
    agentCtx.StateMachine.SetStateData("completion_reason", reason)

    return map[string]any{
        "status": "success",
        "message": "Story marked as already complete",
        "signal": "STORY_COMPLETE",
    }, nil
}
```

### 4. Response Handler

**File**: `pkg/claudecode/handler.go`

**Responsibilities**:
- Parse Claude Code stream-json responses
- Detect completion signals in tool results
- Extract file changes for tracking
- Handle errors and malformed responses

**Key Types**:

```go
package claudecode

type Handler struct {
    logger *logx.Logger
}

type ParsedResponse struct {
    Content       string
    ToolCalls     []ToolCall
    ToolResults   []ToolResult
    Signal        Signal        // Extracted from maestro_* tool results
    SignalData    map[string]any // Data associated with signal
    FileChanges   []FileChange  // Detected file modifications
    Error         error
}

type FileChange struct {
    Path      string
    Operation string  // "created", "modified", "deleted"
    Language  string  // Detected language
}

// Parse processes a raw response from Claude Code
func (h *Handler) Parse(response *Response) (*ParsedResponse, error)

// DetectSignal extracts Maestro signals from tool results
func (h *Handler) DetectSignal(toolResults []ToolResult) (Signal, map[string]any)

// ExtractFileChanges infers file changes from Bash/Write/Edit tool calls
func (h *Handler) ExtractFileChanges(toolCalls []ToolCall) []FileChange
```

### 5. Claude Code Coder Agent

**File**: `pkg/coder/claudecode.go`

**Implementation**:

```go
package coder

type ClaudeCodeCoder struct {
    *CoderBase  // Embed shared functionality

    manager *claudecode.Manager
    handler *claudecode.Handler
    bridge  *bridge.Server
}

// NewClaudeCodeCoder creates a Claude Code-based coder agent
func NewClaudeCodeCoder(
    agentID string,
    workDir string,
    dispatcher *dispatch.Dispatcher,
    // ... other dependencies
) (*ClaudeCodeCoder, error) {
    base := &CoderBase{
        agentID:    agentID,
        workDir:    workDir,
        dispatcher: dispatcher,
        // ... initialize shared fields
    }

    coder := &ClaudeCodeCoder{
        CoderBase: base,
        manager:   claudecode.NewManager(...),
        handler:   claudecode.NewHandler(...),
    }

    // Set strategy handlers
    base.planningHandler = coder
    base.codingHandler = coder

    return coder, nil
}

// ExecutePlanning implements PlanningHandler
func (c *ClaudeCodeCoder) ExecutePlanning(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // 1. Start Claude Code process (if not running)
    if !c.manager.IsRunning() {
        opts := claudecode.StartOptions{
            Mode:         claudecode.ModePlanning,
            SessionID:    c.generateSessionID(),
            WorkDir:      "/workspace",
            SystemPrompt: c.buildPlanningPrompt(sm),
            AgentID:      c.agentID,
        }
        if err := c.manager.Start(ctx, opts); err != nil {
            return proto.StateError, false, err
        }
    }

    // 2. Send story content as initial message
    storyContent := utils.GetStateValueOr[string](sm, "task_content", "")
    if err := c.manager.SendMessage(ctx, claudecode.Message{
        Role:    "user",
        Content: c.formatStoryForPlanning(storyContent, sm),
    }); err != nil {
        return proto.StateError, false, err
    }

    // 3. Read responses until plan submitted
    for {
        response, err := c.manager.ReadResponse(ctx)
        if err != nil {
            return c.handleClaudeCodeError(err, sm)
        }

        parsed := c.handler.Parse(response)

        // Check for completion signal
        if parsed.Signal == claudecode.SignalPlanComplete {
            plan := parsed.SignalData["plan"].(string)
            sm.SetStateData("plan", plan)
            c.logger.Info("Plan submitted, transitioning to PLAN_REVIEW")
            return StatePlanReview, false, nil
        }

        if parsed.Signal == claudecode.SignalStoryComplete {
            reason := parsed.SignalData["reason"].(string)
            sm.SetStateData("completion_reason", reason)
            c.logger.Info("Story marked complete: %s", reason)
            return StateDone, false, nil
        }

        // Continue conversation (Claude Code is still working)
        c.logger.Debug("Planning in progress, received: %s", parsed.Content)
    }
}

// ExecuteCoding implements CodingHandler
func (c *ClaudeCodeCoder) ExecuteCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // 1. Start Claude Code process (if not running)
    if !c.manager.IsRunning() {
        plan := utils.GetStateValueOr[string](sm, "plan", "")
        opts := claudecode.StartOptions{
            Mode:          claudecode.ModeCoding,
            SessionID:     c.generateSessionID(),
            WorkDir:       "/workspace",
            ApprovedPlan:  plan,
            SystemPrompt:  c.buildCodingPrompt(sm),
            AgentID:       c.agentID,
        }
        if err := c.manager.Start(ctx, opts); err != nil {
            return proto.StateError, false, err
        }
    }

    // 2. Send coding start message
    if err := c.manager.SendMessage(ctx, claudecode.Message{
        Role:    "user",
        Content: c.formatCodingStart(sm),
    }); err != nil {
        return proto.StateError, false, err
    }

    // 3. Read responses until implementation complete
    for {
        response, err := c.manager.ReadResponse(ctx)
        if err != nil {
            return c.handleClaudeCodeError(err, sm)
        }

        parsed := c.handler.Parse(response)

        // Track file changes
        if len(parsed.FileChanges) > 0 {
            c.trackFileChanges(sm, parsed.FileChanges)
        }

        // Check for completion signal
        if parsed.Signal == claudecode.SignalDone {
            summary := parsed.SignalData["summary"].(string)
            sm.SetStateData("completion_summary", summary)
            c.logger.Info("Implementation complete, transitioning to TESTING")
            return StateTesting, false, nil
        }

        // Continue conversation
        c.logger.Debug("Coding in progress, received: %s", parsed.Content)
    }
}

// handleClaudeCodeError handles process failures with one restart attempt
func (c *ClaudeCodeCoder) handleClaudeCodeError(err error, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    if utils.GetStateValueOr[bool](sm, "claude_code_restart_attempted", false) {
        c.logger.Error("Claude Code failed after restart attempt: %v", err)
        return proto.StateError, false, err
    }

    c.logger.Warn("Claude Code error, attempting restart: %v", err)
    sm.SetStateData("claude_code_restart_attempted", true)

    if restartErr := c.manager.Restart(context.Background()); restartErr != nil {
        return proto.StateError, false, fmt.Errorf("restart failed: %w", restartErr)
    }

    c.logger.Info("Claude Code restarted successfully, continuing")
    // Return current state to retry
    return sm.GetCurrentState(), false, nil
}
```

### 6. System Prompt Templates

**File**: `pkg/templates/claudecode_planning.go`

```go
package templates

const ClaudeCodePlanningTemplate = `# PLANNING MODE

You are a coder agent in the Maestro multi-agent development system. Your task is to analyze the following story and create a detailed implementation plan.

## Story

{{.TaskContent}}

## Context

**Workspace**: {{.WorkDir}}
**Container**: {{.ContainerName}}
**Mode**: Read-only filesystem (exploration only, no modifications)

{{if .KnowledgePacks}}
## Available Documentation

{{range .KnowledgePacks}}
- {{.Name}}: {{.Description}}
{{end}}

Use your Read and Grep tools to explore these knowledge packs for relevant information.
{{end}}

## Your Task

1. **Explore the Codebase**: Use your standard tools (Bash, Read, Glob, Grep) to understand the current implementation
2. **Analyze Requirements**: Break down the story into specific technical requirements
3. **Design Approach**: Determine the implementation approach, files to modify, and testing strategy
4. **Create Plan**: Write a detailed implementation plan

## Available Maestro Tools

You have access to special Maestro integration tools:

- **maestro_submit_plan**: Submit your implementation plan when ready
  - Required parameter: `plan` (string) - Your detailed plan
  - This will send your plan to the architect for review

- **maestro_ask_question**: Ask the architect for clarification
  - Parameters: `question`, `context`, `urgency`
  - Use when you encounter ambiguity or need technical guidance
  - Your work will pause until the architect responds (up to 10 minutes)

- **maestro_mark_story_complete**: Signal that requirements are already implemented
  - Required parameter: `reason` (string) - Why no work is needed
  - Use only if analysis shows story is already complete

## Git Usage Guidelines

- You are working in a feature branch
- Commits are **allowed** for incremental progress
- **DO NOT**: switch branches, merge, rebase, reset, or modify git configuration
- Keep commits atomic with clear messages

## Instructions

1. Start by exploring the codebase to understand the current state
2. Review any relevant documentation in knowledge packs
3. If requirements are unclear, use maestro_ask_question
4. Once you have a clear plan, call maestro_submit_plan with your detailed approach
5. If analysis shows the story is already complete, call maestro_mark_story_complete

**Remember**: You are in read-only mode. Do not attempt to modify files. Focus on analysis and planning.
`

const ClaudeCodeCodingTemplate = `# CODING MODE

You are a coder agent in the Maestro multi-agent development system. Your task is to implement the following approved plan.

## Approved Plan

{{.Plan}}

## Context

**Workspace**: {{.WorkDir}}
**Container**: {{.ContainerName}}
**Mode**: Read-write filesystem (full implementation access)

{{if .ContainerDockerfile}}
**Container Image**: {{.ContainerName}} (built from {{.ContainerDockerfile}})
{{end}}

{{if .KnowledgePacks}}
## Available Documentation

{{range .KnowledgePacks}}
- {{.Name}}: {{.Description}}
{{end}}
{{end}}

## Your Task

Implement the approved plan above. You have full access to the workspace and all standard development tools.

## Available Maestro Tools

- **maestro_done**: Mark implementation complete when finished
  - Required parameter: `summary` (string) - Brief summary of implementation
  - Call this when implementation and testing are complete
  - The system will then run automated tests and send to architect for review

- **maestro_ask_question**: Ask the architect for guidance
  - Parameters: `question`, `context`, `urgency`
  - Use if you encounter issues or need clarification during implementation
  - Your work will pause until the architect responds (up to 10 minutes)

## Git Usage Guidelines

- You are working in a feature branch
- Commits are **encouraged** for incremental progress
- **DO NOT**: switch branches, merge, rebase, reset, or modify git configuration
- Commit frequently with clear, descriptive messages

## Testing

- Run tests as you implement (use your Bash tool to run test commands)
- Ensure all tests pass before calling maestro_done
- Fix any linting or build errors
- The system will run comprehensive automated tests after you signal completion

## Instructions

1. Implement your approved plan step by step
2. Test your changes as you go
3. Commit your work incrementally with good messages
4. If you encounter blockers or ambiguities, use maestro_ask_question
5. When implementation is complete and tested, call maestro_done with a summary

**Remember**: You have full read-write access. Implement thoroughly and test carefully.
`
```

### 7. Configuration

**File**: `pkg/config/config.go`

```go
// CoderConfig contains configuration for coder agents
type CoderConfig struct {
    // Mode selects the coder implementation
    // Options: "classic" (default) or "claude-code"
    Mode string `json:"mode"`

    // MaxCoders is the maximum number of concurrent coder agents
    MaxCoders int `json:"max_coders"`

    // Claude Code specific configuration
    ClaudeCode *ClaudeCodeConfig `json:"claude_code,omitempty"`
}

// ClaudeCodeConfig contains Claude Code-specific settings
type ClaudeCodeConfig struct {
    // Enabled explicitly enables Claude Code mode (in addition to mode selection)
    Enabled bool `json:"enabled"`

    // BinaryPath is the path to the claude binary
    // Default: "claude" (assumes in PATH)
    BinaryPath string `json:"binary_path"`

    // SessionTimeout is the maximum duration for a Claude Code session
    // Default: 30 minutes
    SessionTimeout Duration `json:"session_timeout"`

    // BridgeSocketPath is the Unix socket path for the MCP bridge server
    // Default: "/tmp/maestro-bridge.sock"
    BridgeSocketPath string `json:"bridge_socket_path"`

    // AutoInstall automatically installs Claude Code in bootstrap container if missing
    // Default: true
    AutoInstall bool `json:"auto_install"`

    // DebugMode enables verbose logging of Claude Code interactions
    // Default: false
    DebugMode bool `json:"debug_mode"`
}
```

**Example Configuration**:

```json
{
  "coder": {
    "mode": "claude-code",
    "max_coders": 5,
    "claude_code": {
      "enabled": true,
      "binary_path": "claude",
      "session_timeout": "30m",
      "bridge_socket_path": "/tmp/maestro-bridge.sock",
      "auto_install": true,
      "debug_mode": false
    }
  }
}
```

## Workflow Examples

### Example 1: PLANNING State Flow

```
1. Orchestrator: Assign story to ClaudeCodeCoder agent

2. ClaudeCodeCoder.ExecutePlanning():
   - Start container (ro mount)
   - Launch Claude Code process:
     $ claude --print --output-format stream-json --input-format stream-json \
         --tools default \
         --mcp-config /workspace/.maestro/bridge-config.json \
         --append-system-prompt "<planning template>"

3. Send initial message:
   {
     "role": "user",
     "content": "Story: Add user authentication...\n\n[story details]"
   }

4. Claude Code explores codebase:
   → Read tool: /workspace/pkg/auth/handlers.go
   → Grep tool: search for "authentication" patterns
   → Bash tool: find . -name "*auth*"

5. Claude Code calls maestro_ask_question:
   {
     "tool": "maestro_ask_question",
     "input": {
       "question": "Should we use JWT or session-based auth?",
       "context": "Existing code uses sessions, but JWT might scale better",
       "urgency": "high"
     }
   }

6. Bridge server:
   - Receives MCP request from bridge client
   - Creates QuestionEffect
   - Sends QUESTION message to architect
   - Blocks waiting for answer (up to 10 minutes)

7. Architect responds:
   - ANSWER message: "Use JWT - we're moving to stateless architecture"

8. Bridge server returns answer to Claude Code:
   {
     "status": "answered",
     "answer": "Use JWT - we're moving to stateless architecture"
   }

9. Claude Code continues planning with answer

10. Claude Code completes plan:
    {
      "tool": "maestro_submit_plan",
      "input": {
        "plan": "## Implementation Plan\n\n1. Create JWT utilities...\n2. Update auth middleware...\n..."
      }
    }

11. Bridge server:
    - Receives submit_plan request
    - Stores plan in agent's state data
    - Returns success with PLAN_COMPLETE signal

12. ClaudeCodeCoder detects signal:
    - Extracts plan from signal data
    - Returns StatePlanReview

13. Orchestrator:
    - Transitions agent to PLAN_REVIEW
    - Sends plan to architect for review

14. (Later) Architect approves plan

15. Orchestrator:
    - Terminates Claude Code process
    - Stops container
```

### Example 2: CODING State Flow

```
1. Orchestrator: Transition to CODING state after plan approval

2. ClaudeCodeCoder.ExecuteCoding():
   - Start container (rw mount)
   - Launch Claude Code process with approved plan:
     $ claude --print --output-format stream-json --input-format stream-json \
         --tools default \
         --mcp-config /workspace/.maestro/bridge-config.json \
         --append-system-prompt "<coding template with plan>"

3. Send initial message:
   {
     "role": "user",
     "content": "Your plan has been approved. Begin implementation:\n\n[approved plan]"
   }

4. Claude Code implements plan:
   → Write tool: Create /workspace/pkg/auth/jwt.go
   → Edit tool: Modify /workspace/pkg/auth/middleware.go
   → Bash tool: go build ./...
   → Bash tool: go test ./pkg/auth/...

5. Handler tracks file changes:
   - Detects Write/Edit tool calls
   - Extracts file paths and operations
   - Updates state data with file change list

6. Claude Code encounters issue, asks question:
   {
     "tool": "maestro_ask_question",
     "input": {
       "question": "Tests are failing - should we update the test fixtures or the implementation?",
       "context": "JWT token generation tests expect old format",
       "urgency": "medium"
     }
   }

7. Architect responds with guidance

8. Claude Code continues, fixes tests

9. Claude Code completes implementation:
   {
     "tool": "maestro_done",
     "input": {
       "summary": "Implemented JWT authentication with refresh tokens. All tests passing. Updated middleware and added comprehensive error handling."
     }
   }

10. Bridge server:
    - Receives done request
    - Stores completion summary in state data
    - Returns success with DONE signal

11. ClaudeCodeCoder detects signal:
    - Extracts summary
    - Returns StateTesting

12. Orchestrator:
    - Transitions to TESTING state
    - CoderBase.handleTesting() runs automated tests (shared logic)
    - Transitions to CODE_REVIEW
    - Sends code to architect for review

13. (Later) Review cycle completes, story marked done

14. Orchestrator:
    - Terminates Claude Code process
    - Stops container
```

### Example 3: Error Recovery

```
1. Claude Code is running during CODING state

2. Claude Code process crashes (exit code 1)

3. Manager.ReadResponse() detects EOF on stdout

4. ClaudeCodeCoder.handleClaudeCodeError():
   - Check restart_attempted flag (false)
   - Set restart_attempted = true
   - Call manager.Restart()

5. Manager.Restart():
   - Stop old process (if still running)
   - Launch new Claude Code process with same session ID
   - Restore conversation history from state data
   - Send "Continue from where you left off" message

6. Claude Code resumes work

7. If Claude Code crashes again:
   - restart_attempted = true
   - Transition to ERROR state
   - Agent garbage collected
   - Orchestrator creates new agent, story requeued
```

## Error Handling

### Error Categories

**1. Fatal Errors** (transition to ERROR state immediately):
- Claude Code binary not found and auto-install fails
- Cannot start container
- Bridge server unavailable
- Second failure after restart attempt
- Unrecoverable parse error

**2. Recoverable Errors** (attempt one restart):
- Claude Code process crash (unexpected exit)
- Broken pipe / stream error
- Process timeout (no response for extended period)
- Malformed JSON response

**3. Normal Errors** (handle gracefully, continue):
- Tool execution failure (pass error to Claude Code)
- LLM refusal (Claude Code handles)
- Architect timeout (return timeout message to Claude Code)
- Plan/code rejection in review (orchestrator handles)

### Error Handling Implementation

```go
// pkg/coder/claudecode.go

func (c *ClaudeCodeCoder) handleClaudeCodeError(err error, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // Classify error
    if isFatalError(err) {
        c.logger.Error("Fatal Claude Code error: %v", err)
        return proto.StateError, false, err
    }

    // Check if restart already attempted
    if utils.GetStateValueOr[bool](sm, "claude_code_restart_attempted", false) {
        c.logger.Error("Claude Code failed after restart: %v", err)
        return proto.StateError, false, fmt.Errorf("claude code failed after restart: %w", err)
    }

    // Attempt graceful restart
    c.logger.Warn("Claude Code error, attempting restart: %v", err)
    sm.SetStateData("claude_code_restart_attempted", true)

    restartCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if restartErr := c.manager.Restart(restartCtx); restartErr != nil {
        c.logger.Error("Restart failed: %v", restartErr)
        return proto.StateError, false, fmt.Errorf("restart failed: %w", restartErr)
    }

    c.logger.Info("Claude Code restarted successfully")

    // Clear restart flag after successful restart
    sm.SetStateData("claude_code_restart_attempted", false)

    // Return current state to continue
    return sm.GetCurrentState(), false, nil
}

func isFatalError(err error) bool {
    // Check error type/message for fatal conditions
    if strings.Contains(err.Error(), "binary not found") {
        return true
    }
    if strings.Contains(err.Error(), "bridge unavailable") {
        return true
    }
    if strings.Contains(err.Error(), "unrecoverable parse error") {
        return true
    }
    return false
}
```

## Bootstrap Container Integration

Claude Code must be available in the bootstrap container to handle bootstrap stories (container/infrastructure setup).

### Installation Strategy

**Dockerfile Addition** (`docker/bootstrap/Dockerfile`):

```dockerfile
# Install Claude Code CLI
RUN npm install -g @anthropic/claude-code@latest

# Verify installation
RUN claude --version
```

### Auto-Install for User Containers

If user container doesn't have Claude Code installed and `auto_install: true`:

```go
// pkg/claudecode/manager.go

func (m *Manager) ensureClaudeCodeInstalled(ctx context.Context) error {
    // Check if claude binary exists
    checkCmd := []string{"which", "claude"}
    result, err := m.executor.Run(ctx, checkCmd, &exec.Opts{Timeout: 5 * time.Second})

    if err == nil && result.ExitCode == 0 {
        // Already installed
        m.logger.Info("Claude Code found at: %s", strings.TrimSpace(result.Stdout))
        return nil
    }

    // Check if auto-install enabled
    cfg, _ := config.GetConfig()
    if cfg.Coder.ClaudeCode == nil || !cfg.Coder.ClaudeCode.AutoInstall {
        return fmt.Errorf("claude binary not found and auto_install is disabled")
    }

    // Attempt installation
    m.logger.Warn("Claude Code not found, attempting automatic installation...")

    installCmd := []string{"npm", "install", "-g", "@anthropic/claude-code@latest"}
    result, err = m.executor.Run(ctx, installCmd, &exec.Opts{Timeout: 60 * time.Second})

    if err != nil || result.ExitCode != 0 {
        return fmt.Errorf("auto-install failed: %w\nOutput: %s", err, result.Stderr)
    }

    m.logger.Info("Claude Code installed successfully")
    return nil
}
```

## Metrics & Observability

### Metrics to Track

**Performance Metrics**:
- `claude_code_session_duration_seconds` - Time from start to completion
- `claude_code_response_latency_seconds` - Time from message to response
- `claude_code_token_count` - Tokens used (if available from API)
- `claude_code_api_cost_dollars` - Estimated cost based on pricing

**Reliability Metrics**:
- `claude_code_process_starts_total` - Process launch count
- `claude_code_process_crashes_total` - Unexpected terminations
- `claude_code_restarts_total` - Graceful restart attempts
- `claude_code_errors_total` - Errors by type

**Functional Metrics**:
- `claude_code_plans_submitted_total` - Planning completions
- `claude_code_implementations_completed_total` - Coding completions
- `claude_code_questions_asked_total` - Architect questions
- `claude_code_stories_skipped_total` - Already-complete stories

**Comparison Metrics** (classic vs Claude Code):
- `coder_story_completion_time_seconds{mode="classic|claude-code"}`
- `coder_token_usage_total{mode="classic|claude-code"}`
- `coder_api_cost_dollars{mode="classic|claude-code"}`
- `coder_iterations_to_approval{mode="classic|claude-code"}`

### Logging

**Standard Log Events**:
```
INFO  [coder-001] Claude Code process started (session: abc123, mode: PLANNING)
DEBUG [coder-001] Sent message to Claude Code: "Story: Add authentication..."
DEBUG [coder-001] Received response from Claude Code: [125 chars]
INFO  [coder-001] Claude Code called maestro_ask_question
INFO  [coder-001] Plan submitted, transitioning to PLAN_REVIEW
WARN  [coder-001] Claude Code process crashed, attempting restart
INFO  [coder-001] Claude Code restarted successfully
ERROR [coder-001] Claude Code failed after restart: broken pipe
INFO  [coder-001] Claude Code process terminated (session: abc123, duration: 5m23s)
```

**Debug Mode** (`debug_mode: true`):
```
DEBUG [coder-001] [STDIN] {"role": "user", "content": "Story: Add authentication..."}
DEBUG [coder-001] [STDOUT] {"type": "assistant", "content": "Let me explore the codebase..."}
DEBUG [coder-001] [STDOUT] {"type": "tool_call", "name": "Read", "input": {...}}
DEBUG [coder-001] [STDOUT] {"type": "tool_result", "content": "..."}
```

### Debugging Tools

**Debug Log File** (when `debug_mode: true`):

Save full stdin/stdout transcript to:
```
logs/claude-code/coder-001-session-abc123.log
```

**Debug Commands**:
```bash
# Tail Claude Code session logs
tail -f logs/claude-code/coder-001-session-*.log

# Replay session (for debugging)
cat logs/claude-code/coder-001-session-abc123.log | jq .

# Check bridge server traffic
journalctl -f | grep maestro-bridge

# Inspect agent state
sqlite3 maestro.db "SELECT * FROM agent_state WHERE agent_id='coder-001'"
```

## Testing Strategy

### Unit Tests

**1. Process Manager Tests** (`pkg/claudecode/manager_test.go`):
- Start/stop lifecycle
- Message send/receive
- Parse response formats
- Restart logic
- Error handling

**2. Bridge Server Tests** (`pkg/claudecode/bridge/server_test.go`):
- Tool registration
- Connection handling
- Request routing
- Response formatting
- Agent context tracking

**3. Handler Tests** (`pkg/claudecode/handler_test.go`):
- JSON parsing (various response types)
- Signal detection
- File change extraction
- Error cases (malformed JSON)

### Integration Tests

**4. End-to-End Bridge Tests** (`pkg/claudecode/bridge/integration_test.go`):
- Launch bridge server
- Connect bridge client
- Send maestro_ask_question
- Verify dispatcher receives QuestionEffect
- Return answer, verify client receives it

**5. Claude Code Process Tests** (`pkg/claudecode/integration_test.go`):
- Launch Claude Code in test container
- Send messages, receive responses
- Verify tool calls work
- Test restart logic
- Test timeout handling

### System Tests

**6. Full Story Tests** (`test/e2e/claude_code_test.go`):
- Complete AppDev story (PLANNING → DONE)
- Complete DevOps story (container modifications)
- Plan review cycle
- Code review cycle
- Question/answer interaction
- Error recovery (simulated crash)

**7. Comparison Tests** (`test/e2e/comparison_test.go`):
- Same story via classic vs Claude Code mode
- Compare: duration, tokens, cost, iterations
- Verify both produce working code
- Document performance differences

### Performance Tests

**8. Load Tests** (`test/performance/claude_code_load_test.go`):
- Multiple concurrent Claude Code coders
- Resource usage (memory, CPU, file descriptors)
- Bridge server capacity
- Process cleanup verification

**9. Stress Tests** (`test/performance/claude_code_stress_test.go`):
- Long-running sessions
- Large codebases
- Many file modifications
- Complex question/answer chains

## Implementation Plan

### Phase 1: Foundation (3-4 days)

**Goal**: Establish shared coder abstraction and basic Claude Code process management

**Tasks**:
1. **Refactor Coder Base** (1 day)
   - [ ] Extract `CoderBase` from current `Coder`
   - [ ] Define `PlanningHandler` and `CodingHandler` interfaces
   - [ ] Refactor current coder to `ClassicCoder` using interfaces
   - [ ] Verify all tests pass (no regressions)

2. **Claude Code Process Manager** (1.5 days)
   - [ ] Implement `pkg/claudecode/manager.go`
   - [ ] Start/stop lifecycle
   - [ ] Stdin/stdout stream-json communication
   - [ ] Basic response parsing
   - [ ] Unit tests for manager

3. **Basic Integration Test** (0.5 days)
   - [ ] Launch Claude Code in test container
   - [ ] Send message, receive response
   - [ ] Verify JSON parsing
   - [ ] Document any quirks/issues

**Deliverables**:
- ✅ `CoderBase` abstraction with strategy pattern
- ✅ `ClassicCoder` using new abstraction (no behavior changes)
- ✅ `Manager` can start/stop Claude Code and exchange messages
- ✅ All existing tests pass

**Success Criteria**:
- No regressions in classic coder
- Can launch Claude Code and exchange JSON messages
- Clear separation of concerns (shared vs divergent logic)

### Phase 2: MCP Bridge Server (3-4 days)

**Goal**: Enable Claude Code to invoke Maestro-specific operations

**Tasks**:
1. **Bridge Server Implementation** (2 days)
   - [ ] Implement `pkg/claudecode/bridge/server.go`
   - [ ] MCP protocol handler
   - [ ] Unix socket listener
   - [ ] Agent context tracking
   - [ ] Tool routing
   - [ ] Unit tests for server

2. **Bridge Tools** (1 day)
   - [ ] Implement `maestro_submit_plan`
   - [ ] Implement `maestro_ask_question` (with timeout)
   - [ ] Implement `maestro_done`
   - [ ] Implement `maestro_mark_story_complete`
   - [ ] Unit tests for each tool

3. **Bridge Client Binary** (0.5 days)
   - [ ] Implement `cmd/maestro-bridge-client/main.go`
   - [ ] Stdin/stdout MCP proxy
   - [ ] Socket communication
   - [ ] Agent ID injection
   - [ ] Build and install script

4. **Integration Tests** (0.5 days)
   - [ ] End-to-end bridge test (client → server → dispatcher)
   - [ ] Question/answer roundtrip
   - [ ] Timeout handling
   - [ ] Error cases

**Deliverables**:
- ✅ Bridge server running alongside orchestrator
- ✅ Bridge client binary installed in containers
- ✅ All maestro_* tools functional
- ✅ Questions reach architect and answers return

**Success Criteria**:
- Bridge server starts and accepts connections
- Claude Code can discover maestro_* tools via MCP
- Tool calls propagate correctly through dispatcher
- Timeout mechanism works (10 minute question timeout)

### Phase 3: Claude Code Coder Agent (4-5 days)

**Goal**: Complete Claude Code coder implementation with full state machine

**Tasks**:
1. **ClaudeCodeCoder Implementation** (2 days)
   - [ ] Implement `pkg/coder/claudecode.go`
   - [ ] `ExecutePlanning()` handler
   - [ ] `ExecuteCoding()` handler
   - [ ] Error handling with restart logic
   - [ ] Context management between phases
   - [ ] Unit tests

2. **System Prompt Templates** (1 day)
   - [ ] Create `pkg/templates/claudecode_planning.go`
   - [ ] Create `pkg/templates/claudecode_coding.go`
   - [ ] Adapt from classic templates
   - [ ] Include Maestro tool descriptions
   - [ ] Add git usage guidelines
   - [ ] Template rendering tests

3. **Response Handler** (1 day)
   - [ ] Implement `pkg/claudecode/handler.go`
   - [ ] Parse stream-json responses
   - [ ] Detect completion signals
   - [ ] Extract file changes from tool calls
   - [ ] Error handling
   - [ ] Unit tests

4. **Configuration Integration** (0.5 days)
   - [ ] Update `pkg/config/config.go`
   - [ ] Add `coder.mode` setting
   - [ ] Add `coder.claude_code.*` settings
   - [ ] Config validation
   - [ ] Factory selection based on mode

5. **Coder Factory** (0.5 days)
   - [ ] Update `pkg/coder/factory.go`
   - [ ] Select implementation based on config
   - [ ] Shared dependency injection
   - [ ] Error handling for missing Claude Code binary

**Deliverables**:
- ✅ Complete `ClaudeCodeCoder` implementation
- ✅ System prompt templates adapted from classic
- ✅ Response handler with signal detection
- ✅ Configuration-based mode selection

**Success Criteria**:
- Can complete PLANNING state end-to-end
- Plan submission works, transitions to PLAN_REVIEW
- Can complete CODING state end-to-end
- Implementation completion works, transitions to TESTING
- Questions to architect work during both phases

### Phase 4: Integration & Testing (3-4 days)

**Goal**: Comprehensive testing and metrics collection

**Tasks**:
1. **End-to-End Tests** (1.5 days)
   - [ ] Full AppDev story lifecycle test
   - [ ] Plan review cycle test
   - [ ] Code review cycle test
   - [ ] Question/answer interaction test
   - [ ] Budget review handling test
   - [ ] Error recovery test (simulated crash)

2. **Metrics Implementation** (1 day)
   - [ ] Add performance metrics
   - [ ] Add reliability metrics
   - [ ] Add cost tracking
   - [ ] Comparison metrics (classic vs Claude Code)
   - [ ] Metric collection points in code

3. **Debug Logging** (0.5 days)
   - [ ] Implement debug mode logging
   - [ ] Session transcript files
   - [ ] Bridge traffic logging
   - [ ] Log rotation

4. **Error Scenario Tests** (1 day)
   - [ ] Process crash recovery
   - [ ] Bridge disconnection
   - [ ] Timeout handling
   - [ ] Malformed responses
   - [ ] Second failure (ERROR state)

**Deliverables**:
- ✅ Complete test coverage for all scenarios
- ✅ Metrics collection and reporting
- ✅ Debug mode for troubleshooting
- ✅ All error cases handled gracefully

**Success Criteria**:
- All end-to-end tests pass
- Can complete real stories successfully
- Metrics collected and comparable to classic mode
- No resource leaks or orphaned processes
- Clear error messages for debugging

### Phase 5: DevOps Support & Polish (2-3 days)

**Goal**: Support DevOps stories and production readiness

**Tasks**:
1. **DevOps Story Support** (1 day)
   - [ ] Adapt DevOps prompts for Claude Code
   - [ ] Test container operations
   - [ ] Test Dockerfile modifications
   - [ ] Integration test for DevOps story

2. **Bootstrap Container Integration** (0.5 days)
   - [ ] Add Claude Code to bootstrap Dockerfile
   - [ ] Verify installation in CI
   - [ ] Test bootstrap story handling
   - [ ] Auto-install for user containers

3. **Documentation** (1 day)
   - [ ] Configuration guide
   - [ ] Troubleshooting guide
   - [ ] Performance comparison guide
   - [ ] Migration guide (classic → Claude Code)
   - [ ] Update CLAUDE.md with Claude Code info

4. **Optimization** (0.5 days)
   - [ ] Prompt tuning based on test results
   - [ ] Process startup optimization
   - [ ] Memory usage profiling
   - [ ] Token usage analysis

**Deliverables**:
- ✅ DevOps stories work correctly
- ✅ Bootstrap container includes Claude Code
- ✅ Complete documentation
- ✅ Performance optimization

**Success Criteria**:
- DevOps stories complete successfully
- Bootstrap stories use Claude Code (when configured)
- Documentation covers all use cases
- Performance acceptable compared to classic mode
- Ready for production use

## Timeline Summary

| Phase | Duration | Deliverables |
|-------|----------|--------------|
| Phase 1: Foundation | 3-4 days | Coder abstraction, process manager, basic communication |
| Phase 2: Bridge Server | 3-4 days | MCP bridge, tools, client binary |
| Phase 3: Agent Implementation | 4-5 days | ClaudeCodeCoder, templates, handlers |
| Phase 4: Testing | 3-4 days | E2E tests, metrics, error handling |
| Phase 5: Polish | 2-3 days | DevOps support, docs, optimization |
| **Total** | **15-20 days** | **Production-ready Claude Code integration** |

## Risk Assessment

### Technical Risks

**Risk 1: Claude Code Process Stability**
- **Impact**: High - Core dependency
- **Probability**: Low - Claude Code is mature
- **Mitigation**:
  - Comprehensive error handling with restart
  - Fallback to ERROR state after one retry
  - Extensive logging for debugging
  - Process health monitoring

**Risk 2: Bridge Server Complexity**
- **Impact**: Medium - Debugging challenges
- **Probability**: Medium - New protocol integration
- **Mitigation**:
  - Extensive logging at all bridge interactions
  - Debug mode dumps full MCP traffic
  - Unit tests for all bridge tools
  - Integration tests for end-to-end flow

**Risk 3: Context Synchronization**
- **Impact**: Medium - Plan not properly passed to coding
- **Probability**: Low - Straightforward state management
- **Mitigation**:
  - Explicit state data tests
  - Validation of plan/context at phase boundaries
  - Debug logging of context injection

**Risk 4: Process Lifecycle Issues**
- **Impact**: High - Orphaned processes, resource leaks
- **Probability**: Medium - Docker + process management is complex
- **Mitigation**:
  - Defer blocks for cleanup
  - Process registry with health checks
  - Integration tests for crash scenarios
  - Orchestrator-level monitoring

**Risk 5: Cost Overruns**
- **Impact**: Medium - Claude Code may use more tokens
- **Probability**: Low - Expected to be more efficient
- **Mitigation**:
  - Track token usage and cost metrics
  - Compare to classic mode baseline
  - Set budget alerts
  - Monitor and optimize prompts

### Operational Risks

**Risk 6: Installation Issues**
- **Impact**: Medium - Users can't use Claude Code mode
- **Probability**: Low - NPM install is standard
- **Mitigation**:
  - Auto-install with clear error messages
  - Bundled in bootstrap container
  - Version compatibility checks
  - Fallback to classic mode

**Risk 7: Performance Degradation**
- **Impact**: Medium - Slower than expected
- **Probability**: Low - Should be faster
- **Mitigation**:
  - Performance benchmarks vs classic
  - Profiling and optimization
  - Adjustable timeouts
  - Clear metrics for comparison

## Success Criteria

**Functional Requirements**:
- ✅ Can complete AppDev stories end-to-end in Claude Code mode
- ✅ Can complete DevOps stories end-to-end in Claude Code mode
- ✅ All orchestration features work (plan review, code review, questions)
- ✅ Configuration-based mode selection (classic vs Claude Code)
- ✅ Bootstrap container includes Claude Code

**Quality Requirements**:
- ✅ No regressions in classic mode
- ✅ Test coverage >80% for new code
- ✅ All error scenarios handled gracefully
- ✅ No resource leaks or orphaned processes
- ✅ Clear error messages for debugging

**Performance Requirements**:
- ✅ Token usage tracked and compared to classic mode
- ✅ Latency tracked and compared to classic mode
- ✅ Dollar cost calculated and compared to classic mode
- ✅ Performance within acceptable range (no hard limits)

**Operational Requirements**:
- ✅ Simple configuration to enable Claude Code mode
- ✅ Comprehensive documentation (config, troubleshooting, migration)
- ✅ Debug mode for troubleshooting
- ✅ Metrics for monitoring and comparison

## Future Enhancements

**Post-MVP Improvements**:

1. **Session Persistence**: Save Claude Code session data to disk for recovery after orchestrator restart
2. **Advanced Context Management**: Smarter context preservation across container restarts
3. **Tool Filtering**: Granular control over which Claude Code tools are allowed
4. **Cost Optimization**: Prompt tuning, caching strategies, token reduction
5. **Multi-Model Support**: Support other AI coding assistants (Cursor, Copilot, etc.)
6. **Interactive Mode**: Human-in-the-loop mode for debugging stories
7. **Parallel Planning**: Use Claude Code for exploration while architect plans architecture
8. **Custom Tool Injection**: Allow users to define custom MCP tools for domain-specific operations

**Research Areas**:

1. **Hybrid Approach**: Use Claude Code for coding, classic mode for planning (or vice versa)
2. **Multi-Agent Claude Code**: Multiple Claude Code instances collaborating on same story
3. **Streaming Optimizations**: Reduce latency by processing partial responses
4. **Smart Restart**: Checkpoint state for faster restart without losing context
5. **Rate Limiting**: Coordinate rate limiting across multiple Claude Code instances

## Appendix A: Constants and Configuration

```go
// pkg/claudecode/constants.go

const (
    // QuestionTimeout is the maximum time to wait for architect answer
    QuestionTimeout = 10 * time.Minute

    // ProcessStartTimeout is the maximum time to start Claude Code process
    ProcessStartTimeout = 30 * time.Second

    // ResponseTimeout is the maximum time to wait for a response
    ResponseTimeout = 5 * time.Minute

    // RestartTimeout is the maximum time for graceful restart
    RestartTimeout = 30 * time.Second

    // ShutdownTimeout is the maximum time for graceful shutdown
    ShutdownTimeout = 10 * time.Second

    // DefaultSessionTimeout is the default maximum duration for a session
    DefaultSessionTimeout = 30 * time.Minute

    // BridgeSocketPath is the default Unix socket path
    DefaultBridgeSocketPath = "/tmp/maestro-bridge.sock"

    // ClaudeBinaryPath is the default path to claude binary
    DefaultClaudeBinaryPath = "claude"
)
```

## Appendix B: MCP Protocol Reference

**Tool Discovery Request**:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list"
}
```

**Tool Discovery Response**:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "tools": [
      {
        "name": "maestro_submit_plan",
        "description": "Submit implementation plan",
        "inputSchema": {
          "type": "object",
          "properties": {
            "plan": {"type": "string"}
          },
          "required": ["plan"]
        }
      }
    ]
  }
}
```

**Tool Call Request**:
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "maestro_ask_question",
    "arguments": {
      "question": "Should I use async?",
      "context": "Working on API client",
      "urgency": "high"
    }
  }
}
```

**Tool Call Response**:
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Yes, use async for better performance..."
      }
    ]
  }
}
```

## Appendix C: Debugging Checklist

When troubleshooting Claude Code issues:

1. **Check Configuration**
   - [ ] `coder.mode` set to "claude-code"
   - [ ] `coder.claude_code.enabled` is true
   - [ ] Claude binary path correct

2. **Verify Installation**
   - [ ] `claude --version` works in container
   - [ ] Bridge client binary installed
   - [ ] Bridge server running

3. **Check Process State**
   - [ ] Claude Code process running (`ps aux | grep claude`)
   - [ ] No orphaned processes
   - [ ] Container still running

4. **Review Logs**
   - [ ] Check orchestrator logs for errors
   - [ ] Check Claude Code session logs (if debug mode)
   - [ ] Check bridge server logs
   - [ ] Check agent state in database

5. **Test Bridge**
   - [ ] Bridge server reachable (`nc -U /tmp/maestro-bridge.sock`)
   - [ ] Tools discoverable (check MCP response)
   - [ ] Tool calls reach dispatcher

6. **Validate Context**
   - [ ] Plan stored in state data
   - [ ] Story content passed correctly
   - [ ] System prompt rendered properly

7. **Performance Check**
   - [ ] Token usage reasonable
   - [ ] Response times acceptable
   - [ ] No memory leaks
   - [ ] File descriptors not leaking

---

**Document Maintenance**:
This specification should be updated as implementation progresses. Mark tasks complete with dates, document deviations from plan, and record lessons learned for future enhancements.
