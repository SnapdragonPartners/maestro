package pm

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
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
	logger := logx.NewLogger("pm")
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

	// Create tool provider for spec_submit tool
	agentCtx := tools.AgentContext{
		Executor:        pmExecutor,
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         pmWorkspace,
	}
	toolProvider := tools.NewProvider(agentCtx, tools.PMSubmittingTools)

	return &Driver{
		pmID:               pmID,
		llmClient:          llmClient,
		renderer:           renderer,
		contextManager:     contextManager,
		logger:             logger, // Use logger created above
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
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
	case StateInterviewing:
		return d.handleInterviewing(ctx)
	case StateDrafting:
		return d.handleDrafting(ctx)
	case StateSubmitting:
		return d.handleSubmitting(ctx)
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

			// Store spec directly as draft (bypass interview and drafting)
			d.stateData["draft_spec"] = specMarkdown
			d.logger.Info("✅ Spec file loaded (%d bytes), transitioning to SUBMITTING", len(specMarkdown))

			// Transition directly to SUBMITTING to validate and send to architect
			return StateSubmitting, nil
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

	return StateInterviewing, nil
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

	// Store feedback in state
	d.stateData["architect_feedback"] = approvalResult.Feedback
	delete(d.stateData, "pending_request_id") // Clear pending request

	// Return to INTERVIEWING to address feedback
	return StateInterviewing, nil
}

// handleInterviewing conducts the interview conversation with the user.
func (d *Driver) handleInterviewing(_ context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM conducting interview")

	// Get conversation state
	turnCount, _ := d.stateData["turn_count"].(int)
	expertise, _ := d.stateData["expertise"].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	// Get max turns from config
	cfg, err := config.GetConfig()
	if err != nil {
		return proto.StateError, fmt.Errorf("failed to get config: %w", err)
	}
	maxTurns := cfg.PM.MaxInterviewTurns

	d.logger.Info("Interview turn %d/%d (expertise: %s)", turnCount, maxTurns, expertise)

	// Check if we've reached turn limit
	if turnCount >= maxTurns {
		d.logger.Info("Interview reached maximum turns, moving to drafting")
		return StateDrafting, nil
	}

	// TODO: Full implementation requires:
	// 1. Render interview template with conversation history
	// 2. Call LLM with read-only tools (list_files, read_file)
	// 3. Save PM response to conversation history in database
	// 4. Wait for user response via WebUI chat
	// 5. Save user response to conversation history
	// 6. Check if interview is complete (user signals done)
	// 7. Increment turn count and loop or transition to DRAFTING
	//
	// For now, this is a stub that transitions to DRAFTING after max turns.
	// Real implementation will be added when WebUI integration is complete.

	d.stateData["turn_count"] = turnCount + 1

	// Stay in INTERVIEWING until max turns or user signals completion
	return StateInterviewing, nil
}

// handleDrafting generates the markdown spec from the conversation.
func (d *Driver) handleDrafting(_ context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM drafting specification")

	// Get conversation history
	conversationHistory, _ := d.stateData["conversation"].([]map[string]string)
	expertise, _ := d.stateData["expertise"].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	d.logger.Info("Drafting spec from %d conversation messages (expertise: %s)", len(conversationHistory), expertise)

	// TODO: Full implementation requires:
	// 1. Render spec_generation template with conversation history
	// 2. Call LLM to generate markdown spec with YAML frontmatter
	// 3. Parse and validate the generated spec structure
	// 4. Store draft spec in stateData["draft_spec"]
	// 5. Transition to SUBMITTING for validation
	//
	// For now, this is a stub that creates a minimal draft.
	// Real implementation will be added when template rendering and LLM calls are wired up.

	// Create minimal draft spec for testing
	draftSpec := `---
version: "1.0"
priority: must
---

# Feature: Placeholder Specification

## Vision
This is a placeholder specification generated by the PM agent stub.

## Scope
### In Scope
- Placeholder item 1

### Out of Scope
- Placeholder item 2

## Requirements

### R-001: Placeholder Requirement
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** This is a placeholder requirement.

**Acceptance Criteria:**
- [ ] Placeholder criterion 1
`

	d.stateData["draft_spec"] = draftSpec
	d.logger.Info("✅ Draft specification created (%d bytes)", len(draftSpec))

	return StateSubmitting, nil
}

// handleSubmitting validates and submits the spec to the architect.
func (d *Driver) handleSubmitting(ctx context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM submitting specification")

	// Get draft spec from state
	draftSpec, ok := d.stateData["draft_spec"].(string)
	if !ok || draftSpec == "" {
		d.logger.Error("No draft spec found in state")
		return proto.StateError, fmt.Errorf("no draft spec to submit")
	}

	d.logger.Info("Validating draft spec (%d bytes)", len(draftSpec))

	// Call spec_submit tool to validate spec
	specSubmitTool, err := d.toolProvider.Get(tools.ToolSpecSubmit)
	if err != nil {
		d.logger.Error("Failed to get spec_submit tool: %v", err)
		return proto.StateError, fmt.Errorf("failed to get spec_submit tool: %w", err)
	}

	// Create tool arguments
	toolArgs := map[string]any{
		"markdown": draftSpec,
		"summary":  "Generated specification from PM interview", // Will be extracted from spec frontmatter
	}

	// Execute tool
	toolResultAny, err := specSubmitTool.Exec(ctx, toolArgs)
	if err != nil {
		d.logger.Error("spec_submit tool execution failed: %v", err)
		return proto.StateError, fmt.Errorf("spec_submit tool execution failed: %w", err)
	}

	// Convert result to map
	toolResult, ok := toolResultAny.(map[string]any)
	if !ok {
		d.logger.Error("spec_submit tool returned unexpected result type: %T", toolResultAny)
		return proto.StateError, fmt.Errorf("spec_submit tool returned unexpected result type")
	}

	// Check if tool signaled to send REQUEST
	sendRequest, _ := toolResult["send_request"].(bool)
	if !sendRequest {
		// Validation failed - transition back to INTERVIEWING with feedback
		d.logger.Warn("Spec validation failed, returning to interview")
		if validationErrors, ok := toolResult["validation_errors"].([]string); ok {
			d.stateData["validation_feedback"] = validationErrors
		}
		return StateInterviewing, nil
	}

	// Validation passed - create REQUEST message for architect
	d.logger.Info("✅ Spec validation passed, sending REQUEST to architect")

	summary, _ := toolResult["summary"].(string)
	specMarkdown, _ := toolResult["spec_markdown"].(string)

	// Create approval request payload for spec review
	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, d.pmID, "architect-001")
	payload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeSpec,
		Content:      summary,
		Reason:       "PM has completed spec interview and generated specification for review",
		Context:      fmt.Sprintf("Spec length: %d bytes", len(specMarkdown)),
		Metadata: map[string]string{
			"spec_markdown": specMarkdown,
		},
	}
	requestMsg.SetTypedPayload(proto.NewApprovalRequestPayload(payload))

	// Send REQUEST via Effects
	effect := &SendMessageEffect{Message: requestMsg}
	if err := d.ExecuteEffect(ctx, effect); err != nil {
		d.logger.Error("Failed to send REQUEST to architect: %v", err)
		return proto.StateError, fmt.Errorf("failed to send spec review request: %w", err)
	}

	d.logger.Info("✅ REQUEST sent to architect, transitioning to WAITING for RESULT")

	// Store request ID to correlate with RESULT
	d.stateData["pending_request_id"] = requestMsg.ID
	d.stateData["spec_markdown"] = specMarkdown

	return StateWaiting, nil
}

// GetID returns the PM agent's ID.
func (d *Driver) GetID() string {
	return d.pmID
}

// GetState returns the current state.
func (d *Driver) GetState() proto.State {
	return d.currentState
}

// GetAgentType returns the agent type (required by agent.Driver interface).
func (d *Driver) GetAgentType() agent.Type {
	return agent.TypePM
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
