package claude

import (
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple semver", "2.1.42", "2.1.42"},
		{"with suffix", "2.1.42 (Claude Code)", "2.1.42"},
		{"with newline", "2.1.42\n", "2.1.42"},
		{"with tabs and suffix", " 2.1.42 (Claude Code)\n", "2.1.42"},
		{"two-part version", "2.1", "2.1"},
		{"four-part version", "1.2.3.4", "1.2.3.4"},
		{"empty string", "", ""},
		{"no dots", "foobar", ""},
		{"non-numeric", "a.b.c", ""},
		{"partial non-numeric", "2.1.beta", ""},
		{"just whitespace", "  \n  ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVersion(tt.input)
			if got != tt.expected {
				t.Errorf("parseVersion(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected int
	}{
		{"equal", "2.1.42", "2.1.42", 0},
		{"a less patch", "2.1.29", "2.1.42", -1},
		{"a greater patch", "2.1.42", "2.1.29", 1},
		{"a less minor", "2.0.42", "2.1.42", -1},
		{"a greater minor", "2.2.0", "2.1.99", 1},
		{"a less major", "1.9.99", "2.0.0", -1},
		{"a greater major", "3.0.0", "2.9.99", 1},
		{"different lengths a shorter", "2.1", "2.1.0", 0},
		{"different lengths b shorter", "2.1.0", "2.1", 0},
		{"different lengths a wins", "2.1.1", "2.1", 1},
		{"different lengths b wins", "2.1", "2.1.1", -1},
		{"zero values", "0.0.0", "0.0.0", 0},
		{"large numbers", "100.200.300", "100.200.299", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareVersions(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

func TestUpgradedInPlace_DefaultFalse(t *testing.T) {
	installer := &Installer{}
	if installer.UpgradedInPlace() {
		t.Error("UpgradedInPlace() should be false by default")
	}
}

func TestUpgradedInPlace_SetTrue(t *testing.T) {
	installer := &Installer{upgradedInPlace: true}
	if !installer.UpgradedInPlace() {
		t.Error("UpgradedInPlace() should be true when set")
	}
}
