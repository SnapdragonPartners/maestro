package architect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

const (
	defaultStoryType = "app"
)

// handleRequest processes the request phase (handling coder requests).
func (d *Driver) handleRequest(ctx context.Context) (proto.State, error) {
	// Check for context cancellation first.
	select {
	case <-ctx.Done():
		return StateError, fmt.Errorf("architect request processing cancelled: %w", ctx.Err())
	default:
	}

	// State: processing coder request

	// Get state data
	stateData := d.GetStateData()

	// Get the current request from state data.
	requestMsg, exists := stateData[StateKeyCurrentRequest].(*proto.AgentMsg)
	if !exists || requestMsg == nil {
		return StateError, fmt.Errorf("no current request found")
	}

	// Persist request to database (fire-and-forget)
	if d.persistenceChannel != nil {
		agentRequest := buildAgentRequestFromMsg(requestMsg)
		d.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpUpsertAgentRequest,
			Data:      agentRequest,
			Response:  nil, // Fire-and-forget
		}
	}

	// Process the request based on type.
	var response *proto.AgentMsg
	var err error

	switch requestMsg.Type {
	case proto.MsgTypeREQUEST:
		// Handle unified REQUEST protocol with kind-based routing
		requestKind, hasKind := proto.GetRequestKind(requestMsg)
		if !hasKind {
			return StateError, fmt.Errorf("REQUEST message missing valid kind in typed payload")
		}

		switch requestKind {
		case proto.RequestKindQuestion:
			// Use iterative question handling if we have LLM and executor
			if d.LLMClient != nil && d.executor != nil {
				response, err = d.handleIterativeQuestion(ctx, requestMsg)
			} else {
				response, err = d.handleQuestionRequest(ctx, requestMsg)
			}
		case proto.RequestKindApproval:
			response, err = d.handleApprovalRequest(ctx, requestMsg)
		case proto.RequestKindMerge:
			response, err = d.handleMergeRequest(ctx, requestMsg)
		case proto.RequestKindRequeue:
			err = d.handleRequeueRequest(ctx, requestMsg)
			response = nil // No response needed for requeue messages
		default:
			return StateError, fmt.Errorf("unknown request kind: %s", requestKind)
		}
	default:
		return StateError, fmt.Errorf("unknown request type: %s", requestMsg.Type)
	}

	// Check for escalation signal
	if errors.Is(err, ErrEscalationTriggered) {
		d.logger.Warn("🚨 Escalation triggered - transitioning to ESCALATED state")
		return StateEscalated, nil
	}

	if err != nil {
		return StateError, err
	}

	// If response is nil, means iterative handling wants to continue iteration
	if response == nil && requestMsg.Type == proto.MsgTypeREQUEST {
		requestKind, _ := proto.GetRequestKind(requestMsg)
		if requestKind == proto.RequestKindApproval || requestKind == proto.RequestKindQuestion {
			d.logger.Info("🔄 Iterative request continuing, staying in REQUEST state")
			return StateRequest, nil
		}
	}

	// Send response back using Effects pattern.
	if response != nil {
		sendEffect := &SendResponseEffect{Response: response}
		if err := d.ExecuteEffect(ctx, sendEffect); err != nil {
			return StateError, err
		}

		// Store the response in state data for merge success detection
		d.SetStateData(StateKeyLastResponse, response)

		// Persist response to database (fire-and-forget)
		if d.persistenceChannel != nil {
			agentResponse := buildAgentResponseFromMsg(requestMsg, response)

			// Log warning if status validation failed (mapper silently ignores invalid statuses)
			if agentResponse.Status == nil {
				if typedPayload := response.GetTypedPayload(); typedPayload != nil {
					if typedPayload.Kind == proto.PayloadKindApprovalResponse {
						if result, err := typedPayload.ExtractApprovalResponse(); err == nil {
							if _, valid := proto.ValidateApprovalStatus(string(result.Status)); !valid {
								d.logger.Warn("Invalid approval status '%s' from ApprovalResult ignored", result.Status)
							}
						}
					}
				}
			}

			d.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpUpsertAgentResponse,
				Data:      agentResponse,
				Response:  nil, // Fire-and-forget
			}
		}
		// Response sent and persisted to database
	}

	// Get fresh state data after processing to see any changes made during request handling
	// (GetStateData returns a copy, so the stateData variable from line 35 is stale)
	stateData = d.GetStateData()

	// Check if work was accepted (completion or merge)
	var workWasAccepted bool
	if accepted, exists := stateData[StateKeyWorkAccepted]; exists {
		if acceptedBool, ok := accepted.(bool); ok && acceptedBool {
			workWasAccepted = true
			// Log the acceptance details for debugging
			if storyID, exists := stateData[StateKeyAcceptedStoryID]; exists {
				if acceptanceType, exists := stateData[StateKeyAcceptanceType]; exists {
					d.logger.Info("🎉 Detected work acceptance for story %v via %v, transitioning to DISPATCHING to release dependent stories",
						storyID, acceptanceType)
				}
			}
		}
	}

	// Check if spec was approved and loaded (PM spec approval flow)
	var specApprovedAndLoaded bool
	if approved, exists := stateData[StateKeySpecApprovedLoad]; exists {
		if approvedBool, ok := approved.(bool); ok && approvedBool {
			specApprovedAndLoaded = true
			d.logger.Info("🎉 Spec approved and stories loaded, transitioning to DISPATCHING")
		}
	}

	// Clear the processed request and acceptance signals
	d.SetStateData("current_request", nil)
	d.SetStateData("last_response", nil)
	d.SetStateData("work_accepted", nil)
	d.SetStateData("accepted_story_id", nil)
	d.SetStateData("acceptance_type", nil)
	d.SetStateData("spec_approved_and_loaded", nil)

	// Determine next state:
	// 1. Spec approval (PM flow) → DISPATCHING
	// 2. Work acceptance (completion or merge) → DISPATCHING
	// 3. Owns spec but no acceptance → MONITORING
	// 4. No spec ownership → WAITING
	if specApprovedAndLoaded {
		return StateDispatching, nil
	} else if workWasAccepted && d.ownsSpec() {
		return StateDispatching, nil
	} else if d.ownsSpec() {
		return StateMonitoring, nil
	} else {
		return StateWaiting, nil
	}
}

// handleQuestionRequest processes a QUESTION message and returns an ANSWER.
func (d *Driver) handleQuestionRequest(ctx context.Context, questionMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract question from typed payload
	typedPayload := questionMsg.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("question message missing typed payload")
	}

	questionPayload, err := typedPayload.ExtractQuestionRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to extract question request: %w", err)
	}

	question := questionPayload.Text

	// Question processing will be logged to database only

	// For now, provide simple auto-response until LLM integration.
	answer := "Auto-response: Question received and acknowledged. Please proceed with your implementation."

	// If we have LLM client, use it for more intelligent responses.
	if d.LLMClient != nil {
		prompt := fmt.Sprintf("Answer this coding question: %s", question)

		// Reset context for this question
		templateName := fmt.Sprintf("question-%s", questionMsg.ID)
		d.contextManager.ResetForNewTemplate(templateName, prompt)

		// Use toolloop with submit_reply tool
		_, result, err := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[SubmitReplyResult]{
			ContextManager: d.contextManager,
			ToolProvider:   newListToolProvider([]tools.Tool{tools.NewSubmitReplyTool()}),
			CheckTerminal:  d.checkTerminalTools,
			ExtractResult:  ExtractSubmitReply,
			MaxIterations:  10,
			MaxTokens:      agent.ArchitectMaxTokens,
			AgentID:        d.GetAgentID(),
		})

		if err == nil {
			answer = result.Response
		}
		// Silently fall back to auto-response on error
	}

	// Create RESPONSE using unified protocol.
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), questionMsg.FromAgent)
	response.ParentMsgID = questionMsg.ID

	// Set typed question response payload
	answerPayload := &proto.QuestionResponsePayload{
		AnswerText: answer,
		Metadata:   make(map[string]string),
	}

	// Copy correlation ID and story_id to metadata
	// Copy metadata using helpers
	if correlationID := proto.GetCorrelationID(questionMsg); correlationID != "" {
		answerPayload.Metadata[proto.KeyCorrelationID] = correlationID
		proto.SetCorrelationID(response, correlationID)
	}
	if storyID := proto.GetStoryID(questionMsg); storyID != "" {
		answerPayload.Metadata[proto.KeyStoryID] = storyID
		proto.SetStoryID(response, storyID)
	}

	response.SetTypedPayload(proto.NewQuestionResponsePayload(answerPayload))

	return response, nil
}

// handleApprovalRequest processes a REQUEST message and returns a RESULT.
func (d *Driver) handleApprovalRequest(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract approval request from typed payload
	typedPayload := requestMsg.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("approval request message missing typed payload")
	}

	approvalPayload, err := typedPayload.ExtractApprovalRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to extract approval request: %w", err)
	}

	content := approvalPayload.Content
	approvalType := approvalPayload.ApprovalType
	approvalIDString := proto.GetApprovalID(requestMsg)

	// Check if this approval type should use iteration pattern
	useIteration := approvalType == proto.ApprovalTypeCode || approvalType == proto.ApprovalTypeCompletion

	// If using iteration and we have LLM and executor, use iterative review
	if useIteration && d.LLMClient != nil && d.executor != nil {
		return d.handleIterativeApproval(ctx, requestMsg, approvalPayload)
	}

	// Handle spec review approval with spec review tools
	if approvalType == proto.ApprovalTypeSpec && d.LLMClient != nil {
		return d.handleSpecReview(ctx, requestMsg, approvalPayload)
	}

	// Handle single-turn reviews (Plan and BudgetReview) with review_complete tool
	useSingleTurnReview := approvalType == proto.ApprovalTypePlan || approvalType == proto.ApprovalTypeBudgetReview
	if useSingleTurnReview && d.LLMClient != nil {
		return d.handleSingleTurnReview(ctx, requestMsg, approvalPayload)
	}

	// Approval request processing will be logged to database only

	// Persist plan to database if this is a plan approval request
	if approvalType == proto.ApprovalTypePlan && d.persistenceChannel != nil {
		planContent := content

		if planContent != "" {
			// Extract story_id from metadata
			storyIDStr := proto.GetStoryID(requestMsg)

			// Debug logging for story_id validation
			if storyIDStr == "" {
				d.logger.Error("Agent plan creation: missing story_id in request from %s", requestMsg.FromAgent)
			} else {
				d.logger.Info("Creating agent plan for story_id: '%s' (len=%d) from agent: %s", storyIDStr, len(storyIDStr), requestMsg.FromAgent)
			}

			// Extract confidence if present
			var confidenceStr *string
			if conf, exists := approvalPayload.Metadata[proto.KeyConfidence]; exists && conf != "" {
				confidenceStr = &conf
			}

			agentPlan := &persistence.AgentPlan{
				ID:         persistence.GenerateAgentPlanID(),
				StoryID:    storyIDStr,
				FromAgent:  requestMsg.FromAgent,
				Content:    planContent,
				Confidence: confidenceStr,
				Status:     persistence.PlanStatusSubmitted,
				CreatedAt:  requestMsg.Timestamp,
			}

			d.logger.Debug("Persisting agent plan %s for story %s", agentPlan.ID, agentPlan.StoryID)
			d.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpUpsertAgentPlan,
				Data:      agentPlan,
				Response:  nil, // Fire-and-forget
			}
		}
	}

	// Fallback: auto-approve if none of the proper toolloop-based handlers were triggered
	// This should only happen in degraded scenarios (no LLM client or missing dependencies)
	approved := true
	feedback := "Auto-approved: Request looks good, please proceed (fallback mode - proper review handlers not available)."
	d.logger.Warn("⚠️  Using auto-approve fallback for %s approval - proper toolloop-based handler not triggered (llmClient=%v, executor=%v)",
		approvalType, d.LLMClient != nil, d.executor != nil)

	// Plan approval completed - artifacts now tracked in database

	// Mark story as completed for approved completions.
	if approved && approvalType == proto.ApprovalTypeCompletion {
		// Extract story ID from metadata and handle work acceptance (queue completion, database persistence, state transition signal)
		if storyIDStr, exists := requestMsg.Metadata[proto.KeyStoryID]; exists && storyIDStr != "" {
			// For completion (non-merge) scenarios, we don't have PR/commit data
			completionSummary := "Story completed via manual approval"
			d.handleWorkAccepted(ctx, storyIDStr, "completion", nil, nil, &completionSummary)
		}
	}

	// Create proper ApprovalResult structure.
	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  approvalIDString,
		Type:       approvalType,
		Status:     proto.ApprovalStatusApproved,
		Feedback:   "", // Will be set after status determination and formatting
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: time.Now().UTC(),
	}

	if !approved {
		// For budget reviews, parse the LLM response to preserve NEEDS_CHANGES vs REJECTED
		if approvalType == proto.ApprovalTypeBudgetReview && feedback != "" {
			responseUpper := strings.ToUpper(feedback)
			if strings.Contains(responseUpper, string(proto.ApprovalStatusNeedsChanges)) {
				approvalResult.Status = proto.ApprovalStatusNeedsChanges
			} else {
				// Default to rejected for REJECTED or unknown negative responses
				approvalResult.Status = proto.ApprovalStatusRejected
			}
		} else if approvalType == proto.ApprovalTypeCode && feedback != "" {
			// For code reviews, parse the LLM response to preserve NEEDS_CHANGES vs REJECTED
			responseUpper := strings.ToUpper(feedback)
			if strings.Contains(responseUpper, string(proto.ApprovalStatusNeedsChanges)) {
				approvalResult.Status = proto.ApprovalStatusNeedsChanges
			} else {
				// Default to rejected for REJECTED or unknown negative responses
				approvalResult.Status = proto.ApprovalStatusRejected
			}
		} else if approvalType == proto.ApprovalTypeCompletion && feedback != "" {
			// For completion requests, parse the LLM response to preserve NEEDS_CHANGES vs REJECTED
			responseUpper := strings.ToUpper(feedback)
			if strings.Contains(responseUpper, string(proto.ApprovalStatusNeedsChanges)) {
				approvalResult.Status = proto.ApprovalStatusNeedsChanges
			} else {
				// Default to rejected for REJECTED or unknown negative responses
				approvalResult.Status = proto.ApprovalStatusRejected
			}
		} else {
			// For other approval types, default to rejected
			approvalResult.Status = proto.ApprovalStatusRejected
		}
	}

	// Format feedback using templates based on approval type
	switch approvalType {
	case proto.ApprovalTypePlan:
		approvalResult.Feedback = d.getPlanApprovalResponse(approvalResult.Status, feedback)
	case proto.ApprovalTypeCode:
		approvalResult.Feedback = d.getCodeReviewResponse(approvalResult.Status, feedback)
	case proto.ApprovalTypeCompletion:
		approvalResult.Feedback = d.getCompletionResponse(approvalResult.Status, feedback)
	case proto.ApprovalTypeBudgetReview:
		// Extract origin state from approval payload context
		// Context is formatted as "origin:STATE" where STATE is PLANNING or CODING
		originState := "UNKNOWN"
		if approvalPayload.Context != "" && strings.HasPrefix(approvalPayload.Context, "origin:") {
			originState = strings.TrimPrefix(approvalPayload.Context, "origin:")
		}
		approvalResult.Feedback = d.getBudgetReviewResponse(approvalResult.Status, feedback, originState)
	default:
		// Fallback to raw feedback for unknown types
		approvalResult.Feedback = feedback
	}

	// If this is an approved plan, update the story's approved plan in the queue
	if approvalResult.Status == proto.ApprovalStatusApproved && approvalType == proto.ApprovalTypePlan {
		if storyIDStr, exists := requestMsg.Metadata[proto.KeyStoryID]; exists && storyIDStr != "" && d.queue != nil {
			// Get the plan content from the request (it's already in 'content' variable from approvalPayload)
			planContent := content

			if planContent != "" {
				if err := d.queue.SetApprovedPlan(storyIDStr, planContent); err != nil {
					d.logger.Error("Failed to set approved plan for story %s: %v", storyIDStr, err)
				} else {
					d.logger.Info("✅ Set approved plan for story %s", storyIDStr)
					// Persist just this story to database with the updated approved plan
					if story, exists := d.queue.GetStory(storyIDStr); exists {
						// Convert queue status to database status
						var dbStatus string
						switch story.GetStatus() {
						case StatusNew, StatusPending:
							dbStatus = persistence.StatusNew
						case StatusDispatched:
							dbStatus = persistence.StatusDispatched
						case StatusPlanning:
							dbStatus = persistence.StatusPlanning
						case StatusCoding:
							dbStatus = persistence.StatusCoding
						case StatusDone:
							dbStatus = persistence.StatusDone
						default:
							dbStatus = persistence.StatusNew
						}

						dbStory := &persistence.Story{
							ID:            story.ID,
							SpecID:        story.SpecID,
							Title:         story.Title,
							Content:       story.Content,
							ApprovedPlan:  story.ApprovedPlan,
							Status:        dbStatus,
							Priority:      story.Priority,
							CreatedAt:     story.LastUpdated,
							StartedAt:     story.StartedAt,
							CompletedAt:   story.CompletedAt,
							AssignedAgent: story.AssignedAgent,
							StoryType:     story.StoryType,
							TokensUsed:    0,   // Metrics data added during completion
							CostUSD:       0.0, // Metrics data added during completion
						}

						persistence.PersistStory(dbStory, d.persistenceChannel)
						d.logger.Debug("💾 Persisted story %s with approved plan to database", storyIDStr)
					}
				}
			}
		}
	}

	// Create RESPONSE using unified protocol with typed approval response payload.
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID

	// Set typed approval response payload
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	// Copy story_id from request metadata for dispatcher validation
	if storyID, exists := requestMsg.Metadata[proto.KeyStoryID]; exists {
		proto.SetStoryID(response, storyID)
	}

	// Copy approval_id to metadata
	proto.SetApprovalID(response, approvalResult.ID)

	// Approval result will be logged to database only

	return response, nil
}

// handleRequeueRequest processes a REQUEUE message (fire-and-forget).
func (d *Driver) handleRequeueRequest(_ /* ctx */ context.Context, requeueMsg *proto.AgentMsg) error {
	// Extract story_id from metadata
	storyIDStr := proto.GetStoryID(requeueMsg)

	if storyIDStr == "" {
		return fmt.Errorf("requeue request missing story_id")
	}

	// Load current queue state.
	if d.queue == nil {
		return fmt.Errorf("no queue available")
	}

	// Mark story as pending for reassignment.
	if err := d.queue.UpdateStoryStatus(storyIDStr, StatusPending); err != nil {
		return fmt.Errorf("failed to requeue story %s: %w", storyIDStr, err)
	}

	// Log the requeue event - this will appear in the architect logs.
	// Requeue completed successfully

	return nil
}

// buildApprovalResponseFromReviewComplete builds an approval response from review_complete tool result.
func (d *Driver) buildApprovalResponseFromReviewComplete(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, statusStr, feedback string) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	storyID := proto.GetStoryID(requestMsg)

	d.logger.Info("Building approval response: %s -> %s", approvalType, statusStr)

	// Map string status to proto.ApprovalStatus
	var status proto.ApprovalStatus
	switch statusStr {
	case "APPROVED":
		status = proto.ApprovalStatusApproved
	case "NEEDS_CHANGES":
		status = proto.ApprovalStatusNeedsChanges
	case "REJECTED":
		status = proto.ApprovalStatusRejected
	default:
		// Should not happen due to tool validation, but handle gracefully
		status = proto.ApprovalStatusNeedsChanges
		d.logger.Warn("Unknown status %s, defaulting to NEEDS_CHANGES", statusStr)
	}

	if feedback == "" {
		feedback = "Review completed via single-turn review"
	}

	// Create approval result
	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  proto.GetApprovalID(requestMsg),
		Type:       approvalType,
		Status:     status,
		Feedback:   feedback,
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: time.Now().UTC(),
	}

	// Handle work acceptance for approved completions
	if status == proto.ApprovalStatusApproved && approvalType == proto.ApprovalTypeCompletion {
		if storyID != "" {
			completionSummary := fmt.Sprintf("Story completed via single-turn review: %s", feedback)
			d.handleWorkAccepted(ctx, storyID, "completion", nil, nil, &completionSummary)
		}
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	// Copy story_id to response metadata
	if storyID != "" {
		proto.SetStoryID(response, storyID)
	}
	proto.SetApprovalID(response, approvalResult.ID)

	d.logger.Info("✅ Built approval response: %s - %s", status, feedback)
	return response, nil
}

// handleWorkAccepted handles the unified flow for work acceptance (completion or merge).
// This is the single path for marking stories as done, persisting to database, and signaling state transition.
func (d *Driver) handleWorkAccepted(ctx context.Context, storyID, acceptanceType string, prID, commitHash, completionSummary *string) {
	if storyID == "" {
		d.logger.Warn("handleWorkAccepted called with empty story ID")
		return
	}

	d.logger.Info("🎉 Work accepted for story %s via %s", storyID, acceptanceType)
	d.logger.Info("🔍 handleWorkAccepted: queue=%v, persistenceChannel=%v", d.queue != nil, d.persistenceChannel != nil)

	// 1. Update story with completion data in queue
	if d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			d.logger.Info("🔍 Found story %s in queue for completion", storyID)
			// Update the story with completion data
			if prID != nil {
				story.PRID = *prID
			}
			if commitHash != nil {
				story.CommitHash = *commitHash
			}
			if completionSummary != nil {
				story.CompletionSummary = *completionSummary
			}

			// Update status and timestamps in memory (don't persist yet)
			now := time.Now().UTC()
			story.SetStatus(persistence.StatusDone) // Use database-compatible status
			story.CompletedAt = &now
			story.LastUpdated = now

			d.logger.Info("✅ Story %s marked as completed in queue with completion data", storyID)
		} else {
			d.logger.Warn("⚠️ Story %s not found in queue for completion", storyID)
		}
	} else {
		d.logger.Warn("⚠️ Queue is nil in handleWorkAccepted")
	}

	// 2. Add metrics to the story and persist to database
	if d.persistenceChannel != nil && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			// Query and add metrics if available
			storyMetrics := d.queryStoryMetrics(ctx, storyID)
			if storyMetrics != nil {
				story.TokensUsed = storyMetrics.PromptTokens + storyMetrics.CompletionTokens
				story.CostUSD = storyMetrics.TotalCost
			}

			d.logger.Info("💾 Persisting completed story %s to database after %s", storyID, acceptanceType)
			persistenceStory := story.ToPersistenceStory()
			d.logger.Info("🔍 Story data for persistence: ID=%s, Status=%s, TokensUsed=%d, CostUSD=%.6f, PRID=%s, CommitHash=%s",
				persistenceStory.ID, persistenceStory.Status, persistenceStory.TokensUsed, persistenceStory.CostUSD, persistenceStory.PRID, persistenceStory.CommitHash)

			// Non-blocking send attempt
			select {
			case d.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpUpsertStory,
				Data:      persistenceStory,
				Response:  nil,
			}:
				d.logger.Info("✅ Story %s persistence request sent successfully", storyID)
			default:
				d.logger.Error("❌ Persistence channel full! Cannot persist story %s", storyID)
			}

			// Notify queue that story completed (for dependency resolution)
			if d.queue != nil {
				d.queue.checkAndNotifyReady()
			}
		} else {
			d.logger.Warn("⚠️ Persistence failed: story %s not found in queue", storyID)
		}
	} else {
		if d.persistenceChannel == nil {
			d.logger.Warn("⚠️ Persistence skipped: persistenceChannel is nil")
		}
		if d.queue == nil {
			d.logger.Warn("⚠️ Persistence skipped: queue is nil")
		}
	}

	// 3. Set state data to signal that work was accepted (for DISPATCHING transition)
	d.SetStateData(StateKeyWorkAccepted, true)
	d.SetStateData(StateKeyAcceptedStoryID, storyID)
	d.SetStateData(StateKeyAcceptanceType, acceptanceType)
}

// Response formatting methods using templates

// ResponseKind identifies the type of approval response for formatting.
type ResponseKind string

const (
	// ResponseKindPlan represents plan approval responses.
	ResponseKindPlan ResponseKind = "plan"
	// ResponseKindCode represents code review responses.
	ResponseKindCode ResponseKind = "code"
	// ResponseKindCompletion represents completion review responses.
	ResponseKindCompletion ResponseKind = "completion"
	// ResponseKindBudget represents budget review responses.
	ResponseKindBudget ResponseKind = "budget"
)

// responseFormatConfig defines template and fallback for each response kind.
type responseFormatConfig struct {
	template       templates.StateTemplate
	fallbackPrefix string
}

// getResponseFormats returns the mapping of response kinds to their formatting configuration.
// Defined as a function to avoid global variable linter warning.
func getResponseFormats() map[ResponseKind]responseFormatConfig {
	return map[ResponseKind]responseFormatConfig{
		ResponseKindPlan: {
			template:       templates.PlanApprovalResponseTemplate,
			fallbackPrefix: "Plan Review",
		},
		ResponseKindCode: {
			template:       templates.CodeReviewResponseTemplate,
			fallbackPrefix: "Code Review",
		},
		ResponseKindCompletion: {
			template:       templates.CompletionResponseTemplate,
			fallbackPrefix: "Completion Review",
		},
		ResponseKindBudget: {
			template:       templates.BudgetReviewResponseTemplate,
			fallbackPrefix: "Budget Review",
		},
	}
}

// formatApprovalResponse formats an approval response using templates.
// Consolidated formatter for all approval response types.
func (d *Driver) formatApprovalResponse(
	kind ResponseKind,
	status proto.ApprovalStatus,
	feedback string,
	extra map[string]any,
) string {
	cfg, exists := getResponseFormats()[kind]
	if !exists {
		d.logger.Warn("Unknown response kind: %s, using generic format", kind)
		return fmt.Sprintf("Review: %s\n\n%s", status, feedback)
	}

	// Build template data with status and feedback
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Status":   string(status),
			"Feedback": feedback,
		},
	}

	// Merge any extra fields
	for k, v := range extra {
		templateData.Extra[k] = v
	}

	// Fallback format if no renderer
	fallback := fmt.Sprintf("%s: %s\n\n%s", cfg.fallbackPrefix, status, feedback)

	if d.renderer == nil {
		return fallback
	}

	content, err := d.renderer.Render(cfg.template, templateData)
	if err != nil {
		d.logger.Warn("Failed to render %s response: %v", kind, err)
		return fallback
	}

	return content
}

// getPlanApprovalResponse formats a plan approval response using templates.
func (d *Driver) getPlanApprovalResponse(status proto.ApprovalStatus, feedback string) string {
	return d.formatApprovalResponse(ResponseKindPlan, status, feedback, nil)
}

// getCodeReviewResponse formats a code review response using templates.
func (d *Driver) getCodeReviewResponse(status proto.ApprovalStatus, feedback string) string {
	return d.formatApprovalResponse(ResponseKindCode, status, feedback, nil)
}

// getCompletionResponse formats a completion review response using templates.
func (d *Driver) getCompletionResponse(status proto.ApprovalStatus, feedback string) string {
	return d.formatApprovalResponse(ResponseKindCompletion, status, feedback, nil)
}

// getBudgetReviewResponse formats a budget review response using templates.
func (d *Driver) getBudgetReviewResponse(status proto.ApprovalStatus, feedback, originState string) string {
	return d.formatApprovalResponse(ResponseKindBudget, status, feedback, map[string]any{
		"OriginState": originState,
	})
}

// handleIterativeApproval processes approval requests with iterative code exploration.
func (d *Driver) handleIterativeApproval(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	storyID := proto.GetStoryID(requestMsg)

	d.logger.Info("🔍 Starting iterative approval for %s (story: %s)", approvalType, storyID)

	// Store story_id in state data for tool logging
	d.SetStateData(StateKeyCurrentStoryID, storyID)

	// Extract coder ID from request (sender)
	coderID := requestMsg.FromAgent
	if coderID == "" {
		return nil, fmt.Errorf("approval request message missing sender (FromAgent)")
	}

	// Create tool provider rooted at coder's workspace
	toolProvider := d.createReadToolProviderForCoder(coderID)
	d.logger.Debug("Created tool provider for coder %s at /mnt/coders/%s (approval)", coderID, coderID)

	// Build prompt based on approval type
	var prompt string
	switch approvalType {
	case proto.ApprovalTypeCode:
		prompt = d.generateCodePrompt(requestMsg, approvalPayload, coderID, toolProvider)
	case proto.ApprovalTypeCompletion:
		prompt = d.generateCompletionPrompt(requestMsg, approvalPayload, coderID, toolProvider)
	default:
		return nil, fmt.Errorf("unsupported iterative approval type: %s", approvalType)
	}

	// Reset context for this approval
	templateName := fmt.Sprintf("approval-%s-%s", approvalType, storyID)
	d.contextManager.ResetForNewTemplate(templateName, prompt)

	// CheckTerminal: Look for submit_reply tool to get approval decision
	checkTerminal := func(calls []agent.ToolCall, _ []any) string {
		for i := range calls {
			if calls[i].Name == tools.ToolSubmitReply {
				// submit_reply tool was called - extract response from parameters
				if response, ok := calls[i].Parameters["response"].(string); ok && response != "" {
					// Store response for building approval result
					d.SetStateData(StateKeySubmitReply, response)
					return "SUBMIT_REPLY"
				}
			}
		}
		return "" // No terminal tool called, continue iteration
	}

	// Run toolloop for iterative approval with type-safe result extraction
	signal, result, err := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[SubmitReplyResult]{
		ContextManager: d.contextManager,
		ToolProvider:   toolProvider,
		CheckTerminal:  checkTerminal,
		ExtractResult:  ExtractSubmitReply,
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("approval_%s", storyID),
			SoftLimit: 8,  // Warn at 8 iterations
			HardLimit: 16, // Escalate at 16 iterations
			OnSoftLimit: func(count int) {
				d.logger.Warn("⚠️  Approval iteration soft limit reached (%d iterations) for story %s", count, storyID)
			},
			OnHardLimit: func(_ context.Context, key string, count int) error {
				d.logger.Error("❌ Approval iteration hard limit reached (%d iterations) for story %s - escalating", count, storyID)
				d.logger.Info("Escalation key: %s", key)
				// Set escalation state data for state machine
				d.SetStateData(StateKeyEscalationRequestID, requestMsg.ID)
				d.SetStateData(StateKeyEscalationStoryID, storyID)
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
		MaxIterations: 20, // Allow multiple inspection iterations
		MaxTokens:     agent.ArchitectMaxTokens,
		AgentID:       d.GetAgentID(),
	})

	if err != nil {
		// Check if this is an iteration limit error (normal escalation path)
		var iterErr *toolloop.IterationLimitError
		if errors.As(err, &iterErr) {
			// OnHardLimit already stored escalation state data
			d.logger.Info("📊 Iteration limit reached (%d iterations), returning escalation sentinel", iterErr.Iteration)
			return nil, ErrEscalationTriggered
		}
		return nil, fmt.Errorf("iterative approval failed: %w", err)
	}

	if signal != "SUBMIT_REPLY" {
		return nil, fmt.Errorf("expected SUBMIT_REPLY signal, got: %s", signal)
	}

	d.logger.Info("✅ Architect submitted final decision via submit_reply")

	// Clean up state data (submit_reply_response no longer stored)
	d.SetStateData("current_story_id", nil)

	// Build and return approval response
	return d.buildApprovalResponseFromSubmit(ctx, requestMsg, approvalPayload, result.Response)
}

// handleSingleTurnReview handles single-turn approval reviews (Plan and BudgetReview)
// that use the review_complete tool for structured responses.
// Uses toolloop for retry/nudging and proper logging.
func (d *Driver) handleSingleTurnReview(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	storyID := proto.GetStoryID(requestMsg)

	d.logger.Info("🔍 Starting single-turn review for %s (story: %s)", approvalType, storyID)

	// Build prompt based on approval type
	var prompt string
	switch approvalType {
	case proto.ApprovalTypePlan:
		prompt = d.generatePlanPrompt(requestMsg, approvalPayload)
	case proto.ApprovalTypeBudgetReview:
		prompt = d.generateBudgetPrompt(requestMsg)
	default:
		return nil, fmt.Errorf("unsupported single-turn review type: %s", approvalType)
	}

	// Reset context for this single-turn review
	templateName := fmt.Sprintf("review-%s-%s", approvalType, storyID)
	d.contextManager.ResetForNewTemplate(templateName, prompt)

	// CheckTerminal: Look for review_complete tool signal
	checkTerminal := func(calls []agent.ToolCall, results []any) string {
		for i := range calls {
			if calls[i].Name == tools.ToolReviewComplete {
				// review_complete tool was called - extract result and store in state
				if resultMap, ok := results[i].(map[string]any); ok {
					d.SetStateData(StateKeyReviewComplete, resultMap)
					return signalReviewComplete
				}
			}
		}
		return "" // No terminal tool called
	}

	// Run toolloop in single-turn mode with type-safe result extraction
	signal, result, err := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[ReviewCompleteResult]{
		ContextManager: d.contextManager,
		ToolProvider:   newListToolProvider([]tools.Tool{tools.NewReviewCompleteTool()}),
		CheckTerminal:  checkTerminal,
		ExtractResult:  ExtractReviewComplete,
		MaxIterations:  3, // Allow nudge retries
		MaxTokens:      agent.ArchitectMaxTokens,
		SingleTurn:     true, // Enforce single-turn completion
		AgentID:        d.GetAgentID(),
	})

	if err != nil {
		return nil, fmt.Errorf("single-turn review failed: %w", err)
	}

	if signal != signalReviewComplete {
		return nil, fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", signal)
	}

	d.logger.Info("✅ Single-turn review completed with status: %s", result.Status)

	// Clean up state data (review_complete_result no longer stored)

	// Build and return approval response
	return d.buildApprovalResponseFromReviewComplete(ctx, requestMsg, approvalPayload, result.Status, result.Feedback)
}

// getArchitectToolsForLLM converts tool metadata to LLM tool definitions.
func (d *Driver) getArchitectToolsForLLM(toolProvider *tools.ToolProvider) []tools.ToolDefinition {
	toolMetas := toolProvider.List()
	toolDefs := make([]tools.ToolDefinition, len(toolMetas))

	for i := range toolMetas {
		meta := &toolMetas[i]
		// Tool definitions from ToolProvider are already in the correct format
		toolDefs[i] = tools.ToolDefinition{
			Name:        meta.Name,
			Description: meta.Description,
			InputSchema: meta.InputSchema,
		}
	}

	return toolDefs
}

// buildApprovalResponseFromSubmit creates an approval response from submit_reply content.
func (d *Driver) buildApprovalResponseFromSubmit(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, submitResponse string) (*proto.AgentMsg, error) {
	// Parse the submit response to extract status and feedback
	responseUpper := strings.ToUpper(submitResponse)

	var status proto.ApprovalStatus
	var feedback string

	if strings.HasPrefix(responseUpper, "APPROVED") {
		status = proto.ApprovalStatusApproved
		feedback = strings.TrimSpace(strings.TrimPrefix(submitResponse, "APPROVED"))
		feedback = strings.TrimSpace(strings.TrimPrefix(feedback, ":"))
	} else if strings.HasPrefix(responseUpper, "NEEDS_CHANGES") {
		status = proto.ApprovalStatusNeedsChanges
		feedback = strings.TrimSpace(strings.TrimPrefix(submitResponse, "NEEDS_CHANGES"))
		feedback = strings.TrimSpace(strings.TrimPrefix(feedback, ":"))
	} else if strings.HasPrefix(responseUpper, "REJECTED") {
		status = proto.ApprovalStatusRejected
		feedback = strings.TrimSpace(strings.TrimPrefix(submitResponse, "REJECTED"))
		feedback = strings.TrimSpace(strings.TrimPrefix(feedback, ":"))
	} else {
		// Default to needs changes if format is unclear
		status = proto.ApprovalStatusNeedsChanges
		feedback = submitResponse
	}

	if feedback == "" {
		feedback = "Review completed via iterative exploration"
	}

	// Create approval result
	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  proto.GetApprovalID(requestMsg),
		Type:       approvalPayload.ApprovalType,
		Status:     status,
		Feedback:   feedback,
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: time.Now().UTC(),
	}

	// Handle work acceptance for approved completions
	if status == proto.ApprovalStatusApproved && approvalPayload.ApprovalType == proto.ApprovalTypeCompletion {
		storyID := proto.GetStoryID(requestMsg)
		if storyID != "" {
			completionSummary := fmt.Sprintf("Story completed via iterative review: %s", feedback)
			d.handleWorkAccepted(ctx, storyID, "completion", nil, nil, &completionSummary)
		}
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	// Copy story_id to response metadata
	if storyID, exists := requestMsg.Metadata[proto.KeyStoryID]; exists {
		proto.SetStoryID(response, storyID)
	}
	proto.SetApprovalID(response, approvalResult.ID)

	d.logger.Info("✅ Built approval response: %s - %s", status, feedback)
	return response, nil
}

// handleIterativeQuestion processes question requests with iterative code exploration.
func (d *Driver) handleIterativeQuestion(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Get state data
	stateData := d.GetStateData()

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

	d.logger.Info("🔍 Starting iterative question handling (story: %s)", storyID)

	// Store story_id in state data for tool logging
	d.SetStateData(StateKeyCurrentStoryID, storyID)

	// Check iteration limit
	iterationKey := fmt.Sprintf(StateKeyPatternQuestionIterations, requestMsg.ID)
	if d.checkIterationLimit(iterationKey, StateRequest) {
		d.logger.Error("❌ Hard iteration limit exceeded for question %s - preparing escalation", requestMsg.ID)
		// Store additional escalation context
		d.SetStateData(StateKeyEscalationRequestID, requestMsg.ID)
		d.SetStateData(StateKeyEscalationStoryID, storyID)
		// Signal escalation needed by returning sentinel error
		return nil, ErrEscalationTriggered
	}

	// Extract coder ID from request (sender)
	coderID := requestMsg.FromAgent
	if coderID == "" {
		return nil, fmt.Errorf("question message missing sender (FromAgent)")
	}

	// Create tool provider rooted at coder's workspace (lazily, once per request)
	toolProviderKey := fmt.Sprintf(StateKeyPatternToolProvider, requestMsg.ID)
	var toolProvider *tools.ToolProvider
	if tp, exists := stateData[toolProviderKey]; exists {
		var ok bool
		toolProvider, ok = tp.(*tools.ToolProvider)
		if !ok {
			return nil, fmt.Errorf("invalid tool provider type in state data")
		}
	} else {
		// Create tool provider rooted at the coder's container workspace
		toolProvider = d.createReadToolProviderForCoder(coderID)
		d.SetStateData(toolProviderKey, toolProvider)
		d.logger.Debug("Created tool provider for coder %s at /mnt/coders/%s", coderID, coderID)
	}

	// Build prompt for technical question
	prompt := d.generateQuestionPrompt(requestMsg, questionPayload, coderID, toolProvider)

	// Reset context for this iteration (first iteration only)
	iterationCount := 0
	if val, exists := stateData[iterationKey]; exists {
		if count, ok := val.(int); ok {
			iterationCount = count
		}
	}

	if iterationCount == 0 {
		templateName := fmt.Sprintf("question-%s", requestMsg.ID)
		d.contextManager.ResetForNewTemplate(templateName, prompt)
	}

	// Flush user buffer before LLM request
	if flushErr := d.contextManager.FlushUserBuffer(ctx); flushErr != nil {
		return nil, fmt.Errorf("failed to flush user buffer: %w", flushErr)
	}

	// Build messages with context
	// Only pass prompt on first iteration - subsequent iterations use context history
	var promptForMessages string
	if iterationCount == 0 {
		promptForMessages = prompt
	}
	messages := d.buildMessagesWithContext(promptForMessages)

	// Get tool definitions for LLM
	toolDefs := d.getArchitectToolsForLLM(toolProvider)

	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: agent.ArchitectMaxTokens,
		Tools:     toolDefs,
	}

	// Call LLM
	d.logger.Info("🔄 Calling LLM for iterative question (iteration %d)", iterationCount+1)
	resp, err := d.LLMClient.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	// Add assistant response to context with structured tool calls (same as PM pattern)
	if len(resp.ToolCalls) > 0 {
		// Use structured tool call tracking
		// Convert agent.ToolCall to contextmgr.ToolCall
		toolCalls := make([]contextmgr.ToolCall, len(resp.ToolCalls))
		for i := range resp.ToolCalls {
			toolCalls[i] = contextmgr.ToolCall{
				ID:         resp.ToolCalls[i].ID,
				Name:       resp.ToolCalls[i].Name,
				Parameters: resp.ToolCalls[i].Parameters,
			}
		}
		d.contextManager.AddAssistantMessageWithTools(resp.Content, toolCalls)
	} else {
		// No tool calls - just content
		d.contextManager.AddAssistantMessage(resp.Content)
	}

	// Process tool calls
	if len(resp.ToolCalls) > 0 {
		submitResponse, err := d.processArchitectToolCalls(ctx, resp.ToolCalls, toolProvider)
		if err != nil {
			return nil, fmt.Errorf("tool processing failed: %w", err)
		}

		// If submit_reply was called, use that response as the final answer
		if submitResponse != "" {
			d.logger.Info("✅ Architect submitted answer via submit_reply")
			return d.buildQuestionResponseFromSubmit(requestMsg, submitResponse)
		}

		// Otherwise, continue iteration (will be called again by state machine)
		d.logger.Info("🔄 Tools executed, continuing iteration")
		//nolint:nilnil // Intentional: nil response signals continuation, not an error
		return nil, nil
	}

	// No tool calls - nudge the LLM to use submit_reply tool
	d.logger.Warn("⚠️  LLM responded without tool calls, nudging to use submit_reply")

	// Increment iteration count before nudging
	iterationCount++
	d.SetStateData(iterationKey, iterationCount)

	// Add nudge to context
	nudgeMessage := "You must use the submit_reply tool to provide your answer. Please call submit_reply with your response as the 'content' parameter."
	d.contextManager.AddMessage("system", nudgeMessage)

	// Return nil to signal continuation (state machine will call us again)
	//nolint:nilnil // Intentional: nil response signals continuation after nudge
	return nil, nil
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
