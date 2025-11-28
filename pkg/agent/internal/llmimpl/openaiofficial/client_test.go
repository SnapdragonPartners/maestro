package openaiofficial

import (
	"testing"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/tools"
)

// TestNewOfficialClient tests client creation with default model.
func TestNewOfficialClient(t *testing.T) {
	client := NewOfficialClient("test-api-key")

	if client == nil {
		t.Fatal("expected client, got nil")
	}

	// Verify it implements the interface
	var _ llm.LLMClient = client
}

// TestNewOfficialClientWithModel tests client creation with custom model.
func TestNewOfficialClientWithModel(t *testing.T) {
	client := NewOfficialClientWithModel("test-api-key", "gpt-4o")

	if client == nil {
		t.Fatal("expected client, got nil")
	}

	modelName := client.GetModelName()
	if modelName != "gpt-4o" {
		t.Errorf("expected model %q, got %q", "gpt-4o", modelName)
	}
}

// TestGetModelName tests model name retrieval.
func TestGetModelName(t *testing.T) {
	client := NewOfficialClientWithModel("test-key", "o3")

	modelName := client.GetModelName()

	if modelName != "o3" {
		t.Errorf("expected model %q, got %q", "o3", modelName)
	}
}

// TestConvertPropertyToSchema tests property to schema conversion.
func TestConvertPropertyToSchema(t *testing.T) {
	tests := []struct {
		name     string
		property tools.Property
		wantType string
		hasEnum  bool
		hasItems bool
	}{
		{
			name: "simple string",
			property: tools.Property{
				Type:        "string",
				Description: "A string value",
			},
			wantType: "string",
			hasEnum:  false,
		},
		{
			name: "string with enum",
			property: tools.Property{
				Type:        "string",
				Description: "Color choice",
				Enum:        []string{"red", "green", "blue"},
			},
			wantType: "string",
			hasEnum:  true,
		},
		{
			name: "array type",
			property: tools.Property{
				Type:        "array",
				Description: "List of items",
				Items: &tools.Property{
					Type:        "string",
					Description: "Item",
				},
			},
			wantType: "array",
			hasItems: true,
		},
		{
			name: "number type",
			property: tools.Property{
				Type:        "number",
				Description: "A number",
			},
			wantType: "number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := convertPropertyToSchema(&tt.property)

			if schema["type"] != tt.wantType {
				t.Errorf("expected type %q, got %v", tt.wantType, schema["type"])
			}

			if schema["description"] != tt.property.Description {
				t.Errorf("expected description %q, got %v", tt.property.Description, schema["description"])
			}

			if tt.hasEnum {
				if _, ok := schema["enum"]; !ok {
					t.Error("expected enum field to be set")
				}
			}

			if tt.hasItems {
				if _, ok := schema["items"]; !ok {
					t.Error("expected items field to be set")
				}
			}
		})
	}
}

