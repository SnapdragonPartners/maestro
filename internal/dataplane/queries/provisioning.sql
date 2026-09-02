-- Provisioning beyond the tenant: products and repositories (Phase 3
-- item 3, design D11). The organization and user queries stay in
-- organizations.sql, where item 9 put them.
--
-- Same shape as InsertOrganizationIfAbsent: INSERT ... ON CONFLICT DO
-- NOTHING, then READ. The unique constraint is the arbiter between two
-- operators provisioning the same slug at once, and the read that follows
-- is what both compare against.

-- name: GetProductBySlug :one
SELECT product_id, organization_id, user_id, slug, display_name, created_at
FROM products
WHERE organization_id = $1 AND slug = $2;

-- name: InsertProductIfAbsent :execrows
INSERT INTO products (product_id, organization_id, user_id, slug, display_name)
VALUES (@product_id, @organization_id, @user_id, @slug, @display_name)
ON CONFLICT ON CONSTRAINT products_org_slug_key DO NOTHING;

-- name: GetRepositoryBySlug :one
SELECT repository_id, organization_id, primary_product_id, user_id, slug, display_name, created_at
FROM repositories
WHERE organization_id = $1 AND slug = $2;

-- The repository row and its primary membership are inserted in ONE
-- transaction by the seam: repositories_primary_is_member_fkey is
-- DEFERRABLE INITIALLY DEFERRED and fires at commit, so a repository whose
-- primary Product is not also a member cannot be committed at all.

-- name: InsertRepositoryIfAbsent :execrows
INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name)
VALUES (@repository_id, @organization_id, @primary_product_id, @user_id, @slug, @display_name)
ON CONFLICT ON CONSTRAINT repositories_org_slug_key DO NOTHING;

-- name: InsertProductRepositoryIfAbsent :execrows
INSERT INTO product_repositories (product_id, repository_id, organization_id)
VALUES (@product_id, @repository_id, @organization_id)
ON CONFLICT ON CONSTRAINT product_repositories_pkey DO NOTHING;

-- name: ListRepositoryProducts :many
SELECT product_id
FROM product_repositories
WHERE organization_id = $1 AND repository_id = $2
ORDER BY product_id;

-- name: GetRepositoryByID :one
SELECT repository_id, organization_id, primary_product_id, user_id, slug, display_name, created_at
FROM repositories
WHERE organization_id = $1 AND repository_id = $2;
