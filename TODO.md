# TODO - Production Readiness Items

## Security & Operations

### SSH Key Management (Priority: High)
- [ ] **Deploy key setup**: Document least-privilege deploy key creation per repository
- [ ] **Machine user alternative**: Document GitHub machine user setup with repo-specific access
- [ ] **Key rotation**: Establish process for periodic SSH key rotation
- [ ] **Audit compliance**: Ensure deploy key access is logged and auditable

### Mirror Garbage Collection (Priority: Medium)
- [ ] **Git GC automation**: Implement periodic cleanup of mirror repositories
  ```bash
  # Weekly cleanup via cron/systemd timer
  git -C {mirror_path} gc --prune=now
  git -C {mirror_path} remote prune origin
  ```
- [ ] **Disk monitoring**: Add metrics for mirror repository size growth
- [ ] **Cleanup threshold**: Define triggers for aggressive cleanup (e.g., >1GB mirrors)

## Scalability & Performance

### Concurrency Support (Priority: Low)
- [ ] **Test sharding**: Extend worktree pattern for parallel test execution
  ```
  Current: {AGENT_ID}/{STORY_ID}
  Future:  {AGENT_ID}/{STORY_ID}/run-{N}
  ```
- [ ] **Database name-spacing**: Plan for parallel test database isolation
- [ ] **Resource limits**: Define CPU/memory limits per concurrent agent

### Performance Optimization (Priority: Medium)
- [ ] **Mirror warming**: Pre-clone mirrors during system startup
- [ ] **Worktree pooling**: Investigate reusing worktrees for similar stories
- [ ] **Network optimization**: Consider local Git cache/proxy for large repositories

## Documentation & UX

### Operator Handbook (Priority: High)
- [ ] **Story file naming**: Document strict `{ID}.md` filename requirement
- [ ] **Troubleshooting guide**: Common Git worktree issues and solutions
- [ ] **Monitoring setup**: Recommended metrics and alerting for worktree operations
- [ ] **Backup procedures**: Mirror repository backup and recovery procedures

### Developer Experience (Priority: Medium)
- [ ] **Local development**: Docker Compose setup for testing worktree flows
- [ ] **Debugging tools**: CLI commands for inspecting agent workspaces
- [ ] **Integration tests**: End-to-end tests with real GitHub repositories

## Infrastructure

### Container Support (Priority: Low)
- [ ] **Docker isolation**: Container-per-agent with mounted worktrees
- [ ] **Kubernetes deployment**: Helm charts with persistent volume claims
- [ ] **Resource quotas**: CPU/memory/disk limits per agent container

### Observability (Priority: Medium)
- [ ] **Metrics collection**: Workspace setup/cleanup timing and success rates
- [ ] **Distributed tracing**: Trace story flow from assignment to completion
- [ ] **Log aggregation**: Centralized logging for multi-agent debugging
- [ ] **Health checks**: Readiness/liveness probes for agent workspace state