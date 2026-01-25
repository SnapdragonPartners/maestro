package ollama

import (
	"testing"

	"github.com/ollama/ollama/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/tools"
)

// makeToolCallArgs creates a ToolCallFunctionArguments from a map for testing.
func makeToolCallArgs(m map[string]any) api.ToolCallFunctionArguments {
	args := api.NewToolCallFunctionArguments()
	for k, v := range m {
		args.Set(k, v)
	}
	return args
}

func TestNewOllamaClientWithModel(t *testing.T) {
	tests := []struct {
		name    string
		hostURL string
		model   string
	}{
		{
			name:    "valid host and model",
			hostURL: "http://localhost:11434",
			model:   "phi4:latest",
		},
		{
			name:    "custom host",
			hostURL: "http://192.168.1.100:11434",
			model:   "llama3.1:8b",
		},
		{
			name:    "invalid URL falls back to default",
			hostURL: "not-a-valid-url",
			model:   "mistral:7b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewOllamaClientWithModel(tt.hostURL, tt.model)
			require.NotNil(t, client)
			assert.Equal(t, tt.model, client.GetModelName())
		})
	}
}

func TestConvertMessagesToOllama(t *testing.T) {
	tests := []struct {
		name     string
		messages []llm.CompletionMessage
		wantLen  int
		wantErr  bool
	}{
		{
			name:     "empty messages returns error",
			messages: []llm.CompletionMessage{},
			wantErr:  true,
		},
		{
			name: "single user message",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
			},
			wantLen: 1,
		},
		{
			name: "system and user messages",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleSystem, Content: "You are helpful"},
				{Role: llm.RoleUser, Content: "Hello"},
			},
			wantLen: 2,
		},
		{
			name: "message with tool calls",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "What's the weather?"},
				{
					Role:    llm.RoleAssistant,
					Content: "",
					ToolCalls: []llm.ToolCall{
						{
							ID:         "call_1",
							Name:       "get_weather",
							Parameters: map[string]any{"location": "NYC"},
						},
					},
				},
			},
			wantLen: 2,
		},
		{
			name: "message with tool results",
			messages: []llm.CompletionMessage{
				{
					Role: llm.RoleUser,
					ToolResults: []llm.ToolResult{
						{
							ToolCallID: "call_1",
							Content:    "Sunny, 72F",
							IsError:    false,
						},
					},
				},
			},
			wantLen: 1, // Tool results become separate "tool" role messages
		},
		{
			name: "tool results with additional content",
			messages: []llm.CompletionMessage{
				{
					Role:    llm.RoleUser,
					Content: "Here's the result",
					ToolResults: []llm.ToolResult{
						{
							ToolCallID: "call_1",
							Content:    "Sunny, 72F",
						},
					},
				},
			},
			wantLen: 2, // One "tool" message + one user message with content
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertMessagesToOllama(tt.messages)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, result, tt.wantLen)
		})
	}
}

func TestConvertMessagesToOllama_RoleMapping(t *testing.T) {
	messages := []llm.CompletionMessage{
		{Role: llm.RoleSystem, Content: "System prompt"},
		{Role: llm.RoleUser, Content: "User message"},
		{Role: llm.RoleAssistant, Content: "Assistant response"},
	}

	result, err := convertMessagesToOllama(messages)
	require.NoError(t, err)
	require.Len(t, result, 3)

	assert.Equal(t, "system", result[0].Role)
	assert.Equal(t, "user", result[1].Role)
	assert.Equal(t, "assistant", result[2].Role)
}

func TestConvertToolsToOllama(t *testing.T) {
	toolDefs := []tools.ToolDefinition{
		{
			Name:        "get_weather",
			Description: "Get weather for a location",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"location": {
						Type:        "string",
						Description: "City name",
					},
					"unit": {
						Type:        "string",
						Description: "Temperature unit",
						Enum:        []string{"celsius", "fahrenheit"},
					},
				},
				Required: []string{"location"},
			},
		},
	}

	result := convertToolsToOllama(toolDefs)
	require.Len(t, result, 1)

	tool := result[0]
	assert.Equal(t, "function", tool.Type)
	assert.Equal(t, "get_weather", tool.Function.Name)
	assert.Equal(t, "Get weather for a location", tool.Function.Description)
	assert.Equal(t, "object", tool.Function.Parameters.Type)
	// Check properties exist using Get method
	_, hasLocation := tool.Function.Parameters.Properties.Get("location")
	_, hasUnit := tool.Function.Parameters.Properties.Get("unit")
	assert.True(t, hasLocation, "should have location property")
	assert.True(t, hasUnit, "should have unit property")
	assert.Equal(t, []string{"location"}, tool.Function.Parameters.Required)

	// Check enum conversion
	unitProp, _ := tool.Function.Parameters.Properties.Get("unit")
	assert.Len(t, unitProp.Enum, 2)
}

func TestConvertPropertyToOllama(t *testing.T) {
	tests := []struct {
		name     string
		prop     tools.Property
		wantType string
		wantDesc string
		wantEnum int
	}{
		{
			name: "simple string property",
			prop: tools.Property{
				Type:        "string",
				Description: "A string value",
			},
			wantType: "string",
			wantDesc: "A string value",
		},
		{
			name: "property with enum",
			prop: tools.Property{
				Type:        "string",
				Description: "A choice",
				Enum:        []string{"a", "b", "c"},
			},
			wantType: "string",
			wantDesc: "A choice",
			wantEnum: 3,
		},
		{
			name: "integer property",
			prop: tools.Property{
				Type:        "integer",
				Description: "A number",
			},
			wantType: "integer",
			wantDesc: "A number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertPropertyToOllama(&tt.prop)
			assert.Equal(t, api.PropertyType{tt.wantType}, result.Type)
			assert.Equal(t, tt.wantDesc, result.Description)
			assert.Len(t, result.Enum, tt.wantEnum)
		})
	}
}

func TestConvertToolCallsFromOllama(t *testing.T) {
	tests := []struct {
		name  string
		calls []api.ToolCall
		want  []llm.ToolCall
	}{
		{
			name:  "empty calls",
			calls: []api.ToolCall{},
			want:  []llm.ToolCall{},
		},
		{
			name: "single call with ID",
			calls: []api.ToolCall{
				{
					ID: "call_abc123",
					Function: api.ToolCallFunction{
						Name:      "get_weather",
						Arguments: makeToolCallArgs(map[string]any{"location": "NYC"}),
					},
				},
			},
			want: []llm.ToolCall{
				{
					ID:         "call_abc123",
					Name:       "get_weather",
					Parameters: map[string]any{"location": "NYC"},
				},
			},
		},
		{
			name: "call without ID gets generated",
			calls: []api.ToolCall{
				{
					Function: api.ToolCallFunction{
						Name:      "search",
						Arguments: makeToolCallArgs(map[string]any{"query": "test"}),
					},
				},
			},
			want: []llm.ToolCall{
				{
					ID:         "call_0",
					Name:       "search",
					Parameters: map[string]any{"query": "test"},
				},
			},
		},
		{
			name: "multiple calls",
			calls: []api.ToolCall{
				{
					ID: "call_1",
					Function: api.ToolCallFunction{
						Name:      "tool_a",
						Arguments: makeToolCallArgs(map[string]any{"a": 1}),
					},
				},
				{
					ID: "call_2",
					Function: api.ToolCallFunction{
						Name:      "tool_b",
						Arguments: makeToolCallArgs(map[string]any{"b": 2}),
					},
				},
			},
			want: []llm.ToolCall{
				{ID: "call_1", Name: "tool_a", Parameters: map[string]any{"a": 1}},
				{ID: "call_2", Name: "tool_b", Parameters: map[string]any{"b": 2}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToolCallsFromOllama(tt.calls)
			require.Len(t, result, len(tt.want))
			for i, want := range tt.want {
				assert.Equal(t, want.ID, result[i].ID)
				assert.Equal(t, want.Name, result[i].Name)
				assert.Equal(t, want.Parameters, result[i].Parameters)
			}
		})
	}
}

func TestGetStopReason(t *testing.T) {
	tests := []struct {
		name       string
		resp       api.ChatResponse
		wantReason string
	}{
		{
			name:       "not done",
			resp:       api.ChatResponse{Done: false},
			wantReason: "incomplete",
		},
		{
			name:       "done with stop",
			resp:       api.ChatResponse{Done: true, DoneReason: "stop"},
			wantReason: "end_turn",
		},
		{
			name:       "done with length",
			resp:       api.ChatResponse{Done: true, DoneReason: "length"},
			wantReason: "max_tokens",
		},
		{
			name:       "done with empty reason",
			resp:       api.ChatResponse{Done: true, DoneReason: ""},
			wantReason: "end_turn",
		},
		{
			name:       "done with custom reason",
			resp:       api.ChatResponse{Done: true, DoneReason: "custom_reason"},
			wantReason: "custom_reason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStopReason(&tt.resp)
			assert.Equal(t, tt.wantReason, result)
		})
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name        string
		errMsg      string
		wantContain string
	}{
		{
			name:        "nil error",
			errMsg:      "",
			wantContain: "",
		},
		{
			name:        "connection refused",
			errMsg:      "dial tcp: connection refused",
			wantContain: "not reachable",
		},
		{
			name:        "model not found",
			errMsg:      "model 'xyz' not found",
			wantContain: "not found",
		},
		{
			name:        "context canceled",
			errMsg:      "context canceled",
			wantContain: "canceled",
		},
		{
			name:        "timeout",
			errMsg:      "request timeout exceeded",
			wantContain: "timeout",
		},
		{
			name:        "unknown error",
			errMsg:      "something unexpected happened",
			wantContain: "API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var inputErr error
			if tt.errMsg != "" {
				inputErr = &testError{msg: tt.errMsg}
			}

			result := classifyError(inputErr)

			if tt.wantContain == "" {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Contains(t, result.Error(), tt.wantContain)
			}
		})
	}
}

// testError is a simple error type for testing.
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
