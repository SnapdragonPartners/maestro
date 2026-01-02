package architect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
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
		case proto.RequestKindHotfix:
			response, err = d.handleHotfixRequest(ctx, requestMsg)
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
	d.SetStateData(StateKeyCurrentRequest, nil)
	d.SetStateData(StateKeyLastResponse, nil)
	d.SetStateData(StateKeyWorkAccepted, nil)
	d.SetStateData(StateKeyAcceptedStoryID, nil)
	d.SetStateData(StateKeyAcceptanceType, nil)
	d.SetStateData(StateKeySpecApprovedLoad, nil)

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

	// Check if this is a maintenance story - use lighter review
	storyID := proto.GetStoryID(requestMsg)
	if story, exists := d.queue.GetStory(storyID); exists && story.IsMaintenance {
		return d.handleMaintenanceApproval(ctx, requestMsg, approvalPayload)
	}

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
						dbStory := &persistence.Story{
							ID:            story.ID,
							SpecID:        story.SpecID,
							Title:         story.Title,
							Content:       story.Content,
							ApprovedPlan:  story.ApprovedPlan,
							Status:        story.GetStatus().ToDatabaseStatus(),
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
			if err := story.SetStatus(StatusDone); err != nil {
				// Story is already done - this is a benign duplicate completion
				d.logger.Debug("Story %s already completed: %v", storyID, err)
			}
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

	// 3. Notify PM of story completion (if PM is enabled)
	d.notifyPMOfCompletion(ctx, storyID, completionSummary)

	// 4. Check if spec is complete (for maintenance tracking)
	d.checkSpecCompletion(ctx, storyID)

	// 5. Set state data to signal that work was accepted (for DISPATCHING transition)
	d.SetStateData(StateKeyWorkAccepted, true)
	d.SetStateData(StateKeyAcceptedStoryID, storyID)
	d.SetStateData(StateKeyAcceptanceType, acceptanceType)
}

// checkSpecCompletion checks if a story's spec is complete and triggers maintenance if needed.
func (d *Driver) checkSpecCompletion(ctx context.Context, storyID string) {
	if d.queue == nil {
		return
	}

	// Get the story to find its spec ID
	story, exists := d.queue.GetStory(storyID)
	if !exists || story.SpecID == "" {
		return
	}

	// Skip maintenance stories (they don't count toward spec completion)
	if story.IsMaintenance {
		d.logger.Debug("📊 Story %s is maintenance, skipping spec completion check", storyID)
		return
	}

	specID := story.SpecID

	// Check if all stories for this spec are done
	if d.queue.CheckSpecComplete(specID) {
		total, completed := d.queue.GetSpecStoryCount(specID)
		d.logger.Info("📊 Spec %s complete: %d/%d stories done", specID, completed, total)
		d.onSpecComplete(ctx, specID)
	} else {
		total, completed := d.queue.GetSpecStoryCount(specID)
		d.logger.Debug("📊 Spec %s progress: %d/%d stories done", specID, completed, total)
	}
}

// notifyPMOfCompletion sends a story completion notification to PM.
func (d *Driver) notifyPMOfCompletion(ctx context.Context, storyID string, completionSummary *string) {
	// Get story details from queue
	if d.queue == nil {
		d.logger.Debug("Skipping PM notification: queue is nil")
		return
	}

	story, exists := d.queue.GetStory(storyID)
	if !exists {
		d.logger.Debug("Skipping PM notification: story %s not found in queue", storyID)
		return
	}

	// Build notification payload
	summary := ""
	if completionSummary != nil {
		summary = *completionSummary
	}

	notificationPayload := &proto.StoryCompletePayload{
		StoryID:   storyID,
		Title:     story.Title,
		IsHotfix:  story.IsHotfix,
		Summary:   summary,
		PRID:      story.PRID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// Create notification message to PM
	notifyMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), "pm-001")
	notifyMsg.SetTypedPayload(proto.NewStoryCompletePayload(notificationPayload))
	notifyMsg.SetMetadata(proto.KeyStoryID, storyID)

	// Send via effect
	sendEffect := &SendMessageEffect{Message: notifyMsg}
	if err := d.ExecuteEffect(ctx, sendEffect); err != nil {
		d.logger.Warn("⚠️ Failed to notify PM of story %s completion: %v", storyID, err)
	} else {
		d.logger.Info("📬 Notified PM of story %s completion (hotfix=%v)", storyID, story.IsHotfix)
	}
}

// notifyPMAllStoriesComplete sends an all-stories-complete notification to PM.
// This is called when all stories in the queue have been completed.
func (d *Driver) notifyPMAllStoriesComplete(ctx context.Context) error {
	// Get spec ID and total stories from queue
	specID := ""
	totalStories := 0
	if d.queue != nil {
		allStories := d.queue.GetAllStories()
		totalStories = len(allStories)
		// Get spec ID from first story if available
		if totalStories > 0 {
			specID = allStories[0].SpecID
		}
	}

	// Build notification payload
	notificationPayload := &proto.AllStoriesCompletePayload{
		SpecID:       specID,
		TotalStories: totalStories,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Message:      "All development stories have been completed successfully.",
		DemoReady:    true, // Assume demo is ready when all stories complete
	}

	// Create notification message to PM
	notifyMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), "pm-001")
	notifyMsg.SetTypedPayload(proto.NewAllStoriesCompletePayload(notificationPayload))

	// Send via effect
	sendEffect := &SendMessageEffect{Message: notifyMsg}
	if err := d.ExecuteEffect(ctx, sendEffect); err != nil {
		return fmt.Errorf("failed to send all-stories-complete notification: %w", err)
	}

	d.logger.Info("🎉 Notified PM that all %d stories are complete (spec=%s)", totalStories, specID)
	return nil
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
		ContextManager:     cm, // Use agent-specific context
		GeneralTools:       generalTools,
		TerminalTool:       terminalTool,
		MaxIterations:      3, // Allow nudge retries
		MaxTokens:          agent.ArchitectMaxTokens,
		SingleTurn:         true, // Enforce single-turn completion
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		StoryID:            storyID,
	})

	// Handle outcome
	if out.Kind != toolloop.OutcomeProcessEffect {
		return nil, fmt.Errorf("single-turn review failed: %w", out.Err)
	}

	if out.Signal != tools.SignalReviewComplete {
		return nil, fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", out.Signal)
	}

	// Extract review data from ProcessEffect.Data
	effectData, ok := utils.SafeAssert[map[string]any](out.EffectData)
	if !ok {
		return nil, fmt.Errorf("REVIEW_COMPLETE effect data is not map[string]any: %T", out.EffectData)
	}

	status := utils.GetMapFieldOr[string](effectData, "status", "")
	feedback := utils.GetMapFieldOr[string](effectData, "feedback", "")

	d.logger.Info("✅ Single-turn review completed with status: %s", status)

	// Clean up state data (review_complete_result no longer stored)

	// Build and return approval response
	return d.buildApprovalResponseFromReviewComplete(ctx, requestMsg, approvalPayload, status, feedback)
}

// handleMaintenanceApproval provides a lighter review process for maintenance stories.
// Maintenance stories are auto-approved since they only make low-risk changes
// (documentation, tests, knowledge sync) and have already passed CI.
func (d *Driver) handleMaintenanceApproval(_ context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	approvalIDString := proto.GetApprovalID(requestMsg)
	storyID := proto.GetStoryID(requestMsg)

	d.logger.Info("🔧 Processing maintenance approval for story %s (type: %s)", storyID, approvalType)

	// Create approval result - auto-approve maintenance stories
	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  approvalIDString,
		Type:       approvalType,
		Status:     proto.ApprovalStatusApproved,
		Feedback:   "✅ Maintenance story auto-approved. Low-risk changes (documentation, tests, or knowledge sync) do not require detailed review.",
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: time.Now().UTC(),
	}

	// Create RESPONSE using unified protocol with typed approval response payload
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID

	// Set typed approval response payload
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	// Copy story_id from request metadata for dispatcher validation
	if storyIDVal, exists := requestMsg.Metadata[proto.KeyStoryID]; exists {
		proto.SetStoryID(response, storyIDVal)
	}

	// Copy approval_id to metadata
	proto.SetApprovalID(response, approvalResult.ID)

	d.logger.Info("🔧 ✅ Maintenance story %s auto-approved", storyID)
	return response, nil
}
