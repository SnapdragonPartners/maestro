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

	Events     []string
	Usage      []contract.Usage
	Provenance []contract.Provenance
	Handshake  contract.HelloAck
	Reconciled ReconcileReport
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
		_ = cmd.Wait()
	}()

	// ---- handshake (§11) ----
	supported := r.SupportedVersions
	if len(supported) == 0 {
		supported = []string{contract.Version}
	}
	if err := w.Send("", 0, contract.TypeHello, contract.Hello{Supported: supported}); err != nil {
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
	if err := w.Send(id, inv.Bindings.Epoch, contract.TypeInvoke, inv); err != nil {
		out.DispatchErr = fmt.Errorf("send invoke: %w", err)
		return out
	}

	started := time.Now()
	var (
		cancelRequested bool
		cancelReason    contract.CancellationReason
		observedTerm    *contract.TerminalResult
	)

	// Event identity (§4): (invocation, epoch, seq). A repeat is dropped,
	// because at-least-once delivery cannot be idempotent without a checked
	// identity -- and a sequence number alone restarts at 1 with every
	// incarnation, so two different messages would share one identity.
	seen := map[uint64]uint64{}

	var deadline <-chan time.Time
	if inv.Config.Budgets.MaxWallClockMS > 0 {
		deadline = time.After(time.Duration(inv.Config.Budgets.MaxWallClockMS) * time.Millisecond)
	}
	var cancelAt <-chan time.Time
	if r.CancelAfter > 0 {
		cancelAt = time.After(r.CancelAfter)
	}

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

	forcedTerminal := func(res contract.TerminalResult) Outcome {
		out.Forced = true
		r.fenceAll(inv, &out)
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

		case <-cancelAt:
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
				_ = w.Send(id, inv.Bindings.Epoch, contract.TypeCancel, contract.Cancel{
					Reason:     string(cancelReason),
					DeadlineMS: grace.Milliseconds(),
				})
				graceExpiry = time.After(grace)
				if r.OnCancelRequested != nil {
					r.OnCancelRequested()
				}
			}

		case <-graceExpiry:
			// The grace period expired, so the domain is fenced. This is the
			// only thing that actually stops a process -- revoking
			// authorization does not, because a running process needs none.
			return forcedTerminal(contract.Cancelled(cancelReason, "cancelled; fenced after grace period"))

		case res := <-actionDone:
			_ = w.Send(id, inv.Bindings.Epoch, contract.TypeActionResult, res)

		case it, ok := <-items:
			if !ok {
				return r.onStreamEnd(inv, &out, observedTerm, started)
			}
			if it.err != nil {
				if errors.Is(it.err, io.EOF) {
					return r.onStreamEnd(inv, &out, observedTerm, started)
				}
				var perr *contract.ErrProtocol
				if errors.As(it.err, &perr) {
					// §8: a protocol violation is FATAL, unlike a policy denial.
					out.Result = contract.Failed(contract.ClassNonRetryableAgent, perr.Error())
					out.Events = append(out.Events, "protocol-violation: "+perr.Detail)
					return r.finish(out)
				}
				out.Result = contract.Failed(contract.ClassRetryableInfrastructure, it.err.Error())
				return r.finish(out)
			}

			// Event identity, checked before anything acts on the message.
			if last, ok := seen[it.env.Epoch]; ok && it.env.Seq <= last {
				out.DuplicateEvents++
				continue
			}
			seen[it.env.Epoch] = it.env.Seq

			if done := r.handleEvent(inv, w, it.env, &out, actionDone); done {
				// The runtime claimed a terminal result, and a claim is all it
				// is (§4). Where the Orchestrator observed something the claim
				// contradicts, the observation wins and the claim is RETAINED
				// rather than discarded.
				claim := out.Claimed
				switch {
				case cancelRequested && claim != nil && claim.Status != contract.StatusCancelled:
					out.Overridden = true
					return forcedTerminal(contract.Cancelled(cancelReason,
						"runtime claimed "+string(claim.Status)+" after cancellation was requested"))
				case claim != nil && claim.Status == contract.StatusCancelled:
					// A cooperative cancellation still fences before the
					// terminal result is recorded (§6 step 4).
					return forcedTerminal(*claim)
				case claim != nil:
					out.Result = *claim
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
	// preserved, validated, and handed back for restoration (§6).
	out.Reconciled = r.Boundary.Recorder.Reconcile()
	out.Events = append(out.Events, fmt.Sprintf(
		"reconciled: %d unknown, %d operator-waits preserved, %d resource-waits to restore, %d invalid",
		len(out.Reconciled.SettledUnknown), len(out.Reconciled.OperatorWaitsPreserved),
		len(out.Reconciled.ResourceWaitsToRestore), len(out.Reconciled.Invalid)))

	if r.Boundary.IsBlocked(id) {
		// §5's exception: `blocked` is composed by the ORCHESTRATOR from the
		// boundary's own state, because a gate the agent cannot see is not the
		// agent's to report. This is the one execution whose terminal result
		// arrives without a terminal event.
		reqs := r.Boundary.BlockedRequirements(id)
		out.Result = contract.Blocked(reqs, "a required decision has no responder in this configuration")
		return r.finish(*out)
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
	case contract.TypeStarted, contract.TypeHeartbeat, contract.TypeActivity, contract.TypeWarning:
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
					Outcome: a.Outcome, Reason: a.Reason, Requirement: a.Requirement,
				})
			}
			// Deliberately coarse: `outstanding` regardless of WHICH wait. The
			// runtime must not learn that a human is being asked.
			ack.Actions = append(ack.Actions, contract.OutstandingAction{
				Correlation: a.Correlation, AttemptID: a.ID, Action: a.Action, State: state,
			})
		}
		_ = w.Send(id, inv.Bindings.Epoch, contract.TypeAttachAck, ack)
		return false

	case contract.TypeActionRequest:
		req, err := contract.Decode[contract.ActionRequest](env)
		if err != nil {
			return false
		}
		// Off the event loop. The result comes back on actionDone, so a gate-2
		// or gate-3 wait cannot stop this loop from serving cancellation,
		// heartbeats, or re-attachment.
		r.Boundary.Submit(inv, req, actionDone)
		return false

	case contract.TypeTerminal:
		res, err := contract.Decode[contract.TerminalResult](env)
		if err != nil {
			bad := contract.Failed(contract.ClassNonRetryableAgent, "terminal event is malformed")
			out.Claimed = &bad
			return true
		}
		if verr := res.Validate(); verr != nil {
			// A terminal result violating §5's applicability rule is a protocol
			// violation, not a result.
			bad := contract.Failed(contract.ClassNonRetryableAgent, "terminal result invalid: "+verr.Error())
			out.Claimed = &bad
			return true
		}
		out.Claimed = &res
		return true

	default:
		return false
	}
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
