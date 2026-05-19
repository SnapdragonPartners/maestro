package llmadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	mopenai "github.com/SnapdragonPartners/maestro-llms/llms/providers/openai"

	"orchestrator/pkg/agent/llm"
)

// TestOpenAIIncompleteRawCouplingGuard is a narrow drift guard for the P2
// workaround (docs/MAESTRO_LLMS_MIGRATION.md §5 OL1/OL3, §9). It is NOT a
// re-test of the toolkit's OpenAI parsing (that belongs upstream in
// maestro-llms). Its sole job: confirm the fragile assumption our
// rawStopReason workaround depends on still holds against the REAL openai-go
// SDK + toolkit provider — namely that on a max-output truncation the
// toolkit yields StopReason "incomplete" with Raw == *responses.Response whose
// IncompleteDetails.Reason == "max_output_tokens" — and that our adapter then
// normalizes that to the legacy "max_tokens" the toolloop guard keys on.
//
// When the toolkit surfaces the incomplete reason directly (the filed
// upstream request), delete this test AND the rawStopReason workaround.
func TestOpenAIIncompleteRawCouplingGuard(t *testing.T) {
	// Canned OpenAI Responses API body: truncated (status=incomplete,
	// incomplete_details.reason=max_output_tokens) with a function call.
	const body = `{
	  "id": "resp_test",
	  "object": "response",
	  "created_at": 1,
	  "model": "gpt-4o-mini",
	  "status": "incomplete",
	  "incomplete_details": {"reason": "max_output_tokens"},
	  "output": [
	    {"type":"function_call","id":"fc_1","call_id":"call_1","name":"do_it","arguments":"{\"x\":1}","status":"completed"}
	  ],
	  "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client, err := mopenai.NewChat(
		mopenai.WithAPIKey("test-key"),
		mopenai.WithModel("gpt-4o-mini"),
		mopenai.WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}

	adapter := Wrap(client, "openai", "gpt-4o-mini")
	resp, err := adapter.Complete(context.Background(), llm.CompletionRequest{
		Messages:  []llm.CompletionMessage{{Role: llm.RoleUser, Content: "hi"}},
		MaxTokens: 8,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// The whole point: a max-output truncation must surface as the legacy
	// canonical "max_tokens" the toolloop branches on — proving the Raw
	// coupling + normalization still works against the real SDK.
	if resp.StopReason != "max_tokens" {
		t.Fatalf("StopReason = %q, want %q (Raw coupling assumption broken — see §9 upstream request)", resp.StopReason, "max_tokens")
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "do_it" {
		t.Fatalf("tool call not preserved through the incomplete path: %+v", resp.ToolCalls)
	}
}
