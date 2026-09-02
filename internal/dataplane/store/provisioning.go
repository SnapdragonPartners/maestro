package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// The provisioning family: organization, user, product and repository
// (Phase 3 item 3, design D11).
//
// Organization and user provisioning arrived with item 9's importer and sat
// on the benchmark surface, because that was the only consumer that had ever
// needed a tenant. Provisioning a tenant is not benchmark work, and the
// Orchestrator is its general consumer, so the family has its own surface.
// Product and repository join it on the same pattern.
//
// Every verb is idempotent by natural key with exact conflict semantics:
// matching data returns the existing record with Created=false, and
// DIFFERING data returns ErrBootstrapConflict rather than silently ignoring
// the difference or quietly renaming the record.

// ErrBootstrapConflict reports that a natural key already exists carrying
// different data.
//
// Distinguished from a plain "already exists" because the outcomes differ:
// matching data is a successful no-op, and differing data is a request
// this command will not honour. Silently ignoring the difference would
// make `provision organization -org acme -org-name "Acme Ltd"` appear to
// succeed while the plane still said "Acme Inc".
var ErrBootstrapConflict = errors.New("the record exists with different data")

// BootstrapConflict carries the stored and supplied data.
type BootstrapConflict struct {
	Kind     string
	Key      string
	Stored   string
	Supplied string
}

func (e *BootstrapConflict) Error() string {
	return fmt.Sprintf("%s: %s %q is %q, not %q; changing it is a separate operation",
		ErrBootstrapConflict, e.Kind, e.Key, e.Stored, e.Supplied)
}

// Is lets callers match the sentinel without unwrapping the detail.
func (e *BootstrapConflict) Is(target error) bool { return target == ErrBootstrapConflict }

// Organization is a tenant.
type Organization struct {
	CreatedAt      time.Time
	Slug           string
	DisplayName    string
	OrganizationID uuid.UUID
}

// User is an accountable human. Local mode has no authentication; this is an
// identity, not a credential.
type User struct {
	CreatedAt      time.Time
	Handle         string
	DisplayName    string
	UserID         uuid.UUID
	OrganizationID uuid.UUID
}

// Product is the lineage root beneath an organization (ADR 0018).
type Product struct {
	CreatedAt      time.Time
	Slug           string
	DisplayName    string
	ProductID      uuid.UUID
	OrganizationID uuid.UUID
	UserID         uuid.UUID
}

// Repository is a logical, forge-independent repository (ADR 0022). It
// designates exactly one primary Product and may be a member of others.
// Forge bindings are attributes that arrive with the forge rework; the
// record is the identity.
type Repository struct {
	CreatedAt   time.Time
	Slug        string
	DisplayName string
	// ProductIDs is every Product this repository is a member of, primary
	// included, in id order.
	ProductIDs       []uuid.UUID
	RepositoryID     uuid.UUID
	OrganizationID   uuid.UUID
	PrimaryProductID uuid.UUID
	UserID           uuid.UUID
}

// BootstrapOrganizationInput provisions a tenant.
type BootstrapOrganizationInput struct {
	Slug        string
	DisplayName string
}

// BootstrapUserInput provisions an accountable human within one tenant.
type BootstrapUserInput struct {
	Handle         string
	DisplayName    string
	OrganizationID uuid.UUID
}

// ProvisionProductInput provisions a Product. UserID is the accountable
// human, carried on the row as lineage (ADR 0022).
type ProvisionProductInput struct {
	Slug           string
	DisplayName    string
	OrganizationID uuid.UUID
	UserID         uuid.UUID
}

// ProvisionRepositoryInput provisions a repository with its primary Product.
type ProvisionRepositoryInput struct {
	Slug             string
	DisplayName      string
	OrganizationID   uuid.UUID
	PrimaryProductID uuid.UUID
	UserID           uuid.UUID
}

// Bootstrapped reports what a provisioning call did.
//
// Created distinguishes the two SUCCESSFUL outcomes, which a caller reports
// differently and which a conflict is neither of.
type Bootstrapped[T any] struct {
	Record  T
	Created bool
}

// ProvisioningReader resolves provisioned records by natural key.
type ProvisioningReader interface {
	GetOrganizationBySlug(ctx context.Context, slug string) (*Organization, error)
	GetUserByHandle(ctx context.Context, organizationID uuid.UUID, handle string) (*User, error)
	GetProductBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (*Product, error)
	GetRepositoryBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (*Repository, error)
}

// ProvisioningWriter creates provisioned records, idempotently.
//
// Reachable from the provisioning verbs and the Orchestrator's entry point.
// The importer resolves with the reader and never provisions: an import
// that silently creates a tenant is a defect waiting for team mode.
type ProvisioningWriter interface {
	BootstrapOrganization(ctx context.Context, input BootstrapOrganizationInput) (Bootstrapped[Organization], error)
	BootstrapUser(ctx context.Context, input BootstrapUserInput) (Bootstrapped[User], error)
	ProvisionProduct(ctx context.Context, input ProvisionProductInput) (Bootstrapped[Product], error)

	// ProvisionRepository inserts the repository AND its primary Product
	// membership in one transaction. It is not independent of its Product,
	// and the schema says so: repositories_primary_is_member_fkey is
	// DEFERRABLE INITIALLY DEFERRED, mandatory at commit, so a repository
	// whose primary is not also a member cannot be committed at all.
	//
	// Re-provisioning an existing repository with a different primary
	// Product is a CONFLICT: changing the designation is a decision someone
	// makes, not a side effect of a retried command.
	ProvisionRepository(ctx context.Context, input ProvisionRepositoryInput) (Bootstrapped[Repository], error)

	// AddRepositoryToProduct records a secondary membership, idempotently.
	// Adding the primary again is a no-op with Created=false.
	AddRepositoryToProduct(ctx context.Context, organizationID, productID, repositoryID uuid.UUID) (Bootstrapped[Repository], error)
}
