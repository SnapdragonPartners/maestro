package architect

// State data keys for architect's stateData map.
// These keys are used to store and retrieve values from the architect's state map
// which persists across LLM calls and state transitions.
const (
	// Request/response tracking.
	StateKeyCurrentRequest = "current_request" // *proto.AgentMsg - current request being processed
	StateKeyLastResponse   = "last_response"   // *proto.AgentMsg - last response sent

	// Work acceptance tracking (for completion and merge).
	StateKeyWorkAccepted     = "work_accepted"            // bool - whether work was accepted
	StateKeyAcceptedStoryID  = "accepted_story_id"        // string - story ID that was accepted
	StateKeyAcceptanceType   = "acceptance_type"          // string - "completion" or "merge"
	StateKeyCurrentStoryID   = "current_story_id"         // string - current story being processed
	StateKeySpecApprovedLoad = "spec_approved_and_loaded" // bool - spec approval signal

	// Tool results from LLM calls.
	StateKeySubmitReply    = "submit_reply_response"  // string - result from submit_reply tool
	StateKeyReviewComplete = "review_complete_result" // map[string]any - result from review_complete tool

	// Escalation tracking.
	StateKeyEscalationRequestID = "escalation_request_id" // string - request ID that triggered escalation
	StateKeyEscalationStoryID   = "escalation_story_id"   // string - story ID that triggered escalation
)

// Dynamic state key patterns (use with fmt.Sprintf).
const (
	// StateKeyPatternApprovalIterations tracks iteration count for approval requests.
	// Usage: fmt.Sprintf(StateKeyPatternApprovalIterations, storyID).
	StateKeyPatternApprovalIterations = "approval_iterations_%s"

	// StateKeyPatternQuestionIterations tracks iteration count for question requests.
	// Usage: fmt.Sprintf(StateKeyPatternQuestionIterations, requestID).
	StateKeyPatternQuestionIterations = "question_iterations_%s"

	// StateKeyPatternToolProvider stores tool provider instances for specific requests.
	// Usage: fmt.Sprintf(StateKeyPatternToolProvider, requestID).
	StateKeyPatternToolProvider = "tool_provider_%s"
)
