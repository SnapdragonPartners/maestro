package claude

import (
	"encoding/json"
	"strings"
)

// Maestro tool names that Claude Code can call to signal state transitions.
const (
	ToolMaestroSubmitPlan    = "maestro_submit_plan"
	ToolMaestroDone          = "maestro_done"
	ToolMaestroQuestion      = "maestro_question"
	ToolMaestroStoryComplete = "maestro_story_complete"
)

// MaestroToolInput represents input to a maestro tool call.
type MaestroToolInput struct {
	// For maestro_submit_plan
	Plan       string `json:"plan,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Risks      string `json:"risks,omitempty"`

	// For maestro_done
	Summary string `json:"summary,omitempty"`

	// For maestro_question
	Question string `json:"question,omitempty"`
	Context  string `json:"context,omitempty"`

	// For maestro_story_complete
	Reason string `json:"reason,omitempty"`
}

// SignalDetector detects maestro tool calls and extracts signal information.
type SignalDetector struct {
	events []StreamEvent
}

// NewSignalDetector creates a new signal detector.
func NewSignalDetector() *SignalDetector {
	return &SignalDetector{
		events: make([]StreamEvent, 0),
	}
}

// AddEvent adds a stream event to be analyzed.
func (d *SignalDetector) AddEvent(event StreamEvent) {
	d.events = append(d.events, event)
}

// AddEvents adds multiple stream events.
func (d *SignalDetector) AddEvents(events []StreamEvent) {
	d.events = append(d.events, events...)
}

// DetectSignal scans events for maestro tool calls and returns the detected signal.
// Returns SignalError if no valid signal is found.
func (d *SignalDetector) DetectSignal() (Signal, *MaestroToolInput) {
	toolCalls := ExtractToolCalls(d.events)

	for i := range toolCalls {
		if strings.HasPrefix(toolCalls[i].Name, "maestro_") {
			signal, input := d.parseToolCall(&toolCalls[i])
			if signal != "" {
				return signal, input
			}
		}
	}

	return "", nil
}

// parseToolCall parses a maestro tool call and returns the corresponding signal.
func (d *SignalDetector) parseToolCall(call *ToolUse) (Signal, *MaestroToolInput) {
	input := parseMaestroInput(call.Input)

	switch call.Name {
	case ToolMaestroSubmitPlan:
		return SignalPlanComplete, input

	case ToolMaestroDone:
		return SignalDone, input

	case ToolMaestroQuestion:
		return SignalQuestion, input

	case ToolMaestroStoryComplete:
		return SignalStoryComplete, input

	default:
		return "", nil
	}
}

// parseMaestroInput converts the tool input to a MaestroToolInput struct.
func parseMaestroInput(input any) *MaestroToolInput {
	if input == nil {
		return &MaestroToolInput{}
	}

	// If it's already a map, convert to JSON and back
	switch v := input.(type) {
	case map[string]any:
		result := &MaestroToolInput{}
		if plan, ok := v["plan"].(string); ok {
			result.Plan = plan
		}
		if confidence, ok := v["confidence"].(string); ok {
			result.Confidence = confidence
		}
		if risks, ok := v["risks"].(string); ok {
			result.Risks = risks
		}
		if summary, ok := v["summary"].(string); ok {
			result.Summary = summary
		}
		if question, ok := v["question"].(string); ok {
			result.Question = question
		}
		if context, ok := v["context"].(string); ok {
			result.Context = context
		}
		if reason, ok := v["reason"].(string); ok {
			result.Reason = reason
		}
		return result

	case string:
		// Try to parse as JSON
		result := &MaestroToolInput{}
		if err := json.Unmarshal([]byte(v), result); err == nil {
			return result
		}
		return &MaestroToolInput{}

	default:
		// Try JSON marshaling/unmarshaling
		data, err := json.Marshal(v)
		if err != nil {
			return &MaestroToolInput{}
		}
		result := &MaestroToolInput{}
		if err := json.Unmarshal(data, result); err != nil {
			return &MaestroToolInput{}
		}
		return result
	}
}

// GetAllMaestroTools returns all tool calls that are maestro tools.
func (d *SignalDetector) GetAllMaestroTools() []ToolUse {
	var maestroTools []ToolUse
	toolCalls := ExtractToolCalls(d.events)

	for i := range toolCalls {
		if strings.HasPrefix(toolCalls[i].Name, "maestro_") {
			maestroTools = append(maestroTools, toolCalls[i])
		}
	}

	return maestroTools
}

// EventCount returns the number of events processed.
func (d *SignalDetector) EventCount() int {
	return len(d.events)
}

// Reset clears all events from the detector.
func (d *SignalDetector) Reset() {
	d.events = d.events[:0]
}

// BuildResult creates a Result from the detected signal and input.
func BuildResult(signal Signal, input *MaestroToolInput, events []StreamEvent) Result {
	result := Result{
		Signal:        signal,
		ResponseCount: CountResponses(events),
	}

	if input != nil {
		switch signal {
		case SignalPlanComplete:
			result.Plan = input.Plan

		case SignalDone:
			result.Summary = input.Summary

		case SignalQuestion:
			result.Question = &Question{
				Question: input.Question,
				Context:  input.Context,
			}

		case SignalStoryComplete:
			result.Reason = input.Reason
		}
	}

	// Check for errors in the stream
	if hasErr, errMsg := HasError(events); hasErr {
		result.Signal = SignalError
		result.Error = &streamError{message: errMsg}
	}

	return result
}

// streamError implements the error interface.
type streamError struct {
	message string
}

func (e *streamError) Error() string {
	return e.message
}

// IsMaestroTool checks if a tool name is a maestro tool.
func IsMaestroTool(name string) bool {
	return strings.HasPrefix(name, "maestro_")
}

// MaestroToolNames returns all recognized maestro tool names.
func MaestroToolNames() []string {
	return []string{
		ToolMaestroSubmitPlan,
		ToolMaestroDone,
		ToolMaestroQuestion,
		ToolMaestroStoryComplete,
	}
}
