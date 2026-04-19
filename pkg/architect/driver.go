// Package architect provides the architect agent implementation for the orchestrator system.
// The architect processes specifications, generates stories, and coordinates with coder agents.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/github"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/mirror"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// Tool signal constants - all signals now use centralized constants from tools package.
// See tools.Signal* constants for the complete list.

// Review type constants for streak tracking.
const (
	ReviewTypeBudget = "budget"
	ReviewTypeCode   = "code"
	ReviewTypePlan   = "plan"
)

// Budget review streak limits.
const (
	BudgetReviewSoftLimit = 3 // Inject warning into prompt at this streak count
	BudgetReviewHardLimit = 6 // Auto-reject without calling LLM at this streak count
)

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

	// Container upgrade signaling (set by coder when Claude Code upgraded in-place)
	NeedsContainerUpgrade  bool   // True if container image needs rebuild
	ContainerUpgradeReason string // What triggered the upgrade (e.g., "claude_code")

	// Maintenance items logged by architect during reviews via add_maintenance_item tool
	Items []tools.MaintenanceItem
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
	maintenance             MaintenanceTracker                    // Maintenance cycle state
	toolLoop                *toolloop.ToolLoop                    // Tool loop for LLM interactions
	renderer                *templates.Renderer                   // Template renderer for prompts
	queue                   *Queue                                // Story queue manager
	escalationHandler       *EscalationHandler                    // Escalation handler
	dispatcher              *dispatch.Dispatcher                  // Dispatcher for sending messages
	logger                  *logx.Logger                          // Logger with proper agent prefixing
	executor                *execpkg.ArchitectExecutor            // Container executor for file access tools
	chatService             ChatServiceInterface                  // Chat service for escalations (nil check required)
	devChatService          *chat.Service                         // Chat service for dev-chat listener (channel-scoped reads)
	gitHubClient            GitHubMergeClient                     // GitHub client for merge operations (nil = create from config)
	shutdownCtx             context.Context                       //nolint:containedctx // Driver-level context for graceful shutdown of background tasks
	shutdownCancel          context.CancelFunc                    // Cancel function for shutdownCtx
	questionsCh             chan *proto.AgentMsg                  // Bi-directional channel for requests (specs, questions, approvals)
	replyCh                 <-chan *proto.AgentMsg                // Read-only channel for replies
	persistenceChannel      chan<- *persistence.Request           // Channel for database operations
	scopeWidener            *ScopeWidener                         // Tracks failure recurrence for scope auto-escalation
	workDir                 string                                // Workspace directory
	reviewStreaks           map[string]map[string]int             // Per-coder, per-review-type consecutive NEEDS_CHANGES count
	openIncidents           map[string]*proto.Incident            // Durable incidents keyed by incident ID
	monitoringIdleSince     time.Time                             // Debounce guard for system_idle detection (not persisted)
	pmAllCompleteNotified   bool                                  // Guard: PM "all stories complete" notification already sent
	pmAllTerminalNotified   bool                                  // Guard: PM "all stories terminal" (with failures) notification already sent
}

// GitHubMergeClient defines the subset of GitHub operations needed for merge requests.
// This interface allows for testing with mocks.
type GitHubMergeClient interface {
	ListPRsForBranch(ctx context.Context, branch string) ([]github.PullRequest, error)
	CreatePR(ctx context.Context, opts github.PRCreateOptions) (*github.PullRequest, error)
	MergePRWithResult(ctx context.Context, ref string, opts github.PRMergeOptions) (*github.MergeResult, error)
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

	escalationHandler := NewEscalationHandler(queue)
	logger := logx.NewLogger(architectID)

	// Create BaseStateMachine with architect transition table
	sm := agent.NewBaseStateMachine(architectID, StateWaiting, nil, architectTransitions)

	// Create driver-level context for graceful shutdown of background tasks
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())

	return &Driver{
		BaseStateMachine:   sm,
		agentContexts:      make(map[string]*contextmgr.ContextManager), // Initialize context map
		reviewStreaks:      make(map[string]map[string]int),             // Initialize streak tracking
		openIncidents:      make(map[string]*proto.Incident),            // Initialize incident tracking
		toolLoop:           nil,                                         // Set via SetLLMClient
		renderer:           renderer,
		workDir:            workDir,
		queue:              queue,
		escalationHandler:  escalationHandler,
		dispatcher:         dispatcher,
		logger:             logger,
		persistenceChannel: persistenceChannel,
		scopeWidener:       NewScopeWidener(),
		shutdownCtx:        shutdownCtx,
		shutdownCancel:     shutdownCancel,
		// Channels will be set during Attach()
		questionsCh: nil,
		replyCh:     nil,
	}
}

// NewArchitect creates a new architect with LLM integration.
// Uses shared LLM factory for proper rate limiting across all agents.
func NewArchitect(ctx context.Context, architectID string, dispatcher *dispatch.Dispatcher, workDir string, persistenceChannel chan<- *persistence.Request, llmFactory *agent.LLMClientFactory, chatService ChatServiceInterface, devChatService *chat.Service) (*Driver, error) {
	// Check for context cancellation before starting construction
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("architect construction cancelled: %w", ctx.Err())
	default:
	}

	// Architect constructor with model configuration validation

	// Get config for other settings
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Get model name (respects airplane mode override)
	modelName := config.GetEffectiveArchitectModel()

	// Create architect without LLM client first (chicken-and-egg: client needs architect as StateProvider)
	architect := NewDriver(architectID, modelName, dispatcher, workDir, persistenceChannel)

	// Set chat service for escalations (may be nil in tests)
	architect.chatService = chatService
	// Set dev chat service for development channel listener (may be nil in tests)
	architect.devChatService = devChatService

	// NOTE: Workspace cloning is deferred to SETUP state.
	// The architect boots in WAITING state with just a mountable directory (already created at startup).
	// When the architect receives work and transitions to SETUP, it will clone from the mirror.
	// This allows PM bootstrap to create the mirror before the architect needs it.

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
	// Use effective model (respects airplane mode override)
	modelName := config.GetEffectiveArchitectModel()

	cm = contextmgr.NewContextManagerWithModel(modelName)

	// Note: Chat service integration for per-agent contexts will be added
	// when needed - currently chat uses single architect context

	d.agentContexts[agentID] = cm
	d.logger.Debug("Created new context for agent %s", agentID)

	return cm
}

// ensureContextForStory ensures the architect's per-agent context is scoped to the correct story.
// Uses ContextManager.GetCurrentTemplate() for idempotent detection: if the template name
// (which encodes the story ID) already matches, this is a no-op. On story change (or first use),
// it resets the context with a fresh system prompt and clears review streaks.
func (d *Driver) ensureContextForStory(ctx context.Context, agentID, storyID string) (*contextmgr.ContextManager, error) {
	cm := d.getContextForAgent(agentID)
	templateName := fmt.Sprintf("agent-%s-story-%s", agentID, storyID)

	if cm.GetCurrentTemplate() == templateName {
		return cm, nil // Already scoped to this story
	}

	// Story changed (or first use) — reset context with new system prompt
	d.logger.Info("Story context change detected for agent %s: %q → %q",
		agentID, cm.GetCurrentTemplate(), templateName)

	systemPrompt, err := d.buildSystemPrompt(ctx, agentID, storyID)
	if err != nil {
		return nil, fmt.Errorf("failed to build system prompt for agent %s story %s: %w",
			agentID, storyID, err)
	}

	cm.ResetForNewTemplate(templateName, systemPrompt)
	d.clearReviewStreaks(agentID)
	d.logger.Info("Reset context for agent %s (story %s)", agentID, storyID)
	return cm, nil
}

// ResetAgentContext resets the context for an agent when they start a new story.
// Delegates to ensureContextForStory using the dispatcher lease as the authoritative story source.
func (d *Driver) ResetAgentContext(agentID string) error {
	storyID := d.dispatcher.GetStoryForAgent(agentID)
	if storyID == "" {
		return fmt.Errorf("no story found for agent %s", agentID)
	}
	_, err := d.ensureContextForStory(context.Background(), agentID, storyID)
	return err
}

// incrementReviewStreak increments the consecutive NEEDS_CHANGES streak for a coder/review-type pair.
func (d *Driver) incrementReviewStreak(coderID, reviewType string) int {
	if d.reviewStreaks[coderID] == nil {
		d.reviewStreaks[coderID] = make(map[string]int)
	}
	d.reviewStreaks[coderID][reviewType]++
	return d.reviewStreaks[coderID][reviewType]
}

// resetReviewStreak resets the streak for a coder/review-type pair to 0.
//
//nolint:unparam // reviewType is always "budget" in Phase 1; code/plan enforcement planned for Phase 2
func (d *Driver) resetReviewStreak(coderID, reviewType string) {
	if d.reviewStreaks[coderID] != nil {
		d.reviewStreaks[coderID][reviewType] = 0
	}
}

// getReviewStreak returns the current streak count for a coder/review-type pair.
func (d *Driver) getReviewStreak(coderID, reviewType string) int {
	if d.reviewStreaks[coderID] == nil {
		return 0
	}
	return d.reviewStreaks[coderID][reviewType]
}

// clearReviewStreaks removes all review streaks for a coder (called on story completion/exit).
func (d *Driver) clearReviewStreaks(coderID string) {
	delete(d.reviewStreaks, coderID)
}

// makeOnLLMErrorCallback creates a callback function for checkpointing context on LLM errors.
// This helps with debugging by persisting the conversation state when errors occur.
func (d *Driver) makeOnLLMErrorCallback(contextType string) func(error, *contextmgr.ContextManager) {
	if d.persistenceChannel == nil {
		return nil
	}

	return func(err error, cm *contextmgr.ContextManager) {
		if cm == nil {
			d.logger.Warn("⚠️ Cannot checkpoint context: context manager is nil")
			return
		}

		// Serialize messages to JSON
		messages := cm.GetMessages()
		messagesJSON, jsonErr := json.Marshal(messages)
		if jsonErr != nil {
			d.logger.Error("❌ Failed to serialize context for checkpoint: %v", jsonErr)
			return
		}

		// Get session ID from config
		cfg, cfgErr := config.GetConfig()
		if cfgErr != nil {
			d.logger.Error("❌ Failed to get config for checkpoint: %v", cfgErr)
			return
		}

		// Create context checkpoint with error suffix
		checkpointType := fmt.Sprintf("%s_error_%d", contextType, time.Now().Unix())

		// Send to persistence channel (non-blocking to avoid deadlock)
		select {
		case d.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpSaveAgentContext,
			Data: &persistence.AgentContext{
				SessionID:    cfg.SessionID,
				AgentID:      d.GetAgentID(),
				ContextType:  checkpointType,
				MessagesJSON: string(messagesJSON),
			},
		}:
			d.logger.Info("📸 Context checkpoint saved: type=%s, messages=%d, error=%v",
				checkpointType, len(messages), err)
		default:
			d.logger.Warn("⚠️ Persistence channel full, checkpoint skipped")
		}
	}
}

// buildSystemPrompt creates the comprehensive system prompt for an agent context.
// This prompt contains persistent context for the entire story lifecycle.
func (d *Driver) buildSystemPrompt(ctx context.Context, agentID, storyID string) (string, error) {
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
	// Note: Knowledge packs are delivered via request content, not via story records
	data := &templates.TemplateData{
		Extra: map[string]any{
			"AgentID":        agentID,
			"StoryID":        storyID,
			"StoryTitle":     story.Title,
			"StoryContent":   story.Content,
			"SpecID":         story.SpecID,
			"ClaudeCodeMode": claudeCodeMode,
		},
	}

	// Load and add MAESTRO.md content if available (formatted with trust boundary)
	if d.workDir != "" {
		mirrorMgr := mirror.NewManager(d.workDir)
		if maestroContent, err := mirrorMgr.LoadMaestroMd(ctx); err == nil && maestroContent != "" {
			data.Extra["MaestroMd"] = utils.FormatMaestroMdForPrompt(maestroContent)
		}
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

// AddMaintenanceItem implements the tools.MaintenanceLog interface.
// Appends an item to the maintenance tracker for processing in the next maintenance cycle,
// and persists it to the database for durability across restarts.
func (d *Driver) AddMaintenanceItem(item tools.MaintenanceItem) {
	d.maintenance.mutex.Lock()
	defer d.maintenance.mutex.Unlock()
	d.maintenance.Items = append(d.maintenance.Items, item)
	d.logger.Info("🔧 Maintenance item logged: [%s] %s (source: %s)", item.Priority, item.Description, item.Source)

	// Fire-and-forget persistence
	persistence.PersistMaintenanceItem(&persistence.MaintenanceItemRecord{
		Description: item.Description,
		Priority:    item.Priority,
		Source:      item.Source,
		CreatedAt:   item.AddedAt,
	}, d.persistenceChannel)
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
	// Cancel driver-level context to stop background tasks (e.g., maintenance)
	if d.shutdownCancel != nil {
		d.logger.Info("Cancelling background tasks")
		d.shutdownCancel()
	}

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
	d.SetStateData(StateKeyStartedAt, time.Now().UTC())

	// Run the state machine loop.
	for {
		// Check context cancellation — exit cleanly on graceful shutdown.
		select {
		case <-ctx.Done():
			d.logger.Info("🛑 Graceful shutdown, exiting cleanly from %s", d.GetCurrentState())
			return nil
		default:
		}

		// Check current state for recycling or terminal exit.
		currentState := d.GetCurrentState()
		if currentState == StateDone {
			// Recycle to WAITING for new specs instead of exiting
			d.logger.Info("✅ All stories completed - recycling to WAITING for new specs")
			if err := d.TransitionTo(ctx, StateWaiting, nil); err != nil {
				d.logger.Error("Failed to transition from DONE to WAITING: %v", err)
				break
			}
			continue
		}
		if currentState == StateError {
			break
		}

		// State processing (WAITING states are handled quietly)

		// Process current state.
		nextState, err := d.processCurrentState(ctx)

		// If the context is cancelled, exit cleanly — don't try to transition
		// to ERROR or log noisy errors during graceful shutdown.
		if err != nil && ctx.Err() != nil {
			d.logger.Info("🛑 Graceful shutdown, exiting cleanly from %s", currentState)
			return nil //nolint:nilerr // intentional: clean exit on shutdown
		}
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
	case StateSetup:
		// SETUP state - ensure workspace is ready before processing requests.
		return d.handleSetup(ctx)
	case StateDispatching:
		return d.handleDispatching(ctx)
	case StateMonitoring:
		return d.handleMonitoring(ctx)
	case StateRequest:
		return d.handleRequest(ctx)
	case StateEscalated:
		return d.handleEscalated(ctx)
	case StateDone:
		// Recycle to WAITING for new specs (handled in Run loop above)
		return StateWaiting, nil
	case proto.StateSuspend:
		nextState, _, err := d.HandleSuspend(ctx)
		if err != nil {
			return StateError, fmt.Errorf("suspend handling failed: %w", err)
		}
		return nextState, nil
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
			story, exists := d.queue.GetStory(requeueRequest.StoryID)
			if !exists {
				d.logger.Error("❌ Story %s not found for requeue", requeueRequest.StoryID)
				continue
			}

			// Increment attempt budget and store failure reason
			attemptCount, attemptMax, _ := d.queue.IncrementBudget(requeueRequest.StoryID, BudgetClassAttempt)
			d.queue.UpdateStoryFailureMetadata(requeueRequest.StoryID, requeueRequest.Reason, requeueRequest.FailureInfo)

			d.logger.Info("🔄 Story %s attempt budget: %d/%d (reason: %s)",
				requeueRequest.StoryID, attemptCount, attemptMax, requeueRequest.Reason)

			// Check retry limit - mark story as failed but architect continues
			if d.queue.IsBudgetExhausted(requeueRequest.StoryID, BudgetClassAttempt) {
				d.logger.Warn("🚨 Story %s abandoned after %d attempts (last failure: %s); architect continues",
					requeueRequest.StoryID, story.AttemptCount, requeueRequest.Reason)

				// Mark story as failed
				if err := story.SetStatus(StatusFailed); err != nil {
					d.logger.Warn("Failed to mark story %s as failed: %v", requeueRequest.StoryID, err)
				}

				// Persist failed status to database
				if d.persistenceChannel != nil {
					persistence.PersistStory(story.ToPersistenceStory(), d.persistenceChannel)
				}

				// Create and persist a failure record for this terminal failure
				d.persistFailureRecord(requeueRequest.StoryID, story.AttemptCount,
					requeueRequest.Reason, proto.FailureActionMarkFailed,
					proto.FailureResolutionFailed, requeueRequest.FailureInfo)

				// Notify PM that story is being abandoned
				if requeueRequest.FailureInfo != nil {
					d.notifyPMOfBlockedStory(ctx, story, requeueRequest.FailureInfo, false, false)
				}

				// Check if all stories are now terminal — if so, notify PM and finish
				if d.queue.AllStoriesTerminal() {
					d.logger.Info("📋 All stories are terminal (done or failed). Transitioning to DONE.")
					if err := d.notifyPMAllStoriesTerminal(ctx); err != nil {
						d.logger.Warn("⚠️ Failed to notify PM of all stories terminal: %v", err)
					}
					if transErr := d.TransitionTo(ctx, StateDone, nil); transErr != nil {
						d.logger.Error("❌ Failed to transition to DONE state: %v", transErr)
					}
				}
				continue
			}

			// Record failure for scope widening detection
			if requeueRequest.FailureInfo != nil && d.scopeWidener != nil {
				fi := requeueRequest.FailureInfo
				normalizedKind := proto.NormalizeFailureKind(fi.Kind)
				d.scopeWidener.RecordFailure(normalizedKind, requeueRequest.StoryID, fi.Explanation)

				// Check for mechanical scope widening — auto-escalate when same kind recurs across multiple stories
				currentScope := fi.ScopeGuess
				if currentScope == "" {
					currentScope = proto.FailureScopeAttempt
				}
				widenedScope := d.scopeWidener.CheckForWidening(normalizedKind, fi.Explanation, currentScope)
				if widenedScope != currentScope {
					d.logger.Warn("🔍 Scope widened for %s failures: %s → %s (recurrence threshold reached)",
						normalizedKind, currentScope, widenedScope)
					fi.ResolvedScope = widenedScope
				}
			}

			// Architect triage: resolve kind and scope with mechanical defaults
			if requeueRequest.FailureInfo != nil {
				fi := requeueRequest.FailureInfo
				fi.Kind = proto.NormalizeFailureKind(fi.Kind)
				if fi.ResolvedKind == "" {
					fi.ResolvedKind = fi.Kind
				}
				if fi.ResolvedScope == "" {
					// Mechanical defaults: story_invalid → story, others → attempt
					switch fi.Kind {
					case proto.FailureKindStoryInvalid:
						fi.ResolvedScope = proto.FailureScopeStory
					default:
						fi.ResolvedScope = proto.FailureScopeAttempt
					}
				}
				d.logger.Info("🔍 Triage: kind=%s, resolved_scope=%s (story %s)",
					fi.ResolvedKind, fi.ResolvedScope, requeueRequest.StoryID)
			}

			// affectedIDs is captured before holding so the prerequisite case can reference the list.
			var affectedIDs []string
			// For epoch/system scoped failures, hold affected stories to prevent wasted work
			if requeueRequest.FailureInfo != nil {
				fi := requeueRequest.FailureInfo
				if fi.ResolvedScope == proto.FailureScopeEpoch || fi.ResolvedScope == proto.FailureScopeSystem {
					affectedIDs = d.queue.GetActiveStoriesForScope(fi.ResolvedScope, requeueRequest.StoryID)
					if len(affectedIDs) > 0 {
						reason := fmt.Sprintf("%s_%s_hold", fi.ResolvedScope, fi.ResolvedKind)
						for _, id := range affectedIDs {
							// Cancel in-flight agents before holding (planning/coding stories have active coders)
							if agentID := d.queue.GetAssignedAgent(id); agentID != "" {
								cancelEffect := &CancelAgentEffect{
									AgentID:    agentID,
									StoryID:    id,
									Reason:     reason,
									Dispatcher: d.dispatcher,
								}
								if cancelErr := d.ExecuteEffect(ctx, cancelEffect); cancelErr != nil {
									d.logger.Warn("Failed to cancel agent %s for story %s: %v", agentID, id, cancelErr)
								}
							}
							if holdErr := d.queue.HoldStory(id, reason, "architect", fi.ID, fi.Explanation); holdErr != nil {
								d.logger.Warn("Failed to hold story %s for %s-scoped failure: %v", id, fi.ResolvedScope, holdErr)
							}
						}
						d.logger.Warn("🔒 Held %d stories for %s-scoped %s failure (trigger: story %s)",
							len(affectedIDs), fi.ResolvedScope, fi.ResolvedKind, requeueRequest.StoryID)
					}

					// System-scoped: suppress all new dispatch until manually resolved
					if fi.ResolvedScope == proto.FailureScopeSystem {
						d.queue.SuppressDispatch(fmt.Sprintf("system-scoped %s failure: %s", fi.ResolvedKind, fi.Explanation))
						d.logger.Warn("⛔ Dispatch suppressed: system-scoped %s failure (story %s)",
							fi.ResolvedKind, requeueRequest.StoryID)
					}
				}
			}

			// If this is a classified failure, give the architect a chance to review and act.
			storyEdited := false
			if requeueRequest.FailureInfo != nil {
				fi := requeueRequest.FailureInfo
				isStoryInvalid := fi.Kind == proto.FailureKindStoryInvalid

				// For story_invalid failures, check rewrite budget BEFORE attempting rewrite.
				// If exhausted, mark failed immediately — don't let handleBlockedRequeue edit again.
				if isStoryInvalid && d.queue.IsBudgetExhausted(requeueRequest.StoryID, BudgetClassRewrite) {
					d.logger.Warn("🚨 Story %s rewrite budget exhausted (%d/%d); marking as failed",
						requeueRequest.StoryID, MaxStoryRewrites, MaxStoryRewrites)
					if err := story.SetStatus(StatusFailed); err != nil {
						d.logger.Warn("Failed to mark story %s as failed: %v", requeueRequest.StoryID, err)
					}
					if d.persistenceChannel != nil {
						persistence.PersistStory(story.ToPersistenceStory(), d.persistenceChannel)
					}
					d.persistFailureRecord(requeueRequest.StoryID, story.AttemptCount,
						requeueRequest.Reason, proto.FailureActionMarkFailed,
						proto.FailureResolutionFailed, fi)
					d.notifyPMOfBlockedStory(ctx, story, fi, false, false)
					if d.queue.AllStoriesTerminal() {
						d.logger.Info("📋 All stories are terminal. Transitioning to DONE.")
						if err := d.notifyPMAllStoriesTerminal(ctx); err != nil {
							d.logger.Warn("⚠️ Failed to notify PM of all stories terminal: %v", err)
						}
						if transErr := d.TransitionTo(ctx, StateDone, nil); transErr != nil {
							d.logger.Error("❌ Failed to transition to DONE: %v", transErr)
						}
					}
					continue
				}

				// Kind-specific recovery routing
				switch {
				case isStoryInvalid:
					// Story content is the problem — hold, rewrite, release
					if holdErr := d.queue.HoldStory(requeueRequest.StoryID, "story_redraft", "architect", fi.ID, fi.Explanation); holdErr != nil {
						d.logger.Warn("Failed to hold story %s: %v", requeueRequest.StoryID, holdErr)
					}

					storyEdited = d.handleBlockedRequeue(ctx, requeueRequest, story, fi)

					// If story was rewritten, increment rewrite budget and persist a rewrite failure record
					if storyEdited {
						_, _, _ = d.queue.IncrementBudget(requeueRequest.StoryID, BudgetClassRewrite)
						d.persistFailureRecord(requeueRequest.StoryID, story.AttemptCount,
							fi.Explanation, proto.FailureActionRewriteStory,
							proto.FailureResolutionSucceeded, fi)
					}

					// Release from hold back to pending
					if story.GetStatus() == StatusOnHold {
						if _, releaseErr := d.queue.ReleaseHeldStories([]string{requeueRequest.StoryID}, "architect review complete"); releaseErr != nil {
							d.logger.Warn("Failed to release story %s from hold: %v", requeueRequest.StoryID, releaseErr)
						}
					}

				case fi.Kind == proto.FailureKindEnvironment:
					// Environment broken — retry with fresh workspace (default requeue path handles this).
					// For epoch/system scope, related stories are already held above.
					d.logger.Info("🔧 Environment failure for story %s — retrying with fresh workspace", requeueRequest.StoryID)

				case fi.Kind == proto.FailureKindPrerequisite:
					// External dependency issue — hold the story and ask PM to clarify with human.
					// Do NOT retry: the prerequisite won't be fixed until a human acts.
					if holdErr := d.queue.HoldStory(requeueRequest.StoryID, "awaiting_human", "architect", fi.ID, fi.Explanation); holdErr != nil {
						d.logger.Warn("Failed to hold story %s for prerequisite failure: %v", requeueRequest.StoryID, holdErr)
					}
					question := fmt.Sprintf("A prerequisite is missing or invalid: %s. Please check and resolve.", fi.Explanation)
					// Use pre-hold affectedIDs (stories are already on_hold, GetActiveStoriesForScope would miss them)
					heldIDs := append(append([]string{}, affectedIDs...), requeueRequest.StoryID)
					d.notifyPMOfClarificationNeeded(ctx, story, fi, question, heldIDs)
					d.persistFailureRecord(requeueRequest.StoryID, story.AttemptCount,
						requeueRequest.Reason, proto.FailureActionAskHuman,
						proto.FailureResolutionPending, fi)
					d.notifyPMOfBlockedStory(ctx, story, fi, false, false)
					d.logger.Info("🔑 Prerequisite failure for story %s — held and clarification requested from PM", requeueRequest.StoryID)
					continue

				default:
					// Transient or unclassified — just retry
					d.logger.Info("🔄 Retrying story %s after %s failure", requeueRequest.StoryID, fi.Kind)
				}
			}

			// Promote hotfix stories to full stories on requeue.
			// If a hotfix needed requeue, the "simple fix" assumption is wrong —
			// the next attempt should get the full planning phase.
			if story.IsHotfix || story.Express {
				d.logger.Info("📋 Promoting story %s from hotfix/express to full story (will get planning phase on next attempt)", requeueRequest.StoryID)
				story.IsHotfix = false
				story.Express = false
			}

			// Persist a retry_attempt failure record for every requeue (for budget reconstruction on resume)
			d.persistFailureRecord(requeueRequest.StoryID, story.AttemptCount,
				requeueRequest.Reason, proto.FailureActionRetryAttempt,
				proto.FailureResolutionRunning, requeueRequest.FailureInfo)

			// Change story status back to PENDING so it can be picked up again
			if err := d.queue.UpdateStoryStatus(requeueRequest.StoryID, StatusPending); err != nil {
				d.logger.Error("❌ Failed to requeue story %s: %v", requeueRequest.StoryID, err)
				continue
			}

			// Dispatch the story back to the work queue (like DISPATCHING state does).
			// Skip dispatch if system-wide suppression is active (e.g., system-scoped failure under repair).
			if suppressed, _ := d.queue.IsDispatchSuppressed(); suppressed {
				d.logger.Info("⛔ Dispatch suppressed — story %s stays pending until suppression is lifted", requeueRequest.StoryID)
				continue
			}
			if story.GetStatus() == StatusPending {
				// Create story message for dispatcher
				storyMsg := proto.NewAgentMsg(proto.MsgTypeSTORY, d.GetAgentID(), "coder")

				// Build story payload
				payloadData := map[string]any{
					proto.KeyTitle:           story.Title,
					proto.KeyEstimatedPoints: story.EstimatedPoints,
					proto.KeyDependsOn:       story.DependsOn,
					proto.KeyStoryType:       story.StoryType,
					proto.KeyExpress:         story.Express,  // Now false for promoted hotfixes
					proto.KeyIsHotfix:        story.IsHotfix, // Now false for promoted hotfixes
					proto.KeyRequirements:    []string{},     // Empty requirements for requeue
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

					// Notify PM after successful requeue+dispatch
					if requeueRequest.FailureInfo != nil {
						d.notifyPMOfBlockedStory(ctx, story, requeueRequest.FailureInfo, true, storyEdited)
					}
				}
			} else {
				d.logger.Error("❌ Story %s not in PENDING status after requeue", requeueRequest.StoryID)
			}
		}
	}
}

// persistFailureRecord creates and persists a failure record for any requeue event.
// This ensures budget reconstruction on resume has the data it needs.
func (d *Driver) persistFailureRecord(storyID string, attemptNumber int, reason string, action proto.FailureAction, resolutionStatus proto.FailureResolutionStatus, fi *proto.FailureInfo) {
	if d.persistenceChannel == nil {
		return
	}

	// sqliteTimestampFormat matches SQLite's strftime('%Y-%m-%dT%H:%M:%fZ','now') with fractional seconds.
	const sqliteTimestampFormat = "2006-01-02T15:04:05.000000Z"

	now := time.Now().UTC()
	record := &persistence.FailureRecord{
		ID:               proto.GenerateFailureID(),
		CreatedAt:        now.Format(sqliteTimestampFormat),
		UpdatedAt:        now.Format(sqliteTimestampFormat),
		StoryID:          storyID,
		AttemptNumber:    attemptNumber,
		Kind:             string(proto.FailureKindEnvironment),
		Explanation:      reason,
		Action:           string(action),
		ResolutionStatus: string(resolutionStatus),
	}
	if fi != nil {
		// Sanitize evidence and compute signature before persistence.
		fi.Sanitize(utils.SanitizeString)

		// Use FailureInfo's ID and timestamps if available, so downstream
		// flows (e.g., PM repair_complete) can correlate by fi.ID.
		if fi.ID != "" {
			record.ID = fi.ID
		}
		if !fi.CreatedAt.IsZero() {
			record.CreatedAt = fi.CreatedAt.Format(sqliteTimestampFormat)
		}
		if !fi.UpdatedAt.IsZero() {
			record.UpdatedAt = fi.UpdatedAt.Format(sqliteTimestampFormat)
		}

		// Context fields
		record.SpecID = fi.SpecID

		// Report fields
		record.Kind = string(fi.Kind)
		record.Source = string(fi.Source)
		record.ScopeGuess = string(fi.ScopeGuess)
		record.FailedState = fi.FailedState
		record.ToolName = fi.ToolName
		record.HumanNeededGuess = fi.HumanNeededGuess

		// Evidence (serialize to JSON for DB storage)
		if len(fi.Evidence) > 0 {
			if data, err := json.Marshal(fi.Evidence); err != nil {
				d.logger.Debug("Failed to marshal failure evidence: %v", err)
			} else {
				record.Evidence = string(data)
			}
		}

		// Triage fields (populated after architect review)
		record.ResolvedKind = string(fi.ResolvedKind)
		record.ResolvedScope = string(fi.ResolvedScope)
		record.HumanNeeded = fi.HumanNeeded
		if len(fi.AffectedStoryIDs) > 0 {
			if data, err := json.Marshal(fi.AffectedStoryIDs); err != nil {
				d.logger.Debug("Failed to marshal affected story IDs: %v", err)
			} else {
				record.AffectedStoryIDs = string(data)
			}
		}
		record.TriageSummary = fi.TriageSummary
		record.Owner = string(fi.Owner)
		record.ResolutionOutcome = fi.ResolutionOutcome

		// Analytics
		if len(fi.Tags) > 0 {
			if data, err := json.Marshal(fi.Tags); err != nil {
				d.logger.Debug("Failed to marshal failure tags: %v", err)
			} else {
				record.Tags = string(data)
			}
		}
		record.Model = fi.Model
		record.Provider = fi.Provider
		record.BaseCommit = fi.BaseCommit
		record.Signature = fi.Signature
	}
	// Compute signature for records without FailureInfo (fallback path).
	if record.Signature == "" {
		fallback := proto.NewFailureInfo(proto.FailureKind(record.Kind), record.Explanation, record.FailedState, record.ToolName)
		record.Signature = fallback.ComputeSignature()
	}
	persistence.PersistFailureRecord(record, d.persistenceChannel)
}

// handleBlockedRequeue gives the architect LLM a chance to review and act on a classified failure
// before the story is requeued. Follows the attemptStoryEdit pattern: inject context into the
// architect's per-agent conversation and run a single-turn toolloop with story_edit.
// This is best-effort: if the LLM call fails, we log and continue with the requeue as-is.
// Returns true if the story was modified (content replaced or notes appended).
func (d *Driver) handleBlockedRequeue(ctx context.Context, req *proto.StoryRequeueRequest, story *QueuedStory, fi *proto.FailureInfo) bool {
	// Get or create a context for the failed agent
	cm := d.getContextForAgent(req.AgentID)

	// Build the blocked requeue prompt from template
	prompt := d.buildBlockedRequeuePrompt(story, req.StoryID, fi)

	// Add prompt to existing per-agent context
	cm.AddMessage("blocked-requeue-prompt", prompt)

	// Create tool provider with story_edit + chat_post (for escalation)
	agentCtx := tools.AgentContext{
		ReadOnly: true,
		WorkDir:  "/mnt/architect",
	}
	allowedTools := []string{tools.ToolStoryEdit}
	toolProvider := tools.NewProvider(&agentCtx, allowedTools)

	storyEditTool, err := toolProvider.Get(tools.ToolStoryEdit)
	if err != nil {
		d.logger.Warn("🚧 Failed to get story_edit tool for blocked requeue: %v", err)
		return false
	}

	d.logger.Info("🚧 Running blocked requeue review for story %s (failure: %s)", req.StoryID, fi.Kind)

	// Run single-turn toolloop with story_edit as the terminal tool
	out := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[StoryEditResult]{
		ContextManager:     cm,
		GeneralTools:       []tools.Tool{},
		TerminalTool:       storyEditTool,
		MaxIterations:      3,
		MaxTokens:          agent.ArchitectMaxTokens,
		Temperature:        config.GetTemperature(config.TempRoleArchitect),
		SingleTurn:         true,
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		StoryID:            req.StoryID,
	})

	// Handle outcome — same pattern as attemptStoryEdit
	if out.Kind != toolloop.OutcomeProcessEffect {
		d.logger.Warn("🚧 Blocked requeue review failed for %s: %v (continuing with requeue)", req.StoryID, out.Err)
		return false
	}

	if out.Signal != tools.SignalStoryEditComplete {
		d.logger.Warn("🚧 Unexpected signal from blocked requeue review: %s (continuing with requeue)", out.Signal)
		return false
	}

	// Extract edit data from EffectData
	effectData, ok := utils.SafeAssert[map[string]any](out.EffectData)
	if !ok {
		d.logger.Warn("🚧 Blocked requeue effect data is not map[string]any: %T (continuing with requeue)", out.EffectData)
		return false
	}

	// Check for full story rewrite first (takes precedence over notes)
	revisedContent, _ := utils.SafeAssert[string](effectData["revised_content"])
	if revisedContent != "" {
		story.Content = revisedContent
		d.logger.Info("🚧 Replaced story content for %s (%d chars) — architect rewrote story after %s failure",
			req.StoryID, len(revisedContent), fi.Kind)
		return true
	}

	// Fall back to appending implementation notes
	notes, _ := utils.SafeAssert[string](effectData["notes"])
	if notes == "" {
		d.logger.Info("🚧 Architect provided no edits for blocked story %s", req.StoryID)
		return false
	}

	story.Content += "\n\n## Failure Context (Auto-generated)\n\n" + notes
	d.logger.Info("🚧 Appended failure context to story %s (%d chars)", req.StoryID, len(notes))
	return true
}

// buildBlockedRequeuePrompt renders the blocked requeue template for the architect.
func (d *Driver) buildBlockedRequeuePrompt(story *QueuedStory, storyID string, fi *proto.FailureInfo) string {
	fallback := fmt.Sprintf("The coder working on story %s reported a %s failure: %s. "+
		"The story will be requeued. Review and call story_edit if the story needs changes.", storyID, fi.Kind, fi.Explanation)

	if d.renderer == nil {
		return fallback
	}

	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"StoryTitle":   story.Title,
			"StoryID":      storyID,
			"FailureKind":  string(fi.Kind),
			"Explanation":  fi.Explanation,
			"FailedState":  fi.FailedState,
			"ToolName":     fi.ToolName,
			"AttemptCount": story.AttemptCount,
		},
	}

	content, err := d.renderer.Render(templates.BlockedRequeueTemplate, templateData)
	if err != nil {
		d.logger.Warn("🚧 Failed to render blocked requeue template: %v", err)
		return fallback
	}

	return content
}

// createReviewToolProviderForCoder creates a tool provider for structured reviews (approvals).
// Includes read_file, list_files, review_complete, add_maintenance_item, and optionally get_diff.
func (d *Driver) createReviewToolProviderForCoder(coderID string, includeGetDiff bool) *tools.ToolProvider {
	// Inside the architect container, coder workspaces are mounted at /mnt/coders/{coder-id}
	containerWorkDir := fmt.Sprintf("/mnt/coders/%s", coderID)

	// Get story ID from dispatcher lease for maintenance item source context
	storyID := d.dispatcher.GetStoryForAgent(coderID)

	ctx := tools.AgentContext{
		Executor:        d.executor,       // Architect executor with read-only mounts
		ChatService:     nil,              // No chat service needed for read tools
		ReadOnly:        true,             // Architect tools are read-only
		NetworkDisabled: false,            // Network allowed for architect
		WorkDir:         containerWorkDir, // Use coder's container mount path
		Agent:           nil,              // No agent reference needed for read tools
		AgentID:         coderID,          // Agent being reviewed (for maintenance item source)
		StoryID:         storyID,          // Story being reviewed (for maintenance item source)
		MaintenanceLog:  d,                // Driver implements MaintenanceLog
	}

	// Build tool list: read tools + review_complete terminal tool + maintenance item
	// Optionally include get_diff for code reviews
	allowedTools := []string{
		tools.ToolReadFile,
		tools.ToolListFiles,
		tools.ToolReviewComplete,     // Terminal tool for structured reviews
		tools.ToolAddMaintenanceItem, // Non-terminal: log issues during review
	}
	if includeGetDiff {
		allowedTools = append(allowedTools, tools.ToolGetDiff)
	}

	return tools.NewProvider(&ctx, allowedTools)
}

// createQuestionToolProviderForCoder creates a tool provider for answering questions.
// Includes read_file, list_files, submit_reply, and add_maintenance_item.
func (d *Driver) createQuestionToolProviderForCoder(coderID string) *tools.ToolProvider {
	// Inside the architect container, coder workspaces are mounted at /mnt/coders/{coder-id}
	containerWorkDir := fmt.Sprintf("/mnt/coders/%s", coderID)

	// Get story ID from dispatcher lease for maintenance item source context
	storyID := d.dispatcher.GetStoryForAgent(coderID)

	ctx := tools.AgentContext{
		Executor:        d.executor,       // Architect executor with read-only mounts
		ChatService:     nil,              // No chat service needed for read tools
		ReadOnly:        true,             // Architect tools are read-only
		NetworkDisabled: false,            // Network allowed for architect
		WorkDir:         containerWorkDir, // Use coder's container mount path
		Agent:           nil,              // No agent reference needed for read tools
		AgentID:         coderID,          // Agent being reviewed (for maintenance item source)
		StoryID:         storyID,          // Story being reviewed (for maintenance item source)
		MaintenanceLog:  d,                // Driver implements MaintenanceLog
	}

	// Build tool list: read tools + submit_reply terminal tool + maintenance item
	allowedTools := []string{
		tools.ToolReadFile,
		tools.ToolListFiles,
		tools.ToolSubmitReply,        // Terminal tool for text replies
		tools.ToolAddMaintenanceItem, // Non-terminal: log issues during Q&A
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
