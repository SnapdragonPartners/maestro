package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/utils"
)

func TestBuildService(t *testing.T) {
	// Create temporary directory for test.
	tempDir, err := os.MkdirTemp("", "build-service-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal Go project.
	goMod := `module test-project
go 1.21
`
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	// Create main.go.
	mainGo := `package main
import "fmt"
func main() {
	fmt.Println("Hello, World!")
}`
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatalf("Failed to create main.go: %v", err)
	}

	// Create build service.
	service := NewBuildService()

	// Test backend detection.
	t.Run("Backend Detection", func(t *testing.T) {
		info, err := service.GetBackendInfo(tempDir)
		if err != nil {
			t.Fatalf("Failed to get backend info: %v", err)
		}

		if info.Name != "go" {
			t.Errorf("Expected backend 'go', got '%s'", info.Name)
		}

		if info.ProjectRoot != tempDir {
			t.Errorf("Expected project root '%s', got '%s'", tempDir, info.ProjectRoot)
		}

		expectedOps := []string{"build", "test", "lint", "run"}
		if len(info.Operations) != len(expectedOps) {
			t.Errorf("Expected %d operations, got %d", len(expectedOps), len(info.Operations))
		}
	})

	// Test build operation.
	t.Run("Build Operation", func(t *testing.T) {
		req := &BuildRequest{
			ProjectRoot: tempDir,
			Operation:   "build",
			Timeout:     30,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		response, err := service.ExecuteBuild(ctx, req)
		if err != nil {
			t.Errorf("Build execution failed: %v", err)
		}

		if response.Backend != "go" {
			t.Errorf("Expected backend 'go', got '%s'", response.Backend)
		}

		if response.Operation != "build" {
			t.Errorf("Expected operation 'build', got '%s'", response.Operation)
		}

		if response.RequestID == "" {
			t.Error("Expected non-empty request ID")
		}

		if response.Duration <= 0 {
			t.Error("Expected positive duration")
		}

		// Check that output contains expected build commands.
		if response.Success && response.Output != "" {
			t.Logf("Build output: %s", response.Output)
		}
	})

	// Test test operation.
	t.Run("Test Operation", func(t *testing.T) {
		req := &BuildRequest{
			ProjectRoot: tempDir,
			Operation:   "test",
			Timeout:     30,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		response, err := service.ExecuteBuild(ctx, req)
		if err != nil {
			t.Errorf("Test execution failed: %v", err)
		}

		if response.Backend != "go" {
			t.Errorf("Expected backend 'go', got '%s'", response.Backend)
		}

		if response.Operation != "test" {
			t.Errorf("Expected operation 'test', got '%s'", response.Operation)
		}

		// Tests might fail due to no test files, but should not error.
		t.Logf("Test result: success=%t, output=%s", response.Success, response.Output)
	})

	// Test invalid operation.
	t.Run("Invalid Operation", func(t *testing.T) {
		req := &BuildRequest{
			ProjectRoot: tempDir,
			Operation:   "invalid",
			Timeout:     30,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		response, err := service.ExecuteBuild(ctx, req)
		if err == nil {
			t.Error("Expected error for invalid operation")
		}

		if response.Success {
			t.Error("Expected success=false for invalid operation")
		}

		if response.Error == "" {
			t.Error("Expected error message for invalid operation")
		}
	})

	// Test cache functionality.
	t.Run("Cache Functionality", func(t *testing.T) {
		// Clear cache.
		service.ClearCache()

		// First detection should populate cache.
		info1, err := service.GetBackendInfo(tempDir)
		if err != nil {
			t.Fatalf("Failed to get backend info: %v", err)
		}

		// Second detection should use cache.
		info2, err := service.GetBackendInfo(tempDir)
		if err != nil {
			t.Fatalf("Failed to get backend info: %v", err)
		}

		if info1.Name != info2.Name {
			t.Errorf("Cache inconsistency: %s != %s", info1.Name, info2.Name)
		}

		// Check cache status.
		status := service.GetCacheStatus()
		cacheSize, err := utils.GetMapField[int](status, "cache_size")
		if err != nil {
			t.Errorf("Failed to get cache_size: %v", err)
		} else if cacheSize != 1 {
			t.Errorf("Expected cache size 1, got %v", cacheSize)
		}
	})
}

func TestBuildRequestValidation(t *testing.T) {
	service := NewBuildService()

	// Test empty project root.
	req := &BuildRequest{
		ProjectRoot: "",
		Operation:   "build",
	}

	ctx := context.Background()
	response, err := service.ExecuteBuild(ctx, req)
	if err == nil {
		t.Error("Expected error for empty project root")
	}

	if response.Success {
		t.Error("Expected success=false for empty project root")
	}

	// Test empty operation.
	req = &BuildRequest{
		ProjectRoot: "/tmp",
		Operation:   "",
	}

	response, err = service.ExecuteBuild(ctx, req)
	if err == nil {
		t.Error("Expected error for empty operation")
	}

	if response.Success {
		t.Error("Expected success=false for empty operation")
	}
}
