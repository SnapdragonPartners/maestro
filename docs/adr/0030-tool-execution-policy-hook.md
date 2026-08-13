+++
title = "ADR 0030: The Tool Execution Boundary And Its Policy Hook"
edit_date = "2026-08-13"
status = "draft"
summary = "All agent requests for Maestro-managed effects pass through one Orchestrator boundary that records intent, validates machine policy, obtains human approval when required, and records the result. Human approval blocks the Story and caller without consuming LLM turns; headless runs instead mark the Story blocked. Expensive resources are acquired only after approval, and all current authorization, work-version, and resource conditions are revalidated immediately before execution. The design centralizes policy and auditing without introducing approval retries, resolution chains, or arbitrary agent checkpointing."
type = "design"
+++

# 0030. The Tool Execution Boundary And Its Policy Hook

Status: **Proposed** (Claude, 2026-08-12; substantially redrafted 2026-08-13).
Item A2 of the accepted
[pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md), which settles the
placement and directs this ADR at the part the original framing missed: the
mediated versus in-resource split and what each mode honestly guarantees. Drafted
concurrently with, and reviewed as a set alongside,
[ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) (item A3).

This ADR resolves [ADR backlog candidate 4](../v2/notes_adr-backlog.md). It fixes
**no policy content**; the gating rules are
[candidate 12](../v2/notes_adr-backlog.md), post-MVP.

**The redraft replaced one premise and everything built on it.** Earlier versions
held that the boundary never suspends, so a gate needing a human *denied* and the
approved action returned as a fresh attempt. That required an approval to survive
across attempts, which required a replay-proof subject, a durable resolution chain
to make the approval single-use, an accumulated reference set once two gates could
each deny, and Orchestrator-owned chain state with atomic claim and terminal
conditions. Six review rounds of real defects were found and fixed inside that
structure. The premise was wrong: a blocked execution performs no LLM turns and
costs almost nothing, while the expensive thing — a retained Incubator or
Habitat — costs the same either way, and more once
[ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §6's reset on return
is counted. **A human-required tool call is one logically blocked call.** Chains,
accumulated references, approval rounds, replay subjects, and the never-suspends
rule are removed; nothing replaces them, because with a single call there is
nothing to replay.

## Context

### What v1 actually has

Verified against the frozen v1 tree at `4a89913`. None of this is a v1 defect to
fix — v1 is frozen (CLAUDE.md) — and all of it is a requirement on Phase 3.

**There is no tool-execution boundary.** `tools.Tool.Exec` is called from five
places:

| Call site | Who calls it | Recorded? |
| --- | --- | --- |
| `pkg/agent/toolloop/toolloop.go:486` | the agent tool loop | Yes |
| `pkg/coder/testing.go:343` | Coder state machine, build tool | No |
| `pkg/coder/testing.go:369` | Coder state machine, container test tool | No |
| `pkg/coder/testing.go:800` | Coder state machine, build tool | No |
| `pkg/coder/claude/mcpserver/server.go:411` | the MCP server serving Claude Code | No |

`agent.LogToolExecution` — the function that writes
[ADR 0022](0022-v2-data-plane.md)'s atomic Audit **action** unit — is reached from
`toolloop.go` alone. `pkg/coder/driver.go:1920` defines a `logToolExecution`
wrapper on the Coder that **has no callers anywhere in the repository**.

The consequence is the shape Phase 3 must not reproduce: **the one execution path
serving an adapted external agent runtime records nothing.** Claude Code reaches
Maestro's tools over the MCP server, and that path has no persistence call at all.

**Authorization is an allowlist resolved at construction, not per action.**
`tools.NewProvider(ctx, allowedTools)` (`pkg/tools/registry.go:139`) captures a set
of tool names, and `Get` refuses a name outside it (`registry.go:159`). That is the
right *kind* of gate, deciding once, on the name alone. It never sees arguments, it
has no notion of which resource the call belongs to, and a refusal on one of the
four unrecorded paths leaves nothing behind.

**Recording happens after the effect and cannot fail loudly.**
`LogToolExecution` is called after `Exec` returns (`toolloop.go:546`) and hands the
record to a persistence channel with no response (`pkg/persistence/persist.go:110`).
A process that dies between the side effect and the record leaves no trace that the
action was attempted — the failure mode the
[Docker fencing spike](../v2/phase_3/spike_docker-fencing.md) hit from the other
direction, where a failed stop deleted the record a reconciler needed.

**And v1 has the escalation failure this ADR must not repeat.**
[#317](https://github.com/SnapdragonPartners/maestro/issues/317) is a headless run
deadlocking in `ESCALATED` — a state no headless run can answer — and
[#221](https://github.com/SnapdragonPartners/maestro/issues/221) is a watchdog
recycling work that was legitimately not progressing. Blocking on a human is only
safe if both are designed for, which is why §4 states them rather than leaving them
to be discovered.

### The question this answers, and the question it does not

The [research synthesis](../v2/research_synthesis.md) posed it as open question 7 —
*where should policy gates live: toolloop, dispatcher, tool execution layer, or a
separate policy service?* — and the parking lot's
[tool-level policy gates](../v2/notes_parking-lot.md) entry deferred the
implementation post-MVP while asking that the contracts leave a seam.

This ADR is that seam and nothing more. What structural, semantic, and human gates
actually check is candidate 12.

### What this ADR must satisfy

- [ADR 0019](0019-orchestrator-boundary.md): tool implementation, routing,
  persistence, and deterministic gate evaluation are Orchestrator machinery.
  Anything requiring inference is an agent.
- [ADR 0022](0022-v2-data-plane.md): the tool call is the atomic Audit action unit,
  and any LLM output that creates artifacts, decisions, or state transitions must
  pass through a tool/action record — parsed free text can never be a side door.
- [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §7 requirement 5:
  a call issued by a fenced holder is **rejected at the boundary** even if it
  arrives late. That boundary is this one.
- [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §8: Maestro's tools
  target a resource reference, never an Agent-derived local path — binding on
  Maestro's own agents, and not a claim of control over arbitrary processes inside a
  resource.
- [ADR backlog candidate 13](../v2/notes_adr-backlog.md) (item A4): native Go agents
  and adapted external agents reach these resources only through the wire contract,
  so both meet this boundary.

## Decision

### 1. One mandatory boundary

Every **agent-initiated action that crosses back into the Orchestrator** passes
through a single execution boundary before its effect.

Not in the tool loop, not in the dispatcher, not in individual tools, and not a
separate policy service. The blocker plan settled the placement and it is not
reopened. What matters for Phase 3 is the adjective: **mandatory**. The boundary is
the only route to the effect, structurally — not a function every tool is expected
to call. A tool that can execute without traversing it is a defect in the same class
as v1's four unrecorded call sites, and Phase 3 must be able to demonstrate the
property rather than assert it.

The seam is one place for a reason that outlives policy: it is where the action
identity, the principal, the work version, the target, and the arguments are all
simultaneously in hand.

### 2. Three gates, in order — and resources come last

| Gate | Decides | Outcome |
| --- | --- | --- |
| **1. Machine-verifiable admission and policy** | principal, effective Story version, action, arguments, capabilities, intended target | allow, deny, or **requires an operator** |
| **2. Human approval**, only when gate 1 says so | the operator's decision, at a declared scope | approve or deny |
| **3. Resource resolution and execution** | acquire or provision the resource, revalidate everything, execute | effect, recorded |

**Expensive resources are acquired after approval, not before.** Inspecting a
Habitat database can be approved before a Habitat exists; Maestro obtains one
afterwards. Provisioning first would hold a production-shaped environment for the
length of a human's attention span, which is the one genuinely expensive thing in
the flow.

Two consequences of that ordering are worth stating because they read as omissions
otherwise:

- **Gate 1 cannot check resource generation or leases**, because there may be no
  resource yet. It validates the *intended target*. Generation, lease, and
  provisioning state are gate 3's, revalidated immediately before the effect.
- **The fencing window is short.** §5's linearization spans gate 3 to commit, not
  the human wait. An earlier ordering would have held a fencing registration open
  across an approval that might take hours.

### 3. Gate 1 — machine-verifiable admission and policy

Two stages, in this order, so that an empty policy cannot disable an invariant and
candidate 12 never has to reimplement one:

1. **Admission — deterministic, Orchestrator-owned, not policy.** The principal
   instance is live; the invocation's work version is still the effective one; the
   action is contained in the invocation's resolved capability set; the intended
   target is one this execution may name. Every one is a rule-and-config decision
   under ADR 0019 and none is negotiable by a policy implementation.
2. **Policy — the hook.** The single extension point. In MVP it is a default-allow
   implementation carrying no rules. Candidate 12 fills it.

**The decision is three-valued**: allow, deny, or **requires an operator**. A
requires-an-operator decision carries a **structured requirement** — what is being
asked, and at which scopes the gate permits an answer (§4) — because the consumer
is the scheduler and a UI, not a person reading prose.

#### The request

| Field | Why it is here |
| --- | --- |
| **Action identity** — a stable kind and verb from an Orchestrator-owned vocabulary | Not the caller's tool name. An adapted runtime brings its own names for the same action (`Edit` against `file_edit`), so policy keyed on the caller's vocabulary would be runtime-specific by construction |
| **Principal instance** and role | [ADR 0021](0021-artifacts-and-principal-instances.md); the accountable identity, and what the record attributes to |
| **Work scope and version** | Version-bound dispatch ([ADR 0019](0019-orchestrator-boundary.md) as amended); admission compares against the effective version |
| **Intended target** — which resource or resource kind the action is for | Nameable before a resource exists, which is what makes §2's ordering possible |
| **Resolved capability set** | The hook must see what was granted, or it cannot reason about the action it is being asked to allow |
| **Normalized arguments**, canonical JSON per [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md), secrets already substituted | A decision must be over a determinate value, and the record must be bindable to it by digest |
| **Attempt identity** | Correlates the decision with its record, and carries the at-most-once semantics below |

#### Three properties of the hook

- **It is deterministic and may not infer.** ADR 0019's boundary rule applies
  directly: it decides from rules and configuration. The consequence for candidate
  12 is better stated now than discovered then — **a semantic gate is an agent.**
  "High-risk action summaries checked against policy" requires judgment, so it is a
  gate that consults an agent, not logic inside the hook.
- **It performs no side effects.** A hook that writes is a second, unrecorded action
  path.
- **It is fail-closed.** A hook that errors, or exceeds its bound, denies. Admission
  fails closed for the same reason, and there the consequence is real: **if the data
  plane is unreachable, mediated actions stop**, because the effective version
  cannot be established and must not be assumed. That is correct, and it is an
  availability property Phase 3 should meet deliberately.

#### Attempt identity has at-most-once semantics

- **A transport retry of the same logical action reuses the same attempt identity.**
  A dropped response is not a new action.
- **An intentional repetition is a new attempt identity.** Running the same command
  twice on purpose is two actions and must record as two.
- **One attempt identity commits its effect at most once.**
- **An attempt with a recorded intent and no outcome does not re-execute.** It
  resolves as `unknown` and goes to reconciliation. Blind re-execution is how an
  adapted runtime's retry duplicates a forge push, an artifact publication, or a
  resource lifecycle transition.

#### The persisted projection is not the decision input

The hook may need complete normalized arguments. Persisting them verbatim would put
secrets, whole file contents, and unbounded command output into the Audit family,
which is durable, queryable, and exportable. So three forms are distinguished:

- **Raw input** — exactly what the caller supplied, in memory only, never digested
  and never persisted.
- **Substituted input** — the raw input with every secret replaced by a
  **version-pinned reference**. This is what the boundary decides on, and the only
  form ever hashed.
- **Persisted projection** — the fields the **action schema** declares safe, plus
  the digest of the substituted input, plus references to artifacts or objects for
  anything large.

**Substitution happens before hashing, not only before persistence.** A canonical
unkeyed digest over complete raw input is an **offline guessing oracle** for
anything low-entropy — a token pasted into a shell argument, a short credential, an
internal hostname — and redacting the projection does not help, because the digest
published beside it is what is attacked. A digest is not a redaction. Where a value
is sensitive, low-entropy, and not registered secret material, the schema MUST
specify a **keyed commitment**, with the key held in Phase 2's vault and never in
the Audit family.

**A substituted reference names a revision, not a name.** A stable identifier
survives rotation, so two attempts either side of one would digest identically over
different effective input. Substitution uses an immutable **secret-version
reference** (Phase 2's `secrets.version` is already part of the key-derivation
context), the applicable **runtime-configuration revision** where the value arrives
that way ([ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §4 makes
the same distinction for the same reason), or failing both the keyed commitment's
scheme and key version.

Redaction and substitution are declared by the code-resident action schema, on the
pattern of ADR 0028's payload type registry — never chosen by the caller, which
would be the hole Phase 2's configuration key registry refuses. The digest then
binds the record to the exact **substituted** input, which is a narrower claim than
"the exact input" and is why the references must be version-pinned.

### 4. Gate 2 — human approval, which blocks

**The logical tool call does not end.** It and its Story enter
`awaiting_resolution`.

#### Waiting semantics

- **The Story and the calling agent are blocked.** The caller performs no further
  LLM turns and issues no further tool calls.
- **Further agent-initiated tool calls for that Story are rejected while it awaits
  resolution**, and a firing of this guard is logged as an **invariant violation**
  rather than as an ordinary denial: it means something upstream let a blocked
  caller keep working.
- **Human resolution, reconciliation, cleanup, and fencing use separate control
  paths** and remain permitted. Blocking the agent does not block the Orchestrator.
- **Dependent Stories remain blocked naturally through the DAG**, because their
  prerequisite has not completed. No separate mechanism is needed.
- **The watchdog must recognise this as healthy waiting**, not inactivity to recycle
  or requeue. v1 has failure precedents in both directions (#317, #221), so this is
  a stated requirement rather than an assumption. The policy belongs to the Phase 3
  plan; the requirement belongs here.
- **No agent checkpoint at an arbitrary instruction is promised.** The pending action
  record and the last durable workflow artifact are sufficient recovery state, which
  is the same guarantee restart already gives.
- **The wait is logical.** It must not hold a database transaction or a transport
  connection open. The open-then-complete record (§6) is what makes that possible:
  the intent is committed, not held.
- **A pending action carries an explicit `awaiting_resolution` state.** An
  unfinished action record alone is ambiguous with a crash, and §6's reconciliation
  rule reads exactly that ambiguity.

#### Resource behaviour while waiting

- **The wait does not request or renew a lease.** Any existing lease may expire
  normally, and lease expiry deauthorizes only
  ([ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §2).
- **Retention policy is separate and may keep an environment warm briefly, reassign
  it, or tear it down.** The pending action cannot depend on its survival.
- **After approval, gate 3 reacquires a suitable resource or provisions a
  replacement.** If an Incubator was released, recovery is from the last durable
  workflow artifact, not from a process checkpoint.
- **The Phase 3 plan must ensure that awaiting resolution cannot renew a lease
  indefinitely, and must define when a retained, potentially billable resource is
  actually relinquished.** ADR 0029 already requires undeallocated resources to stay
  visible and flagged; what is missing is the release rule for this specific wait.

#### Headless operation is first-class

Not a degraded mode.

**Headless is a declared execution configuration, known at dispatch — never an
observation that nobody answered.** The invocation states whether an operator
responder is available for it. Deciding from live evidence instead would turn an
interactive run whose operator stepped away into an immediately terminal block,
which is precisely the ordinary asynchronous wait this design exists to support.
An interactive execution waits however long the human takes.

Where the configuration declares no responder:

- The action's requirement is **recorded**, and the Story becomes **`blocked`
  immediately** rather than waiting for a timeout.
- **Independent runnable work continues.** One gated Story does not end a run.
- Once no runnable work remains, the headless invocation **exits gracefully with a
  distinct blocked/incomplete result**, never success.
- The Story is irreconcilable within that run and remains available for a later
  operator-enabled one.

This is what keeps #317 from recurring in a new form, and it is why A4's terminal
result carries `blocked` as an execution status.

#### The four outcomes

Decision × scope:

| | Once | For the Story |
| --- | --- | --- |
| **Approve** | `approve_once` | `approve_for_story` |
| **Deny** | `deny_once` | `deny_for_story` |

- **The gate — not the approval UI — declares which scopes it permits.** Some
  policies should always demand a fresh decision; spec amendment is the obvious one.
  A UI free to compose scopes would become an authority on what may be approved.
- **MVP may defer the reusable `*_for_story` grants.** The action-scoped decision is
  still durable, for crash recovery, but is not reusable by another action.
- **If Story-scoped grants ship, they are bound to the effective Story version and
  fail closed on amendment.** A grant is durable authorization, so it is
  version-bound exactly as ADR 0019's dispatch records are.

**Not outcomes**, stated so they are not proposed as a fifth and sixth:

- **`cancelled`** — the requester cannot withdraw a blocked action. Cancelling the
  Story is a lifecycle operation on a separate path, and a cancelled Story's pending
  action fails gate 3's revalidation.
- **`superseded`** — amendment changes the governing Story version, and **every
  pending action and every Story-scoped decision bound to the prior version becomes
  stale**, unconditionally. Not "every incompatible one": deciding whether an
  amendment is compatible with a pending approval is a judgment, which under
  ADR 0019 is not the Orchestrator's to make and cannot be the basis of a
  fail-safe rule. This is the same unconditional invalidation ADR 0019's dispatch
  amendment already applies to version-bound dispatch records. It is an effect of
  the amendment, not a decision someone makes here.
- **"Approve with different arguments"** — that makes the human the author of the
  action. Changing the action requires a new request.

#### What the approval binds to

**The logical action**: its Story version, the action, the intended target, and the
arguments. **Not an ephemeral resource generation.** Recycling a Habitat between
approval and execution therefore does not require another approval; amending the
Story or changing the action does.

The honest limit: an approval whose meaning depends on the *contents* of an
environment rather than on the action is not expressible this way, because a
recycled environment is a different one. No Phase 3 gate is in that position, and
this is recorded as a limit rather than as a mechanism nobody needs yet.

### 5. Gate 3 — resources, revalidation, and execution

Only after approval does Maestro acquire, reacquire, or provision the Incubator or
Habitat the action needs, establish the lease and generation, revalidate, and
execute.

**Acquiring a resource can itself take time.** Provisioning is not instant, and
ADR 0029 §2 queues compatible executions when Habitat capacity is exhausted. So
gate 3 has a wait of its own, and it is a *different* wait from gate 2's — see §8,
which requires both to be distinguishable in the record from an interrupted call.

**Approval clears the human requirement and nothing else.** Immediately before the
effect, the boundary revalidates the principal, the effective work version, the
capability set, and the lease and current resource generation, and re-evaluates
machine policy for a **denial**. Any failure refuses the action; the agent resumes
with a result it can act on, and a re-request is an ordinary new action.

**The persisted approval satisfies gate 2 for this logical action, and gate 3
consumes it rather than re-asking.** Re-running the three-valued policy unchanged
would return *requires an operator* again and the action would never execute — the
approval would authorize nothing. So the rule is asymmetric on purpose:

- Gate 3 re-evaluates every **deterministic** condition, and an unchanged policy
  that now *denies* still denies.
- Gate 3 does **not** re-raise a requirement the persisted decision already
  satisfies, for the same logical action (§4's binding: Story version, action,
  intended target, arguments).
- A **new or different** operator requirement — because policy changed and a gate
  that did not previously apply now does — is not satisfied by that decision, and
  the action returns to gate 2 for it. The approval is consumed for what it
  answered, not for what it never saw.

#### The admission-to-effect interval participates in fencing

An authoritative generation read is a snapshot, and a snapshot is not a guarantee:
fencing can complete between revalidation and commit, and the effect would then land
under a generation already holding a positive receipt. ADR 0029 §7 would have
reported `terminated` truthfully while the mutation happened anyway.

**Every action family MUST declare its commit point** — the instant after which the
effect is no longer the Orchestrator's to withhold. For a data-plane write it is the
transaction commit; for a forge push, the point the remote accepts the ref update;
for a mediated external call, **transmission**; for a provider-launched executor, the
point it starts inside the resource's domain.

`Fence()` may return a positive receipt only when, for **every** attempt admitted
against that generation and not yet settled, one of these holds:

1. **Drain** — the attempt is confirmed not to have passed its commit point and is
   stopped before it does. Attempts register against `(resource, generation)` before
   admission completes; `Fence()` **first closes admission for that generation**,
   **then** settles those already registered. That is ADR 0029 §7 step 2's own
   ordering — *revoke the ability to create, then enumerate, then confirm* — applied
   to attempts instead of containers, and the
   [Docker spike](../v2/phase_3/spike_docker-fencing.md) measured what enumerating
   first costs.
2. **Conditional commit** — the effect commits atomically with a generation
   predicate. Available only where the effect site accepts one: a data-plane write
   does; a forge push, a container start, and an external call do not.
3. **Confirmed passage into the fenced domain** — a provider-launched verification
   executor inside the Habitat's domain is covered by fencing the domain.

Otherwise **`Fence()` returns `unconfirmed`.**

**"Invalidate the attempt" is not a fourth option.** Marking an admitted attempt
invalid does nothing unless the effect site checks the mark atomically before
committing, which is mechanism 2 under another name; and for an external request,
recording invalidation after transmission does not un-send it while the record
asserts a control never exercised.

**A drain that will not settle within ADR 0029 §7's grace period yields
`unconfirmed`.** Making the unsettled case cheap by returning `terminated` anyway
would reintroduce best-effort fencing through this door.

**The obligation binds effects that can mutate state current or future work will
touch**, which is ADR 0029 §7's own property. An in-flight *read* cannot mutate that
state and does not hold up a receipt; it can still disclose, which is §6's concern.

Unmediated effects never arrive here, so nothing linearizes them; what bounds them
is §6.

### 6. Mediation and containment are independent axes

Two questions with independent answers. Conflating them produces a specific false
conclusion — that a mediated action's *effects* are policed.

- **Mediation** is a property of the **request path**: can the Orchestrator refuse
  this action?
- **Containment** is a property of the **effect site**: where does the effect land,
  and which authority bounds it? It is decided at grant time.

**The effect site is three-valued.** An agent runtime's own shell can call an
external API, mutate a service Maestro does not manage, or write over an already-open
connection. That is neither the Orchestrator's nor inside the resource's own state,
and no grant Maestro makes at provisioning bounds what a reachable endpoint accepts.

| Request | Effect site | Policed per action | Recorded | Guarantee |
| --- | --- | --- | --- | --- |
| Mediated | **Orchestrator-side** — data-plane writes, artifact publication, forge operations, resource lifecycle, message routing, retrieval | Yes | Yes | The action commits only if the gates allow it, under §5's linearization, and the record survives it |
| Mediated | **In-resource** — an execution contract, or a command Maestro is asked to run in a resource | **The decision to run it, only** | Yes | The Orchestrator governs *whether* the command runs; it makes no claim about what the command then does |
| Mediated | **External** — a Maestro-provided tool that reaches outside, such as a fetch or a search | **The decision to make the call, only** | Yes | The request is governed and recorded; the remote effect is not Maestro's to bound |
| Not mediated | **In-resource** — the agent runtime's own built-in tools, and any other process in the resource | No | No | Containment alone (below) |
| Not mediated | **External** — the same runtime reaching the network directly | No | No | **Bounded only by what was granted**: network reachability and the credentials present in the resource |
| Not mediated | **Orchestrator-side** | — | — | **Must not exist** (§7) |

**Only direct access to Orchestrator-managed effects is structurally forbidden.**
Maestro governs its own resources; it does not govern the internet.

**Two rows differ by who is asked, not by what happens.** A command an agent asks
*Maestro* to run in a resource is mediated; the same command run by the runtime's own
shell is not. The effect is identical and the governance is not, which is why the
classification cannot be read off the action's name.

#### What in-resource actions actually guarantee

**They are not individually policed, and Maestro makes no per-action claim about
them.** The guarantee is **containment, decided at grant time**. It bounds what the
process can do to the resource's own state; whether it can also reach outward is a
separate part of the same grant.

- **An agent holding a lease can do anything the resource permits between mediated
  actions.** That is a property of the design, not a gap in it.
- **Containment is void, not weakened, if the resource's domain is not closed.**
  ADR 0029 §7's general form — *a mediated boundary must be closed under the authority
  it exposes* — applies to a grant exactly as to a fence. An Incubator with a raw
  daemon socket has no containment guarantee to offer at all, so this half of the ADR
  is worth what the resource's closures are worth: in Phase 3, ADR 0029 §1's
  no-ecosystem rule and its decision to give the Incubator no direct daemon route.
- **Stopping such a process is fencing, never deauthorization.** Revoking a lease
  invalidates authorization; a running process needs none.
- **A permissive external grant can cost the resource its fenceability**, not merely
  its policeability: unrevokable external reach removes `isolated` from ADR 0029 §7's
  receipts, and quarantine is the cost.

#### The scope invariant is withdrawn, and stays withdrawn

An earlier draft of the blocker plan proposed prohibiting external agent runtimes
from touching Maestro-managed resources directly. It is withdrawn (delta D3), and
recorded because it will be proposed again: it would disable Claude Code-style
built-in editing unless every internal tool were replaced by a mediated one (Codex),
and it reaches for control over agents that are not Maestro's — an engineer may
legitimately run other agents in a resource, and the application under development
may itself be an agent (DR).

Maestro's enforcement is scoped to **the behavior of Maestro's own agents**.

### 7. Whether an action is mediated is decided by what the resource cannot do

The direct analogue of ADR 0029 §7's rule that domain membership is enforced at
creation and never discovered at fencing time.

**An agent-initiated action is mediated if and only if the Orchestrator is positioned
to refuse it**, and that depends on one thing: whether the execution resource can
perform the action itself. A resource holding a forge credential can push without
asking; one holding data-plane credentials can write without asking. The boundary
does not become weaker — it becomes irrelevant, silently, with no change to any code
implementing it.

So §6's forbidden row is not forbidden by rule. It is forbidden by construction, and
Phase 3 carries the obligation:

- **Credentials for a mediated resource are not placed inside an execution
  resource.** Where a resource must speak a protocol whose mediated form is the real
  action — Git being the obvious case — the credential reaches only a target inside
  the resource's own fencing domain, and the mediated act is the promotion, not the
  local commit.
- **The classification is checkable.** For each action family Phase 3 declares
  mediated, it must say what prevents the resource from performing it directly. A
  family with no answer is mediated in documentation only.

ADR 0022 already states that agents never hold connections or issue queries, and
ADR 0029 §4 already makes the promoted forge commit the sole source and definition
handoff. Each was written about its own resource; stated once over authorization, it
becomes a test a new action family can be held to.

The failure this closes is not an attack. It is an ordinary convenience — mounting a
credential so something works — silently reclassifying an action, with no test failing
and nothing in the boundary's own code to review.

### 8. Recording

The record is [ADR 0022](0022-v2-data-plane.md)'s tool call, encoded per
[ADR 0028](0028-artifact-envelopes-and-payload-schemas.md). **The boundary invents
no record *type*** — but it does need columns Phase 2's does not have, and an
earlier version of this section claimed otherwise.

**`tool_calls` cannot express waiting.** Migration 000005 gives it a non-null
`started_at` with `finished_at` and `succeeded` null while in flight, under a
comment saying an unfinished call is a real state rather than a missing field. That
convention has exactly two positions — in flight, and finished — and it carries no
status column. Under this ADR *in flight* is now at least three different
situations, and conflating them is precisely the ambiguity §4 sets out to remove:

| Situation | What it means | What must happen |
| --- | --- | --- |
| Awaiting an operator | Healthy; gate 2 | Watchdog leaves it alone; the Story is blocked |
| Awaiting a resource | Healthy; gate 3 provisioning or queued for capacity ([ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §2) | Watchdog leaves it alone; no operator is involved |
| Interrupted | The process died between open and completion | Reconciliation; the outcome is `unknown` |

So Phase 3 owes an **additive migration** giving the record explicit nonterminal
states — **at least operator-waiting and resource-waiting** — and A4 owns the
vocabulary, since it owns the action and Story states. The two waits are kept
distinct rather than merged into one "waiting" because they have different
responders, different release rules, and different costs.

**One logical attempt record, opened, possibly waiting, then completed.**

- **Every attempt is opened durably before the effect happens or any result is
  released**, for every mediated action, not only mutating ones. **A read is not
  exempt**: releasing data *is* the security-relevant effect of a retrieval, and a
  crash between disclosure and the open would leave data released with nothing
  recording it.
- **If the open cannot be committed, the action fails closed.**
- **Entering or leaving a wait is a durable transition on that record**, not the
  absence of a completion.
- **Every attempt is completed** — reads included. A mutation's completion carries
  effect details; a read's says whether the result was released. An attempt left open
  in no declared wait reads as *attempted, outcome unknown*, which is what a
  reconciler needs and what §3's at-most-once rule consumes.
- **A denial is terminal and is opened and completed together**, in one transaction,
  with the reason code. There is no effect to await. Denials are the observations
  candidate 12 will be tuned against, so losing them is not a small loss.

**Rejected: record once, after the effect.** That is v1's shape (`toolloop.go:546`),
and a crash between a forge push and its record leaves the plane holding no record of
the push — which reads as never attempted rather than as unknown.

### 9. Relationship to candidate 12

[Candidate 12](../v2/notes_adr-backlog.md) is the gating policy: structural gates
(role, environment and tool allowlists, filesystem scopes), semantic gates, and human
gates. It remains post-MVP. Four constraints:

1. It supplies rules to the hook. It does not extend the boundary, reorder the gates,
   or weaken admission.
2. Its semantic gates consult an agent, because the hook may not infer (§3).
3. Its human gates return *requires an operator* and declare which of §4's scopes they
   permit. They do not compose scopes at the UI.
4. Any argument its rules read must be a field the action schema declares, since the
   record carries only the declared projection and a digest (§3). A rule keyed on
   something unrecordable is a rule whose denials cannot be audited.

MVP ships the boundary with a default-allow hook. The structural allowlist v1 already
has (`registry.go:159`) is subsumed by admission's capability check rather than
reimplemented as policy: a tool absent from the resolved capability set was never
granted.

### 10. Not in scope of this boundary

- **Model invocation.** An LLM call is not an action in ADR 0022's sense — a call that
  produces no tool call does nothing — and it does not pass this boundary. Budget
  enforcement belongs to the execution contract (candidate 13).
- **Orchestrator-initiated work.** Scheduling, dispatch, reconciliation, and cleanup
  are the Orchestrator acting on its own behalf. They are recorded, and they are not
  agent-initiated actions to be refused.
- **Human actions in the UI.** A human principal's Accept is governed by the review
  invariant ([ADR 0020](0020-review-invariant-reviewer-vs-partner.md)) and the
  artifact lifecycle, not by a per-action tool gate.

## Consequences

- **Phase 3 must build the boundary before the tools, not after.** A tool that can
  bypass it is the defect this ADR exists to prevent, and it is cheap to prevent while
  the tool surface is being re-cut. ADR 0029's consequence that roughly three
  agent-visible tools replace fourteen is what makes the timing favourable.
- **The adapted-runtime path stops being a hole.** v1's MCP server executes Maestro
  tools with no record; here an external runtime reaches Maestro's actions only
  through the wire contract (candidate 13), which meets the same boundary as a native
  agent.
- **A blocked Story costs a dormant execution context and no tokens.** A suspended
  goroutine and its in-memory state, nothing more. That is the property that makes
  blocking affordable, and it is why the earlier deny-and-retry design was removed
  rather than refined. It holds only if a blocked Story does **not** consume runnable
  scheduling concurrency — if it did, blocked work would starve independent runnable
  work and the premise would fail. Whether blocked executions count against a
  concurrency limit is therefore an accounting decision this ADR assigns to A4 and
  the Phase 3 plan, not one it leaves implicit.
- **Waiting is cheap; waiting *with resources* is not.** The retention window, and the
  rule for relinquishing a billable resource that is waiting on a human, are Phase 3
  decisions this ADR requires rather than makes.
- **Headless runs degrade honestly.** A gated Story blocks and the run continues;
  the invocation exits with a distinct incomplete result. Golden runs therefore report
  a gate as a gate, not as a failure or a hang.
- **A data-plane outage stops mediated actions and stops neither in-resource work nor
  unmediated external reach.** An agent mid-Story keeps editing files, running its
  toolchain, and calling whatever the network grant permits, and cannot publish,
  deploy, or promote.
- **Fencing gains an obligation**, and **every action family owes a declared commit
  point.** A family whose commit point nobody wrote down cannot be fenced around,
  which makes it the cheapest thing here to skip and the most expensive to have
  skipped.
- **Two writes per executed action, one for a denial, one more per wait entered and
  left, and a data-plane read per action.** No new record *type* — it stays
  `tool_calls` — but Phase 3 owes an additive migration for the nonterminal states,
  because Phase 2's in-flight-versus-finished convention cannot tell waiting from
  interrupted (§8).
- **Audit gains a redaction obligation.** Every action family needs a declared safe
  projection before it can be recorded at all — which is what keeps a `shell` action
  from writing a developer's environment into a queryable, exportable table.
- **Maestro cannot claim per-action governance of what happens inside a resource or
  beyond it, and should not.** The honest summary is three sentences: mediated actions
  are governed and recorded; in-resource actions are contained in the resource's own
  state, and what stops them is fencing; external reach is bounded by the grant made
  at provisioning and by nothing at this boundary.

### Deferred

Policy content of every kind (candidate 12); a policy service or out-of-process policy
engine; filesystem-scope and argument-level rules; risk tiering and approval UX;
reusable Story-scoped grants if MVP defers them, and their storage; approval expiry
and revocation; egress policy and network-grant design; per-action rate limiting and
quotas; policy versioning and simulation ("what would this rule have denied?"); and
any per-action governance of in-resource or external behavior.

### Responsibility split

Recorded because several of the rules above are stated here and owned elsewhere.

| Item | Owner |
| --- | --- |
| The boundary, the three-gate ordering, logical blocking, final revalidation | **This ADR** |
| The nonterminal state vocabulary (at least operator-waiting and resource-waiting), action and Story states, reconnection and restart behavior, the `blocked` terminal result, resource-wait behavior over the wire, and whether a blocked execution counts against runnable concurrency | **A4** (candidate 13) |
| Amendment and cancellation invalidating pending actions and grants | **A5** (ADR 0019 amendment) |
| Watchdog policy for the waiting states; the additive migration that adds them; the headless runner's exit behavior; the retention window and the release rule for a waiting resource | **Phase 3 plan** |
| Which policies exist, their risks, and which approval scopes each gate exposes | **Candidate 12** |

## Related Documents

- [Pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md) item A2 and delta D3;
  [ADR backlog](../v2/notes_adr-backlog.md) candidates 4 (this ADR) and 12 (policy
  content).
- [ADR 0019](0019-orchestrator-boundary.md) (the boundary rule and what is
  Orchestrator machinery), [ADR 0021](0021-artifacts-and-principal-instances.md)
  (principal instances, Audit category), [ADR 0022](0022-v2-data-plane.md) (the tool
  call as the atomic action unit, the persistence seam),
  [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md) (envelope and canonical
  JSON), [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) (§2 leases and
  retention, §7 fencing and late-call rejection, §8 tool routing, capability closure).
- [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) (item A3, reviewed
  as a set with this one).
- [Docker fencing spike](../v2/phase_3/spike_docker-fencing.md);
  [research synthesis](../v2/research_synthesis.md) open question 7;
  [parking lot](../v2/notes_parking-lot.md) tool-level policy gates.
- Historical note [0006](0006-toolloop-process-effect-and-terminal-tools.md) (v1's
  toolloop and terminal-tool discipline, promoted to a data-plane rule by ADR 0022).
