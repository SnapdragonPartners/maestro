package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/proto"
)

// TestE2ESmokeTest verifies the acceptance criteria:
// "All logs show TASK → RESULT cycle. No budget or rate errors."
// 
// This test runs the full message pipeline:
// orchestrator → architect → claude → orchestrator
// Using the health endpoint story from stories/001.md
func TestE2ESmokeTest(t *testing.T) {
	// Setup test environment
	tmpDir := t.TempDir()
	
	// Create stories directory and copy health story
	storiesDir := filepath.Join(tmpDir, "stories")
	err := os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create stories directory: %v", err)
	}

	// Use the actual health story from stories/001.md
	healthStoryPath := "stories/001.md"
	healthStoryContent, err := os.ReadFile(healthStoryPath)
	if err != nil {
		// If the file doesn't exist, create a sample health story
		healthStoryContent = []byte(`# Health Endpoint Implementation

This story implements a basic health check endpoint for the orchestrator system.

## Description

The system needs a simple health check endpoint that external monitoring systems can use to verify the orchestrator is running and responsive.

## Requirements

- GET /health endpoint
- Return 200 OK status when healthy
- JSON response format
- Include system status information
- Include timestamp in response
- Response time under 100ms

## Acceptance Criteria

- Endpoint responds to GET requests at ` + "`/health`" + `
- Returns valid JSON with proper content-type header
- Includes status field with "healthy" value
- Includes timestamp field with current time
- Returns 200 HTTP status code
- Response is fast and lightweight

## Technical Notes

- Use standard HTTP status codes
- Ensure endpoint doesn't require authentication
- Consider future extensibility for detailed health checks
- Keep response minimal for performance

## Example Response

` + "```json" + `
{
  "status": "healthy",
  "timestamp": "2025-06-09T15:30:45Z",
  "version": "1.0.0"
}
` + "```")
	}

	testStoryPath := filepath.Join(storiesDir, "001.md")
	err = os.WriteFile(testStoryPath, healthStoryContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create test health story: %v", err)
	}

	// Create test config with generous limits to avoid rate/budget errors
	cfg := &config.Config{
		Models: map[string]config.ModelCfg{
			"claude_sonnet4": {
				MaxTokensPerMinute: 10000,  // High limit
				MaxBudgetPerDayUSD: 100.0,  // High budget
				CpmTokensIn:        0.003,
				CpmTokensOut:       0.015,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "claude-test", ID: "001", Type: "coder", WorkDir: "./work/claude-test"},
				},
			},
			"openai_o3": {
				MaxTokensPerMinute: 5000,
				MaxBudgetPerDayUSD: 50.0,
				CpmTokensIn:        0.004,
				CpmTokensOut:       0.016,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "architect-test", ID: "001", Type: "architect", WorkDir: "./work/architect-test"},
				},
			},
		},
		GracefulShutdownTimeoutSec: 10,
		MaxRetryAttempts:           3,
		RetryBackoffMultiplier:     2.0,
	}

	// Change to temp directory for this test
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	// Create orchestrator
	orchestrator, err := NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Start orchestrator
	ctx := context.Background()
	err = orchestrator.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		orchestrator.Shutdown(shutdownCtx)
	}()

	t.Log("🚀 Starting E2E smoke test: Health endpoint story processing")

	// Step 1: Send story processing request to architect (simulating orchestrator request)
	t.Log("📋 Step 1: Sending health story to architect agent")
	
	storyTaskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, "orchestrator", "architect")
	storyTaskMsg.SetPayload("story_id", "001")
	storyTaskMsg.SetMetadata("test_type", "e2e_smoke_test")
	storyTaskMsg.SetMetadata("story_name", "health_endpoint")

	err = orchestrator.dispatcher.DispatchMessage(storyTaskMsg)
	if err != nil {
		t.Fatalf("Failed to dispatch story to architect: %v", err)
	}

	// Step 2: Wait for the full pipeline to complete
	t.Log("⏳ Step 2: Waiting for architect → claude → result pipeline")
	
	// Give enough time for:
	// 1. Architect to process story and send TASK to Claude
	// 2. Claude to process TASK and return RESULT to architect  
	// 3. All messages to be logged
	time.Sleep(2 * time.Second)

	// Step 3: Analyze event logs to verify TASK → RESULT cycle
	t.Log("📊 Step 3: Analyzing event logs for complete TASK → RESULT cycle")

	logFile := orchestrator.eventLog.GetCurrentLogFile()
	messages, err := eventlog.ReadMessages(logFile)
	if err != nil {
		t.Fatalf("Failed to read event log: %v", err)
	}

	if len(messages) == 0 {
		t.Fatal("No messages found in event log")
	}

	t.Logf("Found %d messages in event log", len(messages))

	// Verify the expected message flow
	var foundStoryTask, foundArchitectResult, foundClaudeTask, foundClaudeResult bool
	var rateErrors, budgetErrors int

	for i, msg := range messages {
		t.Logf("Message %d: %s → %s (%s) at %s", 
			i+1, msg.FromAgent, msg.ToAgent, msg.Type, msg.Timestamp.Format("15:04:05"))

		// Track the expected message flow
		switch {
		case msg.Type == proto.MsgTypeTASK && msg.FromAgent == "orchestrator" && msg.ToAgent == "openai_o3:001":
			foundStoryTask = true
			t.Log("  ✓ Found story task: orchestrator → openai_o3:001")

		case msg.Type == proto.MsgTypeRESULT && msg.FromAgent == "openai_o3:001" && msg.ToAgent == "orchestrator":
			foundArchitectResult = true
			t.Log("  ✓ Found architect result: openai_o3:001 → orchestrator")

		case msg.Type == proto.MsgTypeTASK && msg.FromAgent == "openai_o3:001" && msg.ToAgent == "claude_sonnet4:001":
			foundClaudeTask = true
			t.Log("  ✓ Found coding task: openai_o3:001 → claude_sonnet4:001")
			
			// Verify task contains expected content
			if content, exists := msg.GetPayload("content"); exists {
				if contentStr, ok := content.(string); ok {
					if !contains(contentStr, "Health") {
						t.Error("Claude task should contain 'Health' in content")
					}
				}
			}

		case msg.Type == proto.MsgTypeRESULT && msg.FromAgent == "claude_sonnet4:001" && msg.ToAgent == "openai_o3:001":
			foundClaudeResult = true
			t.Log("  ✓ Found claude result: claude_sonnet4:001 → openai_o3:001")
			
			// Verify result contains expected fields
			if status, exists := msg.GetPayload("status"); exists {
				if status != "completed" {
					t.Errorf("Expected claude result status 'completed', got %s", status)
				}
			}
			
			if impl, exists := msg.GetPayload("implementation"); exists {
				if implStr, ok := impl.(string); ok {
					if !contains(implStr, "health") {
						t.Error("Claude implementation should contain 'health'")
					}
				}
			}

		case msg.Type == proto.MsgTypeERROR:
			errorPayload, _ := msg.GetPayload("error")
			if errorStr, ok := errorPayload.(string); ok {
				if contains(errorStr, "rate limit") {
					rateErrors++
					t.Errorf("Found rate limit error: %s", errorStr)
				}
				if contains(errorStr, "budget") {
					budgetErrors++
					t.Errorf("Found budget error: %s", errorStr)
				}
			}
		}
	}

	// Step 4: Verify acceptance criteria
	t.Log("✅ Step 4: Verifying acceptance criteria")

	// Verify complete TASK → RESULT cycle
	if !foundStoryTask {
		t.Error("Missing: orchestrator → openai_o3:001 TASK message")
	}
	if !foundArchitectResult {
		t.Error("Missing: openai_o3:001 → orchestrator RESULT message")
	}
	if !foundClaudeTask {
		t.Error("Missing: openai_o3:001 → claude_sonnet4:001 TASK message")
	}
	if !foundClaudeResult {
		t.Error("Missing: claude_sonnet4:001 → openai_o3:001 RESULT message")
	}

	// Verify no rate or budget errors
	if rateErrors > 0 {
		t.Errorf("Found %d rate limit errors (acceptance criteria: no rate errors)", rateErrors)
	}
	if budgetErrors > 0 {
		t.Errorf("Found %d budget errors (acceptance criteria: no budget errors)", budgetErrors)
	}

	// Step 5: Verify system state
	t.Log("🔍 Step 5: Verifying final system state")

	stats := orchestrator.dispatcher.GetStats()
	if !stats["running"].(bool) {
		t.Error("Dispatcher should still be running")
	}

	agents := stats["agents"].([]string)
	expectedAgents := []string{"openai_o3:001", "claude_sonnet4:001"}
	if len(agents) != len(expectedAgents) {
		t.Errorf("Expected %d agents, got %d", len(expectedAgents), len(agents))
	}

	// Verify rate limiter status
	for model := range cfg.Models {
		tokens, budget, agentCount, err := orchestrator.rateLimiter.GetStatus(model)
		if err != nil {
			t.Errorf("Failed to get rate limiter status for %s: %v", model, err)
		} else {
			t.Logf("Rate limiter status for %s: %d tokens, $%.2f budget, %d agents", 
				model, tokens, budget, agentCount)
		}
	}

	// Final validation
	if foundStoryTask && foundArchitectResult && foundClaudeTask && foundClaudeResult && 
	   rateErrors == 0 && budgetErrors == 0 {
		t.Log("🎉 E2E smoke test PASSED!")
		t.Log("   ✓ Complete TASK → RESULT cycle verified")
		t.Log("   ✓ No rate limit errors")
		t.Log("   ✓ No budget errors")
		t.Log("   ✓ Health endpoint story processed successfully")
		t.Log("   ✓ Multi-agent orchestration system working correctly")
	} else {
		t.Error("❌ E2E smoke test FAILED - not all acceptance criteria met")
	}
}

// TestE2EMultipleStories tests processing multiple stories in sequence
func TestE2EMultipleStories(t *testing.T) {
	// Setup test environment
	tmpDir := t.TempDir()
	storiesDir := filepath.Join(tmpDir, "stories")
	err := os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create stories directory: %v", err)
	}

	// Create multiple test stories
	stories := map[string]string{
		"001.md": `# Health Endpoint
Basic health check implementation.
- GET /health endpoint
- Return JSON response`,
		
		"002.md": `# User API
User management endpoints.
- CRUD operations
- Authentication required`,
		
		"003.md": `# Database Layer
Database connection and queries.
- PostgreSQL connection
- Connection pooling`,
	}

	for filename, content := range stories {
		err := os.WriteFile(filepath.Join(storiesDir, filename), []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create story %s: %v", filename, err)
		}
	}

	// Create test config
	cfg := &config.Config{
		Models: map[string]config.ModelCfg{
			"claude_sonnet4": {
				MaxTokensPerMinute: 5000,
				MaxBudgetPerDayUSD: 50.0,
				CpmTokensIn:        0.003,
				CpmTokensOut:       0.015,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "claude-multi", ID: "001", Type: "coder", WorkDir: "./work/claude-multi"},
				},
			},
			"openai_o3": {
				MaxTokensPerMinute: 2000,
				MaxBudgetPerDayUSD: 20.0,
				CpmTokensIn:        0.004,
				CpmTokensOut:       0.016,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "architect-multi", ID: "001", Type: "architect", WorkDir: "./work/architect-multi"},
				},
			},
		},
		GracefulShutdownTimeoutSec: 10,
		MaxRetryAttempts:           3,
		RetryBackoffMultiplier:     2.0,
	}

	// Change to temp directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	// Create and start orchestrator
	orchestrator, err := NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}

	ctx := context.Background()
	err = orchestrator.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		orchestrator.Shutdown(shutdownCtx)
	}()

	t.Log("🚀 Testing multiple story processing")

	// Process each story
	for storyID := range stories {
		storyName := storyID[:3] // Get just the number part
		t.Logf("📋 Processing story %s", storyName)

		storyTaskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, "orchestrator", "architect")
		storyTaskMsg.SetPayload("story_id", storyName)
		storyTaskMsg.SetMetadata("test_type", "multiple_stories")

		err = orchestrator.dispatcher.DispatchMessage(storyTaskMsg)
		if err != nil {
			t.Errorf("Failed to dispatch story %s: %v", storyName, err)
			continue
		}

		// Small delay between stories
		time.Sleep(300 * time.Millisecond)
	}

	// Wait for all processing to complete
	time.Sleep(2 * time.Second)

	// Verify all stories were processed
	logFile := orchestrator.eventLog.GetCurrentLogFile()
	messages, err := eventlog.ReadMessages(logFile)
	if err != nil {
		t.Fatalf("Failed to read event log: %v", err)
	}

	// Count RESULT messages from claude (one per story)
	claudeResults := 0
	for _, msg := range messages {
		if msg.Type == proto.MsgTypeRESULT && msg.FromAgent == "claude_sonnet4:001" {
			claudeResults++
		}
	}

	expectedResults := len(stories)
	if claudeResults != expectedResults {
		t.Errorf("Expected %d claude results, got %d", expectedResults, claudeResults)
	} else {
		t.Logf("✓ All %d stories processed successfully", expectedResults)
	}
}

// Helper functions are defined in shutdown_test.go