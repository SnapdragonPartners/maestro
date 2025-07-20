package coder

import (
	"context"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/state"
	"orchestrator/pkg/tools"
)

// BenchmarkContainerLifecycle benchmarks container readonly→readwrite transitions
func BenchmarkContainerLifecycle(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "perf-container-test")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		b.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	responses := []agent.CompletionResponse{
		{Content: "Benchmark response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("perf-test-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		b.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Test readonly container startup
		startTime := time.Now()
		err := driver.configureWorkspaceMount(ctx, true, "benchmark-readonly")
		if err != nil {
			b.Logf("Readonly container failed (expected in test env): %v", err)
		}
		readonlyTime := time.Since(startTime)

		// Test readwrite container reconfiguration
		startTime = time.Now()
		err = driver.configureWorkspaceMount(ctx, false, "benchmark-readwrite")
		if err != nil {
			b.Logf("Readwrite container failed (expected in test env): %v", err)
		}
		readwriteTime := time.Since(startTime)

		// Log timing for analysis
		if i == 0 {
			b.Logf("Container lifecycle timings:")
			b.Logf("  Readonly setup: %v", readonlyTime)
			b.Logf("  Readwrite reconfigure: %v", readwriteTime)
			b.Logf("  Total transition: %v", readonlyTime+readwriteTime)
		}
	}
}

// BenchmarkContextPreservation benchmarks context save/restore operations
func BenchmarkContextPreservation(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "perf-context-test")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		b.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	responses := []agent.CompletionResponse{
		{Content: "Context benchmark response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("context-perf-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		b.Fatalf("Failed to create driver: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Benchmark planning context save
		startTime := time.Now()
		driver.storePlanningContext(driver.BaseStateMachine)
		saveTime := time.Since(startTime)

		// Benchmark planning context restore
		startTime = time.Now()
		driver.restorePlanningContext(driver.BaseStateMachine)
		restoreTime := time.Since(startTime)

		// Test other context types
		startTime = time.Now()
		driver.storeCodingContext(driver.BaseStateMachine)
		driver.restoreCodingContext(driver.BaseStateMachine)
		codingContextTime := time.Since(startTime)

		if i == 0 {
			b.Logf("Context preservation timings:")
			b.Logf("  Planning save: %v", saveTime)
			b.Logf("  Planning restore: %v", restoreTime)
			b.Logf("  Coding context cycle: %v", codingContextTime)
		}
	}
}

// BenchmarkHelperMethods benchmarks the helper methods used in enhanced planning
func BenchmarkHelperMethods(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "perf-helpers-test")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		b.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	responses := []agent.CompletionResponse{
		{Content: "Helper benchmark response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("helpers-perf-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		b.Fatalf("Failed to create driver: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Benchmark helper methods
		startTime := time.Now()

		// Planning helpers
		_ = driver.getExplorationHistory()
		_ = driver.getFilesExamined()
		_ = driver.getCurrentFindings()

		// Coding helpers
		_ = driver.getCodingProgress()
		_ = driver.getFilesCreated()
		_ = driver.getCurrentTask()

		// Fixing helpers
		_ = driver.getFixingProgress()
		_ = driver.getTestFailures()
		_ = driver.getCurrentFixes()

		helperTime := time.Since(startTime)

		if i == 0 {
			b.Logf("Helper methods timing: %v for 9 method calls", helperTime)
			b.Logf("Average per helper method: %v", helperTime/9)
		}
	}
}

// BenchmarkToolDefinitions benchmarks tool definition creation and access
func BenchmarkToolDefinitions(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		startTime := time.Now()

		// Create tools directly (simulates what happens during planning)
		askQuestionTool := tools.NewAskQuestionTool()
		submitPlanTool := tools.NewSubmitPlanTool()

		// Access tool definitions
		_ = askQuestionTool.Definition()
		_ = submitPlanTool.Definition()

		toolTime := time.Since(startTime)

		if i == 0 {
			b.Logf("Tool definitions timing: %v", toolTime)
		}
	}
}

// BenchmarkStateDataOperations benchmarks state data set/get operations
func BenchmarkStateDataOperations(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "perf-state-test")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		b.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	responses := []agent.CompletionResponse{
		{Content: "State benchmark response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("state-perf-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		b.Fatalf("Failed to create driver: %v", err)
	}

	testData := map[string]interface{}{
		"task_content":          "Benchmark task content",
		"plan":                  "Benchmark implementation plan",
		"plan_confidence":       "HIGH",
		"exploration_summary":   "Benchmark exploration summary",
		"planning_completed_at": time.Now().UTC(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		startTime := time.Now()

		// Benchmark SetStateData operations
		for key, value := range testData {
			driver.BaseStateMachine.SetStateData(key, value)
		}

		// Benchmark GetStateValue operations
		for key := range testData {
			_, _ = driver.BaseStateMachine.GetStateValue(key)
		}

		stateTime := time.Since(startTime)

		if i == 0 {
			b.Logf("State operations timing: %v for %d set + %d get operations",
				stateTime, len(testData), len(testData))
			b.Logf("Average per operation: %v", stateTime/time.Duration(len(testData)*2))
		}
	}
}
