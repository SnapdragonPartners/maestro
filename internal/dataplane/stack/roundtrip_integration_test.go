//go:build integration

package stack

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"orchestrator/internal/dataplane/paths"
)

// openPlane connects to an isolated plane's Postgres.
func openPlane(t *testing.T, cfg *Config) *sql.DB {
	t.Helper()
	rootKey, err := paths.EnsureKey(cfg.Roots.Config)
	if err != nil {
		t.Fatalf("read the root-of-trust key: %v", err)
	}
	dsn, err := cfg.DSN(rootKey)
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping the isolated plane: %v", err)
	}
	return db
}

// runningServices reports which of the project's containers are up.
func runningServices(t *testing.T, cfg *Config) []string {
	t.Helper()
	env, err := cfg.composeEnv(placeholderKey())
	if err != nil {
		t.Fatalf("compose env: %v", err)
	}
	state, err := readProjectState(t.Context(), cfg.ProjectName, testComposeFile(), env)
	if err != nil {
		t.Fatalf("read project state: %v", err)
	}
	return state.running
}

// The criterion itself: data written before a backup is readable after a
// reset and a restore.
//
// Asserted on real cluster contents rather than on file counts, because
// what a backup owes is not "the bytes were copied" but "the plane still
// holds what it held". A tree-comparison test would pass for an archive of
// a cluster that no longer starts.
func TestBackupRestoreRoundTrip(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	db := openPlane(t, cfg)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE roundtrip (value text)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO roundtrip VALUES ('survives the round trip')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// The EXACT set, captured before. "Something is running" would pass for
	// a backup that restarted Postgres and left MinIO down — a plane that
	// answers connections and has no object store.
	before := runningServices(t, cfg)
	if len(before) == 0 {
		t.Fatal("nothing is running before the backup; this test cannot show a restart")
	}

	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	if after := runningServices(t, cfg); !slices.Equal(before, after) {
		t.Errorf("running services = %v, want exactly %v: backup must return the project to the state it found",
			after, before)
	}
	if _, err := ReadManifest(archive); err != nil {
		t.Fatalf("archive has no valid manifest: %v", err)
	}

	if err := Reset(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Restore runs verification internally and refuses an unhealthy plane,
	// so a success here is also the first end-to-end exercise of verify
	// against a real Postgres and MinIO.
	if err := Restore(t.Context(), cfg, testComposeFile(), archive, true); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored := openPlane(t, cfg)
	var value string
	if err := restored.QueryRowContext(t.Context(), `SELECT value FROM roundtrip`).Scan(&value); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if value != "survives the round trip" {
		t.Errorf("value = %q, want the row written before the backup", value)
	}

	// A completed restore leaves no marker, or every later verb would
	// refuse the plane.
	if _, err := os.Stat(markerPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore left its incomplete marker behind: %v", err)
	}
}

// A stopped plane is backed up without being started, and is still stopped
// afterwards.
//
// This is the case round 3 of review found: `compose start` only works on
// containers that still exist, and `dataplane-down` removes them, so a
// design that assumed a running project would copy successfully and then
// fail the restart it promised.
func TestBackupOfAStoppedPlaneLeavesItStopped(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := Down(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup of a stopped plane: %v", err)
	}
	if _, err := ReadManifest(archive); err != nil {
		t.Fatalf("archive has no valid manifest: %v", err)
	}
	if got := runningServices(t, cfg); len(got) != 0 {
		t.Errorf("backup started %v; the plane was stopped when it began and must be stopped after", got)
	}
}

// ADR 0022's two-part restore: the archive carries no key, so restoring it
// without one produces a plane that is locked rather than broken, and
// supplying the key finishes the sequence.
//
// The failure path IS the requirement, so it is asserted as a sequence
// rather than described in a comment.
func TestTwoPartRestoreNeedsTheKey(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	db := openPlane(t, cfg)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE two_part (value text)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Keep the key aside, then take it away — the "restored onto a new
	// machine" case, where the operator has the backup and not the key.
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

	restoreErr := Restore(t.Context(), cfg, testComposeFile(), archive, true)
	if !errors.Is(restoreErr, ErrPlaneLocked) {
		t.Fatalf("err = %v, want the restored plane to report itself locked", restoreErr)
	}
	// A locked plane is a COMPLETED restore awaiting its second part, so the
	// marker is gone — otherwise the `up` that finishes the sequence would
	// refuse a plane that is merely locked.
	if _, statErr := os.Stat(markerPath(cfg)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a locked-but-complete restore left its marker: %v", statErr)
	}
	if got := runningServices(t, cfg); len(got) != 0 {
		t.Errorf("a plane that could not be opened is running: %v", got)
	}

	// Part two: supply the key, and the sequence completes.
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatalf("restore the key: %v", err)
	}
	// The restore could not verify itself, so it recorded the debt; this is
	// the first moment it can be paid.
	owed, owedErr := restoreOwesVerification(cfg)
	if owedErr != nil || !owed {
		t.Fatalf("a restore that skipped verification recorded no debt (owed = %v, err = %v): "+
			"a torn pair would go live through this branch", owed, owedErr)
	}

	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up after supplying the key: %v", err)
	}

	// And `up` paid it. A plane that still owes verification has not been
	// checked by anything.
	stillOwed, stillErr := restoreOwesVerification(cfg)
	if stillErr != nil || stillOwed {
		t.Errorf("up did not settle the outstanding verification (owed = %v, err = %v)", stillOwed, stillErr)
	}

	reopened := openPlane(t, cfg)
	var exists bool
	if err := reopened.QueryRowContext(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'two_part')`).Scan(&exists); err != nil {
		t.Fatalf("query the reopened plane: %v", err)
	}
	if !exists {
		t.Error("the table written before the backup is gone: the second part opened a different plane")
	}
}

// A restore must not be startable while a torn tree is on disk, and the
// marker is what enforces that across process boundaries.
func TestTornRestoreRefusesEveryUnsafeVerb(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := Down(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	if err := writeRestoreMarker(cfg); err != nil {
		t.Fatalf("writeRestoreMarker: %v", err)
	}

	if err := Up(t.Context(), cfg, testComposeFile()); !errors.Is(err, ErrRestoreIncomplete) {
		t.Errorf("Up = %v, want a refusal against a torn tree", err)
	}
	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); !errors.Is(err, ErrRestoreIncomplete) {
		t.Errorf("Backup = %v, want a refusal: backing up a torn plane launders it into an archive", err)
	}

	// Reset is one of the two ways out, and clears the marker.
	if err := Reset(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Reset against a torn tree: %v", err)
	}
	if _, err := os.Stat(markerPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("reset left the marker: %v", err)
	}
	// And the plane provisions cleanly afterwards, which is the composition
	// that matters rather than either half alone.
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up after reset: %v", err)
	}
}
