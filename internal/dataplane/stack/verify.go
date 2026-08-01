package stack

import (
	"context"
	"fmt"

	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// Verify recomputes every stored digest against a running plane.
//
// It takes the lifecycle lock like every other verb, but for a narrower
// reason: it must not run against a torn restore. It does NOT stop the
// plane — verification against a live plane is the useful case, and the
// concurrency that implies is handled at the seam, under the snapshot and
// advisory-lock protocol rather than by excluding writers here.
func Verify(ctx context.Context, c *Config) (_ store.VerifyReport, err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return store.VerifyReport{}, lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	if guardErr := guardRestoreMarker(c, lifecycleVerify); guardErr != nil {
		return store.VerifyReport{}, guardErr
	}
	return verifyLocked(ctx, c)
}

// verifyLocked is Verify assuming the caller already holds the lifecycle
// lock, and having already decided the marker policy.
//
// Restore needs it: flock is not re-entrant, so calling the exported form
// from inside a restore would deadlock against the caller — and restore
// must verify while its own marker is still present, which the exported
// form would refuse.
func verifyLocked(ctx context.Context, c *Config) (store.VerifyReport, error) {
	rootKey, keyErr := rootKeyFor(c, lifecycleVerify)
	if keyErr != nil {
		return store.VerifyReport{}, keyErr
	}
	dsn, dsnErr := c.DSN(rootKey)
	if dsnErr != nil {
		return store.VerifyReport{}, dsnErr
	}
	blob, blobErr := ensureBucket(ctx, c, rootKey)
	if blobErr != nil {
		return store.VerifyReport{}, blobErr
	}
	// Empty for the same reason claim reconciliation's is: verification
	// recomputes digests over stored bytes and validates no artifact type,
	// so a registry populated here would be a second, drifting copy of the
	// one the Orchestrator will own.
	types, typesErr := registry.New(nil)
	if typesErr != nil {
		return store.VerifyReport{}, fmt.Errorf("build an empty artifact registry: %w", typesErr)
	}
	// The key this function already resolved, wrapped — never a second
	// KeyFile, which would remake the create-versus-load decision outside
	// rootKeyFor.
	seam, openErr := postgres.Open(ctx, dsn, types, blob, secret.ResolvedKey(rootKey))
	if openErr != nil {
		return store.VerifyReport{}, fmt.Errorf("open the persistence seam: %w", openErr)
	}
	defer seam.Close()

	report, verifyErr := seam.Verify(ctx)
	if verifyErr != nil {
		return store.VerifyReport{}, fmt.Errorf("verify the data plane: %w", verifyErr)
	}
	return report, nil
}
