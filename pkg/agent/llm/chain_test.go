package llm

import (
	"context"
	"fmt"
	"testing"
)

// TestWrapClient tests the WrapClient helper function.
func TestWrapClient(t *testing.T) {
	completeCalled := false
	streamCalled := false
	modelNameCalled := false

	client := WrapClient(
		func(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
			completeCalled = true
			return CompletionResponse{Content: "wrapped"}, nil
		},
		func(_ context.Context, _ CompletionRequest) (<-chan StreamChunk, error) {
			streamCalled = true
			ch := make(chan StreamChunk)
			close(ch)
			return ch, nil
		},
		func() string {
			modelNameCalled = true
			return "wrapped-model"
		},
	)

	ctx := context.Background()
	req := NewCompletionRequest([]CompletionMessage{NewUserMessage("test")})

	// Test Complete
	resp, err := client.Complete(ctx, req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !completeCalled {
		t.Error("Complete function was not called")
	}
	if resp.Content != "wrapped" {
		t.Errorf("expected 'wrapped', got %q", resp.Content)
	}

	// Test Stream
	_, err = client.Stream(ctx, req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !streamCalled {
		t.Error("Stream function was not called")
	}

	// Test GetModelName
	modelName := client.GetModelName()
	if !modelNameCalled {
		t.Error("GetModelName function was not called")
	}
	if modelName != "wrapped-model" {
		t.Errorf("expected 'wrapped-model', got %q", modelName)
	}
}

// TestChainSingleMiddleware tests chaining with a single middleware.
func TestChainSingleMiddleware(t *testing.T) {
	base := &mockLLMClient{
		completeFunc: func(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
			return CompletionResponse{Content: "base"}, nil
		},
		getModelNameFunc: func() string {
			return "base-model"
		},
	}

	// Middleware that adds a prefix
	prefixMiddleware := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				resp, err := next.Complete(ctx, req)
				if err != nil {
					return resp, err
				}
				resp.Content = "prefix:" + resp.Content
				return resp, nil
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	client := Chain(base, prefixMiddleware)

	ctx := context.Background()
	req := NewCompletionRequest([]CompletionMessage{NewUserMessage("test")})
	resp, err := client.Complete(ctx, req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if resp.Content != "prefix:base" {
		t.Errorf("expected 'prefix:base', got %q", resp.Content)
	}
}

// TestChainMultipleMiddlewares tests chaining with multiple middlewares.
func TestChainMultipleMiddlewares(t *testing.T) {
	base := &mockLLMClient{
		completeFunc: func(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
			return CompletionResponse{Content: "base"}, nil
		},
	}

	// First middleware adds prefix
	mw1 := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				resp, err := next.Complete(ctx, req)
				if err != nil {
					return resp, err
				}
				resp.Content = "mw1:" + resp.Content
				return resp, nil
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	// Second middleware adds suffix
	mw2 := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				resp, err := next.Complete(ctx, req)
				if err != nil {
					return resp, err
				}
				resp.Content = resp.Content + ":mw2"
				return resp, nil
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	// Third middleware wraps in brackets
	mw3 := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				resp, err := next.Complete(ctx, req)
				if err != nil {
					return resp, err
				}
				resp.Content = "[" + resp.Content + "]"
				return resp, nil
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	// Chain middlewares: mw1 -> mw2 -> mw3 -> base
	client := Chain(base, mw1, mw2, mw3)

	ctx := context.Background()
	req := NewCompletionRequest([]CompletionMessage{NewUserMessage("test")})
	resp, err := client.Complete(ctx, req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Expected execution order: mw1 (outer) -> mw2 -> mw3 (inner) -> base
	// Response transformation: base="base" -> mw3="[base]" -> mw2="[base]:mw2" -> mw1="mw1:[base]:mw2"
	expected := "mw1:[base]:mw2"
	if resp.Content != expected {
		t.Errorf("expected %q, got %q", expected, resp.Content)
	}
}

// TestChainRequestModification tests middleware that modifies requests.
func TestChainRequestModification(t *testing.T) {
	base := &mockLLMClient{
		completeFunc: func(_ context.Context, req CompletionRequest) (CompletionResponse, error) {
			// Base sees the modified temperature
			return CompletionResponse{
				Content: fmt.Sprintf("temp=%.1f", req.Temperature),
			}, nil
		},
	}

	// Middleware that modifies request temperature
	tempMiddleware := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				// Modify request before passing to next
				req.Temperature = 0.9
				return next.Complete(ctx, req)
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	client := Chain(base, tempMiddleware)

	ctx := context.Background()
	req := NewCompletionRequest([]CompletionMessage{NewUserMessage("test")})
	req.Temperature = 0.5 // Original temperature

	resp, err := client.Complete(ctx, req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Base should see modified temperature
	if resp.Content != "temp=0.9" {
		t.Errorf("expected 'temp=0.9', got %q", resp.Content)
	}
}

// TestChainErrorHandling tests middleware error propagation.
func TestChainErrorHandling(t *testing.T) {
	base := &mockLLMClient{
		completeFunc: func(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
			return CompletionResponse{}, fmt.Errorf("base error")
		},
	}

	// Middleware that catches and wraps errors
	errorMiddleware := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				resp, err := next.Complete(ctx, req)
				if err != nil {
					return resp, fmt.Errorf("middleware wrapper: %w", err)
				}
				return resp, nil
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	client := Chain(base, errorMiddleware)

	ctx := context.Background()
	req := NewCompletionRequest([]CompletionMessage{NewUserMessage("test")})
	_, err := client.Complete(ctx, req)

	if err == nil {
		t.Error("expected error, got nil")
	}
	if err.Error() != "middleware wrapper: base error" {
		t.Errorf("expected 'middleware wrapper: base error', got %q", err.Error())
	}
}

// TestChainShortCircuit tests middleware that short-circuits the chain.
func TestChainShortCircuit(t *testing.T) {
	baseCalled := false
	base := &mockLLMClient{
		completeFunc: func(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
			baseCalled = true
			return CompletionResponse{Content: "base"}, nil
		},
	}

	// Middleware that short-circuits on certain conditions
	shortCircuitMiddleware := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				// Check if first message contains "skip"
				if len(req.Messages) > 0 && req.Messages[0].Content == "skip" {
					// Short-circuit: don't call next
					return CompletionResponse{Content: "short-circuited"}, nil
				}
				return next.Complete(ctx, req)
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	client := Chain(base, shortCircuitMiddleware)

	ctx := context.Background()

	// Test short-circuit case
	req1 := NewCompletionRequest([]CompletionMessage{NewUserMessage("skip")})
	resp1, err := client.Complete(ctx, req1)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if resp1.Content != "short-circuited" {
		t.Errorf("expected 'short-circuited', got %q", resp1.Content)
	}
	if baseCalled {
		t.Error("base should not have been called (short-circuited)")
	}

	// Test normal case
	baseCalled = false
	req2 := NewCompletionRequest([]CompletionMessage{NewUserMessage("normal")})
	resp2, err := client.Complete(ctx, req2)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if resp2.Content != "base" {
		t.Errorf("expected 'base', got %q", resp2.Content)
	}
	if !baseCalled {
		t.Error("base should have been called (not short-circuited)")
	}
}

// TestChainModelNamePropagation tests GetModelName through the chain.
func TestChainModelNamePropagation(t *testing.T) {
	base := &mockLLMClient{
		getModelNameFunc: func() string {
			return "base-model-v1"
		},
	}

	// Middlewares that just pass through GetModelName
	mw1 := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				return next.Complete(ctx, req)
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	mw2 := func(next LLMClient) LLMClient {
		return WrapClient(
			func(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
				return next.Complete(ctx, req)
			},
			func(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			func() string {
				return next.GetModelName()
			},
		)
	}

	client := Chain(base, mw1, mw2)

	modelName := client.GetModelName()
	if modelName != "base-model-v1" {
		t.Errorf("expected 'base-model-v1', got %q", modelName)
	}
}

// TestChainNoMiddlewares tests chain with no middlewares (just base client).
func TestChainNoMiddlewares(t *testing.T) {
	base := &mockLLMClient{
		completeFunc: func(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
			return CompletionResponse{Content: "base"}, nil
		},
	}

	// Chain with no middlewares should just return base
	client := Chain(base)

	ctx := context.Background()
	req := NewCompletionRequest([]CompletionMessage{NewUserMessage("test")})
	resp, err := client.Complete(ctx, req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if resp.Content != "base" {
		t.Errorf("expected 'base', got %q", resp.Content)
	}
}

// TestClientFuncAdapter tests the clientFunc adapter type.
func TestClientFuncAdapter(t *testing.T) {
	completeInvoked := false
	streamInvoked := false
	modelNameInvoked := false

	adapter := clientFunc{
		complete: func(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
			completeInvoked = true
			return CompletionResponse{Content: "adapted"}, nil
		},
		stream: func(_ context.Context, _ CompletionRequest) (<-chan StreamChunk, error) {
			streamInvoked = true
			ch := make(chan StreamChunk)
			close(ch)
			return ch, nil
		},
		getModelName: func() string {
			modelNameInvoked = true
			return "adapted-model"
		},
	}

	ctx := context.Background()
	req := NewCompletionRequest([]CompletionMessage{NewUserMessage("test")})

	// Test Complete
	resp, err := adapter.Complete(ctx, req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !completeInvoked {
		t.Error("complete function was not invoked")
	}
	if resp.Content != "adapted" {
		t.Errorf("expected 'adapted', got %q", resp.Content)
	}

	// Test Stream
	_, err = adapter.Stream(ctx, req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !streamInvoked {
		t.Error("stream function was not invoked")
	}

	// Test GetModelName
	modelName := adapter.GetModelName()
	if !modelNameInvoked {
		t.Error("getModelName function was not invoked")
	}
	if modelName != "adapted-model" {
		t.Errorf("expected 'adapted-model', got %q", modelName)
	}
}
