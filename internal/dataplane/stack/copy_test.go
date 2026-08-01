package stack

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Modes must survive a copy exactly.
//
// This is the assertion that disqualifies os.CopyFS, and it is written so
// that swapping the copier for CopyFS fails it: CopyFS creates directories
// 0777 and files 0666|mode&0777 before umask, so under umask 022 the 0700
// below comes back 0755 and the 0600 comes back 0644. Those are not
// cosmetic — Roots.Ensure refuses a storage root whose permissions are not
// 0700, so a restored plane would fail its next `up`, and a widened cluster
// file is a real exposure on a shared machine.
func TestCopyTreePreservesModes(t *testing.T) {
	source := t.TempDir()
	nested := filepath.Join(source, "postgres", "base")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// MkdirAll's mode is masked by umask, so set the bits explicitly to
	// establish the fixture the assertion depends on.
	for _, dir := range []string{filepath.Join(source, "postgres"), nested} {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("chmod: %v", err)
		}
	}
	secretFile := filepath.Join(nested, "PG_VERSION")
	mustWrite(t, secretFile, []byte("18\n"))
	if err := os.Chmod(secretFile, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	executable := filepath.Join(source, "postgres", "tool")
	mustWrite(t, executable, []byte("#!/bin/sh\n"))
	if err := os.Chmod(executable, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(source, destination, noSync); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for path, want := range map[string]os.FileMode{
		filepath.Join(destination, "postgres"):                       0o700,
		filepath.Join(destination, "postgres", "base"):               0o700,
		filepath.Join(destination, "postgres", "base", "PG_VERSION"): 0o600,
		filepath.Join(destination, "postgres", "tool"):               0o755,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %#o, want %#o", path, got, want)
		}
	}
}

func TestCopyTreeCopiesContentAndLinks(t *testing.T) {
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "data"), []byte("hello"))
	if err := os.Symlink("data", filepath.Join(source, "relative")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(source, destination, syncContents); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(destination, "data"))
	if err != nil || string(body) != "hello" {
		t.Fatalf("content = %q, err = %v", body, err)
	}

	// The link's own target is reproduced rather than resolved, so a
	// relative link keeps pointing inside the restored tree instead of back
	// at the original one.
	link, err := os.Readlink(filepath.Join(destination, "relative"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if link != "data" {
		t.Errorf("link resolves to %q, want the original relative target %q", link, "data")
	}
}

// An entry the copier cannot reproduce must stop the operation, not be
// skipped. A skipped entry is a backup that reports success and is missing
// something — the failure the whole-root copy exists to prevent, one level
// down.
func TestCopyTreeRefusesUnsupportedFileTypes(t *testing.T) {
	source := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(source, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}

	err := copyTree(source, filepath.Join(t.TempDir(), "copy"), noSync)
	if !errors.Is(err, ErrUnsupportedFileType) {
		t.Errorf("err = %v, want a refusal naming the unsupported entry", err)
	}
}

// Overlap is refused in both directions, and through symlinked ancestry,
// which is the case a string comparison passes.
func TestRefuseOverlap(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(data, "postgres"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sibling := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A symlink whose PARENT is the link, so the leaf name reveals nothing.
	linkedParent := filepath.Join(root, "link-to-data")
	if err := os.Symlink(data, linkedParent); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tests := []struct {
		name                string
		source, destination string
		wantOverlap         bool
	}{
		{name: "disjoint siblings", source: data, destination: filepath.Join(sibling, "archive")},
		{name: "identical paths", source: data, destination: data, wantOverlap: true},
		{name: "destination inside source", source: data, destination: filepath.Join(data, "archive"), wantOverlap: true},
		{name: "source inside destination", source: filepath.Join(data, "postgres"), destination: data, wantOverlap: true},
		{
			// The case that motivates canonicalising the whole ancestry: the
			// destination does not exist yet and its parent is a symlink into
			// the data root.
			name:        "destination under a symlinked parent",
			source:      data,
			destination: filepath.Join(linkedParent, "archive"),
			wantOverlap: true,
		},
		{
			name:        "source reached through a symlink",
			source:      linkedParent,
			destination: filepath.Join(data, "archive"),
			wantOverlap: true,
		},
		{
			// Segment-wise comparison: a sibling whose name merely starts
			// with the source's name is not inside it.
			name:        "adjacent name is not containment",
			source:      data,
			destination: data + "-backup",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := refuseOverlap(test.source, test.destination)
			switch {
			case test.wantOverlap && !errors.Is(err, ErrPathOverlap):
				t.Errorf("err = %v, want an overlap refusal", err)
			case !test.wantOverlap && err != nil:
				t.Errorf("err = %v, want the pair accepted", err)
			}
		})
	}
}
