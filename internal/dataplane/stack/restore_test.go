package stack

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"orchestrator/internal/dataplane/paths"
)

// archiveFrom builds a complete archive from a plane's current data root.
func archiveFrom(t *testing.T, cfg *Config) string {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "archive")
	if err := publishArchive(cfg, destination); err != nil {
		t.Fatalf("publishArchive: %v", err)
	}
	return destination
}

// populatePlane fills a data root with something shaped like a plane.
func populatePlane(t *testing.T, cfg *Config, marker string) {
	t.Helper()
	for _, service := range paths.Services() {
		dir := filepath.Join(cfg.Roots.Data, string(service))
		mustMkdir(t, dir)
		mustWrite(t, filepath.Join(dir, "CONTENT"), []byte(marker))
	}
}

// The restored tree must be the archive's, and every bind-mount source must
// keep its inode while that happens.
//
// The inode half is the round-1 defect this design was corrected for:
// clearing the data root with a helper that RemoveAll's each child would
// satisfy every content assertion here and still break every live mount,
// silently on macOS, because the old inode keeps working for whoever
// already holds it.
func TestCopyArchiveIntoPreservesInodes(t *testing.T) {
	cfg := planeAt(t)
	populatePlane(t, cfg, "from the archive")
	archive := archiveFrom(t, cfg)

	// Now diverge the live root, so a successful restore is observable.
	populatePlane(t, cfg, "live, to be replaced")
	mustMkdir(t, filepath.Join(cfg.Roots.Data, "forge"))
	mustWrite(t, filepath.Join(cfg.Roots.Data, "forge", "HEAD"), []byte("live only"))
	mustWrite(t, filepath.Join(cfg.Roots.Data, LifecycleLockFile), nil)

	before := map[string]uint64{
		cfg.Roots.Data: inodeOf(t, cfg.Roots.Data),
		filepath.Join(cfg.Roots.Data, "postgres"): inodeOf(t, filepath.Join(cfg.Roots.Data, "postgres")),
		filepath.Join(cfg.Roots.Data, "forge"):    inodeOf(t, filepath.Join(cfg.Roots.Data, "forge")),
	}

	if err := writeRestoreMarker(cfg); err != nil {
		t.Fatalf("writeRestoreMarker: %v", err)
	}
	if err := clearDataRootKeeping(cfg, LifecycleLockFile, RestoreIncompleteMarker); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := copyArchiveInto(cfg, filepath.Join(archive, ArchiveDataDir)); err != nil {
		t.Fatalf("copyArchiveInto: %v", err)
	}

	for _, service := range paths.Services() {
		path := filepath.Join(cfg.Roots.Data, string(service), "CONTENT")
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "from the archive" {
			t.Errorf("%s = %q (err %v), want the archive's content", path, body, err)
		}
	}

	// A directory present only in the live root is emptied, not removed:
	// removing it would break a mount that may already reference it.
	forge := filepath.Join(cfg.Roots.Data, "forge")
	entries, err := os.ReadDir(forge)
	if err != nil {
		t.Fatalf("read %s: %v", forge, err)
	}
	if len(entries) != 0 {
		t.Errorf("live-only directory still holds %d entries, want it emptied in place", len(entries))
	}

	for path, ino := range before {
		if got := inodeOf(t, path); got != ino {
			t.Errorf("%s inode changed %d -> %d: bind mounts would still point at the old directory", path, ino, got)
		}
	}

	// The lock this operation holds is never unlinked; a second holder
	// could otherwise lock a fresh inode at the same path.
	if _, err := os.Stat(filepath.Join(cfg.Roots.Data, LifecycleLockFile)); err != nil {
		t.Errorf("lifecycle lock did not survive the restore: %v", err)
	}
}

// A restore that fails after it starts deleting must leave the marker
// behind, so the torn tree announces itself instead of relying on an
// operator remembering an error message.
//
// This drives replaceTree — the production path — rather than calling the
// clear and copy helpers directly. That distinction is the test: an earlier
// version asserted the helpers' behaviour and stayed green when restore was
// mutated to use the reset sweep, which removes the marker.
func TestRestoreLeavesTheMarkerWhenItFailsMidway(t *testing.T) {
	cfg := planeAt(t)
	populatePlane(t, cfg, "live")

	// An archive whose copy fails partway: the copier refuses file types it
	// cannot reproduce, so a FIFO stops it after the clear has happened.
	archiveData := t.TempDir()
	for _, service := range paths.Services() {
		mustMkdir(t, filepath.Join(archiveData, string(service)))
		mustWrite(t, filepath.Join(archiveData, string(service), "CONTENT"), []byte("archived"))
	}
	if err := mkfifo(filepath.Join(archiveData, "postgres", "pipe")); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}

	err := replaceTree(cfg, archiveData)
	if !errors.Is(err, ErrUnsupportedFileType) {
		t.Fatalf("err = %v, want the copy to fail", err)
	}

	if _, statErr := os.Stat(markerPath(cfg)); statErr != nil {
		t.Fatalf("marker did not survive the failure it exists to record: %v", statErr)
	}
	// A torn tree is not a fresh one, so `up` cannot mint a key over it...
	fresh, freshErr := dataRootIsEmpty(cfg)
	if freshErr != nil {
		t.Fatalf("dataRootIsEmpty: %v", freshErr)
	}
	if fresh {
		t.Error("a torn tree reads as fresh")
	}
	// ...and every unsafe verb refuses it.
	if guardErr := guardRestoreMarker(cfg, lifecycleUp); !errors.Is(guardErr, ErrRestoreIncomplete) {
		t.Errorf("up was not refused against the torn tree: %v", guardErr)
	}
}

// A restore that completes clears the marker, so the plane is usable again
// without an extra step.
func TestRestoreClearsTheMarkerOnSuccess(t *testing.T) {
	source := planeAt(t)
	populatePlane(t, source, "archived")
	archive := archiveFrom(t, source)

	cfg := planeAt(t)
	populatePlane(t, cfg, "live")

	if err := replaceTree(cfg, filepath.Join(archive, ArchiveDataDir)); err != nil {
		t.Fatalf("replaceTree: %v", err)
	}
	if _, err := os.Stat(markerPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("marker survived a completed restore (err = %v); every later verb would refuse the plane", err)
	}
	body, err := os.ReadFile(filepath.Join(cfg.Roots.Data, "postgres", "CONTENT"))
	if err != nil || string(body) != "archived" {
		t.Errorf("content = %q (err %v), want the archive's", body, err)
	}
}

// Every validation happens before the first deletion, so a bad source
// leaves the existing plane exactly as it was.
func TestRestoreValidatesBeforeDeleting(t *testing.T) {
	tests := []struct {
		name    string
		build   func(t *testing.T) string
		wantErr error
	}{
		{
			name:    "no manifest at all",
			build:   func(t *testing.T) string { return t.TempDir() },
			wantErr: ErrArchiveIncomplete,
		},
		{
			name: "a killed backup's residue",
			build: func(t *testing.T) string {
				residue := t.TempDir()
				mustMkdir(t, filepath.Join(residue, ArchiveDataDir, "postgres"))
				return residue
			},
			wantErr: ErrArchiveIncomplete,
		},
		{
			name: "manifest but a missing service directory",
			build: func(t *testing.T) string {
				source := planeAt(t)
				mustMkdir(t, filepath.Join(source.Roots.Data, "postgres"))
				mustWrite(t, filepath.Join(source.Roots.Data, "postgres", "CONTENT"), []byte("x"))
				return archiveFrom(t, source)
			},
			wantErr: ErrArchiveMissingService,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := planeAt(t)
			populatePlane(t, cfg, "must survive")

			err := Restore(t.Context(), cfg, DefaultComposeFile, test.build(t), true)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("err = %v, want %v", err, test.wantErr)
			}

			// The plane is untouched, and no marker was written — nothing
			// destructive began.
			for _, service := range paths.Services() {
				body, readErr := os.ReadFile(filepath.Join(cfg.Roots.Data, string(service), "CONTENT"))
				if readErr != nil || string(body) != "must survive" {
					t.Errorf("%s content = %q (err %v): the existing plane was damaged by a refused restore",
						service, body, readErr)
				}
			}
			if _, statErr := os.Stat(markerPath(cfg)); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("a marker was written for a restore that never started deleting")
			}
		})
	}
}

// A populated root is not replaced without the operator saying so.
func TestRestoreRefusesAPopulatedRootWithoutForce(t *testing.T) {
	source := planeAt(t)
	populatePlane(t, source, "archived")
	archive := archiveFrom(t, source)

	cfg := planeAt(t)
	populatePlane(t, cfg, "live")

	err := Restore(t.Context(), cfg, DefaultComposeFile, archive, false)
	if !errors.Is(err, ErrPopulatedRoot) {
		t.Fatalf("err = %v, want a refusal naming the evidence", err)
	}
	body, readErr := os.ReadFile(filepath.Join(cfg.Roots.Data, "postgres", "CONTENT"))
	if readErr != nil || string(body) != "live" {
		t.Errorf("content = %q (err %v), want the live plane untouched", body, readErr)
	}
}

// A restore source inside the data root would be deleted by the restore
// meant to read it.
func TestRestoreRefusesASourceInsideTheDataRoot(t *testing.T) {
	cfg := planeAt(t)
	source := filepath.Join(cfg.Roots.Data, "archive")
	mustMkdir(t, source)

	err := Restore(t.Context(), cfg, DefaultComposeFile, source, true)
	if !errors.Is(err, ErrPathOverlap) {
		t.Errorf("err = %v, want an overlap refusal", err)
	}
}

// mkfifo creates a named pipe, which the copier refuses — the simplest way
// to make a copy fail after the clear has already happened.
func mkfifo(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
