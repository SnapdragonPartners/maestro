package stack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"orchestrator/internal/dataplane/paths"
)

// ErrPopulatedRoot reports a restore onto a data root that already holds a
// plane, without the operator having said so.
var ErrPopulatedRoot = errors.New("data root already holds a plane")

// ErrArchiveMissingService reports an archive with no directory for a
// service the running build expects.
var ErrArchiveMissingService = errors.New("archive is missing a service directory")

// Restore replaces the data root with an archive's contents.
//
// The whole operation runs under one lifecycle lock — stop, validate,
// clear, copy, restart — because releasing it earlier would let `reset`,
// `migrate`, or a second `restore` act on a half-restored plane, which is
// the hazard ADR 0027 names.
//
// Two things it does NOT do. It does not restore the root-of-trust key:
// the archive never held one, so a plane restored onto a machine without
// the original key comes up locked, which is ADR 0022's documented
// two-part restore rather than a failure. And it does not restart on every
// failure — see the phase boundary below.
func Restore(ctx context.Context, c *Config, composeFile, source string, force bool) (err error) {
	// Everything that can be checked without touching the plane is checked
	// before the plane is touched. The design sequences validation after
	// the stop; doing it first is strictly better and satisfies the same
	// requirement — every check precedes the first deletion — while also
	// meaning a mistyped path cannot stop the stack as a side effect.
	if overlapErr := refuseOverlap(source, c.Roots.Data); overlapErr != nil {
		return overlapErr
	}
	manifest, manifestErr := ReadManifest(source)
	if manifestErr != nil {
		return manifestErr
	}
	archiveData := filepath.Join(source, ArchiveDataDir)
	if validateErr := validateArchiveTree(archiveData, manifest); validateErr != nil {
		return validateErr
	}

	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	if guardErr := guardRestoreMarker(c, lifecycleRestore); guardErr != nil {
		return guardErr
	}
	if populatedErr := refusePopulatedRoot(c, force); populatedErr != nil {
		return populatedErr
	}

	// down, not stop: the restored cluster's password derives from the key
	// that produced the ARCHIVE, so the containers have to be recreated
	// with credentials rendered from it. Stopped containers would keep the
	// environment they were built with and fail to authenticate against
	// what was just restored.
	if downErr := down(ctx, c, composeFile); downErr != nil {
		return downErr
	}

	return replaceDataRoot(ctx, c, composeFile, archiveData)
}

// replaceDataRoot performs the destructive half and everything after it.
//
// The phase boundary lives here, and it is a single point rather than a
// judgement at each step: once the marker is written, every failure leaves
// the plane STOPPED. A partial Postgres/MinIO tree must not be started —
// starting it would present a torn plane as a live one, which is worse
// than the failure that produced it. That is the opposite of backup's
// rule, where the authoritative plane is only ever read and a restart is
// always right.
func replaceDataRoot(ctx context.Context, c *Config, composeFile, archiveData string) error {
	if err := replaceTree(c, archiveData); err != nil {
		return err
	}

	if err := up(ctx, c, composeFile); err != nil {
		if errors.Is(err, ErrPlaneLocked) {
			// The documented two-part restore. The files are in place and
			// the next `dataplane-up` with the key present finishes the
			// sequence; the plane stays stopped until then.
			return fmt.Errorf("restore completed, but the plane cannot be opened: %w", err)
		}
		return err
	}
	return nil
}

// replaceTree is the destructive half: everything between the marker going
// down and the marker coming up.
//
// It is a separate function so the torn window can be exercised without a
// running Docker daemon. That matters more than it looks: a test that
// called the clear and copy helpers itself would assert what the helpers
// do, not what restore does with them — and would therefore stay green if
// restore called the wrong clear. It did stay green, when exactly that
// mutation was tried.
func replaceTree(c *Config, archiveData string) error {
	if err := writeRestoreMarker(c); err != nil {
		return err
	}

	// The marker-preserving clear, not the reset sweep: the marker is
	// written before the first deletion precisely so it survives the
	// deletion, and clearing it here would leave a torn tree announcing
	// nothing.
	if err := clearDataRootKeeping(c, LifecycleLockFile, RestoreIncompleteMarker); err != nil {
		return fmt.Errorf("clear the data root for restore: %w", err)
	}
	if err := copyArchiveInto(c, archiveData); err != nil {
		return fmt.Errorf("copy the archive into %s: %w", c.Roots.Data, err)
	}

	// The tree is whole again, so the plane is no longer torn. Clearing the
	// marker here rather than after `up` is deliberate: a plane that is
	// completely restored but cannot be opened for want of its key is a
	// SUCCESSFUL restore awaiting its second part, and leaving the marker
	// would make every later operation refuse a plane that is merely
	// locked.
	return clearRestoreMarker(c)
}

// validateArchiveTree checks the archive's shape before anything is
// deleted, so a mistyped path or a foreign directory cannot destroy a
// plane.
func validateArchiveTree(archiveData string, manifest Manifest) error {
	info, err := os.Stat(archiveData)
	if err != nil {
		return fmt.Errorf("%w: cannot read %s: %w", ErrArchiveIncomplete, archiveData, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrArchiveIncomplete, archiveData)
	}

	// Every service this build expects must be present. An archive taken by
	// an older build that predates a service is a real possibility, and
	// restoring it would leave that service's bind mount empty — a plane
	// that starts and is quietly missing a store.
	for _, service := range paths.Services() {
		path := filepath.Join(archiveData, string(service))
		if _, statErr := os.Stat(path); statErr != nil {
			return fmt.Errorf("%w: %s has no %s directory", ErrArchiveMissingService, archiveData, service)
		}
	}

	// The manifest's inventory is checked against the tree, so an archive
	// truncated after its manifest was written — which the completion
	// protocol alone cannot catch — is still refused.
	present := map[string]bool{}
	entries, err := os.ReadDir(archiveData)
	if err != nil {
		return fmt.Errorf("read %s: %w", archiveData, err)
	}
	for _, entry := range entries {
		present[entry.Name()] = true
	}
	for i := range manifest.Entries {
		if name := manifest.Entries[i].Name; !present[name] {
			return fmt.Errorf("%w: the manifest lists %q but the tree does not contain it",
				ErrArchiveIncomplete, name)
		}
	}
	return nil
}

// refusePopulatedRoot stops a restore from silently replacing a plane.
func refusePopulatedRoot(c *Config, force bool) error {
	if force {
		return nil
	}
	evidence, err := planeEvidence(c)
	if err != nil {
		return err
	}
	if len(evidence) == 0 {
		return nil
	}
	return fmt.Errorf("%w (%s): %v. Re-run with -force to replace it",
		ErrPopulatedRoot, c.Roots.Data, evidence)
}

// copyArchiveInto reproduces the archive's top-level entries in the data
// root, one entry at a time.
//
// Entry by entry rather than a single tree copy, because the two roots are
// not equivalent: the live root's directories are BIND-MOUNT SOURCES whose
// inodes must survive. copyTree's directory step is MkdirAll followed by
// Chmod, both of which leave an existing directory's inode alone, so an
// existing service directory is reused rather than replaced. A whole-root
// copy that removed and recreated the root would satisfy every content
// assertion and still break every live mount — on macOS silently, since
// the old inode keeps working for whoever already holds it.
//
// The lifecycle lock is never copied over: it is not in the archive's
// top-level set under normal operation, and if a hand-assembled archive
// carried one, restoring it would replace the file this operation is
// currently holding.
func copyArchiveInto(c *Config, archiveData string) error {
	entries, err := os.ReadDir(archiveData)
	if err != nil {
		return fmt.Errorf("read %s: %w", archiveData, err)
	}

	for _, entry := range entries {
		if entry.Name() == LifecycleLockFile {
			continue
		}
		source := filepath.Join(archiveData, entry.Name())
		target := filepath.Join(c.Roots.Data, entry.Name())
		if err := copyTree(source, target, noSync); err != nil {
			return err
		}
	}
	return nil
}
