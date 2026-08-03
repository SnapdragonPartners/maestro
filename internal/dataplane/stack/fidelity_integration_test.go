//go:build integration

package stack

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"orchestrator/internal/dataplane/paths"
)

// The properties a round trip must preserve that are invisible in its
// CONTENTS: inode identity, permission bits, and the project's own state.
//
// Each of these is already asserted against the helper that implements it —
// copyTree's modes, copyArchiveInto's inodes — and that is not the same
// claim. A helper test says the helper is right; it says nothing about
// whether the exported verb calls it, calls it with the right arguments, or
// does something afterwards that undoes it. This package has already been
// bitten by exactly that: a marker test asserted the clear/copy helpers'
// behaviour and stayed green when restore switched to the sweep that
// destroys the marker.
//
// So these run the real verbs against a real plane, and assert the property
// on the tree those verbs left behind.

// TestRestorePreservesEveryInodeThroughTheExportedVerb is test-plan item 6
// at the level the plan states it: "across a restore", not across a call to
// the copier.
//
// Inode identity is what bind mounts resolve. A restore that recreated the
// data root or a service directory would satisfy every content assertion in
// this suite and break every live mount — on macOS silently, because the old
// inode keeps working for whoever already holds it. The lock file matters
// for a second reason: unlinking a HELD lock lets another process lock a
// fresh inode at the same path, producing two simultaneous "exclusive"
// holders (ADR 0027).
func TestRestorePreservesEveryInodeThroughTheExportedVerb(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	seed := seedCrossStore(t, cfg)

	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Captured from the LIVE root, immediately before the restore that must
	// preserve them. Every path a mount or a lock can be holding.
	watched := []string{cfg.Roots.Data, filepath.Join(cfg.Roots.Data, LifecycleLockFile)}
	for _, service := range paths.Services() {
		watched = append(watched, filepath.Join(cfg.Roots.Data, string(service)))
	}
	before := make(map[string]uint64, len(watched))
	for _, path := range watched {
		before[path] = inodeOf(t, path)
	}

	// Restored OVER the populated root rather than after a reset: that is
	// the case where inodes can be lost, and the case an operator is
	// actually in.
	if err := Restore(t.Context(), cfg, testComposeFile(), archive, true); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	for _, path := range watched {
		if after := inodeOf(t, path); after != before[path] {
			t.Errorf("%s changed inode across the restore (%d → %d): any bind mount or held lock "+
				"still points at the old one", path, before[path], after)
		}
	}

	// And the restore actually did something, so the inodes are not
	// preserved for the trivial reason that nothing was replaced.
	assertCrossStoreIntact(t, cfg, seed)
}

// TestRestorePreservesModesThroughTheExportedVerb is test-plan item 14 at
// round-trip level.
//
// `os.CopyFS` does not merely fail to preserve modes, it WIDENS them: under
// a typical umask the 0700 storage roots come back 0755 and the 0600 cluster
// files come back 0644. A backup/restore cycle that quietly relaxed the
// permissions on the directory holding the Postgres cluster and the object
// store is the failure this asserts against — and `Roots.Ensure` would then
// refuse the plane on the next `up`, so the damage surfaces far from its
// cause.
//
// The modes asserted are the ones the design states, not the ones observed:
// a test written to match whatever the tree happened to have would pass for
// a widened tree as readily as a correct one.
func TestRestorePreservesModesThroughTheExportedVerb(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := Restore(t.Context(), cfg, testComposeFile(), archive, true); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// The roots and every service directory: 0700, the mode item 2's
	// ErrRootPermissions refuses to see relaxed.
	directories := []string{cfg.Roots.Data}
	for _, service := range paths.Services() {
		directories = append(directories, filepath.Join(cfg.Roots.Data, string(service)))
	}
	for _, path := range directories {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s is %04o after a round trip, want 0700: a widening copier relaxes exactly "+
				"this, and the next `up` would refuse the plane", path, got)
		}
	}

	// And a real cluster file, which Postgres writes 0600. PG_VERSION is
	// chosen because initdb always writes it and nothing later rewrites it.
	//
	// LOCATED rather than named: the cluster lives at the container's PGDATA
	// inside the mount, which the Compose file decides. Spelling that path
	// here would be a second copy of a setting that already exists, and a
	// copy that goes stale silently -- the stat would fail and read as a
	// broken test rather than a moved cluster.
	clusterFile := ""
	postgresRoot := filepath.Join(cfg.Roots.Data, string(paths.ServicePostgres))
	if err := filepath.WalkDir(postgresRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "PG_VERSION" {
			clusterFile = path
		}
		return nil
	}); err != nil {
		t.Fatalf("look for the restored cluster under %s: %v", postgresRoot, err)
	}
	if clusterFile == "" {
		t.Fatalf("no PG_VERSION anywhere under %s: the restore did not put a Postgres cluster back, "+
			"so the mode assertion below would have nothing to check", postgresRoot)
	}
	info, err := os.Stat(clusterFile)
	if err != nil {
		t.Fatalf("stat %s: %v", clusterFile, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("%s is %04o after a round trip, want 0600", clusterFile, got)
	}

	// Deliberately NOT asserted: the modes MinIO writes inside its own data
	// directory, which are 0755 and 0644 and always have been. Those are
	// MinIO's, not this copier's -- the contract here is PRESERVATION, and a
	// copier that normalised them would be as wrong as one that widened the
	// roots. A first version of this test walked the whole tree demanding
	// nothing group-readable anywhere and failed on fourteen `.minio.sys`
	// entries that were faithfully preserved, which is the copier working.
	//
	// The two assertions above are the ones the plan states and the ones
	// that matter: they are the modes item 2's ErrRootPermissions refuses to
	// see relaxed, and they are exactly what os.CopyFS would widen.
}

// TestBackupOfAPartlyRunningPlaneRestoresThatExactState is the third case of
// test-plan item 12, and the one neither of the others can stand in for.
//
// Backup promises to return the project to the state it FOUND. With every
// service running, "start everything" satisfies that; with none running,
// "start nothing" does. Only a partly running project distinguishes those
// two wrong answers from the right one, which is to start exactly what was
// up before.
func TestBackupOfAPartlyRunningPlaneRestoresThatExactState(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	env, err := cfg.composeEnv(placeholderKey())
	if err != nil {
		t.Fatalf("compose env: %v", err)
	}
	// One service down, the other left running. MinIO rather than Postgres
	// so the plane keeps a service that a naive "restart everything" would
	// also have running -- a test that stopped ALL but one would pass for a
	// backup that restarted nothing.
	if err := compose(t.Context(), cfg.ProjectName, testComposeFile(), env,
		"stop", "--timeout", "60", string(paths.ServiceMinIO)); err != nil {
		t.Fatalf("stop one service: %v", err)
	}

	before := runningServices(t, cfg)
	if len(before) != 1 {
		t.Fatalf("running services = %v, want exactly one: this test needs a PARTLY running project, "+
			"and neither of the other two states exercises what it exercises", before)
	}

	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	if after := runningServices(t, cfg); !slices.Equal(before, after) {
		t.Errorf("running services = %v, want exactly %v: backup restarted a service the project did "+
			"not have running, or failed to restart one it did", after, before)
	}
	if _, err := ReadManifest(archive); err != nil {
		t.Fatalf("archive has no valid manifest: %v", err)
	}
}

// TestCancelledBackupStillRestartsThePlane is test-plan item 13.
//
// Ctrl-C cancels the operation context, and a deferred restart INHERITING
// that context would be cancelled before it ran — turning an interrupted
// backup into a stopped plane, which is the precise outcome the deferred
// restart exists to prevent. The restart therefore derives its own context
// with context.WithoutCancel plus a timeout, and this is what says so.
//
// The cancellation is sequenced against observed state rather than a sleep:
// the test waits until the stop has actually begun to take services down,
// which is the earliest moment at which there is something to restart. A
// timer would cancel before the stop on a fast machine and after the copy on
// a slow one, and would pass either way while testing neither.
func TestCancelledBackupStillRestartsThePlane(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	before := runningServices(t, cfg)
	if len(before) == 0 {
		t.Fatal("nothing is running before the backup; this test cannot show a restart")
	}

	// A copy long enough to be interrupted.
	//
	// Without this the test races a fast machine and loses: a freshly
	// initialised plane copies in well under a second, so the cancellation
	// lands after the backup has already finished and the test fails
	// reporting that it proved nothing. Padding the data root is legitimate
	// rather than a trick -- D1's whole point is that the root is copied
	// wholesale, whatever is in it -- and it widens the window to seconds,
	// because every copied file is fsynced.
	padding := make([]byte, 8<<20)
	for i := range padding {
		padding[i] = byte(i)
	}
	for chunk := range 24 {
		name := filepath.Join(cfg.Roots.Data, string(paths.ServiceMinIO), fmt.Sprintf("padding-%02d", chunk))
		if err := os.WriteFile(name, padding, 0o600); err != nil {
			t.Fatalf("pad the data root: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Backup(ctx, cfg, testComposeFile(), filepath.Join(t.TempDir(), "archive"))
	}()

	// Wait for the quiesce to bite, then cancel. Until something has
	// stopped, cancelling would only prove that a backup which did nothing
	// left the plane alone.
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if len(runningServices(t, cfg)) < len(before) {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("the backup never stopped any service, so there was nothing for a cancelled " +
				"restart to put back")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The backup must still be RUNNING when the cancellation arrives, or
	// this cancels nothing and the test is vacuous. This is the check the
	// padding above exists to make pass.
	select {
	case backupErr := <-done:
		t.Fatalf("the backup finished (err = %v) before the cancellation could be delivered: nothing "+
			"was interrupted, so the restart path under test was never taken. The data root needs "+
			"more padding for this machine", backupErr)
	default:
	}
	cancel()
	backupErr := <-done

	// Backup is NOT required to fail here, and asserting that it does would
	// be asserting something the design never promised: copyTree takes no
	// context, so a cancellation arriving mid-copy does not interrupt the
	// copy at all. What the design promises is about the PLANE.
	//
	// So the load-bearing assertion is the state of the project. A restart
	// inheriting the cancelled context would be cancelled before it ran,
	// leaving these services down -- and, because the restart's error is
	// joined into the result, turning a backup that actually completed into
	// a reported failure with a stopped plane behind it.
	if after := runningServices(t, cfg); !slices.Equal(before, after) {
		t.Errorf("running services = %v, want %v (Backup returned %v): an interrupted backup left the "+
			"plane stopped, which is exactly what deriving the restart's context with "+
			"context.WithoutCancel exists to prevent", after, before, backupErr)
	}
}
