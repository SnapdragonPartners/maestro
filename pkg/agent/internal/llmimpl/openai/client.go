// Package openai provides OpenAI client implementation for LLM interface.
package openai

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sashabaranov/go-openai"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
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
func (o *O3Client) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error) {
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

	// Make API request.
	resp, err := o.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:               o.model,
		Messages:            messages,
		MaxCompletionTokens: in.MaxTokens,
		// Note: O3 models have beta limitations - temperature is fixed at 1.
	})

	if err != nil {
		return llm.CompletionResponse{}, fmt.Errorf("openai chat completion failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return llm.CompletionResponse{}, fmt.Errorf("empty response from OpenAI o3")
	}

	return llm.CompletionResponse{Content: resp.Choices[0].Message.Content}, nil
}

// Stream implements the llm.LLMClient interface.
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

// GetDefaultConfig returns default model configuration for O3.
func (o *O3Client) GetDefaultConfig() config.Model {
	return config.Model{
		Name:           "o3-mini",
		MaxTPM:         10000, // 10k tokens per minute for O3 mini
		DailyBudget:    100.0, // $100 daily budget
		MaxConnections: 2,     // 2 concurrent connections (O3 has lower limits)
		CPM:            15.0,  // $15 per million tokens (approximate)
	}
}
