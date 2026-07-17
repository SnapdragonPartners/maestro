package v1target

import (
	"fmt"
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

// phrasePattern is the instruction-phrase half of the scanner heuristics.
func phrasePattern() *regexp.Regexp {
	return regexp.MustCompile(`"You are |` + "`" + `You are |You are an? |Your task |Your job is |Respond with |Respond ONLY|respond ONLY|You must respond|Your decision|Review the following|review_complete with|Call review_complete`)
}

// hasLongRawString is the structural half: a backtick raw string spanning
// three or more lines is, in this codebase, either SQL or prompt/tool text
// — deliberately over-inclusive; the allowlist absorbs the SQL.
func hasLongRawString(raw []byte) bool {
	parts := strings.Split(string(raw), "`")
	for i := 1; i < len(parts); i += 2 {
		if strings.Count(parts[i], "\n") >= 3 {
			return true
		}
	}
	return false
}

// TestPromptScannerClassification is the independent inventory guard: a
// deliberately over-inclusive two-heuristic sweep (instruction phrases +
// multi-line raw strings) for prompt-bearing sources; every candidate must
// be classified into exactly one of the two reviewed lists (manifest =
// hashed into P, allowlist = reviewed non-prompt).
func TestPromptScannerClassification(t *testing.T) {
	root := sourceRoot(t)
	pattern := phrasePattern()
	manifest, allowlist := manifestEntries(), allowlistEntries()

	var unclassified, doubleListed []string
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
		if !pattern.Match(raw) && !hasLongRawString(raw) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("relativize %s: %w", path, relErr)
		}
		inManifest, inAllowlist := coveredBy(rel, manifest), coveredBy(rel, allowlist)
		switch {
		case inManifest && inAllowlist:
			doubleListed = append(doubleListed, rel)
		case !inManifest && !inAllowlist:
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
	if len(doubleListed) > 0 {
		t.Fatalf("candidates must be in exactly one list:\n  %s", strings.Join(doubleListed, "\n  "))
	}
	// Templates are prompt content by definition and must be manifest-covered.
	if !coveredBy("pkg/templates/anything.tpl.md", manifest) {
		t.Fatalf("pkg/templates/ must be covered by the manifest")
	}
}

// TestKnownPromptFilesAreManifested is the regression Codex round 1 asked
// for: every currently known prompt-bearing file stays in P.
func TestKnownPromptFilesAreManifested(t *testing.T) {
	manifest := manifestEntries()
	for _, file := range []string{
		"pkg/architect/dev_chat.go",
		"pkg/architect/request.go",
		"pkg/architect/request_plan.go",
		"pkg/architect/request_code.go",
		"pkg/architect/request_completion.go",
		"pkg/architect/request_question.go",
		"pkg/architect/request_merge.go",
		"pkg/architect/request_spec.go",
		"pkg/coder/driver.go",
		"pkg/coder/todo_collection.go",
		"pkg/coder/code_review.go",
		"pkg/pm/driver.go",
		"pkg/tools/review_complete.go",
	} {
		if !coveredBy(file, manifest) {
			t.Fatalf("%s is prompt-bearing and must be in manifest.txt", file)
		}
	}
}
