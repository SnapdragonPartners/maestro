// Package plane composes a persistence seam from parts that do not know where
// the plane lives.
//
// It is the provider-neutral half of what `stack.OpenSeam` used to do alone
// (#286). That function assembles six LOCAL things — a data root, an flock
// lifecycle lock, restore-marker guards, a key file, a DSN derived from it,
// and a MinIO bucket — and then composes them. Only the composing is portable,
// and everything above it describes one deployment: `stack.Config` is a Docker
// Compose topology, ports and container labels included.
//
// So the split is by WHO KNOWS WHERE THE PLANE IS, not by layer. `stack` stays
// explicitly local and keeps all six assumptions; this package takes what it
// resolved and opens a seam from it. A cloud composition resolves the same
// inputs from entirely different places and calls the same function.
//
// What this package is NOT is a second application-facing abstraction.
// `store.Store` remains the one the Orchestrator sees, and the cloud
// portability claim lives there: application persistence must see the same
// behaviour in both modes.
package plane

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/nilcheck"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/readiness"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// Composition is everything a seam needs, with no statement about where any
// of it came from.
//
//nolint:govet // fieldalignment: grouped by what it supplies, built once per open
type Composition struct {
	// DSN reaches the database. How it was derived — from a local key file,
	// from an instance connection name, from an injected secret — is the
	// composer's business and not this package's.
	DSN string

	// Objects is the object provider. It is the neutral seam extracted in
	// step 1, so this field is what makes a second provider expressible at
	// all.
	Objects objects.Store

	// RootKey supplies the vault's root of trust.
	//
	// It stays a LOCAL seam even here, and that is deliberate. Cloud mode
	// proper does not hand Maestro a root key from a provider secret manager
	// — it replaces the secrets module entirely, because the provider stores
	// and returns the secrets themselves. An implementation of this interface
	// for cloud would have to invent a key it has no use for. A composition
	// that supplies operator-provided material is therefore running the LOCAL
	// vault keyed from outside, which is a legitimate composition and is not
	// evidence that the secrets module is portable.
	RootKey secret.RootKeyProvider

	// Types is the CALLER's registry: what payloads are readable is a
	// property of the caller's job rather than of the plane.
	Types *registry.Registry

	// Keys is the caller's configuration-key registry, with Types's
	// semantics: what keys are writable is a property of the caller's job.
	//
	// It was absent until Phase 3 item 3 (design D7), and its absence was
	// not a default but a hole: postgres.New falls back to an empty,
	// fail-closed registry, so no caller reaching the plane through a
	// composer could write a governed configuration record at all. It is
	// required rather than defaulted for the same reason Types is — a caller
	// that writes no configuration says so with configkeys.MustNew(nil), and
	// is refused if it then tries, which is the existing behaviour reached
	// deliberately instead of by omission.
	Keys *configkeys.Registry

	// Owned are resources whose lifetime is the SEAM's, released when it
	// closes and — the part that is easy to get wrong — also released if this
	// function fails partway.
	Owned []Owned
}

// Owned is a resource the composition takes responsibility for closing.
//
// It carries a name because the failure it exists to report is a leak, and a
// leak reported as "close: %w" names nothing an operator can act on. Locally
// the resource is the lifecycle lock, whose release is what lets `down` and
// `reset` proceed; in cloud mode it is the object client, which holds pooled
// connections. Neither is reachable from `store.Store`, so nothing else can
// close them.
//
// Field order is chosen for alignment rather than for reading: the func value
// leads, which keeps the struct's pointer prefix minimal.
type Owned struct {
	// Close releases the resource. Called at most once.
	Close func() error

	// What names the resource in diagnostics, e.g. "data-plane lifecycle lock".
	What string
}

// Open composes a seam and returns it.
//
// The returned store owns everything in Owned: closing it releases them in
// reverse order, after the store itself is closed. Reverse because ownership
// nests — the lock a caller took before opening the plane is the one that must
// outlive it — and closing in acquisition order would release the outer
// resource while the inner one still depends on it.
//
// EVERY FAILURE PATH RELEASES THEM TOO. That is the half a signature does not
// force and the reason this function exists rather than callers invoking
// postgres.Open directly: an open that fails after the caller acquired a lock
// or built an object client leaves a holder behind, and the next lifecycle
// operation blocks forever on a lock nobody is using. `stack.OpenSeam` already
// had that defer for its flock; a cloud composition has the identical
// obligation for its object client, and discovering that separately in each
// composer is how one of them ends up without it.
//
//nolint:gocritic // hugeParam: by value, matching the ownership model below.
func Open(ctx context.Context, c Composition) (_ store.Store, err error) {
	// THE OWNERSHIP-TRANSFER BOUNDARY, and it has to copy rather than borrow.
	//
	// Passing Composition by value is not enough: Owned is a slice, so its
	// backing array stays shared with the caller after this returns. A caller
	// that later reused or amended that slice would be reaching into what this
	// seam now owns — replacing a closer so the wrong resource is released, or
	// clearing one so a live resource is leaked while the seam believes it
	// released everything.
	//
	// Cloning is also what makes the by-value signature mean what it says.
	// Without it the struct is copied and the thing that matters is not.
	c.Owned = slices.Clone(c.Owned)

	// Released on every path that does not hand ownership to the caller.
	defer func() {
		if err != nil {
			err = errors.Join(err, release(c.Owned))
		}
	}()

	if validateErr := c.validate(); validateErr != nil {
		err = validateErr
		return nil, err
	}

	// The pool is built HERE rather than by postgres.Open, because the probe
	// below must run on it before a store exists, and the pool is lazy:
	// pgxpool.New validates the DSN and contacts nothing.
	pool, err := pgxpool.New(ctx, c.DSN)
	if err != nil {
		return nil, fmt.Errorf("open the persistence seam: open data plane pool: %w", err)
	}
	if probeErr := probe(ctx, pool); probeErr != nil {
		// The pool is closed on the path the probe was added to diagnose;
		// a probe that leaked a connection here would be worse than none.
		pool.Close()
		err = probeErr
		return nil, err
	}
	seam, err := postgres.New(pool, c.Types, c.Objects, c.RootKey, postgres.WithConfigKeys(c.Keys))
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("open the persistence seam: %w", err)
	}
	return &composedSeam{Store: seam, owned: c.Owned}, nil
}

// Neutral remedies. This package does not know how the plane is deployed, so
// it names the ACTION; a composer that knows the command replaces the remedy
// with readiness.WithRemedy.
const (
	remedyStartDatabase = "start the database the plane was configured with and check the endpoint"
	remedyInspectSchema = "inspect the plane: a connection was made but its schema version could not be read"
	remedyMigrate       = "apply this binary's pending migrations to the plane"
	remedyRepairDirty   = "repair the failed migration by hand, then force the version clean (docs/v2/phase_2/design_schema_core.md); nothing automates this"
	remedyNewerBinary   = "run a binary built at or after the plane's schema version; never downgrade the plane"
)

// probe proves the plane is usable from ONE connection, before a seam is
// handed out (Phase 3 item 3, design D4).
//
// Acquiring the connection is what establishes reachability, since the pool
// is lazy; the schema version is then read ON that connection. Two facts,
// one connection, so they cannot disagree. Classification follows from which
// step failed.
//
// This is the same reason the cloud composer probes its bucket rather than
// trusting a constructed handle: it is what makes "the seam opened" mean the
// same thing in both modes.
func probe(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return readiness.Refuse(readiness.Unreachable,
			"the database did not accept a connection", remedyStartDatabase, err)
	}
	defer conn.Release()

	version, dirty, err := migrations.VersionOn(ctx, conn.Conn())
	if err != nil {
		return readiness.Refuse(readiness.SchemaUnreadable,
			"the schema version could not be read on an open connection", remedyInspectSchema, err)
	}
	embedded, err := migrations.Embedded()
	if err != nil {
		// A binary that cannot find its own migrations: not a plane state,
		// and not a readiness cause.
		return fmt.Errorf("open the persistence seam: %w", err)
	}
	switch {
	case dirty:
		// Before the version comparison: a dirty version is the target of
		// the migration that failed, and its remedy is repair, not migrate.
		return readiness.Refuse(readiness.SchemaDirty,
			fmt.Sprintf("schema version %d is marked dirty: a migration to it failed partway", version),
			remedyRepairDirty, nil)
	case version < embedded:
		return readiness.Refuse(readiness.SchemaBehind,
			fmt.Sprintf("the plane is at schema version %d and this binary needs %d", version, embedded),
			remedyMigrate, nil)
	case version > embedded:
		return readiness.Refuse(readiness.SchemaAhead,
			fmt.Sprintf("the plane is at schema version %d and this binary knows only %d", version, embedded),
			remedyNewerBinary, nil)
	}
	return nil
}

// validate refuses a composition that cannot open, before anything is built.
//
// The nil checks use nilcheck rather than `== nil` because three of these are
// INTERFACES, and an interface holding a typed nil pointer is not equal to
// nil. That is not a hypothetical: the same guard one layer down admitted a
// typed-nil object store earlier in this work and deferred the failure to a
// panic on the first read.
//
//nolint:gocritic // hugeParam: by value, matching Open.
func (c Composition) validate() error {
	switch {
	case c.DSN == "":
		return errors.New("compose a data plane: no database DSN was supplied")
	case nilcheck.IsNil(c.Objects):
		return errors.New("compose a data plane: no object store was supplied")
	case nilcheck.IsNil(c.RootKey):
		return errors.New("compose a data plane: no root-key provider was supplied")
	case c.Types == nil:
		return errors.New("compose a data plane: no artifact registry was supplied")
	case c.Keys == nil:
		return errors.New("compose a data plane: no configuration-key registry was supplied; a caller " +
			"that writes no configuration declares that with an empty one")
	}
	for i, owned := range c.Owned {
		if owned.Close == nil {
			return fmt.Errorf("compose a data plane: owned resource %d (%q) has no close function, "+
				"so it would be silently leaked rather than released", i, owned.What)
		}
		if owned.What == "" {
			return fmt.Errorf("compose a data plane: owned resource %d is unnamed, so a failure to "+
				"release it could not say what leaked", i)
		}
	}
	return nil
}

// release closes owned resources in reverse order, collecting every failure
// rather than stopping at the first.
//
// Stopping would leave the remaining resources held because an earlier one
// misbehaved, which is the opposite of what a release path is for.
//
// It CANNOT ASSUME VALIDATED INPUT, which is not defensive habit but the
// arithmetic of when it runs: the failure path it exists for includes
// validation itself, so it is handed the very composition that was just
// rejected. A nil Close panicked here for exactly that reason — validate
// refuses one, and then the deferred cleanup for that refusal dereferenced it,
// turning a clear error into a crash inside the recovery path. A nil entry is
// therefore skipped and reported rather than called.
func release(owned []Owned) error {
	var errs []error
	for i := len(owned) - 1; i >= 0; i-- {
		if owned[i].Close == nil {
			errs = append(errs, fmt.Errorf("cannot release %s: it has no close function, so "+
				"whatever it names is leaked", named(owned[i].What)))
			continue
		}
		if closeErr := owned[i].Close(); closeErr != nil {
			errs = append(errs, fmt.Errorf("release %s: %w", named(owned[i].What), closeErr))
		}
	}
	if joined := errors.Join(errs...); joined != nil {
		return fmt.Errorf("release resources owned by the data-plane seam: %w", joined)
	}
	return nil
}

// named gives an unnamed resource a stand-in, so a release failure reads as a
// failure rather than as an empty string.
func named(what string) string {
	if what == "" {
		return "an unnamed resource"
	}
	return what
}

// composedSeam is a store that holds the composition's owned resources for as
// long as it is open.
type composedSeam struct {
	store.Store
	owned []Owned
	// once makes closing idempotent, which Owned.Close promises and nothing
	// previously delivered: a second Close released everything a second time.
	// That is not harmless. A repeated flock release reports an error from a
	// descriptor already closed, and a resource whose close is not idempotent
	// can do worse — the point of the contract is that a resource need not
	// defend itself against being closed twice.
	//
	// Double-close is not an exotic mistake either. It is what a deferred
	// Close plus an explicit one on an error path produces, in the caller,
	// which is the ordinary shape of this code.
	once sync.Once
}

// Close closes the store and then releases what the composition owns.
//
// In that order, because the resources are what promise the plane stayed
// usable while the store was: a lock released before the store is closed would
// let a destructive lifecycle operation start against a live seam, which is
// the race the lock exists to prevent.
//
// store.Store.Close returns nothing, so a release failure can only be logged.
// That is the honest limit of the interface's signature, and it is logged
// rather than dropped: a lock this process never released blocks every later
// lifecycle operation until it exits.
// The WHOLE sequence is guarded, not just the release: closing the underlying
// store twice is the other half of the same mistake, and splitting the guard
// would leave one of them unprotected.
func (s *composedSeam) Close() {
	s.once.Do(func() {
		s.Store.Close()
		if err := release(s.owned); err != nil {
			logRelease(err)
		}
	})
}
