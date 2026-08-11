// Command fencing is the ADR 0029 A1 spike reproducer.
//
// It tests the claims ADR 0029 §7 makes about Docker/Compose fencing. Each is a
// falsifiable proposition with three possible outcomes, because "the claim is
// false" and "the reproducer could not observe it" are different results and
// collapsing them is how a spike reports false proof:
//
//	PROVEN     the claim held, and its control held too
//	FALSIFIED  the claim is false — a successful run that changes the ADR
//	ERROR      an observation failed; nothing is established either way
//
//	C0 CONTROL      Without a route to a daemon, the same commands cannot create
//	                a sibling, and they fail *for that reason*. C1's mechanism.
//	C1 ESCAPE       A container holding the host Docker socket creates a sibling
//	                outside its own PID namespace.
//	C2 UNENFORCED   That sibling is absent from the domain while the holder is
//	                present — so an unenforced domain is not a boundary.
//	C3 ENFORCED     Behind a mediating proxy, a holder that supplies a competing
//	                domain label still lands inside the enforced domain.
//	C3b CONTAIN     That holder cannot obtain a child that directly binds the
//	                daemon socket — one escape shape, refused.
//	C3c NOT CLOSED  But the same mediator is NOT capability-closed. It filters
//	                creates and forwards everything else, so the holder execs
//	                into the proxy container — which holds the raw socket — and
//	                creates outside the domain. PROVEN means the hole is real.
//	C3d NO RECEIPT  And the consequence: fence that domain, confirm every member
//	                non-running, and the escaped container is still running. A
//	                receipt speaks only for the domain it enumerated.
//	C4a RACE        Enumerate-then-stop is outrun by a holder still creating
//	                during the stopping window.
//	C4b ORDERING    An acknowledged creation barrier, then enumerate, stop, and
//	                confirm every member non-running, yields a set with no member
//	                outside it — membership, not cardinality. Scoped to creation.
//	C5 RECORDS      An unconfirmed stop must leave the provider's record intact.
//	                Modeled provider state, not a live-daemon behaviour.
//
// Controls: C0 for C1; C1 carries a PID-sharing positive control; C2 carries a
// positive enumeration control; C2 and C3 control each other (identical escape,
// differing in whether creation is mediated); C4a and C4b control each other
// (identical stopping work, differing only in order).
//
// C3c and C3d are deliberately claims that a hole EXISTS. Their conclusion is
// that create-filtering mediation is not a conforming fencing strategy — see the
// spike report and ADR 0029 §7.
//
// Run: go run . [-keep]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// domainLabel stands in for an immutable FencingDomainID.
	domainLabel = "maestro.spike.domain"
	// runLabel marks everything a run creates so cleanup is total.
	runLabel = "maestro.spike.run"

	image     = "alpine:3"
	proxyPort = "2375"

	// spawnFloor is how many containers must exist before a fence begins, so
	// both C4 paths are known to have started against an active spawner.
	spawnFloor = 6
)

var keep = flag.Bool("keep", false, "leave containers in place for inspection")

type outcome int

const (
	proven outcome = iota
	falsified
	errored
)

func (o outcome) String() string {
	switch o {
	case proven:
		return "PROVEN"
	case falsified:
		return "FALSIFIED"
	default:
		return "ERROR"
	}
}

type claim struct {
	id, statement string
	result        outcome
	detail        string
}

var results []claim

func record(id, statement string, result outcome, format string, args ...any) {
	detail := fmt.Sprintf(format, args...)
	results = append(results, claim{id, statement, result, detail})
	fmt.Printf("  [%s] %s: %s\n\n", result, id, detail)
}

// assert records PROVEN when held is true and FALSIFIED otherwise.
func assert(id, statement string, held bool, format string, args ...any) {
	r := falsified
	if held {
		r = proven
	}
	record(id, statement, r, format, args...)
}

// observeFailed records ERROR: the reproducer could not see what it needed to.
func observeFailed(id, statement string, err error, format string, args ...any) {
	record(id, statement, errored, "%s: %v", fmt.Sprintf(format, args...), err)
}

func main() { os.Exit(runMain()) }

// runMain exists so cleanup actually runs. os.Exit skips deferred functions, so
// every exit path has to funnel through a return here — an earlier version
// called os.Exit from three helpers and could leak the whole container set on
// any failure, while the report claimed cleanup was total.
func runMain() int {
	flag.Parse()
	runID := fmt.Sprintf("spike%d", time.Now().UnixNano())
	fmt.Printf("ADR 0029 §7 fencing reproducer\nrun: %s\n\n", runID)

	if !*keep {
		defer cleanup(runID)
	}

	if err := requireDocker(); err != nil {
		fmt.Fprintf(os.Stderr, "docker unavailable: %v\n", err)
		return 2
	}
	if err := ensureImage(); err != nil {
		fmt.Fprintf(os.Stderr, "could not obtain %s: %v\n", image, err)
		return 2
	}
	proxyBin, err := buildProxy()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not build proxy: %v\n", err)
		return 2
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(proxyBin)) }()

	c0(runID)
	c1c2(runID)
	c3(runID, proxyBin)
	c4(runID, proxyBin)
	c5(runID)

	return report()
}

// ---------- claims ----------

// c0 is C1's control: it must fail, and fail *because no daemon is reachable*.
// An earlier version accepted any command error, so a network failure during
// package installation would have produced the same PROVEN — a control that
// cannot identify its own mechanism.
func c0(runID string) {
	const id, statement = "C0", "without a daemon route the same commands fail, and fail for that reason"
	fmt.Println("C0 — control: no socket, no escape, and the error names the cause")

	holder := runID + "-holder-nosocket"
	if err := startHolder(holder, runID, "", nil); err != nil {
		observeFailed(id, statement, err, "could not start holder")
		return
	}
	// Install and verify the CLI separately, so a failed install cannot be
	// mistaken for a blocked daemon request.
	if err := installCLI(holder); err != nil {
		observeFailed(id, statement, err, "could not install docker-cli")
		return
	}

	out, err := run("docker", "exec", holder, "docker", "run", "-d", "--name",
		runID+"-sibling-nosocket", image, "sleep", "600")
	if err == nil {
		assert(id, statement, false, "sibling creation unexpectedly succeeded: %s", trim(out))
		return
	}

	// The failure must name the missing daemon, not something incidental.
	blocked := strings.Contains(out, "Cannot connect to the Docker daemon") ||
		strings.Contains(out, "docker daemon running") ||
		strings.Contains(out, "/var/run/docker.sock")
	exists, existsErr := containerExists(runID + "-sibling-nosocket")
	if existsErr != nil {
		observeFailed(id, statement, existsErr, "could not check for the sibling")
		return
	}

	assert(id, statement, blocked && !exists,
		"creation failed naming the daemon=%v; sibling exists=%v; stderr=%q",
		blocked, exists, trim(out))
}

// c1c2 runs the escape and the unenforced-domain check on one sibling: C1 asks
// whether it exists outside the holder's PID namespace, C2 whether the domain
// contains it.
func c1c2(runID string) {
	domain := runID + "-unenforced"
	fmt.Println("C1/C2 — socket escape, and whether an unenforced domain contains it")

	holder := runID + "-holder"
	// The Incubator stand-in: the raw host socket, exactly as
	// pkg/exec/docker_long_running.go:243 mounts it unconditionally today.
	if err := startHolder(holder, runID, domain,
		[]string{"-v", "/var/run/docker.sock:/var/run/docker.sock"}); err != nil {
		observeFailed("C1", "socket escape", err, "could not start holder")
		return
	}
	if err := installCLI(holder); err != nil {
		observeFailed("C1", "socket escape", err, "could not install docker-cli")
		return
	}

	// Distinct sentinel durations, so presence is unambiguous. The holder runs
	// `sleep 900`; an earlier version gave the sibling `sleep 600` and then
	// tested `Count(...) > 1`, which cannot be true for a single sibling — the
	// check reported "invisible" no matter what, and could not have failed.
	const escapedMarker, sharedMarker = "601", "602"

	sibling := runID + "-sibling-unlabeled"
	if out, err := run("docker", "exec", holder, "docker", "run", "-d",
		"--name", sibling, "--label", runLabel+"="+runID, image, "sleep", escapedMarker); err != nil {
		observeFailed("C1", "socket escape", err, "could not create sibling: %s", trim(out))
		return
	}

	running, err := containerRunning(sibling)
	if err != nil {
		observeFailed("C1", "socket escape", err, "could not inspect the sibling")
		return
	}

	// Positive observation control: a container deliberately sharing the
	// holder's PID namespace *must* be visible. Without it, "not visible" is
	// equally consistent with `ps` showing nothing useful at all.
	shared := runID + "-pidshared"
	if out, err := run("docker", "run", "-d", "--name", shared,
		"--label", runLabel+"="+runID,
		"--pid=container:"+holder, image, "sleep", sharedMarker); err != nil {
		observeFailed("C1", "socket escape", err, "could not create the PID-namespace control: %s", trim(out))
		return
	}

	psOut, psErr := run("docker", "exec", holder, "ps", "-o", "args")
	if psErr != nil {
		observeFailed("C1", "socket escape", psErr, "could not read the holder's process table: %s", trim(psOut))
		return
	}
	escapedVisible := strings.Contains(psOut, "sleep "+escapedMarker)
	controlVisible := strings.Contains(psOut, "sleep "+sharedMarker)

	if !controlVisible {
		observeFailed("C1", "socket escape",
			fmt.Errorf("PID-namespace control not visible in the holder's process table"),
			"the process table cannot be trusted to show what is there; ps output=%q", trim(psOut))
		return
	}

	assert("C1", "a socket-holding container creates a sibling outside its PID namespace",
		running && !escapedVisible,
		"sibling running=%v; escaped sibling visible=%v; PID-sharing control visible=%v",
		running, escapedVisible, controlVisible)

	// C2 needs a positive control. "The sibling is absent" is also true of an
	// enumeration that returned nothing at all, so the holder — which does carry
	// the label — must be present in the same result.
	inDomain, err := containersInDomain(domain)
	if err != nil {
		observeFailed("C2", "unenforced domain", err, "could not enumerate the domain")
		return
	}
	assert("C2", "an unenforced domain omits the escaped sibling while containing the holder",
		contains(inDomain, holder) && !contains(inDomain, sibling),
		"domain %q enumerates %v; holder present=%v; sibling present=%v",
		short(domain, runID), short(inDomain, runID),
		contains(inDomain, holder), contains(inDomain, sibling))
}

// c3 is C2's counterpart and the enforcement proof. The holder reaches the
// daemon only through the mediating proxy, and never supplies the domain label —
// it actively supplies a competing value for the same key. An earlier version
// had the holder volunteer the correct label, which proved only that a stamped
// container is enumerable: a tautology, not enforcement.
func c3(runID, proxyBin string) {
	const id, statement = "C3", "a mediated holder lands in the domain without supplying the label, and cannot opt out"
	domain := runID + "-enforced"
	fmt.Println("C3 — enforced membership: the proxy stamps the domain, the holder cannot refuse")

	net := runID + "-net"
	if out, err := run("docker", "network", "create", "--label", runLabel+"="+runID, net); err != nil {
		observeFailed(id, statement, err, "could not create network: %s", trim(out))
		return
	}
	proxy, err := startProxy(runID, "proxy-enforced", net, domain, proxyBin)
	if err != nil {
		observeFailed(id, statement, err, "could not start proxy")
		return
	}

	holder := runID + "-holder-mediated"
	if err := startHolder(holder, runID, domain, []string{
		"--network", net,
		"-e", "DOCKER_HOST=tcp://" + proxy + ":" + proxyPort,
	}); err != nil {
		observeFailed(id, statement, err, "could not start mediated holder")
		return
	}
	if err := installCLI(holder); err != nil {
		observeFailed(id, statement, err, "could not install docker-cli")
		return
	}

	// The holder tries to place itself in a *different* domain. Enforcement
	// means overwriting, not defaulting.
	sibling := runID + "-sibling-mediated"
	if out, err := run("docker", "exec", holder, "docker", "run", "-d",
		"--name", sibling,
		"--label", runLabel+"="+runID,
		"--label", domainLabel+"=attacker-chosen-domain",
		image, "sleep", "600"); err != nil {
		observeFailed(id, statement, err, "could not create mediated sibling: %s", trim(out))
		return
	}

	inDomain, err := containersInDomain(domain)
	if err != nil {
		observeFailed(id, statement, err, "could not enumerate the domain")
		return
	}
	escaped, err := containersInDomain("attacker-chosen-domain")
	if err != nil {
		observeFailed(id, statement, err, "could not enumerate the attacker domain")
		return
	}

	assert(id, statement,
		contains(inDomain, sibling) && !contains(escaped, sibling),
		"sibling in enforced domain=%v; sibling in its own chosen domain=%v; enforced domain enumerates %v",
		contains(inDomain, sibling), contains(escaped, sibling), short(inDomain, runID))

	// C3b: stamping alone is not containment. A correctly labelled child that
	// holds the daemon socket can create an unlabeled grandchild through the raw
	// socket, and the domain survives one hop and fails at two. The mediator must
	// refuse to hand out a route it does not mediate.
	const id3b, stmt3b = "C3b", "a mediated holder cannot re-export an unmediated creation route"
	twoHop := runID + "-sibling-rearming"
	out, err := run("docker", "exec", holder, "docker", "run", "-d",
		"--name", twoHop,
		"--label", runLabel+"="+runID,
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		image, "sleep", "600")
	if err == nil {
		assert(id3b, stmt3b, false,
			"the mediator accepted a child holding the daemon socket: %s", trim(out))
		return
	}
	exists, existsErr := containerExists(twoHop)
	if existsErr != nil {
		observeFailed(id3b, stmt3b, existsErr, "could not check for the re-arming child")
		return
	}
	refusedForReason := strings.Contains(out, "would escape the fencing domain")

	assert(id3b, stmt3b, refusedForReason && !exists,
		"refused naming the escape=%v; child exists=%v; response=%q",
		refusedForReason, exists, trim(out))

	// C3c: the create filter is not the boundary it appears to be. Everything
	// that is not a create is forwarded unchanged, and the proxy container itself
	// holds the raw socket — so the holder reaches the daemon by exec'ing into
	// its own mediator. This claim asserts the escape SUCCEEDS: it is evidence
	// that create-filtering mediation is not capability-closed.
	const id3c, stmt3c = "C3c", "a create-filtering mediator is NOT capability-closed: non-create APIs reach the daemon"
	escapeName := runID + "-sibling-via-proxy-exec"
	out, err = run("docker", "exec", holder, "sh", "-c",
		"docker exec "+proxy+" sh -c 'apk add --no-cache docker-cli >/dev/null 2>&1 && "+
			"docker run -d --name "+escapeName+" --label "+runLabel+"="+runID+
			" "+image+" sleep 600'")
	escaped2, existsErr := containerExists(escapeName)
	if existsErr != nil {
		observeFailed(id3c, stmt3c, existsErr, "could not check for the escaped container")
		return
	}
	inDomain2, domErr := containersInDomain(domain)
	if domErr != nil {
		observeFailed(id3c, stmt3c, domErr, "could not enumerate the domain")
		return
	}

	// PROVEN here means the hole is real. Escaping via a forwarded API, and
	// landing outside the domain, is the finding.
	assert(id3c, stmt3c,
		err == nil && escaped2 && !contains(inDomain2, escapeName),
		"exec through the mediator succeeded=%v; unlabeled container created=%v; inside the domain=%v; output=%q",
		err == nil, escaped2, contains(inDomain2, escapeName), trim(out))

	// C3d: the consequence for the receipt. Fence this domain properly — stop
	// every member and confirm it — and the escaped container is untouched,
	// because a receipt can only speak for the domain it enumerated.
	//
	// A first attempt at this claim tried to restart a fenced container *through
	// the holder*, and failed for the wrong reason: the holder carries the domain
	// label, so fencing had already stopped it. That is correct behaviour, and it
	// hid the real composition — the surviving actor is the one that escaped the
	// domain, not one inside it.
	const id3d, stmt3d = "C3d", "a confirmed receipt over the domain does NOT cover what escaped it"
	members, err := containersInDomain(domain)
	if err != nil {
		observeFailed(id3d, stmt3d, err, "could not enumerate the domain")
		return
	}
	for _, m := range members {
		_, _ = run("docker", "stop", "-t", "0", m)
	}
	stillUp, err := awaitAllStopped(members)
	if err != nil {
		observeFailed(id3d, stmt3d, err, "could not confirm termination")
		return
	}
	survivor, err := containerRunning(escapeName)
	if err != nil {
		observeFailed(id3d, stmt3d, err, "could not inspect the escaped container")
		return
	}

	assert(id3d, stmt3d,
		len(stillUp) == 0 && survivor,
		"domain fenced with %d members, all confirmed non-running=%v; the escaped container is still running=%v",
		len(members), len(stillUp) == 0, survivor)
}

// c4 tests ordering. Both paths do identical stopping work against a domain a
// holder is actively populating; only the order differs.
func c4(runID, proxyBin string) {
	fmt.Println("C4 — fencing must revoke creation, drain, then enumerate and confirm")

	// Naive: enumerate, then spend real time stopping what was enumerated,
	// having never revoked creation.
	naive, err := setupRace(runID, "race", proxyBin)
	if naive != nil {
		// Deferred, not called at the end: every early return below would
		// otherwise leave the spawner running, which is a second leak path
		// beside the os.Exit one — and cleanup can be outrun by a live spawner.
		defer naive.teardown()
	}
	if err != nil {
		observeFailed("C4a", "enumerate-then-stop", err, "could not set up the naive path")
		return
	}
	if err := naive.awaitActive(); err != nil {
		observeFailed("C4a", "enumerate-then-stop", err, "spawner never became active")
		return
	}

	first, err := containersInDomain(naive.domain)
	if err != nil {
		observeFailed("C4a", "enumerate-then-stop", err, "could not enumerate")
		return
	}
	for _, name := range first {
		_, _ = run("docker", "stop", "-t", "0", name)
	}
	after, err := containersInDomain(naive.domain)
	if err != nil {
		observeFailed("C4a", "enumerate-then-stop", err, "could not re-enumerate")
		return
	}

	assert("C4a", "enumerate-then-stop misses siblings created while it stops",
		len(after) > len(first),
		"enumerated and stopped %d; %d in the domain afterwards: %d created inside the window",
		len(first), len(after), len(after)-len(first))

	// Correct: confirm the spawner is producing right now, obtain an acknowledged
	// revocation from the mediator (creation closed, accepted creates drained),
	// enumerate, stop with checked errors, confirm every member non-running, then
	// re-enumerate and compare membership. Earlier versions inferred drainage
	// from cardinality holding steady and compared set sizes, so they could report
	// a receipt they had not established.
	good, err := setupRace(runID, "race2", proxyBin)
	if good != nil {
		defer good.teardown()
	}
	if err != nil {
		observeFailed("C4b", "revoke-drain-enumerate-confirm", err, "could not set up the correct path")
		return
	}
	if err := good.awaitActive(); err != nil {
		observeFailed("C4b", "revoke-drain-enumerate-confirm", err, "spawner never became active")
		return
	}

	if err := good.stillProducing(); err != nil {
		observeFailed("C4b", "revoke-drain-enumerate-confirm", err,
			"the spawner must be creating at the moment of revocation")
		return
	}
	drained, err := good.revoke()
	if err != nil {
		observeFailed("C4b", "revoke-drain-enumerate-confirm", err, "revocation was not acknowledged")
		return
	}

	fenced, err := containersInDomain(good.domain)
	if err != nil {
		observeFailed("C4b", "revoke-drain-enumerate-confirm", err, "could not enumerate after draining")
		return
	}
	var stopErrs []string
	for _, name := range fenced {
		if out, err := run("docker", "stop", "-t", "0", name); err != nil {
			stopErrs = append(stopErrs, fmt.Sprintf("%s: %v (%s)", name, err, trim(out)))
		}
	}
	stillRunning, err := awaitAllStopped(fenced)
	if err != nil {
		observeFailed("C4b", "revoke-drain-enumerate-confirm", err, "could not confirm termination")
		return
	}

	final, err := containersInDomain(good.domain)
	if err != nil {
		observeFailed("C4b", "revoke-drain-enumerate-confirm", err, "could not re-enumerate")
		return
	}

	// Membership, not cardinality. An earlier version compared lengths, which
	// passes if a fenced member disappears while a late-created one replaces it.
	var outside []string
	for _, n := range final {
		if !contains(fenced, n) {
			outside = append(outside, n)
		}
	}

	// Scoped deliberately: this proves the *ordering* property over creation
	// admission — nothing entered the domain inside the window, every stop
	// succeeded, every member is confirmed non-running. It does not by itself
	// establish a terminal receipt; C4c is why.
	assert("C4b", "an acknowledged creation barrier yields a complete, confirmed set at the moment of fencing",
		len(outside) == 0 && len(stopErrs) == 0 && len(stillRunning) == 0,
		"mediator acknowledged %d drained creates; fenced and stopped %d; members outside the fenced set=%v; stop errors=%v; still running=%v",
		drained, len(fenced), short(outside, runID), stopErrs, stillRunning)

	_ = drained
}

// c5 is a modeled proof about provider state, not a live-daemon behaviour, and
// is reported as such. v1's defect at pkg/exec/docker_long_running.go:356-380 is
// that on a stop failure Maestro deletes its own entry from activeContainers and
// unregisters from the global registry, so nothing downstream can reconcile a
// container that is still running. Docker is not the subject.
//
// An earlier version tried to force a real stop failure by trapping SIGTERM.
// `docker stop` escalates to SIGKILL, so the container stopped and the check
// asserted only that a successfully stopped container is still listed.
func c5(runID string) {
	const id, statement = "C5", "an unconfirmed stop keeps the record a reconciler needs (modeled)"
	fmt.Println("C5 — modeled provider state: a failed stop must not delete the record")

	name := runID + "-unreaped"
	if err := startHolder(name, runID, "", nil); err != nil {
		observeFailed(id, statement, err, "could not start the container")
		return
	}

	stopFails := func() error { return fmt.Errorf("simulated daemon failure") }

	v1 := map[string]string{name: "running"}
	if err := stopFails(); err != nil {
		delete(v1, name) // v1: swallow, drop the record, return nil
	}
	conformant := map[string]string{name: "running"}
	if err := stopFails(); err != nil {
		conformant[name] = "unconfirmed" // keep it, mark it
	}

	// The container really is left running, so losing the record has a visible
	// consequence rather than being a bookkeeping difference.
	running, err := containerRunning(name)
	if err != nil {
		observeFailed(id, statement, err, "could not inspect the container")
		return
	}
	_, v1Has := v1[name]
	state, conformantHas := conformant[name]

	assert(id, statement, running && conformantHas && state == "unconfirmed" && !v1Has,
		"container still running=%v; v1-shaped registry retains it=%v; conformant retains it=%v with state=%q (must be \"unconfirmed\")",
		running, v1Has, conformantHas, state)
}

// ---------- race harness ----------

type race struct {
	runID                          string
	domain, network, proxy, holder string
	spawner                        *exec.Cmd
}

// setupRace stands up a mediated holder that creates siblings continuously. It
// must still be creating when the fence begins, or neither ordering is under
// test — awaitActive is what establishes that.
func setupRace(runID, tag, proxyBin string) (*race, error) {
	r := &race{
		runID:   runID,
		domain:  runID + "-" + tag,
		network: runID + "-net-" + tag,
		holder:  runID + "-holder-" + tag,
	}
	if out, err := run("docker", "network", "create", "--label", runLabel+"="+runID, r.network); err != nil {
		return nil, fmt.Errorf("network: %v (%s)", err, trim(out))
	}
	proxy, err := startProxy(runID, "proxy-"+tag, r.network, r.domain, proxyBin)
	if err != nil {
		return nil, err
	}
	r.proxy = proxy

	if err := startHolder(r.holder, runID, r.domain, []string{
		"--network", r.network,
		"-e", "DOCKER_HOST=tcp://" + proxy + ":" + proxyPort,
	}); err != nil {
		return nil, err
	}
	if err := installCLI(r.holder); err != nil {
		return nil, err
	}

	script := "i=0; while [ $i -lt 200 ]; do docker run -d --name " + runID +
		"-" + tag + "-$i --label " + runLabel + "=" + runID + " " + image +
		" sleep 600 >/dev/null 2>&1; i=$((i+1)); done"
	r.spawner = exec.Command("docker", "exec", r.holder, "sh", "-c", script)
	if err := r.spawner.Start(); err != nil {
		return nil, fmt.Errorf("spawner: %w", err)
	}
	return r, nil
}

// awaitActive blocks until the spawner has demonstrably created containers, so
// the fence is known to start against a live producer.
func (r *race) awaitActive() error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		in, err := containersInDomain(r.domain)
		if err != nil {
			return err
		}
		if len(in) >= spawnFloor {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("spawner did not reach %d containers", spawnFloor)
}

// stillProducing confirms the spawner is creating *right now*, immediately
// before revocation. awaitActive only proves it was active earlier, and a fence
// against a spawner that has already finished tests nothing.
func (r *race) stillProducing() error {
	before, err := containersInDomain(r.domain)
	if err != nil {
		return err
	}
	time.Sleep(400 * time.Millisecond)
	after, err := containersInDomain(r.domain)
	if err != nil {
		return err
	}
	if len(after) <= len(before) {
		return fmt.Errorf("spawner not producing at revocation time (%d then %d)",
			len(before), len(after))
	}
	return nil
}

// revoke asks the mediator to close creation and drain accepted creates, and
// requires an acknowledgment. An earlier version killed the proxy and discarded
// the error, then inferred drainage from cardinality holding steady for 750ms —
// neither a closed door nor a drained queue, just a quiet one. The call runs from
// a throwaway container on the same network, so no host port has to be published.
func (r *race) revoke() (int, error) {
	out, err := run("docker", "run", "--rm", "--network", r.network,
		"--label", runLabel+"="+r.runID, image,
		"wget", "-qO-", "--post-data=", "http://"+r.proxy+":"+proxyPort+"/spike/revoke")
	if err != nil {
		return 0, fmt.Errorf("revocation call failed: %v (%s)", err, trim(out))
	}
	var ack struct {
		Revoked bool   `json:"revoked"`
		Drained int    `json:"drained"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(trim(out)), &ack); err != nil {
		return 0, fmt.Errorf("unparseable acknowledgment %q: %w", trim(out), err)
	}
	if !ack.Revoked {
		return 0, fmt.Errorf("mediator did not acknowledge revocation: %s", ack.Error)
	}
	return ack.Drained, nil
}

func (r *race) teardown() {
	_, _ = run("docker", "kill", r.holder, r.proxy)
	if r.spawner != nil && r.spawner.Process != nil {
		_ = r.spawner.Process.Kill()
		_ = r.spawner.Wait()
	}
}

// awaitAllStopped is the confirmation half of the receipt: every recorded
// container must be observed non-running. Returns those that never were.
func awaitAllStopped(names []string) ([]string, error) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		var running []string
		for _, n := range names {
			isRunning, err := containerRunning(n)
			if err != nil {
				return nil, err
			}
			if isRunning {
				running = append(running, n)
			}
		}
		if len(running) == 0 {
			return nil, nil
		}
		if time.Now().After(deadline) {
			return running, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// ---------- docker helpers ----------

func startHolder(name, runID, domain string, extra []string) error {
	args := []string{"docker", "run", "-d", "--name", name, "--label", runLabel + "=" + runID}
	if domain != "" {
		args = append(args, "--label", domainLabel+"="+domain)
	}
	args = append(args, extra...)
	args = append(args, image, "sleep", "900")
	if out, err := run(args...); err != nil {
		return fmt.Errorf("%v (%s)", err, trim(out))
	}
	return nil
}

func startProxy(runID, name, network, domain, proxyBin string) (string, error) {
	full := runID + "-" + name
	out, err := run("docker", "run", "-d", "--name", full,
		"--label", runLabel+"="+runID,
		"--network", network,
		"-v", proxyBin+":/proxy:ro",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-e", "DOMAIN_LABEL="+domainLabel,
		"-e", "DOMAIN_VALUE="+domain,
		image, "/proxy")
	if err != nil {
		return "", fmt.Errorf("proxy: %v (%s)", err, trim(out))
	}
	// Wait for it to listen; a holder pointed at a dead proxy would fail in a
	// way indistinguishable from enforcement working.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if logs, _ := run("docker", "logs", full); strings.Contains(logs, "enforcing") {
			return full, nil
		}
		if running, _ := containerRunning(full); !running {
			logs, _ := run("docker", "logs", full)
			return "", fmt.Errorf("proxy exited: %s", trim(logs))
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("proxy did not start listening")
}

// installCLI installs and verifies the Docker CLI, separately from any use of
// it, so an install failure can never be read as a blocked daemon request.
func installCLI(holder string) error {
	if out, err := run("docker", "exec", holder, "apk", "add", "--no-cache", "docker-cli"); err != nil {
		return fmt.Errorf("apk add: %v (%s)", err, trim(out))
	}
	if out, err := run("docker", "exec", holder, "docker", "--version"); err != nil {
		return fmt.Errorf("cli unusable: %v (%s)", err, trim(out))
	}
	return nil
}

func containersInDomain(domain string) ([]string, error) {
	out, err := run("docker", "ps", "-a", "--filter", "label="+domainLabel+"="+domain,
		"--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("%v (%s)", err, trim(out))
	}
	return nonEmptyLines(out), nil
}

func containerRunning(name string) (bool, error) {
	out, err := run("docker", "inspect", "-f", "{{.State.Running}}", name)
	if err != nil {
		if strings.Contains(out, "No such object") {
			return false, nil
		}
		return false, fmt.Errorf("%v (%s)", err, trim(out))
	}
	return strings.TrimSpace(out) == "true", nil
}

func containerExists(name string) (bool, error) {
	out, err := run("docker", "ps", "-a", "--filter", "name=^/"+name+"$", "--format", "{{.Names}}")
	if err != nil {
		return false, fmt.Errorf("%v (%s)", err, trim(out))
	}
	return strings.TrimSpace(out) != "", nil
}

func requireDocker() error {
	if out, err := run("docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("%v (%s)", err, trim(out))
	}
	return nil
}

func ensureImage() error {
	if _, err := run("docker", "image", "inspect", image); err == nil {
		return nil
	}
	if out, err := run("docker", "pull", image); err != nil {
		return fmt.Errorf("%v (%s)", err, trim(out))
	}
	return nil
}

// buildProxy cross-compiles the mediating proxy for the daemon's platform. The
// binary is bind-mounted as a file, which works where a host unix socket would
// not.
func buildProxy() (string, error) {
	dir, err := os.MkdirTemp("", "maestro-spike-proxy")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "proxy")
	arch, err := run("docker", "version", "--format", "{{.Server.Arch}}")
	if err != nil {
		return "", fmt.Errorf("could not read daemon arch: %w", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, "./proxy")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux",
		"GOARCH="+strings.TrimSpace(arch))
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	return bin, nil
}

// cleanup removes everything a run created. It loops rather than sweeping once,
// because a holder that is still spawning can create containers between the
// enumeration and the removal — the same race the fencing protocol is about,
// arriving in the harness. Removing the holders kills their spawners, so this
// converges rather than chasing indefinitely.
func cleanup(runID string) {
	total := 0
	for range 10 {
		out, err := run("docker", "ps", "-aq", "--filter", "label="+runLabel+"="+runID)
		if err != nil {
			break
		}
		ids := nonEmptyLines(out)
		if len(ids) == 0 {
			break
		}
		_, _ = run(append([]string{"docker", "rm", "-f"}, ids...)...)
		total += len(ids)
		time.Sleep(200 * time.Millisecond)
	}

	if leftover, err := run("docker", "ps", "-aq", "--filter", "label="+runLabel+"="+runID); err == nil {
		if n := len(nonEmptyLines(leftover)); n > 0 {
			fmt.Fprintf(os.Stderr, "WARNING: %d containers could not be removed; "+
				"remove with: docker rm -f $(docker ps -aq --filter label=%s=%s)\n",
				n, runLabel, runID)
		}
	}

	nets, _ := run("docker", "network", "ls", "-q", "--filter", "label="+runLabel+"="+runID)
	if ids := nonEmptyLines(nets); len(ids) > 0 {
		_, _ = run(append([]string{"docker", "network", "rm"}, ids...)...)
	}
	if total > 0 {
		fmt.Printf("cleaned up %d containers\n", total)
	}
}

// ---------- plumbing ----------

func run(args ...string) (string, error) {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	return string(out), err
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

func contains(hay []string, needle string) bool { return slices.Contains(hay, needle) }

func trim(s string) string { return strings.TrimSpace(s) }

// short trims the run prefix so output stays readable.
func short[T string | []string](v T, runID string) any {
	switch x := any(v).(type) {
	case string:
		return strings.TrimPrefix(x, runID+"-")
	case []string:
		out := make([]string, 0, len(x))
		for _, n := range x {
			out = append(out, strings.TrimPrefix(n, runID+"-"))
		}
		return out
	}
	return v
}

func report() int {
	fmt.Println("========================================")
	var bad int
	for _, c := range results {
		fmt.Printf("%-9s  %-4s %s\n", c.result, c.id, c.statement)
		if c.result != proven {
			bad++
		}
	}
	fmt.Println("========================================")
	if bad > 0 {
		fmt.Printf("\n%d claim(s) not proven — see detail above.\n", bad)
		return 1
	}
	fmt.Println("\nAll claims proven.")
	return 0
}
