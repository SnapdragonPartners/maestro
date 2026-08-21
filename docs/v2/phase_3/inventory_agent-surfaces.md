+++
title = "Inventory: Agent Surfaces — Retain, Refactor, Replace, Retire"
edit_date = "2026-08-21"
status = "draft"
type = "inventory"
summary = "Phase 3 item 1: the surface-grain disposition table over the agent, toolloop, proto, supervisor, dispatcher and Claude adapter subsystems, classified by evidence from the import graph unioned over every valid build configuration against the frozen v1 tree — including the three findings that change the plan's starting hypothesis, the deltas from the Phase 0 package-grain inventory, and the reachability evidence that makes issue #298's deletions complete rather than approximate."
+++

# Inventory: Agent Surfaces — Retain, Refactor, Replace, Retire

Status: draft — Phase 3 item 1. Authoring work; no code changed.

Phase 0's [v1-port inventory](../phase_0/inventory_v1-port.md) classified whole
packages as port/rework/rewrite/drop. This document works at **surface grain** —
a named responsibility inside a package — over the six subsystems item 1 names,
using the vocabulary the phase plan fixes: **retain, refactor, replace, retire**.

The plan's ["Decisions This Plan Fixes"](plan_scope.md) §1 supplies a starting
hypothesis. This inventory's job is to test it against the tree, not to restate
it. Three of its clauses survive unchanged, two are sharpened, and one is
answered more simply than it was posed.

## Method

- **Reference points.** The frozen v1 tree at `v1-freeze` (`d272332`), and
  `main` at `ca92bad`.
- **Import graph.** `go list` over `./...`, capturing `Imports`, `TestImports`
  and `XTestImports` separately, so a package whose only consumers are tests is
  distinguishable from one with production consumers. Self-edges are excluded:
  a leaf package tested by its own in-package tests would otherwise appear to
  have a consumer.
- **Build configurations, not a tag union.** The authoritative basis is the
  **union of reachability over the valid build configurations**, computed one
  configuration at a time. Enabling every tag at once is a *single*
  configuration and is not generally equivalent to that union — a file guarded
  by a negated or compound constraint can be excluded from the all-on build
  while being part of another valid one. See the warning below.
- **Reachability is a proxy.** "Zero importers" supports "nothing imports it",
  not "it is dead". Every retirement below is additionally cross-checked
  textually **across all file types, not only `*.go`**, and the two checks are
  reported separately. Documentation references do not prevent retirement, but
  they must be disposed of explicitly rather than left unmentioned.
- **Grain.** Following Phase 0's principle 5, a disposition attaches to a
  responsibility, not a path. Code may move during the port without reopening
  this inventory; only a disposition change reopens it.

### Warning: build configuration changes the answer

An import-graph analysis run under the default configuration reports **four
packages as dead that are not**. The repository carries four build tags, and
three of them guard very few files, which is exactly why an analysis misses
them:

| Tag | Files guarded |
| --- | --- |
| `integration` | 73 |
| `e2e` | 3 |
| `gcs` | 1 |
| `cloud` | 1 |

`internal/dataplane/cloud` is the trap worth naming. It sits behind the `cloud`
tag — deliberately not `integration`, because the pre-push gate runs the
integration suite and requiring cloud credentials to push would either block
contributors without them or skip silently and look green. It therefore has no
in-repo importer under any ordinary listing, and its own tests are invisible to
a default `go list`. Nothing about it is dead.

**The tag set above is documentation of a moment, not a source of truth.** It
must be re-derived at analysis time. Hand-maintained enumerations have already
failed three times in this repository's history.

### Reproducing the graph

Constraint expressions were enumerated first, because they determine which
configurations are needed:

```bash
grep -rh "^//go:build" --include="*.go" . | sort -u
```

At `ca92bad` every constraint is a **bare single tag** — there are no negated
(`!tag`) and no compound (`&&`, `||`) expressions. The five configurations
below therefore cover every valid one.

```bash
F='{{.ImportPath}}|{{join .Imports ","}}|{{join .TestImports ","}}|{{join .XTestImports ","}}'
go list                -f "$F" ./...    # default      93 packages
go list -tags integration -f "$F" ./... # integration  93
go list -tags e2e         -f "$F" ./... # e2e          94
go list -tags gcs         -f "$F" ./... # gcs          93
go list -tags cloud       -f "$F" ./... # cloud        93
```

Each configuration yields a production-importer set (`Imports`) and a
test-importer set (`TestImports` + `XTestImports`) per package; the tables in
this document are the **union of those five**, with self-edges removed.

Because this repository currently has no negated or compound constraints, that
union was checked against the single all-tags-on configuration
(`go list -tags integration,e2e,gcs,cloud`) and found **identical** — zero
differing packages. That equivalence is a property of the current constraint
set, **not a general one**: it must be re-checked, not assumed, whenever a
constraint expression is added.

Exported-surface enumeration must exclude test files
(`grep --exclude='*_test.go'`). An earlier pass of this inventory did not, and
attributed two test doubles to `internal/supervisor`'s production surface.

The binding form of this rule now lives in
[the build process](../process_build.md) under *Reachability Claims*, where it
binds reviewers as well as authors.

## Findings That Change The Starting Hypothesis

### 1. `StateStore` is vestigial, not merely non-durable

The plan calls `StateStore.Save(id string, value any)` "the one already decided",
on the grounds that `json.MarshalIndent` plus `os.WriteFile` with no temporary
file, rename, or fsync is not a durable checkpoint. That is accurate —
`pkg/state/store.go:133,140` is exactly that, with no `O_SYNC`, no rename dance,
and no fsync.

But the durability argument understates the case. The evidence says there is
**nothing persisting through this seam at runtime at all**:

- The interface is declared at `pkg/agent/internal/core/machine.go:70-76` and
  re-exported as an alias at `pkg/agent/core.go:55`.
- Its **only** implementation is `pkg/state`, which has **zero production
  importers** — its sole consumer is a `pkg/agent` race test.
- Every production construction passes `nil`: `pkg/pm/driver.go:207`,
  `pkg/pm/driver.go:255`, `pkg/architect/driver.go:187`,
  `pkg/coder/driver.go:500`.
- The one call site that forwards a store rather than `nil`
  (`pkg/agent/internal/runtime/base_driver.go:25`, via `config.Context.Store`)
  is never given one: no production code assigns `runtime.Context.Store`.

**Consequence for item 3.** Typed durable checkpoints are a *new build*, not a
replacement of a working mechanism, and no migration of existing on-disk state
is owed — there is none. The disposition is **retire**, for the interface and
its implementation together. This also merges two work items that look separate:
"replace `StateStore.Save`" and #298's "delete `pkg/state`" are one action seen
from two sides.

### 2. `pkg/agent`'s public driver surface is test stubs in a production file

`pkg/agent/core.go:135-147` defines `NewShutdownableDriver`, `NewBaseDriver` and
`BaseDriver` as stubs — `NewBaseDriver(_ *Config, _ proto.State)` discards both
parameters and returns `&BaseDriver{}`; `ShutdownManager.IsShuttingDown()`
returns a constant `true`. Their own comments say "legacy test stub" and "to be
removed after test migration", with no linked issue.

The real driver runtime is `pkg/agent/internal/runtime`. So `pkg/agent`'s
exported driver surface does not describe the running system, and any port that
treats it as the agent abstraction would carry a stub across the boundary.

Under CLAUDE.md's standard — critical untracked TODOs are acceptance blockers,
and orphaned code is removed unless a documented gate retains it — these are
**retire**, with the test migration they are waiting on scheduled by item 6.

### 3. `pkg/agent/middleware/chat` is dead

One file, `injection.go`. **Zero Go consumers**: no production importers and no
test importers, in any of the five build configurations.

It is **not** free of textual references, and an earlier draft of this document
wrongly said it was — that claim came from a grep restricted to `*.go`. Four
documents name `pkg/agent/middleware/chat/injection.go`:

| Reference | Status |
| --- | --- |
| `docs/MAESTRO_CHAT_SPEC.md:285` | `deprecated` |
| `docs/MAESTRO_LLMS_MIGRATION.md:168` | `deprecated` |
| `docs/adr/0015-agent-chat-and-human-in-the-loop-escalation.md:45` | `deprecated` |
| `docs/wiki/LLM_WIKI.md:1773` | `deprecated` |

All four carry `status = "deprecated"` and therefore no authority; ADR 0015 is
additionally in the historical 0001–0016 range that CLAUDE.md admits as
non-binding context only. **They do not block retirement**, and none needs
editing: each describes a v1 mechanism accurately as of the tree it documents.
Retirement should note them so a later reader who greps the name finds the
disposition rather than concluding the inventory missed them.

Phase 0 classified the middleware group **rework** at package grain, which is
right for `metrics` and `validation` and wrong for this member.

This is a **sixth** drop candidate, absent from the Phase 0 drop list and from
#298. Disposition: **retire**.

## The Disposition Table

Evidence column cites what was measured. Every row carries surface-level
evidence; no row rests on the plan's hypothesis alone.

### Agent core

| Surface | Disposition | Evidence |
| --- | --- | --- |
| Role state machines and transition tables (`internal/core`) | **retain + refactor** | 1,127 non-comment lines, 3 files; consumed by `pkg/agent` and `internal/runtime` only. Hypothesis confirmed: the FSM engine is self-contained and does not reach the transport. |
| `StateStore` interface + `pkg/state` implementation | **retire** | Finding 1. Zero production implementations; all four construction sites pass `nil`. |
| Public driver stubs in `core.go` (`BaseDriver`, `NewBaseDriver`, `NewShutdownableDriver`, `ShutdownManager`) | **retire** | Finding 2. Parameters discarded; self-described legacy stubs. |
| Driver runtime (`internal/runtime`) | **refactor** | 599 lines, 6 files. The real driver; rewires to item 5's boundary. |
| LLM boundary (`llm`, `llmerrors`, `internal/llmadapter`) | **retain** | Widest internal fan-in of the agent tree (`llm`: 9 production importers). Phase 0 "port" confirmed; the maestro-llms divergence checklist continues to apply. |
| `middleware/chat` | **retire** | Finding 3. |
| `middleware/metrics` | **refactor** | 3 production importers. Follows the metrics family into the data plane (ADR 0022). Note a live v1→v2 edge: `internal/dataplane/benchmarkimport` imports it in tests. |
| `middleware/validation` | **refactor** | Single importer (`pkg/agent`); moves behind the boundary with the loop. |

### Toolloop

| Surface | Disposition | Evidence |
| --- | --- | --- |
| `ToolLoop`, `Config`, `Outcome` external contract | **retain** | 4 production importers (`supervisor`, `architect`, `coder`, `pm`). Phase 0's rework note preserves this contract precisely so call sites migrate unchanged. |
| Harness layer — durable audit persistence, escalation, per-tool circuit breaking (`EscalationHandler`, `ToolCircuitBreakerConfig`, `ActivityTracker`) | **refactor** | 3,228 lines, 5 files. The Phase 0 toolloop spike found this layer worth keeping over `llms/toolloop`. Refactored behind item 5's boundary, per the plan — not rewritten. |
| `TerminalTool[T]` and the one-goal-one-exit rule | **refactor** | Declared `toolloop.go:36`; `Config.TerminalTool` requires exactly one (`:113`) and construction rejects `nil` (`:208`). The loop can *require* a terminal call but cannot *force* one: when the model never calls it, extraction returns `toolloop.ErrNoTerminalTool` (`pkg/architect/toolloop_results.go:28,65,82` for `submit_reply`, `review_complete`, `story_edit`) and the architect escalates. That is [#317](https://github.com/SnapdragonPartners/maestro/issues/317) — the approval loop deadlocks into `ESCALATED`, which is unreachable headlessly. The refactor must make forcing expressible at the boundary. |

### `pkg/proto`

The widest surface in the named set: **16 production importers**, ~3,106 lines.
The plan says split, and the exported type list shows the seam cleanly.

| Surface | Disposition | Evidence |
| --- | --- | --- |
| Failure taxonomy (`FailureKind`, `FailureScope`, `FailureSource`, `FailureOwner`, `FailureAction`, `FailureInfo`, `FailureEvidence`) | **retain** | Self-contained domain vocabulary; Phase 0's "failure taxonomy keeps with rework" confirmed. |
| Domain enums (`StoryType`, `Priority`, `Confidence`, `ApprovalStatus`/`Type`) | **retain** | Value types with no transport coupling. |
| `State` + `StateChangeNotification` | **refactor** | `State` is the FSM vocabulary shared with `internal/core`; the notification is process-local transport. Split along that line. |
| `StateQuestion` (`QUESTION`) | **retain** | Declared `pkg/proto/message.go:641`. Mapped to artifacts and Story state by item 6, per hypothesis. |
| `StateSuspend` (`SUSPEND`) | **retire or redesign** | Declared `message.go:644`. Return-to-origin resumption is process-local: `internal/supervisor/supervisor.go:331,336` observes the transition pair directly, and `pkg/agent/internal/core/machine.go:533,546` gates it. This conflicts with artifact-level restart (plan decision 2). Item 6 settles it. |
| Process-local messaging (`AgentMsg`, `MsgType`, `UnifiedRequest`/`Response`, `RequestKind`/`ResponseKind`, and the ~20 `*Payload` types) | **replace** | The external boundary replaces these. Spec/story-flow payloads (`StoryComplete`, `Requeue`, `Hotfix`, `Clarification`) die with their flows under ADR 0024. |

### Supervisor and dispatcher

| Surface | Disposition | Evidence |
| --- | --- | --- |
| `Supervisor` lifecycle (`RestartPolicy`, `RestartAction`, shutdown handlers) | **replace** | 1,640 lines in a single production file; one importer (`cmd/maestro`). Process-local restart authority moves to the Orchestrator; #265's single-owner restart removes the dual death-observer shape (item 9). |
| SUSPEND observation (`supervisor.go:331,336`) | **retire** | Dies with the SUSPEND decision above. |
| `Dispatcher`, `ChannelReceiver`, queue surfaces (`QueueHead`, `QueueInfo`, `AgentInfo`) | **replace** | 3,661 lines, 8 production importers. Phase 0 already reclassified this as-is → rework with a doctrine consequence: dispatching dependency-ready work is rules, not inference, so it is Orchestrator machinery (ADR 0019). Story/hotfix queues, PM interview channels, spec exceptions and Story leases die. |
| Typed-channel routing discipline (historical note 0004) | **retain** | The discipline ports as a design constraint; the structure does not. |

### Claude adapter

| Surface | Disposition | Evidence |
| --- | --- | --- |
| Subprocess **mechanism** — spawning and driving the external Claude Code process, `StreamParser` and the stream event types | **retain** | 3,558 lines, 6 files; single importer (`pkg/coder`). Phase 0's "port" holds for the mechanism: driving the external process keeps working. |
| Subprocess **interface** — `Runner`, `RunOptions`, `Result` | **refactor** | Cannot be retained as-is; ADR 0032 documents three defects in this surface. `RunOptions.WorkDir` (`types.go:50`) is an **Agent-derived local path**, set from the Coder's own `workDir` (`pkg/coder/claudecode_coding.go:287`, constructor arg at `pkg/coder/driver.go:470`) — prohibited by [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) §8, and the field a **fenced resource reference** replaces (ADR 0032 §424). The outcome is reconstructed after the fact from two channels that can disagree (below). **Neither `RunOptions` nor `Result` carries a protocol version** — the `"version": "1.0.0"` at `server.go:296` is MCP's own `serverInfo`, not Maestro's contract. Item 8 adapts this surface to the versioned contract. |
| `Installer`, `embedded` | **retain** | Cross-architecture packaging per ADR 0026. |
| `lastEffect` and signal-correction path (`SignalDetector`, `SignalToolInput`, `Signal`, `Server.ConsumeLastEffect`) | **replace** | A single mutex-guarded slot (`mcpserver/server.go:34`) overwritten by every tool call returning a `ProcessEffect` (`:430`), drained via `ConsumeLastEffect` (`:166`), then used to **correct** the signal the runner's own stdout parser inferred from a tool name (`pkg/coder/claude/runner.go:199-206`). Two channels that can disagree, one retaining only its most recent value. Item 5 removes it; **item 8 builds the real adapter over the contract** — explicitly not a side effect of item 5. |
| MCP JSON-RPC transport (`Server`, `JSONRPCRequest`/`Response`/`Error`) | **refactor** | 935 lines, one production file, four exported types. The transport itself is sound — loopback listener with token authentication (`Start:66`, `Stop:126`, `Port:152`, `Token:159`, `authenticateConnection:224`) — and is retained in shape; what changes is what crosses it. Tool exposure re-plumbs to v2 tool records (ADR 0022), and `ConsumeLastEffect` leaves the surface with the row above. |
| `cmd/maestro-mcp-proxy`, `cmd/maestro-mcp-server` | **retain** | Companion binaries. #271's per-arch exec remains open against the proxy. |

## Reconciliation With The Phase 0 Package Inventory

Phase 0's dispositions hold at package grain. The deltas are all refinements
that only surface grain or the import graph could produce:

| Phase 0 | This inventory | Why |
| --- | --- | --- |
| `pkg/agent/middleware/*` — rework (group) | `chat` **retire**; `metrics`, `validation` refactor | Group-grain disposition hid a dead member (finding 3). |
| `pkg/agent` — port | Port confirmed for the FSM and LLM boundary; **retire** for the `core.go` stub surface | Finding 2: the exported driver surface is not the running system. |
| `pkg/state` — drop, Phase `—` | **retire**, and it is *the* `StateStore` implementation | Finding 1 gives the drop an owner and a date instead of "dies when its last consumer goes". |
| `pkg/proto` — rework | Split into four dispositions across one package | The plan's "split" made concrete against the exported type list. |

Phase 0's principle 6 — "drops need no ceremony but leave a record" — is what
#298 was filed against: the mechanism had no owner. Finding 1 supplies one for
`pkg/state`.

## Issue #298: Making The Deletions Complete

#298 asks for the five `drop` dispositions to be scheduled. Re-derived from the
import graph unioned over all five build configurations on `main` at `ca92bad`,
rather than carried from the
issue:

| Package | Production importers | Test-only importers | Blocked on |
| --- | --- | --- | --- |
| `pkg/metrics` | none | none | **nothing** |
| `pkg/state` | none | `pkg/agent` | **nothing but the tests** |
| `pkg/templates/maintenance` | `pkg/architect` | none | v1 architect (item 14) |
| `pkg/specs` | `pkg/tools` | none | v1 tools (item 14) |
| `pkg/specrender` | `pkg/architect`, `pkg/pm` | none | v1 PM + architect (item 14) |

Unchanged since the issue was filed. The first two are deletable now; the other
three are genuinely blocked on live v1 consumers and belong to item 14. Add
`pkg/agent/middleware/chat` to the first group — it is blocked on nothing.

`pkg/metrics` is the only importer of `prometheus/client_golang` and
`prometheus/common` in the repository, so its deletion removes two direct
dependencies that nothing calls.

## Open Points For Review

1. **A defect in the phase plan, for DR and Codex rather than for this document
   to fix.** [`plan_scope.md`](plan_scope.md) states: "#316 and #317 close with
   v1, and each leaves a **requirement** behind rather than a fix: a terminal
   tool must be forceable, and sampling parameters must be optional." Read
   positionally that maps #316 to the terminal tool, which is **reversed** —
   [#317](https://github.com/SnapdragonPartners/maestro/issues/317) is the
   architect approval loop that cannot force its terminal tool, and
   [#316](https://github.com/SnapdragonPartners/maestro/issues/316) is
   `llmadapter` forcing `Temperature` non-nil. This inventory follows the
   issues. `plan_scope.md` is `live` and Accepted, so the correction is not
   made here.
2. Whether the `core.go` stub retirement (finding 2) belongs to item 6 with the
   test migration, or earlier and standalone with #298's first group.
3. Whether `SUSPEND` is retired or redesigned is deliberately left to item 6 —
   this inventory records only that its current mechanism is process-local and
   conflicts with artifact-level restart.

## Related Documents

- [Phase 3 scope and plan](plan_scope.md) — item 1; "Decisions This Plan Fixes"
  §1 supplies the starting hypothesis this document tests.
- [Phase 0 v1-port inventory](../phase_0/inventory_v1-port.md) — the
  package-grain dispositions reconciled above.
- ADRs [0019](../../adr/0019-orchestrator-boundary.md) (Orchestrator boundary),
  [0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md)
  (Incubator/Habitat; §8 prohibits the Agent-derived path),
  [0022](../../adr/0022-v2-data-plane.md) (persistence seam),
  [0024](../../adr/0024-intake-and-triage-artifact-contract.md) (intake
  contract), [0030](../../adr/0030-tool-execution-policy-hook.md) (execution
  boundary), [0032](../../adr/0032-agent-execution-contract.md) (agent execution
  contract).
- [#298](https://github.com/SnapdragonPartners/maestro/issues/298) — the five
  drop dispositions this document makes complete.
