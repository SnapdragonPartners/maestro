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
