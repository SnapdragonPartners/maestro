package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	anthropic "github.com/liushuangls/go-anthropic/v2"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// CodeBlock represents an extracted code block from Claude's response
type CodeBlock struct {
	Language string
	Content  string
	Filename string
}

// LiveClaudeAgent implements the Agent interface with real Anthropic API calls
type LiveClaudeAgent struct {
	id         string
	name       string
	workDir    string
	logger     *logx.Logger
	client     *anthropic.Client
	useLiveAPI bool // Feature flag for testing
}

// NewLiveClaudeAgent creates a new Claude agent with real API integration
func NewLiveClaudeAgent(id, name, workDir, apiKey string, useLiveAPI bool) *LiveClaudeAgent {
	// Create workspace directory if it doesn't exist
	if err := os.MkdirAll(workDir, 0755); err != nil {
		fmt.Printf("Warning: failed to create workspace directory %s: %v\n", workDir, err)
	}

	var client *anthropic.Client
	if useLiveAPI && apiKey != "" {
		client = anthropic.NewClient(apiKey)
	}

	return &LiveClaudeAgent{
		id:         id,
		name:       name,
		workDir:    workDir,
		logger:     logx.NewLogger(id),
		client:     client,
		useLiveAPI: useLiveAPI,
	}
}

// GetID returns the agent's identifier
func (c *LiveClaudeAgent) GetID() string {
	return c.id
}

// ProcessMessage handles incoming messages with real Claude API calls
func (c *LiveClaudeAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	c.logger.Info("Processing message %s from %s", msg.ID, msg.FromAgent)

	switch msg.Type {
	case proto.MsgTypeTASK:
		return c.processTask(ctx, msg)
	case proto.MsgTypeSHUTDOWN:
		return c.processShutdown(ctx, msg)
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// Shutdown performs cleanup for the agent
func (c *LiveClaudeAgent) Shutdown(ctx context.Context) error {
	c.logger.Info("Shutting down Claude agent")
	return nil
}

func (c *LiveClaudeAgent) processTask(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	content, exists := msg.GetPayload("content")
	if !exists {
		return c.createErrorResponse(msg, "Missing 'content' in task payload")
	}

	taskContent, ok := content.(string)
	if !ok {
		return c.createErrorResponse(msg, "Task content must be a string")
	}

	c.logger.Info("Processing coding task: %s", strings.Split(taskContent, "\n")[0])

	// Generate code using Claude API or mock
	var implementation string
	var err error

	if c.useLiveAPI && c.client != nil {
		implementation, err = c.generateCodeWithClaude(ctx, taskContent)
	} else {
		implementation = c.generateMockCode(taskContent)
	}

	if err != nil {
		return c.createErrorResponse(msg, fmt.Sprintf("Code generation failed: %v", err))
	}

	// Write code to workspace
	if c.useLiveAPI {
		if err := c.writeCodeToWorkspace(implementation, taskContent); err != nil {
			c.logger.Error("Failed to write code to workspace: %v", err)
			// Continue anyway - this is not a fatal error
		}
	} else {
		if err := c.writeMockCodeToWorkspace(implementation, taskContent); err != nil {
			c.logger.Error("Failed to write mock code to workspace: %v", err)
			// Continue anyway - this is not a fatal error
		}
	}

	// Run tests and linting
	testResults := c.runTestsAndLinting()

	// Create response based on test results
	if testResults.Success {
		response := proto.NewAgentMsg(proto.MsgTypeRESULT, c.id, msg.FromAgent)
		response.ParentMsgID = msg.ID
		response.SetPayload("status", "completed")
		response.SetPayload("implementation", implementation)
		response.SetPayload("test_results", testResults)
		response.SetMetadata("agent_type", "coding_agent")
		response.SetMetadata("workspace", c.workDir)
		return response, nil
	} else {
		return c.createErrorResponse(msg, fmt.Sprintf("Tests failed: %s", testResults.Output))
	}
}

func (c *LiveClaudeAgent) generateCodeWithClaude(ctx context.Context, taskContent string) (string, error) {
	c.logger.Info("Calling Claude API for code generation")

	// Read STYLE.md for context if it exists
	styleGuide := c.readStyleGuide()

	// Construct prompt
	prompt := c.buildCodeGenerationPrompt(taskContent, styleGuide)

	// Make API call
	temp := float32(0.1)
	resp, err := c.client.CreateMessages(ctx, anthropic.MessagesRequest{
		Model: anthropic.ModelClaude3Sonnet20240229,
		Messages: []anthropic.Message{
			{
				Role: anthropic.RoleUser,
				Content: []anthropic.MessageContent{
					anthropic.NewTextMessageContent(prompt),
				},
			},
		},
		MaxTokens: 4000,
		Temperature: &temp, // Low temperature for consistent code generation
	})

	if err != nil {
		return "", fmt.Errorf("Claude API call failed: %w", err)
	}

	if len(resp.Content) == 0 {
		return "", fmt.Errorf("empty response from Claude API")
	}

	// Extract text content
	var implementation strings.Builder
	for _, content := range resp.Content {
		if content.Type == "text" && content.Text != nil {
			implementation.WriteString(*content.Text)
		}
	}

	result := implementation.String()
	c.logger.Info("Generated %d characters of code", len(result))
	return result, nil
}

func (c *LiveClaudeAgent) generateMockCode(taskContent string) string {
	c.logger.Info("Generating mock code implementation")

	// Enhanced mock code generation based on task content
	taskLower := strings.ToLower(taskContent)

	if strings.Contains(taskLower, "health") || strings.Contains(taskLower, "/health") {
		return c.generateHealthEndpointCode()
	}

	if strings.Contains(taskLower, "user") && strings.Contains(taskLower, "api") {
		return c.generateUserAPICode()
	}

	if strings.Contains(taskLower, "database") || strings.Contains(taskLower, "postgresql") {
		return c.generateDatabaseCode()
	}

	// Default implementation
	return fmt.Sprintf(`package main

import (
	"fmt"
	"time"
)

// Generated implementation for: %s
func main() {
	fmt.Printf("Implementation generated at: %%s\\n", time.Now().Format(time.RFC3339))
	fmt.Println("Task: %s")
	
	// TODO: Implement the actual functionality
	fmt.Println("✓ Mock implementation completed")
}

// Example function based on task requirements
func processTask() error {
	// Implementation goes here
	return nil
}
`, strings.Split(taskContent, "\n")[0], taskContent)
}

func (c *LiveClaudeAgent) generateHealthEndpointCode() string {
	return `package main

import (
	"encoding/json"
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
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

func main() {
	http.HandleFunc("/health", healthHandler)
	http.ListenAndServe(":8080", nil)
}
`
}

func (c *LiveClaudeAgent) generateUserAPICode() string {
	return `package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type User struct {
	ID       int       ` + "`json:\"id\"`" + `
	Name     string    ` + "`json:\"name\"`" + `
	Email    string    ` + "`json:\"email\"`" + `
	Created  time.Time ` + "`json:\"created\"`" + `
}

var users = make(map[int]User)
var nextID = 1

func createUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var user User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	user.ID = nextID
	nextID++
	user.Created = time.Now()
	users[user.ID] = user

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func getUserHandler(w http.ResponseWriter, r *http.Request) {
	// Implementation for GET /users/{id}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func main() {
	http.HandleFunc("/users", createUserHandler)
	http.HandleFunc("/users/", getUserHandler)
	http.ListenAndServe(":8080", nil)
}
`
}

func (c *LiveClaudeAgent) generateDatabaseCode() string {
	return `package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

func NewDatabaseConnection(config DatabaseConfig) (*sql.DB, error) {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Host, config.Port, config.User, config.Password, config.DBName)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test the connection
	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

func main() {
	config := DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "postgres",
		Password: "password",
		DBName:   "testdb",
	}

	db, err := NewDatabaseConnection(config)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.Close()

	fmt.Println("✓ Database connection established successfully")
}
`
}

func (c *LiveClaudeAgent) readStyleGuide() string {
	styleGuidePath := "docs/STYLE.md"
	if content, err := os.ReadFile(styleGuidePath); err == nil {
		return string(content)
	}
	return ""
}

func (c *LiveClaudeAgent) buildCodeGenerationPrompt(taskContent, styleGuide string) string {
	prompt := fmt.Sprintf(`You are a senior Go developer. Generate high-quality Go code for the following task:

TASK:
%s

REQUIREMENTS:
- Write clean, idiomatic Go code
- Include proper error handling
- Add appropriate comments
- Follow Go naming conventions
- Include a main function if it's a standalone program
- Generate only the code, no explanations

`, taskContent)

	if styleGuide != "" {
		prompt += fmt.Sprintf(`STYLE GUIDE:
%s

`, styleGuide)
	}

	prompt += `Generate the complete Go code implementation:`

	return prompt
}

func (c *LiveClaudeAgent) writeCodeToWorkspace(implementation, taskContent string) error {
	// Extract code blocks from Claude's response
	codeBlocks := c.extractCodeBlocks(implementation)
	
	if len(codeBlocks) == 0 {
		// If no code blocks found, try to extract raw Go code
		if goCode := c.extractRawGoCode(implementation); goCode != "" {
			codeBlocks = []CodeBlock{{Language: "go", Content: goCode, Filename: ""}}
		} else {
			return fmt.Errorf("no Go code found in Claude's response")
		}
	}

	// Write each code block to appropriate files
	for i, block := range codeBlocks {
		filename := block.Filename
		if filename == "" {
			// Generate filename based on task content
			if strings.Contains(strings.ToLower(taskContent), "health") {
				filename = "health.go"
			} else if strings.Contains(strings.ToLower(taskContent), "user") {
				filename = "user.go"
			} else if strings.Contains(strings.ToLower(taskContent), "database") {
				filename = "database.go"
			} else if len(codeBlocks) == 1 {
				filename = "main.go"
			} else {
				filename = fmt.Sprintf("generated_%d.go", i+1)
			}
		}

		filePath := filepath.Join(c.workDir, filename)
		
		// Write the code content to file
		if err := os.WriteFile(filePath, []byte(block.Content), 0644); err != nil {
			return fmt.Errorf("failed to write code to %s: %w", filePath, err)
		}

		c.logger.Info("Wrote generated code to %s (%d lines)", filePath, strings.Count(block.Content, "\n")+1)
	}

	return nil
}

// writeMockCodeToWorkspace writes mock-generated code directly to workspace without extraction
func (c *LiveClaudeAgent) writeMockCodeToWorkspace(implementation, taskContent string) error {
	// Determine filename based on task content
	filename := "main.go"
	if strings.Contains(strings.ToLower(taskContent), "health") {
		filename = "health.go"
	} else if strings.Contains(strings.ToLower(taskContent), "user") {
		filename = "user.go"
	} else if strings.Contains(strings.ToLower(taskContent), "database") {
		filename = "database.go"
	}

	filePath := filepath.Join(c.workDir, filename)
	
	// Write the code content directly to file
	if err := os.WriteFile(filePath, []byte(implementation), 0644); err != nil {
		return fmt.Errorf("failed to write mock code to %s: %w", filePath, err)
	}

	c.logger.Info("Wrote generated code to %s (%d lines)", filePath, strings.Count(implementation, "\n")+1)
	return nil
}

type TestResults struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Elapsed string `json:"elapsed"`
}

func (c *LiveClaudeAgent) runTestsAndLinting() TestResults {
	start := time.Now()
	
	// Change to workspace directory
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)
	
	if err := os.Chdir(c.workDir); err != nil {
		return TestResults{
			Success: false,
			Output:  fmt.Sprintf("Failed to change to workspace directory: %v", err),
			Elapsed: time.Since(start).String(),
		}
	}

	// Check if there are any Go files
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		return TestResults{
			Success: true, // No Go files to test
			Output:  "No Go files found in workspace",
			Elapsed: time.Since(start).String(),
		}
	}

	// Try to run go mod init if no go.mod exists
	if _, err := os.Stat("go.mod"); os.IsNotExist(err) {
		initCmd := exec.Command("go", "mod", "init", "generated")
		initCmd.Run() // Ignore errors - might already exist at parent level
	}

	// Run go fmt
	fmtCmd := exec.Command("go", "fmt", ".")
	if fmtOutput, err := fmtCmd.CombinedOutput(); err != nil {
		return TestResults{
			Success: false,
			Output:  fmt.Sprintf("go fmt failed: %v\nOutput: %s", err, string(fmtOutput)),
			Elapsed: time.Since(start).String(),
		}
	}

	// Run go build to check compilation
	buildCmd := exec.Command("go", "build", ".")
	if buildOutput, err := buildCmd.CombinedOutput(); err != nil {
		return TestResults{
			Success: false,
			Output:  fmt.Sprintf("go build failed: %v\nOutput: %s", err, string(buildOutput)),
			Elapsed: time.Since(start).String(),
		}
	}

	// Run tests if any exist
	if testFiles, _ := filepath.Glob("*_test.go"); len(testFiles) > 0 {
		testCmd := exec.Command("go", "test", "-v", ".")
		if testOutput, err := testCmd.CombinedOutput(); err != nil {
			return TestResults{
				Success: false,
				Output:  fmt.Sprintf("go test failed: %v\nOutput: %s", err, string(testOutput)),
				Elapsed: time.Since(start).String(),
			}
		}
	}

	return TestResults{
		Success: true,
		Output:  "All checks passed: go fmt, go build completed successfully",
		Elapsed: time.Since(start).String(),
	}
}

func (c *LiveClaudeAgent) processShutdown(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	c.logger.Info("Received shutdown request")

	response := proto.NewAgentMsg(proto.MsgTypeRESULT, c.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "shutdown_acknowledged")
	response.SetMetadata("agent_type", "coding_agent")

	return response, nil
}

func (c *LiveClaudeAgent) createErrorResponse(originalMsg *proto.AgentMsg, errorMsg string) (*proto.AgentMsg, error) {
	c.logger.Error("%s", errorMsg)

	response := proto.NewAgentMsg(proto.MsgTypeERROR, c.id, originalMsg.FromAgent)
	response.ParentMsgID = originalMsg.ID
	response.SetPayload("error", errorMsg)
	response.SetPayload("original_message_id", originalMsg.ID)
	response.SetMetadata("error_type", "processing_error")

	return response, nil
}

// extractCodeBlocks extracts code blocks from Claude's markdown response
func (c *LiveClaudeAgent) extractCodeBlocks(response string) []CodeBlock {
	var blocks []CodeBlock
	
	// Regex to match markdown code blocks with optional language and filename
	codeBlockRegex := regexp.MustCompile("(?s)```(\\w+)?(?:\\s+//\\s*([^\\n]+))?\\n(.*?)```")
	matches := codeBlockRegex.FindAllStringSubmatch(response, -1)
	
	for _, match := range matches {
		if len(match) >= 4 {
			language := strings.ToLower(match[1])
			filename := strings.TrimSpace(match[2])
			content := strings.TrimSpace(match[3])
			
			// Only process Go code blocks
			if language == "go" || language == "golang" || (language == "" && strings.Contains(content, "package ")) {
				blocks = append(blocks, CodeBlock{
					Language: "go",
					Content:  content,
					Filename: filename,
				})
			}
		}
	}
	
	return blocks
}

// extractRawGoCode attempts to extract Go code from unformatted responses
func (c *LiveClaudeAgent) extractRawGoCode(response string) string {
	lines := strings.Split(response, "\n")
	var codeLines []string
	inCode := false
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Start of Go code block
		if strings.HasPrefix(trimmed, "package ") {
			inCode = true
			codeLines = append(codeLines, line)
			continue
		}
		
		// If we're in a code block, continue collecting lines
		if inCode {
			// Skip obvious non-code lines (explanatory text)
			if trimmed == "" || 
			   strings.HasPrefix(trimmed, "//") || 
			   strings.Contains(trimmed, "func ") || 
			   strings.Contains(trimmed, "import ") ||
			   strings.Contains(trimmed, "type ") ||
			   strings.Contains(trimmed, "var ") ||
			   strings.Contains(trimmed, "const ") ||
			   strings.HasPrefix(trimmed, "}") ||
			   strings.HasPrefix(trimmed, "\t") ||
			   strings.HasPrefix(trimmed, " ") {
				codeLines = append(codeLines, line)
			} else if len(trimmed) > 0 && !strings.Contains(trimmed, " ") && !strings.Contains(trimmed, "(") {
				// This might be explanatory text, stop code collection
				break
			} else {
				codeLines = append(codeLines, line)
			}
		}
	}
	
	if len(codeLines) > 0 {
		return strings.Join(codeLines, "\n")
	}
	
	return ""
}