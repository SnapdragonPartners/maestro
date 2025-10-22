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
	}

	// Testing tools - validation and verification.
	TestingTools = []string{
		ToolShell,
		ToolBuild,
		ToolTest,
		ToolLint,
		ToolBackendInfo,
	}
)
