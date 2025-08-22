package persistence

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Request represents a database operation request.
// This is the interface between agents and the orchestrator's database worker.
type Request struct {
	Data      interface{}        `json:"data"`      // Operation-specific data payload
	Response  chan<- interface{} `json:"-"`         // Response channel for queries (nil for fire-and-forget writes)
	Operation string             `json:"operation"` // Operation type
}

// Operation constants for Request.
const (
	// Write operations (fire-and-forget).
	OpUpsertSpec            = "upsert_spec"
	OpUpsertStory           = "upsert_story"
	OpUpdateStoryStatus     = "update_story_status"
	OpAddStoryDependency    = "add_story_dependency"
	OpRemoveStoryDependency = "remove_story_dependency"

	// Agent interaction operations.
	OpUpsertAgentRequest  = "upsert_agent_request"
	OpUpsertAgentResponse = "upsert_agent_response"
	OpUpsertAgentPlan     = "upsert_agent_plan"
	OpUpdateAgentPlan     = "update_agent_plan"

	// Query operations (with response).
	OpQueryStoriesByStatus               = "query_stories_by_status"
	OpQueryPendingStories                = "query_pending_stories"
	OpGetStoryDependencies               = "get_story_dependencies"
	OpGetSpecSummary                     = "get_spec_summary"
	OpGetStoryByID                       = "get_story_by_id"
	OpGetSpecByID                        = "get_spec_by_id"
	OpGetAllStories                      = "get_all_stories"
	OpGetAgentRequestsByStory            = "get_agent_requests_by_story"
	OpGetAgentResponsesByStory           = "get_agent_responses_by_story"
	OpGetAgentPlansByStory               = "get_agent_plans_by_story"
	OpBatchUpsertStoriesWithDependencies = "batch_upsert_stories_with_dependencies"
)

// UpdateStoryStatusRequest represents a status update request.
type UpdateStoryStatusRequest struct {
	Timestamp         time.Time `json:"timestamp,omitempty"`
	PromptTokens      *int64    `json:"prompt_tokens,omitempty"`      // Total prompt tokens used for this story
	CompletionTokens  *int64    `json:"completion_tokens,omitempty"`  // Total completion tokens used for this story
	CostUSD           *float64  `json:"cost_usd,omitempty"`           // Total cost in USD for this story
	PRID              *string   `json:"pr_id,omitempty"`              // Pull request ID
	CommitHash        *string   `json:"commit_hash,omitempty"`        // Commit hash from merge
	CompletionSummary *string   `json:"completion_summary,omitempty"` // Summary of what was completed
	StoryID           string    `json:"story_id"`
	Status            string    `json:"status"`
}

// BatchUpsertStoriesWithDependenciesRequest represents a batch operation for atomically inserting stories with dependencies.
type BatchUpsertStoriesWithDependenciesRequest struct {
	Stories      []*Story           `json:"stories"`      // Stories to upsert
	Dependencies []*StoryDependency `json:"dependencies"` // Dependencies to add after stories are inserted
}

// DatabaseOperations provides methods for database operations.
// This is used by the orchestrator's database worker goroutine.
type DatabaseOperations struct {
	db *sql.DB
}

// NewDatabaseOperations creates a new DatabaseOperations instance.
func NewDatabaseOperations(db *sql.DB) *DatabaseOperations {
	return &DatabaseOperations{db: db}
}

// UpsertSpec inserts or updates a spec record.
func (ops *DatabaseOperations) UpsertSpec(spec *Spec) error {
	query := `
		INSERT INTO specs (id, content, created_at, processed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			processed_at = excluded.processed_at
	`

	_, err := ops.db.Exec(query, spec.ID, spec.Content, spec.CreatedAt, spec.ProcessedAt)
	if err != nil {
		return fmt.Errorf("failed to upsert spec %s: %w", spec.ID, err)
	}
	return nil
}

// UpsertStory inserts or updates a story record.
func (ops *DatabaseOperations) UpsertStory(story *Story) error {
	query := `
		INSERT INTO stories (
			id, spec_id, title, content, status, priority, approved_plan,
			created_at, started_at, completed_at, assigned_agent,
			tokens_used, cost_usd, metadata, story_type, pr_id, commit_hash, completion_summary
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			spec_id = excluded.spec_id,
			title = excluded.title,
			content = excluded.content,
			status = excluded.status,
			priority = excluded.priority,
			approved_plan = excluded.approved_plan,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			assigned_agent = excluded.assigned_agent,
			tokens_used = excluded.tokens_used,
			cost_usd = excluded.cost_usd,
			metadata = excluded.metadata,
			story_type = excluded.story_type,
			pr_id = excluded.pr_id,
			commit_hash = excluded.commit_hash,
			completion_summary = excluded.completion_summary
	`

	_, err := ops.db.Exec(query,
		story.ID, story.SpecID, story.Title, story.Content, story.Status,
		story.Priority, story.ApprovedPlan, story.CreatedAt, story.StartedAt,
		story.CompletedAt, story.AssignedAgent, story.TokensUsed,
		story.CostUSD, story.Metadata, story.StoryType, story.PRID, story.CommitHash, story.CompletionSummary,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert story %s: %w", story.ID, err)
	}
	return nil
}

// UpdateStoryStatus updates the status, timestamp, and optionally token/cost fields of a story.
func (ops *DatabaseOperations) UpdateStoryStatus(req *UpdateStoryStatusRequest) error {
	// Determine which timestamp field to update based on status
	var timestampField string
	switch req.Status {
	case StatusPlanning, StatusCoding:
		timestampField = "started_at"
	case StatusDone:
		timestampField = "completed_at"
	}

	// Build the update query dynamically based on what fields are provided
	setParts := []string{"status = ?"}
	args := []interface{}{req.Status}

	// Add timestamp if applicable
	if timestampField != "" {
		setParts = append(setParts, fmt.Sprintf("%s = ?", timestampField))
		timestamp := req.Timestamp
		if timestamp.IsZero() {
			timestamp = time.Now()
		}
		args = append(args, timestamp)
	}

	// Add token and cost fields if provided (for completion states)
	if req.PromptTokens != nil {
		setParts = append(setParts, "tokens_used = ?")
		totalTokens := *req.PromptTokens
		if req.CompletionTokens != nil {
			totalTokens += *req.CompletionTokens
		}
		args = append(args, totalTokens)
	}

	if req.CostUSD != nil {
		setParts = append(setParts, "cost_usd = ?")
		args = append(args, *req.CostUSD)
	}

	// Add completion-related fields if provided
	if req.PRID != nil {
		setParts = append(setParts, "pr_id = ?")
		args = append(args, *req.PRID)
	}

	if req.CommitHash != nil {
		setParts = append(setParts, "commit_hash = ?")
		args = append(args, *req.CommitHash)
	}

	if req.CompletionSummary != nil {
		setParts = append(setParts, "completion_summary = ?")
		args = append(args, *req.CompletionSummary)
	}

	// Add WHERE clause
	args = append(args, req.StoryID)

	//nolint:gosec // Using safe string concatenation for dynamic query building with bounded inputs
	query := `UPDATE stories SET ` + strings.Join(setParts, ", ") + ` WHERE id = ?`

	result, err := ops.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update story status for %s: %w", req.StoryID, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("story %s not found", req.StoryID)
	}

	return nil
}

// AddStoryDependency adds a dependency relationship between stories.
func (ops *DatabaseOperations) AddStoryDependency(storyID, dependsOn string) error {
	query := `
		INSERT OR IGNORE INTO story_dependencies (story_id, depends_on)
		VALUES (?, ?)
	`

	_, err := ops.db.Exec(query, storyID, dependsOn)
	if err != nil {
		return fmt.Errorf("failed to add dependency %s -> %s: %w", storyID, dependsOn, err)
	}
	return nil
}

// RemoveStoryDependency removes a dependency relationship between stories.
func (ops *DatabaseOperations) RemoveStoryDependency(storyID, dependsOn string) error {
	query := `DELETE FROM story_dependencies WHERE story_id = ? AND depends_on = ?`

	_, err := ops.db.Exec(query, storyID, dependsOn)
	if err != nil {
		return fmt.Errorf("failed to remove dependency %s -> %s: %w", storyID, dependsOn, err)
	}
	return nil
}

// QueryStoriesByFilter returns stories matching the given filter criteria.
func (ops *DatabaseOperations) QueryStoriesByFilter(filter *StoryFilter) ([]*Story, error) {
	query := "SELECT id, spec_id, title, content, status, priority, approved_plan, created_at, started_at, completed_at, assigned_agent, tokens_used, cost_usd, metadata FROM stories WHERE 1=1"
	var args []interface{}

	// Build WHERE conditions
	if filter.Status != nil {
		query += " AND status = ?"
		args = append(args, *filter.Status)
	}
	if filter.AssignedAgent != nil {
		query += " AND assigned_agent = ?"
		args = append(args, *filter.AssignedAgent)
	}
	if filter.SpecID != nil {
		query += " AND spec_id = ?"
		args = append(args, *filter.SpecID)
	}
	if len(filter.Statuses) > 0 {
		placeholders := strings.Repeat("?,", len(filter.Statuses))
		placeholders = placeholders[:len(placeholders)-1] // Remove trailing comma
		query += fmt.Sprintf(" AND status IN (%s)", placeholders)
		for _, status := range filter.Statuses {
			args = append(args, status)
		}
	}

	query += " ORDER BY priority DESC, created_at ASC"

	rows, err := ops.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query stories: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			// Log error but don't override the main error
			_ = closeErr
		}
	}()

	var stories []*Story
	for rows.Next() {
		story := &Story{}
		err := rows.Scan(
			&story.ID, &story.SpecID, &story.Title, &story.Content,
			&story.Status, &story.Priority, &story.ApprovedPlan,
			&story.CreatedAt, &story.StartedAt, &story.CompletedAt,
			&story.AssignedAgent, &story.TokensUsed, &story.CostUSD,
			&story.Metadata,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan story: %w", err)
		}
		stories = append(stories, story)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return stories, nil
}

// QueryPendingStories returns stories that are ready to be worked on (no unfinished dependencies).
func (ops *DatabaseOperations) QueryPendingStories() ([]*Story, error) {
	return ops.queryStoriesBySQL(`
		SELECT DISTINCT s.id, s.spec_id, s.title, s.content, s.status, s.priority, 
		       s.approved_plan, s.created_at, s.started_at, s.completed_at, 
		       s.assigned_agent, s.tokens_used, s.cost_usd, s.metadata
		FROM stories s
		LEFT JOIN story_dependencies d ON s.id = d.story_id
		LEFT JOIN stories dep ON d.depends_on = dep.id 
		    AND dep.status NOT IN ('`+StatusDone+`')
		WHERE s.status = '`+StatusNew+`' AND dep.id IS NULL
		ORDER BY s.priority DESC, s.created_at ASC
	`, "pending stories")
}

// GetStoryDependencies returns all dependencies for a given story.
func (ops *DatabaseOperations) GetStoryDependencies(storyID string) ([]string, error) {
	query := `SELECT depends_on FROM story_dependencies WHERE story_id = ?`

	rows, err := ops.db.Query(query, storyID)
	if err != nil {
		return nil, fmt.Errorf("failed to query dependencies for story %s: %w", storyID, err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			// Log error but don't override the main error
			_ = closeErr
		}
	}()

	var dependencies []string
	for rows.Next() {
		var dependsOn string
		if err := rows.Scan(&dependsOn); err != nil {
			return nil, fmt.Errorf("failed to scan dependency: %w", err)
		}
		dependencies = append(dependencies, dependsOn)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return dependencies, nil
}

// GetSpecSummary returns aggregated metrics for a spec.
func (ops *DatabaseOperations) GetSpecSummary(specID string) (*SpecSummary, error) {
	query := `
		SELECT 
			spec_id,
			COUNT(*) as total_stories,
			SUM(CASE WHEN status IN ('committed', 'merged') THEN 1 ELSE 0 END) as completed_stories,
			SUM(tokens_used) as total_tokens,
			SUM(cost_usd) as total_cost,
			MAX(CASE WHEN status IN ('committed', 'merged') THEN completed_at END) as last_completed
		FROM stories 
		WHERE spec_id = ?
		GROUP BY spec_id
	`

	summary := &SpecSummary{SpecID: specID}
	err := ops.db.QueryRow(query, specID).Scan(
		&summary.SpecID,
		&summary.TotalStories,
		&summary.CompletedStories,
		&summary.TotalTokens,
		&summary.TotalCost,
		&summary.LastCompleted,
	)

	if err == sql.ErrNoRows {
		// No stories for this spec yet
		return summary, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get spec summary for %s: %w", specID, err)
	}

	return summary, nil
}

// GetStoryByID returns a story by its ID.
func (ops *DatabaseOperations) GetStoryByID(storyID string) (*Story, error) {
	query := `
		SELECT id, spec_id, title, content, status, priority, approved_plan, 
		       created_at, started_at, completed_at, assigned_agent, 
		       tokens_used, cost_usd, metadata
		FROM stories WHERE id = ?
	`

	story := &Story{}
	err := ops.db.QueryRow(query, storyID).Scan(
		&story.ID, &story.SpecID, &story.Title, &story.Content,
		&story.Status, &story.Priority, &story.ApprovedPlan,
		&story.CreatedAt, &story.StartedAt, &story.CompletedAt,
		&story.AssignedAgent, &story.TokensUsed, &story.CostUSD,
		&story.Metadata,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("story %s not found", storyID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get story %s: %w", storyID, err)
	}

	return story, nil
}

// GetSpecByID returns a spec by its ID.
func (ops *DatabaseOperations) GetSpecByID(specID string) (*Spec, error) {
	query := `SELECT id, content, created_at, processed_at FROM specs WHERE id = ?`

	spec := &Spec{}
	err := ops.db.QueryRow(query, specID).Scan(
		&spec.ID, &spec.Content, &spec.CreatedAt, &spec.ProcessedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("spec %s not found", specID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get spec %s: %w", specID, err)
	}

	return spec, nil
}

// queryStoriesBySQL is a helper function to execute story queries and scan results.
func (ops *DatabaseOperations) queryStoriesBySQL(query, queryType string) ([]*Story, error) {
	rows, err := ops.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s: %w", queryType, err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			// Log error but don't override the main error
			_ = closeErr
		}
	}()

	var stories []*Story
	for rows.Next() {
		story := &Story{}
		err := rows.Scan(
			&story.ID, &story.SpecID, &story.Title, &story.Content,
			&story.Status, &story.Priority, &story.ApprovedPlan,
			&story.CreatedAt, &story.StartedAt, &story.CompletedAt,
			&story.AssignedAgent, &story.TokensUsed, &story.CostUSD,
			&story.Metadata,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan story: %w", err)
		}
		stories = append(stories, story)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return stories, nil
}

// GetAllStories returns all stories in the database.
func (ops *DatabaseOperations) GetAllStories() ([]*Story, error) {
	return ops.queryStoriesBySQL(`
		SELECT id, spec_id, title, content, status, priority, approved_plan, 
		       created_at, started_at, completed_at, assigned_agent, 
		       tokens_used, cost_usd, metadata
		FROM stories ORDER BY priority DESC, created_at ASC
	`, "all stories")
}

// UpsertAgentRequest inserts or updates an agent request record.
func (ops *DatabaseOperations) UpsertAgentRequest(request *AgentRequest) error {
	query := `
		INSERT INTO agent_requests (
			id, story_id, request_type, approval_type, from_agent, to_agent, 
			content, context, reason, created_at, correlation_id, parent_msg_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			story_id = excluded.story_id,
			request_type = excluded.request_type,
			approval_type = excluded.approval_type,
			from_agent = excluded.from_agent,
			to_agent = excluded.to_agent,
			content = excluded.content,
			context = excluded.context,
			reason = excluded.reason,
			correlation_id = excluded.correlation_id,
			parent_msg_id = excluded.parent_msg_id
	`

	_, err := ops.db.Exec(query,
		request.ID, request.StoryID, request.RequestType, request.ApprovalType,
		request.FromAgent, request.ToAgent, request.Content, request.Context,
		request.Reason, request.CreatedAt, request.CorrelationID, request.ParentMsgID)
	if err != nil {
		return fmt.Errorf("failed to upsert agent request %s: %w", request.ID, err)
	}
	return nil
}

// UpsertAgentResponse inserts or updates an agent response record.
func (ops *DatabaseOperations) UpsertAgentResponse(response *AgentResponse) error {
	query := `
		INSERT INTO agent_responses (
			id, request_id, story_id, response_type, from_agent, to_agent,
			content, status, feedback, created_at, correlation_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			request_id = excluded.request_id,
			story_id = excluded.story_id,
			response_type = excluded.response_type,
			from_agent = excluded.from_agent,
			to_agent = excluded.to_agent,
			content = excluded.content,
			status = excluded.status,
			feedback = excluded.feedback,
			correlation_id = excluded.correlation_id
	`

	_, err := ops.db.Exec(query,
		response.ID, response.RequestID, response.StoryID, response.ResponseType,
		response.FromAgent, response.ToAgent, response.Content, response.Status,
		response.Feedback, response.CreatedAt, response.CorrelationID)
	if err != nil {
		return fmt.Errorf("failed to upsert agent response %s: %w", response.ID, err)
	}
	return nil
}

// UpsertAgentPlan inserts or updates an agent plan record.
func (ops *DatabaseOperations) UpsertAgentPlan(plan *AgentPlan) error {
	// Debug: Log the story_id being used
	if plan.StoryID == "" {
		return fmt.Errorf("cannot upsert agent plan %s: story_id is empty", plan.ID)
	}
	query := `
		INSERT INTO agent_plans (
			id, story_id, from_agent, content, confidence, status,
			created_at, reviewed_at, reviewed_by, feedback
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			story_id = excluded.story_id,
			from_agent = excluded.from_agent,
			content = excluded.content,
			confidence = excluded.confidence,
			status = excluded.status,
			reviewed_at = excluded.reviewed_at,
			reviewed_by = excluded.reviewed_by,
			feedback = excluded.feedback
	`

	_, err := ops.db.Exec(query,
		plan.ID, plan.StoryID, plan.FromAgent, plan.Content, plan.Confidence,
		plan.Status, plan.CreatedAt, plan.ReviewedAt, plan.ReviewedBy, plan.Feedback)
	if err != nil {
		return fmt.Errorf("failed to upsert agent plan %s: %w", plan.ID, err)
	}
	return nil
}

// GetAgentRequestsByStory returns all agent requests for a specific story.
func (ops *DatabaseOperations) GetAgentRequestsByStory(storyID string) ([]*AgentRequest, error) {
	query := `
		SELECT id, story_id, request_type, approval_type, from_agent, to_agent,
		       content, context, reason, created_at, correlation_id, parent_msg_id
		FROM agent_requests
		WHERE story_id = ?
		ORDER BY created_at ASC
	`

	rows, err := ops.db.Query(query, storyID)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent requests for story %s: %w", storyID, err)
	}
	defer func() {
		_ = rows.Close() // Ignore error - operation should not fail due to close error
	}()

	var requests []*AgentRequest
	for rows.Next() {
		var request AgentRequest
		err := rows.Scan(
			&request.ID, &request.StoryID, &request.RequestType, &request.ApprovalType,
			&request.FromAgent, &request.ToAgent, &request.Content, &request.Context,
			&request.Reason, &request.CreatedAt, &request.CorrelationID, &request.ParentMsgID)
		if err != nil {
			return nil, fmt.Errorf("failed to scan agent request: %w", err)
		}
		requests = append(requests, &request)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return requests, nil
}

// GetAgentResponsesByStory returns all agent responses for a specific story.
func (ops *DatabaseOperations) GetAgentResponsesByStory(storyID string) ([]*AgentResponse, error) {
	query := `
		SELECT id, request_id, story_id, response_type, from_agent, to_agent,
		       content, status, feedback, created_at, correlation_id
		FROM agent_responses
		WHERE story_id = ?
		ORDER BY created_at ASC
	`

	rows, err := ops.db.Query(query, storyID)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent responses for story %s: %w", storyID, err)
	}
	defer func() {
		_ = rows.Close() // Ignore error - operation should not fail due to close error
	}()

	var responses []*AgentResponse
	for rows.Next() {
		var response AgentResponse
		err := rows.Scan(
			&response.ID, &response.RequestID, &response.StoryID, &response.ResponseType,
			&response.FromAgent, &response.ToAgent, &response.Content, &response.Status,
			&response.Feedback, &response.CreatedAt, &response.CorrelationID)
		if err != nil {
			return nil, fmt.Errorf("failed to scan agent response: %w", err)
		}
		responses = append(responses, &response)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return responses, nil
}

// GetAgentPlansByStory returns all agent plans for a specific story.
func (ops *DatabaseOperations) GetAgentPlansByStory(storyID string) ([]*AgentPlan, error) {
	query := `
		SELECT id, story_id, from_agent, content, confidence, status,
		       created_at, reviewed_at, reviewed_by, feedback
		FROM agent_plans
		WHERE story_id = ?
		ORDER BY created_at ASC
	`

	rows, err := ops.db.Query(query, storyID)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent plans for story %s: %w", storyID, err)
	}
	defer func() {
		_ = rows.Close() // Ignore error - operation should not fail due to close error
	}()

	var plans []*AgentPlan
	for rows.Next() {
		var plan AgentPlan
		err := rows.Scan(
			&plan.ID, &plan.StoryID, &plan.FromAgent, &plan.Content, &plan.Confidence,
			&plan.Status, &plan.CreatedAt, &plan.ReviewedAt, &plan.ReviewedBy, &plan.Feedback)
		if err != nil {
			return nil, fmt.Errorf("failed to scan agent plan: %w", err)
		}
		plans = append(plans, &plan)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return plans, nil
}

// BatchUpsertStoriesWithDependencies atomically inserts stories and their dependencies.
// This ensures all stories exist before any dependencies are created, preventing foreign key constraint errors.
func (ops *DatabaseOperations) BatchUpsertStoriesWithDependencies(req *BatchUpsertStoriesWithDependenciesRequest) error {
	// Begin transaction for atomicity
	tx, err := ops.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback() // Ignore rollback errors
		}
	}()

	// First, upsert all stories
	storyQuery := `
		INSERT INTO stories (
			id, spec_id, title, content, status, priority, approved_plan,
			created_at, started_at, completed_at, assigned_agent,
			tokens_used, cost_usd, metadata, story_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			spec_id = excluded.spec_id,
			title = excluded.title,
			content = excluded.content,
			status = excluded.status,
			priority = excluded.priority,
			approved_plan = excluded.approved_plan,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			assigned_agent = excluded.assigned_agent,
			tokens_used = excluded.tokens_used,
			cost_usd = excluded.cost_usd,
			metadata = excluded.metadata,
			story_type = excluded.story_type
	`

	for _, story := range req.Stories {
		_, err = tx.Exec(storyQuery,
			story.ID, story.SpecID, story.Title, story.Content, story.Status,
			story.Priority, story.ApprovedPlan, story.CreatedAt, story.StartedAt,
			story.CompletedAt, story.AssignedAgent, story.TokensUsed,
			story.CostUSD, story.Metadata, story.StoryType,
		)
		if err != nil {
			return fmt.Errorf("failed to upsert story %s: %w", story.ID, err)
		}
	}

	// Then, add all dependencies
	depQuery := `
		INSERT OR IGNORE INTO story_dependencies (story_id, depends_on)
		VALUES (?, ?)
	`

	for _, dep := range req.Dependencies {
		_, err = tx.Exec(depQuery, dep.StoryID, dep.DependsOn)
		if err != nil {
			return fmt.Errorf("failed to add dependency %s -> %s: %w", dep.StoryID, dep.DependsOn, err)
		}
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
