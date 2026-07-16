package v1target

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The prompt manifest and its false-positive allowlist, embedded so the
// adapter's prompt identity is versioned with the adapter itself.
var (
	//go:embed manifest.txt
	manifestRaw string
	//go:embed allowlist.txt
	allowlistRaw string
)

// parseList strips comments and blanks from an embedded list file.
func parseList(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// manifestEntries returns the prompt manifest (hashed into P).
func manifestEntries() []string { return parseList(manifestRaw) }

// allowlistEntries returns the reviewed non-prompt allowlist (not hashed).
func allowlistEntries() []string { return parseList(allowlistRaw) }

// promptHash computes the v1-embedded prompt identity: sha256 over the
// deterministic manifest expansion (sorted relative paths + contents) of
// the target checkout at sourceDir. Prompt identity moves only when prompt
// content moves.
func promptHash(sourceDir string) (string, error) {
	files, err := expandManifest(sourceDir)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(sourceDir, rel))
		if err != nil {
			return "", fmt.Errorf("read prompt input %s: %w", rel, err)
		}
		_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00", rel, len(content)) //nolint:errcheck // hash.Hash writers never fail
		hasher.Write(content)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// expandManifest resolves manifest entries against sourceDir into a sorted
// list of relative file paths. Every entry must match something: a moved
// prompt file fails loudly instead of silently dropping out of P.
func expandManifest(sourceDir string) ([]string, error) {
	seen := make(map[string]bool)
	for _, entry := range manifestEntries() {
		if strings.HasSuffix(entry, "/") {
			root := filepath.Join(sourceDir, entry)
			found := false
			walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				rel, relErr := filepath.Rel(sourceDir, path)
				if relErr != nil {
					return fmt.Errorf("relativize %s: %w", path, relErr)
				}
				seen[filepath.ToSlash(rel)] = true
				found = true
				return nil
			})
			if walkErr != nil {
				return nil, fmt.Errorf("manifest entry %q: %w", entry, walkErr)
			}
			if !found {
				return nil, fmt.Errorf("manifest entry %q matched no files in %s", entry, sourceDir)
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(sourceDir, entry)); err != nil {
			return nil, fmt.Errorf("manifest entry %q missing from %s: %w", entry, sourceDir, err)
		}
		seen[filepath.ToSlash(entry)] = true
	}
	files := make([]string, 0, len(seen))
	for rel := range seen {
		files = append(files, rel)
	}
	sort.Strings(files)
	return files, nil
}

// coveredBy reports whether relPath is covered by any entry of list
// (exact file or directory prefix).
func coveredBy(relPath string, list []string) bool {
	rel := filepath.ToSlash(relPath)
	for _, entry := range list {
		if strings.HasSuffix(entry, "/") {
			if strings.HasPrefix(rel, entry) {
				return true
			}
			continue
		}
		if rel == entry {
			return true
		}
	}
	return false
}
