# Preventing Agent Thrashing in PLANNING State

## Problem Statement

### Observed Behavior
Coder agents can get stuck in PLANNING state, repeatedly executing the same failing commands over multiple budget review cycles. Specifically observed:

- **coder-001**: Attempted `git fetch origin main` and `git checkout main` 9-10 times per budget cycle
- Each command failed with read-only filesystem errors
- Budget reviews occurred 141 times without breaking the loop
- Architect approved continuation with inappropriate feedback (code quality praise instead of addressing the loop)
- Meanwhile, **coder-002** successfully completed a complex story using appropriate read-only commands

### Root Causes

1. **Unclear Planning Purpose**: Templates say "READ-ONLY access" but don't explain:
   - Why it's read-only
   - That workspace is already up-to-date from SETUP
   - That git operations are unnecessary and will fail

2. **Architect Missing Context**: Budget review responses were generic because:
   - Automated pattern detection existed but wasn't shown to architect
   - Pattern detection was platform-specific (only checked: go mod init, npm install, make build, go build)
   - No universal metrics for failure rates or repeated commands

3. **Inadequate Template Guidance**: Budget review templates didn't instruct architect to:
   - Look for repetitive failures in recent context
   - Recognize when agent is stuck vs making progress
   - Provide specific corrective guidance vs generic approval

## Solution Approach

### Principles

1. **Universal Metrics Only**: Platform-agnostic detection that works for any toolchain
2. **LLM-Based Interpretation**: Let the architect LLM analyze the actual errors, not hardcoded pattern matching
3. **Surface the Data**: Ensure architect sees failure rates and patterns
4. **Clear Guidance**: Templates explicitly instruct what to look for and how to respond

### Changes Required

## 1. Code Changes

### A. Replace Platform-Specific Detection with Universal Metrics

**File**: `pkg/coder/driver.go:637-726`

**Current**: `detectIssuePattern()` checks for specific commands (go mod init, npm install, etc.)

**New**: Universal metrics that work for any platform:

```go
// detectIssuePattern analyzes recent activity using universal, platform-agnostic metrics.
func (c *Coder) detectIssuePattern() string {
	if c.contextManager == nil {
		return "Cannot analyze - no context manager"
	}

	messages := c.contextManager.GetMessages()
	if len(messages) < 3 {
		return "Insufficient activity to analyze patterns"
	}

	var toolCalls []toolCall

	// Look at last 10 messages for patterns
	start := len(messages) - 10
	if start < 0 {
		start = 0
	}

	// Extract tool calls with success/failure status
	for i := start; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == roleToolMessage {
			content := msg.Content

			// Extract command if present
			var command string
			if strings.Contains(content, "Command:") || strings.Contains(content, "command:") {
				lines := strings.Split(content, "\n")
				for _, line := range lines {
					if strings.Contains(strings.ToLower(line), "command:") {
						parts := strings.SplitN(line, ":", 2)
						if len(parts) == 2 {
							command = strings.TrimSpace(parts[1])
						}
						break
					}
				}
			}

			// Determine if this tool call failed
			failed := strings.Contains(content, "exit_code: 1") ||
				strings.Contains(content, "exit_code: 127") ||
				strings.Contains(content, "exit_code: 255") ||
				strings.Contains(strings.ToLower(content), "error:") ||
				strings.Contains(strings.ToLower(content), "failed:")

			toolCalls = append(toolCalls, toolCall{
				command: command,
				failed:  failed,
				content: content,
			})
		}
	}

	if len(toolCalls) == 0 {
		return "No tool calls to analyze"
	}

	// Calculate universal metrics
	var issues []string

	// 1. Tool failure rate
	failedCount := 0
	for _, tc := range toolCalls {
		if tc.failed {
			failedCount++
		}
	}
	failureRate := float64(failedCount) / float64(len(toolCalls))

	if failureRate > 0.5 {
		issues = append(issues, fmt.Sprintf("High tool failure rate: %d/%d tool calls failed (%.0f%%)",
			failedCount, len(toolCalls), failureRate*100))
	}

	// 2. Identical consecutive failing commands
	for i := 1; i < len(toolCalls); i++ {
		prev := toolCalls[i-1]
		curr := toolCalls[i]

		if prev.command != "" && curr.command != "" &&
		   prev.command == curr.command &&
		   prev.failed && curr.failed {
			issues = append(issues, fmt.Sprintf("Repeated failing command detected: '%s' (same command failed consecutively)", prev.command))
			break // Only report once
		}
	}

	// Add strong guidance when issues detected
	if len(issues) > 0 {
		issues = append(issues, "**ALERT**: Significant issues detected that likely require NEEDS_CHANGES guidance or ABANDON may be appropriate.")
	} else {
		return fmt.Sprintf("Tool calls appear healthy (%d/%d successful)", len(toolCalls)-failedCount, len(toolCalls))
	}

	return strings.Join(issues, "\n")
}

type toolCall struct {
	command string
	failed  bool
	content string
}
```

**Add type definition** near top of file with other types.

### B. Include Pattern Analysis in Budget Review Content

**File**: `pkg/coder/driver.go:1224-1272`

**Current**: `buildBudgetReviewContent()` doesn't include `detectIssuePattern()` output

**Change**: Add automated analysis section, remove unreliable state fields

```go
func (c *Coder) buildBudgetReviewContent(sm *agent.BaseStateMachine, origin proto.State, iterationCount, budget int) string {
	// Basic budget info
	header := fmt.Sprintf("Loop budget exceeded in %s state (%d/%d iterations). How should I proceed?", origin, iterationCount, budget)

	// Get story and plan context
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))

	// Get truncated context messages
	contextMessages := c.getContextMessagesWithTokenLimit(budgetReviewContextTokenLimit)

	// Get automated pattern analysis
	issuePattern := c.detectIssuePattern()

	// Build comprehensive content
	content := fmt.Sprintf(`## Budget Review Request
%s

## Story Context
**Story ID:** %s
**Story Type:** %s

### Story Requirements
%s

## Implementation Plan
%s

## Automated Pattern Analysis
%s

## Recent Context (%d messages, ≤%d tokens)
`+"```"+`
%s
`+"```"+`

Please analyze the recent context and automated findings to determine if the agent is making progress or stuck in a loop. Provide specific guidance.`,
		header,
		storyID, storyType,
		taskContent,
		plan,
		issuePattern,
		len(contextMessages.Messages), budgetReviewContextTokenLimit,
		contextMessages.Content)

	return content
}
```

**Remove**: Lines referencing `KeyFilesCreated`, `KeyTestsPassed` (unreliable/unnecessary)

## 2. Template Changes

### A. Update Planning Templates

**Files**:
- `pkg/templates/app_planning.tpl.md` (line 4)
- `pkg/templates/devops_planning.tpl.md` (line 4)

**Current**:
```markdown
You are a coding agent with READ-ONLY access to the codebase during planning.
```

**New**:
```markdown
You are a coding agent assigned to PLAN the work to be done developing a story. During the planning stage, you will have read-only access to the codebase and filesystem. Once the plan is approved, it will be developed by a separate agent with full access.

**IMPORTANT: Your workspace is already up-to-date.** During SETUP, the system cloned/updated the repository to the correct branch. You do NOT need to run git operations (fetch, pull, checkout, clone). These operations will fail with "read-only file system" errors. Focus on exploration using read-only commands: `ls`, `cat`, `find`, `grep`, `tree`, `docker --version`.
```

### B. Update Budget Review Planning Template

**File**: `pkg/templates/budget_review_planning.tpl.md`

**Add after line 23** (after "Common Planning Issues" section):

```markdown
## Automated Pattern Analysis

The budget review request includes automated detection of universal failure patterns:

1. **High Tool Failure Rate**: If >50% of recent tool calls failed, the agent's approach is fundamentally flawed
2. **Repeated Failing Commands**: If the exact same command failed consecutively, the agent is stuck in a loop

**These metrics are platform-agnostic** and indicate serious issues regardless of technology stack.

### How to Respond to Detected Patterns

**If automated analysis shows "ALERT" or significant issues**:
1. Examine the "Recent Context" section to see the actual commands and errors
2. Identify WHY the commands are failing (read-only filesystem? missing dependencies? wrong approach?)
3. Use **NEEDS_CHANGES** status with specific corrective guidance:
   - "Stop attempting [specific command] - it fails because [specific reason]"
   - "The workspace is already current from SETUP. Git operations are unnecessary and will fail in read-only mode."
   - "You need to use [alternative approach] instead"

**If no automated patterns detected but high iteration count**:
- Agent may be over-exploring without focusing on plan creation
- Guide agent to synthesize findings and submit plan with `submit_plan` tool

**Never approve continuation** when automated analysis detects repeated failures without addressing the root cause.
```

### C. Update Budget Review Coding Template

**File**: `pkg/templates/budget_review_coding.tpl.md`

**Add after line 60** (before "## Decision Options"):

```markdown
## Automated Pattern Analysis

The budget review request includes automated detection of universal failure patterns:

1. **High Tool Failure Rate**: If >50% of recent tool calls failed, the implementation approach is not working
2. **Repeated Failing Commands**: If the exact same command failed consecutively, the agent is stuck

**These metrics are platform-agnostic** and indicate serious issues regardless of technology stack.

### How to Respond to Detected Patterns

**If automated analysis shows "ALERT" or significant issues**:
1. Examine the "Recent Context" to understand what's failing and why
2. Determine if it's:
   - Build/test failures that need code changes
   - Missing dependencies that need container updates
   - Wrong tool usage that needs different approach
3. Use **NEEDS_CHANGES** with specific implementation guidance to break the loop

**Example good feedback for repeated failures**:
- "Your tests are failing because [specific reason]. Try [specific fix]."
- "Container is missing [dependency]. Modify Dockerfile to add it, then use container_build."
- "Stop using [tool] - it's not available. Use [alternative tool] instead."

**Never approve continuation** when automated analysis detects patterns without providing corrective guidance.
```

### D. Add Immediate Feedback for Planning Failures

**File**: `pkg/coder/driver.go`

**Add new helper method** (near other context management methods):

```go
// addPlanningReminder adds a reminder message when tools fail in PLANNING state.
// This provides immediate feedback that the agent is in read-only exploration mode.
func (c *Coder) addPlanningReminder() {
	reminder := `⚠️ REMINDER: You are in PLANNING state with read-only access. Your task is to explore the codebase and create an implementation plan, not to modify files or run implementation commands.

Key points:
- Git operations (fetch, pull, checkout) are unnecessary - the workspace is already up-to-date from SETUP
- Use read-only commands: ls, cat, find, grep, tree
- Focus on understanding the code structure and requirements
- Create a comprehensive plan using submit_plan when ready

If you're uncertain how to proceed or repeatedly encountering errors, use the ask_question tool to get guidance from the architect.`

	c.contextManager.AddMessage("user", reminder)
	c.logger.Debug("Added planning state reminder after tool failure")
}
```

**Update `addShellResultToContext()`** (line ~1118):

Add after the existing `c.contextManager.AddMessage(roleToolMessage, feedback.String())`:

```go
// Add planning reminder if command failed in PLANNING state
if exitCode != 0 && c.GetCurrentState() == StatePlanning {
	c.addPlanningReminder()
}
```

**Update `addToolResultToContext()`** (line ~1073):

In the `else` branch handling non-shell tools, add after `c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("%s operation failed", toolCall.Name))`:

```go
// Add planning reminder if tool failed in PLANNING state
if c.GetCurrentState() == StatePlanning {
	c.addPlanningReminder()
}
```

**Rationale**:
- Provides immediate feedback loop when agent does something wrong in PLANNING
- Reminder appears in very next LLM call (not waiting for budget review)
- Suggests `ask_question` tool as collaborative escape hatch
- No self-destruct button - keeps abandonment decision with architect
- Low noise - only triggers when tools actually fail

## Implementation Checklist

- [ ] Update `detectIssuePattern()` with universal metrics (pkg/coder/driver.go:637-726)
- [ ] Add `toolCall` type definition (pkg/coder/driver.go)
- [ ] Update `buildBudgetReviewContent()` to include pattern analysis (pkg/coder/driver.go:1224-1272)
- [ ] Remove `KeyFilesCreated` and `KeyTestsPassed` from budget review content
- [ ] Add `addPlanningReminder()` helper method (pkg/coder/driver.go)
- [ ] Update `addShellResultToContext()` to call reminder on failures (pkg/coder/driver.go:~1118)
- [ ] Update `addToolResultToContext()` to call reminder on failures (pkg/coder/driver.go:~1090)
- [ ] Update `app_planning.tpl.md` introduction (line 4)
- [ ] Update `devops_planning.tpl.md` introduction (line 4)
- [ ] Add pattern analysis section to `budget_review_planning.tpl.md` (after line 23)
- [ ] Add pattern analysis section to `budget_review_coding.tpl.md` (after line 60)
- [ ] Test with known thrashing scenario (coder attempting git operations in PLANNING)
- [ ] Verify immediate planning reminder appears after first failure
- [ ] Verify architect provides specific corrective guidance when patterns detected

## Expected Outcomes

### Before Changes
- Agent tries `git fetch` 10 times → budget review → "Your code looks good!" → repeat
- 141 budget reviews without breaking the loop
- No recognition of repetitive failure pattern

### After Changes

**Immediate feedback (first layer)**:
- Agent tries `git fetch` → fails with read-only error
- **System immediately adds planning reminder** to context
- Next LLM call sees: "⚠️ REMINDER: You are in PLANNING state with read-only access... Git operations are unnecessary... If uncertain, use ask_question tool"
- Agent has opportunity to self-correct before hitting budget limit

**Budget review (second layer, if thrashing continues)**:
- If agent ignores reminder and tries `git fetch` again → automated pattern detects "Repeated failing command"
- Budget review shows: **ALERT**: "High tool failure rate: 8/10 failed (80%); Repeated failing command detected: 'git fetch origin main'; **ALERT**: Significant issues detected"
- Architect sees alert + recent errors showing read-only filesystem + planning reminders
- Architect responds: **NEEDS_CHANGES** with "Stop attempting git fetch - workspace is already up-to-date from SETUP. These operations fail in read-only planning mode. Focus on exploration using: ls, cat, find, grep"
- Agent receives authoritative guidance and must change approach

**Result**: Two-layer defense prevents thrashing - immediate feedback for quick self-correction, budget review with strong signals for architect intervention if needed.

## Rationale

### Why Universal Metrics?
Platform-agnostic system needs platform-agnostic detection. Hardcoding specific commands (go mod init, npm install) doesn't scale to Python, Rust, JavaScript, infrastructure, etc.

### Why Not More Sophisticated Detection?
The LLM (especially o3-mini) is better at interpreting context than any hardcoded heuristics. Our job is to:
1. Measure universal signals (failure rate, repetition)
2. Surface the data (include in budget review content)
3. Guide the LLM to look for these signals

### Why Strong Alerting Language?
The "ALERT" prefix and explicit "NEEDS_CHANGES or ABANDON" suggestion signals to the LLM that automated tripwires indicate serious problems requiring intervention, not just "interesting data."

### Why Remove Files Created / Tests Passed?
These state fields are unreliable and don't add value. If files were created or tests passed, it will be evident in the recent context. Including unreliable data confuses the architect.

### Why Suggest `ask_question` Instead of Self-Destruct Button?
**Considered**: Adding an `abandon_task` tool for agents to self-destruct when stuck.

**Decision**: Suggest `ask_question` tool instead because:
1. **Authority hierarchy**: Architect (human-supervised) should make abandonment decisions, not the agent
2. **Premature surrender risk**: Agent might give up when just slightly confused, wasting progress
3. **Collaborative approach**: `ask_question` escalates to architect who can provide guidance OR decide to abandon
4. **Already exists**: `ask_question` is already available in `AppPlanningTools` and `DevOpsPlanningTools`
5. **Lower stakes**: Asking for help is reversible, abandoning is not
6. **Budget review safety net**: If agent doesn't ask and keeps thrashing, budget review catches it anyway

The immediate planning reminder guides stuck agents toward `ask_question` as the appropriate escalation path.

## Additional Issue: Git State Confusion

### Problem

Agents in PLANNING were running git commands (`git log`, `git rev-parse HEAD`, `git ls-tree`) and getting confusing results:
- "fatal: your current branch has no commits yet"
- "fatal: ambiguous argument 'HEAD': unknown revision"

This caused agents to incorrectly conclude the workspace wasn't set up properly, even though files were present and accessible.

### Root Cause

The workspace uses a **fresh working branch model**:
- Each story gets a new branch (e.g., `maestro-story-coder-002`)
- The branch starts with no commits
- Files are present from the base/parent branch
- Git commands focused on commit history fail or return confusing output

Agents interpreted "no commits on branch" as "no files in workspace" and spammed chat with "CRITICAL ISSUE" messages.

### Solution

Added **GIT STATE NOTE** to both planning templates explaining:
- You're in a fresh working branch with no git history
- Git history commands will fail/show "no commits" - this is expected
- Workspace files ARE present and ready to explore
- Focus on filesystem exploration (`ls`, `find`, `cat`), not git history

This explains the model truthfully rather than just discouraging git commands.

### Files Updated
- `pkg/templates/app_planning.tpl.md` (lines 9)
- `pkg/templates/devops_planning.tpl.md` (lines 9)

## Testing Strategy

1. **Test immediate feedback (first layer)**:
   - Agent in PLANNING attempts `git fetch`
   - Verify command fails with read-only error
   - Verify planning reminder immediately appears in context
   - Verify next LLM call sees the reminder
   - Verify agent can self-correct after seeing reminder

2. **Test budget review escalation (second layer)**:
   - Agent ignores reminder and continues trying `git fetch`
   - Verify automated pattern detection triggers after 2+ failures
   - Budget review should show ALERT with specific patterns
   - Architect should provide NEEDS_CHANGES with corrective guidance
   - Agent should change approach after receiving guidance

3. **Test `ask_question` escalation**:
   - Agent uses `ask_question` after seeing planning reminder
   - Verify question reaches architect
   - Verify architect can provide guidance or decide to abandon

4. **Test git state confusion prevention**:
   - Agent runs `git log` or `git rev-parse HEAD`
   - Commands fail with "no commits" message
   - Verify agent reads GIT STATE NOTE in prompt
   - Verify agent proceeds with filesystem exploration instead of panicking
   - Verify no "CRITICAL ISSUE" messages about workspace not being set up

5. **Verify no false positives**:
   - Normal exploration (multiple `cat`, `find` commands that succeed) should not trigger reminders
   - Single failures followed by different commands should not trigger budget alerts
   - Expected failures (testing error handling) should not cause false alarms if not repetitive

## Maintenance Notes

If extending automated detection in the future:
- Keep metrics universal (work for any platform)
- Don't try to interpret WHAT commands mean
- Let LLM handle interpretation with proper context
- Focus detection on behavioral patterns: repetition, failure rates, stuck loops
