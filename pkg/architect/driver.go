// Package architect provides the architect agent implementation for the orchestrator system.
// The architect processes specifications, generates stories, and coordinates with coder agents.
package architect

import (
	"context"
	"fmt"
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
)

// LLMClient defines the interface for language model interactions.
type LLMClient interface {
	// GenerateResponse generates a response given a prompt.
	GenerateResponse(ctx context.Context, prompt string) (string, error)
}

// LLMClientToAgentAdapter adapts architect LLMClient to agent.LLMClient.
type LLMClientToAgentAdapter struct {
	client LLMClient
}

// Complete implements the agent.LLMClient interface for completion requests.
func (a *LLMClientToAgentAdapter) Complete(ctx context.Context, req agent.CompletionRequest) (agent.CompletionResponse, error) {
	// Convert the first message to a prompt.
	if len(req.Messages) == 0 {
		return agent.CompletionResponse{}, fmt.Errorf("no messages in completion request")
	}

	prompt := req.Messages[0].Content

	// Call the architect's LLMClient.
	response, err := a.client.GenerateResponse(ctx, prompt)
	if err != nil {
		return agent.CompletionResponse{}, logx.Wrap(err, "architect LLM completion failed")
	}

	// Convert back to agent format.
	return agent.CompletionResponse{
		Content: response,
	}, nil
}

// Stream implements the agent.LLMClient interface for streaming requests.
func (a *LLMClientToAgentAdapter) Stream(ctx context.Context, req agent.CompletionRequest) (<-chan agent.StreamChunk, error) {
	// Simple implementation: call Complete and stream the result as a single chunk.
	response, err := a.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	// Create a channel and send the response as a single chunk.
	ch := make(chan agent.StreamChunk, 1)
	ch <- agent.StreamChunk{
		Content: response.Content,
		Done:    true,
		Error:   nil,
	}
	close(ch)

	return ch, nil
}

// Driver manages the state machine for an architect workflow.
type Driver struct {
	currentState       proto.State
	stateData          map[string]any
	contextManager     *contextmgr.ContextManager
	llmClient          LLMClient                   // LLM for intelligent responses
	renderer           *templates.Renderer         // Template renderer for prompts
	queue              *Queue                      // Story queue manager
	escalationHandler  *EscalationHandler          // Escalation handler
	dispatcher         *dispatch.Dispatcher        // Dispatcher for sending messages
	logger             *logx.Logger                // Logger with proper agent prefixing
	orchestratorConfig *config.Config              // Orchestrator configuration for repo access
	specCh             <-chan *proto.AgentMsg      // Read-only channel for spec messages
	questionsCh        chan *proto.AgentMsg        // Bi-directional channel for questions/requests
	replyCh            <-chan *proto.AgentMsg      // Read-only channel for replies
	persistenceChannel chan<- *persistence.Request // Channel for database operations
	architectID        string
	workDir            string // Workspace directory
}

// NewDriver creates a new architect driver instance.
func NewDriver(architectID string, modelConfig *config.Model, llmClient LLMClient, dispatcher *dispatch.Dispatcher, workDir string, orchestratorConfig *config.Config, persistenceChannel chan<- *persistence.Request) *Driver {
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

	return &Driver{
		architectID:        architectID,
		contextManager:     contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:       StateWaiting,
		stateData:          make(map[string]any),
		llmClient:          llmClient,
		renderer:           renderer,
		workDir:            workDir,
		queue:              queue,
		escalationHandler:  escalationHandler,
		dispatcher:         dispatcher,
		logger:             logx.NewLogger(architectID),
		orchestratorConfig: orchestratorConfig,
		persistenceChannel: persistenceChannel,
		// Channels will be set during Attach()
		specCh:      nil,
		questionsCh: nil,
		replyCh:     nil,
	}
}

// SetChannels sets the communication channels from the dispatcher.
func (d *Driver) SetChannels(specCh <-chan *proto.AgentMsg, questionsCh chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg) {
	d.specCh = specCh
	d.questionsCh = questionsCh
	d.replyCh = replyCh

	d.logger.Info("🏗️ Architect %s channels set: spec=%p questions=%p reply=%p", d.architectID, specCh, questionsCh, replyCh)
}

// SetDispatcher sets the dispatcher reference (already set in constructor, but required for interface).
func (d *Driver) SetDispatcher(dispatcher *dispatch.Dispatcher) {
	// Architect already has dispatcher from constructor, but update it for consistency.
	d.dispatcher = dispatcher
	d.logger.Info("🏗️ Architect %s dispatcher set: %p", d.architectID, dispatcher)
}

// SetStateNotificationChannel implements the ChannelReceiver interface for state change notifications.
func (d *Driver) SetStateNotificationChannel(_ /* stateNotifCh */ chan<- *proto.StateChangeNotification) {
	// TODO: Implement state change notifications for architect
	// For now, just log that it's set - architect uses different state management.
	d.logger.Info("🏗️ Architect %s state notification channel set", d.architectID)
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

// Shutdown implements Agent interface with context.
func (d *Driver) Shutdown(_ /* ctx */ context.Context) error {
	// Call the original shutdown method.
	d.shutdown()
	return nil
}

// shutdown is the internal shutdown method.
func (d *Driver) shutdown() {
	// No filesystem state persistence - clean shutdown
	d.logger.Info("🏗️ Architect %s shutting down cleanly (no state persistence)", d.architectID)

	// Channels are owned by dispatcher, no cleanup needed here.
	d.logger.Info("🏗️ Architect %s shutdown completed", d.architectID)
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
	d.logger.Info("🏗️ Architect %s starting state machine", d.architectID)

	// Ensure channels are attached.
	if d.specCh == nil || d.questionsCh == nil {
		return fmt.Errorf("architect not properly attached to dispatcher - channels are nil")
	}

	// Start in WAITING state, ready to receive specs.
	d.currentState = StateWaiting
	d.stateData = make(map[string]any)
	d.stateData["started_at"] = time.Now().UTC()

	d.logger.Info("🏗️ Architect ready in WAITING state")

	// Run the state machine loop.
	for {
		// Check context cancellation.
		select {
		case <-ctx.Done():
			d.logger.Info("🏗️ Architect state machine context cancelled")
			return fmt.Errorf("architect context cancelled: %w", ctx.Err())
		default:
		}

		// Check if we're already in a terminal state.
		if d.currentState == StateDone || d.currentState == StateError {
			d.logger.Info("🏗️ Architect state machine reached terminal state: %s", d.currentState)
			break
		}

		// Log state processing (only for non-waiting states to reduce noise).
		if d.currentState != StateWaiting {
			d.logger.Info("🏗️ Architect processing state: %s", d.currentState)
		}

		// Process current state.
		nextState, err := d.processCurrentState(ctx)
		if err != nil {
			d.logger.Error("🏗️ Architect state processing error in %s: %v", d.currentState, err)
			// Transition to error state.
			d.transitionTo(ctx, StateError, map[string]any{
				"error":        err.Error(),
				"failed_state": d.currentState.String(),
			})
			return err
		}

		// Transition to next state (always call transitionTo - let it handle self-transitions).
		d.transitionTo(ctx, nextState, nil)

		// Compact context if needed.
		if err := d.contextManager.CompactIfNeeded(); err != nil {
			// Log warning but don't fail.
			d.logger.Warn("context compaction failed: %v", err)
		}
	}

	d.logger.Info("🏗️ Architect state machine completed")
	return nil
}

// processCurrentState handles the logic for the current state.
func (d *Driver) processCurrentState(ctx context.Context) (proto.State, error) {
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
	case StateMerging:
		return d.handleMerging(ctx)
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

	// No filesystem state persistence - state transitions are tracked in memory only

	// Enhanced logging for debugging.
	if oldState != newState {
		d.logger.Info("🏗️ Architect state transition: %s → %s", oldState, newState)
	} else {
		d.logger.Info("🏗️ Architect staying in state: %s", oldState)
	}
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
