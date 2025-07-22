package logx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Use the same contextKey type as defined in context_debug_test.go.

// TestDefaultLogDirectory verifies that the default log directory is set correctly.
func TestDefaultLogDirectory(t *testing.T) {
	// Clear any environment variables that might affect the test.
	oldDebugLogDir := os.Getenv("DEBUG_LOG_DIR")
	oldDebugDir := os.Getenv("DEBUG_DIR")
	defer func() {
		os.Setenv("DEBUG_LOG_DIR", oldDebugLogDir)
		os.Setenv("DEBUG_DIR", oldDebugDir)
	}()

	os.Unsetenv("DEBUG_LOG_DIR")
	os.Unsetenv("DEBUG_DIR")

	// Reset config and reinitialize.
	initDebugFromEnv()

	// Get the default log directory.
	defaultLogDir := getDefaultLogDir()

	// Verify it ends with "/logs".
	if !strings.HasSuffix(defaultLogDir, "logs") {
		t.Errorf("Expected default log directory to end with 'logs', got: %s", defaultLogDir)
	}

	// Verify it's not the current directory.
	if defaultLogDir == "." || defaultLogDir == "./" {
		t.Error("Default log directory should not be current directory")
	}

	// Check that the project root is found correctly.
	projectRoot := getProjectRoot()
	expectedLogDir := filepath.Join(projectRoot, "logs")

	if defaultLogDir != expectedLogDir {
		t.Errorf("Expected default log dir %s, got %s", expectedLogDir, defaultLogDir)
	}

	t.Logf("Project root: %s", projectRoot)
	t.Logf("Default log directory: %s", defaultLogDir)
}

// TestGetProjectRoot verifies the project root detection.
func TestGetProjectRoot(t *testing.T) {
	projectRoot := getProjectRoot()

	// Should find a directory that contains go.mod.
	goModPath := filepath.Join(projectRoot, "go.mod")
	if _, err := os.Stat(goModPath); err != nil {
		t.Errorf("Expected to find go.mod at %s, but got error: %v", goModPath, err)
	}

	// Should not be empty or just ".".
	if projectRoot == "" || projectRoot == "." {
		t.Errorf("Project root should not be empty or current directory, got: %s", projectRoot)
	}

	t.Logf("Project root found: %s", projectRoot)
}

// TestDebugToFileWithDefaultDir verifies file logging uses the correct directory.
func TestDebugToFileWithDefaultDir(t *testing.T) {
	// Create a temporary directory to avoid polluting the real logs directory.
	tempDir := t.TempDir()

	// Set up debug config to use temporary directory.
	SetDebugConfig(true, true, tempDir)

	// Use typed context key to avoid collisions.
	type contextKey string
	const agentIDKey contextKey = "agent_id"
	ctx := context.WithValue(context.Background(), agentIDKey, "test-agent")

	// Test the global DebugToFile function.
	DebugToFile(ctx, "test", "test_file.log", "Test message: %s", "hello")

	// Verify the file was created in the correct location.
	expectedPath := filepath.Join(tempDir, "test_file.log")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("Expected debug file to be created at %s, but got error: %v", expectedPath, err)
	}

	// Read and verify content.
	content, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("Failed to read debug file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Test message: hello") {
		t.Errorf("Expected debug message in file, got: %s", contentStr)
	}

	if !strings.Contains(contentStr, "[test]") {
		t.Errorf("Expected domain in file, got: %s", contentStr)
	}

	t.Logf("Debug file created successfully at: %s", expectedPath)
}

// TestEnvironmentVariableOverride verifies that environment variables override the default.
func TestEnvironmentVariableOverride(t *testing.T) {
	// Set a custom debug log directory.
	customDir := "/tmp/custom_test_logs"
	os.Setenv("DEBUG_LOG_DIR", customDir)
	defer os.Unsetenv("DEBUG_LOG_DIR")

	// Reinitialize.
	initDebugFromEnv()

	// Verify the custom directory is used.
	debugMutex.RLock()
	actualLogDir := debugConfig.LogDir
	debugMutex.RUnlock()

	if actualLogDir != customDir {
		t.Errorf("Expected custom log dir %s, got %s", customDir, actualLogDir)
	}

	t.Logf("Custom log directory correctly set to: %s", actualLogDir)
}
