package paths

import (
	"bytes"
	"errors"
	"io/fs"
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

	assertOnlyKeyFile(t, root)
}

// assertOnlyKeyFile fails if anything beyond the key and its lock file
// survives in the config root. The creation protocol writes a temporary
// file and links it into place, so a leftover is a second copy of a secret
// on disk. The allow-list is exact rather than a "no .tmp-" filter, so a
// future file that nobody thought about also trips it.
func assertOnlyKeyFile(t *testing.T, root string) {
	t.Helper()
	allowed := map[string]bool{KeyFileName: true, lockFileName: true}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read config root: %v", err)
	}
	seenKey := false
	for _, e := range entries {
		if !allowed[e.Name()] {
			t.Errorf("unexpected file %q in config root: a leftover temporary is a second copy of the key", e.Name())
		}
		if e.Name() == KeyFileName {
			seenKey = true
		}
	}
	if !seenKey {
		t.Errorf("config root has no %s", KeyFileName)
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

// Concurrent setup must not race two keys into existence: callers are
// serialized, and whoever arrives second reads the first one's key rather
// than minting its own, so every caller comes away with the same bytes.
//
// This covers contention, not crash-durability. The ordering guarantees in
// EnsureKey's protocol only matter if the machine dies inside a specific
// window, which no in-process test can produce.
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
	// may survive.
	assertOnlyKeyFile(t, root)
}

// A creator that dies between writing its temporary and removing it leaves
// a second copy of a key on disk. The next EnsureKey holds the lock, so any
// temporary it sees can only be such an orphan, and it must be swept.
func TestEnsureKeySweepsOrphanedTemporaries(t *testing.T) {
	tests := []struct {
		name      string
		keyExists bool // whether a key is already present when the orphan appears
	}{
		{name: "orphan with no key yet", keyExists: false},
		{name: "orphan alongside an existing key", keyExists: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.keyExists {
				if _, err := EnsureKey(root); err != nil {
					t.Fatalf("seed EnsureKey: %v", err)
				}
			}

			orphan := filepath.Join(root, KeyFileName+".tmp-orphaned")
			if err := os.WriteFile(orphan, []byte("00112233\n"), keyPerm); err != nil {
				t.Fatalf("plant orphan: %v", err)
			}

			if _, err := EnsureKey(root); err != nil {
				t.Fatalf("EnsureKey: %v", err)
			}
			if _, err := os.Stat(orphan); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("orphaned temporary survived (stat err: %v)", err)
			}
			assertOnlyKeyFile(t, root)
		})
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
