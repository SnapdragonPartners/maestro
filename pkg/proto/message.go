package proto

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type MsgType string

const (
	MsgTypeTASK     MsgType = "TASK"
	MsgTypeQUESTION MsgType = "QUESTION" // Information request: "How should I approach this?"
	MsgTypeANSWER   MsgType = "ANSWER"   // Information response: "Here's the guidance..."
	MsgTypeREQUEST  MsgType = "REQUEST"  // Approval request: "Please review this code"
	MsgTypeRESULT   MsgType = "RESULT"   // Approval response: "APPROVED/REJECTED/NEEDS_CHANGES"
	MsgTypeERROR    MsgType = "ERROR"
	MsgTypeSHUTDOWN MsgType = "SHUTDOWN"
)

// RequestType represents the type of request being made
type RequestType string

const (
	// RequestApproval indicates an approval request
	RequestApproval RequestType = "approval"
	
	// RequestApprovalReview indicates an approval request review
	RequestApprovalReview RequestType = "approval_request"
	
	// RequestQuestion indicates a question request
	RequestQuestion RequestType = "question"
)

// Common payload and metadata keys used in agent messages
const (
	// Payload keys
	KeyRequestType   = "request_type"
	KeyApprovalType  = "approval_type"
	KeyAnswer        = "answer"
	KeyReason        = "reason"
	KeyQuestion      = "question"
	KeyContent       = "content"
	KeyStatus        = "status"
	KeyFeedback      = "feedback"
	KeyCurrentState  = "current_state"
	KeyRequest       = "request"
	
	// Task-related keys
	KeyTaskType      = "task_type"
	KeyStoryID       = "story_id"
	KeyTitle         = "title"
	KeyRequirements  = "requirements"
	KeyDependsOn     = "depends_on"
	KeyEstimatedPoints = "estimated_points"
	KeyFilePath      = "file_path"
)

// ApprovalStatus represents the status of an approval request
type ApprovalStatus string

const (
	// ApprovalStatusApproved indicates the request was approved
	ApprovalStatusApproved ApprovalStatus = "APPROVED"
	
	// ApprovalStatusRejected indicates the request was rejected
	ApprovalStatusRejected ApprovalStatus = "REJECTED"
	
	// ApprovalStatusNeedsChanges indicates the request needs changes
	ApprovalStatusNeedsChanges ApprovalStatus = "NEEDS_CHANGES"
	
	// ApprovalStatusPending indicates the request is pending review
	ApprovalStatusPending ApprovalStatus = "PENDING"
)

// ApprovalType represents the type of approval being requested
type ApprovalType string

const (
	// ApprovalTypePlan indicates a plan approval request
	ApprovalTypePlan ApprovalType = "plan"
	
	// ApprovalTypeCode indicates a code approval request  
	ApprovalTypeCode ApprovalType = "code"
)

// ApprovalRequest represents a request for approval (plan or code)
type ApprovalRequest struct {
	ID          string       `json:"id"`
	Type        ApprovalType `json:"type"`        // "plan" or "code"
	Content     string       `json:"content"`     // The plan or code content
	Context     string       `json:"context"`     // Additional context
	Reason      string       `json:"reason"`      // Why approval is needed
	RequestedBy string       `json:"requested_by"` // Agent requesting approval
	RequestedAt time.Time    `json:"requested_at"`
}

// ApprovalResult represents the result of an approval request
type ApprovalResult struct {
	ID          string         `json:"id"`
	RequestID   string         `json:"request_id"`   // References the original request
	Type        ApprovalType   `json:"type"`         // "plan" or "code"
	Status      ApprovalStatus `json:"status"`       // "APPROVED", "REJECTED", "NEEDS_CHANGES"
	Feedback    string         `json:"feedback"`     // Review feedback/comments
	ReviewedBy  string         `json:"reviewed_by"`  // Agent that reviewed
	ReviewedAt  time.Time      `json:"reviewed_at"`
}

type AgentMsg struct {
	ID          string            `json:"id"`
	Type        MsgType           `json:"type"`
	FromAgent   string            `json:"from_agent"`
	ToAgent     string            `json:"to_agent"`
	Timestamp   time.Time         `json:"timestamp"`
	Payload     map[string]any    `json:"payload"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	RetryCount  int               `json:"retry_count,omitempty"`
	ParentMsgID string            `json:"parent_msg_id,omitempty"`
}

func NewAgentMsg(msgType MsgType, fromAgent, toAgent string) *AgentMsg {
	return &AgentMsg{
		ID:        generateID(),
		Type:      msgType,
		FromAgent: fromAgent,
		ToAgent:   toAgent,
		Timestamp: time.Now().UTC(),
		Payload:   make(map[string]any),
		Metadata:  make(map[string]string),
	}
}

func (msg *AgentMsg) ToJSON() ([]byte, error) {
	return json.Marshal(msg)
}

func (msg *AgentMsg) FromJSON(data []byte) error {
	return json.Unmarshal(data, msg)
}

func FromJSON(data []byte) (*AgentMsg, error) {
	var msg AgentMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AgentMsg: %w", err)
	}
	return &msg, nil
}

func (msg *AgentMsg) SetPayload(key string, value any) {
	if msg.Payload == nil {
		msg.Payload = make(map[string]any)
	}
	msg.Payload[key] = value
}

func (msg *AgentMsg) GetPayload(key string) (any, bool) {
	if msg.Payload == nil {
		return nil, false
	}
	val, exists := msg.Payload[key]
	return val, exists
}

func (msg *AgentMsg) SetMetadata(key, value string) {
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]string)
	}
	msg.Metadata[key] = value
}

func (msg *AgentMsg) GetMetadata(key string) (string, bool) {
	if msg.Metadata == nil {
		return "", false
	}
	val, exists := msg.Metadata[key]
	return val, exists
}

func (msg *AgentMsg) Clone() *AgentMsg {
	clone := &AgentMsg{
		ID:          msg.ID,
		Type:        msg.Type,
		FromAgent:   msg.FromAgent,
		ToAgent:     msg.ToAgent,
		Timestamp:   msg.Timestamp,
		RetryCount:  msg.RetryCount,
		ParentMsgID: msg.ParentMsgID,
	}

	// Deep copy payload
	if msg.Payload != nil {
		clone.Payload = make(map[string]any)
		for k, v := range msg.Payload {
			clone.Payload[k] = v
		}
	}

	// Deep copy metadata
	if msg.Metadata != nil {
		clone.Metadata = make(map[string]string)
		for k, v := range msg.Metadata {
			clone.Metadata[k] = v
		}
	}

	return clone
}

func (msg *AgentMsg) Validate() error {
	if msg.ID == "" {
		return fmt.Errorf("message ID is required")
	}
	if msg.Type == "" {
		return fmt.Errorf("message type is required")
	}
	if msg.FromAgent == "" {
		return fmt.Errorf("from_agent is required")
	}
	if msg.ToAgent == "" {
		return fmt.Errorf("to_agent is required")
	}
	if msg.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}

	// Validate message type
	switch msg.Type {
	case MsgTypeTASK, MsgTypeRESULT, MsgTypeERROR, MsgTypeQUESTION, MsgTypeANSWER, MsgTypeREQUEST, MsgTypeSHUTDOWN:
		// Valid types
	default:
		return fmt.Errorf("invalid message type: %s", msg.Type)
	}

	return nil
}

var (
	idCounter int64
	idMutex   sync.Mutex
)

// generateID creates a simple unique ID for messages
// In a real implementation, this might use UUIDs or other schemes
func generateID() string {
	idMutex.Lock()
	defer idMutex.Unlock()

	idCounter++
	return fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), idCounter)
}

// RequestType helper methods

// ParseRequestType parses a string into a RequestType with validation
func ParseRequestType(s string) (RequestType, error) {
	// Normalize to lowercase for comparison
	normalizedType := strings.ToLower(s)
	
	switch normalizedType {
	case "approval":
		return RequestApproval, nil
	case "approval_request":
		return RequestApprovalReview, nil
	case "question":
		return RequestQuestion, nil
	default:
		// Check if it's already in the correct format
		switch RequestType(s) {
		case RequestApproval, RequestApprovalReview, RequestQuestion:
			return RequestType(s), nil
		default:
			return "", fmt.Errorf("unknown request type: %s", s)
		}
	}
}

// String returns the string representation of RequestType
func (rt RequestType) String() string {
	return string(rt)
}

// NormaliseApprovalType normalizes and validates approval type strings
func NormaliseApprovalType(s string) (ApprovalType, error) {
	// Normalize to lowercase for comparison
	normalizedType := strings.ToLower(s)
	
	switch normalizedType {
	case "plan":
		return ApprovalTypePlan, nil
	case "code":
		return ApprovalTypeCode, nil
	default:
		return "", fmt.Errorf("unknown approval type: %s", s)
	}
}

// Approval helper methods

// IsApproved returns true if the status indicates approval
func (r *ApprovalResult) IsApproved() bool {
	return r.Status == ApprovalStatusApproved
}

// IsRejected returns true if the status indicates rejection or needs changes
func (r *ApprovalResult) IsRejected() bool {
	return r.Status == ApprovalStatusRejected || r.Status == ApprovalStatusNeedsChanges
}

// IsPending returns true if the status indicates pending review
func (r *ApprovalResult) IsPending() bool {
	return r.Status == ApprovalStatusPending
}

// String returns the string representation of ApprovalStatus
func (s ApprovalStatus) String() string {
	return string(s)
}

// String returns the string representation of ApprovalType
func (t ApprovalType) String() string {
	return string(t)
}

// ValidateApprovalStatus validates if a string is a valid approval status
func ValidateApprovalStatus(status string) (ApprovalStatus, bool) {
	switch ApprovalStatus(status) {
	case ApprovalStatusApproved, ApprovalStatusRejected, ApprovalStatusNeedsChanges, ApprovalStatusPending:
		return ApprovalStatus(status), true
	default:
		return "", false
	}
}

// ValidateApprovalType validates if a string is a valid approval type
func ValidateApprovalType(approvalType string) (ApprovalType, bool) {
	switch ApprovalType(approvalType) {
	case ApprovalTypePlan, ApprovalTypeCode:
		return ApprovalType(approvalType), true
	default:
		return "", false
	}
}

// ConvertLegacyStatus converts legacy status strings to new constants
func ConvertLegacyStatus(legacyStatus string) ApprovalStatus {
	// Normalize to lowercase for comparison
	normalizedStatus := strings.ToLower(legacyStatus)
	
	switch normalizedStatus {
	case "approved":
		return ApprovalStatusApproved
	case "rejected":
		return ApprovalStatusRejected
	case "needs_fixes", "needs_changes":
		return ApprovalStatusNeedsChanges
	case "pending":
		return ApprovalStatusPending
	default:
		// Check if it's already in the correct format
		if status, valid := ValidateApprovalStatus(legacyStatus); valid {
			return status
		}
		// Default to rejected for unknown statuses
		return ApprovalStatusRejected
	}
}
