package postgres

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/canonical"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
)

// The two projection builders must agree, and this is the test that keeps
// them agreeing.
//
// buildReviewableProjection assembles the review digest at WRITE time from
// the caller's input; reviewableProjectionOf reassembles it at VERIFY time
// from the stored row. They are field-for-field mirrors, and mirrors drift:
// a field added to one and forgotten in the other would make verification
// report every stored artifact as corrupt — a tool that cries wolf about a
// healthy plane, which is the failure mode this whole design keeps trying
// to avoid.
//
// The assertion is on the DIGESTS rather than on the structs, because the
// digest is what actually has to match. Comparing structs would pass for
// two projections that serialise differently.
func TestReviewProjectionsAgree(t *testing.T) {
	organizationID := uuid.New()
	artifactID := uuid.New()
	authorID := uuid.New()
	productID := uuid.New()
	featureID := uuid.New()
	epicID := uuid.New()
	storyID := uuid.New()
	amendsID := uuid.New()
	supersedesID := uuid.New()
	replacesID := uuid.New()
	scopeID := uuid.New()

	const (
		artifactType = registry.Type("spec")
		category     = registry.Category("management")
		summary      = "a summary that is part of what a reviewer approved"
		version      = 3
	)
	payload := json.RawMessage(`{"b":2,"a":1}`)
	scope := store.Scope{Type: store.ScopeStory, ID: scopeID}
	lineage := store.Lineage{
		ProductID: &productID, FeatureID: &featureID, EpicID: &epicID, StoryID: &storyID,
	}

	input := store.CreateManagementArtifactInput{
		AmendsArtifactID:     &amendsID,
		SupersedesArtifactID: &supersedesID,
		ReplacesArtifactID:   &replacesID,
		Lineage:              lineage,
		Type:                 artifactType,
		Summary:              summary,
		Payload:              payload,
		Scope:                scope,
		ArtifactID:           artifactID,
		OrganizationID:       organizationID,
		AuthorInstanceID:     authorID,
	}
	stored := store.ManagementArtifact{
		AmendsArtifactID:     &amendsID,
		SupersedesArtifactID: &supersedesID,
		ReplacesArtifactID:   &replacesID,
		Lineage:              lineage,
		Type:                 artifactType,
		Category:             category,
		Summary:              summary,
		Payload:              payload,
		Scope:                scope,
		ArtifactID:           artifactID,
		OrganizationID:       organizationID,
		AuthorInstanceID:     authorID,
		SchemaVersion:        version,
	}

	atWrite, err := canonical.Digest(buildReviewableProjection(artifactID, artifactType, category, version, input))
	if err != nil {
		t.Fatalf("write-time digest: %v", err)
	}
	atVerify, err := canonical.Digest(reviewableProjectionOf(&stored))
	if err != nil {
		t.Fatalf("verify-time digest: %v", err)
	}

	if atWrite != atVerify {
		t.Errorf("projections disagree:\n\twrite-time  %s\n\tverify-time %s\n"+
			"a field present in one builder and missing from the other makes every stored review digest fail to reproduce",
			atWrite, atVerify)
	}
}

// Every optional field must actually reach the digest. Without this, a
// builder that dropped one would still agree with a mirror that dropped the
// same one, and the test above would pass while both were wrong.
func TestReviewProjectionCoversOptionalFields(t *testing.T) {
	base := store.ManagementArtifact{
		ArtifactID:       uuid.New(),
		OrganizationID:   uuid.New(),
		AuthorInstanceID: uuid.New(),
		Type:             registry.Type("spec"),
		Category:         registry.Category("management"),
		Summary:          "summary",
		Payload:          json.RawMessage(`{"a":1}`),
		Scope:            store.Scope{Type: store.ScopeStory, ID: uuid.New()},
		SchemaVersion:    1,
	}
	baseline, err := canonical.Digest(reviewableProjectionOf(&base))
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	id := uuid.New()
	mutations := map[string]func(a *store.ManagementArtifact){
		"product_id":             func(a *store.ManagementArtifact) { a.Lineage.ProductID = &id },
		"feature_id":             func(a *store.ManagementArtifact) { a.Lineage.FeatureID = &id },
		"epic_id":                func(a *store.ManagementArtifact) { a.Lineage.EpicID = &id },
		"story_id":               func(a *store.ManagementArtifact) { a.Lineage.StoryID = &id },
		"amends_artifact_id":     func(a *store.ManagementArtifact) { a.AmendsArtifactID = &id },
		"supersedes_artifact_id": func(a *store.ManagementArtifact) { a.SupersedesArtifactID = &id },
		"replaces_artifact_id":   func(a *store.ManagementArtifact) { a.ReplacesArtifactID = &id },
		"summary":                func(a *store.ManagementArtifact) { a.Summary = "different" },
		"schema_version":         func(a *store.ManagementArtifact) { a.SchemaVersion = 2 },
		"payload":                func(a *store.ManagementArtifact) { a.Payload = json.RawMessage(`{"a":2}`) },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			mutated := base
			mutate(&mutated)
			digest, digestErr := canonical.Digest(reviewableProjectionOf(&mutated))
			if digestErr != nil {
				t.Fatalf("digest: %v", digestErr)
			}
			if digest == baseline {
				t.Errorf("changing %s did not change the review digest: it is not part of what a review binds", name)
			}
		})
	}
}
