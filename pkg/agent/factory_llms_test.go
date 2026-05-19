package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	mllms "github.com/SnapdragonPartners/maestro-llms/llms"
	mmw "github.com/SnapdragonPartners/maestro-llms/llms/middleware"

	"orchestrator/pkg/agent/llmerrors"
)

// TestMapSuspend covers the §5 M4 boundary: toolkit terminal errors must map
// onto Maestro's llmerrors.IsServiceUnavailable SUSPEND contract, while
// non-terminal and cancellation errors pass through untouched. Errors are
// wrapped with fmt.Errorf %w to mirror the adapter's real error path.
func TestMapSuspend(t *testing.T) {
	wrap := func(e error) error { return fmt.Errorf("llmadapter: provider call failed: %w", e) }

	tests := []struct {
		name            string
		in              error
		wantSuspend     bool
		wantPassThrough bool // identity preserved (not converted)
	}{
		{name: "nil", in: nil},
		{
			name:        "circuit open",
			in:          wrap(&mmw.CircuitOpenError{Provider: "anthropic", Model: "m"}),
			wantSuspend: true,
		},
		{
			name:        "retryable provider error (rate limited)",
			in:          wrap(&mllms.ProviderError{Provider: "openai", Kind: mllms.ErrorKindRateLimited}),
			wantSuspend: true,
		},
		{
			name:        "retryable provider error (unavailable)",
			in:          wrap(&mllms.ProviderError{Provider: "google", Kind: mllms.ErrorKindUnavailable}),
			wantSuspend: true,
		},
		{
			name:        "limit error always suspends",
			in:          wrap(&mllms.LimitError{Provider: "anthropic", Reason: "tpm"}),
			wantSuspend: true,
		},
		{
			name:            "non-retryable provider error (auth) passes through",
			in:              wrap(&mllms.ProviderError{Provider: "openai", Kind: mllms.ErrorKindAuth}),
			wantPassThrough: true,
		},
		{
			name:            "context canceled passes through",
			in:              fmt.Errorf("aborted: %w", context.Canceled),
			wantPassThrough: true,
		},
		{
			name:            "unknown error passes through",
			in:              errors.New("boom"),
			wantPassThrough: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapSuspend(tc.in)
			if tc.in == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if tc.wantSuspend && !llmerrors.IsServiceUnavailable(got) {
				t.Fatalf("expected ServiceUnavailable, got %T: %v", got, got)
			}
			if tc.wantSuspend && !errors.Is(got, tc.in) {
				t.Fatalf("mapped error must still wrap the cause for errors.Is")
			}
			if tc.wantPassThrough {
				if llmerrors.IsServiceUnavailable(got) {
					t.Fatalf("error should not have been converted to ServiceUnavailable: %v", got)
				}
				if !errors.Is(got, tc.in) {
					t.Fatalf("pass-through must preserve the original error chain")
				}
			}
		})
	}
}

// recordingRecorder captures the last ObserveRequest call.
type recordingRecorder struct {
	storyID                    string
	promptTokens, completionTk int
	cost                       float64
	success                    bool
	calls                      int
}

func (r *recordingRecorder) ObserveRequest(storyID string, p, c int, cost float64, success bool) {
	r.storyID, r.promptTokens, r.completionTk, r.cost, r.success = storyID, p, c, cost, success
	r.calls++
}

// TestMetricsObserver_RecordsUsage verifies the Observer maps toolkit Usage to
// the Recorder (X4: real usage, not estimated) and reports success/failure.
func TestMetricsObserver_RecordsUsage(t *testing.T) {
	rec := &recordingRecorder{}
	obs := &metricsObserver{recorder: rec} // nil stateProvider + logger: must not panic

	obs.Observe(mmw.Event{
		Model:   "claude-haiku-4-5-20251001",
		Latency: 50 * time.Millisecond,
		Usage:   mllms.Usage{InputTokens: 100, OutputTokens: 20},
	})
	if rec.calls != 1 || !rec.success || rec.promptTokens != 100 || rec.completionTk != 20 {
		t.Fatalf("unexpected recorder state after success: %+v", rec)
	}

	obs.Observe(mmw.Event{Model: "m", Err: errors.New("provider down")})
	if rec.calls != 2 || rec.success {
		t.Fatalf("failure event not recorded as failure: %+v", rec)
	}
}
