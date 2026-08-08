package stack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// OpenSeam opens the persistence seam against a running plane.
//
// It exists because ordinary USE of the plane — importing, reading back,
// provisioning a tenant — needs the same three things every lifecycle
// operation needs (the root key, the DSN derived from it, and the object
// bucket) and none of them may be assembled outside this package. The
// create-versus-load decision belongs to rootKeyFor alone, which a
// structure test enforces, so a command that built its own KeyFile would be
// making that decision a second time.
//
// It takes the lifecycle lock SHARED, and holds it until the returned store
// is closed.
//
// Not exclusive, because ordinary use must overlap with itself: two imports
// of different suites have no reason to queue, and the Orchestrator will
// hold a seam open continuously. Not unlocked either, which is what an
// earlier version did: it checked the markers and then operated with nothing
// held, so a reset or a restore could take the exclusive lock immediately
// afterwards and begin deleting the data root under a live import. A
// preflight guard reports what was true a moment ago; it cannot keep it
// true.
//
// Shared-versus-exclusive is exactly the distinction between USING the plane
// and MOVING it between states, and flock already expresses it. The guards
// then run UNDER the lock, so what they observe cannot change while the seam
// is open: a lifecycle operation is neither in flight nor able to start.
//
// The consequence is deliberate and worth stating: `down`, `reset` and
// `restore` now WAIT for an open seam rather than proceeding beside it. An
// operator running `down` during an import waits for the import; that is the
// outcome ADR 0027 asks for, since the alternative is destructive recovery
// removing another actor's in-progress work.
//
// The lock is not re-entrant, so this must not be called by a lifecycle
// operation that already holds the exclusive lock in this process: flock is
// per open file description, and the shared request would block against the
// caller's own exclusive one forever.
//
// The registry is the CALLER's: what types are readable is a property of
// the caller's job, not of the plane, and an empty one here would refuse
// every payload the caller came to write.
func OpenSeam(ctx context.Context, c *Config, types *registry.Registry) (_ store.Store, err error) {
	if types == nil {
		return nil, fmt.Errorf("open the persistence seam: no artifact registry was supplied")
	}
	if mkErr := os.MkdirAll(c.Roots.Data, 0o700); mkErr != nil {
		return nil, fmt.Errorf("create data root %s: %w", c.Roots.Data, mkErr)
	}
	release, err := paths.AcquireSharedLock(filepath.Join(c.Roots.Data, LifecycleLockFile))
	if err != nil {
		return nil, fmt.Errorf("acquire the data-plane lifecycle lock for shared use: %w", err)
	}
	// Released on every path that does not hand the lock to the caller. A
	// seam that failed to open must not leave a holder behind: the next
	// lifecycle operation would block forever on a lock nobody is using.
	defer func() {
		if err != nil {
			err = errors.Join(err, release())
		}
	}()

	// UNDER the lock, which is the whole point: the marker state a guard
	// reads cannot change while this is held.
	if guardErr := guardRestoreState(c, lifecycleUse); guardErr != nil {
		err = guardErr
		return nil, err
	}
	rootKey, err := rootKeyFor(c, lifecycleUse)
	if err != nil {
		return nil, err
	}
	dsn, err := c.DSN(rootKey)
	if err != nil {
		return nil, err
	}
	blob, err := ensureBucket(ctx, c, rootKey)
	if err != nil {
		return nil, err
	}
	// The key this function already resolved, wrapped — never a second
	// KeyFile, which would remake the create-versus-load decision outside
	// rootKeyFor.
	seam, err := postgres.Open(ctx, dsn, types, blob, secret.ResolvedKey(rootKey))
	if err != nil {
		return nil, fmt.Errorf("open the persistence seam: %w", err)
	}
	return &sharedSeam{Store: seam, release: release}, nil
}

// sharedSeam is a store that holds the shared lifecycle lock for as long as
// it is open.
//
// The lock's lifetime is the SEAM's, not the call's, because the race is not
// in opening the plane — it is in using it. A lock released once the store
// was built would leave the import it was taken for entirely unprotected.
type sharedSeam struct {
	store.Store
	release func() error
}

// Close releases the plane and then the lock, in that order: the lock is
// what promises no lifecycle operation ran while this store was usable, so
// it is released once the store no longer is.
//
// Store.Close returns nothing, so a failure to release can only be logged.
// That is the honest limit of this seam's signature, and it is logged at
// error rather than swallowed: a lock this process never released blocks
// every later lifecycle operation until it exits.
func (s *sharedSeam) Close() {
	s.Store.Close()
	if err := s.release(); err != nil {
		slog.Default().Error("could not release the data-plane lifecycle lock; "+
			"lifecycle operations will block until this process exits", "error", err)
	}
}
