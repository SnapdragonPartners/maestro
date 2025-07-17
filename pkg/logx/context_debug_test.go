package logx

import (
	"context"
	"os"
	"strings"
	"testing"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const agentIDKey contextKey = "agent_id"

func TestContextDebugLogging(t *testing.T) {
	// Reset environment
	os.Unsetenv("DEBUG")
	os.Unsetenv("DEBUG_DOMAINS")
	os.Unsetenv("DEBUG_FILE")
	os.Unsetenv("DEBUG_DIR")

	// Reinitialize config
	initDebugFromEnv()

	// Enable debug logging
	SetDebugConfig(true, false, ".")

	// Test basic context debug logging
	ctx := context.WithValue(context.Background(), agentIDKey, "test-agent")

	// This should work since debug is enabled and no domain filtering
	Debug(ctx, "coder", "Test message: %s", "hello")

	// Test domain filtering
	SetDebugDomains([]string{"coder", "architect"})

	// These should work
	Debug(ctx, "coder", "Coder message")
	Debug(ctx, "architect", "Architect message")

	// This should be filtered out
	Debug(ctx, "dispatch", "Dispatch message")

	// Test convenience functions
	DebugState(ctx, "coder", "transition", "PLANNING", "starting new task")
	DebugMessage(ctx, "coder", "TASK", "received task message")
	DebugFlow(ctx, "coder", "code generation", "complete", "generated 5 files")
}

func TestEnvironmentVariableConfiguration(t *testing.T) {
	// Test DEBUG=1
	os.Setenv("DEBUG", "1")
	os.Setenv("DEBUG_DOMAINS", "coder,architect")

	// Reinitialize
	initDebugFromEnv()

	if !IsDebugEnabled() {
		t.Error("Expected debug to be enabled via DEBUG=1")
	}

	if !IsDebugEnabledForDomain("coder") {
		t.Error("Expected coder domain to be enabled")
	}

	if !IsDebugEnabledForDomain("architect") {
		t.Error("Expected architect domain to be enabled")
	}

	if IsDebugEnabledForDomain("dispatch") {
		t.Error("Expected dispatch domain to be disabled")
	}

	// Clean up
	os.Unsetenv("DEBUG")
	os.Unsetenv("DEBUG_DOMAINS")
	initDebugFromEnv()
}

func TestDebugToFileFunction(t *testing.T) {
	// Setup temporary directory
	tempDir := t.TempDir()

	// Enable debug with file logging
	SetDebugConfig(true, true, tempDir)

	ctx := context.WithValue(context.Background(), agentIDKey, "test-agent")

	// Test debug to file
	DebugToFile(ctx, "coder", "test_debug.log", "Test debug message: %s", "file content")

	// Verify file was created
	content, err := os.ReadFile(tempDir + "/test_debug.log")
	if err != nil {
		t.Fatalf("Failed to read debug file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Test debug message: file content") {
		t.Errorf("Expected debug message in file, got: %s", contentStr)
	}

	if !strings.Contains(contentStr, "[coder]") {
		t.Errorf("Expected domain in file, got: %s", contentStr)
	}

	if !strings.Contains(contentStr, "[test-agent]") {
		t.Errorf("Expected agent ID in file, got: %s", contentStr)
	}
}
