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
	"strings"
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

type agent struct {
	w    *contract.Writer
	inv  *contract.Invocation
	mode string

	results chan contract.ActionResult
	attach  chan contract.AttachAck
	cancel  chan contract.Cancel
	readErr chan error

	cancelled bool
}

func main() {
	mode := os.Getenv("SPIKE_MODE")
	if mode == "" {
		mode = "normal"
	}
	a := &agent{
		w:       contract.NewWriter(os.Stdout),
		mode:    mode,
		results: make(chan contract.ActionResult, 8),
		attach:  make(chan contract.AttachAck, 4),
		cancel:  make(chan contract.Cancel, 1),
		readErr: make(chan error, 1),
	}
	if err := a.run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", adapterName, err)
		os.Exit(1)
	}
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
	} else if !contains(hello.Supported, contract.Version) {
		return fmt.Errorf("no mutually supported version; offered %v", hello.Supported)
	}

	if err := a.w.Send("", contract.TypeHelloAck, contract.HelloAck{
		Selected: selected,
		// This runtime cannot resume its own session, so §7 holds it resident
		// while blocked rather than releasing it.
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

	// The invocation carries a resource REFERENCE and no path. There is
	// nothing here to open, which is the point: v1's RunOptions.WorkDir is
	// what this replaces.
	if _, ok := inv.Resource("incubator"); !ok {
		return fmt.Errorf("no incubator reference in invocation")
	}

	go a.readLoop(rd)

	if err := a.w.Send(inv.ID, contract.TypeStarted, contract.Started{
		Adapter:         adapterName,
		ExecutableVer:   executableVer,
		ContractVersion: contract.Version,
	}); err != nil {
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
		}
	}
}

// request issues one mediated action and waits for the boundary's answer.
//
// The correlation is DERIVED from the invocation and a step ordinal rather than
// random. A restarted runtime must be able to re-announce the correlations it
// had outstanding, and it cannot do that if it invented them and lost them.
func (a *agent) request(step int, action contract.ActionID, args any) (contract.ActionResult, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return contract.ActionResult{}, err
	}
	corr := fmt.Sprintf("%s#%d", a.inv.ID, step)
	if err := a.w.Send(a.inv.ID, contract.TypeActionRequest, contract.ActionRequest{
		Correlation: corr,
		Action:      action,
		Arguments:   raw,
	}); err != nil {
		return contract.ActionResult{}, err
	}
	select {
	case res := <-a.results:
		return res, nil
	case c := <-a.cancel:
		a.cancelled = true
		// Reaching a safe boundary means finishing the atomic action already in
		// flight, then issuing no further ones.
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

func (a *agent) checkCancel() bool {
	select {
	case <-a.cancel:
		a.cancelled = true
	default:
	}
	return a.cancelled
}

func (a *agent) terminal(res contract.TerminalResult) error {
	return a.w.Send(a.inv.ID, contract.TypeTerminal, res)
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

func (a *agent) work() error {
	inv := a.inv

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
			_ = a.w.Send(inv.ID, contract.TypeHeartbeat, contract.Heartbeat{Phase: "stuck"})
			time.Sleep(200 * time.Millisecond)
		}

	case "restart_reattach":
		// A restarted runtime re-announces its outstanding correlations rather
		// than reissuing the actions. This is what makes the semantics
		// at-most-once ACROSS a restart, and it works only because the
		// correlation is derivable (see request).
		return a.reattachThenFinish()
	}

	_ = a.w.Send(inv.ID, contract.TypeHeartbeat, contract.Heartbeat{Phase: "reading"})
	_ = a.w.Send(inv.ID, contract.TypeActivity, contract.Activity{Message: "reading the candidate diff"})

	// ---- step 1: read the diff through a mediated action ----
	diffRes, err := a.request(1, actionReadDiff, map[string]any{
		"resource": mustResource(inv, "incubator").ReferenceID,
	})
	if err != nil {
		return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
	}
	if diffRes.Outcome == "" {
		// The boundary recorded an intent and returned no outcome. The agent
		// must not re-execute blindly: ADR 0030 §3 sends this to
		// reconciliation, and blind retry is how an adapted runtime duplicates
		// a forge push.
		_ = a.w.Send(inv.ID, contract.TypeWarning, contract.Warning{
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
		_ = a.w.Send(inv.ID, contract.TypeActivity, contract.Activity{
			Message: "denied as expected; continuing",
		})
	}

	if a.checkCancel() {
		return a.terminalOnCancel()
	}

	// ---- the stub analysis ----
	findings := stubAnalyze(diff.Text)

	// An empty diff is a COMPLETED execution whose work was already done. This
	// is #280's disposition, decided from real data rather than a mode flag.
	if strings.TrimSpace(diff.Text) == "" {
		_ = a.emitProvenance(inv, 0)
		return a.terminal(contract.Completed(contract.DispositionAlreadySatisfied,
			"no candidate changes to review"))
	}

	// ---- step 2: publish findings as an artifact ----
	if a.mode == "escalate" {
		// An action whose gate requires an operator. Headless, this blocks the
		// Story immediately (ADR 0030 §4) rather than waiting for a timeout.
		res, err := a.request(2, actionForge, map[string]any{"ref": "refs/heads/candidate"})
		if err != nil {
			return a.terminal(contract.Failed(contract.ClassRetryableInfrastructure, err.Error()))
		}
		if res.Outcome == contract.OutcomeDenied && strings.HasPrefix(res.Reason, "blocked:") {
			// The caller performs no further LLM turns and issues no further
			// actions. It stops; the Orchestrator records the blocked result,
			// because the Story's state is not the agent's to declare.
			_ = a.w.Send(inv.ID, contract.TypeActivity, contract.Activity{
				Message: "a decision is required; stopping without further turns",
			})
			os.Exit(4)
		}
	}

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
			_ = a.w.Send(inv.ID, contract.TypeHeartbeat, contract.Heartbeat{Phase: "ignoring cancellation"})
			time.Sleep(200 * time.Millisecond)
		}
	}

	if a.checkCancel() {
		return a.terminalOnCancel()
	}

	_ = a.emitProvenance(inv, len(findings))

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

	return a.terminal(contract.Completed(contract.DispositionChanged,
		fmt.Sprintf("%d finding(s) from the %s backend", len(findings), stubBackendTag)))
}

// reattachThenFinish models a runtime restarted into an existing execution.
func (a *agent) reattachThenFinish() error {
	inv := a.inv
	corr := fmt.Sprintf("%s#%d", inv.ID, 1)
	if err := a.w.Send(inv.ID, contract.TypeAttach, contract.Attach{Correlations: []string{corr}}); err != nil {
		return err
	}
	select {
	case ack := <-a.attach:
		state := ack.States[corr]
		_ = a.w.Send(inv.ID, contract.TypeActivity, contract.Activity{
			Message: "re-attached: " + corr + " is " + string(state),
		})
		if state == contract.AttachSettled {
			// Already done. Reissuing would be a second action.
			return a.terminal(contract.Completed(contract.DispositionChanged,
				"resumed; step 1 was already settled and was not reissued"))
		}
		return a.terminal(contract.Completed(contract.DispositionAlreadySatisfied,
			"resumed; nothing outstanding"))
	case err := <-a.readErr:
		return err
	case <-time.After(10 * time.Second):
		return fmt.Errorf("no attach_ack")
	}
}

// emitProvenance discharges ADR 0031 §3's obligations 2 and 3: the bindings
// first, and then a status drawn FROM them.
//
// The stub makes no model calls, so there is no per-call provenance to emit and
// this reports the INVOCATION's bindings once. The conformance report declares
// the per-model-call axes uncovered rather than inventing them.
func (a *agent) emitProvenance(inv *contract.Invocation, findings int) error {
	bindings := []contract.SourceBinding{
		{Source: "P", Ref: inv.Pack.ContentRef, Digest: inv.Pack.Scheme + ":" + inv.Pack.Digest},
		{Source: "H", Ref: adapterName + "@" + executableVer},
	}
	for _, s := range inv.Seeding {
		bindings = append(bindings, contract.SourceBinding{Source: "seeding", Ref: s.ArtifactID, Digest: s.Digest})
	}
	_ = findings
	return a.w.Send(inv.ID, contract.TypeProvenance, contract.Provenance{
		CallRef:  "no-model-call",
		Bindings: bindings,
		// The stub assembles no turn material of its own, so everything that
		// shaped its behaviour is bound. A runtime that assembled context
		// internally could not say this -- which is ADR 0031 §3's point that
		// unclosed is a property of the ADAPTER, not of being external.
		Closure: contract.ClosureClosed,
	})
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

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
