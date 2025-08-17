# Architect Agent Finite-State Machine (Canonical)

*Last updated: 2025-07-24 (rev E - Shutdown Handling)*

This document is the **single source of truth** for the architect agent's workflow.
Any code, tests, or diagrams must match this specification exactly.

---

## Mermaid diagram

```mermaid
stateDiagram-v2
    %% ---------- ENTRY ----------
    [*] --> WAITING              

    %% ---------- SPEC INTAKE ----------
    WAITING       --> SCOPING            : spec received
    WAITING       --> REQUEST            : question received  
    WAITING       --> ERROR              : channel closed/abnormal shutdown
    SCOPING       --> DISPATCHING        : initial "ready" stories queued
    SCOPING       --> ERROR              : unrecoverable scoping error

    %% ---------- STORY DISPATCH ----------
    DISPATCHING   --> MONITORING         : ready stories placed on work-queue
    DISPATCHING   --> DONE               : no stories left ⭢ all work complete

    %% ---------- MAIN EVENT LOOP ----------
    MONITORING    --> REQUEST            : any coder request\n(question • plan • iter/tokens • code-review • merge)
    MONITORING    --> ERROR              : channel closed/abnormal shutdown
    
    %% ---------- REQUEST HANDLING ----------
    REQUEST       --> MONITORING         : approve (non-code) • request changes  
    REQUEST       --> ESCALATED          : cannot answer → ask human
    REQUEST       --> ERROR              : abandon / unrecoverable

    %% ---------- HUMAN ESCALATION ----------
    ESCALATED     --> REQUEST            : human answer supplied
    ESCALATED     --> ERROR              : timeout / no answer

    %% ---------- TERMINALS ----------
    DONE          --> WAITING            : new spec arrives
    ERROR         --> WAITING            : recovery / restart

    %% ---------- NOTES ----------
    %% Self-loops (state → same state) are always valid and are used for states that wait for external events.
```

---

## State definitions

| State            | Purpose                                                                        |
| ---------------- | ------------------------------------------------------------------------------ |
| **WAITING**      | Agent is idle, waiting for specification files to process.                    |
| **SCOPING**      | Parse specification and generate story files with dependencies.               |
| **DISPATCHING**  | Load stories, check dependencies, and assign ready stories to coder agents.   |
| **MONITORING**   | Monitor coder progress and wait for requests (questions, reviews, merges).    |
| **REQUEST**      | Process coder requests: questions, plan/code reviews.                         |
| **ESCALATED**    | Waiting for human intervention on complex business questions.                 |
| **DONE**         | All stories completed successfully.                                           |
| **ERROR**        | Unrecoverable error or workflow abandonment.                                  |

---

## Key workflow changes (Merge Workflow)

- **Merge requests**: Coders send merge requests after code approval via REQUEST state
- **Story completion**: Only happens after successful PR merge (not code approval)
- **Dependency unlocking**: Triggered by merge success, enabling dependent stories
- **Conflict handling**: Merge conflicts returned to coder for resolution

---

## Error handling

* The agent enters **ERROR** when:

  1. It receives **ABANDON** from escalation or unrecoverable runtime errors.
  2. Specification parsing fails with unrecoverable errors.
  3. Any unrecoverable runtime error occurs (panic, out-of-retries, etc.).

* **ERROR** is terminal until recovery/restart by orchestrator.

## Shutdown handling

* The agent enters **DONE** when:

  1. All stories are completed successfully (normal completion via DISPATCHING state).

* The agent enters **ERROR** when:

  1. It receives **ABANDON** from escalation or unrecoverable runtime errors.
  2. Specification parsing fails with unrecoverable errors.
  3. Any unrecoverable runtime error occurs (panic, out-of-retries, etc.).
  4. Channels are closed unexpectedly during shutdown (abnormal termination).

* **Channel closure detection**: When channels are closed during shutdown, the architect detects this via the two-value receive pattern `msg, ok := <-ch`. When `!ok`, it transitions to **ERROR** state for proper cleanup.

* **DONE** is terminal - the orchestrator handles agent cleanup and restart.

---

*Any deviation from this document is a bug.*

