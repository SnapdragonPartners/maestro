+++
title = "ADR 0029: Incubator And Habitat Execution Boundaries"
edit_date = "2026-08-10"
status = "draft"
summary = "Splits the single conflated execution resource into two Orchestrator-managed types: the Incubator, a unitary Story-execution-scoped development environment with a toolchain and no ecosystem, and the Habitat, a production-shaped deployed application environment leased only for verification. They exchange nothing directly — an immutable forge commit is the sole medium — and each is fenced as a provider-created domain returning a three-valued receipt where only terminated and isolated are terminal and neither permits reuse of the fenced generation."
+++

# 0029. Incubator And Habitat Execution Boundaries

Status: **Proposed** (Claude, 2026-08-10). Item A1 of the accepted
[pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md), which scoped this
work under the single name `Habitat`. This ADR splits that resource in two and
records why; the naming reconciliation is stated explicitly in the Decision
rather than left for readers to infer. Accepted before
[ADR backlog candidate 13](../v2/notes_adr-backlog.md) (the agent execution
contract), which consumes it.

## Context

### What v1 actually has

v1 has one execution world per agent, and it is both the development environment
and the dependent-service environment at once. Two lines of code carry the
conflation:

- **`pkg/tools/registry.go:385`** — *"Get the agent's container name so
  `compose_up` can attach it to the compose network."* The agent's development
  container is joined directly to the Compose project's network, so it can reach
  every service in it.
- **`pkg/exec/docker_long_running.go:243`** — the raw host Docker socket is
  mounted into **every** long-running container unconditionally
  (`--volume /var/run/docker.sock:/var/run/docker.sock`), not only in Claude Code
  mode.

The Compose half is driven by the agent through a nine-tool family
(`compose_up`, `compose_down`, `compose_status`, `compose_logs`, `compose_read`,
`compose_write`, `compose_validate`, `compose_add_service`,
`compose_remove_service`) alongside five container tools (`container_build`,
`container_update`, `container_switch`, `container_list`, `container_test`).
`pkg/demo/stack.go` stands up the same world for demo mode, which the roadmap
names as the eventual foundation for UAT.

So the resource this ADR governs is not new. It exists, it is already both
things, and Phase 3 is where it gets cut.

### Why one resource does not work

Three reasons, in increasing order of how binding they are.

**Cost and cardinality.** Development containers are cheap and every Story under
development wants one. A production-shaped environment for a real application can
be many containers and many service types; running several concurrently is
infeasible on a local device and expensive in the cloud. One resource type forces
one capacity limit over two populations whose economics differ by an order of
magnitude.

**Lifetime.** The production-shaped environment is needed during integration
testing and UAT. Holding it through planning and development is convenient and
wasteful — it is idle for most of a Story's life.

**Definition ownership, which is the reason that is not negotiable.** Production
environment definitions — Terraform, Compose, Kubernetes manifests — are
artifacts Maestro does not own. They are the same files used for real production
deployment, and they will not contain a development container. Maestro must not
add one, because that would leave a laggard development container in production
definitions. The first two reasons could in principle be answered by leasing
discipline. This one cannot: the two things must be *separately defined* because
one of the definitions is not ours to write.

### What this ADR must satisfy

The blocker plan's item A1 fixed a set of requirements under the name `Habitat`:
identity and spec-versus-instance, generation and fencing, lifecycle, the
fencing protocol and its three-valued receipt, agent cardinality including
read-only Architect inspection, restart and reconciliation, and the rule that
Maestro's tools target a resource reference rather than an Agent-derived local
path. Those requirements are unchanged. What changes is which of the two
resources each attaches to.

## Decision

### 1. Two resource types, and the naming reconciliation

Maestro manages two independent Orchestrator-owned execution-resource types.

**Incubator** — a unitary development environment: a writable source workspace,
the toolchain, and a command executor. It has **no ecosystem**: no databases, no
queues, no dependent services. It is normally a container; it may be a
filesystem-backed environment where containerization is rejected by the platform
(macOS-native and mobile development), but a filesystem alone is not an
Incubator — a native command and process executor is mandatory.

**Habitat** — a production-shaped deployed application environment: application
services, dependent services, provisioned infrastructure, the deployed candidate,
runtime configuration, and the surfaces integration verification and UAT need.

**The naming reconciliation is explicit.** The blocker plan, [issue #273](https://github.com/SnapdragonPartners/maestro/issues/273),
and [ADR backlog slot 11](../v2/notes_adr-backlog.md) use `Habitat` for the
single conflated resource, and every *concrete* requirement they state — the
tool-routing rule, read-only Architect inspection, the removal of Coder workspace
bind-mounts, the Docker socket escape, the Docker/Compose gating row of the
fencing compatibility matrix — describes what this ADR calls the **Incubator**.
Readers of those documents must map accordingly. `Habitat` is retained for the
deployed environment because that is the resource for which the word is
descriptive; the alternative, renaming the deployed environment and leaving
`Habitat` on the development resource, was considered and rejected as the less
accurate of two imperfect options.

### 2. Scope, ownership, and cardinality

**The Incubator is scoped to the Story execution, not to the Agent principal.**
This is a change from v1, where the development container belongs to the agent.
Agent restart or replacement therefore preserves the work, and a replacement
agent resumes the same execution — which is what makes single-owner agent restart
([#265](https://github.com/SnapdragonPartners/maestro/issues/265)) a lifecycle
question rather than a data-loss question.

- One exclusive writer lease per Story execution.
- Read-only inspection capabilities may coexist with the writer lease.
- **Architect inspection uses a read-only capability**, never a bind mount of the
  Coder's workspace root. This is what permanently removes inode preservation
  from the cross-agent contract (CLAUDE.md's bind-mounted workspace invariant,
  [ADR 0027](0027-concurrency-safety-for-shared-local-infrastructure.md)).
- **After candidate submission, review reads the immutable commit, not the
  Incubator.** A concurrently mutating view is the wrong thing to review;
  forge-mediation (§4) means there is always a better one available.

**The Habitat is leased exclusively for verification and released immediately
after.** Compatible executions queue when capacity is exhausted. The Orchestrator
assigns leases; agents never claim one. The leasing agent need not remain active
while deterministic build, deployment, and test steps wait for capacity.

**Capacity is limited per resource type, never globally.** A deployment may
support ten concurrent Incubators and two Habitats. A single limit over both
populations is a defect, because it prices a development container as if it were
a production-shaped environment.

### 3. The no-direct-channel invariant

**An Incubator and a Habitat have no direct route to one another.** No shared
network, no issued service credentials, no tunnel, no connection manifest. The
agent requests verification; the Orchestrator performs it; the agent receives
results as data.

This deletes `registry.go:385` — the Compose-network attachment — rather than
re-implementing it behind a capability.

The invariant is load-bearing for fencing, not merely tidy. A credential that
lets an Incubator mutate a Habitat's services *is* reach into another fencing
domain, and an Incubator holding one could not honestly be reported `isolated`
while it remained valid. That is structurally the same escape shape as the Docker
socket at `docker_long_running.go:243`, except deliberate. With no channel, the
two domains are genuinely independent and fencing composes without chasing
outstanding capabilities.

**A convenience channel will be proposed again** — running integration tests from
the Incubator against a live Habitat is an obvious-looking shortcut. It is
prohibited. Verification executes in the Habitat (§7).

### 4. The forge commit is the only medium of exchange

The neutral handoff between the two resources is an **immutable forge commit**.
The Incubator produces one; the Habitat consumes one. Nothing else crosses.

This is chosen because a forge is the one integration point essentially every
deployment mechanism already speaks, so existing deployment definitions work
largely unchanged, and because it makes the exchanged thing immutable and
addressable by construction. Binary artifacts stay out of Git — they live in OCI
registries, package registries, or object storage, and are referenced by digest.

**Both halves of the promoted commit matter.** The commit carries the application
source *and* the Habitat definition — the environment is habitat-as-code.
Promotion advances both together.

`HabitatSpec` is therefore **derived, not registered**: the spec identity is the
digest of the Habitat definition files at the promoted commit. There is no
separate spec object to keep in sync with the repository. `HabitatInstance` is
the provisioned, mutable resource with a stable ID, generation, provider
reference, and lifecycle state. The same derivation applies to the Incubator: its
definition (Dockerfile, devcontainer spec, or equivalent) lives in the commit and
its spec identity is that definition's digest.

### 5. Two clocks

A Habitat carries two independent counters, and the distinction between them is
supplied by the digest comparison in §4:

| Counter | Advances when | Is |
| --- | --- | --- |
| **Habitat generation** | The Habitat definition digest changes | The infrastructure identity, and **the fencing unit** |
| **Deployment generation** | Only the application source changed | Which commit is running inside that infrastructure |

One lease spans many deployment generations. Without this split every commit
advance would be a new Habitat generation — a new fencing domain, an invalidated
lease, and re-leasing per iteration, which is the expensive step rather than the
cheap one. It is also required for evidence: a verification receipt must be able
to say whether the environment or the code changed, and one counter cannot.

**MVP performs the same physical action for both** — tear down and rebuild — and
records which clock advanced. The distinction is bookkeeping now and a
convergence trigger later.

### 6. Reset, and why in-place convergence is a correctness trade

Both resource types expose the same reset contract, and for MVP both providers
may always answer `reprovision_required`. The Orchestrator then fences the
existing generation, preserves required evidence, destroys or permanently
quarantines it, provisions a clean replacement, increments the generation, and
verifies readiness before leasing.

Terraform, Compose, and Kubernetes all support incremental convergence, and it is
tempting to treat teardown-and-rebuild as a placeholder for it. It is not.
**Declarative convergence converges the declaration, not the contents**:

- `compose up` on a `build:` service uses the image that already exists — it does
  not rebuild without `--build`, and a pull-through `:latest` is not re-pulled
  without `--pull always`.
- `kubectl apply` with an unchanged `:latest` tag produces no rollout at all: the
  Deployment spec did not change, so no new ReplicaSet is created.
- Terraform does not manage the data inside what it provisioned.

The image half is fixable by pinning digests, which is required anyway. The state
half is not: **named volumes survive `up`, so data from one verification run
leaks into the next.** For a verification environment that is a false-green
generator — a test that passes on residue from the previous iteration.

Teardown-and-rebuild is therefore correct by construction and is the MVP choice
for that reason, not merely for simplicity. Any future in-place path MUST state
what is reset between verification runs even when the infrastructure is not
reprovisioned. It buys latency by spending a correctness guarantee.

Repository-defined `make clean` is not evidence of reset: it does not cover
processes, untracked files, credentials, databases, volumes, or service state.
Content-addressed caches may survive a reset only where they cannot carry mutable
work or secrets between executions.

### 7. Fencing

The fencing contract is **generic over resource type**. Both Incubators and
Habitats are fenced by the same protocol, each in its own domain.

**The protocol.**

1. **Cooperative cancellation with a bounded grace period.** The lease holder is
   asked to stop and given a defined window to reach a safe boundary.
2. **Provider-enforced fencing of the whole domain** once the grace period
   expires, proving **non-interference**. A provider that cannot produce a
   positive receipt is not conformant; best-effort success is forbidden.
3. **Quarantine when fencing cannot be confirmed.** Not knowing whether the old
   generation can still act is the dangerous case and needs a defined resting
   state, not an assumption.
4. **No reassignment and no fresh dispatch** into that resource until fencing is
   acknowledged. A resource with an unconfirmed occupant is not free.
5. **Generation fencing for late calls**, so a call issued by a fenced holder is
   rejected at the boundary even if it arrives after fencing.

**The unit is a provider-created domain, never a process tree.** Process ancestry
is not a portable containment boundary: Docker socket access creates sibling
containers outside the PID tree, a Kubernetes agent can create sibling Pods, and
`chroot` provides no process isolation at all. Each provider MUST create an
immutable `FencingDomainID` and generation *before* execution starts, and MUST
guarantee that every process or subordinate resource able to mutate the resource
is contained in that domain. If one lease cannot be isolated, fencing covers
every lease sharing the domain; say so rather than discovering it.

**The receipt proves non-interference, not death.**

| Receipt | Meaning | Terminal? | Reuse |
| --- | --- | --- | --- |
| `terminated` | The execution domain is confirmed stopped. | Yes | The resource is free once reprovisioned into a new generation. |
| `isolated` | The old generation is permanently quarantined, its capabilities revoked, unable to mutate state reachable by any current or future generation — even if cleanup continues asynchronously. | Yes | **Never.** Fresh work receives a new generation or a new resource. |
| `unconfirmed` | Neither could be established. | **No** | **None** — quarantine, no dispatch. |

**An `isolated` receipt is terminal but never permits reuse.** It says the old
generation can no longer reach anything current or future work touches; it does
not say the resource is free, and cleanup may still be running. Fresh work always
dispatches into a **new generation**. Reading a positive receipt as licence to
reuse the fenced generation would reintroduce exactly the interference the
receipt rules out.

**Docker and Compose use the `terminated` path, and it is the only path Phase 3
implements.** `isolated` exists so a future partitioned Kubernetes, macOS, or
Terraform-provisioned backend has a conformant answer that does not require
synchronous confirmation of death.

**Three additional rules the protocol needs and did not previously state:**

- **Capabilities carry their issuing generation.** Requirement 5 generalizes:
  generation fencing applies to every capability issued out of a domain, not only
  to mediated calls back to the Orchestrator. Either `Fence()` revokes issued
  capabilities as part of producing its receipt, or the far side rejects on
  generation mismatch. §3 keeps this cheap by ensuring no capability crosses
  between the two resource types at all.
- **Fencing is execution-scoped, not resource-scoped, when an execution is being
  terminated.** Cancelling coding fences the Incubator; cancelling verification
  fences the Habitat lease; neither implies the other. But an *execution* being
  recorded terminal — the amendment-versus-running-work case — requires a
  positive receipt from **every** domain that execution holds. Otherwise a
  superseded candidate keeps running in a Habitat and verification keeps
  reporting against it, which is a false terminal record.
- **An `unconfirmed` fence MUST NOT delete the provider-reference record.**
  Deleting it destroys the evidence anything downstream would reconcile against.
  This is exactly the v1 failure at `pkg/exec/docker_long_running.go:356-380`,
  where `StopContainer` swallows both `docker stop` and `docker rm -f` failures,
  removes the container from `activeContainers` and the global registry, and
  returns `nil` — so a failed stop leaves no record that the container exists.

### 8. Tool routing, and contracts declare where they run

**Maestro's tools target a resource reference, never an Agent-derived local
path.** This binds Maestro's own agents. It is not a claim of control over
arbitrary processes inside a resource: an engineer may legitimately run other
agents there, and the application under development may itself be an agent.

**Every execution contract declares the resource it executes in.** This is what
makes §3 enforceable rather than aspirational:

- **Incubator contracts** — build, lint, unit test. No ecosystem required.
- **Habitat contracts** — deploy, integration test. Ecosystem required.

An integration-test contract cannot quietly run in the Incubator and reach the
Habitat, because the Incubator has no route. It also follows that **the Habitat
definition must include somewhere to execute its own verification** — a test
runner is part of the environment, not something reaching in from outside.

The contract set itself, its invocation and result shape, and the reconciliation
of a `deploy` verb with v1's existing `Run` are **Phase 3 plan material, not part
of this ADR**. See [notes_execution-contracts.md](../v2/phase_3/notes_execution-contracts.md).

### 9. Degenerate cases

**No Habitat definition.** A new or simple application may have none. It develops
and verifies entirely in its Incubator and publishes its commit. Record this as
**development-equivalent verification**, distinct from production-shaped
verification, so the evidence does not overclaim. Creating a Habitat definition
may itself become later Story work. Per the roadmap's Phase 3 exit criteria — one
Epic from intake through Story execution to merged Story branches on a fixture
repo — **this is the normal Phase 3 case**, not an edge case.

**Physically identical.** One environment may implement both roles for a simple
application, analogous to build and run stages of a container image. The roles
and their receipts stay distinct even when the implementation collapses them:
source and build identity, deployment identity, and verification evidence remain
separately recorded.

## Consequences

- **The agent tool surface shrinks substantially.** Of the fourteen container and
  compose tools in v1: the four lifecycle tools (`compose_up/down/status/logs`)
  become Orchestrator-owned and disappear from the agent; the five definition
  tools (`compose_read/write/add_service/remove_service/validate`) are file edits
  to a file the agent can already edit, of which only a cheap validate is worth
  keeping so a bad definition fails before it burns a lease; and the container
  tools collapse into definition edits plus one reprovision verb. Roughly three
  agent-visible tools replace fourteen. This is a simplification, not new
  machinery.
- **Deterministic lifecycle machinery moves from the agent to the Orchestrator**,
  which is the direction [ADR 0019](0019-orchestrator-boundary.md) requires:
  leasing, provisioning, deploying, and fencing involve no inference.
- **Environment mutation becomes reviewable.** Because the Incubator definition
  lives in the commit, adding a library or changing the toolchain is a diff
  rather than invisible drift. v1's imperative container mutation left the
  container diverged from its Dockerfile, and the divergence died on reprovision.
  Whether the agent may imperatively mutate a running Incubator for speed is
  deferred; if it is ever allowed it must be explicitly non-durable, so an
  omission from the definition surfaces at promotion rather than after merge.
- **Verification costs a forge round trip.** Write test, commit, deploy, run. The
  agent is not burning tokens while it waits — these are deterministic steps with
  no LLM in the loop — so the cost is wall-clock on an unattended step. The clock
  split (§5) keeps the expensive part, leasing and provisioning, off the
  per-iteration path.
- **The real risk is throughput, not latency.** With many Incubators and few
  Habitats, stories serialize behind Habitat capacity. This is what makes the
  short-lease rule in §2 load-bearing and what per-type capacity limits have to be
  set against.
- **Provenance records both identities.** A promotion binds Story and source
  commit, Incubator identity and generation, the environment digests of both
  resources, artifact digests and locations, Habitat identity, spec digest,
  Habitat generation *and* deployment generation, deployment result, and
  verification evidence. Any durable promotion record is an artifact and routes
  through [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md)'s envelope
  and payload type registry and [ADR 0021](0021-artifacts-and-principal-instances.md)'s
  Management/Audit split — never a bespoke record shape.
- **Persistence is a Phase 3 migration.** The Habitat and Incubator families are
  created in Phase 3, as prompt packs already are; Phase 2 is closed and
  migrations are additive. Provider-specific detail — Docker container IDs, host
  paths, Compose project names — stays out of provider-neutral schema.
- **`unconfirmed` is expected to be rare and must not be made convenient.** The
  quarantine path costs a resource until an operator or reconciliation clears it.
  That cost is the point; making `unconfirmed` cheap by treating it as a soft
  failure would return the system to best-effort fencing.

### Deferred

Kubernetes, Terraform/OpenTofu and macOS Habitat providers; non-container
Incubator providers; remote build execution; mandatory hermetic builds; warm
pools and optimized reset; in-place convergence; concurrent writers sharing one
Habitat; dedicated verification resources; browser and device orchestration; UAT
gate policy and presentation; production deployment; registry and vendor
selection; multi-repository deployment bundles; provider-specific rollback
machinery; and inference of deployment definitions Maestro was not given.

The promotion state machine, build and deployment receipts, and artifact-digest
plumbing are **Phase 4 evidence-package work**, not part of this ADR.

## Spike Evidence

Two artifacts are required before acceptance, and deliberately no more. The
backend list is open-ended and chasing it would turn a design ADR into a research
project.

1. **An executable Docker/Compose reproducer** demonstrating the socket escape at
   `pkg/exec/docker_long_running.go:243` and showing that domain-based fencing
   catches the sibling container descendant-walking misses. Spike code: it lands
   outside `pkg/`, `internal/`, and `cmd/`, and anything preserved goes under
   `spikes/phase_3/`.
2. **A paper walkthrough of a Kubernetes node partition** — no cluster required —
   as the materially different failure shape where confirmed termination is
   unavailable and the `isolated` receipt carries the weight.

Everything in the fencing compatibility matrix of the
[blocker plan](../v2/phase_3/plan_blockers.md) beyond these is a non-gating
example.

## Authority Reconciliation On Acceptance

The split narrows what `Habitat` means in every document written before it. The
full list was established by grepping the *concept*, not the word — two of the six
locations never say `Habitat` at all, and a line-by-line fix would have missed
them. None of these are edited while this ADR is Proposed; all of them land in the
final reviewed commit.

| Location | Change |
| --- | --- |
| [ADR backlog](../v2/notes_adr-backlog.md) slot 11 | Mark **RESOLVED**, pointing here. The slot keeps its number. |
| [ADR backlog](../v2/notes_adr-backlog.md) slots 3, 4, 13 | Their `Habitat` references mean the Incubator in the fencing and containment sentences. |
| [Pre-Phase-3 Blockers](../v2/phase_3/plan_blockers.md), item A1 | Record the split and the resource each A1 requirement attaches to. A5's fencing dependency becomes execution-scoped per §7. |
| [Parking lot](../v2/notes_parking-lot.md) | The graduation pointer names both resources. |
| [Roadmap](../v2/plan_roadmap.md), line 867 | Phase 3's `Epic-scoped workspace` output — **does not contain the word `Habitat`**; #273's documentation-impact section already required this amendment. |
| [v1 port inventory](../v2/phase_0/inventory_v1-port.md), rows for `pkg/workspace`, `pkg/exec`, `pkg/tools` | Dispositions for workspace, execution, tools, and container runtime state — **does not contain the word `Habitat`**. Note `pkg/workspace` is already keyed by repo + Story/run, which is consistent with Incubator scoping. |
| [Issue #273](https://github.com/SnapdragonPartners/maestro/issues/273) | The tracker copy, already amended three times. Its sections 3, 3a and 3b carry the fencing protocol and must reflect the split. |

## Related Documents

- [Pre-Phase-3 Blockers](../v2/phase_3/plan_blockers.md) — item A1, which scoped
  this ADR and fixed the fencing protocol, the three-valued receipt, and the
  bounded spike.
- [ADR backlog](../v2/notes_adr-backlog.md) — slot 11, marked RESOLVED when this
  ADR is Accepted; slot 13 (the agent execution contract) consumes it.
- [Issue #273](https://github.com/SnapdragonPartners/maestro/issues/273) — the
  originating issue and its three amendments.
- [ADR 0019: Orchestrator Boundary](0019-orchestrator-boundary.md) — leasing,
  provisioning, deployment, and fencing are deterministic machinery; the
  amendment-versus-running-work policy layers on §7's fencing.
- [ADR 0021: Artifacts And Principal Instances](0021-artifacts-and-principal-instances.md)
  and [ADR 0028: Artifact Envelopes And Payload Schemas](0028-artifact-envelopes-and-payload-schemas.md)
  — where promotion and verification records must live.
- [ADR 0022: v2 Data Plane](0022-v2-data-plane.md) — the persistence seam the
  Phase 3 migration goes through.
- [ADR 0023: v2 Branch Strategy](0023-v2-branch-strategy.md) — the branch model
  the promoted commit sits in.
- [ADR 0027: Concurrency Safety For Shared Local Infrastructure](0027-concurrency-safety-for-shared-local-infrastructure.md)
  — the bind-mount inode hazard read-only Architect inspection removes.
- [notes_execution-contracts.md](../v2/phase_3/notes_execution-contracts.md) —
  the contract-set design this ADR deliberately excludes.
