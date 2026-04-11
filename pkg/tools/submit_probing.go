package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/utils"
)

// SubmitProbingTool is the terminal tool for the adversarial probing toolloop.
// The LLM calls this to submit structured robustness-probing findings.
type SubmitProbingTool struct{}

// NewSubmitProbingTool creates a new submit probing tool instance.
func NewSubmitProbingTool() *SubmitProbingTool {
	return &SubmitProbingTool{}
}

// Name returns the tool identifier.
func (s *SubmitProbingTool) Name() string {
	return ToolSubmitProbing
}

// Definition returns the tool's definition in Claude API format.
func (s *SubmitProbingTool) Definition() ToolDefinition {
	minItems := 1
	return ToolDefinition{
		Name:        ToolSubmitProbing,
		Description: "Submit structured robustness-probing findings. Call this once you have inspected the implementation for edge-case issues.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"findings": {
					Type:        "array",
					Description: "Results for each probing area checked",
					Items: &Property{
						Type: "object",
						Properties: map[string]*Property{
							"category": {
								Type:        "string",
								Description: "The probing category",
								Enum:        []string{"error_handling", "malformed_input", "boundary_values", "resource_cleanup", "idempotent_operations", "security"},
							},
							"description": {
								Type:        "string",
								Description: "Description of what was checked or found",
							},
							"method": {
								Type:        "string",
								Description: "How the area was probed",
								Enum:        []string{"command", "inspection"},
							},
							"result": {
								Type:        "string",
								Description: "Probing result",
								Enum:        []string{"issue_found", "no_issue", "inconclusive"},
							},
							"severity": {
								Type:        "string",
								Description: "Severity of the finding",
								Enum:        []string{"critical", "advisory"},
							},
							"evidence": {
								Type:        "string",
								Description: "Specific evidence supporting the result (file paths, command output, etc.)",
							},
						},
						Required: []string{"category", "description", "method", "result", "severity", "evidence"},
					},
					MinItems: &minItems,
				},
				"summary": {
					Type:        "string",
					Description: "Brief summary of probing findings",
				},
			},
			Required: []string{"findings", "summary"},
		},
	}
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *SubmitProbingTool) PromptDocumentation() string {
	return `- **submit_probing** - Submit robustness-probing findings
  - Parameters: findings (required array), summary (required)
  - Each finding needs: category (error_handling|malformed_input|boundary_values|resource_cleanup|idempotent_operations|security), description, method (command|inspection), result (issue_found|no_issue|inconclusive), severity (critical|advisory), evidence
  - Call this after inspecting the implementation for edge-case robustness issues`
}

// Exec validates and processes the probing submission.
//
//nolint:cyclop // Validation logic is inherently sequential; splitting would reduce clarity
func (s *SubmitProbingTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Validate required fields
	summary, err := extractRequiredString(args, "summary")
	if err != nil {
		return nil, err
	}

	// Validate findings
	findingsRaw, ok := args["findings"]
	if !ok {
		return nil, fmt.Errorf("findings parameter is required")
	}
	findingsArr, findingsOK := utils.SafeAssert[[]any](findingsRaw)
	if !findingsOK {
		return nil, fmt.Errorf("findings must be an array")
	}
	if len(findingsArr) == 0 {
		return nil, fmt.Errorf("findings must contain at least 1 item")
	}

	// Validate each finding and determine pass/fail signal.
	// Emit []any of map[string]any so consumers see the same shape
	// whether they receive data directly or after JSON-roundtrip (resume).
	hasCriticalIssue := false
	validatedFindings := make([]any, 0, len(findingsArr))

	for i, item := range findingsArr {
		finding, findingOK := utils.SafeAssert[map[string]any](item)
		if !findingOK {
			return nil, fmt.Errorf("findings item %d must be an object", i)
		}

		validated, err := validateFinding(i, finding)
		if err != nil {
			return nil, err
		}

		if validated["result"] == "issue_found" && validated["severity"] == "critical" {
			hasCriticalIssue = true
		}

		// Convert map[string]string -> map[string]any for consumer compatibility
		findingMap := make(map[string]any, len(validated))
		for k, v := range validated {
			findingMap[k] = v
		}
		validatedFindings = append(validatedFindings, findingMap)
	}

	// Determine signal based on results
	signal := SignalProbingPass
	if hasCriticalIssue {
		signal = SignalProbingFail
	}

	return &ExecResult{
		Content: "Probing findings submitted",
		ProcessEffect: &ProcessEffect{
			Signal: signal,
			Data: map[string]any{
				"findings": validatedFindings,
				"summary":  summary,
			},
		},
	}, nil
}

func isValidCategory(s string) bool {
	switch s {
	case "error_handling", "malformed_input", "boundary_values", "resource_cleanup", "idempotent_operations", "security":
		return true
	}
	return false
}

func isValidSeverity(s string) bool {
	return s == "critical" || s == "advisory"
}

func isValidProbingResult(s string) bool {
	return s == "issue_found" || s == "no_issue" || s == "inconclusive"
}

// validateFinding validates a single finding object and returns validated fields.
func validateFinding(index int, finding map[string]any) (map[string]string, error) {
	prefix := fmt.Sprintf("findings item %d", index)

	category, catOK := utils.SafeAssert[string](finding["category"])
	if !catOK || !isValidCategory(category) {
		return nil, fmt.Errorf("%s: category must be one of error_handling, malformed_input, boundary_values, resource_cleanup, idempotent_operations, security", prefix)
	}

	description, descOK := utils.SafeAssert[string](finding["description"])
	if !descOK || description == "" {
		return nil, fmt.Errorf("%s: description field is required and must be a non-empty string", prefix)
	}

	method, methodOK := utils.SafeAssert[string](finding["method"])
	if !methodOK || !isValidMethod(method) {
		return nil, fmt.Errorf("%s: method must be 'command' or 'inspection'", prefix)
	}

	result, resultOK := utils.SafeAssert[string](finding["result"])
	if !resultOK || !isValidProbingResult(result) {
		return nil, fmt.Errorf("%s: result must be 'issue_found', 'no_issue', or 'inconclusive'", prefix)
	}

	severity, sevOK := utils.SafeAssert[string](finding["severity"])
	if !sevOK || !isValidSeverity(severity) {
		return nil, fmt.Errorf("%s: severity must be 'critical' or 'advisory'", prefix)
	}

	evidence, evidOK := utils.SafeAssert[string](finding["evidence"])
	if !evidOK || evidence == "" {
		return nil, fmt.Errorf("%s: evidence field is required and must be a non-empty string", prefix)
	}

	return map[string]string{
		"category":    category,
		"description": description,
		"method":      method,
		"result":      result,
		"severity":    severity,
		"evidence":    evidence,
	}, nil
}
