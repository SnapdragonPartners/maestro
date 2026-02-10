package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// generateQuestionPrompt creates a concise user message for technical questions.
// Context (story, role, tools) is already in the system prompt.
func (d *Driver) generateQuestionPrompt(requestMsg *proto.AgentMsg, questionPayload *proto.QuestionRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	_ = requestMsg   // context already in system prompt
	_ = coderID      // context already in system prompt
	_ = toolProvider // tools already documented in system prompt

	return fmt.Sprintf(`The coder has a technical question:

%s

Please explore their workspace, analyze the code, and provide a clear answer using submit_reply.`, questionPayload.Text)
}

// handleIterativeQuestion processes question requests with iterative code exploration.
func (d *Driver) handleIterativeQuestion(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract question from typed payload
	typedPayload := requestMsg.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("question message missing typed payload")
	}

	questionPayload, err := typedPayload.ExtractQuestionRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to extract question request: %w", err)
	}

	storyID := proto.GetStoryID(requestMsg)
	coderID := requestMsg.FromAgent
	if coderID == "" {
		return nil, fmt.Errorf("question message missing sender (FromAgent)")
	}

	d.logger.Info("🔍 Starting iterative question handling (story: %s)", storyID)

	// Store story_id in state data for tool logging
	d.SetStateData(StateKeyCurrentStoryID, storyID)

	// Get agent-specific context
	cm := d.getContextForAgent(coderID)

	// Build prompt for technical question (on first call only)
	// Create tool provider rooted at the coder's container workspace with submit_reply
	toolProvider := d.createQuestionToolProviderForCoder(coderID)
	prompt := d.generateQuestionPrompt(requestMsg, questionPayload, coderID, toolProvider)

	// Add question prompt as user message to preserve context continuity
	cm.AddMessage("architect-question-prompt", prompt)

	// Get submit_reply tool and wrap as terminal tool
	submitReplyTool, err := toolProvider.Get(tools.ToolSubmitReply)
	if err != nil {
		return nil, logx.Wrap(err, "failed to get submit_reply tool")
	}
	terminalTool := submitReplyTool

	// Get general tools (read_file, list_files)
	var generalTools []tools.Tool
	for _, toolName := range []string{tools.ToolReadFile, tools.ToolListFiles} {
		if tool, err := toolProvider.Get(toolName); err == nil {
			generalTools = append(generalTools, tool)
		}
	}

	// Run toolloop with submit_reply as terminal tool
	d.logger.Info("🔍 Starting iterative question loop")
	out := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[SubmitReplyResult]{
		ContextManager:     cm,
		GeneralTools:       generalTools,
		TerminalTool:       terminalTool,
		MaxIterations:      20, // Allow exploration of workspace
		MaxTokens:          agent.ArchitectMaxTokens,
		Temperature:        config.GetTemperature(config.TempRoleArchitect),
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		StoryID:            storyID,
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("question-%s", requestMsg.ID),
			SoftLimit: 8,
			HardLimit: 16,
			OnSoftLimit: func(count int) {
				d.logger.Warn("⚠️  Iteration %d: Approaching hard limit for question %s", count, requestMsg.ID)
			},
			OnHardLimit: func(_ context.Context, _ string, count int) error {
				d.logger.Error("❌ Hard iteration limit exceeded for question %s - escalating", requestMsg.ID)
				// Store escalation context for state machine (all required by handleEscalated)
				d.SetStateData(StateKeyEscalationOriginState, string(StateRequest))
				d.SetStateData(StateKeyEscalationIterationCount, count)
				d.SetStateData(StateKeyEscalationRequestID, requestMsg.ID)
				d.SetStateData(StateKeyEscalationStoryID, storyID)
				d.SetStateData(StateKeyEscalationAgentID, coderID)
				return ErrEscalationTriggered
			},
		},
	})

	// Handle toolloop outcome
	if out.Kind != toolloop.OutcomeProcessEffect {
		// Check if escalation was triggered
		if out.Err != nil && out.Err.Error() == ErrEscalationTriggered.Error() {
			return nil, ErrEscalationTriggered
		}
		return nil, fmt.Errorf("question handling failed: %w", out.Err)
	}

	// Verify we got REPLY_SUBMITTED signal
	if out.Signal != tools.SignalReplySubmitted {
		return nil, fmt.Errorf("expected REPLY_SUBMITTED signal, got: %s", out.Signal)
	}

	// Extract response from ProcessEffect.Data
	effectData, ok := utils.SafeAssert[map[string]any](out.EffectData)
	if !ok {
		return nil, fmt.Errorf("REPLY_SUBMITTED effect data is not map[string]any: %T", out.EffectData)
	}

	response := utils.GetMapFieldOr[string](effectData, "response", "")
	d.logger.Info("✅ Architect answered question via submit_reply")

	// Build response message with the answer
	return d.buildQuestionResponseFromSubmit(requestMsg, response)
}

// buildQuestionResponseFromSubmit creates a question response from submit_reply content.
func (d *Driver) buildQuestionResponseFromSubmit(requestMsg *proto.AgentMsg, submitResponse string) (*proto.AgentMsg, error) {
	// Create question response
	answerPayload := &proto.QuestionResponsePayload{
		AnswerText: submitResponse,
		Metadata:   make(map[string]string),
	}

	// Add exploration metadata
	answerPayload.Metadata[proto.KeyExplorationMethod] = "iterative_with_tools"

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewQuestionResponsePayload(answerPayload))

	// Copy story_id and question_id to response metadata
	proto.CopyStoryMetadata(requestMsg, response)
	if questionID := proto.GetQuestionID(requestMsg); questionID != "" {
		proto.SetQuestionID(response, questionID)
	}

	d.logger.Info("✅ Built question response via iterative exploration")
	return response, nil
}
