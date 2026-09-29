package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func ptr[T any](v T) *T { return &v }

// TestTerminalResultValidateRefusesEveryInvalidShape is the Go half of
// design D11's "invalid combinations are unrepresentable": the eight shapes
// the schema's CHECKs refuse (migration 000024's test plants them) are each
// refused here too, and every status has one valid shape that passes.
func TestTerminalResultValidateRefusesEveryInvalidShape(t *testing.T) {
	attempt := uuid.New()
	for _, tc := range []struct {
		name   string
		result TerminalResult
	}{
		{"no status", TerminalResult{}},
		{"a status outside the five", TerminalResult{Status: "done"}},
		{"completed without a disposition", TerminalResult{Status: ExecutionCompleted}},
		{"a disposition on a cancelled result", TerminalResult{Status: ExecutionCancelled, CancellationReason: ptr(CancelledShutdown), CompletionDisposition: ptr(CompletionChanged)}},
		{"cancelled without a reason", TerminalResult{Status: ExecutionCancelled}},
		{"a reason on a completed result", TerminalResult{Status: ExecutionCompleted, CompletionDisposition: ptr(CompletionChanged), CancellationReason: ptr(CancelledShutdown)}},
		{"failed without a class", TerminalResult{Status: ExecutionFailed, ErrorMessage: "x"}},
		{"a class on a timed-out result", TerminalResult{Status: ExecutionTimedOut, FailureClass: ptr(FailureNonRetryableAgent)}},
		{"blocked without its attempt", TerminalResult{Status: ExecutionBlocked}},
		{"an attempt on a completed result", TerminalResult{Status: ExecutionCompleted, CompletionDisposition: ptr(CompletionChanged), BlockedToolCallID: &attempt}},
		{"failed with no diagnostic", TerminalResult{Status: ExecutionFailed, FailureClass: ptr(FailureRetryableInfrastructure)}},
		{"failed with a blank diagnostic", TerminalResult{Status: ExecutionFailed, FailureClass: ptr(FailureRetryableInfrastructure), ErrorMessage: " \t"}},
		{"a diagnostic on a completed result", TerminalResult{Status: ExecutionCompleted, CompletionDisposition: ptr(CompletionChanged), ErrorMessage: "x"}},
		{"a disposition outside the two", TerminalResult{Status: ExecutionCompleted, CompletionDisposition: ptr(CompletionDisposition("partial"))}},
		{"a reason outside the three", TerminalResult{Status: ExecutionCancelled, CancellationReason: ptr(CancellationReason("bored"))}},
		{"a class outside the two", TerminalResult{Status: ExecutionFailed, FailureClass: ptr(FailureClass("fatal")), ErrorMessage: "x"}},
		{"a zero attempt id", TerminalResult{Status: ExecutionBlocked, BlockedToolCallID: ptr(uuid.Nil)}},
	} {
		if err := tc.result.Validate(); !errors.Is(err, ErrTerminalResultInvalid) {
			t.Errorf("%s: Validate = %v, want ErrTerminalResultInvalid", tc.name, err)
		}
	}
	for _, ok := range []TerminalResult{
		{Status: ExecutionCompleted, CompletionDisposition: ptr(CompletionAlreadySatisfied)},
		{Status: ExecutionBlocked, BlockedToolCallID: &attempt},
		{Status: ExecutionCancelled, CancellationReason: ptr(CancelledSuperseded)},
		{Status: ExecutionTimedOut},
		{Status: ExecutionFailed, FailureClass: ptr(FailureNonRetryableAgent), ErrorMessage: "protocol violation: completion_disposition is missing"},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("the valid %s shape was refused: %v", ok.Status, err)
		}
	}
}

func TestFenceReceiptValidate(t *testing.T) {
	if err := (FenceReceipt{Domain: DomainNoneHeld}).Validate(); !errors.Is(err, ErrReceiptInvalid) {
		t.Errorf("a receipt not claiming drained actions: %v", err)
	}
	if err := (FenceReceipt{ActionsDrained: true, Domain: "fenced"}).Validate(); !errors.Is(err, ErrReceiptInvalid) {
		t.Errorf("a receipt naming a domain state this seam does not know: %v", err)
	}
	if err := (FenceReceipt{ActionsDrained: true, Domain: DomainNoneHeld}).Validate(); err != nil {
		t.Errorf("the item 5 receipt was refused: %v", err)
	}
}
