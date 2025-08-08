// Package llm provides middleware chaining for LLM clients.
package llm

import (
	"context"

	"orchestrator/pkg/config"
)

// Middleware represents a function that wraps an LLMClient with additional behavior.
// Middleware functions are composed using Chain() to create a processing pipeline.
type Middleware func(next LLMClient) LLMClient

// clientFunc is an adapter that allows plain functions to implement the LLMClient interface.
type clientFunc struct {
	complete     func(context.Context, CompletionRequest) (CompletionResponse, error)
	stream       func(context.Context, CompletionRequest) (<-chan StreamChunk, error)
	getDefConfig func() config.Model
}

func (f clientFunc) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	return f.complete(ctx, req)
}

func (f clientFunc) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
	return f.stream(ctx, req)
}

// GetDefaultConfig delegates to the wrapped function.
func (f clientFunc) GetDefaultConfig() config.Model {
	return f.getDefConfig()
}

// WrapClient creates a new LLMClient using the provided function implementations.
// This is a helper for middleware implementations that need to wrap behavior.
func WrapClient(
	complete func(context.Context, CompletionRequest) (CompletionResponse, error),
	stream func(context.Context, CompletionRequest) (<-chan StreamChunk, error),
	getDefConfig func() config.Model,
) LLMClient {
	return clientFunc{
		complete:     complete,
		stream:       stream,
		getDefConfig: getDefConfig,
	}
}

// Chain composes multiple middlewares around a base LLMClient.
// Middlewares are applied in order, with earlier middlewares being outermost.
//
// For example: Chain(client, mw1, mw2, mw3) creates the call stack:
//
//	mw1 -> mw2 -> mw3 -> client
//
// This means mw1 runs first and has the opportunity to modify the request
// or short-circuit before it reaches mw2, mw3, and finally the base client.
func Chain(base LLMClient, middlewares ...Middleware) LLMClient {
	// Apply middlewares in reverse order so that the first middleware
	// in the slice becomes the outermost wrapper
	client := base
	for i := len(middlewares) - 1; i >= 0; i-- {
		client = middlewares[i](client)
	}
	return client
}
