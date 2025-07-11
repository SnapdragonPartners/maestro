stateDiagram-v2
    %% ---------- ENTRY ----------
    [*] --> WAITING              

    %% ---------- SPEC INTAKE ----------
    WAITING       --> SCOPING            : spec received
    SCOPING       --> DISPATCHING        : initial "ready" stories queued
    SCOPING       --> ERROR              : unrecoverable scoping error

    %% ---------- STORY DISPATCH ----------
    DISPATCHING   --> MONITORING         : ready stories placed on work-queue
    DISPATCHING   --> DONE               : no stories left ⭢ all work complete

    %% ---------- MAIN EVENT LOOP ----------
    MONITORING    --> REQUEST            : any coder request\n(question • plan • iter/tokens • code-review)
    MONITORING    --> MERGING            : auto-trigger when an *approved* code-review arrives

    %% ---------- REQUEST HANDLING ----------
    REQUEST       --> MONITORING         : approve (non-code) • request changes
    REQUEST       --> MERGING            : approve code-review
    REQUEST       --> ESCALATED          : cannot answer → ask human
    REQUEST       --> ERROR              : abandon / unrecoverable

    %% ---------- HUMAN ESCALATION ----------
    ESCALATED     --> REQUEST            : human answer supplied
    ESCALATED     --> ERROR              : timeout / no answer

    %% ---------- MERGE & UNBLOCK ----------
    MERGING       --> DISPATCHING        : merge & push succeeds (may unblock more stories)
    MERGING       --> ERROR              : merge failure / policy block

    %% ---------- TERMINALS ----------
    DONE          --> WAITING            : new spec arrives
    ERROR         --> WAITING            : recovery / restart

    %% ---------- NOTES ----------
    %% Self-loops (state → same state) are always valid and are used for states that wait for external events.

