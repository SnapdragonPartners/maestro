-- Artifact review records (ADR 0021 review records, ADR 0028 digest binding).
--
-- A review stores what the reviewer SAW: review_digest, and for an
-- amendment the base_digest and base_sequence, exactly as observed. The
-- seam never recomputes current values when recording a decision (design
-- D3a) -- doing so would bind the review to content the reviewer never saw,
-- manufacturing the false attestation the digest binding exists to prevent.

-- name: CreateArtifactReview :one
INSERT INTO artifact_reviews (
    review_id, organization_id, artifact_id,
    review_digest, base_digest, base_sequence,
    reviewer_instance_id, decision, rationale
) VALUES (
    @review_id, @organization_id, @artifact_id,
    @review_digest, @base_digest, @base_sequence,
    @reviewer_instance_id, @decision, @rationale
)
RETURNING *;

-- name: GetArtifactReview :one
SELECT * FROM artifact_reviews
WHERE review_id       = @review_id
  AND organization_id = @organization_id;

-- name: ListArtifactReviews :many
SELECT * FROM artifact_reviews
WHERE artifact_id     = @artifact_id
  AND organization_id = @organization_id
ORDER BY decided_at, review_id;

-- Used by the accept transition to classify in Go. It fetches the named
-- review joined to its reviewer AND to the artifact's author, because the
-- acceptance rules turn on both.
--
-- The author's identity is here because ADR 0020's invariant is about the
-- PRINCIPAL, not the instance: "even the human operator does not self-review
-- -- a human may be an artifact's author or its approver, never both". A
-- principal instance is one lifetime, so the same human running a command
-- twice has two of them, and comparing instance ids alone let that human
-- author with one and accept with the other.
--
-- Joined here rather than read separately, though NOT because the artifact
-- row lock protects these columns -- it does not; a joined
-- principal_instances row is outside it. They are safe because a principal's
-- kind and user ownership are immutable for the instance's life, so there is
-- no later value for a second read to observe. One statement is for one round
-- trip and one obvious place to look.
-- name: GetArtifactReviewWithReviewer :one
SELECT
    sqlc.embed(r),
    p.kind            AS reviewer_kind,
    p.organization_id AS reviewer_organization_id,
    p.user_id         AS reviewer_user_id,
    author.kind       AS author_kind,
    author.user_id    AS author_user_id
FROM artifact_reviews r
JOIN principal_instances p ON p.principal_instance_id = r.reviewer_instance_id
JOIN management_artifacts a ON a.artifact_id = r.artifact_id
                           AND a.organization_id = r.organization_id
JOIN principal_instances author ON author.principal_instance_id = a.author_instance_id
                               AND author.organization_id = a.organization_id
WHERE r.review_id       = @review_id
  AND r.organization_id = @organization_id;
