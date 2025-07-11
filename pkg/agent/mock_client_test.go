package agent

import (
	"context"
	"errors"
	"testing"
)

func TestMockLLMClient(t *testing.T) {
	responses := []CompletionResponse{
		{Content: "response1"},
		{Content: "response2"},
	}
	errs := []error{nil, errors.New("test error")}

	client := NewMockLLMClient(responses, errs)

	t.Run("Complete returns responses in order", func(t *testing.T) {
		resp, err := client.Complete(context.Background(), CompletionRequest{})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if resp.Content != "response1" {
			t.Errorf("got %q, want %q", resp.Content, "response1")
		}

		resp, err = client.Complete(context.Background(), CompletionRequest{})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if resp.Content != "response2" {
			t.Errorf("got %q, want %q", resp.Content, "response2")
		}

		_, err = client.Complete(context.Background(), CompletionRequest{})
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("Stream returns responses in order", func(t *testing.T) {
		client := NewMockLLMClient(responses, errs)

		ch, err := client.Stream(context.Background(), CompletionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		chunk := <-ch
		if chunk.Content != "response1" {
			t.Errorf("got %q, want %q", chunk.Content, "response1")
		}
		if !chunk.Done {
			t.Error("expected Done to be true")
		}

		ch, err = client.Stream(context.Background(), CompletionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		chunk = <-ch
		if chunk.Content != "response2" {
			t.Errorf("got %q, want %q", chunk.Content, "response2")
		}
		if !chunk.Done {
			t.Error("expected Done to be true")
		}

		_, err = client.Stream(context.Background(), CompletionRequest{})
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("Returns errors in order", func(t *testing.T) {
		client := NewMockLLMClient(responses, []error{errors.New("test error")})

		_, err := client.Complete(context.Background(), CompletionRequest{})
		if err == nil {
			t.Error("expected error, got nil")
		}
		if err.Error() != "test error" {
			t.Errorf("got %q, want %q", err.Error(), "test error")
		}
	})
}
