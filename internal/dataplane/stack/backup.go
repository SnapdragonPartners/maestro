package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ArchiveDataDir is the copied data root inside an archive, and
// ArchiveManifest is what makes the directory an archive at all.
const (
	ArchiveDataDir  = "data"
	ArchiveManifest = "manifest.json"
)

// ArchiveFormat is the manifest's schema version. It exists so a future
// change to the archive layout is a refusal rather than a misread.
const ArchiveFormat = 1

// stopTimeout bounds `compose stop`.
//
// Generous, and explicitly passed rather than left to Compose's ten-second
// default. The pinned Postgres image carries STOPSIGNAL SIGINT, which is a
// fast shutdown: transactions abort, the cluster checkpoints and exits. A
// large cluster's checkpoint can still take a while, and a stop that times
// out is escalated to SIGKILL — capturing a crashed cluster in the backup,
// which is exactly what a cold backup exists to avoid.
const stopTimeout = 2 * time.Minute

// restartTimeout bounds the restart that must happen whatever else did.
const restartTimeout = 2 * time.Minute

// ErrArchiveIncomplete reports an archive with no valid manifest — a
// killed backup's residue, or a directory that was never an archive.
var ErrArchiveIncomplete = errors.New("not a complete backup archive")

// ErrDestinationExists reports a backup destination that is already there.
var ErrDestinationExists = errors.New("backup destination already exists")

// Manifest describes a completed archive.
//
// Its presence is the completion protocol: it is written and fsynced LAST,
// so a directory carrying one is by construction a finished copy. That is
// what makes archive validity a property of contents rather than of a path
// — a killed backup cannot run its own cleanup, so "the temporary path" is
// not a safety boundary.
//
// It is deliberately NOT an integrity protocol. It proves the copy
// finished, not that the bytes are good; proving that is verify's job,
// after the plane is up and the seam's own digests can be recomputed.
// Conflating the two would let a cheap structural check stand in for the
// expensive semantic one.
type Manifest struct {
	CreatedAt  time.Time       `json:"created_at"`
	SourceRoot string          `json:"source_root"`
	Entries    []ManifestEntry `json:"entries"`
	Format     int             `json:"format"`
}

// ManifestEntry inventories one top-level entry of the copied data root.
type ManifestEntry struct {
	Name  string `json:"name"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// projectState records which containers existed and which were running,
// so a backup can put the project back the way it found it.
type projectState struct {
	running []string
}

// Backup quiesces the plane, copies the data root, and restarts whatever
// was running before.
//
// It never reads the root-of-trust key. `compose stop` and `compose start`
// act on containers that already exist, carrying the environment they were
// created with, so no credential is ever rendered — which is why the key's
// exclusion from the archive is structural rather than a rule that could be
// got wrong. (`down`/`up` would remove and recreate the containers, and
// recreating them requires the key.)
func Backup(ctx context.Context, c *Config, composeFile, destination string) (err error) {
	if overlapErr := refuseOverlap(c.Roots.Data, destination); overlapErr != nil {
		return overlapErr
	}
	if _, statErr := os.Lstat(destination); statErr == nil {
		return fmt.Errorf("%w: %s. Choose a path that does not exist, so a partial archive can never "+
			"be mistaken for a complete one", ErrDestinationExists, destination)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("check %s: %w", destination, statErr)
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

	if guardErr := guardRestoreState(c, lifecycleBackup); guardErr != nil {
		return guardErr
	}

	env, envErr := c.composeEnv(placeholderKey())
	if envErr != nil {
		return envErr
	}

	state, stateErr := readProjectState(ctx, c.ProjectName, composeFile, env)
	if stateErr != nil {
		return stateErr
	}

	// Recovery is armed BEFORE the stop, not after it. `compose stop` is not
	// atomic: a cancelled or partly failed stop can leave some containers
	// stopped and still return an error, and a restart registered only on
	// the success path would never run for exactly that case — the operator
	// would be left with a half-stopped plane and a message about a backup.
	//
	// The restart runs on a context that cannot be cancelled by the one that
	// carried us here: Ctrl-C cancels the operation context, and a deferred
	// restart inheriting it would be cancelled before it ran, turning an
	// interrupted backup into a stopped plane.
	defer func() {
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restartTimeout)
		defer cancel()
		if startErr := composeStart(restartCtx, c.ProjectName, composeFile, env, state); startErr != nil {
			err = errors.Join(err, fmt.Errorf("restart the data plane after backup: %w", startErr))
		}
	}()

	if stopErr := composeStop(ctx, c.ProjectName, composeFile, env); stopErr != nil {
		return stopErr
	}
	return publishArchive(c, destination)
}

// publishArchive builds the archive beside its destination and renames it
// into place once the manifest is written.
func publishArchive(c *Config, destination string) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	// A sibling of the destination, so publication is a rename within one
	// filesystem rather than a second copy across two.
	staging, err := os.MkdirTemp(parent, ".maestro-backup-*")
	if err != nil {
		return fmt.Errorf("create staging directory in %s: %w", parent, err)
	}
	// Removed on every failure path. This is best-effort cleanup, NOT the
	// safety mechanism: a killed process runs no defers, which is why the
	// manifest decides validity.
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()

	// MkdirTemp applies umask, so the staging directory is chmodded to the
	// same 0700 the storage roots use — this tree holds a copy of the
	// Postgres cluster and the object store.
	//nolint:gosec // G302 assumes a file; 0700 on a directory is the tight mode, not a loose one.
	if chmodErr := os.Chmod(staging, 0o700); chmodErr != nil {
		return fmt.Errorf("set mode on %s: %w", staging, chmodErr)
	}
	if copyErr := copyTree(c.Roots.Data, filepath.Join(staging, ArchiveDataDir), syncContents); copyErr != nil {
		return copyErr
	}

	manifest, manifestErr := inventory(filepath.Join(staging, ArchiveDataDir), c.Roots.Data)
	if manifestErr != nil {
		return manifestErr
	}
	if writeErr := writeManifest(staging, manifest); writeErr != nil {
		return writeErr
	}

	if renameErr := os.Rename(staging, destination); renameErr != nil {
		return fmt.Errorf("publish archive to %s: %w", destination, renameErr)
	}
	committed = true
	// The rename itself has to be durable, not just the bytes it published.
	return syncDir(parent)
}

// inventory summarises the copied tree for the manifest.
func inventory(copied, sourceRoot string) (Manifest, error) {
	entries, err := os.ReadDir(copied)
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", copied, err)
	}

	manifest := Manifest{
		Format: ArchiveFormat,
		// Recorded from the wall clock deliberately: this is a human-facing
		// record of when the copy was taken, not an ordering key.
		CreatedAt:  time.Now().UTC(),
		SourceRoot: sourceRoot,
	}
	for _, entry := range entries {
		summary := ManifestEntry{Name: entry.Name()}
		if entry.IsDir() {
			if err := filepath.Walk(filepath.Join(copied, entry.Name()), func(_ string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() {
					summary.Files++
					summary.Bytes += info.Size()
				}
				return nil
			}); err != nil {
				return Manifest{}, fmt.Errorf("inventory %s: %w", entry.Name(), err)
			}
		} else {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return Manifest{}, fmt.Errorf("stat %s: %w", entry.Name(), infoErr)
			}
			summary.Files = 1
			summary.Bytes = info.Size()
		}
		manifest.Entries = append(manifest.Entries, summary)
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Name < manifest.Entries[j].Name })
	return manifest, nil
}

// writeManifest writes and fsyncs the manifest, then fsyncs its directory.
// Everything else in the archive is already on disk by this point, so this
// is the step that makes the archive complete.
func writeManifest(archive string, manifest Manifest) (err error) {
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	path := filepath.Join(archive, ArchiveManifest)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()

	if _, err := file.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return syncDir(archive)
}

// ReadManifest loads an archive's manifest, and is the only thing that
// establishes a directory is an archive.
func ReadManifest(archive string) (Manifest, error) {
	body, err := os.ReadFile(filepath.Join(archive, ArchiveManifest))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, fmt.Errorf("%w: %s has no %s. A backup that was killed mid-copy leaves "+
				"exactly this, and restoring from it would replace a plane with a partial one",
				ErrArchiveIncomplete, archive, ArchiveManifest)
		}
		return Manifest{}, fmt.Errorf("read manifest in %s: %w", archive, err)
	}

	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s has an unreadable %s: %w", ErrArchiveIncomplete, archive, ArchiveManifest, err)
	}
	if manifest.Format != ArchiveFormat {
		return Manifest{}, fmt.Errorf("%w: %s is format %d, this build understands %d",
			ErrArchiveIncomplete, archive, manifest.Format, ArchiveFormat)
	}
	return manifest, nil
}

// readProjectState records which of the project's containers are running.
//
// Backup restores this state rather than assuming "running". `compose
// start` only works on containers that still exist, and `dataplane-down` —
// an ordinary supported command — removes them, so assuming a running
// project would make a backup of a stopped plane copy successfully and then
// fail its promised restart. Backing up a stopped plane is the easiest case
// to get right; refusing it would push operators into starting a plane they
// did not want running.
func readProjectState(ctx context.Context, project, composeFile string, env []string) (projectState, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// --all, so a stopped-but-existing container is seen rather than
	// silently treated as absent.
	out, err := composeOutput(probeCtx, project, composeFile, env, "ps", "--all", "--format", "json")
	if err != nil {
		return projectState{}, fmt.Errorf("read project state: %w", err)
	}

	var state projectState
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var entry composePS
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return projectState{}, fmt.Errorf("parse compose ps output: %w", err)
		}
		if entry.State == "running" {
			state.running = append(state.running, entry.Service)
		}
	}
	sort.Strings(state.running)
	return state, nil
}

// composeStop stops every container in the project without removing any.
//
// Project-wide, so completeness does not depend on the service registry: a
// service missing from paths.Services() is still stopped, because `compose
// stop` acts on the project rather than on a list we supply.
func composeStop(ctx context.Context, project, composeFile string, env []string) error {
	stopCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()

	timeout := fmt.Sprintf("%d", int(stopTimeout.Seconds()))
	if err := compose(stopCtx, project, composeFile, env, "stop", "--timeout", timeout); err != nil {
		return fmt.Errorf("stop the data plane for backup: %w", err)
	}
	return nil
}

// composeStart restarts exactly the containers that were running before.
func composeStart(ctx context.Context, project, composeFile string, env []string, state projectState) error {
	if len(state.running) == 0 {
		// The plane was already down. Leaving it down is the correct
		// restoration of state, and is also what keeps a backup of a
		// stopped plane keyless — nothing is created.
		return nil
	}
	args := append([]string{"start"}, state.running...)
	if err := compose(ctx, project, composeFile, env, args...); err != nil {
		return fmt.Errorf("start %s: %w", strings.Join(state.running, ", "), err)
	}
	return nil
}
