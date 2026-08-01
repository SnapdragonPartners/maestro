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

// ErrRestoreUnverified reports a restored plane whose digests do not check
// out. The incomplete marker is deliberately left in place for it.
var ErrRestoreUnverified = errors.New("restored plane failed verification")

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

	// Pre-destructive recovery, armed before the stop and disarmed the
	// moment the first deletion becomes possible.
	//
	// Everything up to the marker leaves the ORIGINAL plane intact, so a
	// failure there must put it back rather than leave it down — a partial
	// `down`, or a marker that could not be written, would otherwise strand
	// a perfectly good plane stopped. After the marker, the opposite rule
	// applies and the plane must STAY stopped, so this defer disarms itself
	// at exactly that point.
	destructive := false
	defer func() {
		if destructive || err == nil {
			return
		}
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restartTimeout)
		defer cancel()
		if upErr := up(restartCtx, c, composeFile); upErr != nil {
			err = errors.Join(err, fmt.Errorf("restart the untouched plane after a failed restore: %w", upErr))
		}
	}()

	// down, not stop: the restored cluster's password derives from the key
	// that produced the ARCHIVE, so the containers have to be recreated
	// with credentials rendered from it. Stopped containers would keep the
	// environment they were built with and fail to authenticate against
	// what was just restored.
	if downErr := down(ctx, c, composeFile); downErr != nil {
		return downErr
	}

	return replaceDataRoot(ctx, c, composeFile, archiveData, &destructive)
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
func replaceDataRoot(ctx context.Context, c *Config, composeFile, archiveData string, destructive *bool) error {
	*destructive = true
	if err := replaceTree(c, archiveData); err != nil {
		return err
	}

	if err := up(ctx, c, composeFile); err != nil {
		if errors.Is(err, ErrPlaneLocked) {
			// The documented two-part restore, and the ONE case where the
			// marker is cleared without a healthy verification: the tree is
			// whole and the plane simply cannot be opened without its key.
			// Leaving the marker would make every later verb refuse a plane
			// that is merely locked, including the `up` that finishes the
			// sequence once the key is in place.
			if clearErr := clearRestoreMarker(c); clearErr != nil {
				return fmt.Errorf("restore completed but the marker could not be cleared: %w",
					errors.Join(err, clearErr))
			}
			return fmt.Errorf("restore completed, but the plane cannot be opened: %w", err)
		}
		return err
	}

	// Verification is part of the restore, not a separate courtesy. A
	// restore that copied a torn Postgres/object-store pair starts cleanly
	// and reports success, and the corruption surfaces later as a digest
	// mismatch nobody connects to this operation. The marker therefore
	// survives until the report is healthy: an unverified plane is still a
	// plane nobody should build on.
	report, err := verifyLocked(ctx, c)
	if err != nil {
		return fmt.Errorf("verify the restored plane: %w", err)
	}
	if !report.Healthy() {
		return fmt.Errorf("%w: the restored plane failed verification with %d problem(s); "+
			"the incomplete marker is left in place. First problem: %s",
			ErrRestoreUnverified, len(report.Problems), report.Problems[0].Detail)
	}

	return clearRestoreMarker(c)
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

	// The marker is NOT cleared here. The tree being whole is not the same
	// as the plane being sound: only verification, after `up`, can say the
	// copied cluster and object store still agree. The caller clears it.
	return nil
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
		// A DIRECTORY, not merely something at that path: a file named
		// `postgres` would satisfy a bare existence check and then be
		// restored over a bind-mount source, which cannot work.
		serviceInfo, statErr := os.Stat(path)
		if statErr != nil || !serviceInfo.IsDir() {
			return fmt.Errorf("%w: %s has no %s directory", ErrArchiveMissingService, archiveData, service)
		}
	}

	// The inventory is RECOMPUTED and compared exactly, not merely checked
	// for the presence of names. Names alone would accept a tree truncated
	// after its manifest was written, an entry replaced by a shorter one, or
	// an extra entry nobody recorded — and the completion protocol cannot
	// catch any of those, because the manifest was written honestly at the
	// time.
	actual, err := inventory(archiveData, "")
	if err != nil {
		return fmt.Errorf("%w: %s could not be inventoried: %w", ErrArchiveIncomplete, archiveData, err)
	}

	recorded := make(map[string]ManifestEntry, len(manifest.Entries))
	for i := range manifest.Entries {
		recorded[manifest.Entries[i].Name] = manifest.Entries[i]
	}
	for i := range actual.Entries {
		found := actual.Entries[i]
		want, listed := recorded[found.Name]
		switch {
		case !listed:
			return fmt.Errorf("%w: %s holds %q, which the manifest does not list",
				ErrArchiveIncomplete, archiveData, found.Name)
		case want.Files != found.Files || want.Bytes != found.Bytes:
			return fmt.Errorf("%w: %q holds %d files / %d bytes, the manifest records %d / %d",
				ErrArchiveIncomplete, found.Name, found.Files, found.Bytes, want.Files, want.Bytes)
		}
		delete(recorded, found.Name)
	}
	for name := range recorded {
		return fmt.Errorf("%w: the manifest lists %q but the tree does not contain it",
			ErrArchiveIncomplete, name)
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
		// syncContents, not noSync. The marker's removal is a metadata write
		// that can reach the disk while the copied files have not, so a power
		// loss just after a "successful" restore could leave a torn tree with
		// nothing recording that it is torn — the exact state the marker
		// exists to prevent.
		if err := copyTree(source, target, syncContents); err != nil {
			return err
		}
	}
	return nil
}
