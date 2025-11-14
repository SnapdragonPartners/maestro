package pm

import (
	"context"
	"fmt"
	"sync"
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
	pmID                string
	llmClient           agent.LLMClient
	renderer            *templates.Renderer
	contextManager      *contextmgr.ContextManager
	logger              *logx.Logger
	dispatcher          *dispatch.Dispatcher
	persistenceChannel  chan<- *persistence.Request
	chatService         *chat.Service // Chat service for polling new messages
	mu                  sync.RWMutex  // Protects currentState and stateData from concurrent access
	currentState        proto.State
	stateData           map[string]any
	executor            execpkg.Executor // PM executor for running tools
	workDir             string
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

	// Create initial state data with repository availability
	initialStateData := map[string]any{
		StateKeyHasRepository: hasRepository,
	}

	// Create driver first (without LLM client yet)
	pmDriver := &Driver{
		pmID:               pmID,
		llmClient:          nil, // Will be set below
		renderer:           renderer,
		contextManager:     contextManager,
		logger:             logger,
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		chatService:        chatService,
		currentState:       StateWaiting,
		stateData:          initialStateData,
		executor:           pmExecutor,
		workDir:            workDir,
		toolProvider:       toolProvider,
	}

	// Now create LLM client with context (passing driver as StateProvider)
	llmClient, err := llmFactory.CreateClientWithContext(agent.TypePM, pmDriver, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client for PM: %w", err)
	}

	// Set the LLM client (no middleware wrapper needed - chat injection is in FlushUserBuffer)
	pmDriver.llmClient = llmClient

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
			d.mu.RLock()
			stateBefore := d.currentState
			d.mu.RUnlock()

			// Execute current state
			nextState, err := d.executeState(ctx)
			if err != nil {
				d.logger.Error("❌ PM agent state machine failed: %v", err)
				nextState = proto.StateError
			}

			// Validate and apply state transition with mutex protection
			d.mu.Lock()
			currentState := d.currentState

			// Check if state changed externally (via direct method call) while handler was running
			if stateBefore != currentState {
				d.logger.Debug("🔄 State changed externally during handler execution: %s → %s (ignoring handler return)", stateBefore, currentState)
				// State was changed by direct method call - ignore handler's return value
				// and continue with the new state
				d.mu.Unlock()
				continue
			}

			// Validate transition
			if !IsValidPMTransition(currentState, nextState) {
				d.logger.Error("❌ Invalid PM state transition: %s → %s", currentState, nextState)
				nextState = proto.StateError
			}

			// Transition to next state
			if currentState != nextState {
				d.logger.Info("🔄 PM state transition: %s → %s", currentState, nextState)
				d.currentState = nextState
			}

			// Handle terminal states (need to check nextState after potential update)
			terminalState := nextState
			if terminalState == proto.StateError {
				d.logger.Error("⚠️  PM agent %s in ERROR state, resetting to WAITING", d.pmID)
				// Reset to WAITING after error
				d.currentState = StateWaiting
				d.stateData = make(map[string]any)
			}
			d.mu.Unlock()

			// Handle DONE outside of lock
			if terminalState == proto.StateDone {
				d.logger.Info("✅ PM agent %s shutting down", d.pmID)
				return nil
			}
		}
	}
}

// executeState executes the current state and returns the next state.
func (d *Driver) executeState(ctx context.Context) (proto.State, error) {
	// Read current state with lock, then execute handler without lock
	// (handlers may take time and should not hold the lock)
	d.mu.RLock()
	currentState := d.currentState
	d.mu.RUnlock()

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

// GetStoryID returns an empty string as PM doesn't work on stories directly.
func (d *Driver) GetStoryID() string {
	return "" // PM doesn't have a story ID
}

// GetState returns the current state.
func (d *Driver) GetState() proto.State {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentState
}

// GetCurrentState returns the current state (required by agent.Driver interface).
func (d *Driver) GetCurrentState() proto.State {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentState
}

// GetStateData returns a copy of the current state data (required by agent.Driver interface).
func (d *Driver) GetStateData() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()
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

	// Validate and apply state transition with mutex protection
	d.mu.Lock()
	currentState := d.currentState

	// Validate transition
	if !IsValidPMTransition(currentState, nextState) {
		d.logger.Error("❌ Invalid PM state transition: %s → %s", currentState, nextState)
		nextState = proto.StateError
	}

	// Transition to next state
	if currentState != nextState {
		d.logger.Info("🔄 PM state transition: %s → %s", currentState, nextState)
		d.currentState = nextState
	}

	// Handle terminal states
	terminalState := nextState
	if terminalState == proto.StateError {
		d.logger.Error("⚠️  PM agent %s in ERROR state, resetting to WAITING", d.pmID)
		d.currentState = StateWaiting
		d.stateData = make(map[string]any)
	}
	d.mu.Unlock()

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
	d.stateNotificationCh = stateNotifCh
	d.logger.Debug("State notification channel set for PM")
}

// HasRepository returns whether PM has git repository access.
// Returns false if PM is running in no-repo/bootstrap mode.
func (d *Driver) HasRepository() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if hasRepo, ok := d.stateData[StateKeyHasRepository].(bool); ok {
		return hasRepo
	}
	return false
}

// GetBootstrapRequirements returns the detected bootstrap requirements.
// Returns nil if bootstrap detection hasn't run yet or failed.
func (d *Driver) GetBootstrapRequirements() *tools.BootstrapRequirements {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if reqs, ok := d.stateData[StateKeyBootstrapRequirements].(*tools.BootstrapRequirements); ok {
		return reqs
	}
	return nil
}

// GetDetectedPlatform returns the detected platform.
// Returns empty string if platform hasn't been detected.
func (d *Driver) GetDetectedPlatform() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if platform, ok := d.stateData[StateKeyDetectedPlatform].(string); ok {
		return platform
	}
	return ""
}

// GetDraftSpec returns the draft specification markdown if available.
// This is used by the WebUI to display the spec in PREVIEW state.
// Returns empty string if no draft spec is available.
func (d *Driver) GetDraftSpec() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if draftSpec, ok := d.stateData["draft_spec_markdown"].(string); ok {
		return draftSpec
	}
	return ""
}

// GetDraftSpecMetadata returns the draft specification metadata if available.
// Returns nil if no metadata is available.
func (d *Driver) GetDraftSpecMetadata() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if metadata, ok := d.stateData["spec_metadata"].(map[string]any); ok {
		return metadata
	}
	return nil
}

// StartInterview initiates an interview session with the specified expertise level.
// This is called by the WebUI when the user clicks "Start Interview".
// Idempotent: succeeds if already in AWAIT_USER with same expertise (handles double-clicks).
func (d *Driver) StartInterview(expertise string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Idempotency check: if already in AWAIT_USER with same expertise, succeed silently
	if d.currentState == StateAwaitUser {
		if existingExpertise, ok := d.stateData[StateKeyUserExpertise].(string); ok && existingExpertise == expertise {
			d.logger.Info("📝 Interview already started with expertise: %s - idempotent success", expertise)
			return nil
		}
	}

	// Validate state transition
	if d.currentState != StateWaiting {
		return fmt.Errorf("cannot start interview in state %s (must be WAITING)", d.currentState)
	}

	// Store expertise level
	d.stateData[StateKeyUserExpertise] = expertise
	d.contextManager.AddMessage("system", fmt.Sprintf("User has expertise level: %s", expertise))

	// Detect bootstrap requirements
	d.logger.Info("🔍 Detecting bootstrap requirements (expertise: %s)", expertise)
	detector := tools.NewBootstrapDetector(d.workDir)
	reqs, err := detector.Detect(context.Background())
	if err != nil {
		d.logger.Warn("Bootstrap detection failed: %v", err)
		// Continue without bootstrap detection - non-fatal
	} else {
		// Store bootstrap requirements in state
		d.stateData[StateKeyBootstrapRequirements] = reqs
		d.stateData[StateKeyDetectedPlatform] = reqs.DetectedPlatform

		d.logger.Info("✅ Bootstrap detection complete: %d components needed, platform: %s (%.0f%% confidence)",
			len(reqs.MissingComponents), reqs.DetectedPlatform, reqs.PlatformConfidence*100)

		// Add detection summary to context
		if len(reqs.MissingComponents) > 0 {
			d.contextManager.AddMessage("system",
				fmt.Sprintf("Bootstrap analysis: Missing components: %v. Detected platform: %s",
					reqs.MissingComponents, reqs.DetectedPlatform))
		}
	}

	// Transition to AWAIT_USER
	d.currentState = StateAwaitUser

	d.logger.Info("📝 Interview started (expertise: %s) - transitioned to AWAIT_USER", expertise)
	return nil
}

// UploadSpec accepts an uploaded spec markdown file.
// This is called by the WebUI when the user uploads a spec file.
// Idempotent: succeeds if already in PREVIEW with same spec (handles double-submissions).
func (d *Driver) UploadSpec(markdown string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Idempotency check: if already in PREVIEW with same spec, succeed silently
	if d.currentState == StatePreview {
		if existingSpec, ok := d.stateData["draft_spec_markdown"].(string); ok && existingSpec == markdown {
			d.logger.Info("📤 Spec already uploaded (%d bytes) - idempotent success", len(markdown))
			return nil
		}
	}

	// Validate state transition - allow upload in WAITING or AWAIT_USER
	// WAITING: before any interview started
	// AWAIT_USER: during interview (allows user to upload spec instead of continuing interview)
	if d.currentState != StateWaiting && d.currentState != StateAwaitUser {
		return fmt.Errorf("cannot upload spec in state %s (must be WAITING or AWAIT_USER)", d.currentState)
	}

	// Store spec and transition to PREVIEW
	d.stateData["draft_spec_markdown"] = markdown
	d.stateData["user_expertise"] = "EXPERT" // Infer highest proficiency for uploaded specs
	d.contextManager.AddMessage("system", "User uploaded a specification file. You can answer questions about it if the user clicks 'Continue Interview'.")
	d.currentState = StatePreview

	d.logger.Info("📤 Spec uploaded (%d bytes) - transitioned to PREVIEW", len(markdown))
	return nil
}

// PreviewAction handles preview actions from the WebUI.
// This is called when the user clicks "Continue Interview" or "Submit for Development".
// Valid actions: "continue_interview", "submit_to_architect".
// Idempotent: succeeds if already in target state (handles double-clicks).
func (d *Driver) PreviewAction(ctx context.Context, action string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Validate action first
	if action != PreviewActionContinue && action != PreviewActionSubmit {
		return fmt.Errorf("invalid preview action: %s (must be '%s' or '%s')", action, PreviewActionContinue, PreviewActionSubmit)
	}

	// Idempotency check: if already in target state, succeed silently
	if action == PreviewActionContinue && d.currentState == StateAwaitUser {
		d.logger.Info("🔄 Already in AWAIT_USER (continue interview) - idempotent success")
		return nil
	}
	if action == PreviewActionSubmit && d.currentState == StateAwaitArchitect {
		d.logger.Info("📤 Already in AWAIT_ARCHITECT (submitted) - idempotent success")
		return nil
	}

	// Validate state transition
	if d.currentState != StatePreview {
		return fmt.Errorf("cannot perform preview action in state %s (must be PREVIEW)", d.currentState)
	}

	d.logger.Info("📋 Preview action: %s", action)

	switch action {
	case PreviewActionContinue:
		// Inject question to context and transition to AWAIT_USER
		d.contextManager.AddMessage("user-action", "What changes would you like to make?")
		d.currentState = StateAwaitUser
		d.logger.Info("🔄 User chose to continue interview - transitioned to AWAIT_USER")
		return nil

	case PreviewActionSubmit:
		// Copy draft_spec_markdown to spec_markdown for sendSpecApprovalRequest
		if draftSpec, ok := d.stateData["draft_spec_markdown"].(string); ok {
			d.stateData["spec_markdown"] = draftSpec
		}

		// Send REQUEST to architect (this must be done while holding lock)
		err := d.sendSpecApprovalRequest(ctx)
		if err != nil {
			d.logger.Error("❌ Failed to send spec approval request: %v", err)
			d.currentState = proto.StateError
			return fmt.Errorf("failed to send approval request: %w", err)
		}

		d.currentState = StateAwaitArchitect
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
