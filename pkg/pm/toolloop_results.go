package pm

import (
	"fmt"

	"orchestrator/pkg/agent"
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
	BootstrapParams map[string]string

	// Spec preview data (when preview_ready=true)
	SpecMarkdown string
	SpecMetadata map[string]any

	// AwaitUser flag (when await_user=true)
	AwaitUser bool
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

			// Don't return yet - continue checking for other signals in case of multiple tools
			continue
		}

		// Check for spec_submit signal (PREVIEW flow)
		if previewReady, ok := resultMap["preview_ready"].(bool); ok && previewReady {
			result.Signal = SignalSpecPreview

			if specMarkdown, ok := resultMap["spec_markdown"].(string); ok {
				result.SpecMarkdown = specMarkdown
			}
			if metadata, ok := resultMap["metadata"].(map[string]any); ok {
				result.SpecMetadata = metadata
			}

			// This is a terminal signal - return immediately
			return result, nil
		}

		// Check for await_user signal
		if awaitUser, ok := resultMap["await_user"].(bool); ok && awaitUser {
			result.Signal = SignalAwaitUser
			result.AwaitUser = true
			// Don't return yet - spec_preview takes precedence if both are present
		}
	}

	// If we found any signal, return it
	if result.Signal != "" {
		return result, nil
	}

	// No terminal tool was called - this is not an error, just means continue looping
	return WorkingResult{}, fmt.Errorf("no terminal tool was called in PM working phase")
}
