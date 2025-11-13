// Package proto defines the structured message protocol for agent communication.
// It provides message types, states, and data structures used throughout the multi-agent system.
package proto

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/logx"
)

// MsgType represents the type of agent message.
type MsgType string

const (
	// MsgTypeSTORY represents a story assignment message.
	MsgTypeSTORY MsgType = "STORY"
	// MsgTypeSPEC represents a specification message.
	MsgTypeSPEC MsgType = "SPEC"
	// MsgTypeERROR represents an error message.
	MsgTypeERROR MsgType = "ERROR"
	// MsgTypeSHUTDOWN represents a shutdown message.
	MsgTypeSHUTDOWN MsgType = "SHUTDOWN"
	// MsgTypeREQUEST represents a request message (unified protocol).
	MsgTypeREQUEST MsgType = "REQUEST"
	// MsgTypeRESPONSE represents a response message (unified protocol).
	MsgTypeRESPONSE MsgType = "RESPONSE"
)

// Priority represents the priority level for messages.
type Priority string

const (
	// PriorityLow represents low priority messages.
	PriorityLow Priority = "LOW"

	// PriorityMedium represents medium priority messages.
	PriorityMedium Priority = "MEDIUM"

	// PriorityHigh represents high priority messages.
	PriorityHigh Priority = "HIGH"
)

// Confidence represents the confidence level for plans and completions.
type Confidence string

const (
	// ConfidenceLow represents low confidence level.
	ConfidenceLow Confidence = "LOW"

	// ConfidenceMedium represents medium confidence level.
	ConfidenceMedium Confidence = "MEDIUM"

	// ConfidenceHigh represents high confidence level.
	ConfidenceHigh Confidence = "HIGH"
)

// StoryType represents the type of story (DevOps or App).
type StoryType string

const (
	// StoryTypeDevOps represents DevOps infrastructure stories.
	StoryTypeDevOps StoryType = "devops"

	// StoryTypeApp represents application development stories.
	StoryTypeApp StoryType = "app"
)

// Common payload and metadata keys used in agent messages.
const (
	// Core payload keys.
	KeyContent      = "content"
	KeyStatus       = "status"
	KeyCurrentState = "current_state"

	// Unified protocol keys (NEW - preferred).
	KeyKind          = "kind"           // Request/response kind (QUESTION, APPROVAL, etc.)
	KeyCorrelationID = "correlation_id" // Universal correlation ID for request/response pairs
	KeyRequestID     = "request_id"     // References the original request in responses

	// Request-specific keys.
	KeyQuestion = "question" // Question request payload
	KeyApproval = "approval" // Approval request payload
	KeyMerge    = "merge"    // Merge request payload
	KeyRequeue  = "requeue"  // Requeue request payload

	// Response-specific keys.
	KeyAnswer       = "answer"        // Question response payload
	KeyDecision     = "decision"      // Approval decision
	KeyFeedback     = "feedback"      // General feedback/comments
	KeySuccess      = "success"       // Whether operation was successful
	KeyErrorMessage = "error_message" // Error details for failed operations

	// Story-related keys.
	KeyStoryType       = "story_type"
	KeyStoryID         = "story_id"
	KeyTitle           = "title"
	KeyRequirements    = "requirements"
	KeyDependsOn       = "depends_on"
	KeyEstimatedPoints = "estimated_points"
	KeyFilePath        = "file_path"
	KeyBackend         = "backend"

	// Resource request keys.
	KeyRequestedTokens     = "requestedTokens"
	KeyRequestedIterations = "requestedIterations"
	KeyJustification       = "justification"
)

// ApprovalStatus represents the status of an approval request.
type ApprovalStatus string

const (
	// ApprovalStatusApproved indicates the request was approved.
	ApprovalStatusApproved ApprovalStatus = "APPROVED"

	// ApprovalStatusRejected indicates the request was rejected.
	ApprovalStatusRejected ApprovalStatus = "REJECTED"

	// ApprovalStatusNeedsChanges indicates the request needs changes.
	ApprovalStatusNeedsChanges ApprovalStatus = "NEEDS_CHANGES"

	// ApprovalStatusPending indicates the request is pending review.
	ApprovalStatusPending ApprovalStatus = "PENDING"
)

// ApprovalType represents the type of approval being requested.
type ApprovalType string

const (
	// ApprovalTypePlan indicates a plan approval request.
	ApprovalTypePlan ApprovalType = "plan"

	// ApprovalTypeCode indicates a code approval request.
	ApprovalTypeCode ApprovalType = "code"

	// ApprovalTypeBudgetReview indicates a budget review approval request.
	ApprovalTypeBudgetReview ApprovalType = "budget_review"

	// ApprovalTypeCompletion indicates a story completion request.
	ApprovalTypeCompletion ApprovalType = "completion"

	// ApprovalTypeSpec indicates a specification approval request.
	ApprovalTypeSpec ApprovalType = "spec"
)

// ApprovalRequest represents a request for approval (plan or code).
type ApprovalRequest struct {
	ID          string       `json:"id"`
	Type        ApprovalType `json:"type"`         // "plan" or "code"
	Content     string       `json:"content"`      // The plan or code content
	Context     string       `json:"context"`      // Additional context
	Reason      string       `json:"reason"`       // Why approval is needed
	RequestedBy string       `json:"requested_by"` // Agent requesting approval
	RequestedAt time.Time    `json:"requested_at"`
}

// ApprovalResult represents the result of an approval request.
type ApprovalResult struct {
	ID         string         `json:"id"`
	RequestID  string         `json:"request_id"`  // References the original request
	Type       ApprovalType   `json:"type"`        // "plan" or "code"
	Status     ApprovalStatus `json:"status"`      // "APPROVED", "REJECTED", "NEEDS_CHANGES"
	Feedback   string         `json:"feedback"`    // Review feedback/comments
	ReviewedBy string         `json:"reviewed_by"` // Agent that reviewed
	ReviewedAt time.Time      `json:"reviewed_at"`
}

// ResourceRequest represents a request for additional resources (tokens, iterations, etc.).
type ResourceRequest struct {
	ID                  string    `json:"id"`
	RequestedTokens     int       `json:"requestedTokens"`
	RequestedIterations int       `json:"requestedIterations"`
	Justification       string    `json:"justification"`
	RequestedBy         string    `json:"requested_by"`
	RequestedAt         time.Time `json:"requested_at"`
	StoryID             string    `json:"story_id,omitempty"`
}

// ResourceResult represents the result of a resource request.
type ResourceResult struct {
	ID                 string         `json:"id"`
	RequestID          string         `json:"request_id"` // References the original request
	Status             ApprovalStatus `json:"status"`     // "APPROVED", "REJECTED", "NEEDS_CHANGES"
	ApprovedTokens     int            `json:"approved_tokens,omitempty"`
	ApprovedIterations int            `json:"approved_iterations,omitempty"`
	Feedback           string         `json:"feedback"`    // Review feedback/comments
	ReviewedBy         string         `json:"reviewed_by"` // Agent that reviewed
	ReviewedAt         time.Time      `json:"reviewed_at"`
}

// AgentMsg represents a message passed between agents in the system.
type AgentMsg struct {
	ID          string            `json:"id"`
	Type        MsgType           `json:"type"`
	FromAgent   string            `json:"from_agent"`
	ToAgent     string            `json:"to_agent"`
	Timestamp   time.Time         `json:"timestamp"`
	Payload     *MessagePayload   `json:"payload"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	RetryCount  int               `json:"retry_count,omitempty"`
	ParentMsgID string            `json:"parent_msg_id,omitempty"`
}

// NewAgentMsg creates a new agent message with the specified parameters.
func NewAgentMsg(msgType MsgType, fromAgent, toAgent string) *AgentMsg {
	return &AgentMsg{
		ID:        generateID(),
		Type:      msgType,
		FromAgent: fromAgent,
		ToAgent:   toAgent,
		Timestamp: time.Now().UTC(),
		Payload:   nil, // Payload must be set explicitly with SetTypedPayload
		Metadata:  make(map[string]string),
	}
}

// ToJSON serializes the agent message to JSON bytes.
func (msg *AgentMsg) ToJSON() ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, logx.Wrap(err, "failed to marshal AgentMsg to JSON")
	}
	return data, nil
}

// FromJSON deserializes JSON bytes into the agent message.
func (msg *AgentMsg) FromJSON(data []byte) error {
	if err := json.Unmarshal(data, msg); err != nil {
		return logx.Wrap(err, "failed to unmarshal JSON to AgentMsg")
	}
	return nil
}

// FromJSON creates a new AgentMsg from JSON bytes.
func FromJSON(data []byte) (*AgentMsg, error) {
	var msg AgentMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AgentMsg: %w", err)
	}
	return &msg, nil
}

// SetTypedPayload sets the typed payload for the message.
func (msg *AgentMsg) SetTypedPayload(payload *MessagePayload) {
	msg.Payload = payload
}

// GetTypedPayload retrieves the typed payload from the message.
func (msg *AgentMsg) GetTypedPayload() *MessagePayload {
	return msg.Payload
}

// SetMetadata sets a metadata value for the message.
func (msg *AgentMsg) SetMetadata(key, value string) {
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]string)
	}
	msg.Metadata[key] = value
}

// GetMetadata retrieves a metadata value from the message.
func (msg *AgentMsg) GetMetadata(key string) (string, bool) {
	if msg.Metadata == nil {
		return "", false
	}
	val, exists := msg.Metadata[key]
	return val, exists
}

// Clone creates a deep copy of the agent message.
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

	// Shallow copy payload pointer (MessagePayload contains json.RawMessage which is immutable).
	clone.Payload = msg.Payload

	// Deep copy metadata.
	if msg.Metadata != nil {
		clone.Metadata = make(map[string]string)
		for k, v := range msg.Metadata {
			clone.Metadata[k] = v
		}
	}

	return clone
}

// Validate checks if the agent message has valid required fields.
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

	// Validate message type using the validation function.
	if _, valid := ValidateMsgType(string(msg.Type)); !valid {
		return fmt.Errorf("invalid message type: %s", msg.Type)
	}

	return nil
}

// IDGenerator provides thread-safe ID generation for messages.
type IDGenerator struct {
	counter int64
	mutex   sync.Mutex
}

// NextID generates the next unique ID.
func (g *IDGenerator) NextID() string {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.counter++
	return fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), g.counter)
}

// NextCounter generates the next unique counter value.
func (g *IDGenerator) NextCounter() int64 {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.counter++
	return g.counter
}

// Global ID generator instance.
var globalIDGen = &IDGenerator{} //nolint:gochecknoglobals // Single global ID generator instance

// generateID creates a simple unique ID for messages.
// In a real implementation, this might use UUIDs or other schemes.
func generateID() string {
	return globalIDGen.NextID()
}

// MsgType helper methods.

// ValidateMsgType validates if a string is a valid message type.
func ValidateMsgType(msgType string) (MsgType, bool) {
	switch MsgType(msgType) {
	case MsgTypeSTORY, MsgTypeSPEC, MsgTypeREQUEST, MsgTypeRESPONSE, MsgTypeERROR, MsgTypeSHUTDOWN:
		return MsgType(msgType), true
	default:
		return "", false
	}
}

// ParseMsgType parses a string into a MsgType with validation.
func ParseMsgType(s string) (MsgType, error) {
	// Normalize to uppercase for comparison.
	normalizedType := strings.ToUpper(s)

	switch normalizedType {
	case "STORY":
		return MsgTypeSTORY, nil
	case "SPEC":
		return MsgTypeSPEC, nil
	case "REQUEST":
		return MsgTypeREQUEST, nil
	case "RESPONSE":
		return MsgTypeRESPONSE, nil
	case "ERROR":
		return MsgTypeERROR, nil
	case "SHUTDOWN":
		return MsgTypeSHUTDOWN, nil
	default:
		// Check if it's already in the correct format.
		if msgType, valid := ValidateMsgType(s); valid {
			return msgType, nil
		}
		return "", fmt.Errorf("unknown message type: %s", s)
	}
}

// String returns the string representation of MsgType.
func (mt MsgType) String() string {
	return string(mt)
}

// Approval helper methods.

// IsApproved returns true if the status indicates approval.
func (r *ApprovalResult) IsApproved() bool {
	return r.Status == ApprovalStatusApproved
}

// IsRejected returns true if the status indicates rejection or needs changes.
func (r *ApprovalResult) IsRejected() bool {
	return r.Status == ApprovalStatusRejected || r.Status == ApprovalStatusNeedsChanges
}

// IsPending returns true if the status indicates pending review.
func (r *ApprovalResult) IsPending() bool {
	return r.Status == ApprovalStatusPending
}

// String returns the string representation of ApprovalStatus.
func (s ApprovalStatus) String() string {
	return string(s)
}

// String returns the string representation of ApprovalType.
func (t ApprovalType) String() string {
	return string(t)
}

// ValidateApprovalStatus validates if a string is a valid approval status.
func ValidateApprovalStatus(status string) (ApprovalStatus, bool) {
	switch ApprovalStatus(status) {
	case ApprovalStatusApproved, ApprovalStatusRejected, ApprovalStatusNeedsChanges, ApprovalStatusPending:
		return ApprovalStatus(status), true
	default:
		return "", false
	}
}

// ValidateApprovalType validates if a string is a valid approval type.
func ValidateApprovalType(approvalType string) (ApprovalType, bool) {
	switch ApprovalType(approvalType) {
	case ApprovalTypePlan, ApprovalTypeCode, ApprovalTypeBudgetReview, ApprovalTypeCompletion:
		return ApprovalType(approvalType), true
	default:
		return "", false
	}
}

// ConvertLegacyStatus converts legacy status strings to new constants.
func ConvertLegacyStatus(legacyStatus string) ApprovalStatus {
	// Normalize to lowercase for comparison.
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
		// Check if it's already in the correct format.
		if status, valid := ValidateApprovalStatus(legacyStatus); valid {
			return status
		}
		// Default to rejected for unknown statuses.
		return ApprovalStatusRejected
	}
}

// AutoAction represents BUDGET_REVIEW command types for inter-agent communication.
type AutoAction string

const (
	// AutoContinue indicates to continue with the current approach.
	AutoContinue AutoAction = "CONTINUE"
	// AutoPivot indicates to change approach or strategy.
	AutoPivot AutoAction = "PIVOT"
	// AutoEscalate indicates to escalate to higher authority.
	AutoEscalate AutoAction = "ESCALATE"
	// AutoAbandon indicates to abandon the current task.
	AutoAbandon AutoAction = "ABANDON"
)

// Question reason constants.
const (
	QuestionReasonBudgetReview = "BUDGET_REVIEW"
)

// ParseAutoAction validates and converts a string to AutoAction.
func ParseAutoAction(s string) (AutoAction, error) {
	switch AutoAction(s) {
	case AutoContinue, AutoPivot, AutoEscalate, AutoAbandon:
		return AutoAction(s), nil
	default:
		return "", fmt.Errorf("invalid BUDGET_REVIEW command: %q. Valid: CONTINUE, PIVOT, ESCALATE, ABANDON", s)
	}
}

// String returns the string representation of AutoAction.
func (a AutoAction) String() string {
	return string(a)
}

// Correlation ID helpers for QUESTION/ANSWER and REQUEST/RESULT pairs.

// GenerateQuestionID creates a unique ID for a question.
func GenerateQuestionID() string {
	return fmt.Sprintf("q_%d_%d", time.Now().UnixNano(), generateUniqueCounter())
}

// GenerateApprovalID creates a unique ID for an approval request.
func GenerateApprovalID() string {
	return fmt.Sprintf("a_%d_%d", time.Now().UnixNano(), generateUniqueCounter())
}

// GenerateCorrelationID creates a unique ID for any request/response pair.
func GenerateCorrelationID() string {
	return fmt.Sprintf("c_%d_%d", time.Now().UnixNano(), generateUniqueCounter())
}

// Helper for generating unique counters (reuses existing ID generation logic).
func generateUniqueCounter() int64 {
	return globalIDGen.NextCounter()
}

// Note: Correlation IDs are now part of the typed payload structures themselves,
// not separate payload fields. See QuestionRequestPayload, ApprovalRequestPayload, etc.

// Centralized Enum Parsing Utilities.
// These functions provide safe string-to-enum conversion with validation.

// ParseApprovalStatus parses a string into an ApprovalStatus with validation.
func ParseApprovalStatus(s string) (ApprovalStatus, error) {
	// Normalize to uppercase for comparison.
	normalizedStatus := strings.ToUpper(s)

	switch normalizedStatus {
	case "APPROVED":
		return ApprovalStatusApproved, nil
	case "REJECTED":
		return ApprovalStatusRejected, nil
	case "NEEDS_CHANGES", "NEEDS_FIXES":
		return ApprovalStatusNeedsChanges, nil
	case "PENDING":
		return ApprovalStatusPending, nil
	default:
		// Check if it's already in the correct format.
		if status, valid := ValidateApprovalStatus(s); valid {
			return status, nil
		}
		return "", fmt.Errorf("unknown approval status: %s", s)
	}
}

// ParseApprovalType parses a string into an ApprovalType with validation.
func ParseApprovalType(s string) (ApprovalType, error) {
	// Normalize to lowercase for comparison.
	normalizedType := strings.ToLower(s)

	switch normalizedType {
	case "plan":
		return ApprovalTypePlan, nil
	case "code":
		return ApprovalTypeCode, nil
	case "budget_review":
		return ApprovalTypeBudgetReview, nil
	case "completion":
		return ApprovalTypeCompletion, nil
	default:
		// Check if it's already in the correct format.
		if approvalType, valid := ValidateApprovalType(s); valid {
			return approvalType, nil
		}
		return "", fmt.Errorf("unknown approval type: %s", s)
	}
}

// ValidStoryTypes returns all valid story types.
func ValidStoryTypes() []string {
	return []string{
		string(StoryTypeDevOps),
		string(StoryTypeApp),
	}
}

// IsValidStoryType checks if a story type string is valid.
func IsValidStoryType(storyType string) bool {
	for _, validType := range ValidStoryTypes() {
		if storyType == validType {
			return true
		}
	}
	return false
}

// EnumExtractor provides a generic way to safely extract and validate enum values.
type EnumExtractor[T any] func(string) (T, error)

// Note: SafeExtractFromPayload is obsolete with typed payloads.
// Enum values are now accessed directly from typed payload structures.

// SafeExtractFromMetadata extracts and validates an enum value from a message metadata.
func SafeExtractFromMetadata[T any](msg *AgentMsg, key string, parser EnumExtractor[T]) (T, error) {
	var zero T

	if strValue, exists := msg.GetMetadata(key); exists {
		return parser(strValue)
	}
	return zero, fmt.Errorf("metadata key %s not found", key)
}

// State represents a state in a state machine.
type State string

const (
	// StateDone indicates a completed state.
	StateDone State = "DONE"
	// StateError indicates an error state.
	StateError State = "ERROR"
	// StateWaiting indicates a waiting state.
	StateWaiting State = "WAITING"
)

// String returns the string representation of State.
func (s State) String() string {
	return string(s)
}

// StateChangeNotification represents an agent state change event.
type StateChangeNotification struct {
	AgentID   string         `json:"agent_id"`
	FromState State          `json:"from_state"`
	ToState   State          `json:"to_state"`
	Timestamp time.Time      `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// StoryStatusUpdate represents a simple story status change notification.
type StoryStatusUpdate struct {
	StoryID   string    `json:"story_id"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	AgentID   string    `json:"agent_id"`
}

// StoryRequeueRequest represents a request to requeue a story from a failed agent.
type StoryRequeueRequest struct {
	StoryID   string    `json:"story_id"`
	AgentID   string    `json:"agent_id"`
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}
