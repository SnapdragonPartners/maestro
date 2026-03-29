package proto

import (
	"testing"
)

func TestNewFailureInfo(t *testing.T) {
	fi := NewFailureInfo(FailureKindExternal, "git corrupt", "CODING", "done")

	if fi.Kind != FailureKindExternal {
		t.Errorf("expected kind %q, got %q", FailureKindExternal, fi.Kind)
	}
	if fi.Explanation != "git corrupt" {
		t.Errorf("unexpected explanation: %s", fi.Explanation)
	}
	if fi.FailedState != "CODING" {
		t.Errorf("expected failed state CODING, got %q", fi.FailedState)
	}
	if fi.ToolName != "done" {
		t.Errorf("expected tool name done, got %q", fi.ToolName)
	}
}

func TestFailureKindConstants(t *testing.T) {
	// Verify the string values are stable (used in JSON, templates, etc.)
	if string(FailureKindTransient) != "transient" {
		t.Errorf("FailureKindTransient = %q", FailureKindTransient)
	}
	if string(FailureKindStoryInvalid) != "story_invalid" {
		t.Errorf("FailureKindStoryInvalid = %q", FailureKindStoryInvalid)
	}
	if string(FailureKindExternal) != "external" {
		t.Errorf("FailureKindExternal = %q", FailureKindExternal)
	}
}

func TestKeyFailureInfo(t *testing.T) {
	if KeyFailureInfo != "failure_info" {
		t.Errorf("KeyFailureInfo = %q", KeyFailureInfo)
	}
}
