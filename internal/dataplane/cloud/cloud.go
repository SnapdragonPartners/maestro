// Package cloud composes a data plane against managed cloud services.
//
// It is the sibling of `stack`, not a layer above or below it. `stack` resolves
// six local things and hands them to `plane`; this package resolves the same
// inputs from Cloud SQL and Cloud Storage and hands them to the same `plane`.
// Neither knows about the other, which is the point of #286: the composition is
// portable and the composers are not.
//
// # What this package deliberately does not do
//
// The cloud surface is NARROW BY DECISION: provisioning, migration, opening,
// importing, querying. There is no cloud backup, restore, reset, stop or
// recovery here.
//
// That is not an oversight to be filled in later by whoever needs it. The local
// lifecycle lock excludes destructive verbs against a shared data root, and
// there is currently no destructive cloud verb for an equivalent lock to
// exclude — so adding one would be machinery without a consumer. When the first
// such verb arrives it will need coordination that spans Postgres AND the
// object store together, which a database-session lock cannot provide: an
// advisory lock disappears when the database is restored, replaced, or
// unreachable, while an actor may still be reaching the bucket. That design
// belongs with the verb, against its concrete behaviour.
//
// # The vault is local, keyed from outside
//
// A cloud plane here runs the LOCAL secrets module with operator-provided root
// key material. That is a legitimate composition and it is NOT evidence that
// the secrets module is portable: cloud mode proper replaces that module
// entirely, because the provider stores and returns the secrets themselves.
// `secret.RootKeyProvider`'s own documentation says an implementation of it for
// cloud would have to invent a key it has no use for, so there is no cloud
// provider type here and there never should be.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/readiness"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
)

// Config locates a cloud plane and the material needed to open it.
//
//nolint:govet // fieldalignment: grouped by what it configures, read once at startup
type Config struct {
	// DSN reaches the database.
	//
	// Through the Cloud SQL Auth Proxy this is an ordinary local DSN — the
	// proxy listens on loopback and provides the TLS, so `sslmode=disable`
	// on the wire to the proxy is correct rather than careless. The instance
	// itself refuses unencrypted connections and has no authorized networks,
	// so there is no path that reaches it without the proxy.
	DSN string

	// Bucket is the object bucket. It must already exist; EnsureBucket
	// configures it but does not create it, for the same least-privilege
	// reason the object seam excludes bucket creation.
	Bucket string

	// RootKey is operator-provided root-of-trust material.
	//
	// Operator-provided means exactly that: handed to the process from
	// outside, by an environment variable or an injected secret. This package
	// does not read it from anywhere, cannot create it, and will not invent
	// one — an empty value is refused rather than defaulted, because a plane
	// that mints its own key produces a vault whose provenance nobody can
	// state.
	RootKey []byte
}

// validate refuses a configuration that cannot open a plane.
func (c Config) validate() error {
	switch {
	case c.DSN == "":
		return errors.New("open a cloud data plane: no database DSN was supplied")
	case c.Bucket == "":
		return errors.New("open a cloud data plane: no object bucket was supplied")
	case len(c.RootKey) == 0:
		return fmt.Errorf("open a cloud data plane: no operator-provided root key was supplied. "+
			"This package will not generate one: the root key derives the vault's key material, and "+
			"a key minted here would produce secrets whose provenance nobody can state: %w",
			secret.ErrNoRootKey)
	case len(c.RootKey) != paths.RootKeyLen:
		// The same bar as a key file, checked here so it fails at
		// configuration rather than at the vault. secret.ResolvedKey enforces
		// it centrally as well; this one exists so the message names the
		// configuration that is wrong.
		return fmt.Errorf("open a cloud data plane: the operator-provided root key is %d bytes, "+
			"want exactly %d — the same length a key file must be, because it protects the same "+
			"vault: %w", len(c.RootKey), paths.RootKeyLen, secret.ErrRootKeyLength)
	}
	return nil
}

// OpenSeam opens the persistence seam against a cloud plane.
//
// The GCS client is handed to `plane` as an OWNED resource rather than closed
// here, and that is the whole reason this function is more than four lines. The
// client holds pooled connections, it is not reachable through `store.Store`,
// and `objects.GCS.Close` is deliberately absent from the object seam because
// only one of the two adapters needs it — so nothing except the composition can
// close it. `plane.Open` releases it on every failure path and the returned
// store releases it on Close.
func OpenSeam(ctx context.Context, cfg Config, types *registry.Registry) (store.Store, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	// Before the client, deliberately. `plane` refuses a nil registry too, but
	// reaching that costs a network client this function would then have to
	// remember to close — and a resource that only needs closing on an error
	// path is the one that gets leaked.
	if types == nil {
		return nil, errors.New("open a cloud data plane: no artifact registry was supplied")
	}

	blob, err := objects.NewGCS(ctx, objects.GCSConfig{Bucket: cfg.Bucket})
	if err != nil {
		return nil, fmt.Errorf("reach the object bucket %s: %w", cfg.Bucket, err)
	}

	// The bucket is PROBED before a seam is returned, and this is not
	// belt-and-braces — it is what makes "the seam opened" mean the same thing
	// in both modes.
	//
	// `NewGCS` contacts no BUCKET: it resolves credentials — which may itself
	// use the network — and builds a handle, so a bucket that does not exist,
	// or that this identity cannot read, produces a perfectly usable-looking
	// client. The local composer has no
	// equivalent gap because its `ensureBucket` talks to the object store on
	// the way through. Without this, a cloud seam opens against a missing
	// bucket and fails at the first object read — a long way from the
	// configuration that caused it, and after the caller has been told the
	// plane is ready.
	//
	// MEASURED: an integration test opening against a non-existent bucket
	// succeeded before this existed.
	if probeErr := probeBucket(ctx, blob); probeErr != nil {
		// Crosses the seam as a readiness cause (design D6). The remedy is
		// neutral because this composer has no lifecycle verbs to name.
		return nil, unusableBucket(cfg.Bucket, readiness.Refuse(readiness.ObjectStoreUnusable,
			"the object bucket "+cfg.Bucket+" did not answer a listing",
			"create the bucket, or grant this identity read access to it", probeErr), blob)
	}

	// The root key is wrapped as OPERATOR-PROVIDED, which is a claim about
	// where it came from and must be true. Reporting the key file here would
	// make every diagnostic about the vault name a backend nobody configured,
	// on a plane that has no key file at all.
	keyProvider, err := secret.ResolvedKey(cfg.RootKey, secret.BackendOperatorProvided)
	if err != nil {
		// The client is not yet owned by anything, so this path closes it.
		return nil, unusableBucket(cfg.Bucket, fmt.Errorf("wrap the operator-provided root key: %w",
			err), blob)
	}

	seam, err := plane.Open(ctx, plane.Composition{
		DSN:     cfg.DSN,
		Objects: blob,
		RootKey: keyProvider,
		Types:   types,
		Owned: []plane.Owned{
			{What: "cloud object client for " + cfg.Bucket, Close: blob.Close},
		},
	})
	if err != nil {
		// No close here: ownership transferred with the composition, and
		// plane.Open released it. Closing again would report an error from a
		// client already shut down, on a path that is already failing.
		return nil, fmt.Errorf("open a cloud data plane on %s: %w", cfg.Bucket, err)
	}
	return seam, nil
}

// probeReachabilityPrefix is the key prefix the reachability probe lists.
//
// It is deliberately one no real object can carry — object keys are laid out as
// `<organization-uuid>/<aa>/<bb>/<digest>` — so the listing is expected to be
// empty and the probe stays cheap regardless of how much the bucket holds. What
// is being tested is whether the LISTING SUCCEEDS, not what it returns.
const probeReachabilityPrefix = ".maestro-reachability-probe/"

// probeBucket confirms the object store answers before a seam is handed out.
//
// It uses a version listing rather than a bucket-attributes read on purpose.
// Reading bucket metadata needs `storage.buckets.get`, a privilege the data path
// otherwise has no use for, and the object seam withholds bucket-level
// authority from adapters precisely so that serving reads does not require the
// rights to inspect or reconfigure the bucket. Listing versions is a privilege
// the sweep already needs, so this probe adds no new grant.
//
// An empty result is a PASS. A missing bucket, or one this identity cannot
// read, fails the listing itself.
func probeBucket(ctx context.Context, blob objects.Store) error {
	if _, err := blob.ListVersions(ctx, probeReachabilityPrefix); err != nil {
		return fmt.Errorf("the object store did not answer a listing: %w", err)
	}
	return nil
}

// unusableBucket reports why a cloud plane could not be opened and closes the
// object client, which nothing owns yet on these paths.
//
// The original cause is always returned; a failure to close is joined onto it
// rather than replacing it, since a client that would not shut down does not
// make the open have succeeded.
func unusableBucket(bucket string, cause error, blob *objects.GCS) error {
	joined := errors.Join(fmt.Errorf("the object bucket %s is not usable: %w", bucket, cause),
		closeBlob(blob))
	return fmt.Errorf("open a cloud data plane: %w", joined)
}

// closeBlob adds context to a client close that failed on an error path.
func closeBlob(blob *objects.GCS) error {
	if err := blob.Close(); err != nil {
		return fmt.Errorf("close the cloud object client: %w", err)
	}
	return nil
}

// Migrate applies the schema to a cloud plane.
//
// It takes nothing but the DSN because migrations need nothing else: the
// migration set is embedded, and `migrations.Up` is already provider-neutral.
// Concurrent callers are serialized by golang-migrate's own PostgreSQL advisory
// lock rather than by anything here.
//
// A failed migration leaves the recorded version DIRTY, and every later
// migration refuses until that is cleared. That is a gated state rather than a
// transient one, and this function does not clear it — repairing it is a
// deliberate operator action, not something a retry should paper over.
func Migrate(ctx context.Context, cfg Config) error {
	if cfg.DSN == "" {
		return errors.New("migrate a cloud data plane: no database DSN was supplied")
	}
	if err := migrations.Up(ctx, cfg.DSN); err != nil {
		return fmt.Errorf("migrate the cloud data plane schema: %w", err)
	}
	return nil
}

// SoftDeleteStabilization is how long a bucket must have been settled before
// its soft-delete configuration is acted on.
//
// It is MAESTRO POLICY, not a propagation guarantee. Google advises waiting at
// least 30 seconds after disabling soft delete and gives no upper bound, while
// the attribute read reports the configured value immediately — so there is a
// window in which a bucket says retention is zero and still retains what it
// deletes. Sixty seconds is a conservative choice above a stated minimum;
// elapsing it proves nothing on its own. What it buys is that this package
// refuses to treat a just-changed bucket as verified.
const SoftDeleteStabilization = 60 * time.Second
