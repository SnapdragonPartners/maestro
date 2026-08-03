package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// DefaultComposeFile is the Compose file's path relative to the repository
// root, which is where make targets run.
const DefaultComposeFile = "deploy/dataplane/compose.yaml"

// readyTimeout bounds the wait for both services. The first start runs
// initdb, which dominates it; steady-state startup is a few seconds.
const readyTimeout = 3 * time.Minute

// probeTimeout bounds a single readiness probe, so one hung call cannot
// consume the whole budget.
const probeTimeout = 20 * time.Second

// ErrNotReady reports a stack that did not become healthy in time.
var ErrNotReady = errors.New("data plane did not become ready")

// LifecycleLockFile serializes up/down/reset against one data plane. It
// lives at the data root — the resource being protected — and is never
// unlinked (ADR 0027: unlinking a held lock file lets a second caller lock
// a fresh inode at the same path, producing two "exclusive" holders).
const LifecycleLockFile = ".maestro-dataplane.lock"

// lockLifecycle serializes a whole lifecycle operation across processes.
//
// Without it, a `reset` can delete service data while an `up` in another
// terminal is midway through initdb — the destructive-recovery hazard ADR
// 0027 names, where recovery runs concurrently with a live writer. The
// lock is held across the ENTIRE operation, not just its first step,
// because the window that matters spans the whole of initdb.
func lockLifecycle(c *Config) (func() error, error) {
	if err := os.MkdirAll(c.Roots.Data, 0o700); err != nil {
		return nil, fmt.Errorf("create data root %s: %w", c.Roots.Data, err)
	}
	release, err := paths.AcquireLock(filepath.Join(c.Roots.Data, LifecycleLockFile))
	if err != nil {
		return nil, fmt.Errorf("acquire data-plane lifecycle lock: %w", err)
	}
	return release, nil
}

// Up brings the stack up and waits until both services are usable.
//
// It is idempotent: the "one command from a clean checkout" criterion and
// the everyday inner-loop command are the same command, so re-running it
// on a healthy stack must be a no-op rather than a restart.
func Up(ctx context.Context, c *Config, composeFile string) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()
	if err := guardRestoreState(c, lifecycleUp); err != nil {
		return err
	}
	return up(ctx, c, composeFile)
}

func up(ctx context.Context, c *Config, composeFile string) (err error) {
	if rootsErr := c.Roots.Ensure(); rootsErr != nil {
		return fmt.Errorf("prepare storage roots: %w", rootsErr)
	}
	if dirsErr := c.Roots.EnsureServiceDataDirs(paths.Services()...); dirsErr != nil {
		return fmt.Errorf("prepare service data directories: %w", dirsErr)
	}

	rootKey, keyErr := rootKeyFor(c, lifecycleUp)
	if keyErr != nil {
		return keyErr
	}
	if bootErr := paths.WriteBootstrap(c.Roots.Config, c.Bootstrap()); bootErr != nil {
		return fmt.Errorf("write bootstrap pointer: %w", bootErr)
	}

	env, envErr := c.composeEnv(rootKey)
	if envErr != nil {
		return envErr
	}

	// Read ONCE, before anything starts, and act on it through a single
	// deferred stop armed before Compose is invoked.
	//
	// D4a's invariant is that a plane carrying verification debt is never
	// left running, and the obvious implementation — stop it when
	// settlement fails — does not deliver that. Everything between `compose
	// up` and settlement can fail with the containers already started:
	// readiness times out, the bucket cannot be provisioned, a migration is
	// dirty, a deletion claim will not reconcile. Each of those returned
	// directly, leaving a live, unverified plane that never reached the
	// step that would have condemned it — and the marker only gates
	// lifecycle verbs, not a client holding a connection string.
	//
	// This is the defect replaceDataRoot already had and already fixed, one
	// level down, and it is worth naming as a repeat: arming recovery next
	// to the failure you were thinking about covers that failure and no
	// other. The arming point belongs at the START of the region the
	// invariant covers, which here is every statement after Compose.
	owed, owedErr := restoreOwesVerification(c)
	if owedErr != nil {
		return owedErr
	}
	// A plane owing nothing is not this defer's business: an ordinary `up`
	// that fails readiness deliberately leaves its containers for the
	// operator to inspect.
	settled := !owed
	defer func() {
		if settled {
			return
		}
		// A fresh bounded context, for D4's reason: a Ctrl-C'd `up` must
		// still stop the plane it could not vouch for, and a stop
		// inheriting the cancelled context would be cancelled before it
		// ran.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readyTimeout)
		defer cancel()
		if downErr := down(stopCtx, c, composeFile); downErr != nil {
			err = errors.Join(err, fmt.Errorf("stop a plane that still owes verification: %w", downErr))
		}
	}()

	if composeErr := compose(ctx, c.ProjectName, composeFile, env, "up", "-d", "--wait=false"); composeErr != nil {
		return composeErr
	}
	if readyErr := waitReady(ctx, c, composeFile, env); readyErr != nil {
		return readyErr
	}
	blob, bucketErr := ensureBucket(ctx, c, rootKey)
	if bucketErr != nil {
		return bucketErr
	}
	if migrateErr := migrateLocked(ctx, c, rootKey); migrateErr != nil {
		return migrateErr
	}
	// AFTER the migrations, because the claims table is part of the schema
	// they apply, and before `up` reports a ready plane, because a surviving
	// claim is unfinished destructive work.
	if claimErr := reconcileClaims(ctx, c, rootKey, blob); claimErr != nil {
		return claimErr
	}
	// Last, because it needs an open plane: a restore that could not verify
	// itself — the two-part path, where the key was absent — recorded the
	// debt, and this is the first moment it can be paid.
	//
	// A failure here must STOP the plane, not merely report. Detecting a
	// torn pair and leaving it serving is the worst of both outcomes: the
	// operator sees an error while clients keep using the plane it
	// condemns. The marker is deliberately retained, so the debt survives
	// for the next attempt and every guarded verb keeps refusing.
	//
	// The stop is the deferred one armed before Compose rather than a second
	// one written here. Two mechanisms for one invariant is how the gap
	// above came to exist: the one beside the failure somebody pictured got
	// written, and the region it did not cover got nothing.
	if settleErr := settleOutstandingVerification(ctx, c); settleErr != nil {
		return settleErr
	}
	// The ONLY disarming point, and it is after a healthy settlement rather
	// than after the pass merely running: `settleOutstandingVerification`
	// clears the marker itself when the plane checks out, so reaching here
	// means the debt is gone.
	settled = true
	return nil
}

// ErrPlaneLocked reports a data root that already holds a plane whose
// root-of-trust key is absent.
//
// It is the observable restore state item 8 builds on: refuse, supply the
// original key, open. A bare "file not found" would make that sequence
// untestable as a sequence, because nothing would distinguish it from a
// first run.
var ErrPlaneLocked = errors.New("data plane is locked: its root-of-trust key is not present")

// ErrNoPlane reports an operation that needs a provisioned plane against a
// data root where none exists.
//
// Distinct from ErrPlaneLocked because the two states need opposite actions.
// A locked plane has data and is missing its key: the answer is to restore
// the key, or run new-key recovery. An empty root has neither, and telling
// its operator to "restore the original key" sends them looking for
// something that was never created — the answer is to run `dataplane-up`.
var ErrNoPlane = errors.New("no data plane has been provisioned")

// lifecycle names the operation asking for key material.
//
// It is part of the decision rather than derived from the filesystem, because
// emptiness alone answers the wrong question. An empty data root means "no
// plane has been provisioned"; it does not mean "this operation is the one
// that provisions it". `migrate` against an empty root is not setup — it is
// a migration of a plane that does not exist yet, and minting a key for it
// leaves a key file that the eventual `up` will silently adopt.
type lifecycle int

const (
	// lifecycleUp is the only operation that may create key material, and
	// only then against an empty data root.
	lifecycleUp lifecycle = iota
	lifecycleMigrate
	lifecycleForceVersion
	lifecycleDown
	lifecycleBackup
	lifecycleRestore
	lifecycleVerify
	lifecycleReset
	lifecycleRecoverKey
)

// lifecycles is every operation, in one place, so the marker matrix below
// can be checked for completeness rather than trusted.
//
//nolint:gochecknoglobals // Immutable enumeration of a closed constant set.
var lifecycles = []lifecycle{
	lifecycleUp, lifecycleMigrate, lifecycleForceVersion,
	lifecycleDown, lifecycleBackup, lifecycleRestore, lifecycleVerify, lifecycleReset,
	lifecycleRecoverKey,
}

func (l lifecycle) String() string {
	switch l {
	case lifecycleUp:
		return "up"
	case lifecycleMigrate:
		return "migrate"
	case lifecycleForceVersion:
		return "force-version"
	case lifecycleDown:
		return "down"
	case lifecycleBackup:
		return "backup"
	case lifecycleRestore:
		return "restore"
	case lifecycleVerify:
		return "verify"
	case lifecycleReset:
		return "reset"
	case lifecycleRecoverKey:
		return "recover-key"
	default:
		return "unknown"
	}
}

// RestoreIncompleteMarker names a restore that began deleting and did not
// finish. It lives at the data root, beside the resource it describes.
const RestoreIncompleteMarker = ".maestro-restore-incomplete"

// ErrRestoreIncomplete reports a data root holding a torn restore.
var ErrRestoreIncomplete = errors.New("data plane holds an incomplete restore")

// RestoreUnverifiedMarker records a restore whose tree is whole but whose
// contents have never been checked.
//
// It exists for ADR 0022's two-part restore. That branch completes the copy
// and then CANNOT verify, because verification needs an open plane and the
// plane cannot be opened without its key. Clearing the incomplete marker
// there and stopping — which is what an earlier version did — let a torn
// pair go live: the operator supplies the key, `up` starts the plane, and
// nothing ever recomputes a digest.
//
// So the state is handed forward rather than dropped. It is deliberately a
// SEPARATE marker from RestoreIncompleteMarker, because the two states need
// opposite treatment: a torn tree must not be started at all, while an
// unverified one must be started, since starting it is how it gets
// verified.
const RestoreUnverifiedMarker = ".maestro-restore-unverified"

// ErrRestoreUnverifiedPending reports a plane that started but failed the
// verification it owed from an earlier restore.
var ErrRestoreUnverifiedPending = errors.New("restored plane has not passed verification")

// unverifiedMarkerPath is where the pending-verification marker lives.
func unverifiedMarkerPath(c *Config) string {
	return filepath.Join(c.Roots.Data, RestoreUnverifiedMarker)
}

// markRestoreUnverified records that a completed restore still owes a
// verification pass.
func markRestoreUnverified(c *Config) error {
	if err := os.WriteFile(unverifiedMarkerPath(c), []byte(
		"a restore completed here but could not be verified; the next `up` owes it a verification pass\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", RestoreUnverifiedMarker, err)
	}
	return syncDir(c.Roots.Data)
}

// restoreOwesVerification reports whether a pending pass is recorded.
func restoreOwesVerification(c *Config) (bool, error) {
	if _, err := os.Stat(unverifiedMarkerPath(c)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("check for %s: %w", RestoreUnverifiedMarker, err)
	}
	return true, nil
}

// settleOutstandingVerification runs the pass a previous restore could not,
// and clears the debt only when the plane checks out.
//
// Called at the end of `up`, which is the first moment the plane is open —
// and is exactly the moment the two-part restore hands control back.
func settleOutstandingVerification(ctx context.Context, c *Config) error {
	owed, err := restoreOwesVerification(c)
	if err != nil || !owed {
		return err
	}

	report, verifyErr := verifyLocked(ctx, c)
	if verifyErr != nil {
		return fmt.Errorf("verify a restored plane that had not been checked: %w", verifyErr)
	}
	if !report.Healthy() {
		return fmt.Errorf("%w: %d problem(s) found on a plane restored earlier without verification. "+
			"First problem: %s", ErrRestoreUnverifiedPending, len(report.Problems), report.Problems[0].Detail)
	}
	if err := os.Remove(unverifiedMarkerPath(c)); err != nil {
		return fmt.Errorf("clear %s: %w", RestoreUnverifiedMarker, err)
	}
	return syncDir(c.Roots.Data)
}

// markerPermits records, for EVERY lifecycle operation, whether it may run
// against a data root holding a torn restore.
//
// A torn tree looks exactly like a plane — service directories in place,
// files inside them — so nothing about it is self-announcing. Guarding only
// `up` would leave every other verb able to act on it, and the harmful ones
// are not hypothetical: `backup` is how a torn plane becomes an archive
// somebody later restores from, and `migrate` would apply schema changes to
// half a database.
//
// The two permitted verbs are the two ways out. `restore` resumes, which is
// the intended repair; `reset` discards, and clears the marker as part of
// returning the root to freshness. `down` is permitted because stopping
// something already stopped cannot make a torn tree worse.
//
// Completeness is enforced by a test over `lifecycles` rather than by
// review, so an operation added later cannot default into permitted by
// being forgotten here.
//
//nolint:gochecknoglobals // Immutable policy table.
var markerPermits = map[lifecycle]bool{
	lifecycleUp:           false,
	lifecycleMigrate:      false,
	lifecycleForceVersion: false,
	lifecycleBackup:       false,
	lifecycleVerify:       false,
	lifecycleRecoverKey:   false,
	lifecycleDown:         true,
	lifecycleRestore:      true,
	lifecycleReset:        true,
}

// markerPath is where the restore-incomplete marker lives.
func markerPath(c *Config) string {
	return filepath.Join(c.Roots.Data, RestoreIncompleteMarker)
}

// guardRestoreMarker refuses an operation that must not touch a torn tree.
func guardRestoreMarker(c *Config, operation lifecycle) error {
	permitted, known := markerPermits[operation]
	if !known {
		// Unreachable while the completeness test passes. Refusing rather
		// than permitting is the safe reading of an operation whose policy
		// nobody wrote down.
		return fmt.Errorf("%w: no marker policy is defined for %s", ErrRestoreIncomplete, operation)
	}
	if permitted {
		return nil
	}
	if _, err := os.Stat(markerPath(c)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("check for %s: %w", RestoreIncompleteMarker, err)
	}
	return fmt.Errorf("%w, so %s must not run against it (%s). Re-run `dataplane-restore` from a good "+
		"archive to finish it, or `dataplane-reset` to discard the plane",
		ErrRestoreIncomplete, operation, markerPath(c))
}

// unverifiedPermits is the same policy question for the OTHER marker: may
// this operation run against a plane whose tree is whole and whose contents
// nothing has ever checked?
//
// The two tables answer differently for exactly ONE operation, `up`, and
// that difference is the whole reason the states are recorded separately. A
// torn tree must not be started at all; an unverified one must be started,
// because starting it is the only way it gets verified. `up` is the
// settlement, and refusing it would strand exactly the plane the debt exists
// to rescue.
//
// `verify` is refused, which is not the obvious answer and is the right one.
// Settlement is not a verification pass — it is a pass PLUS its consequences:
// clear the marker when the plane is healthy, and stop the plane when it is
// not. The exported Verify does neither, and it cannot sensibly do the
// second: it takes no Compose file, and a read-shaped verb that stops a
// running plane as a side effect is a trap. Permitting it would leave a verb
// that reports "healthy" against an owing plane and settles nothing, so the
// debt would survive a green report — the one outcome most likely to
// convince an operator it is gone. There is exactly one settlement path, and
// it is `up`.
//
// Nothing is lost by this. An owing plane is a STOPPED plane: `up` either
// settles the debt or stops what it started, so `verify` against one could
// only have failed to connect anyway. The refusal replaces a confusing
// connection error with a message naming the way out.
//
// The other three refusals are the torn table's, for its reasons. `backup`
// is how an unchecked plane becomes an archive somebody later restores from
// — and a two-part restore leaves the plane stopped and owing, which is a
// state `backup` is otherwise perfectly happy to copy. `migrate` and
// `force-version` would apply schema changes to contents nothing has vouched
// for.
//
// Neither `reset` nor `restore` clears this marker specially: both sweep the
// data root, and a plane that has been discarded or replaced owes nothing
// about contents that are gone. Only a HEALTHY settlement clears it in place.
//
//nolint:gochecknoglobals // Immutable policy table.
var unverifiedPermits = map[lifecycle]bool{
	lifecycleUp:           true,
	lifecycleDown:         true,
	lifecycleRestore:      true,
	lifecycleReset:        true,
	lifecycleVerify:       false,
	lifecycleMigrate:      false,
	lifecycleForceVersion: false,
	lifecycleBackup:       false,
	lifecycleRecoverKey:   false,
}

// guardUnverifiedMarker refuses an operation that must not act on a plane
// owing a verification pass.
func guardUnverifiedMarker(c *Config, operation lifecycle) error {
	permitted, known := unverifiedPermits[operation]
	if !known {
		// Unreachable while the completeness test passes, and refusing is the
		// safe reading of an operation whose policy nobody wrote down.
		return fmt.Errorf("%w: no pending-verification policy is defined for %s",
			ErrRestoreUnverifiedPending, operation)
	}
	if permitted {
		return nil
	}
	owed, err := restoreOwesVerification(c)
	if err != nil || !owed {
		return err
	}
	return fmt.Errorf("%w, so %s must not run against it (%s). Run `dataplane-up`, which verifies the "+
		"plane and clears this, or `dataplane-reset` to discard it",
		ErrRestoreUnverifiedPending, operation, unverifiedMarkerPath(c))
}

// guardRestoreState applies BOTH marker policies, and is what every
// lifecycle verb calls.
//
// One entry point rather than two calls per verb: the two markers describe
// different states with different tables, and a caller that consulted one
// and forgot the other would be guarded against the failure it remembered.
// A verb cannot opt into half of this by omission.
func guardRestoreState(c *Config, operation lifecycle) error {
	if err := guardRestoreMarker(c, operation); err != nil {
		return err
	}
	return guardUnverifiedMarker(c, operation)
}

// writeRestoreMarker durably records that a restore is about to delete.
//
// Both the file and its parent directory are fsynced BEFORE the first
// deletion. A marker that is still in the page cache when the machine loses
// power describes a restore that did happen, which is the one crash where
// it matters most.
func writeRestoreMarker(c *Config) (err error) {
	file, err := os.OpenFile(markerPath(c), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", RestoreIncompleteMarker, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", RestoreIncompleteMarker, closeErr)
		}
		// A marker that was created and then failed to be written, synced,
		// or published describes nothing, and leaving it would forbid every
		// lifecycle verb — including the recovery that has deleted nothing
		// and should simply restart the plane.
		//
		// This removal is an ERGONOMIC improvement, not the safety
		// mechanism. Safety comes from replaceTree deriving the destructive
		// phase from what is actually on disk: with the file left behind,
		// the phase reads destructive, the plane stays stopped, and the
		// operator gets a marker they can act on. With it removed, they get
		// a running plane and an error. Both are safe; one is kinder.
		//
		// UNCOVERED, stated rather than implied: no test forces a marker
		// write to fail AFTER the file is created — doing so needs an
		// injected failure in the write, fsync or directory-sync step, and
		// deleting this line leaves every test green. What IS tested is the
		// derivation that makes either outcome safe.
		if err != nil {
			_ = os.Remove(markerPath(c))
		}
	}()

	if _, err := file.WriteString("a restore began deleting into this data root and did not finish\n"); err != nil {
		return fmt.Errorf("write %s: %w", RestoreIncompleteMarker, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", RestoreIncompleteMarker, err)
	}
	return syncDir(c.Roots.Data)
}

// clearRestoreMarker removes the marker once the restore has completed.
func clearRestoreMarker(c *Config) error {
	if err := os.Remove(markerPath(c)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", RestoreIncompleteMarker, err)
	}
	return syncDir(c.Roots.Data)
}

// syncDir flushes a directory entry so a create or rename inside it
// survives a power loss.
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	// Sync below is what makes the entry durable; closing a read-only
	// directory handle afterwards has nothing left to report.
	defer func() { _ = handle.Close() }()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}

// rootKeyFor is the ONE place that decides whether a lifecycle operation may
// create key material (item 7, D4).
//
// The root key derives the Postgres password and the object-store
// credentials as well as the vault's key material, so minting a new one over
// an existing data root produces a password the cluster does not know. The
// authenticated healthcheck then fails, waitReady times out, and `up`
// reports "data plane did not become ready" three minutes later — a correct
// refusal reached by accident, naming nothing an operator can act on.
//
// TWO conditions, and both are necessary. The operation must be `up`, which
// is the only one that provisions a plane; and the data root must be empty,
// so `up` on an existing plane loads rather than replaces. Emptiness alone
// would let `migrate` or `force-version` mint against a fresh root, which the
// accepted design forbids for a reason worth stating: neither creates a
// plane, so a key either of them generated would belong to nothing until an
// `up` adopted it silently.
//
// Emptiness is judged across every service data directory, not just Postgres:
// the object store's credentials derive from the same key, so a plane holding
// objects and no cluster is still a plane some earlier key provisioned.
func rootKeyFor(c *Config, operation lifecycle) ([]byte, error) {
	evidence, err := planeEvidence(c)
	if err != nil {
		return nil, err
	}
	fresh := len(evidence) == 0

	// A non-provisioning operation against an empty root refuses HERE, before
	// the key is even read, because the presence of a key does not mean a
	// plane exists. An `up` that died after minting the key and before initdb
	// leaves exactly that state: a key file beside an empty data root. Reading
	// it and proceeding would let `migrate` run against a plane that was never
	// created, and the error it eventually produced would be about the schema
	// rather than about the missing plane.
	//
	// Only `up` may adopt such a key, which is the same rule as creating one:
	// provisioning is its job alone.
	if operation != lifecycleUp && fresh {
		return nil, fmt.Errorf("%w in %s, so %s has nothing to act on. Run `dataplane-up` first, "+
			"which is the only operation that provisions one", ErrNoPlane, c.Roots.Data, operation)
	}

	access := secret.LoadOnly
	if operation == lifecycleUp && fresh {
		access = secret.MayCreate
	}

	key, keyErr := secret.KeyFile(c.Roots.Config, access).RootKey()
	if keyErr == nil {
		return key, nil
	}
	wrapped := fmt.Errorf("read root-of-trust key for %s: %w", operation, keyErr)
	if !errors.Is(wrapped, paths.ErrNoKey) {
		return nil, wrapped
	}

	// Only a populated root reaches here: an empty one refused above, and an
	// empty one under `up` was allowed to create rather than to fail.
	//
	// The evidence is NAMED rather than merely asserted. Freshness reads the
	// whole data root, so an incidental file — a macOS .DS_Store from opening
	// the directory in Finder is the realistic one — makes a genuinely fresh
	// plane look provisioned. Refusing is still right, since minting over a
	// real plane costs every secret in it, but an operator who can see WHAT
	// was found can tell the two cases apart in seconds. Naming the evidence
	// is the alternative to an exclusion list for known junk, which would be
	// a place for a future writer's data to be silently ignored.
	return nil, fmt.Errorf("%w (%s). Its Postgres password and object-store credentials are "+
		"derived from the original key, so a new one would open neither. Restore the key file "+
		"beside the backup, or run the new-key recovery path. The data root is judged non-fresh "+
		"because of: %s: %w",
		ErrPlaneLocked, operation, strings.Join(evidence, ", "), wrapped)
}

// maxEvidencePaths bounds how many offending paths an error names. A
// provisioned cluster holds thousands; a handful identifies the state.
const maxEvidencePaths = 5

// planeEvidence walks the data root and returns the paths proving a plane
// already exists there. An empty result means the root is fresh.
//
// The rule is ANY NON-DIRECTORY ENTRY except the lifecycle lock. Not "any
// entry", and not "any regular file":
//
//   - Not any entry, because `up` creates the service directories before it
//     asks whether the root is fresh, so on a first run this walk already
//     sees empty postgres/ and minio/. Counting them would refuse to mint a
//     key on a clean checkout and fail `dataplane-up` from empty.
//   - Not any regular file, because a FIFO, socket, device node, or anything
//     else unrecognised would then read as "fresh" — and freshness is the
//     judgement that authorises minting a key over whatever is there. The
//     safe reading of an entry we do not understand is that it is somebody's
//     data.
//
// A traversal that cannot be read is an error rather than a "fresh" answer,
// for the same reason: an unreadable root is the case where nothing is
// known, and nothing-known is not emptiness.
//
// This replaces a per-service check. Enumerating services could only ever be
// as complete as the list, and the failure it produced would be silent — a
// plane holding an unlisted service's data judged fresh, and its root key
// replaced. Reading the root cannot be wrong about a writer nobody
// registered.
func planeEvidence(c *Config) ([]string, error) {
	var evidence []string
	err := filepath.WalkDir(c.Roots.Data, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			if errors.Is(err, os.ErrNotExist) && path == c.Roots.Data {
				return filepath.SkipAll // A root that does not exist yet is the first-run case.
			}
			return fmt.Errorf("inspect %s: %w", path, err)
		case entry.IsDir():
			return nil
		case path == filepath.Join(c.Roots.Data, LifecycleLockFile):
			// Never evidence: `up` itself creates it before judging freshness,
			// and it is deliberately never unlinked (ADR 0027).
			return nil
		}
		if len(evidence) < maxEvidencePaths {
			evidence = append(evidence, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inspect data root %s: %w", c.Roots.Data, err)
	}
	return evidence, nil
}

// dataRootIsEmpty reports whether NO plane has been provisioned yet.
func dataRootIsEmpty(c *Config) (bool, error) {
	evidence, err := planeEvidence(c)
	if err != nil {
		return false, err
	}
	return len(evidence) == 0, nil
}

// ensureBucket provisions the object store the way migrateLocked
// provisions the schema: a service answering its health probe is not the
// same as a service ready to be used.
//
// Nothing created this bucket before. The config named it and the bootstrap
// pointer published it, so `up` reported a ready plane whose first write
// would have failed on a bucket that did not exist. Design D3.
//
// The endpoint comes from the bootstrap pointer rather than being formatted
// again here, so what `up` provisions is by construction the endpoint every
// caller is told to use.
//
// It returns the adapter it built. Claim reconciliation needs one over the
// same bucket, and building a second from the same inputs would be two places
// that have to agree about which bucket the plane uses.
func ensureBucket(ctx context.Context, c *Config, rootKey []byte) (*objects.Blob, error) {
	accessKey, err := secret.Derive(rootKey, secret.ContextObjectAccessKey)
	if err != nil {
		return nil, fmt.Errorf("derive object access key: %w", err)
	}
	secretKey, err := secret.Derive(rootKey, secret.ContextObjectSecretKey)
	if err != nil {
		return nil, fmt.Errorf("derive object secret key: %w", err)
	}

	blob, err := objects.New(objects.Config{
		Endpoint:  c.Bootstrap().Objects.Endpoint,
		Bucket:    c.Bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
	})
	if err != nil {
		return nil, fmt.Errorf("build object store client: %w", err)
	}
	if err := blob.EnsureBucket(ctx); err != nil {
		return nil, fmt.Errorf("provision object storage: %w", err)
	}
	return blob, nil
}

// reconcileClaims finishes object deletes an earlier actor could not.
//
// It belongs to `up` for the same reason migration does: both make the plane
// CONSISTENT rather than merely running, and a caller must not be handed a
// plane that is migrated but still carrying unfinished destructive work. A
// surviving deletion claim is exactly that -- storage condemned and possibly
// still there, on a digest whose writers cannot take the existing-object
// shortcut until the claim clears.
//
// Safe to run every time, including on a plane with nothing to recover:
// re-issuing a version-specific delete is a no-op by construction, and the
// claims table is empty whenever nothing is mid-delete.
//
// The registry is empty on purpose. Reconciliation reads no payload and
// validates no artifact type; a registry populated for this caller's benefit
// would be a second, drifting copy of the one the Orchestrator will own.
func reconcileClaims(ctx context.Context, c *Config, rootKey []byte, blob *objects.Blob) error {
	dsn, err := c.DSN(rootKey)
	if err != nil {
		return err
	}
	types, err := registry.New(nil)
	if err != nil {
		return fmt.Errorf("build an empty artifact registry: %w", err)
	}
	// The key this function was HANDED, wrapped — not a second KeyFile.
	// Constructing one here would make the create-versus-load decision a
	// second time, outside rootKeyFor, which is the one place allowed to
	// make it; a structure test enforces that and caught this exact
	// mistake. The caller already resolved the key under the right rule.
	seam, err := postgres.Open(ctx, dsn, types, blob, secret.ResolvedKey(rootKey))
	if err != nil {
		return fmt.Errorf("open the persistence seam: %w", err)
	}
	defer seam.Close()

	recovered, err := seam.ReconcileDeletionClaims(ctx)
	if err != nil {
		return fmt.Errorf("reconcile deletion claims: %w", err)
	}
	// Silent when there was nothing to do, which is every ordinary `up`. A
	// claim exists only because something went wrong earlier, so having
	// finished one is worth an operator's attention rather than a line in the
	// noise.
	if recovered != (store.ClaimReconciliation{}) {
		slog.Default().InfoContext(ctx, "finished object deletions left behind by an earlier run",
			"claims_cleared", recovered.ClaimsCleared,
			"versions_deleted", recovered.VersionsDeleted,
			"uploads_aborted", recovered.UploadsAborted)
	}
	return nil
}

// Migrate applies pending migrations to an already-running stack.
//
// It takes the lifecycle lock like every other operation: it mutates the
// same data plane, and a migration running against a plane that `reset` is
// concurrently emptying is exactly the interleaving the lock exists to
// prevent.
func Migrate(ctx context.Context, c *Config) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	if err := guardRestoreState(c, lifecycleMigrate); err != nil {
		return err
	}
	rootKey, keyErr := rootKeyFor(c, lifecycleMigrate)
	if keyErr != nil {
		return keyErr
	}
	return migrateLocked(ctx, c, rootKey)
}

// migrateLocked applies migrations, assuming the caller holds the
// lifecycle lock.
func migrateLocked(ctx context.Context, c *Config, rootKey []byte) error {
	dsn, err := c.DSN(rootKey)
	if err != nil {
		return err
	}
	if err := migrations.Up(ctx, dsn); err != nil {
		return fmt.Errorf("migrate data plane schema: %w", err)
	}
	return nil
}

// ForceVersion repairs a dirty schema version WITHOUT running migrations.
//
// A failed migration leaves the recorded version marked dirty --
// golang-migrate marks BEFORE executing -- and every later migration
// refuses until that is cleared. Fixing whatever caused the failure is not
// enough on its own; the metadata still claims a migration is half-applied.
//
// This exists because a migration's own recovery instructions must name an
// operation an operator can actually perform. It is deliberately narrow: it
// changes metadata only, and the caller is asserting the schema really is
// at the version being forced. A wrong assertion leaves the schema and its
// recorded version disagreeing, which no later migration can detect.
//
// Serialized on the lifecycle lock like every other stack operation, so it
// cannot race a concurrent migrate.
//
// Takes no context, unlike its neighbours: golang-migrate's Force is not
// context-aware, and accepting one this operation cannot honour would
// promise cancellation that never happens. It is a single metadata write.
func ForceVersion(c *Config, version int) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	if err := guardRestoreState(c, lifecycleForceVersion); err != nil {
		return err
	}
	rootKey, keyErr := rootKeyFor(c, lifecycleForceVersion)
	if keyErr != nil {
		return keyErr
	}
	dsn, dsnErr := c.DSN(rootKey)
	if dsnErr != nil {
		return dsnErr
	}

	// The clean-database refusal lives in migrations.Force, so a direct
	// caller cannot skip it. This wrapper adds the lifecycle lock, which is
	// what makes the read-then-act inside it safe against a concurrent
	// migrate.
	if err := migrations.Force(dsn, version); err != nil {
		return fmt.Errorf("force data plane schema version: %w", err)
	}
	return nil
}

// Down stops the stack and leaves the data root untouched.
func Down(ctx context.Context, c *Config, composeFile string) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()
	return down(ctx, c, composeFile)
}

func down(ctx context.Context, c *Config, composeFile string) error {
	env, err := c.composeEnv(placeholderKey())
	if err != nil {
		return err
	}
	return compose(ctx, c.ProjectName, composeFile, env, "down")
}

// Reset stops the stack and deletes the contents of every service data
// directory. This is the only destructive operation here, and it is the
// caller's job to have confirmed it.
//
// The directories themselves are preserved rather than removed and
// recreated: they are bind-mount sources, and on macOS a recreated
// directory has a new inode, which leaves any existing mount pointing at
// the old one (the same hazard CLAUDE.md records for workspaces).
func Reset(ctx context.Context, c *Config, composeFile string) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	// down, not Down: the lock is already held and flock is not
	// re-entrant, so calling the exported form would deadlock against
	// this very goroutine.
	if err := down(ctx, c, composeFile); err != nil {
		return err
	}
	return clearDataRoot(c)
}

// clearDataRoot returns the data root to exactly the state planeEvidence
// calls fresh.
//
// It sweeps the WHOLE root rather than the registry's service directories,
// and that is a consequence of the freshness rule rather than thoroughness
// for its own sake. Freshness reads every entry under the root, so a reset
// that cleared only registered services would leave anything else in place
// — an unregistered service's directory, a stray file, a restore-incomplete
// marker — and the next `up` would then refuse to provision the plane the
// operator just asked to be wiped. Reset and freshness are two halves of
// one definition and have to agree by construction.
//
// Top-level DIRECTORIES are emptied in place, never removed: they are
// bind-mount sources, and on macOS a recreated directory has a new inode
// while any existing mount keeps pointing at the old one. Everything else
// is removed, except the lifecycle lock, which this operation is currently
// holding and which is deliberately never unlinked (ADR 0027 — unlinking a
// held lock file lets a second caller lock a fresh inode at the same path,
// producing two "exclusive" holders).
func clearDataRoot(c *Config) error {
	return clearDataRootKeeping(c, LifecycleLockFile)
}

// clearDataRootKeeping is clearDataRoot with additional top-level entries
// left alone.
//
// Restore needs it: the restore-incomplete marker is written BEFORE the
// first deletion and must survive the deletion it describes, or a crash
// mid-clear would leave a torn tree with nothing saying so.
func clearDataRootKeeping(c *Config, keep ...string) error {
	preserved := make(map[string]bool, len(keep))
	for _, name := range keep {
		preserved[name] = true
	}

	entries, err := os.ReadDir(c.Roots.Data)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read data root %s: %w", c.Roots.Data, err)
	}

	for _, entry := range entries {
		target := filepath.Join(c.Roots.Data, entry.Name())
		switch {
		case preserved[entry.Name()]:
			continue
		case entry.IsDir():
			if err := clearDirectoryContents(target); err != nil {
				return err
			}
		default:
			if err := os.Remove(target); err != nil {
				return fmt.Errorf("remove %s: %w", target, err)
			}
		}
	}
	return nil
}

// clearDirectoryContents empties a directory while preserving its inode.
func clearDirectoryContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", dir, err)
	}
	for _, entry := range entries {
		target := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("remove %s: %w", target, err)
		}
	}
	return nil
}

// ImagePinsFile is the name of the digest pin file, which lives beside the
// Compose file so the two travel together.
const ImagePinsFile = "images.env"

// loadImagePins reads the digest pins beside the Compose file.
//
// They are loaded here rather than passed to Compose as an --env-file so
// that a missing or malformed pin is our error with our message. Compose's
// own failure mode is to substitute a blank string and then complain that
// a service "has neither an image nor a build context", which names
// neither the file nor the variable.
func loadImagePins(composeFile string) ([]string, error) {
	path := filepath.Join(filepath.Dir(composeFile), ImagePinsFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image pins %s: %w", path, err)
	}

	lines := strings.Split(string(raw), "\n")
	pins := make([]string, 0, len(lines))
	required := map[string]bool{"MAESTRO_PG_IMAGE": false, "MAESTRO_MINIO_IMAGE": false}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			return nil, fmt.Errorf("%s: malformed line %q", path, trimmed)
		}
		if !strings.Contains(value, "@sha256:") {
			return nil, fmt.Errorf("%s: %s is not pinned by digest (ADR 0026)", path, key)
		}
		if _, known := required[key]; known {
			required[key] = true
		}
		pins = append(pins, trimmed)
	}
	for key, present := range required {
		if !present {
			return nil, fmt.Errorf("%s does not define %s", path, key)
		}
	}
	return pins, nil
}

// pinnedImage resolves one image reference from the same pins file Compose
// is given.
//
// Recovery needs it because its isolated server runs OUTSIDE the Compose
// project -- deliberately, so ordinary lifecycle commands never touch it --
// and must still run the exact digest-pinned image the plane runs (ADR
// 0026). Reading the same file is what keeps the two from drifting.
func pinnedImage(composeFile, key string) (string, error) {
	pins, err := loadImagePins(composeFile)
	if err != nil {
		return "", err
	}
	for _, pin := range pins {
		name, value, _ := strings.Cut(pin, "=")
		if name == key {
			return value, nil
		}
	}
	return "", fmt.Errorf("no %s in the image pins beside %s", key, composeFile)
}

// composeOutput runs a docker compose subcommand and returns its combined
// output. Combined, because Compose reports the real cause (a port clash,
// an unwritable mount) on stderr, and losing it turns a diagnosable
// failure into "exit status 1".
func composeOutput(ctx context.Context, project, composeFile string, env []string, args ...string) ([]byte, error) {
	pins, err := loadImagePins(composeFile)
	if err != nil {
		return nil, err
	}
	full := append([]string{"compose", "--project-name", project, "--file", composeFile}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Env = append(append(os.Environ(), env...), pins...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("docker compose %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

// compose runs a docker compose subcommand against the data-plane project.
func compose(ctx context.Context, project, composeFile string, env []string, args ...string) error {
	_, err := composeOutput(ctx, project, composeFile, env, args...)
	return err
}

// waitReady blocks until Postgres reports healthy and MinIO answers its
// liveness endpoint, or the deadline passes.
//
// readyTimeout is enforced by a context, not by a loop condition. A loop
// that only checks elapsed time between iterations cannot bound an
// iteration that never returns — a wedged `docker compose ps` or an
// accepted-but-silent HTTP connection would both hang indefinitely on the
// caller's context while the deadline passed unnoticed. Every probe gets
// its own bounded context derived from that one.
func waitReady(ctx context.Context, c *Config, composeFile string, env []string) error {
	return waitReadyFor(ctx, c, composeFile, env, allServiceNames())
}

// allServiceNames is every service the registry knows, as Compose names
// them. Derived from paths.Services() rather than written out, so a service
// added there is waited for here without a second edit.
func allServiceNames() []string {
	services := paths.Services()
	names := make([]string, 0, len(services))
	for _, service := range services {
		names = append(names, string(service))
	}
	return names
}

// waitReadyFor blocks until every NAMED service is usable.
//
// A subset rather than always both, because `backup` restarts exactly what
// it stopped. A backup of a project with one service deliberately down must
// not wait for that service — it would time out against a service nobody
// asked to be running, turning a correct partial-project backup into a
// three-minute failure. Waiting for the originally-running subset is the
// only rule that serves both cases.
//
// An empty set returns immediately: a plane that was fully stopped is
// restored by starting nothing, and there is nothing to become ready.
func waitReadyFor(ctx context.Context, c *Config, composeFile string, env, services []string) error {
	if len(services) == 0 {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	wantPostgres := slices.Contains(services, string(paths.ServicePostgres))
	wantMinIO := slices.Contains(services, string(paths.ServiceMinIO))

	var lastErr error
	for {
		var pgErr, minioErr error
		if wantPostgres {
			pgErr = postgresHealthy(waitCtx, c.ProjectName, composeFile, env)
		}
		if wantMinIO {
			minioErr = minioLive(waitCtx, c)
		}
		if pgErr == nil && minioErr == nil {
			return nil
		}
		lastErr = errors.Join(pgErr, minioErr)

		select {
		case <-waitCtx.Done():
			// Compose logs are the difference between "did not become
			// ready" and a diagnosis: initdb failures, permission errors on
			// the bind mount, and image problems all appear there.
			return fmt.Errorf("%w within %s: %w\n%s",
				ErrNotReady, readyTimeout, lastErr, recentLogs(ctx, c.ProjectName, composeFile, env))
		case <-time.After(time.Second):
		}
	}
}

// recentLogs returns the tail of the stack's logs for a failure message,
// on a fresh short-lived context so it still works when the caller's has
// already expired — which, at the point this is called, it has.
func recentLogs(ctx context.Context, project, composeFile string, env []string) string {
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	out, err := composeOutput(logCtx, project, composeFile, env, "logs", "--tail", "40")
	if err != nil {
		return fmt.Sprintf("(could not collect compose logs: %v)", err)
	}
	return string(out)
}

// composePS is the subset of `docker compose ps --format json` this needs.
type composePS struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// postgresHealthy reads the container's own healthcheck verdict.
//
// The verdict comes from the container's healthcheck, which runs an
// AUTHENTICATED query (compose.yaml, pinned by a test) — deliberately not
// pg_isready, which succeeds with the wrong user, database or password and
// would report a plane ready that nobody can open.
//
// The check lives in the container because that is where a client that
// speaks the protocol ships. A host-side TCP dial would report success as
// soon as the port is bound, which during a cold initdb is long before the
// database can answer.
func postgresHealthy(ctx context.Context, project, composeFile string, env []string) error {
	// Per-probe bound: one wedged docker invocation must not consume the
	// whole readiness budget.
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	out, err := composeOutput(probeCtx, project, composeFile, env, "ps", "--format", "json")
	if err != nil {
		return fmt.Errorf("docker compose ps: %w", err)
	}

	// Compose emits one JSON object per line, not an array.
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var entry composePS
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return fmt.Errorf("parse compose ps output: %w", err)
		}
		if entry.Service != "postgres" {
			continue
		}
		if entry.Health == "healthy" {
			return nil
		}
		return fmt.Errorf("postgres is %s (health: %q)", entry.State, entry.Health)
	}
	return errors.New("postgres container not found")
}

// minioLive probes the published port from the host.
//
// Host-side rather than a container healthcheck: this image's minimal base
// has changed its available tooling across releases, so a healthcheck
// shelling out to curl or mc is one pin-bump from silently breaking. This
// also tests the path callers actually use — the published port.
func minioLive(ctx context.Context, c *Config) error {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	url := fmt.Sprintf("http://127.0.0.1:%d/minio/health/live", c.MinIOPort)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("build minio health request: %w", err)
	}
	// Not http.DefaultClient: it has no timeout, so a connection that is
	// accepted and then never answered would hang on the context alone.
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("minio not answering: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("minio health returned %s", resp.Status)
	}
	return nil
}

// placeholderKey supplies non-empty key material for operations that must
// render the Compose environment but never touch credentials.
//
// Down and Reset must work even when the key file is missing or
// unreadable — that is exactly when an operator needs to tear the stack
// down. Compose still requires every variable to be set, so the values are
// filled with a constant that is never used to authenticate: the
// containers are being stopped, not started.
func placeholderKey() []byte {
	return []byte("teardown-placeholder-not-a-credential")
}
