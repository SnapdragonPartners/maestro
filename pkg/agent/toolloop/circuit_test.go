package toolloop

import (
	"fmt"
	"testing"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

func testLogger() *logx.Logger {
	return logx.NewLogger("circuit-test")
}

func TestClassifyToolResult_GoError(t *testing.T) {
	isFailure, detail := classifyToolResult(nil, fmt.Errorf("connection refused"))
	if !isFailure {
		t.Error("Expected Go error to be classified as failure")
	}
	if detail != "connection refused" {
		t.Errorf("Expected error detail 'connection refused', got %q", detail)
	}
}

func TestClassifyToolResult_SuccessFalse(t *testing.T) {
	result := &tools.ExecResult{Content: `{"success": false, "error": "old_string not found"}`}
	isFailure, detail := classifyToolResult(result, nil)
	if !isFailure {
		t.Error("Expected success:false to be classified as failure")
	}
	if detail != "old_string not found" {
		t.Errorf("Expected error detail from JSON, got %q", detail)
	}
}

func TestClassifyToolResult_SuccessFalseNoError(t *testing.T) {
	result := &tools.ExecResult{Content: `{"success": false}`}
	isFailure, detail := classifyToolResult(result, nil)
	if !isFailure {
		t.Error("Expected success:false to be classified as failure")
	}
	if detail != "success: false" {
		t.Errorf("Expected 'success: false', got %q", detail)
	}
}

func TestClassifyToolResult_NonZeroExitCode(t *testing.T) {
	result := &tools.ExecResult{Content: `{"exit_code": 1, "stderr": "make: *** [test] Error 1", "stdout": ""}`}
	isFailure, detail := classifyToolResult(result, nil)
	if !isFailure {
		t.Error("Expected non-zero exit code to be classified as failure")
	}
	if detail != "make: *** [test] Error 1" {
		t.Errorf("Expected stderr as detail, got %q", detail)
	}
}

func TestClassifyToolResult_NonZeroExitCodeNoStderr(t *testing.T) {
	result := &tools.ExecResult{Content: `{"exit_code": 127, "stderr": "", "stdout": ""}`}
	isFailure, detail := classifyToolResult(result, nil)
	if !isFailure {
		t.Error("Expected non-zero exit code to be classified as failure")
	}
	if detail != "exit_code: 127" {
		t.Errorf("Expected 'exit_code: 127', got %q", detail)
	}
}

func TestClassifyToolResult_SuccessTrue(t *testing.T) {
	result := &tools.ExecResult{Content: `{"success": true, "output": "built ok"}`}
	isFailure, _ := classifyToolResult(result, nil)
	if isFailure {
		t.Error("Expected success:true to NOT be classified as failure")
	}
}

func TestClassifyToolResult_ZeroExitCode(t *testing.T) {
	result := &tools.ExecResult{Content: `{"exit_code": 0, "stdout": "all tests passed"}`}
	isFailure, _ := classifyToolResult(result, nil)
	if isFailure {
		t.Error("Expected exit_code:0 to NOT be classified as failure")
	}
}

func TestClassifyToolResult_PlainContent(t *testing.T) {
	result := &tools.ExecResult{Content: "File written successfully"}
	isFailure, _ := classifyToolResult(result, nil)
	if isFailure {
		t.Error("Expected plain text content to NOT be classified as failure")
	}
}

func TestClassifyToolResult_NilResult(t *testing.T) {
	isFailure, _ := classifyToolResult(nil, nil)
	if isFailure {
		t.Error("Expected nil result with nil error to NOT be classified as failure")
	}
}

func TestCallFingerprint(t *testing.T) {
	// Same tool, different params should produce different keys
	key1 := callKey("file_edit", map[string]any{"path": "main.go", "old_string": "foo"})
	key2 := callKey("file_edit", map[string]any{"path": "main.go", "old_string": "bar"})
	if key1 == key2 {
		t.Error("Different params should produce different call keys")
	}

	// Same params in different order should produce same key (sorted)
	key3 := callKey("shell", map[string]any{"a": "1", "b": "2"})
	key4 := callKey("shell", map[string]any{"b": "2", "a": "1"})
	if key3 != key4 {
		t.Error("Same params in different order should produce same key")
	}

	// Empty params
	key5 := callKey("tool", nil)
	key6 := callKey("tool", map[string]any{})
	if key5 != key6 {
		t.Error("Nil and empty params should produce same key")
	}
}

func TestFullFingerprint_DifferentErrors(t *testing.T) {
	params := map[string]any{"path": "main.go"}
	fp1 := fullFingerprint("file_edit", params, "old_string not found")
	fp2 := fullFingerprint("file_edit", params, "file not found")
	if fp1 == fp2 {
		t.Error("Different errors should produce different fingerprints")
	}
}

func TestRecordFailureTrips(t *testing.T) {
	tracker := newToolErrorTracker(&ToolCircuitBreakerConfig{MaxConsecutiveFailures: 3}, testLogger())
	params := map[string]any{"command": "make test"}

	// First two failures: not tripped
	tracker.recordFailure("shell", params, "exit_code: 1")
	tracker.recordFailure("shell", params, "exit_code: 1")
	tripped, _ := tracker.checkTripped("shell", params)
	if tripped {
		t.Error("Should not be tripped after 2 failures")
	}

	// Third failure: trips
	tracker.recordFailure("shell", params, "exit_code: 1")
	tripped, lastErr := tracker.checkTripped("shell", params)
	if !tripped {
		t.Error("Should be tripped after 3 failures")
	}
	if lastErr != "exit_code: 1" {
		t.Errorf("Expected last error 'exit_code: 1', got %q", lastErr)
	}
}

func TestSuccessResetsAllFingerprintsForTool(t *testing.T) {
	tracker := newToolErrorTracker(&ToolCircuitBreakerConfig{MaxConsecutiveFailures: 3}, testLogger())
	params := map[string]any{"command": "make test"}

	// Two failures
	tracker.recordFailure("shell", params, "exit_code: 1")
	tracker.recordFailure("shell", params, "exit_code: 1")

	// Success resets
	tracker.recordSuccess("shell")

	// Two more failures: should not trip (counter was reset)
	tracker.recordFailure("shell", params, "exit_code: 1")
	tracker.recordFailure("shell", params, "exit_code: 1")
	tripped, _ := tracker.checkTripped("shell", params)
	if tripped {
		t.Error("Should not be tripped: success should have reset the counter")
	}
}

func TestDifferentToolsDontInterfere(t *testing.T) {
	tracker := newToolErrorTracker(&ToolCircuitBreakerConfig{MaxConsecutiveFailures: 2}, testLogger())

	// Trip tool A
	tracker.recordFailure("file_edit", map[string]any{"path": "a.go"}, "not found")
	tracker.recordFailure("file_edit", map[string]any{"path": "a.go"}, "not found")
	trippedA, _ := tracker.checkTripped("file_edit", map[string]any{"path": "a.go"})
	if !trippedA {
		t.Error("file_edit should be tripped")
	}

	// Tool B should not be tripped
	trippedB, _ := tracker.checkTripped("shell", map[string]any{"command": "ls"})
	if trippedB {
		t.Error("shell should NOT be tripped")
	}
}

func TestChangedErrorResetsCounting(t *testing.T) {
	tracker := newToolErrorTracker(&ToolCircuitBreakerConfig{MaxConsecutiveFailures: 3}, testLogger())
	params := map[string]any{"command": "make test"}

	// Two failures with error A
	tracker.recordFailure("shell", params, "compilation error on line 42")
	tracker.recordFailure("shell", params, "compilation error on line 42")

	// One failure with error B (different fingerprint, counter is separate)
	tracker.recordFailure("shell", params, "test failure in TestFoo")

	// Neither should be tripped (2 of A, 1 of B)
	tripped, _ := tracker.checkTripped("shell", params)
	if tripped {
		t.Error("Should not be tripped: errors changed, so no fingerprint hit threshold")
	}
}

func TestDefaultThreshold(t *testing.T) {
	tracker := newToolErrorTracker(&ToolCircuitBreakerConfig{}, testLogger())
	params := map[string]any{"command": "test"}

	// Default is 3
	tracker.recordFailure("tool", params, "err")
	tracker.recordFailure("tool", params, "err")
	tripped, _ := tracker.checkTripped("tool", params)
	if tripped {
		t.Error("Should not trip at 2 with default threshold of 3")
	}

	tracker.recordFailure("tool", params, "err")
	tripped, _ = tracker.checkTripped("tool", params)
	if !tripped {
		t.Error("Should trip at 3 with default threshold")
	}
}

func TestOnTripCallback(t *testing.T) {
	var tripCalled bool
	var tripLabel string
	tracker := newToolErrorTracker(&ToolCircuitBreakerConfig{
		MaxConsecutiveFailures: 2,
		OnTrip: func(_, label string, _ int) {
			tripCalled = true
			tripLabel = label
		},
	}, testLogger())

	params := map[string]any{"command": "make build"}
	tracker.recordFailure("shell", params, "err")
	tracker.recordFailure("shell", params, "err")

	if !tripCalled {
		t.Error("OnTrip callback should have been called")
	}
	if tripLabel != "shell(command=make build)" {
		t.Errorf("Expected label 'shell(command=make build)', got %q", tripLabel)
	}
}

func TestDisplayLabel(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		params   map[string]any
		expected string
	}{
		{"no params", "build", nil, "build"},
		{"with cmd", "shell", map[string]any{"cmd": "ls -la"}, "shell(cmd=ls -la)"},
		{"with command", "shell", map[string]any{"command": "make test"}, "shell(command=make test)"},
		{"with path", "file_edit", map[string]any{"path": "main.go", "old_string": "foo"}, "file_edit(path=main.go)"},
		{"cmd takes priority", "shell", map[string]any{"cmd": "ls", "command": "ls"}, "shell(cmd=ls)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := displayLabel(tt.toolName, tt.params)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestFirstLine(t *testing.T) {
	if firstLine("hello\nworld", 100) != "hello" {
		t.Error("Should return first line")
	}
	if firstLine("short", 3) != "sho" {
		t.Error("Should truncate to maxLen")
	}
	if firstLine("no newline", 100) != "no newline" {
		t.Error("Should return full string when no newline")
	}
}
