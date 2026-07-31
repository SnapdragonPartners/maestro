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

	// lockFileName guards the whole key-creation critical section across
	// processes. It holds no data and is never unlinked.
	lockFileName = KeyFileName + ".lock"

	// lockPerm is the mode of any lock file this package creates, including
	// ones held on behalf of other packages via AcquireLock. Lock files
	// carry no secret, but there is no reason for them to be wider than the
	// directories that hold them.
	lockPerm fs.FileMode = 0o600

	// tempPattern matches the temporary files key creation writes before
	// linking them into place.
	tempPattern = KeyFileName + ".tmp-*"
)

// ErrKeyPermissions reports a key file whose permissions are not exactly
// 0600. It is a distinct error because the correct operator response is to
// investigate a possible key exposure, not to retry.
var ErrKeyPermissions = errors.New("root-of-trust key file has unsafe permissions")

// ErrNoKey reports a root-of-trust key that is not present where it was
// expected.
//
// It is the state a data root restored without its key file arrives in, and
// it has to be nameable: the alternative is what the plane did before, which
// was to mint a fresh key, derive a Postgres password that does not match
// the cluster, and fail three minutes later with "data plane did not become
// ready" — a correct refusal reached by accident, diagnosing nothing.
var ErrNoKey = errors.New("root-of-trust key file is not present")

// LoadKey returns the key WITHOUT creating one.
//
// This is the reopening half of item 7's D4: setup may create a key, and
// opening an existing plane may only load one. Which applies is decided by
// the caller, because only the caller knows whether the data root already
// holds a cluster — and getting it wrong in the permissive direction is the
// failure above.
//
// It carries EVERY obligation EnsureKey's fast path carries, not merely the
// convenient ones: the same lock, so it cannot observe a half-linked key from
// a concurrent creator; the same orphan sweep, because a load is just as
// capable of leaving a second copy of a secret on disk as a creation is; and
// the same directory sync before returning, because a caller that encrypts
// under a key must not be handed one a crash could still erase.
//
// The sweep is the obligation easiest to leave out and hardest to notice
// missing. EnsureKey's protocol removes its own temporary AFTER linking, so a
// creator that dies in between leaves an orphan — a complete second copy of
// the key, at a predictable name. Once the final key exists, EnsureKey never
// runs its creating path again, so on a plane that only ever LOADS, nothing
// would ever clean it up: the orphan would survive for the life of the
// installation, in a backup, and in every copy of the data root.
//
// The sweep makes its own removal durable, on every return path — see
// sweepOrphanTemps. An unsynced removal is one a crash can undo, which would
// resurrect exactly the copy it just deleted.
func LoadKey(configRoot string) (key []byte, err error) {
	path := filepath.Join(configRoot, KeyFileName)

	release, lockErr := acquireLock(filepath.Join(configRoot, lockFileName))
	if lockErr != nil {
		return nil, lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			key, err = nil, relErr
		}
	}()

	// Under the lock, any temporary is an orphan: no live creator can exist
	// (ADR 0027 — destructive recovery runs under the resource's own lock, so
	// it cannot truncate a concurrent writer's work).
	if sweepErr := sweepOrphanTemps(configRoot); sweepErr != nil {
		return nil, sweepErr
	}

	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("%w: %s", ErrNoKey, path)
		}
		return nil, fmt.Errorf("stat key file %s: %w", path, statErr)
	}
	return returnDurable(configRoot, path)
}

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
//
// The whole section is serialized by an exclusive lock, across processes as
// well as goroutines, because the link protocol alone cannot uphold step 2.
// Without the lock: A links its temporary into place, B observes the key and
// syncs the directory — durably persisting A's temporary link as well — and
// a crash before A's removal leaves both names on disk permanently. That is
// the second copy the protocol exists to prevent, and no test can expose it,
// since the window only matters if the machine dies inside it. Serializing
// by the resource's identity is ADR 0027's rule; here the resource is the
// key path, so readers cannot return until the creator has finished.
func EnsureKey(configRoot string) (key []byte, err error) {
	path := filepath.Join(configRoot, KeyFileName)

	if mkErr := os.MkdirAll(configRoot, rootPerm); mkErr != nil {
		return nil, fmt.Errorf("create config root %s: %w", configRoot, mkErr)
	}

	release, lockErr := acquireLock(filepath.Join(configRoot, lockFileName))
	if lockErr != nil {
		return nil, lockErr
	}
	defer func() {
		// A failed release is not cosmetic: it means the next caller's
		// serialization guarantee is in doubt, so surface it rather than
		// returning a key as if nothing happened.
		if relErr := release(); relErr != nil && err == nil {
			key, err = nil, relErr
		}
	}()

	// Holding the lock, any temporary can only be an orphan left by a
	// creator that died — no live creator can exist. Removing it is safe
	// for exactly that reason, which is ADR 0027's other half: destructive
	// recovery runs under the resource's lock so it cannot truncate a
	// concurrent writer's work. The sync on the way out makes the removal
	// durable.
	if sweepErr := sweepOrphanTemps(configRoot); sweepErr != nil {
		return nil, sweepErr
	}

	// Fast path: an existing key needs no generation at all. It still syncs
	// before returning — see returnDurable.
	if _, statErr := os.Stat(path); statErr == nil {
		return returnDurable(configRoot, path)
	}

	key = make([]byte, keyLen)
	if _, randErr := rand.Read(key); randErr != nil {
		return nil, fmt.Errorf("generate root-of-trust key: %w", randErr)
	}

	tmp, tmpErr := writeTempKey(configRoot, hex.EncodeToString(key)+"\n")
	if tmpErr != nil {
		return nil, tmpErr
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

// sweepOrphanTemps removes temporary key files left behind by a creator that
// died mid-protocol, AND makes the removals durable, as one operation.
//
// It is only safe under the lock, where no live creator can exist.
//
// The two halves cannot be separated, and separating them is the mistake
// this comment exists to prevent. A removal that is not synced is a removal
// a crash can undo, so a caller that sweeps and then returns down any path
// which does not sync has deleted a second copy of the key only until the
// next power loss. Every later return path in both callers is such a path:
// a stat that fails for a reason other than absence, a key file with the
// wrong permissions, a malformed key.
//
// So the sync happens HERE, immediately, rather than being owed by whatever
// the caller does next. It is skipped when nothing was removed, which is
// every ordinary call — the cost falls only on the rare run that actually
// found an orphan.
func sweepOrphanTemps(dir string) error {
	matches, err := filepath.Glob(filepath.Join(dir, tempPattern))
	if err != nil {
		return fmt.Errorf("scan for orphaned temporary key files in %s: %w", dir, err)
	}
	// The removal error is ACCUMULATED rather than returned where it happens.
	// Returning early would skip the sync below — and by then earlier
	// removals in this same loop have already succeeded, so the failure of
	// one orphan would leave the deletion of the others undurable. A partial
	// sweep still has to be a durable partial sweep.
	var (
		removed   int
		removeErr error
	)
	for _, name := range matches {
		switch err := os.Remove(name); {
		case err == nil:
			removed++
		case !errors.Is(err, fs.ErrNotExist):
			removeErr = fmt.Errorf("remove orphaned temporary key file %s: %w", name, err)
		}
	}

	if removed > 0 {
		if err := syncDir(dir); err != nil {
			// Reported ahead of any removal failure: a removal that did not
			// happen leaves a second copy of the key, while a removal that
			// was not made durable leaves one that can come BACK, and the
			// second is the harder state to reason about afterwards.
			return fmt.Errorf("make the removal of %d orphaned temporary key file(s) durable: %w",
				removed, err)
		}
	}
	return removeErr
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
