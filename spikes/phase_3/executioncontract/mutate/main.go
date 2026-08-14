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
			name:  "reconciler-sweeps-an-operator-wait",
			file:  "host/boundary.go",
			old:   "\t\t\trep.OperatorWaitsPreserved = append(rep.OperatorWaitsPreserved, att)",
			new:   "\t\t\tatt.State = StateSettled\n\t\t\tatt.Outcome = contract.OutcomeUnknown\n\t\t\trep.SettledUnknown = append(rep.SettledUnknown, att)",
			claim: "reconcile/preserves-an-operator-wait",
			want:  "settled a healthy operator wait as unknown",
			why:   "THE DEFECT THE FIRST ROUND FOUND: an attempt waiting on an operator is healthy, not interrupted, and settling it destroys the requirement a blocked result must reference",
		},
		{
			name:  "reconciler-abandons-a-resource-wait",
			file:  "host/boundary.go",
			old:   "\t\t\trep.ResourceWaitsToRestore = append(rep.ResourceWaitsToRestore, att)",
			new:   "\t\t\t_ = att",
			claim: "reconcile/hands-back-a-resource-wait",
			want:  "0 resource waits handed back",
			why:   "a resource wait's provisioning operation does not survive a restart, so leaving it alone strands it exactly as settling it would corrupt it",
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
			old:   "\tif existing, ok := r.byCorrelation[key]; ok {",
			new:   "\tif existing, ok := r.byCorrelation[key]; ok && false {",
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
			old:   "\t\t\tb.Recorder.Settle(att, contract.OutcomeBlocked, requirement.Statement)",
			new:   "\t\t\tb.Recorder.Transition(att, StateOperatorWaiting)",
			claim: "gate/headless-blocks-with-one-durable-outcome",
			want:  "was left in operator_waiting",
			why:   "a headless wait has no responder, so recording it as a wait describes an event that will never happen -- and leaves the action nonterminal under a terminally blocked execution",
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
			old:   "\t\t\t\tr.Boundary.CloseAdmission(id)\n\t\t\t\tgrace := r.CancelGrace",
			new:   "\t\t\t\tgrace := r.CancelGrace",
			claim: "cancel/admission-closes",
			want:  "an action admitted during the grace period",
			why:   "ADR 0029 §7 step 2's ordering applied to attempts: without revoke-before-drain the holder keeps creating work the drain must then chase",
		},
		{
			name:  "event-identity-not-checked",
			file:  "host/runtime.go",
			old:   "\t\t\tif last, ok := seen[it.env.Epoch]; ok && it.env.Seq <= last {",
			new:   "\t\t\tif last, ok := seen[it.env.Epoch]; ok && it.env.Seq <= last && false {",
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
			name:  "capability-set-not-enforced",
			file:  "host/boundary.go",
			old:   "\tif !inv.HasCapability(action) {",
			new:   "\tif false && !inv.HasCapability(action) {",
			claim: "capability/denial-is-data",
			want:  "no denial was recorded",
			why:   "admission is the gate an empty policy must not be able to disable",
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
func runSuite(only string) (string, bool) {
	args := []string{"run", "."}
	if only != "" {
		args = append(args, "-run", only)
	}
	out, _ := run(5*time.Minute, "go", args...)
	return out, strings.Contains(out, "0 FALSIFIED, 0 ERROR")
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
