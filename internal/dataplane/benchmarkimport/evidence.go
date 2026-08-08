package benchmarkimport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The evidence caps (design D8).
//
// Both are REPORTED rather than silently applied: a cap that drops work
// quietly reads as "there was nothing more to import", which is the one
// thing a conformance record must never say by accident. Every skip is named
// in the import summary and in the report payload, so the artifact records
// what it does not contain.
const (
	// DefaultFileCapBytes bounds one evidence file. A v1 `maestro.db`
	// snapshot is the realistic candidate to trip it.
	DefaultFileCapBytes int64 = 256 << 20

	// DefaultAttemptCapBytes bounds one attempt's evidence in total. A
	// per-file cap alone does not bound an attempt: a thousand files just
	// under the file cap are within every per-file rule and are still not
	// evidence anyone will read.
	DefaultAttemptCapBytes int64 = 1 << 30
)

// Caps bounds what one attempt may contribute.
//
// Zero means "the default", never "unbounded". An unset field is far more
// often an unset field than a deliberate request to import without limit,
// and the failure mode of guessing wrong is an unbounded upload.
type Caps struct {
	FileBytes    int64
	AttemptBytes int64
}

// withDefaults resolves the zero value, and refuses a negative cap rather
// than treating it as an unreachable bound.
func (c Caps) withDefaults() (Caps, error) {
	if c.FileBytes < 0 || c.AttemptBytes < 0 {
		return Caps{}, fmt.Errorf("evidence caps must not be negative (file %d, attempt %d)",
			c.FileBytes, c.AttemptBytes)
	}
	if c.FileBytes == 0 {
		c.FileBytes = DefaultFileCapBytes
	}
	if c.AttemptBytes == 0 {
		c.AttemptBytes = DefaultAttemptCapBytes
	}
	return c, nil
}

// SkipReason names why a file beneath an evidence directory was not
// uploaded. Each is a different thing an operator does about it.
type SkipReason string

const (
	// SkipSymlink is a link inside a legitimate evidence directory. It is
	// never read and never followed: a containment check on the directory
	// says nothing about a link pointing out of it three levels down, and
	// one pointing back INTO the store is worse — it attributes one
	// attempt's evidence to another while passing every containment test.
	SkipSymlink SkipReason = "symbolic link"

	// SkipIrregular is a socket, device or fifo. Not evidence, and reading
	// one can block forever.
	SkipIrregular SkipReason = "not a regular file"

	// SkipFileCap is a file over the per-file cap.
	SkipFileCap SkipReason = "over the per-file cap"

	// SkipAttemptCap is a file the attempt has no remaining budget for.
	// Distinguished from SkipFileCap because the file itself is fine and
	// the operator's response differs: raise the attempt cap, or accept
	// that this attempt's evidence is larger than the plane will hold.
	SkipAttemptCap SkipReason = "over the per-attempt total cap"
)

// EvidenceFile is one file the walk will upload.
type EvidenceFile struct {
	// Path is relative to the attempt's evidence directory, slash-separated
	// so the payload reads the same on every platform. It is NOT a locator:
	// the bytes are addressed by digest, and this describes what they were.
	Path      string
	Digest    string
	MediaType string
	SizeBytes int64
}

// EvidenceSkip is a file deliberately not uploaded, and why.
type EvidenceSkip struct {
	Path   string
	Reason SkipReason
	Detail string
}

// EvidenceScan is one attempt's evidence as the walk found it.
type EvidenceScan struct {
	Files []EvidenceFile
	Skips []EvidenceSkip
	Bytes int64
}

// mediaTypes maps an extension to what the plane records for it.
//
// Small and explicit, per design D8: unknown is not a failure, it is
// application/octet-stream. Sniffing content would be a second, disagreeing
// answer to a question the extension already answers well enough for
// evidence nobody dispatches on.
//
//nolint:gochecknoglobals // A package-level table, immutable after init.
var mediaTypes = map[string]string{
	".json":  "application/json",
	".jsonl": "application/x-ndjson",
	".log":   "text/plain; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
	".md":    "text/markdown; charset=utf-8",
	".diff":  "text/x-diff; charset=utf-8",
	".patch": "text/x-diff; charset=utf-8",
	".db":    "application/vnd.sqlite3",
	".yaml":  "application/yaml",
	".yml":   "application/yaml",
}

// defaultMediaType is what an unrecognized extension records.
const defaultMediaType = "application/octet-stream"

// mediaTypeOf classifies by extension, lowercased so `.JSON` and `.json`
// are one answer.
func mediaTypeOf(path string) string {
	if known, ok := mediaTypes[strings.ToLower(filepath.Ext(path))]; ok {
		return known
	}
	return defaultMediaType
}

// ScanEvidence walks one attempt's evidence directory and describes what it
// found, hashing every file it will upload.
//
// It is a pure read of the filesystem: nothing is stored here, and the
// caller decides what to do with the result. That separation is what lets
// report assembly scan every attempt in the suite — ledgered or newly
// imported — before it writes anything, which design D7 requires and which
// a scan fused to an upload could not offer.
//
// The walk does not follow symbolic links. filepath.WalkDir reports an entry
// by its Lstat mode and never descends a link, so a link is visited once, as
// a non-directory, and skipped here by mode rather than by trusting the
// walker's behaviour to stay as it is.
//
// Order is lexical, which is WalkDir's own guarantee, so two scans of one
// unchanged directory produce identical slices. The report payload quotes
// this order, and a payload whose content depended on directory iteration
// would digest differently on every read of the same bytes.
func ScanEvidence(dir string, caps Caps) (*EvidenceScan, error) {
	resolved, err := caps.withDefaults()
	if err != nil {
		return nil, err
	}
	scan := &EvidenceScan{}
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk evidence at %s: %w", path, err)
		}
		if path == dir {
			return nil
		}
		relative, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return fmt.Errorf("relate %s to %s: %w", path, dir, relErr)
		}
		name := filepath.ToSlash(relative)
		switch mode := entry.Type(); {
		case mode&fs.ModeSymlink != 0:
			// Named, never read. A skipped link is reported at the point it
			// was found, because "the import ignored something here" is the
			// whole content of the finding.
			scan.skip(name, SkipSymlink, "links are never followed")
			return nil
		case mode.IsDir():
			return nil
		case !mode.IsRegular():
			scan.skip(name, SkipIrregular, mode.String())
			return nil
		}
		return scan.take(path, name, resolved)
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk the evidence under %s: %w", dir, walkErr)
	}
	// Belt and braces over WalkDir's lexical order: the payload's digest
	// depends on this sequence, and one sort is cheaper than a guarantee
	// that has to be re-read every time someone touches this function.
	sort.Slice(scan.Files, func(i, j int) bool { return scan.Files[i].Path < scan.Files[j].Path })
	sort.Slice(scan.Skips, func(i, j int) bool { return scan.Skips[i].Path < scan.Skips[j].Path })
	return scan, nil
}

// skip records a file the walk declined.
func (s *EvidenceScan) skip(path string, reason SkipReason, detail string) {
	s.Skips = append(s.Skips, EvidenceSkip{Path: path, Reason: reason, Detail: detail})
}

// take hashes one regular file, or records why it was skipped.
//
// The caps are enforced against the bytes actually read, not against the
// size the directory entry claims. A file that grew between the two would
// otherwise be admitted on a stale measurement and then hashed whole, which
// is exactly the unbounded read the cap exists to prevent.
func (s *EvidenceScan) take(path, name string, caps Caps) error {
	remaining := caps.AttemptBytes - s.Bytes
	if remaining <= 0 {
		s.skip(name, SkipAttemptCap, fmt.Sprintf("attempt cap %d bytes already reached", caps.AttemptBytes))
		return nil
	}
	// Bounded by the SMALLER of the two caps, so one read enforces both,
	// and by one byte more than the bound, which is what distinguishes a
	// file exactly at the cap from a file over it.
	bound := caps.FileBytes
	if remaining < bound {
		bound = remaining
	}

	digest, size, exceeded, err := hashFile(path, bound)
	if err != nil {
		return err
	}
	if exceeded {
		if bound == caps.FileBytes {
			s.skip(name, SkipFileCap, fmt.Sprintf("over %d bytes", caps.FileBytes))
		} else {
			s.skip(name, SkipAttemptCap,
				fmt.Sprintf("%d bytes remain of the %d-byte attempt cap", remaining, caps.AttemptBytes))
		}
		return nil
	}
	s.Files = append(s.Files, EvidenceFile{
		Path: name, Digest: digest, MediaType: mediaTypeOf(name), SizeBytes: size,
	})
	s.Bytes += size
	return nil
}

// hashFile returns the file's digest and size, or reports that it is longer
// than bound.
//
// It reads ONE byte past the bound rather than stopping at it, because a
// file exactly at the cap is within the cap and a file one byte longer is
// not, and nothing shorter than that extra read can tell them apart.
func hashFile(path string, bound int64) (digest string, size int64, exceeded bool, err error) {
	file, err := os.Open(path) //nolint:gosec // path came from a walk of a contained directory
	if err != nil {
		return "", 0, false, fmt.Errorf("open evidence %s: %w", path, err)
	}
	defer file.Close() //nolint:errcheck // read-only

	hash := sha256.New()
	copied, err := io.Copy(hash, io.LimitReader(file, bound+1))
	if err != nil {
		return "", 0, false, fmt.Errorf("read evidence %s: %w", path, err)
	}
	if copied > bound {
		return "", 0, true, nil
	}
	return hex.EncodeToString(hash.Sum(nil)), copied, false, nil
}

// evidenceBody is one attachment's bytes, opened on first read.
//
// AttachEvidence takes every attachment's reader in one call and stores them
// one at a time, so opening each file eagerly would hold one descriptor per
// evidence file across the whole upload — a suite of a hundred attempts
// would exhaust the process's descriptors before it stored anything. Opening
// lazily keeps one file open at a time on the path that matters, and Close
// is still called for every body, including the ones a content-addressed
// store never had to read.
type evidenceBody struct {
	file *os.File
	path string
}

func (b *evidenceBody) Read(p []byte) (int, error) {
	if b.file == nil {
		file, err := os.Open(b.path) //nolint:gosec // path came from a walk of a contained directory
		if err != nil {
			return 0, fmt.Errorf("open evidence %s: %w", b.path, err)
		}
		b.file = file
	}
	return b.file.Read(p) //nolint:wrapcheck // io.Reader contract: io.EOF must pass through unwrapped
}

// Close releases the descriptor if one was ever taken.
func (b *evidenceBody) Close() error {
	if b.file == nil {
		return nil
	}
	err := b.file.Close()
	b.file = nil
	if err != nil {
		return fmt.Errorf("close evidence %s: %w", b.path, err)
	}
	return nil
}
