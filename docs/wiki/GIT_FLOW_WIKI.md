# File System Management and Git Workflow

## Overview

Maestro implements a sophisticated multi-agent development system where each AI agent operates in complete isolation with its own containerized environment and git workspace. The architecture ensures safe concurrent development while maintaining code quality through a structured review and merge workflow.

This document explains how Maestro manages file systems, git repositories, Docker containers, and the commit-merge workflow across different agent types.

## The Problem

Multi-agent AI coding systems face several unique challenges:

- **Concurrent Modifications**: Multiple agents working simultaneously need isolated workspaces to avoid conflicts
- **Container Compatibility**: Agents run in Docker containers that must have complete, self-contained git repositories
- **Network Efficiency**: Cloning large repositories repeatedly wastes bandwidth and time
- **Review Requirements**: Architect agents need read-only access to inspect coder work without risk of modification
- **State Isolation**: Each agent's work must be independent and recoverable

## The Solution

Maestro uses a **clone-based architecture** with strategic mount points and git workflows:

1. **Mirror Repository**: Shared bare git mirror provides fast local cloning
2. **Isolated Workspaces**: Each coder gets a complete self-contained git clone
3. **Container Mounting**: Strategic read-only and read-write mounts based on workflow phase
4. **Structured Branching**: Consistent branch naming with automatic collision handling
5. **Clear Responsibilities**: Well-defined commit and merge responsibilities per agent type

## Directory Structure

```
projectDir/
├── .maestro/                    # Master configuration
│   ├── config.json             # Project config with pinned Docker image IDs
│   └── maestro.db              # Agent state, messages, and history database
│
├── .mirrors/                    # Git repository mirrors
│   └── project-name.git/       # Bare mirror (shared across all agents)
│
├── coder-001/                   # Coder agent workspace #1
│   ├── .git/                   # Complete git repository
│   ├── .maestro/               # Committed artifacts (knowledge graph, etc.)
│   └── [project files]         # Working tree
│
├── coder-002/                   # Coder agent workspace #2
│   └── ...
│
├── architect-001/               # Architect workspace (planned)
│   ├── .git/                   # Read-only reference clone
│   ├── .maestro/               # Access to knowledge graph
│   └── [project files]         # Latest main branch for scoping
│
└── ...
```

**Key Characteristics**:
- **Workspace Pre-creation**: All coder directories are created before agent execution starts
- **Self-contained**: Each workspace contains a complete `.git` directory with all objects
- **Isolated**: Agents cannot see or modify each other's workspaces
- **Persistent**: Workspaces remain after story completion for debugging and history

## Git Mirror Architecture

### Purpose and Benefits

The **mirror repository** is a bare git clone at `<projectDir>/.mirrors/project-name.git` that serves as a local cache:

**Benefits**:
- **Fast Cloning**: Local filesystem cloning is 10-100x faster than network cloning
- **Bandwidth Efficiency**: Repository is fetched from remote once, used by all agents
- **Offline Capability**: Agents can start new work even during network outages
- **Consistency**: All agents clone from the same synchronized state

### Mirror Management

**Initial Setup** (`pkg/coder/clone.go:181-224`):
```bash
# Create bare mirror
git clone --bare <REPO_URL> <projectDir>/.mirrors/project-name.git
```

**Subsequent Updates**:
```bash
# Update existing mirror
cd <projectDir>/.mirrors/project-name.git
git remote update --prune
```

**Concurrency Safety**:
- File locking (`.update.lock`) prevents concurrent git operations
- Uses `syscall.Flock` with exclusive lock (`LOCK_EX`)
- Ensures mirror integrity during parallel agent startup

### When Mirrors Are Updated

1. **Agent Startup**: Before creating a new workspace, mirror is updated if stale
2. **Workspace Setup**: During SETUP phase, mirror provides fast clone source
3. **Never During Work**: Mirror updates never interrupt active agent work

## Coder Agent Workflow

### Workspace Lifecycle

Each coder agent receives a dedicated workspace directory (`coder-NNN/`) for the duration of a story implementation.

**Workspace Setup** (`pkg/coder/clone.go:62-105`):

1. **Ensure Mirror Updated**: Update or create bare mirror at `.mirrors/`
2. **Clone from Mirror**: Create self-contained clone in `coder-NNN/`
   ```bash
   git clone <projectDir>/.mirrors/project-name.git <projectDir>/coder-001
   ```
3. **Configure Remote**: Set origin to actual remote URL (not mirror)
   ```bash
   cd <projectDir>/coder-001
   git remote set-url origin <REPO_URL>
   ```
4. **Git Identity**: Configure git user identity with agent ID substitution
   ```bash
   git config user.name "Maestro coder-001"
   git config user.email "coder-001@maestro.local"
   ```
5. **Create Branch**: Create feature branch with pattern `maestro/story-{ID}`
   ```bash
   git switch -c maestro/story-001
   ```

**Why Self-Contained Clones?**
- Previous approach used git worktrees with `--shared --reference`
- Worktrees failed in Docker containers due to external dependencies
- Current approach: complete `.git` directory works perfectly in containers
- Each workspace has all objects needed for full git functionality

### Container Mounting Strategy

Coder containers mount their workspace differently depending on the workflow phase:

#### Planning Phase (Read-Only Mount)

**Purpose**: Allow coder to explore code safely without modifications

**Container Configuration**:
- Mount: `<projectDir>/coder-001:/workspace:ro` (read-only)
- Resources: 1 CPU, 512MB RAM, 256 PIDs
- Network: Disabled (no external communication)
- User: Non-root (security hardening)

**What Coders Can Do**:
- Read all project files
- Run analysis tools (grep, find, etc.)
- Explore codebase structure
- Plan implementation approach

**What Coders Cannot Do**:
- Modify any files (filesystem is read-only)
- Commit changes
- Install packages
- Access network APIs

#### Coding Phase (Read-Write Mount)

**Purpose**: Allow coder to implement changes with full git access

**Container Configuration** (`pkg/exec/docker_long_running.go:160-212`):
- Mount: `<projectDir>/coder-001:/workspace:rw` (read-write)
- Resources: 2 CPUs, 2GB RAM, 1024 PIDs
- Network: Enabled (package installation, API access)
- User: Non-root for app code, root for DevOps tasks

**What Coders Can Do**:
- Modify, create, and delete files
- Run tests and build commands
- Install dependencies
- Commit changes locally
- Push branches to remote

**Additional Mounts**:
- Docker socket: `/var/run/docker.sock` (for container self-management)
- Tmpfs: `/tmp`, `/home`, `/.cache` (writable scratch space)

### Branch Management

**Branch Naming Pattern**: `maestro/story-{STORY_ID}`

Example: `maestro/story-001`, `maestro/story-042`, etc.

**Collision Handling** (`pkg/coder/clone.go:266-330`):
- Check existing branches before creation
- Auto-increment if branch already exists
- Example: `maestro/story-001` → `maestro/story-001-2` → `maestro/story-001-3`
- Maximum 10 attempts before failing with error

**Why Collision Detection?**
- Multiple story attempts (revision after rejection)
- Agent restarts during development
- Manual branch creation by developers
- Prevents git errors and provides predictable behavior

### Git Operations by Phase

#### SETUP Phase

**Git Configuration** (`pkg/coder/clone.go:415-448`):
- Configure git user identity **on host** (before container mounts)
- Template substitution: `{AGENT_ID}` → actual agent ID
- Example: `"Maestro {AGENT_ID}"` → `"Maestro coder-001"`
- **Fail-fast approach**: Git errors during SETUP transition to ERROR state

**Why Configure on Host?**
- Container filesystem may be read-only during planning
- Ensures git identity is set before any git operations
- Prevents authentication issues during commit/push

#### PREPARE_MERGE Phase

**Responsibilities** (`pkg/coder/prepare_merge.go:22-138`):

1. **Stage Changes**:
   ```bash
   git add -A
   ```

2. **Check for Modifications**:
   ```bash
   git diff --cached --exit-code
   # Exit code 0: no changes (recoverable error)
   # Exit code 1: changes present (continue)
   ```

3. **Commit Changes**:
   ```bash
   git commit -m "Story {ID}: Implementation complete"
   ```

4. **Push Branch**:
   ```bash
   git push -u origin maestro/story-001:maestro/story-001
   ```

5. **Create Pull Request**:
   ```bash
   gh pr create \
     --title "Story 001: {Title}" \
     --body "{Description}" \
     --base main
   ```

**Error Handling** (`pkg/coder/prepare_merge.go:287-340`):
- **Recoverable errors** (return to CODING):
  - "nothing to commit" / "working tree clean"
  - "merge conflict"
  - "authentication failed"
  - "network" / "timeout" / "connection"
  - "branch already exists"
  - "pull request already exists"
- **Unrecoverable errors** (transition to ERROR):
  - "not a git repository"
  - "gh: command not found"
  - "git: command not found"

#### AWAIT_MERGE Phase

**Responsibilities** (`pkg/coder/await_merge.go:18-84`):
- Wait for architect to approve, request changes, or reject
- Process feedback and transition to appropriate state
- Clean up resources after successful merge

**Possible Outcomes**:
- **Approved**: Transition to DONE, cleanup workspace
- **Needs Changes**: Return to CODING with architect feedback
- **Rejected**: Transition to ERROR with rejection reason

### Authentication Strategy

**Current Implementation**: Personal Access Token (PAT)

**Token Injection** (`pkg/coder/prepare_merge.go:120-135`):
- `GITHUB_TOKEN` environment variable injected during CODING phase
- Token available only in read-write containers, not during planning
- Security model: single-tenant, ephemeral containers with time-limited exposure

**URL Conversion** (automatic):
```bash
# SSH format (not container-friendly)
git@github.com:user/repo.git

# Converted to HTTPS format (works in containers)
https://github.com/user/repo.git

# Git uses token automatically
https://x-access-token:$GITHUB_TOKEN@github.com/user/repo.git
```

**Future Enhancement**: GitHub App with 1-hour tokens (planned)

### Workspace Cleanup

**After Story Completion** (`pkg/coder/clone.go:108-179`):
- Container stopped and removed
- Workspace directory deleted: `rm -rf <projectDir>/coder-001`
- State data cleared from database
- Resources freed for next story

**For Debugging**:
- Workspaces can be inspected before cleanup
- Event logs capture all git operations
- STATUS.md files contain agent state dumps

## Architect Agent Workflow

### Execution Environment

The architect runs in a **single long-lived container** with read-only access to all coder workspaces and the mirror repository.

**Container Configuration** (`pkg/exec/architect_executor.go:82-170`):

```bash
docker run -d \
  --name maestro-architect \
  --security-opt no-new-privileges \
  --read-only \  # Root filesystem is read-only
  --cpus 2 \
  --memory 2g \
  --pids-limit 256 \
  --user 0:0 \  # Root user (safe because all mounts are read-only)

  # Mount all coder workspaces (read-only)
  -v <projectDir>/coder-001:/mnt/coders/coder-001:ro \
  -v <projectDir>/coder-002:/mnt/coders/coder-002:ro \
  -v <projectDir>/coder-003:/mnt/coders/coder-003:ro \
  # ... up to maxCoders (typically 10-20)

  # Mount mirror repository (read-only)
  -v <projectDir>/.mirrors:/mnt/mirror:ro \

  # Writable scratch space
  --tmpfs /tmp:exec,nodev,nosuid,size=2g \
  --tmpfs /home:exec,nodev,nosuid,size=100m \
  --tmpfs /.cache:exec,nodev,nosuid,size=100m \

  maestro-bootstrap sleep infinity
```

**Key Design Decisions**:
- **Read-only mounts**: Architect cannot modify coder work
- **All workspaces mounted**: Architect can inspect any active coder
- **Pre-created directories**: All `coder-NNN/` directories exist even if empty
- **Long-lived**: Container persists across multiple stories
- **Network enabled**: Architect needs LLM API access

### Architect Workspace (Planned)

As part of the knowledge graph implementation, the architect will also have a dedicated workspace:

**Location**: `<projectDir>/architect-001/`

**Purpose**:
- Provide architect with an up-to-date clone of the main branch
- Enable architect to read committed artifacts like `.maestro/knowledge.dot`
- Support scoping activities that need access to repository structure

**Mount Configuration** (planned):
```bash
-v <projectDir>/architect-001:/mnt/architect:ro
```

**Update Strategy**:
- Updated before SCOPING state (ensure latest main branch)
- Updated after each successful merge (keep in sync with repository)
- Uses same clone mechanism as coder workspaces

**Use Cases**:
- Reading knowledge graph during scoping: `/mnt/architect/.maestro/knowledge.dot`
- Inspecting repository structure for story generation
- Accessing architectural documentation

### Code Inspection Tools

The architect uses MCP (Model Context Protocol) tools to inspect coder workspaces with read-only access:

#### read_file Tool (`pkg/tools/read_file.go`)

**Purpose**: Read specific files from coder workspaces

**Signature**:
```json
{
  "coder_id": "coder-001",
  "path": "src/main.go"
}
```

**Implementation**:
- Validates `coder_id` format (prevents directory traversal)
- Resolves path to `/mnt/coders/coder-001/src/main.go`
- Reads up to 1MB (configurable limit)
- Returns file content or error message

**Security**:
- Cannot read files outside coder workspace
- Cannot modify files (read-only mount)
- Cannot access host filesystem

#### list_files Tool (`pkg/tools/list_files.go`)

**Purpose**: List files matching patterns in coder workspace

**Signature**:
```json
{
  "coder_id": "coder-001",
  "pattern": "**/*.go"
}
```

**Implementation**:
- Uses `find` command with glob pattern matching
- Returns up to 1000 file paths (configurable)
- Sorted by modification time (newest first)

**Use Cases**:
- Discover what files the coder created or modified
- Find specific file types for review
- Navigate unfamiliar codebase structure

#### get_diff Tool (`pkg/tools/get_diff.go`)

**Purpose**: View git diff of coder changes

**Signature**:
```json
{
  "coder_id": "coder-001",
  "file_path": "src/main.go"  // Optional: specific file
}
```

**Implementation**:
```bash
cd /mnt/coders/coder-001
git diff --no-color --no-ext-diff origin/main [file_path]
```

**Returns**:
- Unified diff format showing all changes
- Up to 10,000 lines (configurable limit)
- Can diff entire workspace or specific files

**Why This Is Powerful**:
- Architect sees exactly what the coder changed
- No need to fetch or download code to external review tool
- Immediate access without leaving LLM context

#### submit_reply Tool (`pkg/tools/submit_reply.go`)

**Purpose**: Exit iteration loop and send response to coder

**Signature**:
```json
{
  "message": "Approved. Changes look good."
}
```

**Control Flow**:
- Returns special `action: "submit"` signal
- Breaks out of architect's read-iterate-submit loop
- Sends message to coder as REQUEST→RESULT or QUESTION→ANSWER

### Architect Git Responsibilities

**What Architects Do**:
- **Review Code**: Inspect coder changes using read tools
- **Approve/Reject**: Send approval or request changes
- **Provide Guidance**: Answer coder questions about architecture
- **Validate Quality**: Ensure changes meet acceptance criteria

**What Architects Do NOT Do**:
- **No Direct Commits**: Architects never commit code
- **No Branch Management**: Architects never create or merge branches
- **No File Modifications**: Architects cannot edit files
- **No PR Merging**: GitHub Actions or humans merge PRs

**Why This Separation?**:
- Clear audit trail (all commits attributed to coder agents)
- Security (architect cannot accidentally break codebase)
- Accountability (coder responsible for implementation quality)

## Commit and Merge Responsibilities

### Coder Responsibilities

**Commits**:
- ✅ Create commits with structured messages
- ✅ Stage all changes (`git add -A`)
- ✅ Push branches to remote
- ✅ Create pull requests via GitHub CLI
- ❌ Never commit directly to main branch
- ❌ Never force push without explicit instruction

**Branch Management**:
- ✅ Create feature branches with `maestro/story-{ID}` pattern
- ✅ Handle branch name collisions gracefully
- ✅ Keep branches focused on single story
- ❌ Never rebase without architect approval
- ❌ Never merge own pull requests

**Merge Workflow**:
1. Complete implementation and tests
2. Transition to PREPARE_MERGE state
3. Commit all changes with story ID in message
4. Push branch to remote origin
5. Create pull request targeting main branch
6. Send merge request to architect via effect
7. Wait in AWAIT_MERGE state for architect response

### Architect Responsibilities

**Code Review**:
- ✅ Inspect code using read_file, list_files, get_diff tools
- ✅ Verify implementation meets acceptance criteria
- ✅ Check code quality, tests, and documentation
- ✅ Provide specific feedback for revisions
- ❌ Never modify coder files directly
- ❌ Never commit or push on behalf of coder

**Approval Workflow**:
1. Receive merge request from coder (REQUEST message)
2. Use read tools to inspect coder workspace
3. Review git diff against main branch
4. Evaluate against story acceptance criteria
5. Send response:
   - **Approved**: Coder transitions to DONE, PR ready for merge
   - **Needs Changes**: Coder returns to CODING with feedback
   - **Rejected**: Coder transitions to ERROR, story fails

**PR Merge Authority**:
- Architects **approve** but do not merge
- GitHub Actions or human operators perform actual merge
- Merge triggers post-merge hooks (mirror updates, workspace cleanup)

### Merge Conflict Handling

**Detection**: During `git push` or PR creation

**Resolution Process**:
1. Coder encounters merge conflict error
2. Error classified as **recoverable**
3. Coder transitions back to CODING state
4. Architect provides guidance:
   ```
   Pull the latest main branch and resolve conflicts.

   For file X:
   - Keep your implementation of function A
   - Keep main's version of function B
   - Merge both sets of test cases

   After resolving:
   1. Run tests to ensure nothing broke
   2. Commit with message "Resolved merge conflicts"
   3. Push updated branch
   ```

**Automatic Recovery**:
- Merge conflicts don't fail the story
- Coder can pull, resolve, and re-attempt merge
- Architect guides resolution strategy

**Knowledge Graph Conflicts** (special handling):
- `.maestro/knowledge.dot` conflicts receive specific guidance
- Architect instructs: keep all unique nodes, merge complementary descriptions
- Ensures knowledge base consistency

## Container Self-Management

### Why Coders Manage Their Own Containers

Coders have access to container management tools to handle environment changes mid-execution:

**Container Tools Available to Coders**:
- `container_build`: Build Docker images from Dockerfile
- `container_test`: Run validation in temporary throwaway containers
- `container_switch`: Switch active execution environment
- `container_update`: Set persistent target image configuration

**Use Cases**:
1. **Project Setup**: Build initial development container from Dockerfile
2. **Environment Issues**: Switch to bootstrap container if target fails
3. **Validation**: Test changes in clean container before committing
4. **Configuration Updates**: Update project config with new container image

**Mount Policy for Test Containers** (`pkg/tools/container_test_tool.go:129-136`):
- **CODING state**: `/workspace` mounted read-write (line 132)
- **All other states**: `/workspace` mounted read-only (line 135)
- `/tmp` always writable (line 154)

**Security Boundaries**:
- Coders cannot access architect container
- Coders cannot see other coder containers
- Docker socket access limited to self-management
- Container registry tracks all containers for cleanup

### Three-Container Model

**1. Safe Container** (`maestro-bootstrap`):
- Bootstrap and fallback environment
- Never modified—always clean and reliable
- Contains: build tools, Docker, git, GitHub CLI, analysis utilities
- Used when target container unavailable or broken

**2. Target Container** (project-specific, e.g., `maestro-projectname`):
- Primary development environment
- Built from project's Dockerfile
- Where coder agents normally execute
- Updated through `container_update` tool

**3. Test Container** (temporary instances):
- Throwaway containers for validation
- Run on host (not docker-in-docker)
- Test changes without affecting active environment
- Automatically cleaned up after test completes

**Container Lifecycle**:
- Orchestrator does **not** manage container switching
- Agents are self-managing via tools
- Orchestrator only handles cleanup via container registry

## Best Practices

### Performance Optimization

1. **Mirror Caching**: Update mirrors only when necessary, not on every operation
2. **File Locking**: Prevent concurrent mirror updates using `syscall.Flock`
3. **Local Cloning**: Always clone from local mirror, never from remote during workspace setup
4. **Resource Cleanup**: Delete workspaces after story completion to free disk space
5. **Container Reuse**: Architect container persists across stories, coders create new containers per story

### Security Considerations

1. **Isolation**: Each agent's workspace is completely isolated from others
2. **Read-only Architect**: Architect cannot modify code, only inspect
3. **Network Isolation**: Planning phase runs without network access
4. **Container Security**: Non-root users when possible, read-only root filesystems
5. **Workspace Permissions**: Strict validation of coder_id in read tools prevents path traversal
6. **Token Scoping**: GitHub tokens only available in read-write containers during active work

### Debugging and Monitoring

**Logs**:
- Structured logging with agent ID and story ID context
- Debug-level logs for git command execution with full output
- Error logs include command, stdout, stderr for troubleshooting

**State Persistence**:
- All state transitions logged to database
- Agent state dumps available in STATUS.md files
- Event logs in `logs/events.jsonl` with daily rotation

**Workspace Inspection**:
- Workspaces remain after story completion until cleanup
- Can manually inspect `coder-NNN/` directories for debugging
- Git history preserved in workspace `.git` directory

## Migration and Evolution

### Migration from Worktrees

Maestro previously used git worktrees but migrated to the current clone-based approach:

**Previous Issues**:
- Worktrees with `--shared --reference` failed in containers
- External dependencies on host git configuration
- Complex cleanup and state management
- Container compatibility problems

**Migration Benefits**:
- Complete container compatibility
- Simplified state management
- Better error handling and recovery
- Improved agent isolation
- Self-contained workspaces that "just work"

**Implementation Timeline**:
- Worktrees removed in Q4 2024
- Clone-based approach proven in production
- All references to worktrees eliminated from codebase

### Future Enhancements

**GitHub App Authentication** (planned):
- Replace PAT with GitHub App installation tokens
- Benefits: 1-hour TTL, repo-scoped access, separate bot identity, instant revocation
- Migration path documented in `docs/GIT.md:295-320`

**Architect Workspace** (in progress):
- Dedicated workspace at `<projectDir>/architect-001/`
- Enables architect to read committed artifacts like knowledge graph
- Updated before scoping and after each merge
- Part of knowledge graph implementation (DOC_GRAPH.md)

**Workspace Compression** (planned):
- Compress inactive workspaces to save disk space
- Decompress on-demand for debugging or story reactivation
- Reduces storage footprint for long-running projects

## Comparison: Coder vs Architect

| Aspect | Coder Agent | Architect Agent |
|--------|-------------|-----------------|
| **Workspace** | Dedicated `coder-NNN/` with full git clone | Dedicated `architect-001/` (planned) with read-only clone |
| **Container Mounts** | Own workspace: planning (RO) → coding (RW) | All coder workspaces (RO), mirrors (RO), own workspace (RO) |
| **Container Lifecycle** | One container per story, destroyed after completion | Single long-lived container for entire session |
| **Git Operations** | Clone, commit, push, branch creation, PR creation | Read-only inspection via get_diff tool |
| **File Modifications** | Full read-write access in CODING state | No write access, inspection only |
| **Network Access** | Planning: disabled, Coding: enabled | Always enabled (LLM API access) |
| **Resources** | Planning: 1 CPU, 512MB; Coding: 2 CPU, 2GB | 2 CPU, 2GB, persistent |
| **Mirror Access** | Clones from mirror during setup | Read-only mount at `/mnt/mirror` |
| **Commit Responsibility** | Creates all commits | Never commits |
| **Merge Responsibility** | Requests merge approval | Approves/rejects, does not merge |
| **Branch Management** | Creates feature branches | No branch operations |
| **Code Review** | Submits code for review | Reviews using read tools |

## Summary

Maestro's file system and git workflow architecture provides:

- ✅ **Complete Isolation**: Each agent works in a self-contained environment
- ✅ **Container Compatibility**: Self-contained git clones work perfectly in Docker
- ✅ **Network Efficiency**: Shared mirror repository minimizes bandwidth usage
- ✅ **Security**: Read-only architect access prevents accidental modifications
- ✅ **Concurrent Safety**: File locking and isolated workspaces prevent conflicts
- ✅ **Clear Responsibilities**: Coders commit, architects review, humans/CI merge
- ✅ **Flexible Execution**: Agents can manage their own container environments
- ✅ **Robust Error Handling**: Recoverable errors return to coding, unrecoverable errors fail fast
- ✅ **Comprehensive Logging**: Full audit trail of all git operations and state transitions

This architecture scales from single-agent simple tasks to complex multi-agent parallel development while maintaining code quality, security, and system reliability.

---

**Related Documentation**:
- [Git Workflow Implementation](GIT.md) - Detailed technical implementation
- [Knowledge Graph Spec](DOC_GRAPH.md) - Architect workspace and knowledge graph system
- [Project Instructions](../CLAUDE.md) - Overall project architecture
- [Container Tools](../pkg/tools/) - Container management tool implementations
