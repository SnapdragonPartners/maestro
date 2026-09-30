package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// The four-axis terminal result (ADR 0032 section 5; Phase 3 item 5 design,
// D11), as a validated Go type. The same applicability rule is a set of CHECK
// constraints in migration 000024, so a direct SQL writer cannot store what
// Validate refuses: invalid combinations are unrepresentable in the schema,
// not only refused on one Go path.

// ExecutionStatus is the status axis, always present on a terminal result.
type ExecutionStatus string

// The five statuses.
const (
	ExecutionCompleted ExecutionStatus = "completed"
	ExecutionBlocked   ExecutionStatus = "blocked"
	ExecutionCancelled ExecutionStatus = "cancelled"
	ExecutionTimedOut  ExecutionStatus = "timed_out"
	ExecutionFailed    ExecutionStatus = "failed"
)

// CompletionDisposition applies iff the status is completed.
type CompletionDisposition string

// The two dispositions.
const (
	CompletionChanged          CompletionDisposition = "changed"
	CompletionAlreadySatisfied CompletionDisposition = "already_satisfied"
)

// CancellationReason applies iff the status is cancelled.
type CancellationReason string

// The three reasons.
const (
	CancelledSuperseded        CancellationReason = "superseded"
	CancelledOperatorRequested CancellationReason = "operator_requested"
	CancelledShutdown          CancellationReason = "shutdown"
)

// FailureClass applies iff the status is failed.
type FailureClass string

// The two classes.
const (
	FailureRetryableInfrastructure FailureClass = "retryable_infrastructure"
	FailureNonRetryableAgent       FailureClass = "non_retryable_agent"
)

// ErrTerminalResultInvalid is the sentinel every applicability refusal
// wraps. A result violating the rule is a protocol violation (ADR 0032
// section 5): the boundary synthesizes a failed/non_retryable_agent result
// naming the rule and records THAT; the offending value is never stored.
var ErrTerminalResultInvalid = errors.New("terminal result violates the applicability rule")

// TerminalResult is ADR 0032 section 5's table. Each optional axis applies to
// exactly one status and must be present for it and absent otherwise; the
// blocked reference names the attempt carrying the requirement set.
type TerminalResult struct {
	CompletionDisposition *CompletionDisposition
	CancellationReason    *CancellationReason
	FailureClass          *FailureClass
	BlockedToolCallID     *uuid.UUID

	// ErrorMessage is the diagnostic, required for a failed result -- a
	// synthesized failure names the rule it stands in for -- and forbidden
	// otherwise.
	ErrorMessage string

	Status ExecutionStatus
}

// Validate is the applicability rule.
//
// Every refusal wraps ErrTerminalResultInvalid and names the axis, so the
// synthesized failure the boundary records can quote it. The eight invalid
// shapes the design's test plants -- each axis present on the wrong status,
// each absent on its own -- are each refused by exactly one clause below.
//
//nolint:gocritic // hugeParam: the receiver is the value being validated
func (r TerminalResult) Validate() error {
	switch r.Status {
	case ExecutionCompleted, ExecutionBlocked, ExecutionCancelled, ExecutionTimedOut, ExecutionFailed:
	case "":
		return fmt.Errorf("%w: status is required", ErrTerminalResultInvalid)
	default:
		return fmt.Errorf("%w: %q is not a status", ErrTerminalResultInvalid, r.Status)
	}
	if err := r.checkApplicability(); err != nil {
		return err
	}
	return r.checkVocabularies()
}

// checkApplicability is the rule itself: each axis present iff its status.
//
//nolint:gocritic // hugeParam: see Validate
func (r TerminalResult) checkApplicability() error {
	for _, axis := range []struct {
		name             string
		present, applies bool
	}{
		{"completion_disposition", r.CompletionDisposition != nil, r.Status == ExecutionCompleted},
		{"cancellation_reason", r.CancellationReason != nil, r.Status == ExecutionCancelled},
		{"failure_class", r.FailureClass != nil, r.Status == ExecutionFailed},
		{"blocked_tool_call_id", r.BlockedToolCallID != nil, r.Status == ExecutionBlocked},
		{"error_message", strings.TrimSpace(r.ErrorMessage) != "", r.Status == ExecutionFailed},
	} {
		if err := axisApplies(axis.name, axis.present, axis.applies, r.Status); err != nil {
			return err
		}
	}
	return nil
}

// checkVocabularies refuses a present axis outside its closed set.
//
//nolint:gocritic // hugeParam: see Validate
func (r TerminalResult) checkVocabularies() error {
	if r.CompletionDisposition != nil {
		switch *r.CompletionDisposition {
		case CompletionChanged, CompletionAlreadySatisfied:
		default:
			return fmt.Errorf("%w: %q is not a completion disposition", ErrTerminalResultInvalid, *r.CompletionDisposition)
		}
	}
	if r.CancellationReason != nil {
		switch *r.CancellationReason {
		case CancelledSuperseded, CancelledOperatorRequested, CancelledShutdown:
		default:
			return fmt.Errorf("%w: %q is not a cancellation reason", ErrTerminalResultInvalid, *r.CancellationReason)
		}
	}
	if r.FailureClass != nil {
		switch *r.FailureClass {
		case FailureRetryableInfrastructure, FailureNonRetryableAgent:
		default:
			return fmt.Errorf("%w: %q is not a failure class", ErrTerminalResultInvalid, *r.FailureClass)
		}
	}
	if r.BlockedToolCallID != nil && *r.BlockedToolCallID == uuid.Nil {
		return fmt.Errorf("%w: blocked_tool_call_id is the zero uuid", ErrTerminalResultInvalid)
	}
	return nil
}

func axisApplies(axis string, present, applies bool, status ExecutionStatus) error {
	switch {
	case present && !applies:
		return fmt.Errorf("%w: %s does not apply to status %s and must be absent", ErrTerminalResultInvalid, axis, status)
	case !present && applies:
		return fmt.Errorf("%w: %s applies to status %s and is missing", ErrTerminalResultInvalid, axis, status)
	}
	return nil
}

// DomainState is the resource-domain half of a fence receipt.
type DomainState string

// DomainNoneHeld is item 5's only value: no resource exists to hold, so the
// domain half of every receipt is vacuously satisfied. Item 7 adds the
// values a fenced domain reports.
const DomainNoneHeld DomainState = "none_held"

// FenceReceipt is what a positive terminal result carries (ADR 0032 section
// 6; design D11): the two halves of the obligation, admitted actions
// DRAINED and the resource domain FENCED. The verb accepts the action half
// only when every attempt of the execution has a resolved drain disposition
// -- the caller's claim is checked against the rows, never trusted -- and
// the domain half only with a value it recognises.
type FenceReceipt struct {
	Domain         DomainState
	ActionsDrained bool
}

// Validate refuses a receipt that claims nothing or names a domain state
// this seam does not know.
func (r FenceReceipt) Validate() error {
	if !r.ActionsDrained {
		return fmt.Errorf("%w: the receipt does not claim the actions are drained", ErrReceiptInvalid)
	}
	if r.Domain != DomainNoneHeld {
		return fmt.Errorf("%w: %q is not a domain state this seam records", ErrReceiptInvalid, r.Domain)
	}
	return nil
}

// ErrReceiptInvalid reports a fence receipt the verb will not accept.
var ErrReceiptInvalid = errors.New("fence receipt is not acceptable")
