package agent

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/sashabaranov/go-openai"
)

// O3Client wraps the OpenAI API client to implement LLMClient interface
type O3Client struct {
	client *openai.Client
	model  string
}

// NewO3Client creates a new OpenAI o3 client wrapper with default retry logic
func NewO3Client(apiKey string) LLMClient {
	if SystemMode == ModeMock {
		return NewMockLLMClient([]CompletionResponse{{Content: "mock response"}}, nil)
	}
	baseClient := &O3Client{
		client: openai.NewClient(apiKey),
		model:  "o3-mini", // Default o3 model
	}

	// Wrap with both circuit breaker and retry logic
	return NewResilientClient(baseClient)
}

// NewO3ClientWithModel creates a new OpenAI client with specific model and retry logic
func NewO3ClientWithModel(apiKey string, model string) LLMClient {
	if SystemMode == ModeMock {
		return NewMockLLMClient([]CompletionResponse{{Content: "mock response"}}, nil)
	}
	baseClient := &O3Client{
		client: openai.NewClient(apiKey),
		model:  model,
	}

	// Wrap with both circuit breaker and retry logic
	return NewResilientClient(baseClient)
}

// Complete implements the LLMClient interface
func (o *O3Client) Complete(ctx context.Context, in CompletionRequest) (CompletionResponse, error) {
	if SystemMode == ModeDebug {
		log.Printf("O3 completing request with %d messages", len(in.Messages))
	}
	if o.model == "" {
		o.model = "o3-mini"
	}

	// Convert to OpenAI messages
	var messages []openai.ChatCompletionMessage
	for _, msg := range in.Messages {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	// Make API request
	resp, err := o.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:               o.model,
		Messages:            messages,
		MaxCompletionTokens: in.MaxTokens,
		// Note: O3 models have beta limitations - temperature is fixed at 1
	})

	if err != nil {
		return CompletionResponse{}, fmt.Errorf("openai chat completion failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("empty response from OpenAI o3")
	}

	return CompletionResponse{Content: resp.Choices[0].Message.Content}, nil
}

// Stream implements the LLMClient interface
func (o *O3Client) Stream(ctx context.Context, in CompletionRequest) (<-chan StreamChunk, error) {
	if SystemMode == ModeDebug {
		log.Printf("O3 starting stream with %d messages", len(in.Messages))
	}
	if o.model == "" {
		o.model = "o3-mini"
	}

	// Convert to OpenAI messages
	var messages []openai.ChatCompletionMessage
	for _, msg := range in.Messages {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	// Create streaming request
	stream, err := o.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:               o.model,
		Messages:            messages,
		MaxCompletionTokens: in.MaxTokens,
		Stream:              true,
		// Note: O3 models have beta limitations - temperature is fixed at 1
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI stream: %w", err)
	}

	// Create output channel
	ch := make(chan StreamChunk)

	// Start goroutine to process stream
	go func() {
		defer close(ch)
		defer stream.Close()

		for {
			select {
			case <-ctx.Done():
				ch <- StreamChunk{Error: ctx.Err()}
				return
			default:
				response, err := stream.Recv()
				if err == io.EOF {
					ch <- StreamChunk{Done: true}
					return
				}
				if err != nil {
					ch <- StreamChunk{Error: err}
					return
				}

				if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
					ch <- StreamChunk{Content: response.Choices[0].Delta.Content}
				}
			}
		}
	}()

	return ch, nil
}

// SetModel allows changing the model after client creation
func (o *O3Client) SetModel(model string) {
	o.model = model
}

// GetModel returns the current model being used
func (o *O3Client) GetModel() string {
	return o.model
}
