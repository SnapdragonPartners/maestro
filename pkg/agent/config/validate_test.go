package config

import (
	"errors"
	"testing"
)

// TestErrInvalidConfig tests that the error constant is defined.
func TestErrInvalidConfig(t *testing.T) {
	if ErrInvalidConfig == nil {
		t.Fatal("expected ErrInvalidConfig to be defined")
	}

	if ErrInvalidConfig.Error() != "invalid configuration" {
		t.Errorf("expected error message %q, got %q", "invalid configuration", ErrInvalidConfig.Error())
	}
}

// TestErrInvalidConfigIsError tests that ErrInvalidConfig implements error interface.
func TestErrInvalidConfigIsError(t *testing.T) {
	var err = ErrInvalidConfig
	if err == nil {
		t.Error("expected ErrInvalidConfig to implement error interface")
	}
}

// TestErrInvalidConfigComparison tests error comparison with errors.Is.
func TestErrInvalidConfigComparison(t *testing.T) {
	// Test direct comparison
	if !errors.Is(ErrInvalidConfig, ErrInvalidConfig) {
		t.Error("expected errors.Is to return true for same error")
	}

	// Test wrapped error
	wrapped := errors.Join(ErrInvalidConfig, errors.New("additional context"))
	if !errors.Is(wrapped, ErrInvalidConfig) {
		t.Error("expected errors.Is to return true for wrapped error")
	}
}
