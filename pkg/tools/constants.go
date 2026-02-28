package tools

import "orchestrator/pkg/config"

// Tool name constants - use these instead of magic strings to prevent typos.
// and enable compile-time checking.
const (
	// Planning tools.
	ToolSubmitPlan    = "submit_plan"
	ToolAskQuestion   = "ask_question"
	ToolStoryComplete = "story_complete"

	// Development tools.
	ToolFileEdit    = "file_edit"
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

	// Compose tools.
	ToolComposeUp = "compose_up"

	// Chat tools.
	ToolChatPost = "chat_post"
	ToolChatRead = "chat_read"

	// Todo tools.
	ToolTodosAdd     = "todos_add"
	ToolTodoComplete = "todo_complete"
	ToolTodoUpdate   = "todo_update"

	// Architect read tools.
	ToolReadFile       = "read_file"
	ToolListFiles      = "list_files"
	ToolGetDiff        = "get_diff"
	ToolSubmitReply    = "submit_reply"
	ToolSubmitStories  = "submit_stories"
	ToolReviewComplete = "review_complete"
	ToolStoryEdit      = "story_edit"

	// PM tools.
	ToolSpecSubmit      = "spec_submit"
	ToolChatAskUser     = "chat_ask_user"
	ToolBootstrap       = "bootstrap"
	ToolMaestroMdSubmit = "maestro_md_submit"

	// Research tools.
	// Note: ToolWebSearch is defined in web_search.go to keep tool name with its implementation.
)

// State-specific tool availability - defines which tools are available in each state.
//
//nolint:gochecknoglobals // These are constants that need to be globally accessible
var (
	// App planning tools - exploration and plan submission for application stories.
	// Includes chat tools for agent collaboration and container tools for environment verification.
	AppPlanningTools = []string{
		ToolShell,
		ToolSubmitPlan,
		ToolAskQuestion,
		ToolStoryComplete,
		ToolContainerTest,
		ToolContainerList,
		ToolChatPost,
		ToolChatRead,
		ToolWebSearch,
		ToolWebFetch,
	}

	// DevOps planning tools - exploration and plan submission for infrastructure stories.
	// Includes container tools for verification of existing infrastructure and chat for collaboration.
	DevOpsPlanningTools = []string{
		ToolShell,
		ToolSubmitPlan,
		ToolAskQuestion,
		ToolStoryComplete,
		ToolContainerTest,
		ToolContainerList,
		ToolChatPost,
		ToolChatRead,
		ToolWebSearch,
		ToolWebFetch,
	}

	// DevOpsCodingTools - identical to AppCodingTools.
	// Story type is now a hint for prompts, not tool gating.
	// Both app and devops stories have access to all coding tools.
	DevOpsCodingTools = []string{
		ToolFileEdit,
		ToolShell,
		ToolBuild,
		ToolTest,
		ToolLint,
		ToolAskQuestion,
		ToolDone,
		ToolStoryComplete,
		ToolContainerBuild,
		ToolContainerUpdate,
		ToolContainerTest,
		ToolContainerList,
		ToolContainerSwitch,
		ToolComposeUp,
		ToolChatPost,
		ToolChatRead,
		ToolTodosAdd,
		ToolTodoComplete,
		ToolTodoUpdate,
		ToolWebSearch,
		ToolWebFetch,
	}

	// App coding tools - full development environment.
	// Includes chat tools for agent collaboration, container tools for environment changes,
	// and compose for service dependencies.
	AppCodingTools = []string{
		ToolFileEdit,
		ToolShell,
		ToolBuild,
		ToolTest,
		ToolLint,
		ToolAskQuestion,
		ToolDone,
		ToolStoryComplete,
		ToolContainerBuild,
		ToolContainerUpdate,
		ToolContainerTest,
		ToolContainerList,
		ToolContainerSwitch,
		ToolComposeUp,
		ToolChatPost,
		ToolChatRead,
		ToolTodosAdd,
		ToolTodoComplete,
		ToolTodoUpdate,
		ToolWebSearch,
		ToolWebFetch,
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
		ToolWebSearch,
		ToolWebFetch,
	}

	// PM tools - unified tool set for WORKING state.
	// PM has access to read-only codebase tools, chat, spec submission, bootstrap config, and flow control.
	// PM submits specs via spec_submit (with hotfix=true for hotfixes).
	PMTools = []string{
		ToolReadFile,
		ToolListFiles,
		ToolChatPost,
		ToolChatAskUser,
		ToolBootstrap,
		ToolSpecSubmit,
		ToolWebSearch,
		ToolWebFetch,
	}

	// PMMaestroMdTools - tools for MAESTRO.md generation phase.
	// PM uses read tools to explore the project and submit the generated content.
	PMMaestroMdTools = []string{
		ToolReadFile,
		ToolListFiles,
		ToolMaestroMdSubmit,
	}

	// searchTools contains all search-related tools that should be filtered when search is disabled.
	searchTools = map[string]struct{}{
		ToolWebSearch: {},
		ToolWebFetch:  {},
	}
)

// FilterSearchTools removes web search tools from the list if search is disabled in config.
// Pass nil for cfg to auto-detect search availability from environment.
func FilterSearchTools(tools []string, cfg *config.Config) []string {
	if config.IsSearchEnabled(cfg) {
		return tools
	}

	// Filter out search tools
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		if _, isSearchTool := searchTools[tool]; !isSearchTool {
			result = append(result, tool)
		}
	}
	return result
}
