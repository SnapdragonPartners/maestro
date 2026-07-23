package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

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
// argv there.
func runInSolutionOracle(ctx context.Context, solutionDir string, check *story.Check, assets map[string][]byte) runrecord.CheckResult {
	destDir, created, err := ensureMaterialiseDir(solutionDir, check.PackageDir)
	var materialised []string
	defer func() { cleanupMaterialised(materialised, created) }()
	if err != nil {
		return oracleFail(check.Name, err)
	}
	materialised, err = materialiseAssets(destDir, check.Assets, assets)
	if err != nil {
		return oracleFail(check.Name, err)
	}
	result, _ := runProcess(ctx, solutionDir, nil, check.Name, check.Argv...)
	return result
}

// runScratchOracle keeps the oracle out of the graded tree: assets go to an
// engine-owned tool dir, the solution commit is checked out into an
// engine-owned scratch worktree, and $ORACLE_SCRATCH hands that clean checkout
// to the helper.
func runScratchOracle(ctx context.Context, solutionDir string, check *story.Check, assets map[string][]byte, solution string) runrecord.CheckResult {
	toolDir, err := os.MkdirTemp("", "oracle-tool-")
	if err != nil {
		return oracleFail(check.Name, fmt.Errorf("create oracle tool dir: %w", err))
	}
	defer func() { _ = os.RemoveAll(toolDir) }()
	if _, err = materialiseAssets(toolDir, check.Assets, assets); err != nil {
		return oracleFail(check.Name, err)
	}

	scratchParent, err := os.MkdirTemp("", "oracle-scratch-")
	if err != nil {
		return oracleFail(check.Name, fmt.Errorf("create oracle scratch dir: %w", err))
	}
	// The engine owns removal of BOTH the worktree registration (in the solution
	// repo) and the on-disk tree, on every exit path including a timeout kill —
	// so use a fresh context, never the possibly-cancelled ctx. Defer order
	// (LIFO): unregister the worktree first, then remove the parent tree.
	defer func() { _ = os.RemoveAll(scratchParent) }()
	scratchRoot := filepath.Join(scratchParent, "wt")
	if _, err = gitx.Run(ctx, solutionDir, "worktree", "add", "--detach", scratchRoot, solution); err != nil {
		return oracleFail(check.Name, fmt.Errorf("create scratch worktree at solution commit: %w", err))
	}
	// A fresh context on purpose: cleanup must run even after ctx is cancelled
	// or timed out, which is exactly when a leak would occur.
	defer func() { //nolint:contextcheck // cleanup must survive ctx expiry
		_, _ = gitx.Run(context.Background(), solutionDir, "worktree", "remove", "--force", scratchRoot)
	}()

	env := []string{"ORACLE_SCRATCH=" + scratchRoot}
	result, _ := runProcess(ctx, toolDir, env, check.Name, check.Argv...)
	return result
}

// ensureMaterialiseDir resolves <root>/<packageDir>, rejecting any symlink
// component (the solution is agent-controlled, so this is a runtime check, not
// just the load-time lexical one) and creating missing components. It returns
// the resolved directory and the dirs it created (deepest last) so the caller
// can remove exactly what it added. packageDir is already lexically validated
// (no absolute/`..`) at load.
func ensureMaterialiseDir(root, packageDir string) (string, []string, error) {
	if packageDir == "" || packageDir == "." {
		return root, nil, nil
	}
	var created []string
	cur := root
	for _, comp := range strings.Split(filepath.ToSlash(packageDir), "/") {
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
// deepest first (each is empty once its files are gone). Best-effort: a leaked
// artifact is observable by the achievability script, but cleanup failure must
// not mask the check's own result.
func cleanupMaterialised(files, createdDirs []string) {
	for _, f := range files {
		_ = os.Remove(f)
	}
	for i := len(createdDirs) - 1; i >= 0; i-- {
		_ = os.Remove(createdDirs[i])
	}
}

// oracleFail wraps an engine-side oracle error (materialise/scratch failure) as
// a failed check result; it is distinct from the oracle's own non-zero exit,
// which runProcess reports.
func oracleFail(name string, err error) runrecord.CheckResult {
	return runrecord.CheckResult{Name: name, Passed: false, Detail: truncateDetail(err.Error())}
}
