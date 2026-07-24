package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// scratchCleanupTimeout bounds engine-owned scratch teardown. It uses a fresh
// context (cleanup must run after the attempt's ctx is cancelled/timed out —
// exactly when a leak would otherwise occur) but must not itself hang the
// attempt if the git removal wedges.
const scratchCleanupTimeout = 30 * time.Second

// runOracle executes one oracle check: it materialises the check's retained
// asset bytes, runs the declared argv under the shared deadline/process-group
// machinery, and cleans up unconditionally. Two modes:
//
//   - default ("" scratch): assets land in the bound solution at
//     <package_dir>/<basename> via exclusive-create (never overwriting an
//     agent-authored file), argv runs with cwd = the solution checkout.
//   - scratch = "solution-commit": assets land in a separate engine-owned tool
//     dir (never the solution), the engine checks the immutable solution commit
//     into an engine-owned scratch worktree, and argv runs with cwd = the tool
//     dir and $ORACLE_SCRATCH pointing at that clean checkout. The scratch
//     carries the agent's solution and NO oracle asset, so only the agent's own
//     tests — not the oracle — can judge a mutant.
//
// Cleanup (materialised files, created dirs, tool dir, scratch worktree) runs on
// success, failure, and timeout alike; a killed helper cannot leak.
func runOracle(ctx context.Context, solutionDir string, check *story.Check, assets map[string][]byte, solution string) runrecord.CheckResult {
	if check.Scratch == story.OracleScratchSolutionCommit {
		return runScratchOracle(ctx, solutionDir, check, assets, solution)
	}
	return runInSolutionOracle(ctx, solutionDir, check, assets)
}

// runInSolutionOracle materialises the assets into the bound solution and runs
// argv there. A cleanup failure fails the check closed (a leaked asset would
// contaminate subsequent checks), folded into whatever result the run produced.
func runInSolutionOracle(ctx context.Context, solutionDir string, check *story.Check, assets map[string][]byte) (result runrecord.CheckResult) {
	destDir, created, err := ensureMaterialiseDir(solutionDir, check.PackageDir)
	var materialised []string
	defer func() {
		if cerr := cleanupMaterialised(materialised, created); cerr != nil {
			result = foldCleanupError(result, check.Name, cerr)
		}
	}()
	if err != nil {
		return oracleFail(check.Name, err)
	}
	materialised, err = materialiseAssets(destDir, check.Assets, assets)
	if err != nil {
		return oracleFail(check.Name, err)
	}
	result, _ = runProcess(ctx, solutionDir, nil, check.Name, check.Argv...)
	return result
}

// runScratchOracle keeps the oracle out of the graded tree: assets go to an
// engine-owned tool dir, the solution commit is checked out into an
// engine-owned scratch worktree, and $ORACLE_SCRATCH hands that clean checkout
// to the helper.
func runScratchOracle(ctx context.Context, solutionDir string, check *story.Check, assets map[string][]byte, solution string) (result runrecord.CheckResult) {
	toolDir, err := os.MkdirTemp("", "oracle-tool-")
	if err != nil {
		return oracleFail(check.Name, fmt.Errorf("create oracle tool dir: %w", err))
	}
	// Every teardown below folds its failure into result so a leaked worktree
	// registration or tree cannot coexist with a passing oracle. Defer order is
	// LIFO: unregister the worktree first, then remove the scratch tree, then
	// the tool dir.
	defer func() {
		if rerr := os.RemoveAll(toolDir); rerr != nil {
			result = foldCleanupError(result, check.Name, fmt.Errorf("remove tool dir: %w", rerr))
		}
	}()
	if _, err = materialiseAssets(toolDir, check.Assets, assets); err != nil {
		return oracleFail(check.Name, err)
	}

	scratchParent, err := os.MkdirTemp("", "oracle-scratch-")
	if err != nil {
		return oracleFail(check.Name, fmt.Errorf("create oracle scratch dir: %w", err))
	}
	defer func() {
		if rerr := os.RemoveAll(scratchParent); rerr != nil {
			result = foldCleanupError(result, check.Name, fmt.Errorf("remove scratch tree: %w", rerr))
		}
	}()
	scratchRoot := filepath.Join(scratchParent, "wt")
	if _, err = gitx.Run(ctx, solutionDir, "worktree", "add", "--detach", scratchRoot, solution); err != nil {
		return oracleFail(check.Name, fmt.Errorf("create scratch worktree at solution commit: %w", err))
	}
	defer func() { //nolint:contextcheck // removeWorktree derives its own fresh bounded context, by design
		if rerr := removeWorktree(solutionDir, scratchRoot); rerr != nil {
			result = foldCleanupError(result, check.Name, rerr)
		}
	}()

	env := []string{"ORACLE_SCRATCH=" + scratchRoot}
	result, _ = runProcess(ctx, toolDir, env, check.Name, check.Argv...)
	return result
}

// removeWorktree unregisters the scratch worktree from the solution repo under
// a fresh, bounded context — cleanup must survive the attempt's ctx being
// cancelled or timed out (the leak window), but must not itself hang the
// attempt if git wedges. Its failure is surfaced, never swallowed.
func removeWorktree(solutionDir, scratchRoot string) error {
	ctx, cancel := context.WithTimeout(context.Background(), scratchCleanupTimeout)
	defer cancel()
	if _, err := gitx.Run(ctx, solutionDir, "worktree", "remove", "--force", scratchRoot); err != nil { //nolint:contextcheck // cleanup runs on a fresh bounded context, by design
		return fmt.Errorf("remove scratch worktree registration: %w", err)
	}
	return nil
}

// ensureMaterialiseDir resolves <root>/<packageDir>, rejecting any symlink
// component (the solution is agent-controlled, so this is a runtime check, not
// just the load-time lexical one) and creating missing components. It returns
// the resolved directory and the dirs it created (deepest last) so the caller
// can remove exactly what it added.
func ensureMaterialiseDir(root, packageDir string) (string, []string, error) {
	if packageDir == "" || packageDir == "." {
		return root, nil, nil
	}
	// Normalise before splitting and fail closed on any escape. packageDir is
	// story-authored and load-validated (safeRelPath), but this materialiser
	// must not rely on that: splitting the RAW value would walk a `..` segment
	// upward out of the solution root before the per-component checks run, so a
	// value like "sub/../.." could escape. filepath.Clean collapses interior
	// `..` that resolves safely; anything that still escapes is rejected.
	clean := filepath.Clean(packageDir)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return root, nil, fmt.Errorf("package dir %q escapes the solution root", packageDir)
	}
	var created []string
	cur := root
	for _, comp := range strings.Split(filepath.ToSlash(clean), "/") {
		if comp == "" || comp == "." {
			continue
		}
		cur = filepath.Join(cur, comp)
		info, err := os.Lstat(cur)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if mkErr := os.Mkdir(cur, 0o755); mkErr != nil {
				return cur, created, fmt.Errorf("create package dir %q: %w", packageDir, mkErr)
			}
			created = append(created, cur)
		case err != nil:
			return cur, created, fmt.Errorf("stat package dir component %q: %w", cur, err)
		case info.Mode()&os.ModeSymlink != 0:
			return cur, created, fmt.Errorf("package dir component %q is a symlink", cur)
		case !info.IsDir():
			return cur, created, fmt.Errorf("package dir component %q is not a directory", cur)
		}
	}
	return cur, created, nil
}

// materialiseAssets writes each named asset's retained bytes to
// <destDir>/<basename> with exclusive create — an existing file (an
// agent-authored file already using a reserved zz_oracle_ name) fails the
// check loudly rather than being clobbered. It returns every file it wrote,
// including a partial set on error, so the caller always cleans up.
func materialiseAssets(destDir string, names []string, assets map[string][]byte) ([]string, error) {
	written := make([]string, 0, len(names))
	for _, name := range names {
		content, ok := assets[name]
		if !ok {
			// Loaded guarantees every referenced asset was read; belt and braces.
			return written, fmt.Errorf("oracle asset %q missing from loaded bytes", name)
		}
		dest := filepath.Join(destDir, name)
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return written, fmt.Errorf("materialise oracle asset %q (refusing to overwrite): %w", name, err)
		}
		_, werr := f.Write(content)
		cerr := f.Close()
		written = append(written, dest)
		if werr != nil {
			return written, fmt.Errorf("write oracle asset %q: %w", name, werr)
		}
		if cerr != nil {
			return written, fmt.Errorf("close oracle asset %q: %w", name, cerr)
		}
	}
	return written, nil
}

// cleanupMaterialised removes materialised files then the dirs it created,
// deepest first (each is empty once its files are gone). Every removal is
// attempted; a still-present-target (ErrNotExist) is not an error. Any real
// failure is returned so the caller can fail the check closed — a leaked
// asset or directory would contaminate subsequent checks against the worktree.
func cleanupMaterialised(files, createdDirs []string) error {
	var errs []error
	for _, f := range files {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for i := len(createdDirs) - 1; i >= 0; i-- {
		if err := os.Remove(createdDirs[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...) //nolint:wrapcheck // aggregate of already-contextual os.Remove errors
}

// foldCleanupError makes a cleanup failure fail the check closed: the result is
// forced to not-passed and the cleanup detail is appended to whatever the run
// already recorded (so the original failure reason — a non-zero argv, say — is
// never lost).
func foldCleanupError(result runrecord.CheckResult, name string, cerr error) runrecord.CheckResult {
	detail := result.Detail
	if detail != "" {
		detail += "; "
	}
	detail += "cleanup failed: " + cerr.Error()
	return runrecord.CheckResult{Name: name, Passed: false, Detail: truncateDetail(detail)}
}

// oracleFail wraps an engine-side oracle error (materialise/scratch failure) as
// a failed check result; it is distinct from the oracle's own non-zero exit,
// which runProcess reports.
func oracleFail(name string, err error) runrecord.CheckResult {
	return runrecord.CheckResult{Name: name, Passed: false, Detail: truncateDetail(err.Error())}
}
