package coder

import (
	"testing"

	"orchestrator/pkg/agent/llmerrors"
)

func TestIsEmptyResponseError(t *testing.T) {
	// Create a mock coder instance
	coder := &Coder{}

	// Test with empty response error
	emptyResponseErr := llmerrors.NewError(llmerrors.ErrorTypeEmptyResponse, "received empty or nil response from Claude API")

	if !coder.isEmptyResponseError(emptyResponseErr) {
		t.Error("isEmptyResponseError should return true for empty response errors")
	}

	// Test with other error types
	rateLimitErr := llmerrors.NewError(llmerrors.ErrorTypeRateLimit, "rate limit exceeded")
	if coder.isEmptyResponseError(rateLimitErr) {
		t.Error("isEmptyResponseError should return false for rate limit errors")
	}

	authErr := llmerrors.NewError(llmerrors.ErrorTypeAuth, "authentication failed")
	if coder.isEmptyResponseError(authErr) {
		t.Error("isEmptyResponseError should return false for auth errors")
	}

	// Test with non-LLM error
	normalErr := &struct{ error }{error: nil}
	if coder.isEmptyResponseError(normalErr) {
		t.Error("isEmptyResponseError should return false for non-LLM errors")
	}

	t.Logf("✅ Empty response error detection works correctly")
}

func TestEmptyResponseErrorTypes(t *testing.T) {
	// Test the different error types we expect to encounter

	// Test the error message format that matches what user reported
	emptyErr := llmerrors.NewError(llmerrors.ErrorTypeEmptyResponse, "received empty or nil response from Claude API")
	expectedMsg := "LLM error (empty_response): received empty or nil response from Claude API"

	if emptyErr.Error() != expectedMsg {
		t.Errorf("Expected error message: %s, got: %s", expectedMsg, emptyErr.Error())
	}

	// Verify the error type string
	if emptyErr.Type.String() != "empty_response" {
		t.Errorf("Expected error type string 'empty_response', got: %s", emptyErr.Type.String())
	}

	// Verify that empty response errors are retryable
	if !emptyErr.IsRetryable() {
		t.Error("Empty response errors should be retryable")
	}

	t.Logf("✅ Empty response error types work as expected")
}

func TestEmptyResponseErrorDetection(t *testing.T) {
	// Test that we can detect empty response errors using llmerrors.Is

	emptyErr := llmerrors.NewError(llmerrors.ErrorTypeEmptyResponse, "test empty response")

	// Test llmerrors.Is function directly
	if !llmerrors.Is(emptyErr, llmerrors.ErrorTypeEmptyResponse) {
		t.Error("llmerrors.Is should detect empty response errors")
	}

	// Test that it doesn't match other error types
	if llmerrors.Is(emptyErr, llmerrors.ErrorTypeRateLimit) {
		t.Error("llmerrors.Is should not match empty response with rate limit")
	}

	if llmerrors.Is(emptyErr, llmerrors.ErrorTypeAuth) {
		t.Error("llmerrors.Is should not match empty response with auth error")
	}

	t.Logf("✅ Empty response error detection via llmerrors.Is works correctly")
}
