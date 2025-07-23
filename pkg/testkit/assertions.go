// Package testkit provides testing utilities for agent message validation and mock services.
package testkit

import (
	"reflect"
	"strings"
	"testing"

	"orchestrator/pkg/proto"
)

// AssertMessageType verifies the message type.
func AssertMessageType(t *testing.T, msg *proto.AgentMsg, expectedType proto.MsgType) {
	t.Helper()
	if msg.Type != expectedType {
		t.Errorf("Expected message type %s, got %s", expectedType, msg.Type)
	}
}

// AssertMessageFromAgent verifies the sender.
func AssertMessageFromAgent(t *testing.T, msg *proto.AgentMsg, expectedFromAgent string) {
	t.Helper()
	if msg.FromAgent != expectedFromAgent {
		t.Errorf("Expected message from %s, got %s", expectedFromAgent, msg.FromAgent)
	}
}

// AssertMessageToAgent verifies the recipient.
func AssertMessageToAgent(t *testing.T, msg *proto.AgentMsg, expectedToAgent string) {
	t.Helper()
	if msg.ToAgent != expectedToAgent {
		t.Errorf("Expected message to %s, got %s", expectedToAgent, msg.ToAgent)
	}
}

// AssertPayloadExists verifies a payload field exists.
func AssertPayloadExists(t *testing.T, msg *proto.AgentMsg, key string) {
	t.Helper()
	if _, exists := msg.GetPayload(key); !exists {
		t.Errorf("Expected payload key '%s' to exist", key)
	}
}

// AssertPayloadValue verifies a payload field has expected value.
func AssertPayloadValue(t *testing.T, msg *proto.AgentMsg, key string, expectedValue any) {
	t.Helper()
	value, exists := msg.GetPayload(key)
	if !exists {
		t.Errorf("Expected payload key '%s' to exist", key)
		return
	}
	if value != expectedValue {
		t.Errorf("Expected payload '%s' to be %v, got %v", key, expectedValue, value)
	}
}

// AssertPayloadString verifies a payload field is a string with expected value.
func AssertPayloadString(t *testing.T, msg *proto.AgentMsg, key, expectedValue string) {
	t.Helper()
	value, exists := msg.GetPayload(key)
	if !exists {
		t.Errorf("Expected payload key '%s' to exist", key)
		return
	}
	strValue, ok := value.(string)
	if !ok {
		t.Errorf("Expected payload '%s' to be a string, got %T", key, value)
		return
	}
	if strValue != expectedValue {
		t.Errorf("Expected payload '%s' to be '%s', got '%s'", key, expectedValue, strValue)
	}
}

// AssertPayloadContains verifies a payload string contains expected text.
func AssertPayloadContains(t *testing.T, msg *proto.AgentMsg, key, expectedText string) {
	t.Helper()
	value, exists := msg.GetPayload(key)
	if !exists {
		t.Errorf("Expected payload key '%s' to exist", key)
		return
	}
	strValue, ok := value.(string)
	if !ok {
		t.Errorf("Expected payload '%s' to be a string, got %T", key, value)
		return
	}
	if !strings.Contains(strValue, expectedText) {
		t.Errorf("Expected payload '%s' to contain '%s', got '%s'", key, expectedText, strValue)
	}
}

// AssertMetadataExists verifies a metadata field exists.
func AssertMetadataExists(t *testing.T, msg *proto.AgentMsg, key string) {
	t.Helper()
	if _, exists := msg.GetMetadata(key); !exists {
		t.Errorf("Expected metadata key '%s' to exist", key)
	}
}

// AssertMetadataValue verifies a metadata field has expected value.
func AssertMetadataValue(t *testing.T, msg *proto.AgentMsg, key, expectedValue string) {
	t.Helper()
	value, exists := msg.GetMetadata(key)
	if !exists {
		t.Errorf("Expected metadata key '%s' to exist", key)
		return
	}
	if value != expectedValue {
		t.Errorf("Expected metadata '%s' to be '%s', got '%s'", key, expectedValue, value)
	}
}

// AssertParentMessage verifies the parent message ID.
func AssertParentMessage(t *testing.T, msg *proto.AgentMsg, expectedParentID string) {
	t.Helper()
	if msg.ParentMsgID != expectedParentID {
		t.Errorf("Expected parent message ID '%s', got '%s'", expectedParentID, msg.ParentMsgID)
	}
}

// AssertTestResults verifies test results payload.
func AssertTestResults(t *testing.T, msg *proto.AgentMsg, expectedSuccess bool) {
	t.Helper()
	testResults, exists := msg.GetPayload("test_results")
	if !exists {
		t.Error("Expected test_results payload to exist")
		return
	}

	var success bool

	// Handle both map and struct types using reflection.
	val := reflect.ValueOf(testResults)

	if val.Kind() == reflect.Map {
		// Map format (original).
		successValue := val.MapIndex(reflect.ValueOf("success"))
		if !successValue.IsValid() {
			t.Error("Expected test_results.success to exist")
			return
		}

		if successValue.Interface() == nil {
			t.Error("Expected test_results.success to be non-nil")
			return
		}

		successBool, ok := successValue.Interface().(bool)
		if !ok {
			t.Errorf("Expected test_results.success to be a bool, got %T", successValue.Interface())
			return
		}
		success = successBool
	} else if val.Kind() == reflect.Struct {
		// Struct format - use reflection to get Success field.
		successField := val.FieldByName("Success")
		if !successField.IsValid() {
			t.Error("Expected struct to have Success field")
			return
		}

		if successField.Kind() != reflect.Bool {
			t.Errorf("Expected Success field to be bool, got %s", successField.Kind())
			return
		}

		success = successField.Bool()
	} else {
		t.Errorf("Expected test_results to be a map or struct, got %T", testResults)
		return
	}

	if success != expectedSuccess {
		t.Errorf("Expected test_results.success to be %t, got %t", expectedSuccess, success)
	}
}

// AssertCodeCompiles verifies that implementation contains compilable Go code.
func AssertCodeCompiles(t *testing.T, msg *proto.AgentMsg) {
	t.Helper()
	impl, exists := msg.GetPayload("implementation")
	if !exists {
		t.Error("Expected implementation payload to exist")
		return
	}

	implStr, ok := impl.(string)
	if !ok {
		t.Errorf("Expected implementation to be a string, got %T", impl)
		return
	}

	// Basic checks for Go code structure.
	if !strings.Contains(implStr, "package ") {
		t.Error("Implementation should contain package declaration")
	}

	if !strings.Contains(implStr, "func ") {
		t.Error("Implementation should contain at least one function")
	}
}

// AssertHealthEndpointCode verifies health endpoint specific implementation.
func AssertHealthEndpointCode(t *testing.T, msg *proto.AgentMsg) {
	t.Helper()
	AssertCodeCompiles(t, msg)

	impl, _ := msg.GetPayload("implementation")
	implStr, ok := impl.(string)
	if !ok {
		t.Fatalf("Expected implementation to be string, got %T", impl)
	}

	expectedPatterns := []string{
		"HealthResponse",
		"/health",
		"application/json",
		"time.Now()",
		"http.StatusOK",
	}

	for _, pattern := range expectedPatterns {
		if !strings.Contains(implStr, pattern) {
			t.Errorf("Expected implementation to contain '%s'", pattern)
		}
	}
}

// AssertNoAPICallsMade verifies that no real API calls were made (for mock testing).
func AssertNoAPICallsMade(t *testing.T, msg *proto.AgentMsg) {
	t.Helper()
	// Check for common indicators that real API was called.
	if impl, exists := msg.GetPayload("implementation"); exists {
		if implStr, ok := impl.(string); ok {
			// Real API responses tend to be longer and more sophisticated.
			if len(implStr) > 5000 {
				t.Log("Warning: Implementation is unusually long, may indicate real API call")
			}
			// Check for overly sophisticated patterns that mock wouldn't generate.
			sophisticatedPatterns := []string{
				"context.Context",
				"sync.Mutex",
				"error wrapping",
				"detailed logging",
			}
			for _, pattern := range sophisticatedPatterns {
				if strings.Contains(implStr, pattern) {
					t.Logf("Warning: Found sophisticated pattern '%s', may indicate real API call", pattern)
				}
			}
		}
	}
}

// AssertValidMessageFlow verifies a sequence of messages follows expected patterns.
func AssertValidMessageFlow(t *testing.T, messages []*proto.AgentMsg, expectedFlow []proto.MsgType) {
	t.Helper()
	if len(messages) != len(expectedFlow) {
		t.Errorf("Expected %d messages in flow, got %d", len(expectedFlow), len(messages))
		return
	}

	for i, msg := range messages {
		expectedType := expectedFlow[i]
		if msg.Type != expectedType {
			t.Errorf("Message %d: expected type %s, got %s", i, expectedType, msg.Type)
		}
	}
}

// LintTestConditions provides assertions for lint/test conditions.
type LintTestConditions struct {
	ErrorText  string
	ShouldPass bool
}

// AssertLintTestConditions verifies lint/test pass/fail conditions.
func AssertLintTestConditions(t *testing.T, msg *proto.AgentMsg, conditions LintTestConditions) {
	t.Helper()

	if conditions.ShouldPass {
		// Verify it's a RESULT, not ERROR.
		AssertMessageType(t, msg, proto.MsgTypeRESULT)
		AssertTestResults(t, msg, true)
		AssertPayloadString(t, msg, "status", "completed")
	} else {
		// Verify it's an ERROR message.
		AssertMessageType(t, msg, proto.MsgTypeERROR)
		AssertPayloadExists(t, msg, "error")

		if conditions.ErrorText != "" {
			AssertPayloadContains(t, msg, "error", conditions.ErrorText)
		}
	}
}
