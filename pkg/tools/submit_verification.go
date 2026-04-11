package tools

import (
	"context"
	"fmt"
)

// SubmitVerificationTool is the terminal tool for the TESTING verification toolloop.
// The LLM calls this to submit structured acceptance-criteria verification evidence.
type SubmitVerificationTool struct{}

// NewSubmitVerificationTool creates a new submit verification tool instance.
func NewSubmitVerificationTool() *SubmitVerificationTool {
	return &SubmitVerificationTool{}
}

// Name returns the tool identifier.
func (s *SubmitVerificationTool) Name() string {
	return ToolSubmitVerification
}

// Definition returns the tool's definition in Claude API format.
func (s *SubmitVerificationTool) Definition() ToolDefinition {
	minItems := 1
	return ToolDefinition{
		Name:        ToolSubmitVerification,
		Description: "Submit structured acceptance-criteria verification evidence. Call this once you have inspected the implementation against each acceptance criterion.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"acceptance_criteria_checked": {
					Type:        "array",
					Description: "Results for each acceptance criterion checked",
					Items: &Property{
						Type: "object",
						Properties: map[string]*Property{
							"criterion": {
								Type:        "string",
								Description: "The acceptance criterion being verified",
							},
							"method": {
								Type:        "string",
								Description: "How the criterion was verified",
								Enum:        []string{"command", "inspection"},
							},
							"result": {
								Type:        "string",
								Description: "Verification result",
								Enum:        []string{"pass", "fail", "partial", "unverified"},
							},
							"evidence": {
								Type:        "string",
								Description: "Specific evidence supporting the result (file paths, command output, etc.)",
							},
						},
						Required: []string{"criterion", "method", "result", "evidence"},
					},
					MinItems: &minItems,
				},
				"gaps": {
					Type:        "array",
					Description: "Any gaps or missing items found during verification",
					Items: &Property{
						Type: "string",
					},
				},
				"confidence": {
					Type:        "string",
					Description: "Overall confidence in the verification",
					Enum:        []string{"high", "medium", "low"},
				},
				"summary": {
					Type:        "string",
					Description: "Brief summary of verification findings",
				},
			},
			Required: []string{"acceptance_criteria_checked", "confidence", "summary"},
		},
	}
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *SubmitVerificationTool) PromptDocumentation() string {
	return `- **submit_verification** - Submit acceptance-criteria verification evidence
  - Parameters: acceptance_criteria_checked (required array), confidence (required), summary (required), gaps (optional)
  - Each criterion needs: criterion, method (command|inspection), result (pass|fail|partial|unverified), evidence
  - Call this after inspecting the implementation against each acceptance criterion`
}

// Exec validates and processes the verification submission.
//
//nolint:cyclop // Validation logic is inherently sequential; splitting would reduce clarity
func (s *SubmitVerificationTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Validate required fields
	summary, err := extractRequiredString(args, "summary")
	if err != nil {
		return nil, err
	}

	confidence, err := extractRequiredString(args, "confidence")
	if err != nil {
		return nil, err
	}
	if !isValidConfidence(confidence) {
		return nil, fmt.Errorf("confidence must be high, medium, or low")
	}

	// Validate acceptance_criteria_checked
	criteriaRaw, ok := args["acceptance_criteria_checked"]
	if !ok {
		return nil, fmt.Errorf("acceptance_criteria_checked parameter is required")
	}
	criteriaArr, ok := criteriaRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("acceptance_criteria_checked must be an array")
	}
	if len(criteriaArr) == 0 {
		return nil, fmt.Errorf("acceptance_criteria_checked must contain at least 1 item")
	}

	// Validate each criterion and determine pass/fail signal.
	// Emit []any of map[string]any so consumers see the same shape
	// whether they receive data directly or after JSON-roundtrip (resume).
	hasFail := false
	validatedCriteria := make([]any, 0, len(criteriaArr))

	for i, item := range criteriaArr {
		criterion, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("acceptance_criteria_checked item %d must be an object", i)
		}

		validated, err := validateCriterion(i, criterion)
		if err != nil {
			return nil, err
		}

		if validated["result"] == "fail" {
			hasFail = true
		}

		// Convert map[string]string → map[string]any for consumer compatibility
		criterionMap := make(map[string]any, len(validated))
		for k, v := range validated {
			criterionMap[k] = v
		}
		validatedCriteria = append(validatedCriteria, criterionMap)
	}

	// Extract optional gaps as []any for consumer compatibility
	var gaps []any
	if gapsRaw, ok := args["gaps"]; ok {
		if gapsArr, ok := gapsRaw.([]any); ok {
			for _, g := range gapsArr {
				if gs, ok := g.(string); ok {
					gaps = append(gaps, gs)
				}
			}
		}
	}

	// Determine signal based on results
	signal := SignalVerificationPass
	if hasFail {
		signal = SignalVerificationFail
	}

	return &ExecResult{
		Content: "Verification evidence submitted",
		ProcessEffect: &ProcessEffect{
			Signal: signal,
			Data: map[string]any{
				"acceptance_criteria_checked": validatedCriteria,
				"gaps":                        gaps,
				"confidence":                  confidence,
				"summary":                     summary,
			},
		},
	}, nil
}

func isValidMethod(s string) bool {
	return s == "command" || s == "inspection"
}

func isValidResult(s string) bool {
	return s == "pass" || s == "fail" || s == "partial" || s == "unverified"
}

func isValidConfidence(s string) bool {
	return s == "high" || s == "medium" || s == "low"
}

// validateCriterion validates a single criterion object and returns validated fields.
func validateCriterion(index int, criterion map[string]any) (map[string]string, error) {
	prefix := fmt.Sprintf("acceptance_criteria_checked item %d", index)

	criterionText, ok := criterion["criterion"].(string)
	if !ok || criterionText == "" {
		return nil, fmt.Errorf("%s: criterion field is required and must be a non-empty string", prefix)
	}

	method, ok := criterion["method"].(string)
	if !ok || !isValidMethod(method) {
		return nil, fmt.Errorf("%s: method must be 'command' or 'inspection'", prefix)
	}

	result, ok := criterion["result"].(string)
	if !ok || !isValidResult(result) {
		return nil, fmt.Errorf("%s: result must be 'pass', 'fail', 'partial', or 'unverified'", prefix)
	}

	evidence, ok := criterion["evidence"].(string)
	if !ok || evidence == "" {
		return nil, fmt.Errorf("%s: evidence field is required and must be a non-empty string", prefix)
	}

	return map[string]string{
		"criterion": criterionText,
		"method":    method,
		"result":    result,
		"evidence":  evidence,
	}, nil
}
