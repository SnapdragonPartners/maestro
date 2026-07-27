// Package canonical computes the data plane's content digests.
//
// Every digest ADR 0028 requires — payload digests, review digests, the MPH
// seeding set — comes from this one function, over RFC 8785 (JCS) canonical
// JSON. That matters because evidence references detect tampering and
// retention bugs by comparing digests: a digest that moves when a
// serializer reorders keys detects nothing.
//
// The JCS implementation is a library rather than a hand-rolled
// approximation, as ADR 0028 requires. Phase 1's runner has its own,
// different canonicalization (Go's byte-wise key order, HTML escaping,
// literal number text); the two are separate hash domains and runner
// identities entering the plane are opaque strings, never re-derived here.
package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/gowebpki/jcs"
)

// SafeIntegerMax is the largest magnitude a payload number may carry. JCS
// serializes numbers as IEEE-754 binary64, so beyond 2^53-1 consecutive
// integers stop being distinguishable and a value written once no longer
// reads back as itself.
//
// The bound is on the VALUE, not on whether the number was spelled as an
// integer. ADR 0028 states it as "no JSON number outside ±(2^53-1)", and a
// rule that admitted 1e30 because of its notation would not be that rule.
const SafeIntegerMax = 1<<53 - 1

// ErrUnsafeNumber reports a JSON number that cannot survive
// canonicalization. It is a distinct error because the fix is a schema
// change — encode the value as a string — rather than a retry.
var ErrUnsafeNumber = errors.New("payload contains a number outside the JCS-safe range")

// Digest returns the lowercase 64-hex SHA-256 of value's canonical JSON.
//
// It rejects unsafe numbers before hashing rather than after: a digest over
// a payload that cannot round-trip is a digest of something the database
// will not hold, which is worse than no digest at all.
func Digest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal for canonical digest: %w", err)
	}
	return DigestJSON(raw)
}

// DigestJSON returns the digest of already-encoded JSON.
func DigestJSON(raw []byte) (string, error) {
	if err := CheckSafeNumbers(raw); err != nil {
		return "", err
	}
	canonicalJSON, err := jcs.Transform(raw)
	if err != nil {
		return "", fmt.Errorf("canonicalize (RFC 8785): %w", err)
	}
	sum := sha256.Sum256(canonicalJSON)
	return hex.EncodeToString(sum[:]), nil
}

// CheckSafeNumbers walks raw and rejects any number outside the range JCS
// can represent exactly.
//
// This is the universal rule from ADR 0028: it belongs to the encoding
// rather than to any one schema, so it applies to every payload of every
// type. Values needing more range — nanosecond timestamps, large
// identifiers, exact decimals — are string-typed by their schema.
//
// The check is on magnitude alone, so it cannot be evaded by notation.
// 9007199254740992, 9007199254740992.0 and 9.007199254740992e15 are one
// value written three ways and all three are rejected.
func CheckSafeNumbers(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return fmt.Errorf("decode for number check: %w", err)
	}
	return walk(decoded, "")
}

func walk(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if err := walk(child, path+"/"+key); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range typed {
			if err := walk(child, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	case json.Number:
		return checkNumber(typed, path)
	}
	return nil
}

// checkNumber applies the rule to the number's VALUE, never to how it was
// written.
//
// An earlier version classified on the literal's text — decimal point or
// exponent meant "float, therefore fine" — which made the rule bypassable
// by spelling: 9007199254740992.0 and 9.007199254740992e15 are the same
// value as 9007199254740992 and all three must be treated alike. Any check
// keyed on notation is one a payload author evades by adding ".0".
func checkNumber(number json.Number, path string) error {
	value, err := number.Float64()
	if err != nil {
		return fmt.Errorf("%w: %s is %s, which is not a representable number", ErrUnsafeNumber, displayPath(path), number)
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return fmt.Errorf("%w: %s is %s, which is not finite in binary64", ErrUnsafeNumber, displayPath(path), number)
	}
	if math.Abs(value) > SafeIntegerMax {
		return fmt.Errorf("%w: %s is %s, whose magnitude exceeds 2^53-1; encode it as a string in the payload's schema",
			ErrUnsafeNumber, displayPath(path), number)
	}
	return nil
}

func displayPath(path string) string {
	if path == "" {
		return "the payload root"
	}
	return path
}
