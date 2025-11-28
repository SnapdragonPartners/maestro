package runtime

import (
	"errors"
	"testing"
)

// TestErrMaxRetriesExceeded tests that the error constant is defined.
func TestErrMaxRetriesExceeded(t *testing.T) {
	if ErrMaxRetriesExceeded == nil {
		t.Fatal("expected ErrMaxRetriesExceeded to be defined")
	}

	if ErrMaxRetriesExceeded.Error() != "maximum retries exceeded" {
		t.Errorf("expected error message %q, got %q", "maximum retries exceeded", ErrMaxRetriesExceeded.Error())
	}
}

// TestErrMaxRetriesExceededIsError tests that it implements error interface.
func TestErrMaxRetriesExceededIsError(t *testing.T) {
	var err = ErrMaxRetriesExceeded
	if err == nil {
		t.Error("expected ErrMaxRetriesExceeded to implement error interface")
	}
}

// TestErrMaxRetriesExceededComparison tests error comparison with errors.Is.
func TestErrMaxRetriesExceededComparison(t *testing.T) {
	// Test direct comparison
	if !errors.Is(ErrMaxRetriesExceeded, ErrMaxRetriesExceeded) {
		t.Error("expected errors.Is to return true for same error")
	}

	// Test wrapped error
	wrapped := errors.Join(ErrMaxRetriesExceeded, errors.New("attempted 3 times"))
	if !errors.Is(wrapped, ErrMaxRetriesExceeded) {
		t.Error("expected errors.Is to return true for wrapped error")
	}

	// Test different error
	otherErr := errors.New("different error")
	if errors.Is(otherErr, ErrMaxRetriesExceeded) {
		t.Error("expected errors.Is to return false for different error")
	}
}
