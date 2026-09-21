package harness

import (
	"errors"
	"testing"
)

func mustParse(t *testing.T, raw string) Version {
	t.Helper()
	v, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return v
}

func TestParseAdmitsExactlyTwoForms(t *testing.T) {
	for _, raw := range []string{
		"dev",
		"v2.0.0",
		"v2.0.0-phase.3.0.0",
		"v2.0.0-rc.1",
		"v2.0.0-phase.3.0.0+build.7",
		"v0.0.0",
	} {
		v, err := Parse(raw)
		if err != nil {
			t.Errorf("Parse(%q) = %v, want admitted", raw, err)
			continue
		}
		if v.String() != raw || v.IsZero() {
			t.Errorf("Parse(%q) = %q, zero=%v; want the string as stamped", raw, v, v.IsZero())
		}
		if v.IsDev() != (raw == "dev") {
			t.Errorf("Parse(%q).IsDev() = %v", raw, v.IsDev())
		}
	}

	// Each of these is a mis-stamped build. None may construct, and above
	// all none may become the development exception.
	for _, raw := range []string{
		"",
		"2.0.0", // the release built without its v: design D8's named case
		"v2",    // semver.IsValid admits the shorthand; a persisted comparison must not
		"v2.0",
		"V2.0.0",
		"v2.0.0 ",
		" dev",
		"DEV",
		"dev-1",
		"devel",
		"v2.0.0-",
		"v2.0.0-phase..3",
		"v02.0.0",
		"latest",
	} {
		v, err := Parse(raw)
		if !errors.Is(err, ErrMalformedVersion) {
			t.Errorf("Parse(%q) error = %v, want ErrMalformedVersion", raw, err)
		}
		if !v.IsZero() || v.IsDev() {
			t.Errorf("Parse(%q) returned a usable version %q beside its error", raw, v)
		}
	}
}

func TestZeroValueIsInvalidEverywhere(t *testing.T) {
	var zero Version
	if !zero.IsZero() || zero.IsDev() {
		t.Fatalf("zero value: IsZero=%v IsDev=%v", zero.IsZero(), zero.IsDev())
	}
	if _, err := zero.InRange("v1.0.0", "v2.0.0"); !errors.Is(err, ErrMalformedVersion) {
		t.Errorf("zero.InRange error = %v, want ErrMalformedVersion", err)
	}
	if _, err := Compare(zero, mustParse(t, "v1.0.0")); !errors.Is(err, ErrMalformedVersion) {
		t.Errorf("Compare(zero, _) error = %v, want ErrMalformedVersion", err)
	}
}

// TestPhaseLadderOrdering is the in-tree assertion design D8 requires, so the
// claim that the comparator orders the repository's release ladder does not
// live in a review transcript. The ladder is CLAUDE.md's, extended with the
// cases that distinguish numeric from lexical prerelease ordering: phase.10
// must sort above phase.9, and 2.10.0 above 2.9.0.
func TestPhaseLadderOrdering(t *testing.T) {
	ladder := []string{
		"v2.0.0-alpha.1",
		"v2.0.0-phase.2.0.0",
		"v2.0.0-phase.2.1.0",
		"v2.0.0-phase.2.1.1",
		"v2.0.0-phase.2.9.0",
		"v2.0.0-phase.2.10.0",
		"v2.0.0-phase.3.0.0",
		"v2.0.0-phase.4.0.0",
		"v2.0.0-phase.9.0.0",
		"v2.0.0-phase.10.0.0",
		"v2.0.0-rc.1",
		"v2.0.0",
		"v2.0.1",
	}
	for i := range ladder {
		for j := range ladder {
			got, err := Compare(mustParse(t, ladder[i]), mustParse(t, ladder[j]))
			if err != nil {
				t.Fatalf("Compare(%s, %s): %v", ladder[i], ladder[j], err)
			}
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", ladder[i], ladder[j], got, want)
			}
		}
	}
}

func TestCompareIgnoresBuildMetadataAndEqualityDoesNot(t *testing.T) {
	a, b := mustParse(t, "v2.0.0+a"), mustParse(t, "v2.0.0+b")
	if got, err := Compare(a, b); err != nil || got != 0 {
		t.Errorf("Compare = %d, %v; want 0", got, err)
	}
	if a == b {
		t.Error("two builds of one version are equal as values, so a moved harness would read as unmoved")
	}
}

func TestDevelopmentIsNeverCompared(t *testing.T) {
	dev, released := mustParse(t, "dev"), mustParse(t, "v2.0.0")
	for _, pair := range [][2]Version{{dev, released}, {released, dev}, {dev, dev}} {
		if _, err := Compare(pair[0], pair[1]); !errors.Is(err, ErrNotComparable) {
			t.Errorf("Compare(%s, %s) error = %v, want ErrNotComparable", pair[0], pair[1], err)
		}
	}
	inRange, err := dev.InRange("v2.0.0-phase.3.0.0", "v2.0.0-phase.4.0.0")
	if !errors.Is(err, ErrNotComparable) || inRange {
		t.Errorf("dev.InRange = %v, %v; want false, ErrNotComparable", inRange, err)
	}
}

// The built-in's declared band, design D8.
const (
	phase3Lower = "v2.0.0-phase.3.0.0"
	phase3Upper = "v2.0.0-phase.4.0.0"
)

func TestInRange(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"v2.0.0-phase.2.1.1", false},
		{"v2.0.0-phase.3.0.0", true}, // lower is inclusive
		{"v2.0.0-phase.3.0.0+build.1", true},
		{"v2.0.0-phase.3.1.2", true},
		{"v2.0.0-phase.3.10.0", true},
		{"v2.0.0-phase.4.0.0", false}, // upper is exclusive
		{"v2.0.0-rc.1", false},
		{"v2.0.0", false},
		{"v1.9.9", false},
	}
	for _, tt := range tests {
		got, err := mustParse(t, tt.version).InRange(phase3Lower, phase3Upper)
		if err != nil {
			t.Errorf("%s: %v", tt.version, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s in [%s, %s) = %v, want %v", tt.version, phase3Lower, phase3Upper, got, tt.want)
		}
	}
}

func TestCheckRange(t *testing.T) {
	if err := CheckRange(phase3Lower, phase3Upper); err != nil {
		t.Fatalf("the built-in's band: %v", err)
	}
	for name, bounds := range map[string][2]string{
		"inverted":               {phase3Upper, phase3Lower},
		"empty":                  {phase3Lower, phase3Lower},
		"equal but for build":    {"v2.0.0+a", "v2.0.0+b"},
		"lower without its v":    {"2.0.0", "v3.0.0"},
		"upper shorthand":        {"v2.0.0", "v3"},
		"dev as a bound":         {"dev", "v3.0.0"},
		"blank upper":            {"v2.0.0", ""},
		"prerelease above final": {"v2.0.0", "v2.0.0-rc.1"},
	} {
		if err := CheckRange(bounds[0], bounds[1]); !errors.Is(err, ErrMalformedRange) {
			t.Errorf("%s: CheckRange(%q, %q) = %v, want ErrMalformedRange", name, bounds[0], bounds[1], err)
		}
	}

	// The invariant belongs to the declaration, so a development build
	// reports a malformed range as malformed and not as "not comparable":
	// the dev skip must not reach the write check (design D6).
	dev := mustParse(t, "dev")
	if _, err := dev.InRange(phase3Upper, phase3Lower); !errors.Is(err, ErrMalformedRange) {
		t.Errorf("dev.InRange over an inverted range = %v, want ErrMalformedRange", err)
	}
	released := mustParse(t, "v2.0.0-phase.3.0.0")
	if _, err := released.InRange(phase3Upper, phase3Lower); !errors.Is(err, ErrMalformedRange) {
		t.Errorf("InRange over an inverted range = %v, want ErrMalformedRange", err)
	}
}
