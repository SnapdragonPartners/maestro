# MAESTRO ORCHESTRATOR REFACTOR PLAN

## Overview

Refactor the orchestrator architecture to eliminate duplication between `bootstrap.go` and `main.go` by extracting shared components into a clean, testable architecture. This addresses the root cause of missing functionality (like the state change processor in bootstrap) and sets up proper multi-spec support.

## Current Problems

1. **Architectural Duplication**: Bootstrap and main orchestrator implement nearly identical infrastructure (dispatcher, persistence, agent creation) separately
2. **Missing Bootstrap Features**: Bootstrap doesn't have state change processor, so coder agents don't restart on DONE/ERROR
3. **Scattered Responsibilities**: Agent lifecycle, infrastructure, and business logic are mixed together
4. **Limited Multi-Spec Support**: No clean way for architect to handle multiple specs without full system restart

## Target Architecture

### Core Components

1. **Kernel**: Shared infrastructure management
   - Dispatcher, database, build service, web UI
   - Concrete types (no over-abstraction per YAGNI)
   - Single source of truth for infrastructure lifecycle

2. **Supervisor**: Agent lifecycle and restart policy management
   - Handles state change notifications
   - Implements restart policies per agent type
   - Manages story requeuing on coder errors

3. **AgentFactory**: Clean pattern for agent creation
   - Wraps existing `createAgentSet` logic
   - No interface needed, just clear naming

4. **Flows**: Business logic separation
   - `BootstrapFlow`: Single spec execution with termination
   - `MainFlow`: Long-running multi-spec handling

## Restart Policy Matrix

| Agent Type | State | Action | Current | New |
|------------|-------|--------|---------|-----|
| Coder | DONE | Restart (ready for next story) | ✅ | ✅ |
| Coder | ERROR | Restart + Requeue story | ✅ | ✅ |
| Architect | DONE | Restart (ready for next spec) | ❌ | ✅ |
| Architect | ERROR | Fatal shutdown | ❌ | ✅ |

### Story Requeue Flow (Current - Preserve This!)

When coder reaches ERROR state:
1. **Orchestrator/Supervisor** detects state change
2. **Orchestrator/Supervisor** calls `dispatcher.SendRequeue(agentID, "agent error")`
3. **Dispatcher** creates requeue message and sends to `questionsCh` 
4. **Architect** receives requeue via REQUEST protocol
5. **Architect** calls `d.queue.Requeue(storyID)` to make story available again
6. **Orchestrator/Supervisor** restarts the coder agent

**CRITICAL**: The Supervisor must preserve this exact flow - orchestrator calls `SendRequeue`, architect handles the requeuing.

## Implementation Plan

### Phase 1: Stash and Clean Start
- [ ] Move current orchestrator to `cmd/old/`
- [ ] Create clean `cmd/maestro/main.go` with minimal CLI parsing
- [ ] Set up basic project structure with new packages

### Phase 2: Build Kernel (Shared Infrastructure)
- [ ] Create `internal/kernel/kernel.go`
- [ ] Move dispatcher creation and lifecycle from both old files
- [ ] Move database initialization and persistence worker
- [ ] Move build service setup
- [ ] Move web UI lifecycle (if enabled)
- [ ] Add `Start()` and `Stop()` methods for clean lifecycle

### Phase 3: Extract Supervisor (Agent Lifecycle)
- [ ] Create `internal/supervisor/supervisor.go` 
- [ ] Move `startStateChangeProcessor` logic from old main
- [ ] Move `handleStateChange` with extended restart policies:
  ```go
  type RestartPolicy struct {
      OnDone  map[string]RestartAction
      OnError map[string]RestartAction
  }
  
  type RestartAction int
  const (
      RestartAgent RestartAction = iota
      FatalShutdown
  )
  ```
- [ ] Preserve exact requeue flow: `dispatcher.SendRequeue(agentID, "agent error")`
- [ ] Move `restartAgent` and `cleanupAgentResources` logic
- [ ] Add agent tracking (`agents map[string]StatusAgent`, `agentTypes map[string]string`)

### Phase 4: Create AgentFactory
- [ ] Create `internal/agents/factory.go`
- [ ] Move `createAgentSet` logic from `agent_helpers.go`
- [ ] Keep as concrete type (no interface needed)
- [ ] Preserve all existing agent creation patterns

### Phase 5: Implement Flows
- [ ] Create `internal/flows/bootstrap.go`
  - Port bootstrap logic: single spec execution
  - Use Kernel for infrastructure
  - Use Supervisor for agent lifecycle
  - Use AgentFactory for agent creation
  - Preserve `injectSpecContent` and `waitForArchitectCompletion` patterns
  
- [ ] Create `internal/flows/main.go`
  - Port main orchestrator logic: multi-spec handling
  - Long-running operation
  - Web UI integration if enabled
  - Same Kernel/Supervisor/AgentFactory usage

### Phase 6: Centralize Spec Injection
- [ ] Create simple spec injection function (no over-abstraction):
  ```go
  func InjectSpec(dispatcher *dispatch.Dispatcher, source string, content []byte) error {
      msg := proto.NewAgentMsg(proto.MsgTypeSPEC, source, map[string]any{"spec_content": string(content)})
      // Send to SpecCh
  }
  ```
- [ ] Use from both flows for consistent spec handling

### Phase 7: CLI Integration
- [ ] Implement clean CLI with two modes:
  - `maestro bootstrap --git-repo=URL [--spec-file=FILE]`
  - `maestro run [--spec-file=FILE] [--webui]`
- [ ] Preserve convenient git-repo flag for testing
- [ ] Simple flag parsing (no complex subcommand framework)

### Phase 8: Integration Testing
- [ ] Test bootstrap flow with docker container builds
- [ ] Test main flow with multiple specs
- [ ] Test state change processor works in both flows
- [ ] Test coder restarts and story requeuing
- [ ] Test architect restarts on DONE (new feature)
- [ ] Test architect ERROR causes fatal shutdown (new feature)

### Phase 9: Cleanup
- [ ] Remove `cmd/old/` directory
- [ ] Remove any unused code from `agent_helpers.go` if no longer needed
- [ ] Update any documentation references

## Key Preservation Requirements

### DO NOT CHANGE:
- ✅ Agent state machines (architect FSM, coder FSM)
- ✅ Authentication flows (GitHub auth, container setup)
- ✅ Config system (global singleton pattern)
- ✅ Dispatcher message protocol
- ✅ Database persistence operations
- ✅ Story requeue flow (orchestrator → dispatcher → architect pattern)
- ✅ Agent creation patterns (`createAgentSet` logic)

### DO CHANGE:
- ❌ Duplicated infrastructure between bootstrap and main
- ❌ Mixed responsibilities (infra + lifecycle + business logic)
- ❌ Hard-coded restart policies
- ❌ Bootstrap missing state change processor

## Success Criteria

1. **No Duplication**: Bootstrap and main share same Kernel, Supervisor, AgentFactory
2. **Bootstrap Coder Restarts**: Coders restart on DONE/ERROR in bootstrap flow
3. **Multi-Spec Architecture**: Architect can be restarted for new specs (DONE state)
4. **Fatal Error Handling**: Architect ERROR state causes system shutdown
5. **Preserved Functionality**: All existing behavior works identically
6. **Clean Code**: Dead code eliminated, clear separation of concerns
7. **Testability**: Each component can be unit tested in isolation

## Migration Safety

- **Incremental**: Each phase preserves existing functionality
- **Reversible**: `cmd/old/` provides fallback during development
- **Testable**: Extensive testing at each phase before proceeding
- **Aggressive Timeline**: Complete refactor targeting prerelease window

## File Structure (Target)

```
cmd/
├── old/                    # Temporary backup during refactor
└── maestro/
    └── main.go            # Clean CLI entry point

internal/
├── kernel/
│   └── kernel.go          # Shared infrastructure
├── supervisor/
│   └── supervisor.go      # Agent lifecycle management  
├── agents/
│   └── factory.go         # Agent creation patterns
└── flows/
    ├── bootstrap.go       # Bootstrap business logic
    └── main.go           # Main orchestrator business logic

pkg/                       # Unchanged - all existing packages preserved
```

This refactor eliminates architectural debt while preserving all working functionality and enabling future multi-spec support.