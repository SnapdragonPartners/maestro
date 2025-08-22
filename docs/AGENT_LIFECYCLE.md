# Agent Lifecycle Architecture

This document describes the agent lifecycle management system in Maestro.

## Core Principles

1. **Config Singleton Pattern**: All agents get configuration from `config.GetConfig()` - no dependency injection of config data
2. **Stateless Restarts**: Agent restarts create completely fresh instances with no state preservation
3. **Context-Based Termination**: Agents respond to context cancellation for graceful shutdown
4. **Self-Managed Workspaces**: Agents manage their own work directory lifecycle

## Components

### AgentFactory
Lightweight factory responsible for creating agents with minimal dependencies:

```go
type AgentFactory struct {
    dispatcher         *dispatch.Dispatcher
    persistenceChannel chan<- *persistence.Request
}

func (f *AgentFactory) NewAgent(agentID, agentType string) (dispatch.Agent, error)
```

**Responsibilities:**
- Create agents with only essential runtime dependencies
- BuildService created as needed (lightweight)
- All configuration sourced from `config.GetConfig()`

### Supervisor
Manages agent lifecycle and restart policies:

```go
type Supervisor struct {
    // Agent tracking
    Agents       map[string]dispatch.Agent
    AgentTypes   map[string]string
    AgentContexts map[string]context.CancelFunc  // NEW: Context management
    
    // Dependencies
    Factory *AgentFactory
    Policy  RestartPolicy
}
```

**Context Management:**
- Each agent runs in its own cancellable context
- Agent contexts are children of main context (automatic global shutdown)
- Individual agent termination via context cancellation

## Agent Creation Flow

```
1. Factory.NewAgent(agentID, agentType) called
2. Agent constructor gets all config from config.GetConfig()
3. Agent determines workDir from config
4. Agent SETUP state cleans and creates fresh workDir
5. Supervisor creates agent context: context.WithCancel(mainCtx)
6. Supervisor starts agent: go agent.Run(agentCtx)
7. Supervisor tracks agent context for termination
```

## Agent Termination Flow

### Individual Agent Restart (DONE/ERROR states)
```
1. Agent reaches terminal state (DONE/ERROR)
2. Supervisor receives state change notification
3. Supervisor cancels agent context: cancelFunc()
4. Agent Run() loop exits on ctx.Done()
5. Supervisor removes agent from tracking maps
6. For coder ERROR: Supervisor calls RequeueStory()
7. Supervisor creates new agent with same ID
```

### Global Shutdown (Ctrl+C)
```
1. Main context cancelled (Ctrl+C)
2. All agent contexts auto-cancel (child context inheritance)
3. All agent Run() loops exit on ctx.Done()
4. Supervisor loop exits
5. Clean system shutdown
```

## Restart Policies

### Coder Agents
- **DONE state**: Restart for next story
- **ERROR state**: Requeue story + restart agent

### Architect Agents  
- **DONE state**: Restart for next specification
- **ERROR state**: Fatal system shutdown (`os.Exit(1)`)

## Work Directory Management

### Location
- Work directory path: `config.GetConfig().Orchestrator.WorkDir` (or similar)
- Individual agent directories: `filepath.Join(workDir, agentID)`

### Lifecycle
1. **Agent SETUP state**: 
   - `os.RemoveAll(agentWorkDir)` - Clean slate
   - `os.MkdirAll(agentWorkDir, 0755)` - Fresh directory
2. **Agent termination**: No cleanup needed (next restart will clean)

## Error Handling

### Coder Failures
- Story is requeued for retry with different agent
- Failed agent is terminated and restarted
- System continues operation

### Architect Failures
- System cannot recover from architect errors
- Immediate fatal shutdown to prevent inconsistent state
- Manual intervention required

## Context Hierarchy

```
Main Context (from main/flows)
├── Supervisor Context (same as main)
├── Agent Context 1 (individual cancellation)
├── Agent Context 2 (individual cancellation)  
└── Agent Context N (individual cancellation)
```

**Benefits:**
- Individual agent control for restarts
- Automatic global shutdown propagation
- No explicit cleanup needed for Ctrl+C scenarios

## Configuration Access Pattern

All agents follow this pattern:
```go
func NewAgent(agentID string, dispatcher *Dispatcher, persistenceChannel chan<- *Request) (*Agent, error) {
    // Get fresh config
    cfg, err := config.GetConfig()
    if err != nil {
        return nil, fmt.Errorf("failed to get config: %w", err)
    }
    
    // Use config values
    workDir := filepath.Join(cfg.Orchestrator.WorkDir, agentID)
    modelConfig := getModelByName(cfg, cfg.Agents.CoderModel)
    
    // Create agent with config-derived values
    // ...
}
```

This ensures:
- Always current configuration values
- No stale cached configuration
- Consistent with global config singleton pattern