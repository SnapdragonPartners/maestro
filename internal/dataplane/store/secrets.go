package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/secret"
)

// The secrets vault (item 7 design, D5).
//
// A secret is a name, a lineage scope, an optional owning user, and an
// encryption envelope. It shares the configuration family's lineage and
// nothing else: configuration is unencrypted and resolves by scope alone,
// while a secret carries an envelope and resolves by scope AND ownership.

// ErrSecretConflict reports that a replacement or deletion affected no rows.
//
// It is deliberately AMBIGUOUS between "somebody else moved first" and "that
// secret is not yours". Both are true statements about the caller's write —
// it did not apply — and separating them would let a caller probe for the
// existence of credentials it may not read. A caller re-reads and retries in
// either case, so the distinction buys nothing it could act on.
//
// Note the asymmetry with ErrConfigurationConflict, which IS separated from
// ErrNotFound: configuration has no per-user ownership, so there is no
// existence to conceal.
var ErrSecretConflict = errors.New("secret was modified concurrently, or is not yours")

// ErrActingUserNotAMember reports a creation whose acting user does not
// belong to the organization it named.
//
// Separate from ErrSecretConflict, and safe to be: it conceals nothing a
// caller does not already know, since the caller supplied both its own
// identity and the organization. It is a distinct outcome from a refused
// write because the fix is different — a membership problem is not
// something re-reading and retrying will resolve.
var ErrActingUserNotAMember = errors.New("acting user is not a member of that organization")

// SecretOwnership reports whether the secret that answered a resolution is
// the caller's own or one shared across the scope.
//
// Returned rather than inferred, because attribution is preserved by
// recording WHICH secret answered rather than by preferring a credential
// that may not work. A caller that cannot tell which it got cannot report
// "you are using the team token" to anybody.
type SecretOwnership string

const (
	// SecretIndividual is a credential belonging to the acting user.
	SecretIndividual SecretOwnership = "individual"
	// SecretShared is a credential held in common at its scope.
	SecretShared SecretOwnership = "shared"
)

// Secret is one stored credential, WITHOUT its plaintext.
//
// The envelope stays sealed here. Decryption is a separate, explicit call,
// so a listing or an identity read cannot leak plaintext into a log line by
// being formatted.
type Secret struct {
	// OwnerUserID is nil for a shared secret.
	OwnerUserID *uuid.UUID

	CreatedAt time.Time
	UpdatedAt time.Time

	Name string

	// Ownership is the same fact as OwnerUserID in the form a caller acts
	// on, and is what resolution reports for attribution.
	Ownership SecretOwnership

	// Scope is the lineage level this secret was issued for.
	Scope ConfigScope

	ID             uuid.UUID
	OrganizationID uuid.UUID

	// Version is both the optimistic-concurrency token AND part of the key
	// derivation context, which is why replacement must seal for version+1
	// before writing: every stored ciphertext gets its own key, making
	// nonce reuse structurally impossible rather than improbable.
	Version int
}

// CreateSecretInput is the creation input for BOTH creation verbs.
//
// It carries no owner and no ownership FLAG, and both absences are the
// security property.
//
// An owner the caller supplies is an owner the caller can lie about, and the
// damage is not a mislabelled row: the partial unique index gives each user
// exactly one slot per name and scope, so a secret created AS somebody else
// occupies their slot — the victim's own creation then fails against a row
// they cannot read, replace, or delete. A poisoned slot is a denial of
// service the victim cannot diagnose or clear.
//
// A boolean would be nearly as bad, which is why there is not one. Ownership
// chosen by a field is ownership chosen by a field's DEFAULT: an input built
// with the flag omitted picks a semantic silently, and the zero value of a
// bool is a decision nobody wrote down. So the choice is the METHOD —
// CreateIndividualSecret or CreateSharedSecret — and it cannot be defaulted,
// forgotten, or set from deserialised input.
//
// A structural test asserts this type's EXACT field set, so adding one is a
// change that fails the build and has to be argued for.
type CreateSecretInput struct {
	// Plaintext is a secret.Value rather than a []byte, so it is redacted
	// on the way IN as well as on the way out. A raw slice on this surface
	// would be leaked by ordinary formatting of the input struct — %+v on
	// a request, an error body quoting its arguments — which is the exact
	// leak secret.Value exists to prevent, reintroduced one field short of
	// the boundary it guards.
	Plaintext secret.Value

	Name string

	Scope ConfigScope

	OrganizationID uuid.UUID

	// ActingUserID is the authenticated caller. It becomes the owner of an
	// individual secret and is checked for organization membership either
	// way — a shared secret has a NULL owner, so nothing else on the row
	// would mention the caller at all.
	ActingUserID uuid.UUID
}

// SecretReader is the vault's read surface.
type SecretReader interface {
	// ResolveSecret walks the six-step ladder for a repository:
	//
	//	1 repository / caller    4 product      / shared
	//	2 repository / shared    5 organization / caller
	//	3 product    / caller    6 organization / shared
	//
	// Specificity is the OUTER sort and ownership breaks ties within a
	// level. Scope says which resource a credential works against, and a
	// credential for the wrong resource does not function no matter whose
	// it is, while a shared credential for the right one does. Preferring
	// ownership across levels would reach past a repository deploy key for
	// a personal organization-wide token with no access to that repository.
	//
	// No user ever resolves another user's secret; the filter is in the
	// query, not applied after the read.
	ResolveSecret(ctx context.Context, organizationID, repositoryID, actingUserID uuid.UUID, name string) (*Secret, error)

	// GetSecret reads one secret by identity, under the same ownership
	// filter.
	GetSecret(ctx context.Context, organizationID, secretID, actingUserID uuid.UUID) (*Secret, error)

	// RevealSecret decrypts one secret's plaintext.
	//
	// Separate from the reads because decryption is the act worth making
	// explicit and greppable. The returned Value renders as [redacted]
	// through every formatting verb; reaching the bytes takes a further
	// deliberate Reveal.
	RevealSecret(ctx context.Context, organizationID, secretID, actingUserID uuid.UUID) (secret.Value, error)
}

// SecretWriter is the vault's write surface.
//
// Every verb carries the acting-user predicate, not just the reads. A read
// predicate alone gives an access model where one user cannot SEE another's
// credential but can freely replace or delete it — and the destructive half
// is the more damaging one, since a caller who cannot read a secret also
// cannot tell what they destroyed.
type SecretWriter interface {
	// CreateIndividualSecret writes a credential owned by the acting user.
	CreateIndividualSecret(ctx context.Context, input CreateSecretInput) (*Secret, error)

	// CreateSharedSecret writes a credential held in COMMON at its scope,
	// readable by every member of the organization who resolves it.
	//
	// A separate call rather than a flag, because the two are different
	// requests with different blast radii and the difference must be
	// legible at the call site. "Did this create a personal token or a team
	// one?" is answerable by reading the line, not by tracing where a
	// struct field was last assigned.
	CreateSharedSecret(ctx context.Context, input CreateSecretInput) (*Secret, error)

	// ReplaceSecret rotates a credential in place, conditional on the
	// version the caller read.
	//
	// It is an UPDATE, not an append. Item 6's "accepted rows are never
	// rewritten" is an ARTIFACT rule: an artifact is reviewed history whose
	// immutability is the point, while a secret is a live credential.
	// Keeping superseded ciphertexts would make every rotated-away token
	// recoverable forever, turning rotation — whose whole purpose is to end
	// a credential's usefulness — into an archive of credentials.
	//
	// What that promises is narrower than it sounds, and the narrow version
	// is the honest one: a replaced secret is no longer ADDRESSABLE through
	// the vault. It is not erased. Postgres leaves the old tuple dead until
	// vacuum, the old value survives in the WAL and in every backup taken
	// before the rotation, and the old key stays derivable from the root
	// key, the secret id and the previous version because HKDF is
	// deterministic. Anyone needing cryptographic erasure needs a different
	// design; this item does not provide one.
	ReplaceSecret(ctx context.Context, organizationID, secretID, actingUserID uuid.UUID, expectedVersion int, plaintext secret.Value) (*Secret, error)

	// DeleteSecret removes a credential, conditional on its version.
	//
	// Conditional for the same reason replacement is: a plain delete races
	// a rotation, so an operator removing what they believe is stale can
	// erase a replacement committed a moment earlier, and an unconditional
	// delete reports success either way.
	DeleteSecret(ctx context.Context, organizationID, secretID, actingUserID uuid.UUID, expectedVersion int) error
}
