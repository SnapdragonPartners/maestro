package architect

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// handleRequest processes the request phase (handling coder requests).
func (d *Driver) handleRequest(ctx context.Context) (proto.State, error) {
	// Check for context cancellation first.
	select {
	case <-ctx.Done():
		d.logger.Info("🏗️ Request processing cancelled due to context cancellation")
		return StateError, fmt.Errorf("architect request processing cancelled: %w", ctx.Err())
	default:
	}

	// State: processing coder request

	// Get the current request from state data.
	requestMsg, exists := d.stateData["current_request"].(*proto.AgentMsg)
	if !exists || requestMsg == nil {
		d.logger.Error("🏗️ No current request found in state data or request is nil")
		return StateError, fmt.Errorf("no current request found")
	}

	d.logger.Info("🏗️ Processing %s request %s from %s", requestMsg.Type, requestMsg.ID, requestMsg.FromAgent)

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
				case proto.RequestKindRequeue:
					err = d.handleRequeueRequest(ctx, requestMsg)
					response = nil // No response needed for requeue messages
				default:
					d.logger.Error("🏗️ Unknown request kind: %s", kindStr)
					return StateError, fmt.Errorf("unknown request kind: %s", kindStr)
				}
			} else {
				d.logger.Error("🏗️ Request kind is not a string")
				return StateError, fmt.Errorf("request kind is not a string")
			}
		} else {
			d.logger.Error("🏗️ No kind field in REQUEST message")
			return StateError, fmt.Errorf("no kind field in REQUEST message")
		}
	default:
		d.logger.Error("🏗️ Unknown request type: %s", requestMsg.Type)
		return StateError, fmt.Errorf("unknown request type: %s", requestMsg.Type)
	}

	if err != nil {
		d.logger.Error("🏗️ Failed to process request %s: %v", requestMsg.ID, err)
		return StateError, err
	}

	// Send response back using Effects pattern.
	if response != nil {
		sendEffect := &SendResponseEffect{Response: response}
		if err := d.ExecuteEffect(ctx, sendEffect); err != nil {
			return StateError, err
		}

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
							d.logger.Warn("🏗️ Unknown response kind '%s', defaulting to result type", kindStr)
							agentResponse.ResponseType = persistence.ResponseTypeResult
						}
					} else {
						d.logger.Warn("🏗️ Response kind field exists but is not a string (%T), defaulting to result type", kindRaw)
						agentResponse.ResponseType = persistence.ResponseTypeResult
					}
				} else {
					d.logger.Warn("🏗️ Response missing kind field, defaulting to result type")
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
				d.logger.Warn("🏗️ Unknown response type: %s", response.Type)
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

	// Clear the processed request and return to monitoring.
	delete(d.stateData, "current_request")

	// Determine next state based on whether architect owns a spec.
	if d.ownsSpec() {
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
		llmResponse, err := d.llmClient.GenerateResponse(ctx, fmt.Sprintf("Answer this coding question: %v", question))
		if err != nil {
			d.logger.Warn("🏗️ LLM failed, using fallback answer: %v", err)
		} else {
			answer = llmResponse
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
		d.logger.Warn("🏗️ Invalid approval type %s, defaulting to plan", approvalTypeString)
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
Respond with either "APPROVED: [brief reason]" or "REJECTED: [specific feedback on what's missing]".`,
				originalStory, reason, evidence, confidence)
		case proto.ApprovalTypeBudgetReview:
			prompt = d.generateBudgetReviewPrompt(requestMsg)
		default:
			prompt = fmt.Sprintf("Review this request: %v", content)
		}

		llmResponse, err := d.llmClient.GenerateResponse(ctx, prompt)
		if err != nil {
			d.logger.Warn("🏗️ LLM failed, using fallback approval: %v", err)
		} else {
			feedback = llmResponse
			// For completion requests, parse LLM response to determine approval.
			if approvalType == proto.ApprovalTypeCompletion {
				if strings.Contains(strings.ToUpper(llmResponse), "REJECTED") {
					approved = false
				}
			}
			// For budget review requests, parse structured response
			if approvalType == proto.ApprovalTypeBudgetReview {
				responseUpper := strings.ToUpper(llmResponse)
				if strings.Contains(responseUpper, string(proto.ApprovalStatusNeedsChanges)) {
					approved = false
					// Store the specific status to preserve NEEDS_CHANGES vs REJECTED distinction
					feedback = llmResponse // Use the full LLM response as feedback
				} else if strings.Contains(responseUpper, string(proto.ApprovalStatusRejected)) {
					approved = false
					feedback = llmResponse
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
			if storyIDStr, ok := storyIDPayload.(string); ok && storyIDStr != "" {
				if d.queue != nil {
					d.logger.Info("🏗️ Marking story %s as completed in queue", storyIDStr)
					if err := d.queue.MarkCompleted(storyIDStr); err != nil {
						d.logger.Warn("🏗️ Failed to mark story %s as completed: %v", storyIDStr, err)
					}
				} else {
					d.logger.Warn("🏗️ Queue is nil, cannot mark story %s as completed", storyIDStr)
				}
			} else {
				d.logger.Warn("🏗️ Story ID is not a string or is empty: %v", storyIDPayload)
			}
		} else {
			d.logger.Warn("🏗️ No story ID found in completion approval request")
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
		} else {
			// For non-budget reviews, default to rejected
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
	reason, _ := requeueMsg.GetPayload("reason")

	storyIDStr, _ := storyID.(string)
	reasonStr, _ := reason.(string)

	d.logger.Info("🏗️ Processing story requeue request: story_id=%s, reason=%s, from=%s",
		storyIDStr, reasonStr, requeueMsg.FromAgent)

	if storyIDStr == "" {
		d.logger.Error("🏗️ Requeue request missing story_id")
		return fmt.Errorf("requeue request missing story_id")
	}

	// Load current queue state.
	if d.queue == nil {
		d.logger.Error("🏗️ No queue available for requeue")
		return fmt.Errorf("no queue available")
	}

	// Mark story as pending for reassignment.
	if err := d.queue.MarkPending(storyIDStr); err != nil {
		d.logger.Error("🏗️ Failed to requeue story %s: %v", storyIDStr, err)
		return fmt.Errorf("failed to requeue story %s: %w", storyIDStr, err)
	}

	// Log the requeue event - this will appear in the architect logs.
	d.logger.Info("🏗️ Story %s successfully requeued due to: %s (from agent %s)",
		storyIDStr, reasonStr, requeueMsg.FromAgent)

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

	d.logger.Info("🏗️ Processing merge request for story %s, PR: %s, branch: %s", storyIDStr, prURLStr, branchNameStr)

	// Attempt merge using GitHub CLI.
	mergeResult, err := d.attemptPRMerge(ctx, prURLStr, branchNameStr, storyIDStr)

	// Create RESPONSE using unified protocol.
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, request.FromAgent)
	resultMsg.ParentMsgID = request.ID

	// Copy story_id from request for dispatcher validation
	if storyID, exists := request.GetPayload(proto.KeyStoryID); exists {
		resultMsg.SetPayload(proto.KeyStoryID, storyID)
	}

	if err != nil || (mergeResult != nil && mergeResult.HasConflicts) {
		if err != nil {
			d.logger.Info("🏗️ Merge failed with error for story %s: %v", storyIDStr, err)
			resultMsg.SetPayload("status", "merge_error")
			resultMsg.SetPayload("error_details", err.Error())
		} else {
			d.logger.Info("🏗️ Merge failed with conflicts for story %s", storyIDStr)
			resultMsg.SetPayload("status", "merge_conflict")
			resultMsg.SetPayload("conflict_details", mergeResult.ConflictInfo)
		}
	} else {
		d.logger.Info("🏗️ Merge successful for story %s, commit: %s", storyIDStr, mergeResult.CommitSHA)
		resultMsg.SetPayload("status", "merged")
		resultMsg.SetPayload("merge_commit", mergeResult.CommitSHA)

		// Mark story as completed in queue.
		if d.queue != nil {
			if err := d.queue.MarkCompleted(storyIDStr); err != nil {
				d.logger.Warn("🏗️ Failed to mark story %s as completed: %v", storyIDStr, err)
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
	d.logger.Info("🏗️ Attempting to merge PR: %s, branch: %s", prURL, branchName)

	// Use gh CLI to merge PR with squash strategy and branch deletion.
	mergeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Check if gh is available.
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh (GitHub CLI) is not available in PATH: %w", err)
	}

	// If no PR URL provided, use branch name to find or create the PR.
	var cmd *exec.Cmd
	var output []byte
	var err error

	if prURL == "" || prURL == " " {
		if branchName == "" {
			return nil, fmt.Errorf("no PR URL or branch name provided for merge")
		}
		d.logger.Info("🏗️ No PR URL provided, checking for existing PR for branch: %s", branchName)

		// First, try to find an existing PR for this branch.
		listCmd := exec.CommandContext(mergeCtx, "gh", "pr", "list", "--head", branchName, "--json", "number,url")
		listOutput, listErr := listCmd.CombinedOutput()

		if listErr == nil && len(listOutput) > 0 && string(listOutput) != "[]" {
			// Found existing PR, try to merge it.
			d.logger.Info("🏗️ Found existing PR for branch %s, attempting merge", branchName)
			cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", branchName, "--squash", "--delete-branch")
			output, err = cmd.CombinedOutput()
		} else {
			// No PR found, create one first then merge.
			d.logger.Info("🏗️ No existing PR found for branch %s, creating PR first", branchName)

			// Create PR.
			createCmd := exec.CommandContext(mergeCtx, "gh", "pr", "create",
				"--title", fmt.Sprintf("Story merge: %s", storyID),
				"--body", fmt.Sprintf("Automated merge for story %s", storyID),
				"--base", "main",
				"--head", branchName)
			createOutput, createErr := createCmd.CombinedOutput()

			if createErr != nil {
				return nil, fmt.Errorf("failed to create PR for branch %s: %w\nOutput: %s", branchName, createErr, string(createOutput))
			}

			d.logger.Info("🏗️ Created PR for branch %s, now attempting merge", branchName)
			// Now try to merge the newly created PR.
			cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", branchName, "--squash", "--delete-branch")
			output, err = cmd.CombinedOutput()
		}
	} else {
		cmd = exec.CommandContext(mergeCtx, "gh", "pr", "merge", prURL, "--squash", "--delete-branch")
		output, err = cmd.CombinedOutput()
	}

	result := &MergeAttemptResult{}

	if err != nil {
		// Check if error is due to merge conflicts.
		outputStr := strings.ToLower(string(output))
		if strings.Contains(outputStr, "conflict") || strings.Contains(outputStr, "merge conflict") {
			mergeTarget := prURL
			if mergeTarget == "" {
				mergeTarget = branchName
			}
			d.logger.Info("🏗️ Merge conflicts detected for %s", mergeTarget)
			result.HasConflicts = true
			result.ConflictInfo = string(output)
			return result, nil // Not an error, just conflicts
		}

		// Other error (permissions, network, etc.).
		mergeTarget := prURL
		if mergeTarget == "" {
			mergeTarget = branchName
		}
		d.logger.Error("🏗️ Failed to merge %s: %v\nOutput: %s", mergeTarget, err, string(output))
		return nil, fmt.Errorf("gh pr merge failed: %w\nOutput: %s", err, string(output))
	}

	// Success - extract commit SHA from output if available.
	outputStr := string(output)
	mergeTarget := prURL
	if mergeTarget == "" {
		mergeTarget = branchName
	}
	d.logger.Info("🏗️ Merge successful for %s\nOutput: %s", mergeTarget, outputStr)

	// TODO: Parse commit SHA from gh output if needed
	result.CommitSHA = "merged" // Placeholder until we parse actual SHA

	return result, nil
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
		d.logger.Warn("🏗️ Failed to render budget review template: %v", err)
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
