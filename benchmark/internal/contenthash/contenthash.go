// Package contenthash produces stable content identities for authored
// benchmark inputs (golden story definitions and MPH configuration bundles).
//
// Identities hash a canonical JSON serialization of the validated semantic
// content, never raw file bytes: comments, whitespace, and serialization
// order are not identity, and the same content re-materialized from a
// different store (the Phase 2 data plane) reproduces the same hash.
package contenthash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
)

// Prefix identifies the hash algorithm in every emitted identity string.
const Prefix = "sha256:"

//nolint:gochecknoglobals // Package-level compiled regex for performance.
var identityPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// CanonicalJSON returns the "sha256:<hex>" identity of v's canonical JSON
// form. The value is marshaled, decoded into generic maps, and re-marshaled
// so that object keys are sorted and the result is independent of Go struct
// field declaration order. Decoding uses json.Number so integer values
// keep full precision instead of collapsing to float64.
func CanonicalJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal for canonical hash: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var generic any
	if decodeErr := decoder.Decode(&generic); decodeErr != nil {
		return "", fmt.Errorf("decode for canonical hash: %w", decodeErr)
	}
	canonical, err := json.Marshal(generic)
	if err != nil {
		return "", fmt.Errorf("canonical re-marshal: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return Prefix + hex.EncodeToString(sum[:]), nil
}

// Valid reports whether id is a well-formed content identity: the sha256
// prefix followed by exactly 64 lowercase hex digits. Every identity field
// in the run-record contract validates through this single definition.
func Valid(id string) bool {
	return identityPattern.MatchString(id)
}
