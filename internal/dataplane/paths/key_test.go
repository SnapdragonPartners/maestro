package paths

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// TestLoadKeySweepsOrphanTemporaries covers the crash window a load-only
// plane would otherwise preserve forever.
//
// EnsureKey's protocol links its temporary into place and removes it
// afterwards, so a creator that dies in between leaves an orphan: a complete
// second copy of the key, at a predictable name. Once the final key exists,
// EnsureKey never runs its creating path again — so on a plane that only ever
// LOADS, nothing would ever collect it. It would survive for the life of the
// installation, in every backup, and in every copy of the data root.
func TestLoadKeySweepsOrphanTemporaries(t *testing.T) {
	root := t.TempDir()

	created, err := EnsureKey(root)
	if err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	orphan := filepath.Join(root, KeyFileName+".tmp-diedhere")
	if writeErr := os.WriteFile(orphan, []byte("a second copy of the key\n"), keyPerm); writeErr != nil {
		t.Fatalf("plant an orphan: %v", writeErr)
	}

	loaded, err := LoadKey(root)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if string(loaded) != string(created) {
		t.Fatal("LoadKey returned a different key")
	}
	if _, statErr := os.Stat(orphan); !os.IsNotExist(statErr) {
		t.Fatalf("the orphaned temporary survived a load (%v); on a plane that only ever loads, "+
			"nothing else will ever remove this second copy of the key", statErr)
	}
}

// TestSweepReportsEveryUnremovedOrphan pins the rule that a sweep which
// cannot finish reports ALL of what it left behind, not just the last thing
// it tripped over.
//
// Each surviving temporary is an independent second copy of the key. Naming
// one of them and dropping the rest is worse than useless: it tells an
// operator the cleanup they must do by hand, understating it, and the copies
// that go unmentioned are the ones that stay on disk forever.
//
// The orphans here are non-empty DIRECTORIES at the temporary name. They
// match the glob, and os.Remove refuses them with ENOTEMPTY — a removal
// failure that needs no permission games and so behaves the same for an
// unprivileged user, for root, and on both Linux and macOS.
func TestSweepReportsEveryUnremovedOrphan(t *testing.T) {
	root := t.TempDir()

	stuck := []string{
		filepath.Join(root, KeyFileName+".tmp-stuckA"),
		filepath.Join(root, KeyFileName+".tmp-stuckB"),
	}
	for _, dir := range stuck {
		if err := os.MkdirAll(filepath.Join(dir, "occupant"), 0o700); err != nil {
			t.Fatalf("plant an unremovable orphan: %v", err)
		}
	}

	err := sweepOrphanTemps(root)
	if err == nil {
		t.Fatal("sweep reported success while two orphans survived")
	}
	for _, dir := range stuck {
		if !strings.Contains(err.Error(), filepath.Base(dir)) {
			t.Errorf("orphan %s survived but is missing from the error:\n%v",
				filepath.Base(dir), err)
		}
	}
}

// TestSweepKeepsRemovalErrorsAlongsideRemovableOnes is the mixed case: the
// sweep clears what it can and still reports what it could not.
//
// A partial sweep that reports nothing reads as a clean one.
func TestSweepKeepsRemovalErrorsAlongsideRemovableOnes(t *testing.T) {
	root := t.TempDir()

	removable := filepath.Join(root, KeyFileName+".tmp-removable")
	if err := os.WriteFile(removable, []byte("a second copy of the key\n"), keyPerm); err != nil {
		t.Fatalf("plant a removable orphan: %v", err)
	}
	stuck := filepath.Join(root, KeyFileName+".tmp-stuck")
	if err := os.MkdirAll(filepath.Join(stuck, "occupant"), 0o700); err != nil {
		t.Fatalf("plant an unremovable orphan: %v", err)
	}

	err := sweepOrphanTemps(root)
	if err == nil {
		t.Fatal("sweep reported success while an orphan survived")
	}
	if !strings.Contains(err.Error(), filepath.Base(stuck)) {
		t.Errorf("the surviving orphan is missing from the error:\n%v", err)
	}
	if _, statErr := os.Stat(removable); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the removable orphan survived a failing sweep (%v); one stuck orphan "+
			"must not stop the others from being collected", statErr)
	}
}

// TestLoadKeySweepsEvenWhenRefusing is the half that is easy to leave out:
// the sweep must happen, and be made durable, on the path that returns
// ErrNoKey too.
//
// That path is a restored data root whose key file is missing — and if an
// orphan is present there, it is the only copy of the key that still exists.
// Removing it without syncing the directory is a removal a crash can undo,
// resurrecting exactly what was just deleted.
func TestLoadKeySweepsEvenWhenRefusing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create root: %v", err)
	}

	orphan := filepath.Join(root, KeyFileName+".tmp-diedhere")
	if err := os.WriteFile(orphan, []byte("orphaned key material\n"), keyPerm); err != nil {
		t.Fatalf("plant an orphan: %v", err)
	}

	if _, err := LoadKey(root); !errors.Is(err, ErrNoKey) {
		t.Fatalf("LoadKey returned %v, want ErrNoKey", err)
	}
	if _, statErr := os.Stat(orphan); !os.IsNotExist(statErr) {
		t.Fatalf("the orphaned temporary survived a refusing load (%v)", statErr)
	}
}
