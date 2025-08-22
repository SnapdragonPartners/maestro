package persistence

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Spec represents a specification document.
type Spec struct {
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
	ID          string     `json:"id"`
	Content     string     `json:"content"`
}

// Story represents a development story generated from a spec.
//
//nolint:govet // struct alignment optimization not critical for this type
type Story struct {
	// Core persistent fields
	ID           string `json:"id"`
	SpecID       string `json:"spec_id"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	Status       string `json:"status"`
	Priority     int    `json:"priority"`
	ApprovedPlan string `json:"approved_plan,omitempty"`
	StoryType    string `json:"story_type"` // "devops" or "app"

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	LastUpdated time.Time  `json:"last_updated"`

	// Assignment and execution
	AssignedAgent string `json:"assigned_agent,omitempty"`

	// Metrics and costs
	TokensUsed int64   `json:"tokens_used"`
	CostUSD    float64 `json:"cost_usd"`

	// Completion tracking
	PRID              string `json:"pr_id,omitempty"`              // Pull request ID
	CommitHash        string `json:"commit_hash,omitempty"`        // Commit hash from merge
	CompletionSummary string `json:"completion_summary,omitempty"` // Summary of what was completed

	// Extensibility
	Metadata string `json:"metadata,omitempty"` // JSON blob for extensibility

	// Queue-specific fields (not persisted to database)
	DependsOn       []string `json:"depends_on" db:"-"`       // Story dependencies
	EstimatedPoints int      `json:"estimated_points" db:"-"` // Estimation points
}

// StoryDependency represents a dependency relationship between stories.
type StoryDependency struct {
	StoryID   string `json:"story_id"`
	DependsOn string `json:"depends_on"`
}

// Story status constants (mirrored from canonical in architect for database operations).
const (
	StatusNew      = "new"
	StatusPending  = "pending"
	StatusAssigned = "assigned"
	StatusPlanning = "planning"
	StatusCoding   = "coding"
	StatusDone     = "done"
)

// GenerateSpecID generates a new UUID for a spec.
func GenerateSpecID() string {
	return uuid.New().String()
}

// GenerateStoryID generates an 8-character hex ID for a story (like git commit hashes).
// This function includes collision detection and will retry if needed.
func GenerateStoryID() (string, error) {
	bytes := make([]byte, 4) // 4 bytes = 8 hex characters
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return fmt.Sprintf("%x", bytes), nil
}

// StoryFilter represents criteria for querying stories.
type StoryFilter struct {
	Status        *string  `json:"status,omitempty"`
	AssignedAgent *string  `json:"assigned_agent,omitempty"`
	SpecID        *string  `json:"spec_id,omitempty"`
	Statuses      []string `json:"statuses,omitempty"` // For IN queries
}

// SpecSummary represents aggregated metrics for a spec.
type SpecSummary struct {
	LastCompleted    *time.Time `json:"last_completed,omitempty"`
	SpecID           string     `json:"spec_id"`
	TotalTokens      int64      `json:"total_tokens"`
	TotalCost        float64    `json:"total_cost"`
	TotalStories     int        `json:"total_stories"`
	CompletedStories int        `json:"completed_stories"`
}

// AgentRequest represents a request (question or approval) from one agent to another.
//
//nolint:govet // struct alignment optimization not critical for this type
type AgentRequest struct {
	CreatedAt     time.Time `json:"created_at"`
	ID            string    `json:"id"`
	RequestType   string    `json:"request_type"` // "question" or "approval"
	FromAgent     string    `json:"from_agent"`
	ToAgent       string    `json:"to_agent"`
	Content       string    `json:"content"`
	StoryID       *string   `json:"story_id,omitempty"`
	ApprovalType  *string   `json:"approval_type,omitempty"` // "plan", "code", "budget_review", "completion"
	Context       *string   `json:"context,omitempty"`
	Reason        *string   `json:"reason,omitempty"`
	CorrelationID *string   `json:"correlation_id,omitempty"`
	ParentMsgID   *string   `json:"parent_msg_id,omitempty"`
}

// AgentResponse represents a response (answer or result) to an agent request.
//
//nolint:govet // struct alignment optimization not critical for this type
type AgentResponse struct {
	CreatedAt     time.Time `json:"created_at"`
	ID            string    `json:"id"`
	ResponseType  string    `json:"response_type"` // "answer" or "result"
	FromAgent     string    `json:"from_agent"`
	ToAgent       string    `json:"to_agent"`
	Content       string    `json:"content"`
	RequestID     *string   `json:"request_id,omitempty"`
	StoryID       *string   `json:"story_id,omitempty"`
	Status        *string   `json:"status,omitempty"` // "APPROVED", "REJECTED", "NEEDS_CHANGES", "PENDING"
	Feedback      *string   `json:"feedback,omitempty"`
	CorrelationID *string   `json:"correlation_id,omitempty"`
}

// AgentPlan represents a plan submitted by an agent for a story.
//
//nolint:govet // struct alignment optimization not critical for this type
type AgentPlan struct {
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	ID         string     `json:"id"`
	StoryID    string     `json:"story_id"`
	FromAgent  string     `json:"from_agent"`
	Content    string     `json:"content"`
	Status     string     `json:"status"`               // "submitted", "approved", "rejected", "needs_changes"
	Confidence *string    `json:"confidence,omitempty"` // "high", "medium", "low"
	ReviewedBy *string    `json:"reviewed_by,omitempty"`
	Feedback   *string    `json:"feedback,omitempty"`
}

// Request type constants.
const (
	RequestTypeQuestion = "question"
	RequestTypeApproval = "approval"
)

// Response type constants.
const (
	ResponseTypeAnswer = "answer"
	ResponseTypeResult = "result"
)

// Plan status constants.
const (
	PlanStatusSubmitted    = "submitted"
	PlanStatusApproved     = "approved"
	PlanStatusRejected     = "rejected"
	PlanStatusNeedsChanges = "needs_changes"
)

// GenerateAgentRequestID generates a new UUID for an agent request.
func GenerateAgentRequestID() string {
	return uuid.New().String()
}

// GenerateAgentResponseID generates a new UUID for an agent response.
func GenerateAgentResponseID() string {
	return uuid.New().String()
}

// GenerateAgentPlanID generates a new UUID for an agent plan.
func GenerateAgentPlanID() string {
	return uuid.New().String()
}
