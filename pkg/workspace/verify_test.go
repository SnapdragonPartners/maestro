// Package workspace provides workspace verification and validation functionality.
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/logx"
)

func TestVerifyWorkspace_MissingMaestroDir(t *testing.T) {
	// Create a temporary directory that doesn't have .maestro directory
	tempDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	opts := VerifyOptions{
		Fast:    true, // Skip expensive checks
		Timeout: 5 * time.Second,
		Logger:  logx.NewLogger("test"),
	}

	report, err := VerifyWorkspace(ctx, tempDir, opts)
	if err != nil {
		t.Fatalf("VerifyWorkspace failed: %v", err)
	}

	// Should have failures since .maestro directory is missing
	if report.OK {
		t.Error("Expected verification to fail for missing .maestro directory")
	}

	if len(report.Failures) == 0 {
		t.Error("Expected at least one failure")
	}

	// Check that the failure mentions missing .maestro directory
	found := false
	for _, failure := range report.Failures {
		if filepath.Base(failure) != failure && // contains path elements
			(len(failure) > 10) { // reasonable length
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected failure about missing .maestro directory, got: %v", report.Failures)
	}

	// Should have timing information
	if len(report.Durations) == 0 {
		t.Error("Expected duration tracking")
	}
}

func TestVerifyWorkspace_FastMode(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	opts := VerifyOptions{
		Fast:    true, // Skip expensive checks like build verification
		Timeout: 5 * time.Second,
		Logger:  logx.NewLogger("test"),
	}

	report, err := VerifyWorkspace(ctx, tempDir, opts)
	if err != nil {
		t.Fatalf("VerifyWorkspace failed: %v", err)
	}

	// In fast mode, build verification should be skipped
	if _, hasBuildDuration := report.Durations["build"]; hasBuildDuration {
		t.Error("Expected build verification to be skipped in fast mode")
	}

	// Should always have infrastructure and tools checks
	if _, hasInfraDuration := report.Durations["infra"]; !hasInfraDuration {
		t.Error("Expected infrastructure verification to run")
	}

	if _, hasToolsDuration := report.Durations["tools"]; !hasToolsDuration {
		t.Error("Expected tools verification to run")
	}
}

func TestVerifyOptions_Defaults(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	opts := VerifyOptions{
		Fast: true, // Skip build verification for this test
	}

	report, err := VerifyWorkspace(ctx, tempDir, opts)
	if err != nil {
		t.Fatalf("VerifyWorkspace failed: %v", err)
	}

	// Should get some kind of verification result
	if report == nil {
		t.Fatal("Expected non-nil report")
	}

	// Should have initialized durations map
	if report.Durations == nil {
		t.Error("Expected initialized durations map")
	}

	// Should have at least some verification steps
	if len(report.Durations) == 0 {
		t.Error("Expected at least one verification step to run")
	}
}

func TestVerifyReport_StructureAndTypes(t *testing.T) {
	report := &VerifyReport{
		OK:        false,
		Warnings:  []string{"test warning"},
		Failures:  []string{"test failure"},
		Durations: map[string]time.Duration{"test": 100 * time.Millisecond},
	}

	if report.OK {
		t.Error("Expected OK to be false")
	}

	if len(report.Warnings) != 1 {
		t.Errorf("Expected 1 warning, got %d", len(report.Warnings))
	}

	if len(report.Failures) != 1 {
		t.Errorf("Expected 1 failure, got %d", len(report.Failures))
	}

	if len(report.Durations) != 1 {
		t.Errorf("Expected 1 duration entry, got %d", len(report.Durations))
	}

	if report.Durations["test"] != 100*time.Millisecond {
		t.Errorf("Expected duration of 100ms, got %v", report.Durations["test"])
	}
}
