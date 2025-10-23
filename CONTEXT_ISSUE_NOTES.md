# Context Issue Investigation Notes

**Date**: 2025-10-22
**Issue**: Coder agents are repeating work and rewriting files multiple times in CODING state

## Symptoms

1. **Excessive file rewrites**: `main.go` written 41 times, `main()` function appears 14 times
2. **Empty response warnings**: 14-16 instances of LLM returning conversational text without tool calls
3. **Context growth**: Coder-002 context grew from 4,032 tokens → 19,606 tokens
4. **Budget review loop**: CODING → BUDGET_REVIEW → CODING repeatedly

## Root Causes Identified

### 1. Budget Review Loop Without State Awareness
- When coder exceeds coding budget, goes to BUDGET_REVIEW
- Architect approves and coder returns to CODING
- **Problem**: Coder has no memory of what was already done
- Starts coding phase from scratch each time
- Log evidence: "🧑‍💻 Starting coding phase for story_type 'app'" after every budget approval

### 2. Context Pollution
- Each CODING iteration adds tool results to context
- LLM sees previous `cat > main.go` commands in history
- LLM doesn't recognize these as "work already done"
- Keeps rewriting files thinking it needs to
- Context includes multiple identical file writes (shown in ERROR logs)

### 3. No Task Completion Detection
- Coder doesn't check if task is complete before next iteration
- Should verify:
  - Files already exist and are correct
  - Tests pass
  - Requirements satisfied
- Instead, keeps iterating and redoing work

## Log Evidence

```
[2025-10-23T02:52:16.816Z] [coder-002] INFO: Coding budget exceeded, triggering BUDGET_REVIEW
[2025-10-23T02:52:21.232Z] [coder-002] INFO: 🧑‍💻 Budget review approved, returning to origin state: CODING
[2025-10-23T02:52:21.232Z] [coder-002] INFO: 🧑‍💻 Starting coding phase for story_type 'app'
```

File rewrite count:
```bash
$ grep "cat > main.go <<" logs/run.log | wc -l
41
```

Context growth:
```
request tokens: 4,032  (initial)
request tokens: 5,558
request tokens: 8,758
...
request tokens: 19,606 (final)
```

## Proposed Fixes

### 1. Add Continuation Context to Budget Review Return
When returning from BUDGET_REVIEW to CODING, inject a message like:
```
"Budget approved. CONTINUE your previous work - DO NOT start over. Review what you've already done and complete any remaining tasks."
```

### 2. Improve CODING Phase Prompts
- Add instructions to check if work is already done before repeating it
- Encourage using `cat` to verify file state before overwriting
- Emphasize: "If files exist and are correct, move to testing"

### 3. Add Completion Detection
Before starting another CODING iteration, check:
- Do all required files exist?
- Are there any compilation errors?
- Do tests pass?
- If all yes → transition to TESTING or DONE

### 4. Context Management Improvements
- Consider summarizing or compacting repeated tool results
- Add context cleanup between budget review cycles
- Mark previous work as "already completed" in context

### 5. Template Updates Needed
Files to update:
- `pkg/templates/app_coding.tpl.md` - Add completion checks
- `pkg/templates/devops_coding.tpl.md` - Add completion checks
- `pkg/coder/budget_review.go` - Add continuation context when returning to CODING

## Next Steps

1. Fix failing tests (limiter tests need model configuration)
2. Commit current work
3. Implement fixes above
4. Test with a simple story to verify behavior
5. Monitor context growth and file rewrites

## Related Code Locations

- Budget review handler: `pkg/coder/budget_review.go:64`
- Coding phase start: `pkg/coder/coding.go` (search "Starting coding phase")
- Context manager: `pkg/contextmgr/`
- Templates: `pkg/templates/app_coding.tpl.md`, `pkg/templates/devops_coding.tpl.md`
