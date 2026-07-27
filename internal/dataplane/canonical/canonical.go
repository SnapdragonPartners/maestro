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
	"strings"

	"github.com/gowebpki/jcs"
)

// SafeIntegerMax is the largest integer JCS can round-trip: JCS serializes
// numbers as IEEE-754 binary64, so anything beyond 2^53-1 loses precision.
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

func checkNumber(number json.Number, path string) error {
	// Classify on the literal's written form, not on whether Int64 happens
	// to succeed. An integer literal beyond int64 — 10^30 with no decimal
	// point — fails Int64 and parses fine as a float, so a fallthrough to
	// the float branch would accept exactly the value most certain to lose
	// precision. Integer intent is textual, so read it textually.
	if isIntegerLiteral(string(number)) {
		integer, err := number.Int64()
		if err != nil {
			return fmt.Errorf("%w: %s is %s, which exceeds int64 and so is far outside ±(2^53-1)", ErrUnsafeNumber, displayPath(path), number)
		}
		if integer > SafeIntegerMax || integer < -SafeIntegerMax {
			return fmt.Errorf("%w: %s is %s, outside ±(2^53-1)", ErrUnsafeNumber, displayPath(path), number)
		}
		return nil
	}

	// A fractional or exponential literal is already understood to be
	// binary64, so only non-finite results are rejected.
	value, err := number.Float64()
	if err != nil {
		return fmt.Errorf("%w: %s is %s, which is not representable", ErrUnsafeNumber, displayPath(path), number)
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return fmt.Errorf("%w: %s is %s, which is not finite in binary64", ErrUnsafeNumber, displayPath(path), number)
	}
	return nil
}

// isIntegerLiteral reports whether a JSON number was written as a plain
// integer — no decimal point and no exponent.
func isIntegerLiteral(text string) bool {
	return !strings.ContainsAny(text, ".eE")
}

func displayPath(path string) string {
	if path == "" {
		return "the payload root"
	}
	return path
}
