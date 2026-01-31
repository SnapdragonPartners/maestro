package claude

import (
	"testing"

	"orchestrator/pkg/tools"
)

func TestSignalDetector_SubmitPlan(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvent(StreamEvent{
		Type: "tool_use",
		ToolUse: &ToolUse{
			ID:   "tool_1",
			Name: tools.ToolSubmitPlan,
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
			Name: tools.ToolDone,
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
			Name: tools.ToolAskQuestion,
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
			Name: tools.ToolStoryComplete,
			Input: map[string]any{
				"evidence": "Feature already exists in the codebase",
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
	if input.Evidence != "Feature already exists in the codebase" {
		t.Errorf("unexpected evidence: %q", input.Evidence)
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
					Name:  tools.ToolSubmitPlan,
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

	// Add multiple events, only one with signal tool
	detector.AddEvents([]StreamEvent{
		{Type: "assistant", Message: &AssistantMessage{Content: []ContentBlock{{Type: "text", Text: "Working..."}}}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t1", Name: "read_file", Input: map[string]any{}}},
		{Type: "tool_result", ToolResult: &ToolResult{ToolUseID: "t1", Content: "file content"}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t2", Name: tools.ToolDone, Input: map[string]any{"summary": "All done"}}},
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
		ToolUse: &ToolUse{ID: "t1", Name: tools.ToolDone, Input: map[string]any{}},
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

func TestGetAllSignalTools(t *testing.T) {
	detector := NewSignalDetector()
	detector.AddEvents([]StreamEvent{
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t1", Name: "read_file"}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t2", Name: tools.ToolAskQuestion, Input: map[string]any{"question": "Q1"}}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t3", Name: "write_file"}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t4", Name: tools.ToolDone, Input: map[string]any{"summary": "Done"}}},
	})

	signalTools := detector.GetAllSignalTools()

	if len(signalTools) != 2 {
		t.Fatalf("expected 2 signal tools, got %d", len(signalTools))
	}
	if signalTools[0].Name != tools.ToolAskQuestion {
		t.Errorf("expected first tool to be %s, got %s", tools.ToolAskQuestion, signalTools[0].Name)
	}
	if signalTools[1].Name != tools.ToolDone {
		t.Errorf("expected second tool to be %s, got %s", tools.ToolDone, signalTools[1].Name)
	}
}

func TestBuildResult(t *testing.T) {
	tests := []struct {
		name    string
		signal  Signal
		input   *SignalToolInput
		events  []StreamEvent
		checkFn func(t *testing.T, r Result)
	}{
		{
			name:   "plan complete",
			signal: SignalPlanComplete,
			input:  &SignalToolInput{Plan: "My plan here"},
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
			input:  &SignalToolInput{Summary: "Completed implementation"},
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
			input:  &SignalToolInput{Question: "Which API?", Context: "There are two options"},
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
			input:  &SignalToolInput{Evidence: "Already done"},
			events: []StreamEvent{},
			checkFn: func(t *testing.T, r Result) {
				if r.Signal != SignalStoryComplete {
					t.Errorf("expected SignalStoryComplete, got %q", r.Signal)
				}
				if r.Evidence != "Already done" {
					t.Errorf("unexpected evidence: %q", r.Evidence)
				}
			},
		},
		{
			name:   "with error in events",
			signal: SignalDone,
			input:  &SignalToolInput{Summary: "Done"},
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

func TestIsSignalTool(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"submit_plan", tools.ToolSubmitPlan, true},
		{"done", tools.ToolDone, true},
		{"ask_question", tools.ToolAskQuestion, true},
		{"story_complete", tools.ToolStoryComplete, true},
		{"read_file", "read_file", false},
		{"write_file", "write_file", false},
		{"bash", "bash", false},
		{"unknown_tool", "unknown_tool", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsSignalTool(tc.toolName)
			if result != tc.expected {
				t.Errorf("IsSignalTool(%q) = %v, expected %v", tc.toolName, result, tc.expected)
			}
		})
	}
}

func TestSignalToolNamesList(t *testing.T) {
	names := SignalToolNamesList()

	if len(names) != 5 {
		t.Errorf("expected 5 tool names, got %d", len(names))
	}

	expected := map[string]bool{
		tools.ToolSubmitPlan:      true,
		tools.ToolDone:            true,
		tools.ToolAskQuestion:     true,
		tools.ToolStoryComplete:   true,
		tools.ToolContainerSwitch: true,
	}

	for _, name := range names {
		if !expected[name] {
			t.Errorf("unexpected tool name: %q", name)
		}
	}
}

// TestSignalDetector_MCPPrefix tests that signal detection works with MCP-prefixed tool names.
// When Claude Code uses MCP tools, it prefixes them with "mcp__<servername>__<toolname>".
// For our MCP server named "maestro", tools are called as "mcp__maestro__submit_plan" etc.
func TestSignalDetector_MCPPrefix(t *testing.T) {
	testCases := []struct {
		name           string
		mcpToolName    string
		expectedSignal Signal
	}{
		{
			name:           "submit_plan with MCP prefix",
			mcpToolName:    "mcp__maestro__submit_plan",
			expectedSignal: SignalPlanComplete,
		},
		{
			name:           "done with MCP prefix",
			mcpToolName:    "mcp__maestro__done",
			expectedSignal: SignalDone,
		},
		{
			name:           "ask_question with MCP prefix",
			mcpToolName:    "mcp__maestro__ask_question",
			expectedSignal: SignalQuestion,
		},
		{
			name:           "story_complete with MCP prefix",
			mcpToolName:    "mcp__maestro__story_complete",
			expectedSignal: SignalStoryComplete,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			detector := NewSignalDetector()
			detector.AddEvent(StreamEvent{
				Type: "tool_use",
				ToolUse: &ToolUse{
					ID:    "tool_1",
					Name:  tc.mcpToolName,
					Input: map[string]any{"plan": "test", "confidence": "HIGH"},
				},
			})

			signal, _ := detector.DetectSignal()
			if signal != tc.expectedSignal {
				t.Errorf("expected signal %q for tool %q, got %q", tc.expectedSignal, tc.mcpToolName, signal)
			}
		})
	}
}

// TestIsSignalTool_MCPPrefix tests IsSignalTool with MCP-prefixed names.
func TestIsSignalTool_MCPPrefix(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"MCP submit_plan", "mcp__maestro__submit_plan", true},
		{"MCP done", "mcp__maestro__done", true},
		{"MCP ask_question", "mcp__maestro__ask_question", true},
		{"MCP story_complete", "mcp__maestro__story_complete", true},
		{"MCP non-signal tool", "mcp__maestro__read_file", false},
		{"different server prefix", "mcp__other__submit_plan", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsSignalTool(tc.toolName)
			if result != tc.expected {
				t.Errorf("IsSignalTool(%q) = %v, expected %v", tc.toolName, result, tc.expected)
			}
		})
	}
}

func TestParseSignalInput_NilInput(t *testing.T) {
	result := parseSignalInput(nil)
	if result == nil {
		t.Fatal("expected non-nil result even for nil input")
	}
}

func TestParseSignalInput_JSONString(t *testing.T) {
	jsonStr := `{"plan":"test plan","confidence":"high"}`
	result := parseSignalInput(jsonStr)

	if result.Plan != "test plan" {
		t.Errorf("expected plan 'test plan', got %q", result.Plan)
	}
	if result.Confidence != "high" {
		t.Errorf("expected confidence 'high', got %q", result.Confidence)
	}
}

// TestNormalizeMCPToolNames tests that MCP prefixes are stripped from text content.
func TestNormalizeMCPToolNames(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no MCP prefix",
			input:    "Use container_test to verify the build",
			expected: "Use container_test to verify the build",
		},
		{
			name:     "single MCP prefix",
			input:    "Use mcp__maestro__container_test to verify the build",
			expected: "Use container_test to verify the build",
		},
		{
			name:     "multiple MCP prefixes",
			input:    "1. Use mcp__maestro__container_build to build\n2. Use mcp__maestro__container_test to test",
			expected: "1. Use container_build to build\n2. Use container_test to test",
		},
		{
			name:     "mixed content",
			input:    "Call mcp__maestro__shell to run commands, then container_build to build the image",
			expected: "Call shell to run commands, then container_build to build the image",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := NormalizeMCPToolNames(tc.input)
			if result != tc.expected {
				t.Errorf("NormalizeMCPToolNames(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

// TestBuildResult_PreservesPlan tests that BuildResult preserves plan text as-is.
// MCP prefix handling is done at the prompt level, not by modifying content.
func TestBuildResult_PreservesPlan(t *testing.T) {
	input := &SignalToolInput{
		Plan:       "1. Use mcp__maestro__container_build to build\n2. Use mcp__maestro__container_test to verify",
		Confidence: "HIGH",
	}

	result := BuildResult(SignalPlanComplete, input, []StreamEvent{})

	// Plan should be preserved as-is (MCP prefixes not stripped)
	expected := "1. Use mcp__maestro__container_build to build\n2. Use mcp__maestro__container_test to verify"
	if result.Plan != expected {
		t.Errorf("expected preserved plan %q, got %q", expected, result.Plan)
	}
}
