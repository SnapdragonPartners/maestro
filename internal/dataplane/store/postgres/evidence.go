package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
)

// Rejection reasons for the evidence preconditions (design D5).
//
// Four rules, four reasons, because the operator response differs for each:
// pin what the payload names, drop what it does not, fix a pin that binds
// the wrong digest, or restore an object that is gone.
const (
	// ReasonEvidenceUnpinned is the payload naming evidence with no pin.
	ReasonEvidenceUnpinned store.RejectionReason = "reviewed payload names evidence that is not pinned"

	// ReasonPinUnreviewed is a pin the payload does not name. It is not a
	// harmless extra: an artifact's pins are a retention claim, and one
	// nobody reviewed is storage held on nobody's authority.
	ReasonPinUnreviewed store.RejectionReason = "a pin names evidence the reviewed payload does not"

	// ReasonPinDigestMismatch is a pin bound to a digest its target does
	// not have. Such a pin protects nothing the artifact cites.
	ReasonPinDigestMismatch store.RejectionReason = "a pin's digest does not match its target"

	// ReasonEvidenceMissing is a referenced object that is not in the
	// store. This is the one precondition that reaches outside Postgres.
	ReasonEvidenceMissing store.RejectionReason = "referenced evidence is not stored"
)

// checkEvidence is the acceptance precondition on an artifact's evidence.
//
// The expected set comes from the REVIEWED PAYLOAD, never from the pins.
// Deriving it from the pins would be circular -- they would be checked
// against themselves -- and it would let payload B be reviewed while
// attachment A is pinned. ADR 0028's review digest covers the whole
// reviewable envelope including the payload, so a set derived from the
// payload is a set the reviewer saw.
//
// The comparison is SET EQUALITY, not containment. Containment catches the
// missing pin and misses the extra one, and an extra pin is an unreviewed
// retention claim.
//
// It runs under the artifact's row lock, taken by the caller before it
// classified the transition, so the pins cannot change beneath it. The
// object-existence check is the one step that reaches outside Postgres, and
// it is safe in this order because the attachment row already exists and
// the sweep's reachable set is exactly the attachment rows -- between the
// check and the commit there is nothing that may delete the object.
func (t *tx) checkEvidence(
	ctx context.Context, transition string, artifact *gen.ManagementArtifact, payload []byte,
) error {
	expected, err := t.expectedReferences(artifact, payload)
	if err != nil {
		return err
	}

	held, err := t.queries.ListPinsByArtifact(ctx, gen.ListPinsByArtifactParams{
		OrganizationID:     artifact.OrganizationID,
		PinnedByArtifactID: artifact.ArtifactID,
	})
	if err != nil {
		return fmt.Errorf("read pins for artifact %s: %w", fromUUID(artifact.ArtifactID), err)
	}

	artifactID := fromUUID(artifact.ArtifactID)
	organizationID := fromUUID(artifact.OrganizationID)

	// Which targets are pinned, for the missing-reference check below.
	// Duplicates collapse here, and that is correct: the design compares
	// SETS, and two pins on one target name the same member twice.
	pinnedTargets := make(map[evidenceKey]struct{}, len(held))
	for i := range held {
		pinnedTargets[keyOfPin(&held[i])] = struct{}{}
	}

	// Every expected reference is pinned by something.
	wanted := sortedKeys(expected)
	for i := range wanted {
		if _, ok := pinnedTargets[wanted[i]]; !ok {
			return rejected(transition, artifactID, ReasonEvidenceUnpinned, wanted[i].String())
		}
	}

	// And EVERY held row is checked -- every row, not one per target.
	//
	// Nothing forbids two pins on one target: the schema has no uniqueness
	// over (holder, target), the public Pin will write a second, and the
	// design's set comparison is indifferent to the duplication. But a
	// map keyed by target keeps only the LAST row for each, and this
	// listing is ordered, so a correctly bound duplicate reliably hides a
	// corrupted one -- acceptance would verify a pin it never looked at.
	//
	// Forbidding duplicates with a unique constraint was the alternative.
	// It is not this item's decision to make: the accepted design compares
	// sets and says nothing against a redundant pin, and the check has to
	// be right for the rows that exist either way.
	targets := newTargetCache()
	for i := range held {
		pin := &held[i]
		key := keyOfPin(pin)
		if _, expectedIt := expected[key]; !expectedIt {
			return rejected(transition, artifactID, ReasonPinUnreviewed, key.String())
		}
		if err := t.checkPinTarget(ctx, transition, artifactID, organizationID, key, pin, targets); err != nil {
			return err
		}
	}
	return nil
}

// targetCache remembers what each referenced target actually is, so a
// target pinned twice costs one lookup and one existence check rather than
// two of each. The pins are still compared individually; only the reads
// they compare against are shared.
type targetCache struct {
	digests map[evidenceKey]string
	stored  map[evidenceKey]bool
}

func newTargetCache() *targetCache {
	return &targetCache{digests: map[evidenceKey]string{}, stored: map[evidenceKey]bool{}}
}

// checkPinTarget verifies one pin against what it claims to protect.
func (t *tx) checkPinTarget(
	ctx context.Context, transition string, artifactID, organizationID uuid.UUID,
	want evidenceKey, pin *gen.RetentionPin, targets *targetCache,
) error {
	actual, cached := targets.digests[want]
	if !cached {
		read, err := t.evidenceDigest(ctx, organizationID, want.reference())
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return rejected(transition, artifactID, ReasonEvidenceMissing, want.String())
			}
			return err
		}
		actual = read
		targets.digests[want] = read
	}
	if pin.PinnedDigest != actual {
		return rejected(transition, artifactID, ReasonPinDigestMismatch,
			fmt.Sprintf("%s is pinned at %s but is %s", want, pin.PinnedDigest, actual))
	}

	// Attachments reach outside Postgres: the row proves a reference, not
	// bytes. Audit artifacts are rows, and the digest comparison above is
	// the whole of their check.
	if want.attachment != uuid.Nil {
		stored, cached := targets.stored[want]
		if !cached {
			present, err := t.blob.Exists(ctx, objectKey(organizationID, actual))
			if err != nil {
				return fmt.Errorf("check stored object for %s: %w", want, err)
			}
			stored = present
			targets.stored[want] = present
		}
		if !stored {
			return rejected(transition, artifactID, ReasonEvidenceMissing, want.String())
		}
	}
	return nil
}

// expectedReferences is what the reviewed payload names.
func (t *tx) expectedReferences(
	artifact *gen.ManagementArtifact, payload []byte,
) (map[evidenceKey]struct{}, error) {
	extractor, registered, err := t.registry.ExtractorFor(
		registry.Type(artifact.ArtifactType), int(artifact.SchemaVersion))
	if err != nil {
		return nil, fmt.Errorf("look up reference extractor: %w", err)
	}
	// No extractor means the type carries no evidence, and acceptance
	// therefore requires exactly zero pins. Treating it as "cannot tell"
	// would wave through an unreviewed retention claim on every type nobody
	// had got round to registering.
	if !registered {
		return map[evidenceKey]struct{}{}, nil
	}

	references, err := extractor.References(payload)
	if err != nil {
		return nil, fmt.Errorf("extract references from artifact %s: %w",
			fromUUID(artifact.ArtifactID), err)
	}

	expected := make(map[evidenceKey]struct{}, len(references))
	for _, reference := range references {
		key, err := keyOfReference(store.EvidenceRef(reference))
		if err != nil {
			return nil, fmt.Errorf("artifact %s: %w", fromUUID(artifact.ArtifactID), err)
		}
		expected[key] = struct{}{}
	}
	return expected, nil
}

// evidenceKey identifies one piece of evidence for set comparison.
//
// A comparable struct rather than the pointer-bearing reference type, so
// two references to the same thing are the same map key. Exactly one field
// is set, matching the schema's exclusive arc.
type evidenceKey struct {
	audit      uuid.UUID
	attachment uuid.UUID
}

func keyOfReference(reference store.EvidenceRef) (evidenceKey, error) {
	switch {
	case reference.AuditArtifactID != nil && reference.AttachmentID != nil:
		return evidenceKey{}, errors.New("a reference names both an Audit artifact and an attachment")
	case reference.AuditArtifactID != nil:
		return evidenceKey{audit: *reference.AuditArtifactID}, nil
	case reference.AttachmentID != nil:
		return evidenceKey{attachment: *reference.AttachmentID}, nil
	default:
		return evidenceKey{}, errors.New("a reference names nothing")
	}
}

func keyOfPin(pin *gen.RetentionPin) evidenceKey {
	if pin.PinnedAuditArtifactID.Valid {
		return evidenceKey{audit: fromUUID(pin.PinnedAuditArtifactID)}
	}
	return evidenceKey{attachment: fromUUID(pin.PinnedAttachmentID)}
}

func (k evidenceKey) reference() store.EvidenceRef {
	if k.audit != uuid.Nil {
		id := k.audit
		return store.EvidenceRef{AuditArtifactID: &id}
	}
	id := k.attachment
	return store.EvidenceRef{AttachmentID: &id}
}

func (k evidenceKey) String() string {
	if k.audit != uuid.Nil {
		return "audit artifact " + k.audit.String()
	}
	return "attachment " + k.attachment.String()
}

// sortedKeys gives the checks above a stable order, so the reason an
// acceptance was refused does not depend on map iteration.
func sortedKeys[V any](set map[evidenceKey]V) []evidenceKey {
	keys := make([]evidenceKey, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].String() < keys[j].String()
	})
	return keys
}
