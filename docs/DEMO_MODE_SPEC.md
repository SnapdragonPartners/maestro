# Demo Mode Specification

## Overview

Demo mode enables users to run and interact with applications built by Maestro for User Acceptance Testing (UAT) and hotfixes. This feature spins up a running instance of the developed application along with any required services (databases, caches, etc.).

## Problem Statement

Maestro can develop applications but currently has no way for users to run and interact with what's been built. Users need:
- A way to evaluate completed features before approving them
- A mechanism for identifying issues that require hotfixes
- An interactive environment for UAT

## Architecture

### Container Topology

Demo mode uses a per-stack isolation model where each logical unit (coder, demo) gets its own Docker network and service instances via Docker Compose.

```
┌─────────────────────────────────────────────────────────────────┐
│                         Host Docker                             │
│                                                                 │
│  ┌────────────────────────────────────────────────────────┐    │
│  │                   maestro network                       │    │
│  │  ┌───────────┐                                         │    │
│  │  │ Architect │  (safe container, read-only mounts)     │    │
│  │  └───────────┘                                         │    │
│  └────────────────────────────────────────────────────────┘    │
│                                                                 │
│  ┌──────────────────────┐  ┌──────────────────────┐            │
│  │  coder-001-network   │  │  coder-002-network   │            │
│  │  ┌────────┐          │  │  ┌────────┐          │            │
│  │  │Services│          │  │  │Services│          │            │
│  │  └────────┘          │  │  └────────┘          │            │
│  │  ┌────────┐          │  │  ┌────────┐          │            │
│  │  │Coder-1 │          │  │  │Coder-2 │          │            │
│  │  └────────┘          │  │  └────────┘          │            │
│  └──────────────────────┘  └──────────────────────┘            │
│                                                                 │
│  ┌──────────────────────────────────────────────┐              │
│  │              demo-network                     │              │
│  │  ┌────────┐  ┌────────┐  ┌────────┐          │              │
│  │  │Services│  │  Demo  │  │   PM   │          │              │
│  │  └────────┘  └────────┘  └────────┘          │              │
│  └──────────────────────────────────────────────┘              │
│                                                                 │
│  Orchestrator (host) ─── WebUI :8080                           │
│                      ─── Demo  :8081                            │
└─────────────────────────────────────────────────────────────────┘
```

### Key Architectural Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Container orchestration | Docker Compose | Deep existing Docker integration; simpler than Kubernetes |
| Isolation model | Per-stack (Option B) | Clean isolation; no shared state conflicts |
| Compose file location | `.maestro/compose.yml` | Follows established pattern for Maestro-managed files |
| Compose file management | Maestro-managed | Low-knowledge users; edited via devops stories |
| Network creation | Lazy (on demand) for coders; always for demo | Resource efficiency |
| Demo lifetime | Session-scoped | Clean shutdown on Maestro exit |
| Coder service refresh | Always run `compose up` on TESTING entry | Compose handles diffing; simpler than hash tracking |
| Demo change detection | Git diff for user notification | User-controlled Restart/Rebuild via WebUI |
| Compose stack registry | `internal/state/` (alongside container registry) | Infrastructure concern, not demo-specific |
| Log streaming | HTTP polling (consistent with existing WebUI) | Simpler than WebSocket; matches current patterns |
| PM network joining | Dynamic (when demo starts) | Handles PM already running when demo starts |

### Why Not Docker-in-Docker?

Docker-in-Docker (DinD) was considered but rejected:
- Requires privileged containers (security concern)
- Adds ~500MB to bootstrap image
- Double NAT for port exposure (complex)
- Nested networking makes PM→Demo communication difficult
- Current design explicitly avoids DinD

## Components

### 1. Demo Service (`pkg/demo/`)

Core service managing demo lifecycle, networks, and container orchestration.

```go
type Service struct {
    config       *config.Config
    logger       *logx.Logger
    network      string              // "demo-network"
    composeFile  string              // ".maestro/compose.yml" or empty
    running      bool
    port         int                 // Allocated host port (8081+)
    builtFromSHA string              // Git SHA demo was built from
    logStream    *LogStream          // For WebSocket streaming
}

type Status struct {
    Running      bool   `json:"running"`
    Port         int    `json:"port,omitempty"`
    URL          string `json:"url,omitempty"`
    BuiltFromSHA string `json:"built_from_sha,omitempty"`
    CurrentSHA   string `json:"current_sha,omitempty"`
    Outdated     bool   `json:"outdated"`
    Services     []ServiceStatus `json:"services,omitempty"`
}

type ServiceStatus struct {
    Name    string `json:"name"`
    Status  string `json:"status"`  // "running", "starting", "stopped"
    Healthy bool   `json:"healthy"`
}
```

**Methods**:
- `Start(ctx context.Context) error` - Start demo (with or without compose)
- `Stop(ctx context.Context) error` - Stop demo and services
- `Restart(ctx context.Context) error` - Restart demo container only
- `Rebuild(ctx context.Context) error` - Full rebuild (image + services)
- `Status() *Status` - Current demo status
- `StreamLogs(ctx context.Context) (<-chan string, error)` - Log streaming

### 2. Compose Stack Registry (`internal/state/compose.go`)

Global registry for tracking active Docker Compose stacks. Lives alongside the container registry since it's infrastructure, not demo-specific.

```go
type ComposeStack struct {
    ProjectName string    // "coder-001", "demo"
    ComposeFile string    // Path to compose file
    Network     string    // Network name
    StartedAt   time.Time
}

type ComposeRegistry struct {
    mu     sync.RWMutex
    stacks map[string]*ComposeStack  // keyed by ProjectName
}

func (r *ComposeRegistry) Register(stack *ComposeStack)
func (r *ComposeRegistry) Unregister(projectName string)
func (r *ComposeRegistry) Get(projectName string) *ComposeStack
func (r *ComposeRegistry) All() []*ComposeStack
func (r *ComposeRegistry) Cleanup(ctx context.Context) error  // Called on shutdown
```

### 3. Service Stack Manager (`pkg/demo/stack.go`)

Manages Docker Compose stack operations (wraps compose CLI).

```go
type Stack struct {
    ProjectName string  // "coder-001", "demo"
    ComposeFile string  // Path to compose file
    Network     string  // Network name
}

func (s *Stack) Up(ctx context.Context) error
func (s *Stack) Down(ctx context.Context) error
func (s *Stack) Restart(ctx context.Context, service string) error
func (s *Stack) Logs(ctx context.Context, service string) (io.Reader, error)
```

### 4. Network Manager (`pkg/demo/network.go`)

Handles Docker network lifecycle.

```go
func EnsureNetwork(ctx context.Context, name string) error
func RemoveNetwork(ctx context.Context, name string) error
func ConnectContainer(ctx context.Context, network, container string) error
func DisconnectContainer(ctx context.Context, network, container string) error
func NetworkExists(ctx context.Context, name string) bool
```

### 5. Change Detector (`pkg/demo/changes.go`)

Detects what changed between demo build and current HEAD.

```go
type ChangeType int

const (
    NoChange ChangeType = iota
    CodeOnly            // Restart sufficient
    DockerfileChanged   // Rebuild required
    ComposeChanged      // Rebuild + services restart required
)

func DetectChanges(fromSHA, toSHA string) (ChangeType, error)
func GetChangeRecommendation(changeType ChangeType) string
```

### 6. WebUI Integration (`pkg/webui/`)

New endpoints and UI components for demo control.

**API Endpoints**:
```
GET  /api/demo/status     - Get demo status (includes detected_ports, container_port, diagnostics)
POST /api/demo/start      - Start demo (runs port discovery on first start)
POST /api/demo/stop       - Stop demo
POST /api/demo/restart    - Restart demo container
POST /api/demo/rebuild    - Full rebuild (accepts {"skip_detection": true} to use cached port)
GET  /api/demo/logs       - Get demo logs (polling, consistent with existing pattern)
```

**UI Components**:
- Demo control panel (Start/Stop/Restart/Rebuild buttons)
- Status indicator (Running/Stopped/Starting)
- URL display with clickable link
- "Outdated" badge when code has changed
- Log viewer (polling-based, like existing log viewer)
- Resource warning display

### 7. Coder Integration

Modifications to coder workflow for service stack management.

**State Transitions**:
```
CODING → TESTING:
  1. EnsureNetwork("coder-{id}-network")
  2. docker compose -p coder-{id} up -d (always, if compose file exists)
     - Compose handles diffing internally
     - If nothing changed: no-op (~50ms)
     - If compose file changed: rebuilds affected services
  3. ConnectContainer(network, coder-container)
  4. Proceed with tests

TESTING → CODING (test failure):
  (No action - services stay running, will be refreshed on next TESTING entry)

Story Complete:
  1. Stack.Down()
  2. DisconnectContainer()
  3. RemoveNetwork()
```

**Design Note**: We always run `docker compose up -d` when entering TESTING rather than tracking compose file hashes. Compose's built-in diffing handles the "did anything change?" question efficiently. This is simpler and eliminates edge cases around hash tracking.

### 8. Docker Compose Tools (`pkg/tools/compose_*.go`)

MCP tools for agents to manage compose files (mirrors existing container tools).

| Tool | Description |
|------|-------------|
| `compose_read` | Read current compose file contents |
| `compose_write` | Write/update compose file |
| `compose_add_service` | Add a service to compose file |
| `compose_remove_service` | Remove a service from compose file |
| `compose_validate` | Validate compose file syntax |

### 9. Secrets UI (`pkg/webui/`)

WebUI interface for managing encrypted secrets.

**API Endpoints**:
```
GET  /api/secrets         - List secret names (not values)
POST /api/secrets         - Set a secret
DELETE /api/secrets/:name - Remove a secret
```

**UI Components**:
- Settings → Secrets section
- Add secret form (name + value)
- List of configured secrets (names only, values masked)
- Delete button per secret

## Configuration

### Demo Config Section

```json
{
  "demo": {
    "container_port_override": 0,
    "selected_container_port": 8080,
    "detected_ports": [
      {"port": 8080, "bind_address": "0.0.0.0", "protocol": "tcp", "reachable": true},
      {"port": 5432, "bind_address": "127.0.0.1", "protocol": "tcp", "reachable": false}
    ],
    "last_assigned_host_port": 32847,
    "run_cmd_override": "",
    "healthcheck_path": "/health",
    "healthcheck_timeout_seconds": 60
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `container_port_override` | int | 0 | Manual override for container port (skips detection) |
| `selected_container_port` | int | 0 | Auto-detected or user-selected container port |
| `detected_ports` | []PortInfo | [] | All detected listening ports from discovery |
| `last_assigned_host_port` | int | 0 | Last Docker-assigned host port (informational) |
| `run_cmd_override` | string | "" | Override `config.Build.RunCmd` for demo |
| `healthcheck_path` | string | "/health" | HTTP path to check for readiness |
| `healthcheck_timeout_seconds` | int | 60 | Max wait time for app to become healthy |

**PortInfo structure:**
| Field | Type | Description |
|-------|------|-------------|
| `port` | int | Container port number |
| `bind_address` | string | IP address bound to ("0.0.0.0", "127.0.0.1", etc.) |
| `protocol` | string | Protocol ("tcp", "udp") |
| `exposed` | bool | Was in Dockerfile EXPOSE |
| `reachable` | bool | Can be published (not loopback-bound) |

### Directory Structure

Understanding where files live:

```
projectDir/                      # e.g., ~/Code/maestro-work/hello
├── .maestro/                   # LOCAL config - NOT committed to repo
│   ├── config.json            # Project configuration
│   └── database/              # SQLite, runtime state
│
├── coder-001/                  # Coder workspace = clone of repo
│   ├── .maestro/              # COMMITTED to repo
│   │   ├── Dockerfile         # Dev container definition
│   │   ├── compose.yml        # Service definitions (NEW)
│   │   └── knowledge/         # Knowledge graph
│   └── src/                   # Application code
│
└── coder-002/                  # Another clone of repo
    └── .maestro/              # Same files (from repo)
        └── compose.yml        # Same compose.yml
```

**Key points**:
- `compose.yml` lives in `<repo>/.maestro/compose.yml` (version controlled)
- Each coder workspace is a git clone, so all have the same `compose.yml`
- Edits to `compose.yml` are code changes that go through PR review
- Orchestrator reads from coder's workspace: `coder-001/.maestro/compose.yml`

### Compose File Structure

Location: `<workspace>/.maestro/compose.yml` (committed to repo)

```yaml
services:
  demo:
    build: ..
    ports:
      - "${DEMO_PORT:-8081}:8080"
    environment:
      - DATABASE_URL=postgres://postgres:5432/app
      - REDIS_URL=redis://redis:6379
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_started
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 5s
      timeout: 3s
      retries: 5

  postgres:
    image: postgres:15
    environment:
      POSTGRES_PASSWORD: ${DB_PASSWORD:-postgres}
      POSTGRES_DB: app
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "postgres"]
      interval: 2s
      timeout: 2s
      retries: 10

  redis:
    image: redis:7
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 2s
      timeout: 2s
      retries: 5
```

## User Flows

### Starting a Demo

1. User clicks "Demo" button in WebUI
2. System checks if compose file exists
3. If exists: `docker compose -p demo up -d`
4. If not: `docker run --network demo-network ...`
5. System waits for healthcheck to pass
6. WebUI displays URL: `http://localhost:8081`
7. PM container joins demo-network
8. User accesses demo in browser

### Demo After Code Merge

1. Coder merges PR
2. System detects `HEAD` differs from `builtFromSHA`
3. System runs `DetectChanges()` to categorize
4. WebUI shows appropriate indicator:
   - Code only: "Demo outdated. [Restart] recommended"
   - Dockerfile changed: "Dockerfile changed. [Rebuild] required"
   - Compose changed: "Services changed. [Rebuild] required"
5. User clicks recommended action
6. Demo updates

### Coder Running Integration Tests

1. Coder enters TESTING state
2. System checks for `.maestro/compose.yml`
3. If exists:
   - Create `coder-{id}-network` (if not exists)
   - Run `docker compose -p coder-{id} up -d` (always - compose handles diffing)
   - Connect coder container to network (if not connected)
4. Coder runs tests with service access
5. If tests fail, return to CODING:
   - Services stay running
   - On next TESTING entry, compose up runs again and rebuilds if file changed
6. On story complete:
   - Run `docker compose -p coder-{id} down -v`
   - Remove network

### Adding Services via PM Interview

1. PM asks: "What services does your application need?"
2. User responds: "PostgreSQL database and Redis for caching"
3. PM generates requirements for compose file creation
4. Architect creates devops story: "Add PostgreSQL and Redis services"
5. Coder implements by creating/updating `.maestro/compose.yml`
6. Services available for testing and demo

## Resource Management

### Estimation

```go
func EstimateResources(numCoders int, composeFile string) ResourceEstimate {
    servicesPerCoder := countServices(composeFile)

    return ResourceEstimate{
        Containers: numCoders * (1 + servicesPerCoder) + demoContainers,
        MemoryMB:   estimateMemory(numCoders, servicesPerCoder),
    }
}
```

### Warning Thresholds

| Condition | Warning |
|-----------|---------|
| > 10 containers | "High container count may impact performance" |
| > 4GB estimated RAM | "Consider reducing number of coders" |
| > 3 coders with > 3 services each | Specific recommendation shown |

### Warning Display

WebUI shows warning at startup and when coder count changes:

```
⚠️ Resource Warning
3 coders × 4 services = 15 containers (~3GB RAM)
Consider reducing to 2 coders for better performance.
```

## Port Detection and Management

### Dynamic Port Detection

When running without Docker Compose, Maestro uses **discovery mode** to automatically detect which port the application is listening on. This eliminates the need for manual port configuration and handles framework diversity (3000, 5000, 8000, 8080, etc.).

**Discovery Flow:**
1. Start container without `-p` flag (discovery mode)
2. Poll `/proc/net/tcp` and `/proc/net/tcp6` inside the container
3. Detect listening sockets (state `0A` = LISTEN)
4. Check bind address (loopback vs reachable)
5. Select main port using priority order
6. Restart container with `-p 127.0.0.1::${containerPort}` (Docker-assigned host port)
7. TCP probe to verify connectivity
8. Save detected port to config for subsequent runs

**Port Selection Priority:**
1. User selection (`SelectedContainerPort`) - from UI port picker
2. Config override (`ContainerPortOverride`) - manual override in config.json
3. EXPOSE + LISTEN intersection - ports both exposed and listening
4. Preference order intersection - first match from `[80, 443, 8080, 8000, 3000, 5000, 5173, 4000]`
5. Lowest numbered listening port - fallback

### Bind Address Detection

Maestro detects when applications bind to loopback (127.0.0.1) inside the container - a common misconfiguration that prevents Docker port publishing from working.

**Reachability Rule:** Only loopback addresses (`127.0.0.1`, `::1`) are unreachable via Docker port publishing. All other addresses (`0.0.0.0`, `::`, container IPs like `172.x.x.x`) are reachable.

**Diagnostic Message:** When loopback binding is detected:
> "App is listening on 127.0.0.1:8080 inside the container, so it can't be reached via published ports. It must bind to 0.0.0.0 (or ::)."

### Port Caching

After successful detection, ports are cached in config for fast subsequent starts:
- First run: Full discovery (30 seconds max)
- Subsequent runs: Use cached port, verify with TCP probe
- If cached port fails: Fall back to discovery

### Rebuild Options

By default, rebuild re-runs port discovery (code changes may affect ports). To skip detection and use cached port:

```
POST /api/demo/rebuild
{"skip_detection": true}
```

### Port Reference

| Port | Service | Notes |
|------|---------|-------|
| 8080 | WebUI | Reserved, never used by demo |
| Dynamic | Demo app | Docker-assigned from detected container port |

Internal service ports (postgres 5432, redis 6379, etc.) are never exposed to host - only accessible within compose network.

## Lifecycle Management

### Startup Sequence

1. Maestro starts
2. Demo service initializes (no network/containers yet)
3. User explicitly starts demo via WebUI
4. Demo network created, containers started

### Shutdown Sequence

1. Maestro receives shutdown signal
2. Demo service: `docker compose -p demo down -v`
3. Demo network removed
4. All coder stacks: `docker compose -p coder-{id} down -v`
5. Coder networks removed
6. Clean exit

### Crash Recovery

If Maestro crashes:
- Docker containers continue running
- On next startup, detect orphaned containers via labels
- Clean up before proceeding
- Display warning if orphans found

## Template Updates

### Coder Devops Prompt

Update `pkg/templates/coder_devops.tmpl` to include:

```
When implementing local service dependencies (databases, caches, message queues, etc.):
- Use Docker Compose via .maestro/compose.yml
- Do NOT hardcode connection strings; use environment variables
- Include healthchecks for all services
- Use the compose_* tools to manage the compose file
```

### Architect Story Generation

Update story generation prompts to recognize service requirements and generate appropriate devops stories that specify Docker Compose implementation.

## Testing Strategy

### Unit Tests

| Package | Test Focus |
|---------|------------|
| `pkg/demo` | Service lifecycle, status management |
| `pkg/demo/stack` | Compose command generation, parsing |
| `pkg/demo/network` | Network operations (mocked Docker) |
| `pkg/demo/changes` | Git diff parsing, change categorization |
| `pkg/webui` | API endpoint handlers |

### Integration Tests

| Test | Description |
|------|-------------|
| `TestDemoStartStop` | Full demo lifecycle with real Docker |
| `TestDemoWithServices` | Demo with postgres/redis via compose |
| `TestCoderServiceStack` | Coder testing flow with services |
| `TestNetworkIsolation` | Verify coder stacks are isolated |
| `TestPMDemoAccess` | PM can reach demo container |
| `TestRestartVsRebuild` | Correct action based on file changes |
| `TestResourceWarnings` | Warnings shown at appropriate thresholds |
| `TestOrphanCleanup` | Cleanup of containers from crashed session |

### E2E Tests

| Test | Description |
|------|-------------|
| `TestFullDemoFlow` | User starts demo, accesses in browser, PM probes |
| `TestDemoAfterMerge` | Code change → outdated indicator → rebuild |
| `TestServiceDiscovery` | App can connect to services via env vars |

## Implementation Order

### Phase 0: Documentation
1. Add Demo Mode section to README.md with brief overview
2. Create wiki article or mermaid diagram showing container topology
3. Link from README to detailed documentation

### Phase 1: Core Infrastructure
1. `internal/state/compose.go` - Compose stack registry (alongside container registry)
2. `pkg/demo/network.go` - Network management
3. `pkg/demo/stack.go` - Compose stack operations (wraps compose CLI)
4. `pkg/demo/service.go` - Demo service struct and lifecycle
5. Unit tests for above

### Phase 2: WebUI Integration
1. API endpoints (`/api/demo/*`)
2. Demo control panel UI
3. Log polling (consistent with existing pattern)
4. Integration tests

### Phase 3: Coder Service Stacks
1. TESTING state service stack startup
2. Story complete teardown
3. Network connection management
4. Integration tests

### Phase 4: Change Detection & Polish
1. `pkg/demo/changes.go` - Git diff analysis
2. Restart vs Rebuild recommendations
3. Resource estimation and warnings
4. Template updates (coder devops, architect)

### Phase 5: Docker Compose Tools
1. `compose_read`, `compose_write` tools
2. `compose_add_service`, `compose_remove_service` tools
3. `compose_validate` tool
4. Tool documentation

### Phase 6: Secrets UI
1. WebUI secrets management endpoints
2. Secrets settings panel
3. Integration with existing secrets system

## Out of Scope (Future)

- Hot reload / mount-based restart
- Multiple simultaneous demos
- Reverse proxy / pretty URLs
- Persistent demo state between sessions
- Deployment artifact generation (k8s manifests, etc.)
- Shared services mode for resource-constrained environments
- CLI interface for secrets management

## Open Questions

None - all architectural decisions have been made.

## References

- [CLAUDE.md](/CLAUDE.md) - Project architecture and patterns
- [Container Architecture](/CLAUDE.md#container-architecture) - Existing container management
- [PM Agent](/pkg/pm/) - PM agent implementation
- [WebUI](/pkg/webui/) - Existing WebUI patterns
