// Command fencing is the ADR 0029 A1 spike reproducer.
//
// It tests the claims ADR 0029 §7 makes about Docker/Compose fencing. Each is
// stated as a falsifiable proposition and reported PROVEN or FALSIFIED; the
// program's purpose is evidence, so a FALSIFIED result is a successful run that
// changes the ADR.
//
//	C0 CONTROL      Without the socket mount, the same commands cannot create a
//	                sibling. C1's mechanism check.
//	C1 ESCAPE       A container holding the host Docker socket can create a
//	                sibling that is outside its PID namespace.
//	C2 UNENFORCED   When that sibling is created without the domain label,
//	                domain enumeration misses it too — so a fencing domain is a
//	                containment boundary only if membership is enforced at
//	                creation, not hoped for at fencing time.
//	C3 ENFORCED     When creation stamps the domain label, domain enumeration
//	                finds the sibling that descendant-walking misses.
//	C4a RACE        Enumerate-then-stop loses to a holder still creating during
//	                the stopping window.
//	C4b ORDER       Revoke-then-enumerate survives the same window.
//	C5 RECORDS      An unconfirmed stop must leave the provider's record intact,
//	                which is the property pkg/exec/docker_long_running.go:356-380
//	                destroys today.
//
// Each claim has a control: C0 for C1, C2 and C3 for each other (identical
// escape, differing only in whether creation stamps the domain), and C4a and C4b
// for each other (identical work, differing only in order). A claim without a
// control would show a result without showing what produces it.
//
// Run: go run . [-keep]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const (
	// domainLabel is the label standing in for an immutable FencingDomainID.
	// Compose's own project label works identically; a distinct key keeps the
	// spike's domain separate from any real Compose project on the machine.
	domainLabel = "maestro.spike.domain"

	// runLabel marks everything this run creates, so cleanup can be total
	// regardless of which claim failed.
	runLabel = "maestro.spike.run"

	image = "alpine:3"
)

var keep = flag.Bool("keep", false, "leave containers in place for inspection")

type claim struct {
	id, statement string
	proven        bool
	detail        string
}

var results []claim

func record(id, statement string, proven bool, format string, args ...any) {
	results = append(results, claim{id, statement, proven, fmt.Sprintf(format, args...)})
	status := "FALSIFIED"
	if proven {
		status = "PROVEN"
	}
	fmt.Printf("  [%s] %s: %s\n\n", status, id, fmt.Sprintf(format, args...))
}

func main() {
	flag.Parse()
	runID := fmt.Sprintf("spike%d", time.Now().UnixNano())
	fmt.Printf("ADR 0029 §7 fencing reproducer\nrun: %s\n\n", runID)

	if !*keep {
		defer cleanup(runID)
	}

	requireDocker()
	pull()

	c0(runID)
	c1c2(runID)
	c3(runID)
	c4(runID)
	c5(runID)

	report()
}

// ---------- claims ----------

// c0 is C1's control. Without it, C1 shows only that a sibling exists — not that
// the socket mount is what permits it. Defect-shaped verification: the mechanism
// must be shown to be the mechanism.
func c0(runID string) {
	fmt.Println("C0 — control: without the socket mount, the escape is unavailable")

	holder := runID + "-holder-nosocket"
	mustRun("docker", "run", "-d", "--name", holder,
		"--label", runLabel+"="+runID,
		image, "sleep", "600")

	_, err := run("docker", "exec", holder, "sh", "-c",
		"apk add --no-cache docker-cli >/dev/null 2>&1 && "+
			"docker run -d --name "+runID+"-sibling-nosocket --label "+runLabel+"="+runID+
			" "+image+" sleep 600")

	created := containerExists(runID + "-sibling-nosocket")

	record("C0", "without the socket mount the same commands cannot create a sibling",
		err != nil && !created,
		"sibling creation failed=%v; sibling exists=%v", err != nil, created)
}

// c1c2 runs the escape and the unenforced-domain check together, because both
// observe the same sibling: C1 asks whether it exists outside the PID namespace,
// C2 asks whether domain enumeration finds it.
func c1c2(runID string) {
	domain := runID + "-unenforced"
	fmt.Println("C1/C2 — socket escape, and whether an unenforced domain contains it")

	// The Incubator stand-in: a long-running container with the raw host Docker
	// socket mounted, exactly as pkg/exec/docker_long_running.go:243 does
	// unconditionally today.
	holder := runID + "-holder"
	mustRun("docker", "run", "-d", "--name", holder,
		"--label", runLabel+"="+runID,
		"--label", domainLabel+"="+domain,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		image, "sleep", "600")

	// From inside the holder, create a sibling. Nothing here is privileged
	// beyond the socket mount itself.
	sibling := runID + "-sibling-unlabeled"
	out, err := run("docker", "exec", holder, "sh", "-c",
		"apk add --no-cache docker-cli >/dev/null 2>&1 && "+
			"docker run -d --name "+sibling+" --label "+runLabel+"="+runID+
			" "+image+" sleep 600")
	if err != nil {
		record("C1", "socket escape creates a sibling", false,
			"could not create sibling from inside the holder: %v (%s)", err, out)
		return
	}

	siblingRunning := containerRunning(sibling)

	// Descendant view: what the holder's own PID namespace can see. A sibling
	// container's process is a child of the daemon, never of the holder, so it
	// cannot appear here — which is why walking descendants is not a containment
	// boundary.
	psInside, _ := run("docker", "exec", holder, "ps", "-o", "args")
	visibleInside := strings.Contains(psInside, "sleep 600") &&
		strings.Count(psInside, "sleep 600") > 1

	record("C1", "a socket-holding container creates a sibling outside its PID namespace",
		siblingRunning && !visibleInside,
		"sibling running=%v; visible in holder's process table=%v", siblingRunning, visibleInside)

	// Domain view, with membership NOT enforced at creation.
	inDomain := containersInDomain(domain)
	record("C2", "an unenforced domain does not contain the escaped sibling",
		!contains(inDomain, sibling),
		"domain %q enumerates %v; sibling %q absent=%v",
		domain, short(inDomain, runID), short([]string{sibling}, runID)[0], !contains(inDomain, sibling))
}

// c3 repeats the escape with the domain label stamped at creation — the
// behaviour a mediating proxy or private daemon must guarantee — and checks that
// domain enumeration now finds what descendant-walking cannot.
func c3(runID string) {
	domain := runID + "-enforced"
	fmt.Println("C3 — an enforced domain contains the sibling that descendant-walking misses")

	holder := runID + "-holder-enforced"
	mustRun("docker", "run", "-d", "--name", holder,
		"--label", runLabel+"="+runID,
		"--label", domainLabel+"="+domain,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		image, "sleep", "600")

	sibling := runID + "-sibling-labeled"
	out, err := run("docker", "exec", holder, "sh", "-c",
		"apk add --no-cache docker-cli >/dev/null 2>&1 && "+
			"docker run -d --name "+sibling+
			" --label "+runLabel+"="+runID+
			" --label "+domainLabel+"="+domain+
			" "+image+" sleep 600")
	if err != nil {
		record("C3", "an enforced domain contains the escaped sibling", false,
			"could not create labeled sibling: %v (%s)", err, out)
		return
	}

	inDomain := containersInDomain(domain)
	found := contains(inDomain, sibling) && contains(inDomain, holder)

	record("C3", "an enforced domain contains both the holder and its escaped sibling",
		found, "domain %q enumerates %v", domain, short(inDomain, runID))
}

// c4 tests the ordering requirement. The holder creates siblings continuously;
// a fence that enumerates first and revokes second must lose to it, and one that
// revokes first must not.
func c4(runID string) {
	fmt.Println("C4 — fencing must revoke creation before enumerating")

	domain := runID + "-race"
	holder := runID + "-holder-race"
	mustRun("docker", "run", "-d", "--name", holder,
		"--label", runLabel+"="+runID,
		"--label", domainLabel+"="+domain,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		image, "sleep", "600")

	if out, err := run("docker", "exec", holder, "sh", "-c",
		"apk add --no-cache docker-cli >/dev/null 2>&1"); err != nil {
		record("C4", "revoke-before-enumerate closes the sibling-creation race", false,
			"could not install docker-cli in holder: %v (%s)", err, out)
		return
	}

	// The window a naive fence leaves open is the time it spends stopping the
	// set it enumerated. An earlier version of this check enumerated and revoked
	// microseconds apart, leaving no window at all — so it could not fail for the
	// defect it exists to detect, and reported a false PROVEN for the wrong
	// order. The spawner therefore runs throughout, and the naive path does the
	// work a real implementation would do between the two steps.
	spawner := startSpawner(holder, runID, "race", domain)

	time.Sleep(spawnLead)
	enumeratedFirst := containersInDomain(domain)
	// A naive fence stops what it enumerated, one at a time, and only then
	// considers itself done. Creation was never revoked.
	for _, name := range enumeratedFirst {
		_, _ = run("docker", "stop", "-t", "0", name)
	}
	afterNaiveWork := containersInDomain(domain)
	missedByNaive := len(afterNaiveWork) - len(enumeratedFirst)
	stopSpawner(spawner, holder)

	record("C4a", "enumerate-then-stop misses siblings created while it stops",
		missedByNaive > 0,
		"enumerated and stopped %d; %d present in the domain afterwards: %d created inside the window",
		len(enumeratedFirst), len(afterNaiveWork), missedByNaive)

	// Correct order: revoke creation, then enumerate, then confirm. The set is
	// taken when nothing further can be added to it, so the same stopping work
	// cannot be outrun.
	domain2 := runID + "-race2"
	holder2 := runID + "-holder-race2"
	mustRun("docker", "run", "-d", "--name", holder2,
		"--label", runLabel+"="+runID,
		"--label", domainLabel+"="+domain2,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		image, "sleep", "600")
	if out, err := run("docker", "exec", holder2, "sh", "-c",
		"apk add --no-cache docker-cli >/dev/null 2>&1"); err != nil {
		record("C4b", "revoke-then-enumerate yields a complete set", false,
			"could not install docker-cli in holder2: %v (%s)", err, out)
		return
	}

	spawner2 := startSpawner(holder2, runID, "race2", domain2)
	time.Sleep(spawnLead)

	revokeCreation(holder2)
	enumeratedAfterRevoke := containersInDomain(domain2)
	for _, name := range enumeratedAfterRevoke {
		_, _ = run("docker", "stop", "-t", "0", name)
	}
	settled := containersInDomain(domain2)
	missedByCorrect := len(settled) - len(enumeratedAfterRevoke)
	stopSpawner(spawner2, holder2)

	record("C4b", "revoke-then-enumerate survives the same stopping window",
		missedByCorrect <= 0,
		"enumerated and stopped %d after revoking; %d present afterwards: %d created inside the window",
		len(enumeratedAfterRevoke), len(settled), missedByCorrect)
}

// c5 checks record preservation. The subject is the *provider's own registry*,
// not Docker's state: v1's defect at pkg/exec/docker_long_running.go:356-380 is
// that on a stop failure it deletes its entry from activeContainers and
// unregisters from the global registry, so nothing downstream can reconcile the
// container that is still running.
//
// An earlier version of this check tried to make `docker stop` fail by trapping
// SIGTERM. That proves nothing: `docker stop` escalates to SIGKILL, so the
// container stopped and the check passed vacuously, asserting only that a
// successfully stopped container is still listed. The two shapes below are
// modelled directly instead, and the container really is left running so the
// consequence of losing the record is visible.
func c5(runID string) {
	fmt.Println("C5 — a failed stop must leave the provider's record intact")

	name := runID + "-unreaped"
	mustRun("docker", "run", "-d", "--name", name,
		"--label", runLabel+"="+runID,
		image, "sleep", "600")

	// Both providers attempt a stop; the stop fails for both. The container is
	// left running so that "still recorded" has consequences.
	stopFails := func() error { return fmt.Errorf("simulated daemon failure") }

	v1 := map[string]string{name: "running"}
	if err := stopFails(); err != nil {
		delete(v1, name) // v1: swallow the error, drop the record, return nil.
	}

	conformant := map[string]string{name: "running"}
	if err := stopFails(); err != nil {
		conformant[name] = "unconfirmed" // keep the record; mark it unconfirmed.
	}

	stillRunning := containerRunning(name)
	_, v1Has := v1[name]
	state, conformantHas := conformant[name]

	// Reconciliation is only possible against a record that still exists. The
	// container is running in both cases; only one provider can still find it.
	reconcilable := conformantHas && !v1Has && stillRunning

	record("C5", "an unconfirmed stop keeps the record a reconciler needs",
		reconcilable,
		"container still running=%v; v1-shaped registry retains it=%v; conformant retains it=%v (state=%q)",
		stillRunning, v1Has, conformantHas, state)
}

// ---------- docker helpers ----------

// spawnLead is how long a spawner runs before the fence starts, so that both
// orders face a domain already populated and still growing.
const spawnLead = 2 * time.Second

// startSpawner runs a holder that creates siblings continuously until stopped.
// It must still be creating while the fence does its stopping work, or neither
// ordering is actually under test.
func startSpawner(holder, runID, tag, domain string) *exec.Cmd {
	script := "i=0; while [ $i -lt 200 ]; do docker run -d --name " + runID +
		"-" + tag + "-$i --label " + runLabel + "=" + runID +
		" --label " + domainLabel + "=" + domain + " " + image +
		" sleep 600 >/dev/null 2>&1; i=$((i+1)); done"
	cmd := exec.Command("docker", "exec", holder, "sh", "-c", script)
	_ = cmd.Start()
	return cmd
}

func stopSpawner(cmd *exec.Cmd, holder string) {
	_, _ = run("docker", "kill", holder)
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

// revokeCreation removes the holder's ability to create further containers. A
// real provider does this by withdrawing socket access or killing the mediating
// proxy; here, killing the holder is the equivalent act, and what matters is
// that it happens before the set is taken.
func revokeCreation(holder string) {
	_, _ = run("docker", "kill", holder)
}

func containersInDomain(domain string) []string {
	out, err := run("docker", "ps", "-a", "--filter", "label="+domainLabel+"="+domain,
		"--format", "{{.Names}}")
	if err != nil {
		return nil
	}
	return nonEmptyLines(out)
}

func containerRunning(name string) bool {
	out, err := run("docker", "inspect", "-f", "{{.State.Running}}", name)
	return err == nil && strings.TrimSpace(out) == "true"
}

func containerExists(name string) bool {
	out, err := run("docker", "ps", "-a", "--filter", "name=^/"+name+"$", "--format", "{{.Names}}")
	return err == nil && strings.TrimSpace(out) != ""
}

func requireDocker() {
	if _, err := run("docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		fmt.Fprintln(os.Stderr, "docker daemon not reachable")
		os.Exit(2)
	}
}

func pull() {
	if _, err := run("docker", "image", "inspect", image); err == nil {
		return
	}
	mustRun("docker", "pull", image)
}

func cleanup(runID string) {
	out, _ := run("docker", "ps", "-aq", "--filter", "label="+runLabel+"="+runID)
	ids := nonEmptyLines(out)
	if len(ids) == 0 {
		return
	}
	_, _ = run(append([]string{"docker", "rm", "-f"}, ids...)...)
	fmt.Printf("cleaned up %d containers\n", len(ids))
}

// ---------- plumbing ----------

func run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustRun(args ...string) string {
	out, err := run(args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %s: %v\n%s\n", strings.Join(args, " "), err, out)
		os.Exit(1)
	}
	return out
}

func nonEmptyLines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func contains(hay []string, needle string) bool {
	return slices.Contains(hay, needle)
}

// short trims the run prefix so output is readable.
func short(names []string, runID string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.TrimPrefix(n, runID+"-"))
	}
	return out
}

func report() {
	fmt.Println("========================================")
	falsified := 0
	for _, c := range results {
		status := "PROVEN   "
		if !c.proven {
			status = "FALSIFIED"
			falsified++
		}
		fmt.Printf("%s  %-4s %s\n", status, c.id, c.statement)
	}
	fmt.Println("========================================")
	if falsified > 0 {
		fmt.Printf("\n%d claim(s) falsified — ADR 0029 §7 needs revision.\n", falsified)
		os.Exit(1)
	}
	fmt.Println("\nAll claims proven.")
}
