+++
title = "ADR 0030: The Tool Execution Boundary And Its Policy Hook"
edit_date = "2026-08-12"
status = "draft"
summary = "Fixes one mandatory boundary through which every agent-initiated action that crosses back into the Orchestrator must pass, and the single policy hook inside it. The boundary runs in three stages -- deterministic admission (liveness, work version, resource generation, capability containment), then the policy hook, then record-and-effect -- so an empty policy cannot disable an invariant and candidate 12 never reimplements one. Mediation and containment are independent axes: mediation describes the request path and is what the Orchestrator can refuse; containment describes where the effect lands and is decided at grant time. Three of their four combinations occur and the fourth -- an unmediated request with an Orchestrator-side effect -- is forbidden structurally rather than by rule, because whether an action is mediated is decided by what the execution resource cannot do for itself, not by a table. In-resource actions are not individually policed and Maestro makes no per-action claim about them; their guarantee is containment, it is void rather than weakened if the resource's fencing domain is not closed under the authority it exposes, and what stops such a process is ADR 0029's fencing protocol, never deauthorization. The hook is deterministic and fail-closed, may not infer, and a gate needing a human or an agent denies the attempt with a resolution reference instead of suspending inside the boundary; mutating actions record intent before the effect and outcome after, so a lost outcome reads as unknown rather than as nothing."
type = "design"
+++

# 0030. The Tool Execution Boundary And Its Policy Hook

Status: **Proposed** (Claude, 2026-08-12). Item A2 of the accepted
[pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md), which settles the
placement and directs this ADR at the part the original framing missed: the
mediated versus in-resource split and what each mode honestly guarantees.
Drafted concurrently with, and reviewed as a set alongside,
[ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) (item A3).

This ADR resolves [ADR backlog candidate 4](../v2/notes_adr-backlog.md). It fixes
**no policy content**; the gating rules are
[candidate 12](../v2/notes_adr-backlog.md), post-MVP.

The blocker plan calls this the cheapest item in Track A to decide and the most
expensive to defer, because Phase 3 builds the tool plumbing and a seam not
chosen gets retrofitted into every tool. The v1 survey below is the evidence for
that: v1 has five tool-execution call sites and records the Audit action unit on
one of them, which is what "retrofitted into every tool" looks like after the
fact.

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

The consequence is worth stating plainly, because it is the shape Phase 3 must
not reproduce: **the one execution path serving an adapted external agent runtime
records nothing.** Claude Code reaches Maestro's tools over the MCP server, and
that path has no persistence call at all. Any measurement of tool behavior under
that runtime is measuring the tool loop's callers only.

**Authorization is an allowlist resolved at construction, not per action.**
`tools.NewProvider(ctx, allowedTools)` (`pkg/tools/registry.go:139`) captures a set
of tool names, and `Get` refuses a name outside it (`registry.go:159`). That is a
real gate, and it is the right *kind* of gate — but it decides once, on the name
alone. It never sees arguments, it has no notion of which resource or which
generation the call belongs to, and a refusal on one of the four unrecorded paths
leaves nothing behind.

**Recording happens after the effect and cannot fail loudly.**
`LogToolExecution` is called after `Exec` returns (`toolloop.go:546`) and hands
the record to a persistence channel with no response
(`pkg/persistence/persist.go:110`). A process that dies between the side effect
and the record leaves no trace that the action was ever attempted — the failure
mode the [Docker fencing spike](../v2/phase_3/spike_docker-fencing.md) hit from
the other direction, where a failed stop deleted the record a reconciler needed.

### The question this answers, and the question it does not

The [research synthesis](../v2/research_synthesis.md) posed it as open question 7
— *where should policy gates live: toolloop, dispatcher, tool execution layer, or
a separate policy service?* — and the parking lot's
[tool-level policy gates](../v2/notes_parking-lot.md) entry deferred the
implementation post-MVP while asking that the contracts leave a seam.

This ADR is that seam and nothing more. What structural, semantic, and human
gates actually check is candidate 12.

### What this ADR must satisfy

- [ADR 0019](0019-orchestrator-boundary.md): tool implementation, routing,
  persistence, and deterministic gate evaluation are Orchestrator machinery.
  Anything requiring inference is an agent.
- [ADR 0022](0022-v2-data-plane.md): the tool call is the atomic Audit action
  unit, and any LLM output that creates artifacts, decisions, or state
  transitions must pass through a tool/action record — parsed free text can never
  be a side door.
- [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §7 requirement 5:
  a call issued by a fenced holder is **rejected at the boundary** even if it
  arrives late. That boundary is this one.
- [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §8: Maestro's
  tools target a resource reference, never an Agent-derived local path — binding
  on Maestro's own agents, and not a claim of control over arbitrary processes
  inside a resource.
- [ADR backlog candidate 13](../v2/notes_adr-backlog.md) (item A4): native Go
  agents and adapted external agents reach these resources only through the wire
  contract, so both meet this boundary.

## Decision

### 1. One mandatory boundary, at the Orchestrator's tool-execution seam

Every **agent-initiated action that crosses back into the Orchestrator** passes
through a single execution boundary: after the invocation's capabilities are
resolved, and before the side effect.

Not in the tool loop, not in the dispatcher, not in individual tools, and not a
separate policy service. The blocker plan settled this and it is not reopened
here. What matters for Phase 3 is the adjective: **mandatory**. The boundary is
the only route to the effect, structurally — not a function every tool is
expected to call. A tool that can be executed without traversing it is a defect
in the same class as v1's four unrecorded call sites, and Phase 3 must be able to
demonstrate the property rather than assert it.

The seam is one place for a reason that outlives policy: it is where the action
identity, the principal, the work version, the resource generation, and the
arguments are all simultaneously in hand. Nothing upstream has the resource
generation; nothing downstream has the principal.

### 2. The boundary has three stages, and the hook is only the middle one

An empty policy must not be able to disable an invariant, and candidate 12 must
not have to reimplement one. So the boundary is staged, in this order:

1. **Admission — deterministic, Orchestrator-owned, not policy.** The principal
   instance is live; the invocation's work version is still the effective one; the
   generation of every referenced execution resource is current; the action is
   contained in the invocation's resolved capability set; the referenced resources
   are leased to this execution. Every one of these is a rule-and-config decision
   under ADR 0019 and none of them is negotiable by a policy implementation.
2. **Policy — the hook.** The single extension point. In MVP it is a
   default-allow implementation carrying no rules. Candidate 12 fills it.
3. **Record and effect.** Per §6.

Admission is where [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md)
§7's fifth requirement is discharged, and it is the reason the stage ordering is
part of the contract rather than an implementation detail. A late call from a
fenced holder is rejected here, before any policy runs, because the receipt that
fenced it is a fact about the world and not a rule someone may configure away.

**The generation check reads authoritative state, not a cached copy.** A boundary
that trusts the generation recorded in the invocation cannot reject a late call,
because the invocation is exactly what the fenced holder still holds. That is a
data-plane read per mediated action, and it is the price of the guarantee rather
than an inefficiency to optimize away. It is affordable because mediated actions
are comparatively rare — see §4.

### 3. The interface

**The decision request** carries:

| Field | Why it is here |
| --- | --- |
| **Action identity** — a stable kind and verb from an Orchestrator-owned vocabulary | Not the caller's tool name. An adapted external runtime brings its own names for the same action (`Edit` against `file_edit`), and candidate 13 makes such runtimes first-class, so policy keyed on the caller's vocabulary would be runtime-specific by construction |
| **Principal instance** and role | [ADR 0021](0021-artifacts-and-principal-instances.md); the accountable identity, and what the record attributes to |
| **Work scope and version** — the lineage tuple and the version the execution is bound to | Version-bound dispatch ([ADR 0019](0019-orchestrator-boundary.md) as amended); admission compares against the effective version |
| **Resource references** — type, identity, and **generation** | [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §5 and §7 |
| **Resolved capability set** | The hook must see what was granted, or it cannot reason about the action it is being asked to allow |
| **Normalized arguments**, in the canonical JSON form of [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md) | A decision must be over a determinate value, and a decision record must be digestible |
| **Attempt identity** | Correlates the decision with its records, and makes a retried action distinguishable from a repeated one |

**The decision** is two-valued: allow, or deny with a machine-readable reason code
and a human-readable reason. Nothing else.

Four properties of the hook are fixed here, because each is expensive to add
later and cheap to require now.

**It is deterministic and may not infer.** The hook is Orchestrator machinery, so
ADR 0019's boundary rule applies to it directly: it decides from rules and
configuration. This has a consequence for candidate 12 that is better stated now
than discovered then — **a semantic gate is an agent.** Candidate 12's
"high-risk action summaries checked against policy" requires judgment, so it
cannot be implemented inside the hook. It is a gate that consults an agent, and
the boundary's synchronous decision cannot wait for one.

**It performs no side effects.** A hook that writes is a second, unrecorded
action path.

**It is fail-closed.** A hook that errors, or exceeds its bound, denies. A policy
layer that fails open supplies no guarantee at all, and with a default-allow MVP
implementation it would be easy to make an error indistinguishable from an
allowance. Admission fails closed for the same reason, and there the consequence
is real rather than theoretical: **if the data plane is unreachable, mediated
actions stop**, because neither the effective version nor the current generation
can be established and neither can be assumed. That is correct, and it is an
availability property Phase 3 should meet deliberately rather than discover.

**A gate that needs a human or an agent denies the attempt; it never suspends
inside the boundary.** The resolution reference travels in the reason, and a
resumed action is a **new attempt** through the same boundary, not the same one
continued. This is the one thing about candidate 12 that must be settled now: the
alternative — blocking inside the hook while approval is sought — holds a lease,
an execution slot, and an unbounded amount of wall-clock inside a synchronous
call, and unwinding that later means changing every call site's control flow.
Waiting belongs to scheduling, which is Orchestrator machinery of a different
kind. Deciding this now costs a sentence; deferring it costs the retrofit the
blocker plan names.

### 4. Mediation and containment are independent axes

The blocker plan states the split as two modes. They are better read as two
questions with independent answers, because conflating them produces a specific
false conclusion — that a mediated action's *effects* are policed.

- **Mediation** is a property of the **request path**: can the Orchestrator refuse
  this action? It is what §1's boundary governs.
- **Containment** is a property of the **effect site**: where does the effect
  land, and which authority bounds it? It is decided at grant time and enforced at
  the resource boundary.

Three combinations occur. The fourth is forbidden.

| Request | Effect lands | Policed per action | Recorded | Guarantee |
| --- | --- | --- | --- | --- |
| Mediated | Orchestrator-side — data-plane writes, artifact publication, forge operations, resource lifecycle, retrieval, message routing | Yes | Yes | The action happens only if admission and policy allow it, and the record survives it |
| Mediated | In-resource — an execution contract, or a command Maestro is asked to run in a resource | **The decision to run it, only** | Yes | The Orchestrator governs *whether* the command runs; it makes no claim about what the command then does |
| Not mediated | In-resource — the agent runtime's own built-in tools (its editor, its shell), and any other process running in the resource | No | No | Containment alone (below) |
| Not mediated | **Orchestrator-side** | — | — | **Must not exist** (§5) |

**Rows two and three differ by who is asked, not by what happens.** Running a
shell command appears on both sides of the table, and that is the point rather
than an ambiguity: a command an agent asks *Maestro* to run in a resource is a
mediated request, and the same command run by the agent runtime's own built-in
shell is not. The effect is identical and the governance is not, which is why the
classification cannot be read off the action's name — and why §5 matters, since
what decides the row is whether the agent had to ask.

The second row is the one that repays the distinction. An execution contract is a
mediated request with an in-resource effect: the boundary sees `run the test
contract`, admits it, records it, and the test then does whatever the repository's
test suite does inside the resource. Reading "mediated" as "policed" would suggest
the second half is governed. It is not.

#### What in-resource actions actually guarantee

**They are not individually policed, and Maestro makes no per-action claim about
them.** The guarantee is **containment, decided at grant time**: the grant defines
the blast radius, and enforcement lives at the resource boundary rather than in a
per-action gate.

Three consequences, stated as narrowly as they really hold:

- **An agent holding a lease can do anything the resource permits between
  mediated actions.** That is a property of the design, not a gap in it. A5's
  cancellation semantics are built on the honest version of this statement rather
  than the aspirational one.
- **Containment is void, not weakened, if the resource's domain is not closed.**
  ADR 0029 §7's general form — *a mediated boundary must be closed under the
  authority it exposes* — applies to the grant exactly as it applies to a fence.
  The [Docker spike](../v2/phase_3/spike_docker-fencing.md) demonstrated a
  containment claim that was true about the domain it enumerated and useless
  because the escape was never in it. An Incubator with a raw daemon socket has no
  containment guarantee to offer at all. So the in-resource half of this ADR is
  worth exactly what the resource's closures are worth, and in Phase 3 those are
  ADR 0029 §1's no-ecosystem rule and its decision to give the Incubator no direct
  daemon route.
- **Stopping such a process is fencing, never deauthorization.** Revoking a lease
  invalidates authorization; a process already running needs none. See ADR 0029
  §7.

#### The scope invariant is withdrawn, and stays withdrawn

An earlier draft of the blocker plan proposed prohibiting external agent runtimes
from touching Maestro-managed resources directly. It is withdrawn (delta D3), and
recorded here because it will be proposed again:

- It would disable Claude Code-style built-in editing unless every internal tool
  were replaced by a mediated one (Codex).
- It reaches for control over agents that are not Maestro's to control: an
  engineer may legitimately run other agents inside a resource, and the
  application under development may itself be an agent (DR).

Maestro's enforcement is scoped to **the behavior of Maestro's own agents**. That
is what ADR 0029 §8's tool-routing rule binds, and it is all it binds.

### 5. Whether an action is mediated is decided by what the resource cannot do

This is the finding that makes the table in §4 more than a taxonomy, and it is the
direct analogue of ADR 0029 §7's rule that domain membership is enforced at
creation and never discovered at fencing time.

**An agent-initiated action is mediated if and only if the Orchestrator is
positioned to refuse it.** Whether it is so positioned depends on one thing:
whether the execution resource can perform the action itself. A resource holding a
forge credential can push without asking. A resource holding data-plane
credentials can write without asking. In neither case does the boundary become
weaker — it becomes irrelevant, silently, with no change to any code that
implements it.

So the fourth row of §4's table is not forbidden by rule. It is forbidden by
construction, and Phase 3 carries the obligation:

- **Credentials for a mediated resource are not placed inside an execution
  resource.** Where an execution resource must speak a protocol whose mediated
  form is the real action — Git being the obvious case — the credential reaches
  only a target inside the resource's own fencing domain, and the mediated act is
  the promotion, not the local commit.
- **The classification is checkable.** For each action family Phase 3 declares
  mediated, it must be able to say what prevents the resource from performing it
  directly. A family with no answer is mediated in documentation only.

Neither half of this is new as a rule; what is new is the general form and the
obligation to check it. [ADR 0022](0022-v2-data-plane.md) already states that
agents never hold connections or issue queries, and
[ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §4 already makes
the promoted forge commit the sole source and definition handoff. Each was
written about its own resource. Stated once, over authorization rather than over
storage or handoff, it becomes the test a new action family can be held to.

The failure this closes is not an attack. It is an ordinary Phase 3 convenience —
mounting a credential so something works — silently reclassifying an action and
leaving the promotion record incomplete, with no test failing and nothing in the
boundary's own code to review.

### 6. Recording

The record is [ADR 0022](0022-v2-data-plane.md)'s tool call — the atomic Audit
action unit — encoded per [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md).
The boundary invents no record type.

- **Every decision is recorded, allow or deny.** A denial that leaves no record is
  invisible, and denials are the observations candidate 12 will be tuned against.
- **A mutating action records intent before the effect and outcome after**, both
  bound by the attempt identity. An action whose outcome record is missing then
  reads as *attempted, outcome unknown* rather than as nothing at all — which is
  what a reconciler needs, and is the same lesson the Docker spike recorded about
  an unconfirmed stop that must not destroy the provider record.
- **A non-mutating action records once.** The deciding question is whether the
  action can change state outside the boundary; a retrieval cannot, so it pays one
  write, not two.

The cost is one extra write per mediated mutation, and it is bounded by the split
in §4 rather than by the action rate: in-resource actions — every file edit and
every command the agent's own runtime runs — pay nothing here.

**Rejected: record once, after the effect.** That is v1's shape
(`toolloop.go:546`), and it loses the window that matters. A crash between a forge
push and its record leaves the plane holding no record of the push, which reads
as never attempted rather than as unknown.

### 7. Relationship to candidate 12

[Candidate 12](../v2/notes_adr-backlog.md) is the gating policy: structural gates
(role, environment and tool allowlists, filesystem scopes), semantic gates, and
human gates. It remains post-MVP. Three constraints this ADR imposes on it:

1. It supplies rules to the hook. It does not extend the boundary, reorder the
   stages, or weaken admission.
2. Its semantic gates consult an agent, because the hook may not infer (§3).
3. Its human gates deny with a resolution reference; they do not suspend inside
   the boundary (§3).

MVP ships the boundary with a default-allow hook. The structural allowlist v1
already has (`registry.go:159`) is subsumed by admission's capability check rather
than reimplemented as policy: a tool absent from the resolved capability set is
not denied by a rule, it was never granted.

### 8. Not in scope of this boundary

Stated so the boundary is not later read as universal:

- **Model invocation.** An LLM call is not an action in ADR 0022's sense — a call
  that produces no tool call does nothing — and it does not pass this hook. It is
  recorded in the calls family for cost and trace. Budget enforcement belongs to
  the execution contract (candidate 13), not here.
- **Orchestrator-initiated work.** Scheduling, dispatch, reconciliation, and
  cleanup are the Orchestrator acting on its own behalf. They are recorded, and
  they are not agent-initiated actions to be refused.
- **Human actions in the UI.** A human principal's Accept is governed by the
  review invariant ([ADR 0020](0020-review-invariant-reviewer-vs-partner.md)) and
  the artifact lifecycle, not by a per-action tool gate.

## Consequences

- **Phase 3 must build the boundary before the tools, not after.** Every mediated
  action reaches its effect through one seam; a tool that can bypass it is the
  defect this ADR exists to prevent, and it is cheap to prevent while the tool
  surface is being re-cut and expensive afterwards. ADR 0029's consequence that
  roughly three agent-visible tools replace fourteen is what makes the timing
  favourable.
- **The adapted-runtime path stops being a hole.** v1's MCP server executes
  Maestro tools with no record; under this ADR an external runtime reaches
  Maestro's actions only through the wire contract (candidate 13), which meets the
  same boundary as a native agent. This is the single largest observability change
  the boundary buys, and it is why the split is stated as mediation rather than as
  "Maestro tools versus other tools."
- **A data-plane outage stops mediated actions and does not stop in-resource
  work.** An agent mid-Story keeps editing files and running its toolchain, and
  cannot publish, deploy, or promote. This follows from fail-closed admission plus
  the split, and it is a defensible posture — but it must be designed for, since
  the agent experiences it as tools failing rather than as a system pause.
- **Per-action policy becomes a configuration change rather than a code change.**
  That is the whole point of choosing the seam now: candidate 12 lands as rules
  behind an existing hook.
- **Two records per mediated mutation, and a data-plane read per mediated action.**
  Named as a real cost, bounded by how few mediated actions there are relative to
  in-resource ones.
- **Maestro cannot claim per-action governance of what happens inside a
  resource, and should not.** Any external statement about Maestro's controls has
  to distinguish the two modes, or it overclaims. The honest summary is: mediated
  actions are governed and recorded; in-resource actions are contained, and what
  stops them is fencing.

### Deferred

Policy content of every kind (candidate 12); a policy service or out-of-process
policy engine; filesystem-scope and argument-level rules; risk tiering and human
approval UX; per-action rate limiting and quotas; policy versioning and
simulation ("what would this rule have denied?"); and any per-action governance of
in-resource behavior.

## Related Documents

- [Pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md) item A2 and delta D3;
  [ADR backlog](../v2/notes_adr-backlog.md) candidates 4 (this ADR) and 12
  (policy content).
- [ADR 0019](0019-orchestrator-boundary.md) (the boundary rule and what is
  Orchestrator machinery), [ADR 0021](0021-artifacts-and-principal-instances.md)
  (principal instances, Audit category),
  [ADR 0022](0022-v2-data-plane.md) (the tool call as the atomic action unit, the
  persistence seam), [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md)
  (envelope and canonical JSON),
  [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) (§7 fencing and
  late-call rejection, §8 tool routing, the capability-closure rule).
- [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) (item A3,
  reviewed as a set with this one).
- [Docker fencing spike](../v2/phase_3/spike_docker-fencing.md) (capability
  closure, and a record destroyed on the failure path);
  [research synthesis](../v2/research_synthesis.md) open question 7;
  [parking lot](../v2/notes_parking-lot.md) tool-level policy gates.
- Historical note [0006](0006-toolloop-process-effect-and-terminal-tools.md) (v1's
  toolloop and terminal-tool discipline, promoted to a data-plane rule by ADR
  0022).
