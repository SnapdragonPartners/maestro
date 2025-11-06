// Package openai provides OpenAI client implementation for LLM interface.
package openai

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sashabaranov/go-openai"

	"orchestrator/pkg/agent/llm"
)

// O3Client wraps the OpenAI API client to implement llm.LLMClient interface.
type O3Client struct {
	client *openai.Client
	model  string
}

// NewO3Client creates a new OpenAI o3 client wrapper (raw client, middleware applied at higher level).
func NewO3Client(apiKey string) llm.LLMClient {
	return &O3Client{
		client: openai.NewClient(apiKey),
		model:  "o3-mini", // Default o3 model
	}
}

// NewO3ClientWithModel creates a new OpenAI client with specific model (raw client, middleware applied at higher level).
func NewO3ClientWithModel(apiKey, model string) llm.LLMClient {
	return &O3Client{
		client: openai.NewClient(apiKey),
		model:  model,
	}
}

// Complete implements the llm.LLMClient interface.
//
//nolint:gocritic // 80 bytes is reasonable for interface compliance
func (o *O3Client) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error) {
	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
	if o.model == "" {
		o.model = "o3-mini"
	}

	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang

	// Convert to OpenAI messages.
	messages := make([]openai.ChatCompletionMessage, 0, len(in.Messages))
	for i := range in.Messages {
		msg := &in.Messages[i]
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang

	// Convert tools if provided
	var tools []openai.Tool
	if len(in.Tools) > 0 {
		tools = make([]openai.Tool, len(in.Tools))
		for i, tool := range in.Tools {
			tools[i] = openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.InputSchema,
				},
			}
		}
	}

	// Make API request.
	req := openai.ChatCompletionRequest{
		Model:               o.model,
		Messages:            messages,
		MaxCompletionTokens: in.MaxTokens,
		// Note: O3 models have beta limitations - temperature is fixed at 1.
	}
	if len(tools) > 0 {
		req.Tools = tools
	}

	resp, err := o.client.CreateChatCompletion(ctx, req)

	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang

	if err != nil {
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
		return llm.CompletionResponse{}, fmt.Errorf("openai chat completion failed: %w", err)
	}

	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang

	if len(resp.Choices) == 0 {
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
		return llm.CompletionResponse{}, fmt.Errorf("empty response from OpenAI o3")
	}

	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang

	result := llm.CompletionResponse{Content: resp.Choices[0].Message.Content}

	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang

	return result, nil
}

// Stream implements the llm.LLMClient interface.
//
//nolint:gocritic // 80 bytes is reasonable for interface compliance
func (o *O3Client) Stream(ctx context.Context, in llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	if o.model == "" {
		o.model = "o3-mini"
	}

	// Convert to OpenAI messages.
	messages := make([]openai.ChatCompletionMessage, 0, len(in.Messages))
	for i := range in.Messages {
		msg := &in.Messages[i]
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	// Create streaming request.
	stream, err := o.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:               o.model,
		Messages:            messages,
		MaxCompletionTokens: in.MaxTokens,
		Stream:              true,
		// Note: O3 models have beta limitations - temperature is fixed at 1.
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI stream: %w", err)
	}

	// Create output channel.
	ch := make(chan llm.StreamChunk)

	// Start goroutine to process stream.
	go func() {
		defer close(ch)
		defer func() {
			if err := stream.Close(); err != nil {
				// Log error but don't fail the stream processing.
				// This is cleanup code in a streaming context.
				_ = err // Ignore error in cleanup
			}
		}()

		for {
			select {
			case <-ctx.Done():
				ch <- llm.StreamChunk{Error: ctx.Err()}
				return
			default:
				response, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					ch <- llm.StreamChunk{Done: true}
					return
				}
				if err != nil {
					ch <- llm.StreamChunk{Error: err}
					return
				}

				if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
					ch <- llm.StreamChunk{Content: response.Choices[0].Delta.Content}
				}
			}
		}
	}()

	return ch, nil
}

// SetModel allows changing the model after client creation.
func (o *O3Client) SetModel(model string) {
	o.model = model
}

// GetModel returns the current model being used.
func (o *O3Client) GetModel() string {
	return o.model
}

// GetModelName returns the model name for this client.
func (o *O3Client) GetModelName() string {
	return o.model
}
