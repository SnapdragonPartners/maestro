# SQLite Persistence System Implementation Plan

## Overview
Migrate from file-based story/spec storage to SQLite database for better concurrency, queryability, and state tracking.

## Architecture Decisions

### Database Ownership
- **Orchestrator owns all database operations** (reads and writes)
- Single database worker goroutine prevents race conditions
- All agents communicate via channels - no direct SQLite access
- Clean separation: agents focus on business logic, orchestrator handles persistence

### Error Handling Strategy
- **Fire-and-forget writes** for simplicity and non-blocking behavior
- Database ping at startup to verify connectivity
- **Fatal shutdown on SQLite errors** - persistence failures are unrecoverable
- Aggressive error logging and comments explaining the trade-offs

### ID Strategy
- **Specs**: Full UUIDs for global uniqueness
- **Stories**: 8-character hex IDs (like git commits) for brevity and git consistency
- Story IDs become canonical identifiers through entire system lifecycle

## Database Schema

```sql
-- Enable foreign keys and WAL mode for better concurrency
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

-- Specifications table (inbound spec files)
CREATE TABLE specs (
    id TEXT PRIMARY KEY,                -- Full UUID
    content TEXT NOT NULL,              -- Original spec content
    created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    processed_at DATETIME               -- When converted to stories
);

-- Stories table (work units generated from specs)
CREATE TABLE stories (
    id TEXT PRIMARY KEY,                -- 8-char hex ID (like git commits)
    spec_id TEXT REFERENCES specs(id),  -- Parent specification
    title TEXT NOT NULL,                -- User-friendly name (former filename)
    content TEXT NOT NULL,              -- Story markdown content
    status TEXT DEFAULT 'new' CHECK (status IN ('new','planning','coding','committed','merged','error','duplicate')),
    priority INTEGER DEFAULT 0,         -- Story priority
    approved_plan TEXT,                 -- Approved implementation plan
    created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    started_at DATETIME,                -- When work began
    completed_at DATETIME,              -- When story finished
    assigned_agent TEXT,                -- Agent working on story
    tokens_used BIGINT DEFAULT 0,       -- Total tokens consumed (future metrics)
    cost_usd DECIMAL(10,4) DEFAULT 0.0, -- Total cost in USD (future metrics)
    metadata TEXT                       -- JSON for extensibility
);

-- Story dependencies (junction table for queryability)
CREATE TABLE story_dependencies (
    story_id TEXT REFERENCES stories(id) ON DELETE CASCADE,
    depends_on TEXT REFERENCES stories(id) ON DELETE CASCADE,
    PRIMARY KEY (story_id, depends_on),
    CHECK (story_id <> depends_on)      -- Prevent self-dependencies
);

-- Performance indices
CREATE INDEX idx_stories_status ON stories(status);
CREATE INDEX idx_stories_agent ON stories(assigned_agent);
CREATE INDEX idx_depends_on ON story_dependencies(depends_on);
```

## Channel Interface

```go
// PersistenceRequest represents a database operation request
type PersistenceRequest struct {
    Operation string      // Operation type: "upsert_spec", "upsert_story", "query_stories", etc.
    Data      interface{} // Operation-specific data payload
    Response  chan<- interface{} // Response channel for queries (nil for fire-and-forget writes)
}
```

### Supported Operations
- **Writes (fire-and-forget)**:
  - `upsert_spec` - Insert/update specification
  - `upsert_story` - Insert/update story
  - `update_story_status` - Change story status
  - `add_story_dependency` - Add dependency relationship

- **Queries (with response)**:
  - `query_stories_by_status` - Get stories by status
  - `query_pending_stories` - Get stories ready for work
  - `get_story_dependencies` - Get dependency graph
  - `get_spec_totals` - Aggregate metrics for spec

## Implementation Tasks

### Phase 1: Database Foundation
1. **Create SQLite utilities** (`pkg/persistence/`)
   - Database initialization and schema creation
   - Migration support for future schema changes
   - Connection management with WAL mode

2. **Add persistence channel to orchestrator**
   - Database worker goroutine
   - Channel-based request routing
   - Startup database ping for health check

### Phase 2: Schema and Operations
3. **Implement core database operations**
   - CRUD operations for specs/stories/dependencies
   - Query helpers for common operations
   - Error handling with fatal shutdown

4. **ID generation utilities**
   - UUID generation for specs
   - 8-character hex generation for stories
   - Collision detection and retry logic

### Phase 3: Agent Integration
5. **Update architect agent**
   - Replace file operations with channel requests
   - Modify story creation to use database
   - Update queue management to query database

6. **Update story lifecycle**
   - Status transitions via database
   - Dependency tracking
   - Plan approval persistence

## Benefits

### Immediate
- **Concurrency safety**: No more file race conditions
- **Queryable data**: SQL queries for story status, dependencies
- **Atomic operations**: ACID compliance for state changes
- **Clean architecture**: Separation of persistence and business logic

### Future
- **Metrics tracking**: Token usage and cost analysis
- **Dependency analysis**: Detect cycles, find blocked stories
- **Performance insights**: Story completion times, agent workload
- **Resume capability**: Perfect restart from database state

## File Location
- Database: `.maestro/maestro.db`
- Implementation: `pkg/persistence/`

## Migration Notes
- **No backward compatibility needed** (pre-release)
- Existing file-based stories will be manually imported if needed
- Database file is portable and can be backed up easily

## Risk Mitigation
- WAL mode enables concurrent reads during writes
- Foreign key constraints prevent data corruption
- Check constraints enforce valid status values
- Startup ping ensures database connectivity
- Fatal shutdown on persistence errors prevents data loss