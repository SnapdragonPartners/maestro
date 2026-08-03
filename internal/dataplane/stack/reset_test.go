package stack

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"orchestrator/internal/dataplane/paths"
)

// inodeOf identifies a directory by identity rather than by path, which is
// what the bind-mount rule is actually about: a recreated directory at the
// same path is a different directory as far as an existing mount is
// concerned.
func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no inode information on this platform")
	}
	return stat.Ino
}

// Reset and freshness are two halves of one definition, so the property
// worth testing is their COMPOSITION: whatever reset leaves behind, the
// freshness rule must call fresh. Testing each half separately would let
// them drift apart while both stayed green — reset clearing what it always
// cleared, freshness reading more than it used to, and the two disagreeing
// only on a real machine at `up`.
func TestResetLeavesTheRootFresh(t *testing.T) {
	cfg := planeAt(t)
	root := cfg.Roots.Data

	// Everything a data root can plausibly hold at reset time.
	for _, service := range paths.Services() {
		dir := filepath.Join(root, string(service))
		mustMkdir(t, dir)
		mustWrite(t, filepath.Join(dir, "PG_VERSION"), []byte("18\n"))
		mustMkdir(t, filepath.Join(dir, "base"))
		mustWrite(t, filepath.Join(dir, "base", "1"), []byte("x"))
	}
	// An unregistered service's directory: the Phase 3 forge, arriving
	// before anyone adds it to the registry.
	mustMkdir(t, filepath.Join(root, "forge"))
	mustWrite(t, filepath.Join(root, "forge", "HEAD"), []byte("ref: refs/heads/main\n"))
	// A stray file, a restore marker, and the lock.
	mustWrite(t, filepath.Join(root, ".DS_Store"), []byte("x"))
	mustWrite(t, filepath.Join(root, RestoreIncompleteMarker), []byte("{}"))
	mustWrite(t, filepath.Join(root, LifecycleLockFile), nil)

	before := map[string]uint64{}
	for _, dir := range []string{root, filepath.Join(root, "postgres"), filepath.Join(root, "forge")} {
		before[dir] = inodeOf(t, dir)
	}

	if err := clearDataRoot(cfg); err != nil {
		t.Fatalf("clearDataRoot: %v", err)
	}

	fresh, err := dataRootIsEmpty(cfg)
	if err != nil {
		t.Fatalf("dataRootIsEmpty: %v", err)
	}
	if !fresh {
		evidence, _ := planeEvidence(cfg)
		t.Fatalf("root is not fresh after reset; surviving evidence: %v", evidence)
	}

	// Bind-mount sources keep their identity. A delete-and-recreate would
	// pass the freshness assertion above and still break a live mount.
	for dir, ino := range before {
		if got := inodeOf(t, dir); got != ino {
			t.Errorf("%s inode changed %d -> %d: bind mounts would still point at the old directory", dir, ino, got)
		}
	}

	// The lock is held across a real reset and is never unlinked.
	if _, err := os.Stat(filepath.Join(root, LifecycleLockFile)); err != nil {
		t.Errorf("lifecycle lock did not survive reset: %v", err)
	}
}

// A directory that exists only in the live root — the case restore also has
// to handle — is emptied rather than removed.
func TestResetEmptiesUnregisteredDirectoriesInPlace(t *testing.T) {
	cfg := planeAt(t)
	dir := filepath.Join(cfg.Roots.Data, "forge")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "HEAD"), []byte("x"))
	before := inodeOf(t, dir)

	if err := clearDataRoot(cfg); err != nil {
		t.Fatalf("clearDataRoot: %v", err)
	}

	if got := inodeOf(t, dir); got != before {
		t.Errorf("inode changed %d -> %d, want the directory emptied in place", before, got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("directory still holds %d entries", len(entries))
	}
}
