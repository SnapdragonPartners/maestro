package pm

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
