# Maestro Operating Modes

Maestro operates in several distinct modes depending on the project state and user intent. This document explains each mode, when it runs, and what it does.

## Quick Reference

| Mode | When It Runs | What It Does |
|------|--------------|--------------|
| Bootstrap | Automatically on new projects | Sets up basic project infrastructure |
| Development | Default operating mode | Main workflow for building features |
| Airplane | `--airplane` flag or config | Fully offline with local Gitea + Ollama |
| Claude Code | Optional coder variant | Uses Claude Code for implementation |
| Demo | User-triggered via WebUI | Runs the application for testing |
| **Run** | `--run` flag | **Runs app + dependencies only (no agents)** |
| Hotfix | User requests urgent fix | Fast path for production issues |
| Maintenance | After N specs complete | Cleans up technical debt |
| Discovery | Future | Onboards existing codebases |

---

## Bootstrap Mode

**When**: Automatically runs before the first development work on a new project.

**Purpose**: Ensures your project has the minimum infrastructure Maestro needs to function. This is transparent to most users—you likely won't notice it happening.

### What Bootstrap Creates

- **Dockerfile**: A development container definition with your language runtime, build tools, and dependencies
- **Makefile**: Standard targets (`build`, `test`, `lint`, `run`) that Maestro's agents use to interact with your code
- **Documentation stubs**: Basic structure for knowledge graph and project documentation
- **Configuration**: `.maestro/config.json` with sensible defaults

### How It Works

1. PM agent analyzes your initial requirements or existing codebase
2. PM generates a bootstrap specification
3. A single "bootstrap story" is created and executed
4. Coder builds and validates the development container
5. Once the container passes validation, bootstrap completes

### When Bootstrap Runs Again

Bootstrap is typically a one-time operation. However, it may run again if:
- The development container becomes invalid (missing Dockerfile, failed builds)
- You explicitly request re-bootstrapping
- Core infrastructure files are deleted

---

## Development Mode (Standard Mode)

**When**: Default mode after bootstrap completes.

**Purpose**: The main workflow where Maestro builds your application. This is where you'll spend most of your time.

### The Development Flow

```
┌─────────────┐     ┌───────────────┐     ┌─────────────┐
│     PM      │ ──▶ │   Architect   │ ──▶ │   Coders    │
│             │     │               │     │             │
│ Interviews  │     │ Reviews spec  │     │ Plan code   │
│ Generates   │     │ Creates       │     │ Implement   │
│ specs       │     │ stories       │     │ Test & PR   │
└─────────────┘     └───────────────┘     └─────────────┘
```

### Starting Development

**Option A: PM Interview**
1. Open WebUI at `http://localhost:8080`
2. Start a PM interview
3. Answer questions about your requirements
4. PM generates a specification
5. Architect reviews and creates stories

**Option B: Upload Specification**
1. Write a markdown specification file
2. Upload via WebUI or place in project directory
3. Architect parses and creates stories directly

### What Happens During Development

1. **Specification**: PM gathers requirements and generates a detailed spec with acceptance criteria
2. **Story Generation**: Architect breaks the spec into discrete, implementable stories
3. **Dispatch**: Stories are queued and assigned to available coders
4. **Planning**: Each coder creates an implementation plan, reviewed by architect
5. **Coding**: Coder implements the plan, running tests throughout
6. **Review**: Architect reviews the PR, may request changes
7. **Merge**: Approved PRs are merged to main branch

### Multiple Coders

Maestro can run multiple coders in parallel (default: 3). Each coder:
- Operates in its own Docker container
- Has its own git clone of the repository
- Works on a separate story
- Terminates completely after finishing, freeing resources

---

## Airplane Mode (Offline Development)

**When**: Started with `--airplane` flag or `"default_mode": "airplane"` in config.

**Purpose**: Enables fully offline multi-agent development without GitHub or external LLM APIs. Perfect for working on planes, in areas with poor connectivity, or when you want complete local control.

### Requirements

- **Docker**: For running the local Gitea server
- **Ollama**: For local LLM inference (must be running with models pulled)

### How It Works

Airplane mode replaces external dependencies with local alternatives:

| Component | Standard Mode | Airplane Mode |
|-----------|---------------|---------------|
| Git hosting | GitHub | Local Gitea container |
| PR/merge workflow | GitHub PRs | Gitea PRs |
| LLM provider | Anthropic/OpenAI/Google | Ollama (local) |
| Network required | Yes | No |

### Starting Airplane Mode

```bash
# Start with CLI flag
maestro --airplane

# Or configure as default in .maestro/config.json
{
  "default_mode": "airplane"
}
```

### What Happens at Startup

1. **Docker check**: Verifies Docker daemon is running
2. **Gitea startup**: Starts a local Gitea container (or reuses existing)
3. **Ollama check**: Verifies Ollama is running and models are available
4. **Mirror configuration**: Points the git mirror to Gitea instead of GitHub

### Model Configuration

Configure which Ollama models to use in airplane mode:

```json
{
  "agents": {
    "airplane": {
      "coder_model": "ollama:qwen2.5-coder:14b",
      "architect_model": "ollama:mistral-nemo:latest",
      "pm_model": "ollama:mistral-nemo:latest"
    }
  }
}
```

### Syncing Back to GitHub

When you're back online, sync your offline work to GitHub:

```bash
maestro --sync
```

This pushes all branches created in Gitea to GitHub and switches the mirror back to GitHub as upstream.

### Gitea Container

Maestro manages the Gitea container automatically:

- **Container name**: `maestro-gitea-{project-name}`
- **Data persistence**: Volume `maestro-gitea-{project-name}-data`
- **Ports**: 3000 (HTTP), 2222 (SSH) by default
- **Admin user**: `maestro` (auto-configured)

The container persists between runs. Stop it manually with:

```bash
docker stop maestro-gitea-{project-name}
```

See [AIRPLANE_MODE.md](AIRPLANE_MODE.md) for detailed specification.

---

## Claude Code Mode

**When**: Enabled via configuration. An alternative to standard coder agents.

**Purpose**: Uses Anthropic's [Claude Code](https://claude.ai/code) CLI instead of Maestro's built-in coder implementation. This leverages Claude Code's highly optimized tooling while Maestro handles orchestration.

### How It Differs

| Aspect | Standard Coder | Claude Code Mode |
|--------|----------------|------------------|
| Tool execution | Maestro's MCP tools | Claude Code's built-in tools |
| File operations | Custom file tools | Claude Code file operations |
| Context management | Maestro context manager | Claude Code's context |
| Output visibility | Tool-level results | Streaming line-by-line output |
| Orchestration | Maestro | Maestro (unchanged) |

### Configuration

```json
{
  "agents": {
    "coder_mode": "claude-code"
  }
}
```

Container resources for Claude Code mode are taken from the standard container config:

```json
{
  "container": {
    "cpus": "2",
    "memory": "2g",
    "pids_limit": 1024
  }
}
```

These values apply to both planning and coding containers when Claude Code mode is enabled. Standard mode planning containers use reduced resources (1 CPU, 512MB) since they don't run Node.js.

### How It Works

1. **Container setup**: Installer ensures Node.js, npm, and Claude Code CLI are available. Claude Code availability is verified by running `claude --version` as the coder user (UID 1000).
2. **MCP proxy installation**: An embedded `maestro-mcp-proxy` binary is copied into the container (architecture-aware: amd64/arm64). This bridges Claude Code's stdio MCP transport to Maestro's TCP-based MCP server on the host.
3. **MCP health check**: Before launching Claude Code, connectivity is verified in two phases:
   - **Host-side**: TCP dial to the MCP server, authenticate, confirm `{"authenticated": true}`
   - **Container-side**: Run `maestro-mcp-proxy --check host.docker.internal:<port>` inside the container to verify end-to-end connectivity through Docker networking
4. **Claude Code launch**: Claude Code runs as a subprocess via `docker exec` with `--dangerously-skip-permissions` (as non-root user 1000).
5. **Streaming output**: Docker exec output is streamed line-by-line via `RunStreaming()` rather than buffered until process exit. Each output line is logged and counts as activity for timeout tracking.
6. **MCP tool signaling**: Maestro injects custom MCP tools for signaling (plan submission, completion, questions). Stream parser detects tool calls in real-time.
7. **Q&A flow**: Claude Code can ask the architect questions via MCP tools, with answers routed back through the proxy.
8. **Inactivity detection**: A `TimeoutManager` monitors output activity. If no output is received within the inactivity threshold, the docker exec process is actively cancelled via context cancellation (not just flagged post-hoc).

### Timeout and Stall Detection

Claude Code mode uses active timeout management to prevent silent stalls:

- **Total timeout**: Maximum wall-clock time for the entire Claude Code session
- **Inactivity timeout**: Maximum time with no stdout/stderr output before the process is killed
- **Streaming activity**: Every line of output (stdout and stderr) resets the inactivity timer
- **Active cancellation**: When inactivity is detected, the underlying `docker exec` context is cancelled, terminating the process immediately rather than waiting for the total timeout

If the executor supports the `StreamingExecutor` interface, output is streamed line-by-line. If not, a warning is logged and the system falls back to buffered execution (inactivity detection is less reliable in this mode).

### When to Use It

- When you want Claude Code's optimized context management
- For projects where Claude Code's tooling works better
- Experimental—useful for comparing approaches

---

## Demo Mode

**When**: User-triggered via WebUI after bootstrap completes.

**Purpose**: Runs your application so you can see and interact with it. This isn't a distinct development flow—it's a tool within the PM agent for User Acceptance Testing (UAT).

### What Demo Does

- Builds your application inside the development container
- Starts the application with proper port mapping
- Provides a URL to access your running app
- Shows application logs in real-time
- Detects when code changes make the demo outdated

### Port Detection

Demo mode automatically detects which port your application listens on:

1. Starts the container and runs your `make build && make run`
2. Polls `/proc/net/tcp` inside the container to find listening ports
3. Selects the "main" port using priority order (common ports like 8080, 3000, etc.)
4. Maps the container port to a host port (Docker-assigned)
5. Verifies connectivity with a TCP probe

This means you don't need to configure ports manually—Maestro figures it out.

### Demo Controls

| Button | What It Does |
|--------|--------------|
| Start | Builds and runs the demo |
| Stop | Stops the demo container |
| Restart | Quick restart (no rebuild) |
| Rebuild | Full rebuild from scratch |

### Common Issues

If demo fails to start, check:
- **Loopback binding**: App must bind to `0.0.0.0`, not `127.0.0.1`
- **No listeners**: App isn't starting a server
- **Container crash**: Check logs for startup errors

See [DEMO_MODE_SPEC.md](DEMO_MODE_SPEC.md) for detailed specification.

---

## Run Mode

**When**: Started with `--run` flag.

**Purpose**: Runs your application with all its dependencies without starting the full orchestrator (no PM, architect, or coders). This is useful for developers who want to use Maestro as a "better docker compose" for local development.

### What Run Mode Does

1. Starts compose services (databases, caches, etc.) if a compose file exists
2. Builds and runs your application in the development container
3. Connects the app to the compose network so it can reach dependencies
4. Performs port detection and maps the app to a host port
5. Displays the URL and streams logs
6. Cleans up everything (containers, networks, volumes) on shutdown

### Usage

```bash
# Run the application
maestro --run

# Run with project directory specified
maestro --run --projectdir /path/to/project
```

### How It Differs from Demo Mode

| Aspect | Run Mode | Demo Mode |
|--------|----------|-----------|
| Trigger | CLI flag (`--run`) | WebUI button |
| Orchestrator | Not started | Full orchestrator running |
| Agents | None | PM, Architect, Coders available |
| WebUI | Not available | Available at `:8080` |
| Use case | Local development | UAT during development |

### When to Use Run Mode

- **Local development**: You want to run your app locally without the full orchestrator
- **CI/CD testing**: Run integration tests against the app with dependencies
- **Quick iteration**: Test changes without waiting for agent bootstrapping
- **Resource efficiency**: Lighter footprint when you don't need agents

### Requirements

- Bootstrap must have completed previously (Dockerfile, Makefile exist)
- Docker must be running
- If using compose: `.maestro/compose.yml` must exist

### Shutdown Behavior

Run mode handles cleanup gracefully:
- **Ctrl+C**: Stops app container, runs `compose down -v`, removes network
- **SIGTERM**: Same cleanup behavior
- **Crash**: Orphaned containers are cleaned up on next run

---

## Hotfix Mode

**When**: User submits an urgent request via PM.

**Purpose**: Fast path for critical fixes that can't wait for the normal development queue. Mimics the "live team / dev team" pattern in engineering organizations.

### How It Works

1. User submits request with urgency indicators ("URGENT", "hotfix", "broken in production")
2. PM detects urgency and routes to hotfix flow
3. Architect validates the request doesn't depend on in-progress work
4. Dedicated `hotfix-001` coder executes immediately
5. Simple fixes skip the planning phase entirely

### Examples of Hotfix Requests

```
"URGENT: Fix the login button - it's broken in production"
"Quick fix: Update the API endpoint URL"
"Hotfix: Typo in the error message"
```

### When NOT to Use Hotfix

- Features that require significant planning
- Changes that depend on work in progress
- Non-urgent improvements or refactoring

See [HOTFIX_MODE_SPEC.md](HOTFIX_MODE_SPEC.md) for detailed specification.

---

## Maintenance Mode

**When**: Automatically after a configurable number of specs complete.

**Purpose**: Manages technical debt through automated cleanup tasks. Runs between specs to keep the codebase healthy.

### Programmatic Tasks (No LLM)

- Delete merged branches via GitHub API
- Clean up stale artifacts
- Prune old containers and images

### LLM-Driven Stories

| Task | What It Does |
|------|--------------|
| Knowledge sync | Updates knowledge graph with recent patterns |
| Doc verify | Checks documentation links aren't broken |
| TODO scan | Finds TODO/FIXME/deprecated code |
| Test coverage | Suggests areas needing more tests |

### Configuration

```json
{
  "maintenance": {
    "enabled": true,
    "after_specs": 1,
    "stories": {
      "knowledge_sync": true,
      "doc_verify": true,
      "todo_scan": true
    }
  }
}
```

### Output

Maintenance produces a summary report posted to chat. All maintenance PRs auto-merge after CI passes.

See [MAINTENANCE_MODE_SPEC.md](MAINTENANCE_MODE_SPEC.md) for detailed specification.

---

## Discovery Mode (Future)

**When**: Planned for projects with existing codebases.

**Purpose**: Onboards pre-existing projects more efficiently than bootstrap alone. Think of it as "Bootstrap+" for established codebases.

### Planned Capabilities

- **Documentation graph**: Analyzes existing code to build initial knowledge graph
- **Architecture mapping**: Identifies patterns, dependencies, and structure
- **Secret detection**: Finds required API keys, credentials, and environment variables
- **Build system analysis**: Understands existing build tooling
- **Test inventory**: Catalogs existing tests and coverage

### When You'd Use It

- Taking over maintenance of an existing project
- Onboarding a codebase that wasn't built with Maestro
- Analyzing a project before major refactoring

### Current Status

Discovery mode is not yet implemented. For now, Maestro can work with existing codebases through standard bootstrap, but the onboarding is less sophisticated.

---

## Mode Interactions

Modes are not mutually exclusive. Here's how they interact:

```
New Project (Standard):
  Bootstrap → Development → [Maintenance cycles] → ...
                    ↓
               Demo (anytime)
                    ↓
               Hotfix (urgent)

New Project (Airplane):
  Bootstrap → Development (offline) → --sync → GitHub
                    ↓
               Demo (anytime)
                    ↓
               Hotfix (urgent)

Run Mode (standalone):
  Bootstrap → --run (app + deps only, no agents)

Existing Project (future):
  Discovery → Development → [Maintenance cycles] → ...
```

### Run Mode

Run mode is standalone—it doesn't start the orchestrator or any agents. It's useful for:
- Quick local testing during development
- CI/CD pipelines that need the app running
- Developers who want Maestro's compose + container management without the AI agents

Run mode requires that bootstrap has completed previously.

### Airplane + Standard

You can switch between airplane and standard mode:

- **Going offline**: Start with `--airplane` to use local Gitea + Ollama
- **Coming online**: Run `--sync` to push changes to GitHub, then restart without `--airplane`
- **Work continuity**: All commits, branches, and PRs created offline sync to GitHub

### Demo + Development

Demo mode can run while development continues. Changes to the codebase will show the demo as "outdated" in the WebUI, prompting you to restart or rebuild.

### Hotfix + Development

Hotfixes run on a separate coder (`hotfix-001`) and don't block the main development queue. However, the architect ensures hotfixes don't conflict with in-progress work.

### Maintenance + Development

Maintenance runs between specs, not during active development. It won't interrupt coders mid-story.
