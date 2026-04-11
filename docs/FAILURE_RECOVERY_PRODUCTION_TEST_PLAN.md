# Failure Recovery V2 Production Test Plan

## Status

Drafted for execution against Failure Recovery V2 after completion of:

- Phase 1: circuit breaker removal, `on_hold`, retry budgets, failure persistence
- Phase 2: failure kind/scope routing, scope widening, dispatch suppression, TESTING classification
- Phase 2.5: PM clarification flow, manual release via `release_held_stories`, repair completion signaling

This plan is for **rigorous production-path testing** of failure handling. It is not a scale-analysis plan; Phase 3 remains the right place for analytics normalization and long-term pattern mining.

---

## Purpose

Validate that Maestro no longer strands users or thrashes indefinitely when story execution fails.

Specifically, this plan verifies that:

1. Failures are classified into the correct recovery lane.
2. Recovery behavior matches the designed blast radius.
3. Blocked work is contained without halting unrelated work.
4. Human-required failures pause cleanly and resume only when explicitly released.
5. Retry exhaustion fails only the affected story, not the whole architect run.
6. Restart/resume preserves enough failure context to continue safely.

---

## Scope

This plan covers:

- `story_invalid`, `environment`, `prerequisite`, and `transient` failures
- `attempt`, `story`, `epoch`, and `system` scopes
- Detection from PLANNING, CODING, TESTING, commit/merge flow, and SUSPEND flow
- Multi-story hold, in-flight coder cancellation, dispatch suppression, and manual release
- Persistence and resume behavior for held stories and failure budgets

This plan does **not** attempt to validate:

- large-scale analytics quality
- failure signature quality beyond basic operational usefulness
- arbitrary chaos engineering against unrelated subsystems
- broad customer rollout under real user traffic

---

## Entry Criteria

Do not start this plan until all of the following are true:

1. The target branch is merged or otherwise frozen for the test window.
2. The workspace is clean and the exact build under test is recorded.
3. Package-level verification passes:
   - `go test ./pkg/architect ./pkg/pm ./pkg/proto ./pkg/persistence ./internal/supervisor ./pkg/dispatch`
   - `go test ./pkg/tools -run 'TestReportBlockedTool|TestClassifyCommitFailure'`
4. The environment has working chat, persistence, build service, and dispatcher wiring.
5. The operators running the test can inspect:
   - Maestro logs
   - the `stories` table
   - the `failures` table
   - PM chat messages
6. A small internal test repo exists with known stories that can be made to fail deterministically.

Recommended test repo characteristics:

- At least 5 stories across 2 specs
- At least 2 independent stories that can run concurrently
- At least 1 story with a dependency chain
- A simple app test suite that can be made to fail in controlled ways

---

## Test Environment

Use a **production-like internal environment**, not real end-user traffic.

Recommended setup:

- Real persistence database
- Real PM/architect/coder orchestration
- Real dispatcher and supervisor
- Real chat path used by PM
- Real build/test flow
- Controlled internal repository and secrets

Do not start with public/customer-facing traffic. The first pass should be an internal operator-run campaign where failures are deliberately injected.

---

## Required Observability

For every executed scenario, capture:

- session ID
- spec ID
- story ID(s)
- failure ID(s)
- triggering agent ID
- failure kind
- resolved scope
- hold reason
- whether dispatch suppression was active
- final story status
- final failure resolution status

Minimum evidence to save for each scenario:

1. architect log excerpt
2. supervisor log excerpt
3. PM/user chat excerpt if human interaction is involved
4. `stories` table snapshot before and after
5. `failures` table rows for the scenario

If a scenario fails, also capture:

- whether retry churn occurred
- whether unrelated stories stopped unexpectedly
- whether any in-flight coder continued after it should have been cancelled
- whether PM had enough context to recover the run manually

---

## Roles

- **Operator**: triggers failures and records evidence
- **Observer**: watches logs/db state and confirms pass/fail
- **PM test actor**: responds as the human when a clarification path is tested

One person may play multiple roles for small runs, but the evidence must still be recorded.

---

## Execution Model

Run the plan in four stages:

1. **Smoke**
   - Confirm basic healthy execution still works.
2. **Single-story failure paths**
   - Validate each kind at `attempt` or `story` scope.
3. **Wide-scope containment**
   - Validate `epoch` and `system` behaviors, including in-flight cancellation.
4. **Restart and recovery durability**
   - Validate held/suppressed work across orchestrator restart or resume.

Do not advance to the next stage if a blocking failure appears in the current one.

---

## Global Pass Criteria

The overall campaign passes only if all of the following are true:

1. No scenario causes architect-wide fatal shutdown unless the plan explicitly calls for fatal behavior.
2. No prerequisite or system repair scenario causes retry churn while waiting for human action.
3. No `epoch` or `system` hold leaves affected in-flight coders running.
4. No system-scoped repair allows new dispatch while suppression is active.
5. Held stories can be released and restarted cleanly.
6. Resume preserves held status and budget state well enough to continue safely.
7. PM receives enough context to explain the issue and unblock the run.
8. Failure records are created and updated for every tested failure lane.

---

## Blocking Conditions

Stop the campaign and treat the run as failed if any of these occur:

- architect enters fatal `ERROR` because one story exhausted retries
- held stories silently return to active execution without release
- system suppression is active but a new coder is dispatched anyway
- a cancelled in-flight coder continues modifying a held story
- PM cannot recover a held story because the failure/release context is missing
- restart/resume drops held state or resets recovery in a way that causes duplicate unsafe retries

---

## Scenario Matrix

Each scenario below should be executed at least once. The high-risk scenarios marked `Critical` should be executed twice on separate sessions.

| ID | Priority | Scenario |
| --- | --- | --- |
| FR-01 | Critical | `story_invalid` reported in PLANNING rewrites story and restarts cleanly |
| FR-02 | Critical | `story_invalid` reported in CODING rewrites story and restarts cleanly |
| FR-03 | High | attempt-scoped `environment` failure in CODING retries with fresh workspace |
| FR-04 | High | attempt-scoped `environment` failure in TESTING routes through recovery instead of looping in coder |
| FR-05 | High | commit-time `environment` auto-classification routes through blocked recovery |
| FR-06 | Critical | story-scoped `prerequisite` failure holds story, PM asks human, story resumes only after `release_held_stories` |
| FR-07 | Critical | system-scoped `environment` failure suppresses dispatch and holds affected work |
| FR-08 | Critical | epoch-scoped failure holds all affected stories in the spec and cancels in-flight coders |
| FR-09 | High | repeated similar failures trigger scope widening |
| FR-10 | High | unrelated failures of the same top-level kind do not widen together |
| FR-11 | High | retry exhaustion marks only the story failed and architect continues |
| FR-12 | High | held stories survive restart/resume with correct metadata and budgets |
| FR-13 | High | transient SUSPEND creates a failure record and updates on resume |
| FR-14 | Medium | manual release by `failure_id` only releases targeted held stories |
| FR-15 | Medium | release without `failure_id` releases all held stories when explicitly intended |
| FR-16 | Medium | deprecated `external` input still normalizes to `environment` |

---

## Detailed Test Cases

### FR-01: `story_invalid` in PLANNING

Goal:
- Validate early blocked reporting before coding starts.

Setup:
- Use a story with contradictory or impossible requirements.

Trigger:
- Have the coder call `report_blocked(failure_kind=story_invalid)` during PLANNING.

Expected behavior:

- coder transitions to `ERROR`
- supervisor requeues with structured `FailureInfo`
- architect resolves scope to `story`
- story goes `on_hold`
- architect rewrites the story
- story releases back to `pending`
- a fresh coder attempt starts
- PM receives informational blocked notice, not human-action notice

Pass criteria:

- no retry churn before rewrite completes
- no architect fatal shutdown
- rewritten story content differs in a way that resolves the contradiction

### FR-02: `story_invalid` in CODING

Same as FR-01, but trigger during active implementation rather than planning.

Additional pass criteria:

- any active coder attempt is replaced by a fresh attempt
- old plan/context is not resumed in place

### FR-03: attempt-scoped `environment` in CODING

Goal:
- Validate workspace-local recovery without human involvement.

Trigger examples:

- corrupt the local workspace
- break a local checkout state
- inject a deterministic filesystem/toolchain failure

Expected behavior:

- failure classified as `environment`
- scope resolves to `attempt`
- story is retried with a fresh attempt
- no PM action required
- no other stories are held

Pass criteria:

- next attempt starts cleanly
- unrelated stories continue
- failure record shows retry path

### FR-04: attempt-scoped `environment` in TESTING

Goal:
- Validate procedural test-failure classification.

Trigger examples:

- make test execution fail with a known infrastructure signature such as missing executable, permission issue, or broken docker runtime

Expected behavior:

- TESTING transitions to `ERROR` with `FailureInfo`
- recovery follows architect path rather than looping back to CODING

Pass criteria:

- no silent return to CODING for infrastructure-only failure
- story requeues as a fresh attempt

### FR-05: commit-time auto-classified `environment`

Goal:
- Validate blocked recovery when `done`/commit fails after implementation.

Trigger examples:

- induce git corruption or a deterministic commit-time environment failure

Expected behavior:

- commit failure auto-classifies
- blocked recovery path activates
- no overnight stall in CODING

Pass criteria:

- failure shows in `failures` table
- story does not remain stuck in coder state

### FR-06: story-scoped `prerequisite`

Goal:
- Validate human-required recovery.

Trigger examples:

- expired API key
- revoked access token
- missing credential required by the story

Expected behavior:

- story goes `on_hold` with `awaiting_human`
- no automatic retry occurs
- PM receives clarification request with enough context
- PM asks human
- after simulated human fix, PM calls `release_held_stories`
- architect releases the story and it restarts fresh

Pass criteria:

- zero retry churn while waiting
- PM message accurately explains what is needed
- release resumes only the intended story/stories

### FR-07: system-scoped `environment`

Goal:
- Validate global freeze during shared-environment failure.

Trigger examples:

- shared mirror corruption
- globally broken toolchain dependency
- deterministic startup/bootstrap failure that affects all work

Expected behavior:

- dispatch suppression becomes active
- affected stories are held
- new stories are not dispatched
- PM is informed of high-urgency repair need
- release resumes dispatch only after explicit PM/operator action

Pass criteria:

- no new coder dispatch while suppression is active
- release returns held stories to `pending`
- dispatch resumes after release

### FR-08: epoch-scoped hold with in-flight cancellation

Goal:
- Validate that broader failures actually contain active work.

Setup:

- At least 3 stories in the same spec
- At least 2 of them actively in PLANNING/CODING

Trigger:

- cause a failure that resolves or widens to `epoch`

Expected behavior:

- affected stories in the same spec are held
- in-flight coders on those stories are cancelled/restarted
- unaffected stories outside the scope continue

Pass criteria:

- no held story keeps running
- affected agent leases are cleared
- unaffected independent work is not unnecessarily frozen

### FR-09: repeated similar failures widen scope

Goal:
- Validate scope widening for repeated same-cause failures.

Trigger:

- cause 3 materially similar failures within the widening window

Expected behavior:

- scope widens according to the configured ladder

Pass criteria:

- widened scope appears in logs/failure data
- subsequent routing uses the widened scope

### FR-10: unrelated failures do not widen together

Goal:
- Guard against false positives.

Trigger:

- cause multiple `environment` failures with different root causes/explanations

Expected behavior:

- no widening if the failures are unrelated

Pass criteria:

- scope remains narrow
- healthy work is not paused unnecessarily

### FR-11: retry exhaustion is story-local

Goal:
- Validate the old architect-wide circuit breaker is gone.

Trigger:

- exhaust retry budget on one story

Expected behavior:

- story becomes `failed`
- architect continues managing remaining stories
- PM is notified of the abandoned story

Pass criteria:

- architect does not enter fatal `ERROR`
- other ready stories continue

### FR-12: restart/resume durability

Goal:
- Validate persistence of held/recovery state.

Setup:

- create a session with:
  - one held story
  - one failed story
  - one story with consumed retry budget

Trigger:

- stop Maestro and restart in resume mode

Expected behavior:

- held story is still held with metadata
- budgets reconstruct from failure records
- release/retry behavior remains correct

Pass criteria:

- no unsafe budget reset
- no loss of hold metadata

### FR-13: transient SUSPEND adapter

Goal:
- Validate the transient lane participates in the same failure record pipeline.

Trigger:

- induce a suspend-worthy upstream outage

Expected behavior:

- agent enters SUSPEND
- transient failure record is created
- on recovery, record updates to succeeded

Pass criteria:

- no duplicate/error requeue while suspended
- failure resolution updates correctly

### FR-14: targeted release by `failure_id`

Goal:
- Validate precise operator control.

Setup:

- multiple held stories from multiple failures

Trigger:

- PM calls `release_held_stories` with one `failure_id`

Expected behavior:

- only stories linked to that failure release

Pass criteria:

- unrelated held stories stay held

### FR-15: global release without `failure_id`

Goal:
- Validate the explicit bulk-release path.

Expected behavior:

- all held stories release only when PM intentionally omits `failure_id`

Pass criteria:

- operator can recover broad holds after known global repair

### FR-16: backward compatibility for `external`

Goal:
- Validate legacy callers do not break recovery.

Trigger:

- submit `report_blocked(failure_kind=external)`

Expected behavior:

- normalized to `environment`
- routes through modern path

Pass criteria:

- no schema or routing failure

---

## Suggested Execution Order

Run in this order:

1. FR-01
2. FR-02
3. FR-03
4. FR-04
5. FR-05
6. FR-06
7. FR-11
8. FR-09
9. FR-10
10. FR-08
11. FR-07
12. FR-14
13. FR-15
14. FR-12
15. FR-13
16. FR-16

Rationale:

- start narrow and story-local
- validate human gating before wide holds
- validate wide holds before restart/resume
- validate backward compatibility last

---

## Failure Injection Guidance

Use deterministic triggers where possible.

Recommended techniques:

- Contradictory story text for `story_invalid`
- Test fixture that references a missing executable or permission-locked file for TESTING environment failures
- Controlled credential rotation/revocation for prerequisite failures
- Operator-controlled shared breakage for system-scoped environment failure
- Repeated same-cause synthetic failures to test scope widening

Avoid:

- random host damage
- production secrets
- customer repositories
- non-repeatable failure triggers

---

## Evidence Template

For each scenario, record:

```text
Scenario ID:
Session ID:
Spec ID:
Story IDs:
Failure IDs:
Trigger:
Expected path:
Observed path:
Pass/Fail:
Notes:
Links to logs/screenshots/db snapshots:
```

---

## Go/No-Go Decision

### Go

Proceed to limited internal production-path testing if:

- all Critical scenarios pass
- no blocking conditions occur
- no architect-wide fatality is observed from story failure handling
- no retry churn occurs in human-gated scenarios
- no containment failures occur in `epoch` or `system` scope

### No-Go

Do not expose to end users yet if:

- any Critical scenario fails
- PM/operator release path is ambiguous in practice
- held stories resume incorrectly
- scope widening pauses healthy work in normal runs

---

## After This Plan

If this plan passes, the next step is:

1. short internal soak run with intentionally mixed normal + failure traffic
2. limited dogfood usage
3. only then small external exposure

If this plan fails, log each failure against the recovery taxonomy:

- wrong classification
- wrong scope
- wrong containment
- wrong PM/human UX
- wrong persistence/resume behavior
- wrong final outcome

That classification should drive the next fix cycle.

---

## References

- [Failure Recovery V2 Spec](/Users/dratner/Code/maestro/docs/FAILURE_RECOVERY_V2_SPEC.md)
- [Testing Strategy](/Users/dratner/Code/maestro/docs/TESTING_STRATEGY.md)
