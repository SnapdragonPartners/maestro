+++
title = "Execution Contracts: Verbs, Result Shape, And Where They Run"
edit_date = "2026-08-11"
status = "draft"
type = "notes"
summary = "Design input for the Phase 3 plan on the build/test/lint/deploy contract set: what v1 actually has and how thin it is, why the two Habitat deployment stages are identity changes rather than two verbs, why the invocation half of a contract needs almost nothing and the result half needs a preserved audit artifact, the verb inventory Phase 3 should prune, and the Habitat lease-reclamation mechanisms ADR 0029 deliberately left open."
+++

# Execution Contracts: Verbs, Result Shape, And Where They Run

Status: **draft** — working notes from the A1 design conversation (DR and Claude,
2026-08-10), separated from [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md)
deliberately. The ADR carries exactly one sentence from this material: *an
execution contract declares the resource it executes in*, because that is what
makes the no-direct-channel invariant enforceable. Everything else here is Phase
3 plan input and must not be folded into the ADR, which would turn a pre-entry
design item into the phase.

Nothing here is decided. It is recorded so three exchanges of reasoning are not
lost between the ADR and the plan that consumes it.

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

- **Incubator contracts** — build, lint, unit test. No ecosystem required.
- **Habitat contracts** — deploy, integration test. Ecosystem required.

Consequences the plan has to carry:

- The agent's model is *"run unit tests locally; when you submit finished work or
  explicitly request it, the Orchestrator stands up the full environment defined
  in the Habitat source files and runs the integration contract."*
- **The Habitat definition must include somewhere to execute its own
  verification.** Under the no-direct-channel invariant the Incubator cannot reach
  in, so the test runner is part of the environment — a service or target in the
  Compose project — not something reaching in from outside.
- A contract's declared resource is checkable, which is what makes the invariant
  enforceable rather than aspirational.

## Verb inventory to prune

Flagged by DR: *"we might also review the verbs we are capturing generally. I'm
not sure if we need all of the ones we already have."* The current six, with a
first pass:

| Verb | Resource | Note |
| --- | --- | --- |
| `build` | Incubator | Keep. |
| `test` | Incubator | Keep; means *unit* test under the split. |
| `lint` | Incubator | Keep. |
| `run` | — | Reconcile with `deploy`. Probably becomes it. |
| `clean` | — | Candidate for removal. ADR 0029 §6 states `make clean` is not evidence of reset, which is most of what it was for. |
| `install` | Incubator | Candidate for removal or fold into the Incubator definition, now that toolchain changes are definition edits rather than imperative container mutation. |
| `deploy` | Habitat | New. |
| `integration` | Habitat | New, or `test` with a declared resource. |

Whether `integration` is a distinct verb or `test` scoped to the Habitat is open.
A distinct verb is clearer to the agent; a resource-scoped `test` is fewer
concepts. Decide in the plan.

## Habitat lease duration — deferred here by ADR 0029 §2

ADR 0029 fixes that lease lifetime and instance lifetime are separate, that a
lease is bounded and independently revocable, that expiry deauthorizes but does
not fence, and that per-type capacity limits are the backstop. It deliberately
does not fix **how long a lease lasts**, because the answer is a scheduling
policy rather than a boundary.

The shape of the problem, recorded so the plan does not rediscover it:

- **An automated loop is self-delimiting.** A lease acquired for a state-machine
  phase (the v1 analogue is entering `TESTING`) ends when the phase does. The
  duration needs no policy — the loop's own completion releases it.
- **An agent-requested verification is not.** A tool call asking for integration
  verification has no intrinsic end. The agent may run one test, read the result,
  fix, and run again, or it may stop and never return.

So the open question is narrowly about the second case, and it is a
**lease-reclamation** question: how does a Habitat get taken back from an
execution that stopped asking for it?

Candidate mechanisms, none chosen:

| Mechanism | Note |
| --- | --- |
| Wall-clock TTL with renewal on use | Simplest, and self-curing after a crash or a stuck agent. Costs a held Habitat for up to one TTL after abandonment. |
| Idle timeout since last verification request | Same self-curing property, more forgiving of a long-running test, and it measures the thing that actually indicates abandonment. |
| Explicit release verb plus a TTL backstop | The agent releases when done; the TTL only covers failure. Adds a verb the agent can forget, which is why the backstop is not optional. |
| Bind to the execution's own lifecycle | No timer, but a wedged execution holds the Habitat until something else fences it. |

Selection criteria worth stating before choosing: it must be self-curing under
agent crash and under agent inattention; it must not fence a running
verification merely because a timer expired (ADR 0029 §2 — expiry is a lease
event, not a fencing event, so a reclaimed lease still goes through §7's protocol
before the Habitat is reassigned); and its worst case must be bounded by capacity
limits rather than by agent good behaviour.

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
7. Which Habitat lease-reclamation mechanism, per the section above?

## Related Documents

- [ADR 0029: Incubator And Habitat Execution Boundaries](../../adr/0029-incubator-and-habitat-execution-boundaries.md)
  — the resource split these contracts run in, and the one rule taken from here.
- [Pre-Phase-3 Blockers](plan_blockers.md) — item A1, and the rule that pre-entry
  design items produce an ADR and nothing else.
- [ADR 0019: Orchestrator Boundary](../../adr/0019-orchestrator-boundary.md) — why
  summarization is a boundary question.
- [ADR 0025: Golden Stories And The Benchmark Runner](../../adr/0025-golden-stories-and-benchmark-runner.md)
  — the evidence consumer that wants structured test results.
