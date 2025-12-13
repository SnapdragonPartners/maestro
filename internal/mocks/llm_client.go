package mocks

import (
	"context"
	"sync"

	"orchestrator/pkg/agent/llm"
)

// MockLLMClient implements llm.LLMClient for testing.
// It provides configurable behavior for Complete and Stream operations.
//
//nolint:govet // fieldalignment: mock struct layout optimized for readability
type MockLLMClient struct {
	// CompleteFunc is called when Complete is invoked. Override to customize behavior.
	CompleteFunc func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error)

	// StreamFunc is called when Stream is invoked. Override to customize behavior.
	StreamFunc func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error)

	// CompleteCalls tracks all calls to Complete for verification.
	CompleteCalls []llm.CompletionRequest

	// StreamCalls tracks all calls to Stream for verification.
	StreamCalls []llm.CompletionRequest

	// modelName is the model name returned by GetModelName.
	modelName string

	// mu protects call tracking slices
	mu sync.Mutex
}

// NewMockLLMClient creates a new mock LLM client with default behavior.
// Default behavior: Complete returns an empty response, Stream returns an empty channel.
func NewMockLLMClient() *MockLLMClient {
	m := &MockLLMClient{
		modelName: "mock-model",
	}

	// Default Complete behavior: return empty response
	m.CompleteFunc = func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
		return llm.CompletionResponse{
			Content:    "Mock response",
			StopReason: "end_turn",
		}, nil
	}

	// Default Stream behavior: return a channel that immediately closes
	m.StreamFunc = func(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
		ch := make(chan llm.StreamChunk, 1)
		ch <- llm.StreamChunk{Content: "Mock streamed response", Done: true}
		close(ch)
		return ch, nil
	}

	return m
}

// Complete implements llm.LLMClient.
func (m *MockLLMClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	m.mu.Lock()
	m.CompleteCalls = append(m.CompleteCalls, req)
	m.mu.Unlock()
	return m.CompleteFunc(ctx, req)
}

// Stream implements llm.LLMClient.
func (m *MockLLMClient) Stream(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	m.mu.Lock()
	m.StreamCalls = append(m.StreamCalls, req)
	m.mu.Unlock()
	return m.StreamFunc(ctx, req)
}

// GetModelName implements llm.LLMClient.
func (m *MockLLMClient) GetModelName() string {
	return m.modelName
}

// --- Configuration methods ---

// SetModelName sets the model name returned by GetModelName.
func (m *MockLLMClient) SetModelName(name string) {
	m.modelName = name
}

// OnComplete sets a custom handler for Complete calls.
func (m *MockLLMClient) OnComplete(fn func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error)) {
	m.CompleteFunc = fn
}

// OnStream sets a custom handler for Stream calls.
func (m *MockLLMClient) OnStream(fn func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error)) {
	m.StreamFunc = fn
}

// --- Error simulation helpers ---

// FailCompleteWith configures Complete to return the specified error.
func (m *MockLLMClient) FailCompleteWith(err error) {
	m.CompleteFunc = func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
		return llm.CompletionResponse{}, err
	}
}

// FailStreamWith configures Stream to return the specified error.
func (m *MockLLMClient) FailStreamWith(err error) {
	m.StreamFunc = func(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
		return nil, err
	}
}

// --- Response helpers ---

// RespondWith configures Complete to return the specified content.
func (m *MockLLMClient) RespondWith(content string) {
	m.CompleteFunc = func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
		return llm.CompletionResponse{
			Content:    content,
			StopReason: "end_turn",
		}, nil
	}
}

// RespondWithToolCall configures Complete to return a tool call response.
func (m *MockLLMClient) RespondWithToolCall(toolName string, params map[string]any) {
	m.CompleteFunc = func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
		return llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:         "mock-tool-call-1",
					Name:       toolName,
					Parameters: params,
				},
			},
			StopReason: "tool_use",
		}, nil
	}
}

// RespondWithToolCalls configures Complete to return multiple tool call responses.
func (m *MockLLMClient) RespondWithToolCalls(calls []llm.ToolCall) {
	m.CompleteFunc = func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
		return llm.CompletionResponse{
			ToolCalls:  calls,
			StopReason: "tool_use",
		}, nil
	}
}

// RespondWithSequence configures Complete to return different responses for each call.
// Cycles through the responses in order, returning the last one for any additional calls.
func (m *MockLLMClient) RespondWithSequence(responses []llm.CompletionResponse) {
	callIndex := 0
	m.CompleteFunc = func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
		if callIndex < len(responses) {
			resp := responses[callIndex]
			callIndex++
			return resp, nil
		}
		// Return last response for any additional calls
		return responses[len(responses)-1], nil
	}
}

// --- Streaming helpers ---

// StreamContent configures Stream to return the content in chunks.
func (m *MockLLMClient) StreamContent(content string, chunkSize int) {
	m.StreamFunc = func(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
		ch := make(chan llm.StreamChunk)
		go func() {
			defer close(ch)
			for i := 0; i < len(content); i += chunkSize {
				end := i + chunkSize
				if end > len(content) {
					end = len(content)
				}
				ch <- llm.StreamChunk{Content: content[i:end]}
			}
			ch <- llm.StreamChunk{Done: true}
		}()
		return ch, nil
	}
}

// StreamWithError configures Stream to return an error after streaming some content.
func (m *MockLLMClient) StreamWithError(content string, err error) {
	m.StreamFunc = func(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
		ch := make(chan llm.StreamChunk)
		go func() {
			defer close(ch)
			ch <- llm.StreamChunk{Content: content}
			ch <- llm.StreamChunk{Error: err}
		}()
		return ch, nil
	}
}

// --- Verification helpers ---

// Reset clears all recorded calls.
func (m *MockLLMClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CompleteCalls = nil
	m.StreamCalls = nil
}

// GetCompleteCallCount returns the number of times Complete was called.
func (m *MockLLMClient) GetCompleteCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.CompleteCalls)
}

// GetStreamCallCount returns the number of times Stream was called.
func (m *MockLLMClient) GetStreamCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.StreamCalls)
}

// LastCompleteCall returns the most recent Complete call request, or nil if none.
func (m *MockLLMClient) LastCompleteCall() *llm.CompletionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.CompleteCalls) == 0 {
		return nil
	}
	return &m.CompleteCalls[len(m.CompleteCalls)-1]
}

// LastCompleteCallMessages returns the messages from the most recent Complete call.
func (m *MockLLMClient) LastCompleteCallMessages() []llm.CompletionMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.CompleteCalls) == 0 {
		return nil
	}
	return m.CompleteCalls[len(m.CompleteCalls)-1].Messages
}

// AssertCompleteCalledWith verifies that Complete was called with messages containing the expected content.
func (m *MockLLMClient) AssertCompleteCalledWith(expectedContentSubstr string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, call := range m.CompleteCalls {
		for _, msg := range call.Messages {
			if containsSubstring(msg.Content, expectedContentSubstr) {
				return true
			}
		}
	}
	return false
}

// GetNthCompleteCall returns the nth Complete call (0-indexed), or nil if not enough calls.
func (m *MockLLMClient) GetNthCompleteCall(n int) *llm.CompletionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 0 || n >= len(m.CompleteCalls) {
		return nil
	}
	return &m.CompleteCalls[n]
}
