package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"maestro-spike/phase3/executioncontract/contract"
	"maestro-spike/phase3/executioncontract/host"
)

// Boundary and schema claims. These do NOT spawn a process: they exercise
// properties of the boundary and of the terminal-result schema that the wire
// scenarios above depend on but cannot isolate. They are labelled separately so
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
	fmt.Printf("%-10s %-52s %s\n", outcome, bc.name, detail)
}

func boundaryClaims() []boundaryClaim {
	return []boundaryClaim{
		{
			name:  "schema/applicability-rule-both-directions",
			about: "§5 — a required axis must be present AND an inapplicable one absent",
			fn:    claimApplicability,
		},
		{
			name:  "boundary/blocked-caller-is-an-invariant-violation",
			about: "ADR 0030 §4 — not an ordinary denial",
			fn:    claimBlockedCallerGuard,
		},
		{
			name:  "boundary/stale-generation-rejected-late",
			about: "ADR 0029 §7 req 5 — a late call from a fenced holder",
			fn:    claimStaleGeneration,
		},
		{
			name:  "boundary/amended-version-rejected-at-admission",
			about: "ADR 0019 version-bound dispatch",
			fn:    claimAmendedVersion,
		},
		{
			name:  "boundary/at-most-once-per-correlation",
			about: "ADR 0030 §3 — a transport retry is not a new action",
			fn:    claimAtMostOnce,
		},
		{
			name:  "restart/does-not-reissue-a-settled-action",
			about: "§6 re-attach across a restart; correlations must be derivable",
			fn:    claimRestartNoReissue,
		},
	}
}

// claimApplicability is the schema's own regression surface. Each case is a
// shape the four-axis result must refuse; a validator that only checked
// "required axis present" would accept half of them.
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
		{"timed_out+class", contract.TimedOut(cls, ""), false},
		{"blocked+requirement", contract.Blocked([]contract.RequirementRef{{GateID: "g"}}, ""), false},

		{"completed without disposition", contract.TerminalResult{Status: contract.StatusCompleted}, true},
		{"cancelled without reason", contract.TerminalResult{Status: contract.StatusCancelled}, true},
		{"failed without class", contract.TerminalResult{Status: contract.StatusFailed}, true},
		{"timed_out without class", contract.TerminalResult{Status: contract.StatusTimedOut}, true},
		{"blocked without requirement", contract.TerminalResult{Status: contract.StatusBlocked}, true},

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
	for _, c := range cases {
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
	return Proven, fmt.Sprintf("%d shapes: 5 valid accepted, 10 invalid refused", len(cases))
}

func claimBlockedCallerGuard(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	h.boundary.Policy = requiresOperatorFor(capForge)
	inv := invocation("inv-guard", defaultCaps(), false)

	// First: the gated action blocks the Story.
	first := h.boundary.Execute(inv, contract.ActionRequest{
		Correlation: "c1", Action: capForge, Arguments: json.RawMessage(`{}`)})
	if first.Outcome != contract.OutcomeDenied {
		return Errored, "the gated action did not block: " + string(first.Outcome)
	}
	if !h.boundary.IsBlocked(inv.ID) {
		return Falsified, "the Story is not marked awaiting resolution"
	}

	// Then: a further agent-initiated action must be rejected AND recorded as
	// an invariant violation, because it means something upstream let a blocked
	// caller keep working.
	second := h.boundary.Execute(inv, contract.ActionRequest{
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
	inv := invocation("inv-gen", defaultCaps(), false)

	// A positive control first: the action succeeds while the generation holds.
	ok := h.boundary.Execute(inv, contract.ActionRequest{
		Correlation: "c1", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if ok.Outcome != contract.OutcomeSucceeded {
		return Errored, "control failed: " + ok.Reason
	}

	// The resource is fenced and replaced; the invocation's reference is stale.
	h.boundary.CurrentGeneration["inc-1"] = 5
	late := h.boundary.Execute(inv, contract.ActionRequest{
		Correlation: "c2", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if late.Outcome != contract.OutcomeDenied {
		return Falsified, "a late call from a stale generation was admitted"
	}
	return Proven, "control passed, then the stale-generation call was rejected: " + late.Reason
}

func claimAmendedVersion(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-amend", defaultCaps(), false)

	ok := h.boundary.Execute(inv, contract.ActionRequest{
		Correlation: "c1", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if ok.Outcome != contract.OutcomeSucceeded {
		return Errored, "control failed: " + ok.Reason
	}

	h.boundary.EffectiveVersion = 8 // the Story was amended
	after := h.boundary.Execute(inv, contract.ActionRequest{
		Correlation: "c2", Action: capRead, Arguments: json.RawMessage(`{}`)})
	if after.Outcome != contract.OutcomeDenied {
		return Falsified, "an action bound to a superseded version was admitted"
	}
	return Proven, "control passed, then the superseded-version call was rejected"
}

func claimAtMostOnce(string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-once", defaultCaps(), false)

	req := contract.ActionRequest{Correlation: "same", Action: capPublish, Arguments: json.RawMessage(`{"n":1}`)}
	first := h.boundary.Execute(inv, req)
	second := h.boundary.Execute(inv, req)

	if first.Outcome != contract.OutcomeSucceeded {
		return Errored, "control failed: " + first.Reason
	}
	if len(h.published) != 1 {
		return Falsified, fmt.Sprintf("the effect committed %d times", len(h.published))
	}
	if second.AttemptID != first.AttemptID {
		return Falsified, "a transport retry minted a second attempt identity"
	}
	if len(h.recorder.Attempts()) != 1 {
		return Falsified, fmt.Sprintf("%d attempts recorded for one logical action",
			len(h.recorder.Attempts()))
	}
	return Proven, "one attempt, one effect, replayed result on the retry"
}

// claimRestartNoReissue is the honest form of re-attach on a stdio transport.
//
// A broken stdio transport IS a dead process, so there is no reconnection to a
// live runtime to test. The case that actually matters in Phase 3 is a
// RESTARTED runtime rejoining an existing execution, which is what this runs:
// one process settles an action, then a second process for the same invocation
// re-announces the correlation and must not reissue it.
func claimRestartNoReissue(binary string) (Outcome, string) {
	h := newHarness(sampleDiff)
	inv := invocation("inv-restart", defaultCaps(), false)

	// Run 1: an ordinary execution that settles step 1 (and step 3).
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

	// Run 2: a restart into the same execution, against the SAME recorder.
	_ = os.Setenv("SPIKE_MODE", "restart_reattach")
	second := rt.Run(ctx, inv)
	after := len(h.recorder.Attempts())

	if second.Result.Status != contract.StatusCompleted {
		return Falsified, "the restarted runtime did not complete: " + string(second.Result.Status)
	}
	if after != before {
		return Falsified, fmt.Sprintf("the restart minted %d new attempt(s)", after-before)
	}
	if len(h.published) != 1 {
		return Falsified, fmt.Sprintf("the effect committed %d times across the restart", len(h.published))
	}
	return Proven, fmt.Sprintf("re-attached to %d settled attempt(s); nothing reissued, one effect", before)
}
