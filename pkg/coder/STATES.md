Here’s the complete current **STATES.md** content, inline for easy copy-paste:

---

# Coder Agent Finite-State Machine (Canonical)

*Last updated: 2025-07-18 (rev E - Cleanup State for Multi-Story Workflow)*

This document is the **single source of truth** for the coder agent’s workflow.
Any code, tests, or diagrams must match this specification exactly.

---

## Mermaid diagram

```mermaid
stateDiagram-v2
    %% Entry & idle
    [*] --> WAITING

    %% We got work
    WAITING --> SETUP                  : receive task
    SETUP   --> PLANNING               : workspace ready
    SETUP   --> ERROR                  : workspace setup failed

    %% Planning phase
    PLANNING      --> PLAN_REVIEW      : submit plan
    PLANNING      --> QUESTION         : clarification

    PLAN_REVIEW   --> CODING           : approve
    PLAN_REVIEW   --> PLANNING         : changes
    PLAN_REVIEW   --> ERROR            : abandon/error 

    %% Coding / fixing loop
    CODING        --> TESTING          : code complete
    CODING        --> QUESTION         : clarification
    CODING        --> BUDGET_REVIEW    : budget exceeded
    CODING        --> ERROR            : unrecoverable error 

    TESTING       --> CODE_REVIEW      : tests pass
    TESTING       --> FIXING           : tests fail

    FIXING        --> TESTING          : fix done
    FIXING        --> QUESTION         : clarification
    FIXING        --> BUDGET_REVIEW    : budget exceeded
    FIXING        --> ERROR            : unrecoverable error 

    %% Code review & merge workflow
    CODE_REVIEW   --> AWAIT_MERGE      : approve & send merge request
    CODE_REVIEW   --> FIXING           : changes
    CODE_REVIEW   --> ERROR            : abandon/error
    
    AWAIT_MERGE   --> CLEANUP          : merge successful
    AWAIT_MERGE   --> FIXING           : merge conflicts 

    %% Budget review (budget exceeded)
    BUDGET_REVIEW --> CODING           : continue/pivot
    BUDGET_REVIEW --> FIXING           : continue/pivot
    BUDGET_REVIEW --> CODE_REVIEW      : escalate
    BUDGET_REVIEW --> ERROR            : abandon/error

    %% Clarification loop
    QUESTION      --> PLANNING         : answer design Q
    QUESTION      --> CODING           : return to coding
    QUESTION      --> FIXING           : return to fixing
    QUESTION      --> ERROR            : abandon/error 

    %% Multi-story workflow - cleanup and return to WAITING
    CLEANUP       --> WAITING          : cleanup complete
    CLEANUP       --> DONE             : architect shutdown signal
    ERROR         --> WAITING

    %% Terminal state
    DONE          --> [*]
```

---

## State definitions

| State               | Purpose                                                                        |
| ------------------- | ------------------------------------------------------------------------------ |
| **WAITING**         | Agent is idle, waiting for the orchestrator to assign new work.                |
| **SETUP**           | Initialize Git worktree and create story branch.                               |
| **PLANNING**        | Draft a high-level implementation plan.                                        |
| **PLAN\_REVIEW**    | Architect reviews the plan and either approves, requests changes, or abandons. |
| **CODING**          | Implement the approved plan.                                                   |
| **TESTING**         | Run the automated test suite.                                                  |
| **FIXING**          | Address test failures, review changes, or merge conflicts.                     |
| **CODE\_REVIEW**    | Architect reviews the code and either approves, requests changes, or abandons. |
| **BUDGET\_REVIEW**  | Architect reviews budget exceeded request and decides how to proceed. |
| **AWAIT\_MERGE**    | Waiting for architect to merge PR after code approval.                        |
| **CLEANUP**         | Clean up workspace and container resources after successful story completion.  |
| **QUESTION**        | Awaiting external clarification or information.                                |
| **DONE**            | Agent termination state - no more work will be assigned.                       |
| **ERROR**           | Task abandoned or unrecoverable failure encountered.                           |

---

## Allowed transitions (tabular)

| From \ To           | WAITING | SETUP | PLAN\_REVIEW | PLANNING | CODING | TESTING | FIXING | CODE\_REVIEW | BUDGET\_REVIEW | AWAIT\_MERGE | CLEANUP | QUESTION | DONE | ERROR |
| ------------------- | ------- | ----- | ------------ | -------- | ------ | ------- | ------ | ------------ | -------------- | ------------ | ------- | -------- | ---- | ----- |
| **WAITING**         | –       | ✔︎    | –            | –        | –      | –       | –      | –            | –              | –            | –       | –        | –    | –     |
| **SETUP**           | –       | –     | –            | ✔︎       | –      | –       | –      | –            | –              | –            | –       | –        | –    | ✔︎    |
| **PLANNING**        | –       | –     | ✔︎           | –        | –      | –       | –      | –            | –              | –            | –       | ✔︎       | –    | –     |
| **PLAN\_REVIEW**    | –       | –     | –            | ✔︎       | ✔︎     | –       | –      | –            | –              | –            | –       | –        | –    | ✔︎    |
| **CODING**          | –       | –     | –            | –        | –      | ✔︎      | –      | –            | ✔︎             | –            | –       | ✔︎       | –    | ✔︎    |
| **TESTING**         | –       | –     | –            | –        | –      | –       | ✔︎     | ✔︎           | –              | –            | –       | –        | –    | –     |
| **FIXING**          | –       | –     | –            | –        | –      | ✔︎      | –      | –            | ✔︎             | –            | –       | ✔︎       | –    | ✔︎    |
| **CODE\_REVIEW**    | –       | –     | –            | –        | –      | –       | ✔︎     | –            | –              | ✔︎           | –       | –        | –    | ✔︎    |
| **BUDGET\_REVIEW**  | –       | –     | –            | –        | ✔︎     | –       | ✔︎     | ✔︎           | –              | –            | –       | –        | –    | ✔︎    |
| **AWAIT\_MERGE**    | –       | –     | –            | –        | –      | –       | ✔︎     | –            | –              | –            | ✔︎      | –        | –    | –     |
| **CLEANUP**         | ✔︎      | –     | –            | –        | –      | –       | –      | –            | –              | –            | –       | –        | ✔︎   | –     |
| **QUESTION**        | –       | –     | –            | ✔︎       | ✔︎     | –       | ✔︎     | –            | –              | –            | –       | –        | –    | ✔︎    |
| **DONE**            | –       | –     | –            | –        | –      | –       | –      | –            | –              | –            | –       | –        | –    | –     |
| **ERROR**           | ✔︎      | –     | –            | –        | –      | –       | –      | –            | –              | –            | –       | –        | –    | –     |

*(✔︎ = allowed, — = invalid)*

---

## AUTO\_CHECKIN & deterministic budget overflow

1. **Optional question:** While in `CODING` or `FIXING`, the LLM may voluntarily ask for clarification and transition to `QUESTION`.
2. **Deterministic budget review:** Each long-running loop has an iteration budget (`coding_iterations`, `fixing_iterations`). When exhausted, the agent **must** transition to `BUDGET_REVIEW` requesting one of:
   • **CONTINUE** (same plan)
   • **PIVOT** (small plan change)
   • **ESCALATE** (send to `CODE_REVIEW`)
   • **ABANDON** (abort task)

Upon receiving architect approval:

| Approval Result      | Next state                                                                |
| -------------------- | ------------------------------------------------------------------------- |
| **CONTINUE / PIVOT** | Return to the originating state (`CODING` or `FIXING`) and reset counter. |
| **ESCALATE**         | Move to `CODE_REVIEW`.                                                    |
| **ABANDON**          | Move to `ERROR`.                                                          |

---

## Error handling

* The agent enters **ERROR** when:

  1. It receives **ABANDON** from `PLAN_REVIEW`, `CODE_REVIEW`, `BUDGET_REVIEW`, or `QUESTION`.
  2. An **auto-approve** request is rejected with ABANDON.
  3. Any unrecoverable runtime error occurs (panic, out-of-retries, etc.).
* **ERROR** is terminal; orchestrator decides next steps.

---

## Worktree & Merge Workflow Integration

This FSM now includes **Git worktree support** and **merge workflow**:

### New States:
- **SETUP**: Initialize Git worktree and story branch (entry state before PLANNING)
- **BUDGET_REVIEW**: Architect reviews budget exceeded request when iteration budget is exceeded
- **AWAIT_MERGE**: Wait for architect merge result after PR creation
- **CLEANUP**: Clean up workspace and container resources after successful story completion

### Enhanced States:
- **FIXING**: Now handles merge conflicts in addition to test failures and review rejections
- **DONE**: Now terminal state for agent shutdown (no more stories will be assigned)

### Workflow Flow:
```
WAITING → SETUP → PLANNING → CODING → TESTING → CODE_REVIEW → AWAIT_MERGE → CLEANUP → WAITING
                    ↑         ↑         ↑           ↑             ↑           ↓
                    └─────────┴─────────┴───FIXING──┴─────────────┘         DONE (terminal)
                              ↑         ↑
                              └─BUDGET_REVIEW─┘
```

### Merge Conflict Resolution:
1. Code approved → PR created → merge request sent to architect
2. Architect attempts merge using `gh pr merge --squash --delete-branch`
3. If conflicts: `AWAIT_MERGE → FIXING(reason=merge_conflict) → TESTING → CODE_REVIEW`
4. If success: `AWAIT_MERGE → CLEANUP → WAITING` (story completed, ready for next story)

### Multi-Story Workflow:
- **Normal flow**: Stories complete via `CLEANUP → WAITING`, agent ready for next story
- **Terminal flow**: Architect can signal shutdown via `CLEANUP → DONE` when no more stories
- **Error recovery**: Failed stories transition via `ERROR → WAITING` after cleanup

---

*Any deviation from this document is a bug.*

