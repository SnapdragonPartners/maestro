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
	"sort"
	"strings"
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
	// Requirements is the COMPLETE set gate 1 collected, and CanonicalSet its
	// ordering-independent rendering. Gate 3 compares against it: if the set
	// changed, the answer given is void rather than supplemented (ADR 0030 §5).
	Requirements []contract.RequirementRef
	CanonicalSet string
	// EffectiveScopes is the intersection across those gates.
	EffectiveScopes []string
	// OperatorApproved is the answer THIS attempt received, if any. It becomes
	// a reusable decision only if this attempt then goes stale before commit.
	OperatorApproved *bool
	// Binding is the logical action key a decision is scoped to.
	Binding string
	// ResourceOp names the provisioning or queueing operation a resource wait
	// is waiting on. Reconciliation after a restart must be able to restore or
	// validate it; a resource wait that cannot say what it waits for is not
	// recoverable, only stuck.
	ResourceOp string

	// ArgsDigest binds the correlation to the arguments it was opened with.
	// Without it, reusing a correlation with different arguments replays a
	// result computed for something else.
	//
	// It is a DIGEST and not the arguments. Persisting the complete substituted
	// request would put data ADR 0030 §3 deliberately keeps out of the Audit
	// family into it, and would promise a recovery this contract does not offer:
	// §6's guarantee is restart from the last durable workflow ARTIFACT, not
	// resumption of a half-run action.
	ArgsDigest string
	// Disposition records WHERE the attempt stopped, which is what a fence
	// receipt actually needs. "Settled" alone does not distinguish an action
	// that stopped before its commit point from one that committed anyway.
	Disposition Disposition
	// Result is the effect's payload, retained so a retry replays what the
	// action actually returned. Replaying the OUTCOME alone hands a caller a
	// success with no data, which is a different answer wearing the same label.
	Result json.RawMessage
	// SettledAfterClose marks an attempt that settled after admission was
	// closed. Combined with a committed disposition it is the case a receipt
	// must refuse: an effect that landed inside the drain window.
	SettledAfterClose bool
}

// Disposition is ADR 0030 §5's three safe outcomes, plus the unsafe one.
type Disposition string

const (
	// DispositionUnset means nothing recorded where the attempt stopped, which
	// is itself unsafe: a drain cannot treat it as covered.
	DispositionUnset Disposition = ""
	// DispositionBeforeCommit -- confirmed not to have passed its commit point.
	DispositionBeforeCommit Disposition = "before_commit"
	// DispositionCommitted -- the effect committed. Safe only in the sense that
	// it is settled and known; a drain must still refuse a receipt if it
	// committed AFTER admission closed.
	DispositionCommitted Disposition = "committed"
	// DispositionInDomain -- the effect ran inside the fenced resource domain,
	// so fencing the domain covers it.
	DispositionInDomain Disposition = "in_domain"
)

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
	// committed holds sequences committed ahead of the watermark, so a gap is
	// never acknowledged.
	committed map[string]map[uint64]bool
	// decisions persists operator approvals against a logical action, so a
	// restart does not re-ask a human who already answered.
	decisions map[string]bool
	// closedAt mirrors admission closure, so a settle can record whether it
	// happened inside the drain window.
	closedAt map[string]bool
	// responseWaits are execution-level waits on another principal.
	responseWaits map[string]*ResponseWait
}

// MarkAdmissionClosed tells the recorder that admission has closed, so every
// later settle is stamped as having happened inside the drain window.
func (r *Recorder) MarkAdmissionClosed(invocation string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closedAt[invocation] = true
}

// NewRecorder builds an empty recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		byCorrelation: map[string]*Attempt{},
		watermarks:    map[string]uint64{},
		committed:     map[string]map[uint64]bool{},
		decisions:     map[string]bool{},
		closedAt:      map[string]bool{},
		responseWaits: map[string]*ResponseWait{},
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

// Settle completes an attempt with an outcome and a disposition.
//
// The disposition is not optional decoration: ADR 0030 §5's receipt is about
// whether an effect can still land, and "settled" alone cannot say. A refused
// action stopped before its commit point; an executed one committed.
func (r *Recorder) Settle(att *Attempt, outcome contract.ActionOutcome, reason string, d Disposition) {
	r.SettleWith(att, outcome, reason, d, nil)
}

// SettleWith completes an attempt and retains its result payload.
func (r *Recorder) SettleWith(
	att *Attempt, outcome contract.ActionOutcome, reason string,
	d Disposition, result json.RawMessage,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if result != nil {
		att.Result = result
	}
	att.SettledAfterClose = r.closedAt[att.Invocation]
	att.State = StateSettled
	att.Outcome = outcome
	att.Reason = reason
	att.Disposition = d
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
	// StaleWaits are declared waits the restart invalidated. They are settled
	// `stale` -- ADR 0030 §5's own word for an action that must be re-requested
	// rather than continued -- with their requirement and decision preserved,
	// and NEVER as `unknown`: an attempt awaiting an operator is not an attempt
	// whose outcome nobody knows.
	StaleWaits []*Attempt
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
//   - operator_waiting -> settled `stale`, carrying its requirement and any
//     operator grant forward. Nothing else can answer it,
//     and it is not `unknown`: a wait is not an outcome
//     nobody knows.
//   - resource_waiting -> settled `stale`, carrying the operation it named. The
//     operation does not survive a restart either.
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
			// STALE, not resumed. The continuation that would have run gate 3
			// died with the process, and rebuilding it would require persisting
			// the complete substituted request -- which ADR 0030 §3 keeps out of
			// Audit, and which promises more than §6's artifact-level recovery.
			// The requirement and any operator decision are preserved, so the
			// re-requested action is not re-asked.
			att.State = StateSettled
			att.Outcome = contract.OutcomeStale
			att.Reason = "orchestrator restarted while awaiting an operator"
			att.Disposition = DispositionBeforeCommit
			att.Transitions = append(att.Transitions, StateSettled)
			r.promoteLocked(att)
			rep.StaleWaits = append(rep.StaleWaits, att)

		case StateResourceWaiting:
			if att.ResourceOp == "" {
				rep.Invalid = append(rep.Invalid, att)
				continue
			}
			att.State = StateSettled
			att.Outcome = contract.OutcomeStale
			att.Reason = "orchestrator restarted while awaiting " + att.ResourceOp
			att.Disposition = DispositionBeforeCommit
			att.Transitions = append(att.Transitions, StateSettled)
			r.promoteLocked(att)
			rep.StaleWaits = append(rep.StaleWaits, att)
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

// ResponseWait is a durable execution-level wait on another principal's answer
// (§4). It is not an ACTION state: the ask action settled when it was routed.
type ResponseWait struct {
	Invocation       string
	QuestionArtifact string
	Answered         bool
	AnswerArtifact   string
}

// OpenResponseWait records that an execution is awaiting an answer. It names
// the question artifact, because a wait that cannot say what it waits for is
// not recoverable -- the same rule §6 applies to the action waits.
func (r *Recorder) OpenResponseWait(invocation, questionArtifact string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.responseWaits[invocation] = &ResponseWait{
		Invocation: invocation, QuestionArtifact: questionArtifact}
}

// AwaitingResponse reports an open, unanswered response wait.
func (r *Recorder) AwaitingResponse(invocation string) (*ResponseWait, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.responseWaits[invocation]
	if !ok || w.Answered {
		return nil, false
	}
	return w, true
}

// DeliverAnswer closes a response wait with the answering artifact. The
// reference is what the next incarnation carries on its BINDINGS -- the
// immutable configuration cannot acquire one.
// Marking it answered is what closes the guard, because the guard reads this
// record -- one write, not a clear-then-mark whose order could leave the wait
// open and unguarded.
func (r *Recorder) DeliverAnswer(invocation, answerArtifact string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.responseWaits[invocation]
	if !ok || w.Answered {
		return false
	}
	w.AnswerArtifact = answerArtifact
	w.Answered = true
	return true
}

// Watermark returns the highest event sequence durably committed for an epoch.
//
// It lives on the recorder because the receiver's deduplication state must
// OUTLIVE the process: in-memory dedup means a crash lets a replayed usage
// event be counted twice, which is a corrupted cost figure rather than a
// harmless duplicate.
func (r *Recorder) Watermark(invocation string, epoch uint64, stream string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.watermarks[wmKey(invocation, epoch, stream)]
}

func wmKey(invocation string, epoch uint64, stream string) string {
	return fmt.Sprintf("%s\x00%d\x00%s", invocation, epoch, stream)
}

// Advance commits an event and moves the watermark over every CONTIGUOUS
// sequence committed so far.
//
// Storing the maximum received sequence would acknowledge a gap: sequence 5
// arriving before 4 would advance the watermark past 4, telling the sender it
// may discard an event that was never committed. The watermark therefore only
// moves through an unbroken run, and out-of-order arrivals are held until the
// gap fills.
func (r *Recorder) Advance(invocation string, epoch, seq uint64, stream string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := wmKey(invocation, epoch, stream)
	if r.committed[key] == nil {
		r.committed[key] = map[uint64]bool{}
	}
	r.committed[key][seq] = true
	for {
		next := r.watermarks[key] + 1
		if !r.committed[key][next] {
			return
		}
		r.watermarks[key] = next
		delete(r.committed[key], next)
	}
}

// Committed reports whether a specific sequence was committed, whether or not
// the watermark has reached it.
func (r *Recorder) Committed(invocation string, epoch, seq uint64, stream string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := wmKey(invocation, epoch, stream)
	return seq <= r.watermarks[key] || r.committed[key][seq]
}

// ConsumeDecision takes a persisted operator decision for a logical action, and
// REMOVES it. Keeping the decision rather than the request is what lets a
// re-requested action skip gate 2 without persisting anything ADR 0030 §3
// excludes from Audit; consuming it is what keeps `approve_once` meaning once.
//
// One decision resolves one logical action. A decision that survived its use
// would make a second, intentional repetition of the same action indistinguish-
// able from the recovery it was recorded for.
func (r *Recorder) ConsumeDecision(invocation, binding, canonicalSet string) (approved, found bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := decisionKey(invocation, binding, canonicalSet)
	d, ok := r.decisions[key]
	if ok {
		delete(r.decisions, key)
	}
	return d, ok
}

// PeekDecision reports a recorded decision without consuming it.
func (r *Recorder) PeekDecision(invocation, binding, canonicalSet string) (approved, found bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.decisions[decisionKey(invocation, binding, canonicalSet)]
	return d, ok
}

// SettleStaleAndPromote settles an attempt `stale` before its commit point and
// promotes its grant, in ONE critical section.
//
// Settling and promoting separately leaves a window: a crash between them loses
// the grant, and a successor arriving in it consumes nothing and re-asks a human
// who already answered. It also lets a LATE operator response write a grant onto
// an attempt that has already been settled, promoting an answer to an action
// that no longer exists.
func (r *Recorder) SettleStaleAndPromote(att *Attempt, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if att.State == StateSettled {
		return
	}
	att.SettledAfterClose = r.closedAt[att.Invocation]
	att.State = StateSettled
	att.Outcome = contract.OutcomeStale
	att.Reason = reason
	att.Disposition = DispositionBeforeCommit
	att.Transitions = append(att.Transitions, StateSettled)
	r.promoteLocked(att)
}

// promoteLocked publishes an attempt's grant for a successor. Callers hold
// r.mu, so promotion is always in the same critical section as the settlement
// that made the attempt eligible.
func (r *Recorder) promoteLocked(att *Attempt) {
	if att.OperatorApproved == nil || att.Binding == "" {
		return
	}
	r.decisions[decisionKey(att.Invocation, att.Binding, att.CanonicalSet)] = *att.OperatorApproved
}

func decisionKey(invocation, binding, canonicalSet string) string {
	return invocation + "\x00" + binding + "\x00" + canonicalSet
}

// OpenSettled registers an attempt and settles it terminally in ONE critical
// section.
//
// ADR 0030 §8: a denial is terminal and is opened and completed TOGETHER, in one
// transaction, with the reason code -- there is no effect to await. Recording no
// attempt at all loses the observation candidate 12 will be tuned against, and
// opening one that a separate call must then settle is the unsettled-attempt
// shape a drain would wait on.
func (r *Recorder) OpenSettled(
	invocation string, req contract.ActionRequest,
	outcome contract.ActionOutcome, reason string, d Disposition,
) *Attempt {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	att := &Attempt{
		ID:                fmt.Sprintf("attempt-%03d", r.nextID),
		Invocation:        invocation,
		Correlation:       req.Correlation,
		Action:            req.Action,
		State:             StateSettled,
		Outcome:           outcome,
		Reason:            reason,
		Disposition:       d,
		Transitions:       []AttemptState{StateOpen, StateSettled},
		ArgsDigest:        ArgsDigest(req.Arguments),
		SettledAfterClose: r.closedAt[invocation],
	}
	r.attempts = append(r.attempts, att)
	// The denial CLAIMS its correlation, in the same critical section.
	//
	// A previous version deliberately left it unclaimed, reasoning that a retry
	// should be admitted once the wait cleared. That reasoning was the defect:
	// one correlation is one logical action (ADR 0030 §3), so leaving it free
	// lets the same key produce a second terminal record, or produce a denial
	// AND an effect. Replaying the denial to a retry is the correct answer --
	// an intentional later attempt is a new logical action and needs a new
	// correlation.
	r.byCorrelation[correlationKey(invocation, req.Correlation)] = att
	return att
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
// It returns the COMPLETE set of requirements, not the first: ADR 0030 §3
// evaluates every applicable gate before anything blocks, so the operator
// answers once rather than discovering a second gate after clearing the first.
type PolicyHook func(inv *contract.Invocation, action contract.ActionID, args json.RawMessage) (Decision, []contract.RequirementRef)

// DefaultAllow is the MVP hook.
func DefaultAllow(*contract.Invocation, contract.ActionID, json.RawMessage) (Decision, []contract.RequirementRef) {
	return DecisionAllow, nil
}

// CanonicalRequirements renders a requirement set in ADR 0030 §3's canonical,
// ordering-independent form. Two evaluations that collected the same
// requirements in a different order are the same set and must compare equal --
// which is what makes gate 3's comparison well-defined.
func CanonicalRequirements(reqs []contract.RequirementRef) string {
	parts := make([]string, 0, len(reqs))
	for _, r := range reqs {
		parts = append(parts, r.GateID+"\x1f"+r.Statement+"\x1f"+strings.Join(sortedScopes(r.Scopes), ","))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x1e")
}

func sortedScopes(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// IntersectScopes composes the scopes a set of gates permits. ADR 0030 §3:
// composition is INTERSECTION, because a union would let a permissive gate
// broaden a strict one and install a grant the strict gate never authorized.
// An empty intersection fails closed as an invalid policy configuration -- a
// defect in the rules, not an answer about the action.
func IntersectScopes(reqs []contract.RequirementRef) []string {
	if len(reqs) == 0 {
		return nil
	}
	acc := sortedScopes(reqs[0].Scopes)
	for _, r := range reqs[1:] {
		next := map[string]bool{}
		for _, s := range r.Scopes {
			next[s] = true
		}
		kept := acc[:0]
		for _, s := range acc {
			if next[s] {
				kept = append(kept, s)
			}
		}
		acc = kept
	}
	return acc
}

// OperatorFn is gate 2. It may block for as long as a human takes; the boundary
// runs it off the transport's event loop precisely so that it can.
//
// It receives the COMPLETE requirement set and the effective scopes, not the
// first requirement. ADR 0030 §3's whole point is that the operator answers
// once, having seen everything being asked -- handing over one of several is the
// deny-and-retry experience arriving by a different route, and it makes the
// computed scope intersection decorative.
type OperatorFn func(reqs []contract.RequirementRef, scopes []string) (approve bool)

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
	admissionClosed map[string]bool
	// waiting guards an execution awaiting an operator or another principal.
	waiting             map[string]bool
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
		waiting:           map[string]bool{},
	}
}

// setWaiting marks (or clears) an execution as awaiting a resolution -- an
// operator decision or another principal's answer. ADR 0030 §4: while it waits,
// further agent-initiated calls are rejected and a firing of the guard is an
// invariant violation.
func (b *Boundary) setWaiting(invocationID string, waiting bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if waiting {
		b.waiting[invocationID] = true
		return
	}
	delete(b.waiting, invocationID)
}

// IsWaiting reports the guard.
//
// The response half is DERIVED from the durable response-wait record rather
// than mirrored in a second flag. A parallel in-memory flag is lost on restart
// -- so a restarted Orchestrator would let an execution that is still awaiting
// an answer issue actions and even claim a terminal result -- and it can be
// cleared before the record it shadows, which is a window where the wait is
// open and unguarded.
func (b *Boundary) IsWaiting(invocationID string) bool {
	if _, awaiting := b.Recorder.AwaitingResponse(invocationID); awaiting {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.waiting[invocationID] || b.blocked[invocationID]
}

// recordGrant stores the operator's answer ON THE ATTEMPT. It is NOT promoted to
// a reusable decision here: a decision that outlived the action it was given for
// would apply twice, since the granting attempt itself never consumed it.
// Promotion happens only when that attempt goes stale before its commit point.
func (b *Boundary) recordGrant(att *Attempt, approved bool) {
	b.Recorder.mu.Lock()
	defer b.Recorder.mu.Unlock()
	if att.State == StateSettled {
		// The attempt was settled while the human was deciding. Writing the
		// grant now would attach an answer to an action that no longer exists,
		// and a promotion racing this would publish it.
		return
	}
	att.OperatorApproved = &approved
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
	b.admissionClosed[invocationID] = true
	b.mu.Unlock()
	b.Recorder.MarkAdmissionClosed(invocationID)
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
		b.Recorder.Settle(att, contract.OutcomeDenied, admissionClosedReason, DispositionBeforeCommit)
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
	// Mechanism 1 of ADR 0030 §5 is DRAIN -- stop an attempt before its commit
	// point, not merely wait for it. An attempt parked in a declared wait is
	// demonstrably before the effect, so it is stopped rather than waited on;
	// waiting for a human inside a fencing grace period would guarantee an
	// unconfirmed receipt every time.
	for _, a := range b.Recorder.ForInvocation(invocationID) {
		switch b.Recorder.State(a) {
		case StateOperatorWaiting, StateResourceWaiting:
			b.Recorder.SettleStaleAndPromote(a,
				"stopped before its commit point during the drain")
		case StateOpen, StateSettled:
			// An open attempt may commit at any moment; it must be waited on.
		}
	}

	deadline := time.Now().Add(within)
	for {
		outstanding = nil
		for _, a := range b.Recorder.ForInvocation(invocationID) {
			if b.Recorder.State(a) != StateSettled {
				outstanding = append(outstanding, a)
			}
		}
		if len(outstanding) == 0 {
			break
		}
		if time.Now().After(deadline) {
			return false, outstanding
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A commit that lands DURING the drain is not a defect -- it is what
	// draining is for. The guarantee ADR 0030 §5 needs is that nothing commits
	// after the receipt, and waiting for every attempt to settle before the
	// fence is what delivers it.
	//
	// A first version rejected any post-closure commit outright, which made
	// every cooperative cancellation unconfirmed: an action admitted before
	// closure and finishing inside the grace period is the ordinary case. The
	// fact is still recorded, because "two effects landed during the drain" is
	// worth being able to see; it just is not a failure.
	return true, nil
}

// CommittedDuringDrain reports attempts that committed after admission closed.
// Not a defect -- draining permits it -- but recorded, because an operator
// reading a cancellation wants to know what landed on the way out.
func (b *Boundary) CommittedDuringDrain(invocationID string) []*Attempt {
	var out []*Attempt
	for _, a := range b.Recorder.ForInvocation(invocationID) {
		if a.Disposition == DispositionCommitted && a.SettledAfterClose {
			out = append(out, a)
		}
	}
	return out
}

// ReopenAdmission restores admission for a NEW incarnation of the same
// execution. Closure ends an incarnation; the Story outlives it, and a
// successor dispatched afterwards must be admissible or a stale wait could
// never be recovered from.
func (b *Boundary) ReopenAdmission(invocationID string) {
	b.admitMu.Lock()
	defer b.admitMu.Unlock()
	b.mu.Lock()
	delete(b.admissionClosed, invocationID)
	b.mu.Unlock()
	b.Recorder.mu.Lock()
	delete(b.Recorder.closedAt, invocationID)
	b.Recorder.mu.Unlock()
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
func (b *Boundary) Submit(inv *contract.Invocation, req contract.ActionRequest, done chan<- contract.ActionResult) (*Attempt, error) {
	if res, refused := b.refuseWhileWaiting(inv, req); refused {
		go func() { done <- res }()
		return nil, nil
	}
	// Registration is SYNCHRONOUS and the gates are not. The caller
	// acknowledges the event only after this returns, so the intent is durable
	// before the sender is told it may discard the request -- a crash between
	// the acknowledgement and the open would otherwise lose an action the
	// sender had already released.
	att, fresh, err := b.register(inv.ID(), req)
	if err != nil && att == nil {
		return nil, err
	}
	go func() { done <- b.executeRegistered(inv, req, att, fresh, err) }()
	return att, nil
}

// ExecuteSync submits an action and waits for it. It exists for the in-process
// boundary claims, which are about the gates rather than about the transport;
// nothing on the wire path uses it, because a synchronous wait there is the
// defect Submit exists to avoid.
func (b *Boundary) ExecuteSync(inv *contract.Invocation, req contract.ActionRequest) contract.ActionResult {
	return b.execute(inv, req)
}

func (b *Boundary) execute(inv *contract.Invocation, req contract.ActionRequest) contract.ActionResult {
	if res, refused := b.refuseWhileWaiting(inv, req); refused {
		return res
	}
	att, fresh, err := b.register(inv.ID(), req)
	return b.executeRegistered(inv, req, att, fresh, err)
}

// refuseWhileWaiting rejects a genuinely NEW call while the execution awaits a
// resolution, BEFORE any record is opened.
//
// An existing correlation is still served: that is a retry or a replay of an
// action already admitted, and refusing it would break at-most-once. A first
// version ran this guard after registration, so a rejected new call left an
// unsettled attempt behind -- one the drain would then wait on.
func (b *Boundary) refuseWhileWaiting(inv *contract.Invocation, req contract.ActionRequest) (contract.ActionResult, bool) {
	id := inv.ID()
	if !b.IsWaiting(id) {
		return contract.ActionResult{}, false
	}
	if _, known := b.Recorder.Lookup(id, req.Correlation); known {
		return contract.ActionResult{}, false
	}
	b.mu.Lock()
	b.InvariantViolations = append(b.InvariantViolations,
		fmt.Sprintf("invocation %s issued %s while awaiting resolution", id, req.Action))
	b.mu.Unlock()

	// Opened and completed together (ADR 0030 §8). An earlier version recorded
	// NOTHING -- on my reading that rejecting before opening a record meant
	// opening no record at all. It means never leaving an UNSETTLED one; the
	// denial itself is exactly the observation policy work is tuned against,
	// and losing it is not a small loss.
	const why = "story is awaiting resolution"
	att := b.Recorder.OpenSettled(id, req, contract.OutcomeDenied, why, DispositionBeforeCommit)
	return contract.ActionResult{
		Correlation: req.Correlation,
		AttemptID:   att.ID,
		Outcome:     contract.OutcomeDenied,
		Reason:      why,
	}, true
}

//nolint:gocyclo // The gate sequence is the subject; splitting it hides it.
func (b *Boundary) executeRegistered(
	inv *contract.Invocation, req contract.ActionRequest,
	att *Attempt, fresh bool, err error,
) contract.ActionResult {
	id := inv.ID()

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
			Correlation:  req.Correlation,
			AttemptID:    att.ID,
			Outcome:      att.Outcome,
			Reason:       "replayed: " + att.Reason,
			Result:       att.Result,
			Requirements: att.Requirements,
			Scopes:       att.EffectiveScopes,
		}
	}

	// ---- Gate 1: admission, then policy ----
	if err := b.admit(inv, req.Action); err != nil {
		b.Recorder.Settle(att, contract.OutcomeDenied, err.Error(), DispositionBeforeCommit)
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeDenied, Reason: err.Error()}
	}

	decision, requirements := b.Policy(inv, req.Action, req.Arguments)
	switch decision {
	case DecisionDeny:
		reason := "denied by policy"
		if len(requirements) > 0 {
			reason = requirements[0].Statement
		}
		b.Recorder.Settle(att, contract.OutcomeDenied, reason, DispositionBeforeCommit)
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeDenied, Reason: reason}

	case DecisionRequiresOperator:
		if len(requirements) == 0 {
			requirements = []contract.RequirementRef{
				{GateID: "unnamed", Statement: "an operator is required", Scopes: []string{"once"}}}
		}
		for i := range requirements {
			requirements[i].AttemptID = att.ID
		}
		scopes := IntersectScopes(requirements)
		if len(scopes) == 0 {
			// ADR 0030 §3: two gates sharing no permitted scope describe an
			// action no operator can answer. That is a defect in the rules, not
			// an answer about the action, and it must surface as one.
			const why = "invalid policy configuration: the collected gates share no permitted scope"
			b.Recorder.Settle(att, contract.OutcomeFailed, why, DispositionBeforeCommit)
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeFailed, Reason: why}
		}
		att.Requirements = requirements
		att.CanonicalSet = CanonicalRequirements(requirements)
		att.EffectiveScopes = scopes
		requirement := &requirements[0]
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
			b.Recorder.Settle(att, contract.OutcomeBlocked, requirement.Statement, DispositionBeforeCommit)
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeBlocked, Reason: requirement.Statement,
				Requirements: att.Requirements, Scopes: att.EffectiveScopes}
		}

		b.Recorder.Transition(att, StateOperatorWaiting)
		// The Story is awaiting resolution for an INTERACTIVE wait too, not only
		// a headless block. ADR 0030 §4 blocks the caller so no LLM turn happens
		// while a gate is open; a first version set the guard only when there
		// was no responder, so the case the rule was written for went unguarded.
		b.setWaiting(id, true)
		res := b.completeAction(inv, att, req, true)
		b.setWaiting(id, false)
		return res

	case DecisionAllow:
		// fall through
	}

	return b.completeAction(inv, att, req, false)
}

// completeAction runs gates 2 and 3 for an already-registered attempt.
//
// It has exactly ONE caller. An earlier version exposed it so a preserved wait
// could be resumed after a restart, which put two continuations over one
// attempt -- the race detector found both of them settling it. Recovery is
// artifact-level (§6); a wait does not resume.
func (b *Boundary) completeAction(inv *contract.Invocation, att *Attempt, req contract.ActionRequest, needOperator bool) contract.ActionResult {

	if needOperator {
		requirement := att.Requirement
		if requirement == nil {
			requirement = &contract.RequirementRef{GateID: "unnamed", Statement: "an operator is required"}
		}
		// A decision already given for this logical action is consumed rather
		// than re-asked. That is what makes going stale on restart acceptable:
		// the action is re-requested, but the human is not.
		//
		// It is consumed EXACTLY ONCE. A first version left it in place, so
		// `approve_once` became permanent and an intentional repetition was
		// indistinguishable from a recovery -- one approved push approving
		// every later push of the same ref.
		binding := req.Action.String() + "\x00" + att.ArgsDigest
		att.Binding = binding
		if approved, found := b.Recorder.ConsumeDecision(inv.ID(), binding, att.CanonicalSet); found {
			if !approved {
				b.Recorder.Settle(att, contract.OutcomeDenied, "operator denied (recorded)", DispositionBeforeCommit)
				return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
					Outcome: contract.OutcomeDenied, Reason: "operator denied (recorded)"}
			}
			b.Recorder.Transition(att, StateOpen)
			goto gate3
		}
		if b.Operator == nil || !b.Operator(att.Requirements, att.EffectiveScopes) {
			b.recordGrant(att, false)
			b.Recorder.Settle(att, contract.OutcomeDenied, "operator denied", DispositionBeforeCommit)
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeDenied, Reason: "operator denied"}
		}
		b.recordGrant(att, true)
	}

gate3:

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
	// If the attempt was settled while this continuation waited -- by the drain
	// stopping it, or by reconciliation making it stale -- STOP. Marking the
	// record and letting the continuation run on is "invalidate the attempt"
	// under another name, which ADR 0030 §5 rejects precisely because a mark
	// nothing checks does not prevent a commit. The conformance suite caught
	// exactly that: the drain reported the wait stopped and the goroutine
	// committed anyway.
	if b.Recorder.State(att) == StateSettled {
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: att.Outcome, Reason: att.Reason}
	}

	// ADR 0030 §5: gate 3 re-evaluates every DETERMINISTIC condition, and an
	// unchanged policy that now denies still denies. It does not re-raise the
	// operator requirement the decision answered -- but if the canonical
	// requirement SET is no longer identical to the one gate 1 recorded, the
	// action is stale and a fresh one is required. The answer is void rather
	// than supplemented; the action never accumulates approvals.
	if needOperator {
		nowDecision, nowReqs := b.Policy(inv, req.Action, req.Arguments)
		if nowDecision == DecisionDeny {
			const why = "policy denies on revalidation"
			b.Recorder.Settle(att, contract.OutcomeDenied, why, DispositionBeforeCommit)
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeDenied, Reason: why}
		}
		for i := range nowReqs {
			nowReqs[i].AttemptID = att.ID
		}
		if CanonicalRequirements(nowReqs) != att.CanonicalSet {
			// Deliberately NOT promoted: the set changed, so the answer given is
			// void rather than available to a successor.
			const why = "the requirement set changed after approval; a fresh action is required"
			b.Recorder.Settle(att, contract.OutcomeStale, why, DispositionBeforeCommit)
			return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
				Outcome: contract.OutcomeStale, Reason: why}
		}
	}

	// Approval clears the human requirement and nothing else; everything
	// deterministic is still re-checked immediately before the effect.
	if err := b.revalidate(inv, req.Action); err != nil {
		b.Recorder.Settle(att, contract.OutcomeDenied, err.Error(), DispositionBeforeCommit)
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeDenied, Reason: err.Error()}
	}

	exec, ok := b.Executors[req.Action.String()]
	if !ok {
		b.Recorder.Settle(att, contract.OutcomeFailed, "no executor for action", DispositionBeforeCommit)
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeFailed, Reason: "no executor for action"}
	}
	b.Recorder.Transition(att, StateOpen)
	result, err := exec(inv, req.Arguments)
	if err != nil {
		b.Recorder.Settle(att, contract.OutcomeFailed, err.Error(), DispositionCommitted)
		return contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,
			Outcome: contract.OutcomeFailed, Reason: err.Error()}
	}
	b.Recorder.SettleWith(att, contract.OutcomeSucceeded, "", DispositionCommitted, result)
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
		if att.State == StateSettled && att.Outcome == contract.OutcomeBlocked {
			// The COMPLETE set. Reporting the first would tell an operator one
			// of several things the action is waiting on.
			out = append(out, att.Requirements...)
		}
	}
	return out
}
