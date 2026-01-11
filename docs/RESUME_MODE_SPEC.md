# Resume Mode Specification

## Overview

Resume mode allows Maestro to continue work from the most recent session after shutdown or crash. When invoked with the `-continue` flag, Maestro resumes the last session if it has incomplete stories. Gracefully-shutdown sessions restore full agent state (mid-story progress); crashed sessions reset in-flight stories and start agents fresh.

## Goals

1. **Preserve completed work** - Done stories remain done, workspace files persist
2. **Full state restoration on graceful shutdown** - Agents resume mid-story with context intact
3. **Crash recovery** - Crashed sessions can be resumed (stories reset, agents start fresh)
4. **Workspace preservation** - Filesystem changes in agent workspaces persist naturally
5. **Simple UX** - Single `-continue` flag resumes most recent session

## Non-Goals

1. Mid-tool-execution state capture (too complex, non-idempotent tools)
2. Automatic resume detection (explicit flag required)
3. Resume sessions with no incomplete work (pointless)
4. Resume older sessions (filesystem would be out of sync)

---

## User Experience

### Normal Shutdown
```bash
# User presses Ctrl+C during execution
^C
Graceful shutdown initiated...
Completing current operations...
coder-001: Finished iteration, saving state...
coder-002: Finished iteration, saving state...
architect: Finished iteration, saving state...
Persisting state to database...
Session abc123 saved. Use 'maestro -continue' to resume.
```

### Resume from Graceful Shutdown
```bash
maestro -continue -projectdir /path/to/project

╔════════════════════════════════════════════════════════════════════╗
║                    🔄 Resuming Previous Session                    ║
╠════════════════════════════════════════════════════════════════════╣
║  Session ID: abc123                                                ║
║  Status:     shutdown (graceful)                                   ║
║  Started:    2025-01-15 14:32:00                                   ║
║  Stories:    4 incomplete, 3 done                                  ║
╚════════════════════════════════════════════════════════════════════╝

Restoring agent state...
Restoring coder-001 state (CODING, todo 4/7)...
Restoring coder-002 state (TESTING)...
Restoring architect state (DISPATCHING)...
Resuming execution...
```

### Resume from Crash
```bash
maestro -continue -projectdir /path/to/project

╔════════════════════════════════════════════════════════════════════╗
║                    🔄 Resuming Previous Session                    ║
╠════════════════════════════════════════════════════════════════════╣
║  Session ID: def456                                                ║
║  Status:     crashed                                               ║
║  Started:    2025-01-14 09:15:00                                   ║
║  Stories:    7 incomplete, 2 done                                  ║
╚════════════════════════════════════════════════════════════════════╝

Resetting 2 in-progress stories to 'new'...
Loading 7 incomplete stories into queue...
Starting agents fresh...
Resuming execution...
```

### Resume Not Available
```bash
maestro -continue -projectdir /path/to/project

╔════════════════════════════════════════════════════════════════════╗
║                    ❌ No Resumable Session Found                   ║
╠════════════════════════════════════════════════════════════════════╣
║  The most recent session has no incomplete stories.                ║
║                                                                    ║
║  Start a new session with: maestro                                 ║
╚════════════════════════════════════════════════════════════════════╝
```

---

## Architecture

### Session Management

A new `sessions` table tracks session lifecycle:

```sql
CREATE TABLE sessions (
    session_id TEXT PRIMARY KEY,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    ended_at TIMESTAMP,
    status TEXT NOT NULL,           -- 'active', 'shutdown', 'completed', 'crashed'
    config_json TEXT NOT NULL       -- Full config snapshot at session start
);
```

**Session Status Values:**
- `active` - Session is currently running
- `shutdown` - Graceful shutdown completed, resumable with full state restoration
- `completed` - All work finished normally, not resumable (no incomplete stories)
- `crashed` - Process terminated unexpectedly, resumable with story reset

**Resumability:**
Only the most recent session (by `ended_at`) can be resumed. This ensures the filesystem state matches the session being restored.

| Status | Architect/PM | Coders | Stories | Use Case |
|--------|--------------|--------|---------|----------|
| `shutdown` | Full restore | Full restore (mid-story) | Continue as-is | Ctrl+C during work |
| `crashed` | Restore from checkpoint | Fresh start | Reset in-flight to 'new' | Unexpected termination |

**Why the difference?**
- **Architect/PM are session-scoped** - Their context accumulates over the entire session and is incrementally persisted after each LLM call
- **Coders are story-scoped** - Their state resets between stories anyway; losing mid-story state just means re-planning (workspace files preserved)

**Why only most recent?** If you ran session A, then session B, session A's filesystem state has been modified by session B. Resuming session A would result in mismatched state between agent context and actual files.

**Config Snapshot:**
On resume, the saved `config_json` is used instead of the current `config.json`. This ensures consistency - if the user had 3 coders running, resume uses 3 coders regardless of what config.json currently says.

### Agent Context Persistence

All agents (architect, PM, coders) persist their LLM conversation context:

```sql
CREATE TABLE agent_contexts (
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,         -- 'architect', 'pm', 'coder-001', etc.
    context_type TEXT NOT NULL,     -- 'main' or agent ID for per-agent contexts
    messages_json TEXT NOT NULL,    -- JSON array of conversation messages
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, agent_id, context_type)
);
```

**Context Types:**
- Coders have a single `main` context
- PM has a single `main` context
- Architect has multiple contexts: one `main` and one per agent it communicates with

**Checkpoint on Completion (Architect/PM):**
Architect and PM call `SerializeState()` when work is completed, not on every LLM call:

```go
// Architect: checkpoint when story marked done
func (d *Driver) markStoryDone(storyID string) {
    d.queue.MarkDone(storyID)
    d.SerializeState(ctx, db, sessionID)  // Checkpoint
}

// PM: checkpoint when spec submitted to architect
func (d *Driver) submitSpec(spec string) {
    d.sendToArchitect(spec)
    d.SerializeState(ctx, db, sessionID)  // Checkpoint
}
```

The existing `SerializeState()` methods already:
- Use upsert (ON CONFLICT DO UPDATE) - no history needed, just latest state
- Capture full agent state: state machine position, conversation contexts, escalation counts, etc.
- Write to `architect_state`/`pm_state` and `agent_contexts` tables

**Why checkpoint on completion, not every LLM call?**
On crash, we reset all in-flight stories to 'new' anyway. Any context accumulated between completions is about in-flight work that will be rewound. The only meaningful checkpoint is when work is actually done:
- Story marked done → stable boundary, context about completed work preserved
- Spec submitted → PM's work product delivered
- Graceful shutdown → full state for everyone (coders mid-story, etc.)

This is simpler (no toolloop hooks needed) and equally effective for crash recovery.

**Cleanup:**
After successfully persisting contexts for a new session, old session contexts are pruned:
```sql
DELETE FROM agent_contexts WHERE session_id != ?;
```

### Coder State Persistence

Coders have the most complex state to preserve:

```sql
CREATE TABLE coder_state (
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    story_id TEXT,                  -- Current story being worked on
    state TEXT NOT NULL,            -- State machine position: 'PLANNING', 'CODING', etc.
    plan_json TEXT,                 -- Approved implementation plan
    todo_list_json TEXT,            -- Full todo list with completion status
    current_todo_index INTEGER,     -- Which todo is in progress
    knowledge_pack_json TEXT,       -- Accumulated knowledge for this story
    pending_request_type TEXT,      -- 'QUESTION' or 'REQUEST' if awaiting response
    pending_request_json TEXT,      -- Serialized pending request details
    container_image TEXT,           -- Target container image for this story
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, agent_id)
);
```

### Architect State Persistence

```sql
CREATE TABLE architect_state (
    session_id TEXT NOT NULL PRIMARY KEY,
    state TEXT NOT NULL,            -- State machine position
    escalation_counts_json TEXT,    -- Map of story_id -> iteration count
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

The architect's story queue is reconstructed from the existing `stories` table - no additional persistence needed.

### PM State Persistence

```sql
CREATE TABLE pm_state (
    session_id TEXT NOT NULL PRIMARY KEY,
    state TEXT NOT NULL,            -- State machine position
    spec_content TEXT,              -- Current spec being developed
    bootstrap_params_json TEXT,     -- Bootstrap parameters if in progress
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

---

## Shutdown Sequence

### Signal Handling

```go
// Existing pattern - context cancellation on SIGINT/SIGTERM
ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
```

### Toolloop Shutdown Check

Each agent's toolloop checks for shutdown after completing an iteration:

```go
func (l *Loop) Run(ctx context.Context, cfg *Config) (string, T, error) {
    for iteration := 1; iteration <= cfg.MaxIterations; iteration++ {
        // Complete full iteration: LLM call + tool execution
        signal, result, done := l.runIteration(ctx, cfg)

        if done {
            return signal, result, nil
        }

        // Check for graceful shutdown AFTER completing iteration
        select {
        case <-ctx.Done():
            // Persist state before returning
            if cfg.OnShutdown != nil {
                cfg.OnShutdown(iteration, result)
            }
            return "SHUTDOWN", result, ErrGracefulShutdown
        default:
            // Continue to next iteration
        }
    }
    return "", zero, ErrMaxIterations
}
```

### Agent State Serialization

Each agent type implements a `SerializeState()` method called during shutdown:

```go
type Resumable interface {
    SerializeState(ctx context.Context) error
    RestoreState(ctx context.Context, sessionID string) error
}
```

### Persistence Queue Drain

The fire-and-forget persistence queue must be drained before shutdown completes:

```go
func (q *PersistenceQueue) DrainAndClose(ctx context.Context) error {
    // Stop accepting new requests
    close(q.requestChan)

    // Wait for worker to finish all pending writes
    select {
    case <-q.done:
        return nil
    case <-ctx.Done():
        return fmt.Errorf("timeout waiting for persistence queue drain: %w", ctx.Err())
    }
}
```

### Complete Shutdown Flow

```
SIGINT/SIGTERM received
    │
    ▼
context.Cancel() propagates to all agents
    │
    ▼
Each agent's toolloop:
    ├── Completes current iteration (LLM call + tools)
    ├── Detects ctx.Done()
    ├── Calls SerializeState() → queues to persistence
    └── Returns with "SHUTDOWN" signal
    │
    ▼
Supervisor waits for all agents (30s timeout)
    │
    ▼
persistenceQueue.DrainAndClose(30s timeout)
    │
    ▼
Direct DB write: UPDATE sessions SET status='shutdown', ended_at=NOW()
    │
    ▼
Log: "Session {id} saved. Use 'maestro -continue' to resume."
    │
    ▼
os.Exit(0)
```

---

## Resume Sequence

### CLI Flag

```go
continue := flag.Bool("continue", false, "Resume from previous gracefully-shutdown session")
```

### Resume Flow

```
maestro -continue
    │
    ▼
Query: GetMostRecentSession() - most recent non-active session
    │
    ├── No session or no incomplete stories → Error: "No resumable session"
    │
    ▼
Check session status
    │
    ├── status = 'shutdown' ──────────────────────────────┐
    │   │                                                  │
    │   ▼                                                  │
    │   Full state restoration:                            │
    │   ├── Restore ALL agent states from DB               │
    │   ├── Restore ALL conversation contexts              │
    │   ├── Stories continue as-is (no reset)              │
    │   └── All agents resume mid-work                     │
    │                                                      │
    ├── status = 'crashed' ───────────────────────────────┐
    │   │                                                  │
    │   ▼                                                  │
    │   Partial restoration + story recovery:              │
    │   ├── Restore architect/PM contexts (from checkpoint)│
    │   ├── Coders start fresh (story-scoped anyway)       │
    │   ├── Reset in-flight stories to 'new'               │
    │   └── Load stories into architect queue              │
    │                                                      │
    ▼◄─────────────────────────────────────────────────────┘
Update session: status='active'
    │
    ▼
Initialize kernel
    │
    ▼
Create agents (restored or fresh based on session status + agent type)
    │
    ▼
Force architect to DISPATCHING if crashed (stories need dispatch)
    │
    ▼
Normal execution loop
```

### Story Status Reset (Crashed Sessions Only)

For crashed sessions, stories with intermediate statuses are reset:

| Original Status | Reset To | Reason |
|-----------------|----------|--------|
| `new` | (unchanged) | Ready for dispatch |
| `pending` | (unchanged) | Has dependencies, ready when satisfied |
| `planning` | `new` | Agent was mid-planning, no saved state |
| `in_progress` | `new` | Agent was mid-coding, no saved state |
| `review` | `new` | Agent was awaiting review, no saved state |
| `done` | (unchanged) | Completed, no action needed |
| `failed` | (unchanged) | Failed, may need manual intervention |

For graceful shutdown sessions, stories are NOT reset - agents resume exactly where they left off.

### Story Restoration

On resume, the architect loads incomplete stories from the database:

```go
func (a *Architect) LoadStoriesForResume(db *sql.DB, sessionID string) error {
    // Get incomplete stories for this session
    stories, err := persistence.GetIncompleteStoriesForSession(db, sessionID)
    if err != nil {
        return err
    }

    // Add to queue (stories already reset to 'new' by resume flow)
    for _, story := range stories {
        a.queue.Add(story)
    }

    // Force state to DISPATCHING to immediately assign work
    a.ForceState(proto.StateDispatching)

    return nil
}
```

**Database Query:**
```sql
-- GetIncompleteStoriesForSession returns stories that need work
SELECT * FROM stories
WHERE session_id = ?
  AND status NOT IN ('done', 'failed', 'completed')
ORDER BY created_at ASC
```

---

## Context Manager Serialization

The `ContextManager` needs serialization support:

```go
// pkg/contextmgr/contextmgr.go

type SerializedMessage struct {
    Role       string          `json:"role"`
    Content    string          `json:"content,omitempty"`
    ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
    ToolResult *ToolResult     `json:"tool_result,omitempty"`
}

func (cm *ContextManager) Serialize() ([]byte, error) {
    messages := make([]SerializedMessage, len(cm.messages))
    for i, msg := range cm.messages {
        messages[i] = SerializedMessage{
            Role:       msg.Role,
            Content:    msg.Content,
            ToolCalls:  msg.ToolCalls,
            ToolResult: msg.ToolResult,
        }
    }
    return json.Marshal(messages)
}

func (cm *ContextManager) Deserialize(data []byte) error {
    var messages []SerializedMessage
    if err := json.Unmarshal(data, &messages); err != nil {
        return err
    }
    cm.messages = make([]Message, len(messages))
    for i, msg := range messages {
        cm.messages[i] = Message{
            Role:       msg.Role,
            Content:    msg.Content,
            ToolCalls:  msg.ToolCalls,
            ToolResult: msg.ToolResult,
        }
    }
    return nil
}
```

---

## Database Migration

Migration v13 adds resume-related tables:

```sql
-- Migration 13: Resume mode support

CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    ended_at TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'active',
    config_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_contexts (
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    context_type TEXT NOT NULL DEFAULT 'main',
    messages_json TEXT NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, agent_id, context_type)
);

CREATE TABLE IF NOT EXISTS coder_state (
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    story_id TEXT,
    state TEXT NOT NULL,
    plan_json TEXT,
    todo_list_json TEXT,
    current_todo_index INTEGER DEFAULT 0,
    knowledge_pack_json TEXT,
    pending_request_type TEXT,
    pending_request_json TEXT,
    container_image TEXT,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, agent_id)
);

CREATE TABLE IF NOT EXISTS architect_state (
    session_id TEXT NOT NULL PRIMARY KEY,
    state TEXT NOT NULL,
    escalation_counts_json TEXT,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS pm_state (
    session_id TEXT NOT NULL PRIMARY KEY,
    state TEXT NOT NULL,
    spec_content TEXT,
    bootstrap_params_json TEXT,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Index for finding resumable sessions
CREATE INDEX IF NOT EXISTS idx_sessions_status_ended ON sessions(status, ended_at DESC);
```

---

## Error Handling

### Startup Validation

On resume, validate that the saved state is consistent:

1. **Workspace directories exist** - Fail if agent workspace was deleted
2. **Story references valid** - Warn if referenced stories missing from DB
3. **Config compatibility** - Use saved config, log if different from current

### Partial Restoration Failures

If some agents fail to restore but others succeed:
- Log warnings for failed agents
- Continue with successfully restored agents
- Failed agents start fresh (new context, no pending work)

### Stale Session Detection

On normal startup (without `-continue`), detect and mark stale sessions:

```go
func markStaleSessions(db *sql.DB) error {
    // Any 'active' session from a previous run was not shut down gracefully
    _, err := db.Exec(`
        UPDATE sessions
        SET status = 'crashed', ended_at = CURRENT_TIMESTAMP
        WHERE status = 'active'
    `)
    return err
}
```

---

## Implementation Progress

| Phase | Status | Notes |
|-------|--------|-------|
| 1. Database Schema | ✅ Complete | Migration v13 with 5 tables |
| 2. Session Management | ✅ Complete | CRUD ops + ErrSessionNotFound + RowsAffected check |
| 3. Context Manager Serialization | ✅ Complete | Serialize/Deserialize with full message support |
| 4. Persistence Queue Drain | ✅ Complete | DrainPersistenceQueue() in kernel |
| 5. Agent State Serialization | ✅ Complete | SerializeState/RestoreState for all agents |
| 6. Toolloop Shutdown Integration | ✅ Complete | ErrGracefulShutdown, OutcomeGracefulShutdown, OnShutdown callback |
| 7. Graceful Shutdown Flow | ✅ Complete | WaitForAgentsShutdown(), performGracefulShutdown(), session status update |
| 8. Agent State Restoration | ✅ Complete | RestoreState() for coder, architect, PM with context manager restore |
| 9. Resume Flow Integration | ✅ Complete | -continue flag, ResumeFlow, GetMostRecentResumableSession(), SetSessionID() |
| 10. Integration Testing | ✅ Complete | 11 tests: session lifecycle, state persistence (coder/architect/PM), context serialization, cleanup |

---

## Implementation Plan

### Phase 1: Database Schema (Migration v13)
**Files:** `pkg/persistence/schema.go`, `pkg/persistence/migrations/`

1. Add `sessions` table with config snapshot
2. Add `agent_contexts` table
3. Add `coder_state` table
4. Add `architect_state` table
5. Add `pm_state` table
6. Add indexes for efficient queries

**Deliverable:** New migration that creates all tables

### Phase 2: Session Management
**Files:** `pkg/persistence/sessions.go`, `cmd/maestro/main.go`

1. Create session record on startup with config snapshot
2. Update session status on completion/shutdown
3. Mark stale sessions as 'crashed' on startup
4. Query for resumable session

**Deliverable:** Session lifecycle management

### Phase 3: Context Manager Serialization
**Files:** `pkg/contextmgr/contextmgr.go`, `pkg/contextmgr/serialize.go`

1. Add `Serialize()` method to ContextManager
2. Add `Deserialize()` method to ContextManager
3. Handle all message types (user, assistant, tool calls, tool results)
4. Unit tests for round-trip serialization

**Deliverable:** ContextManager can be serialized to/from JSON

### Phase 4: Persistence Queue Drain
**Files:** `pkg/persistence/queue.go`

1. Add `DrainAndClose(ctx)` method to persistence queue
2. Add `done` channel for completion signaling
3. Handle timeout gracefully
4. Unit tests for drain behavior

**Deliverable:** Persistence queue supports graceful drain

### Phase 5: Agent State Serialization
**Files:** `pkg/coder/resume.go`, `pkg/architect/resume.go`, `pkg/pm/resume.go`

1. Implement `Resumable` interface for each agent type
2. `SerializeState()` - queue state to persistence
3. Persistence operations for each state table
4. Unit tests for serialization

**Deliverable:** All agents can serialize their state

### Phase 6: Toolloop Shutdown Integration
**Files:** `pkg/agent/toolloop/loop.go`

1. Add `OnShutdown` callback to toolloop config
2. Check `ctx.Done()` after each iteration
3. Return `ErrGracefulShutdown` sentinel error
4. Propagate shutdown through agent drivers

**Deliverable:** Toolloops stop cleanly on context cancellation

### Phase 7: Graceful Shutdown Flow
**Files:** `internal/supervisor/supervisor.go`, `cmd/maestro/main.go`

1. Wait for all agents to return after context cancel
2. Call persistence queue drain
3. Update session status to 'shutdown'
4. Log resume instructions

**Deliverable:** Clean shutdown with state persistence

### Phase 8: Agent State Restoration
**Files:** `pkg/coder/resume.go`, `pkg/architect/resume.go`, `pkg/pm/resume.go`

1. Implement `RestoreState()` for each agent type
2. Load state from database
3. Restore context manager messages
4. Validate workspace/dependencies

**Deliverable:** All agents can restore from saved state

### Phase 9: Resume Flow Integration
**Files:** `cmd/maestro/main.go`, `cmd/maestro/flows.go`

1. Add `-continue` CLI flag
2. Query for resumable session
3. Load saved config
4. Create agents with restored state
5. Resume execution loop

**Deliverable:** `-continue` flag works end-to-end

### Phase 10: Integration Testing
**Files:** `tests/integration/resume_test.go`

1. Test: Start work → Ctrl+C → Resume → Complete
2. Test: Multiple coders at different states → Resume
3. Test: Pending question/request → Resume → Response received
4. Test: No resumable session → Error message
5. Test: Workspace deleted → Appropriate error

**Deliverable:** Comprehensive integration test coverage

---

## Testing Strategy

### Unit Tests

- Context manager serialization round-trips
- Session CRUD operations
- State serialization for each agent type
- Persistence queue drain with timeout

### Integration Tests

1. **Simple Resume**
   - Start with one story, one coder
   - Ctrl+C during CODING state
   - Resume with `-continue`
   - Verify coder continues from same todo

2. **Multi-Agent Resume**
   - 3 coders at different states (PLANNING, CODING, TESTING)
   - Architect with pending questions
   - Graceful shutdown
   - Resume and verify all positions

3. **Pending Request Resume**
   - Coder sends REQUEST to architect
   - Shutdown before response
   - Resume
   - Verify request/response flow completes

4. **Context Preservation**
   - Architect has conversation history with coder
   - Shutdown and resume
   - Verify architect remembers previous interactions

5. **Error Cases**
   - No resumable session → clean error
   - Workspace deleted → appropriate failure
   - Forced kill (SIGKILL) → next startup marks as crashed

---

## Future Considerations

### Not In Scope (Potential Future Work)

1. **Resume older sessions** - Would require workspace stashing/versioning
   - Currently only the most recent session can be resumed (filesystem must match)
   - To resume older sessions, would need to snapshot workspace per session
   - Implementation: workspace tarball or git stash on session end
2. **Config override on resume** - Allow changing some config values (e.g., number of coders)
3. **Crash state recovery** - Periodic state checkpoints for mid-crash recovery
   - Currently crashed sessions lose agent state (only stories are preserved)
   - Could implement periodic SerializeState calls during execution

### Compatibility

- Resume state schema may evolve; include version field if needed
- Old sessions become non-resumable after schema changes
- Consider migration path for in-progress sessions during upgrades
