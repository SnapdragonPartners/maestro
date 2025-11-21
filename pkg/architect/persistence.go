package architect

import (
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

// buildAgentRequestFromMsg creates a persistence.AgentRequest from an incoming message.
// This function extracts relevant fields from the message and its typed payload
// for storage in the database.
func buildAgentRequestFromMsg(msg *proto.AgentMsg) *persistence.AgentRequest {
	agentRequest := &persistence.AgentRequest{
		ID:        msg.ID,
		FromAgent: msg.FromAgent,
		ToAgent:   msg.ToAgent,
		CreatedAt: msg.Timestamp,
	}

	// Extract story_id from metadata.
	if storyID := proto.GetStoryID(msg); storyID != "" {
		agentRequest.StoryID = &storyID
	}

	// Set request type and content based on unified REQUEST protocol.
	if msg.Type == proto.MsgTypeREQUEST {
		agentRequest.RequestType = persistence.RequestTypeApproval

		// Extract content from typed payload.
		if typedPayload := msg.GetTypedPayload(); typedPayload != nil {
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

	// Set correlation ID from metadata (tries correlation_id, question_id, approval_id).
	if correlationID := proto.GetCorrelationID(msg); correlationID != "" {
		agentRequest.CorrelationID = &correlationID
	}

	// Set parent message ID.
	if msg.ParentMsgID != "" {
		agentRequest.ParentMsgID = &msg.ParentMsgID
	}

	return agentRequest
}

// buildAgentResponseFromMsg creates a persistence.AgentResponse from request and response messages.
// This function extracts relevant fields from the response message and its typed payload,
// falling back to the request message for context when needed.
func buildAgentResponseFromMsg(request, response *proto.AgentMsg) *persistence.AgentResponse {
	agentResponse := &persistence.AgentResponse{
		ID:        response.ID,
		FromAgent: response.FromAgent,
		ToAgent:   response.ToAgent,
		CreatedAt: response.Timestamp,
	}

	// Link to original request.
	if request.ID != "" {
		agentResponse.RequestID = &request.ID
	}

	// Extract story_id from metadata (try response first, fall back to request).
	if storyID := proto.GetStoryID(response); storyID != "" {
		agentResponse.StoryID = &storyID
	} else if storyID := proto.GetStoryID(request); storyID != "" {
		agentResponse.StoryID = &storyID
	}

	// Set response type and content based on message type.
	switch response.Type {
	case proto.MsgTypeRESPONSE:
		// Handle unified RESPONSE protocol with typed payloads.
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

		// Extract approval response if present.
		if typedPayload := response.GetTypedPayload(); typedPayload != nil {
			if typedPayload.Kind == proto.PayloadKindApprovalResponse {
				if result, err := typedPayload.ExtractApprovalResponse(); err == nil {
					// Content contains the feedback/response text.
					agentResponse.Content = result.Feedback

					// Validate status against CHECK constraint.
					if validStatus, valid := proto.ValidateApprovalStatus(string(result.Status)); valid {
						validStatusStr := string(validStatus)
						agentResponse.Status = &validStatusStr
					}
					// Note: Invalid statuses are silently ignored here.
					// The caller should log warnings if needed.
				}
			}
		}
	default:
	}

	// Set correlation ID from metadata (tries correlation_id, question_id, approval_id).
	if correlationID := proto.GetCorrelationID(response); correlationID != "" {
		agentResponse.CorrelationID = &correlationID
	}

	return agentResponse
}
