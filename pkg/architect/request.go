package architect

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/metrics"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
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

		// Extract story_id if present
		if storyID, exists := requestMsg.GetPayload("story_id"); exists {
			if storyIDStr, ok := storyID.(string); ok {
				agentRequest.StoryID = &storyIDStr
			}
		}

		// Set request type and content based on unified REQUEST protocol
		if requestMsg.Type == proto.MsgTypeREQUEST {
			agentRequest.RequestType = persistence.RequestTypeApproval
			// Extract content from different payload structures
			if content, exists := requestMsg.GetPayload("content"); exists {
				if contentStr, ok := content.(string); ok {
					agentRequest.Content = contentStr
				}
			} else if questionPayload, exists := requestMsg.GetPayload("question"); exists {
				// Handle question payload structure
				switch q := questionPayload.(type) {
				case proto.QuestionRequestPayload:
					agentRequest.Content = q.Text
				case string:
					agentRequest.Content = q
				}
			}
			if approvalType, exists := requestMsg.GetPayload("approval_type"); exists {
				if approvalTypeStr, ok := approvalType.(string); ok {
					agentRequest.ApprovalType = &approvalTypeStr
				}
			}
			if reason, exists := requestMsg.GetPayload("reason"); exists {
				if reasonStr, ok := reason.(string); ok {
					agentRequest.Reason = &reasonStr
				}
			}
		}

		// Set correlation ID if present
		if correlationID, exists := requestMsg.GetPayload("correlation_id"); exists {
			if correlationIDStr, ok := correlationID.(string); ok {
				agentRequest.CorrelationID = &correlationIDStr
			}
		}
		if correlationID, exists := requestMsg.GetPayload("question_id"); exists {
			if correlationIDStr, ok := correlationID.(string); ok {
				agentRequest.CorrelationID = &correlationIDStr
			}
		}
		if correlationID, exists := requestMsg.GetPayload("approval_id"); exists {
			if correlationIDStr, ok := correlationID.(string); ok {
				agentRequest.CorrelationID = &correlationIDStr
			}
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
		if kindRaw, exists := requestMsg.GetPayload(proto.KeyKind); exists {
			if kindStr, ok := kindRaw.(string); ok {
				switch proto.RequestKind(kindStr) {
				case proto.RequestKindQuestion:
					response, err = d.handleQuestionRequest(ctx, requestMsg)
				case proto.RequestKindApproval:
					response, err = d.handleApprovalRequest(ctx, requestMsg)
				case proto.RequestKindMerge:
					response, err = d.handleMergeRequest(ctx, requestMsg)
				case proto.RequestKindRequeue:
					err = d.handleRequeueRequest(ctx, requestMsg)
					response = nil // No response needed for requeue messages
				default:
					return StateError, fmt.Errorf("unknown request kind: %s", kindStr)
				}
			} else {
				return StateError, fmt.Errorf("request kind is not a string")
			}
		} else {
			return StateError, fmt.Errorf("no kind field in REQUEST message")
		}
	default:
		return StateError, fmt.Errorf("unknown request type: %s", requestMsg.Type)
	}

	if err != nil {
		return StateError, err
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

			// Extract story_id if present
			if storyID, exists := response.GetPayload("story_id"); exists {
				if storyIDStr, ok := storyID.(string); ok {
					agentResponse.StoryID = &storyIDStr
				}
			} else if storyID, exists := requestMsg.GetPayload("story_id"); exists {
				// Fallback to request message story_id
				if storyIDStr, ok := storyID.(string); ok {
					agentResponse.StoryID = &storyIDStr
				}
			}

			// Set response type and content based on message type
			switch response.Type {
			case proto.MsgTypeRESPONSE:
				// Handle unified RESPONSE protocol
				if kindRaw, exists := response.GetPayload(proto.KeyKind); exists {
					if kindStr, ok := kindRaw.(string); ok {
						switch proto.ResponseKind(kindStr) {
						case proto.ResponseKindQuestion:
							agentResponse.ResponseType = persistence.ResponseTypeAnswer
							if answerPayload, exists := response.GetPayload(proto.KeyAnswer); exists {
								switch ap := answerPayload.(type) {
								case proto.QuestionResponsePayload:
									agentResponse.Content = ap.AnswerText
								case string:
									agentResponse.Content = ap
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
				} else {
					agentResponse.ResponseType = persistence.ResponseTypeResult
				}

				// Extract approval_result struct if present
				if approvalResult, exists := response.GetPayload("approval_result"); exists {
					if result, ok := approvalResult.(*proto.ApprovalResult); ok {
						agentResponse.Content = result.Feedback
						// Validate status against CHECK constraint
						if validStatus, valid := proto.ValidateApprovalStatus(string(result.Status)); valid {
							validStatusStr := string(validStatus)
							agentResponse.Status = &validStatusStr
						} else {
							d.logger.Warn("Invalid approval status '%s' from ApprovalResult ignored", result.Status)
						}
						agentResponse.Feedback = &result.Feedback
					}
				}

				// Fallback to individual fields if approval_result not found
				if agentResponse.Content == "" {
					if content, exists := response.GetPayload("content"); exists {
						if contentStr, ok := content.(string); ok {
							agentResponse.Content = contentStr
						}
					}
				}
				if agentResponse.Status == nil {
					if status, exists := response.GetPayload("status"); exists {
						if statusStr, ok := status.(string); ok {
							// Validate status against CHECK constraint
							if validStatus, valid := proto.ValidateApprovalStatus(statusStr); valid {
								validStatusStr := string(validStatus)
								agentResponse.Status = &validStatusStr
							} else {
								d.logger.Warn("Invalid approval status '%s' ignored, using nil", statusStr)
							}
						}
					}
				}
				if agentResponse.Feedback == nil {
					if feedback, exists := response.GetPayload("feedback"); exists {
						if feedbackStr, ok := feedback.(string); ok {
							agentResponse.Feedback = &feedbackStr
						}
					}
				}
			default:
			}

			// Set correlation ID if present
			if correlationID, exists := response.GetPayload("correlation_id"); exists {
				if correlationIDStr, ok := correlationID.(string); ok {
					agentResponse.CorrelationID = &correlationIDStr
				}
			}
			if correlationID, exists := response.GetPayload("question_id"); exists {
				if correlationIDStr, ok := correlationID.(string); ok {
					agentResponse.CorrelationID = &correlationIDStr
				}
			}
			if correlationID, exists := response.GetPayload("approval_id"); exists {
				if correlationIDStr, ok := correlationID.(string); ok {
					agentResponse.CorrelationID = &correlationIDStr
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

	// Check if this was a successful merge request before clearing state
	var wasSuccessfulMerge bool
	if requestMsg, exists := d.stateData["current_request"]; exists {
		if agentMsg, ok := requestMsg.(*proto.AgentMsg); ok {
			requestType, _ := agentMsg.GetPayload("request_type")
			kindPayload, _ := agentMsg.GetPayload(proto.KeyKind)

			// Check if this was a merge request
			isMergeRequest := false
			if requestTypeStr, ok := requestType.(string); ok && requestTypeStr == "merge" {
				isMergeRequest = true
			} else if kindStr, ok := kindPayload.(string); ok && kindStr == string(proto.RequestKindMerge) {
				isMergeRequest = true
			}

			// If it was a merge request and we have a response stored, check if merge succeeded
			if isMergeRequest {
				if responseData, exists := d.stateData["last_response"]; exists {
					if response, ok := responseData.(*proto.AgentMsg); ok {
						if status, exists := response.GetPayload("status"); exists {
							if statusStr, ok := status.(string); ok && statusStr == string(proto.ApprovalStatusApproved) {
								wasSuccessfulMerge = true
								d.logger.Info("🔀 Detected successful merge, transitioning to DISPATCHING to release dependent stories")
							}
						}
					}
				}
			}
		}
	}

	// Clear the processed request
	delete(d.stateData, "current_request")
	delete(d.stateData, "last_response")

	// Determine next state - successful merges transition to DISPATCHING
	if wasSuccessfulMerge && d.ownsSpec() {
		return StateDispatching, nil
	} else if d.ownsSpec() {
		return StateMonitoring, nil
	} else {
		return StateWaiting, nil
	}
}

// handleQuestionRequest processes a QUESTION message and returns an ANSWER.
func (d *Driver) handleQuestionRequest(ctx context.Context, questionMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	question, exists := questionMsg.GetPayload("question")
	if !exists {
		return nil, fmt.Errorf("no question payload in message")
	}

	// Question processing will be logged to database only

	// For now, provide simple auto-response until LLM integration.
	answer := "Auto-response: Question received and acknowledged. Please proceed with your implementation."

	// If we have LLM client, use it for more intelligent responses.
	if d.llmClient != nil {
		prompt := fmt.Sprintf("Answer this coding question: %v", question)

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
	response.SetPayload(proto.KeyKind, string(proto.ResponseKindQuestion))
	response.SetPayload(proto.KeyAnswer, answer) // Use proto.KeyAnswer instead of "answer"
	response.SetPayload("content", answer)       // Also set content for fallback extraction

	// Copy correlation ID from request for proper tracking
	if correlationID, exists := questionMsg.GetPayload("correlation_id"); exists {
		response.SetPayload("correlation_id", correlationID)
	}
	// Copy story_id if present
	if storyID, exists := questionMsg.GetPayload("story_id"); exists {
		response.SetPayload("story_id", storyID)
	}

	return response, nil
}

// handleApprovalRequest processes a REQUEST message and returns a RESULT.
func (d *Driver) handleApprovalRequest(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	requestType, _ := requestMsg.GetPayload("request_type")

	// Check if this is a merge request.
	if requestTypeStr, ok := requestType.(string); ok && requestTypeStr == "merge" {
		return d.handleMergeRequest(ctx, requestMsg)
	}

	// Handle regular approval requests.
	content, _ := requestMsg.GetPayload("content")
	approvalTypeStr, _ := requestMsg.GetPayload("approval_type")
	approvalID, _ := requestMsg.GetPayload("approval_id")

	// Convert interface{} to string with type assertion
	approvalTypeString := ""
	if approvalTypeStr != nil {
		approvalTypeString, _ = approvalTypeStr.(string)
	}

	approvalIDString := ""
	if approvalID != nil {
		approvalIDString, _ = approvalID.(string)
	}

	// Approval request processing will be logged to database only

	// Parse approval type from request.
	approvalType, err := proto.ParseApprovalType(approvalTypeString)
	if err != nil {
		approvalType = proto.ApprovalTypePlan
	}

	// Persist plan to database if this is a plan approval request
	if approvalType == proto.ApprovalTypePlan && d.persistenceChannel != nil {
		// For plan requests, look for content in the "plan" field first, then "content"
		var planContent string
		var planContentFound bool

		if planPayload, exists := requestMsg.GetPayload("plan"); exists {
			if planStr, ok := planPayload.(string); ok {
				planContent = planStr
				planContentFound = true
			}
		}
		if !planContentFound {
			if contentStr, ok := content.(string); ok {
				planContent = contentStr
				planContentFound = true
			}
		}

		if planContentFound {
			// Extract story_id
			var storyIDStr string
			if storyID, exists := requestMsg.GetPayload("story_id"); exists {
				if storyID, ok := storyID.(string); ok {
					storyIDStr = storyID
				}
			}

			// Debug logging for story_id validation
			if storyIDStr == "" {
				d.logger.Error("Agent plan creation: missing story_id in request from %s", requestMsg.FromAgent)
			} else {
				d.logger.Info("Creating agent plan for story_id: '%s' (len=%d) from agent: %s", storyIDStr, len(storyIDStr), requestMsg.FromAgent)
				// Log all payload keys for debugging
				d.logger.Debug("Request payload keys: %v", func() []string {
					var keys []string
					for k := range requestMsg.Payload {
						keys = append(keys, k)
					}
					return keys
				}())
			}

			// Extract confidence if present
			var confidenceStr *string
			if confidence, exists := requestMsg.GetPayload("confidence"); exists {
				if conf, ok := confidence.(string); ok {
					confidenceStr = &conf
				}
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
			// Extract completion-specific data for better review.
			reason, _ := requestMsg.GetPayload("completion_reason")
			evidence, _ := requestMsg.GetPayload("completion_evidence")
			confidence, _ := requestMsg.GetPayload("completion_confidence")
			originalStory, _ := requestMsg.GetPayload("original_story")

			prompt = fmt.Sprintf(`Review this story completion claim:

ORIGINAL STORY:
%v

COMPLETION CLAIM:
- Reason: %v
- Evidence: %v  
- Confidence: %v

Please evaluate if the story requirements are truly satisfied based on the evidence provided.

## Decision Options:

**APPROVED** - Story is truly complete
- Use when all requirements are fully satisfied
- Effect: Story will be marked as DONE

**NEEDS_CHANGES** - Missing work identified  
- Use when coder missed requirements or needs additional work (tests, docs, validation, etc.)
- Effect: Returns to PLANNING to address missing items

**REJECTED** - Story approach is fundamentally flawed
- Use when approach is wrong or story is impossible  
- Effect: Story is abandoned

## Response Format:
Choose one: "APPROVED: [brief reason]", "NEEDS_CHANGES: [specific missing work]", or "REJECTED: [fundamental issues]".`,
				originalStory, reason, evidence, confidence)
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
		// Extract story ID and mark as completed in queue.
		if storyIDPayload, exists := requestMsg.GetPayload(proto.KeyStoryID); exists {
			if storyIDStr, ok := storyIDPayload.(string); ok && storyIDStr != "" && d.queue != nil {
				// Mark story as completed (ignore errors as this is fire-and-forget)
				_ = d.queue.MarkCompleted(storyIDStr)
			}
		}
	}

	// Create proper ApprovalResult structure.
	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  approvalIDString,
		Type:       approvalType,
		Status:     proto.ApprovalStatusApproved,
		Feedback:   feedback,
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

	// Create RESPONSE using unified protocol with individual approval fields.
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID

	// Set individual approval fields that ApprovalEffect expects
	response.SetPayload("status", approvalResult.Status.String())
	response.SetPayload("feedback", approvalResult.Feedback)
	response.SetPayload("approval_id", approvalResult.ID)

	// Also set approval_result struct for database storage
	response.SetPayload("approval_result", approvalResult)

	// Copy story_id from request for dispatcher validation
	if storyID, exists := requestMsg.GetPayload(proto.KeyStoryID); exists {
		response.SetPayload(proto.KeyStoryID, storyID)
	}

	// Approval result will be logged to database only

	return response, nil
}

// handleRequeueRequest processes a REQUEUE message (fire-and-forget).
func (d *Driver) handleRequeueRequest(_ /* ctx */ context.Context, requeueMsg *proto.AgentMsg) error {
	storyID, _ := requeueMsg.GetPayload("story_id")

	storyIDStr, _ := storyID.(string)

	if storyIDStr == "" {
		return fmt.Errorf("requeue request missing story_id")
	}

	// Load current queue state.
	if d.queue == nil {
		return fmt.Errorf("no queue available")
	}

	// Mark story as pending for reassignment.
	if err := d.queue.MarkPending(storyIDStr); err != nil {
		return fmt.Errorf("failed to requeue story %s: %w", storyIDStr, err)
	}

	// Log the requeue event - this will appear in the architect logs.
	// Requeue completed successfully

	return nil
}

// handleMergeRequest processes a merge REQUEST message and returns a RESULT.
func (d *Driver) handleMergeRequest(ctx context.Context, request *proto.AgentMsg) (*proto.AgentMsg, error) {
	prURL, _ := request.GetPayload("pr_url")
	branchName, _ := request.GetPayload("branch_name")
	storyID, _ := request.GetPayload("story_id")

	// Convert to strings safely.
	prURLStr, _ := prURL.(string)
	branchNameStr, _ := branchName.(string)
	storyIDStr, _ := storyID.(string)

	d.logger.Info("🔀 Processing merge request for story %s: PR=%s, branch=%s", storyIDStr, prURLStr, branchNameStr)

	// Attempt merge using GitHub CLI.
	mergeResult, err := d.attemptPRMerge(ctx, prURLStr, branchNameStr, storyIDStr)

	// Create RESPONSE using unified protocol.
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, request.FromAgent)
	resultMsg.ParentMsgID = request.ID
	resultMsg.SetPayload(proto.KeyKind, string(proto.ResponseKindMerge))

	// Copy story_id from request for dispatcher validation
	if storyID, exists := request.GetPayload(proto.KeyStoryID); exists {
		resultMsg.SetPayload(proto.KeyStoryID, storyID)
	}

	if err != nil {
		// Categorize error for appropriate response
		status, feedback := d.categorizeMergeError(err)
		d.logger.Error("🔀 Merge failed for story %s: %s (status: %s)", storyIDStr, err.Error(), status)

		resultMsg.SetPayload("status", string(status))
		resultMsg.SetPayload("feedback", feedback)
		if status == proto.ApprovalStatusNeedsChanges {
			resultMsg.SetPayload("error_details", err.Error()) // Preserve detailed error for debugging
		}
	} else if mergeResult != nil && mergeResult.HasConflicts {
		// Merge conflicts are always recoverable
		conflictFeedback := fmt.Sprintf("Merge conflicts detected. Resolve conflicts in the following files and resubmit: %s", mergeResult.ConflictInfo)
		d.logger.Warn("🔀 Merge conflicts for story %s: %s", storyIDStr, mergeResult.ConflictInfo)

		resultMsg.SetPayload("status", string(proto.ApprovalStatusNeedsChanges))
		resultMsg.SetPayload("feedback", conflictFeedback)
		resultMsg.SetPayload("conflict_details", mergeResult.ConflictInfo)
	} else {
		// Success
		d.logger.Info("🔀 Merge successful for story %s: commit %s", storyIDStr, mergeResult.CommitSHA)

		resultMsg.SetPayload("status", string(proto.ApprovalStatusApproved))
		resultMsg.SetPayload("feedback", "Pull request merged successfully")
		resultMsg.SetPayload("merge_commit", mergeResult.CommitSHA)

		// Mark story as completed in queue.
		if d.queue != nil {
			// Mark story as completed (ignore errors as this is fire-and-forget)
			_ = d.queue.MarkCompleted(storyIDStr)
		}

		// Update story status to "merged" in database after successful merge
		if d.persistenceChannel != nil {
			// Query metrics for this story if Prometheus is configured
			var storyMetrics *metrics.StoryMetrics
			if d.orchestratorConfig != nil && d.orchestratorConfig.Agents != nil &&
				d.orchestratorConfig.Agents.Metrics.Enabled &&
				d.orchestratorConfig.Agents.Metrics.PrometheusURL != "" {
				d.logger.Info("📊 Querying metrics for completed story %s", storyIDStr)

				queryService, err := metrics.NewQueryService(d.orchestratorConfig.Agents.Metrics.PrometheusURL)
				if err != nil {
					d.logger.Warn("📊 Failed to create metrics query service: %v", err)
				} else {
					queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()

					storyMetrics, err = queryService.GetStoryMetrics(queryCtx, storyIDStr)
					if err != nil {
						d.logger.Warn("📊 Failed to query story metrics: %v", err)
					} else if storyMetrics != nil {
						d.logger.Info("📊 Story %s metrics: prompt tokens: %d, completion tokens: %d, total cost: $%.6f",
							storyIDStr, storyMetrics.PromptTokens, storyMetrics.CompletionTokens, storyMetrics.TotalCost)
					}
				}
			}

			// Prepare status update request with metrics data
			statusReq := &persistence.UpdateStoryStatusRequest{
				StoryID: storyIDStr,
				Status:  persistence.StatusDone,
			}

			// Include metrics data if available
			if storyMetrics != nil {
				statusReq.PromptTokens = &storyMetrics.PromptTokens
				statusReq.CompletionTokens = &storyMetrics.CompletionTokens
				statusReq.CostUSD = &storyMetrics.TotalCost
			}

			// Wrap in proper Request structure
			req := &persistence.Request{
				Data:      statusReq,
				Response:  nil, // Fire-and-forget operation
				Operation: persistence.OpUpdateStoryStatus,
			}

			d.logger.Info("🔀 Updating story %s status to 'merged' after successful merge", storyIDStr)

			// Fire-and-forget database update
			select {
			case d.persistenceChannel <- req:
				d.logger.Debug("🔀 Status update sent to persistence worker")
			default:
				d.logger.Warn("🔀 Failed to send status update - persistence channel full")
			}
		}
	}

	return resultMsg, nil
}

// MergeAttemptResult represents the result of a merge attempt.
//
//nolint:govet // Simple result struct, logical grouping preferred
type MergeAttemptResult struct {
	HasConflicts bool
	ConflictInfo string
	CommitSHA    string
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
	// Extract data from request message
	var storyID string
	if val, exists := requestMsg.GetPayload("story_id"); exists {
		storyID, _ = val.(string)
	}

	var origin string
	if val, exists := requestMsg.GetPayload("origin"); exists {
		origin, _ = val.(string)
	}

	var loops int
	if val, exists := requestMsg.GetPayload("loops"); exists {
		loops, _ = val.(int)
	}

	var maxLoops int
	if val, exists := requestMsg.GetPayload("max_loops"); exists {
		maxLoops, _ = val.(int)
	}

	var contextSize int
	if val, exists := requestMsg.GetPayload("context_size"); exists {
		contextSize, _ = val.(int)
	}

	var phaseTokens int
	if val, exists := requestMsg.GetPayload("phase_tokens"); exists {
		phaseTokens, _ = val.(int)
	}

	var phaseCostUSD float64
	if val, exists := requestMsg.GetPayload("phase_cost_usd"); exists {
		phaseCostUSD, _ = val.(float64)
	}

	var totalLLMCalls int
	if val, exists := requestMsg.GetPayload("total_llm_calls"); exists {
		totalLLMCalls, _ = val.(int)
	}

	var recentActivity string
	if val, exists := requestMsg.GetPayload("recent_activity"); exists {
		recentActivity, _ = val.(string)
	}

	var issuePattern string
	if val, exists := requestMsg.GetPayload("issue_pattern"); exists {
		issuePattern, _ = val.(string)
	}

	// Get story information from queue
	var storyTitle, storyType, specContent string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			storyType = story.StoryType
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
		storyType = "app" // default
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
	if origin == "PLANNING" {
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
