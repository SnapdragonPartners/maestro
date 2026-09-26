+++
title = "Design: The Mediated Execution Boundary (Item 5)"
edit_date = "2026-09-26"
status = "draft"
type = "design"
summary = "Mini-plan for Phase 3 item 5: one Orchestrator-owned boundary package through which every agent-initiated action reaches its effect, with mandatoriness demonstrated by an exact-set guard on the one remaining direct call site rather than asserted; a code-resident action-family registry on ADR 0028's payload-type pattern that declares, per family, the argument schema with its safe projection and secret slots, the effect site, the commit point, the mediation-checkability answer and the reconciliation probe; ADR 0030's three gates in order — deterministic admission against the seam, a default-allow policy hook that may not infer or write, a logical operator wait that holds no transaction, and revalidation immediately before the effect — with the requirement-identity vocabulary item 2 left opaque defined here; attempt identity as the tool-call id so at-most-once is a row property and a recorded intent with no outcome resolves unknown; substitution of every secret by a version-pinned reference before the digest, with the raw value held in a redacting wrapper and revealed only inside the family's effect function; attempts registering against the execution before admission completes so admission closure linearizes on the execution row, with the generation-level key left to item 7; rejection of superseded authority at both gates from the execution row under lock, and of fenced references through a generation predicate whose real source is item 7's; the four-axis terminal result as a validated Go type and as CHECK constraints that make invalid combinations unrepresentable, recorded through a verb that carries fence-receipt discipline from the first day; migration 000024 giving executions their immutable capability set and terminal columns and tool_calls a reason code, with the seam's outcome refusal lifted; a v2-neutral forge seam with the Gitea API client ported by copy, and the first secret-bearing family — the Story pull request — proven against a live, digest-pinned Gitea in the integration job; the MCP lastEffect slot and its signal correction removed as the second of two channels that could disagree, leaving the stdout detector as v1's one channel until item 8; and the toolloop refactored onto an executor seam, its harness layer moved behind it unchanged, with terminal-tool forcing made expressible. Carries two plan amendments — the release rule for a resource held by a waiting execution is assigned to item 7, and the agent-surface inventory gains the pkg/tools row it lacks — and adds no ADR need."
+++

# Design: The Mediated Execution Boundary (Item 5)

Mini-plan for **Phase 3 item 5** (`execution-boundary`), the first item of
block B and the one every later item is built against: *"ADR 0030's three
gates and ADR 0032's binding boundary items … The toolloop is refactored
behind it, and the MCP `lastEffect` and signal-correction path is removed …
Also carries the vault's first live reader."*

Status: **draft** — awaiting Codex review and DR acceptance. Flips to **live**
in the acceptance commit, following items 3 and 4. Its two plan amendments
and one in-place inventory amendment are applied in that commit and not
before.

Reference tree: `main` at `b587bffd` (Checkpoint 1 merged; block A closed),
plus this branch.

## What Binds This Item

| Source | What it binds here |
| --- | --- |
| [Plan](plan_scope.md) item 5 | The line quoted above, in full: three gates; the five named ADR 0032 items — mediated actions with durable intent and result records, capability-scoped tools, the four-axis terminal result with its applicability rule, fenced resource references, rejection of superseded or fenced authority at every mediated boundary; the toolloop refactored behind the boundary; the `lastEffect` path removed; the vault's first live reader as one forge operation on a local Gitea, proven by a test |
| Plan decision 1 | Refactor the toolloop behind the boundary rather than rewriting it; the Claude adapter is item 8's, "not a side effect of item 5" |
| Plan decision 3 | The five demoted mechanisms are settled by their first consumer — item 6, 8 or 13 — and **none is built here** |
| Plan sequencing | Item 5 is the head of the chain 5 → 6 and depends on nothing in block B; items 7, 8 and 9 depend on it. It therefore consumes no fencing, no resources, no agent core |
| [ADR 0030](../../adr/0030-tool-execution-policy-hook.md) | §1 one mandatory boundary, demonstrable; §2 three gates, resources last; §3 admission then policy, the request, the hook's three properties, at-most-once attempts, the three forms of input; §4 the blocking wait, headless, the four outcomes; §5 revalidation, requirement-set equality, commit points and the three drain dispositions; §6 the three-valued effect site; §7 mediation decided by what the resource cannot do; §8 recording — no new record type, open before effect, waits as durable transitions, a denial opened and completed together |
| [ADR 0032](../../adr/0032-agent-execution-contract.md) | Its *Status Of Decisions* — the closed list of thirteen. Items 4, 5, 6, 7, 9 and 12 are this item's; 1, 2, 10 and 13 attach through the plan's other clauses; 3, 8 and 11 are other items'. Everything the closed list does not name is a design input, including §6's execution FSM |
| [ADR 0019](../../adr/0019-orchestrator-boundary.md) second amendment | Enforcement supersedes an execution's *authority*; nothing already done is revoked. The rejection this item builds reads that authority |
| [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) §7 | A call issued by a fenced holder is rejected at the boundary even if it arrives after fencing; capabilities carry their issuing generation |
| [ADR 0022](../../adr/0022-v2-data-plane.md) / [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) | The tool call is the record; canonical JSON (RFC 8785) for everything digested; the code-resident type-registry pattern |
| [ADR 0027](../../adr/0027-concurrency-safety-for-shared-local-infrastructure.md) | Every transition on a shared row is serialized under that row's lock |
| Item 2 [design](design_work-hierarchy.md) | `000022`'s state and outcome vocabularies bind as the *migration*, not as ADR 0032 §6; the requirement-identity vocabulary and `tool_calls.arguments`' projection are this item's (its lines 85–86, 680–684, 705–712) |
| Item 3 [design](design_orchestrator-seam.md) | D2's closure rule; D7 / amendment 3 — the forge operation and its test are this item's; D9's obligation that `OpenWork` be extended for terminal results and outstanding-action states |
| Item 1 [inventory](inventory_agent-surfaces.md) | The toolloop's external contract is **retain**; its harness layer, `TerminalTool`, `internal/runtime` and `middleware/validation` are **refactor** behind this boundary; the `lastEffect` path is **replace** — "item 5 removes it; item 8 builds the real adapter"; the MCP transport is **refactor** and the two `cmd/maestro-mcp-*` binaries **retain** |
| [Process](../process_build.md) | Defect-Shaped Verification for every guard; Reachability Claims for every deletion |

Where this document and an Accepted ADR disagree, the ADR wins.

## Scope

Built here:

| Deliverable | Why it is item 5's |
| --- | --- |
| `internal/boundary` — the request, the three gates, the hook interface with its default-allow implementation, attempt identity, substitution, the requirement-set canonical form, the terminal-result type | The plan line's first clause; ADR 0030 §1 "Phase 3 must build the boundary before the tools" |
| The action-family registry, with exactly one production family — the Story pull request — and the test-only families the proofs need | ADR 0030 §3 "declared by the code-resident action schema, never chosen by the caller"; the forge operation is amendment 3's |
| Migration `000024` and the seam verbs over it: tool-call waits and all six outcomes, the reason code, the execution's immutable capability set and terminal columns, admission closure and supersession | ADR 0030 §8 owes the columns; `000022` and item 3 deferred the writers to "the execution boundary (Phase 3 item 5)" |
| `internal/forge` — the v2-neutral seam and the Gitea client ported behind it | Amendment 3 of 2026-09-02, verbatim in the plan line |
| The live-Gitea integration test of the forge family, the vault read inside it | "proven by a test against that forge"; the exit criterion "a secret has a live reader" |
| The toolloop's executor seam, the legacy executor for frozen v1 drivers, terminal-tool forcing | Plan decision 1; inventory rows 321–325 |
| Removal of `Server.lastEffect`, `ConsumeLastEffect` and the runner's signal correction | The plan line; inventory row 357 |
| The mandatoriness guard and the extension of item 3's closure guards | Exit criterion "no tool reaches its effect around the mediated boundary, and this is demonstrable" |

Deferred, each to the item that first has a consumer:

| Deferred | To | Why |
| --- | --- | --- |
| Resource acquisition, leases, generations, the fence receipt's real values, the `(resource, generation)` attempt key | Item 7 | No resource exists to acquire; D9 and D11 leave the seams the item fills |
| Reusable `*_for_story` approvals | Item 6 or 8 | ADR 0030 §4 "MVP may defer the reusable `*_for_story` grants"; one of the five demoted mechanisms |
| The wire boundary's language-neutral encoding and its external-process consumers | Item 8 | ADR 0032 item 1's *language-neutral* qualifier is met by a second consumer, not by a Go interface |
| Watchdog policy for the two waits; the cancellation sequence; #265's single-owner restart | Item 9 | The plan line for item 9 names all three |
| Capability derivation from the role and pack; the execution FSM; `QUESTION`'s mapping | Item 6 | D12 persists the set; item 6 decides what goes in it |
| The Claude Code adapter that replaces the removed path | Item 8 | Plan decision 1 |
| In-resource action families — build, test, lint, the verb inventory | Item 7 | The plan gives item 7 requirement-routing and the two run kinds; the families are defined beside the resources they run in |
| The branch-promotion (push) family | Item 10 | ADR 0030 §7's "the mediated act is the promotion"; item 10 performs the Story→Epic merge and is the writer of the satisfied edge |

**Not in scope, stated because a reader could reasonably expect it:** policy
content (candidate 12 is post-MVP; the hook ships with zero rules); the
scope invariant ADR 0030 §6 withdrew — this boundary polices what an agent
asks *Maestro* to do and nothing a runtime does inside its resource or on the
network; model invocation and budgets (ADR 0030 §10); `pkg/persistence`'s
deletion (item 14); #370's grammar fix, which folds into the next migration
that touches `prompt_pack_installations`, and this one does not.

## Decisions

### D1. One boundary package, and the toolloop reaches effects only through an executor it no longer owns

The boundary is `internal/boundary`, Orchestrator-owned and inside item 3's
closure rule: it imports the seam (`internal/dataplane/store`), `canonical`,
`nilcheck`, `secret` and nothing under `pkg/` except the two v2-neutral
leaves it needs (`pkg/logx`, and `pkg/tools`' type declarations for the
legacy adapter in D15 only). Its one entry point is

```go
func (b *Boundary) Mediate(ctx context.Context, req Request) (Result, error)
```

and its callers are the toolloop's v2 executor (D15) and, from item 8, the
wire adapter. There is no second entry point for "trusted" callers:
Orchestrator-initiated work is not agent-initiated and does not pass here
(ADR 0030 §10), and it is recorded by the seams that already record it.

**Why one package rather than a set of middleware.** v1's tool path has five
`tools.Tool.Exec` call sites, four unrecorded (ADR 0030 lines 49–56), and the
`logToolExecution` wrapper meant to record them "has no callers anywhere in
the repository". Middleware is an invitation to the same shape: a call site
that forgets the wrapper. A single function that is the *only* way to obtain
an effect, with the effect functions themselves unreachable from anywhere
else (D2), cannot be forgotten.

**What the toolloop keeps.** `toolloop.New`, `Run`, `Config[T]` and
`Outcome[T]` are retained as the inventory says (row 321); the loop's
dispatch step — `tool.Exec(toolCtx, toolCall.Parameters)` at
`pkg/agent/toolloop/toolloop.go:486` — is replaced by a call on a
`Config.Actions ActionExecutor` (D15). The loop no longer knows what a tool
*is*; it knows what an action request and result look like.

Rejected: putting the boundary in `pkg/agent`. That package is v1's and its
closure reaches `pkg/config` through `pkg/tools`; the Orchestrator's closure
guard (`internal/orchestrator/closure_test.go`) would have to admit it, and
the guard is the point.

### D2. Mandatoriness is demonstrated by an exact-set guard, not by review

ADR 0030 §1: *"The boundary is the only route to the effect, structurally —
not a function every tool is expected to call … Phase 3 must be able to
demonstrate the property rather than assert it."*

Two structural facts, each guarded:

1. **An action family's effect function is reachable only from the
   boundary.** Families live in `internal/boundary/families/<name>`, and the
   guard is an import-graph test in the style of item 3's closure guards:
   the set of packages importing any `families/*` package is exactly
   `{internal/boundary}` plus each family's own tests. A new importer fails
   the test with its name.
2. **`tools.Tool.Exec` has exactly one v2-path caller, the legacy
   executor.** An AST guard enumerates every call expression resolving to
   `(tools.Tool).Exec` across the applicable configurations (Reachability
   Claims: the constraint set derived at analysis time, over ADR 0026's
   matrix) and asserts the set is exactly the frozen v1 sites plus
   `toolloop.legacyExecutor.Execute`. The v1 sites are named in the guard
   with the item that deletes them (14); when one disappears the guard
   fails and its entry is removed, so the list can only shrink.

These prove the property for the v2 path. They do not make v1's four
unrecorded sites go through the boundary, and this design does not claim
they do: those sites are frozen and retire with item 14. The exit criterion
is met for the path the phase builds, and the guard says exactly where the
frozen path still bypasses it.

### D3. The action-family registry is code-resident, and a family declares everything the gates need

On ADR 0028's payload-type-registry pattern, `internal/boundary/registry`
holds the closed set of action families. A family is a value, not a
plugin:

| Field | Consumed by | Source |
| --- | --- | --- |
| `Kind`, `Verb` — an Orchestrator-owned identity, "not the caller's tool name" | Gate 1 admission; the record's `tool_name` as `<kind>/<verb>` | ADR 0030 §3 request table |
| `Schema` — the argument schema: required fields, types, and per field one of *persist*, *digest-only*, *secret slot*, *large (by reference)* | Substitution (D6), the persisted projection, policy's readable fields | §3 "declared by the code-resident action schema"; Consequences "every action family needs a declared safe projection before it can be recorded at all" |
| `EffectSite` — `orchestrator_side`, `in_resource`, `external` | Classification and what "policed per action" means for the family | §6's table |
| `Checkability` — one sentence: what prevents the execution resource from performing this directly | Reviewed, and rendered in the family's documentation; a family with an empty answer fails registry construction | §7 "a family with no answer is mediated in documentation only" |
| `CommitPoint` — the instant after which the effect is no longer the Orchestrator's to withhold | Gate 3's reconciliation and, from item 9, drain | §5 "every action family MUST declare its commit point" |
| `ConditionalCommit bool` — whether the effect site accepts a generation predicate | Item 9's drain disposition | §5's second disposition |
| `Effect` — the function that performs the action, receiving the revealed secrets and the resolved target | Gate 3 only | D1 |
| `Reconcile` — the probe that, given an attempt with no recorded outcome, determines whether its effect committed | Reconciliation of `unknown` (D5) | §3 "resolves as `unknown` and goes to reconciliation" |

Registry construction validates the set: identities unique, every schema
field classified, every family with a checkability answer and a commit
point, every `orchestrator_side` family with a reconciliation probe. This is
the same shape as item 4's prompt contract — the seam validates at
construction, so a malformed family cannot be loaded, let alone recorded.

**The one production family** is D13's Story pull request. Test-only
families — a no-op with a secret slot, a family that fails after its commit
point, a family that never returns — live under the boundary's tests and
are what the proofs run. They are not registered by the composition root;
the guard in D2 counts them as their own tests.

Rejected: registering `pkg/tools` tools as families through an adapter.
ADR 0030 §6 says two rows of its table "differ by who is asked, not by what
happens", and v1 tools do not declare which row they are in. Wrapping them
would give seventy families with an empty checkability answer, which the
registry refuses by construction.

### D4. Gate 1 is four deterministic checks against the seam, then a hook that may not infer or write

**Admission** — Orchestrator-owned, not policy, and not negotiable by any
hook (ADR 0030 §3): under the execution row's lock,

1. the principal instance is live and belongs to the execution;
2. the execution's authority is `current` and admission is open
   (`executions.authority_state`, `admission_closed_at`);
3. the family is contained in the execution's resolved capability set
   (D12); and
4. the intended target is one this execution may name — for item 5's only
   family, the repository the Story's Epic binds.

Each failure is a **denial with a reason code** (D12), opened and completed
in one transaction (§8). Check 2 is binding item 9's rejection at this
gate; D10 says why it is also gate 3's.

**Policy** — the hook:

```go
type Hook interface {
    Decide(ctx context.Context, d Decision) (Verdict, error)
}
```

`Decision` carries the request table's fields (§3) with the arguments
already substituted and projected to the schema's readable fields, and
nothing else: a hook cannot read what the schema did not declare readable
(§9's fourth constraint on candidate 12). `Verdict` is three-valued —
`Allow`, `Deny{Reason}`, `RequiresOperator{Requirements}` — and the hook
is bounded by a context deadline; an error or an exceeded bound is a
denial, fail-closed, with `reason_code = policy/hook_failed` (§3 "if the data
plane is unreachable, mediated actions stop"). The production hook is
`DefaultAllow`, carrying no rules. The boundary, not the hook, composes
requirement scopes by intersection; `DefaultAllow` never returns
`RequiresOperator`, so composition is exercised by the test hooks.

**The requirement-identity vocabulary** item 2 left opaque (`000022` lines
42–48): an identity is `<gate>/<kind>` with an optional `/<qualifier>`,
lower-case, from a closed enumeration this package owns — for item 5,
`policy/operator_approval` and `policy/operator_approval/<family>`. The
requirement set is a JSON object keyed by identity whose value is the
structured requirement: `{"question": …, "permitted_scopes": [...]}` with
scopes from `{once, for_story}`. Canonicalisation is `canonical`'s RFC 8785
and the digest is the seam's; the item 2 test "two evaluations collecting
the same requirements in different orders produce the same digest" is
written here against real requirements rather than opaque keys.

Rejected: a hook signature returning the *decision* (allow/deny) and a
separate *requirements* call. ADR 0030 §3 wants every applicable gate
evaluated and the complete requirement set collected before anything
blocks; one call returning one verdict is what makes "the operator answers
once" true by construction.

### D5. Attempt identity is the tool-call id, so at-most-once is a property of a row

The request carries `AttemptID uuid.UUID` (UUIDv7, item 2's convention). It
*is* `tool_calls.tool_call_id`. The boundary's first durable act for any
request is `OpenToolCall` under that id; the seam's insert is
`ON CONFLICT (tool_call_id) DO NOTHING` followed by a read, so:

- a transport retry with the same id finds the row and **re-enters no
  gate**: a settled row replays its recorded result; an open row in a
  declared wait returns *waiting*; an open row in no declared wait is
  *attempted, outcome unknown* and is handed to the family's `Reconcile`
  before anything else happens (ADR 0030 §3 "an attempt with a recorded
  intent and no outcome does not re-execute");
- intentional repetition is a new id, therefore a new attempt, therefore
  every gate again;
- the effect is committed at most once per id because the settle is a
  conditional update on `finished_at IS NULL` (`queries/tool_calls.sql:39`,
  already `:execrows`), and a second settle reports `Recorded: false`.

Reconciliation is bounded here to what item 5 can prove: `Reconcile` is
called synchronously on the retry path and on `Recover` for every
`open`-in-no-wait row belonging to the organization, and it settles the row
`succeeded` (the effect is found) or `unknown` with `reason_code =
attempt/interrupted` (it is not). A scheduled reconciler is item 9's, with
the watchdog.

### D6. Three forms of input; a secret is substituted before it is digested and revealed only inside the effect

ADR 0030 §3 names the forms and this design implements them literally:

| Form | Held where | Lifetime |
| --- | --- | --- |
| **Raw** — what the caller supplied | `Request.Arguments`, with every secret-slot value already inside a `secret.Value` (the redacting wrapper Phase 2 built: `internal/dataplane/secret/value.go`) | The call stack of `Mediate`; never digested, never logged, never persisted |
| **Substituted** — every secret slot replaced by `secret:<secret_id>@<version>` | Built by the family's schema; canonical JSON | Digested; presented to the hook; compared at gate 3 |
| **Persisted projection** — the schema's *persist* fields, plus `digest`, plus object references for *large* fields | `tool_calls.arguments` | The record |

**Where the secret comes from.** A secret slot is declared with a *secret
name* and the *scope* it resolves at; the family's slot for the forge token
is `forge.token` at repository scope. At gate 1 the boundary calls
`ResolveSecret(org, repository, actingUser, "forge.token")` — the six-step
ownership ladder Phase 2 built (`store/secrets.go:135-146`) — and records the
resolved `(secret_id, version)` as the substituted reference. Resolution is
a read of metadata; the plaintext is not yet in hand.

**Where it is revealed.** Immediately before the effect, inside gate 3,
`RevealSecret(org, secret_id, actingUser)` returns a `secret.Value`; the
family's `Effect` receives it and hands it to the client. The value's
lifetime is the effect call. If the version has moved between resolution
and reveal, the reveal is refused and the attempt settles `stale` with
`reason_code = stale/secret_version_moved` — the reference named a revision,
and that revision is what was approved (§3 "a substituted reference names a
revision, not a name"; §4 "what the approval binds to … the arguments").

**Why the substituted form carries the version.** Rotation. A reference by
name would make a later reveal fetch a token nobody approved; Phase 2 put
`secrets.version` into the key-derivation context precisely so that a
version is an immutable identity (`design_config_secrets.md:176`).

**Keyed commitments** for sensitive low-entropy non-secret values are
declared in the schema classification as a fourth kind and *not
implemented* here: no item 5 family has such a field. The classification
exists so a family that needs one cannot omit it silently.

Rejected: revealing at gate 1 and carrying the plaintext through the wait.
A blocked action can wait for hours; §4 says the wait "must not hold a
database transaction or a transport connection open", and holding a
revealed token in process memory across it is the same mistake in a
different resource.

### D7. Gate 2 blocks logically, and headless is declared at dispatch

When gate 1 returns `RequiresOperator`, the boundary:

1. writes the requirement set and digest onto the open row and transitions
   it `open → operator_waiting` in one statement (ADR 0030 §8 "entering or
   leaving a wait is a durable transition on that record");
2. marks the Story `awaiting_resolution` (item 2's conditional transition,
   under the Story lock);
3. returns `Result{Waiting: true, Requirements: …}` to the caller — the
   call returns; the *action* does not end. No transaction, connection or
   goroutine is held for the wait.

**While waiting**, a second agent-initiated request for the same Story is
refused at admission with `reason_code = invariant/story_awaiting_resolution`
and logged at error level as an invariant violation, not an ordinary
denial (§4). Human resolution, reconciliation and — from item 7 — cleanup
and fencing use other paths and are not agent-initiated.

**Resolution** is `ResolveOperatorRequirement(org, tool_call_id, Outcome,
decidedBy)`, an Orchestrator verb (a human acts through the UI or CLI, never
through the boundary): the outcome is one of `approve_once` or `deny_once`;
the `*_for_story` grants are deferred (ADR 0030 §4 permits it) and the
`Outcome` type has no other values until a consumer adds them. The decision
is stored on the row — durable, so a crash between decision and effect does
not lose it — and the row transitions `operator_waiting → open` (approve) or
settles `denied` with `reason_code = operator/denied` (deny). On approve, the
*caller* re-presents the same attempt id and gate 3 runs (D8); the operator
decision is consumed once and the row records that it was.

**Headless.** Whether a responder exists is a property of the *execution*,
read from the resolved configuration D12 persists (`headless bool`), "known
at dispatch — never an observation that nobody answered". Under headless,
the requirement is recorded on the row exactly as above and the row settles
**terminally** `blocked` in the same transaction, preserving the requirement
set (ADR 0032 §5 "a headless block is terminal for the action, not a
wait"); the execution's terminal result is then `blocked` (D11). Independent
runnable work is unaffected because nothing here touches any other Story.

Rejected: a wait implemented as a channel the caller blocks on. That is
v1's `SUSPEND` shape, and it is what item 3 D8's artifact-level restart
rule exists to replace.

### D8. Gate 3 revalidates everything and executes; in item 5 the resource step is a seam whose only implementation is "none required"

On the approved re-presentation (or immediately, when gate 1 allowed), under
the execution row's lock:

1. admission's four checks again — *"an unchanged policy that now denies
   still denies"*, and a superseded authority discovered here is D10's
   second rejection point;
2. the requirement set is recomputed and compared by digest to the one gate
   1 recorded; inequality in either direction settles the attempt `stale`
   with `reason_code = stale/requirement_set_changed` (§5 — "it does not
   return to gate 2 to collect a second approval");
3. the hook is re-evaluated **for a denial only**; `RequiresOperator` from
   the re-evaluation is not re-raised (§5 "approval clears the human
   requirement and nothing else");
4. resources: the family's `EffectSite` decides. `orchestrator_side` needs
   none, and item 5's only family is one. `in_resource` families call a
   `ResourceResolver` seam declared here with no production implementation
   — construction of a boundary with an `in_resource` family and no
   resolver is refused — and item 7 supplies it together with the
   `resource_waiting` transition's first producer. The transition verb and
   its test exist here (D12) so that item 7 adds a producer, not a column;
5. secrets are revealed (D6) and `Effect` runs;
6. the row settles: `succeeded` with the family's result projection;
   `failed` with `error_message`; or, when `Effect` returns after the
   declared commit point with an error that does not say whether the effect
   landed, `Reconcile` is called before settling.

Every attempt is completed, reads included (§8): a family whose effect is a
retrieval settles `succeeded` with the projection, because releasing data is
the security-relevant effect.

Rejected: revalidating without the execution lock. Item 3 D10 takes the
artifact lock the transitions themselves take; the analogue here is that the
authority check and the effect must see the same execution row, or the
window ADR 0030 §5 calls "admission to effect" is exactly where a
supersession could land unseen.

### D9. Attempts register against the execution before admission completes, and closure linearizes on that row

ADR 0030 §5: *"Attempts register against `(resource, generation)` before
admission completes; `Fence()` first closes admission for that generation,
then settles those already registered."* No resource or generation exists
in item 5, but the execution does, and `executions.admission_closed_at`
already exists (`000021:229-288`) with no writer.

So: D5's `OpenToolCall` *is* registration — it writes the row with
`execution_id` set, **inside the transaction that holds the execution row
`FOR SHARE`** and checks `admission_closed_at IS NULL`. `CloseAdmission(org,
execution_id)` takes the same row `FOR UPDATE` and sets the timestamp. The
lock order gives the linearization: a registration that took its share lock
before closure is a registered attempt closure must settle; one that
arrives after is refused at open with `reason_code =
authority/admission_closed`. The spike's claim
`boundary/admission-closure-linearizes-with-registration` is the same
property; here it is proved against Postgres rather than a mutex.

The `(resource, generation)` key is added by item 7 as a nullable column
pair beside `execution_id`, when a family first runs in a resource; the
registration and closure verbs gain a generation parameter then. Nothing in
item 5 has to be redesigned for that, because the registration-before-
admission ordering and the closure-first-then-settle ordering are the same
at either grain.

### D10. Superseded authority is rejected at both gates from the row under lock; fenced references through a predicate whose real source is item 7's

Binding item 9: *"A request carrying superseded or fenced execution
authority is rejected at every mediated boundary. The requirement is
binding. Epochs — and any other mechanism for detecting that authority —
are not."*

**Superseded.** The authority is the execution row's `authority_state`,
which item 2 defined with two values and no writer. This item adds
`SupersedeExecution(org, execution_id)` — sets `superseded` and closes
admission in one statement, satisfying `000021`'s
`executions_superseded_closes_admission_check` — and reads the state at gate
1 (D4 check 2) and gate 3 (D8 step 1), both under the row lock. Item 9 is
the caller that supersedes on a changed dispatch basis; here the caller is
the test. Requests refused for it settle `denied` with `reason_code =
authority/superseded`; an attempt that was `operator_waiting` when
supersession landed is settled `stale` with `reason_code =
stale/authority_superseded` by `SupersedeExecution` itself, preserving the
requirement and any decision (ADR 0019's amendment; the reconciliation
table ADR 0032 §6 sketches, which `000022` froze).

**Fenced.** A `ResourceRef` in a request is `{ResourceID, Generation}` —
never a path (binding item 4; ADR 0029 §8). Admission check 4 asks a
`GenerationSource` whether the reference's generation is current; the
boundary's implementation in item 5 is a table the tests populate, because
no resource exists to have a generation. This is stated plainly so a
reviewer does not read the test as a mocked fence: **the unit under test is
the rejection predicate and its placement at both gates, not fencing.**
Item 7 supplies the source from the lease record and the plan's rule that
fencing is tested against a live provider applies there.

### D11. The four-axis terminal result is a validated type and a set of CHECK constraints, and it is recorded through a verb that carries receipt discipline

ADR 0032 §5's table, as a Go type in `internal/dataplane/store`:

```go
type TerminalResult struct {
    Status                ExecutionStatus        // completed|blocked|cancelled|timed_out|failed — always
    CompletionDisposition *CompletionDisposition // changed|already_satisfied — iff completed
    CancellationReason    *CancellationReason    // superseded|operator_requested|shutdown — iff cancelled
    FailureClass          *FailureClass          // retryable_infrastructure|non_retryable_agent — iff failed
    BlockedToolCallID     *uuid.UUID             // iff blocked: the attempt carrying the requirement
}
func (r TerminalResult) Validate() error
```

`Validate` is the applicability rule: an axis that does not apply must be
absent, and one that applies must be present. A result that fails it is
refused and the execution fails `non_retryable_agent` (§5 "a result
violating the applicability rule is a protocol violation … not recorded and
then reasoned about downstream"). The same rule is a CHECK constraint on
the columns migration 000024 adds to `executions`, so a direct SQL writer
cannot store what the validator refuses — "makes invalid combinations
unrepresentable" is a property of the schema, not only of one Go path.

**Recording.** `RecordTerminalResult(org, execution_id, TerminalResult,
FenceReceipt)`: sets the terminal columns and `terminated_at`, at most once
(a second record is a conflict), and closes admission. The `FenceReceipt`
parameter exists from the first day because ADR 0032 §6 puts receipt
discipline on the *category* — any positive terminal result after admitted
actions, including `blocked` "at a gate no operator can answer" — and not
on `cancelled` alone. Its item 5 type has one value, `NoResourceHeld`, which
the verb accepts only when the execution has no open attempt in a resource
wait and (from item 7) holds no lease; item 7 adds the real receipts. The
alternative — record `blocked` without the parameter and add it later — is
exactly how a positive receipt gets issued while an action can still
commit, which is an exit-checklist criterion of items 7 and 9 this item
must not pre-empt.

**Who records what.** `completed`, `cancelled`, `timed_out` and `failed`
may be claimed by the runtime (item 6's agent core, item 8's adapter) and
are validated here; `blocked` is recorded by the boundary itself from D7's
headless path, referencing the attempt, because it is "a fact about a gate
the agent cannot see".

**`OpenWork` is extended** (item 3 D9's obligation): an execution with a
terminal result is not open work and leaves the projection; one with an
attempt in `operator_waiting` or `resource_waiting` is open and the
projection's row says which wait. Item 6 proves the extension against a
running agent; here the proof is the restart harness re-run with a
terminal result and a waiting attempt planted.

### D12. Migration 000024 and the seam verbs

One migration, three tables touched, every column with an ADR clause and a
consumer in this item:

| Table | Change | Clause | Consumer |
| --- | --- | --- | --- |
| `tool_calls` | `reason_code text`; CHECK: present iff `outcome IN ('denied','stale','unknown')` or `outcome = 'failed'` — `failed` keeps `error_message` as the human text and the code is optional; `succeeded` and `blocked` carry none (`blocked` carries the requirement set) | ADR 0030 §8 "with the reason code"; `000022:159-162`'s explicit deferral | D4, D5, D8, D10 |
| `tool_calls` | `operator_decision text`, `operator_decided_by uuid`, `operator_decided_at timestamptz`; CHECK: all three or none; decision in `('approve_once','deny_once')` | ADR 0030 §4 "the action-scoped decision is still durable, for crash recovery" | D7 |
| `executions` | `capability_set jsonb NOT NULL` — a JSON array of family identities, unique, sorted; `headless boolean NOT NULL`; both immutable after insert by an anti-update trigger on item 4's pattern | ADR 0032 item 10, the resolved-configuration lifetime: "what was resolved for an execution must not silently change"; ADR 0030 §4 "headless is a declared execution configuration, known at dispatch" | D4 check 3; D7 |
| `executions` | `status text`, `completion_disposition text`, `cancellation_reason text`, `failure_class text`, `blocked_tool_call_id uuid`, `terminated_at timestamptz`; the applicability rule as CHECKs; `terminated_at IS NOT NULL` iff `status IS NOT NULL` | ADR 0032 item 7 | D11 |

`capability_set` is supplied to `AcceptDispatch` by the Orchestrator; in
item 5 the composition root passes the set its caller declares, and item 6
derives it from the role and pack. An execution accepted before 000024 does
not exist on any plane this phase supports (the local plane is reset per
phase; the cloud plane was provisioned empty at #286), so the column is
`NOT NULL` without a backfill and the migration refuses to run against a
populated `executions` table — the same total-or-refuse shape item 4's
000023 used.

Seam verbs added, all on `store.Store` behind the ADR 0022 seam and each
with an integration test on a real ephemeral plane:

- tool calls: `OpenToolCall` (idempotent by id, registering against the
  execution under its share lock), `EnterOperatorWait`, `EnterResourceWait`,
  `LeaveWait`, `RecordOperatorDecision`, `SettleToolCall` with all six
  outcomes — which **lifts the refusal** at `store/postgres/toolcalls.go:119-127`
  and replaces it with the reason-code and requirement-set checks that
  refusal was standing in for;
- executions: `SetExecutionConfiguration` (inside `AcceptDispatch`),
  `CloseAdmission`, `SupersedeExecution`, `RecordTerminalResult`;
- `store.ToolCall` gains `ExecutionID`, `RequirementSet`,
  `RequirementSetDigest`, `ReasonCode`, `OperatorDecision` — the columns
  `000022` added and no Go type exposed.

`tool_calls.arguments` becomes the persisted projection by construction:
the only writer is `OpenToolCall` and it takes the projection the family's
schema produced, never the raw arguments. Item 2's deferral (its lines
705–712) is closed by that, not by a column change.

### D13. The forge seam, the Gitea client ported by copy, and the Story pull-request family

**The seam.** `internal/forge`:

```go
type Forge interface {
    CreateOrUpdatePullRequest(ctx context.Context, token secret.Value, spec PullRequestSpec) (PullRequest, error)
    FindPullRequestByHead(ctx context.Context, token secret.Value, head string) (*PullRequest, error)
}
```

Two methods, both of which the family needs — the second is its
`Reconcile` probe. The token is a parameter, never a field: a `Forge` value
holds no credential, so there is nothing to leak from it and nothing that
outlives the effect (D6). The repository binding (`base URL`, `owner`,
`repo`) comes from the repository record, "which is what the inventory's
own row says `forge_state.json` dies into" (item 3 D7).

**The port.** `pkg/forge/gitea/client.go` imports only `pkg/forge` and
`pkg/logx`, and `pkg/forge/client.go` imports nothing under `orchestrator/`
— the survey found the coupling to `pkg/config` confined to `state.go` and
`factory.go`. So the port is a **copy** of the request/response shapes and
the two calls the seam needs into `internal/forge/gitea`, with `pkg/forge`
not imported: importing it would carry `state.go`'s `pkg/config` edge into
the Orchestrator's closure, and the closure guard would say so. The 422
"pull request already exists" fallback the v1 client has (`gitea/client.go:245-300`)
becomes the seam's *update* half rather than a silent list-and-return.

**The family** `forge/story_pull_request`:

| Field | Value |
| --- | --- |
| Schema | `story_id` (persist), `head` (persist), `base` (persist), `title` (persist), `body` (large — an artifact reference when over the projection limit; the PR body is the Story's completion narrative and belongs in the Audit family by reference), `token` (secret slot, `forge.token`, repository scope) |
| Effect site | `orchestrator_side` — ADR 0030 §6's table lists forge operations by name |
| Checkability | The Incubator holds no forge credential: the token exists only in the vault and is revealed only in the Orchestrator process for the lifetime of one effect (D6); a resource that wants a pull request must ask. ADR 0030 §7 "credentials for a mediated resource are not placed inside an execution resource" |
| Commit point | The forge's acceptance of the create or update — HTTP 201 or 200 from the pulls endpoint. A forge operation cannot commit conditionally (§5) |
| Reconcile | `FindPullRequestByHead`: a PR for the head branch exists → the effect committed |
| Target | The repository the Story's Epic binds (`epics.repository_id`); admission check 4 refuses any other |

The branch *push* is not this family and not this item: ADR 0030 §7 says
"the mediated act is the promotion, not the local commit", and the
promotion is item 10's Story→Epic merge path. In item 5's test the head
branch is pushed by the test fixture over the clone URL the harness returns,
which is test setup, not a mediated action.

**The test** is the exit criterion, and it is one test that does all of
this in order against a live Gitea: provision an organization, repository
and Story on an ephemeral plane; create a shared secret `forge.token` at
repository scope holding the token the harness minted; accept a dispatch
with `capability_set = ["forge/story_pull_request"]`; call `Mediate`;
observe the pull request on the forge over an unauthenticated read (the
harness's admin credential, not the family's); read the `tool_calls` row and
assert `arguments` holds the projection with `token` as
`secret:<id>@<version>` and no token text anywhere in the row; call
`Mediate` again with the same attempt id and assert it replays without a
second PR; call it with a new attempt id and assert the *update* path ran
and one PR exists. The negative controls are in the verification table.

**The harness.** Three Gitea harnesses exist in the tree and the plan said
"reusing the local Gitea service and test harness only" without choosing.
This design chooses `pkg/forge/gitea`'s `ContainerManager` and
`SetupManager` (`container.go`, `setup.go`), imported **by the test only**:
they import `pkg/logx` and `pkg/mirror`, neither in the Orchestrator's
closure because test files are not in it. The image is pinned by digest, as
the golden runner's harness already does (`benchmark/target/v1target/gitea.go:32`)
and the airplane harness does not — ADR 0026's lesson. The test carries the
`integration` tag and runs in CI's `dataplane-integration` job, which has
Docker; the cold start is measured in the branch notes, and if it moves the
job past its budget the design says so rather than skipping.

Rejected: a `Forge` implementation over `pkg/forge.Client`. The interface
is v2-neutral in source but its only constructor path is
`factory.NewClient(projectDir)` → `config.GetForgeProvider()` → the state
file. Reusing the interface would invite reusing the constructor.

### D14. The `lastEffect` slot and the signal correction are removed; the stdout detector is v1's one channel until item 8

The path the inventory names (row 357) is two channels that can disagree:
the stdout `SignalDetector` infers a signal from a tool *name*, and the MCP
server keeps the last `ProcessEffect` in a single slot (`mcpserver/server.go:34-35`,
overwritten at `:425-430`) that the runner consumes to override one case
(`runner.go:199-207`: `SignalDone` becomes `SignalStoryComplete` when the
slot says so). The defect is the second channel and the override.

Removed here: `Server.lastEffect`, `Server.effectMu`,
`Server.ConsumeLastEffect`, the write at `:425-430`, and the correction
block at `runner.go:199-207`. **Retained**: `SignalDetector`,
`SignalToolInput`, `Signal` and `signals.go`, because they are the runner's
*only* signal channel once the slot is gone, and removing them would leave
v1's Claude coder — the current implementation until item 8 replaces the
runner — unable to detect any terminal signal. The plan line says "the MCP
`lastEffect` and signal-correction path"; the detector is signal
*detection*, and the inventory's own sentence names the defect as the
disagreement. The behavioural loss is the empty-diff `done` case reporting
`SignalDone` rather than `SignalStoryComplete`; it has no test asserting it
today, and item 8's adapter reports the four-axis result directly (D11) so
the case does not survive in any form.

**Reachability**, per the process rule: `ConsumeLastEffect` has one
production caller (`runner.go:202`) and no test caller; `lastEffect` is
read nowhere else. Measured by `go list`-driven call enumeration over the
explicit build-constraint set derived from the tree and the implicit axis at
`linux/amd64`, `linux/arm64` and `darwin/arm64`, `CGO_ENABLED` 0 and 1; the
command and its output are recorded in the branch notes and the survey's
claim is not carried on its own authority.

This is one of two places item 5 touches `pkg/coder/claude`; the other is
nothing. The runner's interface, `RunOptions.WorkDir` and the MCP transport
stay as the inventory disposes them (rows 355, 358).

### D15. The toolloop refactors onto an executor seam; v1 drivers keep a legacy executor; forcing becomes expressible

`Config[T]` gains one field and the loop's dispatch step calls it:

```go
type ActionExecutor interface {
    Execute(ctx context.Context, call ActionCall) ActionResult
}
```

`ActionCall` is the LLM's tool call — id, name, parameters; `ActionResult`
is content, an error flag, and an optional `ProcessEffect` — the loop's
existing consumption of `tools.ExecResult` (`toolloop.go:486-561`)
unchanged in shape. Two implementations:

- **`legacyExecutor`**, in `toolloop`, wrapping `ToolProvider.Get` and
  `tool.Exec` exactly as the loop does today, with `LogToolExecution` into
  the persistence channel — the one caller D2's guard admits. Constructed by
  `toolloop.New` when `Config.Actions` is nil, so the four v1 driver
  packages migrate with no edit, as the inventory requires.
- **`boundary.Executor`**, in `internal/boundary`, translating an
  `ActionCall` into a `Request` for the execution it was built for and a
  `Result` back into content the model reads — including `Waiting`, which
  the loop returns as a new `OutcomeAwaitingOperator{Requirements}` so the
  caller (item 6's core) can stop the turn. Item 6 is its first
  constructor; here it is constructed by the tests.

**The harness layer moves behind the seam unchanged.** The circuit breaker
(`circuit.go`), escalation, and `ActivityTracker` observe `ActionResult`
rather than `*tools.ExecResult`; their logic is not rewritten
(inventory row 324).

**Forcing** (#317's requirement, inventory row 325): `Config[T]` gains
`ForceTerminalAfter int`. When set and the iteration count reaches it, the
loop offers the model *only* the terminal tool with `ToolChoice` naming it,
and a response that still carries no terminal call is
`OutcomeTerminalRefused`, a new terminal outcome rather than a nudge. It is
expressible at the boundary in the sense the inventory meant: the caller
decides, the loop enforces, and the v1 default (zero, never force) is
unchanged.

**`pkg/tools` disposition**, which the inventory lacks (its only mention is
row 389's "blocked on v1 tools (item 14)"): the `Tool` interface, the
registry and the tool implementations are **v1's and retire with item 14**;
the v2 vocabulary is D3's family registry, and no v2 item registers a
`pkg/tools` tool. Recorded as an in-place inventory amendment (below).

Rejected: routing the legacy executor through the boundary with a
synthetic execution. It would make the v1 drivers' calls *look* mediated
while every admission check is vacuous, which is worse than an honest
bypass the guard names.

## Amendments To The Phase Plan

Two, both requiring Codex and DR acceptance with this design; applied in
the acceptance commit.

1. **The release rule for a resource held by a waiting execution is item
   7's** (D7, D8). ADR 0030 §4 says "the Phase 3 plan must ensure that
   awaiting resolution cannot renew a lease indefinitely, and must define
   when a retained, potentially billable resource is actually
   relinquished", and ADR 0032 §7 repeats it; the plan's item lines assign
   it nowhere. Item 5 introduces the wait and holds no resource; item 7
   owns leases and retention claims, and is where the rule has a consumer.
   Item 7's line gains the sentence.
2. **The agent-surface inventory gains a `pkg/tools` row** (D15): *retire,
   item 14* — the interface, registry and implementations, with the v2
   vocabulary being the boundary's family registry. The inventory is live;
   the row is marked PROPOSED in place through review and flipped in the
   acceptance commit, following item 4's precedent for amending a live
   document.

Neither settles a question an Accepted ADR already answers, and neither
adds an ADR need: the requirement-identity vocabulary, the reason codes
and the terminal columns are all mechanism under clauses the ADRs already
state.

## Implementation And Review Sequence

One branch, `v2/phase_3/execution-boundary`, after this design is
Accepted; commits reviewed in sequence as checkpoints, sized L. The order
is forced: the seam must hold the columns before the boundary can write
them, the boundary must exist before the family can run through it, and
the guards are written last because they enumerate what exists.

| # | Commit | Contents |
| --- | --- | --- |
| 1 | `schema` | Migration 000024 (D12) with its total-or-refuse guard; the store types; the tool-call verbs with all six outcomes and the refusal lifted; the execution verbs; every verb's integration test on an ephemeral plane; `OpenWork` extended (D11) |
| 2 | `registry` | The family registry with construction validation (D3); the requirement-identity vocabulary and canonical set (D4); substitution and the persisted projection (D6); the terminal-result type and validator (D11); the test-only families |
| 3 | `gates` | `Mediate`: admission, the hook, gate 2's transitions and headless path, gate 3's revalidation and execution, attempt idempotency and synchronous reconciliation (D4–D10); `DefaultAllow`; the test hooks |
| 4 | `forge` | `internal/forge` and the Gitea port (D13); the Story pull-request family; the live-Gitea integration test with its digest-pinned image; the vault read inside it |
| 5 | `toolloop` | The executor seam, the legacy executor, `boundary.Executor`, the harness layer behind the seam, forcing (D15); the four v1 driver packages building unchanged |
| 6 | `lasteffect` | The removal (D14) with the reachability measurement in the notes |
| 7 | `guards` | The two mandatoriness guards (D2) with their planted violations; the closure guards extended to admit `internal/boundary` and `internal/forge` and nothing new below them |

Each checkpoint's notes report the mutants of that step per the table
below; the branch's final notes carry all of them.

## Testing And Verification

Per [Defect-Shaped Verification](../process_build.md#defect-shaped-verification),
every guard below is proven by restoring the exact defect it claims to catch
and showing the named test fails at the intended assertion. The plane is real
and ephemeral (`planetest`); the forge is a live Gitea; the hook and the
generation source are the one place a test double is the honest choice, and
D10 says why.

| Claim | How it is proved | The mutation, and the reason the failure must name |
| --- | --- | --- |
| An effect is reachable only through the boundary (D2) | Import-graph guard over `families/*`; AST guard over `(tools.Tool).Exec` callers | Add an import of a family from `internal/orchestrator`: the guard names the package. Add a direct `Exec` call in a new file under `pkg/agent`: the guard names the file and line |
| Every attempt is opened before its effect (ADR 0030 §8) | The failing-after-commit test family: the row exists `open` when the effect runs | Reorder `Mediate` to call `Effect` before `OpenToolCall`: the family's effect observes no row |
| A denial is opened and completed together | A superseded execution's request: one row, `settled`/`denied`, one transaction | Split into two statements with a crash injected between: the row is `open` with no wait, which the test reads as the defect |
| At-most-once by attempt id (D5) | Same id twice after settle: one effect, `Recorded: false` on the second | Remove `ON CONFLICT DO NOTHING`: the second open errors on the PK, and the assertion names the duplicate effect |
| A recorded intent with no outcome does not re-execute | Kill the process between open and effect (the restart harness's kill path); retry: `Reconcile` runs, no second effect | Skip the wait-state check on retry: the effect runs twice and the family's counter says so |
| Secrets are substituted before the digest (D6) | The digest over the substituted form equals a digest computed by the test from the reference, and differs from one over the raw form | Digest the raw arguments: equality with the reference-form digest fails, and the test reads the token text out of `arguments` |
| No token text is persisted anywhere | `tool_calls.arguments`, `error_message`, `result` and the log capture are searched for the minted token | Persist the raw form: found in `arguments` |
| The secret's version is what was approved | Replace the secret between gate 1 and gate 3: `stale/secret_version_moved` | Reveal by name rather than by `(id, version)`: the new token is used and the PR is created — the test asserts it is not |
| The wait holds nothing (D7) | `pg_stat_activity` shows no session for the waiting attempt; the boundary's goroutine count is unchanged | Hold the transaction open across the wait: a session is visible |
| A second request for a waiting Story is an invariant violation | Reason code and an error-level log line | Downgrade to an ordinary denial: the log assertion fails |
| Headless blocks terminally with the requirement preserved | `blocked` row with `requirement_set`; execution `status = blocked` referencing it | Leave the row `operator_waiting` under headless: the terminal-result assertion fails |
| Requirement-set equality is checked both ways (D8) | Add a requirement between gates → `stale`; remove one → `stale` | Compare by subset: the removal case settles `succeeded` |
| Re-evaluation raises no second operator requirement | A hook that returns `RequiresOperator` again at gate 3: the action proceeds | Re-raise: the row enters `operator_waiting` a second time |
| Registration linearizes with closure (D9) | Two goroutines, a barrier between share-lock and insert, `CloseAdmission` racing: every attempt is either registered-then-settled-by-closure or refused, never registered-after-closure | Drop the share lock: an attempt registers after closure |
| Superseded authority is rejected at both gates (D10) | Supersede between gate 1 and gate 3: `denied`/`authority/superseded` at gate 3; supersede before: at gate 1 | Remove the gate 3 check: the effect runs under superseded authority |
| Supersession settles a waiting attempt `stale` with its decision intact | Supersede during `operator_waiting` after a decision is recorded | Clear the decision columns: the assertion names them |
| A fenced reference is rejected (D10) | Generation source says stale: `denied`/`authority/fenced` at gate 1; flip it between gates: at gate 3 | Check only at gate 1: the effect runs |
| Invalid axis combinations are unrepresentable (D11) | `Validate` refuses each of the eight invalid shapes; direct SQL refuses the same eight | Remove one CHECK: the direct-SQL probe for that shape succeeds |
| A positive terminal result needs a receipt | `RecordTerminalResult` with `NoResourceHeld` while an attempt is `resource_waiting`: refused | Accept unconditionally: recorded |
| Terminal executions leave `OpenWork` | The restart harness with a terminal result planted: the row is gone; with a waiting attempt: present, wait named | Skip the terminal filter: `execution_awaiting_boundary` counts it |
| `capability_set` and `headless` are immutable | Direct `UPDATE` refused by the trigger | Drop the trigger: the update succeeds |
| The migration refuses a populated table | Plant an execution before 000024 | Remove the guard: the migration fails on `NOT NULL` instead, which is the wrong reason and is recorded as such |
| The forge family creates, replays, updates (D13) | The live-Gitea test in D13 | Skip `Reconcile` on the replay: two PRs |
| `Reconcile` finds a committed effect | Kill after the forge's 201, before settle; retry: `succeeded`, one PR | Return "not found" unconditionally: `unknown` with a PR present |
| The ported client imports nothing it must not | Closure guard over `internal/forge` | Import `pkg/forge`: the guard names `pkg/config` in the closure |
| The legacy executor is the only `Exec` caller and v1 builds unchanged | D2's AST guard; `go build ./...` over the four driver packages | Add a second caller: named |
| Forcing forces | A stub LLM that never calls the terminal tool: `OutcomeTerminalRefused` at the configured iteration, with the last request's `ToolChoice` naming the terminal tool | Offer all tools on the forced turn: the request assertion fails |
| The removed path is gone and v1 still signals | `pkg/coder/claude` builds and `signals_test.go` passes; grep for `ConsumeLastEffect` is empty | — (a deletion; the reachability measurement is the evidence) |

**Positive controls.** Every negative above has a green sibling: the same
fixture through the valid path, so a red is a refusal and not a broken
fixture. **Integration.** The `integration`-tagged suites need the Docker
plane and Gitea; they are run locally and in `dataplane-integration`, and
reported in the notes — never in the reviewer's sandbox.

## Points Resolved In Review

None yet — round 1.

## Open Questions

1. **`blocked` before fencing exists.** D11 records `blocked` under
   `NoResourceHeld`. If review reads ADR 0032 §6's forced-stop path as
   requiring the full sequence even when nothing is held, the alternative
   is to defer the headless terminal record to item 7 and leave a headless
   block as a settled `blocked` *attempt* with no execution result until
   then. This design prefers recording it, because the receipt parameter
   makes the discipline visible and the vacuous case is honestly vacuous.
2. **Retaining `SignalDetector`** (D14) reads the plan line and the
   inventory row differently from a literal reading of the row's
   parenthetical. If DR would rather the whole row go now and v1's Claude
   coder be non-functional until item 8, the deletion is small and the
   reachability measurement covers it.
3. **The capability set's source in item 5.** D12 has the composition root
   pass what its caller declares. Until item 6, the only caller is the test
   and the CLI's `recover`, which accepts nothing. Whether `dataplanectl`
   should gain an operator verb that accepts a dispatch with a declared set
   — useful for Checkpoint 2's manual path — is a question of scope, not
   design; the design works either way.
4. **Gitea in `dataplane-integration`.** A ~20 s container start per test
   binary is affordable; if the suite structure means it starts once per
   package rather than once per job, the notes will show the number and
   the item may want a shared fixture. Not a design question unless the
   number is bad.

## Related Documents

- [Phase 3 scope and plan](plan_scope.md) — item 5, block B, the exit checklist.
- [ADR 0030](../../adr/0030-tool-execution-policy-hook.md) — the gates, the request, the three forms of input, recording.
- [ADR 0032](../../adr/0032-agent-execution-contract.md) — the closed list; §5 the terminal result.
- [ADR 0019](../../adr/0019-orchestrator-boundary.md) — the second amendment, authority.
- [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) — §7 late calls, §8 references.
- [Item 2 design](design_work-hierarchy.md) — `000022`, the deferrals to item 5.
- [Item 3 design](design_orchestrator-seam.md) — D2 closure, D7 the forge assignment, D9 `OpenWork`.
- [Item 4 design](design_prompt-packs.md) — the registry-validation and anti-update-trigger patterns reused.
- [Agent-surface inventory](inventory_agent-surfaces.md) — rows 313, 317, 321–325, 355–359, 389.
- [Execution-contract conformance slice](spike_execution-contract.md) — historical evidence for the gate shapes; not a template.
- [Phase 2 config and secrets design](../phase_2/design_config_secrets.md) — the ladder, the version in the key context.
