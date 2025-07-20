Here's the complete current **STATES.md** content, inline for easy copy-paste:

---

# Coder Agent Finite-State Machine (Canonical)

*Last updated: 2025-07-18 (rev F - Agent Restart Workflow)*

This document is the **single source of truth** for the coder agent's workflow.
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
    PLANNING      --> DONE             : mark complete (approved)
    PLANNING      --> QUESTION         : clarification
    PLANNING      --> BUDGET_REVIEW    : budget exceeded

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
    
    AWAIT_MERGE   --> DONE             : merge successful
    AWAIT_MERGE   --> FIXING           : merge conflicts 

    %% Budget review (budget exceeded)
    BUDGET_REVIEW --> PLANNING         : continue/pivot
    BUDGET_REVIEW --> CODING           : continue/pivot
    BUDGET_REVIEW --> FIXING           : continue/pivot
    BUDGET_REVIEW --> CODE_REVIEW      : escalate
    BUDGET_REVIEW --> ERROR            : abandon/error

    %% Clarification loop
    QUESTION      --> PLANNING         : answer design Q
    QUESTION      --> CODING           : return to coding
    QUESTION      --> FIXING           : return to fixing
    QUESTION      --> ERROR            : abandon/error 

    %% Agent restart workflow - orchestrator handles cleanup
    ERROR         --> DONE             : orchestrator cleanup & restart
    DONE          --> [*]              : orchestrator shuts down agent
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
| **QUESTION**        | Awaiting external clarification or information.                                |
| **DONE**            | Agent termination state - orchestrator will shut down and restart agent.       |
| **ERROR**           | Task abandoned or unrecoverable failure encountered.                           |

---

## Allowed transitions (tabular)

| From \ To           | WAITING | SETUP | PLAN\_REVIEW | PLANNING | CODING | TESTING | FIXING | CODE\_REVIEW | BUDGET\_REVIEW | AWAIT\_MERGE | QUESTION | DONE | ERROR |
| ------------------- | ------- | ----- | ------------ | -------- | ------ | ------- | ------ | ------------ | -------------- | ------------ | -------- | ---- | ----- |
| **WAITING**         | –       | ✔︎    | –            | –        | –      | –       | –      | –            | –              | –            | –        | –    | –     |
| **SETUP**           | –       | –     | –            | ✔︎       | –      | –       | –      | –            | –              | –            | –        | –    | ✔︎    |
| **PLANNING**        | –       | –     | ✔︎           | –        | –      | –       | –      | –            | ✔︎             | –            | ✔︎       | ✔︎   | –     |
| **PLAN\_REVIEW**    | –       | –     | –            | ✔︎       | ✔︎     | –       | –      | –            | –              | –            | –        | –    | ✔︎    |
| **CODING**          | –       | –     | –            | –        | –      | ✔︎      | –      | –            | ✔︎             | –            | ✔︎       | –    | ✔︎    |
| **TESTING**         | –       | –     | –            | –        | –      | –       | ✔︎     | ✔︎           | –              | –            | –        | –    | –     |
| **FIXING**          | –       | –     | –            | –        | –      | ✔︎      | –      | –            | ✔︎             | –            | ✔︎       | –    | ✔︎    |
| **CODE\_REVIEW**    | –       | –     | –            | –        | –      | –       | ✔︎     | –            | –              | ✔︎           | –        | –    | ✔︎    |
| **BUDGET\_REVIEW**  | –       | –     | –            | ✔︎       | ✔︎     | –       | ✔︎     | ✔︎           | –              | –            | –        | –    | ✔︎    |
| **AWAIT\_MERGE**    | –       | –     | –            | –        | –      | –       | ✔︎     | –            | –              | –            | –        | ✔︎   | –     |
| **QUESTION**        | –       | –     | –            | ✔︎       | ✔︎     | –       | ✔︎     | –            | –              | –            | –        | –    | ✔︎    |
| **DONE**            | –       | –     | –            | –        | –      | –       | –      | –            | –              | –            | –        | –    | –     |
| **ERROR**           | –       | –     | –            | –        | –      | –       | –      | –            | –              | –            | –        | ✔︎   | –     |

*(✔︎ = allowed, — = invalid)*

---

## AUTO\_CHECKIN & deterministic budget overflow

1. **Optional question:** While in `PLANNING`, `CODING` or `FIXING`, the LLM may voluntarily ask for clarification and transition to `QUESTION`.
2. **Deterministic budget review:** Each long-running loop has an iteration budget (`planning_iterations`, `coding_iterations`, `fixing_iterations`). When exhausted, the agent **must** transition to `BUDGET_REVIEW` requesting one of:
   • **CONTINUE** (same plan)
   • **PIVOT** (small plan change)
   • **ESCALATE** (send to `CODE_REVIEW`)
   • **ABANDON** (abort task)

Upon receiving architect approval:

| Approval Result      | Status Code           | Next state                                                                           |
| -------------------- | -------------------- | ------------------------------------------------------------------------------------ |
| **CONTINUE / PIVOT** | `ApprovalStatusApproved` | Return to the originating state (`PLANNING`, `CODING` or `FIXING`) and reset counter. |
| **ESCALATE**         | `ApprovalStatusNeedsChanges` | Move to `CODE_REVIEW`.                                                               |
| **ABANDON**          | `ApprovalStatusRejected` | Move to `ERROR`.                                                                     |

Note: The architect uses standard approval status codes that map to budget review actions as shown above.

---

## Error handling

* The agent enters **ERROR** when:

  1. It receives **ABANDON** from `PLAN_REVIEW`, `CODE_REVIEW`, `BUDGET_REVIEW`, or `QUESTION`.
  2. An **auto-approve** request is rejected with ABANDON.
  3. Any unrecoverable runtime error occurs (panic, out-of-retries, etc.).
* **ERROR** transitions to **DONE** for orchestrator cleanup and agent restart.

---

## Worktree & Merge Workflow Integration

This FSM includes **Git worktree support** and **merge workflow**:

### Key States:
- **SETUP**: Initialize Git worktree and story branch (entry state before PLANNING)
- **BUDGET_REVIEW**: Architect reviews budget exceeded request when iteration budget is exceeded

### Special Transitions:
- **PLANNING → DONE**: Direct completion when story requirements are already implemented (via `mark_story_complete` tool)
- **AWAIT_MERGE**: Wait for architect merge result after PR creation
- **DONE**: Terminal state - orchestrator will shut down and restart agent with clean state

### Enhanced States:
- **FIXING**: Handles merge conflicts in addition to test failures and review rejections
- **ERROR**: Transitions to DONE for orchestrator cleanup and restart

### Workflow Flow:
```
WAITING → SETUP → PLANNING → CODING → TESTING → CODE_REVIEW → AWAIT_MERGE → DONE
                    ↑         ↑         ↑           ↑             ↑           ↓
                    └─────────┴─────────┴───FIXING──┴─────────────┘    [agent restart]
                              ↑         ↑                              ↓
                              └─BUDGET_REVIEW─┘                    WAITING (new agent)
```

### Merge Conflict Resolution:
1. Code approved → PR created → merge request sent to architect
2. Architect attempts merge using `gh pr merge --squash --delete-branch`
3. If conflicts: `AWAIT_MERGE → FIXING(reason=merge_conflict) → TESTING → CODE_REVIEW`
4. If success: `AWAIT_MERGE → DONE` (orchestrator shuts down agent and creates new one)

### Agent Restart Workflow:
- **Story completion**: `AWAIT_MERGE → DONE` (merge successful)
- **Error recovery**: `ERROR → DONE` (unrecoverable failure)
- **Orchestrator actions**: On DONE state, orchestrator shuts down agent and creates fresh instance
- **Complete cleanup**: All resources deleted (workspace, containers, state) for clean slate
- **Future metrics**: Orchestrator will aggregate metrics across agent restarts (not yet implemented)

---

*Any deviation from this document is a bug.*

