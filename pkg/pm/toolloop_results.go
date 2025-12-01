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
	SignalHotfixSubmit      = "HOTFIX_SUBMIT"
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

// ExtractPMWorkingResult is LEGACY - all PM tools now use ProcessEffect.Data.
// TODO: Remove this entire function once all PM tools use ProcessEffect pattern.
// Currently unused - bootstrap and spec_submit now use ProcessEffect.Data.
// Kept temporarily to avoid breaking existing tests but should be removed in cleanup phase.
//
//nolint:cyclop,unused,revive // Legacy code to be removed
func ExtractPMWorkingResult(_ []agent.ToolCall, _ []any) (WorkingResult, error) {
	// All PM tools now use ProcessEffect.Data - this function is obsolete
	return WorkingResult{}, toolloop.ErrNoTerminalTool
}
