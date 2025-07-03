package testkit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
)

// MockAnthropicServer creates an httptest server that emulates Anthropic Claude API
func MockAnthropicServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a messages endpoint
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		// Verify method
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse the request to understand what's being asked
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
			MaxTokens   int     `json:"max_tokens"`
			Temperature float32 `json:"temperature,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Generate mock response based on the prompt content
		var generatedText string
		if len(request.Messages) > 0 && len(request.Messages[0].Content) > 0 {
			prompt := request.Messages[0].Content[0].Text
			generatedText = generateMockCode(prompt)
		} else {
			generatedText = generateDefaultMockCode()
		}

		// Create mock Anthropic response
		response := map[string]any{
			"id":    "msg_mock_12345",
			"type":  "message",
			"role":  "assistant",
			"model": request.Model,
			"content": []map[string]any{
				{
					"type": "text",
					"text": generatedText,
				},
			},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  100,
				"output_tokens": 200,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
}

// MockOpenAIServer creates an httptest server that emulates OpenAI API
func MockOpenAIServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a chat completions endpoint
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		// Verify method
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse the request
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens   int     `json:"max_tokens,omitempty"`
			Temperature float64 `json:"temperature,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Generate mock task based on the prompt content
		var generatedContent string
		if len(request.Messages) > 0 {
			prompt := request.Messages[len(request.Messages)-1].Content
			generatedContent = generateMockTask(prompt)
		} else {
			generatedContent = generateDefaultMockTask()
		}

		// Create mock OpenAI response
		response := map[string]any{
			"id":      "chatcmpl-mock12345",
			"object":  "chat.completion",
			"created": 1699999999,
			"model":   request.Model,
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": generatedContent,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     50,
				"completion_tokens": 100,
				"total_tokens":      150,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
}

func generateMockCode(prompt string) string {
	promptLower := strings.ToLower(prompt)

	if strings.Contains(promptLower, "health") {
		return `package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type HealthResponse struct {
	Status    string    ` + "`json:\"status\"`" + `
	Timestamp time.Time ` + "`json:\"timestamp\"`" + `
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	response := HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func main() {
	http.HandleFunc("/health", healthHandler)
	http.ListenAndServe(":8080", nil)
}`
	}

	if strings.Contains(promptLower, "user") || strings.Contains(promptLower, "api") {
		return `package main

import (
	"encoding/json"
	"net/http"
)

type User struct {
	ID   int    ` + "`json:\"id\"`" + `
	Name string ` + "`json:\"name\"`" + `
}

func createUser(w http.ResponseWriter, r *http.Request) {
	var user User
	json.NewDecoder(r.Body).Decode(&user)
	user.ID = 1

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func main() {
	http.HandleFunc("/users", createUser)
	http.ListenAndServe(":8080", nil)
}`
	}

	return generateDefaultMockCode()
}

func generateDefaultMockCode() string {
	return `package main

import "fmt"

func main() {
	fmt.Println("Mock implementation generated")
}`
}

func generateMockTask(prompt string) string {
	promptLower := strings.ToLower(prompt)

	if strings.Contains(promptLower, "health") {
		return `Implement a health check endpoint with the following requirements:
- Create GET /health endpoint
- Return JSON response with status and timestamp
- Use proper HTTP status codes
- Ensure fast response time under 100ms`
	}

	if strings.Contains(promptLower, "user") {
		return `Implement user management API with the following requirements:
- Create user CRUD endpoints
- Support JSON request/response format
- Include proper validation
- Add authentication if needed`
	}

	return generateDefaultMockTask()
}

func generateDefaultMockTask() string {
	return `Implement the requested feature with the following requirements:
- Follow Go best practices
- Include proper error handling
- Add appropriate tests
- Use standard library when possible`
}
