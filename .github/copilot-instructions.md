# Copilot Code Review Instructions

## Review Philosophy

This is a Go-based multi-agent AI orchestrator. Reviews should focus on **correctness**, **DRYness**, and **testability** — not style or formatting. We have aggressive linters (`golangci-lint`, pre-commit hooks) that handle formatting, naming, and import ordering. Do not comment on those.

Refer to `AGENTS.md` at the repository root for the full coding guidelines and severity levels (P0/P1). The rules there are authoritative. Key highlights:

- Only block on **P0** (bugs, crashes, data corruption, severe security) and **P1** (clear guideline violations harming maintainability).
- Everything else is a suggestion or question.
- This is a **single-user, locally run app** — be pragmatic about security.

## Spec Compliance Check

When a PR includes **new or modified markdown files** (especially under `docs/`), determine whether any of them are **specification documents**. Specs typically have titles like `*_SPEC.md`, contain sections such as "Acceptance Criteria", "Implementation Plan", "Design", or "Architecture", or describe features/behaviors to be built.

If the PR contains a spec document:

1. **Read the spec thoroughly** and identify its acceptance criteria, design constraints, and expected behaviors.
2. **Cross-reference the spec against the code changes in the PR.** Assess whether the implementation is faithful and complete relative to the spec.
3. **Flag any gaps**: acceptance criteria not addressed, behaviors described in the spec but missing from the code, or code that contradicts the spec.
4. **Flag scope creep**: code changes that go significantly beyond what the spec describes without justification.

If the PR modifies an existing spec, check whether the code already in the PR (or referenced by it) still aligns with the updated spec.

## Correctness

- Look for logic errors, off-by-one mistakes, race conditions, and unhandled edge cases.
- Verify error handling: errors should not be silently dropped. Check that errors are wrapped with context (`fmt.Errorf("doing X: %w", err)`).
- Watch for the **Go nil-interface gotcha**: a concrete nil pointer assigned to an interface is non-nil. We use `reflect.ValueOf(e).IsNil()` in places where this matters (see `isExecutorUsable()`).
- `LocalExec.Run()` returns `err == nil` even for non-zero exit codes. Always check `result.ExitCode` rather than relying on `err != nil`.
- Verify that any bare type assertion (`x.(T)`) is justified. We prefer `SafeAssert` (a generics-based helper) for safety and clarity — see AGENTS.md section 2.

## DRYness and Reuse

- Flag **duplicated or near-duplicate logic** across files. This codebase is large — duplication often means a shared helper should be extracted.
- Check for repeated string literals, magic numbers, or timeouts that should be named constants.
- If similar patterns exist elsewhere in the codebase, suggest consolidation rather than a new parallel implementation.

## Testability

- New logic with multiple branches or edge cases should have unit tests.
- Reusable helpers and parsing/validation logic should be tested.
- Integration tests (build tag: `integration`) are expected for cross-component workflows.
- Do not nitpick about test coverage on trivial or straightforward code.

## Architecture and Abstraction

- Push back on **unnecessary abstractions**: interfaces with one implementation, thin wrappers that add no value.
- Prefer simple, direct designs. Accept purposeful abstractions that enable testing or support multiple backends.
- Watch for **dead code** — unused functions, unreachable blocks, or orphaned fields.

## Workspace and Container Patterns

- **Bind mount inode preservation**: Any code that calls `os.RemoveAll()` on a workspace root directory is a bug. Use `utils.CleanDirectoryContents()` instead to preserve bind mount inodes on macOS/Docker Desktop.
- **Merge-base diffs**: The architect's `get_diff` tool uses merge-base semantics to avoid phantom diffs. Verify any diff-related changes maintain this behavior.

## What NOT to Review

- **Formatting and style**: Handled by `golangci-lint` and `gofmt`. Do not comment.
- **Import ordering**: Handled by linter. Do not comment.
- **Naming conventions**: Only flag if naming is genuinely misleading, not just non-standard.
- **Comment density**: Do not suggest adding comments to self-explanatory code.
