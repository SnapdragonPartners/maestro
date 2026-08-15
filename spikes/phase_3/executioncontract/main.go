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
	"sync"
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
	// -race on the SUBPROCESS as well. The harness running race-instrumented
	// says nothing about the agent, which is half the system under test and the
	// half that is genuinely concurrent (a reader goroutine beside the work
	// loop).
	cmd := exec.Command("go", "build", "-race", "-o", bin, "./reviewagent")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return bin, cleanup, nil
}

// ---------- fixtures ----------

const sampleDiff = "--- a/main.go\n+++ b/main.go\n+// TODO: wire this up\n+func added() {}\n"

var (
	capRead    = contract.ActionID{Kind: "repo", Verb: "read_diff"}
	capPublish = contract.ActionID{Kind: "artifact", Verb: "publish"}
	capForge   = contract.ActionID{Kind: "forge", Verb: "push"}
	capAsk     = contract.ActionAsk
)

func defaultCaps() []contract.ActionID {
	return []contract.ActionID{capRead, capPublish, capForge, capAsk}
}

// invocation builds the two halves separately, which is the point: the
// ExecutionConfig is resolved once and reused verbatim across incarnations,
// while the Bindings carry what is true of THIS one.
func invocation(id string, responder bool) *contract.Invocation {
	return &contract.Invocation{
		Config: contract.ExecutionConfig{
			ID:        id,
			Principal: contract.PrincipalRef{InstanceID: "pi-" + id, Kind: "agent"},
			Role:      "reviewer",
			Work:      contract.WorkRef{StoryID: "story-1", EpicID: "epic-1", EffectiveVersion: 7},
			Seeding: []contract.ArtifactRef{
				{ArtifactID: "art-spec-1", Digest: "sha256:aaaa"},
			},
			Model: contract.ModelBinding{
				// Explicit, never inferred from the name (§3). This is exactly
				// the case v1's ProviderPatterns gets wrong: an OpenAI-lineage
				// model whose name begins "gpt" but runs on a local Ollama.
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
			Capabilities: defaultCaps(),
			Budgets:      contract.Budgets{MaxWallClockMS: 20000},
			Sources: []contract.ExpectedSource{
				{Source: "P", Mechanism: "invocation"},
				{Source: "H", Mechanism: "invocation"},
				{Source: "seeding", Mechanism: "invocation"},
				{Source: "turn", Mechanism: "adapter-events"},
			},
			OperatorResponder: responder,
		},
		Bindings: contract.Bindings{
			Epoch: 1,
			Resources: []contract.ResourceRef{
				{Kind: "incubator", ReferenceID: "inc-1", InstanceGeneration: 4},
			},
		},
	}
}

type harness struct {
	boundary  *host.Boundary
	recorder  *host.Recorder
	mu        sync.Mutex
	published []json.RawMessage
	routed    []json.RawMessage
	// publishDelay makes the EFFECT slow rather than the gate. An attempt is
	// then `open` and mid-commit, which is the only shape a drain must wait on
	// -- a declared wait it simply stops.
	publishDelay time.Duration
}

func newHarness(diff string) *harness {
	rec := host.NewRecorder()
	b := host.NewBoundary(rec)
	h := &harness{boundary: b, recorder: rec}

	b.Executors[capRead.String()] = func(_ *contract.Invocation, _ json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]string{"text": diff})
	}
	b.Executors[capPublish.String()] = func(_ *contract.Invocation, args json.RawMessage) (json.RawMessage, error) {
		if d := h.publishDelay; d > 0 {
			time.Sleep(d)
		}
		h.mu.Lock()
		h.published = append(h.published, args)
		h.mu.Unlock()
		return json.Marshal(map[string]string{"artifact_id": "art-findings-1"})
	}
	b.Executors[capForge.String()] = func(_ *contract.Invocation, _ json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]string{"ref": "pushed"})
	}
	b.Executors[capAsk.String()] = func(inv *contract.Invocation, args json.RawMessage) (json.RawMessage, error) {
		// Validate BEFORE recording the routing: an earlier version appended
		// first, so a refused question still counted as routed.
		var q struct {
			QuestionArtifact string `json:"question_artifact"`
		}
		_ = json.Unmarshal(args, &q)
		if q.QuestionArtifact == "" {
			return nil, fmt.Errorf("ask carries no question artifact; a question is an artifact, not inline text")
		}
		h.mu.Lock()
		h.routed = append(h.routed, args)
		h.mu.Unlock()
		// Routing OPENS an execution-level wait. The action itself settles now;
		// what waits is the execution, on another principal (§4).
		rec.OpenResponseWait(inv.ID(), q.QuestionArtifact)
		return json.Marshal(map[string]string{"delivered_to": "architect", "routed": q.QuestionArtifact})
	}
	b.CurrentGeneration["inc-1"] = 4
	b.EffectiveVersion = 7
	return h
}

func (h *harness) publishedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.published)
}

func (h *harness) routedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.routed)
}

// requiresOperatorFor is the shape candidate 12's human gates will have: it
// returns *requires an operator* with a structured requirement, and declares
// nothing else.
func requiresOperatorFor(action contract.ActionID) host.PolicyHook {
	return requiresOperatorWith(action, []contract.RequirementRef{{
		GateID:    "spike.forge-push",
		Statement: "pushing a ref requires an operator decision",
		Scopes:    []string{"once", "for_story"},
	}})
}

// requiresOperatorWith returns a COMPLETE requirement set, which is what
// ADR 0030 §3 collects before anything blocks.
func requiresOperatorWith(action contract.ActionID, reqs []contract.RequirementRef) host.PolicyHook {
	return func(_ *contract.Invocation, a contract.ActionID, _ json.RawMessage) (host.Decision, []contract.RequirementRef) {
		if a == action {
			out := make([]contract.RequirementRef, len(reqs))
			copy(out, reqs)
			return host.DecisionRequiresOperator, out
		}
		return host.DecisionAllow, nil
	}
}

// ---------- wire scenarios ----------

type scenario struct {
	name  string
	about string
	mode  string
	diff  string
	// emptyDiff distinguishes "no diff configured" from "deliberately empty".
	emptyDiff bool
	setup     func(h *harness, rt *host.Runtime, inv *contract.Invocation)
	check     func(h *harness, out host.Outcome) (Outcome, string)
}

func (r *runner) run(sc scenario) {
	diff := sampleDiff
	if sc.emptyDiff {
		diff = ""
	} else if sc.diff != "" {
		diff = sc.diff
	}
	h := newHarness(diff)
	inv := invocation("inv-"+strings.NewReplacer("/", "-").Replace(sc.name), false)

	rt := &host.Runtime{
		BinaryPath: r.binary,
		Boundary:   h.boundary,
		Fencer:     func(contract.ResourceRef) host.FenceReceipt { return host.FenceTerminated },
	}
	if sc.setup != nil {
		sc.setup(h, rt, inv)
	}

	mode := sc.mode
	if mode == "" {
		mode = "normal"
	}
	_ = os.Setenv("SPIKE_MODE", mode)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out := rt.Run(ctx, inv)

	if r.verbose {
		fmt.Printf("  events: %s\n", strings.Join(out.Events, " "))
	}

	outcome, detail := sc.check(h, out)
	r.claims = append(r.claims, claim{name: sc.name, about: sc.about, outcome: outcome, detail: detail})
	fmt.Printf("%-10s %-54s %s\n", outcome, sc.name, detail)
}

func hasState(a *host.Attempt, s host.AttemptState) bool {
	return slices.Contains(a.Transitions, s)
}

func attemptFor(h *harness, action contract.ActionID) *host.Attempt {
	for _, a := range h.recorder.Attempts() {
		if a.Action == action {
			return a
		}
	}
	return nil
}

func errText(out host.Outcome) string {
	if out.DispatchErr != nil {
		return "(" + out.DispatchErr.Error() + ")"
	}
	return "(" + out.Result.Summary + ")"
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
	fmt.Println("  the stub backend makes no model calls, so nothing here exercises them,")
	fmt.Println("  and it deliberately emits no provenance event rather than a hollow one.")
	fmt.Println("  Also uncovered: concurrency accounting (§7), resumable runtimes,")
	fmt.Println("  the retention traversal (§9), paired execution, and non-local transports.")
	if falsified > 0 || errored > 0 {
		return 1
	}
	return 0
}
