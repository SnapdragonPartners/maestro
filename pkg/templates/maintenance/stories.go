package maintenance

import (
	"fmt"
	"strings"
)

// KnowledgeSyncStory returns the story template for knowledge graph synchronization.
func KnowledgeSyncStory() Story {
	return Story{
		ID:    "maint-knowledge-sync",
		Title: "Synchronize Knowledge Graph",
		Content: `Verify that the knowledge graph accurately reflects the current state of the codebase.
Update nodes that reference moved, renamed, or deleted code.

## Acceptance Criteria
- Parse and validate .maestro/knowledge.dot
- For nodes with path attributes, verify the path exists
- For nodes with example attributes, verify the pattern exists in code
- Mark nodes referencing deleted code as status="deprecated"
- Update path attributes for renamed/moved files
- Remove or update stale nodes that no longer apply
- Graph remains valid DOT format after changes

## Constraints
- Only modify .maestro/knowledge.dot
- Do not create new nodes (that's done during feature development)
- Preserve all edges unless a referenced node is removed`,
		Express:       false,
		IsMaintenance: true,
	}
}

// DocsVerificationStory returns the story template for documentation verification.
func DocsVerificationStory() Story {
	return Story{
		ID:    "maint-docs-verification",
		Title: "Verify Documentation Accuracy",
		Content: `Ensure project documentation is accurate and up-to-date.
Fix broken links, outdated examples, and incorrect instructions.

## Acceptance Criteria
- Verify all internal links resolve to existing files
- Verify external links are accessible (no 404s)
- Check code examples for syntax validity
- Verify installation/setup instructions are accurate
- Update outdated configuration examples
- Fix any factual inaccuracies found

## Scope
Focus on:
1. README.md (highest priority)
2. docs/*.md files
3. .maestro/*.md files (agent prompts)

## Constraints
- Documentation changes only (no code changes)
- Preserve existing documentation structure
- Do not add new sections unless fixing missing critical info`,
		Express:       false,
		IsMaintenance: true,
	}
}

// TodoScanStory returns the story template for TODO/deprecated code scanning.
func TodoScanStory(markers []string) Story {
	markerList := strings.Join(markers, ", ")
	return Story{
		ID:    "maint-todo-scan",
		Title: "Scan for TODOs and Deprecated Code",
		Content: fmt.Sprintf(`Scan the codebase for TODO comments, FIXME markers, and deprecated code
annotations. Generate a summary report for the project maintainer.

## Markers to Scan
%s

## Acceptance Criteria
- Scan all source files for configured markers
- Group findings by type (TODO, FIXME, HACK, deprecated)
- Include file path, line number, and surrounding context
- Identify TODOs that reference completed work (can be removed)
- Generate markdown report in .maestro/maintenance-reports/

## Output Format
The report should include:
- Summary counts by marker type
- Table of findings with file, line, content, and status
- Separate sections for each marker type

## Constraints
- Read-only analysis (no code changes)
- Report only, no automatic fixes
- User can request hotfixes for high-priority items`, markerList),
		Express:       true, // No planning needed for scan
		IsMaintenance: true,
	}
}

// DeferredReviewStory returns the story template for deferred knowledge node review.
func DeferredReviewStory() Story {
	return Story{
		ID:    "maint-deferred-review",
		Title: "Review Deferred Knowledge Nodes",
		Content: `Review knowledge graph nodes marked as status="future" or status="legacy"
to determine if they can be promoted, updated, or removed.

## Acceptance Criteria
- Identify all nodes with status="future" or status="legacy"
- For each node, assess if blockers are resolved
- Promote unblocked nodes to status="current"
- Mark obsolete nodes for removal (superseded by different approach)
- Generate report for nodes that remain blocked

## Assessment Criteria
- Promote to current: Referenced component now exists, blocker resolved
- Mark obsolete: Feature implemented differently, node no longer relevant
- Remain deferred: Blockers still exist, dependencies not ready

## Output
- Updated .maestro/knowledge.dot with promotions
- Report section listing nodes that remain blocked with reasons

## Constraints
- Only modify node status attribute
- Do not change node descriptions or other attributes
- Document reasoning for each status change in PR description`,
		Express:       false,
		IsMaintenance: true,
	}
}

// TestCoverageStory returns the story template for test coverage improvement.
func TestCoverageStory() Story {
	return Story{
		ID:    "maint-test-coverage",
		Title: "Improve Test Coverage",
		Content: `Analyze the codebase to identify areas with low or missing unit test coverage.
Create or enhance test suites for the highest-value untested code.

## Scope
Select 3-5 coverage targets (packages, modules, or logical components)
to focus on. Prioritize by:
1. Code that is frequently used or imported
2. Public APIs and exported functions
3. Complex logic with multiple code paths
4. Code lacking any existing tests

## Acceptance Criteria
- Identify 3-5 high-value coverage targets
- Create unit test files for each target
- Tests cover primary happy paths and basic error cases
- All new tests pass when running the project's standard test command
- New tests are automatically discovered by the existing test runner (no build system changes)
- Do not modify application code unless fixing a confirmed bug

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
- Follow existing test file naming conventions (e.g., *_test.go, *.test.js, test_*.py)
- Focus on meaningful coverage, not line count`,
		Express:       false,
		IsMaintenance: true,
	}
}
