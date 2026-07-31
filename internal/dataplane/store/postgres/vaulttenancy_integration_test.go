//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"orchestrator/internal/dataplane/gen"
)

// Tenancy on the vault's acting-user predicate (item 7, D5).
//
// These run the SHIPPED queries against a real database rather than a
// hand-written copy of them, because what is under test is the predicate as
// generated — a test that rewrote the SQL would agree with itself however
// wrong both were.
//
// The hole they exist for: `owner_user_id = @acting_user_id OR
// owner_user_id IS NULL` admits shared rows by design, and a shared row has
// a NULL owner, so that predicate ALONE is satisfied by any acting id at all
// — including a user belonging to a different organization. The organization
// is a separate parameter, so nothing in the predicate ties the two
// together. The membership check is what does.

// vaultFixture is two organizations, each with a user and a repository, so a
// cross-tenant attempt has somewhere real to come from.
type vaultFixture struct {
	queries *gen.Queries

	pool *pgxpool.Pool

	orgA, userA, repoA uuid.UUID
	orgB, userB        uuid.UUID
}

func newVaultFixture(t *testing.T) *vaultFixture {
	t.Helper()
	f := newFixture(t)
	ctx := context.Background()

	// The base fixture's organization and user are A's; the repository is
	// seeded here because the ladder resolves from one.
	v := &vaultFixture{
		queries: gen.New(f.pool),
		orgA:    f.organizationID,
		userA:   f.userID,
		orgB:    f.otherOrgID,
		userB:   uuid.New(),
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'b','B')`,
		v.userB, v.orgB); err != nil {
		t.Fatalf("seed the other organization's user: %v", err)
	}
	v.repoA = seedRepository(t, f.pool, v.orgA, v.userA)
	v.pool = f.pool
	return v
}

// seedRepository writes a Product, a repository and the membership row in
// ONE transaction.
//
// repositories_primary_is_member_fkey is DEFERRABLE INITIALLY DEFERRED: the
// repository's primary Product must also be a member, and the membership row
// necessarily comes after the repository — so autocommitting each statement
// fires the check while it is still unsatisfied and the repository can never
// be written.
func seedRepository(t *testing.T, pool *pgxpool.Pool, org, user uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	product, repository := uuid.New(), uuid.New()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO products (product_id, organization_id, user_id, slug, display_name)
		  VALUES ($1,$2,$3,'vault-p','Vault P')`, []any{product, org, user}},
		{`INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name)
		  VALUES ($1,$2,$3,$4,'vault-r','Vault R')`, []any{repository, org, product, user}},
		{`INSERT INTO product_repositories (product_id, repository_id, organization_id)
		  VALUES ($1,$2,$3)`, []any{product, repository, org}},
	} {
		if _, err := tx.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatalf("seed repository: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit repository seed: %v", err)
	}
	return repository
}

func (v *vaultFixture) createShared(t *testing.T, actingUser uuid.UUID, org uuid.UUID, name string) error {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate secret id: %v", err)
	}
	_, err = v.queries.CreateSecret(context.Background(), gen.CreateSecretParams{
		SecretID:            toPgUUID(id),
		OrganizationID:      toPgUUID(org),
		Name:                name,
		ScopeType:           "organization",
		ScopeOrganizationID: toPgUUID(org),
		Scheme:              "aes-256-gcm/hkdf-sha256/v1",
		Nonce:               make([]byte, 12),
		Ciphertext:          make([]byte, 16),
		ActingUserID:        toPgUUID(actingUser),
	})
	return err
}

// TestSharedSecretDoesNotResolveForAnotherTenantsUser is the read half.
//
// Organization A holds a shared secret. A user of organization B asks for it
// while naming organization A — which the ownership predicate alone would
// allow, since a NULL owner matches any acting id.
func TestSharedSecretDoesNotResolveForAnotherTenantsUser(t *testing.T) {
	v := newVaultFixture(t)
	ctx := context.Background()

	if err := v.createShared(t, v.userA, v.orgA, "forge-token"); err != nil {
		t.Fatalf("create A's shared secret: %v", err)
	}

	// A's own user resolves it, so the case is not passing because the row
	// is unreachable for everybody.
	if _, err := v.queries.ResolveSecretForRepository(ctx, gen.ResolveSecretForRepositoryParams{
		OrganizationID: toPgUUID(v.orgA),
		Name:           "forge-token",
		ActingUserID:   toPgUUID(v.userA),
		RepositoryID:   toPgUUID(v.repoA),
	}); err != nil {
		t.Fatalf("the owning organization's user could not resolve its own shared secret: %v", err)
	}

	_, err := v.queries.ResolveSecretForRepository(ctx, gen.ResolveSecretForRepositoryParams{
		OrganizationID: toPgUUID(v.orgA),
		Name:           "forge-token",
		ActingUserID:   toPgUUID(v.userB),
		RepositoryID:   toPgUUID(v.repoA),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a user of another organization resolved A's shared secret (%v). A NULL owner "+
			"matches any acting id, so the ownership predicate alone does not bind the caller to "+
			"the organization being read.", err)
	}
}

// TestSharedSecretCannotBeCreatedInAnotherTenant is the write half, and the
// one with no foreign key behind it: an INDIVIDUAL secret is tenant-bound by
// the composite owner key, but a shared secret's owner is NULL, so nothing
// about the row mentions the caller.
func TestSharedSecretCannotBeCreatedInAnotherTenant(t *testing.T) {
	v := newVaultFixture(t)

	err := v.createShared(t, v.userB, v.orgA, "planted")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a user of organization B created a shared secret in organization A (%v); the "+
			"INSERT's membership check did not apply", err)
	}

	var planted int
	if err := v.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM secrets WHERE name = 'planted'`).Scan(&planted); err != nil {
		t.Fatalf("count planted secrets: %v", err)
	}
	if planted != 0 {
		t.Fatalf("%d rows were written despite the refusal", planted)
	}
}
