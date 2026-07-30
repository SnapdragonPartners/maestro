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
	// Every attachment must carry a preallocated UUIDv7 id, belong to the
	// artifact's organization, and be named by one of the Pins below. An
	// id allocated during the write could not have been referenced by a
	// payload built beforehand, and an attachment stored here but unpinned
	// is a durable row the artifact never references.
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
	// It is the supported path for evidence-bearing artifacts. What is
	// atomic is the ARTIFACT AND ITS PINS: either the artifact exists
	// holding the complete set, or neither exists.
	//
	// The attachments are not part of that transaction and cannot be. Each
	// is a full PutAttachment -- an object write followed by a committed
	// row -- performed before the transaction opens, because the pins have
	// to name rows that already exist. So a failure here DOES leave
	// attachment rows behind, along with their objects. That residue is
	// unreferenced rather than dangling, which is the distinction that
	// matters: nothing authoritative points at it. Reclaiming it takes two
	// steps in order -- attachment truncation removes the rows, which makes
	// the objects unreachable, and only then can the object sweep collect
	// them.
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

	// CleanUpStaging releases staging objects whose writers are gone.
	//
	// It never removes work that is still in progress. Expiry only decides
	// which leases it may CONSIDER; the row lock decides who acts first
	// when a lease has expired while its writer is still running, and
	// cleanup takes the same lock a promotion holds. ADR 0027: destructive
	// recovery must never remove another actor's in-progress work, and
	// "the victim finds out" is not a mitigation.
	//
	// Idempotent and re-runnable. It is bounded per pass, so a backlog is
	// cleared over several rather than under one long-held set of locks.
	CleanUpStaging(ctx context.Context, organizationID uuid.UUID) (StagingCleanup, error)

	// DeleteUnpinned reclaims the storage of digests nothing references.
	//
	// The reachable set is exactly the attachment rows, so this runs AFTER
	// attachment truncation has removed them: truncation makes an object
	// unreachable, and this makes it gone. The two cannot be one step,
	// because truncation runs under one REPEATABLE READ snapshot and object
	// deletion cannot participate in a snapshot (design D6a).
	//
	// It is coordinated, not timed. Writers and the sweep serialise per
	// (organization, digest) on an advisory lock, and "unreferenced" is
	// established under that lock -- in mutual exclusion with the commit
	// that would make it referenced. The grace period below the lock is
	// defence in depth and nothing more: age cannot prove abandonment, and
	// the design rejected it as the mechanism.
	//
	// Because the deletes are REMOTE, the lock is necessary and not
	// sufficient: a connection can die with a delete in flight, releasing
	// the lock without cancelling anything. So the intent is made durable
	// first, in a deletion claim naming the exact version and upload ids
	// observed under the lock, and every delete names one of them. A late
	// arrival removes something already condemned and nothing else.
	//
	// Bounded per pass and re-runnable: candidates are discovered by their
	// own residue, so a deferred remainder is the next pass's rather than
	// lost.
	DeleteUnpinned(ctx context.Context, organizationID uuid.UUID) (ObjectSweep, error)

	// ReconcileDeletionClaims finishes deletes an earlier actor could not.
	//
	// A claim survives a crash between recording the intent and clearing it,
	// and that row is the only record of storage that was condemned but may
	// not be gone. Re-issuing a version-specific delete is harmless by
	// construction, which is what makes this safe to run at any time -- and
	// it runs at `dataplane-up`, because a surviving claim also forbids the
	// existing-object shortcut for its digest until it clears.
	//
	// It is the ONLY actor besides a claim's owner that may clear one.
	// Writers never clear or take over another actor's claim: intent is not
	// a fence, and the original delete may still be in flight.
	//
	// Not organization-scoped, unlike everything else on this seam. A claim
	// is the plane's own record of an unfinished intention rather than a
	// tenant's data, and no tenant can ask for this. Every lock and delete
	// it issues is scoped by the claim's own organization.
	ReconcileDeletionClaims(ctx context.Context) (ClaimReconciliation, error)
}

// ObjectSweep reports one sweep pass.
//
// The five counts answer different questions, and the two DEFERRED ones are
// not failures. A digest deferred because a reference appeared is the lock
// protocol working; a digest deferred because its residue is young is the
// grace period working. Folding either into a total would make the pass
// that protected an in-flight write indistinguishable from the pass that
// found nothing to do.
type ObjectSweep struct {
	// DigestsReclaimed counts digests condemned, deleted and cleared.
	DigestsReclaimed int
	// VersionsDeleted and UploadsAborted count the storage actually named.
	// Both, because they are separate storage states and neither is
	// visible to the other's vocabulary.
	VersionsDeleted int
	UploadsAborted  int

	// DeferredReferenced counts digests that turned out to be referenced
	// once the lock was granted -- the recheck doing its job. Digests already
	// known to be referenced before the pass took any lock are NOT counted:
	// in a healthy store that is nearly every object, so the number would
	// report the size of the bucket rather than anything about this pass.
	DeferredReferenced int
	// DeferredYoung counts digests whose residue was inside the grace
	// period.
	DeferredYoung int
	// DeferredClaimed counts digests an earlier pass had already condemned
	// and not finished, whether that was known before the lock or discovered
	// under it. Their completion belongs to that pass's owner or to the
	// reconciler; a second claim over the same storage would fence neither
	// attempt.
	//
	// Counted from BOTH places, unlike DeferredReferenced, because this
	// number means something an operator may have to act on: a claim is
	// unfinished recovery work, and one that persists across passes is
	// storage nothing is reclaiming.
	DeferredClaimed int
	// DeferredForNextPass counts candidates the pass's own bound left
	// behind. Reported rather than only logged: a bound that drops work
	// silently reads as "there was nothing more to do".
	DeferredForNextPass int
}

// ClaimReconciliation reports one recovery pass over surviving claims.
type ClaimReconciliation struct {
	ClaimsCleared   int
	VersionsDeleted int
	UploadsAborted  int
}

// StagingCleanup reports one cleanup pass.
//
// The two counts mean different things. A released lease is an abandoned
// writer collected, which is routine. A collected ORPHAN is residue that
// outlived its own discovery record -- a paused writer that resumed after
// its lease was removed, or an upload that completed between cleanup's two
// enumerations -- so a non-zero count says something went wrong earlier and
// is worth an operator's attention rather than being folded into a total.
type StagingCleanup struct {
	LeasesReleased   int
	OrphansCollected int
}
