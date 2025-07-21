package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// TestBaseStateMachineRaceConditions verifies thread safety of BaseStateMachine
func TestBaseStateMachineRaceConditions(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	sm := NewBaseStateMachine("race-test-agent", proto.StateWaiting, store, nil)

	// Number of goroutines and operations per goroutine
	numGoroutines := 10
	operationsPerGoroutine := 100

	var wg sync.WaitGroup

	// Start multiple goroutines that read and write state data concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				// Concurrent state data operations
				key := "test_key"
				value := goroutineID*1000 + j

				// Write operation
				sm.SetStateData(key, value)

				// Read operation
				if readValue, exists := sm.GetStateValue(key); exists {
					if readValue != value {
						t.Logf("Race condition detected: expected %v, got %v", value, readValue)
					}
				}

				// Get state data copy
				stateData := sm.GetStateData()
				if len(stateData) == 0 {
					t.Log("Empty state data encountered")
				}

				// Get current state
				currentState := sm.GetCurrentState()
				if currentState == "" {
					t.Log("Empty state encountered")
				}

				// Small delay to increase chance of race conditions
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Start a goroutine that performs state transitions
	wg.Add(1)
	go func() {
		defer wg.Done()
		states := []proto.State{proto.StateWaiting, proto.StateDone, proto.StateError}

		for i := 0; i < operationsPerGoroutine; i++ {
			targetState := states[i%len(states)]
			ctx := context.Background()

			// Perform state transition
			err := sm.TransitionTo(ctx, targetState, map[string]any{
				"transition_id": i,
				"timestamp":     time.Now(),
			})

			if err != nil {
				t.Logf("Transition error: %v", err)
			}

			time.Sleep(time.Microsecond)
		}
	}()

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify final state is consistent
	finalStateData := sm.GetStateData()
	if len(finalStateData) == 0 {
		t.Error("Expected state data to be present after concurrent operations")
	}

	t.Logf("Race test completed successfully with %d goroutines and %d operations each",
		numGoroutines, operationsPerGoroutine)
}

// TestStateDataConcurrency specifically tests concurrent SetStateData and GetStateValue calls
func TestStateDataConcurrency(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	sm := NewBaseStateMachine("concurrency-test-agent", proto.StateWaiting, store, nil)

	const numWriters = 5
	const numReaders = 5
	const numOperations = 200

	var wg sync.WaitGroup

	// Start writer goroutines
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := "writer_key"
				value := writerID*1000 + j
				sm.SetStateData(key, value)
			}
		}(i)
	}

	// Start reader goroutines
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Read operations
				sm.GetStateValue("writer_key")
				sm.GetStateData()
				sm.GetCurrentState()
			}
		}(i)
	}

	wg.Wait()
	t.Log("Concurrent state data operations completed successfully")
}
