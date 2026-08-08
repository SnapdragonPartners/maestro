+++
title = "Phase 2 Exit Record"
edit_date = "2026-08-08"
status = "live"
summary = "Closing record of Phase 2: what each item delivered, exit-criteria status including the golden-run criterion DR overrode, decisions and what they cost, and the verification post-mortem behind CLAUDE.md's Verification Discipline."
type = "notes"
+++

# Phase 2 Exit Record

Status: **live** — closed at phase exit (item 10), with the exit checklist walked and every criterion marked met, overridden, or carried forward. It was written as the phase ran rather than reconstructed at the end, because the parts worth keeping — what a decision cost, why a rule exists — are the parts that do not survive assembly from memory.

The binding exit criteria live in the [Phase 2 plan](plan_scope.md). This records **status and evidence** against them; where the two disagree, the plan wins.

## Items delivered

| Item | Branch | State | What it delivered |
| --- | --- | --- | --- |
| 0 | `scope-and-plan` | Merged `83a8522` (#283) | Phase scope, 11-item sequence, four delegated decisions. Plus the build-process policy/mechanics split between `process_build.md` and `CLAUDE.md`. |
| 1 | `adr-artifact-envelopes` | Merged `01e7b82` (#284) | [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) Accepted: envelope/payload split, JCS digests, code-resident type registry, additive-only evolution, RFC 7396 amendments, review bound to the reviewable projection. |
| 2 | `local-stack` | Merged `dcf4dd0` (#288) | Four-root path resolver, root-of-trust key, bootstrap pointer, per-service data directories, Compose stack (Postgres + MinIO), HKDF credentials, `dataplane-up/down/reset`, Linux CI job. |
| 3 | `schema-core` | Merged (#289) | 10 migrations / 19 tables applied from empty, embedded migration runner, sqlc config with drift check, [table inventory](inventory_schema-tables.md). |
| 4 | `queries-artifacts` | Merged `55ab7af` (#293) | The persistence seam and its Postgres module, JCS digests, RFC 7396 effective views, the code-resident type registry, and the artifact/review/principal query families. Design [`design_queries_artifacts.md`](design_queries_artifacts.md) live. |
| 5 | `queries-calls` | Merged `6b66958` (#295) | The call family: per-table write invariants in SQL, the open-to-completed lifecycle behind a structurally enforced update surface, organization-scoped dependency-ordered truncation with a serialization-retry contract, bounded keyset reads, and an exact decimal type for cost. Design [`design_calls_family.md`](design_calls_family.md) live. |
| 6 | `objects` | Merged `6d54487` (#297) | The object module: a blob adapter separated from the seam that owns pins, content proven by a local hash, the amended cross-store commit order with its expected evidence set extracted from the reviewed payload, pins mutable only while their holder is a draft, and reclamation. ADR 0022 amended. Design [`design_object_module.md`](design_object_module.md) live. |
| 7 | `config-secrets` | Merged `10d01ed` (#301) | Configuration records under a governed key registry validated before write and resolved most-specific-wins along the org/product/repository lineage; the secrets vault with per-version keys, canonically-encoded AAD, the six-step ownership ladder, and a root-key provider distinct from the replaceable secrets store. Design [`design_config_secrets.md`](design_config_secrets.md) live. |
| 8 | `backup` | Merged `2585723` (#305) + `64dc138` (#309) | Cold backup, restore, verification, and **new-key recovery** — ADR 0022's second restore branch. Five amendments accepted in review (D3a services-usable-not-merely-started, D4a verification-debt marker, D4b interrupted-recovery as a third gated state, D8a/D8b). Native-Linux CI job green on first run, satisfying item 7's assigned proof. Design [`design_backup.md`](design_backup.md) live. |
| 9 | `slice-benchmark-import` | Merged `6dfeabe` (#315) | **The vertical slice.** The importer reads the runner's results store as data — no runner dependency — writing benchmark-scoped artifacts, principal instances and `llm_calls` through the seam, with evidence walked from the store's own layout and a DRAFT `benchmark.suite_report` holding its complete pin set. Migrations 000017–000020; `dataplanectl bootstrap \| benchmark import \| benchmark show`. Design [`design_slice_import.md`](design_slice_import.md) live. |
| 10 | `phase-exit` | This document | Exit review, backlog reconciliation, and the conformance-log entry. The phase-end `golden-all` run was **overridden by DR** — see below. |

## Exit criteria status

Nothing here is claimed complete that has not been demonstrated. Criteria not yet met are listed as such rather than partially credited.

**Met**

- **Artifact envelopes ADR Accepted before any DDL merged** — ADR 0028 merged in item 1; the first migration merges in item 3. Backlog candidate 1 moved to Resolved.
- **Every table traces to an Accepted ADR and a Phase 2 consumer** — the [table inventory](inventory_schema-tables.md) is the checkable form: **26 tables** at phase close (19 from item 3, 23 after item 7, 26 after item 9's benchmark family), each with its ADR and consuming item, plus the families deliberately deferred and where they land.

- **MinIO composed and bind-mounted; local durability invariant** — composed and bind-mounted under the data root since item 2, and **demonstrated** in item 5 rather than asserted. Evidence below.
- **One command from a clean checkout** — `make dataplane-up` composes, health-gates, and migrates, proven on native Linux CI from cold. The criterion also names *typed queries*; the artifact, review and principal-instance families landed in item 4 and the call family in item 5, which closes it.
- **Migrations apply from empty** — done and CI-proven, with typed queries and tests for the artifact, principal-instance and call families across items 4 and 5.
- **Object module with its S3-compatible adapter** — item 6, behind the narrow interface, with the cross-store commit order enforced at the seam.
- **Configuration and secrets families with typed queries, including the key-file root of trust** — item 7. Both families resolve along the lineage in one statement; the vault seals and opens at the seam under a required root-key provider, and the locked-plane path refuses before reading the key.

- **Cold-backup operation, tested, with its documented restore path validated** — item 8, including new-key recovery (ADR 0022's second restore branch) exercised in native-Linux CI.
- **Vertical slice writes real data and is queried back; re-import is a no-op** — item 9, with the object write, digest reference and retention pin exercising the cross-store commit-order invariant end to end.
- **Backlog reconciliation and the Phase 3-blocking entries confirmed** — item 10, below.

Item 7's criterion was previously recorded as met on an unmerged branch; it has since merged as #301 and the reference is now a merge commit.

**Not met — overridden, not satisfied**

- **The phase-end `golden-all` regression run.** DR explicitly overrode it on 2026-08-08. It is recorded as *overridden*, not as met: two of the six stories ran (one accepted, one deadlocked) and **four never executed**, so this phase carries no regression evidence for those four rungs — nor for the agent-path changes item 9 made. See below.

## The measuring instrument, and why this phase has no conformance run

The plan named this the phase's own top risk — "a Phase 2 change that disturbs the measuring instrument is a defect" — and the *persistence* mitigation held as designed: the data plane landed in a new package, `pkg/persistence` was never edited, and the two stacks compose separately.

**That is narrower than "Phase 2 did not touch the agent path", and the stronger claim would be false.** Item 9 changed `pkg/agent/factory_llms.go` and the metrics middleware to carry the usage-surface upgrade — the adapter descriptor moved `v1-as-patched` 0.1.0 → **0.2.0** and the advertised surface v1 → **v2**. Those changes are real and they are **not regression-cleared**, because the run that would have cleared them did not happen.

What can be said precisely: **the blocker that stopped the run was unrelated to the data-plane work** — a retired model, then two pre-existing v1 defects in the request path and the architect loop. What cannot be said: that five-rung behaviour is unchanged. It is unproven either way.

**The trigger was announced, not unforeseeable.** `claude-opus-4-1` was **deprecated on 2026-06-05** with a retirement date of **2026-08-05**, published in Anthropic's [model-deprecations page](https://platform.claude.com/docs/en/about-claude/model-deprecations) and emailed to organizations with active usage, with at least 60 days' notice. That is seven weeks *before* this phase's plan was approved on 2026-07-24. Nobody read it across into the benchmark's model pins. The first phase-exit attempt died in ~8 seconds per story at the architect's first call, for **$0.00** — there was no model to run against. The failure here is a process gap, not bad luck: no step in the Run Protocol or the phase plan checks its pinned model IDs against their published lifecycle.

Moving the seat to `gpt-4.1` surfaced two further v1 defects, both Phase 3 work:

- **[#316](https://github.com/SnapdragonPartners/maestro/issues/316)** — `llmadapter` always sends a non-nil `temperature`, making `maestro-llms`' documented "nil means model default" unreachable. Every model that now rejects sampling parameters is therefore undrivable; verified 400s from `claude-opus-5`, `gpt-5` and `o4-mini`.
- **[#317](https://github.com/SnapdragonPartners/maestro/issues/317)** — the architect's code-approval loop cannot force its terminal tool. `gpt-4.1` never called `review_complete` there, hit the hard limit at 16 iterations, and deadlocked into `ESCALATED`, which no headless run can answer.

**Only #317 blocks the committed configuration**, and that should not be overstated. `gpt-4.1` accepts sampling parameters, so #316 does not affect it; #316 constrains *which other* models could take the seat. Nor was the viable set shown to be empty — `gpt-4o`, `claude-opus-4-5` and `claude-opus-4-6` all accept `temperature` and were **never tested against #317**. What actually happened is narrower and is what belongs on the record: **after the one tested replacement failed, DR declined further paid exploration** and overrode the suite. Alternatives may well work; nobody has spent the money to find out.

Two of six stories ran. `dep-bump-xnet` (rung 2) was accepted at $1.77; `smoke-comment` (rung 1) ran to 332,670 tokens and $0.95 before deadlocking. So **four rungs never executed**, and the accepted attempt is the only positive evidence this phase holds that the agent path still functions.

**The honest position: Phase 2's regression obligation is carried into Phase 3, not discharged.** Two lessons, neither about the data-plane work: a benchmark pinned to third-party model IDs inherits their retirement calendars, and **the calendar was published seven weeks before the plan was written** — this was catchable. Full detail, identity and per-model costs: the [conformance log](../notes_conformance-log.md).

## Durability demonstration

Run 2026-07-27, before item 5, on the item 5 branch so `main` was untouched. The invariant had been asserted by design since item 2 and never shown; nothing in items 5–10 would have exercised it incidentally.

**Platform.** macOS (darwin arm64), Docker Desktop, bind-mounted roots under `~/Library/Application Support/maestro/data/{postgres,minio}`.

**Method.** Two sentinels seeded and recorded by digest — a Postgres row (`durability_probe`, digest over `id||payload`) and a MinIO object (`s3://durability-probe/<sentinel>`), each with a unique timestamped id. Verified by digest after each phase, so a silently truncated or re-initialised store fails rather than looking empty-but-fine.

**Sentinel identity**, recorded so the evidence outlives the scratchpad:

| | |
| --- | --- |
| Sentinel id | `durability-20260727T161211Z-cf0eef8a9e36` |
| Postgres digest | `37e8acb622b37f70542efd4a700d0e59f49fc76cd9e1c5be5f7b1fedb5d37056` (SHA-256 over `id \|\| payload`) |
| Object digest | `66ba5ff464bb00463e89e125256ebca63b778e74ecc3d3aa5eba14b4ddc265c8` (SHA-256 over the object body via S3) |

| Phase | Command | Container ids | Postgres | Object |
| --- | --- | --- | --- | --- |
| Baseline | seed | pg `c0b0abfde17abede43a0ae502e21645af02b6f5de7e47d54aea135058ada912b`, minio `04be6e85c38dc072d98b4e9151373e5c18507c1d5dd64e4ff680f72b6358ec90` | recorded | recorded |
| Container recreate | `make dataplane-down` then `make dataplane-up` | **changed** → pg `2895737f9d8d33e7374df9bc3a916d4e0b77596c5c74e3daf4e935fdf433e0b2`, minio `994faec7e083af2a7717a4d5c6113aeb11cf175178511f1481b1b609af586486` | digest matched | digest matched |
| Daemon restart | quit and relaunch Docker Desktop | unchanged — the restart policy restarts existing containers rather than recreating them | digest matched | digest matched |

`reset` was never used; it is the destructive path and would have proven the opposite of the point.

After the daemon restart the schema was still at migration version 10, not dirty, with the full table set — so the plane came back usable, not merely present.

**The daemon restart did not go to plan, and the record should say so.** Docker Desktop's process stayed alive but its daemon never answered; DR quit it manually and upgraded Docker, and it returned on **server 29.6.2**. The restart therefore spanned a version upgrade rather than being the clean single-variable restart intended.

That makes it **broader but less controlled**, not stronger. More changed than was meant to, which is not the same as more having been proven: the pre-upgrade daemon version was never captured, so the delta is unrecoverable, and no claim is made here about what the upgrade did to the underlying VM — that was not observed. What the evidence supports is exactly what the table says: the sentinels' digests matched after the containers were replaced, and again after the daemon went away and came back on a different version.

**One thing worth knowing for items 6 and 8.** With the **pinned image** (`minio/minio@sha256:14cea493…`, `RELEASE.2025-09-07T16-13-09Z`), a small object is stored inlined in `xl.meta` rather than as a bare data part — observed while attempting a host-side check with the daemon down. The layout is MinIO's internal business and may differ by version, object size or erasure configuration, so the durable rule is not "objects live in `xl.meta`" but:

**Verify object content through the S3 API, over the object bytes. Never digest MinIO's on-disk files.**

A backup or restore check that digests them directly compares the wrong bytes, and will either fail confusingly or pass vacuously depending on what it compares against.

Probe artifacts were removed afterwards: the table dropped and the bucket deleted, leaving the canonical schema at exactly the migrated set and no buckets.

## Decisions and what they cost

Recorded because the cost is the part that does not survive in the code.

- **The scope model took three wrong shapes** before the exclusive arc: a claimed foreign key a polymorphic column cannot have; a same-transaction seam check that is not a guarantee (a plain `SELECT` takes no lock); and a `scopes` supertable whose foreign keys pointed *into* it, so deleting an entity left its scope row behind with artifacts still resolving to it. The deletion test already specified in the design would have failed against that third version — the test was right and the schema was wrong.
- **Item 3 took four design rounds and five implementation rounds.** The design rounds were cheap (a document); the implementation rounds were expensive (rewriting migrations twice). Most implementation findings were items that belonged on a rejected-cases list written *before* the DDL. That observation is now a rule in `CLAUDE.md`.
- **Health checking is deliberately asymmetric** (item 2): Postgres via its own container healthcheck running an authenticated query, MinIO via a host-side probe. `pg_isready` succeeds with wrong credentials, and a loopback check inside the Postgres container accepts *any* password because the image's `pg_hba` trusts loopback. Both traps were hit while testing manually before being designed out.
- **`paths.Bootstrap` is the local module's bootstrap, not the persistence contract.** Recorded as a rule because the pressure to import the concrete struct from above the seam will be real in Phase 3, and that is precisely how a local-only assumption hardens into architecture.

## Verification post-mortem

The evidence behind [`CLAUDE.md`'s Verification Discipline](../../../CLAUDE.md). The rules there are deliberately short; these are the cases that produced them, kept so a future reader can see what each rule cost before deciding to relax it.

**Claims asserted from memory and wrong.** Two shipped into comments and commit messages: *"golang-migrate wraps Postgres migrations in a transaction"* (it does not — every migration needs explicit `BEGIN`/`COMMIT`, and without it a half-applied migration is the recoverable case lost), and *"`GracefulStop` is unbuffered, so a plain send deadlocks"* (it is `make(chan bool, 1)` in the pinned v4.19.1). The second is the worse failure: the commit message presented a hazard invented from an unverified assumption as a bug I had *found and shipped*. Both took under a minute to check once challenged.

**Assertions that could not fail.** A credential-leak test whose two comparisons were both vacuous — the base64url alphabet can never contain raw key bytes, and a 64-character hex needle cannot occur in a 43-character output — which passed against a `Derive` that returned the key verbatim, confirmed by writing that mutant. A lock test that passed with the lock deleted. A CI label check that proved only *at least one* container carried the label. A drift check blind to untracked files, so a new generated file left it green. Each looked like coverage and was decoration.

**Fixes that introduced new defects.** Fixing IPv6 support introduced bracketed forms, which `net.JoinHostPort` would double-bracket. Fixing an ownership error message introduced a copy-pasteable `sudo chown` command for a path that contains a space on the default macOS layout. The first key-durability fix synced only on the creating path, leaving readers able to return a key whose directory entry was not yet durable.

**The same rule applied in one place and not the adjacent one.** `Roots.Ensure` gained a permission check while `EnsureServiceDataDirs` did not. Organization lineage was added without user lineage. A foreign key was removed with nothing put in its place. Understanding that `git diff` ignores untracked files — and using that knowledge to fix the *test harness* — did not carry to the *assertion the harness was testing*.

**Tests whose selector was the property they checked.** Item 7 produced five, and they share one cause: the guard and its test were written together, so the test inherited the guard's blind spot. An AAD test that moved a ciphertext to another row — which fails on the derived key before authentication is reached, so it would have passed with no AAD at all. A creation-ownership test that never supplied an owner, and so could not discover that supplying one was possible. A `validator == nil` guard in a test, asserting the very thing under test: a typed nil is never equal to nil, so as a guard it could only ever pass. `staticcheck` caught one instance of that (SA4023) and not its twin one file away, because the twin read from a table field it could not fold — so the linter's silence was about provability, not correctness. The rule that came out of it: **write the allow-list, not the filter, and mutation-verify by deleting the thing the test selects on.**

**Coverage that stopped one statement short.** The vault's ownership filter exists in two separate SQL statements — the identity read and the ladder — and every ownership test went through the first. Weakening the *ladder's* individual-ownership branch to a tautology left the whole suite green, and the consequence is not subtle: a caller would resolve another user's personal token and use it, with the level and ownership reported back looking entirely ordinary. Found by mutation, not by review or by reading. A predicate duplicated across statements needs a test per statement; one test proves one statement.

**A defect with no behavioural signature at all.** Configuration ids were UUIDv4 where the schema requires v7. Reverting the fix passed the entire integration suite, because a v4 id is indistinguishable from a v7 in every test that only checks what the seam returns. It reached review because nothing could have caught it. The response is the same as for the untestable guarantees below — assert the property directly, or accept that it is uncovered no matter how green the suite is.

**Mutation harnesses that lied.** Four hollow results in one session: mutants that did not compile reported as kills, anchors counted with `grep -c` (which counts matching *lines*, not occurrences of a multi-line pattern) reported as "not applied", and — the expensive one — a harness killed by a timeout mid-restore, leaving the working tree mutated and a later green run meaningless. The rules that came out: **assert the anchor matched exactly once, assert the mutant compiled, and verify the restore against `HEAD` before believing anything that follows.**

**A test that deadlocked instead of failing.** A row-lock test parked a goroutine inside a validator to hold a lock open; on the failure path that goroutine still held a pooled connection, and `pgxpool.Close` blocks until every connection is returned, so the fixture's cleanup hung and took the package with it. The mutation did not report — it stopped. Any test that blocks a real connection needs its release wired to `t.Cleanup` before it is ever run in anger.

**A guard catching its own author.** Item 7 added a structure test forbidding any function but `rootKeyFor` from reaching `secret.KeyFile`, because only that function knows whether an operation may *create* key material. Later in the same item, making the root-key provider a required dependency broke ten call sites, and the fix for one of them constructed a second `KeyFile` — remaking that decision outside the one place allowed to make it. The test refused it. Worth recording because the defect it prevented is invisible locally: it passes on every machine whose key already exists, which is every machine that has run `up` once.

**Guarantees that cannot be tested.** Three key-file durability defects were green the entire time, because `fsync` ordering and crash windows are not reproducible in a unit test. Review caught all three. The response is not more tests but stating the boundary beside the code, so adjacent passing tests stop implying coverage.

**One P0.** A down-migration test ran against the canonical `maestro` database and dropped every table in it — written by copying a file through `/tmp` without asking which database it pointed at. Now behind a disposable-database harness, which itself leaked a database on first run because a deferred close ran before `t.Cleanup`.

## The shape of the recurring problem

Across item 3 and item 4 the implementation converged quickly and the *evidence* took the rounds. There were real implementation defects — the scope model took three wrong shapes, a down-migration test dropped every table in the canonical database, several fixes introduced new defects. But those were found. The pattern that kept recurring, and kept surviving review until someone looked twice, was different:

**The recurring meta-defect was evidence that failed to discriminate correct from incorrect behaviour.**

Items 5 through 7 did not change that diagnosis; they sharpened it. The implementation defects that reached review in item 7 were real but ordinary — a validate-before-classify ordering, a read that classified against an unlocked row, two write verbs disagreeing about the same state. What kept surviving was again the evidence: a suite that was green against a v4 identifier, against a tautological ownership filter, and against a race test that forced no race. Every one of those was found by asking *what would this test do if the code were wrong* rather than by reading the code again.

The practical consequence for later phases is that **mutation testing stopped being a finishing step and became the thing that finds the gaps.** Three of item 7's findings were coverage holes rather than code defects, and none of them would have been found any other way — which also means the harness doing the mutating is load-bearing, and a harness that reports a kill it did not earn is worse than none.

Its forms, all seen in this phase: assertions that could not fail; a drift check blind to untracked files; a mutation check whose mutant did not compile, so the build failure was counted as "no surviving mutants"; a mutant that died for the wrong reason, certifying a test that was itself a false positive; and a backstop unreachable through any normal call path, so removing it left the suite green.

The rules in `CLAUDE.md`'s Verification Discipline came from the first two forms. The last three are newer and are recorded here rather than added to `CLAUDE.md` now — a holistic pass over that file is planned, and piecemeal additions are how a short rule list becomes an unread one.

## Follow-ups

- [maestro#287](https://github.com/SnapdragonPartners/maestro/issues/287) — fold `dataplanectl` into the main binary; blocked on moving the compose assets under a package, since embedding cannot reach parent directories.
- [maestro#282](https://github.com/SnapdragonPartners/maestro/issues/282) — the benchmark-evidence-reviewer agent. Until it exists there is no `accept` verb and every imported suite report stays `DRAFT — UNREVIEWED — NOT AUTHORITATIVE`, which is the correct state under ADR 0020's non-author-review invariant, not a gap in the import path.
- [maestro#314](https://github.com/SnapdragonPartners/maestro/issues/314) — a checked-in mutation harness satisfying the Defect-Shaped Verification rule. Deliberately deferred past Phase 2.
- [maestro#316](https://github.com/SnapdragonPartners/maestro/issues/316) — sampling parameters must be optional; `llmadapter` forces `Temperature` non-nil. **Phase 3.**
- [maestro#317](https://github.com/SnapdragonPartners/maestro/issues/317) — the architect approval loop cannot force its terminal tool, and `ESCALATED` deadlocks headlessly. **Phase 3.** This is the one that blocks the committed config; the cross-vendor pairing preference rides on it.
- [maestro#318](https://github.com/SnapdragonPartners/maestro/issues/318) — benchmark preflight must refuse or record a dirty working tree. The phase-exit target was built from uncommitted changes, so its recorded commit does not rebuild its binary. Found in review, not at run time, which is the point.

### ADR needs discovered in-phase

**One**, raised by DR during the item 10 review and filed as [backlog candidate 16](../notes_adr-backlog.md): **reviewer heterogeneity must mean distinct *lineage*, not distinct model.** ADR 0020 already carries the norm and its degradation semantics, but defines the reviewer as running "a distinct model" from the author without defining lineage — so `claude-opus-4-1` reviewing `claude-sonnet-4-6` reads as heterogeneous while being one lab and one training lineage. That is `paired-default`'s exact composition since its first commit, which means **every row in the conformance log was produced by a same-lineage configuration and none was flagged degraded** — ADR 0020's flagging-and-surfacing requirement was never implemented, and no lineage concept exists in the code. Amendment blocks Phase 5; the benchmark-side clarification should land sooner.

Everything else found in this phase was an implementation defect or an amendment to an already-Accepted ADR, not a decision needing its own record:

- Item 6 amended **ADR 0022** (cross-store commit order, acceptance as the verifying step).
- Item 8 took five amendments to its own design document, all within ADR 0022's accepted backup contract.
- Items 9's thirteen P1s were implementation defects; its design amendments (D4a, D6a, D7a–D7e, D9a) sit inside ADR 0021's and ADR 0028's accepted models.
- #316 and #317 are v1 defects with an obvious right answer; neither needs a decision recorded before it can be fixed.

The three **Phase 3-blocking** backlog entries were re-checked and remain **open and unresolved**: candidate 3 (amendment vs running work), candidate 4 (tool execution policy hook), candidate 5 (prompt pack identity, resolution and storage). None was addressed in Phase 2, and each still blocks Phase 3 implementation. Candidate 2 (online backup) remains open and correctly non-blocking — item 8 delivered the cold baseline it trails.
