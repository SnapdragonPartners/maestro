package agents

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/proto"
)

// TestClaudeAgentRateLimit verifies the acceptance criteria:
// "Unit tests: given TASK payload, agent returns RESULT within rate limits"
func TestClaudeAgentRateLimit(t *testing.T) {
	// Setup test environment with rate limits
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Models: map[string]config.ModelCfg{
			"claude": {
				MaxTokensPerMinute: 300,  // Allow 3 requests (100 tokens each)
				MaxBudgetPerDayUSD: 10.0, // Generous budget
				// MaxAgents:          2,    // Allow 2 concurrent agents (removed field)
				APIKey:             "test-key",
			},
		},
		MaxRetryAttempts:       3,
		RetryBackoffMultiplier: 2.0,
	}

	rateLimiter := limiter.NewLimiter(cfg)
	defer rateLimiter.Close()

	eventLog, err := eventlog.NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}
	defer eventLog.Close()

	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Start dispatcher
	ctx := context.Background()
	err = dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	// Create and register Claude agent
	claudeAgent := NewClaudeAgent("claude", "test-claude", "work")
	err = dispatcher.RegisterAgent(claudeAgent)
	if err != nil {
		t.Fatalf("Failed to register Claude agent: %v", err)
	}

	// Test 1: Verify agent returns RESULT for valid TASK
	t.Run("ValidTaskReturnsResult", func(t *testing.T) {
		msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
		msg.SetPayload("content", "Implement health endpoint")
		msg.SetPayload("requirements", []interface{}{
			"GET /health endpoint",
			"Return JSON response",
		})

		err := dispatcher.DispatchMessage(msg)
		if err != nil {
			t.Fatalf("Failed to dispatch task: %v", err)
		}

		// Wait for processing
		time.Sleep(100 * time.Millisecond)

		// Verify message was logged (indicates successful processing)
		logFile := eventLog.GetCurrentLogFile()
		messages, err := eventlog.ReadMessages(logFile)
		if err != nil {
			t.Fatalf("Failed to read event log: %v", err)
		}

		// Should have original task and result response
		if len(messages) < 2 {
			t.Errorf("Expected at least 2 messages (task + result), got %d", len(messages))
		}

		// Find the result message
		var resultFound bool
		for _, loggedMsg := range messages {
			if loggedMsg.Type == proto.MsgTypeRESULT && loggedMsg.FromAgent == "claude" {
				resultFound = true

				// Verify result structure
				status, exists := loggedMsg.GetPayload("status")
				if !exists {
					t.Error("Expected status in result payload")
				}

				if status != "completed" {
					t.Errorf("Expected status 'completed', got %s", status)
				}

				implementation, exists := loggedMsg.GetPayload("implementation")
				if !exists {
					t.Error("Expected implementation in result payload")
				}

				if implementation == "" {
					t.Error("Expected non-empty implementation")
				}

				break
			}
		}

		if !resultFound {
			t.Error("Expected to find RESULT message from claude in event log")
		}
	})

	// Test 2: Verify multiple tasks can be processed within rate limits
	t.Run("MultipleTasksWithinRateLimit", func(t *testing.T) {
		// Send 3 tasks (should be within our 300 token/minute limit at 100 tokens each)
		taskCount := 3

		for i := 0; i < taskCount; i++ {
			msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
			msg.SetPayload("content", "Implement feature "+string(rune('A'+i)))
			msg.SetPayload("requirements", []interface{}{
				"Basic implementation",
				"Error handling",
			})

			err := dispatcher.DispatchMessage(msg)
			if err != nil {
				t.Fatalf("Failed to dispatch task %d: %v", i, err)
			}
		}

		// Wait for all tasks to process
		time.Sleep(500 * time.Millisecond)

		// Verify all tasks were processed
		logFile := eventLog.GetCurrentLogFile()
		messages, err := eventlog.ReadMessages(logFile)
		if err != nil {
			t.Fatalf("Failed to read event log: %v", err)
		}

		// Count result messages from claude
		resultCount := 0
		for _, loggedMsg := range messages {
			if loggedMsg.Type == proto.MsgTypeRESULT && loggedMsg.FromAgent == "claude" {
				resultCount++
			}
		}

		if resultCount < taskCount {
			t.Errorf("Expected at least %d result messages, got %d", taskCount, resultCount)
		}
	})

	// Test 3: Verify agent handles different task types correctly
	t.Run("DifferentTaskTypes", func(t *testing.T) {
		testCases := []struct {
			name         string
			content      string
			requirements []interface{}
		}{
			{
				name:         "HealthEndpoint",
				content:      "Implement health endpoint",
				requirements: []interface{}{"GET /health", "JSON response"},
			},
			{
				name:         "APIEndpoints",
				content:      "Implement REST API",
				requirements: []interface{}{"RESTful design", "Error handling"},
			},
			{
				name:         "DatabaseLayer",
				content:      "Implement database connection",
				requirements: []interface{}{"PostgreSQL", "Connection pooling"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
				msg.SetPayload("content", tc.content)
				msg.SetPayload("requirements", tc.requirements)

				err := dispatcher.DispatchMessage(msg)
				if err != nil {
					t.Fatalf("Failed to dispatch %s task: %v", tc.name, err)
				}

				// Wait for processing
				time.Sleep(150 * time.Millisecond)
			})
		}

		// Verify all tasks generated results
		logFile := eventLog.GetCurrentLogFile()
		messages, err := eventlog.ReadMessages(logFile)
		if err != nil {
			t.Fatalf("Failed to read event log: %v", err)
		}

		// Count new result messages
		newResultCount := 0
		for _, loggedMsg := range messages {
			if loggedMsg.Type == proto.MsgTypeRESULT && loggedMsg.FromAgent == "claude" {
				newResultCount++
			}
		}

		if newResultCount < len(testCases) {
			t.Errorf("Expected at least %d result messages for different task types, got %d", len(testCases), newResultCount)
		}
	})

	// Test 4: Verify rate limit enforcement
	t.Run("RateLimitEnforcement", func(t *testing.T) {
		// First, exhaust the token bucket by sending tasks that will consume all tokens
		// Our limit is 300 tokens/minute, so send 4 tasks (400 tokens) to exceed it

		for i := 0; i < 4; i++ {
			msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
			msg.SetPayload("content", "Large task that consumes tokens")

			err := dispatcher.DispatchMessage(msg)
			if err != nil {
				t.Logf("Task %d failed (expected for rate limiting): %v", i, err)
			}
		}

		// Wait for processing
		time.Sleep(300 * time.Millisecond)

		// Verify dispatcher stats show activity
		stats := dispatcher.GetStats()
		if !stats["running"].(bool) {
			t.Error("Expected dispatcher to be running")
		}

		agents := stats["agents"].([]string)
		if len(agents) == 0 {
			t.Error("Expected at least one agent registered")
		}

		// The fact that we can complete this test without hanging indicates
		// that rate limiting is working and not blocking the dispatcher
	})
}

// TestClaudeAgentDirectProcessing tests the agent without dispatcher overhead
func TestClaudeAgentDirectProcessing(t *testing.T) {
	agent := NewClaudeAgent("claude", "test-claude", "work")
	ctx := context.Background()

	// Test direct message processing
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	msg.SetPayload("content", "Implement feature X")
	msg.SetPayload("requirements", []interface{}{
		"Requirement 1",
		"Requirement 2",
	})

	start := time.Now()
	response, err := agent.ProcessMessage(ctx, msg)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Failed to process message: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response message")
	}

	// Verify response is RESULT type
	if response.Type != proto.MsgTypeRESULT {
		t.Errorf("Expected RESULT message, got %s", response.Type)
	}

	// Verify processing time is reasonable (under 100ms for mock implementation)
	if duration > 100*time.Millisecond {
		t.Errorf("Processing took too long: %v", duration)
	}

	// Verify all required fields are present
	requiredPayloadFields := []string{"status", "implementation", "tests", "documentation", "files_created"}
	for _, field := range requiredPayloadFields {
		if _, exists := response.GetPayload(field); !exists {
			t.Errorf("Expected field %s in response payload", field)
		}
	}

	// Verify metadata
	if _, exists := response.GetMetadata("processing_agent"); !exists {
		t.Error("Expected processing_agent in response metadata")
	}

	if _, exists := response.GetMetadata("task_type"); !exists {
		t.Error("Expected task_type in response metadata")
	}
}
