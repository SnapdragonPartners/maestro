-- Typed queries are item 4's deliverable. This one exists in item 3 for a
-- narrower reason: sqlc will not generate from an empty queries directory,
-- so without at least one query the configuration beside it is unverified —
-- and an unverified config is one nobody has run against the schema.
--
-- It is a real query rather than a placeholder. Resolving the default
-- organization is the first thing any consumer does, including item 9's
-- benchmark import, so item 4 extends this file rather than replacing it.

-- name: GetOrganizationBySlug :one
SELECT organization_id, slug, display_name, created_at
FROM organizations
WHERE slug = $1;

-- name: GetUserByHandle :one
SELECT user_id, organization_id, handle, display_name, created_at
FROM users
WHERE organization_id = $1 AND handle = $2;

-- Provisioning. Item 9 adds it because nothing else could: an importer goes
-- through the seam, and until now the seam could resolve an organization and
-- a user but never create one, so every integration test wrote its own with
-- raw SQL and the importer had no supported path at all.
--
-- INSERT ... ON CONFLICT DO NOTHING, then READ, rather than a
-- check-then-insert. Two operators running bootstrap at once would both see
-- no row and both insert, and one of them would receive a raw uniqueness
-- violation -- which is neither "created" nor "already existed" and leaks a
-- driver error through the seam. Here the unique constraint is the arbiter
-- and the read that follows is what both callers compare against.

-- name: InsertOrganizationIfAbsent :execrows
INSERT INTO organizations (organization_id, slug, display_name)
VALUES (@organization_id, @slug, @display_name)
ON CONFLICT ON CONSTRAINT organizations_slug_key DO NOTHING;

-- name: InsertUserIfAbsent :execrows
INSERT INTO users (user_id, organization_id, handle, display_name)
VALUES (@user_id, @organization_id, @handle, @display_name)
ON CONFLICT ON CONSTRAINT users_org_handle_key DO NOTHING;
