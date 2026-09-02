//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// TestRepositoryCommitsWithItsPrimaryMembership: the membership row is in
// the plane the moment the repository is, because the schema requires it at
// commit (repositories_primary_is_member_fkey, DEFERRABLE INITIALLY
// DEFERRED).
//
// THE MUTANT: drop the InsertProductRepositoryIfAbsent from
// ProvisionRepository. The commit then fails on that named constraint —
// which is a different failure from this test's, and the point: the
// schema refuses a repository with no primary member, so the seam must
// write both or neither.
func TestRepositoryCommitsWithItsPrimaryMembership(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	primary := provisionProduct(t, f, "core")

	out, err := f.store.ProvisionRepository(ctx, store.ProvisionRepositoryInput{
		Slug: "api", DisplayName: "API", OrganizationID: f.organizationID, PrimaryProductID: primary, UserID: f.userID,
	})
	if err != nil {
		t.Fatalf("provision repository: %v", err)
	}
	if !out.Created {
		t.Fatal("first provisioning must report Created")
	}
	if !slices.Equal(out.Record.ProductIDs, []uuid.UUID{primary}) {
		t.Fatalf("memberships %v, want exactly the primary %s", out.Record.ProductIDs, primary)
	}

	var members int
	if err := f.pool.QueryRow(ctx,
		"SELECT count(*) FROM product_repositories WHERE repository_id = $1 AND product_id = $2",
		out.Record.RepositoryID, primary).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 1 {
		t.Fatalf("%d primary membership rows after commit, want 1", members)
	}
}

// TestReprovisioningUnderAnotherUserIsAConflict: the accountable human is
// persisted lineage and part of what was supplied, so a retry under a
// different user -- even one that does not exist -- is a typed conflict
// that changes nothing. THE MUTANT: compare only the display name.
func TestReprovisioningUnderAnotherUserIsAConflict(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	primary := provisionProduct(t, f, "core")
	repo, err := f.store.ProvisionRepository(ctx, store.ProvisionRepositoryInput{
		Slug: "api", DisplayName: "API", OrganizationID: f.organizationID, PrimaryProductID: primary, UserID: f.userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stranger := uuid.New()

	if _, err := f.store.ProvisionProduct(ctx, store.ProvisionProductInput{
		Slug: "core", DisplayName: "core", OrganizationID: f.organizationID, UserID: stranger,
	}); !errors.Is(err, store.ErrBootstrapConflict) {
		t.Fatalf("a product re-provisioned under another user: %v, want a conflict", err)
	}
	if stored, _ := f.store.GetProductBySlug(ctx, f.organizationID, "core"); stored.UserID != f.userID {
		t.Fatalf("the refused re-provisioning changed the product's user to %s", stored.UserID)
	}

	if _, err := f.store.ProvisionRepository(ctx, store.ProvisionRepositoryInput{
		Slug: "api", DisplayName: "API", OrganizationID: f.organizationID, PrimaryProductID: primary, UserID: stranger,
	}); !errors.Is(err, store.ErrBootstrapConflict) {
		t.Fatalf("a repository re-provisioned under another user: %v, want a conflict", err)
	}
	if stored, _ := f.store.GetRepositoryBySlug(ctx, f.organizationID, "api"); stored.UserID != repo.Record.UserID {
		t.Fatalf("the refused re-provisioning changed the repository's user to %s", stored.UserID)
	}
}

// TestRepositoryPrimaryCannotBeChangedByReprovisioning: re-provisioning an
// existing repository with a different primary Product is a conflict, not a
// silent redesignation — changing the primary is a decision someone makes.
func TestRepositoryPrimaryCannotBeChangedByReprovisioning(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := provisionProduct(t, f, "first")
	second := provisionProduct(t, f, "second")
	input := store.ProvisionRepositoryInput{
		Slug: "api", DisplayName: "API", OrganizationID: f.organizationID, PrimaryProductID: first, UserID: f.userID,
	}
	if _, err := f.store.ProvisionRepository(ctx, input); err != nil {
		t.Fatal(err)
	}

	input.PrimaryProductID = second
	if _, err := f.store.ProvisionRepository(ctx, input); !errors.Is(err, store.ErrBootstrapConflict) {
		t.Fatalf("a different primary must be a typed conflict, got: %v", err)
	}
	stored, err := f.store.GetRepositoryBySlug(ctx, f.organizationID, "api")
	if err != nil {
		t.Fatal(err)
	}
	if stored.PrimaryProductID != first {
		t.Fatalf("the refused re-provisioning changed the primary to %s", stored.PrimaryProductID)
	}
	if slices.Contains(stored.ProductIDs, second) {
		t.Fatal("the refused re-provisioning left a membership behind")
	}
}

// TestSecondaryMembershipIsSeparateAndIdempotent: a secondary Product is
// added by its own verb, twice is once, and adding the primary again is a
// no-op.
func TestSecondaryMembershipIsSeparateAndIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	primary := provisionProduct(t, f, "core")
	shared := provisionProduct(t, f, "shared")
	repo, err := f.store.ProvisionRepository(ctx, store.ProvisionRepositoryInput{
		Slug: "lib", DisplayName: "Lib", OrganizationID: f.organizationID, PrimaryProductID: primary, UserID: f.userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	repositoryID := repo.Record.RepositoryID

	added, err := f.store.AddRepositoryToProduct(ctx, f.organizationID, shared, repositoryID)
	if err != nil {
		t.Fatal(err)
	}
	if !added.Created {
		t.Fatal("a new secondary membership must report Created")
	}
	again, err := f.store.AddRepositoryToProduct(ctx, f.organizationID, shared, repositoryID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created {
		t.Fatal("a repeated secondary membership reported Created")
	}
	primaryAgain, err := f.store.AddRepositoryToProduct(ctx, f.organizationID, primary, repositoryID)
	if err != nil {
		t.Fatal(err)
	}
	if primaryAgain.Created {
		t.Fatal("re-adding the primary reported Created; it was committed with the repository")
	}
	got := again.Record.ProductIDs
	if len(got) != 2 || !slices.Contains(got, primary) || !slices.Contains(got, shared) {
		t.Fatalf("memberships %v, want primary %s and secondary %s", got, primary, shared)
	}
}
