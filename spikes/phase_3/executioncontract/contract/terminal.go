package contract

import "fmt"

// The terminal result of ADR 0032 §5: four INDEPENDENT axes, not one status
// list. v1 demonstrates the failure being avoided -- claude.Signal puts an
// error, a timeout, an already-satisfied completion, a pending question, and a
// container-switch request in one enum, so every consumer switches over a set
// whose members are not alternatives to one another.

// ExecutionStatus is axis 1. Always present, exactly one.
type ExecutionStatus string

const (
	StatusCompleted ExecutionStatus = "completed"
	StatusBlocked   ExecutionStatus = "blocked"
	StatusCancelled ExecutionStatus = "cancelled"
	StatusTimedOut  ExecutionStatus = "timed_out"
	StatusFailed    ExecutionStatus = "failed"
)

// CompletionDisposition is axis 2. Required iff status is completed.
//
// already_satisfied is a COMPLETION: the execution did its job and the work was
// already done. Recording it as a distinct status would make a successful
// execution look unsuccessful, which is issue #280 arriving from the opposite
// error.
type CompletionDisposition string

const (
	DispositionChanged          CompletionDisposition = "changed"
	DispositionAlreadySatisfied CompletionDisposition = "already_satisfied"
)

// CancellationReason is axis 3. Required iff status is cancelled.
//
// The axis is extensible; `superseded` belongs to A5, which owns when a
// cancellation is legitimate and what it does to pending actions. What this
// contract fixes is that amendment terminates work as cancelled rather than
// failed.
type CancellationReason string

const (
	ReasonSuperseded        CancellationReason = "superseded"
	ReasonOperatorRequested CancellationReason = "operator_requested"
	ReasonShutdown          CancellationReason = "shutdown"
)

// FailureClass is axis 4. Required iff status is failed or timed_out.
//
// It applies to timed_out because a timeout is either kind: a slow provider is
// infrastructure, an agent looping is not. timed_out stays a STATUS rather than
// becoming a failure class because a deadline is an Orchestrator-observed fact
// while an error is a runtime-reported one, and they are retried differently.
type FailureClass string

const (
	ClassRetryableInfrastructure FailureClass = "retryable_infrastructure"
	ClassNonRetryableAgent       FailureClass = "non_retryable_agent"
)

// RequirementRef points at ADR 0030 §3's structured requirement set. `blocked`
// deliberately carries NO reason enum of its own: inventing one here would
// duplicate candidate 12's vocabulary in a second place and let the two drift.
type RequirementRef struct {
	AttemptID string `json:"attempt_id"`
	GateID    string `json:"gate_id"`
	Statement string `json:"statement"`
}

// TerminalResult is a SCHEMA WITH AN APPLICABILITY RULE, not a cross product.
// An axis that does not apply is absent, not defaulted.
type TerminalResult struct {
	Status       ExecutionStatus        `json:"status"`
	Disposition  *CompletionDisposition `json:"disposition,omitempty"`
	CancelReason *CancellationReason    `json:"cancel_reason,omitempty"`
	FailureClass *FailureClass          `json:"failure_class,omitempty"`
	BlockedOn    []RequirementRef       `json:"blocked_on,omitempty"`
	Summary      string                 `json:"summary,omitempty"`
}

var (
	validStatus = map[ExecutionStatus]bool{
		StatusCompleted: true, StatusBlocked: true, StatusCancelled: true,
		StatusTimedOut: true, StatusFailed: true,
	}
	validDisposition = map[CompletionDisposition]bool{
		DispositionChanged: true, DispositionAlreadySatisfied: true,
	}
	validReason = map[CancellationReason]bool{
		ReasonSuperseded: true, ReasonOperatorRequested: true, ReasonShutdown: true,
	}
	validClass = map[FailureClass]bool{
		ClassRetryableInfrastructure: true, ClassNonRetryableAgent: true,
	}
)

// Validate enforces §5's applicability rule in both directions: a required axis
// must be present, and an inapplicable axis must be ABSENT.
//
// Checking only the first direction would accept a completed result carrying a
// failure class, which is exactly the axis collision the four-axis schema
// exists to prevent -- the schema would then permit the shape it was designed
// to rule out, and nothing downstream would object.
func (t *TerminalResult) Validate() error {
	if !validStatus[t.Status] {
		return fmt.Errorf("execution status %q is not one of the five", t.Status)
	}

	// Axis 2 -- completion disposition.
	switch {
	case t.Status == StatusCompleted && t.Disposition == nil:
		return fmt.Errorf("status completed requires a completion disposition")
	case t.Status != StatusCompleted && t.Disposition != nil:
		return fmt.Errorf("status %q must not carry a completion disposition", t.Status)
	case t.Disposition != nil && !validDisposition[*t.Disposition]:
		return fmt.Errorf("completion disposition %q is not one of the two", *t.Disposition)
	}

	// Axis 3 -- cancellation reason.
	switch {
	case t.Status == StatusCancelled && t.CancelReason == nil:
		return fmt.Errorf("status cancelled requires a cancellation reason")
	case t.Status != StatusCancelled && t.CancelReason != nil:
		return fmt.Errorf("status %q must not carry a cancellation reason", t.Status)
	case t.CancelReason != nil && !validReason[*t.CancelReason]:
		return fmt.Errorf("cancellation reason %q is not recognized", *t.CancelReason)
	}

	// Axis 4 -- failure class. Applies to failed AND timed_out.
	needsClass := t.Status == StatusFailed || t.Status == StatusTimedOut
	switch {
	case needsClass && t.FailureClass == nil:
		return fmt.Errorf("status %q requires a failure class", t.Status)
	case !needsClass && t.FailureClass != nil:
		return fmt.Errorf("status %q must not carry a failure class", t.Status)
	case t.FailureClass != nil && !validClass[*t.FailureClass]:
		return fmt.Errorf("failure class %q is not one of the two", *t.FailureClass)
	}

	// `blocked` references the pending requirement set rather than restating it.
	if t.Status == StatusBlocked && len(t.BlockedOn) == 0 {
		return fmt.Errorf("status blocked requires at least one requirement reference")
	}
	if t.Status != StatusBlocked && len(t.BlockedOn) > 0 {
		return fmt.Errorf("status %q must not carry requirement references", t.Status)
	}

	return nil
}

// Helpers, so the four axes are constructed rather than assembled field by
// field -- which is how an inapplicable axis gets set by accident.

// Completed builds a completed result with its required disposition.
func Completed(d CompletionDisposition, summary string) TerminalResult {
	return TerminalResult{Status: StatusCompleted, Disposition: &d, Summary: summary}
}

// Cancelled builds a cancelled result with its required reason.
func Cancelled(r CancellationReason, summary string) TerminalResult {
	return TerminalResult{Status: StatusCancelled, CancelReason: &r, Summary: summary}
}

// Failed builds a failed result with its required class.
func Failed(c FailureClass, summary string) TerminalResult {
	return TerminalResult{Status: StatusFailed, FailureClass: &c, Summary: summary}
}

// TimedOut builds a timed-out result with its required class.
func TimedOut(c FailureClass, summary string) TerminalResult {
	return TerminalResult{Status: StatusTimedOut, FailureClass: &c, Summary: summary}
}

// Blocked builds a blocked result referencing the requirements that stopped it.
func Blocked(reqs []RequirementRef, summary string) TerminalResult {
	return TerminalResult{Status: StatusBlocked, BlockedOn: reqs, Summary: summary}
}
