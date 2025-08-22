package persistence

import (
	"context"
	"time"

	"orchestrator/pkg/agent/middleware/metrics"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// PersistStory persists a single story to the database with all available data.
// This is a fire-and-forget operation that sends the story to the persistence worker.
func PersistStory(story *Story, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || story == nil {
		return
	}

	// Send to persistence worker (fire-and-forget)
	persistenceChannel <- &Request{
		Operation: OpUpsertStory,
		Data:      story,
		Response:  nil, // Fire-and-forget
	}
}

// PersistStoryStatus persists a story status update with optional metrics data.
// This is used when completing stories and includes token/cost information.
func PersistStoryStatus(storyID string, status string, timestamp time.Time,
	promptTokens, completionTokens *int64, costUSD *float64,
	persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || storyID == "" {
		return
	}

	statusReq := &UpdateStoryStatusRequest{
		StoryID:          storyID,
		Status:           status,
		Timestamp:        timestamp,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CostUSD:          costUSD,
	}

	persistenceChannel <- &Request{
		Operation: OpUpdateStoryStatus,
		Data:      statusReq,
		Response:  nil, // Fire-and-forget
	}
}

// PersistStoryWithMetrics persists a story completion with metrics data retrieved from Prometheus.
// This combines story status update with metrics retrieval in one operation.
func PersistStoryWithMetrics(ctx context.Context, storyID string, status string, timestamp time.Time,
	persistenceChannel chan<- *Request, logger *logx.Logger) {
	if persistenceChannel == nil || storyID == "" {
		return
	}

	// Query metrics and persist
	storyMetrics := queryStoryMetrics(ctx, storyID, logger)
	persistStoryWithMetricsData(storyID, status, timestamp, storyMetrics, persistenceChannel)
}

// queryStoryMetrics retrieves metrics for a story from the internal metrics recorder.
func queryStoryMetrics(_ /* ctx */ context.Context, storyID string, logger *logx.Logger) *metrics.StoryMetrics {
	cfg, err := config.GetConfig()
	if err != nil {
		logWarning(logger, "📊 Failed to get config for metrics query: %v", err)
		return nil
	}

	if !isMetricsConfigured(cfg) {
		logWarning(logger, "📊 Metrics not enabled - skipping metrics query")
		return nil
	}

	logInfo(logger, "📊 Querying internal metrics for completed story %s", storyID)

	// Get the internal metrics recorder (singleton)
	recorder := metrics.NewInternalRecorder()
	storyMetrics := recorder.GetStoryMetrics(storyID)

	if storyMetrics != nil {
		logInfo(logger, "📊 Story %s metrics: prompt tokens: %d, completion tokens: %d, total tokens: %d, total cost: $%.6f",
			storyID, storyMetrics.PromptTokens, storyMetrics.CompletionTokens, storyMetrics.TotalTokens, storyMetrics.TotalCost)
	} else {
		logWarning(logger, "📊 No metrics found for story %s", storyID)
	}

	return storyMetrics
}

// logWarning logs a warning message if logger is not nil.
func logWarning(logger *logx.Logger, format string, args ...interface{}) {
	if logger != nil {
		logger.Warn(format, args...)
	}
}

// logInfo logs an info message if logger is not nil.
func logInfo(logger *logx.Logger, format string, args ...interface{}) {
	if logger != nil {
		logger.Info(format, args...)
	}
}

// isMetricsConfigured checks if metrics are properly configured.
func isMetricsConfigured(cfg config.Config) bool {
	return cfg.Agents != nil && cfg.Agents.Metrics.Enabled
}

// persistStoryWithMetricsData persists story status with metrics data.
func persistStoryWithMetricsData(storyID, status string, timestamp time.Time, storyMetrics *metrics.StoryMetrics, persistenceChannel chan<- *Request) {
	var promptTokens, completionTokens *int64
	var costUSD *float64

	if storyMetrics != nil {
		promptTokens = &storyMetrics.PromptTokens
		completionTokens = &storyMetrics.CompletionTokens
		costUSD = &storyMetrics.TotalCost
	}

	PersistStoryStatus(storyID, status, timestamp, promptTokens, completionTokens, costUSD, persistenceChannel)
}

// PersistSpec persists a single spec to the database.
func PersistSpec(spec *Spec, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || spec == nil {
		return
	}

	persistenceChannel <- &Request{
		Operation: OpUpsertSpec,
		Data:      spec,
		Response:  nil, // Fire-and-forget
	}
}

// PersistAgentPlan persists a single agent plan to the database.
func PersistAgentPlan(plan *AgentPlan, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || plan == nil {
		return
	}

	persistenceChannel <- &Request{
		Operation: OpUpsertAgentPlan,
		Data:      plan,
		Response:  nil, // Fire-and-forget
	}
}

// PersistAgentResponse persists a single agent response to the database.
func PersistAgentResponse(response *AgentResponse, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || response == nil {
		return
	}

	persistenceChannel <- &Request{
		Operation: OpUpsertAgentResponse,
		Data:      response,
		Response:  nil, // Fire-and-forget
	}
}

// PersistDependency persists a single story dependency to the database.
func PersistDependency(storyID, dependsOnID string, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || storyID == "" || dependsOnID == "" {
		return
	}

	dependency := &StoryDependency{
		StoryID:   storyID,
		DependsOn: dependsOnID,
	}

	persistenceChannel <- &Request{
		Operation: OpAddStoryDependency,
		Data:      dependency,
		Response:  nil, // Fire-and-forget
	}
}
