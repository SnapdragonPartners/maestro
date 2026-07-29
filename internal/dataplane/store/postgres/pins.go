package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// Rejection reasons specific to pinning. They are separate constants
// because the operator response differs: one means "accept it first", the
// other means "amend the original, do not pin from here".
const (
	transitionPin            = "pin"
	transitionUnpin          = "unpin"
	transitionAttachEvidence = "attach evidence"
)

// Pin adds one reference to a draft original's evidence set.
func (s *Store) Pin(
	ctx context.Context, organizationID, artifactID uuid.UUID, reference store.EvidenceRef,
) (*store.Pin, error) {
	return inTx(ctx, s, func(t *tx) (*store.Pin, error) {
		if err := t.requirePinnableHolder(ctx, transitionPin, organizationID, artifactID); err != nil {
			return nil, err
		}
		return t.pin(ctx, organizationID, artifactID, reference)
	})
}

// Unpin removes one reference from a draft original's evidence set.
func (s *Store) Unpin(ctx context.Context, organizationID, artifactID, pinID uuid.UUID) error {
	_, err := inTx(ctx, s, func(t *tx) (struct{}, error) {
		if err := t.requirePinnableHolder(ctx, transitionUnpin, organizationID, artifactID); err != nil {
			return struct{}{}, err
		}
		removed, err := t.queries.DeletePin(ctx, gen.DeletePinParams{
			OrganizationID:     toUUID(organizationID),
			RetentionPinID:     toUUID(pinID),
			PinnedByArtifactID: toUUID(artifactID),
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("remove pin %s: %w", pinID, err)
		}
		if removed == 0 {
			return struct{}{}, fmt.Errorf("%w: pin %s held by artifact %s", store.ErrNotFound, pinID, artifactID)
		}
		return struct{}{}, nil
	})
	return err
}

// ListPins returns what an artifact holds.
func (s *Store) ListPins(ctx context.Context, organizationID, artifactID uuid.UUID) ([]store.Pin, error) {
	rows, err := s.queries.ListPinsByArtifact(ctx, gen.ListPinsByArtifactParams{
		OrganizationID:     toUUID(organizationID),
		PinnedByArtifactID: toUUID(artifactID),
	})
	if err != nil {
		return nil, fmt.Errorf("list pins for artifact %s: %w", artifactID, err)
	}
	pins := make([]store.Pin, 0, len(rows))
	for i := range rows {
		pins = append(pins, pinFromRow(&rows[i]))
	}
	return pins, nil
}

// requirePinnableHolder locks the holding artifact and classifies it.
//
// Pins are mutable only while their holder is a draft ORIGINAL (design D5).
// Two separate rules, refused with two separate reasons:
//
//   - an ACCEPTED or superseded artifact's set was verified at acceptance,
//     and permitting a later change would make that verification true for
//     an instant rather than for the artifact's life;
//   - an AMENDMENT may not pin at all, even as a draft. Every pin in a
//     chain is held by the original, so a draft amendment pinning would
//     mutate the accepted original's verified set before anyone reviewed
//     the amendment -- and invalidating that draft afterwards could not
//     identify which of the original's pins came from it, because a pin
//     records its holder and not its proposer.
//
// Locked and classified in Go, the shape item 4 uses for every transition:
// a rowcount carries no reason, and the caller needs one.
func (t *tx) requirePinnableHolder(
	ctx context.Context, transition string, organizationID, artifactID uuid.UUID,
) error {
	artifact, err := t.queries.LockManagementArtifact(ctx, gen.LockManagementArtifactParams{
		ArtifactID:     toUUID(artifactID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return notFound(err, "management artifact", artifactID)
	}
	if artifact.AmendsArtifactID.Valid {
		return rejected(transition, artifactID, store.ReasonIsAmendment,
			"all pins in a chain are held by the original")
	}
	if store.Status(artifact.Status) != store.StatusDraft {
		return rejected(transition, artifactID, store.ReasonWrongStatus,
			fmt.Sprintf("status is %q; a verified pin set is not editable afterwards", artifact.Status))
	}
	return nil
}

// pin writes one pin, binding the digest of whatever it points at.
//
// The digest is READ HERE rather than accepted from the caller: a pin
// recording a digest its target does not have protects nothing, and a
// caller supplying both would be asserting the very thing acceptance later
// checks. It is read under the same transaction as the insert, so the
// target cannot change between them.
//
// Used by the public Pin and by AttachEvidence. Lifecycle transitions do
// their own removal through internal queries instead, so the draft-only
// rule above is not something they have to be exempted from.
func (t *tx) pin(
	ctx context.Context, organizationID, artifactID uuid.UUID, reference store.EvidenceRef,
) (*store.Pin, error) {
	digest, err := t.evidenceDigest(ctx, organizationID, reference)
	if err != nil {
		return nil, err
	}
	pinID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return nil, err
	}

	row, err := t.queries.CreatePin(ctx, gen.CreatePinParams{
		RetentionPinID:        toUUID(pinID),
		OrganizationID:        toUUID(organizationID),
		PinnedByArtifactID:    toUUID(artifactID),
		PinnedAuditArtifactID: toNullUUID(reference.AuditArtifactID),
		PinnedAttachmentID:    toNullUUID(reference.AttachmentID),
		PinnedDigest:          digest,
	})
	if err != nil {
		return nil, mapPinViolation(err, reference)
	}
	pin := pinFromRow(&row)
	return &pin, nil
}

// evidenceDigest reads the digest of the thing a reference names, and
// refuses a reference that does not name exactly one thing.
func (t *tx) evidenceDigest(
	ctx context.Context, organizationID uuid.UUID, reference store.EvidenceRef,
) (string, error) {
	switch {
	case reference.AuditArtifactID != nil && reference.AttachmentID != nil:
		return "", errors.New("a reference names both an Audit artifact and an attachment; a pin has " +
			"exactly one target, and one naming two describes nothing the schema can hold")
	case reference.AuditArtifactID != nil:
		digest, err := t.queries.GetAuditArtifactDigest(ctx, gen.GetAuditArtifactDigestParams{
			ArtifactID:     toUUID(*reference.AuditArtifactID),
			OrganizationID: toUUID(organizationID),
		})
		if err != nil {
			return "", notFound(err, "audit artifact", *reference.AuditArtifactID)
		}
		return digest, nil
	case reference.AttachmentID != nil:
		digest, err := t.queries.GetAttachmentDigest(ctx, gen.GetAttachmentDigestParams{
			AttachmentID:   toUUID(*reference.AttachmentID),
			OrganizationID: toUUID(organizationID),
		})
		if err != nil {
			return "", notFound(err, "attachment", *reference.AttachmentID)
		}
		return digest, nil
	default:
		return "", errors.New("a reference names nothing; a pin must point at an Audit artifact or " +
			"an attachment")
	}
}

// The foreign key a pin on an attachment must satisfy, and the SQLSTATE
// its absence raises. Matched by name and code, since message text is not
// a contract.
const (
	attachmentPinConstraint = "retention_pins_attachment_fkey"
	foreignKeyViolation     = "23503"
)

// mapPinViolation turns the schema's refusal into something a caller can
// act on.
//
// A 23503 on the attachment key means the attachment is gone -- truncated
// between the digest read and this insert -- and surfacing a constraint
// name to a caller who cannot act on it is not a diagnostic (design D6a).
func mapPinViolation(err error, reference store.EvidenceRef) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation &&
		pgErr.ConstraintName == attachmentPinConstraint && reference.AttachmentID != nil {
		return fmt.Errorf("%w: attachment %s no longer exists; it was truncated while this pin was "+
			"being written", store.ErrNotFound, *reference.AttachmentID)
	}
	return fmt.Errorf("record pin: %w", err)
}

func pinFromRow(row *gen.RetentionPin) store.Pin {
	return store.Pin{
		CreatedAt:       fromTimestamptz(row.CreatedAt),
		Digest:          row.PinnedDigest,
		PinID:           fromUUID(row.RetentionPinID),
		OrganizationID:  fromUUID(row.OrganizationID),
		HeldByArtifact:  fromUUID(row.PinnedByArtifactID),
		AuditArtifactID: fromNullUUID(row.PinnedAuditArtifactID),
		AttachmentID:    fromNullUUID(row.PinnedAttachmentID),
	}
}

// AttachEvidence is the supported path for an artifact that cites evidence
// (design D5).
//
// The order is ADR 0022's, as amended by this item: the objects land first,
// then their attachment rows, then the referencing artifact and its pins
// TOGETHER in one transaction -- and the artifact becomes authoritative
// only on acceptance, which verifies that every referenced object exists
// and every pin matches its target's digest.
//
// Describing that order was not enough. CreateManagementArtifact stays
// reachable with no pins at all, so any caller could produce exactly the
// state the invariant forbids; this is the operation that cannot be
// half-done, and acceptance is the precondition that refuses the halves.
//
// What a failure leaves behind is deliberately asymmetric. Objects written
// before a failed transaction are unreferenced garbage the sweep collects.
// A dangling REFERENCE is what must never exist, and cannot: the artifact
// and its pins commit together or not at all.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) AttachEvidence(
	ctx context.Context, input store.AttachEvidenceInput,
) (*store.AttachEvidenceResult, error) {
	// Checked BEFORE anything is stored. Every failure below is a caller
	// error that no amount of writing will fix, and refusing after the
	// uploads would leave objects and attachment rows behind for a request
	// that was never going to succeed.
	if err := checkAttachEvidence(&input); err != nil {
		return nil, err
	}

	attachments := make([]store.Attachment, 0, len(input.Attachments))
	for i := range input.Attachments {
		stored, err := s.PutAttachment(ctx, input.Attachments[i])
		if err != nil {
			return nil, fmt.Errorf("store evidence %d of %d: %w", i+1, len(input.Attachments), err)
		}
		attachments = append(attachments, *stored)
	}

	return inTx(ctx, s, func(t *tx) (*store.AttachEvidenceResult, error) {
		artifact, err := t.CreateManagementArtifact(ctx, input.Artifact)
		if err != nil {
			return nil, err
		}

		pins := make([]store.Pin, 0, len(input.Pins))
		for i := range input.Pins {
			// t.pin, not the public Pin: this artifact was created in THIS
			// transaction, as a draft, and checkAttachEvidence refused an
			// amendment before any of it began -- so the classification
			// requirePinnableHolder performs has already been established
			// by construction. It is established by that check, not by the
			// creation: CreateManagementArtifact will happily create an
			// amendment if asked.
			written, pinErr := t.pin(ctx, artifact.OrganizationID, artifact.ArtifactID, input.Pins[i])
			if pinErr != nil {
				return nil, fmt.Errorf("pin evidence %d of %d: %w", i+1, len(input.Pins), pinErr)
			}
			pins = append(pins, *written)
		}

		return &store.AttachEvidenceResult{
			Artifact:    artifact,
			Attachments: attachments,
			Pins:        pins,
		}, nil
	})
}

// releasePins drops everything an artifact holds, as part of the
// transition that ends its claim.
//
// It goes through the internal query rather than the public Unpin, so the
// draft-only rule is not something a lifecycle transition has to be
// exempted from -- and so the removal happens in the transition's own
// transaction, atomic with the status change.
//
// Removing nothing is not an error: an artifact that cited no evidence has
// no pins to release, and requiring at least one would make every
// evidence-free artifact unarchivable.
func (t *tx) releasePins(ctx context.Context, transition string, organizationID, artifactID uuid.UUID) error {
	if _, err := t.queries.DeletePinsByArtifact(ctx, gen.DeletePinsByArtifactParams{
		OrganizationID:     toUUID(organizationID),
		PinnedByArtifactID: toUUID(artifactID),
	}); err != nil {
		return fmt.Errorf("release pins on %s of artifact %s: %w", transition, artifactID, err)
	}
	return nil
}

// checkAttachEvidence refuses a composite request that could not produce a
// coherent result, before any of it is written.
//
// Three rules, each closing a hole the happy path does not show:
//
//   - The artifact may not be an AMENDMENT. Every pin in a chain is held
//     by the original, and this path reaches t.pin directly, so an
//     amendment here would write pins that no artifact can ever release:
//     item 4 refuses to archive an amendment at all, and archiving the
//     original removes only the original's own pins.
//   - Every attachment must carry a PREALLOCATED UUIDv7. An attachment
//     whose id is allocated during the write gets it after the payload and
//     the pins were built, so nothing could have named it -- the caller
//     would be asking to store evidence it cannot reference.
//   - Every attachment must be PINNED by this artifact, and belong to its
//     organization. An attachment stored here and left unpinned is a
//     durable row the artifact never references, holding an object the
//     sweep cannot reclaim until something truncates the row.
func checkAttachEvidence(input *store.AttachEvidenceInput) error {
	if input.Artifact.AmendsArtifactID != nil {
		return &store.TransitionRejected{
			Transition: transitionAttachEvidence,
			ArtifactID: input.Artifact.ArtifactID,
			Reason:     store.ReasonIsAmendment,
			Detail: "an amendment cannot hold pins; its additions are written to the original by " +
				"amendment acceptance",
		}
	}

	pinnedHere := make(map[uuid.UUID]struct{}, len(input.Pins))
	for _, reference := range input.Pins {
		if reference.AttachmentID != nil {
			pinnedHere[*reference.AttachmentID] = struct{}{}
		}
	}

	for i := range input.Attachments {
		attachment := &input.Attachments[i]
		switch {
		case attachment.AttachmentID == uuid.Nil:
			return fmt.Errorf("attachment %d of %d has no preallocated id: the payload that references "+
				"it and the pins that protect it are both built before this call, so an id allocated "+
				"during the write is one nothing could have named",
				i+1, len(input.Attachments))
		case attachment.AttachmentID.Version() != 7:
			return fmt.Errorf("attachment %d of %d has a UUID version %d id, want 7",
				i+1, len(input.Attachments), attachment.AttachmentID.Version())
		case attachment.OrganizationID != input.Artifact.OrganizationID:
			return fmt.Errorf("attachment %d of %d belongs to organization %s and the artifact to %s; "+
				"evidence and the artifact citing it are always in one organization",
				i+1, len(input.Attachments), attachment.OrganizationID, input.Artifact.OrganizationID)
		}
		if _, pinned := pinnedHere[attachment.AttachmentID]; !pinned {
			return fmt.Errorf("attachment %d of %d (%s) is stored by this call and pinned by none of "+
				"its pins: it would be a durable row the artifact never references, holding an object "+
				"nothing can reclaim until the row is truncated",
				i+1, len(input.Attachments), attachment.AttachmentID)
		}
	}
	return nil
}
