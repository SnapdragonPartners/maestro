# Merge Conflict Resolution Spec

## Problem Statement

When `PREPARE_MERGE` fails due to git conflicts or push failures, the coder is kicked back to `CODING` state but:

1. **Error messages are too vague** - The coder doesn't know what specifically failed or what tools to use
2. **Rebase is aborted** - Conflict information is lost, coder must re-trigger the conflict to resolve it
3. **No guidance on resolution** - Coder doesn't know it has `shell` tool with git CLI access
4. **No iteration limit** - Could loop forever between CODING and PREPARE_MERGE
5. **No dirty state detection** - PREPARE_MERGE doesn't detect if we're mid-rebase or have uncommitted changes from a previous attempt

Additionally, there's a **root cause investigation needed**: Single-story runs on fresh repos should never hit merge conflicts, suggesting the mirror or clone may not be fresh.

## Goals

1. Give coders clear, actionable information when merge operations fail
2. Allow coders to resolve conflicts using existing tools (shell + git)
3. Prevent infinite loops with a low iteration limit (2-3 attempts)
4. Handle re-entry to PREPARE_MERGE gracefully when workspace is in various git states
5. Identify and fix why merge conflicts occur on single-story fresh runs

## Non-Goals

- Adding new tools (coder already has shell with git access)
- Sharing architect knowledge about why conflicts exist
- Automatic conflict resolution

---

## Design

### 1. Enhanced Error Messages

When PREPARE_MERGE fails, return detailed information to the coder:

**Template changes** (`pkg/templates/coder/git_push_failure.tpl.md` or new `merge_conflict.tpl.md`):

```markdown
**Git Operation Failed: {{.FailureType}}**

{{.ErrorOutput}}

**Current Git State:**
{{.GitStatus}}

**Conflicting Files:**
{{range .ConflictingFiles}}
- {{.}}
{{end}}

**How to Resolve:**

You have access to the git CLI via the `shell` tool. Run these commands to investigate and resolve:

1. View current state: `git status`
2. See conflict details: `git diff`
3. Edit conflicting files to resolve conflicts (remove `<<<<<<<`, `=======`, `>>>>>>>` markers)
4. Stage resolved files: `git add <filename>`
5. {{if .MidRebase}}Continue rebase: `git rebase --continue`{{else}}Commit changes: `git commit -m "Resolve merge conflicts"`{{end}}
6. Call the `done` tool to retry merge

If you cannot resolve the conflict, use `ask_question` to escalate to the architect.

**Attempt {{.AttemptNumber}} of {{.MaxAttempts}}**
```

### 2. Don't Abort Rebase on Conflict

**Change in `prepare_merge.go`**:

Current behavior:
```go
// Abort the rebase to leave workspace in clean state
_, _ = c.longRunningExecutor.Run(ctx, []string{"git", "rebase", "--abort"}, opts)
return fmt.Errorf("rebase has conflicts that require manual resolution: %s", result.Stderr)
```

New behavior:
```go
// Leave workspace in conflicted state so coder can resolve
// Capture conflict information for error message
conflictFiles := c.getConflictingFiles(ctx, opts)
gitStatus := c.getGitStatus(ctx, opts)
return &MergeConflictError{
    Type:            "rebase_conflict",
    ErrorOutput:     result.Stderr,
    ConflictingFiles: conflictFiles,
    GitStatus:       gitStatus,
    MidRebase:       true,
}
```

### 3. Smart Iteration Limit

**Key insight**: Distinguish between "coder is stuck on the same problem" vs "the world changed and coder needs another shot".

**State tracking**:
```go
type MergeAttemptState struct {
    AttemptCount   int    // Total PREPARE_MERGE entries
    LastRemoteHEAD string // SHA of origin/main at last attempt
    StuckAttempts  int    // Consecutive attempts where remote HEAD didn't change
}
```

**Logic**:
- Track remote HEAD SHA at each PREPARE_MERGE attempt
- If remote HEAD changed since last attempt → reset stuck counter (legitimate new conflict)
- If remote HEAD is same → increment stuck counter (coder failed to resolve)

**Limits**:
- **2 stuck attempts**: Fail if coder can't resolve the same conflict twice
- **3 total attempts**: Hard cap regardless of HEAD changes (safety net for pathological race conditions)

**Implementation**:
```go
const MaxStuckAttempts = 2
const MaxTotalAttempts = 3

func (c *Coder) handlePrepareMerge(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // Get current remote HEAD
    currentRemoteHEAD, err := c.getRemoteHEAD(ctx, targetBranch)
    if err != nil {
        return proto.StateError, false, fmt.Errorf("failed to get remote HEAD: %w", err)
    }

    // Load previous attempt state
    attemptCount := utils.GetStateValueOr[int](sm, KeyMergeAttemptCount, 0) + 1
    lastRemoteHEAD := utils.GetStateValueOr[string](sm, KeyLastRemoteHEAD, "")
    stuckAttempts := utils.GetStateValueOr[int](sm, KeyMergeStuckAttempts, 0)

    // Check if remote HEAD changed
    if currentRemoteHEAD != lastRemoteHEAD && lastRemoteHEAD != "" {
        // World changed - reset stuck counter, this is a fresh conflict
        c.logger.Info("🔀 Remote HEAD changed (%s -> %s), resetting stuck counter",
            lastRemoteHEAD[:8], currentRemoteHEAD[:8])
        stuckAttempts = 0
    } else if lastRemoteHEAD != "" {
        // Same HEAD - coder is stuck on same problem
        stuckAttempts++
    }

    // Update state
    sm.SetStateData(KeyMergeAttemptCount, attemptCount)
    sm.SetStateData(KeyLastRemoteHEAD, currentRemoteHEAD)
    sm.SetStateData(KeyMergeStuckAttempts, stuckAttempts)

    // Check limits
    if stuckAttempts >= MaxStuckAttempts {
        return proto.StateError, false, fmt.Errorf(
            "failed to resolve merge conflict after %d attempts on same HEAD - story will be reassigned",
            stuckAttempts,
        )
    }
    if attemptCount >= MaxTotalAttempts {
        return proto.StateError, false, fmt.Errorf(
            "exceeded maximum merge attempts (%d) - story will be reassigned",
            attemptCount,
        )
    }

    // ... rest of function
}
```

**Behavior when limit exceeded**:
- Log error with full context
- Transition to ERROR state (story fails)
- Story will be reassigned to fresh coder on updated main

### 4. Dirty State Detection on Entry

At the start of PREPARE_MERGE, detect the current git state:

```go
type GitWorkspaceState struct {
    MidRebase        bool     // .git/rebase-merge or .git/rebase-apply exists
    HasConflicts     bool     // Files with UU status in git status
    HasUncommitted   bool     // Modified/staged files
    ConflictingFiles []string // List of files with conflicts
    UnpushedCommits  bool     // Local commits not on remote
}

func (c *Coder) detectGitState(ctx context.Context) (*GitWorkspaceState, error) {
    // Check for mid-rebase
    // Run: test -d .git/rebase-merge || test -d .git/rebase-apply

    // Get git status
    // Run: git status --porcelain
    // Parse output for:
    //   - UU = unmerged (conflict)
    //   - M/A/D = modified/added/deleted (uncommitted)

    // Check for unpushed commits
    // Run: git log origin/<branch>..HEAD --oneline
}
```

**Branching logic based on state**:

```go
func (c *Coder) handlePrepareMerge(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    state, err := c.detectGitState(ctx)
    if err != nil {
        return proto.StateError, false, err
    }

    // If mid-rebase with conflicts, kick back to CODING
    if state.MidRebase && state.HasConflicts {
        return c.returnToCodeingWithConflictInfo(ctx, sm, state)
    }

    // If mid-rebase without conflicts, continue the rebase
    if state.MidRebase && !state.HasConflicts {
        if err := c.continueRebase(ctx); err != nil {
            return c.returnToCodingWithError(ctx, sm, err)
        }
        // Fall through to push
    }

    // If uncommitted changes (not mid-rebase), commit them
    if state.HasUncommitted && !state.MidRebase {
        if err := c.commitChanges(ctx, storyID); err != nil {
            return c.returnToCodingWithError(ctx, sm, err)
        }
    }

    // Proceed to push...
}
```

---

## Implementation Plan

### Phase 1: Error Message Enhancement
1. Create structured `MergeConflictError` type with all context
2. Add helper functions: `getConflictingFiles()`, `getGitStatus()`
3. Create/update templates with detailed resolution instructions
4. Update error handling in PREPARE_MERGE to use new templates

### Phase 2: Stop Aborting Rebase
1. Remove `git rebase --abort` call on conflict detection
2. Capture conflict state before returning to CODING
3. Test that coder can resolve and continue

### Phase 3: Iteration Limit
1. Add `KeyMergeAttemptCount` state key
2. Increment on PREPARE_MERGE entry
3. Check limit and fail story if exceeded
4. Reset counter on story start (in SETUP)

### Phase 4: Dirty State Detection
1. Implement `detectGitState()` function
2. Add branching logic at start of PREPARE_MERGE
3. Implement `continueRebase()` helper
4. Test various re-entry scenarios

### Phase 5: Mirror Refresh Investigation (Do Last)
1. Analyze mirror refresh logic in clone manager
2. Add logging to track mirror freshness
3. Identify why single-story runs hit conflicts
4. Fix root cause

---

## Mirror Refresh Investigation

### Hypothesis

Single-story runs on fresh repos should never hit merge conflicts. If they do, either:
1. **Mirror not being updated** before clone
2. **Clone not fresh** (using stale cached clone)
3. **Race condition** in parallel story scenarios leaking into single-story tests

### Investigation Steps

1. **Add debug logging** to mirror refresh:
   - Log when mirror is fetched
   - Log mirror HEAD before/after fetch
   - Log clone source commit

2. **Trace the flow**:
   - SETUP phase: Where does clone come from?
   - Is `git fetch` being called on the mirror?
   - What commit is the coder's workspace based on?

3. **Compare commits**:
   - Coder workspace HEAD at start
   - Origin/main HEAD at push time
   - When did they diverge?

### Files to Investigate

- `pkg/coder/setup.go` - Clone setup
- `pkg/coder/clone_manager.go` - Mirror management
- `pkg/workspace/` - Workspace preparation

### Fix Priority

**Do this last** because the stale mirror bug is actually helpful for testing the conflict resolution changes. Once conflict resolution is working, fix the root cause so it doesn't happen in normal operation.

---

## Testing

### Unit Tests
- `detectGitState()` with various git states
- Iteration counter increment/limit
- Error message formatting

### Integration Tests
1. **Happy path**: No conflicts, push succeeds
2. **Auto-rebase success**: Non-conflicting changes on main, rebase succeeds
3. **Conflict resolution**: Conflicting changes, coder resolves, retry succeeds
4. **Iteration limit**: Coder fails to resolve, hits limit, story fails
5. **Mid-rebase re-entry**: Coder returns after resolving, rebase continues

### Manual Tests
- Intentionally create conflict scenario
- Verify error message is clear and actionable
- Verify coder can resolve using shell + git
- Verify limit triggers story failure appropriately

---

## Open Questions

1. **Iteration limit value**: Spec says 2. Reviewer suggested 3 to handle the race condition where remote moves again after first resolution. **Decision needed.**
2. ~~**Escalation path**: When limit is hit, should we post to chat for human intervention before failing?~~ **Decided: No.** Primary mode is unattended. Failing and restarting story is better than waiting on chat (which has no timeout).
3. **Conflict type detection**: Should we distinguish between rebase conflicts vs other git errors in the messaging? **Reviewer recommends yes** - add `FailureKind` enum to help coder craft right remedy and decide if attempt counts toward limit.

---

## Reviewer Feedback Summary

External review provided additional recommendations:

### Iteration Limit
- Suggested **3** instead of 2 to handle race condition where remote moves again after first resolution
- ~10-15% of branches need third attempt in parallel development scenarios
- Keep configurable in `.maestro/config.json`

### Additional Dirty State Detection
Expand `GitWorkspaceState` to cover all git "operation in progress" states:

```go
type GitWorkspaceState struct {
    MidRebase        bool     // .git/rebase-merge or .git/rebase-apply exists
    MidMerge         bool     // .git/MERGE_HEAD exists
    MidCherryPick    bool     // .git/CHERRY_PICK_HEAD exists
    MidRevert        bool     // .git/REVERT_HEAD exists
    SequencerRunning bool     // .git/sequencer exists (interactive rebase)
    IndexLocked      bool     // .git/index.lock present (stale lock from crash)
    DetachedHEAD     bool     // detached HEAD state
    HasConflicts     bool     // Files with UU status
    HasUncommitted   bool     // Modified/staged files
    HasStagedChanges bool     // Staged but uncommitted (git diff --cached)
    ConflictingFiles []string
    UnpushedCommits  bool
}
```

**YAGNI Note**: Maestro currently only uses `git rebase` - we don't cherry-pick or revert. For initial implementation, only detect the states we actually encounter: `MidRebase`, `MidMerge`, `IndexLocked`, `HasConflicts`, `HasUncommitted`. Add the others (cherry-pick, revert, sequencer, detached HEAD) only if we hit them in practice.

### Edge Cases to Handle
1. **Push rejected without conflicts** - Remote moved but no actual conflict; auto-pull-rebase
2. **Binary conflicts** - No markers to edit; guide coder to use `git checkout --ours/--theirs`
3. **Auth failures vs logical conflicts** - Don't count auth failures toward iteration limit
4. **Index lock left behind** - Detect and clear stale `.git/index.lock` after container crash
5. **Large conflict files** - Truncate diff output in error payload to avoid huge messages

### Failure Kind Enum
Add explicit failure categorization:

```go
type MergeFailureKind string

const (
    FailureRebaseConflict MergeFailureKind = "rebase_conflict"
    FailureMergeConflict  MergeFailureKind = "merge_conflict"
    FailurePushRejected   MergeFailureKind = "push_rejected"
    FailureAuthError      MergeFailureKind = "auth_error"
    FailureUnknown        MergeFailureKind = "unknown"
)
```

This helps:
- Coder knows which remedy to apply
- PREPARE_MERGE can decide if attempt counts toward limit (auth errors shouldn't)

### Persistence for Post-Mortems
- Persist conflict diff text in story log/DB for debugging failed stories
- When story restarts on fresh coder, original conflict context is lost from workspace but available in logs

---

## Implementation Status

**Implemented 2025-12-10**

### Phase 1: Enhanced Error Messages ✅
- Created `MergeConflictInfo` struct with failure kind, error output, conflicting files, git status
- Created `buildConflictResolutionMessage()` that produces detailed markdown with:
  - Type of failure (rebase conflict, merge conflict, push rejected, auth error)
  - Raw error output
  - List of conflicting files
  - Current git status
  - Step-by-step resolution instructions with specific git commands
  - Attempt counter (N of 3)
- Files: `pkg/coder/merge_conflict.go`

### Phase 2: Stop Aborting Rebase ✅
- Removed `git rebase --abort` call when conflict detected
- Created `RebaseConflictError` type that captures conflict state
- Workspace is now left in mid-rebase state for coder to resolve
- Files: `pkg/coder/prepare_merge.go`

### Phase 3: Smart Iteration Limit ✅
- Added state keys: `KeyMergeAttemptCount`, `KeyMergeStuckAttempts`, `KeyLastRemoteHEAD`
- Smart limit: 2 stuck attempts (same remote HEAD), 3 total (hard cap)
- Tracks remote HEAD to distinguish "coder stuck on same problem" vs "world changed"
- Auth failures: Not yet distinguished from logical conflicts (future enhancement)
- Files: `pkg/coder/merge_conflict.go`, `pkg/coder/prepare_merge.go`

### Phase 4: Dirty State Detection ✅
- Implemented `detectGitWorkspaceState()` that checks:
  - MidRebase (`.git/rebase-merge` or `.git/rebase-apply`)
  - MidMerge (`.git/MERGE_HEAD`)
  - IndexLocked (`.git/index.lock`)
  - HasConflicts (UU status in git status)
  - HasUncommitted (other modified files)
- Added `clearStaleIndexLock()` helper
- Added `continueRebase()` helper
- PREPARE_MERGE now handles re-entry scenarios gracefully
- YAGNI: Cherry-pick, revert, sequencer, detached HEAD detection deferred
- Files: `pkg/coder/merge_conflict.go`, `pkg/coder/prepare_merge.go`

### Phase 5: Update Error Templates ✅
- Using programmatic `buildConflictResolutionMessage()` instead of template
- More context-aware than static templates
- Includes specific git commands for the detected situation

### Phase 6: Mirror Refresh Fix ✅
- **Root cause identified**: Clone was fetching from local mirror only
- Mirror could be stale if origin updated between mirror update and clone creation
- **Fix**: After setting up origin remote, fetch from origin AND reset to origin/baseBranch
- Ensures coder always starts from latest origin state
- Files: `pkg/coder/clone.go`

## Acceptance Criteria

- [x] Coder receives clear error message with conflicting files listed
- [x] Coder is told explicitly about shell tool + git access
- [x] Rebase is NOT aborted, workspace stays in conflicted state
- [x] Coder can resolve conflicts and call `done` to retry
- [x] PREPARE_MERGE detects mid-rebase state and handles appropriately
- [x] PREPARE_MERGE detects other git operation states (merge, index lock)
- [x] Iteration counter limits attempts (2 stuck / 3 total)
- [ ] Auth failures don't count toward iteration limit (partial - detected but not distinguished)
- [x] Story fails cleanly when limit exceeded
- [ ] Conflict context persisted to logs for post-mortem analysis (not implemented - existing logging may be sufficient)
- [x] Mirror refresh bug identified and fixed
