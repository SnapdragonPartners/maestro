package stack

import (
	"context"
	"fmt"

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
// It does NOT take the lifecycle lock, and that is deliberate. The lock
// serializes operations that move the plane between states; this one uses
// the plane while it is up, which is what the Orchestrator will do
// continuously. Holding the lock for the duration of an import would make
// every ordinary write a lifecycle event and block `down` behind it.
//
// The guards, on the other hand, do apply. A plane holding a torn restore,
// an unsettled verification debt or an interrupted recovery is one whose
// contents are not established, and writing into it would add records to a
// store nobody has proven is intact.
//
// The registry is the CALLER's: what types are readable is a property of
// the caller's job, not of the plane, and an empty one here would refuse
// every payload the caller came to write.
func OpenSeam(ctx context.Context, c *Config, types *registry.Registry) (store.Store, error) {
	if types == nil {
		return nil, fmt.Errorf("open the persistence seam: no artifact registry was supplied")
	}
	if err := guardRestoreState(c, lifecycleUse); err != nil {
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
	return seam, nil
}
