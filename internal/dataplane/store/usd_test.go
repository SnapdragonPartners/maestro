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

// TestUSDTotalAcceptsWhatARowCannot is the reason USDTotal is a distinct
// type. numeric(18,8) bounds a stored row at ten integer digits, but
// SUM(cost_usd) is unconstrained: a campaign total may legitimately exceed
// what any single call could cost. Applying the row bound to a total would
// reject a correct sum for being large.
func TestUSDTotalAcceptsWhatARowCannot(t *testing.T) {
	const big = "12345678901234.56789012" // 14 integer digits

	if _, err := ParseUSD(big); !errors.Is(err, ErrCostRange) {
		t.Fatalf("a row value of %s should be out of range: %v", big, err)
	}
	total, err := ParseUSDTotal(big)
	if err != nil {
		t.Fatalf("the same value as a TOTAL was rejected: %v", err)
	}
	if got := total.String(); got != big {
		t.Fatalf("total = %q, want %q", got, big)
	}
}

// TestUSDTotalStillBoundsScaleAndShape: unbounded in magnitude is not
// unbounded in everything.
func TestUSDTotalStillBoundsScaleAndShape(t *testing.T) {
	for _, in := range []string{"0.000000001", "-1", "1e30", "lots"} {
		if _, err := ParseUSDTotal(in); err == nil {
			t.Errorf("ParseUSDTotal(%q) was accepted", in)
		}
	}
}

// TestOversizedLiteralIsRejectedBeforeParsing guards the allocation path.
// big.Rat.SetString on a caller-supplied literal allocates proportionally
// to its size, and no later range check can undo work already done.
func TestOversizedLiteralIsRejectedBeforeParsing(t *testing.T) {
	hostile := strings.Repeat("9", 200_000)

	for name, parse := range map[string]func(string) error{
		"row":   func(s string) error { _, err := ParseUSD(s); return err },
		"total": func(s string) error { _, err := ParseUSDTotal(s); return err },
	} {
		err := parse(hostile)
		if !errors.Is(err, ErrCostRange) {
			t.Errorf("%s: a %d-character literal was not rejected on length: %v", name, len(hostile), err)
		}
		if !strings.Contains(err.Error(), "character limit") {
			t.Errorf("%s: rejected for the wrong reason, so the length guard may not be what fired: %v", name, err)
		}
	}
}

// TestIntegerDigitsIgnoresLeadingZeros: "007.5" is one integer digit, not
// three, or a zero-padded value would fail the bound for its padding.
func TestIntegerDigitsIgnoresLeadingZeros(t *testing.T) {
	if _, err := ParseUSD("0000000000000007.50000000"); err != nil {
		t.Fatalf("a zero-padded value was rejected for its padding: %v", err)
	}
}
