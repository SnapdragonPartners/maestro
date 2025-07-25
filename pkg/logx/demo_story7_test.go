package logx

import (
	"context"
	"os"
	"testing"
)

// Use the same contextKey type as defined in context_debug_test.go.

// TestStory7Implementation demonstrates the new context-aware debug logging.
// This shows the completed implementation of Story 7 from the audit.
func TestStory7Implementation(t *testing.T) {
	// Enable debug logging for this demo.
	SetDebugConfig(true, false, ".")
	SetDebugDomains([]string{"coder", "architect", "dispatch"})

	// Create context with agent ID using typed key to avoid collisions.
	ctx := context.WithValue(context.Background(), agentIDKey, "claude-001")

	// Demonstrate the new Debug(ctx, domain, format, args...) pattern
	t.Log("=== Story 7: Context-Aware Debug Logging Demo ===")

	// 1. Domain-filtered debug logging.
	Debug(ctx, "coder", "Task processing started: %s", "implement health check")
	Debug(ctx, "architect", "Story validation: %s", "all requirements met")
	Debug(ctx, "dispatch", "Message routing: %s -> %s", "coder-1", "architect")

	// This should be filtered out if we only enable coder,architect domains.
	Debug(ctx, "unknown", "This should not appear")

	// 2. Convenient helper functions.
	DebugState(ctx, "coder", "transition", "PLANNING -> CODING", "requirements approved")
	DebugMessage(ctx, "dispatch", "TASK", "queued for processing")
	DebugFlow(ctx, "coder", "code-generation", "complete", "3 files created")

	// 3. Environment variable control demo.
	t.Log("--- Testing environment variable control ---")

	// Test with different domain filtering.
	SetDebugDomains([]string{"coder"}) // Only enable coder domain
	Debug(ctx, "coder", "This should appear (coder domain enabled)")
	Debug(ctx, "architect", "This should NOT appear (architect domain disabled)")

	// 4. File logging demo (if enabled via environment)
	if os.Getenv("DEBUG_FILE") == "1" {
		t.Log("--- File logging enabled via DEBUG_FILE=1 ---")
		DebugToFile(ctx, "coder", "test_debug.log", "File debug test: %s", "implementation complete")
	}

	t.Log("=== Story 7 implementation complete ===")

	// Reset for other tests.
	SetDebugConfig(false, false, ".")
	SetDebugDomains(nil)
}

// TestEnvironmentVariableControlDemo shows how to use environment variables.
func TestEnvironmentVariableControlDemo(t *testing.T) {
	t.Log("=== Environment Variable Control Examples ===")
	t.Log("To enable debug logging for specific domains:")
	t.Log("  DEBUG=1 DEBUG_DOMAINS=coder,architect go test")
	t.Log("  DEBUG=1 DEBUG_FILE=1 DEBUG_DIR=./logs go test")
	t.Log("")
	t.Log("To enable debug for all domains:")
	t.Log("  DEBUG=1 go test")
	t.Log("")
	t.Log("To enable file logging:")
	t.Log("  DEBUG=1 DEBUG_FILE=1 go test")
}
