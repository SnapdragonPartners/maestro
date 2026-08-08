+++
title = "Phase 2 Item 9 Design: Importing Golden Runner Records"
edit_date = "2026-08-08"
status = "live"
summary = "Design for the vertical slice: importing golden runner records into the main Postgres plane as benchmark-scoped artifacts, where the schema's own rules decide the shape — a system principal may never author a Management artifact and only a Management artifact may hold a pin, so the evidence-bearing suite report is authored by the operator and the run records are Audit exhaust; identity is a ledger table with unique keys rather than a convention, a suite reserves the one report it may hold before that report is written and artifact creation enforces the reservation, a re-import is judged by a stable projection of everything the report claims rather than by a digest its minted identifiers make unrepeatable, the ledger row follows the record it names so retention still reaches an imported attempt, why an attempt yielded no calls is recorded where it was observed rather than reconstructed later from a store that may have changed, evidence bytes are found by walking the store rather than by trusting recorded absolute paths, the import boundary is the on-disk record contract guarded by a two-sided fixture instead of a module dependency, and the per-call facts the plane requires are recorded at their source by a v2 usage surface rather than reconstructed or defaulted at the seam."
type = "design"
+++

# Phase 2 Item 9 Design: Importing Golden Runner Records

Status: **live** — accepted by Codex and DR, 2026-08-08, after five review rounds on the design and three on the implementation. Round 1
approved the central shape and D5, required D10 with explicit conflict semantics,
**rejected** D2's null `scope_id`, and **rejected** D9's provider sentinel in
favour of recording it at its source. Round 2 found four further blockers,
including a self-review hole in the seam that D5's whole argument rested on, and
reversed the deferral of `cache_write_tokens`. Round 3 corrected the latency
contract, closed a mixed-version hole in the usage-log writer, reduced the timing
fields to one instant plus an exact duration, and carried the cached-token split
through the aggregate. Round 4 stopped failed calls fabricating zero token
measurements, deferred evidence upload to terminal assembly so a partial import
cannot lose it, closed a path-traversal hole in `run_id`, and added the cross-file
coherence the two-sided fixture cannot supply. Round 5 replaced a claim of
inherited per-record validation — unreachable code, run at write time — with a
two-sided conformance corpus, and added value validity to a surface that had only
defined presence. Resolutions are inline; all five dispositions are recorded at the
end.

**Three further rounds ran against the implementation**, and their outcomes are
the D7a–D7e amendments below rather than a separate disposition list — because
what they changed was the design, not only the code. Round 1 found six blockers,
three of which were amendments this document had already made badly: a
truncation predicate that made every imported run record permanent, a claim
written after the artifact it named, and a re-import check that could not see a
changed manifest. Round 2 found five more, including a caller-supplied
identifier that could defeat the claim protocol and a reservation the plane
never enforced. Round 3 found a migration default that would have converted
already-imported unavailable attempts into available ones, and required this
document to stop describing mechanisms it had replaced.

Delivers the [Phase 2 plan](plan_scope.md)'s item 9: golden story runner records
from `benchmark/runs/` imported into the main Postgres plane through the
persistence seam, as `benchmark`-scoped artifacts with their principal instances
and call records, including at least one object write with a digest reference and
a retention pin, then queried back. Append-only, idempotent by run/attempt
identity; a conflicting payload for an existing identity is rejected rather than
overwritten.

The seam, the registry, the object module and the commit-order invariant are
items 4, 5 and 6's; this records only what item 9 adds and what it discovered.

Naming follows the phase directory rather than ADR 0017's kebab-case slug rule,
as `design_local_stack.md` and its siblings already do.

## The honesty check, up front

The plan predicted this item would test whether "the artifact model cannot hold
data the runner already produces". It can hold it — but **not in the shape the
item's one-line description implies**, and the schema said so rather than the
design guessing. Four rules, all already merged, jointly fix the mapping:

1. **A system principal may never author a Management artifact.** Migration
   `000004`: system principals "produce Audit artifacts but can NEVER satisfy the
   Management review invariant, as author or reviewer" — per ADR 0019 they
   perform no inference, so there is no judgment to review. An importer is
   Orchestrator machinery and is therefore a system principal.
2. **Only a Management artifact may hold a retention pin.** Migration `000010`:
   `retention_pins.pinned_by_artifact_id` references `management_artifacts`.
3. **A pin may target an Audit artifact as well as an attachment.** Same
   migration, same exclusive arc.
4. **Acceptance requires a review by a non-author agent or human principal**
   (item 4's `ReasonReviewerIsAuthor`, `ReasonReviewerKind`).

Taken together: the importer cannot itself produce the artifact that holds the
pins the item requires. Something with judgment has to. That is not an obstacle
to route around — it is ADR 0020's review invariant doing its job on the first
real data to meet it, and D4 and D5 below take it as the design's spine rather
than as a special case.

The second finding turned out to be sharper than first written. The plane
requires per-call facts the usage log does not carry — `provider` above all — and
the first draft proposed a sentinel. Review rejected that, correctly: **the
runtime already knows the provider.** `metricsObserver.Observe` receives it as
`ev.Provider` and records only `ev.Model`
([`pkg/agent/factory_llms.go`](../../../pkg/agent/factory_llms.go)). It is an
instrumentation omission, not an unknowable fact, and a sentinel would have
frozen an omission into the contract. D9 upgrades the usage surface instead.

## What the runner produces

Per suite run, under `benchmark/runs/` (durable, git-ignored):

| Artefact on disk | Shape | Rewritten? |
| --- | --- | --- |
| `<suite>.jsonl` | one `runrecord.RunRecord` per attempt, schema-versioned | never; append-only |
| `<suite>.manifest.json` | `results.Manifest`: planned matrix, per-attempt status, budget accounts, stop reason | yes — it is status, not a record |
| `evidence/<run-id>/` | `pr.json`, `diff`, `maestro.db`, `usage.jsonl`, `logs/`, launch log | written once per attempt |

A `RunRecord` carries identity (`run_id`, `suite_run_id`, `story_id`,
`story_hash`, `config_name`, `config_hash`), a verdict with its failure kind or
invalid reason, timestamps, the four-state metrics map, validator and check
results, evidence pointers, the `TargetDescriptor` (adapter identity, commit,
binary identity, MPH signature, budget enforcement, capabilities) and the
isolation facts.

## D1. The import boundary is the on-disk contract, not the runner's Go types

The importer reads JSONL and JSON files. It does **not** import
`github.com/SnapdragonPartners/maestro/benchmark/...`.

ADR 0025's black-box rule forbids the runner depending on the orchestrator, not
the reverse, so a `require` plus `replace ./benchmark` would not violate it
directly. It is still refused: it would pull the runner module into the main
build, test and lint walkers it is deliberately outside of, and it would couple
the plane's build to a module whose whole point is that it can be versioned
against targets that do not exist yet. An importer is a reader of *files* that
happen to have been written by Go.

The cost of that is contract drift, and the drift alarm is **two-sided over one
committed fixture** rather than a hand-copied struct — hand-maintained
enumerations failed three times in item 8 alone:

- `benchmark/testdata/import_fixture/` holds one suite: a `.jsonl` of records
  covering accepted, failed and invalid verdicts, its terminal manifest, and a
  small evidence tree.
- A test **in the runner module** asserts the fixture round-trips through
  `runrecord`/`results` at the current `SchemaVersion`. Bump the schema and this
  fails until the fixture is regenerated.
- A test **in the orchestrator module** decodes the same fixture into
  `map[string]any` and asserts every key at every level is one the importer
  consumes. Regenerate the fixture with a new field and this fails.

Neither test can pass while the two sides disagree, and neither is a list
somebody has to remember to extend.

### Shape is not validity

The fixture proves the importer and the runner agree about the *contract*. It says
nothing about whether a particular file on disk is coherent, and the operator-
authored report is a claim about a real suite — so the importer validates the
input as a whole before any of it becomes an artifact, and refuses the suite rather
than importing part of it.

**Nothing is inherited, and the previous revision was wrong to say otherwise.** It
claimed `results.ReadSuite` runs `RunRecord.Validate` on every line so the
importer need not. Two things are wrong with that in one sentence. D1 forbids the
importer from importing that package, so it cannot call any of it — and even if it
could, that validation ran when the runner *wrote* the file. It is a fact about a
past process, not about the bytes on disk now. Anything that happened to the file
afterwards — truncation, a hand edit, a partial write, deliberate tampering —
happened after the only check that revision was relying on.

So the importer implements the **equivalent per-record semantics itself**: verdict
pairing (an accepted record cannot carry a failure kind, a failed validator or
check, or a missing terminal state), invalid records carrying a reason and no
failure kind, timestamp presence and ordering, the 40-hex shape of
`solution_commit`, metric completeness against its own copy of the registry,
capability coherence, and the isolation rule that unverified cleanup forces
`invalid`.

That is duplication, and duplication is what D1 spent its length avoiding — so it
gets the same treatment the record shape got, one level up. **The corpus is
two-sided and covers rejection as well as acceptance:**
`benchmark/testdata/import_corpus/` holds cases, each a record plus an expected
outcome — `accept`, or `reject` naming the rule. Both validators run every case
and **must agree on every one**. A rule tightened on one side and not the other
turns a corpus case red immediately, which is the property the single-file fixture
could not give: that fixture proves the two sides read the same *shape*, and this
proves they hold the same *judgment*.

Coverage is derived, not listed. The importer's rejection reasons are declared as
constants in one block, a test AST-walks that block, and every constant must be
exercised by at least one `reject` case. Hand-maintained enumerations have failed
this repository three times; a coverage list somebody has to remember to extend
would be the fourth.

**Added, because they are the ones no single record can see:**

| Check | Why it cannot be a per-record rule |
| --- | --- |
| filename, manifest `suite_run_id` and every record's `suite_run_id` are one value | a record knows its own suite id, not the file it was found in |
| `run_id` is unique across the suite | duplicates are how one attempt becomes two ledger rows, or one rejected import |
| every manifest `status` is `planned`, `completed` or `skipped` | the manifest has no validator at all today |
| the set of `completed` manifest entries equals the set of records, exactly | a completed entry with no record is a lost attempt; a record with no entry is a suite the manifest does not describe |
| a manifest attempt's `run_id` matches the record it names | otherwise the manifest's account of the matrix is fiction |

A suite that fails any of these is refused whole, with the specific disagreement
named. This is the difference between "the bytes parse" and "the evidence is
internally consistent", and the report is signed by an operator on the strength of
the second.

## D2. Two tables, because a suite run and an attempt are different identities

Item 9 is the item that adds `scope_benchmark_run_id`, as migration `000006`
already says it will. It adds two tables in migration `000017`.

The migration number moved: `000016` became the token-axis work D3 needs, which
is independent of the benchmark tables and landed at the surface-v2 checkpoint,
so the benchmark schema follows it rather than sharing it. Splitting them keeps
each migration reversible on its own, which a combined one would not have been.

**`benchmark_runs` — one row per suite run.** This is the scope target: every
artifact imported from one suite scopes to the same row, which is what
`ListManagementArtifactsByScope`/`ListAuditArtifactsByScope` then answer. Columns:
`benchmark_run_id uuid` (UUIDv7, app-generated), `organization_id`,
`suite_run_id text`, `first_imported_at`, and nothing that a later import would
have to update. `UNIQUE (organization_id, suite_run_id)`, and
`UNIQUE (benchmark_run_id, organization_id)` — the second is not redundant, it is
what the organization-aware scope foreign keys below reference, the same
`*_id_org_key` shape every other table in this schema carries.

**`benchmark_attempts` — one row per `RunRecord`.** The import ledger, and the
reason idempotency is a database property rather than a convention in the
importer's control flow: `UNIQUE (organization_id, benchmark_run_id, run_id)`,
plus `record_digest text NOT NULL` and `audit_artifact_id uuid NOT NULL`
referencing the Audit artifact the record became. Composite FK to
`benchmark_runs` and to `audit_artifacts`, both organization-aware like every
other reference in this schema.

One table would not do. The suite-level report needs a scope, an attempt-level
scope would leave it homeless, and folding the ledger's unique key into the
artifact tables would mean inventing a natural key column on the largest table in
the system for one caller's benefit.

The scope column lands on **both** artifact families, since both carry
`scope_type = 'benchmark'` in their existing CHECKs. For each of
`management_artifacts` and `audit_artifacts`, migration `000017`:

- adds `scope_benchmark_run_id uuid` with an organization-aware FK to
  `benchmark_runs`;
- drops and recreates `*_one_scope_check` and `*_scope_agrees_check` to include
  it. Migrations are append-only after merge (plan decision 4), so this is a new
  migration, never an edit to `000006`/`000007`;
- **rebuilds `scope_id`'s generated expression to include it.** The first draft
  proposed leaving it alone and letting benchmark rows carry a null `scope_id`.
  Review rejected that and was right: `scope_id` is not merely an index input.
  `toManagementArtifact` reads it as the domain scope id
  ([`artifacts.go`](../../../internal/dataplane/store/postgres/artifacts.go)), so a
  null would surface as `uuid.Nil` in `Scope.ID`, and
  `ListManagementArtifactsByScope` filters on `scope_id = @scope_id`
  ([`management_artifacts.sql`](../../../internal/dataplane/queries/management_artifacts.sql)),
  so benchmark rows would never be listed by the very query the scope exists to
  serve. A design that leaves the item's own artifacts unreadable through the
  seam's own reader is not an asymmetry to document, it is a defect.
- The `*_lineage_check` needs no change: its `ELSE` branch already requires all
  four work-lineage columns null, and the comment in `000006` already names
  benchmark alongside organization as the scopes with no work hierarchy.

The rebuild is `ALTER TABLE ... ALTER COLUMN scope_id SET EXPRESSION AS
(COALESCE(..., scope_benchmark_run_id))`, which PostgreSQL 17 added and the
pinned `postgres:18` image therefore has. **Measured, not assumed**, against the
pinned image digest: it rewrites the table so pre-existing rows are recomputed
under the new expression, and it preserves the dependent
`management_artifacts_scope_idx` rather than dropping it with the column, which a
`DROP COLUMN`/`ADD COLUMN` pair would have done silently. The migration's `down`
restores the five-column expression, and applying `up` then `down` then `up`
against a populated table is part of the migration test rather than an assertion
here.

## D3. What each record becomes

One suite run, imported:

| Source | Becomes | Family |
| --- | --- | --- |
| the suite run | one `benchmark_runs` row | — |
| each `RunRecord` | one Audit artifact, type `benchmark.run_record` | Audit |
| each `RunRecord` | one `benchmark_attempts` ledger row | — |
| each record's `TargetDescriptor` | one **agent** principal instance (the configuration under test) | — |
| each `usage.jsonl` line | one `llm_calls` row, opened and completed | Audit |
| each metrics entry with a value | one `metric_events` row | Audit |
| the import itself | one `tool_calls` row per suite, by the system importer | Audit |
| each evidence file | one object + one `binary_attachments` row | object store |
| the terminal manifest | one Management artifact, type `benchmark.suite_report`, holding every pin | Management |

Payloads are ADR 0028 envelopes; the two new types are registered by this item,
which is the registry's rule (a type is registered by the item that first writes
it). `benchmark.suite_report` registers an **extractor**, since it names evidence;
`benchmark.run_record` registers none, which is the registry's way of saying it
carries no evidence of its own and must therefore hold zero pins.

### The per-call mapping, stated column by column

An `llm_calls` row is not a subset of a usage line; it asks for facts the v1
surface does not record. The mapping below is what D9's surface v2 exists to make
honest, and every left-hand column is a field v2 records at the source rather
than a value the importer reconstructs.

| `llm_calls` column | Source in usage-surface v2 | Note |
| --- | --- | --- |
| `provider` | `provider` | `ev.Provider`, available today and dropped on the floor |
| `model` | `model` | unchanged from v1 |
| `input_tokens` | `input_tokens` | `Usage.InputTokens`, **nullable** |
| `output_tokens` | `output_tokens` | `Usage.OutputTokens` — **visible output only** |
| `reasoning_tokens` | `reasoning_tokens` | `Usage.ReasoningTokens` |
| `cache_read_tokens` | `cache_read_tokens` | renamed from `cached_tokens`, see below |
| `cache_write_tokens` | `cache_write_tokens` | new column, see below |
| `cost_usd` | `cost_usd`, **nullable** | absent means not knowable, never zero |
| `started_at` | **derived** at import: `finished_at - latency_ns` | the whole logical call, retries included — see D9 |
| `finished_at` | `finished_at` | the observation instant |
| `succeeded` | `success` | unchanged |
| `error_message` | `error` | absent on success |

Four of these are corrections, not additions:

- **A failed call has no token counts, and v1 records five zeros.** `Event.Usage`
  is populated only when `Err == nil`; on failure the toolkit hands over the zero
  value, explicitly because "a partial response returned with an error is not
  trusted". Writing those zeros into `NOT NULL DEFAULT 0` counters asserts a
  measurement nobody made, and every aggregate then sums them as though they were
  real. It is the same "unknown versus zero" confusion as `cost_usd`, one column
  over, and it needs the same answer.

- **v1 folds reasoning into `completion_tokens`.** `Observe` sets it from
  `Usage.BillableOutputTokens`, which per maestro-llms ADR-0016 is visible output
  *plus* reasoning. The plane stores the two separately, so importing v1 lines
  would have to either double-count or invent a split. v2 records all three axes
  and the fold disappears.
- **v1 records unknown cost as `0`.** When `config.CalculateCost` fails — an
  unpriced or local model — `Observe` logs a warning and leaves `cost` at its zero
  value, so a `paired-local` call is indistinguishable from a free one. This is
  precisely the "unknown versus free" confusion `llm_calls.cost_usd` is nullable
  to prevent, and item 5.1 already made the same distinction at the *record*
  level (`cost_usd: unavailable`); v2 carries it down to the call.
- **v1 records no start time and no failure diagnostic.** `ts` is the observation
  instant and `Latency` is discarded, so `started_at` would have to be
  reconstructed; and a failed call's error text never reaches the log at all,
  though `llm_calls.error_message` is exactly where it belongs.

**Cache reads and writes get a column each, now.** `Usage` carries
`CacheReadTokens` and `CacheWriteTokens` separately and they are billed
differently; the plane had one `cached_tokens` column whose name did not say
which. The first revision proposed deferring the second column to a measurement
against today's golden configs. Review rejected the reasoning and it is worth
recording why, because it is a trap this repository has fallen into before: an
all-zero result would not show the dimension is irrelevant, only that **this
sample never exercised it**. That is the same shape as a green suite standing in
for a property nobody asserted.

So migration `000016` renames `cached_tokens` to `cache_read_tokens` and adds
`cache_write_tokens bigint NOT NULL DEFAULT 0` with the same non-negative check.
Renaming rather than documenting the old name: the ambiguity is in the name
itself, there is no deployed data to protect, and a column called `cached_tokens`
sitting beside `cache_write_tokens` would invite exactly the wrong reading
forever. `store.TokenCounts.Cached` becomes `CacheRead` and `CacheWrite` to
match. The blast radius is item 5's — the query file, the generated output, the
seam struct, the converter and three test files — and it shrinks every week this
is deferred.

**Token availability, carried through all four layers.** Nullability is the
plane's existing idiom for "not knowable" and it is what the token axes get:

- **Surface.** The five token fields are required iff `success` and omitted
  otherwise, so a failed line does not carry numbers the toolkit never produced.
- **Row.** Migration `000016` drops `NOT NULL DEFAULT 0` from all five columns and
  adds `CHECK (num_nonnulls(input_tokens, output_tokens, reasoning_tokens,
  cache_read_tokens, cache_write_tokens) IN (0, 5))` — availability is a property
  of the observation, so four axes present and one missing describes nothing.
- **Seam.** `CompleteLLMCallInput.Tokens` and `LLMCall.Tokens` become
  `*TokenCounts`; nil is unmeasured. A pointer rather than a bool beside a struct,
  because a struct that must not be read is one somebody eventually reads.
- **Aggregate.** The token sums filter on availability, and `CostAggregate` gains
  `TokensMeasuredCalls` and `TokensUnmeasuredCalls` beside the existing cost pair.
  They are genuinely two axes and not one: a `paired-local` success has measured
  tokens and unknowable cost, a failed call has neither, and one pair of counters
  cannot say that.

**What this does not fix, stated plainly.** Tokens spent on the provider attempts
*inside* a failed logical call are invisible to this surface — the toolkit reports
no usage for them at all. ADR 0025 says failed-attempt costs count, and today they
cannot be counted, so budget enforcement under-counts a failed call. That is
pre-existing v1 behaviour and v2 does not change it; what v2 stops doing is
*claiming a measurement of zero* where there was no measurement. The real fix is
upstream in `maestro-llms`, alongside the per-attempt latency gap; both are filed
as [issue 311](https://github.com/SnapdragonPartners/maestro/issues/311). The budget-total equality of D9 is unaffected: v1 added zeros
for failed calls and v2 adds nothing, which is the same total.

**The cached-token split runs all the way through the aggregate, not just the row.**
`AggregateLLMCost` sums one `total_cached_tokens`
([`llm_calls.sql`](../../../internal/dataplane/queries/llm_calls.sql)) into a
`CostAggregate` that embeds `TokenCounts`
([`calls.go`](../../../internal/dataplane/store/calls.go)), so splitting the column
while leaving the rollup alone would produce a per-call answer the totals
contradict — and the totals are what anyone actually reads. So the change is
required at every layer explicitly: the SQL gains
`total_cache_read_tokens` and `total_cache_write_tokens`, the generated row and
the converter carry both, `CostAggregate.Tokens` is the renamed `TokenCounts`, and
a test asserts each total equals the sum of its own axis over a fixture whose two
axes carry **different** values — identical values would let a mapping that reads
the wrong column pass.

## D4. Three principals, and only one of them is the importer

- **The importer** is a `system` principal instance, `model = 'system-benchmark-importer'`,
  one per import invocation. It authors every Audit artifact and makes the tool
  call. It may author nothing else, by rule 1 above.
- **The configuration under test** is an `agent` principal instance, **one per
  attempt**, carrying the record's MPH signature into the columns built for it:
  `model`, `prompt_pack_id`, `prompt_hash`, `harness_config_hash`,
  `maestro_version`, with `agent_type = 'benchmark-target'`, `start_time` and
  `stop_time` from the record and `stop_reason` from its verdict. This is what
  makes `FindPrincipalInstances` answer "which runs used this prompt hash?" —
  the question ADR 0021 built the MPH columns for, asked for the first time here.
- **The operator** is a `human` principal instance, resolved from a required
  `--operator` handle. It authors the suite report (D5).

**What is lost in translation, stated rather than smoothed over:** a v1 attempt
runs an architect *and* a coder, and the record carries one MPH signature for the
configuration, not one per internal agent. The import therefore produces one
principal instance per attempt, not per agent. That is the runner's contract, not
a shortcut here — the record has nowhere to put the second signature — and a
future adapter that reports per-agent MPH would map to per-agent instances with
no schema change.

### D4a. A lifetime already over is written closed, never opened and then stopped (amendment)

*Proposed during implementation, 2026-08-05; found by Codex review of the mapper.*

D4 requires `start_time` and `stop_time` from the record and `stop_reason` from
its verdict, and the seam as item 4 shipped it **cannot express any of that**:
`CreatePrincipalInstance` opens a lifetime and `StopPrincipalInstance` closes one
later, with no way to supply the stop *time* at all. The first implementation
therefore created the target with neither, so `start_time` defaulted to `now()` —
dating every attempt ever imported to the moment of the import — and the instance
stayed open, leaving every configuration under test looking like it was still
running. The MPH query D4 exists to serve would then have been answering a
question about the importer rather than about the runs.

**The seam gains a closed lifetime at creation**, `CreatePrincipalInstanceInput.
Recorded`, written in the same `INSERT` as the instance. Not a convenience over
create-then-stop: a pair of statements makes the row observable OPEN for the
width of a statement, and a reader in that window sees a live agent that finished
before the import began. The seam validates what the schema cannot — the zero
time is year 1 and sorts before every window a query could ask about, and a stop
before its start is a lifetime that ran backwards — because the schema's only
stop constraint is that time and reason are null together.

**`stop_reason` carries the verdict, plus the failure kind where the record has
one:** `accepted`, `invalid`, and `failed: <failure-kind>` over the record's six
kinds — `budget-overrun`, `checks-failed`, `validator-failed`,
`evidence-missing`, `branch-state`, `target-error`. Closed vocabularies only.
The column sits beside the MPH columns and is therefore read by grouping — *which
runs on this prompt hash failed, and how?* — and `invalid_reason` is free text
written by whatever refused the attempt, so it stays out and is read from the
run-record artifact, which carries the record whole.

**The importer's own instance closes when the invocation ends**, including the
failing end, where it has stopped acting just as certainly. D4 already says one
instance per import invocation; an invocation that never ends is not one.

**That closing runs on a context detached from the caller's**, bounded by its
own deadline. Cancellation and deadline expiry are the most likely ways an
import fails, and they are exactly the cases where the caller's context can no
longer carry a write — so a cleanup reusing it leaves the instance open in
precisely the situation the closing exists for, while looking correct in every
test that does not cancel. The rule generalizes past this call: any write whose
purpose is to record how an operation ENDED cannot depend on the context whose
ending it is recording.

## D5. The suite report is authored by the operator, and a draft is a legitimate outcome

The evidence-bearing artifact is `benchmark.suite_report`: one per terminal suite
run, Management family, scoped to the `benchmark_runs` row, payload carrying the
manifest, the per-story verdicts and the references it pins.

**Author: the operator's human principal.** Not because the importer needs a
proxy, but because the artifact makes a claim — *this suite ran, and this is what
it showed* — and the plane requires an author who could be reviewed. The
machinery link is not lost: `produced_by_tool_call_id` names the import tool call
made by the system importer, which is exactly the guardrail migration `000005`
describes (a state change passes through a tool record) and is how a reader tells
an assembled report from a hand-written one.

**The draft IS item 9's outcome.** `AttachEvidence` writes the attachments,
then the draft artifact and its complete pin set in one transaction — the
cross-store commit order, exercised end to end, object write and digest
reference and retention pin included.

**Acceptance is not in item 9 at all** (amended, 2026-08-07). The original
draft specified a `benchmark accept` verb here; the item ships assembly and
stops. Acceptance requires a reviewer who is not the author, and ADR 0020 makes
that a non-author **agent or human** principal — the invariant is
non-authorship, not humanity. The reviewer this report waits for is expected to
be the `benchmark-evidence-reviewer` agent recorded against GitHub #282, after
Phase 2. Shipping the verb with nobody to run it would have invited exactly the
manufactured reviewer ADR 0020 exists to prevent, so `show` prints
`DRAFT — UNREVIEWED — NOT AUTHORITATIVE` instead, and the draft's pins hold its
evidence in the meantime.

### The seam lets a human self-review, and this item closes it

D5 leans the whole review invariant on the seam's author-versus-reviewer check,
so the check had better hold. It does not. `classifyAcceptance` compares
**principal instance ids only**
([`artifacts.go`](../../../internal/dataplane/store/postgres/artifacts.go)), and a
principal instance is one *lifetime*: the same human running the importer twice
has two instances, and can therefore author with one and accept with the other.
The invariant reads as enforced and is not.

ADR 0020 is explicit about the identity that matters — "every user account gets a
principal instance record whose `model` is `human-<user_id>` … even the human
operator does not self-review — a human may be an artifact's author or its
approver, never both". Migration `000004` already encodes it. So the fix is to
compare the identity the ADR names, not the row id:

**Reject when the reviewer instance is the author instance, or when author and
reviewer are both `human` principals with the same `user_id`.** A new rejection
reason, `ReasonReviewerIsAuthorUser`, because the operator response differs —
"use a different instance" is wrong advice and "find a reviewer who is not this
author" is right. That reviewer need not be a human: ADR 0020 requires a
non-author *agent or human* principal, so the invariant is non-authorship rather
than humanity.

Two boundaries drawn deliberately:

- **It does not extend to agents sharing a model.** ADR 0020 makes distinct
  reviewer model routing a preference — "*where practical*", an M lever and a
  Phase 5 deliverable — not the invariant. Refusing two `claude-opus-5` instances
  here would over-enforce a Phase 5 policy in a Phase 2 constraint.
- **It compares the author *principal's* user, not the artifact's `user_id`.**
  Those are different facts: `management_artifacts.user_id` is the accountable
  human behind the work, so an agent-authored artifact legitimately carries the
  operator there and the operator must still be able to review it. Comparing
  against that column instead would forbid the single-operator workflow ADR 0020
  explicitly endorses.

Mechanically, `GetArtifactReviewWithReviewer` gains the author principal's kind
and user id alongside the reviewer's, so one statement answers the whole question.
**Not because the row lock protects them** — it does not; the lock is on the
artifact row, and a joined `principal_instances` row is outside it. They are safe
for a different reason: a principal's `kind` and its `user_id` ownership are
immutable for the instance's life, so there is no later value for a second read to
observe. Only mutable facts need the lock, and these are not among them. Folding
them into the existing statement is about having one round trip and one obvious
place to look, not about atomicity. The condition is repeated in the `UPDATE` as a
backstop, as item 4's design already does for every other acceptance rule.

This is an item 4 defect and it is fixed here rather than filed, because item 9 is
the first caller whose correctness depends on it, and a filed issue would leave
D5's acceptance step demonstrably bypassable in the meantime.

**What the report pins:** every evidence attachment, and every `benchmark.run_record`
Audit artifact of the suite. The second is the point of pins targeting Audit
artifacts — Audit is truncatable by design, and a conformance claim whose
underlying records can be pruned is a claim that decays. Acceptance verifies the
pin set against the set the extractor reads from the reviewed payload, so an
extra pin is an unreviewed retention claim and fails.

## D6. Identity, conflict, and what a retry does

Idempotency is by `(organization_id, suite_run_id)` and
`(organization_id, benchmark_run_id, run_id)`, enforced by the unique constraints
in D2, not by a check-then-insert.

- **Attempt already ledgered, same `record_digest`:** skipped. Re-import is a
  no-op.
- **Attempt already ledgered, different digest:** rejected with a typed
  `ErrImportConflict` naming the run id and both digests. Records are append-only
  on disk and are never rewritten, so a differing digest is corruption or
  tampering, and overwriting it would erase the evidence of that.
- **Attempt not ledgered:** written. The principal instance, the Audit artifact,
  the calls, the metric events and the ledger row go in **one transaction**. Not
  for tidiness: an artifact committed without its ledger row would be imported
  again on the next run, silently duplicating it.
- **Partial import:** every attempt already ledgered is skipped and the rest
  proceed. Nothing needs manual repair, and nothing is repaired by mutation.

The attachments cannot join a transaction — `PutAttachment` is a remote write
followed by its own committed row — so a failed import can leave unreferenced
attachment rows. That residue is unreferenced rather than dangling, and item 6's
truncation-then-sweep reclaims it. This is the same property `AttachEvidence`
already documents, inherited rather than re-argued.

`record_digest` is the JCS digest of the envelope, computed by the same seam
machinery item 4 uses. Not a digest of the raw JSONL line: whitespace is not
content, and two byte-different serializations of one record are one record.

### D6a. The identity payload carries the record and nothing else (amendment)

*Proposed during implementation, 2026-08-05; found by Codex review of the mapper.*

`record_digest` is the digest of the artifact payload, so **everything in that
payload is identity**. The first implementation added the results-store
directory to it as provenance — where these bytes came from — which put a local
filesystem path inside the identity of an attempt. The store is portable by
design (D8), and the paths that reach it are not: the same unchanged store
imported after being moved, copied to another machine, or reached through a
symlink or a differently-spelled path would offer a different digest for an
identity already ledgered, and D6's tampering check would fire on bytes that
never changed. The mechanism built to detect a rewritten record would have been
reporting relocation.

**The rule, stated so the report cannot repeat it:** a payload whose digest is an
identity contains only what the thing IS. Provenance about the import —
where it ran, which store it read, when — belongs to the import's own tool call,
which records the invocation and is not an identity. This binds the suite report
as much as the run record: D7 checks a re-imported suite's report for
payload-digest agreement, so a store locator in *that* payload would resurrect
the same defect one layer up.

Nothing is lost that the plane did not already hold. The suite the attempt
belongs to is in the record and in the ledger's `benchmark_run_id`; what the
directory added was the one fact that varies without the attempt varying.

## D7. Only a terminal suite gets a report, which is what makes a suite re-importable

The manifest is **status, not a record** — `WriteManifest` replaces it atomically
on every update. So its digest is not an identity, and storing it as one would
make the common case a conflict: import a suite mid-flight, import it again when
more attempts have landed, and the manifest has legitimately changed.

The rule instead: **attempts import at any time; the suite report is written only
when `stop_reason != "running"`.** `benchmark_runs` therefore stores no manifest
digest and nothing an import would ever update — it is created once and read
thereafter. A suite imported while running acquires its report on the later
import that finds it terminal; a suite that already has one is checked for
payload-digest agreement and otherwise skipped, by the same rule as an attempt.

An interrupted or budget-exhausted suite *is* terminal and does get a report.
"Deliberately partial" is a real outcome the manifest is designed to express, and
refusing to report it would discard exactly the distinction it exists to make.

**Evidence upload waits for terminal assembly, and the rescan covers every
attempt.** The first revision had the partial import upload attachments as it went.
That loses evidence, and the loss is silent: an attachment written during a partial
import is held by no artifact, because the only artifact that pins anything is the
report and the report does not exist yet. Attachment truncation may remove the row
and the sweep may then reclaim the object — legitimately, since nothing
authoritative referenced it — and the terminal import will not put it back,
because the attempt is already ledgered and gets skipped. The gap between the two
imports is exactly as long as the suite takes to finish.

So attachments are written **only** during terminal report assembly, and that
assembly walks the evidence directory of **every attempt in the suite, ledgered or
newly imported**. A ledgered attempt is skipped for its *artifact and call rows*,
which are append-only and already correct; it is never skipped for its evidence,
which has no holder until now. The two skip rules are separate for a reason and
conflating them is what caused this.

This also simplifies the failure story: a partial import writes no objects at all,
so the unreferenced-attachment residue D6 describes can only arise from a failed
*terminal* import, in one place, on one path.

### D7a. A report's re-import check is over a stable projection, not its payload digest (amendment)

*Proposed during implementation, 2026-08-06; found by building the report.*

D7 says a suite that already has a report is "checked for payload-digest
agreement and otherwise skipped, by the same rule as an attempt". **That check
cannot be made.** The report payload names the attachment ROWS it pins — a pin
targets a row and a digest cannot name one — and those identifiers are minted at
assembly. A second assembly of an unchanged suite therefore produces different
bytes, and a different digest, every time. Implemented as written, the ordinary
re-import of a reported suite would have been a permanent conflict.

This is D6a's rule arriving one layer up, exactly where D6a predicted it would:
a payload used as an identity must contain only what the thing IS, and an
attachment id is minted by the import rather than being a property of the suite.

**The rule as it now stands: an existing report is compared against the report
the store would produce NOW, over a stable projection** — everything the payload claims,
with only the minted attachment identifiers normalized away. Agreement is a
no-op; disagreement is `ErrReportStale`.

The first version of this amendment compared only the ATTEMPTS the report
covered, and review was right that it was too little: the report quotes the
terminal manifest, so a stop reason, budget account, cell status or timestamp
could change while every attempt stayed identical, and the import would call
that a no-op with the stored report describing a suite that was no longer the
suite. The same held for an evidence file whose bytes changed under a pinned
digest. The attempt-level diff survives only to say WHICH attempts moved: a
projection digest can report that something changed, and an operator needs to
know what. The cost is a rescan on the no-op path, since deciding whether
anything changed means assembling the candidate.

What D7 was protecting is protected better, not merely differently. The
projection is over values that do not change when the store moves — the
attachment identifier is the only field that does, and it is exactly the field
normalized away — which is the property D6a requires of anything used as an
identity. And it covers the whole claim rather than one part of it: an
attempt's own identity is still its record digest, which D6 refuses to see
change long before assembly runs, but the manifest and the evidence are claims
too.

Two consequences worth stating:

- **A stale report is refused, not amended.** An attempt imported after its
  suite was reported leaves the report describing a suite that is no longer the
  suite. The remedy is an amendment, which is acceptance's neighbour and is not
  in this item; until then the import refuses rather than leaving a decayed
  claim in place or writing a second report beside it.
- **The ledger and the store must agree before a report is assembled at all.**
  A ledgered attempt whose record is no longer in the JSONL means the file was
  rewritten — the one thing D6 establishes never happens to an append-only file
  — and a report assembled over it would sign a claim about records nobody can
  read. That is `ErrLedgerDiverged`.

### D7b. One report per suite is a plane invariant, enforced at creation (amendment)

*Proposed during implementation, 2026-08-06; found by extending the item's own
concurrency test to the report.*

Assembly reads for an existing report and writes one when it finds none, and
those are **two statements**. Two imports of the same terminal suite can both
read nothing and both write, leaving two draft reports for one suite — each
independently acceptable, each pinning its own copy of the evidence, and the
second reviewer with no way to know they were accepting a duplicate. The plane
would then hold two authoritative accounts of one conformance run, which is the
state `SupersedeArtifact` exists to prevent for every other subject.

ADR 0027's rule is that shared state is serialized on a key matching the
resource. The key is the suite, so migration `000018` adds `benchmark_reports`:
one row per `(organization, benchmark run)`, born final, naming the Management
artifact that IS the report. The uniqueness is the arbiter, the same shape as
the attempt ledger and for the same reason.

**The claim is recorded BEFORE the artifact it names** (amended after review).
The first version wrote the artifact and claimed it afterwards, because a
foreign key required the referent to exist — which left a window with no owner:
a death between the two commits leaves a live, fully pinned draft that no claim
names, and the retry writes and claims a second one. A post-write unique row
narrows that window; it does not close it.

Reversing the order closes it. **The claim seam allocates the identifier**,
records the reservation, and returns it; the caller writes the artifact under
what it was given. The importer proposes nothing — a caller-supplied identifier
carries an invariant with it, and both ways of getting it wrong defeat the
protocol: the nil UUID is read by artifact creation as "allocate one", which
produces an artifact the claim does not name, and a non-v7 is refused by
artifact creation outright, which strands the reservation on an id nothing can
ever be written under.

The only inconsistent state left — a reservation whose artifact does not exist
*yet* — is self-healing rather than ambiguous: the next import finds it, sees no
artifact, and writes under the id already reserved. There is never a second
artifact to choose between, so there is nothing to withdraw.

The cost is the foreign key, which cannot survive an intent recorded before its
referent. That is the shape the object module already uses for destructive work
— ADR 0022's deletion claim is a durable intent naming storage that may already
be gone. Its down migration therefore has to DISCARD pending reservations before
restoring the constraint, or 000020 would be reversible only on planes that
never used the feature.

**And the reservation is enforced at creation, not merely consulted by the
importer** (amended after review). A rule the importer follows is a convention:
generic Management-artifact creation never looked at the reservation, so another
caller could create a valid, fully pinned report beside the reserved one and
accept it. Creation now refuses a benchmark-scoped Management artifact its scope
did not reserve — expressed over the SCOPE rather than the artifact type, so the
seam learns no name from the registry's vocabulary. Amendments are exempt: an
amendment inherits its original's scope, every pin in a chain is held by the
original, and requiring a reservation of one would forbid amending a report at
all.

The reservation is also what `show` and the re-import check read. Scanning the
scope for a Management artifact of the right type would answer with whatever it
found.

### D7c. The ledger's reference cascades, so retention still reaches an imported record (amendment)

*Proposed during implementation, 2026-08-06; found by a mutation run whose
mutant died for the wrong reason.*

`benchmark_attempts.audit_artifact_id` originally referenced `audit_artifacts`
`ON DELETE RESTRICT` — it cascades now, which is where this amendment ends up.
Item 5's truncation query already states the principle for the other such
reference — "retention_pins references audit_artifacts ON DELETE RESTRICT … the
predicate is what turns that protection from an aborted batch into a counted,
reported outcome" — and item 9 added a second reference without a second
predicate.

The consequence was not a retained row. It was an **aborted pass**: any
organization holding an unpinned imported run record made `TruncateAuditBefore`
fail with SQLSTATE 23001, and audit retention stopped working for that tenant
entirely. Unpinned imported run records are not an edge case — they are every
suite imported while it was still running, since the report that pins them does
not exist until the suite stops.

The first fix — a predicate excluding ledgered records from the pass — is
WITHDRAWN, and nothing in the truncation query mentions `benchmark_attempts`
any more. Review was right that it fixed the symptom in the wrong direction: excluding ledgered records from the pass made
every imported run record **permanent**. Nothing deletes a ledger row, so
nothing could ever delete the artifact it named; invalidating or archiving a
report released its pins and not its records, and Audit retention stopped
applying to anything that had ever been imported.

**So the reference CASCADES** (migration `000019`). A ledger row is a claim that
an attempt was imported, and when retention removes the artifact that claim
describes a row which no longer exists — a tombstone that would make the next
import a no-op restoring nothing. Truncation deletes an unpinned record past the
horizon and takes its ledger row with it; a later import recreates both. A
record a report pins is excluded before any of this applies.

`retention_pins` keeps `ON DELETE RESTRICT`, and the asymmetry is the point: a
pin is a live retention claim by an artifact that cites the evidence, while a
ledger row is bookkeeping about an import and follows what it describes.

**How it was found is the part worth keeping.** The truncation test was written
to assert that a draft's pins hold, and a mutation that removed the report's
run-record pins did kill it — with a foreign-key error, not with a missing
record. A mutant that dies for the wrong reason proves nothing about the test,
and in this case it was reporting a defect somewhere else entirely.

### D7d. The report's quoted manifest is held to the suite's own rules (amendment)

*Proposed during review, 2026-08-07.*

The registered validator checked that the manifest named the right suite and had
stopped, and nothing else about it. Unknown schema version, a stop reason
nothing can interpret, a cell naming no story, a completed cell the report does
not account for, an attempt the manifest never planned, or a manifest and a
report naming different stories for one run: all of it could be written through
the seam as a reviewed Management claim that contradicts itself.

The reader already had these rules and could not enforce them here, because they
lived on `Suite` over records read from disk — and a caller reaching the seam
directly never passes the reader. They are now shared, expressed over the
story/config pair both accounts duplicate, and run at both boundaries. Sharing
them is what makes the two agree by construction rather than by two
implementations happening to be written the same week.

### D7e. The recorded absence is stored with the attempt that observed it (amendment)

*Proposed during review, 2026-08-07.*

`calls_unavailable` was carried from the import invocation's own outcomes, and
an attempt ledgered by an EARLIER import short-circuits before the usage log is
opened at all. So a surface-v1 attempt — or one whose evidence carries no usage
log — imported while the suite was still running appeared in the terminal report
as one whose calls had been read. That is the zero D9 exists to prevent arriving
by a different route: an absence that disappears reads exactly like a
measurement.

The first fix re-read every attempt's usage log at assembly, and review was
right that it answers a different question. An attempt whose calls WERE read,
whose evidence is pruned before the suite finishes, re-reads as "no evidence
directory" — so the report denies the `llm_calls` rows the plane holds for it.
The reverse is possible too, and a read failure additionally embedded absolute
store paths in a payload D6a keeps portable.

**So the observation is written where the attempt is**: `calls_unavailable` on
the ledger row, in the same transaction and from the same read that decided
whether to write call rows at all. A measurement and the reason it is absent
belong to the moment of measurement.

The column is NOT NULL with **no default**, and it lives in the ledger's own
migration (`000017`) rather than a later one. A default would let a writer omit
it and be handed an answer it never observed — and the answer an empty default
gives, "the calls were read", is precisely the one that fabricates a
measurement. Adding it later with that default would have converted every
already-imported surface-v1 attempt from unavailable into available. Folding it
into the ledger's own migration removes the conversion rather than describing
it: no row can predate the column, so there is no historical unknown to invent a
meaning for.

## D8. Evidence bytes are found by walking the store, not by trusting the record

`EvidencePointer.Location` is an **absolute filesystem path recorded on the
machine that ran the attempt**. It is faithful provenance and a poor locator: the
results store is portable and those paths are not.

The importer therefore walks `<results>/evidence/<run-id>/` — the store's own
durable layout, which `results.EvidenceDir` defines — and uploads every regular
file it finds, recursively, with the pointers carried in the payload verbatim as
provenance. Media type is by extension against a small explicit table, defaulting
to `application/octet-stream`; unknown is not a failure.

**`run_id` is untrusted input used as a path component.** `validateIdentity`
requires only that it be non-empty, so a record carrying `../../../etc` makes
`evidence/<run-id>` resolve outside the results root — and the importer reads
whatever is there and uploads it into the plane. The exposure is not only the
importer's: the engine joins the same value into `WorkspaceDir` and passes it to
`EvidenceDir`. Three defences, because one of them is a string check and string
checks are where this class of bug lives:

1. **Shape.** The importer requires `run_id` to be a single path component
   matching `[a-z0-9][a-z0-9_-]*`, which every id the engine generates already
   satisfies (`app-healthz-endpoint--paired-default--r1--1e29828c`). `.` and `..`
   are excluded by construction rather than by being listed.
2. **Containment after joining.** The resolved directory is compared against the
   resolved results root and must be a proper descendant, separator included, so
   `evidence-other` cannot pass a prefix test for `evidence`. This is checked
   *after* symlink resolution, not before, so the check sees what the filesystem
   will actually open.
3. **No symlink following during the walk.** Entries are examined with `Lstat`;
   a symlink is skipped and named in the summary, never followed and never
   uploaded. A containment check on the directory says nothing about a link
   pointing out of it three levels down.

The runner's own `validateIdentity` gains rule 1 as well. It is not the security
boundary — the runner generates these ids and never reads a hostile record — but
it is one line, it makes the fixture prove the rule on both sides, and it closes
the `WorkspaceDir` join at its source. Existing records on disk already comply, so
nothing in `benchmark/runs/` becomes unreadable.

Two bounds, both **reported rather than silently applied**, because a cap that
drops work quietly reads as "there was nothing more to import":

- a per-file size cap (default 256 MiB, flag-adjustable) — a v1 `maestro.db`
  snapshot is the realistic candidate to trip it;
- a per-attempt total cap.

Anything skipped is named in the import summary *and* in the suite report's
payload, so the artifact records what it does not contain.

An attempt with no evidence directory is imported without attachments. The item
requires *at least one* object write per import, which every real suite satisfies;
an import that would produce none is reported as such rather than failing.

## D9. Usage surface v2: record the facts at their source

The v1 usage line is `{ts, story_id, agent_id, model, prompt_tokens,
completion_tokens, cost_usd, success}`. Everything D3's table needs beyond that is
available in `middleware.Event` at the moment of observation and is discarded:
`ev.Provider`, the four token axes on `llms.Usage`, `ev.Latency`, and `ev.Err`.

So item 9 raises the usage surface to **v2** rather than defaulting at the seam.
The rejected alternative was a `ProviderUnrecorded` sentinel, which would have
written an instrumentation omission into the plane's permanent record and made
every later provider-partitioned query quietly wrong.

Concretely:

**v2 is an exact replacement schema, not v1 plus fields.** The first revision
called it "additive apart from `cost_usd`", which left it ambiguous whether
`prompt_tokens` and `completion_tokens` survive beside their replacements. They do
not: `completion_tokens` is the folded value the plane cannot use, and keeping it
would leave two answers to one question in the same line. Round 2's key list had
the same fault in a subtler place — it carried `ts`, `finished_at`, *and*
`started_at` plus `latency_ms`, which is three answers to "when". v2 records **one
instant and one duration** and derives the rest at import:

| Field | Type | Presence | Source |
| --- | --- | --- | --- |
| `finished_at` | RFC 3339 UTC, nanoseconds | always | the observation instant |
| `latency_ns` | int64 | always | `ev.Latency.Nanoseconds()` |
| `provider` | string | always | `ev.Provider` |
| `model` | string | always | `ev.Model` |
| `input_tokens` | int64 | **iff `success`** | `Usage.InputTokens` |
| `output_tokens` | int64 | **iff `success`** | `Usage.OutputTokens` |
| `reasoning_tokens` | int64 | **iff `success`** | `Usage.ReasoningTokens` |
| `cache_read_tokens` | int64 | **iff `success`** | `Usage.CacheReadTokens` |
| `cache_write_tokens` | int64 | **iff `success`** | `Usage.CacheWriteTokens` |
| `success` | bool | always | `ev.Err == nil` |
| `cost_usd` | float64 | omitted when unpriced | `config.CalculateCost` |
| `error` | string | required iff `!success` | `ev.Err.Error()` |
| `story_id` | string | omitted when empty | state provider |
| `agent_id` | string | omitted when empty | state provider |

`latency_ns`, not `latency_ms`: `ev.Latency` is a `time.Duration`, so milliseconds
would round and `started_at` could not be recovered from what was written.
Nanoseconds are exact, and `started_at` is computed at import rather than stored,
so there is no second copy to disagree with the first.

**The coherence rule, enforced on write and on read.** `success` true requires
`error` absent **and all five token fields present**; `success` false requires
`error` present and non-blank **and all five token fields absent**. A line that
says a call failed and does not say how is not a record of a failure; a line that
says it succeeded while carrying an error text is two claims; and a failed line
carrying token counts is the fabricated measurement D3 exists to prevent — the
toolkit gave it nothing to report. The writer refuses to emit any of them and the
importer refuses to accept any of them, the same rule in both places, since the
writer's guarantee is not evidence to a reader parsing a file written by some
other build. Unknown keys and missing required ones fail rather than being guessed
past, which is what the header version is for.

**Presence is not validity, and every reader checks values.** A key being present
says nothing about what is in it, and both readers of this surface — the budget
tail and the importer — act on the numbers. Each rejects:

| Rule | Why it is not paranoia |
| --- | --- |
| `provider`, `model` non-blank after trimming | blank `provider` is what D9 exists to prevent, arriving by a different door |
| every token axis `>= 0` | see below; this is the one with teeth |
| `latency_ns >= 0` | a negative duration makes `started_at` later than `finished_at`, which the row's own ordering then refuses at the far end of the import |
| `cost_usd >= 0`, finite | NaN or ±Inf propagates through every sum it touches and `numeric` cannot store it |
| the token sum does not overflow int64 | a wrapped sum is a small positive number that looks ordinary |
| `finished_at` present, parseable, non-zero | the zero time silently backdates a call to year 1, where it sorts before every window |

**The negative-axis case is a live accounting bypass, not a hypothetical.**
`usageTail` computes `tokens := entry.PromptTokens + entry.CompletionTokens` and
reports that sum; `usageTracker.report` guards `delta.Tokens < 0`
([`attempt.go`](../../../benchmark/engine/attempt.go)) — but it sees only the sum.
A line carrying `{input: 1000000, output: -999999}` yields a delta of 1, passes the
guard, and under-accounts the attempt by two million tokens. The cap is not
enforced; the record's canonical totals are wrong; nothing reports anything. The
guard is not weak, it is simply downstream of the arithmetic that destroys the
evidence, so **the axes must be checked individually, before they are summed**.

Two further gaps in the same function, fixed with it: the tail's own totals —
which its header comment calls "the canonical tokens/cost/llm_calls" — accumulate
with no overflow guard and no NaN/Inf guard, though `usageTracker` carefully
saturates both for its copy. A malformed cost the tracker rejects still lands in
the record.

**And the tail returns an error rather than discarding.** `usageTracker.report`
handles a malformed delta with a bare `return`: correct for it, since a budget
guard's job is to not be walked backwards. It is the wrong response one layer up.
A malformed usage line means the accounting is no longer trustworthy, which is
exactly the condition the `usage.error` sentinel and its process-abort escalation
exist to make loud. `advance` already returns an error for a bad header and a
bad JSON line; a line that parses into impossible numbers gets the same treatment,
and the attempt fails rather than completing with quietly wrong totals.

- `metrics.UsageEntry` is rewritten to that shape; `cost_usd` becomes `*float64`,
  omitted when `CalculateCost` fails, so absent means unpriced rather than free.
- `metrics.Recorder` takes an **`Observation` struct** rather than growing
  `ObserveRequest` from seven parameters to thirteen. Three non-test
  implementations exist (`InternalRecorder`, `NoopRecorder`, `UsageLogRecorder`),
  and `InternalRecorder`'s aggregates are computed from the struct so its
  existing consumers are unchanged.
- `metrics.UsageSurfaceVersion` becomes 2. It is advertised by `maestro -version`
  ([`cmd/maestro/main.go`](../../../cmd/maestro/main.go)) and written as the log
  header, and the v1 adapter validates it in both places
  (`benchmark/target/v1target/usagetail.go`), so the handshake fails loudly
  against a mismatched target instead of mis-parsing.
- **The writer validates the header it is about to append beneath.**
  `NewUsageLogRecorder` writes a header only when the file is empty and otherwise
  appends blind
  ([`usagelog.go`](../../../pkg/agent/middleware/metrics/usagelog.go)), so the
  moment `UsageSurfaceVersion` changes, a stale `.maestro/usage.jsonl` receives v2
  lines under a v1 header. Every reader would then trust the header, parse v2
  lines as v1, and silently mis-total — the exact undercounting the `usage.error`
  sentinel exists to make impossible. A version bump turns a latent hole into a
  live one, so it is closed in the same commit: on a non-empty file the first line
  is read and its version checked, and a mismatch **refuses to open**, naming the
  path and both versions.

  Refuse rather than rotate. Rotation looks kinder and is not: renaming a file a
  concurrent writer already holds open leaves that writer appending to an unlinked
  inode, so its calls vanish from the log the adapter is tailing — undercounting
  again, now caused by the mitigation, and precisely ADR 0027's rule that
  destructive recovery must never remove another actor's in-progress work. A
  refusal is loud, and the benchmark path never meets it because every attempt
  gets a fresh project directory. A malformed or unparseable first line is treated
  the same way as a mismatched one.

  The operator's remedy is to move the stale file aside, and the error says so —
  but it says it with the condition attached, because the remedy has the same
  hazard as the rotation just rejected: **removing the log is safe only once every
  writer on that project directory has stopped.** Doing it under a running target
  leaves that target appending to an unlinked inode and losing exactly the calls
  the refusal was protecting. "Costs one `rm`" was the wrong summary of a two-step
  operation whose first step is quiescing.
- **The v1 adapter's own version goes from `0.1.0` to `0.2.0`.** Its parsing and
  its normalized output both change, and `adapterVersion` is a comparison key in
  every run record's `TargetDescriptor`. It moves independently of the target's
  binary identity — the adapter can change while the target does not, and this is
  one of those times — so leaving it at `0.1.0` would make records produced by two
  different adapters compare as though one instrument had produced them.

**What the interval actually measures, corrected.** Round 2 called it the provider
attempt "after retry, timeout, circuit and rate-limit reservation". That is the
toolkit's *recommended* placement, and **Maestro deliberately does the opposite**:
`buildMaestroLLMsClient` composes `metrics → validation → retry → timeout →
circuit → rate limit → provider`, metrics **outermost**, so one aggregate Event
still observes outer rejections
([`factory_llms.go`](../../../pkg/agent/factory_llms.go)). Its own comment names
the tradeoff — "latency now folds in retry backoff and per-attempt granularity is
lost".

So `latency_ns` is the **whole logical call**: validation, every retry and its
backoff, the per-attempt timeouts, circuit behaviour and rate-limit waiting,
through to the final outcome. Which makes `started_at = finished_at - latency_ns`
the instant the orchestrator *began asking* — and that is the better fact for
`llm_calls.started_at` anyway, since a row that spans a call's real duration is
what cost-over-time and concurrency questions need. Two consequences to state
rather than discover: an `llm_calls` interval covers retries and can therefore be
far longer than any single provider round trip, and per-attempt latency is not
recoverable from this surface at all. A mid-call clock adjustment moves
`started_at` with it.

Extending the toolkit event to carry a real start instant, and to distinguish
logical from per-attempt latency, is the better long-term answer and belongs
upstream in `maestro-llms` — not in a v1 patch. Filed as
[issue 311](https://github.com/SnapdragonPartners/maestro/issues/311).
- **The streamed budget totals must not move.** `usageTail` feeds the engine's
  cap enforcement, so this is not only an accounting format: v2's tail must
  compute `tokens` as `input + output + reasoning`, which is exactly what v1's
  `prompt + completion` came to given that `completion = BillableOutputTokens`.
  Cache-read tokens are recorded and **not** added, because adding them would
  change what a declared cap means. A test pins v1 and v2 to identical totals over
  the same call sequence; that equality is the invariant, and it is proven by
  breaking it first.

**This is a v1 patch, and it is in scope for the reason the freeze allows.**
CLAUDE.md freezes v1 except where a defect blocks v2 work, and this one blocks
item 9's central promise — the call family cannot be honestly populated without
it. It is also the same surface Phase 1's P-1 patch created, extended for the
same reason. It touches `pkg/agent/middleware/metrics` and `pkg/agent/factory_llms.go`
and **not** `pkg/persistence`, so the plan's hard constraint on v1's SQLite path
is untouched.

It does, however, touch the measuring instrument during the phase whose top risk
is breaking it, and v2 being a replacement schema rather than an additive one
raises that rather than lowering it. Three things bound it: the version handshake
is checked in both places, so a v1 adapter meeting a v2 target fails loudly
instead of parsing a line that no longer means what it did; the streamed budget
totals are pinned identical across the change by test; and item 10's `golden-all`
run is the regression proof the plan already schedules. **This is also the review
checkpoint** — the surface-v2 commit is reviewed before the importer is built on
top of it, which is the isolation a separate branch would have bought without the
dependent-branch cost.

**Historical suites import without call records.** `benchmark/runs/` holds
evidence from surface-v1 runs, including Phase 1a's. Their records and artifacts
import normally; their `usage.jsonl` does **not** become `llm_calls`, because the
axes cannot be honestly split. The attempt is imported with call records marked
unavailable in the import summary and in the suite report's payload — a recorded
absence in the one place that is honest, rather than a sentinel in every row. The
legacy path is gated on the log's own header version, so it cannot silently
capture a v2 log.

### D9a. Only v1 is legacy, and the two accounts must reconcile (amendment)

*Proposed during implementation, 2026-08-06; found by Codex review of the
call mapping.*

**Legacy is a version, not a category.** The paragraph above gates the legacy
path on the header, which the first implementation read as "anything that is
not v2 is legacy" — so a v3 log, a v0, or a negative version would have been
imported as an ordinary absence, silently discarding measurements that are
sitting in the file. v1 is the one surface that has been *examined*: it folds
reasoning into a completion count, so its axes cannot be split, and every
suite in `benchmark/runs/` is one. The rule is therefore explicit in all three
directions: **v2 is read, v1 is a recorded absence, and every other version is
refused.** An unknown contract is not an absence.

**The record and its log must reconcile before either is written.** They are
two accounts of one attempt produced by the same process — the tail streams
the log, and its running totals are exactly what `run.go` sets `llm_calls`,
`tokens_total` and `cost_usd` from — so they cannot legitimately disagree. If
they do, one has been edited, truncated or rewritten since the run, and
importing both would put two contradicting *authoritative* accounts in the
plane: the per-call rows saying one thing and the metric events beside them
another, with nothing to say which is right. The importer therefore recomputes
the canonical figures by the tail's own arithmetic — file order, budget axes
(`input + output + reasoning`, cache excluded), float64 cost — and refuses the
attempt on any mismatch, before opening the transaction.

**An accepted attempt must have measured its calls and tokens.** It ran to
completion, so its metrics are the target's own observation, where a validated
log always yields measured counts (`run.go`'s `metrics`). Silence there beside
a readable log would leave per-call rows in the plane with no canonical total
to agree with, so `llm_calls` and `tokens_total` are *required* to be measured
and equal.

**The requirement stops at accepted, and the reason is in the engine.** When
the target errors, `synthesizeMetrics` marks every supported metric
`unavailable` and `overlayStreamedUsage` then restores only `tokens_total` and
`cost_usd` from the streamed tracker — **never `llm_calls`** — while evidence,
the usage log included, is still exported. A target-error attempt carrying
real calls beside `llm_calls: unavailable` is therefore the ordinary shape of
that path, not a contradiction, and requiring measurement unconditionally
would refuse every one of them. `cost_usd` is never required at all: a local
config's cost is `unavailable` by item 5.1 rather than the log's zero passed
through.

**The framing is the tail's framing.** The tail consumes a line only once its
newline has arrived, so a torn final write never reaches the totals above. The
importer drops that fragment for the same reason and by the same rule — a
reader that took it would import a call the record does not know about, and
then fail the reconciliation for a file that is exactly as healthy as the tail
thought it was. `bufio.Scanner` returns a final unterminated token and is
therefore the wrong tool here.

**The header is read strictly by all THREE components** — writer, tail and
importer — with unknown fields and trailing content refused and exhaustion
proven, because the header decides how every line beneath it is read. The
writer matters as much as the readers: appending v2 lines beneath a header the
readers refuse builds a log nobody can read, losing every call in the file
rather than one, which is the undercounting `usage.error` exists to prevent
arriving one door over.

**One line cap, enforced while reading, in all three.** `ReadString` and
`json.Unmarshal` both allocate the whole line before anything can object, so a
hostile or torn log holding one enormous unterminated run of bytes is read
into memory in full and only then measured — the check doing its damage before
its job. The cap is applied as the line accumulates (`ReadSlice`), and it is
the *same* cap in the writer, the tail and the importer: a line one component
is willing to emit must be one the others are willing to read. The number
lives in the corpus (`limits.json`) and each side asserts its own constant
against it, because three constants that merely happen to agree today are
three that can drift. The writer additionally bounds the one field that can
grow without limit — a provider error can carry a whole response body — by
truncating the diagnostic with a visible marker rather than refusing the line,
since truncating keeps the call counted while refusing drops it from every
total.

**A failed call carries no cost, as it carries no tokens.** `calculateCost`
returns nil whenever `success` is false — cost is computed *from* the token
counts a failed call does not have — so a priced failure is the same
fabrication as the counts, one column over. The writer, the budget tail and
the importer all refuse it, and the corpus carries the case.

## D10. Organizations and users have no creation path, and the slice needs one

Nothing in the plane creates an organization or a user. `GetOrganizationBySlug`
and `GetUserByHandle` are generated and have **no caller** — item 9 is their first
one — and every integration test to date has inserted its own org with raw SQL.
The importer cannot do that: it goes through the seam, which is the point.

So item 9 adds the smallest provisioning surface that makes the plane usable:
`CreateOrganization` and `CreateUser` on the seam's `Writer`, both idempotent by
their natural keys (slug, handle), exposed as

```
dataplanectl bootstrap --org <slug> --org-name <text> --user <handle> --user-name <text>
```

This is scope the plan did not name; review confirmed taking it here, because the
alternative is an import that cannot run without hand-written SQL, which is not a
vertical slice through the seam. The importer itself **resolves and never
creates** — an import that silently provisions a tenant is a defect waiting for
team mode.

**"Idempotent" is not enough, so the conflict semantics are exact.** Bootstrap
takes a natural key (slug, handle) and display data (display name). Three cases,
and the third is the one a loose reading gets wrong:

| Existing row | Display data | Outcome |
| --- | --- | --- |
| none | — | created; returned with `Created: true` |
| present | matches | existing row returned, `Created: false` — the no-op |
| present | differs | **`ErrBootstrapConflict`**, naming the key, the stored value and the supplied one |

Silently ignoring differing display data is the failure mode to design out: it
makes `bootstrap --org acme --org-name "Acme Ltd"` appear to succeed while the
plane still says "Acme Inc", and the operator learns otherwise from a report
months later. Renaming is a real operation and it will get an explicit verb when
something needs it; it is not a side effect of a provisioning command. The
importer's own lookups are strictly `Get*`, so this path is reachable only from
`bootstrap`.

**Concurrency, because the table above is a read-then-write and two operators can
run it at once.** The check-then-insert would race: both see no row, both insert,
one gets a raw uniqueness violation from the driver — which is neither of the
three outcomes and leaks a `23505` at the seam. So the write is
`INSERT ... ON CONFLICT (slug) DO NOTHING`, followed by a read of the row that is
now certainly there, and the comparison happens against **that** row. Two
simultaneous matching creates converge on one row and both report
`Created: false` for the loser; two differing creates converge on whichever
committed first and the loser gets `ErrBootstrapConflict` naming both values —
never a driver error. The uniqueness constraint is the arbiter and the seam
translates it, which is ADR 0027's rule for shared state: serialize on a key
matching the resource, and never last-writer-wins.

**Input is validated before SQL, not by it.** Blank or whitespace-only slug,
handle, or display name is refused with a typed error; the slug and handle are
additionally held to the same lowercase `[a-z0-9_-]+` shape the runner already
requires of a suite id, since both become identifiers in URLs and filenames later.
Letting the database refuse a blank via a CHECK would work and would report the
failure in the vocabulary of a constraint rather than of the flag the operator
typed.

## D11. Surface

```
dataplanectl bootstrap        --org <slug> --user <handle>
dataplanectl benchmark import --results <dir> --suite <id> --org <slug> --operator <handle>
dataplanectl benchmark show   --org <slug> --suite <id>
```

**`accept` is deliberately not in item 9** (amendment, 2026-08-06). The item
delivers a draft report with its complete pin set, which is the whole of what
the exit criterion needs — the cross-store commit order exercised end to end,
object write and digest reference and retention pin included. Acceptance needs a
reviewer who is not the author — an agent or a human, since ADR 0020's
invariant is non-authorship rather than humanity — and the review invariant is
worth more than a verb: shipping the command without anyone to run it would
invite exactly the manufactured reviewer D5 argues against. The reviewer this
report waits for is expected to be the `benchmark-evidence-reviewer` agent
recorded against GitHub #282. `show` therefore prints
`DRAFT — UNREVIEWED — NOT AUTHORITATIVE` above every unaccepted report, so a
reader cannot mistake one for a finding. The reviewer role is recorded against
GitHub #282 for after Phase 2.

`--results` defaults to `benchmark/runs`, matching the runner's own default;
`--suite` may be repeated or omitted to mean every suite the store lists. `show`
is the "queried back" half of the exit criterion: it reads through the seam by
scope and prints the suite, its attempts, their verdicts, the pinned evidence and
the report's status.

Make targets mirror the existing `dataplane-*` family:
`make benchmark-import SUITE=<id>`, `make benchmark-show SUITE=<id>`.

Code lands in `internal/dataplane/benchmarkimport/`: the reader (D1), the mapper
(D3–D4), the evidence walk (D8), report assembly (D5, D7) and the importer that
drives the seam. The CLI verb is a thin shell over it, as the existing
`dataplanectl` verbs are over `stack`.

Reaching the plane needed one addition to `stack`: `OpenSeam`, because the root
key, the DSN derived from it and the object bucket may not be assembled outside
that package — `rootKeyFor` alone decides create-versus-load, and a structure
test enforces it. It carries a `lifecycleUse` of its own, guarded like every
other operation, and it takes the lifecycle lock **shared**, holding it until
the store is closed.

Not exclusive, because ordinary use must overlap with itself. Not unlocked
either, which is what the first version did on the argument that using a plane
is not a lifecycle transition — true, and it does not follow. Not a transition
means not exclusive; unlocked made the marker guard a TOCTOU, since a reset
could take the exclusive lock immediately after the check read clean. `down`,
`reset` and `restore` now wait for an open seam, which is the outcome ADR 0027
asks for.

## Testing

Against a real ephemeral Postgres and MinIO, behind the `integration` tag, as the
plan's testing rule for this phase requires.

- **Round trip.** Import the fixture suite; read every artifact back by scope;
  assert the record's fields survive the envelope, the MPH columns match the
  descriptor, the calls and metric events are attributed to the attempt's
  principal, and every evidence file is retrievable by digest through
  `GetAttachment`.
- **Idempotency.** Import twice; assert row counts identical and the second run
  reports every attempt skipped. Then mutate one record in the fixture and assert
  `ErrImportConflict` names that run id and leaves every other row untouched.
- **Partial and resumed.** Import a suite whose `stop_reason` is `running`; assert
  attempts land and no report exists. Append attempts, mark it terminal, import
  again; assert only the new attempts are written and the report appears.
- **Crash between artifact and ledger.** Failure injection inside the per-attempt
  transaction, then re-import; assert exactly one artifact exists. This is the
  test that would fail if the two were not atomic — and it is written by breaking
  the atomicity first, per the Verification Discipline.
- **The review invariant.** Assert the system importer **cannot** author the
  suite report (the schema refuses it), that acceptance by the author is refused
  with `ReasonReviewerIsAuthor`, and that acceptance by a distinct human succeeds
  and pins verify. A mutation that removes the reviewer check must make this fail.
- **Self-review through two instances.** One user, two human principal instances,
  author with the first and accept with the second: refused with
  `ReasonReviewerIsAuthorUser`. This test fails against the seam as it stands
  today, which is what makes it a regression test rather than a restatement. Its
  companions: two *agent* instances sharing a model still accept, and an
  agent-authored artifact accepted by the human named in its `user_id` still
  accepts — the two boundaries D5 draws, each asserted rather than assumed.
- **Pin completeness.** Remove one reference from the reviewed payload and assert
  acceptance refuses the extra pin; add an unreferenced attachment and assert the
  same.
- **Scope constraints.** Assert a benchmark-scoped artifact with any work-lineage
  column set is refused, and that `scope_type = 'benchmark'` with a null
  `scope_benchmark_run_id` is refused — the constraint item 9 is adding, tested by
  breaking it.
- **`scope_id` after the rebuild.** Assert a benchmark-scoped artifact comes back
  from `ListManagementArtifactsByScope` and carries a non-nil `Scope.ID`, and
  that pre-existing rows of every other scope type keep the `scope_id` they had
  across `up`/`down`/`up` on a populated table. This is the test the first draft
  would have failed.
- **Usage surface v2.** The budget-total equality of D9 — v1 and v2 tails
  computing identical `tokens` over one call sequence — proven by first making v2
  count cache reads and watching it fail. The header handshake refusing a v1 log
  to a v2-requiring adapter and vice versa. An unpriced model producing an absent
  `cost_usd` rather than a zero, and a failed call carrying its error text.
  The coherence rule refused in both directions, at the writer and at the importer.
  `latency_ns` round-tripping a duration that milliseconds would have rounded, so
  `finished_at - latency_ns` recovers the start exactly.
- **The writer refuses a mismatched header.** Open a v1 log with a v2 build and
  assert `NewUsageLogRecorder` fails, naming both versions, **and that the file is
  left byte-for-byte unchanged** — a refusal that truncated or rotated would be
  the failure this test exists to prevent. Same for a malformed first line.
- **Legacy suites.** Import a surface-v1 suite; assert artifacts and attempts
  land, no `llm_calls` rows are written, and the unavailability is named in both
  the summary and the report payload.
- **Bootstrap conflict.** All three rows of D10's table, with the differing-display
  case asserted to leave the stored row untouched; concurrent matching creates
  from N goroutines converging on one row with exactly one `Created: true`;
  concurrent differing creates yielding `ErrBootstrapConflict` and never a driver
  error; and blank and malformed inputs refused before any statement is issued.
- **Token axes.** A call reporting cache reads and cache writes lands both in
  their own columns — the assertion that would have been impossible to write under
  the deferred-column proposal, and the reason it was not deferred. And
  `AggregateLLMCost` totalling each axis separately over a fixture whose two axes
  differ, so a rollup reading the wrong column cannot pass.
- **Contract drift.** The two-sided fixture tests of D1, each proven to fail by
  adding a field to the fixture on one side only.
- **Caps.** A file over the cap is skipped, named in the summary, and named in the
  report payload.
- **Failed calls carry no token measurement.** A failed line with token fields is
  refused at the writer and at the importer; a failed call's row has all five
  columns null and the `IN (0, 5)` check refuses four-of-five; `AggregateLLMCost`
  counts it in `TokensUnmeasuredCalls` and excludes it from every sum. Proven by
  first writing zeros and asserting the aggregate then reports it as measured —
  the wrong answer this whole change exists to stop.
- **Evidence survives a partial import.** Import a running suite, run attachment
  truncation and the object sweep, then import the now-terminal suite and assert
  every evidence file is present and pinned. Against the deferred-upload rule this
  passes; against the first revision's upload-as-you-go it does not, which is what
  makes it the regression test for P1 2 rather than a restatement of D8.
- **Path containment.** A record whose `run_id` is `../../etc`, `.`, `..`, or
  contains a separator is refused before any filesystem access; a symlink inside a
  legitimate evidence directory — including one pointing outside the results root —
  is skipped, named, and never read. Proven by planting both and asserting the
  file outside the root is never uploaded.
- **The conformance corpus.** Every case run by both validators with agreement
  asserted, and the AST-derived coverage check proven by adding a rejection reason
  with no corpus case and watching it fail.
- **Value validity.** Each row of D9's table broken independently at both readers.
  The negative-axis case is the one to write first and by hand:
  `{input: 1000000, output: -999999}` must fail the tail with an error — asserted
  against today's code first, where it passes the tracker's guard as a delta of 1
  and under-accounts by two million tokens. A test that cannot fail for that
  defect is not a regression test for it.
- **Cross-file coherence.** Each row of the table in D1 broken independently:
  mismatched suite ids across the three places, a duplicate `run_id`, an unknown
  manifest status, a `completed` entry with no record, a record with no entry, and
  a manifest entry naming the wrong `run_id`. Each refuses the suite whole and
  names the disagreement, and none of them leaves a partial import behind.

## Review round 1 disposition

Codex, 2026-08-03. The central shape — Audit run records, an operator-authored
draft Management report, complete pins, terminal-only report creation,
ledger-backed idempotency — was approved unchanged.

| Question | Disposition | Where |
| --- | --- | --- |
| D5, operator-authored report | **Approved** | unchanged |
| D10, bootstrap verb | **Include now**, with exact conflict semantics | D10 rewritten |
| D2, null `scope_id` | **Rejected** — rebuild the generated column, add the composite unique key | D2 rewritten |
| D9, `provider` sentinel | **Rejected** — record it at the source; upgrade the usage surface in this item | D9 rewritten |

Two blockers found in review beyond those four, both accepted:

1. The surface upgrade needs to be fuller than provider alone — failure
   diagnostic, start/finish timing, and the four token axes, since
   `completion_tokens` folds billable output and reasoning together while the
   plane stores them apart. D3's mapping table and D9 now cover all of it.
2. `scope_id` is read as the domain scope id and compared directly by the scope
   queries, so null benchmark rows would never be listed. D2 covers it.

One point of Codex's was checked and stands: MPH's `M` carries the model-routing
structure, but production bundles hold bare model names and role routing may span
providers, so a single provider on `TargetDescriptor` would be wrong. Per-call
recording is the only correct answer. Provider-bearing MPH routes are a separate
question and are not opened here.

## Review round 2 disposition

Codex, 2026-08-03. The usage-surface upgrade **stays in item 9 on this branch**,
with a review checkpoint after the surface-v2 commit — better isolation than a
dependent branch. Four blockers, all accepted and fixed above:

| # | Blocker | Resolution |
| --- | --- | --- |
| 1 | D5 permitted human self-review through a second principal instance | seam fix, new `ReasonReviewerIsAuthorUser`, with both boundaries tested (D5) |
| 2 | D9's timestamps claimed to be recorded when the source has only latency | documented as derived, `latency_ms` written verbatim beside them (D9) |
| 3 | v2 left ambiguous whether the folded token fields survive | v2 stated as an exact replacement schema; adapter version `0.1.0` → `0.2.0` (D9) |
| 4 | Bootstrap lacked concurrency and input semantics | `ON CONFLICT DO NOTHING` then read-and-compare; validation before SQL (D10) |

And one reversal: **`cache_write_tokens` gets its column now.** The deferred
measurement was wrong-headed and the reason generalises — an all-zero result from
today's configuration would not show the dimension is irrelevant, only that this
sample never exercised it. `cached_tokens` is renamed `cache_read_tokens` in the
same migration so neither column's meaning depends on a comment (D3).

Blocker 1 is the one worth remembering: D5 rested its entire argument on a check
that did not hold, and nothing in the existing suite could have caught it, because
every test that exercises the rule uses one instance per principal.

## Review round 3 disposition

Codex, 2026-08-03. Four P1s, all accepted:

| # | P1 | Resolution |
| --- | --- | --- |
| 1 | D9 had the middleware order backwards — Maestro places `MetricsChat` **outermost** | latency is the whole logical call, retries and backoff included; consequences stated (D9) |
| 2 | The writer appends to any non-empty log without checking its header | validate before append and **refuse**, not rotate; test asserts the file is left unchanged (D9) |
| 3 | v2 still carried three answers for timing, and `latency_ms` is not reversible from a `Duration` | one instant plus `latency_ns`; typed field table with a stated coherence rule (D9) |
| 4 | The cached-token split stopped at the row and left `CostAggregate` and its SQL folded | split required through SQL, generated row, converter, seam type and tests, over a fixture whose axes differ (D3) |

Plus the wording fix: the joined principal identity fields are **not** protected by
the artifact row lock. They are safe because a principal's kind and user ownership
are immutable for the instance's life, and the doc now says that instead.

P1 1 is the instructive one. The claim came from the *toolkit's* comment about its
recommended placement rather than from Maestro's actual chain, which relocates
metrics deliberately and documents the tradeoff in the very function that builds
it. Reading the dependency's documentation instead of the calling code produced a
confident, specific, wrong statement about our own system — the same failure mode
CLAUDE.md's verification discipline names for dependency claims, arriving from the
opposite direction.

## Review round 4 disposition

Codex, 2026-08-03. Four P1s, all accepted:

| # | P1 | Resolution |
| --- | --- | --- |
| 1 | Failed calls would write fabricated zero tokens into non-null counters, and aggregates would sum them as measured | availability carried through surface, row, seam and aggregate; nullable columns with an all-or-nothing check (D3) |
| 2 | A partial import's attachments are held by no artifact, so truncation can reclaim them and the terminal import skips the ledgered attempt | evidence upload deferred to terminal assembly, which rescans **every** attempt; the artifact skip and the evidence skip are separated (D7) |
| 3 | `run_id` is an unvalidated path component, so a crafted record escapes the results root | single-component shape, containment after symlink resolution, no symlink following, and the same shape rule added to the runner's own validator (D8) |
| 4 | Contract-shape agreement is not input validity | cross-file coherence table, with what `RunRecord.Validate` already covers named rather than restated (D1) |

Wording fixed as noted: removing a mismatched usage log is safe only after every
writer on that project directory has quiesced, which the error message now says.
"Costs one `rm`" described a two-step operation as one and understated the same
unlinked-inode hazard the paragraph above it warns about.

P1 2 is the one to carry forward. Two skip rules were folded into one sentence —
"a ledgered attempt is skipped" — and they are not the same rule: its rows are
append-only and correctly skipped, while its evidence has no holder yet and must
not be. Both P1 2 and round 2's blocker 1 have that shape, a single condition
standing in for two distinct questions.

## Review round 5 disposition

Codex, 2026-08-04. Two blockers, both accepted:

| # | Blocker | Resolution |
| --- | --- | --- |
| 1 | D1 claimed per-record validation as inherited from code the importer is forbidden to call — and which ran when the file was *written*, not when it is read | the importer implements the equivalent semantics itself, with a two-sided accept/reject conformance corpus and AST-derived coverage (D1) |
| 2 | Surface v2 defined presence but never value validity | a value-validity table enforced at every reader, and three defects in `usageTail` fixed with it (D9) |

Blocker 1 is the worse of the two and it is the same mistake twice. Round 4 added
the cross-file checks *because* the fixture proves contract shape and not input
validity; then the per-record half was left leaning on a validation that had
already been argued unreachable four decisions earlier, in the same document. The
argument for the corpus is the argument round 4 already made, applied one level
down.

Blocker 2 turned out to have a live bypass under it. `usageTail` sums the token
axes before reporting, and `usageTracker.report`'s non-negative guard sees only the
sum — so `{input: 1000000, output: -999999}` reports a delta of 1, passes the
guard, and under-accounts an attempt by two million tokens with the cap
unenforced and nothing logged. The guard is not weak; it is downstream of the
arithmetic that destroyed the evidence. Two more in the same function: the tail's
canonical totals accumulate without the overflow and NaN/Inf saturation
`usageTracker` applies to its own copy, so a malformed cost the tracker rejects
still reaches the record. All three are fixed in the surface-v2 commit, since that
commit is already rewriting the parser.

**Upstream issue filed:**
[#311](https://github.com/SnapdragonPartners/maestro/issues/311), covering both
acknowledged gaps — usage invisible for failed logical calls, and per-attempt
latency unrecoverable now that metrics sits outermost — per the build process's
rule that discovered deferred work gets an owner before the item closes.

## Related documents

- [Phase 2 scope and plan](plan_scope.md) — item 9, and reviewer question 5 (import destination, not results sink).
- [ADR 0022](../../adr/0022-v2-data-plane.md) — the plane; [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) — artifacts, principals, MPH, pins; [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) — envelopes and the registry; [ADR 0020](../../adr/0020-review-invariant-reviewer-vs-partner.md) — the review invariant D5 turns on; [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) — runner independence, which D1 preserves.
- [Item 4](design_queries_artifacts.md), [item 5](design_calls_family.md), [item 6](design_object_module.md) — the seam, the call family and the commit order this item composes.
- [Schema table inventory](inventory_schema-tables.md) — extended by D2's two tables.
