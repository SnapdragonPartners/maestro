// Command executioncontract is A4's conformance runner for ADR 0032.
//
// It builds the reviewagent executable and drives it as a REAL external process
// over the real local transport, once per scenario. An in-process fake or an
// echo fixture does not discharge A4 (blocker plan, settled question 3), so
// every wire scenario below spawns a process and speaks newline-delimited JSON
// to it.
//
//	go run .            # every claim
//	go run . -v         # with the event stream per scenario
//	go run . -run NAME  # one claim
//
// Exit code is 0 only when every claim is PROVEN. The three-valued outcome is
// deliberate and follows the Docker fencing spike: an inconclusive run must
// never read as a passing one.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"maestro-spike/phase3/executioncontract/contract"
	"maestro-spike/phase3/executioncontract/host"
)

// Outcome is three-valued for the same reason the fencing spike's is: a claim
// that could not be evaluated is not a claim that failed.
type Outcome string

const (
	Proven    Outcome = "PROVEN"
	Falsified Outcome = "FALSIFIED"
	Errored   Outcome = "ERROR"
)

type claim struct {
	name    string
	about   string // the ADR section it evidences
	outcome Outcome
	detail  string
}

type runner struct {
	binary  string
	verbose bool
	claims  []claim
}

func main() {
	var (
		verbose = flag.Bool("v", false, "print the event stream for each scenario")
		only    = flag.String("run", "", "run only claims whose name contains this substring")
	)
	flag.Parse()

	bin, cleanup, err := buildAgent()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot build the agent: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	r := &runner{binary: bin, verbose: *verbose}
	for _, sc := range scenarios() {
		if *only != "" && !strings.Contains(sc.name, *only) {
			continue
		}
		r.run(sc)
	}
	for _, bc := range boundaryClaims() {
		if *only != "" && !strings.Contains(bc.name, *only) {
			continue
		}
		r.runBoundary(bc)
	}

	os.Exit(r.report())
}

func buildAgent() (string, func(), error) {
	dir, err := os.MkdirTemp("", "execcontract-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	bin := filepath.Join(dir, "reviewagent")
	cmd := exec.Command("go", "build", "-o", bin, "./reviewagent")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return bin, cleanup, nil
}

// ---------- fixtures ----------

const sampleDiff = "--- a/main.go\n+++ b/main.go\n+// TODO: wire this up\n+func added() {}\n"

func invocation(id string, caps []contract.ActionID, responder bool) *contract.Invocation {
	return &contract.Invocation{
		ID:        id,
		Principal: contract.PrincipalRef{InstanceID: "pi-" + id, Kind: "agent"},
		Role:      "reviewer",
		Work:      contract.WorkRef{StoryID: "story-1", EpicID: "epic-1", EffectiveVersion: 7},
		Seeding: []contract.ArtifactRef{
			{ArtifactID: "art-spec-1", Digest: "sha256:aaaa"},
		},
		Model: contract.ModelBinding{
			// Explicit, never inferred from the name (§3). This is exactly the
			// case v1's ProviderPatterns gets wrong: an OpenAI-lineage model
			// whose name begins "gpt" but which runs on a local Ollama.
			Route: contract.ModelRoute{
				Provider:          "ollama",
				Endpoint:          "http://localhost:11434",
				ProviderModelName: "gpt-oss:20b",
			},
			Served: contract.ServedModelID{Provider: "ollama", ProviderModelName: "gpt-oss:20b"},
		},
		Pack: contract.PackRef{
			Name: "default", Scheme: "pack-jcs-sha256-v1", Digest: "beef",
			ContentRef: "pack-content-1", InstallationID: "inst-1", InstallationRevision: 3,
		},
		Capabilities: caps,
		Resources: []contract.ResourceRef{
			{Kind: "incubator", ReferenceID: "inc-1", InstanceGeneration: 4},
		},
		Budgets:           contract.Budgets{MaxWallClockMS: 20000},
		OperatorResponder: responder,
		ExpectedSources:   []string{"P", "H", "seeding"},
	}
}

var (
	capRead    = contract.ActionID{Kind: "repo", Verb: "read_diff"}
	capPublish = contract.ActionID{Kind: "artifact", Verb: "publish"}
	capForge   = contract.ActionID{Kind: "forge", Verb: "push"}
)

func defaultCaps() []contract.ActionID {
	return []contract.ActionID{capRead, capPublish, capForge}
}

type harness struct {
	boundary  *host.Boundary
	recorder  *host.Recorder
	published []json.RawMessage
}

func newHarness(diff string) *harness {
	rec := host.NewRecorder()
	b := host.NewBoundary(rec)
	h := &harness{boundary: b, recorder: rec}

	b.Executors[capRead.String()] = func(_ *contract.Invocation, _ json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]string{"text": diff})
	}
	b.Executors[capPublish.String()] = func(_ *contract.Invocation, args json.RawMessage) (json.RawMessage, error) {
		h.published = append(h.published, args)
		return json.Marshal(map[string]string{"artifact_id": "art-findings-1"})
	}
	b.Executors[capForge.String()] = func(_ *contract.Invocation, _ json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]string{"ref": "pushed"})
	}
	b.CurrentGeneration["inc-1"] = 4
	b.EffectiveVersion = 7
	return h
}

// requiresOperatorFor is the shape candidate 12's human gates will have: it
// returns *requires an operator* with a structured requirement, and declares
// nothing else.
func requiresOperatorFor(action contract.ActionID) host.PolicyHook {
	return func(_ *contract.Invocation, a contract.ActionID, _ json.RawMessage) (host.Decision, *contract.RequirementRef) {
		if a == action {
			return host.DecisionRequiresOperator, &contract.RequirementRef{
				GateID:    "spike.forge-push",
				Statement: "pushing a ref requires an operator decision",
			}
		}
		return host.DecisionAllow, nil
	}
}

// ---------- wire scenarios ----------

type scenario struct {
	name  string
	about string
	mode  string
	// setup mutates the harness and runtime before the process is spawned.
	setup func(h *harness, rt *host.Runtime, inv *contract.Invocation)
	check func(h *harness, out host.Outcome) (Outcome, string)
	diff  string
}

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
				return Proven, "adapter selected " + out.Handshake.Selected +
					", resumable=" + fmt.Sprint(out.Handshake.Resumable)
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
			about: "§5 axis 1+2, §8 mediated actions",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if out.Result.Disposition == nil || *out.Result.Disposition != contract.DispositionChanged {
					return Falsified, "disposition not changed"
				}
				if len(h.published) != 1 {
					return Falsified, fmt.Sprintf("%d artifacts published", len(h.published))
				}
				for _, a := range h.recorder.Attempts() {
					if a.State != host.StateSettled {
						return Falsified, "attempt " + a.ID + " left in " + string(a.State)
					}
				}
				return Proven, fmt.Sprintf("completed/changed, %d attempts all settled",
					len(h.recorder.Attempts()))
			},
		},
		{
			name:  "result/completed-already-satisfied",
			about: "§5 axis 2 — issue #280",
			diff:  "",
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
			name:  "capability/denial-is-data",
			about: "§8 denial vs protocol violation",
			mode:  "deny_probe",
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				// The MECHANISM is asserted first: the boundary must have
				// denied the ungranted action, on capability grounds, and
				// recorded it. Checking the execution's status first would let
				// a neighbouring guard -- the agent's own consistency check --
				// fire before this one and report a different defect, which is
				// what the mutation harness caught.
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
				// Then the consequence: a denial is DATA, so the execution
				// continues rather than failing.
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
			about: "§5 applicability rule enforced on the wire",
			mode:  "bad_axes",
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusFailed {
					return Falsified, "an inapplicable axis was accepted: " + string(out.Result.Status)
				}
				if !strings.Contains(out.Result.Summary, "must not carry a failure class") {
					return Falsified, "rejected for the wrong reason: " + out.Result.Summary
				}
				return Proven, "completed+failure_class rejected: " + out.Result.Summary
			},
		},
		{
			name:  "cancel/cooperative",
			about: "§6 steps 1-2, and step 4's fence precondition",
			setup: func(h *harness, rt *host.Runtime, _ *contract.Invocation) {
				// The agent must be genuinely mid-action when cancellation
				// arrives, or this proves only that a finished agent ignores
				// it. Delaying the first action puts it inside one.
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
				if out.FenceReceipt != host.FenceTerminated {
					return Falsified, "terminal recorded without a positive receipt: " + string(out.FenceReceipt)
				}
				return Proven, "cancelled/superseded after a terminated receipt"
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
				if out.FenceReceipt != host.FenceTerminated {
					return Falsified, "receipt " + string(out.FenceReceipt)
				}
				return Proven, "grace expired, domain fenced, then cancelled/superseded recorded"
			},
		},
		{
			name:  "cancel/terminal-withheld-on-unconfirmed-fence",
			about: "§6 step 4 — a result written over an unfenced process is a false record",
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
			name:  "gate/headless-blocks-immediately",
			about: "ADR 0030 §4 + §5's blocked status",
			mode:  "escalate",
			setup: func(h *harness, _ *host.Runtime, inv *contract.Invocation) {
				h.boundary.Policy = requiresOperatorFor(capForge)
				inv.OperatorResponder = false
			},
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusBlocked {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				if len(out.Result.BlockedOn) == 0 {
					return Falsified, "blocked without a requirement reference"
				}
				var waiting bool
				for _, a := range h.recorder.Attempts() {
					if a.State == host.StateOperatorWaiting {
						waiting = true
					}
				}
				if !waiting {
					return Falsified, "no attempt records operator_waiting"
				}
				if out.Result.BlockedOn[0].AttemptID == "" {
					return Falsified, "the requirement does not name the attempt it belongs to"
				}
				return Proven, "blocked immediately, requirement referenced, attempt in operator_waiting"
			},
		},
		{
			name:  "gate/interactive-approval-proceeds",
			about: "ADR 0030 §4 gate 2, §6 the operator_waiting transition",
			mode:  "escalate",
			setup: func(h *harness, _ *host.Runtime, inv *contract.Invocation) {
				h.boundary.Policy = requiresOperatorFor(capForge)
				h.boundary.Operator = func(contract.RequirementRef) bool { return true }
				inv.OperatorResponder = true
			},
			check: func(h *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusCompleted {
					return Falsified, "status " + string(out.Result.Status) + " " + errText(out)
				}
				for _, a := range h.recorder.Attempts() {
					if a.Action == capForge {
						if !hasState(a, host.StateOperatorWaiting) {
							return Falsified, "the approved attempt never recorded operator_waiting"
						}
						if a.Outcome != contract.OutcomeSucceeded {
							return Falsified, "approved attempt outcome " + string(a.Outcome)
						}
						return Proven, "operator_waiting recorded as a durable transition, then executed"
					}
				}
				return Falsified, "the gated action was never attempted"
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
				for _, a := range h.recorder.Attempts() {
					if a.Action != capPublish {
						continue
					}
					if !hasState(a, host.StateResourceWaiting) {
						return Falsified, "no resource_waiting transition"
					}
					if hasState(a, host.StateOperatorWaiting) {
						return Falsified, "a resource wait was recorded as an operator wait"
					}
					return Proven, "resource_waiting recorded and distinguishable from operator_waiting"
				}
				return Falsified, "the delayed action was never attempted"
			},
		},
		{
			name:  "result/timed-out",
			about: "§5 — a deadline is Orchestrator-observed",
			mode:  "hang",
			setup: func(_ *harness, _ *host.Runtime, inv *contract.Invocation) {
				inv.Budgets.MaxWallClockMS = 500
			},
			check: func(_ *harness, out host.Outcome) (Outcome, string) {
				if out.Result.Status != contract.StatusTimedOut {
					return Falsified, "status " + string(out.Result.Status)
				}
				if out.Result.FailureClass == nil {
					return Falsified, "timed_out carries no failure class"
				}
				return Proven, "timed_out/" + string(*out.Result.FailureClass)
			},
		},
		{
			name:  "record/interrupted-attempt-reconciles-unknown",
			about: "ADR 0030 §8's Interrupted row; the value tool_calls cannot express",
			setup: func(h *harness, _ *host.Runtime, _ *contract.Invocation) {
				h.boundary.CrashAfterOpen = map[string]bool{capRead.String(): true}
			},
			check: func(h *harness, _ host.Outcome) (Outcome, string) {
				atts := h.recorder.Attempts()
				if len(atts) == 0 {
					return Errored, "no attempt was opened, so nothing was interrupted"
				}
				for _, a := range atts {
					if a.Action != capRead {
						continue
					}
					if a.State != host.StateSettled {
						return Falsified, "left in " + string(a.State) + " rather than reconciled"
					}
					if a.Outcome != contract.OutcomeUnknown {
						return Falsified, "settled as " + string(a.Outcome) + " rather than unknown"
					}
					return Proven, "intent recorded, no outcome, reconciled as unknown"
				}
				return Errored, "the interrupted action was never attempted"
			},
		},
	}
}

func hasState(a *host.Attempt, s host.AttemptState) bool {
	return slices.Contains(a.Transitions, s)
}

func errText(out host.Outcome) string {
	if out.DispatchErr != nil {
		return "(" + out.DispatchErr.Error() + ")"
	}
	return "(" + out.Result.Summary + ")"
}

func (r *runner) run(sc scenario) {
	diff := sampleDiff
	if sc.diff != "" || sc.name == "result/completed-already-satisfied" {
		diff = sc.diff
	}
	h := newHarness(diff)
	inv := invocation("inv-"+strings.NewReplacer("/", "-").Replace(sc.name), defaultCaps(), false)

	rt := &host.Runtime{
		BinaryPath: r.binary,
		Boundary:   h.boundary,
		Fencer:     func(contract.ResourceRef) host.FenceReceipt { return host.FenceTerminated },
	}
	if sc.setup != nil {
		sc.setup(h, rt, inv)
	}

	if sc.mode != "" {
		_ = os.Setenv("SPIKE_MODE", sc.mode)
	} else {
		_ = os.Setenv("SPIKE_MODE", "normal")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out := rt.Run(ctx, inv)

	if r.verbose {
		fmt.Printf("  events: %s\n", strings.Join(out.Events, " "))
	}

	outcome, detail := sc.check(h, out)
	r.claims = append(r.claims, claim{name: sc.name, about: sc.about, outcome: outcome, detail: detail})
	fmt.Printf("%-10s %-52s %s\n", outcome, sc.name, detail)
}

func (r *runner) report() int {
	fmt.Println()
	proven, falsified, errored := 0, 0, 0
	for _, c := range r.claims {
		switch c.outcome {
		case Proven:
			proven++
		case Falsified:
			falsified++
		case Errored:
			errored++
		}
	}
	fmt.Printf("%d claims: %d PROVEN, %d FALSIFIED, %d ERROR\n", len(r.claims), proven, falsified, errored)
	fmt.Println()
	fmt.Println("Declared UNCOVERED, not fabricated (ADR 0025's unavailable-versus-zero):")
	fmt.Println("  usage events, per-model-call provenance bindings, token accounting —")
	fmt.Println("  the stub backend makes no model calls, so nothing here exercises them.")
	if falsified > 0 || errored > 0 {
		return 1
	}
	return 0
}
