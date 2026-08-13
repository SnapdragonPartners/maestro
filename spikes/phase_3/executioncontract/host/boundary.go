// Package host is the Orchestrator side of the contract: ADR 0030's execution
// boundary in miniature, plus process supervision for the local transport.
//
// The recorder here is IN-MEMORY and is not the data plane. What the spike
// proves is the contract and the boundary's state machine; the plane's own
// shape is checked separately, against the migrations.
package host

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"maestro-spike/phase3/executioncontract/contract"
)

// AttemptState is ADR 0032 §6's action state vocabulary, which ADR 0030 §8
// assigns to A4. The two waits are kept DISTINCT rather than merged into one
// "waiting" because they have different responders, different release rules,
// and different costs -- a watchdog that cannot tell them apart cannot have a
// policy for either.
type AttemptState string

const (
	StateOpen            AttemptState = "open"
	StateOperatorWaiting AttemptState = "operator_waiting"
	StateResourceWaiting AttemptState = "resource_waiting"
	StateSettled         AttemptState = "settled"
)

// Attempt is one logical action. It corresponds to a tool_calls row.
type Attempt struct {
	ID          string
	Invocation  string
	Correlation string
	Action      contract.ActionID
	State       AttemptState
	// Outcome is meaningful only once State is settled. It is nullable-in-
	// spirit: `unknown` is a real outcome, which is why Phase 2's
	// tool_calls_finished_check -- requiring a boolean `succeeded` whenever
	// finished_at is set -- cannot express a settled attempt.
	Outcome contract.ActionOutcome
	Reason  string
	// Transitions records every durable state change, because ADR 0030 §8
	// requires entering and leaving a wait to BE a transition rather than the
	// absence of a completion.
	Transitions []AttemptState
	Requirement *contract.RequirementRef
}

// Recorder stands in for the persistence seam.
type Recorder struct {
	mu       sync.Mutex
	attempts []*Attempt
	// byCorrelation is the durable correlation-to-attempt mapping §6 requires.
	// Without it a reconnection cannot honour ADR 0030 §3's rule that a
	// transport retry of the same logical action reuses the same attempt
	// identity -- which is what makes the semantics at-most-once.
	byCorrelation map[string]*Attempt
	nextID        int
}

// NewRecorder builds an empty recorder.
func NewRecorder() *Recorder {
	return &Recorder{byCorrelation: map[string]*Attempt{}}
}

func correlationKey(invocation, correlation string) string {
	return invocation + "\x00" + correlation
}

// Open records the intent BEFORE the effect, per ADR 0030 §8 -- including for
// reads, because releasing data is the security-relevant effect of a retrieval.
// If an attempt already exists for this correlation it is returned unchanged
// and `fresh` is false: a transport retry of the same logical action is not a
// new action.
func (r *Recorder) Open(invocation, correlation string, action contract.ActionID) (att *Attempt, fresh bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := correlationKey(invocation, correlation)
	if existing, ok := r.byCorrelation[key]; ok {
		return existing, false
	}
	r.nextID++
	att = &Attempt{
		ID:          fmt.Sprintf("attempt-%03d", r.nextID),
		Invocation:  invocation,
		Correlation: correlation,
		Action:      action,
		State:       StateOpen,
		Transitions: []AttemptState{StateOpen},
	}
	r.attempts = append(r.attempts, att)
	r.byCorrelation[key] = att
	return att, true
}

// Transition records a durable nonterminal state change.
func (r *Recorder) Transition(att *Attempt, to AttemptState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	att.State = to
	att.Transitions = append(att.Transitions, to)
}

// Settle completes an attempt with an outcome.
func (r *Recorder) Settle(att *Attempt, outcome contract.ActionOutcome, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	att.State = StateSettled
	att.Outcome = outcome
	att.Reason = reason
	att.Transitions = append(att.Transitions, StateSettled)
}

// Reconcile settles attempts left OPEN -- an intent recorded with no outcome --
// as `unknown`. This is the path ADR 0030 §8 requires and the one Phase 2's
// tool_calls_finished_check cannot express.
//
// It is scoped to StateOpen, and the scoping is load-bearing. A first version
// settled every attempt that was not already settled, which swept up the
// attempts sitting in a DECLARED wait -- and an attempt waiting on an operator
// is healthy, not interrupted. The conformance suite caught it as
// gate/headless-blocks-immediately failing with an empty requirement set: the
// reconciler had destroyed the very record the blocked terminal result must
// reference.
//
// ADR 0030 §8 states "the watchdog leaves it alone" for both waits. The
// RECONCILER is a different actor and the rule was never written for it.
func (r *Recorder) Reconcile() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, att := range r.attempts {
		if att.State != StateOpen {
			continue
		}
		att.State = StateSettled
		att.Outcome = contract.OutcomeUnknown
		att.Reason = "reconciled: intent recorded, no outcome"
		att.Transitions = append(att.Transitions, StateSettled)
		n++
	}
	return n
}

// Attempts returns a snapshot.
func (r *Recorder) Attempts() []*Attempt {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Attempt, len(r.attempts))
	copy(out, r.attempts)
	return out
}

// Lookup finds an attempt by correlation.
func (r *Recorder) Lookup(invocation, correlation string) (*Attempt, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	att, ok := r.byCorrelation[correlationKey(invocation, correlation)]
	return att, ok
}

// ---------- the three gates ----------

// Decision is ADR 0030 §3's three-valued gate-1 result.
type Decision string

const (
	DecisionAllow            Decision = "allow"
	DecisionDeny             Decision = "deny"
	DecisionRequiresOperator Decision = "requires_operator"
)

// PolicyHook is the single extension point. MVP is default-allow and carries no
// rules; candidate 12 fills it. It is deterministic, performs no side effects,
// and may not infer -- a semantic gate is an AGENT, not logic inside the hook.
type PolicyHook func(inv *contract.Invocation, action contract.ActionID, args json.RawMessage) (Decision, *contract.RequirementRef)

// DefaultAllow is the MVP hook.
func DefaultAllow(*contract.Invocation, contract.ActionID, json.RawMessage) (Decision, *contract.RequirementRef) {
	return DecisionAllow, nil
}

// OperatorFn is gate 2. Returning false denies.
type OperatorFn func(req contract.RequirementRef) (approve bool)

// Executor is gate 3's effect.
type Executor func(inv *contract.Invocation, args json.RawMessage) (json.RawMessage, error)

// ErrBlocked is returned when a gate requires an operator and the invocation
// declares no responder. ADR 0030 §4: the Story becomes blocked IMMEDIATELY
// rather than waiting for a timeout, because headless is a declared
// configuration and not an observation that nobody answered.
type ErrBlocked struct{ Requirement contract.RequirementRef }

func (e *ErrBlocked) Error() string { return "blocked: " + e.Requirement.Statement }

// Boundary is the one mandatory route to a mediated effect.
type Boundary struct {
	Recorder *Recorder
	Policy   PolicyHook
	Operator OperatorFn
	// Executors is the action vocabulary the Orchestrator owns. An action with
	// no executor is not a capability the boundary can honour.
	Executors map[string]Executor

	// ResourceDelay simulates gate 3's own wait -- provisioning, or queueing
	// for capacity per ADR 0029 §2. It is a DIFFERENT wait from gate 2's.
	ResourceDelay map[string]time.Duration

	// CurrentGeneration is revalidated immediately before the effect. A stale
	// generation is ADR 0029 §7 requirement 5: a call issued by a fenced holder
	// is rejected at the boundary even if it arrives late.
	CurrentGeneration map[string]uint64

	// CrashAfterOpen is FAULT INJECTION, not behaviour. It models ADR 0030
	// §8's `Interrupted` row -- the process dying between the intent commit and
	// the outcome commit -- which is the exact shape v1 has at
	// toolloop.go:546, where the record is written after the effect. The
	// attempt is left open and no result is returned; reconciliation is what
	// must then settle it as `unknown`.
	CrashAfterOpen map[string]bool

	// EffectiveVersion is re-read at gate 3, so an amendment between admission
	// and effect refuses the action.
	EffectiveVersion uint64

	mu sync.Mutex
	// blocked marks an invocation whose Story is awaiting resolution. ADR 0030
	// §4: further agent-initiated calls are REJECTED while it waits, and a
	// firing of this guard is an invariant violation rather than an ordinary
	// denial -- it means something upstream let a blocked caller keep working.
	blocked             map[string]bool
	InvariantViolations []string
}

// NewBoundary builds a boundary with the MVP default-allow hook.
func NewBoundary(rec *Recorder) *Boundary {
	return &Boundary{
		Recorder:          rec,
		Policy:            DefaultAllow,
		Executors:         map[string]Executor{},
		ResourceDelay:     map[string]time.Duration{},
		CurrentGeneration: map[string]uint64{},
		blocked:           map[string]bool{},
	}
}

// Execute runs one agent-initiated action through all three gates.
func (b *Boundary) Execute(inv *contract.Invocation, req contract.ActionRequest) contract.ActionResult {
	b.mu.Lock()
	isBlocked := b.blocked[inv.ID]
	b.mu.Unlock()
	if isBlocked {
		b.mu.Lock()
		b.InvariantViolations = append(b.InvariantViolations,
			fmt.Sprintf("invocation %s issued %s while awaiting resolution", inv.ID, req.Action))
		b.mu.Unlock()
		return contract.ActionResult{
			Correlation: req.Correlation,
			Outcome:     contract.OutcomeDenied,
			Reason:      "story is awaiting resolution",
		}
	}

	att, fresh := b.Recorder.Open(inv.ID, req.Correlation, req.Action)
	if !fresh && att.State == StateSettled {
		// One attempt identity commits its effect at most once (ADR 0030 §3).
		return contract.ActionResult{
			Correlation: req.Correlation,
			AttemptID:   att.ID,
			Outcome:     att.Outcome,
			Reason:      "replayed: " + att.Reason,
		}
	}

	// ---- Gate 1: admission, then policy ----
	if err := b.admit(inv, req.Action); err != nil {
		b.Recorder.Settle(att, contract.OutcomeDenied, err.Error())
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeDenied, Reason: err.Error()}
	}

	decision, requirement := b.Policy(inv, req.Action, req.Arguments)
	switch decision {
	case DecisionDeny:
		reason := "denied by policy"
		if requirement != nil {
			reason = requirement.Statement
		}
		b.Recorder.Settle(att, contract.OutcomeDenied, reason)
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeDenied, Reason: reason}

	case DecisionRequiresOperator:
		if requirement == nil {
			requirement = &contract.RequirementRef{GateID: "unnamed", Statement: "an operator is required"}
		}
		requirement.AttemptID = att.ID
		att.Requirement = requirement

		// ---- Gate 2: human approval, which BLOCKS ----
		if !inv.OperatorResponder {
			b.mu.Lock()
			b.blocked[inv.ID] = true
			b.mu.Unlock()
			b.Recorder.Transition(att, StateOperatorWaiting)
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeDenied, Reason: "blocked: no operator responder"}
		}
		b.Recorder.Transition(att, StateOperatorWaiting)
		if b.Operator == nil || !b.Operator(*requirement) {
			b.Recorder.Settle(att, contract.OutcomeDenied, "operator denied")
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeDenied, Reason: "operator denied"}
		}

	case DecisionAllow:
		// fall through
	}

	// ---- Gate 3: resources, revalidation, execution ----
	if d, ok := b.ResourceDelay[req.Action.String()]; ok && d > 0 {
		b.Recorder.Transition(att, StateResourceWaiting)
		time.Sleep(d)
	}

	// Approval clears the human requirement and nothing else. Everything
	// deterministic is re-checked immediately before the effect.
	if err := b.revalidate(inv, req.Action); err != nil {
		b.Recorder.Settle(att, contract.OutcomeDenied, err.Error())
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeDenied, Reason: err.Error()}
	}

	if b.CrashAfterOpen[req.Action.String()] {
		// Fault injection: the intent is committed and the outcome never is.
		// The attempt stays open, and only reconciliation may settle it.
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: "", Reason: "interrupted"}
	}

	exec, ok := b.Executors[req.Action.String()]
	if !ok {
		b.Recorder.Settle(att, contract.OutcomeFailed, "no executor for action")
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeFailed, Reason: "no executor for action"}
	}
	b.Recorder.Transition(att, StateOpen)
	result, err := exec(inv, req.Arguments)
	if err != nil {
		b.Recorder.Settle(att, contract.OutcomeFailed, err.Error())
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeFailed, Reason: err.Error()}
	}
	b.Recorder.Settle(att, contract.OutcomeSucceeded, "")
	return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
		Outcome: contract.OutcomeSucceeded, Result: result}
}

// admit is gate 1 stage 1: deterministic, Orchestrator-owned, NOT policy. None
// of it is negotiable by a policy implementation, which is why it runs first --
// an empty policy must not be able to disable an invariant.
func (b *Boundary) admit(inv *contract.Invocation, action contract.ActionID) error {
	if !inv.HasCapability(action) {
		return fmt.Errorf("action %s is not in the resolved capability set", action)
	}
	if b.EffectiveVersion != 0 && inv.Work.EffectiveVersion != b.EffectiveVersion {
		return fmt.Errorf("invocation is bound to work version %d, effective is %d",
			inv.Work.EffectiveVersion, b.EffectiveVersion)
	}
	return nil
}

// revalidate is gate 3's re-check. Gate 1 could not check resource generation
// or leases, because there may have been no resource yet (ADR 0030 §2).
func (b *Boundary) revalidate(inv *contract.Invocation, action contract.ActionID) error {
	if err := b.admit(inv, action); err != nil {
		return err
	}
	for _, r := range inv.Resources {
		current, known := b.CurrentGeneration[r.ReferenceID]
		if known && current != r.InstanceGeneration {
			return fmt.Errorf("resource %s generation %d is stale, current is %d",
				r.ReferenceID, r.InstanceGeneration, current)
		}
	}
	return nil
}

// IsBlocked reports whether the invocation's Story is awaiting resolution.
func (b *Boundary) IsBlocked(invocationID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.blocked[invocationID]
}

// BlockedRequirements returns the requirement set a blocked execution is
// waiting on, for the terminal result to reference.
func (b *Boundary) BlockedRequirements(invocationID string) []contract.RequirementRef {
	var out []contract.RequirementRef
	for _, att := range b.Recorder.Attempts() {
		if att.Invocation == invocationID && att.State == StateOperatorWaiting && att.Requirement != nil {
			out = append(out, *att.Requirement)
		}
	}
	return out
}
