package store

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// USD is an exact cost in United States dollars, matching the schema's
// numeric(18,8).
//
// It exists because float64 cannot represent 8 decimal places exactly, and a
// cost that rounds is a cost that does not reconcile. Phase 1B's whole
// premise is comparing campaign costs; a per-call rounding error that
// accumulates over thousands of calls corrupts the comparison quietly, in
// the direction of whichever config made more calls.
//
// It is validated at construction rather than being a castable string
// alias. An alias documents an intention; this enforces one — there is no
// way to hold a USD that the column would reject.
//
// The zero value is a valid zero cost. Absence is represented by *USD being
// nil, never by the zero value, because "free" and "not knowable" are
// different facts (see the cost-state table in the item 5 design).
type USD struct {
	// amount is nil only in the zero value, which reads as exact zero.
	amount *big.Rat
}

// USDFractionalDigits is the scale of the numeric(18,8) column.
const USDFractionalDigits = 8

// USDIntegerDigits is what remains of numeric(18,8)'s precision once the
// scale is taken: 18 total minus 8 fractional.
//
// Stated as its own constant because the bound that actually bites is the
// integer part, and reading it off "18,8" is a subtraction people skip. An
// earlier draft of the design mentioned only the fractional bound, which
// would have let a value pass the seam and fail the column.
const USDIntegerDigits = 10

var (
	// plainDecimal is the only accepted form: digits, optionally a point
	// and more digits. No sign, no exponent, no separators.
	plainDecimal = regexp.MustCompile(`^\d+(\.\d+)?$`)
	// exponentDecimal recognises a number written with an exponent, purely
	// so that case gets its own diagnosis rather than "not a number".
	exponentDecimal = regexp.MustCompile(`^\d+(\.\d+)?[eE][+-]?\d+$`)
)

// maxCostLiteralLength bounds the input BEFORE exact parsing.
//
// big.Rat.SetString on a caller-supplied literal is unbounded work: a
// million-digit integer allocates proportionally before any range check
// could reject it. A legal row value needs 19 characters and a legal total
// far fewer than this, so nothing real is excluded.
const maxCostLiteralLength = 128

// ErrCostRange reports a cost outside the range its destination can hold.
var ErrCostRange = errors.New("cost is outside the range its column can store")

// ParseUSD builds a USD from a decimal string.
//
// It accepts only plain decimal notation. Exponents are refused: costs are
// written by humans and by provider APIs in plain decimal, and accepting
// 1e-9 would mean silently accepting a value with more precision than the
// column keeps.
func ParseUSD(text string) (USD, error) {
	amount, err := parseDecimal(text, USDIntegerDigits)
	if err != nil {
		return USD{}, err
	}
	return USD{amount: amount}, nil
}

// USDTotal is an aggregate of costs — a SUM, not a row value.
//
// It is a DISTINCT type because it has a different range. numeric(18,8)
// bounds a stored row at ten integer digits, but SUM(cost_usd) returns
// unconstrained numeric and a campaign total may legitimately exceed that.
// Applying the row bound to a total would reject a correct sum for being
// large, which is precisely what a total is for; sharing one type would
// mean one of the two bounds is always wrong.
type USDTotal struct {
	amount *big.Rat
}

// ParseUSDTotal builds an aggregate total. Same scale as a row value, no
// integer bound beyond the literal-length cap that keeps parsing finite.
func ParseUSDTotal(text string) (USDTotal, error) {
	amount, err := parseDecimal(text, 0) // 0 = no integer-digit bound
	if err != nil {
		return USDTotal{}, err
	}
	return USDTotal{amount: amount}, nil
}

// String renders the total at the column's scale.
func (u USDTotal) String() string {
	if u.amount == nil {
		return zeroUSDText()
	}
	return u.amount.FloatString(USDFractionalDigits)
}

// IsZero reports an exact zero total.
func (u USDTotal) IsZero() bool { return u.amount == nil || u.amount.Sign() == 0 }

// Rat returns a copy, for the reason USD.Rat does.
func (u USDTotal) Rat() *big.Rat {
	if u.amount == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(u.amount)
}

// parseDecimal is the shared validator. maxIntegerDigits of 0 means
// unbounded, which is the aggregate case.
func parseDecimal(text string, maxIntegerDigits int) (*big.Rat, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("cost is empty; absence is a nil pointer, not an empty string")
	}
	// Length BEFORE exact parsing: big.Rat.SetString on a hostile literal
	// allocates proportionally to its size, and no later range check can
	// undo work already done.
	if len(trimmed) > maxCostLiteralLength {
		return nil, fmt.Errorf("%w: a %d-character cost literal exceeds the %d-character limit",
			ErrCostRange, len(trimmed), maxCostLiteralLength)
	}
	if strings.HasPrefix(trimmed, "-") {
		return nil, fmt.Errorf("cost %q is negative", text)
	}
	// Shape first, so the diagnosis matches the actual problem. Testing for
	// "eE" before checking the shape reported "free" as exponent notation,
	// which is confidently wrong rather than merely unhelpful.
	if !plainDecimal.MatchString(trimmed) {
		if exponentDecimal.MatchString(trimmed) {
			return nil, fmt.Errorf("cost %q uses exponent notation; write it in plain decimal so the "+
				"stored precision is visible in the value itself", text)
		}
		return nil, fmt.Errorf("cost %q is not a decimal number", text)
	}
	if fractionalDigits(trimmed) > USDFractionalDigits {
		return nil, fmt.Errorf("%w: %q has more than %d fractional digits, which would be rounded away",
			ErrCostRange, text, USDFractionalDigits)
	}
	// Integer digits are counted from the TEXT, before parsing, so an
	// oversized value is rejected without being built.
	if maxIntegerDigits > 0 && integerDigits(trimmed) > maxIntegerDigits {
		return nil, fmt.Errorf("%w: %q has more than %d integer digits", ErrCostRange, text, maxIntegerDigits)
	}

	amount, ok := new(big.Rat).SetString(trimmed)
	if !ok {
		return nil, fmt.Errorf("cost %q is not a decimal number", text)
	}
	return amount, nil
}

// integerDigits counts the digits before the decimal point, ignoring
// leading zeros so "007.5" is one integer digit rather than three.
func integerDigits(text string) int {
	integer, _, _ := strings.Cut(text, ".")
	trimmed := strings.TrimLeft(integer, "0")
	if trimmed == "" {
		return 1
	}
	return len(trimmed)
}

// MustParseUSD is ParseUSD for literals in tests and fixed constants.
func MustParseUSD(text string) USD {
	value, err := ParseUSD(text)
	if err != nil {
		panic(err)
	}
	return value
}

// String renders the cost in plain decimal at the column's scale, so a
// value written and read back compares equal as text.
func (u USD) String() string {
	if u.amount == nil {
		return zeroUSDText()
	}
	return u.amount.FloatString(USDFractionalDigits)
}

// IsZero reports an exact zero cost, which is a real measurement — a local
// model that costs nothing — and never a stand-in for "unknown".
func (u USD) IsZero() bool {
	return u.amount == nil || u.amount.Sign() == 0
}

// Rat returns a copy of the underlying exact value.
//
// A copy because big.Rat is mutable, and handing out the interior would let
// a caller change a cost already recorded elsewhere.
func (u USD) Rat() *big.Rat {
	if u.amount == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(u.amount)
}

// fractionalDigits counts the digits after the decimal point.
func fractionalDigits(text string) int {
	_, fraction, found := strings.Cut(text, ".")
	if !found {
		return 0
	}
	return len(fraction)
}

func zeroUSDText() string {
	return "0." + strings.Repeat("0", USDFractionalDigits)
}
