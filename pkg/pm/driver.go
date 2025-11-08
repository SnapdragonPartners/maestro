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
	"orchestrator/pkg/workspace"
)

const (
	// DefaultExpertise is the default expertise level if none is specified.
	DefaultExpertise = "BASIC"
)

// Driver implements the PM (Product Manager) agent.
// PM conducts interviews with users to generate high-quality specifications.
type Driver struct {
	pmID               string
	llmClient          agent.LLMClient
	renderer           *templates.Renderer
	contextManager     *contextmgr.ContextManager
	logger             *logx.Logger
	dispatcher         *dispatch.Dispatcher
	persistenceChannel chan<- *persistence.Request
	currentState       proto.State
	stateData          map[string]any
	interviewRequestCh <-chan *proto.AgentMsg
	executor           *execpkg.ArchitectExecutor // PM uses same read-only executor as architect
	workDir            string
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

				// TODO: Notify supervisor of state change when state notification channel is added
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

// handleWaiting blocks until an interview request arrives from WebUI.
func (d *Driver) handleWaiting(ctx context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM waiting for interview request")

	select {
	case <-ctx.Done():
		return proto.StateDone, nil
	case interviewMsg := <-d.interviewRequestCh:
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
					// TODO: Use cfg.PM.DefaultExpertise when PM config is added
					d.stateData["expertise"] = DefaultExpertise
				}
			}
		}

		// Initialize conversation history
		d.stateData["conversation"] = []map[string]string{}
		d.stateData["turn_count"] = 0

		return StateInterviewing, nil
	}
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
func (d *Driver) handleSubmitting(_ context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM submitting specification")

	// Get draft spec from state
	draftSpec, ok := d.stateData["draft_spec"].(string)
	if !ok || draftSpec == "" {
		d.logger.Error("No draft spec found in state")
		return proto.StateError, fmt.Errorf("no draft spec to submit")
	}

	d.logger.Info("Validating draft spec (%d bytes)", len(draftSpec))

	// TODO: Full implementation requires:
	// 1. Call submit_spec tool with draft content
	// 2. submit_spec tool validates:
	//    - YAML frontmatter is valid
	//    - Required sections present (Vision, Scope, Requirements)
	//    - Requirement IDs unique and formatted correctly
	//    - All requirements have acceptance criteria
	//    - Dependency graph is acyclic
	// 3. If validation passes:
	//    - Persist to specs table in database
	//    - Send message to architect's spec channel
	//    - Mark PM conversation as completed
	//    - Return to WAITING for next interview
	// 4. If validation fails:
	//    - Store error feedback in stateData
	//    - Return to INTERVIEWING to fix issues
	//
	// For now, this is a stub that simulates successful submission.
	// Real implementation requires specs package (parser, validator) and submit_spec tool.

	// Simulate spec ID generation
	specID := fmt.Sprintf("spec-%d", len(draftSpec)%1000)
	d.stateData["spec_id"] = specID

	d.logger.Info("✅ Specification submitted successfully (spec_id: %s)", specID)
	d.logger.Info("🎯 PM returning to WAITING state for next interview")

	// Clear state for next interview
	d.stateData = make(map[string]any)

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

// Shutdown gracefully shuts down the PM agent.
func (d *Driver) Shutdown(_ context.Context) error {
	d.logger.Info("🎯 PM agent %s shutting down gracefully", d.pmID)
	// PM agent is stateless between interviews, no cleanup needed
	return nil
}
