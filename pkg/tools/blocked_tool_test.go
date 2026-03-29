package tools

import (
	"context"
	"testing"

	"orchestrator/pkg/proto"
)

func TestReportBlockedTool_Definition(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	def := tool.Definition()
	if def.Name != ToolReportBlocked {
		t.Errorf("expected name %q, got %q", ToolReportBlocked, def.Name)
	}
	if _, ok := def.InputSchema.Properties["failure_kind"]; !ok {
		t.Error("expected failure_kind in properties")
	}
	if _, ok := def.InputSchema.Properties["explanation"]; !ok {
		t.Error("expected explanation in properties")
	}
	if len(def.InputSchema.Required) != 2 {
		t.Errorf("expected 2 required fields, got %d", len(def.InputSchema.Required))
	}
}

func TestReportBlockedTool_ExecStoryInvalid(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	result, err := tool.Exec(context.Background(), map[string]any{
		"failure_kind": "story_invalid",
		"explanation":  "Requirements contradict each other",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProcessEffect == nil {
		t.Fatal("expected ProcessEffect")
	}
	if result.ProcessEffect.Signal != SignalBlocked {
		t.Errorf("expected signal %q, got %q", SignalBlocked, result.ProcessEffect.Signal)
	}

	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("expected Data to be map[string]any")
	}
	fi, ok := data[proto.KeyFailureInfo].(proto.FailureInfo)
	if !ok {
		t.Fatal("expected FailureInfo in Data")
	}
	if fi.Kind != proto.FailureKindStoryInvalid {
		t.Errorf("expected kind %q, got %q", proto.FailureKindStoryInvalid, fi.Kind)
	}
	if fi.Explanation != "Requirements contradict each other" {
		t.Errorf("unexpected explanation: %s", fi.Explanation)
	}
	if fi.FailedState != "UNKNOWN" {
		t.Errorf("expected UNKNOWN state (no agent), got %q", fi.FailedState)
	}
}

func TestReportBlockedTool_ExecExternal(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	result, err := tool.Exec(context.Background(), map[string]any{
		"failure_kind": "external",
		"explanation":  "Git repository corrupt: bad tree object HEAD",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProcessEffect == nil {
		t.Fatal("expected ProcessEffect")
	}

	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("expected Data to be map[string]any")
	}
	fi, ok := data[proto.KeyFailureInfo].(proto.FailureInfo)
	if !ok {
		t.Fatal("expected FailureInfo in Data")
	}
	if fi.Kind != proto.FailureKindExternal {
		t.Errorf("expected kind %q, got %q", proto.FailureKindExternal, fi.Kind)
	}
}

func TestReportBlockedTool_InvalidKind(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	_, err := tool.Exec(context.Background(), map[string]any{
		"failure_kind": "made_up",
		"explanation":  "test",
	})
	if err == nil {
		t.Error("expected error for invalid failure_kind")
	}
}

func TestReportBlockedTool_MissingFields(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	// Missing failure_kind
	_, err := tool.Exec(context.Background(), map[string]any{
		"explanation": "test",
	})
	if err == nil {
		t.Error("expected error for missing failure_kind")
	}

	// Missing explanation
	_, err = tool.Exec(context.Background(), map[string]any{
		"failure_kind": "external",
	})
	if err == nil {
		t.Error("expected error for missing explanation")
	}

	// Empty explanation
	_, err = tool.Exec(context.Background(), map[string]any{
		"failure_kind": "external",
		"explanation":  "",
	})
	if err == nil {
		t.Error("expected error for empty explanation")
	}
}

func TestClassifyCommitFailure(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		exitCode int
		want     proto.FailureKind
	}{
		{
			name:     "bad tree object",
			stderr:   "fatal: bad tree object HEAD",
			exitCode: 128,
			want:     proto.FailureKindExternal,
		},
		{
			name:     "corrupt object",
			stderr:   "error: object file is corrupt",
			exitCode: 128,
			want:     proto.FailureKindExternal,
		},
		{
			name:     "not a git repository",
			stderr:   "fatal: not a git repository",
			exitCode: 128,
			want:     proto.FailureKindExternal,
		},
		{
			name:     "permission denied",
			stderr:   "error: Permission denied",
			exitCode: 1,
			want:     proto.FailureKindExternal,
		},
		{
			name:     "no space left",
			stderr:   "fatal: no space left on device",
			exitCode: 1,
			want:     proto.FailureKindExternal,
		},
		{
			name:     "read-only file system",
			stderr:   "error: read-only file system",
			exitCode: 1,
			want:     proto.FailureKindExternal,
		},
		{
			name:     "fakeowner",
			stderr:   "error: fakeowner: operation not permitted",
			exitCode: 128,
			want:     proto.FailureKindExternal,
		},
		{
			name:     "cannot lock ref",
			stderr:   "error: cannot lock ref 'refs/heads/main'",
			exitCode: 1,
			want:     proto.FailureKindExternal,
		},
		{
			name:     "pre-commit hook failure is not classified",
			stderr:   "pre-commit hook exited with error",
			exitCode: 1,
			want:     "",
		},
		{
			name:     "hook failure is not classified",
			stderr:   "hook failed at step: lint",
			exitCode: 1,
			want:     "",
		},
		{
			name:     "unknown error is not classified",
			stderr:   "some other error",
			exitCode: 1,
			want:     "",
		},
		{
			name:     "empty stderr is not classified",
			stderr:   "",
			exitCode: 1,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyCommitFailure(tt.stderr, tt.exitCode)
			if got != tt.want {
				t.Errorf("classifyCommitFailure(%q, %d) = %q, want %q", tt.stderr, tt.exitCode, got, tt.want)
			}
		})
	}
}
