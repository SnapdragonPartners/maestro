# Container Security and Naming Spec

## Overview

This spec documents two related changes to improve container security and reliability:

1. **Remove GitHub credentials from containers** - Coders should not have push access; all git push operations should run on the host
2. **Protect container naming** - The `maestro-bootstrap` name must be reserved for the embedded bootstrap container; project containers should use auto-generated names

## Problem Statement

### Issue 1: Credentials in Containers

Currently, coder containers have GitHub credentials injected:
- `GITHUB_TOKEN` is passed to the container environment
- `gh auth setup-git` configures git credential helper inside container
- `git push` runs inside the container

**Risks:**
- Coders can push unapproved code directly to remote
- Requires `gh` CLI to be installed in every container
- Container startup fails if `gh` is missing (current bug)

### Issue 2: Container Name Conflicts

The `maestro-bootstrap` container name can be overwritten by project-specific containers:
- Bootstrap stories generate Dockerfiles that get tagged as `maestro-bootstrap:latest`
- This overwrites the safe fallback container built from the embedded Dockerfile
- Results in missing tools (like `gh`) when the system falls back to bootstrap container

**Current failure mode:**
```
exec: "gh": executable file not found in $PATH
```
This occurs because a minimal project container overwrote the full bootstrap container.

## Proposed Changes

### Part 1: Remove GitHub Credentials from Containers

#### 1.1 Remove from `pkg/coder/setup.go`

**Remove `setupGitHubAuthentication()` function entirely.** This function:
- Checks for GITHUB_TOKEN (keep this check, but don't inject into container)
- Runs `gh auth setup-git` inside container (remove)
- Calls `verifyGitHubAuthSetup()` (remove)
- Calls `configureGitUserIdentity()` (keep - needed for commits)

**Remove `verifyGitHubAuthSetup()` function.** This function:
- Checks `git --version` inside container (keep - useful verification)
- Checks `gh --version` inside container (remove - gh not needed)
- Calls `validateGitHubAPIConnectivity()` (remove)
- Checks git credential helper config (remove)

**Remove `validateGitHubAPIConnectivity()` function entirely.** This function:
- Runs `gh api /user` inside container (remove)
- Runs `gh api /repos/{path}` inside container (remove)

**Keep `configureGitUserIdentity()`.** Git commits still happen inside container and need user.name/user.email configured.

#### 1.2 Modify `pkg/coder/prepare_merge.go`

**Change `pushBranch()` to run on host instead of container:**

Current (runs in container):
```go
result, err := c.longRunningExecutor.Run(ctx, []string{"git", "push", ...}, opts)
```

New (runs on host):
```go
cmd := exec.CommandContext(ctx, "git", "push", "-u", "origin", fmt.Sprintf("%s:%s", localBranch, remoteBranch))
cmd.Dir = c.workDir  // Host-side path to coder workspace
cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+os.Getenv("GITHUB_TOKEN"))
output, err := cmd.CombinedOutput()
```

The `c.workDir` is the host-side path (e.g., `/path/to/project/coder-001`), which is bind-mounted into the container. Git operations can run from either side.

#### 1.3 Remove GITHUB_TOKEN from Container Environment

In `pkg/coder/setup.go` and any other locations where container environment is configured, ensure `GITHUB_TOKEN` is NOT passed to the container.

### Part 2: Container Naming Protection

#### 2.1 Reserved Names

The following container names are reserved and cannot be used for project containers:
- `maestro-bootstrap` (and any tag, e.g., `maestro-bootstrap:latest`, `maestro-bootstrap:v1`)

#### 2.2 Auto-Generated Container Names

Project containers should use auto-generated names based on:
- Project name (from config)
- Dockerfile name (to support multiple Dockerfiles like GPU/non-GPU)

**Format:** `maestro-<projectname>-<dockerfile>:latest`

**Examples:**
- Project "myapp" with default Dockerfile: `maestro-myapp-dockerfile:latest`
- Project "myapp" with GPU Dockerfile: `maestro-myapp-dockerfile-gpu:latest`

#### 2.3 Tool Changes

**`container_build` tool (`pkg/tools/container_build.go`):**
- Remove `image_name` parameter from tool schema (or make it optional/ignored)
- Auto-generate image name from project config and Dockerfile path
- Reject any attempt to use reserved names

**`container_update` tool (`pkg/tools/container_update.go`):**
- Remove `image_name` parameter from tool schema (or make it optional/ignored)
- Auto-generate image name from project config and Dockerfile path
- Reject any attempt to use reserved names

#### 2.4 Bootstrap Container Protection

In `pkg/coder/driver.go` `ensureBootstrapContainer()`:
- Always rebuild if the existing `maestro-bootstrap:latest` image doesn't match expected characteristics
- Consider adding a label to identify legitimate bootstrap containers

## Security Model

After these changes:

1. **Coders can:**
   - Read/write files in their workspace
   - Run shell commands (build, test, lint)
   - Create and switch branches locally (`git checkout -b`, `git branch`)
   - Commit changes locally (`git commit`)
   - Request code review from architect

2. **Coders cannot:**
   - Push to remote repository
   - Access GitHub API
   - Overwrite the bootstrap container

3. **Host/Orchestrator can:**
   - Push approved changes to remote (via `git push` on host)
   - Create/merge PRs (via `gh` on host)
   - Build and manage containers

## Implementation Order

1. **Phase 1: Remove gh from container setup** (unblocks current bug)
   - Modify `setupGitHubAuthentication()`
   - Modify `verifyGitHubAuthSetup()`
   - Remove `validateGitHubAPIConnectivity()`
   - Move `pushBranch()` to host

2. **Phase 2: Container naming protection** (prevents future issues)
   - Add reserved name checking to container tools
   - Implement auto-generated naming
   - Update container_build and container_update tools

## Testing

1. **Unit tests:**
   - Verify container setup succeeds without gh
   - Verify git push works from host
   - Verify reserved names are rejected

2. **Integration tests:**
   - Full coder flow: commit → push → PR creation
   - Bootstrap container protection

## Part 3: Docker Compose Container Naming

### Problem

When multiple agents (coder-001, coder-002, demo) use the same `compose.yml` file with different project names (`-p maestro-coder-001`, `-p demo`), hardcoded `container_name` values cause collisions:

```yaml
# PROBLEMATIC - causes collisions
services:
  db:
    container_name: helloworld-db  # All projects try to create this same name!
```

### Solution: Sanitize Compose Files

The `Stack.Up()` method sanitizes compose files before running them:

1. **Strip `container_name`**: Remove any hardcoded `container_name` from the compose file
2. **Use temp file**: Write sanitized version to `/tmp/` to preserve original for diffs/PRs
3. **Let Docker generate names**: Docker Compose automatically creates unique names: `{project}-{service}-{instance}`

### Implementation

```go
// In Stack.Up():
// 1. Read compose file
// 2. Strip container_name from all services
// 3. Write to temp file
// 4. Pass temp file to docker compose -f
```

### Additional Flags

- `--remove-orphans`: Added to `compose up` and `compose down` to clean up services removed from the file

### Prompt Guidance

Templates instruct coders to never use `container_name` in compose files:
- `bootstrap.tpl.md`: "Do NOT use `container_name` - let Docker Compose generate unique names"
- `app_coding.tpl.md`: Similar guidance in compose_up tool documentation

### Defense in Depth

1. **Prompt guidance**: Coders are told not to use `container_name`
2. **Server-side sanitization**: `Stack.Up()` strips `container_name` even if coders add it
3. **Cleanup**: `--remove-orphans` prevents accumulation of stale containers

## Part 4: Container Labeling and Cleanup

### Container Labels

All Maestro-managed containers are labeled for identification and cleanup:

```bash
--label com.maestro.managed=true      # All Maestro containers
--label com.maestro.agent=<agent-id>  # Agent that owns the container
--label com.maestro.session=<uuid>    # Session ID for multi-instance isolation
```

### Cleanup on Shutdown

The dispatcher's `Stop()` method calls `StopAllContainersDirect()` to stop all registered containers:

1. Iterates through all containers in the registry
2. Calls `docker stop -t 10` for graceful shutdown
3. Falls back to `docker rm -f` if stop fails
4. Logs each container being stopped

### Manual Cleanup

To clean up all Maestro containers (all sessions):
```bash
docker rm -f $(docker ps -q --filter "label=com.maestro.managed=true")
```

To clean up containers from a specific session:
```bash
docker rm -f $(docker ps -q --filter "label=com.maestro.session=<session-id>")
```

## Container Ownership & Cleanup Summary

| Container | Name Pattern | Labels | Cleanup Method |
|-----------|--------------|--------|----------------|
| PM | `maestro-pm` | `com.maestro.*` | `ContainerRegistry.StopAllContainersDirect()` |
| Architect | `maestro-architect` | `com.maestro.*` | `ContainerRegistry.StopAllContainersDirect()` |
| Coders | `maestro-story-*` | `com.maestro.*` | `ContainerRegistry.StopAllContainersDirect()` |
| Demo | `maestro-demo` | `com.maestro.*` | `DemoService.Cleanup()` |
| Demo Compose | `demo-<service>-1` | Docker Compose native | `ComposeRegistry.Cleanup()` |
| Coder Compose | `maestro-coder-*-<service>-1` | Docker Compose native (sanitized) | `ComposeRegistry.Cleanup()` |

### No Collision Between PM and Demo

- PM runs in `maestro-pm` container (read tools for spec generation)
- Demo runs in `maestro-demo` container OR compose project `demo`
- Different names, different purposes, no overlap

### Shutdown Order (in kernel.go)

1. `DemoService.Cleanup()` - stops demo container/compose
2. `ComposeRegistry.Cleanup()` - stops all compose stacks
3. `Dispatcher.Stop()` → `ContainerRegistry.StopAllContainersDirect()` - stops PM, architect, coders

## Part 5: Docker Compose Network Connectivity

### Problem

When a coder's `compose.yml` defines services (e.g., PostgreSQL), `compose_up` starts them
successfully but the coder container cannot reach them by hostname, localhost, or container name.

**Root cause:** The coder container and compose services are on different Docker networks.

- The coder container starts on Docker's default `bridge` network (e.g., 172.17.0.x)
- `docker compose up` creates its own network `maestro-coder-001_default` (e.g., 172.22.0.x)
- These networks are isolated — no DNS resolution or routing between them

**Why demo mode works:** Demo mode starts a *new* app container with `--network demo_default`,
placing it directly on the compose network at creation time. Coders cannot do this because the
container already exists when compose starts later in the CODING/TESTING states.

### Solution: `Stack.UpAndAttach()`

After `docker compose up` succeeds, connect the existing coder container to the compose network
using `docker network connect`. This adds the compose network as a second interface — the
container keeps its `bridge` connection (internet access) and gains access to compose services
by hostname.

To prevent duplication of this policy across call sites, the fix is centralized as a single
operation on the `Stack` abstraction:

```go
// Stack.UpAndAttach does compose up, then connects the given container to the
// compose project's default network so it can reach compose services by hostname.
func (s *Stack) UpAndAttach(ctx context.Context, containerName string) error {
    // 1. Run docker compose up (idempotent)
    if err := s.Up(ctx); err != nil {
        return err
    }

    // 2. Determine compose network name ({project}_default convention)
    networkName := s.ProjectName + "_default"

    // 3. Verify the network exists (compose may define custom networks)
    // If not found, emit diagnostic rather than failing silently

    // 4. Connect the agent container (idempotent — no-ops if already connected)
    return networkMgr.ConnectContainer(ctx, networkName, containerName)
}
```

Both call sites (`ensureComposeStackRunning()` in the coder state machine and `ComposeUpTool.Exec()`
in the MCP tool) use `UpAndAttach()` instead of `Up()` directly. This ensures hybrid compose
networking is correct everywhere by construction.

### Network Selection

- **Default:** `{project}_default` — covers the standard Docker Compose convention
- **Fallback:** If the network doesn't exist after compose up, emit a clear diagnostic:
  "compose network not found; compose file may define custom networks"
- **Future enhancement (optional):** Discover networks by compose labels
  (`com.docker.compose.project=<project>`) for compose files with custom network definitions

### Call Sites

| Caller | File | Container Name Source |
|--------|------|---------------------|
| `ensureComposeStackRunning()` | `pkg/coder/testing.go` | `"maestro-story-" + agentID` (from executor prefix) |
| `ComposeUpTool.Exec()` | `pkg/tools/compose_up.go` | Injected via `AgentContext` at tool construction |

### Safety Properties

- **Idempotent:** `ConnectContainer()` checks `IsConnected()` first and no-ops if already connected.
  Multiple calls to `compose_up` during a dev cycle (CODING → TESTING → CODING) are safe.
- **Internet preserved:** `docker network connect` adds a network without removing existing ones.
  The container remains dual-homed on both `bridge` (internet) and the compose network (services).
- **No compose file modification:** Compose manages its own network; we simply join the container
  to it after the fact. No brittle YAML injection.
- **Bidirectional access:** Services on the compose network can also initiate connections back to the
  coder container. This is acceptable for local development but worth noting as a security posture change.

### Testing

- **Integration test:** Start a trivial compose service (e.g., `busybox` with `nc -l`) and verify
  connectivity from inside the coder container (`nc -z <service> <port>`)
- **Debug logging on failure:**
  - Log the attempted network name
  - List compose project networks
  - Log current networks attached to the coder container (`docker inspect`)

### Implementation Files

| File | Change |
|------|--------|
| `pkg/demo/stack.go` | Add `UpAndAttach(ctx, containerName)` method |
| `pkg/coder/testing.go` | `ensureComposeStackRunning()` calls `UpAndAttach()` |
| `pkg/tools/compose_up.go` | `ComposeUpTool` stores container name, `Exec()` calls `UpAndAttach()` |
| `pkg/tools/registry.go` | Pass container name to `ComposeUpTool` via `AgentContext` |

## Acceptance Criteria

- [ ] Coder containers start successfully without `gh` installed
- [ ] `git push` operations succeed via host execution
- [ ] `maestro-bootstrap` name is protected from overwrites
- [ ] Project containers use auto-generated names
- [ ] Existing PR creation flow continues to work (already on host)
- [ ] Docker Compose files are sanitized to remove `container_name`
- [ ] Multiple agents can use the same compose.yml without collisions
- [ ] All containers are labeled with `com.maestro.*` labels
- [ ] Containers are stopped during graceful shutdown
- [ ] Coder containers can reach compose services by hostname after `compose_up`
- [ ] `UpAndAttach()` is idempotent across multiple calls per story
- [ ] Internet access is preserved when container joins compose network
