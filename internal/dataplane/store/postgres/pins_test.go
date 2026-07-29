package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"orchestrator/internal/dataplane/store"
)

// mapPinViolation is what turns the schema's refusal into something a
// caller can act on, and it is tested here rather than through the race.
//
// The race tests assert the RAW database errors, because measuring the
// SQLSTATEs is their whole purpose -- they write their pins with direct
// INSERTs and never reach this function. So neither branch of it was
// exercised by anything: deleting either left the suite green while the
// diagnostic it claims to produce disappeared.
//
// What matters here is not only that a match maps, but that a near-miss
// does NOT. Every case below differs from a genuine truncation race in
// exactly one respect.
func TestMapPinViolation(t *testing.T) {
	attachmentID := uuid.New()
	auditID := uuid.New()
	attachmentRef := store.EvidenceRef{AttachmentID: &attachmentID}
	auditRef := store.EvidenceRef{AuditArtifactID: &auditID}

	pgErrorFor := func(code, constraint string) error {
		return &pgconn.PgError{Code: code, ConstraintName: constraint, Message: "raw driver text"}
	}

	for name, testCase := range map[string]struct {
		err       error
		reference store.EvidenceRef
		// mapped names the id the diagnostic must carry; empty means the
		// error must be passed through instead.
		mapped string
	}{
		"the attachment was truncated": {
			err:       pgErrorFor(foreignKeyViolation, attachmentPinConstraint),
			reference: attachmentRef,
			mapped:    attachmentID.String(),
		},
		"the audit artifact was truncated": {
			err:       pgErrorFor(foreignKeyViolation, auditPinConstraint),
			reference: auditRef,
			mapped:    auditID.String(),
		},

		// Near-misses. Each would be mapped by a looser implementation,
		// and each would then name evidence the caller never referenced.
		"the attachment constraint with an audit reference": {
			err:       pgErrorFor(foreignKeyViolation, attachmentPinConstraint),
			reference: auditRef,
		},
		"the audit constraint with an attachment reference": {
			err:       pgErrorFor(foreignKeyViolation, auditPinConstraint),
			reference: attachmentRef,
		},
		// 23001 is what the TRUNCATION receives, not the pin. Mapping it
		// here would report a missing attachment for a row that is very
		// much present.
		"a restrict violation on the attachment constraint": {
			err:       pgErrorFor(restrictViolation, attachmentPinConstraint),
			reference: attachmentRef,
		},
		"a foreign-key violation on another constraint": {
			err:       pgErrorFor(foreignKeyViolation, "retention_pins_by_artifact_fkey"),
			reference: attachmentRef,
		},
		"a non-Postgres failure": {
			err:       errors.New("the connection went away"),
			reference: attachmentRef,
		},
	} {
		t.Run(name, func(t *testing.T) {
			mapped := mapPinViolation(testCase.err, testCase.reference)
			if mapped == nil {
				t.Fatal("mapPinViolation returned nil for a failure")
			}

			if testCase.mapped == "" {
				if errors.Is(mapped, store.ErrNotFound) {
					t.Fatalf("reported %v as a missing target; this is not a truncation race, and a "+
						"caller told to retry would retry forever", mapped)
				}
				// Passed through means passed through: the original error
				// has to survive for anything above to diagnose it.
				if !errors.Is(mapped, testCase.err) {
					t.Fatalf("returned %v, which no longer wraps the original error", mapped)
				}
				return
			}

			if !errors.Is(mapped, store.ErrNotFound) {
				t.Fatalf("returned %v, want ErrNotFound: the target is gone, which is a caller-visible "+
					"outcome rather than a constraint name nobody can act on", mapped)
			}
			if !strings.Contains(mapped.Error(), testCase.mapped) {
				t.Fatalf("the diagnostic %q does not name %s", mapped, testCase.mapped)
			}
			// And it does not leak the constraint name, which is the thing
			// a caller cannot act on.
			if strings.Contains(mapped.Error(), "_fkey") {
				t.Fatalf("the diagnostic %q names a constraint; that is what this mapping exists to "+
					"replace", mapped)
			}
		})
	}
}
