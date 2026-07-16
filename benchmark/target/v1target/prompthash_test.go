package v1target

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sourceRoot locates the v1 target checkout (this repo's root) for tests.
func sourceRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve source root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "templates")); err != nil {
		t.Skipf("v1 source tree not present at %s", root)
	}
	return root
}

func TestPromptHashDeterministicAndContentSensitive(t *testing.T) {
	root := sourceRoot(t)
	first, err := promptHash(root)
	if err != nil {
		t.Fatalf("prompt hash: %v", err)
	}
	second, secondErr := promptHash(root)
	if secondErr != nil {
		t.Fatalf("prompt hash again: %v", secondErr)
	}
	if first != second || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("prompt hash must be deterministic and prefixed: %q vs %q", first, second)
	}

	// Content sensitivity: a synthetic tree with one changed template byte
	// must hash differently.
	synthetic := t.TempDir()
	for _, entry := range manifestEntries() {
		src := filepath.Join(root, entry)
		if strings.HasSuffix(entry, "/") {
			if copyErr := copyTree(strings.TrimSuffix(src, "/"), filepath.Join(synthetic, strings.TrimSuffix(entry, "/"))); copyErr != nil {
				t.Fatalf("copy %s: %v", entry, copyErr)
			}
			continue
		}
		if mkErr := os.MkdirAll(filepath.Dir(filepath.Join(synthetic, entry)), 0o755); mkErr != nil {
			t.Fatalf("mkdir: %v", mkErr)
		}
		raw, readErr := os.ReadFile(src)
		if readErr != nil {
			t.Fatalf("read %s: %v", entry, readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(synthetic, entry), raw, 0o644); writeErr != nil {
			t.Fatalf("write %s: %v", entry, writeErr)
		}
	}
	base, err := promptHash(synthetic)
	if err != nil {
		t.Fatalf("synthetic hash: %v", err)
	}
	target := filepath.Join(synthetic, "pkg", "templates", "pm", "working.tpl.md")
	if mutateErr := os.WriteFile(target, []byte("changed prompt content\n"), 0o644); mutateErr != nil {
		t.Fatalf("mutate template: %v", mutateErr)
	}
	changed, err := promptHash(synthetic)
	if err != nil {
		t.Fatalf("mutated hash: %v", err)
	}
	if base == changed {
		t.Fatalf("prompt content change must move P")
	}
}

func TestManifestEntriesExist(t *testing.T) {
	root := sourceRoot(t)
	if _, err := expandManifest(root); err != nil {
		t.Fatalf("every manifest entry must exist in the target checkout: %v", err)
	}
}

// TestPromptScannerClassification is the independent inventory guard: a
// deliberately over-inclusive heuristic sweep for prompt-bearing sources;
// every candidate must be classified into exactly one of the two reviewed
// lists (manifest = hashed into P, allowlist = reviewed non-prompt).
func TestPromptScannerClassification(t *testing.T) {
	root := sourceRoot(t)
	pattern := regexp.MustCompile(`"You are |` + "`" + `You are |You are an? |Your task |Your job is `)
	manifest, allowlist := manifestEntries(), allowlistEntries()

	var unclassified []string
	walkErr := filepath.WalkDir(filepath.Join(root, "pkg"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !pattern.Match(raw) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if !coveredBy(rel, manifest) && !coveredBy(rel, allowlist) {
			unclassified = append(unclassified, rel)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("scanner walk: %v", walkErr)
	}
	if len(unclassified) > 0 {
		t.Fatalf("prompt-scanner candidates not classified into manifest.txt or allowlist.txt:\n  %s", strings.Join(unclassified, "\n  "))
	}
	// Templates are prompt content by definition and must be manifest-covered.
	if !coveredBy("pkg/templates/anything.tpl.md", manifest) {
		t.Fatalf("pkg/templates/ must be covered by the manifest")
	}
}
