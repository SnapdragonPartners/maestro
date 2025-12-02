# Maintenance Mode Specification

## Overview

Maintenance Mode is an automated technical debt management system that runs periodically between specs. It performs both programmatic housekeeping tasks and LLM-driven maintenance stories to keep the codebase healthy without human intervention.

### Design Principles

1. **Autonomous** - Runs without human oversight, produces summary report
2. **Safe** - Only makes low-risk changes (documentation, tests, cleanup)
3. **Parallelizable** - LLM stories run concurrently for efficiency
4. **Language-agnostic** - No assumptions about specific toolchains
5. **Incremental** - Small, regular maintenance prevents debt accumulation

### When It Runs

Maintenance triggers after:
1. A configurable number of specs have completed (`after_specs`, default: 1)
2. UAT is complete for the most recent spec
3. All story PRs from the spec are merged

## Bootstrap Enhancements

During project bootstrap (`maestro init`), the orchestrator configures GitHub integrations programmatically. These are one-time setup tasks that require no LLM.

### GitHub API Configuration

```go
// Enable dependabot security updates
gh api -X PUT /repos/{owner}/{repo}/automated-security-fixes

// Enable vulnerability alerts
gh api -X PUT /repos/{owner}/{repo}/vulnerability-alerts

// Enable auto-merge repository setting
gh api -X PATCH /repos/{owner}/{repo} -f allow_auto_merge=true
```

### Generated Workflow Files

#### `.github/dependabot.yml`

Ecosystem detected from `config.Platform`:

| Platform | Ecosystem |
|----------|-----------|
| go | gomod |
| node | npm |
| python | pip |
| rust | cargo |
| java | maven |

```yaml
version: 2
updates:
  - package-ecosystem: "${ECOSYSTEM}"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 10
```

#### `.github/workflows/ci.yml`

Uses detected `config.TestTarget`:

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Run tests
        run: ${TEST_TARGET}  # e.g., "make test"
```

#### `.github/workflows/dependabot-auto-merge.yml`

```yaml
name: Dependabot Auto-merge
on: pull_request

permissions:
  contents: write
  pull-requests: write

jobs:
  dependabot:
    runs-on: ubuntu-latest
    if: github.actor == 'dependabot[bot]'
    steps:
      - name: Dependabot metadata
        id: metadata
        uses: dependabot/fetch-metadata@v2
        with:
          github-token: "${{ secrets.GITHUB_TOKEN }}"

      - name: Enable auto-merge for patch updates
        if: steps.metadata.outputs.update-type == 'version-update:semver-patch'
        run: gh pr merge --auto --merge "$PR_URL"
        env:
          PR_URL: ${{ github.event.pull_request.html_url }}
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

## Programmatic Maintenance Tasks

These tasks run at the orchestrator level without LLM involvement. Agents are unaware of them.

### Stale Branch Cleanup

Deletes branches that are fully merged to main:

```go
func cleanupStaleBranches(ctx context.Context, cfg *config.Config) ([]string, error) {
    // Get all remote branches
    branches := gh api repos/{owner}/{repo}/branches --paginate

    var deleted []string
    for _, branch := range branches {
        // Skip protected branches
        if matchesProtectedPattern(branch.Name, cfg.Maintenance.BranchCleanup.ProtectedPatterns) {
            continue
        }

        // Check if fully merged to main
        if isMergedToMain(branch.Name) {
            gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/{branch.Name}
            deleted = append(deleted, branch.Name)
        }
    }

    return deleted, nil
}
```

**Protected patterns** (configurable):
- `main`, `master`
- `develop`
- `release/*`

## LLM Maintenance Stories

Five parallel stories run during each maintenance cycle. Each follows the standard story workflow (planning → coding → testing → review → merge).

### Story A: Knowledge Graph Sync

```markdown
# Story: Synchronize Knowledge Graph

## Description
Verify that the knowledge graph (`.maestro/knowledge.dot`) accurately reflects
the current state of the codebase. Update nodes that reference moved, renamed,
or deleted code.

## Acceptance Criteria
- [ ] Parse and validate the knowledge graph
- [ ] For nodes with `path` attributes, verify the path exists
- [ ] For nodes with `example` attributes, verify the pattern exists in code
- [ ] Mark nodes referencing deleted code as `status="deprecated"`
- [ ] Update `path` attributes for renamed/moved files
- [ ] Remove or update stale nodes that no longer apply
- [ ] Graph remains valid DOT format after changes

## Constraints
- Only modify `.maestro/knowledge.dot`
- Do not create new nodes (that's done during feature development)
- Preserve all edges unless a referenced node is removed
```

### Story B: Documentation Verification

```markdown
# Story: Verify Documentation Accuracy

## Description
Ensure project documentation (README.md, wiki files, and other markdown)
is accurate and up-to-date. Fix broken links, outdated examples, and
incorrect instructions.

## Acceptance Criteria
- [ ] Verify all internal links resolve to existing files
- [ ] Verify external links are accessible (no 404s)
- [ ] Check code examples for syntax validity
- [ ] Verify installation/setup instructions are accurate
- [ ] Update outdated configuration examples
- [ ] Fix any factual inaccuracies found

## Scope
Focus on:
1. `README.md` (highest priority)
2. `docs/` directory markdown files
3. `.maestro/*.md` files (agent prompts)

## Constraints
- Documentation changes only (no code changes)
- Preserve existing documentation structure
- Do not add new sections unless fixing missing critical info
```

### Story C: TODO/Deprecated Scan

```markdown
# Story: Scan for TODOs and Deprecated Code

## Description
Scan the codebase for TODO comments, FIXME markers, and deprecated code
annotations. Generate a summary report for the project maintainer.

## Acceptance Criteria
- [ ] Scan all source files for configured markers
- [ ] Group findings by type (TODO, FIXME, HACK, deprecated)
- [ ] Include file path, line number, and surrounding context
- [ ] Identify TODOs that reference completed work (can be removed)
- [ ] Generate markdown report in `.maestro/maintenance-reports/`

## Markers to Scan
- `TODO`
- `FIXME`
- `HACK`
- `XXX`
- `deprecated` / `DEPRECATED`
- `@deprecated`

## Output Format
```markdown
# TODO/Deprecated Scan Report

**Scan Date**: {date}
**Files Scanned**: {count}

## Summary
- TODOs: {count}
- FIXMEs: {count}
- Deprecated: {count}

## Findings

### TODOs
| File | Line | Content | Status |
|------|------|---------|--------|
| pkg/agent/client.go | 145 | TODO: add retry logic | Active |
| pkg/tools/mcp.go | 89 | TODO: remove after v2 | Stale (v2 released) |

### Deprecated
...
```

## Constraints
- Read-only analysis (no code changes)
- Report only, no automatic fixes
- User can request hotfixes for high-priority items
```

### Story D: Deferred Node Review

```markdown
# Story: Review Deferred Knowledge Nodes

## Description
Review knowledge graph nodes marked as `status="future"` or `status="legacy"`
to determine if they can be promoted, updated, or removed.

## Acceptance Criteria
- [ ] Identify all nodes with status="future" or status="legacy"
- [ ] For each node, assess if blockers are resolved
- [ ] Promote unblocked nodes to status="current"
- [ ] Mark obsolete nodes for removal (superseded by different approach)
- [ ] Generate report for nodes that remain blocked

## Assessment Criteria
- **Promote to current**: Referenced component now exists, blocker resolved
- **Mark obsolete**: Feature implemented differently, node no longer relevant
- **Remain deferred**: Blockers still exist, dependencies not ready

## Output
- Updated `.maestro/knowledge.dot` with promotions
- Report section listing nodes that remain blocked with reasons

## Constraints
- Only modify node `status` attribute
- Do not change node descriptions or other attributes
- Document reasoning for each status change in PR description
```

### Story E: Test Coverage Improvement

```markdown
# Story: Improve Test Coverage

## Description
Analyze the codebase to identify areas with low or missing unit test coverage.
Create or enhance test suites for the highest-value untested code.

## Scope
Select **3-5 coverage targets** (packages, modules, or logical components)
to focus on. Prioritize by:
1. Code that is frequently used or imported
2. Public APIs and exported functions
3. Complex logic with multiple code paths
4. Code lacking any existing tests

## Acceptance Criteria
- [ ] Identify 3-5 high-value coverage targets
- [ ] Create unit test files for each target
- [ ] Tests cover primary happy paths and basic error cases
- [ ] All new tests pass when running the project's standard test command
- [ ] New tests are automatically discovered by the existing test runner (no build system changes)
- [ ] Do not modify application code unless fixing a confirmed bug

## Bug Handling
If tests reveal what appears to be a code bug:
1. Report it to the architect using the question tool
2. Describe the expected vs actual behavior
3. Wait for confirmation before making any fix
4. If confirmed, fix the bug as part of this story

## Constraints
- Maximum 3-5 coverage targets per maintenance cycle
- Unit tests only (no integration tests, E2E tests, or external dependencies)
- Skip generated code, vendored dependencies, and test utilities
- Follow existing test file naming conventions (e.g., `*_test.go`, `*.test.js`, `test_*.py`)
- Focus on meaningful coverage, not line count
```

## PM Integration

### Maintenance Trigger Logic

```go
type MaintenanceState struct {
    SpecsCompleted      int
    LastMaintenanceTime time.Time
    MaintenanceRunning  bool
}

func (pm *PM) OnSpecUATComplete(specID string) {
    pm.maintenance.SpecsCompleted++

    if pm.maintenance.SpecsCompleted >= pm.config.Maintenance.AfterSpecs {
        pm.triggerMaintenanceCycle()
    }
}

func (pm *PM) triggerMaintenanceCycle() {
    pm.maintenance.MaintenanceRunning = true
    pm.maintenance.SpecsCompleted = 0

    // 1. Run programmatic tasks (orchestrator level)
    programmaticReport := runProgrammaticMaintenance()

    // 2. Generate maintenance spec with fixed stories
    maintenanceSpec := generateMaintenanceSpec()

    // 3. Dispatch stories (parallel execution)
    dispatchMaintenanceStories(maintenanceSpec)

    // 4. On completion, generate summary report
    // 5. Present report to user
    // 6. pm.maintenance.MaintenanceRunning = false
}
```

### Maintenance Spec Generation

The PM generates a fixed maintenance spec (not from user input):

```go
func generateMaintenanceSpec() *Spec {
    return &Spec{
        ID:          fmt.Sprintf("maintenance-%s", time.Now().Format("2006-01-02")),
        Title:       "Automated Maintenance Cycle",
        Type:        SpecTypeMaintenance,
        Stories:     getMaintenanceStories(), // Fixed set of 5 stories
        AutoMerge:   true,                    // PRs auto-merge after CI passes
        SkipUAT:     true,                    // No UAT for maintenance
    }
}
```

### Summary Report

After maintenance completes, PM presents:

```markdown
# Maintenance Report - {date}

## Programmatic Tasks

### Branch Cleanup
- Deleted 12 stale branches: `feature/old-feature`, `fix/resolved-bug`, ...

## Story Results

### Knowledge Graph Sync (PR #123)
- Updated 3 node paths
- Deprecated 2 nodes referencing deleted code
- Status: Merged

### Documentation Verification (PR #124)
- Fixed 5 broken links
- Updated 2 outdated examples
- Status: Merged

### TODO/Deprecated Scan
- **TODOs found**: 15
  - 5 appear to reference completed work
  - 3 are high-priority (FIXME)
- **Deprecated markers**: 3
- Full report: `.maestro/maintenance-reports/todo-scan-{date}.md`

### Deferred Node Review (PR #125)
- Promoted 1 node to current: "caching-layer"
- Nodes still blocked: 2
- Status: Merged

### Test Coverage (PR #126)
- Added tests for 4 coverage targets
- New test files: 6
- Status: Merged

## Suggested Actions

Based on the TODO scan, would you like to generate hotfixes for:
1. [ ] Remove 5 stale TODOs (completed work)
2. [ ] Address FIXME in `pkg/agent/client.go:145`
3. [ ] Update deprecated code in `pkg/legacy/handler.go`

Reply with numbers to generate hotfix specs, or "skip" to continue.
```

## Configuration

### Full Schema

```json
{
  "maintenance": {
    "enabled": true,
    "after_specs": 1,
    "tasks": {
      "branch_cleanup": true,
      "knowledge_sync": true,
      "docs_verification": true,
      "todo_scan": true,
      "deferred_review": true,
      "test_coverage": true
    },
    "branch_cleanup": {
      "protected_patterns": ["main", "master", "develop", "release/*", "hotfix/*"]
    },
    "todo_scan": {
      "markers": ["TODO", "FIXME", "HACK", "XXX", "deprecated", "DEPRECATED", "@deprecated"]
    }
  }
}
```

### Defaults

| Setting | Default | Description |
|---------|---------|-------------|
| `enabled` | `true` | Enable maintenance mode |
| `after_specs` | `1` | Specs before maintenance triggers |
| `tasks.*` | `true` | All tasks enabled by default |
| `branch_cleanup.protected_patterns` | See above | Branches to never delete |
| `todo_scan.markers` | See above | Comment markers to scan |

## Detailed Implementation Plan

Based on analysis of the existing codebase architecture, here is the detailed implementation plan organized by phase.

### Architecture Overview

The implementation follows existing patterns:
- **State Machine Pattern**: PM/Architect/Coder all use FSM with validated transitions
- **Tool-Based Communication**: LLM actions via typed tools with signal-based routing
- **Context Management**: Persistent conversation contexts per agent/task
- **Fire-and-Forget Persistence**: Async channel writes for loose coupling
- **Hotfix Mode Blueprint**: Maintenance mode follows the same autonomous execution pattern

### Key Integration Points

| Component | File | Integration |
|-----------|------|-------------|
| Architect State Machine | `pkg/architect/driver.go` | Add maintenance trigger after spec completion |
| Architect Queue | `pkg/architect/queue.go` | Track specs completed, detect all stories done |
| GitHub Client | `pkg/github/` | Centralized GitHub API operations (created in Phase 1) |
| Bootstrap | `pkg/bootstrap/workflows.go` | GitHub workflow generation (created in Phase 1) |
| Config | `pkg/config/config.go` | Maintenance config section (created in Phase 1) |
| Story Templates | `pkg/maintenance/stories.go` | Fixed maintenance story definitions |

---

### Phase 1: Configuration & Bootstrap (Stories 1-3) ✅ COMPLETE

#### Story 1: Maintenance Configuration Schema ✅

**Implementation Notes:**
- Added `MaintenanceConfig` struct to `pkg/config/config.go` with all fields
- Added defaults in `createDefaultConfig()` and `applyDefaults()`
- Added `TestMaintenanceConfigDefaults` test
- All task toggles default to `true` for opinionated defaults

---

#### Story 2: GitHub API Integration for Bootstrap ✅

**Implementation Notes:**
- Created centralized `pkg/github/` package with unified GitHub client wrapping `gh` CLI
- `pkg/github/client.go` - Core Client struct, URL parsing, API helpers
- `pkg/github/pr.go` - PR listing, creation, merging, auto-merge, comments
- `pkg/github/repo.go` - Repository settings, security features
- `pkg/github/branch.go` - Branch listing, deletion, merged branch cleanup
- Refactored `pkg/bootstrap/github.go` to use centralized package
- Added comprehensive integration tests in `pkg/github/integration_test.go`
- Fixed `EnablePRAutoMerge` to use GraphQL node ID (not PR number)

---

#### Story 3: Workflow File Generation ✅

**Implementation Notes:**
- Created `pkg/bootstrap/workflows.go` with `WorkflowGenerator`
- Generates `dependabot.yml` with platform-to-ecosystem mapping
- Generates `ci.yml` with platform-specific setup steps (Go, Node, Python, Rust, Java)
- Generates `dependabot-auto-merge.yml` for patch auto-merge
- Added comprehensive tests in `pkg/bootstrap/workflows_test.go`

---

### Phase 2: Architect Maintenance Tracking (Stories 4-5) ✅ COMPLETE

#### Story 4: Maintenance State in Architect

**Rationale:**
The architect is the correct location for maintenance tracking because:
1. Architect receives merge results from coders via `handleWorkAccepted()`
2. Architect tracks stories by spec via `Queue.stories[].SpecID`
3. Architect knows when all stories for a spec are done
4. PM only submits specs and doesn't track story completion

**Files to modify:**
- `pkg/architect/driver.go` - Add maintenance state tracking
- `pkg/architect/queue.go` - Add spec completion detection

**Implementation:**

```go
// pkg/architect/driver.go

// MaintenanceTracker tracks maintenance cycle state
type MaintenanceTracker struct {
    SpecsCompleted    int
    LastMaintenance   time.Time
    InProgress        bool
    mutex             sync.Mutex
}

// Add to Driver struct
type Driver struct {
    // ... existing fields ...
    maintenance MaintenanceTracker
}

// onSpecComplete is called when all stories for a spec are done
// Called from handleWorkAccepted when a story completes and queue detects spec completion
func (d *Driver) onSpecComplete(specID string) {
    d.maintenance.mutex.Lock()
    defer d.maintenance.mutex.Unlock()

    d.maintenance.SpecsCompleted++
    d.logger.Info("📊 Spec %s completed. Total specs completed: %d", specID, d.maintenance.SpecsCompleted)

    cfg, err := config.GetConfig()
    if err != nil || !cfg.Maintenance.Enabled {
        return
    }

    if d.maintenance.SpecsCompleted >= cfg.Maintenance.AfterSpecs {
        d.triggerMaintenanceCycle()
    }
}

// triggerMaintenanceCycle initiates maintenance mode
func (d *Driver) triggerMaintenanceCycle() {
    if d.maintenance.InProgress {
        d.logger.Info("🔧 Maintenance already in progress, skipping")
        return
    }

    d.maintenance.InProgress = true
    d.maintenance.SpecsCompleted = 0
    d.logger.Info("🔧 Triggering maintenance cycle")

    // Generate maintenance spec and add to queue
    spec := maintenance.GenerateSpec()
    d.dispatchMaintenanceStories(spec)
}

// pkg/architect/queue.go

// CheckSpecComplete returns true if all stories for a spec are done
func (q *Queue) CheckSpecComplete(specID string) bool {
    q.mutex.RLock()
    defer q.mutex.RUnlock()

    for _, story := range q.stories {
        if story.SpecID == specID && story.GetStatus() != StatusDone {
            return false
        }
    }
    return true
}

// GetSpecStoryCount returns total and completed story counts for a spec
func (q *Queue) GetSpecStoryCount(specID string) (total, completed int) {
    q.mutex.RLock()
    defer q.mutex.RUnlock()

    for _, story := range q.stories {
        if story.SpecID == specID {
            total++
            if story.GetStatus() == StatusDone {
                completed++
            }
        }
    }
    return
}
```

**Acceptance Criteria:**
- [x] MaintenanceTracker struct with counter and mutex
- [x] onSpecComplete() called when all stories for a spec are done
- [x] CheckSpecComplete() correctly detects spec completion
- [x] Trigger fires when threshold reached
- [x] Guard against multiple concurrent maintenance cycles
- [x] Counter resets after trigger
- [x] Unit tests for trigger conditions

**Implementation Notes:**
- Added `MaintenanceTracker` struct to `pkg/architect/driver.go`
- Created `pkg/architect/maintenance.go` with tracking functions:
  - `onSpecComplete()` - Called when all stories for a spec are done
  - `triggerMaintenanceCycle()` - Initiates maintenance cycle
  - `runMaintenanceTasks()` - Executes programmatic and LLM maintenance
  - `dispatchMaintenanceSpec()` - Converts maintenance stories to queue
  - `completeMaintenanceCycle()` - Marks cycle as complete
  - `GetMaintenanceStatus()` - Returns current cycle status
- Added spec completion detection to `pkg/architect/queue.go`:
  - `CheckSpecComplete()` - Returns true if all stories for spec are done
  - `GetSpecStoryCount()` - Returns total and completed story counts
  - `GetUniqueSpecIDs()` - Returns all unique spec IDs in queue
  - `AddMaintenanceStory()` - Adds maintenance story to queue
- Added `checkSpecCompletion()` hook in `handleWorkAccepted`
- Added `IsMaintenance` field to `persistence.Story` struct

---

#### Story 5: Maintenance Spec Generation ✅

**Files to create:**
- `pkg/maintenance/spec.go` - Maintenance spec generation
- `pkg/maintenance/stories.go` - Fixed story templates

**Implementation:**

```go
// pkg/maintenance/spec.go

package maintenance

import (
    "fmt"
    "time"
)

// SpecTypeMaintenance indicates an auto-generated maintenance spec
const SpecTypeMaintenance = "maintenance"

// GenerateSpec creates a maintenance spec with fixed stories
func GenerateSpec(cfg *config.MaintenanceConfig) *Spec {
    specID := fmt.Sprintf("maintenance-%s", time.Now().Format("2006-01-02-150405"))

    stories := make([]Story, 0)

    if cfg.Tasks.KnowledgeSync {
        stories = append(stories, KnowledgeSyncStory())
    }
    if cfg.Tasks.DocsVerification {
        stories = append(stories, DocsVerificationStory())
    }
    if cfg.Tasks.TodoScan {
        stories = append(stories, TodoScanStory(cfg.TodoScan.Markers))
    }
    if cfg.Tasks.DeferredReview {
        stories = append(stories, DeferredReviewStory())
    }
    if cfg.Tasks.TestCoverage {
        stories = append(stories, TestCoverageStory())
    }

    return &Spec{
        ID:            specID,
        Title:         "Automated Maintenance Cycle",
        Type:          SpecTypeMaintenance,
        Stories:       stories,
        AutoMerge:     true,   // PRs auto-merge after CI
        SkipUAT:       true,   // No UAT for maintenance
        IsMaintenance: true,
    }
}
```

```go
// pkg/maintenance/stories.go

package maintenance

// Story templates for maintenance tasks

func KnowledgeSyncStory() Story {
    return Story{
        ID:    "maint-knowledge-sync",
        Title: "Synchronize Knowledge Graph",
        Content: `Verify that the knowledge graph accurately reflects the current
state of the codebase. Update nodes that reference moved, renamed, or deleted code.

## Acceptance Criteria
- Parse and validate .maestro/knowledge.dot
- For nodes with path attributes, verify the path exists
- Mark nodes referencing deleted code as status="deprecated"
- Update path attributes for renamed/moved files
- Graph remains valid DOT format after changes

## Constraints
- Only modify .maestro/knowledge.dot
- Do not create new nodes
- Preserve edges unless referenced node is removed`,
        Express:       false,
        IsMaintenance: true,
    }
}

func DocsVerificationStory() Story {
    return Story{
        ID:    "maint-docs-verification",
        Title: "Verify Documentation Accuracy",
        Content: `Ensure project documentation is accurate and up-to-date.
Fix broken links, outdated examples, and incorrect instructions.

## Acceptance Criteria
- Verify all internal links resolve to existing files
- Verify external links are accessible
- Check code examples for syntax validity
- Update outdated configuration examples
- Fix any factual inaccuracies found

## Scope
- README.md (highest priority)
- docs/*.md files
- .maestro/*.md files

## Constraints
- Documentation changes only
- Preserve existing structure
- Do not add new sections unless fixing missing critical info`,
        Express:       false,
        IsMaintenance: true,
    }
}

func TodoScanStory(markers []string) Story {
    markerList := strings.Join(markers, ", ")
    return Story{
        ID:    "maint-todo-scan",
        Title: "Scan for TODOs and Deprecated Code",
        Content: fmt.Sprintf(`Scan the codebase for TODO comments and deprecated
code annotations. Generate a summary report.

## Markers to Scan
%s

## Acceptance Criteria
- Scan all source files for configured markers
- Group findings by type
- Include file path, line number, and context
- Identify TODOs that reference completed work
- Generate report in .maestro/maintenance-reports/

## Constraints
- Read-only analysis (no code changes)
- Report only, no automatic fixes`, markerList),
        Express:       true, // No planning needed for scan
        IsMaintenance: true,
    }
}

func DeferredReviewStory() Story {
    return Story{
        ID:    "maint-deferred-review",
        Title: "Review Deferred Knowledge Nodes",
        Content: `Review knowledge graph nodes marked as status="future" or
status="legacy" to determine if they can be promoted or removed.

## Acceptance Criteria
- Identify all nodes with status="future" or status="legacy"
- Assess if blockers are resolved for each
- Promote unblocked nodes to status="current"
- Mark obsolete nodes for removal
- Generate report for nodes that remain blocked

## Constraints
- Only modify node status attribute
- Document reasoning in PR description`,
        Express:       false,
        IsMaintenance: true,
    }
}

func TestCoverageStory() Story {
    return Story{
        ID:    "maint-test-coverage",
        Title: "Improve Test Coverage",
        Content: `Analyze the codebase to identify areas with low or missing
unit test coverage. Create test suites for high-value untested code.

## Scope
Select 3-5 coverage targets (packages, modules, or components).
Prioritize by:
1. Frequently used/imported code
2. Public APIs and exported functions
3. Complex logic with multiple code paths
4. Code lacking any existing tests

## Acceptance Criteria
- Identify 3-5 high-value coverage targets
- Create unit test files for each target
- Tests cover happy paths and basic error cases
- All new tests pass with standard test command
- New tests auto-discovered by test runner (no build changes)

## Bug Handling
If tests reveal a code bug:
1. Report to architect using question tool
2. Describe expected vs actual behavior
3. Wait for confirmation before fixing

## Constraints
- Maximum 3-5 coverage targets
- Unit tests only (no integration/E2E tests)
- Follow existing test file naming conventions
- Focus on meaningful coverage, not line count`,
        Express:       false,
        IsMaintenance: true,
    }
}
```

**Acceptance Criteria:**
- [x] GenerateSpec() creates valid maintenance spec
- [x] All 5 story templates defined with acceptance criteria
- [x] Stories conditionally included based on config.Tasks
- [x] AutoMerge and SkipUAT flags set correctly
- [x] IsMaintenance flag propagates to story routing
- [x] Unit tests for spec generation

**Implementation Notes:**
- Created `pkg/maintenance/spec.go` with:
  - `Spec` struct for maintenance specifications
  - `Story` struct for maintenance stories
  - `GenerateSpec()` - Creates spec with stories based on config
  - `GenerateSpecWithID()` - Creates spec with custom ID
- Created `pkg/maintenance/stories.go` with story templates:
  - `KnowledgeSyncStory()` - Knowledge graph synchronization
  - `DocsVerificationStory()` - Documentation accuracy verification
  - `TodoScanStory()` - TODO/deprecated code scanning
  - `DeferredReviewStory()` - Deferred knowledge node review
  - `TestCoverageStory()` - Test coverage improvement
- Created `pkg/maintenance/spec_test.go` with comprehensive tests:
  - `TestGenerateSpec` - Full spec generation
  - `TestGenerateSpecWithDisabledTasks` - Conditional story inclusion
  - `TestGenerateSpecWithCustomID` - Custom ID support
  - `TestTodoScanStoryMarkers` - Marker configuration
  - `TestStoryTemplates` - All template validation
  - `TestDefaultMarkersUsed` - Default marker fallback

---

### Phase 3: Orchestrator Integration (Stories 6-8) ✅ COMPLETE

#### Story 6: Programmatic Branch Cleanup ✅

**Note:** Uses existing `pkg/github` package with `CleanupMergedBranches` function.

**Files to create:**
- `pkg/maintenance/cleanup.go` - Programmatic maintenance tasks (thin wrapper)

**Implementation:**

```go
// pkg/maintenance/cleanup.go

package maintenance

import (
    "context"
    "fmt"
    "path/filepath"

    "orchestrator/pkg/config"
    "orchestrator/pkg/github"
)

// ProgrammaticReport holds results of programmatic maintenance tasks
type ProgrammaticReport struct {
    BranchesDeleted []string
    Errors          []string
}

// RunProgrammaticTasks executes non-LLM maintenance tasks
func RunProgrammaticTasks(ctx context.Context, cfg *config.MaintenanceConfig) (*ProgrammaticReport, error) {
    report := &ProgrammaticReport{}

    // Get GitHub client from config
    globalCfg, err := config.GetConfig()
    if err != nil {
        return nil, fmt.Errorf("failed to get config: %w", err)
    }
    if globalCfg.Git == nil || globalCfg.Git.RepoURL == "" {
        return nil, fmt.Errorf("git repo_url not configured")
    }

    ghClient, err := github.NewClientFromRemote(globalCfg.Git.RepoURL)
    if err != nil {
        return nil, fmt.Errorf("failed to create GitHub client: %w", err)
    }

    // Use centralized GitHub client for branch cleanup
    if cfg.Tasks.BranchCleanup {
        deleted, err := cleanupStaleBranches(ctx, ghClient,
            cfg.BranchCleanup.ProtectedPatterns, globalCfg.Git.TargetBranch)
        if err != nil {
            report.Errors = append(report.Errors,
                fmt.Sprintf("branch cleanup: %v", err))
        }
        report.BranchesDeleted = deleted
    }

    return report, nil
}

func cleanupStaleBranches(ctx context.Context, ghClient *github.Client,
    protected []string, targetBranch string) ([]string, error) {

    if targetBranch == "" {
        targetBranch = "main"
    }

    // Use existing CleanupMergedBranches from pkg/github/branch.go
    // This already handles listing branches, checking merge status, and deletion
    deleted, err := ghClient.CleanupMergedBranches(ctx, targetBranch, protected)
    if err != nil {
        return nil, fmt.Errorf("failed to cleanup branches: %w", err)
    }

    return deleted, nil
}

// isProtectedBranch checks if a branch name matches protected patterns
// (Utility function if needed for custom filtering beyond ghClient.CleanupMergedBranches)
func isProtectedBranch(branch string, patterns []string) bool {
    for _, pattern := range patterns {
        if matched, _ := filepath.Match(pattern, branch); matched {
            return true
        }
        if branch == pattern {
            return true
        }
    }
    return false
}
```

**Acceptance Criteria:**
- [x] Uses centralized `pkg/github.Client` for all GitHub operations
- [x] Leverages existing `CleanupMergedBranches` from `pkg/github/branch.go`
- [x] ProgrammaticReport captures results and errors
- [x] Graceful handling of API failures
- [ ] Unit tests with mocked GitHub client

**Implementation Notes:**
- Implemented directly in `pkg/architect/maintenance.go:runProgrammaticMaintenance()`
- Creates GitHub client from `config.Git.RepoURL` using `github.NewClientFromRemote()`
- Uses `config.Git.TargetBranch` (defaults to "main") and `cfg.BranchCleanup.ProtectedPatterns`
- Errors are captured in `ProgrammaticReport.Errors` rather than failing the whole maintenance cycle

---

#### Story 7: Submit Stories Tool Extension ✅

**Files to modify:**
- `pkg/tools/submit_stories.go` - Add maintenance flag support

**Implementation:**

```go
// pkg/tools/submit_stories.go

// Add to SubmitStoriesInput
type SubmitStoriesInput struct {
    // ... existing fields ...
    IsMaintenance bool `json:"is_maintenance,omitempty"`
}

// Add to story processing
func (t *SubmitStoriesTool) Execute(ctx context.Context, input SubmitStoriesInput) (*SubmitStoriesOutput, error) {
    // ... existing validation ...

    for _, story := range input.Stories {
        queuedStory := &QueuedStory{
            // ... existing fields ...
            IsMaintenance: input.IsMaintenance,
            AutoMerge:     input.IsMaintenance, // Maintenance PRs auto-merge
        }

        // Route maintenance stories appropriately
        if input.IsMaintenance {
            // Maintenance stories go to regular coders (parallel execution)
            // but with special flags for auto-merge
            t.dispatchMaintenanceStory(ctx, queuedStory)
        } else {
            t.dispatchNormalStory(ctx, queuedStory)
        }
    }

    // Return appropriate signal
    if input.IsMaintenance {
        return &SubmitStoriesOutput{Signal: SignalMaintenanceSubmitted}, nil
    }
    return &SubmitStoriesOutput{Signal: SignalStoriesSubmitted}, nil
}
```

**Acceptance Criteria:**
- [x] IsMaintenance flag added to input schema
- [ ] AutoMerge flag set for maintenance stories
- [x] SignalMaintenanceSubmitted signal added
- [x] Maintenance stories dispatched to available coders
- [ ] Unit tests for maintenance story dispatch

**Implementation Notes:**
- Added `maintenance` boolean property to tool input schema in `submit_stories.go`
- Added `SignalMaintenanceSubmitted = "MAINTENANCE_SUBMITTED"` to `pkg/tools/mcp.go`
- Priority order: maintenance > hotfix > normal for determining queue type
- Maintenance flag passed through `ProcessEffect.Data` for state machine routing

---

#### Story 8: Architect Maintenance Handling ✅

**Files to modify:**
- `pkg/architect/driver.go` - Handle maintenance specs
- `pkg/architect/request.go` - Auto-approve maintenance PRs

**Implementation:**

```go
// pkg/architect/driver.go

// HandleMaintenanceSpec processes a maintenance spec without scoping
func (d *Driver) HandleMaintenanceSpec(spec *maintenance.Spec) error {
    // Skip SCOPING for maintenance - stories are pre-defined
    d.logger.Info("Processing maintenance spec: %s", spec.ID)

    // Convert maintenance stories to queued stories
    stories := make([]*QueuedStory, 0, len(spec.Stories))
    for _, s := range spec.Stories {
        stories = append(stories, &QueuedStory{
            ID:            s.ID,
            Title:         s.Title,
            Content:       s.Content,
            Express:       s.Express,
            IsMaintenance: true,
            AutoMerge:     true,
        })
    }

    // Add to queue and dispatch
    d.queue.AddStories(stories)
    return d.dispatchReadyStories()
}

// pkg/architect/request.go

// In handleApprovalRequest, add maintenance auto-approve logic
func (d *Driver) handleApprovalRequest(ctx context.Context, msg *proto.AgentMsg) error {
    // ... existing approval logic ...

    // For maintenance stories, use lighter review
    if payload.IsMaintenance {
        return d.handleMaintenanceApproval(ctx, msg, payload)
    }

    // ... existing iterative review ...
}

func (d *Driver) handleMaintenanceApproval(ctx context.Context,
    msg *proto.AgentMsg, payload *ApprovalPayload) error {

    // Verify tests pass (required for auto-merge)
    if !payload.TestsPassed {
        return d.sendNeedsChanges(msg, "Tests must pass for maintenance PRs")
    }

    // Auto-approve if tests pass
    return d.sendApproval(msg, "Maintenance PR approved - tests passing")
}
```

**Acceptance Criteria:**
- [x] HandleMaintenanceSpec() bypasses scoping (via dispatchMaintenanceSpec in maintenance.go)
- [x] Maintenance stories queued with correct flags (AddMaintenanceStory sets IsMaintenance=true)
- [x] handleMaintenanceApproval() provides lighter review
- [x] Auto-approve when tests pass (all maintenance approvals auto-approved)
- [ ] Still reject if tests fail (not implemented - currently auto-approves regardless)
- [ ] Unit tests for maintenance approval flow

**Implementation Notes:**
- Added `handleMaintenanceApproval()` in `pkg/architect/request.go`
- Detection via `story.IsMaintenance` flag in `handleApprovalRequest()`
- Auto-approves with feedback: "Maintenance story auto-approved. Low-risk changes..."
- No LLM call required - immediate approval response
- Test failure rejection deferred to Phase 4 (requires PR status integration)

---

### Phase 4: Reporting & Completion (Stories 9-11)

#### Story 9: Maintenance Cycle Tracking

**Files to create:**
- `pkg/maintenance/tracker.go` - Track maintenance cycle progress

**Implementation:**

```go
// pkg/maintenance/tracker.go

package maintenance

import (
    "sync"
    "time"
)

// CycleTracker tracks progress of a maintenance cycle
type CycleTracker struct {
    mu sync.RWMutex

    CycleID     string
    StartedAt   time.Time
    CompletedAt time.Time

    // Programmatic results
    BranchesDeleted []string

    // Story results
    Stories map[string]*StoryResult

    // Aggregated metrics
    Metrics CycleMetrics
}

type StoryResult struct {
    StoryID     string
    Title       string
    Status      string    // pending, in_progress, completed, failed
    PRNumber    int
    PRMerged    bool
    CompletedAt time.Time
    Summary     string    // From PR description
}

type CycleMetrics struct {
    StoriesCompleted    int
    StoriesFailed       int
    PRsMerged           int
    KnowledgeNodesUpdated int
    DocsLinksFixed      int
    TodosFound          int
    TestsAdded          int
}

func NewCycleTracker(cycleID string) *CycleTracker {
    return &CycleTracker{
        CycleID:   cycleID,
        StartedAt: time.Now(),
        Stories:   make(map[string]*StoryResult),
    }
}

func (t *CycleTracker) OnStoryComplete(storyID string, result *StoryResult) {
    t.mu.Lock()
    defer t.mu.Unlock()

    t.Stories[storyID] = result
    t.Metrics.StoriesCompleted++
    if result.PRMerged {
        t.Metrics.PRsMerged++
    }
}

func (t *CycleTracker) IsComplete() bool {
    t.mu.RLock()
    defer t.mu.RUnlock()

    for _, s := range t.Stories {
        if s.Status == "pending" || s.Status == "in_progress" {
            return false
        }
    }
    return true
}

func (t *CycleTracker) GenerateReport() *CycleReport {
    t.mu.RLock()
    defer t.mu.RUnlock()

    t.CompletedAt = time.Now()

    return &CycleReport{
        CycleID:         t.CycleID,
        Duration:        t.CompletedAt.Sub(t.StartedAt),
        BranchesDeleted: t.BranchesDeleted,
        Stories:         t.Stories,
        Metrics:         t.Metrics,
    }
}
```

**Acceptance Criteria:**
- [ ] CycleTracker tracks all story progress
- [ ] Thread-safe updates with mutex
- [ ] IsComplete() correctly detects cycle completion
- [ ] Metrics aggregated from story results
- [ ] Unit tests for tracker state transitions

---

#### Story 10: Summary Report Generation

**Files to create:**
- `pkg/maintenance/report.go` - Generate markdown report

**Implementation:**

```go
// pkg/maintenance/report.go

package maintenance

import (
    "bytes"
    "fmt"
    "text/template"
    "time"
)

type CycleReport struct {
    CycleID         string
    Duration        time.Duration
    BranchesDeleted []string
    Stories         map[string]*StoryResult
    Metrics         CycleMetrics
    TodoFindings    *TodoScanResults // From TODO scan story
}

type TodoScanResults struct {
    TotalTodos      int
    TotalFixmes     int
    TotalDeprecated int
    StaleCount      int      // TODOs referencing completed work
    Findings        []TodoFinding
}

type TodoFinding struct {
    Type    string // TODO, FIXME, deprecated
    File    string
    Line    int
    Content string
    IsStale bool
}

const reportTemplate = `# Maintenance Report - {{.CycleID}}

**Duration**: {{.Duration}}
**Completed**: {{.CompletedAt.Format "2006-01-02 15:04:05"}}

## Programmatic Tasks

### Branch Cleanup
{{if .BranchesDeleted -}}
Deleted {{len .BranchesDeleted}} stale branches:
{{range .BranchesDeleted}}- ` + "`{{.}}`" + `
{{end}}
{{- else -}}
No stale branches found.
{{- end}}

## Story Results

{{range $id, $story := .Stories}}
### {{$story.Title}}
{{if eq $story.Status "completed" -}}
- **Status**: Completed
- **PR**: #{{$story.PRNumber}} {{if $story.PRMerged}}(merged){{end}}
{{if $story.Summary}}- **Summary**: {{$story.Summary}}{{end}}
{{- else -}}
- **Status**: {{$story.Status}}
{{- end}}

{{end}}

## Metrics Summary

| Metric | Value |
|--------|-------|
| Stories Completed | {{.Metrics.StoriesCompleted}} |
| PRs Merged | {{.Metrics.PRsMerged}} |
| Knowledge Nodes Updated | {{.Metrics.KnowledgeNodesUpdated}} |
| Docs Links Fixed | {{.Metrics.DocsLinksFixed}} |
| TODOs Found | {{.Metrics.TodosFound}} |
| Tests Added | {{.Metrics.TestsAdded}} |

{{if .TodoFindings}}
## TODO Scan Findings

**Summary**:
- TODOs: {{.TodoFindings.TotalTodos}}
- FIXMEs: {{.TodoFindings.TotalFixmes}}
- Deprecated: {{.TodoFindings.TotalDeprecated}}
- Stale (can remove): {{.TodoFindings.StaleCount}}

{{if gt .TodoFindings.StaleCount 0}}
### Suggested Hotfixes

Would you like to generate hotfixes for:
{{range $i, $f := .TodoFindings.Findings}}{{if $f.IsStale}}
{{$i}}. Remove stale TODO in ` + "`{{$f.File}}:{{$f.Line}}`" + `
{{end}}{{end}}

Reply with numbers to generate hotfix specs, or "skip" to continue.
{{end}}
{{end}}
`

func (r *CycleReport) ToMarkdown() (string, error) {
    tmpl, err := template.New("report").Parse(reportTemplate)
    if err != nil {
        return "", err
    }

    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, r); err != nil {
        return "", err
    }

    return buf.String(), nil
}

func (r *CycleReport) SaveToFile(dir string) error {
    filename := fmt.Sprintf("%s/maintenance-report-%s.md",
        dir, time.Now().Format("2006-01-02"))
    content, err := r.ToMarkdown()
    if err != nil {
        return err
    }
    return os.WriteFile(filename, []byte(content), 0644)
}
```

**Acceptance Criteria:**
- [ ] CycleReport contains all maintenance results
- [ ] ToMarkdown() generates readable report
- [ ] Report includes programmatic and LLM story results
- [ ] TODO findings with suggested hotfixes displayed
- [ ] SaveToFile() persists report to .maestro/maintenance-reports/
- [ ] Unit tests for report generation

---

#### Story 11: PM Report Presentation & Hotfix Integration

**Files to modify:**
- `pkg/pm/driver.go` - Present report and handle hotfix requests

**Implementation:**

```go
// pkg/pm/driver.go

// OnMaintenanceCycleComplete called when all maintenance stories finish
func (d *Driver) OnMaintenanceCycleComplete(report *maintenance.CycleReport) {
    d.maintenance.InProgress = false
    d.maintenance.LastMaintenance = time.Now()

    // Generate and save report
    if err := report.SaveToFile(d.projectDir + "/.maestro/maintenance-reports"); err != nil {
        d.logger.Error("Failed to save maintenance report: %v", err)
    }

    // Post report to chat for user visibility
    markdown, _ := report.ToMarkdown()
    d.postToChat(markdown)

    // If there are suggested hotfixes, track them for user response
    if report.TodoFindings != nil && report.TodoFindings.StaleCount > 0 {
        d.SetStateData(StateKeyPendingHotfixSuggestions, report.TodoFindings)
        d.SetState(StateAwaitUser) // Wait for user to respond to suggestions
    }
}

// HandleHotfixSuggestionResponse processes user selection of hotfixes
func (d *Driver) HandleHotfixSuggestionResponse(selection []int) error {
    findings := d.GetStateData(StateKeyPendingHotfixSuggestions).(*maintenance.TodoScanResults)

    // Generate hotfix specs for selected items
    for _, idx := range selection {
        if idx < 0 || idx >= len(findings.Findings) {
            continue
        }

        finding := findings.Findings[idx]
        hotfixSpec := d.generateTodoHotfix(finding)
        d.submitHotfixSpec(hotfixSpec)
    }

    // Clear pending suggestions
    d.SetStateData(StateKeyPendingHotfixSuggestions, nil)
    return nil
}

func (d *Driver) generateTodoHotfix(finding maintenance.TodoFinding) *HotfixSpec {
    return &HotfixSpec{
        Title: fmt.Sprintf("Remove stale %s in %s", finding.Type, finding.File),
        Description: fmt.Sprintf(`Remove the stale %s comment at line %d.

The comment references: %s

This appears to reference completed work and can be safely removed.`,
            finding.Type, finding.Line, finding.Content),
        File: finding.File,
        Line: finding.Line,
    }
}
```

**Acceptance Criteria:**
- [ ] OnMaintenanceCycleComplete() saves report and posts to chat
- [ ] Suggested hotfixes tracked in state
- [ ] State transitions to AWAIT_USER if suggestions exist
- [ ] HandleHotfixSuggestionResponse() generates hotfix specs
- [ ] Integration with existing hotfix flow
- [ ] Unit tests for suggestion handling

---

### Phase 5: Database & Testing (Stories 12-13)

#### Story 12: Database Schema for Maintenance Metrics

**Files to modify:**
- `pkg/persistence/schema.go` - Add maintenance tables

**Implementation:**

```go
// pkg/persistence/schema.go

// Add to schema migrations (increment version)

const maintenanceSchema = `
-- Maintenance cycle tracking
CREATE TABLE IF NOT EXISTS maintenance_cycles (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    started_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'in_progress',
    branches_deleted INTEGER DEFAULT 0,
    stories_completed INTEGER DEFAULT 0,
    stories_failed INTEGER DEFAULT 0,
    prs_merged INTEGER DEFAULT 0,
    knowledge_nodes_updated INTEGER DEFAULT 0,
    docs_links_fixed INTEGER DEFAULT 0,
    todos_found INTEGER DEFAULT 0,
    tests_added INTEGER DEFAULT 0,
    report_path TEXT
);

-- Maintenance story results
CREATE TABLE IF NOT EXISTS maintenance_story_results (
    id TEXT PRIMARY KEY,
    cycle_id TEXT NOT NULL REFERENCES maintenance_cycles(id),
    story_id TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL,
    pr_number INTEGER,
    pr_merged BOOLEAN DEFAULT FALSE,
    completed_at TIMESTAMP,
    summary TEXT,
    error_message TEXT
);

-- TODO scan findings (for hotfix suggestions)
CREATE TABLE IF NOT EXISTS maintenance_todo_findings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cycle_id TEXT NOT NULL REFERENCES maintenance_cycles(id),
    finding_type TEXT NOT NULL,
    file_path TEXT NOT NULL,
    line_number INTEGER NOT NULL,
    content TEXT NOT NULL,
    is_stale BOOLEAN DEFAULT FALSE,
    hotfix_generated BOOLEAN DEFAULT FALSE
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_maint_cycles_session ON maintenance_cycles(session_id);
CREATE INDEX IF NOT EXISTS idx_maint_stories_cycle ON maintenance_story_results(cycle_id);
CREATE INDEX IF NOT EXISTS idx_maint_todos_cycle ON maintenance_todo_findings(cycle_id);
`

func migrateToVersionN() error {
    // Add maintenance schema
    _, err := db.Exec(maintenanceSchema)
    return err
}
```

**Acceptance Criteria:**
- [ ] maintenance_cycles table tracks cycle metadata
- [ ] maintenance_story_results table tracks per-story outcomes
- [ ] maintenance_todo_findings table stores scan results
- [ ] Proper foreign keys and indexes
- [ ] Migration function added to schema.go
- [ ] Unit tests for schema creation

---

#### Story 13: End-to-End Integration Tests

**Files to create:**
- `pkg/maintenance/maintenance_test.go` - Unit tests
- `tests/integration/maintenance_test.go` - E2E tests

**Test coverage:**

```go
// pkg/maintenance/maintenance_test.go

func TestGenerateSpec(t *testing.T) {
    // Test spec generation with various config combinations
}

func TestStoryTemplates(t *testing.T) {
    // Verify all story templates have required fields
}

func TestBranchCleanup(t *testing.T) {
    // Test branch cleanup with mock git/gh
}

func TestProtectedPatternMatching(t *testing.T) {
    // Test glob pattern matching for protected branches
}

func TestReportGeneration(t *testing.T) {
    // Test markdown report generation
}

func TestCycleTracker(t *testing.T) {
    // Test tracker state transitions
}

// tests/integration/maintenance_test.go

func TestMaintenanceTrigger(t *testing.T) {
    // Test that maintenance triggers after N specs
}

func TestMaintenanceStoryDispatch(t *testing.T) {
    // Test that maintenance stories are dispatched to coders
}

func TestMaintenanceAutoMerge(t *testing.T) {
    // Test that maintenance PRs auto-merge after CI
}

func TestMaintenanceReportFlow(t *testing.T) {
    // Test full flow from trigger to report
}

func TestHotfixSuggestionFlow(t *testing.T) {
    // Test TODO findings to hotfix generation
}
```

**Acceptance Criteria:**
- [ ] Unit tests for all maintenance package functions
- [ ] Integration tests for trigger and dispatch flow
- [ ] Integration tests for auto-merge behavior
- [ ] Integration tests for report generation
- [ ] Tests use mocked external services (gh, git)
- [ ] >80% code coverage for maintenance package

---

### Story Dependency Graph

```
Phase 1 (Foundation):
  Story 1: Config Schema
  Story 2: GitHub API ────┐
  Story 3: Workflows ─────┴─→ (Bootstrap complete)

Phase 2 (PM Integration):
  Story 4: PM State ──────┐
  Story 5: Spec Gen ──────┴─→ (PM can trigger maintenance)

Phase 3 (Orchestrator):
  Story 6: Branch Cleanup
  Story 7: Submit Tool ───┐
  Story 8: Architect ─────┴─→ (Stories can execute)

Phase 4 (Reporting):
  Story 9: Tracker ───────┐
  Story 10: Report Gen ───┤
  Story 11: PM Report ────┴─→ (Full cycle complete)

Phase 5 (Polish):
  Story 12: Database
  Story 13: Tests ────────→ (Production ready)
```

### Estimated Complexity

| Story | Complexity | New Code | Modified Files |
|-------|------------|----------|----------------|
| 1 | Low | ~100 LOC | 2 |
| 2 | Medium | ~150 LOC | 3 |
| 3 | Low | ~100 LOC | 1 |
| 4 | Medium | ~200 LOC | 2 |
| 5 | Medium | ~250 LOC | 2 (new) |
| 6 | Medium | ~200 LOC | 1 (new) |
| 7 | Low | ~50 LOC | 1 |
| 8 | Medium | ~150 LOC | 2 |
| 9 | Medium | ~200 LOC | 1 (new) |
| 10 | Medium | ~250 LOC | 1 (new) |
| 11 | Medium | ~200 LOC | 1 |
| 12 | Low | ~80 LOC | 1 |
| 13 | High | ~400 LOC | 2 (new) |

**Total**: ~2,330 LOC across 13 stories

## Success Metrics

Track maintenance effectiveness:

```sql
-- Maintenance cycle metrics
CREATE TABLE maintenance_cycles (
    id TEXT PRIMARY KEY,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    branches_deleted INTEGER,
    knowledge_nodes_updated INTEGER,
    docs_links_fixed INTEGER,
    todos_found INTEGER,
    nodes_promoted INTEGER,
    tests_added INTEGER,
    stories_merged INTEGER
);

-- Query: Average maintenance impact
SELECT
    AVG(branches_deleted) as avg_branches,
    AVG(tests_added) as avg_tests,
    AVG(todos_found) as avg_todos
FROM maintenance_cycles
WHERE completed_at > datetime('now', '-30 days');
```

## Notes and Considerations

### Safety

- **No production code changes** except confirmed bug fixes in test story
- **Documentation and tests only** for LLM stories
- **Fully merged branches only** for cleanup
- **Auto-merge requires CI pass** for all maintenance PRs

### Graceful Degradation

- If GitHub API calls fail during bootstrap, log warning and continue
- If a maintenance story fails, other stories continue
- If maintenance spec generation fails, skip cycle and try next time
- Missing knowledge graph or docs files are handled gracefully

### Parallelization

All 5 LLM stories can run in parallel since they operate on different files:
- Story A: `.maestro/knowledge.dot`
- Story B: `README.md`, `docs/*.md`
- Story C: Read-only scan (report output only)
- Story D: `.maestro/knowledge.dot` (different concern than Story A)
- Story E: `*_test.*` files

**Note**: Stories A and D both touch `knowledge.dot`. They should be sequenced or their changes merged carefully. Alternative: combine into single story.

### Future Enhancements

- **Coverage baseline tracking**: Store coverage metrics over time
- **Trend analysis**: Report on debt accumulation trends
- **Custom maintenance stories**: Allow users to define additional maintenance tasks
- **Maintenance scheduling**: Time-based triggers in addition to spec-based
- **Cross-repo maintenance**: Aggregate maintenance across multiple projects
