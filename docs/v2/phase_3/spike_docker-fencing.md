+++
title = "Spike: Docker Fencing Domains And The Socket Escape"
edit_date = "2026-08-11"
status = "draft"
type = "spike"
summary = "The executable Docker/Compose reproducer ADR 0029 requires: eight three-valued claims run against a live daemon with controls, checked observations, and mutation verification, all proven — establishing that the socket escape is real, that a fencing domain contains it only when a mediating boundary enforces membership at creation against a holder actively trying to opt out, that such a boundary must also be closed under the creation it permits or it is only one hop deep, that a fence must obtain an acknowledged revocation before enumerating and confirm every member non-running, and that an unconfirmed stop must keep the provider record a reconciler needs — with the reproducer's own nine false-proof defects and its self-inflicted container leak recorded rather than quietly repaired."
+++

# Spike: Docker Fencing Domains And The Socket Escape

Status: **draft** — the first of the two spike artifacts item A1 of the
[pre-Phase-3 blocker plan](plan_blockers.md) requires before
[ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) can be
Accepted. The second, a Kubernetes node-partition walkthrough, is separate and
paper-only.

Reproducer: [`spikes/phase_3/fencing`](../../../spikes/phase_3/fencing). Spike
code — a standalone module outside `pkg/`, `internal/`, and `cmd/`, unmaintained,
per CLAUDE.md's Spikes And Deferred Work.

## What it had to establish

ADR 0029 §7 asserts things about Docker that could be false. The reproducer is
written so that each is a falsifiable proposition, and a `FALSIFIED` result is a
successful run that changes the ADR rather than a broken test.

Outcomes are **three-valued**, because "the claim is false" and "the reproducer
could not observe it" are different results and collapsing them is how a spike
reports false proof: `PROVEN`, `FALSIFIED`, or `ERROR`. Every observation is
error-checked; none is allowed to read as evidence by failing quietly.

Every claim carries a control, because a claim without one shows a result without
showing what produces it:

- **C0 controls C1** — remove the socket and the same commands must fail, naming
  the missing daemon.
- **C1 carries a positive observation control** — a container deliberately
  sharing the holder's PID namespace must be *visible* in the same process table
  that shows the escaped sibling absent. Without it, "not visible" is equally
  consistent with a process table showing nothing useful.
- **C2 carries a positive enumeration control** — the labelled holder must be
  present in the same result that omits the sibling.
- **C2 and C3 control each other** — the same escape, differing only in whether
  creation is mediated.
- **C4a and C4b control each other** — the same stopping work, differing only in
  order.

Beyond controls, each mechanism is **mutation-verified**: disabled, rerun, and
required to flip its own claim and no others.

## Results

Run 2026-08-11 against Docker 29.6.2 / Compose v5.3.1, darwin/arm64.

```text
PROVEN     C0   without a daemon route the same commands fail, and fail for that reason
PROVEN     C1   a socket-holding container creates a sibling outside its PID namespace
PROVEN     C2   an unenforced domain omits the escaped sibling while containing the holder
PROVEN     C3   a mediated holder lands in the domain without supplying the label, and cannot opt out
PROVEN     C3b  a mediated holder cannot re-export an unmediated creation route
PROVEN     C4a  enumerate-then-stop misses siblings created while it stops
PROVEN     C4b  revoke-drain-enumerate-confirm yields a complete, confirmed set
PROVEN     C5   an unconfirmed stop keeps the record a reconciler needs (modeled)
```

Selected detail from the same run:

```text
C0:  stderr="failed to connect to the docker API at unix:///var/run/docker.sock;
     check if the path is correct and if the daemon is running: dial unix
     /var/run/docker.sock: connect: no such file or directory"
C1:  sibling running=true; escaped sibling visible=false; PID-sharing control visible=true
C2:  domain "unenforced" enumerates [holder]; holder present=true; sibling present=false
C3:  sibling in enforced domain=true; sibling in its own chosen domain=false
C3b: refused naming the escape=true; child exists=false; response="proxy: refused,
     would escape the fencing domain: bind mounts the daemon socket
     (/var/run/docker.sock:/var/run/docker.sock)"
C4a: enumerated and stopped 7; 13 in the domain afterwards: 6 created inside the window
C4b: mediator acknowledged 11 drained creates; fenced and stopped 12; members
     outside the fenced set=[]; stop errors=[]; still running=[]
```

**Mutation-verified.** Each mechanism was disabled and the run repeated, to
confirm the claim depends on the mechanism rather than on the environment.
Disabling the proxy's label injection flips C3 with the expected detail (`in
enforced domain=false; in its own chosen domain=true`); disabling its escape
rejection flips C3b; giving the escaped sibling a shared PID namespace flips C1.
In each case the other claims stayed proven, so the mutations were targeted
rather than merely destructive.

Nothing in §7 is falsified. Three findings sharpen it, and the third was found
only by building the mediator well enough to attack it.

**C5 is a modeled proof, not a live-daemon proof**, and is labelled as such
throughout. `docker stop` escalates to `SIGKILL`, so a genuine unrecoverable stop
failure cannot be induced on demand; and the subject is not Docker anyway. v1's
defect at `pkg/exec/docker_long_running.go:356-380` is that **Maestro** deletes
its own registry entry. The check models both provider shapes against a container
deliberately left running, so losing the record has a visible consequence.

### Finding 1 — a domain is a boundary only if membership is enforced at creation

This is the one worth carrying into the provider work, and it is not what the
plan's compatibility matrix implies on a fast read.

C1 and C2 observe the same escaped sibling. Descendant-walking misses it, as the
plan says — the sibling's process is a child of the daemon and never appears in
the holder's process table. But **domain enumeration misses it too**, because a
container created through the raw socket carries no domain label. Filtering by
domain returned only the holder, which is why C2 requires the holder's presence
as a positive control: "the sibling is absent" is also true of an enumeration
that returned nothing at all.

C3 supplies the other half, and it is an **enforcement** proof rather than a
labelling demonstration. The holder reaches the daemon only through a mediating
proxy that rewrites every `POST /containers/create`, and it does not merely omit
the domain label — it actively supplies a **competing value** for the same key.
The proxy overwrites it. The sibling lands in the enforced domain and is absent
from the one it chose for itself.

That distinction matters: a holder that volunteers the correct label proves only
that a stamped container is enumerable, which is a tautology. What the ADR needs
is that a holder *cannot opt out*, and that is what C3 now measures.

So "include every created container in the immutable fencing domain" is not
something a provider can do at fencing time. Membership is either enforced when
the container is created or it is unavailable afterwards, and a provider that
mounts the raw socket and hopes to enumerate by label at fence time has **no
containment boundary at all** — it merely has one that looks like it works until
something creates a container the provider did not.

The proxy is [`spikes/phase_3/fencing/proxy`](../../../spikes/phase_3/fencing/proxy).
It is a demonstration of the mediation option, not a design proposal for the
Phase 3 provider.

### Finding 1b — stamping is not containment, and building the mediator proved it

Writing the proxy well enough to satisfy C3 exposed a hole the ADR did not have:
**a mediator that rewrites labels but forwards the rest of the request unchanged
is one hop deep.** It will create a *correctly labelled* child that mounts the
daemon socket, and that child creates an unlabeled grandchild through the raw
socket. The domain holds at hop one and fails at hop two.

C3b is the control for it. The mediated holder asks for a child bind-mounting
`/var/run/docker.sock`; the mediator refuses, naming the escape, and no such
child exists. The proxy now also rejects privileged execution, shared PID
namespaces, and host networking, on the same grounds — they are routes out of the
domain that happen not to be a socket.

The general form is worth stating without Docker in it, and it is now in ADR 0029
§7: **a mediated boundary must be closed under the creation it permits.** If a
resource inside the domain can create something with more authority than itself,
the boundary is one hop deep, and one hop is not a boundary.

This is the argument for preferring a private daemon where one is available: it
closes the same gap by construction rather than by enumeration, and an
enumeration of escape routes is only ever as good as its last review.

### Finding 2 — the race is real and is measured in the stopping window

C4a and C4b differ only in order. Both enumerate a domain a holder is actively
populating, then stop everything they enumerated.

Both paths start only once the spawner has demonstrably produced containers, so
neither can pass by fencing an idle domain.

- **Enumerate, then stop:** enumerated and stopped 7; **14** present in the
  domain afterwards. Seven containers were created while the fence was busy
  stopping the first seven, and the fence had already fixed its set.
- **Revoke, drain, enumerate, stop, confirm:** drained at 7, fenced and stopped
  7, 7 present afterwards, **no stop errors and nothing still running**.

The window is not an instant between two adjacent calls — it is however long the
fence spends doing its work, which scales with the size of the domain. The larger
the Habitat, the wider the hole.

C4b establishes a **receipt**, and each part of it was earned by a defect:

- **An acknowledged revocation, not a killed process.** Killing the proxy
  discards the error and cannot distinguish "revoked" from "crashed", and creates
  already accepted may still be completing. The mediator now exposes a barrier
  that stops admitting creates, waits for in-flight creates to finish, and
  acknowledges with the count it drained. An earlier version inferred drainage
  from cardinality holding steady for 750ms, which measures a quiet daemon rather
  than a closed one.
- **Confirmed termination**, not stopping. Every member of the fenced set must be
  observed non-running, with stop errors checked rather than discarded.
- **Membership, not cardinality.** `len(final) <= len(fenced)` passes if a fenced
  member disappears while a late-created one replaces it. The check now requires
  no member outside the fenced set.
- **A live producer at the moment of revocation.** Reaching the spawn floor only
  proves the spawner was active earlier; C4b now re-samples immediately before
  revoking and requires growth, so it cannot pass by fencing a spawner that had
  already finished.

## The reproducer's own defects, and why they are recorded

Across three review rounds, successive versions reported results that were not
evidence. All are recorded because the failure shape is the one this repository
keeps paying for — **a check that cannot fail for the defect it protects
against** — and because a spike that hides its own repairs is asking to be
trusted rather than read.

Nine defects, in the order they were found.

**C4a could not observe the race it measured.** It enumerated and revoked
microseconds apart, leaving no window, and reported the naive order safe. The
naive path now spends real time stopping what it enumerated, which is where the
window comes from.

**C5 passed vacuously.** It tried to force a stop failure by trapping `SIGTERM`,
but `docker stop` escalates to `SIGKILL`, so the container stopped normally and
the check asserted only that a successfully stopped container is still listed by
Docker. It also had the wrong subject. It is now explicitly a modeled provider
proof, and asserts the retained record's *state* rather than its mere presence.

**C3 was a tautology.** The holder volunteered the domain label, so the claim
reduced to "a labelled container is enumerable." Enforcement is now proved
against a holder that supplies a competing label and has it overwritten.

**Observation failures were being read as evidence.** C0 accepted *any* command
error as proof that removing the socket blocked creation — a failed package
install produced the same PROVEN. C1 discarded the error from reading the
holder's process table, so a failed `ps` read as "not visible." Domain
enumeration converted its error to an empty set, which satisfies "the sibling is
absent." All three are now checked, `ERROR` is a distinct outcome, and C0
additionally requires the failure text to name the unreachable daemon.

**C1's predicate could not detect a single visible sibling.** After the holder's
sentinel changed to `sleep 900`, the check tested `Count("sleep 600") > 1` — but
one visible sibling produces a count of exactly one, so the predicate reported
"invisible" regardless of the truth. It could not have failed. Sentinels are now
distinct per role, presence is tested with `Contains`, and a container
deliberately sharing the holder's PID namespace supplies a **positive observation
control**: if the control is not visible, the process table cannot be trusted and
the claim reports `ERROR` rather than proof.

**The mediator permitted a transitive escape** — Finding 1b above.

**Revocation was neither checked nor acknowledged**, and drainage was inferred
from cardinality holding steady — see the C4b list above.

**C4b compared cardinality rather than membership.**

**The spawner was only known to have been active earlier**, not at the moment of
revocation.

### And one the reproducer proved on itself

The first run leaked its entire container set — 35 containers, found still
present two hours later. `report()` called `os.Exit` on the falsified claim,
which skips deferred functions, so the cleanup the report described as total
never ran.

Two further leak paths were found while fixing it: early returns in the C4 setup
skipped the race teardown, leaving a spawner running; and a single-pass cleanup
can be outrun by a spawner that is still creating. The run now funnels every exit
through one function so cleanup is genuinely deferred, tears races down with
`defer`, and sweeps repeatedly until the label returns nothing — warning loudly,
with the exact removal command, if anything survives.

It is worth naming that the harness's own leak was the same race the artifact
exists to document: a set enumerated once while something was still adding to it.

## What this requires of the Phase 3 provider

1. **Enforce domain membership at creation.** Remove raw socket access, or
   mediate it so every created container is stamped with the `FencingDomainID`
   before it exists. Enumerating by label over a raw socket is not fencing.
2. **Revoke creation before enumerating.** The order is part of the contract, not
   an implementation detail, and the reproducer measures what ignoring it costs.
3. **Confirm after stopping**, against the set taken after revocation.
4. **Never delete a provider record on an unconfirmed stop.** Mark it
   `unconfirmed` and leave it enumerable.

## Reproducing

```bash
cd spikes/phase_3/fencing
go run .          # runs all claims, cleans up after itself
go run . -keep    # leaves containers in place for inspection
```

Exits non-zero if any claim is falsified. Every container it creates carries a
`maestro.spike.run` label, so cleanup is total regardless of which claim failed;
`-keep` suppresses it deliberately.

Requires a reachable Docker daemon and pulls `alpine:3` if absent. It creates and
destroys on the order of 60 containers per run.

## Related Documents

- [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) — §7,
  whose Docker/Compose claims this tests.
- [Pre-Phase-3 Blockers](plan_blockers.md) — item A1's bounded spike requirement:
  this artifact and one paper walkthrough, and no more.
- [ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md)
  — the shared-state discipline the record-preservation rule belongs to.
