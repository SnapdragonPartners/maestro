+++
title = "Spike: Fencing A Partitioned Kubernetes Node"
edit_date = "2026-08-12"
status = "draft"
type = "spike"
summary = "The paper walkthrough ADR 0029 requires: a node partition is the case where confirmed termination is unavailable by construction, and it shows that `isolated` is earned by a positive act — evicting the node's API identity and detaching its storage — rather than granted by a timer, that Kubernetes' own default is `unconfirmed` and force-deletion is a lie the API lets you tell, and that the Docker spike's capability-closure finding recurs here as a stricter rule, since a partitioned kubelet keeps every capability it already holds."
+++

# Spike: Fencing A Partitioned Kubernetes Node

Status: **draft** — the second of the two spike artifacts item A1 of the
[pre-Phase-3 blocker plan](plan_blockers.md) requires before
[ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) can be
Accepted. The first is the executable
[Docker reproducer](spike_docker-fencing.md).

**Paper only, and deliberately so.** No cluster is required, none was used, and
nothing here is a commitment to a Kubernetes provider — ADR 0029 defers all
non-Docker backends. This exists to test one thing: whether the three-valued
receipt survives contact with a failure shape where **confirmed termination is
unavailable by construction**. If `isolated` cannot be earned rigorously, it is a
polite word for `unconfirmed` and the contract is weaker than it reads.

Because it is paper, every claim below is sourced to documented Kubernetes
behaviour rather than to observation, and the two places where that is not enough
are marked as open questions rather than settled.

## Why this shape, and not another backend

The Docker spike could confirm termination: the daemon is reachable, containers
are enumerable, and `terminated` is obtainable. That makes it a poor test of
`isolated`, which exists precisely for the case where you cannot ask.

A node partition inverts every assumption the Docker path relies on:

| | Docker (proven) | Partitioned node |
| --- | --- | --- |
| Is the executor reachable? | Yes | **No** — that is the failure |
| Can the domain be enumerated? | Yes | Only its *last known* membership |
| Can termination be confirmed? | Yes | **Never, while partitioned** |
| Can creation be revoked at the source? | Yes — withdraw the route | **No** — the kubelet already holds what it needs |

The last row is the one that makes this worth writing down, and it is not
intuitive. In the Docker case, revocation works because the holder's authority is
*mediated in the present*: take away the socket and it can create nothing more.
A partitioned kubelet's authority is **already resident**. It holds its Pods, its
mounted volumes, and its running processes, and it needs nothing from the control
plane to keep using them. There is no route to withdraw.

## What a partition actually does

The control plane observes only that it has stopped hearing from the node. It
cannot distinguish:

1. the node is down;
2. the node is up but cannot reach the API server;
3. the node is up, cannot reach the API server, **and its workloads are still
   running and still writing to storage**.

Case 3 is the dangerous one and it is indistinguishable from the others at the
API. Kubernetes' documented behaviour reflects this honestly:

- The node controller marks the node `NotReady` after the monitoring grace period,
  then applies an unreachable taint.
- Pods on it are marked for deletion but enter a **terminating** state that does
  not complete. The API object persists precisely because nothing has confirmed
  the containers stopped.
- For a StatefulSet, the controller will **not** create a replacement Pod with the
  same identity while the original is unconfirmed, because doing so would risk two
  writers with the same identity against the same volume.

That last behaviour is Kubernetes independently arriving at ADR 0029 §7's rule.
It is the same reasoning: an unconfirmed occupant means the resource is not free.

## Where `isolated` is earned, and where it is faked

**Force deletion is not fencing.** Deleting a Pod with `--force
--grace-period=0` removes the API object immediately. It does not stop anything:
it tells the API server to forget the Pod while the kubelet, unreachable, keeps
running the containers. The cluster now believes the workload is gone, which is
strictly worse than knowing it is unconfirmed — it is the same failure shape as
v1's `StopContainer` deleting its own record after a failed stop, which the Docker
spike modelled as C5. **The API allows you to state a conclusion you have not
established.**

So force deletion maps to `unconfirmed` at best, and in practice to a false
`terminated`. It must be forbidden as a fencing primitive.

`isolated` is available, but only through a **positive act that removes the
partitioned generation's reach**, and each act has to be confirmed by a component
that is *not* the partitioned node:

| Act | Removes | Confirmed by |
| --- | --- | --- |
| Evict the node's API identity — revoke or rotate its credential, remove its authorization | The kubelet's ability to affect cluster state on reconnection | The API server, which is reachable |
| Detach or fence the storage — release the volume attachment, revoke the storage credential, or use the storage system's own fencing | The ability to keep writing to shared state | The storage system or CSI driver, which is reachable |
| Fence the network identity — withdraw the Pod IPs from service endpoints, revoke workload certificates | The ability to serve or call as the fenced generation | The control plane and mesh, which are reachable |

The pattern is worth stating plainly because it is the general lesson: **you
cannot get a receipt from the thing you cannot reach, so you must get it from
everything that thing needs.** `isolated` is not "we waited long enough." It is a
set of confirmed revocations, each obtained from a reachable component, which
together establish that the unreachable generation cannot touch anything current
or future work will touch.

Where an act cannot be confirmed — a storage system with no fencing primitive, a
credential that cannot be revoked — the receipt is `unconfirmed`, the Habitat is
quarantined, and no dispatch occurs. That is the correct outcome, not a gap.

## Three things this changes or sharpens in ADR 0029

**1. A timeout must never produce a positive receipt.** Nothing in the ADR says
it may, but nothing forbade it either, and a node-monitor grace period is exactly
the kind of timer an implementer would reach for. Elapsed time since last contact
is evidence about *communication*, not about execution. It is a reasonable trigger
for *beginning* to fence; it is never a receipt.

**2. `isolated` needs a stated evidence obligation.** The ADR defines `isolated`
by its property — the old generation cannot mutate state reachable by current or
future work — but does not say what a provider must produce to claim it. This
walkthrough supplies the shape: an enumerated set of capabilities the fenced
generation holds, and for each, a revocation confirmed by a reachable component.
A provider that cannot enumerate that set cannot claim `isolated`, for the same
reason a provider that cannot enumerate its spec closure declares it `unclosed`.

**3. The Docker capability-closure finding recurs here, and is stricter.** The
Docker spike concluded that a domain must be closed under the authority it
exposes. A partition sharpens it: a partitioned kubelet **keeps every capability
it already holds**, so closure has to be evaluated over what the domain *holds*,
not only over what it can *acquire*. A Habitat design that grants broad standing
credentials — cloud IAM, a database superuser, a shared object-store token — makes
`isolated` proportionally harder to earn, because each is one more thing that has
to be revoked and confirmed.

That is a design pressure worth naming in advance: **narrow, individually
revocable capabilities are cheaper to fence than broad standing ones**, and the
cost only becomes visible when something partitions.

## What this does not establish

Recorded so the walkthrough is not read as more than it is.

- **It is not observation.** No cluster, no induced partition, no measurement of
  what a real kubelet does at the boundary. The behaviours cited are documented
  Kubernetes semantics; a Kubernetes provider would need to verify them against a
  specific version and CSI driver.
- **It does not establish timing.** How long each revocation takes, and therefore
  how long a Habitat sits quarantined, is unmeasured.
- **Two open questions remain**, and both belong to whoever builds a Kubernetes
  provider rather than to this ADR:
  1. Whether every storage backend Maestro would plausibly use offers a fencing
     primitive that can be confirmed from outside the partitioned node. If some do
     not, those backends can only ever reach `unconfirmed`.
  2. Whether revoking a kubelet's credential is sufficient to prevent a
     reconnecting node from acting, or whether in-flight leases and cached tokens
     leave a window. This is the same question the Docker spike answered
     empirically for issued capabilities, and it deserves the same treatment
     before anyone relies on it.

## Conclusion

The three-valued receipt survives this shape, and is improved by it.

`terminated` is correctly unavailable — the ADR already says force deletion is not
confirmation, and this walkthrough explains why the API nonetheless lets you
pretend otherwise. `unconfirmed` is the correct default and Kubernetes' own
controllers behave as though it is. And `isolated` is genuinely reachable, but
only as a **conjunction of confirmed revocations obtained from reachable
components** — never as the passage of time.

Had `isolated` turned out to be unreachable except by waiting, it would have been
`unconfirmed` wearing a better name, and the honest fix would have been to delete
it from the contract. It is not, so it stays — with an evidence obligation it did
not previously carry.

## Related Documents

- [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) — §7,
  whose three-valued receipt this tests.
- [Spike: Docker Fencing Domains](spike_docker-fencing.md) — the executable
  companion, and the origin of the capability-closure rule this sharpens.
- [Pre-Phase-3 Blockers](plan_blockers.md) — item A1's bounded spike requirement:
  one reproducer, one paper walkthrough, and no further backend research.
