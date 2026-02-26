package pm

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
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
	"orchestrator/pkg/specrender"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
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
	// Note: Platform is NOT stored here - PM LLM handles platform confirmation with user.
	StateKeyBootstrapRequirements = "bootstrap_requirements"

	// StateKeyUserSpecMd stores the user's feature requirements markdown (working copy during interview).
	// Cleared after architect accepts the spec.
	// Note: "Md" suffix indicates the value is markdown-formatted text.
	StateKeyUserSpecMd = "user_spec_md"
	// StateKeyBootstrapSpecMd is DEPRECATED - bootstrap spec is now rendered by architect.
	// Kept for backwards compatibility with persisted state.
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

	// StateKeyMaestroMdContent stores MAESTRO.md content for prompt inclusion.
	// Loaded from repo at session start, updated when PM generates or updates it.
	StateKeyMaestroMdContent = "maestro_md_content"

	// StateKeyBootstrapSpecSent indicates whether Spec 0 (bootstrap spec) has been sent
	// to the architect. Prevents duplicate sends on re-entry to WORKING.
	StateKeyBootstrapSpecSent = "bootstrap_spec_sent"
	// StateKeyAwaitingSpecType tracks which type of spec the PM is awaiting
	// architect response for. Values: "bootstrap", "user", "hotfix".
	StateKeyAwaitingSpecType = "awaiting_spec_type"
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
	demoAvailable           bool                   // True when bootstrap is complete (no missing components)
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

	// Get config for other settings
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Get model name (respects airplane mode override)
	modelName := config.GetEffectivePMModel()

	// Create logger first
	logger := logx.NewLogger("pm")

	// Create template renderer
	renderer, err := templates.NewRenderer()
	if err != nil {
		return nil, fmt.Errorf("failed to create template renderer: %w", err)
	}

	// Create context manager with PM model
	contextManager := contextmgr.NewContextManagerWithModel(modelName)

	// PM workspace is pre-created at startup (placeholder directory).
	// Bootstrap detection handles cloning/updating when mirror exists.
	// The executor handles converting to absolute path internally.
	pmWorkspace := filepath.Join(workDir, pmID)
	logger.Info("Using PM workspace at: %s", pmWorkspace)

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

	// Bootstrap detection now runs in SETUP state (entered from WAITING).
	// This keeps the constructor lightweight and allows PM to register quickly.

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
			d.logger.Info("🛑 Graceful shutdown, exiting cleanly from %s", d.GetCurrentState())
			return nil
		default:
			// Capture state before executing handler
			stateBefore := d.GetCurrentState()

			// Execute current state
			nextState, err := d.executeState(ctx)

			// If the context is cancelled, exit cleanly — don't try to transition
			// to ERROR or log noisy errors during graceful shutdown.
			if err != nil && ctx.Err() != nil {
				d.logger.Info("🛑 Graceful shutdown, exiting cleanly from %s", stateBefore)
				return nil //nolint:nilerr // intentional: clean exit on shutdown
			}
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
	case StateSetup:
		return d.handleSetup(ctx)
	case StateWorking:
		return d.handleWorking(ctx)
	case StateAwaitUser:
		return d.handleAwaitUser(ctx)
	case StatePreview:
		return d.handlePreview(ctx)
	case StateAwaitArchitect:
		return d.handleAwaitArchitect(ctx)
	case proto.StateSuspend:
		nextState, _, err := d.HandleSuspend(ctx)
		if err != nil {
			return proto.StateError, fmt.Errorf("suspend handling failed: %w", err)
		}
		return nextState, nil
	default:
		return proto.StateError, fmt.Errorf("unknown state: %s", currentState)
	}
}

// handleWaiting is the idle state where PM waits for user actions.
// User actions (StartInterview, UploadSpec) modify state directly via public methods.
// Architect messages are handled in AWAIT_ARCHITECT, not here.
func (d *Driver) handleWaiting(ctx context.Context) (proto.State, error) {
	d.logger.Debug("🎯 PM in WAITING state - waiting for user action")

	// Check if bootstrap detection has run - if not, transition to SETUP first.
	// This ensures PM has accurate bootstrap state before any user interaction.
	if d.GetBootstrapRequirements() == nil {
		d.logger.Info("🔧 Bootstrap detection needed - transitioning to SETUP")
		return StateSetup, nil
	}

	// Check for context cancellation
	select {
	case <-ctx.Done():
		d.logger.Info("⏹️  Context canceled while in WAITING")
		return proto.StateDone, nil
	default:
		// No state change - sleep briefly to avoid tight loop
		// Direct method calls (StartInterview/UploadSpec) modify state directly,
		// and the Run loop will detect the change and route to the new handler
		time.Sleep(waitingPollInterval)
		return StateWaiting, nil
	}
}

// handleSetup runs bootstrap detection and creates git mirror if needed.
// Always returns to WAITING when complete.
func (d *Driver) handleSetup(ctx context.Context) (proto.State, error) {
	d.logger.Info("🔧 PM SETUP: Running bootstrap detection")

	// Run bootstrap detection - this creates the mirror if git is configured
	d.detectAndStoreBootstrapRequirements(ctx)

	d.logger.Info("✅ PM SETUP complete - returning to WAITING")
	return StateWaiting, nil
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
// PM is the sole authority on bootstrap status - detection runs against PM workspace.
func (d *Driver) detectAndStoreBootstrapRequirements(ctx context.Context) (*BootstrapRequirements, bool) {
	// Use agent workspace (projectDir/agentID) for detection since that's where files are committed
	agentWorkspace := filepath.Join(d.workDir, d.GetAgentID())
	d.logger.Info("🔍 Detecting bootstrap requirements in %s", agentWorkspace)

	detector := NewBootstrapDetector(agentWorkspace)
	reqs, err := detector.Detect(ctx)
	if err != nil {
		d.logger.Warn("Bootstrap detection failed: %v", err)
		return nil, false
	}

	// Store bootstrap requirements in state BEFORE mirror refresh.
	// This ensures state is available immediately, even if refresh is slow.
	// Note: Bootstrap spec rendering is handled by the architect, not PM.
	// PM only stores the detection result; architect renders the full spec from requirement IDs.
	// Platform is NOT detected programmatically - PM LLM handles platform confirmation with user.
	d.SetStateData(StateKeyBootstrapRequirements, reqs)

	// Update demo availability based on bootstrap status
	d.updateDemoAvailable(reqs)

	// Ensure git mirror exists and workspaces are up-to-date.
	// This creates the mirror if git is configured but mirror doesn't exist,
	// or refreshes existing mirrors to get latest changes.
	detector.RefreshMirrorAndWorkspaces(ctx)

	d.logger.Info("✅ Bootstrap detection complete: %d components needed",
		len(reqs.MissingComponents))

	// Log container status details
	cs := reqs.ContainerStatus
	if cs.HasValidContainer {
		d.logger.Info("🐳 Container: valid (%s, image: %s)", cs.ContainerName, truncateImageID(cs.PinnedImageID))
	} else if cs.IsBootstrapFallback {
		if cs.DockerfileExists {
			d.logger.Info("🐳 Container: bootstrap fallback, %s exists (can build project container)", cs.DockerfilePath)
		} else {
			d.logger.Info("🐳 Container: bootstrap fallback, need to create %s", cs.DockerfilePath)
		}
	} else if cs.DockerfileExists {
		d.logger.Info("🐳 Container: not configured, but %s exists (can build)", cs.DockerfilePath)
	} else {
		d.logger.Info("🐳 Container: %s", cs.Reason)
	}

	// Check if any components are missing
	needsBootstrap := reqs.HasAnyMissingComponents()
	if needsBootstrap {
		d.logger.Info("📋 Bootstrap needed:")
		logBootstrapComponent(d.logger, "  Project config", reqs.NeedsProjectConfig)
		logBootstrapComponent(d.logger, "  Git repository", reqs.NeedsGitRepo)
		logBootstrapComponent(d.logger, "  Dockerfile", reqs.NeedsDockerfile)
		logBootstrapComponent(d.logger, "  Makefile", reqs.NeedsMakefile)
		logBootstrapComponent(d.logger, "  Gitignore", reqs.NeedsGitignore)
		logBootstrapComponent(d.logger, "  Knowledge graph", reqs.NeedsKnowledgeGraph)
		logBootstrapComponent(d.logger, "  Claude Code", reqs.NeedsClaudeCode)
	}

	return reqs, needsBootstrap
}

// GetBootstrapRequirements returns the detected bootstrap requirements.
// Returns nil if bootstrap detection hasn't run yet or failed.
func (d *Driver) GetBootstrapRequirements() *BootstrapRequirements {
	reqs, _ := utils.GetStateValue[*BootstrapRequirements](d.BaseStateMachine, StateKeyBootstrapRequirements)
	return reqs
}

// IsDemoAvailable returns true if demo mode should be available.
// Demo is available when bootstrap has completed (no missing components).
// PM is the sole authority on demo availability.
func (d *Driver) IsDemoAvailable() bool {
	return d.demoAvailable
}

// EnsureBootstrapChecked verifies that bootstrap detection has been run.
// Bootstrap detection runs in SETUP state (deferred from constructor for non-blocking startup).
// This method just checks if state exists - it does NOT re-run detection.
// Detection is only re-run after spec completion to verify bootstrap requirements.
func (d *Driver) EnsureBootstrapChecked(_ context.Context) error {
	// Bootstrap detection runs in SETUP state, so it may not exist yet
	// if PM is still initializing. This is expected - not an error.
	return nil
}

// truncateImageID shortens a Docker image ID for display.
// Image IDs are typically long SHA256 hashes - truncate to first 12 chars for readability.
func truncateImageID(imageID string) string {
	// Remove sha256: prefix if present
	if len(imageID) > 7 && imageID[:7] == "sha256:" {
		imageID = imageID[7:]
	}
	if len(imageID) > 12 {
		return imageID[:12]
	}
	return imageID
}

// logBootstrapComponent logs a single bootstrap component status with visual indicators.
func logBootstrapComponent(logger *logx.Logger, name string, needed bool) {
	if needed {
		logger.Info("%s: ❌ missing", name)
	} else {
		logger.Info("%s: ✓ ok", name)
	}
}

// sendBootstrapSpecRequest renders and sends a bootstrap spec (Spec 0) to the architect.
// This allows infrastructure work to begin while the user interview continues.
func (d *Driver) sendBootstrapSpecRequest(_ context.Context) error {
	reqs := d.GetBootstrapRequirements()
	if reqs == nil {
		return fmt.Errorf("no bootstrap requirements available")
	}

	reqIDs := reqs.ToRequirementIDs()
	if len(reqIDs) == 0 {
		return fmt.Errorf("no bootstrap requirement IDs to send")
	}

	// Render the bootstrap spec
	rendered, err := specrender.RenderBootstrapSpec(reqIDs, d.logger)
	if err != nil {
		return fmt.Errorf("failed to render bootstrap spec: %w", err)
	}

	// Create approval request payload with bootstrap spec as Content
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeSpec,
		Content:      rendered, // The complete rendered bootstrap spec
		Reason:       "Bootstrap infrastructure spec (Spec 0) - can be implemented while user interview continues",
		Context:      "Bootstrap spec submitted before user feature spec",
		Confidence:   proto.ConfidenceHigh,
		Metadata: map[string]string{
			"spec_type": "bootstrap",
		},
		// Empty BootstrapRequirements - Content IS the complete spec
	}

	// Create REQUEST message
	requestMsg := &proto.AgentMsg{
		ID:        fmt.Sprintf("pm-bootstrap-spec-%d", time.Now().UnixNano()),
		Type:      proto.MsgTypeREQUEST,
		FromAgent: d.GetAgentID(),
		ToAgent:   "architect",
		Payload:   proto.NewApprovalRequestPayload(approvalPayload),
	}

	// Send via dispatcher
	if err := d.dispatcher.DispatchMessage(requestMsg); err != nil {
		return fmt.Errorf("failed to dispatch bootstrap spec REQUEST: %w", err)
	}

	// Mark bootstrap spec as sent
	d.SetStateData(StateKeyBootstrapSpecSent, true)
	d.SetStateData(StateKeyAwaitingSpecType, "bootstrap")

	d.logger.Info("📤 Sent bootstrap spec (Spec 0) to architect (%d bytes, %d requirements, id: %s)",
		len(rendered), len(reqIDs), requestMsg.ID)

	return nil
}

// updateDemoAvailable updates the demo availability flag based on bootstrap requirements.
// Called after bootstrap detection to update the flag.
// Demo requires: working container + Makefile with run target.
// This is separate from full bootstrap completion - demo can work without
// .gitignore, knowledge graph, etc.
func (d *Driver) updateDemoAvailable(reqs *BootstrapRequirements) {
	if reqs == nil {
		return
	}
	wasAvailable := d.demoAvailable
	d.demoAvailable = reqs.CanRunDemo()

	// Log state change (if logger is available)
	if d.logger != nil {
		if d.demoAvailable && !wasAvailable {
			d.logger.Info("🎮 Demo mode is now available (bootstrap complete)")
		} else if !d.demoAvailable && wasAvailable {
			d.logger.Info("🎮 Demo mode is no longer available (bootstrap incomplete)")
		}
	}
}

// GetDraftSpec returns the draft specification markdown if available.
// This is used by the WebUI to display the spec in PREVIEW state.
// Returns only the user feature spec - bootstrap content is rendered by the architect.
// Returns empty string if no draft spec is available.
func (d *Driver) GetDraftSpec() string {
	return utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
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
//
//nolint:cyclop // Complexity from bootstrap spec detection and state transition logic is inherent
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
	ctx := context.Background()
	reqs, needsBootstrap := d.detectAndStoreBootstrapRequirements(ctx)
	if needsBootstrap && reqs != nil {
		// Inject only config deficits that PM needs to gather from user.
		// Technical bootstrap prerequisites (Dockerfile, Makefile, etc.) are opaque to PM.
		var configDeficits []string
		if reqs.NeedsProjectConfig {
			configDeficits = append(configDeficits, "project name and platform")
		}
		if reqs.NeedsGitRepo {
			configDeficits = append(configDeficits, "git repository URL")
		}
		if len(configDeficits) > 0 {
			d.contextManager.AddMessage("system",
				fmt.Sprintf("Project setup needed. Please gather: %s. "+
					"After collecting this info, call the bootstrap tool.", strings.Join(configDeficits, ", ")))
		} else {
			// Config is complete but technical prerequisites are missing.
			// Send bootstrap spec (Spec 0) to architect immediately.
			bootstrapSpecSent := utils.GetStateValueOr[bool](d.BaseStateMachine, StateKeyBootstrapSpecSent, false)
			if !bootstrapSpecSent {
				if sendErr := d.sendBootstrapSpecRequest(ctx); sendErr != nil {
					d.logger.Warn("⚠️ Failed to send bootstrap spec at interview start: %v", sendErr)
					d.contextManager.AddMessage("system",
						"Project configuration is complete. Technical setup is in progress.")
				} else {
					// Bootstrap spec sent - block in AWAIT_ARCHITECT until acknowledged
					d.logger.Info("📤 Bootstrap spec (Spec 0) sent at interview start, blocking in AWAIT_ARCHITECT")
					if err := d.TransitionTo(ctx, StateAwaitArchitect, nil); err != nil {
						d.logger.Error("❌ Failed to transition to AWAIT_ARCHITECT: %v", err)
						return fmt.Errorf("failed to transition to AWAIT_ARCHITECT: %w", err)
					}
					return nil
				}
			} else {
				d.contextManager.AddMessage("system",
					"Project configuration is complete. Technical setup is in progress.")
			}
		}
	}

	// Decide initial state based on bootstrap needs
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
	_, needsBootstrap := d.detectAndStoreBootstrapRequirements(context.Background())
	if needsBootstrap {
		// Add uploaded spec content and bootstrap instructions to context
		// Note: Infrastructure requirements (Dockerfile, Makefile, etc.) are opaque to PM LLM
		specMessage := fmt.Sprintf(`# User Uploaded Specification File

The user has uploaded a specification file (%d bytes). **Parse this spec to extract project information before asking the user any questions.**

**Look for these details in the spec:**
1. **Project Name** - Often in title, frontmatter, or introduction
2. **Git Repository URL** - May be mentioned in deployment, setup, or configuration sections
3. **Primary Platform** - Look for language/framework mentions (go, python, node, rust, etc.)

**After parsing the spec:**
- Extract any values you find for project_name, git_url, and platform
- ONLY ask the user for values that are genuinely missing or ambiguous in the spec
- Do NOT ask the user to re-provide information that's clearly stated in their spec
- Once you have all three values, call the bootstrap tool

**The uploaded specification:**
`+"```markdown\n%s\n```",
			len(markdown), markdown)

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
