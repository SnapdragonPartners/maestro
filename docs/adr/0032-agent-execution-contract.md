+++
title = "ADR 0032: The Agent Execution Contract"
edit_date = "2026-08-13"
status = "draft"
summary = "Agents reach Maestro only through a versioned wire contract, so a native Go agent and an adapted external runtime meet the same boundary and neither receives a local path or a database connection. An invocation carries resolved identity — principal, effective work version, model route, prompt pack, capabilities, and fenced resource references — and is immutable for the life of the execution. Events report and the Orchestrator's own records decide, so a fact the Orchestrator could not observe is recorded as reported rather than observed. The terminal result is four independent axes rather than one status list, so an already-satisfied Story, a superseded cancellation, a gate awaiting an operator, and an infrastructure failure stop colliding. A blocked execution consumes no runnable concurrency and does consume resource capacity, which is the accounting that makes ADR 0030's blocking affordable."
+++

# 0032. The Agent Execution Contract

Status: **Proposed** — drafted by Claude 2026-08-13. Item A4 of the accepted
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

**There is no contract.** The nearest thing is `pkg/coder/claude`, which runs an
external agent as a subprocess and is the closest v1 comes to the boundary this
ADR specifies. What it has instead of a contract is four separate shapes that each
carry part of the job, and the way they disagree is the argument for the four axes
in §5.

**The result type is a role-shaped union.** `claude.Result`
(`pkg/coder/claude/types.go:94`) carries `Plan`, `Summary`, `Evidence`,
`ExplorationSummary`, `Question`, and `ContainerSwitchTarget` — one field per
signal, so a new role or a new outcome adds a field to a struct every caller
switches on.

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
and is stored as free text in `KeyCompletionDetails`. It survives as a
**control-flow branch** and as prose; there is no terminal-result record with a
field to carry it, because v1 has no terminal-result record at all. That is the
precise shape of [#280](https://github.com/SnapdragonPartners/maestro/issues/280):
not a distinction nobody drew, but one drawn and then dropped.

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

### 2. The invocation

**The invocation is resolved, immutable, and complete.** Everything requiring a
decision is decided before it is sent; the runtime looks nothing up. This follows
directly from ADR 0019 — resolution is rules, not judgment — and from ADR 0031 §4,
which fixes pack resolution at dispatch for the same reason.

| Field | Why it is here |
| --- | --- |
| **Contract version** | Negotiated at handshake (§11); a mismatch fails before any resource is acquired |
| **Invocation identity** | The correlation key for every event, action, and record belonging to this execution |
| **Principal instance** and **role** | ADR 0021's accountable identity; the role names what the work needs, not who implements it |
| **Work scope and effective version** | Version-bound dispatch ([ADR 0019](0019-orchestrator-boundary.md) as amended). ADR 0030's admission gate compares each action against this |
| **Seeding artifact references** | The Management artifacts this instance starts with, recorded in `principal_instance_inputs` (ADR 0021). References, never inlined content |
| **Model route and served identity** | §3 |
| **Prompt pack** — name, scheme-qualified digest, content reference, installation identity and revision | [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §2. The name is a label and is carried for humans and for validation, never dereferenced |
| **Resolved capability set** | §8. Orchestrator-owned action identities, not the runtime's tool names |
| **Fenced resource references** — for the Incubator and, where the work requires one, the Habitat: the reference and its **instance generation** | [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §5 and §8. Never a local path |
| **Budgets and limits** | Token, cost, wall-clock, and iteration bounds. ADR 0030 §10 puts budget enforcement here rather than at its boundary, because an LLM call is not a mediated action |
| **Operator-responder availability** | ADR 0030 §4: headless is a **declared configuration known at dispatch**, never an observation that nobody answered |
| **Expected source contract** | [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §3, obligation 1: which of the four provenance sources this runtime will report, and by what mechanism. A capability claim, made before there is anything to account for |

**What the invocation must never carry**, each because something in the accepted
set forbids it:

- **A filesystem path into an execution resource.** ADR 0029 §8. A resource
  reference plus a generation is what replaces `RunOptions.WorkDir`.
- **A database connection, DSN, or credential for the data plane.** ADR 0022.
- **A credential that would let the resource perform a mediated action
  directly.** ADR 0030 §7 makes this the test of whether an action is mediated at
  all: a resource holding a forge credential can push without asking, and the
  boundary becomes irrelevant with no code change anywhere.
- **Unresolved selection of any kind** — a pack name to look up, a model name to
  infer a provider from, a capability to be decided later.

**The invocation is immutable for the life of the execution.** A change to any
resolved value is a new dispatch. ADR 0031 §4 already fixes this for the pack and
gives the reason generally: a lever that changes under a running execution is a
silent swap with no version behind it.

**Restart reuses the invocation; it does not re-resolve.** ADR 0029 §2 scopes the
Incubator to the Story execution rather than the agent principal, so a replacement
agent resumes the same execution, and ADR 0031 §4 draws the consequence for the
pack. The same rule covers every resolved field, for the same reason: re-resolving
would let a configuration edit between the crash and the restart change the
factory mid-Story with nothing recording that it had happened.

### 3. Model identity: the route, the served offering, and the underlying model

The blocker plan's scope decision 3 requires this ADR to settle whether a
provider's **served** model identity and the **underlying** model identity are one
key. **They are not**, and the invocation carries a third thing that is neither.

| Concept | Is | Keyed on | Carries |
| --- | --- | --- | --- |
| **Route** | Where the request goes | provider + endpoint + the provider's model name | Nothing durable. It is deployment configuration |
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
emits no events at all still produces a complete action history, because the
actions went through the boundary.

The event kinds:

| Event | Carries | Note |
| --- | --- | --- |
| `started` | The runtime's own identity: adapter, executable version, effective contract version | The first message after the handshake |
| `heartbeat` | Liveness, and the runtime's current phase | Separates a working execution from a hung one without inferring either from output volume |
| `activity` | Human-facing progress | Never load-bearing. v1's `INACTIVITY` signal is an observation of this channel, and §6 keeps liveness off it |
| `action_request` / `action_result` | A mediated action and its outcome, correlated (§6) | The request is a request. What happens is ADR 0030's, and the result the runtime receives is what that boundary returns |
| `question` | An agent's question for another principal | A **nonterminal** state, not an outcome — the distinction v1's `QUESTION` signal loses |
| `usage` | Token axes and, where the runtime knows it, cost | §9 governs whether this is observed or reported |
| `provenance` | Per-call source bindings and closure status | [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §3, obligations 2 and 3 |
| `warning` | A condition the runtime wants recorded and did not treat as fatal | |
| `terminal` | The four-axis result (§5) | Exactly one per invocation, and it is a **claim** |

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

**Delivery is at-least-once and ordered per invocation.** A duplicate event is
idempotent by its own identity. Ordering across invocations is not promised and
nothing may depend on it.

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
| **Failure class** | `retryable_infrastructure`, `non_retryable_agent` | Required iff status is `failed` or `timed_out` |

**It is a schema with an applicability rule, not a cross product.** An axis that
does not apply is absent, not defaulted. A `completed` result with a
`failure_class` is invalid, and so is a `cancelled` result without a reason.

Notes on the axes that are easy to get wrong:

- **`blocked` carries no reason enum of its own.** It references the pending
  action and the structured requirement set ADR 0030 §3 already defines. Inventing
  a parallel vocabulary here would duplicate candidate 12's rules in a second
  place and let the two drift.
- **`timed_out` is a status rather than a failure class** because a deadline is an
  Orchestrator-observed fact while an error is a runtime-reported one. Collapsing
  them would lose which party ended the execution, and they are retried
  differently: a timeout is retried with a larger budget, an error as-is.
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
agent cannot see**, established by ADR 0030's boundary and the invocation's
declared responder availability. The agent stops; the Orchestrator records.

This fell out of the conformance slice rather than being designed: the headless
agent reaches a decision it cannot get answered, stops issuing turns, and exits
**without a terminal event**, and the host composes the result from the
boundary's own state. An agent that named itself blocked would be asserting
something it has no way to know.

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
| `settled` | An outcome was determined: succeeded, failed, denied, or **unknown** | Yes |

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

**Reconciliation acts on `open` and on nothing else**, and this is a rule the
conformance slice had to discover. ADR 0030 §8 states that the *watchdog* leaves
both waits alone; the **reconciler is a different actor** and no rule was written
for it. A first implementation settled every attempt that was not already
settled, which swept up an attempt sitting healthily in `operator_waiting` and
destroyed the requirement reference a `blocked` result must carry (§5) — so the
execution reported itself blocked on nothing.

An attempt in a **declared** wait is healthy, not interrupted. That is the whole
distinction the nonterminal states exist to draw, and the first implementation
collapsed it one level below where ADR 0030 drew it. Phase 3's reconciler owes
the same scoping.

**Entering and leaving a wait is a durable transition**, per ADR 0030 §8, not the
absence of a completion.

#### Cancellation

Cancellation is cooperative first and fenced second, and the ordering is
load-bearing because A5 rests on it.

1. **The Orchestrator requests cancellation** over the contract, with a stated
   deadline.
2. **The runtime is permitted to reach a safe boundary** — completing an atomic
   action already in flight — and must issue no further actions.
3. **On expiry, the resource's domain is fenced** per
   [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §7, which is the
   only thing that actually stops a process. Revoking authorization does not:
   a running process needs none.
4. **The terminal result is recorded only after a positive receipt.**
   `terminated` or `isolated` both satisfy this; `unconfirmed` does not, and the
   execution stays non-terminal with the resource quarantined.

Step 4 is A5's stated rule and this ADR is where the lifecycle carries it. A
terminal result written while an unfenced process may still be writing is a false
record, and downstream work would be dispatched against a resource that is not
free.

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

- **Restart** replaces the agent process for the same execution. The invocation is
  reused unchanged (§2). ADR 0029 §2 makes this possible by scoping the resource
  to the Story rather than the principal.
- **Resume** is a runtime capability, declared at the handshake. A runtime that
  can resume its own session (Claude Code's `--resume`, v1's `SessionID`) may be
  offered a resume token that is **opaque to the Orchestrator**; a runtime that
  cannot is restarted from the last durable workflow artifact, which ADR 0030 §4
  already names as the recovery state actually promised.
- **Re-attach** is transport plumbing. Each action the runtime issues carries a
  **correlation identity**; the Orchestrator's response carries the attempt
  identity that owns ADR 0030 §3's at-most-once semantics. A runtime rejoining
  an execution re-announces its outstanding correlation identities and receives
  their current state.

**Re-attach and restart coincide on the local transport**, and a first draft of
this section said "after a transport reconnection" as though they were separate.
Over stdio they are not: **a broken transport is a dead process**, so there is no
live runtime to reconnect to and the only case the local transport presents is a
*restarted* runtime rejoining an existing execution. The reconnection wording is
retained because a socket or remote transport will need it, and it is now stated
as what it is — a case Phase 3's transport does not produce.

**A correlation identity must be derivable, not merely chosen.** The runtime
picks it, but the choice must survive the runtime's own death: a restarted
runtime cannot re-announce identities it invented and then lost. Deriving them
from the invocation identity and a step ordinal is sufficient and is what the
conformance slice does. Without this rule at-most-once holds within a process and
silently stops holding across a restart, which is precisely the boundary a
restart crosses.

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

- A runtime that **declares itself resumable** may be released while blocked and
  re-invoked with its resume token on approval.
- A runtime that does not is **held resident**, and counts against a process
  budget that is neither of the two limits above.

Phase 3 owns the budget's value. What this ADR fixes is that holding an external
process across a human's attention span is a cost someone must have decided to
pay, not one that arrives by default.

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
| Malformed message, unknown required field, contract-version violation | The execution fails. `failed` / `non_retryable_agent` |

Collapsing these would either kill an execution for an ordinary refusal, or let a
runtime keep going after it stopped speaking the protocol.

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

1. **The expected source contract**, on the invocation (§2). Which of the four
   sources this runtime will report, and by what mechanism. A claim about
   capability, made before there is anything to account for.
2. **Per-call source bindings**, on the `provenance` event: the exact contributing
   references and digests — the pack identity, the H pair, the seeding-set
   artifact digests, and references to the specific messages, tool results, and
   retrievals that entered the call.
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
numbers that would make the scenarios look green. That is ADR 0025's
`unavailable`-versus-zero discipline applied to test evidence, and the same
posture the [Docker fencing spike](../v2/phase_3/spike_docker-fencing.md) took
when it recorded its own eleven defects rather than quietly repairing them.

### What was run, and what it changed

**Done**, 2026-08-13:
[`spikes/phase_3/executioncontract`](../v2/phase_3/spike_execution-contract.md).
**22 claims, all `PROVEN`**; sixteen spawn a real external process and speak
newline-delimited JSON to it. **7 of 7 mutations killed for their named reason**,
under a harness that requires a positive control, refuses to start on residue,
and verifies restoration by digest.

**Five findings changed this ADR**, and the first is a defect in the design
rather than in the harness:

1. **Reconciliation must be scoped to `open`** (§6) — it was destroying the
   requirement set a `blocked` result must reference. Now a permanent mutation.
2. **Re-attach over stdio is restart, not reconnection** (§6).
3. **Correlation identities must be derivable** (§6), or at-most-once stops
   holding at exactly the boundary a restart crosses.
4. **An invalid terminal result is a protocol violation** (§5), not a result to
   record and reason about later.
5. **`blocked` is the Orchestrator's to record** (§5), not the agent's to claim.

**One mutation initially survived and indicted the assertion rather than the
mutation** — a claim was checking a consequence before the mechanism, so a
neighbouring guard fired first. Recorded because it is this repository's standing
lesson arriving on schedule, and it was caught only because the harness required
the failure to match a *named reason* rather than merely to occur.

**Uncovered and declared as such**, never fabricated: model calls and everything
shaped by them, concurrency accounting (§7 is reasoned, not measured), resumable
runtimes, the retention traversal (§9), composite execution, any transport but
the local one, and the data plane itself — the recorder is in-memory, and the
schema claims here were made by reading migrations rather than running them.

## Consequences

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

Policy content (candidate 12); amendment policy (A5); the knowledge base
(candidate 10); GitHub Actions presentation; `maestro-agent` extraction (Phase 8);
the #272 routing implementation; the #317 and #280 code fixes; scheduling policy
including the release rule for a resource held by a blocked execution; remote and
socket transports (candidate 14); the provenance retention traversal's mechanism
(Phase 4); composite and paired execution's runtime (Phase 5); and where the
conformance executable lives permanently.

### Responsibility split

| Item | Owner |
| --- | --- |
| The wire contract, the four-axis result, the action and execution state vocabularies, cancellation lifecycle, re-attach, provenance obligations, the model identity split, and the concurrency accounting | **This ADR** |
| Which policies exist and which approval scopes each gate exposes | **Candidate 12** |
| When a cancellation is legitimate, and what amendment does to pending actions and grants | **A5** (ADR 0019 amendment) |
| The `tool_calls` migration including the constraint replacement; the reconciler's scoping in code; watchdog policy for the waits; the headless runner's exit behavior; the retention window and release rule for a waiting resource; the routing implementation; concurrency limit values | **Phase 3 plan** |
| The provenance retention traversal's mechanism, and the evidence-package half | **Phase 4** |
| The metadata home for served-model lifecycle and underlying-model lineage | **[#319](https://github.com/SnapdragonPartners/maestro/issues/319)**, against §3's split |

## Related Documents

- [Pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md) item A4 and its three
  scope decisions; [ADR backlog](../v2/notes_adr-backlog.md) candidate 13.
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
