package main

import (
	"strings"
	"sync/atomic"
	"time"

	"maestro-spike/phase3/executioncontract/contract"
	"maestro-spike/phase3/executioncontract/host"
)

// The wire scenarios. Every one of these spawns a real external process.

//nolint:funlen // A flat scenario table is more legible than a factory.
func scenarios() []scenario {
	return []scenario{
		{
			name:  "handshake/version-agreed",
			about: "§11 negotiation",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.DispatchErr != nil {
					return Falsified, "dispatch failed: " + out.DispatchErr.Error()
				}
				if out.Handshake.Selected != contract.Version {
					return Falsified, "selected " + out.Handshake.Selected
				}
				return Proven, "adapter selected " + out.Handshake.Selected
			},
		},
		{
			name:  "handshake/version-rejected-at-dispatch",
			about: "§5 a refused invocation is not a sixth status; §11 fails before resources",
			mode:  "bad_version",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if out.DispatchErr == nil {
					return Falsified, "an unsupported version was accepted"
				}
				if out.Result.Status != "" {
					return Falsified, "a terminal result was recorded for an execution that never started: " +
						string(out.Result.Status)
				}
				if len(h.recorder.Attempts()) != 0 {
					return Falsified, "actions were admitted before the handshake completed"
				}
				return Proven, "dispatch refused, no execution, no terminal result"
			},
		},
		{
			name:  "result/completed-changed",
			about: "§5 axes 1+2, §8 mediated actions",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if out.Result.Disposition == nil || *out.Result.Disposition != contract.DispositionChanged {
					return Falsified, "disposition not changed"
				}
				if h.publishedCount() != 1 {
					return Falsified, "published " + itoa(h.publishedCount())
				}
				for _, a := range h.recorder.Attempts() {
					if a.State != host.StateSettled {
						return Falsified, "attempt " + a.ID + " left in " + string(a.State)
					}
				}
				return Proven, "completed/changed, " + itoa(len(h.recorder.Attempts())) + " attempts all settled"
			},
		},
		{
			name:      "result/completed-already-satisfied",
			about:     "§5 axis 2 — issue #280",
			emptyDiff: true,
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if out.Result.Disposition == nil || *out.Result.Disposition != contract.DispositionAlreadySatisfied {
					return Falsified, "disposition is not already_satisfied"
				}
				return Proven, "an empty candidate is a completed execution with a different disposition"
			},
		},
		{
			name:  "result/no-provenance-event-without-a-model-call",
			about: "§9 — provenance is PER MODEL CALL; a stub that makes none emits none",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if len(out.Provenance) != 0 {
					return Falsified, "a provenance record was emitted for an execution with no model call"
				}
				if len(out.Usage) != 0 {
					return Falsified, "usage was reported for an execution with no model call"
				}
				return Proven, "no model call, no provenance and no usage — the gap is left declared, not filled"
			},
		},
		{
			name:  "capability/denial-is-data",
			about: "§8 denial vs protocol violation",
			mode:  "deny_probe",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				// The MECHANISM is asserted first: a neighbouring guard -- the
				// agent's own consistency check -- would otherwise fire before
				// this one and report a different defect.
				var denied *host.Attempt
				for _, a := range h.recorder.Attempts() {
					if a.Outcome == contract.OutcomeDenied {
						denied = a
					}
				}
				if denied == nil {
					return Falsified, "no denial was recorded"
				}
				if !strings.Contains(denied.Reason, "capability set") {
					return Falsified, "denied for the wrong reason: " + denied.Reason
				}
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "an ordinary denial ended the execution: " +
						string(out.Result.Status) + " " + errText(out)
				}
				return Proven, "ungranted action denied and recorded; the execution continued to completion"
			},
		},
		{
			name:  "capability/protocol-violation-is-fatal",
			about: "§8 the other side of the line",
			mode:  "bad_protocol",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "status " + string(out.Result.Status)
				}
				if out.Result.FailureClass == nil || *out.Result.FailureClass != contract.ClassNonRetryableAgent {
					return Falsified, "failure class is not non_retryable_agent"
				}
				return Proven, "a malformed message failed the execution as non_retryable_agent"
			},
		},
		{
			name:  "result/invalid-axes-rejected",
			about: "§5 applicability enforced on the wire",
			mode:  "bad_axes",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "an inapplicable axis was accepted: " + string(out.Result.Status)
				}
				if !strings.Contains(out.Result.Summary, "must not carry a failure class") {
					return Falsified, "rejected for the wrong reason: " + out.Result.Summary
				}
				return Proven, "completed+failure_class refused"
			},
		},
		{
			name:  "action/ask-is-mediated",
			about: "§4, §8 — routing a question is an ACTION, not a raw event",
			mode:  "ask",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				att := attemptFor(h, contract.ActionAsk)
				if att == nil {
					return Falsified, "the question never produced an action record"
				}
				if att.Outcome != contract.OutcomeSucceeded {
					return Falsified, "ask settled " + string(att.Outcome) + ": " + att.Reason
				}
				if h.routedCount() != 1 {
					return Falsified, "routed " + itoa(h.routedCount()) + " questions"
				}
				if out.AwaitingResponse == "" {
					return Falsified, "routing left the execution with no response wait"
				}
				if out.Result.Status != "" {
					return Falsified, "a terminal result was recorded while awaiting an answer: " +
						string(out.Result.Status)
				}
				return Proven, "routed as an action, and the execution waits on " + out.AwaitingResponse
			},
		},
		{
			name:  "cancel/cooperative",
			about: "§6 steps 1-2, and step 4's fence precondition",
			setup: func(h *harness, rt *host.Runtime, _ *contract.Invocation) {
				// The agent must be genuinely mid-action when cancellation
				// arrives, or this proves only that a finished agent ignores it.
				h.boundary.ResourceDelay[capRead.String()] = 700 * time.Millisecond
				rt.CancelAfter = 150 * time.Millisecond
				rt.CancelGrace = 5 * time.Second
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCancelled {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if out.Result.CancelReason == nil || *out.Result.CancelReason != contract.ReasonSuperseded {
					return Falsified, "reason is not superseded"
				}
				if !out.FenceReceipt.Positive() {
					return Falsified, "terminal recorded without a positive receipt: " + string(out.FenceReceipt)
				}
				return Proven, "cancelled/superseded after a terminated receipt"
			},
		},
		{
			name:  "cancel/admission-closes-before-the-drain",
			about: "§6 — ADR 0029 §7 step 2's ordering applied to attempts",
			mode:  "act_after_cancel",
			setup: func(h *harness, rt *host.Runtime, _ *contract.Invocation) {
				h.boundary.ResourceDelay[capRead.String()] = 500 * time.Millisecond
				rt.CancelAfter = 150 * time.Millisecond
				rt.CancelGrace = 6 * time.Second
			},
			check: func(h *harness, _ host.Outcome) (Outcome, string) {
				att := attemptFor(h, capPublish)
				if att == nil {
					return Errored, "the post-cancellation action was never attempted"
				}
				if att.Outcome != contract.OutcomeDenied {
					return Falsified, "an action admitted during the grace period: " + string(att.Outcome)
				}
				if !strings.Contains(att.Reason, "admission closed") {
					return Falsified, "denied for the wrong reason: " + att.Reason
				}
				if h.publishedCount() != 0 {
					return Falsified, "the effect committed during the grace period"
				}
				return Proven, "admission closed on request; the post-cancellation action was refused"
			},
		},
		{
			name:  "cancel/uncooperative-is-fenced",
			about: "§6 step 3 — revocation does not stop a process",
			mode:  "ignore_cancel",
			setup: func(_ *harness, rt *host.Runtime, _ *contract.Invocation) {
				rt.CancelAfter = 200 * time.Millisecond
				rt.CancelGrace = 400 * time.Millisecond
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCancelled {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if !out.FenceReceipt.Positive() {
					return Falsified, "receipt " + string(out.FenceReceipt)
				}
				return Proven, "grace expired, domain fenced, then cancelled/superseded recorded"
			},
		},
		{
			name:  "cancel/terminal-withheld-on-unconfirmed-fence",
			about: "§6 step 4 — a result over an unfenced process is a false record",
			mode:  "ignore_cancel",
			setup: func(_ *harness, rt *host.Runtime, _ *contract.Invocation) {
				rt.CancelAfter = 200 * time.Millisecond
				rt.CancelGrace = 400 * time.Millisecond
				rt.Fencer = func(contract.ResourceRef) host.FenceReceipt { return host.FenceUnconfirmed }
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != "" {
					return Falsified, "a terminal result was recorded on an unconfirmed fence: " +
						string(out.Result.Status)
				}
				if out.DispatchErr == nil || !strings.Contains(out.DispatchErr.Error(), "quarantine") {
					return Falsified, "no quarantine was declared"
				}
				return Proven, "unconfirmed receipt left the execution non-terminal and quarantined the resource"
			},
		},
		{
			name:  "timeout/terminal-withheld-on-unconfirmed-fence",
			about: "§6 — the receipt rule belongs to the CATEGORY, not to `cancelled`",
			mode:  "hang",
			setup: func(_ *harness, rt *host.Runtime, inv *contract.Invocation) {
				inv.Config.Budgets.MaxWallClockMS = 400
				rt.Fencer = func(contract.ResourceRef) host.FenceReceipt { return host.FenceUnconfirmed }
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != "" {
					return Falsified, "a forced timeout recorded " + string(out.Result.Status) +
						" without a positive receipt"
				}
				if out.DispatchErr == nil || !strings.Contains(out.DispatchErr.Error(), "quarantine") {
					return Falsified, "no quarantine was declared"
				}
				return Proven, "timeout withheld its terminal result exactly as cancellation does"
			},
		},
		{
			name:  "events/claim-overridden-after-cancel",
			about: "§4 the terminal event is a claim; observations win and the claim is retained",
			mode:  "claim_completed_after_cancel",
			setup: func(h *harness, rt *host.Runtime, _ *contract.Invocation) {
				h.boundary.ResourceDelay[capRead.String()] = 700 * time.Millisecond
				rt.CancelAfter = 150 * time.Millisecond
				rt.CancelGrace = 5 * time.Second
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCancelled {
					return Falsified, "the runtime's claim stood: " + string(out.Result.Status)
				}
				if !out.Overridden {
					return Falsified, "override not recorded"
				}
				if out.Claimed == nil || out.Claimed.Status != contract.StatusCompleted {
					return Falsified, "the claim was discarded rather than retained"
				}
				return Proven, "claim=completed retained, recorded=cancelled/superseded"
			},
		},
		{
			name:  "events/duplicate-rejected-by-identity",
			about: "§4 — at-least-once is only idempotent with a checked identity",
			mode:  "replay_event",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.DuplicateEvents == 0 {
					return Falsified, "a replayed envelope was accepted as a new event"
				}
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				return Proven, itoa(out.DuplicateEvents) + " replayed envelope(s) dropped on (inv, epoch, stream, seq)"
			},
		},
		{
			name:  "gate/headless-blocks-with-one-durable-outcome",
			about: "ADR 0030 §4 + §5's blocked status and its Orchestrator-composed exception",
			mode:  "escalate",
			setup: func(h *harness, _ *host.Runtime, _ *contract.Invocation) {
				h.boundary.Policy = requiresOperatorFor(capForge)
			},
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				att := attemptFor(h, capForge)
				if att == nil {
					return Errored, "the gated action was never attempted"
				}
				// One durable outcome: terminal, blocked, requirement preserved.
				// A headless wait is not a healthy wait.
				if att.State != host.StateSettled {
					return Falsified, "the action was left in " + string(att.State) +
						" although nothing can ever answer it"
				}
				if att.Outcome != contract.OutcomeBlocked {
					return Falsified, "settled as " + string(att.Outcome) + " rather than blocked"
				}
				if att.Requirement == nil {
					return Falsified, "the requirement was not preserved"
				}
				if out.Result.Status != contract.StatusBlocked {
					return Falsified, "execution status " + string(out.Result.Status) + " " + errText(out)
				}
				if len(out.Result.BlockedOn) == 0 || out.Result.BlockedOn[0].AttemptID == "" {
					return Falsified, "the terminal result references no requirement"
				}
				if out.Claimed != nil {
					return Falsified, "the agent claimed a terminal result for a gate it cannot see"
				}
				return Proven, "action settled blocked, requirement preserved, execution blocked by the Orchestrator"
			},
		},
		{
			name:  "gate/interactive-approval-proceeds",
			about: "ADR 0030 §4 gate 2, §6's durable operator_waiting transition",
			mode:  "escalate",
			setup: func(h *harness, _ *host.Runtime, inv *contract.Invocation) {
				h.boundary.Policy = requiresOperatorFor(capForge)
				h.boundary.Operator = func([]contract.RequirementRef, []string) bool { return true }
				inv.Config.OperatorResponder = true
			},
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				att := attemptFor(h, capForge)
				if att == nil {
					return Errored, "the gated action was never attempted"
				}
				if !hasState(att, host.StateOperatorWaiting) {
					return Falsified, "the approved attempt never recorded operator_waiting"
				}
				if att.Outcome != contract.OutcomeSucceeded {
					return Falsified, "approved attempt outcome " + string(att.Outcome)
				}
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				return Proven, "operator_waiting recorded as a durable transition, then executed"
			},
		},
		{
			name:  "wait/transport-stays-live-during-an-operator-wait",
			about: "ADR 0030 §4 — the wait is LOGICAL and must not hold the transport",
			mode:  "escalate",
			setup: func(h *harness, rt *host.Runtime, inv *contract.Invocation) {
				inv.Config.OperatorResponder = true
				h.boundary.Policy = requiresOperatorFor(capForge)
				// SIGNAL-driven, not timer-driven. The gate itself asks for the
				// cancellation once it is holding, and releases once the loop
				// has sent it -- so the ordering this claim asserts holds by
				// construction rather than by margin. A timer here was the one
				// plausible source of an intermittent failure I saw once and
				// could not reproduce.
				cancelNow := make(chan struct{})
				cancelSent := make(chan struct{})
				rt.CancelWhen = cancelNow
				rt.CancelGrace = 10 * time.Second
				rt.OnCancelRequested = func() {
					atomic.StoreInt64(&cancelSentAt, time.Now().UnixMicro())
					close(cancelSent)
				}
				h.boundary.Operator = func([]contract.RequirementRef, []string) bool {
					atomic.StoreInt64(&operatorEnteredAt, time.Now().UnixMicro())
					close(cancelNow) // ask for it only once we are demonstrably holding
					select {
					case <-cancelSent:
					case <-time.After(5 * time.Second):
					}
					atomic.StoreInt64(&operatorLeftAt, time.Now().UnixMicro())
					return true
				}
			},
			check: func(_ *harness, _ host.Outcome) (Outcome, string) {
				entered := atomic.LoadInt64(&operatorEnteredAt)
				left := atomic.LoadInt64(&operatorLeftAt)
				sent := atomic.LoadInt64(&cancelSentAt)
				if entered == 0 || left == 0 {
					return Errored, "the operator gate never ran"
				}
				if sent == 0 {
					return Falsified, "the event loop never sent cancellation — it was blocked by the wait"
				}
				if sent < entered || sent > left {
					return Falsified, "cancellation was not sent DURING the wait"
				}
				return Proven, "the loop sent cancellation while the operator gate was still deciding"
			},
		},
		{
			name:  "gate/resource-wait-is-a-distinct-state",
			about: "§6 — two waits, different responders",
			setup: func(h *harness, _ *host.Runtime, _ *contract.Invocation) {
				h.boundary.ResourceDelay[capPublish.String()] = 120 * time.Millisecond
			},
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				att := attemptFor(h, capPublish)
				if att == nil {
					return Errored, "the delayed action was never attempted"
				}
				if !hasState(att, host.StateResourceWaiting) {
					return Falsified, "no resource_waiting transition"
				}
				if hasState(att, host.StateOperatorWaiting) {
					return Falsified, "a resource wait was recorded as an operator wait"
				}
				if att.ResourceOp == "" {
					return Falsified, "the resource wait names no operation, so it could not be restored"
				}
				return Proven, "resource_waiting recorded, named, and distinct from operator_waiting"
			},
		},
		{
			name:  "result/timed-out-carries-no-failure-class",
			about: "§5 — a deadline is neither failure class",
			mode:  "hang",
			setup: func(_ *harness, _ *host.Runtime, inv *contract.Invocation) {
				inv.Config.Budgets.MaxWallClockMS = 400
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusTimedOut {
					return Falsified, "status " + string(out.Result.Status)
				}
				if out.Result.FailureClass != nil {
					return Falsified, "timed_out was forced into class " + string(*out.Result.FailureClass)
				}
				return Proven, "timed_out recorded with no manufactured classification"
			},
		},
		{
			name:  "record/interrupted-attempt-reconciles-unknown",
			about: "ADR 0030 §8's Interrupted row; the value tool_calls cannot express",
			setup: func(h *harness, _ *host.Runtime, _ *contract.Invocation) {
				h.boundary.CrashAfterOpen = map[string]bool{capRead.String(): true}
			},
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				att := attemptFor(h, capRead)
				if att == nil {
					return Errored, "the interrupted action was never attempted"
				}
				if att.State != host.StateSettled {
					return Falsified, "left in " + string(att.State) + " rather than reconciled"
				}
				if att.Outcome != contract.OutcomeUnknown {
					return Falsified, "settled as " + string(att.Outcome) + " rather than unknown"
				}
				if len(out.Reconciled.SettledUnknown) != 1 {
					return Falsified, "reconciliation reported " +
						itoa(len(out.Reconciled.SettledUnknown)) + " unknown"
				}
				return Proven, "intent recorded, no outcome, reconciled as unknown"
			},
		},
		{
			name:  "record/duplicate-request-commits-once",
			about: "ADR 0030 §3 — an outstanding duplicate re-enters no gate",
			mode:  "duplicate_request",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if n := len(h.recorder.Attempts()); n != 1 {
					return Falsified, itoa(n) + " attempts recorded for one logical action"
				}
				if h.publishedCount() != 1 {
					return Falsified, "the effect committed " + itoa(h.publishedCount()) + " times"
				}
				return Proven, "two requests, one attempt, one effect"
			},
		},
		{
			name:  "events/acknowledgement-never-claims-a-gap",
			about: "§4 — an ack reports the watermark, not the sequence just received",
			mode:  "retain_across_gap",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if !strings.Contains(out.Result.Summary, "retained 1 ") {
					return Falsified, "the acknowledgement released a report across a gap: " +
						out.Result.Summary
				}
				return Proven, "committed past a gap, acknowledged honestly, and still retained"
			},
		},
		{
			name:  "events/replay-beyond-a-gap-is-deduplicated",
			about: "§4 — the watermark alone cannot cover what was committed past a gap",
			mode:  "replay_beyond_gap",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if len(out.Usage) != 1 {
					return Falsified, itoa(len(out.Usage)) +
						" usage reports recorded from one call committed beyond a gap"
				}
				if out.DuplicateEvents == 0 {
					return Falsified, "the replay past a gap was accepted as a new event"
				}
				return Proven, "a report committed above the watermark was still recognised on replay"
			},
		},
		{
			name:  "events/lost-acknowledgement-replays-once",
			about: "§4 — the retention obligation exercised, not merely declared",
			mode:  "replay_before_ack",
			setup: func(_ *harness, rt *host.Runtime, _ *contract.Invocation) {
				rt.DropAcks = true // the lost acknowledgement, by construction
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if out.DuplicateEvents == 0 {
					return Falsified, "the retained replay was accepted as a new event"
				}
				if len(out.Usage) != 1 {
					return Falsified, itoa(len(out.Usage)) +
						" usage reports recorded from one call, so the replay was counted"
				}
				return Proven, "a retained report replayed before its ack was deduplicated; one report recorded"
			},
		},
		{
			name:  "events/acknowledgement-releases-the-outbox",
			about: "§4 — the retention obligation, and what discharges it",
			mode:  "replay_outbox",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if len(out.Usage) != 1 {
					return Falsified, itoa(len(out.Usage)) + " usage reports recorded, want 1"
				}
				// The replay carried the original identity, so it deduplicated;
				// and the acknowledgement had already emptied the outbox.
				if out.DuplicateEvents != 0 {
					return Falsified, itoa(out.DuplicateEvents) +
						" replay(s) arrived, so the acknowledgement did not release the outbox"
				}
				return Proven, "one report committed; the acknowledgement released it and nothing replayed"
			},
		},
		{
			name:  "protocol/unknown-message-type-is-fatal",
			about: "§8 — silently ignoring one lets a runtime look healthy while its work is dropped",
			mode:  "unknown_type",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				// The mechanism first: the violation must be NAMED. Checking the
				// status alone would pass on a run that failed for an unrelated
				// reason, which is exactly what happens when validation is off --
				// the message is ignored, the adapter exits, and the execution
				// fails because the transport closed.
				if !strings.Contains(out.Result.Summary, "unknown message type") {
					return Falsified, "the violation was not detected; failed instead with: " +
						out.Result.Summary
				}
				if out.Result.FailureClass == nil || *out.Result.FailureClass != contract.ClassNonRetryableAgent {
					return Falsified, "a protocol violation was not classed non_retryable_agent"
				}
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "status " + string(out.Result.Status)
				}
				return Proven, "unknown type refused as a protocol violation"
			},
		},
		{
			name:  "protocol/malformed-known-body-is-fatal",
			about: "§8 — decoding and shrugging turns a corrupt report into a dropped one",
			mode:  "malformed_usage",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if !strings.Contains(out.Result.Summary, "call reference") {
					return Falsified, "the malformed body was not detected; failed instead with: " +
						out.Result.Summary
				}
				if out.Result.FailureClass == nil || *out.Result.FailureClass != contract.ClassNonRetryableAgent {
					return Falsified, "a malformed body was not classed non_retryable_agent"
				}
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "status " + string(out.Result.Status)
				}
				return Proven, "usage without a call reference refused"
			},
		},
		{
			name:  "protocol/started-identity-must-agree-with-the-handshake",
			about: "§9 — identity that is never compared is a self-report recorded as a fact",
			mode:  "lying_started",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.IdentityError == "" {
					return Falsified, "a `started` disagreeing with the handshake was accepted"
				}
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if !strings.Contains(out.Result.Summary, "runtime identity") {
					return Falsified, "failed for the wrong reason: " + out.Result.Summary
				}
				return Proven, "identity mismatch detected: " + out.IdentityError
			},
		},
		{
			name:  "protocol/stream-must-match-the-message-type",
			about: "§4 — the stream is caller-supplied and must not be trusted",
			mode:  "lying_stream",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if !strings.Contains(out.Result.Summary, "claims stream") {
					return Falsified, "a usage report opted itself out of the reliable stream: " +
						out.Result.Summary
				}
				if len(out.Usage) != 0 {
					return Falsified, "the mislabelled report was recorded anyway"
				}
				return Proven, "a reliable report claiming best_effort was refused"
			},
		},
		{
			name:  "protocol/prior-epoch-act-is-fatal",
			about: "§4 — only replayable REPORTS may arrive from a superseded incarnation",
			mode:  "prior_epoch_act",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if len(h.recorder.Attempts()) != 0 {
					return Falsified, "an act from a prior epoch was accepted and admitted"
				}
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "an act from a prior epoch was accepted: " +
						string(out.Result.Status)
				}
				if !strings.Contains(out.Result.Summary, "prior epoch") {
					return Falsified, "failed for the wrong reason: " + out.Result.Summary
				}
				return Proven, "an action_request from a superseded incarnation was refused"
			},
		},
		{
			name:  "action/inline-question-refused",
			about: "ADR 0021 — artifacts are the sole agent handoff",
			mode:  "ask_inline",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if h.routedCount() != 0 {
					return Falsified, "inline question text was accepted and routed"
				}
				att := attemptFor(h, contract.ActionAsk)
				if att == nil {
					return Errored, "the ask was never attempted"
				}
				if att.Outcome != contract.OutcomeFailed {
					return Falsified, "inline question text was accepted: " + string(att.Outcome)
				}
				_ = out
				return Proven, "a question carried inline was refused: " + att.Reason
			},
		},
		{
			name:  "transport/eof-without-terminal-is-forced",
			about: "§6 — the Orchestrator ending an execution always drains and fences",
			mode:  "silent_exit",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if !out.Forced {
					return Falsified, "a transport closing with no terminal result was not treated as a forced stop"
				}
				if !out.FenceReceipt.Positive() {
					return Falsified, "recorded without a positive receipt: " + string(out.FenceReceipt)
				}
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "status " + string(out.Result.Status)
				}
				return Proven, "drained and fenced before recording the failure"
			},
		},
		{
			name:  "protocol/epoch-ahead-of-binding-is-fatal",
			about: "§4 — an incarnation the Orchestrator never issued",
			mode:  "bad_epoch",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "a future epoch was accepted: " + string(out.Result.Status)
				}
				if !strings.Contains(out.Result.Summary, "ahead of the active") {
					return Falsified, "failed for the wrong reason: " + out.Result.Summary
				}
				return Proven, "an epoch ahead of the binding refused"
			},
		},
		{
			name:  "cancel/undrained-action-yields-no-positive-receipt",
			about: "ADR 0030 §5 — fencing a resource says nothing about an Orchestrator-side mutation",
			setup: func(h *harness, rt *host.Runtime, _ *contract.Invocation) {
				// The EFFECT is slow, so the attempt is `open` and mid-commit
				// when the grace expires. A declared wait would simply be
				// stopped; only an in-flight commit can defeat the drain.
				h.publishDelay = 5 * time.Second
				rt.CancelAfter = 400 * time.Millisecond
				rt.CancelGrace = 300 * time.Millisecond
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.ActionsDrained {
					return Errored, "the drain unexpectedly completed; the scenario proved nothing"
				}
				if out.OutstandingActions == 0 {
					return Errored, "no action was left outstanding"
				}
				if out.FenceReceipt.Positive() {
					return Falsified, "a positive receipt was issued with " +
						itoa(out.OutstandingActions) + " action(s) unsettled"
				}
				if out.Result.Status != "" {
					return Falsified, "a terminal result was recorded: " + string(out.Result.Status)
				}
				return Proven, itoa(out.OutstandingActions) +
					" unsettled action(s) forced an unconfirmed receipt and withheld the result"
			},
		},
		{
			name:  "gate/headless-block-is-orchestrator-driven",
			about: "§5 — the Orchestrator stops a blocked execution rather than awaiting a courtesy exit",
			mode:  "escalate_defiant",
			setup: func(h *harness, rt *host.Runtime, _ *contract.Invocation) {
				h.boundary.Policy = requiresOperatorFor(capForge)
				rt.CancelGrace = 600 * time.Millisecond
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusBlocked {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if !out.Forced {
					return Falsified, "the stop was not Orchestrator-driven"
				}
				if !out.FenceReceipt.Positive() {
					return Falsified, "recorded without a positive receipt: " + string(out.FenceReceipt)
				}
				if out.Claimed != nil {
					return Falsified, "the agent claimed a terminal result"
				}
				if len(out.Result.BlockedOn) == 0 {
					return Falsified, "no requirement referenced"
				}
				return Proven, "a runtime that refused to stop was cancelled, fenced, and recorded blocked"
			},
		},
		{
			name:  "restart/does-not-reissue-a-settled-action",
			about: "§6 re-attach across a restart; the Orchestrator enumerates",
			// Driven by the boundary claim of the same subject, which needs two
			// processes; see claimRestartNoReissue.
			mode: "normal",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Errored, "the control run did not complete"
				}
				return Proven, "control for restart/does-not-reissue (asserted in the boundary claim)"
			},
		},
	}
}

// Timestamps for the transport-liveness scenario. Package-level because the
// scenario's setup and check are separate closures.
//
//nolint:gochecknoglobals // scenario-local state for one timing assertion
var (
	operatorEnteredAt int64
	operatorLeftAt    int64
	cancelSentAt      int64
)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
