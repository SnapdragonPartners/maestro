package store

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

func TestParseUSDAccepts(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0.00000000"},
		{"1.25", "1.25000000"},
		{"0.00000001", "0.00000001"},                   // the smallest representable cost
		{"9999999999.99999999", "9999999999.99999999"}, // 10 integer digits, 8 fractional: the exact bound
		{"  2.5  ", "2.50000000"},                      // surrounding space is not a value error
	}
	for _, testCase := range cases {
		t.Run(testCase.in, func(t *testing.T) {
			value, err := ParseUSD(testCase.in)
			if err != nil {
				t.Fatalf("ParseUSD(%q): %v", testCase.in, err)
			}
			if got := value.String(); got != testCase.want {
				t.Fatalf("String() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestParseUSDRejects(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", "absence is a nil"},
		{"negative", "-1.00", "negative"},
		{"not a number", "free", "not a decimal"},
		// Exponents would smuggle in precision the column does not keep.
		{"exponent", "1e-9", "exponent notation"},
		// numeric(18,8) bounds TOTAL precision: 10 integer digits, not 18.
		{"eleven integer digits", "10000000000.00", "integer digits"},
		{"nine fractional digits", "0.000000001", "fractional digits"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseUSD(testCase.in)
			if err == nil {
				t.Fatalf("ParseUSD(%q) was accepted", testCase.in)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error %q does not explain the problem (want %q)", err, testCase.want)
			}
		})
	}
}

// TestUSDRangeErrorsAreDistinguishable matters because a range failure is a
// schema-shaped problem — the value must be recorded differently — while a
// malformed string is a caller bug.
func TestUSDRangeErrorsAreDistinguishable(t *testing.T) {
	for _, in := range []string{"10000000000.00", "0.000000001"} {
		_, err := ParseUSD(in)
		if !errors.Is(err, ErrCostRange) {
			t.Fatalf("ParseUSD(%q) error is not ErrCostRange: %v", in, err)
		}
	}
	if _, err := ParseUSD("free"); errors.Is(err, ErrCostRange) {
		t.Fatal("a malformed string was reported as a range error")
	}
}

// TestUSDIsExactAtEightPlaces is the reason this type exists: the same
// arithmetic in float64 loses the eighth decimal place, and a per-call
// error accumulates across a campaign in the direction of whichever config
// made more calls.
func TestUSDIsExactAtEightPlaces(t *testing.T) {
	const perCall = "0.00000003"
	const calls = 1_000_000

	value := MustParseUSD(perCall)
	total := new(big.Rat).Mul(value.Rat(), new(big.Rat).SetInt64(calls))
	if got := total.FloatString(USDFractionalDigits); got != "0.03000000" {
		t.Fatalf("exact total = %s, want 0.03000000", got)
	}

	// The float64 path, for contrast: 3e-8 is not representable, so the
	// same sum drifts.
	var drifted float64
	for range calls {
		drifted += 3e-8
	}
	if drifted == 0.03 {
		t.Skip("float64 happened to be exact here; the contrast this test draws does not hold on this platform")
	}
}

// TestUSDZeroValueIsExactZero pins the distinction the cost-state table
// rests on: zero is a measurement, absence is a nil pointer.
func TestUSDZeroValueIsExactZero(t *testing.T) {
	var zero USD
	if !zero.IsZero() {
		t.Fatal("the zero value is not zero")
	}
	if got := zero.String(); got != "0.00000000" {
		t.Fatalf("zero renders as %q", got)
	}
	if got := zero.Rat().Sign(); got != 0 {
		t.Fatalf("zero Rat has sign %d", got)
	}
}

// TestUSDRatIsACopy guards the mutability hazard: big.Rat is a pointer type,
// so handing out the interior would let a caller change a cost already
// recorded somewhere else.
func TestUSDRatIsACopy(t *testing.T) {
	value := MustParseUSD("1.25")
	borrowed := value.Rat()
	borrowed.SetInt64(999)

	if got := value.String(); got != "1.25000000" {
		t.Fatalf("mutating the returned Rat changed the USD: %s", got)
	}
}
