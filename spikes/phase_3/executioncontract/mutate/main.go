// Command mutate is the defect-shaped verification for the conformance suite
// (process_build.md, binding since Phase 2 item 9).
//
// A claim that passes against the finished code is not thereby proven. Each
// mutation below restores the exact defect its claim protects against, and the
// run counts as evidence only when the claim FALSIFIES for the NAMED reason --
// not when it errors, not when the build breaks, and not when some neighbouring
// guard fires first.
//
//	go run ./mutate
//
// Residue discipline: a killed harness does not run its restore, and the next
// run would then layer a second mutation on a tree that no longer describes the
// defect. So this writes a marker before touching anything and refuses to start
// while one exists.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const markerPath = ".mutation-in-progress"

type mutation struct {
	name string
	file string
	old  string
	new  string
	// claim is the -run selector for the claim this defect must break.
	claim string
	// want is the substring the FALSIFIED detail must contain. Without it a
	// mutation that kills the claim for an unrelated reason would count.
	want string
	// why records the defect being restored, for the report.
	why string
}

func mutations() []mutation {
	return []mutation{
		{
			name:  "applicability-rule-one-direction-only",
			file:  "contract/terminal.go",
			old:   "\tcase t.Status != StatusFailed && t.FailureClass != nil:\n\t\treturn fmt.Errorf(\"status %q must not carry a failure class\", t.Status)",
			new:   "\tcase false:\n\t\treturn fmt.Errorf(\"unreachable\")",
			claim: "schema/applicability",
			want:  "accepted: completed WITH failure class",
			why:   "a validator that checks only 'required axis present' accepts the axis collision the four-axis schema exists to rule out",
		},
		{
			name:  "timed-out-forced-into-a-failure-class",
			file:  "contract/terminal.go",
			old:   "\tcase t.Status == StatusFailed && t.FailureClass == nil:",
			new:   "\tcase (t.Status == StatusFailed || t.Status == StatusTimedOut) && t.FailureClass == nil:",
			claim: "schema/applicability",
			want:  "rejected valid: timed_out with NO class",
			why:   "ordinary wall-clock exhaustion is neither retryable infrastructure nor a non-retryable agent defect; requiring one records a guess as a fact",
		},
		{
			name:  "reconciler-settles-a-wait-as-unknown",
			file:  "host/boundary.go",
			old:   "\t\t\tatt.Outcome = contract.OutcomeStale\n\t\t\tatt.Reason = \"orchestrator restarted while awaiting an operator\"",
			new:   "\t\t\tatt.Outcome = contract.OutcomeUnknown\n\t\t\tatt.Reason = \"orchestrator restarted while awaiting an operator\"",
			claim: "reconcile/declared-wait-goes-stale",
			want:  "settled as unknown rather than stale",
			why:   "THE DEFECT ROUND ONE FOUND, in its final form: an attempt waiting on an operator is not an attempt whose outcome nobody knows, and conflating them destroys the requirement a blocked result references",
		},
		{
			name:  "stale-mark-does-not-stop-the-continuation",
			file:  "host/boundary.go",
			old:   "\tif b.Recorder.State(att) == StateSettled {\n\t\treturn contract.ActionResult{Correlation: req.Correlation, AttemptID: att.ID,\n\t\t\tOutcome: att.Outcome, Reason: att.Reason}\n\t}",
			new:   "\tif false {\n\t\treturn contract.ActionResult{}\n\t}",
			claim: "reconcile/declared-wait-goes-stale",
			want:  "settled as succeeded rather than stale",
			why:   "'invalidate the attempt' is not a mechanism: a mark nothing checks does not stop the goroutine, which commits anyway -- ADR 0030 §5's own rejected option, rediscovered",
		},
		{
			name:  "operator-decision-not-persisted",
			file:  "host/boundary.go",
			old:   "\t\tb.recordGrant(att, true)",
			new:   "\t\t_ = binding",
			claim: "boundary/operator-decision-is-consumed-once",
			want:  "the recovery re-asked the operator",
			why:   "without a persisted decision, going stale on restart means asking a human the same question a second time -- which is what makes stale unacceptable rather than merely inconvenient",
		},
		{
			name:  "started-identity-not-compared",
			file:  "host/runtime.go",
			old:   "\t\t} else if st.Adapter != out.Handshake.Adapter ||",
			new:   "\t\t} else if false || st.Adapter != out.Handshake.Adapter \u0026\u0026 false ||",
			claim: "protocol/started-identity",
			want:  "disagreeing with the handshake was accepted",
			why:   "identity carried and never compared is a self-report recorded as though it were established (§9)",
		},
		{
			name:  "reconciler-settles-nothing",
			file:  "host/boundary.go",
			old:   "\t\tcase StateOpen:",
			new:   "\t\tcase StateOpen + \"-never\":",
			claim: "record/interrupted-attempt",
			want:  "left in open rather than reconciled",
			why:   "an interrupted attempt left open forever is v1's shape: the record cannot say 'attempted, outcome unknown'",
		},
		{
			name:  "settled-retry-mints-a-second-attempt",
			file:  "host/boundary.go",
			old:   "\tif !ok {\n\t\treturn nil, nil\n\t}",
			new:   "\tif !ok || true {\n\t\treturn nil, nil\n\t}",
			claim: "boundary/settled-retry",
			want:  "the effect committed 2 times",
			why:   "a retry treated as a new action is how an adapted runtime duplicates a forge push",
		},
		{
			name:  "outstanding-duplicate-re-enters-the-gates",
			file:  "host/boundary.go",
			old:   "\t\tif st := b.Recorder.State(att); st != StateSettled {",
			new:   "\t\tif st := b.Recorder.State(att); st == \"\\x00never\" {",
			claim: "boundary/outstanding-retry",
			want:  "rather than outstanding",
			why:   "replaying only SETTLED duplicates lets an in-flight one fall through into policy, operator handling and resource acquisition a second time",
		},
		{
			name:  "headless-leaves-a-phantom-operator-wait",
			file:  "host/boundary.go",
			old:   "\t\t\tb.Recorder.Settle(att, contract.OutcomeBlocked, requirement.Statement, DispositionBeforeCommit)",
			new:   "\t\t\tb.Recorder.Transition(att, StateOperatorWaiting)",
			claim: "gate/headless-blocks-with-one-durable-outcome",
			want:  "settled as stale rather than blocked",
			why:   "a headless wait has no responder, so recording it as a wait describes an event that will never happen; the drain then sweeps the phantom to `stale` and the blocked result references nothing",
		},
		{
			name:  "terminal-recorded-on-unconfirmed-fence",
			file:  "host/runtime.go",
			old:   "\tif out.Forced && !out.FenceReceipt.Positive() {",
			new:   "\tif false {",
			claim: "cancel/terminal-withheld",
			want:  "a terminal result was recorded on an unconfirmed fence",
			why:   "a terminal result written while an unfenced process may still be writing is a false record (ADR 0029 §7, ADR 0032 §6 step 4)",
		},
		{
			name:  "timeout-is-not-treated-as-a-forced-stop",
			file:  "host/runtime.go",
			old:   "\t\t\treturn forcedTerminal(contract.TimedOut(\"wall-clock budget exhausted\"))",
			new:   "\t\t\tout.Result = contract.TimedOut(\"wall-clock budget exhausted\")\n\t\t\treturn r.finish(out)",
			claim: "timeout/terminal-withheld",
			want:  "without a positive receipt",
			why:   "the receipt discipline belongs to the CATEGORY -- the Orchestrator forced the stop -- not to the single status `cancelled`",
		},
		{
			name:  "admission-not-closed-on-cancellation",
			file:  "host/runtime.go",
			old:   "\t\t\tr.Boundary.CloseAdmission(id)\n\t\t\tgrace := r.CancelGrace",
			new:   "\t\t\tgrace := r.CancelGrace",
			claim: "cancel/admission-closes",
			want:  "an action admitted during the grace period",
			why:   "ADR 0029 §7 step 2's ordering applied to attempts: without revoke-before-drain the holder keeps creating work the drain must then chase",
		},
		{
			name:  "event-identity-not-checked",
			file:  "host/runtime.go",
			old:   "\t\t\tif r.Boundary.Recorder.Committed(id, it.env.Epoch, it.env.Seq, it.env.Stream) {",
			new:   "\t\t\tif r.Boundary.Recorder.Committed(id, it.env.Epoch, it.env.Seq, it.env.Stream) && false {",
			claim: "events/duplicate-rejected-by-identity",
			want:  "a replayed envelope was accepted",
			why:   "at-least-once delivery cannot be idempotent without a checked identity, and a sequence number alone restarts at 1 with every incarnation",
		},
		{
			name:  "stale-generation-not-revalidated",
			file:  "host/boundary.go",
			old:   "\t\tif known && current != r.InstanceGeneration {",
			new:   "\t\tif false && known && current != r.InstanceGeneration {",
			claim: "boundary/stale-generation",
			want:  "a late call from a stale generation was admitted",
			why:   "ADR 0029 §7 requirement 5: a call issued by a fenced holder must be rejected at the boundary even when it arrives late",
		},
		{
			name:  "old-epoch-accepted-for-every-type",
			file:  "host/runtime.go",
			old:   "\tif env.Epoch < inv.Bindings.Epoch && !replayableFromPriorEpoch(env.Type) {",
			new:   "\tif false && env.Epoch < inv.Bindings.Epoch && !replayableFromPriorEpoch(env.Type) {",
			claim: "protocol/prior-epoch-act",
			want:  "an act from a prior epoch was accepted",
			why:   "admitting an ACT from a superseded incarnation is a stale generation reaching through the boundary, which ADR 0029 §7 requirement 5 exists to prevent",
		},
		{
			name:  "decision-survives-its-use",
			file:  "host/boundary.go",
			old:   "\t\tif approved, found := b.Recorder.ConsumeDecision(inv.ID(), binding, att.CanonicalSet); found {",
			new:   "\t\tif approved, found := b.Recorder.PeekDecision(inv.ID(), binding, att.CanonicalSet); found {",
			claim: "boundary/operator-decision-is-consumed-once",
			want:  "a second repetition reused the decision",
			why:   "`approve_once` that survives its use is permanent: one approved push approves every later push of the same ref, and an intentional repetition is indistinguishable from a recovery",
		},
		{
			name:  "requirement-set-not-compared-at-gate-3",
			file:  "host/boundary.go",
			old:   "\t\tif CanonicalRequirements(nowReqs) != att.CanonicalSet {",
			new:   "\t\tif false && CanonicalRequirements(nowReqs) != att.CanonicalSet {",
			claim: "boundary/changed-requirement-set-is-stale",
			want:  "executed under an approval given for a different requirement set",
			why:   "ADR 0030 §5: if the question changed, the answer is void rather than supplemented -- otherwise a newly-appeared gate is satisfied by an approval nobody gave it",
		},
		{
			name:  "scopes-composed-by-union",
			file:  "host/boundary.go",
			old:   "\t\tkept := acc[:0]\n\t\tfor _, s := range acc {\n\t\t\tif next[s] {\n\t\t\t\tkept = append(kept, s)\n\t\t\t}\n\t\t}\n\t\tacc = kept",
			new:   "\t\tfor s := range next {\n\t\t\tif !slicesContains(acc, s) {\n\t\t\t\tacc = append(acc, s)\n\t\t\t}\n\t\t}",
			claim: "boundary/scopes-compose-by-intersection",
			want:  "broadened",
			why:   "ADR 0030 §3: a union lets a permissive gate install a grant the strict gate never authorized -- the UI becoming an authority, arriving through composition",
		},
		{
			name:  "eof-without-terminal-skips-the-fence",
			file:  "host/runtime.go",
			old:   "\tout.Forced = true\n\tr.Boundary.CloseAdmission(id)\n\twindow := r.CancelGrace",
			new:   "\twindow := r.CancelGrace",
			claim: "transport/eof-without-terminal-is-forced",
			want:  "was not treated as a forced stop",
			why:   "a transport that closed without a terminal result is the Orchestrator ending the execution, and it owes the same drain and fence as any other forced stop",
		},
		{
			name:  "routing-a-question-opens-no-wait",
			file:  "main.go",
			old:   "\t\trec.OpenResponseWait(inv.ID(), q.QuestionArtifact)",
			new:   "\t\t_ = q.QuestionArtifact",
			claim: "question/execution-waits",
			want:  "did not leave the execution awaiting an answer",
			why:   "the ADR asserts routing enters a durable execution wait; without one the execution runs on to completion having asked a question nobody will answer",
		},
		{
			name:  "question-may-cross-inline",
			file:  "main.go",
			old:   "\t\tif q.QuestionArtifact == \"\" {",
			new:   "\t\tif false && q.QuestionArtifact == \"\" {",
			claim: "action/inline-question-refused",
			want:  "inline question text was accepted",
			why:   "ADR 0021 makes artifacts the sole agent handoff; inline question text is the direct principal-to-principal payload it forbids, merely routed through a mediated action",
		},
		{
			name:  "settled-retry-loses-its-result",
			file:  "host/boundary.go",
			old:   "\t\t\tResult:       att.Result,",
			new:   "",
			claim: "question/execution-waits",
			want:  "did not complete",
			why:   "replaying the OUTCOME without the payload hands a caller a success with no data -- a different answer wearing the same label",
		},
		{
			name:  "decision-promoted-at-grant-time",
			file:  "host/boundary.go",
			old:   "\tatt.OperatorApproved = &approved\n}",
			new:   "\tatt.OperatorApproved = &approved\n\tr := b.Recorder\n\tif att.Binding != \"\" {\n\t\tr.decisions[decisionKey(att.Invocation, att.Binding, att.CanonicalSet)] = approved\n\t}\n}",
			claim: "boundary/decision-does-not-outlive",
			want:  "approve_once applied twice",
			why:   "a grant promoted when it is GIVEN is never consumed by the action it was given for, so the next identical action inherits it",
		},
		{
			name:  "operator-shown-one-requirement",
			file:  "host/boundary.go",
			old:   "\t\tif b.Operator == nil || !b.Operator(att.Requirements, att.EffectiveScopes) {",
			new:   "\t\tif b.Operator == nil || !b.Operator(att.Requirements[:1], att.EffectiveScopes) {",
			claim: "boundary/operator-sees-the-complete-set",
			want:  "of 2 requirements",
			why:   "ADR 0030 §3: the operator answers once having seen everything asked; showing one of several is deny-and-retry arriving by another route",
		},
		{
			name:  "interactive-wait-does-not-guard",
			file:  "host/boundary.go",
			old:   "\t\tb.setWaiting(id, true)\n\t\tres := b.completeAction(inv, att, req, true)",
			new:   "\t\tres := b.completeAction(inv, att, req, true)",
			claim: "boundary/waiting-guards-before-registration",
			want:  "did not guard the Story",
			why:   "ADR 0030 §4 blocks the caller so no LLM turn happens while a gate is open; guarding only the headless case leaves the case the rule was written for unguarded",
		},
		{
			name:  "guard-runs-after-registration",
			file:  "host/boundary.go",
			old:   "\tif res, refused := b.refuseWhileWaiting(inv, req); refused {\n\t\treturn res\n\t}\n\tatt, fresh, err := b.register(inv.ID(), req)",
			new:   "\tatt, fresh, err := b.register(inv.ID(), req)\n\tif res, refused := b.refuseWhileWaiting(inv, req); refused {\n\t\treturn res\n\t}",
			claim: "boundary/waiting-guards-before-registration",
			want:  "a new call was admitted while the Story awaited resolution",
			why:   "a rejected call that opened a record leaves an attempt the drain will then wait on -- a refusal that costs a fence",
		},
		{
			name:  "stream-not-validated",
			file:  "host/runtime.go",
			old:   "\tif want := contract.StreamFor(env.Type); env.Stream != want {",
			new:   "\tif want := contract.StreamFor(env.Type); false \u0026\u0026 env.Stream != want {",
			claim: "protocol/stream-must-match",
			want:  "opted itself out of the reliable stream",
			why:   "a caller-supplied stream opts a report out of the retention obligation and the deduplication its own type carries",
		},
		{
			name:  "dedup-checks-only-the-watermark",
			file:  "host/runtime.go",
			old:   "\t\t\tif r.Boundary.Recorder.Committed(id, it.env.Epoch, it.env.Seq, it.env.Stream) {",
			new:   "\t\t\tif it.env.Seq <= r.Boundary.Recorder.Watermark(id, it.env.Epoch, it.env.Stream) {",
			claim: "events/replay-beyond-a-gap",
			want:  "usage reports recorded from one call committed beyond a gap",
			why:   "an event committed BEYOND a gap sits above the watermark, so a watermark-only check accepts its replay and applies it twice",
		},
		{
			name:  "ack-reports-the-received-sequence",
			file:  "host/runtime.go",
			old:   "\tmark := r.Boundary.Recorder.Watermark(inv.ID(), env.Epoch, env.Stream)",
			new:   "\tmark := env.Seq",
			claim: "events/acknowledgement-never-claims-a-gap",
			want:  "claimed watermark 2 across a gap",
			why:   "acknowledging the sequence just received declares everything below it committed, which is false across a gap -- it tells the sender to discard what never landed",
		},
		{
			name:  "promotion-not-atomic-with-settlement",
			file:  "host/boundary.go",
			old:   "\tatt.Transitions = append(att.Transitions, StateSettled)\n\tr.promoteLocked(att)\n}",
			new:   "\tatt.Transitions = append(att.Transitions, StateSettled)\n}",
			claim: "boundary/drain-stale-promotes-atomically",
			want:  "the drain lost the grant",
			why:   "settling and promoting separately leaves a window in which a crash loses the grant, or a successor arrives and re-asks a human who already answered",
		},
		{
			name:  "denial-leaves-no-record",
			file:  "host/boundary.go",
			old:   "\tatt, _, oerr := b.Recorder.OpenSettled(id, req, contract.OutcomeDenied, why, DispositionBeforeCommit)",
			new:   "\tvar att *Attempt = \u0026Attempt{}\n\tvar oerr error",
			claim: "boundary/waiting-guards-before-registration",
			want:  "left no record at all",
			why:   "ADR 0030 §8: a denial is opened and completed together, and denials are the observations candidate 12 will be tuned against -- losing them is not a small loss",
		},
		{
			name:  "response-guard-not-derived-from-the-record",
			file:  "host/boundary.go",
			old:   "\tif _, awaiting := b.Recorder.AwaitingResponse(invocationID); awaiting {\n\t\treturn true\n\t}",
			new:   "",
			claim: "question/execution-waits",
			want:  "did not guard the execution",
			why:   "a parallel in-memory flag is lost on restart, so a restarted Orchestrator lets an execution still awaiting an answer act and even claim a terminal result",
		},
		{
			name:  "denial-does-not-claim-its-correlation",
			file:  "host/boundary.go",
			old:   "\tif r.InOpenSettled != nil {\n\t\tr.InOpenSettled()\n\t}",
			new:   "\tif r.InOpenSettled != nil {\n\t\tr.InOpenSettled()\n\t}\n\tdefer delete(r.byCorrelation, correlationKey(invocation, req.Correlation))",
			claim: "boundary/denial-binds-its-correlation",
			want:  "produced a second record",
			why:   "one correlation is one logical action; leaving it unclaimed lets the same key produce two terminal records, or a denial AND an effect once the wait clears",
		},
		{
			name:  "outbox-records-after-transmission",
			file:  "reviewagent/outbox.go",
			old:   "\ta.mu.Lock()\n\tdefer a.mu.Unlock()\n\tstream, seq, err := a.w.Send(a.inv.ID(), a.epoch, kind, body)",
			new:   "\tstream, seq, err := a.w.Send(a.inv.ID(), a.epoch, kind, body)\n\ttime.Sleep(400 * time.Millisecond)\n\ta.mu.Lock()\n\tdefer a.mu.Unlock()",
			claim: "events/retention-registered-before-the-ack",
			want:  "stranded by an ack that raced retention",
			why:   "an acknowledgement processed against an empty outbox leaves the entry appended after it permanently retained -- the sender waits forever for a release that already happened",
		},
		{
			name:  "denial-uniqueness-not-atomic",
			file:  "host/boundary.go",
			old:   "\tif r.InOpenSettled != nil {\n\t\tr.InOpenSettled()\n\t}",
			new:   "\tif r.InOpenSettled != nil {\n\t\tr.mu.Unlock()\n\t\tr.InOpenSettled()\n\t\tr.mu.Lock()\n\t}",
			claim: "boundary/concurrent-denials-produce-one-record",
			want:  "records for one correlation",
			why:   "lookup and insert in separate critical sections let two concurrent callers each append a terminal record and each overwrite the durable mapping -- the mutation reopens exactly that window",
		},
		{
			name:  "capability-set-not-enforced",
			file:  "host/boundary.go",
			old:   "\tif !inv.HasCapability(action) {",
			new:   "\tif false && !inv.HasCapability(action) {",
			claim: "capability/denial-is-data",
			want:  "no denial was recorded",
			why:   "admission is the gate an empty policy must not be able to disable",
		},
		{
			name:  "correlation-not-bound-to-its-action",
			file:  "host/boundary.go",
			old:   "\tif existing.Action != req.Action {",
			new:   "\tif false && existing.Action != req.Action {",
			claim: "boundary/correlation-is-bound",
			want:  "reused for a different action returned",
			why:   "a key that is not bound to its logical action replays the result of a different one, and the boundary reports success for work it never did",
		},
		{
			name:  "correlation-not-bound-to-its-arguments",
			file:  "host/boundary.go",
			old:   "\tif existing.ArgsDigest != digest {",
			new:   "\tif false && existing.ArgsDigest != digest {",
			claim: "boundary/correlation-is-bound",
			want:  "different arguments returned",
			why:   "same action, different arguments, same key: the second call receives a result computed for the first",
		},
		{
			name:  "admission-closure-not-linearized",
			file:  "host/boundary.go",
			old:   "func (b *Boundary) CloseAdmission(invocationID string) {\n\tb.admitMu.Lock()\n\tdefer b.admitMu.Unlock()",
			new:   "func (b *Boundary) CloseAdmission(invocationID string) {",
			claim: "boundary/admission-closure-linearizes",
			want:  "returned while a registration was in flight",
			why:   "closure that does not linearize with registration leaves a window in which an attempt joins the set AFTER the drain closed it",
		},
		{
			name:  "watermark-does-not-outlive-the-process",
			file:  "host/boundary.go",
			old:   "\treturn r.watermarks[wmKey(invocation, epoch, stream)]",
			new:   "\t_ = invocation\n\t_ = epoch\n\t_ = stream\n\treturn 0",
			claim: "events/prior-epoch-replay",
			want:  "nothing was committed to the watermark",
			why:   "in-memory deduplication is reset by the restart it exists to survive, so a replayed usage event is counted twice",
		},
		{
			name:  "no-drain-before-the-fence",
			file:  "host/runtime.go",
			old:   "\t\tif !drained {\n\t\t\t// An undrained action means no positive receipt, whatever the",
			new:   "\t\tif false {\n\t\t\t// An undrained action means no positive receipt, whatever the",
			claim: "cancel/undrained-action",
			want:  "positive receipt was issued with",
			why:   "ADR 0030 §5: fencing the execution resource says nothing about an in-flight data-plane write or forge push, which land outside every resource domain",
		},
		{
			name:  "headless-block-waits-for-a-courtesy-exit",
			file:  "host/runtime.go",
			old:   "\t\t\tif res.Outcome == contract.OutcomeBlocked && !blockedStop {",
			new:   "\t\t\tif false && res.Outcome == contract.OutcomeBlocked && !blockedStop {",
			claim: "gate/headless-block-is-orchestrator-driven",
			want:  "status timed_out",
			why:   "a non-cooperative runtime keeps making model calls and doing unmediated work under a Story that is already terminally blocked",
		},
		{
			name:  "envelope-not-validated",
			file:  "host/runtime.go",
			old:   "\tif !knownAgentTypes[env.Type] {",
			new:   "\tif false && !knownAgentTypes[env.Type] {",
			claim: "protocol/unknown-message-type-is-fatal",
			want:  "the violation was not detected",
			why:   "ignoring an unimplemented type lets a runtime speaking a different contract look healthy while its work is silently dropped",
		},
		{
			name:  "malformed-known-body-ignored",
			file:  "host/runtime.go",
			old:   "\t\tif u, err = contract.Decode[contract.Usage](env); err == nil && u.CallRef == \"\" {",
			new:   "\t\tif u, err = contract.Decode[contract.Usage](env); err == nil && u.CallRef == \"never\" {",
			claim: "protocol/malformed-known-body-is-fatal",
			want:  "the malformed body was not detected",
			why:   "a usage record that joins to nothing is unattributable spend recorded as though it were attributed",
		},
	}
}

type result struct {
	m      mutation
	killed bool
	detail string
}

func main() {
	if _, err := os.Stat(markerPath); err == nil {
		fmt.Fprintf(os.Stderr,
			"refusing to start: %s exists, so a previous run did not restore.\n"+
				"Run `git status` and `git checkout` the spike before retrying.\n", markerPath)
		os.Exit(2)
	}

	muts := mutations()

	// Snapshot every file this run will touch, and remember its digest, so
	// restoration is verified rather than assumed.
	originals := map[string][]byte{}
	digests := map[string]string{}
	for _, m := range muts {
		if _, ok := originals[m.file]; ok {
			continue
		}
		b, err := os.ReadFile(m.file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", m.file, err)
			os.Exit(2)
		}
		originals[m.file] = b
		digests[m.file] = digest(b)
	}

	// A positive control: the suite must be green BEFORE anything is mutated.
	// Without it, a mutation that "kills" an already-failing claim proves
	// nothing.
	fmt.Println("positive control: running the suite unmutated...")
	if out, ok := runSuite(""); !ok {
		fmt.Fprintf(os.Stderr, "positive control FAILED; the suite is not green to begin with:\n%s\n", out)
		os.Exit(2)
	}
	fmt.Println("positive control: green")
	fmt.Println()

	var results []result
	for _, m := range muts {
		results = append(results, apply(m, originals, digests))
	}

	fmt.Println()
	killed := 0
	for _, r := range results {
		status := "SURVIVED"
		if r.killed {
			status = "KILLED"
			killed++
		}
		fmt.Printf("%-9s %-40s %s\n", status, r.m.name, r.detail)
	}
	fmt.Printf("\n%d/%d mutations killed for their named reason\n", killed, len(results))
	fmt.Println("\nProtected defects:")
	for _, r := range results {
		fmt.Printf("  %-40s %s\n", r.m.name, r.m.why)
	}

	if killed != len(results) {
		fmt.Fprintln(os.Stderr, "\nA SURVIVOR INDICTS THE TEST, not the mutation: interrogate the assertion first.")
		os.Exit(1)
	}
}

func apply(m mutation, originals map[string][]byte, digests map[string]string) result {
	src := string(originals[m.file])
	if strings.Count(src, m.old) != 1 {
		return result{m: m, detail: fmt.Sprintf(
			"MUTATION DID NOT APPLY: anchor occurs %d times in %s (want exactly 1)",
			strings.Count(src, m.old), m.file)}
	}

	if err := os.WriteFile(markerPath, []byte(m.name), 0o600); err != nil {
		return result{m: m, detail: "cannot write marker: " + err.Error()}
	}
	// Restore happens on every ordinary path; the marker covers the rest.
	defer func() {
		for file, b := range originals {
			_ = os.WriteFile(file, b, 0o600)
		}
		for file, want := range digests {
			b, err := os.ReadFile(file)
			if err != nil || digest(b) != want {
				fmt.Fprintf(os.Stderr, "RESTORATION FAILED for %s; do not trust later runs\n", file)
				os.Exit(3)
			}
		}
		_ = os.Remove(markerPath)
	}()

	mutated := strings.Replace(src, m.old, m.new, 1)
	if err := os.WriteFile(m.file, []byte(mutated), 0o600); err != nil {
		return result{m: m, detail: "cannot write mutant: " + err.Error()}
	}

	// The mutant must COMPILE. A compiler failure is not a killed mutation.
	if out, err := run(20*time.Second, "go", "build", "./..."); err != nil {
		return result{m: m, detail: "MUTANT DOES NOT COMPILE (not evidence): " + firstLine(out)}
	}

	out, green := runSuite(m.claim)
	if green {
		return result{m: m, detail: "claim " + m.claim + " still passed"}
	}

	// A non-green run is not yet evidence. The suite must have RUN to its own
	// summary: a mutant that makes the suite hang until the timeout, or crash
	// before it reports, kills nothing and proves nothing.
	if !suiteRan(out) {
		return result{m: m, detail: "INCONCLUSIVE: the suite did not reach its summary " +
			"(hang, crash, or timeout) -- nothing is established"}
	}
	if claimsRun(out) == 0 {
		return result{m: m, detail: "INCONCLUSIVE: selector " + m.claim +
			" matched no claim -- the name is stale, and nothing was tested"}
	}

	// It failed -- but it must have failed AS the named claim, FALSIFIED rather
	// than ERROR, and for the stated reason.
	line := claimLine(out, m.claim)
	switch {
	case line == "":
		return result{m: m, detail: "claim " + m.claim + " did not run (empty selector?)"}
	case strings.HasPrefix(line, "ERROR"):
		return result{m: m, detail: "claim ERRORED rather than falsified (inconclusive): " + line}
	case !strings.HasPrefix(line, "FALSIFIED"):
		return result{m: m, detail: "unexpected outcome: " + line}
	case !strings.Contains(line, m.want):
		return result{m: m, detail: "falsified for the WRONG reason; want " +
			fmt.Sprintf("%q", m.want) + ", got: " + line}
	}
	return result{m: m, killed: true, detail: "falsified: " + strings.TrimSpace(
		strings.TrimPrefix(line, "FALSIFIED"))}
}

// runSuite runs the conformance suite, optionally filtered, and reports whether
// every claim it ran was PROVEN.
//
// Green requires BOTH the expected summary and a clean process exit. A first
// version discarded the subprocess error and concluded from the output text
// alone, so a suite that printed the summary and then hung would pass the
// positive control and every mutation check -- the harness asserting a result it
// had not actually obtained. That is the "mutation harnesses lie" failure in its
// own harness.
func runSuite(only string) (string, bool) {
	out, err, ok := runSuiteErr(only)
	if !ok && err != nil {
		out += "\n[harness] suite process error: " + err.Error() + "\n"
	}
	return out, ok
}

// runSuiteErr is runSuite with the process error kept, so a failure can say WHY
// rather than only that it happened.
func runSuiteErr(only string) (string, error, bool) {
	// -race on EVERY run. A claim can report PROVEN over a data race -- one did,
	// and review caught it rather than the suite. Race instrumentation is the
	// cheapest thing that would have caught it here.
	args := []string{"run", "-race", "."}
	if only != "" {
		args = append(args, "-run", only)
	}
	out, err := run(5*time.Minute, "go", args...)
	summaryOK := strings.Contains(out, "0 FALSIFIED, 0 ERROR")
	// A selector matching NO claims prints "0 claims: ..." and exits zero --
	// which read as green, so a mutation whose claim name had gone stale
	// survived invisibly rather than being reported as unrunnable. An empty run
	// establishes nothing.
	//
	// Matched on the WHOLE COUNT, not a substring. `strings.Contains(out,
	// "0 claims:")` also matches "60 claims:" -- and every other multiple of
	// ten -- so the guard silently condemned a full green run the moment the
	// suite reached sixty claims. A guard that fires on the thing it is meant
	// to permit is worse than no guard, because it is read as a real failure.
	if claimsRun(out) == 0 {
		return out, err, false
	}
	return out, err, summaryOK && err == nil
}

// claimsRun extracts the claim count from the suite's summary line, or -1 when
// there is no summary to read.
func claimsRun(out string) int {
	m := claimCountRE.FindStringSubmatch(out)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}

//nolint:gochecknoglobals // compiled once
var claimCountRE = regexp.MustCompile(`(?m)^(\d+) claims:`)

// suiteRan reports whether the suite reached its own summary line at all. A run
// killed before printing it establishes nothing, and must not be read as a
// mutation kill.
func suiteRan(out string) bool {
	return claimsRun(out) >= 0
}

func claimLine(out, claim string) string {
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, claim) && (strings.HasPrefix(l, "PROVEN") ||
			strings.HasPrefix(l, "FALSIFIED") || strings.HasPrefix(l, "ERROR")) {
			return l
		}
	}
	return ""
}

// run bounds every child process. An unclassified hang is not a killed
// mutation, so it must not be allowed to look like one.
func run(limit time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	b, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(b), fmt.Errorf("timed out after %s", limit)
	}
	return string(b), err
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func init() {
	// The harness edits files by relative path, so it must run from the module
	// root regardless of where it was invoked.
	if _, err := os.Stat("go.mod"); err != nil {
		if wd, e := os.Getwd(); e == nil {
			_ = os.Chdir(filepath.Dir(wd))
		}
	}
}
