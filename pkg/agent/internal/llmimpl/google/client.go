// Package google provides Google Gemini client implementation for LLM interface.
package google

import (
	"context"
	"fmt"

	"google.golang.org/genai"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/tools"
)

// GeminiClient wraps the Google GenAI client to implement llm.LLMClient interface.
type GeminiClient struct {
	client        *genai.Client
	apiKey        string
	model         string
	responseCache []*genai.Content // Cache all assistant responses with thought signatures
}

// NewGeminiClientWithModel creates a new Gemini client with specific model (raw client, middleware applied at higher level).
func NewGeminiClientWithModel(apiKey, model string) llm.LLMClient {
	// Note: Client creation requires context, so we'll defer it to Complete()
	// This matches the pattern of storing config here and creating client on-demand
	return &GeminiClient{
		client: nil, // Will be created on first use
		apiKey: apiKey,
		model:  model,
	}
}

// Complete implements the llm.LLMClient interface.
//
//nolint:gocritic // CompletionRequest size acceptable for interface consistency
func (g *GeminiClient) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error) {
	// Create client if not already created
	if g.client == nil {
		// Pass API key directly to ClientConfig
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  g.apiKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return llm.CompletionResponse{}, llmerrors.NewError(llmerrors.ErrorTypeRateLimit, fmt.Sprintf("failed to create Gemini client: %v", err))
		}
		g.client = client
	}

	// Convert our messages to Gemini Content format
	// Pass responseCache to preserve thought signatures for all assistant messages
	contents, systemInstruction, err := convertMessagesToGemini(in.Messages, g.responseCache)
	if err != nil {
		return llm.CompletionResponse{}, llmerrors.NewError(llmerrors.ErrorTypeBadPrompt, fmt.Sprintf("message conversion error: %v", err))
	}

	// Build generation config
	//nolint:gosec // MaxTokens validated at higher layer, overflow acceptable
	maxTokens := int32(in.MaxTokens)
	config := &genai.GenerateContentConfig{
		Temperature:     &in.Temperature,
		MaxOutputTokens: maxTokens,
	}

	// Add system instruction if present
	if systemInstruction != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: systemInstruction}},
		}
	}

	// Convert tools if provided
	if len(in.Tools) > 0 {
		toolDefs := convertToolsToGemini(in.Tools)
		config.Tools = []*genai.Tool{
			{FunctionDeclarations: toolDefs},
		}

		// Always force tool use when tools are provided.
		// Gemini may return empty responses when not forced to use tools,
		// especially with complex tool schemas or when the available tools
		// change between turns (e.g., context has tool calls for tools that
		// are no longer available). Using mode "ANY" ensures Gemini always
		// calls one of the provided tools.
		config.ToolConfig = &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAny,
			},
		}
	}

	// Call Gemini API
	result, err := g.client.Models.GenerateContent(ctx, g.model, contents, config)
	if err != nil {
		return llm.CompletionResponse{}, llmerrors.NewError(llmerrors.ErrorTypeUnknown, fmt.Sprintf("Gemini API call failed: %v", err))
	}

	if result == nil {
		return llm.CompletionResponse{}, llmerrors.NewError(llmerrors.ErrorTypeUnknown, "empty response from Gemini API")
	}

	// Cache the assistant response with thought signatures for future turns
	if len(result.Candidates) > 0 && result.Candidates[0].Content != nil {
		g.responseCache = append(g.responseCache, result.Candidates[0].Content)
	}

	// Convert response back to our format
	response := llm.CompletionResponse{
		Content:    result.Text(),
		StopReason: getStopReason(result),
	}

	// Extract tool calls if present
	if functionCalls := result.FunctionCalls(); len(functionCalls) > 0 {
		response.ToolCalls = convertFunctionCallsFromGemini(functionCalls)
	}

	return response, nil
}

// Stream implements the llm.LLMClient interface (stub - not used).
//
//nolint:revive,gocritic // ctx and in kept for interface consistency despite being unused
func (g *GeminiClient) Stream(ctx context.Context, in llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	// Streaming not currently used in our system
	return nil, llmerrors.NewError(llmerrors.ErrorTypeUnknown, "streaming not implemented for Gemini client")
}

// GetModelName returns the model name for this client.
func (g *GeminiClient) GetModelName() string {
	return g.model
}

// convertMessagesToGemini converts our message format to Gemini's Content format.
// Returns contents array and optional system instruction.
// responseCache contains cached Gemini responses with thought signatures to preserve.
func convertMessagesToGemini(messages []llm.CompletionMessage, responseCache []*genai.Content) ([]*genai.Content, string, error) {
	if len(messages) == 0 {
		return nil, "", fmt.Errorf("message list cannot be empty")
	}

	var systemInstruction string
	var contents []*genai.Content
	assistantMsgIdx := 0 // Track which assistant message we're processing

	for i := range messages {
		msg := &messages[i]

		// Extract system messages for system instruction
		if msg.Role == llm.RoleSystem {
			if systemInstruction != "" {
				systemInstruction += "\n\n" + msg.Content
			} else {
				systemInstruction = msg.Content
			}
			continue
		}

		// Convert role
		var role string
		switch msg.Role {
		case llm.RoleUser:
			role = "user"
		case llm.RoleAssistant:
			role = "model" // Gemini uses "model" instead of "assistant"
		default:
			return nil, "", fmt.Errorf("unsupported message role: %s", msg.Role)
		}

		// For assistant messages with tool calls, use cached response to preserve thought signatures
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 && assistantMsgIdx < len(responseCache) {
			// Use the cached Gemini response directly (includes thought signatures)
			contents = append(contents, responseCache[assistantMsgIdx])
			assistantMsgIdx++
			continue
		}

		// Count assistant messages without tool calls too
		if msg.Role == llm.RoleAssistant {
			assistantMsgIdx++
		}

		// Build parts for this message
		var parts []*genai.Part

		// Add text content
		if msg.Content != "" {
			parts = append(parts, &genai.Part{Text: msg.Content})
		}

		// Add tool calls (from assistant messages)
		if len(msg.ToolCalls) > 0 {
			for j := range msg.ToolCalls {
				tc := &msg.ToolCalls[j]
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: tc.Name,
						Args: tc.Parameters,
						ID:   tc.ID,
					},
				})
			}
		}

		// Add tool results (from user messages)
		if len(msg.ToolResults) > 0 {
			for j := range msg.ToolResults {
				tr := &msg.ToolResults[j]
				// Gemini requires function name in FunctionResponse.Name field
				// ToolCallID contains the tool name for Gemini (since Gemini doesn't use IDs)
				if tr.ToolCallID == "" {
					// Skip tool results with empty names (shouldn't happen but be defensive)
					continue
				}
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name: tr.ToolCallID, // Contains tool name for Gemini
						Response: map[string]interface{}{
							"content":  tr.Content,
							"is_error": tr.IsError,
						},
					},
				})
			}
		}

		if len(parts) > 0 {
			contents = append(contents, &genai.Content{
				Role:  role,
				Parts: parts,
			})
		}
	}

	return contents, systemInstruction, nil
}

// convertToolsToGemini converts our tool definitions to Gemini's function declarations.
func convertToolsToGemini(toolDefs []tools.ToolDefinition) []*genai.FunctionDeclaration {
	declarations := make([]*genai.FunctionDeclaration, len(toolDefs))

	for i := range toolDefs {
		tool := &toolDefs[i]

		// Convert properties to Gemini schema format
		properties := make(map[string]*genai.Schema)
		//nolint:gocritic // rangeValCopy: Property size acceptable for this use case
		for propName, prop := range tool.InputSchema.Properties {
			properties[propName] = convertPropertyToGeminiSchema(&prop)
		}

		declarations[i] = &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: properties,
				Required:   tool.InputSchema.Required,
			},
		}
	}

	return declarations
}

// convertPropertyToGeminiSchema recursively converts a Property to Gemini schema format.
func convertPropertyToGeminiSchema(prop *tools.Property) *genai.Schema {
	schema := &genai.Schema{
		Description: prop.Description,
	}

	// Convert type
	switch prop.Type {
	case "string":
		schema.Type = genai.TypeString
	case "number":
		schema.Type = genai.TypeNumber
	case "integer":
		schema.Type = genai.TypeInteger
	case "boolean":
		schema.Type = genai.TypeBoolean
	case "array":
		schema.Type = genai.TypeArray
		if prop.Items != nil {
			schema.Items = convertPropertyToGeminiSchema(prop.Items)
		}
	case "object":
		schema.Type = genai.TypeObject
		if prop.Properties != nil {
			properties := make(map[string]*genai.Schema)
			for name, childProp := range prop.Properties {
				if childProp != nil {
					properties[name] = convertPropertyToGeminiSchema(childProp)
				}
			}
			schema.Properties = properties
		}
	default:
		// Default to string for unknown types
		schema.Type = genai.TypeString
	}

	// Add enum if present
	if len(prop.Enum) > 0 {
		schema.Enum = prop.Enum
	}

	return schema
}

// convertFunctionCallsFromGemini converts Gemini function calls to our format.
func convertFunctionCallsFromGemini(calls []*genai.FunctionCall) []llm.ToolCall {
	toolCalls := make([]llm.ToolCall, len(calls))

	for i := range calls {
		call := calls[i]
		// Gemini doesn't provide function call IDs, so use the function name as ID
		// This allows us to match function responses back to calls via ToolCallID
		id := call.ID
		if id == "" {
			id = call.Name
		}
		toolCalls[i] = llm.ToolCall{
			ID:         id,
			Name:       call.Name,
			Parameters: call.Args,
		}
	}

	return toolCalls
}

// getStopReason extracts the stop reason from Gemini response.
func getStopReason(result *genai.GenerateContentResponse) string {
	// Gemini may not provide explicit stop reasons in the same format
	// Default to "end_turn" if generation completed normally
	if result == nil {
		return "unknown"
	}

	// TODO: Extract actual finish reason from Gemini response if available
	// For now, default to successful completion
	return "end_turn"
}
