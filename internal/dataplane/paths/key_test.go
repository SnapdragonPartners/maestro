package paths

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEnsureKeyCreatesOnce(t *testing.T) {
	root := t.TempDir()

	key, err := EnsureKey(root)
	if err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	if len(key) != keyLen {
		t.Fatalf("key is %d bytes, want %d", len(key), keyLen)
	}
	if bytes.Equal(key, make([]byte, keyLen)) {
		t.Fatal("key is all zeros")
	}

	path := filepath.Join(root, KeyFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != keyPerm {
		t.Errorf("key file mode %#o, want %#o", perm, keyPerm)
	}

	// A second call must return the same key, not mint a new one — the
	// vault encrypted under the first key would otherwise be unreadable.
	again, err := EnsureKey(root)
	if err != nil {
		t.Fatalf("second EnsureKey: %v", err)
	}
	if !bytes.Equal(key, again) {
		t.Error("second EnsureKey returned a different key")
	}
}

func TestEnsureKeyCreatesConfigRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does", "not", "exist")
	if _, err := EnsureKey(root); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, KeyFileName)); err != nil {
		t.Fatalf("key not created: %v", err)
	}
}

// Concurrent setup must not race two keys into existence. Losers of the
// O_EXCL race read the winner's key rather than overwriting it, so every
// caller must come away with the same bytes.
func TestEnsureKeyConcurrent(t *testing.T) {
	root := t.TempDir()

	const callers = 8
	keys := make([][]byte, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			keys[i], errs[i] = EnsureKey(root)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	for i := 1; i < callers; i++ {
		if !bytes.Equal(keys[0], keys[i]) {
			t.Fatalf("caller %d got a different key than caller 0", i)
		}
	}

	// Losers of the link race write a temporary file first; none of them
	// may survive. A stray temp file is a second copy of a secret sitting
	// in the config root.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read config root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != KeyFileName {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("config root holds %v, want only %q", names, KeyFileName)
	}
}

func TestEnsureKeyRefusesWidePermissions(t *testing.T) {
	root := t.TempDir()
	if _, err := EnsureKey(root); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	path := filepath.Join(root, KeyFileName)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, err := EnsureKey(root)
	if !errors.Is(err, ErrKeyPermissions) {
		t.Fatalf("got %v, want ErrKeyPermissions", err)
	}

	// The file must be left exactly as found: repairing it would destroy
	// the evidence that the key was briefly readable.
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("permissions changed to %#o; EnsureKey must not repair them", perm)
	}
}

func TestEnsureKeyRejectsMalformed(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "not hex", contents: "zzzz\n"},
		{name: "too short", contents: "abcd\n"},
		{name: "empty", contents: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, KeyFileName)
			if err := os.WriteFile(path, []byte(tc.contents), keyPerm); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := EnsureKey(root); err == nil {
				t.Fatal("expected an error for a malformed key, got nil")
			}
		})
	}
}
