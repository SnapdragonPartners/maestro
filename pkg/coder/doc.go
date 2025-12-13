// Package coder implements the coding agent for the maestro orchestrator.
//
// # Architecture
//
// The coder is a state machine that processes development stories through a series
// of states: WAITING -> SETUP -> PLANNING -> PLAN_REVIEW -> CODING -> TESTING ->
// CODE_REVIEW -> PREPARE_MERGE -> AWAIT_MERGE -> DONE.
//
// # Dual Execution Modes
//
// The coder supports two distinct execution modes for the PLANNING and CODING states:
//
// ## Standard Mode
//
// In standard mode, the coder uses the built-in toolloop system (pkg/agent/toolloop/)
// to orchestrate LLM interactions:
//
//   - State handlers in planning.go and coding.go run the toolloop
//   - LLM makes tool calls via MCP tools registered with the ToolProvider
//   - Iteration limits and escalation managed by toolloop's EscalationConfig
//   - Context management via pkg/contextmgr
//
// ## Claude Code Mode
//
// In Claude Code mode, the coder delegates planning and coding to Claude Code
// running as a subprocess inside the container:
//
//   - State handlers in claudecode_planning.go and claudecode_coding.go manage the subprocess
//   - MCP server (pkg/coder/claude/mcpserver/) exposes maestro tools to Claude Code
//   - Session management allows pause/resume across state transitions
//   - Claude Code handles its own context and tool calling
//
// ## Mode Selection
//
// Mode selection is determined by isClaudeCodeMode() which checks:
//
//  1. Configuration: config.Agents.CoderMode must equal "claude_code"
//  2. Availability: "claude --version" must succeed in the container
//
// If Claude Code mode is configured but Claude Code is not available in the container,
// the coder falls back to standard mode with a warning.
//
// # State Data Keys
//
// The coder uses typed constants for state data keys (see driver.go). Important keys:
//
//   - KeyStoryID: Current story being processed
//   - KeyPlan: Approved implementation plan
//   - KeyCodingSessionID: Claude Code session ID for resume
//   - KeyResumeInput: Feedback to inject when resuming a session
//
// # Effects Pattern
//
// External interactions (approvals, questions, merges) use the Effects pattern
// (pkg/effect/) which provides a clean abstraction for blocking operations that
// require responses from other agents.
//
// # Container Management
//
// The coder manages its own container lifecycle:
//
//   - SETUP state initializes the container (target or fallback to safe container)
//   - Container tools (container_build, container_switch, etc.) available during coding
//   - container_switch is disabled in Claude Code mode to prevent session destruction
//   - Workspace is bind-mounted; inode preservation is critical for Docker Desktop
package coder
