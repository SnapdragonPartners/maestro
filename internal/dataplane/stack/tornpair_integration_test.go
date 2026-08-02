//go:build integration

package stack

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A torn pair is the failure this whole item is defended against, and it is
// the one a structural check cannot see.
//
// An archive holding a torn pair passes every cheap test there is: the
// manifest is honest, the inventory matches, every service directory is
// present, the cluster starts, and the attachment row is exactly where it
// should be. Only recomputing digests across both stores says otherwise.
// These tests are what stand between that archive and a plane somebody
// builds on.

// TestRestoreRefusesATornArchive is the restore-level assertion: a restore
// that copied a torn pair must NOT report success.
//
// The three consequences are asserted together because each alone is a
// half-measure. Reporting an error while leaving the plane running gives
// clients a plane the operator has been told is broken; leaving it stopped
// without the marker gives the next `up` a plane it will happily start; and
// keeping the marker without the error hides the whole thing.
func TestRestoreRefusesATornArchive(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	seed := seedCrossStore(t, cfg)
	tearTheSeededPair(t, cfg, seed)

	// Backup does not verify -- it copies what is there -- so the archive
	// carries the tear. That is the point: an operator reaching for an
	// archive is by definition someone whose live plane is already in
	// trouble, and nothing about this one announces itself.
	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if _, err := ReadManifest(archive); err != nil {
		t.Fatalf("a torn archive has no valid manifest, so the structural checks would have caught "+
			"it and this test would prove nothing about verification: %v", err)
	}

	if err := Reset(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	err := Restore(t.Context(), cfg, testComposeFile(), archive, true)
	if !errors.Is(err, ErrRestoreUnverified) {
		t.Fatalf("Restore = %v, want a refusal: the archive's object does not hash to the digest "+
			"addressing it, and a restore that reports success here hands over a broken plane", err)
	}

	// The marker survives, because a plane that failed verification is one
	// nobody should build on -- and the marker is the only thing that says so
	// across process boundaries.
	if _, statErr := os.Stat(markerPath(cfg)); statErr != nil {
		t.Errorf("the incomplete marker is gone after a failed verification (%v): the next `up` "+
			"would start the plane this restore just condemned", statErr)
	}
	if got := runningServices(t, cfg); len(got) != 0 {
		t.Errorf("an unverified plane is running: %v. The marker stops the lifecycle verbs and does "+
			"nothing to a client holding a connection string", got)
	}
}

// TestTwoPartRestoreOfATornArchiveStopsThePlane is the same tear taken
// through the branch that cannot check it at the time.
//
// ADR 0022's two-part restore completes its copy and then CANNOT verify: the
// plane will not open without its key. Clearing the incomplete marker and
// stopping there -- which an earlier implementation did -- let a torn pair go
// live, because the operator supplies the key, `up` starts the plane, and
// nothing ever recomputes a digest.
//
// So the debt is carried across the two parts, and `up` settles it. This
// asserts the settlement can FAIL, and what happens when it does: the plane
// stops rather than serving, and the debt survives for the next attempt.
// A verification that detected the tear and left the plane running would be
// the worst of both outcomes -- the operator sees an error while clients keep
// using the plane it condemns.
func TestTwoPartRestoreOfATornArchiveStopsThePlane(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	seed := seedCrossStore(t, cfg)
	tearTheSeededPair(t, cfg, seed)

	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// The key set aside and taken away: the "restored onto a new machine"
	// case, where the operator has the backup and not the key.
	keyPath := cfg.Roots.KeyPath()
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read the key: %v", err)
	}
	if err := Reset(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove the key: %v", err)
	}

	// Part one completes: the tree is whole, the plane is locked, and the
	// restore could not look inside it. It must not have reported the tear,
	// because it had no way to see it -- that is precisely why the debt
	// exists.
	restoreErr := Restore(t.Context(), cfg, testComposeFile(), archive, true)
	if !errors.Is(restoreErr, ErrPlaneLocked) {
		t.Fatalf("Restore = %v, want the restored plane to report itself locked", restoreErr)
	}
	owed, owedErr := restoreOwesVerification(cfg)
	if owedErr != nil || !owed {
		t.Fatalf("a restore that skipped verification recorded no debt (owed = %v, err = %v): "+
			"this torn pair would go live through that branch", owed, owedErr)
	}

	// Part two: the key arrives and `up` is the first moment anything can
	// look. It looks, and it does not like what it finds.
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatalf("restore the key: %v", err)
	}
	upErr := Up(t.Context(), cfg, testComposeFile())
	if !errors.Is(upErr, ErrRestoreUnverifiedPending) {
		t.Fatalf("Up = %v, want the outstanding verification to fail on a torn pair", upErr)
	}

	if got := runningServices(t, cfg); len(got) != 0 {
		t.Errorf("the plane is running after failing the verification it owed: %v. Detecting a torn "+
			"pair and leaving it serving is worse than not detecting it", got)
	}
	// And the debt is RETAINED. Clearing it on a failed settlement would
	// make the next `up` a clean start over a plane nothing has ever
	// checked -- the tear laundered by one failed attempt.
	stillOwed, stillErr := restoreOwesVerification(cfg)
	if stillErr != nil || !stillOwed {
		t.Errorf("the verification debt was cleared by a settlement that FAILED (owed = %v, err = %v): "+
			"the next `up` would start this plane owing nothing", stillOwed, stillErr)
	}

	// And the debt is worth something: it stops the verb that would turn this
	// plane into an archive. `backup` works perfectly well on a stopped
	// plane -- that is a tested case -- so nothing else stands between a
	// stopped, owing, torn plane and a copy of itself that a later operator
	// restores from in an emergency.
	backupErr := Backup(t.Context(), cfg, testComposeFile(), filepath.Join(t.TempDir(), "archive"))
	if !errors.Is(backupErr, ErrRestoreUnverifiedPending) {
		t.Errorf("Backup = %v, want a refusal against a plane that owes verification", backupErr)
	}
}
