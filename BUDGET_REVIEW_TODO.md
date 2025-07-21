# BUDGET_REVIEW Future Enhancements

*Created: 2025-07-20*

This document outlines planned improvements to the BUDGET_REVIEW state to make it more intelligent and context-aware.

## Current State

BUDGET_REVIEW currently uses hard-coded iteration limits:
- `planning_iterations` budget (e.g., 3)
- `coding_iterations` budget (e.g., 5) 
- When exceeded → architect approval required

Architect receives basic context:
- Origin state
- Current iteration count  
- Maximum allowed iterations

## Planned Enhancements

### 1. Enhanced Context for Architect

**Current payload:**
```go
requestMsg.SetPayload("origin", string(origin))
requestMsg.SetPayload("loops", iterationCount)
requestMsg.SetPayload("max_loops", budget)
```

**Enhanced payload:**
```go
requestMsg.SetPayload("origin", string(origin))
requestMsg.SetPayload("loops", iterationCount) 
requestMsg.SetPayload("max_loops", budget)
requestMsg.SetPayload("coding_mode", sm.GetStateValue("coding_mode"))     // NEW
requestMsg.SetPayload("work_context", generateWorkSummary(sm))            // NEW
requestMsg.SetPayload("progress_trend", analyzeProgressTrend(sm))         // NEW
requestMsg.SetPayload("blockers", identifyCurrentBlockers(sm))            // NEW
```

**Work context examples:**
- "Fixing test failures: go.mod import path mismatch"
- "Addressing code review: need better error handling in HTTP client"  
- "Resolving merge conflicts: conflicting dependencies in package.json"
- "Initial implementation: 60% complete, working on authentication layer"

### 2. Intelligent Budget Adjustment

**Dynamic budgets based on work type:**
```go
type BudgetConfig struct {
    InitialCoding    int // 5 iterations for new features
    TestFixing       int // 3 iterations for test failures  
    ReviewFixing     int // 4 iterations for code review issues
    MergeConflicts   int // 2 iterations for conflicts (usually quick)
    Planning         int // 3 iterations for planning/replanning
}
```

**Adaptive budget increases:**
- Architect can grant additional iterations: "Continue for 2 more attempts"
- System learns patterns: "Merge conflicts in this codebase typically need 3-4 iterations"

### 3. Progress Trend Analysis

**Track progress indicators:**
```go
type ProgressMetrics struct {
    FilesChanged      int
    TestsFixed        int  
    LintErrors        int
    BuildStatus       string
    CodeCoverage      float64
    LastError         string
    TimeInState       time.Duration
}
```

**Trend analysis:**
- "Making progress: 3 test failures → 1 test failure → all tests passing"
- "Stuck: same error for 3 iterations, may need escalation"
- "Regression: introduced new failures while fixing others"

### 4. Architectural Decision Points

**Questions for architect with context:**
- "Agent has been fixing import errors for 4 iterations. Continue (2 more), Pivot (change approach), or Escalate (manual intervention)?"
- "Code review feedback addressed but introduced new test failures. Continue fixing, or escalate for review?"
- "Merge conflicts resolved but build still failing. Approach isn't working - should we try different strategy?"

### 5. Learning and Adaptation

**Future AI-driven insights:**
- Historical analysis: "Similar test failures in this codebase typically resolved in 2-3 iterations"  
- Pattern recognition: "Go modules issues often require specific commands not in current toolset"
- Proactive suggestions: "Based on error pattern, suggest running 'go mod tidy' before continuing"

## Implementation Priority

**Phase 1** (Current Sprint):
- [x] Use hard-coded limits for now
- [ ] Add `coding_mode` context to BUDGET_REVIEW requests
- [ ] Improve work summary in BUDGET_REVIEW context

**Phase 2** (Next Sprint):
- [ ] Implement progress trend analysis
- [ ] Add dynamic budget adjustment based on work type
- [ ] Enhanced blocker identification

**Phase 3** (Future):
- [ ] Historical pattern analysis
- [ ] AI-driven progress prediction
- [ ] Automated budget optimization

## Configuration

Future config structure:
```json
{
  "budget_review": {
    "budgets": {
      "initial_coding": 5,
      "test_fixing": 3, 
      "review_fixing": 4,
      "merge_conflicts": 2,
      "planning": 3
    },
    "analysis": {
      "track_progress_metrics": true,
      "enable_trend_analysis": true,
      "learn_from_history": false
    }
  }
}
```

## Notes

- BUDGET_REVIEW remains distinct from CODE_REVIEW (progress vs quality assessment)
- Architect retains full decision-making authority
- System provides increasingly sophisticated context to support decisions
- Gradual enhancement path allows testing and refinement

---

*This is a living document - update as implementation progresses*