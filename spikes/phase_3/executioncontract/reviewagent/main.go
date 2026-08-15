// Command reviewagent is the conformance slice's external agent: a real
// separate process that speaks ADR 0032 over stdin and stdout.
//
// It is a CODE-REVIEW agent because #282 names a standalone code-review agent as
// the contract's first external consumer. What it is NOT is a code reviewer:
// its analysis backend is an explicit stub (DR, 2026-08-13) that minimally
// satisfies the contract rather than pretending to do useful review work. The
// real build-out is Phase 3's.
//
// Modes are driven by SPIKE_MODE so that every scenario is exercised by the
// same real binary over the same real transport. Each mode models a failure the
// contract has to have an answer for, not a variant of the agent.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"maestro-spike/phase3/executioncontract/contract"
)

const (
	adapterName    = "spike-reviewagent"
	executableVer  = "0.1.0"
	stubBackendTag = "stub-not-a-reviewer"
)

var (
	actionReadDiff = contract.ActionID{Kind: "repo", Verb: "read_diff"}
	actionPublish  = contract.ActionID{Kind: "artifact", Verb: "publish"}
	actionForge    = contract.ActionID{Kind: "forge", Verb: "push"}
	actionSecret   = contract.ActionID{Kind: "secret", Verb: "read"}
)

// ackKey scopes an acknowledgement: each (epoch, stream) is its own space.
type ackKey struct {
	epoch  uint64
	stream string
}

type agent struct {
	w     *contract.Writer
	inv   *contract.Invocation
	mode  string
	epoch uint64

	results chan contract.ActionResult
	attach  chan contract.AttachAck
	cancel  chan contract.Cancel
	readErr chan error

	mu sync.Mutex
	// ackedThrough is PER EPOCH. A single scalar cannot express two
	// incarnations' progress, and Ack carries an epoch precisely because the
	// sequence spaces are separate.
	ackedThrough map[ackKey]uint64
	// outbox retains the envelopes this runtime is obliged to be able to
	// replay -- only the replay-obligated types (§4), not everything.
	outbox []retained

	cancelled bool
}

func main() {
	mode := os.Getenv("SPIKE_MODE")
	if mode == "" {
		mode = "normal"
	}
	a := &agent{
		w:            contract.NewWriter(os.Stdout),
		mode:         mode,
		ackedThrough: map[ackKey]uint64{},
		results:      make(chan contract.ActionResult, 16),
		attach:       make(chan contract.AttachAck, 4),
		cancel:       make(chan contract.Cancel, 1),
		readErr:      make(chan error, 1),
	}
	if err := a.run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", adapterName, err)
		os.Exit(1)
	}
}

func (a *agent) send(msgType string, body any) error {
	_, _, err := a.w.Send(a.inv.ID(), a.epoch, msgType, body)
	return err
}

func (a *agent) run() error {
	rd := contract.NewReader(os.Stdin)

	// ---- handshake (§11) ----
	env, err := rd.Next()
	if err != nil {
		return fmt.Errorf("await hello: %w", err)
	}
	if env.Type != contract.TypeHello {
		return fmt.Errorf("expected hello, got %q", env.Type)
	}
	hello, err := contract.Decode[contract.Hello](env)
	if err != nil {
		return err
	}

	selected := contract.Version
	if a.mode == "bad_version" {
		// A version nobody offered. The Orchestrator must refuse at DISPATCH,
		// producing no execution and no terminal result (§5).
		selected = "9999.0"
	} else if !slices.Contains(hello.Supported, contract.Version) {
		return fmt.Errorf("no mutually supported version; offered %v", hello.Supported)
	}

	if _, _, err := a.w.Send("", 0, contract.TypeHelloAck, contract.HelloAck{
		Selected: selected,
		// This runtime cannot resume its own session. §7: that does not make it
		// permanently resident -- after bounded retention it is released and
		// restarted from the last durable workflow artifact.
		Resumable:  false,
		Adapter:    adapterName,
		Executable: executableVer,
	}); err != nil {
		return err
	}
	if a.mode == "bad_version" {
		return nil
	}

	// ---- the invocation ----
	env, err = rd.Next()
	if err != nil {
		return fmt.Errorf("await invoke: %w", err)
	}
	if env.Type != contract.TypeInvoke {
		return fmt.Errorf("expected invoke, got %q", env.Type)
	}
	inv, err := contract.Decode[contract.Invocation](env)
	if err != nil {
		return err
	}
	a.inv = &inv
	// The epoch is the Orchestrator's, carried on the mutable bindings. Every
	// event this incarnation emits is identified by (invocation, epoch, seq).
	a.epoch = inv.Bindings.Epoch
	// The epoch owns its own sequence space; the handshake's sequences are not
	// part of it.
	a.w.ResetSeq()

	// The bindings carry a resource REFERENCE and no path. There is nothing
	// here to open, which is the point: v1's RunOptions.WorkDir is what this
	// replaces.
	if _, ok := inv.Resource("incubator"); !ok {
		return fmt.Errorf("no incubator reference in bindings")
	}

	go a.readLoop(rd)

	started := contract.Started{
		Adapter:         adapterName,
		ExecutableVer:   executableVer,
		ContractVersion: contract.Version,
	}
	if a.mode == "lying_started" {
		// The handshake said one thing; `started` says another. Identity that is
		// carried and never compared is a self-report recorded as a fact (§9).
		started.Adapter = "something-else"
	}
	if err := a.send(contract.TypeStarted, started); err != nil {
		return err
	}

	return a.work()
}

// readLoop demultiplexes host-to-agent messages. Cancellation must be
// receivable WHILE work is in flight, which is what makes cooperative
// cancellation cooperative rather than a poll.
func (a *agent) readLoop(rd *contract.Reader) {
	for {
		env, err := rd.Next()
		if err != nil {
			a.readErr <- err
			return
		}
		switch env.Type {
		case contract.TypeActionResult:
			if res, derr := contract.Decode[contract.ActionResult](env); derr == nil {
				a.results <- res
			}
		case contract.TypeAttachAck:
			if ack, derr := contract.Decode[contract.AttachAck](env); derr == nil {
				a.attach <- ack
			}
		case contract.TypeCancel:
			if c, derr := contract.Decode[contract.Cancel](env); derr == nil {
				a.cancel <- c
			}
		case contract.TypeAck:
			// The sender's release signal. Everything at or below Through is
			// durably committed; anything above it this runtime must be
			// prepared to replay, which is what makes delivery at-least-once
			// rather than merely deduplicated.
			if k, derr := contract.Decode[contract.Ack](env); derr == nil {
				a.mu.Lock()
				key := ackKey{epoch: k.Epoch, stream: k.Stream}
				if k.Through > a.ackedThrough[key] {
					a.ackedThrough[key] = k.Through
				}
				// Released: everything at or below the watermark for that
				// (epoch, stream) can leave the outbox.
				kept := a.outbox[:0]
				for _, r := range a.outbox {
					if !(r.epoch == k.Epoch && r.stream == k.Stream && r.seq <= k.Through) {
						kept = append(kept, r)
					}
				}
				a.outbox = kept
				a.mu.Unlock()
			}
		}
	}
}

// request issues one mediated action and waits for the boundary's answer.
//
// The correlation identifies the LOGICAL action. It is derived here from the
// invocation and a step ordinal, which is sufficient for a deterministic agent
// -- but the contract does NOT rely on that: after a restart the agent asks the
// Orchestrator what is outstanding (§6) rather than reconstructing correlations
// it might not be able to reproduce.
func (a *agent) request(step int, action contract.ActionID, args any) (contract.ActionResult, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return contract.ActionResult{}, err
	}
	corr := fmt.Sprintf("%s#%d", a.inv.ID(), step)
	if err := a.send(contract.TypeActionRequest, contract.ActionRequest{
		Correlation: corr,
		Action:      action,
		Arguments:   raw,
	}); err != nil {
		return contract.ActionResult{}, err
	}
	return a.awaitResult(action)
}

func (a *agent) awaitResult(action contract.ActionID) (contract.ActionResult, error) {
	for {
		select {
		case res := <-a.results:
			if res.Outcome == contract.OutcomeOutstanding {
				// Someone asked twice for one logical action. Keep waiting for
				// the original submission's result rather than reissuing.
				continue
			}
			return res, nil
		case c := <-a.cancel:
			a.cancelled = true
			// Reaching a safe boundary means finishing the atomic action
			// already in flight, then issuing no further ones.
			select {
			case res := <-a.results:
				return res, nil
			case <-time.After(time.Duration(c.DeadlineMS) * time.Millisecond):
				return contract.ActionResult{}, fmt.Errorf("cancelled while awaiting %s", action)
			}
		case err := <-a.readErr:
			return contract.ActionResult{}, err
		case <-time.After(30 * time.Second):
			return contract.ActionResult{}, fmt.Errorf("no answer for %s", action)
		}
	}
}

func (a *agent) checkCancel() bool {
	select {
	case <-a.cancel:
		a.cancelled = true
	default:
	}
	return a.cancelled
}

func (a *agent) terminal(res contract.TerminalResult) error {
	return a.send(contract.TypeTerminal, res)
}

// terminalOnCancel is what the agent reports once it has reached a safe
// boundary. In claim_completed_after_cancel it deliberately reports the WRONG
// thing, so the host's override path (§4) has something to override.
func (a *agent) terminalOnCancel() error {
	if a.mode == "claim_completed_after_cancel" {
		return a.terminal(contract.Completed(contract.DispositionChanged, "finished anyway"))
	}
	return a.terminal(contract.Cancelled(contract.ReasonSuperseded, "stopped at a safe boundary"))
}

//nolint:gocyclo // A flat mode switch is more legible than dispatch indirection.
func (a *agent) work() error {
	switch a.mode {
	case "bad_protocol":
		// §8's other side: a malformed message is FATAL, unlike a denial.
		_, _ = os.Stdout.WriteString("this is not a contract message\n")
		time.Sleep(2 * time.Second)
		return nil

	case "hang":
		// Never terminates. The Orchestrator's deadline is what ends this, and
		// a deadline is an Orchestrator-observed fact -- which is why
		// timed_out is a status rather than a failure class (§5).
		for {
			_ = a.send(contract.TypeHeartbeat, contract.Heartbeat{Phase: "stuck"})
			time.Sleep(200 * time.Millisecond)
		}

	case "restart_reattach":
		// A restarted runtime ASKS what is outstanding rather than announcing
		// correlations it derived. §6: the Orchestrator holds the durable
		// attempt records, so it is the authority.
		return a.reattachThenFinish()

	case "duplicate_request":
		// Two requests for one logical action. The second must not re-enter the
		// gates; the effect must commit once.
		return a.duplicateThenFinish()

	case "replay_event":
		// A redelivery: the same message under the identity already used. At-
		// least-once delivery permits it, and event identity is what has to
		// make it harmless.
		msg := contract.Activity{Message: "this message is delivered twice"}
		_ = a.send(contract.TypeActivity, msg)
		_ = a.w.Repeat(a.inv.ID(), a.epoch, contract.TypeActivity, msg)

	case "emit_usage":
		// One retained report, so the reliable stream has something committed
		// and acknowledged for a later incarnation to replay.
		_ = a.sendRetained(contract.TypeUsage, contract.Usage{
			CallRef: "call-1", InputTokens: 10, OutputTokens: 5,
			Served: a.inv.Config.Model.Served, ServedConfirmed: true,
		})
		time.Sleep(150 * time.Millisecond)
		return a.terminal(contract.Completed(contract.DispositionChanged, "emitted one usage report"))

	case "replay_beyond_gap":
		// Commit a report BEYOND a gap: sequence 1 is never sent, so the
		// watermark cannot advance past 0 while sequence 2 is committed. A
		// watermark-only check would then accept the replay of 2.
		u := contract.Usage{CallRef: "call-gap", InputTokens: 4, OutputTokens: 2,
			Served: a.inv.Config.Model.Served, ServedConfirmed: true}
		_ = a.w.SendAs(a.inv.ID(), a.epoch, 2, contract.StreamReliable, contract.TypeUsage, u)
		time.Sleep(200 * time.Millisecond) // let it commit
		_ = a.w.SendAs(a.inv.ID(), a.epoch, 2, contract.StreamReliable, contract.TypeUsage, u)
		time.Sleep(200 * time.Millisecond)
		return a.terminal(contract.Completed(contract.DispositionChanged,
			"replayed a report committed beyond a gap"))

	case "replay_before_ack":
		// A LOST acknowledgement: the report is replayed while still retained,
		// so an actual retained envelope crosses the wire twice.
		_ = a.sendRetained(contract.TypeUsage, contract.Usage{
			CallRef: "call-1", InputTokens: 7, OutputTokens: 3,
			Served: a.inv.Config.Model.Served, ServedConfirmed: true,
		})
		a.replayUnacked() // no wait: the ack has not arrived
		time.Sleep(200 * time.Millisecond)
		return a.terminal(contract.Completed(contract.DispositionChanged,
			"replayed a retained report before its acknowledgement"))

	case "replay_outbox":
		// Emit a replay-obligated event, then replay everything unacknowledged
		// under its ORIGINAL identity. The receiver must drop the replay.
		_ = a.sendRetained(contract.TypeUsage, contract.Usage{
			CallRef: "call-1", InputTokens: 10, OutputTokens: 5,
			Served: a.inv.Config.Model.Served, ServedConfirmed: true,
		})
		time.Sleep(150 * time.Millisecond) // let the ack land and release it
		a.replayUnacked()
		// Anything still retained is replayed under its ORIGINAL identity; an
		// acknowledged report has already left the outbox, so a healthy run
		// replays nothing.
		a.mu.Lock()
		remaining := len(a.outbox)
		a.mu.Unlock()
		_ = a.send(contract.TypeActivity, contract.Activity{
			Message: "outbox retained " + itoaAgent(remaining) + " after the ack"})
		return a.terminal(contract.Completed(contract.DispositionChanged,
			"outbox drained by acknowledgement"))

	case "replay_prior_epoch":
		// A restarted runtime replaying an event it emitted under the PREVIOUS
		// incarnation and never saw acknowledged. The receiver must recognise
		// it against that epoch's durable watermark -- an in-memory watermark
		// would have been reset by the restart and would count it twice.
		_, _, _ = a.w.Send(a.inv.ID(), a.epoch-1, contract.TypeUsage, contract.Usage{
			CallRef: "call-1", InputTokens: 10, OutputTokens: 5,
			Served: a.inv.Config.Model.Served, ServedConfirmed: true,
		})

	case "prior_epoch_act":
		// An ACT, not a report, from a superseded incarnation. Only replayable
		// reports may arrive from a prior epoch (§4).
		_, _, _ = a.w.Send(a.inv.ID(), a.epoch-1, contract.TypeActionRequest,
			contract.ActionRequest{Correlation: "ghost", Action: actionPublish,
				Arguments: []byte(`{"kind":"review.findings"}`)})
		time.Sleep(2 * time.Second)
		return nil

	case "ask_inline":
		// A question as inline text rather than an artifact reference.
		res, err := a.request(4, contract.ActionAsk, map[string]any{
			"text": "should a TODO marker block the candidate?", "to": "architect"})
		if err != nil {
			return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
		}
		if res.Outcome == contract.OutcomeSucceeded {
			return a.terminal(contract.Completed(contract.DispositionChanged,
				"inline question accepted"))
		}
		return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
			"inline question refused: "+res.Reason))

	case "silent_exit":
		// Ends the transport with no terminal result at all.
		_ = a.send(contract.TypeActivity, contract.Activity{Message: "leaving without a word"})
		return nil

	case "lying_stream":
		// A reliable report claiming the best-effort stream, opting itself out
		// of the obligations its own type carries.
		_ = a.w.SendAs(a.inv.ID(), a.epoch, 99, contract.StreamBestEffort,
			contract.TypeUsage, contract.Usage{CallRef: "c", InputTokens: 1})
		time.Sleep(2 * time.Second)
		return nil

	case "bad_epoch":
		// An epoch ahead of the active binding is a protocol violation: it
		// claims an incarnation the Orchestrator has not issued.
		_, _, _ = a.w.Send(a.inv.ID(), a.epoch+7, contract.TypeActivity,
			contract.Activity{Message: "from an incarnation that does not exist"})
		time.Sleep(2 * time.Second)
		return nil

	case "unknown_type":
		// A message type this build does not implement. Ignoring it lets a
		// runtime speaking a different contract look healthy while its work is
		// silently dropped.
		_ = a.send("invent_a_type", map[string]any{"anything": true})
		time.Sleep(2 * time.Second)
		return nil

	case "malformed_usage":
		// A known type with a body that violates its own contract: usage with
		// no call reference joins to nothing.
		_ = a.send(contract.TypeUsage, contract.Usage{InputTokens: 1})
		time.Sleep(2 * time.Second)
		return nil
	}

	_ = a.send(contract.TypeHeartbeat, contract.Heartbeat{Phase: "reading"})
	_ = a.send(contract.TypeActivity, contract.Activity{Message: "reading the candidate diff"})

	// ---- step 1: read the diff through a mediated action ----
	diffRes, err := a.request(1, actionReadDiff, map[string]any{
		"resource": mustResource(a.inv, "incubator").ReferenceID,
	})
	if err != nil {
		return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
	}
	if diffRes.Outcome == "" {
		// The boundary recorded an intent and returned no outcome. The agent
		// must not re-execute blindly: ADR 0030 §3 sends this to
		// reconciliation, and blind retry is how an adapted runtime duplicates
		// a forge push.
		_ = a.send(contract.TypeWarning, contract.Warning{
			Message: "attempt " + diffRes.AttemptID + " returned no outcome; not retrying",
		})
		os.Exit(3)
	}
	if diffRes.Outcome != contract.OutcomeSucceeded {
		return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
			"could not read the diff: "+diffRes.Reason))
	}

	var diff struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(diffRes.Result, &diff); err != nil {
		return a.terminal(contract.Failed(contract.ClassNonRetryableAgent, "diff payload: "+err.Error()))
	}

	// ---- the capability-denial probe ----
	if a.mode == "deny_probe" {
		// An action outside the resolved capability set. §8: this comes back as
		// DATA the agent reads and acts on, and the execution continues.
		res, err := a.request(9, actionSecret, map[string]any{"key": "anything"})
		if err != nil {
			return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
		}
		if res.Outcome != contract.OutcomeDenied {
			return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
				"expected a denial for an ungranted action, got "+string(res.Outcome)))
		}
		_ = a.send(contract.TypeActivity, contract.Activity{Message: "denied as expected; continuing"})
	}

	if a.mode == "act_after_cancel" {
		// Cancellation has been requested. A well-behaved runtime stops here;
		// this one deliberately tries one more action, so the boundary's
		// admission closure has something to refuse. ADR 0029 §7 step 2's
		// ordering exists precisely because a holder can keep creating.
		if !a.checkCancel() {
			return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
				"expected cancellation before the second action"))
		}
		res, err := a.request(5, actionPublish, map[string]any{"kind": "review.findings"})
		if err != nil {
			return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
		}
		if res.Outcome != contract.OutcomeDenied {
			return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
				"an action was admitted after cancellation: "+string(res.Outcome)))
		}
		return a.terminalOnCancel()
	}

	if a.checkCancel() {
		return a.terminalOnCancel()
	}

	// ---- the stub analysis ----
	findings := stubAnalyze(diff.Text)

	// An empty diff is a COMPLETED execution whose work was already done. This
	// is #280's disposition, decided from real data rather than a mode flag.
	if strings.TrimSpace(diff.Text) == "" {
		return a.terminal(contract.Completed(contract.DispositionAlreadySatisfied,
			"no candidate changes to review"))
	}

	if a.mode == "escalate" || a.mode == "escalate_defiant" {
		// An action whose gate requires an operator. Headless, this settles the
		// action TERMINALLY as blocked and blocks the Story immediately
		// (ADR 0030 §4); nothing will ever answer it.
		res, err := a.request(2, actionForge, map[string]any{"ref": "refs/heads/candidate"})
		if err != nil {
			return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
		}
		if res.Outcome == contract.OutcomeBlocked {
			if a.mode == "escalate_defiant" {
				// A runtime that does NOT stop when told its Story is blocked.
				// The Orchestrator must terminate it rather than waiting for a
				// courtesy exit, or a blocked Story keeps burning model calls.
				for {
					_ = a.send(contract.TypeHeartbeat, contract.Heartbeat{Phase: "ignoring the block"})
					time.Sleep(150 * time.Millisecond)
				}
			}
			// The caller performs no further LLM turns and issues no further
			// actions. It stops WITHOUT a terminal event: the Story's state is
			// not the agent's to declare, and `blocked` is composed by the
			// Orchestrator from the boundary's own state (§5).
			_ = a.send(contract.TypeActivity, contract.Activity{
				Message: "a decision is required and has no responder; stopping",
			})
			os.Exit(4)
		}
	}

	// ---- ask, as a MEDIATED ACTION ----
	// Routing a question to another principal changes execution state and
	// invokes Orchestrator message routing, so under ADR 0022 it passes through
	// an action record. A raw `question` event would be a side door around
	// ADR 0030's boundary.
	if a.mode == "ask" {
		// The QUESTION is an artifact, not inline text. ADR 0021 makes
		// artifacts the sole agent handoff, so a question carried as an action
		// argument would be exactly the direct principal-to-principal payload
		// that rule forbids. Publish first, then route the reference.
		qRes, err := a.request(6, actionPublish, map[string]any{
			"kind": "agent.question",
			"text": "should a TODO marker block the candidate?",
		})
		if err != nil || qRes.Outcome != contract.OutcomeSucceeded {
			return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
				"could not publish the question artifact"))
		}
		var pub struct {
			ArtifactID string `json:"artifact_id"`
		}
		_ = json.Unmarshal(qRes.Result, &pub)

		res, err := a.request(4, contract.ActionAsk, map[string]any{
			"question_artifact": pub.ArtifactID,
			"to":                "architect",
		})
		if err != nil {
			return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
		}
		if res.Outcome != contract.OutcomeSucceeded {
			return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
				"ask was not routed: "+res.Reason))
		}
		// The result is a DELIVERY ACKNOWLEDGEMENT, not the answer. The answer
		// arrives as an artifact reference on a LATER incarnation's bindings, so
		// this incarnation ends here -- without a terminal result, because the
		// execution is not over.
		_ = a.send(contract.TypeActivity, contract.Activity{
			Message: "question routed; this incarnation ends awaiting an answer"})
		os.Exit(5)
	}

	if a.mode == "answered" {
		// A later incarnation, whose BINDINGS carry the answer. The immutable
		// configuration could not have acquired it.
		if len(a.inv.Bindings.Inbound) == 0 {
			return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
				"resumed with no inbound answer artifact"))
		}
		_ = a.send(contract.TypeActivity, contract.Activity{
			Message: "answer artifact " + a.inv.Bindings.Inbound[0].ArtifactID + " received"})
		return a.terminal(contract.Completed(contract.DispositionChanged,
			"resumed with the answer and finished"))
	}

	// ---- step 3: publish findings as an artifact ----
	pubRes, err := a.request(3, actionPublish, map[string]any{
		"kind":     "review.findings",
		"backend":  stubBackendTag,
		"findings": findings,
	})
	if err != nil {
		return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
	}
	if pubRes.Outcome != contract.OutcomeSucceeded {
		return a.terminal(contract.Failed(contract.ClassNonRetryableAgent,
			"could not publish findings: "+pubRes.Reason))
	}

	if a.mode == "ignore_cancel" {
		// A runtime that will not stop. The grace period expires, the domain is
		// fenced, and only then is a terminal result recorded (§6 step 4).
		for {
			_ = a.send(contract.TypeHeartbeat, contract.Heartbeat{Phase: "ignoring cancellation"})
			time.Sleep(200 * time.Millisecond)
		}
	}

	if a.checkCancel() {
		return a.terminalOnCancel()
	}

	if a.mode == "bad_axes" {
		// A terminal result that violates §5's applicability rule: completed
		// AND carrying a failure class. The host must reject it as a protocol
		// violation rather than record it.
		cls := contract.ClassNonRetryableAgent
		d := contract.DispositionChanged
		return a.terminal(contract.TerminalResult{
			Status: contract.StatusCompleted, Disposition: &d, FailureClass: &cls,
		})
	}

	// NO provenance event is emitted, and that is deliberate. This stub makes
	// no model calls, so there is no model input to account for -- and
	// provenance is a PER-MODEL-CALL record (§9). Emitting a `closed` status
	// for a call that never happened would fabricate exactly the coverage the
	// conformance report declares missing.

	return a.terminal(contract.Completed(contract.DispositionChanged,
		fmt.Sprintf("%d finding(s) from the %s backend", len(findings), stubBackendTag)))
}

// reattachThenFinish models a runtime restarted into an existing execution.
func (a *agent) reattachThenFinish() error {
	if err := a.send(contract.TypeAttach, contract.Attach{}); err != nil {
		return err
	}
	select {
	case ack := <-a.attach:
		settled := 0
		for _, act := range ack.Actions {
			if act.State == contract.AttachSettled {
				settled++
			}
		}
		_ = a.send(contract.TypeActivity, contract.Activity{
			Message: fmt.Sprintf("re-attached: %d action(s), %d settled", len(ack.Actions), settled),
		})
		if settled > 0 {
			// Already done. Reissuing would be a second logical action.
			return a.terminal(contract.Completed(contract.DispositionChanged,
				fmt.Sprintf("resumed; %d settled action(s) were not reissued", settled)))
		}
		return a.terminal(contract.Completed(contract.DispositionAlreadySatisfied,
			"resumed; nothing outstanding"))
	case err := <-a.readErr:
		return err
	case <-time.After(10 * time.Second):
		return fmt.Errorf("no attach_ack")
	}
}

// duplicateThenFinish issues the same logical action twice.
func (a *agent) duplicateThenFinish() error {
	raw, _ := json.Marshal(map[string]any{"kind": "review.findings", "backend": stubBackendTag})
	corr := fmt.Sprintf("%s#dup", a.inv.ID())
	req := contract.ActionRequest{Correlation: corr, Action: actionPublish, Arguments: raw}

	if err := a.send(contract.TypeActionRequest, req); err != nil {
		return err
	}
	// The duplicate goes out before the first has been answered, so the
	// boundary sees a request for an action that is still in flight.
	if err := a.send(contract.TypeActionRequest, req); err != nil {
		return err
	}

	res, err := a.awaitResult(actionPublish)
	if err != nil {
		return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
	}
	if res.Outcome != contract.OutcomeSucceeded {
		return a.terminal(contract.Failed(contract.ClassNonRetryableAgent, res.Reason))
	}
	return a.terminal(contract.Completed(contract.DispositionChanged, "duplicate request issued once"))
}

// stubAnalyze is the explicitly-stubbed backend. It is NOT a code reviewer and
// must not be read as one: it flags added lines containing a marker, which is a
// mechanical rule chosen because it is obviously not review. Its only job is to
// give the contract something real to carry.
func stubAnalyze(diff string) []map[string]any {
	var out []map[string]any
	for i, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && strings.Contains(line, "TODO") {
			out = append(out, map[string]any{
				"severity": "low",
				"line":     i + 1,
				"message":  "added line carries a TODO marker",
				"backend":  stubBackendTag,
			})
		}
	}
	return out
}

func mustResource(inv *contract.Invocation, kind string) contract.ResourceRef {
	r, _ := inv.Resource(kind)
	return r
}

// itoaAgent is a tiny local formatter; the agent deliberately imports nothing
// beyond the contract.
func itoaAgent(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
