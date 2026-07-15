+++
title = "Welcome to Maestro"
edit_date = "2026-07-15"
status = "deprecated"
summary = "User-facing orientation to running v1 Maestro."
+++

# Welcome to Maestro

## What the Heck is Maestro?

Maestro is a multi-agent AI coding orchestrator built in Go. It coordinates between an Architect agent and multiple Coder agents to process development specifications and implement code changes—systematically, with approval gates, in isolated containers.

<!-- TODO: One-paragraph elevator pitch. What problem does it solve? Who is it for? -->

If Gas Town is a chaotic factory floor where superintelligent chimpanzees sling fish into barrels at terrifying speed, Maestro is an engineering firm where every blueprint gets reviewed, every change gets tested, and nobody ships to production without sign-off.

Gas Town optimizes for **throughput**. Maestro optimizes for **correctness**.

<!-- TODO: Add your own framing here -->

---

## Who Should Use Maestro?

<!-- TODO: Define your target user. What stage are they at? What problems are they facing? -->

Maestro is designed for:

- [ ] <!-- Fill in: team size, experience level, use case -->
- [ ] <!-- Fill in: what kind of projects -->
- [ ] <!-- Fill in: what they're trying to achieve -->

---

## Who Should NOT Use Maestro?

<!-- TODO: Be honest about limitations and who this isn't for -->

Do not use Maestro if:

- [ ] <!-- Fill in: wrong fit scenarios -->
- [ ] <!-- Fill in: prerequisites they need -->
- [ ] <!-- Fill in: deal-breakers -->

---

## The Cast of Characters

Maestro has three agent roles that work together:

### The Architect (o3/Gemini)

<!-- TODO: Expand on the Architect's role, personality, responsibilities -->

The Architect is the coordinator. It:
- Parses specifications into stories
- Dispatches work to Coders
- Answers technical questions (QUESTION → ANSWER)
- Reviews code submissions (REQUEST → RESULT)
- Manages the story queue and dependencies

The Architect runs on a reasoning model (o3 or Gemini) optimized for planning and review, not raw coding speed.

### The Coders (Claude)

<!-- TODO: Expand on the Coder's role, workflow, capabilities -->

Coders are the implementers. Each Coder:
- Receives a story assignment from the Architect
- Plans the implementation
- Writes code using MCP tools in an isolated container
- Runs tests and formatting
- Requests approval when done

Coders run on Claude (Opus/Sonnet), optimized for code generation and tool use.

### The PM (Bootstrap Agent)

<!-- TODO: Expand on PM role if applicable, or remove if not user-facing -->

The PM handles project bootstrap—initial setup, spec review, configuration. It's the first agent you talk to when starting a new project.

---

## Core Concepts

### Stories: The Unit of Work

<!-- TODO: Explain what a story is, how they're structured, dependencies -->

In Maestro, all work is expressed as **stories**. A story is:
- A self-contained unit of implementation
- Has a title, description, and acceptance criteria
- May depend on other stories
- Assigned to exactly one Coder at a time

Stories flow through states: `new → pending → dispatched → planning → coding → testing → done`

### The Message Protocol

<!-- TODO: Explain the typed message system -->

Agents communicate through a typed message protocol:

| Message Type | Direction | Purpose |
|-------------|-----------|---------|
| TASK | Architect → Coder | "Here's your story assignment" |
| QUESTION | Coder → Architect | "I need clarification on X" |
| ANSWER | Architect → Coder | "Here's how to handle X" |
| REQUEST | Coder → Architect | "Please review my code" |
| RESULT | Architect → Coder | "Approved" or "Revise this..." |

### State Machines: Structured Workflows

<!-- TODO: Explain the state machine approach -->

Unlike Gas Town's free-form molecules, Maestro uses explicit state machines:

```
Coder State Machine:
SETUP → PLANNING → CODING → TESTING → AWAIT_APPROVAL → DONE
           ↑                    │
           └────────────────────┘ (test failures)
```

Each state has:
- Entry conditions
- Allowed actions (tools, messages)
- Exit conditions
- Typed results

### Containers: Isolated Execution

<!-- TODO: Explain the three-container model -->

Maestro runs code in Docker containers with a three-container model:

1. **Safe Container**: Bootstrap/fallback environment. Never modified.
2. **Target Container**: Project-specific dev environment. Where Coders normally work.
3. **Test Container**: Temporary validation. Throwaway.

This isolation means a Coder can't accidentally trash the system—worst case, we rebuild the container.

### The Toolloop: Type-Safe LLM Interaction

<!-- TODO: Explain toolloop pattern briefly -->

All agent work runs through the **toolloop**—a generic, type-safe abstraction for LLM tool-calling:

```
Loop:
  1. Send messages to LLM
  2. LLM returns tool calls
  3. Execute tools, collect results
  4. Check: are we done? (CheckTerminal)
  5. If done: extract typed result (ExtractResult)
  6. If not: add results to context, repeat
```

This ensures every agent workflow produces structured, validated output.

---

## The Philosophy

<!-- TODO: Articulate your design philosophy -->

### Correctness Over Velocity

<!-- TODO: Expand -->

Maestro assumes you'd rather ship one thing correctly than ten things broken. Every code change goes through:
- Planning review
- Implementation
- Automated testing
- Architect approval

### Approval Gates Are Features, Not Bugs

<!-- TODO: Expand -->

The QUESTION/ANSWER and REQUEST/RESULT cycles aren't overhead—they're the point. They create checkpoints where humans (or the Architect) can catch mistakes before they propagate.

### Structured > Chaotic

<!-- TODO: Expand -->

State machines, typed messages, explicit workflows. These constraints make the system predictable and debuggable. You always know what state an agent is in and what it's allowed to do next.

### Isolation Enables Trust

<!-- TODO: Expand -->

Because Coders run in containers with limited blast radius, you can let them work autonomously. The system is designed so that agent mistakes are recoverable.

---

## How It Works (The 10,000-Foot View)

<!-- TODO: Add architecture diagram reference -->

```
┌─────────────────────────────────────────────────────────────┐
│                         Human                                │
│                    (You, the Developer)                      │
└─────────────────────────┬───────────────────────────────────┘
                          │ Spec / Chat / Escalations
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                       Architect                              │
│            (Coordinator: o3/Gemini reasoning model)          │
│  • Parses specs → stories                                    │
│  • Dispatches work                                           │
│  • Answers questions                                         │
│  • Reviews code                                              │
└───────────┬─────────────┬─────────────┬─────────────────────┘
            │             │             │
     TASK   │      TASK   │      TASK   │
            ▼             ▼             ▼
      ┌──────────┐  ┌──────────┐  ┌──────────┐
      │ Coder 1  │  │ Coder 2  │  │ Coder 3  │
      │ (Claude) │  │ (Claude) │  │ (Claude) │
      └────┬─────┘  └────┬─────┘  └────┬─────┘
           │             │             │
           ▼             ▼             ▼
      ┌──────────┐  ┌──────────┐  ┌──────────┐
      │Container │  │Container │  │Container │
      │(isolated)│  │(isolated)│  │(isolated)│
      └──────────┘  └──────────┘  └──────────┘
```

---

## A Day in the Life

<!-- TODO: Describe a typical workflow -->

### 1. You Write a Spec

<!-- TODO: What does a spec look like? -->

### 2. Architect Generates Stories

<!-- TODO: How does breakdown work? -->

### 3. Coders Implement in Parallel

<!-- TODO: What happens during coding? -->

### 4. Reviews and Iteration

<!-- TODO: Describe the approval loop -->

### 5. Code Lands

<!-- TODO: What's the end state? -->

---

## Getting Started

<!-- TODO: Installation and quickstart -->

### Prerequisites

- [ ] Go 1.21+
- [ ] Docker
- [ ] API keys: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` (and/or `GOOGLE_GENAI_API_KEY`)

### Installation

```bash
# TODO: Add installation commands
```

### Your First Project

```bash
# TODO: Add quickstart commands
```

---

## Key Differences from Gas Town

<!-- TODO: Brief comparison, link to full comparison doc -->

| Aspect | Gas Town | Maestro |
|--------|----------|---------|
| Philosophy | Throughput, chaos tolerance | Correctness, structured workflows |
| Agent count | 20-30 parallel | Architect + N Coders (typically 3-5) |
| Work unit | Beads/Molecules | Stories with state machines |
| Recovery model | Agent self-heals (GUPP) | Orchestrator-driven redispatch |
| UI | tmux | WebUI + logs |
| Data plane | Git (Beads) | SQLite + Git |

For detailed comparison, see [Gas Town Comparison](./archive/GAS_TOWN_COMPARISON.md).

---

## Learn More

<!-- TODO: Add links to other docs -->

- [CLAUDE.md](../CLAUDE.md) - Detailed technical reference
- [Architecture Decision Records](./adr/) - Why we built it this way
- [Testing Strategy](./TESTING_STRATEGY.md) - How we test
- [Chat System](./MAESTRO_CHAT_SPEC.md) - Real-time collaboration

---

## FAQ

<!-- TODO: Add common questions -->

### Why Go?

<!-- TODO: Answer -->

### Why not just use Claude Code directly?

<!-- TODO: Answer -->

### How does this compare to [other tool]?

<!-- TODO: Answer -->

### Can I use this with my existing codebase?

<!-- TODO: Answer -->

---

## Acknowledgments

<!-- TODO: Credits, inspirations, contributors -->

---

*Last updated: January 2026*
