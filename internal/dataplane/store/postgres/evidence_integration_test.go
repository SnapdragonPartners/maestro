//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// The evidence half of the object module (item 6 design, D5).
//
// The invariant is that no ACCEPTED artifact ever references an object that
// is missing or unpinned, and that its pin set is exactly what the reviewer
// saw. Steps before acceptance may leave removable garbage; what they may
// never leave is a dangling authoritative reference.

// evidenceType is a Management type that carries evidence, so it registers
// an extractor. testType deliberately does not: a type with no extractor
// carries no evidence and must accept with zero pins, which is the default
// every other test in this package relies on.
const evidenceType registry.Type = "evidence_spec"

// evidencePayload is the shape the extractor below reads.
type evidencePayload struct {
	Title       string      `json:"title"`
	Attachments []uuid.UUID `json:"attachments"`
	AuditRefs   []uuid.UUID `json:"audit_refs"`
}

// evidenceExtractor is the per-type, per-version reference extractor. It
// reads the PAYLOAD, which is what the review digest covers -- a set
// derived from the pins would be checked against itself.
func evidenceExtractor() registry.Extractor {
	return registry.ExtractorFunc(func(payload []byte) ([]registry.Reference, error) {
		var decoded evidencePayload
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return nil, err
		}
		references := make([]registry.Reference, 0, len(decoded.Attachments)+len(decoded.AuditRefs))
		for i := range decoded.Attachments {
			references = append(references, registry.Reference{AttachmentID: &decoded.Attachments[i]})
		}
		for i := range decoded.AuditRefs {
			references = append(references, registry.Reference{AuditArtifactID: &decoded.AuditRefs[i]})
		}
		return references, nil
	})
}

func evidenceRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	built, err := registry.New(map[registry.Type]registry.Entry{
		evidenceType: {
			Category:       registry.CategoryManagement,
			CurrentVersion: 1,
			Validators:     map[int]registry.Validator{1: requireTitle()},
			Extractors:     map[int]registry.Extractor{1: evidenceExtractor()},
		},
		testType: {
			Category:       registry.CategoryManagement,
			CurrentVersion: 1,
			Validators:     map[int]registry.Validator{1: requireTitle()},
		},
		"test_event": {
			Category:       registry.CategoryAudit,
			CurrentVersion: 1,
			Validators:     map[int]registry.Validator{1: requireTitle()},
		},
	})
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	return built
}

// evidenceFixture is a store whose registry knows an evidence-bearing
// type. The base fixture's registry has no extractor for anything, which
// is the right default -- and useless for testing what an extractor does.
func evidenceFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	built, err := postgres.New(f.pool, evidenceRegistry(t), f.blob)
	if err != nil {
		t.Fatalf("store with an evidence registry: %v", err)
	}
	f.store = built
	return f
}

// attachEvidence stores one attachment and creates the draft that cites it.
func (f *fixture) attachEvidence(t *testing.T, body []byte) *store.AttachEvidenceResult {
	t.Helper()
	attachmentID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate attachment id: %v", err)
	}
	payload, err := json.Marshal(evidencePayload{
		Title:       "an artifact with evidence",
		Attachments: []uuid.UUID{attachmentID},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result, err := f.store.AttachEvidence(context.Background(), store.AttachEvidenceInput{
		Attachments: []store.PutAttachmentInput{{
			Body:           bytes.NewReader(body),
			Digest:         digestOf(body),
			MediaType:      mediaType,
			SizeBytes:      int64(len(body)),
			OrganizationID: f.organizationID,
			AttachmentID:   attachmentID,
		}},
		Artifact: store.CreateManagementArtifactInput{
			Payload:          payload,
			Type:             evidenceType,
			Summary:          "an artifact with evidence",
			Scope:            f.scope(),
			OrganizationID:   f.organizationID,
			UserID:           f.userID,
			AuthorInstanceID: f.author,
		},
		Pins: []store.EvidenceRef{{AttachmentID: &attachmentID}},
	})
	if err != nil {
		t.Fatalf("AttachEvidence: %v", err)
	}
	return result
}

// TestAttachEvidenceWritesTheArtifactAndItsPinsTogether covers the
// composite operation: one call, and the artifact cannot exist without the
// pins the payload names.
func TestAttachEvidenceWritesTheArtifactAndItsPinsTogether(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	result := f.attachEvidence(t, []byte("the evidence"))

	if len(result.Attachments) != 1 || len(result.Pins) != 1 {
		t.Fatalf("wrote %d attachments and %d pins, want one of each",
			len(result.Attachments), len(result.Pins))
	}
	pin := result.Pins[0]
	if pin.AttachmentID == nil || *pin.AttachmentID != result.Attachments[0].AttachmentID {
		t.Fatalf("the pin does not name the attachment that was stored: %+v", pin)
	}
	// The digest binding is read from the target, not taken from a caller
	// who could assert the very thing acceptance checks.
	if pin.Digest != result.Attachments[0].Digest {
		t.Fatalf("pin binds %s, attachment is %s", pin.Digest, result.Attachments[0].Digest)
	}
	if result.Artifact.Status != store.StatusDraft {
		t.Fatalf("artifact is %q; evidence is attached to a draft, and acceptance verifies it",
			result.Artifact.Status)
	}

	pins, err := f.store.ListPins(ctx, f.organizationID, result.Artifact.ArtifactID)
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("the artifact holds %d pins, want 1", len(pins))
	}
}

// TestAcceptanceRequiresTheReviewedEvidence is the invariant this item
// exists to enforce. Each case breaks the correspondence between what the
// payload names and what is pinned, in one way, and acceptance must refuse
// with the reason that names it.
func TestAcceptanceRequiresTheReviewedEvidence(t *testing.T) {
	for name, testCase := range map[string]struct {
		// break mutates the state after a valid AttachEvidence, and returns
		// the reason acceptance must give.
		breakIt func(t *testing.T, f *fixture, result *store.AttachEvidenceResult) store.RejectionReason
	}{
		"the payload names evidence with no pin": {
			breakIt: func(t *testing.T, f *fixture, result *store.AttachEvidenceResult) store.RejectionReason {
				if err := f.store.Unpin(context.Background(), f.organizationID,
					result.Artifact.ArtifactID, result.Pins[0].PinID); err != nil {
					t.Fatalf("Unpin: %v", err)
				}
				return "reviewed payload names evidence that is not pinned"
			},
		},
		"a pin the payload does not name": {
			breakIt: func(t *testing.T, f *fixture, result *store.AttachEvidenceResult) store.RejectionReason {
				// A second, perfectly valid attachment -- pinned, but never
				// reviewed. An extra pin is a retention claim on nobody's
				// authority, which is why the comparison is set EQUALITY.
				extra, err := f.store.PutAttachment(context.Background(),
					putInput(f.organizationID, []byte("evidence nobody reviewed")))
				if err != nil {
					t.Fatalf("PutAttachment: %v", err)
				}
				if _, err := f.store.Pin(context.Background(), f.organizationID,
					result.Artifact.ArtifactID, store.EvidenceRef{AttachmentID: &extra.AttachmentID}); err != nil {
					t.Fatalf("Pin: %v", err)
				}
				return "a pin names evidence the reviewed payload does not"
			},
		},
		"a pin bound to the wrong digest": {
			breakIt: func(t *testing.T, f *fixture, result *store.AttachEvidenceResult) store.RejectionReason {
				if _, err := f.pool.Exec(context.Background(),
					`UPDATE retention_pins SET pinned_digest = $1 WHERE retention_pin_id = $2`,
					digestOf([]byte("a different object entirely")), result.Pins[0].PinID); err != nil {
					t.Fatalf("rewrite pin digest: %v", err)
				}
				return "a pin's digest does not match its target"
			},
		},
		"a referenced object that is not stored": {
			breakIt: func(t *testing.T, f *fixture, result *store.AttachEvidenceResult) store.RejectionReason {
				f.deleteStoredObject(t, result.Attachments[0].Digest)
				return "referenced evidence is not stored"
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := evidenceFixture(t)
			ctx := context.Background()

			result := f.attachEvidence(t, []byte("the evidence"))
			wantReason := testCase.breakIt(t, f, result)

			reviewID := f.acceptableReview(t, result.Artifact)
			err := f.store.AcceptArtifact(ctx, f.organizationID, result.Artifact.ArtifactID, reviewID)

			var rejection *store.TransitionRejected
			if !errors.As(err, &rejection) {
				t.Fatalf("AcceptArtifact returned %v, want a rejection", err)
			}
			if rejection.Reason != wantReason {
				t.Fatalf("refused with %q, want %q", rejection.Reason, wantReason)
			}

			// And the artifact is still a draft: a refused acceptance
			// leaves nothing authoritative behind.
			artifact, err := f.store.GetManagementArtifact(ctx, f.organizationID, result.Artifact.ArtifactID)
			if err != nil {
				t.Fatalf("read artifact: %v", err)
			}
			if artifact.Status != store.StatusDraft {
				t.Fatalf("artifact is %q after a refused acceptance", artifact.Status)
			}
		})
	}
}

// TestAcceptanceSucceedsWhenTheEvidenceMatches is the positive control.
// Without it every case above would pass against a precondition that
// refuses everything.
func TestAcceptanceSucceedsWhenTheEvidenceMatches(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	result := f.attachEvidence(t, []byte("the evidence"))
	reviewID := f.acceptableReview(t, result.Artifact)

	if err := f.store.AcceptArtifact(ctx, f.organizationID, result.Artifact.ArtifactID, reviewID); err != nil {
		t.Fatalf("AcceptArtifact refused a correct evidence set: %v", err)
	}
	artifact, err := f.store.GetManagementArtifact(ctx, f.organizationID, result.Artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if artifact.Status != store.StatusAccepted {
		t.Fatalf("artifact is %q after acceptance", artifact.Status)
	}
}

// TestPinsAreMutableOnlyWhileTheHolderIsADraftOriginal covers the rule that
// makes acceptance's verification hold for the artifact's life rather than
// for an instant.
func TestPinsAreMutableOnlyWhileTheHolderIsADraftOriginal(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	result := f.attachEvidence(t, []byte("the evidence"))
	reviewID := f.acceptableReview(t, result.Artifact)
	if err := f.store.AcceptArtifact(ctx, f.organizationID, result.Artifact.ArtifactID, reviewID); err != nil {
		t.Fatalf("AcceptArtifact: %v", err)
	}

	spare, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("more evidence")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	_, err = f.store.Pin(ctx, f.organizationID, result.Artifact.ArtifactID,
		store.EvidenceRef{AttachmentID: &spare.AttachmentID})
	assertRejected(t, err, store.ReasonWrongStatus, "pinning an accepted artifact")

	err = f.store.Unpin(ctx, f.organizationID, result.Artifact.ArtifactID, result.Pins[0].PinID)
	assertRejected(t, err, store.ReasonWrongStatus, "unpinning an accepted artifact")

	// The set survives both refusals: this is what acceptance verified.
	pins, err := f.store.ListPins(ctx, f.organizationID, result.Artifact.ArtifactID)
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	if len(pins) != 1 || pins[0].PinID != result.Pins[0].PinID {
		t.Fatalf("the verified pin set changed: %+v", pins)
	}
}

// TestADraftAmendmentMayNotPin is the rule the "any draft" version of this
// missed. All chain pins are held by the ORIGINAL, so an amendment pinning
// would mutate the accepted original's verified set before anyone reviewed
// the amendment -- and invalidating that draft afterwards could not tell
// which of the original's pins came from it.
func TestADraftAmendmentMayNotPin(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	result := f.attachEvidence(t, []byte("the evidence"))
	reviewID := f.acceptableReview(t, result.Artifact)
	if err := f.store.AcceptArtifact(ctx, f.organizationID, result.Artifact.ArtifactID, reviewID); err != nil {
		t.Fatalf("AcceptArtifact: %v", err)
	}

	amendment := f.createAmendment(t, result.Artifact.ArtifactID, `{"title":"amended"}`)
	spare, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("amendment evidence")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	_, err = f.store.Pin(ctx, f.organizationID, amendment.ArtifactID,
		store.EvidenceRef{AttachmentID: &spare.AttachmentID})
	assertRejected(t, err, store.ReasonIsAmendment, "pinning from a draft amendment")

	// The original's set is untouched.
	pins, err := f.store.ListPins(ctx, f.organizationID, result.Artifact.ArtifactID)
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("the original holds %d pins, want the one it was accepted with", len(pins))
	}
}

// TestLifecycleReleasesPinsWhereTheClaimEnds covers the three transitions
// that decide whether an artifact keeps holding its evidence.
func TestLifecycleReleasesPinsWhereTheClaimEnds(t *testing.T) {
	t.Run("invalidation releases", func(t *testing.T) {
		f := evidenceFixture(t)
		ctx := context.Background()
		result := f.attachEvidence(t, []byte("never accepted"))

		if err := f.store.InvalidateArtifact(ctx, f.organizationID, result.Artifact.ArtifactID); err != nil {
			t.Fatalf("InvalidateArtifact: %v", err)
		}
		f.assertPinCount(t, result.Artifact.ArtifactID, 0,
			"an artifact that never became authoritative has nothing justifying its hold")
	})

	t.Run("archival releases", func(t *testing.T) {
		f := evidenceFixture(t)
		ctx := context.Background()
		result := f.attachEvidence(t, []byte("accepted then retired"))

		reviewID := f.acceptableReview(t, result.Artifact)
		if err := f.store.AcceptArtifact(ctx, f.organizationID, result.Artifact.ArtifactID, reviewID); err != nil {
			t.Fatalf("AcceptArtifact: %v", err)
		}
		if err := f.store.ArchiveArtifact(ctx, f.organizationID, result.Artifact.ArtifactID); err != nil {
			t.Fatalf("ArchiveArtifact: %v", err)
		}
		f.assertPinCount(t, result.Artifact.ArtifactID, 0,
			"archived is terminal and non-authoritative, so it is the retention boundary")
	})
}

func (f *fixture) assertPinCount(t *testing.T, artifactID uuid.UUID, want int, why string) {
	t.Helper()
	pins, err := f.store.ListPins(context.Background(), f.organizationID, artifactID)
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	if len(pins) != want {
		t.Fatalf("artifact holds %d pins, want %d: %s", len(pins), want, why)
	}
}

// acceptableReview records an acceptance by a reviewer who is not the
// author, against the artifact's current review digest.
func (f *fixture) acceptableReview(t *testing.T, artifact *store.ManagementArtifact) uuid.UUID {
	t.Helper()
	review := f.review(t, artifact.ArtifactID, artifact.ReviewDigest,
		store.DecisionAccepted, f.reviewer, nil)
	return review.ReviewID
}

func assertRejected(t *testing.T, err error, want store.RejectionReason, what string) {
	t.Helper()
	var rejection *store.TransitionRejected
	if !errors.As(err, &rejection) {
		t.Fatalf("%s returned %v, want a rejection", what, err)
	}
	if rejection.Reason != want {
		t.Fatalf("%s refused with %q, want %q", what, rejection.Reason, want)
	}
}

// TestATypeWithNoExtractorRequiresZeroPins is the meaning of an absent
// extractor, and it is a statement rather than an omission: the type
// carries no evidence, so any pin on it is a retention claim nobody
// reviewed.
//
// Reading a missing extractor as "cannot tell" would be the dangerous
// alternative -- it would wave through unreviewed claims on every type
// nobody had got round to registering, which is every type at first.
func TestATypeWithNoExtractorRequiresZeroPins(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	// testType registers a validator and NO extractor.
	artifact := f.createDraft(t, `{"title":"carries no evidence"}`)

	// It accepts with no pins, which is the ordinary case.
	plain := f.createDraft(t, `{"title":"also carries none"}`)
	if err := f.store.AcceptArtifact(ctx, f.organizationID, plain.ArtifactID,
		f.acceptableReview(t, plain)); err != nil {
		t.Fatalf("a type with no extractor and no pins was refused: %v", err)
	}

	// And it refuses with one, naming the pin as unreviewed.
	stored, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("unreviewed evidence")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}
	if _, pinErr := f.store.Pin(ctx, f.organizationID, artifact.ArtifactID,
		store.EvidenceRef{AttachmentID: &stored.AttachmentID}); pinErr != nil {
		t.Fatalf("Pin: %v", pinErr)
	}

	err = f.store.AcceptArtifact(ctx, f.organizationID, artifact.ArtifactID, f.acceptableReview(t, artifact))
	assertRejected(t, err, "a pin names evidence the reviewed payload does not",
		"accepting a no-extractor type that holds a pin")
}

// TestSupersessionFacesTheEvidencePreconditions covers the second door
// into accepted status.
//
// Supersession accepts the superseding artifact, so it becomes
// authoritative -- and it is the door an evidence-bearing artifact is most
// likely to arrive through, since superseding is how a corrected version
// replaces one. A check on AcceptArtifact alone leaves it wide open.
func TestSupersessionFacesTheEvidencePreconditions(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	// An accepted original to supersede.
	target := f.attachEvidence(t, []byte("the first version's evidence"))
	if err := f.store.AcceptArtifact(ctx, f.organizationID, target.Artifact.ArtifactID,
		f.acceptableReview(t, target.Artifact)); err != nil {
		t.Fatalf("accept the target: %v", err)
	}

	// A superseding artifact whose payload names evidence, with its pin
	// removed. Nothing about the supersession itself is wrong.
	superseding := f.attachSuperseding(t, target.Artifact.ArtifactID, []byte("the second version's evidence"))
	if err := f.store.Unpin(ctx, f.organizationID, superseding.Artifact.ArtifactID,
		superseding.Pins[0].PinID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}

	err := f.store.SupersedeArtifact(ctx, f.organizationID, target.Artifact.ArtifactID,
		superseding.Artifact.ArtifactID, f.acceptableReview(t, superseding.Artifact))
	assertRejected(t, err, "reviewed payload names evidence that is not pinned",
		"superseding with an unpinned reference")

	// Neither artifact moved: the target is still accepted, the
	// superseding one still a draft.
	for _, check := range []struct {
		id   uuid.UUID
		want store.Status
	}{
		{target.Artifact.ArtifactID, store.StatusAccepted},
		{superseding.Artifact.ArtifactID, store.StatusDraft},
	} {
		artifact, readErr := f.store.GetManagementArtifact(ctx, f.organizationID, check.id)
		if readErr != nil {
			t.Fatalf("read artifact: %v", readErr)
		}
		if artifact.Status != check.want {
			t.Fatalf("artifact %s is %q, want %q after a refused supersession",
				check.id, artifact.Status, check.want)
		}
	}

	// The positive control, on the same transition: restore the pin and it
	// goes through. Without this the case above would pass against a
	// supersession that refused everything.
	if _, err := f.store.Pin(ctx, f.organizationID, superseding.Artifact.ArtifactID,
		store.EvidenceRef{AttachmentID: &superseding.Attachments[0].AttachmentID}); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := f.store.SupersedeArtifact(ctx, f.organizationID, target.Artifact.ArtifactID,
		superseding.Artifact.ArtifactID, f.acceptableReview(t, superseding.Artifact)); err != nil {
		t.Fatalf("SupersedeArtifact refused a correct evidence set: %v", err)
	}
}

// TestAttachEvidenceRefusesIncoherentRequests covers what the composite
// path must refuse BEFORE it writes anything, since none of these could
// produce a coherent result and all of them would leave residue.
func TestAttachEvidenceRefusesIncoherentRequests(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	// An accepted original, so the amendment case has something to amend.
	original := f.attachEvidence(t, []byte("the original's evidence"))
	if err := f.store.AcceptArtifact(ctx, f.organizationID, original.Artifact.ArtifactID,
		f.acceptableReview(t, original.Artifact)); err != nil {
		t.Fatalf("accept the original: %v", err)
	}

	// Each case asserts the reason it was refused, not merely that it
	// was. These rules subsume one another -- a nil id is also not a v7,
	// an id nobody pinned is also unpinned -- so a case checking only
	// "some error" passes with its own rule deleted, caught by the next
	// one along. Every one of these three survived exactly that way before
	// the messages were asserted.
	for name, testCase := range map[string]struct {
		mutate func(*store.AttachEvidenceInput)
		reason string
	}{
		// The one that matters most: pins held by an amendment can never
		// be released, because nothing archives an amendment and archiving
		// the original removes only its own.
		"the artifact is an amendment": {
			mutate: func(i *store.AttachEvidenceInput) {
				i.Artifact.AmendsArtifactID = &original.Artifact.ArtifactID
			},
			reason: "an amendment cannot hold pins",
		},
		"an attachment has no preallocated id": {
			mutate: func(i *store.AttachEvidenceInput) { i.Attachments[0].AttachmentID = uuid.Nil },
			reason: "no preallocated id",
		},
		"an attachment id is not a UUIDv7": {
			mutate: func(i *store.AttachEvidenceInput) { i.Attachments[0].AttachmentID = uuid.New() },
			reason: "UUID version 4",
		},
		"an attachment belongs to another organization": {
			mutate: func(i *store.AttachEvidenceInput) { i.Attachments[0].OrganizationID = f.otherOrgID },
			reason: "belongs to organization",
		},
		"an attachment is stored but not pinned": {
			mutate: func(i *store.AttachEvidenceInput) { i.Pins = nil },
			reason: "pinned by none",
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := f.evidenceInput(t, []byte("evidence for a request that will be refused"))
			testCase.mutate(&input)

			// Counted across EVERY organization. An org-scoped count misses
			// the cross-tenant case entirely: the row it writes lands in
			// the other organization, where this test was not looking.
			before := f.countAllAttachments(ctx, t)
			_, err := f.store.AttachEvidence(ctx, input)
			if err == nil {
				t.Fatal("AttachEvidence accepted a request it must refuse")
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Fatalf("refused with %v, which does not name %q; another rule caught this input "+
					"and the rule under test may be doing nothing", err, testCase.reason)
			}
			// Refused before anything was stored, which is the point of
			// checking first: a request that cannot succeed must not leave
			// objects and rows behind on its way to failing.
			if after := f.countAllAttachments(ctx, t); after != before {
				t.Fatalf("a refused request wrote %d attachment rows", after-before)
			}
		})
	}
}

// TestAttachEvidenceRollsBackTheArtifactWithItsPins is the atomicity claim
// under test. The happy path passes whether or not the two are one
// transaction; only a failure between them can tell.
func TestAttachEvidenceRollsBackTheArtifactWithItsPins(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	input := f.evidenceInput(t, []byte("evidence that will be rolled back"))
	// A second pin naming evidence that does not exist. The first pin is
	// perfectly good, so the failure lands BETWEEN the artifact insert and
	// the end of the pin writes.
	missing := uuid.New()
	input.Pins = append(input.Pins, store.EvidenceRef{AuditArtifactID: &missing})

	beforeArtifacts := f.countArtifacts(t)
	beforePins := f.countPins(t)

	if _, err := f.store.AttachEvidence(ctx, input); err == nil {
		t.Fatal("AttachEvidence accepted a pin naming evidence that does not exist")
	}

	if after := f.countArtifacts(t); after != beforeArtifacts {
		t.Fatalf("%d artifacts survived a failed AttachEvidence; the artifact and its pins are "+
			"supposed to commit together or not at all", after-beforeArtifacts)
	}
	if after := f.countPins(t); after != beforePins {
		t.Fatalf("%d pins survived a failed AttachEvidence", after-beforePins)
	}
	// The attachment row DOES survive, and saying so is the contract: it
	// was committed by PutAttachment before this transaction opened. It is
	// unreferenced rather than dangling, and truncation is what makes its
	// object reclaimable.
	var rows int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM binary_attachments WHERE attachment_id = $1`,
		input.Attachments[0].AttachmentID).Scan(&rows); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the attachment row was rolled back too; the documented residue is wrong")
	}
}

// evidenceInput builds a valid composite request.
func (f *fixture) evidenceInput(t *testing.T, body []byte) store.AttachEvidenceInput {
	t.Helper()
	attachmentID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate attachment id: %v", err)
	}
	payload, err := json.Marshal(evidencePayload{
		Title:       "an artifact with evidence",
		Attachments: []uuid.UUID{attachmentID},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return store.AttachEvidenceInput{
		Attachments: []store.PutAttachmentInput{{
			Body:           bytes.NewReader(body),
			Digest:         digestOf(body),
			MediaType:      mediaType,
			SizeBytes:      int64(len(body)),
			OrganizationID: f.organizationID,
			AttachmentID:   attachmentID,
		}},
		Artifact: store.CreateManagementArtifactInput{
			Payload:          payload,
			Type:             evidenceType,
			Summary:          "an artifact with evidence",
			Scope:            f.scope(),
			OrganizationID:   f.organizationID,
			UserID:           f.userID,
			AuthorInstanceID: f.author,
		},
		Pins: []store.EvidenceRef{{AttachmentID: &attachmentID}},
	}
}

// attachSuperseding is evidenceInput plus a supersession target.
func (f *fixture) attachSuperseding(t *testing.T, targetID uuid.UUID, body []byte) *store.AttachEvidenceResult {
	t.Helper()
	input := f.evidenceInput(t, body)
	input.Artifact.SupersedesArtifactID = &targetID
	result, err := f.store.AttachEvidence(context.Background(), input)
	if err != nil {
		t.Fatalf("AttachEvidence for a superseding artifact: %v", err)
	}
	return result
}

func (f *fixture) countRows(t *testing.T, table string) int {
	t.Helper()
	var rows int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE organization_id = $1`, f.organizationID).Scan(&rows); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return rows
}

// countAllAttachments ignores the organization, so a write that lands in
// the WRONG one is still counted.
func (f *fixture) countAllAttachments(ctx context.Context, t *testing.T) int {
	t.Helper()
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM binary_attachments`).Scan(&rows); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	return rows
}
func (f *fixture) countArtifacts(t *testing.T) int { return f.countRows(t, "management_artifacts") }
func (f *fixture) countPins(t *testing.T) int      { return f.countRows(t, "retention_pins") }
