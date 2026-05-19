package validation

import (
	"context"
	"errors"
	"testing"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
)

// scriptedClient returns queued responses in order; the last is repeated.
type scriptedClient struct {
	resps []llm.CompletionResponse
	calls int
}

//nolint:gocritic // test stub; signature fixed by llm.LLMClient
func (s *scriptedClient) Complete(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	r := s.resps[min(s.calls, len(s.resps)-1)]
	s.calls++
	return r, nil
}

//nolint:gocritic // test stub
func (s *scriptedClient) Stream(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("stream not used in these tests")
}
func (s *scriptedClient) GetModelName() string { return "stub" }

// TestWrap_CoderEmptyThenError: a coder response with content but no tool
// calls is "empty"; after guidance retry still empty → ErrorTypeEmptyResponse.
func TestWrap_CoderEmptyThenError(t *testing.T) {
	stub := &scriptedClient{resps: []llm.CompletionResponse{
		{Content: "I will use the shell tool", StopReason: "end_turn"}, // no tool calls
	}}
	c := NewEmptyResponseValidator(AgentTypeCoder).Wrap(stub)

	_, err := c.Complete(context.Background(), llm.CompletionRequest{})
	if !llmerrors.Is(err, llmerrors.ErrorTypeEmptyResponse) {
		t.Fatalf("expected ErrorTypeEmptyResponse, got %v", err)
	}
	if stub.calls != 2 { // original + 1 guidance retry
		t.Fatalf("expected 2 attempts (original + guidance), got %d", stub.calls)
	}
}

// TestWrap_CoderToolCallPasses: a response with a tool call is valid for a
// coder and returns immediately with no error.
func TestWrap_CoderToolCallPasses(t *testing.T) {
	stub := &scriptedClient{resps: []llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{ID: "1", Name: "shell"}}, StopReason: "tool_use"},
	}}
	c := NewEmptyResponseValidator(AgentTypeCoder).Wrap(stub)

	resp, err := c.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil || len(resp.ToolCalls) != 1 || stub.calls != 1 {
		t.Fatalf("expected immediate valid response, got resp=%+v err=%v calls=%d", resp, err, stub.calls)
	}
}

// TestWrap_ArchitectTextPasses: plain text is valid for an architect.
func TestWrap_ArchitectTextPasses(t *testing.T) {
	stub := &scriptedClient{resps: []llm.CompletionResponse{
		{Content: "Here is my analysis and decision.", StopReason: "end_turn"},
	}}
	c := NewEmptyResponseValidator(AgentTypeArchitect).Wrap(stub)

	resp, err := c.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil || resp.Content == "" || stub.calls != 1 {
		t.Fatalf("architect text should pass: resp=%+v err=%v calls=%d", resp, err, stub.calls)
	}
}

// TestWrap_PauseTurnResumes: a pause_turn response is resumed automatically,
// then a valid response is returned.
func TestWrap_PauseTurnResumes(t *testing.T) {
	stub := &scriptedClient{resps: []llm.CompletionResponse{
		{Content: "partial", StopReason: "pause_turn"},
		{ToolCalls: []llm.ToolCall{{ID: "1", Name: "shell"}}, StopReason: "tool_use"},
	}}
	c := NewEmptyResponseValidator(AgentTypeCoder).Wrap(stub)

	resp, err := c.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil || len(resp.ToolCalls) != 1 {
		t.Fatalf("pause_turn should resume to a valid response: resp=%+v err=%v", resp, err)
	}
	if stub.calls < 2 {
		t.Fatalf("expected resume call after pause_turn, calls=%d", stub.calls)
	}
}
