package runrecord_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord/recordtest"
)

func TestMeasuredZeroSurvivesJSONRoundTrip(t *testing.T) {
	raw, err := json.Marshal(runrecord.Measured(0))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back runrecord.Metric
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := back.Float64()
	if !ok || v != 0 {
		t.Fatalf("measured zero lost in round trip: %+v", back)
	}
}

func TestStatusesCarryNoValue(t *testing.T) {
	for _, m := range []runrecord.Metric{
		runrecord.Unsupported(),
		runrecord.NotApplicable(),
		runrecord.Unavailable("target crashed"),
	} {
		if _, ok := m.Float64(); ok {
			t.Fatalf("status %q must not carry a value", m.Status)
		}
	}
}

func TestMetricsValidateCompleteness(t *testing.T) {
	metrics := recordtest.CompleteMetrics()
	delete(metrics, runrecord.MetricToolCalls)
	err := metrics.Validate()
	if err == nil || !strings.Contains(err.Error(), string(runrecord.MetricToolCalls)) {
		t.Fatalf("missing key must fail completeness, got %v", err)
	}
}

func TestMetricsValidateRejectsUnknownKeys(t *testing.T) {
	metrics := recordtest.CompleteMetrics()
	metrics["made_up"] = runrecord.Measured(1)
	err := metrics.Validate()
	if err == nil || !strings.Contains(err.Error(), "made_up") {
		t.Fatalf("unknown key must fail, got %v", err)
	}
}

func TestMetricsValidateValueRules(t *testing.T) {
	cases := []struct {
		name   string
		key    runrecord.MetricKey
		metric runrecord.Metric
	}{
		{"negative", runrecord.MetricCostUSD, runrecord.Measured(-1)},
		{"nan", runrecord.MetricCostUSD, runrecord.Measured(math.NaN())},
		{"infinite", runrecord.MetricCostUSD, runrecord.Measured(math.Inf(1))},
		{"fractional count", runrecord.MetricLLMCalls, runrecord.Measured(1.5)},
		{"value without number", runrecord.MetricLLMCalls, runrecord.Metric{Status: runrecord.StatusValue}},
		{"unsupported with number", runrecord.MetricLLMCalls, func() runrecord.Metric {
			m := runrecord.Measured(1)
			m.Status = runrecord.StatusUnsupported
			return m
		}()},
		{"unknown status", runrecord.MetricLLMCalls, runrecord.Metric{Status: "sideways"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metrics := recordtest.CompleteMetrics()
			metrics[tc.key] = tc.metric
			if err := metrics.Validate(); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestFractionalContinuousMetricsAllowed(t *testing.T) {
	metrics := recordtest.CompleteMetrics()
	metrics[runrecord.MetricCostUSD] = runrecord.Measured(3.14)
	metrics[runrecord.MetricWallClockSeconds] = runrecord.Measured(12.5)
	if err := metrics.Validate(); err != nil {
		t.Fatalf("continuous metrics must accept fractional values: %v", err)
	}
}

func TestUnavailableKeepsRecordValid(t *testing.T) {
	rec := recordtest.Accepted()
	rec.Metrics[runrecord.MetricReviewCycles] = runrecord.Unavailable("log truncated")
	if err := rec.Validate(); err != nil {
		t.Fatalf("unavailable metric must not invalidate a record: %v", err)
	}
}
