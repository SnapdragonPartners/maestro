+++
title = "Spike: Fencing A Partitioned Kubernetes Node"
edit_date = "2026-08-12"
status = "draft"
type = "spike"
summary = "The paper walkthrough ADR 0029 requires: a node partition is the case where confirmed termination is unavailable by construction, and it makes the `isolated` receipt conditional — available only when every path by which the fenced generation can reach state current or future work will touch is either closed by the authority enforcing that path or leads to a permanently abandoned target, and `unconfirmed` otherwise. Deliberately not 'every capability becomes unusable', which would collapse isolated into terminated: an isolated generation may keep running and keep writing to state nothing will read again. A generic Kubernetes Habitat fails the condition on all three shared-state paths, documented against primary sources: no client-certificate revocation exists, orchestration-level volume detach can leave a live writer, and network policy need not close established connections. Force deletion is forbidden as a fencing primitive because it lets the API state a conclusion nothing established, and the Docker capability-closure finding recurs more strictly, since a partitioned executor keeps every capability it already holds."
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

Because it is paper, every behavioural claim below is **linked to primary
Kubernetes documentation** rather than observed. An earlier draft asserted that
sourcing without providing a single link, which is the same defect this document
goes on to describe — a claim wider than its evidence. Where the documentation
does not settle a question, it is recorded as open rather than resolved by
inference.

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
API. Kubernetes records this honestly in the node's own condition:

- When the node controller stops receiving heartbeats, it sets the node's `Ready`
  condition to **`Unknown`** — not `False`. `False` would assert the node is not
  ready; `Unknown` asserts nothing, which is the accurate statement. (`kubectl`
  displays this as `NotReady`, which is where an earlier draft of this document
  got it wrong. The display string and the condition are different things, and
  only the condition carries the epistemic content.)
  [Nodes: conditions](https://kubernetes.io/docs/concepts/architecture/nodes/)
- Pods on the node are marked for deletion but remain **terminating**. The API
  object persists precisely because nothing has confirmed the containers stopped.
- For a StatefulSet, a replacement Pod with the same identity is **not** created
  while the original is unconfirmed, because that would risk two writers with the
  same identity against the same volume. Kubernetes documents force-deleting such
  a Pod as breaking its at-most-one guarantee.
  [Force delete StatefulSet Pods](https://kubernetes.io/docs/tasks/run-application/force-delete-stateful-set-pod/)

**Narrower than an earlier draft claimed.** That draft said Kubernetes' own
controllers "behave as though `unconfirmed` is the correct default." The
StatefulSet case does. The **force-detach path does not**: the attach/detach
controller will forcibly detach a volume from an unreachable node after a
timeout, which can detach from a node whose kubelet is still writing.
Kubernetes itself flags this as risky and gates safer behaviour behind
non-graceful shutdown handling that requires an operator to assert the node is
really out.
[Non-graceful node shutdown](https://kubernetes.io/docs/concepts/cluster-administration/node-shutdown/)

So the honest reading is: one controller embodies the rule, another trades it for
availability, and the second is a caution rather than a precedent.

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

`isolated` requires a **positive act that removes the partitioned generation's
reach**, confirmed by something other than the partitioned node. The candidate
acts are below — and the important word is *candidate*. An earlier draft
presented these three as receipts. **None of them is generically sufficient**, and
saying otherwise would have been the same error the Docker spike kept making: a
claim wider than its evidence.

| Candidate act | What it does **not** establish |
| --- | --- |
| **Positively deny the node identity at the API server** — a provider-specific authorization change, confirmable per request | Two things this is *not*. It is not credential revocation: **Kubernetes has no revocation mechanism for client certificates** — no CRL, no OCSP — and the hardening guide's answer is short lifetimes instead. [Authentication hardening](https://kubernetes.io/docs/concepts/security/hardening-guide/authentication-mechanisms/) And it is not simply "delete the RBAC bindings": kubelets are commonly authorized by the **Node authorizer** against their `system:node:<name>` identity rather than through a removable binding, so what denial looks like depends on the cluster's authorization configuration. [Node authorization](https://kubernetes.io/docs/reference/access-authn-authz/node/) Where a positive denial *is* configurable it is enforced on every request and therefore confirmable — but it only closes the path to **cluster state**; the kubelet needs nothing from the control plane to keep running the containers it already has. |
| **Detach the volume** | **Forced detach can leave a live writer**, which is data corruption rather than fencing. Detaching at the orchestration layer does not stop a process mid-write on an unreachable node. What is actually sufficient is fencing at the **storage system** — a reservation change, a client-access revocation, a CSI driver that supports fencing — and whether that exists is backend-specific. [Non-graceful node shutdown](https://kubernetes.io/docs/concepts/cluster-administration/node-shutdown/) |
| **Withdraw the network identity** (remove Pod IPs from Service endpoints, apply a deny NetworkPolicy) | NetworkPolicy governs whether connections are **allowed**; implementations are not required to terminate connections already established, and endpoint withdrawal stops new routing rather than existing sockets. A fenced generation holding an open connection may keep using it. [Network policies](https://kubernetes.io/docs/concepts/services-networking/network-policies/) |

The pattern still holds, and is worth stating plainly: **you cannot get a receipt
from the thing you cannot reach, so you must get it from everything that thing
depends on.** Two corollaries, and the second corrects an over-strong version of
this document:

> A **path into shared state** is closed when the authority that enforces that
> path confirms the old generation can no longer use it — not the orchestration
> layer that *requested* the change, but the component that actually decides
> whether the path works.

> **Or the target is abandoned.** A path needs no revoking if nothing current or
> future will ever read what lies at the end of it.

That second branch matters, and an earlier version of this walkthrough omitted
it — demanding instead that every capability the generation holds become
unusable. That is approximately termination, and it would have collapsed
`isolated` into `terminated`. **An `isolated` generation may keep running and may
keep writing**; what it may not do is reach anything current or future work will
touch. A partitioned node still scribbling on a local disk that will never be
read again is *isolated*, not merely tolerated.

The API server is the authority for cluster-state access. The storage system, not
Kubernetes, is the authority for writes to shared volumes. The dataplane, not the
NetworkPolicy object, is the authority for established connections.

**Where a path can be neither closed by its authority nor abandoned, the receipt
is `unconfirmed`** — the Habitat is quarantined and no dispatch occurs. On present
evidence that is the *likely* outcome for a generic Kubernetes Habitat rather
than an edge case.

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
  2. Whether **removing authorization** is sufficient to prevent a reconnecting
     node from acting, or whether in-flight leases and cached state leave a
     window. Note this is deliberately not "revoking its credential": Kubernetes
     has no client-certificate revocation, so authorization is the mechanism that
     exists. The Docker spike answered the analogous question empirically for
     issued capabilities, and this deserves the same treatment before anyone
     relies on it.

## Conclusion — conditional, deliberately

The three-valued receipt survives this shape and is improved by it, but the
result is narrower than an earlier draft claimed.

**`terminated` is correctly unavailable** while the node is unreachable. The API
nonetheless lets you state it anyway, via force deletion, which is why the ADR
forbids that as a fencing primitive.

**`unconfirmed` is the correct default**, and it is what a conforming provider
returns unless it can do better. Kubernetes partly agrees with itself here: the
StatefulSet identity rule embodies it, the force-detach timeout trades it away.

**`isolated` is conditionally available**, and the condition is the point:

> `isolated` may be returned **only when every path by which the fenced
> generation can reach state that current or future work will touch is either
> closed by the authority enforcing it, or leads to a target permanently
> abandoned.** Any path that is neither yields `unconfirmed`.

Deliberately *not* "every capability becomes unusable" — that is approximately
termination, and an isolated generation may keep running and keep writing to
state nothing will read again.

On present evidence a generic Kubernetes Habitat does **not** meet that condition
for its shared-state paths: client certificates cannot be individually revoked,
orchestration-level volume detach can leave a live writer, and network changes
need not close established connections. `isolated` is therefore reachable only by
choosing backends whose authorities can confirm — a storage system with real
fencing, a configurable positive denial of the node identity, a dataplane that
terminates connections — or by arranging that what the fenced generation can
still reach is never read again.

That is a stronger outcome than "isolated works here." It says the receipt is
sound *and* tells a future provider what it must buy to earn it.

Had `isolated` turned out to be reachable only by waiting, it would have been
`unconfirmed` wearing a better name and the honest fix would have been to delete
it from the contract. It survives — with an evidence obligation it did not
previously carry, and an explicit admission that the obligation is often unmet.

## Related Documents

- [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) — §7,
  whose three-valued receipt this tests.
- [Spike: Docker Fencing Domains](spike_docker-fencing.md) — the executable
  companion, and the origin of the capability-closure rule this sharpens.
- [Pre-Phase-3 Blockers](plan_blockers.md) — item A1's bounded spike requirement:
  one reproducer, one paper walkthrough, and no further backend research.
