+++
title = "Execution Contracts: Verbs, Result Shape, And Where They Run"
edit_date = "2026-08-11"
status = "draft"
type = "notes"
summary = "Design input for the Phase 3 plan on the build/test/lint/deploy contract set: what v1 actually has and how thin it is, why the two Habitat deployment stages are identity changes rather than two verbs, why the invocation half of a contract needs almost nothing and the result half needs a preserved audit artifact, the verb inventory Phase 3 should prune, and a recommended Habitat lease-reclamation design — Story-bound leases reclaimed on demand rather than on a clock, with provisioning cost measured rather than configured — which ADR 0029 deliberately left open."
+++

# Execution Contracts: Verbs, Result Shape, And Where They Run

Status: **draft** — working notes from the A1 design conversation (DR and Claude,
2026-08-10), separated from [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md)
deliberately. The ADR carries exactly one sentence from this material: *an
execution contract declares the resource it executes in*, because that is what
makes the no-direct-channel invariant enforceable. Everything else here is Phase
3 plan input and must not be folded into the ADR, which would turn a pre-entry
design item into the phase.

**Nothing here binds.** Some sections carry a recommendation and say so; none is
Accepted, and the Phase 3 plan is free to overrule any of it. The document exists
because the reasoning took several exchanges to reach and would otherwise be lost
between the ADR that excludes it and the plan that consumes it.

## What v1 actually has

Less than the phrase "build contract" suggests, and slightly more than expected
in one respect.

`BuildConfig` (`pkg/config/config.go:803`) is **six command strings**, not three:

```go
Build   string   // default "make build"
Test    string   // default "make test"
Lint    string   // default "make lint"
Run     string   // default "make run"
Clean   string   // optional
Install string   // optional
```

They are consumed two ways:

- **Interpolated into prompt templates** (`pkg/templates/bootstrap/data.go:153-155`,
  `pkg/templates/coder/app_coding.tpl.md`), so the agent is told to run
  `shell({"command": "make test"})`. The contract is prompt text on this path.
- **Invoked directly from Go** — `pkg/coder/testing.go:469` (`runMakeTest`) reads
  the configured command and executes it under a hardcoded five-minute timeout.
  `pkg/demo/service.go:307` uses `Build.Build` for demo mode.

**The result shape is `(passed bool, output string, err error)`** — a boolean and
a text blob, truncated before it reaches the agent. Nothing structured is
captured and nothing is preserved.

Two observations that matter for Phase 3:

- **`Run` already exists and is the closest thing to a deploy verb.** In v1
  `make run` means "run the application locally," which under
  [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md)'s split
  is precisely what the Habitat does. `Run` was probably always the degenerate-case
  deploy. Phase 3 must decide whether `deploy` subsumes it or sits beside it.
- **Demo mode already uses this path**, and the roadmap names Demo Mode as the
  foundation for UAT. So the Habitat's verification surface has a v1 antecedent
  here too.

## The two Habitat stages are identity changes, not two verbs

The stages are real:

1. *Are the currently defined services running for this commit?* — infrastructure
   convergence.
2. *Is this commit built and deployed everywhere it needs to go?* — application
   deployment.

They map onto ADR 0029 §5, which after review round 1 carries **three**
identities rather than two counters: stage 1 changes the `SpecDigest` and
therefore requires a new **instance generation**; stage 2 advances the
**deployment generation** within the current instance. The discriminator is
whether the Habitat definition closure changed.

The third identity matters here: an instance generation also advances on reset,
recovery, or quarantine replacement with an unchanged `SpecDigest`, so "the
definition changed" and "this is a new incarnation" are not the same event and a
receipt cannot infer one from the other.

**Recommendation: one `deploy` verb, not two.** Three reasons:

- **They collapse for the MVP backend.** `compose up --build` converges services
  *and* deploys the new code, because the application is a service in the file.
  `kubectl apply` likewise. They are genuinely two steps only for
  Terraform-plus-separate-rollout, which is deferred.
- **Two required verbs force a no-op.** Every Compose repository would write an
  empty stage-2 contract to satisfy the schema. A contract most repositories must
  stub is the wrong contract.
- **Failure attribution does not need it.** The reason to want two is knowing
  whether a failure was infrastructure or application. That comes from the receipt
  recording which clock advanced — which is recorded anyway — not from splitting
  the invocation.

A provider whose backend genuinely needs two steps invokes its own contract
twice. Splitting the verb later is additive.

## The invocation half is nearly free; the result half is not

The generalization question was posed as *"it might be a script, it might be a
forge workflow, it might be something else — we need a wrapper per contract."*

**A script is already a shell command.** `deploy: ./scripts/deploy.sh` needs no
wrapper. The only genuinely different case is a forge workflow, and it differs in
kind rather than in syntax: asynchronous, remote, result arriving by polling or
callback, with its own failure and timeout semantics. A wrapper abstraction buys
nothing for scripts and does not actually cover workflows — the shape CLAUDE.md
directs us to reject, an abstraction with one real implementation and a
speculative second.

**The result shape is where the cost is**, and it is the half that is expensive to
retrofit. A boolean plus a truncated blob is adequate for build and lint, already
thin for test (ADR 0025's evidence wants to know *which* tests failed), and
inadequate for deploy, because a promotion record binds artifact digests and a
boolean cannot carry them.

**Proposed shape** (DR, 2026-08-10):

```text
(passed bool, summary string, audit_artifact_id, error)
```

Three properties worth stating explicitly:

- **The full output is preserved, not returned.** Cascading build errors overwhelm
  agent context; a truncated or summarized string goes to the agent and the
  complete output is retained as a queryable artifact.
- **`audit_artifact_id` is not a new concept.** It is an Audit-category artifact
  under [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md), encoded
  per [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md). The
  contract runner does not invent a record type.
- **Summarization is inference and therefore not the Orchestrator's** under
  [ADR 0019](../../adr/0019-orchestrator-boundary.md), unless the summary is
  purely mechanical (exit code, first N lines, test counts parsed by a known
  format). Phase 3 should keep it mechanical or route it to an agent explicitly;
  a "smart" truncation inside the Orchestrator is a boundary violation wearing a
  convenience hat.

**For MVP, the wrapper can stay a bare command longer than the forge-workflow
case suggests.** The Compose provider can *introspect* what it deployed —
`docker compose ps` yields image identities — rather than trusting a script to
report it. The provider observes reality; the wrapper only reports pass/fail.
That is better for correctness and it defers the structured-result channel to the
case where the provider cannot see the target (a forge-workflow deploy to
somewhere Maestro has no visibility). Reserve that channel; do not build it.

## Contracts declare their execution resource

The one line that belongs in the ADR, restated here for the plan:

- **Incubator contracts** — those requiring only the workspace and toolchain.
- **Habitat contracts** — those requiring deployed or dependent services.

**Routing is by requirement, never by name.** An earlier version of these notes
listed "build, lint, unit test" as Incubator contracts, which contradicts ADR 0029
§1: a repository whose unit tests need a database runs them against a Habitat.
`test` and `integration` are repository terminology and vary between projects.

Consequences the plan has to carry:

- The agent's model is *"run what needs only your toolchain locally; when you
  submit finished work or explicitly request it, the Orchestrator stands up the
  environment your repository defines and runs the contract there."* Note this is
  deliberately **not** phrased as "unit tests are local" — that is exactly the
  label-based routing the rule above rejects.
- **Verification executes inside the Habitat's fencing domain, and the provider
  may launch the executor.** An earlier version of these notes required the
  Habitat *definition* to contain a test runner. That is superseded: ADR 0029 §8
  permits a **provider-launched verification executor** joining the same fencing
  domain and instance generation without appearing in the definition — because
  requiring otherwise would mean editing production deployment definitions, which
  §1 forbids. A repository may still declare its own runner in a
  development-facing overlay. What stays prohibited is a runner *outside* the
  domain reaching in.
- A contract's declared resource is checkable, which is what makes the invariant
  enforceable rather than aspirational.
- **The plan must carry the two run kinds** from ADR 0029 §6, because they change
  what the agent experiences: an *iteration* run redeploys into the existing
  instance and keeps accumulated state, while an *evidence-bearing* run resets
  first and is correspondingly slower. Both always converge the code. The
  Orchestrator classifies; the agent does not choose. DR's v1 analogue is that a
  test request from the coding loop is an iteration and one from the final
  approval loop is evidence-bearing — corroboration that the distinction matches
  how the factory already works, not a proposal to key it on a state name.

## Verb inventory to prune

Flagged by DR: *"we might also review the verbs we are capturing generally. I'm
not sure if we need all of the ones we already have."* The current six, with a
first pass:

**The `Resource` column is what each verb typically requires, not a fixed
assignment** — per the routing rule above, the same verb lands in different
resources in different repositories.

| Verb | Typically requires | Note |
| --- | --- | --- |
| `build` | Toolchain only | Keep. Habitat-resident only where the build itself needs services, which is unusual. |
| `test` | **Either** | The verb that most exposes label-based routing: no dependencies means Incubator, a database means Habitat. Same name, same repository intent, different resource. |
| `lint` | Toolchain only | Keep. |
| `run` | Deployed services | Reconcile with `deploy`. Probably becomes it. |
| `clean` | — | Candidate for removal. ADR 0029 §6 states `make clean` is not evidence of reset, which is most of what it was for. |
| `install` | Toolchain only | Candidate for removal or fold into the Incubator definition, now that toolchain changes are definition edits rather than imperative container mutation. |
| `deploy` | Deployed services | New. |
| `integration` | Deployed services | New, or `test` whose requirements route it to the Habitat. |

Whether `integration` is a distinct verb or simply the requirement-routed form of
`test` is open, and the routing rule tilts it: if requirements determine the
resource, a second verb carries no information the requirements do not already
supply. A distinct verb may still be clearer to the agent. Decide in the plan.

## Habitat lease reclamation — recommended design

ADR 0029 §2 fixes that lease lifetime and instance lifetime are separate, that a
lease is bounded and independently revocable, that expiry deauthorizes rather
than fences, and that per-type capacity limits are the backstop. It deliberately
does not fix **how a lease ends**, because that is scheduling policy rather than
a boundary. This section is the recommendation, arrived at 2026-08-11 (DR and
Claude) and not yet reviewed by Codex.

### One session concept, not two cases

The problem first presented as two cases needing two answers: an automated loop
that is self-delimiting, and an agent tool call that is not. They are one
mechanism. The lease binds to a **verification session** the Orchestrator opens
and closes; the loop closes its session on completion, and the tool-call case
closes on the rules below. Two mechanisms would supply two ways to leak a
Habitat.

### Leases are Story-bound, not agent-bound

**Completion of the Story releases every lease it holds.** This is the primary
release path, not a fallback: leases belong to the Story execution — the same
scoping ADR 0029 §2 gives the Incubator — so agent restart or replacement does
not release them and Story completion always does. It is also the explicit
release verb we would otherwise need, without being a verb the agent can forget.

**UAT is the known exception and is deferred.** A UAT Habitat is held for a human
at Epic grain, so its lease outlives the automated completion of any Story and is
paced in hours rather than minutes. Demand-driven reclamation (below) would be
actively hostile to it. The exception is structural rather than incidental and
needs its own answer when UAT gate policy is designed; nothing here should be
read as covering it.

### Reclaim on demand, not on a clock

What is reclaimed is the **retention claim** (ADR 0029 §2), not authorization —
an idle execution holds no lease to take. A timeout on retention exists only to
resolve contention, so with nothing queued it discards a warm environment for no
benefit:

- **Nothing queued** → the Story keeps its warm instance. The 1:1 case
  (concurrent executions ≤ Habitat capacity) needs no special handling; it falls
  out.
- **A queue forms** → a retention claim with no active lease becomes reclaimable.
  A claim whose lease is active never is.

Still self-curing: a crashed execution holds no lease and stops renewing its
claim, so it loses the instance as soon as anyone wants it. If nobody wants it,
holding costs nothing. An execution that dies without a terminal result is covered
by the reconciliation path that must exist regardless.

The cost is that reclamation needs the scheduler to know who is queued, where a
timer would be purely local. The Orchestrator already assigns leases and already
owns the queue, so this is one component talking to itself.

### A minimum hold, doing a different job

Demand-driven reclamation can thrash — two executions ping-ponging one Habitat,
each paying a rebuild. A **minimum hold** prevents it: a lease cannot be reclaimed
within a short window of acquisition or of its last verification.

This is a timer, but it bounds *thrash*, not *abandonment*. Keep the two jobs
distinct so they are not later "simplified" into one number.

### What reclamation actually costs — corrected

An earlier version of this section said reclaiming costs a rebuild that both
parties pay. That conflated releasing authorization with destroying the
environment, which ADR 0029 §2 now separates.

- **Releasing a lease costs nothing.** Authorization is generation-bound and held
  only during an active verification session; an idle execution holds none, so
  there is usually nothing to reclaim in the first place.
- **Reclaiming the retention claim** hands the instance to another Story. Whether
  that costs a rebuild depends on §6: the arriving Story's first
  evidence-bearing verification would reprovision anyway, so reclaiming an
  instance it was going to reset is nearly free. Reclaiming one it intended to
  iterate against costs the displaced Story its warm environment.

So the comparison is not *wait versus rebuild* but **whose warm environment is
worth more** — the arriving Story's queue time against the displaced Story's lost
warmth. Provisioning cost still weights it, and Maestro **measures** that rather
than being configured with it, since every provision is timed.

One constraint from ADR 0029 §2 is absolute: **reclamation must exclude an
instance with an active lease**, and must be bounded so successive retention
claims cannot starve a queue indefinitely.

### What is configured, and what is not

| | Where it lives | Why |
| --- | --- | --- |
| Per-contract timeouts | **Project config**, with a generous default | A fact about the repository's own suite, unmeasurable in advance. |
| Provisioning cost | **Measured** | The system times every provision. |
| Contention | **Observed** | The scheduler owns the queue. |
| Minimum hold | **Small constant** | A stability floor, not a project fact. |

**A ten-minute default test timeout would fail on this repository.** Maestro's own
`make test-integration` runs around eleven minutes locally. v1's hardcoded
five-minute timeout in `runMakeTest` is the same defect one size smaller. That is
the argument for the default being generous and project config being the real
answer — do not ship a knob for something measurable, and do not ship a default
for something that is not.

### Alternatives considered

| Mechanism | Why not |
| --- | --- |
| Wall-clock TTL from acquisition | Must exceed the slowest suite or it expires mid-run, forcing a decision about reclaiming a Habitat with live verification in it. Patching that with renewal-on-use produces an idle timeout with extra steps. |
| Idle timeout since last verification | Strictly worse than demand-driven in the uncontended case (pays a rebuild nobody asked for) and slower in the contended case (a queued execution waits out a clock that was never about it). Retained only as the fallback if demand signalling proves awkward. |
| Explicit release verb plus TTL backstop | The verb helps only the well-behaved case and the backstop is still mandatory. Story completion gives the same benefit for free. |

Whichever is chosen, one ADR 0029 constraint holds: **expiry or reclamation
deauthorizes; it does not fence.** A reclaimed lease still goes through §7's
protocol before the Habitat is reassigned, and §6's reset requirement applies to
the replacement.

## Open questions for the Phase 3 plan

1. Does `deploy` subsume `Run`, or sit beside it?
2. Distinct `integration` verb, or `test` with a declared resource?
3. Are `clean` and `install` retained, removed, or folded into the definitions?
4. Mechanical summarization only, or an explicit agent summarization step for
   large failure output?
5. May the agent imperatively mutate a running Incubator for iteration speed? ADR
   0029 defers this; if allowed it must be explicitly non-durable so an omission
   from the definition surfaces at promotion rather than after merge.
6. Where does the contract set live — repository config as today, the Incubator
   definition, or the data plane?
7. Confirm demand-driven lease reclamation, per the section above, and fix the
   minimum-hold constant and the default contract timeouts.
8. UAT lease lifetime, which the Story-completion release rule explicitly does
   not cover. Sequenced with UAT gate policy ([backlog candidate 6](../notes_adr-backlog.md)),
   not here.

## Related Documents

- [ADR 0029: Incubator And Habitat Execution Boundaries](../../adr/0029-incubator-and-habitat-execution-boundaries.md)
  — the resource split these contracts run in, and the one rule taken from here.
- [Pre-Phase-3 Blockers](plan_blockers.md) — item A1, and the rule that pre-entry
  design items produce an ADR and nothing else.
- [ADR 0019: Orchestrator Boundary](../../adr/0019-orchestrator-boundary.md) — why
  summarization is a boundary question.
- [ADR 0025: Golden Stories And The Benchmark Runner](../../adr/0025-golden-stories-and-benchmark-runner.md)
  — the evidence consumer that wants structured test results.
