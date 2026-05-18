package llmadapter

import (
	"testing"

	mllms "github.com/SnapdragonPartners/maestro-llms/llms"

	"orchestrator/pkg/agent/llm"
)

// TestToChatRequest_SystemExtraction verifies §4.2b/A3: in-band system
// messages move to ChatRequest.System (text-only) and never appear as
// conversation messages.
func TestToChatRequest_SystemExtraction(t *testing.T) {
	in := &llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: llm.RoleSystem, Content: "you are a coder"},
			{Role: llm.RoleUser, Content: "hello"},
		},
	}
	req, err := toChatRequest(in)
	if err != nil {
		t.Fatalf("toChatRequest: %v", err)
	}
	if len(req.System) != 1 || req.System[0].Text != "you are a coder" {
		t.Fatalf("system not extracted: %+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != mllms.RoleUser {
		t.Fatalf("expected single user message, got %+v", req.Messages)
	}
}

// TestToChatRequest_ToolResultSplit verifies §4.2a: a Maestro RoleUser message
// carrying tool results + the "Tool results:" placeholder becomes a single
// RoleTool message with the placeholder dropped (no spurious user turn).
func TestToChatRequest_ToolResultSplit(t *testing.T) {
	in := &llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: llm.RoleUser, Content: "do it"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "t1", Name: "run", Parameters: map[string]any{"x": 1}},
			}},
			{
				Role:        llm.RoleUser,
				Content:     placeholderToolResults,
				ToolResults: []llm.ToolResult{{ToolCallID: "t1", Content: "ok"}},
			},
		},
	}
	req, err := toChatRequest(in)
	if err != nil {
		t.Fatalf("toChatRequest: %v", err)
	}
	// user, assistant(tool_call), tool — placeholder user turn dropped.
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(req.Messages), req.Messages)
	}
	last := req.Messages[2]
	if last.Role != mllms.RoleTool || len(last.Content) != 1 ||
		last.Content[0].Type != mllms.ContentToolResult ||
		last.Content[0].ToolResult.ToolCallID != "t1" {
		t.Fatalf("tool result not split into RoleTool message: %+v", last)
	}
}

// TestToChatRequest_ToolResultWithRealText keeps genuine user text as a
// separate RoleUser message after the RoleTool message.
func TestToChatRequest_ToolResultWithRealText(t *testing.T) {
	in := &llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{
				Role:        llm.RoleUser,
				Content:     "also consider edge cases",
				ToolResults: []llm.ToolResult{{ToolCallID: "t1", Content: "ok"}},
			},
		},
	}
	req, err := toChatRequest(in)
	if err != nil {
		t.Fatalf("toChatRequest: %v", err)
	}
	if len(req.Messages) != 2 ||
		req.Messages[0].Role != mllms.RoleTool ||
		req.Messages[1].Role != mllms.RoleUser ||
		req.Messages[1].Content[0].Text != "also consider edge cases" {
		t.Fatalf("expected RoleTool then RoleUser(text): %+v", req.Messages)
	}
}

// TestToChatRequest_CacheBreakpoint verifies §4.2/A5 mapping.
func TestToChatRequest_CacheBreakpoint(t *testing.T) {
	in := &llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: llm.RoleSystem, Content: "sys", CacheControl: &llm.CacheControl{Type: "ephemeral"}},
		},
	}
	req, err := toChatRequest(in)
	if err != nil {
		t.Fatalf("toChatRequest: %v", err)
	}
	if !req.System[0].CacheBreakpoint {
		t.Fatalf("CacheControl not mapped to CacheBreakpoint")
	}
}

func TestNormalizeStopReason(t *testing.T) {
	cases := map[string]string{
		"max_tokens":        "max_tokens",
		"max_output_tokens": "max_tokens",
		"length":            "max_tokens",
		"MAX_TOKENS":        "max_tokens",
		"end_turn":          "end_turn",
		"STOP":              "end_turn",
		"completed":         "end_turn",
		"":                  "end_turn",
		"tool_calls":        "tool_use",
		"pause_turn":        "pause_turn",
		"weird_new_reason":  "weird_new_reason",
	}
	for in, want := range cases {
		if got := normalizeStopReason(in); got != want {
			t.Errorf("normalizeStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFromChatResponse_ToolParamsToMap verifies §4.1/A2: raw JSON tool-call
// params are unmarshaled back to map[string]any for existing consumers.
func TestFromChatResponse_ToolParamsToMap(t *testing.T) {
	resp := &mllms.ChatResponse{
		Text:       "done",
		StopReason: "end_turn",
		ToolCalls: []mllms.ToolCall{
			{ID: "t1", Name: "run", Parameters: []byte(`{"path":"/tmp","n":3}`)},
		},
	}
	out, err := fromChatResponse(resp)
	if err != nil {
		t.Fatalf("fromChatResponse: %v", err)
	}
	if out.Content != "done" || out.StopReason != "end_turn" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Parameters["path"] != "/tmp" {
		t.Fatalf("tool params not unmarshaled to map: %+v", out.ToolCalls)
	}
}
