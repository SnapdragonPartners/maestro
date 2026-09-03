package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// The provisioning family (design D11). Organization and user provisioning
// moved here from benchmark.go unchanged; product and repository follow the
// same insert-or-nothing-then-read shape.

func organizationFromRow(row *gen.Organization) store.Organization {
	return store.Organization{
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		Slug:           row.Slug,
		DisplayName:    row.DisplayName,
		OrganizationID: fromUUID(row.OrganizationID),
	}
}

func userFromRow(row *gen.User) store.User {
	return store.User{
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		Handle:         row.Handle,
		DisplayName:    row.DisplayName,
		UserID:         fromUUID(row.UserID),
		OrganizationID: fromUUID(row.OrganizationID),
	}
}

// GetOrganizationBySlug resolves a tenant by its slug.
func (t *tx) GetOrganizationBySlug(ctx context.Context, slug string) (*store.Organization, error) {
	row, err := t.queries.GetOrganizationBySlug(ctx, slug)
	if err != nil {
		return nil, notFoundByName(err, "organization", slug)
	}
	organization := organizationFromRow(&row)
	return &organization, nil
}

// GetUserByHandle resolves an accountable human within one tenant.
func (t *tx) GetUserByHandle(ctx context.Context, organizationID uuid.UUID, handle string) (*store.User, error) {
	row, err := t.queries.GetUserByHandle(ctx, gen.GetUserByHandleParams{
		OrganizationID: toUUID(organizationID),
		Handle:         handle,
	})
	if err != nil {
		return nil, notFoundByName(err, "user", handle)
	}
	user := userFromRow(&row)
	return &user, nil
}

// BootstrapOrganization provisions a tenant, idempotently.
//
// Insert-or-nothing THEN read, never check-then-insert: two operators running
// this at once would both observe no row, both insert, and one would receive
// a raw uniqueness violation — an outcome that is neither "created" nor
// "already existed" and that leaks a driver error through the seam. Here the
// unique constraint decides who wins and the read that follows is what both
// callers compare against, so they converge (ADR 0027: serialize on a key
// matching the resource, never last-writer-wins).
func (t *tx) BootstrapOrganization(ctx context.Context, input store.BootstrapOrganizationInput) (store.Bootstrapped[store.Organization], error) {
	var empty store.Bootstrapped[store.Organization]
	if err := checkIdentifier("organization slug", input.Slug); err != nil {
		return empty, err
	}
	if err := checkDisplayName("organization", input.DisplayName); err != nil {
		return empty, err
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertOrganizationIfAbsent(ctx, gen.InsertOrganizationIfAbsentParams{
		OrganizationID: toUUID(identifier),
		Slug:           input.Slug,
		DisplayName:    input.DisplayName,
	})
	if err != nil {
		return empty, fmt.Errorf("insert organization %q: %w", input.Slug, err)
	}
	stored, err := t.GetOrganizationBySlug(ctx, input.Slug)
	if err != nil {
		return empty, err
	}
	// Compared against the STORED row rather than against our own insert:
	// the row that is there may be the other racer's, and it is that one the
	// caller must be told about.
	if stored.DisplayName != input.DisplayName {
		return empty, &store.BootstrapConflict{
			Kind: "organization", Key: input.Slug,
			Stored: stored.DisplayName, Supplied: input.DisplayName,
		}
	}
	return store.Bootstrapped[store.Organization]{Record: *stored, Created: inserted == 1}, nil
}

// BootstrapUser provisions an accountable human, idempotently. Same shape and
// same reasoning as BootstrapOrganization.
func (t *tx) BootstrapUser(ctx context.Context, input store.BootstrapUserInput) (store.Bootstrapped[store.User], error) {
	var empty store.Bootstrapped[store.User]
	if err := checkIdentifier("user handle", input.Handle); err != nil {
		return empty, err
	}
	if err := checkDisplayName("user", input.DisplayName); err != nil {
		return empty, err
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertUserIfAbsent(ctx, gen.InsertUserIfAbsentParams{
		UserID:         toUUID(identifier),
		OrganizationID: toUUID(input.OrganizationID),
		Handle:         input.Handle,
		DisplayName:    input.DisplayName,
	})
	if err != nil {
		return empty, fmt.Errorf("insert user %q: %w", input.Handle, err)
	}
	stored, err := t.GetUserByHandle(ctx, input.OrganizationID, input.Handle)
	if err != nil {
		return empty, err
	}
	if stored.DisplayName != input.DisplayName {
		return empty, &store.BootstrapConflict{
			Kind: "user", Key: input.Handle,
			Stored: stored.DisplayName, Supplied: input.DisplayName,
		}
	}
	return store.Bootstrapped[store.User]{Record: *stored, Created: inserted == 1}, nil
}

func productFromRow(row *gen.Product) store.Product {
	return store.Product{
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		Slug:           row.Slug,
		DisplayName:    row.DisplayName,
		ProductID:      fromUUID(row.ProductID),
		OrganizationID: fromUUID(row.OrganizationID),
		UserID:         fromUUID(row.UserID),
	}
}

func repositoryFromRow(row *gen.Repository, products []uuid.UUID) store.Repository {
	return store.Repository{
		CreatedAt:        fromTimestamptz(row.CreatedAt),
		Slug:             row.Slug,
		DisplayName:      row.DisplayName,
		RepositoryID:     fromUUID(row.RepositoryID),
		OrganizationID:   fromUUID(row.OrganizationID),
		PrimaryProductID: fromUUID(row.PrimaryProductID),
		UserID:           fromUUID(row.UserID),
		ProductIDs:       products,
	}
}

// GetProductBySlug resolves a Product within one tenant.
func (t *tx) GetProductBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (*store.Product, error) {
	row, err := t.queries.GetProductBySlug(ctx, gen.GetProductBySlugParams{
		OrganizationID: toUUID(organizationID), Slug: slug,
	})
	if err != nil {
		return nil, notFoundByName(err, "product", slug)
	}
	product := productFromRow(&row)
	return &product, nil
}

// GetRepositoryBySlug resolves a repository within one tenant, with its
// memberships.
func (t *tx) GetRepositoryBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (*store.Repository, error) {
	row, err := t.queries.GetRepositoryBySlug(ctx, gen.GetRepositoryBySlugParams{
		OrganizationID: toUUID(organizationID), Slug: slug,
	})
	if err != nil {
		return nil, notFoundByName(err, "repository", slug)
	}
	return t.repositoryWithProducts(ctx, &row)
}

func (t *tx) repositoryWithProducts(ctx context.Context, row *gen.Repository) (*store.Repository, error) {
	members, err := t.queries.ListRepositoryProducts(ctx, gen.ListRepositoryProductsParams{
		OrganizationID: row.OrganizationID, RepositoryID: row.RepositoryID,
	})
	if err != nil {
		return nil, fmt.Errorf("list products of repository %q: %w", row.Slug, err)
	}
	products := make([]uuid.UUID, 0, len(members))
	for _, member := range members {
		products = append(products, fromUUID(member))
	}
	repository := repositoryFromRow(row, products)
	return &repository, nil
}

// ProvisionProduct provisions a Product, idempotently. Same shape and same
// reasoning as BootstrapOrganization.
func (t *tx) ProvisionProduct(ctx context.Context, input store.ProvisionProductInput) (store.Bootstrapped[store.Product], error) {
	var empty store.Bootstrapped[store.Product]
	if err := checkIdentifier("product slug", input.Slug); err != nil {
		return empty, err
	}
	if err := checkDisplayName("product", input.DisplayName); err != nil {
		return empty, err
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertProductIfAbsent(ctx, gen.InsertProductIfAbsentParams{
		ProductID:      toUUID(identifier),
		OrganizationID: toUUID(input.OrganizationID),
		UserID:         toUUID(input.UserID),
		Slug:           input.Slug,
		DisplayName:    input.DisplayName,
	})
	if err != nil {
		return empty, fmt.Errorf("insert product %q: %w", input.Slug, err)
	}
	stored, err := t.GetProductBySlug(ctx, input.OrganizationID, input.Slug)
	if err != nil {
		return empty, err
	}
	if stored.DisplayName != input.DisplayName {
		return empty, &store.BootstrapConflict{
			Kind: "product", Key: input.Slug, Stored: stored.DisplayName, Supplied: input.DisplayName,
		}
	}
	// The accountable human is persisted lineage (ADR 0022) and part of
	// what was supplied; a retry under another user is a different request.
	if stored.UserID != input.UserID {
		return empty, &store.BootstrapConflict{
			Kind: "product user", Key: input.Slug, Stored: stored.UserID.String(), Supplied: input.UserID.String(),
		}
	}
	return store.Bootstrapped[store.Product]{Record: *stored, Created: inserted == 1}, nil
}

// ProvisionRepository provisions a repository with its primary membership,
// in the transaction the caller opened.
//
// The membership row is written in the SAME transaction as the repository
// row, because the schema requires it at commit
// (repositories_primary_is_member_fkey, DEFERRABLE INITIALLY DEFERRED). Two
// statements in one transaction is not belt-and-braces: split across two,
// the first commit fails on the deferred constraint and the second has
// nothing to attach to.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) ProvisionRepository(ctx context.Context, input store.ProvisionRepositoryInput) (store.Bootstrapped[store.Repository], error) {
	var empty store.Bootstrapped[store.Repository]
	if err := checkIdentifier("repository slug", input.Slug); err != nil {
		return empty, err
	}
	if err := checkDisplayName("repository", input.DisplayName); err != nil {
		return empty, err
	}
	if input.PrimaryProductID == uuid.Nil {
		return empty, fmt.Errorf("repository %q needs a primary product", input.Slug)
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertRepositoryIfAbsent(ctx, gen.InsertRepositoryIfAbsentParams{
		RepositoryID:     toUUID(identifier),
		OrganizationID:   toUUID(input.OrganizationID),
		PrimaryProductID: toUUID(input.PrimaryProductID),
		UserID:           toUUID(input.UserID),
		Slug:             input.Slug,
		DisplayName:      input.DisplayName,
	})
	if err != nil {
		return empty, fmt.Errorf("insert repository %q: %w", input.Slug, err)
	}
	row, err := t.queries.GetRepositoryBySlug(ctx, gen.GetRepositoryBySlugParams{
		OrganizationID: toUUID(input.OrganizationID), Slug: input.Slug,
	})
	if err != nil {
		return empty, notFoundByName(err, "repository", input.Slug)
	}
	// Compared against the STORED row, which may be the other racer's.
	if row.DisplayName != input.DisplayName {
		return empty, &store.BootstrapConflict{
			Kind: "repository", Key: input.Slug, Stored: row.DisplayName, Supplied: input.DisplayName,
		}
	}
	if stored := fromUUID(row.PrimaryProductID); stored != input.PrimaryProductID {
		return empty, &store.BootstrapConflict{
			Kind: "repository primary product", Key: input.Slug,
			Stored: stored.String(), Supplied: input.PrimaryProductID.String(),
		}
	}
	if stored := fromUUID(row.UserID); stored != input.UserID {
		return empty, &store.BootstrapConflict{
			Kind: "repository user", Key: input.Slug, Stored: stored.String(), Supplied: input.UserID.String(),
		}
	}
	// The primary membership, idempotently: on a fresh insert it is what
	// lets the commit succeed; on a repeat it is already there.
	if _, memberErr := t.queries.InsertProductRepositoryIfAbsent(ctx, gen.InsertProductRepositoryIfAbsentParams{
		ProductID: row.PrimaryProductID, RepositoryID: row.RepositoryID, OrganizationID: row.OrganizationID,
	}); memberErr != nil {
		return empty, fmt.Errorf("record primary membership of repository %q: %w", input.Slug, memberErr)
	}
	repository, err := t.repositoryWithProducts(ctx, &row)
	if err != nil {
		return empty, err
	}
	return store.Bootstrapped[store.Repository]{Record: *repository, Created: inserted == 1}, nil
}

// AddRepositoryToProduct records a secondary membership, idempotently.
func (t *tx) AddRepositoryToProduct(ctx context.Context, organizationID, productID, repositoryID uuid.UUID) (store.Bootstrapped[store.Repository], error) {
	var empty store.Bootstrapped[store.Repository]
	inserted, err := t.queries.InsertProductRepositoryIfAbsent(ctx, gen.InsertProductRepositoryIfAbsentParams{
		ProductID: toUUID(productID), RepositoryID: toUUID(repositoryID), OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return empty, fmt.Errorf("add repository %s to product %s: %w", repositoryID, productID, err)
	}
	row, err := t.queries.GetRepositoryByID(ctx, gen.GetRepositoryByIDParams{
		OrganizationID: toUUID(organizationID), RepositoryID: toUUID(repositoryID),
	})
	if err != nil {
		return empty, notFoundByName(err, "repository", repositoryID.String())
	}
	repository, err := t.repositoryWithProducts(ctx, &row)
	if err != nil {
		return empty, err
	}
	return store.Bootstrapped[store.Repository]{Record: *repository, Created: inserted == 1}, nil
}
