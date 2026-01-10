// Package persistence provides SQLite-based storage for specs and stories.
package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrSessionNotFound is returned when a requested session does not exist.
var ErrSessionNotFound = errors.New("session not found")

// Session represents a Maestro execution session.
type Session struct {
	SessionID  string     `json:"session_id"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	Status     string     `json:"status"`      // active, shutdown, completed, crashed
	ConfigJSON string     `json:"config_json"` // Snapshot of config at session start
}

// Session status constants.
const (
	SessionStatusActive    = "active"
	SessionStatusShutdown  = "shutdown"  // Graceful shutdown, resumable
	SessionStatusCompleted = "completed" // All work done, not resumable
	SessionStatusCrashed   = "crashed"   // Unexpected termination, not resumable
)

// AgentContext represents a persisted LLM conversation context.
//
//nolint:govet // struct alignment optimization not critical for this type.
type AgentContext struct {
	SessionID    string    `json:"session_id"`
	AgentID      string    `json:"agent_id"`
	ContextType  string    `json:"context_type"` // 'main' or target agent ID
	MessagesJSON string    `json:"messages_json"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CoderState represents persisted coder state for resume.
//
//nolint:govet // struct alignment optimization not critical for this type.
type CoderState struct {
	SessionID          string    `json:"session_id"`
	AgentID            string    `json:"agent_id"`
	StoryID            *string   `json:"story_id,omitempty"`
	State              string    `json:"state"`
	PlanJSON           *string   `json:"plan_json,omitempty"`
	TodoListJSON       *string   `json:"todo_list_json,omitempty"`
	CurrentTodoIndex   int       `json:"current_todo_index"`
	KnowledgePackJSON  *string   `json:"knowledge_pack_json,omitempty"`
	PendingRequestType *string   `json:"pending_request_type,omitempty"` // QUESTION or REQUEST
	PendingRequestJSON *string   `json:"pending_request_json,omitempty"`
	ContainerImage     *string   `json:"container_image,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ArchitectState represents persisted architect state for resume.
//
//nolint:govet // struct alignment optimization not critical for this type.
type ArchitectState struct {
	SessionID            string    `json:"session_id"`
	State                string    `json:"state"`
	EscalationCountsJSON *string   `json:"escalation_counts_json,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// PMState represents persisted PM state for resume.
//
//nolint:govet // struct alignment optimization not critical for this type.
type PMState struct {
	SessionID           string    `json:"session_id"`
	State               string    `json:"state"`
	SpecContent         *string   `json:"spec_content,omitempty"`
	BootstrapParamsJSON *string   `json:"bootstrap_params_json,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// CreateSession creates a new session record in the database.
func CreateSession(db *sql.DB, sessionID, configJSON string) error {
	_, err := db.Exec(`
		INSERT INTO sessions (session_id, status, config_json)
		VALUES (?, ?, ?)
	`, sessionID, SessionStatusActive, configJSON)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// UpdateSessionStatus updates the status and ended_at timestamp of a session.
func UpdateSessionStatus(db *sql.DB, sessionID, status string) error {
	var result sql.Result
	var err error
	if status == SessionStatusShutdown || status == SessionStatusCompleted || status == SessionStatusCrashed {
		result, err = db.Exec(`
			UPDATE sessions
			SET status = ?, ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE session_id = ?
		`, status, sessionID)
	} else {
		result, err = db.Exec(`
			UPDATE sessions SET status = ? WHERE session_id = ?
		`, status, sessionID)
	}
	if err != nil {
		return fmt.Errorf("failed to update session status: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// scanSession scans a session row into a Session struct.
func scanSession(row *sql.Row) (*Session, error) {
	var session Session
	var endedAt sql.NullString
	err := row.Scan(&session.SessionID, &session.StartedAt, &endedAt, &session.Status, &session.ConfigJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan session: %w", err)
	}

	if endedAt.Valid {
		t, parseErr := time.Parse(time.RFC3339Nano, endedAt.String)
		if parseErr == nil {
			session.EndedAt = &t
		}
	}

	return &session, nil
}

// ResumableSessionInfo contains a resumable session along with story statistics.
type ResumableSessionInfo struct {
	Session           *Session
	IncompleteStories int // Count of stories not in 'done' or 'failed' status
	DoneStories       int // Count of completed stories
}

// GetResumableSession returns the most recent session with status='shutdown'.
// Returns ErrSessionNotFound if no resumable session exists.
// Deprecated: Use GetMostRecentResumableSession instead which also supports crashed sessions.
func GetResumableSession(db *sql.DB) (*Session, error) {
	row := db.QueryRow(`
		SELECT session_id, started_at, ended_at, status, config_json
		FROM sessions
		WHERE status = ?
		ORDER BY ended_at DESC
		LIMIT 1
	`, SessionStatusShutdown)

	session, err := scanSession(row)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to get resumable session: %w", err)
	}
	return session, nil
}

// GetMostRecentResumableSession returns the most recent session that can be resumed.
// A session is resumable if:
// 1. Its status is 'shutdown', 'crashed', or 'active' (not 'completed')
//   - 'active' sessions are treated as crashed (the process died unexpectedly)
//
// 2. It has at least one incomplete story (status not in 'done', 'failed')
//
// If an 'active' session is found, its status is updated to 'crashed' before returning.
// Returns nil, nil if no resumable session exists (this is not an error condition).
//
//nolint:nilnil // Returning nil,nil is intentional - no resumable session is a valid (non-error) outcome
func GetMostRecentResumableSession(db *sql.DB) (*ResumableSessionInfo, error) {
	// Get the most recent session that could be resumed (shutdown, crashed, or active)
	// Active sessions indicate a crash (process died without graceful shutdown)
	// Order by COALESCE to handle NULL ended_at (active sessions)
	row := db.QueryRow(`
		SELECT session_id, started_at, ended_at, status, config_json
		FROM sessions
		WHERE status IN (?, ?, ?)
		ORDER BY COALESCE(ended_at, started_at) DESC
		LIMIT 1
	`, SessionStatusShutdown, SessionStatusCrashed, SessionStatusActive)

	session, err := scanSession(row)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get most recent session: %w", err)
	}

	// If session was active, it's a crash - update status to crashed
	if session.Status == SessionStatusActive {
		if updateErr := UpdateSessionStatus(db, session.SessionID, SessionStatusCrashed); updateErr != nil {
			return nil, fmt.Errorf("failed to mark active session as crashed: %w", updateErr)
		}
		session.Status = SessionStatusCrashed
	}

	// Count incomplete and done stories for this session
	var incompleteCount, doneCount int
	err = db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN status NOT IN ('done', 'failed') THEN 1 ELSE 0 END), 0) as incomplete,
			COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0) as done
		FROM stories
		WHERE session_id = ?
	`, session.SessionID).Scan(&incompleteCount, &doneCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count stories for session %s: %w", session.SessionID, err)
	}

	// Only return if there are incomplete stories
	if incompleteCount == 0 {
		return nil, nil
	}

	return &ResumableSessionInfo{
		Session:           session,
		IncompleteStories: incompleteCount,
		DoneStories:       doneCount,
	}, nil
}

// GetSession returns a session by ID.
// Returns ErrSessionNotFound if the session does not exist.
func GetSession(db *sql.DB, sessionID string) (*Session, error) {
	row := db.QueryRow(`
		SELECT session_id, started_at, ended_at, status, config_json
		FROM sessions
		WHERE session_id = ?
	`, sessionID)

	session, err := scanSession(row)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return session, nil
}

// MarkStaleSessions marks any 'active' sessions as 'crashed'.
// This should be called at startup to detect sessions that didn't shut down gracefully.
func MarkStaleSessions(db *sql.DB) (int64, error) {
	result, err := db.Exec(`
		UPDATE sessions
		SET status = ?, ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE status = ?
	`, SessionStatusCrashed, SessionStatusActive)
	if err != nil {
		return 0, fmt.Errorf("failed to mark stale sessions: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

// SaveAgentContext saves or updates an agent's conversation context.
func SaveAgentContext(db *sql.DB, ctx *AgentContext) error {
	_, err := db.Exec(`
		INSERT INTO agent_contexts (session_id, agent_id, context_type, messages_json, updated_at)
		VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(session_id, agent_id, context_type) DO UPDATE SET
			messages_json = excluded.messages_json,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	`, ctx.SessionID, ctx.AgentID, ctx.ContextType, ctx.MessagesJSON)
	if err != nil {
		return fmt.Errorf("failed to save agent context: %w", err)
	}
	return nil
}

// GetAgentContexts returns all conversation contexts for an agent in a session.
func GetAgentContexts(db *sql.DB, sessionID, agentID string) ([]AgentContext, error) {
	rows, err := db.Query(`
		SELECT session_id, agent_id, context_type, messages_json, updated_at
		FROM agent_contexts
		WHERE session_id = ? AND agent_id = ?
	`, sessionID, agentID)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent contexts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var contexts []AgentContext
	for rows.Next() {
		var ctx AgentContext
		if err := rows.Scan(&ctx.SessionID, &ctx.AgentID, &ctx.ContextType, &ctx.MessagesJSON, &ctx.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan agent context: %w", err)
		}
		contexts = append(contexts, ctx)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agent contexts: %w", err)
	}
	return contexts, nil
}

// DeleteOldContexts removes agent contexts from sessions other than the specified one.
func DeleteOldContexts(db *sql.DB, keepSessionID string) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM agent_contexts WHERE session_id != ?
	`, keepSessionID)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old contexts: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

// SaveCoderState saves or updates a coder's state for resume.
func SaveCoderState(db *sql.DB, state *CoderState) error {
	_, err := db.Exec(`
		INSERT INTO coder_state (
			session_id, agent_id, story_id, state, plan_json, todo_list_json,
			current_todo_index, knowledge_pack_json, pending_request_type,
			pending_request_json, container_image, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(session_id, agent_id) DO UPDATE SET
			story_id = excluded.story_id,
			state = excluded.state,
			plan_json = excluded.plan_json,
			todo_list_json = excluded.todo_list_json,
			current_todo_index = excluded.current_todo_index,
			knowledge_pack_json = excluded.knowledge_pack_json,
			pending_request_type = excluded.pending_request_type,
			pending_request_json = excluded.pending_request_json,
			container_image = excluded.container_image,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	`, state.SessionID, state.AgentID, state.StoryID, state.State, state.PlanJSON,
		state.TodoListJSON, state.CurrentTodoIndex, state.KnowledgePackJSON,
		state.PendingRequestType, state.PendingRequestJSON, state.ContainerImage)
	if err != nil {
		return fmt.Errorf("failed to save coder state: %w", err)
	}
	return nil
}

// GetCoderState returns a coder's saved state for a session.
// Returns ErrSessionNotFound if no state exists for the coder.
func GetCoderState(db *sql.DB, sessionID, agentID string) (*CoderState, error) {
	row := db.QueryRow(`
		SELECT session_id, agent_id, story_id, state, plan_json, todo_list_json,
			   current_todo_index, knowledge_pack_json, pending_request_type,
			   pending_request_json, container_image, updated_at
		FROM coder_state
		WHERE session_id = ? AND agent_id = ?
	`, sessionID, agentID)

	var state CoderState
	err := row.Scan(&state.SessionID, &state.AgentID, &state.StoryID, &state.State,
		&state.PlanJSON, &state.TodoListJSON, &state.CurrentTodoIndex,
		&state.KnowledgePackJSON, &state.PendingRequestType, &state.PendingRequestJSON,
		&state.ContainerImage, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get coder state: %w", err)
	}
	return &state, nil
}

// GetAllCoderStates returns all coder states for a session.
func GetAllCoderStates(db *sql.DB, sessionID string) ([]CoderState, error) {
	rows, err := db.Query(`
		SELECT session_id, agent_id, story_id, state, plan_json, todo_list_json,
			   current_todo_index, knowledge_pack_json, pending_request_type,
			   pending_request_json, container_image, updated_at
		FROM coder_state
		WHERE session_id = ?
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query coder states: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var states []CoderState
	for rows.Next() {
		var state CoderState
		if err := rows.Scan(&state.SessionID, &state.AgentID, &state.StoryID, &state.State,
			&state.PlanJSON, &state.TodoListJSON, &state.CurrentTodoIndex,
			&state.KnowledgePackJSON, &state.PendingRequestType, &state.PendingRequestJSON,
			&state.ContainerImage, &state.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan coder state: %w", err)
		}
		states = append(states, state)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating coder states: %w", err)
	}
	return states, nil
}

// SaveArchitectState saves or updates the architect's state for resume.
func SaveArchitectState(db *sql.DB, state *ArchitectState) error {
	_, err := db.Exec(`
		INSERT INTO architect_state (session_id, state, escalation_counts_json, updated_at)
		VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(session_id) DO UPDATE SET
			state = excluded.state,
			escalation_counts_json = excluded.escalation_counts_json,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	`, state.SessionID, state.State, state.EscalationCountsJSON)
	if err != nil {
		return fmt.Errorf("failed to save architect state: %w", err)
	}
	return nil
}

// GetArchitectState returns the architect's saved state for a session.
// Returns ErrSessionNotFound if no state exists for the architect.
func GetArchitectState(db *sql.DB, sessionID string) (*ArchitectState, error) {
	row := db.QueryRow(`
		SELECT session_id, state, escalation_counts_json, updated_at
		FROM architect_state
		WHERE session_id = ?
	`, sessionID)

	var state ArchitectState
	err := row.Scan(&state.SessionID, &state.State, &state.EscalationCountsJSON, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get architect state: %w", err)
	}
	return &state, nil
}

// SavePMState saves or updates the PM's state for resume.
func SavePMState(db *sql.DB, state *PMState) error {
	_, err := db.Exec(`
		INSERT INTO pm_state (session_id, state, spec_content, bootstrap_params_json, updated_at)
		VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(session_id) DO UPDATE SET
			state = excluded.state,
			spec_content = excluded.spec_content,
			bootstrap_params_json = excluded.bootstrap_params_json,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	`, state.SessionID, state.State, state.SpecContent, state.BootstrapParamsJSON)
	if err != nil {
		return fmt.Errorf("failed to save PM state: %w", err)
	}
	return nil
}

// GetPMState returns the PM's saved state for a session.
// Returns ErrSessionNotFound if no state exists for the PM.
func GetPMState(db *sql.DB, sessionID string) (*PMState, error) {
	row := db.QueryRow(`
		SELECT session_id, state, spec_content, bootstrap_params_json, updated_at
		FROM pm_state
		WHERE session_id = ?
	`, sessionID)

	var state PMState
	err := row.Scan(&state.SessionID, &state.State, &state.SpecContent, &state.BootstrapParamsJSON, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get PM state: %w", err)
	}
	return &state, nil
}

// CleanupOldSessionData removes state data from sessions other than the specified one.
// This keeps only the most recent session's data to prevent unbounded growth.
func CleanupOldSessionData(db *sql.DB, keepSessionID string) error {
	tables := []string{
		"agent_contexts",
		"coder_state",
		"architect_state",
		"pm_state",
	}

	for _, table := range tables {
		//nolint:gosec // table names are hardcoded constants, not user input
		_, err := db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE session_id != ?`, table), keepSessionID)
		if err != nil {
			return fmt.Errorf("failed to cleanup %s: %w", table, err)
		}
	}

	return nil
}

// ResetInFlightStories resets stories with in-flight statuses back to 'new'.
// This is used during crash recovery to restart stories that were mid-execution.
// Returns the number of stories that were reset.
func ResetInFlightStories(db *sql.DB, sessionID string) (int64, error) {
	// Reset dispatched, planning, coding, in_progress, and review statuses to 'new'.
	// 'dispatched' = sent to work queue, waiting for coder pickup
	// 'planning' = coder picked up story, planning work
	// 'coding' = coder is implementing
	// 'in_progress' = legacy alias for coding
	// 'review' = coder submitted for architect review
	result, err := db.Exec(`
		UPDATE stories
		SET status = 'new',
		    assigned_agent = NULL,
		    started_at = NULL
		WHERE session_id = ?
		  AND status IN ('dispatched', 'planning', 'coding', 'review', 'in_progress')
	`, sessionID)
	if err != nil {
		return 0, fmt.Errorf("failed to reset in-flight stories: %w", err)
	}

	affected, _ := result.RowsAffected()
	return affected, nil
}

// GetIncompleteStoriesForSession returns all stories that are not yet done or failed.
// Used by the architect to reload stories into the queue on resume.
// Also populates the DependsOn field from the story_dependencies table.
func GetIncompleteStoriesForSession(db *sql.DB, sessionID string) ([]*Story, error) {
	rows, err := db.Query(`
		SELECT id, spec_id, title, content, status, priority, approved_plan,
		       created_at, started_at, completed_at, assigned_agent,
		       tokens_used, cost_usd, metadata, story_type
		FROM stories
		WHERE session_id = ?
		  AND status NOT IN ('done', 'failed')
		ORDER BY priority DESC, created_at ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query incomplete stories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stories []*Story
	for rows.Next() {
		story := &Story{}
		scanErr := rows.Scan(
			&story.ID, &story.SpecID, &story.Title, &story.Content,
			&story.Status, &story.Priority, &story.ApprovedPlan,
			&story.CreatedAt, &story.StartedAt, &story.CompletedAt,
			&story.AssignedAgent, &story.TokensUsed, &story.CostUSD,
			&story.Metadata, &story.StoryType,
		)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan story: %w", scanErr)
		}
		stories = append(stories, story)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating stories: %w", err)
	}

	// Populate DependsOn for all stories in a single query
	if len(stories) > 0 {
		if depErr := populateStoryDependencies(db, sessionID, stories); depErr != nil {
			return nil, fmt.Errorf("failed to populate dependencies: %w", depErr)
		}
	}

	return stories, nil
}

// populateStoryDependencies fetches and populates DependsOn for a slice of stories.
func populateStoryDependencies(db *sql.DB, sessionID string, stories []*Story) error {
	// Build a map for quick lookup
	storyMap := make(map[string]*Story, len(stories))
	for _, s := range stories {
		storyMap[s.ID] = s
		s.DependsOn = []string{} // Initialize to empty slice
	}

	// Get all dependencies for incomplete stories in this session.
	// We query all dependencies for the session's incomplete stories
	// rather than filtering by specific story IDs to keep the query simple.
	depRows, err := db.Query(`
		SELECT sd.story_id, sd.depends_on
		FROM story_dependencies sd
		INNER JOIN stories s ON sd.story_id = s.id
		WHERE s.session_id = ?
		  AND s.status NOT IN ('done', 'failed')
	`, sessionID)
	if err != nil {
		return fmt.Errorf("failed to query dependencies: %w", err)
	}
	defer func() { _ = depRows.Close() }()

	for depRows.Next() {
		var storyID, dependsOn string
		if scanErr := depRows.Scan(&storyID, &dependsOn); scanErr != nil {
			return fmt.Errorf("failed to scan dependency: %w", scanErr)
		}
		if story, exists := storyMap[storyID]; exists {
			story.DependsOn = append(story.DependsOn, dependsOn)
		}
	}

	if err := depRows.Err(); err != nil {
		return fmt.Errorf("error iterating dependencies: %w", err)
	}
	return nil
}

// ConfigSnapshotToJSON converts a config struct to JSON for storage.
func ConfigSnapshotToJSON(config interface{}) (string, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config: %w", err)
	}
	return string(data), nil
}

// ConfigSnapshotFromJSON parses a JSON config snapshot.
func ConfigSnapshotFromJSON(jsonStr string, target interface{}) error {
	if err := json.Unmarshal([]byte(jsonStr), target); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return nil
}
