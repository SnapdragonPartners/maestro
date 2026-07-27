-- Audit artifacts: the exhaust (ADR 0021).
--
-- Born final. No status, no reviews, no amendments, no supersession -- so
-- this file has writes and reads and NO transitions. That asymmetry with
-- management_artifacts.sql is the point: a lifecycle statement here would
-- have nothing to move.
--
-- Organization-scoped throughout, for the same reason as the Management
-- family.

-- name: CreateAuditArtifact :one
INSERT INTO audit_artifacts (
    artifact_id, organization_id, user_id,
    artifact_type, artifact_category, scope_type,
    scope_organization_id, scope_product_id, scope_feature_id,
    scope_epic_id, scope_story_id,
    product_id, feature_id, epic_id, story_id,
    author_instance_id, produced_by_tool_call_id,
    schema_version, summary, payload, payload_digest
) VALUES (
    @artifact_id, @organization_id, @user_id,
    @artifact_type, @artifact_category, @scope_type,
    @scope_organization_id, @scope_product_id, @scope_feature_id,
    @scope_epic_id, @scope_story_id,
    @product_id, @feature_id, @epic_id, @story_id,
    @author_instance_id, @produced_by_tool_call_id,
    @schema_version, @summary, @payload, @payload_digest
)
RETURNING *;

-- name: GetAuditArtifact :one
SELECT * FROM audit_artifacts
WHERE artifact_id     = @artifact_id
  AND organization_id = @organization_id;

-- name: ListAuditArtifactsByScope :many
SELECT * FROM audit_artifacts
WHERE organization_id = @organization_id
  AND scope_type      = @scope_type
  AND scope_id        = @scope_id
ORDER BY created_at, artifact_id;

-- name: ListAuditArtifactsByStory :many
SELECT * FROM audit_artifacts
WHERE organization_id = @organization_id
  AND story_id        = @story_id
ORDER BY created_at, artifact_id;

-- name: ListAuditArtifactsByType :many
SELECT * FROM audit_artifacts
WHERE organization_id = @organization_id
  AND artifact_type   = @artifact_type
ORDER BY created_at, artifact_id;
