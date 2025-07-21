# Merge Workflow Implementation Plan

## Overview

Complete the end-to-end story workflow by implementing PR merge automation and merge conflict resolution. This extends the Worktree MVP to handle the full lifecycle from story assignment to story completion.

## Current Gap

**Current flow stops at PR creation:**
```
Coder: CODE_REVIEW approved → creates PR → DONE (incomplete!)
Architect: ??? (no merge logic)
Story: Never marked as truly complete
```

**Missing pieces:**
1. Coder notification to architect that PR is ready for merge
2. Architect logic to merge PR when ready
3. Merge conflict detection and resolution
4. Story completion only after successful merge

## Solution Design

### 1. PR Merge Request Flow

**Use existing REQUEST/RESULT infrastructure instead of new message types:**

```
1. Architect: CODE_REVIEW approved → RESULT(approved) → MONITORING
2. Coder: Creates PR → REQUEST(type=merge) → Architect  
3. Architect: MONITORING handles merge REQUEST → attempts merge
4a. Success: RESULT(merged) → Coder → DONE → story complete
4b. Conflict: RESULT(merge_conflict) → Coder → FIXING → resolve → TESTING → ...
```

### 2. Enhanced FIXING State

**Reuse FIXING state for merge conflicts instead of new MERGE_CONFLICT state:**

**Current FIXING handles:**
- Test failures from TESTING
- Code review rejections from CODE_REVIEW

**Enhanced FIXING will handle:**
- Merge conflicts from merge REQUEST failures

**Benefits:**
- No new state needed
- Same transitions work: FIXING → TESTING → CODE_REVIEW
- All "fix something" logic centralized
- Natural flow for conflict resolution

## Implementation Tasks

### Task 1: Modify Coder PR Flow

**File:** `pkg/coder/driver.go`

**Changes to `handleCodeReview()`:**
```go
case proto.ApprovalStatusApproved:
    c.logger.Info("🧑‍💻 Code approved, pushing branch and creating PR")
    
    // Create PR
    if err := c.pushBranchAndCreatePR(ctx, sm); err != nil {
        return agent.StateError, false, err
    }
    
    // Send merge REQUEST to architect instead of going to DONE
    if err := c.sendMergeRequest(ctx, sm); err != nil {
        return agent.StateError, false, err
    }
    
    // Wait for merge RESULT
    return StateAwaitMerge, false, nil  // New state or reuse CODE_REVIEW?
```

**New function `sendMergeRequest()`:**
```go
func (c *Coder) sendMergeRequest(ctx context.Context, sm *agent.BaseStateMachine) error {
    storyID, _ := sm.GetStateValue("story_id")
    prURL, _ := sm.GetStateValue("pr_url")
    branchName, _ := sm.GetStateValue("pushed_branch")
    
    requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.GetID(), "architect")
    requestMsg.SetPayload("request_type", "merge")
    requestMsg.SetPayload("pr_url", prURL)
    requestMsg.SetPayload("branch_name", branchName)
    requestMsg.SetPayload("story_id", storyID)
    
    return c.dispatcher.DispatchMessage(requestMsg)
}
```

### Task 2: Architect Merge Logic

**File:** `pkg/architect/review.go` (or wherever MONITORING handles REQUESTs)

**Add merge request handling:**
```go
// In handleRequest() or similar
case "merge":
    return c.handleMergeRequest(ctx, request)

func (c *Architect) handleMergeRequest(ctx context.Context, request *proto.AgentMsg) error {
    prURL, _ := request.GetPayload("pr_url")
    branchName, _ := request.GetPayload("branch_name") 
    storyID, _ := request.GetPayload("story_id")
    
    // Attempt merge using GitHub CLI
    mergeResult, err := c.attemptPRMerge(ctx, prURL, storyID)
    
    // Send RESULT back to coder
    resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, c.GetID(), request.FromAgent)
    if err != nil || mergeResult.HasConflicts {
        resultMsg.SetPayload("status", "merge_conflict")
        resultMsg.SetPayload("conflict_details", mergeResult.ConflictInfo)
    } else {
        resultMsg.SetPayload("status", "merged")
        resultMsg.SetPayload("merge_commit", mergeResult.CommitSHA)
        
        // Mark story as completed in queue
        c.queue.MarkCompleted(storyID)
    }
    
    return c.dispatcher.DispatchMessage(resultMsg)
}
```

**New function `attemptPRMerge()`:**
```go
func (c *Architect) attemptPRMerge(ctx context.Context, prURL, storyID string) (*MergeResult, error) {
    // Use gh CLI to merge PR
    // gh pr merge <pr-url> --squash --delete-branch
    // Handle merge conflicts gracefully
    // Return structured result
}
```

### Task 3: Enhanced FIXING State

**File:** `pkg/coder/driver.go`

**Modify `handleFixing()` to handle merge conflicts:**
```go
func (c *Coder) handleFixing(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
    // Check what triggered FIXING
    fixingReason, _ := sm.GetStateValue("fixing_reason")
    
    switch fixingReason {
    case "test_failure":
        return c.handleTestFailureFix(ctx, sm)
    case "code_review_rejection":
        return c.handleReviewRejectionFix(ctx, sm)
    case "merge_conflict":
        return c.handleMergeConflictFix(ctx, sm)
    default:
        // Existing logic for backward compatibility
        return c.handleGenericFix(ctx, sm)
    }
}

func (c *Coder) handleMergeConflictFix(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
    // 1. Pull latest changes from main branch
    // 2. Identify conflict files
    // 3. Use LLM to resolve conflicts intelligently
    // 4. Update implementation as needed
    // 5. Transition to TESTING (conflicts might break tests)
}
```

### Task 4: Coder Await Merge State

**Option A: New state `StateAwaitMerge`**
```go
const StateAwaitMerge agent.State = "AWAIT_MERGE"

// Transitions:
StateCodeReview: {StateAwaitMerge, StateFixing, agent.StateError}
StateAwaitMerge: {agent.StateDone, StateFixing} // Done on success, Fixing on conflict
```

**Option B: Reuse CODE_REVIEW state**
- Stay in CODE_REVIEW after sending merge request
- Handle merge RESULT in existing CODE_REVIEW logic
- Simpler but less clear semantically

### Task 5: Story Completion Logic

**File:** `pkg/architect/queue.go`

**Modify story completion:**
```go
// Only mark stories complete after successful PR merge
// Not after code approval
func (q *Queue) MarkMerged(storyID, mergeCommit string) error {
    story, exists := q.stories[storyID]
    if !exists {
        return fmt.Errorf("story %s not found", storyID)
    }
    
    now := time.Now().UTC()
    story.Status = StatusCompleted
    story.CompletedAt = &now
    story.LastUpdated = now
    
    // Store merge info
    story.MergeCommit = mergeCommit
    
    // Check if any pending stories became ready
    q.checkAndNotifyReady()
    
    return nil
}
```

## FSM Changes Summary

### Coder FSM Updates
```go
// New transition (Option A):
StateCodeReview: {StateAwaitMerge, StateFixing, agent.StateError}
StateAwaitMerge: {agent.StateDone, StateFixing}

// Enhanced state handling:
StateFixing: {StateTesting, StateQuestion, agent.StateError} // Same as before
// But now triggered by: test_failure, code_review_rejection, OR merge_conflict
```

### Architect FSM Updates
```go
// No FSM changes needed - MONITORING already handles REQUESTs
// Just add merge request type handling
```

## Testing Strategy

### Unit Tests
1. **Coder merge request creation**
2. **Architect merge request handling** 
3. **Enhanced FIXING state logic**
4. **Story completion only after merge**

### Integration Tests
1. **End-to-end story flow** with successful merge
2. **Merge conflict resolution** flow
3. **Multiple stories** with dependencies
4. **Error handling** (PR creation fails, merge fails, etc.)

### Manual Testing
1. **Real GitHub repository** with merge conflicts
2. **SSH key setup** and permissions
3. **GitHub CLI authentication**
4. **Network timeout scenarios**

## Dependencies & Prerequisites

1. **GitHub CLI (`gh`)** installed and authenticated
2. **SSH keys** configured for Git push operations  
3. **Repository permissions** for merge operations
4. **GITHUB_TOKEN** environment variable for API access

## Migration Strategy

1. **Phase 1**: Implement basic merge request flow (no conflicts)
2. **Phase 2**: Add merge conflict detection and enhanced FIXING
3. **Phase 3**: Add comprehensive error handling and edge cases
4. **Phase 4**: Performance optimization and monitoring

## Open Questions

1. **StateAwaitMerge vs reusing CODE_REVIEW?** Which is cleaner?
2. **Merge strategy**: Squash, merge commit, or rebase?
3. **Branch cleanup**: Delete branches after merge or keep them?
4. **Multiple conflicts**: How to handle repeated conflict resolution failures?
5. **Concurrent merges**: How to handle multiple PRs ready simultaneously?

## Success Criteria

✅ **Complete automation**: Story assignment → implementation → PR → merge → completion  
✅ **Conflict resolution**: Automatic detection and intelligent resolution  
✅ **Dependency unlocking**: Completed stories unlock dependent stories  
✅ **Error recovery**: Graceful handling of merge failures  
✅ **Performance**: No blocking operations that halt other story progress