// Package host is the Orchestrator side of the contract: ADR 0030's execution
// boundary in miniature, plus process supervision for the local transport.
//
// The recorder here is IN-MEMORY and is not the data plane. What the spike
// proves is the contract and the boundary's state machine; the plane's own
// shape is checked separately, against the migrations.
package host

import (
	"crypto/sha256"
	"encoding/hex"
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

	// ArgsDigest binds the correlation to the arguments it was opened with.
	// Without it, reusing a correlation with different arguments replays a
	// result computed for something else.
	ArgsDigest string
	// Request is the persisted, substituted request. It is what makes an
	// operator wait RESTARTABLE: after an Orchestrator restart the in-memory
	// continuation is gone, and a wait row that cannot reconstruct its own
	// gate-3 execution is preserved but not recoverable.
	Request contract.ActionRequest
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
	// watermarks is the receiver's durable deduplication state, keyed by
	// (invocation, epoch). See Watermark.
	watermarks map[string]uint64
}

// NewRecorder builds an empty recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		byCorrelation: map[string]*Attempt{},
		watermarks:    map[string]uint64{},
	}
}

func correlationKey(invocation, correlation string) string {
	return invocation + "\x00" + correlation
}

// ErrCorrelationMismatch reports a correlation reused for a different logical
// action. It is a caller defect, not a retry.
type ErrCorrelationMismatch struct{ Detail string }

func (e *ErrCorrelationMismatch) Error() string { return "correlation mismatch: " + e.Detail }

// ArgsDigest is the binding a correlation is checked against. It stands in for
// ADR 0030 §3's digest over the SUBSTITUTED input -- the spike has no secrets to
// substitute, so it digests the arguments as given and the distinction is
// recorded rather than implemented.
func ArgsDigest(args json.RawMessage) string {
	sum := sha256.Sum256(args)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Open records the intent BEFORE the effect, per ADR 0030 §8 -- including for
// reads, because releasing data is the security-relevant effect of a retrieval.
//
// A correlation is bound to its LOGICAL ACTION: the action identity and the
// digest of its substituted arguments. Reuse matching both is a retry and
// returns the existing attempt; reuse matching neither is a defect and is
// refused. A first version keyed on the correlation alone, so one key could
// replay the result of a different action entirely.
func (r *Recorder) Open(invocation string, req contract.ActionRequest) (att *Attempt, fresh bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	digest := ArgsDigest(req.Arguments)
	key := correlationKey(invocation, req.Correlation)
	if existing, ok := r.byCorrelation[key]; ok {
		if existing.Action != req.Action {
			return nil, false, &ErrCorrelationMismatch{Detail: fmt.Sprintf(
				"%q was opened for %s and is being reused for %s",
				req.Correlation, existing.Action, req.Action)}
		}
		if existing.ArgsDigest != digest {
			return nil, false, &ErrCorrelationMismatch{Detail: fmt.Sprintf(
				"%q was opened for %s with different arguments", req.Correlation, existing.Action)}
		}
		return existing, false, nil
	}
	r.nextID++
	att = &Attempt{
		ID:          fmt.Sprintf("attempt-%03d", r.nextID),
		Invocation:  invocation,
		Correlation: req.Correlation,
		Action:      req.Action,
		State:       StateOpen,
		Transitions: []AttemptState{StateOpen},
		ArgsDigest:  digest,
		Request:     req,
	}
	r.attempts = append(r.attempts, att)
	r.byCorrelation[key] = att
	return att, true, nil
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

// Watermark returns the highest event sequence durably committed for an epoch.
//
// It lives on the recorder because the receiver's deduplication state must
// OUTLIVE the process: in-memory dedup means a crash lets a replayed usage
// event be counted twice, which is a corrupted cost figure rather than a
// harmless duplicate.
func (r *Recorder) Watermark(invocation string, epoch uint64) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.watermarks[fmt.Sprintf("%s\x00%d", invocation, epoch)]
}

// Advance commits the watermark. Called only after the event's effect is
// durable, so an acknowledgement never promises more than was recorded.
func (r *Recorder) Advance(invocation string, epoch, seq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fmt.Sprintf("%s\x00%d", invocation, epoch)
	if seq > r.watermarks[key] {
		r.watermarks[key] = seq
	}
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

	// admitMu linearizes admission closure against attempt registration. Taken
	// before mu, always, so the ordering cannot invert.
	admitMu sync.Mutex

	// InRegistration is a test seam; see HookInRegistration.
	InRegistration HookInRegistration
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
//
// It takes admitMu, which is the same lock attempt registration takes, so the
// two LINEARIZE. A first version read the closed flag and registered the
// attempt under different locks, leaving a window in which an attempt could be
// registered after closure and admitted anyway -- the drain would then be
// chasing a set that grew after it was closed, which is the precise failure
// ADR 0029 §7 step 2's ordering exists to prevent.
func (b *Boundary) CloseAdmission(invocationID string) {
	b.admitMu.Lock()
	defer b.admitMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.admissionClosed[invocationID] = true
}

// HookInRegistration runs inside the registration critical section, if set. It
// exists so the linearization can be tested DETERMINISTICALLY rather than by
// hoping a race detector trips: a claim can hold a registration open and check
// that CloseAdmission cannot return while it is in flight.
//
//nolint:revive // deliberately a test seam, and named as one
type HookInRegistration func()

// register atomically refuses-if-closed and opens the attempt.
func (b *Boundary) register(id string, req contract.ActionRequest) (*Attempt, bool, error) {
	b.admitMu.Lock()
	defer b.admitMu.Unlock()

	if b.InRegistration != nil {
		b.InRegistration()
	}

	b.mu.Lock()
	closed := b.admissionClosed[id]
	b.mu.Unlock()

	att, fresh, err := b.Recorder.Open(id, req)
	if err != nil {
		return nil, false, err
	}
	if closed && fresh {
		// Registered and immediately settled, so it is never in the unsettled
		// set a drain would have to wait on.
		b.Recorder.Settle(att, contract.OutcomeDenied, admissionClosedReason)
		return att, false, errAdmissionClosed
	}
	return att, fresh, nil
}

const admissionClosedReason = "admission closed: cancellation in progress"

//nolint:gochecknoglobals // sentinel
var errAdmissionClosed = &ErrCorrelationMismatch{Detail: admissionClosedReason}

// DrainActions waits until every registered attempt for an execution has
// settled, and reports whether it succeeded within the window.
//
// ADR 0030 §5 requires this: `Fence()` may return a positive receipt only when
// every attempt admitted against the generation and not yet settled has been
// drained short of its commit point, conditionally committed, or confirmed
// inside the fenced domain. Fencing the execution RESOURCE does not settle an
// Orchestrator-side mutation -- a data-plane write or a forge push lands
// outside any resource domain, so the domain receipt says nothing about it.
//
// A drain that does not settle in time yields no positive receipt, which is
// ADR 0029 §7's own rule: making the unsettled case cheap by reporting success
// anyway would reintroduce best-effort fencing through this door.
func (b *Boundary) DrainActions(invocationID string, within time.Duration) (drained bool, outstanding []*Attempt) {
	deadline := time.Now().Add(within)
	for {
		outstanding = nil
		for _, a := range b.Recorder.ForInvocation(invocationID) {
			if b.Recorder.State(a) != StateSettled {
				outstanding = append(outstanding, a)
			}
		}
		if len(outstanding) == 0 {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, outstanding
		}
		time.Sleep(10 * time.Millisecond)
	}
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

	// Registration and admission closure linearize (see register).
	att, fresh, err := b.register(id, req)
	if err != nil {
		if att != nil {
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeDenied, Reason: admissionClosedReason}
		}
		// A correlation reused for a different logical action is a caller
		// defect, and refusing it is the whole point of the binding.
		return contract.ActionResult{Correlation: req.Correlation,
			Outcome: contract.OutcomeDenied, Reason: err.Error()}
	}
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
		return b.completeAction(inv, att, true)

	case DecisionAllow:
		// fall through
	}

	return b.completeAction(inv, att, false)
}

// ResumeWait re-drives a preserved wait after an Orchestrator restart, from the
// attempt's own persisted request.
//
// Preserving the wait ROW is not recovery. The in-memory continuation that was
// going to run gate 3 died with the process, so without this the attempt is
// preserved and permanently stuck -- which is what the first version's
// conformance claim actually demonstrated, since its goroutine was still alive.
// Recovery means the record carries enough to execute the rest of the action.
func (b *Boundary) ResumeWait(inv *contract.Invocation, att *Attempt, done chan<- contract.ActionResult) {
	needOperator := b.Recorder.State(att) == StateOperatorWaiting
	go func() { done <- b.completeAction(inv, att, needOperator) }()
}

// completeAction runs gates 2 and 3 for an already-registered attempt. Both the
// first pass and a post-restart resume go through it, so a resumed action cannot
// drift from a fresh one.
func (b *Boundary) completeAction(inv *contract.Invocation, att *Attempt, needOperator bool) contract.ActionResult {
	req := att.Request

	if needOperator {
		requirement := att.Requirement
		if requirement == nil {
			requirement = &contract.RequirementRef{GateID: "unnamed", Statement: "an operator is required"}
		}
		if b.Operator == nil || !b.Operator(*requirement) {
			b.Recorder.Settle(att, contract.OutcomeDenied, "operator denied")
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeDenied, Reason: "operator denied"}
		}
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
