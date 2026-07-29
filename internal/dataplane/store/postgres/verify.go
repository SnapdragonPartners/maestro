package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"

	"orchestrator/internal/dataplane/store"
)

// requireDigest applies the schema's own CHECK shape at the seam, so a
// caller learns which field was wrong rather than reading a constraint name
// it cannot act on. The pattern is artifacts.go's -- one digest shape for
// the whole schema, as the migration comments say.
func requireDigest(digest string) error {
	if !digestPattern.MatchString(digest) {
		return fmt.Errorf("digest %q is not 64 lowercase hex characters: the digest IS the object's "+
			"address, and an address the schema refuses is one nothing could ever read back", digest)
	}
	return nil
}

func requireMediaType(mediaType string) error {
	if strings.TrimSpace(mediaType) == "" {
		return errors.New("media_type is blank: an attachment nothing can interpret is evidence " +
			"nobody can read")
	}
	return nil
}

// countingHasher presents exactly `limit` bytes of a source to the
// uploader, hashing and counting what it hands over.
//
// The LIMIT is not a convenience. MEASURED against the pinned client: given
// a source longer than the stated size, a SEEKABLE one uploads a silent
// truncation, while a non-seekable one dies inside the HTTP transport with
// `ContentLength=N with Body length 0` -- an error that says nothing about
// size, on a path taken by every source once it is wrapped for hashing.
// Neither outcome is a check, and the second one preempts the check this
// module actually makes.
//
// Bounding what the uploader can see makes the outcome the same for both:
// exactly `limit` bytes are uploaded, and whether the source had more is
// decided afterwards, here, by reading past it.
type countingHasher struct {
	source  io.Reader
	limited io.Reader
	hash    hash.Hash
	read    int64
	limit   int64
	// sawEOF records that the LIMITED reader ended. Together with a count
	// below the limit it is what distinguishes a source that ran out early
	// from an upload that failed in transit -- both leave fewer bytes read,
	// and only one of them is the caller's fault.
	sawEOF bool
}

func newCountingHasher(source io.Reader, limit int64) *countingHasher {
	return &countingHasher{
		source:  source,
		limited: io.LimitReader(source, limit),
		hash:    sha256.New(),
		limit:   limit,
	}
}

func (c *countingHasher) Read(p []byte) (int, error) {
	n, err := c.limited.Read(p)
	if n > 0 {
		// Hash.Write never returns an error, by its documented contract.
		_, _ = c.hash.Write(p[:n])
		c.read += int64(n)
	}
	if err == io.EOF { //nolint:errorlint // io.EOF is compared by identity, per io.Reader
		c.sawEOF = true
	}
	// Returned unwrapped, deliberately: io.EOF is compared by identity by
	// every consumer in the standard library, and an io.Reader that wraps
	// it is one nothing can read to completion.
	return n, err //nolint:wrapcheck // an io.Reader must return its source's error as-is
}

// sourceEndedEarly reports a source that ran out before the stated size.
//
// io.LimitReader returns EOF at the limit as well, so EOF alone means
// nothing; it is EOF with fewer bytes read than the limit that can only
// come from the source itself.
func (c *countingHasher) sourceEndedEarly() bool {
	return c.sawEOF && c.read < c.limit
}

func (c *countingHasher) digest() string {
	return hex.EncodeToString(c.hash.Sum(nil))
}

// exhausted reports whether the SOURCE is finished.
//
// This is the "one more read must return EOF" check. It reads the
// underlying source, past the limit the uploader saw, so the probe byte
// reaches neither the hash nor the object. "We stopped at size" is not the
// same claim: a source longer than stated hashes to something plausible
// over its first `size` bytes and stores a truncation nobody detects.
func (c *countingHasher) exhausted() (bool, error) {
	var probe [1]byte
	n, err := c.source.Read(probe[:])
	if n > 0 {
		return false, nil
	}
	if err == nil || err == io.EOF { //nolint:errorlint // io.EOF is compared by identity, per io.Reader
		return true, nil
	}
	return false, fmt.Errorf("read past the stated size: %w", err)
}

// verifyingReader streams an object and fails at EOF if what it read does
// not hash to the digest that addressed it.
//
// The failure surfaces at EOF because that is the earliest instant it CAN
// be known, and it is returned as a read error so a caller copying the
// stream cannot mistake a corrupt object for a complete one. Everything
// this module offers that writes to a destination must therefore treat a
// read error as fatal, and never move a destination into place before the
// final Read has returned.
type verifyingReader struct {
	source io.ReadCloser
	hash   hash.Hash
	want   string
}

func newVerifyingReader(source io.ReadCloser, want string) *verifyingReader {
	return &verifyingReader{source: source, hash: sha256.New(), want: want}
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.source.Read(p)
	if n > 0 {
		_, _ = v.hash.Write(p[:n])
	}
	if err == io.EOF { //nolint:errorlint // io.EOF is compared by identity, per io.Reader
		if got := hex.EncodeToString(v.hash.Sum(nil)); got != v.want {
			// Recomputed rather than latched. An exhausted reader goes on
			// returning EOF, so this comparison runs again and fails again
			// on every subsequent read -- which a test asserts. A latch
			// field would be a second mechanism for the same guarantee, and
			// removing it changed no observable behaviour.
			return n, fmt.Errorf("%w: object addressed by %s hashes to %s", store.ErrInvariant, v.want, got)
		}
	}
	return n, err //nolint:wrapcheck // an io.Reader must return its source's error as-is
}

func (v *verifyingReader) Close() error {
	if err := v.source.Close(); err != nil {
		return fmt.Errorf("close verified stream: %w", err)
	}
	return nil
}

// hashStream drains a reader and returns its digest. Used to verify what
// the store holds, where there is no size to enforce -- the object is
// whatever it is, and the question is only whether it hashes to its own
// address.
func hashStream(source io.Reader) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, source); err != nil {
		return "", fmt.Errorf("read stream: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
