// Package architect provides the architect agent implementation for the orchestrator system.
// The architect processes specifications, generates stories, and coordinates with coder agents.
package architect

import (
	"context"
	"fmt"
	"os"
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

// Story content constants.
const (
	acceptanceCriteriaHeader = "## Acceptance Criteria\n" //nolint:unused

	// Story type constants to avoid repetition and improve maintainability.
	storyTypeDevOps = "devops"
	storyTypeApp    = "app"

	// Tool signal constants.
	signalSubmitStoriesComplete = "SUBMIT_STORIES_COMPLETE"
	signalSpecFeedbackSent      = "SPEC_FEEDBACK_SENT"
)

// listToolProvider adapts a slice of tools.Tool to implement toolloop.ToolProvider.
// This allows architect to use toolloop with its dynamic tool list pattern.
type listToolProvider struct {
	toolsMap  map[string]tools.Tool
	toolsList []tools.Tool
}

// newListToolProvider creates a ToolProvider from a list of tools.
func newListToolProvider(toolsList []tools.Tool) *listToolProvider {
	toolsMap := make(map[string]tools.Tool, len(toolsList))
	for _, tool := range toolsList {
		toolsMap[tool.Name()] = tool
	}
	return &listToolProvider{
		toolsList: toolsList,
		toolsMap:  toolsMap,
	}
}

// Get retrieves a tool by name.
func (p *listToolProvider) Get(name string) (tools.Tool, error) {
	tool, ok := p.toolsMap[name]
	if !ok {
		return nil, fmt.Errorf("tool %s not found", name)
	}
	return tool, nil
}

// List returns tool metadata for all tools.
func (p *listToolProvider) List() []tools.ToolMeta {
	metas := make([]tools.ToolMeta, len(p.toolsList))
	for i, tool := range p.toolsList {
		def := tool.Definition()
		metas[i] = tools.ToolMeta(def)
	}
	return metas
}

// Driver manages the state machine for an architect workflow.
type Driver struct {
	contextManager      *contextmgr.ContextManager
	llmClient           agent.LLMClient                       // LLM for intelligent responses
	renderer            *templates.Renderer                   // Template renderer for prompts
	queue               *Queue                                // Story queue manager
	escalationHandler   *EscalationHandler                    // Escalation handler
	dispatcher          *dispatch.Dispatcher                  // Dispatcher for sending messages
	logger              *logx.Logger                          // Logger with proper agent prefixing
	executor            *execpkg.ArchitectExecutor            // Container executor for file access tools
	chatService         ChatServiceInterface                  // Chat service for escalations (nil check required)
	questionsCh         chan *proto.AgentMsg                  // Bi-directional channel for requests (specs, questions, approvals)
	replyCh             <-chan *proto.AgentMsg                // Read-only channel for replies
	persistenceChannel  chan<- *persistence.Request           // Channel for database operations
	stateNotificationCh chan<- *proto.StateChangeNotification // Channel for state change notifications
	stateData           map[string]any
	architectID         string
	workDir             string // Workspace directory
	currentState        proto.State
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
func NewDriver(architectID, modelName string, llmClient agent.LLMClient, dispatcher *dispatch.Dispatcher, workDir string, persistenceChannel chan<- *persistence.Request) *Driver {
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

	return &Driver{
		architectID:        architectID,
		contextManager:     contextmgr.NewContextManagerWithModel(modelName),
		currentState:       StateWaiting,
		stateData:          make(map[string]any),
		llmClient:          llmClient,
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

	// Create basic LLM client from shared factory (no metrics context yet, need architect instance first)
	llmClient, err := llmFactory.CreateClient(agent.TypeArchitect)
	if err != nil {
		return nil, fmt.Errorf("failed to create architect LLM client: %w", err)
	}

	// Create architect with LLM integration
	architect := NewDriver(architectID, modelName, llmClient, dispatcher, workDir, persistenceChannel)

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

	// Enhance client with metrics context now that we have the architect (StateProvider)
	// Use the shared factory to ensure proper rate limiting
	enhancedClient, err := llmFactory.CreateClientWithContext(agent.TypeArchitect, architect, architect.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create enhanced architect LLM client: %w", err)
	}

	// Replace the client with the enhanced version
	architect.llmClient = enhancedClient

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

// SetStateNotificationChannel implements the ChannelReceiver interface for state change notifications.
func (d *Driver) SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification) {
	d.stateNotificationCh = stateNotifCh
	d.logger.Debug("State notification channel set for architect")
}

// Initialize sets up the driver and loads any existing state.
func (d *Driver) Initialize(_ /* ctx */ context.Context) error {
	// Start fresh - no filesystem state persistence
	// State management is now handled by SQLite for system-level resume functionality
	d.logger.Info("Starting architect fresh for ID: %s (filesystem state persistence removed)", d.architectID)
	savedState := ""
	savedData := make(map[string]any)

	// If we have saved state, restore it.
	if savedState != "" {
		d.logger.Info("Found saved state: %s, restoring...", savedState)
		// Convert string state to proto.State.
		loadedState := d.stringToState(savedState)
		if loadedState == StateError && savedState != "Error" {
			d.logger.Warn("loaded unknown state '%s', setting to ERROR", savedState)
		}
		d.currentState = loadedState
		d.stateData = savedData
		d.logger.Info("Restored architect to state: %s", d.currentState)
	} else {
		d.logger.Info("No saved state found, starting fresh")
	}

	d.logger.Info("Architect initialized")

	return nil
}

// stringToState converts a string state to proto.State.
// Returns StateError for unknown states.
func (d *Driver) stringToState(stateStr string) proto.State {
	// Direct string to proto.State conversion since we're using string constants.
	state := proto.State(stateStr)
	if err := ValidateState(state); err != nil {
		return StateError
	}
	return state
}

// GetID returns the architect ID (implements Agent interface).
func (d *Driver) GetID() string {
	return d.architectID
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

	// Process current state to get next state.
	nextState, err := d.processCurrentState(ctx)
	if err != nil {
		return false, fmt.Errorf("state processing error in %s: %w", d.currentState, err)
	}

	// Check if we're done (reached terminal state).
	if nextState == proto.StateDone || nextState == proto.StateError {
		return true, nil
	}

	// Transition to next state.
	d.transitionTo(ctx, nextState, nil)

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

	// Start in WAITING state, ready to receive specs.
	d.currentState = StateWaiting
	d.stateData = make(map[string]any)
	d.stateData["started_at"] = time.Now().UTC()

	// Run the state machine loop.
	for {
		// Check context cancellation.
		select {
		case <-ctx.Done():
			return fmt.Errorf("architect context cancelled: %w", ctx.Err())
		default:
		}

		// Check if we're already in a terminal state.
		if d.currentState == StateDone || d.currentState == StateError {
			break
		}

		// State processing (WAITING states are handled quietly)

		// Process current state.
		nextState, err := d.processCurrentState(ctx)
		if err != nil {
			// Transition to error state.
			d.transitionTo(ctx, StateError, map[string]any{
				"error":        err.Error(),
				"failed_state": d.currentState.String(),
			})
			return err
		}

		// Transition to next state (always call transitionTo - let it handle self-transitions).
		d.transitionTo(ctx, nextState, nil)

		// Context compaction now handled automatically by middleware in contextManager.AddMessage()
	}

	return nil
}

// processCurrentState handles the logic for the current state.
func (d *Driver) processCurrentState(ctx context.Context) (proto.State, error) {
	// Process state directly without timeout wrapper
	switch d.currentState {
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
		return StateError, fmt.Errorf("unknown state: %s", d.currentState)
	}
}

// transitionTo moves the driver to a new state and persists it.
func (d *Driver) transitionTo(_ context.Context, newState proto.State, additionalData map[string]any) {
	oldState := d.currentState
	d.currentState = newState

	// Add transition metadata.
	d.stateData["previous_state"] = oldState.String()
	d.stateData["current_state"] = newState.String()
	d.stateData["transition_at"] = time.Now().UTC()

	// Special handling for ESCALATED state - record escalation timestamp for timeout guard.
	if newState == StateEscalated {
		d.stateData["escalated_at"] = time.Now().UTC()
		d.logger.Info("entered ESCALATED state - timeout guard set for %v", EscalationTimeout)
	}

	// Merge additional data if provided.
	for k, v := range additionalData {
		d.stateData[k] = v
	}

	// Send state change notification if channel is available
	if d.stateNotificationCh != nil {
		notification := &proto.StateChangeNotification{
			AgentID:   d.architectID,
			FromState: oldState,
			ToState:   newState,
		}

		// Safe channel send with panic recovery for closed channel
		func() {
			defer func() {
				if r := recover(); r != nil {
					d.logger.Debug("State notification channel closed, could not send %s -> %s transition", oldState, newState)
				}
			}()

			// Non-blocking send to prevent deadlock
			select {
			case d.stateNotificationCh <- notification:
				d.logger.Debug("Sent state change notification: %s -> %s", oldState, newState)
			default:
				d.logger.Warn("State notification channel full, could not send %s -> %s transition", oldState, newState)
			}
		}()
	}

	// No filesystem state persistence - state transitions are tracked in memory only

	// State transition completed
}

// GetCurrentState returns the current state of the driver.
func (d *Driver) GetCurrentState() proto.State {
	return d.currentState
}

// GetStateData returns a copy of the current state data.
func (d *Driver) GetStateData() map[string]any {
	result := make(map[string]any)
	for k, v := range d.stateData {
		result[k] = v
	}
	return result
}

// GetAgentType returns the type of the agent.
func (d *Driver) GetAgentType() agent.Type {
	return agent.TypeArchitect
}

// ValidateState checks if a state is valid for this architect agent.
func (d *Driver) ValidateState(state proto.State) error {
	return ValidateState(state)
}

// GetValidStates returns all valid states for this architect agent.
func (d *Driver) GetValidStates() []proto.State {
	return GetValidStates()
}

// GetContextSummary returns a summary of the current context.
func (d *Driver) GetContextSummary() string {
	return d.contextManager.GetContextSummary()
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

// buildMessagesWithContext creates completion messages with context history.
// Converts context manager messages (with structured ToolCalls and ToolResults) to CompletionMessage format.
// Same pattern as PM's buildMessagesWithContext.
func (d *Driver) buildMessagesWithContext(initialPrompt string) []agent.CompletionMessage {
	// Get conversation history from context manager
	contextMessages := d.contextManager.GetMessages()

	// Convert to CompletionMessage format
	messages := make([]agent.CompletionMessage, 0, len(contextMessages)+1)
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

	// Add the new prompt as a user message if provided
	if initialPrompt != "" {
		messages = append(messages, agent.CompletionMessage{
			Role:    agent.RoleUser,
			Content: initialPrompt,
		})
	}

	return messages
}

// callLLMWithTemplate renders a template and gets LLM response using the same pattern as coder.
// This helper centralizes the architect's LLM call pattern with proper context management.
func (d *Driver) callLLMWithTemplate(ctx context.Context, prompt string) (string, error) {
	return d.callLLMWithTools(ctx, prompt, nil)
}

// callLLMWithTools allows calling LLM with optional tools (used for spec review in REQUEST state).
func (d *Driver) callLLMWithTools(ctx context.Context, prompt string, toolsList []tools.Tool) (string, error) {
	// Create ToolProvider adapter from toolsList
	toolProvider := newListToolProvider(toolsList)

	// Use toolloop abstraction for LLM tool calling loop
	loop := toolloop.New(d.llmClient, d.logger)

	cfg := &toolloop.Config{
		ContextManager: d.contextManager,
		InitialPrompt:  prompt,
		ToolProvider:   toolProvider,
		MaxIterations:  20, // Increased for complex spec review workflows
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        d.architectID, // Agent ID for tool context
		DebugLogging:   false,         // Enable for debugging: shows messages sent to LLM
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			// Check for terminal tools and return signal
			return d.checkTerminalTools(calls, results)
		},
		OnIterationLimit: func(_ context.Context) (string, error) {
			// Architect returns error on iteration limit (no budget request)
			return "", fmt.Errorf("maximum tool iterations exceeded")
		},
	}

	signal, err := loop.Run(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("toolloop execution failed: %w", err)
	}

	return signal, nil
}

// checkTerminalTools examines tool execution results for terminal signals.
// Returns non-empty signal to trigger state transition.
func (d *Driver) checkTerminalTools(calls []agent.ToolCall, results []any) string {
	for i := range calls {
		toolCall := &calls[i]

		// Handle submit_reply tool - signals iteration completion (for REQUEST/ANSWERING states)
		if toolCall.Name == tools.ToolSubmitReply {
			response, ok := toolCall.Parameters["response"].(string)
			if !ok || response == "" {
				d.logger.Error("submit_reply tool called without response parameter")
				continue
			}

			d.logger.Info("✅ Architect submitted reply via submit_reply tool")
			return response
		}

		// Handle submit_stories tool - signals spec review completion with structured data
		if toolCall.Name == tools.ToolSubmitStories {
			// Check if tool executed successfully from results
			resultMap, ok := results[i].(map[string]any)
			if !ok {
				d.logger.Error("submit_stories tool result is not a map")
				continue
			}

			// Check for errors
			if success, ok := resultMap["success"].(bool); ok && !success {
				d.logger.Error("submit_stories tool failed")
				continue
			}

			// Store the structured result in state data for scoping to access
			d.stateData["submit_stories_result"] = results[i]

			d.logger.Info("✅ Architect submitted stories via submit_stories tool")
			return signalSubmitStoriesComplete
		}

		// Handle spec_feedback tool - architect sends feedback to PM
		if toolCall.Name == tools.ToolSpecFeedback {
			// Check if tool executed successfully from results
			resultMap, ok := results[i].(map[string]any)
			if !ok {
				d.logger.Error("spec_feedback tool result is not a map")
				continue
			}

			// Check for errors
			if success, ok := resultMap["success"].(bool); ok && !success {
				d.logger.Error("spec_feedback tool failed")
				continue
			}

			// Store the feedback result in state data for message sending
			d.stateData["spec_feedback_result"] = results[i]

			d.logger.Info("✅ Architect sent feedback to PM via spec_feedback tool")
			return signalSpecFeedbackSent
		}
	}

	return "" // Continue loop
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

			// Change story status back to PENDING so it can be picked up again
			if err := d.queue.UpdateStoryStatus(requeueRequest.StoryID, StatusPending); err != nil {
				d.logger.Error("❌ Failed to requeue story %s: %v", requeueRequest.StoryID, err)
				continue
			}

			// Also dispatch the story back to the work queue (like DISPATCHING state does)
			if story, exists := d.queue.stories[requeueRequest.StoryID]; exists && story.GetStatus() == StatusPending {
				// Create story message for dispatcher
				storyMsg := proto.NewAgentMsg(proto.MsgTypeSTORY, d.architectID, "coder")

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
					d.logger.Info("✅ Successfully requeued and dispatched story %s to work queue", requeueRequest.StoryID)
				}
			} else {
				d.logger.Error("❌ Story %s not found or not in PENDING status after requeue", requeueRequest.StoryID)
			}
		}
	}
}

// checkIterationLimit checks if the architect has exceeded iteration limits.
// Returns true if hard limit exceeded (should escalate), false otherwise.
// Soft limit triggers warning, hard limit triggers escalation to ESCALATE state.
func (d *Driver) checkIterationLimit(stateDataKey string, stateName proto.State) bool {
	const softLimit = 8
	const hardLimit = 16

	// Get current iteration count
	iterationCount := 0
	if val, exists := d.stateData[stateDataKey]; exists {
		if count, ok := val.(int); ok {
			iterationCount = count
		}
	}

	// Increment iteration count
	iterationCount++
	d.stateData[stateDataKey] = iterationCount

	// Check soft limit (warning only)
	if iterationCount == softLimit {
		d.logger.Warn("⚠️  Soft iteration limit (%d) reached in %s - architect should consider finalizing analysis", softLimit, stateName)
		// Add warning to context for LLM to see
		warningMsg := fmt.Sprintf("Warning: You have used %d iterations in this phase. Consider finalizing your analysis soon to avoid escalation.", softLimit)
		d.contextManager.AddMessage("system-warning", warningMsg)
		return false
	}

	// Check hard limit (escalate)
	if iterationCount >= hardLimit {
		d.logger.Error("❌ Hard iteration limit (%d) exceeded in %s - escalating to human", hardLimit, stateName)
		// Store escalation context for ESCALATE state
		d.stateData["escalation_origin_state"] = string(stateName)
		d.stateData["escalation_iteration_count"] = iterationCount
		// Additional context will be added by caller (request_id, story_id)
		return true
	}

	d.logger.Debug("Iteration %d/%d (soft: %d, hard: %d) in %s", iterationCount, hardLimit, softLimit, hardLimit, stateName)
	return false
}

// createReadToolProviderForCoder creates a tool provider rooted at a specific coder's workspace.
// coderID should be the agent ID (e.g., "coder-001").
// The tools will be rooted at /mnt/coders/{coderID} inside the architect container.
func (d *Driver) createReadToolProviderForCoder(coderID string) *tools.ToolProvider {
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

	return tools.NewProvider(&ctx, tools.ArchitectReadTools)
}

// createReviewToolProvider creates a tool provider with only the review_complete tool
// for single-turn approval reviews (Plan and BudgetReview).
func (d *Driver) createReviewToolProvider() *tools.ToolProvider {
	ctx := tools.AgentContext{
		Executor:        d.executor, // Architect executor (not needed but required)
		ChatService:     nil,        // No chat service needed
		ReadOnly:        true,       // Read-only context
		NetworkDisabled: false,      // Network allowed
		WorkDir:         "",         // No workspace needed for review_complete
		Agent:           nil,        // No agent reference needed
	}

	// Only provide review_complete tool for single-turn reviews
	reviewTools := []string{tools.ToolReviewComplete}
	return tools.NewProvider(&ctx, reviewTools)
}

// processArchitectToolCalls processes tool calls for architect states (REQUEST for spec review and coder questions).
// Returns the submit_reply response if detected, nil otherwise.
func (d *Driver) processArchitectToolCalls(ctx context.Context, toolCalls []agent.ToolCall, toolProvider *tools.ToolProvider) (string, error) {
	d.logger.Info("Processing %d architect tool calls", len(toolCalls))

	for i := range toolCalls {
		toolCall := &toolCalls[i]
		d.logger.Info("Executing architect tool: %s", toolCall.Name)

		// Handle submit_reply tool - signals iteration completion (for REQUEST/ANSWERING states)
		if toolCall.Name == tools.ToolSubmitReply {
			response, ok := toolCall.Parameters["response"].(string)
			if !ok || response == "" {
				return "", fmt.Errorf("submit_reply tool called without response parameter")
			}

			d.logger.Info("✅ Architect submitted reply via submit_reply tool")
			return response, nil
		}

		// Handle review_complete tool - signals single-turn review completion (for Plan/BudgetReview)
		if toolCall.Name == tools.ToolReviewComplete {
			// Execute the tool to get validated structured data
			tool, err := toolProvider.Get(toolCall.Name)
			if err != nil {
				return "", fmt.Errorf("review_complete tool not found: %w", err)
			}

			result, err := tool.Exec(ctx, toolCall.Parameters)
			if err != nil {
				return "", fmt.Errorf("review_complete validation failed: %w", err)
			}

			// Store the structured result in state data for approval handling to access
			d.stateData["review_complete_result"] = result

			d.logger.Info("✅ Architect completed review via review_complete tool")
			return "REVIEW_COMPLETE", nil // Signal that review is complete
		}

		// Handle submit_stories tool - signals spec review completion with structured data
		if toolCall.Name == tools.ToolSubmitStories {
			// Execute the tool to get validated structured data
			tool, err := toolProvider.Get(toolCall.Name)
			if err != nil {
				return "", fmt.Errorf("submit_stories tool not found: %w", err)
			}

			result, err := tool.Exec(ctx, toolCall.Parameters)
			if err != nil {
				return "", fmt.Errorf("submit_stories validation failed: %w", err)
			}

			// Store the structured result in state data for spec review to access
			d.stateData["submit_stories_result"] = result

			d.logger.Info("✅ Architect submitted stories via submit_stories tool")
			return signalSubmitStoriesComplete, nil // Signal that stories were submitted
		}

		// Handle spec_feedback tool - architect sends feedback to PM
		if toolCall.Name == tools.ToolSpecFeedback {
			// Execute the tool to validate feedback
			tool, err := toolProvider.Get(toolCall.Name)
			if err != nil {
				return "", fmt.Errorf("spec_feedback tool not found: %w", err)
			}

			result, err := tool.Exec(ctx, toolCall.Parameters)
			if err != nil {
				return "", fmt.Errorf("spec_feedback validation failed: %w", err)
			}

			// Store the feedback result in state data for message sending
			d.stateData["spec_feedback_result"] = result

			d.logger.Info("✅ Architect sent feedback to PM via spec_feedback tool")
			return signalSpecFeedbackSent, nil // Signal that feedback was sent
		}

		// Get tool from ToolProvider and execute
		tool, err := toolProvider.Get(toolCall.Name)
		if err != nil {
			d.logger.Error("Tool not found in ToolProvider: %s", toolCall.Name)
			d.contextManager.AddMessage("tool-error", fmt.Sprintf("Tool %s not found: %v", toolCall.Name, err))
			continue
		}

		// Add agent_id to context for tools that need it
		toolCtx := context.WithValue(ctx, tools.AgentIDContextKey, d.architectID)

		// Execute tool
		startTime := time.Now()
		result, err := tool.Exec(toolCtx, toolCall.Parameters)
		duration := time.Since(startTime)

		// Log tool execution to database (fire-and-forget)
		storyID := ""
		if sid, exists := d.stateData["current_story_id"]; exists {
			if sidStr, ok := sid.(string); ok {
				storyID = sidStr
			}
		}
		agent.LogToolExecution(toolCall, result, err, duration, d.architectID, storyID, d.persistenceChannel)

		if err != nil {
			d.logger.Info("Tool execution failed for %s: %v", toolCall.Name, err)
			d.contextManager.AddMessage("tool-error", fmt.Sprintf("Tool %s failed: %v", toolCall.Name, err))
			continue
		}

		d.logger.Debug("Tool %s completed in %.3fs", toolCall.Name, duration.Seconds())

		// Add structured tool result to buffer (same as PM pattern)
		// Convert result to string format
		var resultStr string
		var isError bool
		if resultMap, ok := result.(map[string]any); ok {
			// Check for success field
			if success, ok := resultMap["success"].(bool); ok && !success {
				isError = true
				// Extract error message if present
				if errMsg, ok := resultMap["error"].(string); ok {
					resultStr = errMsg
				} else {
					resultStr = fmt.Sprintf("Tool failed: %v", result)
				}
			} else {
				// Success - convert entire result to string
				resultStr = fmt.Sprintf("%v", result)
			}
		} else {
			// Non-map result - convert to string
			resultStr = fmt.Sprintf("%v", result)
		}

		d.contextManager.AddToolResult(toolCall.ID, resultStr, isError)
		d.logger.Info("Architect tool %s executed successfully", toolCall.Name)
	}

	return "", nil // No submit_reply detected
}

// getSpecReviewTools creates read-only tools for spec review in REQUEST state.
// These tools allow the architect to inspect its own workspace at /mnt/architect,
// submit structured stories via the submit_stories tool, and provide feedback to PM.
func (d *Driver) getSpecReviewTools() []tools.Tool {
	toolsList := []tools.Tool{
		tools.NewSubmitStoriesTool(), // Primary completion tool
		tools.NewSpecFeedbackTool(),  // PM feedback tool
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
