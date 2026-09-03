package migrations

import (
	"io/fs"
	"regexp"
	"strconv"
	"testing"
)

// TestEmbeddedIsTheHighestUpMigration recomputes the answer independently
// from the same embedded files, with a different parse, so the two can only
// agree by both being right.
func TestEmbeddedIsTheHighestUpMigration(t *testing.T) {
	got, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	prefix := regexp.MustCompile(`^(\d{6})_`)
	var want uint
	ups, downs := 0, 0
	for _, e := range entries {
		m := prefix.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, _ := strconv.Atoi(m[1])
		switch {
		case len(e.Name()) > 7 && e.Name()[len(e.Name())-7:] == ".up.sql":
			ups++
			if uint(v) > want {
				want = uint(v)
			}
		case len(e.Name()) > 9 && e.Name()[len(e.Name())-9:] == ".down.sql":
			downs++
		}
	}
	if got != want {
		t.Fatalf("Embedded = %d, independent recount = %d", got, want)
	}
	if ups == 0 || ups != downs {
		t.Fatalf("%d up and %d down migrations; every version needs both", ups, downs)
	}
	if got < 22 {
		t.Fatalf("Embedded = %d, but item 2 landed migration 000022", got)
	}
}

// TestParseMigrationVersionIsBounded is the CodeQL finding PR #346's scan
// raised: an unbounded parse feeding a uint conversion truncates on a 32-bit
// platform, so a binary would believe its own schema were older than it is.
//
// It drives the helper rather than strconv, so it is a claim about this
// package: THE MUTANT is widening the bound back to 64, which makes the
// oversized case parse successfully and this test fail on any platform --
// including the 64-bit ones where the truncation itself cannot be observed.
func TestParseMigrationVersionIsBounded(t *testing.T) {
	const beyond32Bits = "4294967296" // MaxUint32 + 1
	if _, err := parseMigrationVersion(beyond32Bits); err == nil {
		t.Errorf("%s parsed; a version beyond a 32-bit uint would truncate on a 32-bit platform", beyond32Bits)
	}
	if _, err := parseMigrationVersion("4294967295"); err != nil {
		t.Errorf("MaxUint32 was refused: %v", err)
	}
	got, err := parseMigrationVersion("000022")
	if err != nil || got != 22 {
		t.Errorf("parseMigrationVersion(\"000022\") = %d, %v; want 22", got, err)
	}
}
