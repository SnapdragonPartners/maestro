// Package runrecord defines the normalized run-record contract shared by
// every target adapter: four-state metrics, the metric key registry,
// verdicts, failure kinds, the target descriptor with its MPH identity,
// evidence pointers, and the isolation block (ADR 0025).
//
// The package is pure types plus validation; it performs no I/O.
package runrecord

import (
	"fmt"
	"math"
	"sort"
)

// MetricStatus says whether a metric carries a value and, if not, why.
type MetricStatus string

// Metric statuses: ADR 0025's tri-state plus "unavailable". Missing is
// never zero — and never missing: every registry key must appear in a
// record's metrics map with one of these statuses.
const (
	// StatusValue means the metric was measured and Value is meaningful.
	StatusValue MetricStatus = "value"
	// StatusUnsupported means this target cannot report the metric at all.
	StatusUnsupported MetricStatus = "unsupported"
	// StatusNotApplicable means this story does not exercise the metric.
	StatusNotApplicable MetricStatus = "not_applicable"
	// StatusUnavailable means the target supports the metric but it could
	// not be collected on this attempt (target crash, truncated logs).
	StatusUnavailable MetricStatus = "unavailable"
)

// Metric is one normalized metric observation. Value is a pointer so a
// measured zero survives JSON round-trips.
type Metric struct {
	Value  *float64     `json:"value,omitempty"`
	Status MetricStatus `json:"status"`
	Reason string       `json:"reason,omitempty"`
}

// Measured returns a metric carrying the measured value v.
func Measured(v float64) Metric {
	return Metric{Status: StatusValue, Value: &v}
}

// Unsupported returns a metric this target can never report.
func Unsupported() Metric {
	return Metric{Status: StatusUnsupported}
}

// NotApplicable returns a metric this story does not exercise.
func NotApplicable() Metric {
	return Metric{Status: StatusNotApplicable}
}

// Unavailable returns a supported metric that could not be collected on
// this attempt, with a human-readable reason.
func Unavailable(reason string) Metric {
	return Metric{Status: StatusUnavailable, Reason: reason}
}

// Float64 returns the measured value and whether one is present.
func (m Metric) Float64() (float64, bool) {
	if m.Status == StatusValue && m.Value != nil {
		return *m.Value, true
	}
	return 0, false
}

// validate checks status/value coherence; integral requires a whole number.
func (m Metric) validate(integral bool) error {
	switch m.Status {
	case StatusValue:
		if m.Value == nil {
			return fmt.Errorf("status %q requires a value", StatusValue)
		}
		v := *m.Value
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("value must be finite, got %v", v)
		}
		if v < 0 {
			return fmt.Errorf("value must be nonnegative, got %v", v)
		}
		if integral && v != math.Trunc(v) {
			return fmt.Errorf("count metric requires an integral value, got %v", v)
		}
	case StatusUnsupported, StatusNotApplicable, StatusUnavailable:
		if m.Value != nil {
			return fmt.Errorf("status %q must not carry a value", m.Status)
		}
	default:
		return fmt.Errorf("unknown metric status %q", m.Status)
	}
	return nil
}

// MetricKey names one normalized metric in the registry.
type MetricKey string

// The metric key registry (ADR 0025's numeric per-run metrics).
const (
	// MetricTokensTotal is total tokens consumed across all LLM calls.
	MetricTokensTotal MetricKey = "tokens_total"
	// MetricCostUSD is the total attempt cost in US dollars.
	MetricCostUSD MetricKey = "cost_usd"
	// MetricWallClockSeconds is attempt wall-clock duration in seconds.
	MetricWallClockSeconds MetricKey = "wall_clock_seconds"
	// MetricLLMCalls is the number of LLM calls made.
	MetricLLMCalls MetricKey = "llm_calls"
	// MetricToolCalls is the number of tool calls made.
	MetricToolCalls MetricKey = "tool_calls"
	// MetricIterations is the number of toolloop/workflow iterations.
	MetricIterations MetricKey = "iterations"
	// MetricReviewCycles is the number of review round-trips.
	MetricReviewCycles MetricKey = "review_cycles"
	// MetricSelfRepairCycles is the number of self-repair cycles.
	MetricSelfRepairCycles MetricKey = "self_repair_cycles"
	// MetricHumanInterventions is the number of human interventions.
	MetricHumanInterventions MetricKey = "human_interventions"
	// MetricHumanAttentionSeconds is wall-clock time blocked on a person.
	MetricHumanAttentionSeconds MetricKey = "human_attention_seconds"
)

// MetricSpec describes one registry entry.
type MetricSpec struct {
	Key MetricKey
	// Integral marks count-kind metrics whose values must be whole numbers.
	Integral bool
}

// Registry returns the full metric key registry in canonical order.
func Registry() []MetricSpec {
	return []MetricSpec{
		{Key: MetricTokensTotal, Integral: true},
		{Key: MetricCostUSD, Integral: false},
		{Key: MetricWallClockSeconds, Integral: false},
		{Key: MetricLLMCalls, Integral: true},
		{Key: MetricToolCalls, Integral: true},
		{Key: MetricIterations, Integral: true},
		{Key: MetricReviewCycles, Integral: true},
		{Key: MetricSelfRepairCycles, Integral: true},
		{Key: MetricHumanInterventions, Integral: true},
		{Key: MetricHumanAttentionSeconds, Integral: false},
	}
}

// Metrics is a complete map of registry keys to observations.
type Metrics map[MetricKey]Metric

// Validate enforces the completeness rule: every registry key present with
// a coherent status, and no keys outside the registry.
func (m Metrics) Validate() error {
	known := make(map[MetricKey]bool, len(Registry()))
	for _, spec := range Registry() {
		known[spec.Key] = true
		metric, ok := m[spec.Key]
		if !ok {
			return fmt.Errorf("metric %q missing: every registry key must be present as one of value, unsupported, not_applicable, or unavailable", spec.Key)
		}
		if err := metric.validate(spec.Integral); err != nil {
			return fmt.Errorf("metric %q: %w", spec.Key, err)
		}
	}
	var unknown []string
	for key := range m {
		if !known[key] {
			unknown = append(unknown, string(key))
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown metric keys %v: the registry is the only namespace", unknown)
	}
	return nil
}
