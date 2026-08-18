package objects

import (
	"context"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
)

// These tests reach no network. They cover the adapter's structural
// guarantees — that nothing here acts on an object the caller did not name by
// generation, and that it cannot answer a question it is blind to — all of
// which must hold before a request is ever built.
//
// What the provider actually DOES is measured in gcs_integration_test.go
// against a real bucket, because a mock would only replay the belief under
// test.

func TestNewGCSRequiresBucket(t *testing.T) {
	// Reaches no network despite taking a context: the bucket check precedes
	// client construction, which is the only part that resolves credentials.
	// A test that needed ADC would fail on a machine that has never authed.
	if _, err := NewGCS(context.Background(), GCSConfig{}); err == nil {
		t.Fatal("expected a rejection for an empty bucket, got a client")
	}
}

// TestParseGenerationRejectsUnusable covers the values that cannot name one
// immutable object. Each would otherwise be silently promoted into a request
// that addresses whatever is live.
func TestParseGenerationRejectsUnusable(t *testing.T) {
	for name, versionID := range map[string]string{
		"empty":        "",
		"not a number": "latest",
		"zero":         "0",
		"negative":     "-1",
		// -1 is the pinned client's own "unspecified" sentinel, so it is the
		// value most likely to arrive from a caller that read a zero-valued
		// handle and stringified it.
		"float":      "1786920012739517.0",
		"whitespace": " 1786920012739517",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGeneration("probe/a.txt", versionID); err == nil {
				t.Fatalf("expected %q to be rejected, it was accepted", versionID)
			}
		})
	}
}

// TestParseGenerationAcceptsAGeneration is the control. Without it the
// rejection table above would pass just as happily against a function that
// refused everything, which is the failure mode a table of negatives invites.
func TestParseGenerationAcceptsAGeneration(t *testing.T) {
	const observed = "1786920012739517" // a real generation, from the measurement bucket
	got, err := parseGeneration("probe/a.txt", observed)
	if err != nil {
		t.Fatalf("a real generation was rejected: %v", err)
	}
	if got != 1786920012739517 {
		t.Fatalf("generation round-tripped to %d, want 1786920012739517", got)
	}
}

// TestFencedGenerationRejectsUnfenceable guards the write path's side of the
// same property: a stored object whose generation cannot name it later is one
// no delete can ever fence, so the write is reported as failed rather than
// returned with an id that will not work.
func TestFencedGenerationRejectsUnfenceable(t *testing.T) {
	for name, generation := range map[string]int64{
		"zero":        0,
		"unspecified": -1,
		"negative":    -1786920012739517,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fencedGeneration("probe/a.txt", generation)
			if err == nil {
				t.Fatalf("expected generation %d to be refused, it was accepted", generation)
			}
			if !errors.Is(err, errUnusableGeneration) {
				t.Fatalf("refusal did not wrap errUnusableGeneration, so a caller must match on "+
					"message text: %v", err)
			}
		})
	}
}

func TestFencedGenerationAcceptsAGeneration(t *testing.T) {
	got, err := fencedGeneration("probe/a.txt", 1786920012739517)
	if err != nil {
		t.Fatalf("a real generation was refused: %v", err)
	}
	if got != "1786920012739517" {
		t.Fatalf("formatted generation is %q, want \"1786920012739517\"", got)
	}
}

// TestGCSDeclaresProviderReclaimed pins the capability the whole seam split
// was introduced for. If this ever flips to Enumerable the adapter starts
// claiming it can list interrupted writes, and the postgres store's
// construction check will then demand a reclaimer it does not have.
func TestGCSDeclaresProviderReclaimed(t *testing.T) {
	var adapter Store = &GCS{}
	if got := adapter.IncompleteWrites(); got != IncompleteWritesProviderReclaimed {
		t.Fatalf("GCS declares incomplete writes %q, want %q — GCS resumable uploads are not "+
			"enumerable by any API, so claiming otherwise makes the sweep report a count nothing took",
			got, IncompleteWritesProviderReclaimed)
	}
}

// TestGCSIsNotAReclaimer asserts a NEGATIVE the compiler cannot: that this
// adapter does not satisfy IncompleteWriteReclaimer.
//
// It matters because the omission is load-bearing rather than incidental. The
// three reclamation methods look like an obvious gap next to the MinIO
// adapter, and filling them in — returning an empty slice, since there is
// nothing to list — is the exact mistake the capability exists to prevent:
// the sweep would record that it looked and found nothing, which is a claim
// about the bucket when the truth is that this adapter is blind. Anyone who
// adds them will trip this test and read that reasoning.
func TestGCSIsNotAReclaimer(t *testing.T) {
	var adapter Store = &GCS{}
	if _, ok := adapter.(IncompleteWriteReclaimer); ok {
		t.Fatal("GCS now implements IncompleteWriteReclaimer. GCS exposes no API that enumerates " +
			"unfinalized resumable uploads, so an implementation can only return an empty result — " +
			"and an empty result from a reclaimer means 'none present', not 'cannot see'. Declare " +
			"the capability instead; that is what IncompleteWrites is for.")
	}
}

// TestCRC32CPolynomialIsCastagnoli pins the polynomial by its published check
// value rather than by a type assertion, which cannot tell two tables apart.
//
// The wrong table fails silently in the worst direction: IEEE is the package
// default, produces a valid checksum, and would simply never match the
// service's — so every upload would be reported as corrupt and deleted. The
// value below is the standard CRC-32C check constant for "123456789".
func TestCRC32CPolynomialIsCastagnoli(t *testing.T) {
	const checkInput = "123456789"
	const castagnoliCheck = uint32(0xE3069283)

	sum := crc32.Checksum([]byte(checkInput), crc32cTable)
	if sum != castagnoliCheck {
		t.Fatalf("crc32cTable computes %#08x over %q, want %#08x. That is not the Castagnoli "+
			"polynomial, and GCS reports CRC32C — so every upload would mismatch and be discarded "+
			"as corrupt", sum, checkInput, castagnoliCheck)
	}
	// Proves the check above can actually discriminate: the default table
	// must produce a different value, or the assertion is vacuous.
	if ieee := crc32.ChecksumIEEE([]byte(checkInput)); ieee == castagnoliCheck {
		t.Fatal("IEEE and Castagnoli agree on the check input, so this test cannot detect the " +
			"wrong table")
	}
}

// TestDiscardCorruptAlwaysReportsCorruption covers all three ways the cleanup
// can go, because the property under test is that NONE of them returns nil.
//
// The successful-removal case is the one that matters and the one a test
// reaching for a real client would have skipped: returning nil there makes
// PutStaged report the upload as succeeded, with an empty version id, for an
// object it has just deleted for being corrupt. That is not hypothetical —
// the first version of this code left that branch uncovered, and a mutation
// to it survived.
func TestDiscardCorruptAlwaysReportsCorruption(t *testing.T) {
	removalFailed := errors.New("delete refused")

	for name, tc := range map[string]struct {
		remove     removeVersion
		generation int64
		wants      []string
	}{
		"removed successfully": {
			remove:     func(context.Context, string, string) error { return nil },
			generation: 1786920012739517,
			wants:      []string{"does not match what was sent"},
		},
		"removal failed": {
			remove:     func(context.Context, string, string) error { return removalFailed },
			generation: 1786920012739517,
			wants:      []string{"does not match what was sent", "removing it also failed"},
		},
		"nothing nameable to remove": {
			// Must not call remove at all: there is no generation to name.
			remove: func(context.Context, string, string) error {
				t.Fatal("attempted a removal without a usable generation")
				return nil
			},
			generation: 0,
			wants:      []string{"does not match what was sent", "no usable generation"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := discardCorrupt(context.Background(), tc.remove, "probe/a.txt", tc.generation, 1, 2)
			if err == nil {
				t.Fatal("corruption was not reported: PutStaged would return a nil error and an " +
					"empty version id for an object that failed verification")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %q in: %v", want, err)
				}
			}
		})
	}

	// The failed-removal case must keep the cause reachable, so a caller can
	// tell a permissions problem from a transport one without reading text.
	err := discardCorrupt(context.Background(),
		func(context.Context, string, string) error { return removalFailed },
		"probe/a.txt", 1786920012739517, 1, 2)
	if !errors.Is(err, removalFailed) {
		t.Fatalf("the removal failure is not wrapped, so its cause is unreachable: %v", err)
	}
}
