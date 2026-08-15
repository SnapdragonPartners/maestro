package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"time"

	"maestro-spike/phase3/executioncontract/contract"
)

// FenceReceipt is ADR 0029 §7's three-valued receipt. Reproduced here because
// ADR 0032 §6 makes a positive receipt the precondition for recording any
// FORCED terminal result.
type FenceReceipt string

const (
	FenceTerminated  FenceReceipt = "terminated"
	FenceIsolated    FenceReceipt = "isolated"
	FenceUnconfirmed FenceReceipt = "unconfirmed"
)

// Positive reports whether this receipt permits a terminal result to be
// recorded. `unconfirmed` does not.
func (f FenceReceipt) Positive() bool {
	return f == FenceTerminated || f == FenceIsolated
}

// Fencer stops a resource's domain.
type Fencer func(resource contract.ResourceRef) FenceReceipt

// Outcome is what the host records for an execution. It is deliberately NOT
// contract.TerminalResult: the runtime's terminal event is a CLAIM (§4), and
// this is what the Orchestrator concluded.
type Outcome struct {
	Result contract.TerminalResult
	// Claimed is the runtime's own terminal event, retained even when the
	// Orchestrator's observation overrode it -- a runtime that believed it
	// finished is a fact worth having, and v1 discards exactly this by
	// correcting a parsed signal from a side channel with no record that the
	// two ever disagreed.
	Claimed *contract.TerminalResult
	// Overridden says the Orchestrator's observation won.
	Overridden bool
	// Forced says the Orchestrator ended this execution rather than the runtime
	// reporting its own end. Every forced path owes a positive fence receipt.
	Forced       bool
	FenceReceipt FenceReceipt
	// ActionsDrained reports whether every admitted action settled before the
	// fence was taken (ADR 0030 §5). OutstandingActions is how many did not.
	ActionsDrained     bool
	OutstandingActions int
	// TerminalViolation is set when the terminal EVENT was malformed or its
	// axes invalid. That is a framing failure and takes the forced path, unlike
	// a well-formed claim the Orchestrator merely disagrees with.
	TerminalViolation string
	// AwaitingResponse names the question artifact an execution is waiting on.
	// Non-terminal: the incarnation ended, the execution did not.
	AwaitingResponse string
	// CommittedDuringDrain counts effects that landed after admission closed.
	// Permitted, and recorded so a cancellation can say what went out with it.
	CommittedDuringDrain int

	Events     []string
	Usage      []contract.Usage
	Provenance []contract.Provenance
	Handshake  contract.HelloAck
	Reconciled ReconcileReport
	// StartedSeen and IdentityError record §9's identity check: `started` must
	// be first, unique, and agree with the handshake.
	StartedSeen   bool
	IdentityError string
	// DuplicateEvents counts messages rejected by event identity (§4).
	DuplicateEvents int
	DispatchErr     error
}

// Runtime supervises one external agent process over the local transport.
type Runtime struct {
	// BinaryPath is the adapter executable. §1: the ADAPTER is the contract
	// endpoint, not the runtime, because a runtime like Claude Code writes its
	// own structured output to stdout and two protocols cannot share a stream.
	BinaryPath string
	Args       []string
	Boundary   *Boundary
	Fencer     Fencer

	// SupportedVersions is the Orchestrator's offer at the handshake.
	SupportedVersions []string

	// CancelAfter requests cooperative cancellation once the execution has run
	// this long. Zero disables.
	CancelAfter   time.Duration
	CancelReasonV contract.CancellationReason
	// CancelGrace is the bounded window of ADR 0029 §7 step 1. On expiry the
	// domain is fenced.
	CancelGrace time.Duration

	// OnCancelRequested fires when cancellation is sent, so a scenario can
	// observe what the transport does afterwards.
	OnCancelRequested func()

	// CancelWhen requests cancellation on a SIGNAL rather than a timer. A
	// timer-based scenario is only as reliable as its margin: under race
	// instrumentation and load, "cancel at 400ms" can fire before the state it
	// meant to interrupt is even reached, and the claim then fails for a reason
	// that has nothing to do with the property.
	CancelWhen <-chan struct{}
}

// Run executes one invocation to a terminal result.
//
//nolint:gocyclo // The gate and event dispatch is inherently branchy; splitting it would hide the state machine this spike exists to make legible.
func (r *Runtime) Run(ctx context.Context, inv *contract.Invocation) Outcome {
	out := Outcome{}
	id := inv.ID()

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	cmd := exec.CommandContext(runCtx, r.BinaryPath, r.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		out.DispatchErr = fmt.Errorf("stdin pipe: %w", err)
		return out
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		out.DispatchErr = fmt.Errorf("stdout pipe: %w", err)
		return out
	}
	// stderr is diagnostic only and never carries protocol (§10). Draining it
	// rather than sharing the parent's keeps a chatty runtime from being able
	// to desynchronize anything.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		out.DispatchErr = fmt.Errorf("stderr pipe: %w", err)
		return out
	}
	if err := cmd.Start(); err != nil {
		out.DispatchErr = fmt.Errorf("start adapter: %w", err)
		return out
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	w := contract.NewWriter(stdin)
	rd := contract.NewReader(stdout)

	defer func() {
		_ = stdin.Close()
		// Cancel BEFORE waiting. Deferred calls run last-in-first-out, so a
		// `defer cancelRun()` registered earlier runs after this one -- meaning
		// Wait would block on a process this function has already decided to
		// stop. An adapter that ignores its closed stdin (the `hang` shape)
		// then spins until the caller's own context expires, which is how a
		// 400ms timeout scenario came to take a minute.
		cancelRun()
		_ = cmd.Wait()
	}()

	// ---- handshake (§11) ----
	supported := r.SupportedVersions
	if len(supported) == 0 {
		supported = []string{contract.Version}
	}
	if _, _, err := w.Send("", 0, contract.TypeHello, contract.Hello{Supported: supported}); err != nil {
		out.DispatchErr = fmt.Errorf("send hello: %w", err)
		return out
	}
	env, err := rd.Next()
	if err != nil {
		out.DispatchErr = fmt.Errorf("await hello_ack: %w", err)
		return out
	}
	if env.Type != contract.TypeHelloAck {
		out.DispatchErr = fmt.Errorf("expected hello_ack, got %q", env.Type)
		return out
	}
	ack, err := contract.Decode[contract.HelloAck](env)
	if err != nil {
		out.DispatchErr = fmt.Errorf("decode hello_ack: %w", err)
		return out
	}
	out.Handshake = ack
	if !slices.Contains(supported, ack.Selected) {
		// A version mismatch fails at DISPATCH, before any resource is
		// acquired (§11). There is no execution and therefore no terminal
		// result -- a refused invocation is not a sixth execution status (§5).
		out.DispatchErr = fmt.Errorf("adapter selected unsupported version %q", ack.Selected)
		return out
	}

	// ---- the invocation ----
	if _, _, err := w.Send(id, inv.Bindings.Epoch, contract.TypeInvoke, inv); err != nil {
		out.DispatchErr = fmt.Errorf("send invoke: %w", err)
		return out
	}

	started := time.Now()
	var (
		cancelRequested bool
		blockedStop     bool
		cancelReason    contract.CancellationReason
		observedTerm    *contract.TerminalResult
	)

	// Event identity (§4) is (execution, epoch, seq), and the deduplication
	// state lives on the recorder rather than here, so it survives a restart.
	// An in-memory watermark would let a replayed usage event be counted twice
	// after a crash.
	negotiated := ack.Selected

	var deadline <-chan time.Time
	if inv.Config.Budgets.MaxWallClockMS > 0 {
		deadline = time.After(time.Duration(inv.Config.Budgets.MaxWallClockMS) * time.Millisecond)
	}
	var cancelAt <-chan time.Time
	if r.CancelAfter > 0 {
		cancelAt = time.After(r.CancelAfter)
	}
	cancelSignal := r.CancelWhen

	type readItem struct {
		env contract.Envelope
		err error
	}
	items := make(chan readItem)
	go func() {
		defer close(items)
		for {
			e, err := rd.Next()
			items <- readItem{env: e, err: err}
			if err != nil {
				return
			}
		}
	}()

	// actionDone carries results from actions running OFF this loop, so a wait
	// never stops the loop from serving cancellation or heartbeats.
	actionDone := make(chan contract.ActionResult, 16)
	var graceExpiry <-chan time.Time

	drainWindow := r.CancelGrace
	if drainWindow == 0 {
		drainWindow = 2 * time.Second
	}

	requestCancel := func() {
		if !cancelRequested {
			cancelRequested = true
			cancelReason = r.CancelReasonV
			if cancelReason == "" {
				cancelReason = contract.ReasonSuperseded
			}
			// Close admission FIRST, then ask. ADR 0029 §7 step 2's
			// ordering -- revoke the ability to create, then drain --
			// applied to attempts: otherwise the runtime keeps issuing new
			// actions throughout the grace period, and the drain chases a
			// set the holder is still adding to.
			r.Boundary.CloseAdmission(id)
			grace := r.CancelGrace
			if grace == 0 {
				grace = 2 * time.Second
			}
			_, _, _ = w.Send(id, inv.Bindings.Epoch, contract.TypeCancel, contract.Cancel{
				Reason:     string(cancelReason),
				DeadlineMS: grace.Milliseconds(),
			})
			graceExpiry = time.After(grace)
			if r.OnCancelRequested != nil {
				r.OnCancelRequested()
			}
		}
	}

	forcedTerminal := func(res contract.TerminalResult) Outcome {
		out.Forced = true

		// ADR 0030 §5 before ADR 0029 §7: admitted ACTIONS are settled before
		// the resource domain is fenced. Fencing an Incubator says nothing
		// about an in-flight data-plane write or forge push, which land outside
		// every resource domain -- so a domain receipt alone would report
		// `terminated` while a mutation was still committing.
		drained, outstanding := r.Boundary.DrainActions(id, drainWindow)
		out.ActionsDrained = drained
		out.OutstandingActions = len(outstanding)
		out.CommittedDuringDrain = len(r.Boundary.CommittedDuringDrain(id))

		r.fenceAll(inv, &out)
		if !drained {
			// An undrained action means no positive receipt, whatever the
			// domain reported. Reporting success anyway is best-effort fencing
			// through a different door.
			out.FenceReceipt = FenceUnconfirmed
			out.Events = append(out.Events, fmt.Sprintf(
				"drain incomplete: %d action(s) unsettled", len(outstanding)))
		}
		out.Result = res
		observedTerm = &res
		return r.finish(out)
	}

	for {
		select {
		case <-deadline:
			// A deadline is an ORCHESTRATOR-observed fact, which is why
			// timed_out is a status rather than a failure class (§5). It is a
			// forced stop, so it owes a positive receipt exactly as
			// cancellation does.
			r.Boundary.CloseAdmission(id)
			return forcedTerminal(contract.TimedOut("wall-clock budget exhausted"))

		case <-cancelSignal:
			cancelSignal = nil
			requestCancel()

		case <-cancelAt:
			cancelAt = nil
			requestCancel()

		case <-graceExpiry:
			// The grace period expired, so the domain is fenced. This is the
			// only thing that actually stops a process -- revoking
			// authorization does not, because a running process needs none.
			if blockedStop {
				return forcedTerminal(contract.Blocked(
					r.Boundary.BlockedRequirements(id),
					"a required decision has no responder; execution stopped and fenced"))
			}
			return forcedTerminal(contract.Cancelled(cancelReason, "cancelled; fenced after grace period"))

		case res := <-actionDone:
			_, _, _ = w.Send(id, inv.Bindings.Epoch, contract.TypeActionResult, res)

			if res.Outcome == contract.OutcomeBlocked && !blockedStop {
				// The Orchestrator knows the Story is blocked NOW. Waiting for
				// the adapter to close its own stream would leave a
				// non-cooperative runtime free to keep making model calls and
				// doing unmediated in-resource work under a Story that is
				// already terminal. So the stop is driven from here, on the
				// same cooperative-then-fenced path as any other forced stop.
				blockedStop = true
				r.Boundary.CloseAdmission(id)
				_, _, _ = w.Send(id, inv.Bindings.Epoch, contract.TypeCancel, contract.Cancel{
					Reason:     "blocked",
					DeadlineMS: drainWindow.Milliseconds(),
				})
				graceExpiry = time.After(drainWindow)
			}

		case it, ok := <-items:
			if !ok {
				return r.onStreamEnd(inv, &out, observedTerm, started)
			}
			if it.err != nil {
				if errors.Is(it.err, io.EOF) {
					return r.onStreamEnd(inv, &out, observedTerm, started)
				}
				// A protocol violation and a broken transport are both the
				// ORCHESTRATOR ending the execution, so both take the forced
				// path: admission closes, actions drain, the domain is fenced.
				// A first version returned them straight to finish, so a
				// runtime killed for speaking nonsense left its admitted
				// actions unsettled and its resource unfenced.
				var perr *contract.ErrProtocol
				if errors.As(it.err, &perr) {
					out.Events = append(out.Events, "protocol-violation: "+perr.Detail)
					r.Boundary.CloseAdmission(id)
					return forcedTerminal(contract.Failed(contract.ClassNonRetryableAgent, perr.Error()))
				}
				r.Boundary.CloseAdmission(id)
				return forcedTerminal(contract.Failed(contract.ClassRetryableInfrastructure, it.err.Error()))
			}

			// §8: several protocol violations are specified as fatal, so the
			// envelope is VALIDATED before anything acts on it. A first version
			// checked only the sequence number and silently ignored the rest,
			// which made the specification's "fatal" list decorative.
			if verr := r.validateEnvelope(inv, negotiated, it.env); verr != nil {
				out.Events = append(out.Events, "protocol-violation: "+verr.Error())
				r.Boundary.CloseAdmission(id)
				return forcedTerminal(contract.Failed(contract.ClassNonRetryableAgent, verr.Error()))
			}

			// Event identity, against the DURABLE record of what was committed --
			// not the watermark alone. An event committed BEYOND a gap sits
			// above the watermark, so a watermark-only check would accept its
			// replay and apply it twice, which is exactly the double-count the
			// identity exists to prevent.
			if r.Boundary.Recorder.Committed(id, it.env.Epoch, it.env.Seq, it.env.Stream) {
				out.DuplicateEvents++
				// A duplicate is still acknowledged: the sender must be able to
				// release it, and re-acknowledging a committed event is exactly
				// what makes a lost ack recoverable.
				_, _, _ = w.Send(id, inv.Bindings.Epoch, contract.TypeAck,
					contract.Ack{Epoch: it.env.Epoch, Stream: it.env.Stream, Through: it.env.Seq})
				continue
			}

			done := r.handleEvent(inv, w, it.env, &out, actionDone)

			// Advance and acknowledge only AFTER the event's effect is recorded,
			// so an acknowledgement never promises more than was committed.
			r.Boundary.Recorder.Advance(id, it.env.Epoch, it.env.Seq, it.env.Stream)
			_, _, _ = w.Send(id, inv.Bindings.Epoch, contract.TypeAck,
				contract.Ack{Epoch: it.env.Epoch, Stream: it.env.Stream, Through: it.env.Seq})

			if done {
				// The runtime claimed a terminal result, and a claim is all it
				// is (§4). Where the Orchestrator observed something the claim
				// contradicts, the observation wins and the claim is RETAINED
				// rather than discarded.
				claim := out.Claimed
				switch {
				case r.Boundary.IsWaiting(id) && claim != nil:
					// A terminal result while the execution awaits a resolution
					// is an invariant violation, not an outcome: something let a
					// waiting caller keep working (ADR 0030 §4).
					r.Boundary.CloseAdmission(id)
					return forcedTerminal(contract.Failed(contract.ClassNonRetryableAgent,
						"runtime claimed "+string(claim.Status)+" while the execution awaited a resolution"))
				case out.TerminalViolation != "":
					r.Boundary.CloseAdmission(id)
					return forcedTerminal(contract.Failed(
						contract.ClassNonRetryableAgent, out.TerminalViolation))
				case blockedStop:
					// The Story is already blocked. Whatever the runtime claims,
					// the Orchestrator's own state decides -- and it still owes
					// the drain and the fence.
					out.Overridden = claim != nil
					return forcedTerminal(contract.Blocked(
						r.Boundary.BlockedRequirements(id),
						"a required decision has no responder; execution stopped and fenced"))
				case cancelRequested && claim != nil && claim.Status != contract.StatusCancelled:
					out.Overridden = true
					return forcedTerminal(contract.Cancelled(cancelReason,
						"runtime claimed "+string(claim.Status)+" after cancellation was requested"))
				case claim != nil && claim.Status == contract.StatusCancelled:
					// A cooperative cancellation still fences before the
					// terminal result is recorded (§6 step 4).
					return forcedTerminal(*claim)
				case claim != nil:
					// Even an ordinary completion owes the drain: a runtime can
					// report `completed` while an action it issued is still
					// committing, and recording that would be the same false
					// record a forced stop is careful to avoid.
					drained, outstanding := r.Boundary.DrainActions(id, drainWindow)
					out.ActionsDrained = drained
					out.OutstandingActions = len(outstanding)
					if !drained {
						out.Overridden = true
						r.Boundary.CloseAdmission(id)
						return forcedTerminal(contract.Failed(contract.ClassRetryableInfrastructure,
							fmt.Sprintf("runtime claimed %s with %d action(s) unsettled",
								claim.Status, len(outstanding))))
					}
					out.Result = *claim
				}
				if out.IdentityError != "" {
					out.Events = append(out.Events, "identity: "+out.IdentityError)
					r.Boundary.CloseAdmission(id)
					return forcedTerminal(contract.Failed(contract.ClassNonRetryableAgent,
						"runtime identity: "+out.IdentityError))
				}
				return r.finish(out)
			}
		}
	}
}

// onStreamEnd handles the transport closing without a terminal event.
func (r *Runtime) onStreamEnd(inv *contract.Invocation, out *Outcome, observed *contract.TerminalResult, started time.Time) Outcome {
	id := inv.ID()
	if observed != nil {
		out.Result = *observed
		return r.finish(*out)
	}
	if out.Claimed != nil {
		out.Result = *out.Claimed
		return r.finish(*out)
	}

	// Reconciliation: only `open` attempts settle `unknown`; declared waits are
	// settled `stale` with their requirement or operation preserved (§6).
	out.Reconciled = r.Boundary.Recorder.Reconcile()
	out.Events = append(out.Events, fmt.Sprintf(
		"reconciled: %d unknown, %d stale waits, %d invalid",
		len(out.Reconciled.SettledUnknown), len(out.Reconciled.StaleWaits),
		len(out.Reconciled.Invalid)))

	// An execution awaiting another principal's answer is not over: the
	// INCARNATION ended, and only ending the EXECUTION fences. The actions are
	// still drained -- nothing should be in flight -- but the resource stays
	// with the Story and no terminal result is recorded.
	if w, waiting := r.Boundary.Recorder.AwaitingResponse(id); waiting {
		drained, outstanding := r.Boundary.DrainActions(id, 2*time.Second)
		out.ActionsDrained = drained
		out.OutstandingActions = len(outstanding)
		out.AwaitingResponse = w.QuestionArtifact
		out.Events = append(out.Events, "incarnation ended; awaiting an answer to "+w.QuestionArtifact)
		return *out
	}

	if r.Boundary.IsBlocked(id) {
		// §5's exception: `blocked` is composed by the ORCHESTRATOR from the
		// boundary's own state, because a gate the agent cannot see is not the
		// agent's to report. This is the one execution whose terminal result
		// arrives without a terminal event.
		//
		// It is still a FORCED stop and owes the same discipline: the adapter
		// exiting is not evidence that the admitted actions settled or that the
		// resource domain is fenced.
		out.Forced = true
		window := r.CancelGrace
		if window == 0 {
			window = 2 * time.Second
		}
		drained, outstanding := r.Boundary.DrainActions(id, window)
		out.ActionsDrained = drained
		out.OutstandingActions = len(outstanding)
		r.fenceAll(inv, out)
		if !drained {
			out.FenceReceipt = FenceUnconfirmed
		}
		out.Result = contract.Blocked(r.Boundary.BlockedRequirements(id),
			"a required decision has no responder in this configuration")
		return r.finish(*out)
	}
	// A transport that closed without a terminal result is the Orchestrator
	// ending the execution, so it owes the same discipline as any other forced
	// stop. A first version recorded the failure directly, leaving admitted
	// actions unsettled and the resource unfenced.
	out.Forced = true
	r.Boundary.CloseAdmission(id)
	window := r.CancelGrace
	if window == 0 {
		window = 2 * time.Second
	}
	drained, outstanding := r.Boundary.DrainActions(id, window)
	out.ActionsDrained = drained
	out.OutstandingActions = len(outstanding)
	r.fenceAll(inv, out)
	if !drained {
		out.FenceReceipt = FenceUnconfirmed
	}
	out.Result = contract.Failed(contract.ClassRetryableInfrastructure,
		fmt.Sprintf("adapter closed the transport after %s with no terminal result",
			time.Since(started).Round(time.Millisecond)))
	return r.finish(*out)
}

// handleEvent processes one agent-to-host message. It returns true when the
// runtime has claimed a terminal result.
func (r *Runtime) handleEvent(
	inv *contract.Invocation, w *contract.Writer, env contract.Envelope, out *Outcome,
	actionDone chan<- contract.ActionResult,
) bool {
	id := inv.ID()
	out.Events = append(out.Events, env.Type)

	switch env.Type {
	case contract.TypeStarted:
		// Identity is CHECKED, not just carried. A `started` that arrives late,
		// twice, or disagreeing with the handshake is a runtime whose identity
		// provenance (§9) would be a self-report nobody compared to anything.
		st, _ := contract.Decode[contract.Started](env)
		if out.StartedSeen {
			out.IdentityError = "a second `started` was sent"
		} else if len(out.Events) > 1 {
			// Events records this message too, so >1 means something preceded it.
			out.IdentityError = "`started` was not the first message"
		} else if st.Adapter != out.Handshake.Adapter ||
			st.ExecutableVer != out.Handshake.Executable {
			out.IdentityError = fmt.Sprintf(
				"`started` claims %s@%s, the handshake claimed %s@%s",
				st.Adapter, st.ExecutableVer, out.Handshake.Adapter, out.Handshake.Executable)
		} else if st.ContractVersion != out.Handshake.Selected {
			out.IdentityError = fmt.Sprintf(
				"`started` claims contract %s, the handshake selected %s",
				st.ContractVersion, out.Handshake.Selected)
		}
		out.StartedSeen = true
		return false

	case contract.TypeHeartbeat, contract.TypeActivity, contract.TypeWarning:
		if !out.StartedSeen {
			out.IdentityError = env.Type + " arrived before `started`"
		}
		return false

	case contract.TypeUsage:
		if u, err := contract.Decode[contract.Usage](env); err == nil {
			out.Usage = append(out.Usage, u)
		}
		return false

	case contract.TypeProvenance:
		if p, err := contract.Decode[contract.Provenance](env); err == nil {
			out.Provenance = append(out.Provenance, p)
		}
		return false

	case contract.TypeAttach:
		// The ORCHESTRATOR enumerates. A restarted runtime cannot be relied on
		// to reproduce correlations it invented, so it asks rather than tells.
		ack := contract.AttachAck{}
		for _, a := range r.Boundary.Recorder.ForInvocation(id) {
			state := contract.AttachOutstanding
			if a.State == StateSettled {
				state = contract.AttachSettled
				ack.Settled = append(ack.Settled, contract.ActionResult{
					Correlation: a.Correlation, AttemptID: a.ID,
					Outcome: a.Outcome, Reason: a.Reason, Requirements: a.Requirements,
				})
			}
			// Deliberately coarse: `outstanding` regardless of WHICH wait. The
			// runtime must not learn that a human is being asked.
			ack.Actions = append(ack.Actions, contract.OutstandingAction{
				Correlation: a.Correlation, AttemptID: a.ID, Action: a.Action, State: state,
			})
		}
		_, _, _ = w.Send(id, inv.Bindings.Epoch, contract.TypeAttachAck, ack)
		return false

	case contract.TypeActionRequest:
		req, err := contract.Decode[contract.ActionRequest](env)
		if err != nil {
			return false
		}
		// Submit registers the intent SYNCHRONOUSLY and runs the gates off the
		// event loop. The synchronous half matters for delivery: the caller
		// acknowledges this event once handleEvent returns, and acknowledging
		// before the intent was durable would release a request the
		// Orchestrator had not yet recorded.
		if _, serr := r.Boundary.Submit(inv, req, actionDone); serr != nil {
			actionDone <- contract.ActionResult{Correlation: req.Correlation,
				Outcome: contract.OutcomeDenied, Reason: serr.Error()}
		}
		return false

	case contract.TypeTerminal:
		res, err := contract.Decode[contract.TerminalResult](env)
		if err != nil {
			// A malformed terminal is a PROTOCOL violation, not a claim. A first
			// version turned it into an ordinary claim, which then took the
			// non-forced path and skipped the drain and the fence.
			out.TerminalViolation = "terminal event is malformed"
			return true
		}
		if verr := res.Validate(); verr != nil {
			out.TerminalViolation = "terminal result invalid: " + verr.Error()
			return true
		}
		out.Claimed = &res
		return true

	default:
		return false
	}
}

// knownAgentTypes is the closed set of agent-to-host message types. An unknown
// type is a protocol violation, not something to ignore: silently dropping it
// means a runtime speaking a contract this build does not implement looks
// healthy right up to the point its work is lost.
//
// replayableFromPriorEpoch reports whether a message type may legitimately
// arrive under an epoch older than the active binding. Only the reports §4
// gives a retention and replay obligation may: everything else is an ACT, and
// an act from a superseded incarnation is a stale generation reaching through
// the boundary.
func replayableFromPriorEpoch(msgType string) bool {
	return msgType == contract.TypeUsage || msgType == contract.TypeProvenance
}

//nolint:gochecknoglobals // a fixed vocabulary
var knownAgentTypes = map[string]bool{
	contract.TypeStarted: true, contract.TypeHeartbeat: true, contract.TypeActivity: true,
	contract.TypeActionRequest: true, contract.TypeUsage: true, contract.TypeProvenance: true,
	contract.TypeWarning: true, contract.TypeTerminal: true, contract.TypeAttach: true,
}

// validateEnvelope enforces the framing invariants §8 calls fatal.
func (r *Runtime) validateEnvelope(inv *contract.Invocation, negotiated string, env contract.Envelope) error {
	if env.V != negotiated {
		return fmt.Errorf("envelope version %q is not the negotiated %q", env.V, negotiated)
	}
	if env.Inv != inv.ID() {
		return fmt.Errorf("envelope names execution %q, not %q", env.Inv, inv.ID())
	}
	// An epoch from the FUTURE is always a violation.
	if env.Epoch > inv.Bindings.Epoch {
		return fmt.Errorf("envelope carries epoch %d, ahead of the active %d",
			env.Epoch, inv.Bindings.Epoch)
	}
	// An OLDER epoch is admissible only for the types that carry a replay
	// obligation (§4). A first version admitted every type, which let a fenced
	// incarnation send an `action_request` or a `terminal` that would then be
	// handled against the CURRENT bindings -- a stale generation acting through
	// the boundary, which ADR 0029 §7 requirement 5 exists to prevent.
	if env.Epoch < inv.Bindings.Epoch && !replayableFromPriorEpoch(env.Type) {
		return fmt.Errorf("%s carries epoch %d, and only replayable reports may arrive from a prior epoch",
			env.Type, env.Epoch)
	}
	if !knownAgentTypes[env.Type] {
		return fmt.Errorf("unknown message type %q", env.Type)
	}
	// The stream is caller-supplied and must not be trusted: a `usage` claiming
	// `best_effort` would opt itself out of the retention and replay obligation
	// its own type carries, and out of the deduplication that protects it.
	if want := contract.StreamFor(env.Type); env.Stream != want {
		return fmt.Errorf("%s claims stream %q, but its type belongs to %q",
			env.Type, env.Stream, want)
	}
	if err := validateBody(env); err != nil {
		return err
	}
	return nil
}

// validateBody rejects a malformed body for a KNOWN type. Decoding it and
// shrugging on error -- which a first version did throughout handleEvent -- turns
// a corrupt usage report into a silently dropped one.
func validateBody(env contract.Envelope) error {
	var err error
	switch env.Type {
	case contract.TypeStarted:
		_, err = contract.Decode[contract.Started](env)
	case contract.TypeHeartbeat:
		_, err = contract.Decode[contract.Heartbeat](env)
	case contract.TypeActivity:
		_, err = contract.Decode[contract.Activity](env)
	case contract.TypeWarning:
		_, err = contract.Decode[contract.Warning](env)
	case contract.TypeActionRequest:
		var req contract.ActionRequest
		if req, err = contract.Decode[contract.ActionRequest](env); err == nil && req.Correlation == "" {
			return errors.New("action_request carries no correlation")
		}
	case contract.TypeUsage:
		var u contract.Usage
		if u, err = contract.Decode[contract.Usage](env); err == nil && u.CallRef == "" {
			return errors.New("usage carries no call reference, so it joins to nothing")
		}
	case contract.TypeProvenance:
		var p contract.Provenance
		if p, err = contract.Decode[contract.Provenance](env); err == nil {
			if p.CallRef == "" {
				return errors.New("provenance carries no call reference")
			}
			if p.Closure == contract.ClosureClosed && len(p.Bindings) == 0 {
				return errors.New("provenance claims closed with no bindings, which is not provenance")
			}
		}
	case contract.TypeAttach:
		_, err = contract.Decode[contract.Attach](env)
	case contract.TypeTerminal:
		// Validated in handleEvent, where an invalid RESULT (as opposed to an
		// unparseable body) becomes the terminal outcome rather than a framing
		// error.
		return nil
	}
	return err
}

func (r *Runtime) fenceAll(inv *contract.Invocation, out *Outcome) {
	if r.Fencer == nil {
		out.FenceReceipt = FenceTerminated
		return
	}
	worst := FenceTerminated
	for _, res := range inv.Bindings.Resources {
		got := r.Fencer(res)
		if got == FenceUnconfirmed {
			worst = FenceUnconfirmed
		} else if got == FenceIsolated && worst != FenceUnconfirmed {
			worst = FenceIsolated
		}
	}
	out.FenceReceipt = worst
}

// finish applies the rule that must hold on every FORCED path: a terminal
// result is recorded only after a positive fence receipt (§6 step 4).
//
// A first version applied it to `cancelled` alone, so a timeout recorded
// `timed_out` even when fencing came back unconfirmed -- the same false record,
// reached by a different route. The discipline belongs to the CATEGORY (the
// Orchestrator forced the stop), not to one status.
func (r *Runtime) finish(out Outcome) Outcome {
	if out.Forced && !out.FenceReceipt.Positive() {
		out.Events = append(out.Events, "terminal withheld: fence unconfirmed")
		out.Result = contract.TerminalResult{}
		out.DispatchErr = errors.New(
			"fencing unconfirmed; execution remains non-terminal and the resource is quarantined")
	}
	return out
}
