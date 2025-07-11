package agent

import (
	"fmt"
	"sync"
	"testing"

	"orchestrator/pkg/state"
)

// TestStateDataThreadSafety tests concurrent access to different keys (no race expected)
func TestStateDataThreadSafety(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	sm := NewBaseStateMachine("thread-safety-test", StateWaiting, store, nil)

	const numGoroutines = 10
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup

	// Each goroutine writes to its own unique key
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				// Use unique key per goroutine to avoid expected conflicts
				key := fmt.Sprintf("key_%d", goroutineID)
				expectedValue := goroutineID*1000 + j

				// Write
				sm.SetStateData(key, expectedValue)

				// Read back immediately
				if actualValue, exists := sm.GetStateValue(key); exists {
					if actualValue != expectedValue {
						t.Errorf("Goroutine %d: expected %v, got %v", goroutineID, expectedValue, actualValue)
					}
				} else {
					t.Errorf("Goroutine %d: key %s should exist", goroutineID, key)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify all values are still correct
	stateData := sm.GetStateData()
	for i := 0; i < numGoroutines; i++ {
		key := fmt.Sprintf("key_%d", i)
		expectedValue := i*1000 + (operationsPerGoroutine - 1) // Last value written

		if actualValue, exists := stateData[key]; exists {
			if actualValue != expectedValue {
				t.Errorf("Final check: goroutine %d key %s expected %v, got %v", i, key, expectedValue, actualValue)
			}
		} else {
			t.Errorf("Final check: key %s should exist", key)
		}
	}

	t.Logf("Thread safety test completed: %d goroutines × %d operations each", numGoroutines, operationsPerGoroutine)
}

// TestConcurrentReadersAndWriters tests the specific concern about SetStateData thread safety
func TestConcurrentReadersAndWriters(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	sm := NewBaseStateMachine("concurrent-test", StateWaiting, store, nil)

	const numWriters = 5
	const numReaders = 5
	const numOperations = 200

	var wg sync.WaitGroup

	// Writers - each writes to its own set of keys
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("writer_%d_key_%d", writerID, j)
				value := writerID*10000 + j
				sm.SetStateData(key, value)
			}
		}(i)
	}

	// Readers - continuously read all state data
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// These operations should not panic or cause data races
				_ = sm.GetStateData()
				_ = sm.GetCurrentState()

				// Try to read some keys that might exist
				for writerID := 0; writerID < numWriters; writerID++ {
					key := fmt.Sprintf("writer_%d_key_%d", writerID, j%10)
					_, _ = sm.GetStateValue(key)
				}
			}
		}(i)
	}

	wg.Wait()
	t.Log("Concurrent readers and writers test completed successfully")
}
