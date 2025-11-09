package tools

// Tool name constants - use these instead of magic strings to prevent typos.
// and enable compile-time checking.
const (
	// Planning tools.
	ToolSubmitPlan        = "submit_plan"
	ToolAskQuestion       = "ask_question"
	ToolMarkStoryComplete = "mark_story_complete"

	// Development tools.
	ToolShell       = "shell"
	ToolBuild       = "build"
	ToolTest        = "test"
	ToolLint        = "lint"
	ToolDone        = "done"
	ToolBackendInfo = "backend_info"

	// Container tools.
	ToolContainerBuild  = "container_build"
	ToolContainerUpdate = "container_update"
	ToolContainerTest   = "container_test"
	ToolContainerList   = "container_list"
	ToolContainerSwitch = "container_switch"

	// Chat tools.
	ToolChatPost = "chat_post"
	ToolChatRead = "chat_read"

	// Todo tools.
	ToolTodosAdd     = "todos_add"
	ToolTodoComplete = "todo_complete"
	ToolTodoUpdate   = "todo_update"

	// Architect read tools.
	ToolReadFile      = "read_file"
	ToolListFiles     = "list_files"
	ToolGetDiff       = "get_diff"
	ToolSubmitReply   = "submit_reply"
	ToolSubmitStories = "submit_stories"

	// PM tools.
	ToolSpecSubmit   = "spec_submit"
	ToolSpecFeedback = "spec_feedback"
)

// State-specific tool availability - defines which tools are available in each state.
//
//nolint:gochecknoglobals // These are constants that need to be globally accessible
var (
	// App planning tools - exploration and plan submission for application stories.
	// Includes chat tools for agent collaboration.
	AppPlanningTools = []string{
		ToolShell,
		ToolSubmitPlan,
		ToolAskQuestion,
		ToolMarkStoryComplete,
		ToolChatPost,
		ToolChatRead,
	}

	// DevOps planning tools - exploration and plan submission for infrastructure stories.
	// Includes container tools for verification of existing infrastructure and chat for collaboration.
	DevOpsPlanningTools = []string{
		ToolShell,
		ToolSubmitPlan,
		ToolAskQuestion,
		ToolMarkStoryComplete,
		ToolContainerTest,
		ToolContainerList,
		ToolChatPost,
		ToolChatRead,
	}

	// DevOps coding tools - infrastructure focus, container operations.
	// Includes chat tools for agent collaboration.
	DevOpsCodingTools = []string{
		ToolShell,
		ToolAskQuestion,
		ToolDone,
		ToolContainerBuild,
		ToolContainerUpdate,
		ToolContainerTest,
		ToolContainerList,
		ToolContainerSwitch,
		ToolChatPost,
		ToolChatRead,
		ToolTodosAdd,
		ToolTodoComplete,
		ToolTodoUpdate,
	}

	// App coding tools - full development environment.
	// Includes chat tools for agent collaboration.
	AppCodingTools = []string{
		ToolShell,
		ToolBuild,
		ToolTest,
		ToolLint,
		ToolAskQuestion,
		ToolDone,
		ToolChatPost,
		ToolChatRead,
		ToolTodosAdd,
		ToolTodoComplete,
		ToolTodoUpdate,
	}

	// Testing tools - validation and verification.
	TestingTools = []string{
		ToolShell,
		ToolBuild,
		ToolTest,
		ToolLint,
		ToolBackendInfo,
	}

	// Architect read tools - read-only access to coder workspaces.
	// Used in SCOPING and REQUEST states for code review and analysis.
	ArchitectReadTools = []string{
		ToolReadFile,
		ToolListFiles,
		ToolGetDiff,
		ToolSubmitReply,
	}

	// PM SUBMITTING tools - spec validation and submission.
	PMSubmittingTools = []string{
		ToolSpecSubmit,
	}

	// PM INTERVIEWING/DRAFTING tools - read-only codebase exploration.
	PMInterviewTools = []string{
		ToolReadFile,
		ToolListFiles,
	}
)
