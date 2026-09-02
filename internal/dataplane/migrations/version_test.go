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
