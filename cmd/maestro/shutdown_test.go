package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/config"
)

func TestShutdownHandler(t *testing.T) {
	// Create temporary project directory
	tempDir, err := os.MkdirTemp("", "shutdown-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tempDir, config.ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	// Load config
	err = config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}

	// Create orchestrator
	orchestrator, err := NewOrchestrator(&cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Test context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Orchestrator initialization is handled internally in NewOrchestrator

	// Test shutdown
	err = orchestrator.Shutdown(ctx)
	if err != nil {
		t.Errorf("Failed to shutdown orchestrator: %v", err)
	}
}

func TestGracefulShutdown(t *testing.T) {
	// Create temporary project directory
	tempDir, err := os.MkdirTemp("", "shutdown-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tempDir, config.ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	// Load config
	err = config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}

	// Create orchestrator
	orchestrator, err := NewOrchestrator(&cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Test that graceful shutdown completes within timeout
	// Use a short timeout context to force cancellation during shutdown wait
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Shutdown should complete quickly due to context cancellation
	start := time.Now()
	err = orchestrator.Shutdown(ctx)
	elapsed := time.Since(start)

	// Should complete within reasonable time (not the full 7.5s wait)
	if elapsed > 4*time.Second {
		t.Errorf("Shutdown took too long: %v", elapsed)
	}

	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}
