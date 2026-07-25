package paths

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// KeyFileName is the root-of-trust key file, under the config root.
	// The secrets vault is encrypted inside the data plane, so its unlock
	// key can never live there; this file is the external anchor.
	KeyFileName = "root-of-trust.key"

	// keyLen is the raw key length in bytes.
	keyLen = 32

	// keyPerm is the exact mode the key file must have. Anything wider is
	// treated as a possible exposure rather than something to quietly fix.
	keyPerm fs.FileMode = 0o600
)

// ErrKeyPermissions reports a key file whose permissions are not exactly
// 0600. It is a distinct error because the correct operator response is to
// investigate a possible key exposure, not to retry.
var ErrKeyPermissions = errors.New("root-of-trust key file has unsafe permissions")

// EnsureKey returns the root-of-trust key from the config root, generating
// it on first use.
//
// Generation is silent and requires no user ceremony: unattended operation
// disqualifies a passphrase default, so the shipped root of trust is a
// Maestro-generated key file (the ssh model). Keychain and passphrase
// backends are opt-in upgrades behind the auth-module interface.
//
// Creation is write-then-atomically-link, so two concurrent callers cannot
// race two different keys into existence and cannot observe a partial one.
// Plain O_EXCL is NOT sufficient here and the distinction is easy to miss:
// it makes *creation* atomic, not creation-plus-write, so a loser can find
// the file already present and read it before the winner has written a
// byte. Writing to a temporary file first and linking it into place closes
// that window — os.Link is atomic and fails rather than overwrites, so a
// reader sees either no file or the complete one, and the winner's key is
// never replaced. This is ADR 0027's rule at file granularity: the shared
// resource is the path, and recovery must not destroy a writer's work.
//
// Every successful return, on every path, satisfies this protocol in order:
//
//  1. the final link exists;
//  2. this caller's temporary link has been removed, successfully;
//  3. the containing directory has been synced, after both of the above.
//
// The ordering is the point. Removing the temporary after the sync would
// leave the removal itself non-durable, so a crash could resurrect a second
// copy of the key; and syncing only on the creating path would let a losing
// caller return a key whose directory entry is not yet durable.
func EnsureKey(configRoot string) ([]byte, error) {
	path := filepath.Join(configRoot, KeyFileName)

	if err := os.MkdirAll(configRoot, rootPerm); err != nil {
		return nil, fmt.Errorf("create config root %s: %w", configRoot, err)
	}

	// Fast path: an existing key needs no generation at all. It still syncs
	// before returning — see returnDurable.
	if _, err := os.Stat(path); err == nil {
		return returnDurable(configRoot, path)
	}

	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate root-of-trust key: %w", err)
	}

	tmp, err := writeTempKey(configRoot, hex.EncodeToString(key)+"\n")
	if err != nil {
		return nil, err
	}

	linkErr := os.Link(tmp, path)

	// Step 2 of the protocol, before any sync and before any return: drop
	// our own temporary link, and verify it. On success it is a spare link
	// to the key's inode; on failure it is a stray key. Either way a
	// surviving temporary is a second copy of a secret on disk, so an
	// unchecked removal is not good enough — and it must happen before the
	// directory sync, or the removal itself is not crash-durable.
	if rmErr := os.Remove(tmp); rmErr != nil {
		//nolint:wrapcheck // linkErr is already wrapped or nil; Join only combines them.
		return nil, errors.Join(linkErr, fmt.Errorf("remove temporary key file %s: %w", tmp, rmErr))
	}

	switch {
	case linkErr == nil:
		if syncErr := syncDir(configRoot); syncErr != nil {
			return nil, syncErr
		}
		return key, nil
	case errors.Is(linkErr, fs.ErrExist):
		// Another caller won. Its file is complete by construction, since
		// it was only linked into place after being written and flushed.
		return returnDurable(configRoot, path)
	default:
		return nil, fmt.Errorf("install root-of-trust key %s: %w", path, linkErr)
	}
}

// returnDurable reads the key and makes its directory entry durable before
// handing it back.
//
// Every path that returns a key carries this obligation, not just the one
// that created it. A caller that loses the link race — or simply finds an
// existing file — can observe the winner's freshly linked entry before the
// winner has synced the directory. It would then return a key that a crash
// could still erase, and it may already have encrypted a vault under it.
// The obligation belongs to whoever returns the key, because that is who
// causes it to be used.
func returnDurable(configRoot, path string) ([]byte, error) {
	key, err := readKey(path)
	if err != nil {
		return nil, err
	}
	if err := syncDir(configRoot); err != nil {
		return nil, err
	}
	return key, nil
}

// writeTempKey writes the encoded key to a temporary file in dir and
// returns its path. The content is flushed before the caller links it into
// place: losing this key after the vault is encrypted under it is
// unrecoverable, and the backup deliberately does not contain a copy.
func writeTempKey(dir, encoded string) (string, error) {
	f, err := os.CreateTemp(dir, KeyFileName+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temporary key file in %s: %w", dir, err)
	}
	name := f.Name()

	// os.CreateTemp already uses 0600, but the key file's mode is a
	// contract that readKey enforces, so set it explicitly rather than
	// inheriting it by luck.
	if err := f.Chmod(keyPerm); err != nil {
		_ = f.Close()
		return "", discardTemp(name, fmt.Errorf("set key file permissions: %w", err))
	}
	if _, err := f.WriteString(encoded); err != nil {
		_ = f.Close()
		return "", discardTemp(name, fmt.Errorf("write key file: %w", err))
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", discardTemp(name, fmt.Errorf("flush key file: %w", err))
	}
	if err := f.Close(); err != nil {
		return "", discardTemp(name, fmt.Errorf("close key file: %w", err))
	}
	return name, nil
}

// discardTemp removes a partial temporary key file, joining any removal
// failure onto the error being returned rather than dropping it. A
// surviving temporary is a second copy of the key on disk, so its removal
// is reported even when it is cleanup for another failure.
func discardTemp(name string, cause error) error {
	if err := os.Remove(name); err != nil {
		//nolint:wrapcheck // Both operands are already wrapped; Join only combines them.
		return errors.Join(cause, fmt.Errorf("remove temporary key file %s: %w", name, err))
	}
	return cause
}

// syncDir flushes a directory's own entries, making a rename or link into
// it durable. Opening a directory read-only and calling Sync is the
// portable POSIX idiom for this on both Linux and macOS.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s for sync: %w", dir, err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return fmt.Errorf("sync directory %s: %w", dir, err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("close %s after sync: %w", dir, err)
	}
	return nil
}

// readKey loads an existing key file, refusing it if the permissions are
// wider than 0600 or the contents are not a well-formed key.
func readKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat root-of-trust key %s: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm != keyPerm {
		// Do not chmod it into compliance: a key that was readable is a key
		// that may have leaked, and repairing it silently destroys the only
		// evidence that it happened.
		return nil, fmt.Errorf("%w: %s is %#o, want %#o", ErrKeyPermissions, path, perm, keyPerm)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read root-of-trust key %s: %w", path, err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("decode root-of-trust key %s: %w", path, err)
	}
	if len(key) != keyLen {
		return nil, fmt.Errorf("root-of-trust key %s is %d bytes, want %d", path, len(key), keyLen)
	}
	return key, nil
}
