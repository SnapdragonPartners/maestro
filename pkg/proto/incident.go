package proto

// IncidentKind identifies the type of operational incident.
type IncidentKind string

// Incident kind constants.
const (
	IncidentKindStoryBlocked  IncidentKind = "story_blocked"
	IncidentKindClarification IncidentKind = "clarification_needed"
	IncidentKindSystemIdle    IncidentKind = "system_idle"
)

// IncidentAction represents a recovery action a user can take on an incident.
// In Phase 1 these are advisory metadata only (no incident_action tool yet).
type IncidentAction string

// Incident action constants.
const (
	IncidentActionTryAgain      IncidentAction = "try_again"
	IncidentActionChangeRequest IncidentAction = "change_request"
	IncidentActionSkip          IncidentAction = "skip"
	IncidentActionResume        IncidentAction = "resume"
)

// Incident represents an architect-owned operational blocker.
// Incidents are opened by the architect and closed by architect-side recovery.
// User replies do NOT automatically resolve incidents — only asks.
type Incident struct {
	ID               string           `json:"id"`
	Kind             IncidentKind     `json:"kind"`
	Scope            string           `json:"scope"` // "story" | "system"
	StoryID          string           `json:"story_id,omitempty"`
	SpecID           string           `json:"spec_id,omitempty"`
	FailureID        string           `json:"failure_id,omitempty"`
	Title            string           `json:"title"`
	Summary          string           `json:"summary"`
	Details          string           `json:"details,omitempty"`
	AffectedStoryIDs []string         `json:"affected_story_ids,omitempty"`
	AllowedActions   []IncidentAction `json:"allowed_actions"`
	Blocking         bool             `json:"blocking"`
	OpenedAt         string           `json:"opened_at"`
	ResolvedAt       string           `json:"resolved_at,omitempty"`
	Resolution       string           `json:"resolution,omitempty"`
}

// UserAsk represents a PM-owned conversational obligation.
// Asks are created when PM uses chat_ask_user and resolved when the user replies.
// At most one ask is active at a time.
type UserAsk struct {
	ID                string `json:"id"`
	Prompt            string `json:"prompt"`
	Kind              string `json:"kind"` // "interview_question" | "clarification" | "decision_required"
	RelatedIncidentID string `json:"related_incident_id,omitempty"`
	OpenedAt          string `json:"opened_at"`
	ResolvedAt        string `json:"resolved_at,omitempty"`
}
