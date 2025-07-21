# Metrics Pooling Requirements

## Overview

With the implementation of the agent restart workflow (DONE → shutdown → recreate), we have identified a future requirement for metrics pooling at the orchestrator level to provide continuous monitoring and historical tracking across agent lifecycle events.

## Current State

Currently, each agent maintains its own metrics and state, which are lost when the agent is restarted. While the web UI properly handles the disappearance and reappearance of agents during restarts, we lack:

1. **Historical metrics continuity** - Metrics reset when agents restart
2. **Aggregated system health** - No global view of system performance over time  
3. **Agent lifecycle analytics** - No tracking of restart frequency, duration, etc.

## Future Requirements

### 1. Orchestrator-Level Metrics Pool

The orchestrator should maintain a centralized metrics pool that:

- **Aggregates metrics** from all agents before they shut down
- **Persists historical data** across agent restarts
- **Provides system-wide views** of performance and health
- **Tracks agent lifecycle events** (restarts, failures, recovery times)

### 2. Metrics Categories to Pool

#### Agent Performance Metrics
- Stories completed per agent (cumulative)
- Average story completion time
- Code quality metrics (tests passed, lint score)
- LLM token usage and costs

#### System Health Metrics  
- Agent restart frequency and reasons
- Queue utilization over time (storyCh, questionsCh)
- Resource usage trends (memory, CPU, disk)
- Error rates and recovery patterns

#### Workflow Metrics
- End-to-end story processing time
- Architect → Coder handoff efficiency
- Review cycles and approval rates
- Build/test success rates

### 3. Implementation Approach

#### Phase 1: Metrics Collection Infrastructure
```go
type OrchestatorMetrics struct {
    AgentMetrics    map[string]*AgentMetricsHistory
    SystemMetrics   *SystemMetricsHistory  
    WorkflowMetrics *WorkflowMetricsHistory
    // Thread-safe access with sync.RWMutex
}
```

#### Phase 2: Agent Integration
- Agents report metrics before shutdown via state change notifications
- Orchestrator collects and aggregates metrics during agent restart
- Web UI enhanced to show historical views and trends

#### Phase 3: Persistence & Analytics
- Metrics persisted to disk for restart recovery
- Time-series analysis capabilities
- Alerting on anomalous patterns

### 4. Integration Points

#### Current Agent Restart Flow
```
Agent DONE → Orchestrator Detects → Collect Metrics → Shutdown → Recreate
```

#### Enhanced Flow with Metrics Pooling
```
Agent DONE → Orchestrator Detects → Collect Metrics → Pool/Aggregate → Shutdown → Recreate → Resume Metrics
```

#### Web UI Integration
- **Current**: Shows live agent state (present/absent during restart)
- **Enhanced**: Shows historical trends, cumulative metrics, restart patterns

### 5. Technical Considerations

#### Storage
- Local file-based storage for MVP (JSON/SQLite)
- Future: Time-series database (InfluxDB, Prometheus)

#### Performance
- Asynchronous metrics collection to avoid blocking agent restart
- Configurable retention policies (daily, weekly, monthly rollups)

#### Scalability
- Metrics aggregation should handle multiple concurrent agent restarts
- Bounded memory usage with configurable history limits

## Implementation Priority

This requirement is marked as **low priority** because:

1. **Core functionality works** - Agent restart workflow is fully functional
2. **Web UI is stable** - Current monitoring provides adequate operational visibility
3. **No immediate blockers** - System operates correctly without metrics pooling

However, this becomes **high priority** for:
- Production deployments requiring operational metrics
- Performance optimization and capacity planning
- Debugging complex multi-agent interaction issues

## Related Work

- Agent restart workflow: ✅ Completed in this refactoring
- Web UI monitoring: ✅ Tested and verified stable
- State change notifications: ✅ Infrastructure ready for metrics collection
- Orchestrator lifecycle management: ✅ Ready to integrate metrics pooling

## Next Steps

When implementing metrics pooling:

1. **Review existing metrics** in agent implementations
2. **Design metrics schema** for historical storage  
3. **Implement collection hooks** in agent restart workflow
4. **Enhance web UI** with historical views
5. **Add persistence layer** for metrics durability