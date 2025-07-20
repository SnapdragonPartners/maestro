package tools

// Tool name constants - use these instead of magic strings to prevent typos
// and enable compile-time checking
const (
	// Planning tools
	ToolSubmitPlan  = "submit_plan"
	ToolAskQuestion = "ask_question"

	// Development tools
	ToolShell       = "shell"
	ToolBuild       = "build"
	ToolTest        = "test"
	ToolLint        = "lint"
	ToolDone        = "done"
	ToolBackendInfo = "backend_info"
)
