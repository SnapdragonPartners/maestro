// Package architect provides the architect agent implementation for the orchestrator system.
// The architect processes specifications, generates stories, and coordinates with coder agents.
package architect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// Story content constants.
const (
	acceptanceCriteriaHeader = "## Acceptance Criteria\n" //nolint:unused

	// Story type constants to avoid repetition and improve maintainability.
	storyTypeDevOps = "devops"
	storyTypeApp    = "app"
)

// Driver manages the state machine for an architect workflow.
type Driver struct {
	contextManager      *contextmgr.ContextManager
	llmClient           agent.LLMClient                       // LLM for intelligent responses
	renderer            *templates.Renderer                   // Template renderer for prompts
	queue               *Queue                                // Story queue manager
	escalationHandler   *EscalationHandler                    // Escalation handler
	dispatcher          *dispatch.Dispatcher                  // Dispatcher for sending messages
	logger              *logx.Logger                          // Logger with proper agent prefixing
	specCh              <-chan *proto.AgentMsg                // Read-only channel for spec messages
	questionsCh         chan *proto.AgentMsg                  // Bi-directional channel for questions/requests
	replyCh             <-chan *proto.AgentMsg                // Read-only channel for replies
	persistenceChannel  chan<- *persistence.Request           // Channel for database operations
	stateNotificationCh chan<- *proto.StateChangeNotification // Channel for state change notifications
	stateData           map[string]any
	architectID         string
	workDir             string // Workspace directory
	currentState        proto.State
}

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
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)
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
		specCh:      nil,
		questionsCh: nil,
		replyCh:     nil,
	}
}

// NewArchitect creates a new architect with LLM integration.
// The API key is automatically retrieved from environment variables.
func NewArchitect(ctx context.Context, architectID string, dispatcher *dispatch.Dispatcher, workDir string, persistenceChannel chan<- *persistence.Request) (*Driver, error) {
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

	// Create basic LLM client using helper
	llmClient, err := agent.CreateLLMClientForAgent(agent.TypeArchitect)
	if err != nil {
		return nil, fmt.Errorf("failed to create architect LLM client: %w", err)
	}

	// Create architect with LLM integration
	architect := NewDriver(architectID, modelName, llmClient, dispatcher, workDir, persistenceChannel)

	// Enhance client with metrics context now that we have the architect (StateProvider)
	enhancedClient, err := agent.EnhanceLLMClientWithMetrics(llmClient, agent.TypeArchitect, architect, architect.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create enhanced architect LLM client: %w", err)
	}

	// Replace the client with the enhanced version
	architect.llmClient = enhancedClient

	return architect, nil
}

// SetChannels sets the communication channels from the dispatcher.
func (d *Driver) SetChannels(specCh <-chan *proto.AgentMsg, questionsCh chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg) {
	d.specCh = specCh
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
func (d *Driver) Shutdown(_ /* ctx */ context.Context) error {
	// Call the original shutdown method.
	d.shutdown()
	return nil
}

// shutdown is the internal shutdown method.
func (d *Driver) shutdown() {
	// No filesystem state persistence - clean shutdown

	// Channels are owned by dispatcher, no cleanup needed here.
}

// Step implements agent.Driver interface - executes one state transition.
func (d *Driver) Step(ctx context.Context) (bool, error) {
	// Ensure channels are attached.
	if d.specCh == nil || d.questionsCh == nil {
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
	if d.specCh == nil || d.questionsCh == nil {
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
		// WAITING state - block until spec received.
		return d.handleWaiting(ctx)
	case StateScoping:
		return d.handleScoping(ctx)
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

// handleLLMResponse handles LLM responses with proper empty response logic (same as coder).
func (d *Driver) handleLLMResponse(resp agent.CompletionResponse) error {
	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang

	if resp.Content != "" {
		// Case 1: Normal response with content
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
		d.contextManager.AddAssistantMessage(resp.Content)
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
		return nil
	}

	if len(resp.ToolCalls) > 0 {
		// Case 2: Pure tool use - add placeholder for conversational continuity
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
		toolNames := make([]string, len(resp.ToolCalls))
		for i := range resp.ToolCalls {
			toolNames[i] = resp.ToolCalls[i].Name
		}
		placeholder := fmt.Sprintf("Tool %s invoked", strings.Join(toolNames, ", "))
		d.contextManager.AddAssistantMessage(placeholder)
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
		return nil
	}

	// Case 3: True empty response - this is an error condition
	// DO NOT add any message to context - let upstream handle the error
	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
	d.logger.Error("🚨 TRUE EMPTY RESPONSE: No content and no tool calls")
	return logx.Errorf("LLM returned empty response with no content and no tool calls")
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

// buildMessagesWithContext creates completion messages with context history (same as coder).
// This centralizes the pattern used across architect LLM calls with context isolation.
func (d *Driver) buildMessagesWithContext(initialPrompt string) []agent.CompletionMessage {
	messages := []agent.CompletionMessage{
		{Role: agent.RoleUser, Content: initialPrompt},
	}

	// Add conversation history from context manager (same as coder pattern).
	// For architect, we typically reset context between templates, but if context exists, include it.
	contextMessages := d.contextManager.GetMessages()
	for i := range contextMessages {
		msg := &contextMessages[i]
		messages = append(messages, agent.CompletionMessage{
			Role:    agent.CompletionRole(msg.Role),
			Content: msg.Content,
		})
	}

	return messages
}

// callLLMWithTemplate renders a template and gets LLM response using the same pattern as coder.
// This helper centralizes the architect's LLM call pattern with proper context management.
func (d *Driver) callLLMWithTemplate(ctx context.Context, prompt string) (string, error) {
	// Flush user buffer before LLM request (same as coder)
	if err := d.contextManager.FlushUserBuffer(); err != nil {
		return "", fmt.Errorf("failed to flush user buffer: %w", err)
	}

	// Build messages with context (same pattern as coder)
	messages := d.buildMessagesWithContext(prompt)

	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: agent.ArchitectMaxTokens,
	}

	// Get LLM response using same pattern as coder
	d.logger.Info("🔄 Starting LLM call to model '%s' with %d messages, %d max tokens",
		d.llmClient.GetModelName(), len(messages), req.MaxTokens)

	start := time.Now()
	resp, err := d.llmClient.Complete(ctx, req)
	duration := time.Since(start)

	if err != nil {
		d.logger.Error("❌ LLM call failed after %.3gs: %v", duration.Seconds(), err)
		return "", fmt.Errorf("LLM completion failed: %w", err)
	}

	d.logger.Info("✅ LLM call completed in %.3gs, response length: %d chars", duration.Seconds(), len(resp.Content))

	// Handle LLM response with proper empty response logic (same as coder)
	if err := d.handleLLMResponse(resp); err != nil {
		return "", fmt.Errorf("LLM response handling failed: %w", err)
	}

	return resp.Content, nil
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
				storyMsg.SetPayload(proto.KeyStoryID, requeueRequest.StoryID)
				storyMsg.SetPayload(proto.KeyTitle, story.Title)
				storyMsg.SetPayload(proto.KeyEstimatedPoints, story.EstimatedPoints)
				storyMsg.SetPayload(proto.KeyDependsOn, story.DependsOn)
				storyMsg.SetPayload(proto.KeyStoryType, story.StoryType)

				// Use story content from the queue
				content := story.Content
				if content == "" {
					content = story.Title // Fallback to title if content is not set
				}
				storyMsg.SetPayload(proto.KeyContent, content)
				storyMsg.SetPayload(proto.KeyRequirements, []string{}) // Empty requirements for requeue

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
