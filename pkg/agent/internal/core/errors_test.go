package core

import (
	"errors"
	"testing"
)

// TestErrStateNotFound tests that the error constant is defined.
func TestErrStateNotFound(t *testing.T) {
	if ErrStateNotFound == nil {
		t.Fatal("expected ErrStateNotFound to be defined")
	}

	if ErrStateNotFound.Error() != "state not found" {
		t.Errorf("expected error message %q, got %q", "state not found", ErrStateNotFound.Error())
	}
}

// TestErrInvalidTransition tests that the error constant is defined.
func TestErrInvalidTransition(t *testing.T) {
	if ErrInvalidTransition == nil {
		t.Fatal("expected ErrInvalidTransition to be defined")
	}

	if ErrInvalidTransition.Error() != "invalid state transition" {
		t.Errorf("expected error message %q, got %q", "invalid state transition", ErrInvalidTransition.Error())
	}
}

// TestErrInvalidState tests that the error constant is defined.
func TestErrInvalidState(t *testing.T) {
	if ErrInvalidState == nil {
		t.Fatal("expected ErrInvalidState to be defined")
	}

	if ErrInvalidState.Error() != "invalid state" {
		t.Errorf("expected error message %q, got %q", "invalid state", ErrInvalidState.Error())
	}
}

// TestErrorsImplementError tests that all errors implement error interface.
func TestErrorsImplementError(t *testing.T) {
	testCases := []struct {
		name string
		err  error
	}{
		{"ErrStateNotFound", ErrStateNotFound},
		{"ErrInvalidTransition", ErrInvalidTransition},
		{"ErrInvalidState", ErrInvalidState},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Errorf("expected %s to implement error interface", tc.name)
			}
		})
	}
}

// TestErrorComparison tests error comparison with errors.Is.
func TestErrorComparison(t *testing.T) {
	testCases := []struct {
		name   string
		err    error
		target error
	}{
		{"ErrStateNotFound direct", ErrStateNotFound, ErrStateNotFound},
		{"ErrInvalidTransition direct", ErrInvalidTransition, ErrInvalidTransition},
		{"ErrInvalidState direct", ErrInvalidState, ErrInvalidState},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.target) {
				t.Error("expected errors.Is to return true for same error")
			}
		})
	}

	// Test wrapped errors
	wrappedStateNotFound := errors.Join(ErrStateNotFound, errors.New("additional context"))
	if !errors.Is(wrappedStateNotFound, ErrStateNotFound) {
		t.Error("expected errors.Is to return true for wrapped ErrStateNotFound")
	}

	wrappedTransition := errors.Join(ErrInvalidTransition, errors.New("from WAITING to DONE"))
	if !errors.Is(wrappedTransition, ErrInvalidTransition) {
		t.Error("expected errors.Is to return true for wrapped ErrInvalidTransition")
	}

	wrappedState := errors.Join(ErrInvalidState, errors.New("invalid state value"))
	if !errors.Is(wrappedState, ErrInvalidState) {
		t.Error("expected errors.Is to return true for wrapped ErrInvalidState")
	}
}
