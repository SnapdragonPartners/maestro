package persistence

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/knowledge"
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

	// Tool execution logging operations.
	OpInsertToolExecution = "insert_tool_execution"

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
	OpGetRecentMessages                  = "get_recent_messages"
	OpBatchUpsertStoriesWithDependencies = "batch_upsert_stories_with_dependencies"

	// Chat operations.
	OpPostChatMessage  = "post_chat_message"
	OpGetChatMessages  = "get_chat_messages"
	OpGetChatCursor    = "get_chat_cursor"
	OpUpdateChatCursor = "update_chat_cursor"

	// Knowledge graph operations.
	OpStoreKnowledgePack     = "store_knowledge_pack"
	OpRetrieveKnowledgePack  = "retrieve_knowledge_pack"
	OpCheckKnowledgeModified = "check_knowledge_modified"
	OpRebuildKnowledgeIndex  = "rebuild_knowledge_index"

	// Agent state checkpoint operations (for resume support).
	OpCheckpointArchitectState = "checkpoint_architect_state"
	OpCheckpointPMState        = "checkpoint_pm_state"

	// Context checkpoint operations (for error debugging).
	OpSaveAgentContext = "save_agent_context"
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

// StoreKnowledgePackRequest represents a request to store a knowledge pack for a story.
type StoreKnowledgePackRequest struct {
	StoryID     string `json:"story_id"`
	SessionID   string `json:"session_id"`
	Subgraph    string `json:"subgraph"`
	SearchTerms string `json:"search_terms"`
	NodeCount   int    `json:"node_count"`
}

// RetrieveKnowledgePackRequest represents a request to retrieve a knowledge pack.
type RetrieveKnowledgePackRequest struct {
	SessionID   string `json:"session_id"`
	SearchTerms string `json:"search_terms"`
	Level       string `json:"level"`       // Filter by level: "architecture", "implementation", or "all"
	MaxResults  int    `json:"max_results"` // Maximum nodes to return
	Depth       int    `json:"depth"`       // Neighbor depth
}

// RetrieveKnowledgePackResponse represents the response from knowledge retrieval.
type RetrieveKnowledgePackResponse struct {
	Subgraph string `json:"subgraph"` // DOT format subgraph
	Count    int    `json:"count"`    // Number of nodes in result
}

// CheckKnowledgeModifiedRequest represents a request to check if knowledge.dot was modified.
type CheckKnowledgeModifiedRequest struct {
	DotPath string `json:"dot_path"` // Path to knowledge.dot file
}

// RebuildKnowledgeIndexRequest represents a request to rebuild the knowledge index.
type RebuildKnowledgeIndexRequest struct {
	DotPath   string `json:"dot_path"`   // Path to knowledge.dot file
	SessionID string `json:"session_id"` // Session ID for isolation
}

// CheckpointArchitectStateRequest contains all data needed to checkpoint architect state.
type CheckpointArchitectStateRequest struct {
	State    *ArchitectState // Main architect state
	Contexts []*AgentContext // Per-agent conversation contexts
}

// CheckpointPMStateRequest contains all data needed to checkpoint PM state.
type CheckpointPMStateRequest struct {
	State   *PMState      // Main PM state
	Context *AgentContext // PM's conversation context (single 'main' context)
}

// DatabaseOperations provides methods for database operations.
// This is used by the orchestrator's database worker goroutine.
// All database operations automatically inject and filter by session_id for session isolation.
type DatabaseOperations struct {
	db        *sql.DB
	sessionID string // Current orchestrator session ID (injected into all writes, filtered in all reads)
}

// NewDatabaseOperations creates a new DatabaseOperations instance with session isolation.
// The sessionID parameter is automatically injected into all write operations and used to filter all read operations.
// This ensures that database queries are isolated by orchestrator session.
func NewDatabaseOperations(db *sql.DB, sessionID string) *DatabaseOperations {
	return &DatabaseOperations{
		db:        db,
		sessionID: sessionID,
	}
}

// UpsertSpec inserts or updates a spec record.
func (ops *DatabaseOperations) UpsertSpec(spec *Spec) error {
	query := `
		INSERT INTO specs (id, session_id, content, created_at, processed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			processed_at = excluded.processed_at
	`

	_, err := ops.db.Exec(query, spec.ID, ops.sessionID, spec.Content, spec.CreatedAt, spec.ProcessedAt)
	if err != nil {
		return fmt.Errorf("failed to upsert spec %s: %w", spec.ID, err)
	}
	return nil
}

// UpsertStory inserts or updates a story record.
func (ops *DatabaseOperations) UpsertStory(story *Story) error {
	query := `
		INSERT INTO stories (
			id, session_id, spec_id, title, content, status, priority, approved_plan,
			created_at, started_at, completed_at, assigned_agent,
			tokens_used, cost_usd, metadata, story_type, pr_id, commit_hash, completion_summary
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		story.ID, ops.sessionID, story.SpecID, story.Title, story.Content, story.Status,
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

	// Add WHERE clause with session_id filtering
	args = append(args, req.StoryID, ops.sessionID)

	//nolint:gosec // Using safe string concatenation for dynamic query building with bounded inputs
	query := `UPDATE stories SET ` + strings.Join(setParts, ", ") + ` WHERE id = ? AND session_id = ?`

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
	query := "SELECT id, spec_id, title, content, status, priority, approved_plan, created_at, started_at, completed_at, assigned_agent, tokens_used, cost_usd, metadata FROM stories WHERE session_id = ?"
	args := []interface{}{ops.sessionID}

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
	query := `
		SELECT DISTINCT s.id, s.spec_id, s.title, s.content, s.status, s.priority,
		       s.approved_plan, s.created_at, s.started_at, s.completed_at,
		       s.assigned_agent, s.tokens_used, s.cost_usd, s.metadata
		FROM stories s
		LEFT JOIN story_dependencies d ON s.id = d.story_id
		LEFT JOIN stories dep ON d.depends_on = dep.id
		    AND dep.status NOT IN (?)
		WHERE s.session_id = ? AND s.status = ? AND dep.id IS NULL
		ORDER BY s.priority DESC, s.created_at ASC
	`
	return ops.queryStoriesBySQLWithArgs(query, []interface{}{StatusDone, ops.sessionID, StatusNew}, "pending stories")
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
		WHERE session_id = ? AND spec_id = ?
		GROUP BY spec_id
	`

	summary := &SpecSummary{SpecID: specID}
	err := ops.db.QueryRow(query, ops.sessionID, specID).Scan(
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
		FROM stories WHERE session_id = ? AND id = ?
	`

	story := &Story{}
	err := ops.db.QueryRow(query, ops.sessionID, storyID).Scan(
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
	query := `SELECT id, content, created_at, processed_at FROM specs WHERE session_id = ? AND id = ?`

	spec := &Spec{}
	err := ops.db.QueryRow(query, ops.sessionID, specID).Scan(
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

// queryStoriesBySQLWithArgs is a helper function to execute story queries with arguments and scan results.
func (ops *DatabaseOperations) queryStoriesBySQLWithArgs(query string, args []interface{}, queryType string) ([]*Story, error) {
	rows, err := ops.db.Query(query, args...)
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
	query := `
		SELECT id, spec_id, title, content, status, priority, approved_plan,
		       created_at, started_at, completed_at, assigned_agent,
		       tokens_used, cost_usd, metadata
		FROM stories WHERE session_id = ? ORDER BY priority DESC, created_at ASC
	`
	return ops.queryStoriesBySQLWithArgs(query, []interface{}{ops.sessionID}, "all stories")
}

// UpsertAgentRequest inserts or updates an agent request record.
func (ops *DatabaseOperations) UpsertAgentRequest(request *AgentRequest) error {
	query := `
		INSERT INTO agent_requests (
			id, session_id, story_id, request_type, approval_type, from_agent, to_agent,
			content, context, reason, created_at, correlation_id, parent_msg_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		request.ID, ops.sessionID, request.StoryID, request.RequestType, request.ApprovalType,
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
			id, session_id, request_id, story_id, response_type, from_agent, to_agent,
			content, status, created_at, correlation_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			request_id = excluded.request_id,
			story_id = excluded.story_id,
			response_type = excluded.response_type,
			from_agent = excluded.from_agent,
			to_agent = excluded.to_agent,
			content = excluded.content,
			status = excluded.status,
			correlation_id = excluded.correlation_id
	`

	_, err := ops.db.Exec(query,
		response.ID, ops.sessionID, response.RequestID, response.StoryID, response.ResponseType,
		response.FromAgent, response.ToAgent, response.Content, response.Status,
		response.CreatedAt, response.CorrelationID)
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
			id, session_id, story_id, from_agent, content, confidence, status,
			created_at, reviewed_at, reviewed_by, feedback
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		plan.ID, ops.sessionID, plan.StoryID, plan.FromAgent, plan.Content, plan.Confidence,
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
		WHERE session_id = ? AND story_id = ?
		ORDER BY created_at ASC
	`

	rows, err := ops.db.Query(query, ops.sessionID, storyID)
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
//
//nolint:dupl // Similar structure to GetAgentPlansByStory but handles different table/types
func (ops *DatabaseOperations) GetAgentResponsesByStory(storyID string) ([]*AgentResponse, error) {
	query := `
		SELECT id, request_id, story_id, response_type, from_agent, to_agent,
		       content, status, created_at, correlation_id
		FROM agent_responses
		WHERE session_id = ? AND story_id = ?
		ORDER BY created_at ASC
	`

	rows, err := ops.db.Query(query, ops.sessionID, storyID)
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
			&response.CreatedAt, &response.CorrelationID)
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
//
//nolint:dupl // Similar structure to GetAgentResponsesByStory but handles different table/types
func (ops *DatabaseOperations) GetAgentPlansByStory(storyID string) ([]*AgentPlan, error) {
	query := `
		SELECT id, story_id, from_agent, content, confidence, status,
		       created_at, reviewed_at, reviewed_by, feedback
		FROM agent_plans
		WHERE session_id = ? AND story_id = ?
		ORDER BY created_at ASC
	`

	rows, err := ops.db.Query(query, ops.sessionID, storyID)
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
			id, session_id, spec_id, title, content, status, priority, approved_plan,
			created_at, started_at, completed_at, assigned_agent,
			tokens_used, cost_usd, metadata, story_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			story.ID, ops.sessionID, story.SpecID, story.Title, story.Content, story.Status,
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

// RecentMessage represents a message (request or response) for the message viewer.
type RecentMessage struct {
	RequestType  *string // For requests: "question" or "approval"
	ApprovalType *string // For approval requests: "plan", "code", "budget_review", "completion"
	ResponseType *string // For responses: "answer" or "result"
	Status       *string // For responses: "APPROVED", "REJECTED", "NEEDS_CHANGES", "PENDING"
	Reason       *string // For requests: reason for the request
	ID           string
	Type         string // "REQUEST" or "RESPONSE"
	FromAgent    string
	ToAgent      string
	StoryID      *string // Can be NULL for messages not associated with a story (e.g., PM spec requests)
	CreatedAt    string
	Content      string
}

// GetRecentMessages returns the most recent agent requests and responses across all stories.
func (ops *DatabaseOperations) GetRecentMessages(limit int) ([]*RecentMessage, error) {
	query := `
		SELECT
			id,
			'REQUEST' as type,
			from_agent,
			to_agent,
			story_id,
			created_at,
			request_type,
			approval_type,
			NULL as response_type,
			NULL as status,
			content,
			reason
		FROM agent_requests
		WHERE session_id = ?
		UNION ALL
		SELECT
			id,
			'RESPONSE' as type,
			from_agent,
			to_agent,
			story_id,
			created_at,
			NULL as request_type,
			NULL as approval_type,
			response_type,
			status,
			content,
			NULL as reason
		FROM agent_responses
		WHERE session_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := ops.db.Query(query, ops.sessionID, ops.sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent messages: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var messages []*RecentMessage
	for rows.Next() {
		var msg RecentMessage
		err := rows.Scan(
			&msg.ID, &msg.Type, &msg.FromAgent, &msg.ToAgent, &msg.StoryID, &msg.CreatedAt,
			&msg.RequestType, &msg.ApprovalType, &msg.ResponseType, &msg.Status,
			&msg.Content, &msg.Reason,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, &msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return messages, nil
}

// GetAllStoriesWithDependencies returns all stories with their dependencies populated.
func (ops *DatabaseOperations) GetAllStoriesWithDependencies() ([]*Story, error) {
	// First get all stories
	stories, err := ops.GetAllStories()
	if err != nil {
		return nil, err
	}

	// Then fetch dependencies for each story
	for _, story := range stories {
		deps, err := ops.GetStoryDependencies(story.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get dependencies for story %s: %w", story.ID, err)
		}
		story.DependsOn = deps
	}

	return stories, nil
}

// PostChatMessage inserts a new chat message and returns the assigned ID.
func (ops *DatabaseOperations) PostChatMessage(author, text, timestamp string) (int64, error) {
	// Default to development channel, regular chat message with no reply
	return ops.PostChatMessageWithType(author, text, timestamp, "development", nil, "chat")
}

// PostChatMessageWithType posts a chat message with channel, optional reply_to and post_type.
func (ops *DatabaseOperations) PostChatMessageWithType(author, text, timestamp, channel string, replyTo *int64, postType string) (int64, error) {
	query := `
		INSERT INTO chat (session_id, channel, ts, author, text, reply_to, post_type)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	result, err := ops.db.Exec(query, ops.sessionID, channel, timestamp, author, text, replyTo, postType)
	if err != nil {
		return 0, fmt.Errorf("failed to insert chat message: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}

	return id, nil
}

// GetChatMessages returns all chat messages with id > sinceID for the current session.
func (ops *DatabaseOperations) GetChatMessages(sinceID int64) ([]*ChatMessage, error) {
	query := `
		SELECT id, session_id, channel, ts, author, text, reply_to, post_type
		FROM chat
		WHERE session_id = ? AND id > ?
		ORDER BY id ASC
	`

	rows, err := ops.db.Query(query, ops.sessionID, sinceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query chat messages: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var messages []*ChatMessage
	for rows.Next() {
		var msg ChatMessage
		err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Channel, &msg.Timestamp, &msg.Author, &msg.Text, &msg.ReplyTo, &msg.PostType)
		if err != nil {
			return nil, fmt.Errorf("failed to scan chat message: %w", err)
		}
		messages = append(messages, &msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return messages, nil
}

// GetChatMessageByReplyTo returns the first message that replies to the specified message ID.
// Returns sql.ErrNoRows if no reply is found.
func (ops *DatabaseOperations) GetChatMessageByReplyTo(messageID int64) (*ChatMessage, error) {
	query := `
		SELECT id, session_id, channel, ts, author, text, reply_to, post_type
		FROM chat
		WHERE session_id = ? AND reply_to = ?
		ORDER BY id ASC
		LIMIT 1
	`

	var msg ChatMessage
	err := ops.db.QueryRow(query, ops.sessionID, messageID).Scan(
		&msg.ID, &msg.SessionID, &msg.Channel, &msg.Timestamp, &msg.Author, &msg.Text, &msg.ReplyTo, &msg.PostType,
	)

	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows // Return unwrapped sentinel error
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan reply message: %w", err)
	}

	return &msg, nil
}

// GetChatCursor returns the last read message ID for an agent on a specific channel.
func (ops *DatabaseOperations) GetChatCursor(agentID, channel string) (int64, error) {
	query := `SELECT last_id FROM chat_cursor WHERE agent_id = ? AND channel = ? AND session_id = ?`

	var lastID int64
	err := ops.db.QueryRow(query, agentID, channel, ops.sessionID).Scan(&lastID)
	if err == sql.ErrNoRows {
		// No cursor found - return 0 (will read all messages)
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get chat cursor for agent %s channel %s: %w", agentID, channel, err)
	}

	return lastID, nil
}

// UpdateChatCursor updates the last read message ID for an agent on a specific channel.
func (ops *DatabaseOperations) UpdateChatCursor(agentID, channel string, lastID int64) error {
	query := `
		INSERT INTO chat_cursor (agent_id, channel, session_id, last_id)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_id, channel, session_id) DO UPDATE SET last_id = excluded.last_id
	`

	_, err := ops.db.Exec(query, agentID, channel, ops.sessionID, lastID)
	if err != nil {
		return fmt.Errorf("failed to update chat cursor for agent %s channel %s: %w", agentID, channel, err)
	}

	return nil
}

// InsertToolExecution inserts a tool execution record for debugging and analysis.
func (ops *DatabaseOperations) InsertToolExecution(toolExec *ToolExecution) error {
	query := `
		INSERT INTO tool_executions (
			session_id, agent_id, story_id, tool_name, tool_id, params,
			exit_code, success, stdout, stderr, error, duration_ms, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := ops.db.Exec(query,
		ops.sessionID, toolExec.AgentID, toolExec.StoryID, toolExec.ToolName,
		toolExec.ToolID, toolExec.Params, toolExec.ExitCode, toolExec.Success,
		toolExec.Stdout, toolExec.Stderr, toolExec.Error, toolExec.DurationMS,
		toolExec.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert tool execution for agent %s: %w", toolExec.AgentID, err)
	}

	return nil
}

// StoreKnowledgePack stores a knowledge pack for a story.
func (ops *DatabaseOperations) StoreKnowledgePack(req *StoreKnowledgePackRequest) error {
	query := `
		INSERT OR REPLACE INTO knowledge_packs (
			story_id, session_id, subgraph, search_terms, node_count,
			created_at, last_used
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	now := time.Now()
	_, err := ops.db.Exec(query,
		req.StoryID, ops.sessionID, req.Subgraph, req.SearchTerms, req.NodeCount,
		now, now)
	if err != nil {
		return fmt.Errorf("failed to store knowledge pack for story %s: %w", req.StoryID, err)
	}

	return nil
}

// RetrieveKnowledgePack retrieves a knowledge pack using the knowledge retrieval system.
func (ops *DatabaseOperations) RetrieveKnowledgePack(req *RetrieveKnowledgePackRequest) (*RetrieveKnowledgePackResponse, error) {
	// Use the knowledge package to retrieve the pack
	result, err := knowledge.Retrieve(ops.db, ops.sessionID, knowledge.RetrievalOptions{
		Terms:      req.SearchTerms,
		Level:      req.Level,
		MaxResults: req.MaxResults,
		Depth:      req.Depth,
	})

	if err != nil {
		return nil, fmt.Errorf("knowledge retrieval failed: %w", err)
	}

	return &RetrieveKnowledgePackResponse{
		Subgraph: result.Subgraph,
		Count:    result.Count,
	}, nil
}

// CheckKnowledgeModified checks if the knowledge graph file has been modified since last index.
func (ops *DatabaseOperations) CheckKnowledgeModified(req *CheckKnowledgeModifiedRequest) (bool, error) {
	modified, err := knowledge.IsGraphModified(ops.db, req.DotPath)
	if err != nil {
		return false, fmt.Errorf("failed to check knowledge modification: %w", err)
	}
	return modified, nil
}

// RebuildKnowledgeIndex rebuilds the knowledge graph index from the DOT file.
func (ops *DatabaseOperations) RebuildKnowledgeIndex(req *RebuildKnowledgeIndexRequest) error {
	if err := knowledge.RebuildIndex(ops.db, req.DotPath, req.SessionID); err != nil {
		return fmt.Errorf("failed to rebuild knowledge index: %w", err)
	}
	return nil
}
