package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"orchestrator/pkg/config"
)

// createTestConfig creates a minimal valid config for testing.
func createTestConfig() config.Config {
	return config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      2,
			CoderModel:     config.ModelClaudeSonnetLatest,
			ArchitectModel: config.ModelOpenAIO3Mini,
		},
	}
}

// TestNewKernel tests kernel creation and initialization.
func TestNewKernel(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "kernel-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config
	cfg := createTestConfig()

	ctx := context.Background()
	kernel, err := NewKernel(ctx, &cfg, tempDir)

	if err != nil {
		t.Fatalf("NewKernel failed: %v", err)
	}

	if kernel == nil {
		t.Fatal("NewKernel returned nil kernel")
	}

	// Verify kernel components are initialized
	if kernel.Config == nil {
		t.Error("Kernel config is nil")
	}
	if kernel.Logger == nil {
		t.Error("Kernel logger is nil")
	}
	if kernel.Dispatcher == nil {
		t.Error("Kernel dispatcher is nil")
	}
	if kernel.Database == nil {
		t.Error("Kernel database is nil")
	}
	if kernel.PersistenceChannel == nil {
		t.Error("Kernel persistence channel is nil")
	}
	if kernel.BuildService == nil {
		t.Error("Kernel build service is nil")
	}
	if kernel.WebServer == nil {
		t.Error("Kernel web server is nil")
	}

	// Test cleanup
	if err := kernel.Stop(); err != nil {
		t.Errorf("Kernel.Stop() failed: %v", err)
	}
}

// TestKernelLifecycle tests kernel start/stop lifecycle.
func TestKernelLifecycle(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "kernel-lifecycle-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config
	cfg := createTestConfig()

	ctx := context.Background()
	kernel, err := NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("NewKernel failed: %v", err)
	}
	defer kernel.Stop()

	// Test start
	if err := kernel.Start(); err != nil {
		t.Fatalf("Kernel.Start() failed: %v", err)
	}

	// Verify running state
	if !kernel.running {
		t.Error("Kernel should be in running state after Start()")
	}

	// Test double start (should return error)
	if err := kernel.Start(); err == nil {
		t.Error("Kernel.Start() should fail when already running")
	}

	// Test stop
	if err := kernel.Stop(); err != nil {
		t.Errorf("Kernel.Stop() failed: %v", err)
	}

	// Verify stopped state
	if kernel.running {
		t.Error("Kernel should not be in running state after Stop()")
	}

	// Test double stop (should be safe)
	if err := kernel.Stop(); err != nil {
		t.Errorf("Kernel.Stop() should be safe to call multiple times: %v", err)
	}
}

// TestKernelDatabaseInitialization tests database setup.
func TestKernelDatabaseInitialization(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "kernel-db-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config
	cfg := createTestConfig()

	ctx := context.Background()
	kernel, err := NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("NewKernel failed: %v", err)
	}
	defer kernel.Stop()

	// Verify database file was created
	dbPath := filepath.Join(tempDir, ".maestro", "maestro.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("Database file was not created at %s", dbPath)
	}

	// Verify database connection works
	if err := kernel.Database.Ping(); err != nil {
		t.Errorf("Database ping failed: %v", err)
	}
}

// TestKernelPersistenceWorker tests the persistence worker goroutine.
func TestKernelPersistenceWorker(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "kernel-persistence-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config
	cfg := createTestConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	kernel, err := NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("NewKernel failed: %v", err)
	}
	defer kernel.Stop()

	// Start kernel to initialize persistence worker
	if err := kernel.Start(); err != nil {
		t.Fatalf("Kernel.Start() failed: %v", err)
	}

	// Verify persistence channel is available and not closed
	select {
	case <-kernel.PersistenceChannel:
		// Channel should be empty initially, so this should not trigger
		t.Error("Persistence channel should be empty initially")
	default:
		// This is expected - channel is empty
	}

	// Test that channel is not closed
	select {
	case _, ok := <-kernel.PersistenceChannel:
		if !ok {
			t.Error("Persistence channel should not be closed")
		}
	case <-time.After(100 * time.Millisecond):
		// Expected - channel should be open but empty
	}
}

// TestKernelContextCancellation tests proper cleanup on context cancellation.
func TestKernelContextCancellation(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "kernel-cancel-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config
	cfg := createTestConfig()

	ctx, cancel := context.WithCancel(context.Background())
	kernel, err := NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("NewKernel failed: %v", err)
	}
	defer kernel.Stop()

	// Start kernel
	if err := kernel.Start(); err != nil {
		t.Fatalf("Kernel.Start() failed: %v", err)
	}

	// Cancel context and verify graceful shutdown
	cancel()

	// Wait a bit for cleanup to occur
	time.Sleep(100 * time.Millisecond)

	// Context should be cancelled
	select {
	case <-kernel.ctx.Done():
		// Expected - context should be done
	default:
		t.Error("Kernel context should be done after cancellation")
	}
}
