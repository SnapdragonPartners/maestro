package llmadapter

import (
	"testing"

	mllms "github.com/SnapdragonPartners/maestro-llms/llms"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/tools"
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

// TestProviderSignatureRoundTrip verifies G1 (maestro-llms ADR-0010): the
// opaque provider signature survives response → llm.ToolCall and is replayed
// llm.ToolCall → request. Without this, Gemini 3 hard-400s on turn 2.
func TestProviderSignatureRoundTrip(t *testing.T) {
	sig := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	// Response side: toolkit ToolCall.ProviderSignature → llm.ToolCall.
	in := &mllms.ChatResponse{
		StopReason: "tool_use",
		ToolCalls: []mllms.ToolCall{
			{ID: "c1", Name: "list_files", Parameters: []byte(`{}`), ProviderSignature: sig},
		},
	}
	resp, err := fromChatResponse(in)
	if err != nil {
		t.Fatalf("fromChatResponse: %v", err)
	}
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].ProviderSignature) != string(sig) {
		t.Fatalf("signature not captured from response: %+v", resp.ToolCalls)
	}

	// Request side: llm.ToolCall.ProviderSignature → toolkit ToolCall, on the
	// assistant turn that resends the tool call.
	req, err := toChatRequest(&llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: llm.RoleAssistant, ToolCalls: resp.ToolCalls},
		},
	})
	if err != nil {
		t.Fatalf("toChatRequest: %v", err)
	}
	var got []byte
	for _, m := range req.Messages {
		for _, p := range m.Content {
			if p.Type == mllms.ContentToolCall && p.ToolCall != nil {
				got = p.ToolCall.ProviderSignature
			}
		}
	}
	if string(got) != string(sig) {
		t.Fatalf("signature not replayed into request: got %v want %v", got, sig)
	}
}

// TestToToolChoice verifies §5 OC2/G2: no blanket "force tools" default
// (empty → Auto), explicit Required honored, and Required/named downgraded to
// Auto when no tools are offered (toolkit would otherwise reject it).
func TestToToolChoice(t *testing.T) {
	cases := []struct {
		choice   string
		numTools int
		wantType mllms.ToolChoiceType
		wantName string
	}{
		{"", 3, mllms.ToolChoiceAuto, ""},
		{llm.ToolChoiceAuto, 3, mllms.ToolChoiceAuto, ""},
		{llm.ToolChoiceNone, 3, mllms.ToolChoiceNone, ""},
		{llm.ToolChoiceRequired, 3, mllms.ToolChoiceRequired, ""},
		{"any", 3, mllms.ToolChoiceRequired, ""},
		{llm.ToolChoiceRequired, 0, mllms.ToolChoiceAuto, ""}, // downgrade: no tools
		{"get_weather", 2, mllms.ToolChoiceTool, "get_weather"},
		{"get_weather", 0, mllms.ToolChoiceAuto, ""}, // downgrade: no tools
	}
	for _, tc := range cases {
		got := toToolChoice(tc.choice, tc.numTools)
		if got.Type != tc.wantType || got.Name != tc.wantName {
			t.Errorf("toToolChoice(%q,%d) = {%s,%q}, want {%s,%q}",
				tc.choice, tc.numTools, got.Type, got.Name, tc.wantType, tc.wantName)
		}
	}
}

// TestToChatRequest_NoBlanketToolForcing: with tools but no explicit choice,
// the request must NOT force tools (Anthropic's old default was auto).
func TestToChatRequest_NoBlanketToolForcing(t *testing.T) {
	in := &llm.CompletionRequest{
		Messages: []llm.CompletionMessage{{Role: llm.RoleUser, Content: "hi"}},
		Tools:    []tools.ToolDefinition{{Name: "t", InputSchema: tools.InputSchema{Type: "object"}}},
	}
	req, err := toChatRequest(in)
	if err != nil {
		t.Fatalf("toChatRequest: %v", err)
	}
	if req.ToolChoice.Type != mllms.ToolChoiceAuto {
		t.Fatalf("unset ToolChoice with tools must be Auto, got %s", req.ToolChoice.Type)
	}
}

// OpenAI max-output truncation: maestro-llms v0.4.1 (divergence OC4) now
// surfaces the raw finish reason "max_output_tokens" as StopReason (the prior
// v0.4.0 status-only behavior + Maestro Raw workaround were removed). The
// adapter just normalizes it; that "max_output_tokens" → "max_tokens" mapping
// is covered by TestNormalizeStopReason, plus the end-to-end pass-through here.
func TestFromChatResponse_OpenAITruncationNormalized(t *testing.T) {
	resp := &mllms.ChatResponse{Text: "partial", StopReason: "max_output_tokens"}
	out, err := fromChatResponse(resp)
	if err != nil {
		t.Fatalf("fromChatResponse: %v", err)
	}
	if out.StopReason != "max_tokens" {
		t.Fatalf("v0.4.1 raw truncation reason not normalized to max_tokens, got %q", out.StopReason)
	}
}
