// Package contenthash produces stable content identities for authored
// benchmark inputs (golden story definitions and MPH configuration bundles).
//
// Identities hash a canonical JSON serialization of the validated semantic
// content, never raw file bytes: comments, whitespace, and serialization
// order are not identity, and the same content re-materialized from a
// different store (the Phase 2 data plane) reproduces the same hash.
package contenthash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Prefix identifies the hash algorithm in every emitted identity string.
const Prefix = "sha256:"

// CanonicalJSON returns the "sha256:<hex>" identity of v's canonical JSON
// form. The value is marshaled, decoded into generic maps, and re-marshaled
// so that object keys are sorted and the result is independent of Go struct
// field declaration order.
func CanonicalJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal for canonical hash: %w", err)
	}
	var generic any
	if unmarshalErr := json.Unmarshal(raw, &generic); unmarshalErr != nil {
		return "", fmt.Errorf("decode for canonical hash: %w", unmarshalErr)
	}
	canonical, err := json.Marshal(generic)
	if err != nil {
		return "", fmt.Errorf("canonical re-marshal: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return Prefix + hex.EncodeToString(sum[:]), nil
}
