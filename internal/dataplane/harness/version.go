// Package harness is the vocabulary for the version of the running Maestro
// binary as the data plane records and compares it (Phase 3 item 4 design,
// D3 and D8).
//
// It is a neutral vocabulary package of configkeys' class: it imports nothing
// of the plane, so the seam, its composers and the Orchestrator can all name
// a Version without any of them reaching another.
//
// # Why a type and not a string
//
// A prompt-pack installation declares the Maestro versions it supports, and
// the plane records which version validated it. Both are only as good as the
// version string they are compared with, and outside a release build that
// string is "dev". Earlier drafts of the design validated the string at one
// entry point, which the operator verbs never cross, and before that let
// every unparseable string fall into the development exception -- so a release
// stamped "2.0.0" without its "v" would have bypassed every declared range in
// the plane while reporting nothing wrong.
//
// So the version is opaque. Parse is the only constructor, it admits exactly
// two forms, and the zero value is invalid. plane.Open refuses a composition
// without one, so every root that opens a seam has crossed Parse, and nothing
// downstream re-decides what a version is.
package harness

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// Development is the exact sentinel pkg/version carries when no release build
// stamped it.
const Development = "dev"

var (
	// ErrMalformedVersion reports a version string that is neither admitted
	// form. It is configuration damage -- a mis-stamped build -- not a
	// development build, and must never be treated as one.
	ErrMalformedVersion = errors.New("maestro version is neither \"dev\" nor a semantic version with its leading v")

	// ErrMalformedRange reports a declared version range that is not two
	// semantic versions with the lower sorting strictly below the upper.
	ErrMalformedRange = errors.New("declared maestro version range is malformed")

	// ErrNotComparable reports an ordering asked of the development
	// sentinel. A development build has no position on the ladder, so the
	// caller must ask IsDev first and record that nothing was evaluated; an
	// answer here, either way, would assert a comparison that never ran.
	ErrNotComparable = errors.New("a development build has no version to compare")
)

// Version is a validated harness version. The zero value is invalid and is
// refused wherever a Version is required.
type Version struct {
	value string
}

// Parse admits exactly two forms: the sentinel "dev", and a full semantic
// version with its leading "v" -- what goreleaser stamps and what
// golang.org/x/mod/semver requires. Everything else is refused, including
// the shorthand "v2" and "v2.0" that semver.IsValid accepts: a version
// compared against a persisted range has to be one version, not a prefix.
func Parse(raw string) (Version, error) {
	if raw == Development {
		return Version{value: raw}, nil
	}
	if !isFullSemver(raw) {
		return Version{}, fmt.Errorf("%w: %q", ErrMalformedVersion, raw)
	}
	return Version{value: raw}, nil
}

// isFullSemver reports whether raw is vMAJOR.MINOR.PATCH with optional
// prerelease and build. Canonical fills in a shorthand's missing parts and
// drops build metadata, so a full version is one that Canonical changes only
// by removing its build suffix.
func isFullSemver(raw string) bool {
	return semver.IsValid(raw) && semver.Canonical(raw) == strings.TrimSuffix(raw, semver.Build(raw))
}

// String returns the version as it was stamped, which is the form persisted
// beside whatever it validated.
func (v Version) String() string { return v.value }

// IsZero reports the invalid zero value.
func (v Version) IsZero() bool { return v.value == "" }

// IsDev reports the development sentinel.
func (v Version) IsDev() bool { return v.value == Development }

// Compare orders two released versions by semantic-version precedence,
// prerelease identifiers included -- which the phase ladder depends on, since
// v2.0.0-phase.3.0.0 must sort below v2.0.0-phase.4.0.0. Build metadata does
// not participate, so two builds of one version compare equal here while
// remaining unequal as values; "has the harness moved" is ==, not Compare.
func Compare(a, b Version) (int, error) {
	if a.IsZero() || b.IsZero() {
		return 0, fmt.Errorf("%w: a version was never constructed", ErrMalformedVersion)
	}
	if a.IsDev() || b.IsDev() {
		return 0, ErrNotComparable
	}
	return semver.Compare(a.value, b.value), nil
}

// CheckRange is the write invariant on a declared range: both bounds are full
// semantic versions and lower sorts strictly below upper. It is a property of
// the declaration alone and needs no running version, so it holds on a
// development build too -- where skipping it would let a malformed range sit
// in every local plane until the first tagged build met it.
func CheckRange(lower, upper string) error {
	if !isFullSemver(lower) {
		return fmt.Errorf("%w: lower bound %q is not a semantic version with its leading v", ErrMalformedRange, lower)
	}
	if !isFullSemver(upper) {
		return fmt.Errorf("%w: upper bound %q is not a semantic version with its leading v", ErrMalformedRange, upper)
	}
	if semver.Compare(lower, upper) >= 0 {
		return fmt.Errorf("%w: lower bound %q does not sort below upper bound %q", ErrMalformedRange, lower, upper)
	}
	return nil
}

// InRange reports whether v falls in [lower, upper): lower inclusive, upper
// exclusive. The range is re-checked rather than trusted, since it arrives
// from a stored row. A development build is ErrNotComparable, never false
// and never true.
func (v Version) InRange(lower, upper string) (bool, error) {
	if v.IsZero() {
		return false, fmt.Errorf("%w: the version was never constructed", ErrMalformedVersion)
	}
	if err := CheckRange(lower, upper); err != nil {
		return false, err
	}
	if v.IsDev() {
		return false, ErrNotComparable
	}
	return semver.Compare(v.value, lower) >= 0 && semver.Compare(v.value, upper) < 0, nil
}
