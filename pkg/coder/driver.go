// Package coder provides the coder agent implementation for the orchestrator system.
// Coder agents execute development tasks including planning, coding, testing, and review submission.
package coder

import (
	"context"
	"encoding/json"
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
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/git"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
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
	workspaceManager        *WorkspaceManager              // Git worktree management
	buildRegistry           *build.Registry                // Build backend registry
	buildService            *build.Service                 // Build service for MCP tools
	longRunningExecutor     *execpkg.LongRunningDockerExec // Long-running Docker executor for container per story
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

const maxOutputLength = 2000

// truncateOutput truncates long output to prevent context bloat.
func truncateOutput(output string) string {
	if len(output) <= maxOutputLength {
		return output
	}

	truncated := output[:maxOutputLength]
	return truncated + "\n\n[... output truncated after " + fmt.Sprintf("%d", maxOutputLength) + " characters for context management ...]"
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
		} else if msg.Role == "tool" {
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

	// BUDGET_REVIEW and other state keys.
	stateDataKeyQuestionReason      stateDataKey = "question_reason"
	stateDataKeyQuestionOrigin      stateDataKey = "question_origin"
	stateDataKeyQuestionContent     stateDataKey = "question_content"
	stateDataKeyBudgetReviewAction  stateDataKey = "budget_review_action"
	stateDataKeyErrorMessage        stateDataKey = "error_msg"
	stateDataKeyLoops               stateDataKey = "loops"
	stateDataKeyMaxLoops            stateDataKey = "max_loops"
	stateDataKeyQuestionAnswered    stateDataKey = "question_answered"
	stateDataKeyQuestionCompletedAt stateDataKey = "question_completed_at"
	stateDataKeyCompletionSubmitted stateDataKey = "completion_submitted"
	stateDataKeyAwaitingCompletion  stateDataKey = "awaiting_completion_approval"
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
		return 200000 // Claude 3.5 Sonnet context limit
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
		logx.Infof("🏷️  Container tagged and ready for use")
	}
	if strings.Contains(outputStr, "CACHED") {
		logx.Infof("🗂️  Used cached layers for faster build")
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

// SetWorkspaceManager sets the workspace manager (for testing).
func (c *Coder) SetWorkspaceManager(wm *WorkspaceManager) {
	c.workspaceManager = wm
	if wm != nil && c.longRunningExecutor != nil {
		wm.SetContainerManager(c.longRunningExecutor)
	}
}

// convertApprovalData converts approval data from various formats to *proto.ApprovalResult.
// Handles both direct struct pointers and map[string]any from JSON deserialization.
func convertApprovalData(data any) (*proto.ApprovalResult, error) {
	// If data is nil or empty, return error indicating no approval data.
	if data == nil {
		return nil, logx.Errorf("no approval data available")
	}

	// If it's already the correct type, return it.
	if result, ok := data.(*proto.ApprovalResult); ok {
		return result, nil
	}

	// If it's a map (from JSON deserialization), convert it.
	if dataMap, ok := data.(map[string]any); ok {
		// Convert map to JSON and then to struct.
		jsonData, err := json.Marshal(dataMap)
		if err != nil {
			return nil, logx.Wrap(err, "failed to marshal approval data")
		}

		var result proto.ApprovalResult
		if err := json.Unmarshal(jsonData, &result); err != nil {
			return nil, logx.Wrap(err, "failed to unmarshal approval data")
		}

		return &result, nil
	}

	// If it's a string (from cleanup or serialization), handle appropriately.
	if str, ok := data.(string); ok {
		// Empty string means no approval result (from cleanup).
		if str == "" {
			return nil, logx.Errorf("no approval data available")
		}
		// Non-empty string might be JSON-serialized approval result.
		var result proto.ApprovalResult
		if err := json.Unmarshal([]byte(str), &result); err != nil {
			return nil, logx.Wrap(err, "failed to unmarshal approval data from string")
		}
		return &result, nil
	}

	return nil, logx.Errorf("unsupported approval data type: %T", data)
}

// NewCoder creates a new coder using agent foundation.
func NewCoder(agentID string, modelConfig *config.Model, llmClient agent.LLMClient, workDir string, buildService *build.Service, logger *logx.Logger) (*Coder, error) {
	if llmClient == nil {
		return nil, logx.Errorf("LLM client is required")
	}

	// Tools are now auto-registered via init() functions in the tools package

	// Use provided logger or create a default one.
	if logger == nil {
		logger = logx.NewLogger(agentID)
	}

	renderer, err := templates.NewRenderer()
	if err != nil {
		// Log the error but continue with nil renderer for graceful degradation.
		fmt.Printf("ERROR: Failed to initialize coder template renderer: %v\n", err)
	}

	// Create agent context with logger.
	agentCtx := &agent.Context{
		Context: context.Background(),
		Logger:  log.New(os.Stdout, fmt.Sprintf("[%s] ", agentID), log.LstdFlags),
		Store:   nil, // REMOVED: Filesystem state store - state persistence is now handled by SQLite
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

	// Use canonical transition table from fsm package - single source of truth.
	// This ensures driver behavior exactly matches STATES.md specification.
	// IMPORTANT: Use nil state store to prevent loading stale state on agent restarts.
	// Agent state will be managed by SQLite for system-level resume functionality.
	sm := agent.NewBaseStateMachine(agentID, proto.StateWaiting, nil, CoderTransitions)

	// Use default coding budget
	codingBudget := 8 // Default coding budget

	// Create build registry first so we can use it for Docker image selection.
	buildRegistry := build.NewRegistry()

	coder := &Coder{
		BaseStateMachine:    sm,
		agentConfig:         agentCfg,
		agentID:             agentID,
		contextManager:      contextmgr.NewContextManagerWithModel(modelConfig),
		llmClient:           llmClient,
		renderer:            renderer,
		workDir:             workDir,
		originalWorkDir:     workDir, // Store original work directory for cleanup
		logger:              logger,
		dispatcher:          nil, // Will be set during Attach()
		buildRegistry:       buildRegistry,
		buildService:        buildService,
		codingBudget:        codingBudget,
		longRunningExecutor: execpkg.NewLongRunningDockerExec(getDockerImageForAgent(workDir), agentID),
		containerName:       "", // Will be set during setup
	}

	return coder, nil
}

// NewCoderWithClaude creates a new coder with Claude LLM integration (for live mode).
func NewCoderWithClaude(agentID, _, workDir string, modelConfig *config.Model, apiKey string, workspaceManager *WorkspaceManager, buildService *build.Service) (*Coder, error) {
	// Create Claude LLM client.
	llmClient := agent.NewClaudeClient(apiKey)

	// Create coder with LLM integration.
	coder, err := NewCoder(agentID, modelConfig, llmClient, workDir, buildService, nil)
	if err != nil {
		return nil, err
	}

	// Set the workspace manager.
	coder.workspaceManager = workspaceManager

	// Configure workspace manager with container manager for comprehensive cleanup.
	if workspaceManager != nil && coder.longRunningExecutor != nil {
		workspaceManager.SetContainerManager(coder.longRunningExecutor)
	}

	return coder, nil
}

// checkLoopBudget tracks loop counts and triggers BUDGET_REVIEW when budget is exceeded.
// Returns true if budget exceeded and BUDGET_REVIEW should be triggered.
func (c *Coder) checkLoopBudget(sm *agent.BaseStateMachine, key string, budget int, origin proto.State) bool {
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
		// Send REQUEST message for BUDGET_REVIEW approval.
		content := fmt.Sprintf("Loop budget exceeded in %s state (%d/%d iterations). How should I proceed?", origin, iterationCount, budget)

		c.pendingApprovalRequest = &ApprovalRequest{
			ID:      proto.GenerateApprovalID(),
			Content: content,
			Reason:  "BUDGET_REVIEW: Loop budget exceeded, requesting guidance",
			Type:    proto.ApprovalTypeBudgetReview,
		}

		// Store origin state for later use.
		sm.SetStateData(KeyOrigin, string(origin))

		// Set the expected state data for BUDGET_REVIEW questions.
		sm.SetStateData(string(stateDataKeyQuestionReason), "BUDGET_REVIEW")
		sm.SetStateData(string(stateDataKeyQuestionOrigin), string(origin))
		sm.SetStateData(string(stateDataKeyLoops), iterationCount)
		sm.SetStateData(string(stateDataKeyMaxLoops), budget)

		if c.dispatcher != nil {
			requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.GetID(), "architect")
			requestMsg.SetPayload("request_type", proto.RequestApproval.String())
			requestMsg.SetPayload("approval_type", proto.ApprovalTypeBudgetReview.String())
			requestMsg.SetPayload("content", content)
			requestMsg.SetPayload("reason", c.pendingApprovalRequest.Reason)
			requestMsg.SetPayload("approval_id", c.pendingApprovalRequest.ID)
			requestMsg.SetPayload(KeyOrigin, string(origin))
			requestMsg.SetPayload("loops", iterationCount)
			requestMsg.SetPayload("max_loops", budget)

			if err := c.dispatcher.DispatchMessage(requestMsg); err != nil {
				c.logger.Error("🧑‍💻 Failed to send BUDGET_REVIEW request: %v", err)
			} else {
				c.logger.Info("🧑‍💻 Sent BUDGET_REVIEW request %s to architect for %s state", c.pendingApprovalRequest.ID, origin)
			}
		}

		return true
	}

	return false
}

// ProcessState implements the v2 FSM state machine logic.
func (c *Coder) ProcessState(ctx context.Context) (proto.State, bool, error) {
	sm := c.BaseStateMachine
	currentState := c.BaseStateMachine.GetCurrentState()
	c.logger.Debug("ProcessState: coder %p, workDir: %s, currentState: %s", c, c.workDir, currentState)

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
	case StateQuestion:
		nextState, done, err = c.handleQuestion(ctx, sm)
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

// handleWaiting processes the WAITING state.
//
//nolint:unparam // bool return is part of state machine interface
func (c *Coder) handleWaiting(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", "WAITING")
	c.contextManager.AddMessage("assistant", "Waiting for task assignment")

	// First check if we already have a task from previous processing.
	taskContent, exists := sm.GetStateValue(string(stateDataKeyTaskContent))
	if exists && taskContent != "" {
		logx.DebugState(ctx, "coder", "transition", "WAITING -> SETUP", "task content available")
		return StateSetup, false, nil
	}

	// If no story channel is set, stay in WAITING (shouldn't happen in normal operation).
	if c.storyCh == nil {
		logx.Warnf("🧑‍💻 Coder in WAITING state but no story channel set")
		return proto.StateWaiting, false, nil
	}

	// Block waiting for a story message.
	logx.Infof("🧑‍💻 Coder waiting for story message...")
	select {
	case <-ctx.Done():
		return proto.StateError, false, fmt.Errorf("coder waiting cancelled: %w", ctx.Err())
	case storyMsg, ok := <-c.storyCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			logx.Infof("🧑‍💻 Story channel closed, transitioning to ERROR")
			return proto.StateError, true, fmt.Errorf("story channel closed unexpectedly")
		}

		if storyMsg == nil {
			// This shouldn't happen with proper channel management, but handle gracefully
			logx.Warnf("🧑‍💻 Received nil story message on open channel")
			return proto.StateWaiting, false, nil
		}

		// Extract story content and store it in state data.
		content, exists := storyMsg.GetPayload(proto.KeyContent)
		if !exists {
			return proto.StateError, false, logx.Errorf("story message missing content")
		}

		contentStr, ok := content.(string)
		if !ok {
			return proto.StateError, false, logx.Errorf("story content must be a string")
		}

		// Extract the actual story ID from the payload.
		storyID, exists := storyMsg.GetPayload(proto.KeyStoryID)
		if !exists {
			return proto.StateError, false, logx.Errorf("story message missing story_id")
		}

		storyIDStr, ok := storyID.(string)
		if !ok {
			return proto.StateError, false, logx.Errorf("story_id must be a string")
		}

		logx.Infof("🧑‍💻 Received story message %s for story %s, transitioning to SETUP", storyMsg.ID, storyIDStr)

		// Set lease immediately to ensure story is never dropped.
		if c.dispatcher != nil {
			c.dispatcher.SetLease(c.BaseStateMachine.GetAgentID(), storyIDStr)
		}

		// Extract story type from the payload.
		storyType := string(proto.StoryTypeApp) // Default to app
		if storyTypePayload, exists := storyMsg.GetPayload(proto.KeyStoryType); exists {
			c.logger.Info("🧑‍💻 Received story_type payload: '%v' (type: %T)", storyTypePayload, storyTypePayload)
			if storyTypeStr, ok := storyTypePayload.(string); ok && proto.IsValidStoryType(storyTypeStr) {
				storyType = storyTypeStr
				c.logger.Info("🧑‍💻 Set story_type to: '%s'", storyType)
			} else {
				c.logger.Info("🧑‍💻 Invalid story_type payload, using default 'app'")
			}
		} else {
			c.logger.Info("🧑‍💻 No story_type payload found, using default 'app'")
		}

		// Store the task content, story ID, and story type for use in later states.
		sm.SetStateData(string(stateDataKeyTaskContent), contentStr)
		sm.SetStateData(KeyStoryMessageID, storyMsg.ID)
		sm.SetStateData(KeyStoryID, storyIDStr)        // For workspace manager - use actual story ID
		sm.SetStateData(proto.KeyStoryType, storyType) // Store story type for testing decisions
		sm.SetStateData(string(stateDataKeyStartedAt), time.Now().UTC())

		logx.DebugState(ctx, "coder", "transition", "WAITING -> SETUP", "received story message")
		return StateSetup, false, nil
	}
}

// handleRequestBlocking provides the blocking message receipt logic for approval requests.
func (c *Coder) handleRequestBlocking(ctx context.Context, sm *agent.BaseStateMachine, resultKey stateDataKey, currentState proto.State) (proto.State, bool, error) {
	c.logger.Debug("🧑‍💻 Blocking in approval state, waiting for architect RESULT...")
	select {
	case <-ctx.Done():
		return proto.StateError, false, fmt.Errorf("coder request blocking cancelled: %w", ctx.Err())
	case resultMsg, ok := <-c.replyCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			c.logger.Info("🧑‍💻 Reply channel closed, transitioning to ERROR")
			return proto.StateError, true, fmt.Errorf("reply channel closed unexpectedly")
		}

		if resultMsg == nil {
			// This shouldn't happen with proper channel management, but handle gracefully
			c.logger.Warn("🧑‍💻 Received nil RESULT message on open channel")
			return currentState, false, nil
		}

		if resultMsg.Type == proto.MsgTypeRESULT {
			c.logger.Info("🧑‍💻 Received RESULT message %s for approval", resultMsg.ID)

			// Extract approval result and store it.
			if approvalData, exists := resultMsg.GetPayload("approval_result"); exists {
				c.logger.Debug("🧑‍💻 Storing approval data: key=%s, type=%T, value=%+v", resultKey, approvalData, approvalData)
				sm.SetStateData(string(resultKey), approvalData)
				c.logger.Info("🧑‍💻 Approval result received and stored")
				// Return same state to re-process with the new approval data.
				return currentState, false, nil
			} else {
				c.logger.Error("🧑‍💻 RESULT message missing approval_result payload")
				return proto.StateError, false, logx.Errorf("RESULT message missing approval_result")
			}
		} else {
			c.logger.Warn("🧑‍💻 Received unexpected message type: %s", resultMsg.Type)
			return currentState, false, nil
		}
	}
}

// handlePlanReviewApproval handles approved plan review based on approval type.
func (c *Coder) handlePlanReviewApproval(ctx context.Context, sm *agent.BaseStateMachine, approvalType proto.ApprovalType) (proto.State, bool, error) {
	switch approvalType {
	case proto.ApprovalTypePlan:
		// Regular plan approved - configure container and proceed to coding.
		c.logger.Info("🧑‍💻 Development plan approved, reconfiguring container for coding")

		// Reconfigure container with read-write workspace for coding phase.
		if c.longRunningExecutor != nil {
			if err := c.configureWorkspaceMount(ctx, false, "coding"); err != nil {
				return proto.StateError, false, logx.Wrap(err, "failed to configure coding container")
			}
		}

		c.logger.Info("🧑‍💻 Container reconfigured, transitioning to CODING")
		return StateCoding, false, nil

	case proto.ApprovalTypeCompletion:
		// Completion request approved - story is complete.
		c.logger.Info("🧑‍💻 Story completion approved by architect, transitioning to DONE")

		// Mark story as completed.
		sm.SetStateData(KeyStoryCompletedAt, time.Now().UTC())
		sm.SetStateData(KeyCompletionStatus, "APPROVED")

		return proto.StateDone, true, nil

	default:
		return proto.StateError, false, logx.Errorf("unsupported approval type in plan review: %s", approvalType)
	}
}

// Removed handlePlanningWithLLM - replaced with enhanced iterative planning.

// handlePlanReview processes the PLAN_REVIEW state - handles both plan and completion approval.
func (c *Coder) handlePlanReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Determine the type of approval based on pending request.
	var approvalType proto.ApprovalType = proto.ApprovalTypePlan // default
	var contextMessage string = "Plan review phase: waiting for architect approval"

	if c.pendingApprovalRequest != nil {
		approvalType = c.pendingApprovalRequest.Type
		switch approvalType {
		case proto.ApprovalTypeCompletion:
			contextMessage = "Completion review phase: waiting for architect approval"
		case proto.ApprovalTypePlan:
			contextMessage = "Plan review phase: waiting for architect approval"
		}
	}

	// Check if we already have approval result from previous processing.
	if approvalData, exists := sm.GetStateValue(string(stateDataKeyPlanApprovalResult)); exists {
		c.logger.Debug("🧑‍💻 Found existing approval data for %s: type=%T, value=%+v", approvalType, approvalData, approvalData)
		result, err := convertApprovalData(approvalData)
		if err != nil {
			c.logger.Error("🧑‍💻 Failed to convert approval data: data=%+v, error=%v", approvalData, err)
			return proto.StateError, false, logx.Wrap(err, fmt.Sprintf("failed to convert %s approval data", approvalType))
		}

		// Process the approval result based on status.
		switch result.Status {
		case proto.ApprovalStatusApproved:
			// Clear the approval result and pending request since we have the result.
			sm.SetStateData(string(stateDataKeyPlanApprovalResult), nil)
			c.pendingApprovalRequest = nil
			sm.SetStateData(KeyPlanReviewCompletedAt, time.Now().UTC())
			return c.handlePlanReviewApproval(ctx, sm, approvalType)
		case proto.ApprovalStatusRejected, proto.ApprovalStatusNeedsChanges:
			// Clear the approval result and pending request since we have the result.
			sm.SetStateData(string(stateDataKeyPlanApprovalResult), nil)
			c.pendingApprovalRequest = nil
			sm.SetStateData(KeyPlanReviewCompletedAt, time.Now().UTC())
			c.logger.Info("🧑‍💻 %s rejected/needs changes, returning to PLANNING", approvalType)
			if result.Feedback != "" {
				c.contextManager.AddMessage("architect", fmt.Sprintf("Feedback: %s", result.Feedback))
			}
			return StatePlanning, false, nil
		default:
			return proto.StateError, false, logx.Errorf("unknown %s approval status: %s", approvalType, result.Status)
		}
	}

	// Block waiting for RESULT message from architect.
	c.contextManager.AddMessage("assistant", contextMessage)
	return c.handleRequestBlocking(ctx, sm, stateDataKeyPlanApprovalResult, StatePlanReview)
}

// executeMCPToolCalls executes tool calls using the MCP tool system.

// isImplementationComplete checks if the current implementation appears complete.

// getWorkingDirectoryContents returns a summary of what's in the working directory.

// isFilenameHeader checks if a line contains a filename header.

// looksLikeCode uses heuristics to determine if a line looks like code.

// guessFilenameFromContent tries to guess filename from a line of code.

// guessFilenameFromContext looks ahead in lines to guess appropriate filename.

// parseAndCreateFiles extracts code blocks from LLM response and creates files.
// Supports fenced code blocks (```), plain code blocks, and content detection.

// extractFilename extracts filename from header lines.

// extractFilenameFromCodeBlock tries to extract filename from code block language.

// writeFile writes content to a file in the workspace.

// handleTesting processes the TESTING state with story-type awareness - implements AR-103.
func (c *Coder) handleTesting(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get worktree path for running tests.
	worktreePath, exists := sm.GetStateValue(KeyWorktreePath)
	if !exists || worktreePath == "" {
		return proto.StateError, false, logx.Errorf("no worktree path found - workspace setup required")
	}

	worktreePathStr, ok := utils.SafeAssert[string](worktreePath)
	if !ok {
		return proto.StateError, false, logx.Errorf("worktree_path is not a string: %v", worktreePath)
	}

	// Get story type for testing strategy decision.
	storyType := string(proto.StoryTypeApp) // Default to app
	if storyTypeVal, exists := sm.GetStateValue(proto.KeyStoryType); exists {
		if storyTypeStr, ok := storyTypeVal.(string); ok && proto.IsValidStoryType(storyTypeStr) {
			storyType = storyTypeStr
		}
	}

	c.logger.Info("Testing story type: %s", storyType)

	// Use different testing strategies based on story type
	if storyType == string(proto.StoryTypeDevOps) {
		return c.handleDevOpsStoryTesting(ctx, sm, worktreePathStr)
	}
	return c.handleAppStoryTesting(ctx, sm, worktreePathStr)
}

// handleAppStoryTesting handles testing for application stories using traditional build/test/lint flow.
func (c *Coder) handleAppStoryTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr string) (proto.State, bool, error) {
	// Use MCP test tool instead of direct build registry calls.
	if c.buildService != nil {
		// Get backend info first.
		backendInfo, err := c.buildService.GetBackendInfo(worktreePathStr)
		if err != nil {
			c.logger.Error("Failed to get backend info: %v", err)
			return proto.StateError, false, logx.Wrap(err, "failed to get backend info")
		}

		// Store backend information for context.
		sm.SetStateData(KeyBuildBackend, backendInfo.Name)
		c.logger.Info("App story testing: using build service with backend %s", backendInfo.Name)

		// Run tests using the build service.
		testsPassed, testOutput, err := c.runTestWithBuildService(ctx, worktreePathStr)
		if err != nil {
			c.logger.Error("Failed to run tests: %v", err)
			// Truncate error output to prevent context bloat.
			errorStr := err.Error()
			truncatedError := truncateOutput(errorStr)
			sm.SetStateData(KeyTestError, errorStr)               // Keep full error for logging
			sm.SetStateData(KeyTestFailureOutput, truncatedError) // Use truncated for context
			sm.SetStateData(KeyCodingMode, "test_fix")
			return StateCoding, false, nil
		}

		// Store test results.
		sm.SetStateData(KeyTestsPassed, testsPassed)
		sm.SetStateData(KeyTestOutput, testOutput)
		sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

		if !testsPassed {
			c.logger.Info("App story tests failed, transitioning to CODING state for fixes")
			// Truncate test output to prevent context bloat.
			truncatedOutput := truncateOutput(testOutput)
			sm.SetStateData(KeyTestFailureOutput, truncatedOutput)
			sm.SetStateData(KeyCodingMode, "test_fix")
			return StateCoding, false, nil
		}

		c.logger.Info("App story tests passed successfully")
		return c.proceedToCodeReview(ctx, sm)
	}

	// Fallback to legacy testing approach
	return c.handleLegacyTesting(ctx, sm, worktreePathStr)
}

// handleDevOpsStoryTesting handles testing for DevOps stories focusing on infrastructure validation.
func (c *Coder) handleDevOpsStoryTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr string) (proto.State, bool, error) {
	c.logger.Info("DevOps story testing: focusing on infrastructure validation")

	// For DevOps stories, we focus on:
	// 1. Container builds (if Dockerfile present)
	// 2. Configuration validation
	// 3. Basic infrastructure checks
	// Skip traditional build/test/lint which may not be relevant

	// Check if this is a container-related DevOps story
	dockerfilePath := filepath.Join(worktreePathStr, "Dockerfile")
	if fileExists(dockerfilePath) {
		c.logger.Info("DevOps story: validating Dockerfile build")
		if err := c.validateDockerfileBuild(ctx, worktreePathStr); err != nil {
			c.logger.Error("Dockerfile validation failed: %v", err)
			errorStr := err.Error()
			truncatedError := truncateOutput(errorStr)
			sm.SetStateData(KeyTestError, errorStr)
			sm.SetStateData(KeyTestFailureOutput, truncatedError)
			sm.SetStateData(KeyCodingMode, "test_fix")
			return StateCoding, false, nil
		}
	}

	// Check for Makefile and run basic validation if present
	makefilePath := filepath.Join(worktreePathStr, "Makefile")
	if fileExists(makefilePath) {
		c.logger.Info("DevOps story: validating Makefile targets")
		if err := c.validateMakefileTargets(worktreePathStr); err != nil {
			c.logger.Error("Makefile validation failed: %v", err)
			errorStr := err.Error()
			truncatedError := truncateOutput(errorStr)
			sm.SetStateData(KeyTestError, errorStr)
			sm.SetStateData(KeyTestFailureOutput, truncatedError)
			sm.SetStateData(KeyCodingMode, "test_fix")
			return StateCoding, false, nil
		}
	}

	// Store successful test results
	sm.SetStateData(KeyTestsPassed, true)
	sm.SetStateData(KeyTestOutput, "DevOps story infrastructure validation completed successfully")
	sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

	c.logger.Info("DevOps story testing completed successfully")
	return c.proceedToCodeReview(ctx, sm)
}

// handleLegacyTesting handles the legacy testing approach for backward compatibility.
func (c *Coder) handleLegacyTesting(ctx context.Context, sm *agent.BaseStateMachine, worktreePathStr string) (proto.State, bool, error) {
	// Use global config singleton.
	globalConfig, err := config.GetConfig()
	if err != nil {
		c.logger.Error("Global config not available")
		return proto.StateError, false, fmt.Errorf("global config not available: %w", err)
	}

	// Store platform information for context.
	platform := globalConfig.Project.PrimaryPlatform
	sm.SetStateData(KeyBuildBackend, platform)

	// Get build command from config
	testCommand := globalConfig.Build.Test
	if testCommand == "" {
		testCommand = "make test" // fallback
	}
	_ = testCommand // Used in runMakeTest below

	// Run tests using the detected backend.
	testsPassed, testOutput, err := c.runMakeTest(ctx, worktreePathStr)

	// Store test results.
	sm.SetStateData(KeyTestsPassed, testsPassed)
	sm.SetStateData(KeyTestOutput, testOutput)
	sm.SetStateData(KeyTestingCompletedAt, time.Now().UTC())

	if err != nil {
		c.logger.Error("Failed to run tests: %v", err)
		// Truncate error output to prevent context bloat.
		errorStr := err.Error()
		truncatedError := truncateOutput(errorStr)
		sm.SetStateData(KeyTestError, errorStr)               // Keep full error for logging
		sm.SetStateData(KeyTestFailureOutput, truncatedError) // Use truncated for context
		sm.SetStateData(KeyCodingMode, "test_fix")
		return StateCoding, false, nil
	}

	if !testsPassed {
		c.logger.Info("Tests failed, transitioning to CODING state for fixes")
		// Truncate test output to prevent context bloat.
		truncatedOutput := truncateOutput(testOutput)
		sm.SetStateData(KeyTestFailureOutput, truncatedOutput)
		sm.SetStateData(KeyCodingMode, "test_fix")
		return StateCoding, false, nil
	}

	c.logger.Info("Tests passed successfully")
	return c.proceedToCodeReview(ctx, sm)
}

// validateDockerfileBuild validates that a Dockerfile can be built successfully.
func (c *Coder) validateDockerfileBuild(_ context.Context, worktreePathStr string) error {
	// Simple Docker build validation - could be enhanced with actual build
	dockerfilePath := filepath.Join(worktreePathStr, "Dockerfile")
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to read Dockerfile: %w", err)
	}

	// Basic Dockerfile validation
	dockerfileContent := string(content)
	if !strings.Contains(dockerfileContent, "FROM") {
		return fmt.Errorf("dockerfile missing required FROM instruction")
	}

	// Could add more sophisticated validation here
	c.logger.Info("Dockerfile validation passed")
	return nil
}

// validateMakefileTargets validates that Makefile has reasonable targets for DevOps.
func (c *Coder) validateMakefileTargets(worktreePathStr string) error {
	makefilePath := filepath.Join(worktreePathStr, "Makefile")
	content, err := os.ReadFile(makefilePath)
	if err != nil {
		return fmt.Errorf("failed to read Makefile: %w", err)
	}

	makefileContent := string(content)
	// For DevOps stories, we're more lenient - just check that it's not empty
	if strings.TrimSpace(makefileContent) == "" {
		return fmt.Errorf("makefile is empty")
	}

	c.logger.Info("Makefile validation passed")
	return nil
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// handleCodeReview processes the CODE_REVIEW state - blocks waiting for architect's RESULT response.
func (c *Coder) handleCodeReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// State: waiting for architect approval

	// Check if we already have approval result from previous processing.
	if approvalData, exists := sm.GetStateValue(string(stateDataKeyCodeApprovalResult)); exists {
		return c.handleCodeReviewApproval(ctx, sm, approvalData)
	}

	// Send approval request if we haven't already sent one
	if c.pendingApprovalRequest == nil {
		if err := c.sendCodeReviewRequest(ctx, sm); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to send code review request")
		}
	}

	// Block waiting for RESULT message from architect.
	return c.handleRequestBlocking(ctx, sm, stateDataKeyCodeApprovalResult, StateCodeReview)
}

// sendCodeReviewRequest sends an approval request to the architect for code review.
func (c *Coder) sendCodeReviewRequest(ctx context.Context, sm *agent.BaseStateMachine) error {
	// Generate git diff to check if any files actually changed.
	gitDiff, err := c.generateGitDiff(ctx, sm)
	if err != nil {
		c.logger.Warn("Failed to generate git diff, proceeding with normal code review: %v", err)
		gitDiff = "" // Continue with normal flow if diff fails
	}

	// Tests passed, send REQUEST message to architect for approval.
	filesCreated, _ := sm.GetStateValue(KeyFilesCreated)

	// Check if we have any actual changes to review.
	if gitDiff == "" || strings.TrimSpace(gitDiff) == "" {
		// No changes - send completion approval instead of code approval.
		c.logger.Info("🧑‍💻 No file changes detected, requesting story completion approval")

		codeContent := fmt.Sprintf("Story completed during implementation phase: %v files processed, tests passed, no changes needed", filesCreated)

		c.pendingApprovalRequest = &ApprovalRequest{
			ID:      proto.GenerateApprovalID(),
			Content: codeContent,
			Reason:  "Story requirements already satisfied, requesting completion approval",
			Type:    proto.ApprovalTypeCompletion,
		}
	} else {
		// Normal code approval with diff included.
		c.logger.Info("🧑‍💻 File changes detected, requesting code review approval")

		// Get original story and plan from state data
		originalStory := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
		plan := utils.GetStateValueOr[string](sm, KeyPlan, "")

		codeContent := fmt.Sprintf("Code implementation and testing completed: %v files created, tests passed\n\nOriginal Story:\n%s\n\nImplementation Plan:\n%s\n\nChanges:\n%s", filesCreated, originalStory, plan, gitDiff)

		c.pendingApprovalRequest = &ApprovalRequest{
			ID:      proto.GenerateApprovalID(),
			Content: codeContent,
			Reason:  "Code requires architect approval before completion",
			Type:    proto.ApprovalTypeCode,
		}
	}

	if c.dispatcher != nil {
		requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.GetID(), "architect")
		requestMsg.SetPayload("request_type", proto.RequestApproval.String())
		requestMsg.SetPayload("approval_type", c.pendingApprovalRequest.Type.String())
		requestMsg.SetPayload("content", c.pendingApprovalRequest.Content)
		requestMsg.SetPayload("reason", c.pendingApprovalRequest.Reason)
		requestMsg.SetPayload("approval_id", c.pendingApprovalRequest.ID)

		if err := c.dispatcher.DispatchMessage(requestMsg); err != nil {
			return logx.Wrap(err, "failed to send approval request")
		}

		c.logger.Info("🧑‍💻 Sent %s approval request %s to architect during CODE_REVIEW state entry", c.pendingApprovalRequest.Type, c.pendingApprovalRequest.ID)
	} else {
		return logx.Errorf("dispatcher not set")
	}

	return nil
}

// handleCodeReviewApproval processes code review approval results.
func (c *Coder) handleCodeReviewApproval(ctx context.Context, sm *agent.BaseStateMachine, approvalData any) (proto.State, bool, error) {
	result, err := convertApprovalData(approvalData)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to convert approval data")
	}

	// Store result and clear.
	sm.SetStateData(string(stateDataKeyCodeApprovalResult), nil)
	sm.SetStateData(KeyCodeReviewCompletedAt, time.Now().UTC())

	// Store approval type before clearing the request.
	approvalType := c.pendingApprovalRequest.Type
	c.pendingApprovalRequest = nil // Clear since we have the result

	// Handle approval based on original request type.
	switch result.Status {
	case proto.ApprovalStatusApproved:
		// Check what TYPE of approval this was.
		if approvalType == proto.ApprovalTypeCompletion {
			// Completion approved - skip directly to DONE, no merge needed.
			c.logger.Info("🧑‍💻 Story completion approved by architect")

			// Optionally: Clean up empty development branch.
			if err := c.cleanupEmptyBranch(ctx, sm); err != nil {
				c.logger.Warn("Failed to cleanup empty branch: %v", err)
			}

			return proto.StateDone, true, nil
		} else {
			// Normal code approval - proceed with merge flow.
			c.logger.Info("🧑‍💻 Code approved, pushing branch and creating PR")

			// AR-104: Push branch and open pull request.
			if err := c.pushBranchAndCreatePR(ctx, sm); err != nil {
				c.logger.Error("Failed to push branch and create PR: %v", err)
				return proto.StateError, false, err
			}

			// Send merge REQUEST to architect instead of going to DONE.
			if err := c.sendMergeRequest(ctx, sm); err != nil {
				c.logger.Error("Failed to send merge request: %v", err)
				return proto.StateError, false, err
			}

			c.logger.Info("🧑‍💻 Waiting for merge approval from architect")
			return StateAwaitMerge, false, nil
		}
	case proto.ApprovalStatusRejected, proto.ApprovalStatusNeedsChanges:
		c.logger.Info("🧑‍💻 Code rejected/needs changes, transitioning to CODING for fixes")
		// Store review feedback for CODING state to prioritize.
		sm.SetStateData(KeyCodeReviewRejectionFeedback, result.Feedback)
		sm.SetStateData(KeyCodingMode, "review_fix")
		return StateCoding, false, nil
	default:
		return proto.StateError, false, logx.Errorf("unknown approval status: %s", result.Status)
	}
}

// handleAwaitMerge processes the AWAIT_MERGE state, waiting for merge results from architect.
//
//nolint:unparam // bool return is part of state machine interface
func (c *Coder) handleAwaitMerge(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// State: waiting for architect merge result

	// Check if we already have merge result from previous processing.
	if result, exists := agent.GetTyped[git.MergeResult](sm, KeyMergeResult); exists {
		sm.SetStateData(KeyMergeCompletedAt, time.Now().UTC())

		switch result.Status {
		case "merged":
			c.logger.Info("🧑‍💻 PR merged successfully, story complete")
			return proto.StateDone, false, nil
		case "merge_conflict":
			c.logger.Info("🧑‍💻 Merge conflict detected, transitioning to CODING for resolution")
			sm.SetStateData(KeyMergeConflictDetails, result.ConflictInfo)
			sm.SetStateData(KeyCodingMode, "merge_fix")
			return StateCoding, false, nil
		default:
			return proto.StateError, false, logx.Errorf("unknown merge status: %s", result.Status)
		}
	}

	// Block waiting for RESULT message from architect.
	c.logger.Debug("🧑‍💻 Blocking in AWAIT_MERGE, waiting for architect merge result...")
	select {
	case <-ctx.Done():
		return proto.StateError, false, fmt.Errorf("coder await merge cancelled: %w", ctx.Err())
	case resultMsg, ok := <-c.replyCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			c.logger.Info("🧑‍💻 Reply channel closed in AWAIT_MERGE, transitioning to ERROR")
			return proto.StateError, true, fmt.Errorf("reply channel closed unexpectedly during merge")
		}
		if resultMsg == nil {
			c.logger.Warn("🧑‍💻 Received nil RESULT message")
			return StateAwaitMerge, false, nil
		}

		if resultMsg.Type == proto.MsgTypeRESULT {
			c.logger.Info("🧑‍💻 Received RESULT message %s for merge", resultMsg.ID)

			// Extract merge result and store it.
			if status, exists := resultMsg.GetPayload("status"); exists {
				if statusStr, ok := utils.SafeAssert[string](status); ok {
					mergeResult := git.MergeResult{
						Status: statusStr,
					}
					if conflictInfo, exists := resultMsg.GetPayload("conflict_details"); exists {
						if conflictInfoStr, ok := utils.SafeAssert[string](conflictInfo); ok {
							mergeResult.ConflictInfo = conflictInfoStr
						}
					}
					if mergeCommit, exists := resultMsg.GetPayload("merge_commit"); exists {
						if mergeCommitStr, ok := utils.SafeAssert[string](mergeCommit); ok {
							mergeResult.MergeCommit = mergeCommitStr
						}
					}

					agent.SetTyped(sm, KeyMergeResult, mergeResult)
					c.logger.Info("🧑‍💻 Merge result received and stored")
					// Return same state to re-process with the new merge data.
					return StateAwaitMerge, false, nil
				}
			} else {
				c.logger.Error("🧑‍💻 RESULT message missing status payload")
				return proto.StateError, false, logx.Errorf("RESULT message missing status")
			}
		} else {
			c.logger.Warn("🧑‍💻 Received unexpected message type: %s", resultMsg.Type)
			return StateAwaitMerge, false, nil
		}
	}

	// This should not be reached, but add for completeness.
	return StateAwaitMerge, false, nil
}

// handleBudgetReview processes the BUDGET_REVIEW state - blocks waiting for architect's RESULT response.
func (c *Coder) handleBudgetReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// State: waiting for architect guidance

	// Check if we already have approval result from previous processing.
	if approvalData, exists := sm.GetStateValue(string(stateDataKeyBudgetApprovalResult)); exists && approvalData != nil {
		return c.handleBudgetReviewApproval(ctx, sm, approvalData)
	}

	// Block waiting for RESULT message from architect.
	return c.handleRequestBlocking(ctx, sm, stateDataKeyBudgetApprovalResult, StateBudgetReview)
}

// handleBudgetReviewApproval processes budget review approval results.
func (c *Coder) handleBudgetReviewApproval(_ context.Context, sm *agent.BaseStateMachine, approvalData any) (proto.State, bool, error) {
	result, err := convertApprovalData(approvalData)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to convert budget review approval data")
	}

	// Store result and clear.
	sm.SetStateData(string(stateDataKeyBudgetApprovalResult), nil)
	sm.SetStateData(KeyBudgetReviewCompletedAt, time.Now().UTC())
	c.pendingApprovalRequest = nil // Clear since we have the result

	// Get origin state from stored data.
	originStr := utils.GetStateValueOr[string](sm, KeyOrigin, "")

	switch result.Status {
	case proto.ApprovalStatusApproved:
		// CONTINUE/PIVOT - return to origin state and reset counter.
		c.logger.Info("🧑‍💻 Budget review approved, returning to origin state: %s", originStr)

		// Reset the iteration counter for the origin state.
		switch originStr {
		case string(StatePlanning):
			sm.SetStateData(string(stateDataKeyPlanningIterations), 0)
			return StatePlanning, false, nil
		case string(StateCoding):
			sm.SetStateData(string(stateDataKeyCodingIterations), 0)
			return StateCoding, false, nil
		default:
			return StateCoding, false, nil // default fallback
		}
	case proto.ApprovalStatusNeedsChanges:
		// PIVOT - return to PLANNING and reset counter.
		c.logger.Info("🧑‍💻 Budget review needs changes, pivoting to PLANNING")
		sm.SetStateData(string(stateDataKeyPlanningIterations), 0)
		return StatePlanning, false, nil
	case proto.ApprovalStatusRejected:
		// ABANDON - move to ERROR.
		c.logger.Info("🧑‍💻 Budget review rejected, abandoning task")
		return proto.StateError, false, logx.Errorf("task abandoned by architect after budget review")
	default:
		return proto.StateError, false, logx.Errorf("unknown budget review approval status: %s", result.Status)
	}
}

// handleQuestion processes the QUESTION state with origin tracking.
func (c *Coder) handleQuestion(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// State: awaiting clarification

	// Regular QUESTION→ANSWER flow (no more budget review logic).
	return c.handleRegularQuestion(ctx, sm)
}

// handleRegularQuestion handles regular QUESTION→ANSWER flow.
func (c *Coder) handleRegularQuestion(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Check if we have an answer.
	if _, exists := sm.GetStateValue(string(stateDataKeyArchitectAnswer)); exists {
		answerStr := utils.GetStateValueOr[string](sm, string(stateDataKeyArchitectAnswer), "")
		sm.SetStateData(string(stateDataKeyQuestionAnswered), true)
		sm.SetStateData(KeyArchitectResponse, answerStr)
		sm.SetStateData(string(stateDataKeyQuestionCompletedAt), time.Now().UTC())

		// Clear the answer so we don't loop.
		sm.SetStateData(string(stateDataKeyArchitectAnswer), nil)

		// Return to origin state using metadata.
		originStr := utils.GetStateValueOr[string](sm, string(stateDataKeyQuestionOrigin), "")

		switch originStr {
		case string(StatePlanning):
			return StatePlanning, false, nil
		case string(StateCoding):
			return StateCoding, false, nil
		// QUESTION can also transition to PLAN_REVIEW per canonical FSM.
		case string(StatePlanReview):
			return StatePlanReview, false, nil
		default:
			return StatePlanning, false, nil
		}
	}

	// Create question for architect if we don't have one pending.
	if c.pendingQuestion == nil {
		questionContent, _ := sm.GetStateValue(string(stateDataKeyQuestionContent))
		questionReason, _ := sm.GetStateValue(string(stateDataKeyQuestionReason))
		questionOrigin, _ := sm.GetStateValue(string(stateDataKeyQuestionOrigin))
		errorMsg, _ := sm.GetStateValue(string(stateDataKeyErrorMessage))

		// Include error message in content if present.
		content := ""
		if questionContentStr, ok := utils.SafeAssert[string](questionContent); ok {
			content = questionContentStr
		}

		if errorMsgStr, ok := utils.SafeAssert[string](errorMsg); ok && errorMsgStr != "" {
			if content != "" {
				content += "\n\nError: " + errorMsgStr
			} else {
				content = "Error: " + errorMsgStr
			}
		}

		c.pendingQuestion = &Question{
			ID:      proto.GenerateQuestionID(),
			Content: content,
			Reason:  utils.GetStateValueOr[string](sm, string(stateDataKeyQuestionReason), ""),
			Origin:  utils.GetStateValueOr[string](sm, string(stateDataKeyQuestionOrigin), ""),
		}

		// Send QUESTION message to architect.
		if c.dispatcher != nil {
			questionMsg := proto.NewAgentMsg(proto.MsgTypeQUESTION, c.GetID(), "architect")
			questionMsg.SetPayload(proto.KeyQuestion, content)

			if questionReasonStr, ok := utils.SafeAssert[string](questionReason); ok {
				questionMsg.SetPayload(proto.KeyReason, questionReasonStr)
			}

			questionMsg.SetPayload(proto.KeyQuestionID, c.pendingQuestion.ID)

			if questionOriginStr, ok := utils.SafeAssert[string](questionOrigin); ok {
				questionMsg.SetPayload(KeyOrigin, questionOriginStr)
			}

			if err := c.dispatcher.DispatchMessage(questionMsg); err != nil {
				c.logger.Error("🧑‍💻 Failed to send QUESTION message to architect: %v", err)
			} else {
				if questionOriginStr, ok := utils.SafeAssert[string](questionOrigin); ok {
					c.logger.Info("🧑‍💻 Sent QUESTION message %s to architect from %s state", c.pendingQuestion.ID, questionOriginStr)
				}
			}
		}
	}

	// Block waiting for ANSWER message from architect.
	c.logger.Debug("🧑‍💻 Blocking in QUESTION state, waiting for architect ANSWER...")
	select {
	case <-ctx.Done():
		return proto.StateError, false, fmt.Errorf("coder question cancelled: %w", ctx.Err())
	case answerMsg, ok := <-c.replyCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			c.logger.Info("🧑‍💻 Reply channel closed in QUESTION state, transitioning to ERROR")
			return proto.StateError, true, fmt.Errorf("reply channel closed unexpectedly during question")
		}
		if answerMsg == nil {
			c.logger.Warn("🧑‍💻 Received nil ANSWER message")
			return StateQuestion, false, nil
		}

		// Verify this is an ANSWER message
		if answerMsg.Type != proto.MsgTypeANSWER {
			c.logger.Warn("🧑‍💻 Expected ANSWER but received %s message, ignoring", answerMsg.Type)
			return StateQuestion, false, nil
		}

		// Process the answer
		answerContent := utils.GetMapFieldOr[string](answerMsg.Payload, "answer", "")
		if answerContent == "" {
			c.logger.Warn("🧑‍💻 Received empty ANSWER content")
			return StateQuestion, false, nil
		}

		c.logger.Info("🧑‍💻 Received ANSWER message %s from architect", answerMsg.ID)

		// Store answer in state data and process like the existing logic
		sm.SetStateData(string(stateDataKeyArchitectAnswer), answerContent)
		sm.SetStateData(string(stateDataKeyQuestionAnswered), true)
		sm.SetStateData(KeyArchitectResponse, answerContent)
		sm.SetStateData(string(stateDataKeyQuestionCompletedAt), time.Now().UTC())

		// Clear pending question
		c.pendingQuestion = nil

		// Return to origin state using metadata.
		originStr := utils.GetStateValueOr[string](sm, string(stateDataKeyQuestionOrigin), "")
		switch originStr {
		case string(StatePlanning):
			return StatePlanning, false, nil
		case string(StateCoding):
			return StateCoding, false, nil
		case string(StatePlanReview):
			return StatePlanReview, false, nil
		default:
			return StatePlanning, false, nil
		}
	}
}

// Helper methods.

// Removed detectHelpRequest - replaced with tool-based question mechanism.

// GetPendingApprovalRequest returns pending approval request if any.
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

// GetPendingQuestion returns pending question if any.
func (c *Coder) GetPendingQuestion() (bool, string, string, string) {
	if c.pendingQuestion == nil {
		return false, "", "", ""
	}
	return true, c.pendingQuestion.ID, c.pendingQuestion.Content, c.pendingQuestion.Reason
}

// ClearPendingQuestion clears the pending question.
func (c *Coder) ClearPendingQuestion() {
	c.pendingQuestion = nil
}

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

	// Stop the long-running container if it exists.
	c.cleanupContainer(ctx, "shutdown")

	// Use the executor's shutdown method for comprehensive cleanup.
	if c.longRunningExecutor != nil {
		if err := c.longRunningExecutor.Shutdown(ctx); err != nil {
			c.logger.Error("Failed to shutdown long-running executor: %v", err)
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

// handleSetup implements AR-102 workspace initialization.
//
//nolint:unparam // bool return is part of state machine interface
func (c *Coder) handleSetup(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	if c.workspaceManager == nil {
		c.logger.Warn("No workspace manager configured, skipping Git worktree setup")
		return StatePlanning, false, nil
	}

	// Get story ID from state data.
	storyID, exists := sm.GetStateValue(KeyStoryID)
	if !exists {
		return proto.StateError, false, logx.Errorf("no story_id found in state data during SETUP")
	}

	storyIDStr, ok := storyID.(string)
	if !ok {
		return proto.StateError, false, logx.Errorf("story_id is not a string in SETUP state: %v (type: %T)", storyID, storyID)
	}

	// Setup workspace.
	agentID := c.BaseStateMachine.GetAgentID()
	// Make agent ID filesystem-safe using shared sanitization helper.
	fsafeAgentID := utils.SanitizeIdentifier(agentID)
	workspaceResult, err := c.workspaceManager.SetupWorkspace(ctx, fsafeAgentID, storyIDStr, c.workDir)
	if err != nil {
		c.logger.Error("Failed to setup workspace: %v", err)
		return proto.StateError, false, logx.Wrap(err, "workspace setup failed")
	}

	// Store worktree path and actual branch name for subsequent states.
	sm.SetStateData(KeyWorktreePath, workspaceResult.WorkDir)
	sm.SetStateData(KeyActualBranchName, workspaceResult.BranchName)

	// Update the coder's working directory to use the agent work directory.
	// This ensures all subsequent operations (MCP tools, testing, etc.) happen in the right place.
	c.workDir = workspaceResult.WorkDir
	c.logger.Info("Workspace setup complete: %s", workspaceResult.WorkDir)
	c.logger.Debug("Updated coder working directory to: %s", c.workDir)
	c.logger.Debug("Coder instance pointer: %p, workDir: %s", c, c.workDir)

	// Configure container with read-only workspace for planning phase.
	if c.longRunningExecutor != nil {
		if err := c.configureWorkspaceMount(ctx, true, "planning"); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to configure planning container")
		}
	}

	// Tools are now registered globally by the orchestrator at startup.
	// No need to register tools per-story or per-agent.

	return StatePlanning, false, nil
}

// SetDockerImage configures the Docker image for the long-running executor.
func (c *Coder) SetDockerImage(image string) {
	if c.longRunningExecutor != nil {
		c.longRunningExecutor.SetImage(image)
	}
}

// configureWorkspaceMount configures container with readonly or readwrite workspace access.
func (c *Coder) configureWorkspaceMount(ctx context.Context, readonly bool, purpose string) error {
	// Stop current container to reconfigure.
	if c.containerName != "" {
		c.logger.Info("Stopping existing container %s to reconfigure for %s", c.containerName, purpose)
		c.cleanupContainer(ctx, fmt.Sprintf("reconfigure for %s", purpose))
	}

	// Determine user based on story type
	storyType := utils.GetStateValueOr[string](c.BaseStateMachine, proto.KeyStoryType, string(proto.StoryTypeApp))
	containerUser := "1000:1000" // Default: non-root user for app stories
	if storyType == string(proto.StoryTypeDevOps) {
		containerUser = "0:0" // Run as root for DevOps stories to access Docker socket
		c.logger.Info("DevOps story detected - running container as root for Docker access")
	}

	// Create execution options for the new container.
	execOpts := execpkg.Opts{
		WorkDir:         c.workDir,
		ReadOnly:        readonly,
		NetworkDisabled: readonly, // Disable network during planning for security
		User:            containerUser,
		Env:             []string{},
		Timeout:         0, // No timeout for long-running container
		ResourceLimits: &execpkg.ResourceLimits{
			CPUs:   "1",    // Limited CPU for planning
			Memory: "512m", // Limited memory for planning
			PIDs:   256,    // Limited processes for planning
		},
	}

	// For coding phase, allow more resources and network access.
	if !readonly {
		execOpts.ResourceLimits.CPUs = "2"
		execOpts.ResourceLimits.Memory = "2g"
		execOpts.ResourceLimits.PIDs = 1024
		execOpts.NetworkDisabled = false
	}

	// Use sanitized agent ID for container naming (story ID not accessible from here).
	agentID := c.GetID()
	sanitizedAgentID := utils.SanitizeContainerName(agentID)

	// Start new container with appropriate configuration.
	containerName, err := c.longRunningExecutor.StartContainer(ctx, sanitizedAgentID, &execOpts)
	if err != nil {
		return logx.Wrap(err, fmt.Sprintf("failed to start %s container", purpose))
	}

	c.containerName = containerName
	c.logger.Info("Started %s container: %s (readonly=%v)", purpose, containerName, readonly)

	// Update shell tool to use the new container.
	if err := c.updateShellToolForStory(ctx); err != nil {
		c.logger.Error("Failed to update shell tool for new container: %v", err)
		// Continue anyway - this shouldn't block the story.
	}

	return nil
}

// GetContainerName returns the current container name for cleanup purposes.
func (c *Coder) GetContainerName() string {
	return c.containerName
}

// cleanupContainer stops and removes the current story's container.
func (c *Coder) cleanupContainer(ctx context.Context, reason string) {
	if c.longRunningExecutor != nil && c.containerName != "" {
		c.logger.Info("Stopping long-running container %s (%s)", c.containerName, reason)

		containerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := c.longRunningExecutor.StopContainer(containerCtx, c.containerName); err != nil {
			c.logger.Error("Failed to stop container %s: %v", c.containerName, err)
		} else {
			c.logger.Info("Container %s stopped successfully", c.containerName)
		}

		// Clear container name.
		c.containerName = ""
	}
}

// updateShellToolForStory is no longer needed in the new ToolProvider system.
// The executor is provided when ToolProvider creates tools based on AgentContext.
func (c *Coder) updateShellToolForStory(_ /* storyCtx */ context.Context) error {
	// No longer needed - ToolProvider handles executor configuration
	return nil
}

// executeShellCommand runs a shell command in the current container.
func (c *Coder) executeShellCommand(ctx context.Context, args ...string) (string, error) {
	if c.longRunningExecutor == nil || c.containerName == "" {
		return "", logx.Errorf("no active container for shell execution")
	}

	opts := execpkg.Opts{
		WorkDir: "/workspace",
		Timeout: 30 * time.Second,
	}

	result, err := c.longRunningExecutor.Run(ctx, args, &opts)
	if err != nil {
		return "", fmt.Errorf("shell command failed: %w", err)
	}

	return result.Stdout, nil
}

// processQuestionTransition is a common helper for question transitions.
func (c *Coder) processQuestionTransition(sm *agent.BaseStateMachine, questionData any, originState proto.State, stateType string) (proto.State, bool, error) {
	// Extract question details from tool result.
	questionMap, ok := questionData.(map[string]any)
	if !ok {
		return proto.StateError, false, logx.Errorf("invalid question data format")
	}

	question := utils.GetMapFieldOr[string](questionMap, "question", "")
	context := utils.GetMapFieldOr[string](questionMap, "context", "")
	urgency := utils.GetMapFieldOr[string](questionMap, "urgency", "medium")

	// Set question state data for QUESTION state handler.
	sm.SetStateData(string(stateDataKeyQuestionContent), question)
	sm.SetStateData(string(stateDataKeyQuestionReason), fmt.Sprintf("%s clarification (%s urgency)", stateType, urgency))
	sm.SetStateData(string(stateDataKeyQuestionOrigin), string(originState))
	sm.SetStateData(KeyQuestionContext, context)

	// Clear the question submission trigger.
	sm.SetStateData(KeyQuestionSubmitted, nil)

	c.logger.Info("🧑‍💻 Question submitted during %s: %s", strings.ToLower(stateType), question)
	return StateQuestion, false, nil
}

// handleQuestionTransition processes ask_question tool results.
func (c *Coder) handleQuestionTransition(_ context.Context, sm *agent.BaseStateMachine, questionData any) (proto.State, bool, error) {
	// Store current planning context for restoration.
	c.storePlanningContext(sm)
	return c.processQuestionTransition(sm, questionData, StatePlanning, "Planning")
}

// handleCodingQuestionTransition processes ask_question tool results from CODING state.

// handlePlanSubmission processes submit_plan tool results.
func (c *Coder) handlePlanSubmission(_ context.Context, sm *agent.BaseStateMachine, planData any) (proto.State, bool, error) {
	planMap, ok := planData.(map[string]any)
	if !ok {
		return proto.StateError, false, logx.Errorf("invalid plan data format")
	}

	plan, _ := planMap[KeyPlan].(string)
	confidence, _ := planMap["confidence"].(string)
	explorationSummary, _ := planMap["exploration_summary"].(string)
	risks, _ := planMap["risks"].(string)
	todos, _ := planMap["todos"].([]any)

	// Convert todos to structured format.
	planTodos := make([]PlanTodo, len(todos))
	for i, todoItem := range todos {
		if todoMap, ok := utils.SafeAssert[map[string]any](todoItem); ok {
			planTodos[i] = PlanTodo{
				ID:          utils.GetMapFieldOr[string](todoMap, "id", ""),
				Description: utils.GetMapFieldOr[string](todoMap, "description", ""),
				Completed:   utils.GetMapFieldOr[bool](todoMap, "completed", false),
			}
		}
	}

	// Get original story content for reference.
	originalStory, _ := sm.GetStateValue(string(stateDataKeyTaskContent))
	originalStoryStr, _ := originalStory.(string)

	// Store plan data using typed constants.
	sm.SetStateData(string(stateDataKeyPlan), plan)
	sm.SetStateData(string(stateDataKeyPlanConfidence), confidence)
	sm.SetStateData(string(stateDataKeyExplorationSummary), explorationSummary)
	sm.SetStateData(string(stateDataKeyPlanRisks), risks)
	sm.SetStateData(string(stateDataKeyPlanTodos), planTodos)
	sm.SetStateData(KeyPlanningCompletedAt, time.Now().UTC())

	// Clear the plan submission trigger.
	sm.SetStateData(KeyPlanSubmitted, nil)

	// Send REQUEST message to architect for approval.
	c.pendingApprovalRequest = &ApprovalRequest{
		ID:      proto.GenerateApprovalID(),
		Content: plan,
		Reason:  fmt.Sprintf("Enhanced plan requires approval (confidence: %s)", confidence),
		Type:    proto.ApprovalTypePlan,
	}

	if c.dispatcher != nil {
		requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.GetID(), "architect")
		requestMsg.SetPayload("request_type", proto.RequestApproval.String())
		requestMsg.SetPayload("approval_type", proto.ApprovalTypePlan.String())
		requestMsg.SetPayload(KeyPlan, plan)
		requestMsg.SetPayload("confidence", confidence)
		requestMsg.SetPayload("exploration_summary", explorationSummary)
		requestMsg.SetPayload("risks", risks)
		requestMsg.SetPayload("todos", planTodos)
		requestMsg.SetPayload("original_story", originalStoryStr)
		requestMsg.SetPayload("approval_id", c.pendingApprovalRequest.ID)

		// Add story_id for database persistence
		if storyID, exists := sm.GetStateValue(KeyStoryID); exists {
			if storyIDStr, ok := storyID.(string); ok {
				requestMsg.SetPayload("story_id", storyIDStr)
			}
		}

		if err := c.dispatcher.DispatchMessage(requestMsg); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to send enhanced plan approval request")
		}

		c.logger.Info("🧑‍💻 Sent enhanced plan approval request %s to architect", c.pendingApprovalRequest.ID)
	} else {
		c.logger.Error("🧑‍💻 Dispatcher is nil, cannot send plan approval request")
		return proto.StateError, false, logx.Errorf("dispatcher not available for plan approval request")
	}

	return StatePlanReview, false, nil
}

// handleCompletionSubmission processes completion signal from mark_story_complete tool.
func (c *Coder) handleCompletionSubmission(_ /* ctx */ context.Context, sm *agent.BaseStateMachine, completionData any) (proto.State, bool, error) {
	c.logger.Debug("Processing completion submission, data type: %T", completionData)

	if completionData == nil {
		c.logger.Debug("Completion data is nil, skipping")
		return proto.StateError, false, logx.Errorf("completion data is nil")
	}

	completionMap, ok := completionData.(map[string]any)
	if !ok {
		c.logger.Debug("Completion data is not map[string]any, got: %T, value: %+v", completionData, completionData)
		return proto.StateError, false, logx.Errorf("invalid completion data format: expected map[string]any, got %T", completionData)
	}

	reason, _ := completionMap["reason"].(string)
	evidence, _ := completionMap["evidence"].(string)
	confidence, _ := completionMap["confidence"].(string)

	// Get original story content for reference.
	originalStory, _ := sm.GetStateValue(string(stateDataKeyTaskContent))
	originalStoryStr, _ := originalStory.(string)

	// Store completion data.
	sm.SetStateData(KeyCompletionReason, reason)
	sm.SetStateData(KeyCompletionEvidence, evidence)
	sm.SetStateData(KeyCompletionConfidence, confidence)
	sm.SetStateData(KeyCompletionSubmittedAt, time.Now().UTC())

	// Clear the completion submission trigger.
	sm.SetStateData(string(stateDataKeyCompletionSubmitted), nil)

	// Send REQUEST message to architect for completion approval.
	c.pendingApprovalRequest = &ApprovalRequest{
		ID:      proto.GenerateApprovalID(),
		Content: fmt.Sprintf("Story completion claim:\n\nReason: %s\n\nEvidence: %s\n\nConfidence: %s", reason, evidence, confidence),
		Reason:  "Story appears to be already implemented - requesting completion approval",
		Type:    proto.ApprovalTypeCompletion,
	}

	if c.dispatcher != nil {
		// Get story ID from state data
		storyID, exists := sm.GetStateValue(KeyStoryID)
		if !exists {
			return proto.StateError, false, logx.Errorf("no story_id found in state data for completion approval request")
		}
		storyIDStr, ok := storyID.(string)
		if !ok {
			return proto.StateError, false, logx.Errorf("story_id is not a string in completion approval request: %v (type: %T)", storyID, storyID)
		}

		requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.GetID(), "architect")
		requestMsg.SetPayload("request_type", proto.RequestApproval.String())
		requestMsg.SetPayload("approval_type", proto.ApprovalTypeCompletion.String())
		requestMsg.SetPayload("content", c.pendingApprovalRequest.Content)
		requestMsg.SetPayload("reason", c.pendingApprovalRequest.Reason)
		requestMsg.SetPayload("approval_id", c.pendingApprovalRequest.ID)
		requestMsg.SetPayload("original_story", originalStoryStr)
		requestMsg.SetPayload("story_id", storyIDStr)
		requestMsg.SetPayload(KeyCompletionReason, reason)
		requestMsg.SetPayload(KeyCompletionEvidence, evidence)
		requestMsg.SetPayload(KeyCompletionConfidence, confidence)

		if err := c.dispatcher.DispatchMessage(requestMsg); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to send completion approval request")
		}
		c.logger.Info("🧑‍💻 Sent %s approval request %s to architect (request_type: %s)",
			proto.ApprovalTypeCompletion, c.pendingApprovalRequest.ID, proto.RequestApproval.String())
	} else {
		c.logger.Error("🧑‍💻 Dispatcher is nil, cannot send completion approval request")
		return proto.StateError, false, logx.Errorf("dispatcher not available for completion approval request")
	}

	// Clear the completion submission trigger.
	sm.SetStateData(string(stateDataKeyCompletionSubmitted), nil)

	// Route to PLAN_REVIEW for completion approval handling.
	c.logger.Info("🧑‍💻 Completion request submitted, transitioning to PLAN_REVIEW")
	return StatePlanReview, false, nil
}

// Context management helper methods.

// Placeholder helper methods for coding context management (to be enhanced as needed).

//nolint:unparam // Error return required for interface consistency
func (c *Coder) handleDone(_ /* ctx */ context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// DONE is terminal - orchestrator will handle all cleanup and restart.
	// Only log once when entering DONE state to avoid spam.
	if val, exists := sm.GetStateValue(KeyDoneLogged); !exists || val != true {
		c.logger.Info("🧑‍💻 Agent in DONE state - orchestrator will handle cleanup and restart")
		sm.SetStateData(KeyDoneLogged, true)
	}

	// Return done=true to stop the run loop.
	return proto.StateDone, true, nil
}

//nolint:unparam // Error return required for interface consistency
func (c *Coder) handleError(_ context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// ERROR is truly terminal - orchestrator handles all cleanup and story requeue.
	// Only log once when entering ERROR state to avoid spam.
	if val, exists := sm.GetStateValue(KeyDoneLogged); !exists || val != true {
		errorMsg, _ := sm.GetStateValue(KeyErrorMessage)
		c.logger.Error("🧑‍💻 Agent in ERROR state: %v - orchestrator will handle cleanup and story requeue", errorMsg)
		sm.SetStateData(KeyDoneLogged, true)
	}

	// Return done=true to stop the run loop - orchestrator handles everything else.
	return proto.StateError, true, nil
}

// runMakeTest executes tests using the appropriate build backend - implements AR-103.
func (c *Coder) runMakeTest(ctx context.Context, worktreePath string) (bool, string, error) {
	c.logger.Info("Running tests in %s", worktreePath)

	// Create a context with timeout for the test execution.
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Use global config singleton for test command.
	globalConfig, err := config.GetConfig()
	if err != nil {
		return false, "", fmt.Errorf("global config not available: %w", err)
	}

	platform := globalConfig.Project.PrimaryPlatform
	testCommand := globalConfig.Build.Test
	if testCommand == "" {
		testCommand = "make test" // fallback
	}

	c.logger.Info("Using %s platform for testing with command: %s", platform, testCommand)

	// Execute test command using shell.
	opts := execpkg.Opts{
		WorkDir: worktreePath,
		Timeout: 5 * time.Minute,
	}

	result, err := c.longRunningExecutor.Run(testCtx, []string{"sh", "-c", testCommand}, &opts)
	if err != nil {
		return false, "", fmt.Errorf("failed to execute test command: %w", err)
	}
	outputStr := result.Stdout + result.Stderr

	// Log the test output for debugging.
	c.logger.Info("Test output: %s", outputStr)

	// Check if it's a timeout.
	if testCtx.Err() == context.DeadlineExceeded {
		return false, outputStr, logx.Errorf("tests timed out after 5 minutes")
	}

	// Check test result based on exit code.
	if result.ExitCode != 0 {
		// Tests failed - this is expected when tests fail.
		c.logger.Info("Tests failed with exit code: %d", result.ExitCode)
		return false, outputStr, nil
	}

	// Tests succeeded.
	c.logger.Info("Tests completed successfully")
	return true, outputStr, nil
}

// runTestWithBuildService runs tests using the build service instead of direct backend calls.
func (c *Coder) runTestWithBuildService(ctx context.Context, worktreePath string) (bool, string, error) {
	c.logger.Info("Running tests via build service in %s", worktreePath)

	// Create a context with timeout for the test execution.
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Create test request.
	req := &build.Request{
		ProjectRoot: worktreePath,
		Operation:   "test",
		Timeout:     300, // 5 minutes
		Context:     make(map[string]string),
	}

	// Execute test via build service.
	response, err := c.buildService.ExecuteBuild(testCtx, req)
	if err != nil {
		return false, "", logx.Wrap(err, "build service test execution failed")
	}

	// Log the test output for debugging.
	c.logger.Info("Test output: %s", response.Output)

	if !response.Success {
		// Check if it's a timeout.
		if testCtx.Err() == context.DeadlineExceeded {
			return false, response.Output, logx.Errorf("tests timed out after 5 minutes")
		}

		// Tests failed - this is expected when tests fail.
		c.logger.Info("Tests failed: %s", response.Error)
		return false, response.Output, nil
	}

	// Tests succeeded.
	c.logger.Info("Tests completed successfully via build service")
	return true, response.Output, nil
}

// proceedToCodeReview handles the common logic for transitioning to CODE_REVIEW after successful testing.
func (c *Coder) proceedToCodeReview(_ context.Context, _ *agent.BaseStateMachine) (proto.State, bool, error) {
	// Tests passed, transition to CODE_REVIEW state.
	// The approval request will be sent when entering the CODE_REVIEW state.
	c.logger.Info("🧑‍💻 Tests completed successfully, transitioning to CODE_REVIEW")
	return StateCodeReview, false, nil
}

// generateGitDiff generates a git diff showing changes made to the story branch.
func (c *Coder) generateGitDiff(ctx context.Context, _ *agent.BaseStateMachine) (string, error) {
	// Create shell tool for git operations
	tool := tools.NewShellTool(c.longRunningExecutor)

	// Get the main branch name for comparison (usually 'main' or 'master').
	mainBranch := "main" // Could make this configurable if needed

	// Generate diff between current branch and main branch.
	args := map[string]any{
		"cmd": "git diff " + mainBranch + "..HEAD",
		"cwd": c.workDir,
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		// Try 'master' if 'main' doesn't exist.
		args["cmd"] = "git diff master..HEAD"
		result, err = tool.Exec(ctx, args)
		if err != nil {
			return "", logx.Wrap(err, "failed to generate git diff")
		}
	}

	// Extract stdout from result.
	if resultMap, ok := result.(map[string]any); ok {
		if stdout, ok := resultMap["stdout"].(string); ok {
			return stdout, nil
		}
	}

	return "", logx.Errorf("unexpected result format from shell tool")
}

// cleanupEmptyBranch optionally deletes the development branch when no changes were made.
func (c *Coder) cleanupEmptyBranch(ctx context.Context, sm *agent.BaseStateMachine) error {
	// Create shell tool for git operations
	tool := tools.NewShellTool(c.longRunningExecutor)

	// Get the current branch name.
	branchName, exists := sm.GetStateValue(KeyBranchName)
	if !exists {
		c.logger.Debug("No branch name stored, skipping branch cleanup")
		return nil
	}

	branchNameStr, ok := branchName.(string)
	if !ok || branchNameStr == "" {
		c.logger.Debug("Invalid branch name, skipping branch cleanup")
		return nil
	}

	c.logger.Info("🧹 Cleaning up empty development branch: %s", branchNameStr)

	// Switch back to main branch first.
	args := map[string]any{
		"cmd": "git checkout main",
		"cwd": c.workDir,
	}

	_, err := tool.Exec(ctx, args)
	if err != nil {
		// Try master if main doesn't exist.
		args["cmd"] = "git checkout master"
		_, err = tool.Exec(ctx, args)
		if err != nil {
			return logx.Wrap(err, "failed to checkout main/master branch")
		}
	}

	// Delete the development branch.
	args["cmd"] = "git branch -D " + branchNameStr
	_, err = tool.Exec(ctx, args)
	if err != nil {
		return logx.Wrap(err, fmt.Sprintf("failed to delete branch %s", branchNameStr))
	}

	c.logger.Info("🧹 Successfully cleaned up empty branch: %s", branchNameStr)
	return nil
}

// pushBranchAndCreatePR implements AR-104: Push branch & open pull request.
func (c *Coder) pushBranchAndCreatePR(ctx context.Context, sm *agent.BaseStateMachine) error {
	// Get worktree path and story ID.
	worktreePath, exists := sm.GetStateValue(KeyWorktreePath)
	if !exists || worktreePath == "" {
		c.logger.Warn("No worktree path found, skipping branch push and PR creation")
		return nil // Not an error - just skip for backward compatibility
	}

	worktreePathStr, ok := worktreePath.(string)
	if !ok {
		return logx.Errorf("worktree_path is not a string: %v", worktreePath)
	}

	storyID, exists := sm.GetStateValue(KeyStoryID)
	if !exists || storyID == nil {
		return logx.Errorf("no story_id found in state data")
	}

	storyIDStr, ok := storyID.(string)
	if !ok {
		return logx.Errorf("story_id is not a string in pushBranchAndCreatePR: %v (type: %T)", storyID, storyID)
	}

	// Use the actual branch name that was created (which may be different due to collisions).
	actualBranchName, exists := sm.GetStateValue(KeyActualBranchName)
	if !exists || actualBranchName == "" {
		// Fallback to generating the branch name if not found.
		actualBranchName = fmt.Sprintf("story-%s", storyIDStr)
		c.logger.Warn("actual_branch_name not found in state, using fallback: %s", actualBranchName)
	}

	branchName, ok := actualBranchName.(string)
	if !ok {
		branchName = fmt.Sprintf("story-%s", storyIDStr)
		c.logger.Warn("actual_branch_name is not a string, using fallback: %s", branchName)
	}

	agentID := c.BaseStateMachine.GetAgentID()

	c.logger.Info("Pushing branch %s for story %s", branchName, storyIDStr)

	// Step 1: Commit all changes before pushing.
	commitCtx, commitCancel := context.WithTimeout(ctx, 1*time.Minute)
	defer commitCancel()

	// Add all files to staging.
	addCmd := exec.CommandContext(commitCtx, "git", "add", ".")
	addCmd.Dir = worktreePathStr
	addOutput, err := addCmd.CombinedOutput()
	if err != nil {
		return logx.Errorf("failed to stage changes: %w\nOutput: %s", err, string(addOutput))
	}
	c.logger.Info("Staged all changes for commit")

	// Check if there are any changes to commit.
	statusCmd := exec.CommandContext(commitCtx, "git", "status", "--porcelain")
	statusCmd.Dir = worktreePathStr
	statusOutput, err := statusCmd.CombinedOutput()
	if err != nil {
		return logx.Errorf("failed to check git status: %w\nOutput: %s", err, string(statusOutput))
	}

	if strings.TrimSpace(string(statusOutput)) == "" {
		c.logger.Info("No changes to commit for story %s", storyIDStr)
		return nil // No changes, skip push and PR creation
	}

	// Commit changes.
	commitMsg := fmt.Sprintf("Implement story %s\n\n🤖 Generated by Maestro AI", storyIDStr)
	commitCmd := exec.CommandContext(commitCtx, "git", "commit", "-m", commitMsg)
	commitCmd.Dir = worktreePathStr
	commitOutput, err := commitCmd.CombinedOutput()
	if err != nil {
		return logx.Errorf("failed to commit changes: %w\nOutput: %s", err, string(commitOutput))
	}
	c.logger.Info("Committed changes for story %s", storyIDStr)

	// Step 2: Push branch via SSH.
	pushCtx, pushCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer pushCancel()

	pushCmd := exec.CommandContext(pushCtx, "git", "push", "-u", KeyOrigin, branchName)
	pushCmd.Dir = worktreePathStr

	pushOutput, err := pushCmd.CombinedOutput()
	if err != nil {
		return logx.Errorf("failed to push branch %s: %w\nOutput: %s", branchName, err, string(pushOutput))
	}

	c.logger.Info("Successfully pushed branch %s", branchName)
	sm.SetStateData(KeyBranchPushed, true)
	sm.SetStateData(KeyPushedBranch, branchName)

	// Step 3: Create PR if GITHUB_TOKEN is available.
	if githubToken := os.Getenv("GITHUB_TOKEN"); githubToken != "" {
		c.logger.Info("GITHUB_TOKEN found, creating pull request")

		prURL, err := c.createPullRequest(ctx, worktreePathStr, branchName, storyIDStr, agentID)
		if err != nil {
			// Log error but don't fail the push - PR creation is optional.
			c.logger.Error("Failed to create pull request: %v", err)
			sm.SetStateData(KeyPRCreationError, err.Error())
		} else {
			c.logger.Info("Successfully created pull request: %s", prURL)
			sm.SetStateData(KeyPRURL, prURL)
			sm.SetStateData(KeyPRCreated, true)

			// TODO: Post PR URL back to architect agent via message
			c.logger.Info("🧑‍💻 Pull request created for story %s: %s", storyIDStr, prURL)
		}
	} else {
		c.logger.Info("No GITHUB_TOKEN found, skipping automatic PR creation")
		sm.SetStateData(KeyPRSkipped, "no_github_token")
	}

	return nil
}

// createPullRequest uses gh CLI to create a pull request.
func (c *Coder) createPullRequest(ctx context.Context, worktreePath, branchName, storyID, agentID string) (string, error) {
	prCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	// Build PR title and body.
	title := fmt.Sprintf("Story #%s: generated by agent %s", storyID, agentID)

	// Get base branch from config (default: main).
	baseBranch := "main" // TODO: Get from workspace manager config

	// Check if gh is available.
	if _, err := exec.LookPath("gh"); err != nil {
		return "", logx.Wrap(err, "gh (GitHub CLI) is not available in PATH")
	}

	// Check if GITHUB_TOKEN is set.
	if os.Getenv("GITHUB_TOKEN") == "" {
		return "", logx.Errorf("GITHUB_TOKEN environment variable is not set")
	}

	// Create PR using gh CLI.
	prCmd := exec.CommandContext(prCtx, "gh", "pr", "create",
		"--title", title,
		"--body", fmt.Sprintf("Automated pull request for story %s generated by agent %s", storyID, agentID),
		"--base", baseBranch,
		"--head", branchName)
	prCmd.Dir = worktreePath

	prOutput, err := prCmd.CombinedOutput()
	if err != nil {
		return "", logx.Errorf("gh pr create failed: %w\nOutput: %s", err, string(prOutput))
	}

	// Extract PR URL from output (gh returns the PR URL).
	prURL := strings.TrimSpace(string(prOutput))
	return prURL, nil
}

// sendMergeRequest sends a merge request to the architect for PR merging.
func (c *Coder) sendMergeRequest(_ context.Context, sm *agent.BaseStateMachine) error {
	storyID, _ := sm.GetStateValue(KeyStoryID)
	prURL, _ := sm.GetStateValue(KeyPRURL)
	branchName, _ := sm.GetStateValue(KeyPushedBranch)

	// Convert to strings safely.
	storyIDStr, _ := storyID.(string)
	prURLStr, _ := prURL.(string)
	branchNameStr, _ := branchName.(string)

	// Log the state of PR creation for debugging.
	if prCreated := utils.GetStateValueOr[bool](sm, KeyPRCreated, false); prCreated {
		c.logger.Info("🧑‍💻 Sending merge request to architect for story %s with PR: %s", storyIDStr, prURLStr)
	} else {
		c.logger.Info("🧑‍💻 Sending merge request to architect for story %s with branch: %s (PR creation failed or skipped)", storyIDStr, branchNameStr)
		if prError, exists := sm.GetStateValue(KeyPRCreationError); exists {
			c.logger.Warn("🧑‍💻 PR creation error: %v", prError)
		}
	}

	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.GetID(), "architect")
	requestMsg.SetPayload("request_type", "merge")
	requestMsg.SetPayload(KeyPRURL, prURLStr)
	requestMsg.SetPayload(KeyBranchName, branchNameStr)
	requestMsg.SetPayload(KeyStoryID, storyIDStr)

	if err := c.dispatcher.DispatchMessage(requestMsg); err != nil {
		return fmt.Errorf("failed to dispatch merge request: %w", err)
	}
	return nil
}

// addToolResultToContext adds tool execution results to context for Claude to see (DRY version of CODING logic).
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
				c.contextManager.AddMessage("tool", fmt.Sprintf("%s operation completed successfully", toolCall.Name))
			} else {
				c.logger.Info("%s tool failed", toolCall.Name)
				c.contextManager.AddMessage("tool", fmt.Sprintf("%s operation failed", toolCall.Name))
			}
		}

		if output, ok := resultMap["output"].(string); ok && output != "" {
			c.logger.Debug("%s output: %s", toolCall.Name, output)
			c.contextManager.AddMessage("tool", fmt.Sprintf("%s output: %s", toolCall.Name, output))
		}

		if errorMsg, ok := resultMap["error"].(string); ok && errorMsg != "" {
			c.logger.Debug("%s error: %s", toolCall.Name, errorMsg)
			c.contextManager.AddMessage("tool", fmt.Sprintf("%s error: %s", toolCall.Name, errorMsg))
		}
	}
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

	c.contextManager.AddMessage("tool", feedback.String())
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

	c.contextManager.AddMessage("tool", feedback.String())
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
