// Package metrics provides services for querying and aggregating metrics data.
package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// StoryMetrics represents aggregated metrics for a completed story.
type StoryMetrics struct {
	StoryID          string  `json:"story_id"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	TotalCost        float64 `json:"total_cost_usd"`
}

// QueryService provides methods to query metrics from Prometheus.
type QueryService struct {
	client   api.Client
	queryAPI v1.API
}

// NewQueryService creates a new metrics query service.
func NewQueryService(prometheusURL string) (*QueryService, error) {
	client, err := api.NewClient(api.Config{
		Address: prometheusURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Prometheus client: %w", err)
	}

	return &QueryService{
		client:   client,
		queryAPI: v1.NewAPI(client),
	}, nil
}

// GetStoryMetrics retrieves aggregated token and cost metrics for a specific story.
// This queries Prometheus for all LLM requests associated with the story ID and
// aggregates the results across all agents (architect + coders).
func (q *QueryService) GetStoryMetrics(ctx context.Context, storyID string) (*StoryMetrics, error) {
	metrics := &StoryMetrics{
		StoryID: storyID,
	}

	// Query for prompt tokens
	promptTokensQuery := fmt.Sprintf(`sum(llm_tokens_total{story_id=%q, type="prompt"})`, storyID)
	promptResult, _, err := q.queryAPI.Query(ctx, promptTokensQuery, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query prompt tokens: %w", err)
	}

	if vector, ok := promptResult.(model.Vector); ok && len(vector) > 0 {
		metrics.PromptTokens = int64(vector[0].Value)
	}

	// Query for completion tokens
	completionTokensQuery := fmt.Sprintf(`sum(llm_tokens_total{story_id=%q, type="completion"})`, storyID)
	completionResult, _, err := q.queryAPI.Query(ctx, completionTokensQuery, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query completion tokens: %w", err)
	}

	if vector, ok := completionResult.(model.Vector); ok && len(vector) > 0 {
		metrics.CompletionTokens = int64(vector[0].Value)
	}

	// Calculate total tokens
	metrics.TotalTokens = metrics.PromptTokens + metrics.CompletionTokens

	// Query for total cost
	costQuery := fmt.Sprintf(`sum(llm_costs_total{story_id=%q})`, storyID)
	costResult, _, err := q.queryAPI.Query(ctx, costQuery, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query total cost: %w", err)
	}

	if vector, ok := costResult.(model.Vector); ok && len(vector) > 0 {
		metrics.TotalCost = float64(vector[0].Value)
	}

	return metrics, nil
}

// GetStoryMetricsByModel retrieves detailed metrics broken down by model for a specific story.
// This provides more granular data showing which models were used and their individual costs.
func (q *QueryService) GetStoryMetricsByModel(ctx context.Context, storyID string) (map[string]*StoryMetrics, error) {
	result := make(map[string]*StoryMetrics)

	// Query for all models used in this story
	modelsQuery := fmt.Sprintf(`group by (model) (llm_tokens_total{story_id=%q})`, storyID)
	modelsResult, _, err := q.queryAPI.Query(ctx, modelsQuery, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query models: %w", err)
	}

	// Extract unique model names
	var models []string
	if vector, ok := modelsResult.(model.Vector); ok {
		for _, sample := range vector {
			if modelName, ok := sample.Metric["model"]; ok {
				models = append(models, string(modelName))
			}
		}
	}

	// Get metrics for each model
	for _, modelName := range models {
		metrics := &StoryMetrics{
			StoryID: storyID,
		}

		// Query prompt tokens for this model
		promptQuery := fmt.Sprintf(`sum(llm_tokens_total{story_id=%q, model=%q, type="prompt"})`, storyID, modelName)
		promptResult, _, err := q.queryAPI.Query(ctx, promptQuery, time.Now())
		if err != nil {
			return nil, fmt.Errorf("failed to query prompt tokens for model %s: %w", modelName, err)
		}

		if vector, ok := promptResult.(model.Vector); ok && len(vector) > 0 {
			metrics.PromptTokens = int64(vector[0].Value)
		}

		// Query completion tokens for this model
		completionQuery := fmt.Sprintf(`sum(llm_tokens_total{story_id=%q, model=%q, type="completion"})`, storyID, modelName)
		completionResult, _, err := q.queryAPI.Query(ctx, completionQuery, time.Now())
		if err != nil {
			return nil, fmt.Errorf("failed to query completion tokens for model %s: %w", modelName, err)
		}

		if vector, ok := completionResult.(model.Vector); ok && len(vector) > 0 {
			metrics.CompletionTokens = int64(vector[0].Value)
		}

		// Calculate total tokens
		metrics.TotalTokens = metrics.PromptTokens + metrics.CompletionTokens

		// Query cost for this model
		costQuery := fmt.Sprintf(`sum(llm_costs_total{story_id=%q, model=%q})`, storyID, modelName)
		costResult, _, err := q.queryAPI.Query(ctx, costQuery, time.Now())
		if err != nil {
			return nil, fmt.Errorf("failed to query cost for model %s: %w", modelName, err)
		}

		if vector, ok := costResult.(model.Vector); ok && len(vector) > 0 {
			metrics.TotalCost = float64(vector[0].Value)
		}

		result[modelName] = metrics
	}

	return result, nil
}
