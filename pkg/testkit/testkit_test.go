package testkit

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"orchestrator/pkg/proto"
)

func TestMockAnthropicServer(t *testing.T) {
	server := MockAnthropicServer()
	defer server.Close()

	// Test health endpoint request
	requestBody := `{
		"model": "claude-3-sonnet-20240229",
		"messages": [
			{
				"role": "user",
				"content": [
					{
						"type": "text",
						"text": "Create a health endpoint"
					}
				]
			}
		],
		"max_tokens": 1000
	}`

	resp, err := http.Post(server.URL+"/messages", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	// Verify response structure
	if response["type"] != "message" {
		t.Errorf("Expected type 'message', got %v", response["type"])
	}

	content, ok := response["content"].([]any)
	if !ok || len(content) == 0 {
		t.Error("Expected content array")
	}

	firstContent, ok := content[0].(map[string]any)
	if !ok {
		t.Error("Expected content[0] to be a map")
	}

	text, ok := firstContent["text"].(string)
	if !ok {
		t.Error("Expected text field")
	}

	if !strings.Contains(text, "health") {
		t.Error("Expected health-related code in response")
	}
}

func TestMockOpenAIServer(t *testing.T) {
	server := MockOpenAIServer()
	defer server.Close()

	requestBody := `{
		"model": "gpt-4",
		"messages": [
			{
				"role": "user",
				"content": "Create a task for implementing a health endpoint"
			}
		],
		"max_tokens": 500
	}`

	resp, err := http.Post(server.URL+"/chat/completions", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	// Verify response structure
	if response["object"] != "chat.completion" {
		t.Errorf("Expected object 'chat.completion', got %v", response["object"])
	}

	choices, ok := response["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Error("Expected choices array")
	}

	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		t.Error("Expected choices[0] to be a map")
	}

	message, ok := firstChoice["message"].(map[string]any)
	if !ok {
		t.Error("Expected message field")
	}

	content, ok := message["content"].(string)
	if !ok {
		t.Error("Expected content field")
	}

	if !strings.Contains(strings.ToLower(content), "health") {
		t.Error("Expected health-related task in response")
	}
}

func TestMessageHelpers(t *testing.T) {
	// Test story message creation
	storyMsg := NewStoryMessage("architect", "claude").
		WithContent("Create a health endpoint").
		WithStoryID("001").
		WithRequirements([]string{"GET /health", "JSON response"}).
		WithMetadata("story_type", "health").
		Build()

	AssertMessageType(t, storyMsg, proto.MsgTypeSTORY)
	AssertMessageFromAgent(t, storyMsg, "architect")
	AssertMessageToAgent(t, storyMsg, "claude")
	AssertPayloadString(t, storyMsg, "content", "Create a health endpoint")
	AssertPayloadString(t, storyMsg, "story_id", "001")
	AssertMetadataValue(t, storyMsg, "story_type", "health")

	// Test result message creation
	resultMsg := NewResultMessage("claude", "architect").
		WithStatus("completed").
		WithImplementation("package main\nfunc main() {}").
		WithTestResults(true, "All tests passed").
		Build()

	AssertMessageType(t, resultMsg, proto.MsgTypeRESULT)
	AssertPayloadString(t, resultMsg, "status", "completed")
	AssertTestResults(t, resultMsg, true)
	AssertPayloadExists(t, resultMsg, "implementation")

	// Test error message creation
	errorMsg := NewErrorMessage("claude", "architect").
		WithError("Compilation failed").
		WithMetadata("error_type", "build_error").
		Build()

	AssertMessageType(t, errorMsg, proto.MsgTypeERROR)
	AssertPayloadString(t, errorMsg, "error", "Compilation failed")
	AssertMetadataValue(t, errorMsg, "error_type", "build_error")
}

func TestPredefinedMessages(t *testing.T) {
	// Test health endpoint story
	healthStory := HealthEndpointTask("architect", "claude")
	AssertMessageType(t, healthStory, proto.MsgTypeSTORY)
	AssertPayloadContains(t, healthStory, "content", "health")
	AssertPayloadExists(t, healthStory, "requirements")

	// Test successful code result
	implementation := `package main
import "net/http"
func healthHandler(w http.ResponseWriter, r *http.Request) {}
func main() { http.HandleFunc("/health", healthHandler) }`

	successResult := SuccessfulCodeResult("claude", "architect", implementation)
	AssertMessageType(t, successResult, proto.MsgTypeRESULT)
	AssertPayloadString(t, successResult, "status", "completed")
	AssertTestResults(t, successResult, true)

	// Test failed code result
	failedResult := FailedCodeResult("claude", "architect", "Build failed")
	AssertMessageType(t, failedResult, proto.MsgTypeERROR)
	AssertPayloadString(t, failedResult, "error", "Build failed")

	// Test shutdown acknowledgment
	shutdownAck := ShutdownAcknowledgment("claude", "orchestrator")
	AssertMessageType(t, shutdownAck, proto.MsgTypeRESULT)
	AssertPayloadString(t, shutdownAck, "status", "shutdown_acknowledged")
}

func TestAssertions(t *testing.T) {
	// Create a test message for assertion testing
	msg := NewResultMessage("claude", "architect").
		WithStatus("completed").
		WithImplementation(`package main
import (
	"encoding/json"
	"net/http"
	"time"
)
type HealthResponse struct {
	Status    string    `+"`json:\"status\"`"+`
	Timestamp time.Time `+"`json:\"timestamp\"`"+`
}
func healthHandler(w http.ResponseWriter, r *http.Request) {
	response := HealthResponse{Status: "healthy", Timestamp: time.Now()}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
func main() {
	http.HandleFunc("/health", healthHandler)
}`).
		WithTestResults(true, "All checks passed").
		Build()

	// Test all assertion types
	AssertMessageType(t, msg, proto.MsgTypeRESULT)
	AssertMessageFromAgent(t, msg, "claude")
	AssertMessageToAgent(t, msg, "architect")
	AssertPayloadExists(t, msg, "status")
	AssertPayloadString(t, msg, "status", "completed")
	AssertTestResults(t, msg, true)
	AssertCodeCompiles(t, msg)
	AssertHealthEndpointCode(t, msg)
	AssertNoAPICallsMade(t, msg)

	// Test lint/test conditions
	AssertLintTestConditions(t, msg, LintTestConditions{
		ShouldPass: true,
	})
}

func TestMessageFlow(t *testing.T) {
	// Create a sequence of messages
	story := HealthEndpointTask("architect", "claude")
	result := SuccessfulCodeResult("claude", "architect", "mock implementation")
	shutdown := NewShutdownMessage("orchestrator", "claude").Build()
	ack := ShutdownAcknowledgment("claude", "orchestrator")

	messages := []*proto.AgentMsg{story, result, shutdown, ack}
	expectedFlow := []proto.MsgType{
		proto.MsgTypeSTORY,
		proto.MsgTypeRESULT,
		proto.MsgTypeSHUTDOWN,
		proto.MsgTypeRESULT,
	}

	AssertValidMessageFlow(t, messages, expectedFlow)
}
