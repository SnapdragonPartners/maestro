package stack

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// buildArchive produces a complete archive the way publishArchive does,
// without needing Docker.
func buildArchive(t *testing.T, cfg *Config, destination string) {
	t.Helper()
	if err := publishArchive(cfg, destination); err != nil {
		t.Fatalf("publishArchive: %v", err)
	}
}

// The manifest is what makes a directory an archive, and it is written
// last. That ordering is the whole completion protocol: it means a
// directory carrying a manifest is by construction a finished copy.
func TestPublishArchiveWritesManifestLast(t *testing.T) {
	cfg := planeAt(t)
	mustMkdir(t, filepath.Join(cfg.Roots.Data, "postgres"))
	mustWrite(t, filepath.Join(cfg.Roots.Data, "postgres", "PG_VERSION"), []byte("18\n"))

	destination := filepath.Join(t.TempDir(), "archive")
	buildArchive(t, cfg, destination)

	manifest, err := ReadManifest(destination)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if manifest.Format != ArchiveFormat {
		t.Errorf("format = %d, want %d", manifest.Format, ArchiveFormat)
	}
	if manifest.SourceRoot != cfg.Roots.Data {
		t.Errorf("source root = %q, want %q", manifest.SourceRoot, cfg.Roots.Data)
	}

	// The copied root lives under data/, so the wrapper can hold the
	// manifest beside it rather than inside the tree being restored.
	body, err := os.ReadFile(filepath.Join(destination, ArchiveDataDir, "postgres", "PG_VERSION"))
	if err != nil || string(body) != "18\n" {
		t.Fatalf("copied content = %q, err = %v", body, err)
	}

	var postgres *ManifestEntry
	for i := range manifest.Entries {
		if manifest.Entries[i].Name == "postgres" {
			postgres = &manifest.Entries[i]
		}
	}
	if postgres == nil {
		t.Fatal("manifest does not inventory the postgres directory")
	}
	if postgres.Files != 1 || postgres.Bytes != 3 {
		t.Errorf("inventory = %d files / %d bytes, want 1 / 3", postgres.Files, postgres.Bytes)
	}
}

// A killed backup cannot run its own cleanup, so its residue survives —
// and restore must refuse it. This is the case that makes validity a
// property of contents rather than of a path: the residue is a real
// directory holding a real, partial data tree.
func TestReadManifestRefusesAKilledBackupResidue(t *testing.T) {
	residue := filepath.Join(t.TempDir(), ".maestro-backup-1234")
	mustMkdir(t, filepath.Join(residue, ArchiveDataDir, "postgres"))
	mustWrite(t, filepath.Join(residue, ArchiveDataDir, "postgres", "PG_VERSION"), []byte("18\n"))

	_, err := ReadManifest(residue)
	if !errors.Is(err, ErrArchiveIncomplete) {
		t.Errorf("err = %v, want a refusal: the tree looks like a plane but the copy never finished", err)
	}
}

func TestReadManifestRefusesUnreadableAndForeignFormats(t *testing.T) {
	t.Run("corrupt json", func(t *testing.T) {
		archive := t.TempDir()
		mustWrite(t, filepath.Join(archive, ArchiveManifest), []byte("{not json"))
		if _, err := ReadManifest(archive); !errors.Is(err, ErrArchiveIncomplete) {
			t.Errorf("err = %v, want a refusal", err)
		}
	})
	t.Run("future format", func(t *testing.T) {
		archive := t.TempDir()
		mustWrite(t, filepath.Join(archive, ArchiveManifest), []byte(`{"format": 99}`))
		if _, err := ReadManifest(archive); !errors.Is(err, ErrArchiveIncomplete) {
			t.Errorf("err = %v, want a refusal rather than a misread", err)
		}
	})
}

// The destination must not exist. An empty-but-present directory was the
// earlier rule and is not enough: it cannot distinguish "nothing here yet"
// from "something already published here".
func TestPublishRefusesAnExistingDestination(t *testing.T) {
	cfg := planeAt(t)
	destination := filepath.Join(t.TempDir(), "archive")
	mustMkdir(t, destination)

	err := Backup(t.Context(), cfg, DefaultComposeFile, destination)
	if !errors.Is(err, ErrDestinationExists) {
		t.Errorf("err = %v, want a refusal before anything is stopped", err)
	}
}

// Overlap is refused before the plane is touched, so a mistyped
// destination cannot stop the stack as a side effect.
func TestBackupRefusesOverlappingDestination(t *testing.T) {
	cfg := planeAt(t)

	err := Backup(t.Context(), cfg, DefaultComposeFile, filepath.Join(cfg.Roots.Data, "archive"))
	if !errors.Is(err, ErrPathOverlap) {
		t.Errorf("err = %v, want an overlap refusal", err)
	}
}

// A torn restore must not become an archive somebody later restores from.
func TestBackupRefusesATornRestore(t *testing.T) {
	cfg := planeAt(t)
	mustWrite(t, markerPath(cfg), []byte("{}"))

	err := Backup(t.Context(), cfg, DefaultComposeFile, filepath.Join(t.TempDir(), "archive"))
	if !errors.Is(err, ErrRestoreIncomplete) {
		t.Errorf("err = %v, want a refusal: backing up a torn plane launders it into an archive", err)
	}
}

// The archive carries no root-of-trust key, and this asserts the property
// directly rather than trusting that backup never reads one: the key's
// bytes must appear nowhere under the archive.
//
// Asserted on content, not on filename. A filename check would pass for an
// archive that copied the key's bytes into a differently named file, which
// is the failure mode worth catching.
func TestArchiveContainsNoKeyMaterial(t *testing.T) {
	cfg := planeAt(t)
	// A recognisable key-shaped secret placed where the real one lives.
	key := []byte("SUPER-SECRET-ROOT-KEY-MATERIAL-0123456789")
	mustWrite(t, cfg.Roots.KeyPath(), key)
	mustMkdir(t, filepath.Join(cfg.Roots.Data, "postgres"))
	mustWrite(t, filepath.Join(cfg.Roots.Data, "postgres", "PG_VERSION"), []byte("18\n"))

	destination := filepath.Join(t.TempDir(), "archive")
	buildArchive(t, cfg, destination)

	if err := filepath.Walk(destination, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(body, key) {
			t.Errorf("%s contains the root-of-trust key", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk the archive: %v", err)
	}
}
