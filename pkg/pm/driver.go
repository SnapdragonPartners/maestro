package pm

import (
	"context"
	"fmt"
	"path/filepath"
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
	"orchestrator/pkg/utils"
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

	// Poll intervals for state handlers to avoid tight loops.
	waitingPollInterval  = 100 * time.Millisecond // WAITING/PREVIEW state poll interval
	awaitUserPollTimeout = 500 * time.Millisecond // AWAIT_USER timeout before checking chat
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

	// StateKeyUserSpecMd stores the user's feature requirements markdown (working copy during interview).
	// Cleared after architect accepts the spec.
	// Note: "Md" suffix indicates the value is markdown-formatted text.
	StateKeyUserSpecMd = "user_spec_md"
	// StateKeyBootstrapSpecMd stores infrastructure requirements from bootstrap phase.
	// Cleared after architect accepts the spec.
	// Note: "Md" suffix indicates the value is markdown-formatted text.
	StateKeyBootstrapSpecMd = "bootstrap_spec_md"
	// StateKeyInFlight indicates development is in progress (spec submitted and accepted).
	// When true, only hotfixes are allowed (spec_submit with hotfix=true).
	// Set to true when architect approves spec, set to false when all stories complete.
	StateKeyInFlight = "in_flight"

	// StateKeySpecMetadata stores spec metadata (title, version, etc).
	StateKeySpecMetadata = "spec_metadata"
	// StateKeySpecUploaded indicates spec was uploaded vs generated through interview.
	StateKeySpecUploaded = "spec_uploaded"
	// StateKeyBootstrapParams stores bootstrap parameters from the bootstrap phase.
	StateKeyBootstrapParams = "bootstrap_params"
	// StateKeyTurnCount tracks the number of conversation turns.
	StateKeyTurnCount = "turn_count"
	// StateKeyIsHotfix indicates the current spec submission is a hotfix.
	// Set when spec_submit(hotfix=true) is called, cleared on approval.
	StateKeyIsHotfix = "is_hotfix"
)

// Driver implements the PM (Product Manager) agent.
// PM conducts interviews with users to generate high-quality specifications.
//
//nolint:govet // Prefer logical grouping over memory optimization
type Driver struct {
	*agent.BaseStateMachine // Embed state machine (provides LLMClient field and GetAgentID())
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

	// Set the LLM client via SetLLMClient (sets BaseStateMachine.LLMClient)
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

	// Set LLM client via BaseStateMachine
	if llmClient != nil {
		sm.SetLLMClient(llmClient)
	}

	return &Driver{
		BaseStateMachine:   sm,
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
	d.logger.Info("🎯 PM agent %s starting", d.GetAgentID())

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("🎯 PM agent %s received shutdown signal", d.GetAgentID())
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
				d.logger.Error("⚠️  PM agent %s in ERROR state, resetting to WAITING", d.GetAgentID())
				// Reset to WAITING after error and clear state data
				_ = d.TransitionTo(ctx, StateWaiting, nil)
				// Clear all state data
				for key := range d.GetStateData() {
					d.SetStateData(key, nil)
				}
			}

			// Handle DONE
			if terminalState == proto.StateDone {
				d.logger.Info("✅ PM agent %s shutting down", d.GetAgentID())
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
		time.Sleep(waitingPollInterval)
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

	// Inject submitted spec and architect feedback into LLM context
	// Both are added as user messages so they persist across LLM calls
	// Use GetDraftSpec() which handles concatenation of bootstrap + user specs
	if submittedSpec := d.GetDraftSpec(); submittedSpec != "" {
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
	return d.GetAgentID()
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
	// Validate required channels and BaseStateMachine are set
	if d.BaseStateMachine == nil {
		return fmt.Errorf("PM %s: BaseStateMachine not initialized", d.GetAgentID())
	}

	// Verify state notification channel is set on BaseStateMachine
	// This is critical for state transitions to be properly tracked
	if !d.BaseStateMachine.HasStateNotificationChannel() {
		d.logger.Warn("⚠️  State notification channel not set on BaseStateMachine - state changes won't be tracked")
	}

	d.logger.Info("PM agent %s initialized in state: %s", d.GetAgentID(), d.GetCurrentState())
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
		d.logger.Error("⚠️  PM agent %s in ERROR state, resetting to WAITING", d.GetAgentID())
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
	return utils.GetStateValueOr[bool](d.BaseStateMachine, StateKeyHasRepository, false)
}

// detectAndStoreBootstrapRequirements runs bootstrap detection and stores results in state.
// Returns the detected requirements (may be nil on error) and whether bootstrap is needed.
func (d *Driver) detectAndStoreBootstrapRequirements() (*tools.BootstrapRequirements, bool) {
	// Use agent workspace (projectDir/agentID) for detection since that's where files are committed
	agentWorkspace := filepath.Join(d.workDir, d.GetAgentID())
	d.logger.Info("🔍 Detecting bootstrap requirements in %s", agentWorkspace)

	detector := tools.NewBootstrapDetector(agentWorkspace)
	reqs, err := detector.Detect(context.Background())
	if err != nil {
		d.logger.Warn("Bootstrap detection failed: %v", err)
		return nil, false
	}

	// Store bootstrap requirements in state
	d.SetStateData(StateKeyBootstrapRequirements, reqs)
	d.SetStateData(StateKeyDetectedPlatform, reqs.DetectedPlatform)

	d.logger.Info("✅ Bootstrap detection complete: %d components needed, platform: %s (%.0f%% confidence)",
		len(reqs.MissingComponents), reqs.DetectedPlatform, reqs.PlatformConfidence*100)

	// Check if any components are missing
	needsBootstrap := reqs.HasAnyMissingComponents()
	if needsBootstrap {
		d.logger.Info("📋 Bootstrap needed: project_config=%v, git_repo=%v, dockerfile=%v, makefile=%v, knowledge_graph=%v, claude_code=%v",
			reqs.NeedsProjectConfig, reqs.NeedsGitRepo, reqs.NeedsDockerfile, reqs.NeedsMakefile, reqs.NeedsKnowledgeGraph, reqs.NeedsClaudeCode)
	}

	return reqs, needsBootstrap
}

// GetBootstrapRequirements returns the detected bootstrap requirements.
// Returns nil if bootstrap detection hasn't run yet or failed.
func (d *Driver) GetBootstrapRequirements() *tools.BootstrapRequirements {
	reqs, _ := utils.GetStateValue[*tools.BootstrapRequirements](d.BaseStateMachine, StateKeyBootstrapRequirements)
	return reqs
}

// GetDetectedPlatform returns the detected platform.
// Returns empty string if platform hasn't been detected.
func (d *Driver) GetDetectedPlatform() string {
	return utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyDetectedPlatform, "")
}

// GetDraftSpec returns the draft specification markdown if available.
// This is used by the WebUI to display the spec in PREVIEW state.
// For full specs, returns bootstrap + user spec concatenated.
// For hotfixes, returns just the user spec.
// Returns empty string if no draft spec is available.
func (d *Driver) GetDraftSpec() string {
	// Get user spec (required for any preview)
	userSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
	if userSpec == "" {
		return ""
	}

	// Get bootstrap spec (only present for full specs, not hotfixes)
	bootstrapSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyBootstrapSpecMd, "")

	// Concatenate if bootstrap exists
	if bootstrapSpec != "" {
		return bootstrapSpec + "\n\n" + userSpec
	}

	return userSpec
}

// IsInFlight returns true if development is in progress (spec submitted and accepted).
// When in_flight, only hotfixes are allowed.
func (d *Driver) IsInFlight() bool {
	return utils.GetStateValueOr[bool](d.BaseStateMachine, StateKeyInFlight, false)
}

// GetDraftSpecMetadata returns the draft specification metadata if available.
// Returns nil if no metadata is available.
func (d *Driver) GetDraftSpecMetadata() map[string]any {
	metadata, _ := utils.GetStateValue[map[string]any](d.BaseStateMachine, StateKeySpecMetadata)
	return metadata
}

// StartInterview initiates an interview session with the specified expertise level.
// This is called by the WebUI when the user clicks "Start Interview".
// Idempotent: succeeds if already in AWAIT_USER with same expertise (handles double-clicks).
func (d *Driver) StartInterview(expertise string) error {
	// Idempotency check: if already in AWAIT_USER or WORKING with same expertise, succeed silently
	currentState := d.GetCurrentState()
	if currentState == StateAwaitUser || currentState == StateWorking {
		existingExpertise := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserExpertise, "")
		if existingExpertise == expertise {
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

	// Detect bootstrap requirements using shared helper
	reqs, needsBootstrap := d.detectAndStoreBootstrapRequirements()
	if needsBootstrap && reqs != nil {
		// Add detection summary to context
		d.contextManager.AddMessage("system",
			fmt.Sprintf("Bootstrap analysis: Missing components: %v. Detected platform: %s",
				reqs.MissingComponents, reqs.DetectedPlatform))
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
// Always transitions to WORKING so PM can validate the spec before preview.
// Idempotent: succeeds if already processing same spec (handles double-submissions).
func (d *Driver) UploadSpec(markdown string) error {
	// Idempotency check: if already in WORKING or PREVIEW with same spec, succeed silently
	currentState := d.GetCurrentState()
	if currentState == StatePreview || currentState == StateWorking {
		existingSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
		if existingSpec == markdown {
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

	// Store spec and infer expert level (user provided their own spec)
	d.SetStateData(StateKeyUserSpecMd, markdown)
	d.SetStateData(StateKeyUserExpertise, "EXPERT")
	d.SetStateData(StateKeySpecUploaded, true)

	// Detect bootstrap requirements using shared helper
	reqs, needsBootstrap := d.detectAndStoreBootstrapRequirements()
	if needsBootstrap && reqs != nil {
		// Add uploaded spec content and bootstrap instructions to context
		specMessage := fmt.Sprintf(`# User Uploaded Specification File

The user has uploaded a specification file (%d bytes). **Parse this spec to extract bootstrap information before asking the user any questions.**

**Look for these details in the spec:**
1. **Project Name** - Often in title, frontmatter, or introduction
2. **Git Repository URL** - May be mentioned in deployment, setup, or configuration sections
3. **Primary Platform** - Look for language/framework mentions (go, python, node, rust, etc.)

**After parsing the spec:**
- Extract any bootstrap values you find
- ONLY ask the user for values that are genuinely missing or ambiguous in the spec
- Do NOT ask the user to re-provide information that's clearly stated in their spec

**Bootstrap Analysis:**
- Missing components: %v
- Detected platform: %s (%.0f%%%% confidence)

**The uploaded specification:**
`+"```markdown\n%s\n```",
			len(markdown), reqs.MissingComponents, reqs.DetectedPlatform, reqs.PlatformConfidence*100, markdown)

		d.contextManager.AddMessage("system", specMessage)
	}

	// Always transition to WORKING so PM can validate the uploaded spec
	// PM will review the spec, check for issues, and call spec_submit when ready
	ctx := context.Background()
	if needsBootstrap {
		// PM will extract bootstrap info from spec and ask missing questions
		// PM will use chat_post tool to ask questions, then transition to AWAIT_USER via await_user tool
		if err := d.TransitionTo(ctx, StateWorking, nil); err != nil {
			d.logger.Error("❌ Failed to transition to WORKING: %v", err)
			return fmt.Errorf("failed to transition to WORKING: %w", err)
		}
		d.logger.Info("📤 Spec uploaded (%d bytes) - bootstrap needed, transitioned to WORKING to extract info and fill gaps", len(markdown))
	} else {
		// Bootstrap complete - still go to WORKING so PM can validate the spec
		// PM will review it, check for completeness/clarity, and call spec_submit
		specMessage := fmt.Sprintf(`# User Uploaded Specification File

The user has uploaded a specification file (%d bytes). Bootstrap requirements are already satisfied.

**Your task:**
1. Review the uploaded specification for completeness and clarity
2. Check that all requirements are well-defined and actionable
3. Identify any ambiguities or missing information
4. If the spec looks good, use spec_submit to submit it for preview
5. If clarification is needed, use chat_ask_user to ask the user

**The uploaded specification:**
`+"```markdown\n%s\n```",
			len(markdown), markdown)

		d.contextManager.AddMessage("system", specMessage)
		if err := d.TransitionTo(ctx, StateWorking, nil); err != nil {
			d.logger.Error("❌ Failed to transition to WORKING: %v", err)
			return fmt.Errorf("failed to transition to WORKING: %w", err)
		}
		d.logger.Info("📤 Spec uploaded (%d bytes) - bootstrap complete, transitioned to WORKING for PM validation", len(markdown))
	}

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
		// Verify user spec exists before submitting
		userSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
		if userSpec == "" {
			return fmt.Errorf("no spec to submit - user_spec_md is empty")
		}

		// Check if this is a hotfix submission
		isHotfix := utils.GetStateValueOr[bool](d.BaseStateMachine, StateKeyIsHotfix, false)

		var err error
		if isHotfix {
			// Hotfixes jump the line - send directly to architect's hotfix handler
			err = d.sendHotfixRequest(ctx)
			if err != nil {
				d.logger.Error("❌ Failed to send hotfix request: %v", err)
				_ = d.TransitionTo(ctx, proto.StateError, nil)
				return fmt.Errorf("failed to send hotfix request: %w", err)
			}
			d.logger.Info("🔧 Hotfix submitted to architect - transitioned to AWAIT_ARCHITECT")
		} else {
			// Normal specs go through approval flow
			err = d.sendSpecApprovalRequest(ctx)
			if err != nil {
				d.logger.Error("❌ Failed to send spec approval request: %v", err)
				_ = d.TransitionTo(ctx, proto.StateError, nil)
				return fmt.Errorf("failed to send approval request: %w", err)
			}
			d.logger.Info("✅ Spec submitted to architect - transitioned to AWAIT_ARCHITECT")
		}

		_ = d.TransitionTo(ctx, StateAwaitArchitect, nil)
		return nil

	default:
		// Should never reach here due to validation above
		return fmt.Errorf("unknown preview action: %s", action)
	}
}

// Shutdown gracefully shuts down the PM agent.
func (d *Driver) Shutdown(_ context.Context) error {
	d.logger.Info("🎯 PM agent %s shutting down gracefully", d.GetAgentID())
	// PM agent is stateless between interviews, no cleanup needed
	return nil
}
