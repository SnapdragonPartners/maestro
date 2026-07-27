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
WHERE review_id = @review_id;

-- name: ListArtifactReviews :many
SELECT * FROM artifact_reviews
WHERE artifact_id = @artifact_id
ORDER BY decided_at, review_id;

-- Used by the accept transition to classify in Go. It fetches the named
-- review joined to its reviewer, because the acceptance rules turn on the
-- reviewer's kind and identity (not the author, kind agent or human) and
-- fetching them separately would leave a window where the two disagree.
-- name: GetArtifactReviewWithReviewer :one
SELECT
    sqlc.embed(r),
    p.kind            AS reviewer_kind,
    p.organization_id AS reviewer_organization_id
FROM artifact_reviews r
JOIN principal_instances p ON p.principal_instance_id = r.reviewer_instance_id
WHERE r.review_id = @review_id;
