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
	// Outcome is meaningful only once State is settled. `unknown` is a real
	// outcome, which is why Phase 2's tool_calls_finished_check -- requiring a
	// boolean `succeeded` whenever finished_at is set -- cannot express a
	// settled attempt.
	Outcome contract.ActionOutcome
	Reason  string
	// Transitions records every durable state change, because ADR 0030 §8
	// requires entering and leaving a wait to BE a transition rather than the
	// absence of a completion.
	Transitions []AttemptState
	Requirement *contract.RequirementRef
	// ResourceOp names the provisioning or queueing operation a resource wait
	// is waiting on. Reconciliation after a restart must be able to restore or
	// validate it; a resource wait that cannot say what it waits for is not
	// recoverable, only stuck.
	ResourceOp string
}

// Recorder stands in for the persistence seam.
type Recorder struct {
	mu       sync.Mutex
	attempts []*Attempt
	// byCorrelation is the durable correlation-to-attempt mapping §6 requires.
	// Without it a restart cannot honour ADR 0030 §3's rule that a retry of the
	// same logical action reuses the same attempt identity -- which is what
	// makes the semantics at-most-once.
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
// and `fresh` is false: a retry of the same logical action is not a new action.
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

// State reads an attempt's current state under the lock.
func (r *Recorder) State(att *Attempt) AttemptState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return att.State
}

// ReconcileReport is what reconciliation did. It is a report rather than a
// count because the three cases have three different follow-ups, and collapsing
// them is how a first version came to destroy an operator wait.
type ReconcileReport struct {
	// SettledUnknown held an intent and no outcome.
	SettledUnknown []*Attempt
	// OperatorWaitsPreserved are healthy waits, validated and left alone.
	OperatorWaitsPreserved []*Attempt
	// ResourceWaitsToRestore are waits whose provisioning or queueing operation
	// must be restored or re-driven by the Orchestrator. They are NOT settled.
	ResourceWaitsToRestore []*Attempt
	// Invalid are declared waits that failed validation -- an operator wait with
	// no requirement, or a resource wait naming no operation. These are defects,
	// not states, and must be visible rather than swept into `unknown`.
	Invalid []*Attempt
}

// Reconcile runs after an Orchestrator restart or a lost runtime.
//
// The rule is NOT "reconciliation never touches a declared wait" -- a first
// version said that, over-correcting a defect where it settled everything, and
// would leave a resource wait whose provisioning operation had died stuck
// forever. The rule is that a declared wait may never be settled `unknown`
// merely for being nonterminal, because an attempt waiting on an operator is
// HEALTHY and an attempt whose process died is not.
//
// Each state therefore gets its own treatment:
//
//   - open             -> settled `unknown`; an intent was recorded and no
//     outcome ever was.
//   - operator_waiting -> preserved and VALIDATED against its requirement.
//     Nothing else can answer it, and destroying it takes
//     the requirement a `blocked` result references.
//   - resource_waiting -> preserved and handed back for RESTORATION. The
//     operation it waits on does not survive a restart, so
//     leaving it alone is as wrong as settling it.
func (r *Recorder) Reconcile() ReconcileReport {
	r.mu.Lock()
	defer r.mu.Unlock()

	var rep ReconcileReport
	for _, att := range r.attempts {
		switch att.State {
		case StateSettled:
			continue

		case StateOpen:
			att.State = StateSettled
			att.Outcome = contract.OutcomeUnknown
			att.Reason = "reconciled: intent recorded, no outcome"
			att.Transitions = append(att.Transitions, StateSettled)
			rep.SettledUnknown = append(rep.SettledUnknown, att)

		case StateOperatorWaiting:
			if att.Requirement == nil {
				rep.Invalid = append(rep.Invalid, att)
				continue
			}
			rep.OperatorWaitsPreserved = append(rep.OperatorWaitsPreserved, att)

		case StateResourceWaiting:
			if att.ResourceOp == "" {
				rep.Invalid = append(rep.Invalid, att)
				continue
			}
			rep.ResourceWaitsToRestore = append(rep.ResourceWaitsToRestore, att)
		}
	}
	return rep
}

// Attempts returns a snapshot.
func (r *Recorder) Attempts() []*Attempt {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Attempt, len(r.attempts))
	copy(out, r.attempts)
	return out
}

// ForInvocation returns this execution's attempts.
func (r *Recorder) ForInvocation(invocation string) []*Attempt {
	var out []*Attempt
	for _, a := range r.Attempts() {
		if a.Invocation == invocation {
			out = append(out, a)
		}
	}
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

// OperatorFn is gate 2. It may block for as long as a human takes; the boundary
// runs it off the transport's event loop precisely so that it can.
type OperatorFn func(req contract.RequirementRef) (approve bool)

// Executor is gate 3's effect.
type Executor func(inv *contract.Invocation, args json.RawMessage) (json.RawMessage, error)

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

	// EffectiveVersion is re-read at gate 3, so an amendment between admission
	// and effect refuses the action.
	EffectiveVersion uint64

	// CrashAfterOpen is FAULT INJECTION, not behaviour. It models ADR 0030 §8's
	// `Interrupted` row -- the process dying between the intent commit and the
	// outcome commit -- which is the shape v1 has at toolloop.go:546, where the
	// record is written after the effect.
	CrashAfterOpen map[string]bool

	mu sync.Mutex
	// blocked marks an invocation whose Story is awaiting resolution. ADR 0030
	// §4: further agent-initiated calls are REJECTED while it waits, and a
	// firing of this guard is an invariant violation rather than an ordinary
	// denial -- it means something upstream let a blocked caller keep working.
	blocked map[string]bool
	// admissionClosed marks an invocation whose cancellation has been
	// requested. This is ADR 0029 §7 step 2's ordering applied to attempts:
	// revoke the ability to create BEFORE draining. Without it a runtime keeps
	// issuing new actions throughout the grace period and the drain chases a
	// set the holder is still adding to.
	admissionClosed     map[string]bool
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
		CrashAfterOpen:    map[string]bool{},
		blocked:           map[string]bool{},
		admissionClosed:   map[string]bool{},
	}
}

// CloseAdmission refuses further agent-initiated actions for an invocation.
// Already-admitted attempts may reach their commit point; nothing new joins
// them.
func (b *Boundary) CloseAdmission(invocationID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.admissionClosed[invocationID] = true
}

// AdmissionClosed reports whether admission has been closed.
func (b *Boundary) AdmissionClosed(invocationID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.admissionClosed[invocationID]
}

// Submit runs one agent-initiated action through the gates and delivers exactly
// one result on `done`.
//
// It returns immediately. Gate 2's human wait and gate 3's resource wait both
// happen off the caller's goroutine, because ADR 0030 §4's wait is LOGICAL: it
// must not hold a transport connection open. A first version ran both
// synchronously inside the transport's only event loop, so while an action
// waited the host could process no cancellation, no heartbeat, and no
// re-attachment -- which is the detached wait claimed rather than demonstrated.
func (b *Boundary) Submit(inv *contract.Invocation, req contract.ActionRequest, done chan<- contract.ActionResult) {
	go func() { done <- b.execute(inv, req) }()
}

// ExecuteSync submits an action and waits for it. It exists for the in-process
// boundary claims, which are about the gates rather than about the transport;
// nothing on the wire path uses it, because a synchronous wait there is the
// defect Submit exists to avoid.
func (b *Boundary) ExecuteSync(inv *contract.Invocation, req contract.ActionRequest) contract.ActionResult {
	done := make(chan contract.ActionResult, 1)
	b.Submit(inv, req, done)
	return <-done
}

//nolint:gocyclo // The gate sequence is the subject; splitting it hides it.
func (b *Boundary) execute(inv *contract.Invocation, req contract.ActionRequest) contract.ActionResult {
	id := inv.ID()

	b.mu.Lock()
	isBlocked := b.blocked[id]
	closed := b.admissionClosed[id]
	b.mu.Unlock()

	if isBlocked {
		b.mu.Lock()
		b.InvariantViolations = append(b.InvariantViolations,
			fmt.Sprintf("invocation %s issued %s while awaiting resolution", id, req.Action))
		b.mu.Unlock()
		return contract.ActionResult{
			Correlation: req.Correlation,
			Outcome:     contract.OutcomeDenied,
			Reason:      "story is awaiting resolution",
		}
	}

	att, fresh := b.Recorder.Open(id, req.Correlation, req.Action)
	if !fresh {
		// A duplicate request for a logical action that already exists never
		// re-enters the gates. Only a SETTLED attempt may replay its result; an
		// outstanding one is reported as outstanding, because re-running policy,
		// operator handling, or resource acquisition for an action already in
		// flight is a second pass at one logical action.
		if st := b.Recorder.State(att); st != StateSettled {
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeOutstanding, Reason: "already in flight as " + string(st)}
		}
		return contract.ActionResult{
			Correlation: req.Correlation,
			AttemptID:   att.ID,
			Outcome:     att.Outcome,
			Reason:      "replayed: " + att.Reason,
			Requirement: att.Requirement,
		}
	}

	if closed {
		const why = "admission closed: cancellation in progress"
		b.Recorder.Settle(att, contract.OutcomeDenied, why)
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeDenied, Reason: why}
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

		// ---- Gate 2: human approval ----
		if !inv.Config.OperatorResponder {
			// Headless. Nothing will ever answer this, so it is NOT a wait: the
			// attempt settles TERMINALLY as blocked, preserving the requirement,
			// and the Story becomes blocked immediately (ADR 0030 §4).
			//
			// A first version left the attempt in operator_waiting, returned
			// `denied` on the wire, and recorded the execution terminally
			// blocked -- three descriptions of one event, no two agreeing.
			b.mu.Lock()
			b.blocked[id] = true
			b.mu.Unlock()
			b.Recorder.Settle(att, contract.OutcomeBlocked, requirement.Statement)
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeBlocked, Reason: requirement.Statement,
				Requirement: requirement}
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
		att.ResourceOp = "provision:" + req.Action.String()
		b.Recorder.Transition(att, StateResourceWaiting)
		time.Sleep(d)
	}

	if b.CrashAfterOpen[req.Action.String()] {
		// Fault injection: the intent is committed and the outcome never is. In
		// the real case the Orchestrator dies and NO reply is sent; the agent's
		// own timeout ends its wait. This short-circuits that to keep the
		// scenario bounded -- the assertion, that reconciliation settles the
		// attempt `unknown`, is the same either way.
		b.Recorder.Transition(att, StateOpen)
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: "", Reason: "interrupted"}
	}

	// Admission closure is deliberately NOT re-checked here. Closing admission
	// revokes the ability to CREATE attempts; it does not abort ones already
	// admitted -- "revoke, then drain" means the drain lets them reach their
	// commit point (ADR 0029 §7 step 2). A first version re-checked it at gate
	// 3 and thereby killed an in-flight action mid-drain, which the conformance
	// suite caught as the agent failing to read a diff it had already been
	// admitted to read. What bounds an attempt that will not settle is the
	// grace period, not a second refusal.
	//
	// Approval clears the human requirement and nothing else; everything
	// deterministic is still re-checked immediately before the effect.
	if err := b.revalidate(inv, req.Action); err != nil {
		b.Recorder.Settle(att, contract.OutcomeDenied, err.Error())
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeDenied, Reason: err.Error()}
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
	if b.EffectiveVersion != 0 && inv.Config.Work.EffectiveVersion != b.EffectiveVersion {
		return fmt.Errorf("invocation is bound to work version %d, effective is %d",
			inv.Config.Work.EffectiveVersion, b.EffectiveVersion)
	}
	return nil
}

// revalidate is gate 3's re-check. Gate 1 could not check resource generation
// or leases, because there may have been no resource yet (ADR 0030 §2).
func (b *Boundary) revalidate(inv *contract.Invocation, action contract.ActionID) error {
	if err := b.admit(inv, action); err != nil {
		return err
	}
	for _, r := range inv.Bindings.Resources {
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

// BlockedRequirements returns the requirements a blocked execution stopped on,
// for the terminal result to reference.
//
// It reads SETTLED-blocked attempts, because a headless block is terminal for
// the action. Reading operator_waiting instead would find nothing, which is
// what a first version did.
func (b *Boundary) BlockedRequirements(invocationID string) []contract.RequirementRef {
	var out []contract.RequirementRef
	for _, att := range b.Recorder.ForInvocation(invocationID) {
		if att.Requirement == nil {
			continue
		}
		if att.State == StateSettled && att.Outcome == contract.OutcomeBlocked {
			out = append(out, *att.Requirement)
		}
	}
	return out
}
