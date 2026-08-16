// Package postgres is the local implementation of the data plane's
// persistence seam (design D1).
//
// It composes sqlc's generated queries with the seam logic ADR 0028 places
// here and nowhere else: registry lookup, schema validation, digest
// construction, and the classified, conditionally written transitions of
// design D5.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/nilcheck"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
)

// Store is the Postgres-backed implementation of store.Store.
type Store struct {
	pool     *pgxpool.Pool
	queries  *gen.Queries
	registry *registry.Registry
	// keys is the configuration key registry (item 7, design D1).
	//
	// Injected like the artifact registry, and for the same reason: a test
	// registers the keys it needs without mutating global state another
	// test observes. It defaults to an EMPTY registry rather than to nil,
	// which is not a nil trap of the kind the blob comment below warns
	// about -- an empty registry refuses every configuration write with a
	// typed error that names the registered vocabulary, which is the
	// correct behaviour for a store nobody gave a vocabulary to. Answering
	// a required capability with a shrug is a trap; refusing loudly is not.
	keys *configkeys.Registry
	// rootKey provides the key every secret's per-version key is derived
	// from (item 7, design D3).
	//
	// REQUIRED, unlike the configuration key registry beside it, and the
	// asymmetry is not an oversight. An empty key vocabulary is a real
	// state — this package ships none, so a store with no registered keys
	// is correctly configured and refuses writes by saying so. A missing
	// root key is not a state, it is a broken plane: the store would come
	// up, every other family would work, and the vault would fail later,
	// which is exactly the partial-plane mode design D4 rejects. Refusing
	// at construction turns that into one failure at one place.
	rootKey secret.RootKeyProvider
	// blob is the object module's Layer 1 adapter. It is required, not
	// optional: ADR 0022 makes object storage part of the data plane, and a
	// store that satisfied the seam while answering every object operation
	// with "no backend" would be a nil trap wearing an interface.
	blob objects.Store
	// now reads the wall clock for the ONE decision that needs a clock this
	// side of the database: the sweep's grace period, which judges the age
	// of storage the object store dated.
	//
	// Every other time-based decision here is made with the SERVER's clock,
	// in SQL, and must stay that way -- a lease's expiry is judged by
	// several participants and a client-side answer would put one process's
	// skew into every other's decision. An unreferenced object has no row,
	// so there is no server-side timestamp to compare it against.
	//
	// Injectable because design D6 requires the grace period to be tested by
	// MOVING THE CLOCK rather than by switching the rule off: a test that
	// disables a guard proves the guard is switchable.
	now func() time.Time
}

// Option adjusts a Store at construction.
type Option func(*Store)

// WithClock replaces the wall clock the sweep's grace period reads.
//
// The grace period cannot be configured, only aged past. That asymmetry is
// deliberate: a settable duration invites a zero, and design D6 requires the
// guard to be non-zero and unswitchable.
func WithClock(now func() time.Time) Option {
	return func(s *Store) {
		if now != nil {
			s.now = now
		}
	}
}

// WithConfigKeys replaces the configuration key registry.
//
// Absent it, every configuration write is refused as an unregistered key.
// That is the correct default for a store nobody has given a vocabulary:
// this package ships no seed keys, so an empty registry is the honest
// starting state rather than a missing dependency.
func WithConfigKeys(keys *configkeys.Registry) Option {
	return func(s *Store) {
		if keys != nil {
			s.keys = keys
		}
	}
}

// New builds a Store over an existing pool.
//
// The registry is injected rather than read from a package-level default,
// so a test can register the types it needs without mutating global state
// that another test observes.
func New(
	pool *pgxpool.Pool, types *registry.Registry, blob objects.Store,
	rootKey secret.RootKeyProvider, opts ...Option,
) (*Store, error) {
	if pool == nil {
		return nil, errors.New("postgres store: pool is nil")
	}
	if types == nil {
		return nil, errors.New("postgres store: registry is nil")
	}
	if blob == nil {
		return nil, errors.New("postgres store: object adapter is nil")
	}
	// nilcheck, not `rootKey == nil`: an interface holding a typed nil is
	// not equal to nil, so the plain comparison admits one and the panic
	// arrives on the first vault operation instead of here.
	if nilcheck.IsNil(rootKey) {
		return nil, errors.New("postgres store: root-key provider is nil; the secrets vault cannot " +
			"seal or open without one, and a store that came up without it would fail only once " +
			"somebody reached the vault")
	}
	built := &Store{
		pool:     pool,
		queries:  gen.New(pool),
		registry: types,
		keys:     configkeys.MustNew(nil),
		rootKey:  rootKey,
		blob:     blob,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(built)
	}
	return built, nil
}

// Open builds a Store from a DSN.
func Open(
	ctx context.Context, dsn string, types *registry.Registry, blob objects.Store,
	rootKey secret.RootKeyProvider, opts ...Option,
) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open data plane pool: %w", err)
	}
	built, err := New(pool, types, blob, rootKey, opts...)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return built, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// tx is the transactional view. It holds queries bound to a pgx.Tx, so
// every method reached through it participates in that transaction.
type tx struct {
	queries  *gen.Queries
	registry *registry.Registry
	keys     *configkeys.Registry
	rootKey  secret.RootKeyProvider
	// blob is here for one reason: acceptance must check that referenced
	// objects EXIST, which is the single precondition reaching outside
	// Postgres (design D5). It is safe in that order because the
	// attachment row already exists and the sweep's reachable set is
	// exactly the attachment rows.
	blob objects.Store
}

// WithTx runs fn inside one transaction.
//
// Rollback on a non-nil error is deferred rather than called at each return
// so that a panic in fn cannot leave the transaction open; the deferred
// rollback after a successful commit is a documented no-op in pgx.
func (s *Store) WithTx(ctx context.Context, fn func(store.Tx) error) error {
	pgxTx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = pgxTx.Rollback(ctx) }()

	if err := fn(&tx{queries: s.queries.WithTx(pgxTx), registry: s.registry, keys: s.keys, rootKey: s.rootKey, blob: s.blob}); err != nil {
		return err
	}
	if err := pgxTx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// inTx runs one seam operation in its own transaction and returns its
// result. Every multi-statement operation on Store goes through this, so
// the non-transactional entry points are not a second, weaker code path.
func inTx[T any](ctx context.Context, s *Store, fn func(*tx) (T, error)) (T, error) {
	var result T
	err := s.WithTx(ctx, func(handle store.Tx) error {
		inner, ok := handle.(*tx)
		if !ok {
			return fmt.Errorf("%w: unexpected transaction type %T", store.ErrInvariant, handle)
		}
		var opErr error
		result, opErr = fn(inner)
		return opErr
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

// notFound maps pgx's no-rows sentinel onto the seam's, so callers match on
// store.ErrNotFound rather than importing pgx to interpret an error.
func notFound(err error, what string, id uuid.UUID) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", store.ErrNotFound, what, id)
	}
	return err
}

// rejected builds a refusal carrying the rule that refused it.
func rejected(transition string, artifactID uuid.UUID, reason store.RejectionReason, detail string) error {
	return &store.TransitionRejected{
		Transition: transition,
		ArtifactID: artifactID,
		Reason:     reason,
		Detail:     detail,
	}
}

// invariant reports a conditional write that affected no rows after the
// seam's classification said it should succeed (design D5 step 3).
func invariant(transition string, artifactID uuid.UUID) error {
	return fmt.Errorf("%w: %s affected no rows for artifact %s after its preconditions were verified "+
		"under the row lock; the SQL backstop and the Go classification disagree",
		store.ErrInvariant, transition, artifactID)
}

// scopeArc is the schema's exclusive arc: exactly one member is non-nil.
type scopeArc struct {
	organizationID *uuid.UUID
	productID      *uuid.UUID
	featureID      *uuid.UUID
	epicID         *uuid.UUID
	storyID        *uuid.UUID
	benchmarkRunID *uuid.UUID
}

// scopeColumns spreads a scope across the exclusive arc.
//
// It returns an error for an unknown scope type rather than silently
// producing all-nulls, which the schema would reject with a constraint
// violation naming num_nonnulls instead of the actual mistake.
func scopeColumns(scope store.Scope) (scopeArc, error) {
	id := scope.ID
	switch scope.Type {
	case store.ScopeOrganization:
		return scopeArc{organizationID: &id}, nil
	case store.ScopeProduct:
		return scopeArc{productID: &id}, nil
	case store.ScopeFeature:
		return scopeArc{featureID: &id}, nil
	case store.ScopeEpic:
		return scopeArc{epicID: &id}, nil
	case store.ScopeStory:
		return scopeArc{storyID: &id}, nil
	case store.ScopeBenchmark:
		return scopeArc{benchmarkRunID: &id}, nil
	default:
		return scopeArc{}, fmt.Errorf("unknown scope type %q", scope.Type)
	}
}

// newIdentifier allocates a UUIDv7, or returns a caller's preallocated one.
//
// Named for identifiers generally rather than for artifacts: every primary
// key the seam writes comes from here — artifacts, reviews and principal
// instances alike — and an artifact-specific name invited the assumption
// that principals were allocated somewhere else. They were, and they were
// still v4 when that assumption went unexamined.
//
// v7 rather than v4 because the schema design requires it: v7 is
// time-ordered, so primary keys cluster by creation time instead of
// scattering across the index. uuid.New() is v4 and was wrong here.
//
// Callers may preallocate because item 6's cross-store commit order needs
// identifiers before the transaction that writes them: the object lands
// first, then its attachment row, and then the referencing artifact and its
// retention pins TOGETHER -- and a pin names the attachment it protects, so
// that id has to exist before the transaction begins. The object's own key
// is derived from its digest, not from any id.
func newIdentifier(preallocated uuid.UUID) (uuid.UUID, error) {
	if preallocated != uuid.Nil {
		if preallocated.Version() != 7 {
			return uuid.Nil, fmt.Errorf("preallocated id %s is UUID version %d, want 7",
				preallocated, preallocated.Version())
		}
		return preallocated, nil
	}
	generated, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("allocate UUIDv7: %w", err)
	}
	return generated, nil
}

// requirePrincipalKind checks that a principal is one of the kinds a role
// admits, and that it belongs to this organization.
//
// The foreign key proves only that the principal EXISTS. ADR 0021 requires
// both the author and the reviewer of a Management artifact to be an agent
// or a human, and without this a system principal could author one that an
// agent then accepts — acceptance validates the reviewer's kind but had
// nothing to say about the author's.
func (t *tx) requirePrincipalKind(ctx context.Context, organizationID, instanceID uuid.UUID, role string, allowed ...store.PrincipalKind) error {
	instance, err := t.queries.GetPrincipalInstance(ctx, gen.GetPrincipalInstanceParams{
		PrincipalInstanceID: toUUID(instanceID),
		OrganizationID:      toUUID(organizationID),
	})
	if err != nil {
		return notFound(err, role+" principal", instanceID)
	}
	for _, kind := range allowed {
		if store.PrincipalKind(instance.Kind) == kind {
			return nil
		}
	}
	return fmt.Errorf("%s principal %s is of kind %q, which this artifact family does not admit",
		role, instanceID, instance.Kind)
}

// validatePayload runs the registered validator for one version of one
// type, plus the universal encoding rule.
//
// The order matters: the type must be registered before anything else is
// meaningful, and the schema validator runs against the instance that will
// be STORED — for an amendment that is the merged effective payload, never
// the patch (design D3).
func (t *tx) validatePayload(artifactType registry.Type, version int, payload json.RawMessage) error {
	validator, err := t.registry.ValidatorFor(artifactType, version)
	if err != nil {
		return fmt.Errorf("no validator for the payload being written: %w", err)
	}
	if err := validator.Validate(payload); err != nil {
		return fmt.Errorf("payload does not conform to %s schema version %d: %w", artifactType, version, err)
	}
	return nil
}

// Compile-time proof that both the store and its transactional view satisfy
// the seam. Without these, a missing method surfaces at the first call site
// rather than at build time.
var (
	_ store.Store = (*Store)(nil)
	_ store.Tx    = (*tx)(nil)
)
