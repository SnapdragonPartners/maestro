package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/config"
)

func TestOrchestratorBasic(t *testing.T) {
	// Skip if no API keys available
	if os.Getenv("ANTHROPIC_API_KEY") == "" || os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping e2e test: API keys not available")
	}

	// Create temporary project directory
	tempDir, err := os.MkdirTemp("", "e2e-test")
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

	// Orchestrator initialization is handled internally in NewOrchestrator

	// Test that orchestrator was created successfully
	if orchestrator == nil {
		t.Fatal("Expected orchestrator to be created")
	}

	// Basic validation
	if orchestrator.config != &cfg {
		t.Error("Expected orchestrator to use provided config")
	}

	if orchestrator.projectDir != tempDir {
		t.Errorf("Expected projectDir %s, got %s", tempDir, orchestrator.projectDir)
	}
}

func TestOrchestratorShutdown(t *testing.T) {
	// Skip if no API keys available
	if os.Getenv("ANTHROPIC_API_KEY") == "" || os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping e2e test: API keys not available")
	}

	// Create temporary project directory
	tempDir, err := os.MkdirTemp("", "e2e-test")
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
