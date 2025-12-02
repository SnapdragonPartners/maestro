package claude

import (
	"testing"
)

func TestSignalDetector_SubmitPlan(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvent(StreamEvent{
		Type: "tool_use",
		ToolUse: &ToolUse{
			ID:   "tool_1",
			Name: ToolMaestroSubmitPlan,
			Input: map[string]any{
				"plan":       "1. Do thing A\n2. Do thing B",
				"confidence": "high",
				"risks":      "None identified",
			},
		},
	})

	signal, input := detector.DetectSignal()

	if signal != SignalPlanComplete {
		t.Errorf("expected SignalPlanComplete, got %q", signal)
	}
	if input == nil {
		t.Fatal("expected non-nil input")
	}
	if input.Plan != "1. Do thing A\n2. Do thing B" {
		t.Errorf("unexpected plan: %q", input.Plan)
	}
	if input.Confidence != "high" {
		t.Errorf("expected confidence 'high', got %q", input.Confidence)
	}
	if input.Risks != "None identified" {
		t.Errorf("unexpected risks: %q", input.Risks)
	}
}

func TestSignalDetector_Done(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvent(StreamEvent{
		Type: "tool_use",
		ToolUse: &ToolUse{
			ID:   "tool_1",
			Name: ToolMaestroDone,
			Input: map[string]any{
				"summary": "Implemented feature X with tests",
			},
		},
	})

	signal, input := detector.DetectSignal()

	if signal != SignalDone {
		t.Errorf("expected SignalDone, got %q", signal)
	}
	if input == nil {
		t.Fatal("expected non-nil input")
	}
	if input.Summary != "Implemented feature X with tests" {
		t.Errorf("unexpected summary: %q", input.Summary)
	}
}

func TestSignalDetector_Question(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvent(StreamEvent{
		Type: "tool_use",
		ToolUse: &ToolUse{
			ID:   "tool_1",
			Name: ToolMaestroQuestion,
			Input: map[string]any{
				"question": "Should I use interface A or B?",
				"context":  "Both seem applicable but have tradeoffs",
			},
		},
	})

	signal, input := detector.DetectSignal()

	if signal != SignalQuestion {
		t.Errorf("expected SignalQuestion, got %q", signal)
	}
	if input == nil {
		t.Fatal("expected non-nil input")
	}
	if input.Question != "Should I use interface A or B?" {
		t.Errorf("unexpected question: %q", input.Question)
	}
	if input.Context != "Both seem applicable but have tradeoffs" {
		t.Errorf("unexpected context: %q", input.Context)
	}
}

func TestSignalDetector_StoryComplete(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvent(StreamEvent{
		Type: "tool_use",
		ToolUse: &ToolUse{
			ID:   "tool_1",
			Name: ToolMaestroStoryComplete,
			Input: map[string]any{
				"reason": "Feature already exists in the codebase",
			},
		},
	})

	signal, input := detector.DetectSignal()

	if signal != SignalStoryComplete {
		t.Errorf("expected SignalStoryComplete, got %q", signal)
	}
	if input == nil {
		t.Fatal("expected non-nil input")
	}
	if input.Reason != "Feature already exists in the codebase" {
		t.Errorf("unexpected reason: %q", input.Reason)
	}
}

func TestSignalDetector_NoSignal(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvent(StreamEvent{
		Type: "tool_use",
		ToolUse: &ToolUse{
			ID:    "tool_1",
			Name:  "write_file",
			Input: map[string]any{"path": "/test.txt"},
		},
	})

	signal, input := detector.DetectSignal()

	if signal != "" {
		t.Errorf("expected empty signal, got %q", signal)
	}
	if input != nil {
		t.Errorf("expected nil input, got %v", input)
	}
}

func TestSignalDetector_FromContentBlock(t *testing.T) {
	// Tool use can also appear as content blocks in assistant messages
	detector := NewSignalDetector()
	detector.AddEvent(StreamEvent{
		Type: "assistant",
		Message: &AssistantMessage{
			Content: []ContentBlock{
				{Type: "text", Text: "I'll submit my plan now"},
				{
					Type:  "tool_use",
					ID:    "tool_1",
					Name:  ToolMaestroSubmitPlan,
					Input: map[string]any{"plan": "My plan", "confidence": "medium"},
				},
			},
		},
	})

	signal, input := detector.DetectSignal()

	if signal != SignalPlanComplete {
		t.Errorf("expected SignalPlanComplete, got %q", signal)
	}
	if input == nil || input.Plan != "My plan" {
		t.Errorf("unexpected input: %v", input)
	}
}

func TestSignalDetector_MultipleEvents(t *testing.T) {
	detector := NewSignalDetector()

	// Add multiple events, only one with maestro signal
	detector.AddEvents([]StreamEvent{
		{Type: "assistant", Message: &AssistantMessage{Content: []ContentBlock{{Type: "text", Text: "Working..."}}}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t1", Name: "read_file", Input: map[string]any{}}},
		{Type: "tool_result", ToolResult: &ToolResult{ToolUseID: "t1", Content: "file content"}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t2", Name: ToolMaestroDone, Input: map[string]any{"summary": "All done"}}},
	})

	signal, input := detector.DetectSignal()

	if signal != SignalDone {
		t.Errorf("expected SignalDone, got %q", signal)
	}
	if input == nil || input.Summary != "All done" {
		t.Errorf("unexpected input: %v", input)
	}
}

func TestSignalDetector_Reset(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvent(StreamEvent{
		Type:    "tool_use",
		ToolUse: &ToolUse{ID: "t1", Name: ToolMaestroDone, Input: map[string]any{}},
	})

	if detector.EventCount() != 1 {
		t.Errorf("expected 1 event, got %d", detector.EventCount())
	}

	detector.Reset()

	if detector.EventCount() != 0 {
		t.Errorf("expected 0 events after reset, got %d", detector.EventCount())
	}

	signal, _ := detector.DetectSignal()
	if signal != "" {
		t.Errorf("expected empty signal after reset, got %q", signal)
	}
}

func TestGetAllMaestroTools(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvents([]StreamEvent{
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t1", Name: "read_file"}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t2", Name: ToolMaestroQuestion, Input: map[string]any{"question": "Q1"}}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t3", Name: "write_file"}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t4", Name: ToolMaestroDone, Input: map[string]any{"summary": "Done"}}},
	})

	maestroTools := detector.GetAllMaestroTools()

	if len(maestroTools) != 2 {
		t.Fatalf("expected 2 maestro tools, got %d", len(maestroTools))
	}
	if maestroTools[0].Name != ToolMaestroQuestion {
		t.Errorf("expected first tool to be %s, got %s", ToolMaestroQuestion, maestroTools[0].Name)
	}
	if maestroTools[1].Name != ToolMaestroDone {
		t.Errorf("expected second tool to be %s, got %s", ToolMaestroDone, maestroTools[1].Name)
	}
}

func TestBuildResult(t *testing.T) {
	tests := []struct {
		name    string
		signal  Signal
		input   *MaestroToolInput
		events  []StreamEvent
		checkFn func(t *testing.T, r Result)
	}{
		{
			name:   "plan complete",
			signal: SignalPlanComplete,
			input:  &MaestroToolInput{Plan: "My plan here"},
			events: []StreamEvent{
				{Type: "assistant", Message: &AssistantMessage{}},
			},
			checkFn: func(t *testing.T, r Result) {
				if r.Signal != SignalPlanComplete {
					t.Errorf("expected SignalPlanComplete, got %q", r.Signal)
				}
				if r.Plan != "My plan here" {
					t.Errorf("expected plan 'My plan here', got %q", r.Plan)
				}
				if r.ResponseCount != 1 {
					t.Errorf("expected ResponseCount=1, got %d", r.ResponseCount)
				}
			},
		},
		{
			name:   "done with summary",
			signal: SignalDone,
			input:  &MaestroToolInput{Summary: "Completed implementation"},
			events: []StreamEvent{},
			checkFn: func(t *testing.T, r Result) {
				if r.Signal != SignalDone {
					t.Errorf("expected SignalDone, got %q", r.Signal)
				}
				if r.Summary != "Completed implementation" {
					t.Errorf("unexpected summary: %q", r.Summary)
				}
			},
		},
		{
			name:   "question",
			signal: SignalQuestion,
			input:  &MaestroToolInput{Question: "Which API?", Context: "There are two options"},
			events: []StreamEvent{},
			checkFn: func(t *testing.T, r Result) {
				if r.Signal != SignalQuestion {
					t.Errorf("expected SignalQuestion, got %q", r.Signal)
				}
				if r.Question == nil {
					t.Fatal("expected Question to be non-nil")
				}
				if r.Question.Question != "Which API?" {
					t.Errorf("unexpected question: %q", r.Question.Question)
				}
				if r.Question.Context != "There are two options" {
					t.Errorf("unexpected context: %q", r.Question.Context)
				}
			},
		},
		{
			name:   "story complete",
			signal: SignalStoryComplete,
			input:  &MaestroToolInput{Reason: "Already done"},
			events: []StreamEvent{},
			checkFn: func(t *testing.T, r Result) {
				if r.Signal != SignalStoryComplete {
					t.Errorf("expected SignalStoryComplete, got %q", r.Signal)
				}
				if r.Reason != "Already done" {
					t.Errorf("unexpected reason: %q", r.Reason)
				}
			},
		},
		{
			name:   "with error in events",
			signal: SignalDone,
			input:  &MaestroToolInput{Summary: "Done"},
			events: []StreamEvent{
				{Type: "error", Error: &ErrorInfo{Message: "Rate limited"}},
			},
			checkFn: func(t *testing.T, r Result) {
				// Error in events should override signal
				if r.Signal != SignalError {
					t.Errorf("expected SignalError due to error event, got %q", r.Signal)
				}
				if r.Error == nil {
					t.Fatal("expected Error to be non-nil")
				}
				if r.Error.Error() != "Rate limited" {
					t.Errorf("unexpected error: %q", r.Error.Error())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := BuildResult(tc.signal, tc.input, tc.events)
			tc.checkFn(t, result)
		})
	}
}

func TestIsMaestroTool(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"submit_plan", ToolMaestroSubmitPlan, true},
		{"done", ToolMaestroDone, true},
		{"question", ToolMaestroQuestion, true},
		{"story_complete", ToolMaestroStoryComplete, true},
		{"read_file", "read_file", false},
		{"write_file", "write_file", false},
		{"bash", "bash", false},
		{"maestro_unknown", "maestro_unknown", true}, // Prefix match
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsMaestroTool(tc.toolName)
			if result != tc.expected {
				t.Errorf("IsMaestroTool(%q) = %v, expected %v", tc.toolName, result, tc.expected)
			}
		})
	}
}

func TestMaestroToolNames(t *testing.T) {
	names := MaestroToolNames()

	if len(names) != 4 {
		t.Errorf("expected 4 tool names, got %d", len(names))
	}

	expected := map[string]bool{
		ToolMaestroSubmitPlan:    true,
		ToolMaestroDone:          true,
		ToolMaestroQuestion:      true,
		ToolMaestroStoryComplete: true,
	}

	for _, name := range names {
		if !expected[name] {
			t.Errorf("unexpected tool name: %q", name)
		}
	}
}

func TestParseMaestroInput_NilInput(t *testing.T) {
	result := parseMaestroInput(nil)
	if result == nil {
		t.Fatal("expected non-nil result even for nil input")
	}
}

func TestParseMaestroInput_JSONString(t *testing.T) {
	jsonStr := `{"plan":"test plan","confidence":"high"}`
	result := parseMaestroInput(jsonStr)

	if result.Plan != "test plan" {
		t.Errorf("expected plan 'test plan', got %q", result.Plan)
	}
	if result.Confidence != "high" {
		t.Errorf("expected confidence 'high', got %q", result.Confidence)
	}
}
