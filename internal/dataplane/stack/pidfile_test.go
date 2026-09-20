package stack

import (
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/dataplane/paths"
)

// plantPostmasterPid writes a lock file with the given content into a scratch
// plane's PGDATA and returns its path.
func plantPostmasterPid(t *testing.T, cfg *Config, content []byte) string {
	t.Helper()
	pgDir, err := cfg.Roots.ServiceDataDir(paths.ServicePostgres)
	if err != nil {
		t.Fatalf("resolve the postgres data directory: %v", err)
	}
	pidFile := filepath.Join(pgDir, filepath.FromSlash(postmasterPidFile))
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o700); err != nil {
		t.Fatalf("create PGDATA: %v", err)
	}
	if err := os.WriteFile(pidFile, content, 0o600); err != nil {
		t.Fatalf("plant %s: %v", pidFile, err)
	}
	return pidFile
}

// TestClearEmptyPostmasterPidLeavesAPopulatedLockFileAlone is the safety half
// of #352's repair, and the half a too-eager fix gets wrong.
//
// An EMPTY lock file is a remnant Postgres will never clear. A POPULATED one
// is either a live server's or a stale pid Postgres resolves correctly by
// itself; in both cases it is evidence, and removing it could let a second
// postmaster start over a cluster the first still holds. The function must
// return before it consults Docker at all, which is also what lets this run
// as a unit test: the container name below names nothing.
func TestClearEmptyPostmasterPidLeavesAPopulatedLockFileAlone(t *testing.T) {
	cfg := planeAt(t)
	// The first line of a real lock file is the postmaster's pid.
	content := []byte("1\n/var/lib/postgresql/data/pgdata\n")
	pidFile := plantPostmasterPid(t, cfg, content)

	if err := clearEmptyPostmasterPid(t.Context(), cfg, "no-such-container"); err != nil {
		t.Fatalf("clearEmptyPostmasterPid: %v", err)
	}
	got, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("a populated lock file was removed (%v): that is the unsafe direction", err)
	}
	if string(got) != string(content) {
		t.Fatalf("the lock file was rewritten: got %q, want %q", got, content)
	}
}

// TestClearEmptyPostmasterPidIsANoOpWithoutALockFile covers the ordinary
// case: a cleanly stopped plane has no lock file, and that is not an error.
func TestClearEmptyPostmasterPidIsANoOpWithoutALockFile(t *testing.T) {
	cfg := planeAt(t)
	if err := clearEmptyPostmasterPid(t.Context(), cfg, "no-such-container"); err != nil {
		t.Fatalf("clearEmptyPostmasterPid with no lock file: %v", err)
	}
}
