+++
title = "Inventory: v1 Port, Rework, Rewrite, Drop"
edit_date = "2026-07-14"
status = "draft"
type = "inventory"
summary = "The D8 disposition table at package grain over the actual v1 package list: what ports as-is, what ports with rework, what is rewritten, and what is dropped — with breaking-change principles and the deltas from D8's first-pass guess."
+++

# Inventory: v1 Port, Rework, Rewrite, Drop

Status: draft. Phase 0 item 10. Question (roadmap D8): which v1 subsystems port and which are rewritten — decided explicitly now, at package grain, so Phase 3 is scheduled rather than discovered mid-rewrite.

## Method

Enumeration of the actual v1 package list (every directory under `pkg/`, `internal/`, `cmd/` containing Go source), with non-test LOC measured and import graphs checked for vestigial packages. Dispositions consume the three Phase 0 spike reports — toolloop (item 8), disposable project folder (item 9), maestro-cms boundary (item 13) — and the Accepted ADR set (0017–0025). No code written; no refactor performed.

## Breaking-Change Principles

These govern every disposition below and any port decision made later:

1. **v1 compatibility is a non-goal.** v1 is frozen at `v1-freeze` and the v2 story is migration from nothing (roadmap D7, ADR 0022). No v1 interface, schema, file layout, or message shape constrains v2. The one place v1 survives is as the Phase 1 benchmark *target* — and even there the runner reaches it only through a per-target adapter (ADR 0025), never a compatibility surface.
2. **Shared packages invert the rule.** `maestro-llms` and `maestro-cms` serve consumers beyond Maestro: changes are upstream-first, non-breaking preferred, never forked, and routed through the wishlist docs (`requirements_maestro-llms-wishlist.md`, `requirements_maestro-cms-wishlist.md`). Maestro absorbs adapter cost on its side of the seam rather than exporting breakage.
3. **Accepted ADRs bind every disposition — including "port".** Port-as-is describes a package's *logic*, not its wiring: ported code still lands behind v2 seams (Orchestrator boundary 0019, persistence seam 0022, artifact contracts 0021, the four-way local layout from the project-folder spike). A port that smuggles a v1 assumption — the project directory, SQLite, spec/story intake, the plaintext forge token — is a defect, not a port.
4. **Salvage is a design seed, never a file set.** Rewrites may lift proven fragments as references (the DOT traversal, the knowledge-pack pattern); they never begin from a copied tree.
5. **Grain binds responsibility, not paths.** A disposition attaches to the package's responsibility; the code may merge, split, or move during the port without revisiting this inventory — only a *disposition* change (e.g. rework → rewrite) reopens it.
6. **Drops need no ceremony but leave a record.** Dropped packages die with the v1 tree; anything worth remembering gets a historical ADR note. The inventory itself found two packages already dead in v1 (`pkg/metrics`, `pkg/state`), which is evidence the drop list is real, not aspirational.

**Disposition vocabulary** — **port**: logic unchanged, wiring conforms to v2 seams; **rework**: responsibility kept, structure re-cut to v2 contracts; **rewrite**: responsibility kept, code replaced (salvage per principle 4); **drop**: responsibility ends. The Phase column names where the replacement responsibility lands; drops simply die when their last consumer goes.

## The Inventory

### Foundation and LLM boundary

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `pkg/agent` (+ `config`, `msg`) | 1,180 | port | 3 | Core agent abstractions; wiring conforms to 0019. |
| `pkg/agent/internal/core` | 700 | port | 3 | The FSM engine — D8 first-pass "as-is" confirmed. |
| `pkg/agent/internal/runtime` | 520 | port | 3 | Driver runtime. |
| `pkg/agent/llm`, `llmerrors`, `internal/llmadapter` | 890 | port | 3 | The maestro-llms boundary (D8 as-is); divergence checklist continues to apply. |
| `pkg/agent/middleware/{chat,metrics,validation}` | 660 | rework | 3 | Follows the chat rework and the metrics family's move into the data plane. |
| `pkg/agent/toolloop` | 1,150 | rework | 3 | **Spike item 8**: thin harness layer over `llms/toolloop`, upstream wishlist items filed first; external `Config[T]`/`Outcome[T]` contract preserved so 15 call sites migrate unchanged. |
| `pkg/contextmgr` | 1,380 | rework | 3 | Compaction and token counting keep; context construction moves to artifact seeds (0021 — artifacts are the sole handoff). |

### Agents

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `pkg/pm` | 3,710 | rework | 3 | Re-scoped to Work Groups (0018); spec intake replaced by the Feature/Epic contract (0024). |
| `pkg/architect` | 9,370 | rework | 3 | Re-scoped to Work Groups; reviews become artifact review records (0020/0021); merge authority moves to the harness (0023); Story dispatch moves to the Orchestrator (0019 as amended). |
| `pkg/coder` | 10,160 | rework | 3 | Re-scoped to Work Groups; workspace and branch flows re-cut per 0023 and the project-folder spike. |
| `pkg/coder/claude` (+ `embedded`, `mcpserver`) | 2,520 | port | 3 | The external Claude Code subprocess integration keeps working as-is; only its tool exposure re-plumbs to v2 tool records (0022). |
| `cmd/maestro-mcp-proxy`, `cmd/maestro-mcp-server` | 260 | port | 3 | Companion binaries to the above. |
| `pkg/effect` | 880 | rework | 3 | Approval/completion/merge/question effects become artifact and review flows (0021, 0024). |
| `pkg/tools` | 10,380 | rework | 2–4 | The registry, execution plumbing, and reusable execution/container/file/git tools keep, rewired so every call lands as a tool record — the atomic Audit action unit (0022). v1 workflow terminal tools (spec submission, story lifecycle, maintenance) die with their flows (0024); ProcessEffect signal discipline keeps (0022). |

### Orchestrator

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `internal/kernel` | 720 | rework | 3 | Becomes the Orchestrator core (0019); the persistence queue becomes the persistence seam (0022). |
| `internal/supervisor` | 910 | rework | 3 | Agent lifecycle re-scoped to Work Group lifecycle. |
| `internal/factory` | 290 | rework | 3 | Agent creation for Work Groups. |
| `internal/orch` | 700 | rework | 3 | Startup and airplane-mode orchestration; the bootstrap pointer replaces project-directory discovery (item 9). |
| `pkg/dispatch` | 1,320 | rework | 3 | The typed-channel routing discipline (historical note 0004) ports; the structure does not — v1's Story/hotfix queues, PM interview channels, spec exceptions, and Story leases die. Work dispatch at Epic *and* Story grain becomes Orchestrator machinery: principals author the work graph, the Orchestrator dispatches dependency-ready work from the durable backlog — the authoritative scheduler state — invalidating and reissuing version-bound dispatch records on amendment (0019 and 0024 as amended). |
| `pkg/proto` | 2,040 | rework | 3 | Message types re-cut: spec/story flows die (0024); the failure taxonomy keeps with rework (D8 first pass). |
| `internal/state` | 280 | rework | 3 | Container runtime state records move behind the persistence seam. |
| `cmd/maestro` | 1,530 | rework | 3 | Entrypoint rewired to the v2 Orchestrator and data plane. |

### Persistence and data plane

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `pkg/persistence` | 4,390 | **rewrite** | 2 | The SQLite session log becomes the Postgres artifact schema behind the persistence interface with pluggable auth/data/object modules (0022). |
| `pkg/config` | 3,550 | rework | 2 | Data-plane configuration records plus the bootstrap-pointer loader (item 9); secrets/password machinery reworks into the secrets vault with the key-file root of trust (item 9, 0022). |
| `pkg/state` | 300 | **drop** | — | Vestigial: no production importers (only `pkg/agent` race tests); long superseded by `pkg/persistence`. |
| `pkg/metrics` | 160 | **drop** | — | Vestigial: zero importers anywhere; the metrics family lands natively in the data plane (0022). |
| `pkg/knowledge` | 1,190 | **rewrite** | 6 | **Spike item 13**: ingestion consumed from maestro-cms; the generic graph built as a cms contribution and consumed back; DOT traversal and pack pattern salvaged as design seeds; SQLite FTS5 indexer and hardcoded ontology dropped. |
| `pkg/telemetry`, `pkg/issueservice` | 240 | port | 3 | External maestro-issues failure reporting, unchanged. |

### Git, forge, and workspace

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `pkg/mirror`, `pkg/git` | 1,040 | rework | 3 | The generic git/clone primitives port; the managers do not — `projectDir` derivation and single-repository assumptions die, replaced by repo-record injection (0022) and repo-ID cache paths (item 9). |
| `pkg/forge` | 380 | rework | 2–3 | `forge_state.json` dies: token → secrets vault, binding → repo records (item 9, 0022). |
| `pkg/forge/gitea` | 1,490 | rework | 3 | The Gitea API client ports; the lifecycle does not — project-named Docker volumes become durable bind mounts under Maestro data (0022 as amended), and single-repo assumptions become repo records with multiple forge bindings. |
| `pkg/forge/github`, `pkg/github` | 1,140 | rework | 4 | gh-CLI operations port; grows the harness-exclusive merge machinery (0023: `maestro/epic/*` and default writable only by the harness). |
| `pkg/sync` | 340 | rework | 3 | Airplane-mode Gitea→GitHub sync keeps as a responsibility; its single-repo and `forge_state.json` coupling dies — re-cut over repo records and the secrets vault (item 9, 0022). |
| `pkg/workspace` | 2,050 | rework | 3 | The four-way local split: active workspaces in Maestro state keyed by repo + Story/run (item 9); pre-creation re-cut for Work Groups. |

### Containers, execution, and build

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `pkg/exec` | 2,550 | port | 3 | Container/workspace isolation (D8 as-is). |
| `pkg/build` | 1,540 | port | 3 | Build-service backends. |
| `internal/utils` | 200 | port | 3 | Container helpers. |
| `pkg/lint/loopback` | 460 | port | 3 | — |
| `pkg/dockerfiles` | 42 | rework | 3 | Tracks the bootstrap rework. |
| `pkg/demo` | 2,560 | rework | 4 | Demo mode becomes UAT support; exact shape governed by the open backlog ADR (UAT And Demo Mode). |
| `pkg/preflight` | 980 | rework | 2–3 | Validation logic keeps; reads configuration records, not `config.json`. |

### Bootstrap, templates, and specs

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `pkg/bootstrap` | 3,260 | rework | 3 | D8's "revisit" resolved by item 9: scaffolding persists; the committed repo `.maestro/` survives as project artifacts; Maestro-proprietary prompt fragments move to data-plane prompt packs (pillar 10). |
| `pkg/templates` (+ `bootstrap`, `claude`, `packs`) | 1,730 | rework | 3 | Templates re-cut for v2 states; packs become hash-addressed prompt packs (0022 family). |
| `pkg/templates/maintenance` | 490 | **drop** | — | D8: maintenance mode dropped as-is. |
| `pkg/specs`, `pkg/specrender` | 570 | **drop** | — | Spec intake superseded by Feature/Epic/Story intake (0024). |

### UI, observability, and testing

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `pkg/webui` | 3,000 | **rewrite** | 3→7 | D8: the log view becomes the artifact view. Minimal artifact/chat view lands with the Phase 3 runtime; the multi-user dashboard is Phase 7. |
| `pkg/chat` | 800 | rework | 3 | D8 first pass confirmed. |
| `pkg/logx` | 650 | port | 3 | Logs remain scratch; the record is Audit artifacts (0021). |
| `pkg/utils` (excl. `maestro_files.go`) | 280 | port | 3 | Filesystem, sanitization, token counting, `SafeAssert`. |
| `pkg/utils/maestro_files.go` | 284 | rework | 3 | Half the package by LOC, so split out: re-cut to committed repo-artifact management only (item 9); local state-hub file handling dies. |
| `pkg/testkit`, `internal/mocks` | 1,800 | rework | 2–3 | Test infrastructure follows the interfaces it fakes. |
| `pkg/version` | 22 | port | 2 | Feeds the MPH signature's harness component (0021). |

### Benchmark

| v1 package | LOC | Disposition | Phase | Notes |
|---|---|---|---|---|
| `pkg/benchmark`, `cmd/benchmark` | 1,340 | **rewrite** | 1 | ADR 0025's black-box runner with self-contained persistence is a new build; the SWE-EVO runner's Gitea-fixture mechanics are salvaged as design seeds for a later industry-benchmark adapter. |

## Deltas From D8's First Pass

The first-pass guesses in roadmap D8 mostly held. What changed, and why:

- `pkg/agent/toolloop`: **as-is → rework.** The toolloop spike found byte-identical engines but a real harness layer (durable audit persistence, escalation, per-tool circuit breaking) worth keeping as Maestro code over `llms/toolloop` — contingent on the upstream wishlist.
- Bootstrap "revisit" → **rework**, resolved. The project-folder spike answered the open question: the committed repo `.maestro/` exists and survives; the local state hub retires; prompt fragments leave the repo for data-plane packs.
- Config/secrets "rewrite ... to database where possible" → **rework with a precise target**: configuration records, secrets vault, key-file root of trust, bootstrap pointer (item 9; 0022 as amended).
- `pkg/dispatch`: **as-is → rework**, with a doctrine consequence. The typed-channel discipline ports, but the package is structurally v1 (Story/hotfix queues, PM interview channels, spec exceptions, Story leases) — and reviewing it surfaced that v1 puts Story dispatch in the Architect, which the boundary rule says is wrong: dispatching dependency-ready work is rules, not inference. Work dispatch at both Epic and Story grain is Orchestrator machinery, scheduling from the durable backlog rather than transport channels, with pending dispatch records invalidated and reissued on amendment (decided 2026-07-14; ADRs 0019 and 0024 amended accordingly; in-flight-work policy deferred to Phase 3, tracked in the ADR backlog).
- Repository infrastructure (`pkg/mirror`/`pkg/git`, the Gitea lifecycle, `pkg/sync`): **as-is → rework** by the inventory's own definition. The git primitives and API client port; the managers are structurally tied to `projectDir`, one repository, `forge_state.json`, and project-named Docker volumes — all of which v2 doctrine kills.
- Two drops D8 never listed, found by import-graph check: `pkg/metrics` (zero importers) and `pkg/state` (test-only importers).
- `pkg/benchmark` postdates D8's first pass entirely; classified rewrite under ADR 0025. `pkg/tools` was absent from D8's first pass *and* from this inventory's first draft (caught in review): classified rework across Phases 2–4.

Tallies at this grain: **port 14** groups (~12k LOC), **rework 31** groups (~67k LOC), **rewrite 4** (persistence, knowledge, webui, benchmark — ~10k LOC), **drop 5** (`pkg/state`, `pkg/metrics`, `pkg/templates/maintenance`, `pkg/specs`, `pkg/specrender` — ~1.5k LOC). The center of gravity is rework in Phase 3, which is exactly what the phase is scoped for.

## Related Documents

- [Roadmap](../roadmap.md) D7, D8; [Phase 0 plan](scope-and-plan.md) item 10.
- Spike reports consumed: [toolloop](spike_toolloop.md) (item 8), [project folder](spike_project-folder.md) (item 9), [maestro-cms](spike_cms.md) (item 13).
- ADRs [0019](../../adr/0019-orchestrator-boundary.md), [0021](../../adr/0021-artifacts-and-principal-instances.md), [0022](../../adr/0022-v2-data-plane.md), [0023](../../adr/0023-v2-branch-strategy.md), [0024](../../adr/0024-intake-and-triage-artifact-contract.md), [0025](../../adr/0025-golden-stories-and-benchmark-runner.md).
- Wishlists: [maestro-llms](../requirements_maestro-llms-wishlist.md), [maestro-cms](../requirements_maestro-cms-wishlist.md) (breaking-change principle 2).
