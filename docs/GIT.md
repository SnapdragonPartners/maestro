# Git Workflow Implementation

This document describes how git operations are implemented in the maestro orchestrator system for managing code changes across multi-agent workflows.

## Overview

The maestro system uses a **clone-based git workflow** designed for containerized execution environments. Each agent works in complete isolation with self-contained git repositories while maintaining network efficiency through local mirrors.

## Architecture Components

### CloneManager (`pkg/coder/clone.go`)

The central component responsible for git repository management:
- **Purpose**: Manages git clone operations and workspace setup for coder agents
- **Design**: Provides complete agent isolation with self-contained repositories
- **Network Efficiency**: Uses local bare mirrors to reduce network traffic
- **Container Compatibility**: Creates fully independent clones that work in Docker containers

### Git Runner Interface (`pkg/coder/interfaces.go`)

Abstraction layer for git command execution:
- **DefaultGitRunner**: Standard implementation using system git commands
- **Error Handling**: Proper error wrapping and duplication elimination
- **Extensibility**: Interface allows for mocking and alternative implementations

### Git User Configuration

Configurable git identity system with agent-specific templates:
- **Template Substitution**: `{AGENT_ID}` placeholder replacement
- **Default Values**: `"Maestro {AGENT_ID}"` and `"maestro-{AGENT_ID}@localhost"`
- **Configuration Location**: `config.json` with `git_user_name` and `git_user_email` fields

## Workflow States

### SETUP Phase

**Responsibilities:**
- Create workspace with lightweight clone
- Configure git user identity (fail-fast approach)
- Create and checkout feature branch
- Setup container mounts

**Git Operations:**
1. Clone from local mirror to agent workspace
2. Configure git user name and email with agent ID substitution
3. Create branch using pattern `maestro/story-{STORY_ID}`
4. Checkout feature branch for development

### PREPARE_MERGE Phase

**Responsibilities:**
- Commit all changes
- Push branch to remote origin
- Create pull request via GitHub CLI
- Send merge request to architect

**Git Operations:**
1. `git add -A` - Stage all changes
2. `git diff --cached --exit-code` - Check for changes
3. `git commit -m "Story {ID}: Implementation complete"` - Commit with structured message
4. `git push -u origin {local_branch}:{remote_branch}` - Push feature branch
5. `gh pr create` - Create pull request with metadata

### AWAIT_MERGE Phase

**Responsibilities:**
- Wait for architect approval
- Handle merge completion or feedback
- Cleanup resources after successful merge

## Clone Strategy

### Self-Contained Clones

**Previous Approach (Deprecated):**
- Used git worktrees with `--shared --reference` flags
- Lightweight but caused issues in containerized environments
- Dependencies on external object stores

**Current Approach:**
```bash
# Step 1: Create/update bare mirror (shared across agents)
git clone --bare {REPO_URL} {MIRROR_PATH}
git remote update --prune  # For existing mirrors

# Step 2: Create self-contained clone (per agent)
git clone {MIRROR_PATH} {AGENT_WORKSPACE}
git remote set-url origin {REPO_URL}
```

**Benefits:**
- **Fast**: Local cloning from mirrors
- **Self-contained**: Works in Docker containers without external dependencies
- **Isolated**: Safe for concurrent agents
- **Complete**: All objects included for full git functionality

### Directory Structure

```
work/
├── .mirrors/
│   └── repository-name.git/     # Bare mirror (shared)
└── {agent-id}/
    └── story-{story-id}/        # Agent workspace (isolated)
        ├── .git/                # Complete git repository
        └── [project files]
```

## Branch Management

### Branch Naming

**Pattern:** `maestro/story-{STORY_ID}`
- Configurable via `branchPattern` in CloneManager
- Template replacement using `{STORY_ID}` placeholder
- Example: `maestro/story-001`, `maestro/story-002`

### Branch Creation

**Collision Handling:**
1. Check existing branches to avoid naming conflicts
2. Auto-increment branch names if collision detected
3. Maximum 10 attempts with fallback to trial-and-error
4. Example progression: `maestro/story-001` → `maestro/story-001-2` → `maestro/story-001-3`

**Implementation:**
```go
// Primary method with branch listing
existingBranches := getExistingBranches(ctx, agentWorkDir)
if !branchExists(branchName, existingBranches) {
    git.Run(ctx, agentWorkDir, "switch", "-c", branchName)
}

// Fallback method with error detection
if strings.Contains(err.Error(), "already exists") {
    branchName = fmt.Sprintf("%s-%d", originalName, attempt)
}
```

## Configuration

### Git User Identity

**Configuration Fields:**
```json
{
  "git": {
    "git_user_name": "Maestro {AGENT_ID}",
    "git_user_email": "maestro-{AGENT_ID}@localhost",
    "repo_url": "git@github.com:user/repo.git",
    "target_branch": "main"
  }
}
```

**Template Substitution:**
- `{AGENT_ID}` replaced with actual agent identifier
- Applied during SETUP phase configuration
- Example: `"Maestro coder-001"` and `"maestro-coder-001@localhost"`

### Repository Settings

**Required Configuration:**
- `repo_url`: Git repository URL for cloning and pushing
- `target_branch`: Base branch for pull requests (default: "main")
- `mirror_dir`: Directory for bare mirrors (default: ".mirrors")
- `branch_pattern`: Template for feature branch names

## Error Handling

### Recoverable vs Unrecoverable Errors

**Unrecoverable Errors (STATE_ERROR):**
- "not a git repository"
- "gh: command not found" 
- "git: command not found"
- "fatal: not a git repository"
- "no such file or directory"

**Recoverable Errors (return to CODING):**
- "nothing to commit"
- "working tree clean"
- "merge conflict"
- "permission denied"
- "authentication failed"
- "network", "timeout", "connection"
- "branch already exists"
- "pull request already exists"

**Default Behavior:**
- Unknown errors default to recoverable (safer to allow retry)
- Recoverable errors return agent to CODING state with error message
- Unrecoverable errors transition to ERROR state

## Container Integration

### Docker Compatibility

**Requirements:**
- Self-contained git repositories (no external object dependencies)
- Proper git user configuration before container startup
- Container mounts configured for workspace access

**Implementation:**
- Git user identity configured in SETUP phase (fail-fast)
- Complete .git directories mounted into containers
- No reliance on host git configuration or SSH keys within containers

### Resource Management

**Container Lifecycle:**
1. **SETUP**: Create container with read-only workspace for planning
2. **CODING**: Reconfigure container with read-write access and network
3. **CLEANUP**: Stop and remove containers after story completion

**Resource Limits:**
- Planning phase: 1 CPU, 512MB RAM, 256 PIDs, no network
- Coding phase: 2 CPUs, 2GB RAM, 1024 PIDs, network enabled

## State Machine Integration

### State Transitions

**Valid Git-Related Transitions:**
- `SETUP → PLANNING`: After successful workspace setup and git configuration
- `CODE_REVIEW → PREPARE_MERGE`: After architect approves implementation
- `PREPARE_MERGE → AWAIT_MERGE`: After successful PR creation
- `PREPARE_MERGE → CODING`: For recoverable git errors (retry loop)

**State Data Storage:**
- `KeyWorkspacePath`: Agent workspace directory path
- `KeyLocalBranchName`: Local feature branch name
- `KeyRemoteBranchName`: Remote feature branch name (initially same as local)
- `KeyPRURL`: Pull request URL after creation
- `KeyMergeResult`: Architect response to merge request

## Best Practices

### Performance Optimization

1. **Mirror Caching**: Bare mirrors shared across all agents reduce network traffic
2. **File Locking**: Prevent concurrent mirror updates using `syscall.Flock`
3. **Local Cloning**: Agent workspaces cloned from local mirrors for speed
4. **Resource Cleanup**: Comprehensive cleanup of workspaces, containers, and state

### Security Considerations

1. **User Identity**: Configurable git identity prevents hard-coded credentials
2. **Network Isolation**: Planning phase runs without network access
3. **Container Security**: Non-root users for application stories, root only for DevOps
4. **Workspace Isolation**: Complete separation between agent workspaces

### Debugging and Monitoring

**Logging:**
- Structured logging with agent ID and story ID context
- Debug-level logs for git command execution
- Error logs with full command output for troubleshooting

**State Persistence:**
- All state transitions logged to database
- Agent state dumps available in STATUS.md files
- Event logs in `logs/events.jsonl` with daily rotation

## Authentication Strategy

### Current Implementation (Personal Access Token)

The system currently uses **Personal Access Token (PAT)** authentication for GitHub operations:

**Design Decisions:**
- **Token Injection**: `GITHUB_TOKEN` environment variable injected during CODING phase transition
- **URL Conversion**: SSH URLs automatically converted to HTTPS format for container compatibility
- **Scope Management**: Token injected only in read-write containers, not during planning phase
- **Security Model**: Single-tenant, ephemeral containers with time-limited exposure

**Implementation Details:**
```go
// Container configuration during CODING phase
if githubToken := os.Getenv("GITHUB_TOKEN"); githubToken != "" {
    execOpts.Env = append(execOpts.Env, "GITHUB_TOKEN="+githubToken)
}

// URL conversion: git@github.com:user/repo.git -> https://github.com/user/repo.git
httpsURL := c.convertToHTTPSURL(c.repoURL)
git.Run(ctx, workDir, "remote", "set-url", "origin", httpsURL)
```

**Authentication Flow:**
1. Host environment provides `GITHUB_TOKEN` (PAT)
2. During SETUP → CODING transition, token injected into container
3. Git remote URLs converted from SSH to HTTPS format
4. Container git operations use token via HTTPS: `https://x-access-token:$GITHUB_TOKEN@github.com/org/repo.git`

### Future Security Enhancement (Recommended)

For improved security posture, migrate to **GitHub App** authentication:

**Benefits:**
- **Short-lived tokens**: 1-hour TTL vs long-lived PATs
- **Repo-scoped access**: Only specific repositories, not all user repos
- **Audit trail**: Separate bot identity vs personal user actions
- **Revocation control**: Instant app uninstall vs manual token management

**Migration Path:**
1. Register GitHub App with minimal permissions (`contents: write`, `pull_requests: write`)
2. Install app on target repositories only
3. Modify orchestrator to request installation tokens before agent operations:
   ```bash
   POST /app/installations/:id/access_tokens
   # Returns token with 1-hour TTL
   ```
4. Continue using same HTTPS URL format and container injection pattern

**Security Comparison:**
```
Current (PAT):  90-day expiry, all-repo access, personal identity
Future (App):   1-hour expiry, repo-specific, bot identity
```

## Migration from Worktrees

The system was migrated from a worktree-based approach to the current clone-based approach for better container compatibility:

**Previous Issues:**
- Worktrees with `--shared --reference` failed in containers
- External dependencies on host git configuration
- Complex cleanup and state management

**Migration Benefits:**
- Complete container compatibility
- Simplified state management
- Better error handling and recovery
- Improved agent isolation

All references to worktrees have been removed from the codebase and replaced with the current clone-based implementation.