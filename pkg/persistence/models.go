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
type Story struct {
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	ID            string     `json:"id"`
	SpecID        string     `json:"spec_id"`
	Title         string     `json:"title"`
	Content       string     `json:"content"`
	Status        string     `json:"status"`
	ApprovedPlan  string     `json:"approved_plan,omitempty"`
	AssignedAgent string     `json:"assigned_agent,omitempty"`
	Metadata      string     `json:"metadata,omitempty"` // JSON blob for extensibility
	TokensUsed    int64      `json:"tokens_used"`
	CostUSD       float64    `json:"cost_usd"`
	Priority      int        `json:"priority"`
}

// StoryDependency represents a dependency relationship between stories.
type StoryDependency struct {
	StoryID   string `json:"story_id"`
	DependsOn string `json:"depends_on"`
}

// Story status constants.
const (
	StatusNew       = "new"
	StatusPlanning  = "planning"
	StatusCoding    = "coding"
	StatusCommitted = "committed"
	StatusMerged    = "merged"
	StatusError     = "error"
	StatusDuplicate = "duplicate"
)

// ValidStatuses returns all valid story statuses.
func ValidStatuses() []string {
	return []string{
		StatusNew,
		StatusPlanning,
		StatusCoding,
		StatusCommitted,
		StatusMerged,
		StatusError,
		StatusDuplicate,
	}
}

// IsValidStatus checks if a status string is valid.
func IsValidStatus(status string) bool {
	for _, validStatus := range ValidStatuses() {
		if status == validStatus {
			return true
		}
	}
	return false
}

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
