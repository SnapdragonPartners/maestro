# Payload Refactor: Discriminated Union Design

## Problem Statement

The current `AgentMsg.Payload map[string]any` design caused a critical silent failure where:
- Architect changed response format to send `ApprovalResult` struct under `"approval_result"` key
- Effects continued looking for individual `"status"` and `"feedback"` keys
- Plan approvals silently failed with no error messages
- Story status never updated from PLANNING to CODING
- Agent remained stuck thinking workspace was read-only

**Root cause**: Type assertions on `map[string]any` return zero values on mismatch, causing silent failures instead of explicit errors.

## Solution: Discriminated Union Pattern

Replace `Payload map[string]any` with typed `Payload *MessagePayload` where `MessagePayload` is a discriminated union:

```go
type MessagePayload struct {
    Kind PayloadKind     // Discriminator
    Data json.RawMessage // Lazy unmarshal
}
```

### Design Benefits

1. **Forced deserialization** - Cannot access payload without explicit type extraction
2. **Explicit errors** - Wrong payload type returns error, not zero value
3. **Type safety** - Payload structure is clear at message creation
4. **Serialization ready** - Already JSON-compatible for logging/persistence
5. **No silent failures** - Mismatches cause immediate, debuggable errors

## Migration Strategy

### Phase 1: Update Core Types

1. Add `MessagePayload` discriminated union in `pkg/proto/payload.go` ✅
2. Change `AgentMsg.Payload` from `map[string]any` to `*MessagePayload`
3. Remove old `SetPayload/GetPayload` methods
4. Add new typed methods: `SetTypedPayload(*MessagePayload)` and `GetTypedPayload() *MessagePayload`

### Phase 2: Update Message Creators

Update all message creation sites to use typed constructors:

**Effect packages** (~6 files):
- `pkg/effect/approval.go` - Use `NewApprovalRequestPayload/ResponsePayload`
- `pkg/effect/budget_review.go` - Use `NewApprovalRequestPayload/ResponsePayload`
- `pkg/effect/question.go` - Use `NewQuestionRequestPayload/ResponsePayload`
- `pkg/effect/merge.go` - Use `NewMergeRequestPayload/ResponsePayload`
- `pkg/effect/completion.go` - Use `NewApprovalRequestPayload/ResponsePayload`

**Architect packages** (~8 files):
- `pkg/architect/dispatching.go` - Use `NewGenericPayload` for STORY messages
- `pkg/architect/request.go` - Extract typed payloads
- `pkg/architect/review.go` - Use `NewApprovalResponsePayload`
- `pkg/architect/questions.go` - Use `NewQuestionResponsePayload`

**Coder packages** (~5 files):
- Various coder files creating REQUEST messages

**Dispatcher** (~2 files):
- `pkg/dispatch/dispatcher.go` - Handle typed payloads

### Phase 3: Update Payload Extractors

Replace all `GetPayload("key")` calls with typed extraction:

```go
// Old pattern (prone to silent failures)
statusRaw, exists := msg.GetPayload("status")
status, ok := statusRaw.(string)  // Silent failure if wrong type

// New pattern (explicit errors)
result, err := msg.GetTypedPayload().ExtractApprovalResponse()
if err != nil {
    return fmt.Errorf("invalid payload: %w", err)
}
status := result.Status  // Type-safe access
```

### Phase 4: Testing Strategy

1. Build and fix compilation errors
2. Run existing test suite
3. Manual integration testing with actual agent workflows
4. Verify error messages are clear and actionable

## Implementation Details

### Payload Kind Mapping

| Message Type | Payload Kind | Struct Type |
|--------------|--------------|-------------|
| REQUEST (question) | `question_request` | `QuestionRequestPayload` |
| RESPONSE (question) | `question_response` | `QuestionResponsePayload` |
| REQUEST (approval) | `approval_request` | `ApprovalRequestPayload` |
| RESPONSE (approval) | `approval_response` | `ApprovalResult` |
| REQUEST (merge) | `merge_request` | `MergeRequestPayload` |
| RESPONSE (merge) | `merge_response` | `MergeResponsePayload` |
| REQUEST (requeue) | `requeue_request` | `RequeueRequestPayload` |
| RESPONSE (requeue) | `requeue_response` | `RequeueResponsePayload` |
| STORY | `story` | `map[string]any` (generic) |
| SPEC | `spec` | `map[string]any` (generic) |
| ERROR | `error` | `map[string]any` (generic) |
| SHUTDOWN | `shutdown` | empty |

### Backward Compatibility

**None required** - We are pre-release. All code changes at once.

### Key Files to Update

**Proto layer** (3 files):
- `pkg/proto/payload.go` ✅ Created
- `pkg/proto/message.go` - Change Payload type
- `pkg/proto/unified_protocol.go` - Update helper functions

**Effect layer** (6 files):
- `pkg/effect/approval.go`
- `pkg/effect/budget_review.go`
- `pkg/effect/question.go`
- `pkg/effect/merge.go`
- `pkg/effect/completion.go`
- `pkg/effect/test_failure.go`

**Architect layer** (8 files):
- `pkg/architect/dispatching.go`
- `pkg/architect/request.go`
- `pkg/architect/review.go`
- `pkg/architect/questions.go`
- `pkg/architect/driver.go`
- `pkg/architect/scoping.go`

**Coder layer** (5 files):
- `pkg/coder/planning.go`
- `pkg/coder/coding.go`
- `pkg/coder/questions.go`
- Files creating approval/completion requests

**Dispatcher layer** (2 files):
- `pkg/dispatch/dispatcher.go`
- `pkg/dispatch/routing.go`

**Supporting layers** (~10 files):
- Test files
- Persistence layer (if reading payloads)
- Build/bootstrap (if using payloads)

**Total estimated files**: ~40 files, 141 call sites

## Success Criteria

1. ✅ All code compiles without errors
2. ✅ All existing tests pass
3. ✅ No `SetPayload/GetPayload` calls with string keys remain
4. ✅ Manual testing shows plan approval works correctly
5. ✅ Story status updates from PLANNING to CODING
6. ✅ Error messages are clear when payload mismatches occur

## Rollback Plan

Revert the commit. Single atomic change, easy rollback.

## Timeline

Estimated: 2-3 hours for complete refactor including testing.

## Notes

- Keep `Metadata map[string]string` unchanged - it's working as intended for observability
- Messages stay in-memory (Go channels), no serialization in production
- json.RawMessage allows lazy deserialization for performance
- This pattern makes future persistence/logging trivial
