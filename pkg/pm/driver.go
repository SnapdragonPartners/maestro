package pm

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
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

	// PreviewActionContinue represents the "continue interview" action in PREVIEW state.
	PreviewActionContinue = "continue_interview"
	// PreviewActionSubmit represents the "submit to architect" action in PREVIEW state.
	PreviewActionSubmit = "submit_to_architect"
)

// State data keys for PM state management.
const (
	// StateKeyHasRepository indicates whether PM has git repository access.
	StateKeyHasRepository = "has_repository"
	// StateKeyUserExpertise stores the user's expertise level (NON_TECHNICAL, BASIC, EXPERT).
	StateKeyUserExpertise = "user_expertise"
	// StateKeyBootstrapRequirements stores detected bootstrap requirements.
	StateKeyBootstrapRequirements = "bootstrap_requirements"
	// StateKeyDetectedPlatform stores the detected platform.
	StateKeyDetectedPlatform = "detected_platform"
)

// Driver implements the PM (Product Manager) agent.
// PM conducts interviews with users to generate high-quality specifications.
//
//nolint:govet // Prefer logical grouping over memory optimization
type Driver struct {
	*agent.BaseStateMachine // Embed state machine for proper state management
	pmID                    string
	llmClient               agent.LLMClient // Typed LLM client (shadows base's untyped field)
	renderer                *templates.Renderer
	contextManager          *contextmgr.ContextManager
	logger                  *logx.Logger
	dispatcher              *dispatch.Dispatcher
	persistenceChannel      chan<- *persistence.Request
	chatService             *chat.Service    // Chat service for polling new messages
	executor                execpkg.Executor // PM executor for running tools
	workDir                 string
	replyCh                 <-chan *proto.AgentMsg // Receives RESULT messages from architect
	toolProvider            ToolProvider           // Tool provider for spec_submit tool
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
	chatService *chat.Service,
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

	// Create logger first
	logger := logx.NewLogger("pm")

	// Create template renderer
	renderer, err := templates.NewRenderer()
	if err != nil {
		return nil, fmt.Errorf("failed to create template renderer: %w", err)
	}

	// Create context manager with PM model
	contextManager := contextmgr.NewContextManagerWithModel(modelName)

	// Ensure PM workspace exists (pm-001/ read-only clone or minimal workspace)
	pmWorkspace, workspaceErr := workspace.EnsurePMWorkspace(ctx, workDir)
	if workspaceErr != nil {
		return nil, fmt.Errorf("failed to ensure PM workspace: %w", workspaceErr)
	}
	logger.Info("PM workspace ready at: %s", pmWorkspace)

	// Determine if PM has repository access
	hasRepository := cfg.Git != nil && cfg.Git.RepoURL != ""
	if hasRepository {
		logger.Info("PM has repository access: %s", cfg.Git.RepoURL)
	} else {
		logger.Info("PM starting in no-repo mode (bootstrap interview only)")
	}

	// Create and start PM container executor
	// PM mounts only its own workspace at /workspace (same as coders)
	pmExecutor := execpkg.NewPMExecutor(
		config.BootstrapContainerTag, // Use bootstrap image
		pmWorkspace,                  // PM's workspace directory
	)

	// Start the PM container (one retry on failure)
	if startErr := pmExecutor.Start(ctx); startErr != nil {
		logger.Warn("Failed to start PM container, retrying once: %v", startErr)
		// Retry once
		if retryErr := pmExecutor.Start(ctx); retryErr != nil {
			return nil, fmt.Errorf("failed to start PM container after retry: %w", retryErr)
		}
	}

	// Create tool provider with all PM tools (read_file, list_files, chat_post, bootstrap, spec_submit)
	// Note: WorkDir must be the container path, not host path
	agentCtx := tools.AgentContext{
		Executor:        pmExecutor,
		ChatService:     chatService,
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         "/workspace", // Container path where pmWorkspace is mounted
		AgentID:         pmID,
		ProjectDir:      workDir, // Host project directory for bootstrap detection
	}
	toolProvider := tools.NewProvider(&agentCtx, tools.PMTools)

	// Configure chat service on context manager for automatic injection
	if chatService != nil {
		chatAdapter := contextmgr.NewChatServiceAdapter(chatService)
		contextManager.SetChatService(chatAdapter, pmID)
		logger.Info("💬 Chat injection configured for PM %s", pmID)
	}

	// Create BaseStateMachine with PM transition table
	sm := agent.NewBaseStateMachine(pmID, StateWaiting, nil, validTransitions)

	// Set initial state data with repository availability
	sm.SetStateData(StateKeyHasRepository, hasRepository)

	// Create driver first (without LLM client yet)
	pmDriver := &Driver{
		BaseStateMachine:   sm,
		pmID:               pmID,
		llmClient:          nil, // Will be set below
		renderer:           renderer,
		contextManager:     contextManager,
		logger:             logger,
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		chatService:        chatService,
		executor:           pmExecutor,
		workDir:            workDir,
		toolProvider:       toolProvider,
	}

	// Now create LLM client with context (passing driver as StateProvider)
	llmClient, err := llmFactory.CreateClientWithContext(agent.TypePM, pmDriver, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client for PM: %w", err)
	}

	// Set the LLM client in both places
	pmDriver.llmClient = llmClient
	sm.SetLLMClient(llmClient)

	return pmDriver, nil
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
	executor execpkg.Executor, // PM executor for running tools
	workDir string,
) *Driver {
	// Create BaseStateMachine with PM transition table
	sm := agent.NewBaseStateMachine(pmID, StateWaiting, nil, validTransitions)

	// Set LLM client in both places
	if llmClient != nil {
		sm.SetLLMClient(llmClient)
	}

	return &Driver{
		BaseStateMachine:   sm,
		pmID:               pmID,
		llmClient:          llmClient,
		renderer:           renderer,
		contextManager:     contextManager,
		logger:             logx.NewLogger("pm"),
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
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
			// Capture state before executing handler
			stateBefore := d.GetCurrentState()

			// Execute current state
			nextState, err := d.executeState(ctx)
			if err != nil {
				d.logger.Error("❌ PM agent state machine failed: %v", err)
				nextState = proto.StateError
			}

			// Get current state to check for external changes
			currentState := d.GetCurrentState()

			// Check if state changed externally (via direct method call) while handler was running
			if stateBefore != currentState {
				d.logger.Debug("🔄 State changed externally during handler execution: %s → %s (ignoring handler return)", stateBefore, currentState)
				// State was changed by direct method call - ignore handler's return value
				// and continue with the new state
				continue
			}

			// Transition to next state (BaseStateMachine handles validation)
			if currentState != nextState {
				err := d.TransitionTo(ctx, nextState, nil)
				if err != nil {
					d.logger.Error("❌ PM state transition failed: %s → %s: %v", currentState, nextState, err)
					// Force transition to ERROR on validation failure
					_ = d.TransitionTo(ctx, proto.StateError, nil)
				} else {
					d.logger.Info("🔄 PM state transition: %s → %s", currentState, nextState)
				}
			}

			// Handle terminal states
			terminalState := d.GetCurrentState()
			if terminalState == proto.StateError {
				d.logger.Error("⚠️  PM agent %s in ERROR state, resetting to WAITING", d.pmID)
				// Reset to WAITING after error and clear state data
				_ = d.TransitionTo(ctx, StateWaiting, nil)
				// Clear all state data
				for key := range d.GetStateData() {
					d.SetStateData(key, nil)
				}
			}

			// Handle DONE
			if terminalState == proto.StateDone {
				d.logger.Info("✅ PM agent %s shutting down", d.pmID)
				return nil
			}
		}
	}
}

// executeState executes the current state and returns the next state.
func (d *Driver) executeState(ctx context.Context) (proto.State, error) {
	// Get current state from BaseStateMachine (thread-safe)
	currentState := d.GetCurrentState()

	switch currentState {
	case StateWaiting:
		return d.handleWaiting(ctx)
	case StateWorking:
		return d.handleWorking(ctx)
	case StateAwaitUser:
		return d.handleAwaitUser(ctx)
	case StatePreview:
		return d.handlePreview(ctx)
	case StateAwaitArchitect:
		return d.handleAwaitArchitect(ctx)
	default:
		return proto.StateError, fmt.Errorf("unknown state: %s", currentState)
	}
}

// handleWaiting waits for architect RESULT messages (feedback after spec submission).
// User actions (start interview, upload spec) now directly modify state via methods.
func (d *Driver) handleWaiting(ctx context.Context) (proto.State, error) {
	d.logger.Debug("🎯 PM in WAITING state - checking for state changes or architect feedback")

	// Check for architect RESULT messages (non-blocking)
	select {
	case <-ctx.Done():
		d.logger.Info("⏹️  Context canceled while in WAITING")
		return proto.StateDone, nil

	case resultMsg := <-d.replyCh:
		// RESULT message from architect (feedback after previous spec submission)
		return d.handleArchitectResult(resultMsg)

	default:
		// No messages - sleep briefly to avoid tight loop
		// Note: Direct method calls (StartInterview/UploadSpec) modify state directly,
		// and the Run loop will detect the change and route to the new handler
		time.Sleep(100 * time.Millisecond)
		return StateWaiting, nil
	}
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
		// Clear state data for next interview
		for key := range d.GetStateData() {
			d.SetStateData(key, nil)
		}
		return StateWaiting, nil
	}

	// Spec needs changes - architect sent feedback
	d.logger.Info("📝 Spec requires changes (status=%v) - feedback from architect: %s",
		approvalResult.Status, approvalResult.Feedback)

	// Clear pending request from state
	stateData := d.GetStateData()
	if _, hasPending := stateData["pending_request_id"]; hasPending {
		d.SetStateData("pending_request_id", nil)
	}

	// Inject submitted spec and architect feedback into LLM context
	// Both are added as user messages so they persist across LLM calls
	if submittedSpec, ok := stateData["spec_markdown"].(string); ok {
		d.SetStateData("draft_spec", submittedSpec)
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

// GetStoryID returns an empty string as PM doesn't work on stories directly.
func (d *Driver) GetStoryID() string {
	return "" // PM doesn't have a story ID
}

// GetState returns the current state (alias for GetCurrentState for backward compatibility).
func (d *Driver) GetState() proto.State {
	return d.GetCurrentState()
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

	// Get current state
	currentState := d.GetCurrentState()

	// Transition to next state using BaseStateMachine (handles validation)
	if currentState != nextState {
		transitionErr := d.TransitionTo(ctx, nextState, nil)
		if transitionErr != nil {
			d.logger.Error("❌ PM state transition failed: %s → %s: %v", currentState, nextState, transitionErr)
			// Force transition to ERROR on validation failure
			_ = d.TransitionTo(ctx, proto.StateError, nil)
		} else {
			d.logger.Info("🔄 PM state transition: %s → %s", currentState, nextState)
		}
	}

	// Handle terminal states
	terminalState := d.GetCurrentState()
	if terminalState == proto.StateError {
		d.logger.Error("⚠️  PM agent %s in ERROR state, resetting to WAITING", d.pmID)
		_ = d.TransitionTo(ctx, StateWaiting, nil)
		// Clear all state data
		for key := range d.GetStateData() {
			d.SetStateData(key, nil)
		}
	}

	// Return based on terminal state
	switch terminalState {
	case proto.StateDone:
		return true, nil
	case proto.StateError:
		return false, err
	default:
		return false, nil
	}
}

// ValidateState checks if a state is valid for PM agent (required by agent.Driver interface).
func (d *Driver) ValidateState(state proto.State) error {
	if !IsValidPMState(state) {
		return fmt.Errorf("invalid state %s for PM agent", state)
	}
	return nil
}

// GetValidStates returns all valid states for PM agent (required by agent.Driver interface).
func (d *Driver) GetValidStates() []proto.State {
	return GetAllPMStates()
}

// SetChannels sets the dispatcher channels for PM (required by ChannelReceiver interface).
// PM only needs replyCh for RESULT messages from architect.
// Interview requests and spec uploads come directly from WebUI via StartInterview/UploadSpec methods.
// questionsCh (first parameter) is used by PM for interview request messages from dispatcher.
// Second parameter is unused (nil).
func (d *Driver) SetChannels(_, _ chan *proto.AgentMsg, replyCh <-chan *proto.AgentMsg) {
	d.replyCh = replyCh
}

// SetDispatcher sets the dispatcher reference (required by ChannelReceiver interface).
func (d *Driver) SetDispatcher(dispatcher *dispatch.Dispatcher) {
	// PM already has dispatcher from constructor, but update it for consistency.
	d.dispatcher = dispatcher
}

// SetStateNotificationChannel sets the state notification channel (required by ChannelReceiver interface).
func (d *Driver) SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification) {
	// Delegate to BaseStateMachine
	d.BaseStateMachine.SetStateNotificationChannel(stateNotifCh)
	d.logger.Debug("State notification channel set for PM")
}

// HasRepository returns whether PM has git repository access.
// Returns false if PM is running in no-repo/bootstrap mode.
func (d *Driver) HasRepository() bool {
	stateData := d.GetStateData()
	if hasRepo, ok := stateData[StateKeyHasRepository].(bool); ok {
		return hasRepo
	}
	return false
}

// GetBootstrapRequirements returns the detected bootstrap requirements.
// Returns nil if bootstrap detection hasn't run yet or failed.
func (d *Driver) GetBootstrapRequirements() *tools.BootstrapRequirements {
	stateData := d.GetStateData()
	if reqs, ok := stateData[StateKeyBootstrapRequirements].(*tools.BootstrapRequirements); ok {
		return reqs
	}
	return nil
}

// GetDetectedPlatform returns the detected platform.
// Returns empty string if platform hasn't been detected.
func (d *Driver) GetDetectedPlatform() string {
	stateData := d.GetStateData()
	if platform, ok := stateData[StateKeyDetectedPlatform].(string); ok {
		return platform
	}
	return ""
}

// GetDraftSpec returns the draft specification markdown if available.
// This is used by the WebUI to display the spec in PREVIEW state.
// Returns empty string if no draft spec is available.
func (d *Driver) GetDraftSpec() string {
	stateData := d.GetStateData()
	if draftSpec, ok := stateData["draft_spec_markdown"].(string); ok {
		return draftSpec
	}
	return ""
}

// GetDraftSpecMetadata returns the draft specification metadata if available.
// Returns nil if no metadata is available.
func (d *Driver) GetDraftSpecMetadata() map[string]any {
	stateData := d.GetStateData()
	if metadata, ok := stateData["spec_metadata"].(map[string]any); ok {
		return metadata
	}
	return nil
}

// StartInterview initiates an interview session with the specified expertise level.
// This is called by the WebUI when the user clicks "Start Interview".
// Idempotent: succeeds if already in AWAIT_USER with same expertise (handles double-clicks).
func (d *Driver) StartInterview(expertise string) error {
	// Idempotency check: if already in AWAIT_USER or WORKING with same expertise, succeed silently
	currentState := d.GetCurrentState()
	stateData := d.GetStateData()
	if currentState == StateAwaitUser || currentState == StateWorking {
		if existingExpertise, ok := stateData[StateKeyUserExpertise].(string); ok && existingExpertise == expertise {
			d.logger.Info("📝 Interview already started with expertise: %s - idempotent success", expertise)
			return nil
		}
	}

	// Validate state transition - must be in WAITING to start
	if currentState != StateWaiting {
		return fmt.Errorf("cannot start interview in state %s (must be WAITING)", currentState)
	}

	// Store expertise level
	d.SetStateData(StateKeyUserExpertise, expertise)
	d.contextManager.AddMessage("system", fmt.Sprintf("User has expertise level: %s", expertise))

	// Detect bootstrap requirements
	d.logger.Info("🔍 Detecting bootstrap requirements (expertise: %s)", expertise)
	detector := tools.NewBootstrapDetector(d.workDir)
	reqs, err := detector.Detect(context.Background())
	needsBootstrap := false
	if err != nil {
		d.logger.Warn("Bootstrap detection failed: %v", err)
		// Continue without bootstrap detection - non-fatal
	} else {
		// Store bootstrap requirements in state
		d.SetStateData(StateKeyBootstrapRequirements, reqs)
		d.SetStateData(StateKeyDetectedPlatform, reqs.DetectedPlatform)

		d.logger.Info("✅ Bootstrap detection complete: %d components needed, platform: %s (%.0f%% confidence)",
			len(reqs.MissingComponents), reqs.DetectedPlatform, reqs.PlatformConfidence*100)

		// Add detection summary to context if anything is missing
		if reqs.HasAnyMissingComponents() {
			d.contextManager.AddMessage("system",
				fmt.Sprintf("Bootstrap analysis: Missing components: %v. Detected platform: %s",
					reqs.MissingComponents, reqs.DetectedPlatform))

			// Any missing components means we need bootstrap
			// PM should be in WORKING mode to handle setup
			needsBootstrap = true

			d.logger.Info("📋 Bootstrap needed: project_config=%v, git_repo=%v, dockerfile=%v, makefile=%v, knowledge_graph=%v",
				reqs.NeedsProjectConfig, reqs.NeedsGitRepo, reqs.NeedsDockerfile, reqs.NeedsMakefile, reqs.NeedsKnowledgeGraph)
		}
	}

	// Decide initial state based on bootstrap needs
	ctx := context.Background()
	if needsBootstrap {
		// Start in WORKING so PM can proactively ask bootstrap questions
		if err := d.TransitionTo(ctx, StateWorking, nil); err != nil {
			d.logger.Error("❌ Failed to transition to WORKING: %v", err)
			return fmt.Errorf("failed to transition to WORKING: %w", err)
		}
		d.logger.Info("📝 Interview started (expertise: %s) - bootstrap needed, transitioned to WORKING for proactive setup", expertise)
	} else {
		// Start in AWAIT_USER - user will initiate feature discussion
		if err := d.TransitionTo(ctx, StateAwaitUser, nil); err != nil {
			d.logger.Error("❌ Failed to transition to AWAIT_USER: %v", err)
			return fmt.Errorf("failed to transition to AWAIT_USER: %w", err)
		}
		d.logger.Info("📝 Interview started (expertise: %s) - transitioned to AWAIT_USER", expertise)
	}

	return nil
}

// UploadSpec accepts an uploaded spec markdown file.
// This is called by the WebUI when the user uploads a spec file.
// Idempotent: succeeds if already in PREVIEW with same spec (handles double-submissions).
func (d *Driver) UploadSpec(markdown string) error {
	// Idempotency check: if already in PREVIEW with same spec, succeed silently
	currentState := d.GetCurrentState()
	stateData := d.GetStateData()
	if currentState == StatePreview {
		if existingSpec, ok := stateData["draft_spec_markdown"].(string); ok && existingSpec == markdown {
			d.logger.Info("📤 Spec already uploaded (%d bytes) - idempotent success", len(markdown))
			return nil
		}
	}

	// Validate state transition - allow upload in WAITING or AWAIT_USER
	// WAITING: before any interview started
	// AWAIT_USER: during interview (allows user to upload spec instead of continuing interview)
	if currentState != StateWaiting && currentState != StateAwaitUser {
		return fmt.Errorf("cannot upload spec in state %s (must be WAITING or AWAIT_USER)", currentState)
	}

	// Store spec and transition to PREVIEW
	d.SetStateData("draft_spec_markdown", markdown)
	d.SetStateData("user_expertise", "EXPERT") // Infer highest proficiency for uploaded specs
	d.contextManager.AddMessage("system", "User uploaded a specification file. You can answer questions about it if the user clicks 'Continue Interview'.")
	ctx := context.Background()
	_ = d.TransitionTo(ctx, StatePreview, nil)

	d.logger.Info("📤 Spec uploaded (%d bytes) - transitioned to PREVIEW", len(markdown))
	return nil
}

// PreviewAction handles preview actions from the WebUI.
// This is called when the user clicks "Continue Interview" or "Submit for Development".
// Valid actions: "continue_interview", "submit_to_architect".
// Idempotent: succeeds if already in target state (handles double-clicks).
func (d *Driver) PreviewAction(ctx context.Context, action string) error {
	// Validate action first
	if action != PreviewActionContinue && action != PreviewActionSubmit {
		return fmt.Errorf("invalid preview action: %s (must be '%s' or '%s')", action, PreviewActionContinue, PreviewActionSubmit)
	}

	// Idempotency check: if already in target state, succeed silently
	if action == PreviewActionContinue && d.GetCurrentState() == StateAwaitUser {
		d.logger.Info("🔄 Already in AWAIT_USER (continue interview) - idempotent success")
		return nil
	}
	if action == PreviewActionSubmit && d.GetCurrentState() == StateAwaitArchitect {
		d.logger.Info("📤 Already in AWAIT_ARCHITECT (submitted) - idempotent success")
		return nil
	}

	// Validate state transition
	if d.GetCurrentState() != StatePreview {
		return fmt.Errorf("cannot perform preview action in state %s (must be PREVIEW)", d.GetCurrentState())
	}

	d.logger.Info("📋 Preview action: %s", action)

	switch action {
	case PreviewActionContinue:
		// Inject question to context and transition to AWAIT_USER
		d.contextManager.AddMessage("user-action", "What changes would you like to make?")
		_ = d.TransitionTo(ctx, StateAwaitUser, nil)
		d.logger.Info("🔄 User chose to continue interview - transitioned to AWAIT_USER")
		return nil

	case PreviewActionSubmit:
		// Copy draft_spec_markdown to spec_markdown for sendSpecApprovalRequest
		stateData := d.GetStateData()
		if draftSpec, ok := stateData["draft_spec_markdown"].(string); ok {
			d.SetStateData("spec_markdown", draftSpec)
		}

		// Send REQUEST to architect
		err := d.sendSpecApprovalRequest(ctx)
		if err != nil {
			d.logger.Error("❌ Failed to send spec approval request: %v", err)
			_ = d.TransitionTo(ctx, proto.StateError, nil)
			return fmt.Errorf("failed to send approval request: %w", err)
		}

		_ = d.TransitionTo(ctx, StateAwaitArchitect, nil)
		d.logger.Info("✅ Spec submitted to architect - transitioned to AWAIT_ARCHITECT")
		return nil

	default:
		// Should never reach here due to validation above
		return fmt.Errorf("unknown preview action: %s", action)
	}
}

// Shutdown gracefully shuts down the PM agent.
func (d *Driver) Shutdown(_ context.Context) error {
	d.logger.Info("🎯 PM agent %s shutting down gracefully", d.pmID)
	// PM agent is stateless between interviews, no cleanup needed
	return nil
}
