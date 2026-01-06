# Gas Town vs Maestro: Comparative Analysis

## Executive Summary

**Gas Town** and **Maestro** represent two distinct philosophies for multi-agent AI coding orchestration, both emerging from the same recognition: Claude Code (and similar CLI agents) are building blocks that need coordination at scale.

**Gas Town** (Steve Yegge, Dec 2025) is a "vibe-coded" orchestrator optimized for *throughput and chaos tolerance*. It embraces nondeterminism, uses tmux as its primary UI, and relies on the **Beads** git-backed issue tracker as its universal data plane. It's designed for Stage 7+ developers running 20-30 parallel agents who accept that "some fish fall out of the barrel."

**Maestro** is an *engineering-first* orchestrator optimized for *correctness and structured workflows*. It uses explicit state machines, typed toolloop abstractions, container isolation, and a formal message protocol. It's designed for systematic story-driven development with approval gates and human escalation paths.

**Key Insight**: Gas Town treats agents as creative workers in a factory—keep them moving, accept mistakes, prioritize velocity. Maestro treats agents as engineers in a regulated process—enforce structure, require approvals, guarantee correctness.

---

## Relative Strengths and Weaknesses

### Gas Town Strengths (Maestro Gaps)

| Feature | Gas Town | Maestro Status |
|---------|----------|----------------|
| **Massive parallelism** | 20-30 agents in parallel as core design | Limited to Architect + N Coders; no swarm model |
| **Self-propulsion (GUPP)** | Agents auto-continue via hooks without orchestrator | Architect-driven: orchestrator detects crash and redispatches |
| **Graceful degradation** | Works with partial system; no-tmux mode | Tightly coupled; full system required |
| **Ephemeral workers (Polecats)** | Spin up on demand, complete, disappear | Coders are persistent; no ephemeral swarm workers |
| **Merge Queue (Refinery)** | Dedicated agent for intelligent merge conflict resolution | No dedicated merge handling; relies on git workflows |
| **Workflow durability (NDI)** | Molecules survive crashes; resume mid-workflow | Has persistence infrastructure; resume mode intended (verify implementation) |
| **Composable workflows (MEOW)** | Formulas → Protomolecules → Molecules → Wisps | No workflow composition DSL |
| **Agent-to-predecessor communication (gt seance)** | Agents can query previous sessions | No cross-session agent communication |
| **Convoy tracking** | Work orders that survive swarms | Stories tracked but no convoy abstraction |
| **tmux integration** | Native, powerful, works remotely | No terminal UI; relies on logs/WebUI |

### Maestro Strengths (Gas Town Gaps)

| Feature | Maestro | Gas Town Status |
|---------|---------|-----------------|
| **Type-safe toolloop** | Generic Go toolloop with typed extractors | No type safety; relies on Claude interpreting Beads |
| **Container isolation** | Safe/Target/Test model; bind-mount awareness | "Rigs" but no container orchestration |
| **Formal message protocol** | QUESTION/ANSWER, REQUEST/RESULT typed flows | Mail/messaging but less structured |
| **State machine clarity** | Explicit states (PLANNING→CODING→TESTING→AWAIT_APPROVAL) | Implicit via molecules; less visibility |
| **Approval gates** | Multi-turn iterative review with architect | Reviews exist but less formal |
| **Escalation system** | Soft/hard limits; 2-hour timeout; chat escalation | Agents can escalate but less structured |
| **Per-agent context management** | Thread-safe contexts with system prompts | Agents are beads; context is session-scoped |
| **Knowledge packs** | Story-specific knowledge bundled for coders | No equivalent knowledge aggregation |
| **Secret scanning** | Chat messages scanned for secrets | Not mentioned |
| **Database audit trail** | SQLite canonical for messages; session isolation | Git-backed (Beads) for everything |
| **Integration testing** | Mock infrastructure; test strategy docs | "100% vibe coded"; no testing emphasis |
| **Documentation** | Comprehensive CLAUDE.md, specs, guides | "17 days, 75k lines"; docs are blog posts |

### Philosophical Differences

| Aspect | Gas Town | Maestro |
|--------|----------|---------|
| **Error handling** | Accept chaos; some work gets lost | Enforce correctness; retry/escalate |
| **Agent identity** | Persistent Bead; sessions are cattle | Agent + session coupled |
| **Work model** | Throughput; "shiny fish into barrels" | Quality gates; approval flow |
| **User role** | "Overseer"; Product Manager | Developer; hands-on guidance |
| **Scaling strategy** | Add more polecats; swarm | Add more coders; structured dispatch |
| **Data plane** | Git (Beads) for everything | Database for messages; git for code |
| **Target user** | "Stage 7+" developers; cost-insensitive | Engineering teams; structured processes |

---

## Priority Recommendations for Maestro Parity

### P0: Critical (Blocks Competitive Position)

1. **Self-Propulsion (GUPP-style auto-continuation)**
   - Agents check for in-flight work on startup without waiting for dispatch
   - Currently: architect must detect crash and send new TASK message
   - GUPP: agent self-heals by checking DB for assigned story and continuing
   - *Effort: Low | Impact: High*
   - **Note**: Maestro already has persistence infrastructure (`coder_state`, `agent_contexts` tables) and resume mode. The gap is *who initiates*—orchestrator vs agent.

2. **Verify Resume Mode Implementation**
   - Resume mode is intended to work but may have implementation gaps
   - Verify `coder_state` is read back on startup (not just written)
   - Verify `agent_contexts` are restored when resuming a session
   - This is a bug fix, not a new feature
   - *Effort: Low | Impact: High*

3. **Ephemeral Worker Pool (Polecats)**
   - Add swarm capability: spin up N workers for parallel stories
   - Workers complete and disappear
   - Pool management separate from Coder state machines
   - *Effort: High | Impact: High*

### P1: High Priority (Major Capability Gap)

4. **Merge Queue Agent**
   - Dedicated agent/workflow for conflict resolution
   - Intelligent rebase handling when baseline shifts during swarm
   - Escalation when merge is non-trivial
   - *Effort: Medium | Impact: High*

5. **Workflow Composition DSL**
   - Define reusable workflow templates (like Formulas/Protomolecules)
   - Compose workflows: wrap any story with review/test templates
   - Enable "Rule of Five" style multi-pass workflows
   - *Effort: Medium | Impact: Medium*

6. **Graceful Degradation**
   - System works with architect down (coder-only mode)
   - System works with chat disabled
   - Partial startup when services unavailable
   - *Effort: Low | Impact: Medium*

### P2: Important (Competitive Parity)

7. **Convoy/Work-Order Tracking**
   - Wrap related work into trackable delivery units
   - Track across multiple swarms/sessions
   - Dashboard visibility into convoy status
   - *Effort: Low | Impact: Medium*

8. **Agent Session Handoff**
   - Explicit `handoff` command for graceful context rotation
   - Work persisted before restart
   - Successor finds work automatically
   - *Effort: Medium | Impact: Medium*

9. **Terminal UI (tmux or similar)**
   - Option for power users to work in terminal
   - Cycle through agents, view status
   - Works over SSH for remote operation
   - *Effort: Medium | Impact: Low*

### P3: Nice to Have

10. **Cross-Session Agent Queries (seance)**
    - Agent can query its predecessor session
    - Useful for "where did you leave that?"
    - *Effort: Low | Impact: Low*

11. **Plugin System at Workflow Steps**
    - Hook arbitrary code into state transitions
    - Quality gates, external integrations
    - *Effort: Medium | Impact: Low*

12. **Federation / Remote Workers**
    - Scale to cloud instances
    - Multiple "towns" sharing work
    - *Effort: High | Impact: Low (for now)*

---

## Summary Matrix

| Dimension | Gas Town | Maestro | Winner |
|-----------|----------|---------|--------|
| Throughput / Scale | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | Gas Town |
| Correctness / Safety | ⭐⭐ | ⭐⭐⭐⭐⭐ | Maestro |
| Crash Recovery | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | Gas Town (slight edge) |
| Type Safety | ⭐⭐ | ⭐⭐⭐⭐⭐ | Maestro |
| Container Isolation | ⭐⭐ | ⭐⭐⭐⭐⭐ | Maestro |
| Workflow Composition | ⭐⭐⭐⭐⭐ | ⭐⭐ | Gas Town |
| Documentation | ⭐⭐ | ⭐⭐⭐⭐⭐ | Maestro |
| Ease of Setup | ⭐⭐ | ⭐⭐⭐⭐ | Maestro |
| Unattended Operation | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | Gas Town |
| Approval Workflows | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | Maestro |

**Bottom Line**: Gas Town solves the *velocity* problem—how to ship enormous amounts of work with acceptable quality. Maestro solves the *correctness* problem—how to ship high-quality work with acceptable velocity. The P0 recommendations would give Maestro Gas Town's velocity advantages while retaining its engineering rigor.

---

## P0 Recommendations: Detailed Analysis

### P0.1: Self-Propulsion (GUPP-style Auto-Continuation)

#### What It Is

Gas Town's GUPP (Gastown Universal Propulsion Principle) is elegantly simple: every agent has a persistent "hook" where work is hung. The rule is: **if there's work on your hook, you MUST run it**. This creates a self-sustaining system where agents automatically continue working after restarts, crashes, or context window exhaustion.

The key insight is **who initiates continuation**:
- **Gas Town**: Agent checks its hook on startup and self-heals
- **Maestro**: Architect must detect crash and redispatch via TASK message

#### Maestro's Current State

Maestro already has substantial persistence infrastructure:

| Component | Persisted? | Table | Restored on Startup? |
|-----------|-----------|-------|---------------------|
| Coder state machine position | Yes | `coder_state` | Intended (verify) |
| LLM conversation context | Yes | `agent_contexts` | Intended (verify) |
| Story assignment | Yes | `stories.assigned_agent` | Yes |
| Todo list + progress | Yes | `coder_state.todo_list_json` | Intended (verify) |
| Plan + knowledge pack | Yes | `coder_state.plan_json` | Intended (verify) |

**Resume mode is designed into Maestro.** If it's not working, that's an implementation bug, not a missing feature. The schema exists (`coder_state`, `agent_contexts`), the writes happen, but the read-back logic may have gaps.

#### The Actual Gap: Who Initiates?

Even with perfect resume mode, Maestro's recovery flow is:

```
Agent crashes
  → Orchestrator detects (timeout or error)
  → Orchestrator redispatches story (new TASK message)
  → Agent receives TASK, loads persisted state, resumes
```

Gas Town's GUPP flow is:

```
Agent crashes
  → New session spins up (via tmux/daemon)
  → Agent checks hook on startup (no orchestrator involved)
  → Agent finds work, resumes immediately
```

The difference: **GUPP is agent-initiated, Maestro is orchestrator-initiated.**

#### Implementation Options

**Option A: Keep Orchestrator-Driven (Current Design)**

This is simpler and may be sufficient. The architect already monitors for stuck stories and redispatches. If resume mode works correctly, agents continue from their checkpoint.

Pros:
- Centralized control; architect knows what everyone is doing
- Simpler agent logic
- Easier to reason about system state

Cons:
- Orchestrator is a bottleneck; if architect crashes, no recovery
- Latency: depends on monitoring interval to detect crash

**Option B: Add Agent Self-Healing (GUPP-style)**

Add startup check in coder driver:

```go
func (c *CoderDriver) Start(ctx context.Context) error {
    // Check for in-flight work BEFORE waiting for dispatch
    if state, err := c.loadPersistedState(); err == nil && state.StoryID != "" {
        c.log.Info("found in-flight work, resuming", "story", state.StoryID)
        return c.resumeFromState(ctx, state)
    }
    // Normal startup - wait for architect to assign work
    return c.waitForDispatch(ctx)
}
```

Pros:
- Faster recovery (no orchestrator round-trip)
- Works even if architect is down
- Closer to Gas Town's unattended operation model

Cons:
- Agents might resume stale work (story was reassigned)
- Need coordination to prevent duplicate work
- More complex agent logic

**Option C: Hybrid (Recommended)**

Keep orchestrator-driven as primary, but add agent self-healing as fallback:

1. Agent starts, checks for persisted state
2. If found, **notifies architect** "I have in-flight work for story X"
3. Architect confirms or redirects (story might have been reassigned)
4. Agent proceeds based on architect response

This preserves Maestro's centralized control while enabling faster recovery.

#### Effort Assessment (Revised)

| Task | Effort | Priority |
|------|--------|----------|
| Verify `coder_state` is read on resume | Low | P0 (bug fix) |
| Verify `agent_contexts` restored | Low | P0 (bug fix) |
| Add agent self-check on startup (Option B/C) | Medium | P1 (enhancement) |

---

### P0.2: Verify Resume Mode Implementation

#### What Needs Verification

Maestro's resume mode is **designed** to work. The persistence infrastructure exists:

```sql
-- coder_state table (schema v13+)
CREATE TABLE coder_state (
    session_id TEXT,
    agent_id TEXT,
    story_id TEXT,
    state TEXT,                    -- Current state machine position
    plan_json TEXT,                -- Serialized plan
    todo_list_json TEXT,           -- Serialized todos with completion status
    current_todo_index INTEGER,    -- Which todo we're on
    knowledge_pack_json TEXT,      -- Story-specific knowledge
    pending_request_json TEXT,     -- Pending Q&A or approval request
    container_image TEXT,
    updated_at TIMESTAMP
);

-- agent_contexts table
CREATE TABLE agent_contexts (
    session_id TEXT,
    agent_id TEXT,
    messages_json TEXT,            -- Full LLM conversation
    updated_at TIMESTAMP
);
```

#### Verification Checklist

1. **State Machine Resume**
   - [ ] On restart with `--resume`, does `CoderDriver` call `LoadCoderState()`?
   - [ ] Does it restore `state`, `plan`, `todo_list`, `current_todo_index`?
   - [ ] Does it skip already-completed todos?

2. **Context Resume**
   - [ ] On restart, is `agent_contexts` loaded into `ContextManager`?
   - [ ] Does the LLM see the previous conversation?
   - [ ] Is compaction state preserved?

3. **Story Continuity**
   - [ ] Does resumed agent know its `story_id`?
   - [ ] Does architect recognize resumed agent is working on same story?
   - [ ] Are duplicate dispatches prevented?

#### If Gaps Are Found

This is bug-fix work, not new feature development. The infrastructure exists; we just need to ensure the read path matches the write path.

Example fix pattern:
```go
func (c *CoderDriver) Start(ctx context.Context) error {
    if c.resumeMode {
        // Load persisted state
        state, err := c.persistence.LoadCoderState(ctx, c.sessionID, c.agentID)
        if err == nil && state != nil {
            c.state = state.State
            c.plan = state.Plan
            c.todos = state.Todos
            c.currentTodoIndex = state.CurrentTodoIndex
            // ... etc
        }

        // Load persisted context
        serialized, err := c.persistence.LoadAgentContext(ctx, c.sessionID, c.agentID)
        if err == nil && serialized != nil {
            c.contextManager.RestoreFromSerialized(serialized)
        }
    }
    // Continue with normal startup
}
```

---

### P0.3: Ephemeral Worker Pool (Polecats)

#### What It Is

Gas Town's Polecats are ephemeral workers that:
- Spin up on demand when work is slung to them
- Complete a single unit of work (issue, molecule step)
- Submit a Merge Request to the Refinery
- Disappear completely (session terminated, identity recycled)

This enables **swarming**: throw 10-20 polecats at an epic and let them work in parallel. The Witness oversees them, the Refinery merges their work.

Maestro's coders are **persistent**: they have long-lived identities, maintain context across stories, and are never "recycled." This limits parallelism to however many coders are configured.

#### How to Implement in Maestro

**Design Approach: "Swarm Workers with Approval Gates"**

Add a new worker type that complements (not replaces) existing Coders:

```go
// pkg/swarm/worker.go
type SwarmWorker struct {
    ID            string        // Ephemeral ID: "swarm-<story>-<n>"
    StoryID       string        // Single story assignment
    ParentCoderID string        // Which coder spawned this swarm (optional)
    State         SwarmState    // Simplified: WORKING | REVIEW_PENDING | DONE | FAILED
    Branch        string        // Isolated branch for this worker
    StartedAt     time.Time
    CompletedAt   *time.Time
    MergeRequest  *MergeRequest // Submitted to merge queue
}

type SwarmState string
const (
    SwarmWorking       SwarmState = "WORKING"
    SwarmReviewPending SwarmState = "REVIEW_PENDING"
    SwarmDone          SwarmState = "DONE"
    SwarmFailed        SwarmState = "FAILED"
)
```

**Architecture:**

```
┌─────────────────────────────────────────────────────────────┐
│                        Architect                             │
│  (unchanged: dispatches stories, reviews, answers questions) │
└─────────────────────────┬───────────────────────────────────┘
                          │
          ┌───────────────┼───────────────┐
          ▼               ▼               ▼
    ┌──────────┐    ┌──────────┐    ┌──────────┐
    │  Coder   │    │  Coder   │    │  Coder   │   (Persistent)
    │  coder-1 │    │  coder-2 │    │  coder-3 │
    └────┬─────┘    └──────────┘    └──────────┘
         │
         │ "swarm this epic"
         ▼
    ┌─────────────────────────────────────┐
    │           Swarm Controller          │
    │  (manages ephemeral worker pool)    │
    └─────────────────┬───────────────────┘
                      │
      ┌───────────────┼───────────────┬───────────────┐
      ▼               ▼               ▼               ▼
 ┌─────────┐    ┌─────────┐    ┌─────────┐    ┌─────────┐
 │ Swarm-1 │    │ Swarm-2 │    │ Swarm-3 │    │ Swarm-4 │
 │ story-a │    │ story-b │    │ story-c │    │ story-d │
 └────┬────┘    └────┬────┘    └────┬────┘    └────┬────┘
      │              │              │              │
      └──────────────┴──────────────┴──────────────┘
                           │
                           ▼
                    ┌─────────────┐
                    │ Merge Queue │
                    │  (new role) │
                    └─────────────┘
```

**Integration Points:**

1. **Swarm Trigger**: Coder or Architect can trigger swarm on epic
   ```go
   // Architect dispatches epic with swarm flag
   func (a *ArchitectDriver) dispatchEpic(ctx context.Context, epic *Epic) error {
       if epic.SwarmEnabled && len(epic.Stories) > swarmThreshold {
           return a.swarmController.SpawnSwarm(ctx, epic)
       }
       // Normal sequential dispatch
       return a.dispatchSequential(ctx, epic)
   }
   ```

2. **Simplified Swarm Workflow**: Swarm workers skip some gates
   ```
   WORKING → REVIEW_PENDING → DONE
      │
      └── (no PLANNING phase - plan provided in story)
      └── (no AWAIT_APPROVAL - goes to merge queue)
      └── (testing required before REVIEW_PENDING)
   ```

3. **Merge Queue**: New component handles parallel MRs
   - Receives MRs from swarm workers
   - Processes sequentially (like Gas Town's Refinery)
   - Handles conflicts via rebase or escalation
   - Architect reviews merged result (batch review)

4. **Worker Lifecycle**:
   ```go
   func (s *SwarmController) runWorker(ctx context.Context, w *SwarmWorker) {
       defer s.cleanupWorker(w)  // Always cleanup

       // Single-shot execution
       if err := w.Execute(ctx); err != nil {
           w.State = SwarmFailed
           s.escalate(w, err)
           return
       }

       // Submit to merge queue
       mr := w.CreateMergeRequest()
       s.mergeQueue.Submit(mr)
       w.State = SwarmReviewPending
   }
   ```

**Preserving Maestro's Design:**

- **Approval not skipped**: Merge queue output still goes to Architect for review
- **Container isolation maintained**: Each swarm worker gets its own container
- **State machine for merge queue**: MergeQueue has its own typed state machine
- **Escalation preserved**: Swarm failures escalate to chat
- **Persistent coders coexist**: Use swarm for bulk work, coders for complex/interactive stories
- **Quality gates**: Swarm workers must pass tests before submitting MR

---

## Implementation Roadmap

### Phase 1: Verify Resume Mode (P0.1 + P0.2) - Bug Fixes

**Goal**: Ensure existing persistence infrastructure actually works.

1. Audit `coder_state` read path - verify `LoadCoderState()` is called on resume
2. Audit `agent_contexts` read path - verify context is restored to `ContextManager`
3. Add integration test: kill agent mid-CODING, restart with `--resume`, verify:
   - State machine position restored
   - Completed todos not re-executed
   - LLM context includes previous conversation
4. Fix any gaps found (this is bug-fix work, infrastructure exists)

**Effort**: Low (days, not weeks)

### Phase 2: Agent Self-Healing (Optional Enhancement)

**Goal**: Enable GUPP-style agent-initiated recovery.

1. Add startup state check in `CoderDriver.Start()`
2. If in-flight work found, notify architect for confirmation
3. Architect responds: continue, redirect, or start fresh
4. Add timeout fallback if architect unavailable

**Effort**: Medium (requires coordination protocol)

### Phase 3: Swarm Workers (P0.3) - New Feature

**Goal**: Enable parallel story execution with ephemeral workers.

1. Design SwarmWorker type and simplified state machine
2. Implement SwarmController for pool management
3. Add MergeQueue component (Refinery-equivalent)
4. Integrate with Architect for batch review
5. Add swarm trigger to dispatch logic
6. Test: swarm 10 stories, verify parallel execution and merge

**Effort**: High (significant new functionality)

---

## Sources

- [Welcome to Gas Town](https://steve-yegge.medium.com/welcome-to-gas-town-4f25ee16dd04) - Steve Yegge, January 2026
- [Gas Town GitHub](https://github.com/steveyegge/gastown)
