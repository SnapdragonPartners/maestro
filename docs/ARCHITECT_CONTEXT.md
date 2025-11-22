# Architect Multi-Context Support

## Problem Statement

The architect agent currently loses context between requests to the same coder agent, leading to contradictory feedback. Each request creates a new template name that triggers a full context reset, causing the architect to forget previous interactions within the same story.

Example: The architect may request changes to a plan, then when the coder resubmits with those exact changes, the architect contradicts itself because it has no memory of the previous review.

## Solution Overview

Implement a multi-context architecture where the architect maintains separate conversation contexts for each agent it communicates with. This preserves conversation continuity within story boundaries while enabling clean resets when agents move to new stories.

## Key Design Decisions

### 1. Context Key: Agent ID
- Use agent ID (not story ID) as the primary key for context management
- Enables uniform approach for all agent types (PM doesn't have stories)
- Natural mapping since architect communicates with specific agents

### 2. Context Lifecycle
- Reset context when a coder enters SETUP state (new story assignment)
- Preserve context throughout all interactions within a story
- PM context persists across spec interactions (no story boundaries)

### 3. Knowledge Persistence via Express Channel
- Create a bypass channel for knowledge file updates
- Allows coders to handle file operations without planning phase
- Reusable for future hot fixes and small updates

### 4. Template Organization
- Create `pkg/templates/architect/` directory for architect-specific templates
- Follows pattern established by `pkg/templates/pm/`
- Cleaner separation of concerns

## Implementation Architecture

### Phase 1: Multi-Context Management

#### 1.1 Architect Driver Modifications

```go
// pkg/architect/driver.go
type Driver struct {
    *agent.BaseStateMachine
    // REMOVE after migration: contextManager *contextmgr.ContextManager

    // NEW: Multi-context support
    agentContexts   map[string]*contextmgr.ContextManager  // Key: agent_id
    contextMutex    sync.RWMutex                          // Protect map access

    // NEW: Knowledge accumulation
    knowledgeBuffer []KnowledgeEntry
    knowledgeMutex  sync.Mutex

    // ... existing fields ...
}
```

#### 1.2 Context Management Methods

- `getContextForAgent(agentID string)` - Get or create context for an agent
- `ResetAgentContext(agentID string)` - Reset context when agent starts new story
- `buildSystemPrompt(agentID, storyID string)` - Create comprehensive system prompt

#### 1.3 Request Processing Updates

All request handlers must be updated to use agent-specific contexts:
- `handleRequest()` - Use sender's agent ID to get context
- `handleSingleTurnReview()` - Use agent context, not new template
- `handleIterativeQuestion()` - Use agent context for continuity
- `handleIterativeApproval()` - Use agent context for code reviews

### Phase 2: Express Channel for Knowledge Updates

#### 2.1 Dispatcher Modifications

```go
// pkg/dispatch/dispatcher.go
type Dispatcher struct {
    // ... existing fields ...
    knowledgeUpdateCh chan *proto.AgentMsg  // NEW: Express channel for knowledge
}
```

#### 2.2 Message Protocol Extension

```go
// pkg/proto/agent_msg.go
const (
    MsgTypeKNOWLEDGE_UPDATE = "KNOWLEDGE_UPDATE"  // NEW message type
)

type KnowledgeUpdatePayload struct {
    Entries  []KnowledgeEntry
    FilePath string
    Action   string  // "append", "replace"
}
```

#### 2.3 Coder Express Path

- Coder monitors `knowledgeUpdateCh` in WAITING state
- On knowledge update: WAITING → SETUP → CODING (skip PLANNING)
- SETUP detects knowledge update and configures minimal environment
- CODING writes file, commits, pushes, creates PR

### Phase 3: Knowledge Recording Tool

#### 3.1 Tool Definition

```go
// pkg/tools/knowledge_add.go
type KnowledgeAddTool struct{}

// Simple single-parameter design for better LLM compatibility
func (t *KnowledgeAddTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name:        "knowledge_add",
        Description: "Add entry to project knowledge graph",
        InputSchema: json.RawMessage(`{
            "type": "object",
            "properties": {
                "knowledge": {
                    "type": "object",
                    "description": "Knowledge entry matching graph structure"
                }
            },
            "required": ["knowledge"]
        }`),
    }
}
```

#### 3.2 Integration Points

- Available to both architect and coder agents
- Non-terminal tool (can be called with other tools)
- Accumulates in agent's knowledge buffer
- Persisted to database immediately
- Batched to file at spec completion

### Phase 4: Template Migration

#### 4.1 New Template Structure

```
pkg/templates/
├── architect/
│   ├── system_prompt.tmpl      # Base system prompt for agent context
│   ├── plan_review.tmpl        # Plan review (becomes user message)
│   ├── code_review.tmpl        # Code review (becomes user message)
│   ├── question.tmpl           # Question handling (becomes user message)
│   └── budget_review.tmpl      # Budget review (becomes user message)
├── pm/
│   └── ... existing PM templates ...
└── ... other templates ...
```

#### 4.2 System Prompt Design

The system prompt contains persistent context for the entire story:
- Agent identification (who architect is talking to)
- Current story details and spec context
- Knowledge pack (relevant project knowledge)
- Role and guidelines

Request-specific prompts become user messages appended to this context.

## Implementation Plan

### Step 1: Context Infrastructure (Priority 1)
1. Add multi-context fields to architect Driver struct
2. Implement `getContextForAgent()` method
3. Implement `ResetAgentContext()` method
4. Add context reset trigger on coder SETUP state transition

### Step 2: Update Request Handlers (Priority 1)
1. Modify `handleRequest()` to use agent-specific context
2. Update `handleSingleTurnReview()` to preserve context
3. Update `handleIterativeQuestion()` for context continuity
4. Update `handleIterativeApproval()` for context continuity

### Step 3: Template Migration (Priority 2)
1. Create `pkg/templates/architect/` directory
2. Move architect templates to new location
3. Convert request prompts to user message format
4. Create comprehensive system prompt template

### Step 4: Express Channel Implementation (Priority 2)
1. Add `knowledgeUpdateCh` to dispatcher
2. Implement knowledge message routing
3. Update coder WAITING state to monitor express channel
4. Implement fast-path through SETUP to CODING

### Step 5: Knowledge Tool Integration (Priority 3)
1. Implement `knowledge_add` tool
2. Add to architect and coder tool providers
3. Implement knowledge buffer management
4. Add spec completion trigger for persistence

### Step 6: Testing and Validation
1. Test multi-context switching between agents
2. Verify context preservation within stories
3. Test context reset on story transitions
4. Validate knowledge persistence flow
5. Test express channel for knowledge updates

## Migration Strategy

### Backward Compatibility
- Keep existing `contextManager` field during transition
- Use fallback to single context if multi-context not initialized
- Gradual migration of request handlers

### Rollout Phases
1. **Phase 1**: Deploy multi-context with feature flag
2. **Phase 2**: Enable for specific agent pairs in testing
3. **Phase 3**: Full rollout after validation
4. **Phase 4**: Remove legacy single context code

## Success Metrics

- **Context Consistency**: No contradictory feedback within story lifecycle
- **Memory Efficiency**: Context size bounded by story scope
- **Knowledge Capture**: Critical decisions persisted across stories
- **Performance**: No significant latency increase from context management
- **Reliability**: Clean context resets prevent cross-story contamination

## Risk Mitigation

### Risks
1. **Context Growth**: Contexts could grow large over long stories
   - *Mitigation*: Existing compaction system with sliding window

2. **Concurrency Issues**: Multiple goroutines accessing context maps
   - *Mitigation*: Proper mutex protection on all map operations

3. **Express Channel Complexity**: New message path adds complexity
   - *Mitigation*: Clear state machine transitions, comprehensive logging

4. **Knowledge File Conflicts**: Multiple updates could conflict
   - *Mitigation*: Queue updates, batch at natural boundaries

## Future Extensions

The express channel infrastructure enables:
- Hot fixes without planning overhead
- Emergency patches with minimal latency
- Direct file operations for maintenance tasks
- Potential for architect-initiated code updates

## Dependencies

- No changes required to BaseStateMachine
- No changes required to context manager interface
- Minimal changes to dispatcher (add one channel)
- No database schema changes (uses existing knowledge tables)

## Appendix: Context Reset Detection

The system can detect when to reset context through:
1. **State Change Notifications**: Monitor for WAITING → SETUP transitions
2. **Story ID Changes**: Detect when agent receives new story
3. **Explicit Reset Calls**: Orchestrator can trigger resets
4. **Timeout**: Reset contexts older than X hours (optional)