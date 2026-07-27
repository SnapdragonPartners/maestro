-- Management artifacts (ADR 0021 lifecycle, ADR 0028 envelope encoding).
--
-- There is deliberately NO generic status update. Every status write below
-- is a named transition carrying its own preconditions, and a structural
-- test parses this file to fail the build if an un-named one appears
-- (design D4).
--
-- Every statement is organization-scoped, including the ones keyed on a
-- globally unique id. An artifact_id is unguessable but not a permission:
-- once this interface has a cloud implementation, a query that omits
-- organization_id is one a caller in the wrong tenant can serve, and the
-- schema's composite foreign keys already treat organization as part of
-- every artifact's identity.
--
-- Each transition's WHERE repeats every precondition the seam has already
-- checked against the locked row. That redundancy is intentional: it is the
-- backstop that stops a classification bug from writing a transition the
-- rules forbid. Zero rows affected there is an internal invariant failure,
-- not a user-facing outcome.

-- name: CreateManagementArtifact :one
INSERT INTO management_artifacts (
    artifact_id, organization_id, user_id,
    artifact_type, artifact_category, status, scope_type,
    scope_organization_id, scope_product_id, scope_feature_id,
    scope_epic_id, scope_story_id,
    product_id, feature_id, epic_id, story_id,
    author_instance_id, produced_by_tool_call_id,
    amends_artifact_id, supersedes_artifact_id, replaces_artifact_id,
    schema_version, summary, payload, payload_digest, review_digest
) VALUES (
    @artifact_id, @organization_id, @user_id,
    @artifact_type, @artifact_category, 'draft', @scope_type,
    @scope_organization_id, @scope_product_id, @scope_feature_id,
    @scope_epic_id, @scope_story_id,
    @product_id, @feature_id, @epic_id, @story_id,
    @author_instance_id, @produced_by_tool_call_id,
    @amends_artifact_id, @supersedes_artifact_id, @replaces_artifact_id,
    @schema_version, @summary, @payload, @payload_digest, @review_digest
)
RETURNING *;

-- name: GetManagementArtifact :one
SELECT * FROM management_artifacts
WHERE artifact_id     = @artifact_id
  AND organization_id = @organization_id;

-- Lock before any transition. A rowcount carries no reason, so the seam
-- locks the row, classifies the failure in Go against what it read, and
-- only then writes conditionally (design D5).
-- name: LockManagementArtifact :one
SELECT * FROM management_artifacts
WHERE artifact_id     = @artifact_id
  AND organization_id = @organization_id
FOR UPDATE;

-- name: ListManagementArtifactsByScope :many
SELECT * FROM management_artifacts
WHERE organization_id = @organization_id
  AND scope_type      = @scope_type
  AND scope_id        = @scope_id
ORDER BY created_at, artifact_id;

-- Lineage reads. Each takes the denormalised column rather than walking the
-- hierarchy, which is what the denormalisation is for.

-- name: ListManagementArtifactsByStory :many
SELECT * FROM management_artifacts
WHERE organization_id = @organization_id
  AND story_id        = @story_id
ORDER BY created_at, artifact_id;

-- name: ListManagementArtifactsByEpic :many
SELECT * FROM management_artifacts
WHERE organization_id = @organization_id
  AND epic_id         = @epic_id
ORDER BY created_at, artifact_id;

-- name: ListManagementArtifactsByProduct :many
SELECT * FROM management_artifacts
WHERE organization_id = @organization_id
  AND product_id      = @product_id
ORDER BY created_at, artifact_id;

-- Effective-view assembly (design D8): the original plus its ACCEPTED
-- amendments in sequence order. Draft and rejected amendments are never
-- applied, which is why this filters on status rather than assembling
-- everything and letting the caller choose.
-- name: ListAcceptedAmendments :many
SELECT * FROM management_artifacts
WHERE amends_artifact_id = @amends_artifact_id
  AND organization_id    = @organization_id
  AND status             = 'accepted'
ORDER BY amendment_sequence;

-- The next amendment sequence is one more than the maximum over every
-- non-null HISTORICAL sequence, whatever the amendment's current status
-- (design D6 step 5). D5 forbids superseding or archiving an amendment, so
-- accepted is the only status carrying a sequence today and this is
-- currently equivalent to filtering on it -- written as the historical
-- maximum so it stays correct if that matrix ever widens, rather than
-- silently reusing a number.
-- name: MaxAmendmentSequence :one
SELECT COALESCE(MAX(amendment_sequence), 0)::int AS max_sequence
FROM management_artifacts
WHERE amends_artifact_id = @amends_artifact_id
  AND organization_id    = @organization_id;

-- Transitions.
--
-- Accept joins the NAMED review and its reviewer, so every acceptance rule
-- that can be expressed in SQL is expressed here: the review belongs to
-- this artifact and organization, its decision is 'accepted', its digest
-- still matches the row's current review_digest, and the reviewer is a
-- non-author principal of kind agent or human in the same organization.
--
-- reviewer_instance_id is taken FROM the joined review rather than passed
-- in. A caller-supplied reviewer could disagree with the review actually
-- being acted on, and the row would then record a reviewer who never
-- reviewed it.
--
-- The review_digest equality is what makes ADR 0028's binding real: a
-- review of superseded content cannot license acceptance, because the row's
-- current review_digest no longer equals the reviewed one.

-- name: AcceptManagementArtifact :execrows
UPDATE management_artifacts a
SET status               = 'accepted',
    reviewer_instance_id = r.reviewer_instance_id,
    accepted_at          = now()
FROM artifact_reviews r
JOIN principal_instances p ON p.principal_instance_id = r.reviewer_instance_id
WHERE a.artifact_id     = @artifact_id
  AND a.organization_id = @organization_id
  AND a.status          = 'draft'
  AND a.is_amendment    = false
  AND r.review_id       = @review_id
  AND r.artifact_id     = a.artifact_id
  AND r.organization_id = a.organization_id
  AND r.decision        = 'accepted'
  AND r.review_digest   = a.review_digest
  AND p.organization_id = a.organization_id
  AND p.principal_instance_id <> a.author_instance_id
  AND p.kind IN ('agent', 'human');

-- Accept an AMENDMENT, assigning its sequence in the same statement. The
-- sequence is assigned on acceptance and retained thereafter: without a
-- stored total order the effective view is undefined.
--
-- Carries the same review preconditions as an original, plus the base
-- checks the seam performs under the original's lock (design D6), which
-- cannot be expressed here because they compare against an assembled
-- effective view rather than against stored columns.
-- name: AcceptManagementAmendment :execrows
UPDATE management_artifacts a
SET status               = 'accepted',
    reviewer_instance_id = r.reviewer_instance_id,
    accepted_at          = now(),
    amendment_sequence   = @amendment_sequence
FROM artifact_reviews r
JOIN principal_instances p ON p.principal_instance_id = r.reviewer_instance_id
WHERE a.artifact_id        = @artifact_id
  AND a.organization_id    = @organization_id
  AND a.status             = 'draft'
  AND a.is_amendment       = true
  AND a.amends_artifact_id = @amends_artifact_id
  AND r.review_id          = @review_id
  AND r.artifact_id        = a.artifact_id
  AND r.organization_id    = a.organization_id
  AND r.decision           = 'accepted'
  AND r.review_digest      = a.review_digest
  AND p.organization_id    = a.organization_id
  AND p.principal_instance_id <> a.author_instance_id
  AND p.kind IN ('agent', 'human');

-- Invalidation is pre-acceptance by definition (ADR 0021), so draft is the
-- only source status and there are no further preconditions.
-- name: InvalidateManagementArtifact :execrows
UPDATE management_artifacts
SET status = 'invalidated'
WHERE artifact_id     = @artifact_id
  AND organization_id = @organization_id
  AND status          = 'draft';

-- Supersession verifies the SUPERSEDING artifact's reviewed link. Without
-- that check, an artifact reviewed and accepted as superseding A could be
-- used to retire B -- the reviewer approved a replacement for one thing and
-- it retired another.
--
-- The superseding artifact must already be accepted, which the seam does
-- first in the same transaction (design D5): a reader between the two
-- statements would otherwise observe two authoritative artifacts for the
-- same subject.
--
-- Amendments can be neither superseded nor archived, hence is_amendment =
-- false on both. Effective-view assembly loads only accepted amendments, so
-- archiving one would silently drop its contribution from the effective
-- view of an artifact nobody re-reviewed -- mutating accepted content
-- through a lifecycle side door.
-- name: SupersedeManagementArtifact :execrows
UPDATE management_artifacts target
SET status = 'superseded'
FROM management_artifacts superseding
WHERE target.artifact_id     = @artifact_id
  AND target.organization_id = @organization_id
  AND target.status          = 'accepted'
  AND target.is_amendment    = false
  AND superseding.artifact_id            = @superseding_artifact_id
  AND superseding.organization_id        = target.organization_id
  AND superseding.supersedes_artifact_id = target.artifact_id
  AND superseding.status                 = 'accepted'
  AND superseding.is_amendment           = false;

-- name: ArchiveManagementArtifact :execrows
UPDATE management_artifacts
SET status = 'archived'
WHERE artifact_id     = @artifact_id
  AND organization_id = @organization_id
  AND status          IN ('accepted', 'superseded')
  AND is_amendment    = false;
