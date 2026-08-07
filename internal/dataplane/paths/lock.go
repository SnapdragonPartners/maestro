package paths

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// AcquireLock takes an exclusive, cross-process advisory lock on path,
// creating the lock file if needed, and returns a release function.
//
// Exported because writers span packages: ADR 0027's mirror-lock lesson is
// that a package-private lock silently readmits the race as soon as a
// second package touches the same resource. Callers outside this package
// use it to serialize data-plane lifecycle operations on the data root.
//
// The lock is NOT re-entrant. flock is per open file description, so a
// second acquisition in the same process blocks against the first — a
// caller that already holds it must not take it again.
func AcquireLock(path string) (func() error, error) {
	return acquireLock(path, syscall.LOCK_EX)
}

// AcquireSharedLock takes a SHARED advisory lock on path, so that several
// holders may coexist while excluding every exclusive holder.
//
// It exists for the difference between USING a resource and moving it
// between states. Data-plane lifecycle operations take the exclusive lock
// because two of them must never overlap; ordinary use -- an import, a read,
// anything that runs against a plane while it is up -- must be able to
// overlap with itself and must NOT overlap with a reset or a restore. A
// preflight check without a held lock cannot express that: it reports what
// was true a moment ago, and the destructive operation is free to start
// immediately afterwards.
//
// The same non-re-entrancy warning applies, and one more: flock is per open
// file description, so a caller already holding the EXCLUSIVE lock on this
// path deadlocks against itself by asking for the shared one.
func AcquireSharedLock(path string) (func() error, error) {
	return acquireLock(path, syscall.LOCK_SH)
}

// acquireLock takes an exclusive, cross-process advisory lock on path,
// creating the lock file if needed, and returns a release function.
//
// The lock file is deliberately NEVER unlinked. Unlinking it while another
// process holds the lock lets that process keep a lock on an orphaned inode
// while a third creates a fresh file at the same path and locks that — two
// simultaneous "exclusive" holders. An empty lock file is harmless and must
// outlive every holder.
func acquireLock(path string, mode int) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, lockPerm)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), mode); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	return func() error {
		unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		closeErr := f.Close()
		if unlockErr != nil || closeErr != nil {
			return errors.Join(
				wrapLockErr("release lock", path, unlockErr),
				wrapLockErr("close lock file", path, closeErr),
			)
		}
		return nil
	}, nil
}

func wrapLockErr(what, path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %s: %w", what, path, err)
}
