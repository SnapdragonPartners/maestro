package architect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/middleware/metrics"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/git"
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

	// Get the current request from state data.
	requestMsg, exists := d.stateData["current_request"].(*proto.AgentMsg)
	if !exists || requestMsg == nil {
		return StateError, fmt.Errorf("no current request found")
	}

	// Persist request to database (fire-and-forget)
	if d.persistenceChannel != nil {
		agentRequest := &persistence.AgentRequest{
			ID:        requestMsg.ID,
			FromAgent: requestMsg.FromAgent,
			ToAgent:   requestMsg.ToAgent,
			CreatedAt: requestMsg.Timestamp,
		}

		// Extract story_id from metadata
		if storyIDStr, exists := requestMsg.Metadata["story_id"]; exists {
			agentRequest.StoryID = &storyIDStr
		}

		// Set request type and content based on unified REQUEST protocol
		if requestMsg.Type == proto.MsgTypeREQUEST {
			agentRequest.RequestType = persistence.RequestTypeApproval

			// Extract content from typed payload
			if typedPayload := requestMsg.GetTypedPayload(); typedPayload != nil {
				switch typedPayload.Kind {
				case proto.PayloadKindQuestionRequest:
					if q, err := typedPayload.ExtractQuestionRequest(); err == nil {
						agentRequest.Content = q.Text
					}
				case proto.PayloadKindApprovalRequest:
					if a, err := typedPayload.ExtractApprovalRequest(); err == nil {
						agentRequest.Content = a.Content
						approvalTypeStr := a.ApprovalType.String()
						agentRequest.ApprovalType = &approvalTypeStr
						if a.Reason != "" {
							agentRequest.Reason = &a.Reason
						}
					}
				}
			}
		}

		// Set correlation ID from metadata
		if correlationIDStr, exists := requestMsg.Metadata["correlation_id"]; exists {
			agentRequest.CorrelationID = &correlationIDStr
		}
		if correlationIDStr, exists := requestMsg.Metadata["question_id"]; exists {
			agentRequest.CorrelationID = &correlationIDStr
		}
		if correlationIDStr, exists := requestMsg.Metadata["approval_id"]; exists {
			agentRequest.CorrelationID = &correlationIDStr
		}

		// Set parent message ID
		if requestMsg.ParentMsgID != "" {
			agentRequest.ParentMsgID = &requestMsg.ParentMsgID
		}

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
			if d.llmClient != nil && d.executor != nil {
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
		d.stateData["last_response"] = response

		// Persist response to database (fire-and-forget)
		if d.persistenceChannel != nil {
			agentResponse := &persistence.AgentResponse{
				ID:        response.ID,
				FromAgent: response.FromAgent,
				ToAgent:   response.ToAgent,
				CreatedAt: response.Timestamp,
			}

			// Set request ID for correlation
			agentResponse.RequestID = &requestMsg.ID

			// Extract story_id from metadata
			if storyIDStr, exists := response.Metadata["story_id"]; exists {
				agentResponse.StoryID = &storyIDStr
			} else if storyIDStr, exists := requestMsg.Metadata["story_id"]; exists {
				// Fallback to request message story_id
				agentResponse.StoryID = &storyIDStr
			}

			// Set response type and content based on message type
			switch response.Type {
			case proto.MsgTypeRESPONSE:
				// Handle unified RESPONSE protocol with typed payloads
				responseKind, hasKind := proto.GetResponseKind(response)
				if hasKind {
					switch responseKind {
					case proto.ResponseKindQuestion:
						agentResponse.ResponseType = persistence.ResponseTypeAnswer
						if typedPayload := response.GetTypedPayload(); typedPayload != nil {
							if q, err := typedPayload.ExtractQuestionResponse(); err == nil {
								agentResponse.Content = q.AnswerText
							}
						}
					case proto.ResponseKindApproval, proto.ResponseKindExecution, proto.ResponseKindMerge, proto.ResponseKindRequeue:
						agentResponse.ResponseType = persistence.ResponseTypeResult
					default:
						agentResponse.ResponseType = persistence.ResponseTypeResult
					}
				} else {
					agentResponse.ResponseType = persistence.ResponseTypeResult
				}

				// Extract approval response if present
				if typedPayload := response.GetTypedPayload(); typedPayload != nil {
					if typedPayload.Kind == proto.PayloadKindApprovalResponse {
						if result, err := typedPayload.ExtractApprovalResponse(); err == nil {
							// Content contains the feedback/response text
							agentResponse.Content = result.Feedback

							// Validate status against CHECK constraint
							if validStatus, valid := proto.ValidateApprovalStatus(string(result.Status)); valid {
								validStatusStr := string(validStatus)
								agentResponse.Status = &validStatusStr
							} else {
								d.logger.Warn("Invalid approval status '%s' from ApprovalResult ignored", result.Status)
							}
						}
					}
				}
			default:
			}

			// Set correlation ID from metadata
			if correlationIDStr, exists := response.Metadata["correlation_id"]; exists {
				agentResponse.CorrelationID = &correlationIDStr
			}
			if correlationIDStr, exists := response.Metadata["question_id"]; exists {
				agentResponse.CorrelationID = &correlationIDStr
			}
			if correlationIDStr, exists := response.Metadata["approval_id"]; exists {
				agentResponse.CorrelationID = &correlationIDStr
			}

			d.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpUpsertAgentResponse,
				Data:      agentResponse,
				Response:  nil, // Fire-and-forget
			}
		}
		// Response sent and persisted to database
	}

	// Check if work was accepted (completion or merge)
	var workWasAccepted bool
	if accepted, exists := d.stateData["work_accepted"]; exists {
		if acceptedBool, ok := accepted.(bool); ok && acceptedBool {
			workWasAccepted = true
			// Log the acceptance details for debugging
			if storyID, exists := d.stateData["accepted_story_id"]; exists {
				if acceptanceType, exists := d.stateData["acceptance_type"]; exists {
					d.logger.Info("🎉 Detected work acceptance for story %v via %v, transitioning to DISPATCHING to release dependent stories",
						storyID, acceptanceType)
				}
			}
		}
	}

	// Check if spec was approved and loaded (PM spec approval flow)
	var specApprovedAndLoaded bool
	if approved, exists := d.stateData["spec_approved_and_loaded"]; exists {
		if approvedBool, ok := approved.(bool); ok && approvedBool {
			specApprovedAndLoaded = true
			d.logger.Info("🎉 Spec approved and stories loaded, transitioning to DISPATCHING")
		}
	}

	// Clear the processed request and acceptance signals
	delete(d.stateData, "current_request")
	delete(d.stateData, "last_response")
	delete(d.stateData, "work_accepted")
	delete(d.stateData, "accepted_story_id")
	delete(d.stateData, "acceptance_type")
	delete(d.stateData, "spec_approved_and_loaded")

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
	if d.llmClient != nil {
		prompt := fmt.Sprintf("Answer this coding question: %s", question)

		// Get LLM response using centralized helper
		llmAnswer, err := d.callLLMWithTemplate(ctx, prompt)
		if err != nil {
		} else {
			answer = llmAnswer
		}
	}

	// Create RESPONSE using unified protocol.
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, questionMsg.FromAgent)
	response.ParentMsgID = questionMsg.ID

	// Set typed question response payload
	answerPayload := &proto.QuestionResponsePayload{
		AnswerText: answer,
		Metadata:   make(map[string]string),
	}

	// Copy correlation ID and story_id to metadata
	if correlationIDStr, exists := questionMsg.Metadata["correlation_id"]; exists {
		answerPayload.Metadata["correlation_id"] = correlationIDStr
		response.SetMetadata("correlation_id", correlationIDStr)
	}
	if storyIDStr, exists := questionMsg.Metadata["story_id"]; exists {
		answerPayload.Metadata["story_id"] = storyIDStr
		response.SetMetadata("story_id", storyIDStr)
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
	approvalIDString := requestMsg.Metadata["approval_id"]

	// Check if this approval type should use iteration pattern
	useIteration := approvalType == proto.ApprovalTypeCode || approvalType == proto.ApprovalTypeCompletion

	// If using iteration and we have LLM and executor, use iterative review
	if useIteration && d.llmClient != nil && d.executor != nil {
		return d.handleIterativeApproval(ctx, requestMsg, approvalPayload)
	}

	// Handle spec review approval with SCOPING tools
	if approvalType == proto.ApprovalTypeSpec && d.llmClient != nil {
		return d.handleSpecReview(ctx, requestMsg, approvalPayload)
	}

	// Approval request processing will be logged to database only

	// Persist plan to database if this is a plan approval request
	if approvalType == proto.ApprovalTypePlan && d.persistenceChannel != nil {
		planContent := content

		if planContent != "" {
			// Extract story_id from metadata
			storyIDStr := requestMsg.Metadata["story_id"]

			// Debug logging for story_id validation
			if storyIDStr == "" {
				d.logger.Error("Agent plan creation: missing story_id in request from %s", requestMsg.FromAgent)
			} else {
				d.logger.Info("Creating agent plan for story_id: '%s' (len=%d) from agent: %s", storyIDStr, len(storyIDStr), requestMsg.FromAgent)
			}

			// Extract confidence if present
			var confidenceStr *string
			if conf, exists := approvalPayload.Metadata["confidence"]; exists && conf != "" {
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

	// For now, auto-approve all requests until LLM integration.
	approved := true
	feedback := "Auto-approved: Request looks good, please proceed."

	// If we have LLM client, use it for more intelligent review.
	if d.llmClient != nil {
		var prompt string
		switch approvalType {
		case proto.ApprovalTypeCompletion:
			// Use story-type-aware completion approval templates
			prompt = d.generateCompletionApprovalPrompt(requestMsg, content)
		case proto.ApprovalTypeCode:
			// Use story-type-aware code review templates
			prompt = d.generateCodeReviewApprovalPrompt(requestMsg, content)
		case proto.ApprovalTypeBudgetReview:
			prompt = d.generateBudgetReviewPrompt(requestMsg)
		default:
			prompt = fmt.Sprintf("Review this request: %v", content)
		}

		// Get LLM response using centralized helper
		llmFeedback, err := d.callLLMWithTemplate(ctx, prompt)
		if err != nil {
		} else {
			feedback = llmFeedback
			// For completion requests, parse three-status response
			if approvalType == proto.ApprovalTypeCompletion {
				responseUpper := strings.ToUpper(feedback)
				if strings.Contains(responseUpper, string(proto.ApprovalStatusNeedsChanges)) {
					approved = false
					// Store the specific status to preserve NEEDS_CHANGES vs REJECTED distinction
					feedback = llmFeedback // Use the full LLM response as feedback
				} else if strings.Contains(responseUpper, string(proto.ApprovalStatusRejected)) {
					approved = false
					feedback = llmFeedback
				}
				// APPROVED or any other response defaults to approved = true
			}
			// For budget review requests, parse structured response
			if approvalType == proto.ApprovalTypeBudgetReview {
				responseUpper := strings.ToUpper(feedback)
				if strings.Contains(responseUpper, string(proto.ApprovalStatusNeedsChanges)) {
					approved = false
					// Store the specific status to preserve NEEDS_CHANGES vs REJECTED distinction
					feedback = llmFeedback // Use the full LLM response as feedback
				} else if strings.Contains(responseUpper, string(proto.ApprovalStatusRejected)) {
					approved = false
					feedback = llmFeedback
				}
				// APPROVED or any other response defaults to approved = true
			}
			// For code review requests, parse three-status response
			if approvalType == proto.ApprovalTypeCode {
				responseUpper := strings.ToUpper(feedback)
				if strings.Contains(responseUpper, string(proto.ApprovalStatusNeedsChanges)) {
					approved = false
					// Store the specific status to preserve NEEDS_CHANGES vs REJECTED distinction
					feedback = llmFeedback // Use the full LLM response as feedback
				} else if strings.Contains(responseUpper, string(proto.ApprovalStatusRejected)) {
					approved = false
					feedback = llmFeedback
				}
				// APPROVED or any other response defaults to approved = true
			}
			// For other types, always approve in LLM mode for now.
		}
	}

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
		ReviewedBy: d.architectID,
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
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID

	// Set typed approval response payload
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	// Copy story_id from request metadata for dispatcher validation
	if storyID, exists := requestMsg.Metadata[proto.KeyStoryID]; exists {
		response.SetMetadata(proto.KeyStoryID, storyID)
	}

	// Copy approval_id to metadata
	response.SetMetadata("approval_id", approvalResult.ID)

	// Approval result will be logged to database only

	return response, nil
}

// handleRequeueRequest processes a REQUEUE message (fire-and-forget).
func (d *Driver) handleRequeueRequest(_ /* ctx */ context.Context, requeueMsg *proto.AgentMsg) error {
	// Extract story_id from metadata
	storyIDStr := requeueMsg.Metadata["story_id"]

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

// handleMergeRequest processes a merge REQUEST message and returns a RESULT.
func (d *Driver) handleMergeRequest(ctx context.Context, request *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract merge request from typed payload
	typedPayload := request.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("merge request message missing typed payload")
	}

	mergePayload, err := typedPayload.ExtractGeneric()
	if err != nil {
		return nil, fmt.Errorf("failed to extract merge request: %w", err)
	}

	// Extract fields from payload
	prURLStr, _ := mergePayload["pr_url"].(string)
	branchNameStr, _ := mergePayload["branch_name"].(string)

	// Extract story_id from metadata
	storyIDStr := request.Metadata["story_id"]

	d.logger.Info("🔀 Processing merge request for story %s: PR=%s, branch=%s", storyIDStr, prURLStr, branchNameStr)

	// Attempt merge using GitHub CLI.
	mergeResult, err := d.attemptPRMerge(ctx, prURLStr, branchNameStr, storyIDStr)

	// Create RESPONSE using unified protocol.
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, request.FromAgent)
	resultMsg.ParentMsgID = request.ID

	// Copy story_id from request metadata for dispatcher validation
	if storyID, exists := request.Metadata[proto.KeyStoryID]; exists {
		resultMsg.SetMetadata(proto.KeyStoryID, storyID)
	}

	// Build merge response payload (typed)
	mergeResponsePayload := &proto.MergeResponsePayload{
		Metadata: make(map[string]string),
	}

	if err != nil {
		// Categorize error for appropriate response
		status, feedback := d.categorizeMergeError(err)
		d.logger.Error("🔀 Merge failed for story %s: %s (status: %s)", storyIDStr, err.Error(), status)

		mergeResponsePayload.Status = string(status)
		mergeResponsePayload.Feedback = feedback
		if status == proto.ApprovalStatusNeedsChanges {
			mergeResponsePayload.ErrorDetails = err.Error() // Preserve detailed error for debugging
		}
	} else if mergeResult != nil && mergeResult.HasConflicts {
		// Merge conflicts are always recoverable
		// Check if knowledge.dot is among the conflicting files and provide specific guidance
		conflictFeedback := d.generateConflictGuidance(mergeResult.ConflictInfo)
		d.logger.Warn("🔀 Merge conflicts for story %s: %s", storyIDStr, mergeResult.ConflictInfo)

		mergeResponsePayload.Status = string(proto.ApprovalStatusNeedsChanges)
		mergeResponsePayload.Feedback = conflictFeedback
		mergeResponsePayload.ConflictDetails = mergeResult.ConflictInfo
	} else {
		// Success
		d.logger.Info("🔀 Merge successful for story %s: commit %s", storyIDStr, mergeResult.CommitSHA)

		mergeResponsePayload.Status = string(proto.ApprovalStatusApproved)
		mergeResponsePayload.Feedback = "Pull request merged successfully"
		mergeResponsePayload.MergeCommit = mergeResult.CommitSHA

		// Update all dependent clones (architect, PM) to reflect the merge
		cfg, cfgErr := config.GetConfig()
		if cfgErr == nil {
			registry := git.NewRegistry(d.workDir)
			if updateErr := registry.UpdateDependentClones(ctx, cfg.Git.RepoURL, cfg.Git.TargetBranch, mergeResult.CommitSHA); updateErr != nil {
				d.logger.Warn("⚠️  Failed to update dependent clones after merge: %v (merge succeeded, continuing)", updateErr)
				// Don't fail the merge - it already succeeded. Clone updates can be retried later.
			}
		} else {
			d.logger.Warn("⚠️  Failed to get config for clone updates: %v", cfgErr)
		}

		// Extract PR ID from URL for database storage
		var prIDPtr *string
		if prURLStr != "" {
			prID := extractPRIDFromURL(prURLStr)
			if prID != "" {
				prIDPtr = &prID
			}
		}

		// Prepare completion summary
		completionSummary := fmt.Sprintf("Story completed via merge. PR: %s, Commit: %s", prURLStr, mergeResult.CommitSHA)

		// Handle work acceptance (queue completion, database persistence, state transition signal)
		d.handleWorkAccepted(ctx, storyIDStr, "merge", prIDPtr, &mergeResult.CommitSHA, &completionSummary)
	}

	// Set typed merge response payload
	resultMsg.SetTypedPayload(proto.NewMergeResponsePayload(mergeResponsePayload))

	return resultMsg, nil
}

// queryStoryMetrics retrieves metrics for a story from the internal metrics recorder.
func (d *Driver) queryStoryMetrics(_ context.Context, storyID string) *metrics.StoryMetrics {
	cfg, err := config.GetConfig()
	if err != nil {
		d.logger.Warn("📊 Failed to get config for metrics query: %v", err)
		return nil
	}

	if cfg.Agents == nil || !cfg.Agents.Metrics.Enabled {
		d.logger.Warn("📊 Metrics not enabled - skipping metrics query")
		return nil
	}

	d.logger.Info("📊 Querying internal metrics for completed story %s", storyID)

	// Get the internal metrics recorder (singleton)
	recorder := metrics.NewInternalRecorder()
	storyMetrics := recorder.GetStoryMetrics(storyID)

	if storyMetrics != nil {
		d.logger.Info("📊 Story %s metrics: prompt tokens: %d, completion tokens: %d, total tokens: %d, total cost: $%.6f",
			storyID, storyMetrics.PromptTokens, storyMetrics.CompletionTokens, storyMetrics.TotalTokens, storyMetrics.TotalCost)
	} else {
		d.logger.Warn("📊 No metrics found for story %s", storyID)
	}

	return storyMetrics
}

// extractPRIDFromURL extracts the PR number from a GitHub PR URL.
func extractPRIDFromURL(prURL string) string {
	// Extract PR number from URLs like:
	// https://github.com/owner/repo/pull/123
	// https://api.github.com/repos/owner/repo/pulls/123
	parts := strings.Split(prURL, "/")
	if len(parts) > 0 {
		// Get the last part which should be the PR number
		lastPart := parts[len(parts)-1]
		// Validate it's numeric
		if _, err := strconv.Atoi(lastPart); err == nil {
			return lastPart
		}
	}
	return ""
}

// MergeAttemptResult represents the result of a merge attempt.
//
//nolint:govet // Simple result struct, logical grouping preferred
type MergeAttemptResult struct {
	HasConflicts bool
	ConflictInfo string
	CommitSHA    string
}

// generateConflictGuidance creates detailed guidance for resolving merge conflicts.
// Provides specific instructions for knowledge.dot conflicts.
func (d *Driver) generateConflictGuidance(conflictInfo string) string {
	hasKnowledgeConflict := strings.Contains(conflictInfo, ".maestro/knowledge.dot") || strings.Contains(conflictInfo, "knowledge.dot")

	if hasKnowledgeConflict {
		return `Merge conflicts detected, including in the knowledge graph.

**KNOWLEDGE GRAPH CONFLICT RESOLUTION**

The knowledge graph (.maestro/knowledge.dot) has conflicts. Please resolve carefully:

1. **Pull the latest main branch**:
   ` + "`" + `git pull origin main` + "`" + `

2. **Open .maestro/knowledge.dot and resolve conflicts**:
   - **Keep all unique nodes from both branches** (no data loss)
   - **For duplicate node IDs with different content**:
     * Prefer status='current' over 'deprecated' or 'legacy'
     * Merge complementary descriptions if both add value
     * Choose the more specific/detailed example
     * Use the higher priority value (critical > high > medium > low)
   - **Preserve all unique edges** (relationships)
   - **Remove conflict markers** (<<<<<<, =======, >>>>>>>)
   - **Ensure valid DOT syntax** after resolution

3. **Validate the merged file**:
   - Check that all nodes have required fields (type, level, status, description)
   - Verify all enum values are correct (see schema in DOC_GRAPH.md)
   - Ensure edge references point to existing nodes
   - Confirm DOT syntax is valid (no trailing commas, balanced braces)

4. **Commit and push**:
   ` + "`" + `git add .maestro/knowledge.dot` + "`" + `
   ` + "`" + `git commit -m "Resolved knowledge graph conflicts"` + "`" + `
   ` + "`" + `git push` + "`" + `

5. **Resubmit the PR** for review

The knowledge graph is critical for architectural consistency. Take time to merge thoughtfully.

**OTHER CONFLICTS**:
` + conflictInfo
	}

	// Standard conflict message for non-knowledge files
	return fmt.Sprintf("Merge conflicts detected. Resolve conflicts in the following files and resubmit:\n\n%s", conflictInfo)
}

// attemptPRMerge attempts to merge a PR using GitHub CLI.
func (d *Driver) attemptPRMerge(ctx context.Context, prURL, branchName, storyID string) (*MergeAttemptResult, error) {
	// Use gh CLI to merge PR with squash strategy and branch deletion.
	mergeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	d.logger.Debug("🔀 Checking GitHub CLI availability")
	// Check if gh is available.
	if _, err := exec.LookPath("gh"); err != nil {
		d.logger.Error("🔀 GitHub CLI not found in PATH: %v", err)
		return nil, fmt.Errorf("gh (GitHub CLI) is not available in PATH: %w", err)
	}

	// If no PR URL provided, use branch name to find or create the PR.
	var cmd *exec.Cmd
	var output []byte
	var err error

	if prURL == "" || prURL == " " {
		if branchName == "" {
			d.logger.Error("🔀 No PR URL or branch name provided for merge")
			return nil, fmt.Errorf("no PR URL or branch name provided for merge")
		}

		d.logger.Info("🔀 Looking for existing PR for branch: %s", branchName)
		// First, try to find an existing PR for this branch.
		listCmd := exec.CommandContext(mergeCtx, "gh", "pr", "list", "--head", branchName, "--json", "number,url")
		d.logger.Debug("🔀 Executing: %s", listCmd.String())
		listOutput, listErr := listCmd.CombinedOutput()
		d.logger.Debug("🔀 PR list output: %s", string(listOutput))

		if listErr == nil && len(listOutput) > 0 && string(listOutput) != "[]" {
			// Found existing PR, try to merge it.
			d.logger.Info("🔀 Found existing PR, attempting merge for branch: %s", branchName)
			cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", branchName, "--squash", "--delete-branch")
			d.logger.Debug("🔀 Executing merge: %s", cmd.String())
			output, err = cmd.CombinedOutput()
		} else {
			// No PR found, create one first then merge.
			d.logger.Info("🔀 No existing PR found, creating new PR for branch: %s", branchName)

			// Create PR.
			createCmd := exec.CommandContext(mergeCtx, "gh", "pr", "create",
				"--title", fmt.Sprintf("Story merge: %s", storyID),
				"--body", fmt.Sprintf("Automated merge for story %s", storyID),
				"--base", "main",
				"--head", branchName)
			d.logger.Debug("🔀 Executing PR create: %s", createCmd.String())
			createOutput, createErr := createCmd.CombinedOutput()
			d.logger.Debug("🔀 PR create output: %s", string(createOutput))

			if createErr != nil {
				d.logger.Error("🔀 Failed to create PR for branch %s: %v\nOutput: %s", branchName, createErr, string(createOutput))
				return nil, fmt.Errorf("failed to create PR for branch %s: %w\nOutput: %s", branchName, createErr, string(createOutput))
			}

			d.logger.Info("🔀 PR created successfully, now attempting merge")
			// Now try to merge the newly created PR.
			cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", branchName, "--squash", "--delete-branch")
			d.logger.Debug("🔀 Executing merge: %s", cmd.String())
			output, err = cmd.CombinedOutput()
		}
	} else {
		d.logger.Info("🔀 Attempting to merge PR URL: %s", prURL)
		cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", prURL, "--squash", "--delete-branch")
		d.logger.Debug("🔀 Executing merge: %s", cmd.String())
		output, err = cmd.CombinedOutput()
	}

	d.logger.Debug("🔀 Merge command output: %s", string(output))
	result := &MergeAttemptResult{}

	if err != nil {
		d.logger.Error("🔀 Merge command failed: %v\nOutput: %s", err, string(output))

		// Check if error is due to merge conflicts.
		outputStr := strings.ToLower(string(output))
		if strings.Contains(outputStr, "conflict") || strings.Contains(outputStr, "merge conflict") {
			d.logger.Warn("🔀 Merge conflicts detected: %s", string(output))
			result.HasConflicts = true
			result.ConflictInfo = string(output)
			return result, nil // Not an error, just conflicts
		}

		// Other error (permissions, network, etc.).
		return nil, fmt.Errorf("gh pr merge failed: %w\nOutput: %s", err, string(output))
	}

	d.logger.Info("🔀 Merge command completed successfully")
	// Success - merge completed successfully

	// TODO: Parse commit SHA from gh output if needed
	result.CommitSHA = "merged" // Placeholder until we parse actual SHA

	return result, nil
}

// categorizeMergeError categorizes a merge error into appropriate status and feedback.
func (d *Driver) categorizeMergeError(err error) (proto.ApprovalStatus, string) {
	errorStr := strings.ToLower(err.Error())

	// Recoverable errors (NEEDS_CHANGES) - coder can potentially fix these
	if strings.Contains(errorStr, "conflict") || strings.Contains(errorStr, "merge conflict") {
		return proto.ApprovalStatusNeedsChanges, "Merge conflicts detected. Resolve conflicts and resubmit."
	}
	if strings.Contains(errorStr, "no pull request found") || strings.Contains(errorStr, "could not resolve to a pull request") {
		return proto.ApprovalStatusNeedsChanges, "Pull request not found. Ensure the PR is created and accessible."
	}
	if strings.Contains(errorStr, "permission denied") || strings.Contains(errorStr, "forbidden") {
		return proto.ApprovalStatusNeedsChanges, "Permission denied for merge. Check repository access and branch protection rules."
	}
	if strings.Contains(errorStr, "branch") && (strings.Contains(errorStr, "not found") || strings.Contains(errorStr, "does not exist")) {
		return proto.ApprovalStatusNeedsChanges, "Branch not found. Ensure the branch exists and is pushed to remote."
	}
	if strings.Contains(errorStr, "network") || strings.Contains(errorStr, "timeout") || strings.Contains(errorStr, "connection") {
		return proto.ApprovalStatusNeedsChanges, "Network error during merge. Please retry."
	}
	if strings.Contains(errorStr, "not mergeable") || strings.Contains(errorStr, "cannot be merged") {
		return proto.ApprovalStatusNeedsChanges, "Pull request is not mergeable. Check for conflicts or required status checks."
	}
	if strings.Contains(errorStr, "required status check") || strings.Contains(errorStr, "check") {
		return proto.ApprovalStatusNeedsChanges, "Required status checks not passing. Ensure all checks pass before merge."
	}

	// Unrecoverable errors (REJECTED) - fundamental issues
	if strings.Contains(errorStr, "gh") && strings.Contains(errorStr, "not found") {
		return proto.ApprovalStatusRejected, "GitHub CLI (gh) not available. Cannot perform merge operations."
	}
	if strings.Contains(errorStr, "not a git repository") || strings.Contains(errorStr, "repository") && strings.Contains(errorStr, "not found") {
		return proto.ApprovalStatusRejected, "Git repository not properly configured. Cannot perform merge operations."
	}
	if strings.Contains(errorStr, "authentication failed") && strings.Contains(errorStr, "token") {
		return proto.ApprovalStatusRejected, "GitHub authentication not configured. Cannot access repository."
	}

	// Default to NEEDS_CHANGES for unknown errors (safer to allow retry)
	return proto.ApprovalStatusNeedsChanges, fmt.Sprintf("Merge failed with error: %s. Please investigate and retry.", err.Error())
}

// generateBudgetReviewPrompt creates an enhanced prompt for budget review requests using templates.
func (d *Driver) generateBudgetReviewPrompt(requestMsg *proto.AgentMsg) string {
	// Extract data from typed payload
	typedPayload := requestMsg.GetTypedPayload()
	if typedPayload == nil {
		d.logger.Warn("Budget review request missing typed payload, using defaults")
		return "Budget review request missing data"
	}

	payloadData, err := typedPayload.ExtractGeneric()
	if err != nil {
		d.logger.Warn("Failed to extract budget review payload: %v", err)
		return "Budget review request data extraction failed"
	}

	// Extract fields with safe type assertions and defaults
	storyID, _ := payloadData["story_id"].(string)
	origin, _ := payloadData["origin"].(string)
	loops, _ := payloadData["loops"].(int)
	maxLoops, _ := payloadData["max_loops"].(int)
	contextSize, _ := payloadData["context_size"].(int)
	phaseTokens, _ := payloadData["phase_tokens"].(int)
	phaseCostUSD, _ := payloadData["phase_cost_usd"].(float64)
	totalLLMCalls, _ := payloadData["total_llm_calls"].(int)
	recentActivity, _ := payloadData["recent_activity"].(string)
	issuePattern, _ := payloadData["issue_pattern"].(string)

	// Get story information from queue
	var storyTitle, storyType, specContent, approvedPlan string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			storyType = story.StoryType
			// For CODING state reviews, include the approved plan for context
			if origin == string(coder.StateCoding) && story.ApprovedPlan != "" {
				approvedPlan = story.ApprovedPlan
			}
			// TODO: For now, we add a placeholder for spec content
			// In a future enhancement, we could fetch the actual spec content
			// using the story.SpecID and the persistence channel
			specContent = fmt.Sprintf("Spec ID: %s (full context available on request)", story.SpecID)
		}
	}

	// Fallback values
	if storyTitle == "" {
		storyTitle = "Unknown Story"
	}
	if storyType == "" {
		storyType = defaultStoryType // default
	}
	if recentActivity == "" {
		recentActivity = "No recent activity data available"
	}
	if issuePattern == "" {
		issuePattern = "No issue pattern detected"
	}
	if specContent == "" {
		specContent = "Spec context not available"
	}

	// Select template based on current state
	var templateName templates.StateTemplate
	if origin == string(coder.StatePlanning) {
		templateName = templates.BudgetReviewPlanningTemplate
	} else {
		templateName = templates.BudgetReviewCodingTemplate
	}

	// Create template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"StoryID":        storyID,
			"StoryTitle":     storyTitle,
			"StoryType":      storyType,
			"CurrentState":   origin,
			"Loops":          loops,
			"MaxLoops":       maxLoops,
			"ContextSize":    contextSize,
			"PhaseTokens":    phaseTokens,
			"PhaseCostUSD":   phaseCostUSD,
			"TotalLLMCalls":  totalLLMCalls,
			"RecentActivity": recentActivity,
			"IssuePattern":   issuePattern,
			"SpecContent":    specContent,
			"ApprovedPlan":   approvedPlan, // Include approved plan for CODING state context
		},
	}

	// Check if we have a renderer
	if d.renderer == nil {
		// Fallback to simple text if no renderer available
		return fmt.Sprintf(`Budget Review Request

Story: %s (ID: %s)
Type: %s
Current State: %s
Budget Exceeded: %d/%d iterations

Recent Activity:
%s

Issue Analysis:
%s

Please review and provide guidance: APPROVED, NEEDS_CHANGES, or REJECTED with specific feedback.`,
			storyTitle, storyID, storyType, origin, loops, maxLoops, recentActivity, issuePattern)
	}

	// Render template
	prompt, err := d.renderer.Render(templateName, templateData)
	if err != nil {
		// Fallback to simple text
		return fmt.Sprintf(`Budget Review Request

Story: %s (ID: %s)  
Type: %s
Current State: %s
Budget Exceeded: %d/%d iterations

Recent Activity:
%s

Issue Analysis:
%s

Please review and provide guidance: APPROVED, NEEDS_CHANGES, or REJECTED with specific feedback.`,
			storyTitle, storyID, storyType, origin, loops, maxLoops, recentActivity, issuePattern)
	}

	return prompt
}

// generateApprovalPrompt is a shared helper for story-type-aware approval prompts.
func (d *Driver) generateApprovalPrompt(requestMsg *proto.AgentMsg, content any, appTemplate, devopsTemplate templates.StateTemplate, fallbackMsg string) string {
	// Extract story ID from metadata to get story type from queue
	storyID := requestMsg.Metadata["story_id"]

	// Get story type and knowledge pack from queue (defaults to app if not found)
	storyType := defaultStoryType
	knowledgePack := ""
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyType = story.StoryType
			knowledgePack = story.KnowledgePack
		}
	}

	// Select appropriate template based on story type
	var templateName templates.StateTemplate
	if storyType == storyTypeDevOps {
		templateName = devopsTemplate
	} else {
		templateName = appTemplate
	}

	// Create template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Content":       content,
			"KnowledgePack": knowledgePack,
		},
	}

	// Add Dockerfile content for DevOps stories
	if storyType == storyTypeDevOps {
		if dockerfileContent := d.getDockerfileContent(); dockerfileContent != "" {
			templateData.DockerfileContent = dockerfileContent
		}
	}

	// Render template using the same pattern as other methods
	if d.renderer == nil {
		// Fallback to simple prompt if renderer not available
		return fmt.Sprintf("%s: %v", fallbackMsg, content)
	}

	prompt, err := d.renderer.Render(templateName, templateData)
	if err != nil {
		d.logger.Error("Failed to render approval template: %v", err)
		// Fallback to simple prompt
		return fmt.Sprintf("%s: %v", fallbackMsg, content)
	}

	return prompt
}

// getDockerfileContent reads the current Dockerfile content from the locally mounted repository.
func (d *Driver) getDockerfileContent() string {
	// Get config to find Dockerfile path
	cfg, err := config.GetConfig()
	if err != nil {
		d.logger.Debug("Failed to get config for Dockerfile path: %v", err)
		return ""
	}

	// Determine Dockerfile path (default to "Dockerfile" if not configured)
	dockerfilePath := "Dockerfile"
	if cfg.Container != nil && cfg.Container.Dockerfile != "" {
		dockerfilePath = cfg.Container.Dockerfile
	}

	// Try to read the Dockerfile from the configured project directory
	// The architect has access to the locally mounted repository through workDir
	if d.workDir == "" {
		d.logger.Debug("Work directory not set, cannot read Dockerfile")
		return ""
	}

	fullPath := filepath.Join(d.workDir, dockerfilePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		d.logger.Debug("Could not read Dockerfile at %s: %v", fullPath, err)
		return ""
	}

	return string(content)
}

// generateCompletionApprovalPrompt creates a story-type-aware prompt for completion approval requests.
func (d *Driver) generateCompletionApprovalPrompt(requestMsg *proto.AgentMsg, content any) string {
	return d.generateApprovalPrompt(requestMsg, content,
		templates.AppCompletionApprovalTemplate,
		templates.DevOpsCompletionApprovalTemplate,
		"Review this story completion claim")
}

// generateCodeReviewApprovalPrompt creates a story-type-aware prompt for code review approval requests.
func (d *Driver) generateCodeReviewApprovalPrompt(requestMsg *proto.AgentMsg, content any) string {
	return d.generateApprovalPrompt(requestMsg, content,
		templates.AppCodeReviewTemplate,
		templates.DevOpsCodeReviewTemplate,
		"Review this code implementation")
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
	d.stateData["work_accepted"] = true
	d.stateData["accepted_story_id"] = storyID
	d.stateData["acceptance_type"] = acceptanceType
}

// Response formatting methods using templates

// getPlanApprovalResponse formats a plan approval response using templates.
func (d *Driver) getPlanApprovalResponse(status proto.ApprovalStatus, feedback string) string {
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Status":   string(status),
			"Feedback": feedback,
		},
	}

	if d.renderer == nil {
		return fmt.Sprintf("Plan Review: %s\n\n%s", status, feedback)
	}

	content, err := d.renderer.Render(templates.PlanApprovalResponseTemplate, templateData)
	if err != nil {
		d.logger.Warn("Failed to render plan approval response: %v", err)
		return fmt.Sprintf("Plan Review: %s\n\n%s", status, feedback)
	}

	return content
}

// getCodeReviewResponse formats a code review response using templates.
func (d *Driver) getCodeReviewResponse(status proto.ApprovalStatus, feedback string) string {
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Status":   string(status),
			"Feedback": feedback,
		},
	}

	if d.renderer == nil {
		return fmt.Sprintf("Code Review: %s\n\n%s", status, feedback)
	}

	content, err := d.renderer.Render(templates.CodeReviewResponseTemplate, templateData)
	if err != nil {
		d.logger.Warn("Failed to render code review response: %v", err)
		return fmt.Sprintf("Code Review: %s\n\n%s", status, feedback)
	}

	return content
}

// getCompletionResponse formats a completion review response using templates.
func (d *Driver) getCompletionResponse(status proto.ApprovalStatus, feedback string) string {
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Status":   string(status),
			"Feedback": feedback,
		},
	}

	if d.renderer == nil {
		return fmt.Sprintf("Completion Review: %s\n\n%s", status, feedback)
	}

	content, err := d.renderer.Render(templates.CompletionResponseTemplate, templateData)
	if err != nil {
		d.logger.Warn("Failed to render completion response: %v", err)
		return fmt.Sprintf("Completion Review: %s\n\n%s", status, feedback)
	}

	return content
}

// getBudgetReviewResponse formats a budget review response using templates.
func (d *Driver) getBudgetReviewResponse(status proto.ApprovalStatus, feedback, originState string) string {
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Status":      string(status),
			"Feedback":    feedback,
			"OriginState": originState,
		},
	}

	if d.renderer == nil {
		return fmt.Sprintf("Budget Review: %s\n\n%s", status, feedback)
	}

	content, err := d.renderer.Render(templates.BudgetReviewResponseTemplate, templateData)
	if err != nil {
		d.logger.Warn("Failed to render budget review response: %v", err)
		return fmt.Sprintf("Budget Review: %s\n\n%s", status, feedback)
	}

	return content
}

// handleIterativeApproval processes approval requests with iterative code exploration.
func (d *Driver) handleIterativeApproval(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	storyID := requestMsg.Metadata["story_id"]

	d.logger.Info("🔍 Starting iterative approval for %s (story: %s)", approvalType, storyID)

	// Store story_id in state data for tool logging
	d.stateData["current_story_id"] = storyID

	// Check iteration limit
	iterationKey := fmt.Sprintf("approval_iterations_%s", storyID)
	if d.checkIterationLimit(iterationKey, StateRequest) {
		d.logger.Error("❌ Hard iteration limit exceeded for approval %s - preparing escalation", storyID)
		// Store additional escalation context
		d.stateData["escalation_request_id"] = requestMsg.ID
		d.stateData["escalation_story_id"] = storyID
		// Signal escalation needed by returning sentinel error
		return nil, ErrEscalationTriggered
	}

	// Create tool provider (lazily, once per request)
	toolProviderKey := fmt.Sprintf("tool_provider_%s", storyID)
	var toolProvider *tools.ToolProvider
	if tp, exists := d.stateData[toolProviderKey]; exists {
		var ok bool
		toolProvider, ok = tp.(*tools.ToolProvider)
		if !ok {
			return nil, fmt.Errorf("invalid tool provider type in state data")
		}
	} else {
		toolProvider = d.createReadToolProvider()
		d.stateData[toolProviderKey] = toolProvider
		d.logger.Debug("Created read tool provider for approval %s", storyID)
	}

	// Get coder ID from request (extract from FromAgent)
	coderID := requestMsg.FromAgent

	// Build prompt based on approval type
	var prompt string
	switch approvalType {
	case proto.ApprovalTypeCode:
		prompt = d.generateIterativeCodeReviewPrompt(requestMsg, approvalPayload, coderID, toolProvider)
	case proto.ApprovalTypeCompletion:
		prompt = d.generateIterativeCompletionPrompt(requestMsg, approvalPayload, coderID, toolProvider)
	default:
		return nil, fmt.Errorf("unsupported iterative approval type: %s", approvalType)
	}

	// Reset context for this iteration (first iteration only)
	iterationCount := 0
	if val, exists := d.stateData[iterationKey]; exists {
		if count, ok := val.(int); ok {
			iterationCount = count
		}
	}

	if iterationCount == 0 {
		templateName := fmt.Sprintf("approval-%s-%s", approvalType, storyID)
		d.contextManager.ResetForNewTemplate(templateName, prompt)
	}

	// Flush user buffer before LLM request
	if err := d.contextManager.FlushUserBuffer(ctx); err != nil {
		return nil, fmt.Errorf("failed to flush user buffer: %w", err)
	}

	// Build messages with context
	messages := d.buildMessagesWithContext(prompt)

	// Get tool definitions for LLM
	toolDefs := d.getArchitectToolsForLLM(toolProvider)

	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: agent.ArchitectMaxTokens,
		Tools:     toolDefs,
	}

	// Call LLM
	d.logger.Info("🔄 Calling LLM for iterative approval (iteration %d)", iterationCount+1)
	resp, err := d.llmClient.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	// Handle LLM response
	if err := d.handleLLMResponse(resp); err != nil {
		return nil, fmt.Errorf("LLM response handling failed: %w", err)
	}

	// Process tool calls
	if len(resp.ToolCalls) > 0 {
		submitResponse, err := d.processArchitectToolCalls(ctx, resp.ToolCalls, toolProvider)
		if err != nil {
			return nil, fmt.Errorf("tool processing failed: %w", err)
		}

		// If submit_reply was called, use that response as the final decision
		if submitResponse != "" {
			d.logger.Info("✅ Architect submitted final decision via submit_reply")
			return d.buildApprovalResponseFromSubmit(ctx, requestMsg, approvalPayload, submitResponse)
		}

		// Otherwise, continue iteration (will be called again by state machine)
		d.logger.Info("🔄 Tools executed, continuing iteration")
		// Return nil response with no error to signal state machine to call us again
		//nolint:nilnil // Intentional: nil response signals continuation, not an error
		return nil, nil
	}

	// No tool calls and no submit_reply - this is an error
	return nil, fmt.Errorf("LLM response contained no tool calls and no submit_reply signal")
}

// generateIterativeCodeReviewPrompt creates a prompt for iterative code review.
//
//nolint:dupl // Similar structure to completion prompt but intentionally different content
func (d *Driver) generateIterativeCodeReviewPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	storyID := requestMsg.Metadata["story_id"]

	// Get story info from queue for context
	var storyTitle, storyContent string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			storyContent = story.Content
		}
	}

	toolDocs := toolProvider.GenerateToolDocumentation()

	return fmt.Sprintf(`# Code Review Request (Iterative)

You are the architect reviewing code changes from %s for story: %s

**Story Title:** %s
**Story Content:**
%s

**Code Submission:**
%s

## Your Task

Review the code changes by:
1. Use **list_files** to see what files the coder modified (pass coder_id: "%s")
2. Use **read_file** to inspect specific files that need review
3. Use **get_diff** to see the actual changes made
4. Analyze the code quality, correctness, and adherence to requirements

When you have completed your review, call **submit_reply** with your decision:
- Your response must start with one of: APPROVED, NEEDS_CHANGES, or REJECTED
- Follow with specific feedback explaining your decision

## Available Tools

%s

## Important Notes

- You can explore the coder's workspace at /mnt/coders/%s
- You have read-only access to all their files
- Take your time to review thoroughly before submitting your decision
- Use multiple tool calls to gather all information you need

Begin your review now.`, coderID, storyID, storyTitle, storyContent, approvalPayload.Content, coderID, toolDocs, coderID)
}

// generateIterativeCompletionPrompt creates a prompt for iterative completion review.
//
//nolint:dupl // Similar structure to code review prompt but intentionally different content
func (d *Driver) generateIterativeCompletionPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	storyID := requestMsg.Metadata["story_id"]

	// Get story info from queue for context
	var storyTitle, storyContent string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			storyContent = story.Content
		}
	}

	toolDocs := toolProvider.GenerateToolDocumentation()

	return fmt.Sprintf(`# Story Completion Review Request (Iterative)

You are the architect reviewing a completion request from %s for story: %s

**Story Title:** %s
**Story Content:**
%s

**Completion Claim:**
%s

## Your Task

Verify the story is complete by:
1. Use **list_files** to see what files were created/modified (pass coder_id: "%s")
2. Use **read_file** to inspect the implementation
3. Use **get_diff** to see all changes made vs main branch
4. Verify all acceptance criteria are met

When you have completed your review, call **submit_reply** with your decision:
- Your response must start with one of: APPROVED, NEEDS_CHANGES, or REJECTED
- Provide specific feedback on what's complete or what still needs work

## Available Tools

%s

## Important Notes

- You can explore the coder's workspace at /mnt/coders/%s
- Verify the implementation matches the story requirements
- Check for code quality, tests, documentation as needed
- Be thorough but fair in your assessment

Begin your review now.`, coderID, storyID, storyTitle, storyContent, approvalPayload.Content, coderID, toolDocs, coderID)
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
		RequestID:  requestMsg.Metadata["approval_id"],
		Type:       approvalPayload.ApprovalType,
		Status:     status,
		Feedback:   feedback,
		ReviewedBy: d.architectID,
		ReviewedAt: time.Now().UTC(),
	}

	// Handle work acceptance for approved completions
	if status == proto.ApprovalStatusApproved && approvalPayload.ApprovalType == proto.ApprovalTypeCompletion {
		storyID := requestMsg.Metadata["story_id"]
		if storyID != "" {
			completionSummary := fmt.Sprintf("Story completed via iterative review: %s", feedback)
			d.handleWorkAccepted(ctx, storyID, "completion", nil, nil, &completionSummary)
		}
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	// Copy story_id to response metadata
	if storyID, exists := requestMsg.Metadata[proto.KeyStoryID]; exists {
		response.SetMetadata(proto.KeyStoryID, storyID)
	}
	response.SetMetadata("approval_id", approvalResult.ID)

	d.logger.Info("✅ Built approval response: %s - %s", status, feedback)
	return response, nil
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

	storyID := requestMsg.Metadata["story_id"]

	d.logger.Info("🔍 Starting iterative question handling (story: %s)", storyID)

	// Store story_id in state data for tool logging
	d.stateData["current_story_id"] = storyID

	// Check iteration limit
	iterationKey := fmt.Sprintf("question_iterations_%s", requestMsg.ID)
	if d.checkIterationLimit(iterationKey, StateRequest) {
		d.logger.Error("❌ Hard iteration limit exceeded for question %s - preparing escalation", requestMsg.ID)
		// Store additional escalation context
		d.stateData["escalation_request_id"] = requestMsg.ID
		d.stateData["escalation_story_id"] = storyID
		// Signal escalation needed by returning sentinel error
		return nil, ErrEscalationTriggered
	}

	// Create tool provider (lazily, once per request)
	toolProviderKey := fmt.Sprintf("tool_provider_%s", requestMsg.ID)
	var toolProvider *tools.ToolProvider
	if tp, exists := d.stateData[toolProviderKey]; exists {
		var ok bool
		toolProvider, ok = tp.(*tools.ToolProvider)
		if !ok {
			return nil, fmt.Errorf("invalid tool provider type in state data")
		}
	} else {
		toolProvider = d.createReadToolProvider()
		d.stateData[toolProviderKey] = toolProvider
		d.logger.Debug("Created read tool provider for question %s", requestMsg.ID)
	}

	// Get coder ID from request
	coderID := requestMsg.FromAgent

	// Build prompt for technical question
	prompt := d.generateIterativeQuestionPrompt(requestMsg, questionPayload, coderID, toolProvider)

	// Reset context for this iteration (first iteration only)
	iterationCount := 0
	if val, exists := d.stateData[iterationKey]; exists {
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
	messages := d.buildMessagesWithContext(prompt)

	// Get tool definitions for LLM
	toolDefs := d.getArchitectToolsForLLM(toolProvider)

	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: agent.ArchitectMaxTokens,
		Tools:     toolDefs,
	}

	// Call LLM
	d.logger.Info("🔄 Calling LLM for iterative question (iteration %d)", iterationCount+1)
	resp, err := d.llmClient.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	// Handle LLM response
	if err := d.handleLLMResponse(resp); err != nil {
		return nil, fmt.Errorf("LLM response handling failed: %w", err)
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

	// No tool calls and no submit_reply - this is an error
	return nil, fmt.Errorf("LLM response contained no tool calls and no submit_reply signal")
}

// generateIterativeQuestionPrompt creates a prompt for iterative technical question answering.
//
//nolint:dupl // Similar structure to other prompts but intentionally different content
func (d *Driver) generateIterativeQuestionPrompt(requestMsg *proto.AgentMsg, questionPayload *proto.QuestionRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	storyID := requestMsg.Metadata["story_id"]

	// Get story info from queue for context
	var storyTitle, storyContent string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			storyContent = story.Content
		}
	}

	toolDocs := toolProvider.GenerateToolDocumentation()

	return fmt.Sprintf(`# Technical Question from Coder (Iterative)

You are the architect answering a technical question from %s working on story: %s

**Story Title:** %s
**Story Content:**
%s

**Question:**
%s

## Your Task

Answer the technical question by:
1. Use **list_files** to see what files exist in the coder's workspace (pass coder_id: "%s")
2. Use **read_file** to inspect relevant code files that relate to the question
3. Use **get_diff** to see what changes the coder has made so far
4. Analyze the codebase context to provide an informed answer

When you have formulated your answer, call **submit_reply** with your response:
- Provide a clear, actionable answer to the question
- Reference specific files, functions, or patterns when helpful
- Suggest concrete next steps if applicable

## Available Tools

%s

## Important Notes

- You can explore the coder's workspace at /mnt/coders/%s
- You have read-only access to their files
- Use the tools to understand context before answering
- Provide specific, actionable guidance based on the actual code

Begin answering the question now.`, coderID, storyID, storyTitle, storyContent, questionPayload.Text, coderID, toolDocs, coderID)
}

// buildQuestionResponseFromSubmit creates a question response from submit_reply content.
func (d *Driver) buildQuestionResponseFromSubmit(requestMsg *proto.AgentMsg, submitResponse string) (*proto.AgentMsg, error) {
	// Create question response
	answerPayload := &proto.QuestionResponsePayload{
		AnswerText: submitResponse,
		Metadata:   make(map[string]string),
	}

	// Add exploration metadata
	answerPayload.Metadata["exploration_method"] = "iterative_with_tools"

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewQuestionResponsePayload(answerPayload))

	// Copy story_id and question_id to response metadata
	if storyID, exists := requestMsg.Metadata[proto.KeyStoryID]; exists {
		response.SetMetadata(proto.KeyStoryID, storyID)
	}
	if questionID, exists := requestMsg.Metadata["question_id"]; exists {
		response.SetMetadata("question_id", questionID)
	}

	d.logger.Info("✅ Built question response via iterative exploration")
	return response, nil
}

// handleSpecReview processes a spec review approval request from PM.
// Uses SCOPING tools (spec_feedback, submit_stories) for iterative review.
func (d *Driver) handleSpecReview(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	d.logger.Info("🔍 Architect reviewing spec from PM")

	// Extract spec markdown from Content (the critical field for approval requests)
	specMarkdown := approvalPayload.Content
	if specMarkdown == "" {
		return nil, fmt.Errorf("spec markdown not found in approval request Content field")
	}

	d.logger.Info("📄 Spec content length: %d bytes", len(specMarkdown))

	// Prepare template data for spec review
	templateData := &templates.TemplateData{
		TaskContent: specMarkdown,
		Extra: map[string]any{
			"mode":   "spec_review",
			"reason": approvalPayload.Reason,
		},
	}

	// Render spec review template
	prompt, err := d.renderer.RenderWithUserInstructions(templates.SpecAnalysisTemplate, templateData, d.workDir, "ARCHITECT")
	if err != nil {
		return nil, fmt.Errorf("failed to render spec review template: %w", err)
	}

	// Reset context for new spec review
	templateName := fmt.Sprintf("spec-review-%s", requestMsg.ID)
	d.contextManager.ResetForNewTemplate(templateName, prompt)

	// Get SCOPING tools for spec review (spec_feedback, submit_stories)
	scopingTools := d.getScopingTools()

	// Call LLM with SCOPING tools
	signal, err := d.callLLMWithTools(ctx, prompt, scopingTools)
	if err != nil {
		return nil, fmt.Errorf("failed to get LLM response for spec review: %w", err)
	}

	// Process tool signal and create RESULT message
	var approved bool
	var feedback string

	switch signal {
	case signalSpecFeedbackSent:
		// Architect requested changes via spec_feedback tool
		approved = false

		// Extract feedback from stateData
		feedbackResult, ok := d.stateData["spec_feedback_result"]
		if !ok {
			return nil, fmt.Errorf("spec_feedback result not found in state data")
		}

		feedbackMap, ok := feedbackResult.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("spec_feedback result has unexpected type")
		}

		feedbackStr, ok := feedbackMap["feedback"].(string)
		if !ok || feedbackStr == "" {
			return nil, fmt.Errorf("feedback not found in spec_feedback result")
		}

		feedback = feedbackStr
		d.logger.Info("📝 Architect requested spec changes: %s", feedback)

	case signalSubmitStoriesComplete:
		// Architect approved spec and generated stories via submit_stories tool
		approved = true
		d.logger.Info("✅ Architect approved spec and generated stories")

		// Load stories into queue from submit_stories result
		specID, storyIDs, err := d.loadStoriesFromSubmitResult(ctx, specMarkdown)
		if err != nil {
			return nil, fmt.Errorf("failed to load stories after approval: %w", err)
		}

		feedback = fmt.Sprintf("Spec approved - %d stories generated successfully (spec_id: %s)", len(storyIDs), specID)
		d.logger.Info("📦 Loaded %d stories into queue", len(storyIDs))

		// Mark that we now own this spec and should transition to DISPATCHING
		// This is checked by handleRequest to determine next state
		d.stateData["spec_approved_and_loaded"] = true

	default:
		return nil, fmt.Errorf("unexpected signal from spec review: %s", signal)
	}

	// Create RESPONSE message with approval result
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID

	// Determine approval status
	var status proto.ApprovalStatus
	if approved {
		status = proto.ApprovalStatusApproved
	} else {
		status = proto.ApprovalStatusNeedsChanges
	}

	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  requestMsg.Metadata["approval_id"],
		Type:       proto.ApprovalTypeSpec,
		Status:     status,
		Feedback:   feedback,
		ReviewedBy: d.architectID,
		ReviewedAt: response.Timestamp,
	}

	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))
	response.SetMetadata("approval_id", approvalResult.ID)

	d.logger.Info("✅ Spec review complete - sending RESULT to PM (status=%v)", status)
	return response, nil
}
