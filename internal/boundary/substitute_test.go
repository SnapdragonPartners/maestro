package boundary_test

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/boundary"
	"orchestrator/internal/dataplane/canonical"
)

// reference is the one secret reference the tests substitute.
func reference(t *testing.T) boundary.SecretReference {
	t.Helper()
	return boundary.SecretReference{SecretID: uuid.MustParse("0193b4f0-0000-7000-8000-000000000001"), Version: 3}
}

// TestSubstituteProducesEveryFormFromOnePass is design D6 at the unit: the
// request digest is over the caller's fields alone; the arguments digest
// is over the substituted form and equals one the test computes from the
// reference; the projection holds persist fields and the reference and
// nothing digest-only; the readable view is the projection's fields.
//
// THE MUTANTS: digest the raw arguments for ArgumentsDigest and the
// equality with the reference-form digest fails; put the digest-only field
// in the projection and "hint" is read back out of it; leave the secret
// slot out of the substituted form and the request and arguments digests
// come out equal.
func TestSubstituteProducesEveryFormFromOnePass(t *testing.T) {
	schema := noopFamily().Schema
	ref := reference(t)
	arguments := map[string]any{"note": "hello", "hint": "not recorded", "count": float64(2)}

	got, err := boundary.Substitute(schema, arguments, map[string]boundary.SecretReference{"token": ref})
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}

	// The request digest is what the caller supplied, no secret in it.
	wantRequest, err := canonical.Digest(map[string]any{"note": "hello", "hint": "not recorded", "count": 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestDigest != wantRequest {
		t.Fatalf("RequestDigest = %s, want the digest of the caller's fields %s", got.RequestDigest, wantRequest)
	}
	// The arguments digest is over the substituted form -- the reference
	// text, never the plaintext -- so a test can compute it from the
	// reference alone.
	wantArguments, err := canonical.Digest(map[string]any{
		"note": "hello", "hint": "not recorded", "count": 2, "token": "secret:0193b4f0-0000-7000-8000-000000000001@3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ArgumentsDigest != wantArguments {
		t.Fatalf("ArgumentsDigest = %s, want the digest over the substituted form %s", got.ArgumentsDigest, wantArguments)
	}
	if got.RequestDigest == got.ArgumentsDigest {
		t.Fatal("the request and arguments digests are equal, so the secret reference is not in the substituted form")
	}

	var projection map[string]any
	if err := json.Unmarshal(got.Projection, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["note"] != "hello" || projection["count"] != float64(2) || projection["token"] != ref.String() {
		t.Fatalf("projection %s lacks a persist field or the reference", got.Projection)
	}
	if _, present := projection["hint"]; present {
		t.Fatalf("a digest-only field was persisted: %s", got.Projection)
	}
	if strings.Contains(string(got.Projection), "not recorded") {
		t.Fatalf("digest-only text is in the projection: %s", got.Projection)
	}
	if _, readable := got.Readable["hint"]; readable || got.Readable["note"] != "hello" || got.Readable["token"] != ref.String() {
		t.Fatalf("Readable = %v; want the projection's fields and the reference, not the digest-only one", got.Readable)
	}
	if got.Request["token"] != nil || got.Arguments["token"] != ref.String() {
		t.Fatalf("Request %v / Arguments %v: the slot belongs in the substituted form only", got.Request, got.Arguments)
	}
}

// TestSubstituteRefusesWhatTheSchemaDoesNotOffer is admission's argument
// half (D6, D13): an unknown field, a supplied secret, a missing required
// field, a wrong type, and -- a boundary defect rather than a caller's --
// a slot no reference was resolved for.
func TestSubstituteRefusesWhatTheSchemaDoesNotOffer(t *testing.T) {
	schema := noopFamily().Schema
	refs := map[string]boundary.SecretReference{"token": reference(t)}
	for name, tc := range map[string]struct {
		arguments map[string]any
		refs      map[string]boundary.SecretReference
		want      error
	}{
		"unknown field":         {map[string]any{"note": "x", "head": "main"}, refs, boundary.ErrUnknownField},
		"secret supplied":       {map[string]any{"note": "x", "token": "ghp_x"}, refs, boundary.ErrSecretSupplied},
		"required missing":      {map[string]any{"hint": "x"}, refs, boundary.ErrMissingField},
		"string as number":      {map[string]any{"note": 7}, refs, boundary.ErrWrongType},
		"integer with fraction": {map[string]any{"note": "x", "count": 1.5}, refs, boundary.ErrWrongType},
		"unresolved slot":       {map[string]any{"note": "x"}, nil, boundary.ErrUnresolvedSlot},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := boundary.Substitute(schema, tc.arguments, tc.refs)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// An unknown field is refused BEFORE a missing one: the model reads the
	// refusal, and reaching for a field the schema does not offer is the
	// thing to say first.
	_, err := boundary.Substitute(schema, map[string]any{"story_id": "x"}, refs)
	if !errors.Is(err, boundary.ErrUnknownField) {
		t.Fatalf("err = %v, want the unknown field named before the missing one", err)
	}
}

// TestSubstituteReducesALargeFieldOverTheLimit: at the limit the value is
// inline; one byte over, the projection holds its digest and length and
// not the text, while the digests still cover the whole value.
func TestSubstituteReducesALargeFieldOverTheLimit(t *testing.T) {
	schema := noopFamily().Schema
	refs := map[string]boundary.SecretReference{"token": reference(t)}
	atLimit := strings.Repeat("a", boundary.LargeInlineLimit)
	over := atLimit + "b"

	inline, err := boundary.Substitute(schema, map[string]any{"note": "x", "body": atLimit}, refs)
	if err != nil {
		t.Fatal(err)
	}
	var projection map[string]any
	if decodeErr := json.Unmarshal(inline.Projection, &projection); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if projection["body"] != atLimit {
		t.Fatalf("a value at the limit was not persisted inline")
	}

	reduced, err := boundary.Substitute(schema, map[string]any{"note": "x", "body": over}, refs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reduced.Projection), "aaaa") {
		t.Fatalf("the large value is in the projection: %d bytes", len(reduced.Projection))
	}
	if decodeErr := json.Unmarshal(reduced.Projection, &projection); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	body, ok := projection["body"].(map[string]any)
	if !ok || body["bytes"] != float64(len(over)) {
		t.Fatalf("projection body = %v; want {digest, bytes}", projection["body"])
	}
	if digest, isString := body["digest"].(string); !isString || len(digest) != 64 {
		t.Fatalf("projection body digest = %v; want 64 hex", body["digest"])
	}
	if inline.ArgumentsDigest == reduced.ArgumentsDigest {
		t.Fatal("two different bodies digested the same: the digest is over the projection, not the value")
	}
	if reduced.Readable["body"] != over {
		t.Fatal("the hook reads the projection's reduction rather than the value")
	}
}

// TestSecretReferenceRoundTrips: the textual form parses back to itself and
// malformed forms are refused.
func TestSecretReferenceRoundTrips(t *testing.T) {
	ref := reference(t)
	text := ref.String()
	if text != "secret:0193b4f0-0000-7000-8000-000000000001@3" {
		t.Fatalf("String() = %q", text)
	}
	parsed, err := boundary.ParseSecretReference(text)
	if err != nil || parsed != ref {
		t.Fatalf("ParseSecretReference(%q) = %+v, %v", text, parsed, err)
	}
	for name, bad := range map[string]string{
		"no prefix":    "0193b4f0-0000-7000-8000-000000000001@3",
		"no version":   "secret:0193b4f0-0000-7000-8000-000000000001",
		"zero version": "secret:0193b4f0-0000-7000-8000-000000000001@0",
		"not a uuid":   "secret:forge.token@3",
		"by name":      "secret:forge.token",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := boundary.ParseSecretReference(bad); !errors.Is(err, boundary.ErrInvalidSecretReference) {
				t.Fatalf("%q was accepted: %v", bad, err)
			}
		})
	}
}

// TestSubstituteValidatesANumberOnItsLiteral is PR review round 1's first
// P1: a json.Number is checked as the caller wrote it, before conversion.
// Converting first let 9007199254740991.1 round to an integer that passed
// the integer check, and 1e-400 collapse to 0 that passed the safe-range
// check, so the recorded request was a value the caller never sent (ADR
// 0028's encoding constraint, canonical.CheckSafeNumbers).
//
// THE MUTANT: convert with n.Float64() before checking -- both literals are
// then accepted, which the first two cases read.
func TestSubstituteValidatesANumberOnItsLiteral(t *testing.T) {
	schema := noopFamily().Schema
	refs := map[string]boundary.SecretReference{"token": reference(t)}
	for name, tc := range map[string]struct {
		field string
		value any
	}{
		"integer literal with a fraction that rounds away": {"count", json.Number("9007199254740991.1")},
		"number literal that underflows to zero":           {"ratio", json.Number("1e-400")},
		"number literal past the safe range":               {"ratio", json.Number("9007199254740992")},
		"integer literal with a fraction":                  {"count", json.Number("1.5")},
		"float past the safe range":                        {"ratio", 1e30},
		"float NaN":                                        {"ratio", math.NaN()},
		"not a number at all":                              {"ratio", "0.5"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := boundary.Substitute(schema, map[string]any{"note": "x", tc.field: tc.value}, refs)
			if !errors.Is(err, boundary.ErrWrongType) {
				t.Fatalf("%v was accepted for %s: %v", tc.value, tc.field, err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("the refusal does not name the field: %v", err)
			}
		})
	}
	// Positive controls, and the equivalence the conversion relies on: a
	// safe literal and the float it decodes to digest identically.
	fromLiteral, err := boundary.Substitute(schema, map[string]any{"note": "x", "count": json.Number("2"), "ratio": json.Number("0.25")}, refs)
	if err != nil {
		t.Fatalf("safe literals were refused: %v", err)
	}
	fromFloat, err := boundary.Substitute(schema, map[string]any{"note": "x", "count": float64(2), "ratio": 0.25}, refs)
	if err != nil {
		t.Fatalf("safe floats were refused: %v", err)
	}
	if fromLiteral.ArgumentsDigest != fromFloat.ArgumentsDigest {
		t.Fatal("a literal and its decoded float digested differently")
	}
}
