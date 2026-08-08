package benchmarkimport_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/benchmarkimport"
)

// writeTree materialises an evidence directory and returns its path.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// digestOf is the content digest the plane addresses bytes by.
func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// pathsOf reads the scan's file names, which are also its order.
func pathsOf(scan *benchmarkimport.EvidenceScan) []string {
	names := make([]string, 0, len(scan.Files))
	for index := range scan.Files {
		names = append(names, scan.Files[index].Path)
	}
	return names
}

// TestScanEvidenceDescribesWhatItFound is the control.
func TestScanEvidenceDescribesWhatItFound(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pr.json":      `{"number":1}`,
		"logs/run.log": "hello\n",
		"diff":         "--- a\n+++ b\n",
	})

	scan, err := benchmarkimport.ScanEvidence(dir, benchmarkimport.Caps{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(scan.Skips) != 0 {
		t.Errorf("an ordinary tree produced skips: %+v", scan.Skips)
	}
	// Lexical, and slash-separated regardless of platform. The payload
	// quotes this order, and a payload whose content depended on directory
	// iteration would digest differently on every read of the same bytes.
	if got := strings.Join(pathsOf(scan), ","); got != "diff,logs/run.log,pr.json" {
		t.Errorf("scan order is %q, want lexical slash-separated names", got)
	}

	byPath := map[string]benchmarkimport.EvidenceFile{}
	for index := range scan.Files {
		byPath[scan.Files[index].Path] = scan.Files[index]
	}
	if got := byPath["pr.json"]; got.Digest != digestOf(`{"number":1}`) {
		t.Errorf("pr.json digests to %s", got.Digest)
	}
	if got := byPath["logs/run.log"]; got.SizeBytes != 6 {
		t.Errorf("logs/run.log is %d bytes, want 6", got.SizeBytes)
	}
	if scan.Bytes != int64(len(`{"number":1}`)+len("hello\n")+len("--- a\n+++ b\n")) {
		t.Errorf("scan totals %d bytes", scan.Bytes)
	}
}

// Media type is by extension, and an unknown one is not a failure.
func TestMediaTypeIsByExtension(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pr.json":      "{}",
		"log.JSONL":    "{}\n",
		"maestro.db":   "sqlite",
		"diff":         "patch",
		"notes.wibble": "?",
	})
	scan, err := benchmarkimport.ScanEvidence(dir, benchmarkimport.Caps{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := map[string]string{
		"pr.json":      "application/json",
		"log.JSONL":    "application/x-ndjson",
		"maestro.db":   "application/vnd.sqlite3",
		"diff":         "application/octet-stream",
		"notes.wibble": "application/octet-stream",
	}
	for index := range scan.Files {
		file := &scan.Files[index]
		if want[file.Path] != file.MediaType {
			t.Errorf("%s is typed %q, want %q", file.Path, file.MediaType, want[file.Path])
		}
	}
}

// The per-file cap is a boundary, and both sides of it are asserted.
//
// A file exactly AT the cap is within it; one byte more is not. Nothing
// short of reading past the bound can tell those apart, which is why the
// walk reads bound+1 rather than stopping at bound — and why a test that
// only tried a hugely oversized file would pass against an off-by-one.
func TestTheFileCapIsABoundary(t *testing.T) {
	const limit = 64
	dir := writeTree(t, map[string]string{
		"exactly.log": strings.Repeat("a", limit),
		"over.log":    strings.Repeat("b", limit+1),
		"under.log":   strings.Repeat("c", limit-1),
	})
	scan, err := benchmarkimport.ScanEvidence(dir, benchmarkimport.Caps{FileBytes: limit})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := strings.Join(pathsOf(scan), ","); got != "exactly.log,under.log" {
		t.Errorf("the cap took %q, want the file at the cap and the one under it", got)
	}
	if len(scan.Skips) != 1 {
		t.Fatalf("the cap produced %d skips, want 1: %+v", len(scan.Skips), scan.Skips)
	}
	if scan.Skips[0].Path != "over.log" || scan.Skips[0].Reason != benchmarkimport.SkipFileCap {
		t.Errorf("skip is %+v, want over.log over the per-file cap", scan.Skips[0])
	}
	// And the digest of the file AT the cap is the digest of its whole
	// content, not of the bound-length prefix the reader stopped at.
	if scan.Files[0].Digest != digestOf(strings.Repeat("a", limit)) {
		t.Errorf("the file at the cap digests to %s, which is not its content", scan.Files[0].Digest)
	}
}

// The per-attempt cap bounds the whole attempt, and every file it excludes
// is named.
//
// A per-file cap alone does not bound an attempt: a thousand files just
// under it are within every per-file rule. And a cap that dropped the
// remainder silently would read as "there was nothing more to import".
func TestTheAttemptCapNamesEverythingItExcludes(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a.log": strings.Repeat("a", 40),
		"b.log": strings.Repeat("b", 40),
		"c.log": strings.Repeat("c", 40),
	})
	scan, err := benchmarkimport.ScanEvidence(dir, benchmarkimport.Caps{AttemptBytes: 100})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := strings.Join(pathsOf(scan), ","); got != "a.log,b.log" {
		t.Errorf("the attempt cap took %q, want the two that fit", got)
	}
	if len(scan.Skips) != 1 {
		t.Fatalf("%d skips, want 1: %+v", len(scan.Skips), scan.Skips)
	}
	if scan.Skips[0].Path != "c.log" || scan.Skips[0].Reason != benchmarkimport.SkipAttemptCap {
		t.Errorf("skip is %+v, want c.log over the per-attempt cap", scan.Skips[0])
	}
	if scan.Bytes != 80 {
		t.Errorf("the scan totals %d bytes, want 80", scan.Bytes)
	}
}

// A symlink is named and never read, even when its target is an ordinary
// file inside the same directory.
//
// Containment says nothing about a link three levels down, and one pointing
// back INTO the store is worse than one pointing out: it attributes one
// attempt's evidence to another while passing every containment test.
func TestASymlinkIsNamedAndNeverRead(t *testing.T) {
	dir := writeTree(t, map[string]string{"pr.json": "{}"})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not evidence"), 0o600); err != nil {
		t.Fatalf("write the file outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.txt")); err != nil {
		t.Fatalf("plant the escaping link: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "pr.json"), filepath.Join(dir, "inside.json")); err != nil {
		t.Fatalf("plant the inward link: %v", err)
	}

	scan, err := benchmarkimport.ScanEvidence(dir, benchmarkimport.Caps{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := strings.Join(pathsOf(scan), ","); got != "pr.json" {
		t.Errorf("the scan took %q; a link must never be followed", got)
	}
	named := map[string]benchmarkimport.SkipReason{}
	for index := range scan.Skips {
		named[scan.Skips[index].Path] = scan.Skips[index].Reason
	}
	for _, link := range []string{"escape.txt", "inside.json"} {
		if named[link] != benchmarkimport.SkipSymlink {
			t.Errorf("%s is recorded as %q, want %q", link, named[link], benchmarkimport.SkipSymlink)
		}
	}
	// The bytes outside the store are not in the scan at all — not by
	// digest, not by size, not by name.
	for index := range scan.Files {
		if scan.Files[index].Digest == digestOf("not evidence") {
			t.Errorf("the file outside the results store was read: %+v", scan.Files[index])
		}
	}
}

// A negative cap is refused rather than treated as an unreachable bound.
func TestANegativeCapIsRefused(t *testing.T) {
	dir := writeTree(t, map[string]string{"pr.json": "{}"})
	for _, caps := range []benchmarkimport.Caps{{FileBytes: -1}, {AttemptBytes: -1}} {
		if _, err := benchmarkimport.ScanEvidence(dir, caps); err == nil {
			t.Errorf("a negative cap %+v was accepted", caps)
		}
	}
}

// The zero value is the DEFAULT, never "unbounded".
//
// An unset field is far more often an unset field than a deliberate request
// to import without limit, and the failure mode of guessing wrong is an
// unbounded upload.
func TestAZeroCapIsTheDefaultAndNotUnbounded(t *testing.T) {
	dir := writeTree(t, map[string]string{"pr.json": "{}"})
	scan, err := benchmarkimport.ScanEvidence(dir, benchmarkimport.Caps{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(scan.Files) != 1 {
		t.Fatalf("the default caps took %d files, want 1", len(scan.Files))
	}
	if benchmarkimport.DefaultFileCapBytes <= 0 || benchmarkimport.DefaultAttemptCapBytes <= 0 {
		t.Error("a default cap is not positive, so the zero value would import without limit")
	}
}
