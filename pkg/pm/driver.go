package pm

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
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

// NewDriver creates a new PM agent driver.
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
					d.stateData["expertise"] = "BASIC"
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

	// TODO: Implement interview loop
	// - Render interview template based on expertise level
	// - Call LLM with tools (read_file, list_files)
	// - Add PM response to conversation history
	// - Wait for user response via chat
	// - Check if interview is complete (user satisfaction or turn limit)
	// - If complete, transition to DRAFTING
	// - If not complete, stay in INTERVIEWING

	return StateDrafting, fmt.Errorf("interview implementation pending")
}

// handleDrafting generates the markdown spec from the conversation.
func (d *Driver) handleDrafting(_ context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM drafting specification")

	// TODO: Implement drafting
	// - Render drafting template with conversation history
	// - Call LLM to generate markdown spec with YAML frontmatter
	// - Store draft in stateData
	// - Transition to SUBMITTING

	return StateSubmitting, fmt.Errorf("drafting implementation pending")
}

// handleSubmitting validates and submits the spec to the architect.
func (d *Driver) handleSubmitting(_ context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM submitting specification")

	// TODO: Implement submission
	// - Call submit_spec tool with draft content
	// - If validation passes:
	//   - Spec persisted to database
	//   - Message sent to architect's spec channel
	//   - Mark PM conversation as completed
	//   - Return to WAITING
	// - If validation fails:
	//   - Return to INTERVIEWING with error feedback

	return StateWaiting, fmt.Errorf("submission implementation pending")
}

// GetID returns the PM agent's ID.
func (d *Driver) GetID() string {
	return d.pmID
}

// GetState returns the current state.
func (d *Driver) GetState() proto.State {
	return d.currentState
}
