package claude

import (
	"encoding/json"
	"strings"

	"orchestrator/pkg/tools"
)

// MCPServerName is the name of our MCP server as configured in mcpserver.
// Tool calls from Claude Code will be prefixed with "mcp__<servername>__".
const MCPServerName = "maestro"

// mcpToolPrefix is the prefix Claude Code uses when calling our MCP tools.
//
//nolint:gochecknoglobals // This is a constant-like value computed from MCPServerName
var mcpToolPrefix = "mcp__" + MCPServerName + "__"

// normalizeToolName strips the MCP prefix if present to get the base tool name.
// For example: "mcp__maestro__submit_plan" -> "submit_plan".
func normalizeToolName(name string) string {
	if strings.HasPrefix(name, mcpToolPrefix) {
		return strings.TrimPrefix(name, mcpToolPrefix)
	}
	return name
}

// NormalizeMCPToolNames strips all MCP prefixes from tool names in text content.
// This normalizes plan text so the architect sees base tool names (e.g., "container_test")
// instead of MCP-prefixed names (e.g., "mcp__maestro__container_test").
func NormalizeMCPToolNames(text string) string {
	// Replace all occurrences of the MCP prefix
	return strings.ReplaceAll(text, mcpToolPrefix, "")
}

// signalToolNames maps tool names to their signals for detection.
// These match the actual tool names exposed via MCP from pkg/tools/constants.go.
//
//nolint:gochecknoglobals // This is a lookup table that needs to be globally accessible
var signalToolNames = map[string]Signal{
	tools.ToolSubmitPlan:      SignalPlanComplete,
	tools.ToolDone:            SignalDone,
	tools.ToolAskQuestion:     SignalQuestion,
	tools.ToolStoryComplete:   SignalStoryComplete,
	tools.ToolContainerSwitch: SignalContainerSwitch,
}

// SignalToolInput represents input to a signal tool call.
type SignalToolInput struct {
	// For submit_plan
	Plan       string `json:"plan,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Risks      string `json:"risks,omitempty"`

	// For done
	Summary string `json:"summary,omitempty"`

	// For ask_question
	Question string `json:"question,omitempty"`
	Context  string `json:"context,omitempty"`

	// For story_complete
	Evidence           string `json:"evidence,omitempty"`
	ExplorationSummary string `json:"exploration_summary,omitempty"`

	// For container_switch
	ContainerName string `json:"container_name,omitempty"`
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

// DetectSignal scans events for signal tool calls and returns the detected signal.
// Returns empty signal if no valid signal is found.
// Handles both direct tool names (e.g., "submit_plan") and MCP-prefixed names
// (e.g., "mcp__maestro__submit_plan").
func (d *SignalDetector) DetectSignal() (Signal, *SignalToolInput) {
	toolCalls := ExtractToolCalls(d.events)

	for i := range toolCalls {
		// Normalize tool name to handle MCP prefix
		normalizedName := normalizeToolName(toolCalls[i].Name)
		if signal, ok := signalToolNames[normalizedName]; ok {
			input := parseSignalInput(toolCalls[i].Input)
			return signal, input
		}
	}

	return "", nil
}

// parseSignalInput converts the tool input to a SignalToolInput struct.
func parseSignalInput(input any) *SignalToolInput {
	if input == nil {
		return &SignalToolInput{}
	}

	// If it's already a map, convert to JSON and back
	switch v := input.(type) {
	case map[string]any:
		result := &SignalToolInput{}
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
		if evidence, ok := v["evidence"].(string); ok {
			result.Evidence = evidence
		}
		if explorationSummary, ok := v["exploration_summary"].(string); ok {
			result.ExplorationSummary = explorationSummary
		}
		if containerName, ok := v["container_name"].(string); ok {
			result.ContainerName = containerName
		}
		return result

	case string:
		// Try to parse as JSON
		result := &SignalToolInput{}
		if err := json.Unmarshal([]byte(v), result); err == nil {
			return result
		}
		return &SignalToolInput{}

	default:
		// Try JSON marshaling/unmarshaling
		data, err := json.Marshal(v)
		if err != nil {
			return &SignalToolInput{}
		}
		result := &SignalToolInput{}
		if err := json.Unmarshal(data, result); err != nil {
			return &SignalToolInput{}
		}
		return result
	}
}

// GetAllSignalTools returns all tool calls that are signal tools.
// Handles both direct tool names and MCP-prefixed names.
func (d *SignalDetector) GetAllSignalTools() []ToolUse {
	var signalTools []ToolUse
	toolCalls := ExtractToolCalls(d.events)

	for i := range toolCalls {
		normalizedName := normalizeToolName(toolCalls[i].Name)
		if _, ok := signalToolNames[normalizedName]; ok {
			signalTools = append(signalTools, toolCalls[i])
		}
	}

	return signalTools
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
func BuildResult(signal Signal, input *SignalToolInput, events []StreamEvent) Result {
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
			result.Evidence = input.Evidence
			result.ExplorationSummary = input.ExplorationSummary

		case SignalContainerSwitch:
			result.ContainerSwitchTarget = input.ContainerName
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

// IsSignalTool checks if a tool name is a signal tool.
// Handles both direct tool names (e.g., "submit_plan") and MCP-prefixed names
// (e.g., "mcp__maestro__submit_plan").
func IsSignalTool(name string) bool {
	normalizedName := normalizeToolName(name)
	_, ok := signalToolNames[normalizedName]
	return ok
}

// SignalToolNamesList returns all recognized signal tool names.
func SignalToolNamesList() []string {
	names := make([]string, 0, len(signalToolNames))
	for name := range signalToolNames {
		names = append(names, name)
	}
	return names
}
