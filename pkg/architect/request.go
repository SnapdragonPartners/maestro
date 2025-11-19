package architect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/coder"
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

		// Reset context for this question
		templateName := fmt.Sprintf("question-%s", questionMsg.ID)
		d.contextManager.ResetForNewTemplate(templateName, prompt)

		// Use toolloop with submit_reply tool
		llmAnswer, err := d.toolLoop.Run(ctx, &toolloop.Config{
			ContextManager: d.contextManager,
			ToolProvider:   newListToolProvider([]tools.Tool{tools.NewSubmitReplyTool()}),
			CheckTerminal:  d.checkTerminalTools,
			OnIterationLimit: func(_ context.Context) (string, error) {
				return "", fmt.Errorf("maximum tool iterations exceeded for question answering")
			},
			MaxIterations: 10,
			MaxTokens:     agent.ArchitectMaxTokens,
			AgentID:       d.architectID,
		})

		if err == nil {
			answer = llmAnswer
		}
		// Silently fall back to auto-response on error
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

	// Handle spec review approval with spec review tools
	if approvalType == proto.ApprovalTypeSpec && d.llmClient != nil {
		return d.handleSpecReview(ctx, requestMsg, approvalPayload)
	}

	// Handle single-turn reviews (Plan and BudgetReview) with review_complete tool
	useSingleTurnReview := approvalType == proto.ApprovalTypePlan || approvalType == proto.ApprovalTypeBudgetReview
	if useSingleTurnReview && d.llmClient != nil {
		return d.handleSingleTurnReview(ctx, requestMsg, approvalPayload)
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

	// Fallback: auto-approve if none of the proper toolloop-based handlers were triggered
	// This should only happen in degraded scenarios (no LLM client or missing dependencies)
	approved := true
	feedback := "Auto-approved: Request looks good, please proceed (fallback mode - proper review handlers not available)."
	d.logger.Warn("⚠️  Using auto-approve fallback for %s approval - proper toolloop-based handler not triggered (llmClient=%v, executor=%v)",
		approvalType, d.llmClient != nil, d.executor != nil)

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

// generatePlanReviewPrompt creates a prompt for architect's plan review.
func (d *Driver) generatePlanReviewPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) string {
	storyID := requestMsg.Metadata["story_id"]
	planContent := approvalPayload.Content

	// Get story information from queue
	var storyTitle, taskContent, knowledgePack string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			taskContent = story.Content
			// Get knowledge pack if available
			if story.KnowledgePack != "" {
				knowledgePack = story.KnowledgePack
			}
		}
	}

	// Fallback values
	if storyTitle == "" {
		storyTitle = "Unknown Story"
	}
	if taskContent == "" {
		taskContent = "Task content not available"
	}

	// Create template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"StoryTitle":    storyTitle,
			"TaskContent":   taskContent,
			"PlanContent":   planContent,
			"KnowledgePack": knowledgePack,
		},
	}

	// Check if we have a renderer
	if d.renderer == nil {
		// Fallback to simple text if no renderer available
		return fmt.Sprintf(`Plan Review Request

Story: %s
Task: %s

Submitted Plan:
%s

Please review and provide decision: APPROVED, NEEDS_CHANGES, or REJECTED with specific feedback.`,
			storyTitle, taskContent, planContent)
	}

	// Render template
	prompt, err := d.renderer.Render(templates.PlanReviewArchitectTemplate, templateData)
	if err != nil {
		// Fallback to simple text
		return fmt.Sprintf(`Plan Review Request

Story: %s
Task: %s

Submitted Plan:
%s

Please review and provide decision: APPROVED, NEEDS_CHANGES, or REJECTED with specific feedback.`,
			storyTitle, taskContent, planContent)
	}

	return prompt
}

// buildApprovalResponseFromReviewComplete builds an approval response from review_complete tool result.
func (d *Driver) buildApprovalResponseFromReviewComplete(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, statusStr, feedback string) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	storyID := requestMsg.Metadata["story_id"]

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
		RequestID:  requestMsg.Metadata["approval_id"],
		Type:       approvalType,
		Status:     status,
		Feedback:   feedback,
		ReviewedBy: d.architectID,
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
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	// Copy story_id to response metadata
	if storyID != "" {
		response.SetMetadata(proto.KeyStoryID, storyID)
	}
	response.SetMetadata("approval_id", approvalResult.ID)

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
		prompt = d.generateIterativeCodeReviewPrompt(requestMsg, approvalPayload, coderID, toolProvider)
	case proto.ApprovalTypeCompletion:
		prompt = d.generateIterativeCompletionPrompt(requestMsg, approvalPayload, coderID, toolProvider)
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
					d.stateData["submit_reply_response"] = response
					return "SUBMIT_REPLY"
				}
			}
		}
		return "" // No terminal tool called, continue iteration
	}

	// OnIterationLimit: Check escalation limits and handle appropriately
	onIterationLimit := func(_ context.Context) (string, error) {
		iterationKey := fmt.Sprintf("approval_iterations_%s", storyID)
		if d.checkIterationLimit(iterationKey, StateRequest) {
			d.logger.Error("❌ Hard iteration limit exceeded for approval %s - preparing escalation", storyID)
			// Store additional escalation context
			d.stateData["escalation_request_id"] = requestMsg.ID
			d.stateData["escalation_story_id"] = storyID
			// Signal escalation needed by returning sentinel error
			return "", ErrEscalationTriggered
		}
		return "", fmt.Errorf("maximum tool iterations exceeded for approval")
	}

	// Run toolloop for iterative approval
	signal, err := d.toolLoop.Run(ctx, &toolloop.Config{
		ContextManager:   d.contextManager,
		ToolProvider:     toolProvider,
		CheckTerminal:    checkTerminal,
		OnIterationLimit: onIterationLimit,
		MaxIterations:    20, // Allow multiple inspection iterations
		MaxTokens:        agent.ArchitectMaxTokens,
		AgentID:          d.architectID,
	})

	if err != nil {
		return nil, fmt.Errorf("iterative approval failed: %w", err)
	}

	if signal != "SUBMIT_REPLY" {
		return nil, fmt.Errorf("expected SUBMIT_REPLY signal, got: %s", signal)
	}

	// Extract submit_reply response from state data
	submitResponse, ok := d.stateData["submit_reply_response"]
	if !ok {
		return nil, fmt.Errorf("submit_reply_response not found in state data")
	}

	submitResponseStr, ok := submitResponse.(string)
	if !ok {
		return nil, fmt.Errorf("submit_reply_response has invalid type")
	}

	d.logger.Info("✅ Architect submitted final decision via submit_reply")

	// Clean up state data
	delete(d.stateData, "submit_reply_response")
	delete(d.stateData, "current_story_id")

	// Build and return approval response
	return d.buildApprovalResponseFromSubmit(ctx, requestMsg, approvalPayload, submitResponseStr)
}

// handleSingleTurnReview handles single-turn approval reviews (Plan and BudgetReview)
// that use the review_complete tool for structured responses.
// Uses toolloop for retry/nudging and proper logging.
func (d *Driver) handleSingleTurnReview(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	storyID := requestMsg.Metadata["story_id"]

	d.logger.Info("🔍 Starting single-turn review for %s (story: %s)", approvalType, storyID)

	// Build prompt based on approval type
	var prompt string
	switch approvalType {
	case proto.ApprovalTypePlan:
		prompt = d.generatePlanReviewPrompt(requestMsg, approvalPayload)
	case proto.ApprovalTypeBudgetReview:
		prompt = d.generateBudgetReviewPrompt(requestMsg)
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
					d.stateData["review_complete_result"] = resultMap
					return signalReviewComplete
				}
			}
		}
		return "" // No terminal tool called
	}

	// Run toolloop in single-turn mode
	signal, err := d.toolLoop.Run(ctx, &toolloop.Config{
		ContextManager: d.contextManager,
		ToolProvider:   newListToolProvider([]tools.Tool{tools.NewReviewCompleteTool()}),
		CheckTerminal:  checkTerminal,
		MaxIterations:  3, // Allow nudge retries
		MaxTokens:      agent.ArchitectMaxTokens,
		SingleTurn:     true, // Enforce single-turn completion
		AgentID:        d.architectID,
	})

	if err != nil {
		return nil, fmt.Errorf("single-turn review failed: %w", err)
	}

	if signal != signalReviewComplete {
		return nil, fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", signal)
	}

	// Extract review result from state data
	reviewResult, ok := d.stateData["review_complete_result"]
	if !ok {
		return nil, fmt.Errorf("review_complete_result not found in state data")
	}

	resultMap, ok := reviewResult.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("review_complete_result has invalid type")
	}

	status, _ := resultMap["status"].(string)
	feedback, _ := resultMap["feedback"].(string)

	d.logger.Info("✅ Single-turn review completed with status: %s", status)

	// Clean up state data
	delete(d.stateData, "review_complete_result")

	// Build and return approval response
	return d.buildApprovalResponseFromReviewComplete(ctx, requestMsg, approvalPayload, status, feedback)
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
1. Use **list_files** to see what files the coder modified
2. Use **read_file** to inspect specific files that need review
3. Use **get_diff** to see the actual changes made
4. Analyze the code quality, correctness, and adherence to requirements

**Note:** Your read tools are automatically rooted at %s's workspace (/mnt/coders/%s), so paths are relative to their working directory

## REQUIRED: Submit Your Decision

**You MUST call the submit_reply tool to provide your final decision.** Do not respond with text only.

Call **submit_reply** with your decision in this format:
- **response**: Your complete decision as a string
- Must start with one of: APPROVED, NEEDS_CHANGES, or REJECTED
- Follow with specific feedback explaining your decision

## Available Tools

%s

## Important Notes

- You can explore the coder's workspace at /mnt/coders/%s
- You have read-only access to all their files
- Take your time to review thoroughly before submitting your decision
- **Remember: You MUST use submit_reply to send your final decision**

Begin your review now.`, coderID, storyID, storyTitle, storyContent, approvalPayload.Content, coderID, coderID, toolDocs, coderID)
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
1. Use **list_files** to see what files were created/modified
2. Use **read_file** to inspect the implementation
3. Use **get_diff** to see all changes made vs main branch
4. Verify all acceptance criteria are met

**Note:** Your read tools are automatically rooted at %s's workspace (/mnt/coders/%s), so paths are relative to their working directory

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

Begin your review now.`, coderID, storyID, storyTitle, storyContent, approvalPayload.Content, coderID, coderID, toolDocs, coderID)
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

	// Extract coder ID from request (sender)
	coderID := requestMsg.FromAgent
	if coderID == "" {
		return nil, fmt.Errorf("question message missing sender (FromAgent)")
	}

	// Create tool provider rooted at coder's workspace (lazily, once per request)
	toolProviderKey := fmt.Sprintf("tool_provider_%s", requestMsg.ID)
	var toolProvider *tools.ToolProvider
	if tp, exists := d.stateData[toolProviderKey]; exists {
		var ok bool
		toolProvider, ok = tp.(*tools.ToolProvider)
		if !ok {
			return nil, fmt.Errorf("invalid tool provider type in state data")
		}
	} else {
		// Create tool provider rooted at the coder's container workspace
		toolProvider = d.createReadToolProviderForCoder(coderID)
		d.stateData[toolProviderKey] = toolProvider
		d.logger.Debug("Created tool provider for coder %s at /mnt/coders/%s", coderID, coderID)
	}

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
	resp, err := d.llmClient.Complete(ctx, req)
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
	d.stateData[iterationKey] = iterationCount

	// Add nudge to context
	nudgeMessage := "You must use the submit_reply tool to provide your answer. Please call submit_reply with your response as the 'content' parameter."
	d.contextManager.AddMessage("system", nudgeMessage)

	// Return nil to signal continuation (state machine will call us again)
	//nolint:nilnil // Intentional: nil response signals continuation after nudge
	return nil, nil
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
1. Use **list_files** to see what files exist in the coder's workspace
2. Use **read_file** to inspect relevant code files that relate to the question
3. Use **get_diff** to see what changes the coder has made so far
4. Analyze the codebase context to provide an informed answer

**Note:** Your read tools are automatically rooted at %s's workspace (/mnt/coders/%s), so paths are relative to their working directory

## REQUIRED: Submit Your Answer

**You MUST call the submit_reply tool to provide your final answer.** Do not respond with text only.

Call **submit_reply** with your response in this format:
- **response**: Your complete answer as a string

Your answer should:
- Provide a clear, actionable answer to the question
- Reference specific files, functions, or patterns when helpful
- Suggest concrete next steps if applicable

## Available Tools

%s

## Important Notes

- You can explore the coder's workspace at /mnt/coders/%s
- You have read-only access to their files
- Use the tools to understand context before answering
- **Remember: You MUST use submit_reply to send your final answer**

Begin answering the question now.`, coderID, storyID, storyTitle, storyContent, questionPayload.Text, coderID, coderID, toolDocs, coderID)
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
