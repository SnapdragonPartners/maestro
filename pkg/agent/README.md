# Agent Package

This package provides the foundational abstractions for state machine-based agents in the Maestro orchestration system.

## Concurrency Model

The agent state machine architecture follows a strict single-goroutine execution model:

### Core Principles

- **FSM instance drives all state mutations on one goroutine** - Each agent's state machine runs entirely within a single goroutine, ensuring sequential processing of state transitions and mutations.

- **External goroutines interact only via message channels** - All communication with the agent from external components (orchestrator, other agents, UI) must go through message channels. Direct method calls that could mutate state are not permitted from external goroutines.

- **Mutexes inside FSM are unnecessary by design** - Since all state mutations occur on a single goroutine, there are no race conditions between state reads/writes within the FSM logic. Any mutexes found within state machine handlers indicate a design violation.

### Implementation Details

- Agent message processing happens sequentially on the agent's dedicated goroutine
- State transitions are atomic and cannot be interrupted by other state changes
- External events (timeouts, incoming messages, shutdown signals) are queued via channels
- The agent's main processing loop handles one event at a time in order

### Benefits

- **Eliminates race conditions** - No concurrent access to agent state means no race conditions
- **Simplifies debugging** - Linear execution makes it easy to trace state changes
- **Predictable behavior** - Deterministic processing order reduces non-deterministic bugs
- **Clean separation of concerns** - Clear boundary between agent-internal logic and external communication

This model ensures reliable, predictable agent behavior while maintaining clean architectural boundaries in the distributed orchestration system.