package architect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
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
			// Always use iterative question handling with robust LLM toolloop
			response, err = d.handleIterativeQuestion(ctx, requestMsg)
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

	// Get agent-specific context
	cm := d.getContextForAgent(coderID)

	// Create tool provider rooted at coder's workspace with review_complete and get_diff
	toolProvider := d.createReviewToolProviderForCoder(coderID, true)
	d.logger.Debug("Created review tool provider for coder %s at /mnt/coders/%s (with get_diff)", coderID, coderID)

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

	// Add approval prompt as user message to preserve context continuity
	cm.AddMessage("architect-approval-prompt", prompt)

	// Get review_complete tool and wrap as terminal tool
	reviewCompleteTool, err := toolProvider.Get(tools.ToolReviewComplete)
	if err != nil {
		return nil, logx.Wrap(err, "failed to get review_complete tool")
	}
	terminalTool := reviewCompleteTool

	// Get all general tools (everything except review_complete)
	allTools := toolProvider.List()
	generalTools := make([]tools.Tool, 0, len(allTools)-1)
	//nolint:gocritic // ToolMeta is 80 bytes but value semantics preferred here
	for _, meta := range allTools {
		if meta.Name != tools.ToolReviewComplete {
			tool, err := toolProvider.Get(meta.Name)
			if err != nil {
				return nil, logx.Wrap(err, fmt.Sprintf("failed to get tool %s", meta.Name))
			}
			generalTools = append(generalTools, tool)
		}
	}

	// Run toolloop for iterative approval with type-safe result extraction
	out := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[ReviewCompleteResult]{
		ContextManager: cm, // Use agent-specific context
		GeneralTools:   generalTools,
		TerminalTool:   terminalTool,
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
		DebugLogging:  true, // Enable toolloop debug logging
	})

	// Handle outcome
	if out.Kind == toolloop.OutcomeIterationLimit {
		// OnHardLimit already stored escalation state data
		d.logger.Info("📊 Iteration limit reached (%d iterations), returning escalation sentinel", out.Iteration)
		return nil, ErrEscalationTriggered
	}
	if out.Kind != toolloop.OutcomeProcessEffect {
		return nil, fmt.Errorf("iterative approval failed: %w", out.Err)
	}

	if out.Signal != tools.SignalReviewComplete {
		return nil, fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", out.Signal)
	}

	// Extract review data from ProcessEffect.Data
	effectData, ok := out.EffectData.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("REVIEW_COMPLETE effect data is not map[string]any: %T", out.EffectData)
	}

	status, _ := effectData["status"].(string)
	feedback, _ := effectData["feedback"].(string)

	d.logger.Info("✅ Architect completed iterative review with status: %s", status)

	// Clean up state data
	d.SetStateData("current_story_id", nil)

	// Build and return approval response
	return d.buildApprovalResponseFromReviewComplete(ctx, requestMsg, approvalPayload, status, feedback)
}

// handleSingleTurnReview handles single-turn approval reviews (Plan and BudgetReview)
// that use the review_complete tool for structured responses.
// Uses toolloop for retry/nudging and proper logging.
func (d *Driver) handleSingleTurnReview(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	storyID := proto.GetStoryID(requestMsg)

	d.logger.Info("🔍 Starting single-turn review for %s (story: %s)", approvalType, storyID)

	// Get agent-specific context
	agentID := requestMsg.FromAgent
	cm := d.getContextForAgent(agentID)

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

	// Add review prompt as user message to preserve context continuity
	cm.AddMessage("architect-review-prompt", prompt)

	// Create tool provider with review_complete tool
	// Single-turn reviews only get the terminal tool (review_complete)
	// Plan reviews don't need workspace inspection - the plan itself contains the description
	// Budget reviews similarly only need to make a decision based on the request content
	agentCtx := tools.AgentContext{
		Executor:        d.executor,
		ChatService:     nil,
		ReadOnly:        true,
		NetworkDisabled: false,
		WorkDir:         "/mnt/architect", // Not used for single-turn reviews
		Agent:           nil,
	}

	// Both plan and budget reviews only get review_complete tool
	allowedTools := []string{tools.ToolReviewComplete}
	d.logger.Debug("Created tool provider for single-turn review (%s) with review_complete only", approvalType)

	toolProvider := tools.NewProvider(&agentCtx, allowedTools)

	// Get review_complete tool and wrap as terminal tool
	reviewCompleteTool, err := toolProvider.Get(tools.ToolReviewComplete)
	if err != nil {
		return nil, logx.Wrap(err, "failed to get review_complete tool")
	}
	terminalTool := reviewCompleteTool

	// No general tools - only the terminal tool
	generalTools := []tools.Tool{}

	// Run toolloop in single-turn mode with type-safe result extraction
	out := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[ReviewCompleteResult]{
		ContextManager: cm, // Use agent-specific context
		GeneralTools:   generalTools,
		TerminalTool:   terminalTool,
		MaxIterations:  3, // Allow nudge retries
		MaxTokens:      agent.ArchitectMaxTokens,
		SingleTurn:     true, // Enforce single-turn completion
		AgentID:        d.GetAgentID(),
		DebugLogging:   true, // Enable toolloop debug logging
	})

	// Handle outcome
	if out.Kind != toolloop.OutcomeProcessEffect {
		return nil, fmt.Errorf("single-turn review failed: %w", out.Err)
	}

	if out.Signal != tools.SignalReviewComplete {
		return nil, fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", out.Signal)
	}

	// Extract review data from ProcessEffect.Data
	effectData, ok := out.EffectData.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("REVIEW_COMPLETE effect data is not map[string]any: %T", out.EffectData)
	}

	status, _ := effectData["status"].(string)
	feedback, _ := effectData["feedback"].(string)

	d.logger.Info("✅ Single-turn review completed with status: %s", status)

	// Clean up state data (review_complete_result no longer stored)

	// Build and return approval response
	return d.buildApprovalResponseFromReviewComplete(ctx, requestMsg, approvalPayload, status, feedback)
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
		ContextManager: cm,
		GeneralTools:   generalTools,
		TerminalTool:   terminalTool,
		MaxIterations:  20, // Allow exploration of workspace
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        d.GetAgentID(),
		DebugLogging:   true,
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("question-%s", requestMsg.ID),
			SoftLimit: 8,
			HardLimit: 16,
			OnSoftLimit: func(count int) {
				d.logger.Warn("⚠️  Iteration %d: Approaching hard limit for question %s", count, requestMsg.ID)
			},
			OnHardLimit: func(_ context.Context, _ string, _ int) error {
				d.logger.Error("❌ Hard iteration limit exceeded for question %s - escalating", requestMsg.ID)
				// Store escalation context for state machine
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
	effectData, ok := out.EffectData.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("REPLY_SUBMITTED effect data is not map[string]any: %T", out.EffectData)
	}

	response, _ := effectData["response"].(string)
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
