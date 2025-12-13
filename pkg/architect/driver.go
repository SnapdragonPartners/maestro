// Package architect provides the architect agent implementation for the orchestrator system.
// The architect processes specifications, generates stories, and coordinates with coder agents.
package architect

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/workspace"
)

// Tool signal constants - all signals now use centralized constants from tools package.
// See tools.Signal* constants for the complete list.

// KnowledgeEntry represents a knowledge graph entry to be persisted.
//
//nolint:govet // fieldalignment: logical grouping preferred over memory optimization
type KnowledgeEntry struct {
	AgentID   string    // Agent that recorded this knowledge
	StoryID   string    // Story context where this was recorded
	Category  string    // Knowledge category (architecture, convention, etc.)
	Title     string    // Brief title for the entry
	Content   string    // The knowledge content
	Rationale string    // Why this is important or why decision was made
	Scope     string    // Applicability: story, spec, project
	Timestamp time.Time // When this was recorded
}

// MaintenanceTracker tracks maintenance cycle state.
//
//nolint:govet // Simple tracking struct
type MaintenanceTracker struct {
	mutex            sync.Mutex
	SpecsCompleted   int       // Number of specs completed since last maintenance
	LastMaintenance  time.Time // When last maintenance cycle ran
	InProgress       bool      // Whether a maintenance cycle is currently running
	CurrentCycleID   string    // ID of current maintenance cycle (if in progress)
	CompletedSpecIDs []string  // IDs of specs that triggered the counter
	CycleStartedAt   time.Time // When current cycle started

	// Per-story tracking within current maintenance cycle
	StoryResults       map[string]*MaintenanceStoryResult // Story ID -> result
	ProgrammaticReport *ProgrammaticReport                // Results from programmatic tasks (branch cleanup)
	Metrics            MaintenanceMetrics                 // Aggregated metrics for the cycle
}

// MaintenanceStoryResult tracks the result of a single maintenance story.
//
//nolint:govet // Field order for readability
type MaintenanceStoryResult struct {
	StoryID     string
	Title       string
	Status      string    // pending, in_progress, completed, failed
	PRNumber    int       // PR number if created
	PRMerged    bool      // Whether PR was merged
	CompletedAt time.Time // When story completed
	Summary     string    // Brief summary of what was done
}

// MaintenanceMetrics aggregates metrics across a maintenance cycle.
type MaintenanceMetrics struct {
	StoriesTotal     int
	StoriesCompleted int
	StoriesFailed    int
	PRsMerged        int
	BranchesDeleted  int
}

// Driver manages the state machine for an architect workflow.
//
//nolint:govet // fieldalignment: logical grouping preferred over memory optimization
type Driver struct {
	*agent.BaseStateMachine                                       // Embed state machine (provides LLMClient field)
	agentContexts           map[string]*contextmgr.ContextManager // Per-agent contexts (key: agent_id)
	contextMutex            sync.RWMutex                          // Protect agentContexts map
	knowledgeBuffer         []KnowledgeEntry                      // Accumulated knowledge entries for persistence
	knowledgeMutex          sync.Mutex                            //nolint:unused // Protect knowledgeBuffer (remove nolint when knowledge recording is implemented)
	maintenance             MaintenanceTracker                    // Maintenance cycle state
	toolLoop                *toolloop.ToolLoop                    // Tool loop for LLM interactions
	renderer                *templates.Renderer                   // Template renderer for prompts
	queue                   *Queue                                // Story queue manager
	escalationHandler       *EscalationHandler                    // Escalation handler
	dispatcher              *dispatch.Dispatcher                  // Dispatcher for sending messages
	logger                  *logx.Logger                          // Logger with proper agent prefixing
	executor                *execpkg.ArchitectExecutor            // Container executor for file access tools
	chatService             ChatServiceInterface                  // Chat service for escalations (nil check required)
	questionsCh             chan *proto.AgentMsg                  // Bi-directional channel for requests (specs, questions, approvals)
	replyCh                 <-chan *proto.AgentMsg                // Read-only channel for replies
	persistenceChannel      chan<- *persistence.Request           // Channel for database operations
	workDir                 string                                // Workspace directory
}

// ChatServiceInterface defines the interface for chat operations needed by architect.
// This allows for testing with mocks and keeps the architect loosely coupled from chat implementation.
type ChatServiceInterface interface {
	Post(ctx context.Context, req *ChatPostRequest) (*ChatPostResponse, error)
	WaitForReply(ctx context.Context, messageID int64, pollInterval time.Duration) (*ChatMessage, error)
}

// ChatPostRequest represents a chat post request (simplified for architect use).
type ChatPostRequest struct {
	Author   string
	Text     string
	Channel  string
	ReplyTo  *int64
	PostType string
}

// ChatPostResponse represents a chat post response (simplified for architect use).
type ChatPostResponse struct {
	ID      int64
	Success bool
}

// ChatMessage represents a chat message (simplified for architect use).
type ChatMessage struct {
	Timestamp string
	Author    string
	Text      string
	ID        int64
}

// ErrEscalationTriggered is returned when iteration limits are exceeded and escalation is needed.
var ErrEscalationTriggered = fmt.Errorf("escalation triggered due to iteration limit")

// NewDriver creates a new architect driver instance.
// LLM client must be set separately via SetLLMClient after construction.
func NewDriver(architectID, _ string, dispatcher *dispatch.Dispatcher, workDir string, persistenceChannel chan<- *persistence.Request) *Driver {
	renderer, err := templates.NewRenderer()
	if err != nil {
		// Log the error but continue with nil renderer for graceful degradation.
		fmt.Printf("ERROR: Failed to initialize template renderer: %v\n", err)
	}
	// Create queue with persistence if available, otherwise fail
	var queue *Queue
	if persistenceChannel != nil {
		queue = NewQueue(persistenceChannel)
	} else {
		// Fallback queue without persistence is no longer supported
		panic("persistence channel is required - database storage is mandatory")
	}

	// Ensure logs directory exists
	logsDir := workDir + "/logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		fmt.Printf("WARNING: Failed to create logs directory %s: %v\n", logsDir, err)
	}

	escalationHandler := NewEscalationHandler(logsDir, queue)
	logger := logx.NewLogger(architectID)

	// Create BaseStateMachine with architect transition table
	sm := agent.NewBaseStateMachine(architectID, StateWaiting, nil, architectTransitions)

	return &Driver{
		BaseStateMachine:   sm,
		agentContexts:      make(map[string]*contextmgr.ContextManager), // Initialize context map
		knowledgeBuffer:    make([]KnowledgeEntry, 0),                   // Initialize knowledge buffer
		toolLoop:           nil,                                         // Set via SetLLMClient
		renderer:           renderer,
		workDir:            workDir,
		queue:              queue,
		escalationHandler:  escalationHandler,
		dispatcher:         dispatcher,
		logger:             logger,
		persistenceChannel: persistenceChannel,
		// Channels will be set during Attach()
		questionsCh: nil,
		replyCh:     nil,
	}
}

// NewArchitect creates a new architect with LLM integration.
// Uses shared LLM factory for proper rate limiting across all agents.
func NewArchitect(ctx context.Context, architectID string, dispatcher *dispatch.Dispatcher, workDir string, persistenceChannel chan<- *persistence.Request, llmFactory *agent.LLMClientFactory) (*Driver, error) {
	// Check for context cancellation before starting construction
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("architect construction cancelled: %w", ctx.Err())
	default:
	}

	// Architect constructor with model configuration validation

	// Get model name from config
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	modelName := cfg.Agents.ArchitectModel

	// Create architect without LLM client first (chicken-and-egg: client needs architect as StateProvider)
	architect := NewDriver(architectID, modelName, dispatcher, workDir, persistenceChannel)

	// Ensure architect workspace exists before starting container
	architectWorkspace, wsErr := workspace.EnsureArchitectWorkspace(ctx, workDir)
	if wsErr != nil {
		return nil, fmt.Errorf("failed to ensure architect workspace: %w", wsErr)
	}
	architect.logger.Info("Architect workspace ready at: %s", architectWorkspace)

	// Create and start architect container executor
	// The architect container has read-only mounts to all coder workspaces and architect workspace
	architectExecutor := execpkg.NewArchitectExecutor(
		config.BootstrapContainerTag, // Use bootstrap image (same as coders)
		workDir,                      // Project directory containing coder-NNN directories
		cfg.Agents.MaxCoders,         // Number of coder workspaces to mount
	)

	// Start the architect container (one retry on failure)
	if startErr := architectExecutor.Start(ctx); startErr != nil {
		architect.logger.Warn("Failed to start architect container, retrying once: %v", startErr)
		// Retry once
		if retryErr := architectExecutor.Start(ctx); retryErr != nil {
			return nil, fmt.Errorf("failed to start architect container after retry: %w", retryErr)
		}
	}

	// Store executor in architect
	architect.executor = architectExecutor

	// Now create LLM client with full middleware (architect instance available as StateProvider)
	llmClient, err := llmFactory.CreateClientWithContext(agent.TypeArchitect, architect, architect.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create architect LLM client: %w", err)
	}

	// Set the LLM client (also initializes toolLoop and sets on BaseStateMachine)
	architect.SetLLMClient(llmClient)

	return architect, nil
}

// SetChannels sets the communication channels from the dispatcher.
// All requests (specs, questions, approvals) come through questionsCh now.
// The middle parameter (unusedCh) is unused - it was previously specCh before specs were unified with REQUEST messages.
func (d *Driver) SetChannels(questionsCh, _ chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg) {
	d.questionsCh = questionsCh
	d.replyCh = replyCh
}

// SetDispatcher sets the dispatcher reference (already set in constructor, but required for interface).
func (d *Driver) SetDispatcher(dispatcher *dispatch.Dispatcher) {
	// Architect already has dispatcher from constructor, but update it for consistency.
	d.dispatcher = dispatcher
}

// SetLLMClient sets the LLM client and initializes the toolLoop.
// Must be called after construction before the architect can process work.
func (d *Driver) SetLLMClient(llmClient agent.LLMClient) {
	d.BaseStateMachine.SetLLMClient(llmClient)
	d.toolLoop = toolloop.New(llmClient, d.logger)
}

// getContextForAgent retrieves or creates a context manager for the specified agent.
// This enables per-agent conversation continuity within story boundaries.
// Thread-safe with read-write lock protection.
func (d *Driver) getContextForAgent(agentID string) *contextmgr.ContextManager {
	// Fast path: read lock to check if context exists
	d.contextMutex.RLock()
	cm, exists := d.agentContexts[agentID]
	d.contextMutex.RUnlock()

	if exists {
		return cm
	}

	// Slow path: create new context with write lock
	d.contextMutex.Lock()
	defer d.contextMutex.Unlock()

	// Double-check after acquiring write lock (another goroutine might have created it)
	if cm, exists = d.agentContexts[agentID]; exists {
		return cm
	}

	// Create new context manager for this agent
	modelName := ""
	if d.LLMClient != nil {
		cfg, err := config.GetConfig()
		if err == nil {
			modelName = cfg.Agents.ArchitectModel
		}
	}

	cm = contextmgr.NewContextManagerWithModel(modelName)

	// Note: Chat service integration for per-agent contexts will be added
	// when needed - currently chat uses single architect context

	d.agentContexts[agentID] = cm
	d.logger.Debug("Created new context for agent %s", agentID)

	return cm
}

// ResetAgentContext resets the context for an agent when they start a new story.
// Called when a coder transitions to SETUP state with a new story assignment.
func (d *Driver) ResetAgentContext(agentID string) error {
	// Get current story for this agent from dispatcher
	storyID := d.dispatcher.GetStoryForAgent(agentID)
	if storyID == "" {
		return fmt.Errorf("no story found for agent %s", agentID)
	}

	// Get or create context for this agent
	cm := d.getContextForAgent(agentID)

	// Build comprehensive system prompt
	systemPrompt, err := d.buildSystemPrompt(agentID, storyID)
	if err != nil {
		return fmt.Errorf("failed to build system prompt: %w", err)
	}

	// Reset context with story-scoped template name
	templateName := fmt.Sprintf("agent-%s-story-%s", agentID, storyID)
	cm.ResetForNewTemplate(templateName, systemPrompt)

	d.logger.Info("✅ Reset context for agent %s (story %s)", agentID, storyID)

	return nil
}

// buildSystemPrompt creates the comprehensive system prompt for an agent context.
// This prompt contains persistent context for the entire story lifecycle.
func (d *Driver) buildSystemPrompt(agentID, storyID string) (string, error) {
	// Get story details from queue
	story, exists := d.queue.GetStory(storyID)
	if !exists {
		return "", fmt.Errorf("story %s not found in queue", storyID)
	}

	// Check if Claude Code mode is enabled
	claudeCodeMode := false
	if cfg, err := config.GetConfig(); err == nil {
		claudeCodeMode = cfg.Agents.CoderMode == config.CoderModeClaudeCode
	}

	// Build template data with story information
	data := &templates.TemplateData{
		Extra: map[string]any{
			"AgentID":        agentID,
			"StoryID":        storyID,
			"StoryTitle":     story.Title,
			"StoryContent":   story.Content,
			"KnowledgePack":  story.KnowledgePack,
			"SpecID":         story.SpecID,
			"ClaudeCodeMode": claudeCodeMode,
		},
	}

	// Template renderer is required
	if d.renderer == nil {
		return "", fmt.Errorf("template renderer not initialized")
	}

	// Render architect system prompt
	prompt, err := d.renderer.Render(templates.ArchitectSystemTemplate, data)
	if err != nil {
		return "", fmt.Errorf("failed to render architect system template: %w", err)
	}

	return prompt, nil
}

// SetStateNotificationChannel implements the ChannelReceiver interface for state change notifications.
func (d *Driver) SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification) {
	// Delegate to BaseStateMachine - it handles all state transitions
	d.BaseStateMachine.SetStateNotificationChannel(stateNotifCh)
	d.logger.Debug("State notification channel set for architect")
}

// Initialize sets up the driver and loads any existing state.
func (d *Driver) Initialize(_ /* ctx */ context.Context) error {
	// Validate required channels are set
	if d.questionsCh == nil {
		return fmt.Errorf("architect %s: questions channel not set (call SetChannels before Initialize)", d.GetAgentID())
	}
	if d.replyCh == nil {
		return fmt.Errorf("architect %s: reply channel not set (call SetChannels before Initialize)", d.GetAgentID())
	}
	if d.BaseStateMachine == nil {
		return fmt.Errorf("architect %s: BaseStateMachine not initialized", d.GetAgentID())
	}

	// Verify state notification channel is set on BaseStateMachine
	// This is critical for state transitions to be properly tracked
	if !d.BaseStateMachine.HasStateNotificationChannel() {
		d.logger.Warn("⚠️  State notification channel not set on BaseStateMachine - state changes won't be tracked")
	}

	// Start fresh - no filesystem state persistence
	// State management is now handled by SQLite for system-level resume functionality
	d.logger.Info("Starting architect fresh for ID: %s (filesystem state persistence removed)", d.GetAgentID())

	d.logger.Info("Architect initialized in state: %s", d.GetCurrentState())

	return nil
}

// GetID returns the architect ID (implements Agent interface).
func (d *Driver) GetID() string {
	return d.GetAgentID()
}

// GetStoryID returns the current story ID (implements StateProvider interface for metrics).
// Architects don't work on individual stories, so return empty string.
func (d *Driver) GetStoryID() string {
	return "" // Architects coordinate stories but don't work on individual ones
}

// Shutdown implements Agent interface with context.
func (d *Driver) Shutdown(ctx context.Context) error {
	// Stop architect container executor
	if d.executor != nil {
		d.logger.Info("Stopping architect container executor")
		if err := d.executor.Stop(ctx); err != nil {
			d.logger.Error("Failed to stop architect executor: %v", err)
			// Continue with shutdown even if executor stop fails
		}
	}

	// Call the original shutdown method
	d.shutdown()
	return nil
}

// shutdown is the internal shutdown method.
func (d *Driver) shutdown() {
	// No filesystem state persistence - clean shutdown

	// Channels are owned by dispatcher, no cleanup needed here
}

// Step implements agent.Driver interface - executes one state transition.
func (d *Driver) Step(ctx context.Context) (bool, error) {
	// Ensure channels are attached.
	if d.questionsCh == nil {
		return false, fmt.Errorf("architect not properly attached to dispatcher - channels are nil")
	}

	// Get current state for error reporting
	currentState := d.GetCurrentState()

	// Process current state to get next state.
	nextState, err := d.processCurrentState(ctx)
	if err != nil {
		return false, fmt.Errorf("state processing error in %s: %w", currentState, err)
	}

	// Check if we're done (reached terminal state).
	if nextState == proto.StateDone || nextState == proto.StateError {
		return true, nil
	}

	// Transition to next state.
	if err := d.TransitionTo(ctx, nextState, nil); err != nil {
		return false, fmt.Errorf("failed to transition from %s to %s: %w", currentState, nextState, err)
	}

	return false, nil
}

// Run starts the architect's state machine loop in WAITING state.
func (d *Driver) Run(ctx context.Context) error {
	// Ensure channels are attached.
	if d.questionsCh == nil {
		return fmt.Errorf("architect not properly attached to dispatcher - channels are nil")
	}

	// Start status updates processor goroutine.
	go d.processStatusUpdates(ctx)

	// Start requeue requests processor goroutine.
	go d.processRequeueRequests(ctx)

	// Initialize state data
	d.SetStateData("started_at", time.Now().UTC())

	// Run the state machine loop.
	for {
		// Check context cancellation.
		select {
		case <-ctx.Done():
			return fmt.Errorf("architect context cancelled: %w", ctx.Err())
		default:
		}

		// Check if we're already in a terminal state.
		currentState := d.GetCurrentState()
		if currentState == StateDone || currentState == StateError {
			break
		}

		// State processing (WAITING states are handled quietly)

		// Process current state.
		nextState, err := d.processCurrentState(ctx)
		if err != nil {
			// Transition to error state.
			if transErr := d.TransitionTo(ctx, StateError, map[string]any{
				"error":        err.Error(),
				"failed_state": currentState.String(),
			}); transErr != nil {
				d.logger.Error("Failed to transition to ERROR: %v", transErr)
			}
			return err
		}

		// Transition to next state (always call TransitionTo - let it handle self-transitions).
		if err := d.TransitionTo(ctx, nextState, nil); err != nil {
			d.logger.Error("Failed to transition from %s to %s: %v", currentState, nextState, err)
			return fmt.Errorf("failed state transition: %w", err)
		}

		// Context compaction now handled automatically by middleware in contextManager.AddMessage()
	}

	return nil
}

// processCurrentState handles the logic for the current state.
func (d *Driver) processCurrentState(ctx context.Context) (proto.State, error) {
	// Process state directly without timeout wrapper
	currentState := d.GetCurrentState()
	switch currentState {
	case StateWaiting:
		// WAITING state - block until request received.
		return d.handleWaiting(ctx)
	case StateDispatching:
		return d.handleDispatching(ctx)
	case StateMonitoring:
		return d.handleMonitoring(ctx)
	case StateRequest:
		return d.handleRequest(ctx)
	case StateEscalated:
		return d.handleEscalated(ctx)
	case StateDone:
		// DONE is a terminal state - should not continue processing.
		return StateDone, nil
	case StateError:
		// ERROR is a terminal state - should not continue processing.
		return StateError, nil
	default:
		return StateError, fmt.Errorf("unknown state: %s", currentState)
	}
}

// GetAgentType returns the type of the agent.
func (d *Driver) GetAgentType() agent.Type {
	return agent.TypeArchitect
}

// ValidateState checks if a state is valid for this architect agent.
func (d *Driver) ValidateState(state proto.State) error {
	if !IsValidArchitectState(state) {
		return fmt.Errorf("invalid state %s for architect agent", state)
	}
	return nil
}

// GetValidStates returns all valid states for this architect agent.
func (d *Driver) GetValidStates() []proto.State {
	return GetAllArchitectStates()
}

// GetQueue returns the queue manager for external access.
func (d *Driver) GetQueue() *Queue {
	return d.queue
}

// GetStoryList returns all stories with their current status for external access.
func (d *Driver) GetStoryList() []*QueuedStory {
	if d.queue == nil {
		return []*QueuedStory{}
	}
	return d.queue.GetAllStories()
}

// GetEscalationHandler returns the escalation handler for external access.
func (d *Driver) GetEscalationHandler() *EscalationHandler {
	return d.escalationHandler
}

// convertContextMessages converts contextmgr.Message format to agent.CompletionMessage format.
func convertContextMessages(contextMessages []contextmgr.Message) []agent.CompletionMessage {
	messages := make([]agent.CompletionMessage, 0, len(contextMessages))
	for i := range contextMessages {
		msg := &contextMessages[i]

		// Convert contextmgr.ToolCall to agent.ToolCall
		var agentToolCalls []agent.ToolCall
		if len(msg.ToolCalls) > 0 {
			agentToolCalls = make([]agent.ToolCall, len(msg.ToolCalls))
			for j := range msg.ToolCalls {
				agentToolCalls[j] = agent.ToolCall{
					ID:         msg.ToolCalls[j].ID,
					Name:       msg.ToolCalls[j].Name,
					Parameters: msg.ToolCalls[j].Parameters,
				}
			}
		}

		// Convert contextmgr.ToolResult to agent.ToolResult
		var agentToolResults []agent.ToolResult
		if len(msg.ToolResults) > 0 {
			agentToolResults = make([]agent.ToolResult, len(msg.ToolResults))
			for j := range msg.ToolResults {
				agentToolResults[j] = agent.ToolResult{
					ToolCallID: msg.ToolResults[j].ToolCallID,
					Content:    msg.ToolResults[j].Content,
					IsError:    msg.ToolResults[j].IsError,
				}
			}
		}

		messages = append(messages, agent.CompletionMessage{
			Role:        agent.CompletionRole(msg.Role),
			Content:     msg.Content,
			ToolCalls:   agentToolCalls,
			ToolResults: agentToolResults,
		})
	}
	return messages
}

// processStatusUpdates runs as a goroutine to process story status updates from coders.
// This provides a non-blocking way for coders to update story status without waiting for architect availability.
func (d *Driver) processStatusUpdates(ctx context.Context) {
	if d.dispatcher == nil {
		d.logger.Warn("No dispatcher available for status updates processing")
		return
	}

	statusUpdatesCh := d.dispatcher.GetStatusUpdatesChannel()
	d.logger.Info("📊 Status updates processor started")

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("📊 Status updates processor stopping due to context cancellation")
			return

		case statusUpdate := <-statusUpdatesCh:
			if statusUpdate == nil {
				d.logger.Info("📊 Status updates channel closed, processor stopping")
				return
			}

			d.logger.Info("📊 Processing status update: story %s → %s", statusUpdate.StoryID, statusUpdate.Status)

			// Convert string status to StoryStatus and update via queue
			if err := d.queue.UpdateStoryStatus(statusUpdate.StoryID, StoryStatus(statusUpdate.Status)); err != nil {
				d.logger.Error("❌ Failed to update story %s status to %s: %v", statusUpdate.StoryID, statusUpdate.Status, err)
			} else {
				d.logger.Info("✅ Successfully updated story %s status to %s", statusUpdate.StoryID, statusUpdate.Status)
			}
		}
	}
}

// processRequeueRequests runs as a goroutine to process story requeue requests from coders.
// This provides a clean channel-based approach to requeuing stories, replacing the legacy ExternalAPIProvider pattern.
// It also implements a retry circuit breaker: after MaxStoryAttempts failures, the architect
// transitions to ERROR state to prevent infinite requeue loops.
func (d *Driver) processRequeueRequests(ctx context.Context) {
	if d.dispatcher == nil {
		d.logger.Warn("No dispatcher available for requeue requests processing")
		return
	}

	requeueRequestsCh := d.dispatcher.GetRequeueRequestsChannel()
	d.logger.Info("🔄 Requeue requests processor started")

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("🔄 Requeue requests processor stopping due to context cancellation")
			return

		case requeueRequest := <-requeueRequestsCh:
			if requeueRequest == nil {
				d.logger.Info("🔄 Requeue requests channel closed, processor stopping")
				return
			}

			d.logger.Info("🔄 Processing requeue request: story %s from agent %s - %s",
				requeueRequest.StoryID, requeueRequest.AgentID, requeueRequest.Reason)

			// Get story and increment attempt count
			story, exists := d.queue.stories[requeueRequest.StoryID]
			if !exists {
				d.logger.Error("❌ Story %s not found for requeue", requeueRequest.StoryID)
				continue
			}

			// Increment attempt count and store failure reason
			story.AttemptCount++
			story.LastFailReason = requeueRequest.Reason

			d.logger.Info("🔄 Story %s attempt count: %d/%d (reason: %s)",
				requeueRequest.StoryID, story.AttemptCount, MaxStoryAttempts, requeueRequest.Reason)

			// Check retry limit - circuit breaker
			if story.AttemptCount >= MaxStoryAttempts {
				d.logger.Error("🚨 Story %s exceeded retry limit (%d attempts). Last failure: %s. Transitioning architect to ERROR.",
					requeueRequest.StoryID, story.AttemptCount, requeueRequest.Reason)

				// Mark story as failed
				story.SetStatus(StatusFailed)

				// Transition architect to ERROR state
				if transErr := d.TransitionTo(ctx, StateError, map[string]any{
					"error":            fmt.Sprintf("story %s exceeded retry limit after %d attempts", requeueRequest.StoryID, story.AttemptCount),
					"failed_story_id":  requeueRequest.StoryID,
					"attempt_count":    story.AttemptCount,
					"last_fail_reason": requeueRequest.Reason,
				}); transErr != nil {
					d.logger.Error("❌ Failed to transition to ERROR state: %v", transErr)
				}
				continue
			}

			// Change story status back to PENDING so it can be picked up again
			if err := d.queue.UpdateStoryStatus(requeueRequest.StoryID, StatusPending); err != nil {
				d.logger.Error("❌ Failed to requeue story %s: %v", requeueRequest.StoryID, err)
				continue
			}

			// Dispatch the story back to the work queue (like DISPATCHING state does)
			if story.GetStatus() == StatusPending {
				// Create story message for dispatcher
				storyMsg := proto.NewAgentMsg(proto.MsgTypeSTORY, d.GetAgentID(), "coder")

				// Build story payload
				payloadData := map[string]any{
					proto.KeyTitle:           story.Title,
					proto.KeyEstimatedPoints: story.EstimatedPoints,
					proto.KeyDependsOn:       story.DependsOn,
					proto.KeyStoryType:       story.StoryType,
					proto.KeyRequirements:    []string{}, // Empty requirements for requeue
				}

				// Use story content from the queue
				content := story.Content
				if content == "" {
					content = story.Title // Fallback to title if content is not set
				}
				payloadData[proto.KeyContent] = content

				// Set typed story payload
				storyMsg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindStory, payloadData))

				// Store story_id in metadata
				storyMsg.SetMetadata(proto.KeyStoryID, requeueRequest.StoryID)

				// Send story to dispatcher using Effects pattern
				dispatchEffect := &DispatchStoryEffect{Story: storyMsg}
				if err := d.ExecuteEffect(ctx, dispatchEffect); err != nil {
					d.logger.Error("❌ Failed to dispatch requeued story %s to work queue: %v", requeueRequest.StoryID, err)
				} else {
					d.logger.Info("✅ Successfully requeued and dispatched story %s to work queue (attempt %d/%d)",
						requeueRequest.StoryID, story.AttemptCount, MaxStoryAttempts)
				}
			} else {
				d.logger.Error("❌ Story %s not in PENDING status after requeue", requeueRequest.StoryID)
			}
		}
	}
}

// createReviewToolProviderForCoder creates a tool provider for structured reviews (approvals).
// Includes read_file, list_files, review_complete, and optionally get_diff.
func (d *Driver) createReviewToolProviderForCoder(coderID string, includeGetDiff bool) *tools.ToolProvider {
	// Inside the architect container, coder workspaces are mounted at /mnt/coders/{coder-id}
	containerWorkDir := fmt.Sprintf("/mnt/coders/%s", coderID)

	ctx := tools.AgentContext{
		Executor:        d.executor,       // Architect executor with read-only mounts
		ChatService:     nil,              // No chat service needed for read tools
		ReadOnly:        true,             // Architect tools are read-only
		NetworkDisabled: false,            // Network allowed for architect
		WorkDir:         containerWorkDir, // Use coder's container mount path
		Agent:           nil,              // No agent reference needed for read tools
	}

	// Build tool list: read tools + review_complete terminal tool
	// Optionally include get_diff for code reviews
	allowedTools := []string{
		tools.ToolReadFile,
		tools.ToolListFiles,
		tools.ToolReviewComplete, // Terminal tool for structured reviews
	}
	if includeGetDiff {
		allowedTools = append(allowedTools, tools.ToolGetDiff)
	}

	return tools.NewProvider(&ctx, allowedTools)
}

// createQuestionToolProviderForCoder creates a tool provider for answering questions.
// Includes read_file, list_files, and submit_reply.
func (d *Driver) createQuestionToolProviderForCoder(coderID string) *tools.ToolProvider {
	// Inside the architect container, coder workspaces are mounted at /mnt/coders/{coder-id}
	containerWorkDir := fmt.Sprintf("/mnt/coders/%s", coderID)

	ctx := tools.AgentContext{
		Executor:        d.executor,       // Architect executor with read-only mounts
		ChatService:     nil,              // No chat service needed for read tools
		ReadOnly:        true,             // Architect tools are read-only
		NetworkDisabled: false,            // Network allowed for architect
		WorkDir:         containerWorkDir, // Use coder's container mount path
		Agent:           nil,              // No agent reference needed for read tools
	}

	// Build tool list: read tools + submit_reply terminal tool
	allowedTools := []string{
		tools.ToolReadFile,
		tools.ToolListFiles,
		tools.ToolSubmitReply, // Terminal tool for text replies
	}

	return tools.NewProvider(&ctx, allowedTools)
}

// getSpecReviewTools creates read-only tools for spec review in REQUEST state.
// These tools allow the architect to inspect its own workspace at /mnt/architect,
// submit structured stories via the submit_stories tool, and provide feedback to PM.
func (d *Driver) getSpecReviewTools() []tools.Tool {
	toolsList := []tools.Tool{
		tools.NewReviewCompleteTool(), // Review decision tool (first loop)
		tools.NewSubmitStoriesTool(),  // Story generation tool (second loop)
	}

	// Add optional read tools if executor available
	if d.executor != nil {
		toolsList = append(toolsList,
			tools.NewReadFileTool(d.executor, "/mnt/architect", 1048576), // 1MB max
			tools.NewListFilesTool(d.executor, "/mnt/architect", 1000),   // 1000 files max
		)
	} else {
		d.logger.Warn("No executor available for read tools in spec review")
	}

	return toolsList
}
