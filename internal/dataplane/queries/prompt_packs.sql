-- Prompt packs (ADR 0031; Phase 3 item 4 design, D4 and D6).
--
-- Content is inserted ON CONFLICT DO NOTHING, never DO UPDATE: the table's
-- anti-update trigger would fire on the very path that is supposed to be a
-- no-op. Both inserts follow InsertOrganizationIfAbsent's shape -- insert if
-- absent, then READ -- so the unique constraint arbitrates between two
-- installers of one identity and the read is what both compare against.

-- name: InsertPromptPackContentIfAbsent :execrows
INSERT INTO prompt_pack_contents (content_id, organization_id, scheme, digest, entries)
VALUES (@content_id, @organization_id, @scheme, @digest, @entries)
ON CONFLICT (organization_id, scheme, digest) DO NOTHING;

-- name: GetPromptPackContentByIdentity :one
SELECT * FROM prompt_pack_contents
WHERE organization_id = @organization_id
  AND scheme          = @scheme
  AND digest          = @digest;

-- name: GetPromptPackContent :one
SELECT * FROM prompt_pack_contents
WHERE organization_id = @organization_id
  AND content_id      = @content_id;

-- name: InsertPromptPackInstallationIfAbsent :execrows
INSERT INTO prompt_pack_installations (
    installation_id, organization_id, content_id, display_name,
    min_maestro_version, max_maestro_version, declared_roles,
    installed_by_kind, installed_by_user_id, builtin_maestro_version,
    validated_maestro_version, revision
) VALUES (
    @installation_id, @organization_id, @content_id, @display_name,
    @min_maestro_version, @max_maestro_version, @declared_roles,
    @installed_by_kind, @installed_by_user_id, @builtin_maestro_version,
    @validated_maestro_version, 1
)
ON CONFLICT (organization_id, content_id) DO NOTHING;

-- name: GetPromptPackInstallation :one
SELECT * FROM prompt_pack_installations
WHERE organization_id = @organization_id
  AND installation_id = @installation_id;

-- name: GetPromptPackInstallationByContent :one
SELECT * FROM prompt_pack_installations
WHERE organization_id = @organization_id
  AND content_id      = @content_id;

-- name: ListPromptPackInstallations :many
SELECT * FROM prompt_pack_installations
WHERE organization_id = @organization_id
ORDER BY created_at, installation_id;

-- Lock before updating, on the configuration family's pattern: a rowcount
-- carries no reason, so the seam locks, compares the revision in Go, and
-- writes conditionally.
-- name: LockPromptPackInstallation :one
SELECT * FROM prompt_pack_installations
WHERE organization_id = @organization_id
  AND installation_id = @installation_id
FOR UPDATE;

-- The next revision is derived HERE, never supplied: revision = revision + 1
-- under WHERE revision = expected is what "monotonic" means (design D6).
-- The validated version is written on every update because the gate ran
-- again; dispatch reads it to decide whether the harness has moved (D8).
-- name: UpdatePromptPackInstallation :one
UPDATE prompt_pack_installations
SET display_name              = @display_name,
    min_maestro_version       = @min_maestro_version,
    max_maestro_version       = @max_maestro_version,
    declared_roles            = @declared_roles,
    validated_maestro_version = @validated_maestro_version,
    revision                  = revision + 1,
    updated_at                = now()
WHERE organization_id = @organization_id
  AND installation_id = @installation_id
  AND revision        = @expected_revision
RETURNING *;
