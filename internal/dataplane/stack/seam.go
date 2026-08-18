package stack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
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
	keyProvider, err := resolvedRootKey(rootKey)
	if err != nil {
		return nil, err
	}

	// Everything above this line is local: a data root, an flock, marker
	// guards, a key file, a DSN derived from it, and a MinIO bucket. Composing
	// them is not, so it happens in `plane`, which knows none of it.
	//
	// The lock is handed over as an OWNED resource rather than released here,
	// because its lifetime is the seam's and not this call's: the race is not
	// in opening the plane but in using it, and a lock released once the store
	// was built would leave the import it was taken for entirely unprotected.
	//
	// Ownership transfers BEFORE the call rather than after it succeeds.
	// `plane.Open` releases what it owns on its own failure paths, so leaving
	// this function's deferred release armed would release the same lock
	// twice — and it would do so on exactly the paths that are already
	// failing, where the second release reports an error from a descriptor
	// that is already closed. The deferred release still covers every path
	// that fails before this point.
	lock := release
	release = func() error { return nil }

	seam, err := plane.Open(ctx, plane.Composition{
		DSN:     dsn,
		Objects: blob,
		RootKey: keyProvider,
		Types:   types,
		Owned: []plane.Owned{
			{What: "data-plane lifecycle lock", Close: lock},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open the persistence seam for %s: %w", c.Roots.Data, err)
	}
	return seam, nil
}
