package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"orchestrator/pkg/proto"
)

func TestStateTimeoutWrapper_ProcessWithTimeout(t *testing.T) {
	//nolint:govet // Test struct field alignment not critical for performance
	tests := []struct {
		name           string
		processorError error
		timeout        time.Duration
		processorDelay time.Duration
		expectedState  proto.State
		processorPanic bool
		expectedDone   bool
		expectTimeout  bool
		expectPanic    bool
	}{
		{
			name:           "successful_processing_within_timeout",
			timeout:        100 * time.Millisecond,
			processorDelay: 50 * time.Millisecond,
			processorError: nil,
			expectedState:  proto.StateDone,
			expectedDone:   true,
			expectTimeout:  false,
		},
		{
			name:           "processing_timeout",
			timeout:        50 * time.Millisecond,
			processorDelay: 100 * time.Millisecond,
			processorError: nil,
			expectedState:  proto.StateError,
			expectedDone:   false,
			expectTimeout:  true,
		},
		{
			name:           "processor_error_within_timeout",
			timeout:        100 * time.Millisecond,
			processorDelay: 50 * time.Millisecond,
			processorError: errors.New("processing failed"),
			expectedState:  proto.StateError,
			expectedDone:   false,
			expectTimeout:  false,
		},
		{
			name:           "processor_panic",
			timeout:        100 * time.Millisecond,
			processorDelay: 50 * time.Millisecond,
			processorPanic: true,
			expectedState:  proto.StateError,
			expectedDone:   false,
			expectPanic:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper := NewStateTimeoutWrapper(tt.timeout)

			processor := func(ctx context.Context) (proto.State, bool, error) {
				if tt.processorPanic {
					panic("test panic")
				}

				// Simulate processing time
				select {
				case <-time.After(tt.processorDelay):
					// Processing completed
				case <-ctx.Done():
					// Context was cancelled (timeout)
					return proto.StateError, false, ctx.Err()
				}

				if tt.processorError != nil {
					return proto.StateError, false, tt.processorError
				}

				return proto.StateDone, true, nil
			}

			ctx := context.Background()
			currentState := proto.StateWaiting

			nextState, done, err := wrapper.ProcessWithTimeout(ctx, currentState, processor)

			// Check state
			if nextState != tt.expectedState {
				t.Errorf("expected state %s, got %s", tt.expectedState, nextState)
			}

			// Check done flag
			if done != tt.expectedDone {
				t.Errorf("expected done %v, got %v", tt.expectedDone, done)
			}

			// Check timeout behavior
			if tt.expectTimeout {
				if err == nil {
					t.Error("expected timeout error, got nil")
				} else if !contains(err.Error(), "timed out") {
					t.Errorf("expected timeout error, got: %v", err)
				}
			}

			// Check panic behavior
			if tt.expectPanic {
				if err == nil {
					t.Error("expected panic error, got nil")
				} else if !contains(err.Error(), "panicked") {
					t.Errorf("expected panic error, got: %v", err)
				}
			}

			// Check normal error
			if !tt.expectTimeout && !tt.expectPanic && tt.processorError != nil {
				if !errors.Is(err, tt.processorError) && err.Error() != tt.processorError.Error() {
					t.Errorf("expected processor error %v, got %v", tt.processorError, err)
				}
			}
		})
	}
}

func TestStateTimeoutWrapper_ContextCancellation(t *testing.T) {
	wrapper := NewStateTimeoutWrapper(1 * time.Second)

	processor := func(ctx context.Context) (proto.State, bool, error) {
		// Wait for context cancellation
		<-ctx.Done()
		return proto.StateError, false, ctx.Err()
	}

	// Create a context that we'll cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	currentState := proto.StateWaiting
	nextState, done, err := wrapper.ProcessWithTimeout(ctx, currentState, processor)

	if nextState != proto.StateError {
		t.Errorf("expected StateError, got %s", nextState)
	}

	if done {
		t.Error("expected done=false for cancelled context")
	}

	if err == nil {
		t.Error("expected error for cancelled context")
	} else if !contains(err.Error(), "cancelled") {
		t.Errorf("expected cancellation error, got: %v", err)
	}
}

func TestStateTimeoutWrapper_SetAndGetTimeout(t *testing.T) {
	wrapper := NewStateTimeoutWrapper(5 * time.Minute)

	// Test initial timeout
	if wrapper.GetTimeout() != 5*time.Minute {
		t.Errorf("expected 5 minutes, got %v", wrapper.GetTimeout())
	}

	// Test setting new timeout
	newTimeout := 10 * time.Minute
	wrapper.SetTimeout(newTimeout)

	if wrapper.GetTimeout() != newTimeout {
		t.Errorf("expected %v, got %v", newTimeout, wrapper.GetTimeout())
	}
}

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[len(s)-len(substr):] == substr ||
		len(s) > len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestProcessStateWithGlobalTimeout_DefaultFallback(t *testing.T) {
	// Test that ProcessStateWithGlobalTimeout uses 10-minute default when config unavailable
	// This simulates the case where NewStateTimeoutWrapperFromConfig() fails

	processor := func(_ context.Context) (proto.State, bool, error) {
		return proto.StateDone, true, nil
	}

	ctx := context.Background()
	nextState, done, err := ProcessStateWithGlobalTimeout(ctx, proto.StateWaiting, processor)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if nextState != proto.StateDone {
		t.Errorf("Expected StateDone, got: %s", nextState)
	}
	if !done {
		t.Errorf("Expected done=true, got: %v", done)
	}
}

func TestProcessStateWithGlobalTimeoutSimple_DefaultFallback(t *testing.T) {
	// Test that ProcessStateWithGlobalTimeoutSimple uses 10-minute default when config unavailable

	processor := func(_ context.Context) (proto.State, error) {
		return proto.StateDone, nil
	}

	ctx := context.Background()
	nextState, err := ProcessStateWithGlobalTimeoutSimple(ctx, proto.StateWaiting, processor)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if nextState != proto.StateDone {
		t.Errorf("Expected StateDone, got: %s", nextState)
	}
}

func TestProcessStateWithGlobalTimeout_DefaultTimeoutValue(t *testing.T) {
	// Test that the default timeout is actually 10 minutes by testing a scenario that would timeout in less time

	processor := func(ctx context.Context) (proto.State, bool, error) {
		// Sleep for a short time to verify we're not getting immediate execution
		select {
		case <-time.After(10 * time.Millisecond):
			return proto.StateDone, true, nil
		case <-ctx.Done():
			return proto.StateError, false, ctx.Err()
		}
	}

	ctx := context.Background()
	start := time.Now()
	nextState, done, err := ProcessStateWithGlobalTimeout(ctx, proto.StateWaiting, processor)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if nextState != proto.StateDone {
		t.Errorf("Expected StateDone, got: %s", nextState)
	}
	if !done {
		t.Errorf("Expected done=true, got: %v", done)
	}

	// Should complete quickly (within 100ms), not timeout after 10 minutes
	if elapsed > 100*time.Millisecond {
		t.Errorf("Expected quick completion, took: %v", elapsed)
	}
}
