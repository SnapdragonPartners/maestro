// Package architect provides the architect agent implementation for the orchestrator system.
// The architect processes specifications, generates stories, and coordinates with coder agents.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	"orchestrator/pkg/utils"
)

const (
	buildSystemMake   = "make"
	buildSystemPython = "python"
	buildSystemNode   = "node"

	// Story content constants.
	acceptanceCriteriaHeader = "## Acceptance Criteria\n"
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

// handleWaiting blocks until a spec message or question is received.
func (d *Driver) handleWaiting(ctx context.Context) (proto.State, error) {
	d.logger.Info("🏗️ Architect waiting for spec or question...")

	select {
	case <-ctx.Done():
		d.logger.Info("🏗️ Architect WAITING state context cancelled")
		return StateError, fmt.Errorf("architect waiting cancelled: %w", ctx.Err())
	case specMsg, ok := <-d.specCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			d.logger.Info("🏗️ Spec channel closed, transitioning to ERROR")
			return StateError, fmt.Errorf("spec channel closed unexpectedly")
		}

		if specMsg == nil {
			// This shouldn't happen with proper channel management, but handle gracefully
			d.logger.Warn("🏗️ Received nil spec message on open channel")
			return StateWaiting, nil
		}
		d.logger.Info("🏗️ Architect received spec message %s, transitioning to SCOPING", specMsg.ID)

		// Store the spec message for processing in SCOPING state.
		d.stateData["spec_message"] = specMsg

		return StateScoping, nil
	case questionMsg, ok := <-d.questionsCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			d.logger.Info("🏗️ Questions channel closed, transitioning to ERROR")
			return StateError, fmt.Errorf("questions channel closed unexpectedly")
		}

		if questionMsg == nil {
			// This shouldn't happen with proper channel management, but handle gracefully
			d.logger.Warn("🏗️ Received nil question message on open channel")
			return StateWaiting, nil
		}
		d.logger.Info("🏗️ Architect received question message %s in WAITING state, transitioning to REQUEST", questionMsg.ID)

		// Store the question for processing in REQUEST state.
		d.stateData["current_request"] = questionMsg

		return StateRequest, nil
	}
}

// ownsSpec checks if the architect currently owns a spec.
func (d *Driver) ownsSpec() bool {
	// Check if we have a spec message in state data.
	if _, hasSpec := d.stateData["spec_message"]; hasSpec {
		return true
	}

	// Check if we have stories in the queue (indicating we're working on a spec).
	if d.queue != nil && len(d.queue.GetAllStories()) > 0 {
		return true
	}

	return false
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

// handleScoping processes the scoping phase (platform detection, bootstrap, spec analysis and story generation).
func (d *Driver) handleScoping(ctx context.Context) (proto.State, error) {
	d.contextManager.AddMessage("assistant", "Scoping phase: analyzing specification and generating stories")

	// Extract spec file path from the SPEC message.
	specFile := d.getSpecFileFromMessage()
	if specFile == "" {
		return StateError, fmt.Errorf("no spec file path found in SPEC message")
	}

	d.logger.Info("🏗️ Architect reading spec file: %s", specFile)

	// Get spec content - either from file or direct content
	var rawSpecContent []byte
	var err error

	// Check if we have direct content from bootstrap
	// Get the stored spec message first
	var contentPayload interface{}
	var hasContent bool
	if specMsgData, exists := d.stateData["spec_message"]; exists {
		if currentSpecMsg, ok := specMsgData.(*proto.AgentMsg); ok {
			contentPayload, hasContent = currentSpecMsg.GetPayload("spec_content")
		}
	}

	if hasContent {
		if contentStr, ok := contentPayload.(string); ok {
			rawSpecContent = []byte(contentStr)
			d.logger.Info("🏗️ Using direct spec content from bootstrap (%d bytes)", len(rawSpecContent))
		} else {
			return StateError, fmt.Errorf("spec_content payload is not a string: %T", contentPayload)
		}
	} else {
		// Fallback to file-based spec reading
		d.logger.Info("🏗️ Reading spec from file: %s", specFile)
		rawSpecContent, err = os.ReadFile(specFile)
		if err != nil {
			return StateError, fmt.Errorf("failed to read spec file %s: %w", specFile, err)
		}
	}

	// Spec Analysis - check if spec already parsed.
	var requirements []Requirement
	if _, exists := d.stateData["spec_parsing_completed_at"]; !exists {
		// LLM parsing is required - no fallback.
		if d.llmClient == nil {
			return StateError, fmt.Errorf("LLM client not available - spec analysis requires LLM")
		}

		requirements, err = d.parseSpecWithLLM(ctx, string(rawSpecContent), specFile)
		if err != nil {
			return StateError, fmt.Errorf("LLM spec analysis failed: %w", err)
		}
		d.stateData["parsing_method"] = "llm_primary"

		// Store parsed requirements.
		d.stateData["requirements"] = requirements
		d.stateData["raw_spec_content"] = string(rawSpecContent)
		d.stateData["spec_parsing_completed_at"] = time.Now().UTC()
	} else {
		// Reload requirements from state data.
		if reqData, exists := d.stateData["requirements"]; exists {
			requirements, err = d.convertToRequirements(reqData)
			if err != nil {
				return StateError, fmt.Errorf("failed to convert requirements from state data: %w", err)
			}
		}
	}

	// STEP 4: Story Generation - check if stories already generated.
	if _, exists := d.stateData["stories_generated"]; !exists {
		// Generate stories from LLM-analyzed requirements.
		if d.persistenceChannel != nil {
			// Use database-aware story generation from requirements.
			specID, storyIDs, err := d.generateStoriesFromRequirements(requirements, string(rawSpecContent))
			if err != nil {
				return StateError, fmt.Errorf("failed to generate stories from requirements: %w", err)
			}

			d.stateData["spec_id"] = specID
			d.stateData["story_ids"] = storyIDs
			d.stateData["stories_generated"] = true
			d.stateData["stories_count"] = len(storyIDs)

			d.logger.Info("🏗️ Story generation completed: %d stories generated and stored in database (spec ID: %s)", len(storyIDs), specID)
		} else {
			return StateError, fmt.Errorf("persistence channel not available - database storage is required for story generation")
		}
	}

	d.logger.Info("🏗️ Scoping completed using %s method, extracted %d requirements and generated %d stories",
		d.stateData["parsing_method"], len(requirements), d.stateData["stories_count"])

	return StateDispatching, nil
}

// parseSpecWithLLM uses the LLM to analyze the specification.
func (d *Driver) parseSpecWithLLM(ctx context.Context, rawSpecContent, specFile string) ([]Requirement, error) {
	// Check if renderer is available.
	if d.renderer == nil {
		return nil, fmt.Errorf("template renderer not available - falling back to deterministic parsing")
	}

	// LLM-first approach: send raw content directly to LLM.
	templateData := &templates.TemplateData{
		TaskContent: rawSpecContent,
		Extra: map[string]any{
			"spec_file_path": specFile,
			"mode":           "llm_analysis",
		},
	}

	prompt, err := d.renderer.RenderWithUserInstructions(templates.SpecAnalysisTemplate, templateData, d.workDir, "ARCHITECT")
	if err != nil {
		return nil, fmt.Errorf("failed to render spec analysis template: %w", err)
	}

	// Get LLM response.
	response, err := d.llmClient.GenerateResponse(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to get LLM response for spec parsing: %w", err)
	}

	// Add LLM response to context.
	d.contextManager.AddMessage("assistant", response)
	d.stateData["llm_analysis"] = response

	// Parse LLM response to extract requirements.
	return d.parseSpecAnalysisJSON(response)
}

// generateStoriesFromRequirements converts LLM-analyzed requirements into database stories.
func (d *Driver) generateStoriesFromRequirements(requirements []Requirement, specContent string) (string, []string, error) {
	// Generate spec ID and create spec record
	specID := persistence.GenerateSpecID()
	spec := &persistence.Spec{
		ID:        specID,
		Content:   specContent,
		CreatedAt: time.Now(),
	}

	// Store spec in database (fire-and-forget)
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil, // Fire-and-forget
	}

	// Convert requirements to database stories
	storyIDs := make([]string, 0, len(requirements))

	for i := range requirements {
		req := &requirements[i]
		// Generate story ID (8-char hex)
		storyID, err := persistence.GenerateStoryID()
		if err != nil {
			return "", nil, fmt.Errorf("failed to generate story ID: %w", err)
		}

		// Convert requirement to story with rich content
		story := d.requirementToStory(storyID, specID, req)

		// Store story in database (fire-and-forget)
		d.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpUpsertStory,
			Data:      story,
			Response:  nil, // Fire-and-forget
		}

		storyIDs = append(storyIDs, storyID)
	}

	// Handle dependencies between stories (simplified for now)
	d.processDependencies(requirements, storyIDs)

	// Mark spec as processed
	spec.ProcessedAt = &[]time.Time{time.Now()}[0]
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil, // Fire-and-forget
	}

	return specID, storyIDs, nil
}

// requirementToStory converts a LLM-analyzed requirement to a database story.
func (d *Driver) requirementToStory(storyID, specID string, req *Requirement) *persistence.Story {
	// Generate rich story content from LLM-analyzed requirement
	content := d.generateRichStoryContent(req)

	return &persistence.Story{
		ID:         storyID,
		SpecID:     specID,
		Title:      req.Title,
		Content:    content,
		Status:     persistence.StatusNew,
		Priority:   req.EstimatedPoints, // Use points as priority
		CreatedAt:  time.Now(),
		TokensUsed: 0,
		CostUSD:    0.0,
	}
}

// generateRichStoryContent creates detailed markdown content for a story from LLM-analyzed requirement.
func (d *Driver) generateRichStoryContent(req *Requirement) string {
	content := fmt.Sprintf("# %s\n\n", req.Title)

	// Add detailed description from LLM analysis
	if req.Description != "" {
		content += fmt.Sprintf("## Description\n%s\n\n", req.Description)
	}

	// Add acceptance criteria from LLM analysis or provide defaults
	if len(req.AcceptanceCriteria) > 0 {
		content += acceptanceCriteriaHeader
		for _, criterion := range req.AcceptanceCriteria {
			content += fmt.Sprintf("- %s\n", criterion)
		}
		content += "\n"
	} else {
		content += acceptanceCriteriaHeader
		content += "- Implementation completes successfully\n"
		content += "- All tests pass\n"
		content += "- Code follows project conventions\n\n"
	}

	// Add dependencies if any
	if len(req.Dependencies) > 0 {
		content += "## Dependencies\n"
		for _, dep := range req.Dependencies {
			content += fmt.Sprintf("- %s\n", dep)
		}
		content += "\n"
	}

	content += fmt.Sprintf("**Estimated Points:** %d\n", req.EstimatedPoints)

	return content
}

// processDependencies handles story dependencies by storing them in the database.
func (d *Driver) processDependencies(requirements []Requirement, storyIDs []string) {
	// For now, implement a simple dependency model where dependencies
	// are based on the order of requirements (earlier requirements are dependencies)
	// This could be enhanced to parse actual dependencies from LLM analysis
	for i := range requirements {
		req := &requirements[i]
		if len(req.Dependencies) == 0 {
			continue
		}

		storyID := storyIDs[i]

		// Simple implementation: add dependency to previous story
		for j := 0; j < i; j++ {
			dependsOnStoryID := storyIDs[j]

			dependency := &persistence.StoryDependency{
				StoryID:   storyID,
				DependsOn: dependsOnStoryID,
			}

			d.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpAddStoryDependency,
				Data:      dependency,
				Response:  nil, // Fire-and-forget
			}
		}
	}
}

// handleDispatching processes the dispatching phase (queue management and story assignment).
func (d *Driver) handleDispatching(ctx context.Context) (proto.State, error) {
	d.contextManager.AddMessage("assistant", "Dispatching phase: managing queue and assigning stories")

	// Initialize queue if not already done.
	if _, exists := d.stateData["queue_initialized"]; !exists {
		// Load stories from the database (or fallback to directory if no persistence).
		if err := d.queue.LoadFromDatabase(); err != nil {
			return StateError, fmt.Errorf("failed to load stories from database: %w", err)
		}

		// Detect cycles in dependencies.
		cycles := d.queue.DetectCycles()
		if len(cycles) > 0 {
			return StateError, fmt.Errorf("dependency cycles detected: %v", cycles)
		}

		// Persist queue state to JSON for monitoring.
		if err := d.persistQueueState(); err != nil {
			return StateError, fmt.Errorf("critical: failed to persist queue state: %w", err)
		}

		d.stateData["queue_initialized"] = true
		d.stateData["queue_management_completed_at"] = time.Now().UTC()

		// Get queue summary for logging.
		summary := d.queue.GetQueueSummary()
		d.logger.Info("queue loaded: %d stories (%d ready)",
			summary["total_stories"], summary["ready_stories"])
		d.stateData["queue_summary"] = summary
	}

	// Check if there are ready stories to dispatch.
	if story := d.queue.NextReadyStory(); story != nil {
		d.logger.Info("🏗️ Found ready story %s, dispatching to coder", story.ID)
		if err := d.dispatchReadyStory(ctx, story.ID); err != nil {
			// Dispatch failed - dispatcher already logged the details
			// Just note we'll retry later (story remains ready in queue)
			d.logger.Debug("🏗️ Story %s dispatch failed, will retry later", story.ID)
		} else {
			d.logger.Info("🏗️ Successfully dispatched story %s", story.ID)
		}
		// Transition to MONITORING after dispatch attempt (successful or not)
		return StateMonitoring, nil
	}

	// If no stories are ready and all are completed, we're done.
	if d.queue.AllStoriesCompleted() {
		d.logger.Info("all stories completed - transitioning to DONE")
		return StateDone, nil
	}

	// Otherwise, stay in DISPATCHING and wait for stories to become ready.
	return StateDispatching, nil
}

// handleMonitoring processes the monitoring phase (waiting for coder requests).
func (d *Driver) handleMonitoring(ctx context.Context) (proto.State, error) {
	d.contextManager.AddMessage("assistant", "Monitoring phase: waiting for coder requests and review completions")

	// Check if all stories are completed.
	if d.queue.AllStoriesCompleted() {
		d.logger.Info("🏗️ All stories completed, transitioning to DONE")
		return StateDone, nil
	}

	// In monitoring state, we wait for either:
	// 1. Coder questions/requests (transition to REQUEST).
	// 2. Heartbeat to check for new ready stories.
	select {
	case questionMsg, ok := <-d.questionsCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			d.logger.Info("🏗️ Questions channel closed in MONITORING, transitioning to ERROR")
			return StateError, fmt.Errorf("questions channel closed unexpectedly")
		}
		if questionMsg == nil {
			d.logger.Warn("🏗️ Received nil question message in MONITORING")
			return StateMonitoring, nil
		}
		d.logger.Info("🏗️ Architect received question in MONITORING state, transitioning to REQUEST")
		// Store the question for processing in REQUEST state.
		d.stateData["current_request"] = questionMsg
		return StateRequest, nil

	case <-time.After(HeartbeatInterval):
		// Heartbeat debug logging.
		d.logger.Debug("🏗️ Monitoring heartbeat: checking for ready stories")
		return StateMonitoring, nil

	case <-ctx.Done():
		return StateError, fmt.Errorf("architect dispatching cancelled: %w", ctx.Err())
	}
}

// handleRequest processes the request phase (handling coder requests).
func (d *Driver) handleRequest(ctx context.Context) (proto.State, error) {
	// Check for context cancellation first.
	select {
	case <-ctx.Done():
		d.logger.Info("🏗️ Request processing cancelled due to context cancellation")
		return StateError, fmt.Errorf("architect request processing cancelled: %w", ctx.Err())
	default:
	}

	d.contextManager.AddMessage("assistant", "Request phase: processing coder request")

	// Get the current request from state data.
	requestMsg, exists := d.stateData["current_request"].(*proto.AgentMsg)
	if !exists || requestMsg == nil {
		d.logger.Error("🏗️ No current request found in state data or request is nil")
		return StateError, fmt.Errorf("no current request found")
	}

	d.logger.Info("🏗️ Processing %s request %s from %s", requestMsg.Type, requestMsg.ID, requestMsg.FromAgent)

	// Persist request to database (fire-and-forget)
	if d.persistenceChannel != nil {
		agentRequest := &persistence.AgentRequest{
			ID:        requestMsg.ID,
			FromAgent: requestMsg.FromAgent,
			ToAgent:   requestMsg.ToAgent,
			CreatedAt: requestMsg.Timestamp,
		}

		// Extract story_id if present
		if storyID, exists := requestMsg.GetPayload("story_id"); exists {
			if storyIDStr, ok := storyID.(string); ok {
				agentRequest.StoryID = &storyIDStr
			}
		}

		// Set request type and content based on message type
		switch requestMsg.Type {
		case proto.MsgTypeQUESTION:
			agentRequest.RequestType = persistence.RequestTypeQuestion
			if content, exists := requestMsg.GetPayload("question"); exists {
				if contentStr, ok := content.(string); ok {
					agentRequest.Content = contentStr
				}
			}
			if reason, exists := requestMsg.GetPayload("reason"); exists {
				if reasonStr, ok := reason.(string); ok {
					agentRequest.Reason = &reasonStr
				}
			}
		case proto.MsgTypeREQUEST:
			agentRequest.RequestType = persistence.RequestTypeApproval
			if content, exists := requestMsg.GetPayload("content"); exists {
				if contentStr, ok := content.(string); ok {
					agentRequest.Content = contentStr
				}
			}
			if approvalType, exists := requestMsg.GetPayload("approval_type"); exists {
				if approvalTypeStr, ok := approvalType.(string); ok {
					agentRequest.ApprovalType = &approvalTypeStr
				}
			}
			if reason, exists := requestMsg.GetPayload("reason"); exists {
				if reasonStr, ok := reason.(string); ok {
					agentRequest.Reason = &reasonStr
				}
			}
		}

		// Set correlation ID if present
		if correlationID, exists := requestMsg.GetPayload("correlation_id"); exists {
			if correlationIDStr, ok := correlationID.(string); ok {
				agentRequest.CorrelationID = &correlationIDStr
			}
		}
		if correlationID, exists := requestMsg.GetPayload("question_id"); exists {
			if correlationIDStr, ok := correlationID.(string); ok {
				agentRequest.CorrelationID = &correlationIDStr
			}
		}
		if correlationID, exists := requestMsg.GetPayload("approval_id"); exists {
			if correlationIDStr, ok := correlationID.(string); ok {
				agentRequest.CorrelationID = &correlationIDStr
			}
		}

		// Set parent message ID
		if requestMsg.ParentMsgID != "" {
			agentRequest.ParentMsgID = &requestMsg.ParentMsgID
		}

		d.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpUpsertAgentRequest,
			Data:      agentRequest,
			Response:  nil, // Fire-and-forget
		}
	}

	// Process the request based on type.
	var response *proto.AgentMsg
	var err error

	switch requestMsg.Type {
	case proto.MsgTypeQUESTION:
		response, err = d.handleQuestionRequest(ctx, requestMsg)
	case proto.MsgTypeREQUEST:
		response, err = d.handleApprovalRequest(ctx, requestMsg)
	case proto.MsgTypeREQUEUE:
		err = d.handleRequeueRequest(ctx, requestMsg)
		// No response needed for requeue messages (fire-and-forget).
		response = nil
	default:
		d.logger.Error("🏗️ Unknown request type: %s", requestMsg.Type)
		return StateError, fmt.Errorf("unknown request type: %s", requestMsg.Type)
	}

	if err != nil {
		d.logger.Error("🏗️ Failed to process request %s: %v", requestMsg.ID, err)
		return StateError, err
	}

	// Send response back through dispatcher.
	if response != nil {
		if err := d.dispatcher.DispatchMessage(response); err != nil {
			d.logger.Error("🏗️ Failed to send response %s: %v", response.ID, err)
			return StateError, fmt.Errorf("failed to dispatch response %s: %w", response.ID, err)
		}

		// Persist response to database (fire-and-forget)
		if d.persistenceChannel != nil {
			agentResponse := &persistence.AgentResponse{
				ID:        response.ID,
				FromAgent: response.FromAgent,
				ToAgent:   response.ToAgent,
				CreatedAt: response.Timestamp,
			}

			// Set request ID for correlation
			agentResponse.RequestID = &requestMsg.ID

			// Extract story_id if present
			if storyID, exists := response.GetPayload("story_id"); exists {
				if storyIDStr, ok := storyID.(string); ok {
					agentResponse.StoryID = &storyIDStr
				}
			} else if storyID, exists := requestMsg.GetPayload("story_id"); exists {
				// Fallback to request message story_id
				if storyIDStr, ok := storyID.(string); ok {
					agentResponse.StoryID = &storyIDStr
				}
			}

			// Set response type and content based on message type
			switch response.Type {
			case proto.MsgTypeANSWER:
				agentResponse.ResponseType = persistence.ResponseTypeAnswer
				if content, exists := response.GetPayload("answer"); exists {
					if contentStr, ok := content.(string); ok {
						agentResponse.Content = contentStr
					}
				}
			case proto.MsgTypeRESULT:
				agentResponse.ResponseType = persistence.ResponseTypeResult

				// Extract approval_result struct if present
				if approvalResult, exists := response.GetPayload("approval_result"); exists {
					if result, ok := approvalResult.(*proto.ApprovalResult); ok {
						agentResponse.Content = result.Feedback
						statusStr := string(result.Status)
						agentResponse.Status = &statusStr
						agentResponse.Feedback = &result.Feedback
					}
				}

				// Fallback to individual fields if approval_result not found
				if agentResponse.Content == "" {
					if content, exists := response.GetPayload("content"); exists {
						if contentStr, ok := content.(string); ok {
							agentResponse.Content = contentStr
						}
					}
				}
				if agentResponse.Status == nil {
					if status, exists := response.GetPayload("status"); exists {
						if statusStr, ok := status.(string); ok {
							agentResponse.Status = &statusStr
						}
					}
				}
				if agentResponse.Feedback == nil {
					if feedback, exists := response.GetPayload("feedback"); exists {
						if feedbackStr, ok := feedback.(string); ok {
							agentResponse.Feedback = &feedbackStr
						}
					}
				}
			}

			// Set correlation ID if present
			if correlationID, exists := response.GetPayload("correlation_id"); exists {
				if correlationIDStr, ok := correlationID.(string); ok {
					agentResponse.CorrelationID = &correlationIDStr
				}
			}
			if correlationID, exists := response.GetPayload("question_id"); exists {
				if correlationIDStr, ok := correlationID.(string); ok {
					agentResponse.CorrelationID = &correlationIDStr
				}
			}
			if correlationID, exists := response.GetPayload("approval_id"); exists {
				if correlationIDStr, ok := correlationID.(string); ok {
					agentResponse.CorrelationID = &correlationIDStr
				}
			}

			d.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpUpsertAgentResponse,
				Data:      agentResponse,
				Response:  nil, // Fire-and-forget
			}
		}
		d.logger.Info("🏗️ Sent %s response %s to %s", response.Type, response.ID, response.ToAgent)
	}

	// Clear the processed request and return to monitoring.
	delete(d.stateData, "current_request")

	// Determine next state based on whether architect owns a spec.
	if d.ownsSpec() {
		return StateMonitoring, nil
	} else {
		return StateWaiting, nil
	}
}

// handleQuestionRequest processes a QUESTION message and returns an ANSWER.
func (d *Driver) handleQuestionRequest(ctx context.Context, questionMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	question, exists := questionMsg.GetPayload("question")
	if !exists {
		return nil, fmt.Errorf("no question payload in message")
	}

	d.logger.Info("🏗️ Processing question from %s", questionMsg.FromAgent)

	// For now, provide simple auto-response until LLM integration.
	answer := "Auto-response: Question received and acknowledged. Please proceed with your implementation."

	// If we have LLM client, use it for more intelligent responses.
	if d.llmClient != nil {
		llmResponse, err := d.llmClient.GenerateResponse(ctx, fmt.Sprintf("Answer this coding question: %v", question))
		if err != nil {
			d.logger.Warn("🏗️ LLM failed, using fallback answer: %v", err)
		} else {
			answer = llmResponse
		}
	}

	// Create ANSWER response.
	response := proto.NewAgentMsg(proto.MsgTypeANSWER, d.architectID, questionMsg.FromAgent)
	response.ParentMsgID = questionMsg.ID
	response.SetPayload("answer", answer)
	response.SetPayload("status", "answered")

	return response, nil
}

// handleApprovalRequest processes a REQUEST message and returns a RESULT.
func (d *Driver) handleApprovalRequest(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	requestType, _ := requestMsg.GetPayload("request_type")

	// Check if this is a merge request.
	if requestTypeStr, ok := requestType.(string); ok && requestTypeStr == "merge" {
		return d.handleMergeRequest(ctx, requestMsg)
	}

	// Handle regular approval requests.
	content, _ := requestMsg.GetPayload("content")
	approvalTypeStr, _ := requestMsg.GetPayload("approval_type")
	approvalID, _ := requestMsg.GetPayload("approval_id")

	// Convert interface{} to string with type assertion
	approvalTypeString := ""
	if approvalTypeStr != nil {
		approvalTypeString, _ = approvalTypeStr.(string)
	}

	approvalIDString := ""
	if approvalID != nil {
		approvalIDString, _ = approvalID.(string)
	}

	d.logger.Info("🏗️ Processing approval request: type=%v, approval_type=%v", requestType, approvalTypeString)

	// Parse approval type from request.
	approvalType, err := proto.ParseApprovalType(approvalTypeString)
	if err != nil {
		d.logger.Warn("🏗️ Invalid approval type %s, defaulting to plan", approvalTypeString)
		approvalType = proto.ApprovalTypePlan
	}

	// Persist plan to database if this is a plan approval request
	if approvalType == proto.ApprovalTypePlan && d.persistenceChannel != nil {
		if contentStr, ok := content.(string); ok {
			// Extract story_id
			var storyIDStr string
			if storyID, exists := requestMsg.GetPayload("story_id"); exists {
				if storyID, ok := storyID.(string); ok {
					storyIDStr = storyID
				}
			}

			// Extract confidence if present
			var confidenceStr *string
			if confidence, exists := requestMsg.GetPayload("confidence"); exists {
				if conf, ok := confidence.(string); ok {
					confidenceStr = &conf
				}
			}

			agentPlan := &persistence.AgentPlan{
				ID:         persistence.GenerateAgentPlanID(),
				StoryID:    storyIDStr,
				FromAgent:  requestMsg.FromAgent,
				Content:    contentStr,
				Confidence: confidenceStr,
				Status:     persistence.PlanStatusSubmitted,
				CreatedAt:  requestMsg.Timestamp,
			}

			d.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpUpsertAgentPlan,
				Data:      agentPlan,
				Response:  nil, // Fire-and-forget
			}
		}
	}

	// For now, auto-approve all requests until LLM integration.
	approved := true
	feedback := "Auto-approved: Request looks good, please proceed."

	// If we have LLM client, use it for more intelligent review.
	if d.llmClient != nil {
		var prompt string
		switch approvalType {
		case proto.ApprovalTypeCompletion:
			// Extract completion-specific data for better review.
			reason, _ := requestMsg.GetPayload("completion_reason")
			evidence, _ := requestMsg.GetPayload("completion_evidence")
			confidence, _ := requestMsg.GetPayload("completion_confidence")
			originalStory, _ := requestMsg.GetPayload("original_story")

			prompt = fmt.Sprintf(`Review this story completion claim:

ORIGINAL STORY:
%v

COMPLETION CLAIM:
- Reason: %v
- Evidence: %v  
- Confidence: %v

Please evaluate if the story requirements are truly satisfied based on the evidence provided. 
Respond with either "APPROVED: [brief reason]" or "REJECTED: [specific feedback on what's missing]".`,
				originalStory, reason, evidence, confidence)
		default:
			prompt = fmt.Sprintf("Review this request: %v", content)
		}

		llmResponse, err := d.llmClient.GenerateResponse(ctx, prompt)
		if err != nil {
			d.logger.Warn("🏗️ LLM failed, using fallback approval: %v", err)
		} else {
			feedback = llmResponse
			// For completion requests, parse LLM response to determine approval.
			if approvalType == proto.ApprovalTypeCompletion {
				if strings.Contains(strings.ToUpper(llmResponse), "REJECTED") {
					approved = false
				}
			}
			// For other types, always approve in LLM mode for now.
		}
	}

	// Plan approval completed - artifacts now tracked in database

	// Mark story as completed for approved completions.
	if approved && approvalType == proto.ApprovalTypeCompletion {
		// Extract story ID and mark as completed in queue.
		if storyIDPayload, exists := requestMsg.GetPayload(proto.KeyStoryID); exists {
			if storyIDStr, ok := storyIDPayload.(string); ok && storyIDStr != "" {
				if d.queue != nil {
					d.logger.Info("🏗️ Marking story %s as completed in queue", storyIDStr)
					if err := d.queue.MarkCompleted(storyIDStr); err != nil {
						d.logger.Warn("🏗️ Failed to mark story %s as completed: %v", storyIDStr, err)
					}
				} else {
					d.logger.Warn("🏗️ Queue is nil, cannot mark story %s as completed", storyIDStr)
				}
			} else {
				d.logger.Warn("🏗️ Story ID is not a string or is empty: %v", storyIDPayload)
			}
		} else {
			d.logger.Warn("🏗️ No story ID found in completion approval request")
		}
	}

	// Create proper ApprovalResult structure.
	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  approvalIDString,
		Type:       approvalType,
		Status:     proto.ApprovalStatusApproved,
		Feedback:   feedback,
		ReviewedBy: d.architectID,
		ReviewedAt: time.Now().UTC(),
	}

	if !approved {
		approvalResult.Status = proto.ApprovalStatusRejected
	}

	// Create RESULT response with proper approval_result payload.
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetPayload("approval_result", approvalResult)

	d.logger.Info("🏗️ Sending approval result: status=%s", approvalResult.Status)

	return response, nil
}

// handleRequeueRequest processes a REQUEUE message (fire-and-forget).
func (d *Driver) handleRequeueRequest(_ /* ctx */ context.Context, requeueMsg *proto.AgentMsg) error {
	storyID, _ := requeueMsg.GetPayload("story_id")
	reason, _ := requeueMsg.GetPayload("reason")

	storyIDStr, _ := storyID.(string)
	reasonStr, _ := reason.(string)

	d.logger.Info("🏗️ Processing story requeue request: story_id=%s, reason=%s, from=%s",
		storyIDStr, reasonStr, requeueMsg.FromAgent)

	if storyIDStr == "" {
		d.logger.Error("🏗️ Requeue request missing story_id")
		return fmt.Errorf("requeue request missing story_id")
	}

	// Load current queue state.
	if d.queue == nil {
		d.logger.Error("🏗️ No queue available for requeue")
		return fmt.Errorf("no queue available")
	}

	// Mark story as pending for reassignment.
	if err := d.queue.MarkPending(storyIDStr); err != nil {
		d.logger.Error("🏗️ Failed to requeue story %s: %v", storyIDStr, err)
		return fmt.Errorf("failed to requeue story %s: %w", storyIDStr, err)
	}

	// Log the requeue event - this will appear in the architect logs.
	d.logger.Info("🏗️ Story %s successfully requeued due to: %s (from agent %s)",
		storyIDStr, reasonStr, requeueMsg.FromAgent)

	return nil
}

// handleMergeRequest processes a merge REQUEST message and returns a RESULT.
func (d *Driver) handleMergeRequest(ctx context.Context, request *proto.AgentMsg) (*proto.AgentMsg, error) {
	prURL, _ := request.GetPayload("pr_url")
	branchName, _ := request.GetPayload("branch_name")
	storyID, _ := request.GetPayload("story_id")

	// Convert to strings safely.
	prURLStr, _ := prURL.(string)
	branchNameStr, _ := branchName.(string)
	storyIDStr, _ := storyID.(string)

	d.logger.Info("🏗️ Processing merge request for story %s, PR: %s, branch: %s", storyIDStr, prURLStr, branchNameStr)

	// Attempt merge using GitHub CLI.
	mergeResult, err := d.attemptPRMerge(ctx, prURLStr, branchNameStr, storyIDStr)

	// Create RESULT response.
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, request.FromAgent)
	resultMsg.ParentMsgID = request.ID

	if err != nil || (mergeResult != nil && mergeResult.HasConflicts) {
		if err != nil {
			d.logger.Info("🏗️ Merge failed with error for story %s: %v", storyIDStr, err)
			resultMsg.SetPayload("status", "merge_error")
			resultMsg.SetPayload("error_details", err.Error())
		} else {
			d.logger.Info("🏗️ Merge failed with conflicts for story %s", storyIDStr)
			resultMsg.SetPayload("status", "merge_conflict")
			resultMsg.SetPayload("conflict_details", mergeResult.ConflictInfo)
		}
	} else {
		d.logger.Info("🏗️ Merge successful for story %s, commit: %s", storyIDStr, mergeResult.CommitSHA)
		resultMsg.SetPayload("status", "merged")
		resultMsg.SetPayload("merge_commit", mergeResult.CommitSHA)

		// Mark story as completed in queue.
		if d.queue != nil {
			if err := d.queue.MarkCompleted(storyIDStr); err != nil {
				d.logger.Warn("🏗️ Failed to mark story %s as completed: %v", storyIDStr, err)
			}
		}
	}

	return resultMsg, nil
}

// MergeAttemptResult represents the result of a merge attempt.
//
//nolint:govet // Simple result struct, logical grouping preferred
type MergeAttemptResult struct {
	HasConflicts bool
	ConflictInfo string
	CommitSHA    string
}

// attemptPRMerge attempts to merge a PR using GitHub CLI.
func (d *Driver) attemptPRMerge(ctx context.Context, prURL, branchName, storyID string) (*MergeAttemptResult, error) {
	d.logger.Info("🏗️ Attempting to merge PR: %s, branch: %s", prURL, branchName)

	// Use gh CLI to merge PR with squash strategy and branch deletion.
	mergeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Check if gh is available.
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh (GitHub CLI) is not available in PATH: %w", err)
	}

	// If no PR URL provided, use branch name to find or create the PR.
	var cmd *exec.Cmd
	var output []byte
	var err error

	if prURL == "" || prURL == " " {
		if branchName == "" {
			return nil, fmt.Errorf("no PR URL or branch name provided for merge")
		}
		d.logger.Info("🏗️ No PR URL provided, checking for existing PR for branch: %s", branchName)

		// First, try to find an existing PR for this branch.
		listCmd := exec.CommandContext(mergeCtx, "gh", "pr", "list", "--head", branchName, "--json", "number,url")
		listOutput, listErr := listCmd.CombinedOutput()

		if listErr == nil && len(listOutput) > 0 && string(listOutput) != "[]" {
			// Found existing PR, try to merge it.
			d.logger.Info("🏗️ Found existing PR for branch %s, attempting merge", branchName)
			cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", branchName, "--squash", "--delete-branch")
			output, err = cmd.CombinedOutput()
		} else {
			// No PR found, create one first then merge.
			d.logger.Info("🏗️ No existing PR found for branch %s, creating PR first", branchName)

			// Create PR.
			createCmd := exec.CommandContext(mergeCtx, "gh", "pr", "create",
				"--title", fmt.Sprintf("Story merge: %s", storyID),
				"--body", fmt.Sprintf("Automated merge for story %s", storyID),
				"--base", "main",
				"--head", branchName)
			createOutput, createErr := createCmd.CombinedOutput()

			if createErr != nil {
				return nil, fmt.Errorf("failed to create PR for branch %s: %w\nOutput: %s", branchName, createErr, string(createOutput))
			}

			d.logger.Info("🏗️ Created PR for branch %s, now attempting merge", branchName)
			// Now try to merge the newly created PR.
			cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", branchName, "--squash", "--delete-branch")
			output, err = cmd.CombinedOutput()
		}
	} else {
		cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", prURL, "--squash", "--delete-branch")
		output, err = cmd.CombinedOutput()
	}

	result := &MergeAttemptResult{}

	if err != nil {
		// Check if error is due to merge conflicts.
		outputStr := strings.ToLower(string(output))
		if strings.Contains(outputStr, "conflict") || strings.Contains(outputStr, "merge conflict") {
			mergeTarget := prURL
			if mergeTarget == "" {
				mergeTarget = branchName
			}
			d.logger.Info("🏗️ Merge conflicts detected for %s", mergeTarget)
			result.HasConflicts = true
			result.ConflictInfo = string(output)
			return result, nil // Not an error, just conflicts
		}

		// Other error (permissions, network, etc.).
		mergeTarget := prURL
		if mergeTarget == "" {
			mergeTarget = branchName
		}
		d.logger.Error("🏗️ Failed to merge %s: %v\nOutput: %s", mergeTarget, err, string(output))
		return nil, fmt.Errorf("gh pr merge failed: %w\nOutput: %s", err, string(output))
	}

	// Success - extract commit SHA from output if available.
	outputStr := string(output)
	mergeTarget := prURL
	if mergeTarget == "" {
		mergeTarget = branchName
	}
	d.logger.Info("🏗️ Merge successful for %s\nOutput: %s", mergeTarget, outputStr)

	// TODO: Parse commit SHA from gh output if needed
	result.CommitSHA = "merged" // Placeholder until we parse actual SHA

	return result, nil
}

// handleEscalated processes the escalated phase (waiting for human intervention).
func (d *Driver) handleEscalated(ctx context.Context) (proto.State, error) {
	d.contextManager.AddMessage("assistant", "Escalated phase: waiting for human intervention")

	// Check escalation timeout (2 hours).
	if escalatedAt, exists := d.stateData["escalated_at"].(time.Time); exists {
		timeSinceEscalation := time.Since(escalatedAt)
		if timeSinceEscalation > EscalationTimeout {
			d.logger.Warn("escalation timeout exceeded (%v > %v), sending ABANDON review and re-queuing",
				timeSinceEscalation.Truncate(time.Minute), EscalationTimeout)

			// Log timeout event for monitoring.
			if d.escalationHandler != nil {
				if logErr := d.escalationHandler.LogTimeout(escalatedAt, timeSinceEscalation); logErr != nil {
					d.logger.Error("Failed to log timeout event: %v", logErr)
				}
			}

			// Send ABANDON review and re-queue story.
			if err := d.sendAbandonAndRequeue(ctx); err != nil {
				d.logger.Error("failed to send ABANDON review and re-queue: %v", err)
				return StateError, fmt.Errorf("failed to handle escalation timeout: %w", err)
			}

			return StateDispatching, nil
		}

		// Log remaining time periodically (every hour in actual usage, but for demo we'll be more verbose).
		timeRemaining := EscalationTimeout - timeSinceEscalation
		d.logger.Debug("escalation timeout: %v remaining (escalated %v ago)",
			timeRemaining.Truncate(time.Minute), timeSinceEscalation.Truncate(time.Minute))
	} else {
		// If we don't have an escalation timestamp, this is an error - we should always record when we escalate.
		d.logger.Warn("in ESCALATED state but no escalation timestamp found")
		return StateError, fmt.Errorf("invalid escalated state: no escalation timestamp")
	}

	// Check for pending escalations.
	if d.escalationHandler != nil {
		summary := d.escalationHandler.GetEscalationSummary()
		if summary.PendingEscalations > 0 {
			// Still have pending escalations, stay in escalated state.
			return StateEscalated, nil
		}
		// No more pending escalations, return to request handling.
		return StateRequest, nil
	}

	// No escalation handler, return to request.
	return StateRequest, nil
}

// handleMerging processes the merging phase (merging approved code).
func (d *Driver) handleMerging(_ context.Context) (proto.State, error) {
	d.contextManager.AddMessage("assistant", "Merging phase: processing completed stories")

	// TODO: Implement proper merging logic without RequestWorker
	// For now, immediately return to dispatching to check for new ready stories.
	d.logger.Info("🏗️ Merging completed, returning to dispatching")
	return StateDispatching, nil
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

// formatContextAsString formats the context messages as a string for LLM prompts.

// convertToRequirements converts state data back to Requirements slice.
func (d *Driver) convertToRequirements(data any) ([]Requirement, error) {
	// Handle slice of Requirement structs (from spec parser).
	if reqs, ok := data.([]Requirement); ok {
		return reqs, nil
	}

	// Handle slice of maps (from mock or legacy data).
	if reqMaps, ok := data.([]map[string]any); ok {
		var requirements []Requirement
		for _, reqMap := range reqMaps {
			req := Requirement{}

			if title, ok := reqMap["title"].(string); ok {
				req.Title = title
			}
			if desc, ok := reqMap["description"].(string); ok {
				req.Description = desc
			}
			if points, ok := reqMap["estimated_points"].(int); ok {
				req.EstimatedPoints = points
			}

			// Handle acceptance criteria.
			if criteria, ok := reqMap["acceptance_criteria"]; ok {
				if criteriaSlice, ok := criteria.([]string); ok {
					req.AcceptanceCriteria = criteriaSlice
				}
			}

			requirements = append(requirements, req)
		}
		return requirements, nil
	}

	return nil, fmt.Errorf("unsupported requirements data type: %T", data)
}

// parseSpecAnalysisJSON parses the LLM's JSON response to extract requirements.
func (d *Driver) parseSpecAnalysisJSON(response string) ([]Requirement, error) {
	// Try to extract JSON from the response.
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON found in LLM response")
	}

	jsonStr := response[jsonStart : jsonEnd+1]

	// Define the expected LLM response structure.
	//nolint:govet // JSON parsing struct, field order must match expected JSON
	var llmResponse struct {
		Analysis string `json:"analysis"`
		//nolint:govet // JSON parsing struct, field order must match expected JSON
		Requirements []struct {
			Title              string   `json:"title"`
			Description        string   `json:"description"`
			AcceptanceCriteria []string `json:"acceptance_criteria"`
			EstimatedPoints    int      `json:"estimated_points"`
			Dependencies       []string `json:"dependencies,omitempty"`
		} `json:"requirements"`
		NextAction string `json:"next_action"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &llmResponse); err != nil {
		// Enhanced error reporting with truncation detection
		baseErr := fmt.Errorf("failed to parse LLM JSON response: %w", err)

		// Check if this might be a truncation issue by comparing response length to token limits
		// Using tiktoken to get accurate token count for O3 model (approximated with GPT-4 encoding)
		responseTokens := utils.CountTokensSimple(response)
		maxTokens := agent.ArchitectMaxTokens // Current MaxTokens limit from LLMClientAdapter

		// If we're within 10% of the token limit, likely truncation
		if float64(responseTokens) >= float64(maxTokens)*0.9 {
			d.logger.Error("🏗️ JSON parsing failed - likely due to response truncation")
			d.logger.Error("🏗️ Response tokens: %d, MaxTokens limit: %d (%.1f%% of limit)",
				responseTokens, maxTokens, float64(responseTokens)/float64(maxTokens)*100)
			d.logger.Error("🏗️ Consider increasing MaxTokens in LLMClientAdapter")
			d.logger.Error("🏗️ Response length: %d characters", len(response))
			if len(response) > 1000 {
				d.logger.Error("🏗️ Response end (last 500 chars): ...%s", response[len(response)-500:])
			} else {
				d.logger.Error("🏗️ Full response: %s", response)
			}
			return nil, fmt.Errorf("JSON parsing failed - likely truncated due to token limit (%d tokens, %.1f%% of %d limit): %w",
				responseTokens, float64(responseTokens)/float64(maxTokens)*100, maxTokens, err)
		}

		// Not a truncation issue, provide standard error with response details
		d.logger.Error("🏗️ JSON parsing failed - response tokens: %d, limit: %d (%.1f%%)",
			responseTokens, maxTokens, float64(responseTokens)/float64(maxTokens)*100)
		if len(response) > 2000 {
			d.logger.Error("🏗️ Response preview (first 1000 chars): %s...", response[:1000])
			d.logger.Error("🏗️ Response preview (last 1000 chars): ...%s", response[len(response)-1000:])
		} else {
			d.logger.Error("🏗️ Full response: %s", response)
		}

		return nil, baseErr
	}

	// Convert to internal Requirement format.
	requirements := make([]Requirement, 0, len(llmResponse.Requirements))
	for i := range llmResponse.Requirements {
		req := &llmResponse.Requirements[i]
		requirement := Requirement{
			Title:              req.Title,
			Description:        req.Description,
			AcceptanceCriteria: req.AcceptanceCriteria,
			EstimatedPoints:    req.EstimatedPoints,
			Dependencies:       req.Dependencies,
		}

		// Validate and set reasonable defaults.
		if requirement.EstimatedPoints < 1 || requirement.EstimatedPoints > 5 {
			requirement.EstimatedPoints = 2 // Default to medium complexity
		}

		if requirement.Title == "" {
			continue // Skip empty requirements
		}

		if len(requirement.AcceptanceCriteria) == 0 {
			requirement.AcceptanceCriteria = []string{
				"Implementation completes successfully",
				"All tests pass",
				"Code follows project conventions",
			}
		}

		requirements = append(requirements, requirement)
	}

	if len(requirements) == 0 {
		return nil, fmt.Errorf("no valid requirements extracted from LLM response")
	}

	return requirements, nil
}

// persistQueueState saves the current queue state to the state store.
func (d *Driver) persistQueueState() error {
	queueData, err := d.queue.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize queue: %w", err)
	}

	// Store queue data in state data for persistence.
	d.stateData["queue_json"] = string(queueData)

	return nil
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

// dispatchReadyStory assigns a ready story to an available agent.
func (d *Driver) dispatchReadyStory(ctx context.Context, storyID string) error {
	d.logger.Info("🏗️ Dispatching ready story %s", storyID)

	// Get the story from queue.
	story, exists := d.queue.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	if story.Status != StatusPending {
		return fmt.Errorf("story %s is not in pending status (current: %s)", storyID, story.Status)
	}

	// Send to dispatcher via story message.
	d.logger.Info("🏗️ Sending story %s to dispatcher", storyID)

	return d.sendStoryToDispatcher(ctx, storyID)
}

// sendStoryToDispatcher sends a story to the dispatcher.
func (d *Driver) sendStoryToDispatcher(_ context.Context, storyID string) error {
	d.logger.Info("🏗️ Sending story %s to dispatcher", storyID)

	// Create story message for the dispatcher ("coder" targets any available coder).
	storyMsg := proto.NewAgentMsg(proto.MsgTypeSTORY, d.architectID, "coder")
	storyMsg.SetPayload(proto.KeyStoryID, storyID)
	storyMsg.SetPayload("story_type", "implement_story")

	d.logger.Info("🏗️ Created STORY message %s for story %s -> dispatcher", storyMsg.ID, storyID)

	// Get story details.
	if story, exists := d.queue.stories[storyID]; exists {
		storyMsg.SetPayload(proto.KeyTitle, story.Title)
		storyMsg.SetPayload(proto.KeyFilePath, story.FilePath)
		storyMsg.SetPayload(proto.KeyEstimatedPoints, story.EstimatedPoints)
		storyMsg.SetPayload(proto.KeyDependsOn, story.DependsOn)

		// Read and parse story content for the coder.
		if content, requirements, err := d.parseStoryContent(story.FilePath); err == nil {
			storyMsg.SetPayload(proto.KeyContent, content)
			storyMsg.SetPayload(proto.KeyRequirements, requirements)

			// Detect backend from story content and requirements.
			backend := d.detectBackend(storyID, content, requirements)
			storyMsg.SetPayload(proto.KeyBackend, backend)
			d.logger.Info("🏗️ Detected backend '%s' for story %s", backend, storyID)
		} else {
			// Fallback to title if content parsing fails.
			storyMsg.SetPayload(proto.KeyContent, story.Title)
			storyMsg.SetPayload(proto.KeyRequirements, []string{})

			// Default backend detection from title.
			backend := d.detectBackend(storyID, story.Title, []string{})
			storyMsg.SetPayload(proto.KeyBackend, backend)
			d.logger.Info("🏗️ Detected backend '%s' for story %s (from title)", backend, storyID)
		}
	}

	// Send story to dispatcher.
	d.logger.Info("🏗️ Sending STORY message %s to dispatcher", storyMsg.ID)

	if err := d.dispatcher.DispatchMessage(storyMsg); err != nil {
		d.logger.Error("🏗️ Failed to dispatch STORY message %s: %v", storyMsg.ID, err)
		return fmt.Errorf("failed to dispatch STORY message %s: %w", storyMsg.ID, err)
	}

	// Only mark story as dispatched AFTER successful channel send.
	if err := d.queue.MarkInProgress(storyID, "dispatcher"); err != nil {
		return fmt.Errorf("failed to mark story as dispatched: %w", err)
	}

	d.logger.Info("🏗️ Successfully dispatched STORY message %s to dispatcher", storyMsg.ID)
	return nil
}

// sendAbandonAndRequeue sends an ABANDON review response and re-queues the story.
func (d *Driver) sendAbandonAndRequeue(_ context.Context) error {
	// Get the escalated story ID from escalation handler.
	if d.escalationHandler == nil {
		return fmt.Errorf("no escalation handler available")
	}

	summary := d.escalationHandler.GetEscalationSummary()
	if len(summary.Escalations) == 0 {
		return fmt.Errorf("no escalations found to abandon")
	}

	// Find the most recent pending escalation.
	var latestEscalation *EscalationEntry
	for _, escalation := range summary.Escalations {
		if escalation.Status == string(StatusPending) {
			if latestEscalation == nil || escalation.EscalatedAt.After(latestEscalation.EscalatedAt) {
				latestEscalation = escalation
			}
		}
	}

	if latestEscalation == nil {
		return fmt.Errorf("no pending escalations found to abandon")
	}

	storyID := latestEscalation.StoryID
	agentID := latestEscalation.AgentID

	// Create ABANDON review message.
	abandonMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, agentID)
	abandonMsg.SetPayload("story_id", storyID)
	abandonMsg.SetPayload("review_result", "ABANDON")
	abandonMsg.SetPayload("review_notes", "Escalation timeout exceeded - abandoning current submission")
	abandonMsg.SetPayload("reviewed_at", time.Now().UTC().Format(time.RFC3339))
	abandonMsg.SetPayload("timeout_reason", "escalation_timeout")

	// Send via dispatcher.
	if err := d.dispatcher.DispatchMessage(abandonMsg); err != nil {
		return fmt.Errorf("failed to send ABANDON message: %w", err)
	}

	// Re-queue the story by resetting it to pending status.
	story, exists := d.queue.GetStory(storyID)
	if !exists {
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	// Reset to pending status so it can be picked up again.
	story.Status = StatusPending
	story.AssignedAgent = ""
	story.StartedAt = nil
	story.CompletedAt = nil
	story.LastUpdated = time.Now().UTC()

	// Trigger ready notification if dependencies are met.
	d.queue.checkAndNotifyReady()

	d.logger.Info("abandoned story %s due to escalation timeout and re-queued", storyID)
	return nil
}

// parseStoryContent reads a story file and extracts content and requirements for the coder.
func (d *Driver) parseStoryContent(filePath string) (string, []string, error) {
	// Read the story file.
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read story file %s: %w", filePath, err)
	}

	content := string(fileBytes)

	// Skip YAML frontmatter (everything before the second ---).
	lines := strings.Split(content, "\n")
	contentStart := 0
	dashCount := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			dashCount++
			if dashCount == 2 {
				contentStart = i + 1
				break
			}
		}
	}

	if contentStart >= len(lines) {
		return "", nil, fmt.Errorf("no content found after YAML frontmatter in %s", filePath)
	}

	// Get content after frontmatter.
	contentLines := lines[contentStart:]
	storyContent := strings.Join(contentLines, "\n")

	// Extract Story description (everything after **Story** until **Acceptance Criteria**).
	storyStart := strings.Index(storyContent, "**Story**")
	criteriaStart := strings.Index(storyContent, "**Acceptance Criteria**")

	var storyDescription string
	if storyStart != -1 && criteriaStart != -1 {
		storyDescription = strings.TrimSpace(storyContent[storyStart+9 : criteriaStart])
	} else if storyStart != -1 {
		storyDescription = strings.TrimSpace(storyContent[storyStart+9:])
	} else {
		// Fallback: use first paragraph.
		paragraphs := strings.Split(strings.TrimSpace(storyContent), "\n\n")
		if len(paragraphs) > 0 {
			storyDescription = strings.TrimSpace(paragraphs[0])
		}
	}

	// Extract requirements from Acceptance Criteria bullets.
	var requirements []string
	if criteriaStart != -1 {
		criteriaSection := storyContent[criteriaStart+23:] // Skip "**Acceptance Criteria**"
		lines := strings.Split(criteriaSection, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "*") || strings.HasPrefix(line, "-") {
				// Remove bullet point marker and clean up.
				requirement := strings.TrimSpace(line[1:])
				if requirement != "" {
					requirements = append(requirements, requirement)
				}
			}
		}
	}

	return storyDescription, requirements, nil
}

// detectBackend analyzes story content and requirements to determine the appropriate backend.
func (d *Driver) detectBackend(storyID, content string, requirements []string) string {
	// Convert content to lowercase for case-insensitive matching.
	contentLower := strings.ToLower(content)

	// Convert requirements to lowercase for case-insensitive matching.
	requirementsLower := make([]string, len(requirements))
	for i, req := range requirements {
		requirementsLower[i] = strings.ToLower(req)
	}

	// Check content for backend indicators.
	if containsBackendKeywords(contentLower, []string{
		"go", "golang", "go.mod", "go.sum", "main.go", "package main",
		"func main", "import \"", "go build", "go test", "go run",
	}) {
		return "go"
	}

	if containsBackendKeywords(contentLower, []string{
		"python", "pip", "requirements.txt", "setup.py", "pyproject.toml",
		"def ", "import ", "from ", "python3", "venv", "virtualenv", "uv",
	}) {
		return buildSystemPython
	}

	if containsBackendKeywords(contentLower, []string{
		"javascript", "typescript", "node", "npm", "package.json", "yarn",
		"pnpm", "bun", "const ", "let ", "var ", "function", "=>", "nodejs",
	}) {
		return buildSystemNode
	}

	if containsBackendKeywords(contentLower, []string{
		"makefile", "gcc", "clang", "c++", "cpp",
	}) || strings.Contains(contentLower, " make ") || strings.HasPrefix(contentLower, "make ") || strings.HasSuffix(contentLower, " make") || strings.Contains(contentLower, " c ") {
		return buildSystemMake
	}

	// Check requirements for backend indicators.
	for _, req := range requirementsLower {
		if containsBackendKeywords(req, []string{
			"go", "golang", "go.mod", "go.sum", "main.go", "package main",
		}) {
			return "go"
		}

		if containsBackendKeywords(req, []string{
			"python", "pip", "requirements.txt", "setup.py", "pyproject.toml",
		}) {
			return buildSystemPython
		}

		if containsBackendKeywords(req, []string{
			"javascript", "typescript", "node", "npm", "package.json", "yarn",
		}) {
			return buildSystemNode
		}

		if containsBackendKeywords(req, []string{
			"makefile", "gcc", "clang",
		}) || strings.Contains(req, " make ") || strings.HasPrefix(req, "make ") || strings.HasSuffix(req, " make") {
			return buildSystemMake
		}
	}

	// Default to null backend if no specific backend detected.
	d.logger.Info("🏗️ No specific backend detected for story %s, using null backend", storyID)
	return "null"
}

// containsBackendKeywords checks if text contains any of the given keywords.
func containsBackendKeywords(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// getSpecFileFromMessage extracts the spec file path from the stored SPEC message.
func (d *Driver) getSpecFileFromMessage() string {
	// Get the stored spec message.
	specMsgData, exists := d.stateData["spec_message"]
	if !exists {
		d.logger.Error("🏗️ No spec_message found in state data")
		return ""
	}

	// Cast to AgentMsg.
	specMsg, ok := specMsgData.(*proto.AgentMsg)
	if !ok {
		d.logger.Error("🏗️ spec_message is not an AgentMsg: %T", specMsgData)
		return ""
	}

	// Debug: log all payload keys.
	payloadKeys := make([]string, 0)
	for key := range specMsg.Payload {
		payloadKeys = append(payloadKeys, key)
	}
	d.logger.Info("🏗️ SPEC message payload keys: %v", payloadKeys)

	// Check if we have spec_content (bootstrap mode) - no file needed
	if _, hasContent := specMsg.GetPayload("spec_content"); hasContent {
		d.logger.Info("🏗️ Bootstrap mode detected - using spec_content")
		return "<bootstrap-content>" // Return placeholder since actual content is handled elsewhere
	}

	// Extract spec file path from payload - try different keys.
	specFile, exists := specMsg.GetPayload("spec_file")
	if !exists {
		// Try alternative keys.
		specFile, exists = specMsg.GetPayload("file_path")
		if !exists {
			specFile, exists = specMsg.GetPayload("filepath")
			if !exists {
				d.logger.Error("🏗️ No spec file path found in payload with keys: %v", payloadKeys)
				return ""
			}
		}
	}

	// Convert to string.
	if specFileStr, ok := specFile.(string); ok {
		d.logger.Info("🏗️ Found spec file path: %s", specFileStr)
		return specFileStr
	}

	d.logger.Error("🏗️ Spec file path is not a string: %T = %v", specFile, specFile)
	return ""
}
