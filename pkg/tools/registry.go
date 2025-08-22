// Package tools provides MCP (Model Context Protocol) tool implementations and registry.
package tools

import (
	"fmt"
	"strings"
	"sync"

	"orchestrator/pkg/build"
	execpkg "orchestrator/pkg/exec"
)

// AgentContext contains agent+state specific configuration for tool creation.
//
//nolint:govet // fieldalignment: Logical grouping preferred over memory optimization
type AgentContext struct {
	Executor        execpkg.Executor
	ReadOnly        bool
	NetworkDisabled bool
	WorkDir         string
}

// ToolFactory creates a tool instance configured for a specific agent context.
type ToolFactory func(ctx AgentContext) (Tool, error)

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
	ctx      AgentContext
	tools    map[string]Tool
	allowSet map[string]struct{}
	mu       sync.Mutex
}

// NewProvider creates a new ToolProvider for the given agent context and allowed tools.
// Automatically seals the global registry on first use.
func NewProvider(ctx AgentContext, allowedTools []string) *ToolProvider {
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
func createShellTool(ctx AgentContext) (Tool, error) {
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
func createSubmitPlanTool(_ AgentContext) (Tool, error) {
	return NewSubmitPlanTool(), nil
}

// createAskQuestionTool creates an ask question tool instance.
func createAskQuestionTool(_ AgentContext) (Tool, error) {
	return NewAskQuestionTool(), nil
}

// createMarkStoryCompleteTool creates a mark story complete tool instance.
func createMarkStoryCompleteTool(_ AgentContext) (Tool, error) {
	return NewMarkStoryCompleteTool(), nil
}

// createBuildTool creates a build tool instance.
func createBuildTool(_ AgentContext) (Tool, error) {
	// TODO: Properly inject build.Service via AgentContext
	// For now, create a temporary build service
	buildSvc := build.NewBuildService()
	return NewBuildTool(buildSvc), nil
}

// createTestTool creates a test tool instance.
func createTestTool(_ AgentContext) (Tool, error) {
	// TODO: Properly inject build.Service via AgentContext
	// For now, create a temporary build service
	buildSvc := build.NewBuildService()
	return NewTestTool(buildSvc), nil
}

// createLintTool creates a lint tool instance.
func createLintTool(_ AgentContext) (Tool, error) {
	// TODO: Properly inject build.Service via AgentContext
	// For now, create a temporary build service
	buildSvc := build.NewBuildService()
	return NewLintTool(buildSvc), nil
}

// createDoneTool creates a done tool instance.
func createDoneTool(_ AgentContext) (Tool, error) {
	return NewDoneTool(), nil
}

// createBackendInfoTool creates a backend info tool instance.
func createBackendInfoTool(_ AgentContext) (Tool, error) {
	// TODO: Properly inject build.Service via AgentContext
	// For now, create a temporary build service
	buildSvc := build.NewBuildService()
	return NewBackendInfoTool(buildSvc), nil
}

// createContainerBuildTool creates a container build tool instance.
func createContainerBuildTool(ctx AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("container build tool requires an executor")
	}
	return NewContainerBuildTool(ctx.Executor), nil
}

// createContainerUpdateTool creates a container update tool instance.
func createContainerUpdateTool(ctx AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("container update tool requires an executor")
	}
	return NewContainerUpdateTool(ctx.Executor), nil
}

// createContainerTestTool creates a unified container test tool instance.
func createContainerTestTool(ctx AgentContext) (Tool, error) {
	if ctx.Executor == nil {
		return nil, fmt.Errorf("container test tool requires an executor")
	}
	return NewContainerTestTool(ctx.Executor), nil
}

// createContainerListTool creates a container list tool instance.
func createContainerListTool(ctx AgentContext) (Tool, error) {
	return NewContainerListTool(ctx.Executor), nil
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

func getMarkStoryCompleteSchema() InputSchema {
	return NewMarkStoryCompleteTool().Definition().InputSchema
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
	return NewDoneTool().Definition().InputSchema
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
	return NewContainerTestTool(nil).Definition().InputSchema
}

func getContainerListSchema() InputSchema {
	// TODO: Implement container list tool
	return InputSchema{Type: "object"}
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

	Register(ToolMarkStoryComplete, createMarkStoryCompleteTool, &ToolMeta{
		Name:        ToolMarkStoryComplete,
		Description: "Signal that the story requirements are already fully implemented",
		InputSchema: getMarkStoryCompleteSchema(),
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
}
