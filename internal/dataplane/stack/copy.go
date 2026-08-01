package stack

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupportedFileType reports an entry the copier will not reproduce.
var ErrUnsupportedFileType = errors.New("unsupported file type in the data root")

// ErrPathOverlap reports a source and destination that are the same tree,
// or one inside the other.
var ErrPathOverlap = errors.New("source and destination overlap")

// syncMode says whether a copy must reach the disk before it is reported
// as done.
type syncMode bool

const (
	// syncContents fsyncs every file and directory written. Backup uses it:
	// the archive has to survive the power loss it exists to protect
	// against, and an archive that is only in the page cache is not one.
	syncContents syncMode = true
	// noSync leaves flushing to the kernel. Restore uses it: the plane is
	// started and exercised immediately afterwards, and paying an fsync per
	// file across a whole cluster buys nothing a crash would not already
	// force a re-restore for.
	noSync syncMode = false
)

// copyTree copies src to dst, preserving permissions exactly.
//
// It is hand-rolled because os.CopyFS cannot do this. In Go 1.26.3
// (src/os/dir.go) CopyFS creates directories with MkdirAll(path, 0777) and
// files with OpenFile(..., 0666|mode&0777), both BEFORE umask — so it does
// not merely fail to preserve modes, it WIDENS them. Under a typical
// umask 022 the 0700 storage roots come back 0755 and the 0600 cluster
// files come back 0644, and Roots.Ensure would then reject the restored
// root on the next `up`. The permission tests are written to fail against
// CopyFS so this cannot quietly regress.
//
// os.Chmod after creation is what actually sets the bits: the mode passed
// to MkdirAll and OpenFile is masked by umask, and Chmod is not.
//
// Ownership needs no handling. Compose runs both services as
// ${MAESTRO_UID}:${MAESTRO_GID} — the invoking user — so a same-user
// restore preserves ownership naturally. A cross-user restore is out of
// scope and fails at the permission checks rather than being half-supported.
func copyTree(src, dst string, sync syncMode) error {
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		relative, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return fmt.Errorf("relativise %s against %s: %w", path, src, relErr)
		}
		target := filepath.Join(dst, relative)

		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("stat %s: %w", path, infoErr)
		}

		switch {
		case entry.IsDir():
			return copyDir(target, info.Mode().Perm(), sync)
		case entry.Type()&fs.ModeSymlink != 0:
			return copySymlink(path, target)
		case entry.Type().IsRegular():
			return copyFile(path, target, info.Mode().Perm(), sync)
		default:
			// Sockets, FIFOs, and device nodes. Refusing is deliberate: a
			// silently skipped entry is the same failure D1's whole-root copy
			// exists to avoid, one level down — a backup that reports success
			// and is missing something.
			return fmt.Errorf("%w: %s is %s", ErrUnsupportedFileType, path, entry.Type())
		}
	})
	if err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return nil
}

func copyDir(target string, perm fs.FileMode, sync syncMode) error {
	if err := os.MkdirAll(target, perm); err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	// Not redundant with MkdirAll's argument: that one is masked by umask,
	// and MkdirAll is a no-op on a directory that already exists.
	if err := os.Chmod(target, perm); err != nil {
		return fmt.Errorf("set mode on %s: %w", target, err)
	}
	if sync == syncContents {
		return syncDir(target)
	}
	return nil
}

func copySymlink(path, target string) error {
	destination, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("read link %s: %w", path, err)
	}
	// The link's own target is reproduced verbatim rather than resolved: a
	// relative link inside the tree must stay relative, or restoring to a
	// different root would silently repoint it.
	if err := os.Symlink(destination, target); err != nil {
		return fmt.Errorf("create link %s: %w", target, err)
	}
	return nil
}

func copyFile(path, target string, perm fs.FileMode, sync syncMode) (err error) {
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = source.Close() }()

	// O_EXCL: the copier never overwrites. Restore clears its destination
	// first, so an existing file here means two entries claimed one path.
	destination, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	defer func() {
		if closeErr := destination.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", target, closeErr)
		}
	}()

	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("copy %s to %s: %w", path, target, err)
	}
	if err := os.Chmod(target, perm); err != nil {
		return fmt.Errorf("set mode on %s: %w", target, err)
	}
	if sync == syncContents {
		if err := destination.Sync(); err != nil {
			return fmt.Errorf("sync %s: %w", target, err)
		}
	}
	return nil
}

// refuseOverlap rejects a source and destination that are the same tree or
// nested one inside the other.
//
// Both directions are hazardous and neither is exotic. A backup destination
// under the data root makes the copy include its own output, recursively. A
// restore source under the data root is deleted by the restore before it is
// read — the operator's archive destroyed by the operation meant to consume
// it.
//
// Symlinks are evaluated along the WHOLE ancestry, not just at the leaf,
// because a destination whose parent is a symlink into the data root passes
// every string comparison and is still inside it. The paths are canonical
// before they are compared, and comparison is on path SEGMENTS so that
// /data-backup is not read as living inside /data.
func refuseOverlap(source, destination string) error {
	canonicalSource, err := canonicalPath(source)
	if err != nil {
		return err
	}
	canonicalDestination, err := canonicalPath(destination)
	if err != nil {
		return err
	}

	switch {
	case canonicalSource == canonicalDestination:
		return fmt.Errorf("%w: both resolve to %s", ErrPathOverlap, canonicalSource)
	case isAncestor(canonicalSource, canonicalDestination):
		return fmt.Errorf("%w: %s is inside %s", ErrPathOverlap, canonicalDestination, canonicalSource)
	case isAncestor(canonicalDestination, canonicalSource):
		return fmt.Errorf("%w: %s is inside %s", ErrPathOverlap, canonicalSource, canonicalDestination)
	}
	return nil
}

// canonicalPath resolves a path with symlinks evaluated, tolerating a leaf
// that does not exist yet.
//
// A backup destination is required NOT to exist, so resolving it directly
// would always fail. Its existing ancestry is what has to be resolved: that
// is where a symlink could hide, and the non-existent leaf cannot be one.
func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}

	remainder := ""
	current := absolute
	for {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			return filepath.Join(resolved, remainder), nil
		}
		if !errors.Is(evalErr, os.ErrNotExist) {
			return "", fmt.Errorf("resolve %s: %w", path, evalErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the filesystem root without finding anything that
			// exists, which on a sane system means the path is unusable.
			return absolute, nil
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

// isAncestor reports whether child lies under parent, comparing whole path
// segments so /data does not appear to contain /data-backup.
func isAncestor(parent, child string) bool {
	if parent == child {
		return false
	}
	prefix := parent
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(child, prefix)
}
