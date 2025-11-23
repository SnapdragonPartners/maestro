# Architect Tool Configuration

This document defines the tool sets provided to the architect agent for different request types.

## Tool Configuration by Request Type

### 1. **Plan Approval** (Single-turn)
- **Handler**: `handleSingleTurnReview`
- **General Tools**: None
- **Terminal Tool**: `review_complete`
- **Rationale**: Plans are text descriptions submitted by the coder. The architect reviews the plan content directly without needing workspace inspection. Single-turn decision based on plan text alone.

### 2. **Budget Review** (Single-turn)
- **Handler**: `handleSingleTurnReview`
- **General Tools**: None
- **Terminal Tool**: `review_complete`
- **Rationale**: Budget reviews evaluate resource usage and constraints based on request content. No workspace inspection needed, single-turn decision.

### 3. **Code Approval** (Iterative)
- **Handler**: `handleIterativeApproval`
- **General Tools**: `read_file`, `list_files`, `get_diff`
- **Terminal Tool**: `submit_reply`
- **Rationale**: Code reviews require inspecting the workspace to verify implementation. The architect may need multiple iterations to explore the codebase. `get_diff` is useful for seeing changes.

### 4. **Completion Review** (Iterative)
- **Handler**: `handleIterativeApproval`
- **General Tools**: `read_file`, `list_files`, `get_diff`
- **Terminal Tool**: `submit_reply`
- **Rationale**: Completion reviews verify all acceptance criteria are met by inspecting the final workspace state. Multiple iterations may be needed to check all criteria. `get_diff` helps see what was implemented.

### 5. **Technical Questions** (Iterative)
- **Handler**: `handleIterativeQuestion`
- **General Tools**: `read_file`, `list_files`
- **Terminal Tool**: `submit_reply`
- **Rationale**: Questions require workspace inspection to understand the code context. Multiple iterations may be needed to explore. `get_diff` is excluded since there's no "change" being reviewed in a Q&A context.

### 6. **Spec Review** (Special - from PM)
- **Handler**: `handleSpecReview`
- **General Tools**: `read_file`, `list_files`
- **Terminal Tools**: `submit_stories` (approval), `spec_feedback` (rejection)
- **Rationale**: Spec reviews from PM may need to reference existing project files. Uses PM-specific terminal tools for the workflow (submit stories on approval, provide feedback on rejection).

---

## Design Principles

### Terminal Tools vs General Tools

**Terminal Tools**: Signal completion of the review/question and transition to the next state. The architect must call exactly one terminal tool to complete the interaction.

**General Tools**: Support exploration and analysis but do not complete the interaction. Can be called multiple times in iterative modes.

### Single-turn vs Iterative

**Single-turn** (`SingleTurn: true` in toolloop):
- Only terminal tools provided (no general tools)
- Architect must make decision immediately based on request content
- Used for: Plan reviews, Budget reviews
- Expects terminal tool call in first iteration

**Iterative** (`SingleTurn: false` in toolloop):
- Both general tools and terminal tools provided
- Architect can explore workspace across multiple LLM calls
- Used for: Code reviews, Completion reviews, Questions
- Terminal tool called when analysis is complete

### Tool Selection Rationale

**Why no workspace tools for plan reviews?**
- Plans are descriptions of intended implementation, not actual code
- The plan text itself contains all information needed for review
- Workspace inspection would show current state, not planned changes

**Why include `get_diff` for code/completion but not questions?**
- Code reviews: `get_diff` shows what changed (the diff is the subject of review)
- Completion reviews: `get_diff` shows what was implemented (helps verify completeness)
- Questions: No relevant "diff" - coder is asking about existing code or design decisions

**Why different terminal tools?**
- `review_complete`: For reviews requiring a decision (APPROVED/NEEDS_CHANGES/REJECTED)
- `submit_reply`: For open-ended responses (questions, iterative review feedback)
- `submit_stories`/`spec_feedback`: PM workflow-specific tools

---

## Implementation Details

**Code Locations**:
- Tool provider creation: `pkg/architect/driver.go::createReadToolProviderForCoder()`
- Request routing: `pkg/architect/request.go::handleApprovalRequest()`
- Single-turn handler: `pkg/architect/request.go::handleSingleTurnReview()`
- Iterative approval handler: `pkg/architect/request.go::handleIterativeApproval()`
- Question handler: `pkg/architect/request.go::handleIterativeQuestion()`
- Spec review handler: `pkg/architect/request_spec.go::handleSpecReview()`

**Tool Constants**: `pkg/tools/constants.go`

**Key Function Signatures**:
```go
// Create tool provider for coder workspace with optional get_diff
func (d *Driver) createReadToolProviderForCoder(coderID string, includeGetDiff bool) *tools.ToolProvider

// Single-turn reviews (plan, budget)
func (d *Driver) handleSingleTurnReview(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error)

// Iterative approvals (code, completion)
func (d *Driver) handleIterativeApproval(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error)

// Iterative questions
func (d *Driver) handleIterativeQuestion(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error)
```

---

## Future Considerations

### Semantic Naming Improvement

The current tool names could be more semantically consistent:
- `review_complete` → `submit_review` (all approval types are "reviews")
- This would make it clearer that questions use `submit_reply` (not a review) while all approvals use `submit_review` (they are reviews)

### Potential Tool Additions

- **Architecture-level tools**: For spec reviews, could add tools to query architectural patterns from knowledge graph
- **Test execution tools**: For completion reviews, could add ability to run tests and inspect results
- **Lint/validation tools**: For code reviews, could add static analysis tools

### Tool Set Validation

Consider adding runtime validation that:
- Single-turn modes only receive terminal tools
- Iterative modes always include at least one terminal tool
- No tool set includes multiple terminal tools (except PM workflow)
