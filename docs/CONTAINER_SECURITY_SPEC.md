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

## Acceptance Criteria

- [ ] Coder containers start successfully without `gh` installed
- [ ] `git push` operations succeed via host execution
- [ ] `maestro-bootstrap` name is protected from overwrites
- [ ] Project containers use auto-generated names
- [ ] Existing PR creation flow continues to work (already on host)
