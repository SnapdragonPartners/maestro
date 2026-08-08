// Package metrics provides internal metrics tracking for LLM operations.
package metrics

import (
	"sync"
	"time"
)

// InternalRecorder implements the Recorder interface using in-memory aggregation.
// This is much simpler than Prometheus and doesn't require external services.
type InternalRecorder struct {
	stories map[string]*StoryMetrics // storyID -> aggregated metrics
	mu      sync.RWMutex
}

// StoryMetrics represents aggregated metrics for a story.
//
//nolint:govet
type StoryMetrics struct {
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	RequestCount     int64     `json:"request_count"`
	TotalCost        float64   `json:"total_cost_usd"`
	StoryID          string    `json:"story_id"`
	LastUpdated      time.Time `json:"last_updated"`
}

var (
	// Singleton instance and initialization synchronization.
	internalInstance *InternalRecorder //nolint:gochecknoglobals
	internalOnce     sync.Once         //nolint:gochecknoglobals
)

// NewInternalRecorder returns a singleton internal metrics recorder.
func NewInternalRecorder() *InternalRecorder {
	internalOnce.Do(func() {
		internalInstance = &InternalRecorder{
			stories: make(map[string]*StoryMetrics),
		}
	})
	return internalInstance
}

// ObserveCall records metrics for a completed LLM request. Provider, agent
// and model are unused here (the usage-log recorder consumes them); the story
// aggregates below are unchanged in meaning.
//
// CompletionTokens keeps its established meaning for these aggregates --
// visible output plus reasoning -- so handleWorkAccepted's existing consumers
// see the same numbers they saw before the surface split the axes apart.
func (r *InternalRecorder) ObserveCall(observation *Observation) {
	// Only successful requests carry a measurement to aggregate; a failed
	// call has no token counts at all (see Observation.Tokens).
	storyID := observation.StoryID
	if !observation.Success || storyID == "" || observation.Tokens == nil {
		return
	}
	promptTokens := observation.Tokens.Input
	completionTokens := observation.Tokens.Output + observation.Tokens.Reasoning
	var cost float64
	if observation.Cost != nil {
		cost = *observation.Cost
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Get or create story metrics
	story, exists := r.stories[storyID]
	if !exists {
		story = &StoryMetrics{
			StoryID: storyID,
		}
		r.stories[storyID] = story
	}

	// Update aggregated metrics
	story.PromptTokens += promptTokens
	story.CompletionTokens += completionTokens
	story.TotalTokens = story.PromptTokens + story.CompletionTokens
	story.TotalCost += cost
	story.RequestCount++
	story.LastUpdated = time.Now()
}

// GetStoryMetrics returns the aggregated metrics for a specific story.
func (r *InternalRecorder) GetStoryMetrics(storyID string) *StoryMetrics {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if story, exists := r.stories[storyID]; exists {
		// Return a copy to prevent external modification
		return &StoryMetrics{
			StoryID:          story.StoryID,
			PromptTokens:     story.PromptTokens,
			CompletionTokens: story.CompletionTokens,
			TotalTokens:      story.TotalTokens,
			TotalCost:        story.TotalCost,
			RequestCount:     story.RequestCount,
			LastUpdated:      story.LastUpdated,
		}
	}
	return nil
}

// GetAllStoryMetrics returns metrics for all stories.
func (r *InternalRecorder) GetAllStoryMetrics() map[string]*StoryMetrics {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*StoryMetrics)
	for storyID, story := range r.stories {
		result[storyID] = &StoryMetrics{
			StoryID:          story.StoryID,
			PromptTokens:     story.PromptTokens,
			CompletionTokens: story.CompletionTokens,
			TotalTokens:      story.TotalTokens,
			TotalCost:        story.TotalCost,
			RequestCount:     story.RequestCount,
			LastUpdated:      story.LastUpdated,
		}
	}
	return result
}

// ClearStoryMetrics removes metrics for a specific story (useful for testing).
func (r *InternalRecorder) ClearStoryMetrics(storyID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.stories, storyID)
}

// Reset clears all metrics (useful for testing).
func (r *InternalRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stories = make(map[string]*StoryMetrics)
}
