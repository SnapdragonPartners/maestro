+++
title = "ADR 0032: The Agent Execution Contract"
edit_date = "2026-08-15"
status = "live"
summary = "Defines the versioned wire contract every agent reaches Maestro through, so a native Go agent and an adapted external runtime meet one boundary and neither receives a local path or a database connection. An invocation splits into an immutable, persisted execution configuration and per-incarnation bindings, because resource grants and resume tokens change while the configuration must not. The terminal result is four independent axes rather than one status list, so an already-satisfied Story, a superseded cancellation, a gate no operator can answer, and an infrastructure failure stop colliding. Every Orchestrator-forced ending closes admission, drains the actions it admitted, fences the resource, and only then records a result, and recovery is artifact-level: an agent restarts from the last committed workflow artifact rather than from where it stopped. A post-acceptance amendment fixes which decisions bind -- the boundary, the identities, the four axes with their applicability rule, mediated actions, and the fencing preconditions -- and returns the execution FSM, re-attach, delivery mechanism, question-wait lifecycle and reusable approvals to Phase 3 as design inputs rather than settled requirements."
+++

# 0032. The Agent Execution Contract

Status: **Accepted** (Codex + DR, 2026-08-15). Drafted by Claude 2026-08-13 and revised through eight review rounds. Item A4 of the accepted
[pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md), and the last design
item on the critical path to phase entry. It consumes
[ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) (Accepted first, by
the plan's D2), and is drafted against
[ADR 0030](0030-tool-execution-policy-hook.md) and
[ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md), each of which
hands it a named list of obligations.

This ADR resolves [ADR backlog candidate 13](../v2/notes_adr-backlog.md) and the
contract portion of [issue #282](https://github.com/SnapdragonPartners/maestro/issues/282).
It also absorbs the contract portion of
[#272](https://github.com/SnapdragonPartners/maestro/issues/272); the routing
implementation remains a Phase 3 item.

**What it is deliberately not.** It fixes no policy content (candidate 12), no
amendment policy (A5), no knowledge base (candidate 10), and no GitHub Actions
presentation. §12 states the boundary in full.

## Context

### What v1 actually has

Verified against the frozen v1 tree at `62ecac6`. None of it is a v1 defect to fix
— v1 is frozen (CLAUDE.md) — and all of it is a requirement on Phase 3.

**There is no agent *execution* contract.** v1 is not short of protocols —
`pkg/proto` carries the typed dispatch protocol of
[note 0004](0004-channel-dispatch-and-typed-agent-protocol.md), and the MCP
server speaks JSON-RPC — but neither describes *invoking an agent and receiving
its outcome*. The nearest thing to that is `pkg/coder/claude`, which runs an
external agent as a subprocess. What it has instead of a contract is four
separate shapes that each carry part of the job, and the way they disagree is the
argument for the four axes in §5.

**The result type is a role-shaped union.** `claude.Result`
(`pkg/coder/claude/types.go:94`) carries `Plan`, `Summary`, `Evidence`,
`ExplorationSummary`, `Question`, and `ContainerSwitchTarget` — one field per
signal, so a new role or a new outcome adds a field to a struct its callers
already switch over.

**One enum holds four different kinds of fact.** `claude.Signal`
(`types.go:21`) is a single list containing an execution outcome (`ERROR`,
`TIMEOUT`), a completion disposition (`STORY_COMPLETE` — "the story was already
implemented"), a **nonterminal wait** (`QUESTION`), and a **lifecycle request**
(`CONTAINER_SWITCH`, which asks the runner to restart elsewhere). `INACTIVITY` is
a fifth kind: an observation about the transport. This is the flattening §5
undoes, and it is worth noting that v1 arrived at these distinctions
independently — they are real, and only their encoding is wrong.

**The already-satisfied disposition exists in v1 and is discarded as a record.**
`SignalStoryComplete` is produced by the `done` tool on an empty diff
(`pkg/tools/build_tools.go:469`), routes the Coder past `TESTING` straight to
`CODE_REVIEW` (`pkg/coder/coding.go:247`, `pkg/coder/claudecode_coding.go:215`),
and is stored as free text in `KeyCompletionDetails`. So it survives as a
**control-flow branch** and as prose, and the structure that would carry it
onward does not exist: `claude.Result` is per-run and in-memory, and the state
data it lands in is a string. What follows from that is
[#280](https://github.com/SnapdragonPartners/maestro/issues/280)'s own report —
the Story is recorded as an ordinary completion and reads as a false negative.
The distinction was drawn and then dropped, which is a more specific defect than
one nobody drew.

**The terminal signal travels by side channel, and two sources disagree.** The
MCP server keeps a single mutex-guarded slot, `lastEffect`
(`pkg/coder/claude/mcpserver/server.go:34`), overwritten by every tool call that
returns a `ProcessEffect` (`:430`). After the process exits, the runner consumes
that slot to **correct** the signal its own stdout parser inferred from a tool
name (`pkg/coder/claude/runner.go:199-206`). So the authoritative outcome is
reconstructed after the fact from two channels that can disagree, one of which
retains only the most recent value.

**The invocation carries an Agent-derived local path.** `RunOptions.WorkDir`
(`types.go:50`) is set from the Coder's own `workDir`
(`pkg/coder/claudecode_coding.go:287`), which is a constructor argument to
`NewCoder` (`pkg/coder/driver.go:470`). This is exactly what
[ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §8 prohibits, and
it is the field a fenced resource reference replaces.

**Nothing carries a protocol version.** Neither `RunOptions` nor `Result` has
one. The MCP server reports `"version": "1.0.0"` (`server.go:296`) as its own
`serverInfo`, which is MCP's identity rather than Maestro's contract.

**The loop's outcome vocabulary conflates control with result.**
`toolloop.OutcomeKind` (`pkg/agent/toolloop/outcome.go`) has eight values mixing
loop-level guards (`NoToolTwice`, `IterationLimit`, `MaxIterations`), transport and
process facts (`LLMError`, `GracefulShutdown`), a work outcome (`ProcessEffect`),
and a classified failure (`Blocked`). Each is a real fact; they are not the same
kind of fact.

**And v1 has agent state machinery, which this ADR does not replace.** The first
draft's inventory omitted it — which is how §6 came to describe an execution
lifecycle without noticing what it sat beside. `pkg/proto` defines five generic
states: `DONE`, `ERROR`, `WAITING`, `QUESTION`, and `SUSPEND`
(`pkg/proto/message.go:634-644`). Each role FSM extends them with its own
vocabulary and its own transition table (`pkg/coder/coder_fsm.go`, and the
equivalents in `pkg/architect` and `pkg/pm`). `BaseStateMachine`
(`pkg/agent/internal/core/machine.go:81`) holds the current state, an untyped
`StateData map[string]any`, transition history, a retry count, and an
instance-local `TransitionTable`.

**Its persistence is untyped and not crash-safe.** `StateStore` (`machine.go:70`)
is `Save(id string, value any)` and `Load(id string, dest any)`. `Persist()`
(`machine.go:307`) marshals a four-key map — `current_state`, `state_data`,
`transitions`, `retry_count` — and the only implementation
(`pkg/state/store.go:127`) is `json.MarshalIndent` plus `os.WriteFile` at mode
`0644`, one file per agent ID, with no temporary file, no rename, and no fsync. A
crash during the write truncates the record it was meant to protect. **This is
what a typed v2 durable checkpoint replaces**, and it is a better argument for
one than anything in §6.

**Two of those five states are concepts this contract also names.** `QUESTION` is
a Coder waiting for the Architect's answer; the Coder FSM redeclares the same
string locally (`pkg/coder/coder_fsm.go:21`) and routes `PLANNING` and `CODING`
into it and back out (`:139`, `:146`, `:164`). `SUSPEND` stores its originating
state under `KeySuspendedFrom`, and `HandleSuspend` (`machine.go:556`) returns the
agent **to that exact state** on a restore signal, or to `ERROR` after fifteen
minutes (`DefaultSuspendTimeout`, `machine.go:31`). That is precise
return-to-origin resumption, over a process-local channel no restart survives —
the opposite of the artifact-level recovery §6 chose. Phase 3 settles that
conflict; it must not duplicate it.

**And the transport choice was forced once already.** The MCP server listens on
TCP rather than a Unix socket because "Unix sockets don't work through Docker
Desktop's file sharing on macOS" (`server.go:1-6`). §10 owes that constraint an
answer rather than rediscovering it.

### What Phase 2 already built, and where it stops

Checked before specifying any shape, because two of ADR 0030's rounds were spent
on shapes the schema already had or already lacked.

- `principal_instances` (migration 000004) carries `model`, `prompt_pack_id`,
  `prompt_hash`, `harness_config_hash`, and `maestro_version` — the MPH signature
  ([ADR 0021](0021-artifacts-and-principal-instances.md)) — with `model` NOT NULL
  for every principal kind. §3 and §9 attach to these columns rather than
  proposing new identity.
- `llm_calls` (migration 000005, amended by 000011 and 000016) carries `provider`
  and `model` as plain text, four token axes, and a nullable `cost_usd` whose
  comment records Phase 1's lesson that pricing an unknown model at zero is worse
  than reporting it unavailable. §3's identity split lands on those two text
  columns.
- `tool_calls` (migration 000005) is the atomic Audit action unit, links back to
  its originating LLM call under a four-column foreign key, and has **no status
  column** — which ADR 0030 §8 already establishes.

**One thing ADR 0030 §8 did not reach, and it changes the migration it
asked for.** `tool_calls` carries
`CONSTRAINT tool_calls_finished_check CHECK ((finished_at IS NULL) = (succeeded IS NULL))`.
Settling an attempt therefore requires a boolean `succeeded`, and ADR 0030 §8's
own reconciliation outcome — *attempted, outcome unknown* — is neither true nor
false. So the record cannot express the state that section requires of it, and the
Phase 3 migration must **replace that constraint**, not merely add a status
column. ADR 0030 §8 calls the migration "additive"; at the constraint level it is
not, and saying so is cheaper now than discovering it against a populated table.

### What this ADR must satisfy

- [ADR 0019](0019-orchestrator-boundary.md): agent lifecycle, routing, tools,
  persistence, and scheduling are Orchestrator machinery. Anything requiring
  inference is an agent's. Resolution and dispatch are rules-and-configuration
  decisions.
- [ADR 0021](0021-artifacts-and-principal-instances.md): artifacts are the sole
  agent handoff; the principal instance is the accountable identity and carries
  the MPH signature; the seeding set is recorded in `principal_instance_inputs`.
- [ADR 0022](0022-v2-data-plane.md): agents never hold connections or issue
  queries; the tool call is the atomic action unit; anything that creates
  artifacts, decisions, or state transitions passes through an action record.
- [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md): canonical JSON and
  the code-resident payload type registry, for anything this contract digests or
  publishes.
- [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §2 (execution
  scoped to the Story; leases, retention claims, and per-type capacity), §5 (three
  identities, of which the **instance generation** is the fencing identity), §7
  (fencing, and the rejection of late calls at the boundary), §8 (tools target a
  resource reference, never an Agent-derived path; contracts declare where they
  run).
- [ADR 0030](0030-tool-execution-policy-hook.md): every mediated action meets one
  boundary. Its responsibility split assigns this ADR the tool call's nonterminal
  state vocabulary, action states, reconnection and restart behavior, the
  `blocked` terminal result, resource-wait behavior over the wire, and the
  concurrency accounting for a blocked execution.
- [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §2 and §3: the
  invocation carries pack name, scheme-qualified digest, content reference, and
  installation identity and revision; and this ADR owes four invocation-provenance
  obligations §3 deliberately gave no home.

## Status Of Decisions

**Amended 2026-08-15 (Codex + DR), after acceptance.** This section is normative
and **controls every section below it**. Where a decision section states a
provisional item in binding terms, that language is superseded here and the item
is a design input. Nothing below is deleted, so the reasoning remains readable as
what it is: the argument that produced a proposal.

**Why.** A4's job was to stop Phase 3 inheriting an undefined boundary. It was not
to implement Phase 3 in miniature, and parts of it did. The conformance slice
validated an **isolated contract model**, not integration with Maestro's agent
framework — whose state machinery had not been inventoried when the sections below
were written, and is recorded in the Context above only now. Mechanism reasoned
forward from a model, with no consumer to answer to, is a proposal rather than a
settled decision.

### Binding

- A **versioned, language-neutral invocation and terminal-result boundary**.
- **Explicit identities**: principal, role, prompt pack, execution resource, and
  the model's **route, served identity, and underlying identity as three distinct
  concepts** (§3).
- **Fenced resource references and capabilities**, never a local workspace path.
- **No agent access to the data plane.**
- **Centralized mediated actions**, each with a durable intent record and a
  durable result record.
- **The four independent terminal-result axes, together with the applicability
  rule that makes invalid combinations unrepresentable** (§5). The axes without
  the rule are a cross product, most of whose members are incoherent.
- **Cancellation requires that admitted actions drain and the resource be fenced
  before a positive terminal result is recorded** (§6).
- **A request carrying superseded or fenced execution authority is rejected at
  every mediated boundary.** The requirement is binding. **Epochs — and any other
  mechanism for detecting that authority — are not.**
- **The required provenance facts** (§9), without prescribing a delivery
  mechanism for them.

### Provisional design inputs

Phase 3 settles these against real consumers. They are deliberately **not**
replaced with another speculative design here.

- The complete execution FSM.
- Restart, resume, re-attach, and outstanding-action enumeration.
- Epochs, acknowledgements, watermarks, and durable sender outboxes.
- The generic question-wait lifecycle.
- Durable reusable approvals.
- Any persistence family implied solely by one of the above.

### Not reclassified

Anything named in neither list keeps the status it had at acceptance. Three items
are called out because their omission from both lists is easy to read as a
demotion and is not one: **§7's two-limit concurrency accounting** for a blocked
execution, **§8's capability model** beyond the "capabilities, not paths"
statement already binding above, and **§2's split of the invocation into an
immutable persisted configuration and per-incarnation bindings** — which rests on
gate 3 replacing resources *mid-execution*, not on restart, so a single immutable
record was never available regardless of how recovery works.

That last one needs a boundary drawn inside it: the **split** is binding, while
two of the things §2 puts in the bindings — the **epoch** and the **resume
token** — belong to the provisional list and are examples of what bindings might
carry rather than required fields.

### What survives the demotion, and why

A **durable tool/action record** remains necessary — for Audit
([ADR 0022](0022-v2-data-plane.md)'s atomic action unit), for the operator
decision, for fencing ([ADR 0030](0030-tool-execution-policy-hook.md) §5 cannot
issue a positive receipt without knowing whether an admitted action passed its
commit point), and so that a re-requested action does not commit a second effect.

**It is not agent recovery state, and it does not justify a parallel agent FSM.**
Recovery is artifact-level: an agent restarts from the last committed workflow
artifact, not from where it stopped.

## Decision

### 1. One contract, two runtime kinds, and what is normative

**Every agent reaches Maestro through one versioned wire contract.** A native Go
agent and an adapted external runtime are two implementations of one boundary, not
two boundaries. This is what makes ADR 0030's claim true rather than aspirational:
its gates cannot be the only route to a mediated effect if some agents reach the
effect another way.

**Normative:** the message kinds and their fields, the meaning of each field, the
state machine over an invocation, the terminal result schema, the compatibility
rules, and the requirement that mediated actions be requested through the contract
rather than performed directly.

**Not normative:** the Go types, the framing, the process model, and the local
transport's specifics. Those are §10's, and §10 is the one place this ADR expects
Phase 3 to substitute an equivalent without renegotiating the contract.

**Wire messages are not artifacts.** They are encoded as JSON and they borrow
ADR 0028's canonical-JSON discipline wherever this contract digests something, but
they do not enter its payload type registry and they are not persisted as
envelopes. Artifacts are what an execution *publishes*, through a mediated action.
Conflating the two would make every progress event a durable record.

**The adapter is the contract endpoint, not the runtime.** For an adapted external
runtime the contract is spoken by an adapter process that owns the contract
channel and drives the runtime separately. This is not a stylistic preference: a
runtime like Claude Code writes its own structured output to its stdout, so a
design in which the runtime itself spoke the contract on stdout would interleave
two protocols on one stream. Stating it here keeps `RunOptions`-shaped coupling
from reappearing inside an adapter.

**Four concepts stay distinct** and none is derivable from another: the **role**
(what the work needs), the **principal instance** (who is accountable,
ADR 0021), the **adapter and executable** (what implements the runtime), and the
**execution resource** (where effects land, ADR 0029). One Story may involve
several principals over one Incubator; one adapter may serve several roles; a
harness may collapse roles onto one implementation. The contract carries all four
because collapsing any pair loses a distinction some configuration needs.

### 2. The invocation is two halves, and only one of them is immutable

**Everything requiring a decision is decided before dispatch; the runtime looks
nothing up.** That follows from ADR 0019 — resolution is rules, not judgment — and
from ADR 0031 §4, which fixes pack resolution at dispatch for the same reason.

But "the invocation is immutable and reused verbatim" is **false as a statement
about the whole message**, and a first draft asserted it while listing two fields
that contradict it. ADR 0030's gate 3 acquires or replaces a resource *during* an
execution, so a resource generation is a snapshot rather than a constant; and a
resume token exists only on the second and later incarnations. Calling that
structure immutable would have made either the rule or the fields wrong.

So the invocation is **two records with two lifetimes**:

#### The execution configuration — immutable, persisted, reused verbatim

| Field | Why it is here |
| --- | --- |
| **Execution identity** | The key every event, action, and record belongs to |
| **Principal instance** and **role** | ADR 0021's accountable identity; the role names what the work needs, not who implements it |
| **Work scope and effective version** | Version-bound dispatch ([ADR 0019](0019-orchestrator-boundary.md) as amended). ADR 0030's admission gate compares each action against this |
| **Seeding artifact references** | The Management artifacts this instance starts with, recorded in `principal_instance_inputs` (ADR 0021). References, never inlined content |
| **Model route and served identity** | §3 |
| **Prompt pack** — name, scheme-qualified digest, content reference, installation identity and revision | [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §2. The name is a label, carried for humans and for validation, never dereferenced |
| **Resolved capability set** | §8. Orchestrator-owned action identities, not the runtime's tool names |
| **Budgets and limits** | Token, cost, wall-clock, and iteration bounds. ADR 0030 §10 puts budget enforcement here rather than at its boundary, because an LLM call is not a mediated action |
| **Operator-responder availability** | ADR 0030 §4: headless is a **declared configuration known at dispatch**, never an observation that nobody answered. It describes the run, not the moment |
| **Expected source contract** | §9, and [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §3 obligation 1 |

**It is persisted.** This is what makes "restart reuses the invocation" reachable
rather than aspirational: a restarted **Orchestrator** must be able to reissue the
configuration without re-resolving it, and it cannot do that from memory it no
longer has. Re-resolving would let a configuration edit between the crash and the
restart move a factory lever mid-Story with nothing recording that it happened.

#### The bindings — per incarnation, refreshed as the execution runs

| Field | Why it is separate |
| --- | --- |
| **Epoch** | Identifies this incarnation and orders events across restarts (§4). Assigned by the Orchestrator |
| **Fenced resource references** — for the Incubator and, where the work requires one, the Habitat: the reference and its **instance generation** | [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §5 and §8. Never a local path — and never immutable, because gate 3 may acquire or replace the resource |
| **Resume token**, where the runtime declared itself resumable | Opaque to the Orchestrator (§6). Absent on a first incarnation by construction |

**Refreshing a binding is not amending the execution.** A replaced resource is a
new grant under the same configuration, and the configuration is what a restart
reuses verbatim. Keeping them in one record would have meant either treating a
routine reprovision as a new dispatch, or calling something immutable that
demonstrably changes.

**A change to the configuration is a new dispatch.** ADR 0031 §4 fixes this for
the pack and gives the reason generally: a lever that changes under a running
execution is a silent swap with no version behind it.

**What neither half may ever carry**, each because something in the accepted set
forbids it:

- **A filesystem path into an execution resource.** ADR 0029 §8. A resource
  reference plus a generation is what replaces `RunOptions.WorkDir`.
- **A database connection, DSN, or credential for the data plane.** ADR 0022.
- **A credential that would let the resource perform a mediated action
  directly.** ADR 0030 §7 makes this the test of whether an action is mediated at
  all: a resource holding a forge credential can push without asking, and the
  boundary becomes irrelevant with no code change anywhere.
- **Unresolved selection of any kind** — a pack name to look up, a model name to
  infer a provider from, a capability to be decided later.

**Restart reuses the configuration and re-issues the bindings.** ADR 0029 §2
scopes the Incubator to the Story execution rather than the agent principal, so a
replacement agent resumes the same execution; ADR 0031 §4 draws the consequence
for the pack, and the same reasoning covers every configured field.

### 3. Model identity: the route, the served offering, and the underlying model

The blocker plan's scope decision 3 requires this ADR to settle whether a
provider's **served** model identity and the **underlying** model identity are one
key. **They are not**, and the invocation carries a third thing that is neither.

| Concept | Is | Keyed on | Carries |
| --- | --- | --- | --- |
| **Route** | Where the request goes | provider + endpoint + the provider's model name | No comparison identity — two endpoints serving one offering are not two models. It is still **persisted with the configuration** (§2), because a restart must reissue the same route |
| **Served model identity** | A provider's offering | provider + the provider's model name | Deprecation and retirement dates, price, sampling-parameter support, context limits |
| **Underlying model identity** | The model itself | its own key | **Lineage** — the set of originating labs ([ADR 0020](0020-review-invariant-reviewer-vs-partner.md)) |

**The route is explicit and never inferred from the model name.** This is
[#272](https://github.com/SnapdragonPartners/maestro/issues/272)'s contract
portion. v1 infers the provider from a name prefix
(`pkg/config/config.go:290`, `ProviderPatterns`), which required a hand-placed
`gpt-oss` → Ollama rule ahead of the `gpt` → OpenAI rule to stop an open-weight
model being billed to a hosted API. A name is not a routing key. The
implementation that removes `ProviderPatterns` stays a Phase 3 item.

**Served and underlying are separated because their facts have different owners,
and the separation is not an edge case.** One open-weight model served by three
providers has one lineage and up to three retirement calendars. A closed model is
in the same position: `claude-opus-4-1` offered through the Anthropic API,
Bedrock, and Vertex is one model with three offerings, whose retirement schedules
are set independently by three vendors. The retirement that cost the Phase 2 exit
run was a fact about an *offering*.

- **The invocation carries the route and the served identity.** The runtime needs
  the first to make the call and the plane needs the second to record it.
- **The underlying reference is not on the wire.** It is a fact about a model, not
  about this invocation, and the plane resolves it from model metadata. Carrying
  it per invocation would let two configurations disagree about one model — the
  hazard [backlog slot 16](../v2/notes_adr-backlog.md) names explicitly.
- **The reference is nullable, and null means `unclassified`.** ADR 0020 already
  holds unknown lineage outside its ladder rather than guessing, so this
  introduces no new state.

**Requested is not effective, and provenance records the effective identity.** A
requested route may name an alias — `claude-sonnet-4-5` rather than
`claude-sonnet-4-5-20250929` — and an alias resolves at call time to something the
requester did not choose. Recording the alias would make two runs months apart
compare equal when they ran different models, which is the failure
[#319](https://github.com/SnapdragonPartners/maestro/issues/319) exists to
prevent, arriving from the other direction. So:

- Provenance records the **effective** served identity where the provider reports
  one, and records that it was requested by alias.
- A disagreement between requested and effective is **recorded**, not silently
  accepted.
- Where the provider reports nothing more specific, the requested identity is what
  is recorded, marked as unresolved rather than as confirmed.

**The honest limit, stated rather than discovered.** A served identity is the
provider's claim about what it serves. Maestro does not verify weights, so a
self-hosted deployment can serve materially different content under one tag —
different quantizations of `qwen3-coder:30b` are the obvious case — and nothing
here detects it. Two deployments of one offering therefore compare equal in the
MPH signature, which is right for hosted providers and is an assumption for
self-hosted ones. It is recorded as a limit because the alternative is a content
attestation nobody can supply today.

**What this gives [#319](https://github.com/SnapdragonPartners/maestro/issues/319)**
is the shape of its metadata home and nothing more: two levels, lifecycle facts
keyed on the served identity, lineage keyed on the underlying model, and a
nullable reference between them. Whether that is two tables or one with a scope
column is #319's decision.

### 4. Events report; records decide

**An event is a report about an execution. It is not the record of anything.** The
durable facts are the Orchestrator's own: the `tool_call` written at ADR 0030's
boundary, the `llm_call`, the artifact, the principal instance. A runtime that
emits no events at all still produces a complete record of its **mediated**
actions, because those went through the boundary — and no record of its
in-resource or external ones, which never reach it
([ADR 0030](0030-tool-execution-policy-hook.md) §6).

The event kinds:

| Event | Carries | Note |
| --- | --- | --- |
| `started` | The runtime's own identity: adapter, executable version, effective contract version | The first message after the handshake |
| `heartbeat` | Liveness, and the runtime's current phase | Separates a working execution from a hung one without inferring either from output volume |
| `activity` | Human-facing progress | Never load-bearing. v1's `INACTIVITY` signal is an observation of this channel, and §6 keeps liveness off it |
| `action_request` / `action_result` | A mediated action and its outcome, correlated (§6) | The request is a request. What happens is ADR 0030's, and the result the runtime receives is what that boundary returns |
| `usage` | **A call reference**, token axes, cost where known, and the **effective served identity** with its confirmation state | §3 and §9. Without the call reference it joins to nothing |
| `provenance` | The same call reference, its source bindings, and the closure status drawn from them | [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §3, obligations 2 and 3 |
| `attach` / `attach_ack` | A restarted runtime asks what is outstanding; the Orchestrator answers (§6) | |
| `warning` | A condition the runtime wants recorded and did not treat as fatal | |
| `terminal` | The four-axis result (§5) | At most one per execution, and it is a **claim** (§5) |

**Asking another principal a question is an action, not an event.** A first draft
made it an event, which put a side door around ADR 0030's boundary: routing a
question changes execution state and invokes Orchestrator message routing, and
ADR 0022 requires anything that creates decisions or state transitions to pass
through an action record. As a raw event it would carry no intent record, no
policy evaluation, and no outcome — the exact hole ADR 0030 exists to close.

#### `message.ask` routes; it does not return the answer

Making it mediated fixes the audit hole and leaves the handoff question open, so
this ADR settles it rather than letting Phase 3 discover it.

**The action's result is a routing acknowledgement** — delivered, or not
deliverable — and it settles promptly. It is emphatically *not* the eventual
answer. Two reasons, and the second is binding:

- An action whose result is an answer would sit unsettled for as long as another
  principal takes, holding a slot in every drain (§6) and making a fence wait on
  a conversation.
- **[ADR 0021](0021-artifacts-and-principal-instances.md) makes artifacts the
  sole agent handoff.** An answer returned inline as an action result would be a
  direct principal-to-principal message payload, which is precisely what that
  rule forbids. The question is recorded as an artifact and routed; the answer
  arrives as an **artifact reference**, on the same path as any other input.

**Waiting for an answer is an execution state, not an action state.** The
execution enters a declared wait whose responder is another principal — a third
alongside §6's operator and resource waits, with its own responder, its own
release rule, and the same obligations: it must name what it waits for, it is
never settled `unknown` by reconciliation, and it must be restorable from its
record.

**The question itself is an artifact.** Carrying its text as an action argument
would be the direct principal-to-principal payload ADR 0021 forbids, merely
routed through a mediated action — the audit hole closed and the handoff rule
still broken. The agent publishes the question, then routes its **reference**.

**The answer arrives on the bindings, not the configuration.** The configuration
is immutable (§2), so an execution cannot acquire a new seeding artifact by
restarting; the bindings are per-incarnation and are where inbound artifact
references belong. That is the wire path, and it is the reason the two-record
split earns its keep beyond resource generations.

Whether an answer can be delivered into a *live* execution or only into a
restarted one is a Phase 3 decision; the contract requires only that the wait be
durable and named, and that the reference arrive on the mutable half.

**`usage` and `provenance` are two reports about one model call**, joined by a
call reference both carry. A first draft gave `usage` no reference at all, which
left token accounting unattributable to a model and unjoinable to the provenance
for the same call — two records about a thing neither could name.

**A reported fact and an observed fact are different, and the record says which.**
The tempting rule — *events are never authoritative* — is false for the case that
matters most. When an adapted runtime makes its own model calls, its `usage` event
is the **only** source; the Orchestrator did not make the call and cannot observe
the tokens. So:

- Where the Orchestrator performs the act, its own observation is authoritative
  and a contradicting event is recorded as a discrepancy.
- Where only the runtime can see the fact, the runtime's report is what is
  recorded, **marked as reported**.
- The distinction is a field, not a convention. It is the same discipline
  ADR 0031 §3 applies to closure and ADR 0025 applies to unavailable metrics: a
  number whose provenance is unknown reads as a measurement.

**The terminal event is a claim, and the Orchestrator's own observations win.** A
runtime that reports `completed` after cancellation was requested and fencing
confirmed is recorded as `cancelled`; its claim is retained rather than discarded,
because a runtime that believed it finished is a fact worth having. This is the
inverse of v1's arrangement, where a side-channel slot corrected the parsed signal
after the fact (`runner.go:199-206`) with no record that the two had disagreed.

#### Event identity, without which at-least-once means nothing

**Delivery is at-least-once for the events that carry a retention obligation,
and best-effort for the rest** — the narrowing below is part of the guarantee,
not a qualification bolted onto it. A first draft opened by promising
at-least-once universally and narrowed it several paragraphs later, which is the
order in which a reader acquires the wrong belief and then has to be talked out
of it.

For the events that do carry the obligation, a redelivery must be harmless — and
that requires an identity the receiver can check. A first draft asserted the property
and defined no identity, which is a guarantee with no mechanism: the conformance
slice's sequence counter restarted at 1 with every process, so two different
messages from two incarnations shared one identity and nothing deduplicated
either.

**An event's identity is `(execution, epoch, stream, sequence)`.**

- The **epoch** identifies the incarnation and is **assigned by the
  Orchestrator**, on the bindings (§2). A runtime that minted its own would
  restart the identity space on every process, which is the defect.
- The **stream** is which of the two spaces below the event belongs to. It is
  part of the identity because each space has its own sequence and its own
  watermark — and it is **derived from the message type and validated**, never
  trusted as supplied: a `usage` claiming `best_effort` would opt itself out of
  the obligations its own type carries.
- The **sequence** is monotonic within an (epoch, stream).
- **The receiver checks what was COMMITTED, not merely the watermark.** An event
  committed beyond a gap sits above the watermark, so a watermark-only test
  would accept its replay and apply it twice.
- Ordering holds within an (epoch, stream); epochs order incarnations. Ordering
  across executions, or across streams, is not promised and nothing may depend
  on it.

This is what makes the `usage` and `provenance` records safe to persist: a
redelivered usage event that was counted twice is a corrupted cost figure, not a
harmless duplicate.

#### Identity makes a duplicate safe; it does not make delivery at-least-once

A first draft declared at-least-once delivery and supplied only the identity,
which is half a mechanism: nothing told the sender what to retain, and a crash
between emission and commit lost the event with nobody the wiser. Three parts
are required, and all three are the contract's:

- **Acknowledgement.** The Orchestrator returns a **watermark** per (epoch, stream):
  everything at or below it is durably committed, and the acknowledgement carries
  **the watermark**, never the sequence just received — declaring 2 committed
  while 1 is missing would tell a sender to discard what never landed. It is sent **after** the
  event's effect is recorded, so an acknowledgement never promises more than was
  committed, and a duplicate is acknowledged too — re-acknowledging a committed
  event is what makes a *lost acknowledgement* recoverable.
- **The watermark is contiguous.** It advances only through an unbroken run of
  committed sequences. Storing the highest received would acknowledge a gap:
  sequence 5 arriving before 4 would tell the sender it may discard a 4 that was
  never committed. An epoch is its own sequence space, so the handshake's
  sequences are not part of it.
- **A sender retention obligation.** A runtime retains anything above the
  watermark and replays it **under its original identity** — re-sending under a
  fresh sequence is a new event the receiver counts again, which is the opposite
  of a replay.
- **Durable receiver state.** The watermark is persisted. In-memory
  deduplication is reset by exactly the restart it exists to survive, so a
  replayed usage event from the previous incarnation would be counted a second
  time.

#### The guarantee is narrowed by event type, not promised universally

A durable universal outbox is real machinery, and MVP does not need one for
`activity`. So the obligation is **scoped to the events whose loss corrupts
something**:

| Events | Guarantee |
| --- | --- |
| `usage`, `provenance` | **At-least-once, with a retention and replay obligation.** These carry accounting and attribution; a lost usage event is a wrong total, not a missing log line |
| `heartbeat`, `activity`, `warning` | **Best-effort.** Diagnostic. Loss is tolerable, and saying so is better than implying a durability nothing implements |
| `action_request` | Neither — it is a request, not a report. The **correlation** is its delivery guarantee: a re-request of the same logical action reuses the attempt and commits at most once (§6) |
| `terminal` | Neither. The Orchestrator observes the stream ending regardless, and composes a result either way (§5) |

**An event is acknowledged only after its effect is durable.** For an
`action_request` that means the intent is *registered* before the acknowledgement
goes out — a crash between the two would otherwise lose an action the sender had
already been told it could release. Registration is therefore synchronous even
though the gates that follow are not.

**A replay legitimately carries the epoch and stream it was first emitted
under**, so an older epoch is accepted and deduplicated against *that* space's
record. Two restrictions:

- An epoch **ahead** of the active binding is always a violation: it names an
  incarnation the Orchestrator never issued.
- An older epoch is admissible **only for the replayable report types**. An
  `action_request` or a `terminal` from a superseded incarnation is not a
  replay, it is an **act** — a fenced generation reaching through the boundary,
  which ADR 0029 §7 requirement 5 exists to prevent.

### 5. The terminal result is four axes

One list cannot carry these facts, and v1 demonstrates the failure concretely:
`claude.Signal` puts an error, a timeout, an already-satisfied completion, a
pending question, and a container-switch request in one enum, so every consumer
switches over a set whose members are not alternatives to one another.

| Axis | Values | Present when |
| --- | --- | --- |
| **Execution status** | `completed`, `blocked`, `cancelled`, `timed_out`, `failed` | Always, exactly one |
| **Completion disposition** | `changed`, `already_satisfied` | Required iff status is `completed` |
| **Cancellation reason** | `superseded`, `operator_requested`, `shutdown` | Required iff status is `cancelled` |
| **Failure class** | `retryable_infrastructure`, `non_retryable_agent` | Required iff status is `failed`, and carried by nothing else |

**It is a schema with an applicability rule, not a cross product.** An axis that
does not apply is absent, not defaulted. A `completed` result with a
`failure_class` is invalid, and so is a `cancelled` result without a reason.

Notes on the axes that are easy to get wrong:

- **`blocked` carries no reason enum of its own.** It references the pending
  action and the structured requirement set ADR 0030 §3 already defines. Inventing
  a parallel vocabulary here would duplicate candidate 12's rules in a second
  place and let the two drift.
- **`timed_out` is a status, and it carries no failure class.** It is a status
  because a deadline is an Orchestrator-observed fact while an error is a
  runtime-reported one, and collapsing them would lose which party ended the
  execution. It carries no class because **ordinary wall-clock exhaustion is
  neither** — a slow provider and a looping agent both produce it, and a first
  draft required a classification while saying in the next breath that a timeout
  may be retried with a larger budget, which is not what either class means. An
  axis that must be filled with the least-wrong value records a guess as a fact.
  If timeouts later need a cause, that is its own axis with its own vocabulary,
  not a borrowed one.
- **`already_satisfied` is a completion.** The execution did its job; the work was
  already done. Recording it as a distinct *status* would make a successful
  execution look unsuccessful, which is the reading
  [#280](https://github.com/SnapdragonPartners/maestro/issues/280) reports today
  from the opposite error.
- **`cancellation_reason` is extensible; `superseded` is A5's.** This ADR fixes
  that the axis exists and that amendment terminates work as `cancelled` rather
  than `failed`. When a cancellation is legitimate and what it does to pending
  actions is [A5](0019-orchestrator-boundary.md)'s.

#### A result violating the applicability rule is a protocol violation

A first draft of this section defined the rule and did not say what happens when
a runtime sends a result that breaks it. **It is refused and the execution fails
`non_retryable_agent`**; it is not recorded and then reasoned about downstream.
Accepting it would put the exact axis collision this schema exists to prevent
into the plane, with the validator having seen it and shrugged. The conformance
slice exercises this directly.

#### `blocked` is recorded by the Orchestrator, not claimed by the agent

The other four statuses may be claimed by the runtime and are then subject to
§4's override. `blocked` is different in kind: it is a fact about a **gate the
agent cannot see**, established by ADR 0030's boundary and the configuration's
declared responder availability. The agent stops; the Orchestrator records.

This fell out of the conformance slice rather than being designed: the headless
agent reaches a decision it cannot get answered, stops issuing turns, and exits
**without a terminal event**, and the host composes the result from the
boundary's own state. An agent that named itself blocked would be asserting
something it has no way to know.

**So `terminal` is *at most* one event per execution, not exactly one**, and a
first draft said "exactly". The blocked execution is the standing exception:
there is no terminal event at all, and the Orchestrator composes the result.
Stating it as exactly-one would have made the one case this ADR designs for read
as a protocol violation.

#### A headless block is terminal for the action, not a wait

The action does not sit in `operator_waiting` (§6) — **nothing will ever answer
it**, and a state named for a responder that does not exist is a false record.
It settles terminally as `blocked`, **preserving the requirement** so the
execution's result can reference it.

A first draft got this wrong in a way worth keeping: the boundary returned
`denied` on the wire, left the action in `operator_waiting`, and recorded the
execution terminally `blocked` — three descriptions of one event, no two of them
agreeing. One event, one durable action outcome, one wire result, and the
requirement carried by all of them.

**And the Orchestrator stops the execution; it does not wait to be left.** A
second draft composed the `blocked` result only when the adapter closed its own
stream, which makes a terminal Story contingent on the runtime's courtesy: a
non-cooperative one keeps making model calls and doing unmediated in-resource
work under a Story that is already blocked, and nothing bounds it.

A headless block is a **forced stop** and takes the same path as any other
(§6): admission closes, cancellation is requested with a grace period, admitted
actions drain, the domain is fenced, and only then is `blocked` recorded. The
agent exiting promptly is the fast path, not the mechanism.

#### Two things that are not execution statuses

**`rejected` is not one, and #282 as filed lists it.** An agent asked to review
something and rejecting it has **completed** — its judgment is the work product,
carried in the artifact and its review linkage
([ADR 0028](0028-artifact-envelopes-and-payload-schemas.md)). Making rejection an
execution status would make an execution's success depend on the content of its
judgment, so a reviewer that found a real defect and a reviewer that crashed would
report the same axis. This is the same conflation §5 exists to undo, one level up.

**A refused invocation is not one either.** An invocation that fails the handshake
— incompatible contract version, unresolvable pack, a capability set the runtime
cannot honour — produces **no execution and no terminal result**. It is a dispatch
failure, recorded as one on the Orchestrator's side. The line is exact and §6
places it: an execution begins when the handshake completes and the invocation is
accepted. Phase 3 must record dispatch failures durably; what it must not do is
mint a sixth status for an execution that never started.

### 6. Lifecycle

> **Read this section against Status Of Decisions above.** Most of it is
> **provisional**: the execution FSM, restart, resume, re-attach, and
> outstanding-action enumeration are Phase 3 design inputs. Three things here
> **bind** — the drain-and-fence precondition on a positive terminal result, the
> rejection of superseded or fenced authority at every mediated boundary, and
> artifact-level recovery as the rule. Where the text below states a provisional
> item in binding terms, the Status section wins.

#### The execution's states

`dispatched` → `starting` → `running` → (`waiting`) → `stopping` → terminal.

An execution begins at the completion of the handshake. `waiting` is not one
state: it is the two waits below plus `question`, and they are distinguished
because their responders differ.

#### The action's states, and the two waits ADR 0030 assigns here

ADR 0030 §8 requires the tool call record to distinguish healthy waiting from an
interrupted call, and assigns the vocabulary to this ADR. It is:

| State | Meaning | Terminal |
| --- | --- | --- |
| `open` | Admitted; the effect is being attempted | No |
| `operator_waiting` | ADR 0030 gate 2 — a human decision is required | No |
| `resource_waiting` | ADR 0030 gate 3 — provisioning, or queued for capacity ([ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §2) | No |
| `settled` | An outcome was determined: succeeded, failed, denied, **blocked**, or **unknown** | Yes |

`blocked` is an action outcome as well as an execution status, and it is the
headless case of §5: the gate required an operator, the configuration declares
none, so nothing can answer and the attempt settles terminally with its
requirement preserved.

**The two waits stay distinct** — ADR 0030 §8's reason, restated because it is the
whole point of the vocabulary: different responders, different release rules,
different costs. A watchdog that cannot tell them apart cannot have a policy for
either.

**`unknown` is an outcome, not a state.** Reconciliation settles an attempt that
holds an intent and no outcome by completing it as `unknown`. Keeping it an
outcome rather than a state is what stops the confusion this section exists to
remove from reappearing one level down — and it is the value the existing
`tool_calls_finished_check` constraint cannot express, which is why the Phase 3
migration must replace that constraint rather than only add to the table.

#### Reconciliation treats all three states, and settles only one

ADR 0030 §8 states that the *watchdog* leaves both waits alone. The
**reconciler is a different actor** and no rule was written for it, so this ADR
writes one — and got it wrong twice in opposite directions before arriving here.

The first implementation settled **every** unsettled attempt as `unknown`, which
swept up an attempt sitting healthily in `operator_waiting` and destroyed the
requirement a `blocked` result must reference (§5); the execution then reported
itself blocked on nothing. The correction — *reconciliation acts on `open` and
nothing else* — over-shot: it left a `resource_waiting` attempt whose
provisioning operation had died stuck forever, because that operation does not
survive a restart and nobody was going to restore it.

**The rule is that a declared wait may never be settled `unknown` merely for
being nonterminal — not that reconciliation ignores it.** Each state has its own
treatment:

| State | Reconciliation does | Why |
| --- | --- | --- |
| `open` | Settles it `unknown` | An intent was recorded and no outcome ever was |
| `operator_waiting` | **Settles it `stale`**, preserving the requirement and the operator decision | Only an operator can answer it, and the continuation that would have run gate 3 died with the process. It is not `unknown`: an attempt awaiting an operator is not one whose outcome nobody knows. A wait carrying no requirement is a **defect**, surfaced as one |
| `resource_waiting` | **Settles it `stale`**, preserving the operation it named | The provisioning or queueing operation does not survive a restart either. A wait naming no operation is likewise a defect |

#### A wait interrupted by a restart goes stale; it does not resume

The row surviving is necessary and not sufficient — but the fix is *not* to make
the wait resumable, and a draft that tried collided with two accepted rules at
once.

Resuming gate 3 from the record requires the **complete substituted request**.
ADR 0030 §3 permits the Audit family to hold only the declared safe projection
and a digest, precisely because the substituted form may contain what the schema
excluded; persisting it to enable resumption would put that data back. And §6
already promises **restart from the last durable workflow artifact** — nothing
more. Resumption would have been a second, larger guarantee, quietly added.

So the rule is artifact-level:

- A declared wait interrupted by an Orchestrator restart is settled **`stale`** —
  ADR 0030 §5's own word for an action that must be **re-requested** rather than
  continued — with its requirement preserved and its disposition recorded as
  stopped before the commit point.
- It is never settled `unknown`: an attempt awaiting an operator is not an
  attempt whose outcome nobody knows.
- The execution restarts from the last durable workflow artifact and re-requests
  what it still needs.

**The operator decision is persisted, and it is what makes this acceptable.**
Re-requesting an action must not re-ask a human who already answered, so the
*decision* — small, declarable, and nothing like the request — is recorded
against the logical action (its identity and substituted-input digest) and
consumed on the next request. Asked once per logical action, not once per
attempt.

A wait must still **name what it waits for** — a requirement, or a provisioning
operation — because nothing else can tell a stuck wait from a healthy one.

**Entering and leaving a wait is a durable transition**, per ADR 0030 §8, not the
absence of a completion.

#### Cancellation

Cancellation is cooperative first and fenced second, and the ordering is
load-bearing because A5 rests on it.

1. **Admission closes first, and it linearizes with registration.** The boundary
   stops admitting *new* agent-initiated actions for that execution before
   cancellation is even asked for. This is
   [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §7 step 2's own
   ordering — *revoke the ability to create, then enumerate, then confirm* —
   applied to attempts rather than containers.

   **Closure and registration must be atomic with respect to one another.**
   Reading a closed flag and registering the attempt under separate locks leaves
   a window in which an attempt joins the set *after* the set was closed, so the
   drain in step 5 is reasoning about a population that is still growing. A
   closure that has returned must mean no registration is in flight.
2. **The Orchestrator requests cancellation** over the contract, with a stated
   deadline.
3. **The runtime is permitted to reach a safe boundary** — completing an atomic
   action already **admitted** — and issues no further ones. Closing admission
   does *not* abort work already admitted: that is what draining means, and a
   first implementation re-checked closure at gate 3 and thereby killed an
   in-flight action mid-drain. What bounds an attempt that will not settle is the
   grace period, not a second refusal.
4. **Admitted actions are drained, and only then is the domain fenced.**
   ADR 0029 §7 is what stops a process; ADR 0030 §5 is what settles the
   *actions*, and the two are not the same guarantee. Fencing an Incubator or a
   Habitat says nothing about an in-flight **Orchestrator-side** mutation — a
   data-plane write, an artifact publication, a forge push — because those land
   outside every resource domain. A domain receipt alone would report
   `terminated` while such a mutation was still committing.

   Each admitted attempt must therefore be drained short of its commit point,
   conditionally committed, or confirmed inside the fenced domain, exactly as
   ADR 0030 §5 requires — and **"settled" alone does not establish which**. The
   attempt records a **disposition**: stopped before its commit point, committed,
   or run inside the fenced domain. Without it a drain cannot tell an action it
   prevented from one that went ahead.

   **A wait is stopped, not waited on.** An attempt parked in a declared wait is
   demonstrably before the effect, so the drain settles it `stale` rather than
   waiting; waiting for a human inside a fencing grace period would guarantee an
   unconfirmed receipt every time. Only an attempt that is *executing* can commit
   at any moment, and that is what the drain actually waits for.

   **And stopping must be real.** Marking the record while the continuation runs
   on is "invalidate the attempt" under another name — the option ADR 0030 §5
   already rejects, because a mark nothing checks does not prevent a commit. The
   continuation re-reads its own attempt before the effect and abandons it if the
   drain settled it.

   **An attempt that does not settle within the grace period yields no positive
   receipt**, whatever the domain reported. An effect that commits *during* the
   drain is not a defect — that is what draining is for — but it is recorded, so
   a cancellation can say what went out with it.
5. **The terminal result is recorded only after a positive receipt** covering
   *both*: the domain, and the registered actions. `terminated` or `isolated`
   satisfy the domain half; `unconfirmed` does not, and neither does an
   incomplete drain. The execution stays non-terminal with the resource
   quarantined.

Step 5 is A5's stated rule and this ADR is where the lifecycle carries it. A
terminal result written while an unfenced process may still be writing — or
while an admitted forge push may still land — is a false record, and downstream
work would be dispatched against a resource that is not free.

#### The receipt discipline belongs to the category, not to `cancelled`

**Every path on which the Orchestrator forces a stop owes the same positive
receipt** — cancellation, deadline expiry, and any future forced termination.

A first version attached the rule to the `cancelled` status alone, so a timed-out
execution recorded `timed_out` even when fencing came back `unconfirmed`: the
identical false record, reached by a different route. What makes the rule
necessary is not which status is being written but that **the Orchestrator ended
an execution whose process it has not confirmed stopped**.

**Forced means forced, whatever the cause.** Cancellation, deadline expiry, a
**protocol violation**, a broken transport, and a gate no operator can answer are
all the Orchestrator ending the execution, and all take the same path. A second
version routed the protocol and transport cases straight to recording a failure,
so a runtime killed for speaking nonsense left its admitted actions unsettled and
its resource unfenced — the very state the discipline exists to prevent, reached
by the path nobody thought of as a stop.

**Even an ordinary completion drains.** A runtime can report `completed` while an
action it issued is still committing, and recording that would be the same false
record. The claim is held until the drain confirms nothing is outstanding.

**A cancelled execution's output is retained as attributable draft and Audit
history.** It is not accepted against the superseded version; that is A5's.

#### Timeout, liveness, and inactivity

- **The deadline is the Orchestrator's**, declared in the invocation. A runtime
  may impose its own, and a runtime-reported timeout is a report like any other.
- **Liveness is the `heartbeat` channel, not the `activity` channel.** v1 infers
  inactivity from output silence (`claude.SignalInactivity`), which conflates *not
  talking* with *not working* — a long compile is silent and healthy. A missed
  heartbeat is an observation about the runtime; a silent `activity` channel is
  not.
- **A missed heartbeat is not by itself a terminal condition.** It opens the
  reconciliation path, and the resource's state is what settles it.

#### Restart, resume, and re-attach

Three different things, and v1 has one mechanism for all of them.

- **Restart** replaces the agent process for the same execution. The **execution
  configuration** is reused verbatim and the **bindings** are reissued (§2).
  ADR 0029 §2 makes this possible by scoping the resource to the Story rather
  than the principal.
- **Resume** is a runtime capability, declared at the handshake. A runtime that
  can resume its own session (Claude Code's `--resume`, v1's `SessionID`) may be
  offered a resume token that is **opaque to the Orchestrator**; a runtime that
  cannot is restarted from the last durable workflow artifact, which ADR 0030 §4
  already names as the recovery state actually promised.
- **Re-attach** is transport plumbing. Each action the runtime issues carries a
  **correlation identity**; the Orchestrator's response carries the record's own
  identifier.

#### The correlation is ADR 0030's attempt identity, and it binds to the action

Two names for one thing, reconciled here because the two ADRs would otherwise
appear to disagree. ADR 0030 §3 puts **attempt identity** on the *request* and
gives it the at-most-once semantics; this contract calls that field the
**correlation**, and reserves "attempt identity" for the boundary's own record
key. The semantics ADR 0030 attaches are unchanged — a retry of the same logical
action reuses the same correlation, and one correlation commits at most once.

**A key alone does not identify a logical action.** The boundary binds the
correlation to the **action identity** and to a **digest of the substituted
input** (ADR 0030 §3's digest, over the form with secrets already substituted),
and refuses a reuse matching neither. Without that binding one key can replay
the result of a different action, or of the same action with different
arguments, and the boundary reports success for work it never performed — a
false record produced by the very mechanism meant to prevent duplication.

Matching both is a retry. Matching neither is a caller defect and is refused,
not silently reinterpreted.

**Re-attach and restart coincide on the local transport**, and a first draft of
this section said "after a transport reconnection" as though they were separate.
Over stdio they are not: **a broken transport is a dead process**, so there is no
live runtime to reconnect to and the only case the local transport presents is a
*restarted* runtime rejoining an existing execution. The reconnection wording is
retained because a socket or remote transport will need it, and it is now stated
as what it is — a case Phase 3's transport does not produce.

**The Orchestrator enumerates what is outstanding; the runtime asks.** A first
draft had the runtime re-announce its outstanding correlations and required them
to be *derivable* so it could reproduce them after a restart. That is unsound for
a **nondeterministic** runtime: step 3 of the second incarnation need not be the
same logical action as step 3 of the first, so a derived correlation can collide
with an unrelated attempt — turning at-most-once into at-most-once-for-the-wrong-
thing.

The durable attempt records are the Orchestrator's, so it is the authority.
Derivation remains a legitimate strategy for a deterministic runtime; it is not a
requirement of the contract, and nothing depends on the runtime being able to
reconstruct anything.

**Re-attach never surfaces approval semantics to the agent.** The runtime learns
that a call is still outstanding; it does not learn that a human is being asked.
ADR 0030 §4 blocks the caller precisely so that no LLM turn happens while a gate
is open, and telling the model it is waiting on a person would give it something
to reason about — which is the deny-and-retry shape ADR 0030's redraft deleted,
re-entering through the transport.

**The correlation-to-attempt mapping is durable and unique per invocation.**
Otherwise a reconnection cannot honour ADR 0030 §3's rule that a transport retry
of the same logical action reuses the same attempt identity.

### 7. What a blocked execution costs, and what it does not

ADR 0030 assigns this ADR the question, and calls it out as load-bearing: its
premise that blocking is affordable holds only if blocked work does not consume
scheduling capacity.

**Two limits, two units, and they are not one number.**

- **Runnable concurrency** bounds executions eligible to consume LLM turns and
  issue actions. It exists to bound model spend and provider rate limits.
- **Resource capacity** is ADR 0029 §2's per-type limit on Incubators and
  Habitats. It exists to bound machines.

**A blocked execution consumes no runnable concurrency.** It performs no LLM turns
and its further actions are rejected (ADR 0030 §4), so it is not eligible, so it
is not counted. This is the accounting that makes ADR 0030's blocking affordable;
the opposite choice would let blocked work starve independent runnable work and
the premise would fail.

**A blocked execution does consume resource capacity**, for exactly as long as it
retains a resource. That is the real cost, ADR 0030 §4 says so, and the release
rule belongs to the Phase 3 plan.

**The visible consequence, stated so it is designed for:** a run can exhaust
resource capacity while runnable concurrency sits idle. That is the correct
behaviour and it is legible — a queue on a named resource type — rather than the
alternative, where a blocked Story silently occupies a scheduling slot and the
symptom is a run that mysteriously stops making progress.

**ADR 0030's cost claim is exact for a native agent and this ADR owns the other
case.** That ADR describes a blocked Story as costing "a suspended goroutine and
its in-memory state, nothing more," which is true when the agent is in-process.
An external-process runtime is not a goroutine: it is a process, and possibly a
container. So the contract makes the choice explicit rather than assuming either:

- A runtime that **declares itself resumable** may be released as soon as it
  blocks and re-invoked with its resume token on approval.
- A runtime that does not is **held for a bounded retention window**, then
  released and restarted from the last durable workflow artifact when the wait
  outlasts it. While resident it counts against a process budget that is neither
  of the two limits above.

**Non-resumable does not mean permanently resident**, and a first draft implied
it did. That would have contradicted §6's own recovery rule — a runtime that
cannot resume is restarted from the last durable artifact — and created a
process-capacity deadlock with no stated way out: enough blocked non-resumable
executions and nothing new can start, indefinitely, waiting on humans. The
retention window is what bounds it, and the recovery path already exists.

Phase 3 owns the window's length and the budget's value. What this ADR fixes is
that holding an external process across a human's attention span is a cost
someone decided to pay for a bounded time, not one that arrives by default and
never ends.

### 8. Capabilities, tools, and knowledge

**The capability set is resolved at dispatch and carried in the invocation**, and
it names **Orchestrator-owned action identities** — ADR 0030 §3's stable kind and
verb — rather than the runtime's tool names. An adapted runtime brings its own
vocabulary for the same action, so a capability set keyed on the caller's names
would be runtime-specific by construction.

**The invocation's set is not the authority; the boundary is.** ADR 0030's
admission gate re-checks the action against the resolved set immediately before
the effect. The invocation's copy exists so a runtime can present a coherent tool
surface to its model and refuse locally rather than discovering every denial
through a round trip.

**A policy denial is data; a protocol violation is fatal.** The line matters
because it decides whether the agent gets to respond:

| Condition | Result |
| --- | --- |
| Action outside the capability set, or denied by ADR 0030's gates | An `action_result` the agent can read and act on. The execution continues |
| Any framing violation below | The execution fails. `failed` / `non_retryable_agent` |

Collapsing these would either kill an execution for an ordinary refusal, or let a
runtime keep going after it stopped speaking the protocol.

**The fatal list is enumerated, because a first draft called violations fatal and
then silently ignored most of them** — decoding a body, shrugging on error, and
carrying on. A specification whose "fatal" cases are unenforced is decorative,
and the failure it hides is the worst kind: a runtime speaking a contract this
build does not implement looks healthy right up to the point its work is lost.

Every inbound message is validated **before anything acts on it**:

- **Version.** Not the negotiated one — fatal.
- **Execution identity.** Names an execution other than the active one — fatal.
- **Epoch.** *Ahead* of the active binding — fatal. Behind it is a replay (§4).
- **Message type.** Not in this build's vocabulary — fatal. Ignoring an unknown
  type is how a version skew becomes silent data loss.
- **Body of a known type.** Unparseable, or violating that type's own contract —
  fatal. `usage` without a call reference joins to nothing; `provenance`
  claiming `closed` with no bindings is not provenance (§9).

A terminal event is the one exception in kind: an unparseable *body* is a
framing violation, while a parseable body whose axes are invalid (§5) becomes
the execution's failure rather than a framing error. Both fail the execution;
they differ in what is recorded as the cause.

**Knowledge and retrieval are mediated actions, not a second channel.** There is
no retrieval side door and no direct data-plane access (ADR 0022). A retrieval is
recorded like any other action — which matters more than it looks, because
ADR 0030 §8 requires reads to be recorded on the grounds that *releasing data is
the security-relevant effect of a retrieval*. The knowledge base itself is
[candidate 10](../v2/notes_adr-backlog.md) and is not built here.

**Execution contracts route by requirement.** A build, test, lint, or deploy
contract declares the resource it runs in
([ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §8), and the agent
requests it as a mediated action rather than reaching a resource itself. The
contract set, its verbs, and its result shape are Phase 3 plan material
([notes_execution-contracts.md](../v2/phase_3/notes_execution-contracts.md)), not
this ADR's.

### 9. Provenance

Three groups. The first two are what #282 asked for; the third is what
ADR 0031 §3 handed over.

**Identity of the execution** — recorded on the principal instance and the
invocation: adapter and executable identity and version, contract version, the
model identity of §3, the harness pair (Maestro version and harness config hash,
which ADR 0031 §3 fixes as **both** being H), the prompt pack of ADR 0031 §2, and
the resource references with their instance generations.

**`started` must be first, unique, and consistent with the handshake**, and the
Orchestrator checks all three. An adapter that announces itself twice, or late,
or claiming an adapter and version the handshake did not, has produced identity
nobody compared to anything — and §9's provenance would record it as established.
A mismatch fails the execution.

**Adapter and executable identity are observed where they can be, and marked
reported where they cannot.** A first draft listed them as invocation provenance
while the only source was the runtime's own handshake — a self-report recorded as
though it were established. §4's rule applies here as much as to usage: the
Orchestrator records **what it launched** (the executable it resolved, and its
digest where it has one) as observed, and **what the adapter claimed** as
reported, and a disagreement between them is recorded rather than reconciled
silently. Where Maestro did not launch the process, only the claim exists, and
that is what the record says.

#### What must be durable, and what derives

The configuration being "persisted" (§2) is not a complete statement, because
several things around it need a home too:

| Fact | Durability |
| --- | --- |
| Execution configuration, including the model route | **Persisted**, reissued verbatim on restart |
| Epoch | **Persisted and monotonic per execution.** Allocated by the Orchestrator; a reused epoch would collide two incarnations' event identities |
| Bindings — resource references and generations | **Persisted**, and refreshed whenever gate 3 replaces a resource |
| Resume token | **Persisted opaquely.** Never interpreted, and cleared when the runtime is restarted from an artifact instead |
| Event watermark and committed set per (execution, epoch, stream) | **Persisted** (§4) |
| Attempt, its correlation binding, requirement set, disposition and wait | **Persisted** (§6). **Not** the substituted request: ADR 0030 §3 keeps it out of Audit, and §6 needs only artifact-level recovery |
| Operator decision, against the logical action **and its requirement set** | **Persisted** (§6) only when its own attempt went stale before commit — a grant promoted when it is *given* is never consumed by the action it was given for, and applies twice |

Phase 3 owns which of these share a table. What this ADR fixes is that none of
them may live only in a process, because every one of them is consulted after a
restart by something that must not guess.

**Composite and paired execution keeps its participants distinct.** Where two
models participate — the adversarial pairing ADR 0020 measures — each participant
is its own principal instance with its own model identity, usage, and outcomes.
Collapsing them into one principal would make the heterogeneity rule
unmeasurable, since ADR 0020 classifies the author/reviewer **edge** and an edge
needs two endpoints. Pair selection and contention policy remain harness and
Orchestrator concerns; the contract carries only the identities that make them
measurable.

**The four obligations from
[ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §3**, discharged
here because that ADR deliberately gave them no home:

1. **The expected source contract**, on the execution configuration (§2). Which
   of the four sources this runtime will report **and by what mechanism** — for
   example fixed at dispatch, observable in the Audit record because the actions
   are mediated, or reported by the adapter. A first draft carried source *names*
   alone, which is not a capability claim: "this runtime will report its turn
   material" says nothing anyone can check, and a declaration that cannot be
   falsified is not one. The mechanism is what a reviewer holds the adapter to.
2. **Per-call source bindings**, on the `provenance` event, joined to the `usage`
   event by a shared **call reference** (§4): the exact contributing references
   and digests — the pack identity, the H pair, the seeding-set artifact digests,
   and references to the specific messages, tool results, and retrievals that
   entered the call.
3. **The per-call closure status, drawn from those bindings.** A status without
   bindings is not provenance: `closed` asserts everything in the call was
   attributable without saying to what, so nothing can check it. Bindings first;
   the status is the conclusion.
4. **The retention rule**, below.

**Unclosed is a property of the adapter, not of being external.** ADR 0031 §3
fixes this and it is restated because it is the sentence most likely to be
paraphrased into the wrong claim: what makes a runtime unclosed is that its
adapter cannot supply trustworthy bindings. Today's Claude Code path is unclosed
on that test — it assembles context internally and its in-resource actions are
unmediated — and an adapter that emits the bindings is closed on the same test.
The expected source contract is what makes this checkable at dispatch rather than
discovered at analysis time.

#### The retention rule: a defined traversal, not explicit citation

ADR 0031 §3 requires this ADR to choose between two ways of keeping a cited call's
reconstruction alive, because ADR 0021's pins cover what an evidence package
*directly* references and provenance bindings are the second hop.

**The choice is the defined transitive traversal.** Explicit citation is simpler
and uses the existing pin unchanged, and its failure mode is the disqualifying
one: whoever assembles the package omits a source and nothing objects, so the loss
is silent and arrives long after the omission. A traversal's failure mode is a
missing or wrong declaration, which is a reviewable artifact.

The rule:

- **Each Audit record type declares its pin closure** — what pinning it must also
  pin. Bindings are followed, and so are the object and artifact references a
  pinned record's own projection names (ADR 0030 §3 puts large values behind such
  references, so a traversal that stopped at the record would pin a pointer to
  something already pruned).
- **The declarations must terminate**, and Phase 3 owes that proof rather than an
  assumption. This is a declared, code-resident set on the pattern of ADR 0028's
  payload type registry, so it is checkable rather than emergent.
- **A binding that already fails to resolve when the pin is applied is recorded as
  unavailable**, never silently dropped.

**What this does not do**, because ADR 0031 §3 is explicit that the two-state
split makes loss visible rather than preventing it: until the traversal exists, a
cited call's reconstruction can still rot. **Closure** is fixed when the call is
made and never changes; **availability** is a statement about now and degrades as
retention runs. *Closed and unreconstructible* is a real and reportable condition,
and the evidence-package half of this is Phase 4 work under ADR 0029's deferred
list.

### 10. The local process transport

**One transport in Phase 3, and the semantics do not assume it.**

- The adapter is a child process. The contract is spoken as newline-delimited JSON
  over its stdin and stdout.
- **stderr is diagnostic only** and never carries protocol. A runtime's logs must
  not be able to desynchronize the contract.
- The handshake is the first exchange in both directions (§11).
- **Backpressure is the transport's problem, not the contract's.** An event the
  Orchestrator has not yet read does not change what the event means.

**Why stdio rather than a socket**, recorded because v1 already paid for the
answer and the reasoning would otherwise be repeated: the v1 MCP server listens on
TCP specifically because "Unix sockets don't work through Docker Desktop's file
sharing on macOS" (`pkg/coder/claude/mcpserver/server.go:1-6`), and a TCP listener
then needs a port, an auth token, and a host-reachable address from inside a
container — which v1 also has, along with the `host.docker.internal` assumption
that goes with it. Stdio needs none of those and is available on every platform
ADR 0029 contemplates, including the ones that reject containers.

**A remote transport is not designed here** and the contract must not foreclose
it. The two rules that keep it open: no message may depend on shared memory or a
shared filesystem, and no identity may be a process-local handle. Anything else is
deferred with
[candidate 14](../v2/notes_adr-backlog.md).

**What stdio cannot exercise, and is therefore unproven rather than proven.**
Because a broken stdio transport is a dead process (§6), the local transport
produces no reconnection case at all: it produces restarts. The re-attach
semantics are defined for a transport that can reconnect, and Phase 3 ships none.
That is a deliberate forward provision, and it is the kind of claim this ADR must
not let a later reader mistake for something the conformance slice demonstrated.

### 11. Versioning and compatibility

- **The contract carries a version and it is negotiated at the handshake.** The
  Orchestrator states the versions it supports; the adapter selects one or fails.
- **A version mismatch fails at dispatch**, before any resource is acquired — the
  same placement ADR 0031 §5 gives its compatibility checks, for the same reason:
  a fault found after a lease costs a provisioned resource and tokens to discover.
- **Evolution is additive within a version**, on ADR 0028's discipline. An unknown
  field is ignored by a reader; a field whose *meaning* changes requires a new
  version.
- **The reader is the compatibility layer.** ADR 0028 already fixes this for
  payloads and the reason carries: a writer that must know every reader's version
  cannot ship.
- **Capabilities are negotiated separately from the version.** Resume is the
  worked case (§6): a runtime declares it, and the Orchestrator's behaviour
  differs, without either being a different contract version.

### 12. Not in scope

- **Policy content** — [candidate 12](../v2/notes_adr-backlog.md). This contract
  carries capabilities and meets ADR 0030's boundary; it contains no rules.
- **Amendment versus running work** — A5. This ADR provides the cancellation
  lifecycle and the `superseded` reason; the policy that invokes them is
  ADR 0019's.
- **The knowledge base** — [candidate 10](../v2/notes_adr-backlog.md). Retrieval
  is a mediated action; what is retrievable is not decided here.
- **GitHub Actions presentation.** The blocker plan's scope decision 1 puts it in
  Phase 3 or later, explicitly so it cannot hold phase entry hostage.
- **Extraction of a `maestro-agent` repository** — Phase 8, and #282's own
  non-goal. The contract makes extraction an evidence-based decision later; it
  does not require one now.
- **Provider I/O**, which is `maestro-llms`' and is not re-litigated here.
- **The routing implementation** that removes `ProviderPatterns` — #272, a
  Phase 3 item. Only the contract's identity model is settled here.
- **The #317 and #280 code fixes.** What is pre-entry is that the schema has a
  place for them.
- **Scheduling policy**, including the release rule for a resource held by a
  blocked execution. §7 fixes the accounting; the Phase 3 plan fixes the policy.

## Conformance

**What the slice establishes, and what it does not** (2026-08-15). It exercises an
**isolated contract model**: a host implementation and a stub agent speaking this
wire boundary to each other, in one repository, over one transport. It is **not**
the standalone review agent, **not** a reusable agent module, and **not**
integration with `pkg/agent`, `pkg/coder`, or the data plane. A claim proven here
is evidence that the *model* is internally coherent — not that Maestro implements
it, and not that it survives contact with the framework inventoried in the
Context.

**The code is historical evidence.** It is unmaintained spike code under the rule
CLAUDE.md already applies to `spikes/`, and it is **not a Phase 3 implementation
template**. Its `host/` half in particular is one implementation of machinery the
Status Of Decisions section no longer binds. Development of it stopped at
acceptance.

**The contract cannot be proven by inspection**, which is why the blocker plan
grants it the single bounded exception to *an Accepted ADR and nothing else*. One
conformance executable is required, living outside `pkg/`, `internal/`, and
`cmd/` for the pre-entry period, on the same footing as spike code. Where it lands
permanently is a Phase 3 decision.

It must be a **real external-process executable driven over the local transport**,
and must exercise the actual wire boundary, capability handling, the event stream,
cancellation, and the terminal result. An in-process fake or an echo fixture does
not discharge it.

**Its analysis backend is an explicit stub** (DR, 2026-08-13): a deterministic
stub that minimally satisfies the contract, not a function pretending to do useful
review work. The real build-out is Phase 3's.

**The consequence is a declared coverage gap, not a fabricated one.** A stub that
makes no model calls leaves the model-call-shaped parts of this contract
unexercised — `usage` events, per-call provenance bindings, and token accounting.
The conformance report declares those uncovered rather than emitting invented
numbers that would make the scenarios look green.

The delivery scenarios are the one exception, and it is a narrow one: they emit
a synthetic `usage` envelope as a **transport fixture**, because the retention
and deduplication machinery needs a message that carries a retention obligation
in order to move one. Its numbers are invented and nothing concludes anything
from them — the claims count envelopes, never read them. That is ADR 0025's
`unavailable`-versus-zero discipline applied to test evidence, and the same
posture the [Docker fencing spike](../v2/phase_3/spike_docker-fencing.md) took
when it recorded its own eleven defects rather than quietly repairing them.

### What was run, and what it changed

**Done**, 2026-08-13/14:
[`spikes/phase_3/executioncontract`](../v2/phase_3/spike_execution-contract.md).
**60 claims, all `PROVEN`** under the race detector; thirty-nine spawn a real external process and speak
newline-delimited JSON to it. **45 of 45 mutations killed for their named
reason**, **every run under the race detector, agent subprocess included**, under a harness that requires a
positive control and a clean process exit as well as a green summary, refuses to
start on residue, and verifies restoration by digest.

**Findings from running it, in two rounds.** The first round produced five, of
which the first was a defect in the design rather than in the harness:

1. **Reconciliation was destroying the requirement** a `blocked` result must
   reference (§6). Now a permanent mutation.
2. **Re-attach over stdio is restart, not reconnection** (§6).
3. Correlation identity had to be reconsidered (§6) — see below, where the first
   fix turned out to be wrong for a nondeterministic runtime.
4. **An invalid terminal result is a protocol violation** (§5).
5. **`blocked` is the Orchestrator's to record** (§5), not the agent's to claim.

The second round, from review, found that several of those fixes were incomplete
or wrong at one level down — the shape this repository keeps paying for:

- **Reconciliation scoped to `open` over-corrected** (§6). Ignoring declared
  waits strands a `resource_waiting` attempt whose provisioning operation died.
  The rule is that a declared wait is never settled `unknown`, not that
  reconciliation ignores it.
- **Derivable correlations are unsound for a nondeterministic runtime** (§6).
  Step 3 of a second incarnation need not be the same logical action as step 3
  of the first. The Orchestrator enumerates; the runtime asks.
- **At-most-once covered only settled retries** (§6, ADR 0030 §3). A duplicate
  for an *in-flight* attempt fell straight back through the gates.
- **Closing admission at cancellation was implemented at gate 3**, which aborted
  an action already admitted instead of draining it (§6). The conformance suite
  caught this one as a knock-on failure of two unrelated scenarios.
- **The fence-receipt rule was attached to `cancelled` alone**, so a forced
  timeout wrote a terminal result over an unconfirmed fence (§6).
- **`timed_out` was forced into a failure class it does not have** (§5).
- **Events had no durable identity** (§4), so at-least-once delivery was declared
  idempotent with no mechanism behind it.
- **The stub emitted a `closed` provenance record for an execution with no model
  call** — fabricating precisely the coverage the report declares missing. It now
  emits none, and a claim asserts the absence.

A third round found nine more, of which the two that matter most are about what
the earlier rounds had *asserted without machinery*:

- **Cancellation fenced the resource and never settled the actions** (§6).
  ADR 0030 §5 requires a receipt covering admitted attempts; an in-flight
  data-plane write or forge push lands outside every resource domain, so the
  domain receipt said nothing about it. Admission closure was also racy with
  registration, so the drain was closing over a set that could still grow.
- **At-least-once delivery had an identity and no mechanism** (§4). No
  acknowledgement, no sender retention obligation, and deduplication state held
  in memory — reset by exactly the restart it existed to survive.

And four more of the same shape as round two's: **preserving an operator wait is
not recovering it** (the record needs the substituted request, or the lost
continuation cannot be rebuilt); **a headless block waited for the adapter to
exit**, leaving a non-cooperative runtime free to keep working under a terminal
Story; **envelope validation was decorative**, with version, execution, epoch,
unknown types and malformed bodies all silently accepted; and **a correlation was
not bound to its action or arguments**, so one key could replay the result of
different work. Plus `message.ask`'s response lifecycle (§4) and the durability
table (§9).

**Mutations initially survived in two rounds, and each indicted the assertion
rather than the mutation** — a claim checking a consequence before the mechanism,
a claim whose precondition the mutation destroyed so it reported `ERROR` where
the behaviour was plainly broken, and two whose expected reason named the wrong
branch. Each is this repository's standing lesson arriving on schedule, and each
was caught only because the harness requires the failure to match a *named
reason* rather than merely to occur.

**The harness itself was wrong, and fixing it found something.** It read the
suite's exit as green from the output text alone, so a run that printed its
summary and then hung would have passed — demonstrated by construction, where
the old form called a deliberately-hanging suite green and the new form refused
it. Requiring a clean exit immediately surfaced a real defect: the host's
deferred `cancelRun()` ran *after* `cmd.Wait()`, so it waited on processes it
had already decided to kill. The suite went from **5m14s to 9s**.

**A fourth round found seven more, and the first was a claim reporting `PROVEN`
over a data race.** Review ran the suite under `-race`; the resumption path had
two continuations settling one attempt, and nothing in the suite noticed. Every
run is now race-instrumented, which is the cheapest thing that would have caught
it. The rest were guarantees the ADR stated and the code did not implement:
recovery persisted a request ADR 0030 keeps out of Audit and promised more than
§6 offers; at-least-once had a watermark that could skip gaps, a scalar
acknowledgement across epochs, and an acknowledgement sent before the intent was
durable; "settled" was treated as "safely drained"; protocol violations and
transport failures bypassed the forced-stop path entirely; the question still
crossed inline despite the ADR requiring an artifact; and `started` was carried
without ever being compared.

**And one of those fixes was itself wrong in the same way**, caught by the suite
rather than by review: marking a drained attempt `stale` did not stop its
goroutine, which committed anyway. A mark nothing checks is not a mechanism —
ADR 0030 §5's own rejected option, rediscovered by implementing it.

**Uncovered and declared as such**, never fabricated: model calls and everything
shaped by them, concurrency accounting (§7 is reasoned, not measured), resumable
runtimes, the retention traversal (§9), composite execution, any transport but
the local one, and the data plane itself — the recorder is in-memory, and the
schema claims here were made by reading migrations rather than running them.

## Consequences

- **Phase 3 inherits a defined boundary and an open design space, which is what
  the blocker was for.** The amendment above keeps the boundary — identities,
  axes, mediated actions, fencing preconditions — and hands back the lifecycle
  and delivery mechanism. What Phase 3 must not do is re-derive a replacement on
  paper: the demoted items are settled by building a consumer, and the first
  consumer's needs are the evidence.
- **The v1 agent framework becomes a classification task, not a preservation or
  replacement decision.** The Context now records what exists — role FSMs,
  `BaseStateMachine`, an untyped and non-atomic `StateStore`, `QUESTION`, and
  `SUSPEND`'s return-to-origin resumption. Each of those is retained, refactored,
  or retired on evidence. The one that is already decided is `StateStore`:
  `os.WriteFile` of a `map[string]any` is not a durable checkpoint.
- **Phase 3 gets one boundary instead of two.** A native Go agent and an adapted
  external runtime meet the same contract, so ADR 0030's claim that no tool
  reaches its effect around the boundary becomes demonstrable rather than
  aspirational. v1's MCP path — Maestro tools executed with no record at all — is
  the shape this closes.
- **`tool_calls` needs a migration bigger than ADR 0030 §8 asked for.** That
  section calls it additive; it must also **replace
  `tool_calls_finished_check`**, because settling an attempt requires a boolean
  `succeeded` and the reconciliation outcome `unknown` is neither true nor false.
  The record cannot currently express a state ADR 0030 requires of it.
- **Four axes are four columns, and consumers switch on one of them.** Code that
  wants "did this work?" reads execution status; code that wants "was there
  anything to do?" reads the disposition. v1's single `Signal` enum forced every
  consumer to switch over a set whose members are not alternatives.
- **#280 and #317 gain a place to be fixed rather than a fix.** The schema now
  has a field for an already-satisfied completion and a durable `blocked`
  terminal; the code changes remain Phase 3 items.
- **[#319](https://github.com/SnapdragonPartners/maestro/issues/319) is
  unblocked and its home is two-level.** Lifecycle facts key on the served
  identity, lineage on the underlying model, with a nullable reference between
  them whose null is ADR 0020's existing `unclassified`. Whether that is two
  tables is #319's decision.
- **Provenance acquires a real cost.** Bindings per model call are more data than
  a status field, and the alternative — a status nobody can check — is what
  ADR 0031 §3 refuses. The retention traversal is chosen and unbuilt, so a cited
  call's reconstruction can still rot; the availability state makes that
  *visible*, and claiming more would repeat the overclaim ADR 0031 corrected.
- **An adapted runtime's honesty becomes measurable and mostly absent.** Today's
  Claude Code path reports no bindings, so it records as unclosed — a statement
  about its adapter, not about it, and a definite thing for an adapter author to
  build toward.
- **Blocking is affordable for a native agent and costs a process for an external
  one.** §7 makes the runtime declare whether it can be released and resumed, so
  holding an OS process across a human's attention span is a decision rather than
  a default. ADR 0030's "a suspended goroutine and nothing more" is exact for the
  in-process case and was never a claim about the other one.
- **A run can exhaust resource capacity while runnable concurrency sits idle.**
  That is the correct behaviour under §7's two-limit accounting and it is legible
  as a queue on a named resource type, rather than a run that mysteriously stops
  progressing.
- **The contract is one more thing to version.** Additive-within-version and a
  handshake keep that cheap; what it buys is that an external agent can be
  written against a stable surface, which is the precondition for the `maestro-agent`
  extraction staying an evidence-based Phase 8 decision rather than a rewrite.

### Deferred

**Demoted to Phase 3 design inputs by the 2026-08-15 amendment:** the complete
execution FSM; restart, resume, re-attach and outstanding-action enumeration;
epochs, acknowledgements, watermarks and durable sender outboxes; the generic
question-wait lifecycle; durable reusable approvals; and any persistence family
implied solely by those. The Status Of Decisions section is the authority on
which is which.

**Deferred at acceptance:** policy content (candidate 12); amendment policy (A5); the knowledge base
(candidate 10); GitHub Actions presentation; `maestro-agent` extraction (Phase 8);
the #272 routing implementation; the #317 and #280 code fixes; scheduling policy
including the release rule for a resource held by a blocked execution; remote and
socket transports (candidate 14); the provenance retention traversal's mechanism
(Phase 4); composite and paired execution's runtime (Phase 5); and where the
conformance executable lives permanently.

### Responsibility split

| Item | Owner |
| --- | --- |
| The Status Of Decisions section's **binding** list: the versioned boundary, the four axes and their applicability rule, the three model identities, fenced references and capabilities, no data-plane access, mediated actions with durable intent and result records, the drain-and-fence precondition on a positive terminal result, rejection of superseded or fenced authority at every mediated boundary, and the required provenance facts | **This ADR** |
| The Status Of Decisions section's **provisional** list — the execution FSM, restart/resume/re-attach and outstanding-action enumeration, the delivery mechanism, the question-wait lifecycle, reusable approvals, and any persistence family implied solely by those. Settled against real consumers, not replaced with another speculative design | **Phase 3 plan** |
| Which policies exist and which approval scopes each gate exposes | **Candidate 12** |
| When a cancellation is legitimate, and what amendment does to pending actions and grants | **A5** (ADR 0019 amendment) |
| The `tool_calls` migration including the constraint replacement, **and the durable families §9 requires** — execution configurations, bindings and epochs, resume tokens, event watermarks, operator decisions, response waits, and the attempt's correlation binding and disposition; the reconciler's scoping in code; watchdog policy for the waits; the headless runner's exit behavior; the retention window and release rule for a waiting resource; the routing implementation; concurrency limit values; whether an answer may reach a live execution; **a durable sender outbox surviving the adapter's own restart**, which §4 requires and the conformance slice implements only in process | **Phase 3 plan** |
| The provenance retention traversal's mechanism, and the evidence-package half | **Phase 4** |
| The metadata home for served-model lifecycle and underlying-model lineage | **[#319](https://github.com/SnapdragonPartners/maestro/issues/319)**, against §3's split |

## Authority Reconciliation On Acceptance

Established by grepping the **concept** rather than the word, because two of the
entries below never say "execution contract". **None of these is edited while
this ADR is Proposed** — a live document asserting a decision nobody has accepted
is the gap the reconciliation sweep opens if it runs early. All of it lands in the
final reviewed commit.

| Location | Change |
| --- | --- |
| [ADR backlog](../v2/notes_adr-backlog.md) slot 13 | Mark **RESOLVED**, pointing here. The slot keeps its number, per its own citation rule. Its instruction is explicit: *mark this slot RESOLVED when the contract ADR is Accepted* |
| [ADR backlog](../v2/notes_adr-backlog.md) slot 16 | **Narrow one clause.** It reads that a retirement date and a lab are "the same kind of fact about a model ID", which is true of their *kind* and false of their *key*: §3 puts retirement on the served identity and lineage on the underlying model. Read strictly, the current wording asks #319 to build one key — the thing D8 already refused |
| [ADR 0030](0030-tool-execution-policy-hook.md) §8 and its Consequences | **Amend the "additive migration" wording.** §8 asks Phase 3 for an additive migration adding nonterminal states; the Context above establishes that it must also **replace `tool_calls_finished_check`**, because a settled attempt requires a boolean `succeeded` and §8's own `unknown` outcome is neither. Two live ADRs must not knowingly disagree about the same migration, so this lands in the same acceptance commit rather than being left for whoever writes it |
| [Pre-Phase-3 blockers](../v2/phase_3/plan_blockers.md) item A4 | Add the RESOLVED banner, in the form A1–A3 use, recording what the ADR settled differently from what the item asked |
| [Pre-Phase-3 blockers](../v2/phase_3/plan_blockers.md) Track C | #319's dependency on A4 is discharged; the split it waited on is §3 |
| [Parking lot](../v2/notes_parking-lot.md) | The graduation pointer names the ADR, not only the candidate and the issue |
| [ADR README](README.md) | Status **Proposed → Accepted**; the summary is already indexed verbatim |
| This ADR and [its spike report](../v2/phase_3/spike_execution-contract.md) | Front matter `draft` → `live`; body **Proposed → Accepted** |
| [Issue #282](https://github.com/SnapdragonPartners/maestro/issues/282) | The tracker copy. Its acceptance criteria mix the contract with Phase-3-and-later work — the Actions example, blocking workflow status, the OpenHands and Goose mappings — and the blocker plan's scope decision 1 already separated them. Amend it to record which criteria this ADR discharges and which are Phase 3's |

**Deliberately not changed**, recorded so nobody "reconciles" them: the Phase 2
[exit record](../v2/phase_2/notes_exit-record.md) and
[import design](../v2/phase_2/design_slice_import.md) cite #282 for the
**benchmark-evidence-reviewer agent** and the missing `accept` verb. That is
#282's other half, it is Phase 3 work, and this ADR does not build it — so those
statements stay true.

### Propagation of the 2026-08-15 amendment

Same discipline, run against the **concept** rather than the word. Every location
that restated a demoted item as a requirement was corrected in the amending
commit.

| Location | Change |
| --- | --- |
| [ADR 0030](0030-tool-execution-policy-hook.md) §8 and its responsibility split | Two edits. §8 handed A4 "the vocabulary"; the vocabulary is now Phase 3's, while §8's own **requirement** — that a healthy operator wait, a healthy resource wait, and an interrupted attempt be distinguishable — stays where it was. The split table's A4 row is divided accordingly |
| [Blocker plan](../v2/phase_3/plan_blockers.md) item A4 | A **SCOPE CORRECTED** banner beneath the RESOLVED one: what overran, what binds, what became a design input, and that the slice's reach rather than its size was the defect |
| [Blocker plan](../v2/phase_3/plan_blockers.md) "In Phase 3's Plan" | The v1 classification direction and the seven-step sequence, parked explicitly **for `plan_scope.md`, which does not exist yet**, so A6 copies it rather than rediscovering it. Marked direction, not decision |
| [ADR backlog](../v2/notes_adr-backlog.md) slot 13 | The amendment recorded, and two carry-forward bullets narrowed: the invocation split now rests on gate 3 rather than on restart, and artifact-level recovery is stated as the rule without the `stale` mechanism |
| [Spike report](../v2/phase_3/spike_execution-contract.md) | A **What this is evidence of, and what it is not** section, and a front-matter summary that says isolated contract model. A `PROVEN` claim is evidence about the model, not about Maestro |
| [Phase 3 README](../v2/phase_3/README.md) and [ADR README](README.md) | Both quote a front-matter summary verbatim; both summaries changed, so both were updated |
| [Issue #282](https://github.com/SnapdragonPartners/maestro/issues/282) | The resolution comment overstated what the slice discharged — it claimed an adapter validated independently of the Maestro factory, and composite/paired provenance the run declared **uncovered**. Corrected in place |

**Not changed, and why:** the [parking lot](../v2/notes_parking-lot.md) pointer
names the ADR and the resolution without restating any mechanism, so it stays
true. `docs/archive/` is out of scope by ADR 0017, and its `epoch` is v1's
failure-recovery scope — a different concept that a word-level sweep would have
"reconciled" wrongly.

**Corroboration, not conflict:** [ADR 0020](0020-review-invariant-reviewer-vs-partner.md)
already states that serving is not origin and that "lineage needs its own
declared attribute." §3's two-key split is what that attribute attaches to, so
0020 needs no amendment.

## Related Documents

- [Pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md) item A4 and its three
  scope decisions; [ADR backlog](../v2/notes_adr-backlog.md) candidate 13.
- [Conformance slice report](../v2/phase_3/spike_execution-contract.md) and the
  [executable](../../spikes/phase_3/executioncontract/README.md).
- [Issue #282](https://github.com/SnapdragonPartners/maestro/issues/282) (the
  contract and its vertical slice),
  [#272](https://github.com/SnapdragonPartners/maestro/issues/272) (contract
  portion absorbed here),
  [#280](https://github.com/SnapdragonPartners/maestro/issues/280) and
  [#317](https://github.com/SnapdragonPartners/maestro/issues/317) (two of the
  four axes' feeders),
  [#319](https://github.com/SnapdragonPartners/maestro/issues/319) (whose metadata
  home depends on §3).
- [ADR 0019](0019-orchestrator-boundary.md), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md)
  (lineage, and the classified edge §9 preserves),
  [ADR 0021](0021-artifacts-and-principal-instances.md),
  [ADR 0022](0022-v2-data-plane.md),
  [ADR 0028](0028-artifact-envelopes-and-payload-schemas.md),
  [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §2, §5, §7, §8,
  [ADR 0030](0030-tool-execution-policy-hook.md) (the boundary every mediated
  action meets, and the responsibility split this ADR discharges),
  [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §2 and §3.
- [notes_execution-contracts.md](../v2/phase_3/notes_execution-contracts.md) — the
  build/test/deploy contract set, Phase 3 plan material.
