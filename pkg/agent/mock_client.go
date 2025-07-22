package agent

import (
	"context"
	"fmt"
)

// MockLLMClient provides a controllable implementation of LLMClient for testing.
type MockLLMClient struct {
	responses     []CompletionResponse
	responseIndex int
	errors        []error
	errorIndex    int
}

// NewMockLLMClient creates a new mock client with predefined responses.
func NewMockLLMClient(responses []CompletionResponse, errors []error) *MockLLMClient {
	return &MockLLMClient{
		responses: responses,
		errors:    errors,
	}
}

// Complete returns the next predefined response or error.
func (m *MockLLMClient) Complete(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
	if m.errorIndex < len(m.errors) && m.errors[m.errorIndex] != nil {
		err := m.errors[m.errorIndex]
		m.errorIndex++
		return CompletionResponse{}, err
	}

	if m.responseIndex >= len(m.responses) {
		return CompletionResponse{}, fmt.Errorf("mock client: no more responses")
	}

	resp := m.responses[m.responseIndex]
	m.responseIndex++
	return resp, nil
}

// Stream returns a channel that will receive predefined responses.
func (m *MockLLMClient) Stream(_ context.Context, _ CompletionRequest) (<-chan StreamChunk, error) {
	if m.errorIndex < len(m.errors) && m.errors[m.errorIndex] != nil {
		err := m.errors[m.errorIndex]
		m.errorIndex++
		return nil, err
	}

	if m.responseIndex >= len(m.responses) {
		return nil, fmt.Errorf("mock client: no more responses")
	}

	resp := m.responses[m.responseIndex]
	m.responseIndex++

	ch := make(chan StreamChunk)
	go func() {
		defer close(ch)
		ch <- StreamChunk{
			Content: resp.Content,
			Done:    true,
		}
	}()

	return ch, nil
}
