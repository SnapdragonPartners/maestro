package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
)

// The secrets vault (item 7 design, D2 and D5).
//
// Sealing and opening happen HERE, at the seam, so no caller ever holds a
// ciphertext and no caller ever chooses a key. The envelope's authenticated
// data binds every field that decides who may read a secret, which is why
// the binding is rebuilt from the STORED row on the way out rather than
// from anything a caller supplied.

// secretRecord converts a stored row to the seam's type, without opening it.
func secretRecord(row *gen.Secret) (*store.Secret, error) {
	scopeType := configkeys.Scope(row.ScopeType)
	switch scopeType {
	case configkeys.ScopeOrganization, configkeys.ScopeProduct, configkeys.ScopeRepository:
	default:
		return nil, fmt.Errorf("%w: secret %s has scope type %q",
			store.ErrInvariant, fromUUID(row.SecretID), row.ScopeType)
	}

	owner := fromNullUUID(row.OwnerUserID)
	ownership := store.SecretShared
	if owner != nil {
		ownership = store.SecretIndividual
	}

	return &store.Secret{
		ID:             fromUUID(row.SecretID),
		OrganizationID: fromUUID(row.OrganizationID),
		Name:           row.Name,
		OwnerUserID:    owner,
		Ownership:      ownership,
		Scope:          store.ConfigScope{Type: scopeType, ID: fromUUID(row.ScopeID)},
		Version:        int(row.Version),
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		UpdatedAt:      fromTimestamptz(row.UpdatedAt),
	}, nil
}

// bindingFor rebuilds the authenticated data for a stored row.
//
// Every field here is one the AAD binds, and the reason is what each one
// decides. The owner decides WHO may read the secret; the name and scope
// decide WHAT it is for. Editing any of them underneath the seam would hand
// one person's credential to another or retarget a working credential at a
// resource it was never issued for — so a row whose metadata was changed
// outside this seam fails to open rather than opening as something else.
//
// Built from the ROW, never from a caller's claim about the row. A binding
// assembled from arguments would authenticate what the caller believed
// rather than what is stored, which is the same as not authenticating.
func bindingFor(row *gen.Secret) secret.Binding {
	return secret.Binding{
		OrganizationID: fromUUID(row.OrganizationID),
		SecretID:       fromUUID(row.SecretID),
		OwnerUserID:    fromNullUUID(row.OwnerUserID),
		Name:           row.Name,
		ScopeType:      row.ScopeType,
		ScopeID:        fromUUID(row.ScopeID),
		Version:        int(row.Version),
	}
}

// secretNotWritten maps a zero-row write onto the seam's deliberately
// ambiguous conflict.
//
// The ambiguity is the point. "Somebody else moved first" and "that secret
// is not yours" are both true statements about the caller's write — it did
// not apply — and separating them would let a caller probe for the existence
// of credentials it may not read.
func secretNotWritten(verb string, secretID uuid.UUID, expectedVersion int) error {
	return fmt.Errorf("%w: %s of secret %s at version %d affected no rows",
		store.ErrSecretConflict, verb, secretID, expectedVersion)
}

// CreateIndividualSecret writes a credential owned by the acting user.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CreateIndividualSecret(
	ctx context.Context, input store.CreateSecretInput,
) (*store.Secret, error) {
	// The owner is the acting user, taken here and not from any input
	// field. There is no field it could have come from.
	owner := input.ActingUserID
	return t.createSecret(ctx, input, &owner)
}

// CreateSharedSecret writes a credential held in common at its scope.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CreateSharedSecret(
	ctx context.Context, input store.CreateSecretInput,
) (*store.Secret, error) {
	return t.createSecret(ctx, input, nil)
}

// createSecret is the shared implementation. Ownership reaches it as an
// argument the two exported verbs supply, never as something read from the
// caller's input — which is what makes "who owns this" a property of which
// function was called.
//
//nolint:gocritic // hugeParam: by value, matching the exported verbs
func (t *tx) createSecret(
	ctx context.Context, input store.CreateSecretInput, owner *uuid.UUID,
) (*store.Secret, error) {
	arc, err := configScopeColumns(input.Scope)
	if err != nil {
		return nil, err
	}

	secretID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}

	rootKey, err := t.rootKey.RootKey()
	if err != nil {
		return nil, fmt.Errorf("read the root key to seal secret %q: %w", input.Name, err)
	}

	// Version 1 is what the INSERT will store, and it is part of the key
	// derivation context — so the value sealed here is sealed for the row
	// that is about to exist, not for the row as it is read back.
	//
	// Reveal is called HERE and nowhere earlier: this is the boundary where
	// plaintext must become bytes, and keeping the call at the cipher's
	// doorstep is what makes every escape from secret.Value greppable.
	envelope, err := secret.Seal(rootKey, secret.Binding{
		OrganizationID: input.OrganizationID,
		SecretID:       secretID,
		OwnerUserID:    owner,
		Name:           input.Name,
		ScopeType:      string(input.Scope.Type),
		ScopeID:        input.Scope.ID,
		Version:        1,
	}, input.Plaintext.Reveal())
	if err != nil {
		return nil, fmt.Errorf("seal secret %q: %w", input.Name, err)
	}

	row, err := t.queries.CreateSecret(ctx, gen.CreateSecretParams{
		SecretID:            toUUID(secretID),
		OrganizationID:      toUUID(input.OrganizationID),
		Name:                input.Name,
		OwnerUserID:         toNullUUID(owner),
		ScopeType:           string(input.Scope.Type),
		ScopeOrganizationID: toNullUUID(arc.organizationID),
		ScopeProductID:      toNullUUID(arc.productID),
		ScopeRepositoryID:   toNullUUID(arc.repositoryID),
		Scheme:              envelope.Scheme,
		Nonce:               envelope.Nonce,
		Ciphertext:          envelope.Ciphertext,
		ActingUserID:        toUUID(input.ActingUserID),
	})
	if err != nil {
		// The statement is an INSERT ... SELECT guarded by the acting
		// user's membership, so no rows means the caller does not belong to
		// this organization. Without that guard a caller could create a
		// SHARED secret — whose owner is null, so nothing else on the row
		// mentions them — in any organization whose id it could name.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: user %s is not a member of organization %s",
				store.ErrActingUserNotAMember, input.ActingUserID, input.OrganizationID)
		}
		return nil, fmt.Errorf("create secret %q: %w", input.Name, err)
	}
	return secretRecord(&row)
}

// ResolveSecret walks the six-step ladder.
func (t *tx) ResolveSecret(
	ctx context.Context, organizationID, repositoryID, actingUserID uuid.UUID, name string,
) (*store.Secret, error) {
	row, err := t.queries.ResolveSecretForRepository(ctx, gen.ResolveSecretForRepositoryParams{
		OrganizationID: toUUID(organizationID),
		Name:           name,
		ActingUserID:   toUUID(actingUserID),
		RepositoryID:   toUUID(repositoryID),
	})
	if err != nil {
		return nil, notFound(err, "secret for repository", repositoryID)
	}
	return secretRecord(&row)
}

// GetSecret reads one secret by identity, under the ownership filter.
func (t *tx) GetSecret(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID,
) (*store.Secret, error) {
	row, err := t.getSecretRow(ctx, organizationID, secretID, actingUserID)
	if err != nil {
		return nil, err
	}
	return secretRecord(&row)
}

// getSecretRow is the shared read. It exists because three operations need
// the RAW row rather than the seam's type: reveal and replace need the
// envelope and the binding fields, which the seam's type deliberately does
// not carry.
func (t *tx) getSecretRow(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID,
) (gen.Secret, error) {
	row, err := t.queries.GetSecret(ctx, gen.GetSecretParams{
		OrganizationID: toUUID(organizationID),
		SecretID:       toUUID(secretID),
		ActingUserID:   toUUID(actingUserID),
	})
	if err != nil {
		return gen.Secret{}, notFound(err, "secret", secretID)
	}
	return row, nil
}

// RevealSecret decrypts one secret.
func (t *tx) RevealSecret(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID,
) (secret.Value, error) {
	row, err := t.getSecretRow(ctx, organizationID, secretID, actingUserID)
	if err != nil {
		return secret.Value{}, err
	}

	rootKey, err := t.rootKey.RootKey()
	if err != nil {
		return secret.Value{}, fmt.Errorf("read the root key to open secret %s: %w", secretID, err)
	}

	// The scheme comes from the envelope, not from today's default: the row
	// records what sealed it, and opening it under the current scheme would
	// be assuming the answer.
	value, err := secret.Open(rootKey, bindingFor(&row), secret.Envelope{
		Scheme:     row.Scheme,
		Nonce:      row.Nonce,
		Ciphertext: row.Ciphertext,
	})
	if err != nil {
		return secret.Value{}, fmt.Errorf("open secret %s: %w", secretID, err)
	}
	return value, nil
}

// ReplaceSecret rotates a credential in place.
func (t *tx) ReplaceSecret(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID,
	expectedVersion int, plaintext secret.Value,
) (*store.Secret, error) {
	version, err := toInt32(expectedVersion, "expected secret version")
	if err != nil {
		return nil, err
	}

	// The row is read WITHOUT a lock, deliberately, and the reason is that
	// nothing read from it can go stale in a way that matters. Every field
	// the new binding needs — name, scope, owner — is immutable: the
	// permitted UPDATE assigns only the envelope columns and the version,
	// enforced as an allow-list by versionedSetColumns in the queries
	// structure test, which carries a note pointing back here. The one
	// field that does change is the version, and that comes from the
	// CALLER, not from this read.
	//
	// So this read supplies constants and the write is guarded by the
	// caller's version. There is no window between them in which a value
	// used here could become wrong.
	row, err := t.getSecretRow(ctx, organizationID, secretID, actingUserID)
	if err != nil {
		// The pre-read is under the ownership filter, so another user's
		// secret arrives here as ErrNotFound. That must NOT become this
		// verb's answer: the read is an implementation detail of building
		// the binding, and letting it set the error contract makes
		// replacement report "no such secret" where deletion — which has
		// no pre-read — reports a refused write for the identical
		// situation. One verb answering differently from the other about
		// the same state is the inconsistency, and it is the write
		// contract that has to win.
		if errors.Is(err, store.ErrNotFound) {
			return nil, secretNotWritten("replacement", secretID, expectedVersion)
		}
		return nil, err
	}

	rootKey, err := t.rootKey.RootKey()
	if err != nil {
		return nil, fmt.Errorf("read the root key to replace secret %s: %w", secretID, err)
	}

	// Sealed for expectedVersion+1, which is what the UPDATE will store.
	// Binding the version into the key context gives every stored
	// ciphertext its own key, which is what makes nonce reuse across
	// replacements structurally impossible rather than improbable — there
	// is no birthday budget to reason about and no counter to trust.
	binding := bindingFor(&row)
	binding.Version = expectedVersion + 1
	envelope, err := secret.Seal(rootKey, binding, plaintext.Reveal())
	if err != nil {
		return nil, fmt.Errorf("seal replacement for secret %s: %w", secretID, err)
	}

	replaced, err := t.queries.ReplaceSecret(ctx, gen.ReplaceSecretParams{
		Scheme:          envelope.Scheme,
		Nonce:           envelope.Nonce,
		Ciphertext:      envelope.Ciphertext,
		OrganizationID:  toUUID(organizationID),
		SecretID:        toUUID(secretID),
		ExpectedVersion: version,
		ActingUserID:    toUUID(actingUserID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, secretNotWritten("replacement", secretID, expectedVersion)
		}
		return nil, fmt.Errorf("replace secret %s: %w", secretID, err)
	}
	return secretRecord(&replaced)
}

// DeleteSecret removes a credential under its expected version.
func (t *tx) DeleteSecret(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID, expectedVersion int,
) error {
	version, err := toInt32(expectedVersion, "expected secret version")
	if err != nil {
		return err
	}

	affected, err := t.queries.DeleteSecret(ctx, gen.DeleteSecretParams{
		OrganizationID:  toUUID(organizationID),
		SecretID:        toUUID(secretID),
		ExpectedVersion: version,
		ActingUserID:    toUUID(actingUserID),
	})
	if err != nil {
		return fmt.Errorf("delete secret %s: %w", secretID, err)
	}
	if affected == 0 {
		// Not classified further, unlike configuration's delete. There the
		// seam separates a missing record from a version conflict because a
		// caller acts differently on each. Here it must NOT: a caller who
		// may not read a secret must not learn whether it exists, so
		// "gone", "moved", and "not yours" are one answer.
		return secretNotWritten("deletion", secretID, expectedVersion)
	}
	return nil
}
