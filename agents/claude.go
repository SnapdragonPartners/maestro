package agents

import (
	"context"
	"fmt"
	"os"
	"strings"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// ClaudeAgent implements the Agent interface for code generation tasks
type ClaudeAgent struct {
	id      string
	name    string
	workDir string
	logger  *logx.Logger
}

// NewClaudeAgent creates a new Claude coding agent
func NewClaudeAgent(id, name, workDir string) *ClaudeAgent {
	// Create workspace directory if it doesn't exist
	if err := os.MkdirAll(workDir, 0755); err != nil {
		// Log error but don't fail - let it fail later with better context
		fmt.Printf("Warning: failed to create workspace directory %s: %v\n", workDir, err)
	}

	return &ClaudeAgent{
		id:      id,
		name:    name,
		workDir: workDir,
		logger:  logx.NewLogger(id),
	}
}

// GetID returns the agent's identifier
func (c *ClaudeAgent) GetID() string {
	return c.id
}

// ProcessMessage processes incoming task messages and generates code
func (c *ClaudeAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	c.logger.Info("Processing message %s from %s", msg.ID, msg.FromAgent)

	switch msg.Type {
	case proto.MsgTypeTASK:
		return c.handleTaskMessage(ctx, msg)
	case proto.MsgTypeQUESTION:
		return c.handleQuestionMessage(ctx, msg)
	case proto.MsgTypeSHUTDOWN:
		return c.handleShutdownMessage(ctx, msg)
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// Shutdown performs cleanup when the agent is stopping
func (c *ClaudeAgent) Shutdown(ctx context.Context) error {
	c.logger.Info("Claude coding agent shutting down")
	return nil
}

func (c *ClaudeAgent) handleTaskMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract task content from the message payload
	content, exists := msg.GetPayload("content")
	if !exists {
		return nil, fmt.Errorf("missing content in task message")
	}

	contentStr, ok := content.(string)
	if !ok {
		return nil, fmt.Errorf("content must be a string")
	}

	c.logger.Info("Processing coding task: %s", contentStr)

	// Extract requirements if available
	var requirements []string
	if reqPayload, exists := msg.GetPayload("requirements"); exists {
		if reqSlice, ok := reqPayload.([]interface{}); ok {
			for _, req := range reqSlice {
				if reqStr, ok := req.(string); ok {
					requirements = append(requirements, reqStr)
				}
			}
		}
	}

	// For MVP, simulate Claude API call with mock implementation
	// In production, this would call the actual Claude API
	codeImplementation := c.generateCodeImplementation(contentStr, requirements)
	testImplementation := c.generateTestImplementation(contentStr, requirements)
	documentation := c.generateDocumentation(contentStr, requirements)

	// Create result message
	result := proto.NewAgentMsg(proto.MsgTypeRESULT, c.id, msg.FromAgent)
	result.ParentMsgID = msg.ID
	result.SetPayload("status", "completed")
	result.SetPayload("implementation", codeImplementation)
	result.SetPayload("tests", testImplementation)
	result.SetPayload("documentation", documentation)
	result.SetPayload("files_created", c.getCreatedFiles(contentStr))
	result.SetMetadata("processing_agent", "claude-simulated")
	result.SetMetadata("task_type", "code_generation")

	// Extract story ID if available for traceability
	if storyID, exists := msg.GetPayload("story_id"); exists {
		if storyIDStr, ok := storyID.(string); ok {
			result.SetMetadata("story_id", storyIDStr)
		}
	}

	c.logger.Info("Generated code implementation for task %s", msg.ID)

	return result, nil
}

func (c *ClaudeAgent) handleQuestionMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	question, exists := msg.GetPayload("question")
	if !exists {
		return nil, fmt.Errorf("missing question in message")
	}

	questionStr, ok := question.(string)
	if !ok {
		return nil, fmt.Errorf("question must be a string")
	}

	c.logger.Info("Received question: %s", questionStr)

	// For MVP, ask architect for guidance
	response := proto.NewAgentMsg(proto.MsgTypeQUESTION, c.id, "architect")
	response.ParentMsgID = msg.ID
	response.SetPayload("question", questionStr)
	response.SetPayload("context", "Code implementation question")
	response.SetMetadata("original_sender", msg.FromAgent)

	return response, nil
}

func (c *ClaudeAgent) handleShutdownMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	c.logger.Info("Received shutdown request")

	response := proto.NewAgentMsg(proto.MsgTypeRESULT, c.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "shutdown_acknowledged")
	response.SetMetadata("agent_type", "coding_agent")

	return response, nil
}

func (c *ClaudeAgent) generateCodeImplementation(content string, requirements []string) string {
	// Simple MVP implementation - generate basic code based on content analysis
	content = strings.ToLower(content)

	if strings.Contains(content, "health") && strings.Contains(content, "endpoint") {
		return c.generateHealthEndpointCode()
	}

	if strings.Contains(content, "api") || strings.Contains(content, "rest") {
		return c.generateAPICode(content, requirements)
	}

	if strings.Contains(content, "database") || strings.Contains(content, "storage") {
		return c.generateDatabaseCode(content, requirements)
	}

	// Default implementation
	return c.generateDefaultImplementation(content, requirements)
}

func (c *ClaudeAgent) generateHealthEndpointCode() string {
	return `package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type HealthResponse struct {
	Status    string    ` + "`json:\"status\"`" + `
	Timestamp time.Time ` + "`json:\"timestamp\"`" + `
	Version   string    ` + "`json:\"version\"`" + `
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now(),
		Version:   "1.0.0",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

func main() {
	http.HandleFunc("/health", healthHandler)
	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}`
}

func (c *ClaudeAgent) generateAPICode(content string, requirements []string) string {
	return `package api

import (
	"encoding/json"
	"net/http"
)

type APIResponse struct {
	Success bool        ` + "`json:\"success\"`" + `
	Data    interface{} ` + "`json:\"data,omitempty\"`" + `
	Error   string      ` + "`json:\"error,omitempty\"`" + `
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	
	response := APIResponse{
		Success: status < 400,
		Data:    data,
	}
	
	json.NewEncoder(w).Encode(response)
}

func respondError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	
	response := APIResponse{
		Success: false,
		Error:   message,
	}
	
	json.NewEncoder(w).Encode(response)
}`
}

func (c *ClaudeAgent) generateDatabaseCode(content string, requirements []string) string {
	return `package database

import (
	"database/sql"
	"fmt"
	
	_ "github.com/lib/pq"
)

type DB struct {
	conn *sql.DB
}

func NewDB(connectionString string) (*DB, error) {
	conn, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	
	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) Health() error {
	return db.conn.Ping()
}`
}

func (c *ClaudeAgent) generateDefaultImplementation(content string, requirements []string) string {
	return `package main

import (
	"fmt"
	"log"
)

// Implementation generated for: ` + content + `
func main() {
	fmt.Println("Implementation ready")
	
	// TODO: Implement specific requirements:
	` + c.formatRequirements(requirements) + `
	
	log.Println("Task completed successfully")
}`
}

func (c *ClaudeAgent) generateTestImplementation(content string, requirements []string) string {
	content = strings.ToLower(content)

	if strings.Contains(content, "health") && strings.Contains(content, "endpoint") {
		return c.generateHealthEndpointTests()
	}

	// Default test implementation
	return `package main

import (
	"testing"
)

func TestImplementation(t *testing.T) {
	// Test cases for: ` + content + `
	
	t.Run("basic functionality", func(t *testing.T) {
		// TODO: Implement test cases
		// Requirements to test:
		` + c.formatRequirementsAsComments(requirements) + `
	})
	
	t.Run("error handling", func(t *testing.T) {
		// TODO: Test error scenarios
	})
	
	t.Run("edge cases", func(t *testing.T) {
		// TODO: Test boundary conditions
	})
}`
}

func (c *ClaudeAgent) generateHealthEndpointTests() string {
	return `package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"encoding/json"
)

func TestHealthHandler(t *testing.T) {
	t.Run("GET request returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		
		healthHandler(w, req)
		
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
	})
	
	t.Run("response is valid JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		
		healthHandler(w, req)
		
		var response HealthResponse
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Errorf("Failed to decode JSON response: %v", err)
		}
		
		if response.Status != "healthy" {
			t.Errorf("Expected status 'healthy', got %s", response.Status)
		}
	})
	
	t.Run("POST request returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/health", nil)
		w := httptest.NewRecorder()
		
		healthHandler(w, req)
		
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("Expected status 405, got %d", w.Code)
		}
	})
}`
}

func (c *ClaudeAgent) generateDocumentation(content string, requirements []string) string {
	return `# Implementation Documentation

## Overview
` + content + `

## Requirements
` + c.formatRequirementsAsMarkdown(requirements) + `

## Usage
This implementation provides the requested functionality with proper error handling and testing.

## Testing
Run tests with: ` + "`go test`" + `

## Notes
- Implementation follows Go best practices
- Error handling is included throughout
- Tests cover main functionality and edge cases
`
}

func (c *ClaudeAgent) getCreatedFiles(content string) []string {
	content = strings.ToLower(content)

	if strings.Contains(content, "health") && strings.Contains(content, "endpoint") {
		return []string{"health.go", "health_test.go", "README.md"}
	}

	if strings.Contains(content, "api") {
		return []string{"api.go", "api_test.go", "README.md"}
	}

	if strings.Contains(content, "database") {
		return []string{"database.go", "database_test.go", "README.md"}
	}

	return []string{"main.go", "main_test.go", "README.md"}
}

func (c *ClaudeAgent) formatRequirements(requirements []string) string {
	if len(requirements) == 0 {
		return "// No specific requirements provided"
	}

	result := ""
	for _, req := range requirements {
		result += fmt.Sprintf("\t// - %s\n", req)
	}
	return result
}

func (c *ClaudeAgent) formatRequirementsAsComments(requirements []string) string {
	if len(requirements) == 0 {
		return "\t\t// No specific requirements provided"
	}

	result := ""
	for _, req := range requirements {
		result += fmt.Sprintf("\t\t// - %s\n", req)
	}
	return result
}

func (c *ClaudeAgent) formatRequirementsAsMarkdown(requirements []string) string {
	if len(requirements) == 0 {
		return "No specific requirements provided."
	}

	result := ""
	for _, req := range requirements {
		result += fmt.Sprintf("- %s\n", req)
	}
	return result
}
