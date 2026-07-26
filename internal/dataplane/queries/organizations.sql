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
