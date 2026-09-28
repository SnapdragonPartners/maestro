+++
title = "Design: The Mediated Execution Boundary (Item 5)"
edit_date = "2026-09-27"
status = "live"
type = "design"
summary = "Mini-plan for Phase 3 item 5: one Orchestrator-owned boundary package through which every agent-initiated action reaches its effect, with mandatoriness demonstrated by an exact-set guard on the one remaining direct call site rather than asserted; a code-resident action-family registry on ADR 0028's payload-type pattern that declares, per family, the argument schema with its safe projection and secret slots, the effect site, the commit point, the mediation-checkability answer and an attempt-specific reconciliation probe; ADR 0030's three gates in order — deterministic admission against the seam, a default-allow policy hook that may not infer or write, a logical operator wait that holds no transaction, and revalidation immediately before the effect — with the requirement-identity vocabulary item 2 left opaque defined here; attempt identity as the tool-call id so at-most-once is a row property and a recorded intent with no outcome resolves unknown; substitution of every secret by a version-pinned reference before the digest, with the raw value held in a redacting wrapper and revealed only inside the family's effect function; attempts registering against the execution before admission completes so admission closure linearizes on the execution row, with the generation-level key left to item 7; rejection of superseded authority at both gates from the execution row under lock, and of fenced references through a generation predicate whose real source is item 7's; the four-axis terminal result as a validated Go type and as CHECK constraints that make invalid combinations unrepresentable, recorded through a verb that carries fence-receipt discipline from the first day and reads drainage from per-attempt evidence persisted independently of the outcome; migration 000024 giving executions their immutable capability set and terminal columns, tool_calls a reason code and the durable operator decision with its consumption, and repositories the forge-binding child family they have deferred since Phase 2, with the seam's outcome refusal lifted and a version-atomic secret reveal added; a v2-neutral forge seam with the Gitea API client ported by copy, and the first secret-bearing family — the Story pull request — proven against a live, digest-pinned Gitea in the integration job; the MCP lastEffect slot and its signal correction removed as the second of two channels that could disagree, leaving the stdout detector as v1's one channel until item 8; and the toolloop refactored onto an executor seam declared in a neutral leaf package so neither side imports the other, its harness layer moved behind it unchanged, with terminal-tool forcing made expressible. Carries two plan amendments — the release rule for a resource held by a waiting execution is assigned to item 7, and the agent-surface inventory gains the pkg/tools row it lacks — and adds no ADR need."
+++

# Design: The Mediated Execution Boundary (Item 5)

Mini-plan for **Phase 3 item 5** (`execution-boundary`), the first item of
block B and the one every later item is built against: *"ADR 0030's three
gates and ADR 0032's binding boundary items … The toolloop is refactored
behind it, and the MCP `lastEffect` and signal-correction path is removed …
Also carries the vault's first live reader."*

Status: **live** — Accepted by Codex and DR, 2026-09-26. Codex approved the
design at `94a36172` (round 4; nine P1s in round 1, all confirmed against the
tree and all resolved — see *Points Resolved In Review*) and the acceptance
commit at `4825ab76` (round 5); flipped in that commit, following items 3
and 4. The post-push proofread (PR #373, 2026-09-27) is recorded below as
well. Its two plan amendments are Accepted with
it and applied to `plan_scope.md` and the inventory in that commit. DR's
decisions on the open questions are recorded under *Open Questions*.

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
`nilcheck`, `secret`, `internal/forge` (D13), a new leaf package
`internal/action` (below), and nothing under `pkg/` but `pkg/logx`. It does
**not** import `pkg/tools` or `pkg/agent/toolloop`: Go imports packages, not
type declarations, and `pkg/tools/constants.go:3` imports `pkg/config` while
the toolloop's dependency closure reaches `pkg/persistence` (`go list -deps`),
either of which would fail the Orchestrator's closure guard (review round 1).
Its one entry point is

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

**The shared vocabulary lives in a leaf.** `internal/action` holds
`action.Call`, `action.Result` and the `action.Executor` interface, importing only the
standard library and `uuid`. The toolloop imports it to *call* an executor
and the boundary imports it to *be* one; neither imports the other. Being
under `internal/` it is importable from anywhere in this module and from
nowhere outside it.

**What the toolloop keeps.** `toolloop.New`, `Run`, `Config[T]` and
`Outcome[T]` are retained as the inventory says (row 321); the loop's
dispatch step — `tool.Exec(toolCtx, toolCall.Parameters)` at
`pkg/agent/toolloop/toolloop.go:486` — is replaced by a call on
`Config.Actions action.Executor` (D15). The loop no longer knows what a tool
*is*; it knows what an action call and result look like. The legacy adapter
that still knows lives in the toolloop, outside the boundary's closure.

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
   boundary.** The family *types* — schema, classification, the `Effect`
   signature — live in the leaf `internal/boundary/family`, which every
   family imports; the families themselves live in
   `internal/boundary/families/<name>`; and the closed set is assembled in
   `internal/boundary` itself, which is the one place that imports them.
   There is no separate registry package, because a package that imported
   every family to assemble the set would be a second production importer
   and the guard would have to admit it (PR #373 review, second pass). The
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

On ADR 0028's payload-type-registry pattern, `internal/boundary` assembles
the closed set of action families from `internal/boundary/families/*` over
the types in `internal/boundary/family` (D2). A family is a value, not a
plugin:

| Field | Consumed by | Source |
| --- | --- | --- |
| `Kind`, `Verb` — an Orchestrator-owned identity, "not the caller's tool name" | Gate 1 admission; the record's `tool_name` as `<kind>/<verb>` | ADR 0030 §3 request table |
| `Schema` — the argument schema: required fields, types, and per field one of *persist*, *digest-only*, *secret slot*, *large (by reference)*, or *keyed commitment* (D6; declared, not implemented in item 5) | Substitution (D6), the persisted projection, policy's readable fields | §3 "declared by the code-resident action schema"; Consequences "every action family needs a declared safe projection before it can be recorded at all" |
| `EffectSite` — `orchestrator_side`, `in_resource`, `external` | Classification and what "policed per action" means for the family | §6's table |
| `Checkability` — one sentence: what prevents the execution resource from performing this directly | Reviewed, and rendered in the family's documentation; a family with an empty answer fails registry construction | §7 "a family with no answer is mediated in documentation only" |
| `CommitPoint` — the instant after which the effect is no longer the Orchestrator's to withhold | Gate 3's reconciliation and, from item 9, drain | §5 "every action family MUST declare its commit point" |
| `ConditionalCommit bool` — whether the effect site accepts a generation predicate | Item 9's drain disposition | §5's second disposition |
| `Effect` — the function that performs the action, receiving the revealed secrets and the resolved target; on failure it reports one of three things — the declared commit point was not reached, it was passed, or it cannot tell | Gate 3 only; the drain disposition (D11) | D1; ADR 0032 §6 |
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
hook (ADR 0030 §3): in the registration transaction (D8's T1), under the
execution row's share lock,

1. the principal instance is live and belongs to the execution;
2. the execution's authority is `current` and admission is open
   (`executions.authority_state`, `admission_closed_at`);
3. the family is contained in the execution's resolved capability set
   (D12); and
4. the intended target is one this execution may name — for item 5's only
   family, the repository the Story's Epic binds.

Each failure is a **denial with a reason code** (D12), opened and completed
in one statement — `RecordDeniedToolCall`, an insert of a row already
`settled`/`denied`, which is *not* registration and is therefore permitted
after admission has closed (D9): a request refused because its execution is
superseded still leaves the record ADR 0030 §8 requires (review round 1).
Check 2 is binding item 9's rejection at this gate; D10 says why it is also
gate 3's.

Before the four checks, one more that is Story-scoped rather than
execution-scoped (ADR 0030 §4): **no attempt of this Story is
`operator_waiting`**. The Story's waiting state is *derived* from its
attempts, not stored — `stories` carries identity, lineage, title and the
governing artifact and nothing else (`store/work.go:45-55`), and adding a
column would be a second copy of a fact the attempt row already holds. The
check is a query on `tool_calls` for the Story **under the Story row's
exclusive lock (`FOR UPDATE`), taken before the check** and held through
the wait entry when gate 1 blocks — the guard and the entry are one
single-winner operation. A share lock upgraded to exclusive by two
concurrent admissions is a PostgreSQL deadlock, not a race one of them
wins (PR #373 review, second pass), so no share lock is taken. Release is automatic and follows the row: denial and supersession settle
it, and approval releases it only when gate 3 **consumes** the decision (D7
— an approved row stays `operator_waiting` until the caller re-presents
it), so no second Story action is admitted before the approved one has
started. The derived state needs no separate release.

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
*is* `tool_calls.tool_call_id`. **Who mints it.** The LLM's tool-call id is
a provider string (`llm.ToolCall.ID`, `pkg/agent/llm/api.go:81` — values
like `call_1`), not an identity the plane can key on; so `action.Call`
carries `AttemptID`, minted once per logical call by the caller of the
executor — `boundary.Executor` on first presentation for the in-process
path, item 8's adapter for the wire path, where transport retries are the
reason the id exists — and the provider's string is kept as a *persist*
field of the projection (`caller_ref`) for correlation with the LLM turn,
never as the key (PR #373 review, second pass).

**The id is bound to its logical action.** `OpenToolCall` records the
family identity and the substituted-input digest on the row, and a
re-presentation whose family or digest differs from the row's is a
**correlation mismatch**: refused, not recorded as a new attempt (the id is
taken) and not replayed, and logged as an invariant violation — a same-id,
different-arguments request is a caller defect, and returning the old
result for it would be a replay of an action nobody asked for (ADR 0032 §6;
the spike's `boundary/correlation-is-bound-to-its-logical-action`).

**The driver holds a claim.** The row carries `claimed_by`, the
Orchestrator instance id `Start` mints for its process — set at open,
**transferred to the consuming instance in D7's consumption statement**
(an approved attempt survives a restart in `operator_waiting` claimed by
the instance that opened it, and the instance that later consumes the
approval must be the one a duplicate sees as live — review round 7), taken
conditionally by whichever instance reconciles a foreign-claimed `open` row
(`UPDATE … SET claimed_by = me WHERE claimed_by = <foreign>`, so exactly one
reconciler proceeds), and cleared at settle. A duplicate presentation that finds an `open` row
claimed by *this* instance is **in progress** — returned as such, not
reconciled — because the original caller is still driving it and would be
unable to settle its later success behind a duplicate's `unknown`. A row
claimed by another instance belongs to a process that is gone: one
Orchestrator runs per plane (ADR 0027's single-writer rule; item 9's #265
makes restart single-owner), so a foreign claim is an interrupted attempt
and reconciliation is correct. No heartbeat or lease refresh exists —
those are demoted mechanisms — and the rule needs none under the
one-process assumption, which is stated here so item 9 revisits it if the
assumption changes.

The seam's insert is `ON CONFLICT (tool_call_id) DO NOTHING` followed by a
read, so:

- a transport retry with the same id finds the row and is classified by
  its state, re-entering no gate it has passed:

  | Row | Classification | What happens |
  | --- | --- | --- |
  | `settled` | replay | the recorded result is returned; no effect |
  | `operator_waiting`, no decision | waiting | *waiting* is returned |
  | `operator_waiting`, decision `approve_once`, unconsumed | approved, not started | D7's consumption, then gate 3 |
  | `resource_waiting` | waiting | *waiting* is returned (item 7's producer) |
  | `open`, claimed by this instance | in progress | *in progress* is returned; the original caller settles it |
  | `open`, claimed by another instance | attempted, outcome unknown | the family's `Reconcile`, never the effect (ADR 0030 §3 "an attempt with a recorded intent and no outcome does not re-execute") |

  The `open` row covers both the allow path interrupted between open and
  effect and the approved path interrupted after consumption; both are
  attempts whose effect may have landed, and both go to reconciliation;
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
| **Raw** — the plaintext | For a secret slot the caller supplies **nothing**: the slot is declared by the schema and filled by the boundary from the vault, so the raw form exists only as the `secret.Value` gate 3 reveals (the redacting wrapper Phase 2 built, `internal/dataplane/secret/value.go`, which has no JSON form and needs none). For a non-secret field the raw form is what the caller supplied, in `Request.Arguments` | The effect call, for a secret; the call stack of `Mediate`, for the rest; never digested, never logged, never persisted |
| **Substituted** — every secret slot replaced by `secret:<secret_id>@<version>` | Built by the family's schema; canonical JSON | Digested; presented to the hook; compared at gate 3 |
| **Persisted projection** — the schema's *persist* fields, plus `digest`, plus object references for *large* fields | `tool_calls.arguments` | The record |

**Where the secret comes from.** A secret slot is declared with a *secret
name* and the *scope* it resolves at; the family's slot for the forge token
is `forge.token` at repository scope. The request shape an agent (or the
`dispatch` verb's operator path) can produce therefore **omits the slot**,
and a request that supplies a value for a secret slot is refused at
admission — an agent handing the boundary a credential is exactly what
ADR 0030 §7 forbids the resource from holding (PR #373 review). At gate 1 the boundary calls
`ResolveSecret(org, repository, actingUser, "forge.token")` — the six-step
ownership ladder Phase 2 built (`store/secrets.go:135-146`) — and records the
resolved `(secret_id, version)` as the substituted reference. Resolution is
a read of metadata; the plaintext is not yet in hand.

**Where it is revealed.** Immediately before the effect, inside gate 3,
`RevealSecretAtVersion(org, secret_id, actingUser, version)` returns a
`secret.Value`; the family's `Effect` receives it and hands it to the
client. The value's lifetime is the effect call. The verb is new: the
existing `RevealSecret` (`store/postgres/secrets.go:230-254`) decrypts
whichever row its read finds and takes no expected version, and a separate
metadata check before it would race with `ReplaceSecret`, which the
execution lock does not serialize (review round 1). The new verb's single
read is conditioned on `version = $expected`, so the row it decrypts is the
row it checked; a moved version is a refusal, and the attempt settles
`stale` with `reason_code = stale/secret_version_moved` — the reference named
a revision, and that revision is what was approved (§3 "a substituted
reference names a revision, not a name"; §4 "what the approval binds to …
the arguments").

**Why the substituted form carries the version.** Rotation. A reference by
name would make a later reveal fetch a token nobody approved; Phase 2 put
`secrets.version` into the key-derivation context precisely so that a
version is an immutable identity (`design_config_secrets.md:176`).

**Keyed commitments** for sensitive low-entropy non-secret values are a
**fifth** field classification beside D3's four (persist, digest-only,
secret slot, large) and are *not implemented* here: no item 5 family has such a field. The classification
exists so a family that needs one cannot omit it silently.

Rejected: revealing at gate 1 and carrying the plaintext through the wait.
A blocked action can wait for hours; §4 says the wait "must not hold a
database transaction or a transport connection open", and holding a
revealed token in process memory across it is the same mistake in a
different resource.

### D7. Gate 2 blocks logically, and headless is declared at dispatch

When gate 1 returns `RequiresOperator`, the boundary:

1. writes the requirement set and digest onto the open row and transitions
   it `open → operator_waiting` in one statement, under the Story row's
   exclusive lock (ADR 0030 §8 "entering or leaving a wait is a durable
   transition on that record"; D4 for the lock);
2. returns `Result{Waiting: true, Requirements: …}` to the caller — the
   call returns; the *action* does not end. No transaction, connection or
   goroutine is held for the wait.

**While waiting**, a second agent-initiated request for the same Story is
refused by D4's Story-scoped check with `reason_code =
invariant/story_awaiting_resolution` and logged at error level as an
invariant violation, not an ordinary denial (§4). Human resolution, reconciliation and — from item 7 — cleanup
and fencing use other paths and are not agent-initiated.

**Resolution** is `ResolveOperatorRequirement(org, tool_call_id, Outcome,
decidedBy)`, an Orchestrator verb (a human acts through the UI or CLI, never
through the boundary): the outcome is one of `approve_once` or `deny_once`;
the `*_for_story` grants are deferred (ADR 0030 §4 permits it) and the
`Outcome` type has no other values until a consumer adds them. On deny the
row settles `denied` with `reason_code = operator/denied`. On approve the
decision is stored on the row — durable, so a crash between decision and
effect does not lose it — and **the row stays `operator_waiting`**: an
approved attempt that has not started is a different thing from an
interrupted one, and a row moved to `open` at approval would be
indistinguishable from an attempt whose process died mid-effect, which D5
routes to reconciliation without re-entering any gate — so an approved PR
creation would settle `unknown` because no PR exists yet (review round 1).
Instead the *caller* re-presents the same attempt id, and gate 3's first act
is **consumption**: one conditional update, `operator_waiting → open` where
`operator_decision = 'approve_once' AND operator_decision_consumed_at IS
NULL`, setting the consumption time **and `claimed_by` to the consuming
instance** (D5 — after a restart the row is still claimed by the instance
that opened it, and the claim must follow the driver). It succeeds for exactly one
re-presentation; a concurrent second one finds the row `open` with the
decision consumed and is classified by D5's table as attempted — it does not
run the effect. `operator_decision_consumed_at` is a column of D12.

**Headless.** Whether a responder exists is a property of the *execution*,
read from the resolved configuration D12 persists (`headless bool`), "known
at dispatch — never an observation that nobody answered". Under headless,
the requirement is recorded on the row exactly as above and the row settles
**terminally** `blocked` in the same transaction, preserving the requirement
set (ADR 0032 §5 "a headless block is terminal for the action, not a
wait"). What follows is a **forced stop**, in ADR 0032 §6's order and not a
bare record: `CloseAdmission` on the execution first, so no further attempt
registers; then the drain check over every attempt (D11); then
`RecordTerminalResult(blocked)`, whose precondition is that admission is
already closed. Cancellation of a running runtime and fencing of a resource
domain are the steps items 6, 7 and 9 insert between closure and the
record — in item 5 no runtime runs and no domain is held, and the verb's
precondition is what keeps those steps from being skippable once they
exist (PR #373 review). Independent runnable work is unaffected because
nothing here touches any other Story.

Rejected: a wait implemented as a channel the caller blocks on. That is
v1's `SUSPEND` shape, and it is what item 3 D8's artifact-level restart
rule exists to replace.

### D8. Gate 3 revalidates everything and executes; in item 5 the resource step is a seam whose only implementation is "none required"

On the approved re-presentation (or immediately, when gate 1 allowed):

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

#### The transaction protocol: durable intent, then a committed revalidation, then the effect outside any transaction

An earlier revision put gate 3 and the effect "under the execution row's
lock". It cannot: a row lock is transaction-scoped, so holding it through
an external effect would mean the open row is not committed before the
effect (no durable intent), while committing first releases the lock before
the effect (PR #373 review, second pass). The protocol is three
transactions and an interval:

| Step | Transaction | What is durable afterwards |
| --- | --- | --- |
| Register | T1: execution `FOR SHARE`, admission open, insert the row `open` with claim, family, digest | The intent (D5, D9) |
| Revalidate | T2: execution `FOR UPDATE`; D8 steps 1–3; for an approved attempt, D7's consumption in the same statement set; record `revalidated_at` | That the attempt was current when checked, and that the decision was consumed once |
| Effect | none — secrets revealed, `Effect` runs | nothing until the family's commit point |
| Settle | T3: conditional on `finished_at IS NULL` | The outcome and drain disposition |

**The interval between T2 and the effect is not closed by a lock, and the
design does not pretend it is.** It is ADR 0030 §5's "admission-to-effect
interval", and the ADR's answer is fencing, not locking: the attempt is
*registered* (T1), so a supersession that lands in the interval finds it
and must **drain** it — wait for T3 within ADR 0029 §7's grace period, or
report `unconfirmed` — before any positive receipt (D11). `SupersedeExecution`
therefore does two things in one transaction: sets `superseded` and closes
admission, and returns the set of attempts registered-and-unsettled at that
moment, which is the caller's drain list; item 9 is the caller that drains,
and in item 5 the test is. What the lock in T2 *does* guarantee is that no
attempt passes revalidation after supersession has committed, and no
decision is consumed twice; what it does not guarantee — that no effect
lands after supersession — is the drain's to settle, exactly as §5 says.
D9's linearization claim is about T1 against closure, and stands.

Rejected: holding T2 open through the effect. Beyond the durability
problem, the forge call can take seconds and the execution lock would
serialize every attempt of the execution behind it.

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
arrives after is refused **as a registration**, and the refusal is recorded
through D4's `RecordDeniedToolCall` with `reason_code =
authority/admission_closed` — closure prevents new executable attempts and
does not suppress the record of a rejected request (review round 1). The
spike's claim
`boundary/admission-closure-linearizes-with-registration` is the same
property; here it is proved against Postgres rather than a mutex.

The `(resource, generation)` key is added by item 7 as a nullable column
pair beside `execution_id`, when a family first runs in a resource; the
registration and closure verbs gain a generation parameter then. Nothing in
item 5 has to be redesigned for that, because the registration-before-
admission ordering and the closure-first-then-settle ordering are the same
at either grain.

### D10. Superseded authority is rejected at both gates from the row, in each gate's transaction; fenced references through a predicate whose real source is item 7's

Binding item 9: *"A request carrying superseded or fenced execution
authority is rejected at every mediated boundary. The requirement is
binding. Epochs — and any other mechanism for detecting that authority —
are not."*

**Superseded.** The authority is the execution row's `authority_state`,
which item 2 defined with two values and no writer. This item adds
`SupersedeExecution(org, execution_id)` — sets `superseded` and closes
admission in one statement, satisfying `000021`'s
`executions_superseded_closes_admission_check` — and reads the state at gate
1 (D4 check 2, in T1) and gate 3 (D8 step 1, in T2), each under the row's
lock for that transaction; the interval after T2 is the drain's (D8's
protocol). Item 9 is
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
    ErrorMessage          string                 // iff failed: the diagnostic; required for a synthesized result
}
func (r TerminalResult) Validate() error
```

`Validate` is the applicability rule: an axis that does not apply must be
absent, and one that applies must be present. A result that fails it is
refused and the execution fails `non_retryable_agent` (§5 "a result
violating the applicability rule is a protocol violation … not recorded and
then reasoned about downstream") — concretely, the boundary **synthesizes**
`{Status: failed, FailureClass: non_retryable_agent}` with `error_message`
naming the violated rule and records *that* through the same verb, under the
same closure and receipt checks; the offending value is never stored. So a
protocol violation leaves a terminal execution, not one that can be retried
or requeued (PR #373 review). The same rule is a CHECK constraint on
the columns migration 000024 adds to `executions`, so a direct SQL writer
cannot store what the validator refuses — "makes invalid combinations
unrepresentable" is a property of the schema, not only of one Go path.

**Recording.** `RecordTerminalResult(org, execution_id, TerminalResult,
FenceReceipt)`: requires admission already closed (D7's forced-stop order —
the verb refuses otherwise rather than closing it as a side effect), sets
the terminal columns and `terminated_at`, at most once (a second record is a
conflict). The `FenceReceipt`
parameter exists from the first day because ADR 0032 §6 puts receipt
discipline on the *category* — any positive terminal result after admitted
actions, including `blocked` "at a gate no operator can answer" — and not
on `cancelled` alone. A receipt has two halves, because the ADR's
obligation does: **admitted actions drained**, and **the resource domain
fenced**. In item 5 the domain half is vacuous — no resource exists to hold
— but the action half is not: an Orchestrator-side attempt can be `open`
with its forge mutation unresolved (the request timed out, reconciliation
found nothing, the remote may yet commit), and a receipt that ignored it
would be issued while an action can still commit (review round 1). Settlement is
not drainage: D5 settles an inconclusive reconciliation as `unknown`, and a
timed-out forge request can be `settled`/`unknown` while the remote may
still commit (review round 2). So drainage is **persisted evidence of its
own**, independent of the outcome — `tool_calls.drain_disposition`, ADR 0032
§6's three dispositions plus the absence of one: `stopped_before_commit`,
`committed`, `in_fenced_domain`, `unresolved`. It is set at settle from what
the family can attest: `denied`, `stale` and `blocked` never reached the
effect and are `stopped_before_commit`; `succeeded` is `committed`; `failed`
follows what the family reports about its declared commit point —
`stopped_before_commit` if the failure landed before it, `committed` if
after it (the effect is known to have landed even though the family then
failed), and `unresolved` only when it cannot tell; `unknown` is
`unresolved`. Unlike the outcome, the disposition may move later, in one
direction — `unresolved` → `committed` or `stopped_before_commit` — when a
later `Reconcile` (on retry, on `Recover`, and from item 9 on a schedule)
obtains attempt-specific evidence; a mutation that can never be confirmed
either way stays `unresolved`.

The verb accepts a receipt only when **every attempt of the execution has a
resolved disposition**. An execution with an `unresolved` attempt has no
receipt and therefore no terminal result; the operator sees it open with the
attempt named, which is the truthful state and is what ADR 0030 §5 calls
`unconfirmed`. The item 5 receipt type is `Receipt{ActionsDrained: true,
Domain: DomainNoneHeld}`; item 7 adds the domain values. The alternative — record `blocked` without the parameter and
add it later — is exactly the positive-receipt-while-an-action-can-commit
defect that is an exit-checklist criterion of items 7 and 9.

**Who records what.** `completed`, `cancelled`, `timed_out` and `failed`
may be claimed by the runtime (item 6's agent core, item 8's adapter) and
are validated here; `blocked` is recorded by the boundary itself from D7's
headless path, referencing the attempt, because it is "a fact about a gate
the agent cannot see". At that point the blocked attempt is
`stopped_before_commit` by construction, and D4's Story-scoped check
guarantees no other attempt of the Story is waiting; earlier attempts of
the execution must still have resolved dispositions, and the verb checks
all of them.

**`OpenWork` is extended** (item 3 D9's obligation): an execution with a
terminal result is not open work and leaves the projection; one with an
attempt in `operator_waiting` or `resource_waiting` is open and the
projection's row says which wait. Item 6 proves the extension against a
running agent; here the proof is the restart harness re-run with a
terminal result and a waiting attempt planted.

### D12. Migration 000024 and the seam verbs

One migration, three tables touched — `tool_calls`, `executions`, and the
new `repository_forge_bindings` — every column with an ADR clause and a
consumer in this item:

| Table | Change | Clause | Consumer |
| --- | --- | --- | --- |
| `tool_calls` | `claimed_by uuid` (the Orchestrator instance, present iff `state <> 'settled'`), `family text NOT NULL` (the action identity, beside the legacy `tool_name`), `arguments_digest text NOT NULL` (the substituted-input digest, `^[0-9a-f]{64}$`) | D5's correlation binding and claim; ADR 0032 §6 | D5 |
| `tool_calls` | `drain_disposition text`; CHECK: `IN ('stopped_before_commit','committed','in_fenced_domain','unresolved')`, present iff `state = 'settled'`; a trigger permits change only from `unresolved` | ADR 0032 §6's per-attempt disposition; ADR 0030 §5 "otherwise `Fence()` returns `unconfirmed`" | D11 |
| `tool_calls` | `reason_code text`; CHECK: **required** for `denied`, `stale` and `unknown`; **optional** for `failed` (which keeps `error_message` as the human text); **forbidden** for `succeeded` and `blocked` (`blocked` carries the requirement set) and while unsettled | ADR 0030 §8 "with the reason code"; `000022:159-162`'s explicit deferral | D4, D5, D8, D10 |
| `tool_calls` | `operator_decision text`, `operator_decided_by uuid`, `operator_decided_at timestamptz`, `operator_decision_consumed_at timestamptz`; CHECK: the first three all or none; decision in `('approve_once','deny_once')`; consumed only if decided and only for `approve_once` | ADR 0030 §4 "the action-scoped decision is still durable, for crash recovery"; D7's approved-not-started distinction | D5, D7 |
| `repository_forge_bindings` (new) | `repository_id`, `organization_id`, `provider text`, `base_url text`, `owner text`, `repo text`, `created_at`; PK `(repository_id, provider)`; FK to `repositories (repository_id, organization_id)`; `provider IN ('gitea')` until a second provider has a consumer | ADR 0022's logical repository "may carry **several** forge bindings … bindings arrive in Phase 3 with the forge rework" (`000002:35-38`) — a child family, not columns on the row, so a second binding is representable without a schema change (PR #373 review); the record has none today (`store/provisioning.go:80-95`) | D13 |
| `executions` | `capability_set jsonb NOT NULL` — a JSON array of family identities, unique, sorted; `headless boolean NOT NULL`; both immutable after insert by an anti-update trigger on item 4's pattern | ADR 0032 item 10, the resolved-configuration lifetime: "what was resolved for an execution must not silently change"; ADR 0030 §4 "headless is a declared execution configuration, known at dispatch" | D4 check 3; D7 |
| `executions` | `status text`, `completion_disposition text`, `cancellation_reason text`, `failure_class text`, `blocked_tool_call_id uuid`, `error_message text` (present only when `status = 'failed'`), `terminated_at timestamptz`; the applicability rule as CHECKs; `terminated_at IS NOT NULL` iff `status IS NOT NULL`; `blocked_tool_call_id` is a **composite FK** `(blocked_tool_call_id, execution_id, organization_id) → tool_calls (tool_call_id, execution_id, organization_id)` over a new unique key on `tool_calls`, so the reference cannot name another execution's attempt, plus a trigger requiring the referenced row to be `settled` with `outcome = 'blocked'` (PR #373 review) | ADR 0032 item 7; §5 "`blocked` … references the pending action and the structured requirement set" | D11 |

`capability_set` is supplied to `AcceptDispatch` by the Orchestrator; in
item 5 the composition root passes the set its caller declares, and item 6
derives it from the role and pack. **The seam validates it at dispatch**
against the closed family set — handed to the seam at composition as
`plane.Caller.Actions`, on the pattern of `Caller.Keys` and
`Caller.Prompts` (`plane/compose.go:105-116`) — refusing an identity the
registry does not know, and canonicalizes it (sorted, de-duplicated) before
persistence, so the stored set is the invariant D12 states and an unknown
family cannot be stored now to become live under a later registry (PR #373
review, second pass). The immutability trigger then keeps it so. The caller in item 5 is a new operator
verb, `dataplanectl -org <slug> -user <handle> -story <id> -capabilities
<family,...> dispatch`, which creates and accepts a dispatch for a Story
with a declared set and prints the execution — Checkpoint 2's manual path,
and the way an operator exercises the boundary before an agent core exists
(DR, 2026-09-26, open question 3). An execution accepted before 000024 does
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
- tool calls, continued: `RecordDeniedToolCall` (D4 — settled on insert,
  no registration), `ConsumeOperatorDecision` (D7) and
  `ResolveDrainDisposition` (D11 — `unresolved` to a resolved value, with
  the evidence's attempt id);
- executions: `SetExecutionConfiguration` (inside `AcceptDispatch`),
  `CloseAdmission`, `SupersedeExecution`, `RecordTerminalResult`;
- secrets: `RevealSecretAtVersion` (D6);
- repositories: `BindRepositoryForge` (idempotent per `(repository,
  provider)`; a differing binding for the same provider is a conflict, on
  `ProvisionRepository`'s pattern) and `Repository.ForgeBindings
  []ForgeBinding` on the read side, in provider order;
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
    FindPullRequest(ctx context.Context, token secret.Value, head, base string) (*PullRequest, error)
}
```

Two methods, both of which the family needs — the second is its
`Reconcile` probe's lookup and the update half's target, matched on **head
and base** together: the v1 client lists by head alone and its 422 fallback
takes the first result (`gitea/client.go:134-164, 278-284`), which with two
PRs from one head against different bases would update or reconcile the
wrong one (PR #373 review). More than one match is refused as ambiguous,
never resolved by position. The attempt trailer the probe then checks is in
the returned `PullRequest.Body`. The token is a parameter, never a field: a `Forge` value
holds no credential, so there is nothing to leak from it and nothing that
outlives the effect (D6). The repository binding (`provider`, `base URL`,
`owner`, `repo`) comes from the repository's forge bindings — D12's child
family, written by `BindRepositoryForge` — "which is what the inventory's
own row says `forge_state.json` dies into" (item 3 D7). **Selection** in
item 5 is by provider: the family declares `gitea`, and the binding it uses
is the repository's `gitea` binding, refused at admission if absent. A rule
for choosing among several providers (the local forge versus GitHub after
sync) is item 10's, where the promotion path has a consumer for it; item 5
adds no such rule and represents a second binding without needing one.
The record has no bindings today; item 3 provisioned identity only and
migration 000002 deferred them to "the forge rework", which this is.

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
| Reconcile | Evidence must be **attempt-specific**: the family writes the attempt id into the PR body as a trailer, `Maestro-Attempt: <tool_call_id>`, on both create and update, and `Reconcile` finds the PR by head and base and settles `succeeded` only if the trailer names *this* attempt. Head existence alone is inconclusive — on the update path a PR already exists before the attempt starts, and on the create path one may exist from an earlier attempt (review round 1) |
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

`Config[T]` gains one field, `Actions action.Executor`, and the loop's
dispatch step calls it. The interface and its two types live in
`internal/action` (D1), a leaf neither side owns:

```go
package action
type Executor interface {
    Execute(ctx context.Context, call Call) Result
}
```

`Call` is the LLM's tool call — id, name, parameters; `Result`
is content, an error flag, and an optional process effect — the loop's
existing consumption of `tools.ExecResult` (`toolloop.go:486-561`)
unchanged in shape, with the signal vocabulary copied into the leaf rather
than imported from `pkg/tools`. Two implementations:

- **`legacyExecutor`**, in `toolloop`, wrapping `ToolProvider.Get` and
  `tool.Exec` exactly as the loop does today, with `LogToolExecution` into
  the persistence channel — the one caller D2's guard admits. Constructed in
  `Run` when `Config.Actions` is nil — the point where the loop builds its
  local provider today (`toolloop.go:212-217`); `New` receives only the
  client and logger and never sees a `Config` — so the four v1 driver
  packages migrate with no edit, as the inventory requires.
- **`boundary.Executor`**, in `internal/boundary`, translating an
  `action.Call` into a `Request` for the execution it was built for and a
  `Result` back into content the model reads — including `Waiting`, which
  the loop returns as a new `OutcomeAwaitingOperator{Requirements}` so the
  caller (item 6's core) can stop the turn. Item 6 is its first
  constructor; here it is constructed by the tests.

**The harness layer moves behind the seam unchanged.** The circuit breaker
(`circuit.go`), escalation, and `ActivityTracker` observe `action.Result`
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
| 1 | `schema` | Migration 000024 (D12) with its total-or-refuse guard; the `dispatch` operator verb; the store types; the tool-call verbs with all six outcomes and the refusal lifted, `RecordDeniedToolCall` and `ConsumeOperatorDecision`; the execution verbs; `RevealSecretAtVersion`; `BindRepositoryForge`; every verb's integration test on an ephemeral plane; `OpenWork` extended (D11) |
| 2 | `registry` | The leaf `internal/action` (D1); the family registry with construction validation (D3); the requirement-identity vocabulary and canonical set (D4); substitution and the persisted projection (D6); the terminal-result type and validator (D11); the test-only families |
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
| At-most-once by attempt id (D5) | Same id twice after settle: one effect, the replay returns the recorded result. The effect is observed as **mutation requests at the forge**, counted by a recording `http.RoundTripper` the test installs on the family's client (POST and PATCH to the pulls endpoint), not as PRs — the forge upserts, so two creates for one head leave one PR (review round 2) | Skip the settled-row lookup on retry so the request runs the gates again: the mutation-request count reads 2, which is the assertion that fails |
| Concurrent re-presentations of one approved attempt run one effect (D7) | Two goroutines re-present the same approved id through a barrier: mutation-request count 1, one consumption timestamp | Make consumption unconditional — remove the `WHERE` predicates on both `state` and `operator_decision_consumed_at`, leaving only the id — so both re-presentations transition and proceed: count reads 2 |
| An approved attempt is not misread as interrupted (D7) | Approve, kill before re-presentation, restart, re-present: the effect runs once and the row settles `succeeded` | Move the row to `open` at approval: the re-presentation reconciles, finds nothing, settles `unknown`, and the assertion names the outcome |
| A recorded intent with no outcome does not re-execute | Kill the process between open and effect (the restart harness's kill path), so **zero** effects have run; retry in a fresh process: D5's `open` branch calls `Reconcile`, which finds nothing, and the row settles `unknown`/`unresolved` with the mutation-request count still **0** | Replace D5's `open` classification branch with the effect (execute instead of reconcile): the count reads 1, which is the assertion that fails (review round 3 — an earlier version of this row asserted "twice" against a fixture that can only produce one) |
| Secrets are substituted before the digest (D6) | The digest over the substituted form equals a digest computed by the test from the reference, and differs from one over the raw form | Digest the raw arguments: equality with the reference-form digest fails, and the test reads the token text out of `arguments` |
| No token text is persisted anywhere | `tool_calls.arguments`, `error_message`, `result` and the log capture are searched for the minted token | Persist the raw form: found in `arguments` |
| The secret's version is what was approved | Replace the secret between gate 1 and gate 3: `stale/secret_version_moved` | Reveal by name rather than by `(id, version)`: the new token is used and the PR is created — the test asserts it is not |
| The wait holds nothing (D7) | `pg_stat_activity` shows no session for the waiting attempt; the boundary's goroutine count is unchanged | Hold the transaction open across the wait: a session is visible |
| A second request for a waiting Story is an invariant violation | Reason code and an error-level log line | Downgrade to an ordinary denial: the log assertion fails |
| Headless blocks terminally with the requirement preserved | `blocked` row with `requirement_set`; execution `status = blocked` referencing it | Leave the row `operator_waiting` under headless: the terminal-result assertion fails |
| Requirement-set equality is checked both ways (D8) | Add a requirement between gates → `stale`; remove one → `stale` | Compare by subset: the removal case settles `succeeded` |
| Re-evaluation raises no second operator requirement | A hook that returns `RequiresOperator` again at gate 3: the action proceeds | Re-raise: the row enters `operator_waiting` a second time |
| A same-id re-presentation with a different family or digest is refused, not replayed (D5) | Re-present a settled id with changed arguments: `ErrCorrelationMismatch`, no new row, an error-level log | Skip the digest comparison: the old result is returned for the new arguments |
| The claim follows the consumer across a restart (D5, D7) | Instance A opens, blocks, exits; approve; instance B re-presents and consumes; a concurrent duplicate on B during B's effect returns *in progress*; B settles `succeeded` | Consume without transferring the claim: the duplicate sees A's claim as foreign, reconciles, settles `unknown`, and B's settle reports `Recorded: false` |
| Exactly one process reconciles a foreign-claimed row (D5) | Two reconcilers race on a row claimed by a dead instance: one takes the claim, one returns *in progress* | Reconcile without taking the claim: both reconcile, and the second settle's `Recorded: false` is the assertion |
| A duplicate does not reconcile an attempt its creator is still driving (D5) | Two goroutines, same id, barrier after the creator's T1: the duplicate returns *in progress*; the creator's effect runs once and settles `succeeded` | Treat an own-instance claim as foreign: the duplicate reconciles, settles `unknown`, and the creator's settle reports `Recorded: false` — the assertion names the outcome |
| Supersession in the effect interval is drained, not missed (D8, D10) | Supersede between T2 and the effect (barrier inside the test family): `SupersedeExecution` returns the attempt in its drain list; the effect lands; T3 settles it; the receipt is available only after | Return an empty drain list for attempts past T2: the receipt is issued while the effect is in flight, which the recording transport shows |
| `capability_set` is validated and canonical at dispatch (D12) | `AcceptDispatch` with an unknown identity: refused; with `["b","a","a"]`: stored `["a","b"]` | Skip validation: the unknown identity is stored |
| Registration linearizes with closure (D9) | Two goroutines, a barrier between share-lock and insert, `CloseAdmission` racing: every attempt is either registered-then-settled-by-closure or refused, never registered-after-closure | Drop the share lock: an attempt registers after closure |
| Superseded authority is rejected at both gates (D10) | Supersede between gate 1 and gate 3: `denied`/`authority/superseded` at gate 3; supersede before: at gate 1 | Remove the gate 3 check: the effect runs under superseded authority |
| Supersession settles a waiting attempt `stale` with its decision intact | Supersede during `operator_waiting` after a decision is recorded | Clear the decision columns: the assertion names them |
| A fenced reference is rejected (D10) | Generation source says stale: `denied`/`authority/fenced` at gate 1; flip it between gates: at gate 3 | Check only at gate 1: the effect runs |
| Invalid axis combinations are unrepresentable (D11) | `Validate` refuses each of the eight invalid shapes; direct SQL refuses the same eight | Remove one CHECK: the direct-SQL probe for that shape succeeds |
| A positive terminal result needs a receipt | `RecordTerminalResult` with `ActionsDrained` while an attempt is `resource_waiting` or `operator_waiting`: refused | Accept unconditionally: recorded |
| Terminal executions leave `OpenWork` | The restart harness with a terminal result planted: the row is gone; with a waiting attempt: present, wait named | Skip the terminal filter: `execution_awaiting_boundary` counts it |
| `capability_set` and `headless` are immutable | Direct `UPDATE` refused by the trigger | Drop the trigger: the update succeeds |
| The migration refuses a populated table | Plant an execution before 000024 | Remove the guard: the migration fails on `NOT NULL` instead, which is the wrong reason and is recorded as such |
| The forge family creates, replays, updates (D13) | The live-Gitea test in D13, with the recording transport counting mutation requests | On the interrupted-`open` retry, run the effect instead of `Reconcile`: a second mutation request is recorded (one PR either way, because the forge upserts) |
| `Reconcile` is attempt-specific | Kill after the forge's 201, before settle; retry: `succeeded`, one PR carrying this attempt's trailer. Then: an update attempt killed after intent, before the request; retry: the existing PR carries the *previous* trailer → `unknown`, not `succeeded` | Settle on head existence alone: the second case settles `succeeded` for an update that never ran |
| A positive receipt needs drained actions, not settled ones (D11) | A forge attempt whose request times out is reconciled inconclusively and settles `unknown`/`unresolved`: `RecordTerminalResult` refused **after** settlement. Then the test plants the attempt's trailer on the forge (the late commit), `Recover` re-reconciles, the disposition moves to `committed`, and the receipt is accepted | Accept on `state = 'settled'` instead of on the disposition: recorded while the attempt is `unknown`/`unresolved`, which is the assertion that fails |
| A disposition moves only from `unresolved` | Direct `UPDATE` of a `committed` disposition refused by the trigger | Drop the trigger: the update succeeds |
| A denial after closure is still recorded (D4, D9) | Supersede, then request: a `settled`/`denied` row with `authority/superseded` exists and no registration happened | Route the denial through `OpenToolCall`: refused at closure, no row, and the audit assertion fails |
| The reveal is version-atomic (D6) | Replace the secret between gate 1's resolution and gate 3's reveal: the new verb refuses, `stale/secret_version_moved` | Reveal by id without the version predicate: the new token is used and the PR is created with it |
| The Story-scoped guard is derived and releases (D4) | While one attempt is `operator_waiting`, a second is refused; after deny/approve/supersede, admitted | Read a cached flag instead of the attempt rows: the post-release request is still refused |
| The repository binding is the only endpoint source (D13) | The family with an unbound repository: denied at admission check 4 with `target/repository_unbound` | Fall back to a default URL: the effect runs against it |
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

Round 1 (Codex, 2026-09-26). Nine P1s, every one confirmed against the tree
before the design moved: `pkg/tools/constants.go:3` imports `pkg/config` and
`go list -deps ./pkg/agent/toolloop` reaches `pkg/persistence`;
`RevealSecret` (`store/postgres/secrets.go:230-254`) takes no version;
`store.Story` (`work.go:45-55`) carries no state; `Repository`
(`provisioning.go:80-95`) and `000002:35-38` defer forge bindings.

| # | Finding | Resolution |
| --- | --- | --- |
| 1 | `NoResourceHeld` would issue a receipt while an Orchestrator-side mutation was unresolved | D11 — the receipt has two halves; the action half requires every attempt settled, and an attempt reconciliation cannot settle leaves the execution without a terminal result |
| 2 | An approved attempt moved to `open` is indistinguishable from an interrupted one, so re-presentation would reconcile instead of executing | D7 — the row stays `operator_waiting` with the decision recorded; gate 3 begins with a conditional consumption; D5's classification table; `operator_decision_consumed_at` in D12 |
| 3 | `FindPullRequestByHead` would settle an unperformed update as `succeeded` | D13 — reconciliation evidence is attempt-specific: a `Maestro-Attempt` trailer written on create and update, checked on reconcile |
| 4 | `RevealSecret` decrypts whichever row it finds; a separate version check races `ReplaceSecret` | D6, D12 — `RevealSecretAtVersion`, one read conditioned on the approved version |
| 5 | Importing `pkg/tools` for its types reaches `pkg/config`; implementing toolloop-owned types from the boundary reaches `pkg/persistence` | D1, D15 — the vocabulary moves to the leaf `internal/action`; the legacy adapter stays in the toolloop, outside the boundary's closure |
| 6 | The Story-level `awaiting_resolution` transition does not exist | D4, D7 — waiting is derived from attempts under the Story row lock; entry, release and the guard all read the same rows |
| 7 | The repository record has no forge binding to supply an endpoint | D12, D13 — four binding columns on `repositories`, `BindRepositoryForge`, the family reads them and admission refuses an unbound target |
| 8 | Closure refusing `OpenToolCall` suppresses the denied audit row | D4, D9 — registration and denied-audit insertion are separate verbs; closure refuses only registration |
| 9 | Two mutants could not produce their named failure | Testing table — replaced with mutants that bypass replay suppression and reconciliation, with concurrent and interrupted attempts, failing at the effect count |

Round 2 (Codex, 2026-09-26). Findings 1 and 9 reaffirmed as still open; both
confirmed — D5 does settle an inconclusive reconciliation as `unknown`, and
D13's client does upsert on the same head.

| # | Finding | Resolution |
| --- | --- | --- |
| 1 (cont.) | "Every attempt settled" accepts the receipt for a `settled`/`unknown` attempt whose mutation may still commit | D11, D12 — drainage is its own persisted evidence, `tool_calls.drain_disposition`, set from what the family attests at settle and movable only from `unresolved`; the receipt requires every disposition resolved; the test refuses the receipt *after* the `unknown` settlement and accepts it once a late commit is reconciled |
| 9 (cont.) | Counting PRs cannot see a replayed create (the forge upserts); dropping only the `consumed_at` predicate leaves the `state` predicate protecting the transition | Testing table — the effect is counted as mutation requests through a recording transport on the family's client; the concurrency mutant removes both predicates |

Round 3 (Codex, 2026-09-26). Finding 9 partially open: the pre-effect-kill
row asserted a second effect against a fixture that has run none.

| # | Finding | Resolution |
| --- | --- | --- |
| 9 (cont.) | The interrupted-attempt row's fixture yields zero effects, so "runs twice" is unreachable; removing a wait-state check does not bypass the `open` branch | Testing table — the row asserts count 0 and the mutant replaces D5's `open` branch with the effect, count 1 |

PR #373 proofread (Copilot, 2026-09-27), after Codex's approval of the
acceptance commit. Ten threads, all accepted; two were contradictions the
round-2 edits introduced.

| # | Finding | Resolution |
| --- | --- | --- |
| 1 | D4 said approval releases the Story wait; D7 keeps the row waiting until consumption | D4 — release follows consumption, denial or supersession |
| 2 | A secret slot cannot be supplied by the caller: `secret.Value` has no JSON form and the agent holds no credential | D6 — the caller omits the slot, the boundary fills it from the vault, a supplied value is refused at admission; the raw form exists only as the revealed value inside the effect |
| 3 | Headless recorded `blocked` without the forced-stop order | D7, D11 — `CloseAdmission` first, then drain, then the record; the verb requires closure as a precondition so later items' cancellation and fencing steps cannot be skipped |
| 4 | An invalid runtime result had no terminal fallback | D11 — the boundary synthesizes `failed/non_retryable_agent` naming the violation and records it under the same checks |
| 5 | Inline binding columns contradict 000002's several-bindings model | D12, D13 — `repository_forge_bindings` child family keyed `(repository, provider)`; item 5 selects by provider; the cross-provider rule is item 10's |
| 6 | Lookup by head alone can update or reconcile the wrong PR | D13 — `FindPullRequest(head, base)`; ambiguity refused |
| 7 | A failure known to be past the commit point was classified `unresolved` | D11 — three-way report; past the commit point is `committed` |
| 8 | The `reason_code` rule was self-contradictory for `failed` | D12 — required / optional / forbidden stated per outcome |
| 9 | `blocked_tool_call_id` had no integrity tie to a blocked attempt of the same execution | D12 — composite FK plus a trigger on the referenced row's outcome |
| 10 | The Status line's round and commit disagreed with the PR | Status line — design at `94a36172` (round 4), acceptance at `4825ab76` (round 5) |

PR #373 proofread, second pass (Copilot, 2026-09-27, on `9a0550f4`). Ten
threads, all accepted; four are consequences of the first pass's fixes.

| # | Finding | Resolution |
| --- | --- | --- |
| 1 | Share-then-exclusive on the Story row deadlocks two concurrent admissions | D4 — the Story lock is taken `FOR UPDATE` before the check; guard and wait entry are one single-winner operation |
| 2 | At-most-once was not bound to the action identity and digest; `AttemptID` was a UUID while the LLM's tool-call id is a string | D5, D12 — `family` and `arguments_digest` on the row, correlation mismatch refused and logged; the executor's caller mints the UUIDv7, the provider string is a projection field |
| 3 | A concurrent duplicate could reconcile an attempt its creator was still driving | D5, D12 — `claimed_by` (the Orchestrator instance id); an own-instance claim is *in progress*; a foreign claim reconciles, under the stated one-process-per-plane assumption |
| 4 | Gate 3 "under the execution lock" cannot give both durable intent and revalidation-to-effect linearization | D8 — the three-transaction protocol; the interval is the drain's, per ADR 0030 §5; `SupersedeExecution` returns the drain list; D4, D10 reworded |
| 5 | `capability_set` was not validated against the registry or canonicalized | D12 — `plane.Caller.Actions`; the seam validates and canonicalizes at dispatch |
| 6 | `toolloop.New` cannot see `Config.Actions` | D15 — the nil fallback is built in `Run`, where the local provider is built today |
| 7 | A registry package importing every family is a second production importer of `families/*` | D2, D3 — types in the leaf `internal/boundary/family`; assembly in `internal/boundary`; no registry package |
| 8 | Keyed commitments called a "fourth kind" beside four existing classifications | D3, D6 — a fifth classification, declared and not implemented |
| 9 | The synthesized failure's `error_message` had no destination on `executions` | D11, D12 — `executions.error_message`, present only for `failed`; `TerminalResult.ErrorMessage` |
| 10 | `ActionCall`/`ActionResult` versus `Call`/`Result` | D1 — `action.Call`, `action.Result`, `action.Executor` throughout |

Round 7 (Codex, 2026-09-27). One P1 on the second pass's claim rule.

| # | Finding | Resolution |
| --- | --- | --- |
| 1 | The claim was set only at open, so an approval consumed after a restart left the row claimed by the dead instance and a duplicate would reconcile the live effect | D5, D7 — consumption transfers the claim in the same statement; reconciliation of a foreign-claimed row takes the claim conditionally so one reconciler proceeds; two test rows |

## Open Questions

All four were put to DR with the Codex-approved design and decided
2026-09-26; none remains open.

1. **`blocked` before fencing exists.** D11 records `blocked` under a
   receipt whose domain half is `DomainNoneHeld` and whose action half is
   checked against every attempt's drain disposition. Codex accepted this
   reading of ADR 0032 §6 in round 3; DR left it as designed.
2. **Retaining `SignalDetector`** (D14). DR: not needed between now and
   item 8, so "whatever is neater". Neater is the design as written —
   removing the slot and the correction touches two files and leaves no
   half-working runner, where removing the detector too would mean editing
   the runner to compile around a channel that no longer exists. D14 stands.
3. **The capability set's source in item 5.** DR: add the operator verb.
   D12 gains `dataplanectl dispatch`; commit 1 of the sequence carries it.
4. **A stuck-open execution as the honest outcome** when a mutation can
   never be confirmed either way (D11). DR: no strong view; left as is.
5. **Gitea in `dataplane-integration`.** Not a design question; the
   implementation's notes report the container-start cost and the item
   shares a fixture if the number is bad.

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
