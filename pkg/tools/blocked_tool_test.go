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
	if _, ok := def.InputSchema.Properties["scope_guess"]; !ok {
		t.Error("expected scope_guess in properties")
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

func TestReportBlockedTool_ExecEnvironment(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	result, err := tool.Exec(context.Background(), map[string]any{
		"failure_kind": "environment",
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
	if fi.Kind != proto.FailureKindEnvironment {
		t.Errorf("expected kind %q, got %q", proto.FailureKindEnvironment, fi.Kind)
	}
	if fi.Source != proto.FailureSourceLLMReport {
		t.Errorf("expected source %q, got %q", proto.FailureSourceLLMReport, fi.Source)
	}
}

func TestReportBlockedTool_ExecPrerequisite(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	result, err := tool.Exec(context.Background(), map[string]any{
		"failure_kind": "prerequisite",
		"explanation":  "API key is expired",
		"scope_guess":  "system",
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
	if fi.Kind != proto.FailureKindPrerequisite {
		t.Errorf("expected kind %q, got %q", proto.FailureKindPrerequisite, fi.Kind)
	}
	if fi.ScopeGuess != proto.FailureScopeSystem {
		t.Errorf("expected scope_guess %q, got %q", proto.FailureScopeSystem, fi.ScopeGuess)
	}
}

func TestReportBlockedTool_ExecExternalBackwardCompat(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	// "external" should be accepted and normalized to "environment"
	result, err := tool.Exec(context.Background(), map[string]any{
		"failure_kind": "external",
		"explanation":  "Git repository corrupt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("expected Data to be map[string]any")
	}
	fi, ok := data[proto.KeyFailureInfo].(proto.FailureInfo)
	if !ok {
		t.Fatal("expected FailureInfo in Data")
	}
	if fi.Kind != proto.FailureKindEnvironment {
		t.Errorf("expected kind %q (external normalized), got %q", proto.FailureKindEnvironment, fi.Kind)
	}
}

func TestReportBlockedTool_ScopeGuessOptional(t *testing.T) {
	tool := NewReportBlockedTool(nil)

	// Without scope_guess — should succeed with empty scope
	result, err := tool.Exec(context.Background(), map[string]any{
		"failure_kind": "environment",
		"explanation":  "disk full",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("expected Data to be map[string]any")
	}
	fi, ok := data[proto.KeyFailureInfo].(proto.FailureInfo)
	if !ok {
		t.Fatal("expected FailureInfo in Data")
	}
	if fi.ScopeGuess != "" {
		t.Errorf("expected empty scope_guess, got %q", fi.ScopeGuess)
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
		"failure_kind": "environment",
	})
	if err == nil {
		t.Error("expected error for missing explanation")
	}

	// Empty explanation
	_, err = tool.Exec(context.Background(), map[string]any{
		"failure_kind": "environment",
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
		// Environment failures
		{
			name:     "bad tree object",
			stderr:   "fatal: bad tree object HEAD",
			exitCode: 128,
			want:     proto.FailureKindEnvironment,
		},
		{
			name:     "corrupt object",
			stderr:   "error: object file is corrupt",
			exitCode: 128,
			want:     proto.FailureKindEnvironment,
		},
		{
			name:     "not a git repository",
			stderr:   "fatal: not a git repository",
			exitCode: 128,
			want:     proto.FailureKindEnvironment,
		},
		{
			name:     "permission denied",
			stderr:   "error: Permission denied",
			exitCode: 1,
			want:     proto.FailureKindEnvironment,
		},
		{
			name:     "no space left",
			stderr:   "fatal: no space left on device",
			exitCode: 1,
			want:     proto.FailureKindEnvironment,
		},
		{
			name:     "read-only file system",
			stderr:   "error: read-only file system",
			exitCode: 1,
			want:     proto.FailureKindEnvironment,
		},
		{
			name:     "cannot lock ref",
			stderr:   "error: cannot lock ref 'refs/heads/main'",
			exitCode: 1,
			want:     proto.FailureKindEnvironment,
		},
		// Prerequisite failures
		{
			name:     "authentication failed",
			stderr:   "fatal: Authentication failed for 'https://github.com'",
			exitCode: 128,
			want:     proto.FailureKindPrerequisite,
		},
		{
			name:     "fakeowner (auth issue)",
			stderr:   "error: fakeowner: operation not permitted",
			exitCode: 128,
			want:     proto.FailureKindPrerequisite,
		},
		{
			name:     "could not resolve host",
			stderr:   "fatal: unable to access: Could not resolve host: github.com",
			exitCode: 128,
			want:     proto.FailureKindPrerequisite,
		},
		{
			name:     "token expired",
			stderr:   "remote: Token expired, please refresh",
			exitCode: 128,
			want:     proto.FailureKindPrerequisite,
		},
		// Not classified (content errors)
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

func TestNormalizeFailureKind(t *testing.T) {
	if proto.NormalizeFailureKind(proto.FailureKindExternal) != proto.FailureKindEnvironment {
		t.Error("expected external → environment")
	}
	if proto.NormalizeFailureKind(proto.FailureKindStoryInvalid) != proto.FailureKindStoryInvalid {
		t.Error("expected story_invalid unchanged")
	}
	if proto.NormalizeFailureKind(proto.FailureKindEnvironment) != proto.FailureKindEnvironment {
		t.Error("expected environment unchanged")
	}
	if proto.NormalizeFailureKind(proto.FailureKindPrerequisite) != proto.FailureKindPrerequisite {
		t.Error("expected prerequisite unchanged")
	}
}
