package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/canonical"
)

// SchemeAESGCMv1 is the only encryption scheme this item implements.
//
// A named string rather than an opaque integer, because a row carries it
// and a reader has to decide what to do from it. The name says the cipher,
// the derivation and the version, so the next scheme is a new constant
// beside this one rather than an edit to the meaning of an existing value:
// item 6's rule applies here too, that the READER is the compatibility
// layer and stored rows are never rewritten.
const SchemeAESGCMv1 = "aes-256-gcm/hkdf-sha256/v1"

// nonceBytes is AES-GCM's standard nonce length, and the length the schema's
// own CHECK enforces.
const nonceBytes = 12

// ErrUnknownScheme reports a stored row this build cannot decrypt. It is
// deliberately distinct from a decryption failure: one means the data is
// newer than the code, the other means the data is wrong.
var ErrUnknownScheme = errors.New("unknown secret encryption scheme")

// ErrDecrypt reports a ciphertext that did not authenticate — tampered,
// truncated, sealed under different key material, or bound to a different
// identity. GCM cannot tell these apart, and neither can this error.
var ErrDecrypt = errors.New("secret did not decrypt")

// Binding is everything a ciphertext is tied to.
//
// These are not incidental columns; they are the fields that decide WHO MAY
// READ the secret and WHAT it is for. The key derivation covers the id and
// version, so without binding the rest an `UPDATE secrets SET owner_user_id
// = <somebody else>` would leave the ciphertext perfectly decryptable and
// hand one person's credential to another. Changing the name or the scope
// would likewise retarget a working credential at a resource it was never
// issued for.
//
// So all of it goes into the authenticated data, and a row whose metadata
// was edited underneath the seam fails to open (design D2).
type Binding struct {
	// OwnerUserID is nil for a shared secret. Its ABSENCE is bound too: a
	// shared secret promoted to an individual one, or the reverse, is a
	// different secret as far as the seal is concerned.
	OwnerUserID *uuid.UUID

	Name      string
	ScopeType string
	Scheme    string

	OrganizationID uuid.UUID
	SecretID       uuid.UUID
	ScopeID        uuid.UUID

	Version int
}

// Envelope is one sealed secret, exactly as the row stores it.
type Envelope struct {
	Scheme     string
	Nonce      []byte
	Ciphertext []byte
}

// Seal encrypts plaintext for one binding.
//
// The caller allocates the secret id and version BEFORE calling, because the
// key is derived from them — the same reason item 6 preallocates ids for its
// cross-store commit order. An id assigned by the INSERT would be an id the
// encryption could not have used.
//
// its own copy, and a pointer would edit the caller's binding as a side effect
// of encrypting with it.
//
//nolint:gocritic // hugeParam: BY VALUE deliberately. Seal fixes the scheme on
func Seal(rootKey []byte, binding Binding, plaintext []byte) (Envelope, error) {
	binding.Scheme = SchemeAESGCMv1

	sealed, err := aeadFor(rootKey, &binding)
	if err != nil {
		return Envelope{}, err
	}
	additional, err := additionalData(&binding)
	if err != nil {
		return Envelope{}, err
	}

	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate nonce: %w", err)
	}

	return Envelope{
		Scheme:     binding.Scheme,
		Nonce:      nonce,
		Ciphertext: sealed.Seal(nil, nonce, plaintext, additional),
	}, nil
}

// Open decrypts an envelope under the binding the caller believes it has.
//
// The scheme comes from the ENVELOPE, not from the caller: the row records
// what sealed it, and opening it under today's scheme would be assuming the
// answer. It is then bound into the authenticated data, so a row whose
// scheme column was edited fails rather than being read under the wrong one.
//
// from the envelope and writes it to its own copy.
//
//nolint:gocritic // hugeParam: by value, matching Seal — Open takes the scheme
func Open(rootKey []byte, binding Binding, envelope Envelope) (Value, error) {
	if envelope.Scheme != SchemeAESGCMv1 {
		return Value{}, fmt.Errorf("%w: %q", ErrUnknownScheme, envelope.Scheme)
	}
	binding.Scheme = envelope.Scheme

	if len(envelope.Nonce) != nonceBytes {
		return Value{}, fmt.Errorf("%w: nonce is %d bytes, want %d",
			ErrDecrypt, len(envelope.Nonce), nonceBytes)
	}

	sealed, err := aeadFor(rootKey, &binding)
	if err != nil {
		return Value{}, err
	}
	additional, err := additionalData(&binding)
	if err != nil {
		return Value{}, err
	}

	plaintext, err := sealed.Open(nil, envelope.Nonce, envelope.Ciphertext, additional)
	if err != nil {
		// Deliberately not wrapped: GCM's failure carries no detail worth
		// surfacing, and what a caller can act on is that this secret is
		// unreadable, not which of several indistinguishable causes applied.
		return Value{}, ErrDecrypt
	}
	return NewValue(plaintext), nil
}

// KeyContext is the derivation context for one VERSION of one secret.
//
// Per version rather than per secret, which is what makes nonce reuse
// structurally impossible instead of merely improbable: replacement rewrites
// the row, and a per-secret key would draw every rewrite's nonce from the
// same 96-bit space. Binding the version means each stored ciphertext has
// its own key, so there is no birthday bound to budget for and no counter to
// trust.
func KeyContext(secretID uuid.UUID, version int) string {
	return fmt.Sprintf("maestro/dataplane/secret/v1/%s/%d", secretID, version)
}

// aeadFor builds the cipher for one binding's key.
func aeadFor(rootKey []byte, binding *Binding) (cipher.AEAD, error) {
	if binding.Version < 1 {
		return nil, fmt.Errorf("secret version is %d: a version is part of the key context and "+
			"must be the row's own", binding.Version)
	}
	key, err := DeriveKey(rootKey, KeyContext(binding.SecretID, binding.Version))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("build cipher: %w", err)
	}
	sealed, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build GCM: %w", err)
	}
	return sealed, nil
}

// additionalData renders the binding as authenticated data.
//
// It is the CANONICAL DIGEST of the binding rather than its fields joined
// together, because joining variable-length fields is ambiguous: a name of
// "ab" beside "c" produces the same bytes as "a" beside "bc", so two
// different rows could share an AAD. The digest is fixed-length and
// unambiguous by construction, and it reuses the JCS encoder ADR 0028
// already relies on — including its distinct rendering of a null owner,
// which no empty-string convention could keep separate from a real value.
func additionalData(binding *Binding) ([]byte, error) {
	var owner *string
	if binding.OwnerUserID != nil {
		rendered := binding.OwnerUserID.String()
		owner = &rendered
	}

	digest, err := canonical.Digest(map[string]any{
		"organization_id": binding.OrganizationID.String(),
		"secret_id":       binding.SecretID.String(),
		"version":         binding.Version,
		"scheme":          binding.Scheme,
		"owner_user_id":   owner,
		"name":            binding.Name,
		"scope_type":      binding.ScopeType,
		"scope_id":        binding.ScopeID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("render authenticated data: %w", err)
	}
	return []byte(digest), nil
}
