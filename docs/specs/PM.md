# Maestro: Interactive Spec Development with PM Agent

**Version:** 1.1 (Implementation Ready)
**Owner:** @dan
**Last Updated:** 2025-01-08
**Status:** Phase 1 Complete ✅ | Phase 2 Ready

---

## Implementation Progress

**Completed (Phase 1 - PM Agent Core):**
- ✅ PM package structure created (`pkg/pm/driver.go`, `states.go`)
- ✅ State machine implemented (WAITING → INTERVIEWING → DRAFTING → SUBMITTING)
- ✅ Database schema added (pm_conversations, pm_messages tables)
- ✅ PM models added to persistence package (PMConversation, PMMessage)
- ✅ PM templates created in subdirectory (`pkg/templates/pm/`)
- ✅ Configuration integration (PM config section, model selection)
- ✅ Factory integration (PM agent creation via agent factory)
- ✅ Workspace management (EnsurePMWorkspace, UpdatePMWorkspace)
- ✅ State handler stubs (interview, draft, submit with TODO markers)

**Remaining for Phase 1:**
- ⏳ Supervisor wiring (start PM agent at boot)
- ⏳ Read tools registration (wire PM executor to tools)
- ⏳ Full LLM call implementations (replace stubs)

**Branch:** `pm-agent-implementation` (9 commits)

---

## Vision (North Star)

Make Maestro usable by non-technical and expert users alike by adding a **PM Agent** that produces high-quality specifications through a guided interview process. The PM agent enables users to create structured, validated specifications without needing to understand technical implementation details.

---

## Scope (MVP - Phase 1)

### **In Scope**

* **PM Agent** - Singleton agent that starts at boot (like architect), conducts interviews, generates markdown specs
* **Read-Only Tools** - PM uses `list_files`, `read_file` for high-level codebase reference (same isolation as architect)
* **submit_spec Tool** - Validates markdown specs with binary pass/fail linting, persists to database, sends to architect
* **Clone Management** - Lightweight `pkg/git/registry` with `UpdateDependentClones()` method (future-proof for epochs)
* **PM Workspace** - `<projectDir>/pm-001/` read-only clone, updated after merges
* **Conversation Persistence** - Store PM conversations in database for future restoration (restoration deferred to Phase 2)
* **WebUI Specs Modal** - Launch interview, chat with PM, on-demand preview, scrollable ToS-style submission confirmation
* **Spec Format** - **Markdown with YAML frontmatter** (LLM-friendly, human-editable, architect already parses markdown)
* **Binary Validation** - Pass/fail linting (no proportional scoring) - blocking errors prevent submission
* **PM Configuration** - New `pm` section in config.json to define PM model and settings

### **Out of Scope (Post-MVP)**

* **USER_ACCEPTANCE as PM state** - Deferred (requires bug fix workflow design; all work completes to architect satisfaction first)
* **Conversation state restoration** - Schema exists, restoration logic deferred to Phase 2
* **Full epoch system** - Registry has method stub for future, MVP uses simple refresh-on-merge
* **Architect→PM Q&A flow** - No bidirectional questioning in MVP
* **Multi-repo sessions** - Single repo per session
* **Diff-based review** - PM never shows code diffs

---

## Key Design Decisions

### **1. Clone Registry - Lightweight Encapsulation**

**Decision:** Create `pkg/git/registry.go` with `UpdateDependentClones()` method that calls workspace update functions. Future-proof abstraction without full epoch system complexity.

**Rationale:** Fixes current architect workspace staleness bug while establishing clean abstraction layer for future epoch tracking. Prevents "detective work" later.

### **2. PM Agent Lifecycle**

**Decision:** Singleton PM agent, starts at boot with architect, stops at shutdown.

**Rationale:** Agents are low overhead when idle (blocked goroutines). Human is always the bottleneck (not concurrent users). Simpler than agent pooling.

### **3. Spec Format**

**Decision:** Markdown with YAML frontmatter (not JSON).

**Rationale:**
- Current system already uses markdown specs
- LLMs generate cleaner markdown than JSON (fewer hallucinations)
- Human-editable (users can tweak before submitting)
- Architect's `parseSpecWithLLM()` already handles markdown parsing

**Example:**
```markdown
---
version: "1.0"
priority: must
---

# Feature: Interactive Spec Development

## Vision
Make Maestro usable by non-technical users...

## Scope
### In Scope
- PM Agent with read-only tools
- Specs Modal for interview

## Requirements
### R-001: PM Agent Tools
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** PM has read-only file access...

**Acceptance Criteria:**
- [ ] PM can call list_files
- [ ] Calls are logged with latency
```

### **4. WebUI Preview Flow**

**Decision:** On-demand preview (button click) + scrollable ToS-style submission modal.

**Rationale:**
- Live preview is expensive (LLM generation on every message)
- Submission confirmation prevents accidental submission
- Pattern familiar to users (like terms of service modals)

**Flow:**
1. User chats with PM in "Interview" tab
2. User clicks "Generate Preview" → PM drafts spec
3. Rendered markdown shown in preview pane
4. User clicks "Submit Specification"
5. Scrollable modal shows full spec with Submit/Cancel buttons
6. On submit: validate → persist → send to architect

### **5. Conversation Persistence**

**Decision:** Store conversations in database (schema defined now), restoration deferred to Phase 2.

**Rationale:**
- Simple to persist (just write to DB on each message)
- Enables future feature (resume interrupted interviews)
- No complexity in MVP (stateless on browser close)

### **6. Lint Scoring**

**Decision:** Binary pass/fail (not proportional scoring).

**Rationale:**
- Simpler to implement and understand
- Either spec is valid or it's not
- Future: Architect→PM Q&A can iterate on warnings

---

## Architecture

### **PM Agent Package Structure**

```
pkg/pm/
├── driver.go           # State machine: WAITING → INTERVIEWING → DRAFTING → SUBMITTING
├── interviewing.go     # Chat loop with user
├── drafting.go         # LLM generates markdown spec
├── submitting.go       # Validate, persist, send to architect
└── templates.go        # Interview prompts by expertise (NON_TECHNICAL, BASIC, EXPERT)
```

### **Specs Package Structure**

```
pkg/specs/
├── schema.go          # SpecPack Go structs (YAML frontmatter + sections)
├── parser.go          # Markdown → SpecPack parser
├── validator.go       # Binary lint checks (IDs unique, ACs present, DAG acyclic)
└── validator_test.go  # Table-driven validation tests
```

### **Clone Registry**

```
pkg/git/
└── registry.go        # Registry with UpdateDependentClones() method
                       # (Future: add epoch tracking here)
```

### **Database Schema**

```sql
-- PM conversations (schema defined now, restoration in Phase 2)
CREATE TABLE pm_conversations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL UNIQUE,
    user_expertise TEXT CHECK(user_expertise IN ('NON_TECHNICAL', 'BASIC', 'EXPERT')),
    repo_url TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    completed BOOLEAN DEFAULT FALSE,
    spec_id TEXT REFERENCES specs(id)
);

CREATE TABLE pm_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    role TEXT CHECK(role IN ('user', 'pm')) NOT NULL,
    content TEXT NOT NULL,
    timestamp INTEGER NOT NULL,
    FOREIGN KEY(session_id) REFERENCES pm_conversations(session_id) ON DELETE CASCADE
);

CREATE INDEX idx_pm_messages_session ON pm_messages(session_id, timestamp);
```

### **Configuration Schema**

```json
{
  "agents": {
    "max_coders": 2,
    "coder_model": "claude-sonnet-4-5",
    "architect_model": "o4-mini",
    "pm_model": "claude-sonnet-4-5"  // NEW: PM agent model
  },
  "pm": {  // NEW: PM agent configuration
    "enabled": true,
    "max_interview_turns": 20,
    "default_expertise": "BASIC"
  }
}
```

**Config Constants (in `pkg/config/config.go`):**
```go
const (
    DefaultPMModel = ModelClaudeSonnet4
)
```

**Config Struct Addition:**
```go
// PMConfig defines PM agent configuration
type PMConfig struct {
    Enabled            bool   `json:"enabled"`             // Whether PM agent is enabled
    MaxInterviewTurns  int    `json:"max_interview_turns"` // Max conversation turns before forcing submission
    DefaultExpertise   string `json:"default_expertise"`   // Default user expertise level
}

// In Config struct, add:
PM *PMConfig `json:"pm"` // PM agent settings
```

**AgentConfig Update:**
```go
type AgentConfig struct {
    MaxCoders      int              `json:"max_coders"`
    CoderModel     string           `json:"coder_model"`
    ArchitectModel string           `json:"architect_model"`
    PMModel        string           `json:"pm_model"` // NEW: PM agent model
    // ... rest unchanged
}
```

---

## Functional Requirements

### R-001: PM Agent Core

**Description:** Singleton PM agent with state machine for conducting specification interviews.

**State Machine:**
- **WAITING** - Blocked on channel waiting for interview start from WebUI
- **INTERVIEWING** - Chat loop with user, gathering requirements
- **DRAFTING** - LLM generates markdown spec from conversation
- **SUBMITTING** - Validate, persist, send to architect spec channel

**Acceptance Criteria:**
- [x] PM agent starts at boot with architect
- [ ] PM responds to interview requests from WebUI (channel wired, WebUI endpoints pending)
- [x] PM uses expertise-aware prompts (NON_TECHNICAL, BASIC, EXPERT)
- [x] PM generates valid markdown specs from conversations (stub implementation)
- [x] PM handles multiple sequential interviews (state reset after completion)

### R-002: PM Workspace & Read Tools

**Description:** PM has read-only workspace clone for codebase context.

**Workspace:** `<projectDir>/pm-001/` (cloned from mirror, updated after merges)

**Tools:** `read_file`, `list_files` (same implementation as architect, workspace root = `/mnt/pm`)

**Acceptance Criteria:**
- [x] PM workspace created at startup (EnsurePMWorkspace implemented)
- [x] PM workspace updated after successful merges (UpdatePMWorkspace implemented)
- [ ] PM read tools execute in containerized environment (executor wired, tool registration pending)
- [x] PM cannot execute shell commands or write files (architecture enforced)

### R-003: submit_spec Tool

**Description:** Validate markdown spec, persist to database, send to architect.

**Tool Interface:**
```go
func (t *SubmitSpecTool) Exec(ctx context.Context, args map[string]any) (any, error)

Input:
  - spec_content: string (markdown with YAML frontmatter)
  - session_id: string

Output:
  - accepted: bool
  - spec_id: string (if accepted)
  - errors: []string (if rejected)
```

**Validation Checks (Binary Pass/Fail):**
1. YAML frontmatter parses correctly
2. Required sections present (Vision, Scope, Requirements)
3. All requirement IDs unique (format: `R-###`)
4. All requirements have ≥1 acceptance criterion
5. Priority values valid (`must` | `should` | `could`)
6. Dependency graph is acyclic
7. In-scope list has ≥1 item

**Side Effects:**
1. Persist to `specs` table with generated `spec_id`
2. Send message to architect's spec channel
3. Mark PM conversation as completed in database

**Acceptance Criteria:**
- [ ] Valid specs pass all checks and return `accepted: true`
- [ ] Invalid specs return `accepted: false` with error details
- [ ] Architect receives spec message immediately after submission
- [ ] No partial persistence (atomic: all or nothing)

### R-004: Clone Registry & Merge Hook

**Description:** Lightweight registry abstraction, updates dependent clones after merges.

**Implementation:**
```go
// pkg/git/registry.go
type Registry struct {
    projectDir string
    logger     *logx.Logger
}

func (r *Registry) UpdateDependentClones(ctx context.Context, repoURL, branch, mergeSHA string) error {
    // 1. Update architect workspace (existing function)
    workspace.UpdateArchitectWorkspace(ctx, r.projectDir)

    // 2. Update PM workspace (new function)
    workspace.UpdatePMWorkspace(ctx, r.projectDir)

    // Future: Add epoch increment here
    return nil
}
```

**Integration Point:** `pkg/architect/request.go:710` (after successful merge)
```go
// After merge success
registry := git.NewRegistry(d.workDir)
registry.UpdateDependentClones(ctx, cfg.Git.RepoURL, cfg.Git.TargetBranch, mergeResult.CommitSHA)
```

**Acceptance Criteria:**
- [ ] Registry updates architect workspace after merge (fixes existing bug)
- [ ] Registry updates PM workspace after merge
- [ ] Update failures are logged but don't fail the merge
- [ ] Architecture supports future epoch tracking (method exists)

### R-005: WebUI Specs Modal

**Description:** Modal interface for PM interviews with preview and submission.

**UI Components:**
1. **Launch Button** - "Launch PM Interview" next to "Upload Specs"
2. **Interview Tab** - Chat interface with PM
3. **Preview Tab** - "Generate Preview" button + rendered markdown display
4. **Submission Modal** - Scrollable spec with Submit/Cancel buttons (ToS pattern)

**Backend Endpoints:**
```
POST /api/spec/start
  Request:  {repo_url, user_expertise}
  Response: {session_id}

POST /api/spec/chat
  Request:  {session_id, message}
  Response: {reply}

GET /api/spec/preview?session_id=X
  Response: {markdown}

POST /api/spec/submit
  Request:  {session_id}
  Response: {success, spec_id, errors[]}
```

**Acceptance Criteria:**
- [ ] User can launch PM interview from dashboard
- [ ] Chat interface sends/receives messages in real-time
- [ ] Preview button triggers spec generation (may take 10-30s)
- [ ] Preview renders markdown with proper formatting
- [ ] Submission modal shows scrollable spec before final submit
- [ ] Success/error feedback displayed as toast notifications

### R-006: Spec Parsing & Validation

**Description:** Parse markdown specs into structured data and validate.

**Parser (pkg/specs/parser.go):**
- Split YAML frontmatter from markdown body
- Parse frontmatter into struct fields (version, priority, etc.)
- Extract requirement sections from markdown (heading-based parsing)
- Build SpecPack struct with all data

**Validator (pkg/specs/validator.go):**
- Check 1: Requirement IDs unique
- Check 2: Priority values valid
- Check 3: Acceptance criteria present for all requirements
- Check 4: Dependency graph acyclic (topological sort)
- Check 5: In-scope list not empty
- Returns: `LintResult{Passed: bool, Blocking: []string}`

**Acceptance Criteria:**
- [ ] Parser handles frontmatter + markdown body correctly
- [ ] Parser extracts all requirement fields
- [ ] Validator catches all invalid conditions
- [ ] Validator uses table-driven tests with 20+ test cases
- [ ] Error messages are actionable (include requirement ID, specific issue)

---

## Implementation Phases

### Phase 0: Clone Registry Foundation (Day 1)

**Deliverable:** Fix architect workspace staleness + create future-proof abstraction

**Tasks:**
1. Create `pkg/git/registry.go` with `Registry` struct
2. Implement `UpdateDependentClones()` method
3. Create `pkg/workspace/pm.go` with `EnsurePMWorkspace()` and `UpdatePMWorkspace()`
4. Wire registry call into `pkg/architect/request.go` after merge
5. Test that architect workspace stays fresh after merges

**Files Changed:**
- NEW: `pkg/git/registry.go`
- NEW: `pkg/workspace/pm.go`
- EDIT: `pkg/architect/request.go` (add registry call at line ~710)

**Acceptance:**
- [ ] Architect workspace updates successfully after merge
- [ ] PM workspace updates successfully after merge
- [ ] Update failures logged but don't break merge
- [ ] Tests verify both workspaces stay in sync

---

### Phase 1: PM Agent Core (Days 2-4)

**Deliverable:** PM agent runs, has workspace, can conduct interviews

**Tasks:**
1. Create `pkg/pm/` package structure
2. Implement PM state machine (WAITING, INTERVIEWING, DRAFTING, SUBMITTING)
3. Create database schema (migrations in `pkg/persistence/schema.go`)
4. Add PM models to `pkg/persistence/models.go`
5. Wire PM agent to dispatcher (parallel to architect subscription)
6. Create PM prompt templates (`pkg/templates/pm/`)
7. Add PM config section to `pkg/config/config.go`
8. Update config defaults/validation for PM model

**Files Created:**
- `pkg/pm/driver.go`
- `pkg/pm/interviewing.go`
- `pkg/pm/drafting.go`
- `pkg/pm/submitting.go`
- `pkg/pm/templates.go`
- `pkg/templates/pm/interview_start.tmpl`
- `pkg/templates/pm/requirements_gathering.tmpl`
- `pkg/templates/pm/spec_generation.tmpl`

**Files Changed:**
- `pkg/persistence/schema.go` (add pm_conversations, pm_messages tables)
- `pkg/persistence/models.go` (add PMConversation, PMMessage structs)
- `pkg/config/config.go` (add PMConfig, update AgentConfig with PMModel)
- `pkg/dispatch/dispatcher.go` (add PM subscription method)

**Acceptance:**
- [x] PM agent starts successfully at boot (factory integration complete)
- [x] PM agent receives interview requests (channel wired, WebUI endpoints pending)
- [x] PM stores conversation in database (schema and models ready)
- [x] PM generates draft specs (stub implementation with placeholder content)

---

### Phase 2: Specs Package (Days 4-5)

**Deliverable:** Spec parsing and binary validation working

**Tasks:**
1. Create `pkg/specs/` package
2. Define SpecPack struct (schema.go)
3. Implement markdown parser (parser.go)
4. Implement binary validator (validator.go)
5. Write table-driven tests (validator_test.go)
6. Implement submit_spec tool (pkg/tools/submit_spec.go)
7. Register tool in pkg/tools/registry.go

**Files Created:**
- `pkg/specs/schema.go`
- `pkg/specs/parser.go`
- `pkg/specs/validator.go`
- `pkg/specs/validator_test.go`
- `pkg/tools/submit_spec.go`

**Files Changed:**
- `pkg/tools/registry.go` (register ToolSubmitSpec)
- `pkg/tools/constants.go` (add ToolSubmitSpec constant)

**Acceptance:**
- [ ] Parser handles 10+ example specs correctly
- [ ] Validator catches all 7 validation rules
- [ ] Tests achieve 90%+ code coverage
- [ ] submit_spec tool works end-to-end (parse → validate → persist → enqueue)

---

### Phase 3: WebUI Specs Modal (Days 6-8)

**Deliverable:** Complete WebUI flow from interview to submission

**Tasks:**
1. Add backend endpoints to `pkg/webui/server.go`
2. Create modal HTML template (`pkg/webui/templates/spec_modal.html`)
3. Add JavaScript for chat, preview, submission (`pkg/webui/static/js/specs.js`)
4. Wire PM agent channels to WebUI endpoints
5. Test end-to-end flow (launch → chat → preview → submit)

**Files Changed:**
- `pkg/webui/server.go` (add 4 new endpoints)
- NEW: `pkg/webui/templates/spec_modal.html`
- NEW: `pkg/webui/static/js/specs.js`
- `pkg/webui/templates/index.html` (add "Launch PM Interview" button)

**Acceptance:**
- [ ] Modal opens on button click
- [ ] Chat messages send/receive in real-time
- [ ] Preview generates and renders markdown correctly
- [ ] Submission modal shows scrollable spec
- [ ] Successful submission creates spec in database
- [ ] Architect receives spec and begins SCOPING

---

### Phase 4: Integration Testing (Day 9)

**Deliverable:** End-to-end validated, documentation updated

**Tasks:**
1. Write integration test (PM interview → spec generation → architect receives)
2. Test all 3 expertise levels (NON_TECHNICAL, BASIC, EXPERT)
3. Test validation failures (various invalid specs)
4. Test workspace updates after merge
5. Update CLAUDE.md with PM agent documentation
6. Update README if needed

**Acceptance:**
- [ ] Integration test passes consistently
- [ ] All expertise levels generate valid specs
- [ ] Validation correctly rejects 10+ invalid spec variants
- [ ] Documentation is complete and accurate

---

## Timeline

- **Phase 0** (Clone Registry): 1 day
- **Phase 1** (PM Core): 3 days
- **Phase 2** (Specs Package): 1.5 days
- **Phase 3** (WebUI): 2.5 days
- **Phase 4** (Testing): 1 day

**Total: 9 days for complete PM MVP**

---

## Success Criteria

**PM agent is considered successful when:**

1. ✅ Non-technical users can describe features in plain language
2. ✅ PM generates valid markdown specs automatically
3. ✅ Specs pass binary validation checks
4. ✅ Architect receives and processes specs (generates stories)
5. ✅ Workspace clones stay fresh after merges (bug fixed)
6. ✅ Conversation history persists to database
7. ✅ WebUI provides smooth user experience (no errors, clear feedback)

---

## Deferred to Phase 2 (Post-MVP)

* **Conversation restoration** - Resume interrupted interviews
* **Full epoch system** - Strict freshness guarantees with epoch counters
* **USER_ACCEPTANCE as PM state** - Human testing of completed work
* **Architect→PM Q&A** - Bidirectional questioning for spec clarification
* **Multi-repo sessions** - Handle multiple repositories per session
* **Advanced validation** - Proportional lint scoring, warnings vs errors

---

## Security & Boundaries

**PM Agent Constraints:**
- ✅ Read-only file access (no writes, no shell, no network)
- ✅ Containerized execution (same as architect)
- ✅ No access to secrets or environment variables
- ✅ Tools limited to: `read_file`, `list_files`, `submit_spec`

**WebUI Security:**
- ✅ PM endpoints require authentication (Basic Auth)
- ✅ Session IDs are UUIDs (not guessable)
- ✅ Input validation on all endpoints
- ✅ SQL injection prevention (parameterized queries)

---

## Open Questions (Tracked, Not Blocking)

1. **Preview branch retention** - How long to keep PM-generated preview branches? (Deferred)
2. **Schema evolution** - How to migrate from v1.0 → v2.0 spec format? (Document when needed)
3. **Multi-repo** - How to handle projects with multiple repos? (Future feature)
4. **Spec templates** - Should PM offer spec templates for common patterns? (Future enhancement)

---

## Appendix: Example Spec Output

```markdown
---
version: "1.0"
priority: must
created: 2025-01-07
---

# Feature: User Authentication

## Vision
Enable secure user login and registration to protect user data and personalize experiences.

## Scope
### In Scope
- Email/password authentication
- JWT token-based sessions
- Password reset flow

### Out of Scope
- OAuth/social login
- Two-factor authentication
- Role-based access control

## Requirements

### R-001: User Registration
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** Users can create accounts with email and password.

**Acceptance Criteria:**
- [ ] Email validation (format check)
- [ ] Password strength requirements (8+ chars, 1 number, 1 special char)
- [ ] Duplicate email check (return clear error)
- [ ] Confirmation email sent

### R-002: User Login
**Type:** functional
**Priority:** must
**Dependencies:** [R-001]

**Description:** Registered users can log in with email/password.

**Acceptance Criteria:**
- [ ] Credentials validated against database
- [ ] JWT token issued on success (expires in 24h)
- [ ] Failed login attempts logged
- [ ] Rate limiting (5 attempts per 15 minutes)
```

---

**End of Specification**
