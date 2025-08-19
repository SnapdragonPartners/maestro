// Package metrics provides Prometheus-based metrics recording for LLM operations.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// PrometheusRecorder implements the Recorder interface using Prometheus metrics.
type PrometheusRecorder struct {
	requestsTotal   *prometheus.CounterVec
	tokensTotal     *prometheus.CounterVec
	costsTotal      *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	throttleTotal   *prometheus.CounterVec
	queueWaitTime   *prometheus.HistogramVec
}

// NewPrometheusRecorder creates a new Prometheus-based metrics recorder.
func NewPrometheusRecorder() *PrometheusRecorder {
	return &PrometheusRecorder{
		requestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_requests_total",
				Help: "Total number of LLM requests by model, story, state, and status",
			},
			[]string{"model", "story_id", "agent_id", "state", "status", "error_type"},
		),
		tokensTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_tokens_total",
				Help: "Total number of tokens used in LLM requests",
			},
			[]string{"model", "story_id", "agent_id", "state", "type"},
		),
		costsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_costs_total",
				Help: "Total cost in USD for LLM requests",
			},
			[]string{"model", "story_id", "agent_id", "state"},
		),
		requestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "llm_request_duration_seconds",
				Help:    "Duration of LLM requests in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"model", "story_id", "agent_id", "state"},
		),
		throttleTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_throttle_total",
				Help: "Total number of LLM throttling events",
			},
			[]string{"model", "reason"},
		),
		queueWaitTime: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "llm_queue_wait_duration_seconds",
				Help:    "Time spent waiting for rate limit availability",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"model"},
		),
	}
}

// ObserveRequest records metrics for a completed LLM request.
func (p *PrometheusRecorder) ObserveRequest(
	model, storyID, agentID, state string,
	promptTokens, completionTokens int,
	cost float64,
	success bool,
	errorType string,
	duration time.Duration,
) {
	// Determine status label
	status := "success"
	if !success {
		status = "error"
	}

	// Record request count
	p.requestsTotal.WithLabelValues(model, storyID, agentID, state, status, errorType).Inc()

	// Record tokens and costs (only on success)
	if success {
		p.tokensTotal.WithLabelValues(model, storyID, agentID, state, "prompt").Add(float64(promptTokens))
		p.tokensTotal.WithLabelValues(model, storyID, agentID, state, "completion").Add(float64(completionTokens))
		p.costsTotal.WithLabelValues(model, storyID, agentID, state).Add(cost)
	}

	// Record request duration
	p.requestDuration.WithLabelValues(model, storyID, agentID, state).Observe(duration.Seconds())
}

// IncThrottle increments the throttle counter for rate limiting events.
func (p *PrometheusRecorder) IncThrottle(model, reason string) {
	p.throttleTotal.WithLabelValues(model, reason).Inc()
}

// ObserveQueueWait records time spent waiting for rate limit availability.
func (p *PrometheusRecorder) ObserveQueueWait(model string, duration time.Duration) {
	p.queueWaitTime.WithLabelValues(model).Observe(duration.Seconds())
}
