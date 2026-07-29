//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// The object module's write and read paths (item 6 design, D2 and D4).
//
// The invariant is entirely about what happens when a step fails, so a
// happy-path test asserting the rows exist proves almost nothing. What each
// case below establishes is that a rejected write left NOTHING behind that
// a later reader could mistake for evidence.

const mediaType = "application/octet-stream"

func putInput(organizationID uuid.UUID, body []byte) store.PutAttachmentInput {
	return store.PutAttachmentInput{
		Body:           bytes.NewReader(body),
		Digest:         digestOf(body),
		MediaType:      mediaType,
		SizeBytes:      int64(len(body)),
		OrganizationID: organizationID,
	}
}

func TestPutAttachmentStoresAndReadsBack(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("evidence bytes worth pinning")

	attachment, err := f.store.PutAttachment(ctx, putInput(f.organizationID, body))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}
	if attachment.Digest != digestOf(body) || attachment.SizeBytes != int64(len(body)) {
		t.Fatalf("recorded %+v, want the digest and size of the source", attachment)
	}

	exists, err := f.store.AttachmentExists(ctx, f.organizationID, attachment.AttachmentID)
	if err != nil {
		t.Fatalf("AttachmentExists: %v", err)
	}
	if !exists {
		t.Fatal("the attachment this call just created does not exist")
	}

	reader, read, err := f.store.GetAttachment(ctx, f.organizationID, attachment.AttachmentID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	defer func() { _ = reader.Close() }()
	if read.MediaType != mediaType {
		t.Fatalf("read back media type %q, want %q", read.MediaType, mediaType)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("read back %q, want %q", got, body)
	}
}

// TestPutAttachmentIsIdempotentOverTheSameBytes covers the shortcut. The
// second write must reuse the stored object and still produce its own row:
// two artifacts may reference one object, and each needs a reference.
func TestPutAttachmentIsIdempotentOverTheSameBytes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("stored once, referenced twice")

	first, err := f.store.PutAttachment(ctx, putInput(f.organizationID, body))
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	second, err := f.store.PutAttachment(ctx, putInput(f.organizationID, body))
	if err != nil {
		t.Fatalf("second put: %v", err)
	}

	if first.AttachmentID == second.AttachmentID {
		t.Fatal("the second put returned the first row; each reference needs its own attachment")
	}
	if first.Digest != second.Digest {
		t.Fatal("two puts of identical bytes produced different digests")
	}
	// Both rows must read back, which is the property that matters: the
	// shortcut verified an object it did not upload.
	for _, attachment := range []*store.Attachment{first, second} {
		reader, _, getErr := f.store.GetAttachment(ctx, f.organizationID, attachment.AttachmentID)
		if getErr != nil {
			t.Fatalf("GetAttachment(%s): %v", attachment.AttachmentID, getErr)
		}
		got, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", attachment.AttachmentID, readErr)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("attachment %s reads back %q", attachment.AttachmentID, got)
		}
	}
}

// TestPutAttachmentRejectsAWrongDigest is the contract: the digest is the
// address, so a source that does not hash to it is refused before anything
// is promoted, and nothing is left at the address it claimed.
func TestPutAttachmentRejectsAWrongDigest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	input := putInput(f.organizationID, []byte("the actual bytes"))
	input.Digest = digestOf([]byte("something else entirely"))

	_, err := f.store.PutAttachment(ctx, input)
	if !errors.Is(err, store.ErrContentMismatch) {
		t.Fatalf("PutAttachment returned %v, want ErrContentMismatch", err)
	}
	f.assertNoAttachmentFor(t, input.Digest)
}

// TestPutAttachmentRejectsASourceLongerThanStated is the check that cannot
// be made by counting alone: the uploader stops at the stated size, so a
// longer source looks identical from inside the stream and would be stored
// as a silent truncation.
func TestPutAttachmentRejectsASourceLongerThanStated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("this source is longer than it claims")

	input := putInput(f.organizationID, body)
	input.SizeBytes = int64(len(body)) - 5
	// The digest is of the bytes the caller MEANT to store, which is what
	// makes this a size failure rather than a content one.
	input.Digest = digestOf(body[:len(body)-5])

	_, err := f.store.PutAttachment(ctx, input)
	if !errors.Is(err, store.ErrSizeMismatch) {
		t.Fatalf("PutAttachment returned %v, want ErrSizeMismatch", err)
	}
	f.assertNoAttachmentFor(t, input.Digest)
}

// TestPutAttachmentRejectsASourceShorterThanStated is the other direction.
func TestPutAttachmentRejectsASourceShorterThanStated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("short")

	input := putInput(f.organizationID, body)
	input.SizeBytes = int64(len(body)) + 100

	_, err := f.store.PutAttachment(ctx, input)
	if err == nil {
		t.Fatal("PutAttachment accepted a source shorter than its stated size")
	}
	f.assertNoAttachmentFor(t, input.Digest)
}

func TestPutAttachmentValidatesItsInput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("valid")

	for name, mutate := range map[string]func(*store.PutAttachmentInput){
		"digest is not hex":     func(i *store.PutAttachmentInput) { i.Digest = "not-a-digest" },
		"digest is uppercase":   func(i *store.PutAttachmentInput) { i.Digest = strings.ToUpper(i.Digest) },
		"digest is short":       func(i *store.PutAttachmentInput) { i.Digest = i.Digest[:63] },
		"media type is blank":   func(i *store.PutAttachmentInput) { i.MediaType = "  " },
		"media type is missing": func(i *store.PutAttachmentInput) { i.MediaType = "" },
		"size is negative":      func(i *store.PutAttachmentInput) { i.SizeBytes = -1 },
		"body is nil":           func(i *store.PutAttachmentInput) { i.Body = nil },
		"preallocated id is v4": func(i *store.PutAttachmentInput) { i.AttachmentID = uuid.New() },
	} {
		t.Run(name, func(t *testing.T) {
			input := putInput(f.organizationID, body)
			mutate(&input)
			if _, err := f.store.PutAttachment(ctx, input); err == nil {
				t.Fatal("PutAttachment accepted an input it must refuse")
			}
		})
	}
}

// TestGetAttachmentIsOrganizationScoped covers the tenant boundary: another
// organization's attachment id must be indistinguishable from one that
// never existed, or the seam becomes a probe for other tenants' records.
func TestGetAttachmentIsOrganizationScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	attachment, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("mine")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	_, _, err = f.store.GetAttachment(ctx, f.otherOrgID, attachment.AttachmentID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-organization read returned %v, want ErrNotFound", err)
	}
	exists, err := f.store.AttachmentExists(ctx, f.otherOrgID, attachment.AttachmentID)
	if err != nil {
		t.Fatalf("AttachmentExists: %v", err)
	}
	if exists {
		t.Fatal("another organization can see this attachment")
	}
}

func TestGetAttachmentReportsAMissingRow(t *testing.T) {
	f := newFixture(t)
	_, _, err := f.store.GetAttachment(context.Background(), f.organizationID, uuid.New())
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetAttachment of an unknown id returned %v, want ErrNotFound", err)
	}
}

// TestPutAttachmentSeparatesOrganizations covers the deliberate cost of
// organization-scoped keys: identical bytes in two organizations are two
// objects, so one organization deleting its copy can never affect the
// other's, and no organization can learn whether bytes are already stored.
func TestPutAttachmentSeparatesOrganizations(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("the same bytes in both tenants")

	mine, err := f.store.PutAttachment(ctx, putInput(f.organizationID, body))
	if err != nil {
		t.Fatalf("put in the first organization: %v", err)
	}
	theirs, err := f.store.PutAttachment(ctx, putInput(f.otherOrgID, body))
	if err != nil {
		t.Fatalf("put in the second organization: %v", err)
	}
	if mine.Digest != theirs.Digest {
		t.Fatal("identical bytes hashed differently")
	}

	// Two objects, at two keys. Reading each through its own organization
	// is the only access either has.
	for _, tenant := range []struct {
		organizationID uuid.UUID
		attachmentID   uuid.UUID
	}{{f.organizationID, mine.AttachmentID}, {f.otherOrgID, theirs.AttachmentID}} {
		reader, _, readErr := f.store.GetAttachment(ctx, tenant.organizationID, tenant.attachmentID)
		if readErr != nil {
			t.Fatalf("read in %s: %v", tenant.organizationID, readErr)
		}
		got, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("read in %s returned %q, %v", tenant.organizationID, got, err)
		}
	}
}

// TestPutAttachmentStoresAnEmptyObject covers the boundary the size checks
// bracket. Zero is a legal size -- the empty string has a digest like any
// other content -- and a check written as "size > 0" would refuse it.
func TestPutAttachmentStoresAnEmptyObject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	attachment, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte{}))
	if err != nil {
		t.Fatalf("PutAttachment of an empty object: %v", err)
	}
	reader, _, err := f.store.GetAttachment(ctx, f.organizationID, attachment.AttachmentID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read empty attachment: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty attachment read back %d bytes", len(got))
	}
}

// TestPutAttachmentLeavesNoStagingResidue is the cleanup contract on the
// success path: the lease is released and the staging object is deleted,
// so a later sweep finds nothing to collect.
func TestPutAttachmentLeavesNoStagingResidue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("clean up after me"))); err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	var leases int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM staging_leases WHERE organization_id = $1`,
		f.organizationID).Scan(&leases); err != nil {
		t.Fatalf("count leases: %v", err)
	}
	if leases != 0 {
		t.Fatalf("%d staging leases survived a completed write", leases)
	}
}

// assertNoAttachmentFor is the assertion every rejected write shares: no
// row references the digest the caller claimed. The bytes may or may not
// have reached staging -- that is what cleanup is for -- but nothing
// reachable may point at them.
func (f *fixture) assertNoAttachmentFor(t *testing.T, digest string) {
	t.Helper()
	var rows int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM binary_attachments WHERE organization_id = $1 AND object_digest = $2`,
		f.organizationID, digest).Scan(&rows); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if rows != 0 {
		t.Fatalf("%d attachment rows reference %s after a rejected write", rows, digest)
	}
}

// TestPutAttachmentRefusesACorruptObjectAtTheDigestKey is why the
// idempotent shortcut reads the object back instead of trusting its
// presence. The digest key is exactly where a previously corrupted or
// half-promoted object sits, and returning success would bless it into a
// new attachment row -- a reference to bytes that are not what they claim.
func TestPutAttachmentRefusesACorruptObjectAtTheDigestKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("the bytes this digest names")

	first, err := f.store.PutAttachment(ctx, putInput(f.organizationID, body))
	if err != nil {
		t.Fatalf("first put: %v", err)
	}

	// Corrupt the stored object beneath the seam, which is a state the
	// seam itself will not produce.
	f.corruptStoredObject(t, first.Digest, []byte("entirely different bytes"))

	_, err = f.store.PutAttachment(ctx, putInput(f.organizationID, body))
	if !errors.Is(err, store.ErrCorruptObject) {
		t.Fatalf("PutAttachment over a corrupt object returned %v, want ErrCorruptObject", err)
	}

	// Exactly one row still references the digest: the original. The
	// refused write added nothing.
	var rows int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM binary_attachments WHERE organization_id = $1 AND object_digest = $2`,
		f.organizationID, first.Digest).Scan(&rows); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if rows != 1 {
		t.Fatalf("%d attachment rows reference the digest, want only the original", rows)
	}
}

// TestGetAttachmentFailsAtEOFOnACorruptedObject is ADR 0021's requirement:
// evidence whose bytes have been replaced is worse than evidence that is
// missing, because it still reads as evidence.
//
// The failure arrives at EOF because that is the earliest instant it CAN,
// and it arrives as a read error so a caller copying the stream cannot
// mistake a corrupt object for a complete one.
func TestGetAttachmentFailsAtEOFOnACorruptedObject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("evidence that will be tampered with")

	attachment, err := f.store.PutAttachment(ctx, putInput(f.organizationID, body))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}
	f.corruptStoredObject(t, attachment.Digest, []byte("tampered evidence, same length!!!!!"))

	reader, _, err := f.store.GetAttachment(ctx, f.organizationID, attachment.AttachmentID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	defer func() { _ = reader.Close() }()

	_, err = io.ReadAll(reader)
	if !errors.Is(err, store.ErrInvariant) {
		t.Fatalf("reading a corrupted object returned %v, want ErrInvariant", err)
	}

	// And the failure is LATCHED: a caller that reads again must not be
	// told the second time that the stream ended cleanly.
	if _, again := reader.Read(make([]byte, 1)); !errors.Is(again, store.ErrInvariant) {
		t.Fatalf("a second read returned %v; the verification failure must persist", again)
	}
}

// TestGetAttachmentReportsAMissingObject covers the other half of a
// dangling reference: the row survives and the bytes do not.
func TestGetAttachmentReportsAMissingObject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	attachment, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("about to vanish")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}
	f.deleteStoredObject(t, attachment.Digest)

	_, _, err = f.store.GetAttachment(ctx, f.organizationID, attachment.AttachmentID)
	if !errors.Is(err, store.ErrInvariant) {
		t.Fatalf("GetAttachment of a vanished object returned %v, want ErrInvariant", err)
	}
	if errors.Is(err, store.ErrNotFound) {
		t.Fatal("a dangling reference must not read as an unknown attachment: the row exists")
	}
}

// corruptStoredObject replaces an object's bytes beneath the seam.
func (f *fixture) corruptStoredObject(t *testing.T, digest string, replacement []byte) {
	t.Helper()
	key := objectKeyFor(f.organizationID, digest)
	if _, err := f.blob.PutStaged(context.Background(), key,
		int64(len(replacement)), bytes.NewReader(replacement)); err != nil {
		t.Fatalf("corrupt %s: %v", key, err)
	}
}

// deleteStoredObject removes every version of an object beneath the seam.
func (f *fixture) deleteStoredObject(t *testing.T, digest string) {
	t.Helper()
	ctx := context.Background()
	key := objectKeyFor(f.organizationID, digest)
	versions, err := f.blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("list versions of %s: %v", key, err)
	}
	for _, version := range versions {
		if delErr := f.blob.DeleteVersion(ctx, version.Key, version.VersionID); delErr != nil {
			t.Fatalf("delete %s@%s: %v", version.Key, version.VersionID, delErr)
		}
	}
}

// objectKeyFor mirrors the seam's key layout (design D3). Duplicated here
// deliberately: a test that computed the key by calling the code under test
// would agree with it however wrong both were.
func objectKeyFor(organizationID uuid.UUID, digest string) string {
	return organizationID.String() + "/" + digest[:2] + "/" + digest[2:4] + "/" + digest
}
