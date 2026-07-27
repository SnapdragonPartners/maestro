package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
)

// rfc8785Input and rfc8785Canonical are the worked example from RFC 8785
// section 3.2.3. They are the only assertion here that is not
// self-referential: every other test compares this package against itself,
// so if the library were swapped for a plausible-looking approximation this
// is the test that would catch it.
const (
	rfc8785Input = `{
  "numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
  "string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
  "literals": [null, true, false]
}`
	rfc8785Canonical = "{\"literals\":[null,true,false],\"numbers\":[333333333.3333333,1e+30,4.5,0.002,1e-27]," +
		"\"string\":\"€$\\u000f\\nA'B\\\"\\\\\\\\\\\"/\"}"
)

func TestCanonicalFormMatchesRFC8785Vector(t *testing.T) {
	got, err := jcs.Transform([]byte(rfc8785Input))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if string(got) != rfc8785Canonical {
		t.Fatalf("canonical form does not match RFC 8785 section 3.2.3\n got: %s\nwant: %s", got, rfc8785Canonical)
	}
}

func TestDigestOfVectorIsSHA256OfCanonicalForm(t *testing.T) {
	got, err := DigestJSON([]byte(rfc8785Input))
	if err != nil {
		t.Fatalf("DigestJSON: %v", err)
	}
	sum := sha256.Sum256([]byte(rfc8785Canonical))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("digest = %s, want %s (SHA-256 of the RFC 8785 canonical form)", got, want)
	}
}

// TestDigestIsNotNaiveMarshal is the mutation guard. Go's encoding/json
// escapes the HTML-significant characters as \u003c, \u003e and \u0026;
// JCS emits them literally. A mutant Digest that skipped canonicalization
// and hashed json.Marshal output directly would pass every ordering test
// above but fail this one.
func TestDigestIsNotNaiveMarshal(t *testing.T) {
	payload := map[string]string{"note": "a<b>c&d"}

	got, err := Digest(payload)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}

	naive, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	naiveSum := sha256.Sum256(naive)
	if got == hex.EncodeToString(naiveSum[:]) {
		t.Fatal("digest equals SHA-256 of json.Marshal output, so canonicalization is not being applied")
	}

	// And it is specifically the unescaped form that is hashed.
	wantSum := sha256.Sum256([]byte(`{"note":"a<b>c&d"}`))
	if want := hex.EncodeToString(wantSum[:]); got != want {
		t.Fatalf("digest = %s, want %s (SHA-256 of the unescaped canonical form)", got, want)
	}
}

func TestDigestIgnoresKeyOrderAndWhitespace(t *testing.T) {
	first, err := DigestJSON([]byte(`{"b":2,"a":{"d":4,"c":3}}`))
	if err != nil {
		t.Fatalf("DigestJSON: %v", err)
	}
	second, err := DigestJSON([]byte("{\n  \"a\" : { \"c\" : 3, \"d\" : 4 },\n  \"b\" : 2\n}"))
	if err != nil {
		t.Fatalf("DigestJSON: %v", err)
	}
	if first != second {
		t.Fatalf("digest changed with key order and whitespace: %s vs %s", first, second)
	}
}

func TestDigestChangesWithValue(t *testing.T) {
	first, err := DigestJSON([]byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("DigestJSON: %v", err)
	}
	second, err := DigestJSON([]byte(`{"a":2}`))
	if err != nil {
		t.Fatalf("DigestJSON: %v", err)
	}
	if first == second {
		t.Fatal("digest did not change when a value changed")
	}
}

func TestUnsafeNumbersRejected(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string // substring the message must carry, so the schema fix is locatable
	}{
		{"2^53 exactly", `{"n":9007199254740992}`, "/n"},
		{"negative 2^53", `{"n":-9007199254740992}`, "/n"},
		{"beyond int64 as an integer literal", `{"n":1000000000000000000000000000000}`, "exceeds int64"},
		{"nested in an object", `{"outer":{"inner":9007199254740993}}`, "/outer/inner"},
		{"inside an array", `{"list":[1,2,9007199254740993]}`, "/list/2"},
		{"at the payload root", `9007199254740993`, "the payload root"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DigestJSON([]byte(testCase.raw))
			if err == nil {
				t.Fatal("expected rejection, got a digest")
			}
			if !errors.Is(err, ErrUnsafeNumber) {
				t.Fatalf("error is not ErrUnsafeNumber: %v", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("message %q does not locate the value (want substring %q)", err, testCase.want)
			}
		})
	}
}

func TestSafeNumbersAccepted(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"largest safe integer", `{"n":9007199254740991}`},
		{"smallest safe integer", `{"n":-9007199254740991}`},
		{"zero", `{"n":0}`},
		// Large magnitudes are fine when written as floats: the schema has
		// declared binary64, so no precision is being silently lost.
		{"large float", `{"n":1e30}`},
		{"tiny float", `{"n":1e-27}`},
		{"fractional", `{"n":4.5}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := DigestJSON([]byte(testCase.raw)); err != nil {
				t.Fatalf("rejected a safe number: %v", err)
			}
		})
	}
}

func TestDigestRejectsInvalidJSON(t *testing.T) {
	if _, err := DigestJSON([]byte(`{"a":`)); err == nil {
		t.Fatal("expected an error for truncated JSON")
	}
}
