package proto

import (
	"time"
)

// Unified REQUEST/RESPONSE Protocol with Kind-based Routing
// This replaces the inconsistent QUESTION/ANSWER and REQUEST/RESULT patterns
// with a single, consistent async communication model.

// RequestKind represents the type of request being made in the unified protocol.
type RequestKind string

const (
	// RequestKindQuestion represents an information request.
	RequestKindQuestion RequestKind = "QUESTION"

	// RequestKindApproval represents an approval request (plan, code, budget, etc.).
	RequestKindApproval RequestKind = "APPROVAL"

	// RequestKindExecution represents an execution/action request.
	RequestKindExecution RequestKind = "EXECUTION"

	// RequestKindMerge represents a merge request for pull requests.
	RequestKindMerge RequestKind = "MERGE"

	// RequestKindRequeue represents a story requeue request.
	RequestKindRequeue RequestKind = "REQUEUE"

	// RequestKindHotfix represents a hotfix request from PM.
	RequestKindHotfix RequestKind = "HOTFIX"
)

// ResponseKind represents the type of response being sent in the unified protocol.
type ResponseKind string

const (
	// ResponseKindQuestion represents a response to an information request.
	ResponseKindQuestion ResponseKind = "QUESTION"

	// ResponseKindApproval represents a response to an approval request.
	ResponseKindApproval ResponseKind = "APPROVAL"

	// ResponseKindExecution represents a response to an execution request.
	ResponseKindExecution ResponseKind = "EXECUTION"

	// ResponseKindMerge represents a response to a merge request.
	ResponseKindMerge ResponseKind = "MERGE"

	// ResponseKindRequeue represents a response to a requeue request.
	ResponseKindRequeue ResponseKind = "REQUEUE"
)

// UnifiedRequest represents a request in the new unified protocol.
type UnifiedRequest struct {
	ID            string            `json:"id"`
	Kind          RequestKind       `json:"kind"`           // Type of request (QUESTION, APPROVAL, etc.)
	CorrelationID string            `json:"correlation_id"` // For tracking request/response pairs
	FromAgent     string            `json:"from_agent"`
	ToAgent       string            `json:"to_agent"`
	RequestedAt   time.Time         `json:"requested_at"`
	Payload       map[string]any    `json:"payload"`           // Request-specific data
	Context       map[string]string `json:"context"`           // Additional context/metadata
	Priority      Priority          `json:"priority"`          // Request priority
	Timeout       *time.Duration    `json:"timeout,omitempty"` // Optional timeout
}

// UnifiedResponse represents a response in the new unified protocol.
type UnifiedResponse struct {
	ID            string         `json:"id"`
	RequestID     string         `json:"request_id"`     // References the original request
	Kind          ResponseKind   `json:"kind"`           // Type of response (matches request kind)
	CorrelationID string         `json:"correlation_id"` // For tracking request/response pairs
	FromAgent     string         `json:"from_agent"`
	ToAgent       string         `json:"to_agent"`
	RespondedAt   time.Time      `json:"responded_at"`
	Payload       map[string]any `json:"payload"`                 // Response-specific data
	Success       bool           `json:"success"`                 // Whether the request was successful
	ErrorMessage  string         `json:"error_message,omitempty"` // Error details if unsuccessful
}

// Question-specific payload structures

// QuestionRequestPayload represents the payload for question requests.
type QuestionRequestPayload struct {
	Text        string            `json:"text"`                  // The question text
	Context     string            `json:"context,omitempty"`     // Additional context
	Urgency     string            `json:"urgency,omitempty"`     // How urgent is the answer
	Suggestions []string          `json:"suggestions,omitempty"` // Suggested answers
	Metadata    map[string]string `json:"metadata,omitempty"`    // Question-specific metadata
}

// QuestionResponsePayload represents the payload for question responses.
type QuestionResponsePayload struct {
	AnswerText string            `json:"answer_text"`          // The answer text
	Confidence Confidence        `json:"confidence,omitempty"` // Confidence in the answer
	Sources    []string          `json:"sources,omitempty"`    // Information sources used
	FollowUp   string            `json:"follow_up,omitempty"`  // Any follow-up guidance
	Metadata   map[string]string `json:"metadata,omitempty"`   // Answer-specific metadata
}

// Approval-specific payload structures

// ApprovalRequestPayload represents the payload for approval requests.
type ApprovalRequestPayload struct {
	ApprovalType       ApprovalType      `json:"approval_type"`                 // Type of approval (plan, code, budget, etc.)
	Content            string            `json:"content"`                       // What needs approval
	Reason             string            `json:"reason"`                        // Why approval is needed
	Context            string            `json:"context,omitempty"`             // Additional context
	Confidence         Confidence        `json:"confidence,omitempty"`          // Requester's confidence
	InfrastructureSpec string            `json:"infrastructure_spec,omitempty"` // Infrastructure requirements (bootstrap) - PM spec approval only
	Metadata           map[string]string `json:"metadata,omitempty"`            // Approval-specific metadata
}

// ApprovalResponsePayload represents the payload for approval responses.
type ApprovalResponsePayload struct {
	Decision   ApprovalStatus    `json:"decision"`           // APPROVED, REJECTED, NEEDS_CHANGES
	Feedback   string            `json:"feedback,omitempty"` // Review feedback/comments
	Changes    []string          `json:"changes,omitempty"`  // Specific changes requested
	ApprovedBy string            `json:"approved_by"`        // Agent that approved
	Metadata   map[string]string `json:"metadata,omitempty"` // Response-specific metadata
}

// Merge-specific payload structures

// MergeRequestPayload represents the payload for merge requests.
type MergeRequestPayload struct {
	StoryID     string            `json:"story_id"`              // Story being merged
	BranchName  string            `json:"branch_name"`           // Branch to merge
	PRURL       string            `json:"pr_url,omitempty"`      // Pull request URL
	Description string            `json:"description,omitempty"` // Merge description
	Metadata    map[string]string `json:"metadata,omitempty"`    // Merge-specific metadata
}

// MergeResponsePayload represents the payload for merge responses.
type MergeResponsePayload struct {
	Status          string            `json:"status"`                     // merged, conflict, failed
	Feedback        string            `json:"feedback,omitempty"`         // Human-readable feedback message
	MergeCommit     string            `json:"merge_commit,omitempty"`     // Commit hash if merged
	ConflictDetails string            `json:"conflict_details,omitempty"` // Details if conflict
	ErrorDetails    string            `json:"error_details,omitempty"`    // Details if failed
	Metadata        map[string]string `json:"metadata,omitempty"`         // Response-specific metadata
}

// Requeue-specific payload structures

// RequeueRequestPayload represents the payload for requeue requests.
type RequeueRequestPayload struct {
	StoryID   string            `json:"story_id"`             // Story to requeue
	AgentID   string            `json:"agent_id"`             // Agent that failed
	Reason    string            `json:"reason"`               // Why requeue is needed
	ErrorInfo string            `json:"error_info,omitempty"` // Error details
	Metadata  map[string]string `json:"metadata,omitempty"`   // Requeue-specific metadata
}

// RequeueResponsePayload represents the payload for requeue responses.
type RequeueResponsePayload struct {
	Accepted   bool              `json:"accepted"`               // Whether requeue was accepted
	NewStoryID string            `json:"new_story_id,omitempty"` // New story ID if created
	Reason     string            `json:"reason,omitempty"`       // Reason for decision
	Metadata   map[string]string `json:"metadata,omitempty"`     // Response-specific metadata
}

// Hotfix-specific payload structures

// HotfixRequestPayload represents the payload for hotfix requests from PM to architect.
// Hotfixes are small, urgent changes that bypass the normal spec workflow.
// Uses the same requirements format as submit_stories for consistency.
type HotfixRequestPayload struct {
	Analysis     string            `json:"analysis"`           // Brief summary of what the hotfix addresses
	Platform     string            `json:"platform"`           // Platform (e.g., "go", "python", "nodejs")
	Requirements []any             `json:"requirements"`       // Array of requirement objects (same format as submit_stories)
	Urgency      string            `json:"urgency,omitempty"`  // "normal" or "urgent"
	Metadata     map[string]string `json:"metadata,omitempty"` // Hotfix-specific metadata
}

// Unified protocol validation functions use the constants defined in message.go
// No need to redefine MsgTypeREQUEST and MsgTypeRESPONSE here

// Helper functions for creating unified messages removed.
// Use NewAgentMsg + SetTypedPayload(NewXXXPayload(...)) instead.

// IsUnifiedRequest checks if a message is using the new unified request protocol.
func IsUnifiedRequest(msg *AgentMsg) bool {
	return msg.Type == MsgTypeREQUEST
}

// IsUnifiedResponse checks if a message is using the new unified response protocol.
func IsUnifiedResponse(msg *AgentMsg) bool {
	return msg.Type == MsgTypeRESPONSE
}

// GetRequestKind extracts the request kind from a unified request message.
func GetRequestKind(msg *AgentMsg) (RequestKind, bool) {
	if !IsUnifiedRequest(msg) {
		return "", false
	}

	typedPayload := msg.GetTypedPayload()
	if typedPayload == nil {
		return "", false
	}

	// Map PayloadKind to RequestKind
	switch typedPayload.Kind {
	case PayloadKindQuestionRequest:
		return RequestKindQuestion, true
	case PayloadKindApprovalRequest:
		return RequestKindApproval, true
	case PayloadKindMergeRequest:
		return RequestKindMerge, true
	case PayloadKindRequeueRequest:
		return RequestKindRequeue, true
	case PayloadKindHotfixRequest:
		return RequestKindHotfix, true
	default:
		return "", false
	}
}

// GetResponseKind extracts the response kind from a unified response message.
func GetResponseKind(msg *AgentMsg) (ResponseKind, bool) {
	if !IsUnifiedResponse(msg) {
		return "", false
	}

	typedPayload := msg.GetTypedPayload()
	if typedPayload == nil {
		return "", false
	}

	// Map PayloadKind to ResponseKind
	switch typedPayload.Kind {
	case PayloadKindQuestionResponse:
		return ResponseKindQuestion, true
	case PayloadKindApprovalResponse:
		return ResponseKindApproval, true
	case PayloadKindMergeResponse:
		return ResponseKindMerge, true
	case PayloadKindRequeueResponse:
		return ResponseKindRequeue, true
	default:
		return "", false
	}
}
