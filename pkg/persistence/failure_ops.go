package persistence

import (
	"database/sql"
	"fmt"
)

// UpdateFailureResolutionRequest represents a request to update failure resolution fields.
type UpdateFailureResolutionRequest struct {
	ID                string `json:"id"`
	ResolutionStatus  string `json:"resolution_status"`
	ResolutionOutcome string `json:"resolution_outcome,omitempty"`
}

// CountFailuresByStoryAndActionResponse holds the result of counting failures by action.
type CountFailuresByStoryAndActionResponse struct {
	Counts map[string]int `json:"counts"` // action -> count
}

// PersistFailure inserts a failure record into the failures table.
func (ops *DatabaseOperations) PersistFailure(record *FailureRecord) error {
	query := `
		INSERT INTO failures (
			id, session_id, created_at, updated_at,
			spec_id, story_id, attempt_number,
			source, reporter_agent_id, failed_state, tool_name,
			kind, scope_guess, explanation, human_needed_guess, evidence,
			resolved_kind, resolved_scope, human_needed, affected_story_ids, triage_summary,
			owner, action, resolution_status, resolution_outcome,
			tags, model, provider, base_commit
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := ops.db.Exec(query,
		record.ID, ops.sessionID, record.CreatedAt, record.UpdatedAt,
		record.SpecID, record.StoryID, record.AttemptNumber,
		record.Source, record.ReporterAgentID, record.FailedState, record.ToolName,
		record.Kind, record.ScopeGuess, record.Explanation, record.HumanNeededGuess, record.Evidence,
		record.ResolvedKind, record.ResolvedScope, record.HumanNeeded, record.AffectedStoryIDs, record.TriageSummary,
		record.Owner, record.Action, record.ResolutionStatus, record.ResolutionOutcome,
		record.Tags, record.Model, record.Provider, record.BaseCommit,
	)
	if err != nil {
		return fmt.Errorf("failed to persist failure %s: %w", record.ID, err)
	}
	return nil
}

// UpdateFailureResolution updates the resolution fields of a failure record.
func (ops *DatabaseOperations) UpdateFailureResolution(req *UpdateFailureResolutionRequest) error {
	query := `
		UPDATE failures SET
			resolution_status = ?,
			resolution_outcome = ?,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ? AND session_id = ?
	`

	result, err := ops.db.Exec(query, req.ResolutionStatus, req.ResolutionOutcome, req.ID, ops.sessionID)
	if err != nil {
		return fmt.Errorf("failed to update failure resolution %s: %w", req.ID, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("failure %s not found", req.ID)
	}

	return nil
}

// QueryFailuresByStory returns all failure records for a given story.
func (ops *DatabaseOperations) QueryFailuresByStory(storyID string) ([]*FailureRecord, error) {
	query := `
		SELECT id, session_id, created_at, updated_at,
			spec_id, story_id, attempt_number,
			source, reporter_agent_id, failed_state, tool_name,
			kind, scope_guess, explanation, human_needed_guess, evidence,
			resolved_kind, resolved_scope, human_needed, affected_story_ids, triage_summary,
			owner, action, resolution_status, resolution_outcome,
			tags, model, provider, base_commit
		FROM failures
		WHERE story_id = ? AND session_id = ?
		ORDER BY created_at DESC
	`

	rows, err := ops.db.Query(query, storyID, ops.sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query failures for story %s: %w", storyID, err)
	}
	defer func() { _ = rows.Close() }()

	return scanFailureRecords(rows)
}

// scanFailureRecords scans all rows from a failures query into FailureRecords.
func scanFailureRecords(rows *sql.Rows) ([]*FailureRecord, error) {
	var records []*FailureRecord
	for rows.Next() {
		r := &FailureRecord{}
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.CreatedAt, &r.UpdatedAt,
			&r.SpecID, &r.StoryID, &r.AttemptNumber,
			&r.Source, &r.ReporterAgentID, &r.FailedState, &r.ToolName,
			&r.Kind, &r.ScopeGuess, &r.Explanation, &r.HumanNeededGuess, &r.Evidence,
			&r.ResolvedKind, &r.ResolvedScope, &r.HumanNeeded, &r.AffectedStoryIDs, &r.TriageSummary,
			&r.Owner, &r.Action, &r.ResolutionStatus, &r.ResolutionOutcome,
			&r.Tags, &r.Model, &r.Provider, &r.BaseCommit,
		); err != nil {
			return nil, fmt.Errorf("failed to scan failure record: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failure record row iteration error: %w", err)
	}
	return records, nil
}

// QueryFailureByID returns a single failure record by ID.
func (ops *DatabaseOperations) QueryFailureByID(id string) (*FailureRecord, error) {
	query := `
		SELECT id, session_id, created_at, updated_at,
			spec_id, story_id, attempt_number,
			source, reporter_agent_id, failed_state, tool_name,
			kind, scope_guess, explanation, human_needed_guess, evidence,
			resolved_kind, resolved_scope, human_needed, affected_story_ids, triage_summary,
			owner, action, resolution_status, resolution_outcome,
			tags, model, provider, base_commit
		FROM failures
		WHERE id = ? AND session_id = ?
	`

	r := &FailureRecord{}
	err := ops.db.QueryRow(query, id, ops.sessionID).Scan(
		&r.ID, &r.SessionID, &r.CreatedAt, &r.UpdatedAt,
		&r.SpecID, &r.StoryID, &r.AttemptNumber,
		&r.Source, &r.ReporterAgentID, &r.FailedState, &r.ToolName,
		&r.Kind, &r.ScopeGuess, &r.Explanation, &r.HumanNeededGuess, &r.Evidence,
		&r.ResolvedKind, &r.ResolvedScope, &r.HumanNeeded, &r.AffectedStoryIDs, &r.TriageSummary,
		&r.Owner, &r.Action, &r.ResolutionStatus, &r.ResolutionOutcome,
		&r.Tags, &r.Model, &r.Provider, &r.BaseCommit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query failure %s: %w", id, err)
	}

	return r, nil
}

// CountFailuresByStoryAndAction returns a map of action -> count for all failures of a given story.
// Used for budget reconstruction on resume.
func (ops *DatabaseOperations) CountFailuresByStoryAndAction(storyID string) (map[string]int, error) {
	query := `
		SELECT action, COUNT(*) as cnt
		FROM failures
		WHERE story_id = ? AND session_id = ?
		GROUP BY action
	`

	rows, err := ops.db.Query(query, storyID, ops.sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to count failures for story %s: %w", storyID, err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]int)
	for rows.Next() {
		var action string
		var count int
		if err := rows.Scan(&action, &count); err != nil {
			return nil, fmt.Errorf("failed to scan failure count: %w", err)
		}
		counts[action] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error for story %s failure counts: %w", storyID, err)
	}

	return counts, nil
}

// CountFailuresByStoryAndActionForSession is a standalone version of CountFailuresByStoryAndAction
// for use during resume when DatabaseOperations is not available.
func CountFailuresByStoryAndActionForSession(db *sql.DB, sessionID, storyID string) (map[string]int, error) {
	query := `
		SELECT action, COUNT(*) as cnt
		FROM failures
		WHERE story_id = ? AND session_id = ?
		GROUP BY action
	`

	rows, err := db.Query(query, storyID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to count failures for story %s: %w", storyID, err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]int)
	for rows.Next() {
		var action string
		var count int
		if err := rows.Scan(&action, &count); err != nil {
			return nil, fmt.Errorf("failed to scan failure count: %w", err)
		}
		counts[action] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error for story %s failure counts: %w", storyID, err)
	}

	return counts, nil
}

// QueryFailuresBySessionForDB returns all failure records for a given session.
// Standalone function for use during shutdown/startup when DatabaseOperations
// may not be available.
func QueryFailuresBySessionForDB(db *sql.DB, sessionID string) ([]*FailureRecord, error) {
	query := `
		SELECT id, session_id, created_at, updated_at,
			spec_id, story_id, attempt_number,
			source, reporter_agent_id, failed_state, tool_name,
			kind, scope_guess, explanation, human_needed_guess, evidence,
			resolved_kind, resolved_scope, human_needed, affected_story_ids, triage_summary,
			owner, action, resolution_status, resolution_outcome,
			tags, model, provider, base_commit
		FROM failures
		WHERE session_id = ?
		ORDER BY created_at ASC
	`

	rows, err := db.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query failures for session %s: %w", sessionID, err)
	}
	defer func() { _ = rows.Close() }()

	return scanFailureRecords(rows)
}

// SessionSummary holds aggregate stats for a session's telemetry report.
type SessionSummary struct {
	StartedAt        string `json:"started_at"`
	EndedAt          string `json:"ended_at"`
	SessionStatus    string `json:"session_status"`
	StoriesTotal     int    `json:"stories_total"`
	StoriesCompleted int    `json:"stories_completed"`
	StoriesFailed    int    `json:"stories_failed"`
	StoriesHeld      int    `json:"stories_held"`
}

// QuerySessionSummary gathers session timestamps and story counts by status.
func QuerySessionSummary(db *sql.DB, sessionID string) (*SessionSummary, error) {
	summary := &SessionSummary{}

	// Get session timestamps and status
	err := db.QueryRow(`
		SELECT
			COALESCE(started_at, ''),
			COALESCE(ended_at, ''),
			COALESCE(status, '')
		FROM sessions
		WHERE session_id = ?
	`, sessionID).Scan(&summary.StartedAt, &summary.EndedAt, &summary.SessionStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to query session %s: %w", sessionID, err)
	}

	// Count stories by status
	rows, err := db.Query(`
		SELECT status, COUNT(*) as cnt
		FROM stories
		WHERE session_id = ?
		GROUP BY status
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to count stories for session %s: %w", sessionID, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("failed to scan story count: %w", err)
		}
		summary.StoriesTotal += count
		switch status {
		case "done":
			summary.StoriesCompleted += count
		case "failed":
			summary.StoriesFailed += count
		case "on_hold":
			summary.StoriesHeld += count
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session summary row iteration error: %w", err)
	}
	return summary, nil
}
