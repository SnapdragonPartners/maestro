package architect

import (
	"context"
	"fmt"
	"os"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/templates"
)

// MockLLMClient for testing LLM interactions.
type MockLLMClient struct {
	responses map[string]string
	calls     []string
}

func NewMockLLMClient() *MockLLMClient {
	return &MockLLMClient{
		responses: make(map[string]string),
		calls:     make([]string, 0),
	}
}

func (m *MockLLMClient) GenerateResponse(_ context.Context, prompt string) (string, error) {
	m.calls = append(m.calls, prompt)
	// For spec analysis, return a valid JSON response
	if len(m.responses) == 0 {
		return `{
  "analysis": "This is a simple Go web server specification with two endpoints",
  "requirements": [
    {
      "title": "HTTP Server Setup",
      "description": "Create a basic HTTP server in Go that listens on a port and handles incoming requests. The server should be structured to handle multiple endpoints and use the standard library.",
      "acceptance_criteria": [
        "Server starts without errors",
        "Server listens on configurable port",
        "Server handles HTTP requests properly",
        "Server uses Go standard library only"
      ],
      "estimated_points": 2
    },
    {
      "title": "Health Check Endpoint",
      "description": "Implement a /health endpoint that returns a simple 'OK' response for monitoring and health checking purposes.",
      "acceptance_criteria": [
        "GET /health returns 200 status code",
        "Response body contains 'OK'",
        "Content-Type is text/plain",
        "Endpoint responds quickly"
      ],
      "estimated_points": 1
    },
    {
      "title": "Home Page Endpoint",
      "description": "Implement a / endpoint that renders HTML content from a template file called home.html.",
      "acceptance_criteria": [
        "GET / returns 200 status code",
        "Response renders home.html template",
        "Content-Type is text/html",
        "Template rendering works correctly"
      ],
      "estimated_points": 2
    },
    {
      "title": "Testing Suite",
      "description": "Create comprehensive tests using Go's testing package and httptest to verify both endpoints work correctly.",
      "acceptance_criteria": [
        "Tests for /health endpoint",
        "Tests for / endpoint",
        "Tests verify status codes and content",
        "Tests use httptest package",
        "All tests pass"
      ],
      "estimated_points": 2
    },
    {
      "title": "Build System",
      "description": "Create a Makefile with targets for building, running, and testing the application.",
      "acceptance_criteria": [
        "Makefile has build target",
        "Makefile has run target", 
        "Makefile has test target",
        "All targets work correctly"
      ],
      "estimated_points": 1
    }
  ],
  "next_action": "Generate story files for each requirement"
}`, nil
	}

	// Return first available response or error
	for key, response := range m.responses {
		delete(m.responses, key)
		return response, nil
	}

	return "", fmt.Errorf("no mock response available")
}

func (m *MockLLMClient) SetResponse(response string) {
	m.responses["default"] = response
}

func (m *MockLLMClient) GetCalls() []string {
	return m.calls
}

// AgentLLMClientAdapter adapts agent.LLMClient to architect.LLMClient.
type AgentLLMClientAdapter struct {
	client agent.LLMClient
}

// GenerateResponse implements architect.LLMClient interface.
func (a *AgentLLMClientAdapter) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	req := agent.CompletionRequest{
		Messages: []agent.CompletionMessage{
			{Role: agent.RoleUser, Content: prompt},
		},
		MaxTokens:   2000,
		Temperature: 0.7,
	}

	resp, err := a.client.Complete(ctx, req)
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

func TestSpecToStoryConversionWithLiveAPI(t *testing.T) {
	// Skip if no API key available
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping live API test")
	}

	// Read the helloworld spec
	specContent, err := os.ReadFile("../../tests/fixtures/helloworld_spec.md")
	if err != nil {
		t.Fatalf("Failed to read spec file: %v", err)
	}

	// Create a real O3 LLM client and wrap it with adapter
	baseLLM := agent.NewO3ClientWithModel(apiKey, "o3-mini")
	realLLM := &AgentLLMClientAdapter{client: baseLLM}

	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create mock state store
	// No state store needed for tests

	// Create model config
	modelConfig := &config.ModelCfg{
		APIKey:             apiKey,
		MaxTokensPerMinute: 1000,
		MaxContextTokens:   4000,
		MaxReplyTokens:     1000,
	}

	// Create template renderer
	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create template renderer: %v", err)
	}

	// Create persistence channel for testing
	persistenceChannel := make(chan *persistence.Request, 10)

	// Create driver instance with real LLM
	driver := &Driver{
		architectID:        "test-architect",
		contextManager:     contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:       StateWaiting,
		stateData:          make(map[string]any),
		llmClient:          realLLM,
		renderer:           renderer,
		workDir:            tempDir,
		storiesDir:         tempDir + "/stories",
		logger:             logx.NewLogger("test-architect"),
		persistenceChannel: persistenceChannel,
	}

	// Test the raw LLM response first to see what we're getting
	t.Run("TestRawLLMResponse", func(t *testing.T) {
		ctx := context.Background()

		// Create template data for spec analysis
		templateData := &templates.TemplateData{
			TaskContent: string(specContent),
			Extra: map[string]any{
				"spec_file_path": "helloworld_spec.md",
				"mode":           "llm_analysis",
			},
		}

		prompt, err := driver.renderer.RenderWithUserInstructions(templates.SpecAnalysisTemplate, templateData, driver.workDir, "ARCHITECT")
		if err != nil {
			t.Fatalf("Failed to render spec analysis template: %v", err)
		}

		t.Logf("Generated prompt length: %d characters", len(prompt))

		// Get raw LLM response
		t.Logf("Calling real O3 API to get raw response...")
		rawResponse, err := driver.llmClient.GenerateResponse(ctx, prompt)
		if err != nil {
			t.Fatalf("Real LLM call failed: %v", err)
		}

		t.Logf("Raw LLM response length: %d characters", len(rawResponse))
		t.Logf("Raw LLM response:\n%s", rawResponse)

		// Now try to parse it
		requirements, err := driver.parseSpecAnalysisJSON(rawResponse)
		if err != nil {
			t.Logf("Failed to parse LLM response: %v", err)
			t.Logf("This is the error we're seeing in the logs!")
			return
		}

		t.Logf("Successfully parsed %d requirements from raw response", len(requirements))
	})

	// Test the LLM spec parsing with real API
	t.Run("TestRealLLMSpecParsing", func(t *testing.T) {
		ctx := context.Background()

		t.Logf("Calling real O3 API for spec analysis...")
		requirements, err := driver.parseSpecWithLLM(ctx, string(specContent), "helloworld_spec.md")
		if err != nil {
			t.Fatalf("Real LLM spec parsing failed: %v", err)
		}

		if len(requirements) == 0 {
			t.Fatal("No requirements generated from spec by real LLM")
		}

		t.Logf("Real LLM generated %d requirements:", len(requirements))
		for i, req := range requirements {
			t.Logf("  %d. %s (points: %d)", i+1, req.Title, req.EstimatedPoints)
			t.Logf("     Description: %s", req.Description)
			t.Logf("     Acceptance Criteria: %v", req.AcceptanceCriteria)
			if len(req.Dependencies) > 0 {
				t.Logf("     Dependencies: %v", req.Dependencies)
			}
		}

		// Test story generation from real requirements
		specID, storyIDs, err := driver.generateStoriesFromRequirements(requirements, string(specContent))
		if err != nil {
			t.Fatalf("Story generation from real requirements failed: %v", err)
		}

		t.Logf("Generated spec ID: %s", specID)
		t.Logf("Generated %d story IDs: %v", len(storyIDs), storyIDs)

		// Verify story content quality
		for i, req := range requirements {
			story := driver.requirementToStory(storyIDs[i], specID, &req)
			t.Logf("\nStory %d content:\n%s", i+1, story.Content)

			// Verify story has detailed content (not just template)
			if len(story.Content) < 100 {
				t.Errorf("Story %d content seems too short: %d characters", i+1, len(story.Content))
			}
			if req.Description != "" && len(story.Content) < len(req.Description)+50 {
				t.Errorf("Story %d content doesn't seem to include the full description", i+1)
			}
		}
	})
}

func TestSpecToStoryConversionMocked(t *testing.T) {
	// Read the helloworld spec
	specContent, err := os.ReadFile("../../tests/fixtures/helloworld_spec.md")
	if err != nil {
		t.Fatalf("Failed to read spec file: %v", err)
	}

	// Create mock dependencies
	mockLLM := NewMockLLMClient()

	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create mock state store
	// No state store needed for tests

	// Create mock model config
	modelConfig := &config.ModelCfg{
		APIKey:             "test-key",
		MaxTokensPerMinute: 1000,
		MaxContextTokens:   4000,
		MaxReplyTokens:     1000,
	}

	// Create template renderer
	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create template renderer: %v", err)
	}

	// Create persistence channel for testing
	persistenceChannel := make(chan *persistence.Request, 10)

	// Create driver instance
	driver := &Driver{
		architectID:        "test-architect",
		contextManager:     contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:       StateWaiting,
		stateData:          make(map[string]any),
		llmClient:          mockLLM,
		renderer:           renderer,
		workDir:            tempDir,
		storiesDir:         tempDir + "/stories",
		logger:             logx.NewLogger("test-architect"),
		persistenceChannel: persistenceChannel,
	}

	// Test the LLM spec parsing
	t.Run("TestLLMSpecParsing", func(t *testing.T) {
		ctx := context.Background()

		requirements, err := driver.parseSpecWithLLM(ctx, string(specContent), "test-spec.md")
		if err != nil {
			t.Fatalf("LLM spec parsing failed: %v", err)
		}

		if len(requirements) == 0 {
			t.Fatal("No requirements generated from spec")
		}

		t.Logf("Generated %d requirements:", len(requirements))
		for i, req := range requirements {
			t.Logf("  %d. %s (points: %d)", i+1, req.Title, req.EstimatedPoints)
			t.Logf("     Description: %s", req.Description)
			t.Logf("     Acceptance Criteria: %v", req.AcceptanceCriteria)
		}
	})

	// Test story generation from requirements
	t.Run("TestStoryGeneration", func(t *testing.T) {
		// First get requirements
		ctx := context.Background()
		requirements, err := driver.parseSpecWithLLM(ctx, string(specContent), "test-spec.md")
		if err != nil {
			t.Fatalf("Failed to get requirements: %v", err)
		}

		// Test story generation
		specID, storyIDs, err := driver.generateStoriesFromRequirements(requirements, string(specContent))
		if err != nil {
			t.Fatalf("Story generation failed: %v", err)
		}

		if specID == "" {
			t.Fatal("No spec ID generated")
		}

		if len(storyIDs) != len(requirements) {
			t.Fatalf("Expected %d story IDs, got %d", len(requirements), len(storyIDs))
		}

		t.Logf("Generated spec ID: %s", specID)
		t.Logf("Generated %d story IDs: %v", len(storyIDs), storyIDs)

		// Check that persistence requests were sent
		persistenceRequestCount := 0
		timeout := 0
		for len(persistenceChannel) > 0 && timeout < 100 {
			req := <-persistenceChannel
			persistenceRequestCount++
			t.Logf("Persistence request: %s", req.Operation)
			timeout++
		}

		expectedRequests := 1 + len(requirements) + 1 // spec + stories + spec update
		if persistenceRequestCount != expectedRequests {
			t.Logf("Expected %d persistence requests, got %d", expectedRequests, persistenceRequestCount)
		}
	})

	// Test with malformed JSON response
	t.Run("TestMalformedJSONHandling", func(t *testing.T) {
		malformedLLM := NewMockLLMClient()
		malformedLLM.SetResponse("This is not JSON at all")

		driver.llmClient = malformedLLM

		ctx := context.Background()
		_, err := driver.parseSpecWithLLM(ctx, string(specContent), "test-spec.md")
		if err == nil {
			t.Fatal("Expected error for malformed JSON, but got none")
		}

		t.Logf("Correctly caught malformed JSON error: %v", err)
	})

	// Test with truncated JSON response
	t.Run("TestTruncatedJSONHandling", func(t *testing.T) {
		truncatedLLM := NewMockLLMClient()
		truncatedLLM.SetResponse(`{
  "analysis": "This response is truncated"`)

		driver.llmClient = truncatedLLM

		ctx := context.Background()
		_, err := driver.parseSpecWithLLM(ctx, string(specContent), "test-spec.md")
		if err == nil {
			t.Fatal("Expected error for truncated JSON, but got none")
		}

		t.Logf("Correctly caught truncated JSON error: %v", err)
	})
}

// Test the actual LLM response parsing logic in isolation.
func TestParseSpecAnalysisJSON(t *testing.T) {
	// Create minimal driver for testing
	driver := &Driver{
		logger: logx.NewLogger("test"),
	}

	tests := []struct {
		name        string
		response    string
		expectError bool
		expectCount int
	}{
		{
			name: "ValidJSON",
			response: `{
  "analysis": "Test analysis",
  "requirements": [
    {
      "title": "Test Requirement",
      "description": "Test description",
      "acceptance_criteria": ["Criterion 1", "Criterion 2"],
      "estimated_points": 2
    }
  ],
  "next_action": "Generate stories"
}`,
			expectError: false,
			expectCount: 1,
		},
		{
			name:        "EmptyResponse",
			response:    "",
			expectError: true,
			expectCount: 0,
		},
		{
			name:        "NoJSON",
			response:    "This is just text without JSON",
			expectError: true,
			expectCount: 0,
		},
		{
			name:        "TruncatedJSON",
			response:    `{"analysis": "truncated`,
			expectError: true,
			expectCount: 0,
		},
		{
			name: "JSONWithExtraText",
			response: `Here is the analysis:
{
  "analysis": "Test analysis",
  "requirements": [
    {
      "title": "Test Requirement",
      "description": "Test description", 
      "acceptance_criteria": ["Criterion 1"],
      "estimated_points": 1
    }
  ],
  "next_action": "Generate stories"
}
That's the end of the analysis.`,
			expectError: false,
			expectCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requirements, err := driver.parseSpecAnalysisJSON(tt.response)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if len(requirements) != tt.expectCount {
				t.Errorf("Expected %d requirements, got %d", tt.expectCount, len(requirements))
			}

			if !tt.expectError && len(requirements) > 0 {
				req := requirements[0]
				if req.Title == "" {
					t.Error("First requirement has empty title")
				}
				if req.EstimatedPoints < 1 || req.EstimatedPoints > 5 {
					t.Errorf("Invalid estimated points: %d", req.EstimatedPoints)
				}
			}
		})
	}
}
