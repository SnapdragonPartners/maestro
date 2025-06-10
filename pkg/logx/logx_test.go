package logx

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestNewLogger(t *testing.T) {
	logger := NewLogger("test-agent")

	if logger.GetAgentID() != "test-agent" {
		t.Errorf("Expected agent ID 'test-agent', got '%s'", logger.GetAgentID())
	}

	if logger.logger == nil {
		t.Error("Expected logger to be initialized")
	}
}

func TestLogFormat(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	logger := NewLogger("architect")
	logger.logger = log.New(&buf, "", 0)

	logger.Info("Test message with %s", "formatting")

	output := buf.String()

	// Check for required components
	if !strings.Contains(output, "[architect]") {
		t.Errorf("Expected agent ID in output, got: %s", output)
	}

	if !strings.Contains(output, "INFO") {
		t.Errorf("Expected log level in output, got: %s", output)
	}

	if !strings.Contains(output, "Test message with formatting") {
		t.Errorf("Expected formatted message in output, got: %s", output)
	}

	// Check timestamp format (basic check)
	if !strings.Contains(output, "T") || !strings.Contains(output, "Z") {
		t.Errorf("Expected ISO timestamp in output, got: %s", output)
	}
}

func TestLogLevels(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("test-agent")
	logger.logger = log.New(&buf, "", 0)

	tests := []struct {
		level    Level
		logFunc  func(string, ...interface{})
		expected string
	}{
		{LevelDebug, logger.Debug, "DEBUG"},
		{LevelInfo, logger.Info, "INFO"},
		{LevelWarn, logger.Warn, "WARN"},
		{LevelError, logger.Error, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			buf.Reset()
			tt.logFunc("test message")

			output := buf.String()
			if !strings.Contains(output, tt.expected) {
				t.Errorf("Expected level '%s' in output, got: %s", tt.expected, output)
			}
		})
	}
}

func TestWithAgentID(t *testing.T) {
	originalLogger := NewLogger("original-agent")
	newLogger := originalLogger.WithAgentID("new-agent")

	if newLogger.GetAgentID() != "new-agent" {
		t.Errorf("Expected new agent ID 'new-agent', got '%s'", newLogger.GetAgentID())
	}

	if originalLogger.GetAgentID() != "original-agent" {
		t.Errorf("Expected original agent ID unchanged, got '%s'", originalLogger.GetAgentID())
	}

	// Both should share the same underlying logger
	if newLogger.logger != originalLogger.logger {
		t.Error("Expected loggers to share the same underlying log.Logger")
	}
}

func TestLogFormatting(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("claude")
	logger.logger = log.New(&buf, "", 0)

	logger.Info("Processing task %d with priority %s", 123, "high")

	output := buf.String()

	if !strings.Contains(output, "Processing task 123 with priority high") {
		t.Errorf("Expected formatted message, got: %s", output)
	}
}

func TestMultipleAgents(t *testing.T) {
	var buf bytes.Buffer

	architect := NewLogger("architect")
	architect.logger = log.New(&buf, "", 0)

	claude := NewLogger("claude")
	claude.logger = log.New(&buf, "", 0)

	architect.Info("Creating task")
	claude.Info("Executing task")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != 2 {
		t.Errorf("Expected 2 log lines, got %d", len(lines))
	}

	if !strings.Contains(lines[0], "[architect]") {
		t.Errorf("Expected first line to contain [architect], got: %s", lines[0])
	}

	if !strings.Contains(lines[1], "[claude]") {
		t.Errorf("Expected second line to contain [claude], got: %s", lines[1])
	}
}

func TestLogLevelConstants(t *testing.T) {
	expectedLevels := map[Level]string{
		LevelDebug: "DEBUG",
		LevelInfo:  "INFO",
		LevelWarn:  "WARN",
		LevelError: "ERROR",
	}

	for level, expected := range expectedLevels {
		if string(level) != expected {
			t.Errorf("Expected level constant %s to equal '%s', got '%s'",
				expected, expected, string(level))
		}
	}
}

func TestTimestampFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("test")
	logger.logger = log.New(&buf, "", 0)

	logger.Info("timestamp test")

	output := buf.String()

	// Extract timestamp (should be between first [ and ])
	start := strings.Index(output, "[")
	end := strings.Index(output, "]")

	if start == -1 || end == -1 || end <= start {
		t.Fatalf("Could not find timestamp in output: %s", output)
	}

	timestamp := output[start+1 : end]

	// Try to parse the timestamp
	_, err := time.Parse("2006-01-02T15:04:05.000Z", timestamp)
	if err != nil {
		t.Errorf("Invalid timestamp format '%s': %v", timestamp, err)
	}
}

func ExampleLogger_usage() {
	// Create loggers for different agents
	architect := NewLogger("architect")
	claude := NewLogger("claude")

	// Log different levels
	architect.Info("Starting story processing")
	architect.Debug("Reading story file: %s", "stories/001.md")

	claude.Info("Received task from architect")
	claude.Warn("High token usage detected: %d tokens", 950)
	claude.Error("Failed to connect to API: %v", "timeout")

	// Create a new logger with different agent ID
	o3 := architect.WithAgentID("o3")
	o3.Info("Review task completed")
}

func TestExampleUsage(t *testing.T) {
	// This test just ensures the example compiles and runs
	ExampleLogger_usage()
}
