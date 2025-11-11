package agent

import (
	"fmt"

	"orchestrator/pkg/agent/internal/core"
	"orchestrator/pkg/agent/internal/llmimpl/anthropic"
	"orchestrator/pkg/agent/internal/llmimpl/openaiofficial"
	"orchestrator/pkg/agent/internal/runtime"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/msg"
	"orchestrator/pkg/proto"
)

// Re-export essential types from internal packages for public use.
type (
	// BaseStateMachine provides the core state machine functionality.
	BaseStateMachine = core.BaseStateMachine

	// Config represents agent runtime configuration.
	Config = runtime.Config

	// LLMClient represents the interface for LLM interactions.
	LLMClient = llm.LLMClient

	// CompletionRequest represents a request for LLM completion.
	CompletionRequest = llm.CompletionRequest

	// CompletionResponse represents a response from LLM completion.
	CompletionResponse = llm.CompletionResponse

	// CompletionMessage represents a message in completion conversation.
	CompletionMessage = llm.CompletionMessage

	// CompletionRole represents the role of a message in completion conversation.
	CompletionRole = llm.CompletionRole

	// CacheControl represents prompt caching configuration for a message.
	CacheControl = llm.CacheControl

	// StreamChunk represents a streaming response chunk.
	StreamChunk = llm.StreamChunk

	// ToolCall represents a tool call from the LLM.
	ToolCall = llm.ToolCall

	// ToolResult represents a tool execution result.
	ToolResult = llm.ToolResult

	// Context provides runtime context for agents.
	Context = runtime.Context

	// LLMConfig represents configuration for LLM clients.
	LLMConfig = llm.LLMConfig

	// StateStore represents state persistence interface.
	StateStore = core.StateStore

	// TransitionTable represents state machine transition rules.
	TransitionTable = core.TransitionTable
)

// Re-export essential constants.
const (
	ArchitectMaxTokens = llm.ArchitectMaxTokens
	RoleUser           = llm.RoleUser
	RoleAssistant      = llm.RoleAssistant
	RoleSystem         = llm.RoleSystem
)

// GetTyped retrieves a typed value from the state machine.
func GetTyped[T any](sm *BaseStateMachine, key string) (T, bool) {
	return core.GetTyped[T](sm, key)
}

// SetTyped stores a typed value in the state machine.
func SetTyped[T any](sm *BaseStateMachine, key string, value T) {
	core.SetTyped[T](sm, key, value)
}

// ValidateAndSanitizeMessages validates and sanitizes completion messages.
func ValidateAndSanitizeMessages(messages []CompletionMessage) ([]CompletionMessage, error) {
	// First sanitize all messages
	sanitized := make([]CompletionMessage, len(messages))
	for i := range messages {
		sanitized[i] = msg.SanitizeMessage(&messages[i])
	}

	// Then validate the sanitized messages
	if err := msg.ValidateMessages(sanitized); err != nil {
		return nil, fmt.Errorf("message validation failed: %w", err)
	}
	return sanitized, nil
}

// NewBaseStateMachine creates a new base state machine.
func NewBaseStateMachine(agentID string, initialState proto.State, store core.StateStore, table core.TransitionTable) *BaseStateMachine {
	return core.NewBaseStateMachine(agentID, initialState, store, table)
}

// NewClaudeClient creates a new Claude client.
func NewClaudeClient(apiKey string) LLMClient {
	return anthropic.NewClaudeClient(apiKey)
}

// NewO3ClientWithModel creates a new OpenAI client with specific model.
// Uses official OpenAI SDK with Responses API.
func NewO3ClientWithModel(apiKey, model string) LLMClient {
	return openaiofficial.NewOfficialClientWithModel(apiKey, model)
}

// NewConfig creates a new agent configuration.
func NewConfig(id, agentType string, ctx Context) *Config {
	return runtime.NewConfig(id, agentType, ctx)
}

// ShutdownManager provides test stub for shutdown management - these should be migrated away from over time.
type ShutdownManager struct{}

// NewShutdownManager creates a new shutdown manager test stub.
func NewShutdownManager() *ShutdownManager {
	return &ShutdownManager{}
}

// Shutdown performs test stub shutdown operation.
func (sm *ShutdownManager) Shutdown(_ Context) error {
	return nil
}

// IsShuttingDown returns test stub shutdown status.
func (sm *ShutdownManager) IsShuttingDown() bool {
	return true
}

// ShutdownContext returns test stub shutdown context.
func (sm *ShutdownManager) ShutdownContext() Context {
	return Context{}
}

// NewShutdownableDriver creates a legacy test stub driver - to be removed after test migration.
func NewShutdownableDriver(cfg *Config, state proto.State, _ *ShutdownManager) (*BaseDriver, error) {
	return NewBaseDriver(cfg, state)
}

// NewBaseDriver creates a legacy test stub base driver.
func NewBaseDriver(_ *Config, _ proto.State) (*BaseDriver, error) {
	// Minimal stub for tests
	return &BaseDriver{}, nil
}

// BaseDriver provides legacy test stub driver functionality.
type BaseDriver struct{}

// Initialize performs legacy test stub initialization.
func (bd *BaseDriver) Initialize(_ Context) error { return nil }

// GetCurrentState returns legacy test stub current state.
func (bd *BaseDriver) GetCurrentState() proto.State { return proto.StateWaiting }

// GetStateData returns legacy test stub state data.
func (bd *BaseDriver) GetStateData() map[string]any { return make(map[string]any) }

// ValidateMessage provides legacy test stub message validation.
func ValidateMessage(message *CompletionMessage) error {
	if err := msg.ValidateMessage(message); err != nil {
		return fmt.Errorf("validation error: %w", err)
	}
	return nil
}

// ValidateMessages provides legacy test stub messages validation.
func ValidateMessages(messages []CompletionMessage) error {
	if err := msg.ValidateMessages(messages); err != nil {
		return fmt.Errorf("validation error: %w", err)
	}
	return nil
}

// SanitizeMessage provides legacy test stub message sanitization.
func SanitizeMessage(message *CompletionMessage) CompletionMessage {
	return msg.SanitizeMessage(message)
}

// PromptLogConfig provides legacy test stub prompt log configuration.
type PromptLogConfig struct {
	Mode        string
	MaxChars    int
	IncludeHash bool
}

// PromptLogOnFailure provides legacy test stub prompt log mode.
const PromptLogOnFailure = "on_failure"

// NewClaudeClientWithLogger provides legacy test stub Claude client with logger.
func NewClaudeClientWithLogger(apiKey string, _ interface{}) LLMClient {
	return NewClaudeClient(apiKey)
}

// NewClaudeClientWithModel provides legacy test stub Claude client with model.
func NewClaudeClientWithModel(apiKey, _ string) LLMClient {
	return NewClaudeClient(apiKey)
}

// NewClaudeClientWithModelAndLogger provides legacy test stub Claude client with model and logger.
func NewClaudeClientWithModelAndLogger(apiKey, _ string, _ interface{}) LLMClient {
	return NewClaudeClient(apiKey)
}

// NewO3Client provides legacy test stub OpenAI client.
// Uses official OpenAI SDK with Responses API.
func NewO3Client(apiKey string) LLMClient {
	return NewO3ClientWithModel(apiKey, "o1-preview")
}

// NewCompletionRequest provides completion request creation helper.
func NewCompletionRequest(messages []CompletionMessage) CompletionRequest {
	return llm.NewCompletionRequest(messages)
}

// NewSystemMessage provides system message creation helper.
func NewSystemMessage(content string) CompletionMessage {
	return llm.NewSystemMessage(content)
}

// NewUserMessage provides user message creation helper.
func NewUserMessage(content string) CompletionMessage {
	return llm.NewUserMessage(content)
}
