# Coder Improvement Research Brief

## Purpose

This document is the updated handoff for the next round of Maestro coder analysis.

It is based on:

- A comparison between Maestro's native coder and an external coding-agent reference architecture
- Follow-up discussion about where the comparison was most and least relevant to Maestro
- Engineering review of the first-pass recommendations

The external system should be treated only as an **external reference architecture**.

The goal is **not** to make Maestro copy that system.

The goal is to help Maestro coders answer a narrower question:

> Which specific ideas appear likely to improve Maestro's unattended output quality, and what is the smallest Maestro-native version of each?

## Final Recommendation Summary

### Implement now

1. **Stage 1: Prompt refresh and prompt contract cleanup**
2. **Stage 4A: Narrow todo modernization**
3. **Stage 3A: Acceptance-criteria verification in `TESTING`**
4. **Stage 2: Targeted context/toolloop hardening**

### Prototype only

5. **Stage 3B: Adversarial probing in `TESTING`**

### Skip or defer for now

6. **Stage 5: Lightweight memory subsystem**
7. **Stage 6: Read-only sidecars and subagents**

## What the External Reference Architecture Got Right

Only the following behavior patterns are relevant for Maestro right now:

### 1. Evidence-oriented verification

The most useful idea was not "use a different review stage."

It was:

- verification should run commands
- verification should generate evidence
- verification should not trust code reading alone
- verification should try to falsify claims, not just confirm happy paths

For Maestro, that insight belongs primarily in `TESTING`, not `CODE_REVIEW`.

### 2. Better recovery when long stories go wrong

The reference architecture is stronger at:

- recovering after context overflow
- recovering after output truncation
- preserving critical state after compaction
- retrying with reconstructed state instead of just dropping context

This matters for Maestro even if context failures are rare, because those failures are expensive.

### 3. Better structured task handling

The useful idea here is not "make todos the source of truth."

The useful idea is:

- reduce redundant todo-generation passes
- make todo state richer than `completed` vs not
- keep task progression clearer inside coding

### 4. Better context hygiene

The most interesting sidecar/subagent idea was not speed.

It was:

- keep raw search/test noise out of the main reasoning context
- return distilled findings instead of artifacts

That remains interesting, but not yet urgent.

## What Maestro Already Does Better

These are strengths to preserve.

### 1. Explicit FSM and operational boundaries

Relevant files:

- `pkg/coder/coder_fsm.go`
- `pkg/coder/STATES.md`

Maestro's explicit state machine is a feature, not a liability.

### 2. Strong planning/coding separation

Relevant file:

- `pkg/coder/setup.go`

The read-only planning container and read-write coding container are good guardrails.

### 3. Structured blocker escalation

Relevant files:

- `pkg/tools/blocked_tool.go`
- `pkg/coder/planning.go`
- `pkg/coder/coding.go`

Maestro's blocked/error handling is already stronger than the external reference's looser loop model.

### 4. Knowledge graph retrieval

Relevant files:

- `pkg/coder/planning.go`
- `pkg/knowledge/retrieval.go`
- `docs/DOC_GRAPH.md`

For project architecture and repo truth, Maestro's knowledge graph is likely stronger than the external reference's lightweight memory model.

## Important Local Facts and Constraints

These points should shape the next round of analysis.

### 1. `.maestro` files already exist as a repo-backed instruction mechanism

Relevant files:

- `pkg/utils/maestro_files.go`
- `pkg/templates/renderer.go`
- `pkg/tools/spec_submit.go`
- `pkg/mirror/manager.go`

Current Maestro code already supports:

- `.maestro/MAESTRO.md`
- `.maestro/COMMON.md`
- `.maestro/CODER.md`
- `.maestro/ARCHITECT.md`

`RenderWithUserInstructions` appends the agent-specific instruction files to prompts, and `MAESTRO.md` is loaded into planning and coding prompts. There is also already a repo-backed update path for `MAESTRO.md`.

This means the immediate question is **not** whether Maestro needs an update mechanism at all.

The real questions are:

- Which agent should own updates to `.maestro/MAESTRO.md`, `COMMON.md`, `CODER.md`, and `ARCHITECT.md`?
- Should bootstrap ingest repo-local files such as `AGENTS.md` into `.maestro` files, or otherwise surface them in prompt construction?
- Should this be treated as prompt/bootstrap work rather than as a new memory subsystem?

### 2. Context loss is not the same as total data loss

Relevant files:

- `pkg/agent/tool_logging.go`
- `pkg/agent/toolloop/toolloop.go`
- `pkg/persistence/schema.go`
- `pkg/persistence/sessions.go`
- `pkg/coder/resume.go`
- `pkg/architect/driver.go`

Current Maestro already persists:

- tool execution records in the database
- agent context snapshots in `agent_contexts`
- context checkpoints for some error/debugging flows

So when context is compacted out of memory, that information is not necessarily gone forever.

This matters for Stage 2.

The next analysis should distinguish between:

- what must remain in live in-memory context for output quality
- what can safely live only in persistence for debugging, resume, or forensic inspection

It is reasonable to explore whether fuller context persistence would help debugging, but only if secrets are sanitized appropriately.

## Recommended Implementation Order

This is the current recommended order for implementation:

1. Stage 1 + Stage 4A: Prompt refresh, contract cleanup, and narrow todo modernization
2. Stage 2A: Pre-call compaction check (practical prerequisite for Stage 3A)
3. Stage 3A: Acceptance-criteria verification in `TESTING`
4. Stage 2B: State re-injection after compaction + tool error circuit breaker
5. Stage 3B: Bounded adversarial probing in `TESTING`
6. Revisit Stage 5 or Stage 6 only if operating evidence justifies them

Stage 2A is split out because the new `TESTING` verification loop in Stage 3A will immediately depend on compaction behaving correctly. The rest of Stage 2 can follow afterward.

## Stage 1: Prompt Refresh and Prompt Contract Cleanup

### Status

`implement now`

### Why this is first

This is the cheapest and clearest win.

It fixes a real mismatch in today's system rather than speculating about future architecture.

### Current Maestro areas to inspect

- `pkg/templates/coder/app_planning.tpl.md`
- `pkg/templates/coder/app_coding.tpl.md`
- `pkg/templates/architect/code_review.tpl.md`
- `pkg/templates/coder/code_review_request.tpl.md`
- `pkg/tools/planning_tools.go`
- `pkg/coder/todo_collection.go`

### Confirmed issues

#### 1. Plan/todo contract mismatch

The planning prompt currently says `submit_plan` requires todos, but the actual `submit_plan` schema does not include them.

This is a concrete mismatch and should be treated as a bug.

#### 2. `CODE_REVIEW` is not the weakest prompt

The architect review template is already fairly strong.

The weaker prompt link is the coder-side review request, especially its vague evidence section.

That means the highest-value prompt cleanup is likely:

- fix the plan/todo mismatch
- improve structured evidence expectations in `code_review_request.tpl.md`
- strengthen truthful verification language in `app_coding.tpl.md`

### What not to do

- Do not rewrite the architect review prompt just because it is a prompt
- Do not over-rotate on generic "be careful" language
- Do not defer the plan/todo mismatch because a later stage might also touch todos

### Known live prompt files

The following templates are active in the current runtime path:

- `pkg/templates/coder/app_planning.tpl.md` — planning prompt
- `pkg/templates/coder/app_coding.tpl.md` — coding prompt
- `pkg/templates/coder/code_review_request.tpl.md` — coder-side review request
- `pkg/templates/architect/code_review.tpl.md` — architect-side review

The testing template (`pkg/templates/coder/testing.tpl.md`) exists and is registered in `pkg/templates/renderer.go`, but current `TESTING` behavior is the deterministic flow in `pkg/coder/testing.go`. The template is not rendered or sent to an LLM during normal TESTING execution.

### Questions for coders

1. What exact prompt edits fix the plan/todo mismatch?
2. How should coder-side review evidence be structured?
3. What is the smallest wording change that improves truthful reporting without adding noise?

### Expected deliverable

Produce:

- a prompt audit
- a list of exact prompt/tool mismatches
- a concrete prompt edit proposal
- a recommendation: `implement now`

## Stage 4A: Narrow Todo Modernization

### Status

`implement now`

### Why this moved up

This stage is now intentionally narrow because it fixes the same high-value contract problem exposed in Stage 1.

### Current Maestro areas to inspect

- `pkg/tools/planning_tools.go`
- `pkg/coder/plan_review.go`
- `pkg/coder/todo_collection.go`
- `pkg/tools/todo_tools.go`
- `pkg/coder/todo_handlers.go`
- `pkg/coder/coding.go`

### Recommended scope

The analysis should assume the likely target is:

1. Add `todos` to `submit_plan`
2. Carry todos directly out of planning
3. Eliminate the separate todo-collection LLM pass
4. Add at least an `in_progress` state to todo items

### Important constraints

Do **not** recommend:

- making verification a todo item
- elaborate dependency graphs between todos
- turning todos into the primary coordination mechanism of the whole agent

Maestro's FSM and state keys should remain the real coordination mechanism.

### Questions for coders

1. What changes are needed in `submit_plan` schema and process-effect handling?
2. What is the simplest todo-state expansion that improves coding behavior?
3. What logic in `plan_review.go` and `todo_collection.go` becomes unnecessary if todos come directly from planning?
4. What migration path minimizes disruption to existing tests and state handling?

### Expected deliverable

Produce:

- a current-state map of the todo flow
- a minimal design for folding todos into `submit_plan`
- a recommendation: `implement now`

## Stage 3A: Acceptance-Criteria Verification in `TESTING`

### Status

`implement now`

### Why this is the most important medium-complexity change

Right now `TESTING` is primarily deterministic.

It runs backend/build/infrastructure checks and routes pass/fail, but it does not appear to perform acceptance-criteria verification with an LLM in the main runtime path.

That means Maestro can still approve work that:

- compiles
- passes existing tests
- does not actually satisfy the story's acceptance criteria

### Current Maestro areas to inspect

- `pkg/coder/testing.go`
- `pkg/templates/coder/testing.tpl.md`
- `pkg/coder/code_review.go`
- `pkg/templates/architect/code_review.tpl.md`
- `docs/TESTING_STRATEGY.md`
- `docs/specs/TESTING.md`

### Recommended scope

The likely target is:

1. Keep today's deterministic baseline checks
2. After baseline checks pass, run a fresh-context verification loop
3. That loop should read:
   - story task and acceptance criteria
   - approved plan
   - changed files or implementation summary
   - deterministic test/build results
   - available project commands
4. The loop should produce structured evidence for `CODE_REVIEW`

This stage is about:

- "did we actually do what was asked?"

It is not yet about:

- broad adversarial fuzzing
- infinite attempts to break the system

### Hard bounds

The verification loop in `TESTING` must be explicitly bounded:

- **Max 5 LLM turns** — this is a verification pass, not an open-ended conversation
- **Read-only verification only** — no code edits, no file writes, no self-healing inside `TESTING`
- **No self-healing** — verification failures route back to `CODING`, not into a fix loop within `TESTING`
- **`TESTING` produces evidence, it does not become a second coding loop**

### Strawman evidence schema

To prevent coders from spending excessive time on schema design, start from this structure:

```json
{
  "deterministic_results": {
    "build": "pass|fail|not_run",
    "tests": "pass|fail|not_run",
    "lint": "pass|fail|not_run",
    "summary": "..."
  },
  "acceptance_criteria_checked": [
    {
      "criterion": "...",
      "method": "command|inspection",
      "result": "pass|fail|partial|unverified",
      "evidence": "command/output or concise rationale"
    }
  ],
  "gaps": ["..."],
  "confidence": "high|medium|low"
}
```

This is a starting point, not a final spec. Coders should refine it based on what information `CODE_REVIEW` actually needs.

### Important architectural guidance

This verification belongs in `TESTING`, not `CODE_REVIEW`.

`CODE_REVIEW` should stay the architect approval gate that consumes better evidence.

### Questions for coders

1. Is `pkg/templates/coder/testing.tpl.md` currently used in the live path, or is it dead/spec-only?
2. What evidence schema should `TESTING` output for later review?
3. What is the smallest fresh-context testing loop that materially improves confidence?
4. What kinds of acceptance criteria can be mapped to commands deterministically vs LLM-guided?

### Expected deliverable

Produce:

- a current-state map of `TESTING`
- a target-state design for acceptance-criteria verification
- a structured evidence schema proposal
- a recommendation: `implement now`

## Stage 2: Targeted Context and Toolloop Hardening

### Status

`implement now`, but with deliberately narrow scope and split into two tranches:

- **Stage 2A** (pre-call compaction check): implement before or alongside Stage 3A
- **Stage 2B** (state re-injection + tool circuit breaker): implement after Stage 3A

### Important framing

This stage is a robustness play for tail-case failures.

It should not be allowed to expand into a general rewrite of context management.

Also note:

- at least one expensive historical failure was primarily a prompt/completion-criteria problem rather than a context-limit problem
- that is another reason this stage should stay narrow

### Current Maestro areas to inspect

- `pkg/agent/toolloop/toolloop.go`
- `pkg/contextmgr/contextmgr.go`
- `docs/CONTEXT_MANAGEMENT.md`
- `docs/CONTEXT_ISSUE_NOTES.md`
- `docs/PROMPT_CACHING_IMPLEMENTATION_PLAN.md`
- `docs/TOOL_LOOP.md`
- `pkg/persistence/schema.go`
- `pkg/persistence/sessions.go`

### Recommended scope

The next analysis should focus on exactly these three items:

#### 1. Pre-LLM-call compaction check

Confirm whether compaction should fire earlier and more predictably before an API call.

#### 2. State-summary re-injection after compaction

When compaction removes messages, inject a synthetic summary that preserves key working state, such as:

- current FSM state
- approved plan summary
- current todo status
- recent architect feedback
- last test results

#### 3. Tool error circuit breaker

Prevent endless retries on the same failing tool pattern.

At minimum, investigate a cap such as:

- same tool
- same failure shape
- repeated several times in a row

### Persistence note

This stage should explicitly account for existing persistence support:

- tool executions are already logged
- `agent_contexts` snapshots already exist
- checkpoints already exist for some debugging flows

Coders should evaluate whether those mechanisms are sufficient for debugging, or whether a more complete context event stream would help, provided secrets can be sanitized.

### Explicitly out of scope for now

Do **not** recommend any of the following in this stage unless operating evidence strongly demands it:

- full checkpoint/resume redesign
- adaptive escalation based on context usage
- snapshot-plus-delta architecture rewrite
- full event-sourced context system

### Questions for coders

1. What is the current compaction timing, exactly?
2. What state is most damaging to lose after compaction?
3. How should state re-injection be represented in context?
4. What is the safest error-fingerprint definition for a tool circuit breaker?
5. How much debugging value would be added by richer persisted context, beyond current snapshots and tool logs?

### Expected deliverable

Produce:

- a narrow design note for the three targeted items
- an assessment of existing persistence coverage
- a recommendation: `implement now`

## Stage 3B: Adversarial Probing in `TESTING`

### Status

`prototype`

### Why this is not in the first implementation tranche

This is valuable, but it is easier to overscope than acceptance-criteria verification.

It needs careful limits so it does not become:

- an endless loop of trying edge cases
- an unbounded source of cost
- a duplicate of `CODE_REVIEW`

### Recommended scope

Prototype only a bounded version of adversarial checks, for example:

- malformed input
- boundary values
- repeated/idempotent operations
- missing-resource or orphan-operation checks

This should be:

- risk-tiered
- iteration-limited
- justified by story type or change type

### Questions for coders

1. Which story types justify adversarial probes?
2. What iteration or budget cap keeps this safe?
3. Should adversarial probes be optional or automatically triggered for selected change classes?
4. How should adversarial findings be represented in the testing evidence packet?

### Expected deliverable

Produce:

- a prototype design
- explicit scope limits
- a recommendation: `prototype`

## Stage 5: Lightweight Memory

### Status

`skip for now`

### Why

At present, Maestro already has:

- a knowledge graph
- `.maestro/MAESTRO.md`
- `.maestro/COMMON.md`
- `.maestro/CODER.md`
- `.maestro/ARCHITECT.md`
- session persistence

That likely covers the highest-value cases without adding a new subsystem.

If concrete gaps emerge later, they should first be tested against:

- extending `.maestro` files
- bootstrap ingestion of repo-local instruction files
- extending the knowledge graph where appropriate

### Revisit condition

Only revisit this if operating evidence shows recurring, high-value context that:

- is not derivable from code
- does not fit cleanly in `.maestro` files
- does not belong in the knowledge graph

## Stage 6: Read-Only Sidecars and Subagents

### Status

`skip for now`

### Why

This is the most complex idea and not currently the best ROI.

Most of the likely quality value can probably be captured earlier by:

- stronger `TESTING`
- better prompt contracts
- targeted context/toolloop hardening

### Revisit condition

Only revisit this if, after Stages 1, 3A, 4A, and targeted Stage 2 work, operating evidence still shows that:

- context pollution is hurting output quality
- a fresh-context verification or exploration sidecar would clearly help

If revisited later, start with read-only verifier or explorer roles only.

## Standard Output Expected From Coders

For each stage that is still active, coders should produce the same structured output.

### Required sections

1. **Current Maestro behavior**
2. **Exact problem statement**
3. **Smallest Maestro-native change**
4. **Risks and tradeoffs**
5. **Recommendation**
   - `implement now`
   - `prototype`
   - `skip`

### Strong preference

Coders should prefer:

- small, testable changes
- explicit file-level grounding
- recommendations that reinforce the FSM rather than bypass it

## Final Guidance

The most useful lessons from the external reference architecture are now narrower than the first draft of this brief suggested.

The current recommended implementation sequence is:

1. fix the prompt and plan/todo contract bugs, eliminate the redundant todo-collection pass (Stage 1 + 4A)
2. land pre-call compaction check as a prerequisite for the new TESTING loop (Stage 2A)
3. make `TESTING` verify acceptance criteria with a bounded, read-only LLM loop (Stage 3A)
4. add state re-injection and tool circuit breaker (Stage 2B)
5. prototype bounded adversarial probing (Stage 3B)
6. defer memory and subagent work unless operating evidence later proves the need
