// Package llmadapter adapts the external maestro-llms toolkit
// (github.com/SnapdragonPartners/maestro-llms) to Maestro's in-tree
// llm.LLMClient contract.
//
// It is the single seam described in docs/MAESTRO_LLMS_MIGRATION.md: the ~175
// existing LLMClient consumers, BaseStateMachine, the toolloop, and
// internal/mocks.MockLLMClient are unaffected because the adapter preserves
// llm.LLMClient. Everything provider-specific lives behind maestro-llms.
//
// This file implements §4.1 (mechanical type mapping) and §4.2 (transcript
// normalization: system extraction, tool-result message splitting,
// "Tool results:" placeholder drop, and stop-reason normalization). Middleware
// composition (phase 2), explicit tool-choice plumbing (phase 4), and the
// empty-response rework (phase 3) are intentionally NOT here yet — see the
// migration spec for phasing.
package llmadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mllms "github.com/SnapdragonPartners/maestro-llms/llms"
	manthropic "github.com/SnapdragonPartners/maestro-llms/llms/providers/anthropic"
	mgoogle "github.com/SnapdragonPartners/maestro-llms/llms/providers/google"
	mollama "github.com/SnapdragonPartners/maestro-llms/llms/providers/ollama"
	mopenai "github.com/SnapdragonPartners/maestro-llms/llms/providers/openai"
	"github.com/openai/openai-go/responses"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/tools"
)

// placeholderToolResults is the synthetic content ContextManager injects when
// it has tool results but no real user text (Anthropic's SDK historically
// rejected an empty content field). maestro-llms validation rejects mixed
// text+tool-result messages and needs no non-empty filler, so the adapter
// drops this exact string rather than emitting a spurious user turn (§4.2a).
const placeholderToolResults = "Tool results:"

// stopEndTurn is Maestro's legacy canonical "normal completion" stop reason.
const stopEndTurn = "end_turn"

// Adapter implements llm.LLMClient over a maestro-llms ChatClient.
type Adapter struct {
	client   mllms.ChatClient
	provider string
	model    string
}

// Verify the contract at compile time.
var _ llm.LLMClient = (*Adapter)(nil)

// NewChatClient builds the bare maestro-llms provider client (no middleware).
// SDK-level retries are left at the toolkit default (0); the resilience chain
// is the middleware's job (§5 X1) and is composed by the factory (phase 2).
func NewChatClient(provider, apiKey, model string) (mllms.ChatClient, error) {
	var (
		client mllms.ChatClient
		err    error
	)
	switch provider {
	case config.ProviderAnthropic:
		client, err = manthropic.New(manthropic.WithAPIKey(apiKey), manthropic.WithModel(model))
	case config.ProviderOpenAI:
		client, err = mopenai.NewChat(mopenai.WithAPIKey(apiKey), mopenai.WithModel(model))
	case config.ProviderGoogle:
		client, err = mgoogle.New(mgoogle.WithAPIKey(apiKey), mgoogle.WithModel(model))
	case config.ProviderOllama:
		// Ollama takes a base URL where other providers take an API key.
		client, err = mollama.New(mollama.WithBaseURL(apiKey), mollama.WithModel(model))
	default:
		return nil, fmt.Errorf("llmadapter: unsupported provider %q", provider)
	}
	if err != nil {
		return nil, fmt.Errorf("llmadapter: constructing %s client: %w", provider, err)
	}
	return client, nil
}

// Wrap adapts an existing maestro-llms ChatClient (typically already decorated
// with the toolkit middleware chain) to llm.LLMClient.
func Wrap(client mllms.ChatClient, provider, model string) *Adapter {
	return &Adapter{client: client, provider: provider, model: model}
}

// New builds a bare Adapter (no middleware) for the given provider. Used by
// the preflight/raw path; the agent path composes middleware then calls Wrap.
func New(provider, apiKey, model string) (*Adapter, error) {
	client, err := NewChatClient(provider, apiKey, model)
	if err != nil {
		return nil, err
	}
	return Wrap(client, provider, model), nil
}

// GetModelName returns the configured model name.
func (a *Adapter) GetModelName() string { return a.model }

// Stream is unsupported: maestro-llms does not implement streaming and Maestro
// has no Stream() consumers outside the (soon-deleted) in-tree impls. The
// method stays on the interface for backward compatibility only.
//
//nolint:gocritic // hugeParam: signature is fixed by the llm.LLMClient interface.
func (a *Adapter) Stream(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("llmadapter: streaming is not supported by maestro-llms")
}

// Complete maps the request, calls the toolkit, and maps the response back.
//
//nolint:gocritic // hugeParam: signature is fixed by the llm.LLMClient interface.
func (a *Adapter) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error) {
	req, err := toChatRequest(&in)
	if err != nil {
		return llm.CompletionResponse{}, fmt.Errorf("llmadapter: building request: %w", err)
	}
	resp, err := a.client.Complete(ctx, req)
	if err != nil {
		// Error classification/suspend mapping is phase 2 (§5 M4); wrap to
		// preserve the cause (errors.Is/As) until that mapping lands.
		return llm.CompletionResponse{}, fmt.Errorf("llmadapter: provider call failed: %w", err)
	}
	return fromChatResponse(a.provider, &resp)
}

// toChatRequest performs §4.1 type mapping plus §4.2 transcript normalization.
func toChatRequest(in *llm.CompletionRequest) (mllms.ChatRequest, error) {
	var system []mllms.ContentPart
	messages := make([]mllms.Message, 0, len(in.Messages))

	for i := range in.Messages {
		msg := &in.Messages[i]
		switch msg.Role {
		case llm.RoleSystem:
			// §4.2b / A3: system is carried by ChatRequest.System, text-only,
			// never as a mid-conversation message.
			part := mllms.Text(msg.Content)
			if msg.CacheControl != nil {
				part.CacheBreakpoint = true // §4.2 / A5
			}
			system = append(system, part)

		case llm.RoleAssistant:
			parts := make([]mllms.ContentPart, 0, 1+len(msg.ToolCalls))
			if msg.Content != "" {
				parts = append(parts, mllms.Text(msg.Content))
			}
			for j := range msg.ToolCalls {
				tc := &msg.ToolCalls[j]
				raw, mErr := json.Marshal(tc.Parameters)
				if mErr != nil {
					return mllms.ChatRequest{}, fmt.Errorf("marshaling tool-call %q params: %w", tc.Name, mErr)
				}
				parts = append(parts, mllms.ContentPart{
					Type:     mllms.ContentToolCall,
					ToolCall: &mllms.ToolCall{ID: tc.ID, Name: tc.Name, Parameters: raw},
				})
			}
			applyCacheBreakpoint(parts, msg.CacheControl)
			messages = append(messages, mllms.Message{Role: mllms.RoleAssistant, Content: parts})

		case llm.RoleUser:
			// §4.2a: Maestro packs tool results + optional user text (and
			// sometimes only the "Tool results:" placeholder) into one
			// RoleUser message. The toolkit requires tool results in a
			// RoleTool message and rejects mixed text+tool-result content.
			// Split into RoleTool then optional RoleUser; drop the placeholder.
			if len(msg.ToolResults) > 0 {
				trParts := make([]mllms.ContentPart, 0, len(msg.ToolResults))
				for k := range msg.ToolResults {
					tr := &msg.ToolResults[k]
					trParts = append(trParts, mllms.ContentPart{
						Type: mllms.ContentToolResult,
						ToolResult: &mllms.ToolResult{
							ToolCallID: tr.ToolCallID,
							Content:    tr.Content,
							IsError:    tr.IsError,
						},
					})
				}
				messages = append(messages, mllms.Message{Role: mllms.RoleTool, Content: trParts})

				if text := strings.TrimSpace(msg.Content); text != "" && text != placeholderToolResults {
					messages = append(messages, mllms.Message{
						Role:    mllms.RoleUser,
						Content: []mllms.ContentPart{mllms.Text(msg.Content)},
					})
				}
				continue
			}

			parts := []mllms.ContentPart{mllms.Text(msg.Content)}
			applyCacheBreakpoint(parts, msg.CacheControl)
			messages = append(messages, mllms.Message{Role: mllms.RoleUser, Content: parts})

		default:
			return mllms.ChatRequest{}, fmt.Errorf("unknown message role %q", msg.Role)
		}
	}

	tools, err := toToolDefinitions(in.Tools)
	if err != nil {
		return mllms.ChatRequest{}, err
	}

	temp := in.Temperature
	return mllms.ChatRequest{
		System:      system,
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  toToolChoice(in.ToolChoice, len(tools)),
		Purpose:     mllms.PurposeChat,
		MaxTokens:   in.MaxTokens,
		Temperature: &temp,
	}, nil
}

// toToolChoice maps Maestro's ToolChoice string to the toolkit's typed choice
// (§5 OC2/G2). There is NO blanket "force tools" default: an empty string
// maps to Auto (toolkit default). Callers needing a guaranteed tool call set
// llm.ToolChoiceRequired explicitly (the toolloop does). A Required/named
// choice with no tools offered would be rejected up front by the toolkit, so
// it is defensively downgraded to Auto here.
func toToolChoice(choice string, numTools int) mllms.ToolChoice {
	switch choice {
	case "", llm.ToolChoiceAuto:
		return mllms.ToolChoice{Type: mllms.ToolChoiceAuto}
	case llm.ToolChoiceNone:
		return mllms.ToolChoice{Type: mllms.ToolChoiceNone}
	case llm.ToolChoiceRequired, "any":
		if numTools == 0 {
			return mllms.ToolChoice{Type: mllms.ToolChoiceAuto}
		}
		return mllms.ToolChoice{Type: mllms.ToolChoiceRequired}
	default:
		// Treat any other value as a specific tool name to force.
		if numTools == 0 {
			return mllms.ToolChoice{Type: mllms.ToolChoiceAuto}
		}
		return mllms.ToolChoice{Type: mllms.ToolChoiceTool, Name: choice}
	}
}

// applyCacheBreakpoint maps a non-nil Maestro CacheControl to the toolkit's
// neutral CacheBreakpoint hint on the last content part (§4.2 / A5).
func applyCacheBreakpoint(parts []mllms.ContentPart, cc *llm.CacheControl) {
	if cc == nil || len(parts) == 0 {
		return
	}
	parts[len(parts)-1].CacheBreakpoint = true
}

// toToolDefinitions marshals Maestro's structured InputSchema to the raw JSON
// schema the toolkit expects.
func toToolDefinitions(in []tools.ToolDefinition) ([]mllms.ToolDefinition, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]mllms.ToolDefinition, 0, len(in))
	for i := range in {
		schema, err := json.Marshal(in[i].InputSchema)
		if err != nil {
			return nil, fmt.Errorf("marshaling input schema for tool %q: %w", in[i].Name, err)
		}
		out = append(out, mllms.ToolDefinition{
			Name:        in[i].Name,
			Description: in[i].Description,
			InputSchema: schema,
		})
	}
	return out, nil
}

// fromChatResponse maps the toolkit response back, unmarshaling raw tool-call
// params to map[string]any (§4.1 / A2) so existing consumers are untouched,
// and normalizing the stop reason to Maestro's legacy strings (§4.2c).
func fromChatResponse(provider string, resp *mllms.ChatResponse) (llm.CompletionResponse, error) {
	calls := make([]llm.ToolCall, 0, len(resp.ToolCalls))
	for i := range resp.ToolCalls {
		tc := &resp.ToolCalls[i]
		params := map[string]any{}
		if len(tc.Parameters) > 0 {
			if err := json.Unmarshal(tc.Parameters, &params); err != nil {
				return llm.CompletionResponse{}, fmt.Errorf("unmarshaling tool-call %q params: %w", tc.Name, err)
			}
		}
		calls = append(calls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Parameters: params})
	}
	return llm.CompletionResponse{
		ToolCalls:  calls,
		Content:    resp.Text,
		StopReason: normalizeStopReason(rawStopReason(provider, resp)),
	}, nil
}

// rawStopReason resolves the provider's true stop reason before generic
// normalization. maestro-llms v0.4.0 maps OpenAI's *response status* to
// StopReason, so a max-output truncation surfaces as "incomplete" rather than
// a length reason — the toolloop's max_tokens guard would miss it and process
// truncated tool-call params. Until the toolkit surfaces the incomplete
// reason itself, inspect the OpenAI Raw response (a deliberate,
// provider-scoped coupling; Raw is outside the toolkit stability contract so
// the type assertion is defensive). Tracked in docs/MAESTRO_LLMS_MIGRATION.md
// §5 OL1/OL3 and §9 as a candidate upstream fix.
func rawStopReason(provider string, resp *mllms.ChatResponse) string {
	reason := string(resp.StopReason)
	if provider != config.ProviderOpenAI || reason != "incomplete" {
		return reason
	}
	if r, ok := resp.Raw.(*responses.Response); ok && r.IncompleteDetails.Reason != "" {
		return r.IncompleteDetails.Reason // "max_output_tokens" | "content_filter"
	}
	return reason
}

// normalizeStopReason maps provider-specific stop reasons back to the legacy
// canonical strings Maestro consumers branch on — critically "max_tokens",
// which the toolloop uses to detect truncated tool calls
// (pkg/agent/toolloop/toolloop.go:357). The toolkit deliberately does not
// normalize these (§4.2c, §5 OL1/OL3): Anthropic emits "max_tokens", OpenAI
// status-derived reasons, Ollama raw done_reason, Gemini SDK finish enums.
func normalizeStopReason(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "max_tokens", "max_output_tokens", "length", "model_length":
		return "max_tokens"
	case stopEndTurn, "stop", "end", "completed", "complete":
		return stopEndTurn
	case "tool_use", "tool_calls", "tool_call", "function_call":
		return "tool_use"
	case "pause_turn":
		return "pause_turn"
	case "refusal", "content_filter", "safety", "recitation":
		return "refusal"
	case "":
		return stopEndTurn
	default:
		// Unknown/new reasons pass through lowercased rather than being
		// silently coerced; audit before adding to the canonical set.
		return strings.ToLower(strings.TrimSpace(raw))
	}
}
