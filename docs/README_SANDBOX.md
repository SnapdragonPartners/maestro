# Docker Sandboxing for Maestro AI Agents

This document describes the Docker sandboxing implementation for Maestro AI coding system, providing secure, isolated execution environments for AI agents.

## Overview

Docker sandboxing provides security isolation for AI agent code execution through containerized environments. This prevents agents from accessing or modifying files outside their designated workspace and provides resource limits to prevent runaway processes.

## Architecture

### Core Components

1. **Executor Interface** (`pkg/exec/executor.go`)
   - Pluggable abstraction for command execution
   - Supports both local and Docker execution
   - Unified API for all execution contexts

2. **Docker Executor** (`pkg/exec/docker.go`)
   - Docker-based command execution with security hardening
   - Worktree bind mounting for workspace access
   - Resource limits and network isolation

3. **Executor Manager** (`pkg/exec/manager.go`)
   - Auto-detection of Docker availability
   - Configuration-driven executor selection
   - Runtime executor management

4. **MCP Tool Integration** (`pkg/tools/mcp.go`)
   - Shell tool integration with executor interface
   - Transparent sandboxing for AI agent commands
   - Runtime executor switching capabilities

## Configuration

### Basic Configuration

```json
{
  "executor": {
    "type": "docker",
    "fallback": "local",
    "docker": {
      "image": "golang:1.24-alpine",
      "network": "none",
      "read_only": true,
      "tmpfs_size": "100m",
      "cpus": "2",
      "memory": "2g",
      "pids": 1024,
      "auto_pull": true,
      "pull_timeout": 300
    }
  }
}
```

### Configuration Parameters

#### Executor Settings
- `executor.type`: Executor type (`"docker"` | `"local"` | `"auto"`)
- `executor.fallback`: Fallback executor when primary unavailable
- `executor.docker`: Docker-specific configuration

#### Docker Settings
- `image`: Docker image to use for execution
- `network`: Network mode (`"none"` | `"bridge"` | `"host"`)
- `read_only`: Mount filesystem read-only (except workspace)
- `tmpfs_size`: Size for writable tmpfs mounts
- `cpus`: CPU limit (e.g., `"2"`, `"1.5"`)
- `memory`: Memory limit (e.g., `"2g"`, `"512m"`)
- `pids`: Process limit
- `auto_pull`: Auto-pull image if not available
- `pull_timeout`: Image pull timeout in seconds

### Environment Variable Overrides

All configuration can be overridden with environment variables:

```bash
export EXECUTOR_TYPE=docker
export EXECUTOR_DOCKER_IMAGE=golang:1.24-alpine
export EXECUTOR_DOCKER_MEMORY=4g
export EXECUTOR_DOCKER_CPUS=4
```

## Security Model

### Container Hardening

1. **Filesystem Isolation**
   - Read-only root filesystem
   - Workspace mounted with controlled permissions
   - Writable tmpfs for temporary files only

2. **Network Isolation**
   - Network disabled by default (`network: "none"`)
   - Prevents external communication
   - No internet access for AI agents

3. **Resource Limits**
   - CPU and memory constraints
   - Process count limits
   - Prevents resource exhaustion

4. **User Restrictions**
   - Rootless execution
   - No privileged capabilities
   - Limited system access

### Workspace Security

- **Bind Mounting**: Only the designated workspace is accessible
- **Path Validation**: Prevents directory traversal attacks  
- **Permission Controls**: Read-only or read-write as configured
- **Isolation**: Each agent gets isolated execution environment

## Usage Examples

### Basic Usage

```go
// Create Docker executor
executor := exec.NewDockerExec("golang:1.24-alpine")

// Execute command with options
result, err := executor.Run(ctx, []string{"go", "build", "."}, exec.ExecOpts{
    WorkDir: "/path/to/project",
    Timeout: 30 * time.Second,
})

if err != nil {
    log.Fatalf("Command failed: %v", err)
}

fmt.Printf("Exit code: %d\n", result.ExitCode)
fmt.Printf("Output: %s\n", result.Stdout)
```

### MCP Tool Integration

```go
// Initialize shell tool with Docker executor
executor := exec.NewDockerExec("golang:1.24-alpine")
if err := tools.InitializeShellTool(executor); err != nil {
    log.Fatalf("Failed to initialize shell tool: %v", err)
}

// Shell commands now execute in Docker containers
tool, _ := tools.Get("shell")
result, err := tool.Exec(ctx, map[string]any{
    "cmd": "go test ./...",
    "cwd": "/workspace",
})
```

### Runtime Executor Switching

```go
// Switch from local to Docker executor
dockerExec := exec.NewDockerExec("golang:1.24-alpine")
if err := tools.UpdateShellToolExecutor(dockerExec); err != nil {
    log.Fatalf("Failed to update executor: %v", err)
}
```

## Performance Considerations

### Benchmark Results

Typical performance overhead for Docker vs local execution:

- **Simple commands**: 50-100x overhead (container startup cost)
- **Long-running commands**: 5-10% overhead (minimal runtime impact)
- **Build operations**: 10-20% overhead (depends on image caching)

### Optimization Strategies

1. **Image Caching**: Use lightweight base images
2. **Persistent Volumes**: Mount large dependencies
3. **Resource Tuning**: Adjust CPU/memory limits
4. **Container Reuse**: Future enhancement for long-lived containers

## Troubleshooting

### Common Issues

#### Docker Not Available
```
Error: Docker daemon is not available (required for auto mode)
Solution: Install Docker or use 'local' executor explicitly
```

#### Image Pull Failures
```
Error: failed to pull image golang:1.24-alpine
Solution: Check network connectivity and Docker registry access
```

#### Permission Denied
```
Error: permission denied while trying to connect to Docker daemon
Solution: Add user to docker group or use rootless Docker
```

#### Resource Limits Exceeded
```
Error: container killed due to memory limit
Solution: Increase memory limit in configuration
```

### Debug Mode

Enable debug logging for detailed execution information:

```bash
export LOG_LEVEL=debug
```

### Health Checks

```go
// Check executor availability
if !executor.Available() {
    log.Warn("Docker executor not available, falling back to local")
}

// Get executor status
manager := exec.NewExecutorManager(config)
status := manager.GetStatus()
fmt.Printf("Docker available: %v\n", status["docker"])
```

## Testing

### Unit Tests

```bash
# Run all executor tests
go test ./pkg/exec/... -v

# Run specific test suites
go test ./pkg/exec/... -v -run "TestDockerExec"
go test ./pkg/exec/... -v -run "TestDockerExec_Integration"
```

### Integration Tests

```bash
# Run comprehensive tests (requires Docker)
go test ./pkg/exec/... -v -run "TestDockerExec_WorktreeCompatibility"
go test ./pkg/exec/... -v -run "TestDockerExec_MultiAgentStress"
go test ./pkg/exec/... -v -run "TestDockerExec_PerformanceBenchmark"
```

### Docker-in-Docker Testing

```bash
# Run DIND tests for comprehensive validation
go test ./pkg/exec/... -v -run "TestDockerExec_DIND"
```

## Requirements

### System Requirements

- **Docker**: Version 20.10+ or compatible runtime
- **Platform**: Linux, macOS, Windows with Docker Desktop
- **Resources**: Minimum 2GB RAM, 1 CPU core available for containers

### Docker Images

Recommended images for different project types:

- **Go Projects**: `golang:1.24-alpine`
- **Node.js Projects**: `node:20-alpine`
- **Python Projects**: `python:3.11-alpine`
- **Multi-language**: `ubuntu:22.04` with required tools

### Network Requirements

- Docker registry access for image pulling
- No internet access required for container execution (by design)
- Local Docker daemon socket access

## Future Enhancements

### Planned Features

1. **Container Pooling**: Reuse containers for better performance
2. **Custom Images**: Auto-generate project-specific images
3. **Volume Caching**: Persistent dependency caching
4. **Resource Monitoring**: Real-time resource usage tracking
5. **Security Scanning**: Automated vulnerability detection

### Integration Roadmap

- [ ] Kubernetes executor for cloud deployments
- [ ] Podman compatibility for rootless containers
- [ ] BuildKit integration for image building
- [ ] Registry caching for offline operation

## Contributing

### Development Setup

1. Install Docker and ensure daemon is running
2. Run tests: `go test ./pkg/exec/... -v`
3. Run integration tests: `go test ./pkg/exec/... -v -run Integration`

### Adding New Executors

1. Implement the `Executor` interface
2. Add configuration schema
3. Update executor manager
4. Add comprehensive tests
5. Update documentation

For questions or issues, please refer to the project's main documentation or open an issue on GitHub.