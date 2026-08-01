-- Integrity verification (Phase 2 item 8).
--
-- These statements serve `dataplane-verify`, which recomputes every stored
-- digest after a restore. A restore's characteristic failure is a TORN PAIR
-- — a Postgres cluster and an object store copied at moments that disagree —
-- and neither store can detect that alone, so the check has to walk both.
--
-- ON TENANCY. The rest of this directory is organization-scoped by rule: a
-- statement without organization_id is one a caller in the wrong tenant can
-- serve. Verification is plane-wide by nature, and the temptation is an
-- unscoped `SELECT * FROM management_artifacts`. That temptation is refused
-- here. ListOrganizations is the ONLY unscoped statement — an organization
-- list is the scope vocabulary itself, not data inside a scope — and every
-- statement below it is organization-scoped exactly like its neighbours.
-- Verification therefore iterates organizations rather than reaching across
-- them, and the tenancy rule survives an operator-level tool intact.
--
-- ON BOUNDS. These are unbounded reads, which the accepted design permits at
-- Phase 2 scale and flags in the exit record as needing keyset bounds before
-- the plane holds real volume. They are deliberately NOT modelled on the
-- call family's paged reads: a verification that skipped a page would report
-- a healthy plane it had not finished checking, which is worse than a slow
-- one.

-- ListOrganizations enumerates the scopes verification walks.
--
-- Ordered so two runs over an unchanged plane report in the same order,
-- which is what makes a diff between two verification runs meaningful.
--
-- name: ListOrganizations :many
SELECT * FROM organizations
ORDER BY organization_id;

-- ListManagementArtifactsForVerify returns one organization's Management
-- artifacts, with every column the review projection is rebuilt from.
--
-- SELECT * rather than a projection on purpose: review_digest is computed
-- over a structure assembled from many columns, and a hand-listed set here
-- would silently stop covering a column added later — the digest would then
-- verify against a projection missing a field it was originally built with.
--
-- name: ListManagementArtifactsForVerify :many
SELECT * FROM management_artifacts
WHERE organization_id = @organization_id
ORDER BY artifact_id;

-- ListAuditArtifactsForVerify returns one organization's Audit artifacts.
-- This family has no review_digest by design (ADR 0021: exhaust, born
-- final), so only payload_digest is recomputed for it.
--
-- name: ListAuditArtifactsForVerify :many
SELECT * FROM audit_artifacts
WHERE organization_id = @organization_id
ORDER BY artifact_id;

-- ListBinaryAttachmentsForVerify returns EVERY attachment row in one
-- organization.
--
-- Every row, not the subset reachable from artifacts: "referenced" is
-- ambiguous, and a row nothing currently points at is still a row whose
-- blob has to exist for the plane to be internally consistent. Narrowing
-- this to reachable rows would silently shrink what verification covers.
--
-- name: ListBinaryAttachmentsForVerify :many
SELECT * FROM binary_attachments
WHERE organization_id = @organization_id
ORDER BY attachment_id;
