package postgres

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// stallingReader yields its content, then reports no progress a number of
// times, and only then continues or ends.
//
// io.Reader permits (0, nil): it means nothing happened, not that the
// source is finished. A network-backed reader between packets returns
// exactly this, and treating it as exhaustion is how a source that pauses
// mid-stream gets accepted as complete.
type stallingReader struct {
	content []byte
	after   []byte
	stalls  int
	read    int
}

func (r *stallingReader) Read(p []byte) (int, error) {
	if r.read < len(r.content) {
		n := copy(p, r.content[r.read:])
		r.read += n
		return n, nil
	}
	if r.stalls > 0 {
		r.stalls--
		return 0, nil
	}
	if len(r.after) > 0 {
		n := copy(p, r.after)
		r.after = r.after[n:]
		return n, nil
	}
	return 0, io.EOF
}

// TestExhaustedDoesNotMistakeAStallForTheEnd is the case a single probe
// gets wrong: the source pauses at exactly the stated size and then
// produces more, which means the caller's content was longer than it said.
func TestExhaustedDoesNotMistakeAStallForTheEnd(t *testing.T) {
	const content = "exactly this much"
	source := &stallingReader{
		content: []byte(content),
		stalls:  3,
		after:   []byte(" and then some more"),
	}

	hasher := newCountingHasher(source, int64(len(content)))
	if _, err := io.Copy(io.Discard, hasher); err != nil {
		t.Fatalf("read the stated size: %v", err)
	}

	exhausted, err := hasher.exhausted()
	if err != nil {
		t.Fatalf("exhausted: %v", err)
	}
	if exhausted {
		t.Fatal("a source that stalled and then produced more bytes was reported as finished; " +
			"a single (0, nil) read is not proof of anything")
	}
}

// TestExhaustedAcceptsAStallBeforeAGenuineEnd is the other half: a source
// that pauses and then really does end is exhausted, and must not be
// refused for having hesitated.
func TestExhaustedAcceptsAStallBeforeAGenuineEnd(t *testing.T) {
	const content = "exactly this much"
	source := &stallingReader{content: []byte(content), stalls: 3}

	hasher := newCountingHasher(source, int64(len(content)))
	if _, err := io.Copy(io.Discard, hasher); err != nil {
		t.Fatalf("read the stated size: %v", err)
	}

	exhausted, err := hasher.exhausted()
	if err != nil {
		t.Fatalf("exhausted: %v", err)
	}
	if !exhausted {
		t.Fatal("a source that stalled and then ended was not reported as finished")
	}
}

// TestExhaustedGivesUpOnAReaderThatNeverProgresses bounds the loop. A
// source that answers every probe with (0, nil) forever would otherwise
// spin, and the honest answer is that its length cannot be established.
func TestExhaustedGivesUpOnAReaderThatNeverProgresses(t *testing.T) {
	const content = "exactly this much"
	source := &stallingReader{content: []byte(content), stalls: maxIdleProbes + 1}

	hasher := newCountingHasher(source, int64(len(content)))
	if _, err := io.Copy(io.Discard, hasher); err != nil {
		t.Fatalf("read the stated size: %v", err)
	}

	exhausted, err := hasher.exhausted()
	if err == nil {
		t.Fatalf("a reader that never progresses reported exhausted=%v with no error", exhausted)
	}
	if exhausted {
		t.Fatal("a reader that never progresses must not be reported as finished")
	}
}

// TestExhaustedReportsARealReadError distinguishes a broken source from a
// finished one. Both stop producing bytes; only one of them is complete.
func TestExhaustedReportsARealReadError(t *testing.T) {
	const content = "exactly this much"
	broken := io.MultiReader(strings.NewReader(content), &failingReader{})

	hasher := newCountingHasher(broken, int64(len(content)))
	if _, err := io.Copy(io.Discard, hasher); err != nil {
		t.Fatalf("read the stated size: %v", err)
	}

	exhausted, err := hasher.exhausted()
	if !errors.Is(err, errBrokenSource) {
		t.Fatalf("exhausted returned %v, want the source's own error", err)
	}
	if exhausted {
		t.Fatal("a source that failed was reported as finished")
	}
}

var errBrokenSource = errors.New("the source failed")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errBrokenSource }

// TestCountingHasherHashesOnlyWhatItWasLimitedTo pins the boundary between
// the two checks: the hash covers the uploaded prefix, and the probe that
// discovers the source is longer must not reach it. Otherwise a too-long
// source would fail as a content mismatch, describing the wrong problem.
func TestCountingHasherHashesOnlyWhatItWasLimitedTo(t *testing.T) {
	const stated = "the stated bytes"
	source := bytes.NewReader([]byte(stated + " plus a tail nobody asked for"))

	hasher := newCountingHasher(source, int64(len(stated)))
	if _, err := io.Copy(io.Discard, hasher); err != nil {
		t.Fatalf("read: %v", err)
	}
	before := hasher.digest()

	exhausted, err := hasher.exhausted()
	if err != nil {
		t.Fatalf("exhausted: %v", err)
	}
	if exhausted {
		t.Fatal("the source had more to give")
	}
	if hasher.digest() != before {
		t.Fatal("the probe byte reached the hash, so a too-long source would be reported as " +
			"the wrong kind of failure")
	}
	if hasher.read != int64(len(stated)) {
		t.Fatalf("counted %d bytes, want the stated %d", hasher.read, len(stated))
	}
}
