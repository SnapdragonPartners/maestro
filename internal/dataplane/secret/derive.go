// Package secret derives service credentials from the root-of-trust key.
//
// The secrets vault lives inside Postgres (ADR 0022), so the credential
// that opens Postgres cannot live in the vault. Deriving it from the
// root-of-trust key keeps the key file as the single external secret and
// keeps the bootstrap pointer free of anything that unlocks anything —
// which is also what lets the cold backup, which excludes the key, be
// copied around safely.
package secret

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// Derivation contexts. Each is a distinct, versioned domain: HKDF's info
// parameter is what guarantees two consumers of the same root key cannot
// produce the same output, so every new consumer MUST add a context here
// rather than reuse one. The version suffix exists so a future change in
// how a credential is used can rotate it without touching the root key.
const (
	ContextPostgresPassword = "maestro/dataplane/postgres-password/v1"
	ContextObjectAccessKey  = "maestro/dataplane/object-access-key/v1"
	ContextObjectSecretKey  = "maestro/dataplane/object-secret-key/v1"
)

// derivedBytes is the raw length drawn from HKDF before encoding. 32 bytes
// is well beyond what any of these credentials needs and costs nothing.
const derivedBytes = 32

// ErrNoRootKey reports a derivation attempted without key material.
var ErrNoRootKey = errors.New("root-of-trust key is empty")

// Derive returns a printable credential for the given context.
//
// The root key is never used directly as a credential and never encoded
// into one: every output goes through HKDF-SHA-256 with an explicit,
// domain-separated info string, so recovering the root key from a
// credential — or reproducing one credential from another — is not
// possible. Output is raw-URL base64, which is safe in connection strings,
// environment variables, and CLI arguments without escaping.
func Derive(rootKey []byte, context string) (string, error) {
	key, err := DeriveKey(rootKey, context)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

// DeriveKey returns the RAW derived bytes, before any encoding.
//
// Derive's output is a printable credential -- raw-URL base64, so it can sit
// in a connection string without escaping -- and that makes it the wrong
// thing to hand a cipher: the encoded form of a 32-byte key is 43 bytes, so
// a caller passing it as an AES-256 key would be using the wrong length and
// the wrong bytes while everything still compiled.
//
// The vault needs the key material itself, so it takes it from here. Both
// functions run the same derivation for the same context, which is what
// keeps "one context, one secret" true across the encoded and raw forms.
func DeriveKey(rootKey []byte, context string) ([]byte, error) {
	if len(rootKey) == 0 {
		return nil, ErrNoRootKey
	}
	if context == "" {
		return nil, errors.New("derivation context is required")
	}
	key, err := hkdf.Key(sha256.New, rootKey, nil, context, derivedBytes)
	if err != nil {
		return nil, fmt.Errorf("derive %s: %w", context, err)
	}
	return key, nil
}
