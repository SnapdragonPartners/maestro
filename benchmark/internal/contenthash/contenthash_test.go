package contenthash_test

import (
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
)

type sample struct {
	B string `json:"b"`
	A string `json:"a"`
	N int    `json:"n"`
}

func TestIdentityIndependentOfFieldOrder(t *testing.T) {
	fromStruct, err := contenthash.CanonicalJSON(sample{A: "x", B: "y", N: 3})
	if err != nil {
		t.Fatalf("hash struct: %v", err)
	}
	// The same semantic content expressed as a map (different key insertion
	// order, no struct declaration order) must produce the same identity.
	fromMap, err := contenthash.CanonicalJSON(map[string]any{"n": 3, "a": "x", "b": "y"})
	if err != nil {
		t.Fatalf("hash map: %v", err)
	}
	if fromStruct != fromMap {
		t.Fatalf("canonical identity differs: %q vs %q", fromStruct, fromMap)
	}
	if !strings.HasPrefix(fromStruct, contenthash.Prefix) {
		t.Fatalf("identity %q must carry the algorithm prefix", fromStruct)
	}
}

func TestIdentityTracksContent(t *testing.T) {
	a, err := contenthash.CanonicalJSON(sample{A: "x"})
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	b, err := contenthash.CanonicalJSON(sample{A: "y"})
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if a == b {
		t.Fatalf("different content must produce different identities")
	}
}
