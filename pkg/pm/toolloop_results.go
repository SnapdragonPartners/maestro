package pm

import (
	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
)

// Signal constants for PM working phase.
const (
	SignalBootstrapComplete = "BOOTSTRAP_COMPLETE"
	SignalSpecPreview       = "SPEC_PREVIEW"
	SignalAwaitUser         = "AWAIT_USER"
)

// WorkingResult contains the outcome of PM's working phase toolloop.
// Only one field will be populated depending on which terminal tool was called.
//
//nolint:govet // String fields are logically grouped, optimization not beneficial for small struct
type WorkingResult struct {
	// Signal indicates which terminal condition was reached
	Signal string

	// Bootstrap data (when bootstrap_configured=true)
	BootstrapParams   map[string]string
	BootstrapMarkdown string // Rendered bootstrap prerequisites markdown

	// Spec preview data (when preview_ready=true from spec_submit)
	SpecMarkdown string
	SpecMetadata map[string]any
}

// ExtractPMWorkingResult extracts the result from PM's working phase tools.
// Returns the appropriate result based on which terminal tool was called.
//
//nolint:cyclop // Multiple terminal conditions naturally increase complexity
func ExtractPMWorkingResult(calls []agent.ToolCall, results []any) (WorkingResult, error) {
	result := WorkingResult{}

	for i := range calls {
		// Only process successful results
		resultMap, ok := results[i].(map[string]any)
		if !ok {
			continue
		}

		// Check for errors in result
		if success, ok := resultMap["success"].(bool); ok && !success {
			continue // Skip error results
		}

		// Check for bootstrap_configured signal
		if bootstrapConfigured, ok := resultMap["bootstrap_configured"].(bool); ok && bootstrapConfigured {
			result.Signal = SignalBootstrapComplete
			result.BootstrapParams = make(map[string]string)

			if projectName, ok := resultMap["project_name"].(string); ok {
				result.BootstrapParams["project_name"] = projectName
			}
			if gitURL, ok := resultMap["git_url"].(string); ok {
				result.BootstrapParams["git_url"] = gitURL
			}
			if platform, ok := resultMap["platform"].(string); ok {
				result.BootstrapParams["platform"] = platform
			}
			if bootstrapMarkdown, ok := resultMap["bootstrap_markdown"].(string); ok {
				result.BootstrapMarkdown = bootstrapMarkdown
			}

			// Don't return yet - continue checking for other signals in case of multiple tools
			continue
		}

		// Check for spec_submit signal (PREVIEW flow) - THE ONLY terminal signal
		if previewReady, ok := resultMap["preview_ready"].(bool); ok && previewReady {
			result.Signal = SignalSpecPreview

			if specMarkdown, ok := resultMap["spec_markdown"].(string); ok {
				result.SpecMarkdown = specMarkdown
			}
			if metadata, ok := resultMap["metadata"].(map[string]any); ok {
				result.SpecMetadata = metadata
			}

			// This is the terminal signal - return immediately
			return result, nil
		}
	}

	// Bootstrap is not terminal - continue loop after storing data
	if result.Signal == SignalBootstrapComplete {
		return result, nil
	}

	// No terminal tool was called
	return WorkingResult{}, toolloop.ErrNoTerminalTool
}
