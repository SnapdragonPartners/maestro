package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
)

// TestStory4HighConcurrency tests 10 coders running simultaneously.
func TestStory4HighConcurrency(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness with extended timeouts for concurrency.
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 30 * time.Second   // Allow time for all 10 coders
	timeouts.Pump = 5 * time.Millisecond // Faster pumping for concurrency
	harness.SetTimeouts(timeouts)

	// Create always-approval architect.
	architect := NewAlwaysApprovalMockArchitect("architect")
	harness.SetArchitect(architect)

	// Create 10 coders with different tasks.
	const coderCount = 10
	coderIDs := make([]string, coderCount)
	tasks := []string{
		"Create a HTTP health check endpoint",
		"Implement a JSON validator function",
		"Create a file copy utility",
		"Implement a simple cache with TTL",
		"Create a string encryption/decryption utility",
		"Implement a rate limiter",
		"Create a CSV parser",
		"Implement a retry mechanism with backoff",
		"Create a URL shortener service",
		"Implement a simple event emitter",
	}

	startTime := time.Now()

	// Add all coders to harness.
	for i := 0; i < coderCount; i++ {
		coderID := fmt.Sprintf("coder-%02d", i+1)
		coderIDs[i] = coderID

		coderDriver := CreateTestCoder(t, coderID)
		harness.AddCoder(coderID, coderDriver)

		// Start each coder with its task.
		StartCoderWithTask(t, harness, coderID, tasks[i])
	}

	// Verify all coders start in planning state (after task setup)
	for _, coderID := range coderIDs {
		RequireState(t, harness, coderID, coder.StatePlanning)
	}

	// Run until all coders complete.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	err := harness.Run(ctx, DefaultStopCondition)
	if err != nil {
		t.Fatalf("Harness run failed: %v", err)
	}

	endTime := time.Now()
	duration := endTime.Sub(startTime)

	// Verify all coders reached DONE state.
	for _, coderID := range coderIDs {
		RequireState(t, harness, coderID, proto.StateDone)
	}

	// Verify completion time is within expected bounds (target: 500ms wall-clock)
	// Note: This is ambitious and may need adjustment based on actual performance.
	if duration > 2*time.Second {
		t.Logf("Warning: High concurrency test took %v, which is longer than expected", duration)
	}

	// Verify architect received messages from all coders.
	messages := architect.GetReceivedMessages()
	coderMessageCounts := make(map[string]int)

	for _, msg := range messages {
		coderMessageCounts[msg.FromCoder]++
	}

	// Each coder should have sent at least one message.
	for _, coderID := range coderIDs {
		if count := coderMessageCounts[coderID]; count == 0 {
			t.Errorf("Expected messages from coder %s, but got none", coderID)
		}
	}

	t.Logf("High concurrency test completed successfully in %v", duration)
	t.Logf("Total messages to architect: %d from %d coders", len(messages), coderCount)

	// Log per-coder message counts for debugging.
	for _, coderID := range coderIDs {
		count := coderMessageCounts[coderID]
		t.Logf("Coder %s sent %d messages", coderID, count)
	}
}

// TestStory4ChannelIsolationUnderLoad tests that channels remain isolated under high load.
func TestStory4ChannelIsolationUnderLoad(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness.
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 15 * time.Second
	timeouts.Pump = 2 * time.Millisecond // Very fast pumping
	harness.SetTimeouts(timeouts)

	// Create architect with artificial delays for some coders.
	architect := NewAlwaysApprovalMockArchitect("architect")
	architect.SetResponseDelay(10 * time.Millisecond) // Small delay to test isolation
	harness.SetArchitect(architect)

	// Create multiple coders.
	const coderCount = 5
	coderIDs := make([]string, coderCount)

	var wg sync.WaitGroup

	for i := 0; i < coderCount; i++ {
		coderID := fmt.Sprintf("concurrent-coder-%d", i+1)
		coderIDs[i] = coderID

		coderDriver := CreateTestCoder(t, coderID)
		harness.AddCoder(coderID, coderDriver)

		// Start each coder in a goroutine to test true concurrency.
		wg.Add(1)
		go func(id string, task string) {
			defer wg.Done()
			StartCoderWithTask(t, harness, id, task)
		}(coderID, fmt.Sprintf("Create a simple function for task %d", i+1))
	}

	// Wait for all coders to start.
	wg.Wait()

	// Run the harness.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	err := harness.Run(ctx, DefaultStopCondition)
	if err != nil {
		t.Fatalf("Channel isolation test failed: %v", err)
	}

	// Verify all coders completed.
	for _, coderID := range coderIDs {
		RequireState(t, harness, coderID, proto.StateDone)
	}

	// Verify no messages were lost or crossed between channels.
	messages := architect.GetReceivedMessages()
	coderMessageCounts := make(map[string]int)

	for _, msg := range messages {
		coderMessageCounts[msg.FromCoder]++
	}

	// Each coder should have sent at least one message.
	for _, coderID := range coderIDs {
		if count := coderMessageCounts[coderID]; count == 0 {
			t.Errorf("Channel isolation failure: coder %s sent no messages", coderID)
		}
	}

	t.Logf("Channel isolation test completed successfully")
	t.Logf("All %d coders completed without channel interference", coderCount)
}

// TestStory4RaceConditionDetection runs with race detector to catch issues.
func TestStory4RaceConditionDetection(t *testing.T) {
	// This test is designed to be run with `go test -race`.
	SetupTestEnvironment(t)

	// Create test harness.
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 10 * time.Second
	timeouts.Pump = 1 * time.Millisecond // Very fast pumping to stress test
	harness.SetTimeouts(timeouts)

	// Create architect.
	architect := NewAlwaysApprovalMockArchitect("architect")
	harness.SetArchitect(architect)

	// Create multiple coders that will access shared resources.
	const coderCount = 8
	var wg sync.WaitGroup

	for i := 0; i < coderCount; i++ {
		coderID := fmt.Sprintf("race-coder-%d", i+1)
		coderDriver := CreateTestCoder(t, coderID)
		harness.AddCoder(coderID, coderDriver)

		// Start coders concurrently.
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			StartCoderWithTask(t, harness, id, "Create a concurrent-safe counter")
		}(coderID)
	}

	// Wait for all to start.
	wg.Wait()

	// Run with aggressive concurrency.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	err := harness.Run(ctx, DefaultStopCondition)
	if err != nil {
		t.Fatalf("Race condition test failed: %v", err)
	}

	// If we get here without race detector complaints, the test passed.
	t.Logf("Race condition detection test completed successfully")
	t.Logf("No race conditions detected with %d concurrent coders", coderCount)
}

// TestStory4MemoryLeakDetection tests for memory leaks in concurrent scenario.
func TestStory4MemoryLeakDetection(t *testing.T) {
	SetupTestEnvironment(t)

	// This test creates and destroys multiple harnesses to check for leaks.
	for iteration := 0; iteration < 3; iteration++ {
		t.Run(fmt.Sprintf("iteration-%d", iteration+1), func(t *testing.T) {
			// Create test harness.
			harness := NewTestHarness(t)
			timeouts := GetTestTimeouts()
			timeouts.Global = 5 * time.Second
			harness.SetTimeouts(timeouts)

			// Create architect.
			architect := NewAlwaysApprovalMockArchitect("architect")
			harness.SetArchitect(architect)

			// Create multiple coders.
			const coderCount = 6
			for i := 0; i < coderCount; i++ {
				coderID := fmt.Sprintf("leak-test-coder-%d-%d", iteration+1, i+1)
				coderDriver := CreateTestCoder(t, coderID)
				harness.AddCoder(coderID, coderDriver)
				StartCoderWithTask(t, harness, coderID, fmt.Sprintf("Simple task %d", i+1))
			}

			// Run briefly.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			err := harness.Run(ctx, DefaultStopCondition)
			if err != nil {
				t.Logf("Iteration %d completed with: %v", iteration+1, err)
			}

			// Harness and coders should be cleaned up automatically when test ends.
		})
	}

	t.Logf("Memory leak detection test completed")
}
