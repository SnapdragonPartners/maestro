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
	"math/big"
	"strings"

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

// maxNumberLiteralLength caps how much text one JSON number may occupy.
// It bounds the cost of the exact-decimal comparison, which is otherwise
// unbounded work on caller-supplied input.
const maxNumberLiteralLength = 128

// safeIntegerMaxRat returns the bound as an exact rational, so the
// comparison never routes through a float.
//
// A function rather than a package-level value because big.Rat is mutable:
// a shared instance is one Abs or Neg away from silently redefining the
// bound for every later caller.
func safeIntegerMaxRat() *big.Rat { return new(big.Rat).SetInt64(SafeIntegerMax) }

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
// The check is on the EXACT decimal magnitude, so it can be evaded neither
// by notation nor by rounding. 9007199254740992, 9007199254740992.0 and
// 9.007199254740992e15 are one value written three ways and all three are
// rejected; so is 9007199254740991.1, which a float comparison would round
// down onto the limit and admit.
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
	text := string(number)

	// Bound the parse before doing it. Exact decimal arithmetic on an
	// attacker-supplied literal is unbounded work otherwise, and no
	// legitimate payload number needs this many characters -- binary64
	// carries about 17 significant digits, so a longer literal has already
	// lost whatever the extra digits were for.
	if len(text) > maxNumberLiteralLength {
		return fmt.Errorf("%w: %s has a %d-character numeric literal, beyond the %d-character limit",
			ErrUnsafeNumber, displayPath(path), len(text), maxNumberLiteralLength)
	}

	// ParseFloat reports ErrRange for overflow to ±Inf. It does NOT report
	// underflow: 1e-400 and even 1e-999999999 come back as 0 with a nil
	// error, which was checked rather than assumed after an earlier version
	// of this comment claimed otherwise.
	value, err := number.Float64()
	if err != nil {
		return fmt.Errorf("%w: %s is %s, which does not survive conversion to binary64: %w",
			ErrUnsafeNumber, displayPath(path), number, err)
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return fmt.Errorf("%w: %s is %s, which is not finite in binary64", ErrUnsafeNumber, displayPath(path), number)
	}

	// Underflow, detected explicitly because ParseFloat will not.
	//
	// This is load-bearing twice over. It rejects values that do not
	// survive binary64 -- being inside ±(2^53-1) is necessary, not
	// sufficient -- and it is the guard that keeps a hostile exponent away
	// from the exact comparison below. "1e-999999999" is twelve characters,
	// so the length cap admits it, and big.Rat would faithfully build a
	// denominator of 10^999999999 to represent it exactly.
	if value == 0 && !isZeroLiteral(text) {
		return fmt.Errorf("%w: %s is %s, which underflows to zero in binary64",
			ErrUnsafeNumber, displayPath(path), number)
	}

	// Compare the EXACT decimal value, not the parsed float.
	//
	// Rounding before comparing hides the values nearest the boundary,
	// which are exactly the ones the bound exists to catch:
	// 9007199254740991.1 parses to 9007199254740991, lands precisely ON the
	// limit, and passes a check written against the float -- despite an
	// exact magnitude that exceeds it. big.Rat parses decimal literals
	// without loss, so the comparison is against what the payload actually
	// says.
	exact, ok := new(big.Rat).SetString(text)
	if !ok {
		return fmt.Errorf("%w: %s is %s, which is not a representable number", ErrUnsafeNumber, displayPath(path), number)
	}
	if new(big.Rat).Abs(exact).Cmp(safeIntegerMaxRat()) > 0 {
		return fmt.Errorf("%w: %s is %s, whose exact magnitude exceeds 2^53-1; encode it as a string in the payload's schema",
			ErrUnsafeNumber, displayPath(path), number)
	}
	return nil
}

// isZeroLiteral reports whether a JSON number literal denotes zero, which
// is the one value legitimately parsing to 0. It inspects the significand's
// digits rather than the parsed value, since the parsed value is what
// cannot distinguish a real zero from an underflowed one.
func isZeroLiteral(text string) bool {
	significand, _, _ := strings.Cut(strings.ToLower(text), "e")
	for _, character := range significand {
		if character >= '1' && character <= '9' {
			return false
		}
	}
	return true
}

func displayPath(path string) string {
	if path == "" {
		return "the payload root"
	}
	return path
}
