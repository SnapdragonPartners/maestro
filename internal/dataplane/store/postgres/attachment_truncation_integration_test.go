//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"orchestrator/internal/dataplane/store"
)

// Attachment truncation is what makes the object sweep able to reclaim
// anything (item 6 design, D6a). Without it every object stays referenced
// forever and DeleteUnpinned reclaims nothing.
//
// Deleting the row does NOT delete the object: it makes the object
// unreachable, and the sweep collects it afterwards under the digest lock.

// TestAttachmentTruncationRetainsPinnedRows covers the three rules item 5
// requires of every truncation: the pinned row survives, the unpinned row
// goes, and another organization's rows are untouched.
func TestAttachmentTruncationRetainsPinnedRows(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	// Pinned, by an accepted artifact that cites it.
	pinned := f.acceptedOriginal(t)

	// Unpinned: stored, never cited.
	unpinned, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("nobody cites this")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	// Another organization's attachment, which this pass must not see.
	elsewhere, otherErr := f.store.PutAttachment(ctx,
		putInput(f.otherOrgID, []byte("another tenant's evidence")))
	if otherErr != nil {
		t.Fatalf("PutAttachment in the other organization: %v", otherErr)
	}

	// Age every row past the horizon, so the horizon is not what decides
	// which of them survives.
	if _, ageErr := f.pool.Exec(ctx,
		`UPDATE binary_attachments SET created_at = now() - interval '30 days'`); ageErr != nil {
		t.Fatalf("age the attachments: %v", ageErr)
	}

	result, err := f.store.TruncateAuditBefore(ctx, f.organizationID, horizon())
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}

	contribution, reported := result.PerTable[store.TableAttachments]
	if !reported {
		t.Fatal("the pass reported no contribution for binary_attachments")
	}
	if contribution.Candidates != 2 {
		t.Fatalf("counted %d candidates, want this organization's two rows", contribution.Candidates)
	}
	if contribution.Deleted != 1 || contribution.RetainedPinned != 1 {
		t.Fatalf("deleted %d and retained %d pinned, want one of each: %+v",
			contribution.Deleted, contribution.RetainedPinned, contribution)
	}
	if !contribution.Reconciles() {
		t.Fatalf("the buckets do not account for every candidate: %+v", contribution)
	}

	// The rows themselves, which the accounting describes but does not
	// prove.
	for _, check := range []struct {
		name    string
		id      any
		present bool
	}{
		{"the pinned attachment", pinned.Attachments[0].AttachmentID, true},
		{"the unpinned attachment", unpinned.AttachmentID, false},
		{"the other organization's attachment", elsewhere.AttachmentID, true},
	} {
		var exists bool
		if err := f.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM binary_attachments WHERE attachment_id = $1)`,
			check.id).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", check.name, err)
		}
		if exists != check.present {
			t.Errorf("%s: present=%v, want %v", check.name, exists, check.present)
		}
	}
}

// TestAttachmentTruncationRespectsTheHorizon is the other half of the
// selection rule: a recent unpinned row is not a candidate at all.
func TestAttachmentTruncationRespectsTheHorizon(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	recent, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("stored just now")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	result, err := f.store.TruncateAuditBefore(ctx, f.organizationID, horizon())
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if contribution := result.PerTable[store.TableAttachments]; contribution.Candidates != 0 {
		t.Fatalf("a row created now was a candidate: %+v", contribution)
	}

	var exists bool
	if err := f.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM binary_attachments WHERE attachment_id = $1)`,
		recent.AttachmentID).Scan(&exists); err != nil {
		t.Fatalf("check the recent attachment: %v", err)
	}
	if !exists {
		t.Fatal("a row inside the horizon was deleted")
	}
}
