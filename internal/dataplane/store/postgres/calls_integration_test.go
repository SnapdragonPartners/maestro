//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// The call family through the PUBLIC seam.
//
// The existing suites exercise the generated statements; these exercise
// what a caller can actually reach, which is where the seam's own rules
// live — the once-only classification, the rejection matrix, the money
// conversions, and the provenance translation.

// openLLMCall creates a plain open call and returns it.
func openLLMCall(t *testing.T, f *fixture) *store.LLMCall {
	t.Helper()
	call, err := f.store.CreateLLMCall(context.Background(), store.CreateLLMCallInput{
		Provider:            "anthropic",
		Model:               "opus",
		OrganizationID:      f.organizationID,
		PrincipalInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create llm call: %v", err)
	}
	return call
}

func openToolCall(t *testing.T, f *fixture) *store.ToolCall {
	t.Helper()
	call, err := f.store.CreateToolCall(context.Background(), store.CreateToolCallInput{
		ToolName:            "shell",
		Arguments:           json.RawMessage(`{"cmd":"ls"}`),
		OrganizationID:      f.organizationID,
		PrincipalInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create tool call: %v", err)
	}
	return call
}

// TestCallIsCreatedOpenThenCompletedExactlyOnce is design D1 end to end: a
// call is born open, the first completion wins, and a REPEAT — including a
// contradictory one — receives the winner's whole recorded outcome rather
// than an error.
func TestCallIsCreatedOpenThenCompletedExactlyOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	call := openLLMCall(t, f)

	if call.FinishedAt != nil || call.Succeeded != nil || call.Cost != nil {
		t.Fatalf("a created call is not open: finished=%v succeeded=%v cost=%v",
			call.FinishedAt, call.Succeeded, call.Cost)
	}

	cost := store.MustParseUSD("1.23456789")
	winner, err := f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
		OrganizationID: f.organizationID,
		LLMCallID:      call.LLMCallID,
		Succeeded:      true,
		Tokens:         &store.TokenCounts{Input: 11, Output: 22, Reasoning: 33, CacheRead: 44, CacheWrite: 7},
		Cost:           &cost,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !winner.Recorded {
		t.Error("the first completion reports Recorded=false")
	}
	if winner.Call.Cost == nil || winner.Call.Cost.String() != "1.23456789" {
		t.Errorf("winner recorded cost %v, want 1.23456789", winner.Call.Cost)
	}
	if winner.Call.FinishedAt == nil {
		t.Error("a completed call has no finished_at")
	}

	// A repeat that CONTRADICTS the winner, and would not even validate on
	// its own: succeeded=false demands a diagnostic and this one carries
	// none. It is still a repeat, so it must return the recorded outcome.
	loserCost := store.MustParseUSD("999.00000000")
	loser, err := f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
		OrganizationID: f.organizationID,
		LLMCallID:      call.LLMCallID,
		Succeeded:      true,
		Tokens:         &store.TokenCounts{Input: 1, Output: 2},
		Cost:           &loserCost,
	})
	if err != nil {
		t.Fatalf("a conflicting repeat errored instead of returning the recorded outcome: %v", err)
	}
	if loser.Recorded {
		t.Error("the second completion reports Recorded=true")
	}
	if loser.Call.Succeeded == nil || !*loser.Call.Succeeded {
		t.Errorf("repeat returned succeeded=%v, want the winner's true", loser.Call.Succeeded)
	}
	// The whole outcome, not just the flags: tokens and cost are what a
	// losing caller needs to know were not its own.
	if loser.Call.Tokens == nil || *loser.Call.Tokens != (store.TokenCounts{Input: 11, Output: 22, Reasoning: 33, CacheRead: 44, CacheWrite: 7}) {
		t.Errorf("repeat returned tokens %+v, want the winner's", loser.Call.Tokens)
	}
	if loser.Call.Cost == nil || loser.Call.Cost.String() != "1.23456789" {
		t.Errorf("repeat returned cost %v, want the winner's 1.23456789", loser.Call.Cost)
	}

	stored, err := f.store.GetLLMCall(ctx, f.organizationID, call.LLMCallID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Cost == nil || stored.Cost.String() != "1.23456789" {
		t.Errorf("stored cost is %v; the loser overwrote the winner", stored.Cost)
	}
}

// TestToolCallCompletionReturnsItsResult is the same contract on the other
// table, where the payload a repeat must be told about is the result rather
// than the cost.
func TestToolCallCompletionReturnsItsResult(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	call := openToolCall(t, f)

	winner, err := f.store.CompleteToolCall(ctx, store.CompleteToolCallInput{
		OrganizationID: f.organizationID,
		ToolCallID:     call.ToolCallID,
		Succeeded:      true,
		Result:         json.RawMessage(`{"exit":0}`),
	})
	if err != nil {
		t.Fatalf("complete tool call: %v", err)
	}
	if !winner.Recorded || !strings.Contains(string(winner.Call.Result), `"exit"`) {
		t.Fatalf("winner recorded=%v result=%s", winner.Recorded, winner.Call.Result)
	}

	message := "killed"
	loser, err := f.store.CompleteToolCall(ctx, store.CompleteToolCallInput{
		OrganizationID: f.organizationID,
		ToolCallID:     call.ToolCallID,
		Succeeded:      false,
		ErrorMessage:   &message,
	})
	if err != nil {
		t.Fatalf("repeat errored: %v", err)
	}
	if loser.Recorded {
		t.Error("the second tool completion reports Recorded=true")
	}
	if !strings.Contains(string(loser.Call.Result), `"exit"`) {
		t.Errorf("repeat returned result %s, want the winner's", loser.Call.Result)
	}
	if loser.Call.ErrorMessage != nil {
		t.Errorf("repeat returned error message %q; the winner succeeded", *loser.Call.ErrorMessage)
	}
}

// TestUnmeasuredCostStaysNull covers the load-bearing null. A completed
// call whose cost is not knowable — paired-local's local models — must not
// come back as zero, which is a real measurement.
func TestUnmeasuredCostStaysNull(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	call := openLLMCall(t, f)

	outcome, err := f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
		OrganizationID: f.organizationID,
		LLMCallID:      call.LLMCallID,
		Succeeded:      true,
		Tokens:         &store.TokenCounts{Input: 5},
	})
	if err != nil {
		t.Fatalf("complete without cost: %v", err)
	}
	if outcome.Call.Cost != nil {
		t.Errorf("an unmeasured cost came back as %v, want nil", outcome.Call.Cost)
	}
	stored, err := f.store.GetLLMCall(ctx, f.organizationID, call.LLMCallID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Cost != nil {
		t.Errorf("stored cost is %v, want NULL", stored.Cost)
	}
}

// TestCompletionRejections is the rejection matrix on the completion path.
// Each case is checked only while the row is still OPEN, which is why every
// case gets its own freshly created call.
func TestCompletionRejections(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	blank := "  \t\n "
	message := "provider timeout"
	past := time.Now().Add(-24 * time.Hour)

	for _, testCase := range []struct {
		name  string
		input store.CompleteLLMCallInput
		want  string
	}{
		{
			name:  "success carrying an error message",
			input: store.CompleteLLMCallInput{Succeeded: true, ErrorMessage: &message},
			want:  "must not carry an error message",
		},
		{
			// The schema requires error_message IS NULL for a success, so
			// an EMPTY string is a value the column refuses. Absence is a
			// nil pointer; a seam that treated "" as absence would pass the
			// row to a constraint name.
			name:  "success carrying an empty error message",
			input: store.CompleteLLMCallInput{Succeeded: true, ErrorMessage: pointerTo("")},
			want:  "must not carry an error message at all",
		},
		{
			name:  "success carrying a whitespace-only error message",
			input: store.CompleteLLMCallInput{Succeeded: true, ErrorMessage: &blank},
			want:  "must not carry an error message at all",
		},
		{
			name:  "failure with no diagnostic",
			input: store.CompleteLLMCallInput{Succeeded: false},
			want:  "must carry a non-blank diagnostic",
		},
		{
			name:  "failure whose diagnostic is only whitespace",
			input: store.CompleteLLMCallInput{Succeeded: false, ErrorMessage: &blank},
			want:  "must carry a non-blank diagnostic",
		},
		{
			// A failed call has NO measurement: the provider layer reports
			// usage only on success, so counts here were invented, and every
			// aggregate downstream would sum them as though measured.
			name: "failure carrying token counts",
			input: store.CompleteLLMCallInput{
				Succeeded: false, ErrorMessage: &message,
				Tokens: &store.TokenCounts{Input: 10},
			},
			want: "must not carry token counts",
		},
		{
			// And the other direction: a success without a measurement is a
			// caller dropping one it had.
			name:  "success with no measurement",
			input: store.CompleteLLMCallInput{Succeeded: true},
			want:  "requires a token measurement",
		},
		{
			name:  "negative token counter",
			input: store.CompleteLLMCallInput{Succeeded: true, Tokens: &store.TokenCounts{Reasoning: -1}},
			want:  "reasoning_tokens is -1",
		},
		{
			name:  "negative cache write counter",
			input: store.CompleteLLMCallInput{Succeeded: true, Tokens: &store.TokenCounts{CacheWrite: -1}},
			want:  "cache_write_tokens is -1",
		},
		{
			name:  "finishing before it started",
			input: store.CompleteLLMCallInput{Succeeded: true, Tokens: &store.TokenCounts{}, FinishedAt: &past},
			want:  "not an interval",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			call := openLLMCall(t, f)
			input := testCase.input
			input.OrganizationID = f.organizationID
			input.LLMCallID = call.LLMCallID

			_, err := f.store.CompleteLLMCall(ctx, input)
			if err == nil {
				t.Fatalf("completion was accepted; want a rejection mentioning %q", testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("error %q does not mention %q", err, testCase.want)
			}

			// The row must still be open: a refused completion that had
			// already written would make the guard cosmetic.
			stored, readErr := f.store.GetLLMCall(ctx, f.organizationID, call.LLMCallID)
			if readErr != nil {
				t.Fatalf("read back: %v", readErr)
			}
			if stored.FinishedAt != nil {
				t.Error("the refused completion still closed the call")
			}
		})
	}
}

// TestCreationRejections covers the names and lineage rules on the write
// path. Blank rather than empty: the schema's own check learned this the
// hard way, since one-argument btrim strips spaces only.
func TestCreationRejections(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	story := f.seedLineage(t)
	epicOnly := store.Lineage{StoryID: &story}

	t.Run("blank provider", func(t *testing.T) {
		_, err := f.store.CreateLLMCall(ctx, store.CreateLLMCallInput{
			Provider: " \t ", Model: "opus",
			OrganizationID: f.organizationID, PrincipalInstanceID: f.author,
		})
		requireRejection(t, err, "provider is blank")
	})

	t.Run("blank model", func(t *testing.T) {
		_, err := f.store.CreateLLMCall(ctx, store.CreateLLMCallInput{
			Provider: "anthropic", Model: "\n",
			OrganizationID: f.organizationID, PrincipalInstanceID: f.author,
		})
		requireRejection(t, err, "model is blank")
	})

	t.Run("lineage with a gap", func(t *testing.T) {
		_, err := f.store.CreateLLMCall(ctx, store.CreateLLMCallInput{
			Provider: "anthropic", Model: "opus", Lineage: epicOnly,
			OrganizationID: f.organizationID, PrincipalInstanceID: f.author,
		})
		requireRejection(t, err, "names a Story but no Epic")
	})

	t.Run("blank tool name", func(t *testing.T) {
		_, err := f.store.CreateToolCall(ctx, store.CreateToolCallInput{
			ToolName:       "  ",
			OrganizationID: f.organizationID, PrincipalInstanceID: f.author,
		})
		requireRejection(t, err, "tool_name is blank")
	})

	t.Run("malformed arguments", func(t *testing.T) {
		_, err := f.store.CreateToolCall(ctx, store.CreateToolCallInput{
			ToolName: "shell", Arguments: json.RawMessage(`{"unterminated":`),
			OrganizationID: f.organizationID, PrincipalInstanceID: f.author,
		})
		requireRejection(t, err, "arguments is not valid JSON")
	})

	t.Run("blank metric name", func(t *testing.T) {
		_, err := f.store.CreateMetricEvent(ctx, store.MetricEvent{
			MetricName: "\v", Value: 1, OrganizationID: f.organizationID,
		})
		requireRejection(t, err, "metric_name is blank")
	})

	t.Run("non-finite metric value", func(t *testing.T) {
		for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			_, err := f.store.CreateMetricEvent(ctx, store.MetricEvent{
				MetricName: "tokens", Value: value, OrganizationID: f.organizationID,
			})
			requireRejection(t, err, "non-finite value")
		}
	})

	t.Run("blank event type", func(t *testing.T) {
		_, err := f.store.CreateAuditEvent(ctx, store.AuditEvent{
			EventType: " ", OrganizationID: f.organizationID,
		})
		requireRejection(t, err, "event_type is blank")
	})
}

func pointerTo[T any](value T) *T { return &value }

// TestToolCompletionCoherenceCoversBothCallTypes: the coherence rule is one
// helper, but each call table has its own constraint, and a test on one
// says nothing about the other.
func TestToolCompletionCoherenceCoversBothCallTypes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for _, testCase := range []struct {
		name  string
		input store.CompleteToolCallInput
		want  string
	}{
		{
			name:  "success carrying an empty error message",
			input: store.CompleteToolCallInput{Succeeded: true, ErrorMessage: pointerTo("")},
			want:  "must not carry an error message at all",
		},
		{
			name:  "success carrying a real error message",
			input: store.CompleteToolCallInput{Succeeded: true, ErrorMessage: pointerTo("boom")},
			want:  "must not carry an error message at all",
		},
		{
			name:  "failure whose diagnostic is only whitespace",
			input: store.CompleteToolCallInput{Succeeded: false, ErrorMessage: pointerTo(" \t ")},
			want:  "must carry a non-blank diagnostic",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			call := openToolCall(t, f)
			input := testCase.input
			input.OrganizationID = f.organizationID
			input.ToolCallID = call.ToolCallID

			_, err := f.store.CompleteToolCall(ctx, input)
			requireRejection(t, err, testCase.want)

			stored, readErr := f.store.GetToolCall(ctx, f.organizationID, call.ToolCallID)
			if readErr != nil {
				t.Fatalf("read back: %v", readErr)
			}
			if stored.FinishedAt != nil {
				t.Error("the refused completion still closed the call")
			}
		})
	}
}

// TestDefaultCompletionInstantIsValidated closes the gap a defaulted
// finished_at left open.
//
// started_at is caller-supplied and may be in the future. If the seam let
// SQL default finished_at to now(), the interval rule would be enforced by
// the schema's constraint rather than by the seam — the caller would read
// llm_calls_interval_check instead of a sentence. So the seam materialises
// the same instant SQL would have used, validates THAT, and stores it.
func TestDefaultCompletionInstantIsValidated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	future := time.Now().Add(2 * time.Hour)

	call, err := f.store.CreateLLMCall(ctx, store.CreateLLMCallInput{
		Provider: "anthropic", Model: "opus", StartedAt: &future,
		OrganizationID: f.organizationID, PrincipalInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create call started in the future: %v", err)
	}

	// No FinishedAt: the default applies, and it precedes started_at. The
	// measurement is supplied so the rejection under test is the interval
	// one, not the availability rule that runs before it.
	_, err = f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
		OrganizationID: f.organizationID, LLMCallID: call.LLMCallID, Succeeded: true,
		Tokens: &store.TokenCounts{},
	})
	requireRejection(t, err, "not an interval")
	if strings.Contains(err.Error(), "interval_check") {
		t.Errorf("the diagnostic came from the schema constraint, not the seam: %v", err)
	}

	// And the ordinary case still stores an instant, taken from the
	// database's clock rather than this process's.
	ordinary := openLLMCall(t, f)
	outcome, err := f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
		OrganizationID: f.organizationID, LLMCallID: ordinary.LLMCallID, Succeeded: true,
		Tokens: &store.TokenCounts{},
	})
	if err != nil {
		t.Fatalf("ordinary completion: %v", err)
	}
	if outcome.Call.FinishedAt == nil || outcome.Call.FinishedAt.Before(outcome.Call.StartedAt) {
		t.Errorf("stored finished_at %v does not follow started_at %v",
			outcome.Call.FinishedAt, outcome.Call.StartedAt)
	}
}

func requireRejection(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the write was accepted; want a rejection mentioning %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %q", err, want)
	}
}

// TestEventsAreWritten is the positive control for the rejections above: a
// suite that only refuses things passes against a seam that refuses
// everything.
func TestEventsAreWritten(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	metric, err := f.store.CreateMetricEvent(ctx, store.MetricEvent{
		MetricName:          "tokens",
		Value:               42,
		Labels:              json.RawMessage(`{"agent":"coder"}`),
		OrganizationID:      f.organizationID,
		PrincipalInstanceID: &f.author,
	})
	if err != nil {
		t.Fatalf("create metric event: %v", err)
	}
	if metric.MetricEventID == uuid.Nil || metric.RecordedAt.IsZero() {
		t.Errorf("metric event came back without an identity or a timestamp: %+v", metric)
	}

	event, err := f.store.CreateAuditEvent(ctx, store.AuditEvent{
		EventType:      "agent.started",
		OrganizationID: f.organizationID,
	})
	if err != nil {
		t.Fatalf("create audit event: %v", err)
	}
	// Absent JSON becomes the column's empty object rather than failing or
	// writing null.
	if string(event.Detail) != "{}" {
		t.Errorf("audit event detail is %s, want {}", event.Detail)
	}
	if event.OccurredAt.IsZero() {
		t.Error("audit event has no occurred_at; the zero time should have become now()")
	}
}

// TestProvenanceIsTranslated covers design D3. The composite key stays
// authoritative and the seam translates its refusal — generically, because
// the constraint cannot tell its four causes apart.
func TestProvenanceIsTranslated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	story := f.seedLineage(t)
	lineage := store.Lineage{
		ProductID: &f.product, FeatureID: &f.feature, EpicID: &f.epic, StoryID: &story,
	}

	parent, err := f.store.CreateLLMCall(ctx, store.CreateLLMCallInput{
		Provider: "anthropic", Model: "opus", Lineage: lineage,
		UserID:         &f.userID,
		OrganizationID: f.organizationID, PrincipalInstanceID: f.author,
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// The claim the key permits: same principal, same user, same work.
	if _, err := f.store.CreateToolCall(ctx, store.CreateToolCallInput{
		ToolName: "shell", LLMCallID: &parent.LLMCallID, Lineage: lineage,
		UserID:         &f.userID,
		OrganizationID: f.organizationID, PrincipalInstanceID: f.author,
	}); err != nil {
		t.Fatalf("a valid provenance claim was refused: %v", err)
	}

	missing := uuid.New()
	for _, testCase := range []struct {
		name  string
		input store.CreateToolCallInput
	}{
		{
			name: "parent does not exist",
			input: store.CreateToolCallInput{
				LLMCallID: &missing, Lineage: lineage, UserID: &f.userID,
				PrincipalInstanceID: f.author,
			},
		},
		{
			name: "parent belongs to another principal",
			input: store.CreateToolCallInput{
				LLMCallID: &parent.LLMCallID, Lineage: lineage, UserID: &f.userID,
				PrincipalInstanceID: f.reviewer,
			},
		},
		{
			name: "parent was made for different work",
			input: store.CreateToolCallInput{
				LLMCallID:           &parent.LLMCallID,
				Lineage:             store.Lineage{ProductID: &f.product},
				UserID:              &f.userID,
				PrincipalInstanceID: f.author,
			},
		},
		{
			// lineage_key folds user_id in, so this is the accountable-user
			// mismatch the key exists to catch.
			name: "parent names a different accountable user",
			input: store.CreateToolCallInput{
				LLMCallID: &parent.LLMCallID, Lineage: lineage,
				PrincipalInstanceID: f.author,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := testCase.input
			input.ToolName = "shell"
			input.OrganizationID = f.organizationID

			_, err := f.store.CreateToolCall(ctx, input)
			if !errors.Is(err, store.ErrInvalidProvenance) {
				t.Fatalf("error %v is not an ErrInvalidProvenance", err)
			}
			// It reports what was CLAIMED and does not pretend to know
			// which of the four causes applied.
			if !strings.Contains(err.Error(), "claimed llm call") {
				t.Errorf("error %q does not report the claim", err)
			}
		})
	}
}

// TestAggregateReportsExactCostAndCompleteness runs the aggregate through
// the seam, so the money conversion is part of what is under test rather
// than something the row-level suite happens to bypass.
func TestAggregateReportsExactCostAndCompleteness(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Hour)

	// Two measured calls whose sum needs all eight fractional digits, one
	// completed-but-unmeasured, one failed, and one still open.
	for _, text := range []string{"0.00000001", "1.23456789"} {
		cost := store.MustParseUSD(text)
		call := openLLMCall(t, f)
		if _, err := f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
			OrganizationID: f.organizationID, LLMCallID: call.LLMCallID,
			Succeeded: true, Cost: &cost,
			Tokens: &store.TokenCounts{Input: 10, Output: 20, Reasoning: 30, CacheRead: 40, CacheWrite: 5},
		}); err != nil {
			t.Fatalf("complete %s: %v", text, err)
		}
	}
	// Cost-unmeasured but TOKEN-measured: the paired-local shape, and the
	// case that proves the two availability axes are not the same axis.
	unmeasured := openLLMCall(t, f)
	if _, err := f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
		OrganizationID: f.organizationID, LLMCallID: unmeasured.LLMCallID, Succeeded: true,
		Tokens: &store.TokenCounts{Input: 1, Output: 1, Reasoning: 1, CacheRead: 1, CacheWrite: 1},
	}); err != nil {
		t.Fatalf("complete unmeasured: %v", err)
	}
	failed := openLLMCall(t, f)
	message := "provider refused"
	if _, err := f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
		OrganizationID: f.organizationID, LLMCallID: failed.LLMCallID,
		Succeeded: false, ErrorMessage: &message,
	}); err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	openLLMCall(t, f)

	aggregate, err := f.store.AggregateCost(ctx, f.organizationID, "anthropic", "opus", from, to)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if got := aggregate.TotalCost.String(); got != "1.23456790" {
		t.Errorf("total is %s, want 1.23456790 — the eighth digit is exactly what float64 loses", got)
	}
	if aggregate.MeasuredCalls != 2 || aggregate.UnmeasuredCalls != 2 || aggregate.OpenCalls != 1 {
		t.Errorf("completeness is measured=%d unmeasured=%d open=%d, want 2/2/1",
			aggregate.MeasuredCalls, aggregate.UnmeasuredCalls, aggregate.OpenCalls)
	}
	if aggregate.SucceededCalls != 3 || aggregate.FailedCalls != 1 {
		t.Errorf("outcomes are succeeded=%d failed=%d, want 3/1",
			aggregate.SucceededCalls, aggregate.FailedCalls)
	}
	if aggregate.SucceededCalls+aggregate.FailedCalls != aggregate.MeasuredCalls+aggregate.UnmeasuredCalls {
		t.Error("outcome counts and cost-availability counts describe different populations")
	}
	// Each axis totalled from its OWN column: the two cache axes carry
	// different values on purpose, so a rollup reading the wrong one cannot
	// pass this.
	if aggregate.Tokens != (store.TokenCounts{Input: 21, Output: 41, Reasoning: 61, CacheRead: 81, CacheWrite: 11}) {
		t.Errorf("token totals are %+v, want 21/41/61/81/11", aggregate.Tokens)
	}
	// Token availability is a SEPARATE axis from cost availability. Three
	// completed calls carry a measurement (two priced, one unpriced); the
	// failed one carries none, because usage is reported only on success.
	if aggregate.TokensMeasuredCalls != 3 || aggregate.TokensUnmeasuredCalls != 1 {
		t.Errorf("token completeness is measured=%d unmeasured=%d, want 3/1",
			aggregate.TokensMeasuredCalls, aggregate.TokensUnmeasuredCalls)
	}
	// And the axes genuinely differ on this population: folding them into
	// one pair would have to lose one of these two facts.
	if aggregate.MeasuredCalls == aggregate.TokensMeasuredCalls {
		t.Error("cost and token availability agree here by accident; the fixture no longer " +
			"distinguishes the two axes, so this test would pass with them folded together")
	}
}

// TestEmptyCohortIsZeroWithNoCalls: the total of nothing is exactly zero,
// and the counts are what distinguishes it from a cohort that genuinely
// cost nothing. A caller reading the total alone cannot tell them apart,
// which is why completeness is reported beside it.
func TestEmptyCohortIsZeroWithNoCalls(t *testing.T) {
	f := newFixture(t)
	aggregate, err := f.store.AggregateCost(context.Background(), f.organizationID,
		"anthropic", "a-model-nobody-called", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if !aggregate.TotalCost.IsZero() {
		t.Errorf("empty cohort total is %s, want zero", aggregate.TotalCost)
	}
	if aggregate.MeasuredCalls != 0 || aggregate.UnmeasuredCalls != 0 || aggregate.OpenCalls != 0 {
		t.Errorf("empty cohort reports calls: %+v", aggregate)
	}
}

// TestAggregateRejectsAnUnstatedPopulation: an aggregate must describe a
// stated cohort in a stated window, or it describes "everything that
// happens to be retained".
func TestAggregateRejectsAnUnstatedPopulation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()

	for _, testCase := range []struct {
		name            string
		provider, model string
		from, to        time.Time
		want            string
	}{
		{"blank provider", " ", "opus", now.Add(-time.Hour), now, "provider is blank"},
		{"blank model", "anthropic", "\t", now.Add(-time.Hour), now, "model is blank"},
		{"inverted window", "anthropic", "opus", now, now.Add(-time.Hour), "is not after"},
		{"empty window", "anthropic", "opus", now, now, "is not after"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := f.store.AggregateCost(ctx, f.organizationID,
				testCase.provider, testCase.model, testCase.from, testCase.to)
			requireRejection(t, err, testCase.want)
		})
	}
}

// TestListsAreBounded covers the page contract at the seam. The SQL suite
// already proves the cursors page correctly; this proves a caller cannot
// ask for an unbounded scan, and that a cursor missing its tie-breaker is
// refused rather than silently returning an empty page.
func TestListsAreBounded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		openLLMCall(t, f)
	}
	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)

	for _, testCase := range []struct {
		name string
		page store.Page
		want string
	}{
		{"no limit", store.Page{}, "not positive"},
		{"negative limit", store.Page{Limit: -1}, "not positive"},
		{"limit past the maximum", store.Page{Limit: store.MaxPageLimit + 1}, "exceeds the maximum"},
		{
			name: "cursor without its tie-breaker",
			page: store.Page{Limit: 10, After: &store.Cursor{At: time.Now()}},
			want: "the id is the tie-breaker",
		},
		{
			// The other half of the cursor, failing the opposite way: the
			// zero time precedes every row, so this pages from the start
			// again rather than resuming — and a caller looping until a
			// short page never terminates.
			name: "cursor without its timestamp",
			page: store.Page{Limit: 10, After: &store.Cursor{ID: uuid.New()}},
			want: "paging would restart from the beginning",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := f.store.ListLLMCallsInWindow(ctx, f.organizationID, from, to, testCase.page)
			requireRejection(t, err, testCase.want)
		})
	}

	// Positive control, and proof the limit is applied rather than merely
	// validated: three rows exist, two are asked for.
	page, err := f.store.ListLLMCallsInWindow(ctx, f.organizationID, from, to, store.Page{Limit: 2})
	if err != nil {
		t.Fatalf("bounded list: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("limit 2 returned %d rows", len(page))
	}
	next, err := f.store.ListLLMCallsInWindow(ctx, f.organizationID, from, to, store.Page{
		Limit: 2,
		After: &store.Cursor{At: page[1].StartedAt, ID: page[1].LLMCallID},
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(next) != 1 {
		t.Errorf("second page returned %d rows, want the remaining 1", len(next))
	}
}

// TestCallsAreOrganizationScoped: another tenant's identifiers are neither
// readable nor probeable, and the two are deliberately indistinguishable.
func TestCallsAreOrganizationScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	call := openLLMCall(t, f)

	if _, err := f.store.GetLLMCall(ctx, f.otherOrgID, call.LLMCallID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-tenant read returned %v, want ErrNotFound", err)
	}
	if _, err := f.store.CompleteLLMCall(ctx, store.CompleteLLMCallInput{
		OrganizationID: f.otherOrgID, LLMCallID: call.LLMCallID, Succeeded: true,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-tenant completion returned %v, want ErrNotFound", err)
	}
	stored, err := f.store.GetLLMCall(ctx, f.organizationID, call.LLMCallID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.FinishedAt != nil {
		t.Error("the cross-tenant completion closed the call anyway")
	}
}
