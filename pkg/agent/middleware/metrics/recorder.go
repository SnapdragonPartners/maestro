// Package metrics provides metrics recording for LLM client operations.
package metrics

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"orchestrator/pkg/proto"
)

// StateProvider provides access to agent state for metrics collection.
type StateProvider interface {
	// GetCurrentState returns the agent's current state (PLANNING, CODING, etc).
	GetCurrentState() proto.State
	// GetStoryID returns the current story ID being worked on.
	GetStoryID() string
	// GetID returns the agent ID.
	GetID() string
}

// TokenAxes is one call's token measurement, on the five axes the provider
// reports separately.
//
// The axes are kept apart rather than summed because they are billed apart:
// visible output and reasoning are different rates, and a cache read is not a
// cache write. Folding any pair together is a decision a consumer can make and
// a producer cannot undo.
type TokenAxes struct {
	Input     int64
	Output    int64
	Reasoning int64
	CacheRead int64
	// CacheWrite is recorded even where no provider in use reports it. An
	// all-zero sample does not show the dimension is irrelevant, only that
	// this sample never exercised it.
	CacheWrite int64
}

// Total is the budget-relevant token count: input plus visible output plus
// reasoning.
//
// Cache reads are deliberately excluded. They are recorded, but adding them
// here would change what a declared cap means, and the benchmark's cap
// enforcement reads exactly this number.
//
// The addition is checked because these values arrive from a provider and, on
// the read path, from a file: a wrapped sum is a small positive number that
// looks entirely ordinary.
func (t TokenAxes) Total() (int64, error) {
	total := t.Input
	for _, axis := range [...]int64{t.Output, t.Reasoning} {
		if axis > math.MaxInt64-total {
			return 0, fmt.Errorf("token total overflows int64: input=%d output=%d reasoning=%d",
				t.Input, t.Output, t.Reasoning)
		}
		total += axis
	}
	return total, nil
}

// ErrInvalidObservation reports an observation that cannot be recorded as
// stated. It is a sentinel because the usage log treats it exactly as it
// treats a failed write: accounting integrity is lost either way, and a
// caller that cannot record what happened must not carry on as though it had.
var ErrInvalidObservation = errors.New("invalid usage observation")

// Observation is one completed LLM call as the metrics surface sees it.
//
// It is a struct rather than a parameter list because the list reached seven
// before this change and would have reached thirteen after it. It is also the
// place the surface's rules live, so that a producer and a reader validate the
// same conditions in the same code (design_slice_import.md, D9).
type Observation struct {
	// FinishedAt is the observation instant. There is no StartedAt: the
	// middleware event carries a duration, not a start, so a start would be
	// a derived value stored beside the thing it was derived from.
	FinishedAt time.Time

	// Tokens is nil when the call FAILED, which is not the same as zero.
	// maestro-llms populates usage only when the error is nil -- a partial
	// response returned with an error is not trusted -- so a failed call has
	// no measurement rather than a measurement of nothing. See issue #311.
	Tokens *TokenAxes

	// Cost is nil when the model has no modelled price (local models). Null
	// means not knowable; zero would mean free.
	Cost *float64

	Provider string
	Model    string
	StoryID  string
	AgentID  string

	// Error is the failure text, required when Success is false and forbidden
	// otherwise.
	Error string

	// Latency is the WHOLE LOGICAL CALL: Maestro places the metrics
	// middleware outermost (factory_llms.go), so this folds in validation,
	// every retry and its backoff, per-attempt timeouts, circuit behaviour
	// and rate-limit waiting. Per-attempt latency is not recoverable from
	// this surface; see issue #311.
	Latency time.Duration

	Success bool
}

// Validate enforces the surface's coherence and value rules.
//
// Coherence: a successful call carries a measurement and no error; a failed
// call carries an error and no measurement. A failed call bearing token counts
// is a fabricated measurement, and a successful one bearing an error text is
// two claims.
//
// Values: presence is not validity. Every field a reader will do arithmetic
// on is checked here, because the readers of this surface enforce a budget and
// populate a database, and neither can defend itself against a number that is
// already wrong.
func (o *Observation) Validate() error {
	if strings.TrimSpace(o.Provider) == "" {
		return fmt.Errorf("%w: provider is blank", ErrInvalidObservation)
	}
	if strings.TrimSpace(o.Model) == "" {
		return fmt.Errorf("%w: model is blank", ErrInvalidObservation)
	}
	if o.FinishedAt.IsZero() {
		// Not pedantry: the zero time is year 1, so a zero-valued call sorts
		// before every window any query will ever ask about.
		return fmt.Errorf("%w: finished_at is the zero time", ErrInvalidObservation)
	}
	if o.Latency < 0 {
		// started_at is derived by subtracting this, so a negative duration
		// produces a call that ended before it began.
		return fmt.Errorf("%w: latency %s is negative", ErrInvalidObservation, o.Latency)
	}
	if err := o.validateOutcome(); err != nil {
		return err
	}
	return o.validateCost()
}

// validateOutcome checks the success/error/tokens triangle and the axes.
func (o *Observation) validateOutcome() error {
	switch {
	case o.Success && o.Error != "":
		return fmt.Errorf("%w: successful call carries error %q", ErrInvalidObservation, o.Error)
	case !o.Success && strings.TrimSpace(o.Error) == "":
		return fmt.Errorf("%w: failed call carries no error text", ErrInvalidObservation)
	case o.Success && o.Tokens == nil:
		return fmt.Errorf("%w: successful call carries no token measurement", ErrInvalidObservation)
	case !o.Success && o.Tokens != nil:
		return fmt.Errorf("%w: failed call carries token counts, which the provider never reported",
			ErrInvalidObservation)
	}
	if o.Tokens == nil {
		return nil
	}
	for _, axis := range [...]struct {
		name  string
		value int64
	}{
		{"input", o.Tokens.Input}, {"output", o.Tokens.Output},
		{"reasoning", o.Tokens.Reasoning}, {"cache_read", o.Tokens.CacheRead},
		{"cache_write", o.Tokens.CacheWrite},
	} {
		// Checked per axis, never on the sum. A budget guard downstream sees
		// only the total, so {input: 1000000, output: -999999} would reach it
		// as a delta of 1 and under-account the attempt by two million tokens.
		if axis.value < 0 {
			return fmt.Errorf("%w: %s tokens %d is negative", ErrInvalidObservation, axis.name, axis.value)
		}
	}
	if _, err := o.Tokens.Total(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidObservation, err)
	}
	return nil
}

// validateCost checks the optional cost.
func (o *Observation) validateCost() error {
	if o.Cost == nil {
		return nil
	}
	cost := *o.Cost
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		// A non-finite cost propagates through every sum it reaches, and the
		// plane's numeric column cannot store it at all.
		return fmt.Errorf("%w: cost %v is not finite", ErrInvalidObservation, cost)
	}
	if cost < 0 {
		return fmt.Errorf("%w: cost %v is negative", ErrInvalidObservation, cost)
	}
	return nil
}

// Recorder defines the interface for recording LLM operation metrics.
type Recorder interface {
	// ObserveCall records one completed LLM call. The internal recorder
	// aggregates by story; the usage-log recorder writes the durable
	// per-call surface the benchmark adapter reads (P-1, raised to v2 by
	// docs/v2/phase_2/design_slice_import.md).
	ObserveCall(observation *Observation)
}

// NoopRecorder implements Recorder with no-op behavior for when metrics are disabled.
type NoopRecorder struct{}

// Nop returns a no-op metrics recorder that discards all metrics.
func Nop() Recorder {
	return &NoopRecorder{}
}

// ObserveCall does nothing in the no-op recorder.
func (n *NoopRecorder) ObserveCall(_ *Observation) {
	// No-op
}
