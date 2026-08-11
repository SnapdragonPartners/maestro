+++
title = "Spike: Docker Fencing Domains And The Socket Escape"
edit_date = "2026-08-11"
status = "draft"
type = "spike"
summary = "The executable Docker/Compose reproducer ADR 0029 requires: seven claims run against a live daemon, all proven, establishing that the socket escape is real, that a fencing domain contains it only when membership is enforced at creation rather than discovered at fencing time, that a fence must revoke creation before enumerating or lose to work done in the stopping window, and that an unconfirmed stop must keep the provider record a reconciler needs."
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

Every claim carries a control, because a claim without one shows a result without
showing what produces it. C0 controls C1. C2 and C3 control each other — the same
escape, differing only in whether creation stamps the domain. C4a and C4b control
each other — the same stopping work, differing only in order.

## Results

Run 2026-08-11 against Docker 29.6.2 / Compose v5.3.1, darwin/arm64.

| Claim | Statement | Result |
| --- | --- | --- |
| **C0** | Without the socket mount, the same commands cannot create a sibling | PROVEN |
| **C1** | A socket-holding container creates a sibling outside its PID namespace | PROVEN |
| **C2** | An unenforced domain does **not** contain the escaped sibling | PROVEN |
| **C3** | An enforced domain contains both the holder and its escaped sibling | PROVEN |
| **C4a** | Enumerate-then-stop misses siblings created while it stops | PROVEN |
| **C4b** | Revoke-then-enumerate survives the same stopping window | PROVEN |
| **C5** | An unconfirmed stop keeps the record a reconciler needs | PROVEN |

Nothing in §7 is falsified. Two findings sharpen it.

### Finding 1 — a domain is a boundary only if membership is enforced at creation

This is the one worth carrying into the provider work, and it is not what the
plan's compatibility matrix implies on a fast read.

C1 and C2 observe the same escaped sibling. Descendant-walking misses it, as the
plan says — the sibling's process is a child of the daemon and never appears in
the holder's process table. But **domain enumeration misses it too**, because a
container created through the raw socket carries no domain label. Filtering by
domain returned only the holder.

C3 repeats the escape with the domain label stamped at creation — what a
mediating proxy or private daemon must guarantee — and enumeration then returns
both.

So "include every created container in the immutable fencing domain" is not
something a provider can do at fencing time. Membership is either enforced when
the container is created or it is unavailable afterwards, and a provider that
mounts the raw socket and hopes to enumerate by label at fence time has **no
containment boundary at all** — it merely has one that looks like it works until
something creates a container the provider did not.

### Finding 2 — the race is real and is measured in the stopping window

C4a and C4b differ only in order. Both enumerate a domain a holder is actively
populating, then stop everything they enumerated.

- **Enumerate, then stop:** enumerated and stopped 19; **38** present in the
  domain afterwards. Nineteen containers were created while the fence was busy
  stopping the first nineteen, and the fence had already fixed its set.
- **Revoke, then enumerate, then stop:** enumerated and stopped 18; **18**
  present afterwards. Zero created inside the window.

The window is not an instant between two adjacent calls — it is however long the
fence spends doing its work, which scales with the size of the domain. The larger
the Habitat, the wider the hole.

## Two defects in the reproducer itself, and why they are recorded

The first run reported C4a `FALSIFIED` and C5 `PROVEN`. Both were wrong, and both
were defects in the tests rather than in the ADR. They are recorded because the
failure shape is the one this repository keeps paying for — **a check that cannot
fail for the defect it protects against**.

**C4a could not observe the race it measured.** The first version enumerated and
then revoked microseconds later, leaving no window for anything to be created in.
It reported that the naive order was safe. The fix was to make the naive path do
what a naive implementation actually does — spend real time stopping the set it
enumerated — which is where the window comes from.

**C5 passed vacuously.** It tried to force a stop failure by trapping `SIGTERM`,
but `docker stop` escalates to `SIGKILL`, so the container stopped normally and
the check asserted only that a successfully stopped container is still listed by
Docker. It also had the wrong subject: v1's defect at
`pkg/exec/docker_long_running.go:356-380` is that **Maestro** deletes its own
registry entry, not that Docker forgets anything. The check now models both
provider shapes directly against a container deliberately left running, so losing
the record has a visible consequence.

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
