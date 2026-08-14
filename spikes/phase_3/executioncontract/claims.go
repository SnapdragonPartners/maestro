package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"maestro-spike/phase3/executioncontract/contract"
	"maestro-spike/phase3/executioncontract/host"
)

// Boundary and schema claims. These do NOT spawn a process: they exercise
// properties of the boundary and of the terminal-result schema that the wire
// scenarios depend on but cannot isolate. They are labelled separately so
// nothing here is read as evidence about the wire.
//
// The one exception is restart/no-reissue, which spawns TWO processes on
// purpose, because that is the whole claim.

type boundaryClaim struct {
	name  string
	about string
	fn    func(binary string) (Outcome, string)
}

func (r *runner) runBoundary(bc boundaryClaim) {
	outcome, detail := bc.fn(r.binary)
	r.claims = append(r.claims, claim{name: bc.name, about: bc.about, outcome: outcome, detail: detail})
	fmt.Printf("%-10s %-54s %s\n", outcome, bc.name, detail)
}

func boundaryClaims() []boundaryClaim {
	return []boundaryClaim{
		{"schema/applicability-rule-both-directions",
			"§5 — a required axis must be present AND an inapplicable one absent",
			claimApplicability},
		{"boundary/blocked-caller-is-an-invariant-violation",
			"ADR 0030 §4 — not an ordinary denial", claimBlockedCallerGuard},
		{"boundary/stale-generation-rejected-late",
			"ADR 0029 §7 req 5 — a late call from a fenced holder", claimStaleGeneration},
		{"boundary/amended-version-rejected-at-admission",
			"ADR 0019 version-bound dispatch", claimAmendedVersion},
		{"boundary/settled-retry-replays-its-result",
			"ADR 0030 §3 — a retry is not a new action", claimAtMostOnce},
		{"boundary/outstanding-retry-re-enters-no-gate",
			"ADR 0030 §3 — only a SETTLED attempt may replay", claimOutstandingRetry},
		{"reconcile/preserves-an-operator-wait",
			"§6 — an operator wait is healthy, not interrupted", claimReconcileOperatorWait},
		{"reconcile/hands-back-a-resource-wait",
			"§6 — a resource wait's operation does not survive a restart", claimReconcileResourceWait},
		{"boundary/correlation-is-bound-to-its-logical-action",
			"ADR 0030 §3 — a key alone can replay the wrong work", claimCorrelationBinding},
		{"boundary/admission-closure-linearizes-with-registration",
			"ADR 0029 §7 step 2 — revoke before drain, with no window between", claimAdmissionRace},
		{"reconcile/operator-wait-resumes-from-its-record",
			"§6 — preserving a row is not recovering the action", claimResumeOperatorWait},
		{"events/prior-epoch-replay-deduped-across-restart",
			"§4 — deduplication state must outlive the process", claimDurableWatermark},
		{"restart/does-not-reissue-a-settled-action",
			"§6 re-attach across a restart; the Orchestrator enumerates", claimRestartNoReissue},
	}
}

// claimCorrelationBinding proves a correlation is bound to the action and the
// argument digest, not merely to itself.
//
// Keyed on the correlation alone, the same key returns whatever attempt it
// opened first -- so a runtime that reuses a key replays the result of a
// different action, or of the same action with different arguments, and the
// boundary reports success for work it never did.
func claimCorrelationBinding(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-corr", false)

	first := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "k", Action: capRead, Arguments: json.RawMessage(`{"a":1}`)})
	if first.Outcome != contract.OutcomeSucceeded {
		return Errored, "control failed: " + first.Reason
	}

	// Same key, different ACTION.
	other := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "k", Action: capPublish, Arguments: json.RawMessage(`{"a":1}`)})
	if other.Outcome != contract.OutcomeDenied {
		return Falsified, "a correlation reused for a different action returned " + string(other.Outcome)
	}
	if !strings.Contains(other.Reason, "reused for") {
		return Falsified, "refused for the wrong reason: " + other.Reason
	}
	if h.publishedCount() != 0 {
		return Falsified, "the mismatched reuse committed an effect"
	}

	// Same key, same action, different ARGUMENTS.
	args := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "k", Action: capRead, Arguments: json.RawMessage(`{"a":2}`)})
	if args.Outcome != contract.OutcomeDenied {
		return Falsified, "a correlation reused with different arguments returned " + string(args.Outcome)
	}
	if !strings.Contains(args.Reason, "different arguments") {
		return Falsified, "refused for the wrong reason: " + args.Reason
	}
	return Proven, "reuse refused for a different action and for different arguments"
}

// claimAdmissionRace proves closure and registration linearize.
//
// Read under one lock and registered under another, an attempt can be admitted
// after closure -- so the drain would be waiting on a set that grew after it was
// closed, which is exactly the window ADR 0029 §7 step 2's ordering exists to
// eliminate.
// It is checked deterministically rather than by hoping a race detector trips:
// a registration is held open, and CloseAdmission must not be able to RETURN
// while it is in flight. If it can, the drain it precedes is closing over a set
// that is still growing.
func claimAdmissionRace(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-race", false)

	inRegistration := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	h.boundary.InRegistration = func() {
		once.Do(func() {
			close(inRegistration)
			<-release
		})
	}

	done := make(chan contract.ActionResult, 1)
	go h.boundary.Submit(inv, contract.ActionRequest{
		Correlation: "c", Action: capPublish, Arguments: json.RawMessage(`{}`)}, done)

	select {
	case <-inRegistration:
	case <-time.After(2 * time.Second):
		return Errored, "registration never entered the critical section"
	}

	closed := make(chan struct{})
	go func() { h.boundary.CloseAdmission(inv.ID()); close(closed) }()

	// With the two linearized, CloseAdmission cannot return yet.
	select {
	case <-closed:
		return Falsified, "CloseAdmission returned while a registration was in flight, " +
			"so an attempt can be admitted after admission is closed"
	case <-time.After(250 * time.Millisecond):
	}

	close(release)
	<-closed
	<-done

	// A concurrent sweep as well, under whatever scheduling the runtime gives:
	// every attempt must end settled, or a drain could never complete.
	for i := range 100 {
		h2 := newHarness(sampleDiff)
		inv2 := invocation(fmt.Sprintf("inv-race-%d", i), false)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); h2.boundary.CloseAdmission(inv2.ID()) }()
		go func() {
			defer wg.Done()
			h2.boundary.ExecuteSync(inv2, contract.ActionRequest{
				Correlation: "c", Action: capPublish, Arguments: json.RawMessage(`{}`)})
		}()
		wg.Wait()
		for _, a := range h2.recorder.Attempts() {
			if a.State != host.StateSettled {
				return Falsified, fmt.Sprintf("round %d left %s in %s", i, a.ID, a.State)
			}
			if a.Outcome == contract.OutcomeDenied &&
				strings.Contains(a.Reason, "admission closed") && h2.publishedCount() != 0 {
				return Falsified, fmt.Sprintf(
					"round %d refused %s for admission closure yet the effect committed", i, a.ID)
			}
		}
	}
	return Proven, "CloseAdmission blocked on an in-flight registration; 100 concurrent rounds all settled"
}

// claimResumeOperatorWait proves a preserved wait is RECOVERABLE, not merely
// preserved.
//
// The previous claim kept a live goroutine parked in the operator gate, so it
// demonstrated that the row survived and nothing more. Here the continuation is
// abandoned exactly as an Orchestrator restart abandons it, and the action is
// re-driven from the attempt's own persisted request.
func claimResumeOperatorWait(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-resume", true)
	h.boundary.Policy = requiresOperatorFor(capForge)

	// Incarnation 1: the operator never answers, and the continuation dies.
	abandoned := make(chan struct{})
	h.boundary.Operator = func(contract.RequirementRef) bool {
		<-abandoned // never returns during this incarnation
		return false
	}
	h.boundary.Submit(inv, contract.ActionRequest{
		Correlation: "c1", Action: capForge, Arguments: json.RawMessage(`{"ref":"refs/heads/x"}`)},
		make(chan contract.ActionResult, 1))

	deadline := time.Now().Add(2 * time.Second)
	var att *host.Attempt
	for time.Now().Before(deadline) {
		if a, ok := h.recorder.Lookup(inv.ID(), "c1"); ok && a.State == host.StateOperatorWaiting {
			att = a
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if att == nil {
		return Errored, "the attempt never reached operator_waiting"
	}

	// Restart: reconciliation preserves the wait, and the record must carry
	// enough to finish the action without the lost continuation.
	rep := h.recorder.Reconcile()
	if len(rep.OperatorWaitsPreserved) != 1 {
		return Falsified, "the wait was not preserved"
	}
	if rep.OperatorWaitsPreserved[0].Request.Correlation == "" ||
		rep.OperatorWaitsPreserved[0].Request.Action != capForge {
		return Falsified, "the preserved wait carries no executable request, so it is stuck rather than recoverable"
	}

	// Incarnation 2 answers it, driving gate 3 from the record alone.
	h.boundary.Operator = func(contract.RequirementRef) bool { return true }
	done := make(chan contract.ActionResult, 1)
	h.boundary.ResumeWait(inv, rep.OperatorWaitsPreserved[0], done)
	res := <-done
	close(abandoned)

	if res.Outcome != contract.OutcomeSucceeded {
		return Falsified, "the resumed action settled " + string(res.Outcome) + ": " + res.Reason
	}
	if att.State != host.StateSettled {
		return Falsified, "the attempt is still " + string(att.State)
	}
	return Proven, "the wait was resumed from its own record and completed through gate 3"
}

// claimDurableWatermark proves deduplication survives the process.
//
// An in-memory watermark is reset by a restart, so a replayed usage event from
// the previous incarnation is counted a second time -- a corrupted cost figure
// rather than a harmless duplicate.
func claimDurableWatermark(binary string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-watermark", false)
	rt := &host.Runtime{BinaryPath: binary, Boundary: h.boundary,
		Fencer: func(contract.ResourceRef) host.FenceReceipt { return host.FenceTerminated }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Positive control: the first incarnation runs cleanly.
	_ = os.Setenv("SPIKE_MODE", "normal")
	first := rt.Run(ctx, inv)
	if first.Result.Status != contract.StatusCompleted {
		return Errored, "the first run did not complete: " + string(first.Result.Status)
	}

	// A second incarnation replays an event from epoch 1. The MECHANISM is
	// asserted directly: an earlier version gated on the watermark being
	// non-zero first, so a mutation that emptied the watermark tripped the
	// precondition and reported ERROR -- inconclusive where the behaviour was
	// in fact broken and observable.
	mark := h.recorder.Watermark(inv.ID(), 1)
	inv.Bindings.Epoch = 2
	_ = os.Setenv("SPIKE_MODE", "replay_prior_epoch")
	second := rt.Run(ctx, inv)

	if second.DuplicateEvents == 0 {
		return Falsified, fmt.Sprintf(
			"a replayed prior-epoch event was accepted after the restart (watermark %d)", mark)
	}
	if len(second.Usage) != 0 {
		return Falsified, "the replayed usage report was counted again"
	}
	if mark == 0 {
		return Falsified, "nothing was committed to the watermark, so the drop was not the watermark's doing"
	}
	return Proven, fmt.Sprintf("epoch-1 watermark %d survived the restart; %d replay(s) dropped",
		mark, second.DuplicateEvents)
}

// claimApplicability is the schema's own regression surface. Each case is a
// shape the four-axis result must refuse; a validator checking only "required
// axis present" would accept half of them.
func claimApplicability(string) (Outcome, string) {
	changed := contract.DispositionChanged
	superseded := contract.ReasonSuperseded
	cls := contract.ClassNonRetryableAgent

	type tc struct {
		name string
		res  contract.TerminalResult
		bad  bool
	}
	cases := []tc{
		{"completed+disposition", contract.Completed(changed, ""), false},
		{"cancelled+reason", contract.Cancelled(superseded, ""), false},
		{"failed+class", contract.Failed(cls, ""), false},
		{"timed_out with NO class", contract.TimedOut(""), false},
		{"blocked+requirement", contract.Blocked([]contract.RequirementRef{{GateID: "g"}}, ""), false},

		{"completed without disposition", contract.TerminalResult{Status: contract.StatusCompleted}, true},
		{"cancelled without reason", contract.TerminalResult{Status: contract.StatusCancelled}, true},
		{"failed without class", contract.TerminalResult{Status: contract.StatusFailed}, true},
		{"blocked without requirement", contract.TerminalResult{Status: contract.StatusBlocked}, true},

		// timed_out must not be forced into a classification it does not have.
		{"timed_out WITH a class", contract.TerminalResult{
			Status: contract.StatusTimedOut, FailureClass: &cls}, true},

		{"completed WITH failure class", contract.TerminalResult{
			Status: contract.StatusCompleted, Disposition: &changed, FailureClass: &cls}, true},
		{"completed WITH cancel reason", contract.TerminalResult{
			Status: contract.StatusCompleted, Disposition: &changed, CancelReason: &superseded}, true},
		{"failed WITH disposition", contract.TerminalResult{
			Status: contract.StatusFailed, FailureClass: &cls, Disposition: &changed}, true},
		{"cancelled WITH requirements", contract.TerminalResult{
			Status: contract.StatusCancelled, CancelReason: &superseded,
			BlockedOn: []contract.RequirementRef{{GateID: "g"}}}, true},
		{"unknown status", contract.TerminalResult{Status: "finished"}, true},
	}

	var wrong []string
	valid := 0
	for _, c := range cases {
		if !c.bad {
			valid++
		}
		err := c.res.Validate()
		if c.bad && err == nil {
			wrong = append(wrong, "accepted: "+c.name)
		}
		if !c.bad && err != nil {
			wrong = append(wrong, "rejected valid: "+c.name+" ("+err.Error()+")")
		}
	}
	if len(wrong) > 0 {
		return Falsified, fmt.Sprintf("%v", wrong)
	}
	return Proven, fmt.Sprintf("%d shapes: %d valid accepted, %d invalid refused",
		len(cases), valid, len(cases)-valid)
}

func claimBlockedCallerGuard(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	h.boundary.Policy = requiresOperatorFor(capForge)
	inv := invocation("inv-guard", false)

	first := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "c1", Action: capForge, Arguments: json.RawMessage(`{}`)})
	if first.Outcome != contract.OutcomeBlocked {
		return Errored, "the gated action did not block: " + string(first.Outcome)
	}
	if !h.boundary.IsBlocked(inv.ID()) {
		return Falsified, "the Story is not marked awaiting resolution"
	}

	// A further agent-initiated action must be rejected AND recorded as an
	// invariant violation: it means something upstream let a blocked caller
	// keep working.
	second := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "c2", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if second.Outcome != contract.OutcomeDenied {
		return Falsified, "a blocked caller's action was admitted"
	}
	if len(h.boundary.InvariantViolations) != 1 {
		return Falsified, fmt.Sprintf("%d invariant violations recorded, want 1",
			len(h.boundary.InvariantViolations))
	}
	return Proven, "rejected and recorded as an invariant violation, not an ordinary denial"
}

func claimStaleGeneration(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-gen", false)

	ok := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "c1", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if ok.Outcome != contract.OutcomeSucceeded {
		return Errored, "control failed: " + ok.Reason
	}

	// The resource is fenced and replaced; the binding's reference is stale.
	h.boundary.CurrentGeneration["inc-1"] = 5
	late := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "c2", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if late.Outcome != contract.OutcomeDenied {
		return Falsified, "a late call from a stale generation was admitted"
	}
	return Proven, "control passed, then the stale-generation call was rejected: " + late.Reason
}

func claimAmendedVersion(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-amend", false)

	ok := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "c1", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if ok.Outcome != contract.OutcomeSucceeded {
		return Errored, "control failed: " + ok.Reason
	}

	h.boundary.EffectiveVersion = 8 // the Story was amended
	after := h.boundary.ExecuteSync(inv, contract.ActionRequest{
		Correlation: "c2", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if after.Outcome != contract.OutcomeDenied {
		return Falsified, "an action bound to a superseded version was admitted"
	}
	return Proven, "control passed, then the superseded-version call was rejected"
}

func claimAtMostOnce(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-once", false)

	req := contract.ActionRequest{Correlation: "same", Action: capPublish,
		Arguments: json.RawMessage(`{"n":1}`)}
	first := h.boundary.ExecuteSync(inv, req)
	second := h.boundary.ExecuteSync(inv, req)

	if first.Outcome != contract.OutcomeSucceeded {
		return Errored, "control failed: " + first.Reason
	}
	if h.publishedCount() != 1 {
		return Falsified, fmt.Sprintf("the effect committed %d times", h.publishedCount())
	}
	if second.AttemptID != first.AttemptID {
		return Falsified, "a retry minted a second attempt identity"
	}
	if second.Outcome != contract.OutcomeSucceeded {
		return Falsified, "the replay did not return the settled outcome: " + string(second.Outcome)
	}
	if n := len(h.recorder.Attempts()); n != 1 {
		return Falsified, fmt.Sprintf("%d attempts recorded for one logical action", n)
	}
	return Proven, "one attempt, one effect, settled result replayed on the retry"
}

// claimOutstandingRetry covers the half at-most-once did not: a duplicate for an
// attempt that is still IN FLIGHT.
//
// Replaying only settled results leaves the outstanding case falling straight
// through into policy, operator handling, and resource acquisition a second
// time -- a second pass at one logical action, which is what the rule forbids.
func claimOutstandingRetry(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-outstanding", false)
	h.boundary.ResourceDelay[capPublish.String()] = 500 * time.Millisecond

	req := contract.ActionRequest{Correlation: "dup", Action: capPublish,
		Arguments: json.RawMessage(`{"n":1}`)}

	done := make(chan contract.ActionResult, 2)
	h.boundary.Submit(inv, req, done)

	// Let the first submission reach its resource wait, then duplicate it.
	time.Sleep(120 * time.Millisecond)
	dup := h.boundary.ExecuteSync(inv, req)

	if dup.Outcome != contract.OutcomeOutstanding {
		return Falsified, "an in-flight duplicate returned " + string(dup.Outcome) +
			" rather than outstanding"
	}

	first := <-done
	if first.Outcome != contract.OutcomeSucceeded {
		return Errored, "control failed: " + first.Reason
	}
	if h.publishedCount() != 1 {
		return Falsified, fmt.Sprintf("the effect committed %d times", h.publishedCount())
	}
	if n := len(h.recorder.Attempts()); n != 1 {
		return Falsified, fmt.Sprintf("%d attempts for one logical action", n)
	}
	return Proven, "the in-flight duplicate re-entered no gate; one attempt, one effect"
}

// claimReconcileOperatorWait proves reconciliation preserves a healthy wait
// rather than settling it unknown -- and that it VALIDATES rather than merely
// skipping, since a wait with no requirement is a defect and not a state.
func claimReconcileOperatorWait(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-recon-op", true)
	h.boundary.Policy = requiresOperatorFor(capForge)

	release := make(chan struct{})
	h.boundary.Operator = func(contract.RequirementRef) bool {
		<-release
		return true
	}
	done := make(chan contract.ActionResult, 1)
	h.boundary.Submit(inv, contract.ActionRequest{
		Correlation: "c1", Action: capForge, Arguments: json.RawMessage(`{}`)}, done)

	// Wait until the attempt is genuinely in the operator wait.
	deadline := time.Now().Add(2 * time.Second)
	var att *host.Attempt
	for time.Now().Before(deadline) {
		if a, ok := h.recorder.Lookup(inv.ID(), "c1"); ok && a.State == host.StateOperatorWaiting {
			att = a
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if att == nil {
		return Errored, "the attempt never reached operator_waiting"
	}

	rep := h.recorder.Reconcile()
	close(release)
	<-done

	if len(rep.SettledUnknown) != 0 {
		return Falsified, "reconciliation settled a healthy operator wait as unknown"
	}
	if len(rep.OperatorWaitsPreserved) != 1 {
		return Falsified, fmt.Sprintf("%d operator waits preserved, want 1",
			len(rep.OperatorWaitsPreserved))
	}
	if rep.OperatorWaitsPreserved[0].Requirement == nil {
		return Falsified, "the preserved wait carries no requirement, so it was not validated"
	}
	return Proven, "the operator wait survived reconciliation with its requirement intact"
}

// claimReconcileResourceWait proves the other half of the same rule: a resource
// wait is neither settled nor left alone. Its provisioning operation does not
// survive a restart, so it must come back for restoration.
func claimReconcileResourceWait(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-recon-res", false)
	h.boundary.ResourceDelay[capPublish.String()] = 700 * time.Millisecond

	done := make(chan contract.ActionResult, 1)
	h.boundary.Submit(inv, contract.ActionRequest{
		Correlation: "c1", Action: capPublish, Arguments: json.RawMessage(`{}`)}, done)

	deadline := time.Now().Add(2 * time.Second)
	var att *host.Attempt
	for time.Now().Before(deadline) {
		if a, ok := h.recorder.Lookup(inv.ID(), "c1"); ok && a.State == host.StateResourceWaiting {
			att = a
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if att == nil {
		return Errored, "the attempt never reached resource_waiting"
	}

	rep := h.recorder.Reconcile()
	<-done

	if len(rep.SettledUnknown) != 0 {
		return Falsified, "reconciliation settled a resource wait as unknown"
	}
	if len(rep.ResourceWaitsToRestore) != 1 {
		return Falsified, fmt.Sprintf("%d resource waits handed back, want 1",
			len(rep.ResourceWaitsToRestore))
	}
	if rep.ResourceWaitsToRestore[0].ResourceOp == "" {
		return Falsified, "the wait names no operation, so nothing could restore it"
	}
	if len(rep.Invalid) != 0 {
		return Falsified, "a valid wait was reported invalid"
	}
	return Proven, "the resource wait was preserved and handed back as " +
		rep.ResourceWaitsToRestore[0].ResourceOp
}

// claimRestartNoReissue is the honest form of re-attach on a stdio transport.
//
// A broken stdio transport IS a dead process, so there is no reconnection to a
// live runtime to test. The case that matters in Phase 3 is a RESTARTED runtime
// rejoining an existing execution: one process settles its actions, then a
// second process for the same execution asks what is outstanding and must not
// reissue anything.
func claimRestartNoReissue(binary string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-restart", false)

	_ = os.Setenv("SPIKE_MODE", "normal")
	rt := &host.Runtime{BinaryPath: binary, Boundary: h.boundary,
		Fencer: func(contract.ResourceRef) host.FenceReceipt { return host.FenceTerminated }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first := rt.Run(ctx, inv)
	if first.Result.Status != contract.StatusCompleted {
		return Errored, "the first run did not complete: " + string(first.Result.Status)
	}
	before := len(h.recorder.Attempts())

	// Restart: a new incarnation of the same execution. The immutable
	// ExecutionConfig is reused verbatim; only the bindings advance.
	inv.Bindings.Epoch = 2
	_ = os.Setenv("SPIKE_MODE", "restart_reattach")
	second := rt.Run(ctx, inv)
	after := len(h.recorder.Attempts())

	if second.Result.Status != contract.StatusCompleted {
		return Falsified, "the restarted runtime did not complete: " + string(second.Result.Status)
	}
	if after != before {
		return Falsified, fmt.Sprintf("the restart minted %d new attempt(s)", after-before)
	}
	if h.publishedCount() != 1 {
		return Falsified, fmt.Sprintf("the effect committed %d times across the restart",
			h.publishedCount())
	}
	return Proven, fmt.Sprintf("re-attached to %d settled attempt(s); nothing reissued, one effect", before)
}
