package pm

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	chatmiddleware "orchestrator/pkg/agent/middleware/chat"
	"orchestrator/pkg/chat"
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

// ToolProvider is an interface for tool access (allows testing with mocks).
type ToolProvider interface {
	Get(name string) (tools.Tool, error)
	List() []tools.ToolMeta
}

const (
	// DefaultExpertise is the default expertise level if none is specified.
	DefaultExpertise = "BASIC"
)

// ChatServiceInterface defines the chat operations needed by PM.
type ChatServiceInterface interface {
	HaveNewMessages(agentID string) bool
	GetNew(ctx context.Context, req *chat.GetNewRequest) (*chat.GetNewResponse, error)
}

// Driver implements the PM (Product Manager) agent.
// PM conducts interviews with users to generate high-quality specifications.
//
//nolint:govet // Prefer logical grouping over memory optimization
type Driver struct {
	pmID                string
	llmClient           agent.LLMClient
	renderer            *templates.Renderer
	contextManager      *contextmgr.ContextManager
	logger              *logx.Logger
	dispatcher          *dispatch.Dispatcher
	persistenceChannel  chan<- *persistence.Request
	chatService         ChatServiceInterface // Chat service for polling new messages
	currentState        proto.State
	stateData           map[string]any
	interviewRequestCh  <-chan *proto.AgentMsg
	executor            *execpkg.ArchitectExecutor // PM uses same read-only executor as architect
	workDir             string
	specCh              <-chan *proto.AgentMsg // Receives spec requests (file uploads)
	replyCh             <-chan *proto.AgentMsg // Receives RESULT messages from architect
	stateNotificationCh chan<- *proto.StateChangeNotification
	toolProvider        ToolProvider // Tool provider for spec_submit tool
}

// NewPM creates a new PM agent with all dependencies initialized.
// This is the main constructor used by the agent factory.
func NewPM(
	ctx context.Context,
	pmID string,
	dispatcher *dispatch.Dispatcher,
	workDir string,
	persistenceChannel chan<- *persistence.Request,
	llmFactory *agent.LLMClientFactory,
	interviewRequestCh <-chan *proto.AgentMsg,
	chatService ChatServiceInterface,
) (*Driver, error) {
	// Check for context cancellation
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("PM construction cancelled: %w", ctx.Err())
	default:
	}

	// Get model name from config
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	modelName := cfg.Agents.PMModel

	// Create LLM client from shared factory
	llmClient, err := llmFactory.CreateClient(agent.TypePM)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client for PM: %w", err)
	}

	// Wrap with chat injection middleware
	logger := logx.NewLogger("pm")
	llmClient = chatmiddleware.WrapWithChatInjection(llmClient, chatService, pmID, logger)

	// Create template renderer
	renderer, err := templates.NewRenderer()
	if err != nil {
		return nil, fmt.Errorf("failed to create template renderer: %w", err)
	}

	// Create context manager with PM model
	contextManager := contextmgr.NewContextManagerWithModel(modelName)

	// Ensure PM workspace exists (pm-001/ read-only clone)
	pmWorkspace, workspaceErr := workspace.EnsurePMWorkspace(ctx, workDir)
	if workspaceErr != nil {
		return nil, fmt.Errorf("failed to ensure PM workspace: %w", workspaceErr)
	}
	logger.Info("PM workspace ready at: %s", pmWorkspace)

	// Create and start PM container executor (same as architect - read-only tools)
	// PM uses the same ArchitectExecutor for read-only file access
	pmExecutor := execpkg.NewArchitectExecutor(
		config.BootstrapContainerTag, // Use bootstrap image
		workDir,                      // Project directory
		cfg.Agents.MaxCoders,         // Number of coder workspaces to mount
	)

	// Start the PM container (one retry on failure)
	if startErr := pmExecutor.Start(ctx); startErr != nil {
		logger.Warn("Failed to start PM container, retrying once: %v", startErr)
		// Retry once
		if retryErr := pmExecutor.Start(ctx); retryErr != nil {
			return nil, fmt.Errorf("failed to start PM container after retry: %w", retryErr)
		}
	}

	// Create tool provider with all PM tools (read_file, list_files, chat_post, spec_submit)
	agentCtx := tools.AgentContext{
		Executor:        pmExecutor,
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         pmWorkspace,
	}
	toolProvider := tools.NewProvider(agentCtx, tools.PMTools)

	return &Driver{
		pmID:               pmID,
		llmClient:          llmClient,
		renderer:           renderer,
		contextManager:     contextManager,
		logger:             logger, // Use logger created above
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		chatService:        chatService,
		currentState:       StateWaiting,
		stateData:          make(map[string]any),
		interviewRequestCh: interviewRequestCh,
		executor:           pmExecutor,
		workDir:            workDir,
		toolProvider:       toolProvider,
	}, nil
}

// NewDriver creates a new PM agent driver with provided dependencies.
// This is primarily for testing. Production code should use NewPM.
func NewDriver(
	pmID string,
	llmClient agent.LLMClient,
	renderer *templates.Renderer,
	contextManager *contextmgr.ContextManager,
	dispatcher *dispatch.Dispatcher,
	persistenceChannel chan<- *persistence.Request,
	interviewRequestCh <-chan *proto.AgentMsg,
	executor *execpkg.ArchitectExecutor, // PM uses same read-only executor as architect
	workDir string,
) *Driver {
	return &Driver{
		pmID:               pmID,
		llmClient:          llmClient,
		renderer:           renderer,
		contextManager:     contextManager,
		logger:             logx.NewLogger("pm"),
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		currentState:       StateWaiting,
		stateData:          make(map[string]any),
		interviewRequestCh: interviewRequestCh,
		executor:           executor,
		workDir:            workDir,
	}
}

// Run starts the PM agent's main loop.
func (d *Driver) Run(ctx context.Context) error {
	d.logger.Info("🎯 PM agent %s starting", d.pmID)

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("🎯 PM agent %s received shutdown signal", d.pmID)
			return fmt.Errorf("pm agent shutdown: %w", ctx.Err())
		default:
			// Execute current state
			nextState, err := d.executeState(ctx)
			if err != nil {
				d.logger.Error("❌ PM agent state machine failed: %v", err)
				nextState = proto.StateError
			}

			// Validate transition
			if !IsValidPMTransition(d.currentState, nextState) {
				d.logger.Error("❌ Invalid PM state transition: %s → %s", d.currentState, nextState)
				nextState = proto.StateError
			}

			// Transition to next state
			if d.currentState != nextState {
				d.logger.Info("🔄 PM state transition: %s → %s", d.currentState, nextState)
				d.currentState = nextState
				// Supervisor polls state changes via dispatcher
			}

			// Handle terminal states
			switch nextState {
			case proto.StateDone:
				d.logger.Info("✅ PM agent %s shutting down", d.pmID)
				return nil
			case proto.StateError:
				d.logger.Error("⚠️  PM agent %s in ERROR state, resetting to WAITING", d.pmID)
				// Reset to WAITING after error
				d.currentState = StateWaiting
				d.stateData = make(map[string]any)
			}
		}
	}
}

// executeState executes the current state and returns the next state.
func (d *Driver) executeState(ctx context.Context) (proto.State, error) {
	switch d.currentState {
	case StateWaiting:
		return d.handleWaiting(ctx)
	case StateWorking:
		return d.handleWorking(ctx)
	default:
		return proto.StateError, fmt.Errorf("unknown state: %s", d.currentState)
	}
}

// handleWaiting blocks until an interview request, spec file, or RESULT message arrives.
func (d *Driver) handleWaiting(ctx context.Context) (proto.State, error) {
	// Check if we're waiting for RESULT from architect
	pendingRequestID, hasPendingRequest := d.stateData["pending_request_id"].(string)

	if hasPendingRequest {
		d.logger.Info("🎯 PM waiting for RESULT from architect (request_id: %s)", pendingRequestID)
	} else {
		d.logger.Info("🎯 PM waiting for interview request or spec file")
	}

	select {
	case <-ctx.Done():
		return proto.StateDone, nil

	case interviewMsg := <-d.interviewRequestCh:
		return d.handleInterviewRequest(interviewMsg)

	case specMsg := <-d.specCh:
		return d.handleSpecFileUpload(specMsg)

	case resultMsg := <-d.replyCh:
		return d.handleArchitectResult(resultMsg)
	}
}

// handleSpecFileUpload processes a directly uploaded spec file, bypassing the interview.
func (d *Driver) handleSpecFileUpload(specMsg *proto.AgentMsg) (proto.State, error) {
	if specMsg == nil {
		d.logger.Info("Spec channel closed, shutting down")
		return proto.StateDone, nil
	}

	d.logger.Info("📄 PM received spec file upload: %s (bypassing interview)", specMsg.ID)

	// Extract spec content from message
	if typedPayload := specMsg.GetTypedPayload(); typedPayload != nil {
		if payloadData, err := typedPayload.ExtractGeneric(); err == nil {
			// Get spec markdown from payload
			specMarkdown, ok := payloadData["spec_markdown"].(string)
			if !ok || specMarkdown == "" {
				d.logger.Error("Spec file upload missing spec_markdown field")
				return proto.StateError, fmt.Errorf("spec file upload missing spec_markdown")
			}

			// Store spec directly as draft (bypass interview)
			d.stateData["draft_spec"] = specMarkdown
			d.logger.Info("✅ Spec file loaded (%d bytes), transitioning to WORKING", len(specMarkdown))

			// Transition to WORKING to validate and submit via submit_spec tool
			return StateWorking, nil
		}
	}

	d.logger.Error("Failed to extract spec content from message")
	return proto.StateError, fmt.Errorf("failed to extract spec content")
}

// handleInterviewRequest processes a new interview request from WebUI.
func (d *Driver) handleInterviewRequest(interviewMsg *proto.AgentMsg) (proto.State, error) {
	if interviewMsg == nil {
		d.logger.Info("Interview request channel closed, shutting down")
		return proto.StateDone, nil
	}

	d.logger.Info("🎯 PM received interview request: %s", interviewMsg.ID)

	// Extract interview parameters from message
	if typedPayload := interviewMsg.GetTypedPayload(); typedPayload != nil {
		if payloadData, err := typedPayload.ExtractGeneric(); err == nil {
			// Store session context
			if sessionID, ok := payloadData["session_id"].(string); ok {
				d.stateData["session_id"] = sessionID
			}
			if expertise, ok := payloadData["expertise"].(string); ok {
				d.stateData["expertise"] = expertise
			} else {
				// Use config default if not specified
				cfg, cfgErr := config.GetConfig()
				if cfgErr == nil && cfg.PM != nil && cfg.PM.DefaultExpertise != "" {
					d.stateData["expertise"] = cfg.PM.DefaultExpertise
				} else {
					d.stateData["expertise"] = DefaultExpertise
				}
			}
		}
	}

	// Initialize conversation history
	d.stateData["conversation"] = []map[string]string{}
	d.stateData["turn_count"] = 0

	return StateWorking, nil
}

// handleArchitectResult processes a RESULT message from architect.
func (d *Driver) handleArchitectResult(resultMsg *proto.AgentMsg) (proto.State, error) {
	if resultMsg == nil {
		d.logger.Warn("Reply channel closed unexpectedly")
		return proto.StateError, fmt.Errorf("reply channel closed")
	}

	d.logger.Info("🎯 PM received RESULT message: %s (type: %s)", resultMsg.ID, resultMsg.Type)

	// Verify this is a RESPONSE message
	if resultMsg.Type != proto.MsgTypeRESPONSE {
		d.logger.Warn("Unexpected message type: %s (expected RESPONSE)", resultMsg.Type)
		return proto.StateError, fmt.Errorf("unexpected message type: %s", resultMsg.Type)
	}

	// Extract approval response payload
	typedPayload := resultMsg.GetTypedPayload()
	if typedPayload == nil {
		d.logger.Error("RESULT message has no typed payload")
		return proto.StateError, fmt.Errorf("RESULT message missing payload")
	}

	approvalResult, err := typedPayload.ExtractApprovalResponse()
	if err != nil {
		d.logger.Error("Failed to extract approval response: %v", err)
		return proto.StateError, fmt.Errorf("failed to extract approval response: %w", err)
	}

	// Check approval status
	if approvalResult.Status == proto.ApprovalStatusApproved {
		d.logger.Info("✅ Spec APPROVED by architect")
		// Clear state for next interview
		d.stateData = make(map[string]any)
		return StateWaiting, nil
	}

	// Spec needs changes - architect sent feedback
	d.logger.Info("📝 Spec requires changes (status=%v) - feedback from architect: %s",
		approvalResult.Status, approvalResult.Feedback)

	delete(d.stateData, "pending_request_id") // Clear pending request

	// Inject submitted spec and architect feedback into LLM context
	// Both are added as user messages so they persist across LLM calls
	if submittedSpec, ok := d.stateData["spec_markdown"].(string); ok {
		d.stateData["draft_spec"] = submittedSpec
		d.logger.Info("📋 Injecting submitted spec (%d bytes) and architect feedback into PM context", len(submittedSpec))

		// Add submitted spec to context
		specContextMsg := fmt.Sprintf("## Previously Submitted Specification\n\n```markdown\n%s\n```", submittedSpec)
		d.contextManager.AddMessage("user", specContextMsg)

		// Add architect feedback to context
		feedbackMsg := fmt.Sprintf("## Architect Review Feedback\n\n%s\n\nPlease address this feedback and revise the specification.", approvalResult.Feedback)
		d.contextManager.AddMessage("user", feedbackMsg)
	} else {
		d.logger.Warn("⚠️  No submitted spec found in state - PM will start from scratch")
		// Still add feedback even if we don't have the original spec
		feedbackMsg := fmt.Sprintf("## Architect Feedback\n\n%s", approvalResult.Feedback)
		d.contextManager.AddMessage("user", feedbackMsg)
	}

	// Return to WORKING to address feedback
	return StateWorking, nil
}

// GetID returns the PM agent's ID.
func (d *Driver) GetID() string {
	return d.pmID
}

// GetState returns the current state.
func (d *Driver) GetState() proto.State {
	return d.currentState
}

// GetCurrentState returns the current state (required by agent.Driver interface).
func (d *Driver) GetCurrentState() proto.State {
	return d.currentState
}

// GetStateData returns a copy of the current state data (required by agent.Driver interface).
func (d *Driver) GetStateData() map[string]any {
	// Return a shallow copy to prevent external modification
	stateCopy := make(map[string]any, len(d.stateData))
	for k, v := range d.stateData {
		stateCopy[k] = v
	}
	return stateCopy
}

// GetAgentType returns the agent type (required by agent.Driver interface).
func (d *Driver) GetAgentType() agent.Type {
	return agent.TypePM
}

// Initialize sets up the driver and loads any existing state (required by agent.Driver interface).
func (d *Driver) Initialize(_ context.Context) error {
	// PM agent doesn't need initialization - state is initialized in constructor
	return nil
}

// Step executes a single step of the driver's state machine (required by agent.Driver interface).
// Returns whether processing is complete.
func (d *Driver) Step(ctx context.Context) (bool, error) {
	// Execute current state
	nextState, err := d.executeState(ctx)
	if err != nil {
		d.logger.Error("❌ PM agent state machine failed: %v", err)
		nextState = proto.StateError
	}

	// Validate transition
	if !IsValidPMTransition(d.currentState, nextState) {
		d.logger.Error("❌ Invalid PM state transition: %s → %s", d.currentState, nextState)
		nextState = proto.StateError
	}

	// Transition to next state
	if d.currentState != nextState {
		d.logger.Info("🔄 PM state transition: %s → %s", d.currentState, nextState)
		d.currentState = nextState
	}

	// Handle terminal states
	switch nextState {
	case proto.StateDone:
		return true, nil
	case proto.StateError:
		d.logger.Error("⚠️  PM agent %s in ERROR state, resetting to WAITING", d.pmID)
		d.currentState = StateWaiting
		d.stateData = make(map[string]any)
		return false, err
	default:
		return false, nil
	}
}

// ValidateState checks if a state is valid for PM agent (required by agent.Driver interface).
func (d *Driver) ValidateState(state proto.State) error {
	validStates := d.GetValidStates()
	for _, validState := range validStates {
		if state == validState {
			return nil
		}
	}
	return fmt.Errorf("invalid state %s for PM agent", state)
}

// GetValidStates returns all valid states for PM agent (required by agent.Driver interface).
func (d *Driver) GetValidStates() []proto.State {
	return []proto.State{
		StateWaiting,
		StateWorking,
		proto.StateError,
		proto.StateDone,
	}
}

// SetChannels sets the dispatcher channels for PM (required by ChannelReceiver interface).
// PM receives spec requests via specCh (for file uploads) and gets replyCh for RESULT messages.
// questionsCh is nil for PM (only architect processes questions).
func (d *Driver) SetChannels(specCh <-chan *proto.AgentMsg, _ chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg) {
	d.specCh = specCh
	d.replyCh = replyCh
}

// SetDispatcher sets the dispatcher reference (required by ChannelReceiver interface).
func (d *Driver) SetDispatcher(dispatcher *dispatch.Dispatcher) {
	// PM already has dispatcher from constructor, but update it for consistency.
	d.dispatcher = dispatcher
}

// SetStateNotificationChannel sets the state notification channel (required by ChannelReceiver interface).
func (d *Driver) SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification) {
	d.stateNotificationCh = stateNotifCh
	d.logger.Debug("State notification channel set for PM")
}

// Shutdown gracefully shuts down the PM agent.
func (d *Driver) Shutdown(_ context.Context) error {
	d.logger.Info("🎯 PM agent %s shutting down gracefully", d.pmID)
	// PM agent is stateless between interviews, no cleanup needed
	return nil
}
