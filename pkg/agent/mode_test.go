package agent

import (
	"context"
	"testing"
)

func TestSystemMode(t *testing.T) {
	// Reset SystemMode after each test
	defer resetMode()

	t.Run("InitMode sets mode", func(t *testing.T) {
		InitMode(ModeMock)
		if SystemMode != ModeMock {
			t.Errorf("expected mode MOCK, got %s", SystemMode)
		}
	})

	t.Run("Mode strings", func(t *testing.T) {
		tests := []struct {
			mode Mode
			want string
		}{
			{ModeLive, "LIVE"},
			{ModeDebug, "DEBUG"},
			{ModeMock, "MOCK"},
			{Mode(999), "UNKNOWN"},
		}

		for _, tt := range tests {
			if got := tt.mode.String(); got != tt.want {
				t.Errorf("Mode(%d).String() = %v, want %v", tt.mode, got, tt.want)
			}
		}
	})
}

func TestClaudeClientModes(t *testing.T) {
	// Reset SystemMode before each subtest
	defer resetMode()

	t.Run("Returns mock client in mock mode", func(t *testing.T) {
		resetMode()
		InitMode(ModeMock)
		client := NewClaudeClient("test-key")
		if _, ok := client.(*MockLLMClient); !ok {
			t.Error("expected MockLLMClient in mock mode")
		}
	})

	t.Run("Returns real client in live mode", func(t *testing.T) {
		resetMode()
		InitMode(ModeLive)
		client := NewClaudeClient("test-key")
		// In live mode, we get a resilient client that's not a mock
		if _, ok := client.(*MockLLMClient); ok {
			t.Error("expected non-mock client in live mode")
		}
		// Verify it implements LLMClient interface
		if client == nil {
			t.Error("expected non-nil client")
		}
	})
}

func TestClaudeClientResponses(t *testing.T) {
	// Reset SystemMode and use mock mode for tests
	defer resetMode()
	InitMode(ModeMock)

	responses := []CompletionResponse{
		{Content: "response1"},
		{Content: "response2"},
	}
	client := NewMockLLMClient(responses, nil)

	ctx := context.Background()
	req := CompletionRequest{
		Messages: []CompletionMessage{
			{Role: "user", Content: "test"},
		},
	}

	// Test Complete
	resp, err := client.Complete(ctx, req)
	if err != nil {
		t.Errorf("Complete() error = %v", err)
	}
	if resp.Content != "response1" {
		t.Errorf("Complete() = %v, want %v", resp.Content, "response1")
	}

	// Test Stream
	ch, err := client.Stream(ctx, req)
	if err != nil {
		t.Errorf("Stream() error = %v", err)
	}

	chunk := <-ch
	if chunk.Content != "response2" {
		t.Errorf("Stream() chunk = %v, want %v", chunk.Content, "response2")
	}
	if !chunk.Done {
		t.Error("expected Done to be true")
	}
}
