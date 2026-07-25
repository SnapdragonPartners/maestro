package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// composeSource reads the real Compose file, so these assertions bind the
// shipped artifact rather than a copy that could drift from it.
func composeSource(t *testing.T) string {
	t.Helper()
	// Tests run in the package directory; the file lives at the repo root.
	path := filepath.Join("..", "..", "..", DefaultComposeFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// The load-bearing isolation rule from design D3, enforced mechanically
// rather than remembered.
//
// v1 labels its containers `com.maestro.session=<id>`, and the benchmark
// adapter sweeps by that label on teardown. A data-plane container
// carrying one would be destroyed mid-benchmark-run — which would look
// like a flaky golden run, days from the change that caused it. The
// failure mode is bad enough, and the temptation to add the label "for
// symmetry" plausible enough, that it gets a test.
func TestComposeCarriesNoSessionLabel(t *testing.T) {
	source := composeSource(t)

	for _, forbidden := range []string{"com.maestro.session", "maestro.session"} {
		for i, line := range strings.Split(source, "\n") {
			trimmed := strings.TrimSpace(line)
			// The prohibition is documented in a comment in the file; that
			// is the one place the string is allowed to appear.
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(line, forbidden) {
				t.Errorf("line %d carries %q: the benchmark adapter sweeps containers by that label and would destroy the data plane mid-run\n\t%s",
					i+1, forbidden, trimmed)
			}
		}
	}
}

// Every service must carry the component label, which is what makes the
// data plane identifiable without a session label.
func TestComposeLabelsEveryService(t *testing.T) {
	source := composeSource(t)

	const label = "com.maestro.component: dataplane"
	if got := strings.Count(source, label); got != 2 {
		t.Errorf("found %d %q labels, want one per service (postgres, minio)", got, label)
	}
}

// ADR 0026: cross-arch artifacts are pinned by arch-independent manifest
// digest, never a mutable tag. Development is arm64 and CI is amd64, so a
// tag pin would let the two run different images.
func TestImagesArePinnedByDigest(t *testing.T) {
	path := filepath.Join("..", "..", "..", "deploy", "dataplane", "images.env")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	pins := 0
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		_, value, found := strings.Cut(trimmed, "=")
		if !found {
			t.Errorf("malformed pin line: %s", trimmed)
			continue
		}
		pins++
		if !strings.Contains(value, "@sha256:") {
			t.Errorf("image %q is not pinned by digest", value)
		}
		if strings.Contains(value, ":") && strings.Contains(strings.SplitN(value, "@", 2)[0], ":") {
			t.Errorf("image %q carries a tag as well as a digest; the tag belongs in a comment", value)
		}
	}
	if pins != 2 {
		t.Errorf("found %d image pins, want 2 (postgres, minio)", pins)
	}
}
