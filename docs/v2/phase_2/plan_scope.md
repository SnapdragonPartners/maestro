+++
title = "Maestro v2 Phase 2: Scope And Plan"
edit_date = "2026-07-24"
status = "draft"
summary = "Proposed Phase 2 scope and execution plan: implement ADR 0022's data plane — Docker-local Postgres and MinIO under the durable data root, golang-migrate from empty, sqlc typed queries for the artifact, principal-instance, call, configuration, and secrets families, the object module with the cross-store commit-order invariant, the cold-backup operation, and one vertical slice importing golden runner results as benchmark-scoped artifacts. Eleven serial work items, opening with the Phase-2-blocking artifact-envelopes ADR."
type = "plan"
+++

# Phase 2: Data Plane And Artifact Core — Scope And Plan

Status: **draft** — awaiting approval by Codex and DR. It flips to `live` on approval and to `archive` at phase close. (Phase 1's plan was merged still `draft` and needed a follow-up flip PR; this document states its own lifecycle to avoid repeating that.)

Goal (from the [roadmap](../plan_roadmap.md)): establish the v2 persistence model.

Phase 2 implements exactly [ADR 0022](../../adr/0022-v2-data-plane.md) as amended — its "Phase 2 scope" section is the binding statement of what this phase owes. The shapes it stores come from [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) (artifact signature, principal instances, lifecycle vocabulary, MPH signature, retention pinning) and [ADR 0018](../../adr/0018-v2-work-taxonomy.md) (hierarchy and non-null lineage); the access discipline comes from [ADR 0019](../../adr/0019-orchestrator-boundary.md); the on-disk layout comes from the [project-folder spike](../phase_0/spike_project-folder.md). Where this plan and those ADRs diverge, the ADRs win; this plan sequences the work and fixes the decisions they left open.

This is the first v2 phase whose output is *not* visible to the conformance ladder. Phase 2 ships no new agent capability, so the golden suite cannot show progress here — only non-regression. The phase's proof is the vertical slice plus an unbroken measuring instrument, and the exit checklist is written accordingly.

## Scope

In scope:

- **One-command local stack** from a clean checkout: Docker Compose Postgres **and MinIO**, both bind-mounted to durable host paths under the Maestro data root, per ADR 0022's local durability invariant. Anonymous and Docker-internal named volumes are prohibited by construction, not by convention.
- **The local path layout**, to the extent Phase 2 needs it: the **config** root (bootstrap pointer + the 0600 root-of-trust key file) and the **data** root (Postgres, object store), with the `MAESTRO_HOME` override collapsing them as named subdirectories. The cache and state roots belong to Phase 3's workspace design and are not built here.
- **`golang-migrate`** wiring and the core schema, applied **from empty**. There is no migration from v1 (roadmap D7); the migration story is from nothing.
- **`sqlc`** typed queries with tests for the five families ADR 0022 names for this phase: **artifact, principal-instance, call, configuration, and secrets** — including the key-file root of trust and the cold-backup operation.
- **The artifact core**: Management and Audit in separate storage families with opposite retention postures; the ADR 0021 status vocabulary (`draft` → `invalidated`|`accepted` → `superseded`|`archived`); amendment chains with monotonic per-original sequence numbers and deterministic effective-view assembly; review records, where reviewer identity alone is not review completion; retention pins; the MPH signature captured as queryable columns rather than reconstructed at read time.
- **The object module**: put/get by content digest, existence check, pin/unpin, delete-unpinned — Maestro's own narrow interface, with an S3-compatible adapter implemented over MinIO. Binaries never live in relational rows.
- **The cross-store commit-order invariant** — object first, pin recorded, row last — enforced at the seam and tested under failure injection, not left as an implementation convention.
- **The persistence interface** (ADR 0019/0022): the seam with pluggable auth, data, and object modules, and the local modules behind it. Phase 2 builds the interface and its local implementations standing alone; wiring the Orchestrator through it is Phase 3's kernel rework.
- **The cold-backup operation** as a defined, tested command: quiesce every writer into the data root, copy the root, restart; restore validated by the seam's digest checks; the unlock key excluded by design, with the two-part restore requirement documented.
- **The vertical slice**: importing golden story runner results into the plane as `benchmark`-scoped artifacts with their principal instances and call records, including at least one object write with digest reference and retention pin. The importer is a Maestro-side reader of the runner's results store — the runner stays black-box and self-contained (ADR 0025's runner design constraints), and acquires no dependency on the data plane.
- **The Phase-2-blocking ADR**: artifact envelopes and payload schemas ([backlog candidate 1](../notes_adr-backlog.md)), authored and Accepted as item 1 — before any DDL is written.

Out of scope:

- **Any change to v1's SQLite persistence path.** This is a hard constraint, not a preference: the benchmark target is v1-as-patched, the golden suite runs at every phase end (build process), and `pkg/persistence` is what that target writes through. The data plane stands alongside it; v1's retirement happens in Phase 3 when the Orchestrator is reworked. A Phase 2 change that disturbs the measuring instrument is a defect.
- Work Group runtime, dispatch, agent execution, and the Work Groups/runs schema family (Phase 3).
- Prompt pack storage (Phase 3, [backlog candidate 5](../notes_adr-backlog.md)), gates (Phase 5), knowledge items (Phase 6), skills/patterns (Phase 5/6). These families are **reserved by name in ADR 0022, not created here** — see the delegated decisions.
- Evidence package generation and the evidence viewer (Phase 4). Phase 2 builds the retention-pinning mechanics those packages depend on; it builds no packages.
- Any UI. The artifact timeline and row patterns fall out of the schema (ADR 0021) but are Phase 4/7 work.
- Cloud mode: the auth mini-app, cloud Postgres, GCS/S3 cloud adapters, federated login (Phase 7). Organization and user **lineage columns are carried now** so team mode never needs a data migration; nothing enforces them.
- Fine-grained roles and security groups (post-MVP non-goals, roadmap pillar 5).
- Online snapshot/`pg_basebackup`-class backup ([backlog candidate 2](../notes_adr-backlog.md), explicitly trailing and non-blocking).
- Non-Docker local Postgres. Supportable later; never the default path.

## Decisions Delegated By ADR 0022

Proposed here, ratified by this plan's approval:

1. **Reserved families are reserved by name, not by empty DDL.** ADR 0022 enumerates sixteen schema families; several are marked as filled by later phases. A table with no consumer is schema speculation, and `golang-migrate` makes adding one later cheap — so a family is created in the migration that first has a caller. This is the concrete defense against the roadmap's "database becomes the new junk drawer" risk, and it makes ADR 0022's "schema review is conformance checking, not design" testable: every table in the Phase 2 migrations traces to both an Accepted ADR *and* a Phase 2 consumer. **Carve-out:** configuration and secrets have no Phase 2 consumer either, but ADR 0022 names them for Phase 2 typed queries explicitly, and the ADR wins over this plan's rule. They are built.
2. **Code location: a new `internal/dataplane/`, with v1's `pkg/persistence` untouched.** The port inventory classifies `pkg/persistence` as **rewrite** in Phase 2, but rewriting in place would break the v1 target mid-phase. So the rewrite lands as a new package and the old one is *deleted*, not edited, during Phase 3's Orchestrator rework. `internal/` matches the seam's nature as Orchestrator machinery (mirroring `internal/kernel`) and keeps it out of any external import surface. Migrations live at `internal/dataplane/migrations/`, sqlc output at `internal/dataplane/gen/` (generated and committed, so a clean checkout builds without codegen tooling), the object module at `internal/dataplane/objects/`.
3. **Stack composition and lifecycle**: a dedicated `deploy/dataplane/compose.yaml` with `make dataplane-up` / `make dataplane-down` / `make dataplane-reset`, deliberately separate from v1's agent-container and benchmark-Gitea machinery so a data-plane restart cannot disturb a benchmark run in flight (and vice versa). `make dataplane-up` is the "one command" the roadmap's exit criterion names: it composes, waits for health, applies migrations from empty, and is idempotent.
4. **Migrations are append-only after merge.** A merged migration is never edited — corrections land as new migrations. There is no v1 data to migrate and no deployed installation to protect, so this is cheap now and load-bearing later; adopting it before the first migration merges costs nothing.

## Deliverables And PR Sequence

One short-lived branch per item (`v2/phase_2/XXX`), one open at a time, per the [build process](../process_build.md). New ADR needs discovered mid-phase go to the [backlog](../notes_adr-backlog.md), not into the phase.

| # | Branch suffix | Deliverable | Size |
|---|---|---|---|
| 0 | `scope-and-plan` | This document, Accepted. | S |
| 1 | `adr-artifact-envelopes` | **ADR: Artifact Envelopes And Payload Schemas** ([backlog candidate 1](../notes_adr-backlog.md)) — the encoding layer ADR 0021 deliberately left open: the JSON envelope with schema and version in every artifact (Markdown as rendering format only); the payload type registry and its validation point; version evolution rules for payload schemas; amendment and effective-view encoding, making 0021's flat-chain semantics concrete; and review-linkage encoding (how a review record binds to an artifact and, for amended artifacts, to a revision). Accepted before item 3 writes DDL. | M |
| 2 | `local-stack` | The one-command local stack: Compose Postgres + MinIO bind-mounted under the data root; the config/data path resolver with `MAESTRO_HOME` override and the 0600 key-file creation at setup (no user ceremony); health-gated startup; make targets; CI job proving it comes up from a clean checkout. No schema yet. | M |
| 3 | `schema-core` | `golang-migrate` wiring and the core DDL, applying from empty: organizations and users; products and repositories (many-to-many with a designated primary Product, forge-independent repo records carrying multiple bindings); features, epics, stories with non-null lineage at every level; principal instances (agent/human/system kinds with their MPH columns); artifacts in **separate Management and Audit families**; review records; amendment and supersession links; retention pins; tool calls, LLM calls, metric events, audit events; binary attachment digest references. Migration conventions documented. Applies from empty in CI, and the "every table has an ADR and a consumer" rule is checked at review. | M |
| 4 | `queries-artifacts` | `sqlc` integration plus typed queries with tests for the **artifact and principal-instance** families: artifact write and read; the `draft` → `accepted` transition gated on a completed review record (reviewer identity alone must not suffice); invalidate/amend/supersede; deterministic **effective-view assembly** — original plus accepted amendments in sequence order, later prevailing on conflict — with tests over conflicting and out-of-order amendments; principal instance lifecycle; MPH signature capture and query, including the input-artifact-digest seeding set. | M |
| 5 | `queries-calls` | Typed queries with tests for the **call** family: tool calls as the atomic Audit action unit, LLM call records for token/cost accounting, metric events. Audit truncation as a supported operation, correctly refusing to prune retention-pinned records. | S |
| 6 | `objects` | The object module and its MinIO-backed S3-compatible adapter: put/get by digest, exists, pin/unpin, delete-unpinned. The **cross-store commit-order invariant** enforced at the seam — object first, pin recorded, row last — with failure-injection tests at each step proving no row ever references a missing or prunable blob, and digest verification on read so a retention bug fails loudly rather than silently weakening a proof. | M |
| 7 | `config-secrets` | The configuration records family (org/product/repo lineage) and the secrets vault: encrypted at rest inside the plane, unlocked by the external key-file root of trust from item 2; OS-keychain and passphrase backends stubbed behind the auth-module interface without being implemented. Typed queries with tests, including the locked-plane failure path. | M |
| 8 | `backup` | The cold-backup operation as a defined, tested command: quiesce every writer into the data root, copy, restart; restore validated by the seam's digest checks. The writer set is enumerated from the composed stack rather than hardcoded, so the airplane-mode local forge joins it in Phase 3 without reopening this work. The unlock key is **excluded from the backup by design**; the two-part restore requirement (backup + key, or secret re-entry) is documented and tested as a failure path. | S |
| 9 | `slice-benchmark-import` | **The vertical slice.** Import golden story runner results from `benchmark/runs/` into the plane as `benchmark`-scoped artifacts with their principal instances and call records, including at least one object write with digest reference and retention pin, exercising the commit-order invariant end-to-end; then query them back. The importer reads the runner's results store as data — the runner takes no dependency on the plane and stays black-box (ADR 0025). This is where the phase's honesty check lives: if the artifact model cannot hold data the runner already produces, better to learn it here than in Phase 3. | M |
| 10 | `phase-exit` | The phase-end `golden-all` conformance run (build process), appended to the [conformance log](../notes_conformance-log.md) as a **non-regression** record with its target descriptor; the exit review against the checklist below; backlog reconciliation for ADR needs discovered in-phase. This document flips to `archive` on merge. | S |

Sequencing notes:

- Items 2 → 3 → 4 are a strict chain; items 5 and 6 both depend on 3 and are independent of each other; item 9 depends on 3, 4, 5, and 6. Item 7 depends on 2 (the key file) and 3.
- **Item 1 is the designated slack.** It is authoring work, reviewable independently, and blocks only item 3 — so it can absorb a stalled code review without violating the one-branch rule, the way story authoring did in Phase 1.
- **Item 1 is also the phase's main design risk.** Every later item consumes its decisions, and an encoding mistake found at item 9 is expensive. It is deliberately sequenced first and sized M rather than S.
- Item 9 is placed last among the code items on purpose: it is the only item that proves the others compose, and it is the natural place for the phase's discovered work to surface.
- Testing rule for this phase: typed-query and object-module tests run against a **real ephemeral Postgres and MinIO** — the substrate is the thing under test, and mocking it would test nothing but the mock. They sit behind the existing `integration` build tag where container startup makes them unsuitable for `make test`. (v1's `docs/TESTING_STRATEGY.md` is `deprecated` and carries no authority for v2 per ADR 0017; its mock-vs-real boundary is cited here as precedent, not as a rule.)

## Exit Checklist

The roadmap's Phase 2 exit criteria, plus the obligations ADR 0022's "Phase 2 scope" adds and this plan's own scope items.

### From the roadmap

- [ ] Postgres, migrations, and typed queries build and run locally via Docker with **one command from a clean checkout**.
- [ ] Core schema migrations apply from empty, and artifact, principal-instance (the roadmap's "agent instance", generalized by ADR 0021), and LLM/tool-call writes have typed queries with tests.
- [ ] One vertical slice writes real data: golden story runner results can be imported into the data plane and queried.

### From ADR 0022's Phase 2 scope

- [ ] MinIO is composed alongside Postgres, both bind-mounted under the Maestro data root; the **local durability invariant** is demonstrated — containers recreated and the Docker daemon restarted, data intact.
- [ ] Typed queries with tests cover the **configuration and secrets** families, including the key-file root of trust.
- [ ] The **cold-backup operation** exists, is tested, and its documented restore path is validated.
- [ ] The **object module** with its S3-compatible adapter is implemented behind Maestro's narrow interface.
- [ ] The vertical slice includes at least one object write with digest reference and retention pin, **exercising the cross-store commit-order invariant**.

### From this plan

- [ ] The **artifact envelopes ADR is Accepted** (backlog candidate 1) before any DDL merges, and the backlog entry is moved to Resolved.
- [ ] Every table in the Phase 2 migrations traces to an Accepted ADR **and** a Phase 2 consumer, or carries a written justification for the exception.
- [ ] **The measuring instrument is intact**: the phase-end `golden-all` run executes against the v1-as-patched target and is recorded in the conformance log as a non-regression checkpoint. Phase 2 ships no capability, so a flat result is the expected and sufficient outcome; a *regression* is exit-blocking.
- [ ] ADR needs discovered in-phase are filed in the backlog, and the Phase 3-blocking entries (amendment vs running work, tool execution policy hook, prompt pack identity) are confirmed still-open or resolved.

## Risks

- **Breaking the measuring instrument.** Phase 1 spent the whole phase making v1 run well enough to measure; Phase 2 touches persistence, which is where v1 keeps its state. Mitigation is structural rather than procedural: the data plane is a new package, `pkg/persistence` is not edited (it is deleted in Phase 3), and the two stacks compose separately so neither restart disturbs the other. The phase-end conformance run is the proof, not the hope.
- **The database becomes the new junk drawer** (roadmap risk, named). Mitigation: the reserved-by-name rule and the ADR-plus-consumer test at review, which together turn ADR 0022's "conformance checking, not design" claim into something a reviewer can actually check.
- **The envelopes ADR expands into a schema-design project.** It is an *encoding* ADR: ADR 0021 already fixed the model, the signature, and the status vocabulary, and explicitly permits Phase 2 to extend that vocabulary but never repurpose it. Mitigation: the five bullets in the backlog entry are the scope; anything beyond them goes back to the backlog.
- **Cross-store bugs are silent by nature.** A row pointing at a pruned blob surfaces as a failed proof months later. Mitigation: failure injection at every step of the commit order, and digest verification on read so the failure is loud at the first touch rather than at audit time.
- **Docker footprint.** Two more long-lived containers land on machines that already run agent containers and, during benchmark runs, a per-attempt Gitea. Worth watching during item 2 rather than discovering during a golden run; the separate compose stack at least makes the data plane stoppable while benchmarking.
- **No conformance signal.** The ladder cannot show Phase 2 progress, which removes the feedback loop Phase 1 relied on to find out it was wrong. The vertical slice (item 9) is the deliberate substitute, and it is scoped to exercise the model against data that already exists rather than data invented to fit it.
- **Review bottleneck** (standing risk since Phase 0). Serial PRs bound operator load; item 1 is the pressure-relief valve.

## Reviewer Questions

1. **Should the artifact-envelopes ADR be authored inside the phase (item 1) or Accepted before Phase 2 opens?** Proposed: inside, as item 1. The backlog's rule is that a blocking entry is Accepted "before its blocking phase starts *implementation*", which item 1 satisfies exactly — it precedes item 3's DDL. Authoring it as a pre-phase step would add a review round and a branch outside any phase for no additional safety.
2. **Reserved families: name-only, or empty DDL now?** Proposed name-only (delegated decision 1), with configuration and secrets built anyway because ADR 0022 names them for Phase 2. Worth confirming, because it means the Phase 2 schema will look *smaller* than ADR 0022's sixteen-family list, and a reviewer expecting the full list should see that as intended rather than as an omission.
3. **`internal/dataplane/` as a new package, with `pkg/persistence` deleted rather than edited in Phase 3?** Proposed yes (delegated decision 2). This is the mechanism that keeps the v1 target alive through the phase, but it does mean the port inventory's "rewrite in Phase 2" disposition completes in Phase 3.
4. **Is Phase 2 the right home for the secrets vault and cold backup?** ADR 0022 says yes explicitly, so that is the default and this plan follows it. Flagged because both are built ahead of any consumer, which is the one place this plan's own anti-speculation rule and the ADR point in opposite directions. If a reviewer prefers to retime them to Phase 3, that is an ADR 0022 amendment, not a plan change.

## Related Documents

- [Roadmap](../plan_roadmap.md): Phase 2, pillars 2–5, D5 (repo vs database), D7 (v1 break), the junk-drawer risk.
- [ADR 0022](../../adr/0022-v2-data-plane.md) — the binding specification for this phase; [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) (artifact and principal shapes, lifecycle, MPH, retention), [ADR 0018](../../adr/0018-v2-work-taxonomy.md) (hierarchy and lineage), [ADR 0019](../../adr/0019-orchestrator-boundary.md) (persistence seam), [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) (runner independence, which the vertical slice must preserve).
- [Project-folder spike](../phase_0/spike_project-folder.md): the four-way local layout, the key-file root of trust, and the backup boundary.
- [Port inventory](../phase_0/inventory_v1-port.md): `pkg/persistence` rewrite disposition and the breaking-change principles.
- [ADR backlog](../notes_adr-backlog.md): candidate 1 (blocks this phase, delivered as item 1), candidate 2 (online backup, trailing).
- [Build process](../process_build.md): roles, branching, one-branch rule, suite-at-phase-end.
- [Conformance log](../notes_conformance-log.md): where the phase-end non-regression run is recorded. ADR 0022 retires it once performance records become first-class artifacts in the plane — a Phase 3 consequence of this phase's schema, not Phase 2 work.
