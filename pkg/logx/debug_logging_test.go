package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDebugToggle verifies debug logging can be enabled/disabled
func TestDebugToggle(t *testing.T) {
	// Reset to known clean state for this test
	SetDebugConfig(false, false, ".")
	SetDebugDomains(nil)
	
	logger := NewLogger("test-agent")
	
	// Initially debug should be disabled
	if IsDebugEnabled() {
		t.Error("Debug should be disabled by default")
	}
	
	// Enable debug logging
	SetDebugConfig(true, false, "")
	
	if !IsDebugEnabled() {
		t.Error("Debug should be enabled after SetDebugConfig")
	}
	
	// Disable debug logging
	SetDebugConfig(false, false, "")
	
	if IsDebugEnabled() {
		t.Error("Debug should be disabled after SetDebugConfig(false)")
	}
	
	// Test that debug messages respect the toggle
	// This test is more about ensuring the API works correctly
	logger.Debug("This should not appear when disabled")
	
	SetDebugConfig(true, false, "")
	logger.Debug("This should appear when enabled")
}

// TestDebugToFile verifies file-based debug logging
func TestDebugToFile(t *testing.T) {
	tempDir := t.TempDir()
	logger := NewLogger("test-agent")
	
	// Enable debug with file logging
	SetDebugConfig(true, true, tempDir)
	
	testMessage := "Test debug message with data: %s %d"
	testArgs := []interface{}{"hello", 42}
	filename := "test_debug.log"
	
	// Write debug message to file
	logger.DebugToFile(filename, testMessage, testArgs...)
	
	// Verify file was created
	filePath := filepath.Join(tempDir, filename)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("Debug file was not created: %s", filePath)
		return
	}
	
	// Read and verify file contents
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read debug file: %v", err)
	}
	
	contentStr := string(content)
	
	// Should contain timestamp, agent ID, and message
	if !strings.Contains(contentStr, "[test-agent]") {
		t.Error("Debug file should contain agent ID")
	}
	
	if !strings.Contains(contentStr, "DEBUG:") {
		t.Error("Debug file should contain DEBUG level")
	}
	
	if !strings.Contains(contentStr, "Test debug message with data: hello 42") {
		t.Error("Debug file should contain formatted message")
	}
	
	// Cleanup
	SetDebugConfig(false, false, "")
}

// TestDebugToFile_DisabledNoFiles verifies no files created when debug disabled
func TestDebugToFile_DisabledNoFiles(t *testing.T) {
	tempDir := t.TempDir()
	logger := NewLogger("test-agent")
	
	// Ensure debug is disabled
	SetDebugConfig(false, true, tempDir)
	
	filename := "should_not_exist.log"
	logger.DebugToFile(filename, "This should not create a file")
	
	// Verify file was NOT created
	filePath := filepath.Join(tempDir, filename)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("Debug file should not be created when debug is disabled")
	}
}

// TestDebugToFile_NoFileLogging verifies console-only debug mode
func TestDebugToFile_NoFileLogging(t *testing.T) {
	tempDir := t.TempDir()
	logger := NewLogger("test-agent")
	
	// Enable debug but disable file logging
	SetDebugConfig(true, false, tempDir)
	
	filename := "should_not_exist.log"
	logger.DebugToFile(filename, "This should only go to console")
	
	// Verify file was NOT created
	filePath := filepath.Join(tempDir, filename)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("Debug file should not be created when file logging is disabled")
	}
}

// TestDebugState verifies state transition logging convenience method
func TestDebugState(t *testing.T) {
	logger := NewLogger("test-coder")
	
	// Enable debug
	SetDebugConfig(true, false, "")
	defer SetDebugConfig(false, false, "")
	
	// Test basic state logging
	logger.DebugState("transition", "PLANNING")
	logger.DebugState("enter", "CODING", "from PLANNING")
	
	// No errors should occur - main verification is that the API works
}

// TestDebugMessage verifies message processing logging convenience method
func TestDebugMessage(t *testing.T) {
	logger := NewLogger("test-dispatcher")
	
	// Enable debug
	SetDebugConfig(true, false, "")
	defer SetDebugConfig(false, false, "")
	
	// Test message logging
	logger.DebugMessage("TASK", "Processing task from architect")
	logger.DebugMessage("RESULT", "Sending result to coder-1")
	
	// No errors should occur - main verification is that the API works
}

// TestConcurrentDebugConfig verifies thread-safe configuration changes
func TestConcurrentDebugConfig(t *testing.T) {
	const numGoroutines = 10
	const numIterations = 100
	
	done := make(chan bool, numGoroutines)
	
	// Start multiple goroutines toggling debug config
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()
			
			logger := NewLogger("concurrent-agent")
			
			for j := 0; j < numIterations; j++ {
				// Toggle debug state
				enabled := (j % 2) == 0
				SetDebugConfig(enabled, false, "")
				
				// Try to log
				logger.Debug("Concurrent debug test %d-%d", id, j)
				
				// Check state
				IsDebugEnabled()
			}
		}(i)
	}
	
	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		select {
		case <-done:
			// Success
		case <-time.After(5 * time.Second):
			t.Fatal("Concurrent test timed out")
		}
	}
}

// TestDebugFileCreation verifies debug log directory creation
func TestDebugFileCreation(t *testing.T) {
	tempDir := t.TempDir()
	nestedDir := filepath.Join(tempDir, "logs", "debug")
	
	logger := NewLogger("test-agent")
	
	// Enable debug with nested directory
	SetDebugConfig(true, true, nestedDir)
	
	// Write debug message
	logger.DebugToFile("nested_test.log", "Testing nested directory creation")
	
	// Verify directory was created
	if _, err := os.Stat(nestedDir); os.IsNotExist(err) {
		t.Errorf("Debug directory was not created: %s", nestedDir)
	}
	
	// Verify file was created
	filePath := filepath.Join(nestedDir, "nested_test.log")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("Debug file was not created: %s", filePath)
	}
	
	// Cleanup
	SetDebugConfig(false, false, "")
}

// TestDebugBackwardsCompatibility verifies existing debug patterns work
func TestDebugBackwardsCompatibility(t *testing.T) {
	logger := NewLogger("legacy-agent")
	
	// Test that regular Debug() still works when enabled
	SetDebugConfig(true, false, "")
	defer SetDebugConfig(false, false, "")
	
	// These should not panic or error
	logger.Debug("Legacy debug message")
	logger.Info("Info message")
	logger.Warn("Warning message")
	logger.Error("Error message")
}

// TestReplaceScatteredPatterns demonstrates the replacement of scattered debug patterns
func TestReplaceScatteredPatterns(t *testing.T) {
	tempDir := t.TempDir()
	logger := NewLogger("pattern-test")
	
	SetDebugConfig(true, true, tempDir)
	defer SetDebugConfig(false, false, "")
	
	// Old pattern: fmt.Sprintf + os.WriteFile
	// debugMsg := fmt.Sprintf("DEBUG: handleResultMessage called - status=%s\n", status)
	// os.WriteFile("handle_result_debug.log", []byte(debugMsg), 0644)
	
	// New pattern: DebugToFile
	status := "approved"
	logger.DebugToFile("handle_result_debug.log", "handleResultMessage called - status=%s", status)
	
	// Verify file exists and has content
	filePath := filepath.Join(tempDir, "handle_result_debug.log")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read debug file: %v", err)
	}
	
	contentStr := string(content)
	if !strings.Contains(contentStr, "handleResultMessage called - status=approved") {
		t.Error("Debug file should contain the formatted message")
	}
}