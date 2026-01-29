//go:build integration

package architect_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// testArchitectModel returns the model to use for architect integration tests.
// Priority: TEST_ARCHITECT_MODEL env var > config.GetEffectiveArchitectModel() > default
func testArchitectModel() string {
	if model := os.Getenv("TEST_ARCHITECT_MODEL"); model != "" {
		return model
	}
	// Try to get from config, fall back to default
	if model := config.GetEffectiveArchitectModel(); model != "" {
		return model
	}
	return config.DefaultArchitectModel
}

// createTestLLMClient creates an LLM client for the given model name.
// This is a simplified version without the full middleware chain for testing.
func createTestLLMClient(t *testing.T, modelName string) llm.LLMClient {
	t.Helper()

	client, err := agent.NewTestLLMClient(modelName)
	if err != nil {
		t.Skipf("Skipping test: failed to create LLM client for %s: %v", modelName, err)
	}

	provider, _ := config.GetModelProvider(modelName)
	t.Logf("Created LLM client for model: %s (provider: %s)", modelName, provider)
	return client
}

// createTestLLMClientWithMiddleware creates an LLM client with validation middleware.
// This more closely matches production behavior including empty response validation.
func createTestLLMClientWithMiddleware(t *testing.T, modelName string) llm.LLMClient {
	t.Helper()

	client, err := agent.NewTestLLMClientWithMiddleware(modelName, agent.TypeArchitect)
	if err != nil {
		t.Skipf("Skipping test: failed to create LLM client with middleware for %s: %v", modelName, err)
	}

	provider, _ := config.GetModelProvider(modelName)
	t.Logf("Created LLM client WITH MIDDLEWARE for model: %s (provider: %s)", modelName, provider)
	return client
}

// retryableCompletion wraps client.Complete with retry logic for transient errors.
func retryableCompletion(t *testing.T, client llm.LLMClient, req llm.CompletionRequest, maxRetries int) (llm.CompletionResponse, error) {
	t.Helper()
	var lastErr error
	transientFailures := 0

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		resp, err := client.Complete(ctx, req)
		cancel()

		if err == nil {
			return resp, nil
		}

		errStr := err.Error()
		isTransient := strings.Contains(errStr, "504") ||
			strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "DEADLINE_EXCEEDED") ||
			strings.Contains(errStr, "RESOURCE_EXHAUSTED")

		if !isTransient {
			return llm.CompletionResponse{}, err
		}

		transientFailures++
		lastErr = err
		if attempt < maxRetries {
			t.Logf("Attempt %d/%d failed with transient error: %v. Retrying...", attempt, maxRetries, err)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}

	if transientFailures == maxRetries {
		t.Skipf("Skipping test: API unavailable after %d attempts (last error: %v)", maxRetries, lastErr)
	}

	return llm.CompletionResponse{}, lastErr
}

// TestSpecReviewAndStoryGeneration tests the two-phase spec review flow:
// 1. Phase 1: Spec review with review_complete tool (+ optional general tools)
// 2. Phase 2: Story generation with only submit_stories tool
//
// This test replicates the issue where Gemini returns empty responses in Phase 2
// when the context contains tool calls from Phase 1 for tools that are no longer available.
func TestSpecReviewAndStoryGeneration(t *testing.T) {
	modelName := testArchitectModel()
	t.Logf("Testing with architect model: %s", modelName)

	client := createTestLLMClient(t, modelName)
	logger := logx.NewLogger("test-architect")

	// Simple test spec
	testSpec := `# Test Feature Specification

## Overview
Add a simple greeting feature to the application.

## Requirements
1. Create a function that returns "Hello, World!"
2. Add a unit test for the greeting function

## Platform
Go
`

	// =========================================================================
	// PHASE 1: Spec Review (with review_complete as terminal tool)
	// =========================================================================
	t.Log("=== PHASE 1: Spec Review ===")

	cm := contextmgr.NewContextManager()

	// Add spec review prompt
	specReviewPrompt := `You are an architect reviewing a specification.

## Specification to Review
` + "```\n" + testSpec + "\n```" + `

## Your Task
Review this specification and decide whether to approve it.
- If the spec is clear and implementable, call review_complete with status "APPROVED"
- If changes are needed, call review_complete with status "NEEDS_CHANGES" and explain what's missing

You MUST call the review_complete tool to complete your review.
`
	cm.AddMessage("user", specReviewPrompt)

	// Create review_complete tool
	reviewCompleteTool := tools.NewReviewCompleteTool()

	// Run Phase 1 toolloop
	loop := toolloop.New(client, logger)
	phase1Out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   nil, // No general tools for this simple test
		TerminalTool:   reviewCompleteTool,
		MaxIterations:  5,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		DebugLogging:   true,
	})

	if phase1Out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Phase 1 failed: expected OutcomeProcessEffect, got %v with error: %v", phase1Out.Kind, phase1Out.Err)
	}

	if phase1Out.Signal != tools.SignalReviewComplete {
		t.Fatalf("Phase 1: expected signal %s, got %s", tools.SignalReviewComplete, phase1Out.Signal)
	}

	// Extract review status
	effectData, ok := utils.SafeAssert[map[string]any](phase1Out.EffectData)
	if !ok {
		t.Fatalf("Phase 1: expected EffectData to be map[string]any, got %T", phase1Out.EffectData)
	}

	status, _ := effectData["status"].(string)
	t.Logf("Phase 1 completed with status: %s", status)

	if status != "APPROVED" && status != "NEEDS_CHANGES" && status != "REJECTED" {
		t.Fatalf("Phase 1: unexpected status %q", status)
	}

	// For this test, we'll proceed to Phase 2 regardless of status
	// In production, Phase 2 only runs if APPROVED

	// =========================================================================
	// PHASE 2: Story Generation (with ONLY submit_stories tool)
	// This is where the issue occurs - the context has tool calls from Phase 1
	// but now only submit_stories is available
	// =========================================================================
	t.Log("=== PHASE 2: Story Generation ===")

	// Add story generation prompt to the SAME context (this is how production works)
	storyGenPrompt := `# Story Generation from Approved Specification

You have reviewed the specification. Now generate implementation stories.

## Your Task
Generate implementation stories from this specification. You MUST call the submit_stories tool with your generated stories.

## Output Format
Call submit_stories with:
- analysis: Brief summary of what you found
- platform: "go"
- requirements: Array of requirement objects with title, description, acceptance_criteria, dependencies, story_type
`
	cm.AddMessage("user", storyGenPrompt)

	// Create submit_stories tool
	submitStoriesTool := tools.NewSubmitStoriesTool()

	// Run Phase 2 toolloop with ONLY submit_stories (no general tools)
	// This replicates the production configuration
	phase2Out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   nil, // NO general tools - just submit_stories
		TerminalTool:   submitStoriesTool,
		MaxIterations:  5,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		SingleTurn:     true, // Production uses SingleTurn for story generation
		DebugLogging:   true,
	})

	// This is where the issue manifests - empty response from Gemini
	if phase2Out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Phase 2 failed: expected OutcomeProcessEffect, got %v with error: %v", phase2Out.Kind, phase2Out.Err)
	}

	if phase2Out.Signal != tools.SignalStoriesSubmitted {
		t.Fatalf("Phase 2: expected signal %s, got %s", tools.SignalStoriesSubmitted, phase2Out.Signal)
	}

	// Extract stories data
	storiesData, ok := utils.SafeAssert[map[string]any](phase2Out.EffectData)
	if !ok {
		t.Fatalf("Phase 2: expected EffectData to be map[string]any, got %T", phase2Out.EffectData)
	}

	requirements, ok := utils.SafeAssert[[]any](storiesData["requirements"])
	if !ok {
		t.Fatalf("Phase 2: expected requirements to be []any, got %T", storiesData["requirements"])
	}

	t.Logf("Phase 2 completed: generated %d stories", len(requirements))

	if len(requirements) == 0 {
		t.Fatal("Phase 2: expected at least one story to be generated")
	}

	// Log the generated stories
	for i, req := range requirements {
		if reqMap, ok := utils.SafeAssert[map[string]any](req); ok {
			title, _ := reqMap["title"].(string)
			storyType, _ := reqMap["story_type"].(string)
			t.Logf("  Story %d: %s (type: %s)", i+1, title, storyType)
		}
	}

	t.Log("=== TEST PASSED ===")
}

// TestStoryGenerationWithFreshContext tests story generation with a fresh context
// (not reusing the spec review context). This isolates whether the issue is
// context pollution or something else.
func TestStoryGenerationWithFreshContext(t *testing.T) {
	modelName := testArchitectModel()
	t.Logf("Testing with architect model: %s", modelName)

	client := createTestLLMClient(t, modelName)
	logger := logx.NewLogger("test-architect")

	testSpec := `# Test Feature Specification

## Overview
Add a simple greeting feature to the application.

## Requirements
1. Create a function that returns "Hello, World!"
2. Add a unit test for the greeting function

## Platform
Go
`

	// Fresh context with ONLY story generation prompt
	cm := contextmgr.NewContextManager()

	storyGenPrompt := `# Story Generation from Specification

## Approved Specification
` + "```\n" + testSpec + "\n```" + `

## Your Task
Generate implementation stories from this approved specification. You MUST call the submit_stories tool with your generated stories.

## Output Format
Call submit_stories with:
- analysis: Brief summary of what you found in the specification
- platform: "go"
- requirements: Array of requirement objects, each with:
  - title: Concise requirement title
  - description: What needs to be implemented
  - acceptance_criteria: Array of testable criteria
  - dependencies: Array of requirement titles this depends on (can be empty)
  - story_type: Either "app" or "devops"
`
	cm.AddMessage("user", storyGenPrompt)

	submitStoriesTool := tools.NewSubmitStoriesTool()

	loop := toolloop.New(client, logger)
	out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   nil,
		TerminalTool:   submitStoriesTool,
		MaxIterations:  5,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		SingleTurn:     true,
		DebugLogging:   true,
	})

	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Story generation failed: expected OutcomeProcessEffect, got %v with error: %v", out.Kind, out.Err)
	}

	if out.Signal != tools.SignalStoriesSubmitted {
		t.Fatalf("Expected signal %s, got %s", tools.SignalStoriesSubmitted, out.Signal)
	}

	storiesData, ok := utils.SafeAssert[map[string]any](out.EffectData)
	if !ok {
		t.Fatalf("Expected EffectData to be map[string]any, got %T", out.EffectData)
	}

	requirements, ok := utils.SafeAssert[[]any](storiesData["requirements"])
	if !ok {
		t.Fatalf("Expected requirements to be []any, got %T", storiesData["requirements"])
	}

	t.Logf("Generated %d stories with fresh context", len(requirements))

	if len(requirements) == 0 {
		t.Fatal("Expected at least one story to be generated")
	}

	for i, req := range requirements {
		if reqMap, ok := utils.SafeAssert[map[string]any](req); ok {
			title, _ := reqMap["title"].(string)
			t.Logf("  Story %d: %s", i+1, title)
		}
	}
}

// TestSpecReviewWithGeneralToolsThenStoryGeneration tests the production scenario:
// Phase 1: Spec review with general tools (list_files, read_file) + review_complete
// Phase 2: Story generation with ONLY submit_stories
//
// This is the closest replication of the production flow where the context
// accumulates tool calls for tools that are NOT available in Phase 2.
func TestSpecReviewWithGeneralToolsThenStoryGeneration(t *testing.T) {
	modelName := testArchitectModel()
	t.Logf("Testing with architect model: %s", modelName)

	client := createTestLLMClient(t, modelName)
	logger := logx.NewLogger("test-architect")

	// More complex spec that might trigger file exploration
	testSpec := `# User Authentication Feature Specification

## Overview
Implement user authentication with login and logout functionality.

## Requirements
1. Create a login endpoint that accepts username and password
2. Implement password hashing using bcrypt
3. Generate JWT tokens for authenticated sessions
4. Add logout endpoint that invalidates the token
5. Add middleware to protect authenticated routes

## Technical Details
- Use Go standard library for HTTP handling
- Use github.com/golang-jwt/jwt for JWT implementation
- Store user sessions in memory for MVP

## Platform
Go
`

	// =========================================================================
	// PHASE 1: Spec Review with general tools
	// Production uses list_files and read_file in addition to review_complete
	// =========================================================================
	t.Log("=== PHASE 1: Spec Review (with general tools) ===")

	cm := contextmgr.NewContextManager()

	specReviewPrompt := `You are an architect reviewing a specification for a Go project.

## Specification to Review
` + "```\n" + testSpec + "\n```" + `

## Available Tools
- list_files: List files in the project to understand the codebase structure
- read_file: Read specific files to understand existing code
- review_complete: Complete your review with a decision

## Your Task
1. You may optionally explore the project files to understand the context
2. Review the specification for clarity and implementability
3. Call review_complete with your decision (APPROVED, NEEDS_CHANGES, or REJECTED)

Start by reviewing the specification. If you feel you need more context about the existing code, use the exploration tools. Then provide your review.
`
	cm.AddMessage("user", specReviewPrompt)

	// Create tools - including general tools that will create context pollution
	reviewCompleteTool := tools.NewReviewCompleteTool()

	// Create mock general tools that simulate list_files and read_file
	// These will add tool calls to the context that won't be available in Phase 2
	mockListFilesTool := &mockTool{
		name:        "list_files",
		description: "List files in a directory",
		execFunc: func(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "Files in project:\n- main.go\n- go.mod\n- README.md",
			}, nil
		},
	}

	mockReadFileTool := &mockTool{
		name:        "read_file",
		description: "Read a file's contents",
		execFunc: func(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "package main\n\nfunc main() {\n\t// TODO: implement\n}",
			}, nil
		},
	}

	generalTools := []tools.Tool{mockListFilesTool, mockReadFileTool}

	// Run Phase 1 toolloop with general tools
	loop := toolloop.New(client, logger)
	phase1Out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   generalTools, // Include general tools
		TerminalTool:   reviewCompleteTool,
		MaxIterations:  10,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		DebugLogging:   true,
	})

	if phase1Out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Phase 1 failed: expected OutcomeProcessEffect, got %v with error: %v", phase1Out.Kind, phase1Out.Err)
	}

	if phase1Out.Signal != tools.SignalReviewComplete {
		t.Fatalf("Phase 1: expected signal %s, got %s", tools.SignalReviewComplete, phase1Out.Signal)
	}

	effectData, ok := utils.SafeAssert[map[string]any](phase1Out.EffectData)
	if !ok {
		t.Fatalf("Phase 1: expected EffectData to be map[string]any, got %T", phase1Out.EffectData)
	}

	status, _ := effectData["status"].(string)
	t.Logf("Phase 1 completed with status: %s (after %d iterations)", status, phase1Out.Iteration)

	// Log the context state after Phase 1
	messages := cm.GetMessages()
	t.Logf("Context has %d messages after Phase 1", len(messages))
	for i, msg := range messages {
		t.Logf("  [%d] Role: %s, ToolCalls: %d, ToolResults: %d",
			i, msg.Role, len(msg.ToolCalls), len(msg.ToolResults))
	}

	// =========================================================================
	// PHASE 2: Story Generation with ONLY submit_stories
	// The context now has tool calls for list_files and read_file
	// which are NOT available in Phase 2 - this might confuse the model
	// =========================================================================
	t.Log("=== PHASE 2: Story Generation (submit_stories only) ===")

	storyGenPrompt := `# Story Generation from Approved Specification

You have reviewed the specification. Now generate implementation stories.

## Your Task
Generate implementation stories from this specification. You MUST call the submit_stories tool with your generated stories.

## Important
- Do NOT try to use list_files or read_file - only submit_stories is available now
- Focus on generating stories based on the specification you reviewed
`
	cm.AddMessage("user", storyGenPrompt)

	submitStoriesTool := tools.NewSubmitStoriesTool()

	// Run Phase 2 with ONLY submit_stories (no general tools)
	// This is where the context pollution issue might manifest
	phase2Out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   nil, // NO general tools - only submit_stories
		TerminalTool:   submitStoriesTool,
		MaxIterations:  5,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		SingleTurn:     true,
		DebugLogging:   true,
	})

	// Log Phase 2 outcome
	t.Logf("Phase 2 outcome: %v (signal: %s, iteration: %d)",
		phase2Out.Kind, phase2Out.Signal, phase2Out.Iteration)

	if phase2Out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Phase 2 failed: expected OutcomeProcessEffect, got %v with error: %v", phase2Out.Kind, phase2Out.Err)
	}

	if phase2Out.Signal != tools.SignalStoriesSubmitted {
		t.Fatalf("Phase 2: expected signal %s, got %s", tools.SignalStoriesSubmitted, phase2Out.Signal)
	}

	storiesData, ok := utils.SafeAssert[map[string]any](phase2Out.EffectData)
	if !ok {
		t.Fatalf("Phase 2: expected EffectData to be map[string]any, got %T", phase2Out.EffectData)
	}

	requirements, ok := utils.SafeAssert[[]any](storiesData["requirements"])
	if !ok {
		t.Fatalf("Phase 2: expected requirements to be []any, got %T", storiesData["requirements"])
	}

	t.Logf("Phase 2 completed: generated %d stories", len(requirements))

	if len(requirements) == 0 {
		t.Fatal("Phase 2: expected at least one story to be generated")
	}

	for i, req := range requirements {
		if reqMap, ok := utils.SafeAssert[map[string]any](req); ok {
			title, _ := reqMap["title"].(string)
			storyType, _ := reqMap["story_type"].(string)
			t.Logf("  Story %d: %s (type: %s)", i+1, title, storyType)
		}
	}

	t.Log("=== TEST PASSED ===")
}

// mockTool implements a simple mock tool for testing.
type mockTool struct {
	name        string
	description string
	execFunc    func(context.Context, map[string]any) (*tools.ExecResult, error)
}

func (m *mockTool) Name() string { return m.name }

func (m *mockTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        m.name,
		Description: m.description,
		InputSchema: tools.InputSchema{
			Type: "object",
			Properties: map[string]tools.Property{
				"path": {
					Type:        "string",
					Description: "The path to operate on",
				},
			},
		},
	}
}

func (m *mockTool) Exec(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, args)
	}
	return &tools.ExecResult{Content: "ok"}, nil
}

func (m *mockTool) PromptDocumentation() string {
	return m.description
}

// productionSpec is the actual spec that caused the empty response issue in production.
// This is a larger, more complex spec that better represents real-world usage.
const productionSpec = `Quiz Game Gap Analysis & Implementation Spec

## Overview

The quiz game is **partially implemented**. This spec identifies gaps between the current codebase and the target spec, then defines the work required to close those gaps.

---

## Gap Analysis Summary

| Component | Current State | Target State | Gap |
|-----------|--------------|--------------|-----|
| **Constants** | NumQuestions=10, Timer=30s, MaxLeaderboard=10 | NumQuestions=3, Timer=20s, MaxLeaderboard=20 | Update constants |
| **HMAC Signing** | Not implemented | Required for form state integrity | Implement HMAC signing/verification |
| **home.html** | Static "Hello World" page | Links to quiz & leaderboard | Update template |
| **quiz.html** | Missing | Per-question page with timer, choices, score | Create template |
| **results.html** | Missing | End screen with score, %, name entry | Create template |
| **leaderboard.html** | Missing | Top scores table | Create template |
| **quiz_test.go** | Missing | Unit tests for quiz layer | Create test file |
| **Question Schema** | ` + "`text`, `correct_index`" + ` | Keep existing (no ` + "`explanation`" + ` field needed) | No change |
| **Leaderboard Schema** | ` + "`name`, `score`, `timestamp`" + ` | Add ` + "`total`" + ` field for context | Minor update |

---

## Implementation Requirements

### 1. Update Constants in main.go

const (
    NumQuestions          = 3           // was 10
    QuestionTimerSecs     = 20          // was 30
    MaxLeaderboardEntries = 20          // was 10
    QuestionsFile         = "questions.json"
    LeaderboardFile       = "leaderboard.json"
    hmacSecret            = "quiz-game-secret-2025"  // NEW: for form state signing
)

**Rationale**: Align with spec. The hmacSecret is a compile-time constant per spec; production deployments can fork and change.

---

### 2. Implement HMAC Form State Signing

**Purpose**: Prevent users from tampering with hidden form fields (score, question index, question IDs).

**New Functions Required**:

// signState generates an HMAC-SHA256 signature for the given state string
func signState(state string) string

// verifyState checks if the provided signature matches the state
func verifyState(state, signature string) bool

**Form State Payload** (JSON, base64-encoded in hidden field):
{
  "question_ids": ["q1", "q3", "q7"],
  "current_index": 1,
  "score": 1,
  "start_time": "2025-01-15T10:00:00Z"
}

**Hidden Fields in quiz.html**:
- state - base64-encoded JSON payload
- sig - HMAC-SHA256 signature of state

**Validation**: On POST /quiz, verify signature before processing. Return 400 Bad Request if tampered.

---

### 3. Update home.html

Replace static content with navigation.

---

### 4. Create quiz.html

Template for displaying each question with timer, choices, and hidden state fields.

---

### 5. Create results.html

Template for end-of-quiz screen with score display and name entry.

---

### 6. Create leaderboard.html

Template for displaying high scores table.

---

### 7. Update Leaderboard Entry Schema

Add Total field to track questions answered.

---

### 8. Handler Updates

- GET /quiz - Start Quiz
- POST /quiz - Submit Answer
- GET /quiz/results - Show Results
- POST /quiz/leaderboard - Save Score
- GET /leaderboard - View Leaderboard

---

### 9. Create quiz_test.go

Unit tests covering HMAC functions, question loading, random selection, leaderboard operations, and handler tests.

---

## Files to Modify

| File | Action |
|------|--------|
| main.go | Update constants, add HMAC functions, update handlers |
| home.html | Replace content with quiz navigation |

## Files to Create

| File | Description |
|------|-------------|
| quiz.html | Per-question template with timer |
| results.html | End screen with name entry |
| leaderboard.html | High scores table |
| quiz_test.go | Unit tests for quiz functionality |

---

## Acceptance Criteria

1. Quiz Flow: User can start quiz, answer 3 random questions with 20s timer each
2. Timer: JavaScript countdown auto-submits when time expires
3. Tamper Protection: Modifying hidden form fields results in 400 error
4. Results: Shows score, percentage, and name entry form
5. Leaderboard: Saves top 20 scores, displays in descending order
6. Tests: All new tests pass

## Platform
Go
`

// TestStoryGenerationWithProductionSpec tests with the actual production spec
// that caused the empty response issue. This is a larger, more complex spec.
func TestStoryGenerationWithProductionSpec(t *testing.T) {
	modelName := testArchitectModel()
	t.Logf("Testing with architect model: %s", modelName)

	client := createTestLLMClient(t, modelName)
	logger := logx.NewLogger("test-architect")

	// =========================================================================
	// PHASE 1: Spec Review with general tools (replicating production)
	// =========================================================================
	t.Log("=== PHASE 1: Spec Review (production spec) ===")

	cm := contextmgr.NewContextManager()

	specReviewPrompt := `You are an architect reviewing a specification for a Go project.

## Specification to Review
` + "```\n" + productionSpec + "\n```" + `

## Available Tools
- list_files: List files in the project to understand the codebase structure
- read_file: Read specific files to understand existing code
- review_complete: Complete your review with a decision

## Your Task
Review this specification for clarity and implementability, then call review_complete with your decision.
`
	cm.AddMessage("user", specReviewPrompt)

	reviewCompleteTool := tools.NewReviewCompleteTool()

	mockListFilesTool := &mockTool{
		name:        "list_files",
		description: "List files in a directory",
		execFunc: func(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "Files in project:\n- main.go\n- go.mod\n- home.html\n- questions.json\n- leaderboard.json",
			}, nil
		},
	}

	mockReadFileTool := &mockTool{
		name:        "read_file",
		description: "Read a file's contents",
		execFunc: func(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "package main\n\nimport \"net/http\"\n\nfunc main() {\n\thttp.ListenAndServe(\":8080\", nil)\n}",
			}, nil
		},
	}

	generalTools := []tools.Tool{mockListFilesTool, mockReadFileTool}

	loop := toolloop.New(client, logger)
	phase1Out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   generalTools,
		TerminalTool:   reviewCompleteTool,
		MaxIterations:  10,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		DebugLogging:   true,
	})

	if phase1Out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Phase 1 failed: expected OutcomeProcessEffect, got %v with error: %v", phase1Out.Kind, phase1Out.Err)
	}

	effectData, ok := utils.SafeAssert[map[string]any](phase1Out.EffectData)
	if !ok {
		t.Fatalf("Phase 1: expected EffectData to be map[string]any, got %T", phase1Out.EffectData)
	}

	status, _ := effectData["status"].(string)
	t.Logf("Phase 1 completed with status: %s (after %d iterations)", status, phase1Out.Iteration)

	messages := cm.GetMessages()
	t.Logf("Context has %d messages after Phase 1", len(messages))

	// =========================================================================
	// PHASE 2: Story Generation (ONLY submit_stories - this is where issue occurs)
	// =========================================================================
	t.Log("=== PHASE 2: Story Generation (submit_stories only) ===")

	storyGenPrompt := `# Story Generation from Approved Specification

You have reviewed the specification. Now generate implementation stories.

## Your Task
Generate implementation stories from this specification. You MUST call the submit_stories tool with your generated stories.

Do NOT try to use list_files or read_file - only submit_stories is available now.
`
	cm.AddMessage("user", storyGenPrompt)

	submitStoriesTool := tools.NewSubmitStoriesTool()

	// This is where the empty response issue manifested in production
	phase2Out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   nil,
		TerminalTool:   submitStoriesTool,
		MaxIterations:  5,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		SingleTurn:     true,
		DebugLogging:   true,
	})

	t.Logf("Phase 2 outcome: %v (signal: %s, iteration: %d, error: %v)",
		phase2Out.Kind, phase2Out.Signal, phase2Out.Iteration, phase2Out.Err)

	if phase2Out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Phase 2 failed: expected OutcomeProcessEffect, got %v with error: %v", phase2Out.Kind, phase2Out.Err)
	}

	if phase2Out.Signal != tools.SignalStoriesSubmitted {
		t.Fatalf("Phase 2: expected signal %s, got %s", tools.SignalStoriesSubmitted, phase2Out.Signal)
	}

	storiesData, ok := utils.SafeAssert[map[string]any](phase2Out.EffectData)
	if !ok {
		t.Fatalf("Phase 2: expected EffectData to be map[string]any, got %T", phase2Out.EffectData)
	}

	requirements, ok := utils.SafeAssert[[]any](storiesData["requirements"])
	if !ok {
		t.Fatalf("Phase 2: expected requirements to be []any, got %T", storiesData["requirements"])
	}

	t.Logf("Phase 2 completed: generated %d stories", len(requirements))

	if len(requirements) == 0 {
		t.Fatal("Phase 2: expected at least one story to be generated")
	}

	for i, req := range requirements {
		if reqMap, ok := utils.SafeAssert[map[string]any](req); ok {
			title, _ := reqMap["title"].(string)
			storyType, _ := reqMap["story_type"].(string)
			t.Logf("  Story %d: %s (type: %s)", i+1, title, storyType)
		}
	}

	t.Log("=== TEST PASSED ===")
}

// TestStoryGenerationWithMiddleware tests the production scenario with middleware chain.
// This includes empty response validation and retry logic, which more closely matches
// production behavior where empty response errors trigger the fatal shutdown.
func TestStoryGenerationWithMiddleware(t *testing.T) {
	modelName := testArchitectModel()
	t.Logf("Testing WITH MIDDLEWARE using architect model: %s", modelName)

	// Use client WITH middleware (empty response validation + logging)
	client := createTestLLMClientWithMiddleware(t, modelName)
	logger := logx.NewLogger("test-architect-middleware")

	// =========================================================================
	// PHASE 1: Spec Review with general tools (replicating production)
	// =========================================================================
	t.Log("=== PHASE 1: Spec Review (with middleware chain) ===")

	cm := contextmgr.NewContextManager()

	specReviewPrompt := `You are an architect reviewing a specification for a Go project.

## Specification to Review
` + "```\n" + productionSpec + "\n```" + `

## Available Tools
- list_files: List files in the project to understand the codebase structure
- read_file: Read specific files to understand existing code
- review_complete: Complete your review with a decision

## Your Task
Review this specification for clarity and implementability, then call review_complete with your decision.
`
	cm.AddMessage("user", specReviewPrompt)

	reviewCompleteTool := tools.NewReviewCompleteTool()

	mockListFilesTool := &mockTool{
		name:        "list_files",
		description: "List files in a directory",
		execFunc: func(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "Files in project:\n- main.go\n- go.mod\n- home.html\n- questions.json\n- leaderboard.json",
			}, nil
		},
	}

	mockReadFileTool := &mockTool{
		name:        "read_file",
		description: "Read a file's contents",
		execFunc: func(ctx context.Context, args map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "package main\n\nimport \"net/http\"\n\nfunc main() {\n\thttp.ListenAndServe(\":8080\", nil)\n}",
			}, nil
		},
	}

	generalTools := []tools.Tool{mockListFilesTool, mockReadFileTool}

	loop := toolloop.New(client, logger)
	phase1Out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   generalTools,
		TerminalTool:   reviewCompleteTool,
		MaxIterations:  10,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		DebugLogging:   true,
	})

	if phase1Out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Phase 1 failed: expected OutcomeProcessEffect, got %v with error: %v", phase1Out.Kind, phase1Out.Err)
	}

	effectData, ok := utils.SafeAssert[map[string]any](phase1Out.EffectData)
	if !ok {
		t.Fatalf("Phase 1: expected EffectData to be map[string]any, got %T", phase1Out.EffectData)
	}

	status, _ := effectData["status"].(string)
	t.Logf("Phase 1 completed with status: %s (after %d iterations)", status, phase1Out.Iteration)

	messages := cm.GetMessages()
	t.Logf("Context has %d messages after Phase 1", len(messages))

	// =========================================================================
	// PHASE 2: Story Generation (ONLY submit_stories - with middleware)
	// This is where the empty response validation middleware should catch issues
	// =========================================================================
	t.Log("=== PHASE 2: Story Generation (with middleware chain) ===")

	storyGenPrompt := `# Story Generation from Approved Specification

You have reviewed the specification. Now generate implementation stories.

## Your Task
Generate implementation stories from this specification. You MUST call the submit_stories tool with your generated stories.

Do NOT try to use list_files or read_file - only submit_stories is available now.
`
	cm.AddMessage("user", storyGenPrompt)

	submitStoriesTool := tools.NewSubmitStoriesTool()

	// This is where the empty response issue manifested in production
	// The middleware chain should now properly handle this case
	phase2Out := toolloop.Run(loop, context.Background(), &toolloop.Config[any]{
		ContextManager: cm,
		GeneralTools:   nil,
		TerminalTool:   submitStoriesTool,
		MaxIterations:  5,
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        "test-architect",
		SingleTurn:     true,
		DebugLogging:   true,
	})

	t.Logf("Phase 2 (with middleware) outcome: %v (signal: %s, iteration: %d, error: %v)",
		phase2Out.Kind, phase2Out.Signal, phase2Out.Iteration, phase2Out.Err)

	if phase2Out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Phase 2 failed: expected OutcomeProcessEffect, got %v with error: %v", phase2Out.Kind, phase2Out.Err)
	}

	if phase2Out.Signal != tools.SignalStoriesSubmitted {
		t.Fatalf("Phase 2: expected signal %s, got %s", tools.SignalStoriesSubmitted, phase2Out.Signal)
	}

	storiesData, ok := utils.SafeAssert[map[string]any](phase2Out.EffectData)
	if !ok {
		t.Fatalf("Phase 2: expected EffectData to be map[string]any, got %T", phase2Out.EffectData)
	}

	requirements, ok := utils.SafeAssert[[]any](storiesData["requirements"])
	if !ok {
		t.Fatalf("Phase 2: expected requirements to be []any, got %T", storiesData["requirements"])
	}

	t.Logf("Phase 2 completed: generated %d stories WITH MIDDLEWARE CHAIN", len(requirements))

	if len(requirements) == 0 {
		t.Fatal("Phase 2: expected at least one story to be generated")
	}

	for i, req := range requirements {
		if reqMap, ok := utils.SafeAssert[map[string]any](req); ok {
			title, _ := reqMap["title"].(string)
			storyType, _ := reqMap["story_type"].(string)
			t.Logf("  Story %d: %s (type: %s)", i+1, title, storyType)
		}
	}

	t.Log("=== TEST PASSED (WITH MIDDLEWARE) ===")
}
