package anthropic

import (
	"testing"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/tools"
)

// TestEnsureAlternation tests the message alternation logic.
func TestEnsureAlternation(t *testing.T) {
	tests := []struct {
		name         string
		input        []llm.CompletionMessage
		expectSystem string
		expectMsgLen int
		expectErr    bool
		errContains  string
	}{
		{
			name:        "empty messages",
			input:       []llm.CompletionMessage{},
			expectErr:   true,
			errContains: "message list cannot be empty",
		},
		{
			name: "system message extracted",
			input: []llm.CompletionMessage{
				{Role: llm.RoleSystem, Content: "You are helpful"},
				{Role: llm.RoleUser, Content: "Hello"},
			},
			expectSystem: "You are helpful",
			expectMsgLen: 1,
			expectErr:    false,
		},
		{
			name: "multiple system messages concatenated",
			input: []llm.CompletionMessage{
				{Role: llm.RoleSystem, Content: "You are helpful"},
				{Role: llm.RoleSystem, Content: "And concise"},
				{Role: llm.RoleUser, Content: "Hello"},
			},
			expectSystem: "You are helpful\n\nAnd concise",
			expectMsgLen: 1,
			expectErr:    false,
		},
		{
			name: "proper alternation maintained",
			input: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
				{Role: llm.RoleUser, Content: "How are you?"},
			},
			expectSystem: "",
			expectMsgLen: 3,
			expectErr:    false,
		},
		{
			name: "consecutive user messages merged",
			input: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleUser, Content: "Anyone there?"},
			},
			expectSystem: "",
			expectMsgLen: 1,
			expectErr:    false,
		},
		{
			name: "ends with assistant returns error",
			input: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
			},
			expectErr:   true,
			errContains: "last message must be user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system, msgs, err := ensureAlternation(tt.input)

			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if system != tt.expectSystem {
				t.Errorf("expected system %q, got %q", tt.expectSystem, system)
			}

			if len(msgs) != tt.expectMsgLen {
				t.Errorf("expected %d messages, got %d", tt.expectMsgLen, len(msgs))
			}
		})
	}
}

// TestValidatePreSend tests the pre-send validation logic.
func TestValidatePreSend(t *testing.T) {
	tests := []struct {
		name        string
		messages    []llm.CompletionMessage
		expectErr   bool
		errContains string
	}{
		{
			name: "valid alternating messages",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
				{Role: llm.RoleUser, Content: "Bye"},
			},
			expectErr: false,
		},
		{
			name: "system message in array",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleSystem, Content: "You are helpful"},
			},
			expectErr:   true,
			errContains: "system message found",
		},
		{
			name: "consecutive user messages",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleUser, Content: "Anyone?"},
			},
			expectErr:   true,
			errContains: "alternation violation",
		},
		{
			name: "consecutive assistant messages",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
				{Role: llm.RoleAssistant, Content: "There"},
			},
			expectErr:   true,
			errContains: "alternation violation",
		},
		{
			name: "starts with assistant",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleAssistant, Content: "Hello"},
			},
			expectErr:   true,
			errContains: "first message must be user",
		},
		{
			name: "ends with assistant",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
			},
			expectErr:   true,
			errContains: "last message must be user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePreSend("test-model", tt.messages)

			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestGetModelName tests model name retrieval.
func TestGetModelName(t *testing.T) {
	client := NewClaudeClientWithModel("test-key", "claude-3-opus-20240229")

	modelName := client.GetModelName()

	if modelName != "claude-3-opus-20240229" {
		t.Errorf("expected model %q, got %q", "claude-3-opus-20240229", modelName)
	}
}

// TestNewClaudeClient tests client creation.
func TestNewClaudeClient(t *testing.T) {
	client := NewClaudeClient("test-api-key")

	if client == nil {
		t.Fatal("expected client, got nil")
	}

	// Verify it implements the interface
	var _ llm.LLMClient = client
}

// TestNewClaudeClientWithModel tests client creation with custom model.
func TestNewClaudeClientWithModel(t *testing.T) {
	client := NewClaudeClientWithModel("test-api-key", "claude-3-sonnet-20240229")

	if client == nil {
		t.Fatal("expected client, got nil")
	}

	modelName := client.GetModelName()
	if modelName != "claude-3-sonnet-20240229" {
		t.Errorf("expected model %q, got %q", "claude-3-sonnet-20240229", modelName)
	}
}

// contains is a helper to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && hasSubstring(s, substr)))
}

func hasSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestConvertPropertyToAnthropicSchema verifies that the Anthropic schema serializer
// recursively converts nested Properties, including array items with object schemas,
// enums, required fields, and minItems/maxItems.
// Regression: the old flat serializer dropped Items, Properties, Required, and MinItems,
// causing submit_verification and submit_probing to reach Claude without enum constraints.
func TestConvertPropertyToAnthropicSchema(t *testing.T) {
	t.Run("FlatStringProperty", func(t *testing.T) {
		prop := &tools.Property{
			Type:        "string",
			Description: "A simple string",
		}
		schema := convertPropertyToAnthropicSchema(prop)

		if schema["type"] != "string" {
			t.Errorf("expected type=string, got %v", schema["type"])
		}
		if schema["description"] != "A simple string" {
			t.Errorf("expected description, got %v", schema["description"])
		}
		if _, ok := schema["enum"]; ok {
			t.Error("unexpected enum field")
		}
	})

	t.Run("StringWithEnum", func(t *testing.T) {
		prop := &tools.Property{
			Type:        "string",
			Description: "Confidence level",
			Enum:        []string{"high", "medium", "low"},
		}
		schema := convertPropertyToAnthropicSchema(prop)

		enum, ok := schema["enum"].([]string)
		if !ok {
			t.Fatal("expected enum to be []string")
		}
		if len(enum) != 3 || enum[0] != "high" {
			t.Errorf("unexpected enum: %v", enum)
		}
	})

	t.Run("ArrayOfObjects", func(t *testing.T) {
		// Mirrors the submit_verification schema: array of criterion objects.
		minItems := 1
		prop := &tools.Property{
			Type:        "array",
			Description: "Criteria results",
			MinItems:    &minItems,
			Items: &tools.Property{
				Type: "object",
				Properties: map[string]*tools.Property{
					"criterion": {
						Type:        "string",
						Description: "The criterion",
					},
					"method": {
						Type:        "string",
						Description: "How verified",
						Enum:        []string{"command", "inspection"},
					},
					"result": {
						Type:        "string",
						Description: "Result",
						Enum:        []string{"pass", "fail", "partial", "unverified"},
					},
				},
				Required: []string{"criterion", "method", "result"},
			},
		}
		schema := convertPropertyToAnthropicSchema(prop)

		// Verify top-level array fields.
		if schema["type"] != "array" {
			t.Errorf("expected type=array, got %v", schema["type"])
		}
		if schema["minItems"] != 1 {
			t.Errorf("expected minItems=1, got %v", schema["minItems"])
		}

		// Verify items object was serialized.
		items, ok := schema["items"].(map[string]any)
		if !ok {
			t.Fatal("expected items to be map[string]any")
		}
		if items["type"] != "object" {
			t.Errorf("expected items.type=object, got %v", items["type"])
		}

		// Verify nested properties exist.
		itemProps, ok := items["properties"].(map[string]any)
		if !ok {
			t.Fatal("expected items.properties to be map[string]any")
		}
		if len(itemProps) != 3 {
			t.Errorf("expected 3 properties, got %d", len(itemProps))
		}

		// Verify method enum is preserved.
		method, ok := itemProps["method"].(map[string]any)
		if !ok {
			t.Fatal("expected method to be map[string]any")
		}
		methodEnum, ok := method["enum"].([]string)
		if !ok {
			t.Fatal("expected method.enum to be []string")
		}
		if len(methodEnum) != 2 || methodEnum[0] != "command" || methodEnum[1] != "inspection" {
			t.Errorf("unexpected method enum: %v", methodEnum)
		}

		// Verify result enum is preserved.
		result, ok := itemProps["result"].(map[string]any)
		if !ok {
			t.Fatal("expected result to be map[string]any")
		}
		resultEnum, ok := result["enum"].([]string)
		if !ok {
			t.Fatal("expected result.enum to be []string")
		}
		if len(resultEnum) != 4 {
			t.Errorf("expected 4 result enum values, got %d", len(resultEnum))
		}

		// Verify required is preserved at the items level.
		required, ok := items["required"].([]string)
		if !ok {
			t.Fatal("expected items.required to be []string")
		}
		if len(required) != 3 {
			t.Errorf("expected 3 required fields, got %d", len(required))
		}
	})

	t.Run("NestedObjectWithoutArray", func(t *testing.T) {
		// Object property with nested properties (not inside an array).
		prop := &tools.Property{
			Type:        "object",
			Description: "A nested object",
			Properties: map[string]*tools.Property{
				"name": {
					Type:        "string",
					Description: "Name field",
				},
			},
			Required: []string{"name"},
		}
		schema := convertPropertyToAnthropicSchema(prop)

		childProps, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatal("expected properties to be map[string]any")
		}
		if _, hasName := childProps["name"]; !hasName {
			t.Error("expected name property")
		}
		required, ok := schema["required"].([]string)
		if !ok {
			t.Fatal("expected required to be []string")
		}
		if len(required) != 1 || required[0] != "name" {
			t.Errorf("unexpected required: %v", required)
		}
	})

	t.Run("MaxItemsPresent", func(t *testing.T) {
		maxItems := 10
		prop := &tools.Property{
			Type:     "array",
			MaxItems: &maxItems,
			Items: &tools.Property{
				Type: "string",
			},
		}
		schema := convertPropertyToAnthropicSchema(prop)

		if schema["maxItems"] != 10 {
			t.Errorf("expected maxItems=10, got %v", schema["maxItems"])
		}
	})

	// Integration-style: verify submit_verification Definition() round-trips correctly.
	t.Run("SubmitVerificationFullSchema", func(t *testing.T) {
		tool := tools.NewSubmitVerificationTool()
		def := tool.Definition()

		// Convert the top-level properties through our serializer.
		result := make(map[string]any)
		for name := range def.InputSchema.Properties {
			prop := def.InputSchema.Properties[name]
			result[name] = convertPropertyToAnthropicSchema(&prop)
		}

		// Verify acceptance_criteria_checked has nested items with enum.
		acc, ok := result["acceptance_criteria_checked"].(map[string]any)
		if !ok {
			t.Fatal("expected acceptance_criteria_checked")
		}
		items, ok := acc["items"].(map[string]any)
		if !ok {
			t.Fatal("expected items in acceptance_criteria_checked")
		}
		props, ok := items["properties"].(map[string]any)
		if !ok {
			t.Fatal("expected properties in items")
		}

		// The method enum must survive serialization.
		method, ok := props["method"].(map[string]any)
		if !ok {
			t.Fatal("expected method property in items")
		}
		if _, ok := method["enum"]; !ok {
			t.Error("method enum was dropped during serialization — this was the production bug")
		}
	})
}
