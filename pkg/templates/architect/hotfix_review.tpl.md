# Hotfix Requirements Review

The PM has submitted the following hotfix requirements for immediate implementation.

## Requirements

```
{{.TaskContent}}
```

## Your Task

Review these hotfix requirements before they are queued for implementation. You may:

**Explore the codebase (recommended):**
- Use `read_file` to inspect existing code, configuration, and Dockerfiles
- Use `list_files` to discover relevant files and understand project structure
- **Check if the requested work is already done** - if so, approve with feedback noting it

**Complete your review (required):**
- Use `review_complete` with your decision when finished

## Review Guidelines

**Check for:**
1. **Clarity**: Are requirements clear and unambiguous?
2. **Completeness**: Is enough information provided for a coder to implement without planning?
3. **Feasibility**: Are requirements technically feasible?
4. **Already Complete**: Has the work already been done? Check the codebase for evidence.
5. **Scope**: Are these appropriately scoped as hotfixes (small, focused changes)?

**Important - App vs DevOps/Container Requirements:**
- For **app requirements** (code changes, configuration): You CAN fully verify if work is already done by inspecting the codebase
- For **devops/container requirements** (Dockerfile changes, tool installation): You can inspect the Dockerfile and configuration but **cannot verify running container state** (e.g., whether a tool is actually installed in a built image). Note this limitation in your feedback so the coder knows to validate.

**Decision Criteria:**

- **APPROVED**: Requirements are ready for implementation
  - Requirements are clear and implementable
  - Even if work appears already done, approve and note it - the coder will verify and report completion if no changes are needed

- **NEEDS_CHANGES**: Requirements need revision
  - Provide specific, actionable feedback
  - Point out ambiguities or missing information
  - Suggest scope adjustments if requirements are too broad for a hotfix

- **REJECTED**: Requirements have fundamental issues
  - Use only for requirements that are completely inadequate or infeasible
  - Provide clear explanation of why

## Instructions

1. Read through all requirements carefully
2. Explore the codebase to check if work is already done or to understand context
3. Make your decision: APPROVED, NEEDS_CHANGES, or REJECTED
4. Call `review_complete` with your status and feedback

**Important**: Hotfixes are meant to be fast. Keep your review focused and concise. If requirements are reasonable and clear, approve them.
