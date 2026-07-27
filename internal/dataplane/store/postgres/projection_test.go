package postgres

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/canonical"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
)

// TestReviewableProjectionBindsEveryField is the real test of ADR 0028 §5's
// binding, and it runs against the projection rather than through creation.
//
// The version that went through creation was a false positive: every
// created artifact gets a fresh id, the id is part of the projection, so
// every digest differed whatever else the projection contained. It passed
// with the payload, scope, lineage, author and links all removed. Holding
// the id FIXED is what makes each case test the field it names.
//
// Every field in the projection appears below. A field added to the
// envelope without a case here is a field a review does not really bind.
func TestReviewableProjectionBindsEveryField(t *testing.T) {
	fixedID := uuid.MustParse("019fa422-038a-784d-8182-f7dc6e79c593")
	otherID := uuid.MustParse("019fa422-038a-784d-8182-f7dc6e79c594")

	const (
		baseType     = registry.Type("spec")
		baseCategory = registry.CategoryManagement
		baseVersion  = 1
	)
	baseInput := func() store.CreateManagementArtifactInput {
		scopeID := uuid.MustParse("019fa422-038a-784d-8182-f7dc6e79c595")
		return store.CreateManagementArtifactInput{
			Payload:          json.RawMessage(`{"title":"one"}`),
			Type:             baseType,
			Summary:          "a summary",
			Scope:            store.Scope{Type: store.ScopeStory, ID: scopeID},
			AuthorInstanceID: uuid.MustParse("019fa422-038a-784d-8182-f7dc6e79c596"),
		}
	}

	digestOf := func(t *testing.T, id uuid.UUID, artifactType registry.Type, category registry.Category,
		version int, input store.CreateManagementArtifactInput,
	) string {
		t.Helper()
		digest, err := canonical.Digest(buildReviewableProjection(id, artifactType, category, version, input))
		if err != nil {
			t.Fatalf("digest projection: %v", err)
		}
		return digest
	}

	baseline := digestOf(t, fixedID, baseType, baseCategory, baseVersion, baseInput())

	cases := []struct {
		name   string
		digest func(t *testing.T) string
	}{
		{"artifact id", func(t *testing.T) string {
			return digestOf(t, otherID, baseType, baseCategory, baseVersion, baseInput())
		}},
		{"artifact type", func(t *testing.T) string {
			return digestOf(t, fixedID, "design", baseCategory, baseVersion, baseInput())
		}},
		{"artifact category", func(t *testing.T) string {
			return digestOf(t, fixedID, baseType, registry.CategoryAudit, baseVersion, baseInput())
		}},
		{"schema version", func(t *testing.T) string {
			return digestOf(t, fixedID, baseType, baseCategory, 2, baseInput())
		}},
		{"payload", func(t *testing.T) string {
			input := baseInput()
			input.Payload = json.RawMessage(`{"title":"two"}`)
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"summary", func(t *testing.T) string {
			input := baseInput()
			input.Summary = "a different summary"
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"scope type", func(t *testing.T) string {
			input := baseInput()
			input.Scope.Type = store.ScopeEpic
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"scope id", func(t *testing.T) string {
			input := baseInput()
			input.Scope.ID = otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"author", func(t *testing.T) string {
			input := baseInput()
			input.AuthorInstanceID = otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"product lineage", func(t *testing.T) string {
			input := baseInput()
			input.Lineage.ProductID = &otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"feature lineage", func(t *testing.T) string {
			input := baseInput()
			input.Lineage.FeatureID = &otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"epic lineage", func(t *testing.T) string {
			input := baseInput()
			input.Lineage.EpicID = &otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"story lineage", func(t *testing.T) string {
			input := baseInput()
			input.Lineage.StoryID = &otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"amends link", func(t *testing.T) string {
			input := baseInput()
			input.AmendsArtifactID = &otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"supersedes link", func(t *testing.T) string {
			input := baseInput()
			input.SupersedesArtifactID = &otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
		{"replaces link", func(t *testing.T) string {
			input := baseInput()
			input.ReplacesArtifactID = &otherID
			return digestOf(t, fixedID, baseType, baseCategory, baseVersion, input)
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.digest(t); got == baseline {
				t.Fatalf("changing the %s did not move the review digest, so an accepted review would "+
					"survive an edit to it", testCase.name)
			}
		})
	}
}

// TestReviewableProjectionIsStable is the other direction: identical input
// must produce an identical digest, or nothing could ever be accepted.
func TestReviewableProjectionIsStable(t *testing.T) {
	id := uuid.MustParse("019fa422-038a-784d-8182-f7dc6e79c593")
	input := store.CreateManagementArtifactInput{
		Payload:          json.RawMessage(`{"b":2,"a":1}`),
		Type:             "spec",
		Summary:          "s",
		Scope:            store.Scope{Type: store.ScopeStory, ID: id},
		AuthorInstanceID: id,
	}

	first, err := canonical.Digest(buildReviewableProjection(id, "spec", registry.CategoryManagement, 1, input))
	if err != nil {
		t.Fatalf("digest: %v", err)
	}

	// Key order in the payload must not matter: JCS sorts them.
	reordered := input
	reordered.Payload = json.RawMessage(`{"a":1,"b":2}`)
	second, err := canonical.Digest(buildReviewableProjection(id, "spec", registry.CategoryManagement, 1, reordered))
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if first != second {
		t.Fatal("reordering the payload's keys moved the review digest; canonicalization is not being applied")
	}
}
