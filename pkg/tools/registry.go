// Package tools provides MCP (Model Context Protocol) tool implementations and registry.
package tools

import (
	"fmt"
	"strings"
	"sync"

	"orchestrator/pkg/build"
	"orchestrator/pkg/chat"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/proto"
)

// AgentContext contains agent+state specific configuration for tool creation.
//
//nolint:govet // fieldalignment: Logical grouping preferred over memory optimization
type AgentContext struct {
	Executor        execpkg.Executor
	ChatService     *chat.Service
	ReadOnly        bool
	NetworkDisabled bool
	WorkDir         string
	AgentID         string // Agent identifier for tools that need it
	Agent           Agent  // Optional agent reference for state-aware tools
	ProjectDir      string // Project directory for bootstrap detection and config access
}

// Agent interface for tools that need access to agent state.
type Agent interface {
	GetCurrentState() proto.State
	GetHostWorkspacePath() string // Returns the host workspace path for container mounting
	// Todo management methods
	CompleteTodo(index int) bool            // Mark todo at index as complete (-1 for current)
	UpdateTodo(index int, desc string) bool // Update todo description at index (empty string removes)
	UpdateTodoInState()                     // Update todo_list in state machine state data
	GetIncompleteTodoCount() int            // Returns count of incomplete todos (0 if no todo list)
}

// ToolFactory creates a tool instance configured for a specific agent context.
type ToolFactory func(ctx *AgentContext) (Tool, error)

// ToolMeta contains metadata about a tool for documentation and discovery.
type ToolMeta struct {
	Name        string
	Description string
	InputSchema InputSchema
}

// toolDescriptor contains the factory and metadata for a tool.
//
//nolint:govet // fieldalignment: Logical grouping preferred over memory optimization
type toolDescriptor struct {
	meta    ToolMeta
	factory ToolFactory
}

// immutableRegistry is the global, read-only tool registry.
//
//nolint:govet // fieldalignment: Logical grouping preferred over memory optimization
type immutableRegistry struct {
	mu     sync.RWMutex
	sealed bool
	tools  map[string]toolDescriptor
}

// Global registry instance - initialized in init().
//
//nolint:gochecknoglobals // Factory pattern requires global registry
var globalRegistry = &immutableRegistry{
	tools: make(map[string]toolDescriptor),
}

// Register adds a tool factory to the global registry.
// Panics if called after the registry is sealed.
func Register(name string, factory ToolFactory, meta *ToolMeta) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	if globalRegistry.sealed {
		panic(fmt.Sprintf("tool registry sealed - cannot register tool '%s'", name))
	}

	globalRegistry.tools[name] = toolDescriptor{
		meta:    *meta,
		factory: factory,
	}
}

// Seal prevents further tool registrations.
// Called automatically when first ToolProvider is created.
func Seal() {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.sealed = true
}

// ListTools returns metadata for all registered tools.
func ListTools() []ToolMeta {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	result := make([]ToolMeta, 0, len(globalRegistry.tools))
	//nolint:gocritic // rangeValCopy: Direct access is clearer than pointer dereferencing
	for _, desc := range globalRegistry.tools {
		result = append(result, desc.meta)
	}
	return result
}

// ToolProvider creates and manages tool instances for a specific agent+state context.
//
//nolint:govet // fieldalignment: Logical grouping preferred over memory optimization
type ToolProvider struct {
	ctx      *AgentContext
	tools    map[string]Tool
	allowSet map[string]struct{}
	mu       sync.Mutex
}

// NewProvider creates a new ToolProvider for the given agent context and allowed tools.
// Automatically seals the global registry on first use.
func NewProvider(ctx *AgentContext, allowedTools []string) *ToolProvider {
	Seal() // Ensure registry is immutable

	allowSet := make(map[string]struct{}, len(allowedTools))
	for _, name := range allowedTools {
		allowSet[name] = struct{}{}
	}

	return &ToolProvider{
		ctx:      ctx,
		tools:    make(map[string]Tool),
		allowSet: allowSet,
	}
}

// Get retrieves a tool instance, creating it lazily if needed.
func (p *ToolProvider) Get(name string) (Tool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if tool is allowed
	if _, ok := p.allowSet[name]; !ok {
		return nil, fmt.Errorf("tool '%s' not allowed in this context", name)
	}

	// Return cached instance if available
	if tool, ok := p.tools[name]; ok {
		return tool, nil
	}

	// Create new instance
	globalRegistry.mu.RLock()
	desc, exists := globalRegistry.tools[name]
	globalRegistry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("tool '%s' not registered", name)
	}

	tool, err := desc.factory(p.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create tool '%s': %w", name, err)
	}

	// Cache the instance
	p.tools[name] = tool
	return tool, nil
}

// Must is like Get but panics on error. Use for tools that must exist.
func (p *ToolProvider) Must(name string) Tool {
	tool, err := p.Get(name)
	if err != nil {
		panic(err)
	}
	return tool
}

// List returns metadata for all allowed tools.
func (p *ToolProvider) List() []ToolMeta {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	result := make([]ToolMeta, 0, len(p.allowSet))
	for name := range p.allowSet {
		if desc, ok := globalRegistry.tools[name]; ok {
			result = append(result, desc.meta)
		}
	}
	return result
}

// GenerateToolDocumentation generates tool documentation for this provider's allowed tools.
func (p *ToolProvider) GenerateToolDocumentation() string {
	return GenerateToolDocumentationForTools(p.List())
}

// GenerateToolDocumentationForTools creates markdown documentation for the provided tool metadata.
func GenerateToolDocumentationForTools(tools []ToolMeta) string {
	if len(tools) == 0 {
		return "No tools available"
	}

	var doc strings.Builder
	doc.WriteString("## Available Tools\n\n")

	//nolint:gocritic // rangeValCopy: Direct access is clearer than pointer dereferencing
	for _, meta := range tools {
		// Create a temporary tool instance to get documentation
		// This is a simplified approach - in practice, we'd store documentation in metadata
		doc.WriteString(fmt.Sprintf("- **%s** - %s\n", meta.Name, meta.Description))
	}

	return doc.String()
}

// TOOL FACTORY FUNCTIONS

// createShellTool creates a shell tool instance with the provided agent context.
func createShellTool(ctx *AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("shell tool requires an executor")
	}

	return NewShellToolWithConfig(
		ctx.Executor,
		ctx.ReadOnly,
		ctx.NetworkDisabled,
		nil, // No resource limits by default
	), nil
}

// createSubmitPlanTool creates a submit plan tool instance.
func createSubmitPlanTool(_ *AgentContext) (Tool, error) {
	return NewSubmitPlanTool(), nil
}

// createAskQuestionTool creates an ask question tool instance.
func createAskQuestionTool(_ *AgentContext) (Tool, error) {
	return NewAskQuestionTool(), nil
}

// createBuildTool creates a build tool instance.
func createBuildTool(_ *AgentContext) (Tool, error) {
	// TODO: Properly inject build.Service via AgentContext
	// For now, create a temporary build service
	buildSvc := build.NewBuildService()
	return NewBuildTool(buildSvc), nil
}

// createTestTool creates a test tool instance.
func createTestTool(_ *AgentContext) (Tool, error) {
	// TODO: Properly inject build.Service via AgentContext
	// For now, create a temporary build service
	buildSvc := build.NewBuildService()
	return NewTestTool(buildSvc), nil
}

// createLintTool creates a lint tool instance.
func createLintTool(_ *AgentContext) (Tool, error) {
	// TODO: Properly inject build.Service via AgentContext
	// For now, create a temporary build service
	buildSvc := build.NewBuildService()
	return NewLintTool(buildSvc), nil
}

// createDoneTool creates a done tool instance.
func createDoneTool(ctx *AgentContext) (Tool, error) {
	return NewDoneTool(ctx.Agent), nil
}

// createBackendInfoTool creates a backend info tool instance.
func createBackendInfoTool(_ *AgentContext) (Tool, error) {
	// TODO: Properly inject build.Service via AgentContext
	// For now, create a temporary build service
	buildSvc := build.NewBuildService()
	return NewBackendInfoTool(buildSvc), nil
}

// createContainerBuildTool creates a container build tool instance.
func createContainerBuildTool(ctx *AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("container build tool requires an executor")
	}
	return NewContainerBuildTool(ctx.Executor), nil
}

// createContainerUpdateTool creates a container update tool instance.
func createContainerUpdateTool(ctx *AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("container update tool requires an executor")
	}
	return NewContainerUpdateTool(ctx.Executor), nil
}

// createContainerTestTool creates a unified container test tool instance.
func createContainerTestTool(ctx *AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("container test tool requires an executor")
	}

	if ctx.Agent == nil {
		return nil, fmt.Errorf("container test tool requires agent context for proper workDir mounting")
	}

	if ctx.WorkDir == "" {
		return nil, fmt.Errorf("container test tool requires workDir for proper workspace mounting")
	}

	// Only one constructor - requires full context
	return NewContainerTestTool(ctx.Executor, ctx.Agent, ctx.WorkDir), nil
}

// createContainerListTool creates a container list tool instance.
func createContainerListTool(ctx *AgentContext) (Tool, error) {
	return NewContainerListTool(ctx.Executor), nil
}

// createContainerSwitchTool creates a container switch tool instance.
func createContainerSwitchTool(ctx *AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("container switch tool requires an executor")
	}
	return NewContainerSwitchTool(ctx.Executor), nil
}

// createChatPostTool creates a chat post tool instance.
func createChatPostTool(ctx *AgentContext) (Tool, error) {
	if ctx.ChatService == nil {
		return nil, fmt.Errorf("chat post tool requires a chat service")
	}
	return NewChatPostTool(ctx.ChatService), nil
}

// createChatReadTool creates a chat read tool instance.
func createChatReadTool(ctx *AgentContext) (Tool, error) {
	if ctx.ChatService == nil {
		return nil, fmt.Errorf("chat read tool requires a chat service")
	}
	return NewChatReadTool(ctx.ChatService), nil
}

// SCHEMA FUNCTIONS - Extract schemas from tool implementations

func getShellSchema() InputSchema {
	return NewShellTool(nil).Definition().InputSchema
}

func getSubmitPlanSchema() InputSchema {
	return NewSubmitPlanTool().Definition().InputSchema
}

func getAskQuestionSchema() InputSchema {
	return NewAskQuestionTool().Definition().InputSchema
}

func getBuildSchema() InputSchema {
	buildSvc := build.NewBuildService()
	return NewBuildTool(buildSvc).Definition().InputSchema
}

func getTestSchema() InputSchema {
	buildSvc := build.NewBuildService()
	return NewTestTool(buildSvc).Definition().InputSchema
}

func getLintSchema() InputSchema {
	buildSvc := build.NewBuildService()
	return NewLintTool(buildSvc).Definition().InputSchema
}

func getDoneSchema() InputSchema {
	return NewDoneTool(nil).Definition().InputSchema
}

func getBackendInfoSchema() InputSchema {
	buildSvc := build.NewBuildService()
	return NewBackendInfoTool(buildSvc).Definition().InputSchema
}

func getContainerBuildSchema() InputSchema {
	return NewContainerBuildTool(nil).Definition().InputSchema
}

func getContainerUpdateSchema() InputSchema {
	return NewContainerUpdateTool(nil).Definition().InputSchema
}

func getContainerTestSchema() InputSchema {
	// Create a temporary instance just for schema extraction
	tempTool := &ContainerTestTool{}
	return tempTool.Definition().InputSchema
}

func getContainerListSchema() InputSchema {
	return NewContainerListTool(nil).Definition().InputSchema
}

func getContainerSwitchSchema() InputSchema {
	return NewContainerSwitchTool(nil).Definition().InputSchema
}

func getChatPostSchema() InputSchema {
	return NewChatPostTool(nil).Definition().InputSchema
}

func getChatReadSchema() InputSchema {
	return NewChatReadTool(nil).Definition().InputSchema
}

// createTodosAddTool creates an add todos tool instance.
func createTodosAddTool(_ *AgentContext) (Tool, error) {
	return NewTodosAddTool(), nil
}

// createTodoCompleteTool creates a todo complete tool instance.
func createTodoCompleteTool(ctx *AgentContext) (Tool, error) {
	return NewTodoCompleteTool(ctx.Agent), nil
}

// createTodoUpdateTool creates a todo update tool instance.
func createTodoUpdateTool(ctx *AgentContext) (Tool, error) {
	return NewTodoUpdateTool(ctx.Agent), nil
}

func getTodosAddSchema() InputSchema {
	return NewTodosAddTool().Definition().InputSchema
}

func getTodoCompleteSchema() InputSchema {
	return NewTodoCompleteTool(nil).Definition().InputSchema
}

func getTodoUpdateSchema() InputSchema {
	return NewTodoUpdateTool(nil).Definition().InputSchema
}

// createReadFileTool creates a read_file tool instance.
func createReadFileTool(ctx *AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("read_file tool requires an executor")
	}
	// Use WorkDir from context as workspace root
	// This should be set by the caller (e.g., /mnt/architect or /mnt/coders/coder-001)
	workspaceRoot := ctx.WorkDir
	if workspaceRoot == "" {
		return nil, fmt.Errorf("WorkDir is required for read_file tool")
	}
	return NewReadFileTool(ctx.Executor, workspaceRoot, 1048576), nil // 1MB max
}

// createListFilesTool creates a list_files tool instance.
func createListFilesTool(ctx *AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("list_files tool requires an executor")
	}
	// Use WorkDir from context as workspace root
	// This should be set by the caller (e.g., /mnt/architect or /mnt/coders/coder-001)
	workspaceRoot := ctx.WorkDir
	if workspaceRoot == "" {
		return nil, fmt.Errorf("WorkDir is required for list_files tool")
	}
	return NewListFilesTool(ctx.Executor, workspaceRoot, 1000), nil // 1000 files max
}

// createGetDiffTool creates a get_diff tool instance.
func createGetDiffTool(ctx *AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("get_diff tool requires an executor")
	}
	return NewGetDiffTool(ctx.Executor, 10000), nil // 10000 lines max
}

// createSubmitReplyTool creates a submit_reply tool instance.
func createSubmitReplyTool(_ *AgentContext) (Tool, error) {
	return NewSubmitReplyTool(), nil
}

func getReadFileSchema() InputSchema {
	return NewReadFileTool(nil, "", 0).Definition().InputSchema
}

func getListFilesSchema() InputSchema {
	return NewListFilesTool(nil, "", 0).Definition().InputSchema
}

func getGetDiffSchema() InputSchema {
	return NewGetDiffTool(nil, 0).Definition().InputSchema
}

func getSubmitReplySchema() InputSchema {
	return NewSubmitReplyTool().Definition().InputSchema
}

func createSubmitStoriesTool(_ *AgentContext) (Tool, error) {
	return NewSubmitStoriesTool(), nil
}

func getSubmitStoriesSchema() InputSchema {
	return NewSubmitStoriesTool().Definition().InputSchema
}

func createSpecSubmitTool(ctx *AgentContext) (Tool, error) {
	return NewSpecSubmitTool(ctx.ProjectDir), nil
}

func getSpecSubmitSchema() InputSchema {
	// Use empty string for schema generation (projectDir not needed for schema)
	return NewSpecSubmitTool("").Definition().InputSchema
}

func createBootstrapTool(ctx *AgentContext) (Tool, error) {
	return NewBootstrapTool(ctx.ProjectDir), nil
}

func getBootstrapSchema() InputSchema {
	return NewBootstrapTool("").Definition().InputSchema
}

func createChatAskUserTool(ctx *AgentContext) (Tool, error) {
	// Get chat service from context
	if ctx.ChatService == nil {
		return nil, fmt.Errorf("chat_service not found in AgentContext")
	}

	// Get agent ID from context
	if ctx.AgentID == "" {
		return nil, fmt.Errorf("agent_id not found in AgentContext")
	}

	return NewChatAskUserTool(ctx.ChatService, ctx.AgentID), nil
}

func getChatAskUserSchema() InputSchema {
	return NewChatAskUserTool(nil, "").Definition().InputSchema
}

func createReviewCompleteTool(_ *AgentContext) (Tool, error) {
	return NewReviewCompleteTool(), nil
}

func getReviewCompleteSchema() InputSchema {
	return NewReviewCompleteTool().Definition().InputSchema
}

// init registers all tools in the global registry using the factory pattern.
//
//nolint:gochecknoinits // Factory pattern requires init() for tool registration
func init() {
	// Register planning tools
	Register(ToolSubmitPlan, createSubmitPlanTool, &ToolMeta{
		Name:        ToolSubmitPlan,
		Description: "Submit your final implementation plan to advance to review phase",
		InputSchema: getSubmitPlanSchema(),
	})

	Register(ToolAskQuestion, createAskQuestionTool, &ToolMeta{
		Name:        ToolAskQuestion,
		Description: "Ask the architect for clarification or guidance during planning",
		InputSchema: getAskQuestionSchema(),
	})

	// Register development tools
	Register(ToolShell, createShellTool, &ToolMeta{
		Name:        ToolShell,
		Description: "Execute shell commands and return the output",
		InputSchema: getShellSchema(),
	})

	Register(ToolBuild, createBuildTool, &ToolMeta{
		Name:        ToolBuild,
		Description: "Build the project using the build system",
		InputSchema: getBuildSchema(),
	})

	Register(ToolTest, createTestTool, &ToolMeta{
		Name:        ToolTest,
		Description: "Run tests for the project",
		InputSchema: getTestSchema(),
	})

	Register(ToolLint, createLintTool, &ToolMeta{
		Name:        ToolLint,
		Description: "Run linting on the project code",
		InputSchema: getLintSchema(),
	})

	Register(ToolDone, createDoneTool, &ToolMeta{
		Name:        ToolDone,
		Description: "Signal completion of the current task",
		InputSchema: getDoneSchema(),
	})

	Register(ToolBackendInfo, createBackendInfoTool, &ToolMeta{
		Name:        ToolBackendInfo,
		Description: "Get information about the project's backend configuration",
		InputSchema: getBackendInfoSchema(),
	})

	// Register container tools
	Register(ToolContainerBuild, createContainerBuildTool, &ToolMeta{
		Name:        ToolContainerBuild,
		Description: "Build Docker container from Dockerfile with proper validation and testing",
		InputSchema: getContainerBuildSchema(),
	})

	Register(ToolContainerUpdate, createContainerUpdateTool, &ToolMeta{
		Name:        ToolContainerUpdate,
		Description: "Update container configuration in the system",
		InputSchema: getContainerUpdateSchema(),
	})

	Register(ToolContainerTest, createContainerTestTool, &ToolMeta{
		Name:        ToolContainerTest,
		Description: "Unified container testing tool - boot test, command execution, or long-running containers with TTL",
		InputSchema: getContainerTestSchema(),
	})

	Register(ToolContainerList, createContainerListTool, &ToolMeta{
		Name:        ToolContainerList,
		Description: "List available containers in the system",
		InputSchema: getContainerListSchema(),
	})

	Register(ToolContainerSwitch, createContainerSwitchTool, &ToolMeta{
		Name:        ToolContainerSwitch,
		Description: "Switch coder agent execution environment to a different container, with fallback to bootstrap container on failure",
		InputSchema: getContainerSwitchSchema(),
	})

	// Register chat tools
	Register(ToolChatPost, createChatPostTool, &ToolMeta{
		Name:        ToolChatPost,
		Description: "Post a message to the agent chat channel. Use this to communicate with other agents or ask questions in the shared chat.",
		InputSchema: getChatPostSchema(),
	})

	Register(ToolChatRead, createChatReadTool, &ToolMeta{
		Name:        ToolChatRead,
		Description: "Read new messages from the agent chat channel since your last read. Returns messages and updates your read cursor automatically.",
		InputSchema: getChatReadSchema(),
	})

	// Register todo tools
	Register(ToolTodosAdd, createTodosAddTool, &ToolMeta{
		Name:        ToolTodosAdd,
		Description: "Add todos to implementation list (initial submission or additional todos). Recommended: 3-10 items for initial list.",
		InputSchema: getTodosAddSchema(),
	})

	Register(ToolTodoComplete, createTodoCompleteTool, &ToolMeta{
		Name:        ToolTodoComplete,
		Description: "Mark a todo as complete (current todo by default, or specify index for out-of-order completion)",
		InputSchema: getTodoCompleteSchema(),
	})

	Register(ToolTodoUpdate, createTodoUpdateTool, &ToolMeta{
		Name:        ToolTodoUpdate,
		Description: "Update or remove a todo by index",
		InputSchema: getTodoUpdateSchema(),
	})

	// Register architect read tools
	Register(ToolReadFile, createReadFileTool, &ToolMeta{
		Name:        ToolReadFile,
		Description: "Read contents of a file from a coder workspace",
		InputSchema: getReadFileSchema(),
	})

	Register(ToolListFiles, createListFilesTool, &ToolMeta{
		Name:        ToolListFiles,
		Description: "List files in a coder workspace matching a pattern",
		InputSchema: getListFilesSchema(),
	})

	Register(ToolGetDiff, createGetDiffTool, &ToolMeta{
		Name:        ToolGetDiff,
		Description: "Get git diff between coder workspace and main branch",
		InputSchema: getGetDiffSchema(),
	})

	Register(ToolSubmitReply, createSubmitReplyTool, &ToolMeta{
		Name:        ToolSubmitReply,
		Description: "Submit your final response and exit iteration loop",
		InputSchema: getSubmitReplySchema(),
	})

	Register(ToolSubmitStories, createSubmitStoriesTool, &ToolMeta{
		Name:        ToolSubmitStories,
		Description: "Submit analyzed requirements as structured stories (SCOPING phase completion)",
		InputSchema: getSubmitStoriesSchema(),
	})

	// Register PM tools
	Register(ToolBootstrap, createBootstrapTool, &ToolMeta{
		Name:        ToolBootstrap,
		Description: "Configure bootstrap requirements for new project (project name, git URL, platform)",
		InputSchema: getBootstrapSchema(),
	})

	Register(ToolSpecSubmit, createSpecSubmitTool, &ToolMeta{
		Name:        ToolSpecSubmit,
		Description: "Submit finalized specification for validation and storage (PM SUBMITTING phase)",
		InputSchema: getSpecSubmitSchema(),
	})

	Register(ToolChatAskUser, createChatAskUserTool, &ToolMeta{
		Name:        ToolChatAskUser,
		Description: "Post a question to chat and wait for user response. Use when you need user input before proceeding.",
		InputSchema: getChatAskUserSchema(),
	})

	// Register review_complete tool for architect reviews
	Register(ToolReviewComplete, createReviewCompleteTool, &ToolMeta{
		Name:        ToolReviewComplete,
		Description: "Complete a review with decision (APPROVED, NEEDS_CHANGES, or REJECTED) and feedback",
		InputSchema: getReviewCompleteSchema(),
	})
}
