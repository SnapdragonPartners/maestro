package tools

import (
	"context"
	"fmt"
)

// SubmitStoriesTool signals completion of spec analysis with structured story data.
type SubmitStoriesTool struct {
	// No executor needed - this is a control flow tool
}

// NewSubmitStoriesTool creates a new submit_stories tool.
func NewSubmitStoriesTool() *SubmitStoriesTool {
	return &SubmitStoriesTool{}
}

// Name returns the tool name.
func (t *SubmitStoriesTool) Name() string {
	return ToolSubmitStories
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *SubmitStoriesTool) PromptDocumentation() string {
	return `- **submit_stories** - Submit the analyzed requirements as structured stories
  - Parameters:
    - analysis (string, REQUIRED) - Brief summary of spec analysis and identified platform
    - platform (string, REQUIRED) - Identified platform (e.g., "go", "python", "nodejs")
    - requirements (array, REQUIRED) - Array of requirement objects with title, description, acceptance_criteria, dependencies, and story_type
    - hotfix (boolean, OPTIONAL) - If true, routes stories to the hotfix queue (generally 1 story for hotfixes)
  - Call this when you have completed spec analysis and extracted all requirements`
}

// Definition returns the tool definition for LLM.
func (t *SubmitStoriesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolSubmitStories,
		Description: "Submit the analyzed requirements as structured stories. Call this when you have completed spec analysis and extracted all implementable requirements. Use hotfix=true to route stories to the dedicated hotfix queue.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"analysis": {
					Type:        "string",
					Description: "Brief summary of what you found in the specification and the identified platform",
				},
				"platform": {
					Type:        "string",
					Description: "The identified platform (e.g., 'go', 'python', 'nodejs')",
				},
				"requirements": {
					Type:        "array",
					Description: "Array of requirement objects",
					Items: &Property{
						Type: "object",
						Properties: map[string]*Property{
							"title": {
								Type:        "string",
								Description: "Concise, clear requirement title",
							},
							"description": {
								Type:        "string",
								Description: "Detailed description of what needs to be implemented",
							},
							"acceptance_criteria": {
								Type:        "array",
								Description: "Array of specific, testable criteria (3-5 criteria recommended)",
								Items: &Property{
									Type: "string",
								},
							},
							"dependencies": {
								Type:        "array",
								Description: "Array of requirement titles this depends on",
								Items: &Property{
									Type: "string",
								},
							},
							"story_type": {
								Type:        "string",
								Description: "Either 'app' (application code) or 'devops' (infrastructure)",
								Enum:        []string{"app", "devops"},
							},
						},
					},
				},
				"hotfix": {
					Type:        "boolean",
					Description: "If true, routes stories to the dedicated hotfix queue (generally 1 story for hotfixes)",
				},
			},
			Required: []string{"analysis", "platform", "requirements"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *SubmitStoriesTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Validate required fields
	analysis, ok := args["analysis"].(string)
	if !ok || analysis == "" {
		return nil, fmt.Errorf("analysis is required and must be a non-empty string")
	}

	platform, ok := args["platform"].(string)
	if !ok || platform == "" {
		return nil, fmt.Errorf("platform is required and must be a non-empty string")
	}

	requirements, ok := args["requirements"].([]any)
	if !ok {
		return nil, fmt.Errorf("requirements is required and must be an array")
	}

	if len(requirements) == 0 {
		return nil, fmt.Errorf("requirements array cannot be empty")
	}

	// Validate each requirement has required fields
	for i, req := range requirements {
		reqMap, ok := req.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("requirement %d must be an object", i)
		}

		// Check required fields
		if _, ok := reqMap["title"].(string); !ok {
			return nil, fmt.Errorf("requirement %d: title is required", i)
		}
		if _, ok := reqMap["description"].(string); !ok {
			return nil, fmt.Errorf("requirement %d: description is required", i)
		}
		if _, ok := reqMap["acceptance_criteria"].([]any); !ok {
			return nil, fmt.Errorf("requirement %d: acceptance_criteria must be an array", i)
		}
		if _, ok := reqMap["story_type"].(string); !ok {
			return nil, fmt.Errorf("requirement %d: story_type is required", i)
		}
	}

	// Check for hotfix flag (optional, defaults to false)
	isHotfix := false
	if hotfix, ok := args["hotfix"].(bool); ok {
		isHotfix = hotfix
	}

	// Determine signal based on hotfix flag
	signal := SignalStoriesSubmitted
	queueType := "main"
	if isHotfix {
		signal = SignalHotfixSubmit
		queueType = "hotfix"
	}

	// Return human-readable message for LLM context
	// Return structured data via ProcessEffect.Data for state machine
	return &ExecResult{
		Content: fmt.Sprintf("Stories submitted to %s queue (%d requirements identified for platform: %s)", queueType, len(requirements), platform),
		ProcessEffect: &ProcessEffect{
			Signal: signal,
			Data: map[string]any{
				"analysis":     analysis,
				"platform":     platform,
				"requirements": requirements,
				"hotfix":       isHotfix,
			},
		},
	}, nil
}
