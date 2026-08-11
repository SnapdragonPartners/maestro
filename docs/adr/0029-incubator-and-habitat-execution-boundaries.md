+++
title = "ADR 0029: Incubator And Habitat Execution Boundaries"
edit_date = "2026-08-11"
status = "draft"
summary = "Splits the single conflated execution resource into two Orchestrator-managed types: the Incubator, a unitary Story-scoped development environment carrying a toolchain and no ecosystem because it must also be implementable on platforms that reject containers, and the Habitat, a deployed application environment holding every Maestro-managed dependent service. Source and definitions cross only as an immutable forge commit; spec identity closes over the inputs describing the environment while the inputs producing the candidate stay in a separate deployment closure, with runtime configuration as a third axis carrying its own lifecycle; specification, instance, and deployment are distinct identities, as are the lease that authorizes and the retention claim that keeps an environment warm; contracts route by what they require rather than what they are named; reset is demanded before evidence-bearing verification and on transfer of ownership, and must prove namespacing or removal rather than assume teardown suffices; and both types are fenced as provider-created domains whose three-valued receipt is constrained by any reach the fenced generation retains."
+++

# 0029. Incubator And Habitat Execution Boundaries

Status: **Proposed** (Claude, 2026-08-10; revised 2026-08-11 after Codex review
round 1). Item A1 of the accepted
[pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md), which scoped this
work under the single name `Habitat`. This ADR splits that resource in two and
records why; the naming reconciliation is stated explicitly in the Decision
rather than left for readers to infer. Accepted before
[ADR backlog candidate 13](../v2/notes_adr-backlog.md) (the agent execution
contract), which consumes it.

**Round 1 (Codex, 2026-08-11) raised six blockers, all applied.** The split and
§7's three protocol extensions were confirmed sound; the defects were in what the
draft claimed around them. Recorded because four of the six share one shape — *a
correct argument not carried to its own conclusion*:

1. §1 mapped **every** prior requirement to the Incubator. Dependent-service
   lifecycle belongs to the Habitat, and identity and fencing belong to both. Now
   an explicit three-way table.
2. §4's spec digest closed over "the definition files" — insufficient, since equal
   digests could provision different environments. Now a declared dependency
   closure with an `unclosed` declaration where a provider cannot enumerate it.
3. §5 made one counter mean two things: definition-derived *and* incremented on
   reprovision. Now three identities — spec, instance, deployment.
4. §6 argued at length that volumes survive `up`, then concluded teardown is
   "correct by construction" — while `down` does not remove named volumes without
   `-v` and never removes external ones. The draft contradicted its own argument
   one paragraph later.
5. §1's categorical no-ecosystem rule had no stated answer for database-backed
   development. Now decided, with the reasoning and the two rejected alternatives.
6. §9 permitted a physically identical instance, which cannot satisfy §3 and §7
   simultaneously. Now shared *implementation*, never a shared instance.

Two non-blocking wording corrections applied: the forge commit is the sole
**source and definition** handoff, since digest-addressed binaries also cross;
and verification runs on a **provider-launched executor inside the Habitat's
domain**, so the rule does not require editing production deployment definitions
— which §1 forbids.

**Round 2 (Codex, 2026-08-11) raised six more, all applied.** Five are the same
shape as round 1's dominant failure, and the shape is worth naming because it has
now produced eleven of the fourteen findings across both rounds: **a rule stated
correctly in one section and not carried to an adjacent one.**

1. §2 conflated authorization with capacity retention — the ADR said an idle
   execution never holds a lease while the companion notes let one hold it
   indefinitely. Now three things: instance, generation-bound lease, retention
   claim.
2. §5 said MVP advances the instance generation on every deployment, which made
   the Consequences claim that provisioning stays off the iteration path false.
   Resolved by §6's new rule rather than by conceding the cost — see below.
3. §7's external-state caveat **weakened an accepted invariant**. `isolated` means
   the generation cannot affect state reachable by current or future work,
   irrespective of who provisioned it. Unrevokable external reach now removes
   `isolated` from the available receipts rather than narrowing what it claims.
4. §4 said a rotated secret does not change the spec, which is false where a
   credential points somewhere else. Now a separately recorded
   runtime-configuration revision — *not* secret versions folded into
   `SpecDigest`, which would make rotation force reprovisioning.
5. §8 routed contracts by name (build/lint/test → Incubator), contradicting §1's
   decision that a database-backed repository's tests run against a Habitat. Now
   routed by requirement.
6. The companion notes retained the superseded test-runner requirement corrected
   in §8 during round 1 — a stale copy in a document authored alongside the fix.

**The substantive new decision is §6's reset rule**, which resolves #2 without
conceding the cost: reset is required before **evidence-bearing** verification,
not before every run. The rebuild-per-iteration problem was self-inflicted by an
over-broad reset rule, not inherited from missing capability — `compose up
--build` already redeploys application code without disturbing a running database.

**Round 3 (Codex, 2026-08-11) confirmed the three contested resolutions and
raised four more, all applied.** Two are consequences of round 2's own changes,
which is the expected cost of a structural revision; two are older defects the
new structure exposed.

1. **Reset at ownership transfer** (§6). Round 2's persistent iteration state is
   safe only while one Story owns the instance. Reclaiming a retention claim for
   a different Story must fence and reset **before any run**, including one the
   arriving Story requests as an iteration — otherwise it inherits the displaced
   Story's code, database, and service state. That is the cross-lifecycle
   contamination [ADR 0027](0027-concurrency-safety-for-shared-local-infrastructure.md)
   forbids, arriving through a door §6 left open by assuming an instance's history
   belonged to whoever held it. It also collapses the reclamation cost model:
   reclamation transfers **capacity, never warmth**.
2. **Spec closure conflated infrastructure with application source** (§4). A
   Compose `build:` context is commonly the repository root, so the round-2
   closure would have made every application commit a spec change — advancing the
   instance generation per commit and silently reinstating the reprovisioning
   round 2 had just removed. Now two closures, divided by whether an input
   *describes the environment* or *produces the candidate*.
3. **Lease terminology stale after retention was introduced** (§2 and the notes).
   Reclamation was still described as a lease event that merely deauthorizes,
   when it now acts on a retention claim and does trigger fencing and reset.
4. **The configuration axis was recorded but not applied** (§4, §5). Now carries
   lifecycle semantics — resolve and snapshot once per deployment, converge or
   declare unknown on change, bind verification to the revision actually loaded —
   and §4's "equal digests" claim is corrected: the inference needs `SpecDigest`
   **and** `RuntimeConfigurationRevision` together.

`isolated` is settled as of this round, so the Kubernetes walkthrough is
unparked.

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
the toolchain, and a command executor. It is normally a container; it may be a
filesystem-backed environment where containerization is rejected by the platform
(macOS-native and mobile development), but a filesystem alone is not an
Incubator — a native command and process executor is mandatory.

**Habitat** — a production-shaped deployed application environment: application
services, **dependent services**, provisioned infrastructure, the deployed
candidate, runtime configuration, and the surfaces integration verification and
UAT need.

#### The Incubator owns no ecosystem, and the reason is not fastidiousness

An Incubator has no databases, no queues, and no dependent services. Every
Maestro-managed service belongs to a Habitat.

The decisive argument is not the slippery slope from a database to a cache to
everything. It is that **the Incubator has more than one implementation shape**.
A filesystem-backed Incubator on macOS, for a native or mobile application,
cannot host a Compose ecosystem. If Incubators may own dependent services, that
implementation silently becomes second-class — unable to run the dev dependencies
of any repository that has them — and the abstraction fails precisely at the
backend it was widened to admit. Commonality of the database case does not make
macOS able to run Postgres in an Incubator.

**The consequence is not "supply it externally."** A repository whose development
needs a database has a **Habitat, and a small one**. Habitats span two orders of
magnitude: a large application's is many services and genuinely expensive; a Go
service needing Postgres has a one-container Habitat. Only the expensive end
motivated the split, and nothing here requires a Habitat to be large. An
externally supplied database was considered and rejected: it sits outside every
fencing domain, it designs in the cross-Story contention
[ADR 0027](0027-concurrency-safety-for-shared-local-infrastructure.md) forbids, it
is not reproducible from the commit, and Maestro cannot provision it for a fresh
Story.

**Story-scoped support services inside the Incubator's own fencing domain were
considered and rejected**, and the rejection is recorded because the option will
be proposed again. Beyond the multi-implementation argument, it permits a repo to
run a container Postgres in the fast loop while its Habitat uses a managed
service — so the fast loop validates something the slow loop does not, and the
divergence is invisible until it is expensive.

This makes affordable Habitat leasing load-bearing rather than a convenience;
see §2.

#### The naming reconciliation, stated as a mapping

The blocker plan, [issue #273](https://github.com/SnapdragonPartners/maestro/issues/273),
and [ADR backlog slot 11](../v2/notes_adr-backlog.md) use `Habitat` for the single
conflated resource. Their requirements do **not** all move to the Incubator — an
earlier draft of this ADR claimed they did, which would have silently relocated
dependent-service lifecycle to the wrong resource. The mapping is:

| Prior requirement | Now attaches to |
| --- | --- |
| Tool routing targets a resource reference, never an Agent-derived local path | **Both** |
| Read-only Architect inspection; removal of Coder workspace bind-mounts | Incubator |
| Writable source workspace and toolchain lifecycle | Incubator |
| The Docker socket escape (`docker_long_running.go:243`) and the Compose-network attachment (`registry.go:385`) | Incubator (both are properties of the development container today) |
| **Dependent-service lifecycle** | **Habitat** |
| Deployed candidate, runtime configuration, integration and UAT surfaces | Habitat |
| Identity, spec-versus-instance, generation | **Both**, independently |
| The fencing protocol and its three-valued receipt | **Both** — the contract is generic over resource type (§7) |
| The Docker/Compose gating row of the fencing compatibility matrix | **Both** — Docker for the Incubator, Compose for the Habitat |
| Restart and reconciliation expectations | **Both** |

`Habitat` is retained for the deployed environment because that is the resource
the word describes. Renaming the deployed environment and leaving `Habitat` on the
development resource was considered and rejected as the less accurate of two
imperfect options.

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

**The Habitat is leased exclusively.** Compatible executions queue when capacity
is exhausted. The Orchestrator assigns leases; agents never claim one. The leasing
agent need not remain active while deterministic build, deployment, and test steps
wait for capacity.

**Authorization, capacity retention, and the environment are three different
things.** Two successive drafts conflated them: the first said the Habitat is
"released immediately after" verification while also saying one lease spans many
deployment generations, and the second let an idle Story hold authorization
indefinitely. Neither is right, because *authorization* and *keeping the
environment earmarked* are not the same claim.

- The **instance** is the provisioned environment (§5). It persists across many
  deployments of successive commits and is unaffected by who is authorized.
- The **lease** is exclusive authorization to deploy into an instance and verify
  against it. It is **generation-bound** and held for an active verification
  session, so an idle execution holds no authorization.
- The **retention claim** is the Story execution's continuing hold on the
  instance as capacity: it keeps the environment warm and earmarked without
  authorizing anything, and it is what demand-driven reclamation acts on.

**Releasing authorization does not destroy the instance.** That is the point of
the separation, and it is what makes §1's no-ecosystem rule affordable: a
repository whose unit tests need a database keeps a warm Habitat between runs
while holding authorization only while a run is actually in flight.

Whether the retention claim is a distinct persisted object or an affinity field
on the instance is a **Phase 3 implementation decision**; this ADR fixes only that
the two are not one thing.

**Reclamation must exclude an instance with an active lease**, and must be bounded
so a queue cannot be starved indefinitely by successive retention claims.

**Both bind to the Story execution, not to the agent principal** — the same
scoping the Incubator has. Agent restart or replacement therefore releases
neither, and **completion of the Story releases both** as the final backstop.
That is an ownership rule rather than a timeout, so it belongs here rather than in
scheduling policy.

**UAT is the known exception**, and it is deferred. A UAT Habitat is held for a
human at Epic grain, so its lease outlives the automated completion of any Story.
It is named here so the Story-completion rule is not later read as universal;
its lifetime is settled with UAT gate policy ([backlog candidate 6](../v2/notes_adr-backlog.md)).

**Lease release and retention reclamation are different events**, and an earlier
draft described the second in the first's terms:

- **A lease ends** when its verification session completes, or by expiry. This
  **deauthorizes and nothing more** — it does not stop a process (that is §7's
  protocol) and it does not disturb the instance.
- **A retention claim is reclaimed** to reallocate capacity to another Story. This
  **does** trigger fencing and a reset into a new instance generation before the
  arriving Story runs anything (§6). It may only act on a claim with **no active
  lease**, and must be bounded so successive claims cannot starve a queue.

**The reclamation policy is deliberately unresolved here** and is a Phase 3 plan
decision. What this ADR fixes is the two events above, that leases are bounded and
independently revocable, and that per-type capacity limits are the backstop. See
[notes_execution-contracts.md](../v2/phase_3/notes_execution-contracts.md).

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

### 4. The forge commit is the sole source and definition handoff

The neutral handoff of **source and environment definition** between the two
resources is an **immutable forge commit**. The Incubator produces one; the
Habitat consumes one. No source, no definition, and no mutable state crosses by
any other route.

Digest-addressed binaries do cross, and saying "nothing else crosses" would be
false: images, packages, and archives live in OCI registries, package registries,
or object storage and are referenced **by immutable digest** from the definitions
in the commit. They are reachable only because the commit names them, so the
commit remains the root of the handoff. Binaries never go into Git.

This is chosen because a forge is the one integration point essentially every
deployment mechanism already speaks, so existing deployment definitions work
largely unchanged, and because it makes the exchanged thing immutable and
addressable by construction.

**Both halves of the promoted commit matter.** The commit carries the application
source *and* the Habitat definition — the environment is habitat-as-code.
Promotion advances both together.

#### Spec identity is derived, and closes over the environment — not over the candidate

`HabitatSpec` is **derived, not registered**: there is no separate spec object to
keep in sync with the repository. But "the digest of the definition files" is not
sufficient. The provider MUST declare a **dependency closure** — the inputs that
shape the realized environment — and the spec digest is taken over that closure,
not over a directory.

**Two closures, and mixing them breaks §5.** A Compose `build:` context is
commonly the repository root, so a closure that swallows the build context makes
every application commit a spec change — which would advance the instance
generation on every commit and reinstate exactly the per-iteration reprovisioning
§6 removes. The dividing question is: **does this input describe the environment
the candidate runs in, or does it produce the candidate?**

| Closure | Contains | Feeds |
| --- | --- | --- |
| **Spec closure** | Definition files transitively (Compose files and overrides, Terraform/OpenTofu modules, Kustomize bases and overlays, Helm charts and values); **dependent-service image digests**; base and toolchain inputs; provider selection and version; non-secret variables that alter what is provisioned; platform, resource, and policy inputs, which [#273](https://github.com/SnapdragonPartners/maestro/issues/273) names as part of the requested origin | `SpecDigest` → instance generation (§5) |
| **Deployment closure** | The promoted source commit; application build inputs including the application `Dockerfile` and its context; the resulting **candidate artifact digest** | Deployment identity and provenance (§5) |

Worked example: `postgres:16@sha256:…` is spec — it describes the environment.
`build: .` for the application service is deployment — it produces the candidate.
That the `app` service *exists*, with these ports and this dependency on `db`, is
spec; what gets built into it is not. An application `Dockerfile` change that adds
a system package changes the candidate, not the environment, so it is deployment.

Both closures use immutable references. A mutable tag closes neither; images enter
by digest ([ADR 0026](0026-multi-architecture-artifacts.md)).

**The Phase 3 provider MUST enumerate both, and MUST NOT mix them.** Folding the
deployment closure into `SpecDigest` is not a conservative error — it silently
destroys the fast loop.

Secrets are **referenced by identity, never digested by value**.

#### Runtime configuration is a second axis, not part of the spec

A stable secret identity whose *value* rotates can change what the environment
does — a credential may point at a different backend — so "referenced by identity"
alone does not close the gap. But folding a resolved secret version into
`SpecDigest` is the wrong fix twice over: a routine credential rotation would
change spec identity and force reprovisioning of every Habitat that used it, and
declaring the spec `unclosed` over ordinary secrets would make essentially every
real spec unclosed and drain the term of meaning.

The resolution is a **`RuntimeConfigurationRevision`**, carried alongside
`SpecDigest`. The spec answers *was the same thing requested*; the configuration
revision answers *with what runtime values*. Two questions, two records, and
rotation moves only the second.

Where a provider can obtain an immutable, non-secret resolved version identifier
for a secret (a version ID, not the value), that identifier belongs in the
configuration revision. Where it cannot, the configuration revision is declared
**unresolved** for that entry — which constrains what may be inferred, without
contaminating spec identity.

**A recorded revision is not enough; it needs lifecycle semantics.** Three rules,
because a revision that is merely observed cannot back a receipt:

1. **Resolve and snapshot before deployment.** One resolution point per
   deployment, not lazily per service — otherwise two services in one deployment
   can load different revisions and no single revision describes the environment.
2. **Converge on change.** When the revision changes, the provider MUST restart or
   otherwise converge the services that consume it, **or declare the running
   revision unknown**. It must not assume a value read at process start has
   changed because the store behind it did. Blanket restarts are not required —
   what is forbidden is recording a new revision against services still running
   the old values.
3. **Bind verification to the revision actually loaded**, not the most recently
   observed. Reading the store after the fact reintroduces the same
   time-of-check gap that makes an unconfirmed fence unsafe.

**Consequently, inference requires the pair.** An earlier draft said two equal
spec digests must not be able to provision materially different environments. With
configuration as a separate axis that is true only of
`SpecDigest` **plus** `RuntimeConfigurationRevision`, and §5's evidence
requirements take both.

#### Unclosed is a declaration, not an escape hatch

A provider that cannot enumerate its closure MUST declare the spec **unclosed**
rather than emit a digest that implies more than it covers. An unclosed spec is
not a conformance failure in itself; it constrains what may be inferred from spec
equality.

**But the Phase 3 Docker/Compose provider MUST produce a closed spec.** `unclosed`
exists for future backends whose inputs genuinely cannot be enumerated, not as
permission for the one provider being built to skip the work. A Compose project's
spec closure — files, overrides, dependent-service image digests, non-secret
variables — is enumerable, so enumerate it, and enumerate the deployment closure
separately per the table above.

`HabitatInstance` is the provisioned, mutable resource with a stable ID, its own
generation, a provider reference, and lifecycle state (§5).

The same derivation and closure obligation apply to the Incubator: its definition
(Dockerfile, devcontainer spec, or equivalent) lives in the commit, and its spec
identity closes over base image digests and toolchain inputs on the same terms.

### 5. Three identities: specification, incarnation, deployment

An earlier draft carried two counters and made one of them mean two things: the
"Habitat generation" was defined as definition-derived *and* incremented on every
reprovision. Those diverge the moment a healthy environment is reprovisioned
against an unchanged definition — a real case, since fencing-then-replace is the
MVP reset path (§6). Three identities are needed, not two:

| Identity | Changes when | Is |
| --- | --- | --- |
| **`SpecDigest`** | The definition closure changes (§4) | *What was requested.* Content-addressed, not a counter. Two instances may share it. |
| **Instance generation** | A new provider domain is created, for any reason — definition change, reset, recovery, or quarantine replacement | *Which incarnation is real.* This is the **fencing domain identity**, and what stale capabilities are checked against. |
| **Deployment generation** | A new commit's application code is deployed into the current instance | *What is running inside it.* |

The essential rule the two-counter version got wrong: **a fresh provider domain
invalidates stale capabilities even when its `SpecDigest` is unchanged.** Identity
of request is not identity of incarnation. Binding fencing to the spec would let a
capability issued against a destroyed environment be honoured by its replacement.

One **instance** spans many deployment generations. (An earlier draft said "one
lease," which contradicted §2 — the lease is bounded authorization, the instance
is the environment.) Without this separation every commit advance would create a
new provider domain, invalidating the lease and forcing reprovisioning per
iteration, which is the expensive step rather than the cheap one.

Evidence needs all three: a verification receipt must be able to say what was
requested, which incarnation ran it, and which commit was deployed. No two of the
three reconstruct the third.

**A deployment does not by itself advance the instance generation, in MVP or
after.** An earlier draft said it did — that MVP reprovisions on every deployment
because reset is reprovision-only — and that was wrong in a way that quietly
negated the benefit this section exists to provide. Deploying application code
into a running environment has never required a new instance: `docker compose up
--build` rebuilds the application image and recreates that service while leaving
the database running. That is present behaviour of the tool Phase 3 already uses,
not deferred capability.

What actually requires a new instance in MVP is a **reset**, and §6 fixes when a
reset is required. The two were conflated, and the conflation made the fast loop
pay a full rebuild per iteration for no stated reason.

The identities are recorded separately regardless, because evidence must say what
was requested, which incarnation ran it, and which commit was deployed — and
because in-place reset later changes which of them advance together, without the
earlier records needing reinterpretation.

**Evidence takes `SpecDigest` and `RuntimeConfigurationRevision` together** (§4).
Neither alone identifies the environment: equal specs with different resolved
configuration are materially different environments, and equal configuration
across different specs says nothing. A receipt naming only one of them
overstates what it establishes.

### 6. Reset, and why in-place convergence is a correctness trade

#### Reset is required before evidence-bearing verification, not before every run

A verification whose result becomes a **receipt** requires a clean environment.
A run whose result is the agent's working feedback does not. Distinguishing them
is what keeps the fast loop affordable without weakening the guarantee where it
does work:

| Run | State | Code |
| --- | --- | --- |
| **Evidence-bearing verification** | Reset — MVP: a new instance generation | Converged to the promoted commit |
| **Iteration** (agent working feedback) | Persists in the existing instance | Converged to the promoted commit |

#### Ownership transfer is an independent reset trigger

Persistent iteration state is safe **only while the same Story holds the
instance**. When a retention claim is reclaimed for a different Story (§2), the
arriving Story MUST get a fenced, reset instance in a new generation **before any
run**, including a run it requests as an iteration. Without this it inherits the
displaced Story's code, database contents, and service state — cross-Story
contamination, which is the failure class
[ADR 0027](0027-concurrency-safety-for-shared-local-infrastructure.md) exists to
forbid, arriving through a door §6 left open by assuming the instance's history
belonged to whoever was using it.

So the reset triggers are: **evidence-bearing verification**, **ownership
transfer**, and provisioning a new instance (which is clean by circumstance rather
than by demand). Iteration by the owning Story is the only path that keeps state.

**This makes reclamation purely a capacity mechanism.** The arriving Story always
pays a reset, so it gains nothing from the instance being warm — it would pay the
same for a freshly provisioned one. The displaced Story loses its warmth outright.
There is therefore no warm-transfer benefit to weigh: reclamation is worth doing
only when capacity is genuinely exhausted, and never as an optimization. It also
raises the cost of thrash, which is what the minimum hold bounds.

#### Three properties of the evidence-bearing rule

These matter and are easy to lose:

- **The criterion is evidence-reliance, not leak size.** A leak may be arbitrarily
  large — a migration that ran, a table of stale rows — and still not matter if
  nothing relies on the result. A tiny leak matters completely when the result is
  a receipt. Reasoning from "this leak is small" is how the rule erodes.
- **It is not "the last run."** There may be several evidence-bearing
  verifications in a Story, and holding a lease does not make a run an iteration.
  A candidate submitted for verification takes the reset path whether or not the
  same execution has been iterating for an hour.
- **Code convergence is never skipped.** Both paths deploy the promoted commit
  with images pinned by digest. Stale *data* produces confusing feedback; stale
  *code* produces confident wrong feedback — an agent concluding its fix worked
  when the fix never ran. That is the failure this rule must not create, and it is
  why `compose up` without `--build`, or a `:latest` without `--pull always`, is
  prohibited on both paths.

**The Orchestrator decides which path applies**, because it knows whether it is
producing a receipt. This is a deterministic classification, not a judgment, so it
sits on the Orchestrator's side of [ADR 0019](0019-orchestrator-boundary.md) — it
is never the agent's choice and never a timer. A v1 analogue survives into v2: a
test request from the coding loop is an iteration, and one from the final approval
loop is evidence-bearing.

A **new instance** is provisioned from nothing, which produces a clean environment
by circumstance rather than by rule. Keep that distinct from a demanded reset, so
that optimizing one path cannot silently disarm the other.

#### The reset contract

Both resource types expose the same reset contract, and for MVP both providers
may always answer `reprovision_required`. The Orchestrator then fences the
existing generation, preserves required evidence, destroys or permanently
quarantines it, provisions a clean replacement, increments the instance
generation, and verifies readiness before leasing.

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

#### Teardown is not itself a clean reset

The paragraph above was, in an earlier draft, followed by the conclusion that
teardown-and-rebuild is "correct by construction." That does not follow, and the
draft contradicted its own argument one paragraph after making it. **The same
volumes that survive `up` also survive `down`:**

- `docker compose down` removes containers and networks. It removes **named
  volumes only with `-v`/`--volumes`**.
- Volumes declared `external: true` are **never** removed by `down`, with or
  without `-v` — Compose does not own them.
- Bind-mounted host state is not project state at all and outlives the project
  entirely.

So teardown-and-rebuild reaches a clean state only for the subset of mutable
state the project actually owns and is actually asked to remove. Assuming it is
clean is the same class of error as assuming a stopped container is fenced.

**Requirement.** A reset is complete only when the provider establishes one of:

1. **Generation-scoped namespacing** — the replacement instance's mutable state
   lives in names derived from the new instance generation (§5), so it cannot
   reach the prior generation's state whether or not that state was deleted; or
2. **A positive reset receipt** — the provider enumerates the mutable state the
   instance owns and confirms its removal.

Mutable state that satisfies neither — external volumes, bind-mounted host paths,
shared services — MUST be **rejected at provisioning or quarantined**, and MUST
NOT be silently inherited by a replacement instance. A provider that cannot
establish either condition for some part of its state declares that part
**unreset**, and an instance carrying unreset state cannot back a verification
receipt claiming a clean environment.

Generation-scoped namespacing is preferred over deletion for the same reason
`isolated` exists in §7: it proves non-interference without depending on a
destructive operation succeeding.

With that requirement met, teardown-and-rebuild is the MVP choice because it is
the simpler of two paths that can both be made correct — not because it is
correct on its own. Any future in-place path MUST state what is reset between
verification runs even when the infrastructure is not reprovisioned; it buys
latency by spending a correctness guarantee, and it must show that guarantee is
restored by other means.

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

**External reach constrains which receipt is available — it does not shrink what
a receipt means.** An earlier draft said a receipt "covers Maestro-managed state
and claims nothing beyond it." That was a weakening of an invariant this ADR does
not have the authority to weaken: the accepted definition of `isolated` is that
the old generation cannot affect state reachable by current or future work, and it
says nothing about who provisioned that state. A generation still holding valid
credentials to shared staging or a licensed service *can* interfere with later
work, so `isolated` is simply not available to it.

The correct rule follows from §7's own capability requirement, which the earlier
draft failed to apply to outward-directed capabilities:

- Where the fenced generation's external capabilities **can be revoked or
  generation-fenced**, revoking them is part of producing the receipt, and
  `isolated` is available.
- Where they **cannot** — a credential the repository baked into its own
  configuration, which Maestro cannot withdraw — the generation retains reach.
  Only **`terminated`** can then be a positive receipt, because confirmed
  stoppage is the only remaining way to establish non-interference. Uncertainty
  is `unconfirmed`.

Downgrading a later verification receipt does not substitute for this. A terminal
execution result recorded against a generation that can still reach shared state
is a false record, which is the exact failure §7 exists to prevent.

External connections remain permitted and are not a conformance failure. What
changes is that they narrow the available receipts, and an instance holding
unrevokable external reach also cannot back a verification receipt claiming a
clean environment (§6). This is a further reason §1 routes Maestro-managed
dependent services to the Habitat rather than leaving them external.

### 8. Tool routing, and contracts declare where they run

**Maestro's tools target a resource reference, never an Agent-derived local
path.** This binds Maestro's own agents. It is not a claim of control over
arbitrary processes inside a resource: an engineer may legitimately run other
agents there, and the application under development may itself be an agent.

**Every execution contract declares the resource it executes in.** This is what
makes §3 enforceable rather than aspirational:

- **Incubator contracts** — those requiring only the workspace and toolchain.
- **Habitat contracts** — those requiring deployed or dependent services.

**Routing is by requirement, never by the contract's name.** An earlier draft
assigned build, lint, and unit test to the Incubator and deploy and integration
test to the Habitat, which contradicts §1: a repository whose unit tests need a
database runs those tests against a Habitat. `test` versus `integration` is
repository terminology and varies between projects; what a contract *requires* is
a fact about the contract. A repository with no dependencies runs `test` in its
Incubator; a repository needing Postgres runs the same-named contract in its
Habitat.

An integration-test contract cannot quietly run in the Incubator and reach the
Habitat, because the Incubator has no route. Verification therefore executes
**inside the Habitat's fencing domain**.

That does **not** mean the production deployment definition must be modified to
carry a test runner — requiring it would violate §1's binding constraint that
those definitions are not Maestro's to write. The **provider launches a
verification executor into the Habitat's domain**, joining the same fencing unit
and the same instance generation, without appearing in the definition. A
repository may instead declare its own runner in a development-facing overlay if
it prefers; both satisfy the rule. What is prohibited is a runner *outside* the
domain reaching in, which is the no-channel escape under another name.

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

**Shared implementation, never a shared instance.** A simple application may use
one image, one provider, and one definition for both roles — the analogy is the
build and run stages of a container image, which share a Dockerfile and are not
the same container.

An earlier draft said the two roles could be "physically identical." That is not
available: **a single instance cannot satisfy both the independent-fencing-domain
guarantee (§7) and the no-direct-channel invariant (§3)**. Fencing the coding role
would collaterally fence the verification role, since one domain cannot be fenced
in half; and an Incubator that *is* the Habitat has a route to it by definition,
so the invariant is not merely unenforced but false.

The rule:

- The two roles are always **distinct instances with distinct fencing domains**,
  even when they share an image, a definition, a provider implementation, and a
  host.
- They may be **sequential** — the Habitat instance provisioned for verification
  after the Incubator's candidate is promoted, and released after — which is the
  ordinary shape for a simple application and costs nothing extra.
- **Concurrent leases across the two roles by one execution are permitted**
  (§2 allows holding a Habitat across an iteration burst while the Incubator
  remains leased), because they are separate domains with no route between them.
  What is forbidden is collapsing them into one domain to avoid the second
  provisioning.

A provider that genuinely cannot separate the domains MUST declare the collateral
semantics required by §7 — that fencing either role fences both — and MUST NOT
present itself as offering independent domains. No Phase 3 provider is expected
to be in this position; Docker and Compose separate cleanly.

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
  no LLM in the loop — so the cost is wall-clock on an unattended step.
  Provisioning stays off the per-iteration path because a deployment does not
  advance the instance generation (§5) and iteration does not demand a reset (§6).
  **Both are required for that claim**; an earlier draft asserted it while §5 said
  MVP reprovisioned on every deployment, which would have made it false.
- **Evidence-bearing verification is deliberately expensive.** It reprovisions in
  MVP, and that cost is the guarantee, not an inefficiency to optimize away. What
  the fast path buys is that a repository does not pay it on every test run.
- **Throughput is the binding constraint, not latency, and §1 tightened it.**
  With many Incubators and few Habitats, stories serialize behind Habitat
  capacity — and routing every Maestro-managed dependent service to the Habitat
  means more executions need one than a purely production-shaped reading would
  suggest. This is what makes bounded leases (§2) and per-type capacity limits
  load-bearing rather than administrative. Setting the Habitat limit as if only
  large applications needed one would starve the ordinary database-backed case.
- **Provenance records all three identities plus the configuration axis.** A
  promotion binds Story and source commit, Incubator identity and generation, the
  spec digests of both resources and the closure each was taken over, the
  **runtime-configuration revision** (§4), artifact digests and locations, Habitat
  identity, **instance generation and deployment generation**, whether the run was
  evidence-bearing (§6), deployment result, and verification evidence. Where a
  spec is declared *unclosed*, a configuration entry *unresolved* (§4), or an
  instance carries *unreset* or unrevokable external reach (§6, §7), the record
  says so, so a later reader cannot infer more from spec equality than the
  provider established.
  Any durable promotion record is an artifact and routes through
  [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md)'s envelope and payload
  type registry and [ADR 0021](0021-artifacts-and-principal-instances.md)'s
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
   `spikes/phase_3/`. It must additionally prove two things the first sketch of
   this section did not ask for:
   - **Fencing closes the sibling-creation race before enumeration.** A domain
     that is enumerated and *then* fenced loses to a holder that creates one more
     sibling in the interval. The order must be: revoke the ability to create,
     then enumerate, then confirm. A reproducer that creates its sibling before
     fencing begins proves nothing about the race.
   - **Provider records survive a failed fence.** Inject a stop failure and show
     the container remains recorded and reconcilable — the property
     `StopContainer` destroys today.
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
