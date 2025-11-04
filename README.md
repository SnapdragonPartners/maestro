![Maestro](pkg/webui/web/static/img/logos/maestro_logo_small.png)

# Maestro

Maestro is a tool that uses AI to write full applications in a disciplined way that reflects good software engineering principles.  

In some ways, it's an agent orchestration tool. But unlike most others, Maestro bakes in structure, workflow, and opinions drawn from real-world experience managing large software projects. The goal is **production-ready apps, not just code snippets.**

---

## Why Maestro?

**Much simpler setup than other frameworks**: Maestro uses just a single binary and your existing development tools. It comes with preset config and workflow that work out of the box, but can be customized as needed.  

Most frameworks require wrestling with Python versions, dependency hell, or complex setup. With Maestro:  

- Download the binary (or build from source)  
- Provide your API keys as environment variables  
- Run the bootstrap workflow  
- Start building  

---

## Key Ideas

### Architect vs. Coders
- **Architect** (singleton):  
  - Breaks specs into stories  
  - Reviews and approves plans  
  - Enforces principles (DRY, YAGNI, abstraction levels, test coverage)  
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
1. User provides a complex spec  
2. Architect breaks it into stories and dispatches them  
3. Coders plan, get approval, then implement  
4. Architect reviews code + tests, merges PRs  
5. Coders terminate, new ones spawn for new work  

If a coder stalls or fails, Maestro automatically retries or reassigns. Questions can bubble up to a human via CLI or web UI.

See the canonical state diagrams for details:  
- [Architect state machine](pkg/architect/STATES.md)  
- [Coder state machine](pkg/coder/STATES.md)

---

## Tools & Environment

- **GitHub (mandatory for now):**  
  - Local mirrors for speed  
  - Tokens for push/PR/merge  
  - One working clone per coder, deleted when the coder terminates  

- **Docker:**  
  - Coders run in read-only containers for planning, read-write for coding  
  - Currently run as root for simplicity (rootless support under consideration)  
  - Provides security isolation and portability  

- **Makefiles:**  
  - Used for build, test, lint, run  
  - Either wrap your existing build tool or override targets in config  
  - Aggressive lint/test defaults (“turn checks up to 11”)  

- **LLMs:**  
  - Supports OpenAI & Anthropic models via official Go SDKs  
  - Architect defaults: reasoning-oriented models  
  - Coders default: coding-oriented models  
  - Rate limiting handled internally via token buckets  
  - Local model support is on the roadmap  

---

## DevOps vs. App Stories

Maestro distinguishes two story types:  
- **DevOps stories**: adjust Dockerfiles, build envs, CI/CD, etc.  
- **App stories**: generate or modify application code  

This distinction is transparent to the user—architect generates stories automatically.

---

## Quickstart from scratch

> **Step 1:** Download binary and place it on your path
> **Step 2:** Create a working directory and run Maestro:
```bash
mkdir myproject
cd myproject 
maestro
```

## Quickstart with preconfiguration

> **Step 1:** Download binary (or build from source).  
> **Step 2:** Export your API keys as environment variables.  
```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
export GITHUB_TOKEN=ghp-...
```

> **Step 3:** Run Maestro in bootstrap mode.  
```bash
mkdir myapp
maestro -projectdir myapp -bootstrap -git-repo https://github.com/SnapdragonPartners/maestro-demo.git
```

> **Step 4:** Run Maestro in development mode.  
```bash
maestro -projectdir myapp -git-repo https://github.com/SnapdragonPartners/maestro-demo.git
```

> **Step 5 (optional):** Open the web UI at [http://localhost:8080](http://localhost:8080).  

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
- Specs, stories, and todos
- All tool use
- All chat and agent-to-agent message logs  
- Token use  
- Dollar cost  
- Wall-clock time  
- Test results and code quality metrics  

---

## FAQ

**Q: Do I have to use GitHub?**  
Yes, for now. Maestro’s workflow relies on PRs and merges.  

**Q: Can I skip Docker?**  
No. Coders always run in Docker containers for isolation and reproducibility.  

**Q: Why doesn’t the architect write code?**  
By design. The architect enforces engineering discipline, ensures coders don’t review their own work, and keeps technical debt low.  

**Q: Is this secure?**  
Maestro is intended as a single-user tool running locally. It is at least as secure as other common LLM-based coding agents (probably more so due to Docker isolation), but since code is already exchanged with third-party LLMs, the trade-off of running root containers is considered acceptable.

**Q: What happens if Maestro crashes?**  
All stories, states, tool use, messages, and progress are persisted in SQLite. On restart, coders and architect resume where they left off.  

---

## License

MIT  
