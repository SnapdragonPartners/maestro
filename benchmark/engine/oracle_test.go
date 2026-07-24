package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// oracleCheck builds an oracle check with one probe asset.
func oracleCheck(argv []string, packageDir, scratch string) (*story.Check, map[string][]byte) {
	const asset = "zz_oracle_probe.txt"
	return &story.Check{
		Name:       "oracle",
		Type:       story.CheckOracle,
		Assets:     []string{asset},
		Argv:       argv,
		PackageDir: packageDir,
		Scratch:    scratch,
	}, map[string][]byte{asset: []byte("probe-body")}
}

// TestOracleInSolutionPassAndCleanup runs an oracle whose argv confirms the
// asset was materialised at cwd, then asserts it passes AND leaves nothing
// behind (the materialised file is removed unconditionally).
func TestOracleInSolutionPassAndCleanup(t *testing.T) {
	dir := t.TempDir()
	check, assets := oracleCheck([]string{"cat", "zz_oracle_probe.txt"}, "", "")
	res := runOracle(context.Background(), dir, check, assets, "")
	if !res.Passed {
		t.Fatalf("oracle should pass; detail=%q", res.Detail)
	}
	if _, err := os.Stat(filepath.Join(dir, "zz_oracle_probe.txt")); !os.IsNotExist(err) {
		t.Fatalf("materialised asset leaked: stat err=%v", err)
	}
}

// TestOracleFailurePropagates: a non-zero argv exit is a failed check, and the
// asset is still cleaned up.
func TestOracleFailurePropagates(t *testing.T) {
	dir := t.TempDir()
	check, assets := oracleCheck([]string{"false"}, "", "")
	res := runOracle(context.Background(), dir, check, assets, "")
	if res.Passed {
		t.Fatal("oracle with a failing argv must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "zz_oracle_probe.txt")); !os.IsNotExist(err) {
		t.Fatal("materialised asset leaked after failure")
	}
}

// TestOracleExclusiveCreateRefusesOverwrite: an agent-authored file already at
// a reserved destination is NOT clobbered — the check fails loudly and the
// pre-existing bytes survive.
func TestOracleExclusiveCreateRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "zz_oracle_probe.txt")
	if err := os.WriteFile(victim, []byte("agent-authored"), 0o644); err != nil {
		t.Fatal(err)
	}
	check, assets := oracleCheck([]string{"true"}, "", "")
	res := runOracle(context.Background(), dir, check, assets, "")
	if res.Passed {
		t.Fatal("oracle must fail when its destination already exists")
	}
	if !strings.Contains(res.Detail, "overwrite") {
		t.Errorf("detail should explain the refusal: %q", res.Detail)
	}
	body, err := os.ReadFile(victim)
	if err != nil || string(body) != "agent-authored" {
		t.Fatalf("pre-existing file was clobbered or removed: body=%q err=%v", body, err)
	}
}

// TestOracleLeakFreeOnTimeout: a hung argv is killed on ctx expiry and the
// materialised asset is still removed (cleanup runs on every exit path).
func TestOracleLeakFreeOnTimeout(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	check, assets := oracleCheck([]string{"sleep", "30"}, "", "")
	start := time.Now()
	res := runOracle(ctx, dir, check, assets, "")
	if res.Passed {
		t.Fatal("a killed oracle must not pass")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("oracle was not killed promptly: %v", elapsed)
	}
	if _, err := os.Stat(filepath.Join(dir, "zz_oracle_probe.txt")); !os.IsNotExist(err) {
		t.Fatal("materialised asset leaked after timeout kill")
	}
}

// TestOraclePackageDirCreatedAndCleaned: a missing package_dir is created for
// materialisation and removed afterward, leaving the root untouched.
func TestOraclePackageDirCreatedAndCleaned(t *testing.T) {
	dir := t.TempDir()
	// argv runs with cwd = the solution root; the asset lands under package_dir.
	check, assets := oracleCheck([]string{"cat", "sub/pkg/zz_oracle_probe.txt"}, "sub/pkg", "")
	res := runOracle(context.Background(), dir, check, assets, "")
	if !res.Passed {
		t.Fatalf("oracle should pass with a created package_dir; detail=%q", res.Detail)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Fatal("created package_dir tree leaked")
	}
}

// TestOraclePackageDirSymlinkRejected: a symlinked package_dir component in the
// agent-controlled solution is rejected at materialise time.
func TestOraclePackageDirSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	check, assets := oracleCheck([]string{"true"}, "link", "")
	res := runOracle(context.Background(), dir, check, assets, "")
	if res.Passed {
		t.Fatal("a symlinked package_dir component must be rejected")
	}
	if !strings.Contains(res.Detail, "symlink") {
		t.Errorf("detail should name the symlink: %q", res.Detail)
	}
}

// TestOracleCleanupFailureFailsClosed: a passing argv that leaves a stray file
// inside a created package_dir makes os.Remove of that dir fail. The check must
// then be reported as FAILED (fail-closed), not passed — a leak cannot coexist
// with success — and the detail must name the cleanup failure.
func TestOracleCleanupFailureFailsClosed(t *testing.T) {
	dir := t.TempDir()
	// argv succeeds but drops a stray file into the created package_dir, so the
	// deepest-first os.Remove of "sub/pkg" fails (non-empty).
	check, assets := oracleCheck([]string{"sh", "-c", "cat sub/pkg/zz_oracle_probe.txt && : > sub/pkg/leak.txt"}, "sub/pkg", "")
	res := runOracle(context.Background(), dir, check, assets, "")
	if res.Passed {
		t.Fatal("a cleanup failure must fail the check closed, not report success")
	}
	if !strings.Contains(res.Detail, "cleanup failed") {
		t.Errorf("detail should name the cleanup failure: %q", res.Detail)
	}
}

// TestOracleScratchCleanupAfterTimeout: even when the helper is killed on ctx
// expiry, the engine's bounded, fresh-context scratch teardown still runs and
// unregisters the worktree.
func TestOracleScratchCleanupAfterTimeout(t *testing.T) {
	dir, head := gitInit(t, map[string]string{"solution.txt": "done"})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	check, assets := oracleCheck([]string{"sleep", "30"}, "", story.OracleScratchSolutionCommit)
	res := runOracle(ctx, dir, check, assets, head)
	if res.Passed {
		t.Fatal("a killed scratch oracle must not pass")
	}
	out, err := exec.Command("git", "-C", dir, "worktree", "list").Output()
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(out)), "\n"); lines != 0 {
		t.Fatalf("scratch worktree leaked after timeout:\n%s", out)
	}
}

// TestRemoveWorktreeFailsClosed: the scratch teardown surfaces a git removal
// failure (here, an unregistered path) rather than swallowing it, so the caller
// can fail the check closed.
func TestRemoveWorktreeFailsClosed(t *testing.T) {
	dir, _ := gitInit(t, map[string]string{"x.txt": "hi"})
	err := removeWorktree(dir, filepath.Join(dir, "not-a-registered-worktree"))
	if err == nil {
		t.Fatal("removing an unregistered worktree must return an error, not fail open")
	}
}

// TestOraclePackageDirTraversalRejected: a package_dir that escapes the
// solution root via `..` is rejected at materialise time, independent of the
// load-time lexical check — the materialiser must not walk a `..` segment
// upward out of the root.
func TestOraclePackageDirTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(filepath.Dir(dir), "zz_oracle_probe.txt")
	for _, pd := range []string{"..", "sub/../..", "../escape"} {
		check, assets := oracleCheck([]string{"true"}, pd, "")
		res := runOracle(context.Background(), dir, check, assets, "")
		if res.Passed {
			t.Fatalf("package_dir %q must be rejected as escaping the root", pd)
		}
		if !strings.Contains(res.Detail, "escapes") {
			t.Errorf("package_dir %q: detail should name the escape: %q", pd, res.Detail)
		}
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("package_dir %q materialised an asset outside the root", pd)
		}
	}
}

// TestRunProcessEnvOverrideWins: an override key already present in the parent
// environment must be the value the child sees — no duplicate key survives to be
// resolved first-match against us.
func TestRunProcessEnvOverrideWins(t *testing.T) {
	t.Setenv("ORACLE_SCRATCH", "PARENT_WRONG")
	_, out := runProcess(context.Background(), t.TempDir(),
		[]string{"ORACLE_SCRATCH=CHILD_RIGHT"}, "probe",
		"sh", "-c", "printf %s \"$ORACLE_SCRATCH\"")
	if !strings.Contains(out, "CHILD_RIGHT") || strings.Contains(out, "PARENT_WRONG") {
		t.Fatalf("child must see the override, got %q", out)
	}
}

// TestOracleScratchMode proves the load-bearing scratch properties in one run:
// $ORACLE_SCRATCH is a clean checkout of the solution commit (carries the
// solution file), the oracle asset is NOT in the scratch (it lives only in the
// tool dir, which is cwd), and afterward the scratch worktree is unregistered
// from the repo — no leak.
func TestOracleScratchMode(t *testing.T) {
	dir, head := gitInit(t, map[string]string{"solution.txt": "done"})
	// The script runs with cwd = the tool dir. It asserts: the scratch carries
	// the solution, the scratch does NOT carry the oracle asset, and the asset
	// IS present at cwd (the tool dir).
	script := `test -f "$ORACLE_SCRATCH/solution.txt" && ` +
		`test ! -e "$ORACLE_SCRATCH/zz_oracle_probe.txt" && ` +
		`test -f zz_oracle_probe.txt`
	check, assets := oracleCheck([]string{"sh", "-c", script}, "", story.OracleScratchSolutionCommit)
	res := runOracle(context.Background(), dir, check, assets, head)
	if !res.Passed {
		t.Fatalf("scratch oracle should pass; detail=%q", res.Detail)
	}
	// The scratch worktree must be gone from the repo's registration.
	out, err := exec.Command("git", "-C", dir, "worktree", "list").Output()
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(out)), "\n"); lines != 0 {
		t.Fatalf("scratch worktree leaked in registration:\n%s", out)
	}
}
