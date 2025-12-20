![Maestro](pkg/webui/web/static/img/logos/maestro_logo_small.png)

# The Maestro App Factory

Maestro is a tool that uses AI to write full applications in a disciplined way that reflects good software engineering principles.

In some ways, it's an agent orchestration tool. But unlike most others, Maestro bakes in structure, workflow, and opinions drawn from real-world experience managing large software projects. The goal is **production-ready apps, not just code snippets.**

---

## Project Status

Maestro is feature complete for its initial release but not yet production ready. The project is actively seeking bug testers and feedback. Please submit bug reports via Git Issues and thanks in advance for your help. 

---

## Why Maestro?

**Much simpler setup than other frameworks**: Maestro uses just a single binary and your existing development tools. It comes with preset config and workflow that work out of the box, but can be customized as needed.

Most frameworks require wrestling with Python versions, dependency hell, or complex setup. With Maestro:

- Download the binary (or build from source)
- Provide your API keys as environment variables
- Run Maestro and start building via the web UI

---

## What Model Does Maestro Use?

Maestro provides out-of-box support for Anthropic, Google, and OpenAI models through their official SDKs (so it should support the latest models as soon as they become available.) You can mix-and-match models by agent type - in fact, that's the recommended configuration since heterogeneous models can catch errors that models from the same provider may not. Maestro also supports open source and open weight models runnning locally through Ollama. 

---

## Key Ideas

### Agent Roles

- **PM (Product Manager)** (singleton):
  - Conducts interactive requirements interviews via web UI
  - Adapts questions based on user expertise level (non-technical, basic, expert)
  - Can read existing codebase to provide context-aware questions
  - Generates detailed specifications with YAML frontmatter and acceptance criteria
  - Iterates with architect for spec approval and refinement
  - Does *not* write stories directly - that's the architect's job

- **Architect** (singleton):
  - Breaks specs into stories
  - Reviews and approves plans
  - Enforces principles (DRY, YAGNI, abstraction levels, test coverage)
  - Maintains separate conversation contexts for each agent to preserve continuity and avoid contradictory feedback
  - Merges PRs
  - Does *not* write code directly

- **Coders** (many):
  - Pull stories from a queue
  - Develop plans, then code
  - Must check in periodically
  - Run automated tests before completing work
  - Submit PRs for architect review

Coders are goroutines that fully terminate and restart between stories. All state (stories, messages, progress, tokens, costs, etc.) is persisted in a SQLite database.

### Workflow at a Glance
1. PM conducts interactive interview and generates spec (or user provides spec file)
2. Architect reviews and approves spec (with iterative feedback if needed)
3. Architect breaks spec into stories and dispatches them
4. Coders plan, get approval, then implement
5. Architect reviews code + tests, merges PRs
6. Coders terminate, new ones spawn for new work

If a coder stalls or fails, Maestro automatically retries or reassigns. Questions can bubble up to a human via CLI or web UI.

See the canonical state diagrams for details:
- [PM state machine](pkg/pm/STATES.md) - Interactive spec generation and architect feedback
- [Architect state machine](pkg/architect/STATES.md) - Spec review, story generation, and code oversight
- [Coder state machine](pkg/coder/STATES.md) - Planning, coding, and testing workflow

---

## Tools & Environment

- **GitHub (mandatory for now):**
  - Local mirrors for speed
  - Tokens for push/PR/merge
  - One working clone per coder, deleted when the coder terminates

- **Docker:**
  - All agents run in Docker containers with security hardening
  - Containers run as non-privileged user (1000:1000) for security
  - Coders run read-only for planning, read-write for coding
  - Provides security isolation and portability

- **Makefiles:**
  - Used for build, test, lint, run
  - Either wrap your existing build tool or override targets in config
  - Aggressive lint/test defaults (“turn checks up to 11”)

- **LLMs:**
  - Supports OpenAI, Anthropic, Google Gemini, and Ollama (local models) via official SDKs
  - PM defaults: Claude Opus 4.5 (latest Anthropic flagship for nuanced requirements gathering)
  - Architect defaults: Gemini 3 Pro (1M token context window for large codebase analysis)
  - Coders default: Claude Sonnet 4.5 (latest coding-oriented model)
  - All models configurable per-project in config.json
  - Rate limiting handled internally via token buckets
  - **Ollama support**: Run local models like Llama 3.2, Qwen, Mistral for cost-free development (see [docs/OLLAMA.md](docs/OLLAMA.md))

---

## DevOps vs. App Stories

Maestro distinguishes three story types:
- **Bootstrap stories**: these perform the minimum configuration needed for Maestro to run
- **DevOps stories**: adjust Dockerfiles, build envs, CI/CD, etc.
- **App stories**: generate or modify application code

This distinction is transparent to the user—architect generates stories automatically.

---

## Operating Modes

Maestro operates in several distinct modes depending on project state and user intent:

| Mode | When It Runs | What It Does |
|------|--------------|--------------|
| Bootstrap | Automatically on new projects | Sets up basic project infrastructure |
| Development | Default operating mode | Main workflow for building features |
| Claude Code | Optional coder variant | Uses Claude Code for implementation |
| Demo | User-triggered via WebUI | Runs the application for testing |
| Hotfix | User requests urgent fix | Fast path for production issues |
| Maintenance | After N specs complete | Cleans up technical debt |
| Discovery | Future | Onboards existing codebases |

See [docs/MODES.md](docs/MODES.md) for detailed documentation on each mode.

---

## Quickstart

> **Step 1:** Install Maestro via Homebrew (macOS/Linux) or download from [releases](https://github.com/SnapdragonPartners/maestro/releases).
>
> **Option A: Homebrew (recommended)**
> ```bash
> brew tap SnapdragonPartners/tap
> brew install --cask maestro
> ```
>
> **Option B: Direct download**
> Download the binary for your platform from [releases](https://github.com/SnapdragonPartners/maestro/releases) and install it somewhere in your path.
>
> **Step 2:** Export your API keys as environment variables for the models you want to use and Github.
```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
export GOOGLE_GENAI_API_KEY=AIza...  # Optional, for Gemini models
export GITHUB_TOKEN=ghp-...

# Optional: Ollama for local models (default: http://localhost:11434)
export OLLAMA_HOST=http://localhost:11434

# Optional: Enable web search for agents (Google Custom Search)
export GOOGLE_SEARCH_API_KEY=AIza...
export GOOGLE_SEARCH_CX=...  # Your Custom Search Engine ID
```

> **Step 3:** Create a project directory  (projectdir) and switch to it.
```bash
mkdir myproject && cd myproject
```


> **Step 4:** Run Maestro
```bash
maestro
```

> **Step 5:** Open the web UI at [http://localhost:8080](http://localhost:8080) (you can change this in the config file.)
> - Work with the PM to bootstrap your project by uploading a pre-existing spec or starting a PM interview to generate a specification
> - View stories, logs, and system metrics
> - Monitor agent activity in real-time
> - Optionally chat with agents as you watch their progress

Config settings are in <projectdir>/.maestro/config.json.

---

## System Requirements

- **Binary**: ~14 MB fat binary (Linux & macOS tested; Windows soon)
- **Go**: Only needed if compiling from source (Go 1.24+)
- **Docker**: CLI + daemon required
- **GitHub**: Token with push/PR/merge perms
- **Resources**: Runs comfortably on a personal workstation

---

## Metrics & Dashboard

Maestro tracks and displays:
- PM interviews and generated specifications
- Specs, stories, and todos
- All tool use
- All chat and agent-to-agent message logs
- Token use
- Dollar cost
- Wall-clock time
- Test results and code quality metrics

---

## Knowledge Graph

Maestro includes a knowledge graph system that captures architectural patterns, design decisions, and coding conventions. This graph serves as "institutional memory" that helps agents maintain consistency across stories.

The knowledge graph is stored in `.maestro/knowledge.dot` in your repository and automatically provides relevant context to coders during planning. When a coder starts a story, Maestro extracts key terms and builds a focused "knowledge pack" with 20-30 related patterns. The architect reviews whether implementations follow these patterns and validates any updates to the graph.

Benefits:
- **Consistency**: Agents follow established patterns automatically
- **Efficiency**: Fewer review cycles explaining the same concepts
- **Evolution**: Graph grows organically as the project matures
- **Documentation**: Living documentation that stays in sync with code

See [docs/wiki/DOCS_WIKI.md](docs/wiki/DOCS_WIKI.md) for user-friendly overview or [docs/DOC_GRAPH.md](docs/DOC_GRAPH.md) for technical specification.

---

## Web Search

Agents can optionally search the web to find current documentation, API references, and library versions. This is useful when agents need information beyond their training data cutoff.

**To enable web search:**

1. Create a [Google Custom Search Engine](https://programmablesearchengine.google.com/)
2. Get an API key from [Google Cloud Console](https://console.cloud.google.com/)
3. Set the environment variables:
   ```bash
   export GOOGLE_SEARCH_API_KEY=AIza...
   export GOOGLE_SEARCH_CX=...  # Your Custom Search Engine ID
   ```

When both variables are set, web search is automatically enabled for all agents. If unset, agents will log a warning and continue without search capability.

You can also control search explicitly in `.maestro/config.json`:
```json
{
  "search": {
    "enabled": true  // or false to disable even if keys are present
  }
}
```

---

## Hotfix Mode

Hotfix mode provides a fast path for urgent, small changes that bypass the normal spec-driven development queue. This mimics the "live team / dev team" pattern common in engineering organizations, where a dedicated rotation handles production issues while the main team continues feature development.

When you need a quick fix without waiting for in-progress feature work to complete:

- **Dedicated coder**: Hotfixes route to a dedicated `hotfix-001` coder, separate from the normal queue
- **Express execution**: Simple hotfixes skip the planning phase entirely
- **PM triage**: The PM automatically detects urgent requests and routes them appropriately

Examples of hotfix requests:
- "URGENT: Fix the login button - it's broken in production"
- "Quick fix: Update the API endpoint URL"
- "Hotfix: Typo in the error message"

The architect validates hotfix requests to ensure they don't have dependencies on in-progress work, then dispatches them immediately.

See [docs/HOTFIX_MODE_SPEC.md](docs/HOTFIX_MODE_SPEC.md) for detailed specification.

---

## Maintenance Mode

Maestro includes an automated maintenance system that manages technical debt between specs. After a configurable number of specs complete, maintenance mode triggers automatically and performs:

**Programmatic Tasks** (no LLM required):
- Deletes merged branches via GitHub API
- Cleans up stale artifacts

**LLM-Driven Stories** (run as express stories):
- Knowledge graph synchronization
- Documentation link verification
- TODO/FIXME/deprecated code scanning
- Test coverage improvement suggestions

Maintenance runs autonomously and produces a summary report posted to chat. All maintenance PRs auto-merge after CI passes.

Configuration in `.maestro/config.json`:
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

See [docs/MAINTENANCE_MODE_SPEC.md](docs/MAINTENANCE_MODE_SPEC.md) for detailed specification.

---

## Claude Code Mode (Experimental)

Maestro supports an alternative coder implementation that uses [Claude Code](https://claude.ai/code) as a subprocess instead of direct LLM API calls. This mode leverages Claude Code's built-in tooling (file operations, bash execution, etc.) while Maestro handles orchestration and signal detection.

**How it works:**
- Coders run Claude Code inside Docker containers with stream-json output
- Maestro injects custom MCP tools for signaling (plan submission, task completion, questions)
- The stream parser detects tool calls in real-time and extracts results
- Q&A flow allows Claude Code to ask the architect questions and resume with answers

**Configuration:**
```json
{
  "agents": {
    "coder_mode": "claude-code"
  }
}
```

**Benefits:**
- Uses Claude Code's optimized tooling and context management
- Automatic tool approval in non-interactive mode
- Same orchestration benefits (architect review, PR workflow, persistence)

This feature is experimental and requires Claude Code to be installed in the container (auto-installed on first run).

---

## FAQ

**Q: How do I start a new project?**
Open the web UI at http://localhost:8080 and start a PM interview. The PM will ask questions about your requirements, read your existing codebase if applicable, and generate a specification. The architect will then review it and create stories for coders to implement.

**Q: Can I provide my own specification instead of using the PM?**
Yes. You can place a markdown specification file in your project directory and the architect will parse it directly, skipping the PM interview.

**Q: Do I have to use GitHub?**
Yes, for now. Maestro's workflow relies on PRs and merges.

**Q: Can I skip Docker?**
No. Coders always run in Docker containers for isolation and reproducibility.

**Q: Why doesn't the architect write code?**
By design. The architect enforces engineering discipline, ensures coders don't review their own work, and keeps technical debt low.

**Q: Is this secure?**
Maestro is intended as a single-user tool running locally. All agents run in Docker containers as a non-privileged user (1000:1000) with security hardening including read-only root filesystem, no-new-privileges, and resource limits. Combined with Docker isolation, this provides reasonable security for local development.

**Q: What happens if Maestro crashes?**
All stories, states, tool use, messages, and progress are persisted in SQLite. On restart, coders and architect resume where they left off.

---

## License

MIT
