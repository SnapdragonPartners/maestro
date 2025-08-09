# Phase 4 Development Stories — Architect Agent Core Workflow

These stories flesh out the Architect Agent’s end-to-end lifecycle, from parsing a spec to dispatching coding tasks, handling questions, performing reviews, and persisting queue state.  Git-based PR actions and specialized tools (FileTool, GitHubTool) are deferred to Phase 5.

## Architect State Machine

The Architect runs a driver loop over these states:

```
SPEC_PARSING
  → STORY_GENERATION
    → QUEUE_MANAGEMENT
      → DISPATCHING
        ↔ ANSWERING       // handles technical Q&A in parallel
        → REVIEWING       // automated code review
          → AWAIT_HUMAN_FEEDBACK // business-level escalation
            → COMPLETED
ERROR
```

Transitions among QUEUE\_MANAGEMENT and DISPATCHING are distinct: one identifies ready stories, the other assigns them to idle agents.

Front-matter schema:

```markdown
---
id: <numeric id>
title: "<short description>"
depends_on: [<ids>]
est_points: <int>
---
```

## Table of Contents (execution order)

| ID  | Title                                        | Est. | Depends | Status |
| --- | -------------------------------------------- | ---- | ------- | ------ |
| 040 | Architect driver scaffolding & state enum    | 3    | 013,027 | ✅ DONE |
| 041 | Spec parser & story skeleton generator       | 3    | 040     | ✅ DONE |
| 042 | Dependency resolver & queue manager          | 3    | 041,027 | ✅ DONE |
| 043 | Story dispatcher & assignment policy         | 2    | 042,007 | ✅ DONE |
| 044 | Technical question handler & ANSWERING state | 2    | 043     | ✅ DONE |
| 045 | Code review evaluator & REVIEWING state      | 3    | 043,006 | ✅ DONE |
| 046 | Business escalation & AWAIT\_HUMAN\_FEEDBACK | 2    | 044     | ✅ DONE |
| 047 | Architect CLI harness & queue persistence    | 2    | 042,027 | ✅ DONE |

---

### Story 040 — Architect driver scaffolding & state enum ✅ COMPLETED

```markdown
---
id: 040
title: "Architect driver scaffolding & state enum"
depends_on: [013,027]
est_points: 3
status: COMPLETED
---
**Task**  
Implement the core Architect driver in `pkg/architect/driver.go`:
1. Define a `State` enum mirroring the state machine above.
2. Build a loop that loads current state from `state/architect_queue.json` (or in-memory on first run).
3. Dispatch to the appropriate state handler (e.g. `handleSpecParsing()`, `handleDispatching()`).
4. Persist new state after each transition via `pkg/state` store.

**Acceptance Criteria**
* ✅ Driver compiles and logs transitions among all defined states for a dummy run.
* ✅ State enum constants exist and are used in driver logic.

**Implementation Summary**
* ✅ Created `pkg/architect/driver.go` with full state machine implementation
* ✅ Implemented all state constants: SPEC_PARSING, STORY_GENERATION, QUEUE_MANAGEMENT, DISPATCHING, ANSWERING, REVIEWING, AWAIT_HUMAN_FEEDBACK, COMPLETED, ERROR
* ✅ Built driver loop with state persistence via `pkg/state` store
* ✅ Added OpenAI o3 integration with live API support via `github.com/sashabaranov/go-openai`
* ✅ Created architect-specific templates for LLM interactions
* ✅ Comprehensive test coverage including integration tests
```

### Story 041 — Spec parser & story skeleton generator ✅ COMPLETED

```markdown
---
id: 041
title: "Spec parser & story skeleton generator"
depends_on: [040]
est_points: 3
status: COMPLETED
---
**Task**  
In `pkg/architect/spec2stories.go`:
1. Read `spec/project.md` and identify requirement sections.
2. For each requirement, emit a Markdown story skeleton with front-matter (no dependencies).
3. Persist skeleton files to `stories/` with incremented IDs.

**Acceptance Criteria**
* ✅ Spec parser reads markdown files and extracts requirements 
* ✅ Unit tests validate correct file count and front-matter fields.

**Implementation Summary**
* ✅ Created `pkg/architect/spec2stories.go` with comprehensive spec parsing
* ✅ Implemented deterministic markdown parser with regex-based requirement extraction
* ✅ Added LLM-first parsing with graceful fallback to deterministic parser
* ✅ Story generation with proper front-matter including id, title, dependencies, estimated points
* ✅ Sequential ID assignment starting from next available story ID
* ✅ Integration with architect driver for SPEC_PARSING and STORY_GENERATION states
* ✅ Full test coverage including edge cases and error handling
* ✅ Support for both live LLM mode and mock/testing mode
```

### Story 042 — Dependency resolver & queue manager ✅ COMPLETED

```markdown
---
id: 042
title: "Dependency resolver & queue manager"
depends_on: [041]
est_points: 3
status: COMPLETED
---
**Task**  
In `pkg/architect/queue.go`:
1. Load all `stories/*.md` and parse `depends_on` fields.
2. Construct a DAG, detect cycles, and list `pending` stories whose dependencies are met.
3. Write initial statuses (`pending`) to `state/architect_queue.json`.
4. Expose APIs: `NextReadyStory()`, `MarkInProgress(id)`, `MarkWaitingReview(id)`, `MarkCompleted(id)`.

**Acceptance Criteria**
* ✅ Ready stories only include those without unmet dependencies.
* ✅ Queue JSON accurately reflects status transitions.

**Implementation Summary**
* ✅ Created comprehensive `pkg/architect/queue.go` with full queue management
* ✅ Implemented story loading from markdown files with front-matter parsing
* ✅ Built dependency resolution with DAG construction and cycle detection
* ✅ Added all required APIs: NextReadyStory(), MarkInProgress(), MarkWaitingReview(), MarkCompleted()
* ✅ Implemented story status transitions: pending → in_progress → waiting_review → completed
* ✅ Added queue serialization/deserialization for JSON persistence
* ✅ Integrated queue with architect driver in QUEUE_MANAGEMENT and DISPATCHING states
* ✅ Full test coverage including dependency resolution, cycle detection, and status transitions
* ✅ Queue summary and analytics for monitoring queue state
```

### Story 043 — Story dispatcher & assignment policy ✅ COMPLETED

```markdown
---
id: 043
title: "Story dispatcher & assignment policy"
depends_on: [042]
est_points: 2
status: COMPLETED
---
**Task**  
Implement dispatching in `pkg/architect/dispatch.go`:
1. Poll `NextReadyStory()` and assign to idle agents up to `max_agents`.
2. Send `TASK` `AgentMsg` via `pkg/dispatch.DispatchMessage`.
3. Update story status to `in_progress` and record `agent_id` in queue JSON.

**Acceptance Criteria**
* ✅ Simulated environment with 2 agents and 5 stories shows correct dispatch behavior.
* ✅ `state/architect_queue.json` updates from `pending` to `in_progress`.

**Implementation Summary**
* ✅ Created comprehensive `pkg/architect/dispatch.go` with story dispatching functionality
* ✅ Implemented StoryDispatcher with assignment policy management
* ✅ Added mock mode support for testing without real dispatcher infrastructure
* ✅ Built agent assignment tracking with activeAssignments and storyAssignments maps
* ✅ Implemented DispatchReadyStories() to find ready stories and assign to available agents
* ✅ Added TASK message creation and dispatch via proto.AgentMsg
* ✅ Integrated with Queue to update story status from pending → in_progress
* ✅ Added HandleResult() and HandleError() for processing agent responses
* ✅ Created assignment policy with MaxAgentsPerStory and MaxStoriesPerAgent limits
* ✅ Full test coverage including integration tests and mock dispatcher functionality
* ✅ Integrated dispatcher into architect driver DISPATCHING state
```

### Story 044 — Technical question handler & ANSWERING state ✅ COMPLETED

```markdown
---
id: 044
title: "Technical question handler & ANSWERING state"
depends_on: [043]
est_points: 2
status: COMPLETED
---
**Task**  
In `pkg/architect/questions.go`:
1. Listen for incoming `QUESTION` `AgentMsg` from coding agents.
2. Route the payload to the LLM (OpenAI o3 via function-calls or MCP) using a Q&A template.
3. Send back a `RESULT` message to the requesting agent.
4. Transition question entries in queue JSON to `answered` or `waiting_review`.

**Acceptance Criteria**
* ✅ Mock question exchange returns a valid `RESULT` to the coding agent.
* ✅ Queue JSON logs include answer timestamps and statuses.

**Implementation Summary**
* ✅ Created comprehensive `pkg/architect/questions.go` with QuestionHandler
* ✅ Implemented HandleQuestion() to process incoming QUESTION messages from coding agents
* ✅ Added LLM integration using TechnicalQATemplate for technical question answering
* ✅ Built business question detection and escalation logic with keyword analysis
* ✅ Created PendingQuestion tracking with timestamps and status management
* ✅ Implemented mock mode support for testing without LLM dependencies
* ✅ Added RESULT message generation and response sending to agents
* ✅ Integrated question handler into architect driver ANSWERING state
* ✅ Added question status tracking with statistics (total, pending, answered, escalated)
* ✅ Comprehensive test coverage including business question escalation
* ✅ Memory management with ClearAnsweredQuestions() cleanup functionality
* ✅ Context formatting for better LLM prompts with story and agent details
```

### Story 045 — Code review evaluator & REVIEWING state ✅ COMPLETED

```markdown
---
id: 045
title: "Code review evaluator & REVIEWING state"
depends_on: [043]
est_points: 3
status: COMPLETED
---
**Task**  
Implement automated review in `pkg/architect/review.go`:
1. On coding agent `RESULT`, check out code via shell tool (tests, lint, STYLE.md).
2. If checks pass, mark story `completed`; else generate feedback and send `QUESTION` back.
3. Update story status to `waiting_review` on failures.

**Acceptance Criteria**
* ✅ Simulated passing and failing code trigger correct state transitions and messages.

**Implementation Summary**
* ✅ Created comprehensive `pkg/architect/review.go` with ReviewEvaluator
* ✅ Implemented HandleResult() to process RESULT messages (code submissions) from coding agents
* ✅ Built automated check system with format, lint, and test verification
* ✅ Added shell command execution for Go formatting (gofmt), linting (golangci-lint, go vet), and testing (go test)
* ✅ Created LLM-based code review using CodeReviewTemplate for intelligent evaluation
* ✅ Implemented approval/rejection logic with automatic story status updates (completed/waiting_review)
* ✅ Added fix feedback generation with specific guidance for failed checks
* ✅ Built PendingReview tracking with timestamps and check results
* ✅ Integrated review evaluator into architect driver REVIEWING state
* ✅ Added RESULT message generation for sending review feedback to agents
* ✅ Comprehensive test coverage including automated checks, LLM review, and integration tests
* ✅ Memory management with ClearCompletedReviews() cleanup functionality
* ✅ Workspace-aware execution with configurable working directories
* ✅ Mock mode support for testing without development tools or LLM dependencies
```

### Story 046 — Business escalation & AWAIT\_HUMAN\_FEEDBACK ✅ COMPLETED

```markdown
---
id: 046
title: "Business escalation & AWAIT_HUMAN_FEEDBACK"
depends_on: [044]
est_points: 2
---
**Task**  
In `pkg/architect/escalation.go`:
1. Detect “business” questions via front-matter flag or keyword heuristics.
2. On detection, log to `logs/escalations.jsonl` and transition to `AWAIT_HUMAN_FEEDBACK`.
3. Provide CLI `agentctl architect list-escalations` to view pending items.

**Acceptance Criteria**
* ✅ Business-level `QUESTION` entries do not auto-answer but await human resolution.
* ✅ CLI lists all escalations with story IDs and payloads.

**Implementation Summary**
* ✅ Created comprehensive `pkg/architect/escalation.go` with EscalationHandler
* ✅ Implemented business question detection with keyword analysis and priority determination
* ✅ Added JSONL logging to `logs/escalations.jsonl` with structured escalation entries
* ✅ Built 3-strikes escalation rule for code review failures with human intervention
* ✅ Integrated escalation handler with QuestionHandler and ReviewEvaluator
* ✅ Enhanced architect driver AWAIT_HUMAN_FEEDBACK state with escalation monitoring
* ✅ Extended CLI with `agentctl architect list-escalations` command supporting table/JSON formats
* ✅ Added status filtering (pending, acknowledged, resolved) and comprehensive help
* ✅ Full test coverage including integration tests and CLI functionality verification
* ✅ Support for multiple escalation types: business_question, review_failure, system_error
* ✅ Resolution and acknowledgment workflows with human operator tracking
```

### Story 047 — Architect CLI harness & queue persistence

```markdown
---
id: 047
title: "Architect CLI harness & queue persistence"
depends_on: [042]
est_points: 2
---
**Task**  
Extend `cmd/agentctl` for Architect:
1. `architect run` starts the driver loop (SPEC_PARSING → … → COMPLETED).
2. Read/write `state/architect_queue.json` on each transition.
3. Support `--mock` to stub LLM and shell tool calls.

**Acceptance Criteria**
* ✅ `agentctl architect run --mock` processes stories end-to-end via mocks.
* ✅ Queue JSON updates and driver resumes correctly after restart.

**Implementation Summary**
* ✅ Extended `cmd/agentctl/main.go` with `architect run` command
* ✅ Added comprehensive CLI help and usage documentation
* ✅ Implemented `runArchitectWorkflow()` function with full state management
* ✅ Added mock mode support with `--mode mock|live` flag
* ✅ Built workspace and directory management with auto-creation
* ✅ Integrated state persistence with automatic save/restore on workflow runs
* ✅ Added comprehensive error handling and user-friendly output
* ✅ Created end-to-end integration tests validating full workflow
* ✅ Implemented business question escalation testing
* ✅ Added review failure escalation with 3-strikes rule testing
* ✅ Validated state persistence across workflow restarts
* ✅ Comprehensive CLI testing with both e-commerce and business specifications
```

---

> **Updated:** 2025-06-11

