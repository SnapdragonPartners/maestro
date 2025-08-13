// Package coder provides the coder agent implementation for the orchestrator system.
// Coder agents execute development tasks including planning, coding, testing, and review submission.
package coder

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/build"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/dockerfiles"
	"orchestrator/pkg/effect"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

const (
	// roleToolMessage represents tool message role in context manager.
	roleToolMessage = "tool"

	// budgetReviewContextTokenLimit limits the context messages included in budget review requests
	// to avoid burning excessive tokens when asking for permission to use more tokens.
	budgetReviewContextTokenLimit = 10000
)

// Coder implements the v2 FSM using agent foundation.
type Coder struct {
	*agent.BaseStateMachine // Directly embed state machine
	agentConfig             *agent.Config
	agentID                 string
	contextManager          *contextmgr.ContextManager
	llmClient               agent.LLMClient
	renderer                *templates.Renderer
	logger                  *logx.Logger
	dispatcher              *dispatch.Dispatcher           // Dispatcher for sending messages
	cloneManager            *CloneManager                  // Git clone management
	buildRegistry           *build.Registry                // Build backend registry
	buildService            *build.Service                 // Build service for MCP tools
	longRunningExecutor     *execpkg.LongRunningDockerExec // Docker executor for container per story
	planningToolProvider    *tools.ToolProvider            // Tools available during planning state
	codingToolProvider      *tools.ToolProvider            // Tools available during coding state
	pendingApprovalRequest  *ApprovalRequest               // REQUEST→RESULT flow state
	pendingQuestion         *Question
	storyCh                 <-chan *proto.AgentMsg // Channel to receive story messages
	replyCh                 <-chan *proto.AgentMsg // Channel to receive replies (for future use)
	workDir                 string                 // Current working directory (may be story-specific)
	originalWorkDir         string                 // Original agent work directory (for cleanup)
	containerName           string                 // Current story container name
	codingBudget            int                    // Iteration budgets
}

// Runtime extends BaseRuntime with coder-specific capabilities.
type Runtime struct {
	*effect.BaseRuntime
	coder *Coder
}

// NewRuntime creates a new runtime for coder effects.
func NewRuntime(coder *Coder) *Runtime {
	baseRuntime := effect.NewBaseRuntime(coder.dispatcher, coder.logger, coder.agentID, "coder")
	return &Runtime{
		BaseRuntime: baseRuntime,
		coder:       coder,
	}
}

// ReceiveMessage overrides BaseRuntime to use coder's reply channel.
func (r *Runtime) ReceiveMessage(ctx context.Context, expectedType proto.MsgType) (*proto.AgentMsg, error) {
	// Use the coder's replyCh for receiving messages
	if r.coder.replyCh == nil {
		return nil, fmt.Errorf("reply channel not available")
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("receive message cancelled: %w", ctx.Err())
	case msg, ok := <-r.coder.replyCh:
		if !ok {
			return nil, fmt.Errorf("reply channel closed unexpectedly")
		}
		if msg == nil {
			return nil, fmt.Errorf("received nil message")
		}
		if msg.Type != expectedType {
			return nil, fmt.Errorf("expected message type %s but received %s", expectedType, msg.Type)
		}
		return msg, nil
	}
}

// ExecuteEffect executes an effect using the coder's runtime environment.
func (c *Coder) ExecuteEffect(ctx context.Context, eff effect.Effect) (any, error) {
	runtime := NewRuntime(c)
	result, err := eff.Execute(ctx, runtime)
	if err != nil {
		return nil, fmt.Errorf("effect execution failed: %w", err)
	}
	return result, nil
}

// getPlanningToolsForLLM returns tool definitions for planning state tools.
func (c *Coder) getPlanningToolsForLLM() []tools.ToolDefinition {
	if c.planningToolProvider == nil {
		return nil
	}

	// Get tool metadata from provider
	toolMetas := c.planningToolProvider.List()
	definitions := make([]tools.ToolDefinition, 0, len(toolMetas))

	//nolint:gocritic // rangeValCopy: Direct access is clearer than pointer dereferencing
	for _, meta := range toolMetas {
		definitions = append(definitions, tools.ToolDefinition(meta))
	}

	c.logger.Debug("Retrieved %d planning tools for LLM", len(definitions))
	return definitions
}

// getCodingToolsForLLM returns tool definitions for coding state tools.
func (c *Coder) getCodingToolsForLLM() []tools.ToolDefinition {
	if c.codingToolProvider == nil {
		return nil
	}

	// Get tool metadata from provider
	toolMetas := c.codingToolProvider.List()
	definitions := make([]tools.ToolDefinition, 0, len(toolMetas))

	//nolint:gocritic // rangeValCopy: Direct access is clearer than pointer dereferencing
	for _, meta := range toolMetas {
		definitions = append(definitions, tools.ToolDefinition(meta))
	}

	c.logger.Debug("Retrieved %d coding tools for LLM", len(definitions))
	return definitions
}

// buildMessagesWithContext creates completion messages with context history.
// This centralizes the pattern used across PLANNING and CODING states.
func (c *Coder) buildMessagesWithContext(initialPrompt string) []agent.CompletionMessage {
	messages := []agent.CompletionMessage{
		{Role: agent.RoleUser, Content: initialPrompt},
	}

	// Add conversation history from context manager (critical for tool results).
	contextMessages := c.contextManager.GetMessages()
	for i := range contextMessages {
		msg := &contextMessages[i]
		// Skip empty messages to prevent malformed prompts.
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}

		// Map context roles to LLM client roles.
		role := agent.RoleAssistant
		if msg.Role == "user" || msg.Role == "system" {
			role = agent.RoleUser
		} else if msg.Role == roleToolMessage {
			role = agent.RoleUser // Tool messages appear as user messages to Claude
		}

		messages = append(messages, agent.CompletionMessage{
			Role:    role,
			Content: msg.Content, // Use original content without bracket formatting
		})
	}

	// Validate and sanitize messages before returning.
	sanitized, err := agent.ValidateAndSanitizeMessages(messages)
	if err != nil {
		c.logger.Warn("Message validation failed, using sanitized version: %v", err)
		// Return sanitized messages even if validation had issues.
		return sanitized
	}

	return sanitized
}

// StateDataKey provides type safety for state data access.
type stateDataKey string

// State data keys - using typed constants to prevent key mismatch bugs.
const (
	stateDataKeyPlan                     stateDataKey = KeyPlan
	stateDataKeyPlanConfidence           stateDataKey = "plan_confidence"
	stateDataKeyPlanTodos                stateDataKey = "plan_todos"
	stateDataKeyExplorationSummary       stateDataKey = "exploration_summary"
	stateDataKeyPlanRisks                stateDataKey = "plan_risks"
	stateDataKeyPlanApprovalResult       stateDataKey = KeyPlanApprovalResult
	stateDataKeyCompletionApprovalResult stateDataKey = "completion_approval_result"
	stateDataKeyCodeApprovalResult       stateDataKey = KeyCodeApprovalResult
	stateDataKeyBudgetApprovalResult     stateDataKey = "budget_approval_result"
	stateDataKeyArchitectAnswer          stateDataKey = "architect_answer"
	stateDataKeyTaskContent              stateDataKey = KeyTaskContent
	stateDataKeyStartedAt                stateDataKey = "started_at"
	stateDataKeyCodingIterations         stateDataKey = "coding_iterations"
	stateDataKeyPlanningIterations       stateDataKey = "planning_iterations"

	// BUDGET_REVIEW and other state keys - removed unused constants.
)

// PlanTodo represents a single task item in the implementation plan.
type PlanTodo struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
}

// Docker container constants.
const (
	DefaultDockerImage = config.DefaultUbuntuDockerImage // Fallback for unknown project types
)

// getMaxContextTokens returns context limits based on model name.
func getMaxContextTokens(modelName string) int {
	modelName = strings.ToLower(modelName)
	if strings.Contains(modelName, "claude") {
		return 200000 // Claude context limit
	} else if strings.Contains(modelName, "gpt") || strings.Contains(modelName, "o3") {
		return 128000 // GPT-4 Turbo / o3 context limit
	} else {
		return 32000 // Conservative default
	}
}

// getMaxReplyTokens returns reply limits based on model name.
func getMaxReplyTokens(modelName string) int {
	modelName = strings.ToLower(modelName)
	if strings.Contains(modelName, "claude") {
		return 8192 // Claude max output tokens
	} else {
		return 4096 // Conservative default
	}
}

const (
	bootstrapContainerTag = "maestro-bootstrap:latest"
)

// getDockerImageForAgent returns the appropriate Docker image based on global config.
func getDockerImageForAgent(_ string) string {
	// Use global config singleton
	globalConfig, err := config.GetConfig()
	if err != nil {
		logx.Infof("🐳 No global config available, using fallback: %s", config.DefaultUbuntuDockerImage)
		return config.DefaultUbuntuDockerImage
	}
	logx.Infof("🐳 getDockerImageForAgent: globalConfig loaded")

	if globalConfig.Container != nil {
		logx.Infof("🐳 Container config - Name='%s', Dockerfile='%s'",
			globalConfig.Container.Name,
			globalConfig.Container.Dockerfile)

		// Use final tagged container name if available (new schema)
		if globalConfig.Container.Name != "" {
			logx.Infof("🐳 Using final container name: %s", globalConfig.Container.Name)
			return globalConfig.Container.Name
		}

		// If dockerfile mode but no container built yet, build and use bootstrap container
		if globalConfig.Container.Dockerfile != "" {
			logx.Infof("🐳 Dockerfile mode detected, building bootstrap container...")
			// Build bootstrap container if needed
			if err := ensureBootstrapContainer(); err != nil {
				_ = logx.Errorf("❌ Failed to build bootstrap container: %v", err)
				return config.DefaultUbuntuDockerImage // Fallback
			}
			logx.Infof("✅ Using bootstrap container: %s", bootstrapContainerTag)
			return bootstrapContainerTag
		}

		// Fall back to platform-specific default
		platform := globalConfig.Project.PrimaryPlatform
		logx.Infof("🐳 Using platform-specific fallback for platform: %s", platform)
		switch platform {
		case "go":
			logx.Infof("🐳 Selected Go default image: %s", config.DefaultGoDockerImage)
			return config.DefaultGoDockerImage
		case "node":
			logx.Infof("🐳 Selected Node default image: node:18-alpine")
			return "node:18-alpine"
		case "python":
			logx.Infof("🐳 Selected Python default image: python:3.11-alpine")
			return "python:3.11-alpine"
		default:
			logx.Infof("🐳 Selected generic default image: %s", config.DefaultUbuntuDockerImage)
			return config.DefaultUbuntuDockerImage
		}
	}

	// 3. Final fallback if no config available
	logx.Infof("🐳 No global config available, using final fallback: %s", config.DefaultUbuntuDockerImage)
	return config.DefaultUbuntuDockerImage
}

// ensureBootstrapContainer builds the bootstrap container if it doesn't exist or if force rebuild is needed.
func ensureBootstrapContainer() error {
	// Get project directory to write the Dockerfile
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Ensure .maestro directory exists
	maestroDir := filepath.Join(projectDir, config.ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", mkdirErr)
	}

	// Write bootstrap Dockerfile to .maestro/Dockerfile.bootstrap
	dockerfilePath := filepath.Join(maestroDir, "Dockerfile.bootstrap")
	dockerfileContent := dockerfiles.GetBootstrapDockerfile()
	if writeErr := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); writeErr != nil {
		return fmt.Errorf("failed to write bootstrap Dockerfile: %w", writeErr)
	}

	// Build the bootstrap container (Docker handles caching automatically)
	logx.Infof("🔨 Building bootstrap container: %s", bootstrapContainerTag)
	logx.Infof("📋 Using Dockerfile: %s", dockerfilePath)
	logx.Infof("📁 Build context: %s", maestroDir)

	cmd := exec.Command("docker", "build", "-t", bootstrapContainerTag, "-f", dockerfilePath, maestroDir)
	cmd.Dir = projectDir

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		_ = logx.Errorf("❌ Bootstrap container build failed: %v", err)
		_ = logx.Errorf("📋 Docker build output:\n%s", outputStr)
		return fmt.Errorf("failed to build bootstrap container: %w (output: %s)", err, outputStr)
	}

	logx.Infof("✅ Bootstrap container built successfully: %s", bootstrapContainerTag)

	// Log some build details
	if strings.Contains(outputStr, "Successfully tagged") {
		logx.Infof("🏷️ Container tagged and ready for use")
	}
	if strings.Contains(outputStr, "CACHED") {
		logx.Infof("🗂️ Used cached layers for faster build")
	}
	return nil
}

// Removed unused context keys - simplified container management.

// ApprovalRequest represents a pending approval request.
type ApprovalRequest struct {
	ID      string // Correlation ID for tracking responses
	Content string
	Reason  string
	Type    proto.ApprovalType
}

// Question represents a pending question.
type Question struct {
	ID      string // Correlation ID for tracking responses
	Content string
	Reason  string
	Origin  string
}

// GetID implements the dispatch.Agent interface.
func (c *Coder) GetID() string {
	return c.agentConfig.ID
}

// SetChannels implements the ChannelReceiver interface for dispatcher attachment.
func (c *Coder) SetChannels(storyCh <-chan *proto.AgentMsg, _ chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg) {
	c.storyCh = storyCh
	c.replyCh = replyCh
	c.logger.Info("🧑‍💻 Coder %s channels set: story=%p reply=%p", c.GetID(), storyCh, replyCh)
}

// SetDispatcher implements the ChannelReceiver interface for dispatcher attachment.
func (c *Coder) SetDispatcher(dispatcher *dispatch.Dispatcher) {
	c.dispatcher = dispatcher
	c.logger.Info("🧑‍💻 Coder %s dispatcher set: %p", c.GetID(), dispatcher)
}

// SetStateNotificationChannel implements the ChannelReceiver interface for state change notifications.
func (c *Coder) SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification) {
	c.BaseStateMachine.SetStateNotificationChannel(stateNotifCh)
	c.logger.Info("🧑‍💻 Coder %s state notification channel set", c.GetID())
}

// SetCloneManager sets the clone manager (for testing).
func (c *Coder) SetCloneManager(cm *CloneManager) {
	c.cloneManager = cm
	if cm != nil && c.longRunningExecutor != nil {
		cm.SetContainerManager(c.longRunningExecutor)
	}
}

// NewCoder creates a new coder with LLM integration.
// The API key is automatically retrieved from environment variables.
func NewCoder(agentID, _, workDir string, modelConfig *config.Model, _ string, cloneManager *CloneManager, buildService *build.Service) (*Coder, error) {
	// Create LLM client using DRY helper function (same as architect)
	llmClient, err := agent.CreateLLMClientForAgent(agent.TypeCoder)
	if err != nil {
		return nil, fmt.Errorf("failed to create coder LLM client: %w", err)
	}

	// Create basic coder - use helper to inline the basic construction
	logger := logx.NewLogger(agentID)

	// Validate work directory exists
	if workDir == "" {
		return nil, logx.Errorf("work directory is required")
	}
	if mkdirErr := os.MkdirAll(workDir, 0755); mkdirErr != nil {
		return nil, logx.Wrap(mkdirErr, "failed to create work directory")
	}

	// Create template renderer
	renderer, err := templates.NewRenderer()
	if err != nil {
		// Log the error but continue with nil renderer for graceful degradation.
		fmt.Printf("ERROR: Failed to initialize coder template renderer: %v\n", err)
	}

	// Create agent context with logger.
	agentCtx := &agent.Context{
		Context: context.Background(),
		Logger:  log.New(os.Stdout, fmt.Sprintf("[%s] ", agentID), log.LstdFlags),
		Store:   nil, // State persistence handled by SQLite
		WorkDir: workDir,
	}

	// Create agent config.
	agentCfg := &agent.Config{
		ID:      agentID,
		Type:    "coder",
		Context: *agentCtx,
		LLMConfig: &agent.LLMConfig{
			MaxContextTokens: getMaxContextTokens(modelConfig.Name),
			MaxOutputTokens:  getMaxReplyTokens(modelConfig.Name),
			CompactIfOver:    2000, // Default buffer
		},
	}

	// Create state machine
	sm := agent.NewBaseStateMachine(agentID, proto.StateWaiting, nil, CoderTransitions)

	// Create build registry
	buildRegistry := build.NewRegistry()

	coder := &Coder{
		BaseStateMachine:    sm,
		agentConfig:         agentCfg,
		agentID:             agentID,
		contextManager:      contextmgr.NewContextManagerWithModel(modelConfig),
		llmClient:           llmClient,
		renderer:            renderer,
		workDir:             workDir,
		originalWorkDir:     workDir,
		logger:              logger,
		dispatcher:          nil, // Will be set during Attach()
		buildRegistry:       buildRegistry,
		buildService:        buildService,
		codingBudget:        8, // Default coding budget
		longRunningExecutor: execpkg.NewLongRunningDockerExec(getDockerImageForAgent(workDir), agentID),
		containerName:       "", // Will be set during setup
	}

	// Now that we have the coder (StateProvider), create enhanced client with metrics context
	enhancedClient, err := agent.EnhanceLLMClientWithMetrics(llmClient, agent.TypeCoder, coder, coder.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create enhanced coder LLM client: %w", err)
	}

	// Replace the client with the enhanced version
	coder.llmClient = enhancedClient

	// Set the clone manager.
	coder.cloneManager = cloneManager

	// Configure clone manager with container manager for comprehensive cleanup.
	if cloneManager != nil && coder.longRunningExecutor != nil {
		cloneManager.SetContainerManager(coder.longRunningExecutor)
	}

	return coder, nil
}

// handleLLMResponse handles LLM responses with proper empty response logic (same as architect).
func (c *Coder) handleLLMResponse(resp agent.CompletionResponse) error {
	if resp.Content != "" {
		// Case 1: Normal response with content
		c.contextManager.AddAssistantMessage(resp.Content)
		return nil
	}

	if len(resp.ToolCalls) > 0 {
		// Case 2: Pure tool use - add placeholder for conversational continuity
		toolNames := make([]string, len(resp.ToolCalls))
		for i := range resp.ToolCalls {
			toolNames[i] = resp.ToolCalls[i].Name
		}
		placeholder := fmt.Sprintf("Tool %s invoked", strings.Join(toolNames, ", "))
		c.contextManager.AddAssistantMessage(placeholder)
		return nil
	}

	// Case 3: True empty response - this is an error condition
	// DO NOT add any message to context - let upstream handle the error
	c.logger.Error("🚨 TRUE EMPTY RESPONSE: No content and no tool calls")
	return logx.Errorf("LLM returned empty response with no content and no tool calls")
}

// getRecentToolActivity returns a summary of the last N tool calls and their results.
func (c *Coder) getRecentToolActivity(limit int) string {
	if c.contextManager == nil {
		return "No context manager available"
	}

	messages := c.contextManager.GetMessages()
	if len(messages) == 0 {
		return "No recent activity"
	}

	var toolActivity []string
	toolCount := 0

	// Walk backwards through messages to find recent tool activity
	for i := len(messages) - 1; i >= 0 && toolCount < limit; i-- {
		msg := messages[i]
		if msg.Role == roleToolMessage {
			// Truncate long tool outputs for readability
			content := msg.Content
			if len(content) > 200 {
				content = content[:197] + "..."
			}
			toolActivity = append([]string{fmt.Sprintf("- %s", content)}, toolActivity...)
			toolCount++
		}
	}

	if len(toolActivity) == 0 {
		return "No recent tool activity found"
	}

	return fmt.Sprintf("Recent %d tool calls:\n%s", len(toolActivity), strings.Join(toolActivity, "\n"))
}

// detectIssuePattern analyzes recent activity to identify common problems.
func (c *Coder) detectIssuePattern() string {
	if c.contextManager == nil {
		return "Cannot analyze - no context manager"
	}

	messages := c.contextManager.GetMessages()
	if len(messages) < 3 {
		return "Insufficient activity to analyze patterns"
	}

	var recentCommands []string
	var recentErrors []string

	// Look at last 10 messages for patterns
	start := len(messages) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == roleToolMessage {
			content := strings.ToLower(msg.Content)

			// Extract command if it's a shell tool result
			if strings.Contains(content, "command:") {
				lines := strings.Split(content, "\n")
				for _, line := range lines {
					if strings.Contains(line, "command:") {
						cmd := strings.TrimSpace(strings.Split(line, "command:")[1])
						recentCommands = append(recentCommands, cmd)
						break
					}
				}
			}

			// Check for error patterns
			if strings.Contains(content, "exit_code: 1") ||
				strings.Contains(content, "exit_code: 127") ||
				strings.Contains(content, "error:") ||
				strings.Contains(content, "failed") {
				recentErrors = append(recentErrors, content)
			}
		}
	}

	// Analyze patterns
	var issues []string

	// Check for repeated implementation commands in planning
	if len(recentCommands) > 2 {
		implCmds := 0
		for _, cmd := range recentCommands {
			if strings.Contains(cmd, "go mod init") ||
				strings.Contains(cmd, "npm install") ||
				strings.Contains(cmd, "make build") ||
				strings.Contains(cmd, "go build") {
				implCmds++
			}
		}
		if implCmds > 1 {
			issues = append(issues, "Agent repeatedly trying implementation commands (may be in wrong state)")
		}
	}

	// Check for repeated failures
	if len(recentErrors) > 2 {
		issues = append(issues, fmt.Sprintf("Agent experiencing repeated failures (%d recent errors)", len(recentErrors)))
	}

	// Check for "command not found" errors
	commandNotFound := 0
	for _, cmd := range recentCommands {
		for _, err := range recentErrors {
			if strings.Contains(err, "not found") && strings.Contains(err, cmd) {
				commandNotFound++
			}
		}
	}
	if commandNotFound > 1 {
		issues = append(issues, "Agent trying to use unavailable tools/commands")
	}

	if len(issues) == 0 {
		return "No clear issue pattern detected - agent may need more specific guidance"
	}

	return strings.Join(issues, "; ")
}

// checkLoopBudget tracks loop counts and creates BudgetReviewEffect when budget is exceeded.
// Returns (BudgetReviewEffect, bool) - effect to execute and whether budget was exceeded.
func (c *Coder) checkLoopBudget(sm *agent.BaseStateMachine, key string, budget int, origin proto.State) (*effect.BudgetReviewEffect, bool) {
	// Get current iteration count.
	var iterationCount int
	if val, exists := sm.GetStateValue(key); exists {
		if count, ok := val.(int); ok {
			iterationCount = count
		}
	}

	// Increment counter.
	iterationCount++
	sm.SetStateData(key, iterationCount)

	// Check if budget exceeded.
	if iterationCount >= budget {
		// Build comprehensive budget review content
		content := c.buildBudgetReviewContent(sm, origin, iterationCount, budget)

		// Store origin state for later use.
		sm.SetStateData(KeyOrigin, string(origin))

		// Create BudgetReviewEffect with comprehensive payload
		extraPayload := map[string]any{
			"loops":           iterationCount,
			"max_loops":       budget,
			"context_size":    c.contextManager.CountTokens(),
			"recent_activity": c.getRecentToolActivity(5),
			"issue_pattern":   c.detectIssuePattern(),
			"phase_tokens":    0,   // TODO: Track per-phase
			"phase_cost_usd":  0.0, // TODO: Track per-phase
			"total_llm_calls": 0,   // TODO: Count calls
		}

		// Add story context
		if storyID := utils.GetStateValueOr[string](sm, KeyStoryID, ""); storyID != "" {
			extraPayload["story_id"] = storyID
		}

		budgetReviewEffect := &effect.BudgetReviewEffect{
			Content:      content,
			Reason:       "BUDGET_REVIEW: Loop budget exceeded, requesting guidance",
			OriginState:  string(origin),
			StoryID:      utils.GetStateValueOr[string](sm, KeyStoryID, ""),
			TargetAgent:  "architect",
			Timeout:      5 * time.Minute, // Standard timeout for budget reviews
			ExtraPayload: extraPayload,
		}

		return budgetReviewEffect, true
	}

	return nil, false
}

// ProcessState implements the v2 FSM state machine logic.
func (c *Coder) ProcessState(ctx context.Context) (proto.State, bool, error) {
	sm := c.BaseStateMachine
	currentState := c.BaseStateMachine.GetCurrentState()
	c.logger.Debug("ProcessState: coder %p, workDir: %s, currentState: %s", c, c.workDir, currentState)

	// Use global timeout wrapper
	nextState, done, err := agent.ProcessStateWithGlobalTimeout(ctx, currentState, func(ctx context.Context) (proto.State, bool, error) {
		var nextState proto.State
		var done bool
		var err error

		switch currentState {
		case proto.StateWaiting:
			nextState, done, err = c.handleWaiting(ctx, sm)
		case StateSetup:
			nextState, done, err = c.handleSetup(ctx, sm)
		case StatePlanning:
			nextState, done, err = c.handlePlanning(ctx, sm)
		case StatePlanReview:
			nextState, done, err = c.handlePlanReview(ctx, sm)
		case StateCoding:
			nextState, done, err = c.handleCoding(ctx, sm)
		case StateTesting:
			nextState, done, err = c.handleTesting(ctx, sm)
		case StateCodeReview:
			nextState, done, err = c.handleCodeReview(ctx, sm)
		case StateBudgetReview:
			nextState, done, err = c.handleBudgetReview(ctx, sm)
		case StateAwaitMerge:
			nextState, done, err = c.handleAwaitMerge(ctx, sm)
		case proto.StateDone:
			nextState, done, err = c.handleDone(ctx, sm)
		case proto.StateError:
			nextState, done, err = c.handleError(ctx, sm)
		default:
			return proto.StateError, false, logx.Errorf("unknown state: %s", c.BaseStateMachine.GetCurrentState())
		}

		// Log the state transition decision.
		if err != nil {
			c.logger.Error("🔄 State handler %s returned error: %v", currentState, err)
			// Store error message for ERROR state handling.
			sm.SetStateData(KeyErrorMessage, err.Error())
			// Transition to ERROR state instead of propagating error up.
			c.logger.Info("🔄 State handler %s → ERROR (due to error)", currentState)
			return proto.StateError, false, nil
		} else if nextState != currentState {
			c.logger.Info("🔄 State handler %s → %s (done: %v)", currentState, nextState, done)
		}

		return nextState, done, nil
	})

	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "state processing with global timeout failed")
	}

	return nextState, done, nil
}

// contextKeyAgentID is a unique type for agent ID context key.
type contextKeyAgentID string

const agentIDKey contextKeyAgentID = "agent_id"

// ProcessTask initiates task processing with the new agent foundation.
func (c *Coder) ProcessTask(ctx context.Context, taskContent string) error {
	// Add agent ID to context for debug logging.
	ctx = context.WithValue(ctx, agentIDKey, c.agentConfig.ID)

	logx.DebugFlow(ctx, "coder", "task-processing", "starting", fmt.Sprintf("content=%d chars", len(taskContent)))

	// Reset for new task.
	c.BaseStateMachine.SetStateData(string(stateDataKeyTaskContent), taskContent)
	c.BaseStateMachine.SetStateData(string(stateDataKeyStartedAt), time.Now().UTC())

	// Add to context manager.
	c.contextManager.AddMessage("user", taskContent)

	// Initialize if needed.
	if err := c.Initialize(ctx); err != nil {
		return logx.Wrap(err, "failed to initialize")
	}

	// Run the state machine loop using Step() for atomic processing.
	for {
		done, err := c.Step(ctx)
		if err != nil {
			return err
		}

		if done {
			logx.DebugFlow(ctx, "coder", "task-processing", "completed", "state machine finished")
			break
		}

		// Break out if we have pending approvals or questions to let external handler deal with them.
		if c.pendingApprovalRequest != nil || c.pendingQuestion != nil {
			logx.DebugFlow(ctx, "coder", "task-processing", "paused", "pending external response")
			break
		}
	}

	return nil
}

// handleRequestBlocking provides the blocking message receipt logic for approval requests.

// GetPendingApprovalRequest returns whether there's a pending approval request.
func (c *Coder) GetPendingApprovalRequest() (bool, string, string, string, proto.ApprovalType) {
	if c.pendingApprovalRequest == nil {
		return false, "", "", "", ""
	}
	return true, c.pendingApprovalRequest.ID, c.pendingApprovalRequest.Content, c.pendingApprovalRequest.Reason, c.pendingApprovalRequest.Type
}

// ClearPendingApprovalRequest clears the pending approval request.
func (c *Coder) ClearPendingApprovalRequest() {
	c.pendingApprovalRequest = nil
}

// GetPendingQuestion and ClearPendingQuestion moved to question.go

// ProcessApprovalResult processes approval result from architect.
func (c *Coder) ProcessApprovalResult(ctx context.Context, approvalStatus, approvalType string) error {
	// Convert legacy status to standardized format.
	standardStatus := proto.ConvertLegacyStatus(approvalStatus)

	// Validate approval type.
	stdApprovalType, valid := proto.ValidateApprovalType(approvalType)
	if !valid {
		return logx.Errorf("invalid approval type: %s", approvalType)
	}

	result := &proto.ApprovalResult{
		Type:       stdApprovalType,
		Status:     standardStatus,
		ReviewedAt: time.Now().UTC(),
	}

	// Store using the correct key based on type.
	switch stdApprovalType {
	case proto.ApprovalTypePlan:
		c.BaseStateMachine.SetStateData(string(stateDataKeyPlanApprovalResult), result)
	case proto.ApprovalTypeCode:
		c.BaseStateMachine.SetStateData(string(stateDataKeyCodeApprovalResult), result)
	case proto.ApprovalTypeBudgetReview:
		c.BaseStateMachine.SetStateData(string(stateDataKeyBudgetApprovalResult), result)
	default:
		return logx.Errorf("unknown approval type: %s", approvalType)
	}

	// Persist state to ensure approval result is saved.
	if err := c.BaseStateMachine.Persist(); err != nil {
		return logx.Wrap(err, "failed to persist approval result")
	}

	// Debug logging for approval processing.
	logx.DebugToFile(ctx, "coder", "approval_debug.log", "ProcessApprovalResult called - status=%s->%s, type=%s", approvalStatus, standardStatus, approvalType)

	return nil
}

// ProcessAnswer processes answer from architect.
func (c *Coder) ProcessAnswer(answer string) error {
	// Only handle regular QUESTION→ANSWER flow.
	// Budget review now uses REQUEST→RESULT flow.
	c.BaseStateMachine.SetStateData(string(stateDataKeyArchitectAnswer), answer)
	return nil
}

// GetContextSummary returns a summary of the current context.
func (c *Coder) GetContextSummary() string {
	messages := c.contextManager.GetMessages()
	if len(messages) == 0 {
		return "No context available"
	}

	// Return a summary of the last few messages.
	summary := fmt.Sprintf("Context summary: %d messages", len(messages))
	if len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		summary += fmt.Sprintf(", last: %s: %s", lastMsg.Role, lastMsg.Content)
	}

	return summary
}

// GetStateData returns the current state data.
func (c *Coder) GetStateData() map[string]any {
	return c.BaseStateMachine.GetStateData()
}

// GetStoryID returns the current story ID from agent state.
// Implements StateProvider interface for metrics collection.
func (c *Coder) GetStoryID() string {
	return utils.GetStateValueOr[string](c.BaseStateMachine, KeyStoryID, "")
}

// GetAgentType returns the type of the agent.
func (c *Coder) GetAgentType() agent.Type {
	return agent.TypeCoder
}

// ValidateState checks if a state is valid for this coder agent.
func (c *Coder) ValidateState(state proto.State) error {
	return ValidateState(state)
}

// GetValidStates returns all valid states for this coder agent.
func (c *Coder) GetValidStates() []proto.State {
	return GetValidStates()
}

// Run executes the driver's main loop (required for Driver interface).
func (c *Coder) Run(ctx context.Context) error {
	c.logger.Info("🧑‍💻 Coder starting state machine in %s", c.BaseStateMachine.GetCurrentState())

	// Run the state machine loop using Step().
	for {
		c.logger.Debug("🧑‍💻 Coder processing state: %s", c.BaseStateMachine.GetCurrentState())

		done, err := c.Step(ctx)
		if err != nil {
			c.logger.Error("🧑‍💻 Coder state machine error: %v", err)
			return err
		}
		if done {
			c.logger.Info("🧑‍💻 Coder state machine completed")
			break
		}
	}
	return nil
}

// Step executes a single step (required for Driver interface).
func (c *Coder) Step(ctx context.Context) (bool, error) {
	nextState, done, err := c.ProcessState(ctx)
	if err != nil {
		return false, err
	}

	// Transition to next state if different, even when done.
	currentState := c.BaseStateMachine.GetCurrentState()
	if nextState != currentState {
		// Transition validation is handled by base state machine.

		if err := c.BaseStateMachine.TransitionTo(ctx, nextState, nil); err != nil {
			return false, logx.Wrap(err, fmt.Sprintf("failed to transition to state %s", nextState))
		}
	}

	return done, nil
}

// Shutdown performs cleanup (required for Driver interface).
func (c *Coder) Shutdown(ctx context.Context) error {
	c.logger.Info("Shutting down coder agent %s", c.BaseStateMachine.GetAgentID())

	// Stop the container if it exists.
	c.cleanupContainer(ctx, "shutdown")

	// Use the executor's shutdown method for comprehensive cleanup.
	if c.longRunningExecutor != nil {
		if err := c.longRunningExecutor.Shutdown(ctx); err != nil {
			c.logger.Error("Failed to shutdown executor: %v", err)
			// Continue with persist even if container cleanup fails.
		}
	}

	c.logger.Info("Coder agent %s shutdown complete", c.BaseStateMachine.GetAgentID())
	if err := c.BaseStateMachine.Persist(); err != nil {
		return fmt.Errorf("failed to persist coder state on shutdown: %w", err)
	}
	return nil
}

// Initialize sets up the coder and loads any existing state (required for Driver interface).
func (c *Coder) Initialize(ctx context.Context) error {
	if err := c.BaseStateMachine.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize coder state machine: %w", err)
	}
	return nil
}

// processQuestionTransition moved to question.go

// handleCodingQuestionTransition processes ask_question tool results from CODING state.

// Context management helper methods.

// Placeholder helper methods for coding context management (to be enhanced as needed).

//nolint:unparam // Error return required for interface consistency
func (c *Coder) addToolResultToContext(toolCall agent.ToolCall, result any) {
	// Handle shell tool results specifically (most common case).
	if toolCall.Name == tools.ToolShell {
		// Add comprehensive shell execution details to context.
		if resultMap, ok := result.(map[string]any); ok {
			c.addShellResultToContext(resultMap)
		}
		return
	}

	// Handle other tools generically (build, test, lint, etc.).
	if resultMap, ok := result.(map[string]any); ok {
		if success, ok := resultMap["success"].(bool); ok {
			if success {
				c.logger.Info("%s tool succeeded", toolCall.Name)
				c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("%s operation completed successfully", toolCall.Name))
			} else {
				c.logger.Info("%s tool failed", toolCall.Name)
				c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("%s operation failed", toolCall.Name))
			}
		}

		if output, ok := resultMap["output"].(string); ok && output != "" {
			c.logger.Debug("%s output: %s", toolCall.Name, output)
			sanitizedOutput := sanitizeEmptyResponse(output)
			c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("%s output: %s", toolCall.Name, sanitizedOutput))
		}

		if errorMsg, ok := resultMap["error"].(string); ok && errorMsg != "" {
			c.logger.Debug("%s error: %s", toolCall.Name, errorMsg)
			sanitizedError := sanitizeEmptyResponse(errorMsg)
			c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("%s error: %s", toolCall.Name, sanitizedError))
		}
	}
}

// sanitizeEmptyResponse ensures no empty responses break agent/user alternation.
func sanitizeEmptyResponse(content string) string {
	if strings.TrimSpace(content) == "" {
		return "[no response available - try something else or try again]"
	}
	return content
}

// addShellResultToContext adds comprehensive shell execution results to context.
func (c *Coder) addShellResultToContext(resultMap map[string]any) {
	// Extract command details
	command, _ := resultMap["command"].(string)
	exitCode, _ := resultMap["exit_code"].(int)
	stdout, _ := resultMap["stdout"].(string)
	stderr, _ := resultMap["stderr"].(string)
	cwd, _ := resultMap["cwd"].(string)
	duration, _ := resultMap["duration"].(string)

	// Create comprehensive feedback message
	var feedback strings.Builder
	feedback.WriteString(fmt.Sprintf("Command: %s\n", command))
	feedback.WriteString(fmt.Sprintf("Exit Code: %d\n", exitCode))

	if cwd != "" {
		feedback.WriteString(fmt.Sprintf("Working Directory: %s\n", cwd))
	}
	if duration != "" {
		feedback.WriteString(fmt.Sprintf("Duration: %s\n", duration))
	}

	if stdout != "" {
		feedback.WriteString(fmt.Sprintf("Stdout:\n%s\n", stdout))
	} else {
		feedback.WriteString("Stdout: (empty)\n")
	}

	if stderr != "" {
		feedback.WriteString(fmt.Sprintf("Stderr:\n%s\n", stderr))
	} else {
		feedback.WriteString("Stderr: (empty)\n")
	}

	// Add to context with appropriate status
	if exitCode == 0 {
		c.logger.Info("Shell command succeeded: %s", command)
	} else {
		c.logger.Info("Shell command failed with exit code %d: %s", exitCode, command)
	}

	c.contextManager.AddMessage(roleToolMessage, feedback.String())
}

// addComprehensiveToolFailureToContext adds detailed tool failure information to context.
func (c *Coder) addComprehensiveToolFailureToContext(toolCall agent.ToolCall, err error) {
	var feedback strings.Builder
	feedback.WriteString(fmt.Sprintf("Tool: %s\n", toolCall.Name))
	feedback.WriteString(fmt.Sprintf("Error: %s\n", err.Error()))

	// Add parameters for context
	if len(toolCall.Parameters) > 0 {
		feedback.WriteString("Parameters:\n")
		for key, value := range toolCall.Parameters {
			feedback.WriteString(fmt.Sprintf("  %s: %v\n", key, value))
		}
	}

	c.contextManager.AddMessage(roleToolMessage, feedback.String())
}

// createPlanningToolProvider creates a ToolProvider for the planning state.
func (c *Coder) createPlanningToolProvider(storyType string) *tools.ToolProvider {
	// Determine planning tools based on story type
	var planningTools []string
	if storyType == string(proto.StoryTypeDevOps) {
		planningTools = tools.DevOpsPlanningTools
	} else {
		planningTools = tools.AppPlanningTools
	}

	// Create agent context for planning (read-only access)
	agentCtx := tools.AgentContext{
		Executor:        c.longRunningExecutor, // Use container executor
		ReadOnly:        true,                  // Planning is read-only
		NetworkDisabled: true,                  // No network access during planning
		WorkDir:         c.workDir,
	}

	return tools.NewProvider(agentCtx, planningTools)
}

// createCodingToolProvider creates a ToolProvider for the coding state.
func (c *Coder) createCodingToolProvider(storyType string) *tools.ToolProvider {
	// Determine coding tools based on story type
	var codingTools []string
	if storyType == string(proto.StoryTypeDevOps) {
		codingTools = tools.DevOpsCodingTools
	} else {
		codingTools = tools.AppCodingTools
	}

	// Create agent context for coding (read-write access)
	agentCtx := tools.AgentContext{
		Executor:        c.longRunningExecutor, // Use container executor
		ReadOnly:        false,                 // Coding requires write access
		NetworkDisabled: false,                 // May need network for builds/tests
		WorkDir:         c.workDir,
	}

	return tools.NewProvider(agentCtx, codingTools)
}

// buildBudgetReviewContent creates comprehensive budget review content with story, plan, and context.
func (c *Coder) buildBudgetReviewContent(sm *agent.BaseStateMachine, origin proto.State, iterationCount, budget int) string {
	// Basic budget info
	header := fmt.Sprintf("Loop budget exceeded in %s state (%d/%d iterations). How should I proceed?", origin, iterationCount, budget)

	// Get story and plan context
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))

	// Get truncated context messages
	contextMessages := c.getContextMessagesWithTokenLimit(budgetReviewContextTokenLimit)

	// Build comprehensive content
	content := fmt.Sprintf(`## Budget Review Request
%s

## Story Context
**Story ID:** %s
**Story Type:** %s

### Story Requirements
%s

## Implementation Plan
%s

## Recent Context (%d messages, ≤%d tokens)
%s

## Current State
- **Files Created:** %v
- **Tests Passed:** %v
- **Current State:** %s

Please advise how I should proceed given this context.`,
		header,
		storyID, storyType,
		taskContent,
		plan,
		len(contextMessages.Messages), budgetReviewContextTokenLimit,
		contextMessages.Content,
		utils.GetStateValueOr[[]string](sm, KeyFilesCreated, []string{}),
		utils.GetStateValueOr[bool](sm, KeyTestsPassed, false),
		origin)

	return content
}

// ContextMessages represents extracted context messages with metadata.
//
//nolint:govet // fieldalignment: struct is not performance critical
type ContextMessages struct {
	Messages []string `json:"messages"`
	Content  string   `json:"content"`
	Tokens   int      `json:"tokens"`
}

// getContextMessagesWithTokenLimit extracts recent context messages up to the token limit.
func (c *Coder) getContextMessagesWithTokenLimit(tokenLimit int) *ContextMessages {
	if c.contextManager == nil {
		return &ContextMessages{
			Messages: []string{},
			Content:  "No context manager available",
			Tokens:   0,
		}
	}

	// Create token counter
	tokenCounter, err := utils.NewTokenCounter("gpt-4")
	if err != nil {
		c.logger.Debug("Failed to create token counter: %v", err)
		return &ContextMessages{
			Messages: []string{},
			Content:  "Token counter unavailable",
			Tokens:   0,
		}
	}

	// Get recent messages from context manager
	// We'll work backwards from most recent to fit within token limit
	allMessages := c.contextManager.GetMessages()
	if len(allMessages) == 0 {
		return &ContextMessages{
			Messages: []string{},
			Content:  "No messages in context",
			Tokens:   0,
		}
	}

	var selectedMessages []string
	var totalTokens int

	// Work backwards from most recent message
	for i := len(allMessages) - 1; i >= 0; i-- {
		msg := allMessages[i]
		msgContent := fmt.Sprintf("[%s]: %s", msg.Role, msg.Content)
		msgTokens := tokenCounter.CountTokens(msgContent)

		// Check if adding this message would exceed limit
		if totalTokens+msgTokens > tokenLimit {
			// If we haven't selected any messages yet, include this one truncated
			if len(selectedMessages) == 0 {
				truncated := tokenCounter.TruncateToTokenLimit(msgContent, tokenLimit)
				selectedMessages = append([]string{truncated}, selectedMessages...)
				totalTokens = tokenCounter.CountTokens(truncated)
			}
			break
		}

		// Add message to beginning of selection (since we're working backwards)
		selectedMessages = append([]string{msgContent}, selectedMessages...)
		totalTokens += msgTokens
	}

	// Build content string
	content := "No recent messages"
	if len(selectedMessages) > 0 {
		content = fmt.Sprintf("```\n%s\n```", strings.Join(selectedMessages, "\n\n"))
	}

	return &ContextMessages{
		Messages: selectedMessages,
		Content:  content,
		Tokens:   totalTokens,
	}
}
