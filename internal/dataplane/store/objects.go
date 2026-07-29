package store

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

// Attachment is a stored object's row: the reference that makes the bytes
// reachable, and the digest that addresses them.
type Attachment struct {
	CreatedAt      time.Time
	Digest         string
	MediaType      string
	SizeBytes      int64
	AttachmentID   uuid.UUID
	OrganizationID uuid.UUID
}

// PutAttachmentInput is a request to store bytes and record them.
//
// The digest is CLAIMED, not derived: the caller says what the content
// should hash to and the seam proves it, which is what makes a mismatch a
// detectable error rather than a silent re-addressing of whatever arrived.
type PutAttachmentInput struct {
	Body           io.Reader
	Digest         string
	MediaType      string
	SizeBytes      int64
	OrganizationID uuid.UUID

	// AttachmentID may be preallocated so the caller can reference the row
	// it is about to create -- the cross-store commit order needs the id
	// before the transaction that writes it. Zero means allocate one.
	AttachmentID uuid.UUID
}

// Object-module errors. Each names a different failure a caller acts on
// differently, which is why none of them is a bare error string.
var (
	// ErrContentMismatch reports that the source did not hash to the
	// digest the caller claimed. The bytes are the caller's problem: either
	// the digest was computed over something else, or the source changed
	// under it.
	ErrContentMismatch = errors.New("content does not match the claimed digest")

	// ErrSizeMismatch reports a source longer or shorter than the stated
	// size. Distinguished from a digest mismatch because it is detectable
	// before the content is complete, and because size_bytes would
	// misreport stored volume forever.
	ErrSizeMismatch = errors.New("content length does not match the stated size")

	// ErrLeaseLost reports that a writer's staging lease expired or was
	// taken over before it could promote. The write is safe to retry; what
	// it must not do is promote anyway.
	ErrLeaseLost = errors.New("staging lease lost")

	// ErrCorruptObject reports that the object already at a digest key
	// does not hash to that digest. It is deliberately NOT the same as a
	// caller's content mismatch: nothing the caller sent is wrong, and the
	// store is contradicting itself.
	ErrCorruptObject = errors.New("stored object does not match the digest addressing it")
)

// Pin is one artifact's hold on one piece of evidence.
type Pin struct {
	AuditArtifactID *uuid.UUID
	AttachmentID    *uuid.UUID
	CreatedAt       time.Time
	Digest          string
	PinID           uuid.UUID
	OrganizationID  uuid.UUID
	HeldByArtifact  uuid.UUID
}

// EvidenceRef names one piece of evidence to pin. Exactly one target, the
// same exclusive arc the schema enforces.
type EvidenceRef struct {
	AuditArtifactID *uuid.UUID
	AttachmentID    *uuid.UUID
}

// AttachEvidenceInput is the supported way to create an artifact that
// references evidence.
//
// One call, one transaction: the attachments are written, then the draft
// artifact and its pins together. Describing that order in prose while
// leaving CreateManagementArtifact reachable with no pins at all would let
// any caller produce exactly the state the invariant forbids (design D5).
type AttachEvidenceInput struct {
	// Pins are what the artifact holds. Every reference the reviewed
	// payload names must appear here, and nothing else may: acceptance
	// compares the two as SETS, because an extra pin is an unreviewed
	// retention claim.
	Pins []EvidenceRef

	// Attachments are stored FIRST, each by the full PutAttachment path,
	// before the transaction that references them opens. They are ordinary
	// object writes: if the transaction below fails, they are unreferenced
	// garbage the sweep collects, never a dangling reference.
	Attachments []PutAttachmentInput

	// Artifact is the draft this evidence belongs to.
	Artifact CreateManagementArtifactInput
}

// AttachEvidenceResult is what one composite write produced.
type AttachEvidenceResult struct {
	Artifact    *ManagementArtifact
	Attachments []Attachment
	Pins        []Pin
}

// ObjectStore is the object module's seam surface (ADR 0022).
//
// It sits on Store and not on Tx, for the reason Maintenance does: every
// operation here makes REMOTE calls, and the write path holds a database
// transaction across a server-side copy and a read-back that it opens
// itself. Reached through a caller's transaction it could neither take the
// locks it needs in the order it needs them, nor bound them; a Tx
// advertising these would promise an operation it cannot honour.
type ObjectStore interface {
	// PutAttachment stores bytes at their digest and records the row that
	// makes them reachable.
	//
	// The content is proven locally: the source is hashed as it streams and
	// compared to the claimed digest, the length is checked by reading PAST
	// the stated size, and the promoted object is read back and hashed
	// before any row references it. An object already at the digest key is
	// verified the same way rather than trusted -- it is exactly where a
	// previously corrupted object would sit.
	PutAttachment(ctx context.Context, input PutAttachmentInput) (*Attachment, error)

	// GetAttachment streams an attachment's bytes, VERIFYING them.
	//
	// Verification can only complete at the end of the stream, so a digest
	// mismatch surfaces as a read error at EOF wrapping ErrInvariant --
	// never as a short read, and never after a caller has been handed a
	// complete-looking file. ADR 0021 requires an altered reference to fail
	// verification rather than quietly weaken a proof.
	GetAttachment(ctx context.Context, organizationID, attachmentID uuid.UUID) (io.ReadCloser, *Attachment, error)

	// AttachmentExists reports whether the row AND its object exist.
	//
	// Both, because the question acceptance asks is whether the evidence is
	// there. A row whose object is gone answers false with ErrInvariant --
	// the store contradicting itself -- while an unknown id is an ordinary
	// false with no error.
	AttachmentExists(ctx context.Context, organizationID, attachmentID uuid.UUID) (bool, error)

	// AttachEvidence stores objects and creates the draft artifact that
	// references them, with its pins, in one transaction.
	//
	// It is the supported path for evidence-bearing artifacts, and it
	// cannot be half-done: either the artifact exists with the complete pin
	// set the payload names, or nothing relational exists at all.
	AttachEvidence(ctx context.Context, input AttachEvidenceInput) (*AttachEvidenceResult, error)

	// Pin and Unpin maintain a DRAFT ORIGINAL's evidence set.
	//
	// Only a draft original: acceptance verifies the set, and a later
	// change would leave that verification true for an instant rather than
	// for the artifact's life. A draft AMENDMENT may not use them at all --
	// every pin in a chain is held by the original, so an amendment
	// pinning would mutate the accepted original's verified set before
	// anyone reviewed the amendment (design D5).
	Pin(ctx context.Context, organizationID, artifactID uuid.UUID, reference EvidenceRef) (*Pin, error)
	Unpin(ctx context.Context, organizationID, artifactID, pinID uuid.UUID) error

	// ListPins returns what an artifact holds.
	ListPins(ctx context.Context, organizationID, artifactID uuid.UUID) ([]Pin, error)
}
